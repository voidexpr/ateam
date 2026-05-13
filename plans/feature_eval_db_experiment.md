# Plan — Database-Driven Eval (Phase 1: Data Model Only)

## Context

The current `ateam eval` always runs each side from scratch: every comparison
forces 2 fresh role runs (or 2N for multi-role) plus a judge. Runs are
expensive and slow, so every "what if I tried…" question costs another full
execution.

But `agent_execs` already records everything that matters per run: profile,
agent, model, role, action, started/ended, duration, tokens, cost, turns,
peak context, exit code, **git_start_hash / git_end_hash**, and pointers to
`stream_file` / `output_file`. The prompts and full logs live under
`.ateam/logs/<exec_id>` and `.ateam/runtime/<exec_id>`.

The new direction: turn **any past or future agent_exec** into a candidate in
an N-way comparison. A judge then reads each candidate's artifacts (output
files, git commits, hand-attached refs) and produces structured scores plus
a verdict. Storage stays minimal — we lean on `agent_execs` instead of
duplicating its columns.

This plan covers **only the data model**. Worktree reuse, parallel
orchestration, and setup/cleanup scripts are deferred to follow-up phases.

## Schema (new tables, all in `internal/calldb/calldb.go`)

### `experiments`
Top-level grouping. One per "question being asked".

```
id              INTEGER PRIMARY KEY AUTOINCREMENT
project_id      TEXT NOT NULL              -- same convention as agent_execs
name            TEXT NOT NULL              -- short user-supplied label
description     TEXT NOT NULL DEFAULT ''   -- free-form goal/hypothesis
status          TEXT NOT NULL DEFAULT 'open'  -- open|done|abandoned
created_at      DATETIME NOT NULL
updated_at      DATETIME NOT NULL
```
Indexes: `idx_experiments_project (project_id, created_at DESC)`,
`idx_experiments_status (status)`.

### `candidates`
N per experiment. A candidate is a *named bundle of agent_execs*. No
config columns — everything (prompt, role, model, agent, profile) is
recoverable from the linked `agent_execs` rows and `.ateam/logs/<exec_id>`.

```
id              INTEGER PRIMARY KEY AUTOINCREMENT
experiment_id   INTEGER NOT NULL REFERENCES experiments(id) ON DELETE CASCADE
name            TEXT NOT NULL              -- "v1", "claude-sonnet", "prompt-A"
notes           TEXT NOT NULL DEFAULT ''   -- optional human description
created_at      DATETIME NOT NULL
UNIQUE(experiment_id, name)
```
Index: `idx_candidates_experiment (experiment_id)`.

### `candidate_agent_execs`
Many-to-many. Lets the same exec belong to multiple candidates (e.g. shared
baseline) and lets a candidate aggregate multiple execs (e.g. multi-role).

```
candidate_id    INTEGER NOT NULL REFERENCES candidates(id) ON DELETE CASCADE
exec_id         INTEGER NOT NULL REFERENCES agent_execs(id)
role_in_bundle  TEXT NOT NULL DEFAULT ''   -- optional: 'primary', 'review', etc.
PRIMARY KEY (candidate_id, exec_id)
```
Index: `idx_cae_exec (exec_id)`.

### `candidate_artifacts`
Explicit curation of what the judge sees. Default flow is "auto-derive from
linked execs" but rows here override: pin a specific commit, attach an
external file, exclude a noisy output.

```
id              INTEGER PRIMARY KEY AUTOINCREMENT
candidate_id    INTEGER NOT NULL REFERENCES candidates(id) ON DELETE CASCADE
exec_id         INTEGER NULL REFERENCES agent_execs(id)  -- NULL = manually attached
kind            TEXT NOT NULL              -- 'file' | 'commit' | 'diff' | 'log'
ref             TEXT NOT NULL              -- file path | git hash | exec stream path
included        INTEGER NOT NULL DEFAULT 1 -- 0 = explicit exclude
created_at      DATETIME NOT NULL
```
Index: `idx_artifacts_candidate (candidate_id)`.

### `judgments`
One row per judge invocation. The judge run itself is an `agent_exec`
(reuse existing tracking for cost/tokens). Multiple judgments per experiment
are allowed — useful for re-judging with a different model.

