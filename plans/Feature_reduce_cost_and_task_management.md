# Feature: Reduce Cost and Task Management

## Summary and Recommendations

A full ateam cycle costs **$19-28**. The three highest-payoff changes, in implementation order:

| # | Change | Saves/cycle | Effort | Why this order |
|---|--------|-------------|--------|----------------|
| 1 | **Task table + direct code execution** | $2-6 | Large | Eliminates code supervisor ($5.71/51min worst case), enables everything below, fixes docker-in-docker, makes execution deterministic |
| 2 | **Adaptive report commissioning** | $2-8 | Medium | Skips entire agent runs ($0.50-$2.63 each) when nothing relevant changed. Depends on task table for state awareness |
| 3 | **Compact task manifests in report prompts** | $1-2 | Small | Replaces full previous report text with ~1 line per existing task. Natural consequence of task table |

Combined estimated savings: **$5-16/cycle (25-55%)**, with the structural benefits (reliability, partial execution, resume, visibility) being as valuable as the cost reduction.

Additionally, **reducing per-report exploration cost** (section 7) targets the $7-10 report phase where 8 agents independently read the same codebase:

| # | Change | Saves/cycle | Effort | Notes |
|---|--------|-------------|--------|-------|
| 4 | **Prompt reorder for caching** | $0.20-0.50 | Trivial | Put shared content first so Claude's prompt cache covers it across 8 roles |
| 5 | **Codebase snapshot pre-step** | $0.50-1.50 | Small | Go-generated file tree + signatures included in all prompts, cached |
| 6 | **Two-phase scan + deep-dive** | $2-4 | Medium | Haiku quick-scan filters which roles need full opus analysis |
| 7 | **Session resume for incremental runs** | $1-4 | Medium | Resume previous report session with git diff, skip re-reading unchanged files |

Additional changes worth doing in parallel (small effort, independent):
- Fix concurrency bugs (7a) — blocks adoption
- Reduce default roles from 8 to 5-6 — saves $2-4/cycle
- Fix token accounting — needed for accurate cost tracking
- Prompt reorder for caching (4a) — trivial, guaranteed small win

**Delta reports were considered and rejected** — the complexity isn't justified because report cost is dominated by agent exploration (reading files, running commands), not prompt size. Commissioning handles the "nothing changed" case better by skipping the role entirely.

**Sequential generic reporter was considered and rejected** — saves $2-3 but 4-5x worse wall clock, cross-contamination between roles, and quality degradation from generalist analysis.

**SDK-based context forking was considered and deferred** — highest theoretical savings (50-70%) but requires migrating from CLI subprocess to Agent SDK, a large architectural change.

---

## Cost Breakdown (from real `ateam ps` data)

| Phase | Cost | Tokens | Duration | Notes |
|-------|------|--------|----------|-------|
| **Reports** (8-10 roles) | $7-10 | 150K-2.3M per role | 2-8 min each | Many roles timeout at 20min |
| **Review** | $0.40 | 7-12K | 2-3 min | Cheap — reads all reports |
| **Code supervisor** | $1.82-5.71 | 94K-8.1M | 4s-51min | LLM orchestration overhead |
| **Code sub-runs** (5-10 tasks) | $10-12 | 165K-5.1M per task | 1-18 min each | Actual coding work (irreducible) |

Observations:
- Code supervisor is the biggest single-agent cost. It spends tokens reasoning about how to call `ateam run`, not doing actual work.
- Reports are expensive in aggregate and many fail by timeout. `critical_code_reviewer` is consistently the most expensive ($1.87-$2.63).
- Review is cheap. Optimizing review prompts saves tokens but minimal $.
- Code sub-runs are irreducible — they do the actual coding.

---

## 1. Task Table and Direct Code Execution

### Overview

Replace the markdown-based report→review→code pipeline with a structured task table. Agents interact with the table via `ateam task` CLI commands instead of producing/consuming full text documents.

### Current architecture (everything in prompts)

```
report agents → full prose reports → review agent reads all reports →
  full prose review → code supervisor agent → nested ateam run calls per task
```

