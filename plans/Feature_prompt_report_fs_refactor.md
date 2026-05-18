# Feature: Prompt & artifact filesystem refactor

The immediate goal is to restructure ateam's artifacts between prompts and generated files. The longer-term design goal is to provide a generic prompt system that supports many workflows beyond `report/review/code/verify`, with the same simple core mechanics. The report/review/code/verify workflow just happens to use that more generic prompt system. Similarly, arbitrary spawned agents should have flexibility to store and read files in private and shared spaces.

## Context

Today, prompts (configuration) and generated outputs are entangled under the same trees, and the codebase carries two parallel abstractions (`roles/<NAME>/...` vs `supervisor/...`) that complicate prompt resolution, role discovery, and output promotion. `internal/runner/runner.go:1156` already has a TODO acknowledging the split is overdue: "get rid of this exclusion once configured prompts are kept separate from files."

We also want a model where logic moves easily in/out of LLM prompts, balancing token usage, determinism, and the LLM's decision power. The same system should make it cheap to specialize a generic prompt to a use case, or temporarily override behavior in a specific context (e.g. "never report on X here", "pay attention to Y").

## Goals

1. **Split prompts from generated outputs** — `prompts/` for configuration, `shared/` (and per-execution `runtime/<exec_id>/`) for generated artifacts.
2. **Generic prompt system at the core** — ateam's core understands prompts and directories, not action/role. Action/role are user-facing names that emerge from the layout.
3. **Recursive prompt-assembly mechanism** — one rule that works for any prompt at any depth; replaces `_base_prompt.md`, `_extra_prompt.md`, and the supervisor pipeline.
4. **Structural naming safety** — a user-defined role named `review` cannot collide with the singleton review action; they live in different namespaces.
5. **Stage-ready filesystem** — the layout permits adding executable pre/post steps (scripts and named built-in actions) without renaming files. The actual stage executor is a follow-up; this refactor lays the substrate.
6. **Future-friendly** — clean hooks for prompt-text `{{include}}`, CLI ad-hoc additions, and per-prompt frontmatter.

## Naming convention

```
path/to/some/NAME.prompt.md
```

`NAME.prompt.md` defines a named prompt. The path component before the basename is its containing directory.

Files inside any directory:
- `<NAME>.prompt.md` — a named prompt
- `<NAME>.prompt.pre.md` — text prepended to that specific named prompt's body
- `<NAME>.prompt.post.md` — text appended to that specific named prompt's body
- `prompt.md` — the **directory base**, auto-included before every named prompt in the directory (and recursively into subdirectories)
- `prompt.pre.md` — pre for the directory base
- `prompt.post.md` — post for the directory base, applied AFTER any named prompt's full triplet at this level

The "directory base" auto-include is what defends against the easy mistake of forgetting `{{include base}}` in a frontmatter. There is nothing to remember — placement in the directory implies inheritance.

### Identifiers

Names use forward-slash paths, no leading prefix:
- `review` — singleton named prompt at root
- `report/security` — named prompt inside `report/` directory
- `crawl/web/sitea` — deeper nesting (future)
- `report` — the directory itself (resolves to `report/prompt.md` + its pre/post)

The CLI surfaces these with a leading `:` to distinguish from raw text and file refs: `:review`, `:report/security`. The `:` syntax lives at the CLI layer; the core module accepts plain paths.

## Target layout

```
.ateam/
  config.toml
  state.sqlite
  secrets.env
  logs/
  prompts/
    prompt.pre.md            # global pre (rare)
    prompt.md                # global base (rare)
    prompt.post.md           # global post (rare)
    report/
      prompt.pre.md          # dir-level pre (prepended to every named prompt here)
      prompt.md              # dir-level base (auto-included before every named prompt here)
      prompt.post.md         # dir-level post (appended AFTER every named prompt's full triplet here)
      exec.pre.sh            # (future) dir-level pre-execution script
      exec.post.sh           # (future) dir-level post-execution script
      security.prompt.md     # named prompt
      security.prompt.pre.md # named pre (optional)
      security.prompt.post.md# named post (optional)
      security.exec.post.sh  # (future) post-execution script for this named prompt only
      test_gaps.prompt.md
      ...
    code/
      prompt.md
      security.prompt.md
      ...
    review.prompt.md          # singleton named prompts at root
    review.prompt.pre.md
    review.prompt.post.md
    code_management.prompt.md
    code_verify.prompt.md
    auto_setup.prompt.md
    exec_debug.prompt.md
    report_commissioning.prompt.md
  shared/
    report/
      security/
        security.md          # primary output (basename = prompt basename)
        ...                  # side artifacts
      test_gaps/
        test_gaps.md
    review/
      review.md
    verify/
      verify.md
    auto_setup/
      auto_setup.md           # was setup_overview.md historically
  runtime/
    <exec_id>/                # per-run scratch; default destination for prompt writes; full history by construction
```

