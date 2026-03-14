# ATeam — AI Role Team for Code Analysis

ATeam is a CLI, point it at your codebase and a crew of role-specific coding agents gets to work across multiple dimensions: code refactoring, testing, documentation, security, and more. Each agent audits the code, another one prioritizes findings,and then runs coding agents to implement the selected fixes.

It is designed to work out of the box unattended for most software project size and for any tech stack. It can also be ran on-demand and customized by adding or changing prompts. This way you can focus on feature work and have your project quality improve while you sleep with agents instructed to make pragmatic choices and balance priorities for you.

ATeam is also transparent and auditable. Agents produce markdown artifacts at every step:
* Role reports: per-agent findings and recommendations
* Supervisor review: synthesized priorities across all roles
* Supervisor manages coding of the top priority tasks and record what is completed

Then ateam is ready for the next round: reports get updated so each run builds on the last.

Run it on demand — after a coding session, before the weekend — or schedule it nightly. Choose which roles to run. Configure sandboxing to match your risk tolerance: full isolation, selective approval, or fully unattended. ATeam is designed to stay out of your way.

At the core ateam is very simple: your existing coding agent (claude code, codex, more later), some prompt markdown files and other markdown files for the various reports it produces if you wish to audit or modify. It does use a small sqlite database to track cost across coding agent runs and provide aggregated reports.

ATeam should feel like the missing part of agentic coding: add expert colleagues to a solo project or be the infra/platform team big software companies have but scaled to match your project small or big and grow with it.

## Why

ATeam comes from the realization that with agentic software engineering:
* coding agents need to prioritize feature completion over software quality (this is a good tradeoff for the short term)
* feature work requires a lot of attention: back and forth prompting, iteration on how the feature works, approvals, ...
* over time software quality becomes an issue: feature work breaks existing functionality, code changes take longer, security issues are created, dependencies/docs/tests are out of date, ...
* agents are actually very good at reviewing code or entire projects and finding general software quality issues
* if agents are the only one touching the code why even review what they produce besides big high level aspects relevant to current and future feature work ? Code is more and more writing by agents for agents.

So ATeam was born: let's just have role specific agents that can be prompted once on general quality principles that can be applied to any project. Then to reduce prompting fatigue let's have a supervisor agent do the prioritization and act as a 2nd filter on what is worth doing. This should work as a simple CLI invocation or run on a schedule but most importantly can be unattended and require as little attention as possible. You can just see ateam's work as a stream of small focused commits if you even care. It should look as if the coding agents wrote the feature with good software quality engineering but without even taking longer, have ateam run while you sleep.

