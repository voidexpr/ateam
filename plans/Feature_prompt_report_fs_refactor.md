# Feature: Prompt & artifact filesystem refactor

The immediate goal is to restructure ateam's artifacts between prompts and generated files. After several design rounds, **the v1 scope is narrower than earlier drafts proposed.** This document captures the chosen direction up front, then the design detail; everything that was considered and dropped lives in "Explored but not pursued" at the end so the rationale survives if we revisit it.

## Decision summary

**ateam stays sharp as a CLI.** The five built-in workflows (report, code, review, verify, auto_setup) plus `exec` and `parallel` are the surface. Users customize prompt *content* via the file system; they do NOT extend ateam with new workflow shapes. New built-in workflows require Go contributions.

**Prompts split from artifacts.** `.ateam/prompts/` for configuration; `.ateam/shared/` for cross-agent artifacts; `.ateam/runtime/<exec_id>/` for per-run scratch. Old `roles/<R>/...` and `supervisor/...` layouts auto-migrate idempotently on first load.

**No user-pluggable execution.** Frontmatter is parsed (strict allow-list) but only ateam-recognized keys do anything; user-defined `pre_exec` / `post_exec` lists are NOT honored. No `ateam flow`, no `{{shell CMD}}`, no `{{arg.*}}`, no parameterized promotion. Built-in workflows keep their hardcoded Go pre/post logic. The Python framework in `plans/python_framework_examples/` is the answer for anything beyond editing prompt content.

**Better telemetry.** `ateam exec` gains structured progress output and writes progress events to the call DB; `parallel` shares the same code path. `ateam ps` / `ateam inspect` work uniformly regardless of who launched the agent — including external orchestrators driving `ateam exec` from Python or shell.

**Internal Go cleanup.** The five built-in commands collapse onto an internal `Stage` abstraction with `PreAction` / `PostAction` types. Same vocabulary as the Python framework, expressed once in Go, used by the dispatch loop. Not exposed publicly. Adding a 6th built-in becomes a Stage definition + a thin Cobra wrapper, not a sixth `cmd/X.go` that duplicates the pattern.

**No `:promptname` CLI syntax.** Since users don't invoke ateam against arbitrary prompts directly, the `:dir/role` invocation surface isn't needed. Roles continue to be selected via existing flags (`ateam report --roles security` etc.). The naming convention still distinguishes role files from dir-level wrappers for the assembler.

**Uniform `--pre-prompt` / `--post-prompt` across all prompt-taking commands.** Every command that takes a prompt — `report`, `code`, `review`, `verify`, `auto_setup`, `exec`, `parallel` — accepts the same `--pre-prompt TEXT` / `--post-prompt TEXT` for ad-hoc inline wrap (Category A, content only). Resolution order: anchors → dir-level `_pre`/`_post` → role-level `pre`/`post` → CLI `--pre-prompt` / `--post-prompt` (outermost).

**Customization story.** Editing prompt content lives in `.ateam/prompts/`. Anything beyond that — new workflow shapes, conditional skip, multi-project orchestration with per-job state — uses the Python framework in `plans/python_framework_examples/`. The longer-term direction for that framework is a separate `ateam-workflow` project (see Pending Q6); v1 keeps it as a forkable design artifact under `plans/`.

## Related documents

- **`plans/python_framework_examples/`** — the Python framework (`ateam.py` ~280 lines) plus two example workflows (`crawl.py`, `metaproject.py`). This is the maintained reference for code-driven workflow customization. Users who need more than prompt-content editing fork or copy this.
- **`plans/prompt_example_metaproject.md`** — earlier exploration of expressing the metaproject workflow purely through the filesystem (frontmatter exec hooks, scripts, parameterized promotion). Concluded that even with all of those features, a thin Python dispatcher is still needed. Preserved as input that led to the current scope.
- **`plans/prompt_example_metaproject_python_api.md`** / **`plans/prompt_example_release_crawl.md`** — three-way comparisons (shell / filesystem / Python) on real workloads. Showed that Python wins for code-heavy workflows and shell wins for simple ones; the file-based middle ground rarely earns its keep.

## Problem space

### Context

Here we are talking about programs that make use of prompts without user interaction. When interaction is needed, skills, MCP servers, and built-in sub-agent management are the right tools for reuse, maintainability, and performance.

**Audience:** developers building LLM-integrated tooling who have outgrown ad-hoc shell scripts and one-off prompt files but don't want (or need) a full workflow engine.

For unattended agentic flows we deal with bigger prompts doing more per call, often a pipeline of them — the classic parallel research/summarize pattern, multi-review steps, and so on. This covers research tasks (gather information from the web and summarize, index, structure), codebase audits and fixes, and unattended tasks triggered by an outside event (an email triggers data extraction; a job gets started, or the data is stored in a structured way for other parts of the system to act on).

### A worked example

An email arrives describing a bug in a service. A realistic workflow:

1. Extracts a reproducer from the email body (one LLM call).
2. Spawns four agents **in parallel** to investigate the codebase from different angles — security, performance, dependency hygiene, recent commits — each writing findings to its own scratch space.
3. Waits for all four; runs a **synthesizing review** that reads their outputs and identifies the most likely root cause and a recommended fix.
4. **Conditionally**: if the review's confidence is high, a code agent attempts the patch; if not, the workflow halts and posts findings to a ticket.
5. After the patch, runs project tests **as a gate**. Tests fail → roll back. Tests pass → open a PR.

A workflow this shaped is **not** something v1 ateam tries to express in its config. The four parallel investigators + the conditional synthesizing review + the test gate → all of that lives in the orchestrator (Python framework, shell, CI pipeline). ateam owns the prompt content for each step and the audit trail for each agent invocation; the orchestrator owns the shape.

This is the line v1 draws: **prompts are config (ateam-owned), workflows are code (orchestrator-owned).**

### Design challenges

The patterns that emerge in any such system:

- **Prompt assembly.**
  - **Factor out** common definitions to help maintain prompts (common context, report format instructions).
  - **Include dynamic content** — execute code during prompt assembly to inject prior output, datasets, git log info. Avoids paying the LLM to derive information it could be told.
  - **Add runtime custom instructions** while keeping the rest of the prompt as-is ("focus on area X", "skip Y", "pay special attention to a/b/c").
  - **Share prompts between projects** while supporting project-specific overloads.
- **Run algorithmic logic** (scripts, programs, built-in commands) before and after the agent acts on a prompt. Set up a git worktree before; run tests as a gate after.
- **Conditionally execute prompts.** Prompts are expensive and slow; if we know the work isn't needed, skip it.
- **Run independent steps in parallel** and wait for them to complete.
- **Treat compute as a constrained resource.** Track per-run cost; gate batches against a budget.

For ateam itself, only the prompt-assembly bullets are in scope. The rest is delegated to the orchestrator. Built-in workflows do the algorithmic work they need in Go; external workflows do it in Python (or whatever).

### Why prompts belong outside the app

LLM-integrated apps have three layers: **prompts** (instructions to the model — half code in that they handle inputs and shape outputs, half configuration in that they steer behavior), **procedural logic** (scripts and actions around the prompt), and a **driver** (the CLI or workflow).

At day one all three change together. Once the app stabilizes, the driver and procedural logic settle into normal code-review pace. **Prompts keep changing** — they're closer to copy than code, and they want non-development-style updates: the model missed a class of findings, emphasis shifts, a project needs different framing, a domain term changes.

That mismatch is why agentic apps eventually externalize their prompts: the file is the unit of change, not the function. ateam provides that structure — a tree of files, diffable, overridable per project / org / embedded — so prompt evolution doesn't pay code-review tax for every wording tweak.

### What ateam provides

Each feature maps to one or more design challenges above.

| Feature | Addresses | Benefit |
|---|---|---|
| File-based layout for prompts | Assembly / factor out | Easy audit; standardized; maintainability, readability |
| Overload, compose (include), pre/post wrappers as files | Assembly / overload, runtime instructions | Prompt fragment management; surgical customization |
| CLI ad-hoc `--pre-prompt` / `--post-prompt` text wrap | Assembly / runtime instructions | One-off framing without persisting |
| `ateam parallel` for N prompts in one project | Parallelism | Same orchestration across N independent prompts |
| Process tracking, cost tracking, structured progress output, agent-led run debugging | Cost, reproducibility, external orchestration | Easier to manage; debuggable runs; orchestrators can consume |
| Prompt preview | Readability | Inspect what will be sent before sending it |

### What ateam does NOT do (v1)

- **Not a scheduler.** No cron, no event triggers. Use systemd timers, GitHub Actions, or whatever your existing infrastructure provides.
- **Not interactive.** No chat UI, no human-in-the-loop turn-taking.
- **Not multi-tenant.** Runs are local to a project; no shared scheduler across teams.
- **Not a model gateway.** ateam wraps existing CLI agents (Claude Code, Codex) — it doesn't talk to model APIs directly.
- **Not a workflow engine.** No user-defined `pre_exec` / `post_exec`, no `ateam flow`, no `{{shell}}`. The Python framework handles workflow customization; ateam handles agent invocation + audit trail.

### Implementation challenges still in scope for v1

- **Prompt preview** — "what would actually be sent if I ran this?" without running it.
- **Prompt templating without a programming language** — `{{var}}` substitution, `{{include}}` for composition. Bounded vocabulary; no host language semantics bleed into prompts.
- **Readability under composition.** Filename-driven assembly keeps the "what wraps this prompt" question answerable by `ls`.

### Why this matters for LLM systems specifically

The drivers behind moving work out of the LLM (assembly-time content injection + pre/post execution) are five distinct things, often conflated:

