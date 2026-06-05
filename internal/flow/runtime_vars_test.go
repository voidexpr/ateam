package flow

import (
	"strings"
	"testing"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/runner"
)

// These tests are the acceptance gate for the spec's "single substitution
// pass" goal (plans/feature_prompt_cmd_bundle_aware.md, Next round step 1).
// Each assertion fails until the runtime-aware Vars dispatcher is the
// single resolver for exec.* — they MUST NOT be satisfied by a parallel
// substitution path producing the same output.

// TestRuntimeVarsEnumerate exercises the snapshot pattern on the live
// resolver — exec.* is sourced from rt's typed fields (mode-aware);
// every other namespace falls through to the base BaseVars. The
// returned map is the spec's `CreateVars` snapshot.
//
// In ModePreview, exec.* keys render their AT RUNTIME sentinels in
// the snapshot (matching what Resolve would produce). In ModeReal,
// they render the rt field values directly. Pins both branches.
func TestRuntimeVarsEnumerate(t *testing.T) {
	t.Run("ModePreview snapshot", func(t *testing.T) {
		rt := NewRuntime(nil, nil, "")
		rt.SetMode(prompts.ModePreview)
		rt.SetVars(assembler.MapVars{
			Project: map[string]string{"name": "myproj"},
			Args:    map[string]string{"verbose": "true"},
		})
		snap := rt.Vars().Enumerate()

		// Base namespaces flow through from BaseVars.
		if snap["project"]["name"] != "myproj" {
			t.Errorf("project.name = %q, want myproj", snap["project"]["name"])
		}
		if snap["args"]["verbose"] != "true" {
			t.Errorf("args.verbose = %q, want true", snap["args"]["verbose"])
		}

		// exec.* renders the AT RUNTIME sentinel in ModePreview for
		// every key in the closed set.
		exec := snap["exec"]
		if len(exec) == 0 {
			t.Fatal("Enumerate(): exec namespace missing")
		}
		for _, key := range []string{"id", "output_dir", "output_file", "prompt_file"} {
			want := "{{AT RUNTIME:exec." + key + "}}"
			if exec[key] != want {
				t.Errorf("exec.%s = %q, want %q", key, exec[key], want)
			}
		}
		// Closed-set membership: dropped keys absent.
		for _, key := range []string{"profile", "effort", "max_budget_usd"} {
			if _, ok := exec[key]; ok {
				t.Errorf("exec.%s should NOT appear in snapshot (dropped from closed set)", key)
			}
		}
	})

	t.Run("ModeReal snapshot uses rt fields", func(t *testing.T) {
		rt := NewRuntime(nil, nil, "")
		rt.SetMode(prompts.ModeReal)
		rt.ExecID = 7
		rt.Batch = "batch-77"
		rt.OutputDir = "/tmp/runtime/7"
		rt.OutputFile = "/tmp/runtime/7/report.md"
		rt.PromptFile = "/tmp/logs/7/prompt.md"
		rt.Agent = "claude"
		rt.Model = "opus"
		snap := rt.Vars().Enumerate()
		exec := snap["exec"]
		cases := map[string]string{
			"id":          "7",
			"batch":       "batch-77",
			"output_dir":  "/tmp/runtime/7",
			"output_file": "/tmp/runtime/7/report.md",
			"prompt_file": "/tmp/logs/7/prompt.md",
			"agent":       "claude",
			"model":       "opus",
		}
		for key, want := range cases {
			if exec[key] != want {
				t.Errorf("exec.%s = %q, want %q", key, exec[key], want)
			}
		}
	})

	t.Run("exec namespace from BaseVars is ignored", func(t *testing.T) {
		// runtimeVars owns exec.* — even if BaseVars carries an Exec
		// map (legacy seed in BuildAssemblerVars), the snapshot
		// reflects the rt fields, not the BaseVars values.
		rt := NewRuntime(nil, nil, "")
		rt.SetMode(prompts.ModeReal)
		rt.ExecID = 99
		rt.SetVars(assembler.MapVars{
			Exec: map[string]string{"id": "should-not-appear"},
		})
		snap := rt.Vars().Enumerate()
		if snap["exec"]["id"] != "99" {
			t.Errorf("exec.id = %q; BaseVars.Exec must NOT shadow rt fields", snap["exec"]["id"])
		}
	})
}

