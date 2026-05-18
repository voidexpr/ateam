# Supervisor Review — 2026-05-14

## Project Assessment

ATeam is in a healthy, mature state: the four roles converge on "designed-in safety, small surface, no production deployment to worry about." The single MEDIUM finding is a real defect (`ateam secret --value` leaks via the process table — the exact class of leak the rest of the codebase deliberately avoids), and the rest is documentation drift and small hygiene. No structural or architectural issues were raised.

## Priority Actions

### 1. Fix process-table leak in `ateam secret --value`

- **Action**: In `cmd/secret.go:56`, remove the `--value` flag, or replace it with `--value-from-env <ENVVAR>` / `--from-file <path>` so the secret never reaches argv. If the flag must stay for backward compatibility, print a one-line stderr warning when `--value` is non-empty and document the leak in the `Long` help (cmd/secret.go:32-46). The existing stdin / keychain paths are fine and should remain the documented default.
- **Source Role**: project.security (2026-05-14_14-11-43)
- **Source Report**: .ateam/roles/project.security/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: This is the only MEDIUM finding across all four reports and the only real defect. The rest of the codebase goes to deliberate lengths to keep secrets out of argv (`internal/container/docker.go:376-398` splits `-e KEY=VALUE` into argv-only `-e KEY` plus `cmd.Env` value); the `secret` subcommand is the one place that contradicts that discipline. Small change, high consistency win.

### 2. Reconcile DEV.md with actual CI behavior

- **Action**: Update `DEV.md:85-99` to reflect that `.github/workflows/ci.yml` and `.github/workflows/docker-tests.yml` are `workflow_dispatch:` only — not auto-triggered on push or PR. State that the actual gate is the local `make run-ci` and the pre-commit hook installed by `make install-hooks`. While editing, also add `check-docs` to the DEV.md description of `make check` (`DEV.md:65-66` vs `Makefile:56`). Do NOT add `push` / `pull_request` triggers to the workflows — recent direction (commit `2c732b8`: "missing CI is MEDIUM at most, not HIGH") and the solo-author history indicate the manual-dispatch choice is intentional; the fix is in the docs, not in the workflows.
- **Source Role**: project.automation (2026-05-14_14-06-40)
- **Source Report**: .ateam/roles/project.automation/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: DEV.md currently lies about a safety net that does not exist. That false confidence is the worst kind of foundations gap because every downstream role implicitly assumes CI will catch X. The local gates already work; the docs just need to describe reality.

### 3. Refresh the on-disk pre-commit hook from the `install-hooks` template

- **Action**: Run `make install-hooks` to overwrite `.git/worktrees/ateam-candidate/hooks/pre-commit` so it matches the current template (`fmt-check && check-tidy && check-docs && lint`). The current on-disk hook is missing `check-docs` and `lint`, so a developer can land a `golangci-lint` failure or a `ROLES.md` drift without local warning.
- **Source Role**: project.automation (2026-05-14_14-06-40)
- **Source Report**: .ateam/roles/project.automation/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Cheap to do; restores the intended local gate. Pure environment hygiene, no code change.

### 4. Guard `install.sh` against silently deleting an existing Go toolchain

- **Action**: In `install.sh:58` (Linux branch of `install_go()`), before the `sudo rm -rf /usr/local/go`: (a) detect whether `/usr/local/go` exists and skip the install if the existing version already meets the required minimum, (b) when removal is required, print the resolved target path and require either interactive confirmation or an explicit opt-in (`INSTALL_GO=force` env var or `--reinstall-go` flag). The macOS Homebrew branch is unaffected.
- **Source Role**: project.production_ready (2026-05-14_14-06-31)
- **Source Report**: .ateam/roles/project.production_ready/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: `install.sh` is the first thing a new operator runs; silently destroying a distro-installed or pinned Go tree is exactly the dev-environment-safety failure mode the production_ready role watches for. Low frequency of pain but high blast radius when it does hit.

### 5. Suffix the docker test image tag with a per-checkout discriminator

