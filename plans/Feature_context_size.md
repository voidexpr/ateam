# Plan: Track Context Size for Agent Runs

## Context

Agent runs currently track token counts (input, output, cache read/write) only from the final `result` event. But the Claude CLI's `stream-json` format already sends **per-turn usage data in every `assistant` event** — including `input_tokens` which represents the current context size at each API call. The `result` event also includes `modelUsage` with `contextWindow` (e.g., 200000). None of this is currently parsed.

This plan adds context size tracking as both a real-time progress indicator and an end-of-run metric. It answers: "how full is the context window?" — useful for understanding run cost, complexity, and risk of hitting limits.

## Implementation

### 1. Parse per-turn usage from stream events

**`internal/streamutil/events.go`** — Add `Usage` struct to `AssistantEvent.Message` and `ModelUsage` map to `ResultEvent`:

```go
// In AssistantEvent.Message:
Usage struct {
    InputTokens              int `json:"input_tokens"`
    OutputTokens             int `json:"output_tokens"`
    CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
    CacheReadInputTokens     int `json:"cache_read_input_tokens"`
} `json:"usage"`

// In ResultEvent:
ModelUsage map[string]struct {
    ContextWindow   int `json:"contextWindow"`
    MaxOutputTokens int `json:"maxOutputTokens"`
} `json:"modelUsage"`
```

No parser changes needed — `json.Unmarshal` handles new fields automatically.

### 2. Propagate through agent StreamEvent

**`internal/agent/agent.go`** — Add to `StreamEvent`:
- `ContextTokens int` — input_tokens from the latest assistant turn (current context size)
- `ContextWindow int` — model's max context from result event

**`internal/agent/claude.go`** — Two changes:
- `"assistant"` case: read `ast.Message.Usage.InputTokens`, set on StreamEvent. Emit context-only assistant event when there's usage data but no text (tool_use-only turns).
- `"result"` case: extract max `contextWindow` from `res.ModelUsage`, set on StreamEvent.

### 3. Track in runner event loop

**`internal/runner/runner.go`**:

- Add to `RunProgress`: `ContextTokens int`, `ContextWindow int`
- Add to `RunSummary`: `PeakContextTokens int`, `ContextWindow int`
- In event loop: track `peakContextTokens` (max of all `ev.ContextTokens`), capture `contextWindow` from result event
- `emitProgress` closure captures and sends these values
- `finalizeCall` passes them to `CallDB.UpdateCall`

**`internal/runner/parse_stream.go`** — Add `ContextWindow int` to `ResultLine`, extract from `ResultEvent.ModelUsage` in `parseClaudeDisplay`. Update `scanStreamFileForResult` fallback path to propagate it.

### 4. Store in database

**`internal/calldb/calldb.go`**:
- Add `peak_context_tokens INTEGER` and `context_window INTEGER` columns via existing migration pattern (ALTER TABLE ADD COLUMN)
- Add fields to `CallResult` struct
- Update `UpdateCall` SQL

**`internal/calldb/queries.go`**:
- Add `PeakContextTokens`, `ContextWindow` to `RecentRow`
- Update `recentCols` and `scanRecentRow`

### 5. CLI display

**`cmd/run.go`**:

`printProgress` — append context info to tool phase:
```
[security] tool: Read (/path/file.go) (5 total, 32s, ctx: 45K/23%)
```

`printRunSummary` — add context line:
```
  Context: 45.2K / 200.0K (23%)
```

**`cmd/pool_status.go`** — append `ctx:45K` to pool status detail when available.

### 6. Web UI

**`internal/web/server.go`** — Add `fmtPercent` template func.

**`internal/web/templates/runs_table.html`** — Add "Context" column showing `{peak} / {pct}%`.

**`internal/web/templates/run.html`** — Add "Peak Context" row to run detail table.

## Key files to modify

| Layer | File |
|-------|------|
| Stream parsing | `internal/streamutil/events.go` |
| Agent normalization | `internal/agent/agent.go`, `internal/agent/claude.go` |
| Runner | `internal/runner/runner.go`, `internal/runner/parse_stream.go` |
| Database | `internal/calldb/calldb.go`, `internal/calldb/queries.go` |
| CLI display | `cmd/run.go`, `cmd/pool_status.go` |
| Web UI | `internal/web/server.go`, `internal/web/templates/runs_table.html`, `internal/web/templates/run.html` |

## Notes

- **Codex agent**: doesn't emit per-turn usage or context window — fields stay zero, all display code uses `> 0` guards
- **Backward compat**: existing DB rows get 0/NULL for new columns via COALESCE in queries
- **No new channel pressure**: context data piggybacks on existing progress events
- **Multiple models in `modelUsage`**: take max `contextWindow` (in practice there's only one)

## Verification

1. `make build` — compiles
2. `make test` — existing tests pass
3. Run `ateam run` with a Claude agent, verify:
   - Progress output shows `ctx: XK/Y%` on tool lines
   - Summary shows `Context: XK / YK (Z%)`
4. Check web UI `/runs` table shows Context column
5. Check individual run detail page shows Peak Context
6. Verify DB: `sqlite3 .ateamorg/state.sqlite "SELECT peak_context_tokens, context_window FROM agent_execs ORDER BY id DESC LIMIT 5"`
