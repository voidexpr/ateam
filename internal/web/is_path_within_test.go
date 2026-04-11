package web

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsPathWithinBasicCases(t *testing.T) {
	tests := []struct {
		name    string
		absPath string
		baseDir string
		want    bool
	}{
		{"child of base", "/home/user/project/file.txt", "/home/user/project", true},
		{"deep child", "/home/user/project/a/b/c/file.txt", "/home/user/project", true},
		{"exact match (not within)", "/home/user/project", "/home/user/project", false},
		{"sibling dir", "/home/user/other/file.txt", "/home/user/project", false},
		{"parent of base", "/home/user/file.txt", "/home/user/project", false},
		{"empty base", "/home/user/file.txt", "", false},
		{"empty path", "", "/home/user/project", false},
		{"both empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPathWithin(tt.absPath, tt.baseDir)
			if got != tt.want {
				t.Errorf("isPathWithin(%q, %q) = %v, want %v", tt.absPath, tt.baseDir, got, tt.want)
			}
		})
	}
}

func TestIsPathWithinTraversalAttempts(t *testing.T) {
	tests := []struct {
		name    string
		absPath string
		baseDir string
		want    bool
	}{
		{"dot-dot escape", "/home/user/project/../other/file.txt", "/home/user/project", false},
		{"dot-dot to parent", "/home/user/project/../file.txt", "/home/user/project", false},
		{"double dot-dot", "/home/user/project/../../etc/passwd", "/home/user/project", false},
		{"dot-dot in middle resolves inside", "/home/user/project/a/../b/file.txt", "/home/user/project", true},
		{"dot-dot back to base (exact, not within)", "/home/user/project/a/..", "/home/user/project", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPathWithin(tt.absPath, tt.baseDir)
			if got != tt.want {
				t.Errorf("isPathWithin(%q, %q) = %v, want %v", tt.absPath, tt.baseDir, got, tt.want)
			}
		})
	}
}

func TestIsPathWithinPrefixConfusion(t *testing.T) {
	// "/home/user/project-evil/file.txt" should NOT be within "/home/user/project"
	// This is the classic prefix attack.
	got := isPathWithin("/home/user/project-evil/file.txt", "/home/user/project")
	if got {
		t.Error("isPathWithin should not match prefix-overlapping paths")
	}

	got = isPathWithin("/home/user/projects/file.txt", "/home/user/project")
	if got {
		t.Error("isPathWithin should not match when path is a prefix extension")
	}
}

func TestIsPathWithinTrailingSlash(t *testing.T) {
	// Base dir with trailing slash should still work.
	got := isPathWithin("/home/user/project/file.txt", "/home/user/project/")
	if !got {
		t.Error("expected true when baseDir has trailing slash")
	}
}

func TestIsPathWithinDotInBase(t *testing.T) {
	// Base with dots should be cleaned.
	got := isPathWithin("/home/user/project/file.txt", "/home/user/./project")
	if !got {
		t.Error("expected true when baseDir contains dot segment")
	}
}

func TestIsPathWithinSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not reliable on Windows")
	}

	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "sub", "file.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(base, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// isPathWithin uses filepath.Clean, not EvalSymlinks.
	// The link path won't resolve to under the real path — this tests the
	// function's actual behavior (string-based after Clean).
	linkFile := filepath.Join(linkDir, "sub", "file.txt")

	// File under link is within link dir.
	if !isPathWithin(linkFile, linkDir) {
		t.Error("expected file under symlinked dir to be within that dir")
	}

	// File under link is NOT within real dir (paths differ after Clean).
	if isPathWithin(linkFile, realDir) {
		t.Error("expected symlinked path not to be within real dir (no EvalSymlinks)")
	}
}

func TestIsPathWithinRootDir(t *testing.T) {
	// filepath.Clean("/") + "/" = "//" — so nothing starts with "//".
	// This is a known edge case: isPathWithin doesn't handle root as baseDir.
	// In practice baseDir is always a project/org directory, never "/".
	got := isPathWithin("/etc/passwd", "/")
	if got {
		t.Error("expected isPathWithin to return false for root baseDir (known edge case)")
	}
}