// TestRuntimeVarsExecKeyClosedSet locks the set of exec.* keys the
// prompt-body resolver recognizes. Keys outside this set error as
// "unknown key in exec namespace" — they are not silently substituted
// to empty strings. Spec Future section lists exec.{profile, effort,
// max_budget_usd, max_budget_usd_batch} as the wire-on-demand
// candidates; this test pins their absence today.
func TestRuntimeVarsExecKeyClosedSet(t *testing.T) {
	cases := []struct {
		key  string
		want bool // true = recognized; false = unknown-key error
	}{
		// Load-bearing: required in ModeReal, sentinel in ModePreview.
		{"id", true},
		{"batch", true},
		{"output_dir", true},
		{"output_file", true},
		{"prompt_file", true},
		// Verb-supplied, empty-OK in ModeReal: shipped prompts reference them.
		{"timestamp", true},
		{"agent", true},
		{"model", true},
		{"subrun_args", true},
		{"debug_context", true},
		{"auto_roles_commands_output", true},
		// Dropped: no verb wires them, no shipped prompt references them.
		// Referencing in a user prompt errors as unknown-key.
		{"profile", false},
		{"effort", false},
		{"max_budget_usd", false},
		{"max_budget_usd_batch", false},
		// Typos always error.
		{"id_typo", false},
		{"output_filee", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			rt := NewRuntime(nil, nil, "")
			rt.SetMode(prompts.ModePreview)
			_, _, err := rt.Vars().Resolve("exec", tc.key)
			if tc.want && err != nil {
				t.Errorf("exec.%s should be recognized, got error: %v", tc.key, err)
			}
			if !tc.want && err == nil {
				t.Errorf("exec.%s should error as unknown-key, got nil", tc.key)
			}
			if !tc.want && err != nil && !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("expected 'unknown key' error for exec.%s, got: %v", tc.key, err)
			}
		})
	}
}

// TestRuntimeVarsModePreviewSentinels — spec line 612-613: "exec.* renders
// as {{AT RUNTIME:exec.<key>}} in preview/verify modes." This is the
// gate for the verification pass (flow.Verify) producing deterministic
// output without forking git or allocating exec_ids.
func TestRuntimeVarsModePreviewSentinels(t *testing.T) {
	rt := NewRuntime(nil, nil, "")
	rt.SetMode(prompts.ModePreview)
	// Even with rt fields populated, preview mode wins.
	rt.ExecID = 42
	rt.OutputFile = "/tmp/runtime/42/report.md"

	for _, key := range []string{"id", "batch", "output_dir", "output_file", "prompt_file"} {
		t.Run(key, func(t *testing.T) {
			val, known, err := rt.Vars().Resolve("exec", key)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !known {
				t.Fatal("exec namespace must be known")
			}
			want := "{{AT RUNTIME:exec." + key + "}}"
			if val != want {
				t.Errorf("got %q, want %q", val, want)
			}
		})
	}
}

// TestRuntimeVarsModeRealUsesRuntimeFields — spec line 158-163: per-bundle
// runtime values populated by Prepare flow through rt fields. prompts.ModeReal +
// non-zero rt.ExecID means the engine substitutes the actual value during
// Prompt.Resolve — the runner-side ResolveTemplateString pass becomes
// redundant (step 3 deletes it).
func TestRuntimeVarsModeRealUsesRuntimeFields(t *testing.T) {
	rt := NewRuntime(nil, nil, "")
	rt.SetMode(prompts.ModeReal)
	rt.ExecID = 42
	rt.Batch = "code-2026-06-04_13-25-23"
	rt.OutputDir = "/tmp/runtime/42"
	rt.OutputFile = "/tmp/runtime/42/report.md"
	rt.PromptFile = "/tmp/logs/42/prompt.md"

	cases := map[string]string{
		"id":          "42",
		"batch":       "code-2026-06-04_13-25-23",
		"output_dir":  "/tmp/runtime/42",
		"output_file": "/tmp/runtime/42/report.md",
		"prompt_file": "/tmp/logs/42/prompt.md",
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			val, _, err := rt.Vars().Resolve("exec", key)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if val != want {
				t.Errorf("got %q, want %q", val, want)
			}
		})
	}
}

