package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionStats is the per-run summary mined from a Codex rollout JSONL written
// to $CODEX_HOME/sessions/<date>/. Codex writes one such file per interactive
// session; we read ours back after waitIdle to recover token usage that the
// TUI never surfaces through the pane.
type SessionStats struct {
	SessionID          string
	Model              string
	InputTokens        int
	OutputTokens       int
	CachedInputTokens  int
	ReasoningTokens    int
	TotalTokens        int
	ContextWindow      int
	DurationMS         int64
	TimeToFirstTokenMS int64
	LastAgentMessage   string
	SessionLogPath     string
	TaskCompleteFound  bool
	TokenCountFound    bool
}

// FindSessionLog locates the most recent rollout-*.jsonl under codexHome whose
// session_meta.payload.cwd matches workdir AND whose session_meta.payload
// timestamp is at or after `since`. Returns the empty string (no error) if
// no matching file is found.
//
// We match on cwd + timestamp because Codex doesn't accept a session-ID hint
// from the CLI; this is the most-specific signal available.
//
// LIMITATION: two codex-tmux runs that start in the same workdir within the
// 5-second slack window may select the wrong file. The "latest by timestamp"
// tiebreaker biases toward the most recent run, so Run A may end up reading
// Run B's tokens. Concurrent runs in different workdirs are unaffected.
// Mitigation if this becomes a real issue: surface a per-EXEC_ID marker into
// the prompt body that codex echoes into its session log, then prefer files
// whose first user_message contains that marker.
func FindSessionLog(codexHome, workdir string, since time.Time) (string, error) {
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		codexHome = filepath.Join(home, ".codex")
	}
	root := filepath.Join(codexHome, "sessions")
	// Walk only today's and yesterday's directories — codex puts the file
	// under sessions/YYYY/MM/DD/ and runs we care about are very recent.
	candidates := candidateDirs(root, since)

	type entry struct {
		path string
		meta sessionMeta
	}
	var matches []entry
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "rollout-") || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Quick mtime filter — files written before `since` can never
			// be ours. Use a 5s slack for clock skew between Codex's
			// timestamp and our wall clock.
			if info.ModTime().Before(since.Add(-5 * time.Second)) {
				continue
			}
			meta, err := readSessionMeta(path)
			if err != nil {
				continue
			}
			if !pathEqual(meta.CWD, workdir) {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, meta.Timestamp)
			if err != nil {
				ts = info.ModTime()
			}
			if ts.Before(since.Add(-5 * time.Second)) {
				continue
			}
			matches = append(matches, entry{path: path, meta: meta})
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	// Sort by session_meta timestamp ascending; the latest match is the
	// most likely candidate for "our" session.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].meta.Timestamp < matches[j].meta.Timestamp
	})
	return matches[len(matches)-1].path, nil
}

// ReadSessionStats fully reads a rollout JSONL and returns the aggregated
// token usage, model, duration, and completion state. Safe to call after the
// session has finished; if called too early (no task_complete yet) the result
// reports whatever was written so far and TaskCompleteFound=false.
func ReadSessionStats(path string) (SessionStats, error) {
	stats := SessionStats{SessionLogPath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec rolloutRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "session_meta":
			var meta sessionMeta
			if err := json.Unmarshal(rec.Payload, &meta); err == nil {
				stats.SessionID = meta.ID
			}
		case "event_msg":
			var pe payloadEvent
			if err := json.Unmarshal(rec.Payload, &pe); err != nil {
				continue
			}
			switch pe.Type {
			case "task_started":
				if pe.ModelContextWindow > 0 {
					stats.ContextWindow = pe.ModelContextWindow
				}
			case "token_count":
				if pe.Info.TotalTokenUsage.TotalTokens > 0 || pe.Info.TotalTokenUsage.InputTokens > 0 {
					stats.InputTokens = pe.Info.TotalTokenUsage.InputTokens
					stats.OutputTokens = pe.Info.TotalTokenUsage.OutputTokens
					stats.CachedInputTokens = pe.Info.TotalTokenUsage.CachedInputTokens
					stats.ReasoningTokens = pe.Info.TotalTokenUsage.ReasoningOutputTokens
					stats.TotalTokens = pe.Info.TotalTokenUsage.TotalTokens
					stats.TokenCountFound = true
				}
				if pe.Info.ModelContextWindow > 0 {
					stats.ContextWindow = pe.Info.ModelContextWindow
				}
			case "task_complete":
				stats.DurationMS = pe.DurationMS
				stats.TimeToFirstTokenMS = pe.TimeToFirstTokenMS
				stats.LastAgentMessage = pe.LastAgentMessage
				stats.TaskCompleteFound = true
			}
		case "turn_context":
			var tc turnContext
			if err := json.Unmarshal(rec.Payload, &tc); err == nil && tc.Model != "" && stats.Model == "" {
				stats.Model = tc.Model
			}
		}
	}
	if err := sc.Err(); err != nil {
		return stats, fmt.Errorf("read session log: %w", err)
	}
	return stats, nil
}

// candidateDirs returns the dated subdirectories that could plausibly contain
// a session started at `since`. We check today + yesterday (in case the run
// straddles midnight UTC vs local).
func candidateDirs(root string, since time.Time) []string {
	dates := []time.Time{since, since.Add(-24 * time.Hour), since.Add(24 * time.Hour)}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		dir := filepath.Join(root, d.Format("2006"), d.Format("01"), d.Format("02"))
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

// readSessionMeta parses just the first JSONL line of a rollout file (always a
// session_meta record) without scanning the rest.
func readSessionMeta(path string) (sessionMeta, error) {
	var meta sessionMeta
	f, err := os.Open(path)
	if err != nil {
		return meta, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 1024*1024)
	if !sc.Scan() {
		return meta, fmt.Errorf("session log empty: %s", path)
	}
	var rec rolloutRecord
	if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
		return meta, err
	}
	if rec.Type != "session_meta" {
		return meta, fmt.Errorf("first record is %q, want session_meta", rec.Type)
	}
	if err := json.Unmarshal(rec.Payload, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// pathEqual compares two paths after symlink+canonical normalization. Codex
// canonicalizes its cwd before writing session_meta; the workdir we pass in
// may not be canonical (e.g. it could go through /var → /private/var on
// macOS).
func pathEqual(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}

// rolloutRecord is the JSONL envelope shared by every record in a Codex
// rollout file. The payload schema varies by Type and is decoded separately.
type rolloutRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	CWD        string `json:"cwd"`
	Originator string `json:"originator"`
	CLIVersion string `json:"cli_version"`
}

type turnContext struct {
	Model string `json:"model"`
}

type tokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

type tokenCountInfo struct {
	TotalTokenUsage    tokenUsage `json:"total_token_usage"`
	LastTokenUsage     tokenUsage `json:"last_token_usage"`
	ModelContextWindow int        `json:"model_context_window"`
}

// payloadEvent is the union of `event_msg` payload variants we care about.
// Fields irrelevant to a given event type are simply absent in the JSON and
// decode to zero values.
type payloadEvent struct {
	Type               string         `json:"type"`
	ModelContextWindow int            `json:"model_context_window"`
	Info               tokenCountInfo `json:"info"`
	DurationMS         int64          `json:"duration_ms"`
	TimeToFirstTokenMS int64          `json:"time_to_first_token_ms"`
	LastAgentMessage   string         `json:"last_agent_message"`
}
