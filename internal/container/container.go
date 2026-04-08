package container

import (
	"context"
	"io"
	"os"
	"os/exec"
)

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
	// Returns nil for host execution (NoneContainer).
	CmdFactory() CmdFactory

	// GetContainerName returns the name of a long-lived container, or "" if not applicable.
	// Used to populate Runner.ContainerName for liveness tracking.
	GetContainerName() string

	// TranslatePath maps a host path to the corresponding in-container path.
	// Returns the original path unchanged if no mapping applies.
	TranslatePath(path string) string
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

// NoneContainer executes commands directly on the host.
type NoneContainer struct{}

func (n *NoneContainer) Type() string { return "none" }

func (n *NoneContainer) Run(ctx context.Context, opts RunOpts) error {
	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	if len(opts.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range opts.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	return cmd.Run()
}

func (n *NoneContainer) DebugCommand(opts RunOpts) string {
	s := opts.Command
	for _, a := range opts.Args {
		s += " " + a
	}
	return s
}

func (n *NoneContainer) Prepare(_ context.Context) error    { return nil }
func (n *NoneContainer) CmdFactory() CmdFactory             { return nil }
func (n *NoneContainer) GetContainerName() string           { return "" }
func (n *NoneContainer) TranslatePath(path string) string   { return path }
