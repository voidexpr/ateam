package container

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DockerSandboxContainer executes commands inside a Docker AI sandbox.
// The sandbox provides hypervisor-level isolation via a microVM with a
// private Docker daemon and bidirectional workspace sync.
// Requires Docker Desktop 4.58+.
//
// Paths are mapped 1:1 (same absolute path inside sandbox as on host).
// Only the workspace directory gets bidirectional sync; additional dirs
// (OrgDir, ClaudeDir) are tar-copied as read-only snapshots.
//
// CLI model:
//
//	docker sandbox create [--name NAME] AGENT WORKSPACE  (create sandbox)
//	docker sandbox exec [-e KEY=VAL] [-w DIR] SANDBOX CMD (run commands)
//	docker sandbox rm SANDBOX                             (remove sandbox)
type DockerSandboxContainer struct {
	WorkspaceDir  string   // working directory for exec commands (project source dir)
	MountDir      string   // workspace for sandbox create (git root); falls back to WorkspaceDir
	OrgDir        string   // .ateamorg/ dir — tar-copied after create
	ClaudeDir     string   // ~/.claude/ dir — selectively tar-copied (empty = skip)
	CacheDir      string   // dir for hash file (e.g. .ateam/cache); empty = no auto-recreate
	ForwardEnv    []string // env var names to forward via exec -e
	AgentName     string   // sandbox agent type: "claude", "codex", etc.
	SandboxName   string   // name for the sandbox instance
	NetworkPolicy string   // "deny" (default) or "allow"
	BuildVersion  string   // ateam build version (git commit); included in config hash

	startOnce    sync.Once
	startErr     error
	validatedCmd sync.Map
}

func (d *DockerSandboxContainer) Type() string { return "docker-sandbox" }

// EnsureRunning creates the sandbox if it doesn't exist yet.
// If the sandbox exists but the config hash changed (different settings or
// ateam version), it removes the old sandbox and creates a fresh one.
func (d *DockerSandboxContainer) EnsureRunning(ctx context.Context) error {
	d.startOnce.Do(func() {
		if d.sandboxExists() {
			if !d.configChanged() {
				d.applyNetworkPolicy(ctx)
				return
			}
			fmt.Fprintf(os.Stderr, "[docker-sandbox] config changed, recreating sandbox %s\n", d.SandboxName)
			_ = d.Stop()
		}
		d.startErr = d.create(ctx)
		if d.startErr == nil {
			d.writeConfigHash()
		}
	})
	return d.startErr
}

// sandboxExists checks if a sandbox with this name already exists (any status).
func (d *DockerSandboxContainer) sandboxExists() bool {
	cmd := exec.Command("docker", "sandbox", "ls")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == d.SandboxName {
			return true
		}
	}
	return false
}

// workspace returns the directory to sync into the sandbox (MountDir or WorkspaceDir).
func (d *DockerSandboxContainer) workspace() string {
	if d.MountDir != "" {
		return d.MountDir
	}
	return d.WorkspaceDir
}

func (d *DockerSandboxContainer) create(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found; Docker Desktop 4.58+ required for sandbox support")
	}
	agent := d.AgentName
	if agent == "" {
		agent = "claude"
	}
	// docker sandbox create [--name NAME] AGENT WORKSPACE
	args := []string{"sandbox", "create", "--name", d.SandboxName, agent, d.workspace()}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	d.applyNetworkPolicy(ctx)
	d.copyExtraDirs(ctx)
	return nil
}

// applyNetworkPolicy configures the sandbox network proxy.
// Called on both create and reuse, since the policy may reset between runs.
func (d *DockerSandboxContainer) applyNetworkPolicy(ctx context.Context) {
	if d.NetworkPolicy != "allow" {
		return
	}
	cmd := exec.CommandContext(ctx, "docker", "sandbox", "network", "proxy",
		d.SandboxName, "--policy", "allow")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[docker-sandbox] warning: failed to set network policy: %v\n", err)
	}
}

