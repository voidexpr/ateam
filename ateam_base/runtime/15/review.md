### Project Assessment

ATeam is a small, well-curated Go CLI: tests pass with `-race`, secrets are stored at `0600` or in the OS keychain, SQL is parameterized, the web server defaults to `127.0.0.1`, and the dependency graph is short and mostly current. The material gaps are operational and concentrated: GitHub Actions workflows are `workflow_dispatch`-only so the carefully built `make run-ci` never fires automatically, `ateam serve --public/--bind` exposes every report, prompt, and stream log over the network with no authentication, the installer/`go.mod` Go-version drift can silently provision the wrong toolchain, and a few tools/deps (`chroma/v2 v2.2.0`, `golangci-lint @latest`, `govulncheck @latest`, `golang:alpine` base image) are pinned in ways that hurt reproducibility. None of the previously prioritized docs items appear in `git log` as completed, but they are out of scope for this synthesis (different reports).

### Priority Actions

**1. Require auth (or refuse to start) when `ateam serve` binds off-loopback**
- **Action**: In `cmd/serve.go` (around `:69-80`) reject `--public` or any non-loopback `--bind` unless either (a) an explicit `--auth-token <value>` flag is supplied, or (b) the server auto-generates a token at startup and prints it once on stderr. Add a small middleware in `internal/web/server.go` (alongside `securityHeaders` at `:291`) that requires `Authorization: Bearer <token>` (or `?token=<token>` for one-shot links) for every route except a static `/healthz`. Replace the easy-to-miss stderr `WARNING:` line with a refusal-to-start path; keep an opt-out flag (`--unauthenticated`) for the rare LAN-share case and document the data classes exposed (reports, prompts, full stream logs, stderr, `cmd.md`) in `ISOLATION.md`.
- **Source Role**: production_ready (2026-05-14_14-09-35), security (2026-05-14_14-09-10)
- **Source Report**: .ateam/roles/production_ready/report.md, .ateam/roles/security/report.md
- **Priority**: P0
- **Effort**: MEDIUM
- **Rationale**: Two independent roles flagged this as the single biggest exposure. The data leaked is qualitatively serious (rendered prompts and tool outputs frequently quote source and pasted secrets), the warning today is trivially missed in a tmux pane, and the change is bounded — one middleware plus a startup check.

**2. Reconcile `install.sh` Go version with `go.mod`**
- **Action**: In `install.sh:6`, derive `REQUIRED_GO_VERSION` from `go.mod` (e.g. `REQUIRED_GO_VERSION=$(awk '/^go /{print $2}' go.mod | cut -d. -f1,2)`) so the installer's "Go ≥ X found ✓" check and the auto-install Linux tarball name (`go${REQUIRED_GO_VERSION}.0.linux-${arch}.tar.gz`) both track the module. Falling back to a hard-coded `1.26` is acceptable but inferior — the two will drift again next bump.
- **Source Role**: automation (2026-05-14_14-05-53), dependencies (2026-05-14_14-06-13)
- **Source Report**: .ateam/roles/automation/report.md, .ateam/roles/dependencies/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: A user with Go 1.25 currently passes `install.sh`'s version check, then fails confusingly at `go build`; worse, the Linux auto-install path silently provisions the *wrong* version. Carryover from the prior docs review (still uncompleted in `git log`); both new reports re-surfaced it independently.

**3. Pin `golangci-lint`, `govulncheck`, and the `Dockerfile.dind` Go base image**
- **Action**: In `Makefile:109` replace `go install golang.org/x/vuln/cmd/govulncheck@latest` with a pinned `@vX.Y.Z` and do the same for `golangci-lint` at `Makefile:129`. Bump those pins via PR going forward. In `test/Dockerfile.dind:12` replace `FROM golang:alpine AS modules` with `FROM golang:1.26-alpine` (or pass the version as a build arg seeded from `go.mod`). Leave `docker:27-dind` (`:21`) — it's already pinned to a major.
- **Source Role**: automation (2026-05-14_14-05-53)
- **Source Report**: .ateam/roles/automation/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: `@latest` makes "CI suddenly went red" un-bisectable and lets upstream lint/vuln behavior changes break local builds without a repo change. One-line edits each; high reproducibility return.

**4. Enable CI on push and PR (with concurrency cancel-in-progress)**
- **Action**: In `.github/workflows/ci.yml:3-4` add `push: branches: [main]` and `pull_request:` triggers; add `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`. For `.github/workflows/docker-tests.yml:3-4` either add a `pull_request:` trigger filtered by `paths: [internal/container/**, test/**, go.mod, go.sum]`, or move it to a nightly `schedule:` if runtime cost is the concern. Keep `make run-ci` as the canonical entrypoint — no job restructuring required.
- **Source Role**: automation (2026-05-14_14-05-53), production_ready (2026-05-14_14-09-35)
- **Source Report**: .ateam/roles/automation/report.md, .ateam/roles/production_ready/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Both reports flag the same gap — the careful `make run-ci` plumbing only fires when a human clicks "Run workflow". Recent commits `2c732b8` and `f993a50` re-tuned the automation role to call missing CI "MEDIUM at most, not HIGH" for a single-developer project, so I'm honoring that calibration with P1 (not P0). The fix is one-line, and the project already accepts agent-authored commits which makes the safety net more valuable than it would be for a hand-edited repo.

