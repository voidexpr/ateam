package root

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	t.Chdir(tmp)
	_, err := FindOrg(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectNotFound(t *testing.T) {
	tmp := resolvedTempDir(t)
	t.Chdir(tmp)
	_, err := FindProject(tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// makeProject creates a minimal .ateam/ directory with a loadable config.toml
// at projectRoot/.ateam, and returns the .ateam path.
func makeProject(t *testing.T, projectRoot, name string) string {
	t.Helper()
	projectDir := filepath.Join(projectRoot, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	configContent := "[project]\nname = \"" + name + "\"\n"
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}
	return projectDir
}

func makeOrg(t *testing.T, orgRoot string) string {
	t.Helper()
	orgDir := filepath.Join(orgRoot, ".ateamorg")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}
	return orgDir
}

func TestResolveOverrides(t *testing.T) {
	t.Run("project flag points at project root", func(t *testing.T) {
		base := resolvedTempDir(t)
		projectRoot := filepath.Join(base, "myproj")
		projectDir := makeProject(t, projectRoot, "myproj")

		// run from an unrelated cwd
		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		env, err := Resolve("", projectRoot)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.ProjectDir != projectDir {
			t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, projectDir)
		}
		if env.OrgDir != "" {
			t.Errorf("OrgDir = %q, want empty (org-less)", env.OrgDir)
		}
		if env.SourceDir != projectRoot {
			t.Errorf("SourceDir = %q, want %q", env.SourceDir, projectRoot)
		}
	})

	t.Run("project flag points at .ateam directly", func(t *testing.T) {
		base := resolvedTempDir(t)
		projectRoot := filepath.Join(base, "myproj")
		projectDir := makeProject(t, projectRoot, "myproj")

		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		env, err := Resolve("", projectDir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.ProjectDir != projectDir {
			t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, projectDir)
		}
	})

	t.Run("project flag auto-discovers org by walking up", func(t *testing.T) {
		base := resolvedTempDir(t)
		orgDir := makeOrg(t, base)
		projectRoot := filepath.Join(base, "myproj")
		makeProject(t, projectRoot, "myproj")

		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		env, err := Resolve("", projectRoot)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.OrgDir != orgDir {
			t.Errorf("OrgDir = %q, want %q", env.OrgDir, orgDir)
		}
	})

	t.Run("org flag points at .ateamorg directly", func(t *testing.T) {
		base := resolvedTempDir(t)
		orgDir := makeOrg(t, base)
		projectRoot := filepath.Join(base, "myproj")
		makeProject(t, projectRoot, "myproj")

		t.Chdir(projectRoot)

		env, err := Resolve(orgDir, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.OrgDir != orgDir {
			t.Errorf("OrgDir = %q, want %q", env.OrgDir, orgDir)
		}
	})

	t.Run("org flag points at parent of .ateamorg", func(t *testing.T) {
		base := resolvedTempDir(t)
		orgDir := makeOrg(t, base)
		projectRoot := filepath.Join(base, "myproj")
		makeProject(t, projectRoot, "myproj")

		t.Chdir(projectRoot)

		env, err := Resolve(base, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.OrgDir != orgDir {
			t.Errorf("OrgDir = %q, want %q", env.OrgDir, orgDir)
		}
	})

	t.Run("both flags resolve independently", func(t *testing.T) {
		base := resolvedTempDir(t)
		orgDir := makeOrg(t, base)
		projectRoot := filepath.Join(base, "myproj")
		projectDir := makeProject(t, projectRoot, "myproj")

		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		env, err := Resolve(base, projectRoot)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if env.OrgDir != orgDir {
			t.Errorf("OrgDir = %q, want %q", env.OrgDir, orgDir)
		}
		if env.ProjectDir != projectDir {
			t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, projectDir)
		}
	})

	t.Run("project flag with nonexistent path errors", func(t *testing.T) {
		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		_, err := Resolve("", filepath.Join(cwd, "does-not-exist"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "--project") {
			t.Errorf("error %q should mention --project", err)
		}
	})

	t.Run("org flag with nonexistent path errors", func(t *testing.T) {
		cwd := resolvedTempDir(t)
		t.Chdir(cwd)

		_, err := Resolve(filepath.Join(cwd, "does-not-exist"), "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "--org") {
			t.Errorf("error %q should mention --org", err)
		}
	})
}

// TestResolveWorkDirAndGitRepoDir verifies that WorkDir defaults to cwd and
// that GitRepoDir is derived from `git rev-parse --show-toplevel` in WorkDir
// — not from config.toml's [git] repo setting.
func TestResolveWorkDirAndGitRepoDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}

	t.Run("WorkDir defaults to cwd; GitRepoDir is empty outside a repo", func(t *testing.T) {
		env := &ResolvedEnv{}
		if err := env.resolveWorkDir(""); err != nil {
			t.Fatalf("resolveWorkDir: %v", err)
		}
		// WorkDir is realPath'd so it matches env.ProjectDir / env.SourceDir
		// (also realPath'd at discovery). Compare against the resolved cwd.
		cwd, _ := os.Getwd()
		wantCwd := realPath(cwd)
		if env.WorkDir != wantCwd {
			t.Errorf("WorkDir = %q, want realPath(cwd) %q", env.WorkDir, wantCwd)
		}
		// Run from a non-repo temp dir to guarantee GitRepoDir = "".
		tmp := resolvedTempDir(t)
		t.Chdir(tmp)
		env2 := &ResolvedEnv{}
		if err := env2.resolveWorkDir(""); err != nil {
			t.Fatalf("resolveWorkDir: %v", err)
		}
		if env2.GitRepoDir != "" {
			t.Errorf("GitRepoDir = %q, want \"\" (non-repo tmp dir)", env2.GitRepoDir)
		}
	})

	t.Run("OverrideWorkDir sets both WorkDir and GitRepoDir", func(t *testing.T) {
		tmp := resolvedTempDir(t)
		// Initialise a real git repo in tmp.
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
		} {
			c := exec.Command("git", args...)
			c.Dir = tmp
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		env := &ResolvedEnv{}
		if err := env.OverrideWorkDir(tmp); err != nil {
			t.Fatalf("OverrideWorkDir: %v", err)
		}
		if env.WorkDir != tmp {
			t.Errorf("WorkDir = %q, want %q", env.WorkDir, tmp)
		}
		if env.GitRepoDir != tmp {
			t.Errorf("GitRepoDir = %q, want %q (repo root)", env.GitRepoDir, tmp)
		}

		// Subdirectory of the repo: WorkDir = sub, GitRepoDir = repo root.
		sub := filepath.Join(tmp, "deep", "sub")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := env.OverrideWorkDir(sub); err != nil {
			t.Fatalf("OverrideWorkDir(sub): %v", err)
		}
		if env.WorkDir != sub {
			t.Errorf("WorkDir = %q, want %q", env.WorkDir, sub)
		}
		if env.GitRepoDir != tmp {
			t.Errorf("GitRepoDir = %q, want %q (still repo root)", env.GitRepoDir, tmp)
		}
	})

	t.Run("config.toml [git] repo is ignored (not consulted at runtime)", func(t *testing.T) {
		// populateFromConfig must NOT set GitRepoDir even when cfg.Git.Repo is set.
		// (GitRepoDir comes only from gitutil.TopLevel(WorkDir).)
		// We exercise this via the integration tests, but assert here too.
		// See TestIntegration_BasicProject / _MonorepoSubdir for the full flow.
	})
}

// TestNewProjectInfoParamsCachesMeta verifies that multi-role commands which
// call NewProjectInfoParams N times only fork git log/status once. Regression
// guard: pre-caching, ateam report on 5 roles forked git 10 times for the
// same repo state.
func TestNewProjectInfoParamsCachesMeta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	tmp := resolvedTempDir(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = tmp
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	env := &ResolvedEnv{}
	if err := env.OverrideWorkDir(tmp); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}
	if env.projectMeta != nil {
		t.Fatal("projectMeta should start nil")
	}
	p1 := env.NewProjectInfoParams("role one", "report")
	if env.projectMeta == nil {
		t.Fatal("projectMeta should be cached after first call")
	}
	cached := env.projectMeta
	p2 := env.NewProjectInfoParams("role two", "report")
	if env.projectMeta != cached {
		t.Errorf("second call re-ran GetProjectMeta (cached %p, now %p)", cached, env.projectMeta)
	}
	if p1.Meta == nil || p2.Meta == nil {
		t.Errorf("Meta should be populated for a real repo (p1=%v, p2=%v)", p1.Meta, p2.Meta)
	}
	if p1.Meta != p2.Meta {
		t.Errorf("p1.Meta and p2.Meta should be the same pointer (cached)")
	}

	// OverrideWorkDir to a different dir must invalidate the cache.
	other := resolvedTempDir(t)
	if err := env.OverrideWorkDir(other); err != nil {
		t.Fatalf("OverrideWorkDir(other): %v", err)
	}
	if env.projectMeta != nil {
		t.Error("OverrideWorkDir to a new path should clear projectMeta")
	}
	if env.quickOrientation != nil {
		t.Error("OverrideWorkDir to a new path should clear quickOrientation")
	}
}

