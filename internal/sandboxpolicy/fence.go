package sandboxpolicy

import (
	"fmt"
	"os"
	"slices"
)

// FenceConfig is a fence settings file (fence.json, fence 0.1.67): what
// ateam's embedded sandbox loads, and what `fence --settings` reads.
type FenceConfig struct {
	Network    fenceNetwork    `json:"network"`
	Filesystem fenceFilesystem `json:"filesystem"`
	Command    fenceCommand    `json:"command"`
	AllowPty   bool            `json:"allowPty"`
}

type fenceNetwork struct {
	AllowedDomains          []string `json:"allowedDomains"`
	DeniedDomains           []string `json:"deniedDomains"`
	AllowLocalBinding       bool     `json:"allowLocalBinding"`
	AllowLocalOutbound      bool     `json:"allowLocalOutbound"`
	AllowLocalOutboundPorts []int    `json:"allowLocalOutboundPorts,omitempty"`
}

type fenceFilesystem struct {
	DefaultDenyRead bool     `json:"defaultDenyRead"`
	AllowRead       []string `json:"allowRead"`
	DenyRead        []string `json:"denyRead"`
	AllowWrite      []string `json:"allowWrite"`
	DenyWrite       []string `json:"denyWrite"`
}

type fenceCommand struct {
	UseDefaults bool `json:"useDefaults"`
}

// FenceNotes is what fence applies on goos whatever a policy says. None of
// it opens a path the policy excludes. Shown at launch and in exports.
func FenceNotes(goos string) []string {
	notes := []string{
		"fence also refuses writes to .gitmodules, .mcp.json, .ripgreprc, .vscode, .idea and shell startup files in the working folder, and to shell startup files and .gitconfig in the home; no setting lifts this",
		"a new pseudo-terminal may be opened",
	}
	if goos == "linux" {
		return append(notes,
			"fence also lets ~/.npm/_logs be written and the system folders be read; /tmp is a private, empty folder",
			"the sandbox has a localhost of its own; the host's is closed, but for the ports a profile lists as localhost:PORT")
	}
	return append(notes, "fence also lets its own temporary folder (/tmp/fence), the per-user temporary folder and ~/.npm/_logs be written, and the system folders, /tmp and /opt be read")
}

// FenceGaps lists the rules of a policy fence does not enforce on goos.
//
// On Linux fence closes a path with a mount, which needs the path to
// exist. An excluded or read-only path that does not exist yet inside a
// writable folder can be created by the command: closed "before it
// exists" does not hold there. Outside a writable folder nothing can be
// created anyway.
func FenceGaps(p *Policy, goos string) []Rule {
	if goos != "linux" {
		return nil
	}
	var gaps []Rule
	for _, r := range slices.Concat(p.Exclude, p.ReadOnly) {
		if _, err := os.Lstat(r.Path); err != nil && p.WritableRoot(r.Path) != "" {
			gaps = append(gaps, r)
		}
	}
	return gaps
}

