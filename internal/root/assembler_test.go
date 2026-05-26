package root

import (
	"os"
	"path/filepath"
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

func TestSharedPromptDir(t *testing.T) {
	env := &ResolvedEnv{ProjectDir: "/abs/.ateam"}
	cases := []struct {
		in, want string
	}{
		{"review", "/abs/.ateam/shared/review"},
		{"report/security", "/abs/.ateam/shared/report/security"},
		{"code/refactor_small", "/abs/.ateam/shared/code/refactor_small"},
	}
	for _, c := range cases {
		if got := env.SharedPromptDir(c.in); got != c.want {
			t.Errorf("SharedPromptDir(%q) = %q, want %q", c.in, got, c.want)
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
