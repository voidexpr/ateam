# Plan: `ateam run-many` command

## Context

`ateam run` executes a single agent with one prompt. There's no way to run multiple independent agents in parallel from the CLI. The `report` command does run agents in parallel, but it's tightly coupled to role-based report generation. We need a generic parallel runner — `ateam run-many` — that accepts N prompts and runs them concurrently, reusing the existing `RunPool` engine and progress display infrastructure.

The user wants this to be a unix-style building block: simple, composable, pipe-friendly. No orchestration agent, no auto-retry, no JSON spec — those can be added later if needed.

## Feature decisions

| Feature | Decision | Rationale |
|---------|----------|-----------|
| `--labels` | **Include** | Low cost, names tasks in progress display. Default: `task-1`, `task-2`, etc. |
| `--common-prompt-first/last FILE` | **Include** | Prepend/append shared context to each prompt. Simple `os.ReadFile` + concat. |
| `--max-parallel N` | **Include** | Already in `RunPool`. Default: 3. |
| `--no-progress` | **Include** | Suppress ANSI table for CI/non-TTY. |
| `--task-group` | **Include** | Already in `RunOpts`. Auto: `run-many-TIMESTAMP`. |
| Shared flags (`--profile`, `--agent`, `--model`, `--work-dir`, `--timeout`, `--verbose`, `--force`, `--docker-auto-setup`) | **Include** | Reuse existing flag helpers from `table.go` and `run.go`. |
| Manager agent | **Skip** | Adds costly LLM layer for pure task dispatch. Pipeline chaining is simpler. |
| Auto-diagnose / `--retry` | **Skip** | Hard to distinguish transient vs permanent failures. Additive later. |
| JSON spec input | **Skip** | Over-engineering for v1. Per-task overrides are rare. Additive later. |
| `--dry-run` | **Include** | Print assembled prompts without running (same pattern as `report --dry-run`). |

## Implementation

### Step 1: Extract shared pool status display from `report.go`

The ANSI progress table in `report.go` (~230 lines) is reusable. Extract into `cmd/pool_status.go`:

**Rename `reportStatusRow` → `poolStatusRow`** (and all associated functions/constants):
- `reportState*` → `poolState*`
- `reportStatusHeader` → `poolStatusHeader` (rename column `ROLE` → `LABEL`)
- `newReportStatusRows` → `newPoolStatusRows` — change signature to accept `[]string` (labels) instead of `[]PoolTask`
- `reportStatusLinesForWidth` → `poolStatusLinesForWidth`
- `reportStatusRowLines` → `poolStatusRowLines`
- `fitReportLine` → `fitPoolStatusLine`
- `reportStatusTerminal` → `poolStatusTerminal`
- `nextReportStatusRow` → `nextPoolStatusRow`
- `finalizedReportStatusRow` → `finalizedPoolStatusRow`
- `errorReportStatusRow` → `errorPoolStatusRow`
- `doneReportStatusRow` → `donePoolStatusRow`
- `reportStatusCost` → `poolStatusCost`
- `reportStatusTokens` → `poolStatusTokens`
- `writeReportStatusLines` → `writePoolStatusLines`
- `saveReportStatusAnchor` → `savePoolStatusAnchor`
- `redrawReportStatusLines` → `redrawPoolStatusLines`
- `printReportStatuses` → `printPoolStatuses`
- `printPlainReportStatuses` → `printPlainPoolStatuses`
- `reprintReportStatuses` → `reprintPoolStatuses`
- `cloneReportStatusRows` → `clonePoolStatusRows`
- `currentReportStatusLines` → `currentPoolStatusLines`
- `streamFilePrefix` — stays (generic helper)
- `formatRunningToolDetail` — stays (already generic)
- `visualRowsForLine`, `totalVisualRows` — stays

Also move to `cmd/pool_status.go`: the terminal files (`report_terminal_unix.go`, `report_terminal_windows.go`) keep their names but rename to `pool_terminal_unix.go` / `pool_terminal_windows.go` since they contain `stdoutWidth` and `subscribeWindowResize` which are generic.

**`report.go` changes**: Remove all extracted code. `runReport` calls the pool status functions with new names. The `doneReportStatusRow` wrapper passes `relPath(cwd, reportPath)` as the `Path` field — this stays inline in `report.go` as a 1-line call to `donePoolStatusRow`.

