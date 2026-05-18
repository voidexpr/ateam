# Summary

ATeam is a developer-machine CLI; its only network-facing surface is `ateam serve`, which binds to `127.0.0.1` by default. Test suite passes cleanly with `-race`, no hardcoded secrets are present in the source tree, and secret storage uses the OS keychain or a 0600-mode `.env` file. The most material gaps are operational: CI workflows are configured for manual dispatch only (so failing tests can land), the `--public` serve mode exposes reports and run logs over the network without any authentication, and the web markdown viewer relaxes CSP with `unsafe-inline` while serving model-generated content.

# Findings

## CI workflows only run via manual dispatch — failing tests can land on `main`

- **Location**: `.github/workflows/ci.yml:3-4`, `.github/workflows/docker-tests.yml:3-4`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Both workflows are defined with `on: workflow_dispatch` only. There is no `push:` or `pull_request:` trigger, so `make run-ci` (tests, lint, govulncheck, fmt-check, docs check) never runs automatically on commits or PRs. The `make check` target is wired up correctly and the Makefile-level `install-hooks` provides a local pre-commit hook, but neither is enforced on the remote. For a project that intentionally accepts agent-authored commits (ateam itself runs `code`/`verify` and commits) this is the primary safety net and is currently disabled by default.
- **Recommendation**: Add `push: branches: [main]` and `pull_request:` triggers to `ci.yml`. Consider keeping `docker-tests.yml` on `workflow_dispatch` if runtime is too long, but at minimum run it nightly via `schedule:`.

## `ateam serve --public` exposes reports and run logs without authentication

- **Location**: `cmd/serve.go:40-78`, `internal/web/server.go:230-289`
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: `--public` (and `--bind <addr>`) bind the HTTP server to a non-loopback interface with no auth layer. The server exposes `/p/<project>/runs/{id}/{exec|prompt|output|logs|stderr}` which surface prompts, full agent stream logs, stderr, and review/verify documents — content that often quotes source code, secrets in error messages, and internal directory structure. The only deterrent is a stderr `WARNING:` line printed on startup, which is trivially missed when the command is launched in a tmux pane or background. There is no token, password, or bind-time confirmation.
- **Recommendation**: At minimum, require a token (auto-generated, printed once) when `--public` or a non-loopback `--bind` is used, mounted as a `Authorization: Bearer …` or query-param check in `securityHeaders`. Alternatively refuse to start when binding off-loopback unless `--unauthenticated` is also passed. Document the threat clearly in `ISOLATION.md` (currently only `README.md` mentions isolation).

## Web UI CSP allows `unsafe-inline` for scripts and styles

- **Location**: `internal/web/server.go:291-298`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: `Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'`. The server renders user-controlled markdown produced by LLM agents and stored on disk (reports, review/verify, code prompts). Goldmark is not configured with `WithUnsafe`, so raw HTML in markdown is escaped — that's the primary mitigation — but `unsafe-inline` removes the defense-in-depth that would otherwise block an inline `<script>` if a future change either enabled raw HTML, surfaced unescaped content via a template, or added a feature that takes user-supplied query params and reflects them. Inline JS is not actually used anywhere in `internal/web/templates/` (a quick scan shows only one inline `<style>` in `layout.html`); the project can move to nonces or `'strict-dynamic'` with modest effort.
- **Recommendation**: Move any required inline styles into `static/` and drop `'unsafe-inline'`. If a small inline block remains, use a per-response nonce.

## Path traversal soft-guard in `handleCodeSessionFile` only constrains to project dir

- **Location**: `internal/web/handlers.go:1373-1414`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `dirName := r.PathValue("session")` is used to build `canonical = projectDir/supervisor/code/<dirName>`. `fileName` is validated (`.md` suffix, no `/`, no `..`), but `dirName` is not. With `dirName = "../.."` the `os.Stat(canonical)` and `isPathWithin(absPath, pe.ProjectDir)` checks both pass for arbitrary `.md` files anywhere inside the project tree. The other history/run handlers (`handleSupervisorHistory`, `handleReportHistory`, `serveHistoryFile`) correctly scope to a specific subdir via `isPathWithin(path, histDir)`. Impact is bounded — only files under `pe.ProjectDir` and only ones ending in `.md` — but it bypasses the intent of the route. Same handler signature is used by `handleCodeSessionDetail` (`internal/web/handlers.go:1301`) with the same gap.
- **Recommendation**: After building `canonical`, assert `isPathWithin(canonical, filepath.Join(pe.ProjectDir, "supervisor", "code"))` before any read; do the same for `runtimeDir` against `filepath.Join(pe.ProjectDir, "runtime")`.

## `mustGetwd` panics on `os.Getwd` failure

