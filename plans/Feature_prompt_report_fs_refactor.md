# Feature: Prompt & artifact filesystem refactor

## Context

Before v1, the `.ateam/` directory layout needs a cleanup. Today, prompts (configuration) and generated outputs (artifacts) are entangled under the same trees, and the codebase carries two parallel abstractions (`roles/<NAME>/...` vs `supervisor/...`) that complicate prompt resolution, role discovery, and output promotion. `internal/runner/runner.go:1156` already has a TODO acknowledging the split is overdue: "get rid of this exclusion once configured prompts are kept separate from files."

Goals:
1. **Split prompts from generated outputs** — `prompts/` for configuration, `shared/` for generated artifacts.
2. **Flat, action-first prompt layout** — supervisor concept dissolves into singleton actions; no more `roles/` vs `supervisor/` asymmetry.
3. **Uniform recursive prompt-assembly mechanism** — replaces `_base_prompt.md`, `_extra_prompt.md`, and the special-case supervisor pipeline with one rule that works for any prompt at any depth.
4. **Structural naming safety** — a user-defined role named `review` cannot collide with the singleton review action; they live in different namespaces.

## Target layout

```
.ateam/
  config.toml
  state.sqlite
  secrets.env
  logs/
  prompts/
    report.prompt.pre.md           # prepended to every prompt in report/
    report.prompt.post.md          # appended to every prompt in report/
    report.prompt.md               # (optional) base appended to the pre-chain for everything in report/
    report/
      <role>.prompt.md             # per-role report prompt
      <role>.prompt.pre.md         # per-role pre (optional)
      <role>.prompt.post.md        # per-role post (optional)
    code.prompt.pre.md
    code.prompt.post.md
    code/
      <role>.prompt.md
      <role>.prompt.pre.md
      <role>.prompt.post.md
    review.prompt.md               # singleton actions live at top level
    review.prompt.pre.md           # (optional)
    review.prompt.post.md          # (optional)
    code_management.prompt.md
    code_verify.prompt.md
    auto_setup.prompt.md
    exec_debug.prompt.md
    report_commissioning.prompt.md
  shared/
    reports/
      <role>.md
    review.md
    verify.md
    setup_overview.md
    history/                       # parallel tree; reserved for future archival
      reports/<role>/<timestamp>.md
      review/<timestamp>.md
```

The same restructuring applies in parallel to `.ateamorg/`, `.ateamorg/defaults/`, and the embedded `defaults/` tree.

## How a role is identified

A role exists iff `prompts/report/<role>.prompt.md` exists somewhere in the resolution chain (embedded → org defaults → org → project). Singleton actions live at the top of `prompts/` and are never scanned as roles. A user-defined role named `review` would live at `prompts/report/review.prompt.md`; the singleton review action lives at `prompts/review.prompt.md`. Different namespaces — collision is structurally impossible.

## Generic prompt-assembly mechanism

The same rule works for every prompt, at any depth, in both the singleton and role-distributed cases.

### Definitions

For any logical prompt path `P` (e.g. `review` or `report/security`):

- **Main**: the file `<P>.prompt.md` — resolved with **fallback** semantics (most-specific level wins: project → org → org-defaults → embedded).
- **Pre**: every `<P>.prompt.pre.md` across all levels — resolved **additively** (most-general first: embedded → org-defaults → org → project).
- **Post**: every `<P>.prompt.post.md` across all levels — resolved **additively** (same order as pre).

### Assembly algorithm

```
Assemble(P):
    // Recurse into all parent prompt paths, in order
    output = ""
    for each strict prefix Q of P (shortest first):
        output += AssembleFragment(Q)
    output += AssembleFragment(P)
    return output

AssembleFragment(P):
    return concat-of-all(<P>.prompt.pre.md across levels)
         + first-match(<P>.prompt.md across levels)        // optional
         + concat-of-all(<P>.prompt.post.md across levels)
```

A path's strict prefixes are obtained by splitting on `/`. For `report/security` the prefixes are `[report]`. For a hypothetical `code/security/auth`, the prefixes are `[code, code/security]`.