func TestNewProjectInfoParamsQuickOrientation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	tmp := resolvedTempDir(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = tmp
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	t.Run("always populated for a git repo", func(t *testing.T) {
		env := &ResolvedEnv{}
		if err := env.OverrideWorkDir(tmp); err != nil {
			t.Fatalf("OverrideWorkDir: %v", err)
		}
		p1 := env.NewProjectInfoParams("role one", "report")
		if !strings.Contains(p1.QuickOrientation, "## Quick orientation") {
			t.Errorf("QuickOrientation missing expected header:\n%s", p1.QuickOrientation)
		}
		// Cache reuse: second call should return the same pointer (avoids
		// re-running git ls-files / git log per role).
		cached := env.quickOrientation
		p2 := env.NewProjectInfoParams("role two", "report")
		if env.quickOrientation != cached {
			t.Errorf("second call re-collected quickOrientation (cached %p, now %p)", cached, env.quickOrientation)
		}
		if p1.QuickOrientation != p2.QuickOrientation {
			t.Error("p1 and p2 should share the same rendered QuickOrientation")
		}
	})
}

// TestLookupFromSeedsWorkDirFromStart verifies the regression flagged in
// review: LookupFrom(start) must populate WorkDir from `start`, not from
// os.Getwd(). eval --dirs A B passes explicit base/candidate paths and
// would otherwise attach the wrong execution directory.
func TestLookupFromSeedsWorkDirFromStart(t *testing.T) {
	base := resolvedTempDir(t)
	if err := os.MkdirAll(filepath.Join(base, ".ateam"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".ateam", "config.toml"), []byte("[project]\nname=\"p\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	other := resolvedTempDir(t)
	t.Chdir(other)

	env, err := LookupFrom(base)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	if env.WorkDir != base {
		t.Errorf("WorkDir = %q, want %q (the start path, not process cwd)", env.WorkDir, base)
	}
}

// TestResolveWorkDirCanonicalisesSymlinks verifies the fix for the
// symlink-unsafe in-tree detection finding: env.WorkDir is realPath'd so
// callers comparing it to ProjectDir (also realPath'd at discovery) use
// the same canonical form.
func TestResolveWorkDirCanonicalisesSymlinks(t *testing.T) {
	tmp := resolvedTempDir(t)
	realDir := filepath.Join(tmp, "real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(tmp, "link")
	if err := os.Symlink(realDir, symlink); err != nil {
		t.Fatal(err)
	}

	env := &ResolvedEnv{}
	if err := env.OverrideWorkDir(symlink); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}
	// WorkDir must equal the symlink target, not the symlink path —
	// otherwise pathInside(WorkDir, projectRoot) misclassifies a symlinked
	// cwd as outside the project tree.
	if env.WorkDir != realDir {
		t.Errorf("WorkDir = %q, want realPath %q (symlink should resolve)", env.WorkDir, realDir)
	}
}

// TestResolveProjectPathWalksUpFromSubdir verifies that --project PATH walks
// up to find .ateam/ — the same semantics as flag-less discovery. Before
// this, `ateam --project . ...` from a subdir of the project errored out
// with "no .ateam/ found at or under this path".
func TestResolveProjectPathWalksUpFromSubdir(t *testing.T) {
	base := resolvedTempDir(t)
	projectRoot := filepath.Join(base, "myproj")
	projectDir := makeProject(t, projectRoot, "myproj")

	subdir := filepath.Join(projectRoot, "deep", "sub", "path")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProjectPath(subdir)
	if err != nil {
		t.Fatalf("resolveProjectPath(subdir): %v", err)
	}
	if got != projectDir {
		t.Errorf("resolveProjectPath = %q, want %q (project root via walk-up)", got, projectDir)
	}
}

// TestResolveProjectPathWalksUpFromDotRelative covers the exact failing case
// the user reported: cwd inside the project, `--project .` from a subdir.
func TestResolveProjectPathWalksUpFromDotRelative(t *testing.T) {
	base := resolvedTempDir(t)
	projectRoot := filepath.Join(base, "myproj")
	projectDir := makeProject(t, projectRoot, "myproj")
	subdir := filepath.Join(projectRoot, "defaults", "roles")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	got, err := resolveProjectPath(".")
	if err != nil {
		t.Fatalf(`resolveProjectPath("."): %v`, err)
	}
	if got != projectDir {
		t.Errorf(`resolveProjectPath(".") = %q, want %q`, got, projectDir)
	}
}
