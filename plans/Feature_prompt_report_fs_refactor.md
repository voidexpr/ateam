# Feature: Prompt & artifact filesystem refactor

The immediate goal is to restructure ateam's artifacts between prompts and generated files. The longer-term design goal is to provide a generic prompt system that supports many workflows beyond `report/review/code/verify`, with the same simple core mechanics. The report/review/code/verify workflow just happens to use that more generic prompt system. Similarly, arbitrary spawned agents should have flexibility to store and read files in private and shared spaces.

## Related documents

Other files in `plans/` that illustrate and stress-test this design:

- **`prompt_example_metaproject.md`** — full worked example: translating `metaproject.py` (a multi-project audit/fix/verify pipeline) into the filesystem proposal. Surfaces `{{arg.*}}` and parameterized promotion as needs.
- **`prompt_example_metaproject_python_api.md`** — alternative direction: a small typed Python API (`Ctx` / `PromptBundle` / `Runner`) on top of `ateam exec`. Splits framework (~130 lines, reusable) from workflow code (~360 lines).
- **`prompt_example_release_crawl.md`** — three-way comparison (shell / filesystem / Python API) on a tighter workflow (release-notes crawler). Tests the question "when is each approach worth the migration cost?"

Read this spec first; the examples are concrete tests of the design.

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

Every step exercises one of the design challenges below. The parallel investigation step needs per-task pre/post (worktree setup), the synthesizing review needs cross-step data flow (reading investigator outputs), the conditional patch needs cheap "is work needed?" checks, and the gate is deterministic algorithmic logic after the agent runs.

### Design challenges

The patterns that emerge in any such system:

- **Prompt assembly.**
  - **Factor out** common definitions to help maintain prompts (common context, report format instructions).
  - **Include dynamic content** — execute code during prompt assembly to inject prior output, datasets, git log info. Avoids paying the LLM to derive information it could be told.
  - **Add runtime custom instructions** while keeping the rest of the prompt as-is ("focus on area X", "skip Y", "pay special attention to a/b/c").
  - **Share prompts between projects** while supporting project-specific overloads.
- **Run algorithmic logic** (scripts, programs, built-in commands) before and after the agent acts on a prompt. Set up a git worktree before; run tests as a gate after.
- **Conditionally execute prompts.** Prompts are expensive and slow; if we know the work isn't needed, skip it. Enable/disable part of a prompt, or skip an entire prompt that is part of a larger workflow.
- **Run independent steps in parallel** and wait for them to complete. Any pre/post execution logic must be part of each parallel task.
- **Treat compute as a constrained resource.** Track per-run cost; gate batches against a budget; pick cheaper models for cheap stages and expensive ones for hard stages. This loops back into prompt assembly (shorter, more constrained prompts may suit cheaper models).

For simple systems, all these patterns are better written directly in the app — that way it only pays for the features it needs. ateam provides the framework once the patterns start compounding: the exact prompt used is cached, and `ateam ps` / `ateam inspect` surface metrics and logs.

### Why prompts belong outside the app

LLM-integrated apps have three layers: **prompts** (instructions to the model — half code in that they handle inputs and shape outputs, half configuration in that they steer behavior), **procedural logic** (scripts and actions around the prompt — pre, during via hooks or CLI options, post), and a **driver** (the CLI or workflow that ties everything together).

At day one all three change together. Once the app stabilizes, the driver and the procedural logic settle into normal code-review pace. **Prompts keep changing** — they're closer to copy than code, and they want non-development-style updates: the model missed a class of findings, emphasis shifts, a project needs different framing, a domain term changes.

That mismatch is why agentic apps eventually externalize their prompts: the file is the unit of change, not the function. ateam provides that structure — a tree of files, diffable, overridable per project / org / embedded — so prompt evolution doesn't pay code-review tax for every wording tweak.

### What ateam provides

Each feature maps to one or more design challenges above.

| Feature | Addresses | Benefit |
|---|---|---|
| File-based layout for prompts | Assembly / factor out | Easy audit; standardized; maintainability, readability |
| Overload, compose (include), pre/post instructions as files or on the CLI | Assembly / overload, runtime instructions | Prompt fragment management; surgical customization |
| Dynamic prompt generation via lightweight templating + command output (in a read-only sandbox) | Assembly / dynamic content | Saves tokens; faster agent execution; better determinism (not guaranteed) |
| Pre/post code execution around the agent | Algorithmic logic | Determinism, environment setup, gating |
| Bundling pre/post code with the prompt for easy switching between serial and parallel | Parallelism | Saves development time; reduces latency |
| Per-job labels and CLI-injected exec for parallel batches | Parallelism | Same orchestration across N independent tasks |
| Conditional execution via runtime flow control (`ateam flow skip`/`error`/`abort`) | Conditional execution | Skip expensive work cleanly, even inside parallel batches |
| Process tracking, cost tracking, log file management, agent-led run debugging | Cost, reproducibility | Easier to manage; manageable cost; debuggable runs |
| Prompt preview | Readability | Inspect what will be sent before sending it |

