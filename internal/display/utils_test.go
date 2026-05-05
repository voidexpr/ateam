package display

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		max  int
		want string
	}{
		{"", 10, ""},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello…"},
		// multi-byte rune boundary: "日本語" is 3 runes × 3 bytes = 9 bytes
		// max=4 can't fit the second rune (bytes 3-5), so only first rune fits
		{"日本語", 4, "日…"},
		// max=3 fits exactly the first rune, no ellipsis needed... wait, len("日本語")=9 > 3
		{"日本語", 3, "日…"},
		// max=1 can't fit any rune (each is 3 bytes), cut==0 → returns "…"
		{"日本語", 1, "…"},
	}
	for _, c := range cases {
		if got := Truncate(c.s, c.max); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{59 * time.Second, "59s"},
		{90 * time.Second, "1m30s"},
		{2*time.Minute + 0*time.Second, "2m"},
		{3*time.Minute + 45*time.Second, "3m45s"},
		{time.Hour, "1h0m"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{25*time.Hour + 5*time.Minute, "25h5m"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.d); got != c.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestParseTimestampPrefix(t *testing.T) {
	valid := "2026-05-04_20-57-13"
	wantTime, _ := time.ParseInLocation(TimestampFormat, valid, time.Local)

	cases := []struct {
		name    string
		input   string
		wantOK  bool
		wantVal time.Time
	}{
		{"short input", "2026-05", false, time.Time{}},
		{"valid prefix", valid, true, wantTime},
		{"valid prefix with suffix", valid + "_extra", true, wantTime},
		{"invalid format", "not-a-timestamp-here!", false, time.Time{}},
	}
	for _, c := range cases {
		got, ok := ParseTimestampPrefix(c.input)
		if ok != c.wantOK {
			t.Errorf("ParseTimestampPrefix(%q) ok=%v, want %v", c.input, ok, c.wantOK)
			continue
		}
		if c.wantOK && !got.Equal(c.wantVal) {
			t.Errorf("ParseTimestampPrefix(%q) = %v, want %v", c.input, got, c.wantVal)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	cases := []struct {
		input string
		want  string
	}{
		{"~/foo", home + "/foo"},
		{"~/foo/bar", home + "/foo/bar"},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", "~"},
	}
	for _, c := range cases {
		got := ExpandHome(c.input)
		// Normalize separators for comparison
		if !strings.EqualFold(got, c.want) && got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.input, got, c.want)
		}
		// Simpler direct check
		if got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
