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

Each MCP tool is a thin wrapper around the `ateam` CLI, which reads/writes the project's `ateam.db`. This ensures the coordinator and developers always share the same state (see §10.2).

```
subagent_run_audit(agent, project)         → ateam run --agent X --mode audit
subagent_run_implement(agent, project, report) → ateam run --agent X --mode implement
subagent_commit_and_merge(agent, project)  → git: commit changes, run tests, merge if green
format_report(report_path, model?)         → summarize/reformat report (delegate to Haiku)
get_pending_reports(project)               → list reports awaiting review
update_knowledge(agent, project, summary)  → append to knowledge.md
get_budget_status(project)                 → query ateam.db operations for cost data
get_schedule_profile()                     → "night"/"day", allowed agents, max parallel
create_worktree(project, agent)            → create git worktree
cleanup_worktree(project, agent)           → remove worktree
get_recent_commits(project, since)         → list recent commits
get_agent_status(project, agent?)          → query ateam.db agents table
pause_agent(project, agent?)               → ateam pause --agent X
resume_agent(project, agent?)              → ateam resume --agent X
```

Claude Code also uses its built-in tools natively: reading reports (Read), inspecting code (Grep, Glob), writing changelog entries (Write), running test commands (Bash). The MCP server only handles the infrastructure operations that need Docker, git worktrees, or state management.

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

### 10.1 CLI Context: Directory-Aware Commands

The `ateam` CLI infers project and agent context from the current working directory:

```
my_projects/                         # PROJ_ROOT
  projectx/                          # project dir (has config.toml)
    config.toml
    ateam.db                         # SQLite state database
    testing/                         # agent dir
      role.md
      knowledge.md
      work/
    refactor/
      ...
  projecty/
    config.toml
    ateam.db
    ...
```

**Resolution rules:**

1. **Inside `projectx/testing/`** → project=projectx, agent=testing. Commands apply to the testing agent only.
2. **Inside `projectx/`** → project=projectx, no agent. Commands apply to all agents / the coordinator.
3. **Anywhere else** → requires `-d` flag: `ateam -d ~/my_projects/projectx pause --agent testing`.

```bash
# These are all equivalent:
cd ~/my_projects/projectx/testing && ateam pause
cd ~/my_projects/projectx && ateam pause --agent testing
ateam -d ~/my_projects/projectx pause --agent testing

# These are all equivalent (project-wide):
cd ~/my_projects/projectx && ateam pause
ateam -d ~/my_projects/projectx pause
```

