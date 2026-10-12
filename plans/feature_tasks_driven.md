# Feature: Task-driven quality pipeline

## Direction

- Replace report/review/task-file handovers with one project-scoped mdtrack
  store. Markdown reports, reviews, execution summaries and UI pages become
  derived views; no phase reads them back as authoritative state.
- Replace the coding supervisor's file extraction, delegation and bookkeeping
  with a deterministic Go scheduler. Keep agent judgment in report/review/verify.
- Keep findings as stable tasks across rounds. The task body holds evidence,
  locations, proposed action and acceptance criteria; comments hold report
  updates, review reasoning, coding results and verification outcomes.

## Task model

- Use mdtrack's existing statuses: `maybe`, `todo`, `doing`, `blocked`, `done`.
  Stage tags describe workflow progress; `done` alone does not mean coded.
- New finding: `maybe + reported`. Reviewed and approved: `todo + reviewed`;
  retain `reported` as provenance. Deferred: `maybe + reviewed + deferred`.
  Rejected/duplicate: `done` with a reason tag and a link to the surviving task.
- Harness claim: `doing + reported + reviewed + selected`, with `owner` naming
  the execution. `selected` means claimed, not another review decision.
  Completion: `done + coded`, clear `selected`/owner; retain prior stage tags.
  Verification adds `verified`, tied to the inspected commit range.
- Each general observation: `maybe + report-context`, tagged with its role;
  review carryover: `maybe + review-context`. Update stable IDs; retire obsolete
  context as `done + superseded`. Context has no workflow tags/dependencies and
  never enters review's finding queue or code/verify queues.
- Scope every query by project/workspace. Use role tags, `author`, `batch` and
  comments for provenance; use `priority`/`effort` (`low|med|high`) for ordering
  and `blocked_by` for prerequisites. Record execution IDs and commit ranges
  in outcome comments; `git_hash` records the latest completed implementation.

## Handovers

- **Old report → report:** embed that role's open findings, active report
  context and recent completions. Update existing IDs or add new findings;
  do not recreate unresolved findings each round or reset ongoing coding work.
- **Report → review:** query `reported AND NOT reviewed`, excluding context
  and closed tasks. Review deduplicates, prioritizes and makes each approved
  task independently executable, including acceptance criteria and dependencies.
  Splits/merges retain links to the original findings.
- **Old review → review:** embed deferred/open reviewed tasks, their decision
  comments, active review context and recent completions. Reconsider deferrals
  explicitly; append reasoning to the same task. Materially changed findings
  need re-review before becoming eligible again; never rewrite an active claim.
- **Review → code:** query `todo AND reviewed`, excluding context, `deferred`,
  `needs-attention` and live claims. Sort by priority, then creation time/ID;
  atomically recheck and claim before dispatch. Repeat as dependencies unblock,
  within batch limits.
- **Code → verify:** query `done AND coded AND NOT verified`, including pending
  work from earlier batches. Embed full tasks, review/code comments, recorded
  commit ranges and test evidence. Pass: add `verified`; failure: reopen for
  coding (or re-review if scope changed), clearing stale `coded`/`verified` tags.

## Partial work and memory

- `doing + selected` is owned/incomplete work, never a fresh coding candidate.
  Report/review see its progress but do not duplicate or reassign it. The
  harness reconciles the execution and worktree before bounded continuation;
  exhausted retries become `blocked + needs-attention`, with recovery refs.
- Absence of `verified` is never evidence of failure or a prerequisite for
  reporting, review or coding. mdtrack unblocks dependencies when prerequisites
  become `done`; verification is a separate queue. Actual verification failure
  reopens the task and triggers dependency reconsideration.
- Include a bounded recent-completion digest in report/review, with details
  for relevant tasks, whether verified or not. Use completion history/batch,
  not merely `updated_at`; a new comment must not make old work “recent”.
- Keep deferral/retry holds explicit: dependency cascades back to `todo` must
  not silently approve deferred work or bypass exhausted retries.

## Harness and agent responsibilities

- Ateam owns queries, IDs/provenance, transitions, claims, retries, budgets,
  git/test checks and outcomes. Start with sequential coding; parallel coding
  requires isolated worktrees and an integration policy.
- Publish validated report/review submissions atomically per run (per role for
  reports); reject stale task revisions. Failed runs must not publish a partial
  ready queue. Make writes idempotent by execution.
- Coding agents receive the full task and relevant comments in their prompt;
  they implement, test and submit one outcome. The harness checks execution,
  commit and worktree evidence before marking `done + coded`; exit zero alone
  is insufficient. Preserve partial work and retry evidence in comments.
- Use mdtrack's digest/full-Markdown renderers to assemble prompts. Agents only
  query deeper context when needed; offer thin `ateam task` commands that supply
  the store, actor and execution automatically. Proposed write shape:

  ```sh
  ateam task outcome "$TASK_ID" --result completed <<'EOF'
  Implemented the fix. Tests: ...
  Commits: ...
  Remaining work: ...
  EOF
  ```

- Agents can also use the mdtrack CLI for task reads/comments. Lifecycle changes
  go through ateam's validated operations; agents do not manage scheduler tags.
  Containers/worktrees must reach the same canonical store, never copied DBs.

## Task IDs in `exec` and `exec-parallel`

- Proposed entry points: `ateam exec --task ID` runs one task;
  `ateam exec-parallel --task ID1 --task ID2` runs one agent per task, using
  the existing `parallel` machinery (`exec-parallel` becomes an alias).
  Embed task bodies/comments in the prompts; optional prompt text adds instructions.
- Before each agent starts, atomically claim its task: set `owner` to the
  execution ID and status to `doing`. Claim only after acquiring a worker slot
  and checking budgets; queued or undispatched tasks remain unchanged. Reject
  duplicate IDs, existing owners, non-`todo` tasks and unresolved dependencies.
  Explicit task execution does not require the quality pipeline's `reviewed` tag.
- By default, on successful task completion, mark `done`, clear ownership and record the
  outcome. Require a completed outcome and successful execution; timeout,
  cancellation, launch failure or partial work must never imply completion.
  Update only if the execution still owns the task. Parallel outcomes apply
  independently; one failed agent does not undo successful siblings.
- Proposed CLI controls:
  - `--task-state auto|claim-only|none` (default `auto`): claim and complete;
    claim but leave `doing` for later completion; or use tasks only as prompt
    context with no task writes. Without task IDs, existing behavior is unchanged.
  - `--task-on-failure keep|todo|blocked` (default `keep`): preserve `doing`
    and ownership for recovery; release to `todo` after safe cleanup; or release
    as `blocked + needs-attention`. Applies only when this execution claimed
    the task; retain failure details and reconcile stopped executions on restart.
  - Existing `--max-parallel`, budget and `--dry-run` controls apply; dry-run
    previews prompts and transitions without claiming or changing tasks.
- Reuse the same lifecycle hooks in `ateam code`. Generic task execution does
  not add `reported`, `reviewed`, `coded` or `verified`; coding completion adds
  `coded` only through the code workflow's commit/test checks.

## Integration prerequisites

- Expose a supported mdtrack Go package: its store/parser currently live under
  `internal/`, and rendering lives in `cmd/`. Share CLI/library validation,
  rendering and dependency transactions; add conditional claims/revision checks.
- Use explicit library predicates: repeated mdtrack `--tag` flags mean OR.
- Replace file-reading prompt dynamics and file-output validation with task
  queries/submissions. Import existing artifacts once; keep optional Markdown
  exports for convenience, with task IDs and no continuing file synchronization.
