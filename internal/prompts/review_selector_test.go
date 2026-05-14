package prompts

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
)

// makeTestReports writes a roles/<id>/report.md file per entry under
// projectDir, with the requested mtime. Returns the slice DiscoverReports
// would yield.
func makeTestReports(t *testing.T, projectDir string, entries map[string]time.Time) []RoleReport {
	t.Helper()
	for role, mtime := range entries {
		dir := filepath.Join(projectDir, "roles", role)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, ReportFile)
		if err := os.WriteFile(path, []byte("body for "+role), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverReports(projectDir)
	if err != nil {
		t.Fatalf("DiscoverReports: %v", err)
	}
	return got
}

func roleIDs(rs []RoleReport) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.RoleID
	}
	sort.Strings(out)
	return out
}

func TestReviewSelector_Filter(t *testing.T) {
	now := time.Now()
	mtimes := map[string]time.Time{
		"security":  now.Add(-30 * time.Minute), // fresh
		"docs":      now.Add(-3 * time.Hour),    // older
		"deps":      now.Add(-25 * time.Hour),   // 1d+
		"obsoleted": now.Add(-30 * time.Minute), // fresh but disabled in cfg
	}
	configRoles := map[string]string{
		"security":  "on",
		"docs":      "on",
		"deps":      "on",
		"obsoleted": "off",
	}
	dir := t.TempDir()
	all := makeTestReports(t, dir, mtimes)

	cases := []struct {
		name       string
		sel        ReviewSelector
		want       []string
		wantFunnel ReviewFunnel
	}{
		{
			name: "default enabled-only",
			sel:  ReviewSelector{},
			want: []string{"deps", "docs", "security"},
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 3, RolesMatch: 3, FreshEnough: 3,
				HadEnabled: true,
			},
		},
		{
			name: "include disabled",
			sel:  ReviewSelector{IncludeDisabled: true},
			want: []string{"deps", "docs", "obsoleted", "security"},
			// Enabled=0 because the step was skipped (HadEnabled=false).
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 0, RolesMatch: 4, FreshEnough: 4,
			},
		},
		{
			// --roles is authoritative: the named roles bypass the enabled-only
			// gate. The Roles filter still narrows scope to exactly the named
			// set, so an unknown role name simply matches zero reports.
			name: "explicit roles is authoritative (bypasses enabled-only)",
			sel:  ReviewSelector{Roles: []string{"security", "obsoleted"}},
			want: []string{"obsoleted", "security"},
			wantFunnel: ReviewFunnel{
				// Enabled=0 and HadEnabled=false because the step was skipped.
				Available: 4, Enabled: 0, RolesMatch: 2, FreshEnough: 2,
				UsedRoles: []string{"security", "obsoleted"},
			},
		},
		{
			// --all alongside --roles is redundant but harmless.
			name: "explicit roles + include disabled",
			sel:  ReviewSelector{Roles: []string{"security", "obsoleted"}, IncludeDisabled: true},
			want: []string{"obsoleted", "security"},
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 0, RolesMatch: 2, FreshEnough: 2,
				UsedRoles: []string{"security", "obsoleted"},
			},
		},
		{
			name: "max-age 1h cuts docs and deps",
			sel:  ReviewSelector{MaxAge: time.Hour},
			want: []string{"security"},
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 3, RolesMatch: 3, FreshEnough: 1,
				HadEnabled: true, MaxAge: time.Hour,
			},
		},
		{
			name: "max-age 24h leaves only fresh+1h-3h",
			sel:  ReviewSelector{MaxAge: 24 * time.Hour},
			want: []string{"docs", "security"},
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 3, RolesMatch: 3, FreshEnough: 2,
				HadEnabled: true, MaxAge: 24 * time.Hour,
			},
		},
		{
			// --roles=[docs] is authoritative (enabled-only is skipped); only
			// the max-age window prunes it. HadEnabled=false because --roles
			// was set.
			name: "explicit role then max-age filters to nothing",
			sel:  ReviewSelector{Roles: []string{"docs"}, MaxAge: time.Hour},
			want: nil,
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 0, RolesMatch: 1, FreshEnough: 0,
				MaxAge:    time.Hour,
				UsedRoles: []string{"docs"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, funnel := tc.sel.Filter(all, configRoles)
			gotIDs := roleIDs(got)
			if !slices.Equal(gotIDs, tc.want) {
				t.Errorf("kept = %v, want %v", gotIDs, tc.want)
			}
			if !reflect.DeepEqual(funnel, tc.wantFunnel) {
				t.Errorf("funnel = %+v\n   want %+v", funnel, tc.wantFunnel)
			}
		})
	}
}

func TestAssembleReviewPrompt_EmptyAfterFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	makeTestReports(t, dir, map[string]time.Time{
		"security": now,
		"docs":     now,
	})
	configRoles := map[string]string{"security": "on", "docs": "on"}

	pinfo := ProjectInfoParams{ProjectName: "test", ProjectDir: dir}
	_, err := AssembleReviewPrompt("", dir, pinfo, "", "",
		ReviewSelector{Roles: []string{"security"}, MaxAge: time.Nanosecond},
		configRoles)
	if err == nil {
		t.Fatal("expected ReviewEmptyError, got nil")
	}
	var empty *ReviewEmptyError
	if !errors.As(err, &empty) {
		t.Fatalf("expected *ReviewEmptyError, got %T: %v", err, err)
	}
	if empty.Funnel.Available != 2 || empty.Funnel.RolesMatch != 1 || empty.Funnel.FreshEnough != 0 {
		t.Errorf("funnel mismatch: %+v", empty.Funnel)
	}
}