The CLI walks up from `$CWD` looking for `config.toml` (identifies project root). If the current directory basename matches a known agent name (from `config.toml`'s `[agents] enabled`), that agent is used as context.

**The coordinator uses the same CLI.** MCP tools like `subagent_run_audit` are thin wrappers that invoke the `ateam` CLI commands. The CLI reads and writes `ateam.db`. This means developers and the coordinator always have the same view of the system — there's no separate coordinator state, no lockfiles, no divergent data paths.

### 10.2 State Database: SQLite

Each project has a single `ateam.db` in its root directory. This is the sole source of truth for all runtime state. Both the autonomous coordinator (via MCP → CLI) and the developer (directly via CLI) read and write the same database.

```sql
CREATE TABLE agents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_dir     TEXT NOT NULL,         -- absolute path to project root
    agent_name      TEXT NOT NULL,         -- 'testing', 'refactor', 'security', etc.
    docker_instance TEXT,                  -- container ID, NULL if not running
    time_started    TEXT,                  -- ISO timestamp, NULL if not running
    time_ended      TEXT,                  -- ISO timestamp of last completion
    git_commit_start TEXT,                 -- HEAD at agent start
    git_commit_end  TEXT,                  -- HEAD at agent end (after merge, if any)
    reason          TEXT NOT NULL DEFAULT 'coordinator',
                                          -- 'coordinator' : autonomous scheduler
                                          -- 'manual'      : human via ateam run/shell
    status          TEXT NOT NULL DEFAULT 'idle',
                                          -- 'idle'        : available for scheduling
                                          -- 'running'     : autonomous execution
                                          -- 'interactive' : human shell session
                                          -- 'suspended'   : manually paused
                                          -- 'canceled'    : run was aborted
                                          -- 'stopped'     : completed normally
                                          -- 'error'       : last run failed
    UNIQUE(project_dir, agent_name)
);

CREATE TABLE operations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_dir     TEXT NOT NULL,         -- absolute path to project root
    agent_name      TEXT,                  -- NULL for project-wide operations
    docker_instance TEXT,                  -- container ID if applicable, NULL otherwise
    timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
    operation       TEXT NOT NULL,         -- 'start', 'stop', 'suspend', 'resume',
                                          -- 'kill', 'shell', 'error', 'complete'
    notes           TEXT                   -- free-form: error messages, kill reasons,
                                          -- report paths, cost estimates, etc.
);

CREATE TABLE reports (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_dir     TEXT NOT NULL,
    agent_name      TEXT NOT NULL,         -- which agent produced this report
    timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
    report_path     TEXT NOT NULL,         -- relative path: testing/work/2026-02-26_report.md
    git_commit_audited TEXT,               -- commit hash the agent analyzed
    decision        TEXT NOT NULL DEFAULT 'pending',
                                          -- 'pending'     : awaiting coordinator review
                                          -- 'ignore'      : no action needed
                                          -- 'proceed'     : approved for implementation
                                          -- 'ask'         : needs human review
                                          -- 'implemented' : changes applied + merged
                                          -- 'deferred'    : deprioritized for now
                                          -- 'blocked'     : waiting on dependency
    git_commit_impl TEXT,                  -- commit hash after implementation, NULL if not impl
    notes           TEXT                   -- coordinator's reasoning: why this decision,
                                          -- what was prioritized instead, risk assessment
);
```

**Design notes:**

The `agents` table holds current state — one row per enabled agent, upserted on first use. The `operations` table is an append-only audit log of every state transition. The `reports` table tracks the coordinator's analysis and decisions on agent-produced reports. Together they answer "what's happening now?" (agents), "what happened?" (operations), and "what did the coordinator decide?" (reports).

`project_dir` is stored as an absolute path. Since each project has its own `ateam.db`, `project_dir` is technically redundant in most queries — but it makes the schema self-describing and enables future multi-project dashboards that aggregate across databases.

The `reason` field distinguishes coordinator-initiated runs from manual ones. When the scheduler starts a testing audit, `reason='coordinator'`. When a developer runs `ateam shell`, `reason='manual'`. This lets you filter history: "show me only coordinator decisions" vs "show me my interactive sessions."

Git commit tracking (`git_commit_start`, `git_commit_end`) records the codebase state the agent worked against. This enables questions like "which commit did the security agent last audit?" and "did the refactor agent's changes include recent commits?"

`operations.docker_instance` captures the container ID for each state transition. This is critical for post-mortem debugging: if an agent failed, you can correlate the operation with `docker logs {container_id}` (if the container still exists) or at minimum know which container execution you're investigating. It's nullable because some operations (suspend, resume) don't involve a container.

**The `reports` table** is the coordinator's decision log in structured form. Every time an agent produces a report (audit findings, implementation results), the coordinator inserts a row with its decision and reasoning. The `decision` field captures the outcome:

- `pending` — report received, not yet reviewed by coordinator
- `ignore` — coordinator reviewed, no action needed (e.g., clean audit)
- `proceed` — approved for implementation, implementation agent will be scheduled
- `ask` — coordinator is uncertain, flagged for human review
- `implemented` — implementation completed, `git_commit_impl` records the merge commit
- `deferred` — valid findings but lower priority than current work; will revisit
- `blocked` — depends on something else (another agent's work, human input, external dependency)

The `notes` field contains the coordinator's reasoning — why it chose this decision, what it prioritized instead, risk assessment. This makes the coordinator's "thinking" inspectable and debuggable. The `ateam reports` command (§11.5) queries this table to show the coordinator's pipeline.

**Why SQLite:**
- Single file, zero setup, no daemon, no lockfiles.
- WAL mode enables concurrent readers + single writer — perfect for ATeam's access pattern (one writer at a time: either CLI or coordinator, with advisory locking via SQLite's built-in mechanism).
- Trivially inspectable: `sqlite3 ateam.db "SELECT agent_name, status FROM agents"`.
- The `ateam db` command opens a sqlite3 shell for ad-hoc queries.
- Schema is recreatable from code, so `ateam.db` can be `.gitignore`d (transient state, not config).

**MCP tools are CLI wrappers.** The coordinator's MCP server implements tools by calling the `ateam` CLI:

```
subagent_run_audit("testing", "myapp")
  → internally: ateam -d /path/to/myapp run --agent testing --mode audit
  → CLI updates ateam.db: INSERT INTO operations, UPDATE agents SET status='running'
  → CLI spawns Docker container
  → on completion: UPDATE agents SET status='stopped', time_ended=now
  → INSERT INTO operations (operation='complete', notes='exit_code=0, report=...')
```

This means there's zero divergence between what a developer does manually and what the coordinator does autonomously — same CLI, same database, same state transitions.

### 10.3 Container Naming Convention

Deterministic container names: `ateam-{project}-{agent}`.

```
ateam-projectx-testing
ateam-projectx-refactor
ateam-projectx-security
...
```

The CLI checks `agents.status` and `agents.docker_instance` before launching. If the agent is `running` or `interactive`, the launch fails with a message describing the current state. Single-instance per agent for v1; parallelism (e.g., suffixed names) can be added later.

### 10.4 Pause / Resume with Granularity

Pausing is a status update in SQLite plus an operations log entry:

```bash
# ── Agent-level (from agent directory) ───────────────────────────

cd projectx/testing
ateam pause
# → If status='running':
#   "Testing agent is currently running. Kill it? [y/N]"
#     y → docker kill, UPDATE agents SET status='suspended'
#     n → UPDATE agents SET status='suspended' (takes effect after current run)
# → If status='idle':
#   UPDATE agents SET status='suspended'
# → INSERT INTO operations (agent_name='testing', operation='suspend')

ateam resume
# → UPDATE agents SET status='idle' WHERE agent_name='testing'
# → INSERT INTO operations (agent_name='testing', operation='resume')

# ── Project-level (from project directory) ───────────────────────

cd projectx
ateam pause
# → Suspends ALL agents + the coordinator
# → UPDATE agents SET status='suspended' WHERE status IN ('idle','error')
# → For each running agent: offers kill prompt
# → INSERT INTO operations (agent_name=NULL, operation='suspend',
#     notes='project-wide pause')

ateam resume
# → UPDATE agents SET status='idle' WHERE status='suspended'
# → INSERT INTO operations (agent_name=NULL, operation='resume')

# ── From anywhere ────────────────────────────────────────────────

ateam -d ~/my_projects/projectx pause --agent testing --agent security
```

**The scheduler's decision loop** reads from SQLite each cycle:

```
1. Is there a project-wide suspend? (operations log or coordinator key)
   → skip entire cycle
2. For each due agent:
   a. status = 'suspended'? → skip
   b. status = 'interactive'? → skip (human working)
   c. status = 'running'?
      - check Docker container health + time_started
      - if container dead but status='running' → stale, mark 'error'
      - if running > timeout → kill, mark 'error'
      - otherwise → still going, skip
   d. status IN ('idle', 'error', 'stopped')? → eligible to run
3. Check budget limits
4. Launch eligible agents up to max_parallel
5. UPDATE agents SET status='running', time_started=now, docker_instance=...,
     git_commit_start=HEAD, reason='coordinator'
6. INSERT INTO operations (operation='start', notes='coordinator night cycle')
```

### 10.5 Interactive Agent Sessions

```bash
# Launch interactive session as the testing agent
cd projectx/testing
ateam shell

# Or explicitly:
ateam -d ~/my_projects/projectx shell --agent testing

# This:
# 1. Checks ateam.db: is testing idle/suspended/stopped/error?
#    If running → error "agent already running"
# 2. UPDATE agents SET status='interactive', time_started=now,
#      reason='manual', git_commit_start=HEAD
# 3. INSERT INTO operations (operation='shell', notes='interactive session')
# 4. Creates worktree (if not present)
# 5. Assembles the prompt (same layers as autonomous mode)
# 6. Starts Docker container (ateam-projectx-testing)
# 7. Drops into interactive Claude Code:
#      claude --dangerously-skip-permissions
# 8. On exit:
#      UPDATE agents SET status='stopped', time_ended=now, git_commit_end=HEAD
#      INSERT INTO operations (operation='stop', notes='interactive session ended')
# 9. Container stops, worktree preserved

# With coordinator MCP tools available:
ateam shell --with-mcp
# → Also attaches the ATeam MCP server to the Claude Code session
# → Human can use coordinator-level commands:
#   "Format my findings into a report"  → format_report(...)
#   "Commit and merge my changes"       → subagent_commit_and_merge(...)
#   "Update the knowledge file"         → update_knowledge(...)
#   "Check budget"                      → get_budget_status(...)
```

The scheduler sees `status='interactive'` and skips this agent — no ambiguity, no heartbeat heuristics needed. The status is set atomically in SQLite before the container starts.

**Distinguishing stale from active:** For `status='running'` (autonomous mode), the scheduler checks:
1. Is the Docker container still alive? (`docker inspect`)
2. Has `time_started` exceeded the configured timeout?
3. Is stream.jsonl still being written to?

If the container is dead but status='running', it's stale — the CLI crashed or the container was killed externally. The scheduler marks it `error` and logs an operations entry.

### 10.6 CLI Command Implementation Details

Every CLI command is a composition of three data sources: **SQLite** (ateam.db), **git** (worktrees, refs, HEAD), and **Docker** (containers, inspect). This section documents exactly what each command reads and writes.

#### `ateam status`

Shows the combined state of all agents (or a single agent if run from an agent directory), with commit freshness, elapsed time, and live activity preview.

**Project-level output** (`cd projectx && ateam status`):

```
ATeam: projectx                          main @ a3f8c21 (2 min ago)
Coordinator: running                     Last cycle: 3 min ago

AGENT        STATUS       ELAPSED   REASON        LAST SEEN     BEHIND
testing      running      12m 34s   coordinator   a3f8c21       0 commits
refactor     idle         —         —             b1e7d09       3 commits
security     suspended    —         —             8ca2f31       12 commits
performance  stopped      —         —             a3f8c21       0 commits
deps         idle         —         —             f42a1bc       7 commits
docs         error        —         —             91d3e0a       5 commits

Recent Operations:
  14:23  testing    start    coordinator night cycle
  14:12  refactor   complete exit=0 report=refactor/work/...
  14:01  docs       error    timeout after 30m

Pending Reports:
  refactor  2026-02-26 14:12  → decision: proceed (approved, impl queued)
  security  2026-02-25 23:45  → decision: ask (2 high-severity findings)
```

**Agent-level output** (`cd projectx/testing && ateam status`):

```
Agent: testing @ projectx                STATUS: running (12m 34s)
Reason: coordinator                      Container: ateam-projectx-testing (d8a3f...)
Commit: a3f8c21 (0 behind main)         Started: 2026-02-26 14:23:01

Live Activity (stream-json tail):
  [14:34:12] Running test suite: npm test
  [14:34:45] 142/186 tests passing, 3 failing, analyzing failures...
  [14:35:02] Reading src/api/handler.test.ts for context

Recent Operations:
  14:23  start     coordinator night cycle  container=d8a3f...
  12:01  complete  exit=0 report=testing/work/2026-02-26_1200_report.md
  11:30  start     coordinator day check    container=c7b2e...

Reports:
  2026-02-26 12:01  → ignore (all tests passing, no gaps found)
  2026-02-25 23:58  → implemented @ commit e4f2a1b (added 12 test cases)
```

**Implementation:**

```
READ  sqlite  SELECT agent_name, status, docker_instance, time_started,
                     time_ended, reason, git_commit_start, git_commit_end
              FROM agents
              → if scoped to single agent: WHERE agent_name={agent}

READ  git     git -C {bare_repo} rev-parse main → current_head
              For each agent:
                git -C {bare_repo} rev-list --count {git_commit_end}..{current_head}
                → "N commits behind"
                (uses git_commit_end if stopped, git_commit_start if running)
              git -C {bare_repo} log -1 --format='%cr' → "2 min ago" for header

READ  docker  For each agent where docker_instance IS NOT NULL:
                docker inspect {docker_instance}
                → alive/dead, actual uptime, exit code
                → staleness check: if dead but status='running', show WARNING

READ  file    If agent is running and scoped to single agent (agent-level view):
                tail -n 20 {agent}/work/{latest}_stream.jsonl
                → parse stream-json events, extract last few assistant messages
                → display as "Live Activity" with timestamps

READ  sqlite  SELECT timestamp, operation, agent_name, docker_instance, notes
                FROM operations
                WHERE (agent_name={agent} OR {agent} IS NULL)
                ORDER BY timestamp DESC LIMIT 10
              → "Recent Operations" section

READ  sqlite  SELECT agent_name, timestamp, decision, notes, report_path
                FROM reports
                WHERE (agent_name={agent} OR {agent} IS NULL)
                  AND decision != 'ignore'
                ORDER BY timestamp DESC LIMIT 5
              → "Pending/Recent Reports" section (skip 'ignore' to reduce noise,
                show with --all flag)

WRITE         (none — status is read-only)
```

#### `ateam reports [--agent NAME] [--decision DECISION] [--all]`

Shows the coordinator's report pipeline: what it received, what it decided, and why.

```
# From project dir — shows all agent reports
cd projectx && ateam reports

Reports:
  ID  AGENT       DATE        DECISION      COMMIT      NOTES
  14  testing     02-26 12:01 ignore        —           all tests passing
  13  refactor    02-26 11:45 proceed       —           3 dead code removals, low risk
  12  security    02-25 23:45 ask           —           2 high-severity: SQL injection in /api/users
  11  testing     02-25 23:30 implemented   e4f2a1b     added 12 test cases for auth module
  10  performance 02-25 23:00 deferred      —           DB index suggestions, waiting for schema migration
   9  refactor    02-25 22:15 implemented   c8d3f2a     extracted shared validation logic

# Filter by decision
ateam reports --decision ask
ateam reports --decision deferred
ateam reports --decision pending

# From agent dir — shows only that agent's reports
cd projectx/security && ateam reports

# Show full notes (coordinator reasoning)
ateam reports --verbose
  ID 12  security  2026-02-25 23:45  decision: ask
  Report: security/work/2026-02-25_2345_report.md
  Audited: commit 8ca2f31
  Notes: "Found 2 high-severity SQL injection vectors in /api/users and
    /api/admin. These require human review because they involve the
    authentication layer. Also found 4 low-severity issues (missing
    rate limits) which I would normally auto-approve, but bundling
    with the high-severity findings for a single review pass."
```

**Implementation:**

```
READ  sqlite  SELECT id, agent_name, timestamp, report_path,
                     git_commit_audited, decision, git_commit_impl, notes
              FROM reports
              WHERE (agent_name={agent} OR {agent} IS NULL)
                AND (decision={filter} OR {filter} IS NULL)
              ORDER BY timestamp DESC
              LIMIT 20

READ  file    If --verbose: check that report_path exists on disk
              (warn if report file has been cleaned up)
```

#### `ateam changelog [--limit N]`

Parses the project's `changelog.md` and shows recent coordinator decisions in a structured view. The changelog is a markdown file maintained by the coordinator (written by Claude Code via its Write tool). This command parses it for quick terminal viewing.

```
cd projectx && ateam changelog

Changelog (last 10 entries):

2026-02-26 14:12  IMPLEMENTED  refactor
  Removed 3 dead utility functions (utils/legacy.ts, utils/compat.ts).
  No test failures. Merged as commit b1e7d09.

2026-02-26 12:01  REVIEWED     testing
  All tests passing. No coverage gaps found. No action needed.

2026-02-25 23:58  IMPLEMENTED  testing
  Added 12 test cases for auth module based on security audit findings.
  Coverage increased from 72% to 81%. Merged as commit e4f2a1b.

2026-02-25 23:45  FLAGGED      security
  2 high-severity SQL injection findings. Queued for human review.
  See: security/work/2026-02-25_2345_report.md

2026-02-25 23:00  DEFERRED     performance
  DB index suggestions valid but depend on upcoming schema migration.
  Will re-run after migration lands.
```

**Implementation:**

```
READ  file    cat {project_dir}/changelog.md
              → parse markdown: extract date, action, agent, description
              → the changelog format is structured enough for simple parsing:
                entries are separated by "## YYYY-MM-DD" headers with
                subsections per action

              Alternatively, if changelog.md is too free-form to parse reliably:
READ  sqlite  SELECT r.timestamp, r.agent_name, r.decision, r.notes,
                     r.git_commit_impl
              FROM reports r
              ORDER BY r.timestamp DESC LIMIT {limit}
              → this gives the same information from the structured reports table,
                and can serve as a fallback or primary source

WRITE         (none — read-only)
```

The `reports` table and `changelog.md` contain overlapping information by design. The reports table is the structured, queryable source. The changelog is the human-readable narrative maintained by the coordinator for git history. `ateam changelog` tries the file first; `ateam reports` always uses the database.

#### `ateam run [--agent NAME] [--mode MODE]`

Starts an agent run (audit by default). Used by both humans and the coordinator (via MCP).

```
READ  sqlite  SELECT status, docker_instance FROM agents
                WHERE agent_name={agent}
              → reject if status IN ('running', 'interactive'):
                "Agent already active (status={status}, container={docker_instance})"
READ  docker  docker ps --filter name=ateam-{project}-{agent} --format '{{.ID}}'
              → safety check: reject if container exists even if sqlite says idle
                (stale state recovery — update sqlite to match reality)
READ  git     git -C {bare_repo} rev-parse main
              → capture HEAD as git_commit_start
WRITE git     git worktree add {worktree_path} main  (if not already present)
WRITE docker  docker run --name ateam-{project}-{agent} \
                --cpus=2 --memory=4g --pids-limit=256 \
                -v {worktree_path}:/workspace \
                -v {prompt_path}:/agent-data \
                -v {output_path}:/output \
                {image} claude -p {prompt} --dangerously-skip-permissions \
                  --output-format stream-json
              → captures container ID from docker run output
WRITE sqlite  UPDATE agents SET
                status='running',
                docker_instance={container_id},
                time_started=datetime('now'),
                time_ended=NULL,
                git_commit_start={head},
                reason={reason}   -- 'coordinator' or 'manual'
              WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations
                (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {container_id}, 'start',
                'mode={mode} reason={reason} commit={head}')

# ── On completion (monitored by CLI or MCP wrapper) ──────────────

READ  docker  docker inspect {container_id} --format '{{.State.ExitCode}}'
READ  git     git -C {worktree_path} rev-parse HEAD → git_commit_end
READ  file    Parse stream.jsonl for report path, token counts, cost estimate
WRITE sqlite  UPDATE agents SET
                status='stopped',
                time_ended=datetime('now'),
                git_commit_end={head}
              WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations
                (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {container_id}, 'complete',
                'exit={code} report={path} tokens_in={n} tokens_out={n}')
WRITE sqlite  INSERT INTO reports
                (agent_name, report_path, git_commit_audited, decision)
              VALUES ({agent}, {report_path}, {git_commit_start}, 'pending')
              → coordinator will UPDATE decision + notes later during triage
```

#### `ateam shell [--with-mcp]`

Starts an interactive session. Same Docker environment as autonomous, but interactive Claude Code.

```
READ  sqlite  SELECT status, docker_instance FROM agents WHERE agent_name={agent}
              → reject if status IN ('running', 'interactive')
READ  docker  docker ps --filter name=ateam-{project}-{agent}
              → safety check (same as ateam run)
READ  git     git -C {bare_repo} rev-parse main → git_commit_start
WRITE git     git worktree add {worktree_path} main  (if needed)
WRITE sqlite  UPDATE agents SET
                status='interactive',
                docker_instance={container_id},
                time_started=datetime('now'),
                git_commit_start={head},
                reason='manual'
              WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {container_id}, 'shell', 'interactive session started')
WRITE docker  docker run -it --name ateam-{project}-{agent} \
                ... (same volumes as ateam run) ...
                {image} claude --dangerously-skip-permissions
              → blocks until user exits Claude Code

# ── On exit (after Claude Code /exit or Ctrl-D) ──────────────────

READ  git     git -C {worktree_path} rev-parse HEAD → git_commit_end
READ  docker  docker inspect {container_id} --format '{{.State.ExitCode}}'
WRITE sqlite  UPDATE agents SET
                status='stopped',
                time_ended=datetime('now'),
                git_commit_end={head}
              WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {container_id}, 'stop',
                'interactive session ended exit_code={code}')
```

#### `ateam pause`

Suspends an agent (from agent dir) or all agents (from project dir).

```
# ── From agent directory ─────────────────────────────────────────

READ  sqlite  SELECT status, docker_instance FROM agents WHERE agent_name={agent}
              → if status='running':
READ  docker    docker inspect {docker_instance} --format '{{.State.Status}}'
                → prompt: "Agent is running in container {id}. Kill it? [y/N]"
                → if yes:
WRITE docker      docker kill {docker_instance}
WRITE sqlite      UPDATE agents SET status='suspended', docker_instance=NULL,
                    time_ended=datetime('now')
                → if no:
WRITE sqlite      UPDATE agents SET status='suspended'
                  (takes effect after current run completes)
              → if status='interactive':
                → prompt: "Agent is in interactive session. Kill it? [y/N]"
                (same kill flow as above)
              → if status IN ('idle', 'stopped', 'error'):
WRITE sqlite    UPDATE agents SET status='suspended' WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {docker_instance}, 'suspend', 'manual pause')

# ── From project directory ───────────────────────────────────────

READ  sqlite  SELECT agent_name, status, docker_instance FROM agents
              → for each agent with status IN ('running', 'interactive'):
                prompt to kill (same as above)
WRITE sqlite  UPDATE agents SET status='suspended'
                WHERE status IN ('idle', 'stopped', 'error')
WRITE sqlite  INSERT INTO operations (agent_name=NULL, operation='suspend',
                notes='project-wide pause')
```

#### `ateam resume`

```
# ── From agent directory ─────────────────────────────────────────

READ  sqlite  SELECT status FROM agents WHERE agent_name={agent}
              → only resumes if status='suspended'
WRITE sqlite  UPDATE agents SET status='idle' WHERE agent_name={agent}
                AND status='suspended'
WRITE sqlite  INSERT INTO operations (agent_name, operation) VALUES ({agent}, 'resume')

# ── From project directory ───────────────────────────────────────

WRITE sqlite  UPDATE agents SET status='idle' WHERE status='suspended'
WRITE sqlite  INSERT INTO operations (agent_name=NULL, operation='resume',
                notes='project-wide resume')
```

#### `ateam kill [--agent NAME]`

Force-kills a running or interactive agent.

```
READ  sqlite  SELECT status, docker_instance FROM agents WHERE agent_name={agent}
              → reject if status NOT IN ('running', 'interactive'):
                "Agent is not active (status={status})"
READ  docker  docker inspect {docker_instance} --format '{{.State.Status}}'
              → if container already dead, skip docker kill
WRITE docker  docker kill {docker_instance}
WRITE docker  docker rm {docker_instance}  (cleanup)
READ  git     git -C {worktree_path} rev-parse HEAD → git_commit_end
WRITE sqlite  UPDATE agents SET
                status='canceled',
                docker_instance=NULL,
                time_ended=datetime('now'),
                git_commit_end={head}
              WHERE agent_name={agent}
WRITE sqlite  INSERT INTO operations (agent_name, docker_instance, operation, notes)
              VALUES ({agent}, {docker_instance}, 'kill',
                'manual kill of container {docker_instance}')
```

#### `ateam retry [--agent NAME]`

Kill + cleanup worktree + fresh run.

```
(same as ateam kill, then:)
WRITE git     git worktree remove {worktree_path} --force
WRITE git     git worktree add {worktree_path} main  (fresh checkout)
(then same as ateam run)
```

#### `ateam logs [--agent NAME]`

```
READ  sqlite  SELECT docker_instance, status FROM agents WHERE agent_name={agent}
              → determines: show live or historical logs
              → if status IN ('running', 'interactive'):
READ  docker    docker logs --follow {docker_instance}
              → also/alternatively:
READ  file    tail -f {agent}/work/{latest}_stream.jsonl
              → with --last flag:
READ  sqlite    SELECT notes FROM operations WHERE agent_name={agent}
                  AND operation='complete' ORDER BY timestamp DESC LIMIT 1
                → extracts stream_log_path from notes
READ  file      cat {stream_log_path}
              → with --stderr:
READ  docker    docker logs --follow {docker_instance} 2>&1 1>/dev/null
```

#### `ateam diff [--agent NAME]`

```
READ  sqlite  SELECT git_commit_start FROM agents WHERE agent_name={agent}
              → the base commit this agent started from
READ  git     git -C {worktree_path} diff {git_commit_start}..HEAD
              → shows all changes the agent has made
              → if worktree doesn't exist:
READ  git       git -C {bare_repo} diff {git_commit_start}..{git_commit_end}
                → uses stored commits from agents table for historical diff
```

#### `ateam history [--agent NAME]`

```
READ  sqlite  SELECT timestamp, operation, agent_name, notes FROM operations
                WHERE (agent_name={agent} OR {agent} IS NULL)
                ORDER BY timestamp DESC
                LIMIT 50
              → displays formatted operation log
              → enriches with duration: computes time between 'start' and
                'stop'/'complete'/'kill' operations for the same agent
```

#### `ateam doctor [--fix]`

Cross-references all three data sources to find inconsistencies.

```
# ── Check: SQLite says running, but Docker disagrees ─────────────

READ  sqlite  SELECT agent_name, docker_instance FROM agents
                WHERE status IN ('running', 'interactive')
READ  docker  For each: docker inspect {docker_instance}
              → if container doesn't exist or is stopped:
                WARN "Agent {agent} shows status={status} but container is dead"
              → with --fix:
WRITE sqlite    UPDATE agents SET status='error',
                  docker_instance=NULL, time_ended=datetime('now')
WRITE sqlite    INSERT INTO operations (agent_name, docker_instance, operation, notes)
                VALUES ({agent}, {docker_instance}, 'error',
                  'doctor --fix: stale container state')

# ── Check: Docker containers exist that SQLite doesn't know about ─

READ  docker  docker ps --filter name=ateam-{project} --format '{{.Names}}'
READ  sqlite  SELECT agent_name, docker_instance FROM agents
              → for each container not in sqlite:
                WARN "Orphan container: {container_name}"
              → with --fix:
WRITE docker    docker kill {container}; docker rm {container}

# ── Check: Worktrees exist without matching agent state ──────────

READ  git     git worktree list
READ  sqlite  SELECT agent_name, status FROM agents
              → for each worktree not matching an active/recent agent:
                WARN "Stale worktree: {path}"
              → with --fix:
WRITE git       git worktree remove {path}

# ── Check: SQLite integrity ──────────────────────────────────────

READ  sqlite  PRAGMA integrity_check
              → with --fix:
WRITE sqlite    VACUUM

# ── Check: Dependencies ─────────────────────────────────────────

READ  shell   docker --version, git --version, claude --version
              → WARN if any missing or below minimum version
```

#### `ateam cleanup`

```
READ  sqlite  SELECT agent_name, status FROM agents
READ  docker  docker ps -a --filter name=ateam-{project} --filter status=exited
WRITE docker  docker rm {each exited container}
READ  git     git worktree list
              → for each worktree where agent status NOT IN ('running','interactive'):
WRITE git       git worktree remove {path}
READ  file    find {agent}/work/ -name "*_stream.jsonl" -mtime +30
WRITE file    rm {each old log file}
WRITE sqlite  DELETE FROM operations WHERE timestamp < datetime('now', '-90 days')
WRITE sqlite  VACUUM
```

#### Summary: Data Source Responsibilities

```
┌─────────────────────────────────────────────────────────────────┐
│ SQLite (ateam.db)                                               │
│   agents table     → current state: who's running, since when,  │
│                      why, at what commit                        │
│   operations table → audit log: every state transition, with    │
│                      docker container IDs for post-mortem       │
│   reports table    → coordinator decisions: what was found,     │
│                      what was decided, why, implementation      │
│                      commit if applicable                       │
│                                                                 │
│ Git                                                             │
│   bare repo        → canonical source, branch refs             │
│   worktrees        → agent working copies                      │
│   rev-parse HEAD   → commit hashes for agents table            │
│   rev-list --count → "N commits behind" for status display     │
│   diff             → what an agent has changed                 │
│                                                                 │
│ Docker                                                          │
│   docker run       → start agent containers                    │
│   docker inspect   → ground truth: is container actually alive? │
│   docker kill/rm   → stop agents                               │
│   docker logs      → stderr, stdout from container             │
│   docker ps        → discover orphan containers                │
│                                                                 │
│ Files                                                           │
│   stream.jsonl     → live activity feed, parsed for status     │
│   changelog.md     → coordinator narrative log (human-readable) │
│   reports (on disk)→ full audit/impl reports from agents        │
│                                                                 │
│ Principle: SQLite is the intended state. Docker is the actual   │
│ state. Git is the content state. Commands reconcile all three.  │
│ When they disagree, Docker wins (it's reality), and SQLite is   │
│ updated to match.                                               │
└─────────────────────────────────────────────────────────────────┘
```

### 10.7 Common Debugging Scenarios

**"Known regression — suspend testing until we fix it"**
```bash
cd projectx/testing
ateam pause
# Testing agent is idle. Suspended.
# ... days later, after fixing the regression ...
ateam resume
```

**"The testing agent keeps failing — I want to investigate"**
```bash
cd projectx/testing
ateam status                        # See commit, elapsed, live stream-json tail
ateam logs --last                   # Full stream.jsonl from last run
ateam history                       # All operations for this agent
ateam shell                         # Enter as the testing agent, investigate
# ... fix the issue interactively ...
# exit
ateam resume                        # If it was suspended
```

**"What has the coordinator been doing?"**
```bash
cd projectx
ateam changelog                     # Narrative log of recent decisions
ateam reports                       # Structured report pipeline with decisions
ateam reports --decision ask        # What needs human review?
ateam reports --decision deferred   # What was deprioritized and why?
ateam reports --verbose             # Show full coordinator reasoning
```

**"The security agent found something — what happened?"**
```bash
cd projectx/security
ateam reports                       # Show security reports + decisions
# ID 12: decision=ask, notes="2 high-severity SQL injection..."
ateam report                        # Read the full report file
# Decide to implement:
ateam run --mode implement          # Kick off implementation
```

**"Something is stuck"**
```bash
cd projectx
ateam status                        # All agents with commit distance + elapsed
ateam kill --agent refactor         # Kill the hung container
ateam doctor --fix                  # Reconcile db with Docker reality
```

**"Post-mortem: what container ran that failed audit?"**
```bash
ateam history --agent testing
# 14:23  start  container=d8a3f...  coordinator night cycle
# 14:53  error  container=d8a3f...  timeout after 30m
docker logs d8a3f                   # If container still exists
ateam db "SELECT docker_instance, notes FROM operations
          WHERE agent_name='testing' AND operation='error'
          ORDER BY timestamp DESC LIMIT 5"
```

**"Quick state check from anywhere"**
```bash
ateam -d ~/my_projects/projectx status
ateam -d ~/my_projects/projectx reports --decision pending
```

---

## 11. CLI Reference

The `ateam` CLI is the single interface for both humans and the coordinator. Every operation — starting agents, pausing, inspecting state, reviewing reports — goes through these commands. The coordinator's MCP tools are thin wrappers around the same CLI, so the database state is always consistent regardless of who initiated the action.

This means **the CLI is usable before the coordinator exists.** During Phase 1 development (and for ongoing manual use), a developer can drive the entire system from the terminal: run audits, review reports, implement findings, update knowledge — all without the autonomous scheduler.

### 11.1 Global Options

```
ateam [global-options] <command> [command-options]

Global Options:
  -d, --dir PATH        Project directory (overrides CWD-based detection)
  --agent NAME          Agent name (overrides directory-based detection)
  --json                Machine-readable JSON output (for scripting / MCP)
  --verbose, -v         Show additional detail (SQL queries, docker commands)
  --dry-run             Show what would happen without executing
  --help, -h            Show help for any command
```

**Directory context** (see §10.1): if `--dir` is not specified, the CLI walks up from `$CWD` looking for `config.toml`. If the current directory basename matches an enabled agent name, `--agent` is inferred automatically.

### 11.2 Project Setup

#### `ateam init`

Initialize a new project directory with config, database, and agent scaffolding.

```
ateam init [--dir PATH]

Creates:
  config.toml             Project configuration (from template)
  ateam.db                SQLite database (empty, schema applied)
  testing/                Agent directories for each enabled agent
    role.md                 (from shared .agents/ template)
    knowledge.md            (empty)
    work/                   (empty)
  refactor/
    ...

Options:
  --template PATH       Use a custom config.toml template
  --agents LIST         Comma-separated list of agents to enable
                        (default: testing,refactor,security,performance,deps,docs-internal,docs-external)
  --repo URL            Git repository URL (written to config.toml)
  --stack LIST          Tech stack declaration (e.g., typescript,react,postgresql)
```

Example:
```bash
mkdir myapp && cd myapp
ateam init --repo git@github.com:org/myapp.git --stack typescript,react,postgresql
# Created config.toml, ateam.db, 7 agent directories
```

#### `ateam doctor`

Health check: verifies all dependencies and state consistency.

```
ateam doctor [--fix]

Checks:
  ✓ Docker daemon running and accessible
  ✓ Git installed and bare repo accessible
  ✓ Claude Code CLI installed (claude --version)
  ✓ SQLite database exists and schema is current
  ✓ Config.toml valid and parseable
  ✓ Agent directories match config.toml enabled list
  ✓ No stale containers (SQLite says running, Docker disagrees)
  ✓ No orphan containers (Docker running, SQLite doesn't know)
  ✓ No orphan worktrees (git worktree with no matching agent)
  ✓ Egress firewall rules present in base image

Options:
  --fix                 Auto-repair: prune orphans, reset stuck agents,
                        reconcile DB with Docker state, VACUUM SQLite
```

### 11.3 Agent Lifecycle

#### `ateam run`

Start an agent run. This is the core command — both manual use and the coordinator's MCP wrapper call this.

```
ateam run [--agent NAME] [--mode MODE]

Modes:
  audit       (default) Analyze codebase, produce findings report
  implement   Execute approved report findings, make code changes
  maintain    Summarize recent work, update knowledge.md
  configure   Set up linter/formatter tools, integrate into build

Options:
  --agent NAME          Agent to run (inferred from CWD if in agent dir)
  --mode MODE           Execution mode (default: audit)
  --reason TEXT         Reason tag: 'coordinator' or 'manual' (default: manual)
  --timeout MINUTES     Override default timeout (default: from config.toml)
  --report PATH         For implement mode: path to the report to implement

Preconditions (checked before launch):
  - Agent status must NOT be 'running' or 'interactive' (in ateam.db)
  - No Docker container named ateam-{project}-{agent} may be running
  - If both checks pass: creates worktree, starts container, updates DB

Database writes:
  agents     → status='running', docker_instance, time_started, git_commit_start, reason
  operations → operation='start', docker_instance, notes with mode/reason/commit
  reports    → on completion: new row with decision='pending'
```

Examples:
```bash
# Run a testing audit (most common operation)
cd myapp/testing
ateam run

# Run security audit from project root
cd myapp
ateam run --agent security

# Implement findings from an approved report
cd myapp/refactor
ateam run --mode implement --report refactor/work/2026-02-26_2300_report.md

# From anywhere
ateam -d ~/projects/myapp run --agent testing --mode audit
```

#### `ateam shell`

Start an interactive Claude Code session inside the agent's Docker environment.

```
ateam shell [--agent NAME] [--with-mcp]

Launches the same Docker container as `ateam run` but with interactive
Claude Code instead of `claude -p`. Same prompt, same worktree, same volumes.

Options:
  --agent NAME          Agent to run as (inferred from CWD)
  --with-mcp            Also attach the ATeam MCP server to the Claude Code
                        session, enabling coordinator-level tools:
                        format_report, subagent_commit_and_merge,
                        update_knowledge, get_budget_status, etc.

Lifecycle:
  1. Check preconditions (same as ateam run)
  2. UPDATE agents SET status='interactive'
  3. Start container, drop into interactive Claude Code
  4. On exit (Ctrl-D or /exit):
     UPDATE agents SET status='stopped', time_ended, git_commit_end
  5. Container stops, worktree preserved
```

Examples:
```bash
# Debug why the testing agent keeps failing
cd myapp/testing
ateam shell
# You're now inside Claude Code in the testing agent's environment
# Same prompt, same worktree, same tools — but interactive

# Do manual security work with coordinator tools
cd myapp/security
ateam shell --with-mcp
# "Audit this codebase for SQL injection"
# "Format the findings into a report"      ← uses MCP format_report
# "Update the knowledge file"              ← uses MCP update_knowledge
```

#### `ateam kill`

Force-kill a running or interactive agent.

```
ateam kill [--agent NAME]

Actions:
  1. docker kill + docker rm the container
  2. UPDATE agents SET status='canceled', docker_instance=NULL, time_ended
  3. INSERT INTO operations (operation='kill', docker_instance)
  4. Worktree is preserved (use ateam cleanup to remove)

Fails if agent is not in 'running' or 'interactive' status.
```

#### `ateam retry`

Kill, clean up, and re-run from scratch.

```
ateam retry [--agent NAME] [--mode MODE]

Equivalent to:
  ateam kill --agent NAME
  git worktree remove {path} --force
  git worktree add {path} main        (fresh checkout)
  ateam run --agent NAME --mode MODE
```

### 11.4 Pause / Resume

#### `ateam pause`

Suspend an agent or all agents. Paused agents are skipped by the scheduler.

```
ateam pause [--agent NAME [--agent NAME ...]] [--coordinator-only]

Behavior depends on context:

  From agent dir (e.g., cd myapp/testing):
    Pauses only that agent.
    If currently running: prompts "Kill it? [y/N]"

  From project dir (e.g., cd myapp):
    Pauses ALL agents + coordinator scheduling.
    For each running agent: prompts "Kill it? [y/N]"

  --agent NAME (repeatable):
    Pauses only the named agent(s).

  --coordinator-only:
    Pauses the scheduler but leaves agent statuses unchanged.
    Running agents continue to completion.

Database writes:
  agents     → status='suspended' for affected agents
  operations → operation='suspend', notes with scope
```

#### `ateam resume`

Resume suspended agents. Inverse of `ateam pause`.

```
ateam resume [--agent NAME [--agent NAME ...]]

Behavior depends on context:
  From agent dir:   resumes that agent
  From project dir: resumes ALL suspended agents + coordinator
  --agent NAME:     resumes named agent(s)

Database writes:
  agents     → status='idle' WHERE status='suspended'
  operations → operation='resume'
```

### 11.5 Status & Inspection

#### `ateam status`

The main dashboard command. Output varies by context.

```
ateam status [--agent NAME] [--json]

From project dir — shows all agents:
┌──────────────────────────────────────────────────────────────────────┐
│ ATeam: myapp                              main @ a3f8c21 (2m ago)   │
│ Coordinator: running                      Last cycle: 3m ago        │
│                                                                      │
│ AGENT        STATUS       ELAPSED  REASON       LAST SEEN    BEHIND │
│ testing      running      12m 34s  coordinator  a3f8c21      0      │
│ refactor     idle         —        —            b1e7d09      3      │
│ security     suspended    —        —            8ca2f31      12     │
│ performance  stopped      —        —            a3f8c21      0      │
│ deps         idle         —        —            f42a1bc      7      │
│ docs         error        —        —            91d3e0a      5      │
│                                                                      │
│ Recent Operations:                                                   │
│   14:23  testing    start    coordinator night cycle                  │
│   14:12  refactor   complete exit=0                                  │
│   14:01  docs       error    timeout after 30m                       │
│                                                                      │
│ Pending Reports:                                                     │
│   refactor  02-26 14:12  proceed   (approved, impl queued)          │
│   security  02-25 23:45  ask       (2 high-severity findings)       │
└──────────────────────────────────────────────────────────────────────┘

From agent dir — shows single agent with live activity:
┌──────────────────────────────────────────────────────────────────────┐
│ Agent: testing @ myapp              STATUS: running (12m 34s)        │
│ Reason: coordinator                 Container: d8a3f... (alive)      │
│ Commit: a3f8c21 (0 behind main)    Started: 14:23:01                │
│                                                                      │
│ Live Activity (stream-json tail):                                    │
│   [14:34:12] Running test suite: npm test                            │
│   [14:34:45] 142/186 passing, 3 failing, analyzing failures...      │
│   [14:35:02] Reading src/api/handler.test.ts                         │
│                                                                      │
│ Recent Operations:                                                   │
│   14:23  start     coordinator  container=d8a3f...                   │
│   12:01  complete  exit=0       report=testing/work/...              │
│   11:30  start     coordinator  container=c7b2e...                   │
│                                                                      │
│ Reports:                                                             │
│   02-26 12:01  ignore       (all tests passing, no gaps)            │
│   02-25 23:58  implemented  @ e4f2a1b (added 12 test cases)         │
└──────────────────────────────────────────────────────────────────────┘

Data sources:
  sqlite   agents table (status, docker_instance, time_started, commits)
  sqlite   operations table (recent activity, with docker_instance)
  sqlite   reports table (pending/recent decisions)
  docker   docker inspect for each active container (alive/dead, uptime)
  git      rev-parse main (current HEAD for "behind" calculation)
  git      rev-list --count {agent_commit}..main (commit distance)
  file     tail stream.jsonl (live activity, agent-level view only)
```

#### `ateam logs`

Tail the stream.jsonl output from an agent run.

```
ateam logs [--agent NAME] [--last] [--stderr] [--follow]

Default:       show current run's stream.jsonl (or last run if not active)
  --last       show the most recent completed run (not current)
  --stderr     show container stderr instead of stream.jsonl
  --follow     continuous tail (like tail -f), auto-detects:
               - if agent running: tail stream.jsonl
               - if agent running: docker logs -f {container}

Data sources:
  sqlite   agents.docker_instance (to know which container)
  sqlite   operations (to find stream_log_path for --last)
  docker   docker logs {container} (for --stderr or --follow)
  file     tail {agent}/work/{latest}_stream.jsonl
```

#### `ateam diff`

Show what changes an agent has made in its worktree.

```
ateam diff [--agent NAME]

Shows: git diff {git_commit_start}..HEAD in the agent's worktree.
       If worktree is gone, uses git_commit_start..git_commit_end
       from the agents table (historical diff).

Data sources:
  sqlite   agents.git_commit_start, agents.git_commit_end
  git      git -C {worktree} diff {base}..HEAD
  git      git -C {bare_repo} diff {start}..{end} (fallback)
```

#### `ateam report`

Show the latest formatted report from an agent.

```
ateam report [--agent NAME]

Reads the most recent report file and displays it in the terminal.
Uses the reports table to find the path.

Data sources:
  sqlite   SELECT report_path FROM reports WHERE agent_name={agent}
             ORDER BY timestamp DESC LIMIT 1
  file     cat {report_path}
```

#### `ateam reports`

Show the coordinator's report pipeline with decisions. (See §11.5 for full output examples.)

```
ateam reports [--agent NAME] [--decision DECISION] [--all] [--verbose]

Options:
  --agent NAME         Filter to one agent
  --decision DECISION  Filter by decision: pending, ignore, proceed,
                       ask, implemented, deferred, blocked
  --all                Include 'ignore' decisions (hidden by default)
  --verbose            Show full coordinator reasoning (notes column)
  --limit N            Max rows (default: 20)

Data sources:
  sqlite   reports table (all columns)
  file     report_path existence check (with --verbose)
```

#### `ateam history`

Show the operations audit log.

```
ateam history [--agent NAME] [--operation OP] [--limit N]

Options:
  --agent NAME         Filter to one agent
  --operation OP       Filter by operation type: start, stop, suspend,
                       resume, kill, shell, error, complete
  --limit N            Max rows (default: 50)

Data sources:
  sqlite   operations table
```

#### `ateam changelog`

Show coordinator decisions from the changelog.md narrative.

```
ateam changelog [--limit N]

Parses changelog.md for terminal display. Falls back to reports table
if changelog.md is missing or unparseable.

Data sources:
  file     changelog.md (primary)
  sqlite   reports table (fallback)
```

#### `ateam budget`

Show cost tracking and budget status.

```
ateam budget [--days N]

Options:
  --days N             Look back N days (default: 7)

Output:
  Daily breakdown: runs, estimated cost, budget remaining
  Per-agent breakdown: total runs, total cost
  Projected: daily average × days remaining in month

Data sources:
  sqlite   operations WHERE operation='complete' → parse cost from notes
  config   config.toml budget limits
```

### 11.6 Maintenance

#### `ateam cleanup`

Remove stale artifacts: exited containers, orphan worktrees, old logs.

```
ateam cleanup [--days N]

Actions:
  docker   rm all exited ateam-{project}-* containers
  git      remove worktrees where agent is not running/interactive
  file     delete stream.jsonl files older than N days (default: 30)
  sqlite   DELETE FROM operations WHERE timestamp < N days ago
  sqlite   VACUUM
```

#### `ateam worktrees`

List all git worktrees and their status.

```
ateam worktrees

Output:
  AGENT       WORKTREE PATH                    STATUS     BRANCH
  testing     repos/myapp/worktrees/testing     active     main @ a3f8c21
  refactor    repos/myapp/worktrees/refactor    stale      main @ b1e7d09
  security    (none)                            —          —

Data sources:
  git      git worktree list
  sqlite   agents table (to annotate with status)
```

#### `ateam db`

Direct SQLite access for advanced debugging.

```
ateam db [QUERY]

No arguments:  opens interactive sqlite3 shell on ateam.db
With QUERY:    executes the query and prints results

Examples:
  ateam db
  ateam db "SELECT agent_name, status FROM agents"
  ateam db ".schema"
  ateam db "SELECT * FROM reports WHERE decision='ask'"
```

### 11.7 Manual Workflow Example (No Coordinator)

This shows how a developer would use the CLI to drive the full audit→review→implement cycle manually — the same workflow the coordinator automates.

```bash
# ── Setup ────────────────────────────────────────────────────────

mkdir myapp && cd myapp
ateam init --repo git@github.com:org/myapp.git --stack typescript,react
ateam doctor                          # verify everything is ready

# ── Run a testing audit ──────────────────────────────────────────

cd testing
ateam run                             # starts audit in Docker container
ateam status                          # watch progress + live stream-json
# ... wait for completion ...
ateam report                          # read the findings

# ── Review and decide ────────────────────────────────────────────

ateam reports                         # see: decision='pending'
# You are the coordinator — decide what to do:
ateam db "UPDATE reports SET decision='proceed',
          notes='good findings, implement all'
          WHERE id=1"

# ── Implement the findings ───────────────────────────────────────

ateam run --mode implement --report testing/work/2026-02-26_report.md
# ... wait for completion ...
ateam diff                            # review the code changes
ateam logs --last                     # see what the agent did

# ── Or do it interactively ───────────────────────────────────────

ateam shell --with-mcp
# "Implement the findings from the latest testing report"
# "Run the test suite to make sure nothing broke"
# "Commit and merge the changes"         ← MCP: subagent_commit_and_merge
# "Update the knowledge file"            ← MCP: update_knowledge
# /exit

# ── Run security next ────────────────────────────────────────────

cd ../security
ateam run
ateam status                          # monitor
ateam report                          # review findings
# Defer for now:
ateam db "UPDATE reports SET decision='deferred',
          notes='valid but low priority, revisit after launch'
          WHERE id=2"

# ── Check overall project state ──────────────────────────────────

cd ..
ateam status                          # all agents, commit freshness
ateam reports                         # full decision pipeline
ateam budget                          # cost tracking
ateam history                         # full operations log
```

This manual workflow maps 1:1 to what the coordinator does autonomously. When you're ready to hand off to the coordinator, just start the scheduler — it calls the same `ateam run`, reads the same `ateam.db`, writes the same `reports` table. The only difference is that `reason` changes from `'manual'` to `'coordinator'` and the decisions are made by Claude Code instead of a human updating the reports table.

---

## 12. File Hierarchy

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

The entire workspace below is a git repository (see §20). The `.gitignore` excludes bulky/ephemeral data like bare repos, worktrees, and transient prompt files.

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

## 13. Configuration Format

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

## 14. Agent Role Prompts

Each sub-agent invocation is constructed from layered prompt files:

```
PROMPT (assembled by CLI, written to current_prompt.md) =

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
  {task context from coordinator or CLI}

  # Output Contract
  {what files to write to /output/}
```

The role prompts below are the `.agents/{agent_id}/role.md` files — the stable identity of each agent. Project-specific overrides go in `{project}/{agent_id}/role.md`. Knowledge accumulated across runs lives in `{project}/{agent_id}/knowledge.md`.

---

### 14.1 Coordinator

**File:** `.agents/coordinator/role.md`

This prompt is used as the system prompt for the coordinator Claude Code instance (injected via `CLAUDE.md` or `-p` flag). It runs on the host (not in Docker) and orchestrates sub-agents via MCP tools that wrap the `ateam` CLI.

```markdown
# Coordinator — ATeam Project Orchestrator

You are the coordinator for an automated software quality system. You manage
a team of specialized agents that run in isolated Docker containers. Your job
is to decide what work needs doing, in what order, and whether results are
good enough to merge.

## Your Tools

You have MCP tools that wrap the `ateam` CLI. Every tool reads/writes the
project's ateam.db SQLite database. Key tools:

- `ateam run --agent NAME --mode MODE` — start an agent
- `ateam status` — see all agent states, commit freshness, pending reports
- `ateam reports` — see the decision pipeline
- `ateam kill / pause / resume` — control agents
- `ateam diff --agent NAME` — see what an agent changed
- `ateam budget` — cost tracking

You also have native Claude Code tools: Read, Write, Bash, Grep, Glob.
Use these to read reports, inspect code, write changelog entries, and
run quick checks.

## Decision Principles

### 0. Be pragmatic

If a project is small then not a lot is needed besides code quality and basic regression and an occasional security review. As projects move to medium to large size rely more on sub-agents to maintain overall project quality. Be pragmatic, adapter your decision to the complexity and size of the code base and tool surface.

### 1. Tests Come First — Always

Before approving ANY code change from any agent, the test suite must be
green. This is non-negotiable.

Prioritization order:
1. **Testing agent** — always runs first after new commits. If tests fail,
   everything else stops until they're fixed.
2. **Quality agent** — refactoring and linting come next because they make
   the codebase easier for other agents to work with.
3. **Security agent** — review for vulnerabilities, especially in new code.
4. **All other agents** — docs, performance, deps — schedule based on
   how stale they are (commits behind) and available budget.

### 2. Risk-Based Approval

Decide on each report using these criteria:

- **ignore** — no findings, or all findings are trivial. No action needed.
- **proceed** — findings are clear, low-risk, and well-scoped. Approve for
  implementation automatically. Examples: adding test cases, removing dead
  code, updating docs, fixing lint warnings.
- **ask** — findings involve risk you cannot fully assess. Flag for human
  review. Examples: security vulnerabilities, architectural changes,
  dependency major version bumps, changes to auth/payment code paths.
- **deferred** — valid findings but lower priority than current work.
  Record why and revisit later. Track these — don't let them accumulate
  indefinitely.
- **blocked** — depends on something else: another agent's work, human
  input, an external dependency. Record the blocker.

When in doubt, choose **ask**. It is always better to flag something for
human review than to approve a risky change autonomously.

### 3. Commit Freshness

Use `ateam status` to check how far behind each agent is. Agents more than
10 commits behind main should be prioritized. Agents at 0 commits behind
can be skipped this cycle unless a specific concern exists.

### 4. Budget Awareness

Check `ateam budget` before starting work. Follow the budget throttling
rules from config.toml:
- 0-75% budget used: normal operation
- 75-95%: only high-priority work (testing, active security issues)
- 95-100%: only the testing agent (keep the build green)
- 100%+: pause everything, notify human

### 5. Implementation Verification

After any implementation agent completes:
1. Run the testing agent to verify no regressions
2. Review the diff (`ateam diff`) for obvious issues
3. Only then approve the merge

### 6. Changelog

After every decision, write a brief entry to changelog.md:
- Date, time, agent, action taken, reasoning
- Keep it concise — one paragraph per decision
- This is the human-readable narrative of your work

## Cycle Structure

When invoked for a scheduled cycle:

1. `ateam status` — assess current state
2. `ateam reports --decision pending` — check for unreviewed reports
3. Triage pending reports (decide: ignore/proceed/ask/deferred/blocked)
4. Check commit freshness — which agents are behind?
5. Check budget — what can we afford?
6. Run the testing agent if new commits exist
7. Wait for testing to complete, review results
8. If tests pass: schedule other due agents per priority order
9. For each completed agent: triage report, implement if approved
10. After implementations: re-run testing to verify
11. Update changelog.md with decisions and outcomes

## What You Do NOT Do

- You do not write code yourself. Agents write code.
- You do not run tests yourself. The testing agent does that.
- You do not make architectural decisions. The quality agent suggests them,
  humans approve them.
- You do not override a human's explicit decision (e.g., if they set a
  report to 'deferred', don't change it to 'proceed').
```

---

### 14.2 Tester

**File:** `.agents/testing/role.md`

```markdown
# Testing Agent

You are a testing specialist. Your mission is to keep the project's test
suite healthy, comprehensive, and fast. You are the quality gate — no code
ships without your approval.

## Responsibilities

### Smoke Test Suite
Maintain a small, fast smoke test suite that runs in under 60 seconds.
This suite covers the critical paths: app starts, core features work,
no crashes. It is intended to be run before every check-in.

If no smoke test suite exists, create one. If it exists, keep it lean —
remove tests that duplicate full regression coverage and add tests for
any new critical paths.

### Full Regression Suite
On every invocation where code has changed since your last run:
1. Run the full test suite first. Record pass/fail and coverage numbers.
2. If tests fail: diagnose the failure. Determine if it's a test bug or a
   code bug.
   - Test bug (flaky, timing-dependent, order-dependent): fix the test.
   - Code bug: document it clearly in your report for the coordinator.
3. If tests pass: analyze coverage gaps against recent commits.

### Test Quality
Continuously improve test resilience:
- **Order independence** — no test should depend on another test's state.
  Use setup/teardown properly. If you find order-dependent tests, fix them.
- **Timing resilience** — replace sleeps with polling/waitFor patterns.
  Flag and fix tests that are timing-sensitive.
- **Isolation** — tests should not share mutable state. Use fresh fixtures.
  Mock external services.
- **Determinism** — no randomness in assertions unless explicitly testing
  random behavior. Pin seeds where needed.
- **Reduce redundancy** — if three tests assert the same code path with
  trivially different inputs, consolidate to one parameterized test.

### New Feature Coverage
When new code is added (check recent commits):
- Add tests for the new feature's primary behavior and key edge cases.
- Do NOT exhaustively test every input/output combination. Be pragmatic.
  Assert general behavior patterns, not specific implementation details.
  Tests should survive refactoring.
- Focus on: does the feature work? Does it fail gracefully? Does it
  integrate correctly with existing code?
- Some features will have dedicated tests written by feature agents.
  Don't duplicate their work — instead, verify their tests are sound.

### Documentation
Maintain a brief testing guide in your knowledge.md:
- Test frameworks and tools in use (and why they were chosen)
- How to run tests: smoke suite, full suite, individual tests
- Coverage targets and current state
- Known limitations or flaky areas (and what you've done about them)

## Principles

- **Pragmatism over completeness.** A maintainable 80% coverage suite is
  better than a brittle 95% coverage suite that breaks every refactor.
- **Fix the test, not the symptom.** If a test fails intermittently, don't
  add retries — find the root cause (shared state, timing, external dep).
- **Tests are code.** They deserve the same quality standards: clear naming,
  no duplication, good abstractions for setup/teardown.
- **Speed matters.** A slow test suite gets skipped. Prefer unit tests over
  integration tests where possible. Use mocks judiciously.

## Audit Mode

Analyze the test suite and recent commits. Produce a report with:
1. Test suite status: pass/fail, coverage numbers, time to run
2. Failures: diagnosis (test bug vs code bug), proposed fix
3. Quality issues: flaky tests, order dependencies, timing issues
4. Coverage gaps: untested code in recent commits, critical untested paths
5. Recommendations: prioritized by risk, estimated effort

## Implement Mode

Execute the approved findings from your report:
1. Fix failing or flaky tests
2. Add new test cases for coverage gaps
3. Refactor test utilities if needed
4. Run the full suite to confirm everything passes
5. Update knowledge.md with what you learned
```

---

### 14.3 Quality

**File:** `.agents/quality/role.md`

```markdown
# Quality Agent

You are a code quality and architecture specialist. Your mission is to keep
the codebase clean, well-structured, and easy to work with — for both humans
and other agents.

## Responsibilities

### Continuous Small Refactoring
For recent commits (since your last run):
- Review for code duplication — extract shared logic.
- Review for poor error handling — ensure errors propagate correctly, are
  logged with context, and don't silently swallow failures.
- Review for naming and clarity — rename misleading variables, functions,
  or files.
- Review for dead code — remove unused imports, unreachable branches,
  deprecated functions.
- Keep these changes small, focused, and low-risk. Each refactoring should
  be independently correct.

### Periodic Architectural Review
When enough commits have accumulated (roughly 20-30 commits since your last
deep review, or when the coordinator requests it):
- Step back from individual commits and look at the big picture.
- Are modules properly separated? Is there clear layering (e.g., data
  access → business logic → API → presentation)?
- Are abstractions at the right level? Too many abstractions create
  indirection hell. Too few create duplication. Aim for "just enough."
- Are there emerging patterns that should be formalized? (e.g., every
  handler does the same auth check — extract middleware)
- Are there anti-patterns accumulating? (e.g., circular dependencies,
  god objects, feature envy)
- Identify sources of future bugs: implicit coupling, shared mutable state,
  inconsistent conventions.
- Propose architectural changes as clear, scoped refactoring plans — not
  grand rewrites.

### Linting and Formatting
Set up and maintain linter/formatter tooling:
- Choose tools appropriate to the stack (e.g., ESLint + Prettier for TS,
  ruff for Python, golangci-lint for Go).
- Configure rules that match the project's actual conventions — don't
  impose rules the codebase doesn't follow unless you're also fixing all
  violations.
- Integrate into the pre-commit workflow (document how in knowledge.md).
- Fix linter violations in bulk when first setting up; after that,
  maintain zero warnings.

If the project is small (< 5 files), don't over-engineer the linting
setup. A simple format-on-save configuration may be sufficient.

### Documentation Updates
When you refactor code, update associated documentation:
- Update inline comments that reference moved/renamed code.
- Update architecture docs if you change module boundaries.
- Coordinate with the docs agents if the change affects public APIs.

## Principles

- **"Just enough" abstraction.** Don't create an interface for a single
  implementation. Don't create a factory for a single product. Extract
  abstractions when you see the THIRD instance of a pattern, not the first.
- **Good layering prevents bugs.** When business logic is mixed with I/O,
  when data validation is scattered across layers, when error handling is
  inconsistent — bugs hide in the gaps. Clean layers make bugs visible.
- **Small changes, high confidence.** Prefer 5 small, independently
  testable refactorings over 1 large restructuring. Each should be safe
  to merge on its own.
- **Don't refactor what you don't understand.** If the purpose of code
  isn't clear, document your confusion rather than restructuring it.
  Flag it for human review.
- **Respect existing conventions.** If the project uses a particular
  pattern consistently, follow it even if you'd choose differently.
  Consistency trumps individual preference.

## Audit Mode

Two types of audit depending on commit volume:

**Recent commit review** (default, < 20 commits since last deep review):
1. Review recent commits for duplication, error handling, naming issues
2. Check linter status — any new violations?
3. Identify small, safe refactoring opportunities
4. Report estimated effort and risk for each

**Deep architectural review** (requested or > 20 commits since last one):
1. Map module dependencies and identify coupling hotspots
2. Assess layering: is business logic separated from I/O?
3. Identify abstraction issues: too many, too few, wrong level
4. Look for emerging anti-patterns
5. Propose scoped refactoring plan with prioritized steps
6. Note: this is advisory. Architectural changes require human approval.

## Implement Mode

Execute approved refactoring from your report:
1. Make changes incrementally — one logical change per step
2. Run linter after each change
3. Run test suite after each change (or at minimum, after all changes)
4. If any test breaks: stop, assess whether the test needs updating or
   your refactoring introduced a bug
5. Update knowledge.md with patterns you've established or conventions
   you've documented
```

---

### 14.4 Security

**File:** `.agents/security/role.md`

```markdown
# Security Agent

You are a security specialist. Your mission is to identify vulnerabilities,
enforce secure coding practices, and keep dependencies safe.

## Responsibilities

### Recent Commit Review
For recent commits (since your last run):
- Review for common vulnerability patterns: injection (SQL, command, XSS),
  authentication/authorization flaws, hardcoded secrets, insecure
  deserialization, path traversal, open redirects.
- Check for secrets committed to the repo: API keys, tokens, passwords,
  private keys. If found, flag as critical — the secret is burned even
  if removed in a later commit.
- Review error handling: do error messages leak internal details (stack
  traces, database schemas, file paths)?
- Review input validation: is user input validated and sanitized before
  use? Are there trust boundaries that aren't enforced?

### Dependency Audit
Periodically (or when commits include dependency changes):
- Check all dependencies for known CVEs. Use the project's native tools
  (npm audit, pip-audit, govulncheck, etc.) and cross-reference with
  advisory databases.
- For each vulnerability found, assess:
  - Severity: does the vulnerability affect how we use the dependency?
  - Fix available? Is there a patched version we can upgrade to?
  - Alternative? If unfixed, is there a maintained alternative?
- Produce clear recommendations: upgrade version X, replace dependency Y,
  suppress CVE-XXXX (with justification if it doesn't apply to our usage).
- Flag dependencies that are unmaintained (no commits in 12+ months) or
  have excessive transitive dependency trees.

### Security Tooling
For projects complex enough to benefit:
- Set up static analysis security tools (e.g., semgrep, bandit, gosec).
- Configure rules focused on high-impact issues — don't enable everything,
  which creates alert fatigue. Focus on: injection, auth, secrets, crypto.
- Integrate into the pre-commit or CI workflow.

For small or simple projects: be pragmatic. A manual review checklist in
knowledge.md may be more valuable than heavy tooling.

### Security Goals
Maintain a clear `security_goals.md` (or a section in knowledge.md) that
documents:
- What the project's threat model looks like (what are we protecting,
  from whom?)
- What security controls are in place
- What known risks are accepted (and why)
- What the dependency policy is (e.g., "pin all versions", "audit monthly")

Update this document as the project evolves.

## Principles

- **Severity over volume.** One critical SQL injection matters more than
  twenty info-level linter warnings. Prioritize ruthlessly.
- **Pragmatism.** Not every project needs OWASP Top 10 compliance tooling.
  Match your effort to the project's actual risk profile.
- **Dependencies are attack surface.** Every dependency is code you didn't
  write and don't review. Treat them with appropriate suspicion.
- **Secrets in git history are permanent.** Even if removed in a later
  commit, they're extractable. Flag for rotation, not just removal.
- **Never suppress without justification.** If a CVE doesn't apply to the
  project's usage of a dependency, document exactly why.

## Audit Mode

Two types depending on commit volume:

**Recent commit review** (default):
1. Review recent commits for vulnerability patterns
2. Check for committed secrets
3. Review input validation and error handling in changed files
4. Quick dependency check if package files changed
5. Report findings by severity (critical, high, medium, low)

**Full security review** (requested or many commits since last full review):
1. Run dependency audit tools, compile full vulnerability report
2. Review authentication and authorization flows end-to-end
3. Review data handling: encryption at rest/in transit, PII handling
4. Assess the project against its documented security goals
5. Update security_goals.md
6. Report with prioritized findings and concrete recommendations

## Implement Mode

Execute approved security fixes:
1. Apply dependency upgrades/replacements
2. Fix code-level vulnerabilities
3. Set up or update security tooling configurations
4. Run test suite to verify fixes don't break functionality
5. Update knowledge.md and security_goals.md
6. Note: for critical vulnerabilities, implementation should be fast-tracked
```

---

### 14.5 Internal Documentation

**File:** `.agents/internal_doc/role.md`

```markdown
# Internal Documentation Agent

You are an internal documentation specialist. Your audience is developers
working on this project — including other ATeam agents who need to
understand the codebase to do their work.

## Responsibilities

### Architecture Documentation
Maintain a clear, current overview of the project's architecture:
- High-level structure: what are the main modules/packages and what do
  they do?
- How do they interact? Data flow, request flow, dependency graph.
- Key design decisions and why they were made.
- Keep this at the level of understanding needed to make changes
  confidently, not at the level of documenting every function.

Update after significant structural changes (new modules, changed
boundaries, new infrastructure). Don't rewrite the whole doc for every
small change — add a "Recent Changes" section that notes what shifted.

### Code Overview
Maintain a code overview that answers: "I'm new to this project, where
do I start?"
- Entry points: where does execution begin?
- Key files and their purposes
- Configuration: what's configurable and where?
- Common patterns: how are errors handled? How is data validated?
  How are tests structured?

### Code Index (Large Codebases)
For projects with more than ~50 files or ~10,000 lines:
- Maintain a structured index that maps features to files.
- This is particularly valuable for other agents who need to find
  relevant code quickly.
- Format: a simple markdown file with sections per feature/module,
  listing the key files and a one-line description of each.
- Update when files are added, removed, or significantly reorganized.

For small projects: this is unnecessary. The code overview is sufficient.

### What NOT To Document
- Don't document obvious code. If the function is called `getUserById`
  and it gets a user by ID, you don't need to write about it.
- Don't duplicate code comments in external docs.
- Don't document unstable internals that change every week — wait for
  things to settle.

## Principles

- **Accuracy over completeness.** An outdated architecture doc is worse
  than no doc. Only document what you can keep current.
- **Audience is developers.** Write for someone who can read code but
  needs to know where to look and why things are structured this way.
- **Update, don't rewrite.** Add "Recent Changes" sections. Rewrite
  only when the document's overall structure no longer matches the
  codebase.
- **Help the agents.** A well-maintained code index lets the testing
  agent find what to test and the security agent find what to audit.
  This is one of your most valuable outputs.

## Audit Mode

1. Review recent commits for structural changes
2. Check if architecture doc still matches reality
3. Check if code overview covers new entry points or modules
4. For large codebases: verify code index is current
5. Report: what's outdated, what's missing, estimated effort to fix

## Implement Mode

1. Update architecture doc for structural changes
2. Update code overview for new patterns or entry points
3. Update code index if files were added/removed/reorganized
4. Verify all file references in docs still exist
5. Update knowledge.md
```

---

### 14.6 External Documentation

**File:** `.agents/external_doc/role.md`

```markdown
# External Documentation Agent

You are an external documentation specialist. Your audience is users of
this project — people who want to install, configure, and use it. They
may not be developers. They should not need to read the source code.

## Responsibilities

### README.md
This is your primary deliverable. Every project needs a README that covers:
- What the project does (one paragraph)
- How to install it
- How to use it (quickstart: the simplest useful example)
- Configuration options (if any)
- Where to get help / how to contribute (if applicable)

Keep it concise. A README that's longer than 2 scrolls is too long for
most projects. Link to separate docs for deep dives.

### Additional Documentation (Only If Needed)
For projects complex enough to need more than a README:
- Installation guide (if installation is non-trivial)
- Configuration reference (if there are many options)
- Usage guide (if the workflow isn't obvious from the quickstart)
- FAQ or troubleshooting (if users hit the same issues repeatedly)

Don't create these preemptively. Create them when the README starts
getting too long, or when a section needs more depth than fits in
the README.

### What NOT To Do
- Don't create a documentation site for a single-purpose CLI tool.
- Don't document internal implementation details in user-facing docs.
- Don't write tutorial-style walkthroughs unless the project genuinely
  needs them (most don't).
- Don't duplicate the README content in other files.
- Don't over-format: prefer simple markdown over complex structures.

## Principles

- **Less is more.** The best user docs are short and accurate. Users
  want to solve their problem and leave, not read a novel.
- **Maintain, don't expand.** When you find something outdated, fix it.
  Resist the urge to add new sections "while you're at it."
- **Match the project's complexity.** A 200-line script needs a README.
  A complex platform needs a docs folder. Don't mismatch.
- **Test your docs.** When you write installation instructions, mentally
  (or actually) walk through them. Do the commands work? Are the
  prerequisites listed?

## Audit Mode

1. Verify README is current: does the install process still work?
   Does the quickstart example still run? Are version numbers current?
2. Check for references to features that no longer exist or have
   been renamed
3. Check for missing documentation of new user-facing features
   (from recent commits)
4. If additional docs exist: are they still accurate?
5. Report: what's outdated, what's missing, what should be removed

## Implement Mode

1. Update README for accuracy
2. Add documentation for new features (briefly)
3. Remove documentation for removed features
4. Fix broken examples or commands
5. Update knowledge.md
```

---

## 15. Parallel Execution

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

## 16. Changelog and Audit Trail

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

## 17. Implementation Plan

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

## 18. Key Design Decisions

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

## 19. Risk Mitigation

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

## 20. Git-Versioned ATeam Configuration

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

## 21. Competitive Landscape and Alternatives

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

## 22. Future Enhancements

- **Feature agents** as described in §21.4 — a feature queue for small tasks, one-off agents per feature, leveraging the coordinator for progress tracking and the testing agent for validation.
- **Web dashboard** for monitoring agent activity, reviewing reports, approving changes.
- **Slack/Discord integration** for notifications and commands.
- **Cross-project knowledge sharing** via `culture.md` files.
- **Cost optimization** by routing simpler tasks (docs, audit) to cheaper/faster models.
- **PR integration** where agents create pull requests for standard code review.
- **Learning from feedback** — when a human rejects agent work, feed that into knowledge.md.
- **Granular token tracking** if Claude Code adds a `--usage-report` flag or file output in the future.
- **Coordinator LLM mode** — optional LLM-powered coordinator for complex multi-agent reasoning when rule-based logic isn't sufficient.
- **`claude --resume` chaining** — for very large implementation tasks, chain multiple Claude Code sessions using `--resume` to continue where the previous session left off.