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
		fmt.Fprintf(&b, "Missing required secret: %s", formatRequirement(missing[0]))
	} else {
		b.WriteString("Missing required secrets:")
		for _, m := range missing {
			fmt.Fprintf(&b, "\n  - %s", formatRequirement(m))
		}
	}
	// Suggest the first concrete var name for the set command.
	first := strings.SplitN(missing[0], "|", 2)[0]
	fmt.Fprintf(&b, "\n\nSet it with: ateam secret %s", first)

	// Add container-specific guidance when inside Docker.
	if isInDocker() {
		fmt.Fprintf(&b, "\n\nInside Docker container:")
		fmt.Fprintf(&b, "\n  On the host: ateam secret %s --set", first)
		fmt.Fprintf(&b, "\n  Then: ateam secret --save-project-scope")
		fmt.Fprintf(&b, "\n  Or pass directly: docker run -e %s=...", first)
	}
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
				_ = os.Setenv(alt, result.Value)
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

// ResolveDetail describes the resolution result for a single secret.
type ResolveDetail struct {
	Name    string
	Found   bool
	Source  string // "env", "project", "org", "global"
	Backend string // "env", "file", "keychain"
	Masked  string // "sk-a...zQAA"
}

// ResolveAllRequired resolves all required_env and forward_env entries
// without injecting into the process environment. For display/diagnostic use.
func ResolveAllRequired(ac *runtime.AgentConfig, forwardEnv []string, resolver *Resolver) []ResolveDetail {
	seen := map[string]bool{}
	var details []ResolveDetail

	// Resolve required_env entries.
	for _, req := range ac.RequiredEnv {
		for _, alt := range strings.Split(req, "|") {
			alt = strings.TrimSpace(alt)
			if alt == "" || seen[alt] {
				continue
			}
			seen[alt] = true
			details = append(details, resolveOneDetail(alt, resolver))
		}
	}

	// Resolve forward_env entries not already covered.
	for _, key := range forwardEnv {
		if seen[key] {
			continue
		}
		seen[key] = true
		details = append(details, resolveOneDetail(key, resolver))
	}

	return details
}

func resolveOneDetail(name string, resolver *Resolver) ResolveDetail {
	if resolver == nil {
		return ResolveDetail{Name: name}
	}
	r := resolver.Resolve(name)
	if !r.Found {
		return ResolveDetail{Name: name}
	}
	masked := r.Value
	if len(masked) > 8 {
		masked = masked[:4] + "..." + masked[len(masked)-4:]
	} else if len(masked) > 0 {
		masked = "***"
	}
	return ResolveDetail{
		Name:    name,
		Found:   true,
		Source:  r.Source,
		Backend: r.Backend,
		Masked:  masked,
	}
}

func isInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
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