```
id              INTEGER PRIMARY KEY AUTOINCREMENT
experiment_id   INTEGER NOT NULL REFERENCES experiments(id) ON DELETE CASCADE
judge_exec_id   INTEGER NOT NULL REFERENCES agent_execs(id)
verdict         TEXT NOT NULL DEFAULT ''   -- the LLM's prose conclusion
winner_candidate_id INTEGER NULL REFERENCES candidates(id)
created_at      DATETIME NOT NULL
```
Index: `idx_judgments_experiment (experiment_id, created_at DESC)`.

### `judgment_candidate_scores`
Per-candidate scores for one judgment. Fixed dimension columns (matching
the judge prompt in EVAL.md). Add a column via `ALTER TABLE ADD COLUMN`
when a new dimension is introduced — cheap in SQLite, self-documenting.

```
judgment_id     INTEGER NOT NULL REFERENCES judgments(id) ON DELETE CASCADE
candidate_id    INTEGER NOT NULL REFERENCES candidates(id) ON DELETE CASCADE
coverage        REAL NULL                  -- 0.00..1.00
accuracy        REAL NULL
actionability   REAL NULL
conciseness     REAL NULL
overall         REAL NULL                  -- judge's aggregate
PRIMARY KEY (judgment_id, candidate_id)
```

Two tables (not one) because a judgment carries both per-judgment data
(verdict, winner) and per-(judgment, candidate) data (scores). One-table
shapes either duplicate the verdict across N rows or require a JSON blob.
Keeping the split also preserves multi-judgment history (re-judge with a
different model, compare side-by-side later).

### `experiment_results` (VIEW)
Flat read shape for "show me everything about this experiment".
`LEFT JOIN`s scores so unscored / not-yet-judged candidates still appear
with NULL columns.

```sql
CREATE VIEW experiment_results AS
SELECT
    e.id              AS experiment_id,
    e.name            AS experiment_name,
    e.status          AS experiment_status,
    c.id              AS candidate_id,
    c.name            AS candidate_name,
    j.id              AS judgment_id,
    j.judge_exec_id   AS judge_exec_id,
    j.verdict         AS verdict,
    j.winner_candidate_id = c.id AS is_winner,
    j.created_at      AS judged_at,
    s.coverage,
    s.accuracy,
    s.actionability,
    s.conciseness,
    s.overall
FROM experiments e
JOIN candidates c ON c.experiment_id = e.id
LEFT JOIN judgments j ON j.experiment_id = e.id
LEFT JOIN judgment_candidate_scores s
    ON s.judgment_id = j.id AND s.candidate_id = c.id;
```

One row per (experiment, candidate, judgment); candidates with no
judgment yet show up with NULL judgment_id and NULL scores.

## MLflow concepts: adopted, adapted, rejected

**Adapted:**
- Experiment / Run grouping → our experiment / candidate ↔ `agent_execs`.
- MLflow's "metrics" with step/timestamp history → our
  `judgment_candidate_scores` (one row per judgment×candidate, fixed
  dimension columns). Re-judging produces new `judgments` +
  `judgment_candidate_scores` rows — full history preserved. Reads happen
  through the `experiment_results` view, so callers don't deal with the
  multi-table join.
- MLflow's `parent_run_id` for nested runs → our many-to-many
  `candidate_agent_execs` join. Cleaner for the case where one exec belongs
  to multiple candidates.

**Rejected for our scale / use case:**
- **Generic tag tables** (`experiment_tags`, `candidate_tags`). MLflow tags
  pay off with thousands of sweep runs. We're talking 10s–100s of
  hand-curated experiments with 2–5 candidates each — `description TEXT` +
  `LIKE` is enough; candidate dimensions worth filtering on (model, agent,
  role) already live on linked `agent_execs`. Add later only if a concrete
  filter need shows up.
- **Soft delete** via `deleted_at`. The existing `status='abandoned'`
  covers "hide from default list". Hard-deleting an experiment doesn't
  lose anything expensive (the `agent_execs` it references live
  independently). Adding a parallel deletion concept would only cause
  drift between `status` and `deleted_at`.
- Pluggable artifact storage URIs (S3/GCS) — local file paths and git
  hashes are sufficient.
- Model registry / dataset entity — not relevant.
- ms-epoch timestamps — `DATETIME` matches existing `agent_execs`.
- Auto-naming "abundant-ape-123" style — `name` stays required for now;
  reconsider if we add an auto-attach hook.

