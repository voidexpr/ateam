# Summary

The codebase is well-structured with thoughtful concurrency primitives (PrepareGuard, RunPool channel contracts) and documented assumptions. Most error handling is conservative. I found one HIGH-severity data-loss path in the secret store, plus a cluster of silent-failure bugs in stream scanners and minor lifecycle issues. No CRITICAL findings.

# Role performing the audit

- Role: code.bugs
- Model: claude-opus-4-7 (no extended thinking)
- Approach: read-only static analysis with targeted Explore subagent passes; every finding re-verified by reading the cited file directly before being included.

# Findings

## 1. FileStore.Set silently wipes all existing secrets on transient read error

- **Location**: `internal/secret/store.go:84`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: `lines, _ := readLines(s.Path)` discards the error. `readLines` returns an error from `os.Open` for any failure other than `os.IsNotExist` — examples: file exists but mode 0000 after a fat-finger chmod, transient EIO from the underlying disk, file replaced by a directory by an external tool, or any platform-specific `EACCES`/`ENFILE`. In every one of those cases `lines` is `nil`. The function then appends the new `name=value` entry to `nil`, takes the success branch at line 97 (`os.WriteFile`) and rewrites the file with **only the single new entry** — every previously stored secret is lost. The file is then writable (0600) so the next run sees a degenerate one-line file.
  Trigger: any `FileStore.Set` call when `s.Path` exists but `os.Open` returns a non-`IsNotExist` error.
- **Recommendation**: distinguish `os.IsNotExist` from other errors. Return the error to the caller (or refuse to overwrite) when `readLines` fails for any other reason.

## 2. Stream scanners never check `scanner.Err()` — oversized JSON lines silently truncate runs

- **Location**: `internal/agent/claude.go:155` (loop) and lines 131-132 (buffer); same pattern in `internal/agent/codex.go:117-124`; also `internal/runner/events.go:62-77` and `internal/runner/format.go:63-83`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: All four sites configure `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` and then iterate with `for scanner.Scan() { ... }` without a follow-up `if err := scanner.Err()`. `bufio.Scanner` returns `false` from `Scan()` on `bufio.ErrTooLong` (line exceeds the 1MB cap) without propagating the error anywhere — the loop exits silently. Claude streams routinely include large tool-result blocks (e.g. a `Read` of a big file, base64 image content, MCP tool dumps); a single JSONL line above 1MB happens in real runs. When the scanner terminates early in `claude.go`/`codex.go`, the channel closes after `cmd.Wait`, the run is recorded as "no result event" (`classifyFailure` in `runner.go:923-924`) when the agent actually completed successfully but with one fat line. In `events.go` (`scanStreamFileForResult`) and `format.go` the same truncation silently drops the very `result` line we are trying to recover.
  Trigger: any agent that emits a stream-JSONL line larger than 1MB. Reproducible with a `Read` of a moderately large file inside the agent, or a long `text` block from a verbose model.
- **Recommendation**: after each loop, check `scanner.Err()` and either surface it as an error event (in agents) or log/return it (in the recovery readers). Consider raising the per-line cap to match `claude`'s actual output ceiling.

## 3. byteCopy leaves a truncated/corrupt destination on copy failure