Same restructuring applies to `.ateamorg/`, `.ateamorg/defaults/`, and the embedded `defaults/` tree.

## How a role is identified

A role is the basename of a named prompt inside a role-distributed action directory. An "action" is the directory name at the top of `prompts/`. A role exists iff `prompts/report/<role>.prompt.md` exists somewhere in the resolution chain. Singleton actions (named prompts at root) cannot be mistaken for roles — different namespaces, collision is structurally impossible.

There are no `Role` or `Action` types in the core. The CLI keeps role/action vocabulary for discoverability (`ateam report --roles security`); under the hood that maps to `Assemble("report/security")`.

## Recursive prompt-assembly mechanism

### Definitions

For each level (root, every parent directory, the named prompt itself), three file kinds may exist at every anchor:

- **Main** — `prompt.md` (dir level) or `<name>.prompt.md` (named level). **Fallback** semantics: first hit in anchor priority order wins (project → org → org-defaults → embedded).
- **Pre** — `prompt.pre.md` or `<name>.prompt.pre.md`. **Additive** across anchors (most-general first: embedded → org-defaults → org → project).
- **Post** — `prompt.post.md` or `<name>.prompt.post.md`. **Additive** across anchors (same order as pre).

Pre prepends to the level's main, post appends after the level's body (which for a directory level includes any nested levels).

### Assembly order (worked example)

For a request `report/security`, with files present at every level:

```
[CLI --pre-prompt]                     ← outermost ad-hoc (raw text)
prompts/prompt.pre.md                  ← root pre
prompts/prompt.md                      ← root main
  prompts/report/prompt.pre.md         ← dir pre
  prompts/report/prompt.md             ← dir main (auto-included before security)
    prompts/report/security.prompt.pre.md
    prompts/report/security.prompt.md
    prompts/report/security.prompt.post.md
  prompts/report/prompt.post.md        ← dir post (AFTER security's full triplet)
prompts/prompt.post.md                 ← root post
[CLI --post-prompt]                    ← outermost ad-hoc (raw text)
```

For a singleton `review`:

```
[CLI --pre-prompt]
prompts/prompt.pre.md
prompts/prompt.md
  prompts/review.prompt.pre.md
  prompts/review.prompt.md
  prompts/review.prompt.post.md
prompts/prompt.post.md
[CLI --post-prompt]
```

Each "level" pre/main/post is itself the assembled result of all anchors (pre and post additive, main fallback).

### Pseudocode

```
Assemble(name) =
    CLI.pre
  + AssembleAt("", split(name, "/"))
  + CLI.post

AssembleAt(currentDir, remaining):
    out := PreAt(currentDir) + MainAt(currentDir)
    if len(remaining) == 0:
        // referencing the directory itself
    elif len(remaining) == 1 and isNamedLeaf(currentDir, remaining[0]):
        leaf := join(currentDir, remaining[0])
        out += NamedPre(leaf) + NamedMain(leaf) + NamedPost(leaf)
    else:
        out += AssembleAt(join(currentDir, remaining[0]), remaining[1:])
    out += PostAt(currentDir)
    return out

PreAt(dir)   = concat-across-anchors(<anchor>/<dir>/prompt.pre.md)
MainAt(dir)  = first-match-across-anchors(<anchor>/<dir>/prompt.md)
PostAt(dir)  = concat-across-anchors(<anchor>/<dir>/prompt.post.md)
NamedPre(p)  = concat-across-anchors(<anchor>/<p>.prompt.pre.md)
NamedMain(p) = first-match-across-anchors(<anchor>/<p>.prompt.md)
NamedPost(p) = concat-across-anchors(<anchor>/<p>.prompt.post.md)
```

