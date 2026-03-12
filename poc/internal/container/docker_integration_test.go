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

	t.Run("source mount readable", func(t *testing.T) {
		var stdout bytes.Buffer
		err := dc.Run(ctx, RunOpts{
			Command: "cat",
			Args:    []string{"/workspace/hello.txt"},
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
			Args:    []string{"/.ateamorg/org.txt"},
			Stdout:  &stdout,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := stdout.String(); got != "org-file" {
			t.Errorf("expected 'org-file', got %q", got)
		}
	})

	t.Run("workdir is /workspace", func(t *testing.T) {
		var stdout bytes.Buffer
		err := dc.Run(ctx, RunOpts{
			Command: "pwd",
			Stdout:  &stdout,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "/workspace" {
			t.Errorf("expected '/workspace', got %q", got)
		}
	})

	t.Run("source writable from container", func(t *testing.T) {
		err := dc.Run(ctx, RunOpts{
			Command: "sh",
			Args:    []string{"-c", "echo written-inside > /workspace/from-container.txt"},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(sourceDir, "from-container.txt"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if got := strings.TrimSpace(string(data)); got != "written-inside" {
			t.Errorf("expected 'written-inside', got %q", got)
		}
	})

	t.Run("org mount read-only", func(t *testing.T) {
		err := dc.Run(ctx, RunOpts{
			Command: "sh",
			Args:    []string{"-c", "echo nope > /.ateamorg/nope.txt"},
		})
		if err == nil {
			t.Error("expected error writing to read-only org mount")
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
