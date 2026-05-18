# Summary

Internal documentation remains unusually strong for a project this young: `DEV.md`, `CONCURRENCY.md`, `ISOLATION.md`, `CONFIG.md`, and `COMMANDS.md` collectively form a working developer guide with rationale, on-disk layout, runtime / agent / container architecture, and a 7-rule concurrency contract that names the production incidents it prevents. Package-level Go doc comments (`agent.go`, `runner.go`, `calldb.go`) carry through the same discipline. None of the findings from the prior cycle (2026-05-14_13-15-06) have been addressed in the three hours since, and one new doc/reality drift surfaced: `DEV.md` describes `docker-tests.yml` as path-triggered on `main`, but the workflow is now `workflow_dispatch`-only.

# Role performing the audit

- Role: `docs.internal`
- Model: claude-opus-4-7 (default thinking)
- Mode: read-only repository analysis

# Findings

## 1. Agent-facing instruction points at non-existent `./test_data/` directory

- **Location**: `CLAUDE.md` line 4, `AGENTS.md` (symlink → `CLAUDE.md`)
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: `CLAUDE.md` tells agents "ateam testing requires to create and delete files, use ./test_data/", but no `./test_data/` directory exists at the repo root. `.gitignore:8` does list `test_data`, so the intent is "create it as needed, don't commit it" — but the instruction doesn't say that. An agent reading the line literally will either create the directory at an arbitrary scope (per-test subdir? at the repo root? as a shared mutable workspace?) or default to writing inside the working tree. The actual test code uses other patterns: `test/cli/test-auth-combos.sh` uses `mktemp -d`, Go tests use `t.TempDir()`. Plans under `plans/` and `docs/plans/` propagate the same ambiguous `./test_data/` reference, so the rule is silently spreading.
- **Recommendation**: Replace the line with concrete, mechanism-bound guidance: "Use `t.TempDir()` in Go tests and `mktemp -d -p \"$TMPDIR\"` in shell tests. If a test requires a persistent fixture across runs, place it under `./test_data/` (gitignored) and clean up on exit." Optionally seed `test_data/.gitkeep` + a one-line `test_data/README.md` so the path resolves on first use.

## 2. Go-version drift between `go.mod` and developer/install docs

- **Location**: `go.mod` line 3 (`go 1.26.3`); `DEV.md` line 10 ("Requires Go 1.25+"); `install.sh` line 6 (`REQUIRED_GO_VERSION="1.25"`); README install/prereq lines; `Makefile` uses `golang:1.26` for `companion-race`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `go.mod` pins `go 1.26.3`, but `DEV.md` and `install.sh` still say "Go 1.25+". A contributor following the docs and installing 1.25 will hit a toolchain-version error from `go.mod`. The Makefile's `companion-race` target already uses `golang:1.26`, confirming 1.26 is the real floor. `install.sh` is engineer-facing setup (internal-docs scope); the README mirror is `docs.external`'s concern but they share a root cause.
- **Recommendation**: Update `DEV.md` and `install.sh` (`REQUIRED_GO_VERSION="1.26"`) to `1.26+`. Consider deriving the version from `go.mod` in `install.sh` (e.g. parse the `go ` line) so the next bump can't desync.

## 3. `DEV.md` mis-states the docker-tests CI trigger

- **Location**: `DEV.md` line 99; `.github/workflows/docker-tests.yml`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `DEV.md` says: "A separate workflow (`.github/workflows/docker-tests.yml`) runs `make test-docker` on pushes to `main` that touch `test/`, `Dockerfile*`, or `internal/container/`." The actual workflow file declares `on: workflow_dispatch:` only — it's manual-trigger, with no push event and no path filters. A contributor (or agent) editing `internal/container/` and trusting the doc will assume CI will run `make test-docker` for them; in fact nothing runs until someone clicks the button. This is the exact "stale architecture doc that actively misleads" class. It also undermines finding 7 below, which leans on the same doc-claimed path list as a source of truth.
- **Recommendation**: Decide which is canonical: either (a) restore the `push:` + `paths:` filters to `docker-tests.yml` (preferred — the doc describes the intent), or (b) update `DEV.md` to say the workflow is manual-trigger only and explain when a maintainer is expected to run it. If (a), include `internal/agent/` and `internal/runner/` in the paths if those edits should also gate, to match `CLAUDE.md`.