Each handoff embeds full documents verbatim. The code supervisor (211-line prompt) manages: role discovery, task planning, prompt generation via `ateam prompt`, execution via `ateam run`, git workflow, error handling, report updates.

### Proposed architecture (structured data + targeted agents)

```
report agents → ateam task add (per finding) → task table (SQLite)
review agent  → ateam task list → ateam task update --state code/deferred/ignore
ateam code    → Go loop over state=code tasks → run coding agent per task
coding agent  → implements fix → ateam task update --state done + git commit
```

The code management supervisor is eliminated for the happy path. Only on failure does an LLM get involved for troubleshooting.

### Task table schema

```sql
CREATE TABLE tasks (
    id            INTEGER PRIMARY KEY,
    subject       TEXT NOT NULL,          -- short title
    description   TEXT,                   -- full finding detail (from stdin)
    source_role   TEXT NOT NULL,          -- e.g. "security"
    source_action TEXT NOT NULL,          -- "report" or "review"
    severity      TEXT,                   -- CRITICAL/HIGH/MEDIUM/LOW
    effort        TEXT,                   -- SMALL/MEDIUM/LARGE
    location      TEXT,                   -- file paths, line numbers
    state         TEXT DEFAULT 'open',    -- open/code/in_progress/done/failed/deferred/ignore
    priority      TEXT,                   -- P0/P1/P2 (set by review)
    dedup_key     TEXT,                   -- normalized subject+role for matching
    created_at    TEXT,
    updated_at    TEXT,
    report_group  TEXT,                   -- task_group of originating report run
    review_group  TEXT,                   -- task_group of review that triaged this
    code_group    TEXT,                   -- task_group of code run that attempted this
    commit_hash   TEXT,                   -- git commit if completed
    failure_note  TEXT,                   -- why it failed, for troubleshooting
    exec_id       INTEGER REFERENCES calls(id)  -- link to calldb execution record
);
```

### How each phase changes

**Report phase** — agents call `ateam task add` per finding:

```bash
echo "FULL FINDING DESCRIPTION" | ateam task add \
  --subject "Fix SQL injection in auth handler" \
  --severity HIGH --effort SMALL \
  --reporter security:report \
  --task-group "$TASK_GROUP" \
  --exec-id "$EXEC_ID"
```

The report.md becomes human-readable summary + `ateam task list` output. It's no longer consumed by other agents — the task table is the source of truth.

Dedup: `ateam task add` hashes subject+source_role as `dedup_key`. If an open task with the same key exists, it updates the description/severity instead of creating a duplicate. The agent doesn't need to know about dedup.

Report prompt changes: instead of including the full previous report, include a compact task manifest:
```
# Known findings (do not re-report unless your assessment changed):
- [DONE] Fix SQL injection in auth handler (security, HIGH)
- [OPEN] Add input validation for API endpoints (security, MEDIUM)
- [DEFERRED] Upgrade lodash to v5 (dependencies, LOW)
```

This is ~1 line per finding vs ~10-20 lines per finding in the current prose approach.

**Review phase** — agent reads tasks via CLI, sets state:

```bash
ateam task list --state open --sort severity
# Agent decides which tasks to execute, then:
ateam task update --id 5 --state code --priority P0
ateam task update --id 8 --state deferred
ateam task update --id 12 --state ignore
```

The review.md becomes a human-readable summary. The task state changes are the actionable output.

**Code phase** — Go loop replaces code management supervisor:

```go
tasks := db.Query("SELECT * FROM tasks WHERE state = 'code' ORDER BY priority")
for _, task := range tasks {
    // Pre-check: git clean, build passes, tests pass
    db.UpdateState(task.ID, "in_progress")
    prompt := assembleCodePrompt(task)  // task description + code_base_prompt
    result := runner.Run(ctx, prompt, opts)
    if result.Err == nil {
        // Post-check: build passes, tests pass
        db.UpdateState(task.ID, "done", commitHash)
    } else {
        git.RevertDirtyFiles()
        db.UpdateState(task.ID, "failed", result.Err.Error())
        // Optionally: run troubleshooting agent
    }
}
```

No more nested `ateam run` calls. No more 211-line code management prompt. Each coding agent gets just its task description + the code base prompt.

