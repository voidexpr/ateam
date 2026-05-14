package cmd

import (
	"testing"

	"github.com/ateam/internal/config"
)

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
