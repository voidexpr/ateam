# Research: Agent Control

## C. Agent Control

How do existing frameworks actually control coding agents at the process level? This section compares the mechanisms used by four frameworks for: session lifecycle, stuck/idle detection, permission handling, done detection, message injection, and crash recovery.

### C.1 Control Patterns Overview

All four frameworks face the same fundamental problem: coding agents (Claude Code, Codex, etc.) are interactive CLI programs designed for human use. Running them unattended requires solving several sub-problems:

1. **Launching** — start the agent with the right context, permissions, and workspace
2. **Monitoring** — know what the agent is doing without interrupting it
3. **Prompting** — handle permission dialogs, trust prompts, and interactive questions
4. **Injecting** — send new instructions or context to a running agent mid-task
5. **Detecting completion** — know when the agent is done
6. **Recovering** — handle crashes, stuck agents, and resource exhaustion

Two runtime models dominate: **tmux sessions** (Gas Town, CAO, agent-orchestrator's default) and **Docker containers** (ATeam's approach). The tmux model treats agents as long-lived interactive processes; the Docker model treats them as one-shot batch jobs.

### C.2 Gas Town (steveyegge/gastown)

Gas Town runs agents as **long-lived tmux sessions** on the bare host. Each agent ("Polecat") gets a dedicated tmux session, a git worktree, and a persistent identity. The system includes a Mayor (LLM coordinator), Witness (health monitor), Deacon (work dispatcher), and Refinery (merge queue).

#### Stuck Agent Detection

Detection is layered across three subsystems:

**Witness patrol** (`internal/witness/handlers.go`): Runs periodic scans. `DetectStalledPolecats` flags a session as "stalled at startup" when session age exceeds 90 seconds AND last tmux pane activity is older than 60 seconds. `DetectZombiePolecats` checks two dimensions: session-dead (tmux session absent while bead shows `working`/`running`) and agent-dead (tmux session alive but Claude/Node process gone).

**`CheckSessionHealth`** combines three checks: `HasSession()` (tmux present?), `IsAgentAlive()` (Claude process present?), and `GetSessionActivity()` (output within threshold?). Returns one of: `SessionHealthy`, `SessionDead`, `AgentDead`, `AgentHung`. The hung threshold is **30 minutes** of tmux inactivity.

**`IsAgentAlive`** reads `GT_PROCESS_NAMES` from the tmux session environment to know which process names to look for. Uses `ps -p <pid> -o comm=` for the pane's main PID, plus recursive traversal of up to 10 levels of child processes via `pgrep -P`.

**Heartbeat files** (`internal/polecat/heartbeat.go`): Each agent writes a JSON timestamp to `.runtime/heartbeats/<session>.json` whenever a `gt` command runs. Staleness threshold: **3 minutes**.

**Daemon loop** (`internal/daemon/daemon.go`): Runs every **3 minutes**. Checks: Deacon/Witness/Refinery liveness, Polecat session validation, GUPP violations (agent has work but not progressing — threshold: **30 minutes**), orphaned work, orphaned Claude sub-processes. Also includes **mass death detection**: 3+ session deaths within 30 seconds triggers an alert.

#### Permission and Dialog Handling

Gas Town does NOT rely solely on `--dangerously-skip-permissions`. It uses a multi-layer approach:

**`settings-autonomous.json`** is passed via `--settings` to Claude Code. Contains `"skipDangerousModePermissionPrompt": true` to bypass the in-terminal confirmation dialog.

**`AcceptBypassPermissionsWarning`** handles the "Bypass Permissions mode" startup dialog by reading the tmux pane via `CapturePane`, checking for the dialog text, then sending `Down` + `Enter` keystrokes to select "Yes, I accept".

**`AcceptWorkspaceTrustDialog`** handles the workspace trust dialog (introduced in Claude Code v2.1.55) by checking for "trust this folder" or "Quick safety check" in the pane, then sending `Enter`.

Both are called together via `AcceptStartupDialogs()`, invoked both during `StartSession()` and proactively by `DetectStalledPolecats()` for sessions stuck at startup — a common failure mode.

#### Message Injection (gt nudge)

Three delivery modes:
- **`immediate`** (default): direct tmux keystroke injection, may interrupt in-flight work
- **`queue`**: write to file queue, picked up at next `UserPromptSubmit` hook
- **`wait-idle`**: poll for idle state (15s timeout), then deliver; fallback to queue

The immediate injection sequence in `NudgeSession`:
1. Acquire per-session mutex lock
2. Resolve agent pane in multi-pane sessions
3. Exit copy/scroll mode (would intercept keystrokes) — sends `-X cancel`
4. Sanitize: strip ESC, CR, BS; replace TAB with space
5. Send via `send-keys -l` (literal mode). Messages >512 bytes are chunked with 10ms inter-chunk delay
6. Wait 500ms for delivery
7. Send `Escape` (exit vim INSERT mode; harmless in normal mode)
8. Wait **600ms** (must exceed bash readline `keyseq-timeout` of 500ms to prevent ESC+Enter being merged)
9. Send `Enter` with up to 3 retries (200ms between attempts)
10. Send `SIGWINCH` to wake detached sessions

Queue-based nudges are stored as JSON files in `.runtime/nudge_queue/<session>/`. TTLs: normal = 30 minutes, urgent = 2 hours. Max 50 queued per session. Drain is atomic (rename to `.claimed`) to prevent concurrent delivery. Injected as `<system-reminder>` blocks.

#### Done Detection

Primary: agents call **`gt done`** which writes completion metadata to the bead (`exit_type`: COMPLETED/ESCALATED/DEFERRED), transitions state to `idle`, and sends a tmux nudge to Witness.

Backup: the Witness's **"Discover Don't Track" pattern** scans agent beads for `exit_type` and `completion_time` fields on each patrol cycle, not relying exclusively on the mail notification.

Dead sessions with a `done-intent` label less than 30 seconds old are assumed to have exited normally.

#### Crash Recovery

**Restart-first policy**: `gt session restart` kills the tmux session and spawns a fresh one. The polecat's existing hook bead and git worktree are preserved — the agent picks up where it left off via `gt prime`.

**Auto-respawn tmux hook**: installed via `run-shell -b` on pane-died events. Waits 3 seconds, checks if pane is still dead, then runs `tmux respawn-pane`.

**`NukePolecat` safety gates**: refused if the polecat has a pending MR in the refinery. For zombies with uncommitted work, escalates rather than nuking.

**Crash loop protection** (`RestartTracker`): initial backoff 30s, max 10min, 2x multiplier. 5 restarts within 15 minutes → blocked until manual `gt daemon clear-backoff`. Stability period: 30 minutes of uptime resets the counter.

**Stale hook recovery**: beads with `hooked` status and no live agent session are reset to `open` after **1 hour**.

**Redispatch**: up to 3 attempts per bead, 5-minute cooldown between attempts, then escalate to Mayor. A **spawn storm** detector flags beads respawned more than twice.

