# Summary

Overall module coverage sits at 55.6% statements (essentially unchanged from the prior run). The safety-critical packages — `calldb` 82.8%, `gitutil` 89.3%, `display` 94.1%, `runner` 81.3%, `config` 78.8%, `runtime` 73.8%, `root` 74.0% — remain well-tested. The five concentrated gaps from the previous report are all still present and unaddressed: (1) most user-visible CLI commands have no test that exercises their `runX` entry point, so flag wiring can regress silently; (2) `internal/agent/claude_auth.go` — the credential pipeline — has 0% coverage on `DetectAuth`, `BuildCleanEnv`, `EnsureClaudeState`, `Cleanup`, `Conflicts`, `ResolveRefreshToken`; (3) the `prompts` package has 0% on `AssembleRoleCodePrompt`, `AssembleCodeManagementPrompt`, `AssembleAutoSetupPrompt`, `AssembleExecDebugPrompt`, and only 14.6% on `AssembleReviewPrompt`; (4) nine `internal/web` GET routes (`handleHome`, history, sessions, code-session group) are at 0%; (5) `Runner.writeSettings` and `runtime.ResolveDockerfile` (sandbox/container setup) are at 0%. Test tooling and runnability are fine — `make test` runs `go test -race ./...` cleanly with a single command, and coverage is one `go test -coverprofile` away. The work is to plug specific unprotected paths, not to build infrastructure.

# Findings

## CLI command entry points without smoke tests

- **Title**: User-facing CLI commands `eval`, `tail`, `serve`, `update`, `prompt`, `verify`, `install`, `auto-setup`, `runs`, `projects`, `project-rename`, `container-cp`, `version` have no test exercising their `runX` entry point
- **Location**: `cmd/eval.go:135` (`runEval` — 0%), `cmd/tail.go:48` (`runTail` — 0%), `cmd/serve.go:44` (`runServe` — 0%), `cmd/update.go:43` (`runUpdate` — 0%), `cmd/prompt.go:57` (`runPrompt` — 0%) and `:67/:119` (`runPromptRole`/`runPromptSupervisor` — both 0%), `cmd/install.go:24` (`runInstall` — 0%), `cmd/auto_setup.go:44` (`runAutoSetup` — 0%), `cmd/projects.go:24` (`runProjects` — 0%), `cmd/project_rename.go:43` (`runProjectRename` — 0%), `cmd/container_cp.go:35` (`runContainerCp` — 0%), `cmd/version.go:21` (`runVersion` — 0%), `cmd/runs.go` (no `runRuns` test). For `verify` the only existing test is `TestVerifyWarnsWhenCheaperAndModelBothSet` in `cmd/model_override_combined_test.go` — narrow flag warning, not an entry-point smoke test.
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: Each of these is a top-level cobra subcommand the user invokes. Confirmed by `cmd/*_test.go` grep — none of these `runX` functions appear in any `_test.go`. The user-visible bug that ships if these stay at 0%: a flag is renamed, a `MarkFlagsMutuallyExclusive` invariant changes, or a wiring tweak silently breaks one of these commands, and `go test ./...` still passes. `verify` is the most painful: it's chained automatically by `ateam code` and `ateam all`, so an undetected regression there breaks the whole code→verify workflow. `eval` has substantial branching (worktree mode, parallel mode, `--review` mode, multi-role) and only its parsers are exercised indirectly.
- **Recommendation**: For each command, add at least one smoke test in `cmd/` that calls the command with a controlled environment, following the pattern from `cmd/all_test.go` / `cmd/code_test.go` (dry-run with a mock agent, temp HOME). Verify (a) flag parsing succeeds and (b) the command reaches the expected first observable side-effect. Priority: `verify`, `eval`, `tail`, `prompt` (used inside the supervisor loop). One happy-path smoke test per command is enough; the bar is "catch wiring regressions", not deep coverage.

## Web HTTP routes with no handler test

