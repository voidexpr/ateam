# ATeam Design Specification

> **Working Name:** ATeam (Agent-Team)
> **Version:** 0.3 — Added git-versioned config + competitive landscape
> **Date:** 2026-02-26

---

## 1. Executive Summary

ATeam is an agent coordination framework that automates essential but tedious engineering tasks — code quality, architecture integrity, testing, performance, security, and documentation — so human developers can focus on feature work. A lightweight Python coordinator orchestrates specialized sub-agents that run **Claude Code inside Docker containers**, operating on Git worktrees from a shared repository. Work happens primarily during off-hours with minimal human intervention.

**Key architectural insight:** Claude Code is already an excellent coding agent with built-in file editing, shell execution, iterative debugging, and error recovery. Rather than reimplement all of that poorly with a custom API tool-use loop, we use Claude Code as-is inside Docker containers and communicate through the filesystem — prompts go in as files, reports come out as files.

---

## 2. Goals

### Primary Goals

- **Code Quality:** Automated refactoring, architecture enforcement, code review of all changes. Periodic big-picture analysis to recommend and execute larger structural improvements.
- **Testing:** Continuous regression testing on every commit. Expand test coverage over time.
- **Performance:** Automated profiling, bottleneck identification, and targeted optimization.
- **Security:** Vulnerability scanning, dependency auditing, code pattern analysis.
- **Dependency Management:** Keep dependencies current, remove unused ones, consolidate lightly-used dependencies into project libraries, recommend alternatives when a project outgrows a dependency.
- **Internal Documentation:** Architecture docs, code structure, development guides.
- **External Documentation:** Overview, feature details, usage guides, installation, local development.

### Operational Goals

- Avoid overwhelming humans with agent activity.
- Avoid wasteful token consumption on low-value busywork.
- Build and maintain project-specific and cross-project knowledge.
- Configure linter/formatter tools to reduce future audit work.

---

## 3. Language Choice: Python + Shell

**Python** for the coordinator daemon (scheduling, git management, Docker orchestration, resource monitoring, CLI).

**Shell** for the `docker_run.sh` scripts that launch sub-agent containers and invoke Claude Code inside them.

**Claude Code** is the sub-agent runtime — it runs inside Docker and does the actual coding work. We don't write a custom coding agent.

### 3.1 Coordinator Runtime Options

The coordinator needs to be an intelligent agent — not just a cron job. It reads reports, decides priorities, resolves conflicts, and communicates decisions. There are several ways to build this:

#### Option A: Claude Code with Custom MCP Tools (ad-hoc)

