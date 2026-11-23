package sandboxpolicy

import (
	"fmt"
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
	AllowedDomains     []string `json:"allowedDomains"`
	AllowLocalBinding  bool     `json:"allowLocalBinding"`
	AllowLocalOutbound bool     `json:"allowLocalOutbound"`
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

// FenceNotes is what fence applies whatever a policy says. None of it
// opens a path the policy excludes. Shown at launch and in exports.
var FenceNotes = []string{
	"fence also refuses writes to .gitmodules, .mcp.json, .ripgreprc, .vscode, .idea and shell startup files in the working folder, and to shell startup files and .gitconfig in the home; no setting lifts this",
	"fence also lets its own temporary folder (/tmp/fence), the per-user temporary folder and ~/.npm/_logs be written, and the system folders, /tmp and /opt be read",
	"a new pseudo-terminal may be opened",
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
// Linux is refused: there fence reaches the host's localhost one listed
// port at a time, and the policy means localhost as a whole.
func RenderFence(p *Policy, goos string) (*FenceConfig, error) {
	if goos != "darwin" {
		return nil, fmt.Errorf("the fence backend supports macOS only for now (this is %s): on Linux fence opens localhost one listed port at a time, which the profile cannot say yet", goos)
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
	if len(p.Domains) > 0 {
		// As under nono: no domain closes the network, localhost included.
		cfg.Network.AllowLocalBinding = true
		cfg.Network.AllowLocalOutbound = true
	}
	return cfg, nil
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
func FencePrefix(ateam, configFile, workDir string) []string {
	return []string{ateam, FenceExecCommand, "--settings", configFile, "--workdir", workDir, "--"}
}

// FenceCLIPrefix is the same for the fence command itself, without ateam:
// what an exported profile runs. fence takes the current folder as the
// working folder.
func FenceCLIPrefix(configFile string) []string {
	return []string{"env", InSandboxEnv + "=1", "fence", "--settings", configFile, "--"}
}
