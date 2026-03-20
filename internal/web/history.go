package web

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/runner"
)

// HistoryEntry represents a single archived file.
type HistoryEntry struct {
	Filename  string
	Timestamp time.Time
	Kind      string // "report", "review", "report_prompt", "review_prompt", "code_management_prompt", "run_prompt"
	Path      string // absolute path
}

// HistoryGroup groups history entries by timestamp (same session).
type HistoryGroup struct {
	Timestamp time.Time
	Label     string // formatted timestamp
	Entries   []HistoryEntry
}

// discoverHistory reads a history directory and returns entries sorted newest-first.
func discoverHistory(dir string) []HistoryEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var result []HistoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		he := parseHistoryFilename(e.Name(), filepath.Join(dir, e.Name()))
		if !he.Timestamp.IsZero() {
			result = append(result, he)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.After(result[j].Timestamp)
	})
	return result
}

// parseHistoryFilename extracts timestamp and kind from "2026-03-14_00-20-28.review_prompt.md".
func parseHistoryFilename(name, path string) HistoryEntry {
	// Expected: YYYY-MM-DD_HH-MM-SS.kind.md
	// Timestamp is first 19 chars
	if len(name) < 20 {
		return HistoryEntry{}
	}
	tsStr := name[:19]
	t, err := time.ParseInLocation(runner.TimestampFormat, tsStr, time.Local)
	if err != nil {
		return HistoryEntry{}
	}

	rest := name[20:] // skip the dot after timestamp
	kind := strings.TrimSuffix(rest, ".md")

	return HistoryEntry{
		Filename:  name,
		Timestamp: t,
		Kind:      kind,
		Path:      path,
	}
}

// groupHistory groups entries by timestamp into sessions.
func groupHistory(entries []HistoryEntry) []HistoryGroup {
	byTS := map[string]*HistoryGroup{}
	var order []string

	for _, e := range entries {
		key := e.Timestamp.Format(runner.TimestampFormat)
		g, ok := byTS[key]
		if !ok {
			g = &HistoryGroup{
				Timestamp: e.Timestamp,
				Label:     e.Timestamp.Format("2006-01-02 15:04:05"),
			}
			byTS[key] = g
			order = append(order, key)
		}
		g.Entries = append(g.Entries, e)
	}

	var groups []HistoryGroup
	for _, key := range order {
		groups = append(groups, *byTS[key])
	}
	return groups
}

// filterHistoryByKind returns only entries matching the given kind.
func filterHistoryByKind(entries []HistoryEntry, kind string) []HistoryEntry {
	var result []HistoryEntry
	for _, e := range entries {
		if e.Kind == kind {
			result = append(result, e)
		}
	}
	return result
}

// CodeSession represents a code session from calldb task_group data.
type CodeSession struct {
	TaskGroup string
	Timestamp time.Time
	Label     string
	RunCount  int
	TotalCost float64
	Tokens    int64
}
