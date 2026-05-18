# Summary

Internal documentation is unusually strong for a project this young: `DEV.md`, `CONCURRENCY.md`, `ISOLATION.md`, `CONFIG.md`, and `COMMANDS.md` collectively form a working developer guide with rationale, on-disk layout, runtime / agent / container architecture, and a 7-rule concurrency contract that names the production incidents it's preventing. Package-level Go doc comments (`agent.go`, `runner.go`, `calldb.go`) carry through the same discipline. The gaps are concrete and small: a couple of doc/reality drifts (Go version, missing `test_data/`), a duplicated report-base prompt whose temporary nature is only flagged by inline TODOs, and an agent-facing `CLAUDE.md` that under-uses the rich docs that exist around it.

# Findings

## Role performing the audit

- Role: `docs.internal`
- Model: claude-opus-4-7 (default thinking)
- Mode: read-only repository analysis

## 1. Agent-facing instruction points at non-existent `./test_data/` directory

- **Location**: `CLAUDE.md` line 4, `AGENTS.md` (symlink → `CLAUDE.md`)
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: `CLAUDE.md` tells agents "ateam testing requires to create and delete files, use ./test_data/", but no `./test_data/` directory exists at the repo root. An agent following the instruction will either create the directory at an arbitrary location or be confused about scope (per-test subdir? gitignored? committed?). This is exactly the failure mode the role exists to catch: an instruction read literally by an LLM, ambiguous in practice. The instruction is also a duplicate of guidance that already lives in test files' own setup code (e.g. `test/cli/test-auth-combos.sh` uses `mktemp -d` instead).
- **Recommendation**: Either (a) create `test_data/` with a one-line `README.md` and add it to `.gitignore` for runtime artefacts, or (b) replace the line with concrete guidance like "Use `t.TempDir()` in Go tests and `mktemp -d -p \"$TMPDIR\"` in shell tests; do not write inside the repo root".

## 2. Go-version drift between `go.mod` and developer/install docs

- **Location**: `go.mod` line 3 (`go 1.26.3`); `DEV.md` line 10 ("Requires Go 1.25+"); `README.md` line 99 / 106; `install.sh` line 6 (`REQUIRED_GO_VERSION="1.25"`); `Makefile` line 39 uses `golang:1.26`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `go.mod` pins `go 1.26.3`, but every install / dev doc and the installer script still say "Go 1.25+". A contributor (or `install.sh` invocation) that installs 1.25 will hit a build error from the toolchain directive in `go.mod`. The Makefile's `companion-race` target already uses `golang:1.26`, confirming 1.26 is the real floor. This is internal-docs scope because `install.sh` and `DEV.md` are engineer-facing setup; the `README.md` mirror is `docs.external`'s concern but they share a root cause.
- **Recommendation**: Update `DEV.md`, `install.sh` (`REQUIRED_GO_VERSION="1.26"`), and the README install/prereq lines to "Go 1.26+". Consider deriving the version from `go.mod` in `install.sh` to prevent recurrence.

## 3. Duplicate report-base prompt only signposted by inline TODOs

- **Location**: `defaults/report_base_prompt.md`, `defaults/new_report_base_prompt.md`, `internal/prompts/embed.go` (`DefaultNewReportBasePrompt`, `embeddedFiles` — two TODO blocks)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Two report-base prompts exist temporarily for the legacy vs dotted-prefix role A/B. The only signal of this temporary state is (a) the HTML comment at the top of `new_report_base_prompt.md` and (b) two TODO comments in `internal/prompts/embed.go`. There is no entry in `DEV.md`, no `CLAUDE.md` note, and no role-author guidance describing the split. A future engineer or agent who edits one base will silently break the A/B comparison or, worse, "clean up" by deleting one without realising both are referenced from `embed.go` and consumed by different role sets. This is institutional-memory-at-risk: when the v1 cleanup ships, anyone who edited the wrong base in the interim will need rework.
- **Recommendation**: Add a short "Temporary docs / prompts" section to `DEV.md` listing the duplicate, why it exists, which roles read which base, and the explicit removal trigger ("when no dotted-prefix role is still in pilot"). Cross-link the TODOs back to it. The mechanism already exists for "Compatibility shims" in `DEV.md` — extend the same table to cover prompt-level shims, not just code-level ones.

## 4. `CLAUDE.md` is too sparse given the richness of the surrounding docs