ATeam's core principles are:
* **no feature work**: focus on quality improvement, not feature (don't change existing behavior), this way its work requires no interactive prompting
* **unattended**: work on its own, doesn't ask for approval
* **be pragmatic**: smaller code bases don't have the same needs as bigger ones, number of collaborator matters, young projects need to focus on code quality over the rest, etc ...
* **look for opportunities for automation**: save future ateam work by automating linters, test scripts, audits, ...
* **simple**: reuse existing coding agents, close to no orchestration, no arbitrary agent framework
* **safe execution** via sandboxing or containers
* **generally applicable**: software quality is a great target because it relies mostly on principles that agents can follow to adapt to each project tech stack
* **works out of the box, yet customizable**: general prompts can work for a wide variety of project, ateam makes it easy to add new agent roles, add to existing prompts or overload existing prompts for a given project or many projects in one place
* **audit**: display cost related to ateam's work, make it easy to see any artifact used by ateam to make decision, review how the coding sessions were supervised
* **fit into your development workflow**: choose the git approach that works for you (direct to main for small project, integration branch, separate worktree or checkout, etc ...), work for an entire git repo or have multiple ateam instances for the same code base focusing on different components, etc ...

## Features

- **17 built-in roles** — security, testing, refactoring, dependencies, documentation, project profiling, and more
- **3-level prompt fallback** — project overrides → org overrides → embedded defaults. You can also just add extra prompts and benefit from default prompts to customize
- **Add new roles** - just create a directory and role specific prompt file to add particular type of audit
- **Multi-project support** — multiple ateam projects can share a set of personal/organizational defaults
- **Multi-project per repo support** — multiple ateam projects per git repo (monorepo-friendly)
- **Runtime profiles** — HCL-based configuration (`runtime.hcl`) with agent, container, and profile definitions; 3-level resolution (embedded → org defaults → org → project)
- **Multiple agents** — Claude Code, Codex, and custom agents configurable via `runtime.hcl`; switch per-command with `--profile` or `--agent`
- **Docker containers** — run agents inside Docker for full isolation (oneshot and persistent modes); auto-builds from configurable Dockerfile
- **Cost tracking** — per-run token/cost tracking via SQLite call database; `ateam cost` for aggregated reports, `ateam recent-runs` for run history
- **Auditability** — see current and historical reports, execution logs, and cost data
- **Parallel execution** — configurable concurrency with per-role timeouts
- **Stream-json output** — real-time JSONL stream capture with cost/token tracking
- **Full pipeline** — `ateam all` runs report → review → code in one command

## Workflow

You run 'ateam init' within a directory or at the base of a git repo (either your main work area or a separate checkout), it will create a .ateam/ project directory where configuration, prompts and reports are stored. Runtime state (stream logs, stderr captures, runner log) lives in `.ateamorg/projects/<project-id>/` (derived from the project's relative path), keeping `.ateam/` safe to version-control.

  Ignore all:

    **/.ateam/

  Version prompts and reports (runtime state is already outside the repo):

    # nothing to ignore — .ateam/ is clean

Then the workflow is:

  ateam report --roles all           # run all enabled roles
  ateam review                       # supervisor reviews and prioritizes
  ateam code                         # execute prioritized tasks
  ateam all                          # or run the full pipeline at once


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

- Go 1.24+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`claude` command available in PATH)
  - For OAuth token setup (recommended for unattended use using containers): `claude setup-token`
- [Open AI Codex CLI](https://developers.openai.com/codex/cli/?utm_source=chatgpt.com) (partial, ongoing work)

## Install

```bash
git clone <repo-url>
cd poc
make build
```

Have `ateam` in your PATH or symlink to it.

See [DEV.md](DEV.md) for development setup, testing, and architecture details.

## Commands

### Global flags

All commands accept these flags:

| Flag | Short | Description |
|------|-------|-------------|
| `--org PATH` | `-o` | Organization path override (skips auto-discovery) |
| `--project NAME` | `-p` | Project name override (skips auto-discovery) |

### `ateam install [PATH]` (Optional)

Create a `.ateamorg/` directory with default prompts, runtime config, and Dockerfile.

```bash
ateam install              # creates .ateamorg/ in current directory
ateam install ~/projects   # creates .ateamorg/ at the given path
```

### `ateam init [PATH]`

Initialize a project by creating a `.ateam/` directory at PATH (defaults to `.`).

If no `.ateamorg/` is found, you are prompted to create one (defaults to home directory). Use `--org-home` or `--org-create` to skip the interactive prompt.

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
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout per role (overrides `config.toml`) |
| `--print` | Print reports to stdout after completion |
| `--dry-run` | Print computed prompts without running roles |
| `--ignore-previous-report` | Do not include the role's previous report in the prompt |
| `--verbose` | Print agent and docker commands to stderr |

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
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout (overrides `config.toml`) |
| `--print` | Print review to stdout after completion |
| `--dry-run` | Print computed prompt and list reports without running |
| `--verbose` | Print agent and docker commands to stderr |

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
| `--profile NAME` | Profile for sub-runs (passed to `ateam run --profile`) |
| `--supervisor-profile NAME` | Profile for the supervisor itself |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout in minutes (overrides `config.toml`; default 120) |
| `--print` | Print output to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam all`

Run the full pipeline sequentially: report → review → code.

```bash
ateam all
ateam all --extra-prompt "Focus on security"
ateam all --timeout 30
ateam all --quiet
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions passed to all phases (text or `@filepath`) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Per-phase timeout (overrides config) |
| `--quiet` | Suppress output printing |
| `--verbose` | Print agent and docker commands to stderr |

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

Show the current ATeam environment: organization, runtime config, project, and role status. Read-only — never creates or modifies anything.

```bash
ateam env
```

### `ateam run`

Run an agent with a prompt. Can run standalone (just needs `.ateamorg/`) or within a project context. By default prints only the final message to stdout.

```bash
ateam run "say hello"
ateam run "Analyze the auth module" --role security
ateam run "test" --profile cheap
ateam run "say hi" --agent codex
ateam run "say hi" --model sonnet
ateam run "quick check" --quiet
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role to run (optional — requires project context) |
| `--profile NAME` | Runtime profile to use (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (mutually exclusive with --profile) |
| `--model MODEL` | Model override |
| `--work-dir PATH` | Working directory (defaults to project source dir or cwd) |
| `--agent-args "ARGS"` | Extra args passed to the agent CLI (appended after configured args) |
| `--task-group ID` | Group related calls (e.g. all tasks in one `ateam code` run) |
| `--no-stream` | Disable progress updates on stderr (on by default) |
| `--no-summary` | Disable cost/duration/tokens summary (on by default) |
| `--quiet` | Disable both streaming and summary |
| `--verbose` | Print agent and docker commands to stderr |

Returns the agent's exit code. Agent stderr is forwarded to stderr.

### `ateam cost`

Display aggregated cost and token usage, grouped by action type and by code session.

```bash
ateam cost
ateam cost --project myproject
```

When run inside a project, results are filtered to that project by default.

### `ateam recent-runs`

Display summary data about recent agent runs, with optional filtering.

```bash
ateam recent-runs
ateam recent-runs --role security
ateam recent-runs --action report
ateam recent-runs --limit 10
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Filter by role |
| `--action ACTION` | Filter by action (report, review, code, run) |
| `--limit N` | Max rows to show (default 30) |

When run inside a project, results are filtered to that project by default.

### `ateam projects`

List all projects discovered under the current organization.

```bash
ateam projects
```

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

Update on-disk default prompts and runtime config to match the version embedded in the current binary.

```bash
ateam update
ateam update --diff
ateam update --quiet
```

| Flag | Description |
|------|-------------|
| `--diff` | Show diffs between on-disk and embedded prompts |
| `--quiet`, `-q` | Suppress diff output |

## Customization Points

### Prompts

All prompt files can be modified at the project level (.ateam folder), organization for multiple projects (.ateamorg folder) or rely on the built-in defaults.

You can either redefine a prompt file or add to it by adding a prompt called ACTION_extra_prompt.md

To audit what prompt will be used use the following command:

  ateam prompt --role ROLE --action report
  ateam prompt --supervisor --action review
  ateam prompt --supervisor --action code

#### Report
* base_report_prompt.md: included for all roles
* base_report_extra_prompt.md: included for all roles (doesn't exist by default), useful to change how reports are generated
* roles/ROLE/
  * report_prompt.md: role specific unique instructions
  * report_extra_prompt.md: only add additional instruction (doesn't exist by default)

#### Review
* supervisor/
  * review_prompt.md
  * review_extra_prompt.md: only add additional instruction (doesn't exist by default). Very useful to permanently record some tasks you might not want to do or project specific guidelines that will be applied over all roles

#### Coding
* roles/ROLE/
  * code_prompt.md
* supervisor/
  * code_management_prompt.md

### How to run agents

### Modify Reports or Reviews

## Runtime Configuration

Runtime behavior is configured via `runtime.hcl` files using HCL syntax. The configuration defines agents, containers, and profiles.

### Resolution order

1. **Built-in defaults** — compiled into the binary
2. **Org defaults** — `.ateamorg/defaults/runtime.hcl`
3. **Org override** — `.ateamorg/runtime.hcl`
4. **Project override** — `.ateam/runtime.hcl`

Each level's blocks override (by name) those from the previous level. Use `ateam env` to see the active resolution chain.

### Agents

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet"]
}

agent "codex" {
  type    = "codex"
  command = "codex"
  args    = ["--sandbox", "workspace-write", "--ask-for-approval", "never"]
}
```

Agents support inheritance via `base`, sandbox settings (JSON), environment variables, and isolated config dirs.

### Containers

```hcl
container "none" {
  type = "none"
}

container "docker" {
  type        = "docker"
  mode        = "oneshot"        # or "persistent"
  dockerfile  = "Dockerfile"
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

### Profiles

Profiles combine an agent and a container:

```hcl
profile "default" {
  agent     = "claude"
  container = "none"
}

profile "docker" {
  agent     = "claude-docker"
  container = "docker"
}

profile "cheap" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "sonnet", "--max-budget-usd", "0.50"]
}
```

Use `--profile docker` on any command to run inside a container, or `--profile cheap` for cheaper runs.

## Directory Layout

### Organization: `.ateamorg/`

Created by `ateam install`. Holds shared defaults and org-level overrides.

```
.ateamorg/
  projects/<project-id>/                        # runtime state per project (see below)
  defaults/                                    # embedded defaults written to disk
    runtime.hcl                                # runtime config (agents, containers, profiles)
    Dockerfile                                 # default Dockerfile for container builds
    report_base_prompt.md                      # shared report base instructions
    code_base_prompt.md                        # shared code base instructions
    roles/<NAME>/report_prompt.md              # per-role report prompt
    roles/<NAME>/code_prompt.md                # per-role code prompt (where available)
    supervisor/review_prompt.md                # supervisor review prompt
    supervisor/code_management_prompt.md       # supervisor code management prompt
    supervisor/report_commissioning_prompt.md  # report commissioning prompt
  runtime.hcl                                  # org-level runtime config override (optional)
  Dockerfile                                   # org-level Dockerfile override (optional)
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
  runtime.hcl                                # project-level runtime config override (optional)
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
  state.sqlite                               # call database (cost/token tracking)
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

## Future
* better context and memory
  * reduce prompt size
    * by moving more of the instructions to the tooling around
* maintain a current view of a project: overview.md and update it based on commit
  * time generated
  * last commit
