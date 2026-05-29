package prompts

import (
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

// TestDiscoverReportsStableOrder verifies DiscoverReports returns reports
// sorted by RoleID. Without the sort, Go's randomized map iteration would make
// downstream review prompts (and their hashes) vary run to run for the same
// report set.
func TestDiscoverReportsStableOrder(t *testing.T) {
	projectDir := t.TempDir()
	now := time.Now()
	makeTestReports(t, projectDir, map[string]time.Time{
		"security":     now,
		"dependencies": now,
		"performance":  now,
		"docs":         now,
	})
	got, err := DiscoverReports(projectDir)
	if err != nil {
		t.Fatalf("DiscoverReports: %v", err)
	}
	ids := make([]string, len(got))
	for i, r := range got {
		ids[i] = r.RoleID
	}
	want := []string{"dependencies", "docs", "performance", "security"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("RoleID order = %v, want %v", ids, want)
	}
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

	// In every case below where --all (IncludeDisabled) or --roles is set,
	// Enabled=0 and HadEnabled=false because the enabled-only step is skipped.
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
			wantFunnel: ReviewFunnel{
				Available: 4, Enabled: 0, RolesMatch: 4, FreshEnough: 4,
			},
		},
		{
			// --roles is authoritative: named roles bypass the enabled-only
			// gate. The Roles filter still narrows scope to exactly the named
			// set, so an unknown name simply matches zero reports.
			name: "explicit roles is authoritative (bypasses enabled-only)",
			sel:  ReviewSelector{Roles: []string{"security", "obsoleted"}},
			want: []string{"obsoleted", "security"},
			wantFunnel: ReviewFunnel{
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

// TestReviewSelectorFilterFunnelOnEmpty covers the funnel computation
// when all reports are filtered out — the same shape consumers
// (assembleReviewV1) turn into a ReviewEmptyError. Tests the funnel
// without going through any of the now-deleted assembler wrappers.
func TestReviewSelectorFilterFunnelOnEmpty(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	makeTestReports(t, dir, map[string]time.Time{
		"security": now,
		"docs":     now,
	})
	configRoles := map[string]string{"security": "on", "docs": "on"}

	all, err := DiscoverReports(dir)
	if err != nil {
		t.Fatalf("DiscoverReports: %v", err)
	}
	selector := ReviewSelector{Roles: []string{"security"}, MaxAge: time.Nanosecond}
	filtered, funnel := selector.Filter(all, configRoles)
	if len(filtered) != 0 {
		t.Fatalf("expected empty filter result, got %d reports", len(filtered))
	}
	if funnel.Available != 2 || funnel.RolesMatch != 1 || funnel.FreshEnough != 0 {
		t.Errorf("funnel mismatch: %+v", funnel)
	}
}