### What this replaces

- `report_base_prompt.md` → `prompts/report/prompt.md` (or `prompts/report/prompt.pre.md`)
- `code_base_prompt.md` → `prompts/code/prompt.md`
- `report_extra_prompt.md` (broad project/org level) → `prompts/report/prompt.post.md`
- `<role>/report_extra_prompt.md` (per-role) → `prompts/report/<role>.prompt.post.md`
- `supervisor/review_prompt.md` → `prompts/review.prompt.md`
- `supervisor/review_extra_prompt.md` → `prompts/review.prompt.post.md`

The current base-vs-extra distinction (single fallback for base, additive collection for extras) collapses to: **main is fallback, pre/post are additive**. Everything else falls out of the recursive walk.

## The Stage concept (framing for what comes next)

What we're really designing toward is a **stage** — the unit of "one LLM invocation with deterministic wrapping." A stage has three phases:

1. **Pre** — checks (preconditions met?) and setup (project map, git worktree, context gathering).
2. **Prompt** — the LLM call, using the assembled prompt produced by the `Assembler` (the layered system above).
3. **Post** — deterministic actions (run tests, tear down env, copy/version files, update task tracking, log telemetry).

Pre and post phases consist of ordered steps. A step is either:
- a **built-in action** referenced by name (`project-map`, `create-git-worktree`, `copy-runtime-files`, `run-tests`, `git-commit`, …) — implemented in ateam's Go code, with well-defined inputs/outputs and access to ateam's internal APIs.
- a **script** discovered on disk by filename convention (`exec.pre.sh`, `<name>.exec.post.sh`, …) — black-box subprocess that gets the run's environment and template variables.

Both kinds of step compose. Recursive resolution applies to scripts the same way it does to prompts (dir-level `exec.post.sh` runs after named-prompt-level `exec.post.sh`, etc.). Built-in actions are declared in the prompt's frontmatter (or at dir level via `prompt.md` frontmatter); the catalog of available built-ins is documented and versioned with ateam.

**This refactor does not implement the stage executor.** It establishes the substrate the executor will build on:
- a filesystem layout that already permits adding script files and frontmatter without renames or migrations
- the prompt-assembly module (`Assembler`)
- the template variables stages will need (`OUTPUT_DIR`, `SHARED_PROMPT_DIR`, …)
- the principle that **today's hidden Go-side promotion** (`promoteRuntimeFiles`) becomes an explicit built-in action (`copy-runtime-files`) in the post phase

For now, `cmd/report.go` / `cmd/review.go` / etc. keep their current "do everything in Go" shape, just calling the new `Assembler`. The stage executor is a follow-up. **There is no separate `workflow` layer in the codebase as part of this refactor** — action/role vocabulary stays user-facing without a matching internal abstraction.

### Sketch: candidate built-in actions

Not in scope for this refactor — but worth listing as a target for the follow-up. Each maps roughly to behavior ateam already performs in Go today, hidden inside the runner or per-command code.

| Action | Phase | What it does |
|---|---|---|
| `copy-runtime-files` | post | Copy `OUTPUT_DIR/*` to `SHARED_PROMPT_DIR/` (current `promoteRuntimeFiles` behavior). |
| `project-map` | pre | Generate a structured project summary (file tree, language stats, key entry points) into `OUTPUT_DIR/project_map.md`. |
| `inject-shared` | pre | Inline files from `SHARED_BASE_DIR` (e.g. all role reports, the review) into the prompt's context window. Today done inline in supervisor prompts. |
| `create-git-worktree` | pre | Create an isolated worktree for code agents; sets `WORKTREE_DIR` env var. |
| `git-commit` | post | Stage and commit changes produced by a code prompt. |
| `run-tests` | post | Run the project's test command; record pass/fail and output. |
| `validate-schema` | post | Validate the primary output against a declared JSON/Markdown schema. |
| `update-task` | post | Update a task tracker (Linear/GitHub/etc.) with the run result. |
| `log-telemetry` | post | Emit run metadata (cost, duration, tokens) to a configured sink. |

Frontmatter strawman (format TBD — YAML or TOML):

