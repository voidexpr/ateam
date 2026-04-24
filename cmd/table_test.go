package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

func TestOpenProjectDBCreatesProjectDB(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	if _, err := os.Stat(projDBPath); !os.IsNotExist(err) {
		t.Fatalf("project DB should not exist yet, got err=%v", err)
	}

	db, err := openProjectDB(env)
	if err != nil {
		t.Fatalf("openProjectDB: %v", err)
	}
	db.Close()

	if _, err := os.Stat(projDBPath); err != nil {
		t.Fatalf("project DB should have been created: %v", err)
	}
}

func TestOpenProjectDBErrorsWithoutProjectDir(t *testing.T) {
	env := &root.ResolvedEnv{
		ProjectDir: "",
		OrgDir:     "/tmp/some-org",
	}

	_, err := openProjectDB(env)
	if err == nil {
		t.Fatal("expected error when ProjectDir is empty")
	}
}

func TestOpenProjectDBOpensExistingDB(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	projDB, err := calldb.Open(projDBPath)
	if err != nil {
		t.Fatalf("Open project DB: %v", err)
	}
	projDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	db, err := openProjectDB(env)
	if err != nil {
		t.Fatalf("openProjectDB: %v", err)
	}
	db.Close()
}

func TestRequireProjectDBFailsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	_, err := requireProjectDB(env)
	if err == nil {
		t.Fatal("expected error when DB does not exist")
	}
}

func TestRequireProjectDBSucceedsWhenExists(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	projDB, err := calldb.Open(projDBPath)
	if err != nil {
		t.Fatalf("Open project DB: %v", err)
	}
	projDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	db, err := requireProjectDB(env)
	if err != nil {
		t.Fatalf("requireProjectDB: %v", err)
	}
	db.Close()
}

func TestResolveVolumePath(t *testing.T) {
	tmpDir := t.TempDir()
	absInside := filepath.Join(tmpDir, "data") + ":/container"
	cases := []struct {
		name    string
		vol     string
		wantErr bool
	}{
		{
			name:    "relative path within sourceDir",
			vol:     "subdir/file:/container",
			wantErr: false,
		},
		{
			name:    "path traversal escapes boundary",
			vol:     "../../etc/passwd:/container",
			wantErr: true,
		},
		{
			name:    "absolute path inside allowed dir",
			vol:     absInside,
			wantErr: false,
		},
		{
			name:    "absolute path outside allowed dir",
			vol:     "/etc/passwd:/container",
			wantErr: true,
		},
		{
			name:    "single-part spec passes through",
			vol:     "hostpath",
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveVolumePath(tc.vol, tmpDir, tmpDir)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckConcurrentRunsEnv(t *testing.T) {
	// (a) org mode with empty ProjectID → error
	t.Run("OrgModeEmptyProjectID", func(t *testing.T) {
		env := &root.ResolvedEnv{
			OrgDir:    "/some/org/.ateamorg",
			SourceDir: "", // causes ProjectID() == ""
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err == nil {
			t.Fatal("expected error when OrgDir is set but ProjectID is empty")
		}
	})

	// (b) non-org mode with empty ProjectID → no error
	t.Run("NonOrgModeEmptyProjectID", func(t *testing.T) {
		env := &root.ResolvedEnv{
			OrgDir:    "",
			SourceDir: "",
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err != nil {
			t.Fatalf("expected no error when OrgDir is empty, got: %v", err)
		}
	})

	// (c) org mode with valid ProjectID → delegates to checkConcurrentRuns (nil db returns nil)
	t.Run("OrgModeValidProjectID", func(t *testing.T) {
		orgDir := "/some/org/.ateamorg"
		env := &root.ResolvedEnv{
			OrgDir:    orgDir,
			SourceDir: "/some/org/myproject",
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err != nil {
			t.Fatalf("expected no error when ProjectID is valid, got: %v", err)
		}
	})
}
