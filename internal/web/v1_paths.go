package web

import (
	"os"
	"path/filepath"
)

// v1 artifact path helpers. The web layer is read-only and needs to render
// artifacts from BOTH the v1 shared/ tree (where the runner now promotes new
// artifacts) and the legacy roles/ / supervisor/ paths (for pre-migration
// projects). Each helper prefers v1 and falls back to legacy on stat.
//
// Once the migrator is default-on and projects have been migrated, the
// legacy fallback can be removed in one pass.

// reviewPath returns the v1 shared/review/review.md if present, else the
// legacy supervisor/review.md.
func reviewPath(projectDir string) string {
	v1 := filepath.Join(projectDir, "shared", "review", "review.md")
	if _, err := os.Stat(v1); err == nil {
		return v1
	}
	return filepath.Join(projectDir, "supervisor", "review.md")
}

// verifyPath returns the v1 shared/verify/verify.md if present, else the
// legacy supervisor/verify.md.
func verifyPath(projectDir string) string {
	v1 := filepath.Join(projectDir, "shared", "verify", "verify.md")
	if _, err := os.Stat(v1); err == nil {
		return v1
	}
	return filepath.Join(projectDir, "supervisor", "verify.md")
}
