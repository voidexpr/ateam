package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkdirsAndFiles writes files under root, creating parent dirs as needed.
func mkdirsAndFiles(t *testing.T, root string, files map[string]string) error {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func TestSharedDir(t *testing.T) {
	env := &ResolvedEnv{ProjectDir: "/abs/.ateam"}
	if got := env.SharedDir(); got != "/abs/.ateam/shared" {
		t.Errorf("SharedDir = %q", got)
	}
}

func TestFlatSharedPaths(t *testing.T) {
	env := &ResolvedEnv{ProjectDir: "/abs/.ateam"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"RoleReportPath", env.RoleReportPath("security"), "/abs/.ateam/shared/report/security.md"},
		{"RoleReportPath dotted role", env.RoleReportPath("project.security"), "/abs/.ateam/shared/report/project.security.md"},
		{"ReviewPath", env.ReviewPath(), "/abs/.ateam/shared/review.md"},
		{"VerifyPath", env.VerifyPath(), "/abs/.ateam/shared/verify.md"},
		{"AutoSetupPath", env.AutoSetupPath(), "/abs/.ateam/shared/auto_setup.md"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestEnvAssemblerResolvesDefaults(t *testing.T) {
	// Empty project + org: only embedded anchor is populated. Assembler
	// should still resolve a known default like review.prompt.md.
	env := &ResolvedEnv{}
	a := env.Assembler()
	if a == nil {
		t.Fatal("Assembler() returned nil")
	}
	if _, ok, err := a.FirstMatch("review.prompt.md"); err != nil || !ok {
		t.Errorf("FirstMatch(review.prompt.md): ok=%v err=%v", ok, err)
	}
}

// TestBuildAssemblerVarsDefersOutputPaths locks in the contract that the
// assembler must not resolve {{OUTPUT_DIR}} / {{OUTPUT_FILE}} — those depend on
// the exec ID and are filled later by the runner. Resolving them to "" at
// assembly time would strip the placeholders and leave agents with a blank
// destination block.
func TestBuildAssemblerVarsDefersOutputPaths(t *testing.T) {
	env := &ResolvedEnv{}
	vars := env.BuildAssemblerVars("report/security", "", "report")
	if got := vars.Exec["output_dir"]; got != "{{OUTPUT_DIR}}" {
		t.Errorf("exec.output_dir = %q, want {{OUTPUT_DIR}}", got)
	}
	if got := vars.Exec["output_file"]; got != "{{OUTPUT_FILE}}" {
		t.Errorf("exec.output_file = %q, want {{OUTPUT_FILE}}", got)
	}

	// End-to-end: assembling the shipped default report prompt must keep the
	// placeholder intact so runner.ResolveTemplateString can fill it.
	vars.Project["info"] = "INFO" // avoid forking git for project info
	res, err := env.Assembler().Assemble("report/security", vars, nil, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(res.Prompt, "{{OUTPUT_FILE}}") {
		t.Error("assembled report prompt dropped {{OUTPUT_FILE}} placeholder")
	}
}

func TestEnvAssemblerProjectOverride(t *testing.T) {
	tmp := resolvedTempDir(t)
	projectDir := filepath.Join(tmp, ".ateam")
	if err := mkdirsAndFiles(t, projectDir, map[string]string{
		"prompts/review.prompt.md": "PROJECT REVIEW",
	}); err != nil {
		t.Fatal(err)
	}
	env := &ResolvedEnv{ProjectDir: projectDir}
	a := env.Assembler()
	m, ok, err := a.FirstMatch("review.prompt.md")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if m.Anchor != "project" || string(m.Content) != "PROJECT REVIEW" {
		t.Errorf("project anchor not winning: %+v", m)
	}
}
