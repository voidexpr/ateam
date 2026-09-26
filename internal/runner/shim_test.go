package runner

import (
	"os"
	"strings"
	"testing"
)

// TestBuildRequestPrependsShimDirToPATH locks in the audit-trail
// guarantee: when AgentExecutor.ShimDir is set on host execution, the
// request env carries a PATH starting with ShimDir. SpecifiedEnv is
// derived from req.Env, so this is what cmd.md will record.
func TestBuildRequestPrependsShimDirToPATH(t *testing.T) {
	r := &AgentExecutor{ShimDir: "/tmp/shim/bin"}

	req, err := r.buildRequest("p", TemplateVars{}, "/tmp/cwd", "agent.log", "stderr.log", nil, 1, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	got, ok := req.Env["PATH"]
	if !ok {
		t.Fatalf("req.Env missing PATH; got %v", req.Env)
	}
	wantPrefix := "/tmp/shim/bin" + string(os.PathListSeparator)
	if !strings.HasPrefix(got, wantPrefix) && got != "/tmp/shim/bin" {
		t.Errorf("PATH does not start with shim dir.\n  got:    %q\n  prefix: %q", got, wantPrefix)
	}
}

// TestBuildRequestCarriesExtraEnv: RunOpts.AgentEnv reaches the request
// env alongside the runner's own entries (PATH shim here).
func TestBuildRequestCarriesExtraEnv(t *testing.T) {
	r := &AgentExecutor{ShimDir: "/tmp/shim/bin"}

	req, err := r.buildRequest("p", TemplateVars{}, "/tmp/cwd", "agent.log", "stderr.log", nil, 1,
		map[string]string{"BASH_MAX_TIMEOUT_MS": "4200000"})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := req.Env["BASH_MAX_TIMEOUT_MS"]; got != "4200000" {
		t.Errorf("BASH_MAX_TIMEOUT_MS = %q, want 4200000", got)
	}
	if _, ok := req.Env["PATH"]; !ok {
		t.Errorf("extra env dropped the PATH shim: %v", req.Env)
	}
}

// TestBuildRequestSkipsShimWhenEmpty: empty ShimDir means no PATH
// override — the agent inherits the parent's PATH unchanged.
func TestBuildRequestSkipsShimWhenEmpty(t *testing.T) {
	r := &AgentExecutor{ShimDir: ""}

	req, err := r.buildRequest("p", TemplateVars{}, "/tmp/cwd", "agent.log", "stderr.log", nil, 1, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if _, ok := req.Env["PATH"]; ok {
		t.Errorf("expected no PATH override when ShimDir is empty; got %q", req.Env["PATH"])
	}
}
