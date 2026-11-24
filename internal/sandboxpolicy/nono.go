package sandboxpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// NonoInfo is what the installed nono applies whatever a profile says.
type NonoInfo struct {
	// Blocked lists the paths nono's required groups deny. No profile
	// removes them; a grant plus bypass_protection lifts one path.
	Blocked []string
}

// NonoProfile is a nono profile file (`nono profile guide`, nono 0.82).
type NonoProfile struct {
	Extends    string         `json:"extends"`
	Meta       nonoMeta       `json:"meta"`
	Workdir    nonoWorkdir    `json:"workdir"`
	Filesystem nonoFilesystem `json:"filesystem"`
	Network    nonoNetwork    `json:"network"`
	// UnsafeSeatbelt holds raw macOS Seatbelt rules: nono has no read-only
	// rule inside a writable folder, Seatbelt does.
	UnsafeSeatbelt         []string `json:"unsafe_macos_seatbelt_rules,omitempty"`
	AllowParentOfProtected bool     `json:"allow_parent_of_protected,omitempty"`
}

type nonoMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type nonoWorkdir struct {
	Access string `json:"access"`
}

type nonoFilesystem struct {
	Read               []string `json:"read,omitempty"`
	Allow              []string `json:"allow,omitempty"`
	Deny               []string `json:"deny,omitempty"`
	BypassProtection   []string `json:"bypass_protection,omitempty"`
	SuppressSavePrompt []string `json:"suppress_save_prompt"`
}

type nonoNetwork struct {
	Block       bool     `json:"block,omitempty"`
	AllowDomain []string `json:"allow_domain,omitempty"`
	OpenPort    []int    `json:"open_port,omitempty"`
}

var nonoNameRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// nonoName turns a profile name into what nono accepts: letters, digits
// and inner hyphens.
func nonoName(name string) string {
	if out := strings.Trim(nonoNameRe.ReplaceAllString(name, "-"), "-"); out != "" {
		return out
	}
	return "ateam"
}

// RenderNono renders a policy as a nono profile for goos.
//
// macOS: the home is one read grant and the excluded paths are denied
// inside it. Linux: nono refuses a deny inside a grant, and the listing of
// the home around the excluded paths is not ported yet, so it is refused.
//
// nono's required deny groups (credentials, keychains, browsers, shell
// configs and history) cannot be removed from a profile, so each path they
// block that the policy does not exclude is lifted: the policy's excludes
// decide alone what is unreadable.
func RenderNono(p *Policy, info NonoInfo, goos string) (*NonoProfile, error) {
	if goos != "darwin" {
		return nil, fmt.Errorf("the nono backend supports macOS only for now (this is %s): on Linux nono refuses a deny inside a grant", goos)
	}
	excluded := func(path string) bool {
		return slices.ContainsFunc(p.Exclude, func(ex Rule) bool { return Within(path, ex.Path) })
	}
	// The profile decides what is unreadable, not nono's required groups:
	// every path they block that the policy leaves readable is lifted (the
	// home grant covers it). What the policy excludes stays blocked.
	var bypass []string
	for _, b := range info.Blocked {
		if Within(b, p.Home) && !excluded(b) {
			bypass = append(bypass, b)
		}
	}
	// A grant inside an excluded path keeps the exclude and reopens the
	// grant; one outside the home that nono blocks is lifted too.
	for _, g := range slices.Concat(p.Read, p.Write) {
		if excluded(g.Path) || slices.ContainsFunc(info.Blocked, func(b string) bool { return Within(g.Path, b) || Within(b, g.Path) }) {
			bypass = append(bypass, g.Path)
		}
	}
	prof := &NonoProfile{
		Extends: "default",
		Meta:    nonoMeta{Name: nonoName("ateam-" + p.Name), Description: "ateam profile " + p.Name},
		Workdir: nonoWorkdir{Access: "readwrite"},
		Filesystem: nonoFilesystem{
			Read:             sortedPaths(append([]Rule{{Path: p.Home}}, p.Read...)),
			Allow:            sortedPaths(p.Write),
			Deny:             sortedPaths(p.Exclude),
			BypassProtection: sortedStrings(bypass),
			// nono's exit menu would save grants into a profile of its own:
			// rules live in ateam profiles only.
			SuppressSavePrompt: []string{"/"},
		},
		// The home holds nono's own state, which a grant may only cover
		// with this flag (macOS only).
		AllowParentOfProtected: true,
	}
	prof.UnsafeSeatbelt = readOnlyRules(p)
	if err := p.checkListenWithoutPrivateNetwork("nono on macOS"); err != nil {
		return nil, err
	}
	switch {
	case len(p.DeniedDomains) > 0:
		return nil, fmt.Errorf("the nono backend has no checked rule to deny a host inside the allowed ones (%s): use the fence backend, or drop the deny", strings.Join(p.DeniedDomains, ", "))
	case p.NetworkClosed():
		// nono leaves the network open without a domain list.
		prof.Network = nonoNetwork{Block: true}
	case len(p.Domains) == 0:
		// Without a domain list nono leaves the network open, and whether
		// a block plus an open port closes the rest was not checked.
		return nil, fmt.Errorf("the nono backend cannot open localhost alone yet: add at least one host, or use the fence backend")
	default:
		// Port 0 is all of localhost: it covers the listed ports.
		prof.Network = nonoNetwork{AllowDomain: sortedStrings(p.Domains), OpenPort: []int{0}}
	}
	return prof, nil
}

