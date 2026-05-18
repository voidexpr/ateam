# Summary

ateam's package layout is sound: `cmd/` is a thin shell, `internal/{agent,runner,calldb,root,runtime,web,prompts,...}` each have clear, documented roles, and the concurrency contract for `Runner`/`RunPool` is captured in CONCURRENCY.md and enforced at the boundary. The architecture findings below are localized: project-layout knowledge has leaked from `internal/root` into two other packages, the web handlers package mixes HTTP transport with view-aggregation domain logic, and the `Agent` interface carries a "must call before sharing with pool" mutator contract that is documentation-only. No HIGH or CRITICAL issues; no service-boundary problems given the single-process CLI scope.

# Role performing the audit

- Role: `design.architecture` (dotted-prefix variant; legacy `refactor_architecture` exists in defaults but was not the role invoked here)
- Model: claude-opus-4-7 (no extended thinking)
- Approach: read-only static analysis, focused on placement / contract / boundary. Cross-checked against the existing `code.structure` report at `.ateam/runtime/3/report.md` so size-only findings (file LOC, long methods) were intentionally dropped — they are that role's territory, not this one's.
- Maturity calibration: pre-1.0 CLI, single-developer / small-team, active development. Greenfield-leaning, so MEDIUM findings are filed; backlog-y LOWs are kept at one entry.
- No prior `design.architecture` report was found under `.ateam/runtime/`, so this report stands alone.

# Findings

## 1. Project-layout knowledge is duplicated across three packages

- **Scope**: placement
- **Location**: `internal/root/resolve.go:31-74` (canonical helpers), `internal/runner/template.go:148-155` (`runtimeDirFor`/`logsDirFor`), `internal/web/server.go:64-72` + `internal/web/handlers.go:80,88,95,241,803,1050,1062,1079,1177,1179,1190` and several more `filepath.Join(pe.ProjectDir, "supervisor"|"roles"|"runtime"|"code", ...)` call sites
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `ResolvedEnv` already exposes `RoleDir`, `SupervisorDir`, `RoleHistoryDir`, `ReviewPath`, `ReviewHistoryDir`, `VerifyPath`, `LogsDir(execID)`, `RuntimeDir(execID)`, and `ProjectDBPath`. But:
  - `internal/runner/template.go` re-implements `runtimeDirFor` and `logsDirFor` — explicitly noted in a comment to avoid pulling in `root` ("Lives here ... so the runner can build paths from just ProjectDir without taking a dependency on the root package").
  - The web package partly re-implements the layout on `ProjectEntry` (`SupervisorPath`, `SupervisorHistoryDir`) but the handlers then use raw `filepath.Join(pe.ProjectDir, "supervisor", "code", latest, "execution_report.md")` and friends in roughly 20 places, plus a hardcoded `runtime`, `roles`, `code`, and `history` literals embedded in `codeSessionDirs`, `scanCodeSessions`, and `serveHistoryFile`.
  - Adding a new top-level subdirectory or renaming one means hunting through three packages and several test files.
- **Target state**: a single `internal/layout` (or `internal/projectfs`) package whose API takes only `projectDir string` (and `execID int64` where relevant) and returns paths. `ResolvedEnv` keeps its convenience methods but delegates to `layout.*`. The runner replaces its private helpers with the shared package (it already takes a `ProjectDir string` field, so the dependency direction works). `ProjectEntry` likewise delegates and grows the missing helpers (`RoleHistoryDir`, `CodeSessionDirs`, etc.) instead of inlining them.
- **Migration cost**: pure refactor inside the binary — no schema, no API, no on-disk layout change. The only real friction is breaking the runner's "no dependency on root" rule, which is solved by extracting `layout` *out* of root rather than inside it (root then depends on layout, runner depends on layout, web depends on layout — no cycles).
- **Rationale**: this is the standard "validation duplicated across layers" smell. Each new directory has historically been added in three places (the legacy → new logs migration in `internal/root/migrate_logs.go` plus the dual `legacy`/new branches in `handlers.go` are the most visible cost). One canonical owner closes that pattern.

