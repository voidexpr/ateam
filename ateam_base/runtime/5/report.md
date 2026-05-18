# Summary

The ateam codebase has a solid testing baseline: 81 Go test files across 17 packages, all green under `go test -race ./...`, with per-package statement coverage ranging from 31% to 94%. The build/test commands are documented in `DEV.md` and the `Makefile` separates unit (`make test`), CLI (`make test-cli`), and Docker (`make test-docker`, `make test-docker-live`) suites. The main regression-protection gaps are concentrated in the `cmd/` layer: several user-facing subcommands ship without any test (`verify`, `serve`, `runs`, `tail`, `prompt`, `eval`), and the documented headline workflow `report → review → code → verify` is only covered end-to-end by `TestAllRunsAllFourPhases` — there is no isolated smoke test for `verify`, despite it being auto-chained from `code`.

# Findings

## 1. `ateam verify` has no test file

- **Location**: `cmd/verify.go` (188 LOC); compare to `cmd/report_test.go`, `cmd/review_test.go`, `cmd/code_test.go`.
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: `verify` is one of the four documented pipeline steps (`report → review → code → verify`) and is auto-chained from `runCode` and `runAll`. Every sibling step has at least a `--dry-run` test that exercises flag wiring, profile resolution, and the runner construction path; `verify` does not. A regression in `runVerify` (broken flag binding, panic in supervisor prompt resolution, accidental nil deref of `cr.CallDB`) would only surface in `TestAllRunsAllFourPhases`, which uses the `test` profile and currently asserts on phase-header text — easy to mask if the verify phase silently no-ops or skips early. `verify` also has its own unique branch (`setSourceWritable(cr)` mentioned in the architecture report) that no other test path exercises.
- **Recommendation**: Add `cmd/verify_test.go` with a `TestVerifyDryRun` mirroring `TestReportDryRun` / `TestCodeDryRunAgentInjection`: `root.InstallOrg` → `root.InitProject` → `runVerify(VerifyOptions{DryRun: true, Profile: "test"})`, asserting the dry-run output contains the supervisor prompt marker and that no panic occurs when `CallDB` is wired in. One file, ~50 LOC, copy-pasted structure.

## 2. No CLI smoke test for the headline `init → report` happy path

- **Location**: `test/cli/test-auth-combos.sh` is the only CLI shell test; the `Makefile` `test-cli` target runs only it.
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: `test-auth-combos.sh` is excellent for the 16-cell auth matrix but exercises only `ateam exec ping --dry-run`. There is no shell-level smoke test that verifies the README's Quick Start sequence still works end-to-end: `ateam init` creates `.ateam/`, `ateam report --dry-run --profile test --roles testing_basic` succeeds, and `ateam ps` returns. Go-level tests use `root.InstallOrg`/`root.InitProject` directly and bypass the cobra command tree; a breakage in `init.go`'s `RunE`, root flag parsing, or the `cmd.Execute()` entry point would pass `make test` and only surface when a user runs the binary. The Go-test/CLI gap is already visible in `cmd/install.go` and `cmd/init.go` having no `_test.go` despite being the first commands every new user invokes.
- **Recommendation**: Add `test/cli/test-smoke.sh` that runs in an isolated HOME (same pattern as `test-auth-combos.sh`): `ateam init --org-create ...`, `ateam report --dry-run --profile test --roles testing_basic`, `ateam ps`, `ateam serve --port 0 &` + `kill` (or use `--no-open` + immediate shutdown via a flag if available). Wire it into the `test-cli` target so `make test-cli` runs both. Optionally add `make smoke` as a quick health check that's faster than `make test`.

## 3. `test-cli` is not part of the default `make test`

