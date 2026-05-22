# Feature: Codex Tmux Agent

## Goal

Drive the interactive Codex CLI from ATeam to invoke TUI-only slash commands (initially `/review`) and capture their output as a normal ATeam run.

The motivating use case is `/review` — Codex's interactive review slash command, which is only available inside the TUI. `codex exec` and the `codex app-server` JSON-RPC mode do not expose slash commands, so a real terminal is unavoidable.

## Non-goals

- Long-lived multi-turn interactive sessions.
- Mid-task steering or message injection beyond the initial command.
- Replacing ATeam's existing `claude -p` / `codex exec` headless runners — this is an additional adapter, not a substitute.
- General-purpose tmux multiplexer features (multiple windows, panes, scripting).

## Why tmux specifically

| Option | Verdict |
|---|---|
| `codex exec "/review"` | Slash commands not honoured in headless mode. |
| `codex app-server` (JSON-RPC) | Same — slash commands are a TUI feature. |
| Raw PTY via `creack/pty` | Workable but forces us to write an ANSI parser to know what's "on screen." tmux already does this. |
| tmux | Gives us `capture-pane -p` which returns the *rendered* terminal state with cursor positioning, line clearing, and overdraws already applied. This is the single biggest reason. Also: easy out-of-band inspection (`tmux attach -t …`) when debugging. |

Cost of tmux: one extra process per invocation and an external binary dependency. Acceptable.

## Architecture

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

Two deployment modes, both supported by the same adapter:

