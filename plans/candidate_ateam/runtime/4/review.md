# Supervisor Review — 2026-05-13_20-50-55

### Project Assessment

ateam is a healthy ~44k-LOC Go CLI with clear package boundaries, documented concurrency invariants, and conservative error handling. The most pressing issue is a HIGH-severity data-loss path in the secret store that silently wipes existing secrets on a transient read error; alongside it are a cluster of silent stream-truncation bugs that masquerade as "no result event" failures. Structurally, three files (`cmd/table.go`, `internal/web/handlers.go`, `internal/runner/runner.go`) have become catch-alls and are the dominant readability tax, but they are mechanical to split without API change.

### Priority Actions

#### 1. Fix `FileStore.Set` silent secret-wipe on read error

- **Action**: In `internal/secret/store.go:84`, stop discarding the `readLines` error. Distinguish `os.IsNotExist` from all other errors; on any non-`IsNotExist` error from `os.Open`, return the error to the caller instead of treating `lines == nil` as "no prior secrets" and rewriting the file with only the new entry. Add a test that simulates a read failure (e.g. mode 0000 or a directory at the path) and asserts the existing file is not truncated.
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Highest-severity finding in the cycle. Triggers a silent loss of every previously stored secret on any transient EIO/EACCES/replaced-by-directory condition. Single-file fix, easy test.

#### 2. Check `scanner.Err()` in all four 1MB-buffered stream scanners

- **Action**: After the `for scanner.Scan()` loop in `internal/agent/claude.go:155`, `internal/agent/codex.go:117`, `internal/runner/events.go:62`, and `internal/runner/format.go:63`, add `if err := scanner.Err(); err != nil { ... }` handling. In the agent sites, surface as an `error` event so `classifyFailure` no longer reports a successful-but-fat-line run as "no result event". In the recovery readers (`events.go`, `format.go`), log or return the error so dropped result lines are visible. Also consider raising the 1MB per-line cap to match observed Claude output.
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: Affects four sites with the same idiom; reproducible whenever an agent emits a JSONL line >1MB (large `Read` tool result, base64 image, MCP dump). Silently misclassifies successful runs and hides recovery failures.

#### 3. Make `fsclone.Clone` byte-copy atomic on failure

- **Action**: In `internal/fsclone/clone.go:73-76`, change the byte-copy fallback so failure does not leave a truncated `dst` on disk. Either `os.Remove(dst)` (best-effort) before returning the error, or write to `dst+".tmp"` and `os.Rename` on success. The caller `runner.promoteRuntimeFiles` (`internal/runner/runner.go:1177-1187`) currently logs and continues, so any failure right now leaves a torn canonical file (the previous good copy was already removed at line 30).
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Direct cause of corrupted forensic output on disk-full or mid-write failure. Small, well-bounded change.

#### 4. Fix `cmd_sleep` to wait on the actually-breached window

- **Action**: In `scripts/claude-usage.py:250-261`, change `cmd_sleep` to look at the breaches list (the same one `threshold_exit_code` consults), not just the first exit code. Sleep until the latest `resets_at` among breached windows. When only the 7-day window is breached, sleep until `seven_day.resets_at` (or `sys.exit` with a clear message if unavailable). Today the function unconditionally sleeps to `five_hour.resets_at`, which means a 7-day-only breach gets a ≤5h wait and the caller proceeds while still over threshold. Scope: `local`.
- **Source Role**: code.recent (2026-05-13_20-31-33)
- **Source Report**: .ateam/roles/code.recent/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Recently introduced script; semantic correctness issue on a tool the user actively relies on for budget gating.

#### 5. Extract pricing conversion to a single helper

- **Action**: Move `buildPricingFromConfig` and `mergedPricingFromConfig` out of `cmd/table.go:378-392` into a method on `runtime.AgentPricing` (e.g. `func (p AgentPricing) ToTable() (agent.PricingTable, string)`). Update the call site in `cmd/cat.go:126` and replace the inline duplicate in `internal/web/handlers.go:579-591` with a call to the helper. Scope: `module`.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Concrete duplication that has already drifted (inline copy in a 90-line handler is easy to miss). Removing it kills a class of "remember to update the other place" bugs before the larger handlers.go split lands.

#### 6. Adopt `staticcheck` (and run `gocyclo`/`dupl` once) before structural splits

