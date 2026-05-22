# Prompt Example: `metaproject.py` With The Prompt Filesystem Proposal

Source example: `../metaproject/metaproject.py`

This is a concrete translation of the current `metaproject.py` workflow into the
prompt/artifact filesystem model described in
`plans/Feature_prompt_report_fs_refactor.md`.

## Summary

`metaproject.py` is a good stress test for the proposal because it is not just a
prompt file. It currently does all of these jobs:

| Current Python responsibility | Better home under the proposal |
|---|---|
| Static prompt text for discover/audit/fix/verify | `.ateam/prompts/metaproject/.../*.prompt.md` |
| Common wrappers such as "keep focus narrow" and "save output here" | dir-level `_pre.*.md` / `_post.*.md` |
| Reading prior overview, previous audit/fix reports, and tracked files | assembly-time injection: `{{include?}}`, future `{{shell}}` |
| Skip checks based on mtimes | future `pre_exec` script plus `ateam flow skip` |
| Backing up overwritten reports | mostly replaced by `runtime/<exec_id>/` history |
| Promoting runtime output into stable report paths | future `post_exec` script, or parameterized `promote` action |
| Target parsing, pipeline expansion, cross-project `--work-dir` dispatch | still a thin dispatcher, because ateam is not a scheduler |

The proposal improves the prompt and artifact structure a lot, but the exact
script cannot become only prompt files unless the stage-executor follow-up lands:
`pre_exec` / `post_exec`, `ateam flow`, `{{shell}}`, and argument variables.

## Proposed Artifact Layout

Use ateam's normal runtime history for per-run output, then promote selected
files into stable shared paths for the next stage.

```text
.ateam/
  prompts/
    metaproject/
      _meta.yaml
      _pre.project.md
      discover.prompt.md
      audit/
        _meta.yaml
        _pre.context.md
        _post.output.md
        claude.prompt.md
        gitconfig.prompt.md
        build.prompt.md
        test.prompt.md
        docker.prompt.md
      fix/
        _meta.yaml
        _pre.context.md
        _post.output.md
        claude.prompt.md
        gitconfig.prompt.md
        build.prompt.md
        test.prompt.md
        docker.prompt.md
      verify/
        _meta.yaml
        _pre.context.md
        _post.output.md
        claude.prompt.md
        gitconfig.prompt.md
        build.prompt.md
        test.prompt.md
        docker.prompt.md
      scripts/
        discover-mode.sh
        changes-since-overview.sh
        skip-discover.sh
        skip-audit.sh
        skip-followup.sh
        promote-metaproject.sh
  shared/
    metaproject/
      <target-label>/
        overview.md
        files.toml
        claude/
          audit.md
          fix.md
          verify.md
        gitconfig/
          audit.md
          fix.md
          verify.md
        build/
          audit.md
          fix.md
          verify.md
        test/
          audit.md
          tests.toml
          fix.md
          verify.md
        docker/
          audit.md
          fix.md
          verify.md
```

This keeps the important `metaproject.py` paths:

```text
reports/<project>/overview.md
reports/<project>/files.toml
reports/<project>/<scope>/<action>.md
```

but moves them under `.ateam/shared/metaproject/`, where ateam can reason about
them as cross-agent artifacts.

## Argument Handling

Assume ateam supports runtime arguments:

```text
ateam exec --arg anykeyname=valuex
```

and exposes those values in prompts and stage actions as:

```text
{{arg.anykeyname}}
```

For this example:

```text
{{arg.target_label}}  # ateam, autoversion, minimon, etc.
{{prompt.name}}       # claude, gitconfig, build, test, docker
{{prompt.path}}       # metaproject/audit/test, metaproject/verify/docker, etc.
```

This is much cleaner than using `{{LABEL}}` for target identity. `{{LABEL}}` can
stay a per-job label for parallel execution; target identity is a real input
argument.

## Dir Metadata

This example assumes the proposal chooses the recommended `_meta.yaml` option for
dir-level frontmatter.

`.ateam/prompts/metaproject/_meta.yaml`:

```yaml
pre_exec:
  - concurrent-run-check
```

`.ateam/prompts/metaproject/audit/_meta.yaml`:

```yaml
pre_exec:
  - ./../scripts/skip-audit.sh {{arg.target_label}} {{prompt.name}}
post_exec:
  - ./../scripts/promote-metaproject.sh {{arg.target_label}} audit {{prompt.name}}
```

`.ateam/prompts/metaproject/fix/_meta.yaml`:

```yaml
pre_exec:
  - ./../scripts/skip-followup.sh {{arg.target_label}} {{prompt.name}} audit fix
post_exec:
  - ./../scripts/promote-metaproject.sh {{arg.target_label}} fix {{prompt.name}}
```

`.ateam/prompts/metaproject/verify/_meta.yaml`:

```yaml
pre_exec:
  - ./../scripts/skip-followup.sh {{arg.target_label}} {{prompt.name}} fix verify
post_exec:
  - ./../scripts/promote-metaproject.sh {{arg.target_label}} verify {{prompt.name}}
```

The scripts above are exactly the deterministic logic now embedded in
`skip_audit`, `skip_followup`, `backup`, and the report-path helpers. The
important difference is that scripts do not assemble prompt text. They only
decide lifecycle and promote files.

## Shared Root Wrapper

`.ateam/prompts/metaproject/_pre.project.md`:

````markdown
You are running metaproject prompt `{{prompt.path}}` for target project
`{{arg.target_label}}`.

Project source path:

```text
{{PROJECT_FULL_PATH}}
```

Keep your focus narrow to the instruction below.
````

## Discover Prompt

`.ateam/prompts/metaproject/discover.prompt.md`:

````markdown
---
pre_exec:
  - ./scripts/skip-discover.sh {{arg.target_label}}
post_exec:
  - ./scripts/promote-metaproject.sh {{arg.target_label}} discover
---
# Project State

{{PROJECT_INFO}}

# Discover Mode

{{shell ./scripts/discover-mode.sh {{arg.target_label}}}}

# Goals

Produce two files in `{{OUTPUT_DIR}}`:

1. `overview.md`
2. `files.toml`

The overview is a concise narrative covering:

* what the project is, language/stack
* how to build, or "no build step"
* how to run tests by category, or "no tests"
* dev-environment setup, including Docker, devcontainer, or similar
* anything non-obvious for troubleshooting

The TOML must use exactly this structure:

```toml
[meta]
git_branch       = "BRANCH"
git_commit_hash  = "COMMIT"
discovered_at    = "TIMESTAMP"
path_used        = "PATH_USED"
path_canonical   = "PATH_CANONICAL"

[files]
claude    = ["CLAUDE.md"]
gitconfig = [".git/config"]
build     = ["Makefile", "..."]
test      = ["..."]
docker    = ["Dockerfile", "..."]
```

Paths in `[files]` are relative to `{{PROJECT_FULL_PATH}}`. List the smallest
set of files an auditor would need to read to evaluate each scope. Use `[]` for
scopes with nothing relevant.

If the discover mode says this is incremental, inspect only the changed files it
lists. Update `overview.md` only if the changes meaningfully affect it. Update
`files.toml` only if the relevant file lists changed. Always refresh the `[meta]`
block.
````

`discover-mode.sh` is assembly-time content injection, so it belongs to future
`{{shell}}`, not `pre_exec`. It emits either the full-discovery instruction or
the incremental changed-file list that `build_discover_prompt()` currently
computes in Python.

## Audit Context Wrapper

`.ateam/prompts/metaproject/audit/_pre.context.md`:

```markdown
# Context

# Project Overview

{{include? /shared/metaproject/{{arg.target_label}}/overview.md}}

# File Changes Since Overview Was Produced

{{shell ./../scripts/changes-since-overview.sh {{arg.target_label}} {{prompt.name}}}}

# Previous Report

{{include? /shared/metaproject/{{arg.target_label}}/{{prompt.name}}/audit.md}}

# Action

You are running `audit` for scope `{{prompt.name}}`.
Use the Project Overview above for context. Do not redo discovery.
```

