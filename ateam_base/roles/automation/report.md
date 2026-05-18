# Summary

Automation health is solid for a single-developer Go project: one Makefile is the single source of truth for `build / test / fmt / lint / tidy / docs / vuln`, and CI re-uses the exact same target (`make run-ci`). The major weakness is that the GitHub Actions workflows are configured as `workflow_dispatch` only — nothing runs automatically on push or PR, so all the careful checks rely on a human remembering to click "Run workflow". Several smaller issues: tool versions pulled at `@latest`, a Go version mismatch between `install.sh` and `go.mod`, no Dependabot, and pre-commit hooks are opt-in.

Role: automation. Model: Claude Opus 4.7 (claude-opus-4-7), default thinking, read-only analysis.

# Findings

## CI workflows never run automatically — `workflow_dispatch` only
- **Location**: `.github/workflows/ci.yml:3-4`, `.github/workflows/docker-tests.yml:3-4`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: Both workflows declare only `on: workflow_dispatch`. There is no `push` or `pull_request` trigger, so neither tests, lint, format-check, tidy diff, doc-sync check, nor vuln scan run on commits or PRs. The whole CI investment (carefully designed `make run-ci`) is effectively dormant unless someone manually triggers a run.
- **Recommendation**: Add `push` (at least on `main`) and `pull_request` triggers to `ci.yml`. Optionally trigger `docker-tests.yml` on `pull_request` when paths under `internal/container/**`, `test/**`, or `go.{mod,sum}` change. Add a `concurrency:` group keyed by `${{ github.ref }}` with `cancel-in-progress: true` to avoid superseded runs.

