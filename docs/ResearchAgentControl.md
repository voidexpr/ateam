# Research: Agent Control — June 2026 refresh

This refreshes the original `ResearchAgentControl.md` for two reasons. First, ATeam has shipped most of the v1 surface (run-all, report/review/code/verify, exec/parallel, ps/tail/inspect/resume, cost, serve, the 11 built-in roles, three Docker isolation modes, codex-tmux as an experimental backend), so the comparison points have moved. Second, the design space itself has shifted: Claude Code's hook surface has expanded substantially, several of the projects covered last time have either grown ~10x or gone effectively dormant, and a tmux-in-Docker mode for ATeam is the next likely step. The original document treated tmux as one option among many; this one assumes ATeam will adopt tmux+Claude for at least some agent types and concentrates the practical patterns.

The headline conclusion up front, so the rest of this document has a frame: **most of the tmux keystroke-injection complexity in the original doc is now obsolete for new builds**. Claude Code's June-2026 hook surface (PermissionRequest with allow/deny decisions, PreToolUse with `if`-rule matchers, Notification with `terminalSequence`, plus the existing SessionStart/Stop/UserPromptSubmit) replaces almost every reason a tmux harness used to scrape `capture-pane` for dialog text or auto-approve permission dialogs by sending `Enter` keystrokes. The patterns are still documented below because the existing projects use them and ATeam will inherit some of that legacy on day one — but the recommended posture is to lean on hooks first and use tmux scraping only for the gaps hooks don't cover.

A note on the doc shape: per request, this document drops the C.1 / B.2 / §-numbered sections from the previous version. Headings are flat and ordered roughly by what matters most for the upcoming tmux+Claude work. Cross-references are by title.

## What ATeam looks like today

Quick reference so the recommendations below are concrete, not abstract.

The pipeline is **four stages, all driven from one supervisor**: `ateam report` runs role-specific audits in parallel into markdown files; `ateam review` reads all reports and produces a prioritised task list; `ateam code` delegates the top tasks to coding agents and commits; `ateam verify` inspects the commits and runs tests. `ateam run-all` chains them. Each stage is also usable standalone, and `ateam exec` / `ateam parallel` are the lower-level primitives for any other workflow.

The runtime layer drives **Claude Code via `claude -p` with `--output-format stream-json`** and **Codex via `codex exec`**. An experimental third backend, **`codex-tmux`**, drives Codex inside a tmux session so TUI-only commands like `/review` can run unattended — this means ATeam has already crossed the tmux line, just for one specific backend and one specific reason (the Codex TUI). The patterns this document recommends will mostly generalise that experimental code path.

Isolation is **four modes selected by profile**: built-in agent sandbox (default), Docker one-shot (`--profile docker`), Docker exec into a long-lived user-managed container (`--profile docker-exec`), and ATeam-itself-inside-a-container (the container is the boundary, the agent runs un-sandboxed inside it). The artifact layout is markdown files in `.ateam/` per project and `.ateamorg/` per org, with embedded defaults underneath; prompts and config resolve project → org → embedded with composable pre/post fragments.

Observability is **`ateam ps` / `ateam tail` / `ateam inspect EXEC_ID [--auto-debug]` / `ateam cost`**, plus a web UI via `ateam serve` and a static snapshot via `ateam export`. The CallDB SQLite file at `.ateam/state.sqlite` is the source of truth for run state, cost, and process tracking; `ateam resume EXEC_ID` turns any past unattended run into an interactive session by `--resume`-ing the original Claude/Codex session.

Two things this document assumes about where ATeam is going. First, the **tmux-in-Docker mode is the most likely next backend** — the codex-tmux precedent exists, the user has called out tmux+Claude as a near-term focus, and most of the open-question features (mid-task steering, reactive context injection, `--resume`-based crash recovery) require a long-lived session that `claude -p` doesn't provide. Second, **`--dangerously-skip-permissions` should not be the only line of defence**. Hook-based permission gating gives finer-grained control without leaving the Docker boundary as the only safety net.

## What changed in Claude Code itself

The single biggest delta since the original doc was written. Many of the workarounds documented in §C.2–§C.7 of the original now have first-class hook equivalents.

**Hook types are now plural.** The hook config still nests under `hooks` in `settings.json`, but a single hook entry can now be one of five types: `command` (shell, the original), `http` (POST to an endpoint), `mcp_tool` (call an MCP server tool), `prompt` (single-turn LLM evaluation), or `agent` (experimental subagent with tool access). For ATeam's purposes, `command` and `http` are the load-bearing ones — `http` is especially useful in the tmux-in-Docker model because the container can call back to a small HTTP endpoint on the host without needing `docker exec`.