**User editing** — users can triage tasks before coding:

```bash
ateam task list --state open              # see all findings
ateam task update --id 5 --state code     # approve for coding
ateam task update --id 8 --state ignore   # skip
ateam task list --state code              # verify what will be coded
ateam code                                 # execute
```

The web UI (`ateam serve`) can add a task management view.

### CLI surface

New commands:
```
ateam task add      --subject TEXT --severity SEV --effort EFF --reporter ROLE:ACTION [--task-group TG] [--exec-id ID]
ateam task list     [--state STATE] [--role ROLE] [--task-group TG] [--sort FIELD] [--format table|json]
ateam task update   --id ID --state STATE [--priority P] [--commit HASH] [--note TEXT]
ateam task show     --id ID
ateam task clear    [--state done|ignore] [--older-than 30d]
```

### Token savings

| Phase | Current | With task table | Savings |
|-------|---------|----------------|---------|
| Reports (8 roles) | $7-10 | $5-8 | ~$1-2 (compact manifests instead of full previous reports) |
| Review | $0.40 | $0.15-0.25 | ~$0.20 (reads task rows, not 8 full reports) |
| Code supervisor | $1.82-5.71 | **$0** | **$2-6** (Go loop, no LLM orchestration) |
| Code sub-runs | $10-12 | $9-11 | ~$1-2 (cleaner per-task prompts) |
| **Total** | **$19-28** | **$14-20** | **$5-10 (25-35%)** |

### Structural benefits beyond cost

- **Partial execution**: run 3 tasks, stop, resume later
- **Resume after interrupt**: tasks retain state across runs
- **Visibility**: `ateam task list` shows real-time status, links commits to findings
- **No docker-in-docker**: Go loop runs agents directly, supervisor doesn't call `ateam run`
- **Deterministic execution**: Go controls the loop, retry logic, git workflow — not an LLM
- **Failure isolation**: one task failure doesn't cascade to others
- **Timeout resilience**: partial findings from timed-out report runs are already saved via `ateam task add` calls made before timeout

### Design decisions

- **Dedup strategy**: hash of subject + source_role. Existing open task with same key gets updated, not duplicated.
- **Task table location**: same `state.sqlite` in `.ateam/` (new table alongside existing `calls` table).
- **Backward compatibility**: keep `review.md` and `report.md` as human-readable artifacts generated from task table state. Old pipeline (`--supervised` flag) can coexist during transition.
- **Failure troubleshooting**: optional — on task failure, run a haiku-class agent with the error output to suggest a fix. Record suggestion in `failure_note`. This is cheap (~$0.05) and helps the next cycle.

---

## 2. Adaptive Report Commissioning

### Overview

Before running all enabled roles, decide which ones actually need to run based on what changed and what's already known. Skip roles where nothing relevant changed and tasks are stable.

### Input per role (compact, from task table + git)

```
Role: security       | Last run: 2026-04-09 | Open: 5 | Done since: 0 | Changed files: cmd/secret.go, Makefile
Role: testing_basic  | Last run: 2026-04-09 | Open: 2 | Done since: 2 | Changed files: internal/runner/*.go, cmd/report.go (12 files)
Role: dependencies   | Last run: 2026-04-09 | Open: 1 | Done since: 0 | Changed files: (none)
Role: docs           | Last run: 2026-04-09 | Open: 0 | Done since: 1 | Changed files: README.md
```

### Decision logic

Can be rule-based (no LLM cost) or LLM-based ($0.10-0.30):

**Rule-based** (recommended as default):
- Skip if: 0 relevant file changes AND 0 tasks completed since last run
- Skip if: role last ran < 24h ago and no git commits since
- Always run if: explicitly requested via `--roles`

**LLM-based** (opt-in with `--smart-commissioning`):
- Supervisor sees the table above + role descriptions
- Decides which roles to run and why
- Useful when file-change heuristics are too coarse (e.g., "config.go changed but it's only a comment fix")

### Savings

Skipping 3-4 roles at $0.50-$2.63 each saves **$2-8/cycle**. The commissioning step itself costs $0 (rules) or ~$0.10-0.30 (LLM). Net savings are large and predictable.

### Integration with task table

