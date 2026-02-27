# ATeam Design Specification

> **Working Name:** ATeam (Agent-Team)
> **Version:** 0.3 — Added git-versioned config + competitive landscape
> **Date:** 2026-02-26

---

## 1. Executive Summary

Agent-generated code requires constant review or it becomes spaghetti — new features break existing code, test coverage erodes and breaks, dependencies rot, documentation drifts. ATeam manages non-interactive agents that work in the background to prevent this: refactor code as it is being added, improve and debug tests, review security, manage dependencies, update internal and external documentation. Humans focus on features with interactive agents; ATeam quietly handles everything else.

**The problem with agents is attention**. They demand constant review and approval. ATeam manages agents that work completely unattended inside Docker containers. A coordinator agent reviews the output of role-specific agents, prioritizes their work, and only asks a human as a last resort. Typically ATeam runs at night, but it can also run on demand or on each commit.

**Nobody wants a complex tool** in their workflow, so ATeam is built on simple, familiar pieces:
* **Docker** to run agents unattended with full control inside the container — no permission prompts, no risk to the host.
* **Git-managed** markdown files for agent roles, reports, and accumulated knowledge. After each run, agents summarize what they learned to build project-specific context over time. `git log` is a narrative of everything agents have done.
* **A single CLI** to start, stop, and check on agents. It manages Docker containers, git worktrees, and tracks state in a SQLite database — which commits each agent has seen, what they found, what they did. The same CLI is used by humans and by the coordinator agent.
* **No daemons, no complex IPC, no agents chatting**. Simple single-prompt agents doing one thing at a time on their own. A sqlite database is managed by the CLI to track the status of each agent

ATeam's prompts promote **pragmatic approaches**. Small projects don't need exhaustive test suites or complex tooling. Role-specific agents are prompted to configure automated tools — smoke tests, linters, formatters — to tighten the development environment progressively. Code refactoring happens frequently at the scale of recent commits, and occasionally looks at bigger architectural improvements. The coordinator requests reports from role-specific agents on what could be done, then decides which tasks to act on now and which to defer. It tries to be mindful of token usage and the cost of this background work, but it continuously maintains good project hygiene. After each implementation, the coordinator asks role-specific agents to update their project knowledge so the next run has relevant context — knowledge is built and refined over time.

An organization layer above projects **factors out common knowledge** for specific roles and tech stacks — conventions, patterns, and preferences shared across your codebase.

**The cost**: dockerize the development environment of a project (an agent can do the heavy lifting), then a few CLI commands set up ATeam on any git repo while you continue your work.

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

## 3. Language Choice: Go CLI

**Go** for the entire framework: CLI, container adapters, git management, SQLite operations, prompt builder, scheduler. A single `go build` produces one static binary (~15-20MB) that embeds all default files (role prompts, knowledge templates, database schema).

**Claude Code** is the sub-agent runtime — it runs inside Docker and does the actual coding work. The coordinator also runs as Claude Code, using the `ateam` CLI via bash commands.

### 3.2 Dependencies

| Capability | Go Module | Notes |
|---|---|---|
| **CLI framework** | `cobra` + `viper` | De facto Go CLI standard |
| **SQLite** | `modernc.org/sqlite` | Pure Go, no CGo, cross-compiles cleanly |
| **Docker management** | `docker/docker` client | Official Docker SDK |
| **Git operations** | `os/exec` → `git` | Shell out for worktree management (simpler than go-git for our use cases) |
| **TOML parsing** | `BurntSushi/toml` | Mature, well-maintained |
| **Scheduling** | `robfig/cron` | Cron expressions, timezone support |
| **Process execution** | `os/exec` (stdlib) | For invoking `claude -p` |
| **Embedded files** | `embed` (stdlib) | Role prompts, knowledge templates, schema |
| **Structured logging** | `log/slog` (stdlib) | Built-in since Go 1.21 |
| **LLM SDK** | **Not needed** | Claude Code handles all LLM interaction |

### 3.3 API Key Support

Sub-agents and the coordinator can run in two modes:

- **Subscription mode** (default): Claude Code uses the user's existing Claude subscription (Pro/Max). The `~/.claude/` directory is mounted into containers. No API key needed.
- **API key mode**: Set `ANTHROPIC_API_KEY` in the environment or `.env` file. Claude Code uses the API directly. This enables `--max-budget-usd` for per-run cost control, which is critical for unattended operation.

API key mode is preferred for autonomous/scheduled operation because it provides hard budget caps. Subscription mode works for interactive sessions (`ateam shell`) and development.

```toml
# config.toml
[budget]
api_key_env = "ANTHROPIC_API_KEY"   # env var name (or set in .env)
max_budget_per_run = 2.00           # USD, passed as --max-budget-usd to claude
max_budget_daily = 20.00            # USD, enforced by CLI before launching
max_budget_monthly = 200.00         # USD, enforced by CLI before launching
model = "sonnet"                    # default model for sub-agents
coordinator_model = "sonnet"        # model for coordinator (can use cheaper model)
```

The CLI checks budget before every launch:
1. Sum costs from `operations` table for the current day/month
2. If over daily/monthly limit → refuse to launch, log warning
3. If under limit → pass `--max-budget-usd {per_run_limit}` to `claude -p`
4. On completion → parse cost from stream-json, record in operations table

---

## 4. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│                    Human Developer                         │
│       (git push, priority overrides via `ateam` CLI)      │
└────────────────────────┬─────────────────────────────────┘
                         │
              ┌──────────┴──────────┐
              ▼                     ▼
┌───────────────────────┐ ┌───────────────────────────────┐
│    ateam CLI (Go)      │ │ Coordinator (Claude Code)      │
│                        │ │                                │
│  install / init        │ │ Invoked by scheduler:          │
│  run / shell / kill    │ │   claude -p "Run ATeam cycle"  │
│  status / reports      │ │     --max-budget-usd 5.00      │
│  pause / resume        │ │                                │
│  cleanup / doctor      │ │ Uses Bash tool to call:        │
│                        │ │   ateam status --json          │
│  Container adapters    │ │   ateam run -a testing         │
│  Git worktree mgmt     │ │   ateam reports --decision ... │
│  SQLite state          │ │   cat reports/...              │
│  Prompt builder        │ │   ateam db "UPDATE ..."        │
│  Budget enforcement    │ │                                │
└───────────┬────────────┘ └────────────┬──────────────────┘
            │                           │
            │   both call same CLI      │
            └───────────┬───────────────┘
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
       └────────┬───────┴────────────────┘
                ▼
      ┌─────────────────┐
      │ Persistent       │  (bind-mounted into containers)
      │ Workspace:       │
      │  code/ data/     │
      │  artifacts/      │
      └─────────────────┘

Communication is via filesystem:
  IN:  /agent-data/prompt.md  (task + role + knowledge)
  OUT: /output/stream.jsonl   (execution trace)
       /workspace/            (code changes in worktree)
```

The key insight: **both the human and the coordinator use the same `ateam` CLI.** The coordinator is Claude Code with a system prompt describing the CLI commands. It calls `ateam status --json` via its Bash tool, reads the output, makes decisions, calls `ateam run -a testing`, and so on. There is no MCP layer, no separate server process — just a Go binary that manages all infrastructure.

### Core Components

| Component | Implementation | Responsibility |
|---|---|---|
| **CLI** | Go binary (`cobra`) | All commands: run, status, reports, pause, etc. |
| **Container Adapter** | Go interface + Docker/Compose impls | Agent environment lifecycle (see §7.4) |
| **Git Manager** | Go (`os/exec` → `git`) | Bare repo, persistent worktrees, refresh, branch/merge |
| **SQLite State** | Go (`modernc.org/sqlite`) | agents, operations, reports tables |
| **Prompt Builder** | Go + `embed.FS` | Assembles layered prompts from role + knowledge + task |
| **Budget Enforcer** | Go | Checks daily/monthly limits, passes `--max-budget-usd` |
| **Scheduler** | Go (`robfig/cron`) or cron/systemd | Periodic invocation of coordinator Claude Code |
| **Coordinator** | Claude Code (`claude -p`) | Reasoning: report triage, prioritization, decisions |

---

## 5. Sub-Agent Design: Claude Code Inside Docker

### 5.1 Why Claude Code

Claude Code is already an excellent coding agent — file editing, shell execution, iterative debugging, error recovery, and multi-step reasoning are all built-in and battle-tested. Building a custom agent loop would be hundreds of lines of fragile code that's worse than what Claude Code already provides. The tradeoff (less granular programmatic control) is worth the massive reduction in complexity. See Appendix A.1 for the detailed comparison.

### 5.2 Execution Modes

Each sub-agent operates in one of four modes per invocation:

| Mode | Input | Output |
|---|---|---|
| **Audit** | Source code + knowledge.md | `YYYY-MM-DD_HHMM_report.md` — findings and recommendations |
| **Implement** | Report (possibly amended) + source code | Code changes in worktree + `_report_completion.md` |
| **Maintain** | Last completion report + knowledge.md | Updated `knowledge.md` |
| **Configure** | Source code + knowledge.md | Linter/formatter config files integrated into build system |

### 5.3 How Sub-Agents Are Invoked

The coordinator (or a human via `ateam run`) triggers a sub-agent run. The Go CLI handles the full lifecycle:

**Step 1: Assemble the prompt.** The prompt builder reads layered files (org role → project role_add/override → stack knowledge → project knowledge → project goals → mode instructions → task context) and writes the assembled prompt to `{project}/{agent}/current_prompt.md`.

**Step 2: Refresh the worktree.** `git fetch origin && git reset --hard origin/main` in the agent's persistent worktree. Untracked files (node_modules, data/) are preserved.

**Step 3: Launch container via adapter.** The container adapter (Docker, Compose, or script) starts the container with bind mounts for code, data, artifacts, .env, and the prompt file. See §7.3–7.4 for details.

**Step 4: Claude Code runs.** Inside the container, the entrypoint starts any services (database, etc.), then runs:
```bash
claude -p "$(cat /agent-data/current_prompt.md)" \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --max-budget-usd 2.00 \
  | tee /output/stream.jsonl
```

Claude Code autonomously explores the codebase, runs tests, makes changes, iterates, and writes output. The stream-json captures the full execution trace.

**Step 5: Collect results.** The CLI reads the exit code, parses cost from stream-json, generates a report (see §7.8), updates the database (agents table, operations log, reports table), and returns.

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

### 5.5 Is `claude -p` Sufficient?

Yes. Sub-agent tasks are well-scoped by design (audit → approve → implement phases). The prompt includes all necessary context. Claude Code's tool-use and iteration capability is the same in `-p` mode as interactive mode. If a task is too complex for a single invocation, the agent writes `/output/blocked.md` and the coordinator re-scopes or escalates. See Appendix A.2 for the detailed analysis.

### 5.6 Multi-Provider Support

The primary runtime is Claude Code. The container adapter and prompt builder are provider-agnostic — the entrypoint command is configured in `config.toml`. Other CLI agents (Codex, Gemini CLI) can be swapped in by changing the provider setting. A custom API fallback loop is available for providers without CLI agents. See Appendix A.3 for provider comparison.

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

Each project has a bare clone of the source repository and persistent per-agent worktrees under the `workspace/` directory:

```
ORG_ROOT/
  projectx/
    repos/
      bare/                         # bare clone: git clone --bare <remote>
    workspace/                      # gitignored by project repo
      testing/
        code/                       # persistent git worktree
      refactor/
        code/                       # persistent git worktree
      security/
        code/
      feature/                      # manual development worktree
        code/
