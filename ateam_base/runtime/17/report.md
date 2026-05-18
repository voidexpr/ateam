# Summary

The ateam codebase is well-organized for a single-module Go CLI of this size — clear `cmd → internal/{root,runtime,runner,agent,container,calldb,prompts,web}` layering, cobra subcommands, race-clean tests, documented concurrency contract on `Runner`. Structural debt is concentrated in three places: (1) three oversized files — `internal/runner/runner.go` (1282 LOC), `internal/web/handlers.go` (1433 LOC), `cmd/table.go` (1390 LOC) — each acting as a catch-all for its layer; (2) the `Agent`/`Runner` construction protocol relies on post-construction mutation (`SetModel`, `SetEffort`, `applyContainerNameOverride`) with a documented "convention only" contract; (3) the same per-command opening sequence (~30 lines: resolve → prompts → resolveRunner → applyOverrides → openDB+defer → cmdContext+defer → concurrency gate) is hand-copied in 5+ `cmd/*.go` files and has already begun drifting. Beyond those, a handful of cheap local cleanups (duplicate Claude/Codex accessors, repeated DB-error guard, repeated warning-log call, near-identical ANSI helpers) are mechanical wins. No findings here are bugs — bug-shaped concerns belong to `code.bugs`.

Role: `code.structure` (merge of legacy `refactor_small` + `refactor_architecture`). Model: Claude Opus 4.7 (`claude-opus-4-7`), default thinking, read-only analysis. Project maturity: a developer-machine CLI with active development; pre-1.0 (VERSION still uses dev tags) and a small user base — restructuring is justified when it removes drift or unblocks change, not when it would only beautify stable code. Since the prior report the only code changes are a small `codex` arg-ordering fix, a `runtime/config.go` addition, and docs/prompt edits — none of the structural findings below were resolved.

# Findings

## 1. `Runner` is a god struct with a god `Run` method
- **Location**: `internal/runner/runner.go:85-110` (struct, ~22 fields), `internal/runner/runner.go:195-650` (`Run`, ~450 LOC)
- **Scope**: architecture
- **Severity**: HIGH
- **Effort**: LARGE
- **Description**: `Runner` carries agent, container, project paths, sandbox, args (inside/outside container), CallDB, profile metadata, container name + source, project ID, stall settings — three or four orthogonal concerns in one record. `Run` linearly performs: DB insert, build extra-args, compute per-exec dirs, write settings, container clone, template-resolve agent, build the request, write prompt+cmd files, spawn the agent, consume events with a stall watchdog, classify failures, scan the stream for fallback usage data, write fallback output, finalize (promote files, rerender cmd.md, DB update). The function cannot be tested in pieces (everything funnels through one call) and the concurrency contract documented at `runner.go:71-84` ("WRITTEN only during construction; READ-ONLY afterwards") is enforced by convention only — multiple callers still mutate fields post-construction (`cr.CallDB = db` in `cmd/report.go:292`, `applyContainerNameOverride` at `cmd/table.go:180`, `setSourceWritable` at `cmd/table.go:873`).
- **Recommendation**: Split `Runner` into (a) `RunnerConfig` (immutable, built once: agent + container + sandbox + paths + DB + profile metadata) and (b) an `Execution` value created per `Run` holding live state (callID, dirs, files, cumulative counters, stall timer). Decompose `Run` into named phases: `prepare()` (insert DB row, mkdirs, settings, container setup), `execute()` (stream loop + stall watchdog), `finalize()` (promote, rerender, DB update). The exposed `Run` signature stays the same. Move the "stream tail for usage" recovery into a helper. Mechanizable check after split: `gocyclo`/`gocognit` thresholds enforced via `.golangci.yml`.

