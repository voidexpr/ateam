# Eval — Compare Prompts, Roles, and Reviews

`ateam eval` runs a role twice (base vs candidate) against the same codebase
and produces a side-by-side comparison: cost, tokens, duration, plus an LLM
judge that scores each side 0.00–1.00 on coverage, accuracy, actionability,
and conciseness.

It can also run **multiple roles per side** and an **optional supervisor
review** so you can compare consolidations and review prompts.

## Quick start

```bash
# Compare a candidate prompt against the current on-disk prompt
ateam eval --role security --prompt @candidate.md
```

The previous-report context is always skipped, so both runs start fresh.

## Three isolation modes

| Mode | Flag | What ateam does | When |
|------|------|-----------------|------|
| Sequential | (default) | Run base, then candidate, in the current project dir | Quick local iteration |
| User dirs | `--dirs DIR1,DIR2` | Run each side in a pre-set-up project dir, in parallel | Custom setup (Docker, fixtures) |
| Git worktree | `--git-worktree` | Auto-create two detached worktrees under `/tmp/ateam-worktree/<project>/`, copy `.ateam/` minus state, run in parallel | Most parallel evals |

`--git-worktree` errors if the source repo has uncommitted changes (eval
needs a well-defined commit) or if `--git-worktree-base` is inside the
source git repo (would nest repos).

## What's compared

By default, **role reports**. With `--review`, the supervisor runs after the
reports on each side and the judge compares the **reviews** instead.

| Goal | Flags |
|------|-------|
| Two prompts for one role | `--role X --prompt @new.md` |
| Two prompts with a reference | `--role X --prompt @new.md --base @v1.md` |
| Different roles, same intent | `--base-roles A --candidate-roles B` |
| N roles vs M (consolidation) | `--base-roles A,B --candidate-roles C` |
| Compare reviews end-to-end | `--role X --review` |
| Test a new review prompt | `--role X --review --review-candidate-prompt @new.md` |
| All of the above in one shot | `--base-roles A,B --candidate-roles C,D --review --review-candidate-prompt @v2.md --git-worktree` |

`--prompt` / `--base` are only valid when the matching side has exactly one
role (otherwise ambiguous). `--review-base-prompt` and
`--review-candidate-prompt` work regardless of role count and imply
`--review`.

## Picking the agent

Three scopes, with fallback:

```
--profile / --agent / --model           shared by both sides (and the judge if no judge-* set)
  ↓ overridden by
--base-profile / --base-agent / --base-model           base side only
--candidate-profile / --candidate-agent / --candidate-model    candidate side only
--judge-profile / --judge-agent / --judge-model        judge only
```

Within a single scope, `--profile` and `--agent` are mutually exclusive
(same as `ateam report` / `ateam run`).

For most prompt-comparison evals you don't need any of these — the default
config picks the right agent and model for both sides.

```bash
# Cheap iteration: haiku for both sides, sonnet for the judge
ateam eval --role security --prompt @c.md --model haiku --judge-model sonnet

# Compare base claude vs candidate codex
ateam eval --role security --prompt @c.md --base-agent claude --candidate-agent codex
```

## What's printed

```
=== Eval: report security ===

Cost & metrics:
                Base          Candidate     Delta
  Cost:         $0.81         $0.65         -20%
  Input tokens: 352K          289K          -18%
  Output tokens: 4.2K         3.8K          -10%
  Cache read:   180K          155K          -14%
  Duration:     4m41s         3m22s         -28%
  Turns:        12            10
  Peak context: 142K          98K           -31%

Judge scores (0.00-1.00):
                Base          Candidate
  Coverage:     0.70          0.80
  Accuracy:     0.80          0.90
  Actionability: 0.60         0.70
  Conciseness:  0.50          0.80
  Overall:      0.65          0.80

Verdict: Candidate is better — same coverage, fewer false positives, more concise.
```

For multi-role sides, cost/tokens are summed across roles; peak context is
the max. The judge sees the concatenated reports (or the review, if
`--review`).

## Common knobs

| Flag | What |
|------|------|
| `--no-judge` | Skip the LLM judge; print cost comparison only |
| `--timeout N` | Per-run timeout in minutes (0 = config default) |
| `--judge-timeout N` | Judge timeout (default 10) |
| `--verbose` | Print agent and container commands |
| `--force` | Run even if the same role+action is already in flight |

## Where artifacts live

- Sequential mode: each role's output is archived under
  `.ateam/roles/<id>/history/<ts>_eval_<side>.report.md`. Review output goes
  to `.ateam/logs/supervisor/<ts>_eval_<side>.review.md`. The project's
  `report.md` is preserved (snapshotted/restored when `--review` is used).
- `--dirs` / `--git-worktree`: each side has its own `.ateam/`, so artifacts
  stay in their respective dirs after the eval. The worktrees are left in
  place for inspection — clean up manually with `git worktree remove`.

## Caveats

- **Roles within a side run sequentially**: they share `.ateam/` and prompt
  install/restore must serialize. The two sides still run in parallel under
  `--dirs` / `--git-worktree`.
- **Cost is summed per side**, not broken down per role or per step
  (reports vs review).
- **Judge model defaults to whatever the shared/default agent uses.** Pass
  `--judge-model haiku` for cheap quick passes.
- **Same agent per side**: the side's runner is reused for all its roles
  (and its review). Different agents per role within a side aren't supported
  in v1.
- **No persistent eval history**: results print to stdout only.

## Examples

```bash
# Smoke-test a new prompt
ateam eval --role security --prompt @candidate.md --git-worktree

# Does consolidating two roles lose findings?
ateam eval --base-roles code.small,code.module \
           --candidate-roles code.consolidated \
           --review --git-worktree

# Tune a review prompt against the same set of reports
ateam eval --role security --review \
           --review-candidate-prompt @new_review.md

# Cheap iteration: small model both sides, bigger model for the judge
ateam eval --role security --prompt @c.md \
           --model haiku --judge-model sonnet --no-judge
```
