# Summary

The Go test suite (81 `*_test.go` files, ~17k lines) is in good shape: tests use `t.TempDir()` and `t.Cleanup`, run under `-race` via `make test`, and assertions are mostly real behavior checks (output contents, DB rows, returned errors). The strongest material is the `internal/runner` race regression suite, which explicitly cites past data-race bugs and the file/function it guards. The few weaknesses are concentrated in the `cmd` package's flag-mutating tests and a couple of wall-clock-bound tests; no flaky tests are currently failing CI, but the patterns are present and should be tightened before they fire.

Re-verified all 9 prior findings against current source — all remain valid (line numbers unchanged). The previous report has not been acted on; nothing to drop.

# Role performing the audit

- Role: `test.quality`
- Model: Opus 4.7 (`claude-opus-4-7`)
- Mode: read-only analysis; no source edits
- Inputs: every `*_test.go` outside `node_modules`; `Makefile`; project layout

# Findings

## 1. `time.Sleep(200ms)` in `TestTailerGrowingFile` couples the test to wall-clock timing

- **Location**: `internal/runner/tailer_test.go:78` (inside `TestTailerGrowingFile`)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The test spawns a goroutine that sleeps 200ms and then appends a `result` event to the tailed file. The tailer is given `PollInterval = 50ms` and a 2-second context. There is currently ~1.5s of slack, so this rarely flakes. However, the sleep is the only synchronization between the writer and the tailer — there is no signal that the tailer has actually opened the file and reached its polling loop before the writer appends. The test passes today because the polling loop opens the file inside `Run`, which is called before the goroutine wakes up; if `tailer.Run` ever changes to lazy-open or starts adding setup work, the test will silently lose its determinism. The bug class slipping through: tailer regressions that delay first-poll past the wall-clock budget.
- **Recommendation**: Replace the 200ms sleep with a "wait until tailer has read the seed lines" signal — e.g., poll `buf.String()` for the first system event before appending the result, or expose a `tailer.Ready` channel. Keep the 2-second outer timeout as a guard. A `WaitFor`/`assert.Eventually` helper would be useful and is missing from this codebase.

## 2. `time.Sleep(100ms)` in `TestConfigureProcessLifecycle_KillsTreeOnCancel`

- **Location**: `internal/agent/cancel_test.go:38`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The test starts `/bin/sh -c "trap '' TERM; sleep 300"`, then a goroutine sleeps 100ms before calling `cancel()`. The sleep is meant to give the child process time to install the `trap` handler so the SIGTERM is actually ignored — but there is no programmatic check that the trap is in place. On a contended CI runner the trap may not be set when SIGTERM arrives, in which case the test would still pass (process dies on SIGTERM, elapsed stays under 5s), but for the wrong reason: the WaitDelay/SIGKILL escalation under test was never exercised. The assertion `elapsed > 5*time.Second` doesn't distinguish "killed by SIGTERM because trap not ready" from "killed by SIGKILL escalation". The bug class slipping through: a regression that breaks the WaitDelay→SIGKILL path will be masked by SIGTERM still working.
- **Recommendation**: Either (a) use a more deterministic child that signals readiness on stdout before sleeping (e.g. `trap '' TERM; echo READY; sleep 300`) and have the test read `READY` from `cmd.Stdout` before cancelling, or (b) assert `elapsed >= cmd.WaitDelay` (≥2s) to confirm the escalation actually had to happen, rather than just the upper bound.

## 3. cmd-package tests mutate package-level flag globals with save/restore

- **Location**: `cmd/all_test.go`, `cmd/cost_test.go`, `cmd/code_test.go:29-30,71-72`, `cmd/exec_test.go:69-74`, `cmd/cat_test.go`, `cmd/report_test.go`, `cmd/review_test.go`, `cmd/model_override_combined_test.go` (8 files use the save/restore pattern; verified via grep)
- **Severity**: MEDIUM
- **Effort**: LARGE
- **Description**: Each test snapshots package-level flag vars (`orgFlag`, `allRoles`, `allReportProfile`, `execDryRun`, `execAgent`, etc.), assigns its own values, and restores them in a `defer`. This works only because Go runs tests in a package serially by default. The pattern is fragile in three ways: (a) any future `t.Parallel()` call in `cmd/` will silently race on dozens of globals; (b) if a test fails with `t.Fatal` from a goroutine started inside the test, the defer still runs, but if the process is `os.Exit`-ed (`cmd.Execute` paths) the next test runs against partially-set state; (c) the save/restore is duplicated 8+ times per file and easy to get out of sync when new flags are added. The bug class slipping through: silent cross-test interference if anyone enables `t.Parallel()` or adds a new flag and forgets one of the save/restore lists.
- **Recommendation**: Push the flag values into option structs passed to `runX(opts XOptions)` functions (the codebase already does this for `runCode(CodeOptions{...})` in `cmd/code_test.go:37` — extend the pattern to `runAll`, `runExec`, `runCost`, etc.). The tests then construct an options struct locally and don't touch package globals.

