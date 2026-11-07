package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestEnvTracePrintsFileAndLine(t *testing.T) {
	home, projPath, _ := setupFixture(t)
	homeFile := filepath.Join(home, root.ConfigFile)
	if err := os.WriteFile(homeFile, []byte("agent \"claude\" {\n  model = \"sonnet\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	projFile := filepath.Join(projPath, ".ateam", root.ConfigFile)
	if err := os.WriteFile(projFile, []byte("project {\n  name = \"myproj\"\n}\n\nagent \"claude\" {\n  model = \"opus\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := printTrace(env, "agent.claude.model"); err != nil {
			t.Fatal(err)
		}
	})
	want := "agent.claude.model = opus  (" + tildeHome(projFile) + ":6)\n    overrides " + tildeHome(homeFile) + ":2 = sonnet\n"
	if out != want {
		t.Errorf("trace:\n got %q\nwant %q", out, want)
	}

	out = captureStdout(t, func() {
		if err := printTrace(env, "agent.claude"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"agent.claude.name = claude  (built-in:", "agent.claude.sandbox = { ... (", " lines)  (built-in:"} {
		if !strings.Contains(out, want) {
			t.Errorf("trace of agent.claude lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "agent.codex") || strings.Contains(out, "profile.") {
		t.Errorf("prefix must limit the trace to the agent block:\n%s", out)
	}

	if err := printTrace(env, "agent.nope"); err == nil || !strings.Contains(err.Error(), "no attribute matches") {
		t.Errorf("unknown prefix: got %v", err)
	}
}
