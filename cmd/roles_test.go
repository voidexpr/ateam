package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
)

func saveRolesGlobals() func() {
	enabled, available, docs := rolesEnabled, rolesAvailable, rolesDocs
	return func() {
		rolesEnabled = enabled
		rolesAvailable = available
		rolesDocs = docs
	}
}

// TestRunRolesDefaultListing exercises the default (--available) path:
// `ateam roles` should print a table that includes the enabled role.
func TestRunRolesDefaultListing(t *testing.T) {
	defer saveRolesGlobals()()

	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	rolesEnabled = false
	rolesAvailable = false
	rolesDocs = false

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runRoles(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runRoles: %v", runErr)
	}
	for _, want := range []string{"ROLE", "STATUS", "testing_basic"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in roles output:\n%s", want, out)
		}
	}
}

// TestRunRolesDocs covers the --docs path, which is a project-independent
// markdown dump used to regenerate ROLES.md.
func TestRunRolesDocs(t *testing.T) {
	defer saveRolesGlobals()()

	rolesDocs = true

	var runErr error
	out := captureStdout(t, func() {
		runErr = runRoles(nil, nil)
	})
	if runErr != nil {
		t.Fatalf("runRoles --docs: %v", runErr)
	}
	for _, want := range []string{"# ATeam Built-in Roles", "| Role |"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in --docs output:\n%s", want, out)
		}
	}
}

func TestRoleStatus(t *testing.T) {
	cases := []struct {
		name        string
		configRoles map[string]string
		role        string
		want        string
	}{
		{
			name:        "missing from config defaults to off",
			configRoles: map[string]string{"other": "on"},
			role:        "security",
			want:        config.RoleDisabled,
		},
		{
			name:        "nil config defaults to off",
			configRoles: nil,
			role:        "security",
			want:        config.RoleDisabled,
		},
		{
			name:        "custom role missing from config defaults to off",
			configRoles: map[string]string{"other": "on"},
			role:        "my_custom_role",
			want:        config.RoleDisabled,
		},
		{
			name:        "explicit on",
			configRoles: map[string]string{"security": "on"},
			role:        "security",
			want:        config.RoleEnabled,
		},
		{
			name:        "legacy enabled normalizes to on",
			configRoles: map[string]string{"security": "enabled"},
			role:        "security",
			want:        config.RoleEnabled,
		},
		{
			name:        "explicit off",
			configRoles: map[string]string{"security": "off"},
			role:        "security",
			want:        config.RoleDisabled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := roleStatus(tc.configRoles, tc.role); got != tc.want {
				t.Errorf("roleStatus(%v, %q) = %q, want %q", tc.configRoles, tc.role, got, tc.want)
			}
		})
	}
}

func TestEscapeTableCell(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain text", "plain text"},
		{"local | module | architecture", `local \| module \| architecture`},
		{"multi\nline", "multi line"},
		{"|pipe at start|", `\|pipe at start\|`},
		{"", ""},
	}
	for _, c := range cases {
		got := escapeTableCell(c.in)
		if got != c.want {
			t.Errorf("escapeTableCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
