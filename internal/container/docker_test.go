package container

import (
	"context"
	"strings"
	"testing"
)

func TestTranslatePath(t *testing.T) {
	dc := &DockerContainer{
		SourceDir:  "/Users/nic/projects/myapp",
		ProjectDir: "/Users/nic/projects/myapp/.ateam",
		OrgDir:     "/Users/nic/.ateamorg",
	}

	tests := []struct {
		name     string
		hostPath string
		want     string
	}{
		{"empty", "", ""},
		{"source root", "/Users/nic/projects/myapp", "/workspace"},
		{"file in source", "/Users/nic/projects/myapp/src/main.go", "/workspace/src/main.go"},
		{"project dir", "/Users/nic/projects/myapp/.ateam", "/workspace/.ateam"},
		{"file in project", "/Users/nic/projects/myapp/.ateam/logs/stream.jsonl", "/workspace/.ateam/logs/stream.jsonl"},
		{"org dir", "/Users/nic/.ateamorg", "/.ateamorg"},
		{"file in org", "/Users/nic/.ateamorg/runtime.hcl", "/.ateamorg/runtime.hcl"},
		{"unrelated path", "/tmp/something", "/tmp/something"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dc.TranslatePath(tt.hostPath)
			if got != tt.want {
				t.Errorf("TranslatePath(%q) = %q, want %q", tt.hostPath, got, tt.want)
			}
		})
	}
}

func TestTranslatePathWithGitRoot(t *testing.T) {
	dc := &DockerContainer{
		MountDir:   "/Users/nic/projects/repo",
		SourceDir:  "/Users/nic/projects/repo/myapp",
		ProjectDir: "/Users/nic/projects/repo/myapp/.ateam",
		OrgDir:     "/Users/nic/.ateamorg",
	}

	tests := []struct {
		name     string
		hostPath string
		want     string
	}{
		{"git root", "/Users/nic/projects/repo", "/workspace"},
		{"source dir (subdir of git root)", "/Users/nic/projects/repo/myapp", "/workspace/myapp"},
		{"file in source", "/Users/nic/projects/repo/myapp/main.go", "/workspace/myapp/main.go"},
		{"file in git root outside source", "/Users/nic/projects/repo/README.md", "/workspace/README.md"},
		{"org dir", "/Users/nic/.ateamorg", "/.ateamorg"},
		{"file in org", "/Users/nic/.ateamorg/defaults/runtime.hcl", "/.ateamorg/defaults/runtime.hcl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dc.TranslatePath(tt.hostPath)
			if got != tt.want {
				t.Errorf("TranslatePath(%q) = %q, want %q", tt.hostPath, got, tt.want)
			}
		})
	}
}

func TestCmdFactoryProducesDockerArgs(t *testing.T) {
	dc := &DockerContainer{
		Image:      "ateam-test:latest",
		SourceDir:  "/Users/nic/projects/myapp",
		ProjectDir: "/Users/nic/projects/myapp/.ateam",
		OrgDir:     "/Users/nic/.ateamorg",
		ForwardEnv: []string{"ANTHROPIC_API_KEY"},
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "-p", "--verbose")

	if cmd.Path == "" {
		t.Fatal("expected non-empty command path")
	}

	args := cmd.Args
	if len(args) < 2 || args[1] != "run" {
		t.Fatalf("expected docker run, got %v", args)
	}

	// Check that the actual command appears at the end
	found := false
	for i, a := range args {
		if a == "claude" {
			if i+2 < len(args) && args[i+1] == "-p" && args[i+2] == "--verbose" {
				found = true
			}
			break
		}
	}
	if !found {
		t.Errorf("expected 'claude -p --verbose' in args, got %v", args)
	}

	hasMount := func(mount string) bool {
		for _, a := range args {
			if a == mount {
				return true
			}
		}
		return false
	}
	if !hasMount("/Users/nic/projects/myapp:/workspace:ro") {
		t.Errorf("missing source mount, args: %v", args)
	}
	if !hasMount("/Users/nic/.ateamorg:/.ateamorg:rw") {
		t.Errorf("missing org mount, args: %v", args)
	}
}

func TestCmdFactoryWithGitRoot(t *testing.T) {
	dc := &DockerContainer{
		Image:      "ateam-test:latest",
		MountDir:   "/Users/nic/projects/repo",
		SourceDir:  "/Users/nic/projects/repo/myapp",
		ProjectDir: "/Users/nic/projects/repo/myapp/.ateam",
		OrgDir:     "/Users/nic/.ateamorg",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "-p")
	args := cmd.Args

	hasArg := func(flag, value string) bool {
		for i, a := range args {
			if a == flag && i+1 < len(args) && args[i+1] == value {
				return true
			}
		}
		return false
	}

	// Code mount should be git root → /workspace
	if !hasArg("-v", "/Users/nic/projects/repo:/workspace:ro") {
		t.Errorf("missing git root mount, args: %v", args)
	}
	// Working dir should be source dir → /workspace/myapp
	if !hasArg("-w", "/workspace/myapp") {
		t.Errorf("missing workdir, args: %v", args)
	}
}