- **Location**: `internal/root/resolve.go:451-457`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `mustGetwd` panics if `os.Getwd()` fails (deleted CWD, permission-loss after `chdir`, container teardown). This is reachable from CLI entry through `root.Lookup`. A panic prints a stack trace and exits with 2 instead of the project's normal `Error: …` + exit-1 path in `main.go:11-20`. Embedded-resource panics in `internal/config/config.go:23-27` and `internal/prompts/embed.go:178` only fire if the binary was built broken and are acceptable; `mustGetwd` is the runtime-reachable one.
- **Recommendation**: Return an error from `mustGetwd` and propagate it through `root.Lookup`, so a missing/stale CWD surfaces as the standard `Error:` exit.

## `ateam serve` has no graceful shutdown

- **Location**: `internal/web/server.go:281-289`, `cmd/serve.go:44-80`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `srv.Serve(ln)` is called directly; there is no `signal.NotifyContext` wrapper and no `srv.Shutdown(ctx)` on SIGINT/SIGTERM. Read/Write/Idle timeouts are correctly set (`server.go:283-287`), so the worst case is in-flight HTML renders being cut. Other long-running ateam commands wrap context with `signal.NotifyContext` (see `cmd/table.go:33`) — `serve` is the outlier.
- **Recommendation**: Wrap with `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` and call `srv.Shutdown(ctx)` on cancellation; close `pe.db` via existing `Server.Close()` before returning.

## Test isolation: `make test-cli` shell script runs against live CLI binary

- **Location**: `Makefile:66-67`, `test/cli/test-auth-combos.sh`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `test-cli` runs only after an explicit `make test-cli` and is not part of `make check` or `make run-ci`. It exercises auth resolution end-to-end, which is a frequent source of regressions (the project tracks `claude_auth.go` precedence carefully). The default CI path therefore misses an entire layer of integration tests.
- **Recommendation**: Add `test-cli` to the `check` target (or to `run-ci` so CI exercises it once CI triggers are restored).

# Quick Wins

1. **Enable automatic CI triggers** — add `push:` and `pull_request:` to `ci.yml`. One-line edit, biggest single safety improvement. (`.github/workflows/ci.yml:3-4`, MEDIUM, SMALL)
2. **Validate `dirName` against `supervisor/code/`** in `handleCodeSessionFile` and `handleCodeSessionDetail` — one extra `isPathWithin` call per handler. (`internal/web/handlers.go:1308`, `:1380`, LOW, SMALL)
3. **Refuse non-loopback bind without a token flag** — reject `--public` / off-loopback `--bind` unless a token (auto-printed) is configured, replacing the easy-to-miss stderr warning. (`cmd/serve.go:69-78`, HIGH, MEDIUM — closer to SMALL once the token path exists)
4. **Add graceful shutdown to `ateam serve`** — `signal.NotifyContext` + `srv.Shutdown`. (`internal/web/server.go:281`, LOW, SMALL)
5. **Replace `mustGetwd` panic with a returned error** — handful of call sites. (`internal/root/resolve.go:451`, LOW, SMALL)

# Project Context

- **Type**: Go CLI tool (`go 1.26.3`, single binary `ateam`). Not a deployed network service. The only HTTP surface is `ateam serve`.
- **Entry points**: `main.go` → `cmd.Execute` (cobra). Long-running commands wrap context via `signal.NotifyContext` (see `cmd/table.go:33`); `cmd/serve.go` is the exception.
- **Web server**: `internal/web/server.go`. Routes registered at `:240-258`. Static + template content embedded via `//go:embed`. Security headers in `securityHeaders` (`:291`). Timeouts set (`:283-287`).
- **Auth/secrets**: `internal/secret/` (FileStore at `0600`, OS keychain via `zalando/go-keyring`). Auth resolution + env precedence in `internal/agent/claude_auth.go`. No secrets in tree.
- **Runtime profiles & isolation**: `defaults/runtime.hcl` defines `claude_sandbox` Seatbelt/bubblewrap profile and the docker profile. Project-level overrides at `.ateam/runtime.hcl`. `internal/container/` handles docker exec/one-shot.
- **CI**: `.github/workflows/ci.yml` and `docker-tests.yml` — both `workflow_dispatch` only. `make run-ci` is the canonical CI target: `test fmt-check check-tidy check-docs lint vuln`. `govulncheck` is auto-installed.
- **Tests**: `go test -race ./...` passes (17 packages, no failures, no skipped tests outside of `testing.Short()` and platform guards). CLI integration tests in `test/cli/test-auth-combos.sh` (not in default `check`). Docker-in-Docker tests in `test/Dockerfile.dind`.
- **Markdown rendering**: `internal/web/markdown.go` — `goldmark.New(extension.GFM, chromaExtension)` without `WithUnsafe`, so raw HTML in markdown is escaped.
- **Path-traversal helper**: `isPathWithin` in `internal/web/handlers.go:651` with thorough test coverage in `is_path_within_test.go`. Used by most handlers; `handleCodeSessionFile`/`handleCodeSessionDetail` underuse it.
- **Tooling already automated**: `govulncheck`, `golangci-lint`, `gofmt -l`, `go mod tidy -diff`, docs regen check.
