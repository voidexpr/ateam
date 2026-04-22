// Package container defines the Container interface for abstracting command execution environments and provides Docker-based implementations.
package container

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
)

// IsInContainer detects whether the current process is running inside a
// container (Docker or Podman). It checks /.dockerenv (Docker),
// /run/.containerenv (Podman), and the ATEAM_IN_CONTAINER env var override.
func IsInContainer() bool {
	if os.Getenv("ATEAM_IN_CONTAINER") == "1" {
		return true
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// CmdFactory creates an *exec.Cmd. When set on an agent Request, agents use this
// instead of exec.CommandContext. For docker, this wraps commands in docker run/exec.
type CmdFactory func(ctx context.Context, name string, args ...string) *exec.Cmd

// Container abstracts where agent commands execute.
type Container interface {
	Type() string // "none", "docker", "docker-exec"
	Run(ctx context.Context, opts RunOpts) error
	DebugCommand(opts RunOpts) string

	// Prepare performs any pre-run setup: image builds, binary copies, precheck scripts.
	// Called once per Run() before the agent is launched.
	Prepare(ctx context.Context) error

	// CmdFactory returns a CmdFactory that wraps commands for container execution.
	// Returns nil for host execution (nil Container).
	CmdFactory() CmdFactory

	// GetContainerName returns the name of a long-lived container, or "" if not applicable.
	// Used to populate Runner.ContainerName for liveness tracking.
	GetContainerName() string

	// TranslatePath maps a host path to the corresponding in-container path.
	// Returns the original path unchanged if no mapping applies.
	TranslatePath(path string) string

	// ResolveTemplates resolves {{VAR}} placeholders in the container's
	// config fields using the provided replacer. Mutates in place.
	ResolveTemplates(replacer *strings.Replacer)

	// SetSourceWritable marks the container's source mount as read-write.
	// No-op for container types that don't manage source mounts.
	SetSourceWritable(writable bool)

	// SetContainerName overrides the container name. Returns true if the
	// name was applied, false if not supported by this container type.
	SetContainerName(name string) bool

	// ApplyAgentEnv merges agent-level environment overrides into the container.
	// Non-empty values become explicit -e KEY=VALUE flags (overriding ForwardEnv).
	// Empty values suppress forwarding of that key entirely.
	ApplyAgentEnv(env map[string]string)

	// Clone returns a deep copy of the container with independent slice and map
	// backing memory. Callers that will mutate container state (notably
	// ResolveTemplates, SetContainerName, and per-run naming) must operate on
	// a clone when the original may be shared across goroutines.
	Clone() Container
}

// RunOpts holds options for executing a command in a container.
type RunOpts struct {
	Command   string
	Args      []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	WorkDir   string
	Env       map[string]string
	ExtraArgs []string // from --container-args
}