```

Worktrees are **persistent, not ephemeral.** Each agent has one worktree that is created once (on first run or `ateam init`) and reused across all subsequent runs. This means installed dependencies (`node_modules/`, Python venvs), build caches (`.next/`, `dist/`), and local configuration survive between runs.

### 6.2 Worktree Lifecycle

```
ateam init myapp --git ...
  → git clone --bare <remote> → repos/bare/
  → for each enabled agent:
      git -C repos/bare worktree add ../../workspace/{agent}/code origin/main

ateam run -a testing
  → cd workspace/testing/code/
  → git fetch origin
  → git reset --hard origin/main      (refresh to latest, preserve untracked files)
  → record HEAD as git_commit_start
  → launch container with workspace/testing/code/ mounted
  → on completion: record HEAD as git_commit_end

ateam shell -a testing
  → same refresh, but interactive session
  → developer may create branches, commit, etc.

ateam cleanup
  → removes old logs, exited containers
  → does NOT remove worktrees or workspace/ data by default
  → ateam cleanup --full: removes workspace/ entirely (destructive)
```

The `git reset --hard origin/main` at the start of each run ensures the agent sees the latest code while preserving untracked files (node_modules, data directories, build caches). If an agent left uncommitted changes from a previous run, they are discarded — the agent's changes should have been committed and merged (or discarded) by the coordinator before the next run.

### 6.3 Branching

Agents do not commit directly to main. The workflow is:

1. Agent works in its worktree (which tracks `origin/main`).
2. If the agent makes code changes (implement mode), the coordinator creates a branch: `git checkout -b ateam/{agent}/{date}`.
3. Agent commits to the branch.
4. Coordinator reviews the diff, runs the testing agent to verify.
5. Coordinator merges the branch to main (fast-forward or rebase).
6. Next run: `git fetch && git reset --hard origin/main` picks up the merged changes.

### 6.4 Conflict Resolution

When multiple agents have pending branches:

1. Coordinator merges them sequentially, not in parallel.
2. If a rebase produces conflicts, the relevant agent is re-invoked in implement mode with conflict markers as context.
3. The agent resolves conflicts, the coordinator verifies tests pass, then continues.

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

### 7.3 Persistent Workspace and Volume Mounts

Each agent has a persistent workspace directory under `{project}/workspace/{agent}/` with a well-defined structure:

```
workspace/{agent}/
  code/              # git worktree (persistent, bind-mounted rw)
    node_modules/    #   survives between runs
    .next/           #   build cache survives
    ...
  data/              # persistent state (databases, uploads, caches)
    pg_data/         #   PostgreSQL data directory (if project uses PG)
    redis_data/      #   Redis persistence
    ...
  artifacts/         # build outputs, coverage reports, test results
```

**Bind mounts into the container:**

```
Host                                    Container               Mode
workspace/{agent}/code/                 /workspace              rw
workspace/{agent}/data/                 /data                   rw
workspace/{agent}/artifacts/            /artifacts              rw
{project}/.env                          /workspace/.env         ro
{project}/{agent}/current_prompt.md     /agent-data/prompt.md   ro
~/.claude/                              /home/node/.claude      ro
```

The `/workspace` mount is the git worktree — the agent reads and writes source code here. The `/data` mount is where databases, caches, and other persistent state live — this survives across runs so the agent doesn't have to rebuild the database from scratch every time. The `/artifacts` mount is where test results, coverage reports, and build outputs go.

**Why persistent data matters.** For a project like TripManager (Node + PostgreSQL + API integrations):

- First run: the agent runs `npm install` (populates `node_modules/`), starts PostgreSQL (creates `pg_data/`), runs migrations (creates schema), seeds test data. This might take 5-10 minutes.
- Second run: `node_modules/` is already populated (only changed deps are installed), PostgreSQL data directory exists (just start the server), schema is already migrated. The agent is productive in seconds.
- Interactive session (`ateam shell`): everything is already warm. You start debugging immediately.

**The fat-container pattern.** Most projects should use a single Dockerfile that installs everything needed to run the full stack — database server, language runtime, build tools, project dependencies. This keeps things simple: one container, one process supervisor (or just background processes), everything accessible from the Claude Code session for debugging:

```dockerfile
FROM ateam-base:latest

# Install everything in one container
USER root
RUN apt-get update && apt-get install -y \
    postgresql-16 postgresql-client-16 \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Configure PostgreSQL to use /data/pg_data
RUN mkdir -p /etc/postgresql && \
    echo "data_directory = '/data/pg_data'" > /etc/postgresql/postgresql.conf

USER node

# Project dependencies (pre-installed in image for speed)
COPY --chown=node:node package.json package-lock.json /workspace/
RUN cd /workspace && npm ci

# Entrypoint: start services, then hand off to Claude Code
COPY --chown=node:node entrypoint.sh /usr/local/bin/
```

The `entrypoint.sh` script starts PostgreSQL (pointing at `/data/pg_data`), runs migrations if needed, and then executes Claude Code. Because `/data/pg_data` is a persistent bind mount, the database state survives across runs.

### 7.4 Environment Abstraction: Container Adapters

The ATeam CLI doesn't call `docker run` directly. Instead, it calls a **container adapter** — an abstraction that manages the agent's execution environment. This allows swapping Docker for other runtimes, or having project-specific launch logic.

```
┌──────────────────────────────────────────┐
│ ATeam CLI                   │
│   ateam run -a testing                   │
└───────────────┬──────────────────────────┘
                │ calls adapter
                ▼
┌──────────────────────────────────────────┐
│ Container Adapter Interface              │
│   start(agent, project, mode) → id      │
│   stop(id)                              │
│   kill(id)                              │
│   status(id) → running/stopped/...      │
│   logs(id) → stream                     │
│   exec(id, command) → output            │
└───────────────┬──────────────────────────┘
                │
        ┌───────┴────────┐
        ▼                ▼
┌──────────────┐  ┌──────────────┐
│ Docker       │  │ Compose      │
│ Adapter      │  │ Adapter      │
│ (default)    │  │              │
│ Single fat   │  │ Multi-service│
│ container    │  │ agent + db + │
│              │  │ redis + ...  │
└──────────────┘  └──────────────┘
        ▲                ▲
        │                │
   Future adapters:      │
   Podman, nerdctl,      │
   custom scripts        │
```

**The adapter interface:**

```go
type ContainerAdapter interface {
    // Start launches the agent environment and returns a handle
    Start(ctx context.Context, opts AgentRunOpts) (ContainerHandle, error)

    // Stop gracefully stops the environment
    Stop(ctx context.Context, handle ContainerHandle) error

    // Kill force-stops the environment
    Kill(ctx context.Context, handle ContainerHandle) error

    // Status returns the current state
    Status(ctx context.Context, handle ContainerHandle) (ContainerStatus, error)

    // Logs returns a stream of container output
    Logs(ctx context.Context, handle ContainerHandle) (io.ReadCloser, error)

    // Exec runs a command inside the running environment
    Exec(ctx context.Context, handle ContainerHandle, cmd []string) (ExecResult, error)
}

type AgentRunOpts struct {
    Project       string
    Agent         string
    Mode          string            // "audit", "implement", "shell"
    WorkspacePath string            // path to workspace/{agent}/
    EnvFile       string            // path to .env
    PromptFile    string            // path to current_prompt.md
    Interactive   bool              // true for ateam shell
    Resources     ResourceLimits    // cpus, memory, pids
}
```

**Docker adapter** (default): builds/pulls the image, runs a single container with the bind mounts described above. Handles `docker run`, `docker kill`, `docker logs`, etc.

**Compose adapter**: for projects that need multiple services (e.g., agent container + external PostgreSQL + Redis + Nginx). Uses a `docker-compose.yml` in the project directory. The adapter calls `docker-compose up -d` for infrastructure services and `docker-compose run agent` for the agent itself. This enables multiple agents to share the same database service.

**Custom adapter** (`docker_run.sh`): the project's `docker_run.sh` script is actually an escape hatch — if it exists, the CLI invokes it instead of the built-in adapter. This lets projects with unusual requirements (GPU access, host networking, custom runtimes) define their own launch logic while still integrating with the rest of the system (the script receives the same arguments and must produce the same container naming convention).

The adapter is selected per-project in `config.toml`:

```toml
[docker]
adapter = "docker"            # "docker" (default), "compose", "script"
dockerfile = "./Dockerfile"
# compose_file = "./docker-compose.yml"   # for adapter = "compose"
# run_script = "./docker_run.sh"          # for adapter = "script"
```

### 7.5 Shared vs Isolated Agent Environments

By default, each agent gets its own `workspace/{agent}/data/` directory — fully isolated databases, caches, and state. This is the safe default: the testing agent can't corrupt the refactor agent's database.

For projects where agents should share infrastructure (e.g., all agents use the same PostgreSQL instance with the same schema and test data), two patterns work:

**Pattern A: Symlinked data directories.** Simple, no compose needed:
```bash
# All agents share testing's database
ln -s workspace/testing/data workspace/refactor/data
ln -s workspace/testing/data workspace/security/data
```
The agents run in separate containers but their `/data` mounts point to the same host directory. Caution: don't run agents concurrently if they write to the same database.

**Pattern B: Compose with shared services.** The compose adapter runs PostgreSQL as a shared service:
```yaml
services:
  db:
    image: postgres:16
    volumes:
      - ./workspace/shared/data/pg_data:/var/lib/postgresql/data
  testing:
    build: .
    volumes:
      - ./workspace/testing/code:/workspace:rw
    depends_on: [db]
  refactor:
    build: .
    volumes:
      - ./workspace/refactor/code:/workspace:rw
    depends_on: [db]
