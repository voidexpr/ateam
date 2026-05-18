# Summary

The developer-facing documentation set is unusually strong for a project of this size: README, DEV, CONFIG, COMMANDS, ISOLATION, CONCURRENCY, EVAL, FAQ, and ROLES are all populated and cross-linked, every `internal/` package has a `// Package …` doc comment, and concurrency-sensitive code (`runner.RunPool`, `agent.Agent`, `internal/root/migrate_logs.go`) carries explicit contracts. The main gaps are doc-vs-reality drift (Go version), a stub document still cross-linked as canonical (`APPROACH.md`), an outdated implementation plan kept inline with no "historical" marker, and a small amount of inline doc duplication (`AGENTS.md` vs `CLAUDE.md`).

Audit performed by role `docs.internal` (also tagged `docs_internal`) running on model `claude-opus-4-7` (no extended thinking).

# Findings

## 1. Go version drift between `go.mod` and the docs

- **Location**: `go.mod:3` (`go 1.26.3`); `DEV.md:11` ("Requires Go 1.25+."); `README.md:99` ("Go 1.25+ — installed automatically by `install.sh`"); `README.md:106` ("`go version          # ensure Go 1.25+`")
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The module declares `go 1.26.3` but every developer-facing doc still says `Go 1.25+`. A developer who reads the README, satisfies the stated requirement with 1.25, and tries to build will get a confusing toolchain error (or an auto-toolchain download, depending on env). The `Makefile`'s `companion-race` target even pins `golang:1.26` in the docker invocation, confirming the real floor is 1.26.x.
- **Recommendation**: Update the three doc lines to `Go 1.26+` (and verify whether the floor is actually `1.26.3` and should be quoted exactly, or whether `go.mod` should be relaxed to `1.26`).

## 2. `APPROACH.md` is a stub but is cross-linked as canonical

- **Location**: `APPROACH.md` (3 lines, body: "_This document is a stub._"); inbound links from `README.md:9` ("See [APPROACH.md] for the rationale and design principles…"), `README.md:373` ("More docs" table), and indirectly from `ROLES.md` discussion.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The README routes readers to `APPROACH.md` as the place for rationale and design principles, but the file is a one-line stub that delegates back to README. A new developer following the breadcrumbs hits a dead end. The "Why ATeam", "Core principles", and "How It Works" prose already exist inside `README.md` and could be relocated/condensed into `APPROACH.md`.
- **Recommendation**: Either (a) populate `APPROACH.md` by moving the "Why ATeam" + "Core principles" prose out of `README.md` into it, leaving the README with a one-paragraph summary + link, or (b) delete `APPROACH.md` and remove the inbound links. Avoid keeping a stub that is referenced as authoritative.

## 3. Historical implementation plans live in `docs/plans/` with no "completed/archived" marker

- **Location**: `docs/plans/2026-03-07-org-project-split-design.md`, `docs/plans/2026-03-07-org-project-split-plan.md`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Both files describe the `.ateamorg/`+`.ateam/` split as a future task ("Goal: Split `.ateam` into `.ateamorg` …", "POC: no migration from old `.ateam` format"). The split is fully implemented (visible in `internal/config/config.go`, `internal/root/init.go`, `cmd/install.go`, etc.). A reader landing here from a search or directory listing will reasonably believe this is still in flight. The directory has no `README.md` describing the convention ("design + plan kept as ADRs").
- **Recommendation**: Add `docs/plans/README.md` (one paragraph) stating these are historical / shipped plans. Optionally rename to `docs/history/` or `docs/adrs/` and prepend a `> Status: shipped (2026-…)` line to each file.

## 4. `AGENTS.md` and `CLAUDE.md` are byte-identical with no shared-source mechanism

- **Location**: `AGENTS.md` (14 lines), `CLAUDE.md` (14 lines)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Both files contain the same agent-facing instructions (testing dir, build/test commands, commit-discipline rules). Maintainers must remember to update both whenever the rules change; a divergence will silently mislead one class of agent. There is no comment or symlink establishing the relationship.
- **Recommendation**: Make one the source of truth and the other a thin pointer (e.g. `CLAUDE.md` content = "See [AGENTS.md](AGENTS.md)."). If both must literally exist for tool discovery reasons, add a top-of-file note in each declaring which is canonical and that the other is a mirror.