```yaml
---
pre:
  - project-map
  - inject-shared: [shared/report]
post:
  - copy-runtime-files
  - run-tests
  - on_failure: stop
---
```

## How the layers map to the design goals

The system as a whole is a **layered specialization engine**. Each design goal maps to one specific layer; new requirements should fit into an existing layer or surface a missing one (not casually grow the list).

| Goal | Layer that handles it | How |
|---|---|---|
| Project-level customization | Anchors (project / org / org-defaults / embedded) | Higher-priority anchor's `main` wins; pre/post additively layer in. |
| Cross-cutting policy for a group of prompts | Dir-level `prompt.md` + `prompt.pre.md` / `prompt.post.md` | Auto-included before every named prompt in the dir. Nothing to remember. |
| Specialization of one named prompt | `<name>.prompt.pre.md` / `<name>.prompt.post.md` at project level | Surgical additions that persist in the repo, version-controlled. |
| Temporary / one-off override | CLI `--pre-prompt` / `--post-prompt` (outermost wrap) | Doesn't persist; one run. |
| Move logic OUT of the LLM (determinism, fewer tokens) | Stage **pre** phase (built-in action or `exec.pre.sh`) | Extract structured context, gather files, summarize, validate inputs — drop into `OUTPUT_DIR/context.md` and let the prompt read it. |
| Move side effects OUT of the LLM | Stage **post** phase | Run tests, schema-validate, build, commit, copy artifacts, update tracker, log telemetry. The LLM stops being responsible for these. |
| Compose shared paragraphs across prompts | Future `{{include :name}}` | One source of truth for a paragraph reused by N prompts; resolves through anchors like any prompt. |
| Runtime-varying values (same template, different inputs per call) | Future frontmatter `params:` + CLI `--param k=v` + `{{param.k}}` | Single mechanism; defer until concrete need. |
| Inline computed values without writing a script | Future `{{exec: cmd}}` | One Templater hook; high expressiveness per implementation surface. |

Two recurring patterns that fall out:

1. **"Specialize a generic prompt"** — write the prompt as generic as it can be (embedded default), then layer specializations at the appropriate anchor / level. Use `main` overrides only when 80%+ of a prompt actually changes; use pre/post for surgical additions.
2. **"Move that bit out of the prompt"** — every time the LLM does something deterministic (parse this output, run this validator, copy this file), it's a candidate for a built-in action or script in the pre/post phase. Reduces token cost, increases reliability.

## Code structure: `internal/prompts/` (the `PromptAssembler` module)

The core abstraction is a `PromptAssembler` that knows nothing about ateam workflows. It takes anchors (filesystems with prompt trees) and assembles a named prompt. Two phases:
1. **Resolve** — produce the ordered list of files that contribute, with their anchor and role tags. Used by `--preview`.
2. **Assemble** — read the files, run each through the optional `Templater`, concatenate.

A separate `ListNamedPrompts(dir)` enumerates named-prompt basenames in a directory across anchors. This is what role discovery reduces to.

### Sketch

```go
package prompts

type Anchor struct {
    Name string  // "project", "org", "org-defaults", "embedded" — for preview/debug
    FS   fs.FS   // os.DirFS or embed.FS subtree, uniform
}

type Role uint8 // CLIPre, DirPre, DirMain, DirPost, NamedPre, NamedMain, NamedPost, CLIPost

type ResolvedFile struct {
    Anchor   string
    Path     string  // within the anchor
    PromptID string  // logical level it belongs to: "", "report", "report/security"
    Role     Role
}

type Resolution struct {
    Name  string
    Files []ResolvedFile  // in final assembly order
}

type Templater interface {
    Expand(content string) (string, error)
}

type Assembler struct {
    Anchors   []Anchor   // most-specific → least-specific
    Templater Templater  // optional
}

type AssembleOpts struct {
    CLIPre  string  // --pre-prompt (raw text, outermost)
    CLIPost string  // --post-prompt (raw text, outermost)
}

func (a *Assembler) Resolve(name string) (*Resolution, error)
func (a *Assembler) Assemble(name string, opts AssembleOpts) (string, error)
func (a *Assembler) ListNamedPrompts(dir string) ([]string, error)
```

### Design notes

