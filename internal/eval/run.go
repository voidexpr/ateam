// Package eval implements the `ateam eval` command for comparing prompts.
// It runs two variants (base and candidate) of one or more roles against the
// same codebase and produces a side-by-side comparison plus an LLM judge score.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// Side identifies which variant is being run.
type Side string

const (
	SideBase      Side = "base"
	SideCandidate Side = "candidate"
)

// RoleRun describes one role to execute on a side, with an optional prompt
// override (empty PromptText means use the role's on-disk prompt).
type RoleRun struct {
	RoleID     string
	PromptText string
}

// Variant is the per-side configuration of an eval. Roles run sequentially
// within a side (they share .ateam/); the two sides may run in parallel when
// each has its own Dir.
type Variant struct {
	Label            Side
	Roles            []RoleRun
	Runner           *runner.Runner
	Dir              string // parallel mode: directory to run in; empty for sequential
	Env              *root.ResolvedEnv
	RunReview        bool   // run a supervisor review after the role reports
	ReviewPromptText string // optional override for the review prompt
}

// RoleRunResult is the outcome of running one role on one side.
type RoleRunResult struct {
	RoleID  string
	Summary runner.RunSummary
	Report  string
}

// RunResult is the aggregated outcome of one side: per-role runs, an optional
// review step, the summed cost/tokens, and the text passed to the judge
// (review output if present, else concatenated role reports).
type RunResult struct {
	Side    Side
	Runs    []RoleRunResult
	Review  *RoleRunResult
	Summary runner.RunSummary
	Report  string
}

// RunEval executes base and candidate and returns both results.
// Sequential mode (both Dir == ""): base runs to completion before candidate.
// Parallel mode (distinct Dir on each side): both run concurrently in their
// own envs.
//
// On candidate failure the base result is still returned (callers may want to
// show partial cost data); the error is non-nil and the candidate result is
// nil. On base failure both results are nil.
func RunEval(ctx context.Context, base, candidate Variant, timeoutMin int, verbose bool) (*RunResult, *RunResult, error) {
	if base.Label == "" {
		base.Label = SideBase
	}
	if candidate.Label == "" {
		candidate.Label = SideCandidate
	}
	parallel := base.Dir != "" && candidate.Dir != "" && base.Dir != candidate.Dir

	if !parallel {
		br, err := runVariant(ctx, base, timeoutMin, verbose)
		if err != nil {
			return nil, nil, fmt.Errorf("base run: %w", err)
		}
		cr, err := runVariant(ctx, candidate, timeoutMin, verbose)
		if err != nil {
			return br, nil, fmt.Errorf("candidate run: %w", err)
		}
		return br, cr, nil
	}

	var br, cr *RunResult
	var errBase, errCand error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		br, errBase = runVariant(ctx, base, timeoutMin, verbose)
	}()
	go func() {
		defer wg.Done()
		cr, errCand = runVariant(ctx, candidate, timeoutMin, verbose)
	}()
	wg.Wait()
	if errBase != nil {
		// Discard any successful candidate result when base fails: a comparison
		// without a base is meaningless and a partial result would mislead.
		return nil, nil, fmt.Errorf("base run: %w", errBase)
	}
	if errCand != nil {
		return br, nil, fmt.Errorf("candidate run: %w", errCand)
	}
	return br, cr, nil
}

