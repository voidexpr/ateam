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

#### Archon (coleam00/archon) ⭐⭐⭐⭐

**What it is:** "The first open-source harness builder for AI coding." A workflow engine that wraps coding agents in deterministic, YAML-defined DAGs of AI nodes, bash nodes, and human approval gates. 20.3K GitHub stars, 3.1K forks, created Feb 2025, pushed today, 198 open issues — among the most-starred and most-active projects in the space. TypeScript, MIT-licensed, primary runtime is Claude Code with Codex/Pi as alternatives. Archon was originally a Pydantic-AI agent builder; the current iteration is a near-total rewrite focused on coding harnesses.

**Core idea:** the agent itself is non-deterministic, but you can make the *process* deterministic by wrapping the agent in a graph of explicit steps. Each step is either an AI prompt (the model has discretion), a shell command (no discretion), or a human gate (block until approved). Loops with `until` conditions let an AI step iterate until a deterministic predicate is satisfied (`ALL_TASKS_COMPLETE`, `APPROVED`, etc.).

**Architecture:**
- **Platform adapters** — Web UI, CLI, Telegram, Slack, Discord, GitHub webhooks. All inputs route through a single orchestrator and all runs surface in a unified dashboard.
- **Orchestrator** — message routing, context management, codebase resolution.
- **Execution engines** — separate runners for shell commands and YAML workflows.
- **AI assistant clients** — pluggable wrappers for Claude Code, Codex, Pi.
- **Persistent storage** — SQLite or Postgres, 7 tables (codebases, conversations, sessions, workflow runs, isolation environments, messages, workflow events).

**Workflow YAML — illustrative shape:**
```yaml
nodes:
  - id: plan
    prompt: "Explore codebase and create implementation plan"
  - id: implement
    depends_on: [plan]
    loop:
      prompt: "Implement next task. Run validation."
      until: ALL_TASKS_COMPLETE
      fresh_context: true
  - id: run-tests
    depends_on: [implement]
    bash: "bun run validate"
  - id: approve
    depends_on: [run-tests]
    loop:
      prompt: "Present changes for review"
      until: APPROVED
      interactive: true
```

`fresh_context: true` is notable — restarts the agent with a clean context window between loop iterations to prevent context drift. ATeam doesn't currently do this; it's a useful idea for long iterative runs.

**17 built-in workflows** in `.archon/workflows/`, including:
- `archon-fix-github-issue` — classify → investigate → implement → validate → PR.
- `archon-idea-to-pr` — feature idea → plan → implement → validate → PR with 5 parallel reviewers.
- `archon-comprehensive-pr-review` — multi-agent PR review with 5 parallel reviewers.
- `archon-architect` — codebase health and complexity reduction sweeps.
- `archon-refactor-safely` — type-check hooks and behavior verification on every step.
- `archon-assist` — general Q&A with full Claude Code agent access.

These are all customizable YAML files, so the "built-ins" double as templates.

**Git worktree isolation.** Every workflow run gets its own worktree — same model as agent-orchestrator and Gas Town. Run 5 in parallel with no conflicts.

**Deployment.** `archon serve` (single binary that downloads and starts the web UI), `archon` CLI, optional Docker. Self-hosted, not SaaS.

**Overlap with ATeam:** High on workflow shape, low on operational model. Both wrap Claude Code in a higher-level harness, both produce PRs, both isolate runs in worktrees, both gate human approval at key points. Both treat "AI step + deterministic check" as the unit of work.

**What it lacks for our use case:**
- **No scheduler.** Workflows run on-demand via platforms, CLI, or webhooks. No cron, no daily/nightly profiles, no autonomous coordinator deciding what to run next. This is the same gap that disqualified agent-orchestrator and Supacode for ATeam's primary use case.
- **No specialized agent roles with persistent project knowledge.** Each workflow is a fresh DAG; there's no "testing specialist that has been working on this repo for 3 months and knows where the test gaps are." Archon's DAGs are stateless apart from the repo state.
- **No org-level knowledge or cross-project learning.** Per-codebase tables exist but nothing flows up to org defaults.
- **No coordinator reasoning.** Routing is rule-based / event-based. There's no LLM-powered triage that reads multiple reports and decides priority.
- **No budget/cost enforcement.** Same gap as most tools in this list.
- **Heavy multi-platform surface area.** Telegram/Slack/Discord/GitHub adapters add value for some users but are scope ATeam doesn't need or want for v1. They also imply infrastructure (a long-running server, webhook endpoints) that pushes Archon toward "always-on service" rather than "CLI you run when you want to."
- **TypeScript.** Same dependency-boundary concern as Sandcastle if ATeam wants to embed it.

