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

### The isolation problem

Reports are stateful — `ateam report` writes to `.ateam/roles/{role}/report.md`, overwrites the previous report, and the prompt assembly reads the previous report for context. You can't run base and candidate in the same `.ateam/` project without one corrupting the other.

### Approaches evaluated

**A: Sequential + `--ignore-previous-report`** — Run base first, save output, run candidate second, save output. Use `--ignore-previous-report` so the candidate isn't influenced by the base run's output.

- Pro: Simplest, no worktrees, no duplication
- Con: Can't parallelize; the second run happens after the first changed the codebase state (git, report.md); the "previous report" context differs between base (has the real previous) and candidate (has none or base's output)
- Verdict: OK for quick/cheap evals where timing differences don't matter. Not fair comparison.

**B: Git worktrees** — Create two worktrees from the same commit, copy `.ateam/` state to each, run independently.

- Pro: Full isolation, can run in parallel, same codebase state, each has its own `state.sqlite`
- Con: Worktrees duplicate the repo (~100MB+ for large repos), setup/teardown overhead
- Verdict: Cleanest isolation. Disk cost is temporary (cleaned up after eval).

**C: User-provided workspaces** — User sets up two separate project clones, eval runs in each.

- Pro: No automation needed, user controls everything
- Con: Manual setup per eval, user must ensure same commit, divergent `.ateam/` state
- Verdict: Good for cross-project eval (different codebases). Not practical for same-codebase prompt comparison.

**D: Snapshot `.ateam/` + redirect output** — Copy the `.ateam/` directory to a temp location per variant. Add `--project-dir` override to `ateam report` that uses the copy instead of the real `.ateam/`.

- Pro: No worktree (same codebase, different state dirs), lightweight
- Con: Needs new plumbing (`--project-dir` flag threading through the entire pipeline), agents still run in the same working directory (could interact)
- Verdict: Medium complexity. Possible but the plumbing touches many call sites.

**E: Make it configurable `--eval-mode worktree|eval-dirs|inplace` + depending on the mode `--worktree-dir BASEDIR --eval-dirs DIR1 DIR2`** — Offer multiple options to adapt to the main scenarios: simple reporting (then use worktree), complex setup required

- Pro: flexible
- Con: more work but ultimately it remains 2 separate directory, only steps before the run are different
- Pro: different eval (full report run, review or when comparing tasks vs. report based runs) will need some of that flexibility


### Recommendation: Worktrees by default, user-provided dirs when needed

Two workspace modes, same comparison logic:

**`--worktree` (default)**: Ateam creates ephemeral git worktrees. Best for quick prompt iteration on the current project — no setup needed.

```
/tmp/ateam-eval-{ts}/
├── base/           ← git worktree at same commit + .ateam/ copy + original prompt
└── candidate/      ← git worktree at same commit + .ateam/ copy + candidate prompt
```

Both worktrees share git objects (cheap). Each gets independent `.ateam/` with its own `state.sqlite`, `roles/`, and `logs/`.

**`--use-dirs DIR_BASE DIR_CANDIDATE`**: User provides pre-configured directories. Best for projects that need complex setup (Docker compose, database fixtures, env-specific config) that can't be replicated by copying `.ateam/`.

```bash
# User sets up two directories manually (or via a script)
ateam eval --role security --use-dirs ~/eval/base ~/eval/candidate --prompt @candidate.md
```

Ateam installs the candidate prompt in DIR_CANDIDATE, runs both, collects and compares. It does not create or clean up the directories — the user manages their lifecycle.

Both modes feed into the same comparison pipeline. The only difference is how the workspace directories are prepared.

### Core command: `ateam eval`

