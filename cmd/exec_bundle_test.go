package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	assemblerpkg "github.com/ateam/internal/prompts/assembler"
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
			got, err := buildArgPrompt(nil, tc.arg, "", "", tc.raw)
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
	b := staticBundle("exec", "tester", runner.ActionExec, prompts.RawTextPrompt{Text: "hello"}, opts, nil)

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
		runner.RunOpts{}, nil)
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
	b := staticBundle("x", "r", "exec", prompts.RawTextPrompt{Text: "captured"}, runner.RunOpts{}, nil)
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

// TestBuildArgPrompt_LiteralPrePostWrap verifies that for the literal /
// PromptText branch buildArgPrompt populates PrePrompt/PostPrompt on the
// resulting PromptText (NOT pre-concatenated into Text). The wrappers
// are RAW per the spec — engine expansion runs on Text only, then
// PromptText.Resolve wraps the rendered result with the raw pre/post.
// Keeping wrappers out of Text means {{ns.key}} inside --pre-prompt
// reaches the agent verbatim, matching PromptFile semantics for the
// @PATH.prompt.md branch.
func TestBuildArgPrompt_LiteralPrePostWrap(t *testing.T) {
	got, err := buildArgPrompt(nil, "BODY", "PRE", "POST", false)
	if err != nil {
		t.Fatalf("buildArgPrompt: %v", err)
	}
	pt, ok := got.(prompts.PromptText)
	if !ok {
		t.Fatalf("got %T, want PromptText", got)
	}
	if pt.Text != "BODY" {
		t.Errorf("Text = %q, want BODY (pre/post must NOT be concatenated into Text)", pt.Text)
	}
	if pt.PrePrompt != "PRE" {
		t.Errorf("PrePrompt = %q, want PRE", pt.PrePrompt)
	}
	if pt.PostPrompt != "POST" {
		t.Errorf("PostPrompt = %q, want POST", pt.PostPrompt)
	}
}

// TestBuildArgPrompt_RawPrePostWrap mirrors the previous test for the
// --raw branch: pre/post wrap as RAW around Text via RawTextPrompt's
// PrePrompt/PostPrompt fields. Same shape as the PromptText case;
// RawTextPrompt skips engine entirely so the distinction is mainly
// about the inner body.
func TestBuildArgPrompt_RawPrePostWrap(t *testing.T) {
	got, err := buildArgPrompt(nil, "BODY", "PRE", "POST", true)
	if err != nil {
		t.Fatalf("buildArgPrompt: %v", err)
	}
	rt, ok := got.(prompts.RawTextPrompt)
	if !ok {
		t.Fatalf("got %T, want RawTextPrompt", got)
	}
	if rt.Text != "BODY" {
		t.Errorf("Text = %q, want BODY", rt.Text)
	}
	if rt.PrePrompt != "PRE" {
		t.Errorf("PrePrompt = %q, want PRE", rt.PrePrompt)
	}
	if rt.PostPrompt != "POST" {
		t.Errorf("PostPrompt = %q, want POST", rt.PostPrompt)
	}
}

// TestBuildArgPrompt_PromptTextPrePostStayRaw pins the cross-impl
// invariant called out in the Assembler/PromptFactory spec (implementer
// note 2): operator-supplied --pre-prompt / --post-prompt are RAW on
// every Prompt impl, regardless of arg shape. A `{{ns.key}}` inside a
// wrapper reaches the agent verbatim for inline-text prompts (this
// test) AND for @./file.prompt.md prompts (PromptFile's behavior,
// covered separately).
func TestBuildArgPrompt_PromptTextPrePostStayRaw(t *testing.T) {
	got, err := buildArgPrompt(nil, "{{prompt.name}}", "PRE {{prompt.name}}", "POST {{prompt.name}}", false)
	if err != nil {
		t.Fatalf("buildArgPrompt: %v", err)
	}
	pt, ok := got.(prompts.PromptText)
	if !ok {
		t.Fatalf("got %T, want PromptText", got)
	}
	rt := flow.NewRuntime(nil, &root.ResolvedEnv{}, "")
	rt.SetVars(assemblerpkg.MapVars{Prompt: map[string]string{"name": "demo"}})
	out, err := pt.Resolve(rt)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "PRE {{prompt.name}}") {
		t.Errorf("PrePrompt should stay literal, got:\n%s", out)
	}
	if !strings.Contains(out, "POST {{prompt.name}}") {
		t.Errorf("PostPrompt should stay literal, got:\n%s", out)
	}
	// Body itself still expands — only wrappers are raw.
	if !strings.Contains(out, "demo") || strings.Count(out, "demo") != 1 {
		t.Errorf("body expansion missing or wrappers expanded, got:\n%s", out)
	}
}