- **Title**: Nine `internal/web` GET routes have 0% handler coverage despite being wired in the route table
- **Location**: `internal/web/handlers.go:32` (`handleHome`, route `GET /`), `:199` (`handleReports`, `GET /p/{project}/reports`), `:784` (`handleReportHistory`, `GET /p/{project}/reports/{role}/history/{file}`), `:807` (`handleSupervisorHistory`, used for both `review/history` and `verify/history`), `:992` (`handleSessions`, `GET /p/{project}/sessions`), `:1021` (`handleSessionDetail`), `:1155` (`handleCodeSessions`), `:1301` (`handleCodeSessionDetail`), `:1373` (`handleCodeSessionFile`). Also `:689` `resolveOutputFile` (0%) and `:817` `serveHistoryFile` (0%). Route registration: `internal/web/server.go:240-258`.
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: The web UI is one of the project's three user-visible surfaces. Sibling handlers (`handleOverview`, `handleReport`, `handleRun`, `handleRunFile`, `handleCost`, `handleReview`, `handleVerify`, `handlePrompts`) all sit at ≥90% via a consistent `httptest`-driven pattern in `internal/web/handlers_test.go`; these nine routes are conspicuous gaps in an otherwise well-tested file. A 5xx or wrong-content regression on `/`, `/p/{project}/sessions`, `/p/{project}/code/{session}`, or any history route ships without a failing test. `handleCodeSessionFile` (line 1373) takes a file-path parameter — coverage there guards against path-traversal regressions even though the underlying `isPathWithin` is 100% covered.
- **Recommendation**: Add `httptest` tests mirroring the existing pattern (e.g. `TestHandleOverviewReturnsOK`/`TestHandleOverviewNotFound`). One ReturnsOK + one NotFound per handler is enough; the test Server construction is already factored. Prioritize the history/sessions/code-session group (URL parameters = most regression-prone) and `handleHome` (only entry point for users who skip `--single-project`).

## Claude auth pipeline unprotected

- **Title**: Critical `internal/agent/claude_auth.go` decision functions are at 0% coverage
- **Location**: `internal/agent/claude_auth.go:80` (`DetectAuth`), `:184` (`Conflicts`), `:215` (`EnsureClaudeState`), `:246` (`Cleanup`), `:292` (`BuildCleanEnv`), `:324` (`HasRefreshToken`), `:348` (`ResolveRefreshToken`), `:67` (`ClaudeConfigDir`). Sibling helpers `ParseAuthMethod`, `ValidateTarget`, `ExtractRefreshToken`, `credFileHasTokens` are well covered by `claude_auth_test.go`.
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: These functions own credential selection for every Claude agent process. `DetectAuth` chooses between API key, OAuth token, refresh token, on-disk credentials, and macOS Keychain. `BuildCleanEnv` filters which env vars get into the spawned process — a regression here can ship the *wrong* auth method or leak a token. `EnsureClaudeState` is what stops Claude Code from showing first-run interactive auth prompts. The user-visible bug: a refactor changes `BuildCleanEnv` to forget to strip `ANTHROPIC_API_KEY` for OAuth runs, and ateam silently bills the wrong account; or `DetectAuth` returns `AuthNone` for a valid setup, causing a confusing "no auth" error. Both ship today without a single failing test.
- **Recommendation**: Add unit tests against a fake `HOME` + temp project dir (`t.Setenv` is enough; no real keychain needed). Concretely: `TestDetectAuth_PrefersEnvOverFile`, `TestBuildCleanEnv_OAuthStripsAPIKey`, `TestBuildCleanEnv_RegularStripsBoth`, `TestEnsureClaudeState_CreatesFile`, `TestEnsureClaudeState_PreservesExisting`, `TestResolveRefreshToken_EnvBeatsSecretBeatsFile`, `TestConflicts_AllTargets`, `TestCleanup_PreservesSettings`. Skip `hasKeychainEntry` (shells out to `security`); leave that for `make test-docker-live` if needed.

## Core prompt assemblers unprotected

