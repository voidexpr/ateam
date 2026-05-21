# Feature: Prompt & artifact filesystem refactor

The immediate goal is to restructure ateam's artifacts between prompts and generated files. The longer-term design goal is to provide a generic prompt system that supports many workflows beyond `report/review/code/verify`, with the same simple core mechanics. The report/review/code/verify workflow just happens to use that more generic prompt system. Similarly, arbitrary spawned agents should have flexibility to store and read files in private and shared spaces.

## Context

Today, prompts (configuration) and generated outputs are entangled under the same trees, and the codebase carries two parallel abstractions (`roles/<NAME>/...` vs `supervisor/...`) that complicate prompt resolution, role discovery, and output promotion. `internal/runner/runner.go:1156` already has a TODO acknowledging the split is overdue: "get rid of this exclusion once configured prompts are kept separate from files."

We also want a model where logic moves easily in/out of LLM prompts, balancing token usage, determinism, and the LLM's decision power. The same system should make it cheap to specialize a generic prompt to a use case, or temporarily override behavior in a specific context (e.g. "never report on X here", "pay attention to Y").

## Goals

1. **Split prompts from generated outputs** — `prompts/` for configuration, `shared/` (and per-execution `runtime/<exec_id>/`) for generated artifacts.
2. **Minimal framework, conventions in filenames** — the framework provides a small set of primitives (resolution, substitution, includes, frontmatter); composition is encoded in filename patterns, not in template-file orchestration.
3. **Explicit, readable assembly** — running `ls` on a prompts directory tells you exactly what wraps each role. No template file to open, no hidden recursion to memorize.
4. **Structural naming safety** — a user-defined role named `review` cannot collide with the singleton review action; they live in different namespaces.
5. **Stage-ready** — frontmatter is the home for declarative stage metadata (pre_exec/post_exec lists, enablement, future params). The stage executor is a follow-up; this refactor lays the substrate.
6. **Future-friendly** — clean hooks for richer template directives, CLI ad-hoc additions, and per-prompt frontmatter.

## Naming convention

All files used by the framework follow a deterministic, suffix-driven pattern. The `_` prefix marks dir-level structural files; everything else is role-related.

### File patterns

| Pattern | Means |
|---|---|
| `<role>.prompt.md` | Role `<role>` main body. Invokable as `:dir/<role>` or `:<role>`. Optional YAML frontmatter. |
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

Otherwise role names are any dot-separated identifier: `security`, `code.bugs`, `project.security`, `review`, `prompt`, `pre_check`, etc.

### Parsing (deterministic, suffix-driven)

For any `*.md` file in the prompts tree, the parser picks the first matching pattern, suffix-anchored, with `<role>` greedy on the LEFT:

1. Ends with `.prompt.md` → role main, role = everything before `.prompt.md`.
2. Ends with `.pre.md` or `.pre.<NAME>.md` → role pre, role = everything before the final `.pre`.
3. Ends with `.post.md` or `.post.<NAME>.md` → role post, role = everything before the final `.post`.
4. Filename is `_pre.md` or `_pre.<NAME>.md` (no role prefix) → dir-level pre.
5. Filename is `_post.md` or `_post.<NAME>.md` → dir-level post.
6. Otherwise → arbitrary include, ignored by the framework parser.

Role-name restrictions are validated after parsing. Violations error at load with a clear message.

### Identifiers

Names use forward-slash paths, no leading prefix:
- `review` — singleton named prompt at root (file: `review.prompt.md`)
- `report/security` — named prompt inside `report/` directory (file: `report/security.prompt.md`)
- `code.bugs` — dotted role names work (file: `code.bugs.prompt.md`)
- `crawl/web/sitea` — deeper nesting (future)

The CLI surfaces these with a leading `:` to distinguish from raw text and file refs: `:review`, `:report/security`. The `:` syntax lives at the CLI layer; the core module accepts plain paths.

`:dir-name` without a leaf (e.g. `:report`) is **not** an invokable name — directories are namespaces, not prompts.

## Target layout

```
.ateam/
  config.toml
  state.sqlite
  secrets.env
  logs/
  prompts/
    _pre.intro.md            # root-level pre, composes with other _pre.*.md
    _post.disclosure.md      # root-level post
    review.prompt.md         # singleton role
    review.pre.format.md     # (optional) per-role pre fragment
    auto_setup.prompt.md
    code_management.prompt.md
    code_verify.prompt.md
    exec_debug.prompt.md
    report_commissioning.prompt.md
    report/
      _pre.format.md         # report dir-level pre — applies to every role in report/
      _pre.severity.md       # composes lexically with above
      _post.checklist.md     # report dir-level post
      security.prompt.md     # role main
      security.pre.scope.md  # (optional) per-role pre fragment
      security.post.format.md# (optional) per-role post fragment
      test_gaps.prompt.md
      code.bugs.prompt.md    # dotted role names are fine
      code.bugs.pre.realistic.md
      ...
      gather-deps.sh         # arbitrary script; referenced from frontmatter
      validate.sh
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
    history/                 # reserved
  runtime/
    <exec_id>/               # per-run scratch, the default destination for prompt writes
```

Same restructuring applies to `.ateamorg/` and the embedded `defaults/` tree. (The previously planned `.ateamorg/defaults/` tier is dropped — updating embedded defaults now requires a rebuild, which is acceptable since defaults change infrequently and keeping a separate runtime defaults tier added complexity without saving much.)

## Framework primitives (the whole list)

The framework provides exactly these primitives. Composition is built into the naming convention — there is no template file to author.

1. **Prompt resolution by name across anchors.** Anchors ordered most-specific to least: project → org → embedded. Fallback semantics (first hit wins).
2. **Filename-based assembly.** Given a prompt name `<dir>/<role>`, the assembler discovers all matching files (role main, role pre/post fragments, dir-level pre/post fragments at every directory up the chain) and composes them per the assembly order below. No template file mediates this.
3. **`{{var}}` substitution.** Existing template variables (`{{PROJECT_*}}`, `{{OUTPUT_*}}`, `{{EXEC_ID}}`, `{{ATEAM_OWN_*}}`, `{{CONTAINER_*}}`, etc.) plus new `{{prompt.name}}`, `{{prompt.path}}`, `{{LABEL}}`, `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`.
4. **`{{include PATH}}`** — inline a file's content at this position. **First-match across anchors.** Error if no anchor has the file.
5. **`{{include? PATH}}`** — inline a file's content. **First-match across anchors.** Produces empty string if no anchor has it.
6. **`{{include_glob PATTERN}}`** — inline files matching a glob, in deterministic order: within each anchor sorted lexically; across anchors most-general first (embedded → org → project). Empty string if no matches. Used internally by the assembler to find pre/post fragments; can also be called explicitly from any markdown file.
7. **YAML frontmatter parsing** on `<role>.prompt.md` and on dir-level structural files. Strict schema; unknown keys reject with a clear error.

