package eval

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// JudgeScores holds one side's scores.
type JudgeScores struct {
	Coverage      float64
	Accuracy      float64
	Actionability float64
	Conciseness   float64
	Overall       float64
}

// JudgeResult is the parsed verdict from the judge LLM call.
type JudgeResult struct {
	Base      JudgeScores
	Candidate JudgeScores
	Verdict   string // free-form summary paragraph
	Raw       string // full judge output, for debugging
	Summary   runner.RunSummary
}

// JudgeKind selects the wording the judge prompt uses to introduce the
// comparison.
type JudgeKind string

const (
	KindReport JudgeKind = "report"
	KindReview JudgeKind = "review"
)

// JudgeInput configures a judge run. Subject is interpolated into the prompt
// as a label; Kind picks the framing (report-vs-review wording).
type JudgeInput struct {
	Subject         string
	Kind            JudgeKind
	BaseReport      string
	CandidateReport string
}

// RunJudge invokes r with a structured prompt asking it to score both
// artifacts on coverage/accuracy/actionability/conciseness/overall and return
// a verdict.
func RunJudge(ctx context.Context, r *runner.Runner, env *root.ResolvedEnv, in JudgeInput, timeoutMin int, verbose bool) (*JudgeResult, error) {
	prompt := buildJudgePrompt(in)

	ts := time.Now().Format(runner.TimestampFormat)
	logsDir := filepath.Join(env.ProjectDir, "logs", "eval")
	opts := runner.RunOpts{
		RoleID:              "eval-judge",
		Action:              runner.ActionRun,
		LogsDir:             logsDir,
		LastMessageFilePath: filepath.Join(logsDir, ts+"_judge.md"),
		WorkDir:             env.SourceDir,
		TimeoutMin:          timeoutMin,
		PromptName:          "eval_judge_prompt.md",
		Verbose:             verbose,
		TaskGroup:           "eval-judge-" + ts,
	}

	summary := r.Run(ctx, prompt, opts, nil)
	if summary.Err != nil {
		return &JudgeResult{Raw: summary.Output, Summary: summary}, summary.Err
	}

	result := parseJudgeOutput(summary.Output)
	result.Summary = summary
	return result, nil
}

func buildJudgePrompt(in JudgeInput) string {
	intro := fmt.Sprintf(`You are evaluating two analysis reports for "%s" produced
on the same codebase. Each side may aggregate one or more roles; compare
the union of findings, not report-by-report.`, in.Subject)
	if in.Kind == KindReview {
		intro = fmt.Sprintf(`You are evaluating two supervisor reviews for "%s" synthesized
from analysis reports on the same codebase. The two sides may differ in
which roles ran, in role prompts, or in review prompts. Compare the
quality of the synthesis itself.`, in.Subject)
	}

	return fmt.Sprintf(`%s

Score EACH side independently from 0.00 to 1.00 on these dimensions:
- Coverage: did it surface real issues; did it miss obvious ones?
- Accuracy: are findings correct; any false positives?
- Actionability: are recommendations specific enough to implement?
- Conciseness: is it focused, or padded with generic advice?

Then compute an Overall score for each side.

Return your evaluation in EXACTLY this format (machine-parsed):

`+"```"+`
Report A:
  Coverage: <0.00-1.00>
  Accuracy: <0.00-1.00>
  Actionability: <0.00-1.00>
  Conciseness: <0.00-1.00>
  Overall: <0.00-1.00>

Report B:
  Coverage: <0.00-1.00>
  Accuracy: <0.00-1.00>
  Actionability: <0.00-1.00>
  Conciseness: <0.00-1.00>
  Overall: <0.00-1.00>

Verdict: <one paragraph — which is better and why, or note if they are comparable>
`+"```"+`

---

# Report A (base)

%s

---

# Report B (candidate)

%s
`, intro, in.BaseReport, in.CandidateReport)
}

var scoreLine = regexp.MustCompile(`(?i)^\s*(Coverage|Accuracy|Actionability|Conciseness|Overall)\s*:\s*([0-9.]+)`)

// parseJudgeOutput extracts scores for Report A (base) and Report B (candidate)
// plus the Verdict paragraph. Missing scores are stored as -1 so the caller can
// render "-".
func parseJudgeOutput(output string) *JudgeResult {
	result := &JudgeResult{Raw: output}
	result.Base = JudgeScores{Coverage: -1, Accuracy: -1, Actionability: -1, Conciseness: -1, Overall: -1}
	result.Candidate = JudgeScores{Coverage: -1, Accuracy: -1, Actionability: -1, Conciseness: -1, Overall: -1}

	lines := strings.Split(output, "\n")
	var target *JudgeScores
	var verdictLines []string
	inVerdict := false

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "report a"):
			target = &result.Base
			inVerdict = false
			continue
		case strings.HasPrefix(lower, "report b"):
			target = &result.Candidate
			inVerdict = false
			continue
		case strings.HasPrefix(lower, "verdict:"):
			inVerdict = true
			verdictLines = append(verdictLines, strings.TrimSpace(line[len("verdict:"):]))
			continue
		}
		if inVerdict {
			if line == "" || strings.HasPrefix(line, "```") {
				inVerdict = false
				continue
			}
			verdictLines = append(verdictLines, line)
			continue
		}
		if target == nil {
			continue
		}
		m := scoreLine.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		val, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(m[1]) {
		case "coverage":
			target.Coverage = val
		case "accuracy":
			target.Accuracy = val
		case "actionability":
			target.Actionability = val
		case "conciseness":
			target.Conciseness = val
		case "overall":
			target.Overall = val
		}
	}

	result.Verdict = strings.TrimSpace(strings.Join(verdictLines, " "))
	return result
}
