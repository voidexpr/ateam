package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Live status table of running agents",
	Long: `Show a live, top-like status table of running agent processes.

Runs are discovered from the call database (any exec without an end time)
and progress — turns, tokens, context size, current tool — is read from
each run's stream log on disk. top can therefore attach to runs started
from other terminals at any point in their lifetime. New runs appear as
they start; finished rows keep their final status. Press Ctrl-C to quit.

` + progressColumnsHelp("run"),
	Args: cobra.NoArgs,
	RunE: runTop,
}

func runTop(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return err
	}

	db, err := requireStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	if !isTerminal() {
		return fmt.Errorf("ateam top needs a terminal; use 'ateam ps' for a one-shot snapshot")
	}

	ctx, stop := cmdContext()
	defer stop()

	view := &topView{env: env, db: db, index: map[int64]int{}}
	fmt.Println("Watching for running agents (Ctrl-C to quit)")
	renderer := newPoolRenderer(os.Stdout)
	defer renderer.Close()

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var lastDBPoll time.Time

	for {
		if time.Since(lastDBPoll) >= 2*time.Second {
			lastDBPoll = time.Now()
			if err := view.refreshFromDB(); err != nil {
				return err
			}
		}
		view.pollStreams()
		renderer.Render(view.rows)

		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// topEntry carries the per-run state behind one poolStatusRow.
type topEntry struct {
	id        int64
	scanner   *runner.ProgressScanner
	startedAt time.Time
	deadPolls int // consecutive DB polls with ended_at NULL and a dead PID
	finalized bool
}

// topView accumulates discovered runs and keeps rows/entries parallel.
type topView struct {
	env     *root.ResolvedEnv
	db      *calldb.CallDB
	index   map[int64]int // exec id -> row index
	rows    []poolStatusRow
	entries []*topEntry
}

// refreshFromDB discovers newly started runs and finalizes rows whose run
// ended (or whose process died without writing an end timestamp).
func (t *topView) refreshFromDB() error {
	running, err := t.db.FindRunning(t.env.ProjectID(), "")
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	live := make(map[int64]bool, len(running))
	var newIDs []int64
	for _, r := range running {
		alive := r.PID > 0 && isProcessAlive(r.PID)
		live[r.ID] = alive
		if _, known := t.index[r.ID]; !known && alive {
			newIDs = append(newIDs, r.ID)
		}
	}

	if len(newIDs) > 0 {
		calls, err := t.db.CallsByIDs(newIDs)
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}
		for _, c := range calls {
			t.addRun(c)
		}
	}

	for i, e := range t.entries {
		if e.finalized {
			continue
		}
		alive, stillRunning := live[e.id]
		switch {
		case stillRunning && alive:
			e.deadPolls = 0
		case stillRunning && !alive:
			// The process is gone but ended_at is still NULL. Right after
			// exit that's a normal race (the runner writes the end row a
			// moment later); only after it persists across polls is it the
			// orphaned state ps reports as "canceled".
			e.deadPolls++
			if e.deadPolls >= 2 {
				e.finalized = true
				t.rows[i].State = poolStateCanceled
			}
		default:
			t.finalize(i)
		}
	}
	return nil
}

func (t *topView) addRun(c calldb.CallRow) {
	t.index[c.ID] = len(t.rows)
	started, _ := time.Parse(time.RFC3339, c.StartedAt)
	var streamPath string
	if c.AgentFile != "" {
		streamPath = root.ResolveStreamPath(t.env.ProjectDir, t.env.OrgDir, c.AgentFile)
	}
	t.rows = append(t.rows, poolStatusRow{
		ExecID: c.ID,
		Label:  c.Role + "/" + c.Action,
		State:  poolStateRunning,
	})
	t.entries = append(t.entries, &topEntry{
		id:        c.ID,
		scanner:   runner.NewProgressScanner(streamPath),
		startedAt: started,
	})
}

// pollStreams drains each live run's stream file and refreshes its row
// through the same per-event row transition the pool table uses.
func (t *topView) pollStreams() {
	now := time.Now()
	for i, e := range t.entries {
		if e.finalized {
			continue
		}
		e.scanner.Poll()
		var elapsed time.Duration
		if !e.startedAt.IsZero() {
			elapsed = now.Sub(e.startedAt)
		}
		t.rows[i] = nextPoolStatusRow(t.rows[i], e.scanner.Progress(e.id, elapsed))
	}
}

// finalize marks row i terminal using the authoritative DB row.
func (t *topView) finalize(i int) {
	e := t.entries[i]
	row, err := t.db.GetRunByID(e.id)
	if err != nil || row == nil {
		return // not finalized; retried on the next DB poll
	}
	e.finalized = true
	e.scanner.Poll() // drain the stream tail so tool/turn counts are final
	summary := summaryFromRecentRow(*row, e.scanner.Path)
	if row.IsError {
		cwd, _ := os.Getwd()
		t.rows[i] = errorPoolStatusRow(t.rows[i], summary, cwd)
	} else {
		t.rows[i] = donePoolStatusRow(t.rows[i], summary, "")
	}
}

// summaryFromRecentRow adapts a completed agent_execs row into the
// RunSummary shape the pool row formatters consume.
func summaryFromRecentRow(r calldb.RecentRow, streamPath string) runner.RunSummary {
	s := runner.RunSummary{
		ExecID:            r.ID,
		Cost:              r.CostUSD,
		Turns:             r.Turns,
		IsError:           r.IsError,
		InputTokens:       r.InputTokens,
		OutputTokens:      r.OutputTokens,
		CacheReadTokens:   r.CacheReadTokens,
		CacheWriteTokens:  r.CacheWriteTokens,
		PeakContextTokens: r.PeakContextTokens,
		ContextWindow:     r.ContextWindow,
		Duration:          time.Duration(r.DurationMS) * time.Millisecond,
		AgentFilePath:     streamPath,
	}
	if ended, err := time.Parse(time.RFC3339, r.EndedAt); err == nil {
		s.EndedAt = ended
	}
	return s
}
