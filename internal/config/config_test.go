package config

import (
	"testing"
)

func TestEnabledRoles(t *testing.T) {
	cfg := Config{Roles: map[string]string{
		"security": RoleEnabled,
		"testing":  RoleDisabled,
		"legacy":   "enabled",
		"docs":     RoleEnabled,
	}}
	got := cfg.EnabledRoles()
	want := []string{"docs", "legacy", "security"}
	if len(got) != len(want) {
		t.Fatalf("EnabledRoles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("EnabledRoles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if (Config{}).EnabledRoles() != nil {
		t.Error("no roles: expected nil")
	}
}

func TestEffectiveTimeout(t *testing.T) {
	r := ReportConfig{MaxParallel: 3, ReportTimeoutMinutes: 20}
	if r.EffectiveTimeout(0) != 20 || r.EffectiveTimeout(5) != 5 {
		t.Error("report timeout override")
	}
	if r.EffectiveMaxParallel(0) != 3 || r.EffectiveMaxParallel(1) != 1 {
		t.Error("report max parallel override")
	}
	if (CodeConfig{TimeoutMinutes: 120}).EffectiveTimeout(0) != 120 {
		t.Error("code timeout")
	}
	if (ExecConfig{TimeoutMinutes: 60}).EffectiveTimeout(7) != 7 {
		t.Error("exec timeout override")
	}
}

func TestResolveProfileDefault(t *testing.T) {
	cfg := Config{}
	if got := cfg.ResolveProfile("", "").Name; got != "default" {
		t.Errorf("ResolveProfile empty = %q, want 'default'", got)
	}
}

func TestResolveProfileProjectDefault(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{DefaultProfile: "custom"},
	}
	if got := cfg.ResolveProfile("", "").Name; got != "custom" {
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
	if got := cfg.ResolveProfile("review", "").Name; got != "review-prof" {
		t.Errorf("ResolveProfile(review) = %q, want 'review-prof'", got)
	}
	if got := cfg.ResolveProfile("code", "").Name; got != "code-prof" {
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
	if got := cfg.ResolveProfile("report", "security").Name; got != "security-prof" {
		t.Errorf("ResolveProfile(report, security) = %q, want 'security-prof'", got)
	}
	if got := cfg.ResolveProfile("report", "other").Name; got != "proj-default" {
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
	if got := cfg.ResolveProfile("review", "security").Name; got != "sec-prof" {
		t.Errorf("role-specific should win, got %q", got)
	}
	// Action-specific supervisor wins over defaults
	if got := cfg.ResolveProfile("review", "other").Name; got != "review-sup" {
		t.Errorf("review supervisor should win, got %q", got)
	}
	// Supervisor default wins over project default
	if got := cfg.ResolveProfile("exec", "").Name; got != "sup" {
		t.Errorf("supervisor default should win, got %q", got)
	}
}

func TestResolveProfileSource(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{DefaultProfile: "proj"},
		Supervisor: SupervisorConfig{
			CodeProfile:           "code-prof",
			CodeSupervisorProfile: "code-sup",
		},
		Profiles: ProfilesConfig{Roles: map[string]string{"security": "sec-prof"}},
	}
	cases := []struct {
		got        ProfileChoice
		name, from string
	}{
		{cfg.ResolveProfile("report", "security"), "sec-prof", `role "security" profile`},
		{cfg.ResolveProfile("code", ""), "code-prof", "supervisor.code_profile"},
		{cfg.ResolveProfile("report", ""), "proj", "project.default_profile"},
		{cfg.ResolveSupervisorProfile("code"), "code-sup", "supervisor.code_supervisor_profile"},
		{Config{}.ResolveProfile("", ""), "default", ProfileSourceBuiltin},
	}
	for _, c := range cases {
		if c.got.Name != c.name || c.got.Source != c.from {
			t.Errorf("got %+v, want name=%q source=%q", c.got, c.name, c.from)
		}
	}
}

func TestValidateProjectName(t *testing.T) {
	for _, ok := range []string{"ateam", "my-project", "svc_2", "a.b", "x"} {
		if err := ValidateProjectName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "My Project", "-lead", ".hidden", "a/b", "Caps", "a\tb"} {
		if err := ValidateProjectName(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
