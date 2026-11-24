// Package sandboxpolicy turns a composed ateam profile into a
// backend-neutral file and network policy, and renders it for a sandbox
// backend. A rule a backend cannot express is an error, never dropped.
//
// Design: plans/feature_sandboxing_and_attended_agents.md.
package sandboxpolicy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Rule is one path of a policy with where it came from, for auditing.
type Rule struct {
	Path   string // absolute, cleaned
	Source string // the profile that set it, "command line", or "ateam" for an implicit rule
	Reason string // why ateam added it; "" for a rule the user wrote
	// Reopens is set on a read that opens a path an exclude closed.
	Reopens bool
}

// ListenPort is a port the sandboxed command serves on. Public is false
// for this machine only (127.0.0.1) and true for every interface
// (0.0.0.0): other machines can then reach it.
type ListenPort struct {
	Port   int
	Public bool
}

// Addr is the address the port is reachable at from outside the sandbox.
func (l ListenPort) Addr() string {
	if l.Public {
		return fmt.Sprintf("0.0.0.0:%d", l.Port)
	}
	return fmt.Sprintf("127.0.0.1:%d", l.Port)
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
	// ReadOnly lists paths inside a writable folder that stay readable
	// but must not be written, renamed or removed: a read that reopens an
	// excluded path there, and the files of the working folder's
	// repository that git runs or reads outside the sandbox later.
	ReadOnly []Rule
	// Domains lists the hosts reachable; none closes the network. With at
	// least one host the command can also use localhost for itself: on
	// macOS that is the host's localhost, every port (a sandbox there
	// cannot tell its own from the host's); on Linux a private one.
	Domains []string
	// DeniedDomains are refused even when a Domains entry covers them.
	DeniedDomains []string
	// HostLocalhost opens the host's localhost, every port; HostPorts
	// opens single ports of it.
	HostLocalhost bool
	HostPorts     []int
	// Listen lists the ports the command serves on, for this machine or
	// for other machines to reach.
	Listen []ListenPort
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
	// Domains and Deny are network entries: a host ("github.com",
	// "*.github.com"), and for Domains also "localhost" or
	// "localhost:PORT".
	Domains []string
	Deny    []string
	// Listen entries: "PORT", "localhost:PORT", "127.0.0.1:PORT" (this
	// machine only) or "0.0.0.0:PORT" (every interface).
	Listen []string
	// Origin names the profile an entry of a list attribute came from.
	Origin func(attr, entry string) string
	// Filters resolves the entries that are filter names.
	Filters FilterSet

	// OpenAteam leaves ateam's own files reachable and writable: for a
	// command that runs ateam itself.
	OpenAteam    bool
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

// ateamHomeNames is closed in the user's ateam directory and in its state
// folder even before it exists, when one of them is split around a grant.
var ateamHomeNames = append(slices.Clone(ateamClosed), "state", "agents", "cache", "filters", "prompts", "projects.hcl", "sandbox")

// Build resolves the entries of a profile into absolute rules and adds
// ateam's own. An entry is a path (~ or / first) or the name of a filter;
// an unknown name is an error: it must not grant nothing silently.
func Build(in Input) (*Policy, error) {
	if !filepath.IsAbs(in.Home) || !filepath.IsAbs(in.WorkDir) {
		return nil, fmt.Errorf("sandbox policy: home %q and working folder %q must be absolute", in.Home, in.WorkDir)
	}
	home, workDir := filepath.Clean(in.Home), filepath.Clean(in.WorkDir)
	if workDir == home || workDir == "/" {
		return nil, fmt.Errorf("refusing to run in %s: everything below it would be writable", workDir)
	}
	p := &Policy{Name: in.Name, Home: home, WorkDir: workDir}
	if err := p.addNetwork(in.Domains, in.Deny); err != nil {
		return nil, err
	}
	if err := p.addListen(in.Listen); err != nil {
		return nil, err
	}
	origin := in.Origin
	if origin == nil {
		origin = func(string, string) string { return "" }
	}
	var excluded []Rule
	// add resolves one path of an entry. A grant is compared with excludes
	// by where it really is: ~/projects/x and the folder that link leads
	// to are one path.
	add := func(list *[]Rule, attr, entry, path string) error {
		source := origin(attr, entry)
		if !isPathEntry(entry) {
			source += ", filter " + entry
		}
		abs, err := ExpandPath(path, home)
		if err != nil {
			return fmt.Errorf("%s entry %q (%s): %w", attr, entry, origin(attr, entry), err)
		}
		if list != &excluded {
			abs = RealPath(abs)
		}
		*list = append(*list, Rule{Path: abs, Source: source})
		return nil
	}
	// expand resolves the entries of one attribute. A path goes to grants
	// (the exclude attribute: to excluded). A filter name gives its
	// included paths to grants and its excluded paths to excluded; for
	// the exclude attribute every path it names is excluded.
	expand := func(attr string, entries []string, grants *[]Rule) error {
		for _, entry := range entries {
			if isPathEntry(entry) {
				if err := add(grants, attr, entry, entry); err != nil {
					return err
				}
				continue
			}
			include, exclude, err := in.Filters.Paths(entry, home)
			if err != nil {
				return fmt.Errorf("%s entry %q (%s): %w", attr, entry, origin(attr, entry), err)
			}
			for _, path := range include {
				if err := add(grants, attr, entry, path); err != nil {
					return err
				}
			}
			for _, path := range exclude {
				if err := add(&excluded, attr, entry, path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := expand("read", in.Read, &p.Read); err != nil {
		return nil, err
	}
	if err := expand("write", in.Write, &p.Write); err != nil {
		return nil, err
	}
	if err := expand("exclude", in.Exclude, &excluded); err != nil {
		return nil, err
	}
	// opensAteam reports whether a grant opens one of ateam's own rules.
	opensAteam := func(grant, path string) bool { return false }
	if in.OpenAteam {
		for _, dir := range []string{in.AteamHome, in.ProjectDir} {
			if dir != "" {
				p.Write = append(p.Write, Rule{Path: RealPath(dir), Source: "ateam", Reason: "ateam_files is open: a command that runs ateam"})
			}
		}
	} else {
		// A grant that names a place of ateam's opens ateam's own rules
		// below it: `write = ["<project>/.ateam"]` means all of it.
		ateamDirs := []string{}
		for _, dir := range []string{in.AteamHome, in.ProjectDir} {
			if dir != "" {
				ateamDirs = append(ateamDirs, withRealPath(filepath.Clean(dir))...)
			}
		}
		inAteam := func(path string) bool {
			return slices.ContainsFunc(ateamDirs, func(dir string) bool { return Within(path, dir) })
		}
		opensAteam = func(grant, path string) bool {
			return inAteam(grant) && Within(RealPath(path), grant)
		}
		// An unattended run writes inside the user's ateam directory (its
		// output, the agent's config folder). The directory is then closed
		// entry by entry around those grants, as the project's .ateam is:
		// no backend has to open a path inside a closed folder.
		grants := pathsOf(slices.Concat(p.Read, p.Write))
		named := func(dir string) []string {
			if home := filepath.Clean(in.AteamHome); in.AteamHome != "" && (dir == home || dir == filepath.Join(home, "state")) {
				return slices.Clone(ateamHomeNames)
			}
			return nil
		}
		for _, ex := range implicitExcludes(in) {
			excluded = append(excluded, splitAround(ex, grants, named)...)
		}
	}

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
		// An excluded path reached through a link is excluded where the
		// link leads too, and reopened by a grant of either.
		both := withRealPath(ex.Path)
		own := ex.Source == "ateam"
		for i := range p.Read {
			read := p.Read[i].Path
			if slices.ContainsFunc(both, func(path string) bool { return Within(read, path) }) || own && opensAteam(read, ex.Path) {
				p.Read[i].Reopens = true
			}
		}
		if slices.ContainsFunc(both, granted) {
			continue
		}
		if own && slices.ContainsFunc(slices.Concat(p.Read, p.Write), func(g Rule) bool { return opensAteam(g.Path, ex.Path) }) {
			continue
		}
		for _, path := range both {
			if !containsPath(p.Exclude, path) {
				p.Exclude = append(p.Exclude, Rule{Path: path, Source: ex.Source, Reason: ex.Reason})
			}
		}
	}
	for _, r := range p.Read {
		if r.Reopens && !containsPath(p.Write, r.Path) && p.WritableRoot(r.Path) != "" {
			p.ReadOnly = append(p.ReadOnly, r)
		}
	}
	if info, err := os.Stat(filepath.Join(workDir, ".git")); err == nil && info.IsDir() {
		for _, name := range guarded {
			p.ReadOnly = append(p.ReadOnly, Rule{Path: filepath.Join(workDir, name), Source: "ateam",
				Reason: "git runs or reads it outside the sandbox later"})
		}
	}
	return p, nil
}

// addNetwork sorts network entries into hosts, the host's localhost and
// its ports. A form no backend takes is refused here, by name.
func (p *Policy) addNetwork(allow, deny []string) error {
	for _, entry := range allow {
		host, port, hasPort := entry, "", false
		if h, p, err := net.SplitHostPort(entry); err == nil {
			host, port, hasPort = h, p, true
		}
		// 127.0.0.1 and ::1 are localhost. Any other address is refused:
		// a backend's proxy filters names, and what it does with an
		// address was not checked.
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
			switch {
			case ip.IsLoopback():
				host = "localhost"
			case ip.IsUnspecified():
				return fmt.Errorf("network entry %q: this is an address a server listens on, not one to reach; a port the command listens on is not supported yet", entry)
			default:
				return fmt.Errorf("network entry %q: an IP address is not supported yet; write a host name, or localhost", entry)
			}
		}
		switch {
		case entry == "" || strings.ContainsAny(entry, "/ "):
			return fmt.Errorf("network entry %q: want a host, localhost or localhost:PORT", entry)
		case host == "localhost" && !hasPort:
			p.HostLocalhost = true
		case host == "localhost":
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("network entry %q: the port must be a number from 1 to 65535", entry)
			}
			if !slices.Contains(p.HostPorts, n) {
				p.HostPorts = append(p.HostPorts, n)
			}
		case hasPort:
			return fmt.Errorf("network entry %q: a port on a host is not supported yet; only localhost:PORT", entry)
		case strings.Contains(host, ":"):
			return fmt.Errorf("network entry %q: want a host, localhost or localhost:PORT", entry)
		case !slices.Contains(p.Domains, host):
			p.Domains = append(p.Domains, host)
		}
	}
	for _, entry := range deny {
		if entry == "" || strings.ContainsAny(entry, ":/ ") || entry == "localhost" {
			return fmt.Errorf("network deny entry %q: want a host", entry)
		}
		if !slices.Contains(p.DeniedDomains, entry) {
			p.DeniedDomains = append(p.DeniedDomains, entry)
		}
	}
	slices.Sort(p.HostPorts)
	return nil
}

// addListen parses the ports the command serves on.
func (p *Policy) addListen(entries []string) error {
	for _, entry := range entries {
		host, port := "localhost", entry
		if h, pt, err := net.SplitHostPort(entry); err == nil {
			host, port = h, pt
		}
		l := ListenPort{}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		switch {
		case host == "localhost" || ip != nil && ip.IsLoopback():
		case ip != nil && ip.IsUnspecified():
			l.Public = true
		default:
			return fmt.Errorf("listen entry %q: want PORT, 127.0.0.1:PORT (this machine only) or 0.0.0.0:PORT (every interface)", entry)
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("listen entry %q: the port must be a number from 1 to 65535", entry)
		}
		l.Port = n
		if !slices.Contains(p.Listen, l) {
			p.Listen = append(p.Listen, l)
		}
	}
	return nil
}

// checkListenWithoutPrivateNetwork is the rule of every backend that
// shares this machine's localhost with the sandbox (macOS): a served port
// is reachable when all of localhost is open, which such a backend cannot
// narrow to one port, and never from other machines.
func (p *Policy) checkListenWithoutPrivateNetwork(backend string) error {
	for _, l := range p.Listen {
		if l.Public {
			return fmt.Errorf("%s cannot let other machines reach a port (listen %s): only 127.0.0.1", backend, l.Addr())
		}
	}
	if len(p.Listen) > 0 && !p.LocalhostOpen() {
		return fmt.Errorf("%s opens all of localhost or none of it, not one served port (listen %d): add localhost to domains", backend, p.Listen[0].Port)
	}
	return nil
}

// LocalhostOpen reports whether a backend without a private network
// (macOS) must open all of localhost: asked for, or needed by a command
// that reaches hosts and talks to what it starts.
func (p *Policy) LocalhostOpen() bool {
	return p.HostLocalhost || len(p.Domains) > 0
}

// NetworkClosed reports whether the policy allows no network at all.
func (p *Policy) NetworkClosed() bool {
	return len(p.Domains) == 0 && !p.HostLocalhost && len(p.HostPorts) == 0 && len(p.Listen) == 0
}

// WritableRoot returns the writable folder holding path (the working
// folder or a write grant), "" when path is not inside one. A path that is
// itself a writable folder has no root: it is writable on purpose.
func (p *Policy) WritableRoot(path string) string {
	best := ""
	for _, root := range pathsOf(p.Writable()) {
		if path != root && Within(path, root) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

// Writable returns every writable folder: the working folder, then the
// write grants.
func (p *Policy) Writable() []Rule {
	return append([]Rule{{Path: p.WorkDir, Source: "the working folder"}}, p.Write...)
}

// PinnedDirs returns the folders above each read-only path, up to and not
// including the writable folder that holds it. Renaming or removing one
// would move the path out from under its rule: a backend must pin them.
func (p *Policy) PinnedDirs() []string {
	var dirs []string
	for _, r := range p.ReadOnly {
		root := p.WritableRoot(r.Path)
		for dir := filepath.Dir(r.Path); root != "" && dir != root && Within(dir, root); dir = filepath.Dir(dir) {
			dirs = append(dirs, dir)
		}
	}
	return sortedStrings(dirs)
}

func pathsOf(rules []Rule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Path)
	}
	return out
}

// splitAround returns ex, or, when a grant lies inside it, its entries
// level by level down to each grant: every entry that leads to no grant
// stays excluded. named gives the entries of a folder to close even when
// they do not exist yet. An entry created later under another name is not
// excluded: only for folders whose layout ateam owns.
func splitAround(ex Rule, grants []string, named func(dir string) []string) []Rule {
	real := RealPath(ex.Path)
	if !slices.ContainsFunc(grants, func(g string) bool { return g != real && Within(g, real) }) {
		return []Rule{ex}
	}
	names := named(ex.Path)
	if entries, err := os.ReadDir(ex.Path); err == nil {
		for _, e := range entries {
			if !slices.Contains(names, e.Name()) {
				names = append(names, e.Name())
			}
		}
	}
	slices.Sort(names)
	var out []Rule
	for _, name := range names {
		child := ex
		child.Path = filepath.Join(ex.Path, name)
		out = append(out, splitAround(child, grants, named)...)
	}
	return out
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
	return "", fmt.Errorf("not a path starting with ~ or /")
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

// RealPath returns where path really is, following links. For a path
// that does not exist yet, the links of the part that exists are followed.
func RealPath(path string) string {
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if dir == filepath.Dir(dir) {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// withRealPath returns path and, when it goes through a link, where it
// really is.
func withRealPath(path string) []string {
	if real := RealPath(path); real != path {
		return []string{path, real}
	}
	return []string{path}
}

func containsPath(rules []Rule, path string) bool {
	return slices.ContainsFunc(rules, func(r Rule) bool { return r.Path == path })
}
