# Summary

User-facing documentation is broadly in good shape: a clear `README.md` with overview, quick start, and pipeline diagram, plus dedicated reference files (`COMMANDS.md`, `CONFIG.md`, `ISOLATION.md`, `ROLES.md`, `FAQ.md`) that are linked back from the README. The main gaps for a first-time user are factual: a likely-wrong `git clone` URL in two `Quick Start` blocks, an `APPROACH.md` "stub" that the README advertises as the rationale doc, an unfinished `Tips and Tricks` section that ships as a TODO list, a Go-version mismatch (README says 1.25+, `go.mod` says 1.26.3), and a couple of small typos in the README. There are also smaller accuracy/structure issues: a likely-wrong Codex CLI URL, a `ROLES.md` table that mixes legacy and "new style" role names with no explanation, and the README's `Future` section is more roadmap-style content that does not belong on the landing page.

Audit performed by role `docs_external` using model **Claude Opus 4.7** (`claude-opus-4-7`), default thinking, read-only analysis (no files modified).

# Findings

## `git clone` URL in README does not match the repository's module path

- **Location**: `README.md:55`, `README.md:107`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: Both `Quick Start` and `Manual Install` instruct users to `git clone https://github.com/voidexpr/ateam.git`, but `go.mod` declares `module github.com/ateam` and the embedded help / binary identity all say `ateam`. There is no obvious `voidexpr` namespace referenced anywhere else in the project. If `voidexpr/ateam` does not exist (or is a fork), a user copy-pasting the Quick Start cannot install. This is the single most damaging doc error because it sits in the first thing a new user reads.
- **Recommendation**: Confirm the canonical public repo URL and replace both occurrences (Quick Start + Manual Install). If there is no public repo yet, replace with a placeholder that is obviously a placeholder (`https://github.com/<org>/ateam.git`) plus a one-liner explaining how to build from a local checkout.

## `APPROACH.md` is advertised as the design-rationale doc but is a 3-line stub

- **Location**: `APPROACH.md` (3 lines total); `README.md:13`, `README.md:374`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: README links to `APPROACH.md` twice — once near the top as "rationale and design principles" and again at the bottom under "More docs". The file itself says `_This document is a stub._ It will collect…`. A user clicking that link to understand whether ateam matches their needs hits an empty page. Either fill it (even briefly) or remove the links so the README's promises match reality.
- **Recommendation**: Either (a) remove both links to `APPROACH.md` and inline the existing "Why ATeam" / "Core principles" text more clearly in the README, or (b) move the existing "Why ATeam" / "Core principles" content out of `README.md` into `APPROACH.md` and have the README link to it. Pick one — the current state is the worst of both.

## README's `Tips and Tricks` section is an empty TODO list

- **Location**: `README.md:330-338`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The section header `## Tips and Tricks` is followed by `TODO:` and six unimplemented bullets ("separate agent config", "customize test hooks", "ad-hoc scripts: go to lunch, adversarial review, blackbox tester", "perform a task in a worktree", "multi-pass loop with budget / max rounds", "implement all the changes from a given report with an ad-hoc prompt"). Shipping a public TODO list in the README is a poor first impression and suggests the project is unfinished. Several of these topics (separate agent config, worktree, scripted parallel runs) are actually covered in `ISOLATION.md` and `FAQ.md`.
- **Recommendation**: Either remove the section entirely, or replace each TODO bullet with a one-line description plus a link to the relevant section in `ISOLATION.md` / `FAQ.md` / `COMMANDS.md` (e.g., "Separate agent config → see [ISOLATION.md#separate-config-directory-for-the-coding-agent]"). Do not leave the literal word `TODO:` in a user-facing landing page.

## Go version inconsistency across README, `go.mod`, and `install.sh`

