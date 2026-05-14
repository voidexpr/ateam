package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Show aggregated cost reports from the call database",
	Long: `Display aggregated cost and token usage, grouped by action type
and by batch (code sessions, report batches, etc.).

When run inside a project, results are filtered to that project by default.
Use --project to filter explicitly.

Example:
  ateam cost
  ateam cost --project myproject`,
	Args: cobra.NoArgs,
	RunE: runCost,
}

func runCost(cmd *cobra.Command, args []string) error {
	env, err := resolveEnv()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db, err := requireProjectDB(env)
	if err != nil {
		return err
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

	// --- Batch breakdown ---
	sessionRows, err := db.CostByBatch("")
	if err != nil {
		return fmt.Errorf("batch query failed: %w", err)
	}

	if len(sessionRows) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("=== Cost by Batch ===")

	type sessionSummary struct {
		batch        string
		actions      map[string]actionLine
		totalCost    float64
		totalTokens  int64
		firstStarted string
		lastEnded    string
	}
	sessions := make(map[string]*sessionSummary)
	var order []string

	for _, r := range sessionRows {
		s, ok := sessions[r.Batch]
		if !ok {
			s = &sessionSummary{
				batch:   r.Batch,
				actions: make(map[string]actionLine),
			}
			sessions[r.Batch] = s
			order = append(order, r.Batch)
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
	fmt.Fprintln(w, "BATCH\tACTION\tCOUNT\tCOST\tINPUT\tOUTPUT\tCACHE_READ\tTOTAL_TOKENS\tSTARTED\tENDED\tDURATION")

	prevBatch := ""
	for _, batch := range order {
		s := sessions[batch]
		started := fmtTimestamp(s.firstStarted)
		ended := fmtTimestamp(s.lastEnded)
		dur := computeDuration(s.firstStarted, s.lastEnded)

		displayBatch := batch
		if batch == prevBatch {
			displayBatch = ""
		}

		first := true
		for _, action := range []string{runner.ActionCode, runner.ActionReport, runner.ActionReview, runner.ActionExec} {
			al, ok := s.actions[action]
			if !ok {
				continue
			}
			label := displayBatch
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
			case runner.ActionCode, runner.ActionReport, runner.ActionReview, runner.ActionExec:
				continue
			}
			label := displayBatch
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
			label := displayBatch
			if !first {
				label = ""
			}
			fmt.Fprintf(w, "%s\tTOTAL\t\t%s\t\t\t\t%s\t%s\t%s\t%s\n",
				label, display.FmtCost(s.totalCost), display.FmtTokens(s.totalTokens),
				started, ended, dur)
		}

		prevBatch = batch
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

// fmtTimestamp is an alias for display.FmtRFC3339AsTimestamp.
var fmtTimestamp = display.FmtRFC3339AsTimestamp

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
