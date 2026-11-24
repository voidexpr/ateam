package runtime

import (
	"fmt"
	"slices"
	"strings"
)

// DefaultProfile supplies the agent and container of a profile that sets
// neither, itself or through `use`.
const DefaultProfile = "default"

// originKey is the From key of one entry of a list attribute.
func originKey(attr, entry string) string { return attr + "\x00" + entry }

// Origin returns the profile a composed scalar attribute came from.
func (p *ProfileConfig) Origin(attr string) string { return p.From[attr] }

// EntryOrigin returns the profile one entry of a list attribute came from.
func (p *ProfileConfig) EntryOrigin(attr, entry string) string {
	return p.From[originKey(attr, entry)]
}

// composeProfiles replaces every profile by its composition with the
// profiles it uses. An unknown name or a cycle is an error: a profile that
// cannot be resolved must never run with less than it asked for.
func (c *Config) composeProfiles() error {
	raw := c.Profiles
	done := make(map[string]ProfileConfig, len(raw))
	for _, name := range sortedKeys(raw) {
		if _, err := composeProfile(raw, done, name, nil); err != nil {
			return err
		}
	}
	c.Profiles = done
	return nil
}

func sortedKeys(m map[string]ProfileConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func composeProfile(raw, done map[string]ProfileConfig, name string, stack []string) (ProfileConfig, error) {
	if p, ok := done[name]; ok {
		return p, nil
	}
	if slices.Contains(stack, name) {
		return ProfileConfig{}, fmt.Errorf("profile %q uses itself: %s", name, strings.Join(append(stack, name), " -> "))
	}
	own, ok := raw[name]
	if ok && own.AteamFiles != "" && own.AteamFiles != "open" && own.AteamFiles != "closed" {
		return ProfileConfig{}, fmt.Errorf("profile %q: ateam_files = %q; want \"open\" or \"closed\"", name, own.AteamFiles)
	}
	if !ok {
		if len(stack) == 0 {
			return ProfileConfig{}, fmt.Errorf("unknown profile %q", name)
		}
		return ProfileConfig{}, fmt.Errorf("profile %q uses unknown profile %q", stack[len(stack)-1], name)
	}
	out := ProfileConfig{Name: name, Use: own.Use, From: map[string]string{}}
	for _, used := range own.Use {
		p, err := composeProfile(raw, done, used, append(stack, name))
		if err != nil {
			return ProfileConfig{}, err
		}
		mergeComposed(&out, &p, func(key string) string { return p.From[key] })
	}
	mergeComposed(&out, &own, func(string) string { return name })
	done[name] = out
	return out, nil
}

// mergeComposed adds src to dst: lists add up, a scalar src sets wins.
// origin names the profile to record for a value src contributes.
func mergeComposed(dst, src *ProfileConfig, origin func(key string) string) {
	scalar := func(attr string, d *string, s string) {
		if s != "" {
			*d = s
			dst.From[attr] = origin(attr)
		}
	}
	scalar("agent", &dst.Agent, src.Agent)
	scalar("container", &dst.Container, src.Container)
	scalar("model", &dst.Model, src.Model)
	scalar("effort", &dst.Effort, src.Effort)
	scalar("max_budget_usd", &dst.MaxBudgetUSD, src.MaxBudgetUSD)
	scalar("ateam_files", &dst.AteamFiles, src.AteamFiles)
	if len(src.Command) > 0 {
		dst.Command = slices.Clone(src.Command)
		dst.From["command"] = origin("command")
	}
	set := func(attr string, d *[]string, s []string) {
		for _, entry := range s {
			if slices.Contains(*d, entry) {
				continue
			}
			*d = append(*d, entry)
			dst.From[originKey(attr, entry)] = origin(originKey(attr, entry))
		}
	}
	set("read", &dst.Read, src.Read)
	set("write", &dst.Write, src.Write)
	set("exclude", &dst.Exclude, src.Exclude)
	set("domains", &dst.Domains, src.Domains)
	set("network", &dst.Network, src.Network)
	// Arguments may repeat on purpose, so they are not deduplicated.
	dst.AgentExtraArgs = append(dst.AgentExtraArgs, src.AgentExtraArgs...)
	dst.ContainerExtraArgs = append(dst.ContainerExtraArgs, src.ContainerExtraArgs...)
}

// ComposeProfiles composes the named profiles in order, then extra on top:
// what a launch with several --profile flags and its own paths runs with.
// The result is named after its profiles ("a+b"), or "(flags)" without one.
func (c *Config) ComposeProfiles(names []string, extra ProfileConfig) (ProfileConfig, error) {
	out := ProfileConfig{Name: "(flags)", Use: names, From: map[string]string{}}
	for _, name := range names {
		p, ok := c.Profiles[name]
		if !ok {
			return ProfileConfig{}, fmt.Errorf("unknown profile %q (known: %s)", name, strings.Join(sortedKeys(c.Profiles), ", "))
		}
		mergeComposed(&out, &p, func(key string) string { return p.From[key] })
	}
	if len(names) > 0 {
		out.Name = strings.Join(names, "+")
	}
	mergeComposed(&out, &extra, func(string) string { return "command line" })
	return out, nil
}

// ResolveComposed returns the agent and container blocks a composed
// profile runs with, taking from the default profile what it leaves unset.
func (c *Config) ResolveComposed(prof ProfileConfig) (*ProfileConfig, *AgentConfig, *ContainerConfig, error) {
	return c.resolveBlocks(prof)
}

func (c *Config) resolveBlocks(prof ProfileConfig) (*ProfileConfig, *AgentConfig, *ContainerConfig, error) {
	prof.From = cloneFrom(prof.From)
	if def, ok := c.Profiles[DefaultProfile]; ok {
		if prof.Agent == "" {
			prof.Agent = def.Agent
			prof.From["agent"] = DefaultProfile
		}
		if prof.Container == "" {
			prof.Container = def.Container
			prof.From["container"] = DefaultProfile
		}
	}
	ac, ok := c.Agents[prof.Agent]
	if !ok {
		return nil, nil, nil, fmt.Errorf("profile %q references unknown agent %q", prof.Name, prof.Agent)
	}
	cc, ok := c.Containers[prof.Container]
	if !ok {
		return nil, nil, nil, fmt.Errorf("profile %q references unknown container %q", prof.Name, prof.Container)
	}
	return &prof, &ac, &cc, nil
}

func cloneFrom(from map[string]string) map[string]string {
	out := make(map[string]string, len(from)+2)
	for k, v := range from {
		out[k] = v
	}
	return out
}

// ProfilesDefinedIn returns the names of the profiles a file defines,
// sorted: what `--profile FILE` selects.
func (c *Config) ProfilesDefinedIn(file string) []string {
	var names []string
	for name := range c.Profiles {
		prefix := "profile." + name + "."
		defined := false
		for key, chain := range c.Sources {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			if slices.ContainsFunc(chain, func(src Contribution) bool { return src.Layer == file }) {
				defined = true
				break
			}
		}
		if defined {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// AgentProfilePrefix names the profiles holding what an agent type needs
// under a sandbox backend: its own files and API hosts ("agent/claude").
const AgentProfilePrefix = "agent/"

// WithAgentProfile returns prof with the agent/<type> profile merged in
// beneath it, when one exists: a sandboxed agent must reach its own files
// whatever profile launched it.
func (c *Config) WithAgentProfile(prof ProfileConfig, agentType string) ProfileConfig {
	own, ok := c.Profiles[AgentProfilePrefix+agentType]
	if !ok {
		return prof
	}
	out := ProfileConfig{Name: prof.Name, Use: prof.Use, From: map[string]string{}}
	mergeComposed(&out, &own, func(key string) string { return own.From[key] })
	mergeComposed(&out, &prof, func(key string) string { return prof.From[key] })
	return out
}
