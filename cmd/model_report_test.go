package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestNativeConfigForReadsAgentConfigDirAndSkipsContainers(t *testing.T) {
	home, _, env := setupFixture(t)
	cfgDir := filepath.Join(home, "state", "agents", "claude")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"model":"claude-opus-5-5"}`), 0644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := setupAgentConfigDirs(env, agentConfigHome, []string{"claude"}, false); err != nil {
			t.Fatal(err)
		}
	})
	env2, err := root.LookupFrom(env.SourceDir)
	if err != nil {
		t.Fatal(err)
	}
	rt := env2.Runtime

	prof, ac, _, _ := rt.ResolveProfile("default")
	n := nativeConfigFor(env2, rt, prof, ac)
	if n.Model != "claude-opus-5-5" || !strings.HasSuffix(n.Source, "settings.json") {
		t.Errorf("host profile: %+v", n)
	}

	prof, ac, _, _ = rt.ResolveProfile("docker-api")
	n = nativeConfigFor(env2, rt, prof, ac)
	if n.Model != "" || !strings.Contains(n.Note, "container") {
		t.Errorf("container profile must not be inspected: %+v", n)
	}

	prof, ac, _, _ = rt.ResolveProfile("test")
	if n := nativeConfigFor(env2, rt, prof, ac); n.Source != "" {
		t.Errorf("builtin agent: %+v", n)
	}
}