// TestRuntimeVarsModeRealPrePrepareErrors — spec invariant: in prompts.ModeReal,
// reading exec.id before Prepare populated it is a bug. The resolver
// surfaces it loudly so flow.execute (step 2) is forced to wire prepared
// → rt before Prompt.Resolve runs.
func TestRuntimeVarsModeRealPrePrepareErrors(t *testing.T) {
	rt := NewRuntime(nil, nil, "")
	rt.SetMode(prompts.ModeReal)
	// rt.ExecID stays zero.
	_, _, err := rt.Vars().Resolve("exec", "id")
	if err == nil {
		t.Fatal("expected error when reading exec.id with zero ExecID in prompts.ModeReal")
	}
	if !strings.Contains(err.Error(), "exec.id") || !strings.Contains(err.Error(), "flow.execute") {
		t.Errorf("error message should reference exec.id and the flow.execute wire, got: %v", err)
	}
}

// TestRuntimeVarsFallsThroughForNonExecKeys — the runtime resolver
// intercepts exec.* only. Everything else delegates to the base Vars
// supplied via rt.SetVars. This lets bundle factories supply project.*,
// args.*, etc. without re-implementing them in flow.
func TestRuntimeVarsFallsThroughForNonExecKeys(t *testing.T) {
	rt := NewRuntime(nil, nil, "")
	rt.SetMode(prompts.ModeReal)
	rt.SetVars(assembler.MapVars{
		Project: map[string]string{"name": "myproj"},
		Prompt:  map[string]string{"name": "supervisor"},
	})
	val, _, err := rt.Vars().Resolve("project", "name")
	if err != nil || val != "myproj" {
		t.Errorf("project.name = %q err=%v, want myproj", val, err)
	}
	val, _, err = rt.Vars().Resolve("prompt", "name")
	if err != nil || val != "supervisor" {
		t.Errorf("prompt.name = %q err=%v, want supervisor", val, err)
	}
}

// TestRuntimeVarsEngineEndToEnd exercises the full path: a prompt body
// containing {{exec.output_file}} resolved through the assembler engine
// using rt.Vars() as its resolver. prompts.ModePreview produces the sentinel;
// prompts.ModeReal produces the rt field. The runner Replacer is not consulted.
func TestRuntimeVarsEngineEndToEnd(t *testing.T) {
	prompt := "Write report to {{exec.output_file}}"

	t.Run("preview", func(t *testing.T) {
		rt := NewRuntime(nil, nil, "")
		rt.SetMode(prompts.ModePreview)
		out, err := assembler.NewEngine(nil, 0).Render(prompt, rt.Vars())
		if err != nil {
			t.Fatal(err)
		}
		want := "Write report to {{AT RUNTIME:exec.output_file}}"
		if out != want {
			t.Errorf("got %q, want %q", out, want)
		}
	})

	t.Run("real", func(t *testing.T) {
		rt := NewRuntime(nil, nil, "")
		rt.SetMode(prompts.ModeReal)
		rt.OutputFile = "/tmp/runtime/42/report.md"
		out, err := assembler.NewEngine(nil, 0).Render(prompt, rt.Vars())
		if err != nil {
			t.Fatal(err)
		}
		want := "Write report to /tmp/runtime/42/report.md"
		if out != want {
			t.Errorf("got %q, want %q", out, want)
		}
	})
}

