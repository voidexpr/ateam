# Summary

The project's dependency surface is small (10 direct Go modules) and healthy: no archived upstreams, no GitHub advisory database hits against any direct dependency, and every declared direct dependency is actually imported. The audit environment remains partially blocked — `proxy.golang.org` returns a TLS verification failure (`x509: OSStatus -26276`) when the Go toolchain attempts to reach it from this sandbox, and `api.osv.dev` is allowlist-blocked at egress — so version-currency claims (`go list -m -u all`) and call-graph reachability vuln scans (`govulncheck`) cannot be verified locally and must be run in CI; per role policy, version-currency findings are NOT filed in that condition.

# Role

- Role: `project.dependencies`
- Model: claude-opus-4-7 (default reasoning, no extended-thinking flag).
- Working tree: clean at commit `6dcf9a0` ("prompts: fix three internal contradictions surfaced in review"). `go.mod` / `go.sum` unchanged since the previous run.
- Methodology: read `go.mod` / `go.sum`; enumerated direct deps; verified each is imported by `git grep` across `*.go`; (in prior run) queried `api.github.com` for repository archived-flag and `pushed_at` for each direct dep; (in prior run) queried the GitHub Advisory Database (`/advisories?ecosystem=go&affects=…`) for each direct dep; (in prior run) grepped local module cache (`~/go/pkg/mod/...@vX`) for `// Deprecated:` markers and cross-checked against actual usage in this project.
- Audit blockers confirmed this run: `go list -m -u all` fails with `tls: failed to verify certificate: x509: OSStatus -26276` against `proxy.golang.org` for every module; `api.osv.dev` returns no response (egress allowlist). `govulncheck` install + run therefore still not possible from this sandbox.
- Prior reports: previous `project.dependencies` report (2026-05-14_14-08-14, ~2h ago) had one LOW finding (CI gap for `govulncheck`); no code or manifest change since. That finding is carried forward.

# Findings

## 1. Run `govulncheck` in CI; the local sandbox cannot reach the advisory database

- **Title**: Vulnerability scan was not executable in this sandbox; needs a CI run for ground truth
- **Location**: project root (`go.mod`); CI configuration would go in `Makefile` (`make test` target) or whatever harness publishes the agent's CI.
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The conservative-mode dependency audit relies on `govulncheck ./...` to confirm that known CVEs in transitive dependencies are not reached on the call graph. From this sandbox, the Go toolchain cannot complete TLS to `proxy.golang.org` (so `go install golang.org/x/vuln/cmd/govulncheck@latest` and `go list -m -u all` both fail), and `api.osv.dev` is allowlist-blocked at the egress proxy. Neither this report nor a future run from this sandbox can verify reachability of any CVE. The GitHub Advisory Database query (reachable in the prior run) returned zero advisories matching any of the ten direct dependencies at the queried versions, so there is no known-public CVE on a direct dep — but transitive call-graph reachability is unverified.
- **Recommendation**: Add a CI step (or `make` target) that runs `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...` and fails the build on any reported vulnerability. This belongs to the `project.automation` role to wire up; flagging it here so the next dependency-role run from a network-restricted environment knows the gap exists. Do NOT pin the tool version — `govulncheck` is intentionally always-latest because its advisory data ships with the binary.

# Quick Wins

None — there is no SMALL-effort, MEDIUM+-severity work pending in this role's scope. The single finding above is correctly LOW (it documents an environmental blocker for the next run, not a defect in the codebase).

# Project Context