The coordinator runs as a Claude Code instance with custom MCP servers for ATeam operations. The human interactively tells the coordinator what to do (like Gas Town's Mayor).

**Pros:** Zero framework code. Claude Code provides the agent loop, file editing, shell execution. Just add MCP tools.

**Cons:** Interactive — requires human at the keyboard. No scheduling, no autonomous operation. This is Gas Town's model.

#### Option B: Deterministic Python Daemon + LLM Escalation

A Python daemon handles scheduling, git, Docker, and budget via rule-based logic. For complex decisions, it optionally escalates to a cheap LLM call (Haiku via API).

**Pros:** Predictable, testable, cheap. Easy to audit.

**Cons:** Less capable for nuanced decisions. All decision heuristics must be hand-coded. Lots of framework code to build and maintain.

#### Option C: Claude Agent SDK as Coordinator

Use the Claude Agent SDK (Python) to build the coordinator as a programmatic agent. The SDK wraps Claude Code and provides: custom in-process MCP tools, hooks, permission callbacks, and streaming with token usage reporting.

**Pros:** Best programmatic control. In-process tools, hooks, permissions. Token tracking. Can run headless.

**Cons:** Python-only (no Go SDK). Agent SDK is relatively new (v0.1.x). Requires building the scheduling/lifecycle layer in Python around the SDK. Still substantial framework code.

#### Option D: Claude Code as Coordinator + MCP Infrastructure Server (Recommended)

**The framework IS an MCP server.** Claude Code runs as the coordinator — either interactively or via `claude -p` invoked by cron/systemd/launchd. The entire ATeam "framework" is just an MCP server (written in any language) that exposes infrastructure tools:

```
MCP Tools exposed by ATeam server:

# Sub-agent lifecycle
subagent_run_audit(agent, project)     → spawns Docker, runs audit, returns report path
subagent_run_implement(agent, project, report) → spawns Docker, implements approved findings
subagent_commit_and_merge(agent, project) → commits changes, runs tests, merges if green

# Report management
format_report(report_path, model?)     → reformats/summarizes report (can delegate to Haiku)
get_pending_reports(project)           → lists reports awaiting review
get_report(agent, project, date)       → reads a specific report

# Knowledge & config
update_knowledge(agent, project, summary) → appends to knowledge.md
get_config(project)                    → reads config.toml
get_budget_status(project)             → returns runs today, cost estimate, remaining budget

# Git operations
create_worktree(project, agent)        → creates git worktree for agent
cleanup_worktree(project, agent)       → removes worktree after completion
get_recent_commits(project, since)     → lists recent commits

# Scheduling (for autonomous mode)
get_schedule_profile()                 → returns "night" or "day" and allowed agents
get_queued_tasks(project)              → returns prioritized task queue
```

The coordinator's Claude Code instance reads the ATeam system prompt (from `CLAUDE.md` or injected via `-p`), connects to the ATeam MCP server, and orchestrates everything using natural language reasoning + tool calls.

**For autonomous operation:** A thin wrapper (cron job, systemd timer, launchd, or a simple Go/Python daemon) periodically invokes `claude -p "Run the ATeam night cycle for project X" --mcp-server ateam` or uses the Agent SDK's `query()`. The Claude Code agent then autonomously calls `get_schedule_profile`, `subagent_run_audit`, reviews reports, calls `subagent_run_implement` for approved findings, and so on.

**Pros:**

- **Language-agnostic.** The MCP server can be written in Go, Python, TypeScript, Rust — whatever produces the best binary. The MCP protocol is just JSON-RPC over stdio. This eliminates the Python-vs-Go question for the framework itself.
- **Minimal framework code.** The MCP server is pure infrastructure: spawn Docker containers, manage git worktrees, read/write files, track budget. No agent loop, no prompt engineering, no decision heuristics. Claude Code handles all the reasoning.
- **Claude Code does what it's good at.** Report analysis, prioritization, conflict resolution, deciding what to implement — these are reasoning tasks. Claude Code is excellent at them. We don't need to hand-code heuristics.
- **Full Claude Code capabilities.** The coordinator can read files, grep codebases, inspect test results, write summaries — all natively, without us implementing those tools.
- **Natural human interaction.** In interactive mode, the human talks to Claude Code directly: "Focus on security this week" or "Skip the refactor agent, the codebase is frozen." No CLI commands to learn.
- **Works with subscriptions.** Claude Code uses your existing Claude subscription (Pro/Max). No API key needed for the coordinator. Sub-agents inside Docker use the same subscription via `~/.claude` mount.
- **Testable infrastructure.** The MCP server's tools are pure functions: given inputs, produce outputs. Easy to unit test. The reasoning layer (Claude Code) is tested by Anthropic.

**Cons:**

- Coordinator reasoning costs tokens (but using a cheap model like Haiku for routine decisions mitigates this).
- Non-deterministic coordinator decisions (but the decision log in changelog.md provides auditability, and the human can always override).
- MCP server is a separate process that must be running when the coordinator runs.

**Why this is the cleanest architecture:**

```
┌─────────────────────────────────────────────────┐
│ Claude Code (coordinator)                        │
│   - reads ATeam system prompt                    │
│   - reasons about what to do                     │
│   - calls MCP tools for infrastructure           │
│   - reads/writes files natively                  │
│   - reports decisions to human                   │
├─────────────────────────────────────────────────┤
│ ATeam MCP Server (Go/Python/TS binary)           │
│   - subagent lifecycle (Docker)                  │
│   - git worktree management                      │
│   - budget tracking                              │
│   - report storage                               │
│   - schedule configuration                       │
├─────────────────────────────────────────────────┤
│ Sub-agents (Claude Code in Docker)               │
│   - testing, refactor, security, etc.            │
│   - one-shot: read prompt → do work → write output│
└─────────────────────────────────────────────────┘
```

Compare with options B/C where the framework must implement: agent loop, prompt management, decision heuristics, context window management, error recovery, conversation state, tool dispatch, streaming handling, etc. In Option D, all of that is Claude Code. The framework is just plumbing.

#### Decision

**Option D (Claude Code + MCP infrastructure server)** is the recommended approach. It produces the simplest framework with the most capable coordinator. The MCP server can be written in Go for single-binary distribution (since it's just infrastructure — no LLM SDK needed), while the coordinator gets full Claude Code reasoning capabilities.

**For the MCP server language:** Go is now the natural choice. The MCP server needs Docker management, git operations, process spawning, file I/O, and a simple HTTP/stdio transport — all things Go excels at. No LLM SDK is needed because the MCP server doesn't call LLMs; Claude Code does. This gives us Go's single-binary distribution for the infrastructure layer while keeping Claude Code's full reasoning for the coordinator.

**Scheduling wrapper options:**
- **Simplest:** cron/systemd timer that runs `claude -p "Run ATeam cycle" --mcp-server ateam` periodically.
- **More control:** A small Go daemon that checks for commits, manages the schedule profile (night/day), and invokes Claude Code when work is needed. Still minimal — just a scheduler + subprocess launcher.
- **Agent SDK (Python):** If you want programmatic hooks and permission callbacks, use the Agent SDK's `query()` to invoke the coordinator. The MCP server is still the same Go binary.

### 3.2 Go vs Python: Language Comparison

With Option D, the language question shifts. The **MCP server** (the actual framework code) doesn't need an LLM SDK at all — it's pure infrastructure. The **coordinator** is Claude Code (language-agnostic). The comparison becomes:

#### For the MCP Server (the framework)

| Consideration | Go | Python |
|---|---|---|
| **Single binary** | ✅ `go build` → 15MB static binary | ❌ Requires Python runtime or ~100MB PyInstaller bundle |
| **Docker SDK** | ✅ Official client from Docker Inc. | ✅ Official SDK |
| **Git operations** | ✅ `go-git` or `os/exec` → `git` | ✅ `gitpython` or `subprocess` |
| **MCP server** | ✅ `mcp-go` (official) | ✅ `mcp` (official) |
| **Concurrency** | ✅ Goroutines — natural for managing multiple Docker containers | ⚠️ asyncio works but more awkward |
| **Distribution** | ✅ Copy binary and run | ❌ pip install, virtualenv, PATH issues |
| **LLM SDK needed?** | **No** — the MCP server doesn't call LLMs | **No** |

**Go wins for the MCP server.** It doesn't need the Claude Agent SDK (that was the Python dealbreaker before), and Go's strengths (single binary, concurrency, Docker/git ecosystem) are exactly what the MCP server needs.

#### For the Scheduling Wrapper (optional)

If you want more than a cron job (commit detection, day/night profiles, budget enforcement before invoking Claude Code):

| Consideration | Go | Python |
|---|---|---|
| **Simple scheduler** | ✅ `robfig/cron` | ✅ `APScheduler` (more features) |
| **Claude Code invocation** | ✅ `os/exec` → `claude -p` | ✅ Agent SDK `query()` for richer control |
| **Can be same binary as MCP server** | ✅ Single binary does both | ❌ Separate process |

**Go wins if** the scheduler is simple (commit check + cron + budget gate → invoke Claude Code). **Python wins if** you want Agent SDK hooks and programmatic control over the coordinator's behavior.

#### Recommendation: Go MCP Server + Thin Scheduler

The MCP server and scheduling wrapper can be a single Go binary: `ateam serve` runs the MCP server, `ateam daemon` runs the scheduler that periodically invokes Claude Code with the MCP server attached. This gives us Gas Town's distribution model (single `go install` binary) with ATeam's autonomous scheduling.

The Claude Agent SDK remains available as an alternative coordinator driver for users who want programmatic Python control. The MCP server doesn't care who calls it — Claude Code interactively, `claude -p` from cron, or the Agent SDK from Python.

#### Full Dependency Comparison (MCP Server in Go)

| Capability | Go Module | Notes |
|---|---|---|
| **MCP server** | `mcp-go` (official) | JSON-RPC over stdio, handles tool registration and dispatch |
| **Docker management** | `docker/docker` client | Official Docker SDK |
| **Git operations** | `go-git/go-git` or `os/exec` → `git` | Pure Go git or shell out for worktree management |
| **CLI framework** | `cobra` + `viper` | De facto Go CLI standard |
| **TOML parsing** | `BurntSushi/toml` | Mature, well-maintained |
| **Scheduling** | `robfig/cron` or `go-co-op/gocron` | Cron expressions, timezone support |
| **Process execution** | `os/exec` (stdlib) | For invoking `claude -p` |
| **System monitoring** | `shirou/gopsutil` | CPU/memory checks |
| **HTTP server** | `net/http` (stdlib) | For optional dashboard |
| **Logging** | `log/slog` (stdlib) | Structured logging |
| **LLM SDK** | **Not needed** | Claude Code handles all LLM interaction |

#### Python Single-Binary Options (if choosing Python)

| Tool | How It Works | Binary Size | Startup | Notes |
|---|---|---|---|---|
| **PyInstaller** | Bundles interpreter + bytecode in self-extracting archive | ~94 MB | Slow (extracts to temp) | Most popular, cross-platform |
| **Nuitka** | Compiles Python → C → native binary | ~58 MB | Fast (native) | 6+ min compile, potential compat issues |
| **PyOxidizer** | Embeds Python in Rust binary, loads from memory | Varies | Fast | Complex setup, Rust build dependency |
| **Shiv / PEX** | Zip-file packages (.pyz) | Small | Fast | Requires Python on target — defeats purpose |

---

## 4. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│                    Human Developer                         │
│       (git push, priority overrides via `ateam` CLI)      │
└────────────────────────┬─────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────┐
│               Coordinator (Python daemon)                  │
│                                                            │
│  ┌──────────┐ ┌─────────┐ ┌────────────┐ ┌───────────┐  │
│  │ Scheduler│ │ Git Mgr │ │Resource Mon│ │  Task Q   │  │
│  └────┬─────┘ └────┬────┘ └─────┬──────┘ └─────┬─────┘  │
│       └─────────────┴────────────┴──────────────┘        │
│                          │                                │
│                 Docker Runner                              │
│          (spawns containers, waits, reads output)          │
└──────────────────────────┬───────────────────────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
  │   Docker      │ │   Docker      │ │   Docker      │
  │ ┌──────────┐ │ │ ┌──────────┐ │ │ ┌──────────┐ │
  │ │Claude Code│ │ │ │Claude Code│ │ │ │Claude Code│ │
  │ └──────────┘ │ │ └──────────┘ │ │ └──────────┘ │
  │  testing      │ │  refactor    │ │  security    │
  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
         │                │                │
         └───────┬────────┴────────────────┘
                 ▼
        ┌────────────────┐
        │  Git Worktrees  │  (bind-mounted into containers)
        └────────────────┘

Communication is via filesystem:
  IN:  /agent-data/prompt.md  (task + role + knowledge)
  OUT: /workspace/output/     (report.md, completion.md, etc.)
```

### Core Components

| Component | Responsibility |
|---|---|
| **Coordinator** | Scheduling, prioritization, report review, human interaction, decision logging |
| **Scheduler** | Cron-like trigger system with day/night profiles |
| **Git Manager** | Maintains bare repo clone, creates/manages worktrees, handles rebase/push |
| **Docker Runner** | Container lifecycle — start, wait for exit, timeout/kill, read output files |
| **Resource Monitor** | Watches CPU/memory/container health, throttles or kills runaway containers |
| **Task Queue** | Ordered list of pending agent tasks with priorities |

---

## 5. Sub-Agent Design: Claude Code Inside Docker

### 5.1 Why Claude Code, Not a Custom API Agent

| Concern | Custom API loop | Claude Code in Docker |
|---|---|---|
| Coding quality | Must implement file editing, shell, error recovery, iterative debugging from scratch | Already built-in and battle-tested |
| Maintenance burden | We maintain the agent loop; it rots as APIs change | Anthropic maintains it; `claude update` stays current |
| Authentication | API key management | Mount `~/.claude` — uses existing auth, billing, and higher plan limits |
| Tool permissions | Must define and implement each tool | Already has shell, file edit, search, etc. with proper sandboxing |
| Cost | Raw API tokens (potentially more expensive) | Claude Code subscription limits are often more economical |
| Complexity | Hundreds of lines of tool-use loop code | Zero agent code — just prompt construction and file I/O |

**The tradeoffs we accept:**

- No per-invocation token counts (we can estimate from timing and output size, or parse Claude Code's summary output).
- Harder to swap LLM providers (addressed in §5.6).
- Less programmatic control over the agent loop.

These are acceptable tradeoffs given the massive reduction in complexity and the superior coding quality.

### 5.2 Execution Modes

Each sub-agent operates in one of four modes per invocation:

| Mode | Input | Output |
|---|---|---|
| **Audit** | Source code + knowledge.md | `YYYY-MM-DD_HHMM_report.md` — findings and recommendations |
| **Implement** | Report (possibly amended) + source code | Code changes in worktree + `_report_completion.md` |
| **Maintain** | Last completion report + knowledge.md | Updated `knowledge.md` |
| **Configure** | Source code + knowledge.md | Linter/formatter config files integrated into build system |

### 5.3 How Sub-Agents Are Invoked

The coordinator constructs a prompt file, launches a Docker container with Claude Code installed, and Claude Code reads the prompt, executes its full agent loop, writes output files, and exits.

**Step 1: Coordinator assembles the prompt**

The coordinator concatenates the layered prompt into a single markdown file:

```python
def build_prompt(agent_id: str, mode: str, project: str, task_context: str) -> str:
    parts = []

    # Role definition (general + project-specific override)
    parts.append(read_file(f"agents/{agent_id}/role.md"))
    if exists(f"agents/{agent_id}/culture.md"):
        parts.append(read_file(f"agents/{agent_id}/culture.md"))
    if exists(f"{project}/{agent_id}/role.md"):
        parts.append(read_file(f"{project}/{agent_id}/role.md"))

    # Project knowledge
    if exists(f"{project}/{agent_id}/knowledge.md"):
        parts.append(read_file(f"{project}/{agent_id}/knowledge.md"))

    # Mode-specific instructions
    parts.append(MODE_TEMPLATES[mode])

    # Task context (what to do this run)
    parts.append(task_context)

    # Output contract
    parts.append(OUTPUT_CONTRACT[mode])

    return "\n\n---\n\n".join(parts)
```

The prompt file is written to the agent-data directory:

```
{project}/{agent_id}/current_prompt.md
```

**Step 2: Coordinator launches Docker with Claude Code**

```bash
#!/bin/bash
# docker_run.sh — launches a sub-agent container

PROJECT="$1"
AGENT="$2"
TASK_WORKTREE="$3"
TIMESTAMP="$4"

docker run \
  --rm \
  --name "ateam-${PROJECT}-${AGENT}-${TIMESTAMP}" \
  --cpus="${DOCKER_CPUS:-2}" \
  --memory="${DOCKER_MEMORY:-4g}" \
  --pids-limit=256 \
  -v "${HOME}/.claude:/home/agent/.claude:ro" \
  -v "${WORKTREE_PATH}:/workspace:rw" \
  -v "${AGENT_DATA_PATH}:/agent-data:ro" \
  -v "${OUTPUT_PATH}:/output:rw" \
  "${PROJECT_IMAGE}" \
  /bin/bash -c '
    cd /workspace

    # Claude Code reads the prompt and does its work.
    # --verbose gives us a session summary on stdout.
    # The prompt instructs Claude to write outputs to /output/
    claude -p "$(cat /agent-data/current_prompt.md)" \
      --output-format json \
      --verbose \
      > /output/claude_session.json 2>&1

    echo $? > /output/exit_code
  '
```

**Key details about the Docker setup:**

- `~/.claude:ro` — read-only mount of Claude authentication. This gives the container access to the user's Claude subscription without exposing credentials for modification. Claude Code inside the container authenticates against the same account.
- `/workspace:rw` — the git worktree, where Claude Code makes code changes.
- `/agent-data:ro` — the prompt file and any reference materials.
- `/output:rw` — where Claude Code writes its report and the session metadata.
- Network access is needed for Claude Code to reach the Anthropic API. We use Docker's default bridge network but could restrict to only Anthropic's API endpoints via iptables rules in the container.

**Step 3: Claude Code does its full agent loop**

Claude Code reads the prompt via `-p`, then autonomously:
- Explores the codebase
- Runs tests, linters, profilers
- Writes code, runs it, debugs failures
- Iterates until satisfied
- Writes its report/output to `/output/report.md`

The prompt's output contract (see §5.4) tells it exactly what files to produce and where.

**Step 4: Coordinator reads output files**

```python
async def collect_results(output_path: str) -> AgentResult:
    exit_code = int(read_file(f"{output_path}/exit_code"))

    report = read_file_if_exists(f"{output_path}/report.md")
    completion = read_file_if_exists(f"{output_path}/completion.md")
    knowledge_update = read_file_if_exists(f"{output_path}/knowledge_update.md")

    # Parse the JSON session output for metadata
    session = json.loads(read_file(f"{output_path}/claude_session.json"))
    # session.result contains Claude's final text response
    # session.cost_usd if available gives cost info

    return AgentResult(
        exit_code=exit_code,
        report=report,
        completion=completion,
        knowledge_update=knowledge_update,
        session_metadata=session,
    )
```

### 5.4 Output Contract (in the prompt)

Every mode's prompt ends with a clear output contract. With `--output-format stream-json` (see §7.3), the full execution trace is captured automatically. The sub-agent focuses on the work, not on report formatting.

```markdown
## Output Contract

### For Audit Mode:
- Focus on analyzing the codebase. Your reasoning and findings will be captured
  via the execution stream and formatted into a report automatically.
- If you find critical issues, write a brief `/output/critical.md` flagging them
  for immediate human attention.

### For Implement Mode:
- All code changes directly in /workspace/ (the git worktree).
- Run the test suite after your changes. The test output is captured automatically.
- If tests fail after your changes, debug and fix them before finishing.

### For Maintain Mode:
- Write `/output/knowledge_update.md` — a concise summary of what was learned
  from the last implementation cycle (replaces current knowledge.md).

### For Configure Mode:
- Config files written directly to /workspace/.
- Run any relevant checks (linting, formatting) to verify the configuration works.

If you encounter blocking issues, write `/output/blocked.md` explaining the problem.
```

**Note on report generation:** In earlier versions of this design, the sub-agent was responsible for writing structured markdown reports. With stream-json output, the coordinator or a cheap model (Haiku) generates reports from the execution trace (see §7.4). This is more reliable, produces consistent formatting, and keeps the sub-agent's prompt focused on the actual task.

### 5.5 The `claude -p` Question: Is It Enough?

`claude -p` (print/pipe mode) runs Claude Code in a **non-interactive single-prompt mode**. The question is whether it can handle complex multi-step tasks.

**What `claude -p` does:**
- Accepts a single prompt via argument or stdin.
- Executes Claude Code's full agent loop — including tool use, file editing, shell commands, iterative debugging.
- Runs until the task is complete or it hits a limit.
- Outputs the final response (or JSON with `--output-format json`).

**What `claude -p` does NOT do:**
- No interactive follow-up (no "hmm, what about X?" mid-session).
- No mid-stream human approval.
- Less sophisticated context management for very long sessions compared to interactive mode.

**Assessment: `claude -p` is sufficient for sub-agents.** Here's why:

- Sub-agent tasks are well-scoped by design. The coordinator breaks work into discrete tasks via audit → approve → implement phases. Each invocation has a clear objective.
- The prompt includes all necessary context (role + knowledge + task description + output contract). There's nothing for a human to interject about.
- Complex debugging loops work fine — Claude Code's tool-use and iteration capability is the same in `-p` mode as interactive mode.
- If a task is truly too complex for a single `-p` invocation, the agent writes `/output/blocked.md` and the coordinator escalates or re-scopes the task.

**For tasks that genuinely need multi-turn interaction** (rare), we can fall back to feeding Claude Code via a PTY with `expect`-like automation, or use the `--resume` flag to continue a session. But this is an optimization, not a launch requirement.

### 5.6 Multi-Provider Support

The primary agent runtime is Claude Code. For other providers:

| Provider | Approach |
|---|---|
| **Claude (primary)** | Claude Code in Docker via `claude -p` |
| **OpenAI Codex** | Codex CLI in Docker via `codex -p` (same file-based I/O pattern) |
| **Gemini** | Gemini CLI agent if/when available, or fall back to custom API loop for Gemini only |
| **Custom API** | Available as a fallback provider — a custom tool-use loop, used only when no CLI agent is available |

The `docker_run.sh` script is parameterized:

```toml
[providers]
default = "claude-code"    # uses claude -p inside Docker
# testing = "codex"        # uses codex inside Docker
# security = "api-claude"  # uses custom API loop (fallback)
```

```bash
# docker_run.sh reads the provider from config and adjusts the entrypoint:
case "$PROVIDER" in
  claude-code)
    CMD="claude -p \"\$(cat /agent-data/current_prompt.md)\" --output-format json --verbose"
    ;;
  codex)
    CMD="codex -q \"\$(cat /agent-data/current_prompt.md)\""
    ;;
  api-*)
    CMD="python /ateam/api_agent.py --provider ${PROVIDER#api-} --prompt /agent-data/current_prompt.md"
    ;;
