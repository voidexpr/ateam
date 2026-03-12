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

func TestResolveProfileDefault(t *testing.T) {
	cfg := Config{}
	if got := cfg.ResolveProfile("", ""); got != "default" {
		t.Errorf("ResolveProfile empty = %q, want 'default'", got)
	}
}

func TestResolveProfileProjectDefault(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{DefaultProfile: "custom"},
	}
	if got := cfg.ResolveProfile("", ""); got != "custom" {
		t.Errorf("ResolveProfile = %q, want 'custom'", got)
	}
}

func TestResolveProfileSupervisor(t *testing.T) {
	cfg := Config{
		Supervisor: SupervisorConfig{
			ReviewProfile: "review-prof",
			CodeProfile:   "code-prof",
		},
	}
	if got := cfg.ResolveProfile("review", ""); got != "review-prof" {
		t.Errorf("ResolveProfile(review) = %q, want 'review-prof'", got)
	}
	if got := cfg.ResolveProfile("code", ""); got != "code-prof" {
		t.Errorf("ResolveProfile(code) = %q, want 'code-prof'", got)
	}
}

func TestResolveProfileRoleSpecific(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{DefaultProfile: "proj-default"},
		Profiles: ProfilesConfig{
			Roles: map[string]string{
				"security": "security-prof",
			},
		},
	}
	if got := cfg.ResolveProfile("report", "security"); got != "security-prof" {
		t.Errorf("ResolveProfile(report, security) = %q, want 'security-prof'", got)
	}
	if got := cfg.ResolveProfile("report", "other"); got != "proj-default" {
		t.Errorf("ResolveProfile(report, other) = %q, want 'proj-default'", got)
	}
}

func TestResolveProfilePriority(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{DefaultProfile: "proj"},
		Supervisor: SupervisorConfig{
			DefaultProfile: "sup",
			ReviewProfile:  "review-sup",
		},
		Profiles: ProfilesConfig{
			Roles: map[string]string{
				"security": "sec-prof",
			},
		},
	}
	// Role-specific wins over everything
	if got := cfg.ResolveProfile("review", "security"); got != "sec-prof" {
		t.Errorf("role-specific should win, got %q", got)
	}
	// Action-specific supervisor wins over defaults
	if got := cfg.ResolveProfile("review", "other"); got != "review-sup" {
		t.Errorf("review supervisor should win, got %q", got)
	}
	// Supervisor default wins over project default
	if got := cfg.ResolveProfile("run", ""); got != "sup" {
		t.Errorf("supervisor default should win, got %q", got)
	}
}