## `golangci-lint` and `govulncheck` pulled at `@latest`
- **Location**: `Makefile:129` (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`), `Makefile:109` (`go install golang.org/x/vuln/cmd/govulncheck@latest`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Auto-installation with `@latest` means a new upstream release can introduce lints or behavioral changes that break local builds and CI without any repo change. Bad for reproducibility; also makes it hard to bisect when CI suddenly turns red.
- **Recommendation**: Pin to specific versions (e.g. `golangci-lint@v2.x.y`, `govulncheck@vX.Y.Z`) in the Makefile and bump them via PR. Alternatively use the official `golangci/golangci-lint-action@v6` in CI with `version: vX.Y.Z`.

## `install.sh` requires Go 1.25 but `go.mod` is Go 1.26
- **Location**: `install.sh:6` (`REQUIRED_GO_VERSION="1.25"`), `go.mod:3` (`go 1.26.3`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: A user running `install.sh` with Go 1.25 installed will be told "Go 1.25 found ✓" and proceed to `make build`, which then fails because `go.mod` requires 1.26. Worse: the installer's auto-install path on Linux downloads `go${REQUIRED_GO_VERSION}.0.linux-${arch}.tar.gz`, i.e. `go1.25.0` — which silently provisions the wrong version.
- **Recommendation**: Either read the version from `go.mod` (`grep '^go ' go.mod`) in `install.sh`, or update `REQUIRED_GO_VERSION` to `1.26`. The first is preferred so the two stay in sync going forward.

## No Dependabot / Renovate config
- **Location**: `.github/` (no `dependabot.yml`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Neither Go modules nor GitHub Actions versions are kept current automatically. `actions/checkout@v4` and `actions/setup-go@v5` will quietly age; Go vulnerability fixes in deps require manual `go get -u`.
- **Recommendation**: Add `.github/dependabot.yml` with two `package-ecosystem` entries: `gomod` (weekly) and `github-actions` (weekly).

## Pre-commit hook is opt-in and not advertised in CONTRIB docs
- **Location**: `Makefile:151-154` (`install-hooks` target)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Running `make install-hooks` writes a hook that runs `fmt-check && check-tidy && check-docs && lint`, but nothing prompts new contributors (or the installer in `install.sh`) to enable it. As a result `make check` only runs locally if a developer remembers to invoke it.
- **Recommendation**: Either call `make install-hooks` from `install.sh` after a successful build, or document it as a single setup step in `DEV.md`. Consider also wiring `core.hooksPath` to a tracked directory (e.g. `scripts/hooks/`) so the hook can be version-controlled and reviewed.

## `Dockerfile.dind` base image unpinned
- **Location**: `test/Dockerfile.dind:12` (`FROM golang:alpine AS modules`), `test/Dockerfile.dind:21` (`FROM docker:27-dind`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `golang:alpine` floats across Go major/minor versions; a future Go release that breaks something in the container tests will reach CI silently. `docker:27-dind` is pinned to a major, which is fine.
- **Recommendation**: Pin to a specific Go version matching `go.mod`, e.g. `golang:1.26-alpine` (or read it dynamically with a build arg).

## CI single-job runs everything serially, no caching for lint/vuln
- **Location**: `.github/workflows/ci.yml:9-22`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: One `ci` job runs `test`, `fmt-check`, `check-tidy`, `check-docs`, `lint`, and `vuln` sequentially. Format failures are not surfaced until everything before them runs. There is also no `actions/cache` for the `golangci-lint` analysis cache (`~/.cache/golangci-lint`) or for the auto-installed `govulncheck` binary — every run reinstalls.
- **Recommendation**: Either keep the single-job model and just add a cache step for `~/.cache/golangci-lint` and `$(go env GOMODCACHE)`, or split into parallel `test` / `lint` / `vuln` jobs. For a small project, caching is the easier win.

## Test output is not separated into terse vs. verbose targets for role consumption
- **Location**: `Makefile:61-62` (`test` runs `go test -race ./...`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The CLAUDE.md guideline calls for terse output that does not waste tokens, with a verbose variant available on demand. `go test ./...` is reasonably terse on success but a failure dump (and `-race` data races) can be large. There is no `make test-verbose` and no terse summarizer for roles that only need pass/fail counts.
- **Recommendation**: Add a `test-quiet` target that pipes through a simple summarizer (`grep -E '^(--- |FAIL|ok|PASS|FAIL)'`) and keep `make test` verbose; or vice versa. Document which target roles should call.

## No `make help` target listing available actions
- **Location**: `Makefile:3` (`.PHONY:` list is the only inventory)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Contributors and agent roles must read the Makefile to discover targets. A self-documenting `help` target is a low-cost ergonomic win.
- **Recommendation**: Add a `help` target that greps `^[a-zA-Z_-]+:.*?## ` lines and prints them, then annotate each target with `## description`.

## `make vuln` swallows real failures when output contains "fetching vulnerabilities"
- **Location**: `Makefile:117-119`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The "skip when offline" heuristic matches *any* error output that contains `fetching vulnerabilities`. If govulncheck reports a real CVE and the same string appears elsewhere in its output (it commonly does, as a progress line), the failure could be masked. Today's behavior depends on the substring not landing in the normal failure path.
- **Recommendation**: Tighten the check to a more specific marker (e.g. require both `fetching vulnerabilities` *and* a network error indicator like `dial tcp` / `no such host`) or, better, probe network reachability explicitly before deciding to skip.

# Quick Wins

1. **Enable CI on push/PR** — add `on: [push, pull_request]` (with `concurrency: cancel-in-progress`) to `ci.yml`. SMALL effort, HIGH severity. (`.github/workflows/ci.yml:3-4`)
2. **Fix `install.sh` Go version** — derive `REQUIRED_GO_VERSION` from `go.mod` or bump to `1.26`. SMALL effort, MEDIUM severity. (`install.sh:6`)
3. **Pin `golangci-lint` and `govulncheck`** — replace `@latest` with explicit versions in the Makefile. SMALL effort, MEDIUM severity. (`Makefile:109,129`)
4. **Add Dependabot config** — one short `.github/dependabot.yml` covering `gomod` + `github-actions`. SMALL effort, MEDIUM severity.
5. **Pin Dockerfile.dind Go base image** — switch `golang:alpine` to `golang:1.26-alpine`. SMALL effort, LOW severity but trivial to fix. (`test/Dockerfile.dind:12`)

# Project Context

- **Language / runtime**: Go 1.26.3 (`go.mod:3`), single Go module `github.com/ateam`.
- **Task runner**: `Makefile` is the single source of truth. Key targets:
  - Build: `build`, `build-binary`, `companion` (linux/amd64), `build-binary-race`, `companion-race`, `build-all`, `build-all-race`
  - Test: `test` (race), `test-cli`, `test-docker`, `test-docker-live`, `test-all`
  - Quality: `fmt`, `fmt-check`, `lint`, `check-tidy`, `check-docs`, `vuln`
  - Aggregate: `check` (developer health check), `run-ci` (`check` + `vuln`)
  - Misc: `install-hooks`, `clean`, `docs` (regenerates `ROLES.md` from `ateam roles --docs`)
- **CI**: GitHub Actions, `.github/workflows/`:
  - `ci.yml` — single job, runs `make run-ci`, `workflow_dispatch` only
  - `docker-tests.yml` — runs `make test-docker`, `workflow_dispatch` only
  - Permissions correctly scoped to `contents: read`
- **Linter**: `golangci-lint` v2 configured via `.golangci.yml` (default standard linters, errcheck exclusions for common close/write fns, `_test.go` errcheck exclusion). Auto-installed via `go install ...@latest`.
- **Formatter**: `gofmt`. Enforced by `make fmt-check` (used in `check` and the pre-commit hook).
- **Module hygiene**: `make check-tidy` runs `go mod tidy -diff`.
- **Doc sync**: `make check-docs` regenerates `ROLES.md` from the binary and diffs against the committed copy.
- **Vuln scanning**: `make vuln` runs `govulncheck`, auto-installs at `@latest`, gracefully skips when offline/sandboxed.
- **Pre-commit hook**: `make install-hooks` writes `.git/hooks/pre-commit` running `fmt-check && check-tidy && check-docs && lint`. Not auto-installed.
- **Tests**: race-enabled unit tests + CLI shell test (`test/cli/test-auth-combos.sh`) + Docker-in-Docker integration tests (`test/Dockerfile.dind`, `test/run-docker-tests.sh`) + an optional live agent test (`test-docker-live`) requiring real API credentials.
- **Installer**: `install.sh` (bash) — checks Go ≥ 1.25 (mismatched with `go.mod`'s 1.26), installs Go via brew/tarball if missing, runs `make build`, symlinks to `$HOME/.local/bin`.
- **Other scripts**: `scripts/claude-usage.py` (Python helper for Claude usage limits, not part of build).
- **Worktree**: This repo is a git worktree (`.git` is a `gitdir:` pointer to `…/ateam/.git/worktrees/ateam-base`).
- **No previous automation report** existed under `.ateam/roles/automation/history/`.