### What ateam does NOT do

To bound expectations:

- **Not a scheduler.** No cron, no event triggers. Use systemd timers, GitHub Actions, or whatever your existing infrastructure provides.
- **Not interactive.** No chat UI, no human-in-the-loop turn-taking. ateam runs agents unattended.
- **Not multi-tenant.** Runs are local to a project; no shared scheduler across teams.
- **Not a model gateway.** ateam wraps existing CLI agents (Claude Code, Codex) — it doesn't talk to model APIs directly.

### Implementation challenges

A few "deep" features any system in this space eventually needs:

- **Prompt preview** — being able to ask "what would actually be sent if I ran this?" without running it. Essential once the assembly involves dynamic content or multi-anchor layering.
- **Sandboxing for dynamic content scripts.** Assembly-time scripts whose output enters the prompt should be read-only and bounded; their side effects can't be cleaned up after the prompt is sent.
- **Agent artifact management.** Agents produce files that:
  - Need to be read back to maintain state between executions, then overwritten — we lose history.
  - Are useful as history for comparisons, but timestamped files complicate prompt-writing (and agents may find creative ways to discover them).
  - May be agent-type-specific or shared across agents.
  - Each project ends up inventing its own way to manage these.
  - Granularity finer than "a file" may be needed (tasks, issues for handoff/state). Messaging-style coordination is usually overkill.
- **Prompt templating without a programming language.** Most languages (shell, Python) template strings well, but importing one bleeds host-language semantics into prompts and hurts readability for an LLM.
- **Readability under composition.** Prompts get harder to read when littered with dynamic sections and function-call chains. Each project does this differently; after a few use cases, consistency matters.
- **Workflow shape.** Step A → parallel step B → step C → conditionally do step D or E, or skip back to A. Composable, but reaching for Airflow is overkill for most cases.

### Why this matters for LLM systems specifically

The drivers behind moving work out of the LLM (assembly-time content injection + pre/post execution) are five distinct things, often conflated:

- **Tokens.** Every computed value the agent has to derive is paid for in input or output tokens. Move it out and the bill drops.
- **Latency.** Local computation that takes 50ms can save an LLM round-trip of seconds.
- **Determinism.** A script returns the same output for the same input; an LLM doesn't.
- **Trust.** The LLM can do anything; you can't prove it won't. Moving a check, transformation, or side effect into deterministic code means the result is bounded and inspectable. Mature systems migrate toward "I need to *guarantee* this happens correctly," a guarantee the LLM can't make.
- **Reproducibility.** LLM outputs are non-deterministic; the same prompt run twice can produce different results. When something goes wrong, you need to reconstruct exactly what was sent, what came back, what scripts ran around it, and in what context. ateam captures all of this; without it, debugging an LLM-based system is guessing.

The last two are specific to LLM systems and don't show up in conventional pipelines. They're why audit infrastructure isn't optional polish — it's how you make an LLM-based system trustworthy enough to act unattended.

### Evolution: where systems get stuck

Most LLM-integrated tooling follows a similar arc. Knowing where you are helps recognize the next wall:

| Stage | Looks like | Hits a wall when |
|---|---|---|
| 1 | One prompt, called from a shell script | You need a small variation for one project |
| 2 | A CLI with subcommands and flags | You want parallelism over many similar prompts |
| 3 | A workflow with pre/post hooks per step | Debugging requires reconstructing the exact prompt that was sent |
| 4 | Workflow + audit trail | Different teams need different policies layered onto the same prompts |
| 5 | Workflow + audit + layered composition (this) | (you tell us) |

It's great to start with agents taking care of a lot, but over time these systems evolve toward more structure. What started as a simple script — then a CLI with top-level actions, then debugging steps — gets stuck on one of the deeper features: parallelism with ad-hoc instructions, script execution side effects, readability under composition.

## Why this refactor

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

Future addition (deferred): `{{shell CMD}}` directive for user-defined scripts whose stdout becomes prompt content. See the Stage concept's "Forward look" subsection for the full spec — scripts must be idempotent/read-only; preview runs them with `ATEAM_PREVIEW=1` env var set; side-effecting work belongs in `pre_exec`. Scripts can call `ateam flow` for runtime flow control without mixing concerns.

**Key property:** these run during preview. `ateam prompt :NAME --preview --content` shows the actual assembled prompt — that means assembly-time content has been computed. Anything in this category must be **idempotent and side-effect-free**.

### B. Pre / post execution hooks — output does NOT go into the prompt

Work done before or after the agent runs. Output is captured as logging only — never seen by the agent in its prompt. Used for environment setup, post-run validation, and side effects.

