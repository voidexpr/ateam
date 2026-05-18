# Summary

The Go module graph is small and well-curated: ten direct dependencies, no vendor folder, lock file (`go.sum`) committed and in sync. Overall dependency health is good. The main concern is a substantially outdated `github.com/alecthomas/chroma/v2` (project pinned to v2.2.0 from 2022 while v2.24.x is available); everything else is at or very near current. No known CVEs in the current dependency set were identified from local data, and all direct dependencies are actually imported in code.

# Role performing the audit

- Role: project.dependencies
- Model: claude-opus-4-7 (Opus 4.7), default thinking
- Mode: read-only static analysis from `go.mod` / `go.sum` and the local module cache (`$GOPATH/pkg/mod/cache/download`); network egress to `proxy.golang.org` was blocked by TLS, so version recency was cross-checked against locally cached version listings only

# Findings

## 1. `github.com/alecthomas/chroma/v2` is pinned to a 2022 release
- **Location**: `go.mod:7`, used in `internal/web/markdown.go:7`
- **Severity**: MEDIUM
- **Effort**: SMALL (non-breaking within the v2 line)
- **Description**: The project depends on `chroma/v2 v2.2.0` (released Aug 2022) for syntax highlighting in the embedded web markdown renderer. Locally cached versions in `$GOPATH/pkg/mod/cache/download` go up to `v2.24.1`, meaning the project trails by ~22 minor releases and roughly three years of bug fixes, new lexers, and CSS/HTML output adjustments. Chroma has maintained a stable v2 API, so the upgrade should be a drop-in. Staying on a long-abandoned point release keeps known parser bugs and missing language support in place and also forces `github.com/dlclark/regexp2` to stay on the equally old `v1.7.0`.
- **Recommendation**: Bump `chroma/v2` to the latest `v2.24.x` (or whatever `go list -m -u` resolves once network access is available). Re-build and visually smoke-test the web markdown rendering for the few code blocks the project actually renders.

## 2. `modernc.org/sqlite` trails latest by a few minor releases
- **Location**: `go.mod:15`, used in `internal/calldb/calldb.go`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Project uses `modernc.org/sqlite v1.47.0`; local cache lists up to `v1.50.0`. `modernc.org/libc` is at `v1.70.0` (latest cached). The pure-Go SQLite implementation is security-sensitive (it backs `state.sqlite` for runtime/agent state and `calldb`) but no specific CVE is known for the current pin. Lag is modest.
- **Recommendation**: Plan a routine bump to `v1.50.x`; verify with `make test` and `make test-docker` since calldb has its own tests (`internal/calldb/calldb_test.go`).