- **Action**: In `Makefile:75-101`, change the fixed `ateam-test-dind` tag used by `make test-docker` / `make test-docker-live` to include a per-checkout suffix — e.g. `ateam-test-dind:$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)` or a hash of `$(CURDIR)`. Add a one-line Makefile comment explaining the convention. Optional: pass `--pull=false` to `docker run` so a partially-overwritten image is never re-pulled mid-run.
- **Source Role**: project.production_ready (2026-05-14_14-06-31)
- **Source Report**: .ateam/roles/project.production_ready/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: The legitimate ateam workflow pattern is to run `ateam` and `ateam-candidate` worktrees side-by-side (this very project demonstrates it). The fixed tag means concurrent `make test-docker` runs clobber each other's image, producing nondeterministic results. Fix is one Makefile line.

### 6. Reject `..` and `/` in the `dirName` URL segment of code-session handlers

- **Action**: At the top of `handleCodeSessionDetail` and `handleCodeSessionFile` (`internal/web/handlers.go:1301-1413`), reject `dirName` values containing `..`, `/`, or starting with `.`, mirroring the existing `fileName` guard at handlers.go:1387. The downstream `isPathWithin(absPath, pe.ProjectDir)` already prevents escape from `.ateam/`, so this is consistency / defense-in-depth, not exploit prevention.
- **Source Role**: project.security (2026-05-14_14-11-43)
- **Source Report**: .ateam/roles/project.security/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Cheap consistency fix that brings `dirName` to parity with the existing `fileName` guard in the same handlers. Bundle with Action 1 since both touch security-adjacent input validation; same reviewer, same mental context.

## Deferred

- **Tighten CSP to remove `'unsafe-inline'`** (`internal/web/server.go:291-298`) — LOW, MEDIUM effort. project.security explicitly filed this as "no action without owner sign-off" because the verification step requires loading the UI and clicking through every tab to confirm no Chroma/template regressions. Localhost-only default + network-exposure warning is the compensating control. Revisit only if `ateam serve` becomes a remote-exposed feature.

- **Wire `govulncheck` into CI** — project.dependencies LOW finding. The local sandbox could not reach `proxy.golang.org` or `api.osv.dev`, so reachability-based vuln scanning could not run. The CI workflows where this would live are `workflow_dispatch:`-only by current project policy (commit `2c732b8`); when the owner next dispatches `ci.yml` or runs `make run-ci` locally, `make vuln` already covers this. No action required until the workflow trigger policy changes.

- **Chroma v2.2.0 → v2.24.1 version gap** — project.dependencies noted the gap but filed no finding (no CVE, no abandonment, no blocker citing it). Defer unless `code.bugs` or a future security finding traces a chroma-specific issue.

- **Auto-running CI on push/PR** — project.automation framed this as a defensible solo-author choice ("option a or option b — pick whichever matches intent"). Recent commit `2c732b8` ("missing CI is MEDIUM at most, not HIGH") and the absence of concurrent contributors confirms the manual-dispatch policy is intentional. The Priority Action above resolves the symptom (DEV.md lies) without changing the policy. Do not re-promote this in future reviews unless contribution patterns change.

## Conflicts

None. The four roles cover disjoint surfaces (CI/build foundations, dependencies, prod-readiness, security) and their findings do not contradict each other. The closest thing to overlap is automation flagging the CI doc drift and dependencies wanting `govulncheck` in CI — the resolution is the same in both directions: fix the docs to match the manual-dispatch reality, and rely on `make run-ci` / `make vuln` locally.

## Notes

- The project's security posture is unusually mature for its size — secrets-via-env-not-argv, stdin-for-prompts, parameterized SQL, safe-by-default goldmark, localhost-only web UI with warning, OS-keychain default. The `secret --value` flag is a single inconsistency in an otherwise principled story; worth fixing precisely because everything else is right.
- All four roles independently noted that ateam has no production deployment, no service-side resources, and no env separation work to do. Future reviews should not invent prod-readiness findings against a CLI binary; the production_ready role calibration here is correct.
- The two doc-vs-reality gaps (DEV.md CI claims, on-disk pre-commit drift) are the kind of issue a deterministic check could catch: a `make check-hooks` target that diffs the installed hook against the template, plus a doc-lint step that asserts `.github/workflows/*.yml` triggers match a phrase in DEV.md. Not worth building today — the manual fix is one PR — but worth noting if these gaps recur in a future review cycle.
- No role re-found a previous-cycle issue (project.security and project.dependencies are both first-runs; project.automation and project.production_ready findings are stable until the named files change). No tool-adoption recommendation is warranted at this point.
