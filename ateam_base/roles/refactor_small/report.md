# Summary

The ateam codebase (a Go CLI orchestrating Claude/Codex agents) is generally well-organized with clear package boundaries, table-driven tests, and consistent use of `cobra` commands. The main quality issues are concentrated in two areas: (1) substantial duplication between `internal/agent/claude.go` and `internal/agent/codex.go` (interface-method bodies and the entire `run()` setup block are essentially copy-pasted) and (2) repeated patterns in `internal/web/handlers.go` (DB-error guard and warning-log calls) that have grown to 6–8+ instances. Beyond those two hotspots, the remaining findings are localized: a handful of silently-discarded errors, a few thin wrappers, and a couple of names that hide non-obvious behavior.

# Findings

## 1. Duplicate setter / accessor methods on ClaudeAgent vs CodexAgent

- **Location**: `internal/agent/claude.go:31-54` and `internal/agent/codex.go:32-55`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `Name()` aside, the methods `ModelName()`, `SetModel()`, `SetEffort()`, `SetMaxBudgetUSD()`, `AgentEnv()`, and the body of `CloneWithResolvedTemplates()` are byte-for-byte identical between the two agent types. Both also share the same struct fields (`Command`, `Args`, `Model`, `Effort`, `MaxBudgetUSD`, `DefaultModel`, `Pricing`, `Env`). Drift between the two will silently produce inconsistent agent behavior.
- **Recommendation**: Introduce an unexported `baseAgent` struct holding the shared fields and methods, then embed it in both `ClaudeAgent` and `CodexAgent`. `CloneWithResolvedTemplates` can share its body via a helper that takes the embedded base.

## 2. Duplicate process-startup block in agent run() methods

- **Location**: `internal/agent/claude.go:88-127` and `internal/agent/codex.go:78-113`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Both `run()` implementations share a 30-40-line block that resolves the executable name, builds the `exec.Cmd` (with `CmdFactory` fallback to `exec.CommandContext` + `configureProcessLifecycle`), sets `WorkDir`/`Env`, attaches `setupStreamFiles` outputs, starts the process, and emits the initial `system` event with the PID. The only meaningful difference is that Claude pipes `req.Prompt` over stdin while Codex passes it as a CLI arg.
- **Recommendation**: Extract a helper like `startAgentProcess(ctx, req, command, args, stdin io.Reader) (*exec.Cmd, *bufio.Scanner, *streamWriter, []io.Closer, error)`. Both agents would then differ only in (a) the args they construct and (b) the per-line parser they apply.

## 3. Swallowed JSON unmarshal errors in codex.go

- **Location**: `internal/agent/codex.go:297, 320, 351, 368, 542, 551, 553`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Seven `_ = json.Unmarshal(...)` calls silently discard parse errors. The current code falls back to empty-string checks, so a malformed event is treated identically to a structurally-different event. That makes future stream-format changes in the Codex CLI invisible — there will be no log to debug from.
- **Recommendation**: Either log at debug level (gated by an env var) when an Unmarshal fails on a field that *was* present, or aggregate a "parse-error count" into the result event so it surfaces at the end of a session.

## 4. Repeated DB-error guard in web handlers

- **Location**: `internal/web/handlers.go` — pattern repeats around lines 104-125, 408-420, 739-756, 1030, and elsewhere
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Each handler that touches the DB writes the same 3-line pattern: `if db == nil && pe.dbErr != nil { http.Error(...); return } if db != nil { ... }`. This appears 6+ times. A subtle inconsistency (different status codes, different message format) has already crept in.
- **Recommendation**: Add a small helper, e.g. `respondIfNoDB(w, pe) bool`, that handles the error response and returns true so the caller bails out. Same shape as `if respondIfNoDB(w, pe) { return }`.

## 5. Repeated `log.Printf("warning: <op>: %v", err)` calls

