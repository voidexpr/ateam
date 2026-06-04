# Feature: bundle-aware `ateam prompt` (prompt lifecycle redesign)

## Problem

- `ateam prompt --action X` and `ateam X` build their prompts through parallel
  call sites that have already drifted (e.g., `--action code_management` does
  not bundle `.ateam/shared/review.md`; `--supervisor --action code` does).
- `flow.PromptBundle.Render` is a degenerate `func(RuntimeEnv) (string, error)`
  whose every implementation captures a pre-computed string and ignores its
  argument — the framework's "function" abstraction earns nothing today.
- There is no mapping from action name (or prompt file on disk) to bundle
  constructor in code. Adding a new action means coordinating edits across
  `cmd/prompt.go` and the new verb.
- The current two-pass expansion (dotted-form in assembler, ALL_CAPS in runner)
  is an implementation artifact of layered call sites, not a model anyone
  needs to reason about.

The goal: one lifecycle, one expansion pass, one place each piece of behavior
lives.

## Lifecycle (3 phases)

```
verification ─── resolution ─── execution
   (cheap        (right          (runner
   safety net    before each     invokes the
   at startup)   bundle runs)    agent)
```

- **Verification.** Walks every `PromptBundle` in the pipeline before any
  bundle executes. Calls each bundle's `Prompt.Resolve(env, verifyVars,
  ModeVerify)`. Surfaces authoring errors as a batched `VerifyResult`.
- **Resolution.** Per bundle, just before execution: build per-bundle Vars,
  call `bundle.Prompt.Resolve(env, vars, ModeReal)`, produce the final prompt
  text.
- **Execution.** Runner hands the resolved text to the agent.

Assembly and expansion are entangled within resolution (`{{include
{{prompt.name}}.prompt.md}}` requires expansion of the path arg before the
include can resolve), so they're not separate phases — they fuse into
"resolution."

## Types

### `flow.Prompt`

```go
type Prompt interface {
    Resolve(env *root.ResolvedEnv, vars Vars, mode ResolveMode) (string, error)
}

