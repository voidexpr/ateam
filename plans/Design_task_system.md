# Design: Task System

## Prerequisite: Rename agent_execs → agent_runs, task_group → run_group

Before adding the task table, rename existing DB concepts to avoid confusion with the new "task" meaning (findings from reports). The word "task" will mean a finding; agent executions are "runs".

### Design challenges evaluated

**Challenge 1: Do we need a `run_groups` metadata table?**

Original idea: add a `run_groups` table with structured fields (label, timestamp, extra_prompt, profile, roles) instead of parsing the `"report-2026-04-11_08-48-05"` string.

Decision: **Deferred.** The table adds a join to every query and a write on every group creation. The string format already encodes label + timestamp, and `parseTaskGroupTimestamp()` works today. Add the table later when commissioning (needs to query by label) or the web UI (would display extra_prompt) actually needs it. The rename is the essential prerequisite; the metadata table is not.

**Challenge 2: Alternative naming — call findings table `findings` instead of `tasks`?**

If the table were called `findings`, `task_group` wouldn't conflict. But "finding" doesn't describe the full lifecycle (open → approved → coded → done). At the coding phase, they're work items. "Task" is the right general term.

Decision: **Keep `tasks` as the table name, rename `task_group` → `run_group`.**

**Challenge 3: Env var leakage into test processes** (see "Why not env vars" in Input Format section below).

The original plan used `ATEAM_REPORTER` and `ATEAM_RUN_GROUP` env vars. Research confirmed that `buildProcessEnv()` in `agent.go:130` inherits the full parent environment — any env var leaks into test processes. Every CI/CD system has this same problem (GitHub Actions leaks `INPUT_*`, `GITHUB_*`; Buildkite leaks `BUILDKITE_*`). No standard mitigation exists except container isolation (Dagger).

Decision: **Don't use env vars. Use prompt template injection** — inject reporter and run-group as literal CLI flags in the prompt. Zero leakage risk. ~10 extra tokens per finding ($0.0002). Details in Input Format section.

### Renames

| Current | New | Why |
|---------|-----|-----|
| Table `agent_execs` | `agent_runs` | "Exec" is vague; these are agent runs |
| Column `task_group` | `run_group` | Avoids collision with `tasks` table |
| CLI `--task-group` | `--run-group` | Consistent with DB |
| Go structs `TaskGroup` | `RunGroup` | Consistent with DB |
| `CostByTaskGroup()` | `CostByRunGroup()` | Consistent |
| `CallsByTaskGroup()` | `CallsByRunGroup()` | Consistent |
| `LatestTaskGroup()` | `LatestRunGroup()` | Consistent |
| `TaskGroupRow` | `RunGroupRow` | Consistent |

### Migration

Same pattern as existing `agent_calls` → `agent_execs` migration (`calldb.go:149-206`):

```sql
ALTER TABLE agent_execs RENAME TO agent_runs;
ALTER TABLE agent_runs RENAME COLUMN task_group TO run_group;
DROP INDEX IF EXISTS idx_execs_task_group;
CREATE INDEX idx_runs_run_group ON agent_runs(run_group);
```

Detection: check for column `task_group` in `agent_execs` via PRAGMA.

### Affected files

`internal/calldb/calldb.go`, `internal/calldb/queries.go`, `internal/calldb/calldb_test.go`, `internal/runner/runner.go`, `cmd/report.go`, `cmd/code.go`, `cmd/run.go`, `cmd/parallel.go`, `cmd/runs.go`, `cmd/cost.go`, `cmd/tail.go`, `cmd/inspect.go`, `cmd/pool_status.go`, `internal/web/handlers.go`, `internal/web/templates/*.html`

This is mechanical — rename structs/fields/methods, update SQL strings, update templates. No logic changes.

### Future: run_groups metadata table

Deferred. When commissioning or web UI needs structured group metadata (extra_prompt, profile, roles, label), add a `run_groups` table joining with `agent_runs.run_group`. Schema sketch for when needed:

```sql
CREATE TABLE IF NOT EXISTS run_groups (
    id           TEXT PRIMARY KEY,   -- e.g. "report-2026-04-11_08-48-05"
    label        TEXT NOT NULL,      -- "report", "code", "review", "parallel", or custom
    started_at   TEXT NOT NULL,      -- ISO timestamp
    extra_prompt TEXT DEFAULT '',
    profile      TEXT DEFAULT '',
    roles        TEXT DEFAULT ''     -- comma-separated
);
```

---

## Input Format Decision

The agent currently outputs findings as markdown with structured fields (Title, Location, Severity, Effort, Description, Recommendation). The task CLI input should match this structure with minimal ceremony.

**Evaluated formats:**

| Format | Tokens per finding | Error risk | Multi-line description |
|--------|-------------------|------------|----------------------|
| Flags + stdin | ~55 | Medium (flag quoting) | Awkward (stdin is body only) |
| JSON pipe | ~60 | High (escaping quotes, \n in strings) | Bad |
| Frontmatter + body heredoc | ~45 | Low (no escaping, familiar pattern) | Natural |

