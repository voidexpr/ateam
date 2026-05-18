# Supervisor Review — 2026-05-13_22-12-50

### Project Assessment

ateam is in good structural health: clear package boundaries, documented concurrency contract, and a clean working tree on an actively iterated pre-1.0 CLI. The strongest signal across all four reports is that the bug surface is small and localized (one data-loss path, a cluster of silent-failure scanners) while the structural debt is concentrated in three accreted catch-all files (`cmd/table.go`, `internal/web/handlers.go`, `internal/runner/runner.go`). Recent commits are docs/prompt-tuning and an A/B prompt-base split that is intentionally time-bound — that area should not be touched.

### Priority Actions

#### 1. Fix `FileStore.Set` silently wiping all secrets on transient read error

- **Action**: In `internal/secret/store.go:84`, stop discarding the error from `readLines(s.Path)`. Distinguish `os.IsNotExist` (treat as empty) from any other error (return the error to the caller so the existing file is not overwritten with a single-entry degenerate file). Add a regression test exercising a non-existent file and a read-error path (e.g. mode 0000).
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: This is the only HIGH-severity finding across all reports. A transient `os.Open` failure on `.env` permanently destroys every previously stored secret on the next `Set`. Single-file fix with high blast radius if hit.

#### 2. Check `scanner.Err()` in the four streaming scan loops

- **Action**: After each `for scanner.Scan()` loop in `internal/agent/claude.go:155`, `internal/agent/codex.go:117-124`, `internal/runner/events.go:62-77`, and `internal/runner/format.go:63-83`, check `scanner.Err()` and surface it (error event in agents; logged error in the recovery readers). Consider raising the per-line cap above 1MB since Claude tool-result blocks legitimately exceed it.
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Today, a single >1MB JSONL line silently terminates the scanner and a successful run is recorded as "no result event". Four near-identical sites; trivial fix; eliminates a real, reproducible failure mode in long-output runs.

#### 3. Fix `cmd_sleep` in scripts/claude-usage.py to wait on the actually-breached window

- **Action**: In `scripts/claude-usage.py:250-261`, replace the unconditional read of `data["five_hour"]["resets_at"]` with logic that picks the latest `resets_at` among breached windows (or the 7-day reset when only the 7-day breached). Add `h` (hours) to `parse_duration` in `scripts/claude-usage.py:112-118` while in the file. Drop or actually use the unused `cache_status` parameter in `cmd_cat`.
- **Source Role**: code.recent (2026-05-13_20-31-33)
- **Source Report**: .ateam/roles/code.recent/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Sleep currently returns ~5h early when only the 7-day window is over threshold; users invoking the script as a gate get false greenlight. Same-file cluster of small wins.

#### 4. Make `fsclone.byteCopy` leave the destination consistent on failure

- **Action**: In `internal/fsclone/clone.go:73-76`, on `io.Copy` failure best-effort `os.Remove(dst)` before returning the error (or write to `dst+".tmp"` then rename on success). Caller in `runner.promoteRuntimeFiles` continues past the error, so the truncated file currently survives.
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Canonical artifacts can end up half-written and undetectable. Self-contained fix.

#### 5. Surface the silent `git status` / `bufio.Scanner` / pool-cancel papercuts

- **Action**: Three small cleanups, batched because each is one-line:
  - `internal/gitutil/gitutil.go:46-49` — return the error from `git status` instead of returning `(meta, nil)`; or add a `StatusErr` field to the returned struct (Scope: contract).
  - `internal/runner/pool.go:65-71` — replace `continue` with `break` on the `ctx.Done()` branch so `PreDispatch` does not run for tasks that will never dispatch (Scope: local).
  - `internal/calldb/calldb.go:147-148` — scan into `var n int` then compare `> 0`, and stop swallowing the scan error (Scope: local).
  - `internal/agent/claude.go:269` and the analogous `codex.go` block — either implement the "skip error event if a result was already sent" check the comment promises, or rewrite the comment to describe the reconcile-driven design (Scope: contract).
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: A bundle of one-liners that each remove a silent-failure mode. Cheap enough to land in one commit.

#### 6. Centralize pricing conversion into `runtime.AgentPricing.ToTable()`

- **Action**: Extract the `runtime.AgentPricing` → `agent.PricingTable` conversion currently duplicated in `cmd/table.go:378-392` (`buildPricingFromConfig`) and inline at `internal/web/handlers.go:579-591` into a method on `runtime.AgentPricing` (returning `(agent.PricingTable, string)`). Update `cmd/cat.go:126` and `internal/web/handlers.go` to call it; keep `mergedPricingFromConfig` adjacent.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Two identical bodies, one easy to forget on schema change. Mechanical fix; lays groundwork for Action 7 and the `handlers.go` split (Action 9).

#### 7. Move per-run `model` / `effort` / `max_budget_usd` off the `Agent` interface