- **Title**: Four `Assemble*Prompt` functions that produce LLM-bound prompts for primary workflows have 0% coverage; `AssembleReviewPrompt` is only 14.6%
- **Location**: `internal/prompts/prompts.go:131` (`AssembleRoleCodePrompt` — used by `cmd/prompt.go:67`), `:468` (`AssembleCodeManagementPrompt` — used by `cmd/code.go` and `cmd/prompt.go:119`), `:544` (`AssembleAutoSetupPrompt`), `:566` (`AssembleExecDebugPrompt`), `:392` (`AssembleReviewPrompt` — only the empty-after-filter error path is tested).
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: These are the prompts that drive `ateam code` (the entire code-management supervisor loop), `ateam auto-setup`, and `ateam exec --auto-debug`. The sibling `AssembleRolePrompt` and `AssembleCodeVerifyPrompt` both sit at 100% via tests like `TestAssembleRolePromptIncludesPreviousReport` — they use the exact same 3-level fallback pattern (`readFileOr3Level` is 100% covered), so the missing tests are a straightforward extension. The user-visible bug: a section header changes, the `{{SOURCE_DIR}}` substitution regresses, or a supervisor extra file is silently dropped, and the LLM receives a malformed prompt — easy to miss because the agent will still produce *some* output.
- **Recommendation**: Add tests mirroring `TestAssembleRolePromptIncludesPreviousReport`: minimal role on-disk, call the assembler, assert (a) project info appears once at the top, (b) the role/supervisor prompt body is present, (c) extra-prompt is appended at the bottom, (d) `{{SOURCE_DIR}}` is replaced. For `AssembleReviewPrompt`, add tests for the customPrompt branch and the default-supervisor branch with a mix of reports.

## CodexAgent.Run / run untested end-to-end