type ResolveMode int
const (
    ModeReal    ResolveMode = iota  // real values; missing required key = error
    ModeVerify                       // sentinels for runtime-only keys; lenient include?
    ModePreview                      // same as Verify; named distinctly for `ateam prompt` UX
)
```

Three concrete impls:

#### `flow.PromptFile`

Ateam-flavored prompt file. Anchor walk (project → org → embedded), framing
fragments compose (root_pre, dir_pre, role_pre, role_main, role_post,
dir_post, root_post), then the resolver engine runs over the assembled body.

```go
type PromptFile struct {
    Path       string  // "code", "review", "report/security"
    PrePrompt  string  // optional --pre-prompt content
    PostPrompt string  // optional --post-prompt content
    CustomBody string  // optional override of the role main (--prompt / --management)
}
```

#### `flow.PromptText`

Literal-content prompt. Runs the resolver engine on `Text`. No anchor walk,
no framing.

```go
type PromptText struct {
    Text string
}
```

#### Dispatch rule for the CLI

`ateam exec @PATH`, `ateam parallel @PATH`, `ateam prompt @PATH`:

```
PATH ends in ".prompt.md" → flow.PromptFile (with anchor extension below)
otherwise                  → flow.PromptText (file content as-is)
```

If a `.prompt.md` file is **outside** every standard anchor (project/org/
embedded), its parent directory is injected as a **temporary anchor at the
front of the chain** for that single resolution. Sibling `<basename>.pre.*.md`
and dir-level `_pre.*.md` in that directory compose; the standard anchors
still apply for inherited framing.

### `flow.PromptBundle`

```go
type PromptBundle struct {
    // Reporting metadata only. No prompt-resolution logic reads these.
    Name   string  // freeform display name, e.g. "review", "code:fix_sql"
    Role   string  // recorded in agent_execs.role
    Action string  // recorded in agent_execs.action

    Prompt Prompt  // built by the factory; self-contained

    RunOpts  func(RuntimeEnv) runner.RunOpts
    PreExec  []Action
    PostExec []Action
}
```

`Render` is gone. The framework calls `bundle.Prompt.Resolve(...)` at the
moment it needs the text. No `Vars` field on the bundle.

### Factories (action names live here, nowhere else)

One factory function per action in `cmd/`. Each takes the verb's option struct
and returns a `*flow.PromptBundle`. Typed per-action options + a small shim
for the CLI dispatch map; type safety beats less boilerplate.

```go
func NewReviewBundle(env *root.ResolvedEnv, opts ReviewFactoryOpts) (*flow.PromptBundle, error) {
    return &flow.PromptBundle{
        Name: "review", Role: "supervisor", Action: runner.ActionReview,
        Prompt: &flow.PromptFile{
            Path: "review",
            PrePrompt: opts.PrePrompt, PostPrompt: opts.PostPrompt,
            CustomBody: opts.CustomPrompt,
        },
        RunOpts:  ..., PreExec: ..., PostExec: ...,
    }, nil
}
```

No live work in factories; no map writes; no closures over CLI state. The
review prompt template references `{{dynamic.review_reports {{args.roles}}}}`
to inject the formatted reports manifest at resolution time.

### CLI dispatch map

Small lookup in `cmd/`, the **only** place all action names appear in one
list:

```go
var promptFactories = map[string]func(env *root.ResolvedEnv, opts PromptLookupOpts) (*flow.PromptBundle, error){
    "review":          newReviewBundleFromPromptOpts,
    "code":            newCodeBundleFromPromptOpts,
    "code_management": newCodeMgmtBundleFromPromptOpts,
    "code_verify":     newCodeVerifyBundleFromPromptOpts,
    "report":          newReportBundleFromPromptOpts,
    "auto_setup":      newAutoSetupBundleFromPromptOpts,
}
```

Unknown action → fallback to `PromptFile{Path: action}` so any user-installed
`<name>.prompt.md` works without a factory entry.

`ateam prompt --action X` and `ateam X` both go through the same factory, by
construction. Drift is structurally impossible.

## Vars

Vars is set **once when the flow Executor is created** and held for the
invocation's lifetime. Per-bundle `prompt.*` derivation is the only thing
the framework adds during resolution.

| Namespace | Source | When |
|---|---|---|
| `args.*` | CLI flags, every one mapped as `args.<snake_case>` | executor creation |
| `project.*` | `ResolvedEnv` (paths and name only — see `dynamic.project_info` for the rendered context block) | executor creation |
| `env.*` | `os.LookupEnv` (lazy) | executor creation |
| `container.*` | `runtime.hcl` | executor creation |
| `roles.*` | Derived role lists (see below) | per-bundle by factory |
| `prompt.*` | the Prompt being resolved | per-bundle by framework |
| `exec.*` | runner state (sentinels in verify/preview) | per-bundle by framework |

**No `Vars` field on `PromptBundle`.** Anything dynamic flows through
dynamics (next section).

### `args.*` convention

Every CLI flag maps mechanically to `args.<snake_case>`: `--no-project-info`
→ `args.no_project_info`, `--max-age 2h` → `args.max_age`, `--rerun-failed`
→ `args.rerun_failed`. Factories never invent ad-hoc Vars keys; the rule is
uniform and predictable. Operators reading a prompt can `grep args.` and
know what flags drive it.

### `roles.*` namespace

`args.roles` carries the raw CLI value ("what the user typed"). `roles.*`
holds derived lists ("what we're operating on"):

| Var | Meaning |
|---|---|
| `roles.all` | Every known role across config.toml + anchors |
| `roles.enabled` | Roles marked `"on"` in `config.toml` |
| `roles.disabled` | Roles marked `"off"` in `config.toml` |
| `roles.selected` | The operative list after factory filtering — `--roles` ∩ enabled + `--all` / `--max-age` / `--rerun-failed` applied as relevant |
| `roles.failed` | Roles that failed in the last cycle (for `--rerun-failed`) |
| `roles.aged_out` | Roles dropped by `--max-age` (review only) |

Each is a space-separated list per the list convention. Factories populate
only the keys their action uses (review populates `aged_out`; report
populates `failed`; both populate `selected`).

### Sentinels (`exec.*` in verify/preview modes)

| Var | Real | Verify / Preview |
|---|---|---|
| `exec.id` | `<callID>` | `{{AT RUNTIME:exec.id}}` |
| `exec.batch` | real | real if set, else `{{AT RUNTIME:exec.batch}}` |
| `exec.output_dir` | real path | `.ateam/runtime/{{AT RUNTIME:exec.id}}/` |
| `exec.output_file` | real path | `.ateam/runtime/{{AT RUNTIME:exec.id}}/<filename>` |
| every other namespace | real | real |

Sentinels render as `{{AT RUNTIME:ns.key}}` — human-readable; preview output
makes "this gets filled later" visually obvious.

### Lists in Vars

Lists are **space-separated strings**: `args.roles = "security dependencies
code.bugs"`. With the dynamic arg parser's whitespace split (next section),
they fan out naturally when passed unquoted.