## 2. Web `handlers.go` mixes HTTP transport with view-aggregation domain logic

- **Scope**: placement
- **Location**: `internal/web/handlers.go` — specifically `enrichRuns` (188), `resolveRunFiles` (150), `buildSessions` (958), `scanCodeSessions` (1189), `buildCodeSessionEntry` (1216), `codeSessionTimestamp` (1249), `latestCodeSession` (1280), `historyAction`/`navKind`/`supervisorLabel` (895-948), `resolvePromptFile`/`resolveOutputFile`/`resolveHistoryFile`/`promptDir` (659-731). Companion file `internal/web/history.go` already extracts some of this cleanly.
- **Severity**: MEDIUM
- **Effort**: LARGE
- **Description**: The handler functions themselves are short and HTTP-shaped, but the file leans heavily on co-located free functions that walk the filesystem, query the DB, parse batch names, and reconcile two coexisting layout schemes. None of that needs an `http.ResponseWriter` — `internal/web/export.go` already proves the same data is consumable outside the request lifecycle. Cost of the current placement:
  - The "code sessions" model (legacy timestamp dirs vs exec-id dirs, which directory the prompts live in, where the report lives) is encoded as flat helpers in handlers.go and re-encoded in `internal/web/code_sessions_test.go` fixtures rather than as a session abstraction.
  - The same merging pattern (DB-sourced + filesystem-scanned, deduped by exec_id) is repeated for reports, reviews, verify, and code sessions — currently with the merge logic in `history.go` but the wiring inlined in three different handler bodies (228, 294, 807).
  - Anything that wants to add a JSON endpoint for `ateam serve` must duplicate this aggregation.
- **Target state**: pull the aggregation into a small `internal/sessions` (or `internal/view`) package: `Session`, `Run`, `History` types with constructors that take `(projectDir, calldb)` and return ready-to-render data. Handlers shrink to `requireProject → call view → render template`. `export.go` consumes the same view layer instead of walking the filesystem itself.
- **Migration cost**: medium-large. The pattern is already partly there (history.go), so this is more "pull existing helpers into a sibling package and let handlers thin out" than a new abstraction. Test files move with their helpers. No external surface change.
- **Rationale**: handlers.go is 1433 LOC and was already flagged for size in the prior `code.structure` report. From the architecture lens the deeper issue is *layer*: half the file is domain (data assembly, layout reconciliation), half is transport. Splitting them removes the "must serve HTTP to test" friction and makes a future JSON API a one-handler change instead of a dual implementation.

## 3. `Agent` interface contract relies on temporal coupling for parallel safety

- **Scope**: contract
- **Location**: `internal/agent/agent.go:39-51` (interface), `internal/agent/claude.go:40-44` / `internal/agent/codex.go:41-45` / `internal/agent/mock.go:83-85` (impls), call sites at `cmd/table.go:764,772,829,859` and `cmd/eval.go:441`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The `Agent` interface declares `SetModel`, `SetEffort`, and `SetMaxBudgetUSD`, each documented "MUTATES — call before the Agent is shared with a pool." Today the convention holds: every call site is in `cmd/` before `runner.RunPool` dispatches. But the runner already has machinery for per-run isolation — `CloneWithResolvedTemplates` returns a clone with separate slice/map backing memory — and the per-run model/effort/budget *aren't* part of that clone. They sit on the shared `Agent` value the pool reads. A new contributor wiring "set model from review-derived task metadata" inside `Runner.Run` (or in a `PreDispatch` hook) would silently race the pool. The contract is enforced by godoc text and reviewer attention, not by types.
- **Target state**: model / effort / max-budget become fields on `agent.Request` (or, equivalently, on `runner.RunOpts` since the runner owns request construction at `runner.go:653`). The `Agent` interface loses its three setters and becomes read-only after construction. `cmd/table.go:applyModel/applyEffort/applyMaxBudgetUSD` write into `RunOpts` instead.
- **Migration cost**: small. Five call sites in `cmd/`, one per-impl removal in `agent/{claude,codex,mock}.go`, runner reads the new `RunOpts` fields when building the agent argv (the existing `claudeArgs`/`codexFlagArgs` helpers at `agent.go:180-208` already parameterize on these strings). No DB or API change. The temporal-coupling rule disappears with the setters.
- **Rationale**: a runtime-mutable interface that is "safe only if you call it during construction" is the kind of contract that bit production previously (CONCURRENCY.md was written after two SIGSEGVs from exactly this class of shared-mutable-state bug). The remaining mutators stand out in an otherwise clone-or-immutable codebase.