## 4. `captureStdout` replaces global `os.Stdout`

- **Location**: `cmd/exec_test.go:16-30` (the helper). 40 call sites across 12 files: `cmd/all_test.go` (6), `cmd/cost_test.go` (5), `cmd/db_lifecycle_test.go` (6), `cmd/secret_test.go` (5), `cmd/model_override_combined_test.go` (4), `cmd/report_test.go` (4), `cmd/code_test.go` (2), `cmd/review_test.go` (2), `cmd/cat_test.go` (2), `cmd/exec_test.go` (2), `cmd/inspect_test.go` (1), `cmd/export_test.go` (1)
- **Severity**: MEDIUM
- **Effort**: LARGE
- **Description**: The helper reassigns `os.Stdout` to a pipe, runs the function, then restores. Same parallel-safety concern as #3: two concurrent tests calling `captureStdout` would corrupt each other's output and likely deadlock on the pipe. It also means tests can't easily exercise the `--quiet` vs `--verbose` paths because everything must go through `os.Stdout`. The bug class slipping through: a future change that adds `t.Parallel()` to one of these tests will produce inscrutable output corruption rather than a clean failure.
- **Recommendation**: Have the commands accept an `io.Writer` (`cmd.SetOut(...)` from cobra is the obvious lever) and write to it instead of `os.Stdout`. Tests then pass a `&bytes.Buffer{}` directly. This eliminates both the global mutation and the pipe goroutine.

## 5. `TestCodeDryRunSupervisorAgentOverride` captures stdout but never inspects it

- **Location**: `cmd/code_test.go:54-92`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The test sets `CodeOptions{SupervisorAgent: "mock", Agent: "mock", DryRun: true}` and only asserts `runErr == nil`. The output is captured via `captureStdout` (line 78) but discarded. The test name claims to verify the supervisor-agent override; in practice any code path that doesn't return an error passes, including one where `SupervisorAgent` is silently ignored. Compare with `TestCodeDryRunAgentInjection` (immediately above at line 12) which *does* assert `--agent mock` appears in the captured output (line 49) — that's the assertion this test is missing for the supervisor path. The bug class slipping through: a regression where `SupervisorAgent` is ignored or overwritten by `Agent`.
- **Recommendation**: Assert that the dry-run output contains `--supervisor-agent mock` (or whichever flag the supervisor path emits), mirroring the sibling test. Without that, this test only covers "doesn't panic."

## 6. `TestRunExecDryRunNoExec` asserts only `err == nil`

- **Location**: `cmd/exec_test.go:52-92`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Sets up a full project, configures `execDryRun = true`, runs `runExec`, captures stdout (line 83), ignores it, and only checks `runErr == nil`. The dry-run command is supposed to print a Profile/Agent/CLI summary; this test would pass even if dry-run silently returned without emitting anything. The neighbouring `TestPrintExecDryRun` at line 32 *does* assert on the output (`"mock"`, `"dry-run"`, `"Profile:"`, `"hello world"`), so the unit-level dry-run formatting is covered — what's lost here is the integration that wires `runExec`'s lookups to `printExecDryRun`.
- **Recommendation**: Assert that the captured output contains at least one of the markers `TestPrintExecDryRun` asserts on (`"dry-run"`, `"Profile:"`, the prompt body). Otherwise this test is a "did not error" coverage stub.

## 7. `internal/eval/worktree_test.go` and `internal/gitutil/gitutil_test.go` shell out to real `git`

- **Location**: `internal/eval/worktree_test.go:17-35` (and `initGitRepo`/`newTestEnv` helpers); `internal/gitutil/gitutil_test.go:11-19` (`repoRoot` walks up to the actual repo using `dir + "/../.."`); `TestGetProjectMeta` at line 21 uses it.
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: These tests are documented as unit tests (no build tag, run by default `make test`) but require `git` on PATH. `gitutil_test.go:60-63` does `t.Skip` if git is missing for the temp-repo tests; `eval/worktree_test.go` does not — it will hard-fail without git. `TestGetProjectMeta` also depends on the test being run from inside the actual checkout (it walks `../..` to find the repo) — not portable to tarball / detached test environments. Local-vs-CI divergence risk: a developer who runs tests in a sandbox without git will see real failures while CI passes.
- **Recommendation**: Either (a) gate these with `//go:build git_integration` and document the dep, or (b) add the `t.Skip("git not available")` pattern to every test in `eval/worktree_test.go` so the default suite still runs cleanly without git. For `TestGetProjectMeta`, use `initTempRepo`-style isolation rather than `../..`.

## 8. `TestParallelPoolSequentialExecution` couples the test to mock-delay timing