### One rule for file composition across anchors

**Same filename always overloads** — embedded's `_pre.intro.md` and project's `_pre.intro.md` are the same file at different anchors; first-match (project) wins. Never additive.

**Different filenames compose** — if you want multiple fragments to all contribute, give them distinct names. Embedded ships `_pre.intro.md`, org adds `_pre.org_policy.md`, project adds `_pre.local.md`. They are three different files; the assembler picks up all three (lexical order within each anchor, embedded → org → project across anchors). Authors can prefix numerically (`01_`, `02_`) to control order explicitly.

This is one rule for the whole system: **filenames are the unit of overloading; sets of related filenames are the unit of composition**. Intent is explicit in the filename.

### Assembly order for `:dir1/dir2/role`

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

Each `_pre.*.md` / `_post.*.md` slot expands to all matching files at that directory level, composed across anchors per the rule above. Role-level pre/post is just inside the innermost dir-level pre/post.

### Substitution inside include paths

Include paths may contain `{{var}}` substitutions. Resolution is two-pass:

1. Substitute `{{var}}` inside the include path text.
2. Resolve the include against anchors.

So `{{include_glob {{prompt.name}}.pre.*.md}}` resolves `{{prompt.name}}` first (e.g. to `security`), then finds all files matching `security.pre.*.md` across all anchors and inlines them (within-anchor lexical order, embedded → org → project across anchors). The assembler uses `{{include_glob}}` internally to find pre/post fragments at every directory level; the same primitive is available inside any markdown file for ad-hoc composition.

### Cycles, depth, errors

- `{{include}}` recursion is depth-limited (e.g. 16 levels). Cycle detection errors at preview/assembly time.
- `{{include? }}` produces empty on missing; never errors for missing files.
- Invoking a directory name without a leaf (e.g. `:report` instead of `:report/security`) errors at preview time — directories are namespaces, not invokable prompts.
- YAML frontmatter parse errors are loud at preview time.

### Orphan-fragment detection (catches typos)

At preview/load time, the assembler walks every file matching `<role>.pre.md`, `<role>.pre.<NAME>.md`, `<role>.post.md`, `<role>.post.<NAME>.md` across all anchors. For each, the parsed `<role>` is checked against the set of known roles (basenames of `<role>.prompt.md` files at any anchor). If no matching `<role>.prompt.md` exists anywhere, error:

```
orphan fragment: report/securty.pre.scope.md
  no matching report/securty.prompt.md found in any anchor
  did you mean: security?
```

Levenshtein hint when the base name is close to an existing prompt. Catches the typo failure mode cheaply.

Dir-level fragments (`_pre.md`, `_post.md`, `_pre.<NAME>.md`, `_post.<NAME>.md`) are not subject to this check — they don't pair with any specific named prompt.

## Composition: dir-level and role-level wrappers (replaces the template concept)

There is no `_template.md` file. The assembly order is **encoded in the filenames** and the directory chain. Reading `ls` of a directory tells you exactly what wraps each role; opening any single file is enough to understand its content.

### What gets assembled for `:report/security`

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

Each slot expands to all files matching the pattern at that anchor, composed in lexical order within an anchor, embedded → org → project across anchors.

### What ateam ships in embedded defaults

The embedded defaults seed concrete files for the standard workflows.

**Root level** (`defaults/prompts/`):
- `_pre.context.md` — block containing `{{PROJECT_INFO}}` (so every prompt gets project context).
- `review.prompt.md` — review singleton (body references `{{ROLE_REPORTS}}`).
- `auto_setup.prompt.md`, `code_management.prompt.md`, `code_verify.prompt.md`, `exec_debug.prompt.md`, `report_commissioning.prompt.md` — other singletons.

**Report directory** (`defaults/prompts/report/`):
- `_pre.intro.md` — "You are performing a {{prompt.name}} report on this project. Your speciality and approach:"
- `_post.format.md` — "Format your findings as severity-tagged markdown sections, scoped by file path. Use this scale: blocker / high / medium / low. Skip findings without a realistic exploit path or measurable impact."
- `security.prompt.md`, `test_gaps.prompt.md`, `code.bugs.prompt.md`, etc. — pure role bodies (no boilerplate).

Reading `ls defaults/prompts/report/` tells you: there's a shared intro applied to every report, a shared format applied to every report, and N role-specific files. No file to open to "understand the wrap."

**Code directory** (`defaults/prompts/code/`):
- Similar shape, with code-specific framing.
- The `code_management` singleton's body uses `{{include shared/review/review.md}}` directly (first-match, errors if missing) — replacing today's `cmd/code.go` read+inline-or-bail logic.

### What a role file looks like

`defaults/prompts/report/security.prompt.md`:
```
Focus on confirmed exploitable bugs and data exposure with realistic triggers.
Refuse to break working features with defensive header tightening.
```

No frontmatter, no boilerplate. Pure body content. The dir-level `_pre.*.md` and `_post.*.md` files at every level above are applied automatically per the assembly order.

### Project-level customization patterns

**Recommended (surgical, upgrade-safe):**
- Add `report/security.pre.<NAME>.md` at project or org anchor → composes into security's pre alongside whatever embedded ships. Use a meaningful `<NAME>` (e.g. `local_scope`, `policy`, `01_priority`). Numeric prefixes control order.
- Add `report/_post.<NAME>.md` at project or org anchor → applies to every role in `report/` in this project/org.
- Add `_pre.<NAME>.md` at the prompts root → applies to every prompt in every dir.
- Add a brand-new `report/<my-custom>.prompt.md` → custom role. No upgrade conflict because there's no embedded version.
- Frontmatter on a role's `<role>.prompt.md` adds `pre_exec` / `post_exec` actions for that role (additive to dir-level frontmatter, if any).

**Avoid (drift risk on ateam upgrade):**
- Overriding `report/security.prompt.md` wholesale at project anchor — you'll lose embedded improvements when ateam upgrades. If you genuinely need a different security role, fork it under a different name (e.g. `security_strict.prompt.md`) — that becomes a custom role with no upgrade risk.
- Overriding `report/_pre.intro.md` wholesale unless you actually want to replace the shipped intro entirely.

