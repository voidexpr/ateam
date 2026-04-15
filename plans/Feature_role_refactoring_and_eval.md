# Feature: Role Collections and Prompt Eval Framework

## Part 1: Role Collections via `/` Namespacing

### Separator options evaluated

| Separator | CLI feel | Filesystem | TOML | Glob |
|-----------|---------|------------|------|------|
| `/` | `--roles code/architecture` | Nested dirs (works natively) | Needs quoted keys | `code/*` natural |
| `-` | `--roles code-architecture` | Flat (works) | Bare key OK | Ambiguous with names |
| `.` | `--roles code.architecture` | Flat (works) | **Breaks** (`.` = nesting in TOML) | Ambiguous |
| `:` | `--roles code:architecture` | Flat (works) | Needs quoted keys | `code:*` OK |
| frontmatter | `--roles code` (collection) | Flat (works) | No change | Needs metadata parsing |

**Recommendation: `/`**. It reads naturally (`--roles code/architecture`, `--roles testing/*`), maps to nested filesystem directories without any path hacks, and the TOML quoting is minor (`"code/architecture" = "on"`).

### How `/` works with the filesystem

`filepath.Join(projectDir, "roles", "code/architecture")` produces `.ateam/roles/code/architecture/` — nested directories. This is correct behavior, not a bug. The embedded defaults use the same layout:

```
defaults/roles/
├── code/
│   ├── small/report_prompt.md
│   ├── module/report_prompt.md
│   └── architecture/report_prompt.md
├── testing/
│   ├── basic/report_prompt.md
│   ├── full/report_prompt.md
│   └── flaky/report_prompt.md
├── docs/
│   ├── external/report_prompt.md
│   └── internal/report_prompt.md
└── security/
    └── report_prompt.md          ← single role, no sub-level
```

Role IDs become: `code/small`, `code/architecture`, `testing/basic`, `docs/external`, `security`. Single-level roles (no collection) still work unchanged.

### Changes needed

**1. Role discovery** (`internal/prompts/embed.go:55-68`)

Current `discoverRoleIDs()` reads one level with `fs.ReadDir("roles")`. Change to walk two levels:

```go
func discoverRoleIDs() []string {
    var ids []string
    entries, _ := fs.ReadDir(defaults.FS, "roles")
    for _, e := range entries {
        if !e.IsDir() { continue }
        // Check for report_prompt.md at this level (single role)
        if hasReportPrompt("roles/" + e.Name()) {
            ids = append(ids, e.Name())
            continue
        }
        // Check sub-level (collection)
        subEntries, _ := fs.ReadDir(defaults.FS, "roles/" + e.Name())
        for _, sub := range subEntries {
            if !sub.IsDir() { continue }
            id := e.Name() + "/" + sub.Name()
            if hasReportPrompt("roles/" + id) {
                ids = append(ids, id)
            }
        }
    }
    sort.Strings(ids)
    return ids
}
```

Same change for `scanRolesDir()` (lines 156-168) which scans project/org directories.

**2. Glob support in `--roles`** (`internal/prompts/embed.go` `ResolveRoleList`)

Add collection glob expansion:
- `code/*` → all enabled roles starting with `code/`
- `testing/*` → all enabled roles starting with `testing/`
- `all` → unchanged (all enabled)
- `code/small` → exact match (unchanged)

Implementation: in `ResolveRoleList`, if a role contains `*`, treat as prefix match against all known roles.

**3. Config.toml** — quoted keys for collection roles:

```toml
[roles]
"code/small" = "on"
"code/architecture" = "off"
"testing/basic" = "on"
security = "on"            # no collection, still works
```

No code change needed — Go's TOML parser handles quoted keys into `map[string]string`.

**4. `ateam roles` display** — group by collection:

```
Collection: code
  code/small           on   Concrete code improvements: naming, duplication, ...
  code/module          off  Module-level refactoring: interfaces, coupling, ...
  code/architecture    off  Architecture-level analysis: layers, patterns, ...

Collection: testing
  testing/basic        on   Ensures minimal high-value regression tests
  testing/full         off  Comprehensive test suite analysis

Standalone:
  security             on   Security vulnerability analysis
```

**5. Prompt frontmatter** — add optional `collection:` field:

```yaml
---
description: Concrete code improvements
collection: code
---
```

Informational for display/sorting, not for discovery. Discovery uses the filesystem path.

### Proposed collection mapping

