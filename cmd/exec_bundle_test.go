package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// TestBuildArgPrompt_DispatchRule asserts the spec's three-way
// dispatch for `ateam exec`-style CLI arguments (spec lines 471-478):
//
//   - --raw set → RawTextPrompt
//   - `@PATH` where PATH ends in `.prompt.md` → PromptFile
//   - otherwise (literal, `@PATH` not ending in `.prompt.md`, `@-`
//     stdin) → PromptText
func TestBuildArgPrompt_DispatchRule(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "foo.prompt.md")
	if err := os.WriteFile(promptFile, []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}
	plainFile := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(plainFile, []byte("plain body"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		arg       string
		raw       bool
		wantType  string
		wantField string // expected Path (for PromptFile) or Text prefix (for others)
	}{
		{"raw literal", "hello", true, "RawText", "hello"},
		{"raw @path", "@" + promptFile, true, "RawText", "body"},
		{"@PATH.prompt.md → PromptFile", "@" + promptFile, false, "File", promptFile},
		{"@PATH.md → PromptText", "@" + plainFile, false, "Text", "plain body"},
		{"literal text → PromptText", "hello", false, "Text", "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildArgPrompt(tc.arg, "", "", tc.raw)
			if err != nil {
				t.Fatalf("buildArgPrompt: %v", err)
			}
			switch tc.wantType {
			case "RawText":
				r, ok := got.(prompts.RawTextPrompt)
				if !ok {
					t.Fatalf("got %T, want RawTextPrompt", got)
				}
				if r.Text != tc.wantField {
					t.Errorf("Text=%q want %q", r.Text, tc.wantField)
				}
			case "Text":
				p, ok := got.(prompts.PromptText)
				if !ok {
					t.Fatalf("got %T, want PromptText", got)
				}
				if p.Text != tc.wantField {
					t.Errorf("Text=%q want %q", p.Text, tc.wantField)
				}
			case "File":
				f, ok := got.(prompts.PromptFile)
				if !ok {
					t.Fatalf("got %T, want PromptFile", got)
				}
				if f.Path != tc.wantField {
					t.Errorf("Path=%q want %q", f.Path, tc.wantField)
				}
			}
		})
	}
}

func TestStaticBundle_Shape(t *testing.T) {
	opts := runner.RunOpts{RoleID: "tester", Action: runner.ActionExec, WorkDir: "/wd"}
	b := staticBundle("exec", "tester", runner.ActionExec, prompts.RawTextPrompt{Text: "hello"}, opts)

	if b.Name != "exec" || b.Role != "tester" || b.Action != runner.ActionExec {
		t.Errorf("identity fields wrong: %+v", b)
	}

	got, err := b.Prompt.Resolve(nil)
	if err != nil {
		t.Fatalf("Prompt.Resolve: %v", err)
	}
	if got != "hello" {
		t.Errorf("Prompt.Resolve: got %q want hello", got)
	}

	gotOpts := b.RunOpts(flow.RuntimeEnv{})
	if gotOpts.RoleID != opts.RoleID || gotOpts.WorkDir != opts.WorkDir {
		t.Errorf("RunOpts: got %+v want %+v", gotOpts, opts)
	}
}

// TestStaticBundle_PromptTextExpandsExecVars asserts spec step 10's
// load-bearing invariant: `ateam exec` (no --raw) wraps the body in a
// PromptText so {{exec.*}} resolves against rt.Vars() instead of being
// passed verbatim to the agent. The runner DOES NOT re-substitute the
// prompt body (step 3 invariant), so the only thing standing between
// `{{exec.id}}` and the agent's stdin is PromptText.Resolve.
func TestStaticBundle_PromptTextExpandsExecVars(t *testing.T) {
	b := staticBundle("x", "r", "exec",
		prompts.PromptText{Text: "exec={{exec.id}}"},
		runner.RunOpts{})
	// Build a preview runtime so exec.id renders as the AT RUNTIME sentinel —
	// confirms PromptText routes through the engine. The expansion shape is
	// covered byte-for-byte by internal/flow's runtimeVars tests; here we
	// just need to prove the engine ran at all (raw text would have
	// passed `{{exec.id}}` through verbatim).
	got, err := b.ResolvePreview(nil, "")
	if err != nil {
		t.Fatalf("ResolvePreview: %v", err)
	}
	if !strings.Contains(got, "AT RUNTIME:exec.id") {
		t.Errorf("expected exec.id preview sentinel in output, got %q", got)
	}
}

func TestStaticBundle_RawPromptIsLiteral(t *testing.T) {
	// RawTextPrompt: Resolve returns the captured prompt verbatim, no
	// engine expansion. Spec step 10: this is what `ateam exec --raw`
	// wraps the body in.
	b := staticBundle("x", "r", "exec", prompts.RawTextPrompt{Text: "captured"}, runner.RunOpts{})
	got, err := b.Prompt.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "captured" {
		t.Errorf("got %q want captured", got)
	}
}

func TestBuildRunner_ScratchModeDefaultProfile(t *testing.T) {
	// With no project context and neither --profile nor --agent set,
	// scratch mode falls back to profile="default". The Agent ends up
	// being the agent the "default" profile resolves to.
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	r, err := buildRunner(env, RunnerSpec{Action: runner.ActionExec})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil || r.Agent == nil {
		t.Fatal("expected non-nil runner with resolved Agent")
	}
}

func TestBuildRunner_ScratchModeExplicitAgent(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	r, err := buildRunner(env, RunnerSpec{
		Agent:  "mock",
		Action: runner.ActionExec,
	})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil || r.Agent == nil {
		t.Fatal("expected non-nil runner with Agent")
	}
	if got := r.Agent.Name(); got != "mock" {
		t.Errorf("Agent.Name(): got %q want mock", got)
	}
}

func TestBuildRunner_ProjectModeWithProfile(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	env, err := root.Resolve(filepath.Dir(orgDir), projPath)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	r, err := buildRunner(env, RunnerSpec{
		Profile: "default",
		Action:  runner.ActionExec,
	})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestBuildRunner_ConflictingProfileAndAgent(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	// --profile + --agent is a user-facing error.
	_, err = buildRunner(env, RunnerSpec{
		Profile: "default",
		Agent:   "mock",
		Action:  runner.ActionExec,
	})
	if err == nil {
		t.Fatal("expected error from conflicting --profile + --agent")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error message: got %q want substring 'mutually exclusive'", err)
	}
}
