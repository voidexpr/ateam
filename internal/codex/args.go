package codex

import "strings"

// InteractiveArgs builds the argv for interactive Codex TUI mode.
func InteractiveArgs(base []string, model, effort string, extra []string) []string {
	args := make([]string, len(base))
	copy(args, base)
	args = withInteractiveDefaults(args, extra)
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
	}
	return append(args, extra...)
}

func withInteractiveDefaults(args, extra []string) []string {
	detectArgs := append(append([]string{}, args...), extra...)
	if !hasOption(detectArgs, "--no-alt-screen") {
		args = append(args, "--no-alt-screen")
	}
	if !hasOption(detectArgs, "--sandbox", "-s") && !hasOption(detectArgs, "--dangerously-bypass-approvals-and-sandbox") {
		args = append(args, "-s", "workspace-write")
	}
	if !hasOption(detectArgs, "--ask-for-approval", "-a") && !hasOption(detectArgs, "--dangerously-bypass-approvals-and-sandbox") {
		args = append(args, "-a", "never")
	}
	if !hasConfigOverride(detectArgs, updateCheckConfigKey) {
		args = append(args, "-c", updateCheckConfigKey+"=false")
	}
	for _, feature := range unattendedDisabledFeatures {
		if !hasFeatureOverride(detectArgs, feature) && !hasConfigOverride(detectArgs, "features."+feature) {
			args = append(args, "--disable", feature)
		}
	}
	return args
}

func hasOption(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func hasConfigOverride(args []string, key string) bool {
	for i, arg := range args {
		switch {
		case arg == "-c" || arg == "--config":
			if i+1 < len(args) && configOverrideKey(args[i+1]) == key {
				return true
			}
		case strings.HasPrefix(arg, "--config="):
			if configOverrideKey(strings.TrimPrefix(arg, "--config=")) == key {
				return true
			}
		}
	}
	return false
}

func hasFeatureOverride(args []string, feature string) bool {
	for i, arg := range args {
		switch {
		case arg == "--enable" || arg == "--disable":
			if i+1 < len(args) && args[i+1] == feature {
				return true
			}
		case strings.HasPrefix(arg, "--enable="):
			if strings.TrimPrefix(arg, "--enable=") == feature {
				return true
			}
		case strings.HasPrefix(arg, "--disable="):
			if strings.TrimPrefix(arg, "--disable=") == feature {
				return true
			}
		}
	}
	return false
}

func configOverrideKey(value string) string {
	value = strings.TrimSpace(value)
	key, _, _ := strings.Cut(value, "=")
	return strings.TrimSpace(key)
}