esac
```

### 5.7 Sub-Agent List

| Agent ID | Mission |
|---|---|
| `testing` | Run tests, identify gaps, add coverage, regression testing |
| `refactor` | Code quality, abstraction layers, coupling reduction |
| `security` | Vulnerability scanning, code patterns, dependency CVEs |
| `performance` | Profiling, bottleneck identification, optimization |
| `deps` | Dependency health, updates, consolidation, removal |
| `docs-internal` | Architecture docs, code structure, dev guides |
| `docs-external` | User-facing docs, README, installation, usage |

---

## 6. Git Strategy

### 6.1 Repository Layout

```
/var/ateam/repos/
  PROJECT_NAME/
    bare/                    # bare clone: git clone --bare <remote>
    coordinator/             # worktree for coordinator read-only browsing
    worktrees/
      testing-20260226/      # ephemeral worktree for a specific task
      refactor-20260226/     # ...
```

### 6.2 Workflow

1. **Human pushes** to the remote repository.
2. **Coordinator fetches** into the bare repo: `git -C bare/ fetch origin`.
3. **Coordinator updates** its own worktree and detects new commits.
4. **Sub-agent worktrees** are created per task from a specific branch/commit:
   ```bash
   git -C bare/ worktree add ../worktrees/testing-20260226 origin/main
   ```
5. **Sub-agent makes changes** in its worktree (bind-mounted into Docker, where Claude Code runs).
6. **Coordinator reviews** the changes (diff), decides to merge or discard.
7. **Approved changes** are committed to a branch, optionally rebased onto main, and pushed (with human approval for significant changes).
8. **Worktrees are cleaned up** after task completion.

### 6.3 Conflict Resolution

When a rebase produces conflicts:
1. Coordinator identifies conflicting files.
2. The relevant sub-agent is re-invoked in **Implement** mode with conflict markers as context.
3. The agent resolves conflicts, the coordinator verifies the build passes, then continues the rebase.

---

## 7. Docker Execution Model

### 7.1 Building on Anthropic's Devcontainer Sandbox

Anthropic ships a reference devcontainer specifically designed for running `claude --dangerously-skip-permissions` unattended. This is exactly ATeam's sub-agent use case. The reference container provides:

- **Base image:** `node:20-bookworm-slim` with Claude Code pre-installed (`npm install -g @anthropic-ai/claude-code`).
- **Non-root user:** Runs as `node` user (uid 1000) with limited sudo access (only for the firewall script).
- **Default-deny egress firewall:** Uses `iptables` + `ipset` to allowlist only essential domains and block everything else. The allowlist includes:
  - `api.anthropic.com` (LLM API)
  - `github.com`, `raw.githubusercontent.com` (git operations)
  - `registry.npmjs.org` (npm packages)
  - DNS and SSH (outbound)
  - Localhost/loopback
- **Linux capabilities:** Requires `NET_ADMIN` and `NET_RAW` to configure iptables inside the container.
- **Firewall verification:** The startup script tests that blocked domains are unreachable and allowed domains work, failing the container if the firewall is broken.

ATeam uses this as the **base layer** for sub-agent containers. Per-project Dockerfiles extend it with project-specific toolchains and dependencies.

### 7.2 Layered Container Architecture

```
┌────────────────────────────────────────────────┐
│ Layer 3: Project Runtime                        │
│   npm install / pip install / cargo build       │
│   Project-specific tools (postgres client, etc.)│
│   COPY package.json / requirements.txt          │
├────────────────────────────────────────────────┤
│ Layer 2: ATeam Agent Layer                      │
│   Additional tools: ripgrep, fd, jq, tree       │
│   ATeam init-firewall.sh (extends allowlist)    │
│   /agent-data/ and /output/ mount points        │
├────────────────────────────────────────────────┤
│ Layer 1: Anthropic Devcontainer Base            │
│   node:20 + Claude Code + non-root user         │
│   Default-deny egress firewall                  │
│   NET_ADMIN / NET_RAW capabilities              │
└────────────────────────────────────────────────┘
```

**Base Dockerfile (ATeam agent layer, extends Anthropic's devcontainer):**

```dockerfile
FROM node:20-bookworm-slim

# System tools for Claude Code agent work
RUN apt-get update && apt-get install -y \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree bat htop unzip \
    iptables ipset iproute2 dnsutils \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Non-root user (matching Anthropic reference)
ARG USERNAME=node
RUN groupmod --gid 1000 $USERNAME \
    && usermod --uid 1000 --gid 1000 $USERNAME \
    && chown -R 1000:1000 /home/$USERNAME

# Firewall script (adapted from Anthropic's init-firewall.sh)
COPY init-firewall.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/init-firewall.sh

# Sudo only for firewall initialization
RUN echo "$USERNAME ALL=(ALL) NOPASSWD: /usr/local/bin/init-firewall.sh" >> /etc/sudoers

# Agent working directories
RUN mkdir -p /agent-data /output && chown $USERNAME:$USERNAME /agent-data /output

USER $USERNAME
WORKDIR /workspace
```

**Per-project Dockerfile (extends base with project deps):**

```dockerfile
FROM ateam-base:latest

# Project-specific toolchain
USER root
RUN apt-get update && apt-get install -y python3 python3-pip postgresql-client
# (or: RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh)
USER node

# Project dependencies (pre-installed so container can run network-restricted)
COPY --chown=node:node package.json package-lock.json /workspace/
RUN cd /workspace && npm ci

# Extend firewall allowlist if project needs additional domains
# e.g., private registries, test APIs
COPY --chown=node:node project-firewall-additions.sh /usr/local/bin/
```

Projects declare their container requirements in `config.toml`:

```toml
[docker]
base_image = "ateam-base:latest"        # or a pre-built project image
dockerfile = "./Dockerfile"              # per-project Dockerfile
extra_firewall_domains = ["pypi.org", "files.pythonhosted.org"]  # for pip
cpus = 2
memory = "4g"
```

### 7.3 Sub-Agent Execution: `--dangerously-skip-permissions` + `--output-format stream-json`

Sub-agents run inside Docker with two critical flags:

**`--dangerously-skip-permissions`:** Bypasses all Claude Code permission prompts, enabling fully unattended operation. This is safe because:
- The container is isolated via Docker (filesystem, process, network).
- The egress firewall restricts network access to only the LLM API and essential services.
- The worktree is a disposable git branch — damage is limited and reversible.
- The coordinator validates all outputs before merging.

**`--output-format stream-json`:** Streams structured JSON events in real-time as the agent works. This enables:
- **Live observation:** The coordinator (or a monitoring tool) can watch what the sub-agent is doing in real-time — which files it's reading, what commands it's running, what it's thinking.
- **Token tracking:** The stream includes usage metadata (input/output tokens per turn).
- **Progress estimation:** The coordinator can detect if the agent is stuck (no events for N minutes) and kill the container.
- **Structured output:** Instead of the sub-agent writing its own markdown report, the coordinator (or a cheap model like Haiku) can post-process the stream-json into a clean report. This is more reliable than asking the sub-agent to format its own output, and it captures the full reasoning trace.

**Execution flow:**

```bash
#!/bin/bash
# run-subagent.sh — invoked by ATeam MCP server's subagent_run_audit tool

PROJECT="$1"
AGENT="$2"
WORKTREE="/var/ateam/repos/$PROJECT/worktrees/$AGENT-$(date +%Y%m%d%H%M)"
PROMPT_FILE="/var/ateam/workspace/$PROJECT/$AGENT/current_prompt.md"
OUTPUT_DIR="/var/ateam/workspace/$PROJECT/$AGENT/work/$(date +%Y-%m-%d_%H%M)"
STREAM_LOG="$OUTPUT_DIR/stream.jsonl"

mkdir -p "$OUTPUT_DIR"

# Create ephemeral git worktree
git -C "/var/ateam/repos/$PROJECT/bare" worktree add "$WORKTREE" origin/main

# Run sub-agent in Docker
docker run --rm \
  --name "ateam-$PROJECT-$AGENT" \
  --cap-add NET_ADMIN --cap-add NET_RAW \
  --cpus=2 --memory=4g --pids-limit=256 \
  -v "$WORKTREE:/workspace:rw" \
  -v "$PROMPT_FILE:/agent-data/current_prompt.md:ro" \
  -v "$HOME/.claude:/home/node/.claude:ro" \
  -v "$OUTPUT_DIR:/output:rw" \
  "$PROJECT_DOCKER_IMAGE" \
  bash -c '
    # Initialize firewall (allowlist only)
    sudo /usr/local/bin/init-firewall.sh

    # Run Claude Code: one-shot, no permissions, streaming JSON
    claude -p "$(cat /agent-data/current_prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      2>/output/stderr.log \
      | tee /output/stream.jsonl
  '

EXIT_CODE=$?