- **Language / build**: Go module `github.com/ateam`, `go 1.26.3` toolchain directive in `go.mod`. Single binary CLI (`main.go` → `cmd/`). No vendor directory; module-aware build via `make build`.
- **Direct dependencies** (10, all imported, all upstream-active per `api.github.com` as of last reachable check 2026-05-14):
  | Module | Version in go.mod | Upstream | Last push (UTC) | Used in |
  |---|---|---|---|---|
  | github.com/BurntSushi/toml | v1.6.0 | active | 2026-04-15 | `internal/config/config.go`, `internal/runtime/config.go` |
  | github.com/alecthomas/chroma/v2 | v2.2.0 | active | 2026-05-14 | `internal/web/markdown.go` (only `quick.Highlight`) |
  | github.com/hashicorp/hcl/v2 | v2.24.0 | active | 2026-05-14 | `internal/runtime/config.go` (gohcl, hclparse) |
  | github.com/mattn/go-isatty | v0.0.20 | active | 2026-04-27 | many `cmd/*.go` (TTY detection) |
  | github.com/spf13/cobra | v1.10.2 | active | 2026-04-25 | every command in `cmd/` |
  | github.com/vbauerster/mpb/v8 | v8.12.0 | active | 2026-05-06 | `cmd/pool_render_mpb.go` |
  | github.com/yuin/goldmark | v1.8.2 | active | 2026-03-25 | `internal/web/markdown.go` |
  | github.com/zalando/go-keyring | v0.2.8 | active | 2026-04-07 | `internal/secret/store.go` |
  | github.com/zclconf/go-cty | v1.16.3 | active | 2026-04-16 | `internal/runtime/config.go` |
  | modernc.org/sqlite | v1.47.0 | active (on gitlab; gh mirror archived as redirect-only stub) | — | `internal/calldb/*.go`, supervisor state |
- **Abandonment check**: zero direct deps are archived on their canonical upstream. The `github.com/cznic/sqlite` GitHub repo is archived (2018) but only as a redirect-stub pointing at `modernc.org/sqlite`; the live repo is on gitlab and actively released. Not a finding.
- **Deprecated-API check**: grep of `~/go/pkg/mod/...@vX` for `// Deprecated:` markers on the directly-imported packages found hits in `cobra` (`SetOutput`, `ExactValidArgs`) and `goldmark` (legacy `ast.*.Text` accessors and several `util.*` helpers). Cross-checked against this project's source: **none of the deprecated symbols are used** — the project uses `SetOut`/`SetErr` already, does not call `ExactValidArgs`, and the goldmark renderer in `internal/web/markdown.go:39` uses `n.Lines()` + `Segment.Value`, which are the current APIs. Not a finding.
- **CVE check**: `api.github.com/advisories?ecosystem=go&affects=<module>` returned zero advisories for every direct dependency at the queried module-path level (last reachable run, 2026-05-14). OSV.dev (which gives version-resolved hits) remains blocked by the egress allowlist, so this is a necessary but not sufficient check; the proper check is `govulncheck` in CI (finding 1).
- **Unused-dep check**: every direct module in `go.mod`'s `require` block has at least one `import` in `*.go`. No unused direct dependencies.
- **License check**: deferred — `go-licenses` requires reachable `proxy.golang.org` (TLS fails). The project distributes a single CLI binary; the dependency set is dominated by MIT / BSD / MPL-2.0 packages by reputation, with no known GPL/AGPL surface, but this should be confirmed in CI when the network is reachable.
- **Major-version gaps**: chroma is at `v2.2.0`; the module cache shows `v2.24.1` available locally, so the gap is real and large. No finding is filed: per role policy this is currency-only with no abandonment, no CVE, and no other-role blocker citing it. Note for the next run: if `code.bugs` or a security-role finding lands that traces a syntax-highlighting issue or chroma-specific bug, revisit.
- **Files to look at first in future runs**: `go.mod`, `go.sum`, `internal/web/markdown.go` (chroma + goldmark), `internal/secret/store.go` (keyring), `internal/calldb/` (sqlite), `internal/runtime/config.go` (hcl + cty + toml), `cmd/*.go` (cobra surface).
- **Tools to run when network is unblocked**: `govulncheck ./...`, `go list -m -u all`, `go-licenses report github.com/ateam`. Re-verify the chroma gap and any deferred CVE work then.
