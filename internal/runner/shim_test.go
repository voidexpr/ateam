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

	req, err := r.buildRequest("p", TemplateVars{}, "/tmp/cwd", "agent.log", "stderr.log", nil, 1)
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

// TestBuildRequestSkipsShimWhenEmpty: empty ShimDir means no PATH
// override — the agent inherits the parent's PATH unchanged.
func TestBuildRequestSkipsShimWhenEmpty(t *testing.T) {
	r := &AgentExecutor{ShimDir: ""}

	req, err := r.buildRequest("p", TemplateVars{}, "/tmp/cwd", "agent.log", "stderr.log", nil, 1)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if _, ok := req.Env["PATH"]; ok {
		t.Errorf("expected no PATH override when ShimDir is empty; got %q", req.Env["PATH"])
	}
}