- **`fs.FS` for anchors** handles disk and embedded uniformly. No special-casing.
- **No `Resolver`/`Assembler` split** — three methods on one type. `Assemble = Resolve + read + Templater.Expand + join`.
- **Templater is called per-file**, before concatenation. That way a future `{{include FOO}}` resolves in its own context.
- **Templater is an interface**, not a concrete dependency on `internal/runner/template.go`. ateam wires its concrete templater in at construction time. Tests use stubs / `fstest.MapFS`.
- **`{{include}}` hook (future)** — when added, the concrete Templater holds an `IncludeResolver` interface (a narrowed view of Assembler) so it can recursively assemble named prompts. The Assembler interface above doesn't change.
- **Outside the module:** `:` syntax parsing (CLI), workflow knowledge ("report iterates over `report/`"), action/role vocabulary, template variable values, migration of old layouts.

### What changes outside `internal/prompts/`

- `internal/root/resolve.go` — replace `RoleDir`, `RoleReportPath`, `RoleHistoryDir`, `SupervisorDir`, `ReviewPath`, `VerifyPath` with `PromptsDir()`, `SharedDir()`, `SharedPromptDir(promptName)`, `SharedPrimaryOutput(promptName)`. New helpers compute via the prompt-path mirror convention.
- `internal/runner/runner.go:1156` — drop the `*_prompt.md` exclusion in `promoteRuntimeFiles`. Update canonical destination to `SharedPromptDir(promptName)/<basename>.md`.
- `internal/runner/template.go` — add `SHARED_BASE_DIR` and `SHARED_PROMPT_DIR` template variables. Keep `OUTPUT_DIR` and `OUTPUT_FILE` unchanged. `PrimaryOutputName()` becomes `<promptBasename>.md`.
- `defaults/` — rename files into the new `defaults/prompts/...` tree; update `//go:embed`.
- `cmd/*.go` — remove `RoleID: "supervisor"` hardcodes (`cmd/review.go:233`, `cmd/code.go:278`, `cmd/auto_setup.go:83`, `cmd/verify.go:163`, `cmd/inspect.go:300`); reroute through `Assemble(name)`. Rework `cmd/prompt.go` to accept `--name` (or positional `:report/security`) and drop the `--supervisor` flag. Add `--preview` (default; prints resolved file list) and `--content` (prints assembled text). `cmd/roles.go` → `ListNamedPrompts("report")`.
- `internal/config/config.go` — `SupervisorConfig` struct stays (config.toml `[supervisor]` profile/budget keys keep their names; filesystem-only change here).
- `internal/web/handlers.go`, `internal/web/export.go` — read artifacts from `shared/<prompt-path>/<basename>.md`.

## Template variables (changes & additions)

The existing `OUTPUT_DIR` / `OUTPUT_FILE` / `EXEC_ID` / `PROFILE` / `AGENT` / `MODEL` / `PROJECT_*` / `CONTAINER_*` template variables keep their semantics. Changes and additions:

| Variable | New value | Notes |
|---|---|---|
| `{{ACTION}}` | The top-level path component of the prompt name, e.g. `report` for `:report/security`, `review` for `:review`. | Unchanged in spirit; "supervisor" goes away. |
| `{{ROLE}}` | Last path component of the prompt name. For `:report/security` → `security`. For singleton `:review` → `review`. | Matches today's per-role basename; for singletons it equals `{{ACTION}}`, which is mildly redundant but never empty (existing args like `{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}` keep working). |
| `{{PROMPT_NAME}}` (new) | Full prompt path, e.g. `report/security` or `review`. | For new templating that wants the unambiguous identifier. |
| `{{OUTPUT_FILE}}` | `OUTPUT_DIR/<prompt-basename>.md` | Was per-action mapping (`report.md`, `review.md`, `verify.md`, `execution_report.md`, `setup_overview.md`). Now uniformly derived from the prompt basename. `setup_overview.md` becomes `auto_setup.md` post-migration. |
| `{{SHARED_BASE_DIR}}` (new) | Absolute path to `.ateam/shared/` | Use sparingly. |
| `{{SHARED_PROMPT_DIR}}` (new) | Absolute path to `.ateam/shared/<prompt-path>/` | Mirrors the prompt path. Always a directory. |

