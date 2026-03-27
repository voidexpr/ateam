# ATeam — AI Role Team for Code Analysis

ATeam is designed for developers who want to focus on feature work and key architectural design aspects, while delegating more of the engineering to agents. The goal is to produce a healthy codebase with minimal human effort.

ATeam is a CLI that points a crew of role-specific coding agents at your codebase. Each agent audits code across selected dimensions like code refactoring, testing, security, dependencies, documentation, etc. Then a supervisor prioritizes the findings and runs coding agents to implement fixes. It works unattended, out of the box, for any tech stack. It is solely focused on project quality and doesn't make any feature change.

Think of it as a team of expert colleagues for software quality: they audit while you sleep, commit small focused fixes, and the next run builds on the last.

## Why ATeam

Coding agents prioritize feature completion over software quality which is a good short-term tradeoff that degrades over time. Tests fall behind, security issues accumulate, code becomes spaghetti, docs go stale, dependencies rot, ...

ATeam addresses this by running quality-focused agents unattended, no interactive prompting needed, no functional changes. Just steady, incremental quality improvement that looks like the code was engineered well in the first place.

Core principles:
- **No feature work**: focus on quality, don't change behavior
- **Unattended**: your own coding agent works without approval or interaction
- **Safe**: sandboxing and container isolation
- **Pragmatic**: ateam agents are prompted to adapt to the project size and maturity, audits try to automate tools (linter, test automation, security vulnerability tools, ...) rather than constantly relying on agents
- **Simple**: reuses existing coding agents, minimal orchestration
- **Auditable**: every artifact is a readable markdown file
- **Stateful**: old reports or reviews are read before generating a new one so no context is lost, only one file per role so there is no bloat over time
- **Get out of your way**: ATeam is not a generic workflow system, it is a focused report + review + code automation layer designed to preserve your attention for high-value work

## Quick Start

```bash
git clone https://github.com/voidexpr/ateam.git
cd ateam && ./install.sh
```

The install script checks for Go (installs it if missing), builds the binary, and symlinks it into `~/.local/bin/`.

```bash
# 0. Use your own workspace, a git worktree or a separate workspace for ateam
cd /path/to/your/project

# 1. Initialize, it will create .ateam/ directory in your folder
ateam init

# 2. Auto-configure roles for your project (optional)
ateam auto-setup

# 3. Run
ateam report                         # run all enabled role analyses
ateam review                         # supervisor prioritizes findings
ateam code                           # execute top-priority fixes

```

Once familiar with ateam just run the full pipeline: `ateam all`.

You can see all artifacts under `.ateam/` or via an experimental web UI `ateam serve`.

Other very useful commands:
```bash
# See current and past agent runs
ateam ps

# See logs of running agents
ateam tail
```

### Prerequisites

- **Go 1.25+** — installed automatically by `install.sh`
- **[Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code)** — install and authenticate before running agents
- **Docker** (optional) — enables isolated execution via `--profile docker`

### Manual Install

```bash
go version          # ensure Go 1.25+
git clone https://github.com/voidexpr/ateam.git
cd ateam && make build
sudo ln -s "$(pwd)/ateam" /usr/local/bin/ateam
```

## How It Works

### The Pipeline

```
ateam report  →  ateam review  →  ateam code
   │                  │                │
   ▼                  ▼                ▼
 Role agents       Supervisor       Supervisor
 audit code        prioritizes      delegates
 (parallel)        findings         coding tasks
```

**Report**: Role-specific agents analyze your code and produce markdown reports. Each role focuses on one dimension (security, testing, etc.). Runs in parallel. A role is basically a markdown prompt, easy to modify or create new ones.

**Review**: The supervisor reads all reports, applies judgment, and produces a prioritized list of coding tasks. You can edit the review before proceeding with this step or just add some extra instructions on the CLI with `--extra-prompt SOME TEXT`.

**Code**: The supervisor executes the top-priority tasks by delegating to coding agents, then records what was completed.

Each run archives its artifacts. The next cycle's reports incorporate previous findings, so quality improves incrementally with a memory of what has been done so far.

### Workflow Examples

Daily (quick pass):
```bash
ateam report --roles refactor_small,docs_external,testing_basic && ateam review && ateam code
```

Weekly (thorough):
```bash
ateam report --roles security,dependencies,testing_full && ateam review && ateam code
```

Step by step (with review):
```bash
ateam report && ateam review --print    # inspect findings
# optionally edit .ateam/supervisor/review.md
ateam code                              # execute approved tasks
```

### Git Integration

Several approaches work, it's up to you to select the setup you prefer, `ateam` just runs where you want:
- **Simplest**: run ateam in your work area, review its commits
- **Worktree**: run ateam in a separate git worktree
- **Branch**: create an `ateam_work` branch, cherry-pick changes

### Steering Ateam