# Post-process: coordinator or Haiku generates the markdown report
# from the stream.jsonl trace (see §7.4)
echo "$EXIT_CODE" > "$OUTPUT_DIR/exit_code"
```

### 7.4 Report Generation from Stream JSON

Instead of relying on the sub-agent to write its own markdown report (which wastes context window and can be inconsistent), the stream-json output captures the agent's full execution trace. The coordinator then generates the report:

**Option A (cheapest):** The MCP server's `format_report` tool extracts key events from `stream.jsonl` (tool calls, file edits, test results, final assistant messages) and templates them into a structured markdown report. Pure code, no LLM needed.

**Option B (better quality):** The `format_report` tool sends the extracted events to a cheap model (Haiku) with a formatting prompt: "Summarize this agent execution into an audit report following this template: [...]". This produces cleaner, more insightful reports at minimal cost (~$0.01 per report).

**Option C (hybrid):** Use Option A for the raw structured data (findings, test results, code changes) and Option B for the narrative summary. This gives you machine-readable data plus human-readable prose.

The sub-agent's prompt is simplified: instead of "write a report to /output/report.md following this template," it just says "analyze the codebase for testing gaps" or "implement these approved changes." The agent focuses on the work, not on report formatting.

### 7.5 Network Policy

The base firewall (from init-firewall.sh) implements default-deny egress with allowlist:

```bash
# Core allowlist (in init-firewall.sh)
ALLOWED_DOMAINS=(
  "api.anthropic.com"           # Claude API
  "github.com"                  # git operations
  "raw.githubusercontent.com"   # git operations
  "registry.npmjs.org"          # npm (for Claude Code updates)
)

# Per-project additions (from config.toml extra_firewall_domains)
# e.g., pypi.org, crates.io, rubygems.org, private registries
```

For projects that need `npm install` or `pip install` at runtime, the Dockerfile should pre-install all dependencies so the container can run with a minimal allowlist. If runtime package installation is unavoidable, add the relevant registries to `extra_firewall_domains` in `config.toml`.

### 7.6 Resource Limits

Containers are constrained via Docker resource flags:
- `--cpus`: default 2, configurable per project.
- `--memory`: default 4GB, configurable.
- `--pids-limit`: prevent fork bombs (default 256).
- `--cap-add NET_ADMIN --cap-add NET_RAW`: required for iptables firewall setup.
- Coordinator watchdog monitors `stream.jsonl` output — kills containers if no events for configurable timeout (default 5 minutes).

---

## 8. Coordinator Agent

### 8.1 Implementation

The coordinator is **Claude Code itself**, connected to the ATeam MCP server (see §3.1, Option D). The MCP server provides infrastructure tools (sub-agent lifecycle, git management, budget tracking), while Claude Code provides the reasoning (report analysis, prioritization, conflict resolution, decision-making).

**Interactive mode:** The human runs `claude --mcp-server ateam` and talks to the coordinator directly: "Run the testing agent on myapp" → Claude Code calls `subagent_run_audit("testing", "myapp")` → reads the report → decides whether to approve → calls `subagent_run_implement` if appropriate.

**Autonomous mode:** A thin scheduler (cron, systemd timer, or Go daemon) periodically invokes:
```bash
claude -p "Run the ATeam night cycle for project myapp. \
  Check the schedule profile, run due agents, review reports, \
  implement approved findings, update knowledge files." \
  --mcp-server ateam \
  --allowedTools 'mcp:ateam:*' Bash Read Write Edit Glob Grep \
  --output-format json
```

The coordinator's system prompt (injected via `CLAUDE.md` or the `-p` flag) defines its decision-making behavior: always run testing first, auto-approve low-risk changes, queue high-risk for human review, respect budget limits, update changelog.

**Key MCP tools available to the coordinator:**

```
subagent_run_audit(agent, project)         → Docker: run audit, return report path
subagent_run_implement(agent, project, report) → Docker: implement findings, return completion path
subagent_commit_and_merge(agent, project)  → git: commit changes, run tests, merge if green
format_report(report_path, model?)         → summarize/reformat report (delegate to Haiku)
get_pending_reports(project)               → list reports awaiting review
update_knowledge(agent, project, summary)  → append to knowledge.md
get_budget_status(project)                 → runs today, cost estimate, remaining budget
get_schedule_profile()                     → "night"/"day", allowed agents, max parallel
create_worktree(project, agent)            → create git worktree
cleanup_worktree(project, agent)           → remove worktree
get_recent_commits(project, since)         → list recent commits
```

Claude Code also uses its built-in tools natively: reading reports (Read), inspecting code (Grep, Glob), writing changelog entries (Write), running test commands (Bash). The MCP server only handles the infrastructure operations that need Docker, git worktrees, or budget state.

### 8.2 Scheduler

Use `APScheduler` for in-process scheduling:

```python
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.cron import CronTrigger

scheduler = AsyncIOScheduler()

# Check for new commits every 5 minutes
scheduler.add_job(check_new_commits, CronTrigger(minute="*/5"))

# Night mode: aggressive background tasks at 11pm
scheduler.add_job(run_full_audit_cycle, CronTrigger(hour="23"))
```

Schedule profiles in `config.toml`:

```toml
[schedule]
timezone = "America/Los_Angeles"
commit_check_interval_minutes = 5

[schedule.night]
start = "23:00"
end = "05:00"
max_parallel_agents = 4
allowed_agents = ["testing", "refactor", "security", "performance", "deps", "docs-internal", "docs-external"]

[schedule.day]
start = "05:00"
end = "23:00"
max_parallel_agents = 1
allowed_agents = ["testing"]
```

### 8.3 Decision Loop

```
on new_commits:
    1. ALWAYS run testing agent first
    2. If tests fail → stop, notify human
    3. If tests pass → queue other agents based on schedule profile

on schedule_trigger:
    1. Check schedule profile (night vs day)
    2. Check resource headroom (CPU, memory, running containers)
    3. Select highest-priority queued agents
    4. Run agents (parallel up to max_parallel_agents)
    5. Collect output files from each agent
    6. Triage:
       - Agent wrote report.md only → store, queue for review
       - Agent wrote completion.md + code changes → run testing agent to verify
       - Agent wrote blocked.md → log, notify human
    7. Auto-approve low-risk changes (test additions, doc updates)
    8. Queue medium/high-risk for human review
    9. Update changelog.md

on human_override:
    Parse instruction, reprioritize queue
    e.g., "Focus on regression testing then security"
    → Immediately queue testing agent, then security agent
    → Deprioritize other agents
```

### 8.4 Human Interaction Interface

A lightweight CLI:

```
$ ateam status
  Project: myapp
  Last commit checked: abc123 (2h ago)
  Active agents: testing (running 12m), refactor (queued)
  Schedule: night mode (3 of 4 slots available)
  Reports pending review: 2

$ ateam focus "regression testing, then security audit"
  → Priority updated. Testing agent starting now.

$ ateam approve testing/2026-02-26_2300_report.md
  → Queuing implementation of approved report.

$ ateam reject refactor/2026-02-26_2300_report.md --reason "too risky before release"
  → Noted. Report archived.

$ ateam log
  [2026-02-26 23:00] Coordinator: New commits detected (abc123..def456)
  [2026-02-26 23:01] Testing agent: Started (audit mode)
  [2026-02-26 23:15] Testing agent: Completed — report generated
  [2026-02-26 23:16] Coordinator: Auto-approved (low-risk test additions)
  [2026-02-26 23:16] Testing agent: Started (implement mode)
  ...

$ ateam pause          # stop all background work
$ ateam resume         # resume background work
```

---

## 9. Resource Monitoring and Cost Control

### 9.1 Token Tracking (Best-Effort)

Since Claude Code doesn't expose per-invocation token counts programmatically, we use a combination of approaches:

**Approach 1: Parse Claude Code's JSON output**

With `--output-format json`, Claude Code returns session metadata that may include cost information:

```python
session = json.loads(read_file(f"{output_path}/claude_session.json"))
# Fields available (subject to Claude Code version):
# - session.cost_usd (if present)
# - session.result (final text response)
# - session.duration_ms
```

**Approach 2: Time-based estimation**

Track wall-clock time per agent invocation. Correlate with known token rates to estimate usage:

```python
@dataclass
class AgentRun:
    agent_id: str
    project: str
    provider: str
    start_time: datetime
    end_time: datetime
    duration_seconds: int
    exit_code: int
    output_size_bytes: int    # rough proxy for output tokens
    estimated_cost_usd: float  # from time-based heuristic or session metadata
```

**Approach 3: External billing monitoring**

Periodically check the Anthropic usage dashboard (or API if available) to reconcile estimated vs actual usage.

### 9.2 Budget and Throttling

Even without exact token counts, we can throttle effectively:

```toml
[budget]
max_daily_agent_runs = 50             # hard cap on invocations per day
max_concurrent_containers = 4         # Docker concurrency limit
max_agent_runtime_minutes = 60        # kill after this
estimated_cost_per_run_usd = 0.50     # conservative estimate
daily_cost_limit_usd = 25.00          # stop all agents if exceeded
warning_threshold = 0.75
```

| Budget Used | Behavior |
|---|---|
| 0–75% | Normal operation |
| 75–95% | Only high-priority tasks, limit to 1 parallel agent |
| 95–100% | Only `testing` agent (keep the build green) |
| 100%+ | All agents paused, human notified |

### 9.3 System Resource Monitoring

```python
import psutil

def can_spawn_agent() -> bool:
    cpu = psutil.cpu_percent(interval=1)
    mem = psutil.virtual_memory().percent

    if cpu > 80 or mem > 85:
        return False
    return True
```

### 9.4 Container Watchdog

```python
import docker

async def watchdog(container_name: str, timeout_minutes: int):
    client = docker.from_env()
    container = client.containers.get(container_name)
    deadline = time.time() + (timeout_minutes * 60)

    while time.time() < deadline:
        await asyncio.sleep(30)
        container.reload()
        if container.status != "running":
            return  # container exited normally

        # Check if output files are being updated (sign of progress)
        stats = container.stats(stream=False)
        # ... monitor CPU/memory

    # Timeout — kill it
    container.kill()
    logger.warning(f"Killed {container_name}: exceeded {timeout_minutes}m timeout")
```

---

## 10. Debugging and Operations

### 10.1 Container Naming Convention

Every sub-agent container uses a deterministic name: `ateam-{project}-{agent}`. This enforces a single-instance constraint per agent role and makes containers easy to identify, attach to, and manage.

```
ateam-myapp-testing
ateam-myapp-refactor
ateam-myapp-security
ateam-myapp-performance
ateam-myapp-deps
ateam-myapp-docs-internal
ateam-myapp-docs-external
```

The MCP server enforces this: `subagent_run_audit("testing", "myapp")` first checks if `ateam-myapp-testing` already exists. If it's running, the tool returns an error describing the existing container's state (interactive session, autonomous run, or stale). The coordinator can then decide whether to wait, skip, or ask the human.

In the future, this constraint can be relaxed to allow parallel instances (e.g., `ateam-myapp-testing-1`, `ateam-myapp-testing-2`), but for v1 the single-instance model is simpler and avoids worktree conflicts.

### 10.2 Suspend / Resume Coordinator

The autonomous coordinator (the scheduler daemon that periodically invokes Claude Code) supports pause/resume:

```bash
# Suspend: stop scheduling new agent runs, let running agents finish
ateam pause
# → writes a lockfile: ~/.ateam/paused
# → the scheduler loop checks for this file and skips cycles
# → currently running containers continue to completion

# Resume: re-enable the scheduler
ateam resume
# → removes the lockfile
# → next scheduler tick proceeds normally

# Check status
ateam status
# → shows: coordinator paused/running, active containers, pending queue, budget
```

The MCP server also exposes this as a tool:
```
pause_coordinator()   → creates lockfile, returns current state
resume_coordinator()  → removes lockfile, returns current state
get_coordinator_status() → paused/running, active containers, last cycle time
```

The coordinator Claude Code instance (in interactive mode) can also pause/resume itself: "Pause the night cycle, I'm doing manual work on the testing agent."

### 10.3 Interactive Agent Sessions

Any sub-agent can be launched in interactive mode instead of autonomous `-p` mode. This drops the human into the same Docker environment the agent would use, with the same prompt, worktree, and context — but with an interactive Claude Code session instead of a one-shot invocation.

```bash
# Launch interactive session as the testing agent on myapp
ateam shell myapp testing

