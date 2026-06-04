# Feature: bundle-aware `ateam prompt` (prompt lifecycle redesign)

## Implementation status

**Next round complete (all 10 steps shipped).** The construction
phase shipped: single substitution pass, factory-only verb
composition, `Prompt.Resolve` is load-bearing in both preview and
execution, all the legacy `assemble*` helpers are gone, and the
`root → prompts` import cycle is broken with `ResolveContext.Env()`
in place.

Commits on `small-fixes`:

| Commit | Scope |
|---|---|
| `85365a5` | Steps 1-7 land as types + contract changes. Render closure deleted, varmap engine shim deleted, Prepare drops the prompt arg, `prompt_hash` → `prompt_file`, Verify+Walk wired into Run. Most spec-described mechanisms exist as abstractions but are not yet load-bearing. |
| `207cc0f` | `@PATH` framing for `.prompt.md`; `--prompt` / `--management` flags removed. |
| `9856593` | Factory map for `--action X` in cmd/prompt.go; `--paths` routed through `PromptFile.Inspect`; `--raw` on exec/parallel; ALL_CAPS guardrail in `internal/runtime`. |
| `6d8e821` | Runner Replacer accepts dotted forms alongside ALL_CAPS; `defaults/runtime.hcl` swept. |
| `c2ea502` | Earlier (incorrect) version of this section claiming the spec landed. Superseded. |
| `f1312c3` | `test/Dockerfile.dind` pinned; runtime.hcl ALL_CAPS docs swept; `docker_exec` accepts dotted forms. |
| `6222bad` | `--paths` surfaces review's reports manifest without `--supervisor` (bug fix in a parallel path). |
| `10bb528` | `--supervisor` flag deleted; `BuildAssemblerVars` exec.* placeholders switched to dotted form. |
| Next-round step 1-3 | `runtimeVars` owns `exec.*` substitution against `ctx.Mode()` + Runtime fields. `flow.execute` populates `rt.{ExecID, Batch, OutputDir, OutputFile, PromptFile}` from `prepared` + RunOpts. `runner.go:494`'s prompt-substitution pass deleted. Load-bearing test `TestRunnerExecutePreparedDoesNotSubstitutePromptBody` pins the invariant. |
| Next-round step 4-5 | `dynamic.review_reports`, `dynamic.code_mgmt_review`, `dynamic.previous_report` shipped; `defaults/prompts/review.prompt.md`, `defaults/prompts/code_management.prompt.md`, `defaults/prompts/report/_post.previous_report.md` reference them. `reviewPrompt` wrapper struct deleted. |
| Next-round step 6 | `NewReviewBundle` / `NewCodeBundle` / `NewVerifyBundle` / `NewSingleSupervisorBundle` / `NewReportBundle` are the only composition path. `assembleReview` / `assembleSupervisor` / `assembleAction` / `assembleCodeManagementV1` / `assembleRoleReport` all deleted. `ateam prompt --action X` and `ateam <action>` share one factory. |
| Next-round step 7 | `assembleForInspection` → `inspectionDigestsForCurrentFlags`, body composition fully delegated to `inspectBundleForCurrentAction`. No parallel preview composition path. |
| Next-round step 8 | `(b *PromptBundle).ResolvePreview` / `InspectPreview` auto-load `BaseVars` + `Dynamics` + `ModePreview`. Boilerplate at every cmd-layer preview call site collapsed. |
| Next-round step 9 | Extracted `internal/promptdata` (AllRoleIDs / AutoRolesMarker / ProjectInfoParams / FormatProjectInfo / WriteOrgDefaults / RoleMeta / IsValidRole / ResolveRoleList / AllKnownRoleIDs / ParsePromptFrontmatter / embedded-defaults FS). Both `internal/root` and `internal/prompts` import promptdata; `prompts` now imports `root`. Bridge functions (`BuildEngine`, `ProjectInfoDynamic`, `NewInspectionContext`, `liveCtx`) moved from `root/engine.go` into `internal/prompts/env_bridge.go` as free functions. `ResolveContext.Env() *root.ResolvedEnv` is now part of the contract. Net -940 lines. |
| Next-round step 10 | `ateam exec` defaults to `prompts.PromptText` (variable + dynamic expansion); `--raw` opts into `RawTextPrompt`. `parallel` follows the same default. Load-bearing test `TestStaticBundle_PromptTextExpandsExecVars` pins the invariant. |