`.ateam/prompts/metaproject/audit/_post.output.md`:

````markdown
Save your audit report to:

```text
{{OUTPUT_DIR}}/audit.md
```
````

## Audit Scope Bodies

`.ateam/prompts/metaproject/audit/claude.prompt.md`:

```markdown
Recommend improvements to `CLAUDE.md` so it clearly documents:

* git usage, if non-standard
* how to build, or N/A
* how to run tests, by category if multiple
* common troubleshooting steps

Be specific: cite missing sections, vague instructions, or commands that do not
match what the overview describes.
```

`.ateam/prompts/metaproject/audit/gitconfig.prompt.md`:

```markdown
Verify git identity:

* `user.name` is set, locally or globally; note which
* `user.email` is set, locally or globally; note which

Report what is configured. Any identity is acceptable for now.
```

`.ateam/prompts/metaproject/audit/build.prompt.md`:

```markdown
Audit the build story. Flag:

* commands that do not work as documented
* missing prerequisites or environment variables
* inconsistencies between overview and actual project files

If the stack does not need a build step, state that and stop.
```

`.ateam/prompts/metaproject/audit/test.prompt.md`:

````markdown
Audit the test commands in two parts.

## Commands That Exist Today

Only document what can be run today:

* the test frameworks in use
* the command for each category, and the directory it should run from
* any setup required to run tests, including services, fixtures, and env vars

If no tests exist, report that explicitly.

Also write a structured TOML file to:

```text
{{OUTPUT_DIR}}/tests.toml
```

Use this shape:

```toml
[tests]
fast = "QUICK_VERIFICATION_COMMAND"
all = "RUN_ABSOLUTELY_ALL_TESTS"
AREA_X = "COMMAND_X"
AREA_Y = "COMMAND_Y"
```

Replace the placeholders with actual commands. Areas are things like `backend`,
`frontend`, `cli`, or `benchmark`.

## How Complete Is The Test Story

Flag:

* test commands that do not match what the project actually supports
* missing categories, such as e2e tests with no documented runner
* broken or skipped suites worth surfacing
````

`.ateam/prompts/metaproject/audit/docker.prompt.md`:

```markdown
Audit the dev-environment-in-Docker setup. Flag:

* broken Dockerfile, compose, or devcontainer setup
* commands that do not match what is documented

If no Docker setup exists, recommend whether one would be valuable given the
project's stack. Stop if not.
```

## Fix Prompts

The fix prompt bodies are intentionally tiny. The shared wrapper injects the
audit report for the scope and handles output instructions.

`.ateam/prompts/metaproject/fix/_pre.context.md`:

````markdown
# Audit Report For `{{prompt.name}}`

{{include? /shared/metaproject/{{arg.target_label}}/{{prompt.name}}/audit.md}}

# Action

You are running `fix` for scope `{{prompt.name}}`.

If the audit report above is missing or has no actionable items, you are done
and your last message must be exactly:

```text
no audit items to implement
```

Otherwise, make the recommended changes inside:

```text
{{PROJECT_FULL_PATH}}
```
````

`.ateam/prompts/metaproject/fix/_post.output.md`:

````markdown
Save your fix report to:

```text
{{OUTPUT_DIR}}/fix.md
```
````

Each scope body can be either empty or a one-line specialization. Example:

`.ateam/prompts/metaproject/fix/test.prompt.md`:

```markdown
When changing test documentation or commands, prefer commands that a developer
can run locally without paid API calls.
```

The other scopes can use minimal bodies:

```markdown
Apply the scoped audit findings.
```

## Verify Prompts

`.ateam/prompts/metaproject/verify/_pre.context.md`:

````markdown
# Fix Report For `{{prompt.name}}`

{{include? /shared/metaproject/{{arg.target_label}}/{{prompt.name}}/fix.md}}

# Action

You are running `verify` for scope `{{prompt.name}}`.

Try to follow the documented steps or run the documented commands in:

```text
{{PROJECT_FULL_PATH}}
```

