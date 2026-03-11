package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := Config{
		Project: ProjectConfig{Name: "myproject"},
		Git:     GitConfig{Repo: "myrepo", RemoteOriginURL: "https://github.com/example/repo.git"},
		Report: ReportConfig{
			MaxParallel:          5,
			ReportTimeoutMinutes: 20,
		},
		Roles: map[string]string{
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
	if loaded.Git.Repo != original.Git.Repo {
		t.Errorf("Git.Repo = %q, want %q", loaded.Git.Repo, original.Git.Repo)
	}
	if loaded.Git.RemoteOriginURL != original.Git.RemoteOriginURL {
		t.Errorf("Git.RemoteOriginURL = %q, want %q", loaded.Git.RemoteOriginURL, original.Git.RemoteOriginURL)
	}
	if loaded.Report.MaxParallel != original.Report.MaxParallel {
		t.Errorf("Report.MaxParallel = %d, want %d", loaded.Report.MaxParallel, original.Report.MaxParallel)
	}
	if loaded.Report.ReportTimeoutMinutes != original.Report.ReportTimeoutMinutes {
		t.Errorf("Report.ReportTimeoutMinutes = %d, want %d", loaded.Report.ReportTimeoutMinutes, original.Report.ReportTimeoutMinutes)
	}
	if len(loaded.Roles) != len(original.Roles) {
		t.Fatalf("Roles length = %d, want %d", len(loaded.Roles), len(original.Roles))
	}
	for k, v := range original.Roles {
		if loaded.Roles[k] != v {
			t.Errorf("Roles[%q] = %q, want %q", k, loaded.Roles[k], v)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "minimal"

[roles]
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
	if cfg.Report.ReportTimeoutMinutes != DefaultReportTimeoutMinutes {
		t.Errorf("Report.ReportTimeoutMinutes = %d, want default %d", cfg.Report.ReportTimeoutMinutes, DefaultReportTimeoutMinutes)
	}
}

func TestLoadDefaultsNilRoles(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "noroles"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Roles == nil {
		t.Error("Roles map should be initialized, got nil")
	}
	if len(cfg.Roles) != 0 {
		t.Errorf("Roles length = %d, want 0", len(cfg.Roles))
	}
}

func TestEnabledRoles(t *testing.T) {
	cfg := Config{
		Roles: map[string]string{
			"zebra":  "enabled",
			"alpha":  "enabled",
			"beta":   "disabled",
			"gamma":  "enabled",
			"delta":  "disabled",
		},
	}

	got := cfg.EnabledRoles()
	want := []string{"alpha", "gamma", "zebra"}

	if len(got) != len(want) {
		t.Fatalf("EnabledRoles() returned %d items, want %d: %v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("EnabledRoles()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestEnabledRolesEmpty(t *testing.T) {
	cfg := Config{
		Roles: map[string]string{
			"a": "disabled",
			"b": "disabled",
		},
	}

	got := cfg.EnabledRoles()
	if len(got) != 0 {
		t.Errorf("EnabledRoles() returned %v, want empty slice", got)
	}
}

func TestEffectiveTimeout(t *testing.T) {
	r := ReportConfig{ReportTimeoutMinutes: 10}

	if got := r.EffectiveTimeout(0); got != 10 {
		t.Errorf("EffectiveTimeout(0) = %d, want 10", got)
	}
	if got := r.EffectiveTimeout(30); got != 30 {
		t.Errorf("EffectiveTimeout(30) = %d, want 30", got)
	}
}
