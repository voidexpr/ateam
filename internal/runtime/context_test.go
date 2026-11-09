package runtime

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContextBlocksInDefaults(t *testing.T) {
	cfg := Must(Load(nil))

	claude := cfg.Agents["claude"]
	in := cfg.ForContext(claude, true)
	if in.Context != "inside_container" || in.Sandbox != "" || !slices.Contains(in.Args, "--dangerously-skip-permissions") {
		t.Errorf("claude inside: context %q sandbox %q args %v", in.Context, in.Sandbox, in.Args)
	}
	out := cfg.ForContext(claude, false)
	if out.Context != "outside_container" || out.Sandbox == "" || slices.Contains(out.Args, "--dangerously-skip-permissions") {
		t.Errorf("claude outside: context %q sandbox-set %v args %v", out.Context, out.Sandbox != "", out.Args)
	}
	if !slices.Equal(out.Args, claude.Args) {
		t.Errorf("outside must leave the args alone: %v vs %v", out.Args, claude.Args)
	}

	codex := cfg.Agents["codex"]
	if out := cfg.ForContext(codex, false); !slices.Contains(out.Args, "--skip-git-repo-check") {
		t.Errorf("codex outside: %v", out.Args)
	}
	if in := cfg.ForContext(codex, true); slices.Contains(in.Args, "--skip-git-repo-check") {
		t.Errorf("codex inside: %v", in.Args)
	}
	tmux := cfg.Agents["codex-tmux"]
	if out := cfg.ForContext(tmux, false); !slices.Equal(out.Args, tmux.Args) {
		t.Errorf("codex-tmux has its own args and takes no additions: %v", out.Args)
	}
}

func TestContextBlocksLayerAndInherit(t *testing.T) {
	home := writeLayer(t, t.TempDir(), `
agent "claude" {
  model = "claude-opus-5-5"
  outside_container {
    config_dir = "~/shared"
  }
  inside_container {
    args_append = ["--home-extra"]
    env         = { FOO = "bar" }
  }
}
`)
	proj := writeLayer(t, t.TempDir(), `
agent "claude" {
  inside_container {
    args_append = ["--proj-extra"]
    effort      = "low"
  }
}

agent "mine" {
  base = "claude"
  inside_container {
    env = { FOO = "baz" }
  }
}

agent "own-args" {
  base = "claude"
  args = ["-p"]
}
`)
	cfg := Must(Load([]string{home, proj}))
	homeFile, projFile := filepath.Join(home, ConfigFile), filepath.Join(proj, ConfigFile)

	claude := cfg.Agents["claude"]
	out := cfg.ForContext(claude, false)
	if out.ConfigDir != "~/shared" || out.Effort != "" || out.Env["FOO"] != "" {
		t.Errorf("outside: config_dir %q effort %q env %v", out.ConfigDir, out.Effort, out.Env)
	}
	in := cfg.ForContext(claude, true)
	if in.ConfigDir != "" || in.Effort != "low" || in.Env["FOO"] != "bar" || in.Model != "claude-opus-5-5" {
		t.Errorf("inside: config_dir %q effort %q env %v model %q", in.ConfigDir, in.Effort, in.Env, in.Model)
	}
	// additions accumulate in layer order after the agent's args
	n := len(in.Args)
	if n < 3 || !slices.Equal(in.Args[n-3:], []string{"--dangerously-skip-permissions", "--home-extra", "--proj-extra"}) {
		t.Errorf("inside args: %v", in.Args)
	}

	// a derived agent inherits the blocks and may override single keys
	mine := cfg.ForContext(cfg.Agents["mine"], true)
	if mine.Env["FOO"] != "baz" || mine.Effort != "low" || !slices.Equal(mine.Args[len(mine.Args)-3:], in.Args[n-3:]) {
		t.Errorf("mine inside: env %v effort %q args %v", mine.Env, mine.Effort, mine.Args)
	}
	if mineOut := cfg.ForContext(cfg.Agents["mine"], false); mineOut.ConfigDir != "~/shared" {
		t.Errorf("mine outside: config_dir %q", mineOut.ConfigDir)
	}
	// an agent with its own args takes the base's scalars but not its additions
	own := cfg.ForContext(cfg.Agents["own-args"], true)
	if !slices.Equal(own.Args, []string{"-p"}) || own.Effort != "low" {
		t.Errorf("own-args inside: args %v effort %q", own.Args, own.Effort)
	}

	// provenance: each block attribute has its file and line
	if src, ok := cfg.Source("agent.claude.outside_container.config_dir"); !ok || cfg.Where(src, nil) != homeFile+":5" {
		t.Errorf("config_dir source: %+v", src)
	}
	if src, ok := cfg.Source("agent.claude.inside_container.effort"); !ok || cfg.Where(src, nil) != projFile+":5" {
		t.Errorf("effort source: %+v", src)
	}
	if src, ok := cfg.Source("agent.mine.inside_container.effort"); !ok || src.Layer != `agent "claude" (base)` {
		t.Errorf("inherited effort source: %+v", src)
	}
	keys := make([]string, 0)
	for _, e := range cfg.Trace("agent.claude.inside_container") {
		keys = append(keys, e.Key)
	}
	for _, want := range []string{"agent.claude.inside_container.args_append", "agent.claude.inside_container.effort", "agent.claude.inside_container.env.FOO", "agent.claude.inside_container.sandbox"} {
		if !slices.Contains(keys, want) {
			t.Errorf("trace lacks %s: %v", want, keys)
		}
	}
}

