package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
)

// fakeContainer is a minimal Container that records ApplyAgentEnv calls and
// rewrites paths under a configured host mount to a container mount, so
// setupContainer can be exercised without docker.
type fakeContainer struct {
	hostMount      string
	containerMount string
	appliedEnv     map[string]string
	factoryEnv     map[string]string
}

func (f *fakeContainer) Type() string                                 { return "fake" }
func (f *fakeContainer) Run(context.Context, container.RunOpts) error { return nil }
func (f *fakeContainer) DebugCommand(container.RunOpts) string        { return "" }
func (f *fakeContainer) Prepare(context.Context) error                { return nil }
func (f *fakeContainer) GetContainerName() string                     { return "fake-container" }
func (f *fakeContainer) ResolveTemplates(*strings.Replacer)           {}
func (f *fakeContainer) SetSourceWritable(bool)                       {}
func (f *fakeContainer) SetContainerName(string) bool                 { return false }

func (f *fakeContainer) TranslatePath(p string) string {
	if p == "" || f.hostMount == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, f.hostMount); ok {
		return f.containerMount + rest
	}
	return p
}

func (f *fakeContainer) ApplyContainerExtra([]string, []string, map[string]string) {}

func (f *fakeContainer) ApplyAgentEnv(env map[string]string) {
	if f.appliedEnv == nil {
		f.appliedEnv = make(map[string]string, len(env))
	}
	for k, v := range env {
		f.appliedEnv[k] = v
	}
}

// CmdFactory snapshots whatever Env is currently in appliedEnv, mirroring how
// the real docker factory closes over d.Env: setupContainer must apply req.Env
// BEFORE the factory is taken for it to reach the spawned process.
func (f *fakeContainer) CmdFactory() container.CmdFactory {
	snapshot := make(map[string]string, len(f.appliedEnv))
	for k, v := range f.appliedEnv {
		snapshot[k] = v
	}
	f.factoryEnv = snapshot
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, name, args...)
	}
}

func (f *fakeContainer) Clone() container.Container {
	cp := *f
	cp.appliedEnv = nil
	cp.factoryEnv = nil
	return &cp
}

// TestSetupContainerForwardsRequestEnv covers the regression where per-run
// overrides put on req.Env (e.g. CLAUDE_CONFIG_DIR for an isolated config_dir)
// never reached the container because the agent skips buildProcessEnv once a
// CmdFactory is attached. setupContainer must translate the host paths and
// apply the env onto the container so the factory closure picks it up.
func TestSetupContainerForwardsRequestEnv(t *testing.T) {
	fake := &fakeContainer{
		hostMount:      "/host/proj",
		containerMount: "/workspace",
	}

	hostConfigDir := "/host/proj/.ateam/config"
	req := agent.Request{
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": hostConfigDir,
			"OPAQUE_FLAG":       "value-not-a-path",
		},
	}

	if _, err := setupContainer(context.Background(), fake, &req, "/host/proj"); err != nil {
		t.Fatalf("setupContainer: %v", err)
	}

	// Container env must carry the translated path so the agent inside the
	// container reads a path that exists at the bind-mount target.
	wantConfig := "/workspace/.ateam/config"
	if got := fake.factoryEnv["CLAUDE_CONFIG_DIR"]; got != wantConfig {
		t.Errorf("factory env CLAUDE_CONFIG_DIR = %q, want %q (full env: %v)",
			got, wantConfig, fake.factoryEnv)
	}
	if got := fake.factoryEnv["OPAQUE_FLAG"]; got != "value-not-a-path" {
		t.Errorf("factory env OPAQUE_FLAG = %q, want %q", got, "value-not-a-path")
	}

	// req.Env stays in HOST form so host-side preflight (mkdir, cred lookup,
	// etc.) keeps targeting the bind-mount source, not the container path.
	if got := req.Env["CLAUDE_CONFIG_DIR"]; got != hostConfigDir {
		t.Errorf("req.Env CLAUDE_CONFIG_DIR = %q, want %q (host path)", got, hostConfigDir)
	}

	if req.WorkDir != "/workspace" {
		t.Errorf("req.WorkDir = %q, want /workspace (translated)", req.WorkDir)
	}
}

// TestSetupContainerNoEnvNoOps verifies setupContainer doesn't touch the
// container env when req.Env is empty.
func TestSetupContainerNoEnvNoOps(t *testing.T) {
	fake := &fakeContainer{hostMount: "/host", containerMount: "/c"}
	req := agent.Request{}
	if _, err := setupContainer(context.Background(), fake, &req, "/host/cwd"); err != nil {
		t.Fatalf("setupContainer: %v", err)
	}
	if len(fake.appliedEnv) != 0 {
		t.Errorf("ApplyAgentEnv unexpectedly received: %v", fake.appliedEnv)
	}
}
