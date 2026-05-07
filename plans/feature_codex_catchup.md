# Codex agent feature parity with Claude

**Status:** planned, not yet implemented. This document is self-contained — a future session can pick it up without rebuilding context.

## Why

The Claude agent (`internal/agent/claude.go`) has accumulated polish that the Codex agent (`internal/agent/codex.go`) never got: cache-token accounting, per-model pricing, stall heartbeats, verbose tool detail, context-utilization display in `ateam ps` / `ateam tail`, and `ateam resume`. The user wants Codex to be a first-class citizen wherever the Codex CLI's capabilities allow.

Audit summary (full file:line citations are in §"References" at the bottom):

| Gap | Severity | Fixable? |
|---|---|---|
| `thread.started.thread_id` dropped → no resume support | high | yes |
| `usage.cached_input_tokens` dropped → cost is wrong | high | yes |
| No reasoning/thinking event mapping → stall watchdog blind | medium | yes |
| No GPT/o-series pricing entries → `Cost = 0` for codex | medium | yes |
| No GPT/o-series in `modelContextWindows` → 0% utilization in UI | medium | yes |
| Verbose tool-input renderer keys on `e.Claude` → codex truncated | medium | yes |
| `cmd/inspect.go` resume hint claude-only | low | yes |
| `cmd/resume.go` flatly rejects non-claude | high | yes (fix below) |
| OAuth-style auth | low | won't-do (no OpenAI equivalent) |
| Sandbox `--settings` JSON | low | won't-do (Codex has its own model) |
| Multi-turn `Turns > 1` | low | won't-do (`exec --json` is one-shot by design) |

## Verified ground truth

The plan was anchored against a real Codex stream at `/tmp/claude/mtest4/myproj/.ateam/logs/530/stream.jsonl`. Field names below are confirmed from that file (and from `codex resume --help` on the host).

### Codex stream — event types observed

```
agent_message
command_execution
file_change
item.completed
item.started
item.updated
thread.started
todo_list
turn.completed
turn.started
```

### `thread.started` carries the resume id

```json
{"type":"thread.started","thread_id":"019df527-3195-79d1-a838-9adc1bebae81"}
```

The current parser reduces this to `return "system", &struct{}{}, nil` (codex.go ~line 275), throwing the id away. This is the single biggest unlock — capturing `thread_id` into `agent.StreamEvent.SessionID` enables `ateam resume`.

### `turn.completed.usage` shape

```json
{"type":"turn.completed","usage":{"input_tokens":2420850,"cached_input_tokens":2296704,"output_tokens":16207}}
```

`parseCodexResult` (codex.go ~line 494) reads `input_tokens` and `output_tokens` (with camelCase fallbacks) but does **not** read `cached_input_tokens`. Missing field → `summary.CacheReadTokens = 0` → cost calculation undercounts cached prompt usage.

### `codex resume` exists and accepts UUIDs

```
$ codex resume --help
Resume a previous interactive session …
Usage: codex resume [OPTIONS] [SESSION_ID] [PROMPT]

Arguments:
  [SESSION_ID]  Conversation/session id (UUID) or thread name. UUIDs take precedence …

Options:
  --include-non-interactive
      Include non-interactive sessions in the resume picker and --last selection
  …
```

ateam invokes Codex via `codex exec --json` (non-interactive). Without `--include-non-interactive`, those sessions are hidden from the picker. With it, the resume command works end-to-end.

## Design

### 1. `internal/agent/codex.go` — capture thread_id, cache tokens, reasoning

The dispatch table starts around line 274 (`switch eventType { case "turn.started", "thread.started": … }`).

**a) Wire `thread.started.thread_id` → `StreamEvent.SessionID`.**

Replace the empty `&struct{}{}` returned for `thread.started` with a typed event (e.g. `CodexSystemEvent struct { SessionID string }`). Parse `thread_id` out of the raw JSON. Where the dispatched events get translated to `agent.StreamEvent` (the channel-side bridge in `codex.go::Run`, near where the `system` event is emitted with PID — currently `ch <- StreamEvent{Type: "system", PID: cmd.Process.Pid}`), add `SessionID: codexEvt.SessionID`.