Add `report_extra_prompt.md` or `review_extra_prompt.md` to document:
- Rejected approaches (so they aren't retried)
- Project-specific guidelines applied across roles

## Roles

**17 built-in roles**: `automation`, `basic_project_structure`, `critic_engineering`, `critic_project`, `database_config`, `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `production_ready`, `project_characteristics`, `refactor_architecture`, `refactor_small`, `security`, `shortcut_taker`, `testing_basic`, `testing_full`.

Enable/disable roles in `.ateam/config.toml` or let `ateam auto-setup` configure them based on your project.

### Creating Custom Roles

Create `roles/NAME/report_prompt.md` (in `.ateam/` or `.ateamorg/`) and enable it in `config.toml`. Ideas:
- GDPR/PII reviewer
- Cloud deployment safety
- Observability enhancer
- Framework-specific best practices
- Language expert
- Performance regression detector
- Black-box testing agent that reads feature specs and generates (potentially) failing tests, without permission to modify the source code

There is a very long list of potentially very useful roles to add.

## Key Concepts

- **3-level prompt fallback**: project → org → embedded defaults. Customize at any level.
- **Multi-project support**: share org-wide defaults across projects via `.ateamorg/` (by default created in `$HOME`)
- **Runtime profiles**: switch agent/container combos with `--profile docker` or `--profile cheap`
- **Cost tracking**: `ateam cost` for aggregated reports, `ateam ps` for run history
- **Secret management**: `ateam secret` stores API keys in OS keychain or `.env` files

An ateam project is a `.ateam` folder in your code base, a parent directory ($HOME by default) contains `.ateamorg`.
* **Project**:
    * configuration
        * `config.toml` configured roles and general persisted settings
        * optional: overloaded or extended prompts
        * optional: extended coding agent or container config (`runtime.hcl`, `Dockerfile`)
    * produced artifacts
        * reports (and their history)
        * last review (and their history)
        * coding tasks and their execution report
    * runtime logs
        * state.sqlite: track running tasks and statistics about them for live monitoring and cost reporting
        * log files from agent execution, exact prompt used
* **Organization**:
    * optional: overload runtime.hcl or prompts to reuse between projects
    * defaults for all roles and all config

## Commands

| Command | Description |
|---------|-------------|
| `ateam init` | Initialize a project (`.ateam/` directory) |
| `ateam auto-setup` | Auto-configure roles and create project overview |
| `ateam report` | Run role analyses |
| `ateam review` | Supervisor reviews and prioritizes findings |
| `ateam code` | Execute prioritized coding tasks |
| `ateam all` | Full pipeline: report → review → code |
| `ateam run` | Run an agent with a custom prompt |
| `ateam secret` | Manage API keys (keychain or file) |
| `ateam env` | Show environment and configuration status |
| `ateam serve` | Web UI for browsing reports and sessions |
| `ateam ps` | Recent run history |
| `ateam cost` | Aggregated cost and token usage |
| `ateam prompt` | Debug prompt assembly |
| `ateam cat` | Pretty-print stream logs |
| `ateam tail` | Live-stream agent output |
| `ateam roles` | List available roles |
| `ateam projects` | List projects in the organization |
| `ateam update` | Update on-disk defaults to match binary |

See [REFERENCE.md](REFERENCE.md) for full flag documentation, directory layout, prompt configuration, runtime configuration, and troubleshooting.

## FAQ

### How are agents executed by default ?

For both Claude and Codex the built-in sandbox mode is used. For Claure it is enforced by OS level primitives and constraint claude to only specific folders and a few network domains.

It can easily be changed in `.ateam/config.toml` to select specific profiles and how to run agents is fully customizable in `.ateamorg/runtime.hcl`.

### How to look at the exact prompts used by ateam

Use the `ateam prompt --role ROLENAME --action report` to show the exact prompt used taking into account overloaded and extra prompts added.

### Why not just /simplify in claude ?

`/simplify` only looks like at code refactoring and is great to use, ateam can look at many other aspects: testing, documentation, etc ...

It actually fits very well as a first step before a full ateam cycle:

```bash
ateam run "/simplify the recent commits" && ateam all
```

### What if I only want to do some of the code changes or only run some of the reports ?

* you can easily select which reports to run with `ateam report --roles ROLE1,ROLE2`
* you can instruct the supervisor: `ateam review --extra-prompt "I only want tasks from refactoring_small and testing_basic"`

### What if I want to use ateam in a slightly different workflow than report-review-code ?

The `ateam run` command is a wrapper around coding to run one-shot, unattended prompts. You can use it to build your own automated scripts. It can also be run outside of an ateam project (but requires an ateam organization which is created by default in `$HOME`). You still benefit from ateam observability features:
* `ateam ps` to see current and past execution
* `ateam tail` to see logs in real time
* `ateam cost` to get a token cost report

You can then use `ateam run` in your own scripts and build your own workflows reusing agent/container management without the ateam prompt/artifact part. It can be ran without an ateam project but does require an ateam org (which is created in $HOME by default).

For example: `ateam run "/simplify my last few commits" && git commit . -m "round of simplify" && ateam run "Identify and code at most 5 code refactoring opportunities focused on performance and security. Make sure to commit each separately as soon as they are completed, do run tests between each and fix any issue introduced" --profile docker` and then go get than nice walk outside or valuable family time while your agent is at work. You shouldn't come back to see that it got stuck asking for a bash command approval at the first step.

### What size of project is it for ?

ATeam should be adaptable for projects of many size by running on the entire repo for small to medium projects and have separate ateam projects for various component of bigger projects using a mono repo.

## Future

- Reduce input token usage
- Improve default role prompts for accuracy and token usage
- Move more orchestration away from prompts into ateam itself
- Keep `overview.md` up to date based on commits
- More agent types (gemini, cursor, ...) and containers (MacOS native container), improve sandbox configuration
- Stricter testing policy and automation (opt-in)
- Built-in scheduling
- Adaptive report commissioning based on recent code changes (can reduce token usage)
- look at adding an evaluation cycle (after report/review and code) to potentially reject some code changes
- maybe: explicit task system instead of relying on supervisor to orchestrate coding sessions (could save a lot of tokens but coding agents already know how to manage multiple tasks)

## Development

See [DEV.md](DEV.md) for development setup, testing, and architecture details.