**Default-destination guidance:** prompts write to `{{OUTPUT_DIR}}` (per-execution, free history). Promotion to `{{SHARED_PROMPT_DIR}}` is reserved for outputs that need to be visible to other agents (report → review, review → code_management, auto_setup → user/future agents). Until the stage executor lands, today's `promoteRuntimeFiles` Go path handles this hardcoded for known workflows; future custom prompts opt in via `copy-runtime-files` action in frontmatter or via `exec.post.sh`.

## Shared artifacts model

Two destinations for prompt output, very different semantics:

### `OUTPUT_DIR` — per-execution (default, preferred)

`OUTPUT_DIR` = `.ateam/runtime/<exec_id>/`. Every prompt run gets a fresh directory. **History falls out for free** — past runs are right there on disk under different exec IDs. The runner already plumbs `OUTPUT_DIR` and `OUTPUT_FILE` through to agents.

In most cases, this is the only destination a prompt needs. Don't reach for `SHARED_*` unless you actually need cross-agent sharing.

### `SHARED_PROMPT_DIR` / `SHARED_BASE_DIR` — cross-agent sharing (opt-in)

When an artifact must be visible to other agents (e.g. review reads role reports), it lands in the shared tree:

- `SHARED_BASE_DIR` = `.ateam/shared/`
- `SHARED_PROMPT_DIR` = `.ateam/shared/<prompt-path>/` — mirrors the prompt path. For `:report/security`, this is `.ateam/shared/report/security/`. For `:review`, `.ateam/shared/review/`.
- Primary output filename convention: `<prompt-basename>.md`. So `:report/security` → `.ateam/shared/report/security/security.md`. `:review` → `.ateam/shared/review/review.md`.

`SHARED_PROMPT_DIR` is a **directory** by design: everything related to that prompt lives in it. Primary output is the basename file; side artifacts can live alongside. No file-vs-dir-same-stem collisions because the file is inside the directory, not next to it.

### Promotion: today implicit, tomorrow explicit

Today, Go code in the runner (`promoteRuntimeFiles`) copies `OUTPUT_DIR/<file>` → canonical destination on success. This is hidden plumbing.

Direction: replace the hidden Go with explicit post-execution scripts living next to the prompts:

```
prompts/
  report/
    exec.post.sh          # runs after every report; e.g. cp -p {{OUTPUT_FILE}} {{SHARED_PROMPT_DIR}}/
    security.exec.post.sh # runs after :report/security specifically
```

Same recursive resolution as prompts: dir-level exec, per-named-prompt exec. Same template variable expansion. Each script is invoked with the run's environment.

**Multiple exec scripts per level.** A realistic post-execution involves several distinct steps: copy artifacts to shared, validate output format, build/compile, update task tracking, log telemetry. We want all of these expressible without one mega-script. Naming options to be decided:
- Sequence-prefixed: `010-copy.exec.post.sh`, `020-validate.exec.post.sh`, …
- Glob `*.exec.post.sh` runs in lexical order
- Manifest file listing scripts in order

This is **future scope** for this refactor — but the directory layout must not preclude it. Single-file exec scripts in the recursive resolution chain are the minimum we should commit to as a direction.

## Auto-migration

On `ateam` startup, when `.ateam/` or `.ateamorg/` is loaded, detect the old layout and migrate in place. Idempotent.

**Detection** (any one is enough): `.ateam/roles/` exists, `.ateam/supervisor/` exists, `.ateam/{report,code}_base_prompt.md` exists, `.ateam/{report,code}_extra_prompt.md` exists, `.ateam/setup_overview.md` at root.

**Migration map** (project-level; org-level mirrors):