- **Location**: `CLAUDE.md` (and `AGENTS.md` symlink)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The file is 14 lines, mostly procedural ("run make build", "make test always"), and misses several agent-relevant entry points that already exist:
  - No pointer to `CONCURRENCY.md` despite that doc opening with "two SIGSEGVs in production came from shared mutable state … If you're adding a field to `Runner`, a method to `Container` / `Agent`, or a new resource used under the pool — read this first." An agent editing `internal/runner/` or `internal/container/` without this primer reintroduces exactly the bug class the doc was written to prevent.
  - "any time you change agent, container, runner related code or dependencies" — "dependencies" is ambiguous (go.mod? Dockerfile? `defaults/runtime.hcl`?). An agent will guess.
  - No mention of `make check` as the all-in-one pre-commit gate (it runs test + fmt-check + check-tidy + check-docs + lint — exactly what the agent should self-verify with).
  - No mention that editing a role's frontmatter `description:` requires regenerating `ROLES.md` (via `make docs` or `make check-docs` to catch drift). `check-docs` is in `make check`, so drift is caught — but the agent doesn't know `make docs` is the regeneration command.
  - "do NOT git commit without asking me first" appears twice in the same 14-line file (under "Testing" implications and under "Development guidelines").
- **Recommendation**: Rewrite as a short checklist with concrete commands and pointers: build/test commands, when to run `make test-docker` (name the dirs: `internal/agent/`, `internal/container/`, `internal/runner/`, `Dockerfile`, `test/Dockerfile.dind`), pre-commit `make check`, a one-line "before touching runner/container/agent: read `CONCURRENCY.md`", and the `make docs` regenerator. Remove the duplicate no-commit rule.

## 5. `APPROACH.md` is a 3-line stub but linked as if substantive

- **Location**: `APPROACH.md`; cross-references in `README.md` line 13 and the "More docs" list line 374
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `APPROACH.md` reads in full: "_This document is a stub._ It will collect the rationale, principles, and key concepts behind ATeam. See [README.md] for the current overview …". `README.md` says "See [APPROACH.md] for the rationale and design principles behind ATeam." — circular. An engineer (or agent) chasing the documented "rationale and design principles" link gets nothing. This is mainly a docs.external issue but it also misleads a new internal contributor.
- **Recommendation**: Either inline whatever rationale exists into `README.md`'s "Why ATeam" / "Core principles" sections and delete `APPROACH.md`, or remove the README link until the file has content. Don't keep a stub-pointing-back-to-its-pointer.

## 6. No entry-point doc for the agent stream-event pipeline

- **Location**: `internal/agent/agent.go::StreamEvent` (108–), `internal/runner/parse_stream.go::DisplayEvent` (10–), `internal/streamutil/` (raw parsing), `internal/runner/format_stream.go` (formatting)
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: ateam normalises agent output through three layers — raw Claude/Codex JSONL → `agent.StreamEvent` → `runner.DisplayEvent` → renderer. Each layer has good package/struct comments, but the end-to-end flow (where to add a new field, when to extend StreamEvent vs DisplayEvent, what `streamutil` owns vs `parse_stream.go`) is not documented anywhere readable in one sitting. For an engineer adding a new agent backend (the README's "Future" section names Pi and Gemini) this is a discovery cost. `CONCURRENCY.md` is a great model: a single subsystem doc with explicit "read first if you're doing X" framing.
- **Recommendation**: Add `internal/agent/STREAM.md` (or a section in `DEV.md`) describing the three-layer pipeline, where each layer's boundary is, and what gets versioned (e.g. is `StreamEvent` a stable contract across agent backends? Today it's a struct shared in-process, but adding a backend means matching its semantics). One page is sufficient; reference it from the agent.go package comment.

## 7. `make test-docker` trigger condition is vague — "or dependencies"

- **Location**: `CLAUDE.md` line 8 ("`make test-docker`: any time you change agent, container, runner related code or dependencies")
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: "Dependencies" is undefined. Does it mean `go.mod` changes, `Dockerfile` / `test/Dockerfile.dind` changes, or `defaults/runtime.hcl` agent-config changes? `.github/workflows/docker-tests.yml` (referenced in `DEV.md`) gates on `test/`, `Dockerfile*`, and `internal/container/` — that's the actual answer. The agent-facing file should mirror the CI gate.
- **Recommendation**: Replace "or dependencies" with the explicit path list used by CI: changes under `test/`, `Dockerfile*`, `internal/container/`, `internal/agent/`, `internal/runner/`, or `go.mod`/`go.sum` that pull in new container-side packages.

# Quick Wins