```bash
# Basic: compare two prompts for a role (auto-creates worktrees)
ateam eval --role security --prompt @candidate.md [--base @current.md] [--model haiku]

# With pre-configured directories (complex project setup)
ateam eval --role security --prompt @candidate.md --use-dirs ~/eval/base ~/eval/candidate

# Compare across multiple codebases
ateam eval --role security --prompt @candidate.md \
  --workspace ~/projects/webapp \
  --workspace ~/projects/api-service

# Compare different role names (migration, consolidation)
ateam eval --base-role security --candidate-role security.web --prompt @security.web.md

# Compare N roles vs 1 consolidated role
ateam eval --base-roles code.small,code.module,code.architecture \
  --candidate-role code.all --prompt @consolidated.md

# At a specific commit
ateam eval --role security --prompt @candidate.md --commit abc1234

# Repeat for variance
ateam eval --role security --prompt @candidate.md --repeat 3

# Full pipeline comparison (report + review + code)
ateam eval --pipeline --role security --prompt @candidate.md

# Review results
ateam eval results [--last | --id EVAL_ID]
ateam eval list
ateam eval judge --last
```

### Eval modes beyond single-role prompt comparison

The core case is "same role, two prompts." But several other comparison modes are useful:

**Mode 1: Different role names, same intent** — Compare `refactor_small` (old) vs `code.small` (new) during migration. Or compare a generic `security` role against a specialized `security.web` variant.

```bash
ateam eval --base-role security --candidate-role security.web --prompt @security.web_prompt.md
```

Implementation: `--base-role` and `--candidate-role` replace the single `--role`. When only `--role` is given, both sides use the same role name (current behavior). When they differ, the base worktree runs `--base-role` with its on-disk prompt, the candidate worktree runs `--candidate-role` with `--prompt`.

**Mode 2: N roles vs 1 role** — Test whether a single consolidated role (e.g., `code.all`) produces the same findings as running `code.small` + `code.module` + `code.architecture` separately.

```bash
ateam eval --base-roles code.small,code.module,code.architecture --candidate-role code.all --prompt @consolidated_prompt.md
```

The base side runs 3 roles (their reports are concatenated for comparison). The candidate side runs 1 role. Comparison: did the single role cover the same findings? At what cost delta?

This is important for collection design — merging roles saves cost but might lose coverage.

**Mode 3: Full pipeline comparison** — Compare report+review+code, not just reports. "Does the new prompt lead to better code changes?"

```bash
ateam eval --pipeline --role security --prompt @candidate.md
```

This runs `ateam report && ateam review && ateam code` in each worktree (or stops after review if `--stop-at review`). Comparison adds:
- Were the same tasks approved?
- Did coding succeed/fail differently?
- Were git diffs similar?

This is the most expensive eval but the most meaningful. Defer to v2 — start with report-only comparison.

### Extensibility: eval as a runner with pluggable comparison

To support these modes without a combinatorial explosion of flags, structure eval as:

```
ateam eval [MODE] [FLAGS]
```

Where MODE defaults to `report` (compare reports) but can be `review` (report+review) or `pipeline` (full cycle). Each mode defines:
- What commands to run in each worktree
- What artifacts to collect
- What comparison dimensions apply

```go
type EvalMode interface {
    Run(worktreeDir string, opts EvalOpts) error    // what to execute
    Collect(worktreeDir string) (*EvalResult, error) // what to gather
    Compare(base, candidate *EvalResult) *Comparison // how to compare
}
```

Start with `ReportEvalMode` (compares reports). Add `ReviewEvalMode` and `PipelineEvalMode` later. The worktree setup, parallel execution, and result storage are shared infrastructure.

### How single-workspace eval works

```
1. Record current state:
   - Current commit hash
   - Current .ateam/ contents (config, existing reports)
   - Base prompt (on-disk or --base)
   - Candidate prompt (--prompt)

2. Create two git worktrees at the same commit:
   git worktree add /tmp/ateam-eval-{ts}/base {commit} --detach
   git worktree add /tmp/ateam-eval-{ts}/candidate {commit} --detach

3. Copy .ateam/ into each worktree:
   cp -r .ateam/ /tmp/ateam-eval-{ts}/base/.ateam/
   cp -r .ateam/ /tmp/ateam-eval-{ts}/candidate/.ateam/
   
   In candidate worktree: overwrite the role prompt with candidate content
   
4. Run in parallel:
   (cd /tmp/ateam-eval-{ts}/base && ateam report --roles {role} --run-group eval-base-{ts}) &
   (cd /tmp/ateam-eval-{ts}/candidate && ateam report --roles {role} --run-group eval-cand-{ts}) &
   wait

5. Collect results:
   - Copy reports from each worktree's .ateam/roles/{role}/report.md
   - Extract metrics from each worktree's .ateam/state.sqlite
   - Store in .ateam/eval/{ts}/

6. Clean up worktrees:
   git worktree remove /tmp/ateam-eval-{ts}/base
   git worktree remove /tmp/ateam-eval-{ts}/candidate

7. Display comparison
```