## Dynamic functions

```go
package flow

type PromptDynamicFunction func(
    env *root.ResolvedEnv,
    vars Vars,
    mode ResolveMode,
    args ...string,
) (string, error)

type PromptDynamic map[string]PromptDynamicFunction
```

No global registry. The CLI layer constructs the `PromptDynamic` and passes
it into Executor creation:

```go
func buildPromptDynamics() flow.PromptDynamic {
    return flow.PromptDynamic{
        "review_reports":   dynReviewReports,
        "code_mgmt_review": dynCodeMgmtReview,
        // ...
    }
}

func newExecutor(env *root.ResolvedEnv, args flow.Vars) flow.Executor {
    return flow.NewExecutor(env, args, buildPromptDynamics())
}
```

Tests construct executors with whatever `PromptDynamic` they need — clean
isolation; no init-order dependencies.

In prompt templates:

```
{{dynamic.review_reports {{args.roles}}}}
```

## Resolver surface (three layers)

| Layer | Syntax | Resolution |
|---|---|---|
| Directives (engine-baked) | `{{include path}}`, `{{include? path}}`, `{{include path ? TEXT}}`, `{{include_glob pattern}}` | resolver |
| Variable substitution | `{{ns.key}}`, `{{ns.key ? default}}` | resolver, against `Vars` |
| Dynamic functions | `{{dynamic.NAME arg1 arg2 …}}` | resolver, against `PromptDynamic` |

### Include directives

| Directive | Missing-file behavior |
|---|---|
| `{{include path}}` | Error. Strict. |
| `{{include? path}}` | Empty string. Optional. |
| `{{include path ? TEXT}}` | Substitute `TEXT`. Required-with-fallback. |

The first and third share the same underlying code; `include?` is sugar for
`include path ? ""`.

### Variable substitution

| Form | Behavior |
|---|---|
| `{{ns.key}}` | Returns the value. Errors on unknown key in a known namespace (typo guard). Passes through verbatim when the namespace itself is unknown. |
| `{{ns.key ? default}}` | Same as above, except renders `default` when the value is the empty string. Does NOT fire on typos — unknown key in known namespace still errors. |

### Arg parsing (dynamics and include directives)

- Whitespace splits args outside quotes.
- Double or single quotes preserve internal whitespace: `"hello world"` or
  `'hello world'` → one arg.
- Escapes inside quotes: `\"`, `\'`, `\\`. No escapes outside quotes.
- **Order:** variable expansion first, then tokenization.

This means quoting wraps the **value placeholder**, not the **template**:

```
{{dynamic.foo "{{args.title}}"}}        → 1 arg = "Hello World"
{{dynamic.review_reports {{args.roles}}}} → 3 args = ["security", "dependencies", "code.bugs"]
```