### What the spec set out to achieve that did NOT land

Original list of ten gaps recorded before the Next round. **All ten
now landed.** Left in place for historical context and as the
baseline against which the Next round's punt budget was set to zero.

1. **One substitution pass for the prompt body.** `runner.go:494` still
   does `prompt = ResolveTemplateString(prompt, tmplVars)` after
   `bundle.Prompt.Resolve` already produced the text. The two-pass
   mechanism the Problem section explicitly indicts is intact.
2. **One code path for preview and execution.** Review has THREE: live
   (`NewReviewBundle` → `bundle.Prompt.Resolve` → `reviewPrompt.Resolve`),
   body-only preview (`previewReview` → `assembleReview`), and
   `--paths` preview (`assembleForInspection` → `PromptFile.Inspect` +
   hand-added `addLive("reports", …)`). Each implements the
   reports-manifest composition independently.
3. **`{{AT RUNTIME:exec.<key>}}` sentinel pattern for `exec.*` in
   preview/verify modes.** Only `dynamic.project_info` uses the AT
   RUNTIME pattern. `exec.*` substitution doesn't consult `ctx.Mode()`
   at all.
4. **`flow.Runtime.{ExecID, Batch, OutputDir, OutputFile}`** declared on
   the struct, never read by any code path. Dead fields that make the
   design *look* implemented.
5. **`dynamic.review_reports` / `dynamic.code_mgmt_review`** — the
   spec's canonical mechanism for the reports manifest and review-content
   blocks. Neither exists. The reports manifest lives in a hand-written
   `reviewPrompt` wrapper struct, three times (live, preview, paths).
6. **`bundle.Vars` merge into `rt.Vars`** — field declared on
   `PromptBundle`, never read. Factory-curated args.* / roles.* values
   have no path to reach the engine.
7. **`assembleReview` / `assembleCodeManagementV1` / `assembleSupervisor`
   / `assembleRoleReport`** still exist. The factory pattern was
   supposed to make them disappear.
8. **`assembleForInspection`** still exists as a parallel composition
   path to `Prompt.Inspect`.
9. **`ResolveContext.Env() *root.ResolvedEnv`** spec-required method
   deferred citing the `root → prompts` import cycle. The cycle is
   real, the fix (extract helpers from `internal/prompts`) was punted.
10. **`ateam exec "..."` / `ateam parallel`** default semantics
    unchanged — still `RawTextPrompt`. Spec called for `PromptText` with
    engine expansion as the non-`--raw` default.

The next round of work is concentrated on these ten gaps. Per-step
notes in the implementation-sequence table further down record what
each commit actually shipped vs the spec text; those notes are honest
where this section's earlier wording was not.

## Next round: guardrails for finishing the design

Whoever picks this up — me again or anyone else — operates under the
following rules. They exist because the previous round normalized
punting and arrived at "abstractions exist; nothing is load-bearing."

### Authority

**The spec text in this file is authoritative.** Where the spec text
disagrees with the shipped code, the code is wrong unless an explicit
diff to the spec was negotiated in writing first.

If a step seems unimplementable as written:
1. Stop.
2. Quote the specific spec sentence that won't land cleanly.
3. Propose the smallest concrete divergence.
4. Wait for an explicit "ok, depart" from the user.
5. Update this spec to reflect the new contract in the same change.

Never quietly add a parallel path "to be consolidated later." Never
quietly skip a sentence. Never declare a step done with a comment that
calls itself a follow-up. **Each of those is what the previous round
called a "punt."** Ten of them, compounded, are why nothing in this
spec is actually load-bearing today.

