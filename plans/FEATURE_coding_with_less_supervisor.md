# Feature: Coding with Less Supervisor

## Problem

The current `ateam code` flow uses a supervisor LLM to orchestrate coding tasks. The supervisor spends most of its tokens on mechanical work: creating directories, running git commands, invoking `ateam prompt` and `ateam run`, writing templated execution reports. The actual judgment — error diagnosis, retry decisions, report annotation — is a small fraction of the token budget.

But the deeper problem is upstream. The supervisor's review step synthesizes unstructured role reports into a prioritized action plan, and then the code management supervisor re-parses that plan from markdown. This markdown-to-markdown pipeline is fragile, expensive, and loses information at each stage.

## Current Flow

```
role reports (markdown)
  → supervisor review (LLM reads reports, writes review.md as markdown)
    → code management supervisor (LLM reads review.md, orchestrates tasks)
      → ateam run per task (LLM executes code changes)
```

Every arrow is a full LLM invocation parsing semi-structured markdown.

## Proposed Flow

```
role reports + structured findings (ateam finding create)
  → findings table in state.sqlite
    → supervisor review (LLM reads findings, adjusts priority, dedup, writes review)
      → ateam code --managed (Go code orchestrates, LLM only for final review)
        → ateam run per task (LLM executes code changes)
```

Key changes:
1. Roles create structured findings via CLI during their report run
2. Findings are stored in SQLite, not just in markdown prose
3. The supervisor review works from structured data, focusing on judgment (priority, dedup, conflicts)
4. Code orchestration is mechanical Go code; the supervisor only reviews outcomes

## Design

### Part 1: Structured Findings

#### `ateam finding` CLI

```
ateam finding create --role ROLE --priority LOW|MEDIUM|HIGH|CRITICAL \
  --effort SMALL|MEDIUM|LARGE --subject "Short description" \
  --location "file.go:42" --body @finding.md

ateam finding list [--role ROLE] [--priority CRITICAL] [--status open]
ateam finding show ID
ateam finding update ID --status addressed|deferred|wontfix [--note "reason"]
```

#### Schema: `findings` table

```sql
CREATE TABLE findings (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id    TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL,
  subject       TEXT NOT NULL,
  body          TEXT NOT NULL DEFAULT '',
  location      TEXT NOT NULL DEFAULT '',
  priority      TEXT NOT NULL,            -- CRITICAL, HIGH, MEDIUM, LOW
  effort        TEXT NOT NULL,            -- SMALL, MEDIUM, LARGE
  status        TEXT NOT NULL DEFAULT 'open', -- open, approved, deferred, addressed, wontfix
  fingerprint   TEXT NOT NULL DEFAULT '', -- for dedup (hash of role+subject+location)
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  addressed_by  INTEGER,                  -- FK to agent_execs.id (which run fixed it)
  review_note   TEXT NOT NULL DEFAULT ''  -- supervisor annotation
);
```

#### Role report prompt change

Add to `report_base_prompt.md`:

```
## Registering Findings

For each finding in your report, register it using the ateam CLI so it can be
tracked across runs:

    ateam finding create --role YOUR_ROLE \
      --priority CRITICAL|HIGH|MEDIUM|LOW \
      --effort SMALL|MEDIUM|LARGE \
      --subject "Short description" \
      --location "file.go:42" \
      --body "Detailed description and recommendation"

Before creating a finding, check if a similar one already exists:

    ateam finding list --role YOUR_ROLE

If your finding matches an existing open finding, skip it. If it's the same
issue but the details changed, update it instead of creating a duplicate.

Continue producing the markdown report as before — the findings database is
an additional structured output, not a replacement for the report.
```

#### Deduplication

The `fingerprint` column is a hash of `role + normalized_subject + primary_location_file`. This catches exact duplicates from re-runs of the same role. Cross-role dedup (security and refactor_small both flagging the same function) is left to the supervisor review — that's a judgment call, not a mechanical one.

### Part 2: Supervisor Review with Structured Input

The review prompt changes to work from findings data instead of (or in addition to) markdown reports:

```
ateam review --from-findings   # reads from findings table
ateam review                   # current behavior (reads markdown reports)
```

With `--from-findings`, the review prompt includes a structured findings dump:

```
# Findings

## Open Findings (by priority)

### [F-001] SQL injection in user input handler
- **Role**: security
- **Priority**: CRITICAL
- **Effort**: SMALL
- **Location**: internal/api/handler.go:142
- **Status**: open
- **Body**: Raw user input passed to query builder...

### [F-002] Missing index on frequently queried column
- **Role**: database_schema
...
```

The supervisor's job narrows to:
- Adjust priorities based on project context
- Identify cross-role duplicates and merge them
- Resolve conflicts between roles
- Defer findings that need product decisions
- Write the human-readable review summary

The supervisor updates findings via CLI:
```
ateam finding update 1 --status approved --note "P0, do first"
ateam finding update 2 --status deferred --note "needs product input"
ateam finding update 3 --status wontfix --note "duplicate of F-001"
```

### Part 3: Managed Code Execution