- **Location**: `README.md:99` (`Go 1.25+`), `install.sh:6` (`REQUIRED_GO_VERSION="1.25"`), `go.mod:3` (`go 1.26.3`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: README and `install.sh` state Go 1.25+, but `go.mod` declares `go 1.26.3`. Users with Go 1.25 will pass `install.sh`'s version check, then hit a confusing `go build` error because the module requires a newer toolchain. The right number depends on whether `go.mod` is intentional or accidentally drifted.
- **Recommendation**: Decide the real minimum, then make all three sources agree. If `go 1.26.3` in `go.mod` is correct, update `README.md` to `Go 1.26+` and update `install.sh`'s `REQUIRED_GO_VERSION` and the `install_go` Linux tarball name. If 1.25 is the real minimum, lower `go.mod`.

## README typos in user-visible copy

- **Location**: `README.md:28` (`cost transaprency` → `transparency`), `README.md:78` (`ataem serve` → `ateam serve`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The first typo is in a bullet point under "Key Features". The second is inside a fenced code block that a user is likely to copy-paste; running `ataem serve` will fail with "command not found".
- **Recommendation**: Fix both. Consider running a one-shot `codespell` / `typos` pass on all top-level `*.md` files.

## README's `Future` section is roadmap content on the landing page

- **Location**: `README.md:344-366`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The README ends with a 20+-line bullet list of unreleased plans ("0.9.0 Refactor roles…", "Memory system: persist preferences…", "more agents: Pi, Gemini", etc.). This belongs in a `ROADMAP.md` or GitHub Issues/Milestones — on the README it (a) bloats the file, (b) dates poorly, and (c) creates confusion about what ateam does today vs. tomorrow.
- **Recommendation**: Move the section to a new `ROADMAP.md` (or onto the GitHub Issues/Projects board) and replace it in the README with a single line: "See ROADMAP.md for what's coming next."

## `ROLES.md` mixes legacy and "new style" role names with no explanation

- **Location**: `ROLES.md` (all rows); `README.md:269-281` (default-roles table)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ROLES.md` lists 40 roles where many appear in two forms: `critic_engineering` and `critic.engineering`, `database_schema` and `database.schema`, `docs_external` and `docs.external`, `testing_basic` (default ON) and `test.gaps/test.quality/test.recent` (defaults OFF), etc. Nothing in the doc explains whether `critic.engineering` is the successor to `critic_engineering`, whether one is deprecated, or which to choose for a new project. The README's default-roles table uses only the underscore forms (`docs_external`, `database_schema`) without mentioning that dotted alternatives exist.
- **Recommendation**: Add a one-paragraph preamble to `ROLES.md` that explains the naming convention (e.g., "Dotted names are the new finer-grained roles; underscore names are the original coarse roles and remain for compatibility — prefer dotted ones for new projects"), and mark the legacy names in the table (`(legacy)` column or dedicated section). Until the cleanup happens, users have no way to know which role to enable.

## Codex CLI link likely points to wrong URL

- **Location**: `README.md:100`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: README links Codex as `https://developers.openai.com/codex/cli`. Verify this URL — OpenAI's Codex CLI typically lives at `https://github.com/openai/codex` (or has moved). If the link 404s, first-time users have to guess where to install Codex from.
- **Recommendation**: Open the link, confirm it resolves to the install instructions for the Codex CLI that ateam actually shells out to, and update if needed.

## README repeats most of `ISOLATION.md` instead of linking to it

- **Location**: `README.md:191-238` ("Isolation" section), duplicated by `ISOLATION.md`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: The "Isolation" section in `README.md` runs ~45 lines and reproduces the same execution-modes diagram and table that already appear in `ISOLATION.md`. README also contains a `Why isolation matters` block whose content overlaps the equivalent in `ISOLATION.md`. The role guidance says to avoid letting README grow huge and to link out to detail docs instead — this section drifts in the opposite direction.
- **Recommendation**: Shrink the README's Isolation section to a 5-line intro + the 4-mode table + a single "see ISOLATION.md for setup details" link. Keep the diagram in only one place (either README or ISOLATION) so updates don't have to be made twice. Note: `ISOLATION.md:9` already links back to the README diagram, so consolidating the diagram in README and removing the duplicated table/details there is the cleaner direction.

## `ateam install` vs `install.sh` vs `ateam init` confusion not addressed up front

- **Location**: `README.md:52-66` (Quick Start), `COMMANDS.md:16-23` (`ateam install`), `install.sh`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: There are three install-shaped concepts: (1) `./install.sh` builds and symlinks the binary, (2) `ateam install [PATH]` creates the org `.ateamorg/` directory with default prompts, (3) `ateam init [PATH]` creates the project `.ateam/`. The Quick Start in `README.md` only walks through (1) + (3). New users following the Quick Start may wonder why `ateam install` (which `--help` shows prominently) is not part of the flow. The doc nowhere says "you typically do not need to call `ateam install` directly; `ateam init` will create the org for you on first run."
- **Recommendation**: Add a one-line note to the Quick Start (right after step 1) such as: "Note: `ateam init` auto-creates a `.ateamorg/` in `$HOME` on first run, so you usually do not need `ateam install` separately."

# Quick Wins

1. Fix the `git clone https://github.com/voidexpr/ateam.git` URL in `README.md:55` and `README.md:107` — broken install path for new users.
2. Fix typos: `ataem` → `ateam` (`README.md:78`) and `transaprency` → `transparency` (`README.md:28`). The `ataem` one sits inside a copy-paste code block.
3. Decide what to do with `APPROACH.md` — either flesh it out or stop linking to it from the README in two places.
4. Replace the `Tips and Tricks` TODO list with either real content or a removal — shipping `TODO:` in the README is a poor first impression.
5. Reconcile the Go-version mismatch between `README.md`, `install.sh`, and `go.mod` so users with Go 1.25 do not pass the installer's version check only to fail at `make build`.

# Project Context

**Type**: Go CLI tool (`ateam`) for orchestrating Claude Code / Codex coding agents against a project. Distributed as source; built via `make build` and installed via `install.sh` symlinking to `~/.local/bin/`.

**Current version**: `0.8.0` (from `/VERSION`). `go.mod` declares `go 1.26.3`; README/install.sh advertise `Go 1.25+`.

**Key user-facing doc files** (all at repo root, 3,010 lines total across `*.md`):
- `README.md` (380 lines) — overview, key features, quick start, pipeline diagram, isolation summary, roles table, command index, FAQ pointers, roadmap. The single doc most likely to be read first.
- `COMMANDS.md` (766 lines) — full command reference. Up to date with the binary's `--help` output (verified `ateam --help` lists ~36 commands; all appear in `COMMANDS.md` headings).
- `CONFIG.md` (498 lines) — directory layout for `.ateamorg/` and `.ateam/`, `config.toml` and `runtime.hcl` reference, prompt-resolution rules, sandbox-extra reference, effort levels per agent.
- `ISOLATION.md` (532 lines) — four execution modes (built-in sandbox, docker one-shot, docker exec, ateam-inside-docker), secret management, shared Linux agent config, precheck scripts, troubleshooting.
- `ROLES.md` (45 lines, auto-generated by `ateam roles --docs`) — table of 40 built-in roles, defaults, prompt link.
- `FAQ.md` (76 lines) — short, conversational; touches troubleshooting, ad-hoc workflows, codex/claude swap, project size.
- `APPROACH.md` (3 lines, stub).
- `DEV.md`, `EVAL.md`, `CONCURRENCY.md` — developer-facing, out of scope for this role.
- `AGENTS.md`, `CLAUDE.md` — assistant-facing instruction files, out of scope.

**Default-enabled roles user-facing surface** (8): `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `project_characteristics`, `refactor_small`, `security`, `testing_basic`.

**Diagrams**: README includes an ASCII pipeline diagram and four ASCII execution-mode diagrams (also `isolation_execution.png` + `isolation_execution.mmd` Mermaid source at the repo root).

**Cross-doc linking**: README links to all other top-level docs and to `COMMANDS.md` anchors for each command. Anchor format matches GitHub auto-IDs (e.g., `COMMANDS.md#ateam-report`).

**Tooling suggestion for automation**: A lightweight typo + dead-link check would catch the issues above. Recommended tools: `typos` (Rust) or `codespell` (Python) for spelling; `lychee` (Rust) for dead-link / 404 detection across `*.md` files. Both fit cleanly into a Makefile target (e.g., `make docs-check`) and CI, and would have flagged the `ataem` / `transaprency` / `voidexpr/ateam.git` issues automatically.

**No prior `docs_external` report** existed under `.ateam/runtime/`; the previous numbered runtime directories (1, 2, 5, 6) belong to other roles (refactor, testing, …).