- **Title**: `CodexAgent.Run` and its `run` goroutine are at 0% coverage; only the JSONL parser is tested
- **Location**: `internal/agent/codex.go:70` (`Run`), `:76` (`run`), `:460` (`codexMessageText`). For reference `ClaudeAgent.run` is well covered via `internal/agent/claude_test.go`. Also several trivial codex accessors at 0% (`Name`, `SetModel`, `SetEffort`, `SetMaxBudgetUSD`, `AgentEnv`, `CloneWithResolvedTemplates`) — those are LOW.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The codex agent is a first-class agent backend (mentioned in `Makefile`'s `test-docker-live`, in `runtime/config.go`, and across the CLI). The parser (`ParseCodexLine`, `parseCodexResult`) is well covered, but the actual subprocess lifecycle — start, stdout pipe, stream-event emission, error handling, exit-code propagation — has no test. `ClaudeAgent` has equivalent coverage via a fake stdin/stdout pattern; the same pattern can be applied. User-visible bug: a regression in process env construction, stderr tee-ing, or context cancellation for codex runs ships unnoticed.
- **Recommendation**: Add `internal/agent/codex_test.go` modeled on `claude_test.go` that uses `req.CmdFactory` to inject a fake subprocess (cat a fixture JSONL file, exit with a known code) and asserts the events emitted on the channel. One happy path + one non-zero-exit path is enough.

## Critical runtime helpers at 0%

- **Title**: `Runner.writeSettings` and `runtime.ResolveDockerfile` are at 0% coverage despite governing sandbox/container setup
- **Location**: `internal/runner/runner.go:814` (`writeSettings` — writes the merged Claude Code settings.json that controls the sandbox; `RenderSettings` at `:808` is 100% covered, but the file-write path is not), `internal/runtime/config.go:430` (`ResolveDockerfile` — picks which Dockerfile builds the agent image, 5-level fallback).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `writeSettings` produces the sandbox JSON the Claude agent enforces — the merged allow/deny lists. `mergeSandboxPaths` immediately below has coverage, so the merging logic is exercised, but the actual file-write + parse path is not. `ResolveDockerfile` implements a 5-level fallback (project override → org defaults → bundled defaults → embedded → error); a regression that swaps the order or skips the embedded fallback would only surface in production. Both are simple file-IO functions easy to test against a temp dir.
- **Recommendation**: Add a test for `writeSettings` that calls it with a temp dir and asserts the output is valid JSON with expected keys (`sandbox.filesystem.allowWrite` contains workDir, `denyWrite` contains the settings path itself). Add a test for `ResolveDockerfile` exercising each of the 5 fallback levels using temp dirs.

## End-to-end CLI test inventory is one file

- **Title**: `test/cli/` contains only `test-auth-combos.sh` (7 cases); no end-to-end coverage of the primary workflows
- **Location**: `test/cli/test-auth-combos.sh`, referenced by `Makefile` as `test-cli`.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The Go `_test.go` files cover individual `runX` functions via dry-run, but there is no end-to-end CLI harness that runs `./ateam init`, `./ateam report --dry-run`, `./ateam review --dry-run`, `./ateam code --dry-run --no-verify`, `./ateam verify --dry-run`, `./ateam all --dry-run`, or `./ateam serve` (smoke) against an isolated `HOME`. The existing auth-combos script is a fine template — it sets `HOME=$tmp`, makes a project dir, and invokes the binary. Without an OS-process-level test, regressions in argv-to-runner wiring (e.g., a flag deserialization bug only visible at the OS boundary) escape the suite.
- **Recommendation**: Add one or two `test/cli/test-*-dryrun.sh` scripts exercising the report→review→code→verify chain end-to-end with `--dry-run`. The bar is "exits zero and produces the expected file structure" — no LLM calls. Wire them into the existing `test-cli` make target.

## Eval judge + review step at 0%

- **Title**: `eval.RunJudge`, `eval.buildJudgePrompt`, and `eval.runReviewStep` are at 0% in the otherwise well-tested `internal/eval` package (77.4% overall)
- **Location**: `internal/eval/judge.go:54` (`RunJudge` — 0%), `:77` (`buildJudgePrompt` — 0%), `internal/eval/run.go:226` (`runReviewStep` — 0%).
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `RunJudge` is the function that scores eval runs — the core "did the eval pass" decision. `runReviewStep` is the `--review` slice of `runEval`. The surrounding code (`compare.go`, `aggregateSummary`, `formatReport`) is 100% covered, so these stand out as untested decision functions. The user-visible bug: a judge prompt regression makes every eval pass/fail incorrectly, or the `--review` mode produces a wrong artifact path. Both ship silently because the surrounding test suite still passes.
- **Recommendation**: Add tests that drive `buildJudgePrompt` with fixture report content and assert the prompt structure; add a test that drives `RunJudge` against a mock agent (the `agent.Mock` package var pattern from `internal/agent/mock.go` should fit). Skip end-to-end agent invocation — assert prompt assembly + agent-result parsing.

## Cluster: minor 0% helpers worth knowing about (don't write tests)

- **Title**: A cluster of trivial, generated, or only-init-reachable functions at 0% — explicitly noted so they are not retried as findings
- **Location**: `internal/agent/agent.go` (`Result` formatter), `internal/agent/claude.go:31-48` (trivial getters/setters/struct copies, mirror of well-tested codex equivalents), `internal/agent/codex.go:32-49` (same set — `Name`, `SetModel`, `SetEffort`, etc.), `internal/agent/mock.go` (mock package vars only used by tests), `internal/agent/files.go:43` (`Close` wrapper), `internal/runner/parse_stream.go` (interface-seal `displayEvent()` methods), `internal/runner/runner.go:888` (`Truncate` trivial method), `internal/agent/claude_auth.go:403` (`itoa` — explicit "avoid strconv" helper, exercised indirectly when `DetectAuth` is tested), `internal/root/resolve.go:31-55` (one-line path joiners `RoleDir`, `SupervisorDir`, etc. — all exercised indirectly when their callers are tested), `cmd/table.go:38-47` (`errNoReview.Error`), `cmd/secret.go:62-274` (CLI handlers for an interactive command — exercised by `test/cli/test-auth-combos.sh`), `cmd/agent_config.go` (interactive setup helpers, also covered by `test-auth-combos.sh`).
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Tracking these so future `test.gaps` cycles don't re-flag them. They are pass-throughs, trivial wrappers, interface seals, or covered by integration tests outside `go test -coverprofile`. Together they account for a substantial fraction of the 550 reported 0% functions.
- **Recommendation**: Skip in future cycles. If a real regression bug ever lands in one of them, raise as a specific finding then.

# Quick Wins

1. **Add `AssembleRoleCodePrompt` and `AssembleCodeManagementPrompt` tests** — the pattern is already in `internal/prompts/prompts_test.go`; copy `TestAssembleRolePromptIncludesPreviousReport` and adapt. ~15 min per function, protects the core `ateam code` workflow.
2. **Add httptest tests for `handleHome`, `handleSessions`, `handleCodeSessionDetail`, `handleCodeSessionFile`** — sibling handlers already use the pattern; one ReturnsOK + one NotFound each. Closes the largest visible coverage gap in `internal/web`.
3. **Add a smoke test for `runVerify` and `runEval`** — both are user-facing commands chained by `ateam code` / `ateam all` and have substantial flag surface but zero end-to-end coverage of their entry points.
4. **Add `BuildCleanEnv` and `DetectAuth` tests** — pure functions with no external dependency beyond env vars (use `t.Setenv`); protects against silent credential bugs.
5. **Add `writeSettings` and `ResolveDockerfile` tests** — file IO with clear inputs/outputs, ~20 minutes each, covers the sandbox/container setup boundary.

# Project Context

- **Language/toolchain**: Go (module `github.com/ateam`, see `go.mod`). Cobra-based CLI.
- **Test runner**: `make test` → `go test -race ./...` (single command, runs cleanly).
- **Coverage tooling**: `go test -coverprofile=$TMPDIR/cov.out ./... && go tool cover -func=$TMPDIR/cov.out`. No CI yet; coverage is on-demand.
- **Total coverage**: 55.6% statements; 550 functions at 0% (of which a large fraction are trivial/generated/CLI-interactive — see the LOW cluster).
- **Per-package coverage**: `cmd` 41.5%, `internal/agent` 47.9%, `internal/calldb` 82.8%, `internal/config` 78.8%, `internal/container` 49.9%, `internal/display` 94.1%, `internal/eval` 77.4%, `internal/fsclone` 31.2%, `internal/gitutil` 89.3%, `internal/prompts` 52.0%, `internal/root` 74.0%, `internal/runner` 81.3%, `internal/runtime` 73.8%, `internal/secret` 69.4%, `internal/streamutil` 64.1%, `internal/web` 48.8%.
- **CLI surface area**: Subcommands declared in `cmd/`: `all`, `auto-setup`, `cat`, `claude`, `code`, `container-cp`, `cost`, `env`, `eval`, `exec`, `export`, `init`, `inspect`, `install`, `parallel`, `project-rename`, `projects`, `prompt`, `report`, `resume`, `review`, `roles`, `runs`, `secret`, `serve`, `tail`, `update`, `verify`, `version`. Root in `cmd/root.go`.
- **Primary workflow chain**: `report` → `review` → `code` → `verify`; `all` runs them as a chain. `cmd/all_test.go` covers the chain; individual links have varying coverage of the entry point.
- **Test directories**: `cmd/*_test.go`, `internal/*/[*]_test.go`, `test/cli/test-auth-combos.sh` (the only shell-level CLI test). `test/Dockerfile.dind` powers `make test-docker` and `make test-docker-live`.
- **Critical files for `test.gaps`**:
  - `internal/agent/claude_auth.go` — credential pipeline, 7 functions at 0%.
  - `internal/prompts/prompts.go` — prompt assembly, 4 assemblers at 0% + `AssembleReviewPrompt` at 14.6%.
  - `internal/web/handlers.go` — 9 routes at 0%.
  - `internal/runner/runner.go` — `writeSettings` (sandbox JSON) at 0%.
  - `internal/runtime/config.go` — `ResolveDockerfile` at 0%.
  - `internal/agent/codex.go` — `Run`/`run` at 0%.
  - `internal/eval/judge.go` + `run.go` — `RunJudge`/`buildJudgePrompt`/`runReviewStep` at 0%.
  - `cmd/{eval,verify,tail,prompt,serve,update,runs,projects,install,auto_setup,container_cp,project_rename,version}.go` — no entry-point tests.
- **Role**: `test.gaps`, model `claude-opus-4-7` (no extended thinking enabled).
- **Maturity**: Pre-production, schema in flux, no CI configured. Aggressive recommendations are appropriate; CI/CD is explicitly deprioritized.
- **Diff vs previous report (2026-05-14 06:23)**: total coverage essentially unchanged (55.7% → 55.6%); `internal/runtime` slightly down (77.5% → 73.8%); `internal/agent` essentially flat (48.0% → 47.9%). None of the prior findings have been resolved — all five HIGH/MEDIUM clusters from the previous report were re-verified against fresh coverage data and remain valid.