If there are issues, fix them and update the fix report to be accurate. For any
change you make in that file, note that you made changes, why, and what they
are.
````

`.ateam/prompts/metaproject/verify/_post.output.md`:

````markdown
Save your verify report to:

```text
{{OUTPUT_DIR}}/verify.md
```
````

Each scope body can again be tiny:

```markdown
Verify the scoped fix work and correct stale instructions.
```

## Thin Dispatcher Shape

Even with the prompt proposal, keep a small dispatcher for what ateam explicitly
does not do: scheduling, target parsing, and pipeline expansion.

The dispatcher becomes mostly command selection:

```sh
# discover
ateam exec :metaproject/discover \
  --work-dir "$target_path" \
  --arg target_label="$target_label"

# audit all scopes for one target
ateam parallel \
  :metaproject/audit/claude \
  :metaproject/audit/gitconfig \
  :metaproject/audit/build \
  :metaproject/audit/test \
  :metaproject/audit/docker \
  --work-dir "$target_path" \
  --arg target_label="$target_label"

# fix all scopes for one target
ateam parallel \
  :metaproject/fix/claude \
  :metaproject/fix/gitconfig \
  :metaproject/fix/build \
  :metaproject/fix/test \
  :metaproject/fix/docker \
  --work-dir "$target_path" \
  --arg target_label="$target_label"

# verify all scopes for one target
ateam parallel \
  :metaproject/verify/claude \
  :metaproject/verify/gitconfig \
  :metaproject/verify/build \
  :metaproject/verify/test \
  :metaproject/verify/docker \
  --work-dir "$target_path" \
  --arg target_label="$target_label"
```

With `{{arg.*}}`, the dispatcher no longer needs to abuse per-job labels for
target identity. If the user still wants per-job display labels, those can stay
orthogonal:

```sh
ateam parallel \
  :metaproject/audit/claude \
  :metaproject/audit/gitconfig \
  :metaproject/audit/build \
  :metaproject/audit/test \
  :metaproject/audit/docker \
  --work-dir "$target_path" \
  --arg target_label="$target_label" \
  --labels claude,gitconfig,build,test,docker
```

Then prompt files and scripts use `{{arg.target_label}}`, while `{{LABEL}}`
remains an optional display/job label.

## Assessment

### More Or Less Code?

Raw line count is not a clean answer because the new shape moves code across
three surfaces:

| Surface | Current `metaproject.py` | Prompt-files proposal |
|---|---:|---:|
| Single dispatcher file | 754 lines | likely 80-180 lines |
| Prompt text / wrappers | embedded inside the 754 lines | likely 200-300 lines across prompt files |
| Skip/check/promote helpers | embedded inside the 754 lines | likely 120-220 lines across scripts |
| ateam framework support | already exists partially | requires stage executor, `{{shell}}`, `ateam flow`, `{{arg.*}}`, promotion support |

For the metaproject-owned code, the result is probably **similar or slightly
less total code**, not a dramatic reduction. The big win is that far less of it
is Python control flow. Static prompt content becomes plain Markdown. The
remaining code is mostly thin dispatch and deterministic helper scripts.

If the stage-executor features do not exist, this approach is **more code and
more moving parts** because the Python script still has to simulate them. Once
they exist, this approach is likely simpler for this workflow.

### Is It Actually More Readable?

For prompt authors, yes. The current Python interleaves strings, target parsing,
mtime checks, backup logic, and subprocess calls. The proposal separates those
concerns:

* `ls .ateam/prompts/metaproject/audit/` shows every audit scope.
* Opening `audit/test.prompt.md` shows only the test audit instruction.
* Opening `audit/_pre.context.md` shows the common context for every audit.
* `_meta.yaml` shows lifecycle hooks without reading Python.

For workflow debugging, it is only more readable if `ateam prompt --preview`
shows the assembled prompt, merged frontmatter, resolved args, and ordered
pre/post hooks. Without that preview, readability gets worse because the logic
is split across prompt files, scripts, and the dispatcher.

### Concrete Strengths

