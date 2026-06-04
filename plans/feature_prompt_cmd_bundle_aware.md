# Feature: bundle-aware `ateam prompt` (prompt lifecycle redesign)

## Problem

- `ateam prompt --action X` and `ateam X` build their prompts through parallel
  call sites that have already drifted (e.g., `--action code_management` does
  not bundle `.ateam/shared/review.md`; `--supervisor --action code` does).
- `flow.PromptBundle.Render` is a degenerate `func(RuntimeEnv) (string, error)`
  whose every implementation captures a pre-computed string and ignores its
  argument.
- No mapping from action name to bundle constructor; adding a new action
  means coordinating edits across `cmd/prompt.go` and the new verb.
- Two-pass expansion (dotted-form in assembler, ALL_CAPS in runner) is an
  implementation artifact of layered call sites, not a model anyone needs to
  reason about.
- A subtle circularity: today's runner hashes the pre-substitution prompt
  during `InsertCall`, then substitutes `{{EXEC_ID}}` etc. *after* the hash
  is taken. The recorded hash doesn't match what the agent receives.

Goals: one lifecycle, one expansion pass, a clean split between flow (the
agent-and-prompt orchestration framework) and the prompts subsystem (anchor
walks, framing, expansion, dynamics, inspection).

## Lifecycle (3 phases)

```
verification ─── resolution ─── execution
   (cheap        (right          (runner
   safety net    before each     invokes the
   at startup)   bundle runs)    agent)
```

- **Verification.** Walks every `PromptBundle` in the pipeline before any
  bundle executes. Calls each bundle's `Prompt.Resolve(rt, ModePreview)`.
  Surfaces authoring errors as a batched `VerifyResult`.
- **Resolution.** Per bundle, in this order:
  1. `Executor.Prepare(opts)` allocates `exec_id`, log/runtime paths, and
     inserts the DB row with `prompt_file = .ateam/logs/<exec_id>/prompt.md`.
  2. Flow builds `Runtime` (Vars populated with real `exec.*` values,
     `prompt.*` from bundle metadata, dynamics map).
  3. `bundle.Prompt.Resolve(rt, ModeReal)` produces the final text.
  4. Flow writes the text to `prepared.PromptFile`.
  5. `Executor.ExecutePrepared(ctx, prepared, text)` invokes the agent.

  If step 3 errors after Prepare succeeds: the row exists; `prompt_file`
  points at a non-existent path. `inspect` / `web` / `tail` handle missing
  files gracefully — same shape they already need for in-flight runs.

- **Execution.** Runner hands the resolved text to the agent.

Assembly and expansion entangle within resolution (`{{include
{{prompt.name}}.prompt.md}}` requires expanding the path arg before include
resolves), so they're not separate phases — they fuse inside the Prompt
impl.

## Architecture: flow vs prompts subsystem

| Lives in `flow` | Lives in `internal/prompts/` |
|---|---|
| `Prompt` interface | `RawTextPrompt`, `PromptText`, `PromptFile` impls |
| `Runtime` struct | Resolver engine (`Expand`, anchor walk, `Assemble`) |
| `PromptBundle` | `Vars` type and `MapVars` impl |
| `ResolveMode`, `Section` (re-exported) | `PromptDynamic`, `PromptDynamicFunction` |
| Lifecycle (`Run`, `Verify`, `Walk`) | `Vars` constructor (combines runtime + static) |

Flow defines *when* prompts resolve and *what shape* they have. The prompts
package owns *how* they resolve. Flow imports prompts to expose the types
its API talks about; the prompts package doesn't import flow.

## Types

### `flow.Prompt`

```go
package flow

type Prompt interface {
    // Resolve produces the final prompt text. Mode controls whether
    // runtime-only values (exec.*) substitute real data or sentinels.
    Resolve(rt *Runtime, mode ResolveMode) (string, error)

    // Inspect returns per-section provenance for --paths / --inline-paths.
    // Returns (nil, nil) when the Prompt has no section structure (e.g.
    // literal text). Always preview-style — Inspect is for human display,
    // not live execution.
    Inspect(rt *Runtime) ([]Section, error)
}

type ResolveMode int
const (
    ModeReal    ResolveMode = iota  // real exec.* values; missing required key errors
    ModePreview                      // sentinels for runtime-only keys
)

// Section is re-exported from internal/prompts for use in API signatures.
type Section = prompts.Section
```