// _ assertion: prompts.Vars and assembler.Vars are the same interface,
// so the runtime-aware Vars satisfies both consumer surfaces. If this
// breaks, step 1's mechanism is no longer drop-in compatible with the
// engine.
var (
	_ prompts.Vars   = (*runtimeVars)(nil)
	_ assembler.Vars = (*runtimeVars)(nil)
)

// captureRuntimePrompt asserts on the rt's exec.* fields when its Resolve
// runs. Used by step 2 to prove flow.execute populates prepared values
// into rt BEFORE Prompt.Resolve sees the context — the entire point of
// the Prepare/Resolve/ExecutePrepared three-step lifecycle.
type captureRuntimePrompt struct {
	got *Runtime
}

func (p *captureRuntimePrompt) Resolve(ctx prompts.ResolveContext) (string, error) {
	rt, _ := ctx.(*Runtime)
	p.got = rt
	// Synthesize an output that proves the resolver wired the right fields.
	val, _, err := rt.Vars().Resolve("exec", "id")
	if err != nil {
		return "", err
	}
	return "exec_id=" + val, nil
}

func (p *captureRuntimePrompt) Inspect(prompts.ResolveContext) ([]prompts.Section, error) {
	return nil, nil
}

// passthroughExecPrompt resolves to whatever literal string it carries
// without going through the engine. Used by the step-3 invariant test:
// when this string survives intact through flow.execute and lands on
// ExecutePrepared, we have proof that the runner is NOT substituting
// the prompt body.
type passthroughExecPrompt struct{ literal string }

func (p passthroughExecPrompt) Resolve(prompts.ResolveContext) (string, error) {
	return p.literal, nil
}
func (p passthroughExecPrompt) Inspect(prompts.ResolveContext) ([]prompts.Section, error) {
	return nil, nil
}

// TestRunnerDoesNotSubstitutePromptBody — spec Next-round step 3.
// The runner's ExecutePrepared used to do `prompt =
// ResolveTemplateString(prompt, tmplVars)` after Prompt.Resolve already
// produced text. That second pass IS the two-pass mechanism the Problem
// section indicts. This test produces a bundle whose Prompt.Resolve
// returns literal `{{OUTPUT_FILE}}` (a token the runner Replacer knows
// about). If the runner is doing a second substitution pass, the
// captured prompt will be the substituted value. If the spec invariant
// holds, the literal survives.
func TestRunnerDoesNotSubstitutePromptBody(t *testing.T) {
	literal := "Write report to {{OUTPUT_FILE}}"
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	bundle := PromptBundle{
		Name:    "x",
		Prompt:  passthroughExecPrompt{literal: literal},
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{Batch: "B7"} },
	}
	if res := Run(bundle, env, rc).Steps[0].Results[0]; res.Flow.State != StateContinue {
		t.Fatalf("Run: %v", res.Flow)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("ExecutePrepared called %d times, want 1", len(exec.calls))
	}
	got := exec.calls[0].Prompt
	if got != literal {
		t.Errorf("runner substituted the prompt body — got %q, want literal %q\n"+
			"This means runner.go::ExecutePrepared still calls ResolveTemplateString\n"+
			"on the prompt. Spec Next-round step 3 says delete that line.",
			got, literal)
	}
}

// capturePromptForKeys captures the resolved exec.* values for a set
// of keys when its Resolve runs. Used by the tests below to prove
// flow.newBundleRuntime threads the verb-supplied RunOpts fields
// through onto rt before Prompt.Resolve sees the context.
type capturePromptForKeys struct {
	keys []string
	got  map[string]string
}

func (p *capturePromptForKeys) Resolve(ctx prompts.ResolveContext) (string, error) {
	p.got = make(map[string]string, len(p.keys))
	for _, k := range p.keys {
		val, _, err := ctx.Vars().Resolve("exec", k)
		if err != nil {
			return "", err
		}
		p.got[k] = val
	}
	return "ok", nil
}

