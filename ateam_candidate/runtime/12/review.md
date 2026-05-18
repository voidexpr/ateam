# Supervisor Review — 2026-05-14_06-25-49

### Project Assessment

The Go test suite is healthy on fundamentals — `make test` runs `go test -race ./...` cleanly, ~680 tests sit on `t.TempDir()`/`t.Cleanup`, and the `internal/runner` race regressions are explicitly guarded. The concentrated weaknesses are not in *how* tests are written but in *what* is asserted: many user-facing CLI entry points have no smoke test of their `runX` function, several documented behaviors (parallel prompt assembly, exec stdin, `roles --docs` row shape, `version` output, `install` bootstrap) have no asserting test at all, and three high-blast-radius areas — Claude auth credential selection, the prompt assemblers for `code`/`auto-setup`/`exec --auto-debug`, and nine `internal/web` GET routes — are at 0% coverage. This cycle's priorities focus on plugging the highest-leverage gaps with small, locally-shaped tests rather than introducing new test infrastructure.

### Priority Actions

#### 1. Add `escapeTableCell` + `printRolesDocs` row-shape regression tests

- **Action**: Add `TestEscapeTableCell` in `cmd/roles_test.go` covering `|` → `\|`, `\n` → space, both combined, and pass-through. In the same file add `TestPrintRolesDocsRowShape` that calls `printRolesDocs()` with stdout captured and asserts every row line beginning with `| ` contains exactly 5 `|` separators (4 cells), iterating over every built-in role — not just whatever ROLES.md ships with today.
- **Source Role**: test.recent (2026-05-14_06-19-38) + test.blackbox (2026-05-14_06-25-45)
- **Source Report**: .ateam/roles/test.recent/report.md, .ateam/roles/test.blackbox/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Two independent reviewers flagged the same gap. Commit `ccd5003` shipped a bug fix for a real corruption (`code.structure`'s description with `|` broke the table) without a regression test. The two tests together pin both the helper's contract and the structural invariant of the rendered output — the unit test fails fast on the helper, the integration test catches any future role description introducing an unescaped `|` or newline.

#### 2. Add `TestParallelCommonPromptAssembly`

- **Action**: In `cmd/parallel_test.go`, add a test that runs `runParallel` with `parallelDryRun=true`, two prompts, `--common-prompt-first "CTX"`, `--common-prompt-last "POST"`, captures stdout, and asserts the rendered prompts equal `CTX\n\nprompt-1\n\nPOST` and `CTX\n\nprompt-2\n\nPOST`. Add three more cases: only-first set, only-last set, neither set (no separators added). Reuse the dry-run capture pattern from `TestParallelPoolWithMockAgent`.
- **Source Role**: test.blackbox (2026-05-14_06-25-45)
- **Source Report**: .ateam/roles/test.blackbox/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: `COMMANDS.md:437` makes the `first + "\n\n" + prompt + "\n\n" + last` separator format a public contract; today none of the 12 tests in `cmd/parallel_test.go` asserts the assembled shape. A change from `\n\n` to `\n`, or swapping the order, would silently shift every multi-prompt invocation. Single small test guards a documented contract.

#### 3. Add `TestExecReadsStdinWhenNoArg` (+ negative case)

- **Action**: In `cmd/exec_test.go`, add a test that replaces `os.Stdin` with a `*os.File` from `os.Pipe()` writing a fixed prompt, runs `runExec` in dry-run mode with no positional argument, and asserts the resolved prompt body contains the piped text. If `stdinIsPiped()` is hard to override under `--dry-run`, inject the stdin source via the package-level pattern already used elsewhere in `cmd/`. Pair with a negative case: no arg + no piped stdin returns the documented error `"no prompt provided: pass a prompt, @file, or pipe via stdin"`.
- **Source Role**: test.blackbox (2026-05-14_06-25-45) + test.gaps (2026-05-14_06-23-56)
- **Source Report**: .ateam/roles/test.blackbox/report.md, .ateam/roles/test.gaps/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: `git diff | ateam exec` is a primary entrypoint advertised in README "Tips and Tricks" and `COMMANDS.md`; it currently has zero coverage. A regression makes `ateam exec` print "no prompt provided" or hang silently — exactly the kind of bug only manual testing catches today.

#### 4. Tighten weak assertions on two existing tests

- **Action**: In `cmd/code_test.go`, extend `TestCodeDryRunSupervisorAgentOverride` (around line 54) to assert that the captured dry-run stdout contains `--supervisor-agent mock` (mirroring the sibling `TestCodeDryRunAgentInjection` which already asserts on `--agent mock`). In `cmd/exec_test.go`, extend `TestRunExecDryRunNoExec` (around line 52) to assert that the captured stdout contains the same markers `TestPrintExecDryRun` checks (`"dry-run"`, `"Profile:"`, or the prompt body).
- **Source Role**: test.quality (2026-05-14_06-22-32)
- **Source Report**: .ateam/roles/test.quality/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Both tests today only check `err == nil` — they pass even if the behavior they're named after is silently broken. Adding one `strings.Contains` per test converts no-op coverage into real coverage. Trivial change, fixes a misleading test name.

#### 5. Cover the Claude auth credential pipeline

- **Action**: Add unit tests in `internal/agent/claude_auth_test.go` for the credential-selection functions currently at 0% (`DetectAuth`, `BuildCleanEnv`, `EnsureClaudeState`, `Cleanup`, `Conflicts`, `ResolveRefreshToken`). Use `t.Setenv` + `t.TempDir()` against a fake `HOME`. Concrete cases: `TestDetectAuth_PrefersEnvOverFile`, `TestBuildCleanEnv_OAuthStripsAPIKey`, `TestBuildCleanEnv_RegularStripsBoth`, `TestEnsureClaudeState_CreatesFile`, `TestEnsureClaudeState_PreservesExisting`, `TestResolveRefreshToken_EnvBeatsSecretBeatsFile`, `TestConflicts_AllTargets`, `TestCleanup_PreservesSettings`. Skip `hasKeychainEntry` (shells out to `security`).
- **Source Role**: test.gaps (2026-05-14_06-23-56)
- **Source Report**: .ateam/roles/test.gaps/report.md
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: Highest-blast-radius 0% surface area in the project. `BuildCleanEnv` decides which env vars enter the spawned agent — a regression that fails to strip `ANTHROPIC_API_KEY` for OAuth runs silently bills the wrong account. `DetectAuth` returning the wrong method produces confusing "no auth" failures. Pure functions, no network, easy fixture story.

#### 6. Cover the core prompt assemblers

- **Action**: Add tests in `internal/prompts/prompts_test.go` for `AssembleRoleCodePrompt` (line 131), `AssembleCodeManagementPrompt` (line 468), `AssembleAutoSetupPrompt` (line 544), and `AssembleExecDebugPrompt` (line 566). Mirror the existing `TestAssembleRolePromptIncludesPreviousReport` setup — minimal role on-disk, call assembler, assert (a) project info appears once at the top, (b) the role/supervisor prompt body is present, (c) extra-prompt is appended at the bottom, (d) `{{SOURCE_DIR}}` is replaced. Extend `AssembleReviewPrompt` coverage (currently 14.6%) with cases for the `customPrompt` branch and the default-supervisor branch with a mix of reports.
- **Source Role**: test.gaps (2026-05-14_06-23-56)
- **Source Report**: .ateam/roles/test.gaps/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: These four functions produce every prompt sent to the LLM for `ateam code`, `ateam auto-setup`, and `ateam exec --auto-debug`. The sibling `AssembleRolePrompt` test gives a copy-paste pattern. A regression where a section header changes or an extra-prompt is silently dropped doesn't crash — the agent just produces subtly wrong output.

#### 7. Smoke tests for top user-facing CLI commands

- **Action**: Add one happy-path smoke test per command for the four highest-traffic untested entry points: `runVerify` (chained automatically by `code` and `all`), `runEval` (substantial flag matrix), `runTail` (mirror the `cmd/inspect_test.go` selection pattern with stubbed DB), and `runInstall` (assert produced `.ateamorg/` contains `defaults/runtime.hcl`, `defaults/Dockerfile`, and at least one `defaults/roles/<NAME>/report_prompt.md`). Use the existing dry-run + mock-agent pattern from `cmd/all_test.go`/`cmd/code_test.go`.
- **Source Role**: test.gaps (2026-05-14_06-23-56) + test.blackbox (2026-05-14_06-25-45)
- **Source Report**: .ateam/roles/test.gaps/report.md, .ateam/roles/test.blackbox/report.md
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: Both reviewers flag the same group. `verify` is the most painful: a regression there breaks every `code → verify` chain. The bar is "command parses flags without error and reaches the first observable side-effect" — enough to catch wiring regressions when a flag is renamed or a `MarkFlagsMutuallyExclusive` invariant changes. Defer the larger untested-cmd list to a future cycle.

#### 8. Cover `internal/web` handlers at 0%

- **Action**: Add `httptest`-driven tests in `internal/web/handlers_test.go` for the nine handlers at 0%: `handleHome`, `handleReports`, `handleReportHistory`, `handleSupervisorHistory`, `handleSessions`, `handleSessionDetail`, `handleCodeSessions`, `handleCodeSessionDetail`, `handleCodeSessionFile`. Follow the existing `TestHandleOverviewReturnsOK`/`TestHandleOverviewNotFound` pattern — one ReturnsOK + one NotFound per handler. Prioritize the path-parameter handlers (history/sessions/code-session group) and `handleHome` (sole entry point when `--single-project` is off).
- **Source Role**: test.gaps (2026-05-14_06-23-56)
- **Source Report**: .ateam/roles/test.gaps/report.md
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: Sibling handlers (`handleOverview`, `handleReport`, `handleRun`, `handleRunFile`, `handleCost`, `handleReview`, `handleVerify`, `handlePrompts`) are all ≥90% covered using the same pattern — these nine are conspicuous gaps in an otherwise well-tested file. `handleCodeSessionFile` takes a file-path parameter so coverage there also guards against path-traversal regressions.

#### 9. Add `version`, `env --print-org`, and `env --claude-sandbox` output tests

- **Action**: Add `TestVersionCommandPrintsAllFourLines` in `cmd/version_test.go` asserting the output contains exactly one line each prefixed `ateam:`, `commit:`, `built:`, `system:`. Add `TestEnvPrintOrg` (in `cmd/db_lifecycle_test.go` or a new `cmd/env_test.go`) asserting `ateam env --print-org` writes exactly one trailing-newline-stripped absolute path equal to the fixture's org-dir. Add `TestEnvClaudeSandboxIsValidJSON` asserting the output `json.Unmarshal`s into `map[string]any` and contains the documented top-level keys.
- **Source Role**: test.blackbox (2026-05-14_06-25-45)
- **Source Report**: .ateam/roles/test.blackbox/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: All three outputs are user-scriptable contracts. `ateam version` is the documented bug-reporting format. `env --print-org` is referenced in `COMMANDS.md:355` inside a shell command users copy. `env --claude-sandbox` is referenced in `CONFIG.md:127`. Each test is a single `strings.Contains` / `json.Unmarshal` line — tiny code, real protection against silent format changes.

#### 10. Cover `writeSettings` and `ResolveDockerfile`

- **Action**: Add a test for `internal/runner/runner.go:814` (`writeSettings`) that calls it with a temp dir and asserts the produced JSON is valid and contains expected keys (allowWrite includes workDir, denyWrite includes the settings path itself). Add a test for `internal/runtime/config.go:430` (`ResolveDockerfile`) exercising each of the 5 fallback levels using temp dirs.
- **Source Role**: test.gaps (2026-05-14_06-23-56)
- **Source Report**: .ateam/roles/test.gaps/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Both functions sit on the sandbox/container boundary. `writeSettings` produces the merged Claude Code settings.json that defines the agent sandbox; `mergeSandboxPaths` is covered but the actual write path is not. `ResolveDockerfile` decides which Dockerfile builds the agent image — a regression silently swaps the order or skips the embedded fallback. ~20 minutes each.

### Deferred

- **`TestTailerGrowingFile` `time.Sleep(200ms)` replacement and `TestConfigureProcessLifecycle_KillsTreeOnCancel` lower-bound assertion** (test.quality #1, #2). Real reliability issues but not currently flaking. Worth doing when the area is touched for other reasons; not worth a dedicated cycle now. The 2-second outer timeout absorbs the slack.
- **Refactor `cmd/` package to stop mutating package-level flag globals and `os.Stdout`** (test.quality #3, #4). Large effort (≥48 save/restore sites across 8 files plus ~44 `captureStdout` calls across 13 files), only matters once someone enables `t.Parallel()` in `cmd/`. The correct fix (push values into option structs / accept `io.Writer` via `cmd.SetOut`) is the right direction but deferred until the parallel-tests use case actually materializes.
- **Real-git dependency in `internal/eval/worktree_test.go` and `internal/gitutil/gitutil_test.go`** (test.quality #7). Low impact on current CI (git is present) and on developer machines (git is universally installed). The `t.Skip` pattern in `gitutil_test.go` is the right approach when this becomes painful; deferred until then.
- **`MaskValue` boundary edge cases** (test.quality #9). Nit-level; bundle with other secret-package work when it next happens.
- **`TestParallelPoolSequentialExecution` switch from timing to atomic counters** (test.quality #8). Direction-safe today; worth doing when the file is touched.
- **Extend `test/cli/` smoke harness** (test.blackbox #13, test.gaps section "End-to-end CLI test inventory"). Real value but MEDIUM effort and a parallel test infrastructure — better to plug the highest-priority gaps first as in-process Go tests, then revisit whether a shell-level harness is the right home once the inventory has filled in. The existing `test-auth-combos.sh` already covers the only end-to-end shape that has a current regression history (auth).
- **Remaining untested CLI commands**: `prompt`, `serve`, `update`, `runs`, `projects`, `project-rename`, `container-cp`, `auto-setup`, `runs --git-hash columns`, `init --name/--role/--auto-setup`, `update --diff` (test.blackbox #6, #7, #9, #10, #11, #12, test.gaps CLI section). Tracked for the next cycle after the P1 group lands; sequencing-wise these should follow the prompt-assembler and `verify`/`eval`/`tail`/`install` work in priority action #7.
- **`CodexAgent.Run` end-to-end test** (test.gaps Finding "CodexAgent.Run / run untested"). MEDIUM effort, pattern from `ClaudeAgent.run` test is reusable; defer until codex is touched or until the higher-impact prompt-assembler and claude-auth tests land.
- **Trivial 0% cluster** (test.gaps Finding "Cluster: minor 0% helpers"). Explicitly listed as "skip in future cycles." No action.
- **`ateam export` three-tab / anchor-nav structural test** (test.blackbox #4). Real gap but the existing `internal/web/export_test.go` covers basic shape; defer until a regression is observed or after the high-priority work lands.

### Conflicts

No direct contradictions between roles. The four reports are highly complementary:

- test.recent flagged the `escapeTableCell` gap narrowly (helper-level unit test).
- test.blackbox flagged the same area more broadly (every-role row-shape integration test).
- These are bundled together in Priority Action #1 (helper unit + integration test in the same file). Per the supervisor preserve-tags rule, the bundled action keeps both scopes visible to the coding step.

Severity calibrations across reports are consistent — the four roles agree the project is healthy on fundamentals and the highest-leverage work is plugging specific 0% surfaces rather than introducing new infrastructure.

### Notes

- This is the first cycle for all four test-focused roles on this branch (no prior history files referenced by any report). The reviews lean toward concrete asserting tests rather than tooling proposals, which matches the project's current "coverage tooling is on-demand, no CI" stance documented in `defaults/role.test.gaps.md`.
- A pattern worth naming: when a role keeps re-finding 0% on `cmd/<X>.go runX` functions cycle after cycle, the right structural answer is the option-struct refactor noted in the Deferred section. Deferring it is correct *now* (no parallel-tests need); revisit if the `cmd/` test count keeps growing without that refactor landing.
- Sequencing within P0: do #1 (escapeTableCell) and #4 (weak-assertion fixes) first — both are sub-30-minute changes that close the smallest gaps. #2 (parallel) and #3 (exec stdin) are the next-cheapest. The P1 block can be done in any order; the prompt assemblers (#6) and the claude-auth pipeline (#5) are the highest-blast-radius items, so prioritize those over the web handlers if effort is limited.
- The four reports cite the same helper utilities (`captureStdout`, `withChdir`, `setupReviewFixture`, the DB-seeding helpers) as the right reuse surface for new tests. No new test infrastructure should be introduced this cycle.
