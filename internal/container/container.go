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
//
// Concurrency (see CONCURRENCY.md):
//
//   - The shared Container value held on a Runner is effectively read-only
//     once the Runner is dispatched to a pool.
//   - All mutating methods (ResolveTemplates, ApplyAgentEnv, SetContainerName,
//     SetSourceWritable, Prepare's side effects on the receiver) MUST be
//     called on a Clone, not on the shared original.
//   - The pool clones once per task at the top of Runner.Run and routes every
//     subsequent call through that clone.
//   - Clone deep-copies every field touched by a mutating method; only
//     thread-safe guard pointers (PrepareGuard / KeyedPrepareGuard) are
//     shared, so Prepare still fires exactly once per pool / resolved
//     container name.
type Container interface {
	Type() string // "none", "docker", "docker-exec"
	Run(ctx context.Context, opts RunOpts) error
	DebugCommand(opts RunOpts) string

	// Prepare performs any pre-run setup: image builds, binary copies,
	// precheck scripts. Invoked on a per-task clone; the shared PrepareGuard
	// ensures side effects run once per (pool × resolved name).
	Prepare(ctx context.Context) error

	// CmdFactory returns a CmdFactory that wraps commands for container execution.
	// Returns nil for host execution (nil Container).
	CmdFactory() CmdFactory

	// GetContainerName returns the resolved container name, or "" if the
	// implementation has no persistent name.
	GetContainerName() string

	// TranslatePath maps a host path to the corresponding in-container path.
	// Returns the original path unchanged if no mapping applies.
	TranslatePath(path string) string

	// ResolveTemplates resolves {{VAR}} placeholders in the container's
	// config fields using the provided replacer. MUTATES IN PLACE — callers
	// MUST invoke on a Clone, never on a container that another goroutine
	// can still reach.
	ResolveTemplates(replacer *strings.Replacer)

	// SetSourceWritable marks the container's source mount as read-write.
	// No-op for container types that don't manage source mounts.
	// MUTATES — clone-first rule applies.
	SetSourceWritable(writable bool)

	// SetContainerName overrides the container name. Returns true if the
	// name was applied, false if not supported by this container type.
	// MUTATES — clone-first rule applies.
	SetContainerName(name string) bool

	// ApplyAgentEnv merges agent-level environment overrides into the container.
	// Non-empty values become explicit -e KEY=VALUE flags (overriding ForwardEnv).
	// Empty values suppress forwarding of that key entirely.
	// MUTATES — clone-first rule applies (pre-pool dispatch callers are fine,
	// since construction code is single-threaded).
	ApplyAgentEnv(env map[string]string)

	// Clone returns a deep copy of the container. Implementations MUST:
	//   - re-allocate every slice/map that any mutating method writes to,
	//   - copy scalar fields by value,
	//   - share guard pointers (PrepareGuard / KeyedPrepareGuard) by pointer
	//     so Prepare continues to dedupe across clones.
	// The Container interface is intentionally pool-safe only via Clone;
	// sharing a Container across goroutines without cloning is a bug.
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