- **Action**: Remove `SetModel`, `SetEffort`, `SetMaxBudgetUSD` from the `Agent` interface (`internal/agent/agent.go:39-51`) and the three implementations (`claude.go`, `codex.go`, `mock.go`). Add the fields to `agent.Request` (or equivalently to `runner.RunOpts`, since the runner builds the request at `runner.go:653`). Rewrite the five `cmd/` call sites (`cmd/table.go:764,772,829,859`, `cmd/eval.go:441`) so the values flow through `RunOpts` instead of mutating the shared agent. The existing `claudeArgs`/`codexFlagArgs` helpers already parameterize on these strings.
- **Source Role**: design.architecture (2026-05-13_22-01-31)
- **Source Report**: .ateam/roles/design.architecture/report.md
- **Priority**: P1
- **Effort**: MEDIUM
- **Rationale**: The "MUTATES — call before sharing with a pool" contract is documentation-only in an otherwise clone-or-immutable codebase. Removing it eliminates a documented temporal-coupling rule and a future race waiting to happen.

#### 8. Mechanical first slice of `cmd/table.go`: lift runner + container builders

- **Action**: Move runner builders (`newRunner`, `newRunnerFromAgent`, `newRunnerDefault`, `resolveRunner`, `resolveRunnerMinimal`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `buildAgent`, `buildPricingFromConfig`, `mergedPricingFromConfig`) out of `cmd/table.go` into a new `cmd/runner_build.go`, and container helpers (`buildContainer`, `deriveDockerImageName`, `resolveVolumePath`, `splitVolumeSpec`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerExecOutput`, `dockerCp`, `resolveContainerName`, `applyContainerName*`) into a new `cmd/container_build.go`. No call-site changes; mechanical move. (Scope: architecture / placement)
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: `cmd/table.go` is a 1390-LOC catch-all that gets touched by every command change. This is the cheapest first slice — it builds momentum for the rest of the split (flags, budget, db_helpers, dryrun, pool_run) without any API change.

#### 9. Split `internal/web/handlers.go` by feature; extract `internal/sessions` view layer

- **Action**: Land the split as two passes:
  1. **Mechanical file split** — move handlers into `overview.go`, `reports.go`, `supervisor.go`, `runs.go`, `cost.go`, `sessions.go`, `code_sessions.go`, `legacy_layout.go` per the code.structure recommendation. Keep `handlers.go` for shared utilities or rename it `helpers.go`. (Scope: module)
  2. **Layer extraction** — pull the data-aggregation helpers (`enrichRuns`, `resolveRunFiles`, `buildSessions`, `scanCodeSessions`, `buildCodeSessionEntry`, `historyAction`/`navKind`/`supervisorLabel`, legacy path resolvers) into a new `internal/sessions` (or `internal/view`) package returning ready-to-render types. `export.go` consumes the same layer. (Scope: placement / architecture)
- **Source Role**: code.structure (2026-05-13_20-34-29) + design.architecture (2026-05-13_22-01-31)
- **Source Report**: .ateam/roles/code.structure/report.md, .ateam/roles/design.architecture/report.md
- **Priority**: P2
- **Effort**: LARGE
- **Rationale**: Both reports independently flagged this; structure focused on size, architecture on layer-mixing. The split is the cheapest way to make a future JSON API a one-handler change instead of a dual implementation. Do step 1 before step 2.

#### 10. Extract `internal/layout` from `internal/root` to own project-directory paths

- **Action**: Create `internal/layout` (leaf package, takes only `projectDir string` and `execID int64`). Move the canonical helpers from `internal/root/resolve.go:31-74` into it. Have `internal/root.ResolvedEnv`, `internal/runner/template.go:148-155` (`runtimeDirFor`/`logsDirFor`), and `internal/web/{server.go,handlers.go}` delegate to it — the runner's existing "no dependency on root" carve-out goes away because layout is a leaf. Remove the inline `filepath.Join(pe.ProjectDir, "supervisor"|"roles"|"runtime"|"code", ...)` literals scattered through `handlers.go`. (Scope: placement)
- **Source Role**: design.architecture (2026-05-13_22-01-31)
- **Source Report**: .ateam/roles/design.architecture/report.md
- **Priority**: P2
- **Effort**: MEDIUM
- **Rationale**: Layout knowledge is duplicated across three packages with several string-literal directory names. Closes the "validation duplicated across layers" smell that has historically required edits in three places (most visibly the legacy → new logs migration).

#### 11. Adopt `staticcheck`, `dupl`, and `gocyclo` as a local `make` target

- **Action**: Add a `make lint-deep` (or similar) target that runs `staticcheck ./...`, `dupl -threshold 50 ./...`, and `gocyclo -over 30 ./...` and is documented in `README.md`. These tools mechanically catch the classes of issue (dead helpers like `cmd.applyModel`, duplicate pricing conversion, oversize `runner.Run`/`handleRunFile`) that the structural role keeps re-finding. Wire to `make test` only if signal is clean enough; otherwise leave as an opt-in target reviewers can run.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: One tool adoption replaces N future review cycles of the same finding class. Local-only target respects the project's documented "local-first gating" stance — no CI workflow is added.

### Deferred

- **A/B base prompt cleanup** (`internal/prompts/prompts.go:51,114-119`; stale heading example in `defaults/new_report_base_prompt.md`). Explicitly time-bound debt with a "TODO: fix this before v1 — merge with ReportBasePromptFile once the new role set is validated" marker; the recent commit (`d543917`) deliberately keeps the bases identical apart from two intentional deltas to preserve the A/B comparison baseline. Do not touch until the A/B verdict lands. (code.structure #11, code.recent finding 5)

- **Legacy log-layout removal** (`root.IsLegacyStreamFile`, dual branches in `handlers.go`, `cmd/inspect.go`, `cmd/pool_status.go`, `cmd/resume.go`). Removal is correct in principle but depends on confirming no real project still carries pre-`.layout-v2` rows. Both reports recommend waiting on that verification rather than deleting blindly. Revisit after Action 9 lands (the split makes legacy code easier to delete in one place). (code.structure #4, design.architecture #4)

- **`PrepareGuard` caching `context.Canceled` across unrelated callers** (`internal/container/prepare_guard.go:17-20`). LOW severity, MEDIUM effort. Real but only triggers when a pool's first prepare is interrupted before any other worker starts; the typed-sentinel fix interacts with the docker_exec lifecycle and warrants its own focused change. (code.bugs #5)

- **`parse_pct` vs `parse_duration` error-reporting inconsistency** in `scripts/claude-usage.py`. Style drift on a new file; cosmetic. (code.recent)

- **Split `cmd/agent_config.go` into four subcommand files**. Low-severity readability win, no API change. Park until the larger `cmd/table.go` split is in flight to avoid churn. (code.structure #7)

- **`ClaudeAgent` / `CodexAgent` shared base struct, `StreamFormatter` / `HTMLStreamFormatter` generalization**. Both reports explicitly recommend deferring these until a third agent / third format actually arrives — current siblings cost less than premature abstraction. (code.structure #8, #9)

- **Refactor `runner.Run` into `prepareRun` / `runEventLoop` / `assembleSummary`**. Worthwhile but interacts with cost classification, budget caps, stall watchdog, and cancellation — all paths that have churned recently. Defer until the active development direction settles to avoid merge conflicts; the function is well-commented in the interim. (code.structure #3)

- **Collapse Cobra command boilerplate into a `cmd.setupRun` helper**. Real boilerplate, but the per-command variation is meaningful and `RunnerOverrides` already harvested the easy share. Worth doing after Actions 8 + 9 reduce the surrounding noise. (code.structure #6)

- **Parse `agent_execs.batch` via a `calldb.ParseBatch` helper**. LOW severity with only two consumers today; flagged mostly as a marker. (design.architecture #5)

- **Delete `cmd.applyModel` if grep confirms no callers**. Truly trivial; will be picked up automatically by `staticcheck` once Action 11 lands. (code.structure #10)

### Conflicts

No contradictions between roles. `code.structure` and `design.architecture` both target `internal/web/handlers.go` and `cmd/table.go`, but from complementary lenses (file size vs. layer placement). Action 9 explicitly merges their recommendations into a two-pass split so the structural move precedes the layer extraction. `code.structure`'s "defer the runner.Run refactor" recommendation (severity MEDIUM) is honored in Deferred rather than promoted, consistent with sequencing guidance to let active code paths settle.

### Notes

- The codebase has an unusually disciplined concurrency contract (CONCURRENCY.md, `Runner.RunPool` boundary, per-exec `Clone` machinery). The `Agent` setters in Action 7 stand out *because* the rest of the project is so consistent — fixing them shrinks the documentation-only surface to zero.
- Three different reports independently noted the A/B prompt-base TODO. The cleanup path is already spelled out in the commit message for `d543917`; the only structural risk is that the cleanup never happens. Worth a tracking issue so the `TODO: fix this before v1` markers don't outlive the eval.
- Recent commits are all docs/prompt/script-tuning — there is room to land structural work (Actions 6, 7, 8, 10, 11) without competing for the same lines. Defer Actions 3 and the runner.Run refactor relative to areas of recent churn.
- `code.bugs` produced eight findings with one HIGH, four MEDIUM, and three LOW. The HIGH and the four MEDIUM are all in Priority Actions; this is exactly the calibration the role should keep applying.
- `code.recent` correctly scoped itself to the visible diff and resisted reaching for structural debt in untouched files — that discipline is what makes the report useful here.
