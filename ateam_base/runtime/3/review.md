# Supervisor Review — 2026-05-13_20-35-34

## Project Assessment

The ateam codebase is in good overall shape: clear package boundaries, table-driven tests, and a consistent cobra-based CLI structure. The only role report this cycle (`refactor_small`) flags two real duplication hotspots — `internal/agent/{claude,codex}.go` and `internal/web/handlers.go` — plus a scattering of localized hygiene items (silently-discarded errors, thin wrappers, opaque helpers). None of the findings are urgent; the highest-leverage moves are extracting the shared agent base and consolidating the repeated handler patterns before more drift accumulates.

## Priority Actions

### 1. Extract shared base for ClaudeAgent / CodexAgent

- **Action**: Introduce an unexported `baseAgent` struct in `internal/agent/` holding the shared fields (`Command`, `Args`, `Model`, `Effort`, `MaxBudgetUSD`, `DefaultModel`, `Pricing`, `Env`) and the byte-identical methods (`ModelName`, `SetModel`, `SetEffort`, `SetMaxBudgetUSD`, `AgentEnv`, and the body of `CloneWithResolvedTemplates`). Embed it in both `ClaudeAgent` and `CodexAgent`. Run `make build` and `make test-docker` (agent-touching change).
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Six methods plus a clone helper are byte-for-byte identical across two files. Drift here would silently produce inconsistent agent behavior. This is also the natural prerequisite to Action #2 (the run() helper) — landing the shared struct first means the helper signature can reference the base directly.

### 2. Extract shared process-startup helper for agent run() methods

- **Action**: In `internal/agent/`, factor the ~35-line startup block (resolve executable, build `exec.Cmd` with `CmdFactory` fallback, set `WorkDir`/`Env`, attach `setupStreamFiles`, start process, emit initial `system` event with PID) shared by `claude.go:88-127` and `codex.go:78-113` into a helper such as `startAgentProcess(ctx, req, command, args, stdin io.Reader)`. Leave per-agent differences (Claude pipes prompt over stdin; Codex passes it as arg; per-line parser) at the call sites. Land after Action #1 so the helper can take the embedded base.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Largest single block of duplicated logic in the agent layer; the parts that vary are small and well-isolated, so the extraction is low-risk.

### 3. Add `respondIfNoDB` helper in web handlers

- **Action**: In `internal/web/handlers.go`, add a small helper like `func respondIfNoDB(w http.ResponseWriter, pe *projectEntry) bool` that writes the standard error response and returns true so callers can `if respondIfNoDB(w, pe) { return }`. Replace the 6+ occurrences of the `if db == nil && pe.dbErr != nil { ... } if db != nil { ... }` pattern (around lines 104-125, 408-420, 739-756, 1030). Normalize the status code and message format in the process.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Inconsistencies have already crept in across copies. Single helper kills the divergence and shrinks each handler by a few lines.

### 4. Centralize warning-log calls in web handlers

- **Action**: Add a package-private `logWarn(op string, err error)` (or `(s *Server).warn(...)`) in `internal/web/` and replace the 8 `log.Printf("warning: <op>: %v", err)` instances in `handlers.go` (lines 77, 116, 207, 237, 416, 748, 961, 1039). Land after Action #3 so both handler cleanups touch the same file in one pass.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Low-risk consolidation; keeps log format/routing in one place for future changes.

### 5. Stop discarding `runtime.Load` errors in cmd helpers

- **Action**: In `cmd/cat.go:117`, `cmd/code.go:407-409`, `cmd/env.go:96`, `cmd/table.go:1122`, replace `rtCfg, _ := runtime.Load(...)` (and the bare `return nil` in `code.go`) with proper error propagation or, at minimum, a surfaced diagnostic at the next user-visible point. For `code.go`, either return the error and let cobra format it or document precisely which downstream path is expected to re-raise the load failure.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Malformed `runtime.hcl` currently produces a confusing downstream failure with no diagnostic. Each call site is a one-line change; protects users from silent config errors.

### 6. Collapse ANSI color helpers in StreamFormatter

