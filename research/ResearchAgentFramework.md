# Research: Agent Framework

Refresh of the prior landscape pass. ATeam has settled into a recognisable shape — `report → review → code → verify` pipeline, 11 built-in roles, profile-driven Claude Code / Codex / codex-tmux drivers, sandbox / docker / docker-exec / in-container isolation, OAuth + keychain secrets, `ateam serve` web UI, `ateam ps / tail / inspect / resume` for observability — and the field has continued to move. This document re-evaluates each external project as of June 2026 with that shape in mind. Entries that closely overlap ATeam's quality pipeline, autonomous scheduling, isolation, or cost-control concerns come first; pure issue-trackers, interactive consoles, and abandoned projects come last or get dropped entirely.

Mid-June 2026 Claude subscription price change for unattended use is a relevant tailwind for everything in the "budget enforcement" column.

## Competitive Landscape

### Paperclip

[github.com/paperclipai/paperclip](https://github.com/paperclipai/paperclip) — 69.1k⭐, latest release v2026.529.0 (May 30, 2026), MIT-licensed Node.js / TypeScript, embedded React UI, Postgres-backed.

The densest source of borrowable patterns in this entire landscape for ATeam's cost-control gap. Self-pitched as a "control plane for zero-human companies": org charts, budgeting, governance, audit. Adapter philosophy: "if it can receive a heartbeat, it's hired" — Claude Code, Codex, Cursor, Bash/CLI, HTTP/webhook bots.

The heartbeat model is the unit of work. Per `docs/agents-runtime.md`, a heartbeat is "a short execution window triggered by a wakeup": start adapter, provide context, run until exit / timeout / cancellation, store results, stop. Triggers are timer, assignment, on-demand, or automation. Wakeups for the same agent are coalesced. A `withAgentStartLock` ensures only one heartbeat per agent at a time. Pre-flight gates run in order: budget enforcement → concurrency cap → environment lease → runtime services → workspace realisation.

The budget enforcement story is the gold standard in this landscape and the single most directly applicable pattern for ATeam:

- **Scopes:** per-agent, per-project, per-company.
- **Windows:** `lifetime`, `calendar_month_utc`, with sensible defaults.
- **Thresholds:** `soft` (default 80% — fires notifications) and `hard` (100% — pauses the scope).
- **Hard-breach action:** scope transitions to `paused` with `pauseReason: "budget"`; `cancelWorkForScope` halts ongoing work and cancels queued work.
- **Timing:** post-hoc evaluation after each cost event PLUS pre-flight `getInvocationBlock()` refusal of new runs when a hard stop is in effect.

Secrets are scoped by `companySecretBindings(targetType, targetId, configPath)` with a runtime `assertBindingContext()` check; an agent accessing an unbound secret throws. There's an audit table (`secret_access_events`). This is the strictest secret-scoping model in the doc.

Isolation is the weak point. "Local CLI adapters run unsandboxed on the host machine." Real sandboxing is a plugin extension point. Workspaces have `git_worktree` and `local_fs` provider types with declarative `provisionCommand` / `teardownCommand` hooks (echoing Symphony) — that's where dependency setup lives.

**Overlap with ATeam:** Heartbeat + wakeup-queue + per-agent serialisation is nearly a drop-in match for what ATeam's runner does today, expressed more formally. Budget enforcement is the controls ATeam needs to ship before the Claude price change makes unattended runs noticeably more expensive.

**Ideas to integrate (highest priority in this doc):**

- **Layered budget enforcement.** Per-role + per-project + global scopes; `soft` warn at 80%, `hard` pause at 100%; lifetime and per-month windows; post-hoc trip + pre-flight invocation block. `getInvocationBlock()` is the right shape — refuse new work when tripped, don't just record the over-spend. ATeam currently surfaces cost in `ateam ps` / `ateam cost` but doesn't enforce caps; this is the obvious next step.
- **`pauseReason` as first-class agent state.** Distinguish pause-by-budget, pause-by-user, pause-by-error in the run record. The web UI / `ateam ps` should display the reason, not just the state.
- **`cancelWorkForScope` on budget trip.** Cancel everything queued, not just block new starts. Half-paused queues lie about what's coming.
- **Heartbeat run + run events as the log shape.** Paperclip splits one row per run, many rows per event. ATeam's call DB is closer to "one row per call"; an events table would simplify cost-accounting roll-ups and stream-json replay.
- **Scoped secret bindings with audit.** ATeam's current keychain/env/file resolution grants whatever the daemon process has. The `(consumerType, consumerId, configPath)` binding + audit table is a much stricter model worth adopting before adding more secret types.
- **Workspace `provisionCommand` / `teardownCommand`.** Declarative, version-controllable, per-workspace dependency setup. Cleaner than role prompts having to talk about "first run `npm install`".

**Key architectural difference:** Paperclip presupposes the work is already organised (companies, projects, goals, issues) and focuses on running it safely with budget controls. ATeam discovers what needs doing on a schedule. Even if ATeam stays organisationally flat, the budget + heartbeat + secret-binding patterns transfer directly without inheriting the rest of Paperclip's hierarchy.

### SmithersBot

[github.com/smithersbot/smithersbot](https://github.com/smithersbot/smithersbot) — 7⭐, v0.1.0 released May 28, 2026, MIT-licensed TypeScript. Tiny project, but the design framing is closer to ATeam's than anything else in this landscape.

A personal Telegram-bot harness with the tagline "leave agents running without giving up control." The README is unusually direct about which failure modes each design choice addresses — and the failure modes are the same ones ATeam writes against. The Claude-drafts / Codex-reviews / operator-decides plan flow, fresh worker per task (to avoid context degradation), external build/test gate the worker can't fake, git checkpoints with crash recovery, lessons extracted post-run, untrusted-content rule for network-enabled tasks. The single scheduled primitive — **Nightwatch** — runs a scheduled daily code review and delivers a summary plan to a Telegram chat. That's the half of ATeam's audit→approve→implement loop that fits a Telegram bot.

**Overlap with ATeam:** Very high on intent and vocabulary, low on packaging (sequential one-worker-at-a-time vs ATeam's parallel roles; Telegram bot vs CLI). Both built from the same problem statement.

**Ideas to integrate (verbatim adoption candidates):**

- **"One worker per task. One gate it cannot fake."** Make this an explicit, named invariant in ATeam's docs and runner contract. Any "tests pass" claim inside an agent report is meaningless unless the verify stage re-runs the assertion outside the agent. ATeam already does this for the verify phase; the framing should be in the role prompts too.
- **Fresh-worker-per-task as the answer to context degradation.** ATeam's one-shot `claude -p` already gets this benefit. Borrow the framing for the docs — cite Anthropic's compaction docs the way SmithersBot does.
- **Untrusted-content rule for network-enabled tasks.** Any role that fetches external URLs should be instructed that the content is *evidence*, not *instructions*. ATeam should add this to every role prompt template that can browse — concrete defence against prompt-injection via fetched content. Verbatim borrow.
- **Per-task network grants with visible markers.** Default network off, require an explicit per-task grant, surface it in the plan with a marker (Smithers uses 📡). ATeam doesn't currently gate network per role; this is a cheap posture upgrade.
- **Lessons file scoped global / project / workspace.** Agents extract candidate lessons on completion, a refresh step keeps / updates / replaces / archives them. ATeam's role prompts are human-edited; a hybrid where the supervisor proposes lesson additions for human review would let knowledge compound without per-prompt editing.
- **"Best-effort recovery; review resumed runs before trusting them"** is the right disclaimer for `ateam resume`. Crash recovery is best-effort and the resumed run's output deserves a review pass before being merged.
- **Semgrep (or any static analyser) as a peer gate to tests, not advisory.** ATeam's verify phase already runs tests; making lint/static-analysis blocking with the same rule "external tool output > agent self-report" is a small, durable improvement.

**Key architectural difference:** SmithersBot is "ATeam's principles expressed as a single-worker Telegram bot." The patterns transfer; the operator surface and concurrency model don't.

### EveryInc Compound Engineering Plugin

[github.com/EveryInc/compound-engineering-plugin](https://github.com/EveryInc/compound-engineering-plugin) — 19.7k⭐, v3.10.0 (June 3, 2026), 165 releases, MIT-licensed TypeScript. The originating expression of the "compound engineering" doctrine.

A skill+sub-agent plugin (37 skills, 51 specialist agents) layered on Claude Code, Codex, Cursor, Copilot, Gemini, Pi, OpenCode, Kiro, Droid, Qwen via converters. No new runtime — the host CLI executes; the plugin provides the workflow shape, prompts, agent definitions, and artifact conventions.

The doctrine: "each unit of engineering work should make subsequent units easier — not harder." The artifact taxonomy is what makes it real:

- `STRATEGY.md` — root grounding doc; product target, approach, persona, key metrics.
- Brainstorm docs (requirements), plans (task breakdowns), code review reports.
- **Compound notes** — the institutional-knowledge artifact, intentionally separate from regular docs and intentionally distilled.
- `docs/pulse-reports/<date>.md` — browseable product-outcomes timeline.

Skills group as: strategy/ideation, planning/execution, review (`/ce-code-review` with confidence-calibrated tiered review + 25+ specialist reviewers), the actual compounding machinery (`/ce-compound` + `/ce-compound-refresh` with explicit keep/update/replace/archive vocabulary, plus a `ce-learnings-researcher` agent that searches accumulated notes), reporting/research (`/ce-product-pulse`, `/ce-sessions`, `/ce-slack-research`), and git plumbing.

**Overlap with ATeam:** Most direct conceptual overlap of any project in this landscape. Specialist parallel review, strategy→plan→execute→review→document loop, project knowledge as committed markdown, artifact-driven hand-off between stages. The difference is operational — every Compound Engineering action is a human typing a slash command, while ATeam's pipeline runs unattended.

**Ideas to integrate:**

- **Adopt the compound engineering vocabulary explicitly.** Citing Every's framing is better than coining a parallel term; "compounding" makes the roadmap test clear (does this feature make future runs cheaper?).
- **`/compound` and `/compound-refresh` as a distillation step after each run.** ATeam's report agents overwrite their artifacts but don't distill them into reusable institutional knowledge. Adding an extract-learnings pass after each run, plus a periodic keep/update/replace/archive refresh, is the missing half of the loop. The four-verb vocabulary is itself worth borrowing.
- **`STRATEGY.md` as a root grounding artifact.** A single repo-rooted file read by every role tightens alignment between long-running quality work and current product priorities. ATeam already has CLAUDE.md / role prompts / pre-prompts; an explicit `STRATEGY.md` (or `.ateam/STRATEGY.md`) injected at the top of every report's prompt would be a small, obvious win.
- **Tiered review with confidence calibration.** `/ce-code-review` suppresses low-confidence findings before they reach the user. ATeam's review supervisor should do the same — "every reviewer fired some low-confidence noise" is the failure mode this calibration is designed for.
- **Pulse reports as a browseable timeline.** ATeam has run history in CallDB; surfacing time-windowed reports as named markdown files in `.ateam/pulse/` is the UX humans actually want.
- **Compound notes as a dedicated artifact type.** Distinct from per-task reports, distinct from CLAUDE.md — distilled, deduplicated, refresh-cycled. Worth a separate `.ateam/compound/` directory and a different lifecycle from per-run artifacts.

**Key architectural difference:** Compound Engineering is the human-driven version of what ATeam should automate. Adopt the artifact taxonomy and skill names as ATeam's internal vocabulary and run them on a schedule.

### metaswarm

[github.com/dsifry/metaswarm](https://github.com/dsifry/metaswarm) — 305⭐, v0.11.0 (April 1, 2026), MIT-licensed. Extracted from a production multi-tenant SaaS codebase, the README says, with the workflow proven across hundreds of autonomous PRs.

A plugin that ships 18 personas, 13 skills, 15 commands, and rubrics into Claude Code, Gemini CLI, and Codex CLI from a single source via per-CLI manifests. Stands on BEADS (Steve Yegge's git-native issue tracker) and the obra/superpowers skill baseline.

The workflow is a 9-phase pipeline: *Research → Plan → Design Review Gate → Work Unit Decomposition → Orchestrated Execution → Final Review → PR Creation → PR Shepherd → Closure & Learning*. The distinctive bits:

- **Design Review Gate (parallel, 5 reviewers).** PM, Architect, Designer, Security, CTO personas review concurrently. 3-iteration cap before escalating to human.
- **4-phase orchestrated execution loop per work unit:** IMPLEMENT → VALIDATE → ADVERSARIAL REVIEW → COMMIT. Orchestrator "validates independently (never trusts subagent self-reports)" — re-runs tests itself.
- **Cross-model adversarial review.** "Writer always reviewed by different model." If Claude implements, Codex or Gemini reviews — and vice versa. The most concrete answer in this landscape to the single-model echo-chamber failure mode.
- **PR Shepherd.** An agent monitors CI, addresses review comments, resolves threads autonomously until merge.

Knowledge is BEADS-backed: quests/lore/decisions in `.beads/` in the repo, surviving context compaction and session interruption. `bd prime --files <scope> --keywords <kw> --work-type <kind>` is selective knowledge priming — agents load only entries relevant to the files about to be touched, so the KB can grow unbounded without consuming context. `/self-reflect` runs after PR merge and extracts code-review patterns, build/test failure causes, architectural-decision rationale as structured JSONL entries; conversation introspection also analyses the Claude session looking for user-repetition (= candidate skill) and user-disagreements (= preference). Coverage thresholds are blocking gates before PR creation via `.coverage-thresholds.json`.

**Overlap with ATeam:** Highest in this landscape on workflow rigour and quality enforcement specifically. Both insist on TDD, adversarial review, knowledge capture, role specialisation, human escalation.

**Ideas to integrate:**

- **IMPLEMENT → VALIDATE → ADVERSARIAL REVIEW → COMMIT loop, verbatim.** Especially the "orchestrator validates independently, never trusts subagent self-reports" rule. ATeam's verify phase does this for tests; the framing should extend to every claim in every report.
- **Cross-model adversarial review as a policy, not an option.** "Writer always reviewed by different model." ATeam's `scripts/codex-reviews-claude-codes.sh` already does this manually; making it the *default* for the review stage when both agents are available is a one-line policy with outsized effect.
- **`bd prime` selective priming.** ATeam currently loads the full role prompt every run. A `prime --files <scope> --work-type <kind>` filter applied to lessons / knowledge before each run lets institutional memory grow without prompt-size growth.
- **`PreCompact` hook for state survival.** ATeam has no story for what happens when a session gets compacted mid-run. Persisting plan + execution state to disk specifically so the next session resumes is cheap and closes a current failure mode.
- **Coverage threshold as a blocking PR gate.** `.coverage-thresholds.json` as a one-file mechanism for making coverage a precondition of the verify phase passing.
- **Conversation introspection during self-reflection.** Watching the user's own messages for repetition (missing skill) and disagreement (preference to capture) is a knowledge-extraction channel ATeam doesn't currently mine. ATeam records calls; running an introspection pass over them post-run is a natural extension.
- **Rubrics as separate files.** Factor review criteria out of the role prompt into version-controlled rubric files so "review against rubric X" is parameterised.

**Key architectural difference:** metaswarm is a workflow framework you install into a coding agent. ATeam is an autonomous scheduler that drives coding agents. They stack: ATeam decides what to work on and when; a metaswarm-equipped session does it. The 9-phase workflow, BEADS persistence, cross-model review, and self-reflection loop are all importable into ATeam's role definitions.

### Routa

[github.com/phodal/routa](https://github.com/phodal/routa) — 1.6k⭐, v0.18.1 (April 28, 2026), MIT-licensed TypeScript + Rust + Tauri. By Phodal (AutoDev).

A workspace-first multi-agent coordination platform organised around a Kanban board. The slogan captures the design intent: "the same card becomes stricter over time." Backlog → Todo (canonical YAML story) → Dev (execution brief + Dev Evidence) → Review (Gate verdict) → Done (summary). The card *is* the artifact — work isn't recorded in transcripts, it accumulates on the card.

The three-layer review gate is the cleanest answer in this landscape to "what does a good review actually look like":

1. **Harness Monitor** — *what happened?* Surfaces traces, changed files, executed commands, git state. Mechanical, non-judging.
2. **Entrix Fitness** — *what should be true?* Enforces hard gates, evidence requirements, file-budget / policy checks. **Rule-based, not LLM-based.**
3. **Gate Specialist** — *can this move forward?* The LLM reviewer, consulted only after the first two pass and with their outputs as inputs.

The Dev Crafter's constraint set is the other portable piece: refuse to start coding *unless the story is executable*, implement *only the scoped change* (no opportunistic refactors), maintain a clean git state, append Dev Evidence to the card. The agent itself refuses to start on an under-specified card — defence in depth.

**Overlap with ATeam:** High on workflow rigor and observability. The card-as-artifact pattern, three-layer review gate, and the Crafter's "refuse unless executable" constraint are features ATeam should adopt. The Kanban-first metaphor is a different choice — ATeam currently has run rows in CallDB.

**Ideas to integrate:**

- **Three-layer review gate.** ATeam's verify role today is a single LLM pass on a diff. Splitting into (1) mechanical trace/diff/git-state collection, (2) deterministic policy checks (test coverage, file budgets, scope adherence), (3) LLM judgment with the prior two as inputs would dramatically reduce review failure modes. The middle layer in particular — *deterministic, not LLM-based* — is what ATeam currently approximates with prompt instructions.
- **"Refuse unless executable" at the agent level, not just the orchestrator level.** ATeam's code phase assumes the review supervisor handed it a valid task list. Building the precondition into the implementer's prompt makes it defence-in-depth — both layers check.
- **Card-as-artifact growth model.** ATeam currently emits separate markdown files per stage; folding them into a single durable record that grows (story → brief → evidence → verdict → summary) is a strictly better narrative for humans skimming history.
- **Specialist YAML per lane, core YAML for role primitives.** Routa's two-tier `core/{routa,crafter,gate}.yaml` + `workflows/kanban/*.yaml` split is cleaner than ATeam's per-role prompts. Adopting it lets ATeam reuse one "implementer" core across multiple specialised contexts.

**Key architectural difference:** Routa is a board, not a scheduler. ATeam is a scheduler, not a board. The card-and-gate model is the visibility layer ATeam currently lacks; ATeam's scheduling and role discovery is the layer Routa lacks. Plausible end state: ATeam creates Routa-style cards, routes them through Routa-style lanes with Routa-style three-layer gates.

### Archon

[github.com/coleam00/archon](https://github.com/coleam00/archon) — 22.2k⭐, v0.4.1 (May 28, 2026), MIT-licensed TypeScript. "The first open-source harness builder for AI coding." The Python task-management + RAG ancestor was archived; the current iteration is a near-total rewrite.

Core idea: the agent itself is non-deterministic, but you can make the *process* deterministic by wrapping the agent in a YAML DAG of explicit steps. Each step is an AI prompt (model discretion), a shell command (no discretion), or a human gate (block until approved). Loops with `until` conditions let an AI step iterate until a deterministic predicate is satisfied (`ALL_TASKS_COMPLETE`, `TYPECHECK_PASSES`).

Illustrative shape:
```yaml
nodes:
  - id: implement
    loop:
      prompt: "Implement next task. Run validation."
      until: ALL_TASKS_COMPLETE
      fresh_context: true
  - id: run-tests
    depends_on: [implement]
    bash: "bun run validate"
```

`fresh_context: true` restarts the agent with a clean context window between loop iterations to prevent context drift — directly addresses what SmithersBot flags as the compaction problem. 19 built-in workflows include `archon-fix-github-issue`, `archon-idea-to-pr` (with 5 parallel reviewers), `archon-comprehensive-pr-review`, `archon-architect`, `archon-refactor-safely`. All are customisable YAML.

Every workflow run gets its own worktree. `archon serve` is a single binary that downloads and starts the web UI. Multi-platform adapters (CLI, web, Slack, Telegram, Discord, GitHub webhooks).

**Overlap with ATeam:** High on workflow shape, low on operational model. Both wrap Claude Code in a higher-level harness, both produce PRs, both isolate runs in worktrees, both gate human approval at key points. Both treat "AI step + deterministic check" as the unit of work.

**Ideas to integrate:**

- **YAML DAG workflows.** This is the strongest pattern to borrow. ATeam currently encodes per-stage execution in Go code; expressing the report→review→code→verify flow as a YAML DAG (AI + bash + approval gates + loops with `until` conditions) would make it inspectable, user-customisable, and version-controllable. ATeam's `scripts/` directory already approximates this in bash; YAML is the natural next step.
- **`fresh_context` between loop iterations.** When an agent loops on "fix the next failing test," restarting with a clean context window prevents the context from filling with old failure traces. ATeam should consider this for the verify phase and any role that operates in iterative cycles.
- **Loop with deterministic exit predicate.** `until: ALL_TASKS_COMPLETE` or `until: TYPECHECK_PASSES` is a clean pattern that reduces coordinator round trips by letting the agent loop internally with a bash-validated exit condition.
- **Multi-platform adapters as a future plugin slot.** ATeam's notification story is CLI + filesystem today. Archon's clean separation of platform adapter from orchestrator is worth keeping as a future architectural shape for Slack approval flows.

**Key architectural difference:** Archon is a harness builder — its job is to make a single agent's work deterministic and repeatable by wrapping it in an explicit DAG. ATeam is an autonomous quality system — its job is to decide what to work on, run specialised roles on a schedule, accumulate project knowledge, and triage results. They sit at different layers: an organisation could use Archon DAGs *as the implementation* of ATeam's individual role runs, with ATeam's pipeline deciding which DAG to invoke and when.

### Gas Town

[github.com/steveyegge/gastown](https://github.com/steveyegge/gastown) — 15.7k⭐, v1.2.0 (May 30, 2026), Go (95%). By Steve Yegge.

A multi-agent workspace manager with rich vocabulary: Mayor (AI coordinator), Polecats (worker agents), Hooks (git worktree-based persistent storage), Beads (git-backed issue tracking), Convoys (work bundles). 2026 additions are notable:

- **Three-tier watchdog chain** — Witness (per-rig), Deacon (cross-rig), Dogs (infrastructure workers); "Problems view" identifies stuck agents at scale.
- **Scheduler** — config-driven capacity governor for dispatch rate limiting.
- **Escalation system** — severity-routed blockers (CRITICAL/HIGH/MEDIUM).
- **Seance** — session discovery letting agents query predecessors for context.
- **Wasteland** — federated work coordination network via DoltHub.
- OpenTelemetry, plugin system, web dashboard.

Agent communication still uses three channels: **mailboxes** (asynchronous, Beads-backed, `gt mail send`), **nudges** (real-time tmux injection, `gt nudge`), and **hooks** (filesystem state assignment, the GUPP rule — "if there's work on your hook, you MUST run it"). Agents are long-lived tmux sessions on the bare host (no Docker isolation by default) — ATeam runs container-isolated one-shots.

**Overlap with ATeam:** Both run multi-agent systems on a codebase. They differ on almost every operational axis — Gas Town is interactive, LLM-coordinated (Mayor is itself a Claude session), tmux-on-host; ATeam is scheduled, deterministically coordinated, container-isolated.

**Ideas to integrate:**

- **Beads-style structured work tracking.** The bead schema (prefix+ID, title, description, status, priority, assignee, timestamps) plus the status flow `open → hooked → in_progress → done` is more queryable than ATeam's markdown reports. The formula/molecule system on top (TOML multi-step workflows with crash recovery — "if an agent crashes after step 3, a new agent picks up at step 4") is exactly the resumability story ATeam currently lacks.
- **Three-tier watchdog chain.** Witness per-rig + Deacon cross-rig + infrastructure Dogs is more robust than a single supervisor. ATeam's `ateam tail` and `--auto-debug` are the per-rig piece; cross-rig watching for stuck agents and infrastructure-level health checks are not yet there.
- **Scheduler as a capacity governor.** Config-driven dispatch rate limiting — ATeam's current concurrency control is at the runner level; lifting it to a scheduler-level policy with profiles (lunch / nightly / weekly) closes the obvious gap.
- **Severity-routed escalation.** Critical / high / medium routing is the right shape for ATeam's notification story once it gets one.

**Key architectural difference:** Gas Town's interactive Mayor + LLM-coordination + tmux-on-host model gets you a "factory floor of agents you walk through with a clipboard." ATeam's deterministic coordinator + container isolation + schedule-driven model gets you a "night shift that produces a stack of reports by morning." The ideas worth borrowing (beads, watchdog chain, scheduler, escalation) are the operational primitives, not the interactive model.

### amux

[github.com/mixpeek/amux](https://github.com/mixpeek/amux) — 220⭐, MIT + Commons Clause, single-file Python with embedded HTML/CSS/JS, 1,135 commits, active.

A control plane for orchestrating parallel Claude Code sessions via tmux. Zero external dependencies beyond `tmux` and `python3`. Web dashboard + REST + mobile PWA + iOS app. The most directly portable contribution is the **self-healing watchdog**:

| Condition | Action | Cooldown |
|---|---|---|
| Context usage <50% remaining | Sends `/compact` | 5-minute |
| Redacted-thinking corruption in pane | Restarts session, replays last message | — |
| Stuck prompt (with `CC_AUTO_CONTINUE=1`) | Auto-responds based on prompt shape | — |
| Fleet-wide `/rate-limit-options` prompt | Presses option 1 on every blocked session, parses reset time, schedules a resume nudge | 10s base; 5-min safety fallback |

The rate-limit handler has `off` / `capped` (max 3 auto-resumes per session per day) / `unlimited` modes. ATeam currently has no equivalent — when Claude hits a rate limit mid-run the call simply fails.

Coordination primitives: kanban board with SQLite-backed atomic compare-and-swap claims (`POST /api/board/{id}/claim` is atomic — two agents can't grab the same task), inter-session channels with `@mentions`, shared notes, conversation forks, git conflict detection, plus a named cron-style scheduler. No sandbox isolation by default — agents register with `--yolo`.

**Overlap with ATeam:** Very high on "run many Claude sessions unattended." Both target the night-shift framing. Different defaults: amux is operator-facing (dashboard + mobile + REST); ATeam is scheduler-facing.

**Ideas to integrate:**

- **The self-healing watchdog patterns are the single most portable contribution in this entire research.** All four conditions address failure modes ATeam currently has no answer for. The fleet-wide rate-limit handling (parse reset time from scrollback, schedule resume nudge) is especially relevant after the mid-June 2026 subscription price change makes rate-limit events more common. The `capped` default (max 3 auto-resumes/day) is a sensible policy that ATeam should adopt verbatim.
- **Atomic-claim kanban as coordination substrate.** SQLite CAS for task claiming is exactly the primitive ATeam's scheduler needs once concurrent runs start touching the same workdirs. Cheaper and more legible than per-run locks in calldb.
- **Notes as a persistent agent-writable layer.** ATeam currently uses role prompts and `.ateam/shared/review.md` as persistent state; a shared notes store agents can append to between runs would let project knowledge compound without prompt editing.
- **Single-file dashboard philosophy.** If ATeam ever ships a richer dashboard, amux's "zero deps, restarts on edit" model is the right shape — not a separate framework app.

**Key architectural difference:** amux is a single-host operator console (the goal is making 30 simultaneous human-prompted sessions tractable from a phone). ATeam is a scheduled autonomous quality system (the goal is making Claude do work no human prompted, on a cadence). They could compose — amux as ATeam's dashboard, ATeam as amux's scheduling brain.

### Multica

[github.com/multica-ai/multica](https://github.com/multica-ai/multica) — 35.2k⭐, v0.3.16 (June 4, 2026), self-hosted Go + TypeScript / Postgres. Among the most-starred entries in this landscape.

"Agents as teammates" framing — agents have profiles, status, comments, issue-creation rights, and appear in the same assignee picker as humans. Broad CLI compatibility (11 out of the box: Claude Code, Codex, Copilot CLI, OpenClaw, OpenCode, Hermes, Gemini, Pi, Cursor Agent, Kimi, Kiro CLI) with auto-detection on PATH.

End-to-end flow: create issue → assign to agent → agent claims and executes → real-time progress via WebSocket → completion as a comment. The `Autopilot` service is the scheduling primitive — cron-triggered workflows that auto-create an issue and enqueue a task on a designated agent, with a `run_only` mode that skips issue creation. Dispatches a *fixed* workflow to a *fixed* agent; there's no LLM-driven "decide what to work on tonight" step. Multica sits between agent-orchestrator (purely reactive) and ATeam (role-driven discovery + scheduling).

Concurrency model (the precise read of the code, worth borrowing):

- **Per-agent cap:** `agent.MaxConcurrentTasks` checked atomically in `ClaimTask()` — `if running >= MaxConcurrentTasks { outcome = "no_capacity"; return nil }`. 50 issues against one capacity-2 agent → 48 stay queued at zero cost.
- **Per-runtime cap:** daemon's `pollLoop` builds a task-slot semaphore with `cfg.MaxConcurrentTasks` slots, acquired *before* `ClaimTask` so the server's `dispatched` state can't get ahead of actual execution.
- **Worktree serialisation:** `repocache` reuses a worktree path per agent — incidental throttle for repo-touching tasks.
- **Autopilot pre-flight:** records a `skipped` run with a `failure_reason` and returns without enqueueing if the runtime isn't online.

There is no per-workspace/team/org concurrency cap; no token budget; no spend cap. Migration `013_runtime_usage` records tokens per `(runtime_id, provider, model, date)` but the dispatch path doesn't read it. Concurrency caps are the only protection from token burn.

Sandbox is delegated to the underlying CLI. For Codex on macOS the daemon currently writes `sandbox_mode = "danger-full-access"` because Seatbelt silently ignores `network_access` in workspace-write mode — known gap.

**Overlap with ATeam:** Self-hosted, open-source, agent-agnostic, Docker-deployable, real-time progress streaming, durable task lifecycle. Multi-runtime support is broader than ATeam's current Claude + Codex focus.

**Ideas to integrate:**

- **Auto-detection of available CLIs on PATH.** ATeam currently configures agents in `runtime.hcl`; an "available agents" probe at startup that adapts to whatever the user has installed is a small ergonomic win.
- **Two-level concurrency cap (per-agent + per-runtime semaphore).** Acquire-slot-before-claim ordering prevents the server's `dispatched` state from getting ahead of actual execution. ATeam's scheduler should adopt this exact ordering as it grows.
- **Bare-clone + worktree-per-task with branch naming convention.** Single bare clone per `(workspace, remote URL)`, worktrees on branches `agent/{name}/{task-id}`, reset-and-fast-forward instead of recreate. More disk-efficient than fresh worktrees per run.
- **Token usage table partitioned by `(runtime_id, provider, model, date)`.** Useful schema even without enforcement. ATeam's call DB already records cost; a daily roll-up by model and provider would make the data far easier to query.
- **Autopilot's online-ness pre-flight.** Don't dump thousands of doomed tasks into the queue when the executor is down — record a `skipped` run with a reason and return.

**Key architectural difference:** Multica is a team-coordination product (humans and agents share an issue tracker and skill library). ATeam is a quality-maintenance harness (the system decides what to work on without a person assigning it). They could compose — ATeam's report roles file Multica issues; Multica routes them to the right agent CLI.

### Symphony

[github.com/openai/symphony](https://github.com/openai/symphony) — 25k⭐, Elixir (95%), Apache-2.0. OpenAI's reference daemon for "harness engineering." Still in low-key engineering preview, no releases, 20 commits on main.

Tracker-driven, pull-based orchestrator. Work item is a Linear issue normalised to `(id, state, priority, labels, blockers)`. Each issue gets a per-issue workspace; Symphony runs a Codex app-server subprocess in that cwd through a multi-turn conversation. Pulls tracker on `polling.interval_ms` (default 30s), sorts by priority then creation date, respects global and per-state concurrency caps. Re-fetches issue state mid-run so a ticket moving to a terminal state stops the run.

The SPEC's isolation contract is deliberately narrow — workspace confinement (`cwd == workspace_path`), root containment, name sanitisation. Beyond that the SPEC mandates **no sandbox**: invocation is `bash -lc <codex.command>`. §15.1: "Each implementation defines its own trust boundary… Implementations SHOULD state clearly whether they rely on auto-approved actions, operator approvals, stricter sandboxing, or some combination." Workspaces are *persistent* across runs for the same issue. Dependency setup goes in `after_create` / `before_run` hooks (the team's problem). No global secret injection — each secret is named explicitly in `WORKFLOW.md` and resolved from env.

**Overlap with ATeam:** Both run isolated, scheduled, autonomous coding sessions with explicit lifecycle management. Both publish a spec or intentional architecture. Both treat per-task workspace as a hard invariant.

**Ideas to integrate:**

- **The SPEC itself as a reference for harness invariants.** "Workspace path MUST stay inside workspace root," "agent cwd MUST be the per-issue workspace," "reconciliation-stops-orphan-runs." ATeam already enforces these implicitly; restating them in tight language is worth the engineering hygiene.
- **Run reconciliation against external state.** Re-fetching the source of truth during a run and aborting if it moved to terminal is a clean cancellation primitive. ATeam's pipeline could do the same against report-file state — if a finding is resolved by another agent, abort the run.
- **Pull-only scheduling as an explicit design choice.** Useful counterweight to webhook-driven approaches; the orchestrator stays the single authority on what's running. ATeam already leans pull (cron-based); Symphony is evidence that's the right place to land for autonomous systems.
- **Proof-of-work artifact bundle.** CI status + PR review feedback + complexity + walkthrough is a richer "did this run accomplish something" artifact than ATeam's current logs + report diff. A standard `proof.md` per run is the right shape.
- **`/api/v1/state` as an inspection endpoint.** ATeam exposes run state via `ateam ps`; a small read-only HTTP endpoint over the same data would make external monitoring trivial.

**Key architectural difference:** Symphony is tracker-driven (issues come in, runs go out). ATeam is quality-driven (roles look at the project on a schedule and decide what to work on). They could compose — ATeam's report roles file Linear issues, Symphony picks them up.

### Agent Orchestrator (ComposioHQ)

[github.com/ComposioHQ/agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator) — 7.4k⭐, v0.9.2 (May 23, 2026), TypeScript, MIT.

Manages fleets of parallel coding agents in their own git worktrees. Agent-agnostic (Claude Code, Codex, Aider, Cursor), runtime-agnostic (tmux on macOS/Linux, ConPTY/process on Windows, Docker), tracker-agnostic (GitHub, Linear). 8-slot plugin architecture: tracker, workspace, runtime, agent, SCM, reactions, notifier, lifecycle.

Session lifecycle: tracker pulls an issue → workspace creates worktree+branch → runtime starts tmux/container → agent runs with injected issue context → reactions watch for GitHub events and auto-respond.

The **reactions system** is the primary mechanism for detecting when an agent needs external input. Three event types:

- **CI failed** → `auto: true, action: send-to-agent, retries: 2` — orchestrator fetches CI logs and injects them into the agent's session. The case study reports one PR through 12 CI-failure→fix cycles with zero human intervention.
- **Changes requested** → `auto: true, action: send-to-agent, escalateAfter: 30m`.
- **Approved and green** → `auto: false, action: notify`.

The `escalateAfter` timeout is the interaction-detection mechanism. The orchestrator reads Claude Code's JSONL session files directly to determine "working / waiting for tool / idle / finished" — same approach as ATeam's stream-json monitoring.

**Overlap with ATeam:** High on runtime substrate, low on autonomy model. Git worktree isolation, agent-agnostic design, parallel execution, CI-failure auto-remediation, stream-json/JSONL monitoring, web dashboard with "attention zones."

**Ideas to integrate:**

- **Reactions system for ATeam's future reactive triggers.** Watching webhooks and injecting CI failures / review comments back into agent sessions is exactly the pattern ATeam needs when it grows beyond pure-scheduled work. `escalateAfter` for auto-escalation is a clean primitive.
- **JSONL activity detection.** Already validated by ATeam's stream-json design; worth ensuring our monitoring covers the same states (working / waiting for tool / idle / finished).
- **Plugin architecture.** Their 8-slot factoring is clean. ATeam's container adapter abstraction covers runtime+workspace; similar plugin boundaries for tracker, SCM, and notifier are worth keeping as future architectural shapes.
- **Attention zones in the dashboard.** Grouping sessions by urgency is better UX than a flat list. Worth adopting in `ateam serve` if it grows.

**Key architectural difference:** Agent-orchestrator is reactive (responds to issues, CI failures, review comments — you `ao spawn` each task). ATeam is proactive (decides what to work on, runs on a schedule). Complementary problems — an organisation could run both: agent-orchestrator for feature work during the day, ATeam for quality maintenance at night.

### Guild

[github.com/mathomhaus/guild](https://github.com/mathomhaus/guild) — 308⭐, v0.3.2 (May 27, 2026), Go (98.6%). Daemon-style MCP server with four memory primitives backed by SQLite. Agents call Guild's tools when they need shared state; Guild is a context layer they share, not a runtime that hosts them.

- **Quest** — unit of work with priority, dependencies, status, and **atomic claim** via `guild_quest_accept` (DB row's `claimed_by` set under transaction; a second accept returns "already claimed"). Workflow-instance lock at the data-model level.
- **Lore** — typed knowledge entries: `observation` / `decision` / `research` / `principle` / `idea`. The most disciplined typed-knowledge schema in this landscape.
- **Oath** — principles auto-loaded at session start.
- **Brief** — handoff notes between sessions.

Hybrid retrieval combines BM25 keyword scoring with vector similarity via reciprocal-rank fusion. Cascade clearing: completing a quest auto-transitions dependent quests from `blocked` to `available`.

**Overlap with ATeam:** Both centre on long-lived shared state, both agent-agnostic, both local-first single-binary, both SQLite. Different layer — Guild is the data layer; ATeam is the scheduler.

**Ideas to integrate:**

- **Atomic-claim quests as workflow-instance lock.** When ATeam runs concurrent stages and they could race for the same workdir, a `claimed_by` column in CallDB set in a transaction is the right primitive — not advisory file locks, not OS-level mutexes. Adopt the pattern even without adopting the project.
- **Typed lore entries** — five-kind schema (`observation` / `decision` / `research` / `principle` / `idea`) is unusually well-chosen. ATeam's knowledge files are free-form markdown; adding a kind-tag gives retrieval a useful filter axis and lets each kind have a different lifecycle (observations get superseded; principles persist; ideas need triage).
- **Oath / Brief distinction.** ATeam currently has CLAUDE.md (cross-session, persistent) and per-run reports (this-session-only). Guild's split into "always-loaded principles" vs "last-session handoff" is a cleaner two-axis model worth adopting as vocabulary.
- **Hybrid BM25 + vector retrieval via RRF.** SQLite supports BM25 via FTS5 natively; vector via `sqlite-vec`. The Go ecosystem has both. Borrowable code-structure, not just an idea, for when ATeam's knowledge base grows past dozens of files.
- **Quest dependencies with cascade clearing.** Push scheduling logic out of code and into data — explicit task dependencies, data structure tells the scheduler which tasks are available now.

**Key architectural difference:** Guild is a shared-state daemon; ATeam is a scheduled orchestrator. They could compose — ATeam's pipeline could be a Guild client, calling `guild_quest_create` / `guild_quest_accept` / `guild_lore_inscribe`. Whether to depend on Guild or absorb the design into ATeam's SQLite is an engineering call; the primitives transfer either way.

### Sandcastle

[github.com/mattpocock/sandcastle](https://github.com/mattpocock/sandcastle) — 5.7k⭐, v0.7.0 (May 30, 2026), MIT TypeScript. By Matt Pocock.

A library, not a daemon. `sandcastle.run({ agent, sandbox, prompt })` provisions a sandbox, runs the agent against your repo, captures commits, tears down. Returns `{ iterations, commits, branch, logFilePath }`. Agent-agnostic and sandbox-provider-agnostic.

Branch strategy abstraction (three modes per-run):
- `head` — direct writes to host working dir (bind-mount).
- `merge-to-head` — agent works on temp branch, merged back when run finishes.
- `branch` — commits to a named branch, no merge.

Lifecycle hooks at host (`host.onWorktreeReady`, `host.onSandboxReady`) and sandbox (`sandbox.onSandboxReady`) boundaries. Sandbox providers are plugins: `docker()`, `podman()`, `vercel()` (Firecracker microVMs), custom. Recent additions: CLI commands for image management, parallel-planner templates.

**Overlap with ATeam:** Both run agents in isolated sandboxes against a project repo. Both agent-agnostic. Both produce commits / branches.

**Ideas to integrate:**

- **Branch strategy as a first-class option.** ATeam currently bind-mounts and commits to whatever branch the user is on. Exposing `head` / `merge-to-head` / `branch` per role would simplify both interactive use and autonomous runs that want a separate review branch.
- **Host vs sandbox lifecycle hooks.** Splitting hooks into "runs on host before container starts" and "runs inside container after start" is cleaner — useful for credentials generated on host (with keychain access) versus dependency installs that need to run inside the container.
- **Sandbox provider plugin pattern.** ATeam already has a container adapter abstraction; Vercel Firecracker as a provider is interesting if ATeam ever offers a managed/cloud mode.

**Key architectural difference:** Sandcastle is a sandboxing/invocation primitive. ATeam is the layer above (scheduler, role system, audit/implement workflow). You could plausibly build ATeam on top of sandcastle if ATeam were TypeScript; in Go, the patterns transfer.

### Claude Code — Code Review (Anthropic managed service)

[code.claude.com/docs/en/code-review](https://code.claude.com/docs/en/code-review). Research preview, Team and Enterprise only, not available with Zero Data Retention. Billed separately via usage credits, $15–25 per review average.

GitHub-integrated managed multi-agent review. Org admin installs the Claude GitHub App, picks repos, picks a trigger mode (once on PR open / on every push / manual). Multiple specialist agents analyse the diff in parallel on Anthropic infrastructure, a verification step filters false positives, findings post as inline comments with severity (🔴 Important / 🟡 Nit / 🟣 Pre-existing). The local sibling is `/code-review` in any Claude Code session (renamed from `/simplify` at v2.1.147).

Customisation lives in two repo-level files. `CLAUDE.md` is read as project context and newly-introduced violations are flagged as nits, bidirectionally (a PR change that makes a CLAUDE.md statement outdated also gets flagged). `REVIEW.md` is review-only — injected verbatim into every agent's system prompt as the highest-priority instruction block (no `@` import expansion). Used to redefine what severity means, cap nit volume, skip paths, add must-check rules, set a verification bar, define re-review convergence behaviour.

The `Claude Code Review` check run always completes neutrally so branch protection never blocks. For teams that want to gate merges, the last line of the Details text is a machine-readable HTML comment with severity counts:

```bash
gh api repos/OWNER/REPO/check-runs/CHECK_RUN_ID \
  --jq '.output.text | split("bughunter-severity: ")[1] | split(" -->")[0] | fromjson'
```

Returns `{"normal": N, "nit": N, "pre_existing": N}`. Re-run from Checks tab does not retrigger; only `@claude review once` as a comment or a new push does. Spend cap configurable at the org level.

**Overlap with ATeam:** Structurally high (same multi-agent + verification + dedup + severity-ranked shape as ATeam's review stage), operationally low (hosted vs self-hosted, PR-gated vs scheduled, review-only vs full pipeline).

**Ideas to integrate:**

- **`REVIEW.md` as the cleanest expression of "what to flag and how loud."** A single repo-root file, injected verbatim as highest-priority instruction, freeform. Much simpler than per-role prompt forking. ATeam could adopt a `.ateam/REVIEW.md` (or per-role `.ateam/AUDIT.md` etc.) injected at the top of every role prompt before the role's own template. One obvious place to dial severity, cap noise, skip paths, add repo-specific rules without forking the role files. Verbatim borrow.
- **Bidirectional CLAUDE.md enforcement.** Flag both *new code that violates CLAUDE.md* and *changes that make CLAUDE.md outdated*. ATeam's report roles could do the same — treat documentation as a peer artifact to the code and flag drift in either direction.
- **Machine-readable severity tally on a comment line** (`<!-- bughunter-severity: {...} -->`) is a clean way to expose ATeam's findings to CI. Write a check run with a neutral conclusion, append the parseable tally, let the team decide whether to gate on it.
- **Pre-attached 👍/👎 reactions** on every finding. ATeam reports could ship with the same — each finding gets a one-character ack/nack the human writes back, and the supervisor reads these next run to tune what each role flags.
- **"Suppress new nits after the first review on the same branch"** convergence rule. Borrow directly. ATeam's report roles should know whether they've reviewed a branch before and avoid re-emitting the same nits when only one line changed.
- **"Additional findings" heading for findings whose anchor moved.** ATeam reports should never silently drop a finding because the file changed between report and rendering — surface them in a separate section.

**Key architectural difference:** Code Review is the same agent shape ATeam uses, packaged as a hosted GitHub-integrated review service. For teams that can use it, running Code Review on PRs *and* ATeam on a schedule is a coherent combination: Code Review catches the bug the human is about to merge, ATeam finds the work the human hasn't thought to do yet.

### LibreFang

[github.com/librefang/librefang](https://github.com/librefang/librefang) — 285⭐, v2026.5.31-beta.16 (May 31, 2026), Rust (76.6%), MIT. 24 crates, 2,100+ tests.

A general-purpose autonomous-agent OS — kernel + Hands (autonomous capability packages with HAND.toml + system prompt) + Skills (sub-capabilities, optionally WASM) — not a coding-agent driver. LibreFang **does not spawn coding-CLI subprocesses**; it has its own runtime calling LLM HTTP APIs directly via `librefang-llm-drivers`. The `librefang-acp` crate sits on the *opposite* side of the Agent Client Protocol — embeds LibreFang agents in Zed / VSCode / JetBrains via stdio JSON-RPC, the role Claude Code plays in those IDEs. So plugging Claude Code into LibreFang means writing a new runtime that doesn't currently exist. Not a fit as a replacement, but the kernel patterns are uniquely portable.

Three sequential concurrency gates (the cleanest formulation in this landscape):

1. **Global lane semaphore** (`Lane::Trigger`): default 8. Caps total in-flight trigger dispatches kernel-wide.
2. **Per-agent semaphore**: `manifest.max_concurrent_invocations` per Hand, default 1.
3. **Per-session mutex**: applied only for `session_mode = "new"` fires.

Auto-clamp: persistent sessions with `max_concurrent_invocations > 1` are auto-clamped to 1 with a warning — the kernel refuses to let users configure a footgun.

Cron session sizing (`cron_session_max_messages` ≥ 4 to preserve prompt-cache reuse, `cron_session_max_tokens` with CJK-aware char-weighted heuristic to avoid the tokenizer dep, `cron_session_compaction_mode` either `prune` or `summarize_trim`) is the most disciplined treatment of long-lived session growth in this landscape.

`ToolExecBackend` trait abstracts tool execution: Local / Docker / SSH / Daytona. Each Hand can override the global default. SSH host-key verification is explicitly a security boundary.

**Overlap with ATeam:** Scheduled autonomous execution, per-agent identity, cost/quota intent at kernel level, pluggable execution backends, explicit scheduling primitives. Doesn't drive coding CLIs.

**Ideas to integrate:**

- **Three-gate concurrency (global lane + per-agent + per-session).** The cleanest formulation of "how do I avoid runaway fan-out." ATeam currently has implicit caps; making them explicit as `(lane, agent, session)` semaphores is a direct upgrade.
- **Auto-clamp persistent sessions to concurrency 1.** Refusing to let users configure unsafe combinations with a logged warning is good defensive design.
- **Cron session compaction with prune vs summarize_trim.** The minimum-4-messages guard "to preserve prompt-cache reuse" is a concrete insight ATeam should borrow — context compaction must respect the cache window or you pay the cache miss every fire.
- **`ToolExecBackend` as a trait with Local/Docker/SSH/Daytona impls.** ATeam currently couples runtime to Docker. Lifting tool execution to a trait with multiple backends (Local for fast iteration, Docker for prod, SSH for remote) would future-proof the architecture.
- **CJK-aware char-weighted heuristic for token estimation.** Avoids the tokenizer-dep tax. ATeam's cost tracking relies on provider reports; a fast local heuristic for pre-flight budget checks is a free win.

**Key architectural difference:** LibreFang is a general-purpose autonomous-agent OS (the goal is keeping many independent agents alive, scheduled, and bounded, regardless of what they do). ATeam is a coding-quality harness. The kernel-level concerns rhyme exactly; the surface diverges.

### Ona (formerly Gitpod)

[ona.com](https://ona.com/) — GitHub org [github.com/gitpod-io](https://github.com/gitpod-io). SaaS or self-hosted VPC. The Gitpod → Ona rebrand reflects a pivot toward agent infrastructure.

Three pillars: **Ona Agents** (proprietary, undisclosed LLM, runs in ephemeral Dev-Container VMs), **Ona Environments** (ephemeral isolated workspaces with declarative dependency setup), **Ona Guardrails** (network controls, OIDC identity, audit logs, kernel-level policy enforcement). The agent is steered by **AGENTS.md** (Linux Foundation open standard, like CLAUDE.md, recommended under 60 lines) and **Skills** (SKILL.md files in `.ona/skills/`, single-procedure workflows discovered when task descriptions match metadata). External agents (Claude Code, Cursor) can connect to Ona environments via the `ona` CLI.

Secrets are scoped User > Project > Organization, AES256-GCM at rest, injected as env vars or files (certificates, configs at specified paths). Updates propagate within 2 minutes. Build-time access integrates with Docker BuildKit.

Tasks are **Automations** — YAML workflows triggered manually, by PR, scheduled (hourly/daily/weekly/monthly UTC), or webhook. Steps: Command (shell), Prompt (to Ona Agent with prior-step context), Pull Request, Report. Guardrails include command deny lists, kernel-level veto blocking unauthorised executables, and "datawall" detecting data exfiltration via fingerprinting.

**Overlap with ATeam:** Both run background agents for quality work (security, deps, tests), both use isolated environments, both support scheduled execution, both separate analysis from implementation.

**Ideas to integrate:**

- **AGENTS.md as an additional context source.** Becoming a cross-tool convention; ATeam already uses CLAUDE.md but should consider supporting AGENTS.md too. Low cost, cross-tool friendly.
- **Command + Prompt + PR step sequencing.** Run a deterministic command (linter, scanner), feed output to an agent prompt, have the agent commit changes, open a PR. ATeam's pipeline could adopt explicit step types instead of monolithic role prompts.
- **Kernel-level guardrails (Veto + datawall).** Ona's below-agent enforcement is more robust than Docker-level isolation. Worth investigating seccomp/AppArmor profiles for ATeam's Docker mode to achieve similar enforcement.
- **Secrets as mounted files.** ATeam currently supports env-var injection. File-based secrets are useful for certificates, SSH keys, and configs that don't fit env vars well.
- **Skills pattern as complement to roles.** ATeam roles define what to look for (broad mission); SKILL.md-style files could define how to execute specific fixes (narrow procedure). Both layered would be cleaner than putting everything in role prompts.

**Key architectural difference:** Ona is a cloud infrastructure platform (work goes to their cloud, agents run there, results come back). ATeam is local-first (agents run on your machine using your Claude subscription, artifacts as local git-tracked files). Ona is better for enterprises with 100+ repos needing centralised governance; ATeam for individuals and small teams wanting autonomous quality without cloud dependency.

### AWS CLI Agent Orchestrator (CAO)

[github.com/awslabs/cli-agent-orchestrator](https://github.com/awslabs/cli-agent-orchestrator) — 675⭐, v2.2.0 (June 4, 2026), Python. Active.

Lightweight Python orchestration for multiple AI agent sessions in tmux. Hierarchical orchestration with a supervisor delegating to specialist workers via MCP. The directly relevant feature is **cron-like scheduling**: `cao flow` commands with YAML flow definitions, supporting unattended runs.

Three orchestration primitives: `handoff` (synchronous, wait for completion), `assign` (async, fire-and-forget), `send_message` (inbox delivery between agents). Cross-provider mixing — workers on different CLI tools in the same session.

**Overlap with ATeam:** Scheduling + supervisor-worker hierarchy is the same shape as ATeam's pipeline. Scheduling primitive is what ATeam's cron / nightly story needs.

**Ideas to integrate:**

- **YAML flow definitions with cron expressions.** Cleaner pattern than ATeam's current config.toml schedule profiles. The YAML-driven approach makes schedules discoverable, version-controllable, and shareable across projects.
- **Supervisor → worker with context preservation.** Supervisor provides only necessary context to each worker — aligns with ATeam's review→code delegation. Worth being explicit about the contract.
- **Direct-worker-interaction primitive.** Users can steer individual worker agents mid-task. Useful for ATeam's `ateam tail` + steering scenarios.

**Key architectural difference:** General-purpose orchestration, not quality-focused. tmux-based with no Docker isolation. The cron flow system is the durable contribution.

### OpenHands (All-Hands-AI)

[github.com/All-Hands-AI/OpenHands](https://github.com/All-Hands-AI/OpenHands) — 75.8k⭐, v1.7.0 (May 1, 2026), Python + TypeScript, SWE-bench score 77.6.

An autonomous coding agent platform. Multi-interface: SDK, CLI, local GUI, cloud, enterprise self-hosted. Multi-user with RBAC, integrations with Slack/Jira/Linear in cloud/enterprise tiers. Their "Refactor SDK" decomposes large tasks into per-agent subtasks via dependency-tree analysis.

**Overlap with ATeam:** Covers many of the same tasks (refactoring, test gen, dependency upgrades, vulnerability remediation, docs). On-demand task execution though, not continuous background improvement. Heavier infrastructure footprint (Kubernetes / SaaS leaning).

**Ideas to integrate:**

- **Task decomposition strategy.** Dependency-tree analysis to find leaf nodes, work bottom-up. Useful for ATeam's code phase when tackling large multi-file changes.
- **Refactor SDK concept** (fixers, verifiers, progress tracking) could inform how ATeam's code role handles complex multi-file changes.
- **90% automation / 10% human effort framing.** Aligns with ATeam's attention-as-bottleneck pitch — worth citing.

**Key architectural difference:** OpenHands targets on-demand "solve this issue" workflows with strong benchmark performance. ATeam targets autonomous background quality with role specialisation. They could compose at the agent layer — ATeam could potentially use an OpenHands runtime as an alternative sub-agent for specific tasks.

### Compounding Engineering (DSPy)

[github.com/Strategic-Automation/dspy-compounding-engineering](https://github.com/Strategic-Automation/dspy-compounding-engineering) — 61⭐, v0.1.3 (January 7, 2026), Python.

A local-first CLI re-implementing the compound-engineering doctrine on DSPy. Smaller scope than Every's plugin; interesting for the technique contrast. Every completed unit writes a learning artifact to `.knowledge/`; every subsequent call retrieves relevant past learnings and prepends to the prompt — at the framework layer via a **KBPredict wrapper** rather than asking the agent to read/rewrite files.

Two backends: zero-dependency JSON keyword search (default), optional Qdrant for semantic retrieval.

Multi-agent parallel review (`compounding review`) runs 10+ named specialists (Security Sentinel, Performance Oracle, Architecture Strategist…) against a diff, all KB-augmented. ReAct file editing with relevance-scored context and explicit token budgeting.

Activity has slowed (last release Jan 2026), but the patterns are worth flagging.

**Ideas to integrate:**

- **Framework-level KBPredict wrapping.** ATeam currently encodes the report→review→code chain inside agent prompts — the agent is told to read X, update X. DSPy CE moves both halves *out* of the agent: retrieval wraps the call, extraction is a separate module. Worth considering for ATeam: turn the "agent updates the report" pattern into a coordinator-side post-run extraction step so prompt rewrites can't break the memory loop.
- **Dual-backend knowledge (markdown-first, optional vector index).** Keep markdown-in-git as source of truth, optionally layer a vector index for retrieval. Clean pattern when ATeam's knowledge grows past dozens of files.
- **DSPy signatures as a sub-agent contract format.** Typed inputs/outputs (`repo_context, diff -> findings: list[Finding]`) is more structured than markdown prompts and survives prompt rewrites. Future schema option if ATeam wants to optimise prompts via metrics.

**Key architectural difference:** DSPy CE treats agents as composable Python functions; ATeam treats them as Claude Code subprocesses. The compounding doctrine is portable; the DSPy implementation isn't a fit for Go + Claude Code. Borrow the doctrine and the KBPredict pattern.

### Claude Squad

[github.com/smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) — 7.7k⭐, v1.0.18 (May 23, 2026), Go (89.7%), AGPL-3.0.

A terminal-only multi-agent manager — a TUI on top of tmux + git worktrees that lets one human juggle several agents from one keyboard. The full surface fits in a keystroke table: `n` new session, `c` **commit changes and pause session** (the standout primitive), `r` resume paused, `s` push branch via `gh`. Agent-agnostic via `cs -p "<command>"`.

**Overlap with ATeam:** Moderate on runtime layer (both use tmux + worktrees), near-zero on autonomy. Claude Squad is a manual parallel operator console; ATeam removes the human from the loop entirely.

**Ideas to integrate:**

- **`commit-and-pause` as a first-class operator primitive.** ATeam has no concept of pausing a run mid-flight while preserving state. For interactive use (when a human wants to step in, commit progress, let the agent continue later), this is the right primitive: stop agent, `git commit` whatever's in the worktree, leave worktree intact, mark run paused in CallDB.
- **Agent-agnostic launch via passed command string.** Don't write per-agent adapters at the runtime layer — pass through a command string and let whatever CLI handle its own protocol. ATeam already does this in `runtime.hcl` profiles; Claude Squad's narrowness is a reminder to keep the human-facing handle short.

**Key architectural difference:** Claude Squad is a parallel-agents operator console; ATeam is an autonomous quality scheduler. They share runtime substrate (tmux + worktrees) but solve disjoint problems on top.

### npcsh

[github.com/npc-worldwide/npcsh](https://github.com/npc-worldwide/npcsh) — 412⭐, v1.2.19 (June 4, 2026), Python. Active.

A Python "shell for AI" with its own in-process agent runtime via LiteLLM (28+ providers). Agents are YAML `.npc` files with Jinja2 `primary_directive` and a `jinxes:` allowlist of tools. Tools (`jinx` files) are YAML with Python or LLM steps. Recent additions: 125-task benchmark suite, "Plonk" GUI automation, NQL (SQL models with AI functions), knowledge graph + deep research, MCP server, REST API. Also an experimental Rust edition.

Different architecture from ATeam (ships its own agent vs delegating to Claude Code). Most of npcsh doesn't transfer. The portable ideas:

- **Prompt fragment includes.** A small `{{include:fragments/X.md}}` directive resolved through ATeam's existing 3-level fallback would eliminate copy-paste across role prompts (the "how to commit," "test etiquette," "report header schema" boilerplate duplicated across every role today). Low effort — regex pass before the templater. Don't introduce Jinja; markdown stays human-editable.
- **Team-wide shared context.** A single `defaults/shared_context.md` (3-level fallback) prepended to every prompt would centralise project facts ("we use bun, not npm; tests live in `test/`") today duplicated across roles. ATeam has `.ateam/shared/` but it's action-specific, not universal.
- **Per-role declarative tool allowlist.** A first-class `tools = [...]` field in `runtime.hcl` translated to `--allowedTools` (Claude) or codex equivalents would let a security role be read-only without a custom shell wrapper. Today the only lever is `agent_extra_args` as escape hatch.
- **Richer mock agent with scripted turns.** ATeam's mock is a single canned response; a `mock_script.json` describing turns/tool-calls/outputs would unlock cheap prompt regression tests.

Skip explicitly: full Jinja templating in prompts (conditionals in prompts are a smell), multi-agent debate (N× tokens for mush — ATeam's report→review is already structured debate), workflow DSL / Python-in-YAML (ATeam's hardcoded pipeline + bash is simpler and more legible).

### PR-Agent (Qodo)

[github.com/qodo-ai/pr-agent](https://github.com/qodo-ai/pr-agent) — 11.5k⭐, v0.36.0 (June 1, 2026), Apache-2.0. Donated by Qodo to the community, operates independently from Qodo's newer commercial offering, currently seeking maintainers.

Open-source AI-powered PR reviewer that runs on every pull request. Handles any PR size via adaptive compression. Multi-platform (GitHub, GitLab, Bitbucket, Azure DevOps, Gitea). Tools: `/describe`, `/review`, `/improve`, `/ask`. Available as GitHub Actions, CLI, Docker, webhooks, self-hosted.

**Overlap with ATeam:** PR-Agent covers the review function as a hosted gate. PR-triggered only — doesn't proactively scan.

**Ideas to integrate:**

- **PR-Agent as an optional quality gate** in ATeam's workflow. After ATeam's code phase opens a PR, run PR-Agent on it as an automated reviewer before human approval. Decoupled from ATeam, gives a second model's review for free.
- **PR compression strategy for large diffs** could inform how ATeam's review supervisor summarises agent changes when the diff is too big to fit in context.

### MetaGPT

[github.com/FoundationAgents/MetaGPT](https://github.com/FoundationAgents/MetaGPT) — 68.5k⭐, v0.8.1 last release (April 2024), pivoted to MGX (MetaGPT X) commercial product Feb 2025. ICLR 2025 papers on workflow automation.

The open-source project is largely stagnant on release cadence; the team's focus is on the hosted MGX product. The original SOP-as-prompt pattern and "software company" multi-agent metaphor (PM, Architect, Engineer, QA) are still cited in academic work but the project is no longer evolving in directly portable ways.

**Worth carrying forward:**
- **SOP-as-prompt concept** — formalise each role's workflow as an explicit SOP in the prompt, making the process reproducible and auditable. ATeam's role prompts already approach this; the SOP framing is the tighter discipline.

Otherwise drop from active tracking.

### LangGraph

[github.com/langchain-ai/langgraph](https://github.com/langchain-ai/langgraph) — 33.9k⭐, v1.2.4 (June 2, 2026). LangChain ecosystem.

Mature orchestration framework, durable execution, human-in-the-loop checkpoints, expanding into "Deep Agents" (a higher-level package for hierarchical multi-agent architectures). Still general-purpose framework, not a solution — you'd build all of ATeam's domain logic on top.

**Worth carrying forward:**
- **Durable execution.** If ATeam's pipeline crashes mid-cycle, resuming from a checkpoint is the right shape. LangGraph's pattern of persisting state checkpoints could inform ATeam's resume story beyond the per-call recovery `ateam resume` does today.
- **Human-in-the-loop with "time travel"** (roll back, take different action) is a nice model for the review→code approval gate.

Otherwise too heavy and too general for ATeam to adopt.

### Supacode

[supacode.sh](https://supacode.sh/) — macOS-only native terminal multiplexer for coding agents (libghostty + Swift). BETA, requires macOS 26 Tahoe, free download, `brew install supacode`. Open source on GitHub.

Interactive only, no scheduled operation. macOS-only. Not a fit as a coordinator replacement, but the **one-worktree-per-task with auto-agent-launch** UX and **libghostty terminal embedding** are interesting if ATeam ever builds a native monitoring app. Otherwise low-relevance.

### runCLAUDErun

[github.com/runCLAUDErun/releases](https://github.com/runCLAUDErun/releases) — macOS scheduler for Claude CLI tasks. v2.4.1 (November 17, 2025), 14⭐. Effectively a launchd wrapper for `claude -p` invocations. Solves exactly one piece of ATeam (scheduling on macOS); not enough on its own. Worth noting as proof that the scheduling-Claude-CLI niche is real and small.

### Other Notable Tools

| Tool | Link | State (June 2026) | Why Not a Direct Fit |
|---|---|---|---|
| **CrewAI** | [github.com/crewaiinc/crewai](https://github.com/crewaiinc/crewai) | 52.8k⭐, v1.14.6 (May 28, 2026), active. Now has Crews + Flows + AMP Suite enterprise offering. | General-purpose framework, not code-quality focused. No scheduling or Docker isolation built in. |
| **Ruflo** (was claude-flow) | [github.com/ruvnet/ruflo](https://github.com/ruvnet/ruflo) | 57.9k⭐, v3.10.34 (June 2, 2026), 1,524 releases. Renamed; "meta-harness for Claude" with 33+ plugins, web UI beta, federation. | Over-engineered for ATeam's needs (175+ MCP tools, neural routing, swarm intelligence framing). |
| **ccswarm** | [github.com/nwiizo/ccswarm](https://github.com/nwiizo/ccswarm) | 139⭐, v0.5.0 (Feb 26, 2026). Rust multi-agent for Claude Code. | Orchestration loop still incomplete — `start` exits immediately, ParallelExecutor not integrated. Architectural ideas only. |
| **Emdash** | [github.com/generalaction/emdash](https://github.com/generalaction/emdash) | 4.8k⭐, v1.1.27 (May 31, 2026), Electron, 21 CLI agents, SSH/SFTP for remote projects. | Interactive desktop app, not scheduled/background. |
| **OpenAgentsControl** | [github.com/darrenhinde/OpenAgentsControl](https://github.com/darrenhinde/OpenAgentsControl) | 4.2k⭐, v0.7.1 (Jan 30, 2026). Pattern-driven code generation with approval gates. | Pattern-matching focus (teach your patterns once, agents follow them). Interesting for ATeam's auto-setup mode. |
| **AutoGen** | [github.com/microsoft/autogen](https://github.com/microsoft/autogen) | 58.7k⭐, last release Sep 2025. **In maintenance mode** — Microsoft Agent Framework is the successor. Magentic-One still inside autogen but uncertain future. | Project is sunsetting; not a future-relevant target. Magentic-One's stuck-counter+reflection pattern still worth reading (see Articles Review). |
| **Beads** | [github.com/steveyegge/beads](https://github.com/steveyegge/beads) | 24.3k⭐, v1.0.4 (May 9, 2026), Dolt-backed not pure-git, agent-centric workflows. | Standalone issue tracker, already covered via Gas Town + metaswarm. Worth its own future look if ATeam adopts structured work tracking. |
| **SWE-agent** | [github.com/princeton-nlp/SWE-agent](https://github.com/princeton-nlp/SWE-agent) | 19.4k⭐, v1.1.0 (May 2025), team now recommends **Mini-SWE-agent** as the successor. | Single-agent solver, not orchestrator. Agent-computer interface design philosophy worth comparing against ATeam's role prompts. |

## Build or Adopt?

No existing tool combines all of ATeam's specific requirements: scheduled autonomous background operation, specialised roles with persistent project knowledge, deliberate report→review→code→verify phase separation, Claude Code / Codex as the sub-agent runtime, resource budgeting, git-versioned decision trail, cross-project knowledge sharing. The closest tools fall into two camps: **reactive/interactive** (agent-orchestrator, Gas Town, amux, Multica, Claude Squad — wait for a human to trigger work) or **PR-triggered review-only** (PR-Agent, Code Review — only see code that already exists in a PR). ATeam's proactive quality discovery sits in the gap.

Continue building ATeam; borrow specific patterns:

- **Budget enforcement** from Paperclip — the highest-priority pattern in this doc given the mid-June 2026 Claude price change. Per-role / per-project / global scopes, soft (80%) and hard (100%) thresholds, post-hoc trip + pre-flight `getInvocationBlock`, `pauseReason: "budget"` as first-class state, `cancelWorkForScope`. This is the controls ATeam needs to add before unattended runs get noticeably more expensive.
- **Self-healing watchdog** from amux — context-compaction trigger, redacted-thinking restart, rate-limit fleet handling with parsed reset time. Concrete failure modes ATeam has no answer for. Also rate-limit related — relevant post price change.
- **"One worker per task. One gate it cannot fake."** + **untrusted-content rule** + **lessons-with-keep/update/replace/archive** from SmithersBot. Verbatim adoption candidates for docs and role prompts.
- **`STRATEGY.md` + compound notes + pulse reports + `/compound-refresh` distillation cycle** from EveryInc Compound Engineering. The artifact taxonomy ATeam should automate.
- **IMPLEMENT → VALIDATE → ADVERSARIAL REVIEW → COMMIT** with the "orchestrator validates independently" rule, plus **cross-model adversarial review** as default policy from metaswarm. The verify phase already does part of this; the framing should extend.
- **Three-layer review gate** (mechanical / deterministic / LLM) from Routa. The deterministic middle layer is what ATeam currently approximates with prompt instructions.
- **YAML DAG workflows** with `fresh_context` between loop iterations and `until: PREDICATE` exit conditions from Archon. The natural shape for ATeam's pipeline once role logic gets more complex than "run prompt, write report."
- **Atomic-claim quests** + **typed lore entries** (`observation`/`decision`/`research`/`principle`/`idea`) + **Oath / Brief** distinction from Guild. The shared-state primitives for when ATeam adds resumable workflows and concurrent runs.
- **Beads-style structured work tracking** + **scheduler as capacity governor** + **severity-routed escalation** from Gas Town.
- **REVIEW.md as highest-priority verbatim instruction** + **machine-readable severity tally for CI** + **pre-attached 👍/👎 reactions** from Claude Code Code Review. The simplest customisation surface anyone in this landscape has shipped.
- **Three-gate concurrency** (global lane + per-agent + per-session semaphores) + **auto-clamp persistent sessions** + **cron session compaction with cache-window guard** from LibreFang. Direct upgrade over ATeam's implicit caps.
- **Per-agent + per-runtime concurrency** with acquire-slot-before-claim ordering from Multica. Prevents the queue from getting ahead of execution.
- **Branch strategy as a first-class option** (head / merge-to-head / branch) + **host vs sandbox lifecycle hooks** from Sandcastle. Cleaner factoring of the worktree question.
- **AGENTS.md support** + **Command + Prompt + PR step sequencing** + **kernel-level guardrails** from Ona. Becoming standards.
- **YAML flow definitions with cron** from AWS CAO. Discovery-friendly schedules.
- **Task decomposition (dependency-tree → leaf nodes → bottom-up)** from OpenHands. For ATeam's code phase on large changes.
- **Reactions system + JSONL activity detection + plugin architecture** from agent-orchestrator. For ATeam's future reactive-trigger work.
- **`commit-and-pause` primitive** from Claude Squad. For interactive use of `ateam` sessions.
- **Prompt fragment includes + shared-context file + declarative tool allowlist + scripted-turn mock agent** from npcsh. Low-cost ergonomic wins.

---

## Articles Review

External writing that materially shapes ATeam's design, distinct from the project surveys above.

### Christopher Meiklejohn — Multi-Agent Systems Series

[christophermeiklejohn.com/series/multi-agent-systems](https://christophermeiklejohn.com/series/multi-agent-systems/)

An eight-post survey of academic multi-agent LLM research by a distributed-systems researcher (CRDTs, Lasp, Erlang). Throughline: MAS research is quietly rediscovering distributed systems without the vocabulary — CAP, monotonicity, CALM theorem, CRDTs, causal consistency, fault injection are all relevant and underused. Academic in scope (no commercial products evaluated), so sharper on theory than on operational concerns.

**Posts (links verified):**

- [The Landscape](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/24/mas-series-01-the-landscape.html) — Wave 1 (2023, *can agents coordinate?*) vs Wave 2 (2025+, *why does it fail?*). Single-agent systems with great tool interfaces (Devin, SWE-agent) outperform MAS on coding benchmarks; MAS must justify its overhead.
- [The Vocabulary](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/25/mas-series-02-the-vocabulary.html) — Tran et al.'s four-axis taxonomy (cooperation/competition × centralised/decentralised/hierarchical × rule/role/model-based × static/dynamic), Zhou et al.'s five agent components, Chen et al.'s challenge levels.
- [Wave 1: Can Agents Coordinate At All?](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/26/mas-series-03-wave-one.html) — CAMEL, Generative Agents, ChatDev, MetaGPT, AutoGen each examined. Common gaps: no escalation, no concurrency control, fixed topologies.
- [Wave 2: Why It Breaks](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/27/mas-series-04-wave-two.html) — MAST (1,600 traces, 14 failure modes — see below), MAS-FIRE (15 fault types, capability paradox under blind-trust faults), Silo-Bench (the bottleneck is **information integration, not acquisition**).
- [Debate, State, and Coordination](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/28/mas-series-05-debate-state-coordination.html) — convergent debate (Du et al., [arXiv 2305.14325](https://arxiv.org/abs/2305.14325)), adversarial debate ([Liang et al., arXiv 2305.19118](https://arxiv.org/abs/2305.19118)), the **shared notebook** mechanism ([Ou et al., arXiv 2508.12981](https://arxiv.org/abs/2508.12981); +18% hallucination reduction from append-only fact log alone). [CALM theorem](https://arxiv.org/abs/1901.01930) cited as the right theoretical lens.
- [Verification Patterns](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/29/mas-series-06-verification-patterns.html) — **The single most operationally useful post.** Three-tier taxonomy (self-verify / separate verifier / structural gate) and the **modality shift principle**: verification across representations (code → test execution, code → screenshot) is dramatically stronger than text-to-text review.
- [Benchmarks and What They Miss](https://christophermeiklejohn.com/ai/agents/mas-series/2026/04/30/mas-series-07-benchmarks.html) — Single-agent benchmarks cannot capture coordination overhead, redundancy, recovery, or scale degradation. Multi-agent-explicit: GAIA, TravelPlanner, Silo-Bench, BrowseComp.
- [Open Questions](https://christophermeiklejohn.com/ai/agents/mas-series/2026/05/01/mas-series-08-open-questions.html) — Six open research gaps; nine "stealable today" patterns including artifacts-between-stages, append-only notebooks, tool-interface investment, stuck-detection with replanning, modality-shift verification, and "Docker plus permission configs" as the entire isolation recommendation.

### MAST: 14 Failure Modes (Cemri et al. 2025)

Paper: [Why Do Multi-Agent LLM Systems Fail?](https://arxiv.org/abs/2503.13657) — Cemri, Pan, Yang et al., UC Berkeley Sky Computing Lab.
Code/data: [github.com/multi-agent-systems-failure-taxonomy/MAST](https://github.com/multi-agent-systems-failure-taxonomy/MAST)
Project page: [sky.cs.berkeley.edu/project/mast](https://sky.cs.berkeley.edu/project/mast/)

Taxonomy derived from 150 expert-annotated traces (κ = 0.88), validated across 1,600+ traces from 7 frameworks. **Use as ATeam's failure-mode coverage checklist** — if the supervisor can detect each, that's a defensible reliability story.

**Specification & System Design Failures**

| ID | Mode | Description |
|---|---|---|
| FM-1.1 | Disobey task specification | Agent fails to follow stated constraints/requirements. |
| FM-1.2 | Disobey role specification | Agent oversteps assigned role; behaves outside defined scope. |
| FM-1.3 | Step repetition | Agent unnecessarily redoes completed steps; wastes compute without progress. |
| FM-1.4 | Loss of conversation history | Context truncated unexpectedly; agent reverts to earlier state. |
| FM-1.5 | Unaware of termination conditions | Agent fails to recognise when work should end; continues unnecessarily. |

**Inter-Agent Misalignment**

| ID | Mode | Description |
|---|---|---|
| FM-2.1 | Conversation reset | Unwarranted dialogue restart; loses accumulated context. |
| FM-2.2 | Fail to ask for clarification | Agent proceeds on ambiguous input instead of asking. |
| FM-2.3 | Task derailment | Agent deviates from intended objective toward irrelevant activity. |
| FM-2.4 | Information withholding | Agent has relevant knowledge but doesn't share with collaborators. |
| FM-2.5 | Ignored other agent's input | Agent disregards recommendations from peers. |
| FM-2.6 | Reasoning–action mismatch | Stated reasoning diverges from actual behaviour. |

**Task Verification & Termination**

| ID | Mode | Description |
|---|---|---|
| FM-3.1 | Premature termination | Dialogue ends before objectives met. |
| FM-3.2 | No or incomplete verification | Outputs not thoroughly checked; errors propagate undetected. |
| FM-3.3 | Incorrect verification | Validation runs but fails to adequately cross-check. |

**Headline empirical results:** step repetition 15.7%, reasoning-action mismatch 13.2%, termination unawareness 12.4% are the most common modes. Frameworks with **explicit verifier components performed measurably better** — direct support for the modality-shift / structural-gate principle.

### Takeaways for ATeam

Ranked by leverage:

1. **Modality-shift verification as a design rule.** Every transition between stages should cross a modality (agent prose → committed file → linter → tests → screenshot). Deterministic gates between agent stages convert weak text-to-text review into strong structural-gate review. Most directly actionable insight in the series — and the same instinct as Routa's three-layer gate and SmithersBot's "one gate it cannot fake."
2. **Append-only shared notebook** for cross-stage *facts* (separate from the report/review/code artifacts which are per-stage deliverables that get overwritten). Ou et al. measure +18% hallucination reduction from this single mechanism. ATeam currently overwrites artifacts; add a parallel append-only fact log. Closest existing implementation: **Guild's typed `Lore` entries** — read Guild as the reference implementation before designing ATeam's version.
3. **MAST 14-mode coverage as a checklist** for ATeam's supervisor. Step repetition, reasoning-action mismatch, and termination unawareness are the highest-frequency failures and should be detected first.
4. **Stuck-detection with reflection** (Magentic-One pattern): explicit loop counter, threshold-triggered reflection branch. Maps to FM-1.3 (step repetition) and FM-2.3 (task derailment).
5. **Recency × relevance × importance retrieval** (Generative Agents pattern) as the score function for knowledge injection — replaces naïve temporal or keyword retrieval.
6. **CALM/CRDT thinking applied to shared state.** Make as much shared state as possible monotonic (append-only, no retraction) so multiple agents can write concurrently without locks. The mutable-JSON-KB approach signs you up for the hard concurrency problem.

Skip: hunting for a prompt-templating framework (the series implicitly says it's a non-issue — patterns matter, framework doesn't). The series is weak on isolation; `ResearchSanboxing.md` is already deeper.

### Most Promising Linked Projects

In rough order of ATeam relevance:

- [Magentic-One](https://github.com/microsoft/autogen) (Microsoft Research, in `autogen/python/packages/autogen-magentic-one`) — production-grade MAS with the **stuck-counter + reflection** mechanism. Most operationally mature thing the series cites. With AutoGen now in maintenance mode, future of Magentic-One is uncertain but the pattern is portable regardless.
- [SWE-agent](https://github.com/princeton-nlp/SWE-agent) (Princeton) — pioneered the **agent-computer interface** abstraction. Note the project now recommends **Mini-SWE-agent** as the simpler successor with matching performance — same design philosophy. Worth comparing against ATeam's role-prompt approach.
- [Ou et al. shared-notebook paper](https://arxiv.org/abs/2508.12981) — primary source for the append-only fact log empirical result.
- [MAST repo](https://github.com/multi-agent-systems-failure-taxonomy/MAST) — annotations and dataset for replicating the 14-mode analysis on ATeam's own traces.
- [Generative Agents](https://arxiv.org/abs/2304.03442) — primary source for the recency × relevance × importance retrieval scoring.