Mechanisms in v1: ordered steps declared in YAML frontmatter on prompt-tree markdown files. Dir-level lists live in frontmatter on any `_pre.*.md` or `_post.*.md` file in the dir (lists merge across all such files); role-level lists live in `<role>.prompt.md` frontmatter. A step is either:
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

**Decided**: on any `_pre.*.md` or `_post.*.md` file in the directory. Frontmatter lists merge across all such files in the dir. There is no separate metadata file (no `_meta.yaml`); structural files carry both content and metadata.

Per-role frontmatter on `<role>.prompt.md`:
```yaml
---
pre_exec: [./security-extra-setup.sh]
post_exec: [./security-extra-validate.sh]
---
Focus on confirmed exploitable bugs...
```

Dir-level frontmatter on (e.g.) `report/_pre.intro.md`:
```yaml
---
pre_exec: [concurrent-run-check, source-writable, ./gather-deps.sh]
post_exec: [copy-runtime-files, ./validate.sh]
---
You are performing a {{prompt.name}} report on this project.
[... rest of the pre fragment's markdown body ...]
```

Assembling `:report/security` then runs `pre_exec` = `[concurrent-run-check, source-writable, ./gather-deps.sh, ./security-extra-setup.sh]` and `post_exec` = `[copy-runtime-files, ./validate.sh, ./security-extra-validate.sh]`. None of these contribute to prompt text.

If multiple structural files in the same dir carry frontmatter (e.g. both `_pre.intro.md` and `_post.format.md` declare `pre_exec` entries), the lists concatenate in lexical filename order. This is consistent with how the content fragments themselves compose.

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

### Forward look: `ateam flow` and `{{shell}}` (stage-executor follow-up, not in this refactor)

Two related primitives extend the stage system without changing what we've already designed. They're called out here so the spec stays consistent with where the design is heading.

#### `ateam flow <action>` — runtime flow control from scripts

A new CLI subcommand that scripts (in `pre_exec`, `post_exec`, or even `{{shell}}` directives) call to signal lifecycle decisions back to the runner. Cleanly separates "decide what should happen" (script) from "framework reacts" (runner).

**v1 action set:**

| Action | What it does |
|---|---|
| `ateam flow skip [--reason TEXT]` | Don't invoke the LLM for this job. Mark as skipped in the call DB. Post_exec doesn't run. `chain-next` doesn't fire. |
| `ateam flow error [--reason TEXT]` | Don't invoke the LLM. Mark as failed (with reason). Counts as failure in batch summary. |
| `ateam flow abort [--reason TEXT]` | Kill the entire `ateam parallel` batch. In-flight jobs receive SIGTERM; pending jobs marked cancelled. |
| `ateam flow continue` | Explicit no-op signal. Enables clean `... && ateam flow continue \|\| ateam flow error` shell patterns. |
| `ateam flow note <text>` | Attach a free-form note to the run record. Visible in `ateam ps`, `ateam inspect`, the DB. Use for "found cached result" type observations that aren't full skips. |

**Richer v2 action set (before the "future" deferral).** The v1 set above is the minimum needed for the substrate. The following extend it to make script-driven workflows self-sufficient — without them, any non-trivial post-LLM business logic forces an external dispatcher (a Python wrapper, a Makefile, etc.).

**Pre-exec additions:**

| Action | What it does |
|---|---|
| `ateam flow completed [--reason TEXT]` | Script did the work itself; no LLM call needed. Like `skip` but marks success. The script is responsible for writing the output to the normal output path (`runtime/<exec_id>/<primary>.md`). post_exec runs normally (promotion, chain-next, telemetry). DB row records `status = completed-by-script` (distinct from `completed-by-agent`). |

Useful when a deterministic path handles the common case and the LLM is only needed for edge cases (e.g. a formatter handles 90% of inputs; the LLM handles the 10% with non-obvious diffs).

**Post-exec additions:**

| Action | What it does |
|---|---|
| `ateam flow redo --extra TEXT` | Re-run the same prompt with an appended instruction. New `exec_id` with `parent_exec_id` pointing at the original; the prior attempt is preserved on disk and in the DB. Loop-guarded by frontmatter `max_redos: N` (default 2). Default skips pre_exec on the redo (gating already passed); opt-in `--rerun-pre`. Output lands in the redo's `runtime/<exec_id>/`; promotion runs after the redo settles. |
| `ateam flow fallback --profile X` (or `--agent X`) | Re-run with a different agent/profile. Same audit shape (new exec_id, parent pointer). Useful when one agent's API is degraded but the workflow shouldn't fail the whole batch. |
| `ateam flow retry [--after N \| --until-reset \| --backoff POLICY]` | Re-run with the same config after a wait. One action, three policies: explicit sleep, rate-limit-aware sleep (parse headers from prior error, sleep until reset), exponential backoff for 5xx. |