The task table makes commissioning much more informed. Without it, you'd need to parse report files to understand what's been found and resolved. With it, a simple SQL query gives you open/done/deferred counts per role.

```sql
SELECT source_role, state, COUNT(*) FROM tasks
WHERE source_action = 'report'
GROUP BY source_role, state
```

---

## 3. Delta Reports — Assessment (Not Recommended)

Delta reports would feed agents only what changed since their last run (git diff) instead of the full codebase.

**Why not:**

1. **Cost is in exploration, not prompts.** Report agents cost $0.45-$2.63 each. The previous-report inclusion is ~$0.10-0.20 of that. The rest is the agent reading files, running commands, reasoning about code structure. A smaller initial prompt doesn't reduce exploration.

2. **Missing transitive context.** A change in `config.go` might create a security issue in `auth.go` that didn't change. Delta agents won't trace impact across unchanged files.

3. **Quality drift.** After N delta runs, findings are a patchwork from different snapshots. The agent can't assess whether an earlier finding is still valid without seeing the current code.

4. **Commissioning handles "nothing changed" better.** If 0 relevant files changed, commissioning skips the role entirely ($0). Delta still runs the agent, pays the base cost, and gets "no new findings" ($0.30-0.50 for nothing).

5. **Two code paths, double the complexity.** Different prompts, output handling, merging logic, and tests for `--full` vs `--delta`.

6. **Large deltas negate the benefit.** After a refactor touching 50 files, the delta is the whole codebase anyway.

---

## 4. Reducing Per-Report Exploration Cost

### The problem

8 report agents each independently explore the same codebase. A typical agent reads 20-50 files, runs grep/find commands, and reasons about what it finds. With ~50% overlap in files read across roles, that's $3-5 of redundant exploration per cycle. Most of the $7-10 report cost is this exploration, not prompt processing.

### Evaluated approaches

| Approach | Saves/cycle | Wall clock | Effort | Verdict |
|----------|-------------|------------|--------|---------|
| Prompt reorder for caching | $0.20-0.50 | Same | Trivial | **Do first** |
| Codebase snapshot pre-step | $0.50-1.50 | Same | Small | **Do second** |
| Two-phase scan + deep-dive | $2-4 | ~Same | Medium | **Do third** |
| Session resume | $1-4 | Same | Medium | **Do fourth** |
| Third-party indexing (LSP) | $0.50-2 | Same | Medium | Limited ceiling, optional |
| SDK context forking | $3-5 | Same | Large | Deferred — requires architecture change |
| Sequential generic reporter | $2-3 | 4-5x worse | Medium | **Rejected** — quality loss |

### 4a. Prompt reorder for caching [TRIVIAL, DO FIRST]

Claude's prompt caching charges ~10% for cache reads vs 100% for fresh input. Caching only applies to matching prefixes — if the first tokens differ between roles, nothing is cached.

**Current prompt order** (`prompts.go:96-124`): ProjectInfo → RolePrompt → BasePrompt → Extras → PreviousReport → CLI extra. The role-specific prompt comes second, making each role's prompt unique from byte ~200 onward. No caching benefit across roles.

**Proposed order**: ProjectInfo → BasePrompt → PreviousReport/TaskManifest → Extras → **RolePrompt last** → CLI extra. The 8 roles now share a long common prefix (project info + base instructions + task manifest = 5-15K tokens). After the first role, 7 subsequent roles pay ~10% for this prefix.

Savings: 7 roles × 10K shared tokens × 0.9 discount = ~63K tokens saved per cycle. At $3/M input: ~$0.19. Modest but free.

**Implementation**: Swap the order in `assembleRoleAction()` — put `basePrompt` before `rolePrompt` in the `parts` slice. One-line change in `prompts.go`.

### 4b. Codebase snapshot pre-step [SMALL EFFORT, DO SECOND]

Before launching report agents, ateam generates a structured codebase summary in Go (no LLM, no cost). This gives agents a navigation map so they go directly to relevant files instead of spending 30-60 seconds exploring directory structure.

#### Tooling research

