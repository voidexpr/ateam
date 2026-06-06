package root

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/config"
)

// projectEnv builds a minimal ResolvedEnv whose HasProject() returns true.
func projectEnv(projectDir string) *ResolvedEnv {
	return &ResolvedEnv{
		ProjectDir: projectDir,
		Config:     &config.Config{},
	}
}

func TestEnsureCLIShim_NoProject(t *testing.T) {
	if got := EnsureCLIShim(nil); got != "" {
		t.Errorf("nil env: got %q, want \"\"", got)
	}
	if got := EnsureCLIShim(&ResolvedEnv{}); got != "" {
		t.Errorf("scratch env (no project): got %q, want \"\"", got)
	}
}

func TestEnsureCLIShim_CreatesSymlink(t *testing.T) {
	projectDir := t.TempDir()
	env := projectEnv(projectDir)

	shimDir := EnsureCLIShim(env)
	if shimDir == "" {
		t.Fatal("expected non-empty shim dir")
	}
	wantDir := filepath.Join(projectDir, "cache", "bin")
	if shimDir != wantDir {
		t.Errorf("shim dir = %q, want %q", shimDir, wantDir)
	}

	link := filepath.Join(shimDir, "ateam")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(%s): %v", link, err)
	}

	exe, _ := os.Executable()
	wantTarget, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", exe, err)
	}
	if target != wantTarget {
		t.Errorf("symlink target = %q, want %q", target, wantTarget)
	}
}

func TestEnsureCLIShim_IdempotentFastPath(t *testing.T) {
	projectDir := t.TempDir()
	env := projectEnv(projectDir)

	if got := EnsureCLIShim(env); got == "" {
		t.Fatal("first call returned empty")
	}
	link := filepath.Join(projectDir, "cache", "bin", "ateam")
	info1, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}

	// Second call should be the fast path — symlink mtime unchanged.
	if got := EnsureCLIShim(env); got == "" {
		t.Fatal("second call returned empty")
	}
	info2, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("symlink was rewritten on idempotent call (mtime changed: %v -> %v)",
			info1.ModTime(), info2.ModTime())
	}
}

func TestEnsureCLIShim_ReplacesStaleSymlink(t *testing.T) {
	projectDir := t.TempDir()
	env := projectEnv(projectDir)
	shimDir := filepath.Join(projectDir, "cache", "bin")
	link := filepath.Join(shimDir, "ateam")

	// Plant a stale symlink pointing at a bogus target.
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/old/ateam", link); err != nil {
		t.Fatal(err)
	}

	if got := EnsureCLIShim(env); got == "" {
		t.Fatal("EnsureCLIShim returned empty for stale-link case")
	}

	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target == "/nonexistent/old/ateam" {
		t.Errorf("stale symlink was not replaced")
	}
}

func TestEnsureCLIShim_ReadOnlyProjectDir_WarnsAndContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem permissions; skip")
	}
	projectDir := t.TempDir()
	if err := os.Chmod(projectDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(projectDir, 0o755) })

	got := EnsureCLIShim(projectEnv(projectDir))
	if got != "" {
		t.Errorf("expected empty shim dir on read-only project, got %q", got)
	}
}
