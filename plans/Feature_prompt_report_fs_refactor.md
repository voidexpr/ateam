# Feature: Prompt & artifact filesystem refactor

The immediate goal is to restructure ateam's artifacts between prompts and generated files. The longer-term design goal is to provide a generic prompt system that supports many workflows beyond `report/review/code/verify`, with the same simple core mechanics. The report/review/code/verify workflow just happens to use that more generic prompt system. Similarly, arbitrary spawned agents should have flexibility to store and read files in private and shared spaces.

## Context

Today, prompts (configuration) and generated outputs are entangled under the same trees, and the codebase carries two parallel abstractions (`roles/<NAME>/...` vs `supervisor/...`) that complicate prompt resolution, role discovery, and output promotion. `internal/runner/runner.go:1156` already has a TODO acknowledging the split is overdue: "get rid of this exclusion once configured prompts are kept separate from files."

We also want a model where logic moves easily in/out of LLM prompts, balancing token usage, determinism, and the LLM's decision power. The same system should make it cheap to specialize a generic prompt to a use case, or temporarily override behavior in a specific context (e.g. "never report on X here", "pay attention to Y").

## Goals

1. **Split prompts from generated outputs** — `prompts/` for configuration, `shared/` (and per-execution `runtime/<exec_id>/`) for generated artifacts.
2. **Minimal framework, conventions in templates** — the framework provides a small set of primitives (resolution, substitution, includes, frontmatter); conventions for pre/post composition live in shipped templates, not in framework rules.
3. **Explicit, readable assembly** — opening the dir-level template shows exactly what gets assembled, in what order. No hidden auto-inclusion.
4. **Structural naming safety** — a user-defined role named `review` cannot collide with the singleton review action; they live in different namespaces.
5. **Stage-ready** — frontmatter is the home for declarative stage metadata (pre_exec/post_exec lists, enablement, future params). The stage executor is a follow-up; this refactor lays the substrate.
6. **Future-friendly** — clean hooks for richer template directives, CLI ad-hoc additions, and per-prompt frontmatter.

## Naming convention

Three kinds of file in `prompts/`:

| File | Purpose | Invokable as a prompt? |
|---|---|---|
| `<name>.prompt.md` | Named prompt. Body content. Optional YAML frontmatter. | Yes — `:dir/name` or `:name` |
| `_template.md` | Directory template. Wraps named prompts in this directory via `{{include}}` directives. Optional YAML frontmatter. | No — purely structural |
| Other `*.md` (e.g. `prompt.pre.md`, `prompt.post.md`, `<name>.prompt.pre.md`, fragments) | Content fragments. Referenced by templates via `{{include}}` / `{{include?}}`. | No — included only |

The framework's behavior is determined entirely by these file kinds plus what a template's body and frontmatter say. There is no hardcoded "auto-prepend the dir base" rule.

### Identifiers

Names use forward-slash paths, no leading prefix:
- `review` — singleton named prompt at root (file: `review.prompt.md`)
- `report/security` — named prompt inside `report/` directory (file: `report/security.prompt.md`, wrapped by `report/_template.md` if present)
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
    _template.md             # root template — wraps singletons at root
    review.prompt.md         # singleton
    auto_setup.prompt.md
    code_management.prompt.md
    code_verify.prompt.md
    exec_debug.prompt.md
    report_commissioning.prompt.md
    report/
      _template.md           # dir template — wraps roles in report/
      prompt.pre.md          # (optional) dir-level pre fragment, included by template
      prompt.post.md         # (optional) dir-level post fragment
      security.prompt.md     # role body
      security.prompt.pre.md # (optional) per-role pre fragment
      security.prompt.post.md
      test_gaps.prompt.md
      ...
      gather-deps.sh         # arbitrary script; referenced from frontmatter
      validate.sh
    code/
      _template.md
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

Same restructuring applies to `.ateamorg/`, `.ateamorg/defaults/`, and the embedded `defaults/` tree.

## Framework primitives (the whole list)

The framework provides exactly these primitives. Everything else — pre/post composition, action ordering, etc. — is convention encoded in templates.