// RenderFence renders a policy as a fence configuration for goos.
//
// Reading is closed by default and opened for the home and the policy's
// grants; fence adds the system folders. In fence a deny always wins over
// an allow, for reads and for writes:
//   - an excluded path is denied for both: a write grant around it would
//     otherwise leave it writable;
//   - a read-only path is a write deny, and fence refuses the rename of
//     the folders above a denied path;
//   - a grant inside a folder that stays excluded cannot be expressed, and
//     is refused.
//
// Localhost: macOS has all of it or none; Linux gives the sandbox one of
// its own and reaches the host's one listed port at a time. A form the
// system does not have is refused. See FenceGaps for what Linux does not
// enforce.
func RenderFence(p *Policy, goos string) (*FenceConfig, error) {
	if goos != "darwin" && goos != "linux" {
		return nil, fmt.Errorf("the fence backend supports macOS and Linux (this is %s)", goos)
	}
	for _, grant := range slices.Concat(p.Read, p.Write) {
		for _, ex := range p.Exclude {
			if Within(grant.Path, ex.Path) {
				return nil, fmt.Errorf("fence cannot open %s (%s) inside the excluded %s (%s): in fence a denied folder stays denied as a whole. Exclude the entries of %s one by one, or use the nono backend",
					grant.Path, ruleOrigin(grant), ex.Path, ruleOrigin(ex), ex.Path)
			}
		}
	}
	writable := p.Writable()
	if goos == "linux" {
		// On Linux a read-only path is a mount, and the folder above a
		// mount can be renamed away. A folder that is a mount of its own
		// cannot: each folder above a read-only path is listed as
		// writable, which fence binds onto itself. On macOS fence refuses
		// the rename itself.
		for _, dir := range p.PinnedDirs() {
			writable = append(writable, Rule{Path: dir})
		}
	}
	cfg := &FenceConfig{
		Filesystem: fenceFilesystem{
			DefaultDenyRead: true,
			// A write grant does not make a path readable in fence's
			// Seatbelt profile: every writable path is listed here too.
			AllowRead:  sortedPaths(slices.Concat([]Rule{{Path: p.Home}}, p.Read, writable)),
			DenyRead:   sortedPaths(p.Exclude),
			AllowWrite: sortedPaths(writable),
			DenyWrite:  sortedPaths(slices.Concat(p.Exclude, p.ReadOnly)),
		},
		// The policy holds no command rule: fence's own list of refused
		// commands is left out, so both backends run the same commands.
		Command:  fenceCommand{UseDefaults: false},
		AllowPty: true,
	}
	cfg.Network.AllowedDomains = sortedStrings(p.Domains)
	cfg.Network.DeniedDomains = sortedStrings(p.DeniedDomains)
	switch {
	case goos == "linux" && p.HostLocalhost:
		return nil, fmt.Errorf("fence on Linux opens the host's localhost one port at a time: write localhost:PORT for each port instead of localhost")
	case goos == "linux":
		// The sandbox has a localhost of its own. Each listed port of the
		// host's is bridged into it; each served port is bridged out
		// (FenceExposures, given to fence when the command starts).
		cfg.Network.AllowLocalOutbound = len(p.HostPorts) > 0
		cfg.Network.AllowLocalOutboundPorts = slices.Clone(p.HostPorts)
	case p.LocalhostOpen():
		// macOS has all of localhost or none of it: with at least one
		// host, as under nono, or when asked for.
		cfg.Network.AllowLocalBinding = true
		cfg.Network.AllowLocalOutbound = true
	case len(p.HostPorts) > 0:
		return nil, fmt.Errorf("fence on macOS opens all of localhost or none of it, not single ports (localhost:%d): write localhost", p.HostPorts[0])
	}
	if goos == "darwin" {
		if err := p.checkListenWithoutPrivateNetwork("fence on macOS"); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// FenceExposures returns the served ports fence must bridge out of the
// sandbox on goos, as ADDR:PORT: on Linux each one. On macOS the sandbox
// shares this machine's localhost and nothing is bridged.
func FenceExposures(p *Policy, goos string) []string {
	if goos != "linux" {
		return nil
	}
	var out []string
	for _, l := range p.Listen {
		out = append(out, l.Addr())
	}
	return out
}

func ruleOrigin(r Rule) string {
	if r.Source == "" {
		return "no origin"
	}
	return r.Source
}

// WriteFenceConfig writes the configuration beside dest, has validate
// accept that file, then renames it.
func WriteFenceConfig(cfg *FenceConfig, dest string, validate func(tmp string) error) error {
	return writeValidated(cfg, dest, validate)
}

// FenceExecCommand is the hidden ateam subcommand that runs a command
// under a fence configuration with the embedded library.
const FenceExecCommand = "fence-exec"

// FencePrefix is the command line up to the command to run: ateam itself,
// which holds the sandbox, so no other binary is needed.
func FencePrefix(ateam, configFile, workDir string, exposures []string) []string {
	args := []string{ateam, FenceExecCommand, "--settings", configFile, "--workdir", workDir}
	for _, addr := range exposures {
		args = append(args, "--expose", addr)
	}
	return append(args, "--")
}

// FenceCLIPrefix is the same for the fence command itself, without ateam:
// what an exported profile runs. fence takes the current folder as the
// working folder.
func FenceCLIPrefix(configFile string, exposures []string) []string {
	args := []string{"env", InSandboxEnv + "=1", "fence", "--settings", configFile}
	for _, addr := range exposures {
		args = append(args, "-p", addr)
	}
	return append(args, "--")
}