`ModeVerify` is **not** a third mode — verification is a caller pattern:
"call `Resolve(rt, ModePreview)` on every bundle and accumulate errors."
The impl's behavior is identical in verify and preview contexts.

### `flow.Runtime`

The per-invocation context. Holds everything a `Prompt` impl might need to
resolve. `Vars` lives here alongside the DB handle and paths:

```go
type Runtime struct {
    DB       *calldb.CallDB
    Env      *root.ResolvedEnv
    WorkDir  string
    Vars     Vars            // built by flow, opaque to flow
    Dynamics PromptDynamic   // registered dynamic functions

    // Per-bundle runtime values populated by Prepare. Zero values in
    // ModePreview / verify contexts (sentinels filled by Vars instead).
    ExecID     int64
    Batch      string
    OutputDir  string
    OutputFile string
}

// Re-exports so flow callers don't need to import prompts directly.
type Vars = prompts.Vars
type PromptDynamic = prompts.PromptDynamic
```

### Concrete Prompt impls (in `internal/prompts`)

```go
// RawTextPrompt: literal text, no expansion, no anchor walk.
// Used by `ateam exec --raw "..."` and `ateam exec --raw @file`.
type RawTextPrompt struct {
    Text string
}

// PromptText: literal text WITH expansion. Used by `ateam exec "..."`,
// `ateam exec @file` (without --raw), and `ateam prompt @file`.
type PromptText struct {
    Text string
}

// PromptFile: anchored .prompt.md with framing. Composes root_pre, dir_pre,
// role_main, role_post, dir_post fragments per the assembler's rules; then
// expands the assembled body.
type PromptFile struct {
    Path       string  // "code", "review", "report/security"
    PrePrompt  string  // optional --pre-prompt content
    PostPrompt string  // optional --post-prompt content
    CustomBody string  // optional override of the role main
    // Future impls (JinjaPrompt, RemotePrompt, …) hold their own state in
    // their struct fields the same way.
}
```

All three implement `flow.Prompt`. Each holds whatever state it needs in
its own fields. New impls (Jinja, remote-fetched, etc.) just satisfy the
interface — no registration, no plugin system.

### Dispatch rule for `@PATH`

`ateam exec @PATH`, `ateam parallel @PATH`, `ateam prompt @PATH`:

```
--raw set                                                  → RawTextPrompt
PATH ends in ".prompt.md"                                  → PromptFile
otherwise                                                  → PromptText
```

When PATH is `.prompt.md` but **outside** every standard anchor, its
parent dir is injected as a temporary anchor at the front of the chain so
sibling `<basename>.pre.*.md` and dir-level `_pre.*.md` in that dir
compose. Standard anchors still apply for inherited framing.

### `flow.PromptBundle`

```go
type PromptBundle struct {
    Name, Role, Action string  // reporting metadata only; no logic reads these

    Prompt Prompt  // built by the factory; self-contained

    RunOpts  func(RuntimeEnv) runner.RunOpts
    PreExec, PostExec []Action
}
```

`Render` is gone. Framework calls `bundle.Prompt.Resolve(rt, mode)` at the
moment it needs the text.

## Vars

Lives in the prompts package. Single interface; the framework builds an
impl per-invocation and stores it on `Runtime.Vars`.

| Namespace | Source | When |
|---|---|---|
| `args.*` | CLI flags, **positive-listed by the factory** | factory time (per-bundle merge into `rt.Vars`) |
| `project.*` | `ResolvedEnv` (paths and name only) | executor creation |
| `env.*` | `os.LookupEnv` (lazy) | executor creation |
| `container.*` | `runtime.hcl` | executor creation |
| `roles.*` | derived role lists | factory time |
| `prompt.*` | the Prompt being resolved (name, path, action) | per-bundle by framework |
| `exec.*` | runner state (sentinels in `ModePreview`) | per-bundle by framework |