- **Location**: `internal/fsclone/clone.go:73-76`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, ...)` on line 69 truncates `dst` to 0 immediately. If `io.Copy` fails midway (read error on `src`, ENOSPC on `dst`, signal, etc.), the function calls `out.Close()` and returns the error but leaves a half-written `dst` on disk. The caller in `runner.promoteRuntimeFiles` (`internal/runner/runner.go:1177-1187`) logs the error and continues; nothing removes the corrupt file. Subsequent reads of the canonical destination read a torn file — particularly damaging because `Clone` also `os.Remove(dst)` first (line 30), so the *previous good copy was already deleted* before the failed copy started.
  Trigger: `io.Copy` failure during `fsclone.Clone` in the post-run promote step (e.g. disk fills, agent killed mid-write to source, network FS).
- **Recommendation**: on copy failure, `os.Remove(dst)` (best-effort) before returning the error so the canonical path is either correct or absent. Alternatively, write to `dst+".tmp"` then `os.Rename` on success.

## 4. GetProjectMeta hides `git status` failure

- **Location**: `internal/gitutil/gitutil.go:46-49`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: When `git status --porcelain` fails, the function returns `(meta, nil)` with `meta.Uncommitted == nil`. Callers that branch on `Uncommitted` (e.g. to refuse to start a run on a dirty tree, or to record the dirty state on `agent_execs`) cannot distinguish "tree is clean" from "git failed". A `git status` failure typically means git is unhealthy in a way that should surface (corrupt index, locked `.git/index.lock` from an aborted `git add`, permission change on `.git`); silently treating it as a clean tree masks the problem.
  Trigger: any condition that makes `git status` fail after `git log` succeeded (most commonly `index.lock` held by a parallel git process, or `.git` ownership mismatch).
- **Recommendation**: return the error from this branch, or at minimum mark the failure in the returned struct (e.g. a `StatusErr` field).

## 5. PrepareGuard caches a context-cancellation error across unrelated callers

- **Location**: `internal/container/prepare_guard.go:17-20`; consumed in `internal/container/docker_exec.go:255-271`
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: `PrepareGuard.Do` runs `fn` under `sync.Once` and **caches whatever error the first call produces forever**. If the first `Prepare()` call cancels mid-`EnsureBinary` (ctx deadline, SIGINT during initial `docker cp`), `p.err` holds the context error for the lifetime of the shared container. Every subsequent `Prepare()` call — including those from later runs with a fresh, healthy ctx — returns the cached error without rerunning. In a `ateam parallel` invocation where the first worker's setup is interrupted, none of the surviving workers can recover. The `KeyedPrepareGuard` below has the same issue per key.
  Trigger: cancel a pool during its first container prepare (Ctrl-C before the first agent prints anything). The pool's other clones inherit the cached error rather than retrying.
- **Recommendation**: clear `p.err` (or invalidate the `once`) when the error is `context.Canceled` / `context.DeadlineExceeded`. A typed sentinel for "retryable cancellation" or a `sync.Mutex`+`done bool` pattern that allows reset on cancellation would work.

## 6. Pool dispatch loop uses `continue` instead of `break` on ctx cancellation

- **Location**: `internal/runner/pool.go:65-71`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: When `ctx.Done()` fires while waiting for a semaphore slot, the code calls `wg.Done()` and `continue`s. The next loop iteration calls `opts.PreDispatch()` (a budget check for `--max-budget-usd-batch`) again and then immediately re-enters the `select` that picks `ctx.Done` again. With N remaining tasks, PreDispatch runs N times and N "Pool: stopping dispatch" messages may print to stderr if PreDispatch then errors. The functional outcome is correct (no goroutines start) but PreDispatch should not run for tasks that will never dispatch — and the budget-check helper could conceivably have side effects (DB read, log).
  Trigger: cancel a pool while tasks are still queued.
- **Recommendation**: replace `continue` with `break` on the `ctx.Done()` branch.

## 7. `looksLikeSecret`-style heuristic may scan `bool` poorly in calldb

- **Location**: `internal/calldb/calldb.go:147-148`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE …").Scan(&hasOldTable)` scans an SQLite `INTEGER` (driver returns `int64`) into a `*bool`. Go's `database/sql` only reliably converts `bool`-shaped sources (`true`/`false`/`"0"`/`"1"`) into `*bool`; an `int64(0)`/`int64(1)` source falls into `driver.Bool.ConvertValue` which returns an error. Both errors are swallowed by `_ =`, so `hasOldTable` and `hasNewTable` stay `false`, and the function tries to apply the full `schema` against a database that may already contain `agent_execs` with `task_group`. The CREATE TABLE IF NOT EXISTS is a no-op (so this masks itself today), but the subsequent `CREATE INDEX IF NOT EXISTS idx_execs_batch ON agent_execs(batch)` would fail because `batch` doesn't exist yet — exactly the case the comment on lines 141-145 warns about. The bug is currently latent because modernc.org/sqlite happens to convert COUNT() results to `int64`-as-string in some paths, but the safe form is `Scan(&intVal); existed := intVal > 0`.
  Trigger: opening a DB created by an older `ateam` that still uses the `agent_execs(task_group)` shape; depends on the driver's row materialization.
