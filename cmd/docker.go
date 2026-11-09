package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

// deriveDockerImageName builds the per-project Docker image name. It assumes
// the standard ateam layout where projectDir is `<project root>/.ateam`,
// so filepath.Dir(projectDir) is the project root and its basename is the
// project name (used to scope the image and avoid clashes between projects).
func deriveDockerImageName(projectDir string) string {
	return "ateam-" + filepath.Base(filepath.Dir(projectDir)) + ":latest"
}

// buildContainer creates a Container implementation from config.
// Returns nil for "none" type (runner treats nil as host execution).
// roleID is used for Dockerfile resolution (role-specific Dockerfiles).
func buildContainer(cc *runtime.ContainerConfig, prof *runtime.ProfileConfig, env *root.ResolvedEnv, roleID string, dockerAutoSetup bool) (container.Container, error) {
	if cc == nil || cc.Type == "none" {
		return nil, nil
	}
	sourceDir, projectDir, homeDir, gitRepoDir := env.WorkDir, env.ProjectDir, env.HomeDir, env.GitRepoDir
	switch cc.Type {
	case "docker":
		image := deriveDockerImageName(projectDir)
		if dockerAutoSetup {
			if generated, names, err := runtime.AutoSetupDockerfile(sourceDir, projectDir, env.Layers); err != nil {
				fmt.Fprintf(os.Stderr, "[docker] auto-setup warning: %v\n", err)
			} else if generated {
				fmt.Fprintf(os.Stderr, "[docker] auto-generated .ateam/Dockerfile (%s)\n", strings.Join(names, ", "))
			}
		}
		dockerfile, dockerfileTmpDir, err := runtime.ResolveDockerfile(cc, env.Layers, roleID)
		if err != nil {
			return nil, err
		}
		// Resolve relative paths in extra_volumes from project dir
		volumes := make([]string, len(cc.ExtraVolumes))
		for i, vol := range cc.ExtraVolumes {
			resolved, err := resolveVolumePath(vol, sourceDir, sourceDir, homeDir)
			if err != nil {
				return nil, err
			}
			volumes[i] = resolved
		}
		var extraArgs []string
		if prof != nil {
			extraArgs = prof.ContainerExtraArgs
		}
		mountDir := sourceDir
		if gitRepoDir != "" {
			mountDir = gitRepoDir
		}
		var claudeCredentialsFile string
		if cc.MountClaudeConfig {
			if home, err := os.UserHomeDir(); err == nil {
				f := filepath.Join(home, ".claude", ".credentials.json")
				if _, err := os.Stat(f); err == nil {
					claudeCredentialsFile = f
				}
			}
		}
		dc := &container.DockerContainer{
			Image:                 image,
			Dockerfile:            dockerfile,
			DockerfileTmpDir:      dockerfileTmpDir,
			ForwardEnv:            cc.ForwardEnv,
			ExtraVolumes:          volumes,
			ExtraArgs:             extraArgs,
			MountDir:              mountDir,
			SourceDir:             sourceDir,
			ProjectDir:            projectDir,
			HomeDir:               homeDir,
			HostCLIPath:           findLinuxBinary(homeDir),
			ClaudeCredentialsFile: claudeCredentialsFile,
		}
		dc.UseSharedPrepareGuard()
		return dc, nil
	case "docker-exec":
		if cc.DockerContainer == "" {
			cc.DockerContainer = "{{CONTAINER_NAME}}"
		}
		precheckCmd := runtime.ResolvePrecheckCmd(cc, env.Layers, roleID)
		containerWorkDir := "/workspace"
		if sourceDir != "" && gitRepoDir != "" && gitRepoDir != sourceDir {
			if rel, err := filepath.Rel(gitRepoDir, sourceDir); err == nil {
				containerWorkDir = filepath.Join("/workspace", rel)
			}
		}
		var hostCLIPath string
		if cc.CopyAteam {
			hostCLIPath = findLinuxBinary(homeDir)
		}
		de := &container.DockerExecContainer{
			ContainerName: cc.DockerContainer,
			ExecTemplate:  cc.ExecTemplate,
			ForwardEnv:    cc.ForwardEnv,
			WorkDir:       containerWorkDir,
			HostCLIPath:   hostCLIPath,
			PrecheckCmd:   precheckCmd,
		}
		de.UseSharedPrepareGuard()
		return de, nil
	default:
		return nil, nil
	}
}

// linuxCompanionName is the on-disk name of the linux companion binary for
// the host's CPU arch (e.g. ateam-linux-arm64 on Apple Silicon,
// ateam-linux-amd64 on Intel). Matched to the host arch because the
// container engine on the host typically defaults to the same platform —
// running an amd64 ateam under x86_64 emulation in an arm64 container
// triggers Go runtime GC crashes ("found pointer to free object").
func linuxCompanionName() string {
	return "ateam-linux-" + goruntime.GOARCH
}