## 2. `Agent` interface requires mutation with ordering rules
- **Location**: `internal/agent/agent.go:31-58`; callsites `cmd/runner_overrides.go`, `cmd/report.go:235-252`, `cmd/table.go:760-861`
- **Scope**: architecture
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: `Agent.SetModel`, `SetEffort`, `SetMaxBudgetUSD` are each documented as "MUTATES — call before the Agent is shared with a pool." `CloneWithResolvedTemplates` partly mitigates this by handing each pool worker its own clone, but the contract is fragile: any future caller that forgets the ordering creates a data race that won't show up under `go test -race` unless the exact path is exercised. The pattern bleeds into `applyRunnerOverrides` (`cmd/runner_overrides.go`) and the per-role override flow in `cmd/report.go:235-252` where a *second* runner is built and overrides are reapplied. Same shape as Finding 1: written-then-frozen-by-convention.
- **Recommendation**: Move `Model`/`Effort`/`MaxBudgetUSD` into the `Request` struct (already passed by value through `runner.Runner.Run`). Each agent reads them off `Request`; the three setters and the "before sharing" contract disappear. Where a field can't move to `Request` (e.g. `Env`), wrap the agent in a small immutable `AgentSpec` and have `CloneWithResolvedTemplates` return a fresh agent built from spec+overrides.

