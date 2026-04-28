# Feature: Dot-Namespaced Roles and Prompt Eval Framework

## Part 1: Dot-Namespaced Roles

### Separator options evaluated

| Separator | CLI feel | Filesystem | TOML | Glob |
|-----------|---------|------------|------|------|
| `/` | `--roles code/architecture` | Nested dirs (works natively) | Needs quoted keys | `code/*` natural |
| `-` | `--roles code-architecture` | Flat (works) | Bare key OK | Ambiguous with names |
| `.` | `--roles code.architecture` | Flat (works) | Needs quoted keys (unquoted `.` = nesting) | `code.*` natural |
| `:` | `--roles code:architecture` | Flat (works) | Needs quoted keys | `code:*` OK |
| frontmatter | `--roles code` (collection) | Flat (works) | No change | Needs metadata parsing |

**Recommendation: `.`**. It reads naturally (`--roles code.architecture`, `--roles testing.*`), keeps the filesystem flat (the dot is part of the directory name, no nested dirs), and requires no changes to role discovery code. TOML quoting (`"code.architecture" = "on"`) is the same minor cost as `/` or `:`.

### How `.` works with the filesystem

The dot is part of the directory name — the filesystem stays flat. `filepath.Join(projectDir, "roles", "code.architecture")` produces `.ateam/roles/code.architecture/`, a single directory at the same level as all other roles.

```
defaults/roles/
├── code.small/report_prompt.md
├── code.module/report_prompt.md
├── code.architecture/report_prompt.md
├── testing.basic/report_prompt.md
├── testing.full/report_prompt.md
├── testing.flaky/report_prompt.md
├── docs.external/report_prompt.md
├── docs.internal/report_prompt.md
└── security/report_prompt.md          ← standalone role, no collection prefix
```

Role IDs become: `code.small`, `code.architecture`, `testing.basic`, `docs.external`, `security`. Roles without a dot (e.g. `security`, `dependencies`) are equally valid. Both dotted and dotless roles coexist — existing roles stay as-is.

The dot is a naming convention, not a system concept. There is no formal "collection" abstraction. The dot just enables prefix-based glob selection (`--roles testing` or `--roles testing.*`).

Because the layout is flat, **existing role discovery code needs no changes** — `fs.ReadDir("roles")` and `os.ReadDir(rolesDir)` read dot-named directories as single entries. Role names are treated as opaque strings throughout the codebase (no splitting, no special-character validation).

### Changes needed

**1. Role discovery** — no change needed.

`discoverRoleIDs()` and `scanRolesDir()` in `internal/prompts/embed.go` use single-level `fs.ReadDir` / `os.ReadDir`. Since dot-named directories are flat entries, these functions pick up `code.small`, `testing.basic`, etc. without modification.

**2. Prefix expansion and glob in `--roles`** (`internal/prompts/embed.go` `ResolveRoleList`)

Resolution order for each entry in `--roles`:

1. `code.small` → exact match (unchanged)
2. `security` → exact match if a role named `security` exists (unchanged)
3. `testing` → no exact match, try prefix `testing.` → expands to all enabled `testing.*` roles (includes `testing.unit`, `testing.e2e.smoke`, etc.)
4. `testing.*` → explicit glob, expands to all enabled roles starting with `testing.`
5. `all` → unchanged (all enabled, including dotless roles)
6. no match → error

Exact match always wins. Prefix expansion only triggers when there is no role with that exact name. This means dotless roles like `security` are never accidentally expanded.

Multi-level roles work naturally: `--roles infra` expands to all `infra.*` roles including `infra.db.schema` and `infra.db.config`.

Implementation: in `ResolveRoleList`, if no exact match, try prefix `roleID + "."` against all known roles. If matches found, use them. If a role contains `*`, treat as explicit prefix match. This is the only code change to discovery/resolution.

**3. Config.toml** — quoted keys for collection roles:

```toml
[roles]
"code.small" = "on"
"code.architecture" = "off"
"testing.basic" = "on"
security = "on"            # standalone, still works unquoted
```

