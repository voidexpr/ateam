package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Show aggregated cost reports from the call database",
	Long: `Display aggregated cost and token usage, grouped by action type
and by task group (code sessions, report batches, etc.).

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
		return fmt.Errorf("cannot find project: %w", err)
	}

	db := openProjectDB(env)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	if env.ProjectName != "" {
		fmt.Printf("Project: %s\n\n", env.ProjectName)
	}

	// --- All-time aggregation by action category ---
	actionAggs, err := db.CostByAction("")
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
				a.Category, a.Count, display.FmtCost(a.CostUSD),
				display.FmtTokens(a.InputTokens), display.FmtTokens(a.OutputTokens),
				display.FmtTokens(a.CacheReadTokens), display.FmtTokens(a.TotalTokens))
			totalCost += a.CostUSD
			totalTokens += a.TotalTokens
		}
		fmt.Fprintf(w, "TOTAL\t\t%s\t\t\t\t%s\n",
			display.FmtCost(totalCost), display.FmtTokens(totalTokens))
		w.Flush()
	}

	// --- Task group breakdown ---
	sessionRows, err := db.CostByTaskGroup("")
	if err != nil {
		return fmt.Errorf("task group query failed: %w", err)
	}

	if len(sessionRows) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("=== Cost by Task Group ===")

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
			count:        r.Count,
			cost:         r.CostUSD,
			inputTok:     r.InputTokens,
			outputTok:    r.OutputTokens,
			cacheReadTok: r.CacheReadTokens,
			totalTok:     r.TotalTokens,
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

	prevTG := ""
	for _, tg := range order {
		s := sessions[tg]
		started := fmtTimestamp(s.firstStarted)
		ended := fmtTimestamp(s.lastEnded)
		dur := computeDuration(s.firstStarted, s.lastEnded)

		displayTG := tg
		if tg == prevTG {
			displayTG = ""
		}

		first := true
		for _, action := range []string{runner.ActionCode, runner.ActionReport, runner.ActionReview, runner.ActionRun} {
			al, ok := s.actions[action]
			if !ok {
				continue
			}
			label := displayTG
			if !first {
				label = ""
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				label, action, al.count, display.FmtCost(al.cost),
				display.FmtTokens(al.inputTok), display.FmtTokens(al.outputTok),
				display.FmtTokens(al.cacheReadTok), display.FmtTokens(al.totalTok),
				started, ended, dur)
			first = false
		}
		// Print any other actions not in the ordered list above.
		for action, al := range s.actions {
			switch action {
			case runner.ActionCode, runner.ActionReport, runner.ActionReview, runner.ActionRun:
				continue
			}
			label := displayTG
			if !first {
				label = ""
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				label, action, al.count, display.FmtCost(al.cost),
				display.FmtTokens(al.inputTok), display.FmtTokens(al.outputTok),
				display.FmtTokens(al.cacheReadTok), display.FmtTokens(al.totalTok),
				started, ended, dur)
			first = false
		}

		if len(s.actions) > 1 {
			label := displayTG
			if !first {
				label = ""
			}
			fmt.Fprintf(w, "%s\tTOTAL\t\t%s\t\t\t\t%s\t%s\t%s\t%s\n",
				label, display.FmtCost(s.totalCost), display.FmtTokens(s.totalTokens),
				started, ended, dur)
		}

		prevTG = tg
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
