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

### Optional: Docker on MacOS

Agents running in Docker need an API key forwarded as an environment variable.
Set the key for whichever agent you use:

```bash
# Claude (one of these)
ateam secret CLAUDE_CODE_OAUTH_TOKEN    # recommended (uses your subscription)
ateam secret ANTHROPIC_API_KEY          # use API directly (pay as you go)

# Codex
ateam secret OPENAI_API_KEY
```

### Upgrade

```bash
git pull --rebase && make build
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

Many built-in roles covering security, testing, documentation, dependencies, refactoring, and more. See [ROLES.md](ROLES.md) for full descriptions.

**Enabled by default** (8 roles):

| Role | Description |
|------|-------------|
| `database_schema` | Analyzes schema definitions, migrations, indexes, constraints, and naming conventions. |
| `dependencies` | Assesses dependency health: outdated packages, unused deps, duplicates, and CVE vulnerabilities. |
| `docs_external` | Reviews user-facing documentation: README quality, install instructions, API docs, and accuracy. |
| `docs_internal` | Assesses developer-facing docs: architecture guides, onboarding, inline comments, and config docs. |
| `project_characteristics` | Produces a structured project profile: size, complexity, tech stack, test coverage, and activity. |
| `refactor_small` | Concrete code improvements: naming, duplication, error handling, dead code, and conventions. |
| `security` | Security vulnerability analysis: injection, auth flaws, hardcoded secrets, input validation, and CVEs. |
| `testing_basic` | Ensures a minimal set of high-value regression tests covering critical paths. |

Enable/disable roles in `.ateam/config.toml` or let `ateam auto-setup` configure them based on your project.

### Creating Custom Roles

Create `roles/YOUR_NEW_ROLE_NAME/report_prompt.md` (in `.ateam/` or `.ateamorg/`), you can then run a report with `ateam report --roles YOUR_NEW_ROLE_NAME`. If you want to have it run by default enable it in `config.toml`.

Ideas:
- GDPR/PII reviewer
- Cloud deployment safety
- Observability enhancer
- Framework-specific best practices
- Language expert
- Performance regression detector
- Black-box testing agent that reads feature specs and generates (potentially) failing tests, without permission to modify the source code
- Pure maintainer: only track CVE, OS and major stack component compatibility without changing anything in the code except to support dependency upgrades

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
| `ateam parallel` | Run multiple agents in parallel, each with its own prompt |
| `ateam secret` | Manage API keys (keychain or file) |
| `ateam agent-config` | [experimental] Audit and configure agent authentication |
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

## Isolation

ATeam runs unattended agents that must operate safely without constant permission approval. The field is evolving — ATeam supports multiple approaches and will adapt as best practices emerge.

**Why isolation matters:**
- **Filesystem**: prevent accidental or malicious writes outside the project, protect access to sensitive files, avoid time wasting configuration breakages
- **Network**: prevent data exfiltration (especially combined with filesystem access), prevent remote control

**The tradeoff**: stricter restrictions increase safety but can break tools that rely on directories outside the project, Unix sockets (Docker), pipes (tsx), nested sandboxes (Playwright on macOS), or shared `/tmp` directories.

### Approaches

| Approach | How it works | Best for |
|----------|-------------|----------|
| **Sandbox** (default) | OS-level syscall restrictions (Seatbelt/bubblewrap) per command | Most projects — fast, no setup |
| **Docker** | Isolated Linux container with controlled mounts and network | Projects needing build/test isolation, multi-tier stacks |
| **Devcontainer** | Uses project's `.devcontainer/` environment | Projects already using devcontainers |
| **None** | No isolation (agent runs directly on host) | Debugging, or when already inside a container |

By default ATeam uses the agent's built-in sandbox. Use `--profile docker` for container isolation. See `defaults/runtime.hcl` for all profiles.

### Sandbox

No config needed — works out of the box. ATeam's default sandbox restricts filesystem access to fewer directories than the agent's default and limits network to package registries and API endpoints.

To customize, edit `.ateam/config.toml`:

```toml
[sandbox-extra]
allow_write = ["/tmp/my-tool-output"]
allow_read = ["/opt/my-sdk"]
allow_domains = ["my-internal-registry.dev"]
unsandboxed_commands = ["playwright"]    # commands that can't run inside a sandbox
```

**Known limitations** (will change as agents evolve):
- Claude Code doesn't yet support Unix domain sockets or named pipes — Docker, playwright-cli and tsx must run unsandboxed
- Sandboxes can't be nested (e.g., Playwright CLI inside a sandbox)
- All files are readable by default; sensitive paths must be explicitly excluded

### Docker

Two ways to use Docker with ATeam:

1. **ATeam orchestrates Docker** (`--profile docker`): ATeam runs on the host and launches agents inside Docker containers. Best when you want isolated build/test environments with per-project customization.

2. **ATeam inside Docker**: Add ATeam and agents to your own Docker image. Agents run without any sandbox since the container is the isolation boundary. Simplest setup — no special ATeam config needed. Best for complex docker setup like multi-tier systems.

Docker containers need API keys forwarded as environment variables. Store them with `ateam secret` (see [Quick Start](#optional-docker-on-macos)).

For detailed Docker setup instructions, container customization, and technical details, see [SANDBOX_DOCKER.md](SANDBOX_DOCKER.md).

### Agent Configuration

In sandbox mode, agents use your default config (e.g. `~/.claude` for Claude Code) — permissions, hooks, and settings all apply. In Docker mode, agents run with `--dangerously-skip-permissions` since the container provides isolation.

To use a project-specific config directory instead of your default, use the `claude-isolated` agent or set `config_dir` in a custom agent definition in `runtime.hcl`.

### Customizing Runtime

- **`config.toml`**: simple customization — sandbox paths, container extras, unsandboxed commands, profiles
- **`runtime.hcl`**: full control — agent definitions, container types, profiles, pricing

See [REFERENCE.md](REFERENCE.md) for complete configuration documentation.


## FAQ

### How to troubleshoot?

* `ateam env` — paths, roles, and available reports
* `ateam ps` — current and past agent runs
* `ateam tail` — live-stream agent logs
* `ateam inspect` — full command args and logs (use `--auto-debug` for self-diagnosis)
* `ateam cat` — pretty-print agent output (.jsonl)

Use `--help` on any command for details. See [REFERENCE.md](REFERENCE.md) for more.

You can also resume sessions from role agents, for example you can use 'claude --resume' and select a specific session to dive more into the findings or why a command the agent ran failed (but start with `ateam ps`, `ateam inspect`, `ateam cat` first).

### How are agents executed by default ?

Both Claude and Codex use their built-in sandbox mode. For Claude this means OS-level restrictions (Seatbelt/bubblewrap) limiting filesystem and network access.

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
