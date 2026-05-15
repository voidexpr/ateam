package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/root"
)

// TestLookupEnvMatrix asserts the exact env values returned by lookupEnv()
// across the cross-product of cwd × --work-dir × --project. This is the
// pipeline behind `ateam env` (and every other command that calls
// resolveEnv / lookupEnv), so the matrix locks in:
//
//   - cwd-aware project discovery (walks up from cwd when no --project)
//   - --project PATH walking up to find .ateam/ (resolveSpecialDir)
//   - the project-aware WorkDir policy: cwd inside project → promoted to
//     project root; cwd outside → stays at cwd; --work-dir explicit always
//     wins.
//   - --project pointing at a non-ateam path → error
//
// If we change any of these rules, every affected row will fail and force
// us to think about the new semantics deliberately.
func TestLookupEnvMatrix(t *testing.T) {
	// Save and restore package-level flags.
	savedOrg, savedProj, savedWD := orgFlag, projectFlag, workDirFlag
	t.Cleanup(func() { orgFlag, projectFlag, workDirFlag = savedOrg, savedProj, savedWD })
	orgFlag = "" // always rely on discovery

	// Fixture:
	//   base/
	//     .ateamorg/
	//     myproj/.ateam/        (the project)
	//     myproj/sub/           (nested 1 level inside the project)
	//     myproj/a/b/c/         (nested 3 levels inside the project)
	//     other/                (cwd "outside" — same parent as myproj)
	//     noateam/              (--project target with no .ateam at or above)
	//     explicitWD/           (--work-dir target)
	base := resolvedTestDir(t)

	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projectRoot := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projectRoot, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectDir := filepath.Join(projectRoot, ".ateam")

	nested1 := filepath.Join(projectRoot, "sub")
	nested3 := filepath.Join(projectRoot, "a", "b", "c")
	outsideDir := filepath.Join(base, "other")
	nonProject := filepath.Join(base, "noateam")
	explicitWD := filepath.Join(base, "explicitWD")
	for _, d := range []string{nested1, nested3, outsideDir, nonProject, explicitWD} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	type cwdSpec struct {
		label string
		path  string
	}
	cwds := []cwdSpec{
		{"root", projectRoot},
		{"n1", nested1},
		{"n3", nested3},
		{"out", outsideDir},
	}

	type projSpec struct {
		label string
		flag  string
	}
	projs := []projSpec{
		{"none", ""},
		{"root", projectRoot},
		{"n1", nested1},
		{"n3", nested3},
		{"nonProj", nonProject},
	}

	wds := []struct {
		label string
		flag  string
	}{
		{"none", ""},
		{"explicit", explicitWD},
	}

	// expect returns the (ProjectDir, WorkDir, OrgDir, wantErr) we expect
	// from lookupEnv() for the given combination. Keep this logic as a
	// readable mirror of the rules in resolve.go / cmd/resolve_env.go.
	expect := func(c cwdSpec, p projSpec, wdFlag string) (proj, work, org string, wantErr bool) {
		// --project pointing at a path with no .ateam at or above → error.
		if p.flag == nonProject {
			return "", "", "", true
		}

		// Project discovery.
		switch p.flag {
		case "":
			if c.label == "out" {
				proj = "" // discovery from outside finds org only, no project
			} else {
				proj = projectDir // walk up from cwd
			}
		default:
			proj = projectDir // walk up from explicit --project target
		}

		// Org is always findable in this fixture (base contains .ateamorg)
		// regardless of cwd or --project, because every resolved path is a
		// descendant of base.
		org = orgDir

		// WorkDir: explicit --work-dir wins; else cwd, promoted to project
		// root when cwd is inside the project tree AND a project exists.
		if wdFlag != "" {
			work = wdFlag
			return proj, work, org, false
		}
		if proj != "" && c.label != "out" {
			work = projectRoot
		} else {
			work = c.path
		}
		return proj, work, org, false
	}

	for _, c := range cwds {
		for _, p := range projs {
			for _, wd := range wds {
				name := fmt.Sprintf("cwd=%s/proj=%s/wd=%s", c.label, p.label, wd.label)
				t.Run(name, func(t *testing.T) {
					t.Chdir(c.path)
					projectFlag = p.flag
					workDirFlag = wd.flag

					env, err := lookupEnv()
					wantProj, wantWork, wantOrg, wantErr := expect(c, p, wd.flag)

					if wantErr {
						if err == nil {
							t.Fatalf("want error, got env={ProjectDir=%q WorkDir=%q OrgDir=%q}",
								env.ProjectDir, env.WorkDir, env.OrgDir)
						}
						return
					}
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if env.ProjectDir != wantProj {
						t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, wantProj)
					}
					if env.WorkDir != wantWork {
						t.Errorf("WorkDir = %q, want %q", env.WorkDir, wantWork)
					}
					if env.OrgDir != wantOrg {
						t.Errorf("OrgDir = %q, want %q", env.OrgDir, wantOrg)
					}
				})
			}
		}
	}
}

// resolvedTestDir returns a tempdir with symlinks resolved so its path
// matches what resolveWorkDir / discoverProject produce after their own
// realPath canonicalisation.
func resolvedTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return resolved
}
