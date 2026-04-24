package eval

import (
	"strings"
	"testing"
	"time"
)

func makeRunResult(cost float64, inputTokens, outputTokens int) *RunResult {
	rr := &RunResult{Side: SideBase}
	rr.Summary.Cost = cost
	rr.Summary.InputTokens = inputTokens
	rr.Summary.OutputTokens = outputTokens
	rr.Summary.Duration = 2 * time.Second
	rr.Summary.Turns = 3
	return rr
}

func TestPrintComparison_Normal(t *testing.T) {
	base := makeRunResult(0.0012, 1000, 200)
	cand := makeRunResult(0.0010, 900, 180)

	var sb strings.Builder
	PrintComparison(&sb, "myrole", base, cand, nil)

	out := sb.String()
	if !strings.Contains(out, "=== Eval: myrole ===") {
		t.Errorf("missing header in output: %q", out)
	}
	if !strings.Contains(out, "Cost") {
		t.Errorf("missing Cost row in output: %q", out)
	}
	if strings.Contains(out, "Judge scores") {
		t.Errorf("expected no judge section when judge is nil, got: %q", out)
	}
}

func TestPrintComparison_NilBase(t *testing.T) {
	cand := makeRunResult(0.001, 500, 100)

	var sb strings.Builder
	PrintComparison(&sb, "myrole", nil, cand, nil)

	out := sb.String()
	if !strings.Contains(out, "comparison unavailable") {
		t.Errorf("expected unavailable notice for nil base, got: %q", out)
	}
}

func TestPrintComparison_NilCandidate(t *testing.T) {
	base := makeRunResult(0.001, 500, 100)

	var sb strings.Builder
	PrintComparison(&sb, "myrole", base, nil, nil)

	out := sb.String()
	if !strings.Contains(out, "comparison unavailable") {
		t.Errorf("expected unavailable notice for nil candidate, got: %q", out)
	}
}

func TestPrintComparison_NilJudgeResult(t *testing.T) {
	base := makeRunResult(0.002, 800, 150)
	cand := makeRunResult(0.0018, 750, 140)

	var sb strings.Builder
	PrintComparison(&sb, "myrole", base, cand, nil)

	out := sb.String()
	if strings.Contains(out, "Judge scores") {
		t.Errorf("unexpected judge section with nil judge: %q", out)
	}
}

func TestPrintComparison_WithJudge(t *testing.T) {
	base := makeRunResult(0.002, 800, 150)
	cand := makeRunResult(0.0018, 750, 140)
	judge := &JudgeResult{
		Base:      JudgeScores{Coverage: 0.7, Accuracy: 0.8, Actionability: 0.6, Conciseness: 0.5, Overall: 0.65},
		Candidate: JudgeScores{Coverage: 0.8, Accuracy: 0.9, Actionability: 0.7, Conciseness: 0.8, Overall: 0.80},
		Verdict:   "Candidate is better",
	}

	var sb strings.Builder
	PrintComparison(&sb, "myrole", base, cand, judge)

	out := sb.String()
	if !strings.Contains(out, "Judge scores") {
		t.Errorf("expected judge section: %q", out)
	}
	if !strings.Contains(out, "Candidate is better") {
		t.Errorf("expected verdict in output: %q", out)
	}
}

func TestPctDelta_ZeroBase(t *testing.T) {
	got := pctDelta(0, 5.0)
	if got != "" {
		t.Errorf("pctDelta(0, 5) = %q, want \"\"", got)
	}
}

func TestPctDelta_Normal(t *testing.T) {
	got := pctDelta(10.0, 12.0)
	if got != "+20%" {
		t.Errorf("pctDelta(10, 12) = %q, want \"+20%%\"", got)
	}
}

func TestFormatScore_Negative(t *testing.T) {
	got := formatScore(-1)
	if got != "-" {
		t.Errorf("formatScore(-1) = %q, want \"-\"", got)
	}
}

func TestFormatScore_Zero(t *testing.T) {
	got := formatScore(0)
	if got != "0.00" {
		t.Errorf("formatScore(0) = %q, want \"0.00\"", got)
	}
}