// TestBuildArgPrompt_PromptFileBranchCarriesPrePost verifies that the
// @PATH.prompt.md branch propagates --pre-prompt / --post-prompt onto
// the PromptFile struct fields (instead of pre-concatenating with
// `---` separators) — the assembler uses these to emit the
// cli_pre_prompt and cli_post_prompt slots so sibling framing fragments
// still wrap the body the same way they would for an anchored role.
func TestBuildArgPrompt_PromptFileBranchCarriesPrePost(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "foo.prompt.md")
	if err := os.WriteFile(promptFile, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := buildArgPrompt(nil, "@"+promptFile, "PRE", "POST", false)
	if err != nil {
		t.Fatalf("buildArgPrompt: %v", err)
	}
	pf, ok := got.(prompts.PromptFile)
	if !ok {
		t.Fatalf("got %T, want PromptFile", got)
	}
	if pf.PrePrompt != "PRE" {
		t.Errorf("PromptFile.PrePrompt = %q, want PRE (must flow through to AssembleOptions, not be pre-concatenated)", pf.PrePrompt)
	}
	if pf.PostPrompt != "POST" {
		t.Errorf("PromptFile.PostPrompt = %q, want POST", pf.PostPrompt)
	}
	// The body itself stays empty on the struct — PromptFile reads it
	// at Resolve time through the assembler — so PrePrompt and PostPrompt
	// must NOT have been concatenated with the body.
	if strings.Contains(pf.PrePrompt, "body") || strings.Contains(pf.PostPrompt, "body") {
		t.Errorf("PrePrompt/PostPrompt should not include body content: pre=%q post=%q", pf.PrePrompt, pf.PostPrompt)
	}
}

// TestBuildArgPrompt_StdinSentinelStaysLiteral verifies the @- branch:
// `@-` does NOT match the @PATH.prompt.md predicate (the predicate
// excludes @-), so buildArgPrompt drops through to ResolveValue which
// reads stdin. Without explicit handling buildArgPrompt would route @-
// into the PromptFile branch as a filesystem path, which would try to
// statfs "-".
func TestBuildArgPrompt_StdinSentinelExcludedFromPromptFileBranch(t *testing.T) {
	// We can't easily exercise the stdin read in a unit test, but we can
	// verify that the dispatch doesn't mistakenly treat @- as a
	// filesystem path: if it did, the PromptFile branch would return a
	// PromptFile{Path: "-"} which IsFilesystemPromptPath would (correctly)
	// reject. So the assertion is the symmetric one — `@-` is NOT
	// IsFilesystemPromptPath material, which means buildArgPrompt's
	// `!strings.HasPrefix(arg, "@-")` guard isn't strictly necessary
	// today, but it locks in the user-visible promise that `@-` always
	// reads stdin even if the predicate is later expanded.
	if assemblerpkg.IsFilesystemPath("-") {
		t.Error("IsFilesystemPath(\"-\") should be false — `@-` is the stdin sentinel, not a path")
	}
}

// TestStaticBundle_WithEnvResolvesProjectNamespace — commit 9e96d4d
// added the env parameter to staticBundle so non-exec namespaces
// resolve in non-raw bodies for the ad-hoc exec/parallel paths.
// Without BaseVars seeded from env, {{project.name}} in a piped prompt
// would render to "" (or worse, error) — losing parity with the
// factory-bundle path. This test passes only when env.BuildAssemblerVars
// is wired into bundle.BaseVars.
func TestStaticBundle_WithEnvResolvesProjectNamespace(t *testing.T) {
	_, _, env := setupTestProject(t)

	b := staticBundle(
		"exec",
		"tester",
		runner.ActionExec,
		prompts.PromptText{Text: "project={{project.name}} prompt={{prompt.name}}"},
		runner.RunOpts{},
		env,
	)
	got, err := b.ResolvePreview(env, env.WorkDir)
	if err != nil {
		t.Fatalf("ResolvePreview: %v", err)
	}
	if !strings.Contains(got, "project=testproj") {
		t.Errorf("expected {{project.name}} to resolve to testproj, got: %q", got)
	}
	if !strings.Contains(got, "prompt=exec") {
		t.Errorf("expected {{prompt.name}} to resolve to bundle name 'exec', got: %q", got)
	}
}