// copyExtraDirs copies OrgDir and (optionally) ClaudeDir into the sandbox.
// Errors are logged but not fatal — the agent may still work without them.
func (d *DockerSandboxContainer) copyExtraDirs(ctx context.Context) {
	if d.OrgDir != "" {
		if err := d.copyDirToSandbox(ctx, d.OrgDir); err != nil {
			fmt.Fprintf(os.Stderr, "[docker-sandbox] warning: failed to copy %s: %v\n", d.OrgDir, err)
		}
	}
	if d.ClaudeDir != "" {
		if err := d.copyClaudeDirSelective(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[docker-sandbox] warning: failed to copy claude config: %v\n", err)
		}
	}
}

// claudeConfigPaths lists the ~/.claude/ subdirs/files to copy into the sandbox.
// Only includes what the inner agent needs for skills, plugins, and MCP config.
// Excludes sessions, history, credentials, hooks, debug logs, etc.
var claudeConfigPaths = []string{
	"CLAUDE.md",
	"plugins",
	"skills",
	"settings.json",
	"projects",
}

// copyClaudeDirSelective copies only the relevant parts of ~/.claude/ into the
// sandbox (skills, plugins, settings, CLAUDE.md, projects). Non-existent paths
// are silently skipped by tar.
func (d *DockerSandboxContainer) copyClaudeDirSelective(ctx context.Context) error {
	if err := d.mkdirInSandbox(ctx, d.ClaudeDir); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Filter to paths that actually exist on host
	var paths []string
	for _, p := range claudeConfigPaths {
		if _, err := os.Stat(filepath.Join(d.ClaudeDir, p)); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}

	// tar cf - -C ~/.claude CLAUDE.md plugins ... | docker sandbox exec tar xf - -C ~/.claude
	tarArgs := append([]string{"cf", "-", "-C", d.ClaudeDir}, paths...)
	tar := exec.CommandContext(ctx, "tar", tarArgs...)

	untarArgs := []string{"sandbox", "exec", "-i", d.SandboxName, "tar", "xf", "-", "-C", d.ClaudeDir}
	untar := exec.CommandContext(ctx, "docker", untarArgs...)

	pipe, err := tar.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	untar.Stdin = pipe

	if err := untar.Start(); err != nil {
		return fmt.Errorf("untar start: %w", err)
	}
	if err := tar.Run(); err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	return untar.Wait()
}

// mkdirInSandbox creates a directory inside the sandbox.
// Uses -u root because the workspace parent dirs are owned by root, then
// chowns to the agent user so subsequent writes succeed without root.
func (d *DockerSandboxContainer) mkdirInSandbox(ctx context.Context, dir string) error {
	mkdirArgs := []string{"sandbox", "exec", "-i", "-u", "root", d.SandboxName,
		"sh", "-c", fmt.Sprintf("mkdir -p '%s' && chown -R agent:agent '%s'", dir, dir)}
	return exec.CommandContext(ctx, "docker", mkdirArgs...).Run()
}

// copyDirToSandbox copies a host directory into the sandbox at the same path.
func (d *DockerSandboxContainer) copyDirToSandbox(ctx context.Context, hostDir string) error {
	if err := d.mkdirInSandbox(ctx, hostDir); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	tar := exec.CommandContext(ctx, "tar", "cf", "-", "-C", hostDir, ".")
	untarArgs := []string{"sandbox", "exec", "-i", d.SandboxName, "tar", "xf", "-", "-C", hostDir}
	untar := exec.CommandContext(ctx, "docker", untarArgs...)

	pipe, err := tar.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	untar.Stdin = pipe

	if err := untar.Start(); err != nil {
		return fmt.Errorf("untar start: %w", err)
	}
	if err := tar.Run(); err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	return untar.Wait()
}

// Stop removes the sandbox.
func (d *DockerSandboxContainer) Stop() error {
	cmd := exec.Command("docker", "sandbox", "rm", d.SandboxName)
	return cmd.Run()
}