- **Location**: `Makefile:55-66`. `test:` runs only `go test -race ./...`; `test-cli` is a separate target that only `make test-all` invokes.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The CLI integration test is the only thing that exercises the compiled binary and the secret-resolution code path through the real cobra command tree. By gating it behind `test-all`, anyone running `make test` (the default contributor workflow per `DEV.md`) skips it. A regression that breaks the binary entry point but leaves the Go tests passing — for example, an `init()` panic in a subcommand that the unit tests don't import — would not be caught by `make check` either, since `check: test fmt-check check-tidy check-docs lint` doesn't include CLI tests.
- **Recommendation**: Either fold `test-cli` into `test` (it's 16 fast subprocess invocations, low overhead) or into `check` (the documented "developer quick health check"). Document the choice in `DEV.md`. If the binary-build dependency is a concern, gate with `test-cli: build-binary` (already the case) so it runs after a fresh build.

## 4. `fsclone` has 31.2% coverage — promotion path is the runner's single source of truth for artifacts

- **Location**: `internal/fsclone/clone.go` (81 LOC), tested by `internal/fsclone/clone_test.go` (71 LOC).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `fsclone.Clone` is how every promoted artifact reaches its canonical path (`runtime/<exec_id>/report.md` → `roles/<role>/report.md`). Current tests cover the happy path and overwrite; they don't cover: missing source file (returns sensible error?), source is a symlink, parent directory creation when nested, permission-denied destination. A silent fsclone failure means agents appear to succeed (stream completed, DB row updated, exit 0) but the canonical file is stale — the worst kind of regression for a tool whose contract is "auditable markdown files." 31.2% is well below the project's median package coverage.
- **Recommendation**: Add three regression tests: `TestCloneReturnsErrorWhenSourceMissing`, `TestCloneCreatesNestedParentDirs`, `TestClonePreservesContentWhenSrcIsSymlink`. Keep it small — the goal is to lock the contract, not to test the OS.

## 5. Five user-facing commands have no `_test.go` at all

- **Location**: `cmd/{verify,serve,runs,tail,prompt,eval,projects,install,update,env,container_cp,project_rename,auto_setup}.go`. Of these, `verify` (Finding 1), `serve`, `runs` (the `ateam ps` command), `tail`, and `prompt` are the most user-visible.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: These commands collectively account for ~1500 LOC of `cmd/` code that's only exercised by the cobra-registration `init()` blocks (which run as a side effect of importing the package in other tests). Practical risks per command:
  - `serve` (`cmd/serve.go`): server startup, port allocation, `--public` / `--bind` flag wiring. A panic during `web.NewServer` would only surface when a user runs `ateam serve`.
  - `runs` / `ps` (`cmd/runs.go`): the primary tool for inspecting past agent runs; SQL queries against `calldb` are not validated here.
  - `tail` (`cmd/tail.go`): the only way to watch a live run; broken stream parsing wouldn't be caught by `internal/runner` tests which work on saved files.
  - `prompt` (`cmd/prompt.go`): 190 LOC of 4-level prompt resolution glue. `internal/prompts` is tested but its cobra wrapper isn't.
  - `eval` (`cmd/eval.go`): 451 LOC of self-contained subcommand. `internal/eval` is well tested (77.4%) but the cobra wrapper that wires flags into `eval.Run` has zero direct coverage.
- **Recommendation**: Don't aim for full coverage — add one smoke test per command that exercises the cobra `RunE` with minimal valid arguments (or `--help` parse + arg-validation): `TestServeBindFlagParsing`, `TestRunsFilterFlags`, `TestTailNoArgsDoesNotPanic`, `TestPromptDryResolution`, `TestEvalListSubcommand`. Each ~30 LOC, modelled on existing `TestPrintExecDryRun`. The goal is a single signal per command that flag wiring + entry-point construction haven't regressed.

## 6. `make test-docker-live` regresses silently when auth is missing in CI

- **Location**: `Makefile:74-99`, `internal/container/docker_live_test.go`.
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `DEV.md` documents the design choice: "The tests themselves also fail (not skip) when auth is missing — this catches configuration issues in CI." Good intent, but in practice `make test-docker-live` is not part of any aggregate target a contributor runs by default — it's an opt-in `make test-all` step that requires `~$0.03` of API spend. There's no scheduled or documented cadence for running it, so a regression in the live-agent path could sit for weeks. The existing Docker `test-docker` (no auth needed) does catch most container-level breakages, so the marginal value of `test-docker-live` is low for everyday testing but high as a periodic sanity check.
- **Recommendation**: Either (a) document a recommended cadence in `DEV.md` (e.g. "run before each tagged release") and add a `make release-check` target that includes it, or (b) split a `test-docker-live-cheap` variant that runs a single 5-token agent call as a smoke probe rather than the full suite. No code changes to the tests themselves — purely process/Makefile.

## 7. `internal/web` handler tests don't cover the markdown render pipeline that powers `ateam serve`

- **Location**: `internal/web/handlers.go:593` (HTML render), `internal/web/markdown.go`; tests in `internal/web/handlers_test.go`, `helpers_test.go`, `history_test.go`. Coverage: 48.8%.
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: The web server is how users browse reports and reviews. The existing tests verify routing, path-traversal helpers (`is_path_within_test.go`), and history scanning, but there's no test that renders a sample `report.md` through `newMarkdown()` and asserts a stable HTML shape (e.g. fenced code blocks render, no panic on the markdown the agents actually produce). A regression in the markdown library or the renderer initialization would only show as visual ugliness when a user opens `ateam serve`.
- **Recommendation**: Add one golden-output test that feeds a representative `report.md` (containing headings, lists, fenced code, tables — the four constructs the role prompts produce) into the markdown renderer and asserts it returns non-empty HTML containing the expected tags. Don't snapshot the full HTML (brittle); assert structural markers only.

# Quick Wins

1. **Add `cmd/verify_test.go` dry-run** (Finding 1) — SMALL effort, closes the only documented pipeline step with no isolated test; ~50 LOC modeled on `cmd/report_test.go:TestReportDryRun`.
2. **Add `test/cli/test-smoke.sh`** (Finding 2) — SMALL effort, single shell script following the existing `test-auth-combos.sh` pattern. Catches binary-entry-point regressions that all Go tests miss.
3. **Fold `test-cli` into `make test` (or `make check`)** (Finding 3) — one-line Makefile change; raises the floor of the default contributor workflow.
4. **Three `fsclone` regression tests** (Finding 4) — locks the artifact-promotion contract (missing source, nested parents, symlink source).

# Project Context

- **Language / build**: Go (module `github.com/ateam`). Build: `make build` produces `./ateam`. Linux companion: `make companion`.
- **Test layout**:
  - **Go unit/integration**: 81 `*_test.go` files across 17 packages. Run with `make test` (which is `go test -race ./...`).
  - **Shell CLI**: `test/cli/test-auth-combos.sh` — the only CLI-level test; run with `make test-cli`. Exercises `ateam exec ping --dry-run` across 16 auth combinations.
  - **Docker integration**: `internal/container/docker_integration_test.go` (build tag `docker_integration`) + `docker_live_test.go` (tag `docker_live`). Driven by `test/Dockerfile.dind` and run via `make test-docker` / `make test-docker-live`.
  - **Test data**: `internal/runner/testdata/`. Per `CLAUDE.md`, ad-hoc ateam testing artifacts go in `./test_data/`.
- **Per-package coverage** (from `go test -cover ./...`):
  - High (>75%): `internal/display` 94.1%, `internal/gitutil` 89.3%, `internal/calldb` 82.8%, `internal/runner` 81.3%, `internal/config` 78.8%, `internal/runtime` 77.5%, `internal/eval` 77.4%, `internal/root` 74.0%.
  - Medium (50–75%): `internal/secret` 69.4%, `internal/streamutil` 64.1%, `internal/prompts` 52.0%.
  - Low (<50%): `cmd` 41.5%, `internal/agent` 47.1%, `internal/container` 49.9%, `internal/web` 48.8%, `internal/fsclone` 31.2%.
- **Test helpers**: `cmd/exec_test.go:captureStdout`, `cmd/*_test.go:withChdir`, `internal/runner/test_helpers_test.go`, `internal/agent/mock.go:MockAgent`, and the `test` profile in `defaults/runtime.hcl` that resolves to a mock agent — these are the standard scaffolding for new tests.
- **Untested CLI commands** (no sibling `_test.go`): `verify`, `serve`, `runs` (the `ateam ps` command), `tail`, `prompt`, `eval`, `projects`, `install`, `update`, `env`, `container_cp`, `project_rename`, `auto_setup`, `root`, `pool_render_mpb`.
- **Audit role**: `testing_basic` (this report); model: `claude-opus-4-7`, no extended thinking. Prior report files for this slot: none — `.ateam/runtime/5/` was empty before this run. Prior `.ateam/runtime/{1,2}` contain unrelated architecture/code-structure reports.
- **Recommended automation tool**: none beyond the existing `go test -race -cover` + `Makefile` setup. The project's test tooling is appropriate for the codebase size; the gaps are about which paths are tested, not how.