**Ideas to integrate:**

- **YAML DAG workflows.** This is the strongest pattern to borrow. ATeam currently encodes per-agent execution implicitly in Go code; expressing audit/implement flows as YAML DAGs (AI nodes + bash nodes + approval gates + loops with `until` conditions) would make ATeam's workflows inspectable, version-controllable, and user-customizable. The 17 built-in workflows are essentially the same shape as ATeam's role prompts — formalizing them as YAML is a clear next step.
- **`fresh_context` between loop iterations.** When an agent loops on "fix the next failing test," restarting with a clean context window prevents the context from filling with old failure traces. ATeam should consider this for any agent that operates in iterative cycles (especially the testing and refactor agents).
- **Loop with deterministic exit predicate.** `until: ALL_TASKS_COMPLETE` or `until: TYPECHECK_PASSES` is a clean pattern. ATeam's current "agent runs once, coordinator decides what to do next" model could benefit from in-agent loops with bash-validated exit conditions, reducing coordinator round trips.
- **Multi-agent parallel review.** `archon-comprehensive-pr-review` runs 5 parallel reviewers and combines their feedback. ATeam could adopt this for the review/audit phase — multiple specialist agents review the same diff in parallel, coordinator synthesizes.
- **Platform adapters as a future plugin slot.** ATeam's notification story is currently CLI + filesystem. Archon's clean separation of "platform adapter" from "orchestrator" is worth keeping as a future architectural shape, especially for Slack/Telegram approval flows.
- **`archon serve` single-binary web UI.** The download-and-start pattern is friendlier than docker-compose. If/when ATeam adds a dashboard, this is the bar to match.

**Key architectural difference from ATeam:** Archon is a *harness builder* — its job is to make a single agent's work deterministic and repeatable by wrapping it in an explicit DAG. ATeam is an *autonomous quality system* — its job is to decide what to work on, run specialized agents on a schedule, accumulate project knowledge, and triage results for human review. They sit at different layers: an organization could plausibly use Archon DAGs *as the implementation* of ATeam's individual agent runs, with ATeam's coordinator deciding which DAG to invoke and when. Archon is the choreography for one agent's dance; ATeam is the show producer deciding which dance happens tonight.

#### Compound Engineering Plugin (EveryInc) ⭐⭐⭐⭐