### `args.*` is curated, not mechanical

Factories explicitly populate the `args.*` keys their prompt consumes.
Adding to `args.*` is **adding to a stable public prompt API**: prompt
files reference these keys; renaming or removing one is a breaking change
for any prompt that uses it.

Rule: factory code reads CLI flags, decides what to expose, populates
specific `args.<snake_case>` keys. The CLI flag set is not the prompt API.
The exposed `args.*` set is.

### `roles.*` namespace

For prompts that operate on role lists, dedicated keys disambiguate
"what we're acting on" from "what the user typed":

| Var | Meaning |
|---|---|
| `roles.all` | Every known role across config.toml + anchors |
| `roles.enabled` | Roles marked `"on"` in `config.toml` |
| `roles.disabled` | Roles marked `"off"` in `config.toml` |
| `roles.selected` | The operative list after factory filtering |
| `roles.failed` | Roles that failed in the last cycle (for `--rerun-failed`) |
| `roles.aged_out` | Roles dropped by `--max-age` (review only) |

Each value is a space-separated string per the list convention. Factories
populate only the keys their action uses.

### Sentinels (`ModePreview`)

| Var | `ModeReal` | `ModePreview` |
|---|---|---|
| `exec.id` | `<callID>` | `{{AT RUNTIME:exec.id}}` |
| `exec.batch` | real | real if known, else `{{AT RUNTIME:exec.batch}}` |
| `exec.output_dir` | real path | `.ateam/runtime/{{AT RUNTIME:exec.id}}/` |
| `exec.output_file` | real path | `.ateam/runtime/{{AT RUNTIME:exec.id}}/<filename>` |
| every other namespace | real | real |

Sentinels render as `{{AT RUNTIME:ns.key}}` — human-readable; preview
output makes "this gets filled later" obvious.

### Lists in Vars

Lists are space-separated strings: `args.roles = "security dependencies
code.bugs"`. The dynamic arg parser splits on whitespace, so lists fan out
naturally when passed unquoted.

## Dynamic functions

```go
package prompts

type PromptDynamicFunction func(
    rt *flow.Runtime, mode flow.ResolveMode, args ...string,
) (string, error)

type PromptDynamic map[string]PromptDynamicFunction
```

No global registry. The CLI layer constructs the `PromptDynamic` map at
executor creation and passes it on `Runtime.Dynamics`. Tests build
executors with whatever dynamics they need.

Invoked from templates as `{{dynamic.NAME arg1 arg2 ...}}`.

### Dynamics are prompt API

Each registered dynamic is a stability commitment to prompt authors.
Per-dynamic documentation includes:

- **Args contract**: positional types; required vs optional.
- **Output shape**: text format; structured block if applicable.
- **Side effects**: what the dynamic reads (files, DB, git, env).
- **Mode behavior**: what `ModeReal` returns vs `ModePreview` (typically a
  sentinel block for preview, real data for real).

Internal dynamics used only by ateam-shipped prompts can use a `_` prefix
(`_internal_X`) to mark them off-API and skip the stability commitment.

## Resolver surface (three layers)

| Layer | Syntax | Resolution |
|---|---|---|
| Directives | `{{include path}}`, `{{include? path}}`, `{{include path ? TEXT}}`, `{{include_glob pattern}}` | engine-baked |
| Variable substitution | `{{ns.key}}`, `{{ns.key ? default}}` | engine, against `Vars` |
| Dynamic functions | `{{dynamic.NAME arg1 arg2 ...}}` | engine, against `PromptDynamic` |

### Include directives

| Directive | Missing-file behavior |
|---|---|
| `{{include path}}` | Error. Strict. |
| `{{include? path}}` | Empty string. Optional. |
| `{{include path ? TEXT}}` | Substitute `TEXT`. Required-with-fallback. |

