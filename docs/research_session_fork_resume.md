# Research: Session fork / resume for unattended runs

*Date: 2026-09-21. Tool versions checked: Claude Code 2.1.278, Codex CLI 0.146.1 local (source and release notes read up to 0.153.0).*

## The question

Since ATeam started, Claude Code gained `--fork-session`, headless `--resume`, `--continue`, session stores and several resume knobs; Codex gained `codex exec resume` and `codex exec fork`. The ideas on the table:

- **A. Overview-then-fork.** Run one "project overview" agent that discovers the repo, then fork each report role out of that session so roles skip discovery and start faster.
- **B. Checkpoint-and-resume.** Save that point (or the end of a report run) and resume it in later cycles with "check what changed in git since `<sha>`".
- **C. Report-discovery-first.** Same as A but the discovery step is the first half of a report run rather than a separate overview role.
- **D. Crash recovery.** When a run dies (persistent API errors, killed after a tool call, timeout), resume the same session instead of restarting from scratch. If the failure was a bad tool call, fork from a few turns earlier and add a warning about what went wrong.

The short answer: **D is the strongest case and should be built first. A is technically sound but only as a same-run, same-cwd, within-cache-TTL optimisation, and it competes with a cheaper mechanism ATeam already has. B is a bad idea and the field agrees. C is A with worse cache economics.** Details and a concrete recommendation follow.

## 1. What the CLIs offer today

### Claude Code (2.1.278)

| Flag / option | Headless (`-p`) behaviour |
|---|---|
| `--resume <id>` | Works. Runs one turn, exits, **appends to the same session id**. Since 2.1.223 the id is found across all projects on the machine, not only the launch cwd. |
| `--continue` | Works. `-p --continue` picks up `-p`-created sessions too. |
| `--fork-session` (with `--resume`/`--continue`) | Works. New session id, history copied, original untouched. The `system/init` stream event already carries the fork's id. Combine with `--session-id <uuid>` to pick the id. |
| `--no-session-persistence` | Print mode only. No transcript written, not resumable. |
| `--resume-session-at <msg-uuid>` | Not in `cli-reference`; accepted by the 2.1.278 binary and documented as the SDK option `resumeSessionAt`. Resume from a point in the transcript rather than the end. `--resume-drops-turn <uuid>` drops one turn. |
| `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1` (env) | On the next `-p --resume <id>`, auto-continues a turn that was cut short by SIGTERM (exit 143), bounded by `..._MAX_AGE_MS`. Anthropic's own headless crash-recovery hook. |
| `--system-prompt-snapshot on` (default) | Not in `cli-reference`; from the 2.1.267 changelog. The system prompt is recorded on the first request and reused verbatim on every resume until compaction. |
| `--bare` | Skips hooks, plugins, auto-memory, CLAUDE.md. Docs say it "will become the default for `-p`". |
| `--autocompact <auto\|100k..1M>` | Per-launch auto-compact window. |

Agent SDK names: TypeScript `resume`, `forkSession`, `continue`, `resumeSessionAt`, `persistSession`, `sessionStore` (alpha); Python `resume`, `fork_session`, `continue_conversation`, `session_id`, `resume_session_at`, `session_store`. Session id is on the `init` message and on `result`.

Things a `-p --resume` does **not** restore: permission mode, `--add-dir`, `--mcp-config`, `--settings`, `--plugin-dir`, `--fallback-model`. They must be passed again. The model is restored from the transcript unless `--model` is passed. The docs state `--fork-session` for `-p` only indirectly (via the plan-mode restoration rules) and describe same-id append as "messages from both interleave into one transcript" when two processes resume the same id; both behaviours were confirmed on the local binary.

Sub-agent fork: the Agent tool's `subagent_type: "fork"` inherits the parent's full history and shares its prompt cache, but is **off by default in `-p`** (docs confirm the default; the `CLAUDE_CODE_FORK_SUBAGENT=1` opt-in comes from the changelog, not the env-vars page). Agent teams do not spawn in `-p` at all.

### Codex CLI (0.146.1 local; 0.148+ for fork)