func TestContextBlockArgsReplace(t *testing.T) {
	dir := writeLayer(t, t.TempDir(), `
agent "plain" {
  base = "claude"
  inside_container {
    args        = ["-p"]
    args_append = ["--last"]
  }
}
`)
	cfg := Must(Load([]string{dir}))
	in := cfg.ForContext(cfg.Agents["plain"], true)
	if !slices.Equal(in.Args, []string{"-p", "--last"}) {
		t.Errorf("args in the block replace the agent's, additions wrap them: %v", in.Args)
	}
}

func TestProfilePins(t *testing.T) {
	proj := writeLayer(t, t.TempDir(), `
profile "cheap" {
  model = "claude-haiku-5-5"
}

profile "mine" {
  agent          = "claude"
  container      = "none"
  effort         = "max"
  max_budget_usd = "1.00"
}
`)
	cfg := Must(Load([]string{proj}))
	projFile := filepath.Join(proj, ConfigFile)

	// the project layer overrides one pin of the built-in cheap profile
	prof, ac, _ := Must3(cfg.ResolveProfile("cheap"))
	run := cfg.AgentForRun(prof, ac, false)
	if run.Model != "claude-haiku-5-5" || run.MaxBudgetUSD != "0.50" || ac.Model != "" {
		t.Errorf("cheap: run model %q budget %q, agent model %q", run.Model, run.MaxBudgetUSD, ac.Model)
	}
	rep := cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "claude-haiku-5-5" || rep.Model.Source != `profile "cheap" (`+projFile+`)` {
		t.Errorf("cheap report: %+v", rep.Model)
	}
	if rep = cfg.ModelReport(prof, ac, CLIOverrides{Model: "sonnet"}, NativeConfig{}); rep.Model.Value != "sonnet" || rep.Model.Source != LayerCLI {
		t.Errorf("--model must win over the profile: %+v", rep.Model)
	}
	if ws := cfg.ModelWarnings(prof, ac, ""); len(ws) != 0 {
		t.Errorf("cheap with a cheap pin must not warn: %v", ws)
	}

	// a new profile pins effort and budget only; the model stays unpinned
	prof, ac, _ = Must3(cfg.ResolveProfile("mine"))
	run = cfg.AgentForRun(prof, ac, false)
	if run.Effort != "max" || run.MaxBudgetUSD != "1.00" || run.Model != "" {
		t.Errorf("mine: %+v", run)
	}
	if src, ok := cfg.Source("profile.mine.effort"); !ok || cfg.Where(src, nil) != projFile+":9" {
		t.Errorf("effort source: %+v", src)
	}
}

func TestOldContainerAttributesAreRejected(t *testing.T) {
	for _, attr := range []string{`args_inside_container = []`, `args_outside_container = []`, `sandbox_inside_container = true`} {
		dir := writeLayer(t, t.TempDir(), "agent \"claude\" {\n  "+attr+"\n}\n")
		_, err := Load([]string{dir})
		if err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
			t.Errorf("%s: want an unsupported-argument error, got %v", attr, err)
		}
	}
}

// Must3 is Must for the three-value ResolveProfile.
func Must3(prof *ProfileConfig, ac *AgentConfig, cc *ContainerConfig, err error) (*ProfileConfig, *AgentConfig, *ContainerConfig) {
	if err != nil {
		panic(err)
	}
	return prof, ac, cc
}
