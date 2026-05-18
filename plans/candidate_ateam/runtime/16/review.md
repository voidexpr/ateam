# Supervisor Review

## Project Assessment

Three docs roles ran this cycle (`docs.external`, `docs.followable`, `docs.internal`) and converged on a consistent picture: the user-facing and engineer-facing documentation is broad, accurate in substance, and unusually rich for a project this young — but it has accumulated a handful of literal-reader failures (typos, broken example commands, version drift) and one or two structural drifts (a stub `APPROACH.md` linked as if substantive, a too-sparse `CLAUDE.md` next to very rich subsystem docs). All findings are doc edits; none are architectural. The fixes are individually small and several can be bundled into a single sweep.

## Priority Actions

### 1. Fix copy-paste-breaking errors in README / FAQ / EVAL examples (bundle)

- **Action**: Apply the following edits in one pass:
  - `README.md:78` — `ataem serve` → `ateam serve` (typo; first runnable block in Quick Start).
  - `FAQ.md:21` — rewrite the `ateam parallel` example to use **positional** prompts (verified in `cmd/parallel.go`: no `--prompt` flag exists). Example: `ateam parallel "take care of problem 1" "take care of problem 2" "take care of problem 3"`.
  - `FAQ.md:61` — `refactoring_small` → `refactor_small` (no `refactoring_small` role exists in `defaults/roles/`).
  - `EVAL.md:158-159` — the "Cheap iteration" example combines `--judge-model sonnet` with `--no-judge`, which contradict. Drop `--judge-model sonnet` (intent appears to be cost-comparison-only).
  - `README.md:23` — "a of **roles** covering…" — missing word; insert "set" (or rephrase).
  - `README.md:28` — `cost transaprency` → `cost transparency`.
- **Source Role**: docs.external (2026-05-14_13-13-28), docs.followable (2026-05-14_13-13-23)
- **Source Report**: .ateam/roles/docs.external/report.md, .ateam/roles/docs.followable/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Every item here is a literal command an agent or user will copy-paste verbatim and get an immediate error or unmatched role name. These are the highest-leverage doc fixes available — minutes of work, eliminates first-contact failure for new readers.

### 2. Fix Go version drift between `go.mod` and install/dev docs

- **Action**: `go.mod` pins `go 1.26.3`, but `DEV.md:10`, `install.sh:6` (`REQUIRED_GO_VERSION="1.25"`), and `README.md:99`/`README.md:106` still say "Go 1.25+". Update all three (plus any other "1.25" string in the install/prereq blocks) to "1.26+". Consider having `install.sh` derive the required version from `go.mod` to prevent recurrence.
- **Source Role**: docs.internal (2026-05-14_13-15-06)
- **Source Report**: .ateam/roles/docs.internal/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: A contributor who installs the documented 1.25 will hit a toolchain error on `make build`. The Makefile's `companion-race` target already uses `golang:1.26`, confirming 1.26 is the real floor.

### 3. Resolve the `APPROACH.md` stub vs README link

- **Action**: `APPROACH.md` is a 3-line stub but `README.md:13` and `README.md:374` link to it twice as the source for "rationale and design principles". Pick one path:
  - **Preferred**: move the README's "Why ATeam" + "Core principles" + "Get out of your way" sections into `APPROACH.md` so the link resolves to real content (also helps README size discipline — currently 380 lines).
  - **Alternative**: delete `APPROACH.md` and remove both README links until the file has content.
- **Source Role**: docs.external (2026-05-14_13-13-28), docs.internal (2026-05-14_13-15-06)
- **Source Report**: .ateam/roles/docs.external/report.md, .ateam/roles/docs.internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Both docs roles independently flagged this. A documented "rationale and design principles" link that lands on "This document is a stub" is the worst variant — readers neither get content nor know the topic was abandoned.

### 4. Remove or replace the literal `TODO:` block in README "Tips and Tricks"

- **Action**: `README.md:330-338` ships as a literal `TODO:` list ("separate agent config", "customize test hooks", "ad-hoc scripts: go to lunch, adversarial review, blackbox tester", "perform a task in a worktree", "multi-pass loop with budget / max rounds", "implement all the changes from a given report with an ad-hoc prompt"). Either delete the section, replace it with a one-line pointer to where the recipes will live, or lift the worktree / `--extra-prompt` / `parallel` examples that already exist in `FAQ.md` and the README "Workflow Examples" block to fill at least one or two of the bullets.
- **Source Role**: docs.external (2026-05-14_13-13-28)
- **Source Report**: .ateam/roles/docs.external/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: A `TODO:` list visible in the main README is a presence signal — readers see "shipping unfinished documentation" on the front page.