1. **Prompt content is no longer hidden inside Python.** Scope prompts become
   small Markdown files that are easier to review, diff, override, and reuse.
2. **Common context becomes explicit.** `_pre.context.md` and `_post.output.md`
   make it obvious what every scope receives before and after its role-specific
   body.
3. **`{{arg.*}}` solves target identity cleanly.** `{{arg.target_label}}` is a
   real runtime input; `{{LABEL}}` can remain a parallel job label instead of
   carrying unrelated meaning.
4. **Runtime history replaces backup mechanics.** The current `backup()` helper
   exists because reports are overwritten in place. `runtime/<exec_id>/` gives
   history by default, then promotion writes the stable artifact.
5. **Skip logic becomes lifecycle logic.** Mtime checks belong in `pre_exec`
   scripts that call `ateam flow skip`, not in prompt assembly.
6. **Prior outputs are first-class prompt inputs.** `{{include?}}` makes review
   of previous audit/fix files explicit and previewable.
7. **It scales better across scopes.** Adding a new scope is a new
   `<scope>.prompt.md` file plus optional hook logic, not another decorated
   Python function.

### Concrete Weaknesses

1. **More files.** The workflow becomes easier to scan by directory, but harder
   to understand if someone expects one file to contain everything.
2. **The stage executor is required for the simplicity claim.** Without
   `pre_exec`, `post_exec`, `{{shell}}`, and `ateam flow`, the dispatcher must
   keep most of the old Python behavior.
3. **Promotion needs parameters.** Plain `copy-runtime-files` promotes to
   `shared/<prompt-path>/`, but this workflow needs
   `shared/metaproject/<target-label>/<scope>/`. A parameterized promote action
   or a script is necessary.
4. **`{{shell}}` is not optional for this example.** Discover mode and
   "changes since overview" are deterministic context. They should be computed
   during assembly and visible in preview.
5. **Hook failure semantics matter.** If `skip-audit.sh` or
   `promote-metaproject.sh` fails, users need obvious status, logs, and failure
   behavior. Otherwise the split makes failures harder to reason about.
6. **The dispatcher does not disappear.** ateam still should not become a
   scheduler or full workflow engine. Target parsing, `discover+`, `audit+`,
   and multi-project fan-out remain external glue.
7. **Template paths can become noisy.** Paths like
   `/shared/metaproject/{{arg.target_label}}/{{prompt.name}}/audit.md` are clear
   enough, but they are still a mini-language. Preview tooling has to make the
   resolved paths visible.

### Simplicity Verdict

The proposal is not simpler because it reduces line count. It is simpler because
it moves each kind of work to a narrower mechanism:

| Concern | Current script | Proposal |
|---|---|---|
| Prompt wording | Python string literals | Markdown prompt files |
| Shared prompt context | Python string assembly | dir-level `_pre.*.md` |
| Previous artifacts | Python file reads | `{{include?}}` |
| Dynamic context | Python functions | `{{shell}}` |
| Skip checks | Python branches | `pre_exec` plus `ateam flow skip` |
| Artifact promotion | Python path helpers / backup | runtime history plus post hook |
| Pipeline fan-out | Python loops | still dispatcher glue |

So the honest answer is:

* **Less application code:** likely yes, once the stage-executor primitives
  exist.
* **Less total surface area:** no; it becomes more distributed.
* **More readable prompt logic:** yes.
* **More readable whole workflow:** yes only with strong preview/inspect output.
* **Worth doing:** yes for ateam, because this is exactly the class of workflow
  the prompt filesystem is meant to make inspectable and reusable.

## Bottom Line

The current proposal is a better home for the prompt system in
`metaproject.py`, but it does not fully replace the Python script by itself. The
clean split is:

* prompt files own prompt text, wrappers, includes, and declared lifecycle hooks
* scripts own deterministic checks and artifact promotion
* a thin dispatcher owns target parsing and pipeline fan-out

To make this example feel first-class, the proposal should prioritize `{{arg.*}}`,
`{{shell}}`, `ateam flow`, parameterized promotion, and preview output that shows
the assembled prompt plus the ordered stage hooks.
