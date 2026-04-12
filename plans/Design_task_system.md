# Design: Task System

## Input Format Decision

The agent currently outputs findings as markdown with structured fields (Title, Location, Severity, Effort, Description, Recommendation). The task CLI input should match this structure with minimal ceremony.

**Evaluated formats:**

| Format | Tokens per finding | Error risk | Multi-line description |
|--------|-------------------|------------|----------------------|
| Flags + stdin | ~55 | Medium (flag quoting) | Awkward (stdin is body only) |
| JSON pipe | ~60 | High (escaping quotes, \n in strings) | Bad |
| Frontmatter + body heredoc | ~45 | Low (no escaping, familiar pattern) | Natural |

**Winner: frontmatter + body via heredoc.** LLMs produce this pattern reliably (it's markdown frontmatter), multi-line descriptions work naturally, no escaping needed, and it's the most compact.

Common fields (reporter, task-group) are injected as environment variables by the runner — the agent never passes them. This saves ~10 tokens per `ateam task add` call.

```bash
# The agent just does this per finding. Reporter and task-group come from env.
ateam task add <<'EOF'
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

The runner sets these before spawning the agent:
```
ATEAM_REPORTER=security:report
ATEAM_TASK_GROUP=report-2026-04-11_08-48-05
```

For explicit override (testing, manual use): `ateam task add --reporter security:report --task-group TG`.

Also accept a file: `ateam task add --file finding.md` (same frontmatter format).

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
    task_group    TEXT,          -- originating report/review run
    code_group    TEXT,          -- code run that attempted this
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

Lives in the existing `.ateam/state.sqlite` alongside the `calls` table.

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
and task group are set automatically.

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

## What this is NOT

- No assignees, no due dates, no sprints
- No parent-child relationships or dependencies
- No custom fields or labels
- No webhooks or notifications
- No rich-text formatting in comments
- No access control (anyone can change anything)

It's a findings list with an audit trail. Like a shared notepad with structure.
