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

// HistoryEntry represents one past run, surfaced either from a legacy
// timestamped history file or from an agent_execs row.
type HistoryEntry struct {
	Filename    string
	Timestamp   time.Time
	Kind        string // "report", "review", "report_prompt", "review_prompt", "code_management_prompt", "run_prompt"
	Path        string // absolute path
	CostUSD     float64
	TotalTokens int64
	ExecID      int64 // 0 for legacy entries discovered by filename scan only
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

// historyFromDB returns history entries for a (role, action) pair sourced from
// agent_execs. Each row becomes one HistoryEntry whose Path points at the
// runtime/<exec_id>/ output (when output_file is populated). Cost/tokens are
// filled from the same row. Sorted newest-first.
func historyFromDB(db *calldb.CallDB, projectDir, action, role, kind string) []HistoryEntry {
	if db == nil {
		return nil
	}
	rows, err := db.RecentRuns(calldb.RecentFilter{Role: role, Action: action, Limit: 100})
	if err != nil {
		return nil
	}
	out := make([]HistoryEntry, 0, len(rows))
	for _, r := range rows {
		ts, err := time.Parse(time.RFC3339, r.StartedAt)
		if err != nil {
			continue
		}
		path := ""
		if r.OutputFile != "" {
			path = filepath.Join(projectDir, r.OutputFile)
		}
		out = append(out, HistoryEntry{
			Timestamp:   ts.In(time.Local),
			Kind:        kind,
			Path:        path,
			CostUSD:     r.CostUSD,
			TotalTokens: int64(r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens),
			ExecID:      r.ID,
		})
	}
	return out
}

// mergeHistory combines DB-sourced entries (preferred) with legacy filename
// scan results, deduplicating by exec_id when the filename embeds it.
func mergeHistory(dbEntries, legacy []HistoryEntry) []HistoryEntry {
	seen := make(map[int64]bool, len(dbEntries))
	for _, e := range dbEntries {
		if e.ExecID > 0 {
			seen[e.ExecID] = true
		}
	}
	merged := append([]HistoryEntry(nil), dbEntries...)
	for _, e := range legacy {
		if e.ExecID > 0 && seen[e.ExecID] {
			continue
		}
		merged = append(merged, e)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp.After(merged[j].Timestamp)
	})
	return merged
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

// CodeSession represents a batch session from calldb.
type CodeSession struct {
	Batch     string
	Timestamp time.Time
	Kind      string // "code" or "report"
	RunCount  int
	TotalCost float64
	Tokens    int64
}