- **Action**: Add `staticcheck ./...` to the local `make` target (or a `make lint`) and fix what it surfaces. Run `gocyclo -over 30 ./...` and `dupl -threshold 50 ./...` once and capture the list — the structural report predicts they will flag `runner.Run`, `handleRunFile`, `runAgentConfig`, plus the pricing/agent-boilerplate duplication. The dead `cmd.applyModel` (`cmd/table.go:770-774`) flagged in finding 10 is the kind of thing `staticcheck` catches mechanically. Scope: `architecture`.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Per the supervisor principle: when the same class of issue keeps appearing across cycles (dead helpers, duplication, oversized functions), a deterministic tool replaces N future LLM audits. Cheap to adopt and the local-first gating policy explicitly favors `make`-driven checks over CI workflows.

#### 7. Split `cmd/table.go` (mechanical lift, no API change)

- **Action**: Move runner/container builders, flag adders, budget helpers, DB helpers, and dry-run printers out of `cmd/table.go` (1390 LOC) into the topic files named in the structure report: `cmd/runner_build.go`, `cmd/container_build.go`, `cmd/flags.go`, `cmd/budget.go`, `cmd/db_helpers.go`, `cmd/dryrun.go`, `cmd/pool_run.go`. Leave only the small shared utilities (`ExitError`, `newTable`, `relPath`, `printDone`, `isTerminal`, `stdinIsPiped`) or rename the residue `cmd/common.go`. Pure mechanical move; call sites unchanged. Scope: `architecture | module`.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P2
- **Effort**: LARGE
- **Rationale**: Highest readability tax in the codebase, but mechanical and low-risk. Order matters: do this before further `cmd/` refactors (finding 6 in the structure report) so the boilerplate-reduction pass lands on a clean layout.

#### 8. Split `internal/web/handlers.go` by feature

- **Action**: Break `internal/web/handlers.go` (1433 LOC) into feature files: `overview.go`, `reports.go`, `supervisor.go`, `runs.go`, `cost.go`, `sessions.go`, `code_sessions.go`, `legacy_layout.go`. Move each handler with its page-data struct. Keep `requireProject`, `readFileWithModTime`, `isPathWithin`, `capitalizeASCII` in a `helpers.go`. No API or template changes. Concentrating the legacy-layout resolvers (`promptDir`, `resolvePromptFile`, `resolveOutputFile`, `resolveHistoryFile`) into one file is a precondition for eventually removing them. Scope: `architecture`.
- **Source Role**: code.structure (2026-05-13_20-34-29)
- **Source Report**: .ateam/roles/code.structure/report.md
- **Priority**: P2
- **Effort**: MEDIUM
- **Rationale**: Second-largest catch-all; every web change pays the navigation cost. Mechanical move; sequencing this after #5 means the duplicated pricing conversion is already gone before files are reorganized.

#### 9. Surface `git status` failure in `GetProjectMeta`

- **Action**: In `internal/gitutil/gitutil.go:46-49`, stop returning `(meta, nil)` with `meta.Uncommitted == nil` when `git status --porcelain` fails. Return the error (or add a `StatusErr` field) so callers can distinguish "clean tree" from "git is unhealthy" (index.lock held, `.git` ownership mismatch, corrupt index).
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: One-liner that closes a silent-failure mode; low blast radius if it surfaces unhealthy git state.

#### 10. Quick correctness/hygiene cleanups in stream + pool + calldb

- **Action**: Three small follow-ups, do them together:
  - `internal/runner/pool.go:65-71`: replace `continue` with `break` on the `ctx.Done()` branch so `PreDispatch` is not called for tasks that will never dispatch.
  - `internal/calldb/calldb.go:147-148`: scan COUNT() into `var n int` and derive `hasOldTable = n > 0`; stop swallowing the scan error — log it. Prevents a latent migration failure on legacy `agent_execs(task_group)` databases.
  - `internal/agent/claude.go:261-282` (and the analogous `codex.go`): either implement the "skip error event when a result was already sent" check the comment promises (gate on `resultEv != nil && resultEv.Type == "result"`), or rewrite the comment to describe the reconcile-driven design.
- **Source Role**: code.bugs (2026-05-13_20-35-51)
- **Source Report**: .ateam/roles/code.bugs/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Three independent one-to-five-line fixes; bundling avoids the per-PR overhead.

### Deferred

