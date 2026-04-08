package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DockerExecContainer executes commands inside a user-managed container.
// It does not build images or manage the container lifecycle — those are
// the user's responsibility (via docker-compose, devcontainer, manual docker run, etc.).
// An optional precheck script runs before each exec.
type DockerExecContainer struct {
	ContainerName string   // required: name of the running container
	ExecTemplate  string   // exec command template (default: "docker exec {{CONTAINER}} {{CMD}}")
	ForwardEnv    []string // env var names to forward via -e
	WorkDir       string   // working directory inside the container

	// HostCLIPath is the path to a Linux-compatible ateam binary on the host.
	// When set (via copy_ateam = true), it is copied into the container before exec.
	HostCLIPath string

	// PrecheckScript runs on the HOST before each exec.
	// Can be used to start the container, verify health, install deps, etc.
	PrecheckScript string
}

func (d *DockerExecContainer) Type() string { return "docker-exec" }

// CmdFactory returns a function that wraps commands in docker exec (or custom exec template).
func (d *DockerExecContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		tmpl := d.ExecTemplate
		if tmpl == "" {
			tmpl = "docker exec {{CONTAINER}} {{CMD}}"
		}

		// Expand container name in template
		expanded := strings.ReplaceAll(tmpl, "{{CONTAINER}}", d.ContainerName)

		// Split template around {{CMD}} to preserve argument boundaries.
		// Joining args into a string and re-splitting with Fields would
		// destroy boundaries for args containing whitespace.
		cmdArgs := append([]string{name}, args...)
		var allArgs []string
		if idx := strings.Index(expanded, "{{CMD}}"); idx >= 0 {
			prefix := strings.TrimSpace(expanded[:idx])
			suffix := strings.TrimSpace(expanded[idx+len("{{CMD}}"):])
			if prefix != "" {
				allArgs = append(allArgs, strings.Fields(prefix)...)
			}
			allArgs = append(allArgs, cmdArgs...)
			if suffix != "" {
				allArgs = append(allArgs, strings.Fields(suffix)...)
			}
		} else {
			allArgs = append(allArgs, strings.Fields(expanded)...)
			allArgs = append(allArgs, cmdArgs...)
		}

		if len(allArgs) == 0 {
			return exec.CommandContext(ctx, "echo", "empty exec template")
		}

		// Insert -i and env forwarding after "exec" for docker exec commands
		if allArgs[0] == "docker" && len(allArgs) > 1 && allArgs[1] == "exec" {
			// Rebuild as: docker exec -i [-w WORKDIR] [-e KEY=VALUE...] CONTAINER CMD...
			dockerArgs := []string{"exec", "-i"}
			if d.WorkDir != "" {
				dockerArgs = append(dockerArgs, "-w", d.WorkDir)
			}
			for _, key := range d.ForwardEnv {
				if val, ok := os.LookupEnv(key); ok {
					dockerArgs = append(dockerArgs, "-e", key+"="+val)
				}
			}
			// Append everything after "docker exec" (container name + command args)
			dockerArgs = append(dockerArgs, allArgs[2:]...)
			cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
			cmd.Env = os.Environ()
			return cmd
		}

		// Non-docker exec template: use as-is
		cmd := exec.CommandContext(ctx, allArgs[0], allArgs[1:]...)
		cmd.Env = os.Environ()
		return cmd
	}
}

// Run executes a command inside the container.
func (d *DockerExecContainer) Run(ctx context.Context, opts RunOpts) error {
	factory := d.CmdFactory()
	cmd := factory(ctx, opts.Command, opts.Args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

// EnsureBinary copies the ateam binary into the container via docker cp.
func (d *DockerExecContainer) EnsureBinary(ctx context.Context) error {
	if d.HostCLIPath == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "docker", "cp", d.HostCLIPath, d.ContainerName+":/usr/local/bin/ateam")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunPrecheck runs the precheck script on the host before agent execution.
// The container name is passed as the first argument.
func (d *DockerExecContainer) RunPrecheck(ctx context.Context) error {
	if d.PrecheckScript == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sh", d.PrecheckScript, d.ContainerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("precheck script failed: %w", err)
	}
	return nil
}

// DebugCommand returns the exec command string for logging.
func (d *DockerExecContainer) DebugCommand(opts RunOpts) string {
	tmpl := d.ExecTemplate
	if tmpl == "" {
		tmpl = "docker exec {{CONTAINER}} {{CMD}}"
	}

	cmdParts := append([]string{opts.Command}, opts.Args...)
	cmdStr := strings.Join(cmdParts, " ")

	expanded := strings.ReplaceAll(tmpl, "{{CONTAINER}}", d.ContainerName)
	expanded = strings.ReplaceAll(expanded, "{{CMD}}", cmdStr)

	// Add env forwarding info
	var envParts []string
	for _, key := range d.ForwardEnv {
		if _, ok := os.LookupEnv(key); ok {
			envParts = append(envParts, "-e "+key)
		}
	}
	if len(envParts) > 0 {
		return expanded + " (with " + strings.Join(envParts, ", ") + ")"
	}
	return expanded
}

// TranslatePath maps host paths to container paths.
// For docker-exec, we don't know the mount layout — return as-is.
func (d *DockerExecContainer) TranslatePath(hostPath string) string {
	return hostPath
}

// Prepare copies the ateam binary (if configured) and runs the precheck script.
// Implements the Container interface.
func (d *DockerExecContainer) Prepare(ctx context.Context) error {
	if err := d.EnsureBinary(ctx); err != nil {
		return fmt.Errorf("copy ateam binary failed: %w", err)
	}
	return d.RunPrecheck(ctx)
}

// GetContainerName returns the name of the user-managed container.
func (d *DockerExecContainer) GetContainerName() string { return d.ContainerName }