# This:
# 1. Creates the worktree (if not already present)
# 2. Builds the prompt (same as autonomous mode)
# 3. Starts the Docker container with the deterministic name (ateam-myapp-testing)
# 4. Drops you into an interactive Claude Code session:
#    claude --dangerously-skip-permissions \
#           --system-prompt "$(cat /agent-data/current_prompt.md)"
# 5. Mounts the same volumes: worktree at /workspace, prompt at /agent-data,
#    output at /output, .claude at /home/node/.claude
```

Inside the interactive session, the human can:
- Investigate issues the autonomous agent got stuck on.
- Manually run parts of the audit/implement workflow.
- Test changes before letting the autonomous agent take over.
- Use all of Claude Code's interactive features (follow-up questions, exploration, undo).

**Exiting:** When the human exits the interactive session (`/exit` or Ctrl-D), the container stops. The worktree and any `/output/` files are preserved. The human can then let the autonomous cycle resume, or continue with another `ateam shell` session.

### 10.4 Distinguishing Interactive vs Stale Containers

When the coordinator (or MCP server) encounters an already-running container `ateam-myapp-testing`, it needs to determine: is this a human interactive session, a normally-running autonomous agent, or a stale/hung process?

**Mechanism: state file + heartbeat.**

When a container starts, the entrypoint writes a state file to the shared output volume:

```json
// /output/.ateam-state.json
{
  "mode": "interactive",      // or "autonomous"
  "started_at": "2026-02-26T23:00:00Z",
  "pid": 42,
  "heartbeat": "2026-02-26T23:15:00Z"
}
```

- **`mode: "interactive"`** — launched via `ateam shell`. The coordinator knows a human is working and does not interfere. It logs "testing agent in interactive session, skipping" and moves on.
- **`mode: "autonomous"`** — launched via the normal MCP `subagent_run_audit` tool. The coordinator monitors `stream.jsonl` for activity.
- **`heartbeat`** — updated periodically (every 60 seconds) by a background process in the container. For autonomous mode, the stream-json output itself serves as the heartbeat. For interactive mode, a simple `while true; do date > /output/.ateam-heartbeat; sleep 60; done &` background process.

**Decision logic in the MCP server:**

```
container exists?
  ├─ no → start new container
  └─ yes → read .ateam-state.json
       ├─ mode: interactive → return "agent in interactive session, skipping"
       ├─ mode: autonomous + heartbeat recent (<5 min) → return "agent running normally"
       ├─ mode: autonomous + heartbeat stale (>5 min) → return "agent appears hung"
       │    → coordinator decides: kill and retry, or notify human
       └─ state file missing/unreadable → check docker inspect
            ├─ container running → assume stale, warn human
            └─ container exited → cleanup and start new
```

### 10.5 Manual MCP Commands in Interactive Sessions

When inside an interactive `ateam shell` session, the human should be able to run the same operations the coordinator would. The ATeam MCP server can also be attached to the interactive Claude Code session:

```bash
# Launch interactive session WITH MCP server attached
ateam shell myapp testing --with-mcp

# Now inside Claude Code, you can use coordinator-level tools:
# "Format the findings I just wrote into a proper audit report"
#   → Claude calls format_report(...)
# "Commit my changes and run the test suite"
#   → Claude calls subagent_commit_and_merge(...)
# "Update the testing knowledge file with what I learned"
#   → Claude calls update_knowledge(...)
# "What's the budget status?"
#   → Claude calls get_budget_status(...)
```

This means the human in an interactive session can do everything the autonomous coordinator does, but manually and with full control. The files (reports, knowledge updates, changelog) are written to the same paths the autonomous system uses, so there's no divergence.

**Without `--with-mcp`:** The human is in a plain Claude Code session in the agent's environment. They can still manually write files to `/output/`, edit the worktree, run tests, etc. They just don't have access to the coordinator's infrastructure tools (commit-and-merge, format-report, etc.).

### 10.6 Troubleshooting Commands

```bash
# ── Status & Monitoring ──────────────────────────────────────────

ateam status                      # Overview: coordinator state, active containers,
                                  # pending queue, budget, last cycle time

ateam status myapp                # Per-project: enabled agents, last run per agent,
                                  # pending reports, worktree status

ateam containers                  # List all ateam-* Docker containers with state,
                                  # mode (interactive/autonomous), uptime, resource usage

# ── Agent Inspection ─────────────────────────────────────────────

ateam logs myapp testing          # Tail the stream.jsonl for the current/last run
                                  # (shows what the agent is doing in real-time)

ateam logs myapp testing --stderr # Show stderr output (errors, warnings)

ateam logs myapp testing --last   # Show the stream.jsonl from the most recent
                                  # completed run (not the current one)

ateam report myapp testing        # Show the latest formatted report for this agent

ateam diff myapp testing          # Show git diff of the agent's worktree vs main
                                  # (what changes has the agent made?)

# ── Agent Control ────────────────────────────────────────────────

ateam shell myapp testing         # Interactive session (see §10.3)

ateam kill myapp testing          # Kill a running/hung container immediately

ateam retry myapp testing         # Kill if running, clean up worktree, re-run
                                  # the last task from scratch

ateam skip myapp testing          # Mark this agent as "skip" for the current cycle
                                  # (coordinator won't run it tonight)

# ── Coordinator Control ──────────────────────────────────────────

ateam pause                       # Suspend autonomous scheduling
ateam resume                      # Resume autonomous scheduling
ateam run myapp                   # Manually trigger a full cycle for a project
                                  # (even if paused — one-shot override)
ateam run myapp testing           # Manually trigger a specific agent

# ── Maintenance & Cleanup ────────────────────────────────────────

ateam cleanup myapp               # Remove stale worktrees, exited containers,
                                  # old stream.jsonl files (>30 days)

ateam worktrees myapp             # List all worktrees for a project with status

ateam doctor                      # Health check: verify Docker, git, Claude Code
                                  # installation, MCP server connectivity, firewall

ateam doctor --fix                # Auto-repair: remove stale lockfiles, prune
                                  # orphaned containers, fix worktree state

# ── History & Audit ──────────────────────────────────────────────

ateam history myapp               # Show recent changelog entries (coordinator decisions)

ateam history myapp testing       # Show run history for a specific agent:
                                  # timestamps, modes, exit codes, report summaries

ateam budget                      # Show budget status across all projects:
                                  # runs today, estimated cost, remaining budget
```

### 10.7 Common Debugging Scenarios

**"The testing agent keeps failing on my project"**
```bash
ateam logs myapp testing --last     # What happened in the last run?
ateam shell myapp testing           # Enter interactive mode, reproduce the issue
# ... investigate in Claude Code ...
# Found it: the test suite needs a running database.
# Fix: add postgres to the project Dockerfile, update config.toml
```

**"I want to manually run a security audit right now"**
```bash
ateam pause                         # Stop autonomous cycles
ateam shell myapp security          # Enter as the security agent
# ... interactive Claude Code session ...
# "Audit this codebase for SQL injection vulnerabilities"
# ... review findings, iterate ...
ateam resume                        # Let the autonomous cycle continue
```

**"The refactor agent made changes I don't like"**
```bash
ateam diff myapp refactor           # See what it changed
ateam history myapp refactor        # See the coordinator's decision to approve it
# If not yet merged:
ateam kill myapp refactor           # Stop the agent
cd /var/ateam/repos/myapp/worktrees/refactor-*
git checkout -- .                   # Discard changes
# If already merged:
git revert HEAD                     # Revert the merge commit
```

**"Something is running but I don't know what"**
```bash
ateam containers                    # Show all running ateam-* containers
ateam logs myapp testing            # Watch real-time activity
ateam status                        # Full system overview
```

---

## 11. File Hierarchy

### 10.1 ATeam Framework Source

```
ateam/                                 # framework source (Python package)
  src/
    ateam/
      __init__.py
      cli.py                           # CLI entry point
      coordinator.py                   # main coordinator logic
      scheduler.py                     # schedule management
      git_manager.py                   # bare repo, worktree management
      docker_runner.py                 # container lifecycle + output collection
      resource_monitor.py              # CPU/memory/container monitoring
      budget.py                        # cost tracking and throttling
      config.py                        # config parsing
      prompt_builder.py                # assembles layered prompts
      providers/
        __init__.py
        base.py                        # provider interface
        claude_code.py                 # claude -p invocation
        codex.py                       # codex CLI invocation
        api_fallback.py                # custom API tool-use loop (fallback)
  tests/
  pyproject.toml
```

### 10.2 User Workspace (Git-Versioned)

The entire workspace below is a git repository (see §19). The `.gitignore` excludes bulky/ephemeral data like bare repos, worktrees, and transient prompt files.

```
my_projects/                           # root workspace — itself a git repo
  .agents/                             # default agent definitions (shared across projects)
    coordinator_role.md
    testing/
      role.md
    refactor/
      role.md
    security/
      role.md
    performance/
      role.md
    deps/
      role.md
    docs-internal/
      role.md
    docs-external/
      role.md

  .common/                             # tech-stack culture (cross-project knowledge)
    golang.md                          # Go conventions, preferred patterns, common pitfalls
    typescript.md                      # TS/JS conventions, preferred libraries
    python.md                          # Python conventions, tooling preferences
    postgresql.md                      # PostgreSQL patterns, query optimization notes
    docker.md                          # Dockerfile best practices, common configurations
    react.md                           # React patterns, state management preferences
    cli_fd.md                          # notes on using fd (find replacement)
    cli_ripgrep.md                     # notes on using ripgrep
    cli_jq.md                          # notes on using jq
    testing_jest.md                    # Jest patterns and conventions
    testing_pytest.md                  # Pytest patterns and conventions
    security_owasp.md                  # OWASP top 10 patterns to check
    linting_eslint.md                  # ESLint preferred configs
    linting_ruff.md                    # Ruff preferred configs
    # ... files are named after what they describe
    # agents pick up only the files relevant to their project's stack

  PROJECT_NAME/                        # per-project configuration
    config.toml                        # project config (enabled agents, schedule, budget)
    Dockerfile                         # build environment for sub-agent containers
    docker_run.sh                      # container launch helper
    project_goals.md                   # project-specific instructions and priorities
    changelog.md                       # coordinator decision log
    testing/
      role.md                          # project-specific role overrides
      knowledge.md                     # accumulated project knowledge
      current_prompt.md                # latest assembled prompt (transient, .gitignored)
      work/
        2026-02-26_2300_report.md
        2026-02-26_2300_report_completion.md
    refactor/
      role.md
      knowledge.md
      work/
    security/
      ...
    # (same structure for each enabled agent)
```

### 10.3 Tech-Stack Culture Files (`.common/`)

The `.common/` directory contains **on-demand knowledge files** organized by technology, tool, or pattern. These are not loaded into every agent's context — the prompt builder selects only the files relevant to the current project's stack (as declared in `config.toml`):

```toml
[project]
stack = ["typescript", "react", "postgresql", "docker", "testing_jest", "linting_eslint"]
```

The prompt builder then includes the matching `.common/` files in the agent's system prompt, keeping the context window lean. A Go project's security agent sees `golang.md` and `security_owasp.md`, not `react.md` or `testing_jest.md`.

**How culture files are populated:**
- Initially seeded manually or by running a dedicated "culture harvest" pass on an existing project.
- Sub-agents in **maintain** mode can propose additions to `.common/` files when they learn something project-specific that would be broadly useful.
- The coordinator reviews and commits these updates to the workspace git repo.

**Example `.common/golang.md`:**
```markdown
# Go Tech-Stack Culture