Same convention shell users already know.

**Includes apply the same rule.** `{{include EXPR}}` requires EXPR to be
properly quoted by the prompt author when the expanded value can contain
whitespace. The engine does not attempt to be clever about it — same
discipline as a shell command. `{{include? "{{args.review_path}}"}}` for a
value that might have spaces; `{{include? {{prompt.name}}.md}}` when the
expanded value is a known no-space identifier.

### Legacy ALL_CAPS dropped

`{{ROLE}}`, `{{BATCH}}`, `{{EXEC_ID}}`, `{{OUTPUT_DIR}}`, `{{SOURCE_DIR}}`,
and the rest of `varmap.go`'s `VarRenameMap` — gone. Dotted form only. A
migrator rewrites `runtime.hcl` agent args on first read so existing installs
don't break on upgrade.

## Verification

Built into `flow.Run` by default. Walks every `PromptBundle` reachable from
the top step (single bundle, `Pipeline`, or `Parallel`) and calls each
`bundle.Prompt.Resolve` in `ModeVerify`. Accumulates errors per bundle.

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

Tree traversal: `flow.Step` interface gains `Walk(func(*PromptBundle))` —
callback-based, no slice allocation. Composites recurse.

Verification scope is **medium**: assemble each bundle, walk anchors, resolve
`{{include}}`, run the engine end-to-end against verify-mode Vars with
sentinels for runtime-only keys.

Verification's behavior decomposes from primitives — no special-case rules:

- **Typos in known namespaces** → engine errors (caught).
- **Strict `{{include path}}` of missing file** → engine errors (caught).
- **Optional `{{include? path}}` or `{{include path ? TEXT}}` of missing file**
  → engine substitutes empty / fallback text (not an error; same as exec time).
- **Runtime-only `exec.*` keys** → sentinel renders (not an error).
- **Format errors inside present-but-included files** → engine errors when
  expanding the included content (caught).

Errors are batched, not first-fail. Dedup by `(file, line, message)` before
printing to mitigate cascading errors.

## `ateam prompt` behavior

Same machinery as live runs, just `mode = ModePreview`:

```go
factory := promptFactories[promptAction]
bundle, err := factory(env, opts)
vars := newVarsForMode(rc, bundle, ModePreview)
text, err := bundle.Prompt.Resolve(env, vars, ModePreview)
fmt.Println(text)
```

A typo in any prompt's `{{ns.key}}` surfaces the same engine error verify
would. `ateam prompt --action X` doubles as a single-bundle verify check.

The positional `@PATH` form (already shipped) continues to work — dispatched
to `PromptFile` or `PromptText` per the `.prompt.md` suffix rule.

## Migration plan

The refactor is large but mechanical. Suggested commit shape:

1. **Add `PromptDynamic` infrastructure** (resolver + parser + executor
   plumbing). No behavior change; nothing calls dynamics yet.
2. **Drop ALL_CAPS legacy** (engine, varmap, defaults sweep, runtime.hcl
   migrator). Independent and tractable in one pass.
3. **Introduce `flow.Prompt` interface, `PromptFile`, `PromptText`.** Port
   one verb (review or code) end-to-end as a proof.
4. **Migrate remaining verbs** to factories. Delete `cmd/code_assemble.go`,
   `cmd/report_assemble.go`, `cmd/review_assemble.go`'s old helpers.
5. **Rewrite `cmd/prompt.go`** against the factory dispatch map. Delete the
   old `runPromptRole` / `runPromptSupervisor` / `runPromptAction` branches.
6. **Wire verification into `flow.Run`** with the new `Walk` traversal.
   Update tests for the batched-error UX.
7. **`--supervisor` deprecation** — emit a warning when the flag is used,
   pointing at the canonical `--action <X>` form. Removal in a follow-up
   release.

## Rules digest (for `ateam prompt --help` and `CONFIG.md`)

Short, copy-pasteable for documentation:

```
Prompt directives:
  {{ns.key}}                 Substitute a Vars value. Errors on typos in known namespaces.
  {{ns.key ? default}}       Substitute, falling back to `default` if the value is empty.
  {{include path}}           Required include. Missing file → error.
  {{include? path}}          Optional include. Missing file → empty.
  {{include path ? TEXT}}    Required include with fallback. Missing file → TEXT.
  {{include_glob pattern}}   Glob-include; joined matches.
  {{dynamic.NAME args...}}   Call a registered dynamic function.

Namespaces:
  args.*       User CLI flags (per-invocation).
  project.*    Project paths and name.
  env.*        Environment variables.
  container.*  Container type and name (from runtime.hcl).
  prompt.*     The current prompt's name/path/action.
  exec.*       Execution-time values (id, batch, output_dir, output_file, …).
               Renders as {{AT RUNTIME:exec.<key>}} in preview/verify modes.

Argument parsing for include directives and dynamics:
  - Tokens are whitespace-separated.
  - Single or double quotes preserve whitespace: "hello world" → one arg.
  - Variables expand BEFORE tokenization. Quote a placeholder if its value
    must stay a single arg.

Lists in Vars are space-separated strings; they fan out naturally when
passed unquoted to a dynamic.
```

## Accepted breaking changes

- **`ateam review --prompt @file` removed.** Operators who want to override
  the supervisor body for review put their custom content at
  `.ateam/prompts/review.prompt.md` (project anchor) or
  `.ateamorg/prompts/review.prompt.md` (org anchor). Same precedence chain as
  every other prompt override. No special CLI flag for "one-shot custom prompt"
  — anchor overrides are the documented mechanism.
- **`ateam code --management @file` removed.** Same story:
  `.ateam/prompts/code_management.prompt.md` to override.
- **`ateam exec @PATH` where `PATH` ends in `.prompt.md` and is outside every
  standard anchor** composes framing from PATH's parent dir (decision 11/B
  above). Files at `.ateam/prompts/...` keep working as today.
- **`{{project.info}}` → `{{dynamic.project_info}}`.** The project-context
  block (the long "You are part of the ateam software…" preamble) moves from
  a static Vars value to a dynamic function. Defaults sweep; user customs
  under `.ateam/prompts/` get wiped as part of the pre-v1 one-time upgrade.
  Side benefit: `--no-project-info` becomes `args.no_project_info = "true"`
  and the dynamic returns empty when set — no special-casing in the engine.

## How CLI flags shape prompts

Resolved: no conditional directives needed in the engine. Three mechanisms,
applied per flag:

1. **Static `args.*` value** — for flags that gate already-static content.
   `--no-project-info` → `args.no_project_info = "true"`; the relevant
   dynamic (`{{dynamic.project_info}}`) checks the flag and returns empty.
2. **Factory pre-filtering into `roles.*`** — for flags that narrow a
   dataset. `--max-age`, `--all`, `--rerun-failed` all funnel into
   `roles.selected`. Prompts iterate over `roles.selected` without knowing
   which flags shaped it.
3. **Dynamic functions** — for per-bundle conditional content tied to
   `prompt.*`. `--ignore-previous-report` becomes
   `{{dynamic.previous_report {{prompt.name}}}}` — the dynamic reads
   `args.ignore_previous_report` and decides whether to include.

Inspection flags (`--paths`, `--inline-paths`) are verb-level orchestration:
they call `PromptFile.Inspect()` (a separate method from `Resolve`) and
print the per-section table. No engine extension needed.

Execution-mode flags (`--plan-only`, `--dry-run`) are also verb-level: the
verb resolves the bundle, prints, and skips `flow.Run`. No engine
extension needed.

**The engine stays small.** No `{{if}}` / `{{range}}` / sub-templates added.
The cost is paid once at factory time (filtering, args population), at
dynamic-invocation time (per-bundle conditional content), and at the verb
layer (mode flags).

## Behavior changes intentional in this refactor