**Boundary: what belongs in `ateam flow` vs. in ateam core.** Pure transient-error handling (5xx, rate limits) should be **inside ateam itself** as profile config (e.g. `retry_on: [api_5xx, rate_limit]`, `max_retries: 3`, `backoff: exponential`) rather than driven by `post_exec` scripts. Every workflow wants this; re-implementing it per-script is a footgun. The only retry case that genuinely needs `ateam flow` is the **business-logic** one — script inspects the output and decides "almost right, redo with this hint." Pure transient retry should be invisible to scripts.

**`on_failure` is a separate primitive, not a flow action.** When a pre_exec script returns a non-zero exit (e.g. the deterministic shortcut tried but failed), the right answer is a step-level `on_failure: stop | continue | fallback_to_llm` declared in frontmatter — not a flow action. The script doesn't decide "should the LLM run on my error?"; the workflow author declares that policy once. See pending question #6.

**Rich post_exec context: `runtime/<exec_id>/_run.json`.** post_exec scripts need more than env vars to make business decisions. Write a JSON document at the start of post_exec (before user scripts run) containing:

- **Identity:** `exec_id`, `parent_exec_id` (for redo/fallback chains), `batch`, `work_dir`, `prompt_path`, `args` (resolved `{{arg.*}}` namespace).
- **Config:** `agent`, `profile`, `model`, `effort`, container info.
- **Timing:** `started_at`, `ended_at`, `elapsed_ms`.
- **Cost:** `usd`, tokens by category (`input`, `output`, `cache_read`, `cache_write`).
- **Outcome:** `exit_code`, `is_error`, `failure_category` (`timeout` | `api` | `budget` | `no_result`), `peak_context`.
- **Output:** `primary_output_path`, file list under `runtime/<exec_id>/`.
- **Activity summary:** tool_use counts by tool name, assistant message count, thinking token count.

This is what unlocks non-trivial post_exec scripts: "did the agent run tests? → if no tool_use of Bash, `redo` with stronger instruction"; "produced the expected output file?"; "cost exceeded budget → `flow error` and stop, don't redo." None of this is doable from env vars alone. The `--auto-debug` agent reads the same JSON the post_exec script does — single source of truth for "what happened in this run."

**Future actions (deferred until concrete need):**
- `set <key> <value>` / `get <key>` — run-scoped K/V store (post_exec can read what pre_exec computed).
- `defer [--until TIMESTAMP]` — requeue at a future time.
- `output <file>` — declare a non-default primary output file.
- `promote <src> <dst>` — explicit copy from `runtime/` to `shared/`.
- `fork <name>`, `wait <exec_id>` — dynamic fan-out / job dependencies inside a batch.

**Storage:**
- In-flight: `runtime/<exec_id>/_flow.toml` — append-only during the run. Runner reads at decision points (before LLM, before post_exec, after post_exec). File-based, no DB lock contention with many parallel writers.
- Historical: call DB row's `status` field gains `skipped` / `aborted` values; new `flow_reason` text column captures the message.

**Discovery:**
- `ATEAM_EXEC_ID` env var (ateam already sets project/role env for child processes). Scripts ateam launches inherit it.
- Optional `--exec-id` flag for explicit override (escape hatch for sandboxes or sub-processes that strip env).

**Example — pre_exec gates the run:**
```bash
#!/bin/sh
# .ateam/prompts/crawl/check-needed.sh, declared in dir-level pre_exec
if [ "$(stat -c %Y source.json)" -le "$(cat .last_crawl 2>/dev/null || echo 0)" ]; then
  ateam flow skip --reason "source unchanged since last crawl"
  exit 0
fi
```

Works the same for `ateam exec` (single job) and `ateam parallel` (N independent jobs, each with its own flow signal).

#### `{{shell CMD}}` — assembly-time content injection (Category A)

Already noted as "Out of scope" earlier; now formalized for symmetry with `ateam flow`.

`{{shell CMD}}` runs `CMD` during prompt assembly; its stdout becomes part of the prompt body. Used for deterministic context injection that the agent would otherwise have to compute itself.

```
{{shell git diff --stat HEAD~5}}
{{shell ./pick-paragraph.sh "$ATEAM_STATE"}}
```

**Rules:**

1. **Scripts must be idempotent and read-only** beyond `ateam flow` side effects. Assembly may run during `--preview`; scripts that write files or call external APIs don't belong here (use `pre_exec` instead).
2. **`ATEAM_PREVIEW=1` env var is set during preview** runs. Scripts that need to branch on "is this a real run or a preview?" check that.
3. **Frontmatter is NOT modifiable from `{{shell}}`.** Frontmatter is parsed before content; assembly-time scripts run during content expansion and can't retroactively change it. To influence the runtime (e.g. "skip this job"), call `ateam flow skip` from inside the shell script — the runner picks up the flow signal after assembly completes and acts on it before launching the LLM.
4. **Two-pass expansion** (like includes): `{{var}}` substitutions inside `{{shell CMD}}` are resolved first, then the shell command runs.

