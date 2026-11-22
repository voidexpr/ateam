// Package sandboxpolicy turns a composed ateam profile into a
// backend-neutral file and network policy, and renders it for a sandbox
// backend. A rule a backend cannot express is an error, never dropped.
//
// Design: plans/feature_sandboxing_and_attended_agents.md.
package sandboxpolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Rule is one path of a policy with where it came from, for auditing.
type Rule struct {
	Path   string // absolute, cleaned
	Source string // the profile that set it, "command line", or "ateam" for an implicit rule
	Reason string // why ateam added it; "" for a rule the user wrote
}

// Policy is what a sandboxed command may touch. Reading is open below
// Home, except Exclude; writing is the working folder plus Write.
type Policy struct {
	Name    string
	Home    string
	WorkDir string
	Read    []Rule // readable, beside the home
	Write   []Rule // writable, beside the working folder
	Exclude []Rule // neither readable nor writable
	Domains []string
	// Gaps lists what the policy means to protect and no backend rule
	// below does; a renderer adds its own. Shown at launch and in exports.
	Gaps []string
}

// Input is a composed profile and the places of one launch.
type Input struct {
	Name    string
	Home    string // the user's home directory
	WorkDir string // the command's working folder
	Read    []string
	Write   []string
	Exclude []string
	Domains []string
	// Origin names the profile an entry of a list attribute came from.
	Origin func(attr, entry string) string

	AteamHome    string   // the user's ateam directory (~/.ateam); "" to skip
	ProjectDir   string   // the project's .ateam directory; "" without a project
	ProfileFiles []string // profile files given by path for this launch
}

// ateamOpen is what a sandboxed command may reach in a project's .ateam:
// its own output, what agents share, the prompts, the test scripts a role
// generates, and the .gitignore git reads. Everything else there is
// ateam's own.
var ateamOpen = []string{"runtime", "shared", "prompts", "cache", ".gitignore"}

// ateamClosed is closed in a project's .ateam even before it exists, so a
// command cannot create the config its next launch would read.
var ateamClosed = []string{"ateam.hcl", "profiles", "secrets.env", "state.sqlite", "state.sqlite-wal", "state.sqlite-shm", "logs", "Dockerfile"}

// Build resolves the entries of a profile into absolute rules and adds
// ateam's own. An entry that is not a path is an error: filter names are
// not resolved yet, and an unknown entry must not grant nothing silently.
func Build(in Input) (*Policy, error) {
	if !filepath.IsAbs(in.Home) || !filepath.IsAbs(in.WorkDir) {
		return nil, fmt.Errorf("sandbox policy: home %q and working folder %q must be absolute", in.Home, in.WorkDir)
	}
	home, workDir := filepath.Clean(in.Home), filepath.Clean(in.WorkDir)
	if workDir == home || workDir == "/" {
		return nil, fmt.Errorf("refusing to run in %s: everything below it would be writable", workDir)
	}
	p := &Policy{Name: in.Name, Home: home, WorkDir: workDir, Domains: slices.Clone(in.Domains)}
	origin := in.Origin
	if origin == nil {
		origin = func(string, string) string { return "" }
	}
	expand := func(attr string, entries []string) ([]Rule, error) {
		var rules []Rule
		for _, entry := range entries {
			path, err := ExpandPath(entry, home)
			if err != nil {
				return nil, fmt.Errorf("%s entry %q (%s): %w", attr, entry, origin(attr, entry), err)
			}
			rules = append(rules, Rule{Path: path, Source: origin(attr, entry)})
		}
		return rules, nil
	}
	var err error
	if p.Read, err = expand("read", in.Read); err != nil {
		return nil, err
	}
	if p.Write, err = expand("write", in.Write); err != nil {
		return nil, err
	}
	excluded, err := expand("exclude", in.Exclude)
	if err != nil {
		return nil, err
	}
	excluded = append(excluded, implicitExcludes(in)...)

	// A read or write of exactly an excluded path reopens it: the nearer
	// profile decides. A wider grant does not: it keeps the narrower exclude.
	granted := func(path string) bool {
		for _, g := range slices.Concat(p.Read, p.Write) {
			if g.Path == path {
				return true
			}
		}
		return false
	}
	for _, ex := range excluded {
		if granted(ex.Path) {
			continue
		}
		// An excluded path reached through a link is excluded where the
		// link leads too.
		for _, path := range withRealPath(ex.Path) {
			if !containsPath(p.Exclude, path) {
				p.Exclude = append(p.Exclude, Rule{Path: path, Source: ex.Source, Reason: ex.Reason})
			}
		}
	}
	return p, nil
}

func implicitExcludes(in Input) []Rule {
	var rules []Rule
	add := func(path, reason string) {
		rules = append(rules, Rule{Path: filepath.Clean(path), Source: "ateam", Reason: reason})
	}
	if in.AteamHome != "" {
		add(in.AteamHome, "ateam's user configuration, state and secrets")
	}
	if in.ProjectDir != "" {
		names := slices.Clone(ateamClosed)
		if entries, err := os.ReadDir(in.ProjectDir); err == nil {
			for _, e := range entries {
				if !slices.Contains(ateamOpen, e.Name()) && !slices.Contains(names, e.Name()) {
					names = append(names, e.Name())
				}
			}
		}
		slices.Sort(names)
		for _, name := range names {
			add(filepath.Join(in.ProjectDir, name), "ateam's project configuration and state")
		}
	}
	for _, file := range in.ProfileFiles {
		add(file, "the profile of this launch")
	}
	return rules
}

// ExpandPath resolves a profile entry to an absolute path: "~", "~/x" or
// "/x". Anything else is refused.
func ExpandPath(entry, home string) (string, error) {
	switch {
	case entry == "~":
		return home, nil
	case strings.HasPrefix(entry, "~/"):
		return filepath.Join(home, entry[2:]), nil
	case filepath.IsAbs(entry):
		return filepath.Clean(entry), nil
	}
	return "", fmt.Errorf("not a path starting with ~ or /; filter names are not supported yet")
}

// Within reports whether path is base or lies below it.
func Within(path, base string) bool {
	if path == base {
		return true
	}
	if base == "/" {
		return true
	}
	return strings.HasPrefix(path, base+string(filepath.Separator))
}

// withRealPath returns path and, when it goes through a link, where it
// really is.
func withRealPath(path string) []string {
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		return []string{path, real}
	}
	return []string{path}
}

func containsPath(rules []Rule, path string) bool {
	return slices.ContainsFunc(rules, func(r Rule) bool { return r.Path == path })
}
