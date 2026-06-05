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
| Post-step-10 cleanups | (1) `PromptBundle.Vars` field deleted (dead — never read). (2) `ateam prompt @PATH` now routes through `prompts.PromptText` instead of calling `prompts.BuildEngine().Render()` directly. (3) `applyPromptBatchOverride` + `--batch` flag on `ateam prompt` deleted; `{{exec.batch}}` is a runtime value like `exec.id`, filled at exec time by the runner. (4) `PromptFile.Assembler` / `PromptFile.Vars` fields removed; both sourced from `ctx.Env().Assembler()` and `ctx.Vars()`. (5) flow's `prompts.X` type re-exports dropped. |

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
| `args.*` / `roles.*` namespaces | A bundle whose BaseVars carries `args.batch: "abc"` resolved against a prompt containing `{{args.batch}}` produces `abc`. Same for `roles.*` — see `internal/prompts/assembler/varmap.go` for the namespace allow-list. |
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
8. Provide `bundle.ResolvePreview(env, workDir)` and
   `bundle.InspectPreview(env, workDir)` helpers so every preview /
   `--paths` callsite auto-loads `BaseVars + Dynamics + ModePreview`
   without per-caller boilerplate.
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
| `Executor` interface (`Prepare`, `ExecutePrepared`) | `Vars` type, `PromptDynamic`, `PromptDynamicFunction`, `Section` |

Flow defines *when* prompts resolve. The prompts package owns *how* they
resolve and *what types* describe them. Flow imports prompts; callers
reference `prompts.X` directly (no re-exports). The prompts package
imports `root` for `ResolvedEnv` (cycle broken in step 9 by extracting
data helpers into `internal/promptdata`).

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

Flow does NOT re-export the prompts types — callers reference
`prompts.Prompt`, `prompts.ResolveContext`, `prompts.ModePreview`, etc.
directly. flow's public API mentions them by fully-qualified name. (An
earlier draft used type aliases; the indirection bought nothing and
made flow's surface a passthrough that drifted whenever the prompts
package changed.)

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
    DB      *calldb.CallDB
    env     *root.ResolvedEnv  // private; access via Env()
    WorkDir string

    vars     prompts.Vars
    mode     prompts.ResolveMode
    dynamics prompts.PromptDynamic

    // Per-bundle runtime values populated by Prepare. In ModePreview
    // these stay zero — runtimeVars renders the AT RUNTIME sentinel
    // instead. In ModeReal, a zero load-bearing field is a wiring bug
    // and surfaces as an error.
    ExecID     int64
    Batch      string
    OutputDir  string
    OutputFile string
    PromptFile string
    // (plus Timestamp / Profile / Agent / Model / Effort /
    // MaxBudgetUSD / SubRunArgs / DebugContext /
    // AutoRolesCommandsOutput — empty-OK in ModeReal until a verb
    // wires them.)
}

// Satisfy prompts.ResolveContext. Vars() returns &runtimeVars{rt},
// which dispatches exec.* against the typed fields above and falls
// through to rt.vars for every other namespace.
func (r *Runtime) Env() *root.ResolvedEnv         { return r.env }
func (r *Runtime) Vars() prompts.Vars             { return &runtimeVars{rt: r} }
func (r *Runtime) Mode() prompts.ResolveMode      { return r.mode }
func (r *Runtime) Dynamics() prompts.PromptDynamic { return r.dynamics }
```

The exec.* namespace is the typed half of the resolver: a closed key
set with `requireExec` validation in ModeReal. Other namespaces are a
plain map carrier (`BaseVars`) — no typed access needed at runtime.

**Decision recorded:** the typed fields stay on the Runtime struct
itself (currently ~18 scalars including the verb-supplied
empty-OK ones). A previous review proposed factoring them into a
`RunContext` substruct so the top-level shape is smaller. That was
considered and declined — every field is on a hot read path during
`Prompt.Resolve`, and the substruct only renames the indirection
without removing a layer. Promoting it would also force every
factory test fixture to grow a level. The 18-field struct is the
accepted shape; new verb-supplied keys land here.

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

`PromptFile` is the spec-clean shape: just `{Path, PrePrompt,
PostPrompt, CustomBody}`. The assembler and the variable resolver come
from `ctx.Env().Assembler()` and `ctx.Vars()` at resolve time — no
factory injection.

All three implement `prompts.Prompt`. Each holds whatever state it needs
in its own fields. New impls (Jinja, remote-fetched, etc.) just satisfy
the interface — no registration, no plugin system.

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

    Prompt   prompts.Prompt        // built by the factory; self-contained
    BaseVars prompts.Vars          // env-derived resolver for non-exec.* namespaces
    Dynamics prompts.PromptDynamic // factory-supplied dynamics, copied into rt at resolve time

    RunOpts  func(RuntimeEnv) runner.RunOpts
    PreExec, PostExec []Action
}
```

