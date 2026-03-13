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
	Image        string   // docker image name, e.g. "ateam-myproject:latest"
	Dockerfile   string   // absolute path to Dockerfile
	ForwardEnv   []string // env var names to forward from host
	ExtraVolumes []string // additional -v mounts, e.g. "/host/data:/data:ro"
	ExtraArgs    []string // additional docker run args from profile container_extra_args

	// Runtime context
	MountDir   string // volume mount source: git root, or SourceDir as fallback
	SourceDir  string // project root (parent of .ateam/) — determines -w
	ProjectDir string // .ateam/ dir
	OrgDir     string // .ateamorg/ dir
}

const containerRoot = "/ateam"

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
	// Resolve symlinks so Docker can read the actual file.
	// Symlinked Dockerfiles (e.g. .ateamorg/Dockerfile → .../defaults/Dockerfile)
	// point outside the build context, which BuildKit can't follow.
	dockerfilePath, err := filepath.EvalSymlinks(d.Dockerfile)
	if err != nil {
		return fmt.Errorf("Dockerfile not found: %s", d.Dockerfile)
	}

	// Pass host UID so the container user owns bind-mounted files.
	// Fall back to 1000 when running as root (e.g. inside DinD).
	uid := "1000"
	if u, err := user.Current(); err == nil && u.Uid != "0" {
		uid = u.Uid
	}

	buildCtx := filepath.Dir(dockerfilePath)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"--build-arg", "USER_UID="+uid,
		"-t", d.Image,
		"-f", dockerfilePath,
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
	containerCodePath, containerWorkDir, containerOrgPath := d.containerPaths()

	mount := d.mountDir()

	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		dockerArgs := []string{"run", "--rm", "-i"}

		// Mount code dir (git root or source dir)
		if mount != "" {
			dockerArgs = append(dockerArgs, "-v", mount+":"+containerCodePath+":rw")
		}

		// Mount org dir
		if d.OrgDir != "" {
			dockerArgs = append(dockerArgs, "-v", d.OrgDir+":"+containerOrgPath+":rw")
		}

		// Extra volumes from container config (e.g. "../data:/data:ro")
		for _, vol := range d.ExtraVolumes {
			dockerArgs = append(dockerArgs, "-v", vol)
		}

		// Working directory
		dockerArgs = append(dockerArgs, "-w", containerWorkDir)

		// Extra docker run args from profile
		dockerArgs = append(dockerArgs, d.ExtraArgs...)

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

	_, _, containerOrgPath := d.containerPaths()
	orgRoot := filepath.Dir(d.OrgDir)

	// Check OrgDir first (most specific non-code path)
	if d.OrgDir != "" {
		if rel, ok := relativeUnder(hostPath, d.OrgDir); ok {
			return filepath.Join(containerOrgPath, rel)
		}
	}

	// Check MountDir (git root or sourceDir)
	mount := d.mountDir()
	if mount != "" {
		if rel, ok := relativeUnder(hostPath, mount); ok {
			relMount, _ := filepath.Rel(orgRoot, mount)
			return filepath.Join(containerRoot, relMount, rel)
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
	containerCodePath, containerWorkDir, containerOrgPath := d.containerPaths()

	mount := d.mountDir()

	parts := []string{"docker", "run", "--rm", "-i"}
	if mount != "" {
		parts = append(parts, "-v", mount+":"+containerCodePath+":rw")
	}
	if d.OrgDir != "" {
		parts = append(parts, "-v", d.OrgDir+":"+containerOrgPath+":rw")
	}
	for _, vol := range d.ExtraVolumes {
		parts = append(parts, "-v", vol)
	}
	parts = append(parts, "-w", containerWorkDir)
	parts = append(parts, d.ExtraArgs...)
	for _, key := range d.ForwardEnv {
		parts = append(parts, "-e", key)
	}
	parts = append(parts, d.Image, opts.Command)
	parts = append(parts, opts.Args...)
	return strings.Join(parts, " ")
}

// mountDir returns the effective mount source: MountDir if set, otherwise SourceDir.
func (d *DockerContainer) mountDir() string {
	if d.MountDir != "" {
		return d.MountDir
	}
	return d.SourceDir
}

// containerPaths computes the container paths for code, workdir, and orgdir,
// preserving the relative hierarchy between orgRoot and the mounted dirs.
func (d *DockerContainer) containerPaths() (codePath, workDir, orgPath string) {
	orgRoot := filepath.Dir(d.OrgDir)

	// Container org path: /ateam/.ateamorg
	orgPath = filepath.Join(containerRoot, filepath.Base(d.OrgDir))

	// Container code path: /ateam/<relMountDir>
	mount := d.mountDir()
	relMount, err := filepath.Rel(orgRoot, mount)
	if err != nil {
		relMount = filepath.Base(mount)
	}
	codePath = filepath.Join(containerRoot, relMount)

	// Container workdir: /ateam/<relSourceDir>
	relSource, err := filepath.Rel(orgRoot, d.SourceDir)
	if err != nil {
		relSource = relMount
	}
	workDir = filepath.Join(containerRoot, relSource)

	return codePath, workDir, orgPath
}
