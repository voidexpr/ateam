package root

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveTriggersV1MigrationByDefault verifies that Resolve() runs the
// v1-layout migrator on first contact with an old-layout .ateam tree.
func TestResolveTriggersV1MigrationByDefault(t *testing.T) {
	tmp := resolvedTempDir(t)
	projectDir := filepath.Join(tmp, "myproj")
	ateamDir := filepath.Join(projectDir, ".ateam")
	if err := os.MkdirAll(filepath.Join(ateamDir, "supervisor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal config so config.Load succeeds.
	if err := os.WriteFile(filepath.Join(ateamDir, "config.toml"),
		[]byte("[project]\nname = \"myproj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Old-layout file that should migrate.
	if err := os.WriteFile(filepath.Join(ateamDir, "supervisor", "review_prompt.md"),
		[]byte("legacy review body"), 0o644); err != nil {
		t.Fatal(err)
	}

	prevWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prevWd) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	env, err := Resolve("", "")
	if err != nil {
		t.Fatal(err)
	}
	if env.ProjectDir != ateamDir {
		t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, ateamDir)
	}

	// Old path gone, new path present.
	if _, err := os.Stat(filepath.Join(ateamDir, "supervisor", "review_prompt.md")); err == nil {
		t.Error("legacy review_prompt.md should have been moved")
	}
	if _, err := os.Stat(filepath.Join(ateamDir, "prompts", "review.prompt.md")); err != nil {
		t.Errorf("migrated review.prompt.md missing: %v", err)
	}
}

// TestResolveSkipsV1MigrationWhenSuppressed verifies the opt-out gate:
// with ATEAM_NO_MIGRATE=1 set, an old-layout tree is left untouched. Useful
// for one-off recovery or debugging the legacy layout in place.
func TestResolveSkipsV1MigrationWhenSuppressed(t *testing.T) {
	t.Setenv("ATEAM_NO_MIGRATE", "1")
	tmp := resolvedTempDir(t)
	projectDir := filepath.Join(tmp, "myproj")
	ateamDir := filepath.Join(projectDir, ".ateam")
	if err := os.MkdirAll(filepath.Join(ateamDir, "supervisor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ateamDir, "config.toml"),
		[]byte("[project]\nname = \"myproj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ateamDir, "supervisor", "review_prompt.md"),
		[]byte("legacy review body"), 0o644); err != nil {
		t.Fatal(err)
	}

	prevWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prevWd) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve("", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(ateamDir, "supervisor", "review_prompt.md")); err != nil {
		t.Error("with ATEAM_NO_MIGRATE=1, supervisor/review_prompt.md should remain")
	}
	if _, err := os.Stat(filepath.Join(ateamDir, "prompts", "review.prompt.md")); err == nil {
		t.Error("with ATEAM_NO_MIGRATE=1, prompts/review.prompt.md should not be created")
	}
}
