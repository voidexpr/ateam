# Summary

The ateam test suite is in good shape overall: ~46% test code (20.6k / 44.6k LoC), 81 `_test.go` files, race-enabled default (`go test -race ./...`), table-driven tests in most packages, dedicated regression-marker tests (`bugs_test.go`, `race_test.go`), and an explicit `MockAgent` that lets the runner be exercised without spawning Claude/Codex. Build-tagged isolation (`docker_integration`, `docker_live`) cleanly separates hermetic Go tests from environment-dependent ones, and a DinD harness (`test-docker`) makes container tests reproducible. The main improvement opportunities are: (1) no test in the repo calls `t.Parallel()` despite many being short, pure, and shareable; (2) every cmd-level test mutates package-level Cobra-flag globals with hand-rolled save/restore stanzas which both blocks parallelism and risks drift; (3) two tests rely on `time.Sleep` for "wait for goroutine to do X" instead of channel-based coordination; (4) the giant `Runner.Run` (440 LOC, one function) is exercised end-to-end but not in phases, so failure modes inside it can only be reproduced by hitting the whole pipeline; (5) the CLI integration entrypoint is a single hand-rolled bash script that is hard to extend; and (6) there is no `make coverage` target so the project has no visibility into where coverage actually drops.

Audit performed by role `testing_full` using model **Claude Opus 4.7** (`claude-opus-4-7`), default thinking, read-only analysis (no files modified).

# Findings

## 1. No test in the suite uses `t.Parallel()` — entire suite runs serially

- **Location**: search `t.Parallel\(\)` across `**/*_test.go` → 0 hits
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: With ~81 test files and 20k LOC of tests, zero tests opt into parallel execution. Many tests are pure (e.g. `internal/runner/format_test.go`, `internal/runner/format_stream_test.go`, `internal/runner/template_test.go`, `internal/agent/pricing_test.go`, `internal/agent/claude_test.go`, `internal/web/is_path_within_test.go`, `internal/secret/secret_test.go`, `cmd/version_test.go`, `cmd/table_test.go`) and would gain immediate wall-clock speedup. `go test -race ./...` already runs packages in parallel, but inside each package every test serializes. As the suite grows this will start to matter more.
- **Recommendation**: Add `t.Parallel()` to all tests that use only `t.TempDir()` or pure inputs. Two gotchas to keep in mind: (a) tests that mutate `cmd/*` package globals (see finding 2) must stay serial; (b) tests that use `t.Setenv` are automatically marked non-parallel by `testing`, so no change needed there. Consider a Go-specific lint rule (golangci-lint `paralleltest`) to encourage it for new code.

## 2. `cmd/*_test.go` files mutate package-level flag globals with hand-rolled save/restore

- **Location**: `cmd/all_test.go:30-55`, `cmd/exec_test.go:65-80`, `cmd/code_test.go`, `cmd/cost_test.go`, `cmd/report_test.go`, `cmd/inspect_test.go`, `cmd/cat_test.go`, `cmd/review_test.go`, `cmd/model_override_combined_test.go`, `cmd/db_lifecycle_test.go`, `cmd/export_test.go`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Cobra flags are stored as package-level vars (`orgFlag`, `execRole`, `execDryRun`, `execQuiet`, `execAgent`, `allRoles`, `allReportProfile`, etc.). `cmd/all_test.go` alone saves and restores 25 globals before/after a single test. The pattern is fragile (any new flag added to the `runAll` path requires editing all tests that go through it), opaque (the actual test intent is buried in 30 lines of setup/teardown), and inherently serial — running two such tests in parallel would scribble over each other. The same trade-off keeps `cmd` tests from ever using `t.Parallel()`.
- **Recommendation**: Refactor option-bearing functions to accept an explicit `Opts` struct instead of reading package globals. `runCode` already does this (`runCode(CodeOptions{…})` per `cmd/code_test.go:36`) — extending the same pattern to `runExec`, `runAll`, `runReport`, `runReview`, `runVerify` would eliminate the save/restore boilerplate. The Cobra hook stays a one-liner that copies flag vars into an Opts struct and calls the action. This unblocks `t.Parallel()` for that package too.