**What it is:** The official Compound Engineering plugin from [Every Inc.](https://every.to/) — the team (Kieran Klaassen, Dan Shipper, et al.) that coined and popularised "compound engineering." A multi-tool plugin (Claude Code primary, plus Codex, Cursor, GitHub Copilot, Gemini, Pi, OpenCode, Droid, Qwen, Kiro via Bun-based converters). 16.2K⭐, 1.3K forks, created Oct 2025, pushed today. TypeScript, MIT-licensed. Among the most-starred and most-active projects in this entire research.

**The doctrine, applied:** "each unit of engineering work should make subsequent units easier — not harder." This is the originating expression of the doctrine; everything else covered in this section that mentions compounding (notably DSPy Compounding Engineering below) is a re-implementation of the same idea in different primitives. The Every methodology is documented at [every.to/guides/compound-engineering](https://every.to/guides/compound-engineering).

**Architecture — a skill+agent system layered on existing coding agent CLIs:**

The plugin doesn't introduce a new runtime. It ships ~38 user-facing **skills** (slash commands) and ~50 specialist **sub-agents** that the skills delegate to. The host coding agent (Claude Code, Cursor, etc.) executes the skills; the plugin provides the workflow shape, the prompts, the agent definitions, and — critically — the artifact conventions that make the loop compound.

**The skill set, grouped:**

- **Strategy & ideation:** `/ce-strategy` (creates and maintains `STRATEGY.md` — the product target, approach, persona, key metrics that ground every downstream decision), `/ce-ideate` (generates ideas grounded in strategy), `/ce-brainstorm` (interactive Q&A that produces a requirements doc).
- **Planning & execution:** `/ce-plan` (structured multi-step task plan from a brainstorm doc), `/ce-work` (executes plan tasks systematically), `/ce-debug` (root-cause + test-first fix), `/ce-optimize` (iterative optimisation with parallel experiments).
- **Review:** `/ce-code-review` (tiered multi-agent review with confidence gating), plus 25+ specialist review sub-agents (correctness, security, performance, language-specialists for Rails/Swift/TS, etc.) and 7 document-review agents (coherence, design, feasibility, product lens).
- **The actual compounding machinery:** `/ce-compound` (document a solved problem as a reusable note), `/ce-compound-refresh` (keep/update/replace/archive learnings — the *forgetting* half of the loop), and a `ce-learnings-researcher` agent that searches the accumulated notes when other skills run.
- **Reporting & research:** `/ce-product-pulse` (single-page time-windowed usage/performance report saved to `docs/pulse-reports/`), `/ce-sessions` (query prior Claude Code/Cursor history), `/ce-slack-research` (search org Slack), `ce-riffrec-feedback-analysis` (recordings → structured feedback).
- **Git plumbing:** `ce-commit`, `ce-commit-push-pr`, `ce-worktree`, `ce-clean-gone-branches`.
- **Utilities:** `/ce-demo-reel`, `/ce-setup`, `/ce-update`, `/ce-test-browser`, `/ce-test-xcode`.

**Artifact-centric workflow:**

Every skill produces or updates a versioned artifact in the repo. The artifacts are the workflow's memory. Each downstream skill reads upstream artifacts, so the chain is not "agent→agent message-passing" but "agent→file→agent." Artifact types:

- `STRATEGY.md` — root grounding doc.
- Brainstorm docs (requirements).
- Plans (task breakdowns).
- Code review reports.
- **Compound notes** — the institutional-knowledge artifact, intentionally separate from regular docs and intentionally distilled (not the full review, just the reusable pattern).
- `docs/pulse-reports/<date>.md` — browseable product-outcomes timeline.

This is an explicit answer to the question "where does the compounding actually happen?" It's not in a vector DB or a JSON KB — it's in **markdown files committed to the repo**, with a refresh skill (`/ce-compound-refresh`) that explicitly does the keep/update/replace/archive triage so the corpus doesn't drown in stale notes.

**Multi-tool support via converters:**

Native install on Claude Code. For Codex, a native plugin plus a Bun installer for the custom agent definitions. For Copilot/Droid/Qwen, a native converter. For OpenCode/Pi/Gemini/Kiro, a Bun-based converter that maps the skill+agent definitions onto each tool's plugin/skill format. This is the broadest cross-runtime support of any project covered in this section.

**Overlap with ATeam:**

- Specialist parallel review with named roles (correctness, security, performance, language-specific) — same shape as ATeam's specialist agents and Archon's reviewer panel.
- Strategy → plan → execute → review → document loop — same shape as ATeam's audit → review → implement.
- Project knowledge as committed markdown — same model ATeam uses for knowledge files.
- Artifact-driven hand-off between stages — ATeam's report→review→code chain works the same way (each stage reads and overwrites a file).

It is, in fact, the most direct conceptual overlap with ATeam in this entire section. The headline difference is operational, covered next.

**What it lacks for our use case:**

- **Interactive only.** The whole system is invoked via slash commands inside a coding-agent CLI session. No scheduler, no daemon, no autonomous coordinator. A human types `/ce-brainstorm`, then later `/ce-plan`, etc.
- **No container/sandbox.** Runs in the same shell as the host coding agent — inherits whatever sandboxing (or lack of it) the host provides.
- **No workflow engine.** The "workflow" is a *documented sequence* of slash commands ("typical loop: strategy → ideate → brainstorm → plan → work → review → compound → pulse"), not an executable artifact. There's no `workflow.yaml`, no resume-from-step, no concurrent-instance lock — because there's nothing to schedule. Each skill is just a manually-invoked prompt.
- **No budget enforcement.**
- **No Go integration.** Plugin definitions are TypeScript + skill YAML/markdown.

**Ideas to integrate (this entry contributes the most concrete patterns of any in the doc):**

- **Explicit `/compound` and `/compound-refresh` skills.** ATeam's report agents already overwrite their own artifacts, but the *distillation* step — turn a finished piece of work into a reusable institutional-knowledge note, separate from the per-task report — is missing. ATeam should add an explicit "extract learnings from this run into the knowledge file" step after each agent run, plus a periodic refresh that keep/update/replace/archives the knowledge corpus. The keep/update/replace/archive vocabulary is itself worth borrowing.
- **`STRATEGY.md` as a root grounding artifact.** Every Inc.'s convention of putting product-level strategy in a single repo-rooted file that all downstream skills read is the kind of thing that's obvious in hindsight. ATeam currently grounds agents in CLAUDE.md / role prompts / project knowledge files; adding an explicit `STRATEGY.md` (or equivalent) read by every agent would tighten the alignment between long-running agent work and current product priorities.
- **Tiered review with confidence gating.** `/ce-code-review` runs many specialists and uses confidence calibration to suppress low-confidence findings before showing them to the user. ATeam's review pipeline could adopt the same pattern — over many runs, "every reviewer fired some low-confidence noise" is the failure mode the calibration is designed for.
- **Compound notes as a dedicated artifact type.** Distinct from per-task reports, distinct from project knowledge, distinct from CLAUDE.md. The point is that they're the *distilled, deduplicated, refresh-cycled* form of accumulated learnings — a different lifecycle from any of the other artifacts.
- **`docs/pulse-reports/` browseable timeline.** Time-windowed reports as named files in a directory, naturally git-versioned, naturally browseable. ATeam has run histories in CallDB; surfacing them as a similar markdown timeline (the way humans actually read history) is a UX win.
- **Multi-tool converters.** If ATeam ever needs to support agent runtimes other than Claude Code, Every's pattern of "one source-of-truth definition, N converters per target tool" is a clean architecture.
- **Docrine endorsement.** Adopting "compound engineering" as a stated ATeam principle now means citing Every's framing rather than coining a parallel term — fewer competing names for the same idea is better for the ecosystem.

**Key architectural difference from ATeam:** The Compound Engineering Plugin is a *human-driven workflow toolkit* — a curated set of slash commands and sub-agents that a human invokes inside a coding agent session, with markdown artifacts as the connective tissue. ATeam is an *autonomous workflow system* — a scheduler that decides when and what to run, runs it without a human in the session, and reports back. Both centre on the same loop and the same artifact-based memory model. The right way to think about the relationship: ATeam should adopt the plugin's *artifact taxonomy and skill set* as its own internal vocabulary (strategy, brainstorm, plan, review, compound, refresh, pulse) and run them autonomously — i.e., what the plugin makes a human do interactively, ATeam should do unattended. They don't compete; the plugin is the manual version of what ATeam should automate.

#### DSPy Compounding Engineering (Strategic-Automation) ⭐⭐⭐

**What it is:** A local-first Python CLI by Strategic-Automation that re-implements the compound-engineering doctrine (see EveryInc entry above for the canonical version) on top of [DSPy](https://github.com/stanfordnlp/dspy), Stanford's declarative-LLM framework. 56⭐, ~5 months old (created Nov 2025), pushed April 2026. Much smaller in scope and adoption than Every's plugin, but interesting for a different reason: it expresses the same loop in DSPy's typed/optimisable primitives rather than as a curated set of human-invoked slash commands. Worth a full entry as a *technique* contrast, not as a competing implementation.

**The core idea — "compounding engineering":** every completed unit of work automatically writes a learning artifact into a JSON knowledge base under `.knowledge/`. Every subsequent agent call automatically retrieves relevant past learnings and prepends them to the prompt. The system literally gets smarter with use, without an explicit human curating the knowledge.

ATeam already implements the same loop in coarser form: report agents read the prior report and overwrite it, review agents do the same on the review file, and the coding agent updates the review with what was actually implemented. The artifact *is* the running memory; each run replaces its predecessor with an updated version. DSPy Compounding Engineering's contribution isn't the loop itself (ATeam has it) but how it's mechanised: extraction is a separate DSPy module that runs after every call, and retrieval happens at the framework layer (KBPredict wrapper) rather than via the agent re-reading and rewriting a file. That's a different cost/benefit point, discussed below.

**Architecture (5 layers):**

1. **CLI** — Typer-based commands: `compounding review`, `triage`, `work p1`, `plan "..."`, `generate-agent "..."`.
2. **Orchestration** — Python workflow scripts that wire commands into multi-step processes.
3. **Intelligence** — DSPy agents declared via `Signature` classes (typed inputs/outputs) and composed via `Module` subclasses. Optimised via DSPy teleprompters (metric-driven prompt compilation).
4. **Knowledge** — `.knowledge/` store with auto-retrieval. Two backends: a default JSON store with keyword search (zero-dependency, easy to inspect, works out of the box) **or** an optional Qdrant vector backend for semantic retrieval. The dual-backend design is worth flagging on its own — most other tools in this space lock you into one or the other.
5. **Infrastructure** — Git services, project context gathering, todo management, MCP server for Claude Desktop integration.

**KBPredict wrapper.** The interesting primitive. Every DSPy `Predict` call is wrapped so that:

- Before the call: relevant prior learnings are pulled from the KB based on the current task and injected into the prompt.
- After the call: a separate DSPy module extracts new learnings from the result and writes them back to the KB.

The agent code never has to think about retrieval or memory. It's framework-level "every call gets context, every call leaves a trace."

**Multi-agent parallel review.** `compounding review` runs 10+ named specialist agents in parallel against a diff: Security Sentinel, Performance Oracle, Architecture Strategist, etc. Each is KB-augmented, so all of them benefit from accumulated patterns from prior reviews. Same shape as Archon's `comprehensive-pr-review` (5 reviewers) but with more specialists and KB augmentation.

**ReAct file editing.** "Think → Act → Observe → Iterate" pattern with relevance-scored context and explicit token budgeting per step. Worth borrowing if ATeam ever needs to drive long edit sessions inside a single agent.

**Workflow.** `review` produces findings → `triage` lets a human batch-classify them → `work` resolves them in isolated git worktrees with parallel processing. This is essentially ATeam's audit → approve → implement pipeline, expressed as discrete CLI commands rather than a coordinator-driven schedule.

**Distribution.** Standalone CLI via `curl | sh` or pip. Docker compose provided. Python 3.10+, `uv` package manager, Ruff, FastMCP for the MCP server.

**Overlap with ATeam:** Conceptual overlap is high — multi-specialist parallel review, audit/triage/implement pipeline, git worktree per task, persistent project knowledge, local-first execution, model-agnostic (OpenAI/Anthropic/Ollama/OpenRouter). Operational overlap is partial: same workflow shape, very different machinery underneath.

**What it lacks for our use case:**

- **No scheduler.** On-demand only. `compounding review` runs when invoked; no cron, no nightly profiles, no autonomous coordinator.
- **No Docker / sandbox isolation.** Worktree-only. Agent has full access to the host. ATeam's container adapter is a strict superset.
- **Uses its own DSPy-defined agents, not Claude Code.** This is a fundamentally different runtime model from ATeam's "delegate to a Claude Code subprocess." DSPy agents are programmatic LLM calls, not coding-CLI sessions, so they're weaker at multi-step file edits than Claude Code (Claude Code has been hardened on exactly this task for ~2 years).
- **Low momentum.** 56⭐, single-organisation, no visible community traction yet. Concept-grade, not production-grade.
- **Python-only.** ATeam is Go; embedding DSPy means a separate process or porting the abstractions.
- **JSON KB without semantic dedup.** Knowledge accumulates, but the deduplication / promotion / forgetting story is not described in depth — risk of stale learnings drowning out current ones over months of use.

**Ideas to integrate:**

- **Framework-level KBPredict wrapping.** ATeam already does write-back via the report/review/code chain (each agent overwrites the artifact from the prior stage), but the loop is implemented inside agent prompts — the agent is told to read X, update X, and the human-authored prompt is responsible for getting that right. DSPy Compounding Engineering moves both halves *out* of the agent: retrieval is wrapped around every Predict call, and extraction is a separate optimised DSPy module. The benefit is that prompt rewrites can't accidentally break the memory loop, and extraction quality can be tuned independently of the agent's main task. Worth considering as a future shape: turn ATeam's "agent updates the report" pattern into a coordinator-side post-run extraction step, so the agent's job is to do the work and a separate small model is responsible for distilling learnings.
- **Pluggable knowledge backend (JSON or vector DB).** ATeam currently stores knowledge as files in the project repo. DSPy Compounding Engineering's design — start with a zero-dependency JSON keyword-search backend, allow opt-in upgrade to Qdrant for semantic retrieval — is a clean pattern. ATeam's knowledge-as-files model is great for git diff-ability and human review but starts to creak when there are many files and the right one needs to be retrieved by relevance rather than path. A future ATeam could expose the same dual-backend choice: keep markdown-in-git as the source of truth, optionally layer a vector index for retrieval at injection time.
- **Compounding engineering as an explicit doctrine.** The framing — "each unit of work should make the next equivalent unit cheaper" — is a useful design lens for ATeam's roadmap. It gives a pass/fail test for proposed features: does this measurably improve the agents' future effectiveness, or does it just ship this one task? ATeam's report→review→code chain already compounds in this sense; the doctrine is worth stating explicitly so future features are evaluated against it rather than just against immediate utility.
- **DSPy signatures as a sub-agent contract format.** ATeam's specialist agents are defined via markdown role prompts. DSPy's `Signature` (typed inputs and outputs, e.g., `repo_context, diff -> findings: list[Finding]`) is a more structured contract that survives prompt rewrites. Worth considering as a future schema for agent definitions, especially if ATeam ever needs to optimise prompts via metrics.
- **Named specialist reviewers.** "Security Sentinel," "Performance Oracle," "Architecture Strategist" — concrete role names with clear scopes. ATeam already has specialist agents; a quick audit of whether each has a tight enough scope to merit a distinct identity is worth doing.
- **Teleprompter-style prompt compilation (long term).** DSPy compiles prompts by running the pipeline against labelled examples and optimising for a metric. ATeam doesn't have a metric for "good audit," but if it ever did (e.g., reviewer agreement rate, % findings that survive triage), DSPy-style compilation could optimise the role prompts mechanically rather than by hand-tuning.

**Key architectural difference from ATeam:** DSPy Compounding Engineering is a *programmatic-LLM* tool — it treats agents as composable functions you build out of LLM calls in Python. ATeam is a *coding-agent orchestrator* — it treats agents as long-running Claude Code subprocesses you launch in containers. Different layers of abstraction. The compounding-engineering *idea* is portable across both; the DSPy *implementation* isn't a fit for ATeam's Go + Claude Code substrate. The right move is to borrow the doctrine and the KBPredict pattern, not to adopt the framework.

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

**Recommendation: Build ATeam, but borrow heavily from Gas Town, ComposioHQ/agent-orchestrator, OpenHands, Ona, Sandcastle, and Archon patterns.**

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
- **YAML DAG workflows** (AI + bash + human-gate nodes), **`fresh_context` loop iterations**, and **deterministic `until` exit predicates** from Archon.
- **Compound engineering doctrine** ("each unit of work makes the next one easier") plus the **artifact taxonomy** (`STRATEGY.md`, brainstorm, plan, review, compound notes, pulse reports), **`/ce-compound` and `/ce-compound-refresh` distillation+keep/update/replace/archive cycle**, and **tiered review with confidence calibration** from EveryInc's Compound Engineering Plugin (the canonical implementation). The **KBPredict wrapper pattern** (framework-level auto-inject + auto-codify around every model call) from DSPy Compounding Engineering as a complementary technical primitive.

### B.4 Future: Feature Agents

Several of these tools (OpenHands, agent-orchestrator, GitHub Copilot agent) already support the pattern of assigning feature work to agents. When ATeam adds feature agents:

- **Small features** would go through a feature queue managed by the coordinator, similar to how agent-orchestrator spawns agents per GitHub issue.
- **Each feature agent** would be a one-off — created for a specific task, given a temporary worktree, and cleaned up after the PR is merged or rejected.
- **The coordinator** would summarize progress using the same changelog pattern, with human approval gates for merging.
- **Knowledge doesn't persist** for feature agents (they're disposable), but they benefit from the project's existing knowledge files and the testing agent validates their output.

This is essentially what agent-orchestrator already does, so we could potentially integrate it as a subcomponent or adopt its patterns when the time comes.

---
