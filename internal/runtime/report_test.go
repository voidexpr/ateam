package runtime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestModelReportBuiltinAgents(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// claude: nothing pinned anywhere
	prof, ac, _, _ := cfg.ResolveProfile("default")
	rep := cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "" || rep.Model.Source != SourceAgentDefault {
		t.Errorf("default: model = %+v", rep.Model)
	}
	if !strings.Contains(rep.ModelText(), "claude CLI picks its own default") {
		t.Errorf("default: model text %q", rep.ModelText())
	}
	if len(rep.Chain) != 1 || rep.Chain[0].Layer != BuiltinLayer || rep.Chain[0].Model != "" {
		t.Errorf("default: chain %+v", rep.Chain)
	}

	// claude-high: model and effort attributes in the built-in layer
	ac2 := cfg.Agents["claude-high"]
	rep = cfg.ModelReport(nil, &ac2, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "opus" || rep.Model.Source != BuiltinLayer || rep.Effort.Value != "high" {
		t.Errorf("claude-high: %+v / %+v", rep.Model, rep.Effort)
	}

	// cheap: sonnet via the claude-sonnet agent, no extra args
	prof, ac, _, _ = cfg.ResolveProfile("cheap")
	rep = cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "sonnet" || rep.Model.Note != "" {
		t.Errorf("cheap: %+v", rep.Model)
	}

	// CLI override outranks the attribute.
	rep = cfg.ModelReport(nil, &ac2, CLIOverrides{Model: "haiku", Effort: "low"}, NativeConfig{})
	if rep.Model.Value != "haiku" || rep.Model.Source != LayerCLI || rep.Effort.Value != "low" || rep.Effort.Source != LayerCLI {
		t.Errorf("cli override: %+v / %+v", rep.Model, rep.Effort)
	}
	if last := rep.Chain[len(rep.Chain)-1]; last.Layer != LayerCLI {
		t.Errorf("cli must be the last chain entry, got %+v", last)
	}
}

func TestModelReportArgsAttributesAndExtraArgs(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "legacy" {
  base = "claude"
  args = ["-p", "--model", "sonnet", "--effort", "low"]
}

agent "pinned" {
  base  = "legacy"
  model = "claude-opus-5-5"
}

profile "legacy" {
  agent            = "legacy"
  container        = "none"
  agent_extra_args = ["--model", "haiku"]
}

profile "pinned" {
  agent     = "pinned"
  container = "none"
}
`)
	projDir := writeLayer(t, t.TempDir(), `
agent "pinned" {
  effort = "xhigh"
}
`)
	cfg, err := Load([]string{orgDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	orgFile := filepath.Join(orgDir, ConfigFile)
	projFile := filepath.Join(projDir, ConfigFile)

	// legacy: args carry the model, the profile's extra args outrank them.
	prof, ac, _, _ := cfg.ResolveProfile("legacy")
	rep := cfg.ModelReport(prof, ac, CLIOverrides{Model: "opus"}, NativeConfig{})
	if rep.Model.Value != "haiku" || rep.Model.Source != orgFile || rep.Model.Note != noteExtraArgs {
		t.Errorf("legacy model: %+v", rep.Model)
	}
	if rep.Effort.Value != "low" || rep.Effort.Note != noteArgs {
		t.Errorf("legacy effort: %+v", rep.Effort)
	}
	if rep.Chain[0].Note != noteArgs || rep.Chain[0].Model != "sonnet" {
		t.Errorf("args entry must come first: %+v", rep.Chain[0])
	}

	// pinned: attribute beats the args inherited from the base; the effort
	// attribute set in the project layer wins over the base's args effort.
	prof, ac, _, _ = cfg.ResolveProfile("pinned")
	rep = cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "claude-opus-5-5" || rep.Model.Source != orgFile {
		t.Errorf("pinned model: %+v", rep.Model)
	}
	if rep.Effort.Value != "xhigh" || rep.Effort.Source != projFile {
		t.Errorf("pinned effort: %+v", rep.Effort)
	}
	lines := strings.Join(rep.ChainLines(), "\n")
	for _, want := range []string{"model sonnet, effort low — inside args", orgFile + ": model claude-opus-5-5", projFile + ": effort xhigh", BuiltinLayer + ": (nothing)"} {
		if !strings.Contains(lines, want) {
			t.Errorf("chain lines missing %q:\n%s", want, lines)
		}
	}
}

func TestModelReportShowsBaseAndMissingLayers(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "child" {
  base = "claude-high"
}
`)
	projDir := t.TempDir()
	cfg, err := Load([]string{orgDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac := cfg.Agents["child"]
	rep := cfg.ModelReport(nil, &ac, CLIOverrides{}, NativeConfig{})
	if rep.Model.Value != "opus" || rep.Model.Source != `agent "claude-high" (base)` {
		t.Errorf("inherited model: %+v", rep.Model)
	}
	if rep.Chain[0].Layer != `agent "claude-high" (base)` {
		t.Errorf("base pseudo-layer must come first: %+v", rep.Chain)
	}
	lines := strings.Join(rep.ChainLines(), "\n")
	if !strings.Contains(lines, filepath.Join(projDir, ConfigFile)+": (nothing) — no file") {
		t.Errorf("missing project layer must be listed:\n%s", lines)
	}
}

func TestModelReportNativeConfigIsLowest(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	native := NativeConfig{Model: "claude-opus-5-5", Source: "~/.claude/settings.json"}

	// Unpinned agent: the native model is what will run.
	prof, ac, _, _ := cfg.ResolveProfile("default")
	rep := cfg.ModelReport(prof, ac, CLIOverrides{}, native)
	if rep.Model.Value != "claude-opus-5-5" || rep.Model.Source != native.Source || rep.Chain[0].Layer != native.Source {
		t.Errorf("unpinned: %+v chain %+v", rep.Model, rep.Chain[0])
	}

	// Pinned agent: native stays at the bottom and loses.
	high := cfg.Agents["claude-high"]
	rep = cfg.ModelReport(nil, &high, CLIOverrides{}, native)
	if rep.Model.Value != "opus" || rep.Model.Source != BuiltinLayer || rep.Chain[0].Layer != native.Source {
		t.Errorf("pinned: %+v chain %+v", rep.Model, rep.Chain[0])
	}

	// A note without values still shows the layer.
	rep = cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{Source: "~/.claude/settings.json", Note: "no settings.json"})
	if rep.Model.Value != "" || !strings.Contains(strings.Join(rep.ChainLines(), "\n"), "no settings.json") {
		t.Errorf("note only: %+v\n%s", rep.Model, strings.Join(rep.ChainLines(), "\n"))
	}
	if rep := cfg.ModelReport(prof, ac, CLIOverrides{}, NativeConfig{}); rep.Chain[0].Layer != BuiltinLayer {
		t.Errorf("empty native must be omitted: %+v", rep.Chain[0])
	}
}