## Error Handling
- Always wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Use sentinel errors for expected conditions, wrapped errors for unexpected
- Never ignore errors; at minimum log them

## Testing
- Table-driven tests preferred for functions with multiple cases
- Use testify/assert for readable assertions
- Prefer testing behavior over implementation details

## Dependencies
- Prefer stdlib over third-party where reasonable
- Use golangci-lint with the project's .golangci.yml
- Run `go vet` and `staticcheck` in CI
```

### 10.4 Relationship to CLAUDE.md Files

ATeam's knowledge system complements — but does not replace — Claude Code's native `CLAUDE.md` mechanism:

- **`~/.claude/CLAUDE.md`** (global): Personal preferences and conventions that apply to all Claude Code sessions, including ATeam sub-agents. This is a good place for universal preferences like "prefer explicit error handling over try/catch" or "always use conventional commits".
- **Per-repo `CLAUDE.md`**: Project-specific instructions that Claude Code reads automatically. Developers should continue maintaining this for their own interactive Claude Code sessions. ATeam sub-agents also benefit from it since they run Claude Code in the project's worktree.

**How these layers interact:**

```
Claude Code context (inside Docker) =
  ~/.claude/CLAUDE.md          (global preferences, mounted via ~/.claude)
  + /workspace/CLAUDE.md       (project CLAUDE.md, in the git worktree)
  + ATeam prompt               (role + culture + knowledge + task)
```

The key distinction: `CLAUDE.md` files are maintained by human developers for their own Claude Code usage. ATeam's `.agents/` roles and `.common/` culture files are maintained by the ATeam system for its specialized agents. They coexist naturally because Claude Code reads `CLAUDE.md` automatically, and ATeam injects its own context via the `-p` prompt.

**Cross-pollination:** The `.common/` tech-stack culture files can harvest knowledge from one project's `CLAUDE.md` and make it available to other projects using the same technology. For example, if Project A's `CLAUDE.md` has detailed PostgreSQL optimization notes, those can be extracted into `.common/postgresql.md` and benefit Project B without Project B's developers having to rediscover them.

---

## 12. Configuration Format

`config.toml` for each project:

```toml
[project]
name = "myapp"
repo_url = "git@github.com:org/myapp.git"
branch = "main"
dockerfile = "./Dockerfile"
stack = ["typescript", "react", "postgresql", "docker", "testing_jest", "linting_eslint"]

[agents]
enabled = ["testing", "refactor", "security", "performance", "deps", "docs-internal", "docs-external"]

[providers]
default = "claude-code"
# testing = "codex"               # override per agent
# security = "api-claude"         # fallback to custom API loop

[schedule]
timezone = "America/Los_Angeles"
commit_check_interval_minutes = 5

[schedule.night]
start = "23:00"
end = "05:00"
max_parallel_agents = 4
allowed_agents = ["testing", "refactor", "security", "performance", "deps", "docs-internal", "docs-external"]

[schedule.day]
start = "05:00"
end = "23:00"
max_parallel_agents = 1
allowed_agents = ["testing"]

[budget]
max_daily_agent_runs = 50
max_concurrent_containers = 4
max_agent_runtime_minutes = 60
estimated_cost_per_run_usd = 0.50
daily_cost_limit_usd = 25.00
warning_threshold = 0.75

[docker]
cpus = 2
memory = "4g"
pids_limit = 256
network = "bridge"                    # needs network for LLM API access
# network_restrict_egress = true     # optional: whitelist only LLM API endpoints

[timeouts]
audit_minutes = 30
implement_minutes = 60
```

---

## 13. Sub-Agent System Prompt Structure

Each sub-agent invocation is constructed from layered prompt files:

```
PROMPT (assembled by coordinator, written to current_prompt.md) =

  # Your Role
  {.agents/{agent_id}/role.md}

  # Tech-Stack Culture (on-demand, based on project stack config)
  {.common/typescript.md}              (if project stack includes typescript)
  {.common/postgresql.md}              (if project stack includes postgresql)
  {.common/testing_jest.md}            (if project stack includes testing_jest)
  # ... only matching files are included

  # Project-Specific Role
  {project/{agent_id}/role.md}         (if exists)

  # Project Knowledge
  {project/{agent_id}/knowledge.md}    (accumulated from past runs)

  # Your Mission This Run
  {mode-specific instructions}
  {task context from coordinator}

  # Output Contract
  {what files to write to /output/}
```

### Example: Testing Agent — Audit Mode Prompt

```markdown
# Your Role

You are a testing specialist agent. Your mission is to ensure comprehensive
test coverage and build reliability for this project.

# Project Knowledge

- Build system: npm, Jest for unit tests, Playwright for E2E
- Test command: `npm test` (unit), `npm run test:e2e` (integration)
- Known flaky test: tests/api/rate-limit.test.ts (timing-dependent)
- Coverage threshold: 80% lines (currently at 76%)

# Your Mission This Run

**Mode: Audit**

New commits since last audit: abc123..def456 (3 commits)
Focus areas requested by human: "regression testing after the new payment feature"

Analyze the codebase and produce a prioritized report of testing gaps.

1. Run the existing test suite. Report any failures.
2. Analyze code coverage. Identify untested critical paths.
3. Review the new commits for testable behavior.
4. Prioritize recommendations by risk (what breaks worst if untested).
5. Estimate effort for each recommended test.

# Output Contract

Write your report to `/output/report.md` using this structure:

## Test Suite Status
[pass/fail, coverage numbers]

## Findings (by priority)
### Critical
### High
### Medium
### Low

## Recommended New Tests
[numbered list with file paths and descriptions]
```

---

## 14. Parallel Execution

Use Python's `asyncio` with semaphore-based concurrency:

```python
class AgentOrchestrator:
    def __init__(self, max_parallel: int):
        self.semaphore = asyncio.Semaphore(max_parallel)

    async def run_agent(self, task: AgentTask) -> AgentResult:
        async with self.semaphore:
            # Create worktree
            worktree = await self.git.create_worktree(task)
            output_dir = create_output_dir(task)

            # Build and write prompt
            prompt = build_prompt(task)
            write_file(f"{task.agent_data_path}/current_prompt.md", prompt)

            try:
                # Launch Docker container (blocks until exit or timeout)
                exit_code = await self.docker.run_agent_container(
                    task=task,
                    worktree_path=worktree,
                    output_path=output_dir,
                    timeout_minutes=task.timeout,
                )

                # Read results from filesystem
                return await collect_results(output_dir)
            finally:
                await self.git.cleanup_worktree(worktree)

    async def run_batch(self, tasks: list[AgentTask]) -> list[AgentResult]:
        return await asyncio.gather(
            *[self.run_agent(t) for t in tasks]
        )
```

---

## 15. Changelog and Audit Trail

The coordinator maintains `changelog.md`:

```markdown
# Changelog

## 2026-02-26

### 23:00 — Commit Check
- New commits detected: abc123..def456 (3 commits by @developer)
- Triggered: testing agent (audit mode)

### 23:15 — Testing Report
- Report: testing/work/2026-02-26_2300_report.md
- Findings: 3 untested endpoints, 1 flaky test
- Decision: AUTO-APPROVED (low-risk, test additions only)
- Triggered: testing agent (implement mode)

### 23:45 — Testing Implementation Complete
- Completion: testing/work/2026-02-26_2300_report_completion.md
- Result: 12 new tests added, 1 flaky test fixed
- Build status: GREEN
- Runtime: 28 minutes

### 23:50 — Night Cycle Started
- Launched: refactor (audit), security (audit)
- Available slots: 2 of 4
- Budget remaining: 72%
```

---

## 16. Implementation Plan

### Phase 1: Foundation (Week 1–2)

- [ ] Python project scaffold (`pyproject.toml`, CLI via `click`)
- [ ] Config parser (`config.toml` with `tomllib`)
- [ ] Git manager: bare clone, worktree create/delete, fetch, detect new commits
- [ ] Docker runner: build image, run container with Claude Code, wait for exit, collect output files
- [ ] Prompt builder: concatenate role + knowledge + mode + task into `current_prompt.md`
- [ ] Basic CLI: `ateam init <project>`, `ateam run <agent> <mode>`, `ateam status`
- [ ] Write the first sub-agent prompt: `testing/role.md` with audit + implement modes

**Milestone:** Can manually trigger the testing agent (Claude Code in Docker) against a real project, read its report, approve it, re-run in implement mode, and see code changes in the worktree.

### Phase 2: Coordinator Logic (Week 3–4)

- [ ] Scheduler with day/night profiles (APScheduler)
- [ ] Commit detection loop
- [ ] Decision logic: auto-approve low-risk, queue high-risk
- [ ] Changelog writer
- [ ] Resource monitor (CPU/memory checks)
- [ ] Container watchdog (timeout + kill)
- [ ] Budget tracker (run counts + time-based cost estimation)
- [ ] CLI: `ateam focus`, `ateam approve`, `ateam reject`, `ateam log`, `ateam pause/resume`

**Milestone:** Coordinator runs as daemon, detects commits, auto-runs testing agent, logs decisions, respects day/night schedule.

### Phase 3: Full Agent Suite (Week 5–6)

- [ ] Write role.md prompts for all 7 sub-agents (audit + implement modes each)
- [ ] Implement maintain and configure modes
- [ ] Knowledge maintenance cycle (post-task knowledge.md updates)
- [ ] Parallel agent execution with semaphore
- [ ] Conflict resolution flow (rebase + re-invoke agent)
- [ ] Human notification (terminal bell, optional webhook)

**Milestone:** Full night cycle runs all agents, produces reports, implements approved changes, maintains knowledge files.

### Phase 4: Multi-Provider and Polish (Week 7–8)

- [ ] Codex CLI provider (same file-based I/O pattern)
- [ ] API fallback provider (custom tool-use loop for providers without CLI agents)
- [ ] Cross-project culture.md maintenance
- [ ] Network egress restrictions (whitelist LLM API endpoints only)
- [ ] Integration tests for coordinator
- [ ] Documentation

**Milestone:** Framework supports Claude Code and Codex interchangeably, with robust resource management.

---

## 17. Key Design Decisions

### Why Claude Code for sub-agents, not a custom API agent?

Claude Code is already an excellent coding agent. Reimplementing file editing, shell execution, iterative debugging, and error recovery in a custom tool-use loop would be hundreds of lines of fragile code that's worse than what Claude Code already does. The tradeoff (less granular token tracking) is worth it.

### Why file-based I/O instead of capturing stdout?

Stdout capture from Claude Code is unreliable for structured data — it mixes progress output, tool results, and final responses. File-based I/O is robust: the prompt tells the agent "write your report to /output/report.md" and the coordinator reads that file. Simple, testable, debuggable.

### Why a Python daemon for the coordinator, not an LLM?

The coordinator makes simple rule-based decisions (scheduling, priority ordering, file I/O). An LLM is overkill and would add latency, cost, and unpredictability. Python gives us deterministic scheduling, Docker SDK integration, and `asyncio` for parallelism. Optional LLM escalation (via Haiku API) handles the rare complex decision.

### Why network access for containers?

Claude Code needs to reach the Anthropic API. We accept this but can restrict egress to only LLM API endpoints for security.

### Why Git worktrees instead of clones?

Worktrees are lightweight — they share the object store with the bare repo. Creating one is instant compared to a full clone. Disposable per-task with no disk waste.

### Why TOML for config?

TOML is human-readable, supports nested sections naturally, and has excellent Python support (`tomllib` in stdlib since 3.11).

---

## 18. Risk Mitigation

| Risk | Mitigation |
|---|---|
| Runaway agent | Container timeout watchdog, Docker resource limits, budget cap on daily runs |
| Agent breaks the build | All changes on branches, never direct to main. Testing agent verifies. |
| Conflicting changes between agents | Each agent gets own worktree. Coordinator merges sequentially. |
| LLM produces incorrect code | Testing agent validates. Human approval for high-risk changes. |
| Costs spiral | Daily run cap, time-based cost estimation, warning/hard-stop thresholds |
| Network exfiltration from container | Restrict egress to LLM API endpoints only |
| Human overwhelm | Minimal notifications, auto-approve low-risk, batch decisions |
| Knowledge files grow unbounded | Maintain mode summarizes and trims. Max file size enforced. |
| Claude Code version breaks things | Pin Claude Code version in Dockerfile. Test upgrades explicitly. |
| `claude -p` insufficient for complex task | Agent writes `blocked.md`, coordinator re-scopes or escalates to human |

---

## 19. Git-Versioned ATeam Configuration

### 18.1 The ATeam Project Directory Is a Git Repo

All agent `.md` files, config files, and coordinator state are under git version control. The per-project ATeam directory (e.g., `my_projects/PROJECT_NAME/`) is itself a git repository. This provides:

- **Timeline view of agent activity.** Every coordinator interaction, every report, every knowledge update is a git commit. You can `git log` the ATeam repo to see exactly what agents did, when, and why.
- **Rollback capability.** If an agent corrupts a knowledge file or makes a bad decision, revert the commit.
- **Collaboration.** Multiple humans can review agent activity through standard git tooling (diffs, blame, log).
- **Auditability.** The changelog.md is human-readable, but the git log is the authoritative record.

### 18.2 .gitignore

The ATeam repo ignores the bulky/ephemeral data:

```gitignore
# Git worktrees and bare repos (managed by git_manager, not version-controlled here)
repos/

