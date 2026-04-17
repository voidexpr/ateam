package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// DockerContainer runs agent commands inside a Docker container (oneshot mode).
// Each invocation launches a fresh container via `docker run --rm -i` and
// removes it when the agent exits.
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

	// HostCLIPath is the path to a Linux-compatible ateam binary on the host.
	// When set, it is bind-mounted to /usr/local/bin/ateam inside the container.
	HostCLIPath string

	// DockerfileTmpDir is a temporary directory created for the embedded Dockerfile.
	// EnsureImage removes it after the build completes.
	DockerfileTmpDir string

	// ClaudeCredentialsFile is the host path to ~/.claude/.credentials.json.
	// When set, it is mounted read-only into the container. Required for OAuth
	// tokens which are session-scoped and need the credential store.
	ClaudeCredentialsFile string

	// SourceWritable mounts the source code directory as :rw instead of :ro.
	// Required for actions that modify source code (code, run).
	SourceWritable bool

	// Env holds explicit environment variables (KEY=VALUE) to set inside the container.
	// Unlike ForwardEnv (which forwards host values), these are literal values.
	Env map[string]string
}

const (
	containerCodeRoot = "/workspace"
	containerOrgRoot  = "/.ateamorg"
)

func (d *DockerContainer) Type() string { return "docker" }

// ResolveTemplates resolves {{VAR}} placeholders in ExtraArgs, ExtraVolumes, and Env.
func (d *DockerContainer) ResolveTemplates(r *strings.Replacer) {
	for i, arg := range d.ExtraArgs {
		d.ExtraArgs[i] = r.Replace(arg)
	}
	for i, vol := range d.ExtraVolumes {
		d.ExtraVolumes[i] = r.Replace(vol)
	}
	if d.Env != nil {
		resolved := make(map[string]string, len(d.Env))
		for k, v := range d.Env {
			resolved[k] = r.Replace(v)
		}
		d.Env = resolved
	}
}

// Prepare builds the docker image. Implements the Container interface.
func (d *DockerContainer) Prepare(ctx context.Context) error {
	return d.EnsureImage(ctx)
}

// GetContainerName returns "" — oneshot containers have no persistent name.
func (d *DockerContainer) GetContainerName() string { return "" }

// SetSourceWritable marks the source mount as read-write.
func (d *DockerContainer) SetSourceWritable(writable bool) { d.SourceWritable = writable }

// SetContainerName is not supported for oneshot docker containers.
func (d *DockerContainer) SetContainerName(_ string) bool { return false }