| Tool | Languages | Integration | Extracts signatures | Cross-compile safe | Speed |
|------|-----------|-------------|--------------------|--------------------|-------|
| **universal-ctags** | 164 | CLI (`ctags --output-format=json`) | Yes | N/A (external dep) | <1s |
| **gotreesitter** (pure Go) | 206 | Go library import | Yes (via .scm queries) | Yes (no CGo) | ~4ms/file |
| **scc** | Hundreds | Go library import | No (lines/complexity) | Yes | Fastest |

**Recommended approach — two layers:**

**Layer 1 (immediate):** Shell out to `universal-ctags --output-format=json --fields=+nKSZ`. Parse JSON in Go, group by file, filter to functions/types/interfaces/classes. Tested on ateam: ~16K tokens for 121 Go files with full signatures. Already available on most dev machines and CI (`brew install universal-ctags`, `apt install universal-ctags`). If ctags is not installed, fall back to grep-based extraction or skip (graceful degradation).

**Layer 2 (medium-term):** Import `github.com/odvcencio/gotreesitter` — pure Go tree-sitter runtime, 206 grammars, no CGo. Critical for ateam's cross-compilation (`make companion` builds linux/amd64). Use per-language `.scm` query files (borrow from aider's MIT-licensed `tags.scm` collection) to extract symbol definitions and references. Build a simple reference-count ranker: symbols referenced by more files rank higher. This produces aider-quality maps without Python dependency.

**Complement with scc:** Import `github.com/boyter/scc/v3` for per-file line counts and complexity scores. Builds the file tree portion of the map.

**Evaluated and not recommended:**
- **repomix** (`--compress` mode does exactly what we want): requires Node.js runtime, too heavy as dependency
- **sourcegraph/scip, zoekt**: enterprise-grade code intelligence, far too heavy
- **go/ast**: Go-only, not multi-language
- **aider's repo-map**: Python-only, but its PageRank ranking algorithm is worth reimplementing in Go

#### Output format

```
# Codebase Map (auto-generated)

## Structure (87 files, 12,431 lines)
cmd/           14 files  3,201 lines  complexity: 142
internal/      38 files  6,890 lines  complexity: 298
  agent/        5 files  1,102 lines
  calldb/       3 files    892 lines
  config/       2 files    441 lines
  ...
defaults/      22 files  1,504 lines
test/           5 files    844 lines

## Key Symbols (ranked by cross-file references)
internal/runner/runner.go:
  func (r *Runner) Run(ctx context.Context, prompt string, opts RunOpts, progress chan RunProgress) RunSummary
  func (r *Runner) RunPool(ctx context.Context, tasks []PoolTask, maxParallel int) []RunSummary
  type Runner struct
  type RunOpts struct
  type RunSummary struct

cmd/report.go:
  func runReport(opts ReportOptions) error
  type ReportOptions struct

internal/prompts/prompts.go:
  func AssembleRolePrompt(...) (string, error)
  func AssembleReviewPrompt(...) (string, error)
  func AssembleCodeManagementPrompt(...) (string, error)
...

## Dependencies (from go.mod / package.json / requirements.txt)
github.com/spf13/cobra v1.8.0
modernc.org/sqlite v1.29.1
...

## Recent Changes (last 10 commits)
9a7b6fc Makefile: added build-all
111fac4 testing roles: don't propose CI/CD so often
...
```

Target: 5-20K tokens depending on codebase size. Include as the first section of every role's prompt. Combined with prompt reorder (4a), this shared prefix is cached across all 8 roles — 7 roles pay ~10% for the cached map.

#### Evidence of effectiveness

No published quantitative benchmarks exist for repo maps reducing agent cost. The indirect evidence is strong:
- aider, Claude Code, Cursor, and repomix (23K GitHub stars) all implement codebase context injection
- aider defaults to 1K tokens for its map, expanding to 8K when no files are in chat — even a small map helps
- A 5K-token map replaces ~10-20 exploratory tool calls per agent (ls, find, grep, cat for structure discovery)
- With 8 parallel agents: 80-160 avoided tool calls, estimated 20-50K tokens saved per agent = 160-400K tokens/cycle

