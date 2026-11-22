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
	Extends                string         `json:"extends"`
	Meta                   nonoMeta       `json:"meta"`
	Workdir                nonoWorkdir    `json:"workdir"`
	Filesystem             nonoFilesystem `json:"filesystem"`
	Network                nonoNetwork    `json:"network"`
	AllowParentOfProtected bool           `json:"allow_parent_of_protected,omitempty"`
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
// configs and history) stay in force; a path the policy grants explicitly
// inside one of them is lifted.
func RenderNono(p *Policy, info NonoInfo, goos string) (*NonoProfile, error) {
	if goos != "darwin" {
		return nil, fmt.Errorf("the nono backend supports macOS only for now (this is %s): on Linux nono refuses a deny inside a grant", goos)
	}
	explicit := slices.Concat(p.Read, p.Write)
	// A grant inside an excluded path keeps the exclude and reopens the
	// grant; one inside a path nono blocks needs lifting too.
	var bypass []string
	for _, g := range explicit {
		inside := slices.ContainsFunc(p.Exclude, func(ex Rule) bool { return Within(g.Path, ex.Path) }) ||
			slices.ContainsFunc(info.Blocked, func(b string) bool { return Within(g.Path, b) || Within(b, g.Path) })
		if inside {
			bypass = append(bypass, g.Path)
		}
	}
	// A write grant holding a blocked path lifts nothing below it by
	// itself: name the blocked paths it holds, so the grant means what it says.
	for _, w := range p.Write {
		for _, b := range info.Blocked {
			if Within(b, w.Path) && b != w.Path && !slices.ContainsFunc(p.Exclude, func(ex Rule) bool { return Within(b, ex.Path) }) {
				bypass = append(bypass, b)
			}
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
	if len(p.Domains) == 0 {
		// nono leaves the network open without a domain list.
		prof.Network = nonoNetwork{Block: true}
	} else {
		prof.Network = nonoNetwork{AllowDomain: sortedStrings(p.Domains), OpenPort: []int{0}}
	}
	return prof, nil
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

// JSON returns the profile as an indented JSON document.
func (n *NonoProfile) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// NonoCommand is the command line that runs argv under a profile file.
func NonoCommand(nono, profileFile, workDir string, argv []string) []string {
	return append([]string{nono, "run", "--profile", profileFile, "--workdir", workDir, "--allow-cwd", "--"}, argv...)
}

// QueryNono asks the installed nono for the paths its required groups
// deny on this platform.
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
			info.Blocked = append(info.Blocked, filepath.Clean(a.Expanded))
		}
	}
	return info, nil
}

func nonoJSON(ctx context.Context, nono string, out any, args ...string) error {
	args = append(args, "--json")
	data, err := exec.CommandContext(ctx, nono, args...).Output()
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
	data, err := prof.JSON()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp := dest + ".part.json"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, nono, "profile", "validate", tmp).CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("nono refuses the profile: %s", strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, dest)
}
