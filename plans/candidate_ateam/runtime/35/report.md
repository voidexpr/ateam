# Summary

User-facing docs (README, COMMANDS.md, CONFIG.md, ISOLATION.md, ROLES.md, FAQ.md, EVAL.md) are broadly comprehensive and accurate in the parts that matter most. None of the issues flagged in the previous `docs.external` report (3 hours ago) have been addressed; the README is byte-identical and APPROACH.md is still the same 3-line stub. The actionable surface is unchanged: a published `TODO:` block in the README, a stub APPROACH.md the README sells as the rationale source, a typo'd `ataem serve` in Quick Start, a wrong role name in the FAQ (`refactoring_small`), a Commands table missing several public subcommands a user would realistically reach for (`auto-setup`, `cost`, `secret`, `update`, `install`, `eval`), plus duplication between the README role table and `ROLES.md`.

# Role performing the audit

- **Role**: `docs.external` (user-facing documentation)
- **Model**: Claude Opus 4.7 (`claude-opus-4-7`), no extended thinking enabled
- **Mode**: read-only analysis from `/Users/nicolas/SyncDatabox/nicmac/projects/ateam-candidate`

# Project maturity

ATeam is pre-1.0 (current README "Future" roadmap calls out `0.9.0` and `1.0.0` milestones). It has a public CLI surface but is still in active doc-shape churn (legacy underscore role names being replaced by dotted-prefix variants, A/B comparison ongoing). The user-facing-docs role can be aggressive about deletion/restructuring — there are no migration-compatibility constraints to honor here, just a need to keep what ships current.

# Findings

## TODO list left visible in README "Tips and Tricks"

- **Location**: `README.md:330-338`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: The "Tips and Tricks" section is published as a literal `TODO:` list of unwritten topics ("separate agent config", "customize test hooks", "ad-hoc scripts: go to lunch, adversarial review, blackbox tester", "perform a task in a worktree", "multi-pass loop with budget / max rounds", "implement all the changes from a given report with an ad-hoc prompt"). A reader scrolling the README sees the project shipping unfinished documentation, which is exactly the kind of presence signal `docs.external` exists to catch.
- **Recommendation**: Either remove the section entirely until content exists, replace it with a single line pointing at where these recipes will live, or write at least one or two short examples and delete the rest of the TODO bullets. The `--extra-prompt`, worktree and `parallel` examples already live in `FAQ.md` / README "Workflow Examples" and could be lifted/linked rather than re-promised.

## APPROACH.md is a stub but the README sells it as the rationale source

- **Location**: `APPROACH.md` (3 lines, currently `_This document is a stub._`); `README.md:13` and `README.md:374`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: The README links to APPROACH.md twice — once near the top ("See [APPROACH.md] for the rationale and design principles behind ATeam") and once in the "More docs" footer ("rationale and design principles"). A user following those links lands on a 3-line file that admits it has no content and bounces them back to the README. This is the standard "docs → code" drift inversion: the doc the README promises does not exist.
- **Recommendation**: Pick one path. Either (a) delete APPROACH.md and remove both README links until it is real, or (b) move the "Why ATeam" + "Core principles" + "Get out of your way" sections currently in the README into APPROACH.md and let the README's link finally resolve to actual content. Option (b) also helps README-size discipline (currently 380 lines, close to the soft cap).

## FAQ references a role name that does not exist

- **Location**: `FAQ.md:61`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Example reads `ateam review --extra-prompt "I only want tasks from refactoring_small and testing_basic"`. There is no `refactoring_small` role — `defaults/roles/` shows `refactor_small`, and `ateam roles --docs` confirms the same. A user copy-pasting this and adapting their own commands will write a directive the supervisor cannot match.
- **Recommendation**: Rename to `refactor_small` (`testing_basic` is correct as written).

## Broken example command in README Quick Start

- **Location**: `README.md:78`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The Quick Start comment reads `ataem serve              # local web server to browse documents, processes, cost`. `ataem` is a typo for `ateam`; the command as written will fail in a copy-paste-into-shell flow, which is exactly what Quick Start is for.
- **Recommendation**: Fix the typo.

## README Commands table is missing several public subcommands