## 4. Legacy log-layout branching has no deprecation horizon

- **Scope**: placement
- **Location**: `internal/web/handlers.go:150-184,510-558,895-948,1141-1271`, `internal/runner/runner.go` (relStream resolution), `internal/root/resolve.go:381-412` (`ResolveStreamPath`, `IsLegacyStreamFile`), `internal/root/migrate_logs.go` (one-shot migrator)
- **Severity**: LOW
- **Effort**: SMALL (delete) or MEDIUM (formal deprecation plan)
- **Description**: `MigrateLogsLayout` runs sentinel-guarded on project open and lifts every legacy `<TS>_<ACTION>_*.jsonl` row into `logs/<exec_id>/`. After it runs successfully the legacy paths cannot be produced by ateam anymore. But every consumer still branches on `root.IsLegacyStreamFile(absStream)` and carries a parallel code path (file-name suffixing, timestamp-±60s prompt matching, dual `runFiles` resolution, dual session-dir scan in `buildCodeSessionEntry`). The branching has tests, named helpers, and section comments — i.e. it has settled in as permanent surface.
- **Target state**: pick a stance and document it in CONFIG.md or APPROACH.md.
  - Option A (recommended): declare a layout-v2 cutoff version, delete the `IsLegacyStreamFile` branches, and have `MigrateLogsLayout` either fully convert or refuse to open the project. Pre-1.0 is the cheap window.
  - Option B: keep both, but document that the legacy branch is permanent fallback (and add a single architectural-decision note).
- **Migration cost**: option A is largely a delete-and-test pass — fewer branches in handlers.go, less code in runner output resolution. Option B is just docs. Neither is structural.
- **Rationale**: kept LOW because both paths work today. The architectural concern is that the *transition state has become indistinguishable from the steady state*, which is exactly the place where future contributors add a third path "to handle the unusual case" instead of collapsing the two.

## 5. `batch` column carries session kind via filename-style prefix

- **Scope**: contract
- **Location**: `internal/calldb/calldb.go:36-66` (schema, `batch` column), `internal/web/handlers.go:969-972` (kind derivation: `if strings.HasPrefix(row.Batch, "code-")`), `internal/web/handlers.go:1127-1138` (timestamp parse from same field)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `agent_execs.batch` is a free-form string, but its value is structured: `"<kind>-<timestamp>"`. Two consumers in handlers.go take it apart with `strings.HasPrefix` and `strings.IndexByte('-')`. The kind ("code"/"report") is the entire basis of the `Kind` field on `CodeSession` — i.e. a semantically important property is encoded as a string-prefix convention.
- **Target state**: either (a) add a small parser (`calldb.ParseBatch(batch) (kind, ts)`) so every consumer goes through one place, or (b) split into `batch_kind` + `batch_id` columns. Given `batch` already participates in an index and is a public-ish identifier, (a) is the cheaper move.
- **Migration cost**: tiny — single helper in `calldb`, two replacements in `handlers.go`. (b) is a schema migration; not justified at current scope.
- **Rationale**: kept LOW because there are only two prefixes and one consumer file. Filed mostly as a marker so a third batch kind doesn't get added in a third file.