// ValidateAgent checks that the agent command exists inside the sandbox.
func (d *DockerSandboxContainer) ValidateAgent(ctx context.Context, command string) error {
	if cached, ok := d.validatedCmd.Load(command); ok {
		if err, isErr := cached.(error); isErr {
			return err
		}
		return nil
	}
	args := d.execArgs("which", command)
	cmd := exec.CommandContext(ctx, "docker", args...)
	if err := cmd.Run(); err != nil {
		err = fmt.Errorf("agent %q not found in docker sandbox; the sandbox must include the agent CLI", command)
		d.validatedCmd.Store(command, err)
		return err
	}
	d.validatedCmd.Store(command, true)
	return nil
}

// CmdFactory returns a function that wraps commands for sandbox execution.
func (d *DockerSandboxContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		sandboxArgs := d.execArgs(name, args...)
		cmd := exec.CommandContext(ctx, "docker", sandboxArgs...)
		cmd.Env = os.Environ()
		return cmd
	}
}

func (d *DockerSandboxContainer) Run(ctx context.Context, opts RunOpts) error {
	factory := d.CmdFactory()
	cmd := factory(ctx, opts.Command, opts.Args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

func (d *DockerSandboxContainer) DebugCommand(opts RunOpts) string {
	args := d.execArgs(opts.Command, opts.Args...)
	parts := append([]string{"docker"}, args...)
	return strings.Join(parts, " ")
}

// execArgs builds the docker sandbox exec argument list.
func (d *DockerSandboxContainer) execArgs(command string, args ...string) []string {
	sandboxArgs := []string{"sandbox", "exec", "-i", "-w", d.WorkspaceDir}
	for _, key := range d.ForwardEnv {
		if val, ok := os.LookupEnv(key); ok {
			sandboxArgs = append(sandboxArgs, "-e", key+"="+val)
		}
	}
	sandboxArgs = append(sandboxArgs, d.SandboxName, command)
	sandboxArgs = append(sandboxArgs, args...)
	return sandboxArgs
}

// configHash computes a SHA-256 hash of all fields that affect sandbox setup.
// When this hash changes, the sandbox must be recreated.
func (d *DockerSandboxContainer) configHash() string {
	env := make([]string, len(d.ForwardEnv))
	copy(env, d.ForwardEnv)
	sort.Strings(env)

	h := sha256.New()
	fmt.Fprintf(h, "workspace=%s\n", d.WorkspaceDir)
	fmt.Fprintf(h, "mount=%s\n", d.MountDir)
	fmt.Fprintf(h, "org=%s\n", d.OrgDir)
	fmt.Fprintf(h, "claude=%s\n", d.ClaudeDir)
	fmt.Fprintf(h, "agent=%s\n", d.AgentName)
	fmt.Fprintf(h, "network=%s\n", d.NetworkPolicy)
	fmt.Fprintf(h, "env=%s\n", strings.Join(env, ","))
	fmt.Fprintf(h, "build=%s\n", d.BuildVersion)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// hashFile returns the path to the cached config hash file.
func (d *DockerSandboxContainer) hashFile() string {
	if d.CacheDir == "" {
		return ""
	}
	return filepath.Join(d.CacheDir, "sandbox-"+d.SandboxName+".hash")
}

// configChanged returns true if the current config hash differs from the cached one.
func (d *DockerSandboxContainer) configChanged() bool {
	path := d.hashFile()
	if path == "" {
		return false
	}
	cached, err := os.ReadFile(path)
	if err != nil {
		return false // no cached hash = first run, don't force recreate
	}
	return strings.TrimSpace(string(cached)) != d.configHash()
}

// writeConfigHash writes the current config hash to the cache file.
func (d *DockerSandboxContainer) writeConfigHash() {
	path := d.hashFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[docker-sandbox] warning: cannot create cache dir: %v\n", err)
		return
	}
	if err := os.WriteFile(path, []byte(d.configHash()+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[docker-sandbox] warning: cannot write config hash: %v\n", err)
	}
}
