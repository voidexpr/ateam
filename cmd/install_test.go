package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/root"
)

func TestRunInstallCreatesOrgFixture(t *testing.T) {
	tmp := t.TempDir()

	if err := runInstall(nil, []string{tmp}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	orgDir := filepath.Join(tmp, root.OrgDirName)
	if info, err := os.Stat(orgDir); err != nil || !info.IsDir() {
		t.Fatalf("expected org dir %s to exist: %v", orgDir, err)
	}

	// Embedded runtime defaults must be present.
	for _, rel := range []string{
		filepath.Join("defaults", "runtime.hcl"),
		filepath.Join("defaults", "Dockerfile"),
	} {
		path := filepath.Join(orgDir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	// Embedded supervisor prompt must be present at the v1 path.
	supervisorPrompt := filepath.Join(orgDir, "defaults", "prompts", "review.prompt.md")
	if _, err := os.Stat(supervisorPrompt); err != nil {
		t.Errorf("expected %s to exist: %v", supervisorPrompt, err)
	}

	// Embedded role prompts must be present for every known role.
	if len(promptdata.AllRoleIDs) == 0 {
		t.Fatal("promptdata.AllRoleIDs is empty; embedded defaults missing")
	}
	for _, id := range promptdata.AllRoleIDs {
		rolePrompt := filepath.Join(orgDir, "defaults", "prompts", "report", id+".prompt.md")
		if _, err := os.Stat(rolePrompt); err != nil {
			t.Errorf("expected role prompt %s to exist: %v", rolePrompt, err)
		}
	}
}

func TestRunInstallRefusesExistingOrg(t *testing.T) {
	tmp := t.TempDir()

	if err := runInstall(nil, []string{tmp}); err != nil {
		t.Fatalf("first runInstall: %v", err)
	}

	err := runInstall(nil, []string{tmp})
	if err == nil {
		t.Fatal("expected second runInstall to error when org already exists")
	}
}
