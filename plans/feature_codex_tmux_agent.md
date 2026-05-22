# Feature: Codex Tmux Agent

## Goal

Drive the interactive Codex CLI from ATeam to invoke TUI-only slash commands (initially `/review`) and capture their output as a normal ATeam run.

The motivating use case is `/review` — Codex's interactive review slash command, which is only available inside the TUI. `codex exec` and the `codex app-server` JSON-RPC mode do not expose slash commands, so a real terminal is unavoidable.

## Non-goals

- Long-lived multi-turn interactive sessions.
- Mid-task steering or message injection beyond the initial command.
- Replacing ATeam's existing `claude -p` / `codex exec` headless runners — this is an additional adapter, not a substitute.
- General-purpose tmux multiplexer features (multiple windows, panes, scripting).

## Status

**v1 landed in commit `621076c` ("Add codex tmux agent")** — it does the architectural skeleton (tmuxctl, codex adapter, agent type, runtime.hcl wiring, basic tests). This document now tracks **v1.1**: the fixes required to ship the feature, drawn from a follow-up architectural review against Gas Town / oauth-cli-coder lessons and an external code review.

Below the original architecture sections, the **v1.1 plan** lists what changes and in what order.

## Why tmux specifically

| Option | Verdict |
|---|---|
| `codex exec "/review"` | Slash commands not honoured in headless mode. |
| `codex app-server` (JSON-RPC) | Same — slash commands are a TUI feature. |
| Raw PTY via `creack/pty` | Workable but forces us to write an ANSI parser to know what's "on screen." tmux already does this. |
| tmux | Gives us `capture-pane -p` which returns the *rendered* terminal state with cursor positioning, line clearing, and overdraws already applied. This is the single biggest reason. Also: easy out-of-band inspection (`tmux attach -t …`) when debugging. |

Cost of tmux: one extra process per invocation and an external binary dependency. Acceptable.

## Architecture (v1, unchanged)

One tmux session per Codex invocation. Strictly synchronous from ATeam's runner's perspective:

```
spawn → wait-ready → send command → wait-idle → capture → kill
```

```
┌─ ATeam runner (Go) ───────────────────────────────────┐
│                                                       │
│  RunCodexInteractive(workdir, slashCommand) Output    │
│        │                                              │
│        ▼                                              │
│  ┌─ tmuxctl (Go package) ─────────────────────────┐   │
│  │  NewSession(name, w, h, env)                   │   │
│  │  SendKeysLiteral(name, text)                   │   │
│  │  SendEnter(name)                               │   │
│  │  CapturePane(name) (raw, rendered)             │   │
│  │  KillSession(name)                             │   │
│  └───────────────┬────────────────────────────────┘   │
│                  │ shells out to `tmux`               │
│                  ▼                                    │
│           tmux server (host or container)             │
│                  │                                    │
│                  ▼                                    │
│             codex (TUI process)                       │
└───────────────────────────────────────────────────────┘
```

### Where this runs

- **Host mode** — the only supported v1 mode. tmux and codex on the host. Reuses the user's Codex auth.
- **Container mode** — explicitly rejected at runner-construction time in v1 (returns an error). Tracked as a v2 follow-on.

## State machine

```
   spawn
     │
     ▼
  ┌──────────┐   ready marker appears   ┌──────────┐
  │ STARTING │ ───────────────────────► │  READY   │
  └────┬─────┘                          └────┬─────┘
       │ timeout                             │ send command
       ▼                                     ▼
   ┌───────┐                            ┌──────────┐
   │ ERROR │ ◄──── timeout ────────────│   BUSY   │
   └───────┘                            └────┬─────┘
                                             │ prompt re-appears
                                             ▼  (≥2 consecutive idle polls,
                                             │   no busy indicator)
                                        ┌──────────┐
                                        │   IDLE   │
                                        └────┬─────┘
                                             │ capture + kill
                                             ▼
                                          DONE
```

Three timeouts, each separately configurable:

- `start_timeout` — max time from spawn to READY. Default 15s.
- `busy_timeout` — max time from "command sent" to IDLE. Default 5min in code, runtime.hcl pins 20m for `/review`. Effective value also clamps to ctx deadline minus 1s when set.
- `quiescence_window` — once output appears stable, wait this long before declaring IDLE. Default 2s.

## Per-instance identity