**5. Bump `github.com/alecthomas/chroma/v2` from `v2.2.0` to the current `v2.24.x`**
- **Action**: In `go.mod:7` bump `alecthomas/chroma/v2` to the latest `v2.24.x`, run `go mod tidy`, and visually smoke-test the few code blocks rendered by `internal/web/markdown.go:7` (review/verify pages, code-session detail). The v2 API has been stable; expect a drop-in. Optionally bump `modernc.org/sqlite v1.47.0 → v1.50.x` in the same PR — it backs `state.sqlite` and `calldb`, has its own tests, and is security-adjacent.
- **Source Role**: dependencies (2026-05-14_14-06-13)
- **Source Report**: .ateam/roles/dependencies/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Three years (~22 minor releases) of chroma fixes/lexers, all behind a stable API. Largest version-lag in the dependency graph and the only one that meaningfully matters.

**6. Plug the `dirName` path-traversal gap in `handleCodeSessionFile` and `handleCodeSessionDetail`**
- **Action**: In `internal/web/handlers.go:1373-1414` (`handleCodeSessionFile`) and `:1301` (`handleCodeSessionDetail`), after building `canonical`, assert `isPathWithin(canonical, filepath.Join(pe.ProjectDir, "supervisor", "code"))` before any `os.Stat`/read. The existing `fileName` validation (`.md` suffix, no `/`, no `..`) is correct; the gap is that `dirName` passes through unchecked, so `dirName="../.."` resolves to arbitrary `.md` files anywhere under `pe.ProjectDir`. Mirror the pattern already used by `handleSupervisorHistory`, `handleReportHistory`, and `serveHistoryFile`.
- **Source Role**: production_ready (2026-05-14_14-09-35)
- **Source Report**: .ateam/roles/production_ready/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Bounded but real bypass of the route's intent; impact grows materially once Action #1 lands and serving off-loopback becomes a supported mode.

**7. Resolve symlinks in `isPathWithin` callers + tighten new `.ateam/` directory perms to `0700`**
- **Action**: Two related changes. (a) In `internal/web/handlers.go:651-656`, after computing `absPath`, run `filepath.EvalSymlinks` (and on `baseDir`) and re-apply the prefix check; reject if `EvalSymlinks` fails for an existing file. Update callers `handleRunFile`, `handleCodeSessionFile`, `serveHistoryFile`. (b) In `internal/root/init.go:37, 43, 97, 103, 108, 167, 170, 180, 184` switch `MkdirAll` perms from `0755` to `0700`, matching what `internal/runner/runner.go:305, 309` and `internal/secret/store.go` already do. Together these close the symlink-escape and shared-host metadata-leak surfaces.
- **Source Role**: security (2026-05-14_14-09-10)
- **Source Report**: .ateam/roles/security/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Each is a few-line change, and they interact: with Action #1 enabled, symlink containment matters more; even without, `0700` is the right default for files containing rendered prompts and `cmd.md`. Bundle to avoid two PRs over the same `.ateam/` plumbing.

**8. Add a minimal `.github/dependabot.yml` (gomod + github-actions, weekly)**
- **Action**: Create `.github/dependabot.yml` with two `package-ecosystem` blocks: `gomod` (weekly, schedule on Monday) and `github-actions` (weekly). One file, ~15 lines. No source changes required.
- **Source Role**: automation (2026-05-14_14-05-53), dependencies (2026-05-14_14-06-13)
- **Source Report**: .ateam/roles/automation/report.md, .ateam/roles/dependencies/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Replaces the `chroma/v2` and `actions/setup-go` ageing problems with mechanical PRs going forward — the deterministic-tool-over-recurring-LLM-audit play. Pairs naturally with Action #5.

**9. Add graceful shutdown to `ateam serve` and stop panicking from `mustGetwd`**
- **Action**: Two small reliability fixes. (a) In `cmd/serve.go:44-80` / `internal/web/server.go:281-289`, wrap with `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` and call `srv.Shutdown(ctx)` on cancellation; ensure `pe.db` is closed via `Server.Close()` before returning. Other long-running commands (see `cmd/table.go:33`) already follow this pattern. (b) In `internal/root/resolve.go:451-457`, change `mustGetwd` to return an error and propagate through `root.Lookup` so a missing/stale CWD surfaces as the standard `Error: …` exit-1, not a panic + exit-2. Leave the embed-resource panics in `internal/config/config.go:23-27` and `internal/prompts/embed.go:178` alone — they only fire on a broken binary.
- **Source Role**: production_ready (2026-05-14_14-09-35)
- **Source Report**: .ateam/roles/production_ready/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: `serve` is the outlier among long-running commands; the panic path is the one runtime-reachable case among several `must…` callsites. Both are obvious-once-flagged hygiene fixes.