## 3. Two tests rely on `time.Sleep` for goroutine coordination

- **Location**: `internal/runner/tailer_test.go:78` (`time.Sleep(200ms)` then write to file), `internal/agent/cancel_test.go:38` (`time.Sleep(100ms)` then cancel context)
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Both tests work today but they encode a "this much wall-clock should be enough" assumption that fails under load (CI, `-race` overhead, slow VMs). The tailer test in particular polls a file at 50ms with a 2s deadline and writes after a 200ms `time.Sleep` — under heavy load the tail loop can miss the window. These are exactly the tests that go flaky in CI after the project has been stable for a year.
- **Recommendation**: For `tailer_test.go`, replace `time.Sleep(200ms)` with a `chan struct{}` signaled after the tailer has consumed the partial file (e.g. expose a hook that fires when the first line is parsed). For `cancel_test.go`, you can cancel inline before `cmd.Wait()` instead of from a goroutine — the WaitDelay mechanic doesn't need the test to "wait then cancel"; it tests the SIGKILL escalation regardless of timing.

## 4. `Runner.Run` (440 LOC, ~10 responsibilities) is only tested end-to-end

- **Location**: `internal/runner/runner.go:195-632` (the `Run` method), tested via `internal/runner/runner_test.go:16-501`
- **Severity**: MEDIUM
- **Effort**: LARGE
- **Description**: All 15 tests in `runner_test.go` invoke `r.Run(...)` and assert on `RunSummary` or side-effect files. That's good as a smoke test but means a bug in the stall watchdog, the cumulative-progress accumulator, the fallback usage extractor, the file-promotion step, or the per-exec settings.json renderer can only be reproduced by exercising the whole pipeline with a tailored mock agent. New tests need a fresh mock per scenario (`fakeProgressAgent`, `concurrencyTrackingAgent`, `fakeContainer` are all bespoke). Refactor finding #1 in the existing architecture report (split `Run` into `prepare`/`execute`/`finalize`) directly enables much smaller, more focused unit tests.
- **Recommendation**: When the architecture report's phase split is done, add targeted tests for each phase: `prepareRun()` (DB insert + dir creation + settings rendering), `executeRun()` (event consumption + stall + classification), `finalizeRun()` (file promotion + DB update + cmd.md rerender). Independent of the refactor, two helpers are already public enough to be unit-tested in isolation: `classifyFailure` (good — has 5 tests), `appendStderrSummary` (good — has 7 tests). `renderSettingsJSON` and `mergeSandboxPaths` have one test (`TestRenderSettingsSandboxExtra`) — split it.

## 5. CLI integration coverage is a single hand-written bash matrix

- **Location**: `test/cli/test-auth-combos.sh` (16 cases, all about auth resolution), invoked by `make test-cli`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The only end-to-end CLI test is `test-auth-combos.sh`, which is tightly scoped to ANTHROPIC_API_KEY × CLAUDE_CODE_OAUTH_TOKEN combinations on `ateam exec ping --dry-run`. There is no smoke coverage for `ateam init`, `ateam report`, `ateam review`, `ateam code`, `ateam verify`, `ateam all`, `ateam runs`, `ateam ps`, `ateam tail`, `ateam serve`, `ateam projects`, `ateam env`, `ateam roles`, `ateam config`, `ateam secret` (beyond auth), nor for the full happy-path lifecycle "init → exec → ps → cat". The bash script is also custom-built (no test framework) so adding cases requires understanding the bespoke `run_case` helper.
- **Recommendation**: Either (a) port the existing matrix and add new cases as Go integration tests using `testscript` (rsc.io/script/scripttest) — it is purpose-built for CLI golden-output testing and runs in the standard `go test` pipeline, or (b) replace the bash harness with `bats-core` and add ~10 lifecycle scenarios. Option (a) is preferred because tests run with the rest of `make test` and don't need bash. Either way, add a "happy path" lifecycle scenario: `init` → `exec` (mock agent) → `runs` → `inspect <id>` → assert outputs.

