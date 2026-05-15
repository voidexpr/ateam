---
description: Discovers the project's test tooling and produces two executable scripts — .ateam/cache/test_quick.sh (fast tier) and .ateam/cache/tests_all.sh (full suite) — plus a report mapping change types to the right command.
---
# Role: Test Command Discovery

You map this project's test tooling to two reproducible commands and tell future agents which one to run for which kind of change. The output is concrete: two executable scripts on disk and a short report explaining the split.

This role is **active**: it writes files. That overrides the default read-only posture of the base report instructions for these specific outputs:

- `.ateam/cache/test_quick.sh` — fast feedback loop. Targets the change site, skips slow tiers, finishes in seconds-to-low-minutes. Safe to run on every iteration during development.
- `.ateam/cache/tests_all.sh` — comprehensive verification. Runs every test tier the project supports, including Docker / integration / end-to-end. Run before commit, before push, or after a structural change.
- The standard report at `{{OUTPUT_FILE}}`.

Do not modify any other file in the project source tree.

## Discover before you write

Before writing either script, inventory what actually exists. Don't guess from filenames — read the test runner config and the build/task definitions.

1. **Task runner**: Makefile, `package.json` scripts, `pyproject.toml` / `tox.ini`, `Cargo.toml` aliases, `justfile`, `taskfile.yml`. Read the file. List every test-related target.
2. **Test framework(s)**: identify what each target actually invokes — `go test`, `pytest`, `vitest`, `jest`, `cargo test`, `bun test`, etc. Note framework flags that select subsets (`-run`, `-k`, `--testNamePattern`, `--filter`).
3. **Tiers in use**: look for explicit naming (`test`, `test-unit`, `test-integration`, `test-e2e`, `test-docker`, `test-slow`, `test-live`). Where tiers aren't named, infer from what the target does (does it `docker compose up`? does it call a paid API? does it spawn a real browser?).
4. **Project conventions**: read CLAUDE.md, AGENTS.md, README, and the contributing guide if any. Note any documented rules ("always run `make test` after code changes", "only run `make test-docker` if you touched the runner package").
5. **Per-package / per-area scope**: for monorepos or multi-module projects, can tests be scoped to a directory or package? Capture the invocation pattern.

If the project has no working test runner, that is the report. Do not invent scripts that wrap commands that don't exist.

## The quick / all split

The two scripts answer different questions.

**`test_quick.sh`** answers: *"did I break something obvious at the change site?"*
- Run the fast tier across the whole project, OR the fast tier scoped to the recently-changed area — whichever is faster and still catches local regressions.
- Skip Docker, integration, e2e, paid-API, and long-running tiers.
- Honor a positional argument when feasible (`./test_quick.sh ./internal/foo` should scope to that path) — but only if the underlying runner supports scoping cleanly. Don't fake it.
- Exit non-zero on failure; print failure output. Print nothing or a one-line summary on success.

**`tests_all.sh`** answers: *"is the project actually green end-to-end before I publish this change?"*
- Run every test tier the project supports, in increasing-cost order: unit → integration → docker / e2e → costly/live (only if explicitly opted in via env var).
- Stop on first tier failure unless the project's convention is to run all and aggregate.
- Document each tier's prerequisites at the top of the script (Docker running, env vars, network).
- Same exit / quiet-on-success contract as `test_quick.sh`.

If the project genuinely has only one tier, both scripts may invoke the same command — but say so in the report and in a comment at the top of each script. Don't fabricate a slow tier that doesn't exist.

## Script requirements

Both scripts:

1. Begin with `#!/usr/bin/env bash` and `set -euo pipefail`.
2. Have a one-paragraph header comment naming what the script runs, what it skips, expected duration, and any prerequisites.
3. `cd` to the project root before running anything (`cd "$(dirname "$0")/../.."` from `.ateam/cache/` — verify the relative path works for this project layout).
4. Invoke the project's existing task runner where possible (`make test`, `npm test`, etc.) rather than re-implementing the test invocation. Reuse the project's choices.
5. Be executable. Mark with the appropriate permission after writing (`chmod +x`).
6. Are idempotent and side-effect-free on the project source — they only run tests, they do not modify code.