- **Tokens.** Every computed value the agent has to derive is paid for in input or output tokens.
- **Latency.** Local computation that takes 50ms can save an LLM round-trip of seconds.
- **Determinism.** A script returns the same output for the same input; an LLM doesn't.
- **Trust.** The LLM can do anything; you can't prove it won't. Moving a check, transformation, or side effect into deterministic code means the result is bounded and inspectable.
- **Reproducibility.** LLM outputs are non-deterministic; the same prompt run twice can produce different results. When something goes wrong, you need to reconstruct exactly what was sent, what came back, what scripts ran around it, and in what context. ateam captures all of this; without it, debugging an LLM-based system is guessing.

In v1, ateam handles the first four for assembly-time content (template variables + includes). For the fifth (reproducibility), ateam owns the audit trail of agent invocations; the orchestrator owns the audit trail of its own logic around them.

## Why this refactor

Today, prompts (configuration) and generated outputs are entangled under the same trees, and the codebase carries two parallel abstractions (`roles/<NAME>/...` vs `supervisor/...`) that complicate prompt resolution, role discovery, and output promotion. `internal/runner/runner.go:1156` already has a TODO acknowledging the split is overdue: "get rid of this exclusion once configured prompts are kept separate from files."

We also want a model where layered overrides (project / org / embedded) and surgical specialization (per-role fragments) are first-class — without dragging in a full workflow engine.

## Goals

1. **Split prompts from generated outputs** — `prompts/` for configuration, `shared/` (and per-execution `runtime/<exec_id>/`) for generated artifacts.
2. **Filename-driven assembly** — composition encoded in filename patterns and directory placement, not in template-file orchestration. `ls` of a directory tells you what wraps each role.
3. **Layered anchors** — project → org → embedded. Same filename overloads (first hit wins); distinct filenames compose.
4. **Structural naming safety** — dir-level structural files prefixed with `_`; role-level files use the role basename. Parser deterministic.
5. **Internal Go cleanup** — built-in commands collapse onto a `Stage` abstraction with `PreAction` / `PostAction` types. Same vocabulary as the Python framework, expressed once in Go.
6. **Better external-orchestrator support** — `ateam exec` emits structured progress, writes events to the call DB, sharing the code path with `parallel`. Telemetry uniform regardless of who launched the agent.
7. **Easy future flag: `--prompt-dir DIR`** — the assembler takes a `prompt_dir` parameter throughout. Adding the CLI flag later becomes plumbing, not a refactor.

## Naming convention

All files used by the framework follow a deterministic, suffix-driven pattern. The `_` prefix marks dir-level structural files; everything else is role-related.

### File patterns

| Pattern | Means |
|---|---|
| `<role>.prompt.md` | Role `<role>` main body. Optional YAML frontmatter. |
| `<role>.pre.md` | Role pre, single (overloads across anchors). |
| `<role>.pre.<NAME>.md` | Role pre fragment named `<NAME>` (composes with other `<role>.pre.*.md` files). |
| `<role>.post.md` | Role post, single. |
| `<role>.post.<NAME>.md` | Role post fragment named `<NAME>`. |
| `_pre.md` | Dir-level pre, single (overloads). |
| `_pre.<NAME>.md` | Dir-level pre named `<NAME>` (composes). |
| `_post.md` | Dir-level post, single. |
| `_post.<NAME>.md` | Dir-level post named `<NAME>`. |

Any other `*.md` file is an arbitrary content fragment; the framework parser ignores it, but it can be referenced explicitly via `{{include}}`.

### Restrictions on role names

Two rules ensure unambiguous parsing:

1. **Cannot start with `_`** — that prefix is reserved for dir-level structural files.
2. **Cannot end with `.pre` or `.post`** — prevents greedy-parse ambiguity. A hypothetical role `code.pre` would make `code.pre.pre.scope.md` parse two ways (role `code.pre` pre `scope` vs. role `code` pre `pre.scope`).

Otherwise role names are any dot-separated identifier: `security`, `code.bugs`, `project.security`, `review`, etc.

### Parsing (deterministic, suffix-driven)

For any `*.md` file in the prompts tree, the parser picks the first matching pattern, suffix-anchored, with `<role>` greedy on the LEFT:

1. Ends with `.prompt.md` → role main, role = everything before `.prompt.md`.
2. Ends with `.pre.md` or `.pre.<NAME>.md` → role pre, role = everything before the final `.pre`.
3. Ends with `.post.md` or `.post.<NAME>.md` → role post, role = everything before the final `.post`.
4. Filename is `_pre.md` or `_pre.<NAME>.md` → dir-level pre.
5. Filename is `_post.md` or `_post.<NAME>.md` → dir-level post.
6. Otherwise → arbitrary include, ignored by the framework parser.

Role-name restrictions are validated after parsing. Violations error at load with a clear message.

### How roles are invoked

Roles are selected by existing built-in commands' flags:

```
ateam report --roles security
ateam report --roles project.security
ateam code --roles auth.refactor
ateam review              # singleton
```

No `:dir/role` CLI syntax is introduced — that was speculative for a more open workflow surface that v1 doesn't ship. The internal assembler accepts a path-style name (`report/security`); the CLI maps `--action report --roles X` to that path.

## Target layout

```
.ateam/
  config.toml
  state.sqlite
  secrets.env
  logs/
  prompts/
    _pre.context.md          # root-level pre, applies to every prompt
    review.prompt.md         # singleton role
    review.pre.format.md     # (optional) per-role pre fragment
    auto_setup.prompt.md
    code_management.prompt.md
    code_verify.prompt.md
    exec_debug.prompt.md
    report_commissioning.prompt.md
    report/
      _pre.intro.md          # report dir-level pre — applies to every role in report/
      _post.format.md        # report dir-level post
      security.prompt.md     # role main
      security.pre.scope.md  # (optional) per-role pre fragment
      test_gaps.prompt.md
      code.bugs.prompt.md
      ...
    code/
      _pre.constraints.md
      security.prompt.md
      ...
  shared/
    report/
      security/
        security.md          # primary output
    review/
      review.md
    verify/
      verify.md
    auto_setup/
      auto_setup.md
  runtime/
    <exec_id>/               # per-run scratch, default destination for prompt writes
```

Same restructuring applies to `.ateamorg/` and the embedded `defaults/` tree.

## Framework primitives (the whole list)

The framework provides exactly these primitives. Composition is built into the naming convention — there is no template file to author.

1. **Prompt resolution by name across anchors.** Anchors ordered most-specific to least: project → org → embedded. First hit wins.
2. **Filename-based assembly.** Given a prompt name `<dir>/<role>`, the assembler discovers all matching files and composes them per the assembly order below.
3. **`{{var}}` substitution.** Template variables in the `scope.name` convention: `prompt.*`, `exec.*`, `project.*`, `git.*`, `container.*`, `env.NAME`, `ateam.*`, `role.*`. See the "Template variables" section for the full list. Old ALL_CAPS forms (`OUTPUT_DIR`, `EXEC_ID`, `PROJECT_NAME`, …) auto-migrate to the new names in user prompts.
4. **`{{include PATH}}`** — inline a file's content. **First-match across anchors.** Error if no anchor has the file.
5. **`{{include? PATH}}`** — inline a file's content. **First-match across anchors.** Empty string if no anchor has it.
6. **`{{include_glob PATTERN}}`** — inline files matching a glob, deterministic order: within each anchor sorted lexically; across anchors most-general first (embedded → org → project). Empty string if no matches. Used internally by the assembler to find pre/post fragments; can also be called explicitly.
7. **YAML frontmatter parsing** on `<role>.prompt.md` and on dir-level structural files. **Strict allow-list** — unknown keys reject with a clear error. v1 honors no user-pluggable keys; the parser exists to validate format and reserve the surface for future ateam-internal metadata.

### One rule for file composition across anchors

**Same filename always overloads** — embedded's `_pre.context.md` and project's `_pre.context.md` are the same file at different anchors; first-match (project) wins. Never additive.

**Different filenames compose** — if you want multiple fragments to all contribute, give them distinct names. Embedded ships `_pre.context.md`, org adds `_pre.org_policy.md`, project adds `_pre.local.md`. They are three different files; the assembler picks up all three (lexical order within each anchor, embedded → org → project across anchors).

### Assembly order for `dir1/dir2/role`

```
[CLI --pre-prompt]
  [root _pre.*.md]              (all matching files, composed)
    [dir1 _pre.*.md]
      [dir2 _pre.*.md]
        [role.pre.*.md]         (role-level pre fragments)
        [role.prompt.md]        (main, first-match)
        [role.post.*.md]        (role-level post fragments)
      [dir2 _post.*.md]
    [dir1 _post.*.md]
  [root _post.*.md]
[CLI --post-prompt]
```

Each `_pre.*.md` / `_post.*.md` slot expands to all matching files at that directory level, composed across anchors per the rule above.

### Substitution inside include paths

Include paths may contain `{{var}}` substitutions. Resolution is two-pass:

1. Substitute `{{var}}` inside the include path text.
2. Resolve the include against anchors.

### Cycles, depth, errors

- `{{include}}` recursion is depth-limited (e.g. 16 levels). Cycle detection errors at preview/assembly time.
- `{{include? }}` produces empty on missing; never errors for missing files.
- YAML frontmatter parse errors are loud at preview time. Unknown frontmatter keys error.

### Orphan-fragment detection (catches typos)

At preview/load time, the assembler walks every file matching `<role>.pre.md`, `<role>.pre.<NAME>.md`, `<role>.post.md`, `<role>.post.<NAME>.md` across all anchors. For each, the parsed `<role>` is checked against the set of known roles (basenames of `<role>.prompt.md` files at any anchor). If no matching `<role>.prompt.md` exists anywhere, error:

```
orphan fragment: report/securty.pre.scope.md
  no matching report/securty.prompt.md found in any anchor
  did you mean: security?
```

Levenshtein hint when the base name is close to an existing prompt.

## Composition: dir-level and role-level wrappers

There is no template file. The assembly order is **encoded in the filenames** and the directory chain. Reading `ls` of a directory tells you exactly what wraps each role; opening any single file is enough to understand its content.

### What gets assembled for `report/security`

```
[CLI --pre-prompt]                          (outermost ad-hoc)
  prompts/_pre.*.md                         (root dir-level pre, composed)
    prompts/report/_pre.*.md                (report dir-level pre, composed)
      prompts/report/security.pre.*.md      (role-level pre, composed)
      prompts/report/security.prompt.md     (role main, first-match)
      prompts/report/security.post.*.md     (role-level post, composed)
    prompts/report/_post.*.md               (report dir-level post)
  prompts/_post.*.md                        (root dir-level post)
[CLI --post-prompt]
```

### What ateam ships in embedded defaults

**Root level** (`defaults/prompts/`):
- `_pre.context.md` — block containing `{{project.info}}` (every prompt gets project context).
- `review.prompt.md` — review singleton (body references `{{role.reports}}`).
- `auto_setup.prompt.md`, `code_management.prompt.md`, `code_verify.prompt.md`, `exec_debug.prompt.md`, `report_commissioning.prompt.md` — other singletons.

**Report directory** (`defaults/prompts/report/`):
- `_pre.intro.md` — "You are performing a {{prompt.name}} report on this project."
- `_post.format.md` — "Format your findings as severity-tagged markdown sections..."
- `security.prompt.md`, `test_gaps.prompt.md`, `code.bugs.prompt.md`, etc. — pure role bodies.

Reading `ls defaults/prompts/report/` tells you: there's a shared intro, a shared format, N role-specific files. No file to open to "understand the wrap."

### Project-level customization patterns

**Recommended (surgical, upgrade-safe):**
- Add `report/security.pre.<NAME>.md` at project or org anchor → composes into security's pre alongside whatever embedded ships.
- Add `report/_post.<NAME>.md` at project or org anchor → applies to every role in `report/`.
- Add `_pre.<NAME>.md` at the prompts root → applies to every prompt in every dir.
- Add a brand-new `report/<my-custom>.prompt.md` → custom role. No upgrade conflict.

**Avoid (drift risk on ateam upgrade):**
- Overriding `report/security.prompt.md` wholesale at project anchor — you'll lose embedded improvements when ateam upgrades. If you need a different security role, fork it under a different name (e.g. `security_strict.prompt.md`).

### Why this scheme

- **`ls` is documentation.** A directory's contents tell you everything that wraps the roles in it.
- **Forgot-an-include is impossible.** The assembler walks the directory chain and discovers all matching files automatically.
- **Fixed structure where it counts.** Header → role pre → role main → role post → footer at every level.
- **Flexibility preserved via `{{include}}`.** Inside any file you can `{{include}}` other content.

## How a role is identified

A role is the basename `<role>` of a file matching `<role>.prompt.md`. An "action" is the directory name at the top of `prompts/`. A role exists iff `prompts/<action>/<role>.prompt.md` exists somewhere in the resolution chain. Singleton actions (named prompts at root) live alongside roles in different dirs; they cannot collide.

The CLI keeps role/action vocabulary (`ateam report --roles security`); the assembler accepts the path-style equivalent (`report/security`).

## Two kinds of "extra work" around a prompt

The system distinguishes two fundamentally different categories of work that ateam performs around an LLM invocation.

### A. Assembly-time content injection — output goes INTO the prompt

Data the agent reads. Computed during prompt assembly. Saves the agent from doing the work itself.

Mechanisms in v1:
- **Template variables** — `{{project.info}}`, `{{role.reports}}`, `{{project.name}}`, etc. Computed once per env and inlined.
- **Includes** — `{{include FILE}}`, `{{include? FILE}}`, `{{include_glob PATTERN}}`.

**Key property:** these run during preview. Anything in this category must be **idempotent and side-effect-free**.

### B. Pre / post execution hooks — output does NOT go into the prompt

Work done before or after the agent runs. **In v1, this is NOT user-pluggable.** Built-in workflows have hardcoded Go pre/post logic; users can't add their own hooks via frontmatter, scripts, or any other surface.

Today's built-in pre-hooks (kept in Go for v1):

| Hook | What it does | Where (file:line) | Used by |
|---|---|---|---|
| `concurrent-run-check` | Query DB for live processes matching project+action+role; block unless `--force`. | `cmd/table.go:921-948` | report / code / verify / review / parallel |
| `budget-precheck` | Gate new dispatches against accumulated batch cost. | `cmd/table.go:825-849` | report / code / verify (batch mode) |
| `source-writable` | Flag container to allow writes to project source. | `cmd/table.go:905-911` | code / verify / auto_setup / inspect |

Today's built-in post-hooks (kept in Go for v1):

| Hook | What it does | Where (file:line) | Used by |
|---|---|---|---|
| `copy-runtime-files` (promotion) | Copy every file from `runtime/<exec_id>/` to `exec.shared_prompt_dir/`. | `internal/runner/runner.go:1150-1199` | report / review / auto_setup |
| `chain-next` | Optional next stage on success (`report --review` → `ateam review`; `code` → `ateam verify` unless `--no-verify`). | `cmd/report.go:354-356`, `cmd/code.go:347-365` | report (opt-in), code (default-on) |

After the internal Stage refactor (next section), these become typed `PreAction` / `PostAction` values registered on each Stage definition. Still not user-pluggable; just cleaner internally.

**What external orchestrators do.** Anything Category B that's specific to a custom workflow lives in the orchestrator (Python framework, shell, CI). ateam exposes enough on `ateam exec` (`--work-dir`, `--role`, `--action`) and `ateam ps` / `ateam inspect` for orchestrators to drive runs and read back outcomes. See "External orchestration: the Python framework" below.

## Internal Go restructuring (Stage / PreAction / PostAction)

The five built-in commands today (`cmd/report.go`, `cmd/code.go`, `cmd/review.go`, `cmd/verify.go`, `cmd/auto_setup.go`) hand-write the same shape: resolve env → build prompt → check concurrent runs → run agent → promote files → maybe chain to next stage. Each does it slightly differently; helpers in `cmd/table.go` get called from each site individually.

This refactor introduces an internal `Stage` abstraction that captures the shape once, and reframes each built-in command as a Stage definition + a thin Cobra wrapper.

### Sketch

```go
// internal/stage/stage.go
type Stage struct {
    Name      string             // "report" / "code" / "review" / ...
    Action    string             // ateam-action label (passes to call DB)
    Prompt    PromptAssembler    // builds the prompt string from Ctx
    PreExec   []PreAction        // gating + setup, in declaration order
    PostExec  []PostAction       // promotion + chaining + telemetry
}

type PreAction  interface{ Run(*Ctx) error }
type PostAction interface{ Run(*Ctx, *Result) error }

// internal/stage/ctx.go
type Ctx struct {
    Env       *root.ResolvedEnv
    Roles     []string
    Profile   string
    Agent     string
    Model     string
    Effort    string
    Force     bool
    DryRun    bool
    Batch     string
    Budget    BudgetConfig
    DB        *calldb.CallDB
    // ... additional typed fields as needed
}
```

### Vocabulary mirrors the Python framework

This is deliberate: the Python framework in `plans/python_framework_examples/ateam.py` already validates this shape externally. The Go internals adopt the same vocabulary so that ateam's own code and the Python framework's code can be read against each other. Single mental model, two implementations.

| Python framework | Go internal equivalent |
|---|---|
| `Ctx` | `stage.Ctx` |
| `PromptBundle.prompt` (callable / file / string parts) | `Stage.Prompt` (assembler returning a string) |
| `PromptBundle.pre_exec`, `PromptBundle.post_exec` | `Stage.PreExec`, `Stage.PostExec` |
| `Flow("continue" / "skip" / "error")` | `error` return from `PreAction.Run` (nil = continue; `ErrSkip` = skip; other = error) |
| `SkipIf`, `EnsureParents`, `BackupFiles` | Same shape as registered `PreAction` implementations |
| `Runner.run(name, ctx)` | `stage.Run(stage, ctx)` |

The Go side stays internal — no public API contract, no exported `stage` package. If a user wants Go composition they vendor the package; the supported surface is `ateam exec` as a subprocess (same as Python).

### The built-in action catalog becomes real types

Today's hardcoded functions become small typed values:

```go
// internal/stage/actions/concurrent.go
type ConcurrentRunCheck struct{}
func (ConcurrentRunCheck) Run(ctx *stage.Ctx) error { ... }  // existing logic

// internal/stage/actions/promote.go
type CopyRuntimeFiles struct{}
func (CopyRuntimeFiles) Run(ctx *stage.Ctx, r *stage.Result) error { ... }
```

Each Stage definition declares which actions it wants:

```go
// cmd/report.go (simplified)
var reportStage = stage.Stage{
    Name:    "report",
    Action:  "report",
    Prompt:  prompts.AssembleReport,
    PreExec: []stage.PreAction{
        actions.ConcurrentRunCheck{},
        actions.BudgetPrecheck{},
    },
    PostExec: []stage.PostAction{
        actions.CopyRuntimeFiles{},
        actions.ChainNext{Next: "review", Opt: "--review"},
    },
}

func runReport(cmd *cobra.Command, args []string) error {
    ctx := buildCtx(cmd, args)
    return stage.Run(reportStage, ctx)
}
```

