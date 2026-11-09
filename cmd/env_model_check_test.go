package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestEnvReportsModelNameError(t *testing.T) {
	_, projPath, _ := setupFixture(t)
	projFile := filepath.Join(projPath, ".ateam", root.ConfigFile)
	if err := os.WriteFile(projFile, []byte("project {\n  name = \"myproj\"\n}\n\nagent \"claude\" {\n  model = \"claude-ops-5-5\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}
	rtCfg, err := loadRuntime(env)
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { printProfilesInUse(env, rtCfg) })
	for _, want := range []string{
		`Error: agent "claude": model "claude-ops-5-5" does not look like a claude model id (set by ` + projFile + ":6)",
		"\n      known models: ",
		"\n      reference: https://platform.claude.com/docs/en/models/overview",
		"\n      --skip-model-name-check runs it anyway",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env lacks %q:\n%s", want, out)
		}
	}
}