1. **Prompt resolution by name across anchors.** Anchors ordered most-specific to least: project → org → org-defaults → embedded. Fallback semantics (first hit wins).
2. **`{{var}}` substitution.** Existing template variables (`{{PROJECT_*}}`, `{{OUTPUT_*}}`, `{{EXEC_ID}}`, `{{ATEAM_OWN_*}}`, `{{CONTAINER_*}}`, etc.) plus new `{{prompt.name}}` and `{{prompt.path}}`.
3. **`{{include PATH}}`** — inline a file's content at this position. **First-match across anchors.** Error if no anchor has the file.
4. **`{{include? PATH}}`** — inline a file's content. **Additive across anchors** (all matches concatenated, most-general first: embedded → org-defaults → org → project). Produces empty string if no anchor has it.
5. **YAML frontmatter parsing** for `*.prompt.md` and `_template.md`. Strict schema (small fixed key set); unknown keys reject with a clear error.
6. **Two file kinds** as defined above (`*.prompt.md` invokable, `_template.md` structural, anything else is a fragment included only via `{{include}}`).

### Substitution inside include paths

Include paths may contain `{{var}}` substitutions. Resolution is two-pass:

1. Substitute `{{var}}` inside the include path text.
2. Resolve the include against anchors.

So `{{include? {{prompt.name}}.prompt.pre.md}}` resolves `{{prompt.name}}` first (e.g. to `security`), then looks for `security.prompt.pre.md` across all anchors and concatenates matches.

### Cycles, depth, errors

- `{{include}}` recursion is depth-limited (e.g. 16 levels). Cycle detection errors at preview/assembly time.
- `{{include? }}` produces empty on missing; never errors for missing files.
- A template that references `{{prompt.name}}` but is invoked without a body (e.g. `:report` without a leaf) errors at preview time.
- YAML frontmatter parse errors are loud at preview time.

### Orphan-fragment detection (catches typos)

At preview/load time, the assembler walks every `*.prompt.pre.md` and `*.prompt.post.md` file across all anchors. For each, it checks that a matching `<name>.prompt.md` exists in some anchor. If none does, error:

```
orphan fragment: report/securty.prompt.pre.md
  no matching report/securty.prompt.md found in any anchor
  did you mean: security.prompt.pre.md?
```

Levenshtein hint when a basename is close to an existing prompt. Catches the typo failure mode cheaply.

Dir-level fragments (`prompt.pre.md`, `prompt.post.md`, `preamble.md`, `epilogue.md`) are exempt — they don't pair with any specific named prompt.

## Templates: conventions live here, not in the framework

ateam ships embedded default templates that encode the standard report/review pipeline. Users inherit them; users who want different behavior override the template at their project or org anchor.

### Root template (`defaults/prompts/_template.md`)

Wraps singletons at root (`:review`, `:auto_setup`, `:code_management`, `:code_verify`, `:exec_debug`, `:report_commissioning`).

```yaml
---
post_exec: [copy-runtime-files]
---
{{PROJECT_INFO}}

{{include? prompt.pre.md}}
{{include? {{prompt.name}}.prompt.pre.md}}

{{include {{prompt.name}}.prompt.md}}

{{include? {{prompt.name}}.prompt.post.md}}
{{include? prompt.post.md}}
```

`{{PROJECT_INFO}}` is a template variable (category A) — its expansion is the formatted git HEAD + uncommitted-files block that's part of every prompt today. It is NOT a `pre_exec` action.

### Review template (`defaults/prompts/review.prompt.md`, singleton)

Since review is a singleton at root, it uses the root template. The review prompt's body references `{{ROLE_REPORTS}}`:

```
You are reviewing the role reports gathered for this project.

{{ROLE_REPORTS}}

Synthesize across reports. Identify cross-cutting issues, conflicts between
recommendations, and the smallest sequence of changes that addresses the most
high-priority findings.
```

`{{ROLE_REPORTS}}` is a template variable provided by ateam — it expands to the discovered + filtered role reports inlined as a section. The framework handles the `--roles` / `--all` / `--max-age` filtering and produces the text. Same category-A mechanism as `{{PROJECT_INFO}}`.

### Report template (`defaults/prompts/report/_template.md`)

Wraps role bodies with report-shaped instructions.

