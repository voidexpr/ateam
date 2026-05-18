# Summary

The project has healthy unit-test coverage overall (55.7% statements across the module; the most safety-critical packages — `calldb`, `gitutil`, `runner`, `display`, `config`, `runtime` — all sit between 74% and 94%). The gaps that matter are concentrated in three places: (1) most user-visible CLI commands have no smoke test of their `runX` entry point, so flag wiring can break silently; (2) `internal/agent/claude_auth.go` — the code that decides which Claude credential ships into the agent process — has 0% coverage on `DetectAuth`, `BuildCleanEnv`, `EnsureClaudeState`, `Cleanup`, `Conflicts`, and `ResolveRefreshToken`; (3) the `prompts` package, which builds every prompt sent to an LLM, has 0% coverage on `AssembleRoleCodePrompt`, `AssembleCodeManagementPrompt`, `AssembleAutoSetupPrompt`, and `AssembleExecDebugPrompt`, and only 14.6% on `AssembleReviewPrompt`. Coverage tooling is already in use (`go test -coverprofile`) and `make test` runs cleanly with a single command, so the foundations are in place — the work is to plug specific unprotected paths.

# Findings

## CLI command entry points without smoke tests

- **Title**: User-facing CLI commands `eval`, `tail`, `serve`, `update`, `prompt`, `verify`, `install`, `auto-setup`, `runs`, `projects`, `project-rename`, `container-cp`, `version` have no test that exercises their `runX` entry point or flag wiring
- **Location**: `cmd/eval.go:135` (`runEval`), `cmd/tail.go:48` (`runTail`), `cmd/serve.go:44` (`runServe`), `cmd/update.go:43` (`runUpdate`), `cmd/prompt.go:57` (`runPrompt`), `cmd/install.go:24` (`runInstall`), `cmd/auto_setup.go:44` (`runAutoSetup`), `cmd/projects.go:24` (`runProjects`), `cmd/project_rename.go:43` (`runProjectRename`), `cmd/container_cp.go:35` (`runContainerCp`), `cmd/version.go:21` (`runVersion`), `cmd/runs.go` (no `runRuns` test); `verify` has only `TestVerifyWarnsWhenCheaperAndModelBothSet` (a single flag warning).
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: Each of these is a top-level cobra subcommand the user can invoke. Coverage on the `runX` function is 0%. The user-visible bug that ships if these stay at 0%: a flag is renamed, a `MarkFlagsMutuallyExclusive` invariant changes, or a wiring tweak breaks one of these commands, and `go test ./...` passes. `verify` is the most painful: it's chained automatically by `ateam code` and `ateam all`, so an undetected regression there breaks the whole "code then verify" workflow even though `model_override_combined_test.go:149` exercises one slice of `runVerify`. `eval` is the next-most-painful: it has substantial branching (worktree mode, parallel mode, `--review` mode, multi-role) and only the parsers are exercised indirectly.
- **Recommendation**: For each command, add at least one test in `cmd/` that calls the command with a controlled environment (use the existing pattern from `cmd/all_test.go`/`cmd/code_test.go`: dry-run with a mock agent), verifying (a) the command parses its flags without error and (b) reaches the expected first observable side-effect. Use existing helpers like `requireProjectDB` setup from `cmd/init_test.go`. The bar is low — a single happy-path smoke test per command is enough to catch wiring regressions. Prioritize `verify`, `eval`, `tail`, and `prompt` (used inside the supervisor loop).

## Web HTTP routes with no handler test

- **Title**: Nine `internal/web` GET routes have 0% handler coverage despite being wired in the route table
- **Location**: `internal/web/handlers.go:32` (`handleHome`, route `GET /`), `internal/web/handlers.go:199` (`handleReports`, `GET /p/{project}/reports`), `internal/web/handlers.go:784` (`handleReportHistory`, `GET /p/{project}/reports/{role}/history/{file}`), `internal/web/handlers.go:807` (`handleSupervisorHistory`, used for both `review/history` and `verify/history`), `internal/web/handlers.go:992` (`handleSessions`, `GET /p/{project}/sessions`), `internal/web/handlers.go:1021` (`handleSessionDetail`), `internal/web/handlers.go:1155` (`handleCodeSessions`), `internal/web/handlers.go:1301` (`handleCodeSessionDetail`), `internal/web/handlers.go:1373` (`handleCodeSessionFile`). Route registration: `internal/web/server.go:240-258`.
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: The web UI is one of the project's three user-visible surfaces (CLI, web, agent output). Sibling handlers like `handleOverview`, `handleReport`, `handleRun`, `handleRunFile`, `handleCost`, `handleReview`, `handleVerify`, and `handlePrompts` all have ≥90% coverage and use a consistent `httptest`-driven pattern; these nine routes are conspicuous gaps in an otherwise well-tested file. A 5xx or wrong-content regression on `/`, `/p/{project}/sessions`, `/p/{project}/code/{session}`, or any of the history routes would ship without any failing test. `handleCodeSessionFile` (line 1373) handles a file-path parameter — coverage there guards against path-traversal regressions even though `isPathWithin` is 100% covered.
- **Recommendation**: Add `httptest`-based tests following the existing pattern in `internal/web/handlers_test.go` (e.g. mirror `TestHandleOverviewReturnsOK`/`TestHandleOverviewNotFound`). One ReturnsOK + one NotFound per handler is enough; the existing test fixtures already cover Server construction. Prioritize the history/sessions/code-session group because they take URL parameters (most regression-prone) and `handleHome` (only entry point for users who skip `--single-project`).

