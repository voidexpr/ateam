package container

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// Container abstracts where agent commands execute.
type Container interface {
	Type() string // "none", "docker", "srt"
	Run(ctx context.Context, opts RunOpts) error
	DebugCommand(opts RunOpts) string
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