### Variable substitution

| Form | Behavior |
|---|---|
| `{{ns.key}}` | Returns the value. Errors on unknown key in a known namespace. Passes through verbatim when the namespace itself is unknown. |
| `{{ns.key ? default}}` | Same as above, except renders `default` when the value is empty. Typos in known namespaces still error. |

### Arg parsing (dynamics and include directives)

- Whitespace splits args outside quotes.
- Double or single quotes preserve internal whitespace: `"hello world"` → one arg.
- Escapes inside quotes: `\"`, `\'`, `\\`. No escapes outside quotes.
- **Order:** variable expansion first, then tokenization.

Quoting wraps the value placeholder, not the template:

```
{{dynamic.foo "{{args.title}}"}}        → 1 arg = "Hello World"
{{dynamic.review_reports {{args.roles}}}} → 3 args = ["security", "dependencies", "code.bugs"]
```

**Includes follow the same rule.** Authors quote when the expanded value
can contain whitespace: `{{include? "{{args.report_path}}"}}`. Same
discipline as a shell command. The engine doesn't try to be clever.

### Legacy ALL_CAPS dropped

`{{ROLE}}`, `{{BATCH}}`, `{{EXEC_ID}}`, `{{OUTPUT_DIR}}`, `{{SOURCE_DIR}}`,
and the rest of `varmap.go`'s `VarRenameMap` — gone. Dotted form only.

## How CLI flags shape prompts

No conditional directives in the engine. Three mechanisms cover every flag:

1. **Static `args.*` value** — the factory exposes a specific `args.*`
   value the prompt is documented to consume. `--no-project-info` →
   factory sets `args.no_project_info = "true"`; the relevant dynamic
   checks it. Positive-listed by the factory, not mechanical.
2. **Factory pre-filtering into `roles.*`** — `--max-age`, `--all`,
   `--rerun-failed` funnel into `roles.selected`. Prompts iterate over
   `roles.selected` without knowing which flags shaped it.
3. **Dynamic functions** — `--ignore-previous-report` becomes
   `{{dynamic.previous_report {{prompt.name}}}}`; the dynamic reads
   `args.ignore_previous_report` and decides whether to include.

Inspection flags (`--paths`, `--inline-paths`) call `bundle.Prompt.Inspect()`
at the verb layer. Execution-mode flags (`--plan-only`, `--dry-run`)
short-circuit at the verb layer — resolve the bundle, print, skip
`flow.Run`. Neither needs engine extensions.

## Verification

```go
type VerifyResult struct {
    Errors []VerifyError  // nil/empty => clean
}

type VerifyError struct {
    BundleName string
    Err        error
}

func Verify(top Step, rc RunCtx) *VerifyResult { … }

func Run(top Step, env RuntimeEnv, rc RunCtx) PipelineResult {
    if vr := Verify(top, rc); vr != nil && len(vr.Errors) > 0 {
        return failedWithVerifyErrors(vr)
    }
    // existing execution path
}
```

Tree traversal: `flow.Step` interface gains `Walk(func(*PromptBundle))`.
Verify calls each `bundle.Prompt.Resolve(rt, ModePreview)`. Errors batched
per decision.

Verification's behavior decomposes from primitives — no special-case rules:

- **Typos in known namespaces** → engine errors.
- **Strict `{{include path}}` of missing file** → engine errors.
- **`{{include? path}}` or `{{include path ? TEXT}}` of missing file** →
  engine renders empty or fallback (same as exec time).
- **Runtime-only `exec.*` keys** → sentinel renders.
- **Format errors inside included files** → engine errors when expanding.

Errors batched. Dedup by `(file, line, message)` before printing.

## Factories (action names live here, nowhere else)

One factory function per action in `cmd/`. Each takes the verb's typed
option struct and returns a `*flow.PromptBundle`. Factory exposes only the
`args.*` keys its prompt is documented to consume:

```go
func NewReviewBundle(env *root.ResolvedEnv, opts ReviewFactoryOpts) (*flow.PromptBundle, error) {
    staticVars := map[string]string{
        "args.no_project_info": boolStr(opts.NoProjectInfo),
        "roles.selected":       strings.Join(opts.SelectedRoles, " "),
        // ...positive-listed; nothing else from opts leaks into args.*
    }
    return &flow.PromptBundle{
        Name: "review", Role: "supervisor", Action: runner.ActionReview,
        Prompt: &prompts.PromptFile{
            Path:       "review",
            PrePrompt:  opts.PrePrompt,
            PostPrompt: opts.PostPrompt,
            StaticVars: staticVars,
        },
        RunOpts: ..., PreExec: ..., PostExec: ...,
    }, nil
}
```

CLI dispatch for `ateam prompt --action X`: a small map in `cmd/` is the
only place all action names appear together. Unknown action → fallback to
`PromptFile{Path: action}` so any `.ateam/prompts/<name>.prompt.md` works
without a factory entry.

## `ateam prompt` behavior

Same machinery as live runs with `mode = ModePreview`:

```go
factory := promptFactories[promptAction]
bundle, err := factory(env, opts)
rt := buildRuntime(env, ModePreview)  // sentinel exec.*
text, err := bundle.Prompt.Resolve(rt, ModePreview)
fmt.Println(text)
```

A typo surfaces the same engine error verify would. `--paths` /
`--inline-paths` call `bundle.Prompt.Inspect(rt)` instead.

## Accepted breaking changes

- **`ateam review --prompt @file` removed.** Operators put custom content at
  `.ateam/prompts/review.prompt.md` (project anchor) or
  `.ateamorg/prompts/review.prompt.md` (org anchor).
- **`ateam code --management @file` removed.** Same pattern with
  `code_management.prompt.md`.
- **`ateam exec @PATH` where `PATH` ends in `.prompt.md` and is outside every
  standard anchor** composes framing from PATH's parent dir.
- **`{{project.info}}` → `{{dynamic.project_info}}`.** Defaults sweep;
  `--no-project-info` becomes `args.no_project_info = "true"` and the
  dynamic returns empty when set.
- **ALL_CAPS legacy syntax removed.** Forms like `{{ROLE}}`, `{{BATCH}}`,
  `{{EXEC_ID}}`, `{{SOURCE_DIR}}` no longer resolve.

## Migration policy

**Detection-not-destruction.** On first read after upgrade, detect
incompatible syntax in user prompts and `runtime.hcl` agent args. If
detected: emit a structured error pointing at file/line/what-to-change.
Refuse to run. **No automated rewrite.** Operators fix files manually —
the affected population is small.

No `ateam migrate prompts` command in scope. Defaults shipped with the
binary are pre-migrated; user prompts are the operator's responsibility.

## Behavior changes intentional in this refactor