## Claude auth pipeline unprotected

- **Title**: Critical `internal/agent/claude_auth.go` decision functions are at 0% coverage
- **Location**: `internal/agent/claude_auth.go:80` (`DetectAuth`), `:184` (`Conflicts`), `:215` (`EnsureClaudeState`), `:246` (`Cleanup`), `:292` (`BuildCleanEnv`), `:324` (`HasRefreshToken`), `:348` (`ResolveRefreshToken`), `:67` (`ClaudeConfigDir`). Sibling helpers `ParseAuthMethod`, `ValidateTarget`, `ExtractRefreshToken`, `credFileHasTokens` are well covered by `claude_auth_test.go`.
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: These functions own credential selection for every Claude agent process. `DetectAuth` chooses between API key, OAuth token, refresh token, on-disk credentials, and macOS Keychain. `BuildCleanEnv` filters which env vars get into the spawned process — a regression here can ship the *wrong* auth method or leak a token. `EnsureClaudeState` is what stops Claude Code from showing first-run interactive auth prompts. `Cleanup` is what `agent_config_test.go:TestValidateLocalPath` indirectly relies on but never asserts behavior of. The user-visible bug: a refactor changes `BuildCleanEnv` to forget to strip `ANTHROPIC_API_KEY` for OAuth runs, and ateam silently bills the wrong account; or `DetectAuth` returns `AuthNone` for a valid setup, causing a confusing "no auth" error. Both ship today without a single failing test.
- **Recommendation**: Add unit tests for each of these against a fake `HOME` + temp project dir (`t.Setenv` is enough; no real keychain needed). Concretely: `TestDetectAuth_PrefersEnvOverFile`, `TestBuildCleanEnv_OAuthStripsAPIKey`, `TestBuildCleanEnv_RegularStripsBoth`, `TestEnsureClaudeState_CreatesFile`, `TestEnsureClaudeState_PreservesExisting`, `TestResolveRefreshToken_EnvBeatsSecretBeatsFile`, `TestConflicts_AllTargets`, `TestCleanup_PreservesSettings`. Avoid testing `hasKeychainEntry` directly (it shells out to `security`); leave that for `make test-docker-live` if needed.

## Core prompt assemblers unprotected

- **Title**: Four `Assemble*Prompt` functions that produce LLM-bound prompts for the primary workflows have 0% coverage
- **Location**: `internal/prompts/prompts.go:131` (`AssembleRoleCodePrompt` — used by `cmd/prompt.go:109`), `internal/prompts/prompts.go:468` (`AssembleCodeManagementPrompt` — used by `cmd/code.go:183` and `cmd/prompt.go:163`), `internal/prompts/prompts.go:544` (`AssembleAutoSetupPrompt`), `internal/prompts/prompts.go:566` (`AssembleExecDebugPrompt`). `AssembleReviewPrompt` is only 14.6% covered (`internal/prompts/prompts.go:392`); only the empty-after-filter error path is tested.
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: These are the prompts that drive `ateam code` (the entire code-management supervisor loop), `ateam auto-setup`, and `ateam exec --auto-debug`. The sibling `AssembleRolePrompt` and `AssembleCodeVerifyPrompt` have dedicated tests (`TestAssembleRolePromptIncludesPreviousReport`, `TestAssembleCodeVerifyPrompt`) and use the exact same 3-level fallback pattern — the missing tests are a straightforward extension of the existing pattern. The user-visible bug: a section header changes, the `{{SOURCE_DIR}}` substitution regresses, or a supervisor extra file is silently dropped, and the LLM receives a malformed prompt — easy to miss because the agent will still produce *some* output.
- **Recommendation**: Add tests mirroring the existing `TestAssembleRolePromptIncludesPreviousReport` setup: minimal role on-disk, call the assembler, assert (a) project info appears once at the top, (b) the role/supervisor prompt body is present, (c) extra-prompt is appended at the bottom, (d) `{{SOURCE_DIR}}` is replaced. For `AssembleReviewPrompt`, add tests for the customPrompt branch and the default-supervisor branch with a mix of reports.