The fragment's `main` is optional: if `<P>.prompt.md` doesn't exist at any level, the fragment is just `pre + post`. This is how the current `report_base_prompt.md` (= `report.prompt.pre.md` in the new world) is applied even though there is no `report.prompt.md` main.

### Worked examples

**Singleton action — `review`:**
```
Assemble("review")
  = AssembleFragment("review")
  = [all review.prompt.pre.md, additive] + [first-match review.prompt.md] + [all review.prompt.post.md, additive]
```

**Role-distributed action — `report/security`:**
```
Assemble("report/security")
  = AssembleFragment("report")                  // parent
    + AssembleFragment("report/security")       // leaf
  = [all report.prompt.pre.md] + [report.prompt.md if any] + [all report.prompt.post.md]
    + [all report/security.prompt.pre.md] + [first-match report/security.prompt.md] + [all report/security.prompt.post.md]
```

### What this replaces

- `report_base_prompt.md` → `prompts/report.prompt.pre.md` (or `.md` if you prefer "main" semantics)
- `code_base_prompt.md` → `prompts/code.prompt.pre.md`
- `report_extra_prompt.md` (broad, project or org level) → `prompts/report.prompt.post.md`
- `<role>/report_extra_prompt.md` (per-role) → `prompts/report/<role>.prompt.post.md`
- `supervisor/review_prompt.md` → `prompts/review.prompt.md`
- `supervisor/review_extra_prompt.md` → `prompts/review.prompt.post.md`

The base-vs-extra distinction in current code (single fallback for base, additive collection for extras) collapses to the simpler rule above: **main is fallback, pre/post are additive**. Everything else falls out of the recursive parent-walk.

### Outer layers (still applied around `Assemble(P)`)

Project-side wrappers stay the same in spirit:
1. ATeam Project Context (auto-injected at the very top)
2. `Assemble(P)` output
3. Cross-agent data injection (e.g. review reads role reports from `shared/reports/<role>.md`; code_management reads `shared/review.md`)
4. Previous artifact content (e.g. previous `shared/reports/<role>.md` for report action)
5. CLI `--extra-prompt`

## Code changes

### `internal/prompts/`

- Replace `assembleRoleAction`, `assembleSupervisorPrompt`, `collectRoleExtras`, `collectSupervisorExtras` with a single `Assemble(env, promptPath string) (string, error)` that implements the recursive algorithm above.
- Replace the `*PromptFile` constants block (`prompts.go:42-61`) with three helpers: `mainFiles(promptPath)`, `preFiles(promptPath)`, `postFiles(promptPath)` — each returns the candidate filesystem paths to check across levels.
- `discoverRoleIDs()` (`embed.go:84-97`): scan `defaults/prompts/report/*.prompt.md` (filename without the `.prompt.md` suffix is the role ID).
- `AllKnownRoleIDs()` (`prompts.go:159-182`): union across `prompts/report/*.prompt.md` at each level.
- `DiscoverReports()` (`prompts.go:208-238`): read from `.ateam/shared/reports/<role>.md`.
- Add `DiscoverSingletonActions()` for `ateam prompt`'s validation (top-level `<name>.prompt.md` files in embedded defaults).

### `internal/root/resolve.go`

Remove:
- `RoleDir`, `RoleReportPath`, `RoleHistoryDir`
- `SupervisorDir`, `ReviewPath`, `VerifyPath`

Add:
- `PromptsDir()` → `.ateam/prompts`
- `SharedDir()` → `.ateam/shared`
- `SharedArtifactPath(action, role string)` — `shared/reports/<role>.md` when role is set, `shared/<action>.md` when role empty
- `SharedHistoryDir(action, role string)` — symmetric, under `shared/history/`

### `internal/runner/runner.go`

- `promoteRuntimeFiles` (line 1156): drop the `*_prompt.md` exclusion — prompts no longer live in canonical output dirs.
- Update canonical-destination computation to use new `SharedArtifactPath`.

### `internal/runner/template.go`

- `PrimaryOutputName()` (line 180): keep current mapping; destination paths come from the new helpers.

### `defaults/`