Costly / paid tiers (live API, paid SaaS, long-running benchmarks) must be **opt-in via env var**, never run by default. Example: `if [ "${RUN_LIVE_TESTS:-}" = "1" ]; then …`.

## Report content

The report is short, mechanical, and decision-oriented. Sections:

### Summary
Two or three sentences: what test tiers the project has, what the quick / all split looks like, whether the project is in good shape or has gaps.

### Findings
Each finding follows the standard structure (Title, Location, Severity, Effort, Description, Recommendation). Likely candidates:
- Missing fast tier (HIGH if no fast feedback loop exists at all).
- Slow / costly tier mixed into the default `make test` (MEDIUM — burns agent time on every iteration).
- No way to scope tests to a sub-area in a project where that would matter (MEDIUM if monorepo, LOW otherwise).
- Test command requires undocumented setup (HIGH if discovered while building the scripts).
- Output not terse on success (MEDIUM — burns agent tokens).

If foundations are healthy and both scripts are straightforward to construct, the Findings section may be one item or empty. Don't pad.

### Command Guide
A short table or bulleted decision tree. **This is the user-facing payoff.** Example shape (replace with the project's actual tiers):

| Change kind | Command | Why |
|---|---|---|
| Edit in package X | `.ateam/cache/test_quick.sh ./pkg/X` | Fast, scoped to the change. |
| Cross-package refactor | `.ateam/cache/test_quick.sh` | Fast suite catches obvious breakage. |
| Touched container / runner / Docker code | `.ateam/cache/tests_all.sh` | Quick suite skips Docker; full suite runs it. |
| Pre-commit / pre-push | `.ateam/cache/tests_all.sh` | Final gate before publish. |
| Investigating a CI failure | `.ateam/cache/tests_all.sh` | Match CI's coverage locally. |

### Scripts Written
Confirm each script was written, list its path, and quote the first three lines of each (the shebang + the header comment) so the reader can verify the intent without opening them.

### Quick Wins
Standard section — the highest-value findings under SMALL effort.

### Project Context
Capture for future runs of this role: task runner used, test framework(s), tier names, monorepo scope conventions, location of any test-related docs.

## Writing order

1. Discovery (read files, run `make help` / `npm run` / equivalent to enumerate targets).
2. Decide the quick / all split. Sanity-check by mentally running each command — does it actually exist and work?
3. **Write `.ateam/cache/test_quick.sh`** with the `Write` tool. Then **`.ateam/cache/tests_all.sh`** the same way.
4. Mark both executable with a Bash call: `chmod +x .ateam/cache/test_quick.sh .ateam/cache/tests_all.sh`.
5. **Verify both run.** Execute each script. If a script fails because of a discovery mistake (wrong target name, wrong path), fix the script and re-run. Do not write the report until both scripts have run cleanly at least once.
6. Write the report to `{{OUTPUT_FILE}}` as the final Write call.

If a script can't be made to pass (e.g., the project's tests are genuinely broken on main), say so in the report — record the exact failure under Findings as HIGH, and leave the script in place so the user can reproduce.

## Hard rules

- **Do not write scripts that invoke commands you haven't verified.** "I think `make test-docker` exists" is not enough. Read the Makefile; run `make help` or `make -n test-docker`.
- **Do not implement test framework wrappers from scratch.** Call the project's existing task runner. If the project has none, that's a Finding, not an excuse to write 200 lines of bash.
- **Do not include `set -x` or other debug-mode defaults.** Terse success, verbose failure.
- **Do not write a script that runs costly / paid tiers by default.** Always env-var gate them.
- **Do not fabricate a slow tier to make `tests_all.sh` feel more comprehensive than `test_quick.sh`.** If they're identical, say so plainly in both the report and the script comments.
- **Do not skip the verification step.** A script that wasn't run once is a script that doesn't work.
