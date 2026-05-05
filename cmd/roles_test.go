package cmd

import (
	"testing"

	"github.com/ateam/internal/config"
)

func TestRoleStatusDefaultsToEnabled(t *testing.T) {
	cases := []struct {
		name        string
		configRoles map[string]string
		role        string
		want        string
	}{
		{
			name:        "missing entry defaults to on",
			configRoles: map[string]string{"other": "on"},
			role:        "security",
			want:        config.RoleEnabled,
		},
		{
			name:        "nil config defaults to on",
			configRoles: nil,
			role:        "security",
			want:        config.RoleEnabled,
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
			name:        "explicit off is preserved",
			configRoles: map[string]string{"security": "off"},
			role:        "security",
			want:        "off",
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