#### Context Injection at Startup (gt prime)

`gt prime` is the context injection entrypoint, called at session start via the `SessionStart` hook. It outputs: session metadata, full role context (300-500 lines from embedded docs), handoff content, and pending mail. If hooked work exists, outputs "AUTONOMOUS WORK MODE" with immediate-execution instructions and the bead description. After context compaction, a lighter version is injected to avoid disrupting workflow continuity.

### C.3 Tmux Orchestrator Pattern

"Tmux Orchestrator" is not a single project but a **pattern with multiple independent implementations**, all built around the same core idea: use tmux as the process manager for AI CLI agents with a supervisor layer that sends keystrokes in, reads terminal output back, and reacts.

Key implementations: [Jedward23/Tmux-Orchestrator](https://github.com/Jedward23/Tmux-Orchestrator) (shell scripts), [Dicklesworthstone/claude_code_agent_farm](https://github.com/Dicklesworthstone/claude_code_agent_farm) (Python, 20+ parallel agents), [mixpeek/amux](https://github.com/mixpeek/amux) (Python + REST API), [adamwulf/ittybitty](https://github.com/adamwulf/ittybitty) (bash, worktree-isolated), [claude-yolo/claude-yolo](https://github.com/claude-yolo/claude-yolo) (permission auto-approval daemon).

#### Session Control

The fundamental mechanism is universal:
```bash
tmux new-session -s project -d
tmux send-keys -t project:agent-1 "claude --dangerously-skip-permissions" Enter
tmux send-keys -t project:agent-1 "Implement feature X" Enter
```

For large payloads with special characters, `claude_code_agent_farm` uses the tmux buffer API (binary-safe):
```python
tmux("load-buffer", "-b", buf_name, tmp_path)
tmux("paste-buffer", "-d", "-b", buf_name, "-t", target)
```

The Jedward23 architecture uses a 3-tier hierarchy: Orchestrator (window 0) → Project Managers (one window per project) → Engineers (workers).

#### Stuck Agent Detection

Four approaches exist across implementations:

**Heartbeat files** (`claude_code_agent_farm`): Agents touch a file on each command. Monitor checks file mtime — if age > 120 seconds, agent is considered hung and restarted.

**`capture-pane` output scanning** (multiple projects): `tmux capture-pane -p -t SESSION:WINDOW` reads the visible terminal buffer. The orchestrator pattern-matches for signs of activity:
```python
def is_claude_working(content):
    return any(ind in content for ind in ["✻ Pontificating", "● Bash(", "esc to interrupt"])
def is_claude_ready(content):
    return any(["Welcome to Claude Code!" in content, "│ >" in content])
```

**Claude Code hooks** (`ittybitty`, `claude-code-manager`, `tmux-agent-indicator`): Claude Code's native `Stop`, `PermissionRequest`, `UserPromptSubmit` hooks fire shell commands on lifecycle events:
```json
{ "hooks": { "Stop": [{ "command": "agent-state.sh --state done" }] } }
```

**Self-scheduling check-ins** (Jedward23): Agents call `nohup bash -c "sleep N && tmux send-keys ..."` at the end of each work session. If an agent dies, it never reschedules, which becomes visible at the next check-in.

#### Permission Handling

**`--dangerously-skip-permissions`**: the simplest approach, used by most for fully unattended operation. Known bugs where it doesn't bypass every prompt (workspace trust, certain mode dialogs).

**Auto-approval daemon** (`claude-yolo`): Polls `capture-pane` every 0.3 seconds. Uses a two-tier detection (primary + secondary signals) to avoid false positives:
- Primary: "Allow" and "Deny" both visible, OR numbered options like "1. Yes / 2. No"
- Secondary: tool keywords (Bash, Read, Write), context phrases ("want to proceed", "permission")
- Safety vetoes: if a slash-command autocomplete menu is visible, do NOT approve (prevents false trigger from the word "permissions" in autocomplete)

Only `Enter` works as the approval keystroke (not `y`) — Claude Code's prompt has a pre-selected default. A 2-second per-pane cooldown prevents duplicate approvals.

**Hook-based allowlist** (`ittybitty`): `PreToolUse` hook auto-denies tool calls accessing paths outside the agent's assigned worktree.

#### Done Detection

**Filesystem sentinels**: Workers write a `.done` file when finished. Orchestrator polls for it.

**`Stop` hook**: Claude Code's native hook fires when a turn completes. Projects wire this to state files, notifications, or nudges.

**Completion phrases**: Agents are prompted to output specific strings ("WAITING", "I HAVE COMPLETED THE GOAL"). The `Stop` hook scans for these.

**Prompt box reappearance**: `capture-pane` detects the `│ >` prompt box returning — transition from "working" to "ready".

#### Robustness

**Exponential backoff restart** (`claude_code_agent_farm`): `min(10 * 2^restart_count, 300)` seconds.

**File locking**: prevents concurrent Claude Code launches from corrupting shared `~/.claude` config files.

**Self-healing context compaction** (`amux`): when context drops below 20%, automatically sends `/compact` with a 5-minute cooldown.

**Known fragility — tmux send-keys race**: documented [bug](https://github.com/anthropics/claude-code/issues/23513) where `tmux send-keys` fires before the shell in a newly created pane finishes initializing, causing lost commands. Standard workaround: `sleep 1-2` after pane creation.

### C.4 CAO (CLI Agent Orchestrator, awslabs)

Repository: [awslabs/cli-agent-orchestrator](https://github.com/awslabs/cli-agent-orchestrator). A Python orchestration system that manages multiple agent sessions in tmux terminals with a supervisor-worker hierarchy, MCP-based inter-agent communication, and cron-scheduled flows.

#### Session Lifecycle

Every agent gets its own **tmux session** (not just a window), named with prefix `cao-` plus an 8-character hex suffix. A `CAO_TERMINAL_ID` environment variable is injected into the session for process identification.

Full creation flow:
```
generate_session_name() + generate_terminal_id()
  → tmux create_session()
  → SQLite database.create_terminal()
  → provider.initialize()         # starts agent, waits for IDLE
  → tmux pipe_pane(log_path)      # pipes all output to .log file
  → inbox_service.register()      # starts watchdog on log file
```

All terminal output is piped to `~/.aws/cli-agent-orchestrator/logs/terminal/{id}.log`. The SQLite database tracks terminal state.

#### IDLE / Done Detection

Done detection is **provider-specific regex pattern matching** on `capture-pane` output (last 200 lines).

**Claude Code**: COMPLETED when the response marker `⏺` is found AND the idle prompt `[>❯]` is also present.

**Q CLI / Kiro CLI**: COMPLETED when green arrow `>` plus idle prompt pattern are found.

State priority order: `PROCESSING → WAITING_USER_ANSWER → COMPLETED → IDLE → ERROR`. Processing is checked first because spinner patterns can appear alongside prompt patterns during transitions.

**Shell readiness check** during startup: polls with 0.5s intervals up to 10 seconds, checking that two consecutive `capture-pane` reads return the same non-empty output (stability check).

#### Permission Handling

**Claude Code**: launched with `--dangerously-skip-permissions`. The `WAITING_USER_ANSWER` state fires when Claude presents a numbered menu (but trust prompts are excluded via `TRUST_PROMPT_PATTERN`).

**Q CLI / Kiro CLI**: permission prompts detected via `Allow this action?.*[y/n/t]:` pattern. The system checks how many idle lines appear after the last permission match — 0-1 means an active prompt needing response, 2+ means stale (already answered).

When status is `WAITING_USER_ANSWER`, the system surfaces it through the API. The supervisor agent is expected to notice and handle it via `send_message` or `send_input`. No automatic resolution for non-Claude providers.

**Trust prompts** are handled in `_handle_trust_prompt()` during `initialize()`: polls for "Yes, I trust this folder" text and sends `Enter` to accept.

#### Supervisor-to-Worker Communication

Three MCP tools:

**`handoff(agent_profile, message, timeout=600)`** — synchronous, blocking: creates terminal, waits for IDLE, sends message, polls for COMPLETED (up to `timeout` seconds, default 600), retrieves output, sends exit command. Returns the output.

**`assign(agent_profile, message)`** — async, non-blocking: creates terminal, sends message, returns `terminal_id` immediately. The worker calls `send_message` back when done.

**`send_message(receiver_id, message)`** — inbox delivery: queued in SQLite, delivered asynchronously when receiver reaches IDLE. A Python `watchdog` `PollingObserver` monitors log files; when modified, checks for idle patterns before querying terminal status (two-phase approach for performance).

Input injection uses tmux **bracketed paste mode**: `tmux load-buffer` + `paste-buffer`. `paste_enter_count` defaults to 2 (first Enter adds newline in Claude Code's multi-line mode, second submits).

#### Recovery and Robustness

Minimal explicit recovery in the current codebase:
- **Timeout-based**: `wait_until_terminal_status()` returns `False` after timeout
- **Handoff timeout**: 600s default, configurable up to 3600s
- **Inbox retry**: PENDING messages retried on every log file modification event (no max retry count)
- **Cleanup daemon**: removes data older than 14 days
- No automatic agent restart on crash, no heartbeat beyond status polling

#### Flows / Cron Scheduling

Flows are defined as markdown files with YAML frontmatter containing a cron expression. Uses APScheduler's `CronTrigger`. A background daemon polls every 60 seconds for flows whose `next_run <= NOW AND enabled = TRUE`.

An optional **script gate** runs before flow execution: an external script can check conditions and return `{"execute": false}` to abort. This is the primary mechanism for conditional/health-gated execution. Once a flow fires, it is fire-and-forget — no waiting for completion.

### C.5 Agent Orchestrator (ComposioHQ)

Repository: [ComposioHQ/agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator). TypeScript platform managing fleets of parallel coding agents. Agent-agnostic, runtime-agnostic (tmux default, process alternative), tracker-agnostic (GitHub, Linear).

#### Session Lifecycle

Tmux sessions use a two-tier naming scheme:
- User-facing: `{prefix}-{num}` (e.g., `int-1`)
- tmux name: `{hash}-{prefix}-{num}` where hash = `sha256(dirname(configPath)).slice(0, 12)` — prevents collision between different checkouts

Agent launch for commands >200 chars uses `tmux load-buffer` + `paste-buffer` to avoid truncation, falling back to `send-keys` for shorter commands.

Session status state machine:
```
spawning → working → pr_open → ci_failed / review_pending / changes_requested
                              → approved → mergeable → merged
Error paths: needs_input, stuck, errored, killed
```

#### Activity Detection (Dual-Channel)

**Terminal output classifier** (fast, synchronous): scans `capture-pane` output.
```
Last line matches /^[❯>$#]\s*$/ → idle
Last 5 lines contain "Do you want to proceed?" or "(Y)es...(N)o" → waiting_input
Otherwise → active
```

**JSONL-based detection** (authoritative): reads Claude Code's session JSONL files directly at `~/.claude/projects/{encoded-path}/`. Uses a backwards-reading algorithm (4KB chunks) to find only the last entry. Maps entry types:
- `user`, `tool_use`, `progress` → `active` (if recent) or `idle` (if stale)
- `assistant`, `result`, `summary` → `ready` (if recent) or `idle`
- `permission_request` → `waiting_input`
- `error` → `blocked`

The `DEFAULT_READY_THRESHOLD_MS` separates "recently finished" from "stale/idle" based on file mtime.

Per-session enrichment has a **2-second timeout cap** to prevent subprocess calls from blocking the polling loop.

#### Permission Handling

**Not `--dangerously-skip-permissions` by default** — it's opt-in per project:
```yaml
agentConfig:
  permissions: skip    # adds --dangerously-skip-permissions
```

The **orchestrator agent** always gets `permissions: "skip"` since it must run `ao` CLI commands autonomously.

When permissions are not skipped and Claude shows a prompt, both the terminal classifier and JSONL classifier detect `waiting_input`, which maps to `needs_input` session status and triggers the `agent-needs-input` reaction.

#### Reactions System

Event-to-reaction mapping:
```
ci.failing               → ci-failed
review.changes_requested → changes-requested
automated_review.found   → bugbot-comments
merge.conflicts          → merge-conflicts
merge.ready              → approved-and-green
session.stuck            → agent-stuck
session.needs_input      → agent-needs-input
session.killed           → agent-exited
summary.all_complete     → all-complete
```

Action types: `send-to-agent` (inject message into tmux session), `notify` (alert human), `auto-merge`.

Message injection via `sendMessage()`:
1. `C-u` to clear partial input
2. For long/multiline messages: named buffer via `tmux load-buffer` + `paste-buffer` (named `ao-{uuid}` to avoid race conditions on concurrent sends)
3. For short messages: `tmux send-keys -l` (literal mode)
4. 300ms delay, then `Enter`

#### escalateAfter Mechanism

Per-reaction trackers record `attempts` count and `firstTriggered` timestamp, keyed by `"sessionId:reactionKey"`.

`escalateAfter` accepts either a **duration string** (`"30m"`, `"1h"`) or a **count** (numeric). When the threshold is exceeded, the reaction fires a `reaction.escalated` event and notifies the human.

Trackers reset when the session changes status (e.g., CI fails again after a fix — retry counter starts fresh).

Example config:
```yaml
reactions:
  ci-failed:
    auto: true
    action: send-to-agent
    retries: 2
    escalateAfter: 2        # escalate after 2 attempts
  changes-requested:
    auto: true
    action: send-to-agent
    escalateAfter: 30m      # escalate after 30 minutes
```

#### ao send

CLI command for injecting instructions into running sessions. Sequence:
1. Wait for agent to become idle (polls every 5s, configurable timeout)
2. `C-u` to clear partial input
3. Send message (via `send-keys -l` or `load-buffer`/`paste-buffer` for long messages)
4. 300ms delay + `Enter`
5. **Delivery confirmation** (up to 3 retries): wait 2s, check if agent became active or message is queued. Re-send `Enter` if needed.

#### Done / Exited Detection

- **Process exit**: `ps -eo pid,tty,args` to find `claude` process on the tmux pane's TTY
- **tmux session liveness**: `tmux has-session -t id`
- **JSONL `exited` state**: if process not running, `getActivityState()` returns `{ state: "exited" }`
- **PR merge/close**: from SCM plugin
- **`summary.all_complete`**: fires when all sessions reach `merged` or `killed`

#### Session Recovery

`restore()` implements full crash recovery:
1. Look in active metadata, fall back to archived metadata
2. Validate workspace still exists (attempt `workspace.restore()` if missing)
3. Destroy old runtime (in case tmux session survived the agent crash)
4. Prefer `claude --resume {sessionUuid}` over fresh launch (reads session UUID from JSONL filename)
5. Metadata is archived (not deleted) on `kill()`, enabling future restoration

#### Monitoring

30-second polling loop in `lifecycle-manager.ts`. Re-entrancy guard skips overlapping cycles. Probe failure preserves existing `stuck`/`needs_input` status rather than overwriting with `working`. No separate watchdog process.

### C.6 Comparison Table

| Concern | Gas Town | Tmux Orchestrator | CAO | ComposioHQ |
|---|---|---|---|---|
| **Runtime** | tmux on bare host | tmux on bare host | tmux on bare host | tmux (default) or child_process |
| **Session model** | Long-lived, resumable | Long-lived or one-shot | One tmux session per agent | Long-lived, restorable |
| **Stuck detection** | Witness patrol + heartbeat + daemon loop (3min cycle). Hung threshold: 30min inactivity | Heartbeat files (120s), capture-pane scanning, Claude Code hooks | Timeout-based polling only (no watchdog) | Dual-channel: terminal capture + JSONL file mtime. 30s poll cycle |
| **Permission handling** | settings.json `skipDangerousModePermissionPrompt` + tmux keystroke injection for startup dialogs | `--dangerously-skip-permissions` or auto-approval daemon (capture-pane poll, 0.3s interval) | `--dangerously-skip-permissions` for Claude; regex detection for Q/Kiro CLI | Opt-in `permissions: skip` per project. Activity classifier detects `waiting_input` |
| **Trust prompt** | `AcceptWorkspaceTrustDialog` sends Enter | Manual or `--dangerously-skip-permissions` | `_handle_trust_prompt()` sends Enter during init | Not explicitly handled |
| **Message injection** | `gt nudge`: 3 modes (immediate/queue/wait-idle). Literal send-keys, chunked >512B, mutex-locked. 600ms ESC/Enter timing | `tmux send-keys` or `load-buffer`/`paste-buffer` for binary safety | Bracketed paste (`load-buffer` + `paste-buffer`), double-Enter for Claude | `C-u` clear + `send-keys -l` or named buffer. 3-retry delivery confirmation |
| **Done detection** | `gt done` self-report + Witness bead scanning (Discover Don't Track) | `.done` files, Stop hook, completion phrases, prompt box reappearance | Provider-specific regex on capture-pane (response marker + idle prompt) | Process exit + tmux liveness + JSONL state + PR merge |
| **Crash recovery** | Restart-first (preserve worktree+bead). Auto-respawn tmux hook. Crash loop protection (5 restarts/15min → blocked). Stale hook recovery (1hr). Redispatch (3 attempts, 5min cooldown) | Exponential backoff restart. File locking for ~/.claude. Context compaction at 20% | Timeout-based only. No auto-restart | Full restore with `--resume`. Metadata archival. Workspace validation. 2s enrichment timeout |
| **Escalation** | GUPP violation (30min) → Deacon/Mayor. `gt help` → Witness triage. Spawn storm detection | Manual (no built-in escalation) | Supervisor notices via API, handles manually | `escalateAfter` per reaction (duration or count). Tracker resets on state change |
| **Inter-agent comms** | 3 channels: mail (persistent), nudge (real-time), hooks (filesystem) | tmux send-keys between windows | MCP tools: handoff (sync), assign (async), send_message (inbox) | Reactions system: event → action mapping |
| **Scheduling** | Manual dispatch by Mayor | Manual or self-scheduled check-ins | APScheduler cron with script gate | Manual `ao spawn` (no built-in scheduling) |

### C.7 Lessons for ATeam

**Gas Town's robustness is the gold standard** — layered detection (Witness patrol + heartbeat + daemon), crash loop protection with exponential backoff, TOCTOU guards before destructive actions, spawn storm detection, stale hook recovery. ATeam should adopt the crash loop protection pattern and the "Discover Don't Track" philosophy (read state from artifacts, don't rely on notifications).

**The tmux `send-keys` timing problem is real.** Gas Town's 600ms ESC/Enter delay, mutex-locked delivery, chunked messages >512B, and 3-retry Enter are all solutions to actual race conditions. ATeam's Docker+`claude -p` model avoids this entirely — one-shot invocations don't need keystroke injection. This is a significant simplicity advantage.

**Permission handling is a pain point for everyone.** The `--dangerously-skip-permissions` flag doesn't bypass every dialog (workspace trust, certain mode prompts). Gas Town's approach of both using settings.json AND having tmux keystroke handlers for startup dialogs is pragmatic. ATeam's Docker containers can pre-configure the settings file in the image, avoiding runtime dialog handling.

**JSONL/stream-json monitoring is converging as the standard.** Both ComposioHQ and Gas Town read Claude Code's session files directly as a side-channel for activity detection. ATeam's stream-json approach is the same pattern. The dual-channel approach (terminal output for fast checks + JSONL for authoritative state) from ComposioHQ is worth adopting.

**`escalateAfter` is the right abstraction.** ComposioHQ's per-reaction escalation with both duration and count thresholds, plus automatic reset on state change, is clean. ATeam's coordinator should use the same pattern for deciding when to give up on an agent and escalate to human review.

**One-shot vs long-lived is a fundamental tradeoff.** Long-lived sessions (Gas Town, tmux orchestrator) enable mid-task steering, context accumulation, and `--resume` after crashes. One-shot sessions (ATeam's `claude -p`) are simpler, stateless, and avoid the entire class of stuck-session bugs. ATeam's choice is validated by the complexity required to manage long-lived sessions — Gas Town has thousands of lines of session management code that ATeam doesn't need.

### C.8 Hybrid: tmux Inside Docker

ATeam currently uses one-shot `claude -p` in Docker containers — simple, stateless, no session management. The tmux-based frameworks (Gas Town, CAO, tmux orchestrator) use long-lived interactive sessions — more capable, but complex and fragile. A hybrid approach runs tmux *inside* the Docker container, combining Docker's isolation with tmux's interactive control.

#### Why Consider This

`claude -p` is fire-and-forget: the prompt is fixed at launch, there's no way to steer mid-task, and if the agent gets stuck on the wrong approach there's no recourse but to wait for it to finish (or kill it). The tmux-inside-Docker model would allow:

- **Mid-task steering** — inject corrections, additional context, or "stop and try a different approach" without killing the container
- **Reactive context injection** — feed CI failures, review comments, or coordinator decisions to a running agent (the ComposioHQ reactions pattern)
- **Interactive mode with `--resume`** — Claude Code in interactive mode accumulates context across turns; `--resume` can recover from crashes without losing conversation history
- **Multi-turn workflows** — an audit agent could first explore, then the coordinator reviews its findings and sends a follow-up prompt to refine, all within one session
- **Graceful shutdown** — send "wrap up and commit your work" instead of `docker kill`

#### Architecture

Two viable approaches:

**Option A: tmux inside the container, controlled via `docker exec`**

```
Host                          Docker container
─────────────────────────     ──────────────────────────────
coordinator                   tmux server
  │                             └─ session "agent"
  ├─ docker exec ... \              └─ claude (interactive)
  │    tmux send-keys ...
  ├─ docker exec ... \
  │    tmux capture-pane ...
  └─ docker exec ... \
       cat /output/stream.jsonl
```

The container entrypoint starts tmux and launches Claude Code inside it. The host controls the session via `docker exec <container> tmux send-keys ...`. All the tmux patterns from C.2–C.5 apply, just prefixed with `docker exec`.

Dockerfile additions:
```dockerfile
RUN apt-get install -y tmux
```

Container entrypoint:
```bash
#!/bin/bash
tmux new-session -d -s agent -x 200 -y 50
tmux send-keys -t agent "claude --dangerously-skip-permissions" Enter
# Keep container alive while tmux runs
tmux wait-for agent-done  # or: while tmux has-session -t agent 2>/dev/null; do sleep 5; done
```

Host sends a task:
```bash
docker exec $CONTAINER tmux send-keys -t agent "Audit the test coverage of /workspace" Enter
```

Host reads state:
```bash
docker exec $CONTAINER tmux capture-pane -t agent -p    # terminal output
docker exec $CONTAINER cat /output/stream.jsonl          # JSONL side-channel (if configured)
```

Host injects mid-task context:
```bash
docker exec $CONTAINER tmux send-keys -t agent \
  "Actually, focus on the auth module first — CI is failing there" Enter
```

**Option B: tmux on the host, Docker as a "fat shell"**

The tmux session lives on the host. Each pane runs `docker exec -it <container> bash` rather than a local shell. This is simpler conceptually (tmux is just tmux, Docker is just the sandbox) but messier in practice — the tmux session survives the container, creating orphan state.

Option A is cleaner. The container is the unit of isolation, and tmux is an implementation detail inside it.

#### Sending Messages: `docker exec` vs Bind-Mount Mailbox

The `docker exec ... tmux send-keys` approach inherits all the tmux timing fragility documented in C.2–C.5 (ESC/Enter races, chunking, mutex locking). An alternative is a **file-based mailbox**:

```
Host writes:    $OUTPUT_DIR/inbox/001-task.md
Container reads: /output/inbox/001-task.md  (via bind mount)
```

Claude Code's `UserPromptSubmit` hook or a simple inotifywait loop inside the container picks up new files and injects them into the session. This avoids tmux keystroke timing entirely — the message is a file, not a sequence of keystrokes. Gas Town's nudge queue uses the same pattern (§C.2), and it's more reliable than direct tmux injection.

The mailbox approach can be combined with tmux: the container runs tmux internally for session persistence and `--resume` support, but the host communicates via files rather than `docker exec tmux send-keys`.

#### Monitoring: Three Channels

With tmux inside Docker, monitoring can use all three channels simultaneously:

1. **stream-json / JSONL** (existing) — `claude -p --output-format stream-json > /output/stream.jsonl` or Claude Code's session JSONL at `~/.claude/projects/`. File is bind-mounted to host. The host's live monitor (§G in `agent_in_container.sh`) already does this.

2. **tmux capture-pane** (new) — `docker exec $CONTAINER tmux capture-pane -t agent -p -S -50` returns the last 50 lines of terminal output. Enables the terminal-output classifiers from ComposioHQ (§C.5): detect idle prompt, permission dialogs, spinner activity.

3. **Claude Code hooks** (new) — configure `Stop`, `PermissionRequest`, etc. in the container's `.claude/settings.json` to write state files:
   ```json
   { "hooks": { "Stop": [{ "command": "echo done > /output/agent-state" }] } }
   ```
   The host reads `/output/agent-state` via the bind mount. No `docker exec` needed.

The dual-channel approach from ComposioHQ (§C.5) — fast terminal scan + authoritative JSONL — maps directly to channels 2 and 1.

#### Stuck Detection and Recovery

With tmux in the container, stuck detection can go beyond "wait for exit code":

| Signal | Detection method | Action |
|---|---|---|
| Agent idle >5min | JSONL file mtime or `capture-pane` shows prompt | Send follow-up via mailbox |
| Permission prompt | Claude Code hook or `capture-pane` regex | Auto-approve or escalate |
| Agent hung >30min | No JSONL writes, no tmux activity | Send Ctrl-C, then new prompt; or kill and `--resume` in new container |
| Container OOM | Docker event or exit code 137 | Restart container with higher memory, `--resume` previous session |
| Context exhaustion | JSONL `result` event with high token count | Send `/compact` or start new session with summary |

Recovery via `--resume` is the key advantage over `claude -p`. If the container dies (OOM, timeout, crash), a new container can be started with `claude --resume <session-id>`, picking up where the previous session left off. The session JSONL files must be persisted on the host (already the case via bind mount of `~/.claude`).

#### What This Unlocks vs `claude -p`

| Capability | `claude -p` (current) | tmux-in-Docker |
|---|---|---|
| Mid-task steering | No | Yes — mailbox or tmux send-keys |
| Reactive context (CI fail, review) | No — must wait for next run | Yes — inject during current run |
| Crash recovery | Restart from scratch | `--resume` continues session |
| Multi-turn workflow | Separate invocations, no shared context | Same session, accumulated context |
| Graceful shutdown | `docker kill` (loses in-progress work) | "Commit your work and exit" message |
| Done detection | Exit code only | Exit code + hooks + capture-pane + JSONL |
| Session visibility | stream.jsonl tail | stream.jsonl + `tmux attach` for live view |
| Complexity | Minimal | Moderate (tmux in image, entrypoint, mailbox or send-keys) |

#### Complexity Cost

The hybrid model adds:
- tmux in the Docker image (~2MB)
- An entrypoint script that starts tmux and launches Claude Code
- Either `docker exec tmux send-keys` wiring (with the timing issues) or a mailbox mechanism
- A keep-alive loop so the container doesn't exit when Claude Code finishes a turn
- Session ID tracking for `--resume` across container restarts

This is substantially less than what Gas Town requires (no Witness, no Deacon, no Beads, no nudge queue atomics, no crash loop tracker) because Docker provides the isolation and lifecycle that Gas Town builds manually with tmux sessions on bare metal.

#### When to Use Which

**`claude -p`** (current ATeam model): best for well-scoped, autonomous tasks where the prompt contains everything the agent needs. Audit runs, test generation, documentation — anything that can be specified upfront and evaluated after completion.

**tmux-in-Docker**: best for tasks that benefit from mid-task interaction. Implementation tasks where the coordinator may need to course-correct. Long-running tasks where crashes are likely. Workflows where the coordinator reviews intermediate output before the agent continues. Reactive scenarios where external events (CI, reviews) should feed back into a running agent.

A pragmatic approach: start with `claude -p` for all agent types. Add tmux-in-Docker as an alternative runtime for implement-mode agents and for any task that exceeds a cost/duration threshold where crash recovery justifies the complexity.

### C.9 Non-tmux Agent Control

Not every framework uses tmux. This section covers the four main alternatives: subprocess pipes, Docker one-shot, API-based agent loops, and REST servers inside containers. Each avoids tmux's session management complexity but introduces different tradeoffs.

#### Subprocess Pipes (Claude Agent SDK)

**Claude Agent SDK** ([anthropics/claude-agent-sdk-python](https://github.com/anthropics/claude-agent-sdk-python), [claude-agent-sdk-typescript](https://github.com/anthropics/claude-agent-sdk-typescript)) is Anthropic's official way to run Claude Code programmatically. It spawns the Claude Code CLI as a child process and communicates via JSON-lines over stdin/stdout.

The SDK provides two interfaces:
- **`query()`** — one-shot: new subprocess per call, returns when done. Maps to ATeam's `claude -p` pattern.
- **`ClaudeSDKClient`** — persistent: reuses the same subprocess for multiple turns, supports interrupts. Maps to the tmux-in-Docker interactive pattern but without tmux.

**Communication protocol**: structured JSON-lines with two categories — regular messages (agent responses, tool outputs, cost) and control messages (permission requests with multiplexed `request_id`). Permission callbacks allow the orchestrator to intercept and approve/deny individual tool calls programmatically:
```json
{"type": "control_request", "request_id": "req_1", "request": {"subtype": "can_use_tool", "tool_name": "Bash", "input": {"command": "rm -rf /"}}}
{"type": "control_response", "request_id": "req_1", "response": {"behavior": "deny"}}
```

This is strictly more capable than `--dangerously-skip-permissions` (which is all-or-nothing) — the orchestrator can allow `Read` but deny `Bash`, or inspect the actual command before approving.

**Permission modes**: `default` (standard prompting), `acceptEdits` (auto-accept file edits, still prompt for Bash), `bypassPermissions` (equivalent to `--dangerously-skip-permissions`), `plan` (read-only).

**Subagent support**: first-class. Agents can spawn subagents via the `Task` tool, with `parent_tool_use_id` for tracking. Subagents run within the same session, not as separate processes. The orchestrator can define allowed tools per subagent.

**Session resume**: `query(prompt="continue", options=ClaudeAgentOptions(resume=session_id))` allows multi-turn orchestration without a persistent subprocess — each call spawns a new process but resumes the previous session's context.

**Completion detection**: waits for three conditions — `result` event, stdout EOF, and process exit. All three must occur. If any is missing, the SDK hangs indefinitely.

**Known production issues**:

| Issue | Severity | Impact on ATeam |
|---|---|---|
| Missing final `result` event ([#1920](https://github.com/anthropics/claude-code/issues/1920)) | High | After some tool executions, Claude Code fails to emit the final `result` event. SDK hangs indefinitely. Requires external watchdog timeout. |
| Silent mid-task hang ([#28482](https://github.com/anthropics/claude-code/issues/28482)) | High | Claude Code stops producing output mid-task. No error, no exit, no recovery path. Only workaround is Esc key (interactive only). Marked as blocking for production automation. |
| Process group signal self-kill in Docker ([#16135](https://github.com/anthropics/claude-code/issues/16135)) | Medium | When Claude Code kills a background process group, it sends the signal to its own group, terminating itself (exit 137). Not triggered by `claude -p` since the background process manager is not invoked. |
| JSON buffer overflow | Low | Default 1MB buffer. Large tool outputs (big `git diff`) can cause parse failures. |

**Crash recovery**: none built-in. When the subprocess dies, `ProcessTransport` enters a broken state — subsequent requests fail. The caller must start a fresh subprocess and reconstruct context.

**Key advantage over tmux**: structured JSON protocol gives typed events, programmatic permission callbacks, cost tracking, and subagent tracking — all things that tmux `capture-pane` parsing can never provide reliably.

**Key limitation**: no mid-task steering with `query()`. The `ClaudeSDKClient` persistent mode supports it but is less battle-tested and inherits the hang bugs.

#### Subprocess One-Shot (Aider, Codex CLI)

Simpler than the Agent SDK — just spawn the process with a prompt and read exit code.

**Aider** ([Aider-AI/aider](https://github.com/Aider-AI/aider)):
```bash
aider --message "add docstrings to all functions" file.py --yes --no-auto-commits
```
`--yes` auto-confirms all prompts. `--message` runs one instruction then exits. Exit code 0 = success. No streaming JSON, no cost tracking in output, no permission granularity. Git integration is the crash recovery mechanism — every change creates a commit. Also has an unofficial Python API (`Coder.create()`) for in-process use.

**Codex CLI** ([openai/codex](https://github.com/openai/codex)):
```bash
codex exec --full-auto "implement feature X"
```
Similar one-shot pattern. `--full-auto` bypasses prompts. Exit code for completion.

These are the simplest possible agent control — subprocess with timeout, check exit code, read output files. The limitation is obvious: no observability during the run, no mid-task steering, no structured events.

#### Docker One-Shot (ATeam's Current Model)

This is ATeam's `agent_in_container.sh` approach. The container runs once, the agent completes, the container exits. The orchestrator reads exit code and output files from bind-mounted volumes.

**How the "no mid-task steering" limitation is handled** across frameworks using this pattern:

1. **Scope tasks narrowly** — so steering is never needed. ATeam's audit/implement separation does this.
2. **Monitor stream-json for blocks** — detect when the agent writes `blocked.md` or stops making progress, then kill and re-invoke with an amended prompt.
3. **Agent self-reports** — the agent writes status files (`blocked.md`, `progress.md`) that the coordinator reads after completion.
4. **`--resume` as escape hatch** — if the container dies, start a new one with `claude --resume <session-id>`. Session JSONL files persisted via bind mount.

**SWE-agent + SWE-ReX** ([SWE-agent/SWE-ReX](https://github.com/SWE-agent/SWE-ReX)) extends the Docker one-shot model with a REST API *inside* the container. The container runs a FastAPI server (`swerex-remote`); the orchestrator sends commands via HTTP. Each command is synchronous (execute → return output + exit code). Multiple concurrent sessions supported. This turns the container from a batch job into a controlled sandbox, while keeping Docker's isolation. **Mini-SWE-agent** achieves >74% on SWE-bench with ~100 lines using plain `subprocess.run` / `docker exec` — stateless, no event streaming.

**sandbox-agent** ([rivet-dev/sandbox-agent](https://github.com/rivet-dev/sandbox-agent)) takes a different approach: a Rust HTTP server inside the container exposes session management via REST and real-time events via SSE. Supports Claude Code, Codex, OpenCode, Amp. Sessions are persistent within the container lifetime (not one-shot per prompt) — you can `createSession()`, `postMessage()`, and `streamEvents()` over HTTP. This bridges Docker isolation with interactive session control, no tmux needed.

#### API-Based Agent Loops (No CLI Agent)

No subprocess at all. The orchestrator calls the LLM API directly, implements its own tool execution loop, and maintains conversation history.

```python
while True:
    response = client.messages.create(model="claude-opus-4-6", messages=messages, tools=tools)
    if response.stop_reason == "end_turn":
        break
    for block in response.content:
        if block.type == "tool_use":
            result = execute_tool(block.name, block.input)
            messages.append(tool_result_message(block.id, result))
```

**What you gain**: full control over tool definitions and permissions per call, provider agnosticism (swap Claude for GPT-4 or Gemini), no subprocess hang bugs, granular per-API-call cost tracking, programmatic mid-task injection (the loop is yours), custom tool implementations in any language.

**What you lose vs Claude Code** (extends §A.2):

| Concern | Custom API Loop | Claude Code CLI |
|---|---|---|
| Coding quality | Must implement file editing, shell, error recovery, iterative debugging | Built-in, battle-tested, continuously improved |
| Tool maintenance | You maintain tools as API changes | `claude update` stays current |
| Context management | Must handle window limits, truncation, summarization | Handled internally |
| Multi-file editing | Must implement search-replace, diff application | Built-in Edit/Write/Read |
| Sub-agent spawning | Must implement orchestration protocol | Built-in Task tool with parent tracking |
| Complexity | Hundreds of lines of agent loop code | Zero agent code |
| Cost | Raw API tokens | Subscription plan often more economical |

**Specific failure modes**:
- **Infinite tool call loops**: model keeps calling tools without progress. Detect by tracking turn count and/or repeated identical tool calls.
- **Context overflow**: long tasks accumulate history until the window overflows. Must implement rolling summarization.
- **Tool execution crashes**: unhandled exception in a tool causes the model to either halt or hallucinate. Must return errors as `tool_result` content.
- **Prompt injection via tool output**: malicious content in files can inject instructions. Must sanitize.
- **Rate limits**: concurrent agents hitting limits simultaneously. Backoff with jitter.

**Frameworks using this approach**:

**CrewAI** ([crewAIInc/crewAI](https://github.com/crewAIInc/crewAI)): role-based multi-agent. In-process Python, no Docker by default. Agents communicate via natural language messages. Tools are Python functions.

**LangGraph** ([langchain-ai/langgraph](https://github.com/langchain-ai/langgraph)): graph-based execution runtime. The most interesting mid-task steering mechanism: `interrupt()` suspends the graph and serializes state to a checkpointer (disk/database). Resume later with `graph.invoke(Command(resume="approved"), thread_config)`. This is deterministic human-in-the-loop without any tmux keystroke injection. Reached v1.0 in late 2025.

**Open SWE** ([langchain-ai/open-swe](https://github.com/langchain-ai/open-swe)): three-agent LangGraph workflow (Manager → Planner → Programmer+Reviewer). Runs in Daytona sandboxes. Triggered by GitHub issue labels. Human can interrupt to review/edit the plan before execution begins.

#### REST/WebSocket Server Inside Container

A pattern emerging from OpenHands, SWE-ReX, and sandbox-agent: run a server inside the container, communicate over HTTP. Combines Docker isolation with interactive control.

**OpenHands** ([OpenHands/OpenHands](https://github.com/OpenHands/OpenHands)): the most architecturally complete implementation. Each session is an **event-sourced append-only log** of actions and observations. This enables deterministic replay for debugging, fault recovery by replaying events, and auditability. When resuming after a crash, the system loads base state and replays the event log — no state is lost. The V1 SDK includes a production server with REST+WebSocket APIs, plus workspace access via VSCode Web and VNC desktop — enabling human intervention during a running session by opening a URL.

Permission handling: a two-layer model — `SecurityAnalyzer` rates each tool call as LOW/MEDIUM/HIGH risk; `ConfirmationPolicy` determines if approval is required. On `WAITING_FOR_CONFIRMATION`, the agent pauses completely until approved. On rejection, the agent backtracks.

**Daytona** ([daytonaio/daytona](https://github.com/daytonaio/daytona)): cloud-managed version of the same pattern. Provides sandboxes as a service with Python/TypeScript SDKs. Used by Open SWE as the execution environment. You get a stable API endpoint per sandbox — no Docker infrastructure to manage yourself.

#### Platform-Managed Execution (GitHub Actions)

A distinct approach: the agent runs inside a CI/CD runner, communicates entirely via the platform's APIs (issues, PRs, comments), and has no direct process relationship with the orchestrator.

**GitHub Copilot Coding Agent**: creates an ephemeral GitHub Actions runner, runs the agent inside it, communicates via GitHub Actions APIs and WebSocket streams. The human interacts via issue comments and PR reviews — entirely HTTP/webhook-based. The agent pushes commits to a draft PR continuously as it works. CI runs on the agent's commits and failures feed back via automated PR comments.

**Amazon Q Developer**: similar pattern. Triggered by assigning a GitHub issue to Q. Runs on GitHub Actions Ubuntu runners. Communicates via GitHub API. Fully asynchronous.

This approach eliminates all subprocess/container management but is tightly coupled to the platform and provides no real-time agent visibility beyond PR diffs.

#### PTY Control

A PTY (pseudo-terminal) creates a virtual terminal pair. The orchestrator holds the master side and writes input/reads output; the agent thinks it has a real terminal.

**Who uses it**: VS Code's integrated terminal uses `node-pty` ([microsoft/node-pty](https://github.com/microsoft/node-pty)) — this is how Claude Code runs inside VS Code extensions. Cline ([cline/cline](https://github.com/cline/cline)) uses this in VS Code and gRPC in JetBrains. Classic `expect`/`pexpect` can automate Claude Code interactively.

**Why it exists**: some programs behave differently with a TTY (colors, buffering, TUI rendering). PTY makes the program think it's interactive. For Claude Code specifically, this is unnecessary — `claude -p` provides structured output without needing a fake terminal.

**Failure modes**: pattern matching brittleness (any output format change breaks automation), race conditions (async PTY reads with interleaved output), no structured output (must strip ANSI codes), buffer deadlocks. PTY adds complexity without benefit when `claude -p` or the Agent SDK is available.

#### Comparison: Failure Modes by Approach

| Failure | Subprocess Pipe | Docker One-Shot | API Loop | REST-in-Container |
|---|---|---|---|---|
| Hang without exit | Yes (missing result event, silent hang) | Yes (same Claude Code bugs) | No (loop is yours) | Depends on agent |
| Mid-task steering | No (`query()`), Yes (`ClaudeSDKClient`) | No | Yes (loop injection) | Yes (HTTP message) |
| Crash recovery | None built-in | Restart container, volumes persist | Rebuild history from storage | Event replay (OpenHands) |
| Process isolation | Process-level only | Full container isolation | In-process, none | Full container isolation |
| Permission control | Programmatic callbacks (best) | `--dangerously-skip-permissions` | Custom per-tool (best) | Risk-rated (OpenHands) |
| Cost tracking | `result` event | `result` event | Per-API-call headers | Session complete event |
| Observability during run | JSON stream | JSON stream via bind mount | Full request/response logs | HTTP/SSE events |
| Complexity | Medium (SDK integration) | Low (shell script) | High (full agent loop) | Medium (HTTP client) |

#### Key Takeaways for ATeam

**The missing `result` event bug is a real production concern.** ATeam's `agent_in_container.sh` should implement a watchdog: if no stream-json output for N minutes (configurable, e.g., 10min), kill the container and mark the run as hung. Currently the script waits indefinitely.

**The Agent SDK's permission callbacks are strictly better than `--dangerously-skip-permissions`** for scenarios where fine-grained control matters. For ATeam's Docker model where the container IS the sandbox, `--dangerously-skip-permissions` is fine — but if ATeam ever moves to a non-containerized model, the SDK's permission callbacks become essential.

**The REST-server-inside-container pattern (SWE-ReX, sandbox-agent, OpenHands) is the most architecturally sound non-tmux approach for interactive sessions.** It gives Docker isolation + HTTP-based mid-task steering + structured events, without any tmux timing fragility. If ATeam needs interactive sessions beyond `claude -p`, this pattern is preferable to tmux-in-Docker (§C.8).

**Event sourcing (OpenHands) is the gold standard for crash recovery** — deterministic replay from an append-only log. ATeam's stream-json output already IS an append-only event log. The missing piece is a `--resume`-like mechanism that can reconstruct agent state from the log. Claude Code's `--resume` flag provides this at the session level.

**API-based loops trade simplicity for control.** ATeam's choice to use Claude Code (not raw API) is validated by the tool maintenance burden — keeping a custom agent loop working across API changes is ongoing cost. The API approach only makes sense if ATeam needs provider flexibility (GPT-4, Gemini) or per-tool-call permission logic that the SDK doesn't support.

---

### C.10 Claude Code Remote Control Protocol

* Official docs from Anthropic: https://code.claude.com/docs/en/remote-control
* Reverse Engineering notes: https://gist.github.com/sorrycc/9b9ac045d5329ac03084a465345b59c3

#### Summary from ChatGPT as of 2026/03/07

There are really two layers people mean by “Claude Code Remote Control protocol.”

The full official Remote Control stack is the claude remote-control / /remote-control feature that connects claude.ai/code or the Claude mobile app to a local Claude Code session. Anthropic’s docs describe that feature at a high level. A public reverse-engineering write-up by sorrycc reconstructs the full bridge as environment registration at /v1/environments/bridge, long-polling at /v1/environments/{id}/work/poll, and event posting at /v1/sessions/{id}/events, with separate environment_secret auth for polling. Anthropic bug logs also show Remote Control spawning a child Claude process with --sdk-url wss://api.anthropic.com/v1/session_ingress/ws/..., which is the inner session-ingress layer.  ￼

If you mean the full bridge, I can only verify one clear public reverse-engineering artifact right now:
    •   sorrycc / remote-control-implementation.md — a reverse-engineered implementation reference for “Remote Control (Tengu Bridge).” It documents the bridge endpoints, feature flags, OAuth flow, environment registration, work polling, session event posting, and the hybrid WebSocket/HTTP transport. It is more of a spec/reference than a polished end-user product.  ￼

If you also count the lower-level session-ingress / --sdk-url WebSocket protocol that Remote Control uses internally, then the list gets longer:
    •   The-Vibe-Company / Companion — this is the clearest one. The repo includes WEBSOCKET_PROTOCOL_REVERSED.md, which explicitly says the undocumented --sdk-url WebSocket protocol was reverse-engineered from Claude Code CLI, and the README says its bridge uses the CLI --sdk-url WebSocket path plus NDJSON events. An Anthropic docs issue even cites The Vibe Companion as a third-party project that had to reverse-engineer the internal WebSocket NDJSON protocol because the official docs were insufficient.  ￼
    •   kxbnb / claude-code-companion — a TUI that spawns Claude Code in SDK mode and talks to it over a local WebSocket using NDJSON. I’d count this as an implementation of the lower layer, though the repo does not itself present a reverse-engineering write-up.  ￼
    •   pandazki / pneuma-skills — explicitly says it spawns a Claude Code CLI session over WebSocket, and that /ws/cli/:sessionId carries NDJSON messages for Claude Code’s --sdk-url protocol. Again: implementation yes, explicit RE write-up no.  ￼
    •   lebovic / agent-quickstart — says its API is “unofficially interoperable with Claude Code,” and shows Claude launched against its own session_ingress endpoints via --sdk-url and --resume. That is effectively a clean-room reimplementation of the session-ingress layer.  ￼
    •   ZhangHanDong / claude-code-api-rs — exposes WebSocket sessions that spawn Claude with --sdk-url, plus WS endpoints for the CLI side and external client side. This clearly implements the same lower layer, though it is not explicitly framed as a reverse-engineering project.  ￼

What I would not count as “already reversed engineered the official Remote Control protocol” are projects that give you remote control by other means:
    •   sled uses ACP (Agent Control Protocol), not Anthropic Remote Control.  ￼
    •   CCBot explicitly says it works via tmux, “not the Claude Code SDK.”  ￼
    •   DarkCode Server is its own phone-to-server WebSocket bridge and talks to Claude via stdin/stdout.  ￼
    •   Claude-Code-Remote is a messaging-platform wrapper (email/Telegram/LINE), not the Anthropic Remote Control stack.  ￼

So the concise answer is: full official RC stack: basically just sorrycc’s reference so far; inner session_ingress / --sdk-url layer: Companion is the clearest reverse-engineered project, with claude-code-companion, pneuma-skills, agent-quickstart, and claude-code-api-rs also implementing that lower layer.