`Render` is gone. flow.execute builds a Runtime, sets `rt.SetVars(b.BaseVars)`
and `rt.SetDynamics(b.Dynamics)`, then calls `bundle.Prompt.Resolve(rt)`
at the moment it needs the text.

`BaseVars` is built by the factory from `env.BuildAssemblerVars(...)` and
carries `prompt.*`, `project.*`, `git.*`, `container.*`, `ateam.*`,
`role.*`, `args.*`, `roles.*`. The `exec.*` namespace is handled by
`runtimeVars` against typed fields on Runtime — never goes through
BaseVars.

`Dynamics` lives on the bundle so each factory can declare exactly which
dynamic functions its prompt expects (e.g. the review factory registers
`project_info` + `review_reports`). Without per-bundle Dynamics, the rt
would need a global registry — which the spec explicitly avoids (see
"Dynamic functions" above).

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
{{dynamic.bar a b c}}                    → 3 args = ["a", "b", "c"]
```

**Includes follow the same rule.** Authors quote when the expanded value
can contain whitespace: `{{include? "{{args.report_path}}"}}`. Same
discipline as a shell command. The engine doesn't try to be clever.

Note: shipped dynamics (`project_info`, `review_reports`,
`code_mgmt_review`, `previous_report`) currently ignore positional
args — the filtering / lookup state they need lives in the factory
closure, not the prompt body. Authors who want positional-arg
dispatch should see the Future section.

### Legacy ALL_CAPS — dropped from the prompt-body engine

The prompt-body resolver supports the dotted form only. ALL_CAPS
tokens — `{{ROLE}}`, `{{BATCH}}`, `{{EXEC_ID}}`, `{{OUTPUT_DIR}}`,
`{{SOURCE_DIR}}`, etc., the full `varmap.go::VarRenameMap` set — are
not recognized. They will:

1. **Fail at render time** with the engine's unknown-namespace
   passthrough or known-namespace unknown-key error, depending on
   whether the legacy token happens to match a current namespace
   prefix. Either way the operator sees a loud error, not a silent
   empty substitution.
2. **Be surfaced as warnings by the migrator** when it moves a file
   into `prompts/`. `internal/migrate/legacy_tokens.go` scans
   migrated bodies for the closed legacy set and appends a per-token
   warning naming the dotted replacement.

There is **no automated rewrite** and **no compat shim**. The
migrator does not edit prompt bodies, and the engine does not
forgive the legacy form. Operators see the warning, edit by hand,
and move on. The affected population is small (only existing user
prompts predating the bundle-aware refactor); built-in defaults are
clean.

**Separate, unchanged: runtime.hcl ALL_CAPS.** Agent CLI args and
container fields in `runtime.hcl` still substitute ALL_CAPS
placeholders (`{{EXEC_ID}}`, `{{OUTPUT_DIR}}`, …) via
`runner.TemplateVars.Replacer()`. That is a separate substitution
pass on a separate config surface — it is not part of the
prompt-body engine and is not affected by this policy. Do not
conflate the two.

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
sets `rt.SetVars(b.BaseVars)` and `rt.SetDynamics(b.Dynamics)`, and calls
`bundle.Prompt.Resolve(rt)`. Errors batched per decision.

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
func NewReviewBundle(in ReviewBundleInput) *flow.PromptBundle {
    return &flow.PromptBundle{
        Name: "review", Role: "supervisor", Action: runner.ActionReview,
        Prompt: prompts.PromptFile{
            Path:       "review",
            PrePrompt:  in.PrePrompt,
            PostPrompt: in.PostPrompt,
        },
        BaseVars: in.Env.BuildAssemblerVars("review", "the supervisor", "review"),
        Dynamics: prompts.PromptDynamic{
            "project_info":   prompts.ProjectInfoDynamic(in.Env, "the supervisor", "review"),
            "review_reports": reviewReportsDynamic(in.Env, ...),
        },
        RunOpts: ..., PreExec: ..., PostExec: ...,
    }
}
```

