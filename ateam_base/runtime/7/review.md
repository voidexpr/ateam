# Supervisor Review — 2026-05-14_06-22-39

### Project Assessment

The ateam test suite is in good shape: 81 `_test.go` files, ~46% test code by LOC, race-enabled by default, `MockAgent` scaffolding, build-tagged isolation for Docker/live suites, and per-package coverage in a 31–94% range. The reviews agree the gaps are concentrated in two places: (a) the `cmd/` layer — `verify`, `serve`, `runs`/`ps`, `tail`, `prompt`, `eval`, `env` have no isolated tests despite being user-visible entry points, and (b) two latent flakes (`time.Sleep`-based goroutine coordination) plus zero coverage visibility (no `make coverage` target). The structural improvements that would unlock parallel testing (cmd-flag globals → Opts struct; split of `Runner.Run`) are real but overlap with refactor work the prior cycle already deferred; the leverage here is in small, targeted additions, not a broad test refactor.

### Priority Actions

#### 1. Add `cmd/verify_test.go` dry-run smoke test
- **Action**: Add a single `TestVerifyDryRun` in `cmd/verify_test.go` that mirrors `cmd/report_test.go:TestReportDryRun` — `root.InstallOrg` → `root.InitProject` → call `runVerify(VerifyOptions{DryRun: true, Profile: "test"})`, capture stdout via `captureStdout`, assert the dry-run output contains the supervisor prompt marker and that the call returns without panicking when `CallDB` is wired. ~50 LOC.
- **Source Role**: testing_basic (2026-05-14_06-20-41), testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_basic/report.md (Finding 1), .ateam/roles/testing_full/report.md (Finding 7)
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: `verify` is one of the four documented pipeline steps (`report → review → code → verify`) and is auto-chained from `runCode`/`runAll`. Every sibling step has a `--dry-run` test; `verify` does not. A regression in `runVerify` (broken flag binding, panic in supervisor prompt resolution, nil deref on `cr.CallDB`, the unique `setSourceWritable(cr)` branch) currently only surfaces through `TestAllRunsAllFourPhases`, which asserts on phase-header text and is easy to mask. Both reports flag this; identical fix.

#### 2. Add `make coverage` target
- **Action**: Add `coverage:` to `Makefile`: `go test -coverprofile=coverage.out -covermode=atomic ./...` then `go tool cover -func=coverage.out | tail -1` for a single-number summary, plus a `coverage-html:` variant emitting `coverage.html`. Reference it in `DEV.md`. No CI enforcement, no threshold — purely visibility.
- **Source Role**: testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_full/report.md (Finding 6)
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Both reports' heuristic finds ("X is at 31%", "Y has no _test.go") are guesses without a coverage profile. A one-target Makefile change makes future testing reviews factual and lets the project see where coverage actually drops without writing a single new test.

#### 3. Replace `time.Sleep`-based goroutine coordination in the two known-flaky tests
- **Action**: In `internal/runner/tailer_test.go:78`, replace `time.Sleep(200ms)` with a `chan struct{}` signaled when the tailer parses the first line (expose a small hook on the tailer for tests, or write the file before starting the tailer and use a deterministic readiness signal). In `internal/agent/cancel_test.go:38`, cancel the context inline before `cmd.Wait()` instead of from a goroutine after a sleep — the WaitDelay/SIGKILL escalation does not need timing-based coordination.
- **Source Role**: testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_full/report.md (Finding 3)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Two `time.Sleep` calls in a 20k-LOC otherwise-deterministic suite are exactly the spots that go flaky in CI months later under `-race` and load. Cheap to convert today, expensive to debug from intermittent failures.

#### 4. Add CLI smoke test for the `init → report → ps` happy path and fold `test-cli` into the default contributor workflow
- **Action**: Add `test/cli/test-smoke.sh` modeled on the existing `test/cli/test-auth-combos.sh` pattern (isolated `HOME`, subprocess invocations of the compiled binary): run `ateam init --org-create ...`, `ateam report --dry-run --profile test --roles testing_basic`, `ateam ps`; assert each exits 0 and writes the expected `.ateam/` artifacts. Wire it into `make test-cli`. Then either fold `test-cli` into the default `test:` target or into `check:` so `make check` runs it — `test-cli` is 16 fast subprocess invocations plus the new ~3-case smoke; cost is negligible.
- **Source Role**: testing_basic (2026-05-14_06-20-41)
- **Source Report**: .ateam/roles/testing_basic/report.md (Findings 2, 3)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Go-level tests use `root.InstallOrg`/`root.InitProject` directly and bypass the cobra command tree; a breakage in `init.go`'s `RunE`, root flag parsing, or `cmd.Execute()` would pass `make test` and only surface to users. The current `test-auth-combos.sh` exercises only `ateam exec ping --dry-run`. Sequenced before action 5 because once the CLI smoke harness exists, adding per-command smokes is a one-line append.