```
Each agent has its own code worktree but connects to the same database. The scheduler still enforces single-instance-per-agent, preventing concurrent writes.

### 7.6 The `.env` Pattern

API keys, database credentials, and port assignments live in `{project}/.env`:

```bash
# projectx/.env — gitignored, manually created
DATABASE_URL=postgresql://ateam:ateam@localhost:5432/tripmanager
ANTHROPIC_API_KEY=sk-ant-...
GOOGLE_MAPS_API_KEY=AIza...
NODE_ENV=development
PORT=3000
```

This file is:
- **Gitignored** by both the project repo and `.ateam/`.
- **Mounted read-only** into every agent container at `/workspace/.env`.
- **Shared** across all agents in the same project (same credentials, same database URL).
- **Not sent** to the LLM — it's environment variables, not prompt content.

Agent-specific overrides (e.g., different ports to avoid conflicts) can be placed in `{project}/.env.{agent}`:
```bash
# projectx/.env.testing — overrides for the testing agent
PORT=3001
DATABASE_URL=postgresql://ateam:ateam@localhost:5432/tripmanager_test
```

The container adapter merges `.env` and `.env.{agent}` (agent-specific wins).

### 7.7 Sub-Agent Execution: `--dangerously-skip-permissions` + `--output-format stream-json`

Sub-agents run inside Docker with two critical flags:

**`--dangerously-skip-permissions`:** Bypasses all Claude Code permission prompts, enabling fully unattended operation. This is safe because:
- The container is isolated via Docker (filesystem, process, network).
- The egress firewall restricts network access to only the LLM API and essential services.
- The worktree is a git branch — damage is limited and reversible via `git reset`.
- The coordinator validates all outputs before merging.

**`--output-format stream-json`:** Streams structured JSON events in real-time as the agent works. This enables:
- **Live observation:** The coordinator (or a monitoring tool) can watch what the sub-agent is doing in real-time — which files it's reading, what commands it's running, what it's thinking.
- **Token tracking:** The stream includes usage metadata (input/output tokens per turn).
- **Progress estimation:** The coordinator can detect if the agent is stuck (no events for N minutes) and kill the container.
- **Structured output:** Instead of the sub-agent writing its own markdown report, the coordinator (or a cheap model like Haiku) can post-process the stream-json into a clean report. This is more reliable than asking the sub-agent to format its own output, and it captures the full reasoning trace.

**Execution flow (default Docker adapter):**

```bash
#!/bin/bash
# Simplified view of what the Docker adapter does internally.
# In practice this is Go code in the adapter, not a shell script.

PROJECT="$1"
AGENT="$2"
ORG_ROOT="$3"
WORKSPACE="$ORG_ROOT/$PROJECT/workspace/$AGENT"
CODE_DIR="$WORKSPACE/code"
DATA_DIR="$WORKSPACE/data"
ARTIFACTS_DIR="$WORKSPACE/artifacts"
PROMPT_FILE="$ORG_ROOT/$PROJECT/$AGENT/current_prompt.md"
OUTPUT_DIR="$ORG_ROOT/$PROJECT/$AGENT/work/$(date +%Y-%m-%d_%H%M)"

mkdir -p "$DATA_DIR" "$ARTIFACTS_DIR" "$OUTPUT_DIR"

# Refresh persistent worktree to latest main (preserves untracked: node_modules, etc.)
cd "$CODE_DIR" && git fetch origin && git reset --hard origin/main

# Run sub-agent in Docker with persistent bind mounts
docker run --rm \
  --name "ateam-$PROJECT-$AGENT" \
  --cap-add NET_ADMIN --cap-add NET_RAW \
  --cpus=2 --memory=4g --pids-limit=256 \
  -v "$CODE_DIR:/workspace:rw" \
  -v "$DATA_DIR:/data:rw" \
  -v "$ARTIFACTS_DIR:/artifacts:rw" \
  -v "$ORG_ROOT/$PROJECT/.env:/workspace/.env:ro" \
  -v "$PROMPT_FILE:/agent-data/current_prompt.md:ro" \
  -v "$HOME/.claude:/home/node/.claude:ro" \
  -v "$OUTPUT_DIR:/output:rw" \
  --env-file "$ORG_ROOT/$PROJECT/.env" \
  "$PROJECT_DOCKER_IMAGE" \
  bash -c '
    # Initialize firewall (allowlist only)
    sudo /usr/local/bin/init-firewall.sh

    # Start services (if entrypoint handles it, e.g., PostgreSQL from /data/pg_data)
    /usr/local/bin/start-services.sh 2>/output/services.log || true

    # Run Claude Code: one-shot, no permissions, streaming JSON
    claude -p "$(cat /agent-data/current_prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      2>/output/stderr.log \
      | tee /output/stream.jsonl
  '

EXIT_CODE=$?

# Post-process: coordinator or Haiku generates the report (see §7.8)
echo "$EXIT_CODE" > "$OUTPUT_DIR/exit_code"
```

### 7.8 Report Generation from Stream JSON

Instead of relying on the sub-agent to write its own markdown report (which wastes context window and can be inconsistent), the stream-json output captures the agent's full execution trace. The coordinator then generates the report:

**Option A (cheapest):** The CLI's `format-report` subcommand extracts key events from `stream.jsonl` (tool calls, file edits, test results, final assistant messages) and templates them into a structured markdown report. Pure code, no LLM needed.

**Option B (better quality):** The `format_report` tool sends the extracted events to a cheap model (Haiku) with a formatting prompt: "Summarize this agent execution into an audit report following this template: [...]". This produces cleaner, more insightful reports at minimal cost (~$0.01 per report).

**Option C (hybrid):** Use Option A for the raw structured data (findings, test results, code changes) and Option B for the narrative summary. This gives you machine-readable data plus human-readable prose.

The sub-agent's prompt is simplified: instead of "write a report to /output/report.md following this template," it just says "analyze the codebase for testing gaps" or "implement these approved changes." The agent focuses on the work, not on report formatting.

### 7.9 Network Policy

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

### 7.10 Resource Limits

Containers are constrained via Docker resource flags:
- `--cpus`: default 2, configurable per project.
- `--memory`: default 4GB, configurable.
- `--pids-limit`: prevent fork bombs (default 256).
- `--cap-add NET_ADMIN --cap-add NET_RAW`: required for iptables firewall setup.
- Coordinator watchdog monitors `stream.jsonl` output — kills containers if no events for configurable timeout (default 5 minutes).

---

## 8. Coordinator Agent

### 8.1 Implementation

The coordinator is **Claude Code** invoked via `claude -p` with a system prompt describing the `ateam` CLI. It uses its native Bash tool to call CLI commands, its Read/Write tools to inspect reports and update changelogs, and its reasoning capabilities to make decisions.

**Interactive mode:** The human runs Claude Code and tells it what to do. The coordinator system prompt can be loaded from `.ateam/coordinator_role.md` or injected inline:
```bash
claude -p "$(cat .ateam/coordinator_role.md)

Run the testing agent on myapp and review the report."
```

**Autonomous mode:** A thin scheduler (cron, systemd timer, or the Go-based `ateam daemon`) periodically invokes:
```bash
claude -p "$(cat .ateam/coordinator_role.md)

Run the ATeam night cycle for project myapp." \
  --dangerously-skip-permissions \
  --max-budget-usd 5.00 \
  --output-format stream-json \
  2>> /var/log/ateam/coordinator.log
```

The coordinator uses the CLI via bash, not MCP. It calls commands like:

```bash
# Check state
ateam status -p myapp --json
ateam reports -p myapp --decision pending --json

# Run agents
ateam run -a testing -p myapp
ateam run -a security -p myapp --mode audit

# Review
cat myapp/testing/work/2026-02-26_2300_AUTH_COVERAGE.report.md

# Decide
ateam db "UPDATE reports SET decision='proceed', notes='clean findings, low risk' WHERE id=14"

# Implement
ateam run -a testing -p myapp --mode implement --report testing/work/...

# Track
ateam budget -p myapp --json
```

The `--json` flag gives the coordinator structured output to reason about. Claude Code's native Read/Write/Grep tools handle file inspection and changelog updates. No framework code needed for decision heuristics — Claude Code does the reasoning.

### 8.2 Scheduler

The scheduler is either external (cron/systemd) or built into the Go binary (`ateam daemon`).

**Option A: External scheduler (simplest)**
```bash
# crontab
# Night cycle at 11pm
0 23 * * * cd /home/user/org && claude -p "$(cat .ateam/coordinator_role.md) Run night cycle for myapp." --dangerously-skip-permissions --max-budget-usd 5.00

# Commit check every 15 minutes during day
*/15 9-17 * * 1-5 cd /home/user/org && ateam run -a testing -p myapp --if-new-commits
```

**Option B: Built-in scheduler**
```bash
ateam daemon -p myapp
# Reads [schedule] from config.toml
# Checks for commits at configured interval
# Invokes coordinator Claude Code when work is needed
# Enforces budget limits before launching
# Runs in foreground (use systemd/launchd for background)
```

Schedule profiles in `config.toml`:

```toml
[schedule]
timezone = "America/Los_Angeles"
commit_check_interval_minutes = 15

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

The coordinator's decision logic is in its system prompt (§14.1), not in framework code. But the general flow is:

```
on new_commits (detected by scheduler or --if-new-commits flag):
    1. ALWAYS run testing agent first
    2. If tests fail → stop, notify human (or fix test bugs if simple)
    3. If tests pass → check which other agents are stale (commits behind)

on schedule_trigger (night cycle):
    1. ateam status --json → assess all agent states
    2. ateam reports --decision pending --json → triage unreviewed reports
    3. ateam budget --json → check remaining budget
    4. Run agents per priority: testing → quality → security → others
    5. For each completed agent: review report, decide, update reports table
    6. Auto-approve low-risk changes (test additions, doc updates, lint fixes)
    7. Flag high-risk for human review (decision='ask')
    8. After implementations: re-run testing to verify
    9. Update changelog.md

on human_override (via ateam pause/resume/run):
    Scheduler respects agent status in ateam.sqlite.
    Paused agents are skipped. Manual runs just work.
```

### 8.4 Cost Control for Coordinator

The coordinator itself consumes tokens. With `--max-budget-usd`, Claude Code enforces a hard cap per invocation. The scheduler should set this based on remaining daily budget:

```bash
# Pseudo-logic in ateam daemon
remaining=$(ateam budget -p myapp --json | jq '.daily_remaining')
coordinator_budget=$(echo "$remaining * 0.25" | bc)  # reserve 25% for coordinator
agent_budget=$(echo "$remaining * 0.75" | bc)         # 75% for agents
claude -p "..." --max-budget-usd "$coordinator_budget"
```

---

## 9. Resource Monitoring and Cost Control

### 9.1 Budget Enforcement via `--max-budget-usd`

Claude Code's `--max-budget-usd` flag is the primary cost control mechanism. It enforces a hard cap per invocation — when the budget is exhausted, Claude Code stops. The CLI passes this flag to every `claude -p` invocation:

```bash
claude -p "$(cat prompt.md)" \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --max-budget-usd 2.00
```

**Budget hierarchy (config.toml):**

```toml
[budget]
api_key_env = "ANTHROPIC_API_KEY"     # env var or set in .env
max_budget_per_run = 2.00             # USD per agent invocation
max_budget_daily = 20.00              # USD across all agents, all projects
max_budget_monthly = 200.00           # USD hard monthly cap
coordinator_budget_pct = 25           # % of remaining daily budget for coordinator
model = "sonnet"                      # default model for sub-agents
coordinator_model = "sonnet"          # model for coordinator
```

**CLI enforcement before every launch:**

1. Query `operations` table: sum costs for current day and month
2. If daily or monthly limit exceeded → refuse to launch, log warning
3. Calculate per-run budget: `min(max_budget_per_run, daily_remaining / estimated_remaining_runs)`
4. Pass `--max-budget-usd {calculated}` to `claude -p`
5. On completion: parse actual cost from stream-json, record in operations table

### 9.2 Cost Tracking (Best-Effort)

Stream-json output includes usage metadata that may contain token counts and cost data. The CLI parses this and records it in the `operations` table:

```sql
INSERT INTO operations (project, agent_name, operation, notes)
VALUES ('myapp', 'testing', 'complete',
  '{"exit_code": 0, "cost_usd": 1.23, "duration_s": 420, "report": "testing/work/..."}');
```

Cost tracking is best-effort — the actual cost enforced by `--max-budget-usd` is the reliable control. Tracked costs are used for reporting and trend analysis. The `ateam budget` command shows:

```
Project: myapp
  Today:  $8.42 / $20.00 (42%)  │  7 runs
  Month:  $67.30 / $200.00 (34%) │  89 runs
  Last run: testing audit — $1.23, 7 min
```

### 9.3 Throttling Tiers

| Budget Used | Behavior |
|---|---|
| 0–75% | Normal operation — all agents eligible |
| 75–95% | Reduced: only high-priority tasks (testing, security), 1 agent at a time |
| 95–100% | Minimal: only testing agent (keep the build green) |
| 100%+ | All agents paused, human notified via changelog.md |

### 9.4 Container Watchdog

The CLI monitors running containers via the stream-json output:

- If no stream-json events for `watchdog_timeout_minutes` (default: 5) → container is stuck → kill it.
- If container exceeds `max_agent_runtime_minutes` (default: 60) → kill regardless.
- On kill: record `operation='error'` with notes in operations table, set agent status to `'error'`.

```toml
[resources]
max_concurrent_agents = 4             # Docker concurrency limit
max_agent_runtime_minutes = 60        # hard kill after this
watchdog_timeout_minutes = 5          # kill if no progress for this long
cpus_per_agent = 2                    # Docker --cpus
memory_per_agent = "4g"               # Docker --memory
```

### 9.5 API Key vs Subscription Mode

Sub-agents and the coordinator can run in two modes:

- **API key mode** (recommended for autonomous operation): Set `ANTHROPIC_API_KEY` in `.env`. Enables `--max-budget-usd` for hard cost caps. Costs are per-token, visible in stream-json output. Best for scheduled/unattended runs.

- **Subscription mode**: Claude Code uses `~/.claude` mount for authentication against a Pro/Max subscription. No API key needed. `--max-budget-usd` may not apply (subscription-based billing). Best for interactive sessions (`ateam shell`).

The CLI auto-detects: if `ANTHROPIC_API_KEY` is set (in environment or `.env`), it passes it to the container and enables budget enforcement. Otherwise, it mounts `~/.claude` and logs a warning that budget caps may not be enforced.

---

## 10. Debugging and Operations

### 10.1 CLI Context: Directory-Aware Commands

The `ateam` CLI infers organization, project, and agent context from the current working directory:

```
ORG_ROOT_DIR/                        # organization root (has .ateam/)
  .ateam/
    ateam.sqlite                     # org-wide state database
    coordinator_role.md
    agents/
    knowledge/
  projectx/                          # project dir (has config.toml)
    config.toml
    testing/                         # agent dir
      knowledge.md
      work/
    refactor/
      ...
  projecty/
    config.toml
    ...
```

**Resolution rules:**

1. **Inside `projectx/testing/`** → org=ORG_ROOT_DIR, project=projectx, agent=testing. Commands apply to the testing agent only.
2. **Inside `projectx/`** → org=ORG_ROOT_DIR, project=projectx, no agent. Commands apply to all agents / the coordinator for this project.
3. **Inside `ORG_ROOT_DIR/`** (but not in a project) → org=ORG_ROOT_DIR, no project, no agent. Commands apply org-wide (e.g., `ateam status` shows all projects).
4. **Anywhere else** → requires explicit flags.

The CLI walks up from `$CWD` looking for `.ateam/` (identifies org root), then checks if the current path is inside a subdirectory containing `config.toml` (identifies project), then checks if the current directory basename matches an enabled agent name (identifies agent).

**CLI flag conventions:**

```
ateam [global-options] <command> [command-options]

Context overrides:
  --org PATH                  Organization root directory
  -p NAME, --proj NAME,
    --project NAME            Project name (subdirectory of org root)
  -a NAME, --agent NAME       Agent name
  --coordinator, -coord       Target the coordinator (not an agent)
```

```bash
# These are all equivalent:
cd ~/org/projectx/testing && ateam pause
cd ~/org/projectx && ateam pause -a testing
cd ~/org && ateam pause -p projectx -a testing
ateam --org ~/org pause -p projectx -a testing

# These are all equivalent (project-wide):
cd ~/org/projectx && ateam pause
cd ~/org && ateam pause -p projectx
ateam --org ~/org pause -p projectx

# Org-wide status:
cd ~/org && ateam status
# Shows all projects, all agents, from the single ateam.sqlite
```

**The coordinator uses the same CLI.** It calls `ateam` CLI commands via its Bash tool. The CLI reads and writes `.ateam/ateam.sqlite`. This means developers and the coordinator always have the same view of the system — there's no separate coordinator state, no lockfiles, no divergent data paths.

### 10.2 State Database: SQLite

A single `ateam.sqlite` lives in `.ateam/` at the organization root. This is the sole source of truth for all runtime state across all projects. Both autonomous coordinators (one per project, via `claude -p`) and developers (directly via CLI) read and write the same database.

```sql
CREATE TABLE agents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project         TEXT NOT NULL,         -- project name (directory basename, e.g., 'myapp')
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
    UNIQUE(project, agent_name)
);

CREATE TABLE operations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project         TEXT NOT NULL,         -- project name
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
    project         TEXT NOT NULL,
    agent_name      TEXT NOT NULL,         -- which agent produced this report
    timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
    report_path     TEXT NOT NULL,         -- relative path from project root:
                                          --   testing/work/2026-02-26_2300_auth_coverage.report.md
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

CREATE INDEX idx_agents_project ON agents(project);
CREATE INDEX idx_operations_project ON operations(project);
CREATE INDEX idx_reports_project ON reports(project);
CREATE INDEX idx_reports_decision ON reports(project, decision);
```

**Design notes:**

The `agents` table holds current state — one row per enabled agent per project, upserted on first use. The `operations` table is an append-only audit log of every state transition. The `reports` table tracks the coordinator's analysis and decisions on agent-produced reports. Together they answer "what's happening now?" (agents), "what happened?" (operations), and "what did the coordinator decide?" (reports).

**Why one database for the entire org?** Multiple coordinators (one per project) and human CLI commands can all access the same database concurrently. SQLite in WAL mode supports this naturally: unlimited concurrent readers, and write transactions are very short (a single UPDATE or INSERT), so writers just wait briefly for the lock. No coordinator blocks another for any meaningful duration.

This makes cross-project queries trivial:
```sql
-- What's running across all projects right now?
SELECT project, agent_name, status, time_started FROM agents WHERE status='running';

-- Budget across all projects this week
SELECT project, COUNT(*) as runs, SUM(CAST(json_extract(notes, '$.cost') AS REAL)) as cost
  FROM operations WHERE operation='complete' AND timestamp > date('now', '-7 days')
  GROUP BY project;

-- Which projects have pending reports needing human review?
SELECT project, agent_name, timestamp, notes FROM reports WHERE decision='ask';
```

The `project` column stores the project directory name (e.g., `'myapp'`), not an absolute path. The CLI resolves the actual filesystem path from the org root + project name. This keeps the database portable — moving the org directory doesn't break it.

The `reason` field distinguishes coordinator-initiated runs from manual ones. When the scheduler starts a testing audit, `reason='coordinator'`. When a developer runs `ateam shell`, `reason='manual'`.

Git commit tracking (`git_commit_start`, `git_commit_end`) records the codebase state the agent worked against. This enables questions like "which commit did the security agent last audit?" and "did the refactor agent's changes include recent commits?"

`operations.docker_instance` captures the container ID for each state transition. This is critical for post-mortem debugging: correlate the operation with `docker logs {container_id}` (if the container still exists) or at minimum know which container execution you're investigating.

**The `reports` table** is the coordinator's decision log in structured form. Report paths follow the naming convention `{agent}/work/{date}_{time}_{DESCRIPTIVE_NAME}.report.md` with a matching `.actions.md` if the coordinator acted on it (see §12). The decision field captures outcomes: `pending`, `ignore`, `proceed`, `ask`, `implemented`, `deferred`, `blocked`.

**Why SQLite:**
- Single file, zero setup, no daemon, no lockfiles.
- WAL mode: unlimited concurrent readers + brief exclusive writes. Multiple coordinators and CLI sessions coexist safely.
- Trivially inspectable: `sqlite3 .ateam/ateam.sqlite "SELECT project, agent_name, status FROM agents"`.
- The `ateam db` command opens a sqlite3 shell for ad-hoc queries.
- `.gitignore`d because the data is transient runtime state, not configuration.

**The coordinator calls the CLI.** The coordinator (Claude Code) uses the same `ateam` commands as developers:

```
subagent_run_audit("testing", "myapp")
  → internally: ateam -p myapp run -a testing --mode audit
  → CLI updates ateam.sqlite: INSERT INTO operations, UPDATE agents SET status='running'
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

ateam -p projectx pause -a testing -a security
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
ateam -p projectx shell -a testing

# This:
# 1. Checks ateam.sqlite: is testing idle/suspended/stopped/error?
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
ateam shell --with-tools
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

Every CLI command is a composition of three data sources: **SQLite** (ateam.sqlite), **git** (worktrees, refs, HEAD), and **Docker** (containers, inspect). This section documents exactly what each command reads and writes.

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
READ  file    cat {project}/changelog.md
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

Starts an agent run (audit by default). Used by both humans and the coordinator (via Bash tool).

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

# ── On completion (monitored by CLI) ──────────────

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

#### `ateam shell [--with-tools]`

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
│ SQLite (ateam.sqlite)                                               │
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
ateam -p projectx status
ateam -p projectx reports --decision pending
```

---

## 11. CLI Reference

The `ateam` CLI is the single interface for both humans and the coordinator. Every operation — starting agents, pausing, inspecting state, reviewing reports — goes through these commands. The coordinator calls the same CLI via its Bash tool, so the database state is always consistent regardless of who initiated the action.

This means **the CLI is usable before the coordinator exists.** During Phase 1 development (and for ongoing manual use), a developer can drive the entire system from the terminal: run audits, review reports, implement findings, update knowledge — all without the autonomous scheduler.

### 11.1 Global Options

```
ateam [global-options] <command> [command-options]

Global Options:
  --org PATH                Organization root (overrides CWD-based detection)
  -p NAME, --proj NAME,
    --project NAME          Project name (overrides CWD-based detection)
  -a NAME, --agent NAME     Agent name (overrides CWD-based detection)
  -coord, --coordinator     Target the coordinator (not an agent)
  --json                    Machine-readable JSON output (for scripting / MCP)
  --verbose, -v             Show additional detail (SQL queries, docker commands)
  --dry-run                 Show what would happen without executing
  --help, -h                Show help for any command
```

**Directory context** (see §10.1): the CLI walks up from `$CWD` looking for `.ateam/` (org root), then checks if CWD is inside a subdirectory with `config.toml` (project), then checks if the directory basename matches an enabled agent name. All levels can be overridden with explicit flags.

### 11.2 Organization & Project Setup