- **Location**: `README.md:297-326`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Compared to the live `ateam --help` output, the README Commands table omits `auto-setup`, `secret`, `cost`, `update`, `install`, `eval`, `agent-config`, `claude`, `container-cp`, `projects`, `project-rename`. Several of these are user-facing and recurring: `auto-setup` is in the Quick Start steps; `cost` is name-dropped in "Key Configuration Concepts"; `secret` is the user's only entry point for OAuth/API key management; `update` is the only mechanism to refresh default prompts after upgrading ateam. The bullet list above the table mentions `init`, `install`, `update` but the table itself does not include `install` or `update`. `eval` has its own doc (EVAL.md) but is invisible from the README.
- **Recommendation**: Add at least `auto-setup`, `secret`, `cost`, `update`, `install`, and `eval` rows to the table (one line each is enough — `COMMANDS.md` carries the depth). `agent-config`, `claude`, `container-cp`, `projects`, `project-rename` are advanced and can either go into a sub-bullet or stay COMMANDS.md-only with a single "Advanced" pointer.

## Cross-doc duplication: roles enumerated in two places, drifting

- **Location**: `README.md:269-282` (the "Enabled by default" table) and `ROLES.md` (full table generated by `ateam roles --docs`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The README hand-curates an 8-role default table. `ROLES.md` is generated. The README table currently shows only the legacy underscore names (`docs_external`, `docs_internal`, `database_schema`, `refactor_small`, `testing_basic`, `project_characteristics`, `dependencies`, `security`) while `ROLES.md` lists both legacy underscore variants AND the new dotted variants (`docs.external`, `docs.internal`, `code.bugs`, `code.recent`, `code.structure`, `critic.engineering`, `critic.features`, `critic.project`, `database.schema`, `design.architecture`, `docs.followable`, `perf.benchmarks`, `perf.optimization`, `project.automation`, `project.dependencies`, `project.maintenance`, `project.production_ready`, `project.security`, `test.blackbox`, `test.gaps`, `test.quality`, `test.recent`). The README does not explain why two naming conventions coexist or which a user should pick. `defaults/report_base_prompt.md`'s top TODO comment confirms the duplication is a temporary A/B-comparison artifact, but a user reading just the README would not know this.
- **Recommendation**: Either (a) drop the README table entirely and link to ROLES.md as the single source of truth (recommended — the README table is already a stale subset), or (b) keep the README table but add one sentence explaining the dotted-prefix migration so the duplicate ROLES.md rows make sense. Long-term: once the A/B is over and the legacy underscore roles are removed, regenerate ROLES.md and remove duplicates.

## Inconsistent install destination between install.sh and "Manual Install"

- **Location**: `README.md:54-59` and `README.md:103-110`; `install.sh:7,99-101`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The auto-install (`./install.sh`) symlinks into `$HOME/.local/bin/`. The "Manual Install" block tells the reader to `sudo ln -s ... /usr/local/bin/ateam`. A user who does both, or who switches between machines, ends up with two different `ateam` symlinks in different precedence-order locations; `ateam update` and `make build` updates may then appear to "not stick" depending on which one their PATH resolves first.
- **Recommendation**: Standardize on `~/.local/bin/ateam` in both blocks (no sudo, no system path pollution), or call out the choice explicitly with one sentence.

## Repo URL in clone instructions may not match canonical path

- **Location**: `README.md:55`, `README.md:107`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: README tells the user to `git clone https://github.com/voidexpr/ateam.git`. `go.mod` declares `module github.com/ateam`. These are not necessarily wrong (GitHub user vs Go module path can legitimately differ), but the docs role cannot verify the URL resolves to a real repo from inside the working tree; if the canonical org has changed and the README was not updated, every first-time user's `git clone` fails.
- **Recommendation**: Confirm `https://github.com/voidexpr/ateam.git` resolves; if the canonical home is different (e.g. `github.com/ateam/ateam`), update both clone lines. If the README's URL is correct, no change.

## Sentence fragments and typos in "Key Features"

- **Location**: `README.md:23`, `README.md:28`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Line 23 reads "a of **roles** covering all core aspects of a project: ..." — a word is missing (probably "a set of"). Line 28 reads "**cost transaprency**" (typo for "transparency"). These are in the first screen a new user reads.
- **Recommendation**: Fix both.

## "Future" roadmap section is large and lives inside the README

- **Location**: `README.md:344-366`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The README dedicates roughly 25 lines to a detailed roadmap (0.9.0, 1.0.0, then "Focus on", "More flexible and generic core", "other", "more flexible workflow"). Roadmaps are not part of "what is this / how do I install it / how do I use it" — they are noise to a new user and a maintenance burden as plans shift. The README is already at 380 lines; this section is the easiest excision.
- **Recommendation**: Move to `ROADMAP.md` (new) and replace the section in the README with a one-line link. This is the same README-size-discipline lesson the role brief calls out.

## "Steering Ateam" section is incomplete

- **Location**: `README.md:174-189`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Section is structured as "1. Providing directions" / "2. Specify selected roles", but step 2 trails off after a list of when-to-run examples — it never actually shows the `--roles` flag syntax or links to it. A first-time reader who scrolls there expecting to learn how to pick roles gets motivation without mechanics.
- **Recommendation**: Either add a one-line `ateam all --roles security,refactor_small` example and a link to the Commands section, or merge this into the existing "Workflow Examples" block, which already demonstrates `--roles`.

## "How are agents executed" FAQ understates that this is configurable per-role

- **Location**: `FAQ.md:38-42`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The FAQ answer is correct for the default profile but doesn't mention that profiles are selectable via `--profile docker`, `--profile cheap`, etc. — a user who reads only the FAQ won't discover the runtime-profile concept until they hit COMMANDS.md or ISOLATION.md.
- **Recommendation**: Add a single sentence pointing at `--profile` and ISOLATION.md.

# Quick Wins

1. Remove or replace the literal `TODO:` list in `README.md:330-338` ("Tips and Tricks").
2. Fix `ataem serve` → `ateam serve` at `README.md:78`.
3. Fix `refactoring_small` → `refactor_small` at `FAQ.md:61`.
4. Resolve APPROACH.md: either delete it and unlink, or move the README rationale into it. Currently the link is a dead-end for new readers.
5. Add `auto-setup`, `secret`, `cost`, `update`, `install`, `eval` rows to the README Commands table.

# Project Context

- **Project**: ATeam — a Go CLI (`module github.com/ateam`) that runs role-specific coding agents against a codebase. User-facing docs are top-level Markdown files plus `defaults/roles/<role>/report_prompt.md` for per-role prompts.
- **User-facing doc surface** (this role's scope):
  - `README.md` (380 lines) — main entry; mixes overview, quick start, isolation, roles, commands, future roadmap.
  - `COMMANDS.md` (766 lines) — full CLI reference, organized by subcommand; appears current and aligned with `ateam --help`.
  - `CONFIG.md` (498 lines) — directory layout, `config.toml`, `runtime.hcl`, agents/profiles/templates.
  - `ISOLATION.md` (532 lines) — sandbox/docker modes, secrets, auth.
  - `ROLES.md` (45 lines) — generated by `ateam roles --docs`; lists 39 roles (legacy underscore + new dotted prefix coexist as the project A/B-migrates naming).
  - `FAQ.md` (76 lines) — short Q&A.
  - `EVAL.md` (160 lines) — prompt evaluation workflow.
  - `APPROACH.md` (3 lines) — stub.
  - `install.sh` — installer; installs Go if missing, runs `make build`, symlinks into `~/.local/bin`.
- **Source of truth for CLI surface**: `cmd/` directory (Cobra subcommands) and `defaults/runtime.hcl` (profiles). `ateam --help`, `ateam roles --docs`, and `ateam env` reproduce most of what the docs say. Live `ateam --help` (verified this run) lists 33 subcommands; the README Commands table currently lists 19.
- **Migration in flight**: legacy underscore role names (`docs_external`, `testing_basic`, `refactor_small`, etc.) are being replaced by dotted-prefix variants (`docs.external`, `test.gaps`, `code.bugs`, etc.). Both sets exist on disk under `defaults/roles/`. `defaults/report_base_prompt.md` carries a TODO marker that flags the duplication as temporary A/B. The README still uses only the legacy names; ROLES.md lists both.
- **Recent doc commits** (last few): `roles --docs` markdown-table escaping fixes, `code.structure` scope changes, prompts cleanup, review-roles authority — all internal-doc / prompt churn, not user-facing rewrites. No commits in the last 3 hours touched the user-facing surface, which is why every previous-cycle finding carries forward unchanged.
- **Prior reports**: one prior `docs.external` report from 3 hours ago (2026-05-14_13-13-28). All findings remain unaddressed and have been carried forward.
