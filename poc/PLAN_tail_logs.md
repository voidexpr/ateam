# Plan: `ateam tail` command — live stream JSONL formatter

## Context

When roles run (via `ateam report`, `ateam code`, etc.), their output is written to timestamped `*_stream.jsonl` files. Currently there's no way to watch a running role's progress from a separate terminal. The `ateam log` command reads completed stream files, but doesn't tail live ones. The `claude-sandbox.sh` script has a `live_monitor()` function that does exactly this — we want a Go equivalent as `ateam tail`.

## Effort estimate

**Small-medium.** Most infrastructure exists:
- `internal/runner/events.go` — `parseStreamLine()` and all event structs already exist
- `internal/runner/format.go` — `FormatStream()` reads JSONL and formats output (used by `ateam log`)
- `cmd/log.go` — resolves stream file paths via `--role`/`--supervisor`/`--action` flags
- `cmd/run.go:printProgress()` — formats progress events to stderr

The new command is essentially `tail -f` on a JSONL file, parsing complete lines via `parseStreamLine()`, and formatting output similar to `claude-sandbox.sh`'s `live_monitor()`.

## Files to modify

### 1. `cmd/tail.go` (new)

New cobra command `ateam tail` with same flags as `ateam log`:
- `--role NAME` — tail a specific role's stream
- `--supervisor` — tail supervisor stream
- `--action ACTION` — action type (default: "report" for roles, "code" for supervisor)

**Implementation:**
- Resolve stream file path using same logic as `cmd/log.go` (`findLatestStreamFile` in the flat logs dir)
- Open file, seek to beginning (or end with a `--follow-from-end` flag, default: beginning)
- Read loop: read lines, parse only complete lines (ending with `\n`), format and print
- When no more data, `time.Sleep(300ms)` and retry (like `tail -f`)
- Exit on `result` event or on interrupt (Ctrl+C via signal handling)

### 2. `internal/runner/format.go`

Add `FormatStreamLine(w io.Writer, line []byte, state *FormatState)` that formats a single parsed event. Extract the per-event formatting from `FormatStream()` into this function so both `FormatStream` (batch) and the new tail command (live) can reuse it.

`FormatState` tracks mutable state across lines (turn counter).

### 3. `cmd/root.go`

Register `tailCmd`.

## Design details

### Line-complete reading

Read from file into a buffer. Only process data up to the last `\n`. Keep any trailing partial line in the buffer for the next read cycle. This avoids parsing incomplete JSON.

```
loop:
  n, err := file.Read(buf)
  append to pending buffer
  split on \n
  keep last segment if no trailing \n
  parse + format each complete line
  if no new data: sleep 300ms
  if result event seen: exit
```

### Output format

Match `claude-sandbox.sh` style with ANSI colors:
- `system/init`: dim `[MM:SS] session=SID model=MODEL`
- `assistant` with tools: cyan `[MM:SS] tool #N: TOOLNAME detail`
- `assistant` with text: yellow `[MM:SS] text #N: preview...`
- `result`: green `[MM:SS] done cost=$X turns=N tokens=IN/OUT`

Track elapsed time from the first event's timestamp (or from command start).

### Reuse from existing code

- `runner.parseStreamLine()` from `events.go` — parse each JSONL line
- `runner.extractReportText()` from `events.go` — extract text from assistant events
- Stream file path resolution pattern from `cmd/log.go` lines 50-66
- Tool detail extraction approach from `claude-sandbox.sh` lines 221-235

## Verification

1. `go build ./...` and `go test ./...`
2. In one terminal: `ateam report --roles security`
3. In another terminal: `ateam tail --role security` — verify live formatted output appears
4. Verify tail exits after `result` event
5. Run `ateam tail --role security` on a completed stream file — verify it replays and exits
6. Verify `ateam tail --supervisor --action review` works for supervisor streams
