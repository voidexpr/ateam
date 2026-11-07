package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

func TestSetupModelPinsWritesSuggestion(t *testing.T) {
	home, projPath, env := setupFixture(t)

	out := captureStdout(t, func() {
		if err := setupModelPins(env, modelChoiceSuggested, agentConfigHome, []string{"claude"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `model = "claude-sonnet-5-5"`) {
		t.Errorf("expected the suggested pin to be written:\n%s", out)
	}
	env2, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if env2.Runtime.Agents["claude"].Model != "claude-sonnet-5-5" {
		t.Errorf("claude model after setup = %q", env2.Runtime.Agents["claude"].Model)
	}
	if src, _ := env2.Runtime.Source("agent.claude.model"); src.Layer != filepath.Join(home, root.ConfigFile) {
		t.Errorf("pin source: %+v", src)
	}

	// Pinned now: without an explicit choice nothing changes.
	out = captureStdout(t, func() {
		if err := setupModelPins(env2, "", agentConfigHome, []string{"claude"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "already pinned") {
		t.Errorf("expected already-pinned notice:\n%s", out)
	}

	// An explicit id goes to the project layer.
	captureStdout(t, func() {
		if err := setupModelPins(env2, "claude-haiku-5-5", agentConfigProject, []string{"claude"}); err != nil {
			t.Fatal(err)
		}
	})
	env3, _ := root.LookupFrom(projPath)
	if env3.Runtime.Agents["claude"].Model != "claude-haiku-5-5" {
		t.Errorf("project pin must win: %q", env3.Runtime.Agents["claude"].Model)
	}

	// none and bad layer
	before, _ := os.ReadFile(filepath.Join(home, root.ConfigFile))
	captureStdout(t, func() {
		if err := setupModelPins(env3, modelChoiceNone, agentConfigHome, []string{"codex"}); err != nil {
			t.Fatal(err)
		}
	})
	after, _ := os.ReadFile(filepath.Join(home, root.ConfigFile))
	if string(before) != string(after) {
		t.Error("none must write nothing")
	}
	if err := setupModelPins(env3, modelChoiceSuggested, "elsewhere", []string{"claude"}); err == nil {
		t.Error("bad layer must error")
	}
	scratch := &root.ResolvedEnv{HomeDir: home, Runtime: env3.Runtime}
	if err := setupModelPins(scratch, modelChoiceSuggested, agentConfigProject, []string{"claude"}); err == nil {
		t.Error("project layer without a project must error")
	}
}

func TestRefreshPricingWritesManagedSection(t *testing.T) {
	home, projPath, env := setupFixture(t)
	data, err := defaults.FS.ReadFile(runtime.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	served := strings.Replace(string(data), `pricing_as_of = "`, `pricing_as_of = "2030-01-01" // was "`, 1)
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(served))
	}))
	defer srv.Close()
	t.Setenv("ATEAM_DEFAULTS_URL", srv.URL+"/ateam.hcl")

	for i := 0; i < 2; i++ {
		out := captureStdout(t, func() {
			if err := refreshPricing(env); err != nil {
				t.Fatalf("refresh %d: %v", i, err)
			}
		})
		if !strings.Contains(out, "pricing as of 2030-01-01") {
			t.Errorf("refresh %d output:\n%s", i, out)
		}
	}
	content, _ := os.ReadFile(filepath.Join(home, root.ConfigFile))
	if strings.Count(string(content), runtime.PricingSectionStart) != 1 {
		t.Errorf("managed section must exist once:\n%s", content)
	}
	env2, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("home file no longer loads: %v", err)
	}
	if env2.Runtime.PricingAsOf != "2030-01-01" || env2.Runtime.Agents["claude"].Pricing.SuggestedModel == "" {
		t.Errorf("fetched pricing not applied: as_of=%q", env2.Runtime.PricingAsOf)
	}
	if w := pricingStaleWarning(env2.Runtime, time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)); w != "" {
		t.Errorf("fresh tables must not warn: %q", w)
	}
	if w := pricingStaleWarning(env2.Runtime, time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)); !strings.Contains(w, "refresh-models") {
		t.Errorf("old tables must warn: %q", w)
	}

	status = http.StatusInternalServerError
	if err := refreshPricing(env); err == nil {
		t.Error("HTTP error must be reported")
	}
}
