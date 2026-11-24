package runtime

import (
	"fmt"
	"slices"
	"strings"
)

// NetworkConfig is a named set of network rules, tool-agnostic: profiles
// list them in `network`, and each sandbox backend maps the rules to its
// own or refuses the launch.
type NetworkConfig struct {
	Name string
	// Use names the networks merged into this one, in order.
	Use []string
	// Allow lists what is reachable: a host ("github.com", "*.github.com"),
	// "localhost" (the host's localhost, every port) or "localhost:PORT".
	Allow []string
	// Deny lists hosts refused even when an allow entry covers them.
	Deny []string
	// Default is what happens to the rest: "deny" (also when unset).
	Default string
}

// checkNetworks resolves every network and every profile's network list:
// an unknown name, a cycle or an unsupported default is a load error.
func (c *Config) checkNetworks() error {
	for _, name := range sortedNames(c.Networks) {
		if _, _, err := c.NetworkRules([]string{name}); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(c.Profiles) {
		if _, _, err := c.NetworkRules(c.Profiles[name].Network); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	return nil
}

func sortedNames(m map[string]NetworkConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// NetworkRules returns the allow and deny entries of the named networks,
// with the networks they use, in order and without repeats.
func (c *Config) NetworkRules(names []string) (allow, deny []string, err error) {
	seen := map[string]bool{}
	var visit func(name string, stack []string) error
	visit = func(name string, stack []string) error {
		if slices.Contains(stack, name) {
			return fmt.Errorf("network %q uses itself: %s", name, strings.Join(append(stack, name), " -> "))
		}
		n, ok := c.Networks[name]
		if !ok {
			if len(stack) > 0 {
				return fmt.Errorf("network %q uses unknown network %q", stack[len(stack)-1], name)
			}
			return fmt.Errorf("unknown network %q (known: %s)", name, strings.Join(sortedNames(c.Networks), ", "))
		}
		if n.Default != "" && n.Default != "deny" {
			return fmt.Errorf("network %q: default = %q; only \"deny\" is supported for now", name, n.Default)
		}
		if seen[name] {
			return nil
		}
		for _, used := range n.Use {
			if err := visit(used, append(stack, name)); err != nil {
				return err
			}
		}
		seen[name] = true
		allow = appendNew(allow, n.Allow)
		deny = appendNew(deny, n.Deny)
		return nil
	}
	for _, name := range names {
		if err := visit(name, nil); err != nil {
			return nil, nil, err
		}
	}
	return allow, deny, nil
}

func appendNew(list, entries []string) []string {
	for _, entry := range entries {
		if !slices.Contains(list, entry) {
			list = append(list, entry)
		}
	}
	return list
}