// findLinuxBinary locates or builds a linux ateam binary for the host's
// arch, suitable for pushing into a same-arch Docker container.
// Search order: self (if linux), companion binary, the user cache under
// ~/.ateam/state/cache, cross-compile.
func findLinuxBinary(homeDir string) string {
	name := linuxCompanionName()

	// 1. Already on linux — the running binary is linux/<host arch> by
	// definition, so just use it.
	if goruntime.GOOS == "linux" {
		if exe, err := os.Executable(); err == nil {
			return exe
		}
		return ""
	}

	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// 2. build/ directory (from `make companion` in dev).
	buildDir := filepath.Join(filepath.Dir(exe), "build", name)
	if info, err := os.Stat(buildDir); err == nil && !info.IsDir() {
		return buildDir
	}

	// 3. Companion binary next to host binary (e.g. from a release archive).
	companion := filepath.Join(filepath.Dir(exe), name)
	if info, err := os.Stat(companion); err == nil && !info.IsDir() {
		return companion
	}

	// 4. Cached under the user state dir from a prior auto cross-compilation.
	if homeDir != "" {
		cached := filepath.Join(binaryCacheDir(homeDir), name)
		if info, err := os.Stat(cached); err == nil && !info.IsDir() {
			return cached
		}
	}

	// 5. Cross-compile if Go toolchain is available.
	if homeDir != "" {
		if built := crossBuildIfPossible(exe, homeDir); built != "" {
			return built
		}
	}

	fmt.Fprintf(os.Stderr, "Warning: no Linux ateam binary found for Docker; "+
		"place %s next to %s or install Go to cross-compile\n", name, exe)
	return ""
}

// binaryCacheDir is where cross-compiled companion binaries are cached.
func binaryCacheDir(homeDir string) string {
	return filepath.Join(homeDir, runtime.StateSubdir, "cache")
}

// crossBuildIfPossible cross-compiles ateam for linux/<host arch> if go.mod
// exists next to hostExe and `go` is in PATH. The result is cached under
// ~/.ateam/state/cache/ and reused if the host binary hasn't changed.
func crossBuildIfPossible(hostExe, homeDir string) string {
	modDir := filepath.Dir(hostExe)
	if _, err := os.Stat(filepath.Join(modDir, "go.mod")); err != nil {
		return ""
	}
	if _, err := exec.LookPath("go"); err != nil {
		return ""
	}

	cacheDir := binaryCacheDir(homeDir)
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return ""
	}
	target := filepath.Join(cacheDir, linuxCompanionName())

	// Rebuild only if the target is missing or older than the host binary.
	hostInfo, _ := os.Stat(hostExe)
	targetInfo, targetErr := os.Stat(target)
	if targetErr == nil && hostInfo != nil && !targetInfo.ModTime().Before(hostInfo.ModTime()) {
		return target
	}

	fmt.Fprintf(os.Stderr, "Cross-compiling ateam for linux/%s...\n", goruntime.GOARCH)
	cmd := exec.Command("go", "build", "-o", target, ".")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goruntime.GOARCH, "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cross-build failed: %v\n", err)
		return ""
	}
	return target
}

func dockerExecOutput(container string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", container}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker exec %s %s: %w", container, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveContainerName resolves a possibly-partial container name to the exact
// Docker container name via substring matching. Stored in a var so tests can
// stub out the Docker dependency, mirroring runAutoRoles.
var resolveContainerName = func(name string) (string, error) {
	return container.ResolveRunningContainerName(context.Background(), name)
}

func dockerCp(src, dst string) error {
	cmd := exec.Command("docker", "cp", src, dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker cp %s → %s: %w", src, dst, err)
	}
	return nil
}

// resolveVolumePath resolves relative host paths in a volume spec and validates
// that the resolved path stays within allowedDirs. Volume format:
// "hostPath:containerPath[:mode]". Relative hostPath is resolved from baseDir.
func resolveVolumePath(vol, baseDir string, allowedDirs ...string) (string, error) {
	parts := splitVolumeSpec(vol)
	if len(parts) < 2 {
		return vol, nil
	}
	hostPath := display.ExpandHome(parts[0])
	if !filepath.IsAbs(hostPath) {
		hostPath = filepath.Join(baseDir, hostPath)
	}
	hostPath = filepath.Clean(hostPath)

	allowed := false
	for _, dir := range allowedDirs {
		if dir == "" {
			continue
		}
		rel, err := filepath.Rel(dir, hostPath)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("volume path %s escapes project boundary", hostPath)
	}

	parts[0] = hostPath
	result := parts[0] + ":" + parts[1]
	if len(parts) > 2 {
		result += ":" + parts[2]
	}
	return result, nil
}

// splitVolumeSpec splits "host:container[:mode]" respecting that
// host path on Windows might contain a drive letter (C:\...).
func splitVolumeSpec(vol string) []string {
	parts := []string{}
	for remaining := vol; remaining != ""; {
		idx := strings.IndexByte(remaining, ':')
		if idx < 0 {
			parts = append(parts, remaining)
			break
		}
		parts = append(parts, remaining[:idx])
		remaining = remaining[idx+1:]
	}
	return parts
}