No code change needed — BurntSushi/toml handles quoted dotted keys as literal keys into `map[string]string`. Unquoted `code.small` would be interpreted as TOML nesting — the quotes are required.

**4. `ateam roles` display** — group by shared prefix for readability:

```
code.small           on   Concrete code improvements: naming, duplication, ...
code.module          off  Module-level refactoring: interfaces, coupling, ...
code.architecture    off  Architecture-level analysis: layers, patterns, ...
testing.basic        on   Ensures minimal high-value regression tests
testing.full         off  Comprehensive test suite analysis
security             on   Security vulnerability analysis
dependencies         on   Dependency health assessment
refactor_small       on   Concrete code improvements (legacy)
```

Roles are sorted alphabetically. The dot prefix naturally groups related roles together. No special grouping logic needed — just `sort.Strings`.

### Proposed role naming

| Prefix | Roles | Description |
|--------|-------|-------------|
| `code.` | small, module, architecture | Code review at increasing abstraction levels |
| `testing.` | basic, full, flaky, ci | Test coverage, quality, CI health |
| `docs.` | external, internal, first_time, upgrade | Documentation quality |
| `security.` | code_audit, system_audit | Security vulnerabilities |
| `infra.` | database_schema, database_config, production_ready | Infrastructure concerns |
| `feedback.` | engineering, project, shortcuts | Critical reviews, run less often |
| (no prefix) | dependencies, automation | Standalone roles |

TODO: feedback/critic, db/audit-only vs. migration allowed, etc ...

Exact naming TBD — roles can be added/renamed freely since the dot is just a convention.

### Migration

No migration needed, old names keep working.

- Old flat names (`refactor_small`) continue to work as-is
- New names (`code.small`) are the canonical form

Once evaluation of the new roles is done, old roles will be removed. Migration will be to save them in ateamorg so they have frozen defaults for these installs alone.

### Files to modify

| File | Change |
|------|--------|
| `defaults/roles/` | Keep old directories: `refactor_small/` |
| `defaults/config.toml` | Use new names with quoted keys |
| `cmd/roles.go` | No change (alphabetical sort already groups by prefix) |
| `internal/prompts/embed.go` | Only if glob (`code.*`) is implemented: prefix matching in `ResolveRoleList` |
| `internal/config/config.go` | No change (map[string]string handles `.` keys) |
| `internal/root/resolve.go` | No change (filepath.Join handles dots in names) |
| `internal/prompts/embed.go` | No change to discovery (`discoverRoleIDs`, `scanRolesDir`) |

---

## Part 2: Prompt Eval Framework

### What eval does

Eval compares two prompts for the same role by running each against the same codebase and comparing the results. The output is:

1. **Cost comparison** — token usage and estimated cost for each prompt (from `state.sqlite`)
2. **Judge score** — an LLM agent reads both reports and scores each 0.0–1.0 on coverage, accuracy, actionability, conciseness

No persistent eval storage in phase 1. Results are printed to stdout. The reports themselves live in the respective `.ateam/` directories if you want to inspect them after.

### Three modes

**Mode 1: Sequential (simple, single directory)**

Runs base then candidate in the same project directory. Uses `--ignore-previous-report` so the second run isn't influenced by the first run's output.

```bash
ateam eval --role security --prompt @candidate.md
```

Steps:
1. Run `ateam report --roles {role} --ignore-previous-report` with current on-disk prompt (base)
2. Save report output + cost/tokens from `state.sqlite`
3. Install candidate prompt, run again with `--ignore-previous-report`
4. Save report output + cost/tokens
5. Restore original prompt
6. Run judge, print comparison

Pro: no setup, no extra directories, works anywhere.
Con: sequential (slower), not perfectly isolated (second run happens after first may have modified codebase state — though `--ignore-previous-report` prevents prompt contamination).

**Mode 2: Two directories (parallel)**

User provides two directories, each an initialized ateam project pointing at the same (or different) codebase. Runs in parallel.

```bash
ateam eval --role security --prompt @candidate.md --dirs ./project1 ./project2
```

Steps:
1. Install base prompt in dir A, candidate prompt in dir B
2. Run `ateam report --roles {role} --ignore-previous-report` in both directories in parallel
3. Collect report output + cost/tokens from each directory's `state.sqlite`
4. Restore original prompts in both directories
5. Run judge, print comparison

