package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindOrg(t *testing.T) {
	tmp := t.TempDir()
	orgDir := filepath.Join(tmp, ".ateamorg")
	if err := os.MkdirAll(filepath.Join(orgDir, "defaults"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Run("from parent", func(t *testing.T) {
		got, err := FindOrg(tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != orgDir {
			t.Errorf("got %q, want %q", got, orgDir)
		}
	})

	t.Run("from child dir", func(t *testing.T) {
		child := filepath.Join(tmp, "some", "child")
		if err := os.MkdirAll(child, 0755); err != nil {
			t.Fatal(err)
		}
		got, err := FindOrg(child)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != orgDir {
			t.Errorf("got %q, want %q", got, orgDir)
		}
	})

	t.Run("from inside .ateamorg", func(t *testing.T) {
		got, err := FindOrg(orgDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != orgDir {
			t.Errorf("got %q, want %q", got, orgDir)
		}
	})

	t.Run("from inside .ateamorg/defaults", func(t *testing.T) {
		defaultsDir := filepath.Join(orgDir, "defaults")
		got, err := FindOrg(defaultsDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != orgDir {
			t.Errorf("got %q, want %q", got, orgDir)
		}
	})
}

func TestFindProject(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a minimal config.toml
	configContent := "[project]\nname = \"test\"\n"
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("from project parent", func(t *testing.T) {
		got, err := FindProject(tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != projectDir {
			t.Errorf("got %q, want %q", got, projectDir)
		}
	})

	t.Run("from child of project parent", func(t *testing.T) {
		child := filepath.Join(tmp, "src", "pkg")
		if err := os.MkdirAll(child, 0755); err != nil {
			t.Fatal(err)
		}
		got, err := FindProject(child)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != projectDir {
			t.Errorf("got %q, want %q", got, projectDir)
		}
	})

	t.Run("from inside .ateam", func(t *testing.T) {
		got, err := FindProject(projectDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != projectDir {
			t.Errorf("got %q, want %q", got, projectDir)
		}
	})
}

func TestFindOrgNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := FindOrg(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := FindProject(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