Each codex-tmux invocation is identified by its **EXEC_ID** (the row ID in `agent_execs`). EXEC_ID is the single source of truth for naming:

- tmux session: `ateam-codex-<EXEC_ID>`
- socket path: `<ProjectDir>/.ateam/cache/tmux/exec-<EXEC_ID>.sock`

Both are derived from `ResolvedEnv.ProjectDir` (NOT `os.Getwd()` or `req.WorkDir`) so they:
- work correctly when `--work-dir` points outside the project tree,
- isolate concurrent runs (e.g. `ateam parallel`, `ateam report`),
- are trivially garbage-collected on session kill (each EXEC_ID owns its own socket).

There is **no per-exec CODEX_HOME** — see next section.

## CODEX_HOME: don't touch it

v1 created `<workdir>/.cache/codex-home/<EXEC_ID>/`, symlinked `~/.codex/auth.json` into it, and wrote a custom `config.toml`. **v1.1 deletes all of that.** Codex uses the user's `~/.codex/` natively, exactly the way `oauth-cli-coder` and Gas Town drive it.

The reasoning, from reading `openai/codex` source:

- **`-c projects."<path>".trust_level=trusted` does NOT work.** The override splitter in `codex-rs/config/src/overrides.rs` splits on `.` with no quote handling, so the path segment becomes a literal table key including the quote characters — never matches the lookup in `config_toml.rs`. There is no CLI flag that suppresses the trust dialog directly.
- **The trust dialog defaults to "Yes, continue".** `onboarding_screen.rs` unconditionally sets `let highlighted = TrustDirectorySelection::Trust;`. Pressing Enter accepts.
- **Accepting writes one trust entry to `<CODEX_HOME>/config.toml`.** `trust_directory.rs:170` calls `set_project_trust_level(&self.codex_home, &target, TrustLevel::Trusted)` — adds `[projects."<workdir>"] trust_level = "trusted"`. **Idempotent**: `should_show_trust_screen` only fires when `config.active_project.trust_level.is_none()`, so subsequent runs in the same workdir skip the dialog entirely.
- **Codex natively supports many concurrent sessions in one CODEX_HOME.** Sessions live in `<CODEX_HOME>/sessions/<id>/`, namespaced by ID. No race.
- **Custom CODEX_HOMEs fragment auth state.** `codex-rs/login/src/auth/storage.rs` keys the keyring backend by SHA256 of the canonical CODEX_HOME path. A custom CODEX_HOME lands in a different keyring slot than the user's. After a token refresh, `AutoAuthStorage.save` writes the fresh token to *our* slot and deletes the disk file — future runs read the fresh token from our slot only, never the user's. This is exactly the "Claude auth fragmentation" failure mode we are explicitly avoiding.

The plan therefore is:

- **CODEX_HOME**: never set. Codex reads/writes `~/.codex/` directly.
- **Auth**: never touched by ateam. Zero risk surface.
- **Update dialog**: suppressed by `-c check_for_update_on_startup=false` (already in `withInteractiveDefaults`).
- **Trust dialog**: dismissed by the existing `codexTrustDialog` detector pressing Enter. Codex writes one trust entry per ateam workdir to `~/.codex/config.toml` — identical to the user clicking "Yes" in interactive codex. Bounded growth, idempotent.
- **Token usage / session log mining**: `~/.codex/sessions/<id>/` is the stable path (PR 4 research).

The external-review P2 item about container-mode workdir translation also dissolves: there's no host-side staging to translate, so there's no bug to fix.

## Idle detection (revised in v1.1)

Two channels, both required:

### Prompt-shape regex, ≥2 consecutive matches

`PromptReady` matches the bottom-of-pane input line shape (`›` followed by an optional command, with a Codex status line `<model> <effort> · <cwd>` somewhere in the tail). Per Gas Town's `WaitForIdle` lesson, the regex must match on **two consecutive polls** before declaring IDLE — TUI redraws can briefly show a prompt-shaped line during inter-tool gaps while Codex is still working. v1 transitions on a single match; v1.1 fixes this.

### Busy-indicator scan

In every poll, scan the captured pane for Codex's "working" cues (`Esc to interrupt`, `Thinking…`, spinner glyphs). If found, IDLE is reset regardless of prompt shape. This blocks the inter-tool-gap false positive that the consecutive-match check would still let slip through if both polls happen during the gap.

### Quiescence fallback (kept)