- **Action**: In `internal/runner/format_stream.go:359-406`, replace the six near-identical helpers (`dim`, `boldMagenta`, `cyan`, `boldCyan`, `yellow`, `boldGreen`, `red`) with a single `func (f *StreamFormatter) ansi(code, s string) string` and call sites like `f.ansi(ansiBoldMagenta, name)`. Keep the named escape constants.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Pure mechanical cleanup; minutes of work; tightens a duplicated pattern that will keep growing if new colors are added.

### 7. Surface swallowed JSON unmarshal errors in codex stream parser

- **Action**: In `internal/agent/codex.go` (lines 297, 320, 351, 368, 542, 551, 553), replace the seven `_ = json.Unmarshal(...)` calls with either a debug-gated log when a field that was present fails to parse, or an aggregated parse-error count surfaced on the final `result` event. Pick one approach and apply it uniformly.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Current behavior makes future Codex CLI stream-format changes invisible — there's no breadcrumb to debug from. Diagnostic-only change, no behavior risk.

### 8. Adopt golangci-lint with duplication/error checks

- **Action**: Add a minimal `.golangci.yml` at repo root enabling `dupl`, `gocognit`, `errcheck`, `unparam`, `unused`, `gosimple`. Wire `golangci-lint run` into a `make lint` target (local-only, no CI workflow — per project policy). Document the command in CLAUDE.md.
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Most LOW-severity items in this report (swallowed errors, thin wrappers, duplicated helpers) are mechanically detectable. One tool adoption replaces N future LLM review cycles — exactly the "deterministic tools over recurring LLM audits" pattern. Local-only invocation respects the documented no-CI policy.

### 9. Nits bundle (low-priority, decide-and-do-as-a-batch)

- **Action**: Group of small, localized cleanups; pick up opportunistically or as a single batch:
  - Move `firstNonEmpty` from `internal/agent/claude.go:285-293` into a shared `internal/agent/helpers.go`.
  - Delete (or alias) the thin `ExpandHome`/`Truncate`/`FormatDuration` wrappers in `internal/runner/runner.go:45, 888, 971` and call `display.*` directly.
  - Capture and skip-with-count the dropped `e.Info()` error in `internal/web/handlers.go:1333`.
  - Add a docstring to `codeSessionTimestamp` in `internal/web/handlers.go:1246-1267` describing the three-tier lookup and what the bool means.
  - Rename `relPath` in `cmd/table.go:55-64` to `relResolvedPath` (or add a one-line comment about EvalSymlinks).
  - Introduce a `hasContainerConfig` intermediate at `cmd/table.go:143`.
  - Make error-wrapping consistent across `cmd/cat.go:94-113` (use `%w` with the op-name prefix in all returns).
- **Source Role**: refactor_small @ 2026-05-13_20-31-48
- **Source Report**: .ateam/roles/refactor_small/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Each item is a true nit on its own. Bundled, they take an afternoon and clear the long tail.

## Deferred

- **CI/CD workflows for golangci-lint or any other check**: project policy is local-first gating with no CI/CD pipeline (per recent commit `aa3ed7e` and supervisor instructions). Action #8 is scoped to a local `make lint` target only; no GitHub Actions workflow, no `pull_request` triggers, no scheduled runs.
- **None of the refactor_small findings are deferred for tradeoff reasons**: all listed items are non-controversial cleanups. The report did not surface any items requiring product-feature judgment.

## Conflicts

None. Only one role report was produced this cycle.

## Notes

- Only `refactor_small` ran this cycle, so this review reflects a single perspective. If breadth matters next cycle, consider running the architecture, security, or test-coverage roles for cross-validation.
- The two duplication hotspots (`internal/agent/{claude,codex}.go` and `internal/web/handlers.go`) are exactly the kind of finding that will recur every cycle until structurally fixed — Actions #1–#4 are sequenced to land the structural change first so Action #9's small follow-ups inherit the cleanup rather than getting applied to soon-to-be-deleted code.
- Adopting `golangci-lint` (Action #8) is the highest compounding move in this set: it mechanizes the detection of most LOW-severity items in this report, so future refactor_small cycles can focus on architectural duplication instead of re-finding `_ = json.Unmarshal` calls.
- File-size hotspots (`internal/web/handlers.go` at 1433 lines, `cmd/table.go` at 1390, `internal/runner/runner.go` at 1281) are worth tracking but no role flagged a split as urgent. Defer any package-split work until a role specifically motivates it.