`BuildAssemblerVars` populates `prompt.*` / `project.*` / `git.*` /
`ateam.*` / `args.*` / `roles.*`. Factories that need to expose
operator-supplied scalars (`args.batch`, `roles.enabled`, etc.) merge
them into the map before handing it to the bundle.

CLI dispatch for `ateam prompt --action X`: a small map in `cmd/` is the
only place all action names appear together. Unknown action → fallback to
`PromptFile{Path: action}` so any `.ateam/prompts/<name>.prompt.md` works
without a factory entry.

## `ateam prompt` behavior

Same machinery as live runs with `Mode == ModePreview`:

```go
bundle := promptFactories[promptAction](env, ...)
text, err := bundle.ResolvePreview(env, env.WorkDir)
fmt.Println(text)
```

`ResolvePreview` builds the Runtime, auto-loads `BaseVars + Dynamics +
ModePreview`, and calls `bundle.Prompt.Resolve(rt)`. A typo surfaces
the same engine error verify would. `--paths` / `--inline-paths` use
`bundle.InspectPreview(env, env.WorkDir)` instead.

## Assembler / PromptFactory split

This section is a buildable design — an agent picking this up should be
able to implement it without further conversation. Status: not yet
implemented. Lands independent of any script-Prompt support; cleans up
existing code on its own merits.

### Why split

`prompts.PromptFile` today fuses three concerns:

1. **Look up by name** — given `"review"`, walk project → org → embedded anchors and find `review.prompt.md` plus any `*.pre.*.md` / `_pre.*.md` / `*.post.*.md` / `_post.*.md` framing fragments.
2. **Compose framing slots** — order the discovered files into `root_pre` → `dir_pre` → `role_main` → `role_post` → `dir_post` per the assembly rules.
3. **Render bodies** — for each file, run its content through the template engine (variable expansion, dynamics dispatch, include directives) and concatenate.

These three responsibilities are independent. Today they're hardcoded inside `assembler.Assemble`. Splitting them buys:

- **Clean Prompt-impl plug-in.** A future `PythonPrompt` / `JinjaPrompt` / `RemotePrompt` needs to swap (3) without touching (1) or (2). The current `PromptFile` doesn't let it.
- **Multiple lookup strategies.** The existing `IsFilesystemPromptPath` predicate inside `PromptFile` (which toggles temp-anchor injection when `Path` ends in `.prompt.md` and contains a `/`) is a one-off special case. With the split, it becomes a separate `TempAnchorAssembler` and the special case dies.
- **Single-file rendering.** Operators who want "this one file, no framing, but with engine expansion" today get `PromptText` — which can't reference includes against the standard anchor chain. A `BasicAssembler` covers this without expanding `PromptText`'s contract.

### Types

Two new interfaces live in `internal/prompts/assembler` (alongside the existing `Assembler` struct, which gets demoted to an impl):

```go
// ResolvedFile is one entry in an Assembler's ordered output. It carries
// what the renderer needs to read and the metadata --paths / Inspect
// consume.
type ResolvedFile struct {
    Slot   string  // root_pre | dir_pre | role_main | role_post | dir_post
    Anchor string  // project | org | embedded | external | impl-defined
    Path   string  // anchor-relative
    FS     fs.FS   // FS to read content from
}

// Assembler resolves a logical name (or filesystem path) into the
// ordered file list that composes the assembled prompt. It owns the
// lookup strategy; it does NOT own rendering.
//
// FindOrphans is unchanged from today — the inspection path consumes
// it directly.
type Assembler interface {
    Resolve(name string) ([]ResolvedFile, error)
    FindOrphans() ([]Orphan, error)
    Anchors() []Anchor  // exposed for callers that need to inspect the chain
}
```