```yaml
---
post_exec: [copy-runtime-files]
---
You are performing a {{prompt.name}} report on this project.

{{PROJECT_INFO}}

{{include? prompt.pre.md}}
{{include? {{prompt.name}}.prompt.pre.md}}

Your speciality and approach:

{{include {{prompt.name}}.prompt.md}}

{{include? {{prompt.name}}.prompt.post.md}}
{{include? prompt.post.md}}

Format your findings as severity-tagged markdown sections, scoped by file path.
Use this scale: blocker / high / medium / low.
Skip findings without a realistic exploit path or measurable impact.
```

### Code template (`defaults/prompts/code/_template.md`)

Similar shape; the code_management template uses `{{include /shared/review/review.md}}` (first-match, errors if missing) where today `cmd/code.go` reads review.md and bails:

```yaml
---
pre_exec: [concurrent-run-check, source-writable]
---
You are managing a code change based on this review.

{{PROJECT_INFO}}

The review you must act on:

{{include /shared/review/review.md}}

{{include? {{prompt.name}}.prompt.pre.md}}
{{include {{prompt.name}}.prompt.md}}
{{include? {{prompt.name}}.prompt.post.md}}
```

Note: `copy-runtime-files` is intentionally absent — code outputs are diffs/commits.

### What a role file looks like

`defaults/prompts/report/security.prompt.md`:
```
Focus on confirmed exploitable bugs and data exposure with realistic triggers.
Refuse to break working features with defensive header tightening.
```

No frontmatter, no boilerplate. Pure body content. Wrapped by the dir template's includes.

### Project-level customization patterns

- Override `report/_template.md` at project anchor → restructure all reports.
- Add `report/security.prompt.pre.md` at project anchor → prepended to security (via the template's `{{include? security.prompt.pre.md}}`).
- Add `report/prompt.post.md` at project anchor → appended after every report's body (via the template's `{{include? prompt.post.md}}`).
- Add `security.prompt.md` at project anchor → fully override the security role's body.
- Override `report/_template.md` frontmatter to add `./my-extra-validate.sh` to `post_exec`.

Each is a single file edit. None affects others.

### Why this beats "framework auto-includes everything"

Open `report/_template.md` and the assembly order is **literally the file**. No knowledge of framework rules needed. Customizing the structure means editing this file, not memorizing how the framework's recursive walk behaves.

The cost: more lines in the embedded default template. The framework gets simpler in exchange.

## How a role is identified

A role is the basename of a `*.prompt.md` file inside a directory that has a `_template.md` (or in a directory without a template, where each file is just a standalone prompt). An "action" is the directory name at the top of `prompts/`. A role exists iff `prompts/report/<role>.prompt.md` exists somewhere in the resolution chain. Singleton actions (named prompts at root) cannot be mistaken for roles — different namespaces, collision is structurally impossible.

There are no `Role` or `Action` types in the core. The CLI keeps role/action vocabulary for discoverability (`ateam report --roles security`); under the hood that maps to `Assemble("report/security")`.

## Two kinds of "extra work" around a prompt

The system distinguishes two fundamentally different categories of work that ateam performs around an LLM invocation. They must not be conflated.

### A. Assembly-time content injection — output goes INTO the prompt