The directories can be:
- Two git worktrees of the same repo (user creates them: `git worktree add ../eval-candidate`)
- Two separate clones
- Same repo at different commits
- Different projects entirely (cross-project eval)

Ateam doesn't create or manage the directories — the user controls their lifecycle. This keeps the implementation simple and covers all isolation scenarios without ateam needing worktree/clone logic.

**Mode 3: Git worktree — auto-isolation**

```bash
ateam eval --role security --prompt @candidate.md --git-worktree
ateam eval --role security --prompt @candidate.md --git-worktree --git-worktree-base /tmp/my-eval
```

Ateam creates two git worktrees automatically so you get the isolation benefits of Mode 2 with none of the manual setup.

Default worktree base: `/tmp/ateam-worktree/<flattened-abs-project-path>/`. Layout:

```
<base>/base/        ← git worktree at --detach HEAD + copied .ateam/
<base>/candidate/   ← git worktree at --detach HEAD + copied .ateam/
```

Steps:
1. Refuse if the source repo has uncommitted changes (`git status --porcelain` non-empty). Eval needs a well-defined commit.
2. Refuse if `--git-worktree-base` resolves to a path inside the source git repo (would nest repos).
3. For each side:
   - Remove any existing worktree at that path (detached worktrees are cheap to recreate).
   - `git worktree add --detach <path> HEAD`.
   - Copy the parent project's `.ateam/` into `<path>/.ateam/`, excluding state/log files: `state.sqlite*`, `logs/`, `roles/*/report.md`, `roles/*/history/`, `eval/`. Config, role prompts, and extra prompts are preserved.
4. Hand the two paths to the same parallel-run pipeline as Mode 2.
5. Leave worktrees in place after the run (inspect, diff, etc.). No auto-cleanup in phase 1.

Design decisions:
- **Detached HEAD** (not named branches) → sidesteps "branch exists / can't rebase" problems entirely.
- **Copy .ateam/ minus state** → consistent starting point, no prior report context skewing the eval.
- **Error on dirty tree** → reproducible eval tied to a specific commit.

Mode 2 (`--dirs`) remains for use cases that a worktree + config copy can't capture: Docker compose setups, database fixtures, env-specific config, cross-project eval.

### `--ignore-previous-report`

New flag for `ateam report`. When set, the prompt assembly skips injecting the previous report content. This ensures both eval runs start from the same baseline — neither is influenced by a prior report.

This flag is useful beyond eval (e.g., force a fresh analysis), but eval is the primary motivation.

### Core command

```bash
# Sequential: run in current directory
ateam eval --role security --prompt @candidate.md

# Parallel: run in two user-provided directories
ateam eval --role security --prompt @candidate.md --dirs . ../worktree-eval

# Parallel: ateam creates detached worktrees automatically
ateam eval --role security --prompt @candidate.md --git-worktree

# Override base prompt (default: current on-disk prompt)
ateam eval --role security --prompt @candidate.md --base @alternative_base.md

# Use a cheaper model for quick iteration
ateam eval --role security --prompt @candidate.md --model haiku

# Repeat for variance (run N times, average scores)
ateam eval --role security --prompt @candidate.md --repeat 3
```

### Cost comparison

Extracted from `state.sqlite` after each run. Printed as a side-by-side table:

```
                    Base           Candidate      Delta
Cost:               $0.81          $0.65          -20%
Input tokens:       352K           289K           -18%
Output tokens:      4.2K           3.8K           -10%
Cache read:         180K           155K           -14%
Duration:           4m41s          3m22s          -28%
```

### Judge

After both runs complete, a judge agent reads both reports and scores each. The judge is an LLM call (haiku by default for cost — ~$0.05) with a structured prompt:

```markdown
You are evaluating two reports produced by the same "{role}" analysis role
run on the same codebase. Report A used one prompt, Report B used another.

Score EACH report from 0.0 to 1.0 on these dimensions:
1. **Coverage** (0.0–1.0): Did it find real issues? Did it miss obvious ones?
2. **Accuracy** (0.0–1.0): Are findings correct? Any false positives?
3. **Actionability** (0.0–1.0): Are recommendations specific enough to implement?
4. **Conciseness** (0.0–1.0): Is it focused, or padded with generic advice?

Then provide:
- **Overall score** for each report (0.0–1.0)
- **Common findings**: issues found by both
- **Differences**: issues found by only one
- One paragraph summary of which is better and why

Report A:
{base report}

Report B:
{candidate report}
```

The judge output is parsed for the scores and printed alongside the cost comparison:

```
=== Eval: security ===

Cost:
                    Base           Candidate      Delta
Cost:               $0.81          $0.65          -20%
Input tokens:       352K           289K           -18%
Output tokens:      4.2K           3.8K           -10%
Duration:           4m41s          3m22s          -28%

Judge scores (0.0–1.0):
                    Base           Candidate
Coverage:           0.7            0.8
Accuracy:           0.8            0.9
Actionability:      0.6            0.7
Conciseness:        0.5            0.8
Overall:            0.65           0.80

Verdict: Candidate is better — similar coverage, fewer false positives,
more concise. 20% cheaper.
```

When `--repeat N` is used, scores are averaged across runs and standard deviation is shown to indicate variance.

### Implementation

| File | Change |
|------|--------|
| `cmd/eval.go` | New: `ateam eval` command — flag parsing, mode selection, orchestration |
| `internal/eval/run.go` | New: sequential and parallel run logic, prompt swap/restore |
| `internal/eval/judge.go` | New: judge prompt assembly, LLM call, score parsing |
| `internal/eval/compare.go` | New: side-by-side display formatting |
| `internal/eval/worktree.go` | New: detached worktree setup + selective `.ateam/` copy (Mode 3) |
| `internal/prompts/` | No change — `AssembleRolePrompt` already supports `skipPreviousReport` |

Cost/tokens/duration come directly from `runner.RunSummary` (returned by `Runner.Run`), so no separate metrics extraction from `state.sqlite` is needed for phase 1.

### Future extensions (not phase 1)

- `--base-role` / `--candidate-role` — compare different role names (migration, consolidation)
- Pipeline eval — run report+review+code, compare task selection and code changes
- Persistent eval history — store results for trend tracking
- Multi-project aggregation — run eval across N codebases, aggregate scores
- Finding overlap analysis — automated file-location matching without LLM
- `--repeat N` for variance across multiple runs

---

## Verification

**Dot-namespaced roles:**
- `ateam report --roles code.small,security` accepts both dotted and dotless roles
- `ateam report --roles security` still works (exact match, dotless role)
- Old names (`refactor_small`) continue to work as-is
- Config `.ateam/config.toml` with `"code.small" = "on"` (quoted key) loads correctly
- `go test ./...` passes (incl. `TestDotNamespacedRole`)

**Eval:**
- `ateam eval --role security --prompt @candidate.md` runs sequentially, prints cost + judge scores
- `ateam eval --role security --prompt @candidate.md --dirs . ../worktree` runs in parallel
- `ateam eval --role security --prompt @candidate.md --git-worktree` auto-creates detached worktrees
- `ateam eval --role security --prompt @candidate.md --model haiku` uses cheaper model
- Judge output includes 0.00–1.00 scores per dimension and overall
- Dirty tree error: `ateam eval ... --git-worktree` in a dirty repo exits with a clear message
- Nesting error: `ateam eval ... --git-worktree --git-worktree-base <inside-repo>` exits with a clear message
- `go test ./...` passes (incl. `TestParseJudgeOutput` and new worktree tests)

---

## Implementation Status

**Implemented (2026-04-17):**

Part 1 — Dot-namespaced roles:
- Verified end-to-end: discovery, config, validation, prompt assembly, flag parsing all handle dots transparently (no code changes required).
- Added `TestDotNamespacedRole` in `internal/prompts/prompts_test.go` exercising a `code.small` role.