### 5. Tighten and expand `CLAUDE.md` to match the surrounding subsystem docs

- **Action**: Rewrite the agent-facing `CLAUDE.md` (and its `AGENTS.md` symlink) as a short, concrete checklist. Specifically:
  - Resolve the `./test_data/` reference (line 4) — the directory does not exist. Replace the line with concrete guidance: "Use `t.TempDir()` in Go tests and `mktemp -d -p \"$TMPDIR\"` in shell tests; do not write inside the repo root." (Alternative: create `test_data/` with a `.gitignore` and a one-line README, but the temp-dir recommendation matches existing test code in `test/cli/test-auth-combos.sh`.)
  - Replace the vague "or dependencies" trigger for `make test-docker` with the explicit path list CI uses: changes under `test/`, `Dockerfile*`, `internal/container/`, `internal/agent/`, `internal/runner/`, or `go.mod`/`go.sum`.
  - Add a one-line "before touching `internal/runner/`, `internal/container/`, or `internal/agent/`: read `CONCURRENCY.md`" pointer — that doc explicitly opens with the SIGSEGVs it prevents and is currently unlinked from `CLAUDE.md`.
  - Mention `make check` as the single pre-commit gate (runs test + fmt-check + check-tidy + check-docs + lint).
  - Mention `make docs` as the `ROLES.md` regenerator after editing role frontmatter.
  - Remove the duplicate "do NOT git commit without asking" line (currently appears twice in 14 lines).
- **Source Role**: docs.internal (2026-05-14_13-15-06)
- **Source Report**: .ateam/roles/docs.internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: `CLAUDE.md` is the file every coding agent reads first. The surrounding docs (`CONCURRENCY.md`, `DEV.md`) are excellent but invisible from there. Tightening this file is the highest-leverage agent-facing improvement on the table this cycle.

### 6. Document the temporary duplicate report-base prompt

- **Action**: `defaults/report_base_prompt.md` and `defaults/new_report_base_prompt.md` coexist as a temporary A/B for the legacy-underscore vs dotted-prefix role split. Today the only signposting is two TODO comments in `internal/prompts/embed.go` and an HTML comment in `new_report_base_prompt.md`. Add a short "Temporary docs / prompts" subsection to `DEV.md` (alongside the existing "Compatibility shims" table) describing: which roles read which base, why both exist, and the explicit removal trigger ("when no dotted-prefix role is still in pilot / once the v1 role set is final"). Cross-link the TODOs back to it.
- **Source Role**: docs.internal (2026-05-14_13-15-06)
- **Source Report**: .ateam/roles/docs.internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Institutional-memory-at-risk: a future agent or engineer who "cleans up" the apparent duplicate will silently break the A/B comparison or force rework. The mechanism for documenting transitional state already exists in `DEV.md` — extend it.

### 7. Expand the README Commands table

- **Action**: `README.md:297-326` omits `auto-setup`, `secret`, `cost`, `update`, `install`, and `eval` — all user-facing and several already referenced elsewhere in the same README (`auto-setup` in Quick Start, `cost` in "Key Configuration Concepts", `eval` has its own EVAL.md). Add one-line rows for each (depth lives in `COMMANDS.md`). The advanced commands (`agent-config`, `claude`, `container-cp`, `projects`, `project-rename`) can stay COMMANDS.md-only behind a single "Advanced" pointer.
- **Source Role**: docs.external (2026-05-14_13-13-28)
- **Source Report**: .ateam/roles/docs.external/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Discoverability fix — a user reading only the README won't know `secret` is the only entry point for OAuth/key management or that `update` is the way to refresh default prompts after upgrading.

### 8. Small README readability fixes (bundle)