Each recommended pattern is a single file drop. None affects others.

### Why this scheme

- **`ls` is documentation.** A directory's contents tell you everything that wraps the roles in it. No template file mediates.
- **Forgot-an-include is impossible.** The assembler walks the directory chain and discovers all matching files automatically. You can't forget what you didn't have to write.
- **Fixed structure where it counts.** Header → role pre → role main → role post → footer at every level. You give up the ability to invent custom shapes; you gain readability.
- **Flexibility is preserved via `{{include}}`.** Inside any file (header, role body, footer), you can `{{include}}` other content for complex composition. Same template engine, used inside slots instead of orchestrating them.

## How a role is identified

A role is the basename `<role>` of a file matching `<role>.prompt.md`. An "action" is the directory name at the top of `prompts/`. A role exists iff `prompts/<action>/<role>.prompt.md` exists somewhere in the resolution chain. Singleton actions (named prompts at root) live alongside roles in different dirs; they cannot collide.

There are no `Role` or `Action` types in the core. The CLI keeps role/action vocabulary for discoverability (`ateam report --roles security`); under the hood that maps to `Assemble("report/security")`.

## Two kinds of "extra work" around a prompt

The system distinguishes two fundamentally different categories of work that ateam performs around an LLM invocation. They must not be conflated.

### A. Assembly-time content injection — output goes INTO the prompt