### Same scenario in real MLflow (for comparison)

What the "2 roles + review vs. 1 role + review" example below would look
like using the actual `mlflow` Python SDK and CLI, so the gap between
MLflow as-is and our adapted model is concrete.

```bash
mlflow experiments create -n code-consolidation
# → returns experiment_id, e.g. 7
```

```python
import mlflow, subprocess, json

mlflow.set_experiment("code-consolidation")

def head(workdir):
    return subprocess.check_output(
        ["git", "-C", workdir, "rev-parse", "HEAD"]
    ).decode().strip()

def run_role(role, action, workdir):
    """Wrap one ateam agent invocation as a nested MLflow run."""
    with mlflow.start_run(run_name=f"{action}-{role}", nested=True) as r:
        mlflow.log_params({"role": role, "action": action,
                           "agent": "claude", "model": "sonnet",
                           "work_dir": workdir})
        mlflow.set_tag("git_start_hash", head(workdir))
        # Invoke ateam, capture metrics from its JSON output:
        out = subprocess.check_output(
            ["ateam", action, "--roles", role, "--work-dir", workdir,
             "--json-summary"]
        )
        summary = json.loads(out)
        mlflow.log_metrics({"input_tokens":  summary["input_tokens"],
                            "output_tokens": summary["output_tokens"],
                            "cost_usd":      summary["cost_usd"],
                            "duration_s":    summary["duration_s"]})
        mlflow.set_tag("git_end_hash", head(workdir))
        mlflow.log_artifact(summary["output_file"])
        return r.info.run_id

# Candidate A — 2 roles + review in worktree A
WT_A = "/tmp/ateam-worktree/myproj/A"
with mlflow.start_run(run_name="two-roles") as cand_a:
    mlflow.set_tag("git_worktree", WT_A)
    a_small  = run_role("code.small",  "report", WT_A)
    a_module = run_role("code.module", "report", WT_A)
    a_review = run_role("",            "review", WT_A)

# Candidate B — 1 role + review in worktree B (run in parallel via threads)
WT_B = "/tmp/ateam-worktree/myproj/B"
with mlflow.start_run(run_name="one-role") as cand_b:
    mlflow.set_tag("git_worktree", WT_B)
    b_consol = run_role("code.consolidated", "report", WT_B)
    b_review = run_role("",                  "review", WT_B)

# Judge — a separate run that scores both candidates
with mlflow.start_run(run_name="judge-sonnet") as judge:
    mlflow.set_tag("references_runs",
                   f"{cand_a.info.run_id},{cand_b.info.run_id}")
    mlflow.log_text("consolidation lost two SQL injection findings…",
                    "verdict.md")
    # MLflow metrics are scoped to one run, so per-candidate scores must
    # encode the candidate in the metric key:
    mlflow.log_metrics({
        "two-roles.coverage":      0.85, "two-roles.accuracy":      0.90,
        "two-roles.actionability": 0.80, "two-roles.conciseness":   0.55,
        "two-roles.overall":       0.78,
        "one-role.coverage":       0.65, "one-role.accuracy":       0.88,
        "one-role.actionability":  0.75, "one-role.conciseness":    0.80,
        "one-role.overall":        0.77,
    })
    mlflow.set_tag("winner", "two-roles")
```

Reading results back:

```python
runs = mlflow.search_runs(experiment_names=["code-consolidation"],
                          filter_string="tags.mlflow.runName = 'judge-sonnet'")
print(runs[["metrics.two-roles.overall", "metrics.one-role.overall",
            "tags.winner"]])
```

Or via CLI:
```bash
mlflow runs list --experiment-id 7
mlflow runs describe --run-id <judge_run_id>
```

**Why we adapt rather than adopt:**
- MLflow metrics are per-run scalars; N-candidate scoring forces awkward
  key-encoding (`two-roles.coverage`) or N synthetic "score-emitter" child
  runs per judgment. Our `judgment_candidate_scores` table is the natural
  shape.
- The "verdict" is prose, not a metric — in MLflow it has to live as a
  logged artifact (text file). For us it's a column on `judgments`.
- "Winner" in MLflow is a tag string by convention; we use a typed FK
  (`winner_candidate_id`).