# Quick Wins

1. **Finding 5** — extract a `calldb.ParseBatch` helper. ~15 LOC, removes the only HasPrefix/IndexByte string-shape parsing of `batch` from a non-DB package.
2. **Finding 3** — move `model` / `effort` / `max_budget_usd` from `Agent` setters to `agent.Request` (or `RunOpts`). 5 call sites in `cmd/`, 3 impl methods removed, 1 interface tightened. Closes a documented "temporal-coupling required for safety" surface that no longer needs to exist.
3. **Finding 1** — extract `internal/layout` from `internal/root` and have `runner/template.go` and `web/{server,handlers}.go` delegate to it. The runner's "no dependency on root" carve-out goes away because layout becomes a leaf package.

# Project Context

- **Stack**: single Go binary (`go.mod` declares 1.25+), Cobra CLI in `cmd/`, internal packages under `internal/`, embedded HCL/Markdown defaults in `defaults/`, sqlite via `modernc.org/sqlite`, HCL2 for runtime config, TOML for project config.
- **Architecture lens — key files / packages**:
  - `internal/root/resolve.go` — `ResolvedEnv` is the project/org context object, also owns the layout helpers (RoleDir, LogsDir, RuntimeDir, etc.).
  - `internal/runner/{runner.go,pool.go,template.go,events.go}` — orchestration and the parallel pool. `Runner.Run` (runner.go:195-632) is the central state machine; `RunPool` (pool.go) is the parallel boundary with documented channel contracts.
  - `internal/agent/{agent.go,claude.go,codex.go,mock.go}` — `Agent` interface + 3 backends. Stream events are normalized in `internal/streamutil`.
  - `internal/calldb/{calldb.go,queries.go}` — single-table state DB (`agent_execs`), with `batch` column tying related execs.
  - `internal/web/{server.go,handlers.go,history.go,export.go,markdown.go}` — `ateam serve` UI; goldmark-based markdown rendering.
  - `internal/runtime/config.go` — 4-level HCL inheritance (embedded → org defaults → org → project).
  - `internal/container/{container.go,docker.go,docker_exec.go,prepare_guard.go}` — three execution modes (none / docker / docker-exec) behind one `Container` interface.
  - `internal/secret/{store.go,resolve.go,validate.go}` — secret store + resolution.
  - `cmd/table.go` (1390 LOC) — CLI utility hub: runner construction (newRunner, resolveRunner, buildAgent, buildContainer), DB open, profile resolution, model/budget appliers.
- **Dual-layout state**: `MigrateLogsLayout` (`internal/root/migrate_logs.go`) is sentinel-guarded one-shot. Legacy branching lives in handlers.go and `root.ResolveStreamPath`/`IsLegacyStreamFile`.
- **Concurrency contract**: codified in `CONCURRENCY.md`. The runner is read-only after dispatch; per-call clones happen at the top of `Runner.Run` via `Container.Clone()` and `Agent.CloneWithResolvedTemplates()`. The `Agent` setters (Finding 3) are the one part of that contract that lives in godoc rather than types.
- **Maturity signals**: clean working tree, small recent commits (docs + python script tweaks + prompt tuning), no production-user discipline indicators in CLAUDE.md, multiple in-flight role-naming variants (dotted vs underscore) suggesting internals are still in flux. Architecture changes are low-risk.
- **Out of scope for this role** (handled elsewhere or already filed): file size of `cmd/table.go` / `internal/web/handlers.go` / `internal/runner/runner.go` (`code.structure` report at `.ateam/runtime/3/report.md`); secret-store data-loss, stream scanner silent failures (`code.bugs` report at `.ateam/runtime/1/report.md`); python `claude-usage.py` semantics (`code.recent` report at `.ateam/runtime/2/report.md`).
