package runtime

import (
	"path/filepath"
	"testing"

	"github.com/ateam/internal/config"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.Report.MaxParallel != 3 || s.Report.ReportTimeoutMinutes != 20 || s.Code.TimeoutMinutes != 120 || s.Exec.TimeoutMinutes != 60 {
		t.Errorf("built-in timeouts: %+v %+v %+v", s.Report, s.Code, s.Exec)
	}
	if s.Project.Name != "" {
		t.Errorf("the built-in defaults must not name a project, got %q", s.Project.Name)
	}
	if s.Roles["code.bugs"] != config.RoleEnabled || s.Roles["critic.features"] != config.RoleDisabled {
		t.Errorf("built-in role statuses: %v", s.Roles)
	}
	if len(s.EnabledRoles()) == 0 {
		t.Error("expected some roles enabled by default")
	}
}

func TestSettingsMergeAcrossLayers(t *testing.T) {
	homeDir := writeLayer(t, t.TempDir(), `
project {
  default_profile        = "cheap"
  report_timeout_minutes = 5
}

role "code.bugs" { status = "off" }
role "custom.role" { status = "on" }

supervisor {
  code_profile = "codex"
}

sandbox_extra {
  allow_domains = ["home.example"]
}

container_extra {
  env = { A = "home", B = "home" }
}
`)
	projDir := writeLayer(t, t.TempDir(), `
project {
  name = "svc"
}

role "code.bugs" { profile = "docker" }

supervisor {
  review_profile = "claude-high"
}

sandbox_extra {
  allow_domains = ["proj.example"]
}

container_extra {
  env = { B = "proj" }
}
`)
	cfg, err := Load([]string{homeDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Settings
	if s.Project.Name != "svc" || s.Project.DefaultProfile != "cheap" {
		t.Errorf("project: %+v", s.Project)
	}
	if s.Report.ReportTimeoutMinutes != 5 || s.Report.MaxParallel != 3 {
		t.Errorf("report settings must merge per attribute: %+v", s.Report)
	}
	if s.Roles["code.bugs"] != config.RoleDisabled || s.Profiles.Roles["code.bugs"] != "docker" {
		t.Errorf("role code.bugs: status %q profile %q", s.Roles["code.bugs"], s.Profiles.Roles["code.bugs"])
	}
	if s.Roles["custom.role"] != config.RoleEnabled || s.Roles["test.gaps"] != config.RoleEnabled {
		t.Errorf("roles: %v", s.Roles)
	}
	if s.Supervisor.CodeProfile != "codex" || s.Supervisor.ReviewProfile != "claude-high" {
		t.Errorf("supervisor: %+v", s.Supervisor)
	}
	if len(s.SandboxExtra.AllowDomains) != 1 || s.SandboxExtra.AllowDomains[0] != "proj.example" {
		t.Errorf("lists replace: %v", s.SandboxExtra.AllowDomains)
	}
	if s.ContainerExtra.Env["A"] != "home" || s.ContainerExtra.Env["B"] != "proj" {
		t.Errorf("maps merge per key: %v", s.ContainerExtra.Env)
	}
	if src, _ := cfg.Source("project.name"); src.Layer != filepath.Join(projDir, ConfigFile) {
		t.Errorf("project.name source: %+v", src)
	}
	if src, _ := cfg.Source("role.code.bugs.status"); src.Layer != filepath.Join(homeDir, ConfigFile) {
		t.Errorf("role status source: %+v", src)
	}
	if src, _ := cfg.Source("supervisor.code_profile"); src.Value != "codex" {
		t.Errorf("supervisor source: %+v", src)
	}
}

func TestProjectNameProbe(t *testing.T) {
	named := writeLayer(t, t.TempDir(), `project { name = "probe" }`)
	if got := ProjectName(named); got != "probe" {
		t.Errorf("ProjectName = %q, want probe", got)
	}
	unnamed := writeLayer(t, t.TempDir(), `project { default_profile = "cheap" }`)
	if got := ProjectName(unnamed); got != "" {
		t.Errorf("project block without name must not count, got %q", got)
	}
	if got := ProjectName(t.TempDir()); got != "" {
		t.Errorf("missing file: got %q", got)
	}
	broken := writeLayer(t, t.TempDir(), `project { name = `)
	if got := ProjectName(broken); got != "" {
		t.Errorf("unparsable file: got %q", got)
	}
}

func TestRoleBlockWithOnlyProfileDeclaresRole(t *testing.T) {
	dir := writeLayer(t, t.TempDir(), `role "my.custom" { profile = "codex" }`)
	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status, ok := cfg.Settings.Roles["my.custom"]; !ok || status != config.RoleDisabled {
		t.Errorf("role declared by profile only: status %q present=%v", status, ok)
	}
}
