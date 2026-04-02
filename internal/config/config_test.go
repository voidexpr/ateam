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
			"lint":   "on",
			"test":   "off",
			"review": "on",
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
	// On-disk roles override defaults; check saved values are preserved.
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
lint = "on"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	defaults := DefaultConfig()
	if cfg.Report.MaxParallel != defaults.Report.MaxParallel {
		t.Errorf("Report.MaxParallel = %d, want default %d", cfg.Report.MaxParallel, defaults.Report.MaxParallel)
	}
	if cfg.Report.ReportTimeoutMinutes != defaults.Report.ReportTimeoutMinutes {
		t.Errorf("Report.ReportTimeoutMinutes = %d, want default %d", cfg.Report.ReportTimeoutMinutes, defaults.Report.ReportTimeoutMinutes)
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
	// When no [roles] section is on disk, defaults from embedded template are used.
	defaults := DefaultConfig()
	if len(cfg.Roles) != len(defaults.Roles) {
		t.Errorf("Roles length = %d, want %d (defaults)", len(cfg.Roles), len(defaults.Roles))
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Report.MaxParallel != 3 {
		t.Errorf("Report.MaxParallel = %d, want 3", cfg.Report.MaxParallel)
	}
	if cfg.Report.ReportTimeoutMinutes != 20 {
		t.Errorf("Report.ReportTimeoutMinutes = %d, want 20", cfg.Report.ReportTimeoutMinutes)
	}
	if cfg.Review.TimeoutMinutes != 20 {
		t.Errorf("Review.TimeoutMinutes = %d, want 20", cfg.Review.TimeoutMinutes)
	}
	if cfg.Code.TimeoutMinutes != 120 {
		t.Errorf("Code.TimeoutMinutes = %d, want 120", cfg.Code.TimeoutMinutes)
	}
	if len(cfg.Roles) == 0 {
		t.Fatal("Roles should not be empty")
	}
	if cfg.Roles["security"] != "on" {
		t.Errorf("Roles[security] = %q, want %q", cfg.Roles["security"], "on")
	}
	if cfg.Roles["automation"] != "off" {
		t.Errorf("Roles[automation] = %q, want %q", cfg.Roles["automation"], "off")
	}
}

func TestEnabledRoles(t *testing.T) {
	cfg := Config{
		Roles: map[string]string{
			"zebra": "on",
			"alpha": "enabled", // backward compat
			"beta":  "off",
			"gamma": "on",
			"delta": "disabled", // backward compat
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
			"a": "off",
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

func TestSandboxExtra(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "sandbox-test"

[sandbox-extra]
allow_write = ["/tmp/test-write", "/data/output"]
allow_read = ["/opt/test-read"]
allow_domains = ["example.com", "*.internal.dev"]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.SandboxExtra.AllowWrite) != 2 {
		t.Fatalf("AllowWrite length = %d, want 2", len(cfg.SandboxExtra.AllowWrite))
	}
	if cfg.SandboxExtra.AllowWrite[0] != "/tmp/test-write" {
		t.Errorf("AllowWrite[0] = %q, want %q", cfg.SandboxExtra.AllowWrite[0], "/tmp/test-write")
	}
	if len(cfg.SandboxExtra.AllowRead) != 1 {
		t.Fatalf("AllowRead length = %d, want 1", len(cfg.SandboxExtra.AllowRead))
	}
	if cfg.SandboxExtra.AllowRead[0] != "/opt/test-read" {
		t.Errorf("AllowRead[0] = %q, want %q", cfg.SandboxExtra.AllowRead[0], "/opt/test-read")
	}
	if len(cfg.SandboxExtra.AllowDomains) != 2 {
		t.Fatalf("AllowDomains length = %d, want 2", len(cfg.SandboxExtra.AllowDomains))
	}
	if cfg.SandboxExtra.AllowDomains[0] != "example.com" {
		t.Errorf("AllowDomains[0] = %q, want %q", cfg.SandboxExtra.AllowDomains[0], "example.com")
	}
}

func TestContainerExtra(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "with-container-extra"

[container-extra]
extra_args = ["-p", "3000:3000", "-v", "pgdata:/pgdata"]
forward_env = ["DB_PORT"]

[container-extra.env]
DB_HOST = "localhost"
BIND_HOST = "0.0.0.0"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.ContainerExtra.ExtraArgs) != 4 {
		t.Fatalf("ExtraArgs length = %d, want 4", len(cfg.ContainerExtra.ExtraArgs))
	}
	if cfg.ContainerExtra.ExtraArgs[0] != "-p" || cfg.ContainerExtra.ExtraArgs[1] != "3000:3000" {
		t.Errorf("ExtraArgs = %v, want [-p 3000:3000 -v pgdata:/pgdata]", cfg.ContainerExtra.ExtraArgs)
	}
	if len(cfg.ContainerExtra.ForwardEnv) != 1 || cfg.ContainerExtra.ForwardEnv[0] != "DB_PORT" {
		t.Errorf("ForwardEnv = %v, want [DB_PORT]", cfg.ContainerExtra.ForwardEnv)
	}
	if cfg.ContainerExtra.Env["DB_HOST"] != "localhost" {
		t.Errorf("Env[DB_HOST] = %q, want %q", cfg.ContainerExtra.Env["DB_HOST"], "localhost")
	}
	if cfg.ContainerExtra.Env["BIND_HOST"] != "0.0.0.0" {
		t.Errorf("Env[BIND_HOST] = %q, want %q", cfg.ContainerExtra.Env["BIND_HOST"], "0.0.0.0")
	}
}

func TestContainerExtraEmpty(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "no-extras"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.ContainerExtra.ExtraArgs) != 0 {
		t.Errorf("ExtraArgs should be empty, got %v", cfg.ContainerExtra.ExtraArgs)
	}
	if len(cfg.ContainerExtra.ForwardEnv) != 0 {
		t.Errorf("ForwardEnv should be empty, got %v", cfg.ContainerExtra.ForwardEnv)
	}
	if len(cfg.ContainerExtra.Env) != 0 {
		t.Errorf("Env should be empty, got %v", cfg.ContainerExtra.Env)
	}
}

func TestSandboxExtraEmpty(t *testing.T) {
	dir := t.TempDir()

	content := `[project]
name = "no-sandbox"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.SandboxExtra.AllowWrite) != 0 {
		t.Errorf("AllowWrite should be empty, got %v", cfg.SandboxExtra.AllowWrite)
	}
	if len(cfg.SandboxExtra.AllowRead) != 0 {
		t.Errorf("AllowRead should be empty, got %v", cfg.SandboxExtra.AllowRead)
	}
	if len(cfg.SandboxExtra.AllowDomains) != 0 {
		t.Errorf("AllowDomains should be empty, got %v", cfg.SandboxExtra.AllowDomains)
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