**Winner: frontmatter + body via heredoc.** LLMs produce this pattern reliably (it's markdown frontmatter), multi-line descriptions work naturally, no escaping needed, and it's the most compact.

Common fields (reporter, run-group) are injected into the prompt template by ateam at prompt assembly time. The agent sees literal CLI flags in its instructions and copies them per finding. **No env vars are used** — this avoids leaking `ATEAM_*` variables into test processes run by the agent (see "Why not env vars" below).

```bash
# The prompt template includes the full command with flags baked in.
# The agent copies this per finding, changing only the frontmatter.
ateam task add --reporter security:report --run-group report-2026-04-11 <<'EOF'
subject: Secret env vars forwarded as -e KEY=VALUE docker arguments
severity: MEDIUM
effort: MEDIUM
location: internal/container/docker.go:162-165
---
Both container backends forward secret-bearing env vars as -e KEY=VALUE
arguments to docker run or docker exec. These are visible to other local
users via ps aux for the lifetime of the child process.

Recommendation: Use Docker's --env-file with a temp file (mode 0600,
deleted after launch) instead of per-variable -e KEY=VALUE args.
EOF
```

The prompt assembly (`AssembleRolePrompt`) injects the reporter and run-group values into the instructions template. The agent never constructs these values — it copies them from the prompt.

Also accept a file: `ateam task add --file finding.md --reporter security:report --run-group RG`.

### Why not env vars

`buildProcessEnv()` in `agent.go:130` inherits the full parent environment via `os.Environ()`. Any env var set by ateam (via `os.Setenv`) leaks into:
1. The agent subprocess (Claude)
2. Every tool call the agent makes (bash commands)
3. Every test process the agent runs (`go test`, `npm test`, etc.)

Sandbox settings do NOT filter env vars — they only restrict filesystem and network. This means `ATEAM_REPORTER=security:report` would be visible inside `go test ./...` for the project being audited. If tests check for a clean environment or use similarly-named variables, they break.

The prompt template approach costs ~10 extra tokens per finding (the flags) but has zero leakage risk. At 5 findings per report, that's ~$0.0002 — negligible.

### Parsing rules

- Lines before `---` are `key: value` pairs (trimmed, case-insensitive keys)
- Everything after `---` is the description (trimmed)
- If no `---`, everything is description and subject is required via flag
- Unknown keys are ignored (forward-compatible)
- Minimal required field: `subject`
- **Enum normalization**: ateam normalizes casing and common variations on input:
  - Severity: `high`, `HIGH`, `High`, `hi`, `h` → `HIGH`
  - Effort: `small`, `SMALL`, `sm`, `s`, `lo`, `low` → `SMALL`; `med`, `m`, `medium` → `MEDIUM`; `large`, `lg`, `l`, `hi`, `high` → `LARGE`
  - State: case-insensitive, e.g. `Done` → `done`
  - Priority: `p0`, `P0`, `0` → `P0`
  - This means agents can write `severity: high` or `severity: HIGH` — both work

---

## Schema

```sql
CREATE TABLE tasks (
    id            INTEGER PRIMARY KEY,
    subject       TEXT NOT NULL,
    description   TEXT,
    source_role   TEXT NOT NULL,
    source_action TEXT NOT NULL DEFAULT 'report',
    severity      TEXT,          -- CRITICAL, HIGH, MEDIUM, LOW
    effort        TEXT,          -- SMALL, MEDIUM, LARGE
    location      TEXT,          -- file paths, line numbers
    state         TEXT NOT NULL DEFAULT 'open',
    priority      TEXT,          -- P0, P1, P2
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    report_run_group TEXT,       -- run_group of originating report run
    code_run_group   TEXT,       -- run_group of code run that attempted this
    commit_hash   TEXT,
    session_id    TEXT           -- claude session for resume
);

CREATE TABLE task_comments (
    id         INTEGER PRIMARY KEY,
    task_id    INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author     TEXT NOT NULL,    -- "security:report", "supervisor:review", "user", "system"
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE INDEX idx_tasks_state ON tasks(state);
CREATE INDEX idx_tasks_source ON tasks(source_role);
CREATE INDEX idx_comments_task ON task_comments(task_id);
```

Lives in the existing `.ateam/state.sqlite` alongside the `agent_runs` and `run_groups` tables.

No CHECK constraints on severity/effort/state — agents may produce variations ("Med" vs "MEDIUM") and we'd rather store and normalize than reject.

---

## States

```
open ──→ approved ──→ in_progress ──→ done
  │          │              │
  │          │              └──→ failed ──→ approved (retry)
  │          │                      │
  │          │                      └──→ deferred / wontfix
  │          │
  └──→ deferred ──→ open (re-opened next cycle)
  │          │
  │          └──→ wontfix
  │
  └──→ wontfix
```

| State | Meaning | Who sets it |
|-------|---------|-------------|
| `open` | New finding, untriaged | Report agent via `task add` |
| `approved` | Approved for coding | Review agent or user |
| `in_progress` | Coding agent working | `ateam code` Go loop |
| `done` | Completed, commit recorded | Coding agent or `ateam code` |
| `failed` | Coding attempt failed | `ateam code` Go loop |
| `deferred` | Valid, not now — revisit later | Review agent or user |
| `wontfix` | Will not fix — permanent decision | User or review agent |

No enforced state machine. Any transition is allowed. Every state change auto-creates a comment recording what changed, by whom.

`deferred` vs `wontfix`: deferred tasks reappear in the next review cycle (the review agent sees them and can re-approve). Wontfix tasks are hidden from review unless `--include-wontfix` is passed.

---

## Dedup

**No dedup at add time.** Text hashing is brittle — one rephrased sentence and it's a "new" task. Fuzzy matching creates false positives. Both require complexity in `task add` for marginal benefit.

Instead, **dedup happens at review time.** The review agent already reads all tasks to triage them. Merging duplicates is a natural part of that job and costs negligible extra tokens since the agent is already reading the task list.

### task merge

```bash
ateam task merge 3 8          # keep #3, close #8
```

What it does:
- Appends to #3's comments: "Merged from #8: {#8 subject}\n{#8 description}"
- If #8 had different severity/effort, notes the difference
- Sets #8 state to `wontfix` with comment "Merged into #3"

The review prompt includes:
```
If you see duplicate or overlapping findings, merge them:
    ateam task merge TARGET_ID SOURCE_ID
This keeps the target, closes the source with a cross-reference.
```

### Why not dedup at add time

- **Hash on text**: "Fix SQL injection in auth" vs "SQL injection vulnerability in auth handler" — same finding, different hash.
- **Hash on location**: Works for file-scoped findings but fails for cross-cutting findings ("no rate limiting across the API") or findings that span multiple files.
- **Fuzzy matching**: False positives are worse than duplicates. A false merge loses a legitimate finding; a duplicate just creates a row the review agent closes in 2 seconds.

Keeping `task add` dead simple (always creates a new task, no matching logic) means it never silently drops findings.

---

## Comments

Every meaningful action creates a comment automatically:

```
State changed: open → approved (by supervisor:review)
State changed: approved → in_progress (by ateam code)
State changed: in_progress → done (by refactor_small:code, commit abc123)
Updated by security:report (severity HIGH→MEDIUM, description updated)
State changed: open → wontfix (by user)
```

Users and agents can also add explicit comments:

```bash
# Agent adds a comment
ateam task comment 5 "Tried parameterized queries but the ORM doesn't support them. Need to use raw SQL with manual escaping."

# User adds a comment
ateam task comment 5 "Check with the DB team before changing this"

# Comment on state change
ateam task update 5 --state deferred --comment "Blocked on ORM upgrade in Q3"
```

---

## CLI

### task add

```bash
# Frontmatter from stdin (most common — agent use)
ateam task add <<'EOF'
subject: Fix SQL injection in auth handler
severity: HIGH
effort: SMALL
location: internal/auth/handler.go:45
---
The handleAuth function constructs SQL queries using string concatenation.
Recommendation: Use parameterized queries.
EOF

# From file
ateam task add --file .ateam/supervisor/code/finding_01.md

# Minimal (subject only, for quick human use)
ateam task add --subject "Check auth module for rate limiting"

# Override env
ateam task add --reporter user --subject "Something I noticed"
```

Prints: `task #5 created (security, HIGH, SMALL)`
On dedup: `task #3 updated (security, severity MEDIUM→HIGH)`

### task list

```bash
ateam task list                              # open + approved (default)
ateam task list --all                        # all states
ateam task list --state approved             # specific state
ateam task list --state open,approved        # multiple states
ateam task list --role security              # from specific role
ateam task list --sort severity              # sort (default: id)
ateam task list --format table               # compact table (default for tty)
ateam task list --format full                # with descriptions (default for pipe/non-tty)
ateam task list --format oneline             # 1 line per task (for prompt injection)
ateam task list --format json                # for programmatic use
```

**`table`** — default when stdout is a terminal. For human scanning:
```
ID  STATE     SEV     EFFORT  ROLE              SUBJECT
 3  open      HIGH    SMALL   security          Secret env vars forwarded as -e KEY=VALUE
 5  open      MEDIUM  MEDIUM  security          Codex prompt visible in process table
 8  approved  HIGH    SMALL   refactor_small    Extract duplicate validation logic
12  deferred  MEDIUM  LARGE   testing_basic     Add integration tests for container module
```

**`full`** — default when piped (non-tty). This is what the review agent sees.
Includes descriptions so the supervisor can make triage decisions without calling `task show` per task:
```
#3 [open] security | HIGH | SMALL
Subject: Secret env vars forwarded as -e KEY=VALUE docker arguments
Location: internal/container/docker.go:162-165

Both container backends forward secret-bearing env vars as -e KEY=VALUE
arguments to docker run or docker exec. These are visible to other local
users via ps aux for the lifetime of the child process.

Recommendation: Use Docker's --env-file with a temp file (mode 0600,
deleted after launch) instead of per-variable -e KEY=VALUE args.

---

#5 [open] security | MEDIUM | MEDIUM
Subject: Codex prompt visible in process table
Location: internal/agent/codex.go:58

The full agent prompt is passed as a positional argument. Any local user
can read it via ps aux. Claude correctly uses stdin instead.

Recommendation: Switch to stdin or write prompt to a temp file.

---
```

Each task is ~50-80 tokens. 20 open tasks ≈ 1300 tokens — cheap for the review agent.
The `---` separator makes it parseable if we ever need to split programmatically.
The `#ID` prefix is prominent so the agent can reference it in `task update` calls.

**`oneline`** — for the compact task manifest injected into report prompts:
```
#3 [open] HIGH SMALL security: Secret env vars forwarded as -e KEY=VALUE
#5 [open] MEDIUM MEDIUM security: Codex prompt visible in process table
#7 [done] MEDIUM SMALL security: extra_volumes allows arbitrary host path mounts
```

**`json`** — array of task objects, for programmatic use.

### task show

```bash
ateam task show 3
```

Output (readable markdown):
```
# Task 3: Secret env vars forwarded as -e KEY=VALUE

State: open | Severity: HIGH | Effort: SMALL | Priority: —
Role: security:report | Created: 2026-04-11 08:48

## Location
internal/container/docker.go:162-165

## Description
Both container backends forward secret-bearing env vars as -e KEY=VALUE
arguments to docker run or docker exec. These are visible to other local
users via ps aux for the lifetime of the child process.

Recommendation: Use Docker's --env-file with a temp file (mode 0600,
deleted after launch).

## Comments
[2026-04-11 08:48] security:report — Created
[2026-04-11 11:37] supervisor:review — State: open → approved, priority P0
[2026-04-11 22:07] refactor_small:code — State: approved → failed
    Build error: docker_exec.go:74 undefined: writeTempEnvFile
[2026-04-12 user] — Needs a helper function first, defer to next cycle
[2026-04-12 user] — State: failed → deferred
```

### task update

```bash
ateam task update 5 --state approved --priority P0
ateam task update 5 --state deferred --comment "Blocked on ORM upgrade"
ateam task update 5 --severity HIGH            # change metadata without state change
ateam task update 5 --commit abc1234           # record commit (also sets state=done)
```

### task comment

```bash
ateam task comment 5 "Check with the DB team before changing this"
ateam task comment 5 --file notes.md           # longer comment from file
```

### task edit

```bash
ateam task edit 5                              # opens in $EDITOR
```

Opens a temp markdown file:
```markdown
# Task 5: Codex prompt visible in process table

state: open
severity: MEDIUM
effort: MEDIUM
priority:
---
Description text here...

# Comments (read-only below this line, add new comments above the line)
# ---
# [2026-04-11 08:48] security:report — Created
```

User edits state/severity/priority, optionally adds text above the `# ---` separator (becomes a new comment), saves. Ateam parses changes and applies them.

This is the "as easy as editing a markdown file" experience.

### task merge

```bash
ateam task merge 3 8                           # keep #3, close #8
ateam task merge 3 8 12                        # merge multiple into #3
```

### task clear

```bash
ateam task clear --state done                  # remove completed tasks
ateam task clear --state wontfix               # remove won't-fix
ateam task clear --older-than 30d              # age-based cleanup
ateam task clear --all                         # nuclear option
```

Deleted tasks get a final comment "Cleared by user" before deletion (or just archive to a `tasks_archive` table if we want history).

---

## How the pipeline uses tasks

### Report prompt changes

The report base prompt (`report_base_prompt.md`) changes from "produce a markdown report with findings" to:

```markdown
## Reporting Findings

For each finding, call ateam task add with a heredoc:

    ateam task add <<'EOF'
    subject: Short description of the finding
    severity: CRITICAL | HIGH | MEDIUM | LOW
    effort: SMALL | MEDIUM | LARGE
    location: file/path.go:line
    ---
    What the issue is, why it matters, and what to do about it.
    EOF

Call this once per finding as you discover them. Your reporter identity
and run group are set automatically.

After reporting all findings, produce a brief summary for the report file.
The summary should include an overall assessment and a reference to
`ateam task list` for the full findings.
```

The report.md becomes:
```markdown
# Summary
Security posture is reasonable. 6 findings reported, 2 HIGH, 4 MEDIUM.
Run `ateam task list --role security` for details.

# Project Context
...
```

Much shorter report → fewer output tokens per role.

### Compact task manifest replaces previous report

Instead of including the full previous report (hundreds of lines), include:

```
# Known findings for security (do not re-report unchanged findings):
 3  open      HIGH    SMALL   Secret env vars forwarded as -e KEY=VALUE
 5  approved  MEDIUM  MEDIUM  Codex prompt visible in process table
 7  done      MEDIUM  SMALL   extra_volumes allows arbitrary host path mounts
```

This is the output of `ateam task list --role security --all --format oneline`, generated by ateam and injected into the prompt. ~1 line per finding vs ~10-20 lines in the current prose.

### Review agent

The review prompt injects all open tasks via `ateam task list --state open --format full` directly into the prompt (same pattern as current report embedding, but structured tasks instead of prose). The agent then uses CLI calls to triage:

```markdown
## Your tools

    ateam task list --state open                # already included below, but you can re-query
    ateam task update ID --state approved --priority P0|P1|P2
    ateam task update ID --state deferred --comment "reason"
    ateam task update ID --state wontfix --comment "reason"
    ateam task merge TARGET_ID SOURCE_ID        # merge duplicates
    ateam task comment ID "your assessment"

## Instructions

The open tasks are listed below. For each, decide: approve for coding
(with priority), defer, or wontfix. Merge duplicates. Add comments
explaining cross-role interactions or concerns.

Produce a brief review summary for the review.md file.

## Open Tasks

{output of ateam task list --state open --format full}
```

The open tasks are embedded in the prompt so the agent doesn't need to spend a tool call fetching them. For 20 tasks at ~65 tokens each, that's ~1300 tokens — cheaper than the current approach of embedding 8 full reports.

### Code phase (Go loop)

```go
tasks := db.TasksByState("approved", "ORDER BY priority, severity DESC")
for _, task := range tasks {
    db.UpdateTaskState(task.ID, "in_progress", "ateam code")
    prompt := assembleCodePrompt(task) // code_base_prompt + task description
    result := runner.Run(ctx, prompt, opts)
    if result.Err == nil {
        commitHash := detectNewCommit()
        db.UpdateTaskState(task.ID, "done", "ateam code")
        db.SetCommitHash(task.ID, commitHash)
    } else {
        revertDirtyFiles()
        db.UpdateTaskState(task.ID, "failed", "ateam code")
        db.AddComment(task.ID, "ateam code", result.ErrMessage)
    }
}
```

### Coding agent prompt

The coding agent gets the task description as its primary input. It also calls `ateam task update` on completion:

```markdown
## Your task

{task.description}

Source: {task.source_role} | Severity: {task.severity} | Location: {task.location}

## On completion

After committing your changes:
    ateam task update {task.id} --state done --commit $(git rev-parse HEAD)

If you cannot complete the task:
    ateam task comment {task.id} "What went wrong and why"
```

Note: the Go loop also sets done/failed as a safety net, so the agent's `task update` call is belt-and-suspenders. If the agent forgets, the loop handles it.

---

## Review Prompt Rewrite

### Issues discovered while designing this

**Issue 1: The supervisor doesn't know which report roles failed.**

With the current system, all reports are files on disk — the supervisor reads whatever exists. If security timed out, there's simply no security report and the supervisor doesn't know whether security is clean or unanalyzed.

With tasks, it's the same problem but worse: if security timed out after creating 2 of 6 findings via `ateam task add`, those 2 appear as open tasks. The supervisor might think security found only 2 issues when the analysis was actually incomplete.

**Solution**: Ateam injects a **report health summary** into the review prompt. Generated from `agent_runs`, it shows which roles ran, which succeeded, which failed/timed out, and how many tasks each created. The supervisor can then defer decisions for roles with incomplete coverage.

```
## Report Health (auto-generated by ateam)
| Role            | Status   | Duration | Tasks created |
|-----------------|----------|----------|---------------|
| security        | ok       | 4m41s    | 6             |
| testing_basic   | ok       | 2m57s    | 3             |
| refactor_small  | ok       | 4m54s    | 5             |
| docs_internal   | timeout  | 20m      | 1 (partial)   |
| dependencies    | ok       | 3m35s    | 2             |
```

Implementation: `ateam review` queries `agent_runs` for the latest report run_group, counts tasks per role, formats the table, and injects it into the prompt.

**Issue 2: Failed coding tasks need context for re-assessment.**

After a code run, some tasks are `failed` with comments like "Build error: undefined function writeTempEnvFile." The next review needs to decide: retry (maybe the code changed), defer, or wontfix. The supervisor needs the failure reason visible.

**Solution**: Show failed tasks with their latest comment in a separate section. The `full` format already includes comments, but explicitly grouping failed tasks highlights them for re-triage.

**Issue 3: Deferred tasks accumulate across cycles.**

A task deferred 5 cycles ago keeps appearing. The supervisor re-defers it every time, wasting tokens.

**Solution**: Show deferred task age and last deferral reason. After N cycles (configurable, default 3), suggest wontfix. The prompt says: "Tasks deferred for 3+ cycles should be resolved — either approve or wontfix."

**Issue 4: Interrupted review leaves mixed state.**

If the review agent crashes after approving 3 of 10 tasks, 7 remain `open`. The next review sees a mix. It might contradict the previous review's decisions.

**Solution**: Show recently-approved tasks as context (read-only, not for re-triage). The prompt says: "Tasks already triaged in this cycle are shown for context. Do not re-triage them unless you see a specific reason."

Detection: if the latest review run_group has runs in `agent_runs` that are `error` status, it was interrupted. Ateam checks this and adds a note: "The previous review was interrupted. Some tasks may already be triaged."

**Issue 5: What goes in review.md now?**

With tasks, the review state lives in the DB. review.md becomes a human-readable summary, not the source of truth for the code phase.

**Solution**: review.md contains:
- Project assessment (2-3 sentences)
- Actions taken (approved N, deferred M, wontfix K, merged L)
- Cross-role observations (conflicts, patterns, concerns)
- Report health notes (which roles had issues)

The code phase reads from `tasks WHERE state='approved'`, not from review.md.

### New review prompt

```markdown
# Role: ATeam Supervisor

You are the ATeam supervisor. You triage tasks reported by specialized
roles that have analyzed a codebase. Tasks are tracked in a database
via the `ateam task` CLI. Your job is to decide which tasks should be
coded, deferred, or closed.

## Principles

- **No feature work**: Only quality improvements. Ignore feature plans.
- **Impact over completeness**: Not every finding needs action.
- **Small wins matter**: HIGH severity + SMALL effort = approve first.
- **Context matters**: CRITICAL for production, LOW for a prototype.
- **Sequencing**: Fix tests before refactoring. Fix builds before tests.
- **Ambiguous tasks**: If a task requires product decisions or has unclear
  tradeoffs, defer it with a comment explaining what needs human input.
  Do not approve tasks you're unsure about.

## Your tools

```
ateam task list [--state STATE] [--role ROLE]    # query tasks
ateam task update ID --state STATE [--priority P0|P1|P2] [--comment "..."]
ateam task merge TARGET_ID SOURCE_ID             # merge duplicates
ateam task comment ID "text"                     # add a note
```

States you can set:
- `approved` — approve for coding, must set priority (P0/P1/P2)
- `deferred` — valid finding, not now. Must add a comment with the reason
- `wontfix` — will not fix. Must add a comment with the reason

## Workflow

### 1. Read the report health and task lists below

The sections below are auto-generated by ateam. They show:
- Which report roles ran and their status
- New open tasks (untriaged findings from the latest report)
- Failed tasks (coding was attempted and failed — needs re-assessment)
- Deferred tasks (from previous reviews — periodic re-evaluation)
- Recently completed/approved tasks (context only)

### 2. Merge duplicates

Different roles may report the same issue from different angles.
If two tasks describe the same underlying problem:
```
ateam task merge TARGET_ID SOURCE_ID
```
This keeps the target, closes the source with a cross-reference.

### 3. Triage each open task

For each open task, decide and execute:
```
ateam task update ID --state approved --priority P0 --comment "Critical security fix"
ateam task update ID --state deferred --comment "Needs upstream API change first"
ateam task update ID --state wontfix --comment "Acceptable risk for internal tool"
```

### 4. Re-assess failed tasks

Tasks that failed during coding have comments explaining what went wrong.
Decide: approve for retry (if conditions changed), defer, or wontfix.

### 5. Review deferred tasks

Tasks deferred for 3+ cycles should be resolved — approve or wontfix.
Don't keep deferring indefinitely.

### 6. Add cross-role observations

If you notice patterns across tasks (e.g., "all 3 security findings stem
from the same missing input validation layer"), add a comment to the
most relevant task explaining the connection.

### 7. Produce the review summary

Your final message must be a brief review summary (saved as review.md):

```
### Project Assessment
2-3 sentences on overall state.

### Actions Taken
- Approved: N tasks (list IDs and short subjects)
- Deferred: M tasks
- Won't fix: K tasks
- Merged: L duplicates

### Report Coverage
Note any roles that failed/timed out and the impact on coverage.

### Observations
Cross-role patterns, concerns, suggestions for the project.
```

Do not repeat task descriptions in the review summary — they live in the
task database. The summary is for humans scanning what happened.

## Critical Output Rule

Your FINAL assistant message must be the review summary.
It will be saved as review.md.
```

### What ateam injects into the prompt

Ateam assembles the review prompt with these sections appended after the instructions above:

**Section A: Report health** (from `agent_runs` for latest report run_group)
```
## Report Health

Run group: report-2026-04-11_08-48-05

| Role            | Status   | Duration | Tasks created |
|-----------------|----------|----------|---------------|
| security        | ok       | 4m41s    | 6             |
| testing_basic   | ok       | 2m57s    | 3             |
| docs_internal   | timeout  | 20m      | 1 (partial)   |
| refactor_small  | error    | 0s       | 0             |

Note: docs_internal timed out (findings may be incomplete).
refactor_small failed to start (0 findings from this role).
```

**Section B: Open tasks** (from `ateam task list --state open --format full`)
```
## Open Tasks (14 tasks)

#23 [open] security | HIGH | SMALL
Subject: Secret env vars forwarded as -e KEY=VALUE docker arguments
Location: internal/container/docker.go:162-165

Both container backends forward secret-bearing env vars as -e KEY=VALUE
arguments to docker run or docker exec...

---
...
```

**Section C: Failed tasks** (from `ateam task list --state failed --format full`)
```
## Failed Tasks (2 tasks — need re-assessment)

#8 [failed] refactor_small | MEDIUM | SMALL
Subject: Extract duplicate validation logic
Location: cmd/report.go:112, cmd/code.go:87

[2026-04-10 22:32] refactor_small:code — Build error after extracting
  validateOpts(): cmd/all.go:85 undefined validateOpts. Call sites in
  all.go were missed.

---
...
```

**Section D: Deferred tasks** (from `ateam task list --state deferred --format full`)
```
## Deferred Tasks (3 tasks — re-evaluate)

#5 [deferred] testing_basic | MEDIUM | LARGE  (deferred 3 cycles ago)
Subject: Add integration tests for container module
[2026-04-07 11:37] supervisor:review — Deferred: container module is
  being refactored, tests would be thrown away.

---
...
```

**Section E: Recently completed** (from `ateam task list --state done --format oneline`, last 10)
```
## Recently Completed (context only — do not re-triage)

#7  [done] MEDIUM SMALL security: extra_volumes path traversal (commit 9a7b6fc)
#12 [done] HIGH   SMALL refactor_small: Remove dead code in runner.go (commit 111fac4)
#15 [done] MEDIUM MEDIUM testing_basic: Add pool_test.go edge cases (commit 6be15f9)
```

**Section F: Already triaged this cycle** (if any approved/deferred tasks were set during the same run_group — indicates interrupted or incremental review)
```
## Already Triaged This Cycle (context only)

#23 [approved] P0 security: Secret env vars forwarded as -e KEY=VALUE
#25 [approved] P1 testing_basic: Fix flaky timeout test
```

This section only appears if a previous review run in the same logical cycle modified tasks. It prevents re-triage of already-decided tasks.

### How ateam assembles this

```go
func AssembleReviewPrompt(...) (string, error) {
    // 1. Load the review instructions (3-level fallback as today)
    // 2. Generate report health from agent_runs
    // 3. Query tasks by state: open, failed, deferred, done (last 10)
    // 4. Check for interrupted previous review (approved tasks from same cycle)
    // 5. Format each section using task list formatting
    // 6. Concatenate: instructions + health + sections + extra_prompt
}
```

The current `DiscoverReports()` + full report embedding is replaced entirely. No report files are read by the review prompt assembly — everything comes from the task table and agent_runs.

### Failure mode analysis

| Failure | Impact | Recovery |
|---------|--------|----------|
| **Report role times out** | Partial tasks created (via `task add` calls before timeout). Report health shows "timeout". | Supervisor defers tasks from that role or approves what exists. Next cycle runs the role again. |
| **Report role errors immediately** | 0 tasks from that role. Report health shows "error". | Supervisor notes the gap. `ateam report --rerun-failed` retries. |
| **Review agent crashes mid-triage** | Some tasks are approved/deferred, others still open. | Next review run shows "Already Triaged" section. Supervisor continues with remaining open tasks. |
| **Review agent makes bad decisions** | Tasks incorrectly approved, deferred, or wontfix'd. | Human runs `ateam task update ID --state open --comment "Reverting supervisor decision"`. Comments preserve the history of why. |
| **Coding agent fails** | Task state = `failed`, comment has error details. | Next review shows failed tasks for re-assessment. Supervisor can retry, defer, or wontfix. |
| **Coding agent partially succeeds** | Dirty git tree, no commit. `ateam code` reverts and marks failed. | Same as above. The revert ensures clean state for next task. |
| **Multiple report runs without review** | Many duplicate open tasks from different runs. | Supervisor merges duplicates. `task merge` handles this. Report health shows multiple run groups if relevant. |
| **User edits tasks between cycles** | Tasks in unexpected states (e.g., user approved something the supervisor would have deferred). | Supervisor sees the current state and respects it. Comments show who changed what. |

### What changes vs current review_prompt.md

| Aspect | Current | New |
|--------|---------|-----|
| **Input** | Full prose reports from 8+ roles (~8000 words) | Structured task list (~1300 tokens for 20 tasks) + report health table |
| **Output** | review.md with Priority Actions, Deferred, Conflicts, Notes (source of truth for code phase) | review.md as human summary only. Task state changes are the real output. |
| **How code phase reads decisions** | Parses review.md prose for Priority Actions | Queries `tasks WHERE state='approved' ORDER BY priority` |
| **Cross-cycle context** | Full previous review.md embedded (or ignored) | Failed/deferred tasks carry context via comments. Report health shows coverage gaps. |
| **Failure recovery** | If review crashes, review.md may be partial or absent. Code phase has nothing to work with. | If review crashes, already-triaged tasks retain their state. Next review continues with remaining open tasks. |
| **Human override** | Edit review.md before `ateam code` | `ateam task update ID --state X` before `ateam code`. Or `ateam task edit ID` for full markdown editing. |

---

## Failed Task Retry: Supervisor Diagnosis Prompt

### The two-phase code cycle

The mechanical Go loop handles the happy path: for each approved task, run a coding agent, record success/failure. This is cheap and deterministic.

But failures are where judgment matters. A coding agent fails with "Build error: undefined function writeTempEnvFile" — is this a missing import? A wrong file? A flawed approach? The mechanical loop can't diagnose this. A supervisor can.

**Phase 1 (mechanical)**: Go loop runs all approved tasks sequentially. Successes committed, failures reverted and commented. No LLM orchestration cost.

**Phase 2 (supervisor diagnosis)**: If any tasks failed, spawn a supervisor agent with the failed tasks, their error context, and the ability to read code and attempt fixes. This is the only LLM-cost step in the code phase.

If all tasks succeeded, phase 2 is skipped entirely. Cost: $0.

### When phase 2 runs

```go
failed := db.TasksByState("failed", "WHERE code_run_group = ?", currentRunGroup)
if len(failed) == 0 {
    return // all tasks succeeded, no supervisor needed
}
// Spawn supervisor with failed tasks
prompt := assembleFailureDignosisPrompt(failed, currentRunGroup)
result := runner.Run(ctx, prompt, opts)
```

### Failure diagnosis prompt

```markdown
# Role: Coding Supervisor — Failure Diagnosis

You are diagnosing and potentially fixing coding tasks that failed
during the automated coding cycle. The mechanical loop already
attempted each task with a coding agent; those agents failed.

Your job:
1. Understand why each task failed (read the error, check the code)
2. If the fix is clear, implement it yourself and commit
3. If the fix requires a different approach, update the task description
   and re-approve it for the next cycle
4. If the task is not fixable now, defer or wontfix with an explanation

## Your tools

```
ateam task show ID                          # full task detail + comments
ateam task update ID --state done --commit HASH
ateam task update ID --state deferred --comment "reason"
ateam task update ID --state wontfix --comment "reason"
ateam task comment ID "diagnosis details"
```

You also have full access to the codebase: read files, run commands,
run tests, make changes, commit.

## Workflow

For each failed task below:

1. **Read the failure comment** to understand what the coding agent tried
2. **Check the current code** — has it changed since the attempt?
   (Other tasks may have succeeded and changed the codebase)
3. **Diagnose** — is the failure due to:
   - A simple mistake the agent made? → Fix it, commit, mark done
   - A changed dependency from another task's commit? → Fix it, commit
   - A fundamentally wrong approach? → Update task description with
     the right approach, set state to `approved` for retry
   - Missing infrastructure (tool, dependency, config)? → Defer with
     explanation of what's needed
   - The task itself is ill-defined? → Wontfix with explanation
4. **Always add a comment** explaining your diagnosis, even if you fix it

## Context

Git log since start of coding cycle:
{recent git log showing successful task commits}

## Failed Tasks

{for each failed task: full task detail + all comments, especially the
failure comment from the mechanical loop}
```

### What the supervisor sees per failed task

```
### Failed Task #8: Extract duplicate validation logic

State: failed | Severity: MEDIUM | Effort: SMALL
Role: refactor_small | Location: cmd/report.go:112, cmd/code.go:87

Description:
  Both report.go and code.go have nearly identical option validation
  blocks. Extract to a shared validateOpts() function.

Comments:
  [2026-04-11 08:48] refactor_small:report — Created
  [2026-04-11 11:37] supervisor:review — approved, P1
  [2026-04-11 22:07] ateam code — State: approved → in_progress
  [2026-04-11 22:13] ateam code — State: in_progress → failed
    Coding agent error: Created validateOpts() in cmd/shared.go but
    missed call site in cmd/all.go:85. Build failed:
    ./all.go:85:14: undefined: validateOpts
    Changes reverted.

Recent commits (context):
  abc1234 [ateam: security] Fix extra_volumes path traversal
  def5678 [ateam: testing_basic] Add pool edge case tests
```

The supervisor can see that other tasks modified files in `cmd/`, potentially affecting the approach. It can check if `cmd/all.go` also has the duplicated validation block that needs the new function.

### Integration with ateam code

```go
func runCode(opts CodeOptions) error {
    // Phase 1: Mechanical loop
    approved := db.TasksByState("approved", ...)
    for _, task := range approved {
        runCodingAgent(task) // success → done, failure → failed + comment
    }

    // Phase 2: Supervisor diagnosis (only if failures)
    failed := db.TasksByState("failed", "WHERE code_run_group = ?", runGroup)
    if len(failed) > 0 {
        fmt.Printf("Phase 2: %d failed tasks, running supervisor diagnosis...\n", len(failed))
        prompt := assembleFailureDiagnosisPrompt(failed, runGroup)
        supervisorResult := runner.Run(ctx, prompt, supervisorOpts)
        // Supervisor updates task states directly via CLI
    }

    printCodeSummary(runGroup)
    return nil
}
```

The supervisor profile can differ from the coding agent profile (e.g., supervisor runs on host, coding agents run in Docker). The supervisor needs code access to diagnose but also needs `ateam task` CLI access.

---

## Evaluation: Pipeline phases as tasks

### The idea

Model report/review/code runs as tasks themselves:
- `ateam report` creates a task "Run security report", state=in_progress
- When the report agent finishes, state=done or state=failed
- Finding tasks are children of the report task

This gives a unified view: `ateam task list --all-types` shows both pipeline phases and findings. Failures at any level are visible in the same system.

### Assessment: Not recommended

**Against:**

1. **Two fundamentally different things.** A "run security report" task has no severity, effort, location, or description. A finding has all of these. Putting both in the same table requires either nullable fields everywhere or a `type` column that bifurcates every query.

2. **`agent_runs` already tracks this.** Pipeline run status, duration, error state, cost — all already in `agent_runs`. Adding pipeline tasks to the `tasks` table duplicates this data.

3. **Clutters the task list.** Users running `ateam task list` want to see findings to triage, not "Run security report: done." Every query, display, and prompt would need to filter by type.

4. **The problem it solves is already solved.** "Did the report phase complete?" → check `agent_runs`. "Did the review crash?" → check `agent_runs`. The report health table in the review prompt already surfaces this from `agent_runs`.

**What it would add:**
- A single place to see "everything that happened" — but `ateam ps` already does this for runs, and `ateam task list` does it for findings. Combining them isn't clearly better.

**Verdict:** Keep tasks for findings/work items only. Pipeline tracking stays in `agent_runs`. The report health injection in the review prompt bridges the gap.

---

## Evaluation: Task relationships

### The need

Real relationships exist between tasks:
- "Task #23 was reported by the security report that ran as part of run-group report-2026-04-11" → **provenance**
- "Task #30 is a retry of task #8 with a different approach" → **retry lineage**
- "Task #8 was merged into #3" → **dedup** (already handled by merge)
- "Task #12 should be done before #15 (fix tests before refactoring)" → **sequencing**

### Options evaluated

**Option A: `parent_id` column**

```sql
ALTER TABLE tasks ADD COLUMN parent_id INTEGER REFERENCES tasks(id);
```

Simple one-level hierarchy. A finding's parent could be... what? There's no "report task" to point to (we decided against pipeline-as-tasks). So parent_id would link retries or follow-ups.

Verdict: **Too narrow.** Only models one relationship type, and the most common one (provenance: which report created this) is already tracked via `report_run_group`.

**Option B: `task_relations` table (n:m)**

```sql
CREATE TABLE task_relations (
    source_id     INTEGER NOT NULL REFERENCES tasks(id),
    target_id     INTEGER NOT NULL REFERENCES tasks(id),
    relation_type TEXT NOT NULL,  -- 'retry_of', 'merged_into', 'depends_on', 'related'
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    PRIMARY KEY (source_id, target_id, relation_type)
);
```

Flexible, models all relationship types. But:
- Adds a table and joins
- Every relationship type needs UI/prompt support
- `depends_on` implies execution ordering — the Go loop would need topological sort
- Agents need to discover and create relationships, adding prompt complexity and tokens

Verdict: **Over-designed for now.** The only relationships that matter today are provenance (already `report_run_group`) and retry lineage (rare).

**Option C: Comments + convention (no schema change)**

Relationships are recorded as comments with a convention:
```
[2026-04-12] supervisor:code — Retry of #8: modified approach — use temp file instead of env var
[2026-04-12] supervisor:review — Merged from #30: same issue reported by testing_basic
[2026-04-12] supervisor:review — Related to #15: both stem from missing input validation
```

The `merge` command already creates cross-reference comments. The failure diagnosis supervisor naturally adds "Retry of #8" comments when creating follow-up tasks.

Verdict: **Good enough for now.** Comments are human-readable, agent-readable (the review prompt shows them), and require no schema changes. If we need structured queries on relationships later ("show me all retries of #8"), we can add the relations table then.

### Recommendation

**Start with Option C (comments + convention).** Add one small schema addition for the most common structured query:

```sql
ALTER TABLE tasks ADD COLUMN retry_of INTEGER REFERENCES tasks(id);
```

When the failure diagnosis supervisor creates a new task to retry differently, set `retry_of` pointing to the original. This lets `ateam task show 8` display "Retried as #31" without parsing comments. Everything else (related, depends_on) stays in comments.

The `task_relations` table (Option B) can be added later if structured relationship queries become important. The schema is designed so it can be added without breaking anything.

### Updated schema

```sql
CREATE TABLE tasks (
    id               INTEGER PRIMARY KEY,
    subject          TEXT NOT NULL,
    description      TEXT,
    source_role      TEXT NOT NULL,
    source_action    TEXT NOT NULL DEFAULT 'report',
    severity         TEXT,
    effort           TEXT,
    location         TEXT,
    state            TEXT NOT NULL DEFAULT 'open',
    priority         TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    report_run_group TEXT,          -- which report run created this
    code_run_group   TEXT,          -- which code run attempted this
    commit_hash      TEXT,
    session_id       TEXT,
    retry_of         INTEGER REFERENCES tasks(id)  -- links retries to original
);
```

---

## What this is NOT

- No assignees, no due dates, no sprints
- No full dependency graph (retry_of is the only structured link; other relationships live in comments)
- No custom fields or labels
- No webhooks or notifications
- No rich-text formatting in comments
- No access control (anyone can change anything)
- No pipeline-as-tasks (pipeline tracking stays in agent_runs)

It's a findings list with an audit trail. Like a shared notepad with structure.
