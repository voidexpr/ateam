package gitutil

import (
	"os"
	"regexp"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	// Walk up from the test file's working directory to find the repo root.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return dir + "/../.."
}

func TestGetProjectMeta(t *testing.T) {
	meta, err := GetProjectMeta(repoRoot(t))
	if err != nil {
		t.Fatalf("GetProjectMeta: %v", err)
	}

	if meta.CommitHash == "" {
		t.Error("CommitHash is empty")
	}
	if len(meta.CommitHash) != 40 {
		t.Errorf("CommitHash length = %d, want 40", len(meta.CommitHash))
	}
	if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, meta.CommitHash); !matched {
		t.Errorf("CommitHash %q is not a valid hex SHA", meta.CommitHash)
	}

	if meta.CommitMessage == "" {
		t.Error("CommitMessage is empty")
	}

	if meta.CommitDate == "" {
		t.Error("CommitDate is empty")
	}
	// Timestamp should be formatted as YYYY-MM-DD_HH-MM-SS
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`, meta.CommitDate); !matched {
		t.Errorf("CommitDate %q does not match expected format YYYY-MM-DD_HH-MM-SS", meta.CommitDate)
	}
}

func TestGetProjectMetaInvalidDir(t *testing.T) {
	_, err := GetProjectMeta("/nonexistent-dir-that-should-not-exist")
	if err == nil {
		t.Error("expected error for invalid directory, got nil")
	}
}