- `defaults/embed.go`: update `//go:embed` directives to cover `prompts/...`.
- File renames (content unchanged):
  - `defaults/roles/<role>/report_prompt.md` → `defaults/prompts/report/<role>.prompt.md`
  - `defaults/roles/<role>/code_prompt.md` → `defaults/prompts/code/<role>.prompt.md`
  - `defaults/report_base_prompt.md` → `defaults/prompts/report.prompt.pre.md`
  - `defaults/code_base_prompt.md` → `defaults/prompts/code.prompt.pre.md`
  - `defaults/supervisor/review_prompt.md` → `defaults/prompts/review.prompt.md`
  - `defaults/supervisor/code_management_prompt.md` → `defaults/prompts/code_management.prompt.md`
  - `defaults/supervisor/code_verify_prompt.md` → `defaults/prompts/code_verify.prompt.md`
  - `defaults/supervisor/auto_setup_prompt.md` → `defaults/prompts/auto_setup.prompt.md`
  - `defaults/supervisor/exec_debug_prompt.md` → `defaults/prompts/exec_debug.prompt.md`
  - `defaults/supervisor/report_commissioning_prompt.md` → `defaults/prompts/report_commissioning.prompt.md`

### `cmd/*.go`

- Remove every `RoleID: "supervisor"` hardcode in `cmd/review.go:233`, `cmd/code.go:278`, `cmd/auto_setup.go:83`, `cmd/verify.go:163`, `cmd/inspect.go:300`. Dispatch via singleton action name.
- `cmd/prompt.go`: rework `--role` / `--action` / `--supervisor` validation. New surface: `--action review` for singletons (no `--role`), `--role X --action report` for role-distributed actions. Validate `--action` against the discovered set of actions; reject unknown.
- `cmd/roles.go`: list logic unchanged in spirit; now scans `prompts/report/*.prompt.md`.

### `internal/config/config.go`

- `SupervisorConfig` struct (lines 58-63) is kept — `[supervisor]` is still a valid TOML section header for profile/budget overrides. The renames here are filesystem-only; `config.toml` schema is not part of this refactor.

### `internal/web/`

- `handlers.go`, `export.go`: update artifact read paths to `shared/reports/<role>.md` and `shared/review.md`.

## Auto-migration

On `ateam` startup, when `.ateam/` or `.ateamorg/` is loaded, detect the old layout and migrate in place.

**Detection** (any one is enough): `.ateam/roles/` exists, `.ateam/supervisor/` exists, `.ateam/{report,code}_base_prompt.md` exists, `.ateam/{report,code}_extra_prompt.md` exists, `.ateam/setup_overview.md` exists at root.

**Migration map** (project-level; org-level mirrors):

| Old | New |
|---|---|
| `.ateam/roles/<R>/report_prompt.md` | `.ateam/prompts/report/<R>.prompt.md` |
| `.ateam/roles/<R>/code_prompt.md` | `.ateam/prompts/code/<R>.prompt.md` |
| `.ateam/roles/<R>/report_extra_prompt.md` | `.ateam/prompts/report/<R>.prompt.post.md` |
| `.ateam/roles/<R>/code_extra_prompt.md` | `.ateam/prompts/code/<R>.prompt.post.md` |
| `.ateam/roles/<R>/report.md` | `.ateam/shared/reports/<R>.md` |
| `.ateam/roles/<R>/history/...` | `.ateam/shared/history/reports/<R>/...` |
| `.ateam/report_base_prompt.md` | `.ateam/prompts/report.prompt.pre.md` |
| `.ateam/code_base_prompt.md` | `.ateam/prompts/code.prompt.pre.md` |
| `.ateam/report_extra_prompt.md` | `.ateam/prompts/report.prompt.post.md` |
| `.ateam/code_extra_prompt.md` | `.ateam/prompts/code.prompt.post.md` |
| `.ateam/supervisor/review_prompt.md` | `.ateam/prompts/review.prompt.md` |
| `.ateam/supervisor/review_extra_prompt.md` | `.ateam/prompts/review.prompt.post.md` |
| `.ateam/supervisor/code_management_prompt.md` | `.ateam/prompts/code_management.prompt.md` |
| `.ateam/supervisor/code_management_extra_prompt.md` | `.ateam/prompts/code_management.prompt.post.md` |
| `.ateam/supervisor/code_verify_prompt.md` | `.ateam/prompts/code_verify.prompt.md` |
| `.ateam/supervisor/auto_setup_prompt.md` | `.ateam/prompts/auto_setup.prompt.md` |
| `.ateam/supervisor/exec_debug_prompt.md` | `.ateam/prompts/exec_debug.prompt.md` |
| `.ateam/supervisor/report_commissioning_prompt.md` | `.ateam/prompts/report_commissioning.prompt.md` |
| `.ateam/supervisor/review.md` | `.ateam/shared/review.md` |
| `.ateam/supervisor/verify.md` | `.ateam/shared/verify.md` |
| `.ateam/supervisor/history/...` | `.ateam/shared/history/review/...` |
| `.ateam/setup_overview.md` | `.ateam/shared/setup_overview.md` |

