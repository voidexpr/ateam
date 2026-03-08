package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := Config{
		Project: ProjectConfig{Name: "myproject", Source: "/src"},
		Git:     GitConfig{Repo: "myrepo", RemoteOriginURL: "https://github.com/example/repo.git"},
		Report: ReportConfig{
			MaxParallel:               5,
			AgentReportTimeoutMinutes: 20,
		},
		Agents: map[string]string{
			"lint":   "enabled",
			"test":   "disabled",
			"review": "enabled",
		},
	}

	if err := Save(dir, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Project.Name != original.Project.Name {
		t.Errorf("Project.Name = %q, want %q", loaded.Project.Name, original.Project.Name)
	}
	if loaded.Project.Source != original.Project.Source {
		t.Errorf("Project.Source = %q, want %q", loaded.Project.Source, original.Project.Source)
	}
	if loaded.Git.Repo != original.Git.Repo {
		t.Errorf("Git.Repo = %q, want %q", loaded.Git.Repo, original.Git.Repo)
	}
	if loaded.Git.RemoteOriginURL != original.Git.RemoteOriginURL {
		t.Errorf("Git.RemoteOriginURL = %q, want %q", loaded.Git.RemoteOriginURL, original.Git.RemoteOriginURL)
	}
	if loaded.Report.MaxParallel != original.Report.MaxParallel {
		t.Errorf("Report.MaxParallel = %d, want %d", loaded.Report.MaxParallel, original.Report.MaxParallel)
	}
	if loaded.Report.AgentReportTimeoutMinutes != original.Report.AgentReportTimeoutMinutes {
		t.Errorf("Report.AgentReportTimeoutMinutes = %d, want %d", loaded.Report.AgentReportTimeoutMinutes, original.Report.AgentReportTimeoutMinutes)
	}
	if len(loaded.Agents) != len(original.Agents) {
		t.Fatalf("Agents length = %d, want %d", len(loaded.Agents), len(original.Agents))
	}
	for k, v := range original.Agents {
		if loaded.Agents[k] != v {
			t.Errorf("Agents[%q] = %q, want %q", k, loaded.Agents[k], v)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal config with no [report] section
	content := `[project]
name = "minimal"
source = "/tmp/src"

[agents]
lint = "enabled"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Report.MaxParallel != DefaultMaxParallel {
		t.Errorf("Report.MaxParallel = %d, want default %d", cfg.Report.MaxParallel, DefaultMaxParallel)
	}
	if cfg.Report.AgentReportTimeoutMinutes != DefaultAgentReportTimeoutMinutes {
		t.Errorf("Report.AgentReportTimeoutMinutes = %d, want default %d", cfg.Report.AgentReportTimeoutMinutes, DefaultAgentReportTimeoutMinutes)
	}
}

func TestLoadDefaultsNilAgents(t *testing.T) {
	dir := t.TempDir()

	// Write a config with no [agents] section
	content := `[project]
name = "noagents"
source = "/tmp/src"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agents == nil {
		t.Error("Agents map should be initialized, got nil")
	}
	if len(cfg.Agents) != 0 {
		t.Errorf("Agents length = %d, want 0", len(cfg.Agents))
	}
}

func TestEnabledAgents(t *testing.T) {
	cfg := Config{
		Agents: map[string]string{
			"zebra":  "enabled",
			"alpha":  "enabled",
			"beta":   "disabled",
			"gamma":  "enabled",
			"delta":  "disabled",
		},
	}

	got := cfg.EnabledAgents()
	want := []string{"alpha", "gamma", "zebra"}

	if len(got) != len(want) {
		t.Fatalf("EnabledAgents() returned %d items, want %d: %v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("EnabledAgents()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestEnabledAgentsEmpty(t *testing.T) {
	cfg := Config{
		Agents: map[string]string{
			"a": "disabled",
			"b": "disabled",
		},
	}

	got := cfg.EnabledAgents()
	if len(got) != 0 {
		t.Errorf("EnabledAgents() returned %v, want empty slice", got)
	}
}

func TestEffectiveTimeout(t *testing.T) {
	r := ReportConfig{AgentReportTimeoutMinutes: 10}

	if got := r.EffectiveTimeout(0); got != 10 {
		t.Errorf("EffectiveTimeout(0) = %d, want 10", got)
	}
	if got := r.EffectiveTimeout(30); got != 30 {
		t.Errorf("EffectiveTimeout(30) = %d, want 30", got)
	}
}