## 3. `golang.org/x/*` modules are recent but worth a periodic rebase
- **Location**: `go.mod:36-40` (indirect: `mod v0.34.0`, `sync v0.20.0`, `sys v0.43.0`, `text v0.36.0`, `tools v0.43.0`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: All `golang.org/x/*` packages are listed as **indirect** in `go.mod`, but they appear nowhere in source under the project (`grep` shows only `go.mod` / `go.sum` matches). They are pulled in transitively (notably by `hashicorp/hcl/v2` and `modernc.org/sqlite`). Locally cached sys listings extend to `v0.43.0` (matches), text to `v0.36.0` (matches). The set is current; the only reason to act here is the standard practice of letting these track upstream so transitive callers get current builds.
- **Recommendation**: No action now. Re-check during the next chroma/sqlite bump and let `go mod tidy` propagate.

## 4. Go toolchain pin is very fresh — confirm CI/build matrix
- **Location**: `go.mod:3` — `go 1.26.3`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The module requires `go 1.26.3`. This is the current toolchain on the dev machine (`go version go1.26.3 darwin/arm64`) and is fine, but it means any contributor / CI runner pinned to 1.25 or earlier will not be able to build. Worth a brief confirmation that release pipelines and Docker base images for `make test-docker` provide Go ≥ 1.26.3.
- **Recommendation**: Verify the Docker test image and any release/CI scripts use Go ≥ 1.26.3. If they pin a lower version, either bump them or relax the `go` directive to the lowest version that compiles cleanly.

## 5. Automated vulnerability scanning is not visible in the project
- **Location**: repository root (no `govulncheck`, `dependabot.yml`, `renovate.json`, or workflow that runs them)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: There is no committed configuration for `govulncheck` or a dependency-update bot. Given the small dependency surface and conservative bump policy, the absence is not alarming, but periodic CVE checks would be cheap insurance — especially for `modernc.org/sqlite`, `zalando/go-keyring`, and `hashicorp/hcl/v2`, all of which touch security-adjacent paths (local DB, OS keychain, configuration parsing).
- **Recommendation**: Add a single CI step (or `make vuln` target) that runs `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` and fails on findings. This needs network access to the GO vuln DB; document it as an opt-in check if offline builds must remain supported.

## Confirmed clean (no action)

- All ten direct dependencies are actually imported somewhere in the codebase (verified via grep): `BurntSushi/toml`, `alecthomas/chroma/v2`, `hashicorp/hcl/v2`, `mattn/go-isatty`, `spf13/cobra`, `vbauerster/mpb/v8`, `yuin/goldmark`, `zalando/go-keyring`, `zclconf/go-cty`, `modernc.org/sqlite`.
- Indirect entries that only show up in `go.mod`/`go.sum` (e.g. `google/go-cmp`, `google/uuid`, `mitchellh/go-wordwrap`, `dustin/go-humanize`, `ncruces/go-strftime`) are pulled by transitive dependencies — they are correctly marked `// indirect` and there is no unused-direct-dep cleanup to do.
- `go.sum` is committed (115 lines) and matches `go.mod` (`go mod graph` succeeded offline using the local cache).
- No duplicated functionality: one CLI framework (cobra), one TOML parser, one HCL parser, one markdown renderer, one syntax highlighter, one progress bar, one SQLite driver, one keyring.
- License surface is mainstream (MIT / BSD / Apache-2.0 / MPL-2.0 for HCL) — no copyleft or restrictive licenses among the direct deps.

# Quick Wins

1. **Bump `chroma/v2` from `v2.2.0` to the latest `v2.24.x`** (Finding 1) — single line in `go.mod`, three years of upstream fixes, very low risk inside the v2 line.
2. **Bump `modernc.org/sqlite` from `v1.47.0` to `v1.50.x`** (Finding 2) — security-relevant module backing local state DB, small jump, test coverage exists in `internal/calldb/`.
3. **Add a `govulncheck` make target / CI step** (Finding 5) — one new target, no source changes, gives ongoing CVE signal across all transitive deps.

# Project Context

- **Language / toolchain**: Go (`go.mod` declares `go 1.26.3`). Single module rooted at `github.com/ateam`. No `vendor/` directory; deps resolved through the module cache.
- **Manifest / lock**: `go.mod` (45 lines, 10 direct + 26 indirect), `go.sum` (115 lines, committed). Lock-in-sync verified via `go mod graph`.
- **Direct dependencies and their usage sites**:
  - `BurntSushi/toml` → `internal/config/config.go`
  - `alecthomas/chroma/v2` → `internal/web/markdown.go`
  - `hashicorp/hcl/v2` + `zclconf/go-cty` → `internal/runtime/config.go`
  - `mattn/go-isatty` → `cmd/table.go`
  - `spf13/cobra` → all `cmd/*.go` (CLI framework, primary surface)
  - `vbauerster/mpb/v8` → `cmd/pool_render_mpb.go` (parallel run progress bars)
  - `yuin/goldmark` → `internal/web/server.go`, `internal/web/markdown.go`
  - `zalando/go-keyring` → `internal/secret/store.go` (OS keychain for credentials)
  - `modernc.org/sqlite` → `internal/calldb/calldb.go` (pure-Go SQLite for `state.sqlite` and call-tracking)
- **Build / test commands** (from `CLAUDE.md`): `make build`, `make test`, `make test-docker` (the last is mandatory after container/agent/runner-related changes — relevant for sqlite/keyring upgrades).
- **Network constraint observed during this audit**: `proxy.golang.org` was unreachable due to TLS verification failure on this machine; remote `go list -m -u all` and module-retraction lookups failed. All version comparisons in this report are against the local module cache (`$GOPATH/pkg/mod/cache/download/`), so "latest cached" is a lower bound on what's actually published.
- **No previous dependency report**: `.ateam/roles/project.dependencies/history/` and `.ateam/roles/dependencies/history/` are both empty, so there are no unresolved prior findings to re-include.
- **Recommended automation**: `golang.org/x/vuln/cmd/govulncheck` for CVEs; Dependabot or Renovate (Go ecosystem support) for low-noise periodic version PRs — both standard for Go projects of this size.
