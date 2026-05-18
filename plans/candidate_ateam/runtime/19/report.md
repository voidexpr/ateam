# Summary

The local foundations are in good shape: `make build` works (~1.6s), `make test` runs the full unit suite cleanly with `-race` (~14s), `make check` chains test/fmt-check/check-tidy/check-docs/lint, and the Makefile separates fast / Docker / live tiers explicitly. The real gaps are at the edges — DEV.md misrepresents what CI actually runs, and the on-disk pre-commit hook has drifted behind the `install-hooks` template. CI is `workflow_dispatch`-only, which is a defendable choice for a solo-author project but should not be sold in the docs as auto-running on push/PR.

# Findings

## DEV.md misrepresents CI behavior — claims it runs on every push/PR, but workflows are manual-only

- **Location**: `DEV.md:85-99`, `.github/workflows/ci.yml:3-4`, `.github/workflows/docker-tests.yml:3-4`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `DEV.md` states "GitHub Actions (`.github/workflows/ci.yml`) runs on every push to `main` and on pull requests" and "A separate workflow (`.github/workflows/docker-tests.yml`) runs `make test-docker` on pushes to `main`". Both workflows are actually defined with `on: workflow_dispatch:` only — neither runs automatically on any push, PR, or merge. An agent or contributor reading DEV.md will believe a safety net exists that does not. This is the worst kind of foundations gap because it gives false confidence: every higher-priority quality role downstream may write findings on the assumption that CI will catch X, when in fact nothing runs unless the workflow is manually triggered. The discrepancy is purely documentation — the gates themselves (lint, vuln, test, docker tests) all exist and run fine locally via `make run-ci` / `make test-docker`.
- **Recommendation**: Either (a) change the workflow triggers to match what DEV.md describes (`on: [push, pull_request]` for `ci.yml`, plus the path-filtered push trigger DEV.md describes for `docker-tests.yml`), or (b) update DEV.md to reflect reality: that CI workflows exist but must be manually dispatched, and that the local `make run-ci` / pre-commit hook is the actual gate. Pick whichever matches intent — the project is solo-author with no concurrent contribution today, so option (b) is fully defensible. What is not OK is letting the docs stay out of sync.

## Pre-commit hook has drifted from the `install-hooks` template

- **Location**: `.git/worktrees/ateam-candidate/hooks/pre-commit` (resolved from `Makefile:151-154`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The `make install-hooks` target installs `make fmt-check && make check-tidy && make check-docs && make lint`, but the on-disk pre-commit currently runs only `make fmt-check && make check-tidy`. The two missing gates (`check-docs`, `lint`) silently no-op on every commit, so a developer can land a `golangci-lint` failure or a `ROLES.md` drift without local warning. This is harmless in steady-state — `make check` covers it when run manually — but defeats the purpose of having the hook installed.
- **Recommendation**: Re-run `make install-hooks` to refresh the hook from the template. Optional follow-up: have the hook itself print its template version (or have `make check` warn when the on-disk hook predates the template) so future drift is visible.

## DEV.md `make check` description is also stale

- **Location**: `DEV.md:65-66`, `Makefile:56`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: DEV.md says `make check` runs "test, fmt-check, check-tidy, and lint". The Makefile target actually runs `test fmt-check check-tidy check-docs lint` — `check-docs` is missing from the docs. Same class of issue as the CI drift above: the gate exists and runs, the docs just don't list it. Low-impact because `check-docs` failing surfaces with a clear diff against `ROLES.md`, but it still erodes trust in DEV.md.
- **Recommendation**: Add `check-docs` to the DEV.md description of `make check`. Bundle with the CI doc fix above.

# Quick Wins

1. Fix the DEV.md / CI mismatch (MEDIUM severity, SMALL effort) — either add real triggers to the workflows or correct DEV.md.
2. Re-run `make install-hooks` to refresh the local pre-commit (LOW, SMALL).
3. Add `check-docs` to the `make check` description in DEV.md (LOW, SMALL).

# Project Context

- **Language / build**: Go (`go.mod` requires Go 1.25+; Makefile builds via `go build -ldflags ...`). Single binary `ateam` at repo root, plus an optional Linux companion under `build/ateam-linux-amd64`.
- **Task runner**: GNU `make`. All gates are Make targets — Makefile is the single source of truth. Key targets: `build`, `companion`, `test`, `test-cli`, `test-docker`, `test-docker-live`, `lint`, `fmt-check`, `check-tidy`, `check-docs`, `check`, `run-ci`, `vuln`, `install-hooks`, `clean`.
- **Test tiers (well-separated)**:
  - Fast: `make test` — `go test -race ./...`, ~14s, no external deps.
  - Docker integration: `make test-docker` — runs DinD, requires Docker; isolated from host daemon.
  - Live (paid): `make test-docker-live` — calls real Claude haiku (~$0.03/run); guarded by an auth precheck that fails clearly if no `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` is configured.
  - CLI: `make test-cli` — ends-to-end against the built binary with isolated HOME.
  - All four are documented and gated correctly.
- **Lint / format / static checks**: `golangci-lint` v2 with `default: standard` linters, configured in `.golangci.yml`; `gofmt` enforced via `fmt-check`; `go mod tidy -diff` via `check-tidy`; `govulncheck` via `make vuln` (auto-installs from `@latest`, gracefully skips when sandboxed/offline).
- **Local gates**: `make check` (test + fmt-check + check-tidy + check-docs + lint) and `make run-ci` (`check` + `vuln`). Pre-commit hook installed via `make install-hooks` writes `.git/hooks/pre-commit` running fmt-check + check-tidy + check-docs + lint.
- **CI**: `.github/workflows/ci.yml` (`make run-ci`) and `.github/workflows/docker-tests.yml` (`make test-docker`). Both are `workflow_dispatch:` only — manual trigger required. No `push` or `pull_request` triggers.
- **Output discipline**: `make build`, `make fmt-check`, `make check-tidy` are quiet on success; `make test` prints the per-package `ok` lines (Go default — acceptable for both human and agent use).
- **Project shape**: Solo-author in recent history (`git log --since='3 months ago'` shows only `me on Desktop`). Older history shows other names (`Nicolas`, `ATeam Agent`, `Voidexpr at Desktop`, agent-role authors), but no concurrent contribution. CI absence on `push` is therefore not a foundations crisis — local gates carry the load. The actual finding is the documentation lying about what CI does, not the manual-trigger choice itself.
- **Key files for next run**: `Makefile`, `DEV.md`, `.github/workflows/ci.yml`, `.github/workflows/docker-tests.yml`, `.golangci.yml`, `install.sh`, `CLAUDE.md`. Pre-commit hook resolves through worktree to `/Users/nicolas/SyncDatabox/nicmac/projects/ateam/.git/worktrees/ateam-candidate/hooks/pre-commit`.
