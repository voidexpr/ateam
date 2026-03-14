package cmd

import (
	"fmt"
	"time"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Show aggregated cost reports from the call database",
	Long: `Display aggregated cost and token usage, grouped by action type
and by code session.

When run inside a project, results are filtered to that project by default.
Use --project to filter explicitly.

Example:
  ateam cost
  ateam cost --project myproject`,
	Args: cobra.NoArgs,
	RunE: runCost,
}

func runCost(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	db := openCallDB(env.OrgDir)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	projectID := ""
	if projectFlag != "" {
		projectID = projectFlag
	} else if env.ProjectDir != "" {
		projectID = env.ProjectID()
	}

	if projectID != "" {
		fmt.Printf("Project: %s\n\n", projectID)
	}

	// --- All-time aggregation by action category ---
	actionAggs, err := db.CostByAction(projectID)
	if err != nil {
		return fmt.Errorf("action aggregation query failed: %w", err)
	}

	fmt.Println("=== Cost by Action (all time) ===")
	if len(actionAggs) == 0 {
		fmt.Println("No data.")
	} else {
		w := newTable()
		fmt.Fprintln(w, "ACTION\tCOUNT\tCOST\tINPUT\tOUTPUT\tCACHE_READ\tTOTAL_TOKENS")
		var totalCost float64
		var totalTokens int64
		for _, a := range actionAggs {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
				a.Category, a.Count, fmtCost(a.CostUSD),
				fmtTokens64(a.InputTokens), fmtTokens64(a.OutputTokens),
				fmtTokens64(a.CacheReadTokens), fmtTokens64(a.TotalTokens))
			totalCost += a.CostUSD
			totalTokens += a.TotalTokens
		}
		fmt.Fprintf(w, "TOTAL\t\t%s\t\t\t\t%s\n",
			fmtCost(totalCost), fmtTokens64(totalTokens))
		w.Flush()
	}

	// --- Code session breakdown ---
	sessionRows, err := db.CostByCodeSession(projectID)
	if err != nil {
		return fmt.Errorf("code session query failed: %w", err)
	}

	if len(sessionRows) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("=== Cost by Code Session ===")

	type sessionSummary struct {
		taskGroup    string
		actions      map[string]actionLine
		totalCost    float64
		totalTokens  int64
		firstStarted string
		lastEnded    string
	}
	sessions := make(map[string]*sessionSummary)
	var order []string

	for _, r := range sessionRows {
		s, ok := sessions[r.TaskGroup]
		if !ok {
			s = &sessionSummary{
				taskGroup: r.TaskGroup,
				actions:   make(map[string]actionLine),
			}
			sessions[r.TaskGroup] = s
			order = append(order, r.TaskGroup)
		}
		s.actions[r.Action] = actionLine{
			count:       r.Count,
			cost:        r.CostUSD,
			inputTok:    r.InputTokens,
			outputTok:   r.OutputTokens,
			cacheReadTok: r.CacheReadTokens,
			totalTok:    r.TotalTokens,
		}
		s.totalCost += r.CostUSD
		s.totalTokens += r.TotalTokens
		if s.firstStarted == "" || r.FirstStarted < s.firstStarted {
			s.firstStarted = r.FirstStarted
		}
		if r.LastEnded.Valid && (s.lastEnded == "" || r.LastEnded.String > s.lastEnded) {
			s.lastEnded = r.LastEnded.String
		}
	}

	w := newTable()
	fmt.Fprintln(w, "TASK_GROUP\tACTION\tCOUNT\tCOST\tINPUT\tOUTPUT\tCACHE_READ\tTOTAL_TOKENS\tSTARTED\tENDED\tDURATION")

	for _, tg := range order {
		s := sessions[tg]
		started := fmtTimestamp(s.firstStarted)
		ended := fmtTimestamp(s.lastEnded)
		dur := computeDuration(s.firstStarted, s.lastEnded)

		for _, action := range []string{"code", "run"} {
			al, ok := s.actions[action]
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tg, action, al.count, fmtCost(al.cost),
				fmtTokens64(al.inputTok), fmtTokens64(al.outputTok),
				fmtTokens64(al.cacheReadTok), fmtTokens64(al.totalTok),
				started, ended, dur)
		}
		// Print any other actions not covered above.
		for action, al := range s.actions {
			if action == "code" || action == "run" {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tg, action, al.count, fmtCost(al.cost),
				fmtTokens64(al.inputTok), fmtTokens64(al.outputTok),
				fmtTokens64(al.cacheReadTok), fmtTokens64(al.totalTok),
				started, ended, dur)
		}

		// Session total row.
		fmt.Fprintf(w, "%s\tTOTAL\t\t%s\t\t\t\t%s\t%s\t%s\t%s\n",
			tg, fmtCost(s.totalCost), fmtTokens64(s.totalTokens),
			started, ended, dur)
	}
	w.Flush()

	return nil
}

type actionLine struct {
	count        int
	cost         float64
	inputTok     int64
	outputTok    int64
	cacheReadTok int64
	totalTok     int64
}

func fmtTokens64(n int64) string {
	return fmtTokens(int(n))
}

func fmtTimestamp(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format(runner.TimestampFormat)
}

func computeDuration(startStr, endStr string) string {
	if startStr == "" || endStr == "" {
		return ""
	}
	start, err1 := time.Parse(time.RFC3339, startStr)
	end, err2 := time.Parse(time.RFC3339, endStr)
	if err1 != nil || err2 != nil {
		return ""
	}
	return runner.FormatDuration(end.Sub(start))
}