### How multi-workspace eval works

For cross-project eval, the user provides existing workspaces. Each workspace gets the same worktree treatment internally:

```bash
ateam eval --role security --prompt @candidate.md \
  --workspace ~/projects/webapp \
  --workspace ~/projects/api
```

For each workspace:
1. `cd` to the workspace
2. Create two worktrees from the workspace's current commit
3. Run base + candidate in parallel
4. Collect results

All workspaces run in parallel too (up to `--parallel N` limit).

Results are aggregated across all workspaces in a single comparison table.

### Eval storage

```
.ateam/eval/
├── 2026-04-14_16-30-00/
│   ├── eval.json              # metadata: role, model, prompts, commit, workspaces
│   ├── base/
│   │   ├── report.md          # base prompt report output
│   │   └── metrics.json       # cost, tokens, duration, context peak
│   ├── candidate/
│   │   ├── report.md
│   │   └── metrics.json
│   ├── judge.md               # LLM quality comparison (if run)
│   └── comparison.md          # human-readable comparison
```

For multi-workspace:
```
.ateam/eval/2026-04-14_16-30-00/
├── eval.json
├── workspaces/
│   ├── webapp/
│   │   ├── base/{report.md, metrics.json}
│   │   └── candidate/{report.md, metrics.json}
│   └── api/
│       ├── base/...
│       └── candidate/...
├── judge.md
└── comparison.md              # aggregated
```

### Comparison dimensions

**1. Token cost** (from `state.sqlite`)

```
                    Base           Candidate      Delta
Cost:               $0.81          $0.65          -20%
Input tokens:       352K           289K           -18%
Output tokens:      4.2K           3.8K           -10%
Cache read:         180K           155K           -14%
```

**2. Context peak** (from `peak_context_tokens` in `state.sqlite`)

```
Peak context:       142K / 200K    98K / 200K     -31%
Context %:          71%            49%
```

High context (>80%) risks auto-compaction which degrades output quality. A lower peak means the prompt guides the agent more efficiently.

**3. Finding overlap** (automated, no LLM)

Match findings by file location (same file + nearby line numbers). Quick, cheap, catches obvious differences:

```
Finding overlap:
  ✓ docker.go:162 — Secret env vars (both, same severity)
  ✗ server.go:250 — CSP unsafe-inline (base only, LOW)
  + validate.go:45 — Missing input sanitization (candidate only, MEDIUM)
Summary: 4 shared, 1 base-only, 1 candidate-only
```

**4. Quality judgment** (LLM-based, via `ateam eval judge`)

Run haiku (~$0.05) to compare the two reports:

```markdown
You are comparing two reports from the same security analysis role
run on the same codebase at the same commit. Report A used the base
prompt, Report B used the candidate prompt.

Compare them on:
1. **Coverage**: Did one find issues the other missed?
2. **Accuracy**: Any false positives?
3. **Actionability**: Are recommendations specific enough to implement?
4. **Conciseness**: Any padding or generic advice?
5. **Overall**: Which is better and why?

Report A:
{base report}

Report B:
{candidate report}
```

For multi-workspace, the judge runs per workspace and produces an aggregate verdict.

### Comparison display