## CodexAgent.Run / run untested end-to-end

- **Title**: `CodexAgent.Run` and its `run` goroutine are at 0% coverage; only the JSONL parser is tested
- **Location**: `internal/agent/codex.go:66` (`Run`), `:72` (`run`), `:452` (`codexMessageText`). `ClaudeAgent.run` is at 72.6% via `internal/agent/claude_test.go`.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The codex agent is a first-class agent backend (mentioned in `Makefile`'s `test-docker-live`, in `runtime/config.go`, and in CLI surfaces). The parser (`ParseCodexLine`, `parseCodexResult`, etc.) is well covered, but the actual subprocess lifecycle — start, stdout pipe, stream-event emission, error handling, exit-code propagation — has no test. ClaudeAgent has equivalent coverage via a fake stdin/stdout pattern; the same pattern can be applied. User-visible bug: a regression in process env construction, stderr tee-ing, or context cancellation for codex runs ships unnoticed.
- **Recommendation**: Add `internal/agent/codex_test.go` tests modeled on `claude_test.go` that use `req.CmdFactory` to inject a fake subprocess (cat a fixture JSONL file, exit with a known code), and assert the events emitted on the channel match expectations. One happy path, one non-zero-exit path is enough.

## Critical runtime helpers at 0%

- **Title**: `Runner.writeSettings` and `runtime.ResolveDockerfile` are at 0% coverage despite governing sandbox/container setup
- **Location**: `internal/runner/runner.go:814` (`writeSettings` — writes the merged Claude Code settings.json that controls the sandbox), `internal/runtime/config.go:430` (`ResolveDockerfile` — picks which Dockerfile is used for container runs).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `writeSettings` is the function that produces the sandbox JSON the Claude agent enforces — the merged allow/deny lists. `mergeSandboxPaths` immediately below it has coverage, so the merging logic is exercised, but the actual file-write + parse path is not. `ResolveDockerfile` implements a 5-level fallback that decides which Dockerfile builds the agent image; a regression that swaps the order or skips the embedded fallback would only surface in production. Both are simple file-IO functions easy to test.
- **Recommendation**: Add a test for `writeSettings` that calls it with a temp dir and asserts the output is valid JSON with expected keys (sandbox.filesystem.allowWrite contains workDir, denyWrite contains the settings path itself). Add a test for `ResolveDockerfile` that exercises each of the 5 fallback levels using temp dirs.

## End-to-end CLI test inventory is one file

- **Title**: `test/cli/` contains only `test-auth-combos.sh` (7 cases); no end-to-end coverage of the primary workflows
- **Location**: `test/cli/test-auth-combos.sh`, referenced by `Makefile:66` as `test-cli`.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The Go `_test.go` files cover individual `runX` functions via dry-run, but there is no end-to-end CLI harness that runs `./ateam init`, `./ateam report --dry-run`, `./ateam review --dry-run`, `./ateam code --dry-run --no-verify`, `./ateam verify --dry-run`, `./ateam all --dry-run`, `./ateam serve` (smoke) against an isolated `HOME`. The existing auth-combos script is a fine template — it sets `HOME=$tmp`, makes a project dir, and invokes the binary. Without it, regressions in argv-to-runner wiring (e.g., a flag deserialization bug only visible at the OS process boundary) escape the suite.
- **Recommendation**: Add one or two `test/cli/test-*-dryrun.sh` scripts that exercise the report→review→code→verify chain end-to-end with `--dry-run`. The bar is "exits zero and produces the expected file structure" — no LLM calls. Wire them into the existing `test-cli` make target.

## Cluster: minor 0% helpers worth knowing about (don't write tests)

- **Title**: A cluster of trivial, generated, or only-init-reachable functions at 0% — explicitly noted so they are not retried as findings
- **Location**: `internal/agent/agent.go:267` (`Result` formatter), `internal/agent/claude.go:31-48` (`Name`, `SetModel`, `SetEffort`, `SetMaxBudgetUSD`, `AgentEnv`, `CloneWithResolvedTemplates` — trivial getters/setters/struct copies, mirror of well-tested codex equivalents), `internal/agent/codex.go:32-49` (same), `internal/agent/mock.go:36-91` (mock package vars only used by tests), `internal/agent/files.go:43` (`Close` wrapper), `internal/runner/parse_stream.go:85-93` (nine empty `displayEvent()` interface-seal methods), `internal/runner/runner.go:888` (`Truncate` trivial method), `internal/agent/claude_auth.go:403` (`itoa` — explicit "avoid strconv" helper, exercised indirectly when `DetectAuth` is tested), `internal/root/resolve.go:31-55` (one-line path joiners — `RoleDir`, `SupervisorDir`, etc., all exercised indirectly when callers are tested), `cmd/table.go:38-47` (`errNoReview.Error`), `cmd/secret.go:62-274` (CLI handlers for an interactive command — exercised by `test/cli/test-auth-combos.sh`).
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Tracking these so future test.gaps cycles don't re-flag them. They are pass-throughs, trivial wrappers, interface seals, or covered by integration tests outside `go test -coverprofile`.
- **Recommendation**: Skip in future cycles. If a real regression bug ever lands in one of them, raise as a specific finding then.

# Quick Wins

1. **Add `AssembleRoleCodePrompt` and `AssembleCodeManagementPrompt` tests** — the pattern is already in `internal/prompts/prompts_test.go`; copy `TestAssembleRolePromptIncludesPreviousReport` and adapt. ~15 minutes per function, protects the core `ateam code` workflow.
2. **Add httptest tests for `handleHome`, `handleSessions`, `handleCodeSessionDetail`, `handleCodeSessionFile`** — sibling handlers already use the pattern; one ReturnsOK + one NotFound each. Closes the largest visible coverage gap in `internal/web`.
3. **Add a smoke test for `runVerify` and `runEval`** — both are user-facing commands chained by `ateam code` / `ateam all` and have substantial flag surface but zero end-to-end coverage of their entry points.
4. **Add `BuildCleanEnv` and `DetectAuth` tests** — pure functions with no external dependency beyond env vars (use `t.Setenv`); protects against silent credential bugs.
5. **Add `writeSettings` and `ResolveDockerfile` tests** — file IO with clear inputs/outputs, ~20 minutes each, covers the sandbox/container setup boundary.

# Project Context

- **Language/toolchain**: Go (module `github.com/ateam`, see `go.mod`). Cobra-based CLI.
- **Test runner**: `make test` → `go test -race ./...` (single command, runs cleanly).
- **Coverage tooling**: `go test -coverprofile=$TMPDIR/cov.out ./... && go tool cover -func=$TMPDIR/cov.out`. No CI yet; coverage is on-demand.
- **Total coverage**: 55.7% statements; 268 functions at 0% (of which ~half are trivial/generated/CLI-interactive and listed in the LOW cluster).
- **Per-package coverage**: `cmd` 41.5%, `internal/agent` 48.0%, `internal/calldb` 82.8%, `internal/config` 78.8%, `internal/container` 49.9%, `internal/display` 94.1%, `internal/eval` 77.4%, `internal/fsclone` 31.2%, `internal/gitutil` 89.3%, `internal/prompts` 52.0%, `internal/root` 74.0%, `internal/runner` 81.3%, `internal/runtime` 77.5%, `internal/secret` 69.4%, `internal/streamutil` 64.1%, `internal/web` 48.8%.
- **CLI surface area**: Subcommands declared in `cmd/`: `all`, `auto-setup`, `cat`, `claude`, `code`, `container-cp`, `cost`, `env`, `eval`, `exec`, `export`, `init`, `inspect`, `install`, `parallel`, `project-rename`, `projects`, `prompt`, `report`, `resume`, `review`, `roles`, `runs`, `secret`, `serve`, `tail`, `update`, `verify`, `version`. Root in `cmd/root.go`.
- **Primary workflow chain**: `report` → `review` → `code` → `verify`; `all` runs them as a chain. `cmd/all_test.go` covers the chain; individual links have varying coverage of the entry point.
- **Test directories**: `cmd/*_test.go`, `internal/*/[*]_test.go`, `test/cli/test-auth-combos.sh` (the only shell-level CLI test). `test/Dockerfile.dind` powers `make test-docker` and `make test-docker-live`.
- **Critical files for `test.gaps`**:
  - `internal/agent/claude_auth.go` — credential pipeline, mostly 0%.
  - `internal/prompts/prompts.go` — prompt assembly, multiple 0% assemblers.
  - `internal/web/handlers.go` — nine routes at 0%.
  - `internal/runner/runner.go` — `writeSettings` (sandbox JSON) at 0%.
  - `internal/runtime/config.go` — `ResolveDockerfile` at 0%.
  - `cmd/{eval,verify,tail,prompt,serve,update,runs,projects,install,auto_setup,container_cp,project_rename,version}.go` — no entry-point tests.
- **Maturity**: Pre-production, schema in flux, no CI configured (the user already de-prioritizes CI per `defaults/role.test.gaps.md`). No `project.maintenance` mode signal. Aggressive recommendations are appropriate.
- **No prior `test.gaps` report exists** at `.ateam/roles/test.gaps/history/` — this is the first run.
