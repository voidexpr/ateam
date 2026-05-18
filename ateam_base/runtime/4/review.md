# Supervisor Review — 2026-05-13_21-47-34

### Project Assessment

The ateam codebase is in a generally healthy state: clear package boundaries, a documented concurrency contract, table-driven tests, and a consistent cobra-based command layout. Two structural patterns dominate the review: (a) duplication concentrated in a few hotspots — `internal/agent/{claude,codex}.go`, `internal/web/handlers.go`, and the per-command setup sequence in `cmd/` — and (b) three oversized "kitchen-sink" files (`cmd/table.go` 1390 LOC, `internal/web/handlers.go` 1433 LOC, `internal/runner/runner.go` 1281 LOC) that make navigation harder than the underlying logic warrants. The deepest structural debts (god `Runner`, mutation-based `Agent` contract) are real but expensive to repay and not blocking; the higher-leverage wins are the small dedup helpers and one or two file splits.

### Priority Actions

#### 1. Adopt `golangci-lint` with `dupl`, `errcheck`, `gosimple`, `unused`, `unparam`, `gocognit` enabled
- **Action**: Add a minimal `.golangci.yml` at repo root enabling `dupl`, `errcheck`, `gosimple`, `unused`, `unparam`, `gocognit` (the project already has a `.golangci.yml` per the architecture report — extend it). Wire `golangci-lint run` into a `make lint` target and into `make test` (or as a pre-commit hook), staying local-only per project policy (no CI/CD). Fix or `//nolint:` annotate the initial findings in the same change.
- **Source Role**: refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: The refactor_small report explicitly suggests this and it lines up with the principle "prefer deterministic tools to recurring LLM audits". Most LOW-severity items in this cycle (swallowed errors, dead wrappers, near-duplicate methods, shadowed returns) are mechanically detectable. One tool adoption substitutes for several future LLM passes on the same class of issue.

#### 2. Remove the `runner → display` facade — inline the 5 forwarders at ~11 callsites
- **Action**: Delete `runner.TimestampFormat`, `runner.ExpandHome`, `runner.Truncate`, `runner.FormatDuration`, `runner.ParseTimestampPrefix` from `internal/runner/runner.go` (lines 24-26, 44-45, 888, 961-976) and update the ~11 cmd callsites (`cmd/resume.go:334`, `cmd/code.go:180`, `cmd/exec.go:258`, `cmd/cat.go:76`, `cmd/pool_status.go:94,152`, plus the others surfaced by the build) to import `internal/display` directly. Run `make build` + `make test` to confirm.
- **Source Role**: refactor_architecture (2026-05-13_20-35-31), refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_architecture/report.md (Finding 6), .ateam/roles/refactor_small/report.md (Finding 10)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Both reports independently flag this. Pure mechanical cleanup, no behavior change, trims the public surface of `internal/runner` and makes the next runner-package work easier to scan. Quick win cited by both roles.

