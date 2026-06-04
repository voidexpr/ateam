package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/root"
)

func TestUpdateDiffShowsChangedFile(t *testing.T) {
	tmp := t.TempDir()
	if err := runInstall(nil, []string{tmp}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	if len(promptdata.AllRoleIDs) == 0 {
		t.Fatal("promptdata.AllRoleIDs is empty; embedded defaults missing")
	}
	roleID := promptdata.AllRoleIDs[0]
	rel := filepath.Join("defaults", "prompts", "report", roleID+".prompt.md")
	rolePrompt := filepath.Join(tmp, root.OrgDirName, rel)
	if err := os.WriteFile(rolePrompt, []byte("mutated content\n"), 0644); err != nil {
		t.Fatalf("mutate prompt: %v", err)
	}

	out := withUpdateRun(t, tmp, true /*diff*/, false /*quiet*/, func() {
		if err := runUpdate(nil, nil); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
	})

	if !strings.Contains(out, rel) {
		t.Errorf("expected diff output to mention %q, got:\n%s", rel, out)
	}
}

func TestUpdateOverwritesStaleDefault(t *testing.T) {
	tmp := t.TempDir()
	if err := runInstall(nil, []string{tmp}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	if len(promptdata.AllRoleIDs) == 0 {
		t.Fatal("promptdata.AllRoleIDs is empty; embedded defaults missing")
	}
	roleID := promptdata.AllRoleIDs[0]
	embeddedPath := filepath.Join("prompts", "report", roleID+".prompt.md")
	want, err := defaults.FS.ReadFile(embeddedPath)
	if err != nil {
		t.Fatalf("read embedded %s: %v", embeddedPath, err)
	}

	rolePrompt := filepath.Join(tmp, root.OrgDirName, "defaults", "prompts", "report", roleID+".prompt.md")
	if err := os.WriteFile(rolePrompt, []byte("mutated content\n"), 0644); err != nil {
		t.Fatalf("mutate prompt: %v", err)
	}

	withUpdateRun(t, tmp, false /*diff*/, false /*quiet*/, func() {
		if err := runUpdate(nil, nil); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
	})

	got, err := os.ReadFile(rolePrompt)
	if err != nil {
		t.Fatalf("read role prompt after update: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("prompt file was not restored from embedded default\n got: %q\nwant: %q", string(got), string(want))
	}
}

func TestUpdateNoChangesProducesEmptyDiff(t *testing.T) {
	tmp := t.TempDir()
	if err := runInstall(nil, []string{tmp}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	out := withUpdateRun(t, tmp, true /*diff*/, false /*quiet*/, func() {
		if err := runUpdate(nil, nil); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
	})

	if !strings.Contains(out, "All defaults are up to date") {
		t.Errorf("expected up-to-date message, got:\n%s", out)
	}
	if strings.Contains(out, "Found ") && strings.Contains(out, " file(s) to update") {
		t.Errorf("did not expect any files to be listed for a fresh install, got:\n%s", out)
	}
}

// withUpdateRun chdirs into dir, sets the updateDiff/updateQuiet flag globals
// to the given values, captures os.Stdout while fn runs, then restores
// everything. Returns whatever fn wrote to stdout.
func withUpdateRun(t *testing.T, dir string, diff, quiet bool, fn func()) string {
	t.Helper()

	prevDiff, prevQuiet := updateDiff, updateQuiet
	updateDiff, updateQuiet = diff, quiet
	defer func() {
		updateDiff, updateQuiet = prevDiff, prevQuiet
	}()

	out := captureStdout(t, func() {
		withChdir(t, dir, fn)
	})
	return out
}
