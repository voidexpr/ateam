# Summary

ATeam is a CLI tool distributed as a Go binary; it has no production deployment, no dev/staging/prod boundary, no service-side resources, no databases beyond the local SQLite state file, and no remote infrastructure. The traditional "agent wiped my prod DB" risks (Lens 2) are mitigated by design: the tool's reason for existing is to wrap unattended agents in OS sandboxes / Docker, secrets live in the OS keychain by default, `.env`/`secrets.env`/`.ateam/` are gitignored, and `serve` is localhost-only with an explicit warning when bound publicly. Two LOW items worth noting around the install path and shared infrastructure used by tests.

# Findings

## install.sh wipes /usr/local/go without confirmation

- **Title**: `install.sh` performs `sudo rm -rf /usr/local/go` without prompting on Linux
- **Location**: `install.sh:58`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The Linux branch of `install_go()` unconditionally runs `sudo rm -rf /usr/local/go` before extracting a freshly downloaded Go toolchain. A user who already has a working Go install at that prefix (e.g. installed via distro package or a different version they pinned) will have it destroyed silently. This is a "dev-environment safety" gap: the install script is the first thing a new operator runs, and it makes an unprompted destructive system change against a path the operator may not even realize ateam touches. The user-visible script output ("Installing Go…") does not warn that an existing Go tree at `/usr/local/go` will be removed.
- **Recommendation**: Before the `rm -rf`, detect whether `/usr/local/go` exists, print the resolved target path explicitly ("about to remove existing Go install at /usr/local/go"), and require either an interactive confirmation or an explicit `--reinstall-go` / `INSTALL_GO=force` flag. If `/usr/local/go` already contains the required version, skip the install entirely. The macOS branch already delegates to Homebrew and is fine.

## Shared test/build artefacts use a single Docker tag