`turn.started` should keep returning the bare system event (no id on that one).

**b) Parse `usage.cached_input_tokens` in `parseCodexResult`.**

Today (codex.go ~512):

```go
var usage struct {
    InputTokens    int `json:"input_tokens"`
    OutputTokens   int `json:"output_tokens"`
    InputTokensCC  int `json:"inputTokens"`
    OutputTokensCC int `json:"outputTokens"`
}
```

Add:

```go
    CachedInputTokens   int `json:"cached_input_tokens"`
    CachedInputTokensCC int `json:"cachedInputTokens"`
```

Map to `re.CacheReadTokens`. The bridge from `CodexResultEvent` to `agent.StreamEvent` (look for where `summary.InputTokens = resultEv.InputTokens` happens — it's via `processEvent` in `internal/runner/runner.go`'s event loop, which already reads `ev.CacheReadTokens` for claude) should pick up the new field automatically once the bridging Codex event sets it. Verify by tracing one event through.

**c) Map reasoning events to `thinking`.**

Add a `case "agent_reasoning_delta", "agent_reasoning":` returning `("thinking", &CodexTextEvent{Text: <delta or text>}, nil)`. These events didn't appear in the sample stream but the schema documents them. Defensive — no harm if they never arrive.

**d) Leave `Turns = 1` hardcoded** (codex.go ~178) but add a 1-line comment explaining why: `exec --json` is a single-turn invocation. A future contributor shouldn't try to "fix" this by reading some `turns` field that doesn't exist.

### 2. `cmd/resume.go` — codex branch

Today (lines 71-72):
```go
if row.Agent != "claude" {
    return fmt.Errorf("resume only supports claude (run %d used agent %q)", row.ID, row.Agent)
}
```

Replace with a switch on `row.Agent`:

```go
switch row.Agent {
case "claude":
    // existing path: claude --resume <sid>
case "codex":
    // codex resume --include-non-interactive <sid>
default:
    return fmt.Errorf("resume only supports claude and codex (run %d used agent %q)", row.ID, row.Agent)
}
```

