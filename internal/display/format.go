// Package display provides output formatting utilities including timestamp formatting and display helpers.
package display

import (
	"fmt"
	"time"
)

// TimestampFormat is the canonical layout for ateam timestamps in file names and display.
const TimestampFormat = "2006-01-02_15-04-05"

// FmtRFC3339AsTimestamp parses an RFC3339 string and reformats it using TimestampFormat.
// Returns the original string on parse error, or "" for empty input.
func FmtRFC3339AsTimestamp(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format(TimestampFormat)
}

func FmtTokens(n int64) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func FmtCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", cost)
}

// FmtBytes renders a byte count in human units (B, KB, MB).
func FmtBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func FmtDateAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	date := t.Format("01/02")
	age := time.Since(t)
	switch {
	case age < time.Minute:
		return date + " (just now)"
	case age < time.Hour:
		return fmt.Sprintf("%s (%dm ago)", date, int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%s (%dh ago)", date, int(age.Hours()))
	default:
		days := int(age.Hours()) / 24
		return fmt.Sprintf("%s (%dd ago)", date, days)
	}
}