# Docker build artifacts
*.tar
*.log

# Transient files
**/current_prompt.md
**/claude_session.json
**/exit_code

# OS artifacts
.DS_Store
```

Everything else is tracked: `config.toml`, all `role.md` and `knowledge.md` files, all reports in `work/` directories, `changelog.md`, `project_goals.md`, Dockerfiles.

### 18.3 Coordinator Commits

The coordinator makes git commits to the ATeam repo at these points:

| Event | Commit Message Pattern |
|---|---|
| After generating a report | `[testing] audit report 2026-02-26_2300` |
| After implementing a report | `[testing] implementation complete 2026-02-26_2300` |
| After updating knowledge.md | `[testing] knowledge update` |
| After coordinator decision | `[coordinator] auto-approved testing report` |
| After human approval/rejection | `[coordinator] human approved refactor report` |
| After config changes | `[config] updated schedule to night-only` |
| After human focus override | `[coordinator] human override: focus on security` |

This means `git log --oneline` on the ATeam repo reads like a narrative of all agent activity on the project.

### 18.4 Cross-Project Agents Directory

The top-level `my_projects/agents/` directory (containing default role.md and culture.md files) can also be a git repo or a git submodule shared across projects. This enables cross-project cultural knowledge to be versioned and shared.

---

## 20. Competitive Landscape and Alternatives

### 19.1 Summary

The agent orchestration space has exploded in 2025–2026. There are dozens of tools, but most fall into categories that don't quite match ATeam's specific niche: **scheduled, background, autonomous software quality improvement on an existing codebase with minimal human oversight.** Most tools are either general-purpose agent frameworks, interactive coding assistants, or PR-triggered review bots. ATeam's specific value proposition — a "night shift" of specialized agents that relentlessly improve code quality while humans sleep — is underserved.

### 19.2 Most Promising Alternatives

#### ComposioHQ/agent-orchestrator ⭐⭐⭐⭐ (Closest Match)

**What it is:** An open-source TypeScript platform that manages fleets of parallel coding agents. Each agent gets its own git worktree, branch, and PR. Supports Claude Code, Codex, and Aider as agent backends. Has a plugin architecture for swapping agent runtimes (tmux, Docker), trackers (GitHub, Linear), and notification channels.

**Overlap with ATeam:** Very high. Git worktree isolation, agent-agnostic design, parallel execution, CI failure auto-remediation, web dashboard, and reactive automation (CI fails → agent fixes it).

**What it lacks for our use case:**
- No scheduled/cron-based autonomous operation. It's reactive (responds to issues, CI failures, review comments) rather than proactive (scans for code quality improvements on a schedule).
- No specialized agent roles with persistent knowledge. Agents are generic — there's no concept of a "testing specialist" that accumulates project knowledge over time.
- No audit → approve → implement workflow. Agents go straight from issue to PR.
- No resource budgeting or token-aware throttling.
- No night/day schedule profiles.

**Ideas to integrate:**
- Adopt their **plugin architecture** pattern for swappable agent backends and notification channels. Their 8-slot plugin system (agent, runtime, workspace, tracker, scm, notifier, reviewer, merger) is well-designed.
- Borrow their **reactions system** — automated responses to GitHub events (CI failure → spawn agent, review comment → address it). This would be a great addition to ATeam's coordinator for handling events beyond our scheduled cycles.
- Their **web dashboard** is a nice-to-have for ATeam's Phase 4 or future enhancements.
- Consider using agent-orchestrator as a **runtime layer** under ATeam's coordinator. ATeam adds the scheduling, specialization, knowledge management, and budgeting on top.

#### OpenHands (formerly OpenDevin) ⭐⭐⭐⭐

**What it is:** An autonomous coding agent platform with 65K+ GitHub stars and $18.8M in funding. Supports full development loops: task decomposition, autonomous terminal execution, repository-wide edits, test-and-fix loops, and PR generation. Runs in Docker/Kubernetes sandboxes. Model-agnostic.

**Overlap with ATeam:** OpenHands covers many of the same tasks — refactoring, test generation, dependency upgrades, vulnerability remediation, documentation. Their "Refactor SDK" decomposes large tasks into per-agent subtasks using dependency tree analysis.

**What it lacks for our use case:**
- Primarily designed for **on-demand task execution** (assign an issue, agent solves it), not continuous background improvement.
- No concept of specialized agent roles with persistent project knowledge.
- No coordinator that decides what to work on next based on a schedule and project state.
- Heavier infrastructure footprint (Kubernetes-oriented, SaaS focus).

**Ideas to integrate:**
- Their **task decomposition strategy** (dependency-tree analysis to find leaf nodes, work bottom-up) is excellent for ATeam's refactor agent when tackling large-scale changes.
- Their insight about **90% automation / 10% human effort** with human oversight focused on strategy rather than debugging aligns with ATeam's philosophy.
- Their **Refactor SDK** concept (fixers, verifiers, progress tracking) could inform how ATeam's implement mode works for complex multi-file changes.
- Could potentially use OpenHands as an **alternative sub-agent runtime** alongside Claude Code for specific tasks.

#### AWS CLI Agent Orchestrator (CAO) ⭐⭐⭐

**What it is:** A lightweight Python orchestration system by AWS Labs that manages multiple AI agent sessions in tmux terminals. Features hierarchical orchestration with a supervisor agent, session isolation, and — critically — **scheduled flows using cron expressions**.

**Overlap with ATeam:** The flow/scheduling feature is exactly what ATeam needs. CAO supports cron-based automated agent execution, supervisor-worker hierarchies, and MCP-based inter-agent communication.

**What it lacks for our use case:**
- General-purpose agent orchestration, not focused on software quality.
- No specialized agent roles, knowledge persistence, or audit/implement workflow.
- Limited to tmux-based sessions (no Docker isolation).
- No resource budgeting or throttling.

**Ideas to integrate:**
- Their **flow system with cron expressions** is a clean implementation pattern. The YAML-based flow definition is more flexible than ATeam's current config.toml schedule profiles.
- Their **supervisor→worker pattern with context preservation** (supervisor provides only necessary context to each worker) aligns with ATeam's coordinator→sub-agent design.
- The **direct worker interaction** feature (users can steer individual worker agents mid-task) could be useful for ATeam's human override scenarios.

#### Qodo PR-Agent ⭐⭐⭐

**What it is:** An open-source AI-powered PR reviewer that runs on every pull request. Handles any PR size via compression, highly customizable via JSON prompts, generates descriptions, reviews, suggestions, and test generation.

**Overlap with ATeam:** PR-Agent covers code review, which is one of ATeam's sub-agent responsibilities. It's battle-tested and widely used.

**What it lacks for our use case:**
- PR-triggered only — doesn't proactively scan for improvements.
- Single-purpose (review), not a coordination framework.
- Doesn't make changes, just comments.

**Ideas to integrate:**
- Consider integrating PR-Agent as a **quality gate** in ATeam's workflow. After a sub-agent creates a PR, run PR-Agent on it as an automated reviewer before human approval.
- Their **PR compression strategy** for handling large diffs could inform how ATeam's coordinator summarizes agent changes for human review.

#### MetaGPT ⭐⭐

**What it is:** A multi-agent framework that simulates a software company with roles (PM, Architect, Engineer, QA). Uses SOPs (Standard Operating Procedures) to structure agent collaboration. Research-focused, strong on initial project scaffolding.

**Overlap with ATeam:** The role-based agent specialization and SOP concept. MetaGPT's pipeline (requirements → design → code → test → review) resembles ATeam's audit → implement workflow.

**What it lacks for our use case:**
- Designed for **greenfield project generation**, not ongoing maintenance of existing codebases.
- No continuous/scheduled operation, no git integration for existing repos.
- Roles are fixed to the "software company" metaphor, not configurable for maintenance tasks.
- API-level agent implementation (no Claude Code quality).

**Ideas to integrate:**
- The **SOP-as-prompt** concept is elegant. ATeam could formalize each agent's workflow as an explicit SOP in the role.md files, making the process reproducible and auditable.
- MetaGPT's **incremental mode** (`--inc` flag, works with existing repos) is worth watching as it matures.

#### Gas Town (steveyegge/gastown) ⭐⭐⭐⭐

**What it is:** A multi-agent workspace manager by Steve Yegge (8.8K GitHub stars, written in Go). Introduces a rich metaphor: a "Mayor" AI coordinator manages "Rigs" (projects), "Polecats" (worker agents), and "Hooks" (git worktree-based persistent storage). Work is tracked via "Beads" (a git-backed issue tracking system) and "Convoys" (bundles of work items). Supports Claude Code and Codex as agent runtimes. Includes a web dashboard, tmux integration, and formula-based repeatable workflows.

**How agents communicate:** Gas Town uses three communication mechanisms:

1. **Mailboxes (`gt mail`):** Each agent (Mayor, Polecats, Witness, etc.) has a mailbox backed by the Beads git store. Agents send messages via `gt mail send <addr> -s "Subject" -m "Message"`. The Mayor checks its inbox with `gt mail inbox` and reads specific messages with `gt mail read <id>`. This is asynchronous, persistent, and survives session restarts. For Claude Code agents, mail can be injected into the session context at startup via `gt mail check --inject`.

2. **Nudges (`gt nudge`):** Real-time messages sent directly into a running agent's tmux session. `gt nudge <target> "message"` injects text into the agent's terminal. This is the imperative "do this now" channel, used for coordination messages like "Process the merge queue" or "Check your hook." The Mayor is explicitly instructed to always use `gt nudge` rather than raw `tmux send-keys` to avoid dropped keystrokes.

3. **Hooks (filesystem state):** The primary work-assignment mechanism. When work is "slung" to an agent (`gt sling <bead-id> <rig>`), a hook file is attached to the agent's worktree. When the agent starts or resumes (via `gt prime`), it reads the hook and executes the attached work. This is the GUPP principle (Gas Town Universal Propulsion Principle): "If there is work on your hook, you MUST run it." No negotiation, no waiting for commands. The hook IS the assignment.

**How agents execute (Claude Code / Codex):** Gas Town runs agents as **long-lived tmux sessions**, not one-shot Docker containers:

1. **Spawning:** `gt sling <bead-id> <rig>` creates a fresh git worktree under `polecats/<AgentName>/`, creates a tmux session named `gt-<rig>-polecat-<Name>`, and launches the configured runtime (Claude Code by default, Codex as an option via `--agent codex`).

2. **Runtime injection:** For Claude Code, Gas Town uses `.claude/settings.json` hooks — Claude Code's native startup mechanism — to inject the role prompt, mail, and hook context when the session starts. For runtimes without native hooks (Codex), Gas Town sends a startup fallback sequence: `gt prime` (loads context), `gt mail check --inject` (injects pending messages), and `gt nudge deacon session-started`.

3. **Execution:** The agent (Claude Code) runs inside tmux, reads its hook via `bd show <bead-id>`, and executes the work described in the bead. There is no Docker isolation — agents run directly on the host in their git worktree. The Witness agent monitors their health via `gt peek <agent>`.

4. **Completion:** When done, the agent runs `gt done`, which pushes its branch to the remote and submits a merge request to the Refinery (the merge queue processor). The Refinery lands changes on main.

**Key difference from ATeam's model:** Gas Town runs agents in tmux sessions on the bare host. ATeam runs agents in Docker containers. Gas Town's Mayor is itself a Claude Code instance (an LLM-powered coordinator). ATeam's coordinator is a deterministic Python daemon. Gas Town uses Claude Code's interactive mode (long-lived sessions with `--resume`). ATeam uses one-shot `claude -p` invocations.

**What is Beads?** Beads is a separate project by Yegge (`github.com/steveyegge/beads`) — a git-backed issue tracking system stored as structured data files in the repository's `.beads/` directory. It functions as:

- **Issue tracker:** Each bead has a prefix + ID (e.g., `gt-abc12`), title, description, status, priority, assignee, and timestamps. Status flows through: `open` → `hooked` → `in_progress` → `done`.
- **Work assignment medium:** When you "sling" a bead, it gets attached to an agent's hook. The bead IS the task specification.
- **Formula engine:** Beads supports "formulas" (TOML-defined multi-step workflows) and "molecules" (instances of formulas). A formula defines steps with dependencies; a molecule tracks progress through those steps. If an agent crashes after step 3, a new agent picks up at step 4.

It's **more than a feature queue** — it's a full work-tracking system with crash recovery semantics. But in practice for Gas Town, beads are primarily used to describe units of work (features, bugs, tasks) that get assigned to agents. The formula system adds structured workflows on top. Compared to ATeam's approach of markdown reports, beads are more structured and machine-readable, which enables the crash-recovery and convoy-bundling features.

**Complexity comparison with ATeam:**

**(1) Implementation complexity:**

| Dimension | Gas Town | ATeam |
|---|---|---|
| Language | Go (~3600 commits, 93% Go, substantial codebase) | Python (estimated 2-3K lines for v1) |
| Coordinator | LLM-powered (Mayor = Claude Code instance) | Deterministic Python daemon |
| Agent runtime | tmux sessions on bare host | Docker containers with one-shot `claude -p` |
| Communication | 3 channels (mail, nudge, hooks) | 1 channel (filesystem I/O) |
| Work tracking | Full issue tracker (Beads, separate project) | Markdown files in git |
| Agent lifecycle | Complex (spawn, hook, prime, resume, done, handoff, witness monitoring) | Simple (start container, wait, read output) |
| Session management | Long-lived with resume, crash recovery, session rotation | Stateless one-shot invocations |
| Dependencies | Beads CLI, tmux, sqlite3, Git 2.25+ | Docker, Git, Python stdlib |

Gas Town is **significantly more complex to implement** — it's a full orchestration platform with its own issue tracker, inter-agent messaging, session management, and crash recovery. The Mayor being an LLM means the coordinator itself is non-deterministic and expensive. ATeam is deliberately simpler: a deterministic coordinator that launches isolated one-shot agents and reads their file outputs.

**(2) Usage complexity:**

| Dimension | Gas Town | ATeam |
|---|---|---|
| Setup | `gt install`, `gt rig add`, `gt crew add`, learn 20+ `gt` commands, understand Mayor/Polecat/Hook/Convoy/Bead/Witness/Refinery/Deacon roles | `ateam init`, `ateam run`, `ateam status`, `ateam approve/reject` |
| Daily use | Interactive — attach to Mayor, tell it what to do, monitor convoys | Autonomous — configure schedule, agents run overnight, review reports in morning |
| Mental model | 8+ agent roles, 3 communication channels, formula/molecule/bead/convoy abstractions | 7 specialist agents, 4 modes, markdown reports |
| Scaling | Designed for 20-30 parallel agents (but Yegge reports $100+/hour burn, maxing out Pro Max plans) | Conservative — 1-4 parallel agents, budget-capped |
| Failure recovery | Crash-resilient via hooks and beads (agent crashes, new one resumes) | Simple — container died? Coordinator logs it and retries next cycle |

Gas Town is **significantly more complex to use** for ATeam's goals. It's designed for Stage 7-8 developers running massive parallelization across large codebases. ATeam targets a simpler workflow: set it up, let it run overnight, review results. The interactive Mayor model requires ongoing human attention; ATeam's daemon model requires attention only at review time.

**Bottom line:** Gas Town is a more ambitious and general-purpose system. ATeam is a more focused and opinionated tool for a specific use case. If you want an interactive multi-agent factory, Gas Town is compelling. If you want a quiet night-shift crew that improves your code while you sleep, ATeam's simpler architecture is a better fit. The ideas worth borrowing from Gas Town are the structured work tracking (beads/convoys) and the crash-recovery semantics (hooks with resumable state), not the interactive tmux-based execution model.

#### LangGraph ⭐⭐

**What it is:** A Python-based framework from LangChain for managing multi-agent workflows using graph architectures. Organizes tasks as nodes in a directed graph with conditional edges, parallel execution, and persistent state. MIT-licensed, 120K+ GitHub stars (LangChain ecosystem). Features durable execution, human-in-the-loop checkpoints, and LangGraph Platform for deployment.

**Overlap with ATeam:** LangGraph provides the orchestration primitives (state management, conditional routing, parallel execution, human checkpoints) that ATeam's coordinator needs. Its emphasis on "context engineering" — controlling exactly what context each agent sees — aligns with ATeam's layered prompt system.

**What it lacks for our use case:**
- **General-purpose framework, not a solution.** LangGraph is infrastructure you build on, not a system you deploy. You'd still need to build all of ATeam's domain logic (scheduling, git management, Docker containers, specialized agent roles) on top of it.
- **Significant complexity overhead.** Multiple layers of abstraction (graphs, sub-graphs, state objects, decorators). Teams report debugging difficulties and API instability.
- **No native code execution sandbox.** LangGraph manages LLM interactions and state, but doesn't handle Docker containers, git worktrees, or file-based I/O.
- **Overkill for ATeam's coordinator.** ATeam's coordinator logic is simple rule-based decisions. LangGraph's graph-based orchestration adds complexity without proportional benefit for our use case.

**Ideas to integrate:**
- **Durable execution** is a genuinely useful concept. If ATeam's coordinator crashes mid-cycle, it should be able to resume from where it left off. LangGraph's approach of persisting state checkpoints could inform ATeam's coordinator state management.
- **Human-in-the-loop checkpoints** with "time travel" (roll back and take a different action) is a nice model for ATeam's approve/reject workflow.
- If ATeam's coordinator ever grows complex enough to need a graph-based workflow engine (unlikely for v1), LangGraph would be the right foundation.

#### Other Notable Tools

| Tool | What It Is | Why Not a Direct Fit |
|---|---|---|
| **CrewAI** | Role-based agent collaboration framework | General-purpose, not code-quality focused. No scheduling, no Docker isolation. |
| **Claude-flow (ruvnet)** | Agent orchestration for Claude with MCP | Over-engineered: 175+ MCP tools, neural routing, swarm intelligence. Too complex for our needs. |
| **ccswarm** | Rust-native multi-agent orchestration with Claude Code | Early stage, orchestration loop not fully implemented. Good architectural ideas. |
| **Emdash** | Desktop app for parallel coding agents (YC W26) | Interactive/GUI-focused, not background/scheduled. Supports 21 CLI agents. |
| **runCLAUDErun** | macOS scheduler for Claude Code tasks | Simple cron-like scheduler — exactly one piece of ATeam. Too minimal alone. |
| **OpenAgentsControl** | Plan-first development with approval gates | Pattern-matching focus (teach your patterns, agents follow them). Interesting for ATeam's "configure" mode. |
| **AutoGen (Microsoft)** | Multi-agent framework with human-in-the-loop | Enterprise-grade but too general. Heavy setup for our specific use case. |

### 19.3 Conclusion: Build or Adopt?

**Recommendation: Build ATeam, but borrow heavily from Gas Town, ComposioHQ/agent-orchestrator, and OpenHands patterns.**

No existing tool combines all of ATeam's core requirements:
1. Scheduled, autonomous background operation (night shift).
2. Specialized agent roles with persistent project knowledge.
3. Audit → approve → implement workflow with human checkpoints.
4. Claude Code as the sub-agent runtime (leveraging its superior coding ability).
5. Resource budgeting and throttling.
6. Git-versioned configuration and decision trail.
7. Cross-project tech-stack culture and knowledge sharing.

Gas Town and agent-orchestrator come closest but are both reactive/interactive rather than proactive (schedule-driven). The best approach is to build ATeam's coordinator and scheduling layer while adopting proven patterns:

- **Beads/convoy work tracking** and **hook lifecycle** from Gas Town.
- **Plugin architecture** and **CI reaction system** from agent-orchestrator.
- **Task decomposition** from OpenHands.
- **SOP-as-prompt** from MetaGPT.
- **Durable execution checkpoints** from LangGraph.
- **Cron flow definitions** from AWS CAO.
- **PR-Agent as a quality gate** for agent-generated changes.

### 19.4 Future: Feature Agents

Several of these tools (OpenHands, agent-orchestrator, GitHub Copilot agent) already support the pattern of assigning feature work to agents. When ATeam adds feature agents:

- **Small features** would go through a feature queue managed by the coordinator, similar to how agent-orchestrator spawns agents per GitHub issue.
- **Each feature agent** would be a one-off — created for a specific task, given a temporary worktree, and cleaned up after the PR is merged or rejected.
- **The coordinator** would summarize progress using the same changelog pattern, with human approval gates for merging.
- **Knowledge doesn't persist** for feature agents (they're disposable), but they benefit from the project's existing knowledge files and the testing agent validates their output.

This is essentially what agent-orchestrator already does, so we could potentially integrate it as a subcomponent or adopt its patterns when the time comes.

---

## 21. Future Enhancements

- **Feature agents** as described in §20.4 — a feature queue for small tasks, one-off agents per feature, leveraging the coordinator for progress tracking and the testing agent for validation.
- **Web dashboard** for monitoring agent activity, reviewing reports, approving changes.
- **Slack/Discord integration** for notifications and commands.
- **Cross-project knowledge sharing** via `culture.md` files.
- **Cost optimization** by routing simpler tasks (docs, audit) to cheaper/faster models.
- **PR integration** where agents create pull requests for standard code review.
- **Learning from feedback** — when a human rejects agent work, feed that into knowledge.md.
- **Granular token tracking** if Claude Code adds a `--usage-report` flag or file output in the future.
- **Coordinator LLM mode** — optional LLM-powered coordinator for complex multi-agent reasoning when rule-based logic isn't sufficient.
- **`claude --resume` chaining** — for very large implementation tasks, chain multiple Claude Code sessions using `--resume` to continue where the previous session left off.