#### How `ateam flow` and `{{shell}}` compose

Assembly-time decision flow:
1. Prompt is assembled. `{{shell ./check.sh}}` runs; the script calls `ateam flow skip` if it determines no work is needed.
2. Assembly completes; prompt text is built normally.
3. Runner reads `_flow.toml` before launching the agent.
4. If `skip` was signalled, the LLM is never invoked. Status recorded; batch summary reflects the skip.

This two-stage flow (assembly produces content + flow signals; runner acts on signals after assembly) cleanly separates content composition from lifecycle control. Neither mechanism leaks into the other.

#### What we get vs. cost

- Solves the conditional-run-in-parallel case the file-system approach can't express in shell pipelines.
- Solves the "skip a job without writing a custom dispatcher" case (Option 4 in the brainstorm).
- One new CLI subcommand (`ateam flow`), one new flow-state file (`_flow.toml`), one expansion of the call DB schema (status enum, reason text).
- `{{shell}}` is one additional template-engine primitive.
- Both are **post-refactor** — this refactor establishes the substrate (assembler module, frontmatter, anchors). The stage executor follow-up implements `ateam flow` and `{{shell}}`.

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

## Python framework as external orchestration

Some workflows are dominated by **algorithmic state** — typed config across N variants, per-job pre-flight checks, conditional skips, parallel batches with structured per-job results. The filesystem-and-`ateam-flow` path expresses this through scripts + frontmatter; for code-heavy workflows a small typed Python framework on top of `ateam exec` is often clearer. The crawl and metaproject examples both ended up there.

This section captures the framework shape and the two implementation patterns. Full worked example: `plans/prompt_example_metaproject_python_api.md`.

### Framework primitives

A reusable `ateam_runner.py` module (~130 lines) provides:

| Primitive | Role |
|---|---|
| `Ctx` | Per-invocation context: `work_dir`, `role`, `action`, `force`, `dry_run`, `data: dict`. Workflows stash typed state in `data`. |
| `ExecPrompt` (protocol) | Produces text inserted into the prompt. Runs during preview. (Category A.) |
| `ExecAction` (protocol) | Runs before or after the agent. Output is NOT in the prompt. Does NOT run during preview. (Category B.) Returns `Flow("continue" \| "skip" \| "error")`. |
| `PromptBundle` | One executable agent unit: `name`, `prompt: list[PromptPart]`, `pre_exec: list[ExecAction]`, `post_exec: list[ExecAction]`. |
| `Runner` | Registry + executor. `add(bundle)`, `preview(name, ctx)`, `run(name, ctx)`, `run_many(items, workers=N)`. |
| Helpers | `SkipIf(predicate, reason)`, `EnsureParents(paths)`, `BackupFiles(paths)`, `PromptFn(fn)`, `ActionFn(fn)`. |

The vocabulary mirrors this spec exactly: `ExecPrompt` ≡ Category A, `ExecAction` ≡ Category B, `Flow` ≡ `ateam flow`. The Python framework is the same model expressed in code instead of filesystem layout. Workflow-specific code (one Python file per workflow) defines its `PromptBundle`s, registers them with a `Runner`, and ties into the workflow's CLI.

### Pattern A: framework calls `ateam exec`, parallelism stays in Python

What the metaproject and crawl examples show. `PromptBundle.run` does:

1. Render the prompt in Python (no ateam assembler involved).
2. Run `pre_exec` actions in Python; honor `Flow("skip")` / `Flow("error")`.
3. `subprocess.run(["ateam", "exec", "--work-dir", ..., "--action", ..., "--role", ..., ...], input=prompt, text=True, check=True)`.
4. Run `post_exec` actions in Python.

`Runner.run_many()` uses `ThreadPoolExecutor`; each thread is one `ateam exec` subprocess.

**Pattern A gap list (small):**

1. **`--work-dir DIR` on `ateam exec`** — already supported.
2. **`--action NAME` on `ateam exec`** — already supported.
3. **Ad-hoc role/action accepted as free-form tracking labels** when the prompt comes from stdin (no registry check). Confirm or add.
4. **Emit `exec_id` on stdout/stderr in a parseable form** so the framework can correlate to `ateam inspect`. Either a `--print-exec-id` flag or a structured line on stderr like `exec_id=<id>`. Optional but small.
5. **Progress visible to the framework** — see "Progress telemetry" below.

Pattern A is the natural target: small additions to `ateam exec`, Python keeps ownership of typed config and conditional logic, ateam keeps ownership of the agent invocation + audit trail.

### Pattern B: framework hands the batch to `ateam parallel`

In principle, the framework could build N prompts and let ateam fan them out. In practice this requires substantially more from ateam:

1. **Per-job `--work-dir`, `--role`, `--action`** — today `ateam parallel` shares these across the batch. No per-positional override syntax exists.
2. **Per-job `--arg key=value`** — the `{{arg.*}}` namespace (Phase 2 of the refactor).
3. **`ateam flow` actions** for skip/error/redo/fallback (Phase 2).
4. **Per-job pre/post hooks that are more than shell scripts.** The Python framework's hooks close over typed `Tool` state and Python helpers. `ateam parallel --pre-exec` is one declaration applied to all jobs; varying behavior per job means hooks read `{{arg.*}}` and re-implement the logic in shell.
5. **Machine-readable per-job result on stdout.** `Runner.run_many()` returns `[Result(bundle, flow)]`. `ateam parallel` emits progress + exit code, not structured per-job outcomes. Needs a JSON-lines mode or a per-job status file.

Pattern B effectively requires all of Phase 2 plus a per-job argument surface that isn't in the current spec. It only makes sense when the **entire** bundle (including hooks) is expressible in ateam-native primitives — at which point the Python framework stops carrying its weight and you're in the filesystem-proposal world.

**Recommendation:** for the Python framework path, target Pattern A and keep parallelism in Python. The two patterns are complementary, not competing — Python framework for code-heavy / algorithmic workflows, filesystem layout for prompt-heavy / content-edited workflows.

### Progress telemetry: the missing piece (both patterns)

`ateam parallel` renders live per-agent stats — token counts, current tool, elapsed time, context size — straight from the in-process event stream. Those values likely never land in `state.sqlite`; they're rendered in-memory and lost when the process exits.

That's fine when ateam owns the whole batch, but it breaks both patterns above:

- **Pattern A:** each `ateam exec` subprocess is opaque to its siblings and to the orchestrating Python script. To surface "agent is currently in tool X, 42k tokens in" across a batch, the framework would have to re-parse the event stream itself or poll subprocess stdout.
- **Pattern B:** the live table is great for humans but doesn't survive past process exit; the calling script gets no structured per-job timeline.

**Proposal: make both `ateam exec` and `ateam parallel` write progress to the call DB** (token deltas, current tool, peak context, elapsed) — batched per N events or per second to avoid write storms. In return:

- `ateam ps` works the same whether the agent was launched by `ateam parallel`, `ateam exec`, or a Python subprocess.
- External orchestrators can opt into progress display by reading the DB without re-parsing the event stream.
- Post-mortem inspection has the same fidelity as live inspection.
- The `_run.json` written by post_exec gets richer "what actually happened" data for free.

**Complement: structured progress output from `ateam exec`.** A `--progress-format=jsonl` flag emits one JSON line per progress event on a chosen channel (stderr, or a fd passed via `--progress-fd`). Cheaper than DB writes, lower latency for consumers that want live feedback without polling the DB.

Both together — DB-backed for persistence + streaming output for low-latency consumers — is the obvious complete answer. This is the single most useful set of additions to make ateam pleasant to drive from any external orchestrator (Python framework, shell loop, CI pipeline). It also helps `ateam ps` work uniformly regardless of who launched the agent.

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
| `{{arg.<key>}}` (planned for stage-executor follow-up) | Value of `--arg <key>=<value>` passed on the CLI. Multiple `--arg` flags accumulate; one namespace per invocation. | The metaproject and crawl examples surfaced this as a real need — cross-project workflows need per-invocation identity (`{{arg.target_label}}`) distinct from `{{LABEL}}`. Plan to add alongside the stage executor. |

**Dotted namespaces are the convention for new variables.** `prompt.name`, `prompt.path`, `arg.X`, and future `git.repo_name` / `git.branch` follow this. Existing ALL_CAPS variables (`PROJECT_NAME`, `OUTPUT_DIR`, etc.) stay as-is for backward compatibility — no renames in this refactor.

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

Today: hardcoded Go (`promoteRuntimeFiles`). Future (stage executor): explicit `copy-runtime-files` action in dir-level metadata.

**Parameterized promotion (planned for stage-executor follow-up).** The default `copy-runtime-files` action promotes `OUTPUT_DIR/*` → `SHARED_PROMPT_DIR/`. Real workflows (the metaproject example surfaced this) need richer targets — e.g. `OUTPUT_DIR/report.md` → `<shared>/metaproject/<target>/<scope>/report.md`. The follow-up should support either:
- Action parameters: `copy-runtime-files --to <pattern>` with `{{arg.*}}` and `{{prompt.*}}` substitution in the destination, or
- A user-provided script in `post_exec` that does the copy explicitly.

The promotion model itself is also worth revisiting (see pending question 19); the runtime/shared split is not as intuitive as it could be.

### Overridable directory locations (planned)

`ateam exec` and `ateam parallel` should accept CLI flags to override the default `.ateam/` subtrees:
- `--prompt-dir DIR` — where prompts live (default `.ateam/prompts/`)
- `--runtime-dir DIR` — where per-execution scratch lands (default `.ateam/runtime/`)
- `--shared-dir DIR` — where cross-agent artifacts land (default `.ateam/shared/`)