#### 3. Deduplicate `ClaudeAgent` and `CodexAgent` via an embedded `baseAgent` + shared `startAgentProcess` helper
- **Action**: Create an unexported `baseAgent` struct in `internal/agent/` holding the shared fields (`Command`, `Args`, `Model`, `Effort`, `MaxBudgetUSD`, `DefaultModel`, `Pricing`, `Env`) and the byte-for-byte identical methods (`ModelName`, `SetModel`, `SetEffort`, `SetMaxBudgetUSD`, `AgentEnv`, body of `CloneWithResolvedTemplates`). Embed in `ClaudeAgent` and `CodexAgent` (`internal/agent/claude.go:31-54`, `internal/agent/codex.go:32-55`). Then extract the 30-40 LOC process-startup block (resolve executable → build `exec.Cmd` with `CmdFactory` fallback → set `WorkDir`/`Env` → attach `setupStreamFiles` → start → emit initial `system` event) into `startAgentProcess(ctx, req, command, args, stdin io.Reader) (...)` and call it from both `run()` methods (`internal/agent/claude.go:88-127`, `internal/agent/codex.go:78-113`). Move the `firstNonEmpty` helper into a shared `internal/agent/helpers.go` while you are in the file.
- **Source Role**: refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_small/report.md (Findings 1, 2, 9)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: This is the largest concrete duplication in the codebase and the highest drift risk — the two agent types must stay behaviorally aligned. Doing it now also makes the deeper architectural fix (move mutators onto `Request`, Finding arch #2) much cheaper later, because there will be a single base type to change.

#### 4. Add `respondIfNoDB` and `logWarn` helpers in `internal/web/handlers.go`
- **Action**: In `internal/web/handlers.go`, add a `respondIfNoDB(w http.ResponseWriter, pe *projectEnv) bool` helper that encapsulates the 3-line `if db == nil && pe.dbErr != nil { http.Error(...); return true }` pattern, and a `logWarn(op string, err error)` helper for the `log.Printf("warning: %s: %v", ...)` pattern. Replace the 6+ DB-guard sites (around lines 104-125, 408-420, 739-756, 1030) and the 8 warning-log sites (lines 77, 116, 207, 237, 416, 748, 961, 1039) with calls to the helpers. Pick a single canonical HTTP status + message format while you consolidate.
- **Source Role**: refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_small/report.md (Findings 4, 5)
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Sequenced before any handlers.go file split — once these helpers exist, the split (deferred) becomes a pure file move with no risk of fixing inconsistencies in only some of the pieces. Already produced visible drift (different status codes / wording).

#### 5. Split `cmd/table.go` into 4–5 focused files (pure file split, no semantic change)
- **Action**: Move helpers out of `cmd/table.go` (1390 LOC) into:
  - `cmd/runner_build.go` — `newRunner`, `newRunnerFromAgent`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `newRunnerDefault`, `resolveRunner`, `resolveRunnerMinimal`, `applyContainerName*`.
  - `cmd/container_build.go` — `buildContainer`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerCp`, `dockerExecOutput`, `resolveContainerName`, `resolveVolumePath`, `splitVolumeSpec`, `gitWriteDirs`, `execGitCmd`.
  - `cmd/flags.go` — `addProfileFlags`, `addContainerNameFlag`, `addBudgetFlags`, `addCheaperModelFlag`, `addVerboseFlag`, `addForceFlag`, `addDockerAutoSetupFlag`, `parseBudgetUSD`.
  - `cmd/dry_run.go` — `printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`.
  - Keep tabwriter / `printDone` / `ExitError` plumbing in `cmd/table.go` (or rename to `cmd/output.go`).
  Same package, no exported identifiers change. Verify with `make build` + `make test`.
- **Source Role**: refactor_architecture (2026-05-13_20-35-31)
- **Source Report**: .ateam/roles/refactor_architecture/report.md (Finding 3)
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: The file's name no longer describes its contents; everything else in `cmd/` is one-purpose per file. Sequenced before action 6 — once runner construction lives in its own file, extracting `prepareRun` is a localized edit.

#### 6. Extract `cmd.prepareRun` to dedupe the per-command setup boilerplate
- **Action**: Introduce `func prepareRun(action string, opts CommonRunOpts) (*runContext, error)` in the new `cmd/runner_build.go` (or a new `cmd/run_context.go`) that returns `{env, runner, db, ctx, cancel}` and exposes a single `Close()` chaining `db.Close()` + `cancel()`. Replace the ~30-line opening sequence in `cmd/exec.go`, `cmd/report.go:132-205`, `cmd/review.go:152-228`, `cmd/verify.go:100-160`, `cmd/code.go`, and `cmd/parallel.go` with a 3-line call. Add a `prepareRunBatch` variant for the pool commands (`report`, `parallel`, `code`) returning the batch token + pre-dispatch budget closure. The per-role second-runner build in `cmd/report.go:235-252` becomes a call to the same helper.
- **Source Role**: refactor_architecture (2026-05-13_20-35-31)
- **Source Report**: .ateam/roles/refactor_architecture/report.md (Finding 5)
- **Priority**: P2
- **Effort**: MEDIUM
- **Rationale**: 30→3 LOC per command and prevents the drift already visible (e.g. `verify.go` adding `setSourceWritable`, `report.go` partially reinventing the sequence). Lower than the file split because the file split makes the function easier to find a home for.

#### 7. Stop discarding `runtime.Load` errors at four cmd helpers
- **Action**: At `cmd/cat.go:117`, `cmd/code.go:407-409`, `cmd/env.go:96`, `cmd/table.go:1122`, replace `rtCfg, _ := runtime.Load(...)` (and the `if err != nil { return nil }` swallow in `code.go`) with proper propagation. Either return the error from the helper (preferred, lets cobra format it) or capture it and surface it once at the next user-visible point so a malformed `runtime.hcl` produces a clear diagnostic instead of a confusing downstream failure.
- **Source Role**: refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_small/report.md (Finding 7)
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: User-visibility win, one-line edits, prevents silent config-error confusion. Effectively zero risk.

#### 8. Collapse the six `StreamFormatter` ANSI color helpers into one `ansi(code, s)` method
- **Action**: In `internal/runner/format_stream.go:359-406`, replace `dim`, `boldMagenta`, `cyan`, `boldCyan`, `yellow`, `boldGreen`, `red` with a single `func (f *StreamFormatter) ansi(code, s string) string` that checks `f.Color`, prepends `code`, appends the reset. Keep the escape sequences as named constants (`ansiBoldMagenta`, …) for readability. Update callsites.
- **Source Role**: refactor_small (2026-05-13_20-31-48)
- **Source Report**: .ateam/roles/refactor_small/report.md (Finding 6)
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Pure mechanical cleanup, minutes of work. Small enough that golangci-lint's `dupl` would find it on the next pass; pre-empting it costs less than the cycle.

### Deferred

- **Decompose god `Runner` and 440 LOC `Run` method** (refactor_architecture #1, HIGH/LARGE): The split of `Runner` into immutable `RunnerConfig` + per-call `Execution`, and `Run` into `prepare/execute/finalize`, is the right direction but is a large structural change that overlaps with action 3 (agent dedup) and action 6 (`prepareRun`). Land those first; revisit once the surrounding noise is gone.
- **Move `Agent` mutators (`SetModel`, `SetEffort`, `SetMaxBudgetUSD`) onto `Request`** (refactor_architecture #2, HIGH/MEDIUM): Sensible direction but touches the `Agent` interface contract and every override path (`applyRunnerOverrides`, per-role override in `cmd/report.go:235-252`). Defer until action 3 lands a shared `baseAgent`, which makes this much cheaper to do in one place.
- **Split `internal/web/handlers.go` by feature area** (refactor_architecture #4, MEDIUM/MEDIUM): Worthwhile but lower payoff than the helper dedup (action 4). Once `respondIfNoDB` / `logWarn` exist, this becomes a pure file move that can be scheduled freely.
- **Decide whether Codex `_ = json.Unmarshal(...)` errors should be logged** (refactor_small #3, MEDIUM): Has a real tradeoff — gated debug-level logging vs an aggregated "parse-error count" event vs leaving it. This is a product decision (what the user should see), and per the principle "report but skip ambiguous tasks or tasks with tradeoffs", leave it for a maintainer call.
- **Extract stream formatters out of `internal/runner` into `internal/streamfmt`** (refactor_architecture #8, MEDIUM): Real architectural improvement (the web layer transitively pulls in agent/container/calldb today) but no immediate pain; expensive to do mid-refactor.
- **Move install/init/migrate out of `internal/root`** (refactor_architecture #7, MEDIUM): Conceptually right (eliminates the `root→calldb` edge), but the package isn't large enough today for the navigation cost to be felt.
- **Move `internal/agent/claude_auth.go` into a new `internal/agentaudit`** (refactor_architecture #9, LOW): Single-file move; defer until there's a reason to touch the audit feature.
- **Lift report→review / code→verify auto-chain up into `runAll`** (refactor_architecture #12, LOW): The implicit cross-command calls are messy but the workaround (`NoVerify: true`) works. Touches user-facing flags; revisit if the auto-chain grows another consumer.
- **Replace per-command `var` flag blocks with closure-bound `Options` structs** (refactor_architecture #10, LOW): Wide reach, ~26 files, and primarily a style preference. Defer.
- **Lift Dockerfile generation/resolution out of `internal/runtime`** (refactor_architecture #11, LOW): Minor reorg; not worth the import-graph churn right now.

### Nits (record-only, not actioned)

- Rename `relPath` or document the `EvalSymlinks` behavior (refactor_small #8).
- Move `firstNonEmpty` to a shared `internal/agent/helpers.go` (folded into action 3 above).
- Log skipped entries in `handleCodeSessionDetail`'s `e.Info()` walk (refactor_small #11).
- Add a docstring to `codeSessionTimestamp` explaining the three-tier fallback and the meaning of the returned `bool` (refactor_small #12).
- Name the intermediate `hasContainerConfig` at `cmd/table.go:143` (refactor_small #13).
- Consistent `%w` error wrapping in `cmd/cat.go:94-113` (refactor_small #14).

### Conflicts

No direct contradictions between the two reports. Where they overlap they reinforce each other:

- Both flag the `runner → display` facade as a quick mechanical removal (refactor_architecture #6 = refactor_small #10). Resolved as Priority Action 2.
- `refactor_architecture` recommends moving `Model`/`Effort`/`MaxBudgetUSD` onto `Request` and deleting the setters entirely; `refactor_small` recommends keeping the setters but sharing them via an embedded `baseAgent`. These are different end-states for the same target. Resolution: take the `refactor_small` step first (action 3) because it is mechanical and risk-free; treat `refactor_architecture #2` as the follow-up that can land cleanly afterward (kept in Deferred).

### Notes

- **Hotspot pattern**: the three biggest files (`cmd/table.go`, `internal/web/handlers.go`, `internal/runner/runner.go`) account for most of the architectural findings between them. None of them suffer from a single cohesive problem — they have just accumulated unrelated helpers. The cheapest leverage is the file splits and helper extractions (actions 4, 5, 6); they preserve behavior and unblock the harder refactors.
- **Sequencing**: actions 3 (agent dedup) → arch#2 (mutators to Request), action 4 (web helpers) → arch#4 (handlers split), action 5 (table.go split) → action 6 (prepareRun). Each pair lands the structural change first so the follow-up is a localized edit.
- **Lint adoption (action 1)** will mechanically pick up most LOW-severity items the small-refactor role keeps flagging (swallowed errors, dead wrappers, near-identical methods). Future cycles should read the lint output before re-running the role on the same areas.
- **No CI/CD recommendations are surfaced** — the project's pipeline is local-first (`make build` / `make test` / `make test-docker`), and all proposed gates (golangci-lint, pre-commit hook) stay local.
- **Cycle 4 baseline**: no prior supervisor review on disk for this slot; this is the first cycle for runtime/4.