- **Action**: A handful of small structural fixes:
  - Move the "Prerequisites" section (`README.md:97-101`) **above** Quick Start, or add a single warning line at the top of Quick Start: "Requires Go 1.26+ and an authenticated `claude` or `codex` CLI."
  - Align the install destination between `install.sh` (`~/.local/bin`) and "Manual Install" (`/usr/local/bin`). Standardize on `~/.local/bin` in both, or add one sentence calling out the choice.
  - Add a single sentence under Workflow Examples: "Roles listed in `--roles` run even if disabled in `config.toml`." (Mirrors the recent commit `2f81a30 review: --roles is authoritative everywhere`.)
  - Add one Quick Start line: "`make test    # quick unit smoke test; do NOT run 'make test-docker-live' — it costs real API credits`."
  - Fix `install.sh:86` — replace `./ateam --version` (no such flag) with `./ateam version | head -1` so the installer's success line is informative.
- **Source Role**: docs.followable (2026-05-14_13-13-23), docs.external (2026-05-14_13-13-28)
- **Source Report**: .ateam/roles/docs.followable/report.md, .ateam/roles/docs.external/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Each item individually is a nit, but bundled they materially improve the first-contact experience and are cheap to do in one pass.

## Deferred

- **README "Future" roadmap excision** (docs.external): moving the ~25-line roadmap from `README.md:344-366` into a new `ROADMAP.md` is reasonable for README-size discipline, but it's a stylistic call rather than a correctness fix and competes with the substantive README edits above. Revisit once the higher-priority README edits land.
- **Cross-doc duplication of role enumeration** (docs.external): the README "Enabled by default" table (`README.md:269-282`) and the generated `ROLES.md` show different (drifting) subsets because the legacy-underscore / dotted-prefix A/B is in flight. The right time to consolidate is when the A/B ends; doing it now creates churn and re-do work. Action #6 above documents the transitional state — when that ends, regenerate `ROLES.md`, drop or shrink the README table, and link to `ROLES.md` as the single source of truth.
- **Clone URL verification** (docs.external): `README.md:55,107` tells the user to clone `https://github.com/voidexpr/ateam.git` while `go.mod` declares `module github.com/ateam`. `docs.followable` independently confirmed the clone URL matches the actual origin remote, so no action — but the gap between Go module path and GitHub org is a known shape that recurs in audits and is worth noting.
- **Internal `STREAM.md` for the agent stream-event pipeline** (docs.internal, finding 6): the three-layer normalization (Claude/Codex JSONL → `agent.StreamEvent` → `runner.DisplayEvent` → renderer) deserves a focused subsystem doc like `CONCURRENCY.md`, but the layers all have package-level Go comments and no one is actively adding a third agent backend right now. Defer until the next backend (Gemini/Pi from the roadmap) lands — write the doc as part of that work.
- **`FAQ.md "How are agents executed"` profile note** (docs.external): adding a one-line `--profile` / ISOLATION.md pointer is a real improvement but a nit on its own; fold it in if/when the FAQ gets a broader pass.
- **"Steering Ateam" section incompleteness** (docs.external): valid but cosmetic — the section trails off without showing `--roles` syntax. Defer until a broader README pass.

## Conflicts

No contradictory recommendations between roles. `docs.external` and `docs.followable` independently flagged the `ataem serve` typo with the same severity and fix. `docs.external` and `docs.internal` independently flagged the `APPROACH.md` stub with compatible recommendations (external focused on the broken user-facing promise, internal focused on the broken contributor-facing promise; the proposed fix collapses both).

## Notes

- All three reports converge on the same shape of feedback: the project's docs are substantively strong (`DEV.md`, `CONCURRENCY.md`, `CONFIG.md`, `COMMANDS.md`, `ISOLATION.md`), and the gaps that remain are small, concrete, and bunched in three files: the README, `FAQ.md`, and `CLAUDE.md`. This is a healthier pattern than the inverse (good top-level marketing, hollow internals).
- `CONCURRENCY.md` is called out by `docs.internal` as exemplary — naming the production SIGSEGVs it prevents, framing itself as "read first if you're doing X". When `STREAM.md` (deferred) eventually gets written, that's the template.
- Two roles independently observed the legacy-underscore / dotted-prefix role-name migration as a source of doc drift. The fact that it shows up as drift in multiple places (README role table, FAQ examples, `ROLES.md`, duplicate base prompt) suggests the A/B finalization is the single highest-leverage internal cleanup queued behind this docs work — but it is itself feature/scope work, not a docs fix, and belongs in a future non-docs cycle.
- `docs.followable` ran in trace-mode only this cycle (no execute-mode sandbox). A future execute-mode run on a fresh Linux container would surface any hidden `~/.claude/` assumptions and `install.sh` macOS/Linux branching that trace mode cannot catch. Worth scheduling before the next release rather than now.