// runVariant executes all roles for one side (sequentially), then optionally
// runs the supervisor review. When RunReview is true, role reports are written
// to the standard env.RoleReportPath so the review can pick them up; existing
// report.md files in that location are snapshotted and restored after the run.
func runVariant(ctx context.Context, v Variant, timeoutMin int, verbose bool) (*RunResult, error) {
	env := v.Env
	roleIDs := make([]string, len(v.Roles))
	for i, r := range v.Roles {
		roleIDs[i] = r.RoleID
	}
	if err := root.EnsureRoles(env.ProjectDir, roleIDs); err != nil {
		return nil, err
	}

	// When review will read on-disk report.md files, snapshot existing ones so
	// the eval doesn't permanently overwrite the project's reports.
	var restorers []func()
	defer func() {
		for i := len(restorers) - 1; i >= 0; i-- {
			restorers[i]()
		}
	}()
	if v.RunReview {
		for _, r := range v.Roles {
			restore, err := snapshotFile(env.RoleReportPath(r.RoleID))
			if err != nil {
				return nil, fmt.Errorf("snapshot report for %s: %w", r.RoleID, err)
			}
			restorers = append(restorers, restore)
		}
	}

	runs := make([]RoleRunResult, 0, len(v.Roles))
	for _, role := range v.Roles {
		rr, err := runOneRole(ctx, role, v, timeoutMin, verbose)
		if err != nil {
			return &RunResult{Side: v.Label, Runs: runs}, err
		}
		runs = append(runs, *rr)
	}

	var review *RoleRunResult
	if v.RunReview {
		rr, err := runReviewStep(ctx, v, timeoutMin, verbose)
		if err != nil {
			return &RunResult{Side: v.Label, Runs: runs}, err
		}
		review = rr
	}

	return &RunResult{
		Side:    v.Label,
		Runs:    runs,
		Review:  review,
		Summary: aggregateSummary(runs, review),
		Report:  formatReport(runs, review),
	}, nil
}

// runOneRole runs a single role with the variant's runner, writing to the
// standard report path when review will follow (so AssembleReviewPrompt picks
// it up) and to a per-side history file otherwise.
func runOneRole(ctx context.Context, role RoleRun, v Variant, timeoutMin int, verbose bool) (*RoleRunResult, error) {
	env := v.Env

	restorePrompt, err := installPrompt(env.ProjectDir, role.RoleID, role.PromptText)
	if err != nil {
		return nil, err
	}
	defer restorePrompt()

	pinfo := env.NewProjectInfoParams("", "eval")
	pinfo.Role = "role " + role.RoleID
	promptText, err := prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, role.RoleID, env.SourceDir, "", pinfo, true)
	if err != nil {
		return nil, fmt.Errorf("assemble prompt for %s: %w", role.RoleID, err)
	}

	roleDir := env.RoleDir(role.RoleID)
	ts := time.Now().Format(runner.TimestampFormat)
	var lastMsgPath string
	if v.RunReview {
		lastMsgPath = env.RoleReportPath(role.RoleID)
	} else {
		lastMsgPath = filepath.Join(roleDir, "history", ts+"_eval_"+string(v.Label)+".report.md")
	}
	opts := runner.RunOpts{
		RoleID:               role.RoleID,
		Action:               runner.ActionReport,
		LogsDir:              env.RoleLogsDir(role.RoleID),
		LastMessageFilePath:  lastMsgPath,
		ErrorMessageFilePath: filepath.Join(roleDir, "history", ts+"_eval_"+string(v.Label)+".error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeoutMin,
		HistoryDir:           env.RoleHistoryDir(role.RoleID),
		PromptName:           "eval_" + string(v.Label) + "_prompt.md",
		Verbose:              verbose,
		TaskGroup:            "eval-" + ts,
	}

	summary := v.Runner.Run(ctx, promptText, opts, nil)
	if summary.Err != nil {
		return &RoleRunResult{RoleID: role.RoleID, Summary: summary}, summary.Err
	}
	return &RoleRunResult{RoleID: role.RoleID, Summary: summary, Report: summary.Output}, nil
}

