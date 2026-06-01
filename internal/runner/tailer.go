package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// TailSource tracks a single stream file being tailed.
type TailSource struct {
	ID        int64
	AgentFile string
	Formatter *StreamFormatter
	offset    int64
	partial   []byte // incomplete last line
	done      bool
	emitted   bool // final-message JSONL line already written
}

// Tailer multiplexes live streaming from one or more JSONL stream files.
type Tailer struct {
	Writer       io.Writer
	PollInterval time.Duration // default 500ms
	DB           *calldb.CallDB
	ProjectDir   string // .ateam/ dir, used to resolve relative stream_file paths (new layout)
	OrgDir       string // .ateamorg/ dir, used to resolve legacy relative stream_file paths
	Batch        string // discover new agent_execs joining this batch
	ProjectID    string // for finding running calls (legacy, empty for per-project DB)
	DiscoverAll  bool   // discover all running calls in the DB (per-project mode)
	Action       string // "report" or "" — for --reports mode
	Color        bool
	Verbose      bool
	Pricing      agent.PricingTable // cost estimation table forwarded to formatters
	DefaultModel string             // fallback model for pricing lookup
	WaitTimeout  time.Duration      // how long to wait for first source (default 30s)
	// FinalMessageOnly suppresses the formatted stream output and instead
	// writes one JSONL line to Writer per source as it completes. The line
	// carries the call's metadata plus the final assistant message text
	// under "final_message". Designed for pipelining parallel runs.
	FinalMessageOnly bool
	// AnyError is set to true after Run returns when any tailed source
	// ended with is_error=true. Callers use it to choose a non-zero exit.
	AnyError bool
	sources  []*TailSource
	knownIDs map[int64]bool
}

// NewTailer creates a Tailer with sensible defaults.
func NewTailer(w io.Writer, db *calldb.CallDB, color, verbose bool) *Tailer {
	return &Tailer{
		Writer:       w,
		PollInterval: 500 * time.Millisecond,
		DB:           db,
		Color:        color,
		Verbose:      verbose,
		WaitTimeout:  30 * time.Second,
		knownIDs:     make(map[int64]bool),
	}
}

// AddSource registers a stream file to tail.
func (t *Tailer) AddSource(id int64, role, action, agentFile, model string) {
	if t.knownIDs[id] {
		return
	}
	t.knownIDs[id] = true
	label := role + "/" + action
	var prefix string
	if t.Color {
		prefix = fmt.Sprintf("\033[36m[%d:%s]\033[0m ", id, label)
	} else {
		prefix = fmt.Sprintf("[%d:%s] ", id, label)
	}
	t.sources = append(t.sources, &TailSource{
		ID:        id,
		AgentFile: agentFile,
		Formatter: &StreamFormatter{
			Verbose:      t.Verbose,
			Color:        t.Color,
			Model:        model,
			DefaultModel: t.DefaultModel,
			Pricing:      t.Pricing,
			Prefix:       prefix,
		},
	})
}

// Run polls stream files and DB, writing formatted output to Writer.
// It blocks until all sources are done or ctx is cancelled.
func (t *Tailer) Run(ctx context.Context) error {
	discoveryMode := t.Batch != "" || t.ProjectID != "" || t.DiscoverAll
	pollTick := time.NewTicker(t.PollInterval)
	defer pollTick.Stop()

	dbPollInterval := 2 * time.Second
	lastDBPoll := time.Time{}

	// Wait for first source if none exist yet
	if len(t.sources) == 0 {
		if err := t.waitForSources(ctx); err != nil {
			return err
		}
		if len(t.sources) == 0 {
			return nil
		}
	}

	lastNewSource := time.Now()

	for {
		select {
		case <-ctx.Done():
			// Final drain before exit
			t.pollFiles()
			return nil
		case <-pollTick.C:
		}

		t.pollFiles()

		// DB discovery
		if discoveryMode && time.Since(lastDBPoll) >= dbPollInterval {
			lastDBPoll = time.Now()
			prevCount := len(t.sources)
			t.discoverSources()
			if len(t.sources) > prevCount {
				lastNewSource = time.Now()
			}
			t.checkDone()
		}

		if t.allDone() {
			if discoveryMode {
				// Wait a bit after last discovery to catch late arrivals
				if time.Since(lastNewSource) > 3*time.Second {
					t.pollFiles() // final drain
					return nil
				}
			} else {
				return nil
			}
		}
	}
}

func (t *Tailer) waitForSources(ctx context.Context) error {
	deadline := time.After(t.WaitTimeout)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		t.discoverSources()
		if len(t.sources) > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			fmt.Fprintln(t.Writer, "No running processes found.")
			return nil
		case <-tick.C:
		}
	}
}