After migration, remove the now-empty `roles/` and `supervisor/` directories. Print a one-line notice on stderr on the first migration run. Implementation lives in a new `internal/migrate/v1_layout.go`, invoked from `internal/root/resolve.go` when the env is first materialized.

The migration must be idempotent (re-running it on an already-migrated tree is a no-op).

## Out of scope

- Renaming `code_management` to something shorter (e.g. `manage`). Action names kept verbatim.
- Implementing history archival (the `shared/history/` tree is reserved but not populated yet).
- Reserved-name validation for user role IDs. With the new namespacing, collisions are structurally impossible.
- Changes to `config.toml` schema (`[supervisor]`, `[profiles.roles]`, etc. keep their current keys).
- Built-in prompt content changes — only renames.

## What this refactor does NOT change

- `runtime/<exec_id>/` per-run scratch dirs and template variables (`{{OUTPUT_FILE}}`, `{{OUTPUT_DIR}}`) — mechanism unchanged; only canonical destination paths change.
- `config.toml` schema.
- `runtime.hcl` schema, agent/profile/container config.
- `state.sqlite`, `secrets.env`, `logs/`, `cache/` at `.ateam/` root.

## Critical files to read before implementing

- `internal/prompts/prompts.go` — the resolver this refactor centers on
- `internal/prompts/embed.go` — role discovery from embedded FS
- `internal/root/resolve.go` — path helpers
- `internal/runner/runner.go:737, 1156-1199` — `promoteRuntimeFiles`
- `internal/runner/template.go:17-33, 180-195` — template vars and primary output names
- `defaults/embed.go` + the `defaults/` tree
- `cmd/review.go`, `cmd/code.go`, `cmd/auto_setup.go`, `cmd/verify.go`, `cmd/inspect.go`, `cmd/prompt.go`, `cmd/roles.go`, `cmd/report.go`
- `internal/web/handlers.go`, `internal/web/export.go`
- `CONFIG.md`, `ROLES.md`, `README.md`, `ISOLATION.md` for doc updates

## Verification plan

1. `make build` + `go test ./...` after each significant step.
2. `make test-docker` once at the end (runner + container code is touched).
3. **Golden prompt test:** before the refactor, capture output of `ateam prompt --role <r> --action report` for several roles and `ateam prompt --supervisor --action review` on a test project. After the refactor, re-run (`ateam prompt --action review`) and diff — should be byte-identical except for path strings in any debug output.
4. `ateam roles` lists the same set of roles before/after.
5. End-to-end on a fresh `./test_data/` project: `ateam init`, `ateam report --roles project.security`, verify the report lands at `.ateam/shared/reports/project.security.md`. Then `ateam review`, verify it lands at `.ateam/shared/review.md`.
6. **Migration test:** take a project with the old layout (artifacts plus overrides at all levels), run `ateam` once, verify migration runs and produces equivalent behavior. Re-run to confirm idempotence.
7. **Org-override test:** create a project with both org-level and project-level overrides for the same role; confirm 3-level fallback still works.
8. **Recursive pre/post test:** create a project with `prompts/report.prompt.pre.md` + `prompts/report/security.prompt.pre.md` + `prompts/report/security.prompt.md` + `prompts/report.prompt.post.md`, dump the assembled prompt, verify ordering matches the algorithm.
9. Manual smoke: `ateam prompt --action review`, `ateam prompt --action code_management`, `ateam prompt --role project.security --action report`.