**Move tests**: `report_test.go` tests that test pool status functions move to `cmd/pool_status_test.go` with renamed types.

**Files**:
- Create: `cmd/pool_status.go` (~200 lines, moved from report.go)
- Create: `cmd/pool_status_test.go` (~80 lines, moved from report_test.go)
- Rename: `cmd/report_terminal_unix.go` → `cmd/pool_terminal_unix.go`
- Rename: `cmd/report_terminal_windows.go` → `cmd/pool_terminal_windows.go`
- Modify: `cmd/report.go` — remove extracted code, use new names

### Step 2: Create `cmd/run_many.go`

New cobra command:

```
Use:   "run-many PROMPT_OR_@FILE..."
Short: "Run multiple agents in parallel"
Args:  cobra.MinimumNArgs(1)
```

**`runRunMany` flow**:

1. Resolve each positional arg via `prompts.ResolveValue(arg)` (handles `@file`)
2. Read `--common-prompt-first` / `--common-prompt-last` files, prepend/append to each prompt
3. Assign labels: `--labels` if provided (validate count matches), else `task-1`, `task-2`, ...
4. Resolve env via `root.Lookup()` (project context optional, like `run.go`)
5. Build runner: `resolveRunner` (with project) or `resolveRunnerMinimal` (without), same as `run.go`
6. Apply `--model` override (same pattern as `run.go`)
7. Open CallDB via `openProjectDB(env)`
8. Generate task group: `run-many-TIMESTAMP` unless `--task-group` provided
9. Logs dir: `{projectDir|orgDir}/logs/run-many/{label}/` per task
10. Build `[]runner.PoolTask` — use label as `RoleID` for progress tracking (RoleID is just a string identifier, `run.go` already allows empty/arbitrary values)
11. If `--dry-run`: print each prompt labeled, return
12. If `--force` not set: `checkConcurrentRuns` for action `run-many`
13. Call `runner.RunPool` in goroutine with progress/completed channels
14. Progress display: ANSI table via `poolStatusRow` (TTY + no `--no-progress`), else `printProgress` from `run.go` (already handles multi-task via `[label]` prefix)
15. On completion: update status rows, collect results
16. Print summary: `N succeeded, M failed (duration)`
17. For failures: print stream tail (same as `report.go`)
18. Exit non-zero if any task failed

**Output strategy**: Task outputs go to stdout in submission order, each with a label header (`=== task-1 ===`). If only 1 task, skip headers. Progress/status to stderr.

**Flags**:
```go
--labels []string      // names for each task
--task-group string    // custom task group
--max-parallel int     // default 3
--no-progress bool     // suppress ANSI table
--common-prompt-first string  // file path
--common-prompt-last string   // file path
--profile string       // reuse addProfileFlags
--agent string         // reuse addProfileFlags
--model string         // model override
--work-dir string
--timeout int
--verbose bool         // reuse addVerboseFlag
--force bool           // reuse addForceFlag
--dry-run bool
--print bool           // print outputs to stdout (default false)
--docker-auto-setup bool  // reuse addDockerAutoSetupFlag
```

### Step 3: Register in `cmd/root.go`

Add `rootCmd.AddCommand(runManyCmd)` to `init()`.

### Step 4: Add `runner.ActionRunMany` constant

In `internal/runner/runner.go`, add `ActionRunMany = "run-many"` alongside existing `ActionReport`, `ActionRun`, etc.

## Key files

| File | Action |
|------|--------|
| `cmd/pool_status.go` | Create (extracted from report.go) |
| `cmd/pool_status_test.go` | Create (moved from report_test.go) |
| `cmd/pool_terminal_unix.go` | Rename from `report_terminal_unix.go` |
| `cmd/pool_terminal_windows.go` | Rename from `report_terminal_windows.go` |
| `cmd/report.go` | Modify — remove extracted code, use pool_status |
| `cmd/report_test.go` | Modify — remove moved tests |
| `cmd/run_many.go` | Create (~250 lines) |
| `cmd/root.go` | Modify — add `runManyCmd` |
| `internal/runner/runner.go` | Modify — add `ActionRunMany` constant |

## Reused functions (no changes needed)