| Old | New |
|---|---|
| `.ateam/roles/<R>/report_prompt.md` | `.ateam/prompts/report/<R>.prompt.md` |
| `.ateam/roles/<R>/code_prompt.md` | `.ateam/prompts/code/<R>.prompt.md` |
| `.ateam/roles/<R>/report_extra_prompt.md` | `.ateam/prompts/report/<R>.prompt.post.md` |
| `.ateam/roles/<R>/code_extra_prompt.md` | `.ateam/prompts/code/<R>.prompt.post.md` |
| `.ateam/roles/<R>/report.md` | `.ateam/shared/report/<R>/<R>.md` |
| `.ateam/roles/<R>/history/...` | dropped (history now via `runtime/<exec_id>/`) |
| `.ateam/report_base_prompt.md` | `.ateam/prompts/report/prompt.md` |
| `.ateam/code_base_prompt.md` | `.ateam/prompts/code/prompt.md` |
| `.ateam/report_extra_prompt.md` | `.ateam/prompts/report/prompt.post.md` |
| `.ateam/code_extra_prompt.md` | `.ateam/prompts/code/prompt.post.md` |
| `.ateam/supervisor/review_prompt.md` | `.ateam/prompts/review.prompt.md` |
| `.ateam/supervisor/review_extra_prompt.md` | `.ateam/prompts/review.prompt.post.md` |
| `.ateam/supervisor/code_management_prompt.md` | `.ateam/prompts/code_management.prompt.md` |
| `.ateam/supervisor/code_management_extra_prompt.md` | `.ateam/prompts/code_management.prompt.post.md` |
| `.ateam/supervisor/code_verify_prompt.md` | `.ateam/prompts/code_verify.prompt.md` |
| `.ateam/supervisor/auto_setup_prompt.md` | `.ateam/prompts/auto_setup.prompt.md` |
| `.ateam/supervisor/exec_debug_prompt.md` | `.ateam/prompts/exec_debug.prompt.md` |
| `.ateam/supervisor/report_commissioning_prompt.md` | `.ateam/prompts/report_commissioning.prompt.md` |
| `.ateam/supervisor/review.md` | `.ateam/shared/review/review.md` |
| `.ateam/supervisor/verify.md` | `.ateam/shared/verify/verify.md` |
| `.ateam/supervisor/history/...` | dropped (history via `runtime/`) |
| `.ateam/setup_overview.md` | `.ateam/shared/auto_setup/auto_setup.md` |

After migration, remove the now-empty `roles/` and `supervisor/` directories. Print a one-line notice on stderr on first migration. Implementation in a new `internal/migrate/v1_layout.go`, called from `internal/root/resolve.go` when env is first materialized.

## What this refactor does NOT change

- `runtime/<exec_id>/` per-run scratch dirs and the `OUTPUT_*` template variables — mechanism unchanged; only canonical destination paths change.
- `config.toml` schema.
- `runtime.hcl`, agent/profile/container config.
- `state.sqlite`, `secrets.env`, `logs/`, `cache/` at `.ateam/` root.

## `ateam prompt :NAME --preview` (in scope)

The `Resolve()` phase already produces the exact ordered file list. Pretty-printing it ships with the refactor — it is the maintainability story for the whole layered design. Without it, debugging "why did the assembled prompt include X?" is painful.

Output format: indented tree mirroring the assembly order, with the anchor tag in brackets next to each file. CLI ad-hoc inputs and the auto-injected Project Context line are shown in their assembly position with `[CLI]` / `[ateam]` tags.

```
$ ateam prompt :report/security --preview
Assembly for 'report/security':
  [CLI]      --pre-prompt                                       (empty)
  [ateam]    project-context
  [embedded] prompts/report/prompt.pre.md                       (dir pre)
  [embedded] prompts/report/prompt.md                           (dir main)
  [project]  prompts/report/security.prompt.pre.md              (named pre)
  [embedded] prompts/report/security.prompt.md                  (named main)
  [project]  prompts/report/security.prompt.post.md             (named post)
  [embedded] prompts/report/prompt.post.md                      (dir post)
  [CLI]      --post-prompt                                      (empty)
```

`--preview --content` dumps the actual concatenated text (replacing the current `ateam prompt` output). `--preview` alone is the structural view.

## Out of scope (deliberately deferred)

- Executable pre/post steps (`exec.pre.sh`, `exec.post.sh`) — committed as a direction (layout must permit it); implementation later.
- Multiple exec scripts per level (ordering scheme TBD).
- `{{include PROMPT_NAME}}` in prompt text — Templater interface designed to support it; implementation later.
- `{{exec: cmd}}` template directive — single Templater hook; cheap to add once needed.
- Per-prompt frontmatter (yaml/toml) for inheritance opt-out, custom primary output names, exec hooks.
- Frontmatter `params:` + CLI `--param k=v` for runtime parameterization. One mechanism only when it lands.
- Renaming `code_management` to something shorter.
- Reserved-name validation for user role IDs (structurally impossible with the new namespacing).
- Built-in prompt content changes — only file renames in this refactor.