## 6. No `make coverage` / coverage measurement target

- **Location**: `Makefile` — no `coverage`, no `-coverprofile` in `test` or `test-all`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: With ~20k LOC of tests it is hard to know where the gaps actually are (this report is heuristic; coverage data would make it concrete). Cmd files like `cmd/auto_setup.go`, `cmd/env.go` (352 LOC, no test file), `cmd/runs.go`, `cmd/eval.go` (451 LOC, no test file), `cmd/projects.go`, `cmd/tail.go`, `cmd/prompt.go`, `cmd/verify.go`, `cmd/serve.go` have no `*_test.go` companion at all. Without coverage, that is invisible; some of these may have indirect coverage via integration tests, some may have none. A coverage target makes the gaps visible without adding a single test.
- **Recommendation**: Add `make coverage` that runs `go test -coverprofile=coverage.out -covermode=atomic ./...` then `go tool cover -func=coverage.out | tail -1` for a single-number summary plus optional `-html=coverage.out -o coverage.html`. Document a target threshold in CLAUDE.md (e.g. "core packages — `internal/runner`, `internal/agent`, `internal/calldb`, `internal/runtime`, `internal/root` — must stay ≥70%"). No CI enforcement needed yet; visibility alone will guide the next 3-4 finds.

## 7. Several cmd entry points have no companion `_test.go`

- **Location**: `cmd/eval.go` (451 LOC), `cmd/env.go` (352 LOC), `cmd/verify.go` (188 LOC), `cmd/prompt.go` (190 LOC), `cmd/runs.go` (149 LOC), `cmd/tail.go` (124 LOC), `cmd/auto_setup.go` (118 LOC), `cmd/serve.go` (81 LOC), `cmd/projects.go` (74 LOC), `cmd/project_rename.go`, `cmd/install.go`, `cmd/update.go`, `cmd/container_cp.go`, `cmd/pool_render_mpb.go`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: ~14 of 37 `cmd/*.go` files lack a `_test.go`. The biggest two are `eval.go` (451 LOC — a full `ateam eval` workflow that compares agent outputs) and `env.go` (352 LOC — environment introspection used by debugging). `verify.go:runVerify` is on the `ateam all` critical path and is only covered indirectly through `cmd/all_test.go`. `eval` has tests in `internal/eval/` for the library, but the cobra-level glue (flag parsing, error messages, dry-run output) is untested.
- **Recommendation**: For each, add at minimum a dry-run smoke test patterned on `cmd/exec_test.go:TestRunExecDryRunNoExec` (set flags, set up temp org+project via `root.InitProject`, capture stdout, assert key fragments appear). Start with `eval`, `verify`, and `env` — those have the most LOC and the highest blast radius if they regress.

## 8. `ResolveAgentTemplateArgs` race regression test is per-impl — easy to forget for a 3rd agent type

- **Location**: `internal/runner/race_test.go:30-90` (TestResolveAgentTemplateArgsConcurrentRace)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The race test enumerates `claude` and `codex` agent types via a hand-coded slice. If a third agent (e.g. a future `gemini`) is added, nothing forces a corresponding entry. The same shape applies to `cmd/runner_overrides_test.go` and any agent-agnostic test. A new `Agent` impl will silently skip the race coverage.
- **Recommendation**: Either drive the test from a registry (`agent.RegisteredAgents()` style) so every implementation gets the same suite, or add a TODO comment at the `Agent` interface ("any new impl: add to race_test.go cases"). The registry approach is preferred — it future-proofs the suite.