## 4. Duplicate report-base prompt only signposted by inline TODOs

- **Location**: `defaults/report_base_prompt.md`, `defaults/new_report_base_prompt.md`, `internal/prompts/embed.go` lines 191–196 and 257–261
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Two report-base prompts exist temporarily for the legacy vs dotted-prefix role A/B. The only signal of this temporary state is (a) the HTML comment at the top of `new_report_base_prompt.md` and (b) two TODO comments in `internal/prompts/embed.go` ("fix this before v1 — fold into DefaultReportBasePrompt …"). There is no entry in `DEV.md`, no `CLAUDE.md` note, and no role-author guidance describing the split. A future engineer or agent who edits one base will silently break the A/B comparison or "clean up" by deleting one without realising both are referenced from `embed.go`. Institutional memory at risk: when v1 cleanup ships, anyone who edited the wrong base in the interim will need rework.
- **Recommendation**: Add a short "Temporary prompts" subsection to `DEV.md` (alongside the existing "Compatibility shims" table) listing the duplicate, why it exists, which role sets consume which base, and the explicit removal trigger ("when no dotted-prefix role is still in pilot"). Cross-link from the two TODOs in `embed.go`. Extend the existing compatibility-shim table to cover prompt-level shims as well as code-level ones.

## 5. `CLAUDE.md` is too sparse given the richness of the surrounding docs

- **Location**: `CLAUDE.md` (14 lines) / `AGENTS.md` (symlink)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The file is mostly procedural ("run make build", "make test always") and misses several agent-relevant entry points that already exist in the repo:
  - No pointer to `CONCURRENCY.md` despite that doc opening with "two SIGSEGVs in production came from shared mutable state … if you're adding a field to `Runner`, a method to `Container` / `Agent`, or a new resource used under the pool — read this first." An agent editing `internal/runner/`, `internal/container/`, or `internal/agent/` without this primer reintroduces exactly the bug class the doc was written to prevent.
  - "any time you change agent, container, runner related code or dependencies" — "dependencies" is undefined (see finding 7).
  - No mention of `make check` as the all-in-one pre-commit gate (it runs test + fmt-check + check-tidy + check-docs + lint — exactly what the agent should self-verify with).
  - No mention that editing a role's frontmatter `description:` requires `make docs` to refresh `ROLES.md`. `make check-docs` (part of `make check`) catches drift, but the agent doesn't know `make docs` is the regenerator.
  - The "do NOT git commit without asking me first" rule appears twice in 14 lines.
- **Recommendation**: Rewrite as a short checklist with concrete commands and pointers: build/test commands; when to run `make test-docker` (with explicit dirs — see finding 7); `make check` as the self-verify gate; a one-liner "before touching runner/container/agent: read `CONCURRENCY.md`"; the `make docs` regenerator. Remove the duplicate no-commit rule.

## 6. `APPROACH.md` is a 3-line stub linked as if substantive

- **Location**: `APPROACH.md`; cross-references in `README.md`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `APPROACH.md` reads in full: "_This document is a stub._ It will collect the rationale, principles, and key concepts behind ATeam. See [README.md] for the current overview …". `README.md` says "See [APPROACH.md] for the rationale and design principles behind ATeam." — circular. An engineer (or agent) chasing the documented "rationale and design principles" link gets nothing. Primarily a docs.external concern but it also misleads a new internal contributor reading the doc index.
- **Recommendation**: Either inline whatever rationale exists into `README.md`'s "Why ATeam" / "Core principles" sections and delete `APPROACH.md`, or remove the README link until the file has content. Don't keep a stub pointing back to its pointer.

