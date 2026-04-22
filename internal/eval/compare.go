package eval

import (
	"fmt"
	"io"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// PrintComparison writes a side-by-side cost/tokens table and (if present) the
// judge scores to w.
func PrintComparison(w io.Writer, roleID string, base, candidate *RunResult, judge *JudgeResult) {
	fmt.Fprintf(w, "=== Eval: %s ===\n\n", roleID)

	fmt.Fprintln(w, "Cost & metrics:")
	b, c := base.Summary, candidate.Summary
	rows := []metricRow{
		{"Cost", fmt.Sprintf("$%.4f", b.Cost), fmt.Sprintf("$%.4f", c.Cost), pctDelta(b.Cost, c.Cost)},
		{"Input tokens", formatInt(b.InputTokens), formatInt(c.InputTokens), pctDelta(float64(b.InputTokens), float64(c.InputTokens))},
		{"Output tokens", formatInt(b.OutputTokens), formatInt(c.OutputTokens), pctDelta(float64(b.OutputTokens), float64(c.OutputTokens))},
		{"Cache read", formatInt(b.CacheReadTokens), formatInt(c.CacheReadTokens), pctDelta(float64(b.CacheReadTokens), float64(c.CacheReadTokens))},
		{"Duration", runner.FormatDuration(b.Duration), runner.FormatDuration(c.Duration), pctDelta(float64(b.Duration), float64(c.Duration))},
		{"Turns", formatInt(b.Turns), formatInt(c.Turns), ""},
	}
	if b.PeakContextTokens > 0 || c.PeakContextTokens > 0 {
		rows = append(rows, metricRow{
			"Peak context",
			formatInt(b.PeakContextTokens),
			formatInt(c.PeakContextTokens),
			pctDelta(float64(b.PeakContextTokens), float64(c.PeakContextTokens)),
		})
	}
	printMetricTable(w, rows)

	if judge == nil {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Judge scores (0.00-1.00):")
	scoreRows := []metricRow{
		{"Coverage", formatScore(judge.Base.Coverage), formatScore(judge.Candidate.Coverage), ""},
		{"Accuracy", formatScore(judge.Base.Accuracy), formatScore(judge.Candidate.Accuracy), ""},
		{"Actionability", formatScore(judge.Base.Actionability), formatScore(judge.Candidate.Actionability), ""},
		{"Conciseness", formatScore(judge.Base.Conciseness), formatScore(judge.Candidate.Conciseness), ""},
		{"Overall", formatScore(judge.Base.Overall), formatScore(judge.Candidate.Overall), ""},
	}
	printMetricTable(w, scoreRows)

	if judge.Verdict != "" {
		fmt.Fprintf(w, "\nVerdict: %s\n", strings.TrimSpace(judge.Verdict))
	}
}

type metricRow struct {
	label, base, candidate, delta string
}

func printMetricTable(w io.Writer, rows []metricRow) {
	const labelW = 16
	const colW = 14
	fmt.Fprintf(w, "  %-*s%-*s%-*s%s\n", labelW, "", colW, "Base", colW, "Candidate", "Delta")
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s%-*s%-*s%s\n", labelW, r.label+":", colW, r.base, colW, r.candidate, r.delta)
	}
}

func formatInt(n int) string {
	if n == 0 {
		return "-"
	}
	return display.FmtTokens(int64(n))
}

func formatScore(s float64) string {
	if s < 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", s)
}

func pctDelta(base, cand float64) string {
	if base == 0 {
		return ""
	}
	return fmt.Sprintf("%+.0f%%", (cand-base)/base*100)
}
