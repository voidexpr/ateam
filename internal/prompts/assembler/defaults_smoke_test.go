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
		"code_management.prompt.md",
		"code_verify.prompt.md",
		"auto_setup.prompt.md",
		"exec_debug.prompt.md",
		"report_auto_roles.prompt.md",
		"report/_pre.base.md",
		"code/_pre.base.md",
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

	// AllMatches picks up multiple dir-level pres under report/.
	matches, err := a.AllMatches("report/_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < 2 {
		t.Errorf("report _pre.*.md AllMatches: got %d, want >=2", len(matches))
	}
	// Verify _pre.context.md uses the new dotted variable.
	root, ok, _ := a.FirstMatch("_pre.context.md")
	if !ok || !strings.Contains(string(root.Content), "{{project.info}}") {
		t.Errorf("_pre.context.md should reference {{project.info}}, got %q", string(root.Content))
	}
}