- **Location**: `internal/web/handlers.go:77, 116, 207, 237, 416, 748, 961, 1039` (8 instances)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The "warning:" log prefix is hand-copied in 8+ spots. Changing the log routing or format means hunting and replacing all of them; one is bound to be missed.
- **Recommendation**: A package-private `logWarn(op string, err error)` (or `func (s *Server) warn(...)`) keeps the format in one place.

## 6. Six near-identical ANSI color helpers on StreamFormatter

- **Location**: `internal/runner/format_stream.go:359-406`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `dim`, `boldMagenta`, `cyan`, `boldCyan`, `yellow`, `boldGreen`, `red` follow the identical shape: check `f.Color`, prepend an escape, append the reset. The escape codes are the only thing that varies.
- **Recommendation**: Replace with a single `func (f *StreamFormatter) ansi(code, s string) string` and call sites like `f.ansi(ansiBoldMagenta, name)`. Keep the named constants for readability.

## 7. `runtime.Load` errors silently discarded in helpers

- **Location**: `cmd/cat.go:117`, `cmd/code.go:407-409`, `cmd/env.go:96`, `cmd/table.go:1122`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Four call sites assign `rtCfg, _ := runtime.Load(...)` (or, in `code.go`, `if err != nil { return nil }` with a comment "let the runner resolution surface this error later"). A malformed `runtime.hcl` produces no diagnostic at these points — the user sees an unrelated downstream failure instead. The pattern is also inconsistent with the rest of the cmd package, which generally propagates load errors.
- **Recommendation**: In the three `_` cases, at minimum stash the error and surface it once at the next user-visible point. In `code.go:407`, document precisely which downstream path is expected to re-raise it (or just return the error and let cobra format it).

## 8. Misleading `relPath` helper resolves symlinks

- **Location**: `cmd/table.go:55-64`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `relPath(cwd, path)` calls `filepath.EvalSymlinks` before computing the relative path. Callers reading the name reasonably expect a pure path operation; the symlink resolution can produce surprising output when project roots are symlinked (a common dev setup on macOS).
- **Recommendation**: Either rename to `relResolvedPath` / add a one-line `// resolves symlinks first` comment, or split into two functions where the symlink-resolving variant is the explicit choice.

## 9. `firstNonEmpty` helper shared across files without a clear home

- **Location**: Defined in `internal/agent/claude.go:285-293`; used in `internal/agent/codex.go:198`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The helper is a generic string utility but lives at the bottom of `claude.go`. A reader looking at `codex.go` won't see it; same-package import works, but the placement implies ownership it doesn't have.
- **Recommendation**: Move to an `internal/agent/helpers.go` (or the existing shared file in this package). Same change can house the `setupStreamFiles`, `buildProcessEnv`, `errorEvent` helpers if they aren't already there.

## 10. Thin wrappers in `runner` that just delegate to `display`

- **Location**: `internal/runner/runner.go:45, 888, 971` — `ExpandHome`, `Truncate`, `FormatDuration`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Each wrapper is a one-liner that forwards to the `display` package. They add no behavior, no defaulting, no logging. They do, however, multiply the public surface of the `runner` package and obscure where the real implementation lives.
- **Recommendation**: Delete the wrappers and update call sites to use `display.ExpandHome` etc. directly. If callers exist outside `runner`, an alias `var ExpandHome = display.ExpandHome` is at least clearer than a function literal.

## 11. Swallowed `e.Info()` error in code-session directory walk