**Permission handling is now hook-shaped, not keystroke-shaped.** The new `PermissionRequest` hook fires when Claude Code is about to show a permission dialog. The hook can return `{"hookSpecificOutput": {"decision": {"behavior": "allow"}}}` or `{"behavior": "deny"}`, and can also rewrite the tool input via `updatedInput`. The complementary `PermissionDenied` hook fires after the auto-mode classifier denies a call, with an option to retry. Together these replace the Gas Town `AcceptStartupDialogs()` + tmux send-keys pattern and the `claude-yolo` capture-pane-polling auto-approval daemon for the common case — there is no longer a reason to scrape pane text looking for "Allow / Deny" buttons.

**`PreToolUse` matchers now accept `if` rules.** The matcher can specify both a tool name (`Bash`, `Edit|Write`) and a permission-rule `if` filter (`Bash(rm *)`, `Edit(*.ts)`). This is the difference between "run this hook on every Bash call and let it figure out whether to block" and "only run this hook for `rm` commands." For ATeam this matters because the role-prompts can stay clean while a `.claude/settings.json` shipped with the container enforces "no `rm -rf`, no `sudo`, no writes outside the workspace" deterministically.

**`Notification` hooks can emit terminal escape sequences.** Returning `{"terminalSequence": "\033]777;notify;Claude Code;Needs input\007"}` from a `Notification` hook produces a desktop notification (or a tmux status update, depending on the receiver) without the hook needing a TTY of its own. ATeam can wire this to update `ateam tail` / `ateam ps` state files directly from inside the container, instead of inferring state from stream-json events.

**New lifecycle hooks worth noting.** `PostToolBatch` fires after every parallel tool call in a batch resolves but before the next model turn — the natural choke-point for "did anything change that I need to react to before continuing." `CwdChanged` fires when the agent `cd`s somewhere new — useful for direnv-style env reloads in a long-lived session. `FileChanged` fires when a watched path on disk is touched — the natural surface for "the coordinator wrote to the mailbox, deliver it to the agent." `UserPromptExpansion` fires when a slash command expands into a prompt — useful for gating `/deploy` style commands at the container boundary even if the operator is in the loop via `ateam resume`.

**Two settings-level autonomous-mode fixes.** Gas Town's recent commits identify two Claude Code settings that should be set for any unattended session: `feedbackSurveyRate: 0` (suppresses the "How is Claude doing this session?" survey modal, which captures stdin and freezes the pane until dismissed) and `awaySummaryEnabled: false` (suppresses the "※ recap" shown after a session returns from idle, same stdin-capture problem). Both should be in the `.claude/settings.json` baked into ATeam's Docker images and the codex-tmux profile.

**`SessionStart` can now return `initialUserMessage`** for non-interactive mode (`claude -p` and equivalents). For ATeam the implication is subtle but real: the role-prompt + run-context injection that ATeam currently does by composing the `-p` argument could move into a `SessionStart` hook with `additionalContext` and `initialUserMessage`, which would let ATeam reuse the same session-start logic across interactive (`ateam resume`) and unattended (`ateam run`) modes.

**The Remote Control protocol exists but isn't ready.** Anthropic ships an official `claude remote-control` / `/remote-control` feature that bridges `claude.ai/code` and the mobile app to a local Claude Code session via a long-polling `/v1/environments/bridge` endpoint and an inner WebSocket `--sdk-url` ingress. The protocol is documented at code.claude.com/docs/en/remote-control but is positioned as user-facing remote access, not a programmable harness API. The reverse-engineering write-ups linked in the original doc (sorrycc's bridge spec, The Vibe Company's `WEBSOCKET_PROTOCOL_REVERSED.md`) are still the best public references. ATeam shouldn't build on the WebSocket layer yet — it's undocumented, unstable, and the public hook API now covers most of what people used to want it for.

## Tmux + Claude Code, condensed: the patterns ATeam needs

This is the core of the refresh. The original doc spread these patterns across §C.2–§C.10; this section consolidates them, weighted by what's actually load-bearing in 2026 for a system like ATeam.

### When tmux earns its complexity

Three things change when you move from `claude -p` to tmux+Claude. You gain **mid-task steering** (inject context into a running session), **`--resume` crash recovery** (continue after the container dies), and **interactive `ateam resume`** (the human attaches to the live session). You pay with **timing-sensitive keystroke injection**, **a capture-pane scraping loop**, **session lifecycle management**, and **a longer-lived container** that has to outlive a single turn.

The honest answer is that for ATeam's audit roles (report stage) the trade isn't worth it — the prompt is fixed at launch, the output is a markdown report, the run either succeeds or fails. For implement-mode work (`ateam code` and `ateam verify`) the trade flips: long runs are more likely, mid-task corrections actually help, and a CI failure mid-implement is exactly the kind of event you'd want to react to without re-spawning the agent from scratch.

So the recommended posture for the upcoming work: keep `claude -p` as the default for `report`, add tmux+Claude as an opt-in profile for `code`/`verify` and for codex-tmux's TUI-only use cases. Don't try to move everything at once.

### Where the tmux session should live

Two viable shapes, and the choice matters less than it used to.

