package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindOrg(t *testing.T) {
	tmp := resolvedTempDir(t)
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

	t.Run("prefers nearest org from project path", func(t *testing.T) {
		projectRoot := filepath.Join(tmp, "myproj")
		localOrgDir := filepath.Join(projectRoot, ".ateamorg")
		if err := os.MkdirAll(filepath.Join(localOrgDir, "defaults"), 0755); err != nil {
			t.Fatal(err)
		}

		got, err := FindOrg(projectRoot)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != localOrgDir {
			t.Errorf("got %q, want %q", got, localOrgDir)
		}
	})
}

func TestFindProject(t *testing.T) {
	tmp := resolvedTempDir(t)
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

func TestResolveStreamPath(t *testing.T) {
	tests := []struct {
		name       string
		projectDir string
		orgDir     string
		sf         string
		want       string
	}{
		{
			name:       "empty string",
			projectDir: "/home/user/myproject/.ateam",
			orgDir:     "/home/user/.ateamorg",
			sf:         "",
			want:       "",
		},
		{
			name:       "absolute path returned as-is",
			projectDir: "/home/user/myproject/.ateam",
			orgDir:     "/home/user/.ateamorg",
			sf:         "/var/log/stream.jsonl",
			want:       "/var/log/stream.jsonl",
		},
		{
			name:       "legacy projects/ prefix resolves to orgDir",
			projectDir: "/home/user/myproject/.ateam",
			orgDir:     "/home/user/.ateamorg",
			sf:         "projects/myproject/roles/security/logs/stream.jsonl",
			want:       filepath.Join("/home/user/.ateamorg", "projects/myproject/roles/security/logs/stream.jsonl"),
		},
		{
			name:       "new relative path resolves to projectDir",
			projectDir: "/home/user/myproject/.ateam",
			orgDir:     "/home/user/.ateamorg",
			sf:         "logs/roles/security/2026-03-18_stream.jsonl",
			want:       filepath.Join("/home/user/myproject/.ateam", "logs/roles/security/2026-03-18_stream.jsonl"),
		},
		{
			name:       "legacy prefix but empty orgDir falls back to projectDir",
			projectDir: "/home/user/myproject/.ateam",
			orgDir:     "",
			sf:         "projects/myproject/roles/security/logs/stream.jsonl",
			want:       filepath.Join("/home/user/myproject/.ateam", "projects/myproject/roles/security/logs/stream.jsonl"),
		},
		{
			name:       "empty projectDir falls back to orgDir",
			projectDir: "",
			orgDir:     "/home/user/.ateamorg",
			sf:         "logs/stream.jsonl",
			want:       filepath.Join("/home/user/.ateamorg", "logs/stream.jsonl"),
		},
		{
			name:       "both empty returns sf as-is",
			projectDir: "",
			orgDir:     "",
			sf:         "logs/stream.jsonl",
			want:       "logs/stream.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStreamPath(tt.projectDir, tt.orgDir, tt.sf)
			if got != tt.want {
				t.Errorf("ResolveStreamPath(%q, %q, %q) = %q, want %q", tt.projectDir, tt.orgDir, tt.sf, got, tt.want)
			}
		})
	}
}

func TestFindOrgNotFound(t *testing.T) {
	tmp := resolvedTempDir(t)
	_, err := FindOrg(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectNotFound(t *testing.T) {
	tmp := resolvedTempDir(t)
	_, err := FindProject(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