#### 5. Add three `internal/fsclone` regression tests
- **Action**: In `internal/fsclone/clone_test.go`, add `TestCloneReturnsErrorWhenSourceMissing`, `TestCloneCreatesNestedParentDirs`, `TestClonePreservesContentWhenSrcIsSymlink`. Lock the contract; do not pad with OS-behavior tests.
- **Source Role**: testing_basic (2026-05-14_06-20-41)
- **Source Report**: .ateam/roles/testing_basic/report.md (Finding 4)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: `fsclone.Clone` is how every promoted artifact reaches its canonical path (`runtime/<id>/report.md` → `roles/<role>/report.md`). 31.2% coverage is the lowest in the repo, and a silent failure means agents appear to succeed while the canonical file is stale — the worst regression class for a tool whose contract is "auditable markdown files."

#### 6. Add per-command dry-run smoke tests for `eval`, `env`, `runs`, `tail`, `prompt`, `serve`
- **Action**: For each of `cmd/eval.go`, `cmd/env.go`, `cmd/runs.go`, `cmd/tail.go`, `cmd/prompt.go`, `cmd/serve.go`, add one `_test.go` patterned on `cmd/exec_test.go:TestRunExecDryRunNoExec`: set flags, set up temp org+project via `root.InitProject`, capture stdout, assert a key output fragment appears and that the entry point does not panic. ~30 LOC each. For `serve`, use `--port 0` and immediately shut down (or test only the option-parsing path if the server has no clean shutdown hook).
- **Source Role**: testing_basic (2026-05-14_06-20-41), testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_basic/report.md (Finding 5), .ateam/roles/testing_full/report.md (Finding 7)
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: Both reports converge on this set. The goal is one signal per command that flag wiring + entry-point construction haven't regressed, not full coverage. `eval` (451 LOC) and `env` (352 LOC) are the largest cobra wrappers without tests; their libraries (`internal/eval`) are well tested but the flag glue is not. Best done after action 4 lands the coverage profile so the gaps are quantified, and after action 2's `make coverage` makes the improvement visible.

#### 7. Tighten `internal/runner/pool_test.go` to assert exact peak concurrency
- **Action**: In `TestRunPoolBasic`, when `len(tasks) > parallelism` and the agent has a deterministic delay, assert `a.maxConcurrent.Load() == int64(parallelism)` instead of `≤`. The `concurrencyTrackingAgent` already records peak; only the assertion changes.
- **Source Role**: testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_full/report.md (Finding 15)
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Free regression guard against "pool quietly serialized" — a class of bug current tests would silently pass.

#### 8. Pin file mtimes in `internal/web/code_sessions_test.go` with `os.Chtimes`
- **Action**: After writing test files in `internal/web/code_sessions_test.go:13-30`, call `os.Chtimes(path, target, target)` to lock mtime to the value the assertion expects, so the test is independent of FS-time quantization and machine clock.
- **Source Role**: testing_full (2026-05-14_06-22-36)
- **Source Report**: .ateam/roles/testing_full/report.md (Finding 13)
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Cheap pre-emptive hardening of the only timestamp-comparing test, which would otherwise drift to flaky if the directory walker ever switches from DB rowtime to mtime.

### Deferred

