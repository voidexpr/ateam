package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/agent"
)

// DockerContainer runs agent commands inside a Docker container.
// Uses the one-shot model: each invocation is a `docker run --rm`.
type DockerContainer struct {
	// From ContainerConfig
	Image      string   // docker image name, e.g. "ateam-myproject:latest"
	Dockerfile string   // absolute path to Dockerfile
	ForwardEnv []string // env var names to forward from host

	// Runtime context
	SourceDir  string // project source root → mounted as /workspace
	ProjectDir string // .ateam/ dir → inside /workspace/.ateam
	OrgDir     string // .ateamorg/ dir → mounted as /.ateamorg
}

const (
	containerWorkspace = "/workspace"
	containerOrgDir    = "/.ateamorg"
)

func (d *DockerContainer) Type() string { return "docker" }

// EnsureImage builds the docker image if it doesn't exist.
func (d *DockerContainer) EnsureImage(ctx context.Context) error {
	// Check if image already exists
	check := exec.CommandContext(ctx, "docker", "image", "inspect", d.Image)
	if check.Run() == nil {
		return nil
	}

	// Build from Dockerfile
	if d.Dockerfile == "" {
		return fmt.Errorf("no Dockerfile configured for docker container")
	}
	if _, err := os.Stat(d.Dockerfile); err != nil {
		return fmt.Errorf("Dockerfile not found: %s", d.Dockerfile)
	}

	// Pass host UID so the container user owns bind-mounted files.
	uid := "1000"
	if u, err := user.Current(); err == nil {
		uid = u.Uid
	}

	buildCtx := filepath.Dir(d.Dockerfile)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"--build-arg", "USER_UID="+uid,
		"-t", d.Image,
		"-f", d.Dockerfile,
		buildCtx,
	)
	cmd.Stdout = os.Stderr // build output goes to stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// CmdFactory returns a function that wraps commands in `docker run --rm -i`.
// The returned factory sets up all mounts, env forwarding, and workdir.
// The agent uses this instead of exec.CommandContext.
func (d *DockerContainer) CmdFactory() agent.CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		dockerArgs := []string{"run", "--rm", "-i"}

		// Mount source dir as /workspace
		if d.SourceDir != "" {
			dockerArgs = append(dockerArgs, "-v", d.SourceDir+":"+containerWorkspace+":rw")
		}

		// Mount org dir as /.ateamorg (read-only)
		if d.OrgDir != "" {
			dockerArgs = append(dockerArgs, "-v", d.OrgDir+":"+containerOrgDir+":ro")
		}

		// Working directory
		dockerArgs = append(dockerArgs, "-w", containerWorkspace)

		// Forward env vars from host
		for _, key := range d.ForwardEnv {
			if _, ok := os.LookupEnv(key); ok {
				dockerArgs = append(dockerArgs, "-e", key)
			}
		}

		// Image
		dockerArgs = append(dockerArgs, d.Image)

		// The actual command
		dockerArgs = append(dockerArgs, name)
		dockerArgs = append(dockerArgs, args...)

		cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
		// Env is handled by docker -e flags, not cmd.Env
		// WorkDir is handled by docker -w flag, not cmd.Dir
		// Set cmd.Env to host env so docker itself can find credentials
		cmd.Env = os.Environ()
		return cmd
	}
}

// TranslatePath maps a host path to the corresponding container path.
// Returns the original path if no mapping applies.
func (d *DockerContainer) TranslatePath(hostPath string) string {
	if hostPath == "" {
		return ""
	}

	// .ateam/ is inside source dir, so check ProjectDir first (more specific)
	if d.ProjectDir != "" {
		if rel, ok := relativeUnder(hostPath, d.ProjectDir); ok {
			return filepath.Join(containerWorkspace, ".ateam", rel)
		}
	}

	if d.SourceDir != "" {
		if rel, ok := relativeUnder(hostPath, d.SourceDir); ok {
			return filepath.Join(containerWorkspace, rel)
		}
	}

	if d.OrgDir != "" {
		if rel, ok := relativeUnder(hostPath, d.OrgDir); ok {
			return filepath.Join(containerOrgDir, rel)
		}
	}

	return hostPath
}

// relativeUnder returns the relative path of target under base, if target is under base.
func relativeUnder(target, base string) (string, bool) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

// Run executes a command inside the container (Container interface).
func (d *DockerContainer) Run(ctx context.Context, opts RunOpts) error {
	factory := d.CmdFactory()
	cmd := factory(ctx, opts.Command, opts.Args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

// DebugCommand returns the full docker command string for logging.
func (d *DockerContainer) DebugCommand(opts RunOpts) string {
	parts := []string{"docker", "run", "--rm", "-i"}
	if d.SourceDir != "" {
		parts = append(parts, "-v", d.SourceDir+":"+containerWorkspace+":rw")
	}
	if d.OrgDir != "" {
		parts = append(parts, "-v", d.OrgDir+":"+containerOrgDir+":ro")
	}
	parts = append(parts, "-w", containerWorkspace)
	for _, key := range d.ForwardEnv {
		parts = append(parts, "-e", key)
	}
	parts = append(parts, d.Image, opts.Command)
	parts = append(parts, opts.Args...)
	return strings.Join(parts, " ")
}