// TestStaticBundle_WithEnvRegistersProjectInfoDynamic — sibling to the
// above for the dynamics surface. The commit-9e96d4d staticBundle
// pre-populates b.Dynamics with project_info so a non-raw body that
// references {{dynamic.project_info}} (today: the auto-debug prompt,
// any user-supplied prompt) resolves to the real per-env block in
// ModeReal and to an actual rendered block (project_info is
// mode-agnostic per env_bridge.go's docstring) in ModePreview.
func TestStaticBundle_WithEnvRegistersProjectInfoDynamic(t *testing.T) {
	_, _, env := setupTestProject(t)

	b := staticBundle(
		"exec",
		"the operator",
		runner.ActionExec,
		prompts.PromptText{Text: "{{dynamic.project_info}}"},
		runner.RunOpts{},
		env,
	)
	got, err := b.ResolvePreview(env, env.WorkDir)
	if err != nil {
		t.Fatalf("ResolvePreview: %v", err)
	}
	// project_info renders a Markdown header in both modes (per
	// env_bridge.go ProjectInfoDynamic docstring); the exact body
	// varies, so just assert a marker shows up.
	if !strings.Contains(got, "ATeam Project Context") && !strings.Contains(got, "Project") {
		t.Errorf("expected project_info dynamic to produce a context block, got: %q", got)
	}
}

// TestStaticBundle_NilEnvOmitsBaseVars — when env is nil (the legacy
// `staticBundle(..., nil)` call shape the test-only TestStaticBundle_*
// suite still exercises) BaseVars and Dynamics MUST stay nil. The
// commit-9e96d4d signature change kept this branch so unit tests that
// build a bundle without a real env continue to work. This is the
// safety check that the env wiring is *additive*, not unconditional.
func TestStaticBundle_NilEnvOmitsBaseVars(t *testing.T) {
	b := staticBundle("x", "r", "exec", prompts.PromptText{Text: "x"}, runner.RunOpts{}, nil)
	if b.BaseVars != nil {
		t.Errorf("expected nil BaseVars with nil env, got %v", b.BaseVars)
	}
	if b.Dynamics != nil {
		t.Errorf("expected nil Dynamics with nil env, got %v", b.Dynamics)
	}
}

// TestStaticBundle_WithEnvDoesNotShadowExecPlaceholders verifies that
// staticBundle's BaseVars seed does NOT introduce a parallel exec.*
// substitution path. Spec invariant: exec.* lives on flow.runtimeVars,
// nowhere else. env.BuildAssemblerVars seeds Exec with literal
// `{{exec.id}}` etc., but rt.Vars() intercepts the `exec` namespace
// BEFORE the base map sees it — so ModePreview produces the
// `{{AT RUNTIME:exec.id}}` sentinel, not the BaseVars literal.
func TestStaticBundle_WithEnvDoesNotShadowExecPlaceholders(t *testing.T) {
	_, _, env := setupTestProject(t)
	b := staticBundle(
		"exec",
		"tester",
		runner.ActionExec,
		prompts.PromptText{Text: "id={{exec.id}}"},
		runner.RunOpts{},
		env,
	)
	got, err := b.ResolvePreview(env, env.WorkDir)
	if err != nil {
		t.Fatalf("ResolvePreview: %v", err)
	}
	if !strings.Contains(got, "{{AT RUNTIME:exec.id}}") {
		t.Errorf("expected runtime-vars sentinel for exec.id, got: %q\n"+
			"if BaseVars.Exec is shadowing the runtimeVars resolver, the spec's "+
			"single-substitution-pass invariant is broken", got)
	}
}

// TestNewCodeBundle_ThreadsSubRunArgsToRunOpts — commit 9e96d4d also
// closed the wire on the `ateam code` supervisor side: cmd/code.go
// builds a SubRunArgs fragment and passes it into CodeBundleInput,
// which NewCodeBundle must thread onto bundle.RunOpts so
// flow.newBundleRuntime later populates rt.SubRunArgs from it. Without
// this thread, {{exec.subrun_args}} in code_management.prompt.md would
// render empty and the supervisor would emit `ateam exec` invocations
// missing --profile/--agent/--project, which would resolve against the
// wrong environment.
func TestNewCodeBundle_ThreadsSubRunArgsToRunOpts(t *testing.T) {
	_, _, env := setupTestProject(t)

	bundle := NewCodeBundle(CodeBundleInput{
		Env:        env,
		SubRunArgs: "--profile alpha --agent beta",
		Batch:      "code-2026-06-04_21-19-02",
	})
	if bundle == nil {
		t.Fatal("NewCodeBundle returned nil")
	}
	opts := bundle.RunOpts(flow.RuntimeEnv{})
	if opts.SubRunArgs != "--profile alpha --agent beta" {
		t.Errorf("RunOpts.SubRunArgs = %q, want '--profile alpha --agent beta' — supervisor sub-run args would render to empty in {{exec.subrun_args}}", opts.SubRunArgs)
	}
}
