package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// DevcontainerContainer executes commands inside a devcontainer using the
// @devcontainers/cli (devcontainer up / devcontainer exec).
type DevcontainerContainer struct {
	ConfigPath   string   // absolute path to devcontainer.json
	WorkspaceDir string   // project source dir (--workspace-folder)
	ForwardEnv   []string // env var names to forward from host

	startOnce    sync.Once
	startErr     error
	validatedCmd sync.Map // caches ValidateAgent results per command name
}

func (d *DevcontainerContainer) Type() string { return "devcontainer" }

// EnsureRunning starts the devcontainer if not already running.
func (d *DevcontainerContainer) EnsureRunning(ctx context.Context) error {
	d.startOnce.Do(func() {
		if _, err := exec.LookPath("devcontainer"); err != nil {
			d.startErr = fmt.Errorf("devcontainer CLI not found; install with: npm install -g @devcontainers/cli")
			return
		}

		args := []string{"up", "--workspace-folder", d.WorkspaceDir}
		if d.ConfigPath != "" {
			args = append(args, "--config", d.ConfigPath)
		}
		cmd := exec.CommandContext(ctx, "devcontainer", args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		d.startErr = cmd.Run()
	})
	return d.startErr
}

// ValidateAgent checks that the agent command exists inside the devcontainer.
// Results are cached per command name for the process lifetime.
func (d *DevcontainerContainer) ValidateAgent(ctx context.Context, command string) error {
	if cached, ok := d.validatedCmd.Load(command); ok {
		if err, isErr := cached.(error); isErr {
			return err
		}
		return nil
	}
	args := d.execArgs("which", command)
	cmd := exec.CommandContext(ctx, "devcontainer", args...)
	if err := cmd.Run(); err != nil {
		err = fmt.Errorf("agent %q not found in devcontainer; add it as a Feature or install it in your Dockerfile", command)
		d.validatedCmd.Store(command, err)
		return err
	}
	d.validatedCmd.Store(command, true)
	return nil
}

// CmdFactory returns a function that wraps commands for devcontainer execution.
func (d *DevcontainerContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		dcArgs := d.execArgs(name, args...)
		cmd := exec.CommandContext(ctx, "devcontainer", dcArgs...)
		cmd.Env = os.Environ()
		return cmd
	}
}

func (d *DevcontainerContainer) Run(ctx context.Context, opts RunOpts) error {
	factory := d.CmdFactory()
	cmd := factory(ctx, opts.Command, opts.Args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

func (d *DevcontainerContainer) DebugCommand(opts RunOpts) string {
	args := d.execArgs(opts.Command, opts.Args...)
	parts := append([]string{"devcontainer"}, args...)
	return strings.Join(parts, " ")
}

// execArgs builds the devcontainer exec argument list.
func (d *DevcontainerContainer) execArgs(command string, args ...string) []string {
	dcArgs := []string{"exec", "--workspace-folder", d.WorkspaceDir}
	if d.ConfigPath != "" {
		dcArgs = append(dcArgs, "--config", d.ConfigPath)
	}
	// Forward env vars from host
	for _, key := range d.ForwardEnv {
		if val, ok := os.LookupEnv(key); ok {
			dcArgs = append(dcArgs, "--remote-env", key+"="+val)
		}
	}
	dcArgs = append(dcArgs, command)
	dcArgs = append(dcArgs, args...)
	return dcArgs
}
