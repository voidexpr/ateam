package secret

import (
	"fmt"
	"os"
	"strings"

	"github.com/ateam/internal/container"
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

	// Add container-specific guidance when inside a container.
	if container.IsInContainer() {
		fmt.Fprintf(&b, "\n\nInside Docker container:")
		fmt.Fprintf(&b, "\n  On the host: ateam secret %s --set", first)
		fmt.Fprintf(&b, "\n  Then: ateam secret --save-project-scope")
		fmt.Fprintf(&b, "\n  Or pass directly: docker run -e %s=...", first)
	}
	return fmt.Errorf("%s", b.String())
}

// resolveRequirement checks a single requirement (possibly "A|B" alternatives).
// If found, injects the value into the process environment and returns true.
// Always injects via os.Setenv, even when the source is "env", to ensure the
// resolved value is consistent after any prior stripping or overriding.
func resolveRequirement(req string, resolver *Resolver) bool {
	alternatives := strings.Split(req, "|")
	for _, alt := range alternatives {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		result := resolver.Resolve(alt)
		if result.Found {
			_ = os.Setenv(alt, result.Value)
			return true
		}
	}
	return false
}

// IsolationResult describes the outcome of credential isolation for one
// required_env group (e.g., "CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY").
type IsolationResult struct {
	ActiveKey    string   // the credential that will be used
	ActiveSource string   // where it came from: "project", "org", "global", "env"
	Stripped     []string // competing env vars removed from agent env
}

// IsolateCredentials modifies ac.Env so that:
//   - The resolved credential is overridden in the agent env
//   - Competing alternatives are stripped (set to "") from the agent env
//
// This prevents credential confusion: e.g., if CLAUDE_CODE_OAUTH_TOKEN was
// resolved from the secret store, ANTHROPIC_API_KEY is stripped from the agent
// process so Claude Code doesn't pick it up via its own auth priority.
//
// When multiple alternatives resolve, store-backed credentials are preferred
// over env-only fallbacks. This ensures ateam secret is authoritative even
// when a different alternative happens to exist in the environment.
func IsolateCredentials(ac *runtime.AgentConfig, resolver *Resolver) []IsolationResult {
	if resolver == nil {
		return nil
	}
	if ac.Env == nil {
		ac.Env = make(map[string]string)
	}
	var results []IsolationResult
	for _, req := range ac.RequiredEnv {
		alternatives := strings.Split(req, "|")

		// Resolve all alternatives, prefer store over env.
		var storeKey, storeVal, storeSource string
		var envKey, envVal string
		for _, alt := range alternatives {
			alt = strings.TrimSpace(alt)
			if alt == "" {
				continue
			}
			result := resolver.Resolve(alt)
			if !result.Found {
				continue
			}
			if result.Source != "env" && storeKey == "" {
				storeKey, storeVal, storeSource = alt, result.Value, result.Source
			} else if result.Source == "env" && envKey == "" {
				envKey, envVal = alt, result.Value
			}
		}

		resolvedKey, resolvedVal, resolvedSource := storeKey, storeVal, storeSource
		if resolvedKey == "" {
			resolvedKey, resolvedVal, resolvedSource = envKey, envVal, "env"
		}
		if resolvedKey == "" {
			continue
		}

		ac.Env[resolvedKey] = resolvedVal
		ir := IsolationResult{
			ActiveKey:    resolvedKey,
			ActiveSource: resolvedSource,
		}
		for _, alt := range alternatives {
			alt = strings.TrimSpace(alt)
			if alt == "" || alt == resolvedKey {
				continue
			}
			if _, exists := os.LookupEnv(alt); exists {
				ac.Env[alt] = ""
				ir.Stripped = append(ir.Stripped, alt)
			}
		}
		results = append(results, ir)
	}
	return results
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
	Status  string // "active", "stripped", "" (default/unset)
}

// ResolveAllRequired resolves all required_env and forward_env entries
// without injecting into the process environment. For display/diagnostic use.
// When ac.Env has been modified by IsolateCredentials, the Status field reflects
// whether each credential is active (will be used) or stripped (removed from agent env).
func ResolveAllRequired(ac *runtime.AgentConfig, forwardEnv []string, resolver *Resolver) []ResolveDetail {
	seen := map[string]bool{}
	var details []ResolveDetail

	// Resolve required_env entries, annotating active vs stripped.
	for _, req := range ac.RequiredEnv {
		for _, alt := range strings.Split(req, "|") {
			alt = strings.TrimSpace(alt)
			if alt == "" || seen[alt] {
				continue
			}
			seen[alt] = true
			d := resolveOneDetail(alt, resolver)
			if ac.Env != nil {
				if v, ok := ac.Env[alt]; ok {
					if v == "" {
						d.Status = "stripped"
					} else {
						d.Status = "active"
					}
				}
			}
			details = append(details, d)
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
	return ResolveDetail{
		Name:    name,
		Found:   true,
		Source:  r.Source,
		Backend: r.Backend,
		Masked:  MaskValue(r.Value),
	}
}

// MaskValue returns a short masked preview of a secret value: the first and
// last 4 characters for long values, "***" for anything 8 chars or shorter
// (including empty).
func MaskValue(val string) string {
	if len(val) <= 8 {
		return "***"
	}
	return val[:4] + "..." + val[len(val)-4:]
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
