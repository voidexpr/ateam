package eval

import (
	"testing"
)

func TestParseJudgeOutput(t *testing.T) {
	output := `Here is my analysis.

Report A:
  Coverage: 0.7
  Accuracy: 0.8
  Actionability: 0.6
  Conciseness: 0.5
  Overall: 0.65

Report B:
  Coverage: 0.8
  Accuracy: 0.9
  Actionability: 0.7
  Conciseness: 0.8
  Overall: 0.80

Verdict: Candidate is better — similar coverage, fewer false positives,
more concise. 20% cheaper.
`
	r := parseJudgeOutput(output)

	if r.Base.Coverage != 0.7 || r.Base.Accuracy != 0.8 || r.Base.Actionability != 0.6 || r.Base.Conciseness != 0.5 || r.Base.Overall != 0.65 {
		t.Errorf("Base scores wrong: %+v", r.Base)
	}
	if r.Candidate.Coverage != 0.8 || r.Candidate.Accuracy != 0.9 || r.Candidate.Actionability != 0.7 || r.Candidate.Conciseness != 0.8 || r.Candidate.Overall != 0.80 {
		t.Errorf("Candidate scores wrong: %+v", r.Candidate)
	}
	if r.Verdict == "" || !contains(r.Verdict, "Candidate is better") {
		t.Errorf("Verdict not captured: %q", r.Verdict)
	}
}

func TestParseJudgeOutputMissingScores(t *testing.T) {
	// Only partial scores — missing ones should remain -1.
	output := `Report A:
  Overall: 0.5

Report B:
  Coverage: 0.9
  Overall: 0.9
`
	r := parseJudgeOutput(output)
	if r.Base.Overall != 0.5 {
		t.Errorf("Base.Overall = %v, want 0.5", r.Base.Overall)
	}
	if r.Base.Coverage != -1 {
		t.Errorf("Base.Coverage = %v, want -1 (unset)", r.Base.Coverage)
	}
	if r.Candidate.Coverage != 0.9 || r.Candidate.Overall != 0.9 {
		t.Errorf("Candidate scores wrong: %+v", r.Candidate)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