Not every workflow wants its outputs under `.ateam/`. The metaproject example uses a top-level `reports/` directory because the audit reports are first-class user-visible artifacts, not internal ateam state. With `--shared-dir reports`, the framework writes to `reports/<prompt-path>/...` instead of `.ateam/shared/<prompt-path>/...`. The prompt assembler and template variables transparently follow the override.

`--prompt-dir` is more advanced — useful when a project keeps its own custom prompt tree outside `.ateam/` (e.g. shared across repos via a submodule). When set, it stacks on the anchor chain: the explicit `--prompt-dir` becomes an additional project-level anchor checked first.

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
- `{{shell CMD}}` template directive — assembly-time script execution (category A). Spec'd in the Stage concept's "Forward look" subsection; implemented post-refactor alongside `ateam flow`.
- `ateam flow` CLI subcommand for runtime flow control (`skip` / `error` / `abort` / `continue` / `note`). Spec'd in the Stage concept's "Forward look" subsection; implemented post-refactor.
- Frontmatter `params:` + CLI `--param k=v` for runtime parameterization. One mechanism only when it lands.
- Multi-target adders (`prepend_to: [a, b]`) — `{{include}}` + shared library file handles it via the library pattern.
- Renaming `code_management` to something shorter.
- Reserved-name validation for user role IDs (structurally impossible with the new namespacing).
- Built-in prompt content changes — only file renames in this refactor.

## Pending questions / open directions

### Stage-related (drives a follow-up design pass)

1. **Pre/post exec action v1 catalog** (category B only — content injection lives in template vars, see category A): `concurrent-run-check`, `budget-precheck`, `source-writable` (pre); `copy-runtime-files`, `chain-next` (post). Confirm.
2. **Assembly-time content injection v1**: `{{PROJECT_INFO}}` and `{{ROLE_REPORTS}}` as template variables. Plus `{{include /shared/review/review.md}}` in code_management's prompt (replacing today's `cmd/code.go` read+inline). Confirm.
3. **Where dir-level frontmatter lives** — **decided**: on any `_pre.*.md` or `_post.*.md` file in the directory. Frontmatter lists merge across all such files in lexical order. No separate `_meta.yaml`. Documented in the Stage concept section above.
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
19. **Better model for runtime/shared file promotion.** The current split — agent writes to `OUTPUT_DIR` (`runtime/<exec_id>/`) and then promotion copies selected files to `SHARED_PROMPT_DIR` (`shared/<prompt-path>/`) — works but isn't intuitive. Most filesystem-backed systems either write to the canonical location directly (and overwrite) or use a staging/commit pattern (write to staging, atomically move). Ours is closer to staging-then-promote, but the "what gets promoted" decision is currently hardcoded Go (and planned to become declarative `copy-runtime-files`). Worth a design pass: is there a cleaner model? E.g. agents always write to the canonical shared path; the runtime dir is a tee'd copy purely for history; promotion goes away as a concept. Or: prompt frontmatter declares which output files are "shared" vs "scratch," and the runner enforces the split at write time. Defer until after the substrate lands, but flag explicitly that the current model is the weakest part of the design.

20. **Progress telemetry in the call DB.** Both `ateam exec` and `ateam parallel` should write per-event progress (token deltas, current tool, peak context, elapsed) to `state.sqlite` so `ateam ps` works uniformly regardless of how the agent was launched, and external orchestrators (see "Python framework as external orchestration") can read live state without re-parsing event streams. Complementary: a `--progress-format=jsonl` mode on `ateam exec` for low-latency streaming consumers. Today `ateam parallel` shows live stats in-process but those values aren't persisted; `ateam exec` doesn't show them at all. Lock the storage shape (batched writes per N events or per second to avoid write storms), then implement.

21. **Python framework integration shape.** The Python framework (`ateam_runner.py`) is documented here but not in-tree. Decide: ship it as a sibling package (`python/ateam_runner/`), document it as an example only, or extract its primitive set into ateam-native equivalents over time. Confirm during Phase 2.

## Implementation order

The Verification plan covers *how* to verify; this is *what to build first*. The substrate (this refactor) ships before the stage executor.

### Phase 1: substrate (this refactor)