If the prompt regex hasn't matched but the pane hash hasn't changed for `quiescence_window` and a busy indicator isn't present, declare IDLE. Log a warning so quiescence-rate drift surfaces TUI changes.

### Tighter input echo

`waitInputEcho` currently does `strings.Contains(fullRendered, input)`. v1.1 tightens this to:
1. capture only the bottom 5–8 non-empty lines,
2. require the prompt marker `› ` prefix to be present,
3. match a **sentinel** the agent prepends to every prompt (see below), not the full prompt text.

### Sentinel-marker prompt echo (multi-line support)

External review item P2: multi-line prompts (`@file`, stdin, role prompts) break the single-line `› <prompt>` echo match.

Fix: prepend a unique single-line marker to every prompt before sending, e.g.:

```
# ateam-exec-<EXEC_ID>
<the actual prompt — any number of lines>
```

- Echo detection becomes a trivial single-line match on the marker. Robust to any prompt content.
- `ExtractCommandOutputWithStatus` extracts the output by skipping past the marker line, then continuing to the trailing prompt re-draw.
- The marker is stripped from the captured output before returning to the runner.
- Codex's TUI treats `#` lines as ordinary input (`#` is just a character to it) — verify in a smoke test, fall back to a sentinel like `> ateam-exec-<EXEC_ID>` if `#` is reserved.

### Polling cadence

- During STARTING and BUSY: poll every 250ms. `tmux capture-pane` is a local IPC call.
- Stable pane hash detects "unchanged" cheaply (already implemented in v1).

## TTY sizing