`cmd/report.go` ends up much smaller. So do `cmd/code.go`, `cmd/review.go`, `cmd/verify.go`, `cmd/auto_setup.go`. The dispatch loop lives in one place — meaning when progress telemetry lands, it's wired once in `stage.Run` instead of five times in five cmd files.

### Why this isn't user-pluggable

A Stage definition is Go code, not config. There's no way for a user's `.ateam/` to register a new Stage, add a PreAction, or modify the PreAction list of an existing built-in. The Stage abstraction exists for ateam's own engineering benefit (less duplication, easier testing, easier feature addition); it deliberately doesn't expose a user-facing surface.

If you want a new workflow shape, use the Python framework.

### Test seam falls out naturally

With Stage in place, you can unit-test the pre/post chain against a synthetic Ctx, mocking the agent step in the middle. Today's command-level tests stay; cheaper unit tests appear underneath.

## Template variables

**Naming convention: `scope.name`.** The existing ALL_CAPS variables (`OUTPUT_DIR`, `EXEC_ID`, `PROJECT_NAME`, etc.) are renamed into dotted namespaces — `exec.output_dir`, `exec.id`, `project.name` — for readability and extensibility. The rename is its own task (Task 7); the spec describes the v1 outcome.

The dotted vocabulary matches the Python framework's (`exec.shared_dir`, `exec.runtime_dir`, `exec.work_dir`, `prompt.name`, `arg.KEY`, `env.NAME`) so a reader who knows one knows the other.

### Namespaces

| Scope | What lives there |
|---|---|
| `prompt.*` | Identity of the prompt being assembled (name, path, action). |
| `exec.*` | Per-execution state — id, work dir, output paths, shared paths, profile, agent, model, batch, timestamp. |
| `project.*` | Project identity — name, root dir, info block (git HEAD + uncommitted files). |
| `git.*` | Git-derived metadata — repo, branch, commit, head_short, dirty. |
| `container.*` | Container metadata when running in one. |
| `env.NAME` | Process environment variable. |
| `ateam.*` | ateam self-metadata (own path, version). |
| `role.*` | Role-set computations like `role.reports` (assembly-time inlined prior reports). |

### Identity vars

| Variable | Value | Notes |
|---|---|---|
| `{{prompt.name}}` | Last path component (e.g. `security` for `report/security`). | Never empty. Was `{{ROLE}}`. |
| `{{prompt.path}}` | Full prompt path (e.g. `report/security`). | Unambiguous identifier. |
| `{{prompt.action}}` | Top-level path component (e.g. `report` for `report/security`; `review` for `review`). | Was `{{ACTION}}`. "supervisor" goes away post-migration. |

### Output / artifact vars

| Variable | Value | Replaces |
|---|---|---|
| `{{exec.id}}` | Execution ID for this run. | `{{EXEC_ID}}` |
| `{{exec.output_dir}}` | `.ateam/runtime/<exec_id>/` — per-execution scratch. | `{{OUTPUT_DIR}}` |
| `{{exec.output_file}}` | `exec.output_dir/<prompt-basename>.md` — primary output. | `{{OUTPUT_FILE}}` |
| `{{exec.shared_base_dir}}` | Absolute path to `.ateam/shared/`. | (new) |
| `{{exec.shared_prompt_dir}}` | Absolute path to `.ateam/shared/<prompt-path>/`. | (new) |
| `{{exec.batch}}` | Batch identifier when launched via `parallel` or with `--batch`. | `{{BATCH}}` |
| `{{exec.timestamp}}` | Run start timestamp. | `{{TIMESTAMP}}` |
| `{{exec.profile}}`, `{{exec.agent}}`, `{{exec.model}}`, `{{exec.effort}}` | Resolved per-run config. | `{{PROFILE}}` / `{{AGENT}}` / `{{MODEL}}` |

### Project / git / container vars

| Variable | Value | Replaces |
|---|---|---|
| `{{project.name}}`, `{{project.root}}` | Project identity. | `{{PROJECT_NAME}}`, `{{PROJECT_ROOT}}` |
| `{{project.info}}` | Formatted block: git HEAD hash + uncommitted files list. Computed once per env, cached. Replaces the per-`cmd/*.go` `gitutil.GetProjectMeta` + `FormatProjectInfo()` injection. | (new content; was hardcoded Go) |
| `{{git.repo}}`, `{{git.branch}}`, `{{git.commit}}`, `{{git.head_short}}`, `{{git.dirty}}` | Git facts. Computed once, cached. | Partially in `{{PROJECT_*}}` today; split out cleanly. |
| `{{container.*}}` | Container metadata. | `{{CONTAINER_*}}` |
| `{{ateam.own_bin}}`, `{{ateam.own_version}}`, etc. | ateam self-info. | `{{ATEAM_OWN_*}}` |

### Environment

| Variable | Value |
|---|---|
| `{{env.NAME}}` | Value of the named environment variable. Missing variable errors at assembly time. |

### Role / report assembly