- **Title**: `make test-docker` / `test-docker-live` build into the same fixed image tag `ateam-test-dind` regardless of branch / commit
- **Location**: `Makefile:75-101`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Both docker-integration test targets build into the tag `ateam-test-dind`. Running them concurrently from two checkouts (the legitimate ateam pattern is the `candidate` worktree alongside the main worktree — confirmed by this very project's directory name) causes the second build to clobber the first build's image while the first run is still using it, producing nondeterministic results. Frame this as deploy-reproducibility-shaped: the artefact identity does not capture which source produced it. Not a prod-credential leak (the image is built locally and consumed locally), but it's the kind of shared-namespace pattern the role's Lens 1 watches for.
- **Recommendation**: Suffix the test image tag with a per-checkout hash (e.g. `ateam-test-dind:$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)`, or a hash of `$(CURDIR)`). Document the convention briefly in the Makefile header. Optional: add `--pull=false` to the `docker run` so a partially-overwritten image doesn't get pulled.

## No findings against Lens 3 (production deployment readiness)

ATeam does not deploy to a server; it ships as a binary via `install.sh` and `go install`. The classic Lens 3 surface — debug routes in prod, missing graceful shutdown for an HTTP service, public-facing TLS / health checks, prod log retention, etc. — does not apply here. The one HTTP surface (`ateam serve`) is a local read-only browser for reports; it defaults to `127.0.0.1`, prints a network-exposure warning if `--bind` or `--public` is used (`cmd/serve.go:76-78`), and is explicitly documented as a local tool. No finding.

## No findings against Lens 1 (env separation) for ateam itself

ATeam has a single environment: the developer's host (or a Docker-isolated derivative of it). Configuration scopes (project → org → global) are correctly walked narrowest-first (`internal/secret/resolve.go:43-72`); secrets default to the OS keychain (`internal/secret/store.go:26-31`); `.env`, `secrets.env`, and `.ateam/` are all gitignored. No prod URLs, prod buckets, or prod queues are hardcoded anywhere. No finding.

## Lens 2 (dev-environment safety) — designed-in, no new gaps

The destructive operations present in the codebase are all bounded to artefacts ateam itself owns:

- `cmd/project_rename.go:111` removes stale project registrations under `OrgDir/projects/<id>` only after checking the source dir doesn't exist.
- `internal/eval/worktree.go:90,164` removes `.ateam/` inside a freshly-created git worktree under `baseDirAbs`, not the source tree.
- `internal/agent/claude_auth.go:389-401` has explicit dry-run support before any directory removal.
- `cmd/agent_config.go:730-732` runs `rm -rf "$1"/*` inside the *container* (`docker exec`), passes `claudeDir` as `$1` to avoid shell injection, and only fires when both `claudeDirNonEmpty` and `--force` are set.

None of these reach into a "production" target because no such target exists. The Makefile's `clean` (`Makefile:156-158`) removes only build artefacts (`ateam`, `ateam-race`, `ateam-linux-*`, `build/`). There is no `make reset-db`, `make drop`, or `make seed-prod` target an agent could be tricked into running. No finding.

# Quick Wins

1. **`install.sh:58`** — guard `sudo rm -rf /usr/local/go` behind a confirmation / opt-in flag and skip when the existing install already meets the version requirement. (LOW / SMALL)
2. **`Makefile:75,96`** — tag `ateam-test-dind` with a per-checkout suffix so parallel checkouts (e.g. `ateam` + `ateam-candidate`) don't clobber each other's test image. (LOW / SMALL)

(No MEDIUM+ severity findings to promote into a quick-win list — the project's prod-readiness surface is small by design.)

# Project Context

**Project shape** — Go CLI binary (`main.go`, `cmd/`, `internal/`), built via `make build` → `./ateam`. No service runs. The only network listener is `ateam serve` (read-only local web UI for reports). State is a per-project SQLite file under `.ateam/state.sqlite`. Distribution is via `install.sh` symlinking into `~/.local/bin`.

**Deployment** — None. The project does not target a production environment; it is run interactively or unattended on developer machines / CI runners. Lens 3 of this role's brief is therefore mostly N/A — flagged explicitly above so future runs don't manufacture findings.

**Env separation** — Single environment (the host). Config scopes are project (`.ateam/`) → org (`.ateamorg/`) → global (`~/.config/ateam/`), walked narrowest-first by `internal/secret/resolve.go`. Secrets default to the OS keychain (`internal/secret/store.go`); file-backend is `0600`-mode `secrets.env`. `.gitignore` excludes `.env`, `.ateam/`, `build/`, `test_data/`, `ateam` binary, `ateam-linux-*`.

**Isolation modes** — Documented in `ISOLATION.md`: sandbox (Seatbelt / bubblewrap), Docker one-shot, Docker exec, ateam-inside-Docker. Default is the built-in OS sandbox. This *is* the project's safety story for running unattended agents — Lens 2 is intrinsic to the product.

**Key files for this lens**:
- `install.sh` — install path, contains the only unconditional `sudo rm -rf` in the repo
- `Makefile` — destructive-target survey (none beyond build cleanup)
- `cmd/serve.go` — only HTTP listener; defaults to localhost with warning when public
- `internal/secret/{store.go,resolve.go}` — secret backends and scope chain
- `internal/agent/claude_auth.go`, `internal/eval/worktree.go`, `cmd/project_rename.go`, `cmd/agent_config.go` — every `os.RemoveAll` / `rm -rf` site; all confined to ateam-owned paths
- `ISOLATION.md` — operator-facing safety story
- `.gitignore`, `.ateam/.gitignore` — credential/state exclusion

**Recent commits (last 5)** — all touch `roles --docs`, prompts, review/report wiring, code-structure findings. None touched config loading, secret resolution, the install script, or the Makefile. No recent-changes-bias findings.

**Re-runs** — On next run, re-check whether `install.sh:58` and the Makefile test-image tagging have changed; both findings are stable until those files do.
