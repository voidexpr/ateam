package assembler_test

import (
	"strings"
	"testing"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/prompts/assembler"
)

// TestDefaultsReachableViaAssembler is an integration smoke test: the v1
// prompt tree shipped in defaults/prompts/ must be reachable through the
// standard anchor chain. Catches embed-glob drift early, before the cmd/*
// rewires arrive.
func TestDefaultsReachableViaAssembler(t *testing.T) {
	a := assembler.New(assembler.BuildAnchors("", "", defaults.FS))

	// Root-level framing.
	for _, p := range []string{
		"_pre.context.md",
		"report/_pre.intro.md",
		"report/_post.format.md",
		"review.prompt.md",
		"code.prompt.md",
		"code_management.prompt.md",
		"code_verify.prompt.md",
		"auto_setup.prompt.md",
		"exec_debug.prompt.md",
		"report_auto_roles.prompt.md",
	} {
		m, ok, err := a.FirstMatch(p)
		if err != nil {
			t.Errorf("FirstMatch(%s) err: %v", p, err)
			continue
		}
		if !ok {
			t.Errorf("FirstMatch(%s): not found", p)
		}
		if ok && len(m.Content) == 0 {
			t.Errorf("FirstMatch(%s): empty content", p)
		}
	}

	// Report roles: at least one well-known role should resolve.
	for _, role := range []string{"security", "dependencies", "code.bugs"} {
		path := "report/" + role + ".prompt.md"
		if _, ok, err := a.FirstMatch(path); err != nil || !ok {
			t.Errorf("FirstMatch(%s): ok=%v err=%v", path, ok, err)
		}
	}

	// AllMatches picks up the dir-level pre under report/.
	matches, err := a.AllMatches("report/_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < 1 {
		t.Errorf("report _pre.*.md AllMatches: got %d, want >=1", len(matches))
	}
	// Verify _pre.context.md uses the dynamic that supersedes the old
	// {{project.info}} static variable.
	root, ok, _ := a.FirstMatch("_pre.context.md")
	if !ok || !strings.Contains(string(root.Content), "{{dynamic.project_info}}") {
		t.Errorf("_pre.context.md should reference {{dynamic.project_info}}, got %q", string(root.Content))
	}
}

// stubDynamicDispatcher returns canned content for every dynamic name,
// keyed by name. Used by defaults smoke tests to stand in for the verb
// factories' real dispatcher.
type stubDynamicDispatcher map[string]string

func (s stubDynamicDispatcher) Dispatch(name string, _ []string) (string, error) {
	if v, ok := s[name]; ok {
		return v, nil
	}
	return "", nil
}

// TestAssembleAgainstRealDefaults exercises the full assembly pipeline
// against the shipped defaults — proves the engine + anchors + Assemble +
// embedded prompts hang together end-to-end before any cmd/ rewires arrive.
func TestAssembleAgainstRealDefaults(t *testing.T) {
	a := assembler.New(assembler.BuildAnchors("", "", defaults.FS))
	vars := assembler.MapVars{
		Prompt:  map[string]string{"name": "security", "path": "report/security", "action": "report"},
		Exec:    map[string]string{"output_file": "/tmp/report.md", "output_dir": "/tmp"},
		Project: map[string]string{"name": "ateam"},
	}
	// project_info moved from a static vars entry to a dynamic; supply a
	// stub dispatcher so the smoke test can assert the expanded output.
	engine := assembler.NewEngine(a, 0).WithDispatcher(stubDynamicDispatcher{
		"project_info": "# Test project info",
	})

	res, err := a.Assemble("report/security", vars, engine, nil)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if !strings.Contains(res.Prompt, "# Test project info") {
		t.Error("expected {{dynamic.project_info}} expansion in output")
	}
	if !strings.Contains(res.Prompt, "performing the security report") {
		t.Error("expected report/_pre.intro.md expansion with role name")
	}
	// Report Format / Output Validation lives in _post.format.md after the
	// base-prompt split.
	if !strings.Contains(res.Prompt, "Report Format") {
		t.Error("expected report/_post.format.md content (Report Format header)")
	}
	if !strings.Contains(res.Prompt, "Output Validation Gate") {
		t.Error("expected report/_post.format.md content (Output Validation Gate)")
	}
	// Sections should include at least: root_pre, dir_pre:report, role_main,
	// dir_post:report.
	slots := make(map[string]int)
	for _, s := range res.Sections {
		slots[s.Slot]++
	}
	if slots["root_pre"] == 0 {
		t.Error("missing root_pre slot")
	}
	if slots["dir_pre:report"] == 0 {
		t.Errorf("dir_pre:report count = %d, want >=1", slots["dir_pre:report"])
	}
	if slots["role_main"] != 1 {
		t.Errorf("role_main count = %d, want 1", slots["role_main"])
	}
	if slots["dir_post:report"] == 0 {
		t.Errorf("dir_post:report count = %d, want >=1", slots["dir_post:report"])
	}
}
