package cmd

import (
	"strings"
	"testing"
)

func TestParseAutoRolesOutput(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantRoles []string
		// wantRationaleSubstring lets the test stay robust to whitespace
		// trimming details. Empty = no rationale check.
		wantRationaleSubstring string
		wantErr                bool
	}{
		{
			name: "well-formed with roles",
			input: `# Recommendation

Recent changes touched the auth flow and added new test branches.

` + "```" + `
ateam report --roles code.bugs,test.recent
ateam report --roles code.bugs,test.recent --review
ateam all --roles code.bugs,test.recent
` + "```" + `

RECOMMENDED_ROLES: code.bugs,test.recent
`,
			wantRoles:              []string{"code.bugs", "test.recent"},
			wantRationaleSubstring: "Recent changes touched the auth flow",
		},
		{
			name: "empty marker means no work",
			input: `Everything is fresh.

RECOMMENDED_ROLES:
`,
			wantRoles:              nil,
			wantRationaleSubstring: "Everything is fresh.",
		},
		{
			name:    "missing marker is an error",
			input:   "I forgot to include the marker line.",
			wantErr: true,
		},
		{
			name: "trailing whitespace on marker is trimmed",
			input: `Rationale.

RECOMMENDED_ROLES:   code.bugs ,  test.gaps   ` + "\t" + `
`,
			wantRoles:              []string{"code.bugs", "test.gaps"},
			wantRationaleSubstring: "Rationale.",
		},
		{
			name: "duplicate marker lines: last wins",
			input: `Rationale.

RECOMMENDED_ROLES: stale.role

RECOMMENDED_ROLES: code.bugs
`,
			wantRoles:              []string{"code.bugs"},
			wantRationaleSubstring: "Rationale.",
		},
		{
			name: "single role",
			input: `Single recommendation.

RECOMMENDED_ROLES: code.recent
`,
			wantRoles: []string{"code.recent"},
		},
		{
			name: "empty entries between commas are dropped",
			input: `Rationale.

RECOMMENDED_ROLES: code.bugs,,test.recent,
`,
			wantRoles: []string{"code.bugs", "test.recent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rationale, roles, err := parseAutoRolesOutput(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (rationale=%q roles=%v)", rationale, roles)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(roles) != len(tc.wantRoles) {
				t.Errorf("roles = %v, want %v", roles, tc.wantRoles)
			} else {
				for i, r := range roles {
					if r != tc.wantRoles[i] {
						t.Errorf("roles[%d] = %q, want %q", i, r, tc.wantRoles[i])
					}
				}
			}
			if tc.wantRationaleSubstring != "" && !strings.Contains(rationale, tc.wantRationaleSubstring) {
				t.Errorf("rationale missing substring %q; got:\n%s", tc.wantRationaleSubstring, rationale)
			}
		})
	}
}