1. **`internal/prompts/` rewrite** — `Assembler` module with `fs.FS`-based anchors (project → org → embedded), filename-pattern parser (`<role>.prompt.md`, `<role>.pre.[NAME.]md`, `<role>.post.[NAME.]md`, `_pre.[NAME.]md`, `_post.[NAME.]md`), role-name validation (no `_` prefix, no `.pre`/`.post` suffix), and orphan-fragment detection. Returns `Resolution { Files, Frontmatter }`.
2. **Template directives**: `{{include}}` (first-match, error if missing), `{{include?}}` (first-match, silent if missing), `{{include_glob}}` (additive across anchors, lexical within anchor). Variable substitution already exists; integrate into the Assembler.
3. **`internal/migrate/v1_layout.go`** — auto-migration from old layout. Idempotent. Hooked into `internal/root/resolve.go` when env is first materialized.
4. **`defaults/` tree restructure** — rename files into `defaults/prompts/...`, drop `_template.md` concept, ship `_pre.<NAME>.md` and `_post.<NAME>.md` for the standard workflows. Update `//go:embed`.
5. **`internal/root/resolve.go`** — replace `RoleDir`/`RoleReportPath`/`SupervisorDir`/`ReviewPath`/`VerifyPath` with prompt-name-based helpers (`SharedArtifactPath(promptName)`, etc.).
6. **`internal/runner/runner.go`** — drop the `*_prompt.md` exclusion in `promoteRuntimeFiles` (line 1156). Update canonical destination to use new helpers.
7. **`internal/runner/template.go`** — add new template variables: `{{prompt.name}}`, `{{prompt.path}}`, `{{LABEL}}`, `{{PROJECT_INFO}}`, `{{ROLE_REPORTS}}`, `{{SHARED_BASE_DIR}}`, `{{SHARED_PROMPT_DIR}}`. `PrimaryOutputName()` becomes `<prompt-basename>.md`.
8. **`cmd/*.go` rewiring** — remove `RoleID: "supervisor"` hardcodes (`cmd/review.go:233`, `cmd/code.go:278`, `cmd/auto_setup.go:83`, `cmd/verify.go:163`, `cmd/inspect.go:300`). Route through `Assemble(name)`. Rework `cmd/prompt.go` for positional `:report/security` and `--preview` / `--content` flags. Add `--pre-exec`, `--post-exec`, `--labels` to `ateam parallel`.
9. **`ateam prompt :NAME --preview` tool** — pretty-print the `Resolution` (ordered files with anchor tags + merged frontmatter). `--preview --content` adds the assembled text.
10. **`internal/web/`** — update artifact read paths in `handlers.go` and `export.go`.
11. **Documentation pass** — `CONFIG.md`, `ROLES.md`, `README.md`, `ISOLATION.md`.

### Phase 2: stage executor (follow-up refactor)

After Phase 1 ships:

12. Frontmatter `pre_exec` / `post_exec` execution (built-in actions + scripts).
13. Built-in action catalog: `concurrent-run-check`, `budget-precheck`, `source-writable`, `copy-runtime-files`, `chain-next`. Replace today's hardcoded Go promotion with declarative `copy-runtime-files`.
14. `ateam flow` subcommand v1 (`skip`, `error`, `abort`, `continue`, `note`) + `runtime/<exec_id>/_flow.toml` channel + call-DB schema additions (`status` enum gains `skipped` / `aborted` / `completed-by-script`; new `flow_reason` text column; `parent_exec_id` for redo/fallback chains).
15. `ateam flow` subcommand v2 (`completed` for pre_exec; `redo --extra`, `fallback --profile`, `retry --after/--until-reset/--backoff` for post_exec) + `max_redos` frontmatter key + `parent_exec_id` audit chain wiring.
16. **`runtime/<exec_id>/_run.json` writer** — populated at the start of post_exec with identity / config / timing / cost / outcome / activity-summary fields (see Stage concept "Forward look"). Same JSON consumed by `--auto-debug`.
17. **`on_failure` step-level policy** (`stop` | `continue` | `fallback_to_llm`) on frontmatter pre/post-exec entries (pending question #6).
18. **Progress telemetry persistence (pending question #20).** Both `ateam exec` and `ateam parallel` write batched progress events (tokens, current tool, peak context, elapsed) to the call DB. Add `--progress-format=jsonl` on `ateam exec` for streaming consumers.
19. **Built-in transient-error handling** in profile config: `retry_on: [api_5xx, rate_limit]`, `max_retries`, `backoff`. Replaces the "script-driven transient retry" mistake before it becomes a habit.
20. `{{shell CMD}}` template directive (assembly-time, idempotent).
21. `{{arg.<key>}}` CLI-passed argument namespace + `--arg key=value` flag.
22. Parameterized `copy-runtime-files` (or equivalent) to support cross-project shared paths.
23. `--prompt-dir DIR` / `--runtime-dir DIR` / `--shared-dir DIR` CLI flags on `ateam exec` / `ateam parallel` to override default `.ateam/` subtrees.
24. **Pattern A polish for the Python framework path:** confirm ad-hoc `--role` / `--action` accepted without registry check on stdin-fed `ateam exec`; emit `exec_id` in a parseable form (`--print-exec-id` or stderr line) so external orchestrators correlate to `ateam inspect`.

### Phase 3: optional simplifications

- Revisit the runtime/shared promotion model (pending question 19).
- Frontmatter `params:` + CLI `--param k=v` (if a use case demands runtime-varying values beyond `{{arg.*}}`).
- Frontmatter `enabled_from:` for cross-workflow enablement.

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
