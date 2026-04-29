package display

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// FormatDuration returns a human-readable duration string.
// >=1h shows "XhYm" (minute precision); shorter shows seconds.
func FormatDuration(d time.Duration) string {
	if d >= time.Hour {
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%dm", h, m)
	}
	rounded := d.Round(time.Second)
	if rounded < time.Minute {
		return fmt.Sprintf("%ds", int(rounded/time.Second))
	}
	minutes := int(rounded / time.Minute)
	seconds := int((rounded % time.Minute) / time.Second)
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// ParseTimestampPrefix parses the leading TimestampFormat prefix
// ("YYYY-MM-DD_HH-MM-SS") from a filename. Returns ok=false when the
// name is too short or doesn't match. Local timezone is used.
func ParseTimestampPrefix(name string) (time.Time, bool) {
	if len(name) < len(TimestampFormat) {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(TimestampFormat, name[:len(TimestampFormat)], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ExpandHome replaces a leading ~/ with the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// Truncate shortens s to at most max bytes on a rune boundary, appending "…"
// when it had to cut. Returns "" for max<=0 and the original s when it fits.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := 0
	for i, r := range s {
		end := i + utf8.RuneLen(r)
		if end > max {
			break
		}
		cut = end
	}
	if cut == 0 {
		return "…"
	}
	return s[:cut] + "…"
}