func (t *Tailer) discoverSources() {
	if t.DB == nil {
		return
	}

	if t.Batch != "" {
		rows, err := t.DB.CallsByBatch(t.Batch)
		if err != nil {
			return
		}
		for _, r := range rows {
			if r.AgentFile != "" {
				t.AddSource(r.ID, r.Role, r.Action, root.ResolveStreamPath(t.ProjectDir, t.OrgDir, r.AgentFile), r.Model)
			}
		}
	}

	if t.ProjectID != "" || t.DiscoverAll {
		rows, err := t.DB.FindRunning(t.ProjectID, t.Action)
		if err != nil {
			return
		}
		var newIDs []int64
		roleByID := map[int64]string{}
		actionByID := map[int64]string{}
		for _, r := range rows {
			if t.knownIDs[r.ID] {
				continue
			}
			newIDs = append(newIDs, r.ID)
			roleByID[r.ID] = r.Role
			actionByID[r.ID] = r.Action
		}
		if len(newIDs) == 0 {
			return
		}
		calls, err := t.DB.CallsByIDs(newIDs)
		if err != nil {
			return
		}
		for _, c := range calls {
			if c.AgentFile != "" {
				role := roleByID[c.ID]
				if role == "" {
					role = c.Role
				}
				action := actionByID[c.ID]
				if action == "" {
					action = c.Action
				}
				t.AddSource(c.ID, role, action, root.ResolveStreamPath(t.ProjectDir, t.OrgDir, c.AgentFile), c.Model)
			}
		}
	}
}

func (t *Tailer) pollFiles() {
	for _, src := range t.sources {
		if src.done {
			continue
		}
		t.pollSource(src)
	}
}

func (t *Tailer) pollSource(src *TailSource) {
	f, err := os.Open(src.AgentFile)
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() <= src.offset {
		return
	}

	if src.offset > 0 {
		if _, err := f.Seek(src.offset, io.SeekStart); err != nil {
			return
		}
	}

	buf := make([]byte, info.Size()-src.offset)
	n, _ := f.Read(buf)
	if n == 0 {
		return
	}
	buf = buf[:n]
	src.offset += int64(n)

	// Prepend any partial line from last read
	if len(src.partial) > 0 {
		buf = append(src.partial, buf...)
		src.partial = nil
	}

	// Split into lines, saving incomplete trailing data
	lines, remainder := splitLines(buf)
	if len(remainder) > 0 {
		src.partial = make([]byte, len(remainder))
		copy(src.partial, remainder)
	}
	for _, line := range lines {
		out := src.Formatter.FormatLine(line)
		if out != "" && !t.FinalMessageOnly {
			fmt.Fprint(t.Writer, out)
		}
	}

	if src.Formatter.HasResult() {
		src.done = true
		t.maybeEmitFinal(src)
	}
}

// splitLines splits buf on newlines. Returns complete lines and any
// trailing partial data (bytes after the last newline).
func splitLines(buf []byte) (lines [][]byte, remainder []byte) {
	for len(buf) > 0 {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			remainder = buf
			break
		}
		lines = append(lines, buf[:idx])
		buf = buf[idx+1:]
	}
	return
}

func (t *Tailer) checkDone() {
	if t.DB == nil {
		return
	}
	var ids []int64
	idxMap := map[int64]*TailSource{}
	for _, src := range t.sources {
		if src.done {
			continue
		}
		ids = append(ids, src.ID)
		idxMap[src.ID] = src
	}
	if len(ids) == 0 {
		return
	}
	calls, err := t.DB.CallsByIDs(ids)
	if err != nil {
		return
	}
	for _, c := range calls {
		if c.EndedAt != "" {
			if src := idxMap[c.ID]; src != nil {
				t.pollSource(src)
				src.done = true
				t.maybeEmitFinal(src)
			}
		}
	}
}

// maybeEmitFinal writes one JSONL line for src to Writer when running in
// FinalMessageOnly mode. It is idempotent per source (guarded by emitted)
// and updates AnyError when the run ended with is_error=true.
func (t *Tailer) maybeEmitFinal(src *TailSource) {
	if !t.FinalMessageOnly || src.emitted || t.DB == nil {
		return
	}
	row, err := t.DB.GetRunByID(src.ID)
	if err != nil || row == nil {
		return
	}
	src.emitted = true
	if row.IsError {
		t.AnyError = true
	}
	rec := map[string]any{
		"exec_id":            row.ID,
		"agent":              row.Agent,
		"model":              row.Model,
		"role":               row.Role,
		"action":             row.Action,
		"batch":              row.Batch,
		"started_at":         row.StartedAt,
		"ended_at":           row.EndedAt,
		"duration_ms":        row.DurationMS,
		"is_error":           row.IsError,
		"exit_code":          row.ExitCode,
		"error_message":      row.ErrorMessage,
		"cost_usd":           row.CostUSD,
		"input_tokens":       row.InputTokens,
		"output_tokens":      row.OutputTokens,
		"cache_read_tokens":  row.CacheReadTokens,
		"cache_write_tokens": row.CacheWriteTokens,
		"turns":              row.Turns,
		"final_message":      scanAgentFileForFinalText(src.AgentFile),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fmt.Fprintln(t.Writer, string(line))
}

func (t *Tailer) allDone() bool {
	if len(t.sources) == 0 {
		return false
	}
	for _, src := range t.sources {
		if !src.done {
			return false
		}
	}
	return true
}