| Command | Headless behaviour |
|---|---|
| `codex exec --json` | Emits `thread.started {thread_id}` first. The thread id is the resume key. |
| `codex exec resume <uuid\|name> [PROMPT]` | Present in 0.146.1. Appends to the same rollout. **No `-s`, `-a`, `-C`, `--add-dir` flags** on this subcommand; sandbox is not persisted, so pass `-c sandbox_mode=...` again (issue #40149). Caller cwd replaces the recorded cwd. |
| `codex exec resume --last [--all]` | `--last` is cwd-filtered; `--all` lifts that. |
| `codex exec fork <id> [PROMPT]` | Shipped in 0.148.0 (from issue #11750, a harness author who needed headless fork). New rollout, fresh thread id, `forkedFromId` metadata. |
| App-server protocol | `thread/fork` with `lastTurnId` (fork from a chosen turn) and `ephemeral: true` (in-memory fork). The TypeScript/Python SDK exposes `resumeThread` but **no fork API**. |

Rollouts live at `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`, may be zstd-compressed since 0.153, and are self-contained. Resume by UUID works from any cwd, so rollouts are portable if `CODEX_HOME` is preserved.

## 2. Cost mechanics: what a resume or fork actually pays

This is the part that decides the question.

**Neither CLI keeps server-side conversation state across processes.** Claude Code docs: "Claude Code sends the whole conversation again, and the request reads from the cache whatever part of its prefix is unchanged and still within the cache lifetime." Codex sends `store: false` over HTTP; its websocket transport does chain `response_id` incrementally *within* a live process, but after a resume or fork the first request is flagged `restored_history` and is a full send.

So a fork or resume never *avoids* the tokens of the parent transcript. It only changes the price per token of that prefix:

| Situation | Price of the inherited prefix |
|---|---|
| Fork within the cache TTL, same cwd, same model, same tools, same system prompt | Cache read: 0.1× input on both providers (Claude Fable 5.1: 0.025×). |
| Fork or resume after the TTL | Full input price, plus a cache write at 1.25× (5 min) or 2× (1 h) the first time. |
| Resume from a different cwd, container, worktree or machine | Always a full miss. Claude's cache prefix embeds cwd, platform, git branch/commit snapshot and memory paths. |

TTL numbers for Claude: 5 min on API key or usage credits, 1 h on a subscription *within the plan's included usage* (it drops to 5 min once a run is on usage credits). The 1 h bucket is the "main conversation", and the docs place non-interactive `-p` runs in it. Only *in-process* subagents, fork subagents and teammates get the separate 5 min bucket governed by `subagentPromptCacheTtl`. So a `claude -p --fork-session` role, being its own process, gets the main-conversation TTL; `promptCacheTtl=1h` forces it on API-key auth. OpenAI: for GPT-5.6+ the cache TTL is 30 min and `prompt_cache_options.ttl: "30m"` is the only value; older models use `prompt_cache_retention` with a `24h` option. No setting for either was found in the Codex CLI config reference. Every cache hit refreshes the TTL for free.

Which auth mode ATeam runs under decides the TTL bucket and every dollar figure below. The default `docker` profile and the sandbox path use OAuth subscription auth (`ISOLATION.md:110`); `docker-api` uses an API key.

Worked example on Fable 5.1 pricing ($10 in / $12.50 5-min write / $20 1-h write / $0.25 cache read per MTok). Suppose an overview run leaves a 60k-token transcript (asserted for the example, not measured; see §6):

| | Cost |
|---|---|
| Re-establish 60k cold, 5-min TTL | $0.75 once |
| Re-establish 60k cold, 1-h TTL | $1.20 once |
| Carry 60k warm, per turn per fork | $0.015 |
| 10k-token written overview, first turn of a role (uncached) | $0.10 |
| Same 10k, each later turn (cached) | $0.0025 |

Break-even on carried tokens alone is roughly 10 to 15 turns per role: a 50-turn cold role would favour the artifact, a 6-turn warm role would not. Per-token cost is therefore *not* the decisive argument against forking. The decisive arguments are TTL fragility and the inheritance of unverifiable prior observations, covered in §5.

Other cost-relevant facts:

- A resumed session keeps carrying the full transcript on every subsequent turn. Discovery tool results (file reads) are the bulk of it and cannot be un-read by a later instruction.
- Compaction is lossy and stacks. Claude Code's own docs warn quality "can degrade with multiple compactions"; Amp removed auto-compaction for this reason; Codex keeps user messages verbatim plus an opaque server-side summary item. After compaction Claude Code re-reads up to five recently read files (sessions doc).
- A transcript that grows past the model window returns "Prompt is too long" on resume and cannot be compacted (claude-code #14472, closed not-planned).
- Claude transcripts are swept after `cleanupPeriodDays` (30 by default). A checkpoint older than that is gone.

## 3. What other projects do

| Project | Approach | Mechanism | Outcome / caveat |
|---|---|---|---|
| Anthropic engineering ("Effective harnesses for long-running agents", "Harness design for long-running apps", "Effective context engineering") | Fresh context + written handoff | Initializer agent writes `feature_list.json`, `claude-progress.txt`, `init.sh`; each session starts clean from `git log` + progress file. "Compaction isn't sufficient." | Their own docs on cross-host resume: "Don't rely on session resume. Capture the results you need and pass them into a fresh session's prompt. This is often more robust than shipping transcript files around." |
| Claude Code workflows (fan-out) | Fresh siblings, shared cache prefix | Same-prefix agents staggered ≤5 s so siblings read the first agent's cache write. | Anthropic's own answer to "N agents, one discovery": share the *cache*, not the *session*. |
| Paperclip | Long-lived session per agent, resumed every heartbeat (Claude adapter: `claude --resume`; Codex adapter: thread reuse) | Persists `sessionId`, rotates only on error. Docs: "good for prompt caching". | Worst documented failure set in the survey: CLAUDE.md/AGENTS.md changes silently ignored until rotation (#3596, open feature request); failed runs leave stale beliefs the next run acts on (#635, open); an unrotated Codex thread reached 148.8M cumulative input tokens against ~1.9k output tokens (#5462, closed, fixed by rotation). |
| Gas Town / gascity (Yegge) | Sessions are "cattle"; fork used read-only and for warm workers | `gt seance --talk <id>` forks a predecessor with `--fork-session --resume` to interrogate it without mutating it. gascity "warm arm" forks a pre-built "brain" session at first launch only; later wakes resume the child. | State of record is beads/hooks/git, never the transcript (GUPP). Warm-vs-cold savings not published. |
| Amp (Sourcegraph) | Removed fork, replaced compaction with handoff | `/handoff <goal>` writes a new-thread prompt plus relevant file list as an editable draft. | Handoff post: "Compaction is lossy... stacking summary on top of summary." Fork-removal post: "We'd rather spend our time perfecting handoff and thread mentions than support fork." |
| Ralph Wiggum loops (Huntley) | Fresh process per iteration, state on disk | `IMPLEMENTATION_PLAN.md`, `specs/`, `AGENTS.md` reloaded each loop. | Keep each run in the "smart zone" (~40–60% of usable context); avoid compaction entirely. |
| OpenAI Symphony | One thread per worker process; fresh thread on redispatch | Reuses `thread_id` only for continuation turns *inside* one run. Workspace persists, `WORKFLOW.md` re-rendered. | Cross-process resume deliberately not attempted. |
| OpenHands | Long session + LLM condenser | Condenses at thresholds to keep the cache warm. Measured: per-turn cost under half, SWE-bench 54% vs 53%. | Within-task only. Condensation loops on browsing tasks (#8282). |
| Cline / Roo Code | Handoff to fresh task + repo memory files | `new_task` preloads a distilled context; Memory Bank markdown in repo. | "Crisp handoff when history feels heavy with exploration." |
| Aider | Deterministic repo index | tree-sitter repo map, mtime-keyed disk cache, PageRank-ranked to a token budget. | Zero LLM cost for discovery. Map, not semantics. |
| Devin, Cursor cloud agents, Copilot coding agent, Warp, Jules, Kiro | Fresh agent per run; environment snapshots; repo instruction/memory files | Snapshot the *disk* (Devin ~15 s create, ~10 s to first message), never the conversation. | Mature pattern: index + curated notes + fresh session. |
| vibe-kanban | `--resume` per task attempt | Stores `agent_session_id`. | Resume breaks when the worktree path changes because the transcript key is cwd (#2993). Exactly ATeam's per-run clone situation. |
| OpenCode | Resume/fork available | `opencode run -c|-s <id> --fork`. | Parity feature, no rationale. |
| Cognition ("Don't build multi-agents") | Single thread, share full traces | "Actions carry implicit decisions." | Parallel workers from a shared brief diverge on implicit decisions. Applies to forks too: each fork inherits the overview's decisions and none of its siblings'. |
| metaswarm, Guild, Compound Engineering, SmithersBot, Archon (from `ResearchAgentFramework.md`) | Fresh + durable artifacts | Selective priming (`bd prime --files`), Brief/Oath handoff, `fresh_context: true` between loop iterations. | Context degradation avoidance is the stated reason. |

A GitHub code search for `--fork-session` and `forkSession` (counts not independently re-verified) turns up mostly flag pass-through in wrappers. The only fan-out uses found are gascity's warm arm and agentpool's `FORKING.md` (ephemeral queries, A/B, "subagent with current context"; notes fork is a one-way snapshot). Nobody publishes measured savings from forking.

**Consensus, with counterexamples.** For unattended or cross-process work most orchestrators converged on fresh context plus durable written artifacts, with a deterministic index underneath where discovery is expensive. Session fork is used successfully in two shapes: (1) read-only interrogation of a finished session, and (2) same-run fan-out immediately after a shared prefix is built, within the TTL, same cwd/model/tools. Counterexamples exist: vibe-kanban resumes per task attempt (and hit the cwd-key problem), OpenHands measured a within-task win from condensation, and Claude Code itself productised cross-break resume for Pro/Max users as the "Resume from a summary" dialog for sessions idle over an hour and above 100k tokens. That last one is telling: Anthropic's answer to resuming a large idle session is to *summarise it into a fresh one*, which is the artifact pattern. The one orchestrator that resumes sessions across runs unconditionally (Paperclip) has the most severe documented failures.

## 4. ATeam constraints that bear on this

Facts from the code and docs, with pointers.

- **Launch shape.** Claude roles run as `claude -p --output-format stream-json --verbose ...` with the prompt on stdin (`defaults/runtime.hcl:243`, `internal/agent/claude.go:86` and `:106`). Codex runs `codex exec --json ... <prompt>` (`internal/agent/codex.go:83`). The only flag the runner injects per run is `--settings` for the sandbox (`internal/runner/runner.go:506`); ATeam never passes `--add-dir` or `--append-system-prompt`. Every role in `ateam report` is a `flow.Parallel` step with bounded workers (`cmd/report.go:347`, `internal/flow/flow.go:807`). Nothing today passes a session id between steps; `ateam resume` recovers it on demand from the run's stream file (`cmd/resume.go:283`) and it is not a calldb column.
- **Session id capture already works.** The Claude `system/init` event's `session_id` and Codex `thread.started` are parsed and stored in the stream (`internal/agent/claude.go:184`, `internal/agent/codex.go:332`). A fork step would read the parent's id the same way `ateam resume` does.
- **Where transcripts live per isolation mode.**
  - *Sandbox (default)*: host `~/.claude` or the agent's `config_dir` (`ISOLATION.md:52`). Transcripts persist. The cwd is the project directory, stable across runs. Fork and resume are feasible here. Note that `config_dir = ".claude-{{ROLE}}"` (`CONFIG.md:538`) puts each role in a *different* transcript store, so cross-role forking requires all forked roles to share the overview's config dir.
  - *Docker one-shot*: at most `~/.claude/.credentials.json` is mounted, read-only and only with `mount_claude_config = true` (`ISOLATION.md:96-103`). `~/.claude/projects` is inside the container and destroyed with it. Fork within one run would need every forked role to run in the *same* container, which is not how the runner works today (one container per exec, `internal/container/docker.go:15`). Resume across runs is impossible without mounting a transcript volume.
  - *Docker exec (persistent container)*: transcripts persist in the container's home, cwd `/workspace` is stable. Feasible, but `ateam resume` already refuses `--launch` here because the session lives in the container.
  - *ATeam inside Docker*: same as sandbox from the agent's point of view.
- **Cache prefix is per cwd and git snapshot.** Any mode where the run cwd changes (a future per-exec clone, a worktree, a fresh container) makes the forked prefix a full cache miss. vibe-kanban hit exactly this (#2993).
- **Flags are not restored on `-p --resume`.** ATeam's `--settings` sandbox file must be re-passed on every fork or resume, and Codex `exec resume` cannot take `-s` at all (whether `exec fork` accepts `-s` and `--skip-git-repo-check`, which ATeam passes, was not checked). The isolated-config guard at `internal/agent/claude.go:74-82` already requires `--settings`, so this fits, but it is a second code path to keep in sync.
- **System prompt snapshot.** Claude reuses the recorded system prompt on resume until compaction. If a role prompt or `_pre` fragment changes between cycles, a resumed session keeps the old one (Paperclip #3596 is this failure).
- **Concurrency.** `CONCURRENCY.md` rule 6 gives each exec its own runtime dir and rule 3 clones agent config at one boundary. N forks of one session are N independent `claude` processes with N different session ids, so the pool contract holds. The only new shared resource is the parent transcript file, which forks read once at start.
- **Flow shape.** Idea A needs the overview step to *finish* before any role starts, so `ateam report` changes from a single `flow.Parallel` to a `flow.Pipeline{overview, Parallel{roles}}` and the roles' `RunOpts` gain a parent session id. The flow types support that composition already; the new work is the session-id plumbing and the extra `--resume <id> --fork-session --settings ...` argv path in the Claude agent.
- **ATeam already has the warm-context mechanism, and it is measured.** `{{dynamic.previous_report}}` inlines the role's prior report, and `{{dynamic.project_info}}` injects a deterministic orientation block (`CONFIG.md:332-335`, `internal/root/resolve.go:295`). `plans/Feature_TokenReduction.md` measured cold vs warm across 16/14 runs:

| Metric | Cold | Warm (prior report inlined) | Δ |
|---|---|---|---|
| Total cost | $41.5 | $13.0 | −69% |
| Cache-read tokens | ~50M | ~8M | −84% |
| Median tool calls per role | ~50 | ~10 | −80% |
| Findings lost | | 0 | |

  `code.structure` on the same commit went from 65 tool calls / $4.80 (clean cold) to 5 tool calls / $0.71 (warm, prior warm report inlined, `Feature_TokenReduction.md:36`), and the warm run with orientation block plus tightened prompts came in at 5 turns / $0.85 (`debug_cold_warm_report.md:145`). The discovery cost that ideas A to C target is the cold-run warmup, and this data says a written artifact already removes most of it. The remaining measured gap is the *first* cycle in a project and roles like `code.bugs` that wander for reasons unrelated to orientation.

## 5. Evaluation of the ideas

### A. Overview agent, then fork report roles (same run)

What it would buy: each role starts with the overview's tool results in context and shares the cache write. It skips the 6 to 10 orientation calls a cold role makes.

What it costs and risks:

- Every fork carries the whole overview transcript on every turn, at cache-read price if the fork starts inside the TTL and nothing invalidates the prefix. Each forked `-p` process is a main-conversation request, so on subscription auth within plan usage the window is 1 h; on API-key auth or usage credits it is 5 min unless `promptCacheTtl=1h` is set (which doubles the write price). With `max_parallel` smaller than the role count, later roles start after earlier ones finish, and each cache hit refreshes the TTL, so the risk is a gap longer than the window between batches, not the batch itself.
- Thundering herd on the first batch: N forks launched in the same second all send the same uncached prefix before the first cache write lands, and each pays the write. Anthropic's workflow fan-out staggers same-prefix agents by up to 5 s for exactly this; a fork design needs the same stagger, or the overview step must make one trivial turn to land the write before the fan-out.
- Ordering versus `previous_report`: Claude's cache is prefix-exact. The role prompt comes *after* the forked history, so the per-role prompt sits at the tail and is fine. Any per-role `--settings` difference that changes the recorded system prompt busts the prefix for that role.
- Claude only. Codex has `exec fork`, but a Codex role cannot fork a Claude overview. ATeam's cross-model review (Claude drafts, Codex reviews) needs the artifact form anyway.
- Sandbox and docker-exec modes only. Docker one-shot cannot do it without restructuring the runner to keep a container alive across the fan-out.
- Implicit-decision inheritance (Cognition). If the overview agent mis-characterises something, all N roles inherit it in a form they treat as their own observation, not as a claim to verify. The warm-report data already shows this: two confidently wrong "facts" in a prior report's Project Context survived into the next run. A written artifact can be labelled "generated, verify before relying"; a forked transcript cannot.
- Not composable with the existing warm path. A fork of a 60k-token overview plus an inlined previous report doubles up.

Verdict: sound, narrow, and it has to beat a 5k-token orientation file that is already there. On carried-token cost alone the fork can win for short warm roles (§2 break-even); it loses on TTL fragility, mode coverage, cross-model reuse, and inherited unverifiable observations. It is worth one A/B experiment, not a feature commitment.

### B. Checkpoint, then resume next cycle with "check git diff since `<sha>`"

- No cost saving. After the TTL the entire transcript is re-sent at full price, then carried at cache-read price on every turn of the new run. A day later the checkpoint is strictly more expensive than a fresh run with a 5k-token summary.
- Stale evidence cannot be retracted. The transcript contains old file contents as tool results. "Check what changed since X" adds new reads on top of old ones; the model now holds two versions of the same file and the old one is the one it "read". This is Paperclip #635 in slow motion.
- Frozen system prompt (Claude) and frozen sandbox policy (Codex #40149). Prompt changes between cycles are silently ignored until compaction.
- Unbounded growth. Each cycle adds turns; after enough cycles you hit auto-compact (lossy) or "Prompt is too long" (unrecoverable). Transcripts also expire at 30 days.
- Isolation: impossible in docker one-shot, fragile anywhere cwd changes.
- The git-diff-since-sha instruction is the right idea, but it belongs in the *prompt* of a fresh run with a written baseline, which is the `previous_report` mechanism plus a base SHA stamp. Symphony, Ralph, Anthropic's harness posts and Amp all landed here.

Verdict: do not build. Every project that tried cross-run session reuse either abandoned it (Amp removed fork), never attempted it (Symphony), or documented serious failures (Paperclip).

### C. Report-discovery first, fork from mid-run

Same as A, but the fork point is inside a role's run rather than a dedicated overview. `--resume-session-at <msg-uuid>` makes it technically possible on Claude, and Codex `thread/fork` takes `lastTurnId`. It is worse than A: the discovery prefix is contaminated with one role's framing, the fork point has to be chosen by parsing the transcript, and the parent role has been running for minutes so the TTL clock is already ticking for siblings. Skip.

### D. Crash recovery: resume the failed session instead of restarting

This is the strongest use of resume in the whole document, because it has every property §2 and §3 say a resume needs and none of the ones that sink B:

- Same cwd, same process config, same model, same tools. The prefix is cache-exact.
- The failure just happened, so the cache is warm. A restart pays the whole prefix uncached; a resume pays cache-read (0.1×, 0.025× on Fable 5.1) for everything already done.
- The transcript carries state no artifact can: which files were read, what was concluded, and for `code` runs which edits were already applied. A fresh agent facing half-applied edits is worse than the crash. A resumed one knows what it did.
- It runs once, immediately, so TTL drift and transcript growth across cycles do not apply.
- Anthropic built the plain case in: `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1` auto-continues a SIGTERM-cut turn on the next `-p --resume`. The rewind case is `--resume <id> --fork-session --resume-session-at <msg-uuid>`, which leaves the failed transcript intact for `ateam inspect`. Codex `exec resume <id> "<prompt>"` covers resume-in-place; rewind-to-turn exists only in the app-server `thread/fork lastTurnId` (not confirmed for the `exec fork` CLI).

Concrete illustration: while producing this document, an exploration agent died twice on HTTP 529 overload and was restarted from zero both times.

Design shape, by failure class:

| Failure | Action | Prompt on resume |
|---|---|---|
| API error (429/529/5xx), network, process killed mid-turn, timeout | `--resume <id>` in place, after a back-off | Nothing, or "continue where you left off". With the interrupted-turn env var, Claude continues the cut turn itself. |
| Bad tool call (ateam-classified: sandbox denial, command not found, wrote outside the runtime dir) | First retry: resume in place with a note naming the failed call and why. Second identical failure: `--fork-session --resume-session-at` a few turns earlier, same note. | "Your call `X` failed because Y. Do not repeat it; do Z instead." |
| Repeated identical call (loop) | Fork earlier immediately; the poisoned turns are the problem. | Same note plus the loop count. |
| Auth, permission, "Prompt is too long" (#14472), budget exhausted | Not resumable. Fail the run. | |

Caveats that shape the implementation:

- **Docker one-shot cannot do it.** The transcript dies with the container. Sandbox, docker-exec and ateam-inside-docker can. Either accept the gap or mount a transcript volume (`CLAUDE_CONFIG_DIR` / `CODEX_HOME`) for that profile.
- **Re-pass what resume does not restore.** `--settings` for Claude (the runner already builds it, `internal/runner/runner.go:506`), `-c sandbox_mode=...` for Codex because `exec resume` has no `-s` (#40149). Same `--model`; a resumed session restores it from the transcript, but passing it keeps the cache prefix identical.
- **Budget resets per process.** `--max-budget-usd` starts fresh on the resumed process. The retry needs an ateam-side cumulative cap per logical run, a retry limit (2 or 3), and a decay: in place, then rewind, then give up.
- **Classification is the real work.** The resumable/not decision belongs next to the existing failure classification in `internal/runner` (`classify_test.go`, the supervisor SIGTERM handling). Exit 143 and `result.is_error` with `error_during_execution` are the Claude signals; `turn.failed` and `error` events are Codex's.
- **Rewind needs message uuids.** Claude's stream-json events carry them, but whether ateam's parsed stream keeps the field was not checked. A first version can skip rewind entirely: resume at the end with the warning note. The model sees its own failed call in context, and that is usually enough. Rewind earns its keep for loops.
- **Session id must be stored.** `ateam resume` today scans the stream file (`cmd/resume.go:283`). A calldb column, already listed as hygiene, becomes a prerequisite. The retry should be a new exec row linked to the parent so cost roll-ups and `ateam ps` show the chain.
- **After auto-compact** the first resumed request lands in the 5 min cache bucket. Minor.

Verdict: build it. It is the one shape where resume is strictly better than restart on cost, speed and correctness, and it needs no experiment to justify.

### Other ideas in this area that hold up better

- **Read-only "seance" for review and verify.** Fork the *report* agent's finished session with `--fork-session --resume <id>` and ask it questions: "which files did you actually read for finding 3?", "was X verified or inferred?" The parent is untouched, the fork has all the evidence in context, and it runs once so TTL is irrelevant (it pays the cold re-read once). This is what Gas Town's `seance` does and what `ateam resume` already does interactively. An unattended `review --interrogate` step that forks the reporter and asks a fixed question list would directly attack the "re-verification claims have no audit" gap listed in `Feature_TokenReduction.md`. This is the one place where a fork beats an artifact: the artifact is what is being audited.
- **Fork the code session for verify.** `code_verify` today reads the diff cold. Forking the coding session gives verify the agent's own rationale for each change. Same read-only shape, same cost profile (one cold re-read), but Cognition's warning applies in reverse: a fork of the coder is biased toward the coder's view. Better as a second signal next to the cold verify than as a replacement.
- **Handoff prompt instead of resume.** Amp's `/handoff` is the mechanical form of what `previous_report` does: ask the finishing agent to write "what the next run should know, and which files matter", stamped with the base SHA. ATeam's `Project Context` section already is this. The improvement is structure (base SHA, file list with mtimes, "verified vs inferred" tags), not transcript reuse. `Feature_TokenReduction.md` Phase 2 already plans this.
- **Cache-stagger without forking.** Anthropic's workflows fan-out gets shared-cache benefits by staggering same-prefix agents by a few seconds. ATeam roles share the `_pre` fragments and `project_info` header but differ in the role body, and `-p` puts the prompt in the first user turn, so the shared prefix is only the system prompt. Reordering so the shared part is as long as possible and launching the first batch within seconds is free and applies in every mode. Measure whether `cache_creation` tokens drop.
- **Persist `session_id` in calldb.** Cheap, useful for any of the above and for `ateam resume`, and removes the stream-file scan.

## 6. Recommendation

1. **Build D, crash recovery by resume.** Store `session_id` in calldb, classify failures as resumable or not in the runner, and on a resumable failure relaunch with `--resume <id>` plus the same `--settings` and `--model`, with a cumulative budget and a retry cap. Start with resume-in-place and a warning note; add the `--fork-session --resume-session-at` rewind for loops later. Sandbox and docker-exec profiles first; docker one-shot needs a transcript mount. Set `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1` for Claude runs.
2. **Keep the artifact path as the source of truth** for cross-run context. It works in every isolation mode, across models, survives crashes and prompt edits, is auditable in git, and the measured −69% already exists. Finish the `Feature_TokenReduction.md` Phase 0.5 / Phase 2 work (deterministic orientation, structured Project Context with base SHA and provenance) before adding any other session mechanics.
3. **Do not build B.**
4. **Run one cheap A/B on A** before deciding: sandbox mode, Claude, subscription auth within plan usage (1 h main-conversation TTL; `subagentPromptCacheTtl` is irrelevant because forked roles are separate processes), `max_parallel` at least the role count, forks staggered by a few seconds. First measure the overview transcript size and per-role turn counts, since §2 shows break-even depends on both. Then compare three arms on the same commit with `ateam_base`: (i) warm roles with `previous_report` + `project_info` as today, (ii) overview agent then `--fork-session` roles with `--ignore-previous-report`, (iii) both. Read `cost_usd`, `cache_read_tokens`, `cache_creation_tokens`, tool calls, and finding counts from `state.sqlite` as in `plans/debug_cold_warm_report.md`. Decision rule: adopt A only if arm (ii) or (iii) beats (i) on cost with no findings lost *and* the wrong-fact inheritance risk is judged acceptable. Cost alone may well favour (ii) for short roles; the experiment is there to put a number on it.
5. **Prototype the read-only interrogation fork** for review or verify. It shares the session-id plumbing with D, needs no TTL luck, and reuses `resolveSessionID` and the `resume` command line from `cmd/resume.go`. Sandbox and docker-exec only; skip in docker one-shot.
6. **Do not add `--bare` blindly:** bare mode "never reads OAuth credentials or the system keychain" and needs `ANTHROPIC_API_KEY` or an `apiKeyHelper`, so it breaks the default subscription auth and moves runs to the 5 min TTL bucket. It is only an option for the `docker-api` profile.

## Sources

Claude Code and Agent SDK: [cli-reference](https://code.claude.com/docs/en/cli-reference), [headless](https://code.claude.com/docs/en/headless), [sessions](https://code.claude.com/docs/en/sessions), [prompt-caching](https://code.claude.com/docs/en/prompt-caching), [costs](https://code.claude.com/docs/en/costs), [context-window](https://code.claude.com/docs/en/context-window), [checkpointing](https://code.claude.com/docs/en/checkpointing), [worktrees](https://code.claude.com/docs/en/worktrees), [sub-agents](https://code.claude.com/docs/en/sub-agents), [agent-teams](https://code.claude.com/docs/en/agent-teams), [memory](https://code.claude.com/docs/en/memory), [workflows](https://code.claude.com/docs/en/workflows), [agent-sdk/sessions](https://code.claude.com/docs/en/agent-sdk/sessions), [agent-sdk/session-storage](https://code.claude.com/docs/en/agent-sdk/session-storage), [CHANGELOG](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md), issues [#14472](https://github.com/anthropics/claude-code/issues/14472), [#48835](https://github.com/anthropics/claude-code/issues/48835), [#61058](https://github.com/anthropics/claude-code/issues/61058), [#81304](https://github.com/anthropics/claude-code/issues/81304). API pricing: [platform prompt-caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).

Anthropic engineering: [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents), [Harness design for long-running apps](https://www.anthropic.com/engineering/harness-design-long-running-apps), [Effective context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents), [Multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system), [Prompt caching is everything](https://claude.com/blog/lessons-from-building-claude-code-prompt-caching-is-everything).

Codex: [non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode), [app-server](https://learn.chatgpt.com/docs/app-server), [CLI commands](https://learn.chatgpt.com/docs/developer-commands?surface=cli), [config reference](https://learn.chatgpt.com/docs/config-file/config-reference), [memories](https://learn.chatgpt.com/docs/customization/memories?surface=cli), issues [#11750](https://github.com/openai/codex/issues/11750), [#40149](https://github.com/openai/codex/issues/40149), [#4791](https://github.com/openai/codex/issues/4791), [#15538](https://github.com/openai/codex/issues/15538), source `codex-rs/core/src/client.rs`, `codex-rs/rollout/src/recorder.rs`, `codex-rs/core/src/thread_manager.rs`. OpenAI API: [prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching), [conversation state](https://developers.openai.com/api/docs/guides/conversation-state), [compaction](https://developers.openai.com/api/docs/guides/compaction).

Other projects: [Paperclip claude-local adapter](https://github.com/paperclipai/paperclip/blob/master/docs/adapters/claude-local.md) and issues #3596, #635, #5462; [Gas Town seance.go](https://github.com/gastownhall/gastown/blob/main/internal/cmd/seance.go), [gascity](https://github.com/gastownhall/gascity); [Amp handoff](https://ampcode.com/news/handoff), [Amp removes fork](https://ampcode.com/news/stick-a-fork-in-it); [Ralph Wiggum](https://github.com/ghuntley/how-to-ralph-wiggum); [Symphony SPEC](https://github.com/openai/symphony/blob/main/SPEC.md); [OpenHands condensation](https://www.openhands.dev/blog/openhands-context-condensensation-for-more-efficient-ai-agents); [Cline new_task](https://cline.bot/blog/unlocking-persistent-memory-how-clines-new_task-tool-eliminates-context-window-limitations); [Roo condensing](https://docs.roocode.com/features/intelligent-context-condensing); [Aider repo map](https://aider.chat/2023/10/22/repomap.html); [Devin environment](https://docs.devin.ai/onboard-devin/environment); [Cursor cloud agent builds](https://cursor.com/docs/cloud-agent/builds); [vibe-kanban #2993](https://github.com/BloopAI/vibe-kanban/issues/2993); [OpenCode CLI](https://opencode.ai/docs/cli); [Cognition: Don't build multi-agents](https://cognition.com/blog/dont-build-multi-agents); [compaction comparison gist](https://gist.github.com/badlogic/cd2ef65b0697c4dbe2d13fbecb0a0a5f); [agentpool FORKING.md](https://github.com/phil65/agentpool).

ATeam internal: `docs/ResearchAgentFramework.md`, `plans/Feature_TokenReduction.md`, `plans/debug_cold_warm_report.md`, `plans/Research_InvestigateReportLogsForTokenUsage.md`, `ISOLATION.md`, `CONCURRENCY.md`, `cmd/resume.go`, `internal/agent/claude.go`, `internal/agent/codex.go`.