- MLflow's parent/child run model maps to candidate→exec, but doesn't
  let one exec belong to multiple candidates (no shared baseline). Our
  many-to-many `candidate_agent_execs` covers that.
- Most damningly: MLflow expects the orchestrator to log everything at
  run time. We already log everything to `agent_execs` regardless of
  whether an experiment exists — so any past run can be retroactively
  attached. MLflow has no equivalent of "attach this historical run to
  a new experiment".

### What we explicitly DO NOT store

- **Prompts, models, agents, profiles, costs, tokens, durations** — already
  on linked `agent_execs` rows. Joining recovers everything.
- **Per-candidate parameter blobs** — recoverable via `agent_execs` columns
  + `.ateam/logs/<exec_id>` + `.ateam/runtime/<exec_id>`.
- **Per-experiment configuration** — express via `tags` + the candidates
  themselves.

If we later find a gap (e.g. "extra-prompt was the differentiator and isn't
recorded anywhere"), revisit by adding a column then, not preemptively.

## Worked example: 2 roles + review vs. 1 role + review

This is the canonical "does consolidation lose findings?" question from
EVAL.md, expressed in the MLflow-inspired model with git worktrees as the
isolation mechanism. CLI commands below are sketches for the follow-up
phase — Phase 1 only adds the schema, but walking through the flow
validates that the data model can express it cleanly.

### Setup

Two detached worktrees so the roles run in parallel without stomping each
other's `.ateam/` state. Reusing `internal/eval/worktree.go` (already
copies `.ateam/` minus state).

```
/tmp/ateam-worktree/myproj/A    # for candidate "two-roles"
/tmp/ateam-worktree/myproj/B    # for candidate "one-role"
```

### Step 1 — create the experiment + candidates

```bash
ateam experiment create \
    --name "code-consolidation" \
    --description "Does merging code.small + code.module into code.consolidated lose findings?"
# → INSERT INTO experiments (id=42, name='code-consolidation', ...)

ateam experiment add-candidate 42 --name two-roles
ateam experiment add-candidate 42 --name one-role
# → INSERT INTO candidates (id=101, experiment_id=42, name='two-roles')
# → INSERT INTO candidates (id=102, experiment_id=42, name='one-role')
```

### Step 2 — run the agents in each worktree, auto-attaching execs

The MLflow-style auto-attach hook (forward-scoped feature) makes every
new `agent_exec` link to the named candidate via `candidate_agent_execs`.

```bash
# Candidate A: two roles + review in worktree A
ATEAM_EXPERIMENT=42 ATEAM_CANDIDATE=two-roles \
    ateam report --roles code.small,code.module --work-dir /tmp/ateam-worktree/myproj/A
# → agent_execs id=2001 (role=code.small), id=2002 (role=code.module)
# → candidate_agent_execs (101, 2001), (101, 2002)

ATEAM_EXPERIMENT=42 ATEAM_CANDIDATE=two-roles \
    ateam review --work-dir /tmp/ateam-worktree/myproj/A
# → agent_execs id=2003 (action=review)
# → candidate_agent_execs (101, 2003)

# Candidate B: one role + review in worktree B (parallel-safe)
ATEAM_EXPERIMENT=42 ATEAM_CANDIDATE=one-role \
    ateam report --roles code.consolidated --work-dir /tmp/ateam-worktree/myproj/B
# → agent_execs id=2004
# → candidate_agent_execs (102, 2004)

ATEAM_EXPERIMENT=42 ATEAM_CANDIDATE=one-role \
    ateam review --work-dir /tmp/ateam-worktree/myproj/B
# → agent_execs id=2005
# → candidate_agent_execs (102, 2005)
```

`agent_execs` already records `git_start_hash`/`git_end_hash` per row,
so the worktree HEAD at run time is captured automatically.

### Step 3 — pick artifacts for the judge

Default: judge reads each candidate's review output. Optional explicit
curation pins the review file as the artifact and excludes the
intermediate role reports.

```bash
ateam experiment attach-artifact 101 --exec 2003 --kind file --primary
ateam experiment attach-artifact 102 --exec 2005 --kind file --primary
# → candidate_artifacts rows (kind='file', ref=output_file of the review exec)
```

If skipped, the judge falls back to "all output_files of all linked
execs", which also works.

### Step 4 — judge

