package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// DockerExecContainer executes commands inside a user-managed container.
// It does not build images or manage the container lifecycle — those are
// the user's responsibility (via docker-compose, devcontainer, manual docker run, etc.).
// An optional precheck script runs before each exec.
type DockerExecContainer struct {
	ContainerName string            // required: name of the running container
	ExecTemplate  string            // exec command template (default: "docker exec {{CONTAINER}} {{CMD}}")
	ForwardEnv    []string          // env var names to forward via -e
	Env           map[string]string // explicit env overrides; empty values suppress ForwardEnv
	WorkDir       string            // working directory inside the container

	// HostCLIPath is the path to a Linux-compatible ateam binary on the host.
	// When set (via copy_ateam = true), it is copied into the container before exec.
	HostCLIPath string

	// PrecheckCmd runs on the HOST before each exec.
	// {{CONTAINER_NAME}} in args is replaced with the resolved container name.
	// Examples: ["sh", "precheck.sh", "{{CONTAINER_NAME}}"], ["make", "docker-restart"]
	PrecheckCmd []string

	prepareOnce sync.Once
	prepareErr  error
}

func (d *DockerExecContainer) Type() string { return "docker-exec" }

// Clone returns a deep copy with independent slice and map backing memory.
// The clone carries a fresh prepareOnce, so Prepare runs once per clone
// (idempotent: name resolution + binary copy).
func (d *DockerExecContainer) Clone() Container {
	cp := DockerExecContainer{
		ContainerName: d.ContainerName,
		ExecTemplate:  d.ExecTemplate,
		WorkDir:       d.WorkDir,
		HostCLIPath:   d.HostCLIPath,
		ForwardEnv:    append([]string(nil), d.ForwardEnv...),
		PrecheckCmd:   append([]string(nil), d.PrecheckCmd...),
	}
	if d.Env != nil {
		cp.Env = make(map[string]string, len(d.Env))
		for k, v := range d.Env {
			cp.Env[k] = v
		}
	}
	return &cp
}

// ResolveTemplates resolves {{VAR}} placeholders in ContainerName and WorkDir.
// ExecTemplate is NOT resolved here — its {{CONTAINER}} and {{CMD}} placeholders
// are expanded by CmdFactory at execution time (separate namespace).
func (d *DockerExecContainer) ResolveTemplates(r *strings.Replacer) {
	if strings.Contains(d.ContainerName, "{{") {
		d.ContainerName = r.Replace(d.ContainerName)
	}
	if strings.Contains(d.WorkDir, "{{") {
		d.WorkDir = r.Replace(d.WorkDir)
	}
}

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
				if _, overridden := d.Env[key]; overridden {
					continue // handled below
				}
				if val, ok := os.LookupEnv(key); ok {
					dockerArgs = append(dockerArgs, "-e", key+"="+val)
				}
			}
			dockerArgs = append(dockerArgs, d.envArgs()...)
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

// RunPrecheck runs the precheck command on the host before agent execution.
// {{CONTAINER_NAME}} placeholders in args are replaced with d.ContainerName.
func (d *DockerExecContainer) RunPrecheck(ctx context.Context) error {
	if len(d.PrecheckCmd) == 0 {
		return nil
	}
	args := make([]string, len(d.PrecheckCmd))
	for i, a := range d.PrecheckCmd {
		args[i] = strings.ReplaceAll(a, "{{CONTAINER_NAME}}", d.ContainerName)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("precheck failed: %w", err)
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
		if _, overridden := d.Env[key]; overridden {
			continue
		}
		if _, ok := os.LookupEnv(key); ok {
			envParts = append(envParts, "-e "+key)
		}
	}
	for k, v := range d.Env {
		if v != "" {
			envParts = append(envParts, "-e "+k)
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

// ResolveRunningContainerName resolves a possibly-partial container name to the
// exact running container name via docker ps substring matching.
func ResolveRunningContainerName(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("docker-exec container name is empty — set via docker_container, --container-name, or ateam secret CONTAINER_NAME")
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "name="+name, "--format", "{{.Names}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker ps: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var matches []string
	for _, l := range lines {
		if l != "" {
			matches = append(matches, l)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no running container matching %q", name)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous container name %q matches %d containers: %s", name, len(matches), strings.Join(matches, ", "))
	}
}

// Prepare validates the container name, copies the ateam binary (if configured),
// and runs the precheck command. Safe to call concurrently — runs only once.
func (d *DockerExecContainer) Prepare(ctx context.Context) error {
	d.prepareOnce.Do(func() {
		d.prepareErr = d.prepare(ctx)
	})
	return d.prepareErr
}

func (d *DockerExecContainer) prepare(ctx context.Context) error {
	resolved, err := ResolveRunningContainerName(ctx, d.ContainerName)
	if err != nil {
		return err
	}
	d.ContainerName = resolved
	if err := d.EnsureBinary(ctx); err != nil {
		return fmt.Errorf("copy ateam binary failed: %w", err)
	}
	return d.RunPrecheck(ctx)
}

// GetContainerName returns the name of the user-managed container.
func (d *DockerExecContainer) GetContainerName() string { return d.ContainerName }

// SetSourceWritable is a no-op for docker-exec containers (no managed source mount).
func (d *DockerExecContainer) SetSourceWritable(_ bool) {}

// SetContainerName overrides the container name.
func (d *DockerExecContainer) SetContainerName(name string) bool {
	d.ContainerName = name
	return true
}

// envArgs returns sorted -e KEY=VALUE args for non-empty Env entries.
func (d *DockerExecContainer) envArgs() []string {
	if len(d.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		if d.Env[k] == "" {
			continue
		}
		args = append(args, "-e", k+"="+d.Env[k])
	}
	return args
}

// ApplyAgentEnv merges agent-level env overrides into d.Env.
// Non-empty values override ForwardEnv; empty values suppress forwarding.
func (d *DockerExecContainer) ApplyAgentEnv(env map[string]string) {
	if len(env) == 0 {
		return
	}
	if d.Env == nil {
		d.Env = make(map[string]string, len(env))
	}
	for k, v := range env {
		d.Env[k] = v
	}
}