## Pending questions / open directions

### Stage-related (drives a follow-up design pass)

1. **Built-in action catalog** — initial set, naming convention, input/output contract. Candidates from today's hidden Go: `copy-runtime-files`, `project-map`, `create-git-worktree`, `run-tests`, `git-commit`, `update-task`, `validate-schema`. Minimum useful set for v1?
2. **Frontmatter format** — YAML or TOML inside the prompt file? Where on dir-level prompts (`prompt.md`) do dir-level pre/post action declarations live?
3. **Step ordering within a phase** — built-in actions and scripts at the same level: built-ins first then scripts, or one unified ordered list?
4. **Multiple scripts per level** — sequence-prefixed (`010-copy.exec.post.sh`), glob `*.exec.post.sh`, or manifest in frontmatter?
5. **Recursive script resolution direction** — pre phase: root → leaf. Post phase: leaf → root (mirroring `prompt.post.md` being inside the dir-level post). Confirm.
6. **Failure semantics** — does a failed pre step block the prompt? Does a failed post step fail the stage? Per-step `on_failure` policy?
7. **Today's promotion behavior preserved during transition** — until the stage executor lands, `cmd/report.go` / `cmd/review.go` / `cmd/auto_setup.go` keep calling `promoteRuntimeFiles`. Once the executor lands, those promotions become explicit `copy-runtime-files` actions on the report/review/auto_setup prompts.

### Prompt-system questions (smaller)

8. **`{{include}}` resolution scope** — does it follow the included prompt's own anchor stack, or the includer's? Probably the includer's. Pin down before implementing.
9. **`SHARED_PROMPT_DIR` for the dir-itself case** — `:report` as a name refers to the dir base; `SHARED_PROMPT_DIR` would be `.ateam/shared/report/` with no primary output file. Confirm when a real use case appears.
10. **Setup overview filename** — auto-migration renames `setup_overview.md` → `auto_setup/auto_setup.md` (uniform basename rule). Acceptable break, or keep historical name?
11. **`ateam roles` output** — keep as a role-listing command, or unify under `ateam prompts list` / `ateam stages list`? CLI surface decided after the refactor lands.
12. **Specialization mechanism for runtime-varying values** — frontmatter `params:` + CLI `--param k=v` + `{{param.k}}` in body. Deferred; design when the first real need appears.

## Critical files to read before implementing

- `internal/prompts/prompts.go` — the current resolver this refactor replaces
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
2. `make test-docker` once at the end.
3. **Golden prompt test** — capture `ateam prompt --role <r> --action report` and `ateam prompt --supervisor --action review` outputs before the refactor; after, run the equivalent `ateam prompt :report/<r>` and `ateam prompt :review` and diff. Should be byte-identical modulo intentional ordering changes.
4. `ateam roles` lists the same set of roles before/after.
5. End-to-end on a fresh `./test_data/` project: `ateam init`, `ateam report --roles project.security`, verify the report lands at `.ateam/shared/report/project.security/project.security.md`. Then `ateam review`, verify it lands at `.ateam/shared/review/review.md`.
6. **Migration test** — project with old layout (artifacts plus overrides at all levels), run `ateam` once, verify behavior. Re-run to confirm idempotence.
7. **Org-override test** — both org-level and project-level overrides for the same role; 3-level fallback still works.
8. **Recursive pre/post test** — `prompts/report/prompt.pre.md` + `prompts/report/security.prompt.pre.md` + `prompts/report/security.prompt.md` + `prompts/report/prompt.post.md`; dump assembled prompt; verify ordering.
9. Manual smoke: `ateam prompt :review`, `ateam prompt :code_management`, `ateam prompt :report/project.security`.
10. **Preview tool** — `ateam prompt :report/project.security --preview` lists every contributing file with anchor tags in the exact assembly order. Run on a project with overrides at multiple levels; confirm the tags reflect where each file resolved from. `--preview --content` produces the full assembled text.