```bash
ateam experiment judge 42 --judge-model sonnet
# → agent_execs id=2006 (action='judge')
# → judgments id=501 (experiment_id=42, judge_exec_id=2006,
#                     verdict='consolidation lost two SQL injection findings...',
#                     winner_candidate_id=101)
# → judgment_candidate_scores (501, 101, coverage=0.85, accuracy=0.90, ...)
# → judgment_candidate_scores (501, 102, coverage=0.65, accuracy=0.88, ...)
```

### Step 5 — read results via the view

```sql
SELECT candidate_name, coverage, accuracy, actionability, conciseness, overall, is_winner
FROM experiment_results
WHERE experiment_id = 42
ORDER BY judged_at DESC, candidate_name;
```

```
candidate_name | coverage | accuracy | actionability | conciseness | overall | is_winner
---------------+----------+----------+---------------+-------------+---------+----------
two-roles      |   0.85   |   0.90   |    0.80       |    0.55     |  0.78   |    1
one-role       |   0.65   |   0.88   |    0.75       |    0.80     |  0.77   |    0
```

A second judge run (`--judge-model opus`) would insert another
`judgments` row + new `judgment_candidate_scores` rows; the view now
returns 4 rows (2 candidates × 2 judgments), and the caller picks the
latest by `judged_at`.

### Cost / token comparison (no view needed)

Sum across linked execs per candidate via the join:

```sql
SELECT c.name AS candidate, SUM(ae.cost_usd) AS cost_usd, SUM(ae.duration_ms) AS dur_ms
FROM candidates c
JOIN candidate_agent_execs cae ON cae.candidate_id = c.id
JOIN agent_execs ae ON ae.id = cae.exec_id
WHERE c.experiment_id = 42
GROUP BY c.id;
```

This is what the existing `eval.PrintComparison` does today, but driven
by stored relations instead of in-memory state — so the comparison can
be re-run any time without re-running the agents.

## Files to modify

- `internal/calldb/calldb.go` — schema (the `CREATE TABLE` block at top of
  `Open()`), new struct types, CRUD helpers (`InsertExperiment`,
  `AttachExec`, `RecordJudgment`, …).
- `internal/calldb/calldb_test.go` (new or extended) — schema migration +
  CRUD round-trip tests. Use `./test_data/` per CLAUDE.md.

No changes yet to `cmd/eval.go`, `internal/eval/*`, runner, or container
code — those come in a later phase when we add the new command(s).

## Reused infrastructure

- `agent_execs` (calldb.go:36-73) — authoritative per-run record.
- `git_start_hash` / `git_end_hash` (recent commit `08587ed`) — power
  artifact derivation for `kind='commit'|'diff'`.
- `output_file` / `stream_file` columns — power artifact derivation for
  `kind='file'|'log'`.
- `effectiveWorkDir` / `--work-dir` (runner.go:961, run.go:124-133) — already
  lets agent_execs originate from worktrees; nothing to add here.

## Verification

1. `go build ./...` — schema compiles, no cycles introduced.
2. `go test ./internal/calldb/...` — new tests cover:
   - Migration is idempotent (run `Open()` twice on same file).
   - Insert experiment → candidate → link exec → fetch back joined view.
   - Cascade delete: removing experiment removes candidates,
     candidate_agent_execs, candidate_artifacts, judgments,
     judgment_candidate_scores.
   - Same exec_id can belong to two candidates (reuse case).
3. `make test` — full project test suite.
4. Manual sanity via `duckdb` or `sqlite3`: open `state.sqlite`, list new
   tables, confirm indexes via `.indexes`.

## Out of scope for this phase (note for follow-up)

- New CLI command(s): `ateam experiment create | attach | judge | show | ls`.
- Worktree reuse strategy (single `.ateam/` reference + N rotating worktrees).
- Setup/cleanup scripts per candidate run.
- Auto-derivation rules for `candidate_artifacts` when no rows exist.
- UI surfacing in `ateam serve`.
- **MLflow-style auto-attach hook**: an `ATEAM_EXPERIMENT_ID` env var or
  `--experiment NAME` flag on `run`/`report`/`parallel` so any new
  `agent_exec` auto-links as a candidate of an active experiment. Maps to
  MLflow's autologging idiom and lets users record N variants over a
  session without explicit attach calls.