| Collection | Roles | Description |
|------------|-------|-------------|
| `code` | small, module, architecture | Code review at increasing abstraction levels |
| `testing` | basic, full, flaky, ci | Test coverage, quality, CI health |
| `docs` | external, internal | Documentation quality |
| `security` | (standalone) | Security vulnerabilities |
| `dependencies` | (standalone) | Dependency health |
| `automation` | (standalone) | CI/CD, scripts, tooling |
| `feedback` | engineering, project, shortcuts | Critical reviews, run less often |
| `infra` | database_schema, database_config, production_ready | Infrastructure concerns |

Exact mapping TBD — the structure supports adding/renaming later.

### Migration

- Old flat names (`refactor_small`) continue to work via a compatibility map
- New names (`code/small`) are the canonical form
- `ateam update` rewrites config.toml to new names
- Both old and new names resolve to the same prompt file during transition
- After one release cycle, remove the compat map

### Files to modify

| File | Change |
|------|--------|
| `internal/prompts/embed.go` | Two-level discovery, glob expansion |
| `defaults/roles/` | Reorganize into collections |
| `defaults/config.toml` | Use new names with quoted keys |
| `cmd/roles.go` | Group display by collection |
| `internal/config/config.go` | No change (map[string]string handles `/` keys) |
| `internal/root/resolve.go` | No change (filepath.Join handles nested) |

---

## Part 2: Prompt Eval Framework

### The isolation problem

Reports are stateful — `ateam report` writes to `.ateam/roles/{role}/report.md`, overwrites the previous report, and the prompt assembly reads the previous report for context. You can't run base and candidate in the same `.ateam/` project without one corrupting the other.

Additionally, the `state.sqlite` database tracks runs, and concurrent writes from two eval variants would intermingle.

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

### Recommendation: Git worktrees (B) as the primary mechanism

`ateam eval` creates ephemeral worktrees, each with its own `.ateam/` state:

```
/tmp/ateam-eval-{ts}/
├── base/           ← git worktree at same commit + .ateam/ copy + original prompt
└── candidate/      ← git worktree at same commit + .ateam/ copy + candidate prompt
```

Both worktrees share the same git objects (git worktrees are cheap — they share `.git`). Each gets an independent `.ateam/` with its own `state.sqlite`, `roles/`, and `logs/`. Both can run in parallel since they're completely isolated.

### Core command: `ateam eval`

```bash
# Compare two prompts (creates worktrees automatically)
ateam eval --role security --prompt @candidate.md [--base @current.md] [--model haiku]

# Compare across multiple codebases (user provides pre-existing workspaces)
ateam eval --role security --prompt @candidate.md \
  --workspace ~/projects/webapp \
  --workspace ~/projects/api-service

# Compare at a specific commit
ateam eval --role security --prompt @candidate.md --commit abc1234

# Repeat for variance
ateam eval --role security --prompt @candidate.md --repeat 3

# Review results
ateam eval results [--last | --id EVAL_ID]
ateam eval list
ateam eval judge --last
```

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
cp candidate_security.md defaults/roles/security/report_prompt.md
```

### Implementation

| File | Change |
|------|--------|
| `cmd/eval.go` | New: `ateam eval` with subcommands (run, results, list, judge) |
| `internal/eval/worktree.go` | New: create/teardown worktrees, copy `.ateam/` state, install prompt |
| `internal/eval/metrics.go` | New: extract cost/tokens/context from `state.sqlite` |
| `internal/eval/findings.go` | New: parse findings from report markdown, location-based matching |
| `internal/eval/judge.go` | New: LLM quality comparison via haiku |
| `internal/eval/compare.go` | New: comparison display, aggregation across workspaces |

---

## Verification

**Collections:**
- `ateam roles` shows grouped output
- `ateam report --roles code/*` runs all code collection roles
- `ateam report --roles code/small` runs a specific role
- `ateam report --roles security` still works (no collection)
- Old names (`refactor_small`) resolve to new names (`code/small`) during migration
- `go test ./...` passes

**Eval:**
- `ateam eval --role security --prompt @candidate.md --model haiku` runs both prompts
- Output shows cost/token/context/finding comparison
- `ateam eval --workspace A --workspace B` runs across projects and aggregates
- `ateam eval --commit abc1234` creates worktree, runs eval, cleans up
- `ateam eval judge --last` runs LLM comparison
- `ateam eval list` shows past evals
- `.ateam/eval/` directory has all artifacts