- **Refactor `cmd/*` to take `Opts` structs instead of package-level Cobra-flag globals** (testing_full #2, MEDIUM/MEDIUM): The right direction and would unblock `t.Parallel` for the cmd package, but it is a 25-flag, multi-file refactor that overlaps with the prior cycle's deferred `cmd/table.go` split and `prepareRun` extraction (prior review Priority Actions 5 and 6). Land those structural changes first; the Opts conversion is a natural follow-up.
- **Add `t.Parallel()` to pure tests across the suite** (testing_full #1, MEDIUM/MEDIUM): Mechanical and would speed up the suite, but the cmd package is the largest consumer of pure tests and is blocked by the flag-globals refactor above. Revisit once that lands. For now, the race detector + per-package parallelism via `go test ./...` is sufficient.
- **Split `Runner.Run` (440 LOC) into phases for unit-testable parts** (testing_full #4, MEDIUM/LARGE): Exactly the architecture-report deferred item from the prior cycle ("Decompose god Runner"). Same call: do it after the agent dedup and `prepareRun` extraction. Independent of the split, `renderSettingsJSON` and `mergeSandboxPaths` are already isolable enough to test in isolation — fold into action 6's pass if convenient, otherwise leave.
- **Migrate `test-auth-combos.sh` and new CLI smokes to `testscript` (rsc.io/script/scripttest) or `bats-core`** (testing_full #5, MEDIUM/MEDIUM): Worthwhile long-term but the existing bash harness works and adding `testscript` is a new dependency + paradigm shift. Land action 4's smoke first; revisit migration once there are 5+ CLI scenarios that justify the framework.
- **Add fuzz targets for stream parsers and `parseMaxAge`** (testing_full #14, LOW/MEDIUM): Reasonable hardening but no concrete pain reported; defer until a parser regression actually bites or until coverage (action 2) shows the parsers are under-tested.
- **Drive `race_test.go` from an agent registry** (testing_full #8, LOW/SMALL): Future-proofing for a third agent type that does not exist. Leave a `// TODO` at the `Agent` interface instead if anyone is concerned; the registry refactor is a non-issue today.
- **Document docker-tagged tests in `make help` / a comment in `internal/container`** (testing_full #9, LOW/SMALL): Documentation polish, low blast radius.
- **`docker_live_test.go` should `t.Skip` instead of `t.Fatal` when image build fails** (testing_full #10, LOW/SMALL): Marginal UX win for the rare contributor running `test-docker-live`; defer until someone hits the cascading-failure mode.
- **Document `bugs_test.go` known-open-bug convention** (testing_full #11, LOW/SMALL): Process polish; defer until a known-open bug actually needs to be encoded.
- **BuildX cache for `make test-docker`** (testing_full #12, LOW/SMALL): Real dev-experience win but only for contributors iterating on container code daily; defer until that workload appears.
- **Web markdown render golden test** (testing_basic #7, LOW/MEDIUM): Catches visual regressions on `ateam serve` but no concrete pain. Defer.
- **`make test-docker-live` cadence policy** (testing_basic #6, LOW/SMALL): Process question, not a code fix; tie to a future release-process write-up rather than a one-off action.
- **CI workflow recommendation** (testing_full #16): **Invalid finding** — `.github/workflows/ci.yml` and `.github/workflows/docker-tests.yml` already exist in the repo and `DEV.md` documents them. The role report appears to have missed the workflows directory. No action; flagging here so a future testing role report can re-verify before re-raising.

### Conflicts

No direct contradictions between the two reports. They converge on the same priorities — `cmd/verify_test.go`, the broader untested-cmd-entry-points list, and the CLI-coverage gap. Where they overlap (Finding 5 in testing_basic and Finding 7 in testing_full), the recommendations are identical in shape; bundled into Priority Action 6.

### Notes

- **Both reports agree the suite's bones are healthy**: race-by-default, `MockAgent` plumbing, build-tag isolation, dedicated `bugs_test.go` regression markers, and a DinD harness. The deltas are all about coverage of the cobra/CLI surface, not test architecture.
- **Sequencing relative to the prior cycle**: the deferred `cmd/table.go` split → `prepareRun` extraction → cmd-globals → `Opts` struct → `t.Parallel()` chain is a real path forward, but each link is a separate cycle's work. This review intentionally avoids stacking onto that chain and focuses on additive smoke coverage that does not block or get blocked by the structural refactors.
- **`make coverage` (action 2) is the highest-leverage item**: it converts the next testing review from heuristic ("eval.go is 451 LOC with no test file") to factual ("`cmd/eval.go` is at 12% coverage; the uncovered lines are the flag-parsing branch"). One-time cost; compounds across cycles.
- **Stale-report watch**: testing_full Finding 16 claimed there is no CI; both `.github/workflows/ci.yml` and `docker-tests.yml` exist. The prior supervisor review also noted "no CI/CD recommendations are surfaced — the project's pipeline is local-first," which is similarly stale. Worth correcting in the next agent-facing docs pass so future role reports calibrate correctly.