## 5. README's "Tips and Tricks" section is a TODO outline, not content

- **Location**: `README.md:331-337`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: The README publishes a `## Tips and Tricks` heading whose body is literally `TODO:` followed by five unwritten bullets ("separate agent config", "customize test hooks", "go to lunch, adversarial review, blackbox tester", "perform a task in a worktree", "multi-pass loop with budget / max rounds", "implement all the changes from a given report with an ad-hoc prompt"). A reader who navigates to this anchor (it sits between "Roles" and "FAQ") receives no information.
- **Recommendation**: Either fill in the bullets (each is a concrete `ateam exec` / `ateam parallel` recipe already supported by the CLI) or remove the section until content exists. Promote the worktree + multi-pass tips to `FAQ.md` if README space is tight.

## 6. `defaults/roles/` carries dual-named roles with no documented relationship

- **Location**: `ROLES.md` lines for `dependencies` vs `project.dependencies`, `docs_internal` vs `docs.internal`, `docs_external` vs `docs.external`, `critic_engineering` vs `critic.engineering`, `critic_project` vs `critic.project`, `testing_basic`/`testing_full` vs `test.gaps`/`test.quality`/`test.blackbox`/`test.recent`, `production_ready` vs `project.production_ready`, `refactor_architecture`/`refactor_small` vs `code.structure`, `database_schema` vs `database.schema`, `database_config` vs `database.schema`, etc.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `ROLES.md` (auto-generated by `ateam roles --docs`) advertises ~39 roles, with multiple pairs using `_` vs `.` naming for what appear to be older vs newer formulations. There is no header in `ROLES.md` and no inline note explaining the convention — a developer choosing roles for `auto-setup` or `config.toml` has to read every prompt to discover that `docs.internal` is the dotted/newer version of `docs_internal`. The README's "Enabled by default" table lists only the `_` variants, deepening the confusion.
- **Recommendation**: Add a short preamble to `ROLES.md` (auto-emitted by `cmd/roles.go` when `--docs` is passed) that documents the `.`-vs-`_` convention and which family is preferred. Mark deprecated names with `(deprecated, prefer X)` in the Description column, or drop one family.

## 7. No top-level architecture index — overview is split across DEV.md + CONCURRENCY.md

- **Location**: `DEV.md:170-260` ("Architecture: Runtime / Agents / Containers / Profiles" inside the dev-setup file); `CONCURRENCY.md` (focused on pool concurrency only); `isolation_execution.mmd` / `isolation_execution.png` (one diagram, isolation-only)
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: The "what packages exist and how they call each other" overview is currently embedded inside `DEV.md` between the test instructions and the maintenance-command reference. There is no `ARCHITECTURE.md` (nor `docs/architecture.md`) that a new developer can read in 10 minutes to understand the layering (`cmd/` → `internal/runner/` → `internal/agent/` + `internal/container/`, with `internal/prompts/` + `internal/config/` feeding both, and `calldb` + `runtime` underneath). For a Go project with 16 internal packages this is the largest onboarding gap.
- **Recommendation**: Lift the "Architecture" subsections out of `DEV.md` into a new `ARCHITECTURE.md` (or `internal/README.md` since Go convention places it there). Include a one-paragraph description per package, a call-graph sketch, and pointers to `CONCURRENCY.md` for the pool contract and `ISOLATION.md` for the execution-mode diagram. Link from `README.md` ("More docs") and from `DEV.md`'s old anchor.

## 8. `internal/secret/` package doc is on a non-first file

- **Location**: `internal/secret/store.go` has `// Package secret …`; `internal/secret/resolve.go` (alphabetically first) has no doc comment.
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `gofmt`/`golint` tooling and `go doc` are unaffected (Go accepts the doc comment on any file), but readers using IDE "jump to file" navigation and some doc generators show the alphabetically first file. Minor inconsistency with peer packages (`agent`, `runner`, `web`, `runtime`, `prompts`, `eval`, `calldb`, `config`, `container`, `display`, `fsclone`, `gitutil`, `root`) where the doc lives on either the package-named file or the primary entry file.
- **Recommendation**: Move the `// Package secret …` comment from `store.go` to a new `internal/secret/doc.go` (Go-idiomatic), or to whichever file is the package's primary entrypoint.

