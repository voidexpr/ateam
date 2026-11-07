package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/runtime"
)

// ReadNativeConfig reports what the agent CLI's own configuration would
// contribute to the model/effort decision if ateam passed no flags.
// configDir is the resolved config_dir of the run ("" = the CLI's default
// location); lookupEnv resolves env vars as the run would see them.
// Best-effort and read-only: a missing or unparsable file is reported in
// Note, never as an error.
//
//   - claude: <dir>/settings.json "model", then ANTHROPIC_MODEL in the env
//     (the env var outranks the file, as in the CLI)
//   - codex / codex-tmux: <CODEX_HOME>/config.toml "model" and
//     "model_reasoning_effort" (top-level keys)
//   - anything else: not inspectable
func ReadNativeConfig(agentType, configDir string, lookupEnv func(string) string) runtime.NativeConfig {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	switch agentType {
	case NameClaude, "":
		dir := configDir
		if dir == "" {
			dir = lookupEnv("CLAUDE_CONFIG_DIR")
		}
		if dir == "" {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, ".claude")
			}
		}
		n := readClaudeSettings(filepath.Join(dir, "settings.json"))
		if m := lookupEnv("ANTHROPIC_MODEL"); m != "" {
			n.Model = m
			n.Source = "ANTHROPIC_MODEL (env)"
			n.Note = ""
		}
		return n
	case NameCodex, NameCodexTmux:
		dir := configDir
		if dir == "" {
			dir = lookupEnv("CODEX_HOME")
		}
		if dir == "" {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, ".codex")
			}
		}
		return readCodexConfig(filepath.Join(dir, "config.toml"))
	}
	return runtime.NativeConfig{Source: agentType + " native config", Note: "not inspectable"}
}

func readClaudeSettings(path string) runtime.NativeConfig {
	n := runtime.NativeConfig{Source: tildeHome(path)}
	data, err := os.ReadFile(path)
	if err != nil {
		n.Note = "no settings.json; the CLI falls back to its account default"
		return n
	}
	var settings struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		n.Note = "settings.json is not valid JSON"
		return n
	}
	n.Model = settings.Model
	if n.Model == "" {
		n.Note = "settings.json sets no model; the CLI falls back to its account default"
	}
	return n
}

// readCodexConfig reads the top-level model and model_reasoning_effort
// keys of a codex config.toml with a line scan (the keys are plain
// strings and sit above any [table]).
func readCodexConfig(path string) runtime.NativeConfig {
	n := runtime.NativeConfig{Source: tildeHome(path)}
	data, err := os.ReadFile(path)
	if err != nil {
		n.Note = "no config.toml; the CLI falls back to its default model"
		return n
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "model":
			n.Model = value
		case "model_reasoning_effort":
			n.Effort = value
		}
	}
	if n.Model == "" && n.Effort == "" {
		n.Note = "config.toml sets no model; the CLI falls back to its default model"
	}
	return n
}

func tildeHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
