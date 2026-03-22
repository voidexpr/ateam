package web

import (
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/runner"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Project", "my-project"},
		{"hello", "hello"},
		{"UPPER CASE", "upper-case"},
		{"with_underscores", "with_underscores"},
		{"special!@#chars", "specialchars"},
		{"  spaces  ", "--spaces--"},
		{"", "project"},
		{"!!!???", "project"},
		{"a", "a"},
		{"hello-world", "hello-world"},
		{"123-numbers", "123-numbers"},
		{"mixed 123 input!", "mixed-123-input"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFmtTokensI64(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, ""},
		{-1, ""},
		{1, "1"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{10000, "10.0K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{10000000, "10.0M"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := fmtTokensI64(tt.input)
			if got != tt.want {
				t.Errorf("fmtTokensI64(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFuncMapFmtCost(t *testing.T) {
	fm := funcMap()
	fmtCost := fm["fmtCost"].(func(float64) string)

	tests := []struct {
		input float64
		want  string
	}{
		{0, ""},
		{-1, ""},
		{0.50, "$0.50"},
		{1.0, "$1.00"},
		{12.345, "$12.35"},
		{0.001, "$0.00"},
	}
	for _, tt := range tests {
		got := fmtCost(tt.input)
		if got != tt.want {
			t.Errorf("fmtCost(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFuncMapFmtDuration(t *testing.T) {
	fm := funcMap()
	fmtDuration := fm["fmtDuration"].(func(int64) string)

	tests := []struct {
		input int64
		want  string
	}{
		{0, ""},
		{-100, ""},
		{500, "0s"},
		{1000, "1s"},
		{5000, "5s"},
		{59999, "59s"},
		{60000, "1m0s"},
		{61000, "1m1s"},
		{125000, "2m5s"},
	}
	for _, tt := range tests {
		got := fmtDuration(tt.input)
		if got != tt.want {
			t.Errorf("fmtDuration(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFuncMapFmtTimestamp(t *testing.T) {
	fm := funcMap()
	fmtTimestamp := fm["fmtTimestamp"].(func(string) string)

	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"not-a-timestamp", "not-a-timestamp"},
		{"2026-03-14T10:30:00Z", "03/14 10:30"},
		{"2026-01-01T00:00:00Z", "01/01 00:00"},
	}
	for _, tt := range tests {
		got := fmtTimestamp(tt.input)
		if got != tt.want {
			t.Errorf("fmtTimestamp(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFuncMapFmtTokensInt(t *testing.T) {
	fm := funcMap()
	fmtTokensInt := fm["fmtTokensInt"].(func(int) string)

	if got := fmtTokensInt(5000); got != "5.0K" {
		t.Errorf("fmtTokensInt(5000) = %q, want %q", got, "5.0K")
	}
	if got := fmtTokensInt(0); got != "" {
		t.Errorf("fmtTokensInt(0) = %q, want %q", got, "")
	}
}

func TestParseHistoryFilename(t *testing.T) {
	tests := []struct {
		name string
		path string
		kind string
		zero bool
	}{
		{
			name: "2026-03-14_00-20-28.review_prompt.md",
			path: "/tmp/2026-03-14_00-20-28.review_prompt.md",
			kind: "review_prompt",
		},
		{
			name: "2026-03-14_00-20-28.report.md",
			path: "/tmp/2026-03-14_00-20-28.report.md",
			kind: "report",
		},
		{
			name: "2026-01-01_12-00-00.code_management_prompt.md",
			path: "/some/path",
			kind: "code_management_prompt",
		},
		{
			name: "short.md",
			path: "/tmp/short.md",
			zero: true,
		},
		{
			name: "not-a-date-at-all.report.md",
			path: "/tmp/not-a-date-at-all.report.md",
			zero: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			he := parseHistoryFilename(tt.name, tt.path)
			if tt.zero {
				if !he.Timestamp.IsZero() {
					t.Errorf("expected zero timestamp for %q, got %v", tt.name, he.Timestamp)
				}
				return
			}
			if he.Timestamp.IsZero() {
				t.Fatalf("expected non-zero timestamp for %q", tt.name)
			}
			if he.Kind != tt.kind {
				t.Errorf("kind = %q, want %q", he.Kind, tt.kind)
			}
			if he.Path != tt.path {
				t.Errorf("path = %q, want %q", he.Path, tt.path)
			}
			if he.Filename != tt.name {
				t.Errorf("filename = %q, want %q", he.Filename, tt.name)
			}
		})
	}
}

func TestFilterHistoryByKind(t *testing.T) {
	entries := []HistoryEntry{
		{Kind: "report", Filename: "a.md"},
		{Kind: "review_prompt", Filename: "b.md"},
		{Kind: "report", Filename: "c.md"},
		{Kind: "review", Filename: "d.md"},
	}

	got := filterHistoryByKind(entries, "report")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Filename != "a.md" || got[1].Filename != "c.md" {
		t.Errorf("unexpected filenames: %v, %v", got[0].Filename, got[1].Filename)
	}

	got = filterHistoryByKind(entries, "nonexistent")
	if len(got) != 0 {
		t.Errorf("expected 0 entries for nonexistent kind, got %d", len(got))
	}

	got = filterHistoryByKind(nil, "report")
	if len(got) != 0 {
		t.Errorf("expected 0 entries for nil input, got %d", len(got))
	}
}

func TestLatestRunCost(t *testing.T) {
	costs := map[string]calldb.RunCost{
		"2026-03-14_00-20-28": {CostUSD: 0.10, TotalTokens: 1000},
		"2026-03-15_10-00-00": {CostUSD: 0.25, TotalTokens: 2500},
		"2026-03-13_08-00-00": {CostUSD: 0.05, TotalTokens: 500},
	}

	cost, tokens := latestRunCost(costs)
	if cost != 0.25 {
		t.Errorf("cost = %f, want 0.25", cost)
	}
	if tokens != 2500 {
		t.Errorf("tokens = %d, want 2500", tokens)
	}

	// Empty map
	cost, tokens = latestRunCost(nil)
	if cost != 0 || tokens != 0 {
		t.Errorf("empty map: cost=%f tokens=%d, want 0/0", cost, tokens)
	}
}

func TestEnrichHistoryCost(t *testing.T) {
	ts1, _ := time.ParseInLocation(runner.TimestampFormat, "2026-03-14_00-20-28", time.Local)
	ts2, _ := time.ParseInLocation(runner.TimestampFormat, "2026-03-15_10-00-00", time.Local)

	entries := []HistoryEntry{
		{Timestamp: ts1},
		{Timestamp: ts2},
	}
	costs := map[string]calldb.RunCost{
		"2026-03-14_00-20-28": {CostUSD: 0.10, TotalTokens: 1000},
	}

	enrichHistoryCost(entries, costs)

	if entries[0].CostUSD != 0.10 || entries[0].TotalTokens != 1000 {
		t.Errorf("entry[0]: cost=%f tokens=%d, want 0.10/1000", entries[0].CostUSD, entries[0].TotalTokens)
	}
	if entries[1].CostUSD != 0 || entries[1].TotalTokens != 0 {
		t.Errorf("entry[1]: cost=%f tokens=%d, want 0/0", entries[1].CostUSD, entries[1].TotalTokens)
	}

	// Nil costs map should not panic
	enrichHistoryCost(entries, nil)
}

func TestPromptDir(t *testing.T) {
	tests := []struct {
		action string
		role   string
		want   string
	}{
		{runner.ActionReport, "security", "roles/security/history"},
		{runner.ActionRun, "testing_basic", "roles/testing_basic/history"},
		{runner.ActionReview, "supervisor", "supervisor/history"},
		{runner.ActionCode, "supervisor", "supervisor/history"},
		{"unknown", "any", "supervisor/history"},
	}
	for _, tt := range tests {
		t.Run(tt.action+"/"+tt.role, func(t *testing.T) {
			got := promptDir(tt.action, tt.role)
			if got != tt.want {
				t.Errorf("promptDir(%q, %q) = %q, want %q", tt.action, tt.role, got, tt.want)
			}
		})
	}
}

func TestParseTaskGroupTimestamp(t *testing.T) {
	tests := []struct {
		input string
		zero  bool
		year  int
		month time.Month
		day   int
	}{
		{"code-2026-03-19_00-35-57", false, 2026, time.March, 19},
		{"report-2026-01-15_12-30-00", false, 2026, time.January, 15},
		{"", true, 0, 0, 0},
		{"noprefix", true, 0, 0, 0},
		{"x-invalid-timestamp", true, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseTaskGroupTimestamp(tt.input)
			if tt.zero {
				if !got.IsZero() {
					t.Errorf("expected zero time for %q, got %v", tt.input, got)
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("expected non-zero time for %q", tt.input)
			}
			if got.Year() != tt.year || got.Month() != tt.month || got.Day() != tt.day {
				t.Errorf("parseTaskGroupTimestamp(%q) = %v, want %d-%02d-%02d", tt.input, got, tt.year, tt.month, tt.day)
			}
		})
	}
}