## 9. Compatibility-shim removal table is hidden in `DEV.md`, not surfaced where the shims live

- **Location**: `DEV.md:215-230` ("Compatibility shims (remove after legacy data ages out)" table) — pointers into `internal/root/resolve.go`, `cmd/inspect.go`, `cmd/resume.go`, `cmd/pool_status.go`, `internal/web/handlers.go`, `internal/web/history.go`, `internal/root/migrate_logs.go`, `internal/runner/template.go`, `internal/runner/runner.go`.
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The table tells a maintainer where to look but the corresponding code locations have no inline `// LEGACY:` marker tying them back. A future agent or developer touching `internal/runner/template.go::EXECUTION_DIR` won't know to also delete the DEV.md row when removing the shim, leading to drift.
- **Recommendation**: At each shim site add a brief comment such as `// LEGACY shim — see DEV.md "Compatibility shims".` The DEV.md table already mentions "Search for `legacy` in those files" but only some files actually contain the word.

# Quick Wins

1. **Bump Go version in docs** (Finding 1) — three single-line edits, removes a real onboarding blocker.
2. **Add `docs/plans/README.md` marking the plans as shipped** (Finding 3) — one-paragraph file, prevents the "is this in flight?" confusion.
3. **De-duplicate `AGENTS.md` / `CLAUDE.md`** (Finding 4) — collapse one into a pointer, removes a silent-drift hazard.
4. **Either fill or remove the README "Tips and Tricks" TODO list** (Finding 5) — a published heading with no body should not ship.
5. **Add a one-paragraph naming-convention preamble to `ROLES.md`** (Finding 6) — emitted from `cmd/roles.go --docs`, immediately clarifies the `.` vs `_` duplication that new users hit on `auto-setup`.

# Project Context

- **Tech stack**: Go 1.26.3 (per `go.mod`), single-binary CLI, cobra-based commands, embedded `defaults/` filesystem for prompts/runtime config.
- **Top-level docs (file → role)**:
  - `README.md` — overview, install, pipeline, isolation summary, roles table
  - `APPROACH.md` — *stub* (Finding 2)
  - `DEV.md` — build, tests, on-disk layout, architecture-by-subsection, compatibility shims, maintenance commands
  - `CONFIG.md` — `.ateamorg/` + `.ateam/` directory layout, `config.toml`, `runtime.hcl`
  - `COMMANDS.md` — full CLI reference; coverage verified against the 30 cobra subcommands in `cmd/*.go`
  - `ISOLATION.md` — sandbox/Docker modes, secrets, auth
  - `CONCURRENCY.md` — pool-boundary contract (7 rules)
  - `EVAL.md` — `ateam eval` prompt comparison
  - `FAQ.md` — short Q&A
  - `ROLES.md` — auto-generated by `make docs` from `ateam roles --docs`
  - `AGENTS.md` / `CLAUDE.md` — agent-facing rules (Finding 4)
  - `isolation_execution.mmd` / `.png` — single architecture diagram, isolation-only
- **Plans / ADRs**: `docs/plans/2026-03-07-org-project-split-{design,plan}.md` — historical, currently unmarked (Finding 3).
- **Inline / package docs** (Go side):
  - All 16 `internal/*` packages have a `// Package …` doc comment.
  - `internal/runner/pool.go::RunPool`, `internal/runner/pool.go::RunPoolWithOpts`, `internal/agent/agent.go::Agent`, `internal/root/migrate_logs.go::MigrateLogsLayout`, `internal/prompts/prompts.go` all carry substantive contract doc comments.
  - 6 `TODO/FIXME/XXX` markers across non-test `internal/` + `cmd/` code (low).
- **Doc-generation automation**: `make docs` runs `ateam roles --docs > ROLES.md`; `make check-docs` is wired into `make check` (verified in `Makefile` `.PHONY` line). Recent commit `ccd5003` shows the pipeline is actively maintained ("roles --docs: escape `|` in descriptions").
- **Suggested doc tooling** (not yet in repo): `golangci-lint` with the `revive` linter's `package-comments`/`exported` rules to enforce doc comments going forward; `markdown-link-check` in CI to catch broken cross-references between top-level `.md` files.