```
=== Eval: security (2026-04-14_16-30-00) ===
Model: haiku | Commit: abc1234

                    Base           Candidate      Delta
Cost:               $0.81          $0.65          -20%
Input tokens:       352K           289K           -18%
Output tokens:      4.2K           3.8K           -10%
Peak context:       142K (71%)     98K (49%)      -31%
Duration:           4m41s          3m22s          -28%
Findings:           6              6
  HIGH:             2              3              +1
  MEDIUM:           3              2              -1
  LOW:              1              1

Overlap: 5 shared, 1 base-only, 1 candidate-only
Judge: Candidate slightly better — same coverage, lower cost, better severity calibration
```

Multi-workspace:

```
=== Eval: security (across 3 workspaces) ===

Workspace          Base $    Cand $    Base ctx%   Cand ctx%   Findings
webapp             $0.81     $0.65     71%         49%         6 vs 6
api-service        $1.23     $0.98     82%         61%         9 vs 8
cli-tool           $0.45     $0.38     45%         33%         3 vs 2

Totals             $2.49     $2.01     -19%                    18 vs 16
Judge:             2 better, 1 worse → net improvement
```

### Workflow

```bash
# 1. Edit prompt
vim candidate_security.md

# 2. Quick single-project check (cheap)
ateam eval --role security --prompt @candidate_security.md --model haiku

# 3. Multi-project check
ateam eval --role security --prompt @candidate_security.md \
  --workspace ~/projects/webapp --workspace ~/projects/api --model haiku

# 4. Full eval with default model
ateam eval --role security --prompt @candidate_security.md \
  --workspace ~/projects/webapp --workspace ~/projects/api

# 5. Quality judgment
ateam eval judge --last

# 6. Deploy
cp candidate_security.md defaults/roles/security/report_prompt.md  # standalone role, no dot prefix
```

### Implementation

**Phase 1: Core eval (report-only, single role)**

| File | Change |
|------|--------|
| `cmd/eval.go` | New: `ateam eval` parent command |
| `cmd/eval_run.go` | New: `ateam eval run` (default subcommand) — workspace setup, parallel execution, comparison |
| `cmd/eval_results.go` | New: `ateam eval results`, `ateam eval list` — display stored evals |
| `cmd/eval_judge.go` | New: `ateam eval judge` — LLM quality comparison |
| `internal/eval/workspace.go` | New: worktree creation/teardown, `.ateam/` copy, prompt installation. Also `--use-dirs` mode (skip creation, validate existing dirs) |
| `internal/eval/metrics.go` | New: extract cost/tokens/context from `state.sqlite` |
| `internal/eval/findings.go` | New: parse findings from report markdown, location-based matching |
| `internal/eval/judge.go` | New: judge prompt assembly, haiku invocation, verdict parsing |
| `internal/eval/compare.go` | New: comparison display, delta formatting, aggregation |
| `internal/eval/storage.go` | New: eval.json read/write, eval directory management |

**Phase 2: Extended modes (later)**

| Feature | Effort |
|---------|--------|
| `--base-role` / `--candidate-role` (different role names) | Small — different role IDs in each worktree |
| `--base-roles` (N vs 1 comparison) | Medium — concatenate multiple reports for comparison |
| `--pipeline` (full report+review+code) | Large — run full pipeline in each worktree, compare tasks/commits |
| `EvalMode` interface for pluggable comparison | Medium — refactor phase 1 into the interface |

---

## Verification

**Dot-namespaced roles:**
- `ateam roles` lists all roles sorted alphabetically (dot-prefixed roles group naturally)
- `ateam report --roles code` expands to all `code.*` roles via prefix expansion
- `ateam report --roles code.*` same via explicit glob
- `ateam report --roles code.small` runs a specific role (exact match)
- `ateam report --roles security` still works (exact match, dotless role)
- Old names (`refactor_small`) continue to work as-is
- `go test ./...` passes

**Eval:**
- `ateam eval --role security --prompt @candidate.md --model haiku` runs both prompts
- Output shows cost/token/context/finding comparison
- `ateam eval --workspace A --workspace B` runs across projects and aggregates
- `ateam eval --commit abc1234` creates worktree, runs eval, cleans up
- `ateam eval judge --last` runs LLM comparison
- `ateam eval list` shows past evals
- `.ateam/eval/` directory has all artifacts