- **Location**: `internal/web/handlers.go:1333` (inside the `addFile` closure of `handleCodeSessionDetail`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `info, _ := e.Info()` silently drops the error; the following `if info != nil` masks the failure, so a file that becomes inaccessible mid-walk is just absent from the listing with no signal. Race or permission issues will manifest as confusing "empty" sessions.
- **Recommendation**: Capture the error, skip the entry, and accumulate a count of skipped entries to include in the response (or at least `logWarn`).

## 12. Opaque three-tier fallback without doc: `codeSessionTimestamp`

- **Location**: `internal/web/handlers.go:1246-1267`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The function returns `(time.Time, bool)` and tries three sources (DB row → parse from dir name → mtime). With no docstring, callers cannot tell which source they got, and the `bool` is undocumented.
- **Recommendation**: Add a one-line comment explaining the lookup order and what the boolean means. If callers need to distinguish sources, change the return to a small typed enum.

## 13. Complex condition mixing concerns in `cmd/table.go`

- **Location**: `cmd/table.go:143` — `if (cc != nil && cc.Type != "none") || runner.IsInContainer()`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The condition mixes "container is configured" with "we are already inside a container." Both meanings would benefit from named intermediates; future readers shouldn't have to dissect the boolean to know which branch they're on.
- **Recommendation**: `hasContainerConfig := cc != nil && cc.Type != "none"` then `if hasContainerConfig || runner.IsInContainer()`.

## 14. Inconsistent error wrapping inside `cmd/cat.go`

- **Location**: `cmd/cat.go:94-113`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Within ~20 lines: line 94 wraps with `fmt.Errorf("cannot find project: %w", err)`; lines 99 and 105 return `err` bare; line 110 wraps with a different prefix style. No consistent convention is followed and the bare returns lose user-facing context.
- **Recommendation**: Apply the same wrapping convention to all returns in this function (prefix with the operation name, use `%w`). This is local cleanup, not a project-wide style fight.

# Quick Wins

1. **Finding #1** — Introduce `baseAgent` and remove the duplicated setter/accessor methods from `ClaudeAgent`/`CodexAgent`. High value for prevention of drift; ~30 minutes.
2. **Finding #6** — Collapse the six ANSI color helpers in `format_stream.go` into one `ansi(code, s)` method. Pure mechanical cleanup; minutes.
3. **Finding #4** — Add a single `respondIfNoDB` helper in `internal/web/handlers.go`. Removes 6+ repetitions and the small inconsistencies already in place.
4. **Finding #5** — Centralize the `log.Printf("warning: %s: %v", ...)` calls in handlers.go behind one helper.
5. **Finding #7** — Stop discarding `runtime.Load` errors in the four cmd helpers. Each call site is a one-line change; protects users from silent config errors.

# Project Context

- **Language/build**: Go 1.26.3; `make build` / `make test` / `make test-docker` (per CLAUDE.md). 171 `.go` files; ~1.2 MB of source under `internal/`.
- **Entry points**: `main.go` → `cmd/root.go` (cobra). One `cmd/*.go` per subcommand (~30 files).
- **Hot files** (refactoring candidates by size):
  - `internal/web/handlers.go` (1433 lines) — biggest duplication target (DB-error guard, warning logs).
  - `cmd/table.go` (1390 lines) — runner setup utilities; mixes table rendering and DB plumbing.
  - `internal/runner/runner.go` (1281 lines) — core orchestration; mostly tight, has thin wrappers to `display`.
  - `cmd/agent_config.go` (805) and `internal/prompts/prompts.go` (731).
- **Agent layer** (`internal/agent/`): `claude.go`, `codex.go`, `claude_auth.go`, plus shared helpers (`firstNonEmpty`, `setupStreamFiles`, `buildProcessEnv`, `errorEvent`, `configureProcessLifecycle`, `resolveSlice`, `resolveStringMap`, `resolveConfigDir`). Heaviest duplication concentrated between `claude.go` and `codex.go`.
- **Stream parsing**: Centralized in `internal/streamutil` (`ParseClaudeLine`). Codex still does its own parsing in `internal/agent/codex.go`.
- **Web layer**: `internal/web/{server.go,handlers.go}` — HTTP handlers with per-project DB access via `internal/calldb`.
- **Tests**: `*_test.go` co-located; table-driven style; `make test` for unit, `make test-docker` for container/runner code.
- **No prior refactor-report exists** in `.ateam/runtime/2/` — this is the first run for this slot.
- **Suggested automation**: `golangci-lint` with `dupl`, `gocognit`, `errcheck`, `unparam`, `unused`, and `gosimple` enabled would catch most LOW-severity items above mechanically. A short `.golangci.yml` at repo root would let this audit run continuously.
