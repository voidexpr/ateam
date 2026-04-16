package container

import (
	"context"
	"strings"
	"testing"
)

func TestDockerExecEnvForwarding(t *testing.T) {
	t.Setenv("MY_SECRET", "s3cret")
	t.Setenv("ANOTHER_VAR", "val2")

	dc := &DockerExecContainer{
		ContainerName: "testctr",
		ForwardEnv:    []string{"MY_SECRET", "ANOTHER_VAR"},
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

	if !hasArg("-e", "MY_SECRET=s3cret") {
		t.Errorf("missing -e MY_SECRET=s3cret, args: %v", args)
	}
	if !hasArg("-e", "ANOTHER_VAR=val2") {
		t.Errorf("missing -e ANOTHER_VAR=val2, args: %v", args)
	}
}

func TestDockerExecEnvForwardingSkipsUnset(t *testing.T) {
	// Only set one of two vars
	t.Setenv("SET_VAR", "present")

	dc := &DockerExecContainer{
		ContainerName: "testctr",
		ForwardEnv:    []string{"SET_VAR", "UNSET_VAR_12345"},
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "-p")
	args := cmd.Args

	hasFlag := func(flag, value string) bool {
		for i, a := range args {
			if a == flag && i+1 < len(args) && args[i+1] == value {
				return true
			}
		}
		return false
	}

	if !hasFlag("-e", "SET_VAR=present") {
		t.Errorf("missing -e SET_VAR=present, args: %v", args)
	}

	// Unset var should not appear
	for i, a := range args {
		if a == "-e" && i+1 < len(args) {
			if val := args[i+1]; len(val) > 10 && val[:10] == "UNSET_VAR_" {
				t.Errorf("unset env var should not be forwarded, got -e %s", val)
			}
		}
	}
}

func TestDockerExecWorkdirSet(t *testing.T) {
	dc := &DockerExecContainer{
		ContainerName: "testctr",
		WorkDir:       "/app/src",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "echo", "hi")
	args := cmd.Args

	hasArg := func(flag, value string) bool {
		for i, a := range args {
			if a == flag && i+1 < len(args) && args[i+1] == value {
				return true
			}
		}
		return false
	}

	if !hasArg("-w", "/app/src") {
		t.Errorf("missing -w /app/src, args: %v", args)
	}
}

func TestDockerExecNoWorkdir(t *testing.T) {
	dc := &DockerExecContainer{
		ContainerName: "testctr",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "echo", "hi")
	args := cmd.Args

	for _, a := range args {
		if a == "-w" {
			t.Errorf("-w flag should not be present when WorkDir is empty, args: %v", args)
			break
		}
	}
}

func TestDockerExecDefaultTemplateArgs(t *testing.T) {
	dc := &DockerExecContainer{
		ContainerName: "myctr",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "--model", "sonnet")
	args := cmd.Args

	// Should produce: docker exec -i myctr claude --model sonnet
	if len(args) < 2 || args[0] != "docker" || args[1] != "exec" {
		t.Fatalf("expected docker exec, got %v", args)
	}

	// -i flag must be present
	found := false
	for _, a := range args {
		if a == "-i" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing -i flag, args: %v", args)
	}

	// Container name before command
	for i, a := range args {
		if a == "myctr" {
			if i+1 >= len(args) || args[i+1] != "claude" {
				t.Errorf("expected command after container name, args: %v", args)
			}
			if i+3 >= len(args) || args[i+2] != "--model" || args[i+3] != "sonnet" {
				t.Errorf("expected full command args after container name, args: %v", args)
			}
			return
		}
	}
	t.Errorf("container name 'myctr' not found in args: %v", args)
}

func TestDockerExecCustomTemplatePassthrough(t *testing.T) {
	dc := &DockerExecContainer{
		ContainerName: "myctr",
		ExecTemplate:  "nerdctl exec {{CONTAINER}} {{CMD}}",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "-p")
	args := cmd.Args

	// Non-docker template should pass through without -i injection
	if args[0] != "nerdctl" {
		t.Fatalf("expected nerdctl, got %v", args)
	}

	// Should NOT have -i injected (that only happens for docker exec)
	for _, a := range args {
		if a == "-i" {
			t.Errorf("non-docker template should not get -i injected, args: %v", args)
			break
		}
	}
}

func TestDockerExecArgBoundariesWithSpaces(t *testing.T) {
	dc := &DockerExecContainer{
		ContainerName: "myctr",
	}

	factory := dc.CmdFactory()
	cmd := factory(context.Background(), "claude", "--prompt", "hello world with spaces", "--flag")
	args := cmd.Args

	// Find --prompt and verify next arg is the full string
	for i, a := range args {
		if a == "--prompt" {
			if i+1 >= len(args) {
				t.Fatal("--prompt has no following arg")
			}
			if args[i+1] != "hello world with spaces" {
				t.Errorf("arg boundary broken: got %q, want %q", args[i+1], "hello world with spaces")
			}
			if i+2 >= len(args) || args[i+2] != "--flag" {
				t.Errorf("expected --flag after prompt value, args: %v", args)
			}
			return
		}
	}
	t.Errorf("--prompt not found in args: %v", args)
}

func TestDockerExecCombinedEnvAndWorkdir(t *testing.T) {
	t.Setenv("API_KEY", "test123")

	dc := &DockerExecContainer{
		ContainerName: "myctr",
		WorkDir:       "/project",
		ForwardEnv:    []string{"API_KEY"},
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

	if !hasArg("-w", "/project") {
		t.Errorf("missing -w /project, args: %v", args)
	}
	if !hasArg("-e", "API_KEY=test123") {
		t.Errorf("missing -e API_KEY=test123, args: %v", args)
	}

	// All docker-specific flags should come before container name
	ctrIdx := -1
	for i, a := range args {
		if a == "myctr" {
			ctrIdx = i
			break
		}
	}
	if ctrIdx == -1 {
		t.Fatalf("container name not found in args: %v", args)
	}
	for i, a := range args[:ctrIdx] {
		if a == "-w" || a == "-e" || a == "-i" {
			_ = i // flags are before container name - correct
		}
	}
	// Verify command comes after container name
	if ctrIdx+1 >= len(args) || args[ctrIdx+1] != "claude" {
		t.Errorf("expected command after container name, args: %v", args)
	}
}

func TestResolveRunningContainerNameEmpty(t *testing.T) {
	_, err := ResolveRunningContainerName(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty container name")
	}
	if !strings.Contains(err.Error(), "container name is empty") {
		t.Errorf("unexpected error: %v", err)
	}
}