#### `ateam install`

Initialize a new organization root — creates the `.ateam/` directory with the org-wide database, default agent prompts, and the knowledge base.

```
ateam install [PATH]

PATH defaults to current directory.

Creates:
  PATH/.ateam/
    .git/                         Git repo for org-level config files
    .gitignore                    Ignores ateam.sqlite
    ateam.sqlite                  Org-wide SQLite database (WAL mode)
    coordinator_role.md           Default coordinator prompt
    agents/                       Default agent role prompts
      testing/role.md
      refactor/role.md
      security/role.md
      performance/role.md
      deps/role.md
      docs-internal/role.md
      docs-external/role.md
    knowledge/                    Tech-stack culture files (starter set)
      golang.md
      typescript.md
      python.md
      postgresql.md
      docker.md
      react.md
      testing_jest.md
      testing_pytest.md
      security_owasp.md
      linting_eslint.md
      linting_ruff.md

Commits the initial state to .ateam/.git
```

Example:
```bash
mkdir ~/my_org && cd ~/my_org
ateam install
# Created .ateam/ with database, 7 agent roles, 11 knowledge files
```

#### `ateam init`

Initialize a new project within an existing organization. Must run inside a directory containing `.ateam/`.

```
ateam init PROJECT_NAME [options]

Creates:
  PROJECT_NAME/
    .git/                         Project-level git repo (for config, reports, changelogs)
    config.toml                   Project configuration (from template)
    Dockerfile                    Build environment for sub-agent containers
    docker_run.sh                 Container launch helper
    project_goals.md              Project-specific instructions (empty template)
    changelog.md                  Coordinator decision log (empty)
    testing/                      Agent directories (knowledge + work only)
      knowledge.md
      work/
    refactor/
      knowledge.md
      work/
    ... (one per enabled agent)

  Agent directories do NOT get role.md — they inherit from
  .ateam/agents/{agent}/role.md. Create a local role.md
  (full override) or role_add.md (additive) only when needed.

Options:
  --git URL               Project source repo URL (bare clone set up)
  --stack LIST            Tech stack (e.g., typescript,react,postgresql)
  --agents LIST           Agents to enable (default: all 7)
  --template PATH         Custom config.toml template

Database writes:
  INSERT INTO agents (project, agent_name, status='idle')
    for each enabled agent
```

Example:
```bash
cd ~/my_org
ateam init myapp --git git@github.com:org/myapp.git --stack typescript,react,postgresql
# Created myapp/ with config, 7 agent directories
# Registered 7 agents in .ateam/ateam.sqlite
```

#### `ateam doctor`

Health check: verifies all dependencies and state consistency.

```
ateam doctor [--fix]

Checks:
  ✓ .ateam/ exists with valid ateam.sqlite
  ✓ Docker daemon running and accessible
  ✓ Git installed and accessible
  ✓ Claude Code CLI installed (claude --version)
  ✓ SQLite schema is current
  ✓ All registered projects have config.toml
  ✓ Agent directories match config.toml enabled lists
  ✓ No stale containers (SQLite says running, Docker disagrees)
  ✓ No orphan containers (Docker running, SQLite doesn't know)
  ✓ No orphan worktrees
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
  - Agent status must NOT be 'running' or 'interactive' (in ateam.sqlite)
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
ateam -p myapp run -a testing --mode audit
```

#### `ateam shell`

Start an interactive Claude Code session inside the agent's Docker environment.

```
ateam shell [--agent NAME] [--with-tools]

Launches the same Docker container as `ateam run` but with interactive
Claude Code instead of `claude -p`. Same prompt, same worktree, same volumes.

Options:
  --agent NAME          Agent to run as (inferred from CWD)
  --with-tools            Also make the ateam CLI available inside the Claude Code
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
ateam shell --with-tools
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

No arguments:  opens interactive sqlite3 shell on ateam.sqlite
With QUERY:    executes the query and prints results

Examples:
  ateam db
  ateam db "SELECT agent_name, status FROM agents"
  ateam db ".schema"
  ateam db "SELECT * FROM reports WHERE decision='ask'"
```

#### `ateam update-org-knowledge`

Aggregate project-level knowledge into org-level knowledge files. This is the mechanism that keeps `.ateam/knowledge/` and `.ateam/agents/` current based on what agents have learned across all projects.

```
ateam update-org-knowledge [options]

Options:
  --git-commit            Commit changes to .ateam/.git after updating
  --roles ROLE1,ROLE2     Only update for specific roles (default: all)
  --dry-run               Show what would change without writing
  --projects P1,P2        Only harvest from specific projects (default: all)

Process (for each role):
  1. Discovers all projects that have this role enabled
  2. Collects knowledge sources for this role across projects:
     - {project}/{role}/knowledge.md     (accumulated agent knowledge)
     - {project}/{role}/work/*.report.md (recent reports, for patterns)
     - {project}/{role}/role_add.md      (project-specific additions that
                                          might indicate missing org defaults)
  3. Spawns a "culture maintainer" agent (Claude Code in Docker) with:
     - All collected knowledge files as input
     - The current org-level files as baseline:
       - .ateam/agents/{role}/role.md
       - .ateam/knowledge/{relevant stack files}
     - A prompt instructing it to:
       a. Identify patterns that appear across multiple projects
       b. Extract broadly useful knowledge into org-level files
       c. Deduplicate: remove from org files anything project-specific
       d. Update stack knowledge files (e.g., typescript.md) with
          patterns learned from TypeScript projects
       e. Propose role.md improvements if agents consistently need
          the same role_add.md overrides
  4. Reviews the proposed changes (diff against current org files)
  5. If --git-commit: commits to .ateam/.git with a summary message

Output:
  Shows a diff of proposed changes to .ateam/ files.
  With --git-commit: commits and shows the commit hash.
  Without: writes changes but leaves them uncommitted for review.

Database writes:
  INSERT INTO operations (project=NULL, agent_name='culture-maintainer',
    operation='complete', notes='updated {N} org files for roles: {roles}')
```

Example:
```bash
# Update all org knowledge, review before committing
ateam update-org-knowledge
# Review changes: git -C .ateam diff
# Happy with it:
git -C .ateam add -A && git -C .ateam commit -m "knowledge update"

# Or let it commit directly
ateam update-org-knowledge --git-commit

# Only update testing and security knowledge
ateam update-org-knowledge --roles testing,security --git-commit

# Schedule it (cron, every few days)
# 0 6 */3 * * cd /home/user/my_org && ateam update-org-knowledge --git-commit
```

**What the culture maintainer agent produces:**

The agent doesn't blindly merge files. It synthesizes. For example:

- Three TypeScript projects all have `knowledge.md` entries about "prefer Zod for runtime validation" → the agent adds this to `.ateam/knowledge/typescript.md` (if not already there).
- Two projects have `security/role_add.md` saying "check for hardcoded AWS credentials" → the agent proposes adding this to `.ateam/agents/security/role.md` as a standard check, so future projects get it by default.
- One project's testing knowledge.md has a Jest-specific pattern for mocking timers → goes into `.ateam/knowledge/testing_jest.md`.
- Project-specific knowledge (like "our API rate limit is 100 req/s") stays in the project's knowledge.md and is NOT promoted to org level.

### 11.7 Manual Workflow Example (No Coordinator)

This shows how a developer would use the CLI to drive the full audit→review→implement cycle manually — the same workflow the coordinator automates.

```bash
# ── Setup ────────────────────────────────────────────────────────

mkdir ~/my_org && cd ~/my_org
ateam install                             # creates .ateam/ with db, roles, knowledge
ateam init myapp --git git@github.com:org/myapp.git --stack typescript,react
ateam doctor                              # verify everything is ready

# ── Run a testing audit ──────────────────────────────────────────

cd myapp/testing
ateam run                                 # starts audit in Docker container
ateam status                              # watch progress + live stream-json
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

ateam shell --with-tools
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

This manual workflow maps 1:1 to what the coordinator does autonomously. When you're ready to hand off to the coordinator, just start the scheduler — it calls the same `ateam run`, reads the same `ateam.sqlite`, writes the same `reports` table. The only difference is that `reason` changes from `'manual'` to `'coordinator'` and the decisions are made by Claude Code instead of a human updating the reports table.

---

## 12. File Hierarchy

### 12.1 ATeam Framework Source

```
ateam/                                 # Go module
  cmd/
    ateam/
      main.go                          # CLI entry point (cobra)
  internal/
    cli/                               # command implementations
      run.go, status.go, reports.go,
      install.go, init.go, daemon.go, ...
    adapter/                           # container adapter interface + impls
      adapter.go                       # ContainerAdapter interface
      docker.go                        # Docker adapter (default)
      compose.go                       # Docker Compose adapter
      script.go                        # Script adapter (docker_run.sh)
    git/                               # worktree management
    db/                                # SQLite operations + migrations
    prompt/                            # prompt builder (layered assembly)
    config/                            # config.toml parsing
    budget/                            # cost tracking and enforcement
  embed/                               # compiled into binary via embed.FS
    agents/                            # default role.md files
    knowledge/                         # default knowledge templates
    schema.sql                         # SQLite schema
    config.template.toml               # default config.toml template
  go.mod
  go.sum
```

### 12.2 Organization Workspace

The org root contains `.ateam/` (org-level config, database, agent defaults, knowledge) and one or more project directories. The `.ateam/` directory has its own git repo for version-controlling the org-level files. Each project directory has its own git repo for version-controlling project-specific config, reports, and changelogs.

```
ORG_ROOT_DIR/                          # organization root
  .ateam/                              # org-level — managed by `ateam install`
    .git/                              # git repo for org-level files
    .gitignore                         # ignores ateam.sqlite
    ateam.sqlite                       # state of ALL agents, ALL projects
                                       #   (not version-controlled — transient state)

    coordinator_role.md                # base coordinator prompt

    agents/                            # default agent role prompts
      testing/                         #   (shared by all projects unless overridden)
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

    knowledge/                         # tech-stack culture (cross-project)
      golang.md                        #   Go conventions, patterns, pitfalls
      typescript.md                    #   TS/JS conventions, libraries
      python.md                        #   Python conventions, tooling
      postgresql.md                    #   PostgreSQL patterns, optimization
      docker.md                        #   Dockerfile best practices
      react.md                         #   React patterns, state management
      cli_fd.md                        #   notes on using fd
      cli_ripgrep.md                   #   notes on using ripgrep
      cli_jq.md                        #   notes on using jq
      testing_jest.md                  #   Jest patterns and conventions
      testing_pytest.md                #   Pytest patterns and conventions
      security_owasp.md               #   OWASP top 10 patterns
      linting_eslint.md               #   ESLint preferred configs
      linting_ruff.md                  #   Ruff preferred configs
      # ... files named after what they describe
      # prompt builder picks only files matching project's stack

  PROJECT_NAME/                        # per-project — managed by `ateam init`
    .git/                              # project-level git repo
    .gitignore                         # ignores workspace/, .env, current_prompt.md
    config.toml                        # project config (agents, schedule, budget, stack)
    Dockerfile                         # build environment for sub-agent containers
    docker_run.sh                      # custom container launch (optional, adapter="script")
    docker-compose.yml                 # multi-service setup (optional, adapter="compose")
    .env                               # API keys, ports, credentials (gitignored, manual)
    .env.testing                       # agent-specific overrides (optional, gitignored)
    project_goals.md                   # project-specific instructions and priorities
    changelog.md                       # coordinator decision log (narrative)

    repos/
      bare/                            # bare clone of project source repo

    testing/                           # agent config directory (git-versioned)
      knowledge.md                     #   accumulated project knowledge
      current_prompt.md                #   latest assembled prompt (gitignored)
      work/
        2026-02-26_2300_AUTH_COVERAGE.report.md    # descriptive report name
        2026-02-26_2300_AUTH_COVERAGE.actions.md   # what was done about it

    refactor/                          # inherits role from .ateam/agents/refactor/role.md
      knowledge.md
      work/

    security/
      role_add.md                      # ADDS to org-level security/role.md
      knowledge.md
      work/

    performance/
      role.md                          # REPLACES org-level performance/role.md entirely
      knowledge.md
      work/

    deps/
      knowledge.md
      work/

    docs-internal/
      knowledge.md
      work/

    docs-external/
      knowledge.md
      work/

    workspace/                         # gitignored — persistent agent working environments
      testing/
        code/                          #   git worktree (persistent, bind-mounted rw)
          node_modules/                #     survives between runs
          .next/                       #     build cache survives
        data/                          #   persistent state (databases, caches)
          pg_data/                     #     PostgreSQL data directory
        artifacts/                     #   build outputs, coverage reports
      refactor/
        code/                          #   separate worktree (can diverge)
        data/                          #   own database state (isolated)
        artifacts/
      security/
        code/
        data/
        artifacts/
      feature/                         #   manual development agent
        code/
        data/
        artifacts/