- **Location**: `cmd/parallel_test.go:339-371`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Three tasks with `MockAgent{Delay: 10ms}`, `maxParallel=1`, asserts `elapsed >= 25ms` (comment says expected 30ms, assertion is 25ms — already lenient). The direction is safe (slow runner can't make elapsed smaller), but the test is tightly coupled to the mock's `Delay` field; if `MockAgent.Delay` is renamed or repurposed, the test stops checking what it claims to check while still passing the numeric bound. Compare with the more robust `concurrencyTrackingAgent` in `internal/runner/pool_test.go:14-65`, which counts concurrent invocations via atomics rather than timing.
- **Recommendation**: Adopt the same pattern: track concurrent invocations on the agent and assert `maxConcurrent == 1` when `maxParallel == 1`, rather than relying on `elapsed`.

## 9. Missing assertion edge case for `MaskValue` boundary

- **Location**: `internal/secret/secret_test.go:13-40` (`TestMaskValueShort`, `…Medium`, `…Long`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `TestMaskValueShort` tests lengths 0, 1, 4, 8 (all asserted to map to `"***"`); `TestMaskValueMedium` tests length 9; `TestMaskValueLong` tests length 29. The boundary between "short" and "medium" is at exactly 9 chars based on the existing inputs, and there is a test at length 9 — but the medium/long boundary (between 9 and 29) is unverified. An off-by-one in the `len(val) >= N` check that selects the long-form output would not break the existing cases. Note that this is a "missing edge case on covered code" — `MaskValue` is covered — not a test-gap finding.
- **Recommendation**: Add `MaskValue` cases at the exact medium/long transition length (locate the boundary in `secret.go` and test `boundary-1` and `boundary` directly) so an off-by-one regression in the mask-length selector fails a test.

# Quick Wins

1. **Add assertion to `TestCodeDryRunSupervisorAgentOverride`** (#5): one extra `strings.Contains(out, "--supervisor-agent mock")` line turns a no-op test into a real one. SMALL effort, MEDIUM severity.
2. **Add assertion to `TestRunExecDryRunNoExec`** (#6): same fix — one `strings.Contains` line on the captured stdout. SMALL/LOW.
3. **Replace `time.Sleep(200ms)` in `TestTailerGrowingFile`** (#1) with a "wait for first event in buf" poll. SMALL effort, MEDIUM severity, removes the only real timing-flake risk in `internal/runner/`.
4. **Tighten `TestConfigureProcessLifecycle_KillsTreeOnCancel`** (#2) by asserting `elapsed >= 2s` (the WaitDelay) so a regression collapsing back to plain SIGTERM is caught.

# Project Context

- **Language**: Go (`go.mod` at root, sqlite via `modernc.org/sqlite`).
- **Test commands**: `make test` runs `go test -race ./...`. Docker integration tests use build tags `docker_integration` / `docker_live` and are invoked via `make test-docker` / `make test-docker-live`. Documented and consistent.
- **Test layout**: co-located `*_test.go` next to source (81 files). Integration variants gated by build tags. No `testdata/` lock-in problems observed.
- **Reliability primitives in use**: `t.TempDir()`, `t.Cleanup`, `t.Setenv` (only 5 files; none combined with `t.Parallel()`), `atomic.Int64` for race-counters, real SQLite (`modernc.org/sqlite` in-memory or file-backed).
- **Sleep audit (current)**: only two `time.Sleep` sites in any `*_test.go` — `internal/runner/tailer_test.go:78` and `internal/agent/cancel_test.go:38` (both covered above).
- **Key strengths**:
  - `internal/runner/race_test.go` — explicit, well-commented regression tests for past data-race bugs, with cite-the-bug headers.
  - `internal/streamutil/parse_test.go:44` — defensive `TestParseClaudeLineRecoversPanic` guarding a real Go 1.26.2 stdlib panic.
  - `internal/agent/codex_test.go:136-171` (`TestCodexResultCostUsesCachedRate`) — asserts both the *correct* value and a *meaningfully-different inflated* value to catch silent regressions.
  - `internal/web/helpers_test.go:378-420` — concurrent `getDB` test using a `start` channel for synchronized goroutine launch, no `time.Sleep`.
- **Key weak spots**: `cmd/` package — heavy reliance on package-level flag globals and `os.Stdout` capture (findings #3, #4). 40 `captureStdout` call sites across 12 files; 8 files use the save/restore-flag pattern.
- **No assertion library**: tests use stdlib `t.Errorf` / `t.Fatalf` — no `testify` / `require` calls in the repo. Custom helpers (`withChdir`, `setupTestProject`, `newTestRunner`, `newCmdTestRunner`) live next to the tests that use them; no shared `assert.Eventually`-style waiting helper exists, which is felt by findings #1 and #2.
- **Files most worth re-reading next cycle**: `cmd/all_test.go`, `cmd/cost_test.go`, `cmd/code_test.go`, `cmd/exec_test.go` (flag-global pattern); `internal/runner/tailer_test.go` (sleep-based sync); `internal/agent/cancel_test.go` (subprocess timing); `internal/eval/worktree_test.go` (real-git dependency).