**Implementation**: New `internal/codebase/` package. `GenerateMap(sourceDir string) (string, error)` tries ctags first, falls back to grep-based extraction. Complement with scc stats. Include output in `FormatProjectInfo()` or as a separate prompt section.

**Estimated savings**: 10-30% reduction in per-role exploration ($0.05-0.20 per role × 8 = $0.40-1.60/cycle). The map also improves report quality by directing agents to the most important code first.

### 4c. Two-phase scan + deep-dive [MEDIUM EFFORT, BEST NEW SAVINGS]

Instead of running all roles with the expensive model:

**Phase 1 — Quick scan** (haiku, $0.03-0.05/role): Run all enabled roles with a cheap model. Prompt: "Quickly assess whether there are findings worth detailed analysis. Report YES with a 1-line summary of potential issues, or NO."

**Phase 2 — Deep dive** (opus, $0.50-2.00/role): Only roles where the scan found potential issues get a full analysis.

```
Current:   8 roles × $0.75 avg                    = $6.00
Two-phase: 8 × $0.05 (scan) + 4 × $0.75 (dive)   = $3.40
Savings:   ~$2.60 (43%)
```

This compounds with commissioning: commissioning asks "should this role run at all?" (based on code changes), two-phase asks "is there anything to find?" (based on quick analysis). Together they can reduce 8 roles to 2-3 deep dives.

**Implementation**: Add `--scan` flag to `ateam report` that runs phase 1 with haiku, then phase 2 with the configured model for roles that returned YES. Or make it the default behavior with `--full` to force all roles at full depth.

**Risks**: Haiku might miss findings that opus would catch (false negative on scan). Mitigate by periodically forcing full scans (`--full`) and by tuning the scan prompt to be inclusive rather than selective ("if in doubt, say YES").

### 4d. Session resume for incremental reports [MEDIUM EFFORT]

Claude Code supports `--resume SESSION_ID` with `-p` mode. The resumed session maintains full conversation history including all files the agent previously read. This means:

1. First run: agent explores codebase, produces report. Session ID saved.
2. Second run: `--resume SESSION_ID` with prompt "The codebase has changed since your last analysis. Here are the changes: [git diff summary]. Update your findings."
3. Agent already has file contents in context from the first run. Only reads changed files.

**Key constraint**: The 200K context window. After auto-compaction, file contents are summarized away. Effectiveness depends on how much context was used:

| Context usage after first run | Resume effectiveness | Savings |
|-------------------------------|---------------------|---------|
| <30% (small codebase) | Agent has most files in context | 50-70% |
| 30-60% (medium codebase) | Agent has key files, needs some re-reads | 30-50% |
| >60% (large codebase) | Auto-compaction lost details, needs many re-reads | 10-20% |

