---
description: Recommend which roles to run report for at this point in time
---
# Recommend ateam roles

Ateam runs role-specific agents that audit this codebase, then a supervisor reviews their findings and a coder implements selected fixes. Given the current state, recent code changes, and prior runs, recommend which roles to run this round and at what depth.

## Inspect (do this first)

Run these commands and read their output before deciding. The git-diff step is the primary input.

- `ateam roles` — current per-role status (`on` / `off` in `config.toml`), plus `legacy` / `deprecated` flags.
- `ls -lt .ateam/roles/*/report.md` — age of each role's last report.
- `cat .ateam/supervisor/review.md` — last review. It cites the commit it ran on and which findings were selected vs. deferred or rejected.
- Latest code cycle: `find .ateam -type f -name execution_report.md | xargs ls -lt | head -1` then read the file. It records which fixes were applied.
- **Changes since the last review's commit**: `git log <commit>..HEAD --stat` and `git diff <commit>..HEAD --stat`. Group changes by area (database, scripts, docs, tests, source).

For a role whose purpose isn't obvious from `ateam roles`, read its prompt with `ateam prompt --action report --role NAME`. Prompts can be long — only do this for unfamiliar custom roles.

## Decide

Apply in order, stop at the first match:

1. **No code changes since the last review** → recommend `ateam review` alone (or nothing if the existing review is still current). Regenerating reports against unchanged code wastes tokens.
2. **Recent changes touched a role's territory** → run that role. Examples:
 - SQL / migration / schema → `database.schema`
 - Build / CI / lint / hooks → `project.automation`
 - New code, new branches, missing tests → `test.gaps`, `test.recent`
 - Dependency bumps → `project.dependencies`
 - Auth / secrets / input handling → `project.security`
3. **Role's territory was untouched** → skip it, even if `on` by default. The prior report is still accurate.
4. **Prior review rejected or deferred every finding from a role AND that role's territory hasn't changed since** → skip.
5. **A disabled role's territory was heavily touched** → recommend it anyway. `--roles NAME` runs the role regardless of `config.toml` status.

Bias toward short role lists. Two well-chosen roles produce a better review than ten that mostly repeat last cycle.

## Pick depth

- `ateam report --roles X,Y` — fresh reports only; lets you inspect before reviewing.
- `ateam report --roles X,Y --review` — reports + review in one go.
- `ateam all --roles X,Y` — report → review → code → verify, end-to-end.

## Output

1. **Rationale** (2–4 lines): which roles you picked or skipped and why, noting each role's current status (`on` / `off` / not listed).
2. **One recommended command** on its own line, exact. Optionally print the two other depth variants for the same role list as alternatives the human can pick instead.

Do not execute the commands. Print and stop.