func (p *capturePromptForKeys) Inspect(prompts.ResolveContext) ([]prompts.Section, error) {
	return nil, nil
}

// TestExecuteWiresOptsToRuntime locks the opts→rt wiring for the
// "verb-supplied" exec.* keys — values that flow.newBundleRuntime
// must read from RunOpts and write onto rt before Prompt.Resolve so
// the corresponding {{exec.<key>}} placeholders resolve in ModeReal.
//
// Each row exists because of a specific regression we hit:
//   - subrun_args / debug_context: commit 9e96d4d closed the
//     deferred-wire TODO after `ateam code` shipped broken
//     sub-invocations and `inspect --auto-debug` pre-resolved its
//     debug bundle with the sentinel inlined.
//   - auto_roles_commands_output: predates 9e96d4d but is structurally
//     identical (`cmd/auto_roles.go` sets opts; supervisor prompt
//     references the placeholder); pinning it here prevents the same
//     pre-resolve-in-ModePreview regression from re-landing.
func TestExecuteWiresOptsToRuntime(t *testing.T) {
	cases := []struct {
		key  string // exec.* key the prompt asks for
		set  func(*runner.RunOpts)
		want string
	}{
		{
			key:  "subrun_args",
			set:  func(o *runner.RunOpts) { o.SubRunArgs = "--profile foo --agent bar" },
			want: "--profile foo --agent bar",
		},
		{
			key:  "debug_context",
			set:  func(o *runner.RunOpts) { o.DebugContext = "## Recent runs\n- exec 42 failed\n" },
			want: "## Recent runs\n- exec 42 failed\n",
		},
		{
			key:  "auto_roles_commands_output",
			set:  func(o *runner.RunOpts) { o.AutoRolesCommandsOutput = "discovered roles: a, b, c" },
			want: "discovered roles: a, b, c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			exec := &fakeExecutor{}
			rc := newCtx()
			env := newEnv(exec)
			capture := &capturePromptForKeys{keys: []string{tc.key}}
			bundle := PromptBundle{
				Name:   "x",
				Prompt: capture,
				RunOpts: func(RuntimeEnv) runner.RunOpts {
					opts := runner.RunOpts{}
					tc.set(&opts)
					return opts
				},
			}
			res := Run(bundle, env, rc).Steps[0].Results[0]
			if res.Flow.State != StateContinue {
				t.Fatalf("Run: %v", res.Flow)
			}
			if got := capture.got[tc.key]; got != tc.want {
				t.Errorf("exec.%s = %q, want %q (opts→rt wire broken)", tc.key, got, tc.want)
			}
		})
	}
}

// TestExecutePopulatesRuntimeFromPrepared — spec Next-round step 2.
// flow.execute MUST populate rt.{ExecID, Batch, OutputDir, OutputFile,
// PromptFile} from prepared + RunOpts before calling Prompt.Resolve.
// Without this, prompts.ModeReal Resolve calls error out (per step 1's
// resolver), so the system as a whole is unusable until step 2 lands.
// This test fails until that wire exists.
func TestExecutePopulatesRuntimeFromPrepared(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	capture := &captureRuntimePrompt{}
	bundle := PromptBundle{
		Name:    "x",
		Prompt:  capture,
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{Batch: "B7"} },
	}
	res := Run(bundle, env, rc).Steps[0].Results[0]
	if res.Flow.State != StateContinue {
		t.Fatalf("Run: %v", res.Flow)
	}
	if capture.got == nil {
		t.Fatal("Prompt.Resolve never ran")
	}
	rt := capture.got
	if rt.ExecID == 0 {
		t.Error("rt.ExecID not populated from prepared.ExecID")
	}
	if rt.Batch != "B7" {
		t.Errorf("rt.Batch = %q, want B7", rt.Batch)
	}
	if rt.Mode() != prompts.ModeReal {
		t.Errorf("rt.Mode = %v, want prompts.ModeReal post-Prepare", rt.Mode())
	}
}