`ateam code --managed` replaces the LLM orchestration with Go code:

#### Go orchestration loop

```
1. Setup
   - Create execution directory
   - git fetch, check clean working tree
   - Check build + tests pass

2. Query approved findings
   - SELECT * FROM findings WHERE status = 'approved' ORDER BY priority, effort

3. For each finding:
   a. Write task description from finding body + context
   b. ateam prompt --role ROLE --action code --extra-prompt @task.md > prompt.md
   c. Pre-check: build + tests
   d. ateam run @prompt.md --role ROLE --task-group GROUP --profile PROFILE
   e. Post-check: build + tests
   f. If success:
      - Verify git committed
      - UPDATE findings SET status='addressed', addressed_by=CALL_ID
      - Append success to execution_report.md
   g. If failure:
      - git checkout . (revert changes)
      - Append failure to execution_report.md
      - Continue to next finding

4. Finalize
   - Write execution_report.md summary
   - Invoke supervisor LLM with execution report for final review
```

#### What the supervisor still does (Phase 4 only)

After Go code finishes all tasks, the supervisor gets a focused prompt:

```
Here is the execution report from an automated code session.
Review the outcomes and:
1. For failures, briefly assess whether they should be retried with different context
2. Update review.md with outcomes
3. Update source role reports with what was addressed
```

This is a single LLM call with a small, focused prompt instead of a multi-hour orchestration session.

#### The `--managed` flag

```
ateam code              # current behavior (full LLM supervisor)
ateam code --managed    # Go orchestration + LLM review of outcomes
```

The default stays as-is. `--managed` is opt-in.

### Part 4: What stays in the LLM supervisor (and why)

The full LLM supervisor (`ateam code` without `--managed`) keeps its current behavior. This is important because:

1. **Customization without recompilation** — users can edit `code_management_prompt.md` to add/remove steps, try different strategies (parallel execution, worktree management), or change error handling, all without touching Go code
2. **Adaptive recovery** — the LLM can do things Go code can't: answer a coding agent's clarifying question, try a different role when one fails, creatively work around permission issues
3. **Experimentation** — the prompt is the right place to prototype new orchestration ideas before hardcoding them in Go

The `--managed` flag is for production runs where the workflow is settled and token cost matters.

## Tradeoffs

### Structured findings

| | Pro | Con |
|---|---|---|
| **Findings in DB** | Machine-readable, queryable, dedup-able, trackable across runs | Roles must call CLI during report — adds complexity to role prompts, roles might forget or misformat |
| **Dual output** | Markdown reports stay human-readable; DB is for automation | Two sources of truth that can drift — a finding in the report but not the DB, or vice versa |
| **Fingerprint dedup** | Catches exact re-run duplicates automatically | Cross-role dedup still needs LLM judgment; fingerprint is fragile if subject wording varies |
| **Finding lifecycle** | Clear status tracking (open → approved → addressed) | More state to manage; stale findings accumulate if not cleaned up |

### Managed code execution

| | Pro | Con |
|---|---|---|
| **Token savings** | ~60-80% reduction in supervisor tokens for the code phase | Loses adaptive error handling — Go code can't diagnose failures or adjust prompts |
| **Speed** | No LLM round-trips for mechanical steps | Minimal — the sub-runs (actual coding) dominate wall-clock time |
| **Reliability** | Deterministic orchestration, no hallucinated commands | Less resilient to unexpected situations (new error types, permission changes) |
| **Simplicity** | Two code paths (managed vs full LLM) instead of one | Maintenance burden: changes to workflow must be reflected in both paths |

### The fundamental bet

This design bets that **most coding sessions are routine**: run the approved tasks, check build/test, commit, move on. The LLM supervisor is overkill for the common case and valuable for the uncommon case (failures, conflicts, creative recovery).

`--managed` optimizes for the common case. The full supervisor remains available for the uncommon case.

## Migration Path

1. **Phase A**: Add `findings` table and `ateam finding` CLI. No prompt changes yet — findings are created manually or by scripts.
2. **Phase B**: Update role report prompts to register findings during report runs. Both markdown and DB exist in parallel.
3. **Phase C**: Update review prompt to optionally work from findings (`--from-findings`). Validate that structured input produces equivalent reviews.
4. **Phase D**: Implement `ateam code --managed`. Start with the happy path only (no error recovery beyond revert-and-continue).
5. **Phase E**: Add supervisor review of outcomes as the final step of `--managed`.

Each phase is independently useful and shippable.

## Files to Create/Modify

### New
- `cmd/finding.go` — cobra command for `ateam finding`
- `internal/calldb/findings.go` — findings table schema, queries, migrations

### Modified
- `internal/calldb/calldb.go` — add findings table to schema, migration
- `defaults/report_base_prompt.md` — add "Registering Findings" section
- `defaults/supervisor/review_prompt.md` — add structured findings input mode
- `defaults/supervisor/code_management_prompt.md` — no change (stays as-is for non-managed mode)
- `cmd/code.go` — add `--managed` flag, implement Go orchestration loop
- `cmd/review.go` — add `--from-findings` flag
