# Summary

ateam is a 44.7k-LOC Go CLI + small embedded web server with a generally healthy package layout (`cmd/` thin shell, `internal/{agent,runner,calldb,prompts,runtime,root,web,…}` with clear roles). Three files have accreted into catch-alls that now dominate the structural debt: `cmd/table.go` (1390 LOC), `internal/web/handlers.go` (1433 LOC), and `internal/runner/runner.go` (1282 LOC, with a ~445-line `Run` method). The rest of the codebase is consistent and well-commented; remaining findings are localized duplication (pricing-table conversion, parallel command boilerplate), one dead helper, and a planned legacy-layout removal that is gated on a migration sentinel.

Role performing the audit: `code.structure` (merged refactor-small + architecture lens). Model: claude-opus-4-7. Thinking: standard reasoning, no extended thinking enabled. A prior report from 2026-05-13_20-34-29 exists at `.ateam/runtime/23/report.md`; all findings below were re-verified against current code and re-included in full.

# Findings

## 1. `cmd/table.go` is a catch-all utility module

- **Location**: `cmd/table.go` (1390 LOC, 57 functions)
- **Scope**: architecture
- **Severity**: HIGH
- **Effort**: LARGE
- **Description**: The file's name suggests it's about table rendering, but the actual contents are: ExitError type, table writer factory, DB open helpers (`openProjectDB`, `requireProjectDB`), runner construction (`newRunner`, `newRunnerFromAgent`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `resolveRunnerMinimal`, `resolveRunner`, `newRunnerDefault`), container construction (`buildContainer`, `deriveDockerImageName`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerExecOutput`, `dockerCp`, `resolveContainerName`), volume parsing (`resolveVolumePath`, `splitVolumeSpec`), Cobra flag adder helpers (`addProfileFlags`, `addContainerNameFlag`, `addBudgetFlags`, `addVerboseFlag`, `addCheaperModelFlag`, `addDockerAutoSetupFlag`, `addForceFlag`), runner mutators (`applyContainerName*`, `applyEffort`, `applyModel`, `applyModelOverrides`, `applyMaxBudgetUSD`, `setSourceWritable`), budget parsing/precheck (`parseBudgetUSD`, `batchBudgetPrecheck`), concurrency guard (`checkConcurrentRuns*`, `isProcessAlive`), ID parsing (`parseIDArgs`, `resolveExecIDs`, `lastRunID`), TTY/stdin helpers, secret resolver helper, the large `printDryRunInfo` + `printDryRunSecrets`, and the `runPool` orchestrator. `runPool` alone is ~165 LOC of pool/renderer plumbing. Readers looking for "where does table rendering live?" find none of it — `tabwriter` is used by a single 4-line factory. A new contributor reading `cmd/` cannot guess what's in `table.go`; many of the helpers are imported by every command, so the file becomes friction on every change.
- **Recommendation**: Split mechanically without changing call sites. Concretely:
  - `cmd/runner_build.go`: `newRunner`, `newRunnerFromAgent`, `newRunnerDefault`, `resolveRunner`, `resolveRunnerMinimal`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `buildAgent`, `buildPricingFromConfig`, `mergedPricingFromConfig`.
  - `cmd/container_build.go`: `buildContainer`, `deriveDockerImageName`, `resolveVolumePath`, `splitVolumeSpec`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerExecOutput`, `dockerCp`, `resolveContainerName`, `applyContainerName*`.
  - `cmd/flags.go`: every `add*Flag` helper plus `cheaperModelName` constant.
  - `cmd/budget.go`: `parseBudgetUSD`, `batchBudgetPrecheck`, `applyMaxBudgetUSD`.
  - `cmd/db_helpers.go`: `openProjectDB`, `requireProjectDB`, `parseIDArgs`, `resolveExecIDs`, `lastRunID`, `checkConcurrentRuns*`, `isProcessAlive`, `secretResolver`.
  - `cmd/dryrun.go`: `dryRunOpts`, `printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`.
  - `cmd/pool_run.go`: `poolDisplayOpts`, `runPool`.
  - Keep `cmd/table.go` only for `ExitError`, `cmdContext`, `errNoReview`, `newTable`, `relPath`, `printDone`, `isTerminal`, `stdinIsPiped`, `applyEffort`, `applyModel`, `applyModelOverrides`, `setSourceWritable` — or rename it `cmd/common.go`.

## 2. `internal/web/handlers.go` is one file for the whole HTTP layer

- **Location**: `internal/web/handlers.go` (1433 LOC, 42 functions)
- **Scope**: architecture
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: Every page handler lives in this file alongside legacy-layout file resolvers, run-file rendering, history dispatch, sessions/code-sessions logic, and a duplicated pricing-table builder (lines 580–591). The package already has dedicated files (`history.go`, `export.go`, `markdown.go`, `server.go`), but new handlers have continued to land in `handlers.go`. The file is the single biggest source file in the project and slows every web change — readers must skim past unrelated handlers to find one. The page-data structs (`overviewData`, `reportData`, `supervisorOutputData`, `runDetailData`, `runFileData`, `historyDetailData`, `costPageData`, `sessionsPageData`, …) and their handlers are interleaved rather than colocated with their templates.
- **Recommendation**: Split by feature. Suggested target files:
  - `internal/web/overview.go`: `handleHome`, `handleOverview`, `overviewRun`, `overviewData`, `enrichRuns`, `resolveRunFiles`, `runFiles`.
  - `internal/web/reports.go`: `handleReports`, `handleReport`, `handleReportHistory`, `reportData`.
  - `internal/web/supervisor.go`: `handleSupervisorOutput`, `handleReview`, `handleVerify`, `handleSupervisorHistory`, `serveHistoryFile`, `historyAction`, `navKind`, `supervisorLabel`, `lookupExecForHistory`, `supervisorPageConfig`, `supervisorOutputData`, `historyDetailData`.
  - `internal/web/runs.go`: `handleRuns`, `handleRun`, `handleRunFile`, `runsPageData`, `runDetailData`, `runFileData`.
  - `internal/web/cost.go`: `handleCost`, `costPageData`.
  - `internal/web/sessions.go`: `buildSessions`, `handleSessions`, `handleSessionDetail`, `parseBatchTimestamp`, `sessionsPageData`.
  - `internal/web/code_sessions.go`: `handleCodeSessions`, `handleCodeSessionDetail`, `handleCodeSessionFile`, `scanCodeSessions`, `buildCodeSessionEntry`, `codeSessionTimestamp`, `latestCodeSession`, `codeSessionDirs`, `readDirOrNil`, `codeSessionEntry`.
  - `internal/web/legacy_layout.go`: `promptDir`, `resolvePromptFile`, `resolveOutputFile`, `resolveHistoryFile`, plus the legacy branch of `resolveRunFiles` and the legacy-suffix `strings.TrimSuffix(..., "_stream.jsonl")` arms. Centralizing this is also a precondition for finding 4 below.
  - Leave `handlers.go` for shared utilities (`requireProject`, `readFileWithModTime`, `isPathWithin`, `capitalizeASCII`) or rename it `helpers.go`.

## 3. `runner.Run` is a ~445-line function

- **Location**: `internal/runner/runner.go:195`–`runner.go:638`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `Run` interleaves at least seven distinct phases: pre-insert validation, DB insert and path setup, fail closures, extra-args/sandbox/template resolution, container setup, the stream event loop (~100 lines), summary assembly, and finalize/promotion. The function is the most-edited code path (cost classification, budget caps, stall watchdog, cancelation handling all landed here), and every new feature has to be read against the entire flow. Local extraction into named helpers — `prepareRun`, `runEventLoop`, `assembleSummary` — would shrink the top-level body to a readable scaffold without changing semantics. The concurrency-contract comment at the top of the file already names the phases informally.
- **Recommendation**: Without changing public API, split into private methods on `*Runner`:
  - `prepareRun(ctx, prompt, opts) (*runState, error)` — does the DB insert, path setup, args build, settings write, container clone, template resolution, request build, cmd.md write.
  - `runEventLoop(ctx, events, state, progress) eventLoopResult` — owns processEvent, the stall timer, lastEventAt.
  - `assembleSummary(state, loopResult, opts) RunSummary` — folds the post-loop result/fallback logic and the streamed-text fallback.
  - `Run` becomes a ~40-line glue function calling the three, plus `finalizeCall`. The phases are already in the comments; this just makes them executable.

## 4. Legacy log-layout code path is still wired into web + several cmd files

- **Location**: `internal/web/handlers.go:148–183, 510–558, 704–723`; `cmd/inspect.go:215–217`; `cmd/pool_status.go:72–73`; `cmd/resume.go:254–255`; plus the `root.IsLegacyStreamFile` predicate at `internal/root/resolve.go:402` and `FindHistoryFileWithSkew` at `internal/root/resolve.go:419`.
- **Scope**: architecture
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: There are two file-layout branches everywhere: the new `logs/<exec_id>/{stream.jsonl,prompt.md,cmd.md,stderr.out}` and the legacy `<TS>_<ACTION>_stream.jsonl` prefix layout. `internal/root/migrate_logs.go` has a sentinel-guarded migration (`logs/.layout-v2`) that runs on every DB open via `openProjectDB`/`requireProjectDB`, so any project that has been opened with the current binary is already migrated. The dual code path adds reading cost (every web handler with file resolution branches twice) and a class of "what does this row look like" questions. `DEV.md:215` already documents the cleanup precondition ("When no `agent_execs` row in any deployed project still has a `_stream.jsonl` stream_file").
- **Recommendation**: Verify on the codebase's user population that no legacy data remains (or accept that any unmigrated row will return 404 from the web UI, which is already the worst case if the legacy files were deleted). Then:
  1. Remove `root.IsLegacyStreamFile` and `FindHistoryFileWithSkew` if they have no remaining callers post-cleanup.
  2. Remove `promptDir`, `resolvePromptFile`, `resolveOutputFile`, `resolveHistoryFile` from `internal/web/handlers.go` and the legacy branches of `resolveRunFiles`/`handleRunFile`.
  3. Drop the `strings.TrimSuffix(absStream, "_stream.jsonl")` arms in `cmd/inspect.go`, `cmd/pool_status.go`, `cmd/resume.go`.
  4. Keep `internal/root/migrate_logs.go` and its tests (they document the historical shape; removal would lose a piece of history that is useful for newcomers reading the DB schema).

  Do NOT do this until you've confirmed no production project still carries pre-`.layout-v2` rows. If unsure, leave a single guarded fast-path that returns NotFound for legacy rows and removes the resolution code.

## 5. Pricing-table conversion is duplicated three ways

- **Location**: `cmd/table.go:378–392` (`buildPricingFromConfig`), `cmd/table.go:357–375` (`mergedPricingFromConfig`), `cmd/cat.go:126`, `internal/web/handlers.go:579–591` (inline conversion inside `handleRunFile`).
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The conversion from `runtime.AgentPricing` (`InputPerMTok` / `CachedInputPerMTok` / `OutputPerMTok`) to `agent.PricingTable` (per-token rates) appears twice as identical logic. The inline copy in `handlers.go` (the loop populating `agent.ModelPrice` from `mp.InputPerMTok / 1e6`, etc.) bit-for-bit duplicates the body of `buildPricingFromConfig`. Any change to the pricing schema must be made in both places; the inline copy is easy to miss because it lives inside a 90-line handler.
- **Recommendation**: Move `buildPricingFromConfig` and `mergedPricingFromConfig` from `cmd/table.go` into `internal/runtime` (e.g. `runtime.AgentPricing.ToTable()` returning `(agent.PricingTable, string)`), and update `cmd/cat.go`, `cmd/code.go:298`, `cmd/tail.go:67`, and `internal/web/handlers.go` to call it. Belongs in `runtime` rather than `agent` because `runtime` already imports `agent` and is the natural source side of the conversion.

## 6. Cobra command files have a repeated 50–100 LOC setup ritual

- **Location**: `cmd/report.go`, `cmd/review.go`, `cmd/code.go`, `cmd/exec.go`, `cmd/parallel.go`, `cmd/verify.go`, `cmd/all.go`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Each top-level command repeats the same sequence: declare ~10 package-level flag vars, define an Options struct that mirrors them, register flags in `init()` using the shared `add*Flag` helpers, define a Cobra `RunE` that constructs the Options struct, and a `run<X>` body that does `root.Resolve` → `prompts.ResolveOptional` → `resolveRunner` → `applyRunnerOverrides` → `openProjectDB` → `checkConcurrentRunsEnv` → build `RunOpts` → call `runPool`/`r.Run`/print summary. The introduction of `RunnerOverrides` (`cmd/runner_overrides.go`) and the `add*Flag` helpers already proved this is mechanizable. There is real per-command variation (which flags exist, which prompt assembler is called, whether `runPool` or `r.Run` is used), but the boilerplate ratio is high — `report.go` is 415 LOC and `code.go` is 425 LOC with maybe 100 of unique behavior each. This is the canonical N+ site duplication bundle the role description calls out: ~6 commands × ~50 lines = ~300 LOC of mechanical duplication, fixed once with one helper.
- **Recommendation**: Don't try to unify the commands into one generic dispatcher — that would obscure the differences. But:
  - Promote the Options structs to a small generic "common command flags" struct (Verbose, Force, Profile, Agent, DockerAutoSetup, ContainerName, ExtraPrompt, Timeout, plus the existing `RunnerOverrides`). Each command embeds it and adds its own specific flags.
  - Provide one helper `cmd.setupRun(env, opts, action, roleID) (*runner.Runner, *calldb.CallDB, error)` that does Resolve → resolveRunner → applyRunnerOverrides → openProjectDB → checkConcurrentRunsEnv in the order every command already does. Single-exec commands that need `applyContainerName` already get it via `applyRunnerOverrides`.
  - Leave the prompt-assembly + RunOpts-building + run-loop unique to each command.

  This collapses the ritual into ~3 lines per command without erasing variation.

## 7. `cmd/agent_config.go` mixes four subcommands in one file

- **Location**: `cmd/agent_config.go` (805 LOC, 20 functions)
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The file has four distinct subcommands wedged into one Cobra command (`--audit`, `--copy-out`, `--copy-in`, `--setup-interactive`), each with its own helpers (`runAgentConfigAudit`, `runCopyOut`, `runCopyIn`, `runSetupInteractive`, `runRemoteAudit`), plus the shared Docker plumbing (`detectContainer`, `containerPathExists`, `containerDirEmpty`, `copyAteamBinary`, `resolveLocalPath`, `validateLocalPath`, `printLocalDirStatus`, `printSharedConfigStatus`, `printAuthSources`, `execClaude`). Reading the file means scanning 800 lines to find one path.
- **Recommendation**: Split into `cmd/agent_config.go` (command registration + dispatch), `cmd/agent_config_audit.go`, `cmd/agent_config_copy.go` (copy-in + copy-out share the container detection), `cmd/agent_config_setup.go`. Keep the Cobra command in one place. Cost is low (no API changes), and the four flows being explicitly named on disk helps a new contributor pick the right entry point.

## 8. `ClaudeAgent` and `CodexAgent` boilerplate is parallel

- **Location**: `internal/agent/claude.go:1–62`, `internal/agent/codex.go:1–65`
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The two agent types declare an identical set of small methods (`Name`, `ModelName`, `SetModel`, `SetEffort`, `SetMaxBudgetUSD`, `AgentEnv`, `CloneWithResolvedTemplates`) with slight differences in CLI arg construction (`claudeArgs` vs `codexFlagArgs` already factor the divergence). The struct fields are also near-identical. The duplication is small in absolute terms (~50 LOC each) and the agents are unlikely to grow to a dozen — keeping them as siblings is fine — but if a third agent is added (a frequent topic in `plans/`), it will pay to share base behavior.
- **Recommendation**: Defer. If a third agent lands, extract a `baseAgent` embeddable struct with the common fields and the trivial setters; until then the cost of the indirection is higher than the benefit.

## 9. `StreamFormatter` and `HTMLStreamFormatter` have parallel dispatch

- **Location**: `internal/runner/format_stream.go` (420 LOC), `internal/runner/format_stream_html.go` (265 LOC)
- **Scope**: module
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: Both formatters consume the same `DisplayEvent` types and dispatch via the same switch (`SystemLine`, `UserLine`, `ToolCallLine`, …). One renders ANSI, the other renders HTML. The duplication is real but each formatter has format-specific quirks (HTML escaping, color codes, no equivalent of `dim` in HTML). Generalizing via a visitor would obscure those quirks for limited gain.
- **Recommendation**: Defer. Worth flagging only because it's the second-largest cluster of duplication after pricing. The cost of splitting the format families later is the same as the cost today.

## 10. `cmd.applyModel` is dead in production code

- **Location**: `cmd/table.go:770–774`; sole callers are `cmd/table_test.go:253,271,280`.
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `applyModel(r, value)` sets the runner's model unconditionally. Since `applyRunnerOverrides` always routes through `applyModelOverrides(r, cheaper, model)`, no production caller uses `applyModel` directly — a grep confirms the only call sites are three lines in `cmd/table_test.go`. Either delete the function and the three tests, or keep it and mark with a comment explaining why it remains.
- **Recommendation**: Delete `applyModel` and its three test cases; if the underlying behavior is still worth a unit test, fold it into a single `TestApplyModelOverrides` case that runs the public override path with `cheaperModel=""`. Recommend a final `staticcheck ./...` pass to surface any other unused helpers in the same idiom.

## 11. Two TODO-acknowledged parallel base prompts

- **Location**: `internal/prompts/prompts.go:48` (`NewReportBasePromptFile` constant TODO), `internal/prompts/prompts.go:108–119` (`resolveBaseFileForRole` TODO).
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL (after the A/B comparison finishes)
- **Description**: `NewReportBasePromptFile` and `resolveBaseFileForRole` exist to keep legacy roles on `report_base_prompt.md` and dotted-prefix roles on `new_report_base_prompt.md` during an A/B comparison. Both TODOs ("fix this before v1 — merge with ReportBasePromptFile once the new role set is validated") name the cleanup explicitly. This is intentional, time-bound debt; the only structural risk is that the cleanup never happens.
- **Recommendation**: Once the new role set is validated, merge `new_report_base_prompt.md` into `report_base_prompt.md`, delete `NewReportBasePromptFile` and `resolveBaseFileForRole`, and inline the constant in both `assembleRoleAction` and the matching `traceRoleAction` (which the TODO calls out). No work to do now; track via the existing TODO.

# Quick Wins

1. **Finding 5** — Extract pricing conversion into `runtime.AgentPricing.ToTable()` and update three call sites (`cmd/cat.go`, `internal/web/handlers.go:579–591`, plus the `mergedPricingFromConfig` body). SMALL effort, MEDIUM severity, removes a class of "remember to update the other place" bugs.
2. **Finding 10** — Delete `cmd.applyModel` and its three tests; covered already by `applyRunnerOverrides`/`applyModelOverrides`. SMALL effort, LOW severity but mechanical.
3. **Finding 7** — Split `cmd/agent_config.go` into four subcommand files. SMALL effort (no API change), LOW severity, big readability win for a file that's tedious to navigate.
4. **Finding 1 (subset)** — Just lift the runner builders (`newRunner`, `newRunnerFromAgent`, `resolveRunner`, `resolveRunnerMinimal`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `buildAgent`, `buildContainer`) out of `cmd/table.go` into `cmd/runner_build.go` and `cmd/container_build.go`. SMALL effort if attempted as a single mechanical move, HIGH-severity foundation that the rest of the table.go split builds on.

# Project Context

- **Language / build**: Go 1.26.3 module `github.com/ateam`. Build: `make build`. Tests: `make test`, `make test-docker`. Lint config in `.golangci.yml`.
- **Top-level shape**: `cmd/` is a 61-file Cobra CLI surface (one file per command + shared `cmd/table.go` + `cmd/runner_overrides.go` + `cmd/std_redirect.go` + a few render/status helpers). `internal/` holds 16 packages: `agent` (claude/codex/mock + auth + pricing), `runner` (orchestration, pool, stream parsing, formatting), `calldb` (sqlite tracking via modernc.org/sqlite), `runtime` (HCL profile/agent/container config), `root` (env resolution, log layout migration), `prompts` (template assembly + 4-level fallback), `web` (Cobra-served HTTP UI), `container` (docker / docker-exec implementations), `config` (toml), plus `display`, `eval`, `fsclone`, `gitutil`, `secret`, `streamutil`.
- **Biggest non-test files (structural debt hotspots)**: `internal/web/handlers.go` (1433), `cmd/table.go` (1390), `internal/runner/runner.go` (1282), `cmd/agent_config.go` (805), `internal/prompts/prompts.go` (739), `internal/agent/codex.go` (593), `internal/runtime/config.go` (592), `internal/root/resolve.go` (457), `internal/calldb/queries.go` (455), `cmd/eval.go` (451), `internal/calldb/calldb.go` (429), `cmd/code.go` (425), `internal/runner/format_stream.go` (420), `cmd/report.go` (415).
- **Concurrency contract**: documented at the top of `internal/runner/runner.go` and in `CONCURRENCY.md`. Runner fields are read-only after `RunPool` dispatch; agents/containers are cloned per-exec via `CloneWithResolvedTemplates` / `Clone`. `calldb` serializes writes via `SetMaxOpenConns(1)`.
- **Log layout**: New canonical layout is `.ateam/logs/<exec_id>/{stream.jsonl,prompt.md,cmd.md,stderr.out,settings.json}` + `.ateam/runtime/<exec_id>/`. Legacy `<TS>_<ACTION>_*` layout is migrated on first DB open via `root.MigrateLogsLayout` (sentinel `.ateam/logs/.layout-v2`). Cleanup precondition for finding 4 is documented at `DEV.md:215`.
- **Entry points**: `main.go` → `cmd.Execute` → Cobra dispatch in `cmd/root.go`.
- **Helper that already exists for boilerplate**: `cmd/runner_overrides.go::applyRunnerOverrides` is the model to follow for finding 6.
- **Known intentional duplication**: `internal/prompts/prompts.go::NewReportBasePromptFile` + `resolveBaseFileForRole` are intentional A/B branching with two `TODO: fix this before v1` markers.
- **Recent commits (informational)**: last commit `6dcf9a0 prompts: fix three internal contradictions surfaced in review` (2026-05-14). No commits since the prior `code.structure` report (2026-05-13_20-34-29) materially restructure any of the hotspots; all 11 findings remain in current code.
- **Recommended mechanical tools to run alongside structural work**: `staticcheck ./...` (catches the kind of dead code in finding 10), `dupl -threshold 50 ./...` (would surface findings 5, 8, 9 mechanically on each commit), `gocyclo -over 30 ./...` (would flag `runner.Run`, `handleRunFile`, `runAgentConfig`).