The original doc covered **Option A: tmux inside the Docker container, controlled via `docker exec`**, and **Option B: tmux on the host, Docker as a "fat shell"**. Option A is still the right default for ATeam because the container is the unit of isolation and tmux is an implementation detail inside it. The 2026 update is that you don't need `docker exec tmux send-keys` for most things anymore — the hook surface covers the common cases and a bind-mounted mailbox covers the rest. `docker exec` is still useful for `ateam tail` interactive attach, but it doesn't have to be the primary control channel.

A third pattern has emerged that's worth naming: **tmux session inside the container, control via HTTP into a small in-container daemon** (the shape SWE-ReX, sandbox-agent, and OpenHands all converged on independently). The container runs a tiny HTTP server alongside tmux; the host posts JSON commands and reads SSE/JSONL events. This is the cleanest split between "isolation boundary" (container + tmux) and "control plane" (HTTP), and it lets ATeam's runner stay backend-agnostic — Claude or Codex or anything else, the HTTP surface is the same. Not necessary for v1 of the tmux work, but the right target for v2.

### Launching the session safely

Some invariants the existing projects converged on independently. These should all go into ATeam's tmux profile defaults rather than being discovered per-run.

**Resize the virtual terminal generously.** oauth-cli-coder uses 300×100 (`tmux new-session -d -x 300 -y 100`), Multiclaude defaults much smaller and pays for it. Most stuck/done detection failures in the original research came from `capture-pane` returning truncated buffers; a 300-column virtual terminal with 10K+ lines of scrollback eliminates the truncation class entirely.

**Disable copy/scroll mode before sending input.** Gas Town's nudge sequence starts with `tmux send-keys -X cancel` because copy mode silently swallows subsequent keystrokes. Free reliability win.

**Hide tmux from Claude's process tree if you can.** oauth-cli-coder's stealth mode scrubs `TMUX` / `TMUX_PANE` env vars from the child, sets `TERM=xterm-256color`, allocates a fresh PTY via `script`, and uses `setsid` for process-group separation. Claude Code currently doesn't behave differently when it detects tmux, but if it ever starts to (the projects flag this as a real risk), having stealth-mode wrapping already in place is cheap insurance. Inside a Docker container `script` and `setsid` are reliable; the patch is small.

**Bake the right `settings.json` into the image.** At minimum: `feedbackSurveyRate: 0`, `awaySummaryEnabled: false`, `skipDangerousModePermissionPrompt: true` (Gas Town's `settings-autonomous.json` patterns), plus the hook config that handles permissions. Ship this in the Docker image, not as a runtime overlay, so it survives `--resume`.

**Use `--session-id <uuid>` from the start, not just on resume.** Multiclaude's runner assigns a UUID at first launch and uses `--resume <uuid>` on every restart. The alternative — letting Claude auto-generate a session ID and reading it back from JSONL — works but adds a round trip and a parsing failure mode. ATeam should generate the session ID at run-spawn time and store it in CallDB, same as it stores the exec_id.

### Sending input: three channels, ranked

For mid-task steering and reactive context injection, three approaches in increasing order of robustness.

The least robust: **`tmux send-keys` to the live pane**. This is what Gas Town's `gt nudge` does and what most of §C.3 of the original doc covers. It works but inherits a long list of timing constraints: 600ms ESC/Enter delay (must exceed bash readline's `keyseq-timeout`), per-session mutex to prevent concurrent sends, chunked messages over 512 bytes with 10ms inter-chunk delay, 3-retry Enter with 200ms gaps, sanitisation of ESC/CR/BS. ATeam should not implement direct `send-keys` injection unless there's a specific reason — the gotchas are well-documented and the alternatives are better.