Part 2 — Eval framework (Modes 1 and 2):
- `cmd/eval.go` with full flag layout: `--role`, `--prompt`, `--base`, `--dirs`, `--timeout`, `--verbose`, `--no-judge`, `--judge-timeout`.
- Shared agent flags: `--profile`, `--agent`, `--model` (apply to both sides by default).
- Per-side overrides: `--base-profile/--base-agent/--base-model`, `--candidate-profile/--candidate-agent/--candidate-model`.
- Judge agent: `--judge-profile/--judge-agent/--judge-model` (fall back to shared, then config).
- Mutual exclusion between profile/agent within each scope (same pattern as `ateam report`).
- `internal/eval/run.go`: sequential + parallel orchestration, prompt swap/restore with original backup.
- `internal/eval/judge.go`: structured judge prompt, regex score parser, handles missing scores gracefully.
- `internal/eval/compare.go`: side-by-side metrics table with percentage deltas.
- Previous-report context is always skipped for eval runs (via the existing `skipPreviousReport` argument to `AssembleRolePrompt` — no new flag needed).

**Implemented (2026-04-21):**

Part 2 — Mode 3: Git worktree auto-isolation:
- `internal/eval/worktree.go`: `SetupWorktrees` creates two detached worktrees, copies the parent's `.ateam/` minus state files (`state.sqlite*`, `logs/`, `roles/*/report.md`, `roles/*/history/`, `eval/`).
- Default base dir: `/tmp/ateam-worktree/<flattened-abs-project-path>/`.
- Validation: refuses if source repo has uncommitted changes; refuses if the base dir is inside the source repo (would nest repos).
- `cmd/eval.go`: `--git-worktree` bool + `--git-worktree-base DIR` string flags; mutually exclusive with `--dirs`.

**Implemented (2026-04-28):**

Part 2 — N-vs-M roles + optional review:
- `Variant` reshaped: `Roles []RoleRun` (per-side list of role + optional prompt override) replaces the old single `PromptText`.
- `RunResult` now holds per-role results (`Runs []RoleRunResult`), an optional `Review *RoleRunResult`, an aggregated `Summary` (sum cost/tokens/duration; max peak context), and the `Report` text passed to the judge (review output if present, else concatenated role reports with `## Role: <id>` headers).
- New flags on `ateam eval`:
    - `--base-roles` / `--candidate-roles` (StringSlice) — set per-side role lists. `--role X` is the shorthand: both sides default to `[X]`.
    - `--review` — also run supervisor review per side after the role reports; the judge then compares reviews instead of reports.
    - `--review-base-prompt` / `--review-candidate-prompt` — optional review prompt overrides (also imply `--review`).
- Validation: `--prompt` only allowed when there is exactly one candidate role; `--base` only with one base role.
- Reuses existing `prompts.AssembleReviewPrompt(..., customPrompt)` and `runner.Run` with `ActionReview`. When review will run, role reports are written to `env.RoleReportPath` (snapshot+restore the original to avoid polluting the project's reports in sequential mode).
- Judge prompt branches on report-vs-review and accepts a free-form subject label so multi-role and review evals get a sensible intro.
- Tests added in `internal/eval/run_test.go`: `TestRunEval_NvsM`, `TestFormatReportPicksReviewWhenPresent`, `TestAggregateSummarySumsAndKeepsPeak`.

Use cases unlocked:
```bash
# Compare 2 separate roles vs 1 consolidated role
ateam eval --base-roles code.small,code.module --candidate-roles code.consolidated --review

# Compare report+review pipeline end-to-end
ateam eval --base-roles A,B --candidate-roles C,D --review --git-worktree

# Test a new review prompt against the same set of reports
ateam eval --role security --review --review-candidate-prompt @new_review.md
```

**Deferred:**
- Glob expansion for `--roles` (e.g. `testing`, `testing.*` → all `testing.*` roles) — simple to add later via prefix match in `ResolveRoleList`.
- Persistent eval storage (`.ateam/eval/`).
- Worktree auto-cleanup after eval completes.
- Per-step cost breakdown in display (currently shows aggregated total).
- Per-side review profile/agent overrides (review currently uses the side's report runner).
- Multi-workspace aggregation, `--repeat N`, full pipeline eval (report+review+code).
