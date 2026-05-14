# Add `work_dir` to agent_execs + pre-v1 layout migration

Companion to the broader Env/WorkDir refactor (`~/.claude/plans/when-running-ateam-outside-elegant-gizmo.md`). This file covers two coupled implementation pieces that ship together as the pre-v1 cleanup:

1. New `agent_execs.work_dir` column with backfill from `cmd.md`.
2. One-shot migration that splits `.ateam/roles/` into `.ateam/prompts/` (inputs) + `.ateam/artifacts/` (promoted outputs), and adds the new schema column.

Context, terminology (Env, AteamDir, WorkDir, GitRepoDir), and the rationale for splitting prompts from artifacts live in the broader plan. Read it first.

## Part A: `agent_execs.work_dir` column

Schema change in `internal/calldb/calldb.go`, same inline ALTER pattern as `output_file` / `peak_context_tokens` (`calldb.go:164-352`):

- Column: `work_dir TEXT NOT NULL DEFAULT ''`
- Index: `idx_execs_work_dir`
- `RecentFilter` gets a `WorkDir` option for query filtering.

### Storage rule

**Store the absolute WorkDir on every row.** No heuristics, no "default vs explicit" comparison. Storage cost is negligible (~100 bytes/row). Project moves make historical rows stale — true of any path field and the value the user genuinely wants preserved as "where this run actually happened."

Write path: insert in `internal/runner/runner.go` `Run()` at the same point that writes `output_file` / `git_start_hash`.

### Backfill (one-shot, at the v1 migration)

```
for row in agent_execs:
    cmd_md = read("<AteamDir>/logs/<id>/cmd.md")
    cwd = regex_match "* cwd: (.+)"     // cmd.md cwd is always absolute (runner.go:280)
    store cwd                            // or '' if cmd.md missing/unparseable
```

Reasoning for "always absolute, no empty-for-default rule": we considered making empty mean "ran at default location," but the heuristic is fuzzy (which "default" — `filepath.Dir(AteamDir)`? `GitRepoDir`? `os.Getwd()` at backfill time?) and the storage win is trivial. Picking a heuristic creates surprise behavior on project moves; always-absolute is predictable.

### Display

- `serve` run list: new "work_dir" column. Empty renders blank.
- `ateam ps --verbose`: include the column.
- Forensic detail page: full absolute path.

## Part B: One-shot pre-v1 migration

`internal/root/migrate.go` (new), invoked from `Env` resolution (in `internal/root/resolve.go` after AteamDir is known):

1. Sentinel: `<AteamDir>/.migrated-v1`. If present, skip.
2. `flock` the sentinel path so concurrent ateam invocations don't race.
3. Move files (`os.Rename`; atomic within the same filesystem):
   - `<AteamDir>/roles/<ROLE>/*_prompt.md` → `<AteamDir>/prompts/<ROLE>/`
   - `<AteamDir>/roles/<ROLE>/{report,review}.md` → `<AteamDir>/artifacts/<ROLE>/`
   - `<AteamDir>/supervisor/*_prompt.md` → `<AteamDir>/prompts/supervisor/`
   - `<AteamDir>/supervisor/{review,verify}.md` and `<AteamDir>/supervisor/code/` → `<AteamDir>/artifacts/supervisor/`
   - Remove now-empty `<AteamDir>/roles/` and old `<AteamDir>/supervisor/` directories.
4. DB: ALTER TABLE add `work_dir` + index, then run the Part A backfill (single transaction).
5. Config: remove `[git] repo` from `config.toml` if present (informational; not consulted at runtime in the new model, but the line lingers from old `init` runs).
6. Write the sentinel with timestamp and counts (`moved_files`, `backfilled_rows`).

### Post-v1 cleanup commit (separate, just before tagging v1)

- Delete `internal/root/migrate.go` and its call site in `resolve.go`.
- Drop the sentinel check.
- Drop any old-path fallback (none should exist if both parts are implemented correctly).
- Result: v1 ships with a single canonical layout, no migration code, no fallback.

### Test installs

Migrated on first ateam command after this lands:
- `~/.ateamorg/` (Nicolas's local org)
- `test/` fixtures in the repo
- Any local `.ateam/` directories in working projects

Document in the commit message that running any ateam command in an existing project will trigger the migration once.

## Files touched

| File | Change |
|---|---|
| `internal/calldb/calldb.go` | ALTER TABLE add `work_dir`; index; backfill helper; `RecentFilter.WorkDir`. |
| `internal/runner/runner.go` | Insert `work_dir` on `agent_execs` write. Use `effectiveWorkDir(opts)` (absolute). |
| `internal/root/migrate.go` (new) | Sentinel-gated, flock-protected one-shot migration: file moves + DB ALTER + backfill. |
| `internal/root/resolve.go` | Call `migrate.Run(ateamDir)` after AteamDir is resolved (returns nil if sentinel present). |
| `internal/web/handlers.go` | Display `work_dir` column in the run list. Optional `?work_dir=` filter. |
| `cmd/ps.go` | Show `work_dir` in `--verbose`. |
| Tests | `calldb_test.go` (backfill correctness), `migrate_test.go` (sentinel, flock, file moves, missing cmd.md), `runner_test.go` (new runs write the column). |

## Verification

- `make build && make test && make test-docker`.
- New tests:
  - `internal/calldb/calldb_test.go`: ALTER applies cleanly on a pre-populated DB; `RecentFilter.WorkDir` filters; backfill reads from cmd.md fixtures.
  - `internal/root/migrate_test.go`: fresh fixture with old layout → all files moved, sentinel written; second invocation no-ops; flock blocks concurrent migration in a goroutine race test; missing `cmd.md` files don't break backfill (just leave row's `work_dir` empty).
  - `internal/runner/runner_test.go`: a new run writes the absolute WorkDir into the row.
- Manual end-to-end on a real project:
  ```
  cd /Users/nicolas/SyncDatabox/nicmac/projects/ateam-small-fixes
  ateam ps --verbose                     # before: no work_dir column
  ateam exec "ping"                      # triggers migration on first run
  cat .ateam/.migrated-v1                # sentinel exists
  ls .ateam/prompts/ .ateam/artifacts/   # new layout
  ! ls .ateam/roles/                     # gone
  ateam ps --verbose                     # work_dir column populated
  ```

## Coordination with the broader plan

Part B's file moves are consumed by code that the broader plan also rewrites (`internal/prompts/prompts.go` `DiscoverReports`, `internal/runner/runner.go` `promoteRuntimeFiles`, `cmd/code.go` `env.ReviewPath()`). The migration moves the files; the broader refactor changes the reader/writer paths. **Both ship in the same commit (or sequenced commits in the same PR).** Otherwise an intermediate state would have files in `artifacts/` but consumers still reading from `roles/`.