// readOnlyRules renders the policy's read-only paths as Seatbelt rules:
// no write below the path, and no rename or removal of the folders above
// it up to the writable folder holding it, which would move the path out
// from under its rule.
func readOnlyRules(p *Policy) []string {
	var rules []string
	for _, r := range p.ReadOnly {
		rules = append(rules, fmt.Sprintf("(deny file-write* (subpath %s))", seatbeltString(r.Path)))
	}
	for _, dir := range p.PinnedDirs() {
		rules = append(rules, fmt.Sprintf("(deny file-write* (literal %s))", seatbeltString(dir)))
	}
	return sortedStrings(rules)
}

// seatbeltString quotes s as a Seatbelt (Scheme) string.
func seatbeltString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func sortedPaths(rules []Rule) []string {
	paths := make([]string, 0, len(rules))
	for _, r := range rules {
		paths = append(paths, r.Path)
	}
	return sortedStrings(paths)
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return slices.Compact(out)
}

// NonoPrefix is the nono command line up to the command to run. silent
// keeps nono's banner and summary off the terminal: for unattended runs.
func NonoPrefix(nono, profileFile, workDir string, silent bool) []string {
	args := []string{nono, "run"}
	if silent {
		args = append(args, "--silent")
	}
	// ATEAM_IN_SANDBOX tells an ateam started inside that a sandbox is
	// already in place: it must not start another, which cannot nest.
	return append(args, "--profile", profileFile, "--workdir", workDir, "--allow-cwd", "--", "/usr/bin/env", InSandboxEnv+"=1")
}

// InSandboxEnv is set for every command ateam runs under a sandbox backend.
const InSandboxEnv = "ATEAM_IN_SANDBOX"

// QueryNono asks the installed nono for the paths its required groups
// deny on this platform, keeping those that exist.
func QueryNono(ctx context.Context, nono string) (NonoInfo, error) {
	type group struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
	}
	var here []group
	if err := nonoJSON(ctx, nono, &here, "profile", "groups"); err != nil {
		return NonoInfo{}, err
	}
	var info NonoInfo
	for _, g := range here {
		if !g.Required {
			continue
		}
		var detail struct {
			Deny struct {
				Access []struct {
					Expanded string `json:"expanded"`
				} `json:"access"`
			} `json:"deny"`
		}
		if err := nonoJSON(ctx, nono, &detail, "profile", "groups", g.Name); err != nil {
			return NonoInfo{}, err
		}
		for _, a := range detail.Deny.Access {
			// nono lists every path it knows; lifting one that is not
			// there only draws a warning per launch.
			if _, err := os.Lstat(a.Expanded); err == nil {
				info.Blocked = append(info.Blocked, filepath.Clean(a.Expanded))
			}
		}
	}
	return info, nil
}

// inspect builds a nono command that only reads or checks profiles. Its
// update check is turned off: it costs seconds per call and a launch makes
// several.
func inspect(ctx context.Context, nono string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, nono, args...)
	cmd.Env = append(os.Environ(), "NONO_NO_UPDATE_CHECK=1")
	return cmd
}

func nonoJSON(ctx context.Context, nono string, out any, args ...string) error {
	args = append(args, "--json")
	data, err := inspect(ctx, nono, args...).Output()
	if err != nil {
		return fmt.Errorf("nono %s: %w", strings.Join(args, " "), err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("nono %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// WriteNonoProfile writes the profile beside dest, has nono validate it,
// then renames it: an invalid profile never replaces a good one.
func WriteNonoProfile(ctx context.Context, nono string, prof *NonoProfile, dest string) error {
	return writeValidated(prof, dest, func(tmp string) error {
		if out, err := inspect(ctx, nono, "profile", "validate", tmp).CombinedOutput(); err != nil {
			return fmt.Errorf("nono refuses the profile: %s", strings.TrimSpace(string(out)))
		}
		return nil
	})
}

// writeValidated writes a backend profile as indented JSON beside dest,
// has validate accept that file, then renames it: a refused profile never
// replaces a good one, and a launch never reads half a file.
func writeValidated(profile any, dest string, validate func(tmp string) error) error {
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp := dest + ".part.json"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := validate(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