Data the agent reads. Computed during prompt assembly. Saves the agent from doing the work itself (or makes it possible for the agent to act on context it couldn't otherwise see).

Mechanisms in v1:
- **Template variables** — `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`, `{{PROJECT_NAME}}`, `{{ATEAM_OWN_*}}`, etc. ateam computes these once per env and inlines them where referenced.
- **Includes** — `{{include FILE}}` (required, first-match) and `{{include? FILE}}` (optional, additive). Inline file content.

Future addition (deferred): `{{shell CMD}}` directive for user-defined scripts whose stdout becomes prompt content. Inert during preview only if explicitly marked side-effect-free; otherwise preview is the only safe way to run them.

**Key property:** these run during preview. `ateam prompt :NAME --preview --content` shows the actual assembled prompt — that means assembly-time content has been computed. Anything in this category must be **idempotent and side-effect-free**.

### B. Pre / post execution hooks — output does NOT go into the prompt

Work done before or after the agent runs. Output is captured as logging only — never seen by the agent in its prompt. Used for environment setup, post-run validation, and side effects.

Mechanisms in v1: ordered steps declared in YAML frontmatter as `pre_exec:` / `post_exec:` lists on `_template.md` (dir-level) and individual `<name>.prompt.md` (per-prompt). A step is either:
- a **built-in action** referenced by name (e.g. `copy-runtime-files`, `concurrent-run-check`) — implemented in ateam's Go code.
- a **script path** relative to the template's directory — black-box subprocess that gets the run's environment.

**Key property:** these do NOT run during preview. Preview only shows what *would* run. Side effects are expected and acceptable at run time (creating dirs, copying files, running tests, committing).

### Where past brainstorming conflated them

Earlier drafts put `project-info` and `discover-reports` in `pre_exec`. That was wrong: their **output is part of the prompt content**, not the runtime environment. They belong in category A (template variables / includes), not B (frontmatter exec lists). The action catalog below is corrected accordingly.

The two categories are connected at the run level (a pre-exec script might write a file that an `{{include}}` later picks up), but the mechanisms are separate and live in different parts of the spec.

## The Stage concept (framing for what comes next)

A **stage** is the unit of "one LLM invocation with deterministic wrapping." A stage has three phases:

1. **Pre** — environment setup, gating checks. From frontmatter `pre_exec` list.
2. **Prompt** — the LLM call, using the assembled prompt. Assembly-time content (category A above) is already baked into the prompt text at this point.
3. **Post** — runtime validation, copy/version files, update tracking, log telemetry. From frontmatter `post_exec` list.

Pre and post lists are ordered. Composition: per-prompt frontmatter lists **extend** dir-template lists. Order in the YAML list is the execution order.

Frontmatter on `report/_template.md`:
```yaml
---
pre_exec: [concurrent-run-check, source-writable, ./gather-deps.sh]
post_exec: [copy-runtime-files, ./validate.sh]
---
```

Frontmatter on `report/security.prompt.md`:
```yaml
---
post_exec: [./security-extra-validate.sh]
---
Focus on confirmed exploitable bugs...
```

Assembling `:report/security` runs `pre_exec` = `[concurrent-run-check, source-writable, ./gather-deps.sh]` and `post_exec` = `[copy-runtime-files, ./validate.sh, ./security-extra-validate.sh]`. None of these contribute to prompt text.

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
| `cmd/code.go:158-171` reads `shared/review/review.md`, errors if missing, inlines into code_management prompt | `cmd/code.go:158-171` | `{{include /shared/review/review.md}}` in the code_management template — first-match (errors if missing), inlines content. No special action needed. |

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

For lightweight workflows the user doesn't want to put a full template + role files in the prompt tree. Common case: `ateam parallel` over a few hand-written prompt files, with shared orchestration declared via CLI flags.

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

For (2) and (3) there is no `_template.md` wrapping — the prompt is **standalone**. The file body (after frontmatter, if any) is what the agent sees, plus the outermost CLI pre/post wrappers and any CLI-injected pre/post-exec actions.

`{{prompt.name}}` for ad-hoc prompts defaults to the file's basename without extension (`file1` for `@file1.md`), or the `--label` value if provided, or empty for raw inline/stdin.

### CLI-injected stage actions

Two new flag families mirror the frontmatter declarations:

- `--pre-exec ACTION` (repeatable) — appended to whatever `pre_exec` the template/file frontmatter declares. CLI items run **after** template/frontmatter items in the pre phase (so user CLI hooks see the template's setup already done).
- `--post-exec ACTION` (repeatable) — appended to `post_exec`. Same ordering.

CLI items go through the same expansion as frontmatter items (substitutions, builtin-vs-script resolution). Existing `--pre-prompt TEXT` / `--post-prompt TEXT` keep their meaning (outermost raw-text wrap).

**Action vs script syntax.** Bare names resolve against the built-in actions registry (`concurrent-run-check`, `copy-runtime-files`, etc.). Path-like values (starting with `./`, `/`, or `~`) are scripts. The explicit `builtin:NAME` prefix is supported for clarity (and matches the user's mental model in mixed examples).

**Script path resolution.** Differs by source:
- Frontmatter in `_template.md` or `<name>.prompt.md`: relative paths resolve from the file's directory.
- CLI `--pre-exec` / `--post-exec`: relative paths resolve from the user's CWD (where they invoked `ateam`).
- This matches user expectation: template-declared scripts ship with the template; CLI-injected scripts are project-local.

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

1. Items from the dir-template's frontmatter (if any).
2. Items from the named prompt's frontmatter (if any).
3. Items from `--pre-exec` CLI flags (in order).

Same shape for `post_exec`. Same general layering for the prompt-text wrap (CLI `--pre-prompt` outermost, then template/file body, then CLI `--post-prompt`).

### Why not put orchestration in each file?

For one or two ad-hoc files, frontmatter in each works. For "N similar jobs with the same hooks," the CLI form is the right place: one declaration covers N jobs, and each job's per-job substitutions (`{{LABEL}}`) flow naturally. For ateam's built-in workflows (report, code, review), the orchestration lives in the shipped `_template.md` files — same mechanism, different home.

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
| Project-level customization | Anchors (project / org / org-defaults / embedded) | Higher-priority anchor's `main` wins; `{{include?}}` is additive across anchors. |
| Cross-cutting policy for a group of prompts | Dir-template `_template.md` | Wraps every role body; the body content of the template IS the structure. |
| Specialization of one named prompt | Project-anchor `<name>.prompt.pre.md` / `.post.md` (fragments included by the template) | Surgical additions persisted in the repo; layered via `{{include?}}`. |
| Temporary / one-off override | CLI `--pre-prompt` / `--post-prompt` (outermost wrap, raw text) | Doesn't persist; one run. |
| Inject deterministic context INTO the prompt (save the agent the work) | Category A: template variables (`{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`, future `{{shell CMD}}`) and `{{include}}` directives. Computed during assembly. | Idempotent, side-effect-free; runs during preview too. |
| Set up the runtime environment BEFORE the agent runs | Category B: stage **pre** phase (frontmatter `pre_exec`) | concurrent-run-check, budget-precheck, source-writable, future worktree creation. Side effects expected; does NOT run during preview. |
| Side effects AFTER the agent runs (artifacts, tests, commits, telemetry) | Category B: stage **post** phase (frontmatter `post_exec`) | copy-runtime-files, chain-next, run tests, schema-validate, build, commit, update tracker. |
| Compose shared paragraphs across prompts | `{{include :name}}` | One source of truth for shared content; resolves through anchors. |
| Runtime-varying values (same template, different inputs) | Future frontmatter `params:` + CLI `--param k=v` + `{{param.k}}` | Single mechanism; defer until concrete need. |
| Filter "run all enabled" prompts | Future frontmatter `enabled_from:` on `_template.md` (delegates to a metadata source like `config.toml`) | Keeps prompt-system content-only; enablement is workflow metadata. |

Two recurring patterns:

1. **Specialize a generic prompt.** Write the prompt as generic as possible (embedded default), layer specializations via project-anchor fragments. The dir-template's `{{include?}}` slots are the standard specialization points.
2. **Move that bit out of the prompt.** Every deterministic operation is a candidate for `pre_exec` / `post_exec`. Reduces token cost, increases reliability.

## Code structure: `internal/prompts/` (the `PromptAssembler` module)

The core abstraction is a `PromptAssembler` that knows nothing about ateam workflows. It takes anchors and assembles a named prompt by resolving its `_template.md` (if any) and recursively expanding `{{include}}` / `{{include?}}` directives.

### Sketch

```go
package prompts

type Anchor struct {
    Name string  // "project", "org", "org-defaults", "embedded" — for preview/debug
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
    Kind     string  // "template" | "named" | "fragment-via-include?" | "cli-pre" | "cli-post"
    ViaInclude string // include directive that produced this (empty if direct)
}

type Resolution struct {
    Name        string
    Files       []ResolvedFile  // ordered as they contribute to the final text
    Frontmatter Frontmatter     // merged: dir-template + per-prompt
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
- **`{{include}}` and `{{include?}}` are handled by the Assembler**, not by Templater. The Templater handles only variable substitution. This is because include resolution requires anchor knowledge that the Templater doesn't have.
- **Two-pass expansion**: variable substitution inside include paths first, then resolve includes, then variable substitution in the final content.
- **`ListNamedPrompts(dir)`** is what role discovery reduces to. Filters out `_template.md` and fragment files; returns invokable basenames.
- **Outside the module:** `:` syntax parsing (CLI), workflow knowledge, action/role vocabulary, template variable values (Templater wraps these), migration of old layouts, stage execution (follow-up refactor).

### What changes outside `internal/prompts/`

- `internal/root/resolve.go` — replace path helpers with prompt-name-based lookups.
- `internal/runner/runner.go:1156` — drop the `*_prompt.md` exclusion in `promoteRuntimeFiles`. Update canonical destination to `SharedPromptDir(promptName)/<basename>.md`.
- `internal/runner/template.go` — add `SHARED_BASE_DIR` and `SHARED_PROMPT_DIR` template variables. `PrimaryOutputName()` becomes `<promptBasename>.md`.
- `defaults/` — rename files into the new tree; ship `_template.md` defaults; update `//go:embed`.
- `cmd/*.go` — remove `RoleID: "supervisor"` hardcodes; route through `Assemble(name)`. Rework `cmd/prompt.go` to accept positional `:report/security` and `--preview` / `--content` flags.
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

**Default-destination guidance:** prompts write to `{{OUTPUT_DIR}}` (per-execution, free history). Promotion to `{{SHARED_PROMPT_DIR}}` is reserved for outputs that need to be visible to other agents (report → review, review → code_management, auto_setup → user/future agents). Today's `promoteRuntimeFiles` Go path handles this hardcoded for known workflows; eventually it's the `copy-runtime-files` action declared in `_template.md` frontmatter.

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

Today: hardcoded Go (`promoteRuntimeFiles`). Future (stage executor): explicit `copy-runtime-files` action in `_template.md` frontmatter. This refactor keeps Go-side promotion for now to minimize moving parts.

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
| `.ateam/report_base_prompt.md` | merged into `.ateam/prompts/report/_template.md` body (above the role include) |
| `.ateam/code_base_prompt.md` | merged into `.ateam/prompts/code/_template.md` body |
| `.ateam/report_extra_prompt.md` | `.ateam/prompts/report/prompt.post.md` (included by template via `{{include? prompt.post.md}}`) |
| `.ateam/code_extra_prompt.md` | `.ateam/prompts/code/prompt.post.md` |
| `.ateam/supervisor/review_prompt.md` | `.ateam/prompts/review.prompt.md` |
| `.ateam/supervisor/review_extra_prompt.md` | `.ateam/prompts/review.prompt.post.md` (root template includes via `{{include? {{prompt.name}}.prompt.post.md}}`) |
| `.ateam/supervisor/code_management_prompt.md` | `.ateam/prompts/code_management.prompt.md` |
| `.ateam/supervisor/code_management_extra_prompt.md` | `.ateam/prompts/code_management.prompt.post.md` |
| `.ateam/supervisor/code_verify_prompt.md` | `.ateam/prompts/code_verify.prompt.md` |
| `.ateam/supervisor/auto_setup_prompt.md` | `.ateam/prompts/auto_setup.prompt.md` |
| `.ateam/supervisor/exec_debug_prompt.md` | `.ateam/prompts/exec_debug.prompt.md` |
| `.ateam/supervisor/report_commissioning_prompt.md` | `.ateam/prompts/report_commissioning.prompt.md` |
| `.ateam/supervisor/review.md` | `.ateam/shared/review/review.md` |
| `.ateam/supervisor/verify.md` | `.ateam/shared/verify/verify.md` |
| `.ateam/supervisor/history/...` | dropped |
| `.ateam/setup_overview.md` | `.ateam/shared/auto_setup/auto_setup.md` |

For built-in embedded defaults: ateam ships new `_template.md` files at root and at `report/` / `code/`. The migration step at the project anchor doesn't write these (they come from embedded defaults); it only moves user-authored files into the new layout.

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

Frontmatter (merged from dir-template + named prompt):
  pre_exec:  [project-info, discover-reports]
  post_exec: [copy-runtime-files]

Resolution:
  [CLI]      --pre-prompt                                       (empty)
  [embedded] prompts/report/_template.md                        (dir template)
    via {{include? prompt.pre.md}}:
      [project]  prompts/report/prompt.pre.md                   (additive)
    via {{include? security.prompt.pre.md}}:
      (none — file does not exist)
    via {{include security.prompt.md}}:
      [embedded] prompts/report/security.prompt.md              (role body)
    via {{include? security.prompt.post.md}}:
      [project]  prompts/report/security.prompt.post.md         (additive)
    via {{include? prompt.post.md}}:
      (none)
  [CLI]      --post-prompt                                      (empty)
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

1. **Pre/post exec action v1 catalog** (category B only — content injection actions moved to category A): `concurrent-run-check`, `budget-precheck`, `source-writable` (pre); `copy-runtime-files`, `chain-next` (post). Confirm.
2. **Assembly-time content injection v1**: `{{PROJECT_INFO}}` and `{{ROLE_REPORTS}}` as template variables (replacing today's hardcoded `FormatProjectInfo` and `DiscoverReports` Go logic). Plus `{{include /shared/review/review.md}}` in code's template (replacing today's `cmd/code.go` read+inline). Confirm.
3. **Action parameter shape** — single string vs key-value map. Recommend single string for v1 (matches `chain-next: <action>`).
4. **Script ordering**: explicit YAML list order — no glob, no numeric prefix.
5. **Failure semantics** — does a failed pre step block the prompt? Does a failed post step fail the stage? Per-step `on_failure: stop|continue` policy.
6. **Per-prompt frontmatter merge with dir-template frontmatter**: append by default (`pre_exec` lists concatenate). `replace: true` opt-in for full override.
7. **Today's promotion behavior preserved during transition** — `cmd/report.go` / `cmd/review.go` / `cmd/auto_setup.go` keep calling `promoteRuntimeFiles`. `cmd/code.go` / `cmd/code_management.go` / `cmd/verify.go` continue NOT to promote. Once the stage executor lands, the promotions become explicit `copy-runtime-files` actions in `_template.md` frontmatter.
8. **`{{ROLE_REPORTS}}` filtering inputs** — today's review reads `--roles`, `--all`, `--max-age` from CLI flags and applies them. As a template variable, how are those filter inputs passed? Probably as additional run-context that the variable resolver reads (CLI → run context → variable expansion). Pin down before implementation.
9. **CLI-injected `--pre-exec` / `--post-exec` shape** — single string per flag, repeatable (matches `--pre-prompt` shape). Action expansion (substitutions, builtin-vs-script) runs at execution time, not flag-parse time. Confirm.
10. **`{{LABEL}}` default for non-parallel runs** — empty string, or same as `{{prompt.name}}`? Empty is cleaner (clearly means "no per-job context"). Confirm.

### Prompt-system questions

7. **`{{include?}}` semantics across anchors**: additive (recommended) or first-match. Locked: **additive**.
8. **`{{include}}` (non-`?`) semantics**: first-match (recommended) or additive. Locked: **first-match, error if missing**.
9. **Setup overview filename** — auto-migration renames `setup_overview.md` → `auto_setup/auto_setup.md`. Acceptable break, or keep historical name?
10. **`ateam roles` output** — keep as role-listing, or unify under `ateam prompts list` / `ateam stages list`? Decided after the refactor lands.
11. **Specialization for runtime-varying values** — frontmatter `params:` + CLI `--param k=v` + `{{param.k}}`. Deferred.
12. **Enablement: "run all enabled prompts in parallel" — where does enabled/disabled metadata live?** ateam keeps `config.toml [roles]`. For future mini-workflows: frontmatter `enabled_from:` on `_template.md` pointing at an enablement source, or inline `enabled: [a, b, c]`. Revisit when a second workflow surfaces.
13. **Frontmatter schema strictness** — strict allow-list (recommended) so unknown keys error. Reserves space for future keys and prevents silent typos. Lock as: strict.

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
8. **Include resolution test** — `{{include?}}` additive: create a project with the same fragment file at embedded, org, and project anchors; assert the assembled output concatenates all three in most-general-first order. `{{include}}` first-match: assert project wins; remove project file and assert org wins; etc.
9. **Frontmatter test** — invalid YAML errors at preview; unknown key errors; pre_exec list ordering preserved; per-prompt frontmatter extends dir-template frontmatter.
10. Manual smoke: `ateam prompt :review`, `ateam prompt :code_management`, `ateam prompt :report/project.security`.
11. **Preview tool** — `ateam prompt :report/project.security --preview` lists every contributing file with anchor tags AND merged frontmatter, in the exact assembly order. `--preview --content` produces the full assembled text.

---

## Initial approach (rejected)

For posterity. The earlier draft of this spec used a different mechanism: **convention-driven recursive composition encoded in the framework**, no template directives, no frontmatter (initially). It was abandoned for the reasons noted at the bottom of this section.

### Mechanism

- File kinds: `<name>.prompt.md` (named prompt), `prompt.md` (dir base, auto-prepended), `prompt.pre.md` / `prompt.post.md` (dir-level pre/post), `<name>.prompt.pre.md` / `<name>.prompt.post.md` (per-named pre/post). All meaning baked into the framework by filename.
- Recursive composition: for any prompt at path `P`, walk strict prefixes shortest→longest, then `P` itself. At each level: pre (additive across anchors) + main (first-match) + post (additive). The framework hardcoded "main is fallback, pre/post are additive."
- `prompt.md` at a directory level was **auto-prepended** to every named prompt below it. No template directive, no opt-in.
- A later evolution introduced `_template.prompt.md` as a directory wrapper file with `{{prompt.body}}` substitution. The convention-driven composition still applied around it.
- A still-later evolution considered `_.toml` / `_.yaml` sidecar metadata files for declaring stage actions; rejected for file proliferation.

### What it looked like

```
prompts/
  report/
    prompt.md              # dir base, auto-prepended to every role
    prompt.pre.md          # dir-level pre
    prompt.post.md         # dir-level post (after every named prompt's triplet)
    security.prompt.md     # role
    security.prompt.pre.md
    security.prompt.post.md
  review.prompt.md
  review.prompt.pre.md
  review.prompt.post.md
```

Assembly for `:report/security`:
```
[CLI pre]
  prompts/prompt.pre.md (root)
  prompts/prompt.md (root, if any)
    prompts/report/prompt.pre.md
    prompts/report/prompt.md
      prompts/report/security.prompt.pre.md
      prompts/report/security.prompt.md
      prompts/report/security.prompt.post.md
    prompts/report/prompt.post.md
  prompts/prompt.post.md (root, if any)
[CLI post]
```

### Why it was rejected

1. **"Inserted first vs inserted last" was a forced choice.** The framework's rule put the dir base BEFORE the role's body, but many real wrappers (report format, output structure) belong AFTER. The split between `prompt.md` (before) and `prompt.post.md` (after) was awkward — you had to mentally separate "the dir's shared instructions" between two files when they were really one wrapper.
2. **Hardcoded composition in the framework.** "Why did the assembled prompt include X?" required knowing the framework's recursive walk rules, not reading the files. The implicit auto-prepend was magic — convenient at first, opaque under pressure.
3. **No natural home for declarative metadata.** Stage actions (pre_exec/post_exec, enablement) need somewhere to live. The convention-based approach kept inventing parallel mechanisms (sibling exec files, separate metadata files, in-prompt directives) and each had its own ordering ambiguity or file-proliferation problem.
4. **The naming explosion.** With per-role and per-dir pre/post, plus templates and template pre/post, the framework had to know about ~6 file patterns and the rules between them. The new approach has 2 file kinds plus arbitrary fragments.
5. **The dir-template `_template.prompt.md` evolution still had hidden rules.** The framework decided what was "main" vs "wrapper" by filename, what got substituted where, and how anchor levels composed. Hidden complexity.

### What carried forward to the chosen approach

- The directory split between `prompts/` (config), `shared/` (cross-agent), `runtime/` (per-run).
- The action-first identifier model (`:report/security`, `:review`).
- The anchor system (project → org → org-defaults → embedded).
- Auto-migration of old layouts.
- The Stage concept (pre/prompt/post phases) and the grounded built-in action catalog.
- The substitution variables `{{prompt.name}}`, `{{prompt.path}}` (originally proposed as `{{prompt.body}}` too, dropped in favor of explicit `{{include {{prompt.name}}.prompt.md}}`).

The chosen approach replaces the hardcoded recursive composition with explicit `{{include}}` directives in templates, and moves declarative metadata into YAML frontmatter on `_template.md` and `<name>.prompt.md` files.
