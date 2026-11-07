package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestReadNativeConfigClaude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model": "claude-opus-5-5", "permissions": {}}`), 0644); err != nil {
		t.Fatal(err)
	}
	n := ReadNativeConfig(NameClaude, dir, noEnv)
	if n.Model != "claude-opus-5-5" || n.Note != "" || !strings.HasSuffix(n.Source, "settings.json") {
		t.Errorf("settings.json: %+v", n)
	}

	env := func(k string) string {
		if k == "ANTHROPIC_MODEL" {
			return "claude-sonnet-5-5"
		}
		return ""
	}
	n = ReadNativeConfig(NameClaude, dir, env)
	if n.Model != "claude-sonnet-5-5" || !strings.Contains(n.Source, "ANTHROPIC_MODEL") {
		t.Errorf("env must outrank the file: %+v", n)
	}

	n = ReadNativeConfig("", filepath.Join(dir, "missing"), noEnv)
	if n.Model != "" || !strings.Contains(n.Note, "no settings.json") {
		t.Errorf("missing file: %+v", n)
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{not json`), 0644); err != nil {
		t.Fatal(err)
	}
	n = ReadNativeConfig(NameClaude, dir, noEnv)
	if n.Model != "" || !strings.Contains(n.Note, "not valid JSON") {
		t.Errorf("bad json: %+v", n)
	}

	// CLAUDE_CONFIG_DIR from the env is used when no config_dir is set.
	envDir := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	if n := ReadNativeConfig(NameClaude, "", envDir); !strings.Contains(n.Source, "settings.json") || !strings.Contains(n.Note, "not valid JSON") {
		t.Errorf("env config dir: %+v", n)
	}
}

func TestReadNativeConfigCodex(t *testing.T) {
	dir := t.TempDir()
	cfg := "# codex\nmodel = \"gpt-5.5\"  # pinned\nmodel_reasoning_effort = 'high'\n\n[projects.\"/x\"]\ntrust_level = \"trusted\"\nmodel = \"ignored-in-table\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{NameCodex, NameCodexTmux} {
		n := ReadNativeConfig(typ, dir, noEnv)
		if n.Model != "gpt-5.5" || n.Effort != "high" || n.Note != "" {
			t.Errorf("%s: %+v", typ, n)
		}
	}
	n := ReadNativeConfig(NameCodex, t.TempDir(), noEnv)
	if n.Model != "" || !strings.Contains(n.Note, "no config.toml") {
		t.Errorf("missing: %+v", n)
	}
	if n := ReadNativeConfig(NameMock, "", noEnv); n.Note != "not inspectable" {
		t.Errorf("mock: %+v", n)
	}
}