## 9. Test discovery for `docker_integration` / `docker_live` is opaque

- **Location**: `internal/container/docker_integration_test.go:1`, `internal/container/docker_live_test.go:1`, `test/Dockerfile.dind`, `test/run-docker-tests.sh`, `Makefile:test-docker`, `Makefile:test-docker-live`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A developer reading `go test ./...` output will not realize that docker-tagged tests are silently skipped (build tags make them invisible, not "skipped"). There is no `t.Log("docker tests live behind -tags=docker_integration")` reminder anywhere, no `make help`, and no mention in `README.md` of how to run the full matrix. The current test-suite developer experience is: run `make test`, see green, assume the suite is complete.
- **Recommendation**: Add a top-level `make test-all` description to a `make help` target, and add a comment in a non-tagged file (e.g. `internal/container/docker_test.go` at top) pointing out that `docker_integration_test.go` and `docker_live_test.go` exist behind build tags. Also: `make test` currently does not include `test-cli`; consider `make test` running `test-cli` too, given how cheap it is. Or document the split.

## 10. Live Anthropic tests fail open when image build errors

- **Location**: `internal/container/docker_live_test.go:42-80` (the `buildOnce` / `ensureLiveImage` pair)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `ensureLiveImage` runs once across all tests; on failure it sets `buildErr` and `t.Fatalf`'s the calling test. But because `sync.Once` only runs once, every subsequent test that needs the image will see `buildErr != nil` and also fail — fine — but the *root cause* (a build problem) is only visible in the first test's output, and the remaining failures look like cascading symptoms. The first failure also doesn't include the build output (`exec.Command(...).CombinedOutput()` output is read but not printed unless the assertion fires in that exact ordering).
- **Recommendation**: When `buildErr` is set, every dependent test should `t.Skip(...)` (not `Fatal`) with a message pointing to the original build log path that `ensureLiveImage` saved. Bonus: write the docker build output to a file under `t.TempDir()`-equivalent and print the path in every dependent test's skip message.

## 11. `bugs_test.go` files mix passing-regression and "still-broken" cases inconsistently

- **Location**: `internal/runner/bugs_test.go`, `internal/streamutil/bugs_test.go`, `internal/agent/bugs_test.go`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: These files follow a `===== REGRESSION: <bug name>` documentation pattern, which is great. But there is no convention for "the bug is still open" — i.e., a known-failing test that documents the regression. Today, all `bugs_test.go` tests pass. If a regression were found tomorrow, it would either be added as a passing test (after the fix lands, losing the "I found a bug" history) or as `t.Skip` with a comment (losing the failing signal). Project consistency comment from CLAUDE.md asks contributors not to duplicate code; this convention deserves an explicit policy.
- **Recommendation**: Document the policy at the top of each `bugs_test.go` (or in CLAUDE.md): "regression tests must remain passing; for known-open bugs use `t.Skip` with `[OPEN BUG #…]` prefix so they show up in `go test -v` output and can be grep'd". Optionally, use Go 1.22 `t.Run("…", ...)` subtests to group fix/known-bug separately.

## 12. `make test-docker` rebuilds the DinD image on every invocation

- **Location**: `Makefile:test-docker` (calls `docker buildx build` / `docker build` unconditionally)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A clean DinD container test run takes minutes because the Dockerfile (`test/Dockerfile.dind`) does a full `go mod download` + `go build` + `go test -run ^$` inside the modules stage. Docker layer caching helps after the first run, but `--load`/`--builder` plumbing varies between hosts and incremental dev loops are still slow. The race-instrumented build (`build-binary-race` etc.) is invoked separately and is even slower.
- **Recommendation**: Wire the stage-1 module cache to a named buildx cache (`--cache-to=type=local,dest=…,mode=max --cache-from=…`) gated behind a make var (e.g. `BUILDX_CACHE_DIR ?= /tmp/ateam-buildx-cache`). Skip if env var unset. This is the single biggest dev-experience improvement for anyone iterating on docker integration code.