A new interface lives in `internal/prompts`:

```go
// PromptFactory picks the Prompt implementation for a role-main file.
// Framing fragments (slots != "role_main") are always rendered by the
// engine — they're documented to be markdown. Only the role main can
// vary.
//
// Today: DefaultPromptFactory returns prompts.PromptText{Text: <body>}
// for every file (which is what the current assembler does inline).
// Future: a registry maps `.prompt.py` → PythonPrompt, etc.
type PromptFactory interface {
    For(path string) Prompt
}
```

### Concrete impls

Three Assembler implementations cover every shape today's code uses, plus the temp-anchor edge case:

| Impl | What it does | Replaces |
|---|---|---|
| `MultiAnchorAssembler` (today's `*Assembler`, renamed) | Walks project → org → embedded for `<name>.prompt.md` + slot composition. | Current `(*Assembler).Assemble` lookup path. |
| `TempAnchorAssembler{Inner, ExternalDir}` | Wraps another Assembler. Injects `ExternalDir` as a "external" anchor at the front of `Inner`'s chain, then delegates. | `IsFilesystemPromptPath`-driven branch inside `PromptFile.assemble`. |
| `BasicAssembler{FS, Path}` | Returns exactly one `ResolvedFile` with `Slot: "role_main"`. No framing, no anchor walk. | The cmd-layer one-off where an operator wants a literal file rendered with engine expansion but no framing. |

`DefaultPromptFactory()` is a single function in `internal/prompts`. It returns the same `Prompt` impl for every path (today: `PromptText`). New impls register as needed; until they do, the default covers every shipped use.

### Updated `PromptFile`

`PromptFile` becomes the thin orchestrator:

```go
type PromptFile struct {
    Path       string
    PrePrompt  string
    PostPrompt string
    CustomBody string

    // Optional. nil means "use ctx.Env().Assembler() and
    // DefaultPromptFactory" — the current default behavior.
    Assembler PromptAssembler  // see naming note below
    Factory   PromptFactory
}

func (p PromptFile) Resolve(ctx ResolveContext) (string, error) {
    a := p.Assembler
    if a == nil {
        a = ctx.Env().Assembler()  // backward-compat fall-through
    }
    files, err := a.Resolve(p.Path)
    if err != nil { return "", err }

    fac := p.Factory
    if fac == nil {
        fac = DefaultPromptFactory()
    }

    out := make([]string, 0, len(files))
    for _, f := range files {
        body, err := readFile(f.FS, f.Path)
        if err != nil { return "", err }

        var text string
        if f.Slot == "role_main" {
            // Per-file Prompt impl owns role-main rendering.
            text, err = fac.For(f.Path).Resolve(ctx)
        } else {
            // Framing fragments always go through the engine — they're
            // markdown by convention.
            text, err = renderEngine(body, ctx)
        }
        if err != nil { return "", err }
        out = append(out, text)
    }
    return wrap(p.PrePrompt, join(out), p.PostPrompt), nil
}
```

Naming note: keep the `Assembler` name in the prompts package; rename the existing concrete type in `assembler/` to `MultiAnchorAssembler` (or `ChainAssembler`). The interface and the concrete impl shouldn't share the bare name.

### CLI dispatch update

`cmd/exec_bundle.go::buildArgPrompt` currently uses `prompts.IsFilesystemPromptPath` to toggle the temp-anchor case inside `PromptFile`. With the split, the cmd-layer picks the Assembler explicitly:

```go
func buildArgPrompt(arg, pre, post string, raw bool) (prompts.Prompt, error) {
    if !raw && strings.HasPrefix(arg, "@") && !strings.HasPrefix(arg, "@-") {
        cleanPath := strings.TrimPrefix(arg, "@")
        if strings.HasSuffix(cleanPath, ".prompt.md") && (strings.ContainsRune(cleanPath, '/') || strings.HasPrefix(cleanPath, ".")) {
            // Filesystem-path .prompt.md: temp-anchor injection on
            // top of the standard chain.
            return prompts.PromptFile{
                Path:       cleanPath,
                PrePrompt:  pre,
                PostPrompt: post,
                Assembler:  assembler.TempAnchor(parentOf(cleanPath)),  // wraps ctx.Env().Assembler() at resolve time
            }, nil
        }
    }
    // body + concat path stays as-is for non-.prompt.md
    ...
}
```

`prompts.IsFilesystemPromptPath` is deleted. The predicate moves into a single conditional at the cmd layer; the prompts package stops exporting a predicate that's really a cmd-layer dispatch rule.

### Inspection / `--paths`

`PromptFile.Inspect` follows the same shape — walks `a.Resolve(p.Path)`, returns one `Section` per `ResolvedFile`. The existing `Section` struct already matches `ResolvedFile`'s data.

### Migration plan

1. Define `assembler.PromptAssembler` interface + `ResolvedFile`.
2. Rename today's concrete `assembler.Assembler` to `assembler.MultiAnchorAssembler`; keep `Anchors() []Anchor` and `FindOrphans()`. The renamed type satisfies the new interface trivially.
3. Add `TempAnchorAssembler{Inner, ExternalDir}` — a wrapper that prepends an `external` anchor and delegates `Resolve` / `FindOrphans`.
4. Add `BasicAssembler{FS, Path}` — single-file Resolve.
5. Define `prompts.PromptFactory` + `DefaultPromptFactory()`.
6. Add optional `Assembler` + `Factory` fields to `PromptFile`. Implement the orchestrator loop with nil-fall-through to the existing behavior.
7. Update `cmd/exec_bundle.go::buildArgPrompt` to inject `TempAnchorAssembler` explicitly; remove `prompts.IsFilesystemPromptPath`.
8. Sweep callers that construct `assembler.New(...)` → produce a `MultiAnchorAssembler` instance. `env.Assembler()` returns one.

### Tests

- `MultiAnchorAssembler`: existing `assembler.Assemble` tests retarget here (they already cover anchor walk + slot ordering).
- `TempAnchorAssembler`: pin that `Resolve("./foo.prompt.md")` returns the `foo.prompt.md` from `ExternalDir` as `role_main` plus the inherited `dir_pre` / `dir_post` from the wrapped chain.
- `BasicAssembler`: pin that `Resolve(path)` returns exactly one `role_main` entry.
- `DefaultPromptFactory`: pin that it returns `PromptText` regardless of path.
- `PromptFile` orchestrator: pin that a synthetic two-file Assembler (one `dir_pre`, one `role_main`) renders correctly with a fake Factory that returns a literal-string Prompt for `role_main`.
- All existing `internal/prompts/prompt_test.go` and `cmd/prompt_test.go` cases keep passing with default behavior.

### Out of scope

- No new Prompt impls. `DefaultPromptFactory` is the only factory shipped with the split.
- No URL-fetched or git-ref Assemblers. They're future work; the split makes them possible without further architecture.
- No changes to `flow.PromptBundle`, `ResolvePreview`, or the runtime resolver.

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

## Future

Items the design accommodates but ships without — each is well-scoped
enough that future work can land it without re-litigating the
architecture. Until they ship, prompts referencing the listed keys /
forms error at render time (closed-set resolver). Operators see a
loud message; nothing renders empty.

### Positional-arg dispatch for dynamics

Spec line 580 + arg-parsing rules (line 640) describe a contract where
each dynamic accepts a positional arg list:

```
{{dynamic.review_reports {{roles.selected}}}}
  → reviewReportsDynamic(ctx, "security", "dependencies", "code.bugs")
```

Today every shipped dynamic ignores its args — the filtering /
lookup state lives in the closure the factory captures (e.g. the
review selector on `reviewReportsDynamic`). Promoting the args
contract requires:

1. Each dynamic that filters / looks up state moves the state from
   closure capture to args.
2. Factories stop pre-filtering; the prompt body controls scope by
   passing `{{roles.selected}}` (or equivalent) as an arg.
3. New tests pin the arg-driven behavior per dynamic.

The canonical first consumer is `review_reports` — operators with a
multi-bundle review pipeline want to render different role subsets
inline without per-bundle factory variants.

### `roles.*` namespace coverage

Resolver ships `roles.enabled` (one key). The spec also lists
`roles.all`, `roles.disabled`, `roles.selected`, `roles.failed`,
`roles.aged_out` (line 544-549). Referencing any of these today
errors as unknown-key in the `roles` namespace. Wiring them is
straightforward — each is a `[]string` already available somewhere
(config, calldb, ReviewSelector) — but every key adds a stability
commitment to prompt authors, so they should land only when a real
prompt asks for them.

### `args.*` namespace coverage

Resolver ships `args.ignore_previous_report` (one key, wired into
the report factory). The spec calls for the verb's CLI surface to
expose specific scalars: `args.no_project_info`, `args.roles`,
`args.batch`, etc. Same rule as above — closed set, unknown keys
error, factories add entries on demand. The path is one line per
factory; the gate is "does any shipped prompt reference it".

### `{{ns.key ? default}}` default-substitution form

Spec table line 638 promises `{{ns.key ? default}}` renders `default`
when the value is empty (typos still error). The engine doesn't
implement this today. Authors needing it use `{{include? path ?
TEXT}}` for files; for variables there is no fallback shorthand. The
implementation slots into the existing engine token parser; the
test surface is small.

### Verify error dedup

Spec line 719: "Errors batched. Dedup by `(file, line, message)`
before printing." Shipped `flow.Verify` batches per-bundle but does
NOT dedup — three bundles failing with byte-identical messages emit
three `VerifyError` entries. Pinned by
`TestVerify_DoesNotDedupIdenticalErrorsAcrossBundles`. Promoting the
dedup means adding a dedup map keyed on the error string (or the
underlying `engine.Error`'s file/line) in `flow.Verify` and flipping
that test's expectation; ~10 lines.

### Script-Prompt impls (`.prompt.py`, `.prompt.sh`, `.prompt.jinja`, …)

The Assembler / PromptFactory split (see main spec) is the
prerequisite that makes this future-friendly. Once it lands, adding
a Prompt impl for an additional file type is "register an entry in
the factory + ship the impl"; this section pins the data-access and
mode-behavior contract every script impl will share.

**No template substitution on script bodies.** The script body is
fed verbatim to its interpreter. The framework does NOT pre-expand
`{{namespace.key}}` directives inside `.prompt.py` (etc.). If the
script wants a value, it asks for it (see "Data access" below). This
sidesteps the metadata problem entirely — no need to declare per-file
whether the body should be expanded.

Operators who want the engine to expand a value AROUND a script's
output route via `{{include path}}` against the script's pre-computed
output file:

```
{{include ./out/script_result.md}}
```

The script runs in a `PreExec` action (or out-of-band before
`ateam run-all`) and writes to disk; the prompt body includes the
result. This composes cleanly without a per-file expansion-policy
flag.

**Data access.** Two ways to read vars from inside a script:

1. **JSON on stdin.** When the framework invokes the script, it
   passes a JSON document on stdin containing every var the
   framework can enumerate, plus `mode` (`"preview"` or `"real"`)
   and a `phase` discriminator (see lifecycle note below). The
   script reads and parses; no API surface to learn.

2. **`ateam vars` CLI.** A subprocess of the script can shell out to
   `ateam vars` (filtered by namespace / key) to read individual
   values without pulling the whole JSON. Useful for sh scripts that
   don't want to parse JSON inline. The CLI consults the same
   underlying source as the JSON dump — one snapshot, two access
   paths.

The framework's responsibility is producing the enumerable snapshot
at the moment the script runs. The script's responsibility is asking
the right questions of the snapshot. There is no late-binding contract
inside script bodies (no `{{exec.id}}` substitution); the script
reads the value at the moment it cares.

**Preview behavior.** Scripts always run, even in `ModePreview`. The
JSON stdin carries `mode: "preview"` so cheap scripts can branch and
short-circuit (e.g. return a stub manifest instead of hitting the
network). For genuinely expensive scripts that can't be made
cheap-in-preview, the operator routes them through a `PreExec` action
+ `{{include}}` (above) instead of the script-Prompt path. Verify
will invoke every script; a per-script `timeout_seconds` knob lives
on the Prompt impl to bound how long a bad script can stall verify.

**Security.** Sandboxing for script-Prompt impls is deferred until
the planned container-sandbox infrastructure lands. The shipped
defaults will be RO filesystem + work-dir local read + no network;
opt-out via runtime.hcl. Until that lands, **scripts are not enabled**
— the design above is the spec for when they are.

### Vars lifecycle: explicit phases vs. snapshot

The current `Vars` interface is ASK-only (`Resolve(ns, key)`); the
framework dispatches across multiple sources (`runtimeVars` for
`exec.*`, `BaseVars` for everything else, `Dynamics` for late-binding
strings) and the lifecycle is implicit. This works as long as
consumers ask for one key at a time.

Scripts break the asymmetry — they need to enumerate. The design
question that surfaces: WHEN is "the moment of enumeration"?

Two proposals worth evaluating when the data is in:

1. **Earlier ID allocation.** `exec.id` is genuinely runtime-only
   because the runner allocates it from the DB at Prepare time. If
   we shift allocation earlier (e.g. at bundle-build time, with a
   reservation that gets either committed or released at Prepare),
   then `exec.id` joins `project.name` / `args.batch` in the
   bundle-build snapshot. Most other "late" values are computable
   the same way. Only `agent_start_time` (set by the agent process
   itself when it begins emitting) is genuinely irreducibly late.

2. **Snapshot-on-resolve.** Add `CreateVars(ctx) → map[ns]map[key]string`
   called once right before `Prompt.Resolve` runs. The result is a
   frozen map; `Prompt.Resolve` consumes it as the single source of
   truth. This is what scripts would receive as JSON.

The two proposals compose. (1) shrinks the set of values that
need late binding; (2) makes the boundary between "available" and
"not yet" explicit. With both, only `agent_start_time` and any
agent-output-derived values remain as sentinels / post-exec inputs.

Cost: substantial. The current implicit-timing model works because
only `exec.*` is late and only the resolver path consults it. Adding
a snapshot moment + an ID-allocation refactor touches the runner, the
DB schema, every factory, every consumer. Worth doing ONLY when
scripts (or a similar consumer demanding enumeration) make the
ask-only model insufficient.

### Verb-supplied exec.* fields

`runtimeVars` ships the closed set
`exec.{id, batch, output_dir, output_file, prompt_file, timestamp,
agent, model, subrun_args, debug_context,
auto_roles_commands_output}`. The first five are load-bearing
(required in ModeReal); the rest are verb-supplied and empty-OK
because at least one shipped default prompt references each.

**Dropped:** `exec.{profile, effort, max_budget_usd,
max_budget_usd_batch}` were previously recognized as empty-OK but
nothing wired them from `RunOpts → rt` and no shipped prompt
references them. Today they error as unknown-key — referencing one
in a user prompt surfaces a loud typo signal rather than a silent
empty substitution. Pinned by `TestRuntimeVarsExecKeyClosedSet`.

To wire one back when a real consumer shows up, add it to
`validExecKey` + the `resolveExec` switch, populate `rt.<Field>` in
`newBundleRuntime` from `opts`, and add a test row to
`TestExecuteWiresOptsToRuntime`. The factory then sets the RunOpts
field. Each field is the same three-step pattern as `SubRunArgs` /
`DebugContext` (commit 9e96d4d) — no design work required, just an
explicit yes from the verb that owns the value.

(Separately: `runner.TemplateVars` keeps substituting all these keys
in `runtime.hcl` args / container fields. That is a different
substitution surface and is not affected — see
`internal/runner/template.go::Replacer`.)

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