### Punt budget

**Zero.** Every parallel path that survives a step is the step not
completing. Every helper added to wire around a spec mechanism that
doesn't yet work means fix the spec mechanism instead.

Concretely, in the next round:

- `previewReview` / `previewCodeManagement` / `previewVerify` get
  deleted, not extended.
- `env.BuildEngine` / `env.NewInspectionContext` / `env.ProjectInfoDynamic`
  get deleted (or moved into the spec's canonical dispatcher). They
  exist because `ctx.Vars()` / `ctx.Dynamics()` aren't load-bearing —
  fix that, not the symptom.
- `assembleForInspection` gets deleted, not refactored.
- `assembleReview` / `assembleCodeManagementV1` / `assembleSupervisor`
  / `assembleRoleReport` get deleted.
- The `reviewPrompt` wrapper struct gets deleted; its reports-manifest
  composition becomes `dynamic.review_reports` per spec.
- `runner.go::ExecutePrepared` stops calling `ResolveTemplateString`
  on the prompt body (it still resolves args / container fields,
  which don't pass through `Prompt.Resolve`).

If a deletion above looks impossible, that's the prompt to ask before
departing — not the prompt to ship a third parallel path.

### Tasks defined as deletions

For each remaining work item, write the task as a deletion of an
existing parallel path, not as an addition of a new helper. Example
shapes:

- ❌ "Migrate review's reports manifest to a dynamic."
- ✅ "Delete `reviewPrompt`. The factory's `bundle.Prompt` is
  `prompts.PromptFile{Path: 'review'}` with `dynamic.review_reports`
  registered. Tests assert byte-identity with the legacy
  `assembleReview` output."

- ❌ "Wire `flow.Runtime.ExecID` into substitution."
- ✅ "Delete `runner.go:494`'s prompt-substitution line. The runner
  Replacer no longer sees the prompt body. Tests assert that
  `bundle.Prompt.Resolve(rt)` with `rt.ExecID=42` renders
  `{{exec.id}}` to `42` directly."

The deletion target is what proves the spec mechanism is load-bearing.
Anything that grows without a deletion is a punt.

### Acceptance tests, written before the code

Each remaining step lands with a test that fails until the spec
mechanism *itself* works. The test must NOT be satisfiable by a
parallel path producing the same output. Concrete required tests:

| Step | Test invariant |
|---|---|
| One substitution pass | `grep -n 'ResolveTemplateString.*prompt' internal/runner/runner.go` produces zero hits inside `ExecutePrepared`. The runner Replacer only sees args / container fields. |
| Single preview/live code path | `cmd/prompt.go::runPromptAction` calls the same factory (e.g. `NewReviewBundle`) the live verb calls, and produces a body byte-identical to `ateam review --dry-run`'s resolved prompt. A test diffs the two. |
| `exec.*` sentinel in preview | A bundle whose prompt contains `{{exec.id}}` resolved against `rt` with `Mode == ModePreview` produces `{{AT RUNTIME:exec.id}}` in the output. |
| `exec.*` real value in live | The same bundle, resolved against `rt` with `Mode == ModeReal` and `rt.ExecID = 42`, produces `42`. The runner Replacer does NOT touch the prompt. |
| `Runtime.ExecID` etc. load-bearing | A test reads `rt.ExecID` / `rt.Batch` / `rt.OutputDir` / `rt.OutputFile` somewhere on the production code path. `grep` shows non-test consumers. |
| `bundle.Vars` merge | A bundle with `Vars: map[string]string{"args.x": "y"}` resolved against a prompt containing `{{args.x}}` produces `y`. |
| `dynamic.review_reports` | A bundle whose prompt body contains `{{dynamic.review_reports}}` and whose dynamics registers `review_reports` produces the same manifest the legacy `formatReportsBlock` would. `reviewPrompt` is deleted. |
| `dynamic.code_mgmt_review` | Same shape for code-management. `assembleCodeManagementV1` is deleted. |
| `ResolveContext.Env()` | `ctx.Env()` returns the resolved env. The `root → prompts` cycle is broken by extracting helpers (or by an explicitly negotiated divergence). |
| `ateam exec "..."` default expansion | A prompt containing `{{prompt.name}}` passed to `ateam exec` (no `--raw`) expands. With `--raw`, it doesn't. |

Each row is one or two assertions, not a full test file. They
collectively prevent the next round from drifting into the punt
pattern that produced this one.

### Mid-step check-ins

Whenever the phrase "I'll defer this to a follow-up" forms in your
head, that's the stop signal. Surface the specific deferral, quote the
spec sentence, and ask. Three of the previous ten punts compounded
because the call was made alone.

### Spec invariants pinned to the code

Where a load-bearing rule lands (e.g. "the runner does not substitute
the prompt body"), pin it as a comment adjacent to the code that
honors it — not just in this file. The next maintainer touching the
line should have to read the invariant.

### Order of operations for the next round

Driven by the deletion-first rule, the natural sequence:

1. Make `Vars.Resolve("exec", key)` consult `ctx.Mode()` and the
   `*flow.Runtime` fields. The substitution decision lives in a single
   resolver, not in `BuildAssemblerVars`'s hardcoded map.
2. Wire `flow.execute` to populate `rt.{ExecID, Batch, OutputDir,
   OutputFile}` from `prepared` before calling `Prompt.Resolve`. The
   four Runtime fields become load-bearing.
3. Delete `runner.go:494`'s prompt-substitution line. Args / container
   fields still go through `ResolveTemplateString`. Tests for shipped
   prompts run.
4. Implement `dynamic.review_reports` (and `dynamic.code_mgmt_review`).
   Update `defaults/prompts/review.prompt.md` (and code_management) to
   reference them.
5. Delete `reviewPrompt`. The review factory becomes
   `bundle.Prompt = prompts.PromptFile{Path: "review", PrePrompt:…,
   PostPrompt:…}` with `bundle.Dynamics["review_reports"] = …`.
6. Make `cmd/prompt.go::runPromptAction` call the same factory the
   live verb calls. Delete `previewReview` / `previewCodeManagement`
   / `previewVerify`. Delete `assembleReview` / `assembleSupervisor`
   / `assembleCodeManagementV1` / `assembleRoleReport`.
7. Make `--paths` / `--inline-paths` call `bundle.Prompt.Inspect(rt)`
   against the same factory. Delete `assembleForInspection`. Live
   sections (reports manifest, previous_report) flow through
   `Prompt.Inspect` via the same dynamics or via per-bundle wrappers
   the factory installs.
8. Merge `bundle.Vars` into `rt.Vars` in `flow.execute`. Test with a
   factory-exposed `args.*` value.
9. Extract the helpers from `internal/prompts` that `internal/root`
   needs (or break the dependency another way), then add
   `ResolveContext.Env()`.
10. Change `ateam exec` default to `PromptText`; `--raw` opts back into
    `RawTextPrompt`.

Each step ends with at least one parallel-path deletion. If a step
finishes with the new mechanism added but the legacy one still alive,
the step is not done.

### Estimate

300-500 LOC of consolidation, mostly deletions. The risk is not
technical — the abstractions are sound. The risk is discipline. The
guardrails above are how that risk is paid.

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
  bundle executes. Builds a Runtime with `Mode == ModePreview` and calls
  each bundle's `Prompt.Resolve(rt)`.
  Surfaces authoring errors as a batched `VerifyResult`.
- **Resolution.** Per bundle, in this order:
  1. `Executor.Prepare(opts)` allocates `exec_id`, log/runtime paths, and
     inserts the DB row with `prompt_file = .ateam/logs/<exec_id>/prompt.md`.
  2. Flow builds `Runtime` (Vars populated with real `exec.*` values,
     `prompt.*` from bundle metadata, dynamics map).
  3. `bundle.Prompt.Resolve(rt)` produces the final text. `rt.Mode()` is `ModeReal`. **This is a flow step, not an Executor method.**
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
| `Runtime` struct (implements `prompts.ResolveContext`) | `Prompt` interface, `ResolveContext` interface |
| `PromptBundle` | `RawTextPrompt`, `PromptText`, `PromptFile` impls |
| Lifecycle: `Run`, `Verify`, `Walk` | Resolver engine (`Expand`, anchor walk, `Assemble`) |
| `Executor` interface (`Prepare`, `ExecutePrepared`) | `Vars` type and `MapVars` impl |
| Re-exported type aliases (`Prompt`, `Vars`, `ResolveMode`, `Section`, …) so callers don't import both | `PromptDynamic`, `PromptDynamicFunction`, `Section` |

Flow defines *when* prompts resolve. The prompts package owns *how* they
resolve and *what types* describe them. Flow imports prompts and re-exports
the types its API mentions; the prompts package never imports flow.

The runner (`Executor`) does **not** participate in prompt resolution.
Its surface is `Prepare(opts)` + `ExecutePrepared(prepared, text)`. Flow
calls `bundle.Prompt.Resolve(rt)` between those two — resolution is a
flow step, not a runner method.

## Types

### `prompts.Prompt` (the interface)

The `Prompt` interface and everything it touches lives in the prompts
package — flow consumes it but does not own the type. This avoids the
import-cycle that would arise if `prompts` had to import `flow` to talk
about Runtime / ResolveMode.

```go
package prompts

// ResolveContext is what Prompt.Resolve and dynamics receive. Flow's
// Runtime implements it; tests can construct stubs. Lives in prompts so
// the package never imports flow.
type ResolveContext interface {
    Env() *root.ResolvedEnv
    Vars() Vars
    Mode() ResolveMode
    Dynamics() PromptDynamic
}

// _Implementation note (commit `85365a5`):_ `Env()` is intentionally
// deferred — `internal/root` already imports `internal/prompts` for
// `prompts.FormatProjectInfo`, `AllRoleIDs`, etc., so the reverse
// dependency would cycle. The shipped `ResolveContext` exposes
// `Vars()`, `Mode()`, `Dynamics()`. No shipped dynamic needs Env access
// today; the method will be added once the helpers in `internal/prompts`
// that root depends on are extracted to a lower package.

type Prompt interface {
    // Resolve produces the final prompt text. ctx.Mode() controls whether
    // runtime-only values (exec.*) substitute real data or sentinels.
    Resolve(ctx ResolveContext) (string, error)

    // Inspect returns per-section provenance for --paths / --inline-paths.
    // Returns (nil, nil) when the Prompt has no section structure (literal
    // text). Always preview-style — Inspect is for human display, not live
    // execution. Callers pass a preview-mode ctx by convention.
    Inspect(ctx ResolveContext) ([]Section, error)
}

type ResolveMode int
const (
    ModeReal    ResolveMode = iota  // real exec.* values; missing required key errors
    ModePreview                      // sentinels for runtime-only keys
)
```

Flow re-exports the types its API surface mentions so callers don't have
to import both:

```go
package flow

type (
    Prompt         = prompts.Prompt
    ResolveContext = prompts.ResolveContext
    ResolveMode    = prompts.ResolveMode
    Section        = prompts.Section
    Vars           = prompts.Vars
    PromptDynamic  = prompts.PromptDynamic
)

const (
    ModeReal    = prompts.ModeReal
    ModePreview = prompts.ModePreview
)
```

`ModeVerify` is **not** a third mode — verification is a caller pattern:
"call `Resolve(ctx, where ctx.Mode()==ModePreview)` on every bundle and
accumulate errors." The impl's behavior is identical in verify and preview
contexts.

### `flow.Runtime`

The per-invocation context. Holds everything needed across the lifecycle,
including the per-bundle data Prompt impls consume. Implements
`prompts.ResolveContext`:

```go
type Runtime struct {
    DB       *calldb.CallDB
    Env      *root.ResolvedEnv
    WorkDir  string
    Vars     Vars            // built by flow, opaque to flow
    Dynamics PromptDynamic
    Mode     ResolveMode

    // Per-bundle runtime values populated by Prepare. Zero values when
    // Mode == ModePreview (sentinels filled by Vars instead).
    ExecID     int64
    Batch      string
    OutputDir  string
    OutputFile string
}

// Satisfy prompts.ResolveContext.
func (r *Runtime) Env() *root.ResolvedEnv      { return r.Env }
func (r *Runtime) Vars() Vars                  { return r.Vars }
func (r *Runtime) Mode() ResolveMode           { return r.Mode }
func (r *Runtime) Dynamics() PromptDynamic     { return r.Dynamics }
```

(In practice the methods need different names from the fields to avoid Go
shadowing — e.g. `vars`, `mode` etc. fields with `Vars()`, `Mode()`
methods. Sketch above is for clarity.)

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
    // Path is interpreted in one of two ways depending on its shape:
    //
    //   1. Logical name — no path separator, no ".prompt.md" suffix.
    //      Examples: "review", "code", "report/security".
    //      Resolved via the standard anchor walk (project → org → embedded).
    //      The framing fragments compose around the role main found in that
    //      chain.
    //
    //   2. Filesystem path — ends in ".prompt.md", contains a path separator
    //      or is absolute. Examples: "/tmp/foo.prompt.md",
    //      ".ateam/prompts/foo.prompt.md".
    //      Resolved by injecting the file's parent dir as a temporary
    //      anchor at the front of the chain. Sibling fragments
    //      (<basename>.pre.*.md, dir-level _pre.*.md in that dir) compose;
    //      standard anchors still apply for inherited framing.
    //
    // Detection rule: filesystem path ⇔ Path ends in ".prompt.md" AND
    // contains either a "/" or starts with ".". Everything else is a
    // logical name.
    Path string

    PrePrompt  string  // optional --pre-prompt content
    PostPrompt string  // optional --post-prompt content
    CustomBody string  // optional override of the role main
    // Future impls (JinjaPrompt, RemotePrompt, …) hold their own state in
    // their struct fields the same way.
}
```

_Implementation note (commit `85365a5`):_ the shipped `PromptFile` also
carries `Assembler *assembler.Assembler` and `Vars assembler.Vars` —
factory-injected at bundle construction. The spec's eventual shape lifts
both to the `ResolveContext` so the bundle's Prompt is just `{Path, Pre,
Post, CustomBody}`. They live on the struct today so the Step-4 migration
could land without first restructuring `flow.Runtime`'s vars builder; a
future cleanup moves them to `ResolveContext`.

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

    Prompt   Prompt          // built by the factory; self-contained
    Vars     map[string]string // factory-curated args.* / roles.* / action.* values
    Dynamics PromptDynamic    // factory-supplied dynamics, copied into rt at resolve time

    RunOpts  func(RuntimeEnv) runner.RunOpts
    PreExec, PostExec []Action
}
```

`Render` is gone. Framework builds a Runtime, merges `bundle.Vars` into
`rt.Vars`, sets `rt.Dynamics` from `bundle.Dynamics`, then calls
`bundle.Prompt.Resolve(rt)` at the moment it needs the text.

_Implementation note (commit `85365a5`):_ `Dynamics` was added to the
struct alongside `Prompt`/`Vars` so each factory can declare exactly
which dynamic functions its prompt expects (e.g. the review factory
registers `project_info`). Without per-bundle Dynamics, the rt would
need a global registry — which the spec explicitly avoids (see
"Dynamic functions" above).

**`Vars` is the canonical home for factory-curated values.** Prompt impls
(`PromptFile`, `PromptText`, etc.) stay focused on prompt source/framing.
They don't carry CLI-derived state — that belongs at the bundle level so
the same Prompt type can be reused across factories with different
exposed values.

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
    ctx ResolveContext, args ...string,
) (string, error)

type PromptDynamic map[string]PromptDynamicFunction
```

`ctx` carries everything dynamics need (Vars, Mode, Env). The interface is
defined in the prompts package; flow.Runtime satisfies it. No import cycle.

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

**Dynamics that depend on generated artifacts** (e.g., `dynamic.review_reports`
reading `shared/report/*.md` produced by an earlier `ateam report` run, or
`dynamic.code_mgmt_review` reading `shared/review.md` produced by
`ateam review`) **return preview sentinels in `ModePreview` rather than
proving runtime availability.** Verification is best-effort for these —
the engine catches authoring errors but cannot validate that an upstream
phase will have produced its output by the time the dependent bundle runs.
Each such dynamic's docs make the dependency explicit.

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
For each bundle, Verify builds a Runtime with `Mode == ModePreview`,
merges `bundle.Vars`, and calls `bundle.Prompt.Resolve(rt)`. Errors batched
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
    return &flow.PromptBundle{
        Name: "review", Role: "supervisor", Action: runner.ActionReview,
        Prompt: &prompts.PromptFile{
            Path:       "review",
            PrePrompt:  opts.PrePrompt,
            PostPrompt: opts.PostPrompt,
        },
        Vars: map[string]string{
            "args.no_project_info": boolStr(opts.NoProjectInfo),
            "roles.selected":       strings.Join(opts.SelectedRoles, " "),
            // ...positive-listed; nothing else from opts leaks into args.*
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
rt := buildRuntime(env, ModePreview)  // sentinel exec.*; merges bundle.Vars
text, err := bundle.Prompt.Resolve(rt)
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

**Reading the table:** steps 1-2 add types but nothing uses them yet.
Step 3 reshapes the runner contract — required before verbs can migrate to
`Prompt.Resolve` because the new contract is what allows resolution to see
the real `exec.id`. Steps 4-5 port verbs to the new flow one at a time;
during this period `PromptBundle.Render` is gone for migrated verbs and
still present for the rest. Step 6 (ALL_CAPS drop) waits until every verb
is on the new contract because ALL_CAPS substitution depends on the old
two-pass mechanism.

| # | Status | Commit | Behavior change | Risk |
|---|---|---|---|---|
| 1 | ✅ | `85365a5` | Engine extensions: `PromptDynamic`, `{{ns.key ? default}}`, `{{include path ? TEXT}}`, quoted-arg parser, `ResolveContext` interface | None | Low |
| 2 | ✅ | `85365a5` | Add `flow.Runtime`, `prompts.{Prompt, RawTextPrompt, PromptText, PromptFile}` types. Coexists with today's `Render` (nothing uses the new types yet) | None | Low |
| 3 | ✅ | `85365a5` | Reshape `Executor` interface to `Prepare(opts)` + `ExecutePrepared(prepared, text)` — drop the prompt arg from Prepare. Prompt resolution happens in flow between the two calls, via `bundle.Prompt.Resolve(rt)`. Replace `prompt_hash` DB column with `prompt_file`. Old `Render` path keeps working through a compat shim in flow that does the in-between resolve step internally. _Render-time failure after Prepare allocates an exec_id now closes the orphan row via `calldb.MarkExecFailed`._ | Change #10 | **High** (runner contract change) |
| 4 | ✅ | `85365a5` | Migrate one verb end-to-end (suggest `review`) — factory + PromptFile + new Executor flow. `PromptBundle.Render` deleted for this verb. _The reports manifest + bundled bodies + `--post-prompt` tail live in a `reviewPrompt` wrapper around PromptFile so framing order stays byte-identical._ | None observable | Medium |
| 5 | ✅ | `85365a5` | Migrate remaining verbs (`code`, `verify`, `auto_setup`, `code_management` supervisor, `report` per-role, `inspect --auto-debug`, `exec`, `parallel`). Render compat shim deleted once all verbs migrated. _`staticBundle` now uses `prompts.RawTextPrompt`; `inspect --auto-debug` continues to use `runner.Execute` directly (it never went through PromptBundle)._ | None observable | Medium |
| 6 | ✅ | `85365a5`, `9856593`, `6d8e821` | Drop ALL_CAPS legacy + sweep `defaults/prompts/` + `{{project.info}}` → `{{dynamic.project_info}}` migration + ALL_CAPS detection-error in `runtime.hcl`. _Engine-side: `varmap.go` deleted, undotted tokens pass through verbatim. `defaults/runtime.hcl`'s lone runner-side template (`docker_container`) flipped to `{{container.name}}`; the runner Replacer now accepts every dotted form alongside its ALL_CAPS alias, so back-compat is preserved. The "detection error" landed as `internal/runtime/allcaps_check.go` which warns on user-level files referencing unknown ALL_CAPS tokens (embedded defaults skip the check). `env.BuildEngine` and `env.ProjectInfoDynamic` are the new canonical surfaces every cmd-layer assembly site uses._ | Changes #4, #6 | **High** (test sweep) |
| 7 | ✅ | `85365a5` | Wire verification into `flow.Run` (`Walk` callback, batched `VerifyResult`). _`Step` interface gained `Walk`; `Verify` runs `Prompt.Resolve` in `ModePreview` with panic recovery; `Run` short-circuits via `failedWithVerifyErrors` when the pass returns errors._ | Change #7 | Medium |
| 8 | ✅ | `207cc0f`, `9856593` | Rewrite `cmd/prompt.go` against factory dispatch; `--supervisor` deprecation warning; `--paths` / `--inline-paths` rewire to `Prompt.Inspect()`; `--raw` flag on `exec` / `parallel` / `prompt`. _Landed in two passes: the deprecation warning + `--raw` on `prompt` shipped in `207cc0f`; the factory map (`promptFactories`), `--raw` on `exec`/`parallel`, and the `PromptFile.Inspect` rewire via `env.NewInspectionContext` shipped in `9856593`. `--raw` on exec/parallel is forward-compat plumbing — both verbs already feed prompts byte-for-byte to the agent via `RawTextPrompt`, so the flag is a no-op until a future change moves expansion to the default._ | Changes #1, #2, #6, #8, #11 land here | Medium |
| 9 | ✅ | `207cc0f` | `@PATH/foo.prompt.md` outside-anchor framing. _Implemented in `cmd/prompt.go::runPromptExternalFile`: parent dir injected as the front anchor, base name as the role; standard anchors still contribute inherited framing. `--raw` short-circuits before any framing._ | Change #3 | Low |
| 10 | ✅ | `207cc0f` | Remove `ateam review --prompt` and `ateam code --management` flags. _`reviewCustomPrompt` / `codeManagement` vars gone; `COMMANDS.md` updated; operators override the supervisor body by placing `review.prompt.md` / `code_management.prompt.md` under `.ateam/prompts/` or `.ateamorg/prompts/`._ | Change #9 | Low |

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

This list is the **post-spec-completion** queue: ideas worth considering
once the design above is actually load-bearing. The "Next round"
section is where the in-flight gaps live; do not duplicate them here.

- **ALL_CAPS template alias removal** from the runner Replacer (commit
  `6d8e821` left them in as back-compat aliases). Prune once nothing
  depends on the ALL_CAPS aliases and tighten the `allcaps_check`
  warning into an error.
- **`{{shell CMD}}`** — defer until we have a read-only sandbox.
- **Executable prompts (`#!/usr/bin/env python` + `.prompt.py`)** —
  defer; the three-mechanism model handles every current use case.
- **`ateam migrate prompts` command** — re-evaluate if user prompts ever
  proliferate enough that manual migration becomes painful.
