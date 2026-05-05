package display

import (
	"testing"
	"time"
)

func TestFmtCost(t *testing.T) {
	cases := []struct {
		cost float64
		want string
	}{
		{0, ""},
		{-1, ""},
		{0.005, "$0.01"},
		{1.5, "$1.50"},
	}
	for _, c := range cases {
		if got := FmtCost(c.cost); got != c.want {
			t.Errorf("FmtCost(%v) = %q, want %q", c.cost, got, c.want)
		}
	}
}

func TestFmtTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, ""},
		{-1, ""},
		{500, "500"},
		{1500, "1.5K"},
		{1_500_000, "1.5M"},
	}
	for _, c := range cases {
		if got := FmtTokens(c.n); got != c.want {
			t.Errorf("FmtTokens(%v) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFmtRFC3339AsTimestamp(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"2026-05-04T20:57:13Z", "2026-05-04_20-57-13"},
		{"not-a-date", "not-a-date"},
	}
	for _, c := range cases {
		if got := FmtRFC3339AsTimestamp(c.input); got != c.want {
			t.Errorf("FmtRFC3339AsTimestamp(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{2048, "2.0KB"},
		{1024 * 1024, "1.0MB"},
		{3 * 1024 * 1024, "3.0MB"},
	}
	for _, c := range cases {
		if got := FmtBytes(c.n); got != c.want {
			t.Errorf("FmtBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFmtDateAge(t *testing.T) {
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, ""},
	}
	for _, c := range cases {
		if got := FmtDateAge(c.t); got != c.want {
			t.Errorf("FmtDateAge(%v) = %q, want %q", c.t, got, c.want)
		}
	}

	// Non-zero time produces a non-empty string with date prefix (01/02 format).
	past := time.Now().Add(-48 * time.Hour)
	got := FmtDateAge(past)
	if got == "" {
		t.Error("FmtDateAge(past) returned empty string")
	}
}