The middle option: **`tmux load-buffer` + `paste-buffer`**, which oauth-cli-coder, Multiclaude, and Harnex all use as their default. Binary-safe, no chunking, no per-character timing — load the buffer from a file, paste it in. Named buffers (`ao-{uuid}` in ComposioHQ's pattern) avoid races on concurrent sends. This is what ATeam should use if it does direct injection.

The most robust: **bind-mounted mailbox file + Claude Code hook**. The coordinator writes a markdown file into a bind-mounted directory; a `FileChanged` hook (new in 2026) or a `UserPromptSubmit` hook drains the mailbox into the session context. No tmux involvement, no timing, the message is a file. Gas Town's nudge queue uses the same shape; the new `FileChanged` hook makes it cheaper than before because the container doesn't need its own inotifywait loop. For ATeam, this should be the default mid-task steering channel.

### Detecting idle / done: four signals, ranked

Knowing when a turn finished matters for "send the next prompt" and for "the run is over, archive it." Four signals in decreasing order of reliability.

**Process exit + last `result` event in stream-json.** What `claude -p` gives for free. Reliable but only works for one-shot invocations. ATeam already uses this for the non-tmux backends.

**Claude Code `Stop` hook.** Fires when Claude finishes responding. Hook can write to a state file via bind mount, post HTTP back to the host, or emit a terminal sequence. This is the cleanest signal for a long-lived tmux session — the agent itself tells you when it's done, no scraping required. ATeam should wire `Stop` to write a sentinel file in the workdir (or update CallDB directly via a small `ateam` subcommand the container can call).

**Claude Code session JSONL tail.** The session writes events to `~/.claude/projects/{encoded-path}/{session-id}.jsonl`. The ComposioHQ pattern (read the last entry, classify by `type` field) gives `active` / `idle` / `ready` / `waiting_input` / `blocked` / `exited` cheaply. The JSONL is the authoritative state — when stream-json and the JSONL disagree, the JSONL is right. ATeam should bind-mount `~/.claude/projects/` so the host can tail it without `docker exec`.

**`tmux capture-pane` scraping for idle prompt.** The lowest-coordination signal: scrape the visible buffer, look for the `│ >` Claude prompt box. Works without hooks, without JSONL access, without any agent-side cooperation. The cost is per-CLI brittleness (the prompt-box pattern changes between releases) and false positives from spinners. Worth keeping as a fallback but not the primary signal.

The dual-channel idea from ComposioHQ — capture-pane for fast checks, JSONL for authoritative state — is still correct. The 2026 update is that the `Stop` hook is now the cheapest of all, and ATeam should prefer it.

### Permission handling, post-hooks

The original doc spent a lot of words on permission dialog handling: Gas Town's `AcceptStartupDialogs()` keystroke injection, `claude-yolo`'s 0.3s polling daemon, CAO's regex on capture-pane, two-tier signal detection for false-positive prevention. Most of that is now obsolete.

The 2026 recommended stack:

1. **In `settings.json`** baked into the image: `skipDangerousModePermissionPrompt: true` to skip the in-terminal confirmation dialog at startup. Set `feedbackSurveyRate: 0` and `awaySummaryEnabled: false`.
2. **`PermissionRequest` hook** to programmatically allow / deny / rewrite-input. The hook can be a `command` (small shell script that checks against an allowlist), an `http` POST to the host (deferring policy to ATeam's coordinator), or — for complex decisions — a `prompt`-type hook that asks an LLM. For ATeam's Docker model, "allow everything because the container is the sandbox" is reasonable but expressing it as an allow-all hook (rather than `--dangerously-skip-permissions`) leaves the door open to tightening selectively later.
3. **`PreToolUse` hook with `if` rules** for the gaps. The classic example is `Bash(rm *)`-style rules that block destructive commands even inside the container — useful because Docker's filesystem isolation doesn't protect the bind-mounted workspace from `rm -rf .`. ATeam should ship a small set of these in `defaults/.claude/settings.json` and let projects extend them via `.ateam/.claude/settings.json`.

The fallback path — capture-pane scraping for dialog text — should only be added if a dialog appears that hooks don't cover. oauth-cli-coder's LLM-assisted TUI navigation (use Gemini CLI to read the screen and decide which keys to press) is the right escape hatch for novel dialogs.

### Stuck detection: how aggressive to be

The original doc covered four approaches: heartbeat files, capture-pane output scanning, Claude Code hooks, self-scheduling check-ins. The 2026 recommendation is much narrower.

For ATeam's tmux-in-Docker model, **`signal(0)` on the agent PID plus `tmux has-session` plus a JSONL mtime check** is enough. Multiclaude's "Brownian Ratchet" framing makes this point well: if the acceptance gate (test pass / commit lands) is strong, you don't need heuristic stuck-detection. ATeam's audit-then-verify pipeline IS a strong acceptance gate — a report exists or it doesn't, a commit passes verify or it doesn't.

The one heuristic worth keeping is the **per-phase stall preset** that Harnex calls out: `(stall_after, max_resumes)` tuples named per role. ATeam's vocabulary already maps cleanly — `report` gets a short stall, `code` gets longer, `verify` gets longest. Bundling them into named profiles (`plan`/`impl`/`gate` in Harnex's terms) is much cleaner than tuning a global timeout.

Gas Town's spawn-storm detector (5 restarts in 15 minutes → block until manual clear) is the right safety net. Cheap to implement; saves you when something goes systemically wrong at 3am.

### Crash recovery via `--resume`

The strongest argument for tmux+Claude over `claude -p` is `--resume`. When the container dies (OOM, host reboot, kill -9), a fresh container with `claude --resume <session-id>` picks up the conversation history exactly where it stopped. ATeam already does this for `ateam resume EXEC_ID` — the infrastructure is in place, the JSONL session files just need to be in a persistent bind-mounted volume.

The session-id-tracking already happens in CallDB. The remaining pieces are: making sure the bind-mount layout puts `~/.claude/projects/` on a persistent volume (not tmpfs), and adding the `--resume <session-id>` invocation as the second-and-later launch path in the runner.

ComposioHQ's `restore()` flow is a good reference for the full sequence: look up the session UUID, validate the workspace still exists (or restore from archive), destroy old runtime artifacts, prefer `--resume` over fresh launch, archive metadata on `kill()` so future restoration is still possible. Most of these steps map directly to ATeam's existing CallDB rows + runtime cleanup logic.

### Inter-agent and coordinator-to-agent messaging

ATeam's runtime is single-agent-per-container; the coordinator is `ateam` itself, not another Claude. So the question is narrower than Gas Town's three-channel (mail / nudge / hooks) system: it's just "how does the supervisor send a message to a running worker."

For the mid-task-steering case, the **bind-mounted mailbox + `FileChanged` hook** path from "Sending input" above is the right primary channel. The supervisor writes a file, the hook drains it. Simple, no `docker exec`, survives container restart, plays well with `--resume`.

For the **reactive context** case (CI failed, review comment posted, coordinator decided something needs to change), the trigger is external — a webhook, a poll loop, or a coordinator-side decision. ATeam's coordinator can write to the same mailbox; the hook treats coordinator-injected and CI-derived messages the same way. ComposioHQ's reactions system is a good config shape to copy: `ci.failing → send-to-agent` with `escalateAfter: 2 attempts` is a clean pattern.

Don't build an inter-agent message bus yet. ATeam's roles are specialised and the orchestration happens at the supervisor level; agents talk to the supervisor (via artifact files), not to each other.

### The `ObservationBackend` shape

Clarp's pluggable observation interface (`prepare / getClaudeEnv / startObserving / stop / onObservation`) is the right architectural shape for ATeam's monitoring layer. Today the runner is tightly coupled to stream-json scraping on stdout; lifting "how we read agent state" behind a small interface — with backends for stream-json, JSONL tail, the new `Notification` hook, and (optionally) HTTP-from-hook — lets ATeam mix and match per profile without touching the runner core.

This isn't critical for the first tmux profile but it's the right factoring to keep in mind as more backends accumulate.

### A small parity test suite is worth its weight

Clarp's `npm run parity:stream` test (run native `claude -p` and clarp against identical prompts, diff the public event stream) is the kind of test ATeam doesn't have but should. Claude Code's stream-json schema has changed between minor releases; pinning the event shapes ATeam reads into a parity test would surface upgrade breaks at CI time instead of at 3am during a `run-all`.

The same idea applies to hooks: a small fixture that exercises every hook ATeam ships in `defaults/.claude/settings.json` and asserts the expected behavior against a current Claude Code release would catch hook-schema drift early.

## The projects, current state as of June 4 2026

Ordered by relevance to ATeam's near-term tmux+Claude work, not by star count. Each entry covers what changed since the original doc and what's currently worth lifting.

### Gas Town (steveyegge/gastown)

**Current state.** 15.7K stars (up from 8.8K — roughly 80% growth in three months), 1.46K forks, MIT, Go, last push June 1 2026, version 1.2.0 released May 27 2026. This is the most actively developed project in this entire research and remains the gold standard for layered watchdogs and autonomous-mode discipline.

**What was added recently that matters for ATeam.** Two settings the project pushes into every autonomous Claude session — `feedbackSurveyRate: 0` and `awaySummaryEnabled: false` — both documented in Gas Town's May-22 commit as fixes for stdin-capture freezes during unattended runs (the "How is Claude doing this session?" survey and the "※ recap" after idle return). These are real bugs that bite long tmux sessions; both should be in ATeam's image-level `settings.json` baseline from day one of the tmux work. The mail system was extended with reply-tracking via `gt mail send` reply-to inference, scheduler latency was tightened, and `bd timeout` diagnostics got process-group-cancellation hardening. None of these are direct lifts for ATeam, but they confirm Gas Town's posture: keep finding small autonomous-mode failure modes and close them.

**What's still worth lifting for ATeam.** Process-group signal handling, crash-loop protection (5 restarts / 15min → block until manual clear), the autonomous-mode settings above, and the "Discover Don't Track" philosophy of reading agent state from artifacts (beads, JSONL, heartbeat files) rather than asking agents to self-report. The five-loop daemon shape — health / messages / wake / worktree-refresh / server, each on its own cadence — remains the cleanest example of "separate jobs at separate cadences" in this section.

**What ATeam shouldn't copy.** The Mayor/Polecat/Witness/Deacon/Refinery vocabulary, the long-lived-per-agent tmux session model, and the LLM-as-coordinator posture. ATeam's coordinator is deterministic Go code calling Claude/Codex; that's a stronger boundary than Gas Town's Mayor and should stay.

### ComposioHQ / agent-orchestrator

**Current state.** 7.4K stars (up from 2.7K — almost 3x), 1.01K forks, MIT, TypeScript, last push June 1 2026. Description updated to: "Agentic orchestrator for parallel coding agents — plans tasks, spawns agents, and autonomously handles CI fixes, merge conflicts, and code reviews." Active and growing fast.

**Still relevant patterns.** The reactions system (event → action mapping with `escalateAfter` duration-or-count thresholds, trackers that reset on state change) is the single best abstraction in this section for "how does the coordinator decide when to give up and escalate." ATeam should adopt this shape verbatim for the reactive triggers when they ship — `ci.failing → send-to-agent` with `escalateAfter: 2 attempts`, `review.changes_requested → send-to-agent` with `escalateAfter: 30m`, `session.stuck → notify`.

**JSONL side-channel for activity detection** remains the right reference. ComposioHQ's backwards-reading algorithm (4KB chunks from the end of the JSONL) to find just the last entry is the right shape for ATeam's stream-json monitoring — don't tail forever, just read the last event.

**`ao send` for mid-task injection** is now overshadowed by the new `FileChanged` hook, but the wait-for-idle-then-send sequence is the right semantics: don't send while the agent is mid-turn, queue and deliver on idle.

### CAO (awslabs/cli-agent-orchestrator)

**Current state.** 675 stars, 121 forks, Apache-2.0, Python, last push June 4 2026 (today). Steadily active.

**Worth lifting.** The provider-specific regex pattern matching for COMPLETED state — Claude Code uses response marker `⏺` plus idle prompt `[>❯]`, Q CLI / Kiro CLI use a different signature — is a useful reference for ATeam if it ever supports more agents than Claude/Codex. The state priority order (`PROCESSING → WAITING_USER_ANSWER → COMPLETED → IDLE → ERROR`) is a cleaner taxonomy than ATeam currently has.

**The flow/cron scheduling** with markdown-frontmatter cron expressions and a script-gate for conditional execution maps to ATeam's role-based execution model with `--roles X,Y,Z`. APScheduler's CronTrigger with a 60s poll is the right shape; ATeam currently relies on the OS cron and could plausibly absorb this — but probably not worth a dependency on APScheduler when external cron + `ateam run-all` already works.

**Less relevant.** The MCP-based supervisor↔worker communication (handoff/assign/send_message) is more ceremony than ATeam needs — markdown files plus the new `FileChanged` hook cover the same ground.

### amux (mixpeek/amux)

**Current state.** 220 stars, single-file Python + tmux, MIT + Commons Clause, last push June 3 2026. New addition to this doc — covered in the Frameworks research but not the Control research originally. Worth pulling in here because the self-healing watchdog patterns are directly applicable to tmux-in-Docker.

**Worth lifting.** The four self-healing conditions:

- **Context usage <50% remaining → send `/compact`**, with a 5-minute cooldown. ATeam should add this to any long-running tmux session.
- **Redacted-thinking corruption in pane → restart session, replay last message.** Specific Claude Code failure mode; the detection is a pane-content match plus a `--resume`.
- **Stuck prompt (with `CC_AUTO_CONTINUE=1`) → auto-respond based on prompt shape.** Less useful with the new hook surface but a reasonable fallback.
- **Fleet-wide `/rate-limit-options` prompt → press option 1, parse reset time from scrollback, schedule resume nudge.** With `capped` default (max 3 auto-resumes per session per day). Directly portable; ATeam has no equivalent today.

**The atomic-claim SQLite kanban** (CAS task claiming so two agents can't grab the same task) is the right primitive for ATeam's CallDB once concurrent runs start touching the same workdirs. Cheaper than per-run locks.

**The single-file Python posture** — entire app including UI in one file with auto-restart on edit — isn't directly applicable to a Go project but the principle (a self-contained dashboard with zero dependencies beyond `tmux` and `python3`) is the right ambition shape for ATeam's `ateam serve` if/when it grows.

### oauth-cli-coder (codeninja/oauth-cli-coder)

**Current state.** 165 stars (up modestly from ~140), Python, no license file, last push April 13 2026 — about seven weeks ago. Not abandoned but slowing; the README hasn't gained new features recently.

**Still the canonical reference for.** Stealth mode (`TMUX` / `TMUX_PANE` env scrub + fresh PTY via `script` + `setsid`) — directly portable to ATeam's container adapter. 300×100 virtual terminal sizing as a free reliability win. `load-buffer` + `paste-buffer` as the default input path. LLM-assisted TUI navigation as an escape hatch for unknown dialogs (use Gemini CLI to read the screen). OAuth-inheritance posture as the precedent for ATeam running on a Claude subscription rather than API tokens.

**Mildly stale.** No new patterns since the original write-up; the recent additions to Claude Code's hook surface aren't reflected in the library. Library-shape, not platform-shape, so the slow cadence is fine.

### clarp (dn00/clarp)

**Current state.** 23 stars, TypeScript, MIT, created May 20 2026, last push May 30 2026. Very new — about two weeks old. Active so far but unproven.

**Why it still matters.** The premise — wrap interactive Claude Code so it exposes the `claude -p` protocol but bills against the subscription — is a direct response to the cost arithmetic ATeam will face when the mid-June 2026 Claude subscription price increase for unattended use kicks in (the README literally calls this out). ATeam should look at clarp as a candidate optional backend for cost-sensitive deployments.

**Patterns worth lifting.** PID-file polling (`~/.claude/sessions/{pid}.json` for `busy`/`idle`/`waiting`) as a state channel — strictly cheaper than parsing stream-json for "is the agent idle now." The `ObservationBackend` interface as the right factoring for ATeam's monitoring layer. `ANTHROPIC_BASE_URL` env-var redirection as a one-line injection point for per-request observability (a logging proxy in front of Claude gives ATeam per-request cost attribution without any agent-side change). The schema-pinned parity test discipline (`npm run parity:stream`).

**Caveats.** New, small, and the README is honest that "if Claude stops honoring `ANTHROPIC_BASE_URL`, the proxy approach fails." Not safe to depend on as a primary path; safe to track as an alternative backend.

### Harnex (jikkuatwork/harnex)

**Current state.** 5 stars, Ruby, MIT, last push May 26 2026. Tiny and stable; useful as a reference, not as something ATeam would depend on.

**The one durable contribution.** Phase-tuned stall presets — `plan` / `impl` / `gate` mapping `(stall_after, max_resumes)` per role — is the cleanest expression in this section of "stop tuning a global timeout, use role-specific bundles." ATeam's vocabulary already matches (audit/implement/verify); this is a small UX/config improvement that's a one-day project.

**Per-adapter completion detection** (Codex JSON-RPC `task_complete` for Codex, scrollback prompt detection for Claude) is the right shape if/when ATeam grows more backends. Codex's app-server JSON-RPC path (requires Codex CLI ≥ 0.128.0) gives structured task-completion events for free.

**Dispatch JSONL at the harness level** (`stalls`, `force_resumes`, `disconnections`, `lines_changed`, `output_lines` per dispatch) is the right granularity for ATeam's CallDB to expose — already partly there, but the `lines_changed` and `force_resumes` columns aren't.

### Multiclaude (dlorenc/multiclaude)

**Current state.** 550 stars (barely moved from 548), Go, no license file, **last push January 28 2026 — over four months ago**. Effectively dormant. Not archived, but no signs of active development. The most recent commit was a feature addition (task management diagnostics endpoint) — not a wind-down, but the cadence stopped abruptly.

**Status: stale, mention with caveat.** The patterns are still valid (process-liveness as the only stuck signal, five separately-cadenced daemon loops, `workspace/<name>` branch convention, markdown-file agent definitions with local/repo/merged precedence, persistent-vs-transient agent distinction). The "Brownian Ratchet" philosophy — accept redundant work, gate only on CI/tests — is still a worthwhile mental model for ATeam's acceptance gates.

**What's at risk by depending on it.** It's a single-maintainer project that's been quiet for four months; updates to Claude Code's CLI surface (new `--session-id` semantics, hook changes) won't necessarily get reflected. Treat as a design reference, not a dependency.

### Claude Agent SDK (Python and TypeScript)

**Current state.** Python SDK: 7.2K stars, MIT, last push June 3 2026. TypeScript SDK: 1.5K stars, last push June 3 2026. Both active and current with the CLI.

**Why it matters for ATeam.** The SDK is now the recommended way to drive Claude Code programmatically. It spawns `claude` as a subprocess and exposes structured JSON over stdin/stdout, including the **programmatic permission callback** channel (`control_request` / `control_response`) that lets an orchestrator allow/deny individual tool calls without `--dangerously-skip-permissions`. This is strictly more powerful than the hook-based approach for ATeam's coordinator: hooks fire inside the container and have to either decide locally or call back to the host; the SDK's control channel runs the decision on the host directly.

**Permission modes available**: `default`, `acceptEdits` (auto-accept file edits, prompt for Bash), `bypassPermissions`, `plan` (read-only). The `acceptEdits` mode is interesting for ATeam's `code` stage — accept all writes but require approval for Bash.

**Known production issues that haven't been resolved.** The "missing final `result` event" hang ([#1920](https://github.com/anthropics/claude-code/issues/1920)) and the silent mid-task hang ([#28482](https://github.com/anthropics/claude-code/issues/28482)) are both still flagged as production-blocking. ATeam's runner needs an external watchdog timeout regardless of which backend it uses — currently the `agent_in_container.sh` script doesn't have one. This should be the first hardening to ship alongside the tmux work.

**Process-group signal self-kill in Docker** ([#16135](https://github.com/anthropics/claude-code/issues/16135)) only triggers when Claude Code's background process manager is invoked; `claude -p` doesn't hit this. For tmux+Claude in Docker, the background-process manager could be triggered — worth knowing about, worth a `process-group-isolation` flag in the tmux profile.

### The tmux-orchestrator pattern umbrella

**Current state.** The pattern (Jedward23's shell scripts, claude_code_agent_farm's Python, ittybitty's bash, claude-yolo's permission daemon) is largely historical now. The individual implementations haven't moved much and the patterns they pioneered are mostly subsumed by the new hook surface and the dedicated tmux libraries (oauth-cli-coder, the Multiclaude `pkg/tmux/` library).

**Still useful as a reference for.** The fundamental tmux session control commands (`new-session -d`, `send-keys -l`, `capture-pane -p -S -N`), the load-buffer/paste-buffer pattern for binary safety, exponential backoff restart, file locking for `~/.claude` config corruption. None of these need a project dependency — they're patterns that go directly into ATeam's container adapter.

## Non-tmux alternatives, compressed

The original doc's §C.11 covered this in detail. The 2026 update is brief: nothing has fundamentally changed about the design space, but the relative weights have shifted.

**Subprocess pipes via the Claude Agent SDK** is now the recommended non-tmux path. It's where Anthropic is putting engineering effort, the permission-callback channel is genuinely powerful, and the SDK is in sync with the CLI. ATeam's current `claude -p` invocation could plausibly be replaced by the Python SDK without losing much — and would gain the programmatic permission control. The trade-off is one more dependency and slightly less direct shell-out control.

**Docker one-shot (ATeam's current model)** remains the simplest. The only change to recommend is adding an external watchdog timeout for the missing-`result`-event bug — the script currently waits indefinitely.

**API-based agent loops** (CrewAI, LangGraph, raw Anthropic SDK) trade simplicity for control. LangGraph's `interrupt()` for deterministic human-in-the-loop suspension is still the most interesting pattern in this space, but it's a different architectural commitment than what ATeam has. Not a near-term direction.

**REST/WebSocket server inside container** (SWE-ReX, sandbox-agent, OpenHands) remains the most architecturally sound non-tmux interactive pattern. OpenHands' event-sourced append-only log for sessions is the gold standard for crash recovery via replay. If ATeam ever wants interactive sessions in Docker without the tmux complexity, this pattern is preferable.

**Platform-managed execution** (GitHub Copilot Coding Agent, Amazon Q Developer) is tightly coupled to specific platforms and outside ATeam's positioning.

**Hidden PTY + HTTP proxy** (clarp) is covered above as a subscription-cost adapter.

## What ATeam should actually do next

Concrete recommendations, ordered by what to do first.

**Add an external watchdog timeout to `agent_in_container.sh`.** The missing-`result`-event bug and the silent mid-task hang are both still open against Claude Code. Watch stream-json output; if no events arrive for a configurable threshold (default 10min), kill the container and mark the run as hung in CallDB. This is the single most cost-effective hardening — it applies to every backend, current and future.

**Bake autonomous-mode settings into the Docker image.** `feedbackSurveyRate: 0`, `awaySummaryEnabled: false`, `skipDangerousModePermissionPrompt: true` in `/root/.claude/settings.json` inside the image. Plus a minimal hook config: `PermissionRequest → allow-all` (since the container is the sandbox), and `PreToolUse` with `Bash(rm -rf *)` / `Bash(sudo *)` rules that deny. This is independent of the tmux work and improves the existing Docker profile immediately.

**Add phase-tuned stall presets.** A small config addition: `(stall_after, max_resumes)` per role, with sensible defaults — `report` short, `code` longer, `verify` longest. Harnex's `plan`/`impl`/`gate` naming maps directly to ATeam's role taxonomy. One day's work.

**Ship the tmux-in-Docker profile as opt-in for `code` and `verify`.** Build on the codex-tmux precedent. Use bind-mounted mailbox + `FileChanged` hook for mid-task steering. Use `Stop` hook for done detection. Use `PermissionRequest` hook for permission gating. Generate `--session-id` at run-spawn; persist `~/.claude/projects/` on a bind-mounted volume for `--resume` recovery. Don't try to support tmux for `report` — `claude -p` is the right shape there.

**Add the JSONL side-channel to the monitoring layer.** Tail `~/.claude/projects/{encoded-path}/{session-id}.jsonl` from the host (bind-mount makes it free) and use ComposioHQ's backwards-reading algorithm to classify the last event into `active`/`idle`/`ready`/`waiting_input`/`blocked`/`exited`. This becomes the authoritative state, with stream-json as the secondary channel.

**Add `ANTHROPIC_BASE_URL` injection capability.** Even without building a full logging proxy, wiring the container to optionally point at a localhost proxy gives ATeam a one-line surface for per-request observability and replay capture. Worth doing once; uses optional once it exists.

**Adopt the `ObservationBackend` shape if the monitoring layer grows past two channels.** Not necessary for v1 — but if ATeam ends up with stream-json + JSONL + hook-callback + clarp-proxy backends, lift them behind a small Go interface so the runner doesn't grow N branches.

**Don't build inter-agent messaging.** Keep the supervisor-as-coordinator model. Agents talk to the supervisor via files; the supervisor talks to agents via mailbox + hook. No need for channels, mail systems, or kanban primitives at this stage.

**Don't build on the Remote Control WebSocket protocol.** It's undocumented, unstable, and the public hook surface now covers most of what people wanted it for. Track it; don't depend on it.

**Watch the subscription-cost story closely.** Clarp's existence flags a real economic pressure. The mid-June 2026 Claude subscription price change for unattended use will land before any of the above work fully ships. ATeam's positioning is already aligned with "run on whatever pricing makes sense" — the concrete next step is probably an optional clarp-style adapter behind the `ObservationBackend` interface, but only after the primary tmux profile is stable.