## 13. `code_sessions_test.go` test relies on file mtimes which can differ from `time.Now`

- **Location**: `internal/web/code_sessions_test.go:13-30` (uses `rowTime := time.Date(2026, 3, 19, …)` then writes files and expects the runtime to match)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Tests that compare timestamps inside scan results are sensitive to FS mtime quantization (some filesystems round to 2s) and to the test machine clock. They pass today but get flaky if the directory walker switches from DB rowtime to mtime in any future change.
- **Recommendation**: Use `os.Chtimes` after writing files to pin mtimes explicitly to the value the test expects, so the test is independent of whatever the FS chose. Same recipe for any other timestamp-comparing test.

## 14. No fuzz tests on parsers that consume external input

- **Location**: `internal/streamutil/parse.go` (JSON lines from agent stream), `internal/runner/parse_stream.go`, `internal/secret/secret.go` (secrets.env parser), `cmd/review.go:parseMaxAge`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: All four parse external-ish input: the first two consume whatever the Claude/Codex CLI emits (format can change without notice), the third is user-edited, the fourth is user-typed. None have a `Fuzz*` test. A malformed stream line currently triggers `_ = json.Unmarshal(...)` (silently ignored — see the code-structure report finding #3) so a fuzz test would surface real divergences from the documented schema.
- **Recommendation**: Add small fuzz targets for the two stream parsers (seed corpus from `internal/runner/testdata/sample_stream.jsonl`) and for `parseMaxAge`. Even a 30s `go test -fuzz=Fuzz... -fuzztime=30s` per parser per CI run is enough to catch most panics.

## 15. `pool_test.go` checks max concurrency but not fairness or ordering

- **Location**: `internal/runner/pool_test.go` (TestRunPoolBasic and surrounding)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The pool tests verify (a) all tasks complete and (b) peak concurrency ≤ limit (via `concurrencyTrackingAgent`). They do not verify that the pool actually achieves the limit (i.e. that *peak concurrency = limit* when tasks > limit and tasks are slow). A regression where the pool quietly serializes (e.g. a misplaced lock) would pass the test.
- **Recommendation**: In `TestRunPoolBasic`, assert `a.maxConcurrent.Load() == int64(parallelism)` when `len(tasks) > parallelism` and the agent has a `delay` long enough that the test stays deterministic. The `concurrencyTrackingAgent` already exists for this; just tighten the assertion from "≤" to "=".

## 16. CI: no Github Actions or external CI configuration

- **Location**: project root — no `.github/workflows/`, no `.gitlab-ci.yml`, no `circleci/` etc.
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: `make run-ci` exists locally (= `check` + `vuln`), but nothing runs it automatically on a PR or push. Given the project's intent ("steady, incremental quality improvement") this is fine for now — but it means a regression that escapes local testing only gets caught on the next manual `make check`.
- **Recommendation**: This project is mature enough that the value of CI/CD now outweighs the maintenance cost. Single workflow: matrix on macOS+linux, run `make run-ci`, plus `make test-docker` on linux only. Treat this as the lowest-priority item per role guidance — useful but not blocking other work.

# Quick Wins

1. **Add `make coverage` target** (Finding #6) — SMALL effort, MEDIUM severity. Reveals what is actually under-tested instead of guessing. No risk; pure visibility.
2. **Replace `time.Sleep` in `tailer_test.go` and `cancel_test.go` with deterministic signaling** (Finding #3) — SMALL effort, MEDIUM severity. Removes the two most likely future flakes from a currently-stable suite.
3. **Add `t.Parallel()` to pure tests in `internal/runner`, `internal/streamutil`, `internal/agent`, `internal/secret`, `internal/display`, `internal/web`** (Finding #1) — MEDIUM effort but mechanical. Significant wall-clock speedup; race detector still works inside packages.
4. **Tighten `pool_test.go` to assert exact peak concurrency** (Finding #15) — SMALL effort, LOW severity. Catches "pool secretly serialized" regression for free.
5. **Pin file mtimes in `code_sessions_test.go` with `os.Chtimes`** (Finding #13) — SMALL effort, LOW severity. Cheap hardening.

# Project Context

**Language & tools**

- Go 1.26+ project, single binary `ateam` built via `make build`.
- Test runner: `go test -race ./...` (race detector always on).
- No third-party assertion library — plain `t.Errorf` / `t.Fatalf` throughout.
- Coverage: not measured (Finding #6).
- Lint: `golangci-lint` (auto-installed by `make lint`).
- Vuln scan: `govulncheck` (auto-installed and run by `make vuln` / `make run-ci`).

**Test surface**

- 81 `_test.go` files, ~20.6k LOC of tests vs ~44.6k total Go LOC (~46% test code).
- 0 `t.Parallel()` calls anywhere.
- 2 `time.Sleep` usages in non-tagged tests (`tailer_test.go:78`, `cancel_test.go:38`).
- 5 `t.Skip` usages (mostly platform-conditional, one for `-short`).
- `t.TempDir()` used heavily; no `./test_data/` directory exists despite CLAUDE.md mention (that directive appears to be for manual `ateam` invocation testing, not unit tests).

**Test architecture (clear & worth preserving)**

- `MockAgent` (`internal/agent/mock.go`) is the standard test double for runner/cmd tests.
- `newTestRunner` helpers exist in both `internal/runner/test_helpers_test.go` and `cmd/parallel_test.go` (duplicated — could be shared).
- Build tags `docker_integration` and `docker_live` isolate environment-dependent tests cleanly.
- DinD harness (`test/Dockerfile.dind` + `test/run-docker-tests.sh`) gives reproducible container tests via `make test-docker`.
- Live Anthropic tests (`docker_live` tag) cost ~$0.03 per run with haiku.
- Dedicated regression markers in `bugs_test.go` files (`internal/runner/`, `internal/streamutil/`, `internal/agent/`) — well documented but no policy for known-open bugs.
- `race_test.go` in `internal/runner/` specifically exercises concurrency-sensitive paths under `-race`.

**Key files for the next testing audit**

- `Makefile` — test targets (`test`, `test-cli`, `test-docker`, `test-docker-live`, `test-all`, `check`, `run-ci`).
- `test/Dockerfile.dind`, `test/run-docker-tests.sh` — DinD orchestration.
- `test/cli/test-auth-combos.sh` — only CLI integration test; covers 16 auth combos.
- `internal/runner/runner_test.go` (501 LOC) — runner pipeline tests via MockAgent.
- `internal/runner/race_test.go` (506 LOC) — concurrency regression suite.
- `internal/runner/bugs_test.go`, `internal/streamutil/bugs_test.go`, `internal/agent/bugs_test.go` — named-regression suites.
- `internal/web/handlers_test.go` (849 LOC) — HTTP handler coverage via `httptest`.
- `internal/container/docker_integration_test.go` (build tag `docker_integration`), `docker_live_test.go` (build tag `docker_live`).
- `cmd/*_test.go` — share a `setupTestProject` helper and a `captureStdout` helper; rely heavily on package-level flag globals.

**Untested cmd entry points**

`cmd/eval.go`, `cmd/env.go`, `cmd/verify.go` (covered indirectly), `cmd/prompt.go`, `cmd/runs.go`, `cmd/tail.go`, `cmd/auto_setup.go`, `cmd/serve.go`, `cmd/projects.go`, `cmd/project_rename.go`, `cmd/install.go`, `cmd/update.go`, `cmd/container_cp.go`, `cmd/pool_render_mpb.go`.

**No previous testing_full report on disk** — this is a first-run report; nothing to merge.