```

### 12.3 Role Inheritance

The prompt builder assembles agent prompts using a layered inheritance model:

```
Assembled prompt =
  1. .ateam/agents/{agent}/role.md          (org-level base role)
  2. {project}/{agent}/role_add.md          (if exists: appended to base)
     OR
  2. {project}/{agent}/role.md              (if exists: REPLACES base entirely)
  3. .ateam/knowledge/{matching files}      (tech-stack culture, based on project stack)
  4. {project}/{agent}/knowledge.md         (accumulated project-specific knowledge)
  5. {project}/project_goals.md             (project priorities)
  6. Mode-specific instructions + task context
```

**Three patterns:**

- **Default inheritance** (most common): No `role.md` or `role_add.md` in the project's agent directory. The org-level role is used as-is. This is the case for most agents in most projects.

- **Additive override** (`role_add.md`): The project has extra instructions that supplement the base role. Example: the security agent's base role covers general vulnerability patterns, but this project has specific concerns about its API key management. The `role_add.md` adds those instructions without replacing the base.

- **Full override** (`role.md`): The project's agent needs fundamentally different behavior. The local `role.md` completely replaces the org-level one. Example: a performance-critical project where the performance agent needs a completely different audit methodology.

If both `role.md` and `role_add.md` exist in the same agent directory, `role.md` wins (full override), and `role_add.md` is ignored. The prompt builder logs a warning.

### 12.4 Report Naming Convention

Reports in the `work/` directory use descriptive names:

```
{date}_{time}_{DESCRIPTIVE_NAME}.report.md
{date}_{time}_{DESCRIPTIVE_NAME}.actions.md
```

Examples:
```
2026-02-26_2300_AUTH_COVERAGE.report.md        # testing agent: auth module coverage gaps
2026-02-26_2300_AUTH_COVERAGE.actions.md        # what was implemented from that report
2026-02-27_0100_SQL_INJECTION_API.report.md     # security agent: SQL injection findings
2026-02-27_0200_DEAD_CODE_REMOVAL.report.md     # refactor agent: dead code analysis
2026-02-27_0200_DEAD_CODE_REMOVAL.actions.md    # the actual removals performed
```

The `.report.md` is produced by the agent during audit mode. The `.actions.md` is produced during implement mode (if the coordinator decided to act on the report). Both are git-versioned in the project repo. The `reports` table in `ateam.sqlite` tracks the mapping between report files and coordinator decisions.

### 12.5 Knowledge Files

The `.ateam/knowledge/` directory contains **on-demand knowledge files** organized by technology, tool, or pattern. These are not loaded into every agent's context — the prompt builder selects only the files relevant to the current project's stack (as declared in `config.toml`):

```toml
[project]
stack = ["typescript", "react", "postgresql", "docker", "testing_jest", "linting_eslint"]
```

The prompt builder then includes the matching knowledge files in the agent's prompt, keeping the context window lean. A Go project's security agent sees `golang.md` and `security_owasp.md`, not `react.md` or `testing_jest.md`.

**Knowledge flows in two directions:**

- **Down (org → project):** Org-level knowledge files are included in agent prompts via the prompt builder. This is how patterns learned from Project A benefit Project B.

- **Up (project → org):** Project-level `knowledge.md` files accumulate agent learnings during runs. The `ateam update-org-knowledge` command (§11.6) periodically harvests these files, identifies cross-project patterns, and promotes broadly useful knowledge to the org level.

**How knowledge files are populated:**

1. **Initial seeding:** `ateam install` creates a starter set with basic conventions for common stacks.
2. **Agent learning:** During maintain mode, agents update their project's `{agent}/knowledge.md` with patterns and decisions from recent runs.
3. **Org-level aggregation:** `ateam update-org-knowledge` (run on-demand or on a schedule) spawns a culture maintainer agent per role. This agent reads all project knowledge files for that role, identifies patterns appearing across multiple projects, and promotes them to org-level files. It also proposes role.md improvements when multiple projects use the same `role_add.md` overrides.
4. **Manual editing:** Developers can edit any knowledge file directly. Org-level files are in `.ateam/.git`, project-level in `{project}/.git`.

**What gets promoted vs what stays local:**

- "Prefer Zod for runtime validation in TypeScript" → promoted to `.ateam/knowledge/typescript.md` (broadly useful)
- "Our API rate limit is 100 req/s" → stays in project knowledge.md (project-specific)
- Two projects both add "check for hardcoded AWS credentials" to `security/role_add.md` → promoted into `.ateam/agents/security/role.md` as a default check

### 12.6 Git Structure: Two Repos

Each level has its own git repo to avoid write contention when multiple coordinators run concurrently:

- **`.ateam/.git`** — org-level files: agent roles, knowledge base, coordinator prompt. Changed infrequently. Only humans (or a dedicated maintain cycle) commit here.

- **`{project}/.git`** — project-level files: config.toml, reports, actions, changelogs, knowledge.md. Changed frequently by coordinators and agents. Each project's coordinator commits independently, so projects don't block each other.

The `ateam.sqlite` database is `.gitignore`d in `.ateam/` because it's transient runtime state, not configuration. The schema is recreatable from code.

### 12.7 Relationship to CLAUDE.md Files

ATeam's knowledge system complements — but does not replace — Claude Code's native `CLAUDE.md` mechanism:

```
Claude Code context (inside Docker) =
  ~/.claude/CLAUDE.md          (global preferences, mounted via ~/.claude)
  + /workspace/CLAUDE.md       (project CLAUDE.md, in the git worktree)
  + ATeam prompt               (role + knowledge + task)
```

`CLAUDE.md` files are maintained by human developers for their own Claude Code sessions. ATeam's `.ateam/agents/` roles and `.ateam/knowledge/` files are maintained for specialized agents. They coexist naturally because Claude Code reads `CLAUDE.md` automatically, and ATeam injects its context via the prompt.

**Cross-pollination:** Knowledge files can harvest useful patterns from one project's `CLAUDE.md` and make them available org-wide. For example, PostgreSQL optimization notes from Project A can be extracted into `.ateam/knowledge/postgresql.md` for all projects.

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
  {.ateam/agents/{agent_id}/role.md}           (org-level base role)
  {project/{agent_id}/role_add.md}             (if exists: appended to base)
  OR
  {project/{agent_id}/role.md}                 (if exists: REPLACES base)

  # Tech-Stack Knowledge (on-demand, based on project stack config)
  {.ateam/knowledge/typescript.md}             (if project stack includes typescript)
  {.ateam/knowledge/postgresql.md}             (if project stack includes postgresql)
  {.ateam/knowledge/testing_jest.md}           (if project stack includes testing_jest)
  # ... only matching files are included

  # Project Knowledge
  {project/{agent_id}/knowledge.md}            (accumulated from past runs)

  # Project Goals
  {project/project_goals.md}                   (project-level priorities)

  # Your Mission This Run
  {mode-specific instructions}
  {task context from coordinator or CLI}

  # Output Contract
  {what files to write to /output/}
```

The role prompts below are the `.ateam/agents/{agent_id}/role.md` files — the stable identity of each agent. Projects can add to them with `role_add.md` or fully override with a local `role.md` (see §12.3 for inheritance rules). Knowledge accumulated across runs lives in `{project}/{agent_id}/knowledge.md`.

---

### 14.1 Coordinator

**File:** `.ateam/agents/coordinator/role.md`

This prompt is used as the system prompt for the coordinator Claude Code instance (injected via `-p` flag). It runs on the host (not in Docker) and orchestrates sub-agents by calling `ateam` CLI commands via its Bash tool.

