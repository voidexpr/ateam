// Package eval implements the `ateam eval` command for comparing prompts.
// It runs two variants (base and candidate) of a role against the same codebase
// and produces a side-by-side comparison plus an LLM judge score.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// Variant is the per-side configuration of a run.
type Variant struct {
	Label      Side
	PromptText string         // role prompt content to use; empty means use on-disk as-is
	Runner     *runner.Runner // profile/agent/model already resolved
	Dir        string         // parallel mode: directory to run in; empty for sequential
	Env        *root.ResolvedEnv
}

// RunResult holds one side's outcome.
type RunResult struct {
	Side    Side
	Summary runner.RunSummary
	Report  string // report.md contents
}

// RunEval executes base and candidate and returns both results.
// Sequential mode (both variants have Dir == ""): runs serially in variants[0].Env.
// Parallel mode (both have distinct Dir): runs concurrently, each in its own env.
//
// On candidate failure the base result is still returned (callers may want to
// show partial cost data); the error is non-nil and the candidate result is nil.
func RunEval(ctx context.Context, roleID string, base, candidate Variant, timeoutMin int, verbose bool) (*RunResult, *RunResult, error) {
	if base.Label == "" {
		base.Label = SideBase
	}
	if candidate.Label == "" {
		candidate.Label = SideCandidate
	}
	parallel := base.Dir != "" && candidate.Dir != "" && base.Dir != candidate.Dir

	if !parallel {
		br, err := runOne(ctx, roleID, base, timeoutMin, verbose)
		if err != nil {
			return nil, nil, fmt.Errorf("base run: %w", err)
		}
		cr, err := runOne(ctx, roleID, candidate, timeoutMin, verbose)
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
		br, errBase = runOne(ctx, roleID, base, timeoutMin, verbose)
	}()
	go func() {
		defer wg.Done()
		cr, errCand = runOne(ctx, roleID, candidate, timeoutMin, verbose)
	}()
	wg.Wait()
	if errBase != nil {
		return nil, nil, fmt.Errorf("base run: %w", errBase)
	}
	if errCand != nil {
		return br, nil, fmt.Errorf("candidate run: %w", errCand)
	}
	return br, cr, nil
}

// runOne executes a single variant. If v.PromptText is set, it writes it to
// the project-level role prompt file for the duration of the run and restores
// the original after (whether present or absent).
func runOne(ctx context.Context, roleID string, v Variant, timeoutMin int, verbose bool) (*RunResult, error) {
	env := v.Env
	if err := root.EnsureRoles(env.ProjectDir, []string{roleID}); err != nil {
		return nil, err
	}

	restore, err := installPrompt(env.ProjectDir, roleID, v.PromptText)
	if err != nil {
		return nil, err
	}
	defer restore()

	pinfo := env.NewProjectInfoParams("", "eval")
	pinfo.Role = "role " + roleID
	promptText, err := prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, roleID, env.SourceDir, "", pinfo, true)
	if err != nil {
		return nil, fmt.Errorf("assemble prompt: %w", err)
	}

	roleDir := env.RoleDir(roleID)
	ts := time.Now().Format(runner.TimestampFormat)
	opts := runner.RunOpts{
		RoleID:               roleID,
		Action:               runner.ActionReport,
		LogsDir:              env.RoleLogsDir(roleID),
		LastMessageFilePath:  filepath.Join(roleDir, "history", ts+"_eval_"+string(v.Label)+".report.md"),
		ErrorMessageFilePath: filepath.Join(roleDir, "history", ts+"_eval_"+string(v.Label)+".error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeoutMin,
		HistoryDir:           env.RoleHistoryDir(roleID),
		PromptName:           "eval_" + string(v.Label) + "_prompt.md",
		Verbose:              verbose,
		TaskGroup:            "eval-" + ts,
	}

	summary := v.Runner.Run(ctx, promptText, opts, nil)
	if summary.Err != nil {
		return &RunResult{Side: v.Label, Summary: summary}, summary.Err
	}

	return &RunResult{
		Side:    v.Label,
		Summary: summary,
		Report:  summary.Output,
	}, nil
}

// installPrompt writes promptText to the project-level role prompt file,
// returning a restore function. If promptText is empty, no change is made.
// Restore handles the case where no project-level file existed before.
func installPrompt(projectDir, roleID, promptText string) (func(), error) {
	if promptText == "" {
		return func() {}, nil
	}
	path := filepath.Join(projectDir, "roles", roleID, prompts.ReportPromptFile)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	original, hadOriginal, err := readIfExists(path)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(promptText), 0644); err != nil {
		return nil, err
	}

	return func() {
		if hadOriginal {
			if err := os.WriteFile(path, original, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to restore prompt %s: %v\n", path, err)
			}
		} else {
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove temp prompt %s: %v\n", path, err)
			}
		}
	}, nil
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