1. **Host mode** — `tmux` and `codex` both on the host. Simplest for development. Reuses the user's Codex auth.
2. **Container mode** — `tmux` and `codex` inside an ATeam container, the way Claude runs today. Required for unattended/scheduled use. Auth seeded the same way as the existing Claude container (mount `~/.codex` read-only, or whatever Codex's equivalent is).

The adapter does not care which mode it runs in. The container-mode plumbing is handled by ATeam's existing container layer.

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
                                             ▼
                                        ┌──────────┐
                                        │   IDLE   │
                                        └────┬─────┘
                                             │ capture + kill
                                             ▼
                                          DONE
```

Three timeouts, each separately configurable:

- `start_timeout` — max time from spawn to READY. Default 15s.
- `busy_timeout` — max time from "command sent" to IDLE. Default 5min for `/review`; per-command override expected.
- `quiescence_window` — once output appears stable, wait this long before declaring IDLE. Default 2s.

## Idle detection (the hard part)

This is where the project lives or dies. Two channels, in order of preference:

### Primary: prompt-shape regex

Codex's TUI redraws an input box at the bottom of the pane when waiting for input. The bottom border / cursor marker is a stable visual signal that "Codex is ready for the next command." On every poll:

1. `capture-pane -p` (rendered, ANSI-stripped via `-J`)
2. Match the bottom N lines against a Codex-specific regex
3. If matched AND the match wasn't present in the previous poll → state was BUSY, transition to IDLE
4. If matched AND present in the previous poll → state was already IDLE, ignore

The exact regex is a Codex-version-specific detail and *must* live in one place (`internal/codex/prompt.go`) with a comment naming the Codex version it was tested against.

### Fallback: quiescence

If the prompt regex doesn't match within `quiescence_window` but the pane content hasn't changed for `quiescence_window` seconds either, declare IDLE anyway. Log a warning so we notice when the prompt regex drifts.

### Polling cadence

- During STARTING and BUSY: poll every 250ms. Cheap (`tmux capture-pane` is a local IPC call) and the user-facing latency dominates anyway.
- Compute a stable hash of the rendered pane to detect "unchanged" cheaply.

### Why not Codex hooks or JSONL

Codex has neither in any usable form for TUI sessions. Don't go looking — this was the conclusion of the prior research turn.

## TTY sizing (the gotcha that wastes a day)

TUIs render based on terminal size. If you don't set the pane size explicitly, you get a default that wraps `/review`'s output unpredictably and your prompt regex breaks.

- Create the session with explicit dimensions: `tmux new-session -d -x 200 -y 50 …`
- Pick numbers wide enough that Codex's input box doesn't wrap. 200×50 is a reasonable starting point; oauth-cli-coder uses 300×100 and that's also fine.
- Document the chosen size at the top of `tmuxctl` with the rationale; future developers will want to know.

## Capturing the result

`/review`'s output can scroll past the visible pane. Use:

```
tmux capture-pane -p -t <session> -S - -E -
```

`-S -` means "from the start of the scrollback"; `-E -` means "to the end including the visible region." This returns the whole history, post-render. ANSI codes can optionally be preserved with `-e` if we want to surface them in the UI later.

Strip the trailing prompt re-draw from the captured text before storing — the user wants the command's *output*, not the prompt that came after.

## Cleanup

`KillSession` in a `defer`. Tmux sessions outliving their parent process is a debugging nightmare. Belt-and-braces: name the session deterministically (e.g. `ateam-codex-<runID>`) so an external janitor can sweep up stragglers if a panic ever skips the defer.

## Components

| Package | Responsibility | LOC estimate |
|---|---|---|
| `internal/tmuxctl` | Thin Go wrapper around the `tmux` binary. Stateless. | ~200 |
| `internal/codex/adapter` | Codex-specific knowledge: ready marker, prompt regex, slash-command syntax, version probe. | ~150 |
| `internal/runner/codexinteractive` | Glues runner → tmuxctl → adapter; produces a CallDB record. | ~200 |
| `cmd/<verb>.go` | CLI surface — e.g. `ateam codex-review <path>`. | ~50 |

Total estimate: ~600 lines of Go plus tests. Small enough to own outright.

### `tmuxctl` surface (proposed)

```go
type Session struct {
    Name   string
    Width  int
    Height int
}

func New(ctx context.Context, name string, w, h int, env []string, cmd []string) (*Session, error)
func (s *Session) SendKeys(ctx context.Context, keys string) error          // tmux-style: "Enter", "C-c"
func (s *Session) SendLiteral(ctx context.Context, text string) error       // send-keys -l
func (s *Session) Capture(ctx context.Context, full bool) (string, error)   // full = include scrollback
func (s *Session) HasSession(ctx context.Context) (bool, error)
func (s *Session) Kill(ctx context.Context) error
```

Multi-line input goes via paste-buffer (`load-buffer` + `paste-buffer`), not chunked `send-keys`. This is non-negotiable — it's the lesson every project in the research independently learned.

## Re-implementation strategy

We will not take a dependency on any of the projects below. We will read them, copy ideas, and own the result.

| What we want | Where to study | What to lift |
|---|---|---|
| Go tmux primitives | **Gas Town's `pkg/tmux/`** ([steveyegge/gastown](https://github.com/steveyegge/gastown)) | The interface shape, paste-buffer multi-line pattern, `StartPipePane` for live capture (probably unneeded for v1 but worth knowing about). |
| Codex idle-prompt regex | **oauth-cli-coder** ([codeninja/oauth-cli-coder](https://github.com/codeninja/oauth-cli-coder)) (Python) | The actual prompt-shape regex for the current Codex version. Translate to Go. |
| TUI size handling, stealth-mode scrubbing of `TMUX`/`TMUX_PANE` env vars | **oauth-cli-coder** | The env-scrubbing trick is essential if ATeam itself is ever run from inside tmux (so we don't nest). |
| Pane-content stable-hash quiescence detection | None — write it. SHA256 of the rendered pane string with a small ring buffer of the last N hashes. | — |
| Codex `app-server` JSON-RPC client (for *non*-slash-command Codex work later) | **Harnex** ([jikkuatwork/harnex](https://github.com/jikkuatwork/harnex)) (Ruby) | The JSON-RPC message shapes. Out of scope for this feature, but the right adapter to add next when we want headless Codex. |

What we explicitly do **not** copy: orchestrators, mailboxes, supervisors, multi-agent coordination, watchdog frameworks. None of that is in scope here.

## Testing

- **Unit tests** for `tmuxctl` against `tmux` on the test runner. Spawn `cat`, send text, capture, assert. Skip cleanly if `tmux` is absent.
- **Unit tests** for the prompt regex with captured Codex output snippets in `testdata/`. When Codex's TUI changes, this is the test that fails and tells us to update the regex.
- **Integration test** (gated, `-tags=codex_live`) that actually invokes a small `/help`-style command end-to-end against a real `codex` install. Off in CI by default.
- **No mocking of tmux itself.** Tmux is fast enough that fake-shelling adds complexity without value.

## Observability

The runner produces a CallDB record with:
- `runner = "codex-interactive"`
- `command = "/review"` (or whatever slash command)
- `duration_ms` — split into `time_to_ready`, `time_to_idle`, `time_to_capture`
- `output_chars`, `output_lines`
- `idle_signal` — one of `prompt_match`, `quiescence`, `timeout` (the diagnostic that will tell us when the regex drifts)
- `tmux_session_name`
- `final_state` — `done`, `timeout`, `error`

The `idle_signal` distribution across runs is the leading indicator of TUI version drift. If `quiescence` rises above e.g. 10% of runs, the regex needs updating.

## Failure modes and what we do about each

| Failure | Symptom | Response |
|---|---|---|
| `codex` not found | `exec` error at spawn | Surface immediately; do not attempt retry. |
| TUI never reaches READY | `start_timeout` elapses | Capture pane for debugging, kill session, mark run failed. |
| Slash command rejected | Prompt re-appears with error text in scrollback | IDLE detected normally; output capture includes the error. Caller's problem to interpret. |
| Codex hangs mid-`/review` | `busy_timeout` elapses | Send `C-c` once, wait briefly for IDLE, then kill. Capture whatever's there. |
| tmux server crashes | `HasSession` returns false | Run marked failed; no recovery (Codex state is gone). |
| Pane size too small, prompt regex fails | `quiescence` fallback fires repeatedly | Logged; bump size on next run via config. |
| Authentication missing | `codex` itself prints an auth-needed message instead of starting the TUI | Caught by `start_timeout`; the captured pane explains the failure. |

## Open questions

1. **Container or host for v1?** Recommend host-first to ship faster; container mode follows once the regex is stable.
2. **Single Codex binary version pin?** Probably yes — record the version in the CallDB record, and the prompt regex carries a "tested against vX.Y" comment.
3. **How does ATeam discover Codex auth state?** Defer until we hit it; for v1 assume the user is logged in.
4. **Multiple slash commands or just `/review` for v1?** Just `/review`. Generalising the surface (`ateam codex-tui <slash-command>`) is one PR later, not now.
5. **Stream output back live, or only on completion?** Only on completion for v1. `StartPipePane` is the path to live streaming if we ever want it.

## What this is *not*

A general-purpose interactive-agent framework. We are deliberately writing the minimum code that calls `/review` reliably. Every project in the research that grew beyond this scope (multiclaude, gas town, amux) did so because they needed multi-agent coordination, persistent sessions, or watchdog supervision — none of which apply to a synchronous "invoke one slash command and return" use case. Resist scope creep.

## Out-of-scope follow-ons (parking lot)

- Headless Codex via `codex app-server` JSON-RPC — separate adapter, study Harnex.
- Multi-turn interactive Codex — not currently planned.
- Live output streaming to the operator — `StartPipePane` exists if we ever want it.
- A general `ateam tui` command for arbitrary CLIs — premature; build this first, generalise if a second use case appears.