Data the agent reads. Computed during prompt assembly. Saves the agent from doing the work itself (or makes it possible for the agent to act on context it couldn't otherwise see).

Mechanisms in v1:
- **Template variables** — `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`, `{{PROJECT_NAME}}`, `{{ATEAM_OWN_*}}`, etc. ateam computes these once per env and inlines them where referenced.
- **Includes** — `{{include FILE}}` (required, first-match), `{{include? FILE}}` (optional, first-match), `{{include_glob PATTERN}}` (composes all matches across anchors). Inline file content.

Future addition (deferred): `{{shell CMD}}` directive for user-defined scripts whose stdout becomes prompt content. Inert during preview only if explicitly marked side-effect-free; otherwise preview is the only safe way to run them.

**Key property:** these run during preview. `ateam prompt :NAME --preview --content` shows the actual assembled prompt — that means assembly-time content has been computed. Anything in this category must be **idempotent and side-effect-free**.

### B. Pre / post execution hooks — output does NOT go into the prompt

Work done before or after the agent runs. Output is captured as logging only — never seen by the agent in its prompt. Used for environment setup, post-run validation, and side effects.

Mechanisms in v1: ordered steps declared in YAML frontmatter. Dir-level lists live in `_meta.yaml` (or another agreed-on dir-level metadata file — see open question below); role-level lists live in `<role>.prompt.md` frontmatter. A step is either:
- a **built-in action** referenced by name (e.g. `copy-runtime-files`, `concurrent-run-check`) — implemented in ateam's Go code.
- a **script path** relative to the file's directory — black-box subprocess that gets the run's environment.

**Key property:** these do NOT run during preview. Preview only shows what *would* run. Side effects are expected and acceptable at run time (creating dirs, copying files, running tests, committing).

### Where past brainstorming conflated them

Earlier drafts put `project-info` and `discover-reports` in `pre_exec`. That was wrong: their **output is part of the prompt content**, not the runtime environment. They belong in category A (template variables / includes), not B (frontmatter exec lists). The action catalog below is corrected accordingly.

The two categories are connected at the run level (a pre-exec script might write a file that an `{{include}}` later picks up), but the mechanisms are separate and live in different parts of the spec.

## The Stage concept (framing for what comes next)

A **stage** is the unit of "one LLM invocation with deterministic wrapping." A stage has three phases:

1. **Pre** — environment setup, gating checks. From frontmatter `pre_exec` list.
2. **Prompt** — the LLM call, using the assembled prompt. Assembly-time content (category A above) is already baked into the prompt text at this point.
3. **Post** — runtime validation, copy/version files, update tracking, log telemetry. From frontmatter `post_exec` list.

Pre and post lists are ordered. Composition: per-role frontmatter lists **extend** dir-level lists. Order in the YAML list is the execution order.

### Where does dir-level frontmatter live?

Since there's no `_template.md` file anymore, dir-level `pre_exec` / `post_exec` declarations need a home. **Open question** — options:
- (a) On any dir-level structural file (`_pre.md`, `_post.md`, `_pre.<NAME>.md`, `_post.<NAME>.md`) — frontmatter merged across all such files at the dir level.
- (b) A dedicated `_meta.yaml` (or `_meta.md` with only frontmatter) per directory, one source of truth.
- (c) Only on `<role>.prompt.md` — no dir-level declarations; users must replicate per role.

(b) is cleanest (single source per dir, separates structure from content); (a) is most consistent with the "filenames carry meaning" theme. Pin down before implementation.

Per-role frontmatter on `<role>.prompt.md`:
```yaml
---
pre_exec: [./security-extra-setup.sh]
post_exec: [./security-extra-validate.sh]
---
Focus on confirmed exploitable bugs...
```

Assuming option (b) — `report/_meta.yaml`:
```yaml
pre_exec: [concurrent-run-check, source-writable, ./gather-deps.sh]
post_exec: [copy-runtime-files, ./validate.sh]
```

Assembling `:report/security` then runs `pre_exec` = `[concurrent-run-check, source-writable, ./gather-deps.sh, ./security-extra-setup.sh]` and `post_exec` = `[copy-runtime-files, ./validate.sh, ./security-extra-validate.sh]`. None of these contribute to prompt text.

**This refactor does not implement the stage executor.** It establishes the substrate:
- The filesystem layout where actions get declared (frontmatter).
- The prompt-assembly engine (`{{include}}`, substitution, anchors).
- Today's hidden Go-side promotion will become an explicit `copy-runtime-files` action.

For now, `cmd/report.go` / `cmd/review.go` / etc. keep their current shape, just calling the new `Assembler`. The stage executor is a follow-up.

### Where the line is: stage actions vs. runner core

- **Stage action** = business logic specific to a prompt or family of prompts. Express-able as a discrete, named, opt-in step. Could realistically live in a script.
- **Runner core** = infrastructure plumbing every agent invocation needs. Stays hardcoded in `internal/runner/`. Not user-configurable.

### Today's hardcoded Go logic, re-sorted by category

What ateam does in Go today around an invocation, split by which mechanism in the new world handles it.

**Category A — Assembly-time content injection (becomes template variables or includes)**

| Today's hardcoded Go | Where (file:line) | New-world mechanism |
|---|---|---|
| `gitutil.GetProjectMeta` + `FormatProjectInfo()` → git HEAD + uncommitted-files block prepended to every prompt | `internal/root/resolve.go:91-121`, called by every `cmd/*.go` | `{{PROJECT_INFO}}` template variable (computed once per env, cached) |
| `prompts.DiscoverReports` → scan `shared/report/*/<name>.md`, filter by `--roles/--all/--max-age`, inline as "# Role Reports" section into the review prompt | `cmd/review.go:177-184`, `prompts/prompts.go:209-242` | `{{ROLE_REPORTS}}` template variable (provided by ateam; encapsulates discovery + filtering + inlining) |
| `cmd/code.go:158-171` reads `shared/review/review.md`, errors if missing, inlines into code_management prompt | `cmd/code.go:158-171` | `{{include /shared/review/review.md}}` in the `code_management.prompt.md` body — first-match (errors if missing), inlines content. No special action needed. |

These do NOT belong in `pre_exec` frontmatter. They are part of the prompt's content; they are computed during assembly; they have no effect on the runtime environment.

**Category B — Pre-execution hooks (stays as `pre_exec` actions)**

| Action | What it does today | Where (file:line) | Universal or per-prompt |
|---|---|---|---|
| `concurrent-run-check` | Query DB for live processes matching project+action+role; block unless `--force`. | `cmd/table.go:921-948` | report / code / verify / review |
| `budget-precheck` | Gate new dispatches against accumulated batch cost. | `cmd/table.go:825-849` | report / code / verify (batch mode) |
| `source-writable` | Flag container to allow writes to project source. | `cmd/table.go:905-911` | code / verify / auto_setup / inspect |

**Category B — Post-execution hooks (stays as `post_exec` actions)**

| Action | What it does today | Where (file:line) | Universal or per-prompt |
|---|---|---|---|
| `copy-runtime-files` | Copy every file from `runtime/<exec_id>/` to `SHARED_PROMPT_DIR/`. | `internal/runner/runner.go:1150-1199` | report / review / auto_setup |
| `chain-next` | Optional next stage on success (`report --review` → `ateam review`; `code` → `ateam verify` unless `--no-verify`). | `cmd/report.go:354-356`, `cmd/code.go:347-365` | report (opt-in), code (default-on) |

That's the v1 catalog. Nothing speculative.

### Aspirational additions (real new capabilities, not pull-outs)

Listed for completeness; design when there's concrete need.

**Category A (future template variables / directives)**

| Mechanism | What it would do |
|---|---|
| `{{PROJECT_MAP}}` | Richer than `{{PROJECT_INFO}}`: file tree, language stats, key entry points. Template variable. |
| `{{shell CMD}}` directive | Run a script during assembly, inline stdout into the prompt. For user-provided context-gathering scripts. |
| `{{include /SHARED/...}}` | Already covered by the include primitive; just convention. |

**Category B (future pre/post-exec actions)**

| Action | Phase | What it would do |
|---|---|---|
| `create-git-worktree` | pre | Isolated worktree for code agents. |
| `git-commit` | post | Stage and commit code changes. |
| `run-tests` | post | Run project test command; record pass/fail. |
| `validate-schema` | post | Validate primary output against a declared schema. |
| `update-task` | post | Push run result to a task tracker. |
| `log-telemetry` | post | Emit run metadata to a configured sink. |

### What stays in the runner core (never a stage action)

Infrastructure every agent invocation needs.

**Pre-execution:**
- Allocate `exec_id` and insert call row (`InsertCall`)
- Create `logs/<exec_id>/` and `runtime/<exec_id>/` directories
- Container preparation, sandbox JSON generation, `settings.json` materialization
- Template-variable resolution (the engine itself)
- Resolved-prompt archival to `logs/<exec_id>/prompt.md`
- Initial `cmd.md` render with run context

**Post-execution:**
- Event-stream processing (assistant / thinking / tool_use / tool_result / result / error)
- Cost & token accounting; budget enforcement
- PID recording for liveness tracking
- `output-fallback`: seed `runtime/<id>/<primary>.md` if the agent didn't write the primary file
- `finalizeCall` (success determination)
- `classifyFailure` (timeout / cancellation / agent error / no-result)
- `appendStderrSummary`
- Final `cmd.md` re-render
- `UpdateCall` (EndedAt, DurationMS, ExitCode, IsError, costs, tokens, model, peak context, git end hash)
- Progress channel finalization

### What's already in templates (and not Go logic anymore)

`{{PROJECT_*}}`, `{{OUTPUT_*}}`, `{{ATEAM_OWN_*}}`, `{{ROLE}}`, `{{ACTION}}`, `{{BATCH}}`, `{{TIMESTAMP}}`, `{{PROFILE}}`, `{{EXEC_ID}}`, `{{AGENT}}`, `{{MODEL}}`, `{{CONTAINER_*}}`. Resolved in `internal/runner/template.go:37-68`.

### Things checked but not present today

- Git worktree creation in Go
- History archival
- Container teardown / `defer c.Stop()`
- Auto-commit / PR creation in Go
- Log rotation / cleanup

## Ad-hoc prompts and CLI-injected stages

For lightweight workflows the user doesn't want to set up a full dir structure with role files. Common case: `ateam parallel` over a few hand-written prompt files, with shared orchestration declared via CLI flags.

```
ateam parallel @file1.md @file2.md \
  --pre-exec 'builtin:create-git-worktree {{LABEL}}' \
  --pre-exec './my-script.sh {{LABEL}}' \
  --post-exec './my-other-script.py' \
  --labels job1,job2 \
  --post-prompt "pay attention to this custom instruction"
```

This pattern needs the following spec additions.

### Prompt sources

A prompt can come from three places, with the same assembly pipeline:

1. **Named prompt**: `:name` or `:dir/name` — resolved through the anchor system.
2. **File reference**: `@path/to/file.md` — absolute or relative path. Frontmatter is parsed if present.
3. **Inline text or stdin** — passed as a positional argument or piped.

For (2) and (3) there is no dir-level wrap — the prompt is **standalone**. The file body (after frontmatter, if any) is what the agent sees, plus the outermost CLI pre/post wrappers and any CLI-injected pre/post-exec actions.

`{{prompt.name}}` for ad-hoc prompts defaults to the file's basename without extension (`file1` for `@file1.md`), or the `--label` value if provided, or empty for raw inline/stdin.

### CLI-injected stage actions

Two new flag families mirror the frontmatter declarations:

- `--pre-exec ACTION` (repeatable) — appended to whatever `pre_exec` the dir-level and role-level frontmatter declare. CLI items run **after** frontmatter-declared items in the pre phase (so user CLI hooks see the standard setup already done).
- `--post-exec ACTION` (repeatable) — appended to `post_exec`. Same ordering.

CLI items go through the same expansion as frontmatter items (substitutions, builtin-vs-script resolution). Existing `--pre-prompt TEXT` / `--post-prompt TEXT` keep their meaning (outermost raw-text wrap).

**Action vs script syntax.** Bare names resolve against the built-in actions registry (`concurrent-run-check`, `copy-runtime-files`, etc.). Path-like values (starting with `./`, `/`, or `~`) are scripts. The explicit `builtin:NAME` prefix is supported for clarity (and matches the user's mental model in mixed examples).

**Script path resolution.** Differs by source:
- Frontmatter in dir-level metadata or `<role>.prompt.md`: relative paths resolve from the file's directory.
- CLI `--pre-exec` / `--post-exec`: relative paths resolve from the user's CWD (where they invoked `ateam`).
- This matches user expectation: prompt-tree-declared scripts ship with the prompts; CLI-injected scripts are project-local.

### Labels (`--labels`)

`ateam parallel` accepts `--labels A,B,C` matching the order of positional prompt sources. Within each job, `{{LABEL}}` resolves to that job's label.

If `--labels` is omitted, defaults are:
- For `@file.md`: the file's basename (`file1` from `@file1.md`).
- For inline/stdin: positional index (`job-1`, `job-2`, …).
- For `:named/prompt`: the prompt's last path component (matches `{{prompt.name}}`).

`{{LABEL}}` is distinct from `{{prompt.name}}`. `prompt.name` identifies the prompt CONTENT (source); `LABEL` identifies the JOB within a parallel batch. For `ateam parallel @site-a.md @site-b.md --labels east,west`, prompt 1 has `prompt.name = site-a`, `LABEL = east`.

Pre-exec / post-exec scripts referencing `{{LABEL}}` get the per-job substitution. Concretely, this makes the worktree-per-job pattern trivial: `--pre-exec 'builtin:create-git-worktree myproj-{{LABEL}}'` creates one worktree per parallel job.

### Final composition for a run

For any run (built-in workflow or ad-hoc), the final `pre_exec` list is:

1. Items from the dir-level frontmatter (if any).
2. Items from the named prompt's frontmatter (if any).
3. Items from `--pre-exec` CLI flags (in order).

Same shape for `post_exec`. Same general layering for the prompt-text wrap (CLI `--pre-prompt` outermost, then assembled prompt body, then CLI `--post-prompt`).

### Why not put orchestration in each file?

For one or two ad-hoc files, frontmatter in each works. For "N similar jobs with the same hooks," the CLI form is the right place: one declaration covers N jobs, and each job's per-job substitutions (`{{LABEL}}`) flow naturally. For ateam's built-in workflows (report, code, review), the orchestration lives in the shipped dir-level metadata — same mechanism, different home.

### Inline frontmatter on stdin / heredoc

When the prompt source is stdin or a heredoc, standard YAML frontmatter works:

```
ateam exec <<EOF
---
pre_exec: [./my_script.sh {{prompt.name}}]
post_exec: [./my_other_script.sh]
---
{{include ./local_file.md}}

do something

{{include? ./custom_instructions.md}}
EOF
```

Whether this is heavily used is an open question (the user noted: "for this use case it's better to just run the pre_exec scripts before/after `ateam exec`"). The framework supports it because frontmatter is uniform across all prompt sources, but the killer use case is `ateam parallel` with CLI flags, not single-shot inline.

## How the layers map to the design goals

The system is a **layered specialization engine**. Each design goal maps to exactly one layer; new requirements should fit an existing layer or surface a missing one.

| Goal | Layer | How |
|---|---|---|
| Project-level customization | Anchors (project / org / embedded) | Higher-priority anchor's `main` wins; `{{include?}}` is additive across anchors. |
| Cross-cutting policy for a group of prompts | Dir-level `_pre.*.md` / `_post.*.md` files | Composed into every role's assembly automatically based on directory placement. |
| Specialization of one named prompt | Project-anchor `<role>.pre.<NAME>.md` / `<role>.post.<NAME>.md` fragments | Surgical additions persisted in the repo; multiple distinct files compose across anchors. |
| Temporary / one-off override | CLI `--pre-prompt` / `--post-prompt` (outermost wrap, raw text) | Doesn't persist; one run. |
| Inject deterministic context INTO the prompt (save the agent the work) | Category A: template variables (`{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`, future `{{shell CMD}}`) and `{{include}}` directives. Computed during assembly. | Idempotent, side-effect-free; runs during preview too. |
| Set up the runtime environment BEFORE the agent runs | Category B: stage **pre** phase (frontmatter `pre_exec`) | concurrent-run-check, budget-precheck, source-writable, future worktree creation. Side effects expected; does NOT run during preview. |
| Side effects AFTER the agent runs (artifacts, tests, commits, telemetry) | Category B: stage **post** phase (frontmatter `post_exec`) | copy-runtime-files, chain-next, run tests, schema-validate, build, commit, update tracker. |
| Compose shared paragraphs across prompts | `{{include :name}}` | One source of truth for shared content; resolves through anchors. |
| Runtime-varying values (same template, different inputs) | Future frontmatter `params:` + CLI `--param k=v` + `{{param.k}}` | Single mechanism; defer until concrete need. |
| Filter "run all enabled" prompts | Future frontmatter `enabled_from:` on dir-level metadata file (delegates to a source like `config.toml`) | Keeps prompt-system content-only; enablement is workflow metadata. |

Two recurring patterns:

1. **Specialize a generic prompt.** Write the prompt as generic as possible (embedded default), layer specializations via project-anchor fragments. The dir-template's `{{include?}}` slots are the standard specialization points.
2. **Move that bit out of the prompt.** Every deterministic operation is a candidate for `pre_exec` / `post_exec`. Reduces token cost, increases reliability.

## Code structure: `internal/prompts/` (the `PromptAssembler` module)

The core abstraction is a `PromptAssembler` that knows nothing about ateam workflows. Given a prompt name `<dir>/<role>`, it walks the directory chain from root to leaf, discovers all matching `_pre.*.md`, `_post.*.md`, `<role>.pre.*.md`, `<role>.prompt.md`, `<role>.post.*.md` files across anchors, and composes them per the assembly order. It also expands `{{include}}` / `{{include?}}` / `{{include_glob}}` directives inside any included content.

### Sketch

```go
package prompts

type Anchor struct {
    Name string  // "project", "org", "embedded" — for preview/debug
    FS   fs.FS   // os.DirFS or embed.FS subtree, uniform
}

type Templater interface {
    // Expand variables in content; handles {{var}} but NOT includes.
    // Includes are handled by the Assembler itself (it owns include resolution).
    Expand(content string) (string, error)
}

type Frontmatter struct {
    PreExec    []string `yaml:"pre_exec"`
    PostExec   []string `yaml:"post_exec"`
    // Future: EnabledFrom, Params, etc. Strict allow-list of keys.
}

type ResolvedFile struct {
    Anchor   string
    Path     string  // within the anchor
    Slot     string  // "dir-pre" | "dir-post" | "role-pre" | "role-main" | "role-post" | "cli-pre" | "cli-post"
    Depth    int     // 0 = root, 1 = first subdir, etc. (for dir-level slots)
}

type Resolution struct {
    Name        string
    Files       []ResolvedFile  // ordered as they contribute to the final text
    Frontmatter Frontmatter     // merged: all dir-level + role-level
}

type Assembler struct {
    Anchors   []Anchor
    Templater Templater
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

- **`fs.FS` for anchors** handles disk and embedded uniformly.
- **No `Resolver`/`Assembler` split** — three methods on one type.
- **`{{include*}}` directives are handled by the Assembler**, not the Templater. The Templater handles only variable substitution. Include resolution requires anchor knowledge.
- **Assembly is filename-driven** — no template file orchestrates it. The Assembler discovers `_pre.*.md`, `_post.*.md`, and role pre/main/post files by pattern.
- **Two-pass expansion** for `{{include* ... {{var}} ...}}` paths: variable substitution inside include paths first, then resolve includes, then final variable substitution.
- **`ListNamedPrompts(dir)`** is what role discovery reduces to. Filters by pattern; returns invokable basenames (excludes dir-level structural files).
- **Orphan-fragment validation** runs at load: every `<role>.pre.*.md` and `<role>.post.*.md` requires a matching `<role>.prompt.md`.
- **Outside the module:** `:` syntax parsing (CLI), workflow knowledge, action/role vocabulary, template variable values (Templater wraps these), migration of old layouts, stage execution (follow-up refactor).

### What changes outside `internal/prompts/`

- `internal/root/resolve.go` — replace path helpers with prompt-name-based lookups.
- `internal/runner/runner.go:1156` — drop the `*_prompt.md` exclusion in `promoteRuntimeFiles`. Update canonical destination to `SharedPromptDir(promptName)/<basename>.md`.
- `internal/runner/template.go` — add `SHARED_BASE_DIR`, `SHARED_PROMPT_DIR`, `PROJECT_INFO`, `ROLE_REPORTS`, `LABEL` template variables. `PrimaryOutputName()` becomes `<promptBasename>.md`.
- `defaults/` — rename files into the new tree; ship `_pre.*.md` / `_post.*.md` defaults; update `//go:embed`.
- `cmd/*.go` — remove `RoleID: "supervisor"` hardcodes; route through `Assemble(name)`. Rework `cmd/prompt.go` to accept positional `:report/security` and `--preview` / `--content` flags. Add `--pre-exec`, `--post-exec`, `--labels`.
- `internal/config/config.go` — `SupervisorConfig` struct stays; filesystem-only change.
- `internal/web/handlers.go`, `internal/web/export.go` — update artifact read paths.

## Template variables (changes & additions)

The existing `OUTPUT_DIR` / `OUTPUT_FILE` / `EXEC_ID` / `PROFILE` / `AGENT` / `MODEL` / `PROJECT_*` / `CONTAINER_*` template variables keep their semantics. Several new variables are introduced; some replace previously hardcoded Go content-injection.

### Identity vars

| Variable | New value | Notes |
|---|---|---|
| `{{ACTION}}` | Top-level path component of the prompt name. `report` for `:report/security`; `review` for `:review`. | "supervisor" goes away. |
| `{{ROLE}}` | Last path component. `security` for `:report/security`; `review` for singleton. | Keeps current basename semantics; never empty. |
| `{{prompt.name}}` (new) | Same as `{{ROLE}}` — last path component. | Preferred inside templates. |
| `{{prompt.path}}` (new) | Full prompt path, e.g. `report/security`. | Unambiguous identifier. |
| `{{LABEL}}` (new) | Per-job label inside a parallel batch (from `--labels`, or default by source). Distinct from `{{prompt.name}}`. Empty for single-shot non-parallel runs. | Used in pre/post-exec scripts to disambiguate per-job state (worktree names, output directories). |

**Future direction: dotted namespaces.** New variables will tend toward `namespace.attribute` form (`prompt.name`, `prompt.path`, future `git.repo_name`, `git.branch`, `arg.X` for CLI-passed params). Existing ALL_CAPS variables (`PROJECT_NAME`, `OUTPUT_DIR`, etc.) stay as-is for backward compat — no renames in this refactor.

### Output / artifact vars

| Variable | New value | Notes |
|---|---|---|
| `{{OUTPUT_FILE}}` | `OUTPUT_DIR/<prompt-basename>.md` | Uniformly derived. `setup_overview.md` becomes `auto_setup.md` post-migration. |
| `{{SHARED_BASE_DIR}}` (new) | Absolute path to `.ateam/shared/` | Use sparingly. |
| `{{SHARED_PROMPT_DIR}}` (new) | Absolute path to `.ateam/shared/<prompt-path>/` | Always a directory. |

### Assembly-time content injection (was hardcoded Go)

| Variable | Expansion | Replaces |
|---|---|---|
| `{{PROJECT_INFO}}` (new) | Formatted block: git HEAD hash, uncommitted-files list. Computed once per env, cached. | Today's `gitutil.GetProjectMeta` + `FormatProjectInfo()` injected by every `cmd/*.go` |
| `{{ROLE_REPORTS}}` (new) | Inlined role reports from `shared/report/*/<name>.md`, filtered per the run's `--roles` / `--all` / `--max-age`. Section header included. Empty if no reports. | Today's `prompts.DiscoverReports` + inlining in `AssembleReviewPrompt` |

These do NOT belong in `pre_exec` frontmatter — they're prompt content, computed during assembly, idempotent.

`{{prompt.body}}` is **not** a variable. Where the previous approach used it, templates use `{{include {{prompt.name}}.prompt.md}}` — explicit inclusion.

**Default-destination guidance:** prompts write to `{{OUTPUT_DIR}}` (per-execution, free history). Promotion to `{{SHARED_PROMPT_DIR}}` is reserved for outputs that need to be visible to other agents (report → review, review → code_management, auto_setup → user/future agents). Today's `promoteRuntimeFiles` Go path handles this hardcoded for known workflows; eventually it's the `copy-runtime-files` action declared in dir-level metadata.

## Shared artifacts model

### `OUTPUT_DIR` — per-execution (default, preferred)

`OUTPUT_DIR` = `.ateam/runtime/<exec_id>/`. Every prompt run gets a fresh directory. **History falls out for free** — past runs are right there on disk under different exec IDs.

In most cases, this is the only destination a prompt needs.

### `SHARED_PROMPT_DIR` / `SHARED_BASE_DIR` — cross-agent sharing (opt-in)

When an artifact must be visible to other agents:
- `SHARED_BASE_DIR` = `.ateam/shared/`
- `SHARED_PROMPT_DIR` = `.ateam/shared/<prompt-path>/`
- Primary output: `<prompt-basename>.md` inside that dir.

### Promotion

Today: hardcoded Go (`promoteRuntimeFiles`). Future (stage executor): explicit `copy-runtime-files` action in dir-level metadata. This refactor keeps Go-side promotion for now to minimize moving parts.

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

For built-in embedded defaults: ateam ships new `_pre.*.md` and `_post.*.md` files at root and inside `report/`, `code/`. The migration step at the project anchor doesn't write these (they come from embedded defaults); it only moves user-authored files into the new layout.

After migration, remove the now-empty `roles/` and `supervisor/` directories. Print a one-line notice on stderr on first migration. Implementation in a new `internal/migrate/v1_layout.go`, called from `internal/root/resolve.go` when env is first materialized.

## What this refactor does NOT change

- `runtime/<exec_id>/` per-run scratch dirs and the `OUTPUT_*` template variables — mechanism unchanged; only canonical destination paths change.
- `config.toml` schema.
- `runtime.hcl`, agent/profile/container config.
- `state.sqlite`, `secrets.env`, `logs/`, `cache/` at `.ateam/` root.

## `ateam prompt :NAME --preview` (in scope)

`Resolve()` produces the exact ordered file list AND the parsed frontmatter. Pretty-printing both ships with the refactor — it is the maintainability story for the whole layered design.

```
$ ateam prompt :report/security --preview
Assembly for 'report/security':

Frontmatter (merged from dir-level + role-level):
  pre_exec:  [concurrent-run-check]
  post_exec: [copy-runtime-files]

Resolution:
  [CLI]      --pre-prompt                                                (empty)

  root _pre.*.md:
    [embedded] prompts/_pre.context.md            (contains {{PROJECT_INFO}})

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

`--preview --content` dumps the actual concatenated text.

## Out of scope (deliberately deferred)

- Executable pre/post scripts and built-in actions — frontmatter format is locked, execution is the stage-executor follow-up.
- `{{shell CMD}}` template directive — assembly-time script execution (category A). Inert-during-preview question to be resolved when implemented.
- Frontmatter `params:` + CLI `--param k=v` for runtime parameterization. One mechanism only when it lands.
- Multi-target adders (`prepend_to: [a, b]`) — `{{include}}` + shared library file handles it via the library pattern.
- Renaming `code_management` to something shorter.
- Reserved-name validation for user role IDs (structurally impossible with the new namespacing).
- Built-in prompt content changes — only file renames in this refactor.

## Pending questions / open directions

### Stage-related (drives a follow-up design pass)

1. **Pre/post exec action v1 catalog** (category B only — content injection lives in template vars, see category A): `concurrent-run-check`, `budget-precheck`, `source-writable` (pre); `copy-runtime-files`, `chain-next` (post). Confirm.
2. **Assembly-time content injection v1**: `{{PROJECT_INFO}}` and `{{ROLE_REPORTS}}` as template variables. Plus `{{include /shared/review/review.md}}` in code_management's prompt (replacing today's `cmd/code.go` read+inline). Confirm.
3. **Where dir-level frontmatter lives.** Now that `_template.md` is gone, dir-level `pre_exec`/`post_exec` declarations need a home. Options: (a) on `_pre.md`/`_post.md` files; (b) dedicated `_meta.yaml` per dir; (c) only on `<role>.prompt.md`. Recommend (b). **Decide.**
4. **Action parameter shape** — single string vs key-value map. Recommend single string for v1.
5. **Script ordering** — explicit YAML list order.
6. **Failure semantics** — does a failed pre step block the prompt? Does a failed post step fail the stage? Per-step `on_failure: stop|continue` policy.
7. **Per-role frontmatter merge with dir-level frontmatter** — append by default (`pre_exec` lists concatenate). `replace: true` opt-in for full override.
8. **Today's promotion behavior preserved during transition** — `cmd/report.go` / `cmd/review.go` / `cmd/auto_setup.go` keep calling `promoteRuntimeFiles`. `cmd/code.go` / `cmd/code_management.go` / `cmd/verify.go` continue NOT to promote. Once the stage executor lands, promotions become explicit `copy-runtime-files` actions in dir-level frontmatter.
9. **`{{ROLE_REPORTS}}` filtering inputs** — today's review reads `--roles`, `--all`, `--max-age` and applies them. As a template variable, filter inputs flow via run-context. Pin down.
10. **CLI-injected `--pre-exec` / `--post-exec` shape** — single string per flag, repeatable. Action expansion runs at execution time, not flag-parse time. Confirm.
11. **`{{LABEL}}` default for non-parallel runs** — empty string (recommended) or same as `{{prompt.name}}`. Confirm.

### Prompt-system questions

12. **`{{include?}}` semantics across anchors** — first-match (same as `{{include}}`, just optional). Locked.
13. **`{{include_glob}}` semantics across anchors** — additive (all matches, embedded → org → project, lexical within anchor). Locked.
14. **Setup overview filename** — auto-migration renames `setup_overview.md` → `auto_setup/auto_setup.md`. Acceptable break, or keep historical name?
15. **`ateam roles` output** — keep as role-listing, or unify under `ateam prompts list` / `ateam stages list`? Decided after the refactor lands.
16. **Specialization for runtime-varying values** — frontmatter `params:` + CLI `--param k=v` + `{{param.k}}`. Deferred.
17. **Enablement** — ateam keeps `config.toml [roles]`. For future mini-workflows: frontmatter `enabled_from:` on dir-level metadata, or inline `enabled: [a, b, c]`. Revisit when a second workflow surfaces.
18. **Frontmatter schema strictness** — strict allow-list (recommended) so unknown keys error. Lock as: strict.

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
7. **Org-override test** — both org-level and project-level overrides for the same role; confirm anchor fallback still works.
8. **Composition test** — `{{include}}` / `{{include?}}` first-match: create the same filename at embedded, org, and project anchors; assert project's wins. `{{include_glob}}` additive: create distinct filenames at each anchor matching the glob; assert all are concatenated in most-general-first order. Dir-level `_pre.*.md`: same composition; multiple files at one anchor sorted lexically.
9. **Frontmatter test** — invalid YAML errors at preview; unknown key errors; pre_exec list ordering preserved; per-role frontmatter extends dir-level frontmatter.
10. **Role-name validation test** — `_security.prompt.md` (starts with `_`) errors at load. `code.pre.prompt.md` (ends with `.pre`) errors. Both messages clearly state the rule.
11. **Orphan-fragment test** — `securty.pre.scope.md` (typo) errors with Levenshtein hint suggesting `security`.
12. Manual smoke: `ateam prompt :review`, `ateam prompt :code_management`, `ateam prompt :report/project.security`.
13. **Preview tool** — `ateam prompt :report/project.security --preview` lists every contributing file with anchor tags AND merged frontmatter, in the exact assembly order. `--preview --content` produces the full assembled text.

---

## Earlier approaches (rejected)

The design went through two earlier shapes before landing on the current dir-level `_pre.*.md` / `_post.*.md` scheme. Both are documented here for posterity.

### Rejected approach 1: convention-driven recursive composition

The first draft baked composition into the framework: filenames carried meaning, and the framework recursively walked the directory chain.

**Mechanism:**
- File kinds: `<name>.prompt.md` (named prompt), `prompt.md` (dir base, auto-prepended), `prompt.pre.md` / `prompt.post.md` (dir-level pre/post), `<name>.prompt.pre.md` / `<name>.prompt.post.md` (per-named pre/post).
- Recursive composition: for any prompt at path `P`, walk strict prefixes shortest→longest, then `P` itself. At each level: pre (additive across anchors) + main (first-match) + post (additive). The framework hardcoded "main is fallback, pre/post are additive."
- `prompt.md` at a directory level was **auto-prepended** to every named prompt below it. No template directive, no opt-in.

**Why rejected:**
1. **"Inserted first vs inserted last" was a forced choice.** The framework's rule put the dir base BEFORE the role's body, but many real wrappers (report format, output structure) belong AFTER. Splitting "the dir's shared instructions" between `prompt.md` (before) and `prompt.post.md` (after) was awkward.
2. **Hardcoded composition in the framework.** "Why did the assembled prompt include X?" required knowing the recursive walk rules, not reading the files.
3. **No natural home for declarative metadata** (stage actions, enablement). Parallel mechanisms kept being invented.
4. **Naming explosion.** ~6 file patterns with rules between them.

### Rejected approach 2: template file + `{{include}}` directives

The second draft tried to make composition explicit by putting it in a per-directory `_template.md` file that used `{{include}}` / `{{include?}}` / `{{include_glob}}` to orchestrate fragments.

**Mechanism:**
- `_template.md` was a structural file (not invokable) that wrapped each role in the directory.
- Templates used `{{include}}` directives to pull in dir-level fragments (`prompt.pre.<NAME>.md`), per-role fragments (`<role>.prompt.pre.<NAME>.md`), and the role body itself.
- YAML frontmatter on `_template.md` declared dir-level stage actions (`pre_exec`, `post_exec`).
- Naming used `<role>.prompt.pre.<NAME>.md` and `prompt.pre.<NAME>.md` (no `_` prefix).

**Example shipped template:**
```yaml
---
post_exec: [copy-runtime-files]
---
You are performing a {{prompt.name}} report on this project.

{{PROJECT_INFO}}

{{include_glob prompt.pre.*.md}}
{{include_glob {{prompt.name}}.prompt.pre.*.md}}

{{include {{prompt.name}}.prompt.md}}

{{include_glob {{prompt.name}}.prompt.post.*.md}}
{{include_glob prompt.post.*.md}}

Format your findings as severity-tagged markdown sections...
```

**Why rejected:**
1. **Templates are opaque.** To know what gets assembled, you must open the template file and read its `{{include}}` boilerplate. `ls` alone doesn't tell you anything.
2. **Boilerplate cost.** Every templated directory needed 5+ `{{include}}` lines just to wire up the standard convention. Authoring a new workflow meant writing or copying this boilerplate.
3. **Subtle ambiguity in dotted filenames.** `prompt.pre.foo.md` could parse as either dir-level pre or role "prompt" pre fragment "foo." The required reservation of `prompt`/`pre`/`post` as forbidden role-name suffixes was hard to defend in writing.
4. **`.prompt.` infix duplication.** Files like `security.prompt.pre.scope.md` repeated `.prompt.` unnecessarily.

### What carried forward to the chosen approach

- Directory split between `prompts/` (config), `shared/` (cross-agent), `runtime/` (per-run).
- Action-first identifier model (`:report/security`, `:review`).
- Anchor system (project → org → embedded).
- Auto-migration of old layouts.
- The Stage concept (pre/prompt/post phases) and the grounded built-in action catalog.
- The "one rule" for filename composition (same name = overload; different name = compose).
- Template engine primitives: `{{include}}`, `{{include?}}`, `{{include_glob}}`, `{{var}}`, YAML frontmatter parsing.
- Substitution variables `{{prompt.name}}`, `{{prompt.path}}`, `{{LABEL}}`, `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`.
- Ad-hoc prompt mechanics (`@file.md`, stdin, CLI `--pre-exec`/`--post-exec`).

The chosen approach drops the template file entirely. Assembly is filename-driven: `_pre.*.md`, `_post.*.md` (dir-level, structural — marked by `_` prefix), and `<role>.prompt.md`, `<role>.pre.*.md`, `<role>.post.*.md` (role-level). The framework discovers the matching files and composes them per the fixed assembly order; `{{include*}}` directives remain available inside any file for ad-hoc composition.
