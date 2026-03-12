//go:build docker_live

package container

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func liveTestUID() string {
	if u, err := user.Current(); err == nil && u.Uid != "0" {
		return u.Uid
	}
	return "1000"
}

// These tests run real Claude (haiku) inside Docker containers.
// Requirements:
//   - Running Docker daemon
//   - Either ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN env var set
//   - Internet access (Anthropic API)
//
// Run via: make test-docker-live
// Cost: ~$0.01-0.03 per test run (haiku model)

const (
	liveImage       = "ateam-live-test:latest"
	liveTestTimeout = 5 * time.Minute
)

// buildOnce ensures the claude-code image is built exactly once across all tests.
var buildOnce sync.Once
var buildErr error

func ensureLiveImage(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ateam-live-dockerfile-*")
		if err != nil {
			buildErr = err
			return
		}
		df := filepath.Join(dir, "Dockerfile")
		os.WriteFile(df, []byte("FROM node:22-slim\nRUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/* && npm install -g @anthropic-ai/claude-code\nARG USER_UID=1000\nRUN useradd -m -u $USER_UID ateam\nUSER ateam\nWORKDIR /workspace\n"), 0644)
		cmd := exec.Command("docker", "build", "--build-arg", "USER_UID="+liveTestUID(), "-t", liveImage, "-f", df, dir)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatalf("failed to build live test image: %v", buildErr)
	}

	// Smoke test: verify claude is callable inside the container
	var vOut, vErr bytes.Buffer
	vCmd := exec.Command("docker", "run", "--rm", liveImage, "claude", "--version")
	vCmd.Stdout = &vOut
	vCmd.Stderr = &vErr
	if err := vCmd.Run(); err != nil {
		t.Fatalf("claude --version failed inside container: %v\nstdout: %s\nstderr: %s", err, vOut.String(), vErr.String())
	}
	t.Logf("container claude version: %s", strings.TrimSpace(vOut.String()))
}

func requireAuth(t *testing.T) {
	t.Helper()
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		t.Fatalf("neither ANTHROPIC_API_KEY nor CLAUDE_CODE_OAUTH_TOKEN set — see 'make test-docker-live' for setup")
	}
}

// authEnvVars returns the env var names that should be forwarded to the container.
func authEnvVars() []string {
	var vars []string
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		vars = append(vars, "ANTHROPIC_API_KEY")
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
		vars = append(vars, "CLAUDE_CODE_OAUTH_TOKEN")
	}
	return vars
}

// claudeArgs builds the standard claude CLI args for live tests.
// --dangerously-skip-permissions is required for tool use in non-interactive containers.
func claudeArgs(maxTurns string, prompt string) []string {
	return []string{
		"-p",
		"--dangerously-skip-permissions",
		"--output-format", "text",
		"--model", "haiku",
		"--max-turns", maxTurns,
		prompt,
	}
}

// runClaude is a helper that runs claude in a DockerContainer and returns
// stdout/stderr, failing the test with full diagnostic output on error.
func runClaude(t *testing.T, ctx context.Context, dc *DockerContainer, args []string) (stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err := dc.Run(ctx, RunOpts{
		Command: "claude",
		Args:    args,
		Stdout:  &outBuf,
		Stderr:  &errBuf,
	})
	if err != nil {
		t.Fatalf("claude run failed: %v\nstdout: %s\nstderr: %s", err, outBuf.String(), errBuf.String())
	}
	return outBuf.String(), errBuf.String()
}

func TestLiveClaudeReadFile(t *testing.T) {
	requireAuth(t)
	ensureLiveImage(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTestTimeout)
	defer cancel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "project")
	os.MkdirAll(sourceDir, 0755)

	os.WriteFile(filepath.Join(sourceDir, "test-data.txt"), []byte("The answer is 42."), 0644)

	dc := &DockerContainer{
		Image:      liveImage,
		SourceDir:  sourceDir,
		ForwardEnv: authEnvVars(),
	}

	stdout, _ := runClaude(t, ctx, dc, claudeArgs("2",
		"Read /workspace/test-data.txt and reply with ONLY its exact contents.",
	))

	if !strings.Contains(stdout, "42") {
		t.Errorf("expected output to contain '42', got: %s", stdout)
	}
}

func TestLiveClaudeWriteFile(t *testing.T) {
	requireAuth(t)
	ensureLiveImage(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTestTimeout)
	defer cancel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "project")
	os.MkdirAll(sourceDir, 0755)

	dc := &DockerContainer{
		Image:      liveImage,
		SourceDir:  sourceDir,
		ForwardEnv: authEnvVars(),
	}

	runClaude(t, ctx, dc, claudeArgs("3",
		"Create a file at /workspace/agent-output.txt containing exactly 'written-by-agent'. Reply 'done' when finished.",
	))

	data, err := os.ReadFile(filepath.Join(sourceDir, "agent-output.txt"))
	if err != nil {
		t.Fatalf("agent did not create file on host: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "written-by-agent" {
		t.Errorf("expected 'written-by-agent', got %q", got)
	}
}

func TestLiveClaudeOrgReadOnly(t *testing.T) {
	requireAuth(t)
	ensureLiveImage(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTestTimeout)
	defer cancel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "project")
	orgDir := filepath.Join(dir, "org")
	os.MkdirAll(sourceDir, 0755)
	os.MkdirAll(orgDir, 0755)

	os.WriteFile(filepath.Join(orgDir, "config.txt"), []byte("org-config-value"), 0644)

	dc := &DockerContainer{
		Image:      liveImage,
		SourceDir:  sourceDir,
		OrgDir:     orgDir,
		ForwardEnv: authEnvVars(),
	}

	stdout, _ := runClaude(t, ctx, dc, claudeArgs("2",
		"Read /.ateamorg/config.txt and reply with ONLY its exact contents.",
	))

	if !strings.Contains(stdout, "org-config-value") {
		t.Errorf("expected 'org-config-value' in output, got: %s", stdout)
	}
}

func TestLiveClaudeNoAccessOutsideMounts(t *testing.T) {
	requireAuth(t)
	ensureLiveImage(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTestTimeout)
	defer cancel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "project")
	os.MkdirAll(sourceDir, 0755)

	// Secret file on host — NOT mounted into container
	secretDir := filepath.Join(dir, "secret")
	os.MkdirAll(secretDir, 0755)
	os.WriteFile(filepath.Join(secretDir, "password.txt"), []byte("super-secret"), 0644)

	dc := &DockerContainer{
		Image:      liveImage,
		SourceDir:  sourceDir,
		ForwardEnv: authEnvVars(),
	}

	// The host path won't exist inside the container at all.
	var outBuf, errBuf bytes.Buffer
	err := dc.Run(ctx, RunOpts{
		Command: "claude",
		Args: claudeArgs("2",
			"Try to read "+filepath.Join(secretDir, "password.txt")+". If you cannot, reply 'ACCESS_DENIED'.",
		),
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	_ = err // agent may error or report denial — both are fine

	if strings.Contains(outBuf.String(), "super-secret") {
		t.Error("agent read unmounted host path — isolation breach")
	}
}