## 7. `make test-docker` trigger condition is vague — "or dependencies"

- **Location**: `CLAUDE.md` line 11
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: "Dependencies" is undefined. Does it mean `go.mod` changes, `Dockerfile` / `test/Dockerfile.dind` changes, or `defaults/runtime.hcl` agent-config changes? The intended answer (per DEV.md, modulo finding 3) is the CI path filter list: `test/`, `Dockerfile*`, and `internal/container/`. The agent-facing file should mirror whatever the CI gate actually is.
- **Recommendation**: Replace "or dependencies" with the explicit path list: changes under `test/`, `Dockerfile*`, `internal/container/`, `internal/agent/`, `internal/runner/`, or `go.mod`/`go.sum` entries that pull in new container-side packages. Coordinate with finding 3 so docs, `CLAUDE.md`, and the workflow file all agree.

## 8. No entry-point doc for the agent stream-event pipeline

- **Location**: `internal/agent/agent.go::StreamEvent`, `internal/runner/parse_stream.go::DisplayEvent`, `internal/streamutil/`, `internal/runner/format_stream.go`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: ateam normalises agent output through three layers — raw Claude/Codex JSONL → `agent.StreamEvent` → `runner.DisplayEvent` → renderer. Each layer has good package/struct comments, but the end-to-end flow (where to add a new field, when to extend `StreamEvent` vs `DisplayEvent`, what `streamutil` owns vs `parse_stream.go`) is not documented anywhere readable in one sitting. For an engineer adding a new agent backend (the README "Future" section names Pi and Gemini) this is a real discovery cost. `CONCURRENCY.md` is a great model: one subsystem doc with explicit "read first if you're doing X" framing.
- **Recommendation**: Add `internal/agent/STREAM.md` (or a section in `DEV.md`) describing the three-layer pipeline, the boundary of each layer, and what's a stable contract across backends. One page suffices; reference it from `internal/agent/agent.go`'s package comment.

# Quick Wins