1. **Fix the Go-version drift** (finding 2): one-line edits to `DEV.md`, `install.sh`, and `README.md` to match `go.mod`'s `1.26`. Prevents install-time confusion.
2. **Resolve `./test_data/` ambiguity** (finding 1): either create the directory + gitignore, or rewrite the line to point at `t.TempDir()` / `mktemp`. One-line `CLAUDE.md` edit.
3. **Document the duplicate report-base prompt** (finding 3): add a short note to `DEV.md` (alongside the "Compatibility shims" table) and cross-link the inline TODOs. Saves a future agent from "cleaning up" the duplicate prematurely.
4. **Tighten `make test-docker` trigger paths in `CLAUDE.md`** (finding 7): replace "or dependencies" with the concrete path list already used by CI.
5. **Add a one-line `read CONCURRENCY.md` pointer to `CLAUDE.md`** (part of finding 4): preempts the exact bug class the doc was written to prevent.

# Project Context

- **Tech stack**: Go 1.26 module (`github.com/ateam`), single binary built from `main.go`. SQLite via `modernc.org/sqlite`. Docker for isolation modes. Embedded prompts FS in `defaults/`.
- **Top-level docs** (engineer-facing, all live at repo root):
  - `DEV.md` (364 lines) — build/test/architecture; the closest thing to a complete dev guide. Includes `runtime/<exec_id>/` and `logs/<exec_id>/` layout, promotion semantics, compatibility-shim table, runtime/agent/container/profile architecture.
  - `CONCURRENCY.md` (158 lines) — 7-rule contract for the pool boundary. Names the production incidents (two SIGSEGVs) it prevents. Exemplary internal doc.
  - `ISOLATION.md` (532 lines) — execution modes, secrets, auth.
  - `CONFIG.md` (498 lines) — directory layout, `config.toml`, `runtime.hcl`, 4-level prompt resolution.
  - `COMMANDS.md` (766 lines) — full CLI reference.
  - `ROLES.md` (45 lines) — auto-generated from role prompt frontmatter via `make docs` / `ateam roles --docs`.
  - `EVAL.md` (160 lines) — `ateam eval` workflow.
  - `FAQ.md` (76 lines) — troubleshooting and intent.
  - `APPROACH.md` — 3-line stub (see finding 5).
  - `CLAUDE.md` (14 lines) / `AGENTS.md` (symlink) — agent-facing.
- **Embedded prompt FS**: `defaults/` (read by `internal/prompts/embed.go`). Role descriptions come from YAML frontmatter in `defaults/roles/<NAME>/report_prompt.md`. Editing a description requires re-running `make docs` to refresh `ROLES.md`; `make check-docs` catches drift and is part of `make check` and the optional pre-commit hook (`make install-hooks`).
- **Build/verification commands** (Makefile):
  - `make build` — host binary.
  - `make companion` — linux/amd64 binary for Docker mode.
  - `make build-all-race` — race-instrumented host + linux binaries for SIGSEGV repro.
  - `make test` — unit tests with `-race`.
  - `make test-docker` — DinD container integration tests.
  - `make test-docker-live` — live Claude haiku in DinD (~$0.03, requires auth).
  - `make test-cli` — CLI auth-combo shell test (`test/cli/test-auth-combos.sh`).
  - `make check` — test + fmt-check + check-tidy + check-docs + lint (developer one-liner).
  - `make run-ci` — `check` + `vuln`.
  - `make docs` — regenerate `ROLES.md`.
  - `make install-hooks` — installs pre-commit hook (`fmt-check`, `check-tidy`, `check-docs`, `lint`).
- **Internal architecture entry points**:
  - `internal/runner/runner.go` (1281 lines) — agent execution orchestration.
  - `internal/runner/pool.go` — pool boundary; concurrency contract enforced here.
  - `internal/agent/agent.go` — `Agent` interface + `StreamEvent` normalized type. Backends: `claude.go`, `codex.go`, `mock.go`.
  - `internal/container/` — `Container` interface; `none` / `docker` / `docker-exec`; `prepare_guard.go` for one-time setup.
  - `internal/prompts/embed.go` — 4-level prompt resolution + `embed.FS` from `defaults/`.
  - `internal/calldb/calldb.go` — `agent_execs` SQLite schema (single table).
  - `internal/root/migrate_logs.go` — lazy migration of legacy log layout (sentinel `logs/.layout-v2`).
- **CI**: `.github/workflows/ci.yml` (fmt-check, check-tidy, lint, test, vuln) on push/PR; `.github/workflows/docker-tests.yml` on touches to `test/`, `Dockerfile*`, `internal/container/`.
- **Project maturity signal**: actively developed (HEAD: 2026-05-14, four commits in the last day touching prompts and review logic). Internal docs are load-bearing because agents (including this very report) read them.
- **Known temporary duplication**: `defaults/report_base_prompt.md` vs `defaults/new_report_base_prompt.md`, A/B for legacy vs dotted-prefix role set. Removal trigger: dotted-prefix roles validated for v1. Only signposted by inline TODOs (finding 3).
