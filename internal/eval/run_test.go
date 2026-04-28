package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// installPrompt tests

func TestInstallPrompt_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	roleID := "testrole"
	path := filepath.Join(dir, "roles", roleID, prompts.ReportPromptFile)

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("original content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore, err := installPrompt(dir, roleID, "installed content")
	if err != nil {
		t.Fatalf("installPrompt: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after install: %v", err)
	}
	if string(got) != "installed content" {
		t.Errorf("after install: got %q, want %q", string(got), "installed content")
	}

	restore()

	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after restore: %v", err)
	}
	if string(got) != "original content" {
		t.Errorf("after restore: got %q, want %q", string(got), "original content")
	}
}

func TestInstallPrompt_NewFile(t *testing.T) {
	dir := t.TempDir()
	roleID := "testrole"
	path := filepath.Join(dir, "roles", roleID, prompts.ReportPromptFile)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to not exist before install, got: %v", err)
	}

	restore, err := installPrompt(dir, roleID, "temp content")
	if err != nil {
		t.Fatalf("installPrompt: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after install: %v", err)
	}
	if string(got) != "temp content" {
		t.Errorf("after install: got %q, want %q", string(got), "temp content")
	}

	restore()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed after restore, got: %v", err)
	}
}

func TestInstallPrompt_DirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	roleID := "testrole"
	path := filepath.Join(dir, "roles", roleID, prompts.ReportPromptFile)

	_, err := installPrompt(dir, roleID, "content")
	if err != nil {
		t.Fatalf("installPrompt: %v", err)
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	got := info.Mode().Perm()
	if got != 0700 {
		t.Errorf("directory permissions = %04o, want 0700", got)
	}
}

func TestInstallPrompt_RestoreHandlesMissingFile(t *testing.T) {
	dir := t.TempDir()
	roleID := "testrole"
	path := filepath.Join(dir, "roles", roleID, prompts.ReportPromptFile)

	restore, err := installPrompt(dir, roleID, "content")
	if err != nil {
		t.Fatalf("installPrompt: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Simulate the file disappearing before restore is called (error path).
	os.Remove(path)

	// Restore should not panic; it logs a warning to stderr.
	restore()
}

// RunEval tests

func makeEvalVariant(t *testing.T, label Side, response string, errToReturn error) Variant {
	t.Helper()
	var mock *agent.MockAgent
	if errToReturn != nil {
		mock = &agent.MockAgent{Err: errToReturn}
	} else {
		mock = &agent.MockAgent{Response: response}
	}
	return Variant{
		Label:  label,
		Roles:  []RoleRun{{RoleID: "testrole", PromptText: "# test prompt\nAnalyze this."}},
		Runner: &runner.Runner{Agent: mock},
		Env: &root.ResolvedEnv{
			ProjectDir: t.TempDir(),
			SourceDir:  t.TempDir(),
		},
	}
}

func TestRunEval_Serial(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "base report output", nil)
	candidate := makeEvalVariant(t, SideCandidate, "candidate report output", nil)

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err != nil {
		t.Fatalf("RunEval: %v", err)
	}
	if br == nil {
		t.Fatal("base result is nil")
	}
	if cr == nil {
		t.Fatal("candidate result is nil")
	}
	if br.Side != SideBase {
		t.Errorf("base result Side = %v, want %v", br.Side, SideBase)
	}
	if cr.Side != SideCandidate {
		t.Errorf("candidate result Side = %v, want %v", cr.Side, SideCandidate)
	}

	// Verify each mock was called exactly once.
	baseMock := base.Runner.Agent.(*agent.MockAgent)
	candMock := candidate.Runner.Agent.(*agent.MockAgent)
	if len(baseMock.Requests) != 1 {
		t.Errorf("base mock called %d times, want 1", len(baseMock.Requests))
	}
	if len(candMock.Requests) != 1 {
		t.Errorf("candidate mock called %d times, want 1", len(candMock.Requests))
	}
}

func TestRunEval_Parallel(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "base parallel output", nil)
	base.Dir = t.TempDir()

	candidate := makeEvalVariant(t, SideCandidate, "candidate parallel output", nil)
	candidate.Dir = t.TempDir()

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err != nil {
		t.Fatalf("RunEval parallel: %v", err)
	}
	if br == nil || cr == nil {
		t.Fatalf("expected both results, got base=%v candidate=%v", br, cr)
	}
	if br.Side != SideBase {
		t.Errorf("base Side = %v, want %v", br.Side, SideBase)
	}
	if cr.Side != SideCandidate {
		t.Errorf("candidate Side = %v, want %v", cr.Side, SideCandidate)
	}

	// Verify both goroutines ran by checking each mock was invoked.
	baseMock := base.Runner.Agent.(*agent.MockAgent)
	candMock := candidate.Runner.Agent.(*agent.MockAgent)
	if len(baseMock.Requests) != 1 {
		t.Errorf("base mock called %d times, want 1", len(baseMock.Requests))
	}
	if len(candMock.Requests) != 1 {
		t.Errorf("candidate mock called %d times, want 1", len(candMock.Requests))
	}
}

