package web

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/runner"
)

// HistoryEntry represents a single archived file.
type HistoryEntry struct {
	Filename    string
	Timestamp   time.Time
	Kind        string // "report", "review", "report_prompt", "review_prompt", "code_management_prompt", "run_prompt"
	Path        string // absolute path
	CostUSD     float64
	TotalTokens int64
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

// fetchRunCosts returns cost/token data for all runs matching action+role.
func fetchRunCosts(db *calldb.CallDB, action, role string) map[string]calldb.RunCost {
	if db == nil {
		return nil
	}
	costs, err := db.RunCostByActionRole(action, role)
	if err != nil {
		return nil
	}
	return costs
}

// enrichHistoryCost fills in CostUSD and TotalTokens on history entries
// using a pre-fetched cost map.
func enrichHistoryCost(entries []HistoryEntry, costs map[string]calldb.RunCost) {
	for i := range entries {
		key := entries[i].Timestamp.Format(runner.TimestampFormat)
		if rc, ok := costs[key]; ok {
			entries[i].CostUSD = rc.CostUSD
			entries[i].TotalTokens = rc.TotalTokens
		}
	}
}

// latestRunCost returns cost/tokens for the most recent entry in the cost map.
func latestRunCost(costs map[string]calldb.RunCost) (float64, int64) {
	var best string
	var rc calldb.RunCost
	for k, v := range costs {
		if k > best {
			best = k
			rc = v
		}
	}
	return rc.CostUSD, rc.TotalTokens
}

// CodeSession represents a task group session from calldb.
type CodeSession struct {
	TaskGroup string
	Timestamp time.Time
	Kind      string // "code" or "report"
	RunCount  int
	TotalCost float64
	Tokens    int64
}