// ApplyAgentEnv merges agent-level env overrides into d.Env.
// Non-empty values override ForwardEnv; empty values suppress forwarding.
func (d *DockerContainer) ApplyAgentEnv(env map[string]string) {
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

// EnsureImage builds the docker image, relying on Docker's layer cache for speed.
// Always runs docker build so Dockerfile changes are picked up automatically.
func (d *DockerContainer) EnsureImage(ctx context.Context) error {
	defer d.cleanupDockerfileTmpDir()

	// Build from Dockerfile
	if d.Dockerfile == "" {
		return fmt.Errorf("no Dockerfile configured for docker container")
	}
	// Resolve symlinks so Docker can read the actual file.
	// Symlinked Dockerfiles (e.g. .ateamorg/Dockerfile → .../defaults/Dockerfile)
	// point outside the build context, which BuildKit can't follow.
	dockerfilePath, err := filepath.EvalSymlinks(d.Dockerfile)
	if err != nil {
		return fmt.Errorf("dockerfile not found: %s", d.Dockerfile)
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

func (d *DockerContainer) cleanupDockerfileTmpDir() {
	if d.DockerfileTmpDir != "" {
		os.RemoveAll(d.DockerfileTmpDir)
		d.DockerfileTmpDir = ""
	}
}

// baseRunArgs builds the common docker run argument list shared by CmdFactory and DebugCommand.
// When forExec is true, environment variables are forwarded as KEY=VALUE (resolving host values);
// when false (for display/logging), they are passed as bare KEY names.
func (d *DockerContainer) baseRunArgs(forExec bool) []string {
	containerCodePath, containerWorkDir, containerOrgPath := d.containerPaths()
	mount := d.mountDir()

	args := []string{"run", "--rm", "-i"}

	if mount != "" {
		args = append(args, "-v", mount+":"+containerCodePath+":"+d.sourceMountMode())
	}
	if d.OrgDir != "" {
		args = append(args, "-v", d.OrgDir+":"+containerOrgPath+":rw")
	}
	args = append(args, d.projectDirArgs()...)
	if d.HostCLIPath != "" {
		args = append(args, "-v", d.HostCLIPath+":/usr/local/bin/ateam:ro")
	}
	if d.ClaudeCredentialsFile != "" {
		args = append(args, "-v", d.ClaudeCredentialsFile+":/home/agent/.claude/.credentials.json:ro")
	}
	for _, vol := range d.ExtraVolumes {
		args = append(args, "-v", vol)
	}
	args = append(args, timezoneArgs()...)
	args = append(args, "-w", containerWorkDir)
	args = append(args, d.ExtraArgs...)

	if forExec {
		for _, key := range d.ForwardEnv {
			if _, overridden := d.Env[key]; overridden {
				continue // handled by envArgs
			}
			if val, ok := os.LookupEnv(key); ok {
				args = append(args, "-e", key+"="+val)
			}
		}
	} else {
		for _, key := range d.ForwardEnv {
			if _, overridden := d.Env[key]; overridden {
				continue
			}
			args = append(args, "-e", key)
		}
	}
	args = append(args, d.envArgs()...)
	args = append(args, d.Image)
	return args
}

// CmdFactory returns a function that wraps commands in `docker run --rm -i`.
func (d *DockerContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		dockerArgs := d.baseRunArgs(true)
		dockerArgs = append(dockerArgs, name)
		dockerArgs = append(dockerArgs, args...)

		cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
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

	// Check OrgDir first (most specific non-code path)
	if d.OrgDir != "" {
		if rel, ok := relativeUnder(hostPath, d.OrgDir); ok {
			return filepath.Join(containerOrgRoot, rel)
		}
	}

	// Check MountDir (git root or sourceDir)
	mount := d.mountDir()
	if mount != "" {
		if rel, ok := relativeUnder(hostPath, mount); ok {
			return filepath.Join(containerCodeRoot, rel)
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
	parts := append([]string{"docker"}, d.baseRunArgs(false)...)
	parts = append(parts, opts.Command)
	parts = append(parts, opts.Args...)
	return strings.Join(parts, " ")
}

// sourceMountMode returns "rw" or "ro" for the source code volume mount.
func (d *DockerContainer) sourceMountMode() string {
	if d.SourceWritable {
		return "rw"
	}
	return "ro"
}

// mountDir returns the effective mount source: MountDir if set, otherwise SourceDir.
func (d *DockerContainer) mountDir() string {
	if d.MountDir != "" {
		return d.MountDir
	}
	return d.SourceDir
}

// containerPaths computes the container paths for code, workdir, and orgdir.
// Code is mounted at /workspace, org at /.ateamorg.
func (d *DockerContainer) containerPaths() (codePath, workDir, orgPath string) {
	codePath = containerCodeRoot
	orgPath = containerOrgRoot

	// If sourceDir is a subdirectory of mountDir (git root), workdir is /workspace/<rel>
	mount := d.mountDir()
	if mount != "" && d.SourceDir != mount {
		if rel, err := filepath.Rel(mount, d.SourceDir); err == nil && !strings.HasPrefix(rel, "..") {
			workDir = filepath.Join(containerCodeRoot, rel)
			return
		}
	}
	workDir = containerCodeRoot
	return
}

// projectDirArgs returns docker -v args to mount .ateam/ read-write,
// overlaying the read-only source code mount so agents can write state files.
func (d *DockerContainer) projectDirArgs() []string {
	if d.ProjectDir == "" {
		return nil
	}
	containerPath := d.TranslatePath(d.ProjectDir)
	if containerPath == d.ProjectDir {
		return nil
	}
	return []string{"-v", d.ProjectDir + ":" + containerPath + ":rw"}
}

// timezoneArgs returns docker args to forward the host timezone into the container.
func timezoneArgs() []string {
	if _, err := os.Stat("/etc/localtime"); err == nil {
		return []string{"-v", "/etc/localtime:/etc/localtime:ro"}
	}
	return nil
}

// envArgs returns sorted -e KEY=VALUE args for the Env map.
// Empty values are skipped — they serve as suppression markers
// (e.g. from credential isolation stripping competing credentials).
func (d *DockerContainer) envArgs() []string {
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