**Implementation details**:
- Store `session_id` in the calls table (from Claude's stream-json output)
- On next report run for the same role, check if a previous session exists
- If yes and context was <threshold: use `--resume SESSION_ID` with a delta prompt
- If no or context was high: start fresh
- Docker: session files live at `~/.claude/projects/<encoded-cwd>/` — need to be mounted or stored in shared config

**Integration with task table**: The delta prompt includes the compact task manifest instead of the full previous report. The agent sees "these tasks are already known, focus on what's new."

```bash
# Resume with task context
claude -p --resume "$SESSION_ID" --output-format stream-json <<EOF
Git changes since your last analysis:
$(git diff --stat $LAST_COMMIT..HEAD)

Known findings (do not re-report unless changed):
$(ateam task list --role security --format compact)

Analyze the changes and report new findings using ateam task add.
EOF
```

**Estimated savings**: $1-4/cycle depending on codebase size and change frequency.

### 4e. Approaches evaluated and not recommended

**Sequential generic reporter** — One agent runs all roles sequentially, keeping codebase in context across role switches.

Rejected because:
- **Wall clock 4-5x worse**: 8 roles × 5 min = 40 min sequential vs ~8 min with 3-parallel
- **Cross-contamination**: Security findings bias the testing analysis. A finding about "unsafe input handling" makes the agent over-focus on input-related code when analyzing refactoring opportunities.
- **All-or-nothing**: If the agent crashes at role 4, roles 5-8 never run. With the task table, all findings from the first 4 roles are also lost (no `ateam task add` calls from a crashed session).
- **Context compaction**: After 2-3 roles fill 60%+ context, auto-compaction summarizes away file contents. Later roles don't benefit from the shared reading.
- **Quality**: A generalist doing "now analyze security, now analyze testing" produces shallower findings than a focused specialist.

**Savings: $2-3/cycle. Not worth the 4-5x wall clock increase and quality loss.**

**SDK-based context forking** — Run a base exploration agent, then fork 8 sessions from its context. Each fork starts with files already in context.

This is the theoretically optimal approach: files read once, analyzed 8 times. The Agent SDK supports `forkSession` which creates a new session with a copy of the original's history.

Deferred because:
- Requires migrating from `claude -p` subprocess spawning to the TypeScript/Python Agent SDK
- This is a significant architectural change to `internal/agent/` and `internal/runner/`
- The shared snapshot approach (4b) gets ~60% of the benefit with ~20% of the effort
- Worth revisiting after the task table work stabilizes

**Savings: $3-5/cycle. Large effort. Revisit later.**

**Third-party indexing** — Use LSP, tree-sitter, or vector embeddings to pre-index the codebase.

Claude Code already has LSP support for navigation (go-to-definition, find-references). This helps agents find code faster but doesn't fundamentally reduce exploration — agents still need to read and understand code to assess quality.

A pre-generated index could help specific roles (dependencies can scan package manifests, security can focus on known vulnerability patterns) but the savings are modest because the deep analysis is the expensive part, not the navigation.

**Savings: $0.50-2/cycle. Medium effort. Limited ceiling. Optional.**

---

## 5. Prompt Optimizations (Independent of Task Table)

These are smaller changes that can be done in parallel or before the task table work. Mostly superseded by the task table (section 1) and exploration reduction (section 4), but useful as interim improvements or if those larger changes are deferred.

### 5a. Trim reports in review prompt

`prompts.go:213-220` — strip reports to Summary + Findings sections before embedding in review. Add `TrimReportForReview()`. Saves ~30-50% of review prompt size. Minor $ savings (~$0.10) since review is already cheap, but cleaner architecture. Superseded by task table (review reads task rows instead of reports).

### 5b. Extract Priority Actions for code management prompt

`prompts.go:301` — embed only `### Priority Actions` section from review instead of full document. Saves ~40-60% of code management input. Superseded by task table (code phase reads task rows, no code management prompt).

### 5c. Compress code management prompt

`code_management_prompt.md` is 211 lines. Git Workflow repeats `code_base_prompt.md`. Verbose Output uses 30 lines of examples for 5 lines of content. Can be tightened ~30-40%. Superseded by task table (eliminates code management supervisor entirely).

### 5d. Address timeout failures

Many roles timeout at 20min default (docs_internal, production_ready, refactor_architecture, security, testing_basic). Options:
- Per-role timeout in config.toml
- Tighter role prompts to limit exploration scope
- With task table: partial findings from timed-out runs are already saved via `ateam task add` calls made before timeout
- With two-phase scan (4c): haiku scan rarely times out, opus deep-dives are more focused

---

## 6. Product Simplification

### 6a. Reduce default roles from 8 to 5-6

Current defaults: database_schema, dependencies, docs_external, docs_internal, project_characteristics, refactor_small, security, testing_basic

Proposed defaults: dependencies, refactor_small, security, testing_basic, docs (merged external+internal)

Remove from defaults: database_schema (many projects have no DB), project_characteristics (make it a setup-time thing)

Saves $2-4/cycle from fewer agent runs.

### 6b. Simplify profile resolution

Current: 5-level resolution + 4 supervisor profile settings + 6 per-stage overrides on `all`.

Proposed: CLI flag → config.toml → "default". One `supervisor_profile` field instead of four.

### 6c. Consolidate observability commands

Keep `ps`, `tail`, `cat`. Merge `cost` into `ps --cost`. Merge `inspect` into `ps --inspect ID`. Add `task list` and `task show` as new first-class commands.

### 6d. Make org creation implicit

Auto-create `$HOME/.ateamorg` on first use. `ateam init` becomes the only required setup.

---

## 7. Pre-Adoption Fixes

### 7a. Fix concurrency bugs [CRITICAL]

- `internal/runner/race_test.go`: `ResolveAgentTemplateArgs()` mutates shared `r.Agent.Args` in parallel. Clone args slice before template resolution.
- `internal/agent/bugs_test.go`: `buildProcessEnv()` doesn't deduplicate env keys. Use a map.

These are correctness bugs affecting the most common operation (parallel reports).

### 7b. Fix token accounting

`CacheWriteTokens` never captured (documented in bugs_test.go). Parse `cache_creation_input_tokens` from Claude's stream output.

### 7c. Add end-to-end pipeline test

No test covers report → review → code. Add one with mock agent producing canned outputs.

### 7d. Other

- Warn on partial report failures before review runs
- State cleanup: `ateam cleanup [--older-than 30d]` or `ateam task clear`
- UTF-8 truncation: `Truncate()` in runner.go cuts mid-character
- First-run diagnostics: preflight check for Claude CLI, auth, project init

---

## Implementation Order

### Phase 1: Foundations (can be done now, independent)
1. Fix concurrency bugs (7a) — small, blocks adoption
2. Fix token accounting (7b) — small, needed for accurate cost tracking
3. Reduce default roles (6a) — small, immediate savings
4. Prompt reorder for caching (4a) — trivial, guaranteed small win

### Phase 2: Task Table Core
5. Task table schema + `ateam task add/list/update/show/clear` commands
6. Modify report prompts: agents call `ateam task add` per finding, receive compact task manifest instead of full previous report
7. Modify report output: summary prose + `ateam task list` output
8. Modify review: agent reads tasks via CLI, sets state via CLI
9. Implement direct code execution: Go loop over state=code tasks, replaces code management supervisor

### Phase 3: Exploration Reduction
10. Codebase snapshot pre-step (4b) — Go-generated map included in prompts
11. Two-phase scan + deep-dive (4c) — haiku scans filter which roles need opus
12. Rule-based commissioning (2) — skip roles with no relevant changes and no resolved tasks

### Phase 4: Session Intelligence
13. Session resume for incremental reports (4d) — store session_id, resume with delta prompt
14. Optional LLM-based commissioning for smarter decisions

### Phase 5: Polish
15. Web UI task management view
16. Profile simplification, command consolidation
17. End-to-end tests for new pipeline
18. Evaluate SDK context forking if session resume proves insufficient

---

## Key Files

| File | Relevance |
|------|-----------|
| `internal/calldb/calldb.go` | Add tasks table, add session_id column to calls |
| `internal/prompts/prompts.go` | Prompt reorder (4a), task manifests, snapshot inclusion |
| `defaults/report_base_prompt.md` | Change report instructions to use `ateam task add` |
| `defaults/supervisor/review_prompt.md` | Change review to use `ateam task list/update` |
| `defaults/supervisor/code_management_prompt.md` | Eventually replaced by Go loop |
| `defaults/code_base_prompt.md` | Change coding agent to call `ateam task update --state done` |
| `cmd/code.go` | Implement Go task loop replacing supervisor |
| `cmd/task.go` | New file: `ateam task` subcommands |
| `cmd/report.go` | Integrate commissioning, two-phase scan, session resume |
| `internal/runner/runner.go` | Fix race condition, session ID capture from stream output |
| `internal/agent/` | Fix env dedup, token accounting |
| `internal/codebase/` | New package: codebase snapshot generator (4b) |
| `defaults/config.toml` | Reduce default roles |

## Verification

- `go build ./...` and `go test ./...` after every change
- `go test -race ./...` after concurrency fixes
- Compare `ateam cost` output before/after on a real project for each phase
- `ateam task list` produces correct output after report runs
- `ateam code` executes tasks and links commits correctly
- Verify dedup: run `ateam report` twice, check task count doesn't double
- Verify commissioning: modify 1 file, check that only relevant roles run
- Verify two-phase: run with `--scan`, verify haiku filters correctly, opus runs only for flagged roles
- Verify session resume: run report, make small change, run report again, check fewer tokens used
- Verify prompt caching: compare cache_read_tokens before/after prompt reorder