| # | Change | Notes |
|---|---|---|
| 1 | `ateam prompt --action code_management` bundles `.ateam/shared/review.md` content | Today it doesn't; legacy `--supervisor --action code` does. Drift fix. |
| 2 | `ateam prompt --action review` bundles reports manifest | Same drift fix. |
| 3 | `ateam exec @PATH` outside anchors with `.prompt.md` suffix composes framing | Per decision 11/B. |
| 4 | ALL_CAPS forms in user prompts and runtime.hcl require migration | Auto-migrator on first read with loud stderr warning. |
| 5 | New syntax: `{{ns.key ? default}}`, `{{include path ? TEXT}}`, `{{dynamic.NAME args}}` | Pure additions. |
| 6 | Preview/dry-run output shows `{{AT RUNTIME:exec.id}}` instead of `{{EXEC_ID}}` placeholders | Cosmetic but user-visible. |
| 7 | Pipeline verification runs before any bundle executes | New failure mode: batched errors at startup. |
| 8 | `--supervisor` emits a deprecation warning | Flag still works for one release. |
| 9 | `ateam review --prompt` and `ateam code --management` removed | See Accepted breaking changes above. |

Everything else MUST stay byte- or behavior-equivalent (database schema,
`ps`/`cost`/`serve` columns, forensic artifacts, runtime.hcl template
substitution after migration, stream JSONL format, `--paths`/`--inline-paths`
output format, per-action canonical destinations, etc.).

## Implementation sequence

| # | Commit | Behavior change | Risk |
|---|---|---|---|
| 1 | Engine extensions: `PromptDynamic`, `{{ns.key ? default}}`, `{{include path ? TEXT}}`, quoted-arg parser | None | Low |
| 2 | Add `flow.Prompt`, `PromptFile`, `PromptText`, `PromptDynamicFunction` types + factory scaffolding (coexists with Render) | None | Low |
| 3 | Migrate one verb end-to-end (suggest `review`) — factory + PromptFile + Render still wraps | None observable | Medium |
| 4 | Migrate remaining verbs (`code`, `verify`, `auto_setup`, `code_management`'s supervisor, `report` per-role, `inspect --auto-debug`, `exec`, `parallel`) | None observable | Medium |
| 5 | Drop ALL_CAPS legacy + sweep `defaults/prompts/` + runtime.hcl auto-migrator + `{{project.info}}` → `{{dynamic.project_info}}` migration | Change #4 above; project_info dynamic | **High** (test sweep) |
| 6 | Wire verification into `flow.Run` (`Walk` callback, batched VerifyResult) | Change #7 above | Medium |
| 7 | Rewrite `cmd/prompt.go` against factory dispatch; `--supervisor` deprecation warning; `--paths`/`--inline-paths` rewire | Changes #1, #2, #6, #8 land here | Medium |
| 8 | Kill `PromptBundle.Render`; framework calls `bundle.Prompt.Resolve` directly | None observable (cleanup) | Low |
| 9 | `@PATH/foo.prompt.md` outside-anchor framing | Change #3 | Low |

Bisectable across the whole sequence: regressions between #3 and #8 can be
localized to a specific verb's port.

## Followups (not in scope for the initial implementation)

- **`--supervisor` removal** after the deprecation period.
- **`PromptBundle.Render` is gone** in this design; if a future use case
  needs per-attempt re-resolution (retry with re-read of live data), the
  framework can call `bundle.Prompt.Resolve` again on retry — no Render
  change needed.
- **`{{shell CMD}}`** — defer until we have a read-only sandbox to enforce
  bounded output and no side effects.
- **`exec` / `parallel` arbitrary-prompt path** stays outside the factory
  registry — they construct `PromptText` directly from user input.
- **Single-process pipelines** (one process executes report → review → code
  sequentially). Today's design already supports this: each verb's factory
  runs at its bundle's turn, not at pipeline construction. The migration to
  a single-process pipeline command (`ateam run-all` collapsing to one
  process) is a separate effort.
