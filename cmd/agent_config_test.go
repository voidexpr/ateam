package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateLocalPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot get home dir: %v", err)
	}

	t.Run("home dir rejected", func(t *testing.T) {
		if err := validateLocalPath(home); err == nil {
			t.Error("expected error for home directory, got nil")
		}
	})

	t.Run("dot-claude rejected", func(t *testing.T) {
		claudeDir := filepath.Join(home, ".claude")
		if err := validateLocalPath(claudeDir); err == nil {
			t.Error("expected error for ~/.claude, got nil")
		}
	})

	t.Run("ordinary project path accepted", func(t *testing.T) {
		dir := t.TempDir()
		if err := validateLocalPath(dir); err != nil {
			t.Errorf("expected nil for ordinary path, got: %v", err)
		}
	})

	t.Run("symlink to home rejected", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "homelink")
		if err := os.Symlink(home, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := validateLocalPath(link); err == nil {
			t.Error("expected error for symlink pointing to home, got nil")
		}
	})
}