| # | Change |
|---|---|
| 1 | `ateam prompt --action code_management` bundles `.ateam/shared/review.md` (today doesn't; legacy `--supervisor --action code` does) |
| 2 | `ateam prompt --action review` bundles reports manifest (same drift fix) |
| 3 | `ateam exec @PATH` outside anchors with `.prompt.md` suffix composes framing |
| 4 | ALL_CAPS forms in user prompts and runtime.hcl error with file/line guidance |
| 5 | New syntax: `{{ns.key ? default}}`, `{{include path ? TEXT}}`, `{{dynamic.NAME args}}` |
| 6 | Preview/dry-run output shows `{{AT RUNTIME:exec.id}}` instead of `{{EXEC_ID}}` |
| 7 | Pipeline verification runs before any bundle executes; failures are batched |
| 8 | `--supervisor` emits a deprecation warning |
| 9 | `ateam review --prompt` and `ateam code --management` removed |
| 10 | `agent_execs` schema: `prompt_hash` column replaced with `prompt_file` (path) |
| 11 | `--raw` flag on `exec`/`parallel`/`prompt` selects literal-text mode without expansion |

Everything else stays byte-equivalent (DB schema apart from #10, ps/cost/serve
columns, forensic artifacts, runtime.hcl semantics after migration, JSONL
stream format, per-action canonical destinations).

## Implementation sequence

| # | Commit | Behavior change | Risk |
|---|---|---|---|
| 1 | Engine extensions: `PromptDynamic`, `{{ns.key ? default}}`, `{{include path ? TEXT}}`, quoted-arg parser | None | Low |
| 2 | Add `flow.Prompt`, `flow.Runtime`, `prompts.{RawTextPrompt, PromptText, PromptFile}` (coexists with today's `Render`) | None | Low |
| 3 | Migrate one verb end-to-end (suggest `review`) — factory + PromptFile + Render still wraps for compat | None observable | Medium |
| 4 | Migrate remaining verbs (`code`, `verify`, `auto_setup`, `code_management` supervisor, `report` per-role, `inspect --auto-debug`, `exec`, `parallel`) | None observable | Medium |
| 5 | Drop ALL_CAPS legacy + sweep `defaults/prompts/` + `{{project.info}}` → `{{dynamic.project_info}}` migration + ALL_CAPS detection-error in `runtime.hcl` | Changes #4, #6 | **High** (test sweep) |
| 6 | Replace `prompt_hash` with `prompt_file`; reshape `Executor` interface (Prepare → Resolve → ExecutePrepared) | Change #10 | Medium |
| 7 | Wire verification into `flow.Run` (`Walk` callback, batched VerifyResult) | Change #7 | Medium |
| 8 | Rewrite `cmd/prompt.go` against factory dispatch; `--supervisor` deprecation warning; `--paths` / `--inline-paths` rewire to `Prompt.Inspect()`; `--raw` flag on `exec` / `parallel` / `prompt` | Changes #1, #2, #6, #8, #11 land here | Medium |
| 9 | Kill `PromptBundle.Render`; framework calls `bundle.Prompt.Resolve` directly | None observable (cleanup) | Low |
| 10 | `@PATH/foo.prompt.md` outside-anchor framing | Change #3 | Low |
| 11 | Remove `ateam review --prompt` and `ateam code --management` flags | Change #9 | Low |

## Rules digest (for `ateam prompt --help` and `CONFIG.md`)

```
Prompt directives:
  {{ns.key}}                 Substitute a Vars value. Errors on typos in known namespaces.
  {{ns.key ? default}}       Substitute, falling back to default if the value is empty.
  {{include path}}           Required include. Missing file → error.
  {{include? path}}          Optional include. Missing file → empty.
  {{include path ? TEXT}}    Required include with fallback. Missing file → TEXT.
  {{include_glob pattern}}   Glob-include; joined matches.
  {{dynamic.NAME args...}}   Call a registered dynamic function.

Namespaces:
  args.*       Factory-exposed CLI values (positive-listed; stable prompt API).
  project.*    Project paths and name.
  env.*        Environment variables.
  container.*  Container type and name (from runtime.hcl).
  roles.*      Derived role lists (all, enabled, selected, failed, aged_out).
  prompt.*     The current prompt's name/path/action.
  exec.*       Execution-time values (id, batch, output_dir, output_file).
               Renders as {{AT RUNTIME:exec.<key>}} in preview/verify modes.

Argument parsing for include directives and dynamics:
  - Tokens are whitespace-separated outside quotes.
  - Single or double quotes preserve whitespace: "hello world" → one arg.
  - Variables expand BEFORE tokenization. Quote a placeholder when its value
    must stay a single arg.

Lists in Vars are space-separated strings; they fan out naturally when
passed unquoted to a dynamic.
```

## Followups (not in scope)

- **`--supervisor` removal** after the deprecation period.
- **`{{shell CMD}}`** — defer until we have a read-only sandbox.
- **Executable prompts (`#!/usr/bin/env python` + `.prompt.py`)** — defer; the
  three-mechanism model handles every current use case.
- **`ateam migrate prompts` command** — re-evaluate if user prompts ever
  proliferate enough that manual migration becomes painful.