1. **Fix the Go-version drift** (finding 2): one-line edits to `DEV.md` and `install.sh` (`REQUIRED_GO_VERSION="1.26"`) to match `go.mod`. Prevents install-time confusion.
2. **Resolve `./test_data/` ambiguity** (finding 1): rewrite the `CLAUDE.md` line to point at `t.TempDir()` / `mktemp` and reserve `test_data/` for persistent fixtures (it's already gitignored).
3. **Reconcile DEV.md's docker-tests claim with the actual workflow** (finding 3): either restore push+path filters to `docker-tests.yml` or correct `DEV.md` to say "manual trigger".
4. **Document the duplicate report-base prompt** (finding 4): short note in `DEV.md` next to the compatibility-shim table; cross-link the two `embed.go` TODOs.
5. **Tighten `make test-docker` trigger paths in `CLAUDE.md`** (finding 7): replace "or dependencies" with the concrete path list, and add the `CONCURRENCY.md` pointer (part of finding 5) in the same edit.

# Project Context

- **Tech stack**: Go 1.26 module (`github.com/ateam`), single binary built from `main.go`. SQLite via `modernc.org/sqlite`. Docker for isolation modes. Embedded prompts FS in `defaults/`.
- **Top-level docs** (engineer-facing, all live at repo root):
  - `DEV.md` (~364 lines) — build/test/architecture; the closest thing to a complete dev guide. Includes `runtime/<exec_id>/` and `logs/<exec_id>/` layout, promotion semantics, compatibility-shim table, runtime/agent/container/profile architecture. Note: line 99's claim about `docker-tests.yml` is stale (finding 3).
  - `CONCURRENCY.md` (~158 lines) — 7-rule contract for the pool boundary. Names the production SIGSEGVs it prevents. Exemplary internal doc.
  - `ISOLATION.md` (~532 lines) — execution modes, secrets, auth.
  - `CONFIG.md` (~498 lines) — directory layout, `config.toml`, `runtime.hcl`, 4-level prompt resolution.
  - `COMMANDS.md` (~766 lines) — full CLI reference.
  - `ROLES.md` (~45 lines) — auto-generated from role prompt frontmatter via `make docs` / `ateam roles --docs`.
  - `EVAL.md` (~160 lines) — `ateam eval` workflow.
  - `FAQ.md` (~76 lines) — troubleshooting and intent.
  - `APPROACH.md` — 3-line stub (finding 6).
  - `CLAUDE.md` (14 lines) / `AGENTS.md` (symlink) — agent-facing; thin given the surrounding richness (finding 5).
- **Embedded prompt FS**: `defaults/` (read by `internal/prompts/embed.go`). Role descriptions come from YAML frontmatter in `defaults/roles/<NAME>/report_prompt.md`. Editing a description requires re-running `make docs` to refresh `ROLES.md`; `make check-docs` catches drift and is part of `make check` and the optional pre-commit hook (`make install-hooks`).
- **Build/verification commands** (Makefile):
  - `make build` — host binary.
  - `make companion` — linux/amd64 binary for Docker mode.
  - `make build-all-race` — race-instrumented host + linux binaries for SIGSEGV repro (uses `golang:1.26`).
  - `make test` — unit tests with `-race`.
  - `make test-docker` — DinD container integration tests.
  - `make test-docker-live` — live Claude haiku in DinD (~$0.03, requires auth).
  - `make test-cli` — CLI auth-combo shell test (`test/cli/test-auth-combos.sh`).
  - `make check` — test + fmt-check + check-tidy + check-docs + lint (developer one-liner; NOT mentioned in `CLAUDE.md`).
  - `make run-ci` — `check` + `vuln`.
  - `make docs` — regenerate `ROLES.md`.
  - `make install-hooks` — installs pre-commit hook (`fmt-check`, `check-tidy`, `check-docs`, `lint`).
- **Internal architecture entry points**:
  - `internal/runner/runner.go` — agent execution orchestration.
  - `internal/runner/pool.go` — pool boundary; concurrency contract enforced here.
  - `internal/agent/agent.go` — `Agent` interface + `StreamEvent` normalized type. Backends: `claude.go`, `codex.go`, `mock.go`.
  - `internal/container/` — `Container` interface; `none` / `docker` / `docker-exec`; `prepare_guard.go` for one-time setup.
  - `internal/prompts/embed.go` — 4-level prompt resolution + `embed.FS` from `defaults/`. Contains the two A/B TODOs (finding 4).
  - `internal/calldb/calldb.go` — `agent_execs` SQLite schema (single table).
  - `internal/root/migrate_logs.go` — lazy migration of legacy log layout (sentinel `logs/.layout-v2`).
- **CI**: `.github/workflows/ci.yml` (fmt-check, check-tidy, lint, test, vuln) on push/PR; `.github/workflows/docker-tests.yml` is currently `workflow_dispatch`-only despite DEV.md's claim of a push+path trigger (finding 3).
- **Project maturity signal**: actively developed (HEAD: 2026-05-14, four commits in the last day touching prompts and review logic). Internal docs are load-bearing because agents (including this report) read them.
- **Gitignore-vs-disk note**: `test_data` is listed in `.gitignore` but the directory does not exist. The `CLAUDE.md` instruction (finding 1) does not explain this is "create-as-needed, don't commit".
- **Known temporary duplication**: `defaults/report_base_prompt.md` vs `defaults/new_report_base_prompt.md`, A/B for legacy vs dotted-prefix role set. Removal trigger: dotted-prefix roles validated for v1. Only signposted by inline TODOs (finding 4).