// runReviewStep invokes the supervisor review for the variant's side, using
// v.ReviewPromptText as a custom prompt when non-empty. It reads the role
// reports that runOneRole just wrote at env.RoleReportPath.
func runReviewStep(ctx context.Context, v Variant, timeoutMin int, verbose bool) (*RoleRunResult, error) {
	env := v.Env

	pinfo := env.NewProjectInfoParams("the supervisor", "eval-review")
	prompt, err := prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, "", v.ReviewPromptText)
	if err != nil {
		return nil, fmt.Errorf("assemble review prompt: %w", err)
	}

	ts := time.Now().Format(runner.TimestampFormat)
	logsDir := env.SupervisorLogsDir()
	opts := runner.RunOpts{
		RoleID:               "supervisor",
		Action:               runner.ActionReview,
		LogsDir:              logsDir,
		LastMessageFilePath:  filepath.Join(logsDir, ts+"_eval_"+string(v.Label)+".review.md"),
		ErrorMessageFilePath: filepath.Join(logsDir, ts+"_eval_"+string(v.Label)+".review_error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeoutMin,
		PromptName:           "eval_" + string(v.Label) + "_review_prompt.md",
		Verbose:              verbose,
		TaskGroup:            "eval-" + ts,
	}

	summary := v.Runner.Run(ctx, prompt, opts, nil)
	if summary.Err != nil {
		return &RoleRunResult{RoleID: "supervisor", Summary: summary}, summary.Err
	}
	return &RoleRunResult{RoleID: "supervisor", Summary: summary, Report: summary.Output}, nil
}

// installPrompt writes promptText to the project-level role prompt file,
// returning a restore function. Empty promptText is a no-op. Restore handles
// the case where no project-level file existed before.
func installPrompt(projectDir, roleID, promptText string) (func(), error) {
	if promptText == "" {
		return func() {}, nil
	}
	path := filepath.Join(projectDir, "roles", roleID, prompts.ReportPromptFile)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return snapshotAndWrite(path, []byte(promptText))
}

// snapshotFile captures the current contents (or absence) of path and returns
// a restore function. The file is left untouched at snapshot time; the caller
// may mutate it freely until the restore runs.
func snapshotFile(path string) (func(), error) {
	original, hadOriginal, err := readIfExists(path)
	if err != nil {
		return nil, err
	}
	return func() {
		if hadOriginal {
			if err := os.WriteFile(path, original, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to restore %s: %v\n", path, err)
			}
		} else {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", path, err)
			}
		}
	}, nil
}

// snapshotAndWrite snapshots path, writes content, and returns the restore.
func snapshotAndWrite(path string, content []byte) (func(), error) {
	restore, err := snapshotFile(path)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		restore()
		return nil, err
	}
	return restore, nil
}

func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

// aggregateSummary sums cost/tokens/duration across role runs and the optional
// review. PeakContextTokens takes the max (a single high-water mark is more
// meaningful than a sum).
func aggregateSummary(runs []RoleRunResult, review *RoleRunResult) runner.RunSummary {
	var total runner.RunSummary
	add := func(s runner.RunSummary) {
		total.Cost += s.Cost
		total.InputTokens += s.InputTokens
		total.OutputTokens += s.OutputTokens
		total.CacheReadTokens += s.CacheReadTokens
		total.CacheWriteTokens += s.CacheWriteTokens
		total.Duration += s.Duration
		total.DurationMS += s.DurationMS
		total.Turns += s.Turns
		if s.PeakContextTokens > total.PeakContextTokens {
			total.PeakContextTokens = s.PeakContextTokens
		}
		if total.ContextWindow == 0 {
			total.ContextWindow = s.ContextWindow
		}
	}
	for _, r := range runs {
		add(r.Summary)
	}
	if review != nil {
		add(review.Summary)
	}
	return total
}

// formatReport produces the text the judge will compare. If a review was run,
// the review output is the canonical artifact for the side. Otherwise role
// reports are concatenated with `# Role Report: <id>` headers (matching
// AssembleReviewPrompt's format) — skipped for a single-role variant.
func formatReport(runs []RoleRunResult, review *RoleRunResult) string {
	if review != nil {
		return review.Report
	}
	if len(runs) == 1 {
		return runs[0].Report
	}
	var sb strings.Builder
	for i, r := range runs {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&sb, "# Role Report: %s\n\n%s", r.RoleID, r.Report)
	}
	return sb.String()
}