| Variable | Value | Replaces |
|---|---|---|
| `{{role.reports}}` | Inlined role reports from `shared/report/*/<name>.md`, with the resolved role set + `--max-age` filter applied (from the run's Ctx). Section header included. Empty if no reports. | `{{ROLE_REPORTS}}` (was `DiscoverReports` + inlining in `AssembleReviewPrompt`) |

These are part of the prompt's content; they are computed during assembly; they have no effect on the runtime environment.

`{{prompt.body}}` is **not** a variable. Where the previous approach used it, templates use `{{include {{prompt.name}}.prompt.md}}` — explicit inclusion.

### Migration of ALL_CAPS variables in user prompts

Auto-migration (Task 1) rewrites known ALL_CAPS variable references in user-authored `.ateam/prompts/*.md` files to the new dotted form. Unknown ALL_CAPS tokens (literal text the user wrote that isn't an ateam variable) are left untouched. The migration is reported on stderr alongside the layout migration. The embedded `defaults/` tree ships with the new names from the start.

## Shared artifacts model

### `exec.output_dir` — per-execution (default)

`exec.output_dir` = `.ateam/runtime/<exec_id>/`. Every prompt run gets a fresh directory. **History falls out for free** — past runs are right there on disk under different exec IDs.

### `exec.shared_prompt_dir` / `exec.shared_base_dir` — cross-agent sharing (opt-in)

When an artifact must be visible to other agents:
- `exec.shared_base_dir` = `.ateam/shared/`
- `exec.shared_prompt_dir` = `.ateam/shared/<prompt-path>/`
- Primary output: `<prompt-basename>.md` inside that dir.

### Promotion

Hardcoded Go (`promoteRuntimeFiles`). Becomes a `CopyRuntimeFiles` PostAction on Stages that need it (report, review, auto_setup). Stages that shouldn't promote (code, verify) simply don't list it.

The runtime/shared split is acknowledged as the weakest part of the model — see Pending question 4. Worth a design pass once the substrate is in place.

## Progress telemetry

**Goal.** External orchestrators (Python framework, shell scripts, CI pipelines) need to see what ateam is doing during a run, not just at the end. `ateam ps` and `ateam inspect` should work uniformly regardless of who launched the agent.

**Use cases.**

1. Python framework driving N parallel `ateam exec` subprocesses wants a live view: "agent X is in tool Y, 42k tokens in." Today the subprocess output streams to the parent terminal and is hard to consume programmatically.
2. `ateam ps` should show progress for runs launched by `ateam exec`, not just runs launched by `ateam parallel`.
3. Post-mortem inspection of a completed run should have the same fidelity as live inspection.
4. CI pipelines should be able to detect "agent stalled" or "agent in unexpected tool" without parsing free-form output.

**Requirements.**

- **Structured progress output from `ateam exec`.** A flag like `--progress-format=jsonl` (or `--progress-fd=N`) emits one JSON line per progress event on a chosen channel. Format: at minimum `{ts, event, ...event-specific fields}` where `event` is one of `tool_start`, `tool_end`, `assistant_message`, `thinking`, `token_count`, `result`. Schema versioned.
- **Progress events written to the call DB.** Both `ateam exec` and `ateam parallel` write a batched stream of progress events to `state.sqlite` so `ateam ps` and `ateam inspect` work uniformly. Batching strategy left to implementation (per N events / per second / on tool-boundaries) to avoid write storms.
- **Shared code path between `exec` and `parallel`.** `ateam parallel` already renders live per-agent stats in memory; that rendering should consume the same event source as the DB writes + structured output, not duplicate it.
- **`exec_id` discoverable from outside.** Either a `--print-exec-id` flag or a structured line on stderr like `exec_id=<id>` so the orchestrator can correlate to `ateam inspect <id>` after the run.

**Out of scope for this telemetry work.**

- Real-time aggregation across multiple parallel processes (each process writes its own events; querying is via DB).
- Replay / time-travel inspection.
- Server-sent-events HTTP endpoint (the web UI already polls; that's separate).

**Design left to the implementing task.** Exact event schema, batching strategy, DB table layout, output format details — all owned by the task that implements this. The requirements above are the constraints.

## External orchestration: the Python framework

For workflow customization beyond editing prompt content, the answer is **`plans/python_framework_examples/ateam.py`** — a small (~280 lines) Python framework that wraps `ateam exec`.

### What it provides

- `PromptBundle` (name + prompt parts + pre/post hooks).
- `Ctx` with typed fields plus `args` (template namespace) and `data` (Python objects).
- `Runner` with `add`, `preview`, `run`, `run_many` (parallelism via `ThreadPoolExecutor`).
- `Flow("continue" | "skip" | "error")` for pre/post hook control.
- Helpers: `SkipIf`, `EnsureParents`, `BackupFiles`, `PromptFn`, `ActionFn`.
- **External prompt files** via `PromptFile`, with templating (`{{prompt.*}}`, `{{arg.*}}`, `{{env.*}}`, `{{exec.shared_dir}}`, `{{exec.runtime_dir}}`, `{{exec.work_dir}}`) and `{{include}}` / `{{include?}}`.
- `Runner(prompt_dir=..., shared_dir=...)` for external prompt trees and custom artifact destinations.

### What it gives up vs. ateam-native

- No anchor system (project / org / embedded). Workflow authors using Python typically control all the prompts in one tree.
- No frontmatter parsing (workflow logic is Python).
- No call DB / sandbox / container — those still come from `ateam exec`.

### How it wraps ateam

```python
subprocess.run(["ateam", "exec",
                "--work-dir", str(ctx.work_dir),
                "--action", ctx.action or name,
                "--role", ctx.role or name],
               input=prompt, text=True, check=True)
```

Each Python thread = one `ateam exec` subprocess. ateam handles agent invocation + audit trail; Python handles prompt assembly + skip/error logic + parallelism.

### Example: the metaproject workflow

`plans/python_framework_examples/metaproject.py` (~360 lines) shows the full pattern: 5 scopes × 4 actions (discover/audit/fix/verify) across multiple target projects, with skip predicates per stage and externalized audit-body prompts under `plans/python_framework_examples/prompts/metaproject/audit/`. Built-in ateam couldn't express this without growing significantly.

### Distribution

The framework lives in `plans/python_framework_examples/` as a design artifact users fork or copy. Not bundled with the ateam binary; not published as a pip package (yet — see Pending question 21). A potential next step is to spin this up as `ateam-workflow`, a separate project that owns the prompt management story for code-driven workflows.

## Auto-migration

On `ateam` startup, when `.ateam/` or `.ateamorg/` is loaded, detect the old layout and migrate in place. Idempotent.

**Detection** (any one is enough): `.ateam/roles/` exists, `.ateam/supervisor/` exists, `.ateam/{report,code}_base_prompt.md` exists, `.ateam/{report,code}_extra_prompt.md` exists, `.ateam/setup_overview.md` at root.

**Migration map** (project-level; org-level mirrors):

| Old | New |
|---|---|
| `.ateam/roles/<R>/report_prompt.md` | `.ateam/prompts/report/<R>.prompt.md` |
| `.ateam/roles/<R>/code_prompt.md` | `.ateam/prompts/code/<R>.prompt.md` |
| `.ateam/roles/<R>/report_extra_prompt.md` | `.ateam/prompts/report/<R>.post.extra.md` |
| `.ateam/roles/<R>/code_extra_prompt.md` | `.ateam/prompts/code/<R>.post.extra.md` |
| `.ateam/roles/<R>/report.md` | `.ateam/shared/report/<R>/<R>.md` |
| `.ateam/roles/<R>/history/...` | dropped (history now via `runtime/<exec_id>/`) |
| `.ateam/report_base_prompt.md` | `.ateam/prompts/report/_pre.base.md` |
| `.ateam/code_base_prompt.md` | `.ateam/prompts/code/_pre.base.md` |
| `.ateam/report_extra_prompt.md` | `.ateam/prompts/report/_post.extra.md` |
| `.ateam/code_extra_prompt.md` | `.ateam/prompts/code/_post.extra.md` |
| `.ateam/supervisor/review_prompt.md` | `.ateam/prompts/review.prompt.md` |
| `.ateam/supervisor/review_extra_prompt.md` | `.ateam/prompts/review.post.extra.md` |
| `.ateam/supervisor/code_management_prompt.md` | `.ateam/prompts/code_management.prompt.md` |
| `.ateam/supervisor/code_management_extra_prompt.md` | `.ateam/prompts/code_management.post.extra.md` |
| `.ateam/supervisor/code_verify_prompt.md` | `.ateam/prompts/code_verify.prompt.md` |
| `.ateam/supervisor/auto_setup_prompt.md` | `.ateam/prompts/auto_setup.prompt.md` |
| `.ateam/supervisor/exec_debug_prompt.md` | `.ateam/prompts/exec_debug.prompt.md` |
| `.ateam/supervisor/report_commissioning_prompt.md` | `.ateam/prompts/report_commissioning.prompt.md` |
| `.ateam/supervisor/review.md` | `.ateam/shared/review/review.md` |
| `.ateam/supervisor/verify.md` | `.ateam/shared/verify/verify.md` |
| `.ateam/supervisor/history/...` | dropped |
| `.ateam/setup_overview.md` | `.ateam/shared/auto_setup/auto_setup.md` |

After migration, remove the now-empty `roles/` and `supervisor/` directories. Print a one-line notice on stderr on first migration. Implementation in a new `internal/migrate/v1_layout.go`, called from `internal/root/resolve.go` when env is first materialized.

## `ateam prompt` inspection modes (in scope)

> **Shipped naming**: this section uses the spec's original `--preview` / `--content` names. The shipped CLI tightened these to `--paths` (table view) and `--inline-paths` (full prompt with per-section anchor/path/mod-time/tokens headers); `--content` was dropped since `--inline-paths` subsumes it. The user-facing reference is `COMMANDS.md#ateam-prompt`.

Without `:syntax`, the CLI shape is flag-based, mirroring existing commands:

```
ateam prompt --action report --role security --paths
ateam prompt --action report --role security --inline-paths
ateam prompt --action review --paths
```

The output pretty-prints the resolved ordered file list with anchor tags, plus the merged frontmatter:

```
$ ateam prompt --action report --role security --paths
Assembly for 'report/security':

Frontmatter (merged from dir-level + role-level):
  (no recognized keys)

Resolution:
  [CLI]      --pre-prompt                                                (empty)

  root _pre.*.md:
    [embedded] prompts/_pre.context.md            (contains {{project.info}})

  report _pre.*.md:
    [embedded] prompts/report/_pre.intro.md
    [project]  prompts/report/_pre.local.md       (added by project anchor)

  security.pre.*.md:
    [project]  prompts/report/security.pre.scope.md

  security.prompt.md:
    [embedded] prompts/report/security.prompt.md  (first-match overload)

  security.post.*.md:
    (no matches)

  report _post.*.md:
    [embedded] prompts/report/_post.format.md

  root _post.*.md:
    (no matches)

  [CLI]      --post-prompt                                               (empty)
```

`--inline-paths` dumps the actual concatenated text, with each section preceded by an anchor/path/mod-time/tokens header.

## What this refactor does NOT change

- `runtime/<exec_id>/` per-run scratch dirs and the `OUTPUT_*` template variables — mechanism unchanged.
- `config.toml` schema.
- `runtime.hcl`, agent/profile/container config.
- `state.sqlite`, `secrets.env`, `logs/`, `cache/` at `.ateam/` root.

## What this refactor explicitly does NOT do

These were considered in earlier drafts and deliberately dropped. They live in "Explored but not pursued" at the end of this document if you want to revisit:

- **User-pluggable execution hooks.** No frontmatter `pre_exec` / `post_exec` lists, no script registry, no `--pre-exec` / `--post-exec` CLI flags.
- **`ateam flow` CLI subcommand** (skip / error / abort / continue / note / completed / redo / fallback / retry).
- **`{{shell CMD}}` template directive** for assembly-time script execution.
- **`{{arg.<key>}}` CLI argument namespace.**
- **Parameterized promotion.** Today's `promoteRuntimeFiles` becomes the `CopyRuntimeFiles` PostAction with the same hardcoded destination derivation.
- **Per-job `--work-dir` / `--role` / `--action` on `ateam parallel`.** Stays "N prompts, shared everything." Multi-project orchestration uses shell + `&` or Python.
- **`runtime/<exec_id>/_run.json`** post-exec context file.
- **`:dir/role` CLI invocation syntax.** Roles selected through existing command flags.
- **`--prompt-dir DIR` / `--runtime-dir DIR` / `--shared-dir DIR` CLI flags.** The assembler is *parameterized* on `prompt_dir` internally (so adding the flag later is plumbing), but the flags themselves don't ship in v1.
- **Public Go API for Stages.** The internal `stage` package is unexported; the supported integration surface is `ateam exec` as a subprocess.

## Immediate work / Tasks

Concrete deliverables, each scoped to be a discrete piece of work. Order matters where listed; otherwise parallelizable.

### Task 1: Prompt-directory refactor (assembler + migration)

**Deliverable:** `.ateam/prompts/` exists, `.ateam/shared/` exists, old layouts auto-migrate, built-in commands assemble through the new tree.

**Acceptance:**

- `internal/prompts/` rewritten with `Assembler` module: `fs.FS`-based anchors (project → org → embedded), filename-pattern parser, role-name validation, orphan-fragment detection.
- **`Assembler` takes `prompt_dir` as a parameter throughout.** Not a global, not hardcoded — passed through construction and threaded into the anchor list. This is the change that makes a future `--prompt-dir DIR` flag a small wiring job rather than a refactor. Same shape for `shared_dir`.
- Template directives: `{{include}}`, `{{include?}}`, `{{include_glob}}` with the documented semantics.
- New template variables in the `scope.name` convention: `{{prompt.name}}`, `{{prompt.path}}`, `{{project.info}}`, `{{role.reports}}`, `{{exec.shared_base_dir}}`, `{{exec.shared_prompt_dir}}`. (Full rename of legacy ALL_CAPS variables is Task 7.)
- `internal/migrate/v1_layout.go` — idempotent auto-migration from the table above, called from `internal/root/resolve.go` on first env materialization. Stderr notice on first migration.
- **Migration failure handling:** stop and surface the error; leave whatever was already moved in place. Idempotency means a re-run picks up where it left off. Do NOT roll back partial migration (too complex; not worth the engineering for a one-time event). Error message must say "migration paused at <step>; re-run ateam to continue."
- `defaults/` tree restructured (`defaults/prompts/...`), embed updated, `_pre.*.md` / `_post.*.md` defaults shipped for the standard workflows.
- `internal/root/resolve.go` path helpers replaced with prompt-name-based lookups.
- `internal/runner/runner.go` — drop the `*_prompt.md` exclusion at line 1156; update canonical destination to `SharedPromptDir(promptName)/<basename>.md`.
- `cmd/*.go` rewired to use the assembler (no `RoleID: "supervisor"` hardcodes).
- `internal/web/handlers.go` and `internal/web/export.go` updated for new artifact paths.
- Frontmatter parsing in place (strict allow-list) but no exec keys honored. Unknown keys error.

**Out of scope:** the `--prompt-dir` CLI flag (parameter is enough); any user-pluggable exec; the `:syntax` CLI form.

### Task 2: Internal Stage / PreAction / PostAction refactor

**Deliverable:** built-in commands run through a shared internal Stage abstraction.

**Acceptance:**

- `internal/stage/` package: `Stage` type, `Ctx` struct, `PreAction` / `PostAction` interfaces, `Run` dispatch function.
- `internal/stage/actions/` package with typed implementations of today's hardcoded hooks: `ConcurrentRunCheck`, `BudgetPrecheck`, `SourceWritable`, `CopyRuntimeFiles`, `ChainNext`.
- `cmd/report.go`, `cmd/code.go`, `cmd/review.go`, `cmd/verify.go`, `cmd/auto_setup.go` rewritten as Stage definitions + Cobra wrappers. Per-command logic that doesn't fit the Stage shape (CLI flag parsing, validation messages) stays in `cmd/`.
- Stage definitions registered in a `cmd/registry.go` (or similar) for discoverability.
- Internal — package not exported, no public API contract.
- All existing tests pass; new unit tests for the Stage runner against synthetic Ctx.

**Out of scope:** exposing Stages as a public Go API; allowing users to register custom Stages; new built-in workflows (this task is restructuring, not adding).

**Design decisions to make at the start of this task** (not pre-decided in the spec):

1. **Stage vs existing `internal/runner/runner.go` relationship.** Three plausible shapes — Stage wraps Runner (PreActions → `runner.Run` → PostActions); Stage replaces Runner (fold the existing Runner into Stage's dispatch); Stage coexists (built-ins use Stage, exec/parallel keep calling Runner directly). The right answer depends on how tangled Runner is with each command site — assess and pick before starting the rewrite.
2. **Where Ctx is constructed.** Per-command Cobra wrappers build it from flag values; or a shared `buildCtx(cmd, args)` helper handles the common case with per-command extensions. Likely the latter, but confirm by trying both.
3. **Action dependency on Runner internals.** Today's hardcoded actions reach into `cmd/table.go` helpers + `internal/runner/` types. PreAction/PostAction signatures need access to whatever subset they actually use — minimize the surface so actions stay small and testable.
4. **How `exec` and `parallel` interact with Stage.** They don't fit the Stage shape (no named role, no fixed prompt assembler). Two options: leave them outside Stage entirely, or model them as a degenerate Stage with no PreExec/PostExec. Likely the former.

### Task 3: Progress telemetry — `exec` output format + shared code with `parallel` + DB writes

**Deliverable:** `ateam exec` emits structured progress, both `exec` and `parallel` write progress events to the call DB, `ateam ps` works uniformly.

**Acceptance** (requirements; exact design left to the implementing task):

- `ateam exec --progress-format=jsonl` (or `--progress-fd=N`) emits per-event JSON lines on the chosen channel.
- Event vocabulary at minimum: `tool_start`, `tool_end`, `assistant_message`, `thinking`, `token_count`, `result`. Versioned schema.
- Progress events persisted to `state.sqlite` (table layout / batching strategy chosen by the task).
- `ateam exec` and `ateam parallel` consume the same event source (no duplicated event handling). Today's in-memory parallel rendering feeds off the same stream.
- `exec_id` emission discoverable from outside (`--print-exec-id` flag or stderr line); orchestrators can correlate to `ateam inspect <id>`.
- `ateam ps` shows running `exec` invocations alongside `parallel` ones.
- `ateam inspect <exec_id>` shows per-event history for completed runs.

**Indicative event shape** (for design grounding — exact schema is the task's to lock):

```jsonl
{"v":1,"ts":"2026-05-22T15:01:02.123Z","exec_id":"abc123","event":"tool_start","tool":"Bash","input_preview":"go test ./..."}
{"v":1,"ts":"2026-05-22T15:01:08.456Z","exec_id":"abc123","event":"tool_end","tool":"Bash","exit":0,"duration_ms":6333}
{"v":1,"ts":"2026-05-22T15:01:09.789Z","exec_id":"abc123","event":"token_count","input":12450,"output":340,"cache_read":8200}
{"v":1,"ts":"2026-05-22T15:01:10.234Z","exec_id":"abc123","event":"assistant_message","preview":"I'll start by reading..."}
{"v":1,"ts":"2026-05-22T15:01:30.567Z","exec_id":"abc123","event":"result","status":"success","cost_usd":0.0421}
```

Conventions to lock in the task: `v` for schema version (start at 1); `ts` ISO-8601 with millis; `exec_id` on every event; `event` from the closed vocabulary above; event-specific fields after.

**Out of scope:** real-time aggregation across processes, replay / time-travel, SSE HTTP endpoint, structured `_run.json` for post-exec consumption (since v1 has no post-exec hook surface for users).

### Task 4: `ateam prompt` inspection modes

**Deliverable:** the inspection tool described above. (Shipped as `--paths` / `--inline-paths`; see the note at the top of that section.)

**Acceptance:**

- `ateam prompt --action X --role Y --paths` pretty-prints the resolution + merged frontmatter, with anchor tags on every file.
- `--inline-paths` adds the full assembled text, each section preceded by an anchor/path/mod-time/tokens header.
- Errors loudly on invalid frontmatter, orphan fragments, role-name violations, missing required includes.

### Task 5: Documentation pass

- `CONFIG.md` — new prompt layout + customization patterns.
- `ROLES.md` — anchor-based override patterns.
- `README.md` — updated commands and customization story.
- `ISOLATION.md` — verify no impact on container/sandbox semantics; refresh if anything changed.
- Reference `plans/python_framework_examples/` for "I need more than prompt-content customization."

### Task 6 (smaller): drop the `runs.go` → `ps.go` rename's remnants, if any

The recent rename to `psCmd` is complete; this is a cleanup pass if anything was missed.

### Task 7: Rename template variables to the `scope.name` convention

**Deliverable:** all template variables follow the dotted-namespace convention described in the "Template variables" section. Old ALL_CAPS forms are mapped to the new forms during migration.

**Acceptance:**

- `internal/runner/template.go` renamed/restructured to dispatch by namespace: `prompt.*`, `exec.*`, `project.*`, `git.*`, `container.*`, `env.NAME`, `ateam.*`, `role.*`. Resolution is one function per namespace; adding a new variable in a namespace is a one-line change.
- `{{env.NAME}}` reads from process environment; missing variable errors at assembly time with the variable name in the message.
- `{{git.*}}` set computed once per env, cached: `repo`, `branch`, `commit`, `head_short`, `dirty`.
- `defaults/prompts/*` rewritten to use the new variable names. Embedded defaults ship the new vocabulary from v1.
- Auto-migration (Task 1's migrator) gains a content-rewrite pass that updates known ALL_CAPS variable references in user-authored `.ateam/prompts/*.md` files to the new dotted form. Unknown ALL_CAPS tokens are left untouched. Migration is logged on stderr.
- Migration mapping table is closed (no aliases). Once a v1 ateam binary sees a project, the prompts use the new names.
- Tests: rename mapping is exhaustive for current variables; assembler emits clear errors for unknown variables in a known namespace; unknown namespaces pass through unchanged (so literal `{{foo.bar}}` text agents emit stays as-is).

**Out of scope:** keeping ALL_CAPS aliases past migration; new namespaces beyond the ones listed in the spec (`git.*` is the only meaningfully new one — existing variables redistribute into the others).

**Ordering note:** do alongside or immediately after Task 1 to avoid the embedded defaults ever shipping with ALL_CAPS names in v1.

### Task 8: Normalize `--pre-prompt` / `--post-prompt` across all prompt-taking commands

**Deliverable:** every command that takes a prompt accepts the same `--pre-prompt TEXT` / `--post-prompt TEXT` flags for ad-hoc inline wrap.

**Acceptance:**

- Audit current state across `report`, `code`, `review`, `verify`, `auto_setup`, `exec`, `parallel` — which commands accept the flags today, which don't. Make every prompt-taking command accept them with identical semantics.
- Flag values are text or `@filepath` (matching the existing `--extra-prompt` shape on `exec`).
- Wrap order is documented and consistent: anchors → dir-level `_pre`/`_post` → role-level `pre`/`post` → CLI `--pre-prompt` (outermost head) / `--post-prompt` (outermost tail).
- `ateam <cmd> --help` text describes the flag identically across commands (extract to a shared help fragment if helpful).
- Tests verify a fixture prompt assembles with the CLI wrap correctly for each command that takes prompts.

**Out of scope:** `--pre-exec` / `--post-exec` (those are execution hooks; explicitly dropped per the Decision summary); `--extra-prompt` (the existing `exec` flag stays as-is for backward compatibility; document overlap with `--post-prompt`).

---

The big tasks (1, 2, 3, 4, 7) are mostly parallelizable once Task 1's assembler shape exists. Task 2 depends on Task 1 for the Ctx + assembler shape. Task 3 is fully independent. Task 7 is tightly coupled to Task 1 and best done by the same person. Tasks 5 (docs), 6 (cleanup), and 8 (CLI normalize) can land last.

## Scope guardrails

While implementing, things to actively NOT do:

- **Don't expose `internal/stage` as a public API.** Even if the abstraction looks clean, exporting it commits us to a compatibility surface. Keep it internal; external orchestration is `ateam exec` as a subprocess.
- **Don't introduce frontmatter keys beyond what the spec lists.** Strict allow-list. If a key seems useful, add it to "Pending questions" with a use case rather than implementing it.
- **Don't add `--pre-exec` / `--post-exec` / `--arg` / `--prompt-dir` CLI flags.** Those are explicitly dropped from v1. The Python framework is the answer for the use cases they'd address.
- **Don't backport ALL_CAPS aliases past Task 7's migration.** Once migrated, prompts use the new names. Aliases would entrench the old vocabulary and complicate the engine.
- **Don't extend `ateam parallel` with per-job overrides** (`--work-dir`, `--role`, `--action`). It stays "N prompts, shared everything." Multi-project orchestration is shell+& or Python.
- **Don't reintroduce the `:promptname` CLI syntax.** Roles continue to be selected via command flags.
- **Don't grow the agent CLI wrapper logic** (`internal/agent/...`). This refactor is about prompts and dispatch shape; the agent invocation layer stays as-is.

## Definition of done (whole refactor)

v1 of this refactor is complete when:

1. **All eight tasks ship** with their acceptance criteria met.
2. **All verification plan items pass** (see below).
3. **A fresh user (no prior `.ateam/`)** can run `ateam init` + `ateam report --roles project.security` + `ateam review` against `./test_data/` and get expected outputs at the documented paths.
4. **A migrated user (old `.ateam/` layout)** gets transparent migration on first command, with their prior customizations preserved at the correct new paths.
5. **`plans/python_framework_examples/crawl.py` runs end-to-end** against a fixture using the new `ateam exec` interface, and `ateam ps` shows its runs while in flight.
6. **`make run-ci` passes** and `make test-docker` passes.
7. **Documentation is updated** (Task 5) — README, CONFIG.md, ROLES.md, ISOLATION.md reflect the new layout, the customization story, and the Python-framework escape hatch.

## Pending questions

Several of the earlier pending questions become moot with the narrowed scope. The remaining ones:

1. **Setup overview filename** — auto-migration renames `setup_overview.md` → `auto_setup/auto_setup.md`. Acceptable break, or keep historical name?
2. **`ateam roles` output** — keep as role-listing, or unify under `ateam prompts list`? Decide after Task 1 lands.
3. **Frontmatter schema strictness** — strict allow-list (locked: strict). What ateam-internal keys, if any, get added in v1? Probably none; reserved for future.
4. **Runtime/shared promotion model** — current split (agent writes to `exec.output_dir`, then promotion copies selected files to `exec.shared_prompt_dir`) works but isn't intuitive. Worth a design pass after Task 1 + Task 2 land: is there a cleaner model? E.g. agents always write to the canonical shared path; the runtime dir is a tee'd copy purely for history; promotion goes away as a concept. Or: prompt frontmatter declares which output files are "shared" vs "scratch," and the runner enforces the split at write time. Defer until after the substrate lands; flag explicitly that the current model is the weakest part of the design.
5. **Progress event schema versioning** — owned by Task 3.
6. **Python framework distribution.** Stay in `plans/python_framework_examples/`, promote to top-level `python/` or `examples/`, or spin out as a separate `ateam-workflow` project? Probably stays in `plans/` until external users start asking for it; the `ateam-workflow` idea is the more ambitious vision (typed `actions × entities × roles/scopes` data model, eventually resumable multi-step workflows) and is a meaningful side project of its own.

### Resolved (recorded here so a future reader doesn't reopen them)

- **`{{role.reports}}` filter inputs.** Expands to the resolved role set for the current invocation, with the existing `--roles` / `--all` / `--max-age` filters applied. The Stage's `Ctx` carries the resolved filter; the assembler reads it when expanding the variable. Same behavior as today's review command; no new template-argument syntax.
- **Enablement.** Stays in `config.toml [roles]`. No frontmatter `enabled:` or `enabled_from:` key in v1.

## Critical files to read before implementing

- `internal/prompts/prompts.go` — current resolver this refactor replaces
- `internal/prompts/embed.go` — role discovery from embedded FS
- `internal/root/resolve.go` — path helpers
- `internal/runner/runner.go:737, 1156-1199` — `promoteRuntimeFiles`
- `internal/runner/template.go:17-33, 180-195` — template vars and primary output names
- `defaults/embed.go` + the `defaults/` tree
- `cmd/review.go`, `cmd/code.go`, `cmd/auto_setup.go`, `cmd/verify.go`, `cmd/inspect.go`, `cmd/prompt.go`, `cmd/roles.go`, `cmd/report.go`, `cmd/table.go`
- `internal/web/handlers.go`, `internal/web/export.go`
- `CONFIG.md`, `ROLES.md`, `README.md`, `ISOLATION.md` for doc updates
- `plans/python_framework_examples/ateam.py` — the external orchestration reference

## Verification plan

1. `make build` + `go test ./...` after each significant step.
2. `make test-docker` once at the end.
3. **Golden prompt test** — capture `ateam prompt --role <r> --action report` and `ateam prompt --supervisor --action review` outputs before the refactor; after, run the equivalent `ateam prompt --action report --role <r>` and `ateam prompt --action review` and diff. Should be byte-identical modulo intentional ordering changes.
4. `ateam roles` lists the same set of roles before/after.
5. End-to-end on a fresh `./test_data/` project: `ateam init`, `ateam report --roles project.security`, verify the report lands at `.ateam/shared/report/project.security/project.security.md`. Then `ateam review`, verify it lands at `.ateam/shared/review/review.md`.
6. **Migration test** — project with old layout (artifacts plus overrides at all levels), run `ateam` once, verify behavior. Re-run to confirm idempotence.
7. **Org-override test** — both org-level and project-level overrides for the same role; confirm anchor fallback still works.
8. **Composition test** — `{{include}}` / `{{include?}}` first-match: create the same filename at embedded, org, and project anchors; assert project's wins. `{{include_glob}}` additive: create distinct filenames at each anchor matching the glob; assert all are concatenated in most-general-first order. Dir-level `_pre.*.md`: same composition.
9. **Frontmatter test** — invalid YAML errors at preview; unknown key errors.
10. **Role-name validation test** — `_security.prompt.md` (starts with `_`) errors at load. `code.pre.prompt.md` (ends with `.pre`) errors.
11. **Orphan-fragment test** — `securty.pre.scope.md` (typo) errors with Levenshtein hint.
12. **Stage refactor test** — internal `stage.Run` against synthetic Ctx with mocked agent step; pre-actions fire in order, post-actions fire in order, errors propagate correctly.
13. **Progress telemetry test** (Task 3) — `ateam exec --progress-format=jsonl` against a short prompt, parse the emitted events, confirm DB rows exist for the same events.
14. Manual smoke: `ateam prompt --action review`, `ateam prompt --action code --role management`, `ateam prompt --action report --role project.security`.
15. **External orchestration smoke** — run `plans/python_framework_examples/crawl.py` (or equivalent) against a fixture, confirm `ateam ps` sees the runs.
16. **Variable rename migration test** (Task 7) — user prompt containing `{{OUTPUT_DIR}}`, `{{EXEC_ID}}`, `{{ROLE_REPORTS}}` etc. migrates to `{{exec.output_dir}}`, `{{exec.id}}`, `{{role.reports}}`. Literal ALL_CAPS text that isn't a known variable name is left untouched.
17. **Template error test** (Task 7) — `{{prompt.unknown_key}}` errors at assembly with the key in the message; `{{foo.bar}}` (unknown namespace) passes through as literal text.
18. **CLI uniformity test** (Task 8) — `--pre-prompt "X"` and `--post-prompt "Y"` work identically across `report`, `code`, `review`, `verify`, `auto_setup`, `exec`, `parallel`. Wrap order matches the documented resolution (CLI outermost).

---

# Explored but not pursued

The sections below capture design directions that earlier drafts pursued and that v1 explicitly drops. Preserved here so the rationale survives — if any of this becomes worth revisiting, the design notes don't need to be re-derived.

## File-based execution hooks (frontmatter `pre_exec` / `post_exec` + scripts)

The strongest competing direction was making execution hooks user-pluggable: dir-level and role-level frontmatter declares ordered lists of pre-exec and post-exec steps; each step is either a built-in action name (`copy-runtime-files`, `concurrent-run-check`) or a script path. Combined with runtime flow control (`ateam flow skip / error / abort / continue / note`), assembly-time scripting (`{{shell CMD}}`), and CLI-injected stages (`--pre-exec`, `--post-exec`, `--arg key=value`), this would let users build new workflow shapes entirely inside `.ateam/`.

### Why it was dropped

1. **Files everywhere.** A non-trivial workflow ends up as 10+ frontmatter-bearing `.md` files + a `scripts/` directory of small shell scripts. The Python framework's single-file expression of the same workflow (typed Python + 5 small markdown prompts for the static content) reads dramatically better.
2. **The "small piece of logic" gap doesn't exist in practice.** People want either "edit this prompt's wording" (file system handles cleanly) or "build a real workflow" (needs typed code, control flow, error handling — Python wins). The frontmatter-hook middle ground rarely earns its keep.
3. **Surface area cost.** `ateam flow`, `{{shell}}`, `{{arg.*}}`, parameterized promotion, on_failure policy, per-step retry, redo loops — each is a small feature, but the bag of them roughly doubles the spec. And once `ateam` ships them, removing them is hard.
4. **The CLI promise stays cleaner without them.** "ateam is a sharp CLI you use from shell scripts" is a stronger story than "ateam is a workflow engine with a DSL."
5. **External orchestration in Python is genuinely good.** `plans/python_framework_examples/` proves that ~280 lines of framework + ~50 lines per workflow gets you typed config, structured results, parallel execution, and skip semantics — without inventing new languages or scattering logic across files.

### The Stage concept (framing the dropped design)

A **stage** was the unit of "one LLM invocation with deterministic wrapping" — three phases (pre / prompt / post). Pre and post lists were ordered; per-role frontmatter extended dir-level frontmatter; dir-level frontmatter lived on `_pre.*.md` / `_post.*.md` files alongside their content.

Example dir-level frontmatter (dropped):

```yaml
---
pre_exec: [concurrent-run-check, source-writable, ./gather-deps.sh]
post_exec: [copy-runtime-files, ./validate.sh]
---
You are performing a {{prompt.name}} report on this project.
[... rest of the pre fragment's markdown body ...]
```

Per-role frontmatter would have extended dir-level lists by appending — `[concurrent-run-check, source-writable, ./gather-deps.sh, ./security-extra-setup.sh]` for an `audit/security.prompt.md` with its own `pre_exec`.

### The `ateam flow` subcommand (dropped)

A CLI scripts (in `pre_exec` / `post_exec` / `{{shell}}`) would call to signal lifecycle decisions back to the runner. Action set (v1 + richer v2):

| Action | What it would have done |
|---|---|
| `ateam flow skip [--reason TEXT]` | Don't invoke the LLM; mark skipped. |
| `ateam flow completed [--reason TEXT]` | Script did the work; mark success without invoking LLM. |
| `ateam flow error [--reason TEXT]` | Don't invoke the LLM; mark failed. |
| `ateam flow abort [--reason TEXT]` | Kill the entire `ateam parallel` batch. |
| `ateam flow continue` | Explicit no-op signal. |
| `ateam flow note <text>` | Attach a free-form note to the run record. |
| `ateam flow redo --extra TEXT` | Re-run the same prompt with appended instruction. New `exec_id` with `parent_exec_id`. |
| `ateam flow fallback --profile X` | Re-run with different agent/profile. |
| `ateam flow retry [--after N \| --until-reset \| --backoff POLICY]` | Re-run after a wait policy. |

Storage was to be `runtime/<exec_id>/_flow.toml` (append-only during run) plus call-DB schema additions (`status` enum gains `skipped` / `aborted` / `completed-by-script`; new `flow_reason` text column; `parent_exec_id` for redo chains).

Discovery via `ATEAM_EXEC_ID` env var on scripts ateam launched.

### `{{shell CMD}}` template directive (dropped)

Runs `CMD` during prompt assembly; stdout becomes prompt body. Scripts had to be idempotent and read-only beyond `ateam flow` side effects (assembly may run during `--preview`). `ATEAM_PREVIEW=1` env var set during preview. Two-pass expansion like includes: variables inside `{{shell CMD}}` resolved first, then shell runs.

### Rich post_exec context: `runtime/<exec_id>/_run.json` (dropped)

JSON document written at the start of post_exec containing identity / config / timing / cost / outcome / activity-summary fields. Would have been consumed by post_exec scripts and `--auto-debug` alike. Dropped because there's no user-pluggable post_exec to consume it.

### `{{arg.<key>}}` namespace + `--arg key=value` CLI (dropped)

Per-invocation typed argument surface. Would have let one prompt template parameterize per invocation (e.g. `{{arg.target_label}}` for multi-target metaproject runs). Dropped because the multi-target use case is exactly what the Python framework handles naturally (typed config in Python).

### Parameterized promotion (dropped)

The default `copy-runtime-files` action would have grown a `--to <pattern>` form with `{{arg.*}}` / `{{prompt.*}}` substitution in the destination, supporting workflows like the metaproject's `reports/<target>/<scope>/audit.md` layout. Dropped because v1's promotion stays hardcoded per Stage.

### `on_failure` step-level policy (dropped)

Per-step `on_failure: stop | continue | fallback_to_llm` in frontmatter would have governed what happens when a pre_exec script returned non-zero. Dropped along with the user-pluggable exec lists.

### `--prompt-dir DIR` / `--runtime-dir DIR` / `--shared-dir DIR` CLI flags (deferred)

CLI flags to override the default `.ateam/` subtrees were planned to let workflows like the metaproject example use a top-level `reports/` directory for user-visible artifacts. Deferred for v1 — but the assembler is parameterized on `prompt_dir` internally so adding the flag later is small. The Python framework already supports `--shared-dir`-equivalent via `Runner(shared_dir=...)`.

## Ad-hoc prompts and CLI-injected stages

Earlier drafts let `ateam parallel` accept per-job pre/post exec hooks via CLI flags:

```
ateam parallel @file1.md @file2.md \
  --pre-exec 'builtin:create-git-worktree {{LABEL}}' \
  --pre-exec './my-script.sh {{LABEL}}' \
  --post-exec './my-other-script.py' \
  --labels job1,job2 \
  --post-prompt "pay attention to this custom instruction"
```

Combined with the file-based exec story (each `--pre-exec` either a built-in action name or a script path, with the same expansion as frontmatter items), this would have made `ateam parallel` a small workflow engine of its own.

Dropped along with frontmatter exec. `ateam parallel` stays "N prompts, shared everything." Multi-project orchestration with per-job differences goes to shell + `&` or Python.

The `--pre-prompt` / `--post-prompt` CLI flags (raw text wrap, Category A only) stay — those are content-only and useful for one-off framing without persisting.

## The "Pattern B" Python framework approach

The Python framework had two possible architectures:

- **Pattern A** — framework calls `ateam exec` per job, parallelism stays in Python. **Chosen.** Small ateam-side gap list (`--work-dir`, `--action`, ad-hoc role/action acceptance, exec_id emission), Python keeps ownership of typed config and conditional logic.
- **Pattern B** — framework hands the batch to `ateam parallel`. **Dropped.** Required all of the Phase-2 work above (per-job `--work-dir`, per-job args, `ateam flow`, per-job hooks, machine-readable per-job results). At which point the Python framework stopped carrying its weight — you'd be back in the filesystem proposal.

Pattern A is in `plans/python_framework_examples/`.

## Earlier rejected approaches

The design went through two earlier shapes before landing on the current dir-level `_pre.*.md` / `_post.*.md` scheme. Both are documented here for posterity.

### Rejected approach 1: convention-driven recursive composition

The first draft baked composition into the framework: filenames carried meaning, and the framework recursively walked the directory chain.

**Mechanism:**
- File kinds: `<name>.prompt.md` (named prompt), `prompt.md` (dir base, auto-prepended), `prompt.pre.md` / `prompt.post.md` (dir-level pre/post), `<name>.prompt.pre.md` / `<name>.prompt.post.md` (per-named pre/post).
- Recursive composition: for any prompt at path `P`, walk strict prefixes shortest→longest, then `P` itself. At each level: pre (additive across anchors) + main (first-match) + post (additive).
- `prompt.md` at a directory level was **auto-prepended** to every named prompt below it. No template directive, no opt-in.

**Why rejected:**
1. "Inserted first vs inserted last" was a forced choice. Many real wrappers (report format, output structure) belong AFTER. Splitting between `prompt.md` (before) and `prompt.post.md` (after) was awkward.
2. Hardcoded composition in the framework — "Why did the assembled prompt include X?" required knowing the recursive walk rules, not reading the files.
3. No natural home for declarative metadata (stage actions, enablement).
4. Naming explosion — ~6 file patterns with rules between them.

### Rejected approach 2: template file + `{{include}}` directives

The second draft tried to make composition explicit by putting it in a per-directory `_template.md` file that used `{{include}}` directives to orchestrate fragments.

**Mechanism:**
- `_template.md` was a structural file that wrapped each role in the directory.
- Templates used `{{include}}` directives to pull in dir-level fragments, per-role fragments, and the role body itself.
- YAML frontmatter on `_template.md` declared dir-level stage actions.

**Why rejected:**
1. Templates are opaque. To know what gets assembled, you must open the template file and read its `{{include}}` boilerplate. `ls` alone doesn't tell you anything.
2. Boilerplate cost. Every templated directory needed 5+ `{{include}}` lines.
3. Subtle ambiguity in dotted filenames. `prompt.pre.foo.md` could parse two ways.
4. `.prompt.` infix duplication. Files like `security.prompt.pre.scope.md` repeated `.prompt.` unnecessarily.

### What carried forward to the chosen approach

- Directory split between `prompts/` (config), `shared/` (cross-agent), `runtime/` (per-run).
- Anchor system (project → org → embedded).
- Auto-migration of old layouts.
- The "one rule" for filename composition (same name = overload; different name = compose).
- Template engine primitives: `{{include}}`, `{{include?}}`, `{{include_glob}}`, `{{var}}`, YAML frontmatter parsing (strict allow-list).
- Substitution variables `{{prompt.name}}`, `{{prompt.path}}`, `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`.

The chosen approach drops the template file entirely. Assembly is filename-driven: `_pre.*.md`, `_post.*.md` (dir-level, structural — marked by `_` prefix), and `<role>.prompt.md`, `<role>.pre.*.md`, `<role>.post.*.md` (role-level). The framework discovers matching files and composes them per the fixed assembly order; `{{include*}}` directives remain available inside any file for ad-hoc composition.
