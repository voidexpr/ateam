package assembler

import (
	"strings"
	"testing"
)

func TestValidateRoleName(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantErr     bool
		wantErrSubs string
	}{
		{"plain", "security", false, ""},
		{"dotted", "code.bugs", false, ""},
		{"deeper dotted", "project.security.audit", false, ""},
		{"with underscore inside", "code_bugs", false, ""},
		{"with hyphen", "code-bugs", false, ""},

		{"empty", "", true, "empty"},
		{"leading underscore", "_security", true, "reserved"},
		{"ends in .pre", "code.pre", true, "ambiguous"},
		{"ends in .post", "code.post", true, "ambiguous"},
		{"ends in .pre after dots", "project.code.pre", true, "ambiguous"},

		// Words containing pre/post but not as dot-suffix are fine.
		{"preamble", "preamble", false, ""},
		{"postscript", "postscript", false, ""},
		{"code.preamble", "code.preamble", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRoleName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateRoleName(%q) succeeded, want error containing %q", tc.in, tc.wantErrSubs)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubs) {
					t.Fatalf("ValidateRoleName(%q) error = %v, want substring %q", tc.in, err, tc.wantErrSubs)
				}
			} else if err != nil {
				t.Fatalf("ValidateRoleName(%q) unexpected error: %v", tc.in, err)
			}
		})
	}
}