- **PrepareGuard caches context-cancellation forever** (`internal/container/prepare_guard.go:17-20`, code.bugs LOW): real but only bites when the first container prepare of a pool is Ctrl-C'd before any agent starts. Worth fixing eventually but ranked below the silent-data-loss and structural items. Keep on the radar.
- **Legacy log-layout removal** (`internal/web/handlers.go`, `cmd/inspect.go`, `cmd/pool_status.go`, `cmd/resume.go`, `internal/root/resolve.go:402`, code.structure MEDIUM): requires confirming no production project still carries pre-`.layout-v2` rows. The migration runs on every DB open, so most projects are already converted, but the cleanup has a user-data-dependency the supervisor cannot resolve from in-repo evidence. Defer until someone can verify or accept the "legacy rows return 404" worst case.
- **`cmd_cat` cache_status parameter unused** (`scripts/claude-usage.py:229`, code.recent LOW), **`--cache-ttl` lacks `h`** (line 112, code.recent LOW), **`parse_pct`/`parse_duration` error-style drift** (lines 104-118, code.recent LOW): genuine nits on a freshly-introduced script. Bundle into a single follow-up commit on the Python tool when its author touches it next; not worth a dedicated cycle.
- **`runner.Run` is 440 lines** (`internal/runner/runner.go:195-632`, code.structure MEDIUM): real readability cost, but the function is the most-edited code path in the project. Recent commits show active work elsewhere; refactoring it now risks merge conflicts with in-flight changes. Revisit after the A/B prompt experiment finishes and the structural splits in actions #7 and #8 have landed.
- **Cobra command boilerplate ritual** (code.structure MEDIUM, finding 6): worth doing, but only after `cmd/table.go` is split (action #7). The `setupRun` helper is easier to design once the runner/container builders live in their own files. Sequencing concern, not a value concern.
- **`cmd/agent_config.go` four-subcommands split** (code.structure LOW, finding 7): genuine readability win but low severity; pull into a future cycle.
- **`ClaudeAgent`/`CodexAgent` parallel boilerplate** (code.structure LOW, finding 8) and **`StreamFormatter`/`HTMLStreamFormatter` parallel dispatch** (code.structure LOW, finding 9): both correctly flagged as "defer" by the role itself. The cost of splitting later is the same as splitting today; only pay it when a third agent or third formatter lands.
- **A/B base prompt cleanup** (`internal/prompts/prompts.go:51,114-119`, code.recent + code.structure, time-bound TODO): intentional debt with a clear cleanup path. Do not touch until the A/B verdict is in — the comparison baseline depends on the prompts being identical apart from the two intentional deltas. Tracked by the in-source TODO; no supervisor action required.
- **Stale heading-style mismatch in `defaults/new_report_base_prompt.md`** (code.recent LOW): explicitly deferred by the commit that introduced the file to keep the A/B base narrow. Honor that.

### Conflicts

None. The three role reports cover non-overlapping concerns (latent bugs, recent-diff review, structural debt) and do not contradict each other. The structure report's call to defer `runner.Run` refactoring aligns with the bugs report's findings being concentrated elsewhere in the file (`promoteRuntimeFiles`, `reconcileErrorEvent`, `classifyFailure`) — those are addressable without restructuring the 440-line `Run`.

### Notes

- **Severity calibration looks honest**: every role flagged its own LOW findings as "defer" rather than padding the priority list. That makes the HIGH/MEDIUM ranks credible and supports promoting the secret-wipe and scanner findings to P0 without second-guessing.
- **Silent-failure cluster**: findings #1, #2, #3, #9, and the calldb scan in #10 are all the same anti-pattern — swallowing or discarding errors at I/O boundaries. After fixing the specific sites, consider an `errcheck`-style sweep (would complement `staticcheck` in action #6) to catch any others. This is exactly the kind of class-of-issue that benefits from a deterministic tool over recurring LLM audits.
- **Sequencing across the priority list**: #1–#4 are independent bug fixes and can land in any order. #5 (pricing extraction) is a prerequisite for #8 (web split) reading cleanly. #6 (adopt staticcheck) should precede #7/#8 so the splits land on a lint-clean baseline. #10 bundles three unrelated micro-fixes that don't conflict with anything.
- **Active-direction respect**: recent commits are concentrated in docs, the A/B prompt scaffold, and the `claude-usage.py` script. None of the priority actions above conflict with that work — the structural splits are mechanical and touch files unrelated to prompts/docs, and the bug fixes are localized.
- **No CI/CD recommendations**: per the local-first gating policy in CLAUDE.md, all gating goes into `make`-driven targets (action #6 uses this path). No GitHub Actions workflows proposed.
