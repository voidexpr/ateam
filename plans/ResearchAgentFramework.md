# Research: Agent Framework

## B. Competitive Landscape and Alternatives

### B.1 Summary

The agent orchestration space has exploded in 2025–2026. There are dozens of tools, but most fall into categories that don't quite match ATeam's specific niche: **scheduled, background, autonomous software quality improvement on an existing codebase with minimal human oversight.** Most tools are either general-purpose agent frameworks, interactive coding assistants, or PR-triggered review bots. ATeam's specific value proposition — a "night shift" of specialized agents that relentlessly improve code quality while humans sleep — is underserved.

See also: [Agent Orchestrator research](https://github.com/ComposioHQ/agent-orchestrator/blob/main/artifacts/competitive-research.md).

### B.2 Most Promising Alternatives

#### ComposioHQ/agent-orchestrator ⭐⭐⭐⭐ (Closest Match)

**What it is:** An open-source TypeScript platform (40K LOC, 3.3K tests, MIT licensed) that manages fleets of parallel coding agents. Each agent gets its own git worktree, branch, and PR. Agent-agnostic (Claude Code, Codex, Aider, OpenCode), runtime-agnostic (tmux, Docker), tracker-agnostic (GitHub, Linear). Built in 8 days by 30 concurrent AI agents orchestrating their own construction. 2.7K GitHub stars as of Feb 2026.

**How agents are controlled:**

The system uses an 8-slot plugin architecture where every abstraction is swappable. The session lifecycle is:

1. **Tracker** pulls an issue from GitHub or Linear
2. **Workspace** creates an isolated git worktree (or clone) with a feature branch
3. **Runtime** starts a tmux session (default) or Docker container
4. **Agent** plugin launches the coding agent (e.g., Claude Code) with issue context injected into the prompt
5. The agent works autonomously — explores code, writes changes, creates a PR
6. **SCM** plugin enriches the PR with context
7. **Reactions** system watches for GitHub events and auto-responds (see below)
8. **Notifier** pings the human only when judgment is needed

Agents are spawned via `ao spawn <project> <issue>`. Each gets a dedicated tmux session (or process). The orchestrator doesn't communicate with agents mid-task via IPC. Instead, when external events occur (CI failure, review comment), the reactions system **injects context into the agent's terminal session** — it sends keystrokes or text into the tmux pane, which the agent reads and acts on. This is a clever hack: the agent doesn't need a special API for receiving feedback, it just sees new information appear in its session as if a human typed it.

The orchestrator also supports `ao send <session> "Fix the tests"` for manual injection of instructions into a running agent session.

**How interaction needs are detected (activity detection):**

This is one of the more interesting problems they solved. The orchestrator needs to know what each agent is doing without asking it (because asking would interrupt the agent's work).

Their solution: **Claude Code writes structured JSONL event files during every session.** The orchestrator reads these files directly (not stdout, not the agent's self-report) to determine:

- Is the agent actively generating tokens? (working)
- Is it waiting for tool execution? (tool call in progress)
- Is it idle? (may be stuck or finished)
- Has it finished? (session complete)

This avoids the unreliability of asking agents to self-report their status. The JSONL events are a side-channel that the orchestrator monitors passively. This is essentially the same approach as ATeam's stream-json monitoring (§7.3), which validates our design choice.

The **reactions system** is the primary mechanism for detecting when an agent needs external input. It watches GitHub webhooks for three event types:

- **CI failed** → `auto: true, action: send-to-agent, retries: 2` — the orchestrator fetches CI logs and injects them into the agent's session. The agent reads the failure, fixes the code, pushes again. In their case study, one PR went through 12 CI failure→fix cycles with zero human intervention.
- **Changes requested** (review comments) → `auto: true, action: send-to-agent, escalateAfter: 30m` — review comments are routed to the agent with context. If the agent hasn't addressed them within 30 minutes, escalate to human.
- **Approved and green** → `auto: false, action: notify` — human gets a notification to merge (can be set to auto-merge).

The `escalateAfter` timeout is the key interaction-detection mechanism: if an agent can't resolve an issue within a configured window, the system assumes human judgment is needed and escalates via the notifier plugin (desktop notification, Slack, webhook).

**The web dashboard** (Next.js 15 with Server-Sent Events) groups sessions into "attention zones" — failing CI, awaiting review, running fine — so the human sees at a glance which sessions need attention. Live terminal view via xterm.js shows what agents are actually doing in real time.

**Numbers from their self-build:**

- 30 concurrent agents, 747 commits across all branches, 65 of 102 PRs merged
- 84% of PRs created by AI sessions, 100% of code AI-authored
- 700 automated code review comments (Cursor Bugbot), agents fixed 68% immediately
- 41 CI failures across 9 branches, all self-corrected — 84.6% overall CI success rate
- Human involvement: 1% of code review comments (13 of ~1000)

**Overlap with ATeam:** Very high. Git worktree isolation, agent-agnostic design, parallel execution, CI failure auto-remediation, stream-json/JSONL monitoring for activity detection, web dashboard.

**What it lacks for our use case:**

- **No scheduled/autonomous operation.** It's reactive (responds to issues, CI failures, review comments) not proactive (scans for code quality improvements on a schedule). No cron, no night/day profiles. You must `ao spawn` each task.
- **No specialized agent roles with persistent knowledge.** Agents are generic workers — no concept of a "testing specialist" that accumulates project knowledge and gets smarter over time.
- **No audit → approve → implement workflow.** Agents go straight from issue to PR. No deliberate phase separation where findings are reviewed before code changes.
- **No budget enforcement.** No per-run, daily, or monthly cost caps. They reported their creator burning through Pro Max plan limits with 30 concurrent agents.
- **No org-level knowledge sharing.** No mechanism to learn patterns across projects and promote them to shared defaults.
- **No coordinator reasoning.** The orchestrator agent is described as intelligent, but the current open-source implementation is primarily event-driven reactions, not LLM-powered decision-making about what to work on next.

**Ideas to integrate:**

- **Reactions system** for ATeam's coordinator. The pattern of watching GitHub webhooks and injecting CI failures / review comments back into agent sessions is exactly what ATeam's reactive triggers (§20.7) should do. The `escalateAfter` timeout for auto-escalation is a clean pattern.
- **JSONL activity detection.** Their approach of reading Claude Code's structured event files to determine agent status validates ATeam's stream-json design. We should ensure our stream-json monitoring covers the same states: working, waiting for tool, idle, finished.
- **tmux injection pattern.** For interactive sessions (`ateam shell`), the ability to send instructions to a running agent via `ao send` is useful. In ATeam's architecture, this maps to the container adapter's `Exec()` method or writing to a message file in the bind-mounted workspace.
- **Plugin architecture.** Their 8-slot system (runtime, agent, workspace, tracker, SCM, notifier, terminal, lifecycle) is well-factored. ATeam's container adapter abstraction (§7.4) covers runtime+workspace. We should consider similar plugin boundaries for notification and tracker integration in future phases.
- **Attention zones** in the dashboard. Grouping sessions by urgency (failing, needs review, running fine) is better UX than a flat list of agents. Worth adopting if/when ATeam adds a dashboard.

**Key architectural difference from ATeam:** Agent-orchestrator is an interactive tool for feature work — you assign issues, agents build features, you review PRs. ATeam is an autonomous background system for code quality — it decides what to work on, agents audit and improve, the coordinator triages. They solve complementary problems. An organization could plausibly run both: agent-orchestrator for feature work during the day, ATeam for quality maintenance at night.

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

#### Supacode ⭐⭐

**What it is:** A native macOS terminal application ([supacode.sh](https://supacode.sh/)) that serves as a "command center" for running multiple coding agents in parallel. Built in Swift with The Composable Architecture (TCA) and libghostty as the terminal engine. Open source, 72 releases, requires macOS 26. Installable via `brew install supacode`.

**How it works:** Supacode is a terminal multiplexer purpose-built for coding agents. Each task gets an isolated git worktree (via `⌘N`). The user adds repositories through the sidebar, configures a setup script per repo (e.g., `claude --dangerously-skip-permissions`), and Supacode launches the agent in the worktree automatically. It supports Claude Code, Codex, and OpenCode as agent runtimes — BYOA (Bring Your Own Agents) with no translation layer. Claims to handle 50+ parallel agent sessions.

**Overlap with ATeam:** Git worktree isolation per task, multi-agent parallelism, support for Claude Code as runtime, native GitHub integration (PRs, CI checks, conflict resolution).

**What it lacks for our use case:**
- **Interactive-only.** No scheduled/autonomous operation. A human must create each worktree and launch each agent. No daemon, no night cycle, no coordinator.
- **No agent specialization or knowledge.** Agents are generic terminal sessions. No concept of persistent roles, project knowledge, or accumulated context across runs.
- **No audit → implement workflow.** No report generation, no triage, no approval gates.
- **No budget tracking.** No cost visibility or caps.
- **macOS only.** Native Swift app, no Linux or headless server support.
- **Terminal multiplexer, not orchestrator.** Supacode manages terminal sessions, not agent lifecycles. It doesn't monitor agent output, detect failures, or retry. It's tmux-with-worktrees, not a coordination system.

**Ideas to integrate:**
- The **one-worktree-per-task with auto-agent-launch** UX is clean. ATeam's `ateam run` already does this programmatically, but Supacode's visual approach could inform a future TUI or dashboard.
- The **libghostty terminal embedding** is interesting if ATeam ever builds a native monitoring app.

**Key difference from ATeam:** Supacode is a developer productivity tool — a better terminal for manually running agents side by side. ATeam is an autonomous system that decides what to work on, runs agents unattended, and triages results. Supacode is the cockpit; ATeam is the autopilot.

#### Sandcastle (mattpocock/sandcastle) ⭐⭐⭐

**What it is:** A TypeScript library for running coding agents inside isolated sandboxes via a programmatic `sandcastle.run()` API. Created March 2026 by Matt Pocock, 1.85K stars in ~6 weeks, 889 commits, latest release v0.5.6 (April 2026), pushed daily. Agent-agnostic (Claude Code, Codex, Pi, OpenCode, custom) and sandbox-provider-agnostic (Docker bind-mount, Podman with SELinux, Vercel Firecracker microVMs, custom providers). MIT-licensed.

**How it works:** Library, not a daemon. You call `sandcastle.run({ agent: claudeCode("claude-opus-4-6"), sandbox: docker(), prompt: "..." })` and it provisions a sandbox, runs the agent inside it against your repo, captures commits, and tears it down. Returns `{ iterations, commits, branch, logFilePath }`.

**Branch strategy abstraction.** Three modes selected per-run:
- `head` — agent writes directly to the host working directory (bind-mount, default).
- `merge-to-head` — agent works on a temp branch, sandcastle merges back when the run finishes.
- `branch` — agent commits to a named branch (`agent/fix-42`), no merge.

This is a cleaner factoring of the worktree question than most tools. ATeam's container adapter hardcodes the bind-mount + branch model; sandcastle makes it a knob.

**Lifecycle hooks** at two boundaries:
- `host.onWorktreeReady`, `host.onSandboxReady` — run on the developer's machine.
- `sandbox.onSandboxReady` — runs inside the container (with optional `sudo`).

Each hook is `{ command: string; timeoutMs?: number }`. Useful for installing project-specific deps, seeding databases, or warming caches before the agent starts.

**Sandbox providers as plugins.** `docker()`, `podman()`, `vercel()`, plus a documented interface for writing your own. Vercel's Firecracker microVMs are notable — they give you cloud-isolated sandboxes without standing up infrastructure.

**Overlap with ATeam:** Both run agents in isolated sandboxes against a project repo. Both are agent-agnostic in principle. Both produce commits/branches as their output artifact.

**What it lacks for our use case:**
- **On-demand only.** No scheduler, no cron, no background daemon. You invoke `sandcastle.run()` from your own code or CLI; sandcastle won't decide when to run.
- **No coordinator or supervisor.** Single-agent invocations. You can call `createSandbox()` and chain multiple `run()` calls in the same container, but there's no LLM-powered coordinator deciding what to do next.
- **No specialized agent roles or persistent project knowledge.** Each `run()` is stateless apart from the repo state and any hooks you wire up.
- **No tracker/reactions/notifier integration.** Sandcastle is the sandboxing + invocation layer; you'd build the rest (issue intake, CI reaction, human escalation) on top.
- **No web dashboard, no audit/approve/implement gating.** Programmatic only.
- **Not a macOS seatbelt option.** Container-based isolation only — Docker/Podman locally, or a cloud microVM. Doesn't help if you specifically want process-level seatbelt sandboxing on macOS.
- **TypeScript-only.** ATeam is Go; integrating sandcastle as a dependency means crossing a language boundary or re-implementing the abstractions.

**Ideas to integrate:**

- **Branch strategy as a first-class option.** ATeam should consider exposing `head` / `merge-to-head` / `branch` modes per agent run instead of always using a feature branch. `head` mode (direct writes to working tree) would simplify interactive ATeam shell use; `merge-to-head` is what most autonomous runs effectively want.
- **Host vs sandbox lifecycle hooks.** ATeam currently has container-side setup baked into the runtime config. Splitting hooks into "runs on host before container starts" and "runs inside container after start" is cleaner — useful for things like generating credentials on the host (with access to keychain) and seeding DB schemas inside the container.
- **Sandbox provider plugin pattern.** ATeam already has a container adapter abstraction; sandcastle's interface is worth comparing against ours. The Vercel Firecracker provider in particular is interesting if ATeam ever offers a managed cloud option — outsource the sandbox to an existing microVM platform rather than building one.
- **Programmatic `run()` API.** ATeam exposes the agent runtime via CLI commands. A library-shaped API (callable from Go programs, not just from `ateam` invocations) would make it embeddable in custom workflows. Not a v1 priority but a clean future shape.

**Key architectural difference from ATeam:** Sandcastle is a sandboxing/invocation primitive — it answers "given an agent and a repo, run the agent safely and capture the diff." ATeam is the layer above that primitive — it answers "given a project and a schedule, decide which agents to run, when, and what to do with the results." You could plausibly build ATeam on top of sandcastle (if ATeam were TypeScript) by treating sandcastle as the container adapter and adding the coordinator, scheduler, role system, and audit/implement workflow on top. The right mental model: sandcastle is the runtime ATeam already has internally, packaged as a reusable library; ATeam is everything else.

#### Ona (formerly Gitpod) ⭐⭐⭐⭐

**What it is:** A cloud platform (SaaS or self-hosted VPC) for running AI software engineering agents in isolated, reproducible environments. Originally Gitpod (cloud dev environments), rebranded as Ona in 2025–2026 with a pivot toward AI agent infrastructure. Supports background agents, automations triggered by PRs/schedules/webhooks, enterprise guardrails, and kernel-level security enforcement. SOC 2 certified, GDPR compliant. Targets Fortune 500.

**Agent model — own agent, not Claude Code:**

Ona runs its own proprietary agent ("Ona Agent"). The underlying LLM is not disclosed publicly. The agent operates inside ephemeral cloud VMs provisioned from Dev Container configs — each task gets a fresh isolated environment with the project's full toolchain (compilers, test suites, linters, etc.).

Ona Agent is steered through two mechanisms:
- **AGENTS.md**: an open standard (Linux Foundation) placed in the repo root. Functions like CLAUDE.md — teaches the agent project conventions, commands, structure. Loaded at session start. Recommended under 60 lines.
- **Skills** (SKILL.md files in `.ona/skills/`): reusable multi-step workflows (e.g., "create-pr", "go-tests", "sentry-triage"). Discovered automatically when task descriptions match the skill's metadata. Similar to ATeam's role prompts but more granular — each skill is a single procedure rather than a full role.

Ona also supports **external agents** (Claude Code, Cursor) connecting to Ona environments via the `ona` CLI. External agents use `ona environment create` to provision a VM, then run commands inside it via `ona environment exec`. This is a different model from ATeam: Ona provides the infrastructure, the agent runs remotely inside it.

**How they manage credentials/authentication:**

Three-level secret hierarchy with strict precedence: **User** (highest) > **Project** > **Organization** (lowest).

- **Encryption**: AES256-GCM at rest, TLS in transit. Ona employees cannot access encryption keys.
- **Injection**: secrets are injected into environments as environment variables, files (certificates, configs mounted at specified paths), or container registry credentials. Updates propagate to running environments within 2 minutes.
- **Build-time access**: secrets integrate with Docker BuildKit during Dev Container image builds automatically.
- **For external agents**: the `ona` CLI authenticates via `ona login` (browser-based OAuth). Machine-to-machine auth uses service accounts or personal access tokens.
- **Enterprise**: SSO (Google, Okta, Entra ID, PingFederate, Amazon Cognito, GitLab), OIDC for cloud resource access (e.g., AWS role assumption from inside an environment), SCIM for user provisioning.

This is more sophisticated than ATeam's secret management. ATeam resolves secrets from keychain/env/files and forwards them via `docker run -e`. Ona has a full secrets management plane with scoping, encryption, and file-based injection.

**How they organize tasks:**

Tasks are organized as **Automations** — YAML-defined workflows (`automations.yaml`) with sequential steps:

1. **Trigger**: manual, PR event, time-based schedule (hourly/daily/weekly/monthly in UTC), or webhook.
2. **Steps** execute in sequence within the same environment. Step types:
   - **Command**: run a shell command (e.g., `npx knip --reporter json > report.json`)
   - **Prompt**: send a prompt to the Ona Agent with context from previous steps
   - **Pull Request**: open a PR with the changes made
   - **Report**: extract structured execution data
3. **Guardrails**: command deny lists, kernel-level veto (blocks unauthorized executables), datawall (detects data exfiltration via fingerprinting).

Example automation (CVE remediation):
```
Command step: snyk test --json > snyk-report.json
Prompt step: "Read snyk-report.json. Resolve every CVE found..."
Pull Request step: open PR with changes
Trigger: scheduled weekly Sunday 8 PM UTC
```

Automations can target multiple repositories in parallel (e.g., run CVE remediation across 100 repos simultaneously). Managed via UI or CLI (`ona ai automation create automation.yaml`).

This is similar to ATeam's report → review → code pipeline but more rigid: Ona's steps are a fixed sequence defined in YAML. ATeam's coordinator makes dynamic decisions about what to work on based on report content.

**Pricing:** OCU-based (Ona Compute Units). Free tier: $10 in credits, 3 parallel environments. Core: from $20/month, up to 100 members, unlimited environments, GPU support. Enterprise: custom pricing, VPC deployment, SSO/OIDC, warm pools, SLA.

**Overlap with ATeam:** Both run background agents for code quality tasks (security scanning, dependency updates, test improvements). Both use isolated environments. Both support scheduled execution. Both separate analysis from implementation.

**What it lacks for our use case:**

- **No specialized agent roles with persistent knowledge.** Ona Agent is a single general-purpose agent steered by AGENTS.md and Skills. No concept of a "testing specialist" or "security specialist" that accumulates project-specific knowledge over time. Skills are static instructions, not learned context.
- **No coordinator/supervisor.** No LLM-powered triage layer that reads multiple reports and prioritizes. Each automation runs independently — there's no cross-automation reasoning about what matters most.
- **Cloud-only.** Requires Ona's infrastructure (SaaS or VPC deployment). Cannot run on a developer's laptop or a simple build server with Docker. ATeam is a local CLI that works anywhere Docker runs.
- **Closed-source agent.** The Ona Agent is proprietary. Cannot inspect, modify, or replace the agent's behavior beyond AGENTS.md and Skills. ATeam uses Claude Code directly — any improvement to Claude Code immediately benefits ATeam.
- **No git-versioned decision trail.** Automation runs produce logs and reports, but there's no equivalent of ATeam's git repo of decisions, reports, and knowledge files that forms an auditable timeline.
- **No cross-project knowledge.** No organization-level knowledge that agents accumulate and share between projects.
- **Cost model tied to platform.** OCU billing combines compute + tokens. ATeam separates infrastructure cost (your own Docker) from API cost (your own Claude subscription).

**Ideas to integrate:**

- **AGENTS.md as a standard.** Ona's AGENTS.md is a Linux Foundation open standard. ATeam already uses CLAUDE.md for similar purposes but should consider supporting AGENTS.md as an additional context source for sub-agents — it's becoming a cross-tool convention.
- **Command + Prompt + PR step pattern.** Ona's automation step types map cleanly to ATeam's workflow: run a deterministic command (linter, scanner), feed output to an agent prompt, have the agent create changes, open a PR. ATeam's `ateam run` could adopt this explicit step sequencing.
- **Kernel-level guardrails (Veto).** Ona's below-agent enforcement (blocking executables, detecting data exfiltration at the kernel level) is more robust than ATeam's Docker-level isolation. Worth investigating for ATeam's container profiles — could use seccomp or AppArmor profiles to achieve similar enforcement.
- **Secrets as files.** Ona injects secrets as mounted files at specified paths, not just env vars. ATeam currently only supports env var injection. File-based secrets are useful for certificates, SSH keys, and configs.
- **Multi-repo automations.** Ona can run the same automation across hundreds of repos in parallel. ATeam's organization concept supports multiple projects but doesn't have a "run this across all projects" primitive yet.
- **Skills pattern.** Ona's SKILL.md (small, focused, auto-discovered procedures) is a useful complement to ATeam's role prompts (broad, domain-wide missions). ATeam could support both: roles define what to look for, skills define how to execute specific fixes.

**Key architectural difference from ATeam:** Ona is a cloud infrastructure platform — you send work to Ona's cloud, agents run there, results come back as PRs. ATeam is a local-first CLI — agents run on your machine (or any machine with Docker), using your Claude subscription, with artifacts stored as local git-tracked files. Ona abstracts away the infrastructure; ATeam gives you full control of it. Ona is better for enterprises with 100+ repos needing centralized governance. ATeam is better for individual developers or small teams wanting autonomous quality improvement without a cloud dependency.

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

**Recommendation: Build ATeam, but borrow heavily from Gas Town, ComposioHQ/agent-orchestrator, OpenHands, Ona, and Sandcastle patterns.**

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
- **AGENTS.md standard**, **Command+Prompt+PR step sequencing**, **kernel-level guardrails**, and **Skills pattern** from Ona.
- **Branch strategies** (`head` / `merge-to-head` / `branch`) and **host-vs-sandbox lifecycle hooks** from Sandcastle.

### B.4 Future: Feature Agents

Several of these tools (OpenHands, agent-orchestrator, GitHub Copilot agent) already support the pattern of assigning feature work to agents. When ATeam adds feature agents:

- **Small features** would go through a feature queue managed by the coordinator, similar to how agent-orchestrator spawns agents per GitHub issue.
- **Each feature agent** would be a one-off — created for a specific task, given a temporary worktree, and cleaned up after the PR is merged or rejected.
- **The coordinator** would summarize progress using the same changelog pattern, with human approval gates for merging.
- **Knowledge doesn't persist** for feature agents (they're disposable), but they benefit from the project's existing knowledge files and the testing agent validates their output.

This is essentially what agent-orchestrator already does, so we could potentially integrate it as a subcomponent or adopt its patterns when the time comes.

---