- **Recommendation**: scan into `var n int; if err := ... Scan(&n); ...; hasOldTable = n > 0`. Stop swallowing the scan error — log it.

## 8. cmd.Wait error path emits an `error` event even when a `result` was already sent

- **Location**: `internal/agent/claude.go:261-282` (and the analogous block in `codex.go`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The comment at line 269 says "Only emit error if we haven't sent a result event already" but no such check exists — every non-zero `cmd.Wait` sends an `error` event. The runner papers over this with `reconcileErrorEvent` (`runner.go:640-649`), which keeps the prior result if its type is `"result"`. Net effect today: correct. But the comment is a lie that will mislead someone into changing the reconcile logic and breaking this contract, or into trusting the comment when adding a new agent. Worth recording as a real-but-mild bug because future-tense correctness depends on reconcile and reconcile depends on `prev.Type == "result"`; a code path that flips that ordering would suddenly classify successful runs as errors.
  Trigger: any agent that exits non-zero after sending a `result` event (Claude does this when it returns its own `is_error: true`).
- **Recommendation**: either implement the check the comment promises (skip the error event when `resultEv != nil && resultEv.Type == "result"`) or rewrite the comment to describe the reconcile-driven design.

# Quick Wins

1. **#1 — Stop wiping the secret file on transient read errors** (`store.go:84`). SMALL effort, HIGH severity, single-file fix.
2. **#2 — Check `scanner.Err()` in the four scan loops** (`claude.go`, `codex.go`, `events.go`, `format.go`). SMALL effort, prevents silent run truncation on long JSON lines.
3. **#3 — Clean up `dst` on `byteCopy` failure** (`clone.go:73-76`). SMALL effort, prevents corrupt canonical output.
4. **#6 — `break` instead of `continue` on `ctx.Done`** (`pool.go:70`). One-line change.
5. **#4 — Surface `git status` failure** (`gitutil.go:46-49`). One-liner; removes a silent failure mode that hides git index corruption.

# Project Context

- **Language**: Go 1.26 (per `go.mod`). 171 .go files, ~44k LOC. Cobra CLI in `cmd/`, business logic in `internal/`.
- **Key risky surfaces**:
  - `internal/runner/runner.go` — orchestrates agent execs, stream consumption, finalize/promote/DB update.
  - `internal/runner/pool.go` — parallel dispatch with `PreDispatch` budget hook.
  - `internal/agent/claude.go`, `codex.go` — JSONL stream parsing from subprocess stdout; `configureProcessLifecycle` (in `cancel.go`) sets `Setpgid` + `WaitDelay=30s` and a `Cancel` hook that SIGTERMs the process group.
  - `internal/container/docker_exec.go` + `prepare_guard.go` — sync.Once-guarded container prep across pool clones.
  - `internal/calldb/calldb.go` — SQLite-backed exec tracking; `SetMaxOpenConns(1)` serializes writers; WAL journal; migration in `migrate()`.
  - `internal/secret/store.go` — file (.env) + OS keychain backends.
  - `internal/fsclone/clone.go` — CoW clone (Darwin `cp -pc`, Linux `cp --reflink=auto`) with byte-copy fallback.
  - `internal/gitutil/gitutil.go` — `git log`/`git status`/`git rev-parse HEAD` wrappers.
- **Concurrency model**: documented in `CONCURRENCY.md`. Runner fields read-only after construction; `Agent` and `Container` cloned per exec; `*sql.DB` shared via single writer connection.
- **Conventions worth remembering**:
  - All stream scanners use `64KB/1MB` buffer pairs.
  - Canonical paths derived from `exec_id` returned by `InsertCall`; `runtime/<exec_id>/` is agent-writable, `logs/<exec_id>/` is forensic.
  - `classifyFailure` (`runner.go:902`) maps `ctx.Err()` + `resultEv` to `ErrorSource*` constants.
  - `cmd.md` redaction via `looksLikeSecret` substring match (KEY/TOKEN/SECRET/etc.).
- **No prior report** present at `.ateam/runtime/1/report.md` (this is the first run).