func TestDebugCommand(t *testing.T) {
	dc := &DockerContainer{
		Image:      "ateam-test:latest",
		SourceDir:  "/src",
		OrgDir:     "/org",
		ForwardEnv: []string{"ANTHROPIC_API_KEY"},
	}

	got := dc.DebugCommand(RunOpts{
		Command: "claude",
		Args:    []string{"-p", "--verbose"},
	})

	// Timezone mount is platform-dependent (/etc/localtime may or may not exist),
	// so check required parts instead of exact match.
	for _, substr := range []string{
		"docker run --rm -i",
		"-v /src:/workspace:ro",
		"-v /org:/.ateamorg:rw",
		"-w /workspace",
		"-e ANTHROPIC_API_KEY",
		"ateam-test:latest claude -p --verbose",
	} {
		if !strings.Contains(got, substr) {
			t.Errorf("DebugCommand missing %q:\n  got: %s", substr, got)
		}
	}
}

func TestPersistentCmdFactoryArgs(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret123")

	dc := &DockerContainer{
		Image:         "ateam-test:latest",
		Persistent:    true,
		ContainerName: "ateam-projects_myapp-security",
		SourceDir:     "/Users/nic/projects/myapp",
		ProjectDir:    "/Users/nic/projects/myapp/.ateam",
		OrgDir:        "/Users/nic/.ateamorg",
		ForwardEnv:    []string{"MY_TOKEN"},
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "-p", "--verbose")
	args := cmd.Args

	// Should be docker exec, not docker run
	if len(args) < 2 || args[1] != "exec" {
		t.Fatalf("expected docker exec, got %v", args)
	}

	hasArg := func(flag, value string) bool {
		for i, a := range args {
			if a == flag && i+1 < len(args) && args[i+1] == value {
				return true
			}
		}
		return false
	}

	// Working dir
	if !hasArg("-w", "/workspace") {
		t.Errorf("missing -w flag, args: %v", args)
	}

	// Env vars: docker exec requires KEY=VALUE
	if !hasArg("-e", "MY_TOKEN=secret123") {
		t.Errorf("missing -e KEY=VALUE, args: %v", args)
	}

	// Container name should appear before the command
	foundName := false
	for i, a := range args {
		if a == "ateam-projects_myapp-security" {
			if i+1 < len(args) && args[i+1] == "claude" {
				foundName = true
			}
			break
		}
	}
	if !foundName {
		t.Errorf("expected container name before command, args: %v", args)
	}

	// Should NOT contain -v mounts (those are set at docker run, not exec)
	for _, a := range args {
		if a == "-v" {
			t.Errorf("persistent CmdFactory should not have -v mounts, args: %v", args)
			break
		}
	}
}

func TestDebugCommandPersistent(t *testing.T) {
	dc := &DockerContainer{
		Image:         "ateam-test:latest",
		Persistent:    true,
		ContainerName: "ateam-projects_myapp-security",
		SourceDir:     "/src",
		OrgDir:        "/org",
		ForwardEnv:    []string{"ANTHROPIC_API_KEY"},
	}

	got := dc.DebugCommand(RunOpts{
		Command: "claude",
		Args:    []string{"-p"},
	})

	want := "docker exec -i -w /workspace -e ANTHROPIC_API_KEY=... ateam-projects_myapp-security claude -p"
	if got != want {
		t.Errorf("DebugCommand persistent:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestContainerPathsSimple(t *testing.T) {
	dc := &DockerContainer{
		SourceDir:  "/Users/nic/projects/myapp",
		ProjectDir: "/Users/nic/projects/myapp/.ateam",
		OrgDir:     "/Users/nic/.ateamorg",
	}

	codePath, workDir, orgPath := dc.containerPaths()

	if codePath != "/workspace" {
		t.Errorf("codePath = %q, want /workspace", codePath)
	}
	if workDir != "/workspace" {
		t.Errorf("workDir = %q, want /workspace", workDir)
	}
	if orgPath != "/.ateamorg" {
		t.Errorf("orgPath = %q, want /.ateamorg", orgPath)
	}
}

func TestContainerPathsWithGitRoot(t *testing.T) {
	dc := &DockerContainer{
		MountDir:   "/home/user/repo",
		SourceDir:  "/home/user/repo/myapp",
		ProjectDir: "/home/user/repo/myapp/.ateam",
		OrgDir:     "/home/user/.ateamorg",
	}

	codePath, workDir, orgPath := dc.containerPaths()

	if codePath != "/workspace" {
		t.Errorf("codePath = %q, want /workspace", codePath)
	}
	if workDir != "/workspace/myapp" {
		t.Errorf("workDir = %q, want /workspace/myapp", workDir)
	}
	if orgPath != "/.ateamorg" {
		t.Errorf("orgPath = %q, want /.ateamorg", orgPath)
	}
}