- `runner.RunPool` (`internal/runner/pool.go`) — parallel execution engine
- `prompts.ResolveValue` (`internal/prompts/prompts.go`) — `@file` resolution
- `resolveRunner` / `resolveRunnerMinimal` (`cmd/table.go`) — runner construction
- `addProfileFlags` / `addVerboseFlag` / `addForceFlag` / `addDockerAutoSetupFlag` (`cmd/table.go`)
- `openProjectDB` (`cmd/table.go`) — DB setup
- `cmdContext` (`cmd/table.go`) — signal handling
- `checkConcurrentRuns` (`cmd/table.go`) — concurrent run guard
- `printProgress` (`cmd/run.go`) — simple line-based progress (for `--no-progress` fallback)
- `runner.StreamTailError` — error tail extraction
- `runner.FormatDuration` — duration formatting

## Discussion: `run-many` vs GNU parallel

### Why not just `parallel`?

You can already do this today:
```sh
parallel -j3 ateam run ::: "prompt1" "prompt2" "prompt3"
```

This works and gives you GNU parallel's full feature set (job slots, retries, `--halt`, `--joblog`, `--results`, etc.). So is `run-many` worth building?

**What `run-many` adds over `parallel ateam run`:**

| Capability | `parallel` | `run-many` |
|-----------|-----------|------------|
| Shared task group for cost tracking | Manual (`--task-group` per invocation, must coordinate) | Automatic — all tasks share one `run-many-TIMESTAMP` group |
| Live ANSI progress table | No — each process writes to its own stderr | Yes — unified table showing all tasks, tool calls, elapsed time |
| Shared runner/container setup | N separate processes, N container builds | One runner, one container build, N tasks |
| CallDB integration | N separate DB connections | One connection, proper grouping |
| `ateam cost` / `ateam serve` visibility | Tasks appear as unrelated runs | Tasks grouped under one task group, visible as a batch |
| Common prompt injection | Requires shell scripting | `--common-prompt-first/last` |
| Output collection | `--results` dir or manual | Ordered stdout with labels |

The strongest arguments for `run-many`:
1. **Task group integration** — the ateam cost/serve/inspect tools understand task groups. With `parallel`, you'd need to manually coordinate `--task-group` across N invocations and they'd still be separate DB rows with no batch identity.
2. **Single runner instance** — avoids N×container setup, N×DB connections, N×config resolution. Meaningful for Docker profiles.
3. **Progress display** — the ANSI table showing all tasks simultaneously is a much better UX than N interleaved stderr streams.

The argument against:
- It's ~250 lines of new code for something `parallel` mostly handles. If you don't care about task grouping or the progress table, `parallel` is fine.

### GNU parallel features worth considering (later)

| `parallel` feature | Worth copying? | Notes |
|-------------------|---------------|-------|
| `--halt now,fail=1` / `--halt soon,fail=30%` | **Maybe later** | Useful for fail-fast. Currently `run-many` waits for all tasks. Could add `--halt-on-failure` flag. |
| `--joblog FILE` | **No** | CallDB already tracks this better (cost, tokens, duration, streams). |
| `--retry N` | **Maybe later** | Already discussed as a deferred feature. |
| `--results DIR` | **No** | `RunOpts.LastMessageFilePath` already saves outputs per task. |
| `--progress` bar | **No** | The ANSI status table is superior for LLM tasks (shows tool calls, elapsed). |
| `:::+` (linked args) | **No** | Over-engineering. Per-task config should use JSON spec if ever needed. |
| `--pipe` / stdin distribution | **No** | Doesn't map well to agent prompts. |
| `--timeout` | **Already included** | Via `RunOpts.TimeoutMin`. |
| `--tag` (prefix output with args) | **Already included** | Via `--labels`. |

### Verdict

`run-many` is worth building. The integration with ateam's task group system, progress display, and shared runner setup is the value — not the parallelism itself. GNU parallel solves the process-level parallelism but can't provide the ateam-aware coordination. The implementation cost is low (~250 new lines, ~280 moved lines) because the infrastructure already exists.

## Verification

1. `make build` — compiles
2. `make test` — all tests pass (pool_status_test.go, report tests still work)
3. Manual: `ateam run-many "say hello" "say goodbye" --labels greet,farewell --dry-run`
4. Manual: `ateam run-many "say hello" "say goodbye" --max-parallel 1` — verify sequential execution, progress display, summary
5. Manual: `ateam run-many "say hello" --no-progress` — verify plain output
6. `ateam report` — verify still works identically after refactor
