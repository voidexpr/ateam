# Summary

Local foundations are healthy: `make build` succeeds, `make test` runs the full unit suite with `-race`, and `make check` chains `test fmt-check check-tidy check-docs lint` cleanly. Tiers (fast / Docker / live-paid) are well-separated and documented. The outstanding gaps are at the edges and unchanged from the previous run — `DEV.md` still misrepresents what CI actually runs, the on-disk pre-commit hook is still stale relative to the `install-hooks` template, and the `make check` description in `DEV.md` still omits `check-docs`. CI is `workflow_dispatch`-only, which is defensible for a solo-author project but should not be advertised as auto-running on push/PR.

# Role performing the audit

- **Role**: `project.automation`
- **Model**: Claude Opus 4.7 (`claude-opus-4-7`), standard reasoning, no extended thinking mode.
- **Mode**: Read-only audit. Verified findings by reading `DEV.md`, both workflow files, the resolved pre-commit hook, and the `Makefile`. No commands were executed against the build/test/lint chain in this run — the previous report already verified those work, and nothing in the working tree has changed since (last commit `6dcf9a0`, working tree clean).

# Findings

## DEV.md misrepresents CI behavior — claims it runs on every push/PR, but workflows are manual-only

- **Location**: `DEV.md:85-99`, `.github/workflows/ci.yml:3-4`, `.github/workflows/docker-tests.yml:3-4`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `DEV.md` states "GitHub Actions (`.github/workflows/ci.yml`) runs on every push to `main` and on pull requests" and "A separate workflow (`.github/workflows/docker-tests.yml`) runs `make test-docker` on pushes to `main` that touch `test/`, `Dockerfile*`, or `internal/container/`." Both workflows are actually defined with `on: workflow_dispatch:` only — neither runs automatically on any push, PR, or merge. An agent or contributor reading `DEV.md` will believe a safety net exists that does not. This is the worst kind of foundations gap because it gives false confidence: every higher-priority quality role downstream may write findings on the assumption that CI will catch X, when in fact nothing runs unless the workflow is manually dispatched. The gates themselves (lint, vuln, test, docker tests) all exist and run fine locally via `make run-ci` / `make test-docker`; only the documentation is wrong.
- **Recommendation**: Either (a) change the workflow triggers to match what `DEV.md` describes (`on: [push, pull_request]` for `ci.yml`, plus the path-filtered push trigger `DEV.md` describes for `docker-tests.yml`), or (b) update `DEV.md` to reflect reality: that CI workflows exist but must be manually dispatched, and that the local `make run-ci` / pre-commit hook is the actual gate. The project is solo-author in recent history with no concurrent contribution, so option (b) is fully defensible. What is not OK is letting the docs stay out of sync.

## Pre-commit hook has drifted from the `install-hooks` template

- **Location**: `/Users/nicolas/SyncDatabox/nicmac/projects/ateam/.git/hooks/pre-commit` (resolved from the worktree), template at `Makefile:151-154`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The `make install-hooks` target installs a hook that runs `make fmt-check && make check-tidy && make check-docs && make lint`. The on-disk hook currently contains only `make fmt-check && make check-tidy` (verified directly — file is 44 bytes, dated Apr 8). The two missing gates (`check-docs`, `lint`) silently no-op on every commit, so a developer can land a `golangci-lint` failure or a `ROLES.md` drift without local warning. Harmless in steady-state because `make check` covers it when run manually, but defeats the purpose of having the hook installed.
- **Recommendation**: Re-run `make install-hooks` to refresh the hook from the template. Optional follow-up: have the hook itself print its template version (or have `make check` warn when the on-disk hook predates the template) so future drift is visible.

## DEV.md `make check` description is also stale

- **Location**: `DEV.md:65-66`, `Makefile:56`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `DEV.md` says `make check` runs "test, fmt-check, check-tidy, and lint". The Makefile target actually runs `test fmt-check check-tidy check-docs lint` — `check-docs` is missing from the docs. Same class of issue as the CI drift above: the gate exists and runs, the docs just don't list it. Low-impact because `check-docs` failing surfaces with a clear diff against `ROLES.md`, but it still erodes trust in `DEV.md`.
- **Recommendation**: Add `check-docs` to the `DEV.md` description of `make check`. Bundle with the CI doc fix above.

# Quick Wins

1. Fix the `DEV.md` / CI mismatch (MEDIUM severity, SMALL effort) — either add real triggers to the workflows or correct `DEV.md`.
2. Re-run `make install-hooks` to refresh the local pre-commit (LOW, SMALL).
3. Add `check-docs` to the `make check` description in `DEV.md` (LOW, SMALL).

# Project Context

- **Language / build**: Go (`go.mod` requires Go 1.25+; Makefile builds via `go build -ldflags ...`). Single binary `ateam` at repo root, plus an optional Linux companion under `build/ateam-linux-amd64`.
- **Task runner**: GNU `make`. All gates are Make targets — Makefile is the single source of truth. Key targets: `build`, `companion`, `test`, `test-cli`, `test-docker`, `test-docker-live`, `lint`, `fmt-check`, `check-tidy`, `check-docs`, `check`, `run-ci`, `vuln`, `install-hooks`, `clean`.
- **Test tiers (well-separated)**:
  - Fast: `make test` — `go test -race ./...`, ~14s, no external deps.
  - Docker integration: `make test-docker` — runs DinD, requires Docker; isolated from host daemon. Build tag `docker_integration`.
  - Live (paid): `make test-docker-live` — calls real Claude haiku (~$0.03/run); guarded by an auth precheck that fails clearly if no `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` is configured. Build tag `docker_live`.
  - CLI: `make test-cli` — end-to-end against the built binary with isolated HOME.
- **Lint / format / static checks**: `golangci-lint` v2 with `default: standard` linters, configured in `.golangci.yml`; `gofmt` enforced via `fmt-check`; `go mod tidy -diff` via `check-tidy`; `govulncheck` via `make vuln` (auto-installs from `@latest`, gracefully skips when sandboxed/offline).
- **Local gates**: `make check` (test + fmt-check + check-tidy + check-docs + lint) and `make run-ci` (`check` + `vuln`). Pre-commit hook installed via `make install-hooks` writes `.git/hooks/pre-commit` running fmt-check + check-tidy + check-docs + lint.
- **CI**: `.github/workflows/ci.yml` (`make run-ci`) and `.github/workflows/docker-tests.yml` (`make test-docker`). Both are `workflow_dispatch:` only — manual trigger required. No `push` or `pull_request` triggers.
- **Output discipline**: `make build`, `make fmt-check`, `make check-tidy` are quiet on success; `make test` prints the per-package `ok` lines (Go default — acceptable for both human and agent use).
- **Project shape**: Solo-author contribution in recent history. `git log --since='3 months ago'` shows automated/role authors (`ATeam Agent`, `critical_code_reviewer (codex)`, `ateam security role`) and a single human (`me on Desktop` / `Nicolas` / `Voidexpr at Desktop` — same person across machines/contexts). No concurrent multi-developer contribution. CI absence on `push` is therefore not a foundations crisis — local gates carry the load. The actual finding is the documentation lying about what CI does, not the manual-trigger choice itself.
- **Key files for next run**: `Makefile`, `DEV.md`, `.github/workflows/ci.yml`, `.github/workflows/docker-tests.yml`, `.golangci.yml`, `install.sh`, `CLAUDE.md`. Pre-commit hook resolves through worktree to `/Users/nicolas/SyncDatabox/nicmac/projects/ateam/.git/hooks/pre-commit` (the common git dir, since `core.hooksPath` is unset and worktrees share hooks with the main repo).
