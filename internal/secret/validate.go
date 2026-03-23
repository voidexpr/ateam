package secret

import (
	"fmt"
	"os"
	"strings"

	"github.com/ateam/internal/runtime"
)

// ValidateSecrets checks that all required env vars for the given agent are
// available. Found secrets are injected into the process environment via
// os.Setenv so that container forward_env and agent code can pick them up.
//
// Note: container forward_env is NOT validated here — it's opportunistic
// forwarding. Docker silently skips missing forward_env vars. The agent's
// required_env is the authoritative declaration of what must be present.
func ValidateSecrets(ac *runtime.AgentConfig, resolver *Resolver) error {
	if resolver == nil {
		return nil
	}

	var missing []string
	for _, req := range ac.RequiredEnv {
		if !resolveRequirement(req, resolver) {
			missing = append(missing, req)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	if len(missing) == 1 {
		b.WriteString(fmt.Sprintf("Missing required secret: %s", formatRequirement(missing[0])))
	} else {
		b.WriteString("Missing required secrets:")
		for _, m := range missing {
			b.WriteString(fmt.Sprintf("\n  - %s", formatRequirement(m)))
		}
	}
	// Suggest the first concrete var name for the set command.
	first := strings.SplitN(missing[0], "|", 2)[0]
	b.WriteString(fmt.Sprintf("\n\nSet it with: ateam secret %s", first))
	return fmt.Errorf("%s", b.String())
}

// resolveRequirement checks a single requirement (possibly "A|B" alternatives).
// If found, injects the value into the process environment and returns true.
func resolveRequirement(req string, resolver *Resolver) bool {
	alternatives := strings.Split(req, "|")
	for _, alt := range alternatives {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		result := resolver.Resolve(alt)
		if result.Found {
			// Inject into process env if not already there.
			if result.Source != "env" {
				os.Setenv(alt, result.Value)
			}
			return true
		}
	}
	return false
}

// formatRequirement formats "A|B" as "A or B" for display.
func formatRequirement(req string) string {
	parts := strings.Split(req, "|")
	if len(parts) == 1 {
		return parts[0]
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, " or ")
}

// CollectRequiredEnvNames returns all unique env var names from required_env
// across all agents in the config. Useful for listing all known secrets.
func CollectRequiredEnvNames(cfg *runtime.Config) []string {
	seen := map[string]bool{}
	var names []string
	for _, ac := range cfg.Agents {
		for _, req := range ac.RequiredEnv {
			for _, alt := range strings.Split(req, "|") {
				alt = strings.TrimSpace(alt)
				if alt != "" && !seen[alt] {
					seen[alt] = true
					names = append(names, alt)
				}
			}
		}
	}
	// Also collect from container forward_env.
	for _, cc := range cfg.Containers {
		for _, key := range cc.ForwardEnv {
			if !seen[key] {
				seen[key] = true
				names = append(names, key)
			}
		}
	}
	return names
}
