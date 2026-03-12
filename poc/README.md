# ATeam — AI Role Team for Code Analysis

A Go CLI to improve a project quality using coding agents as unattended as desired. Have project quality improved while you sleep. Focus on feature work and have a team of expert agent improve your project code, tests, scripts, documentation.

It automates role specific coding agents to improve project quality along multiple dimensions: code refactoring, testing, documentation, security, ... It audits, prioritize and perform the code changes unattended via pre-built but customizable prompts. All steps produce easy to audit (and edit) markdown files:
* role specific reports
* supervisor synthesizes and prioritizes in a review document
* a supervisor coordinate the implementation of prioritized fixes
* reports and reviews get updated with progress and are ready for another round when you are

You can run it on demand (say after a coding day, before the week-end) or on a schedule (every day at night) for all or part the roles it knows how to do. Ateam is intended to run unattended with various sandboxing options to manage risks, isolation and convenience (i.e. don't constantly ask for approval).

## Why

Coding agents tend to produce sub-par code when implementing features. They also require a lot of attention between approvals and spec steering. As code is mostly written, read and modified by agents there is little incentive left for humans to perform code reviews except to gauge the current quality. At the same time coding agents are actually very good at finding refactoring opportunities, testing gaps, analyze dependencies and all the standard software quality tasks. So why keep reading code or why having to prompt agents to improve with these standard tasks ?

Let's just reuse or write project quality improvement prompts once and have background agents run in a sandbox without requiring approvals perform the work. We add supervisor layer to balance priorities between the various dimensions of the work.

The end result is to be able to focus our attention to the value generating feature work and let agents do the rest.

Ateam provides additional default/overload/tracking structure compared to just 'claude -p "/simplify"' or other simple one-liners: multi-role audit + supervisor prioritization taking more decision fatigue away, built-in agent switching and sandboxing, audit and cost tracking. Also an out of the box immediate working state for any code base.

Note that Ateam is not made to provide generic agent workflow management for feature work, it provides one simple and effective workflow for orthogonal project quality features that don't need much customization. Agent workflow management require a lot of attention to learn and configure, ateam is designed for the opposite: run it and forget it, see a stream of commits you can choose to integrate or not.

Ateam makes use of existing coding agents instead of having its own agent talking to the LLM models directly. It is done to leverage the fast pace work going on coding agents and for familiary of behavior. A built-in agent could easily be added in the future if using existing coding agents is problematic

## Features

- **16 built-in roles** — security, testing, refactoring, dependencies, documentation, project profiling, and more
- **3-level prompt fallback** — project overrides → org overrides → embedded defaults. You can also just add extra prompts and benefit from default prompts to customize
- **Add new roles** - just create a directory and role specific prompt file to add particular type of audit
- **Multi-project support** — multiple ateam projects can share a set of personal/organizational defaults
- **Multi-project per repo support** — multiple ateam projects per git repo (monorepo-friendly)
- **Auditability**: see current and historical reports, execution, logs
- **Parallel execution** — configurable concurrency with per-role timeouts
- **Stream-json output** — real-time JSONL stream capture with cost/token tracking

TODO:
- cost tracking and limiting
- flexible sandboxing and agent clients
  - for example: audit with codex or gemini and code with claude

## Workflow

You run 'ateam init' within a directory or at the base of a git repo (either your main work area or a separate checkout), it will create a .ateam/ project directory where configuration, prompts and reports are stored. Runtime state (stream logs, stderr captures, runner log) lives in `.ateamorg/projects/<project-id>/` (derived from the project's relative path), keeping `.ateam/` safe to version-control.

  Ignore all:

    **/.ateam/

  Version prompts and reports (runtime state is already outside the repo):

    # nothing to ignore — .ateam/ is clean

Then the workflow is:

  ateam report --roles CHOOSE_SOME_ROLES       # commission role reports
  ateam review --print                         # supervisor synthesizes a prioritized review
  ateam code                                   # supervisor delegates tasks as code changes


Can also be more methological:
* edit .ateam/config.toml to enable/disable relevant roles (you should probably never run all of them)
* gather information

  ateam report && ateam review --print

* Edit reports and reviews to make sure you specify the work you want to occur.
* code:

  ateam code


* Example of scheduled runs with a subset of all the work:
Run at night:

  ateam all --roles refactor_small,docs_external,testing_basic

Run on Fridays:

  ateam all --roles security,dependencies,testing_full

### Git
* use your work area, use ateam directly on main, get commits and rebase done automatically
* use a separate checkout of your repo, work from main or a branch
* create an 'ateam_work' branch and git worktree, do your work there

### Provide feedback
* use report_extra_prompt.md or review_extra_prompt.md to specify rejected approaches so they are taken into account in the future
  * can also document rejected comments for the same reason


## Prerequisites

- Go 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`claude` command available in PATH)

## Install

```bash
git clone <repo-url>
cd poc
make build
```

Copy or symlink the `ateam` binary to somewhere in your PATH.

## Commands

### Global flags

All commands accept these flags:

| Flag | Short | Description |
|------|-------|-------------|
| `--org PATH` | `-o` | Organization path override (skips auto-discovery) |
| `--project NAME` | `-p` | Project name override (skips auto-discovery) |

### `ateam install [PATH]`

Create a `.ateamorg/` directory with default prompts for all roles and the supervisor.

```bash
ateam install              # creates .ateamorg/ in current directory
ateam install ~/projects   # creates .ateamorg/ at the given path
```

### `ateam init [PATH]`

Initialize a project by creating a `.ateam/` directory at PATH (defaults to `.`).

If no `.ateamorg/` is found, you are prompted to create one. Use `--org-home` or `--org-create` to skip the interactive prompt.

```bash
ateam init
ateam init --name myproject --role testing_basic,security
ateam init --org-home                      # auto-create .ateamorg/ in $HOME
ateam init --org-create ~/projects         # auto-create .ateamorg/ at path
```

| Flag | Description |
|------|-------------|
| `--name NAME` | Project name (defaults to relative path from org root) |
| `--role LIST` | Roles to enable (comma-separated; if omitted, defaults are used) |
| `--git-remote URL` | Git remote origin URL (auto-detected if omitted) |
| `--org-create PATH` | Create `.ateamorg/` at PATH if none exists |
| `--org-home` | Create `.ateamorg/` in `$HOME` if none exists |

### `ateam report`

Run one or more roles in parallel to analyze the project and produce markdown reports.

```bash
ateam report --roles all
ateam report --roles security,testing_basic
ateam report --roles all --extra-prompt "Focus on the API layer"
ateam report --roles all --extra-prompt @notes.md
ateam report --roles all --dry-run
ateam report --roles all --print
```

| Flag | Description |
|------|-------------|
| `--roles LIST` | Comma-separated role list, or `all` **(required)** |
| `--extra-prompt TEXT` | Additional instructions appended to every role's prompt (text or `@filepath`) |
| `--timeout MINUTES` | Timeout per role (overrides `config.toml`) |
| `--print` | Print reports to stdout after completion |
| `--dry-run` | Print computed prompts without running roles |

Output table columns: `ROLE`, `ENDED_AT`, `ELAPSED`, `COST`, `TURNS`, `STATUS`, `PATH`.

### `ateam review`

Have the supervisor read all role reports and produce a prioritized decisions document.

```bash
ateam review
ateam review --extra-prompt "This is a production financial app"
ateam review --prompt @custom_review.md
ateam review --dry-run
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions appended to the supervisor prompt (text or `@filepath`) |
| `--prompt TEXT` | Custom prompt replacing the default supervisor role entirely (text or `@filepath`) |
| `--timeout MINUTES` | Timeout (overrides `config.toml`) |
| `--print` | Print review to stdout after completion |
| `--dry-run` | Print computed prompt and list reports without running |

### `ateam code`

Read the review document and execute prioritized tasks as code changes, delegating each task to the appropriate role via `ateam run`.

```bash
ateam code
ateam code --review @custom_review.md
ateam code --management @custom_management.md
ateam code --dry-run
```

| Flag | Description |
|------|-------------|
| `--review TEXT` | Review content (text or `@filepath`; defaults to `.ateam/supervisor/review.md`) |
| `--management TEXT` | Management prompt override (text or `@filepath`) |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--timeout MINUTES` | Timeout in minutes (overrides `config.toml`; default 120) |
| `--print` | Print output to stdout after completion |
| `--dry-run` | Print the computed prompt without running |

### `ateam prompt`

Resolve and print the full prompt for a role or supervisor without running it. Useful for debugging prompt assembly.

```bash
ateam prompt --role security --action report
ateam prompt --role refactor_small --action code
ateam prompt --role security --action report --extra-prompt "Focus on auth"
ateam prompt --supervisor --action review
ateam prompt --supervisor --action code
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role name (mutually exclusive with `--supervisor`) |
| `--supervisor` | Generate supervisor prompt instead of role prompt |
| `--action ACTION` | Action type: `report` or `code` for roles; `review` or `code` for supervisor **(required)** |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--no-project-info` | Omit the ATeam Project Context section from the prompt |
| `--ignore-previous-report` | Do not include the role's previous report in the prompt |

### `ateam log`

Pretty-format the last stream JSONL log for a role or the supervisor.

```bash
ateam log --supervisor
ateam log --supervisor --action review
ateam log --role security
ateam log --role security --action report
```

| Flag | Description |
|------|-------------|
| `--supervisor` | Show supervisor log (defaults to `code` action) |
| `--role ROLE` | Show role log (defaults to `run` action) |
| `--action ACTION` | Override the action (e.g. `report`, `code`, `review`, `run`) |

### `ateam env`

Show the current ATeam environment: organization, project, roles, and latest report/review timestamps. Read-only — never creates or modifies anything.

```bash
ateam env
```

### `ateam projects`

List all projects discovered under the current organization.

```bash
ateam projects
```

### `ateam run`

Run an agent with a prompt. Can run standalone (just needs `.ateamorg/`) or within a project context. By default prints only the final message to stdout.

```bash
ateam run "say hello"
ateam run "Analyze the auth module" --role security
ateam run @prompt.md --role testing_basic --stream
ateam run "test" --profile test
ateam run "say hi" --model sonnet
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role to run (optional — requires project context) |
| `--profile NAME` | Runtime profile to use (overrides config resolution) |
| `--model MODEL` | Model override |
| `--stream` | Show progress updates on stderr during execution |
| `--work-dir PATH` | Working directory (defaults to project source dir or cwd) |
| `--summary` | Print cost/duration/tokens summary to stderr after completion |

Returns the agent's exit code. Agent stderr is forwarded to stderr.

### `ateam roles`

List roles configured for the current project.

```bash
ateam roles                  # all roles with status (default)
ateam roles --enabled        # enabled roles only
ateam roles --available      # same as default
```

| Flag | Description |
|------|-------------|
| `--enabled` | List enabled roles only |
| `--available` | List all roles with status (default) |

### `ateam update`

Update on-disk default prompts to match the version embedded in the current binary.

```bash
ateam update
ateam update --diff
ateam update --quiet
```

| Flag | Description |
|------|-------------|
| `--diff` | Show diffs between on-disk and embedded prompts |
| `--quiet`, `-q` | Suppress diff output |

## Directory Layout

### Organization: `.ateamorg/`

Created by `ateam install`. Holds shared defaults and org-level overrides.

```
.ateamorg/
  projects/<project-id>/                        # runtime state per project (see below)
  defaults/                                    # embedded prompts written to disk
    report_base_prompt.md                      # shared report base instructions
    code_base_prompt.md                        # shared code base instructions
    roles/<NAME>/report_prompt.md              # per-role report prompt
    roles/<NAME>/code_prompt.md                # per-role code prompt (where available)
    supervisor/review_prompt.md                # supervisor review prompt
    supervisor/code_management_prompt.md       # supervisor code management prompt
    supervisor/report_commissioning_prompt.md  # report commissioning prompt
  report_base_prompt.md                        # org-level report base override (optional)
  code_base_prompt.md                          # org-level code base override (optional)
  report_extra_prompt.md                       # org-wide extra instructions for reports (optional)
  code_extra_prompt.md                         # org-wide extra instructions for code (optional)
  roles/                                       # org-level role overrides
    <NAME>/report_prompt.md                    # override a specific role's report prompt
    <NAME>/report_extra_prompt.md              # extra instructions for this role's reports
    <NAME>/code_prompt.md                      # override a specific role's code prompt
    <NAME>/code_extra_prompt.md                # extra instructions for this role's code
  supervisor/                                  # org-level supervisor overrides
    review_prompt.md
    review_extra_prompt.md                     # extra instructions for reviews
    code_management_prompt.md
    code_management_extra_prompt.md            # extra instructions for code management
```

### Project: `.ateam/`

Created by `ateam init`. Holds project config, prompts, reports, and history (version-controllable).

```
.ateam/
  config.toml                                # project configuration
  report_base_prompt.md                      # project-level report base override (optional)
  code_base_prompt.md                        # project-level code base override (optional)
  report_extra_prompt.md                     # project-wide extra instructions for reports (optional)
  code_extra_prompt.md                       # project-wide extra instructions for code (optional)
  roles/<NAME>/
    report_prompt.md                         # project-level role report prompt override (optional)
    report_extra_prompt.md                   # extra instructions for this role's reports (optional)
    code_prompt.md                           # project-level role code prompt override (optional)
    code_extra_prompt.md                     # extra instructions for this role's code (optional)
    report.md                                # latest successful report
    report_error.md                          # error details (on failure only)
    history/                                 # timestamped archive
      2026-03-08_15-04-00.report_prompt.md       # archived prompt
      2026-03-08_15-04-00.report.md              # archived report
  supervisor/
    review_prompt.md                         # project-level supervisor override (optional)
    review_extra_prompt.md                   # extra instructions for reviews (optional)
    code_management_prompt.md                # project-level code management override (optional)
    code_management_extra_prompt.md          # extra instructions for code management (optional)
    review.md                                # latest successful review
    review_error.md                          # error details (on failure only)
    code_output.md                           # latest code management output
    code_error.md                            # error details (on failure only)
    history/
      2026-03-08_15-04-00.review_prompt.md
      2026-03-08_15-04-00.review.md
```

### Runtime state: `.ateamorg/projects/<project-id>/`

Runtime files are stored outside the project, keyed by the project's relative path from the org root (escaped: `_` → `__`, `/` → `_`). For example, project at `services/api` gets project ID `services_api`.

```
.ateamorg/projects/<project-id>/
  runner.log                                 # append-only execution log
  roles/<NAME>/logs/
    2026-03-10_22-17-58_report_exec.md       # full execution context (env, settings, prompt)
    2026-03-10_22-17-58_report_stream.jsonl  # raw JSONL stream
    2026-03-10_22-17-58_report_stderr.log    # stderr capture
    2026-03-10_22-17-58_report_settings.json # sandbox settings used
  supervisor/logs/
    2026-03-10_22-18-00_review_exec.md
    2026-03-10_22-18-00_review_stream.jsonl
    2026-03-10_22-18-00_review_stderr.log
    2026-03-10_22-18-00_review_settings.json
```

### Migrating from UUID-based or old-encoding state directories

Older versions used a random UUID per project (stored in `config.toml` as `project_uuid`) or a
different encoding (`_S` for `/`, `_D` for `.`) to key state directories. Current versions derive
the project ID from the project's relative path using `_` for `/` and `__` for `_`.

To migrate:

1. Delete the old state directories and registry:
   ```bash
   rm -rf .ateamorg/projects/ .ateamorg/orgconfig.toml
   ```
2. Optionally remove `project_uuid` lines from `.ateam/config.toml` (harmless if left — parsed but ignored)

New state directories are created automatically on the next `ateam report`, `ateam review`, or `ateam run`.

### `config.toml`

```toml
[project]
name = "myproject"

[git]
repo = "."
remote_origin_url = "git@github.com:org/repo.git"

[report]
max_parallel = 3
report_timeout_minutes = 20

[review]
timeout_minutes = 20

[code]
timeout_minutes = 120

[roles]
security = "enabled"
testing_basic = "enabled"
refactor_small = "disabled"
```

## Prompt Resolution

Prompts are resolved with a 3-level fallback: **project** → **org** → **org defaults**. The first file found wins. This lets you customize prompts at any level without modifying the embedded defaults.

The placeholder `{{SOURCE_DIR}}` in prompts is replaced with the absolute path to the project source directory.

### ATeam Project Context

All prompts (role and supervisor) start with an **ATeam Project Context** section containing:

- Runtime files path, project name
- Role (e.g. "role security", "the supervisor")
- Source code directory and reports directory
- Git metadata: last commit hash/date/message, uncommitted changes

Use `--no-project-info` on `ateam prompt` to omit this section.

### Role prompt assembly (`report` and `code`)

Parts are concatenated with `---` separators in this order:

```
ATeam Project Context → Base prompt → Role-specific prompt → Extra prompts → CLI --extra-prompt
```

| Part | Source | Required |
|------|--------|----------|
| **ATeam Project Context** | Auto-generated | No |
| **Base prompt** | 3-level fallback: `report_base_prompt.md` or `code_base_prompt.md` | At least one of base or role required |
| **Role-specific prompt** | 3-level fallback: `roles/<NAME>/report_prompt.md` or `code_prompt.md` | At least one of base or role required |
| **Extra prompts** | Additive from all levels (see below) | No |
| **CLI extra** | `--extra-prompt` flag | No |

Base prompt 3-level fallback (e.g. for report):

1. `.ateam/report_base_prompt.md`
2. `.ateamorg/report_base_prompt.md`
3. `.ateamorg/defaults/report_base_prompt.md`

Role-specific prompt 3-level fallback (e.g. for report):

1. `.ateam/roles/<NAME>/report_prompt.md`
2. `.ateamorg/roles/<NAME>/report_prompt.md`
3. `.ateamorg/defaults/roles/<NAME>/report_prompt.md`

If a role has no role-specific prompt for an action (e.g. no `code_prompt.md`), the base prompt alone is used — this is not an error. Both base and role missing is an error.

### Supervisor prompt assembly (`review` and `code`)

Parts are concatenated with `---` separators in this order:

```
ATeam Project Context → Action prompt → Extra prompts → Review → CLI --extra-prompt
```

| Part | Source | Required |
|------|--------|----------|
| **ATeam Project Context** | Auto-generated | No |
| **Action prompt** | 3-level fallback or `--prompt`/`--management` override | Yes |
| **Extra prompts** | Additive from org and project levels (see below) | No |
| **Review** | Role reports (for `review`) or review document (for `code`) | Yes |
| **CLI extra** | `--extra-prompt` flag | No |

Action prompt 3-level fallback (e.g. for review):

1. `.ateam/supervisor/review_prompt.md`
2. `.ateamorg/supervisor/review_prompt.md`
3. `.ateamorg/defaults/supervisor/review_prompt.md`

For `ateam code`, the fallback uses `code_management_prompt.md` at each level.

### Extra prompts

Extra prompts are **additive** — all matching files are included (not fallback). They are appended after the main prompt, before any CLI `--extra-prompt`.

For roles, extras are collected from four locations in order:

1. `.ateamorg/report_extra_prompt.md` — org-wide
2. `.ateamorg/roles/<NAME>/report_extra_prompt.md` — org role-specific
3. `.ateam/report_extra_prompt.md` — project-wide
4. `.ateam/roles/<NAME>/report_extra_prompt.md` — project role-specific

(Same pattern with `code_extra_prompt.md` for the code action.)

For supervisors, extras are collected from two locations:

1. `.ateamorg/supervisor/review_extra_prompt.md` — org-level
2. `.ateam/supervisor/review_extra_prompt.md` — project-level

(Same pattern with `code_management_extra_prompt.md` for the code action.)

### Default prompt files

The embedded default prompts are in the source tree under [`internal/prompts/defaults/`](internal/prompts/defaults/):

| Prompt | Source file |
|--------|------------|
| Report base instructions | [`defaults/report_base_prompt.md`](internal/prompts/defaults/report_base_prompt.md) |
| Code base instructions | [`defaults/code_base_prompt.md`](internal/prompts/defaults/code_base_prompt.md) |
| Supervisor review | [`defaults/supervisor/review_prompt.md`](internal/prompts/defaults/supervisor/review_prompt.md) |
| Supervisor code management | [`defaults/supervisor/code_management_prompt.md`](internal/prompts/defaults/supervisor/code_management_prompt.md) |
| Role: security | [`defaults/roles/security/report_prompt.md`](internal/prompts/defaults/roles/security/report_prompt.md) |
| Role: testing_basic | [`defaults/roles/testing_basic/report_prompt.md`](internal/prompts/defaults/roles/testing_basic/report_prompt.md) |
| Role: refactor_small | [`defaults/roles/refactor_small/report_prompt.md`](internal/prompts/defaults/roles/refactor_small/report_prompt.md) |

All role prompts follow the same pattern: `defaults/roles/<NAME>/report_prompt.md` (and optionally `code_prompt.md`).

## Roles

Roles are auto-discovered from [`internal/prompts/defaults/roles/`](internal/prompts/defaults/roles/). Each subdirectory containing a `report_prompt.md` becomes a valid role. Use `all` as shorthand for every enabled role.

Available roles: `automation`, `basic_project_structure`, `critic_engineering`, `critic_project`, `database_config`, `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `production_ready`, `project_characteristics`, `refactor_architecture`, `refactor_small`, `security`, `shortcut_taker`, `testing_basic`, `testing_full`.

## Troubleshooting

### Runner log

Every `ateam report` and `ateam review` invocation is logged to `.ateamorg/projects/<project-id>/runner.log`. Each line is tab-separated with quoted fields:

```
TIMESTAMP  "ROLE"  "STATUS"  "CWD"  "CLI"  [EXTRA...]
```

- **start** lines include the prompt path and output path (relative to `.ateam/`)
- **ok** lines confirm successful completion
- **error** lines include the error message

Example:

```
2026-03-08_15-04-00	"security"	"start"	"/home/user/myapp"	"claude -p --output-format stream-json --verbose"	"roles/security/history/2026-03-08_15-04-00.report_prompt.md"	"roles/security/report.md"
2026-03-08_15-06-23	"security"	"ok"	"/home/user/myapp"	"claude -p --output-format stream-json --verbose"
2026-03-08_15-07-01	"testing_basic"	"error"	"/home/user/myapp"	"claude -p --output-format stream-json --verbose"	"timed out after 10 minutes"
```

### Detailed output

Use `--dry-run` on `report`, `review`, and `code` to inspect the fully assembled prompt without running anything:

```bash
ateam report --roles security --dry-run      # print the prompt that would be sent
ateam review --dry-run                       # print prompt and list discovered reports
ateam code --dry-run                         # print the code management prompt
ateam prompt --role security --action report  # resolve and print a role prompt
```

Use `ateam log` to pretty-format the last stream JSONL:

```bash
ateam log --supervisor               # last code management stream
ateam log --supervisor --action review  # last review stream
ateam log --role security            # last run stream for a role
```

When a run fails, inspect these files:

| File | Location | Content |
|------|----------|---------|
| `report_error.md` | `.ateam/roles/<NAME>/` | Error summary, exit code, duration, stderr, partial output, token usage |
| `*_stderr.log` | `.ateamorg/projects/<project-id>/roles/<NAME>/logs/` | Raw stderr from the `claude` subprocess |
| `*_stream.jsonl` | `.ateamorg/projects/<project-id>/roles/<NAME>/logs/` | Raw JSONL event stream (useful for debugging parsing issues) |
| `*_exec.md` | `.ateamorg/projects/<project-id>/roles/<NAME>/logs/` | Full execution context: env, settings, prompt |

For the supervisor, error files are `.ateam/supervisor/review_error.md` (review) and `.ateam/supervisor/code_error.md` (code). Runtime logs are in `.ateamorg/projects/<project-id>/supervisor/logs/`.

### History

Every run archives its prompt and output to the `history/` directory with a timestamp prefix:

```bash
ls .ateam/roles/security/history/
# 2026-03-07_14-30-00.report_prompt.md
# 2026-03-07_14-30-00.report.md
# 2026-03-08_09-00-00.report_prompt.md
# 2026-03-08_09-00-00.report.md

ls .ateam/supervisor/history/
# 2026-03-07_14-35-00.review_prompt.md
# 2026-03-07_14-35-00.review.md
```

This lets you compare reports across runs and trace what prompt produced what output.

## Development

### Build

```bash
make build        # tidy + build with embedded build timestamp
make clean        # remove binary
```

Or manually:

```bash
go build -o ateam .
```

### Test

```bash
go test ./...                        # all tests
go test ./internal/config/ -v        # config tests
go test ./internal/prompts/ -v       # prompt fallback tests
go test ./internal/root/ -v          # resolution + integration tests
go test ./internal/runner/ -v        # stream event parsing tests
```

### Adding a new role

1. Create `internal/prompts/defaults/roles/ROLE_NAME/report_prompt.md`
2. Optionally add `code_prompt.md` in the same directory
3. Rebuild with `make build` — the role is auto-discovered from the embedded filesystem

## Future
* execution flexibility
  * support codex
  * support model choice
  * support sandboxing options: docker, MacOS container, etc ...
  * easily default at install time
  * use different models/clis for different tasks: review with codex, code with claude, report with codex
* move orchestration logic out of the supervisor prompts into the ateam CLI:
  * git workflow
  * control of report and review break down in tasks and check completed tasks
* explicit workspace management using container
  * remove all permission checks and all sandboxing
  * can enter a docker container to debug issues and run in the same environment as the role
* better context and memory
  * reduce prompt size
    * by moving more of the instructions to the tooling around
* maintain a current view of a project: overview.md and update it based on commit
  * time generated
  * last commit