Default to **300×100** in v1.1 (oauth-cli-coder's tested size). v1 used 200×50 — fine for short outputs, but `/review` output on long files wraps at 200 columns and breaks the prompt regex. `tmux_width` / `tmux_height` in `runtime.hcl` still override per-agent.

## Process lifecycle and Ctrl-C

Same structural approach as `claude.go` / `codex.go` — no special signal logic; ctx-cancel handles everything.

The tmux *server* (`tmux -D -S <socket>`) is the thing we bind to the run's context:

```go
serverCmd := exec.CommandContext(ctx, "tmux", "-D", "-S", socket)
configureProcessLifecycle(serverCmd)  // sets Setpgid: true on unix
serverCmd.Start()
```

When ctx is canceled (user Ctrl-C, parent SIGTERM, ateam timeout), `exec.CommandContext` SIGKILLs the server, the process-group setting cascades the signal to every process in the pane (codex), and the whole tree dies in one go.

v1 had two problems on the cancel path:
1. `configureProcessLifecycle` was not applied to the server cmd → no process group → kill may not cascade reliably on macOS.
2. `defer sess.Kill(context.Background())` ran on the *background* context, racing the ctx-driven kill. This adds noise and complexity for no benefit on the cancel path.

v1.1: apply `configureProcessLifecycle`, drop the `Kill(Background)` defer on the cancel path. Keep an explicit `KillSession` on the **success path only** (clean up the socket and CODEX_HOME promptly instead of waiting for SIGHUP propagation).

## Capturing the result

`/review` output can scroll past the visible pane. Use:

```
tmux capture-pane -p -t <session> -S - -E -
```

`-S -` captures from the start of scrollback; `-E -` to the end including the visible region. `ExtractCommandOutputWithStatus` skips past the sentinel marker line and trims the trailing prompt re-draw.

## PID tracking and multi-instance support

The runner uses `agent_execs.pid` to detect dead runs (`isProcessAlive(pid)` in `cmd/table.go:929`). v1 never writes a PID for codex-tmux → concurrency guards blind, `ateam ps` shows no liveness info.

v1.1: after `waitReady`, query the pane's foreground PID:

```
tmux display-message -t <session> -p '#{pane_pid}'
```

Then emit `StreamEvent{Type: "system", Subtype: "process_start", PID: panePid}`. The runner's existing `processEvent` handler writes it to `agent_execs.pid` via `CallDB.SetPID` — no runner changes needed.

Combined with per-EXEC_ID sockets and CODEX_HOME dirs, this restores full concurrency support: `ateam parallel`, `ateam report`, and any multi-role workflow can spawn N codex-tmux instances concurrently with correct PID tracking and isolation.

## Observability gaps (closing them)

The runner expects a rich `StreamEvent` stream. v1's codex-tmux emits one assistant event at the end and nothing in between — consequences:

| Field / event | v1 state | v1.1 fix |
|---|---|---|
| `system` with PID | missing | emit via `pane_pid` (above) |
| `thinking` heartbeats | missing | emit one every 5–10s while BUSY, gated on pane-hash change; keeps `StallWarnAfter` from mis-firing on long `/review` |
| Token usage / cost | always 0 | parse `$CODEX_HOME/sessions/<id>/` session log incrementally — research-first, see below |
| `tool_use` events | never | scrape from session log if it has them; otherwise document the gap |
| Streamed `assistant` text | batched at end | acceptable for v1.1 (matches plan §non-goals on live streaming) |

### Token tracking research task

Before implementing, inspect `$CODEX_HOME/sessions/` on a real v0.132+ codex run:
- file layout (one file per session? JSONL?),
- write cadence (per-turn? per-event?),
- presence of token counts and per-tool events,
- whether the format is stable across patch versions.

If session logs are clean JSONL with usage: parse them incrementally (a separate goroutine tailing the file), emit `assistant` / `tool_use` / `result` events matching the codex JSONL agent's schema, and we get cost/usage parity with `codex exec --json`.

If they're not: fall back to (a) custom statusline script writing to a tail-able file, or (b) explicit "$0 for codex-tmux" doc note.

## Robustness improvements (Gas Town lessons)

- `-u` flag on every tmux invocation for UTF-8 (Gas Town issue #1219 fix).
- NBSP normalization (` ` → space) in `PromptReady` before regex match.
- (Skip for now: SIGWINCH wake-pane dance — add if "typed but never submitted" symptoms appear).
- (Skip for now: stealth PTY wrapper — add if Codex ever changes behavior under tmux).

## Components

| Package | Responsibility | v1 LOC | v1.1 change |
|---|---|---|---|
| `internal/tmuxctl` | tmux primitives; stateless | ~440 | path resolution via injected ProjectDir; process-lifecycle on serverCmd; `-u` flag; drop `nearestGoModule` and `ATEAM_TMUX_SOCKET_DIR` |
| `internal/codex/adapter.go` | Codex-specific: prompt regex, dialog dismissal, output extraction, idle detection | ~530 | consecutive-idle, busy-indicator, sentinel marker, NBSP normalization, tighter `waitInputEcho`, pane-change heartbeat |
| `internal/codex/sessionlog.go` (new) | tail+parse `$CODEX_HOME/sessions/<id>/` | — | new in v1.1 (after research) |
| `internal/agent/codex_tmux.go` | Agent interface; PID emit; heartbeat | ~280 (shrinks ~80) | delete all CODEX_HOME staging code; emit `process_start` w/ pane PID; emit `thinking` heartbeats; container-mode rejection at builder |
| `defaults/runtime.hcl` | agent + profile config | — | bump pane to 300×100 |

## `tmuxctl` surface (revised)

```go
type Session struct {
    Name       string
    SocketPath string
    Width      int
    Height     int
    // … internal
}

// New now takes the project dir directly; caller resolves it via ResolvedEnv.
// The socket is always at <projectDir>/.ateam/cache/tmux/<safeName>.sock.
func New(ctx context.Context, projectDir, name string, w, h int, env []string, workdir string, cmd []string, factory CmdFactory) (*Session, error)

func (s *Session) SendKeys(ctx context.Context, keys ...string) error
func (s *Session) SendLiteral(ctx context.Context, text string) error      // via paste buffer
func (s *Session) TypeLiteral(ctx context.Context, text string) error      // send-keys -l (typed)
func (s *Session) Capture(ctx context.Context, full bool) (string, error)
func (s *Session) PanePID(ctx context.Context) (int, error)                // new in v1.1
func (s *Session) HasSession(ctx context.Context) (bool, error)
func (s *Session) Kill(ctx context.Context) error
```

Multi-line input via paste-buffer (`set-buffer` + `paste-buffer`). Already in v1, keep.

## Container-mode rejection (v1)

In `cmd/table.go` `buildAgent` for `codex-tmux`, or earlier in `newRunner` after profile resolution, reject when the resolved container is anything other than `none`:

```go
if ac.Type == "codex-tmux" && cc.Type != "none" {
    return nil, fmt.Errorf("codex-tmux is host-only in v1; use a container=none profile")
}
```

Tracked as a v2 follow-on (requires tmux+codex in the container image, host-side stage→mount of CODEX_HOME, and host-vs-container path handling).

## Testing

- **Unit tests** for `tmuxctl` against `tmux` on the test runner. Spawn `cat`, send text, capture, assert. Skip if `tmux` is absent. (v1 has this; keep.)
- **Unit tests** for `PromptReady` regex + busy-indicator + `ExtractCommandOutputWithStatus` with captured Codex output snippets in `testdata/`. (v1 has prompt-ready and extract tests; v1.1 adds busy-indicator, consecutive-idle, sentinel-marker, multi-line-prompt cases.)
- **Unit tests** for sentinel-marker round-trip: prepend marker, simulate echo, verify extraction strips it.
- **Unit tests** for the session-log parser (if implemented) with recorded `sessions/<id>/` samples.
- **Integration test** (gated, `-tags=codex_live`) end-to-end `/help` against a real `codex` install. Off in CI by default.
- **Concurrency test** spawning N≥3 simultaneous fake-codex tmux sessions to validate per-EXEC_ID isolation.
- **No mocking of tmux itself.**

## Observability (CallDB record fields)

- `runner = "codex-tmux"`
- `pid` — codex pane PID (NEW in v1.1; was null)
- `command` — the slash command or full prompt
- `duration_ms` — split into `time_to_ready`, `time_to_idle`, `time_to_capture` in the synthetic stream
- `output_chars`, `output_lines`
- `idle_signal` — `prompt_match` | `quiescence` | `timeout` (drift canary)
- `tmux_session_name` (NEW in v1.1, surfaced in stream + dry-run for `tmux attach -t …`)
- `final_state` — `done` | `timeout` | `error`
- `input_tokens`, `output_tokens`, `cost_usd` — populated only if session-log parsing is implemented

## Failure modes

| Failure | Symptom | Response |
|---|---|---|
| `codex` not found | exec error at spawn | Surface immediately. |
| TUI never reaches READY | `start_timeout` elapsed | Capture pane, kill session, mark run failed. |
| Slash command rejected | Prompt re-appears with error text | IDLE detected normally; capture includes error; `codexErrorLine` catches structured errors. |
| Codex hangs mid-`/review` | `busy_timeout` elapsed | Send `C-c`, brief wait, kill. Capture whatever's there. |
| tmux server crashes | `HasSession` returns false | Run marked failed. |
| Pane size too small, regex fails | `quiescence` fallback fires repeatedly | Logged; operator bumps size in config. |
| Auth missing | codex prints auth-needed instead of TUI | Caught by `start_timeout`; captured pane explains. |
| Multi-line prompt | echo never matches single line | Solved by sentinel marker. |
| Container profile chosen | runner construction | Error: "codex-tmux is host-only in v1." |
| Concurrent codex-tmux runs | shared scratch state | Solved by per-EXEC_ID sockets + CODEX_HOME. |
| Stall watchdog mis-fire | "agent stalled" log during long `/review` | Solved by pane-change heartbeat as `thinking` event. |
| Ctrl-C orphan codex | session survives parent SIGINT | Solved by `configureProcessLifecycle` on serverCmd + ctx-driven kill. |

---

## v1.1 implementation sequence

Each PR is independently shippable and testable.

### PR 1 — Correctness (the bugs that cause wrong results)

Goal: stop reporting completion before Codex is actually done.

1. Sentinel-marker prompt-echo path (multi-line support, single-line detection).
2. Require ≥2 consecutive `PromptReady` matches before IDLE.
3. Busy-indicator scan (`Esc to interrupt`, `Thinking…`, spinner glyphs) — short-circuit IDLE.
4. Tighter `waitInputEcho`: bottom-N lines + prompt marker + sentinel.
5. NBSP normalization in `PromptReady`.
6. `-u` flag on every tmux command.
7. Bump default pane to 300×100.
8. New tests: multi-line prompt, consecutive-idle, busy-indicator, NBSP, sentinel round-trip.

### PR 2 — Lifecycle, PIDs, concurrency, CODEX_HOME cleanup

Goal: enable `ateam parallel` / `ateam report` with codex-tmux, clean Ctrl-C, and remove the v1 CODEX_HOME risk surface.

1. **Delete v1 CODEX_HOME staging code entirely** — `ensureWritableCodexHome`, `writeCodexTmuxConfig`, `seedCodexHomeFile`, the CODEX_HOME branch in `codexTmuxEnv`. Codex reads/writes `~/.codex/` natively; ateam does not interpose.
2. Plumb `ResolvedEnv.ProjectDir` to `tmuxctl.New`; move socket to `<ProjectDir>/.ateam/cache/tmux/exec-<EXEC_ID>.sock`. Drop `nearestGoModule` and `ATEAM_TMUX_SOCKET_DIR`.
3. Use EXEC_ID-based session name (`ateam-codex-<EXEC_ID>`).
4. Apply `configureProcessLifecycle` to the tmux server cmd.
5. Drop the `defer sess.Kill(context.Background())` on the cancel path; keep an explicit `KillSession` only on the success path.
6. Query `pane_pid` via `tmux display-message` after `waitReady`; emit `StreamEvent{Type: "system", Subtype: "process_start", PID: panePid}`.
7. Reject `container != none` for `codex-tmux` at runner construction with a clear error.
8. Surface `tmux_session_name` in the synthetic stream JSON and the dry-run output.
9. New tests: concurrent runs (N≥3), Ctrl-C delivers SIGHUP/SIGKILL to inner codex within 1s, container-mode rejection, no `CODEX_HOME` env var is set by the agent.

### PR 3 — Observability heartbeat (stall watchdog fix, no token tracking yet)

Goal: progress UI works during long runs; stall watchdog stops mis-firing.

1. In `waitIdle`, when the pane hash changes, emit a `thinking` event with a short pane delta. Throttle to one per 5–10s to avoid spam.
2. Update the synthetic stream to record per-event timing (`time_to_ready`, `time_to_idle`).
3. New tests: heartbeat fires on hash change, doesn't fire when pane is static, is rate-limited.

### PR 4 — Token/cost via Codex session logs (research → implement)

Goal: cost/usage parity with `codex exec --json` if the session log format permits.

1. **Research spike** (non-code): run codex with a known `CODEX_HOME` and one `/review` invocation. Inspect `$CODEX_HOME/sessions/<id>/`:
   - file layout, write cadence, schema,
   - whether token counts and `tool_use`/`tool_result` events are present,
   - format stability hint (look for version field or schema commitment).
2. If clean JSONL with usage: add `internal/codex/sessionlog.go` — a goroutine tailing the session file in parallel with the tmux pane watcher. Emit normal `assistant`, `tool_use`, `tool_result`, `result` events with token/cost fields. Wire `Pricing` table into the agent's cost estimation path (it's currently dead-carry).
3. If not: document codex-tmux as `$0` and skip implementation. File a follow-on for a statusline-script-based approach.
4. New tests: session-log parser with recorded fixtures.

### PR 5 (optional) — Container mode

Out of v1 scope. Tracked as a follow-on; documented but not implemented.

## Open questions resolved

| Question (v1 plan) | Resolution |
|---|---|
| Container or host for v1? | Host only. Container is v2 and explicitly rejected. |
| Single Codex binary version pin? | Tested against v0.132.0; pin recorded in CallDB row; prompt regex carries a "tested against vX.Y" comment. |
| How does ATeam discover Codex auth state? | ATeam does not touch auth. Codex reads `~/.codex/auth.json` natively (no CODEX_HOME override). v1.1 doesn't add discovery; if the user isn't logged in, `start_timeout` fires with the auth-prompt capture for diagnosis. |
| Multiple slash commands or just `/review` for v1? | Any prompt the operator types. Sentinel marker means `/review`, `/help`, free-form text, multi-line `@file` prompts all work uniformly. |
| Stream output back live, or only on completion? | Completion only for v1/v1.1. Pane-change heartbeats give progress without true streaming. `StartPipePane` remains the path to live streaming if ever needed. |

## What this is *not*

A general-purpose interactive-agent framework. Resist scope creep — every project that grew beyond this size did so because it needed multi-agent coordination, persistent sessions, or watchdog supervision. None of that applies to synchronous "invoke one slash command and return."

## Out-of-scope follow-ons (parking lot)

- Container mode (v2).
- Headless Codex via `codex app-server` JSON-RPC — separate adapter, study Harnex.
- Multi-turn interactive Codex.
- Live output streaming to the operator — `StartPipePane` exists if we ever want it.
- A general `ateam tui <slash-command>` for arbitrary CLIs — premature; build this first, generalise if a second use case appears.