For codex, mirror the structure of the claude branch around lines 108–139:
- `case "", "none"`: print `codex resume --include-non-interactive <sid>`, exec it via a new `execCodexResume` helper if `resumeLaunch` is true.
- `case "docker-exec"`: print the docker-exec equivalent — `docker exec -it <container> codex resume --include-non-interactive <sid>` (no env-flag for `CLAUDE_CONFIG_DIR`; codex doesn't use one). Same caveat about session locality.
- `default`: same "oneshot container is gone" caveat.

`extractSessionID` (cmd/resume.go ~line 165) currently scans for `session_id` (Claude's field). Generalize: per line, check both `session_id` and `thread_id` and return whichever appears. `maxSessionScanLines` cap stays.

`selectResumeRow` (~line 142): the `--last` filter is `RecentFilter{Agent: "claude"}` — change to allow either claude or codex (probably easiest: drop the filter entirely and accept whichever recent run matched, then error in the switch above for unsupported agents).

`recentRowsByIDs` doesn't filter by agent; no change.

`resolveResumeConfigDir` returns the CLAUDE_CONFIG_DIR — only used in the claude branch; leave untouched.

### 3. Pricing — `defaults/runtime.hcl`

Pricing per model lives in HCL, not Go (see the existing `agent "claude" { … pricing { model "claude-sonnet-4-6" { input_per_mtok = 3.00 … } } }` blocks in `defaults/runtime.hcl` around line 262).

Add a `pricing { … }` block under `agent "codex" { … }` covering the GPT/o-series. Use OpenAI's published rates **at the time of implementation** (don't hardcode current numbers from this doc — they change). Models to include:

- `gpt-5`, `gpt-5-mini`, `gpt-5-nano`
- `gpt-4o`, `gpt-4o-mini`
- `gpt-4.1`, `gpt-4.1-mini`
- `o3`, `o4-mini`

Effect: `summary.Cost` populated for codex runs going forward (today: always 0 because the pricing map has no entry for the model name).

`pricing.go::NormalizeModel` strips trailing `-YYYY-MM-DD` date suffixes. Smoke-check that a name like `gpt-4o-mini-2024-07-18` normalizes correctly to `gpt-4o-mini` (the regex is right-anchored on the date so should be fine — verify in a test).

### 4. Context windows — `cmd/table.go::modelContextWindows`

The map (cmd/table.go around line 41-48) has only Claude entries today. The "ContextWindow %" column in `ateam ps` and the per-row context utilization bar in `ateam tail` show 0% for codex.

Add OpenAI families. Per current docs:
- `gpt-5`, `gpt-5-mini`, `gpt-5-nano`: 400k context (verify at write time)
- `gpt-4o`, `gpt-4o-mini`: 128k
- `gpt-4.1`, `gpt-4.1-mini`: 1M
- `o3`, `o4-mini`: 200k

Use `NormalizeModel` on lookup so date suffixes don't cause misses (the existing claude entries already rely on this).

### 5. Verbose rendering — `internal/runner/format_stream.go`

Around line 135 (per audit), the verbose tool-input renderer reads `e.Claude` (a typed pointer that's nil for codex events) before printing tool inputs. Codex tool calls populate the generic `StreamEvent.ToolName` / `ToolInput` fields just fine — drop the claude-specific guard and key off `e.ToolName != ""` instead.

Confirm by re-running `ateam tail --verbose` against a codex run (or by adding a test, see §7).

### 6. Inspect resume hint — `cmd/inspect.go`

Today: `if r.Agent == "claude" { fmt.Println("Run \`ateam resume\`...") }` (audit cited this).

Make agent-aware: print the hint for `claude` or `codex`; nothing for others. The `ateam resume` command itself dispatches to the right CLI after the §2 changes, so the hint text doesn't even need to mention the underlying tool.

### 7. Tests

- `internal/agent/codex_test.go` — add three cases:
  1. `thread.started` → `StreamEvent.SessionID == "<uuid>"`. Use the actual UUID from the verified sample for realism: `019df527-3195-79d1-a838-9adc1bebae81`.
  2. `turn.completed.usage` with `cached_input_tokens: 2296704` → `StreamEvent.CacheReadTokens == 2296704`.
  3. `agent_reasoning_delta` synthetic JSON → `StreamEvent.Type == "thinking"`, Text matches.

- `cmd/resume_test.go` — table-driven:
  - claude row → command starts with `claude --resume `
  - codex row → command starts with `codex resume --include-non-interactive `
  - unknown agent → error containing both "claude" and "codex"

  Don't actually exec; the dry-print path (`resumeLaunch=false`) prints "Command: …" before exec, so capture stdout and assert.

- `internal/runner/format_stream_test.go` — synthesize a codex `tool_use` `StreamEvent` (no `e.Claude`), render in verbose mode, assert the `ToolInput` snippet appears.

- `defaults/runtime.hcl` is exercised by the existing pricing-loader tests; verify the new entries parse.

### 8. Won't-do (document but don't implement)

Add a brief note to `DEV.md` (in the "Architecture: Runtime / Agents / Containers / Profiles" section, near the existing Agent table) listing what's intentionally claude-only:

- **OAuth login** (`claude setup-token` flow). OpenAI ships no equivalent; `OPENAI_API_KEY` (or `~/.codex/auth.json`) is the canonical path.
- **Sandbox `--settings` JSON.** Codex CLI uses its own sandbox model (`workspace-write` / `read-only` plus approval policies). The schemas don't translate; Codex sandbox flags belong in `agent "codex" { args = [...] }` in runtime.hcl.
- **Multi-turn turn count.** ateam invokes codex via `exec --json` which is one-shot; `Turns: 1` is correct unless we move to a different invocation mode.

## File map

| File | Change |
|---|---|
| `internal/agent/codex.go` | New `CodexSystemEvent { SessionID string }`; `parseCodexResult` reads `cached_input_tokens` (snake + camel); reasoning events → `thinking`; bridge updates so the channel-side `agent.StreamEvent` carries SessionID + CacheReadTokens. |
| `internal/agent/codex_test.go` | Three new cases (see §7). |
| `cmd/resume.go` | Drop claude-only guard; agent switch with a codex branch invoking `codex resume --include-non-interactive <sid>`; `extractSessionID` reads `thread_id` too; widen `selectResumeRow` `--last` filter. |
| `cmd/resume_test.go` | New (or extended) table test for both agents. |
| `defaults/runtime.hcl` | Add `pricing { … }` entries under `agent "codex"` for GPT/o-series. |
| `cmd/table.go` | Extend `modelContextWindows` with OpenAI families. |
| `internal/runner/format_stream.go` | Drop the `e.Claude != nil` guard around verbose tool-input rendering. |
| `internal/runner/format_stream_test.go` | Codex verbose rendering case. |
| `cmd/inspect.go` | Resume hint covers both agents. |
| `DEV.md` | Brief note listing intentional claude-only items (§8). |

## Verification

1. `go build ./...`, `go test ./...`, `make lint` — clean.
2. Replay an existing Codex run from the `/tmp/claude/mtest4/myproj/` test copy:
   ```
   ATEAM=/Users/nicolas/SyncDatabox/nicmac/projects/ateam/ateam
   cd /tmp/claude/mtest4/myproj && "$ATEAM" resume 530
   ```
   Should print a line like:
   ```
   Command: codex resume --include-non-interactive 019df527-3195-79d1-a838-9adc1bebae81
   ```
3. `ateam ps` shows non-zero `COST` for codex rows (after a fresh codex run that hits the new pricing path); the context-utilization column shows a real percentage instead of 0%.
4. `ateam tail --verbose` against an in-progress codex run shows tool inputs (file paths, command arguments) for `command_execution`, `web_search`, etc.
5. `ateam resume --last` → `--launch` drops into a codex resume session if the most recent run was codex.
6. `ateam serve` web UI: cost rendering for codex rows in the runs list is non-zero.

## References (file:line citations from the audit)

Pin these locations so re-orienting the work is fast:

- `internal/agent/codex.go:275` — `case "turn.started", "thread.started"` returns empty system event (drops thread_id).
- `internal/agent/codex.go:494` — `parseCodexResult` start.
- `internal/agent/codex.go:512–530` — usage struct (snake + camel fallback) — extend here.
- `internal/agent/codex.go:106` — `ch <- StreamEvent{Type: "system", PID: cmd.Process.Pid}` — extend to carry SessionID.
- `internal/agent/codex.go:178` — `Turns: 1` hardcode (leave; add comment).
- `internal/agent/claude.go:178` — claude's `SessionID: sys.SessionID` for parallel structure reference.
- `cmd/resume.go:71-72` — claude-only guard to remove.
- `cmd/resume.go:108-139` — claude branch shape to mirror for codex.
- `cmd/resume.go:142` — `selectResumeRow` filter to widen.
- `cmd/resume.go:~165` — `extractSessionID` reads `session_id`; add `thread_id`.
- `cmd/inspect.go:~103` — resume hint conditional.
- `cmd/table.go:~41-48` — `modelContextWindows` map.
- `internal/runner/format_stream.go:~135` — `e.Claude != nil` guard on verbose tool detail.
- `defaults/runtime.hcl:~262-287` — existing claude pricing block; mirror for codex.
- `internal/runner/runner.go::processEvent` — where `ev.CacheReadTokens` flows from the agent stream into `summary` and `cmdInfo`. Confirm cache_read picks up the new codex value.

## Sample fixtures for tests

Use these literal JSON snippets (copied from the verified stream) so future tests anchor on real shapes:

```jsonl
{"type":"thread.started","thread_id":"019df527-3195-79d1-a838-9adc1bebae81"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Reviewing the current tree…"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/opt/homebrew/bin/bash -lc pwd","exit_code":0,"status":"completed"}}
{"type":"turn.completed","usage":{"input_tokens":2420850,"cached_input_tokens":2296704,"output_tokens":16207}}
```

Synthetic for the reasoning case (not in the verified stream — schema-derived):

```jsonl
{"type":"agent_reasoning_delta","delta":"thinking through the diff…"}
```
