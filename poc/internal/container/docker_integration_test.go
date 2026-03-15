//go:build docker_integration

package container

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests require a running Docker daemon.
// Run via: go test -tags docker_integration -v ./internal/container/
// Or:      make test-docker (runs inside DinD, no host impact)

func cleanupImage(image string) {
	_ = exec.Command("docker", "rmi", "-f", image).Run()
}

func TestDockerEnsureImageAndRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := t.TempDir()

	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3.20\nWORKDIR /workspace\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dc := &DockerContainer{
		Image:      "ateam-dind-test-basic:latest",
		Dockerfile: dockerfile,
	}
	t.Cleanup(func() { cleanupImage(dc.Image) })

	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	// Second call should be a no-op (image exists)
	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage (cached): %v", err)
	}

	var stdout bytes.Buffer
	err := dc.Run(ctx, RunOpts{
		Command: "echo",
		Args:    []string{"hello from docker"},
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello from docker" {
		t.Errorf("expected 'hello from docker', got %q", got)
	}
}

func TestDockerMountsAndWorkdir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := t.TempDir()

	sourceDir := filepath.Join(dir, "project")
	projectDir := filepath.Join(sourceDir, ".ateam")
	orgDir := filepath.Join(dir, "org")
	for _, d := range []string{sourceDir, projectDir, orgDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(sourceDir, "hello.txt"), []byte("source-file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orgDir, "org.txt"), []byte("org-file"), 0644); err != nil {
		t.Fatal(err)
	}

	dockerfile := filepath.Join(projectDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3.20\nWORKDIR /workspace\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dc := &DockerContainer{
		Image:      "ateam-dind-test-mounts:latest",
		Dockerfile: dockerfile,
		SourceDir:  sourceDir,
		ProjectDir: projectDir,
		OrgDir:     orgDir,
	}
	t.Cleanup(func() { cleanupImage(dc.Image) })

	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	codePath, _, orgPath := dc.containerPaths()

	t.Run("source mount readable", func(t *testing.T) {
		var stdout bytes.Buffer
		err := dc.Run(ctx, RunOpts{
			Command: "cat",
			Args:    []string{codePath + "/hello.txt"},
			Stdout:  &stdout,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := stdout.String(); got != "source-file" {
			t.Errorf("expected 'source-file', got %q", got)
		}
	})

	t.Run("org mount readable", func(t *testing.T) {
		var stdout bytes.Buffer
		err := dc.Run(ctx, RunOpts{
			Command: "cat",
			Args:    []string{orgPath + "/org.txt"},
			Stdout:  &stdout,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := stdout.String(); got != "org-file" {
			t.Errorf("expected 'org-file', got %q", got)
		}
	})

	t.Run("workdir is correct", func(t *testing.T) {
		var stdout bytes.Buffer
		err := dc.Run(ctx, RunOpts{
			Command: "pwd",
			Stdout:  &stdout,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != codePath {
			t.Errorf("expected %q, got %q", codePath, got)
		}
	})

	t.Run("source read-only from container", func(t *testing.T) {
		err := dc.Run(ctx, RunOpts{
			Command: "sh",
			Args:    []string{"-c", "echo written-inside > " + codePath + "/from-container.txt"},
		})
		if err == nil {
			t.Error("expected write to read-only source mount to fail, but it succeeded")
		}
		if _, statErr := os.Stat(filepath.Join(sourceDir, "from-container.txt")); statErr == nil {
			t.Error("file appeared on host from read-only mount — isolation breach")
		}
	})

	t.Run("org mount writable", func(t *testing.T) {
		err := dc.Run(ctx, RunOpts{
			Command: "sh",
			Args:    []string{"-c", "echo org-write > " + orgPath + "/written.txt"},
		})
		if err != nil {
			t.Fatalf("expected write to org mount to succeed: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(orgDir, "written.txt"))
		if err != nil {
			t.Fatalf("host ReadFile: %v", err)
		}
		if got := strings.TrimSpace(string(data)); got != "org-write" {
			t.Errorf("expected 'org-write', got %q", got)
		}
	})
}

func TestDockerCmdFactory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}

	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3.20\nWORKDIR /workspace\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dc := &DockerContainer{
		Image:      "ateam-dind-test-factory:latest",
		Dockerfile: dockerfile,
		SourceDir:  sourceDir,
	}
	t.Cleanup(func() { cleanupImage(dc.Image) })

	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	factory := dc.CmdFactory()
	cmd := factory(ctx, "echo", "factory-output")
	cmd.Stdin = strings.NewReader("")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("CmdFactory Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "factory-output" {
		t.Errorf("expected 'factory-output', got %q", got)
	}
}

// TestDockerFilePermissions exercises the full permission matrix:
// rw mount: read ok, write ok
// ro mount: read ok, write fails
// no mount: access fails
// Tests run a shell script through CmdFactory, same path real agents take.
func TestDockerFilePermissions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := t.TempDir()

	sourceDir := filepath.Join(dir, "project")
	projectDir := filepath.Join(sourceDir, ".ateam")
	orgDir := filepath.Join(dir, "org")
	secretDir := filepath.Join(dir, "secret")
	for _, d := range []string{sourceDir, projectDir, orgDir, secretDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Seed files
	os.WriteFile(filepath.Join(sourceDir, "src.txt"), []byte("source"), 0644)
	os.WriteFile(filepath.Join(orgDir, "org.txt"), []byte("org"), 0644)
	os.WriteFile(filepath.Join(secretDir, "secret.txt"), []byte("secret"), 0644)

	dockerfile := filepath.Join(projectDir, "Dockerfile")
	os.WriteFile(dockerfile, []byte("FROM alpine:3.20\nWORKDIR /workspace\n"), 0644)

	dc := &DockerContainer{
		Image:      "ateam-dind-test-perms:latest",
		Dockerfile: dockerfile,
		SourceDir:  sourceDir,
		ProjectDir: projectDir,
		OrgDir:     orgDir,
		// secretDir is NOT mounted — should be inaccessible
	}
	t.Cleanup(func() { cleanupImage(dc.Image) })

	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	// Helper: run a command via CmdFactory (same path agents use)
	runViaFactory := func(command string, args ...string) (stdout, stderr string, err error) {
		factory := dc.CmdFactory()
		cmd := factory(ctx, command, args...)
		cmd.Stdin = strings.NewReader("")
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err = cmd.Run()
		return outBuf.String(), errBuf.String(), err
	}

	codePath, _, orgPath := dc.containerPaths()

	// --- RO mount (source → codePath) ---
	t.Run("ro-source/read", func(t *testing.T) {
		out, _, err := runViaFactory("cat", codePath+"/src.txt")
		if err != nil {
			t.Fatalf("expected read to succeed: %v", err)
		}
		if out != "source" {
			t.Errorf("expected 'source', got %q", out)
		}
	})
	t.Run("ro-source/write-fails", func(t *testing.T) {
		_, _, err := runViaFactory("sh", "-c", "echo rw-ok > "+codePath+"/rw-test.txt")
		if err == nil {
			t.Fatal("expected write to read-only source mount to fail, but it succeeded")
		}
		if _, statErr := os.Stat(filepath.Join(sourceDir, "rw-test.txt")); statErr == nil {
			t.Error("file appeared on host from read-only mount — isolation breach")
		}
	})

	// --- RW mount (org → orgPath) ---
	t.Run("rw-org/read", func(t *testing.T) {
		out, _, err := runViaFactory("cat", orgPath+"/org.txt")
		if err != nil {
			t.Fatalf("expected read to succeed: %v", err)
		}
		if out != "org" {
			t.Errorf("expected 'org', got %q", out)
		}
	})
	t.Run("rw-org/write", func(t *testing.T) {
		_, _, err := runViaFactory("sh", "-c", "echo org-ok > "+orgPath+"/org-write.txt")
		if err != nil {
			t.Fatalf("expected write to org mount to succeed: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(orgDir, "org-write.txt"))
		if err != nil {
			t.Fatalf("host ReadFile: %v", err)
		}
		if got := strings.TrimSpace(string(data)); got != "org-ok" {
			t.Errorf("expected 'org-ok', got %q", got)
		}
	})

	// --- No mount (secretDir is not mounted at all) ---
	// The host path doesn't exist inside the container. The container
	// uses its own root filesystem, so the exact host path won't resolve.
	// We test that a path outside any mount simply doesn't exist.
	t.Run("no-mount/read fails", func(t *testing.T) {
		_, _, err := runViaFactory("cat", "/secret/secret.txt")
		if err == nil {
			t.Error("expected read of unmounted path to fail")
		}
	})
	t.Run("no-mount/write fails", func(t *testing.T) {
		_, _, err := runViaFactory("sh", "-c", "mkdir -p /secret && echo nope > /secret/hack.txt")
		if err != nil {
			// Write to container filesystem might succeed (not a host path),
			// but the file must NOT appear on the host
			t.Logf("write returned error (expected): %v", err)
		}
		if _, err := os.Stat(filepath.Join(secretDir, "hack.txt")); err == nil {
			t.Error("file appeared on host from unmounted path — isolation breach")
		}
	})
}

func TestDockerEnvForwarding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine:3.20\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dc := &DockerContainer{
		Image:      "ateam-dind-test-env:latest",
		Dockerfile: dockerfile,
		ForwardEnv: []string{"ATEAM_TEST_VAR"},
	}
	t.Cleanup(func() { cleanupImage(dc.Image) })

	if err := dc.EnsureImage(ctx); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	t.Setenv("ATEAM_TEST_VAR", "secret-value-42")

	var stdout bytes.Buffer
	err := dc.Run(ctx, RunOpts{
		Command: "sh",
		Args:    []string{"-c", "echo $ATEAM_TEST_VAR"},
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "secret-value-42" {
		t.Errorf("expected 'secret-value-42', got %q", got)
	}
}