```markdown
# Coordinator — ATeam Project Orchestrator

You are the coordinator for an automated software quality system. You manage
a team of specialized agents that run in isolated Docker containers. Your job
is to decide what work needs doing, in what order, and whether results are
good enough to merge.

## Your Tools

You use the `ateam` CLI via your Bash tool. All commands read/write the
org-wide .ateam/ateam.sqlite database. Use --json for structured output
when you need to parse results.

Key commands:
  ateam status -p PROJECT --json       # all agents, commit freshness, reports
  ateam run -a AGENT -p PROJECT        # start an agent (--mode audit|implement)
  ateam reports -p PROJECT --json      # the decision pipeline
  ateam reports -p PROJECT --decision pending  # what needs your review
  ateam kill -a AGENT -p PROJECT       # stop a running agent
  ateam pause / resume                 # control scheduling
  ateam diff -a AGENT -p PROJECT       # see what an agent changed
  ateam budget -p PROJECT --json       # cost tracking
  ateam db "SQL QUERY"                 # direct database access

To record decisions on reports:
  ateam db "UPDATE reports SET decision='proceed', notes='...' WHERE id=N"
  ateam db "UPDATE reports SET decision='ask', notes='...' WHERE id=N"

You also have native Claude Code tools: Read, Write, Bash, Grep, Glob.
Use Read to inspect reports and code. Use Write to update changelog.md
and knowledge files. Use Bash for everything else.

## Decision Principles

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

**File:** `.ateam/agents/testing/role.md`

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

**File:** `.ateam/agents/quality/role.md`

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

**File:** `.ateam/agents/security/role.md`

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

**File:** `.ateam/agents/internal_doc/role.md`

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

**File:** `.ateam/agents/external_doc/role.md`

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

The CLI supports running multiple agents concurrently, limited by `max_concurrent_agents` in config.toml. The coordinator (or `ateam daemon`) manages parallelism:

- Each agent gets its own persistent worktree, data directory, and container — no shared mutable state between agents.
- The CLI's `ateam run` command checks the agents table before launching. If `max_concurrent_agents` would be exceeded, it queues or refuses.
- Go goroutines monitor multiple running containers concurrently (watching stream-json for progress, enforcing timeouts).
- The coordinator merges agent results sequentially (see §6.4) to avoid merge conflicts.

```toml
[resources]
max_concurrent_agents = 4    # during night cycle
```

For v1, the coordinator runs agents one at a time to keep things simple. Parallel execution is enabled by the architecture (isolated worktrees, separate containers) but the coordinator prompt can choose sequential execution for safety.
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

- [ ] Go module scaffold (`go.mod`, cobra CLI, embedded assets)
- [ ] SQLite database: schema, open with WAL + busy_timeout, migrations
- [ ] Config parser (`config.toml` via BurntSushi/toml)
- [ ] `ateam install` — create .ateam/ with embedded defaults, init git
- [ ] `ateam init PROJECT` — scaffold project, register agents in DB
- [ ] Git manager: bare clone, persistent worktree create, refresh (fetch + reset)
- [ ] Docker adapter (default): build image, run container with bind mounts, wait, collect stream-json
- [ ] Prompt builder: layered assembly (org role + role_add + knowledge + goals + mode + task)
- [ ] `ateam run -a AGENT --mode audit` — full lifecycle
- [ ] `ateam status` — read agents/operations tables + Docker inspect
- [ ] Write testing agent role.md (audit + implement modes)

**Milestone:** Can manually `ateam run -a testing -p myapp`, Claude Code runs in Docker with persistent workspace, stream-json captured, report generated, result recorded in ateam.sqlite.

### Phase 2: Full CLI + Coordinator (Week 3–4)

- [ ] `ateam reports`, `ateam diff`, `ateam logs`, `ateam history`
- [ ] `ateam pause / resume / kill / retry`
- [ ] `ateam shell` — interactive Claude Code session in container
- [ ] `ateam budget` — cost tracking from operations table
- [ ] `ateam doctor` — health checks with --fix
- [ ] `ateam db` — direct SQLite access
- [ ] Budget enforcement: check limits before launch, pass --max-budget-usd
- [ ] Container watchdog: stream-json monitoring, timeout kills
- [ ] Coordinator system prompt (§14.1) — write role.md for CLI-based coordination
- [ ] `ateam daemon` — scheduler with commit detection and day/night profiles
- [ ] Changelog writer (coordinator updates changelog.md via Write tool)

**Milestone:** `ateam daemon -p myapp` detects commits, runs testing, coordinator reviews reports and makes decisions, budget enforced, all via the same CLI.

### Phase 3: Full Agent Suite (Week 5–6)

- [ ] Write role.md prompts for all 7 agents (audit + implement + maintain modes)
- [ ] `ateam update-org-knowledge` — culture maintainer agent
- [ ] Compose adapter for multi-service projects
- [ ] Script adapter (docker_run.sh escape hatch)
- [ ] Report naming convention (DESCRIPTIVE_NAME.report.md + .actions.md)
- [ ] --json output on all commands for coordinator parsing
- [ ] `ateam cleanup` with --full for workspace destruction

**Milestone:** Full night cycle: daemon runs all agents, produces reports, coordinator triages and implements, knowledge updated, changelog maintained.

### Phase 4: Polish & Hardening (Week 7–8)

- [ ] Devcontainer sandbox integration (Anthropic base image + egress firewall)
- [ ] .env security (entrypoint loads env vars, deletes file)
- [ ] Prompt size monitoring (warn >12K, fail >20K tokens)
- [ ] Integration tests for CLI commands and coordinator flows
- [ ] Cross-compilation: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- [ ] Documentation: README, getting-started guide
- [ ] `ateam doctor --fix` for production robustness

**Milestone:** Single Go binary, cross-platform, handles real projects with databases and API keys, robust budget control.

---

## 18. Key Design Decisions

| Decision | Rationale |
|---|---|
| **Go CLI, no MCP** | Single binary with embedded assets. CLI composes naturally (pipe, grep, chain). No MCP indirection — coordinator calls CLI via Bash. See §3.1. |
| **Claude Code for sub-agents** | Battle-tested coding agent. No custom agent loop to maintain. Worth the tradeoff in programmatic control. See §5.1, Appendix A.1. |
| **One-shot `claude -p`** | Tasks are well-scoped by design. IPC adds enormous complexity. Stream-json gives visibility without bidirectional communication. See §5.5. |
| **Coordinator is Claude Code** | Report analysis, prioritization, conflict resolution are reasoning tasks. Claude Code does them well via CLI. No hand-coded heuristics. |
| **`--max-budget-usd` for cost control** | Hard per-invocation caps. Reliable regardless of tracking accuracy. CLI enforces daily/monthly limits before launch. See §9. |
| **Org-wide SQLite** | Single file, zero setup. WAL + `busy_timeout=5000` handles concurrent coordinators. Cross-project queries trivial. See §10.2, §20.1. |
| **File-based I/O** | Stream-json + filesystem is robust. No stdout parsing. Prompt → file → container → stream-json → report. |
| **Persistent worktrees** | Dependencies, databases, build caches survive across runs. First run is slow, subsequent runs are fast. See §6, §7.3. |
| **Container adapter abstraction** | Swap Docker for Compose, Podman, or custom scripts. Fat container is default, multi-service is the escape hatch. See §7.4. |
| **TOML for config** | Human-readable, nested sections, good Go support (`BurntSushi/toml`). |
| **Git worktrees, not clones** | Lightweight (shared object store). Persistent per-agent. `git reset --hard` refreshes tracked files, preserves untracked. |
| **Markdown for knowledge** | Natural for LLMs. Git-diffable. No schema. Agents read and write it natively. |

---

## 19. Risk Mitigation

| Risk | Mitigation |
|---|---|
| Runaway agent | Container timeout watchdog, Docker resource limits, `--max-budget-usd` per run |
| Agent breaks the build | All changes on branches, never direct to main. Testing agent verifies. |
| Conflicting changes between agents | Each agent gets own worktree. Coordinator merges sequentially. |
| LLM produces incorrect code | Testing agent validates. Human approval for high-risk changes. |
| Costs spiral | Per-run, daily, and monthly budget caps enforced by CLI before launch |
| Network exfiltration from container | Restrict egress to LLM API endpoints only |
| Human overwhelm | Minimal notifications, auto-approve low-risk, batch decisions |
| Knowledge files grow unbounded | Maintain mode summarizes and trims. Max file size enforced. |
| Claude Code version breaks things | Pin Claude Code version in Dockerfile. Test upgrades explicitly. |
| `claude -p` insufficient for complex task | Agent writes `blocked.md`, coordinator re-scopes or escalates to human |

---

## 20. Known Issues and Operational Concerns

This section documents known limitations, edge cases, and design tradeoffs that should be addressed during implementation.

### 20.1 SQLite Concurrent Access

Multiple coordinators (one per project) and human CLI commands may write to `.ateam/ateam.sqlite` concurrently. SQLite in WAL mode handles this correctly — unlimited concurrent readers, and writers acquire an exclusive lock briefly for each transaction.

When two writers collide, SQLite returns `SQLITE_BUSY`. Rather than building retry logic, we set a busy timeout:

```sql
PRAGMA busy_timeout = 5000;   -- wait up to 5 seconds for the lock
```

This is set every time the Go code opens a database connection. Since all write transactions are very short (a single INSERT or UPDATE), 5 seconds is more than enough. If a writer still can't acquire the lock after 5 seconds, something is seriously wrong (deadlock, stuck process), and the error should be surfaced to the user.

The Go database initialization looks like:

```go
db, _ := sql.Open("sqlite", filepath.Join(orgRoot, ".ateam", "ateam.sqlite"))
db.Exec("PRAGMA journal_mode=WAL")
db.Exec("PRAGMA busy_timeout=5000")
db.Exec("PRAGMA foreign_keys=ON")
```

### 20.2 Coordinator Context Window Management

The coordinator is Claude Code running `claude -p` with a system prompt. The prompt includes the coordinator role description, the CLI reference summary, and the task instructions. If the coordinator also needs to read pending reports, project state, and multiple agent reports in a single session, the context window can fill up.

**Mitigations:**

- The coordinator system prompt should be as lean as possible (~2-3K tokens). It describes *what* the CLI commands are and *how* to make decisions, not the full CLI reference.
- The coordinator uses `--json` output and reads state on demand via `ateam status --json` rather than having state pre-loaded in the prompt.
- For report review: the coordinator reads individual report files with `cat` rather than loading all reports into the prompt at once.
- The `--max-budget-usd` flag provides a natural backstop — if the coordinator uses too many tokens reasoning, it hits the budget limit and stops.

**Guideline:** The assembled coordinator prompt (role + task instructions) should target under 4K tokens. The coordinator then pulls in what it needs during execution via tool calls.

### 20.3 Agent Prompt Size

As knowledge.md files grow and the prompt stacks org role + role_add + knowledge + stack culture + project goals + mission + output contract, the assembled prompt can get large.

**Mitigations:**

- The prompt builder should measure the assembled prompt and warn above 12K tokens, hard-fail above 20K tokens.
- Knowledge.md files should be kept under ~2K tokens each. The maintain mode should explicitly prune: remove outdated entries, consolidate redundant ones.
- Stack culture files in `.ateam/knowledge/` should be concise — conventions and patterns, not tutorials.
- The prompt builder reports the token count breakdown (role: N, knowledge: N, culture: N, task: N) so operators can identify what's bloating the prompt.

### 20.4 Cost Estimation Accuracy

Budget tracking relies on two mechanisms:

- `--max-budget-usd` on `claude -p`: Claude Code enforces this as a hard cap per invocation. This is the primary cost control and is reliable.
- Parsing cost from stream-json output: The `stream-json` format includes usage metadata that may contain token counts and cost estimates. This is used for tracking and reporting, but may not be perfectly accurate in all cases (e.g., internal tool-use retries, cached prompts).

**Mitigations:**

- Use `--max-budget-usd` as the primary control (hard cap). This is reliable regardless of tracking accuracy.
- Use stream-json cost parsing as best-effort tracking for reporting and trend analysis.
- The `ateam budget` command should clearly label cost estimates as approximate.
- Conservative defaults: set per-run limits lower than you think necessary, and adjust upward based on observed costs.

### 20.5 `.env` Security in Containers

API keys in `.env` are mounted read-only into agent containers at `/workspace/.env`. The agent (Claude Code with `--dangerously-skip-permissions`) can read any file in the container, including `.env`. If the agent hallucinates or misbehaves, it could echo API keys to stream-json output.

**Mitigations:**

- The egress firewall prevents exfiltration to external services.
- The entrypoint script can load `.env` into environment variables and then delete the file before handing off to Claude Code:
  ```bash
  # In entrypoint.sh
  set -a; source /workspace/.env; set +a
  rm /workspace/.env
  # Now Claude Code sees env vars but can't read the file
  ```
- Stream-json output should be treated as potentially containing sensitive data — don't log it to shared locations.
- For high-security environments: use Docker secrets or a vault instead of `.env` files.

### 20.6 First-Run Bootstrapping

The first agent run for a new project needs to set up everything: install dependencies, create databases, run migrations, seed test data. But knowledge.md is empty and there's no prior state.

**Mitigations:**

- The project's `Dockerfile` should handle infrastructure setup (install PostgreSQL, create user/database, configure).
- The `entrypoint.sh` script should handle first-run detection:
  ```bash
  if [ ! -d "/data/pg_data" ]; then
    initdb -D /data/pg_data
    pg_ctl -D /data/pg_data start
    createdb myapp
    # Run migrations if a migration script exists
    [ -f /workspace/migrate.sh ] && /workspace/migrate.sh
  else
    pg_ctl -D /data/pg_data start
  fi
  ```
- `project_goals.md` should include bootstrapping notes: "First run setup: npm install, then run migrations with `npx prisma migrate deploy`."
- After the first successful run, the agent's knowledge.md will contain the setup information for subsequent runs.

### 20.7 Future: Reactive Triggers

The current scheduler polls for new commits at a configurable interval. Reactive triggers (on commit, on PR, via chat) are a natural extension that requires minimal architectural change:

- **Commit hooks:** A `post-receive` git hook or GitHub webhook calls `ateam run -a testing -p myapp --reason webhook`. Same CLI, same database.
- **Chat-based triggering:** The coordinator is already Claude Code. Giving it a chat interface (Slack bot, CLI chat mode) instead of one-shot `claude -p` requires no changes to the CLI or database — only a different invocation mode.
- **Config:** A `[triggers]` section in config.toml for declaring reactive triggers:
  ```toml
  [triggers]
  on_commit = ["testing"]
  on_pr = ["testing", "security"]
  ```

These are additive features that don't require architectural changes because the CLI is the universal interface.

### 20.8 Future: Sophisticated Agent Loops

The current design uses one-shot `claude -p` for all agent work. If future requirements demand more sophisticated agent behavior (multi-turn conversations, IPC between agents, checkpointing mid-task), the architecture supports this:

- The container adapter's `Exec()` method can send commands to a running agent.
- A running agent could poll a file in the bind-mounted workspace for coordinator messages.
- A Python or TypeScript agent loop could be built inside the container, using the Anthropic API directly, while the Go CLI still manages the container lifecycle.

The key constraint: the Go CLI manages the lifecycle (start, stop, status, budget), and the agent runtime (whatever it is) runs inside the container. This separation means swapping the agent runtime doesn't require changing the framework.


## 21. Future Enhancements

- **Reactive triggers** — commit hooks, GitHub webhooks, chat-based triggering. Minimal architecture change since the CLI is the universal interface. See §20.7.
- **Feature agents** — one-off agents for small feature work, leveraging the same Docker infrastructure and persistent workspaces.
- **MCP server** — `ateam serve-mcp` command to expose CLI as MCP tools for use from Claude.ai or other MCP clients.
- **Web dashboard** for monitoring agent activity, reviewing reports, approving changes.
- **Slack/Discord integration** for notifications and commands.
- **Cost optimization** by routing simpler tasks (docs, audit) to cheaper/faster models.
- **PR integration** where agents create pull requests for standard code review.
- **Learning from feedback** — when a human rejects agent work, feed that into knowledge.md.
- **Sophisticated agent loops** — Python/TypeScript agent loops inside containers for tasks that need IPC or multi-turn conversations. See §20.8.
- **`claude --resume` chaining** — for very large implementation tasks, chain multiple sessions using `--resume`.

---
---

# Appendices

These appendices contain exploration notes, alternative analyses, and competitive research that informed the design decisions above. They are preserved for context but are not part of the active specification.

---

## Appendix A. Design Exploration

### A.1 Why Go, No MCP

The framework is a CLI, not an MCP server. The coordinator is Claude Code with a system prompt that describes how to use the `ateam` CLI. When the coordinator needs to run an agent, it calls `ateam run -a testing -p myapp` via its native Bash tool. When it needs status, it calls `ateam status --json`. No MCP layer, no JSON-RPC, no tool registration — just a binary that Claude Code shells out to.

**Why this works better than MCP:**

- Claude Code can already run shell commands. Adding an MCP server between "Claude Code wants to run an agent" and "the CLI runs the agent" is pure indirection.
- The CLI composes naturally: the coordinator can pipe, grep, chain commands with `&&`, use `--json` for structured output — things that are awkward with discrete MCP tool calls.
- Developers and the coordinator use the exact same interface. No divergence between "what the MCP tool does" and "what the CLI does."
- One less process to manage. No `ateam serve` that must be running when the coordinator runs.

**Why Go:**

- **Single binary.** `go build` → one file. No Python runtime, no virtualenv, no `pip install`. Copy and run.
- **Embedded assets.** Default role prompts, knowledge templates, and the SQLite schema are embedded in the binary via `embed.FS`. `ateam install` extracts them to disk.
- **Fast compilation and type checking.** The framework is infrastructure code (Docker, git, SQLite, file I/O) — exactly what Go is built for.
- **Native concurrency.** Goroutines for monitoring multiple agent containers, streaming logs, watching for timeouts.
- **Docker and git ecosystem.** Official Docker client SDK, `go-git` for pure-Go git operations, `os/exec` for worktree management.

**MCP escape hatch:** If MCP is needed later (e.g., to use ATeam from Claude.ai chat or other MCP clients), add an `ateam serve-mcp` command that wraps CLI functions as MCP tools. This is a backwards-compatible addition.

### A.2 Claude Code vs Custom API Agent (Comparison)

| Concern | Custom API loop | Claude Code in Docker |
|---|---|---|
| Coding quality | Must implement file editing, shell, error recovery, iterative debugging from scratch | Already built-in and battle-tested |
| Maintenance burden | We maintain the agent loop; it rots as APIs change | Anthropic maintains it; `claude update` stays current |
| Authentication | API key management | Mount `~/.claude` — uses existing auth, billing, and higher plan limits |
| Tool permissions | Must define and implement each tool | Already has shell, file edit, search, etc. with proper sandboxing |
| Cost | Raw API tokens (potentially more expensive) | Claude Code subscription limits are often more economical |
| Complexity | Hundreds of lines of tool-use loop code | Zero agent code — just prompt construction and file I/O |

**Tradeoffs accepted:** No per-invocation token counts (use `--max-budget-usd` instead), harder to swap LLM providers (addressed via container adapter), less programmatic control (but stream-json gives visibility).

### A.3 Is `claude -p` Sufficient? (Analysis)

`claude -p` (print/pipe mode) runs Claude Code in non-interactive single-prompt mode. It executes the full agent loop — tool use, file editing, shell commands, iterative debugging — until the task completes or a limit is hit.

**What it does NOT do:** No interactive follow-up mid-session. No mid-stream human approval. Less sophisticated context management for very long sessions.

**Assessment: Sufficient for sub-agents.** Tasks are well-scoped (audit → approve → implement). The prompt includes all context. If a task is too complex, the agent writes `blocked.md` and the coordinator re-scopes. For rare cases needing multi-turn interaction, `--resume` flag or PTY automation are available as optimizations.

### A.4 Multi-Provider Support (Reference)

| Provider | Approach |
|---|---|
| **Claude (primary)** | Claude Code via `claude -p` |
| **OpenAI Codex** | Codex CLI via `codex -p` (same file-based I/O pattern) |
| **Gemini** | Gemini CLI agent if available, or custom API fallback |
| **Custom API** | Fallback provider — custom tool-use loop for providers without CLI agents |

Provider is configured per-project in `config.toml`:
```toml
[providers]
default = "claude-code"
# testing = "codex"
```

### A.5 Coordinator Architecture Options (Historical)

Four options were evaluated for the coordinator:

- **Option A: Claude Code + MCP (interactive)** — Human tells coordinator what to do. No scheduling.
- **Option B: Deterministic daemon + LLM escalation** — Rule-based Python. Less capable for nuanced decisions.
- **Option C: Claude Agent SDK** — Python-only, new API. Substantial framework code.
- **Option D: Claude Code + MCP server** — Framework as MCP server. Claude Code calls tools.

**Final decision: Go CLI + Claude Code (no MCP).** Simpler than all options above. The CLI is the tool; Claude Code calls it via Bash. No MCP indirection, no separate server process, no Python dependency.

### A.6 Git-Versioned Configuration

Project and org directories are git repos. This provides:

- **Timeline view**: `git log` shows all agent activity.
- **Rollback**: Revert corrupted knowledge files or bad decisions.
- **Auditability**: The git log is the authoritative record beyond changelog.md.

**Coordinator commit patterns:**

| Event | Commit Message Pattern |
|---|---|
| Report generated | `[testing] audit report 2026-02-26_2300` |
| Implementation complete | `[testing] implementation 2026-02-26_2300` |
| Knowledge updated | `[testing] knowledge update` |
| Coordinator decision | `[coordinator] auto-approved testing report` |
| Human decision | `[coordinator] human approved refactor report` |

**.gitignore for project repos:**
```gitignore
workspace/
repos/
.env
.env.*
**/current_prompt.md
**/stream.jsonl
*.tar
*.log
.DS_Store
```

---


## Appendix B. Competitive Landscape and Alternatives

### B.1 Summary

The agent orchestration space has exploded in 2025–2026. There are dozens of tools, but most fall into categories that don't quite match ATeam's specific niche: **scheduled, background, autonomous software quality improvement on an existing codebase with minimal human oversight.** Most tools are either general-purpose agent frameworks, interactive coding assistants, or PR-triggered review bots. ATeam's specific value proposition — a "night shift" of specialized agents that relentlessly improve code quality while humans sleep — is underserved.

### B.2 Most Promising Alternatives

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

**Key difference from ATeam's model:** Gas Town runs agents in tmux sessions on the bare host. ATeam runs agents in Docker containers. Gas Town's Mayor is itself a Claude Code instance (an LLM-powered coordinator). ATeam's coordinator is Claude Code calling a Go CLI. Gas Town uses Claude Code's interactive mode (long-lived sessions with `--resume`). ATeam uses one-shot `claude -p` invocations.

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
| Coordinator | LLM-powered (Mayor = Claude Code instance) | Claude Code + Go CLI |
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

### B.3 Conclusion: Build or Adopt?

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

### B.4 Future: Feature Agents

Several of these tools (OpenHands, agent-orchestrator, GitHub Copilot agent) already support the pattern of assigning feature work to agents. When ATeam adds feature agents:

- **Small features** would go through a feature queue managed by the coordinator, similar to how agent-orchestrator spawns agents per GitHub issue.
- **Each feature agent** would be a one-off — created for a specific task, given a temporary worktree, and cleaned up after the PR is merged or rejected.
- **The coordinator** would summarize progress using the same changelog pattern, with human approval gates for merging.
- **Knowledge doesn't persist** for feature agents (they're disposable), but they benefit from the project's existing knowledge files and the testing agent validates their output.

This is essentially what agent-orchestrator already does, so we could potentially integrate it as a subcomponent or adopt its patterns when the time comes.