func TestRunEval_CandidateFailure(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "base report", nil)
	candidate := makeEvalVariant(t, SideCandidate, "", errors.New("agent error"))

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err == nil {
		t.Fatal("expected non-nil error for candidate failure")
	}
	if br == nil {
		t.Error("base result should still be returned on candidate failure")
	}
	if cr != nil {
		t.Error("candidate result should be nil on failure")
	}
	if br != nil && br.Side != SideBase {
		t.Errorf("base result Side = %v, want %v", br.Side, SideBase)
	}
}

func TestRunEval_BaseFailure(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "", errors.New("base agent error"))
	candidate := makeEvalVariant(t, SideCandidate, "candidate report", nil)

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err == nil {
		t.Fatal("expected non-nil error for base failure")
	}
	if br != nil {
		t.Errorf("base result should be nil on base failure, got %v", br)
	}
	if cr != nil {
		t.Errorf("candidate result should be nil on base failure, got %v", cr)
	}
}

func TestRunEval_ParallelCandidateFailure(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "base parallel report", nil)
	base.Dir = t.TempDir()
	candidate := makeEvalVariant(t, SideCandidate, "", errors.New("parallel candidate error"))
	candidate.Dir = t.TempDir()

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err == nil {
		t.Fatal("expected non-nil error for parallel candidate failure")
	}
	if br == nil {
		t.Error("base result should be preserved when candidate fails in parallel mode")
	}
	if cr != nil {
		t.Errorf("candidate result should be nil on failure, got %v", cr)
	}
	if br != nil && br.Side != SideBase {
		t.Errorf("base result Side = %v, want %v", br.Side, SideBase)
	}
}

func TestRunEval_NvsM(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "first", nil)
	base.Roles = []RoleRun{{RoleID: "r1"}, {RoleID: "r2"}}
	candidate := makeEvalVariant(t, SideCandidate, "single", nil)
	candidate.Roles = []RoleRun{{RoleID: "r3"}}

	// MockAgent returns the same Response for every call; that's fine — we
	// only need to check that the right number of runs happened and that the
	// summary aggregates per-role results.
	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err != nil {
		t.Fatalf("RunEval: %v", err)
	}
	if len(br.Runs) != 2 {
		t.Errorf("base: got %d runs, want 2", len(br.Runs))
	}
	if len(cr.Runs) != 1 {
		t.Errorf("candidate: got %d runs, want 1", len(cr.Runs))
	}
	baseMock := base.Runner.Agent.(*agent.MockAgent)
	if len(baseMock.Requests) != 2 {
		t.Errorf("base mock called %d times, want 2", len(baseMock.Requests))
	}
	// Concatenated report (multi-role base) should include role headers.
	if !strings.Contains(br.Report, "# Role Report: r1") || !strings.Contains(br.Report, "# Role Report: r2") {
		t.Errorf("base report missing role headers: %q", br.Report)
	}
	// Single-role candidate should not have a header (just the report).
	if strings.Contains(cr.Report, "# Role Report:") {
		t.Errorf("single-role candidate should not have role header: %q", cr.Report)
	}
}

func TestFormatReportPicksReviewWhenPresent(t *testing.T) {
	runs := []RoleRunResult{{RoleID: "x", Report: "x report"}}
	rev := &RoleRunResult{RoleID: "supervisor", Report: "review text"}
	got := formatReport(runs, rev)
	if got != "review text" {
		t.Errorf("formatReport with review = %q, want %q", got, "review text")
	}
}

func TestAggregateSummarySumsAndKeepsPeak(t *testing.T) {
	runs := []RoleRunResult{
		{Summary: runner.RunSummary{Cost: 0.5, InputTokens: 100, PeakContextTokens: 200, ContextWindow: 1000}},
		{Summary: runner.RunSummary{Cost: 0.3, InputTokens: 50, PeakContextTokens: 500, ContextWindow: 1000}},
	}
	review := &RoleRunResult{Summary: runner.RunSummary{Cost: 0.1, InputTokens: 20, PeakContextTokens: 100}}
	total := aggregateSummary(runs, review)
	if total.Cost != 0.9 {
		t.Errorf("Cost = %v, want 0.9", total.Cost)
	}
	if total.InputTokens != 170 {
		t.Errorf("InputTokens = %d, want 170", total.InputTokens)
	}
	if total.PeakContextTokens != 500 {
		t.Errorf("PeakContextTokens = %d, want 500 (max)", total.PeakContextTokens)
	}
	if total.ContextWindow != 1000 {
		t.Errorf("ContextWindow = %d, want 1000", total.ContextWindow)
	}
}

func TestRunEval_ParallelBaseFailure(t *testing.T) {
	ctx := context.Background()
	base := makeEvalVariant(t, SideBase, "", errors.New("parallel base error"))
	base.Dir = t.TempDir()
	candidate := makeEvalVariant(t, SideCandidate, "candidate parallel report", nil)
	candidate.Dir = t.TempDir()

	br, cr, err := RunEval(ctx, base, candidate, 1, false)
	if err == nil {
		t.Fatal("expected non-nil error for parallel base failure")
	}
	if br != nil {
		t.Errorf("base result should be nil on base failure, got %v", br)
	}
	if cr != nil {
		t.Errorf("candidate result should be discarded when base fails, got %v", cr)
	}
}
