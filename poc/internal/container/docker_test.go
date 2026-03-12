package container

import (
	"context"
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

	// Verify it's a docker command
	if cmd.Path == "" {
		t.Fatal("expected non-empty command path")
	}

	args := cmd.Args
	// args[0] is "docker"
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

	// Check mounts are present
	hasMount := func(mount string) bool {
		for _, a := range args {
			if a == mount {
				return true
			}
		}
		return false
	}
	if !hasMount("/Users/nic/projects/myapp:/workspace:rw") {
		t.Error("missing source mount")
	}
	if !hasMount("/Users/nic/.ateamorg:/.ateamorg:ro") {
		t.Error("missing org mount")
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

	want := "docker run --rm -i -v /src:/workspace:rw -v /org:/.ateamorg:ro -w /workspace -e ANTHROPIC_API_KEY ateam-test:latest claude -p --verbose"
	if got != want {
		t.Errorf("DebugCommand:\n  got:  %s\n  want: %s", got, want)
	}
}
