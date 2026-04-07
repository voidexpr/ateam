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

		// Build the full agent command
		cmdParts := append([]string{name}, args...)
		cmdStr := strings.Join(cmdParts, " ")

		// Expand template
		expanded := strings.ReplaceAll(tmpl, "{{CONTAINER}}", d.ContainerName)
		expanded = strings.ReplaceAll(expanded, "{{CMD}}", cmdStr)

		// Parse into command + args
		fields := strings.Fields(expanded)
		if len(fields) == 0 {
			return exec.CommandContext(ctx, "echo", "empty exec template")
		}

		// Insert -i and env forwarding after "exec" for docker exec commands
		if fields[0] == "docker" && len(fields) > 1 && fields[1] == "exec" {
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
			// Append everything after "docker exec" from the expanded template
			dockerArgs = append(dockerArgs, fields[2:]...)
			cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
			cmd.Env = os.Environ()
			return cmd
		}

		// Non-docker exec template: use as-is
		cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
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

// RunPrecheck runs the precheck script on the host before agent execution.
func (d *DockerExecContainer) RunPrecheck(ctx context.Context) error {
	if d.PrecheckScript == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", d.PrecheckScript)
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