**10. Verify the Go tarball SHA-256 in `install.sh`**
- **Action**: In `install.sh:54-60`, before `tar -C /usr/local -xzf`, fetch `https://go.dev/dl/?mode=json` to look up the published SHA-256 for the resolved version+arch (or hard-code it next to `REQUIRED_GO_VERSION` and bump in lockstep) and verify with `sha256sum -c`. Fail the install on mismatch. The script already runs `tar` under `sudo`, which makes a tampered toolchain especially sharp.
- **Source Role**: security (2026-05-14_14-09-10)
- **Source Report**: .ateam/roles/security/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Low likelihood, high blast radius (the unverified toolchain builds every subsequent `ateam` binary). Naturally done in the same PR as Action #2 since both touch `install.sh`'s Go-version handling.

### Deferred

- **CSP `unsafe-inline` for scripts/styles** (production_ready Finding 3, security Finding 6): goldmark already escapes raw HTML and chroma writes only escaped output, so this is defense-in-depth, not an active vector. The fix is a non-trivial template refactor (extract inline `<script>`/`<style>` to `static/` or move to per-response nonces) and the security report explicitly cautions against changing CSP without runtime verification. Reconsider when the inline block is touched for another reason.
- **Add `test-cli` to `make check` / `run-ci`** (production_ready Finding 7): worth doing eventually, but `test-cli` exercises live auth resolution and may want isolation/credential-stub work first; safer to enable it together with Action #4 once CI triggers exist and any flake surface is visible.
- **`make test-quiet` summarizer for role token budget** (automation Finding 8): real ergonomic improvement but a product/UX choice (which summary format, which target name); defer until a role consuming this signals it's painful.
- **`make help` self-documenting target** (automation Finding 9): pure ergonomics; the Makefile is short and discoverable. Add when a contributor asks.
- **Tighten `make vuln`'s offline heuristic** (automation Finding 10): real correctness concern (real CVE could be masked if `fetching vulnerabilities` appears in the failure path), but currently theoretical and lower-impact than enabling CI itself. Pair with the `govulncheck` pin in Action #3.
- **`golang.org/x/*` rebase + Go-toolchain matrix confirmation** (dependencies Findings 3 and 4): the indirect `x/*` set is current, and the Go 1.26.3 floor matches the dev/CI toolchain; let `go mod tidy` propagate during the next chroma/sqlite bump.
- **Validate container/secret-store names against `[A-Za-z0-9_.-]`** (security Finding 5): self-attack surface; fix opportunistically when the next change touches `internal/container/docker_exec.go`.
- **Single-job CI caching for `~/.cache/golangci-lint` and the `govulncheck` binary** (automation Finding 7): cache wiring is most useful *after* CI runs automatically; revisit once Action #4 lands and run cost is observable.
- **Auto-install `make install-hooks` from `install.sh` / advertise in `DEV.md`** (automation Finding 5): low-impact ergonomics; revisit only if pre-commit failures show up in practice.
- **Nits**: rename `mustGetwd` callers consistently after Action #9; small docstring tidy on `securityHeaders`.

### Conflicts

No direct contradictions. The four reports converged independently on the two largest items:
- `ateam serve --public` lacking auth was rated **HIGH** by production_ready and **MEDIUM** by security — production_ready emphasizes the surface (full reports/logs/prompts), security emphasizes that it's opt-in and warns of tampering risk only on shared LAN. I resolved at **P0** because the data classes exposed are the more decisive factor.
- The CI-trigger gap was **HIGH** in automation and **MEDIUM** in production_ready. The project's own role calibration (`2c732b8 project.automation: missing CI is MEDIUM at most, not HIGH`) explicitly downranks this for a single-developer project, so I resolved at **P1**, not P0 — honoring the project's policy signal even though one role still labels it HIGH.

### Notes

- Recent commits (`2c732b8`, `f993a50`, `aa3ed7e`) show the project actively re-tuning role severity calibration; that history is the right anchor for the CI-priority decision above.
- The previous supervisor review (`.ateam/supervisor/review.md`) was driven by the docs roles and is orthogonal to this synthesis. Its install.sh / `go.mod` Go-version item (Action #2 there) re-surfaces here from automation + dependencies and is preserved as P0; the docs-specific items are not duplicated and remain in their own review.
- Three of the priority actions (`golangci-lint`/`govulncheck` pin, Dependabot, chroma bump) are all "stop chasing version drift by hand" and pair naturally — landing Action #3 and Action #8 together would shrink the dependency role's future findings to noise.
- The web-handlers cluster (Actions #1, #6, #7) all touch `internal/web/handlers.go` plumbing. Sequencing: land Action #1 first (gates the surface), then Action #6 (tighten the bypass that Action #1 amplifies), then Action #7 (symlink + perms hardening that benefits from both).
- No Conflicts section needed beyond the two severity reconciliations above; the reports are unusually well-aligned.