## 3. `cmd/table.go` is a kitchen-sink helper file (~50 functions, 1390 LOC)
- **Location**: `cmd/table.go`
- **Scope**: architecture
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: One file holds: project DB open helpers (`openProjectDB`, `requireProjectDB`), runner construction (`newRunner`, `newRunnerFromAgent`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `newRunnerDefault`, `resolveRunner`, `resolveRunnerMinimal`), container construction (`buildContainer`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerCp`, `dockerExecOutput`, `resolveContainerName`), agent construction (`buildAgent`, `buildPricingFromConfig`, `mergedPricingFromConfig`), sandbox/git helpers (`gitWriteDirs`, `resolveVolumePath`, `splitVolumeSpec`), flag-registration (`addProfileFlags`, `addContainerNameFlag`, `addBudgetFlags`, `addCheaperModelFlag`, `addVerboseFlag`, `addForceFlag`, `addDockerAutoSetupFlag`), override appliers (`applyContainerName*`, `applyEffort`, `applyModel`, `applyModelOverrides`, `applyMaxBudgetUSD`, `setSourceWritable`), the pool driver (`runPool` + `poolDisplayOpts`), dry-run printers (`printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`), batch-budget precheck, exec-ID resolution, terminal helpers. The name "table" no longer describes anything — the file is whatever didn't fit elsewhere.
- **Recommendation**: Pure file-level split into 4–5 files in `cmd/`:
  - `cmd/runner_build.go` — `newRunner*`, `resolveRunner*`, `runnerFromAgentConfig`, `applyContainerName*`.
  - `cmd/container_build.go` — `buildContainer`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerCp`, `resolveVolumePath`, `gitWriteDirs`.
  - `cmd/flags.go` — `add*Flag` helpers and `parseBudgetUSD`.
  - `cmd/dry_run.go` — `printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`.
  - Keep `cmd/table.go` for the tabwriter/`printDone`/`ExitError`/`relPath` code that actually justifies the name (~80 LOC), or merge those into a new `cmd/output.go`. No behavior change.

## 4. `internal/web/handlers.go` mixes feature areas (1433 LOC, 35+ handlers)
- **Location**: `internal/web/handlers.go`
- **Scope**: architecture
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Handlers for overview, reports, runs, run-file viewing (markdown/stream HTML), supervisor outputs (review/verify), history (legacy + DB), cost, prompts, sessions, code-sessions all live in one file along with shared helpers (`resolveRunFiles`, `enrichRuns`, `resolveHistoryFile`, `parseExecHistoryFilename`, `buildSessions`, `codeSessionTimestamp`). Adding a new view means scrolling 1.4k LOC to find adjacent handlers; the file is also the natural touchpoint for path-traversal review (`isPathWithin`, `serveHistoryFile`) but the security helpers are buried mid-file at L651-707.
- **Recommendation**: Split by feature within the same package: `handlers_overview.go`, `handlers_runs.go` (run + run_file + enrich helpers), `handlers_history.go`, `handlers_supervisor.go` (review/verify shared `handleSupervisorOutput`), `handlers_sessions.go`, `handlers_codesessions.go`, `handlers_cost.go`, plus `handlers_safety.go` for `isPathWithin` + `resolveHistoryFile` + `serveHistoryFile`. Pure file split, no behavior change.

## 5. Per-command boilerplate is duplicated across 5+ files (N+ site bundle)
- **Location**: `cmd/exec.go`, `cmd/report.go:132-205`, `cmd/review.go:152-230`, `cmd/verify.go:100-160`, `cmd/code.go:150-280`, `cmd/parallel.go:184+`, `cmd/eval.go:230+`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Every primary action does the same opening sequence: `root.Resolve(orgFlag, projectFlag)` → `prompts.ResolveOptional(extraPrompt)` → `resolveRunner(...)` → `applyRunnerOverrides(...)` → `openProjectDB(env)` + `defer db.Close()` → `cr.CallDB = db` → `checkConcurrentRunsEnv(...)` gated by `--force` → `cmdContext()` + `defer stop()`. That's ~25-30 lines repeated five or six times (confirmed: `root.Resolve` appears in 18 cmd files; `openProjectDB`/`requireProjectDB` 6 places; `checkConcurrentRunsEnv` 5 places; `cmdContext()` 4 places — all in the same order). Drift is already visible: `verify.go` adds `setSourceWritable(cr)` between steps, and `report.go:235-252` builds a *second* per-role runner with its own override pass that is *almost* the same code. This is exactly the N+ site bundle pattern the role calls out: the per-site update is small, but the bundle (one extraction + 5 call-site updates in one commit) is the real finding.
- **Recommendation**: Introduce `cmd.prepareRun(action string, opts CommonRunOpts) (*runContext, error)` returning `{env, runner, db, ctx, cancel}` with `runContext.Close()` handling `db.Close()` + `cancel()`. Each command's setup drops to ~3 lines. The pool variant (`report`/`parallel`/`code`/`eval`) extends with `prepareRunBatch` returning the same plus a batch token + pre-dispatch budget closure. Captures the per-role second-runner pattern of `cmd/report.go:235-252` in one place too.

## 6. Duplicate setter/accessor methods + struct fields on `ClaudeAgent` vs `CodexAgent`
- **Location**: `internal/agent/claude.go:31-54` and `internal/agent/codex.go:36-59`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Aside from `Name()`, the methods `ModelName()`, `SetModel()`, `SetEffort()`, `SetMaxBudgetUSD()`, `AgentEnv()`, and the body of `CloneWithResolvedTemplates()` are byte-for-byte identical between the two agent types. Both also share the same struct fields (`Command`, `Args`, `Model`, `Effort`, `MaxBudgetUSD`, `DefaultModel`, `Pricing`, `Env`). Drift between the two would silently produce inconsistent agent behavior. Note this finding is partially absorbed by Finding 2 (moving `Model`/`Effort`/`MaxBudgetUSD` to `Request`); the remaining shared fields and `CloneWithResolvedTemplates` body still want a shared base.
- **Recommendation**: Introduce an unexported `baseAgent` struct holding the shared fields and methods, embedded in both `ClaudeAgent` and `CodexAgent`. `CloneWithResolvedTemplates` shares its body via a helper on the base.

## 7. Duplicate process-startup block in agent `run()` methods
- **Location**: `internal/agent/claude.go:88-127` and `internal/agent/codex.go:78-122`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Both `run()` implementations share a ~30-40-line block that resolves the executable name, builds the `exec.Cmd` (with `CmdFactory` fallback to `exec.CommandContext` + `configureProcessLifecycle`), sets `WorkDir`/`Env`, attaches `setupStreamFiles`, starts the process, emits the initial `system` event with the PID. The only meaningful differences are (a) Claude pipes `req.Prompt` over stdin, Codex passes it as a CLI arg, and (b) each parses its own JSONL.
- **Recommendation**: Extract a helper `startAgentProcess(ctx, req, command, args, stdin io.Reader) (*exec.Cmd, *bufio.Scanner, *streamWriter, []io.Closer, error)`. Both agents then differ only in (a) the args they construct and (b) the per-line parser they apply.

## 8. Swallowed JSON-unmarshal errors in `codex.go`
- **Location**: `internal/agent/codex.go` — 7 sites of `_ = json.Unmarshal(...)`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Seven `_ = json.Unmarshal(...)` calls silently discard parse errors. The current code falls back to empty-string checks, so a malformed event is treated identically to a structurally-different event. That makes future stream-format changes in the Codex CLI invisible — there will be no log to debug from when (not if) the codex JSONL shape evolves.
- **Recommendation**: Either log at debug level (gated by an env var, e.g. `ATEAM_CODEX_PARSE_DEBUG=1`) when an `Unmarshal` fails on a field that *was* present in `raw`, or aggregate a "parse-error count" into the final result event so it surfaces at end-of-session. A single helper `unmarshalField(raw, key, out) error` lets the call sites stay terse while making the error path explicit.

## 9. Repeated DB-error guard in web handlers (N+ site bundle)
- **Location**: `internal/web/handlers.go:105-108, 409-412, 740-743, 999-1002`
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Each handler that touches the DB writes the same 3-line pattern: `if db == nil && pe.dbErr != nil { http.Error(...); return } if db != nil { ... }`. Four confirmed sites today; subtle inconsistency (different status codes, different message format) has already crept in.
- **Recommendation**: Add `respondIfNoDB(w http.ResponseWriter, pe *ProjectEntry) bool` that handles the error response and returns `true` so the caller can `return`. Same shape as `if respondIfNoDB(w, pe) { return }`.

## 10. Repeated `log.Printf("warning: <op>: %v", err)` calls (N+ site bundle)
- **Location**: `internal/web/handlers.go:77, 116, 207, 237, 416, 748, 961, 1039` (8 instances)
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The `"warning:"` log prefix is hand-copied in 8 spots. Changing log routing or format requires hunting and replacing all of them; one is bound to be missed.
- **Recommendation**: Package-private `logWarn(op string, err error)` (or `func (s *Server) warn(...)`) keeps the format in one place.

## 11. Seven near-identical ANSI color helpers on `StreamFormatter`
- **Location**: `internal/runner/format_stream.go:359-406`
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `dim`, `boldMagenta`, `cyan`, `boldCyan`, `yellow`, `boldGreen`, `red` all share the same shape: check `f.Color`, prepend an escape, append the reset. The escape codes are the only thing that varies.
- **Recommendation**: One method `func (f *StreamFormatter) ansi(code, s string) string` and call sites like `f.ansi(ansiBoldMagenta, name)`. Keep named constants for readability.

## 12. `runtime.Load` errors silently discarded in cmd helpers (N+ site bundle)
- **Location**: `cmd/cat.go:117`, `cmd/env.go:96`, `cmd/table.go:1122` (and a similar early-return swallow in `cmd/code.go` around the runner-config load)
- **Scope**: module
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Three+ call sites assign `rtCfg, _ := runtime.Load(env.ProjectDir, env.OrgDir)`. A malformed `runtime.hcl` produces no diagnostic at these points — the user sees an unrelated downstream failure instead, and the pattern is inconsistent with the rest of `cmd/` which generally propagates load errors.
- **Recommendation**: In the three `_` cases, surface the error at the next user-visible point at minimum (e.g. fall back to empty pricing and warn on stderr, or return the error if the surrounding command can do so without breaking layout). Document the chosen convention in `CONFIG.md` or in `runtime.Load`'s doc comment.

## 13. `runner` re-exports `display` helpers as a back-compat facade
- **Location**: `internal/runner/runner.go:24-26, 44-45, 886-888, 969-976`; ~18 cmd-side callers (`cmd/resume.go:334`, `cmd/code.go:180`, `cmd/exec.go:258,323`, `cmd/cat.go:76`, `cmd/pool_status.go:94,152,162`, `cmd/report.go:206`, `cmd/runs.go:96,99,111`, `cmd/parallel.go:191`, `cmd/cost.go:212`, `cmd/table.go:71,701,1059,1367`, `cmd/review.go:317`, `cmd/parallel_test.go:285`)
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `runner.TimestampFormat`, `runner.ExpandHome`, `runner.Truncate`, `runner.FormatDuration`, `runner.ParseTimestampPrefix` are one-line forwarders to `internal/display` with comments like "kept as an alias for backward compatibility." 18+ callers go through the facade. The indirection makes `runner` look larger than it is and obscures which functions are real runner concerns.
- **Recommendation**: Update the ~18 callsites to import `internal/display` and delete the forwarders. Mechanical. Mechanizable check after: keep `golangci-lint`'s `unparam`/`unused` enabled so future facade re-introduction surfaces.

## 14. `internal/root` mixes resolution, project init, and DB migration
- **Location**: `internal/root/resolve.go`, `internal/root/init.go`, `internal/root/migrate_logs.go`
- **Scope**: architecture
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `root` is the natural place for "where am I?" discovery (`Resolve`, `Lookup`, `FindOrg`, `FindProject`, `ResolvedEnv`). It also owns `InstallOrg`, `InitProject`, `EnsureRoles` (one-time install actions) and `MigrateLogsLayout`, a sentinel-guarded migration that touches the filesystem AND the `calldb` schema and is called from `cmd/table.go:88,112`. Three lifecycle stages (every command vs install-time vs migration) coexist with no separation, and `migrate_logs.go` is the only reason `root` imports `calldb` (`internal/root/migrate_logs.go:11`).
- **Recommendation**: Keep `root` as discovery/resolution only. Move `InstallOrg`/`InitProject`/`EnsureRoles` to a sibling `internal/install` (or `internal/project`). Move `MigrateLogsLayout` to either `internal/calldb` (DB schema migrations) or a dedicated `internal/migrate`. Eliminates the `root → calldb` import edge and makes "what runs at install vs every command" obvious from the package layout.

## 15. `internal/runner` mixes execution with stream formatting/parsing
- **Location**: `internal/runner/{format_stream.go, format_stream_html.go, format_helpers.go, parse_stream.go, tailer.go, format.go}` (~2000 LOC); consumed by `cmd/cat.go:79,151`, `cmd/tail.go:61`, `internal/web/handlers.go:593` (`runner.HTMLStreamFormatter`)
- **Scope**: architecture
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The runner package owns both "drive an agent and collect events" and "pretty-print or HTML-render a JSONL stream after the fact." The stream-formatting code is consumed by `cmd/cat.go`, `cmd/tail.go`, and `internal/web/handlers.go`, none of which need to *run* anything. Any package that wants to display a saved stream pulls in the entire runner transitively (agent, container, calldb, fsclone, gitutil), and grepping `internal/runner` for a real execution change drowns in formatter code.
- **Recommendation**: Extract formatters into `internal/streamfmt` (or `internal/runner/format` as a subpackage). Move `format_stream.go`, `format_stream_html.go`, `format_helpers.go`, `parse_stream.go`, `tailer.go`. Keep `events.go` (phase constants used by the live progress UI) where it is or split to `runner/events`. Afterwards, `internal/web` and `cmd/{cat,tail}` import only the formatter package; runner becomes a pure execution engine.

## 16. `internal/agent` reaches into `internal/secret` for credential auditing
- **Location**: `internal/agent/claude_auth.go` imports `internal/secret`
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Of the entire `agent` package, only `claude_auth.go` (414 LOC) imports `secret` — to call `secret.MaskValue` and `secret.NewResolver` while auditing where Anthropic credentials are coming from. This is the *only* cross-cutting edge from the agent core to a non-execution concern, and it exists because the `ateam claude` / `ateam agent-config` audit feature was implemented inside the agent package instead of next to those commands.
- **Recommendation**: Move `claude_auth.go` (+ `claude_auth_test.go`) into a new `internal/agentaudit` package (or fold into `cmd/agent_config.go` if no internal callers outside `cmd/`). The agent core then depends only on `streamutil` + `container`, matching its narrow purpose.

## 17. Each `cmd/*.go` declares a 15-28-variable module-level flag block
- **Location**: `cmd/exec.go:18-37`, `cmd/report.go:16-37`, `cmd/code.go:30-50`, `cmd/review.go`, `cmd/all.go:9-35`, `cmd/eval.go`, `cmd/verify.go`, …
- **Scope**: architecture (cross-file convention)
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: Cobra's standard pattern is module-level `var foo string` plus `cmd.Flags().StringVar(&foo, ...)`. Each command repeats this for 15-28 flags, then bundles them into a typed `Options` struct (`ReportOptions`, `ReviewOptions`, `VerifyOptions`, `CodeOptions`) and passes the struct into `runXxx`. The module-level vars are a soft barrier to running two commands in the same process — relevant for `ateam all`, which sequentially feeds `runReport` → `runReview` → `runCode` → `runVerify` via shared globals.
- **Recommendation**: Declare the flag vars inside `init()` and capture into a single struct that `RunE` reads via closure (or use a small `bindFlag(struct *T, "name", ...)` helper). Lower priority than the other splits but pairs naturally with Finding 5.

## 18. `runtime` package mixes parsing with project-setup concerns
- **Location**: `internal/runtime/config.go:430-545` (`ResolveDockerfile`, `ResolvePrecheckCmd`, `WriteOrgDefaults`), `internal/runtime/dockergen.go` (`AutoSetupDockerfile`)
- **Scope**: module
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `internal/runtime` ought to be "parse runtime.hcl, hand back typed config." It also handles writing org defaults, locating per-role Dockerfiles on disk, and auto-generating Dockerfiles. The first half is pure data; the second is filesystem/I/O coupled to project layout and called only from `cmd/install.go` and `cmd/table.go:454`.
- **Recommendation**: Lift Dockerfile generation/resolution into `internal/container` (which already owns Docker concerns) or a sibling `internal/dockersetup`. Keep `runtime` pure HCL parsing + inheritance resolution.

## 19. Auto-chain between `report → review` and `code → verify` is wired inside the command implementations
- **Location**: `cmd/report.go:352-355` (`opts.Review` calls `runReview` inline), `cmd/code.go:338-343` (`runVerify` invoked unless `NoVerify`), `cmd/all.go:181` (sets `NoVerify: true` to suppress the chain so verify doesn't run twice)
- **Scope**: architecture
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A higher-level command (`runReport`) directly invokes another command's entry point (`runReview`); `runCode` does the same for `runVerify`. `runAll` then has to know to *suppress* those chains (`NoVerify: true` at `cmd/all.go:181`) so verify doesn't run twice. Cross-command calls turn each command into both a leaf and an orchestrator; the suppression flag is a tell that the design is being fought rather than followed.
- **Recommendation**: Lift the chain decisions up into `runAll` (or a small `cmd/pipeline.go`). `runReport` and `runCode` do one thing. `--review` on `ateam report` becomes a shorthand wired in pipeline code, not an embedded call. Removes the need for `NoVerify` and similar suppression flags.

## 20. Misleading `relPath` helper resolves symlinks
- **Location**: `cmd/table.go:55-64`
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `relPath(cwd, path)` calls `filepath.EvalSymlinks(cwd)` before computing the relative path. Callers reading the name reasonably expect a pure path operation; the symlink resolution produces surprising output when project roots are symlinked (common on macOS dev setups). The name hides a non-obvious effect.
- **Recommendation**: Rename to `relResolvedPath` (or add a one-line `// resolves symlinks first` comment), or split into two functions where the symlink-resolving variant is the explicit choice.

## 21. `firstNonEmpty` helper shared across files without a clear home
- **Location**: Defined in `internal/agent/claude.go:285-293`; used in `internal/agent/codex.go:206`
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The helper is a generic string utility but lives at the bottom of `claude.go`. A reader looking at `codex.go` won't see it; same-package import works, but placement implies ownership it doesn't have.
- **Recommendation**: Move to a new `internal/agent/util.go` (or fold into an existing shared file in this package alongside `setupStreamFiles`, `buildProcessEnv`, `errorEvent`, `configureProcessLifecycle`, `resolveSlice`, `resolveStringMap`, `resolveConfigDir`).

## 22. Swallowed `e.Info()` error in code-session directory walk
- **Location**: `internal/web/handlers.go:1333` (`addFile` closure inside `handleCodeSessionDetail`)
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `info, _ := e.Info()` silently drops the error; the following `if info != nil` masks the failure, so a file that becomes inaccessible mid-walk is simply absent from the listing with no signal. Race or permission issues manifest as confusing "empty" sessions.
- **Recommendation**: Capture the error, skip the entry, accumulate a count of skipped entries to include in the response (or at least `logWarn` — pairs with Finding 10's helper).

## 23. Complex condition mixing concerns in `cmd/table.go`
- **Location**: `cmd/table.go:143`
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `if (cc != nil && cc.Type != "none") || runner.IsInContainer()` mixes "container is configured" with "we are already inside a container." Future readers must dissect the boolean to know which branch they're on.
- **Recommendation**: Name the intermediates: `hasContainerConfig := cc != nil && cc.Type != "none"` then `if hasContainerConfig || runner.IsInContainer()`.

## 24. Inconsistent error wrapping in `cmd/cat.go`
- **Location**: `cmd/cat.go:91-114` (functions `runCatIDs` / its `requireProjectDB` call site)
- **Scope**: local
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Within ~25 lines: line 94 wraps with `fmt.Errorf("cannot find project: %w", err)`; lines 99 and 105 return `err` bare; line 110 wraps with `"query failed: %w"`. No consistent convention; the bare returns lose user-facing context.
- **Recommendation**: Apply the same wrapping convention to all returns in this function (prefix with the operation name, use `%w`). Local cleanup, not a project-wide style fight. Mechanizable check: `wrapcheck` linter (in `golangci-lint`) catches the bare returns from internal helpers.

## 25. Recommend enabling `dupl` / `gocognit` / `wrapcheck` in `.golangci.yml`
- **Location**: `.golangci.yml`
- **Scope**: architecture (project-wide mechanizable check)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Several findings above (6, 7, 8, 11, 13, 24) are the kind of structural debt a static-analysis linter catches mechanically and re-catches on every change. Running the LLM monthly is not a substitute for a linter in CI.
- **Recommendation**: Enable in `.golangci.yml` (already present at repo root): `dupl` (catches Findings 6, 7, 11), `gocognit`/`gocyclo` with a per-file threshold (catches the god `Run` of Finding 1 from growing further), `errcheck`/`wrapcheck` (Findings 8, 12, 22, 24), `unused`/`unparam` (Finding 13 prevents regression). Re-run `make lint` from `ci.yml`. After this is in place, the local-scope findings here should not need a future code.structure pass.

# Quick Wins

1. **Finding 9** — `respondIfNoDB` helper in `internal/web/handlers.go` (4 sites already drifting).
2. **Finding 11** — collapse 7 ANSI helpers in `format_stream.go` into one `ansi(code, s)` method.
3. **Finding 6** — `baseAgent` for shared Claude/Codex setters and `CloneWithResolvedTemplates` body.
4. **Finding 10** — `logWarn` helper for the 8 `log.Printf("warning: ...")` calls in handlers.
5. **Finding 13** — delete the `runner → display` facade and update ~18 cmd callsites; pure mechanical refactor that trims `runner`'s surface area.

# Project Context

- **Language / build**: Go 1.26 single module `github.com/ateam`; `make build`, `make test`, `make test-docker` (per `CLAUDE.md`); linter config at `.golangci.yml`.
- **Entry**: `main.go` → `cmd/root.go` (cobra). One `cmd/<name>.go` per subcommand (~38 files). Each registers module-level flag vars and delegates to `run<Name>(opts)`.
- **Layout**:
  - `cmd/` — Cobra subcommands and a 1390-LOC catch-all helper at `cmd/table.go` (Finding 3).
  - `internal/root/` — Project + org discovery (`Resolve`, `ResolvedEnv`); also project init + log-layout migration (Finding 14).
  - `internal/runtime/` — HCL parsing for `runtime.hcl` (agents, containers, profiles, inheritance); also Dockerfile generation (Finding 18).
  - `internal/runner/` — Execution engine. `runner.go` (1282 LOC, Finding 1) drives one agent run; `pool.go` parallelizes; `format_stream*.go`, `parse_stream.go`, `tailer.go` handle stream formatting (mixed concern — Finding 15).
  - `internal/agent/` — `Agent` interface + Claude/Codex/Mock implementations (Findings 2, 6, 7, 8); `claude_auth.go` pulls `internal/secret` (Finding 16); shared helpers in `agent.go`, `files.go`.
  - `internal/container/` — Docker / docker-exec containers, prepare guard.
  - `internal/calldb/` — SQLite persistence (`agent_execs` table); inline migrations.
  - `internal/prompts/` — 4-level prompt fallback (project → org → org-defaults → embedded), review selector, project info, trace helpers.
  - `internal/web/` — Embedded web UI; `handlers.go` (1433 LOC, Findings 4, 9, 10) covers every view.
  - `internal/{secret,display,gitutil,streamutil,fsclone,eval,config}/` — focused helpers, all under 500 LOC.
  - `defaults/` — embedded prompts/Dockerfile/runtime.hcl via `//go:embed`.
- **Key data flow**: `cmd/<x>.go` → `root.Resolve` → `runtime.Load` → `cmd.resolveRunner` → `runner.Runner` (single or via `runner.RunPool`) → `agent.Agent.Run` → JSONL events parsed by `agent` + persisted to `logs/<exec_id>/stream.jsonl` + `calldb`. Promoted artefacts land in `runtime/<exec_id>/` and clone to canonical paths (e.g. `roles/<id>/report.md`).
- **Concurrency contract**: `internal/runner/runner.go:71-84` and `CONCURRENCY.md`; Runner fields read-only after construction by convention (Finding 1), Agent / Container cloned per pool exec via `CloneWithResolvedTemplates` / `Clone`. SQLite serialized to one writer (`SetMaxOpenConns(1)`).
- **Hot files** (refactoring candidates by size; cross-reference findings above):
  - `internal/web/handlers.go` 1433 LOC — Findings 4, 9, 10, 22.
  - `cmd/table.go` 1390 LOC — Findings 3, 5, 20, 23.
  - `internal/runner/runner.go` 1282 LOC — Findings 1, 13, 15.
  - `cmd/agent_config.go` ~800 LOC, `internal/prompts/prompts.go` ~730 LOC — not currently flagged but worth a glance next pass.
- **Changes since prior code.structure pass**: Only `internal/agent/codex.go` arg ordering, a small addition to `internal/runtime/config.go`, and docs/prompt edits. No structural finding above has been resolved; all carry forward.
- **Suggested automation**: see Finding 25 (`.golangci.yml` enabling `dupl`, `gocognit`, `gocyclo`, `errcheck`, `wrapcheck`, `unparam`). Many LOW-severity items above would be caught and prevented from returning.
