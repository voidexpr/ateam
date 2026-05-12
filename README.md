# ATeam — AI Role Team for Code Analysis

ATeam is designed for developers who want to focus on feature work and key architectural design aspects, while delegating more of the engineering to agents. The goal is to produce a healthy codebase with minimal human effort.

ATeam is a CLI that runs role-specific coding agents against your codebase. Each agent audits code across selected dimensions like code refactoring, testing, security, dependencies, documentation, etc. Then a supervisor prioritizes the findings and runs coding agents to implement fixes. It works unattended, out of the box, for any tech stack. It is solely focused on project quality and doesn't make any feature change.

Think of it as a team of expert colleagues for software quality: they audit while you sleep, commit small focused fixes, and the next run builds on the last.

If you want to work on complex tasks then keep using interactive agents, if you want to run pre-built prompts to perform a task then `ateam` helps manage them.

At its core ateam is a CLI to run one-shot unattended agents with saved prompts. It layers a small workflow of parallel reports, supervisor review and supervised coding of selected tasks. But can also be used in shell commands for sequences of steps (single agent or parallel agents). The focus is on software engineering quality improvement tasks and not feature development. Feature development (or also some software engineering quality tasks) benefit from interactive agents. Ateam solves the problem of having background agent improve the code base quality behind the scene to reduce the need to explicitly do it.

See [APPROACH.md](APPROACH.md) for the rationale and design principles behind ATeam.

## Features

* **use existing coding agents like claude code or codex**: leverages subscriptions instead of much more expensive APis, benefit from the expertise of llm providers. Ateam focuses on automating them
* **flexible workflow**: you get to decide if ateam works on worktree, separate workspace, separate server or with containers (docker, devcontainer, ...)
* **flexible isolation**: out of the box ateam uses your coding agents as-is for ease of configuration. But it also supports the following workflows:
    * run in a sandbox on your base host: protects your files
    * use a separate config for your coding agent (`CLAUDE_CONFIG_DIR`)
    * run inside docker (built-in secret management for oauth or just use an already authenticated agent in the container)
    * run outside of docker but docker exec only the agents in docker
* **just a CLI**: can run the workflows built-in ateam (report, review, code, verify) or ad-hoc unattended agent execs (`run` for a single agent exec, `parallel` for multiple simultaneous agents)
* **convenient tooling**: `ps` to see current/past agent runs, `inspect` for troubleshooting
* **cost transaprency**: all agent execution track token usage and estimated cost (less relevant for subscription). Tokens are the new software engineering currency and help gauge if an error is worthwhile

## Why ATeam

Coding agents prioritize feature completion over software quality which is a good short-term tradeoff that degrades over time. Tests fall behind, security issues accumulate, code becomes spaghetti, docs go stale, dependencies rot, ...

ATeam addresses this by running quality-focused agents unattended, no interactive prompting needed, no functional changes. Just steady, incremental quality improvement that looks like the code was engineered well in the first place.

Core principles:

* **No feature work**: focus on quality, don't change behavior
* **Unattended**: your own coding agent works without approval or interaction
* **Safe**: sandboxing and container isolation
* **Pragmatic**: ateam agents are prompted to adapt to the project size and maturity, audits try to automate tools (linter, test automation, security vulnerability tools, ...) rather than constantly relying on agents
* **Simple**: reuses existing coding agents, minimal orchestration
* **Auditable**: every artifact is a readable markdown file
* **Stateful**: old reports or reviews are read before generating a new one so no context is lost, only one file per role so there is no bloat over time
* **Get out of your way**: ATeam is not a generic workflow system, it is a focused report + review + code + verify automation layer designed to preserve your attention for high-value work

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
ateam code                           # execute top-priority fixes (then verify)

```

Once familiar with ateam just run the full pipeline: `ateam all`.
`ateam code` and `ateam all` chain `ateam verify` automatically; pass
`--no-verify` to stop after the code phase.

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

### Optional: Docker

Docker agents need API credentials. Store them once with `ateam secret`:

```bash
ateam secret CLAUDE_CODE_OAUTH_TOKEN    # recommended (uses your subscription)
ateam secret ANTHROPIC_API_KEY          # or use API directly (pay as you go)
```

For interactive Claude in containers, mount the shared config directory and use `ateam claude`:

```bash
docker run -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" ...
# Inside the container:
ateam claude --config-dir ~/shared_claude
```

See [ISOLATION.md](ISOLATION.md) for Docker setup details including shared Linux agent config.

### Upgrade

```bash
git pull --rebase && make build
```

## How It Works

### The Pipeline

```
ateam report  →  ateam review  →  ateam code  →  ateam verify
   │                  │                │                │
   ▼                  ▼                ▼                ▼
 Role agents       Supervisor       Supervisor       Supervisor
 audit code        prioritizes      delegates        inspects commits
 (parallel)        findings         coding tasks     and runs tests
```

**Report**: Role-specific agents analyze your code and produce markdown reports. Each role focuses on one dimension (security, testing, etc.). Runs in parallel. A role is basically a markdown prompt, easy to modify or create new ones.

**Review**: The supervisor reads all reports, applies judgment, and produces a prioritized list of coding tasks. You can edit the review before proceeding with this step or just add some extra instructions on the CLI with `--extra-prompt SOME TEXT`.

**Code**: The supervisor executes the top-priority tasks by delegating to coding agents, then records what was completed.

**Verify**: The supervisor inspects the commits made during the code phase, looks for logical bugs, broken or missing tests, and risky changes, then runs the project's test suite and records findings. `ateam code` and `ateam all` chain this automatically; pass `--no-verify` to skip it.

Each run archives its artifacts. The next cycle's reports incorporate previous findings, so quality improves incrementally with a memory of what has been done so far.

### Workflow Examples

Daily (quick pass):
```bash
ateam all --roles refactor_small,docs_external,testing_basic
```

Weekly (thorough):
```bash
ateam all --roles security,dependencies,testing_full
```

Step by step (with review):
```bash
ateam report && ateam review --print    # inspect findings
# optionally edit .ateam/supervisor/review.md
ateam code                              # execute approved tasks (then verify)
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
- **Secret management**: `ateam secret` stores API keys in OS keychain or `.env` files. For a given key the store beats the environment; when an agent accepts alternatives (e.g. `CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY`), OAUTH wins any same-level tie. Competing credentials are stripped from agent processes

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
        * state.sqlite: track running agent execs and statistics about them for live monitoring and cost reporting
        * log files from agent execution, exact prompt used
* **Organization**:
    * optional: overload runtime.hcl or prompts to reuse between projects
    * defaults for all roles and all config

## Commands

| Command | Description |
|---------|-------------|
| `ateam install` | Create a `.ateamorg/` directory with defaults |
| `ateam init` | Initialize a project (`.ateam/` directory) |
| `ateam auto-setup` | Auto-configure roles for your project |
| `ateam report` | Run role analyses |
| `ateam review` | Supervisor reviews and prioritizes findings |
| `ateam code` | Execute prioritized coding tasks (chains `ateam verify`; pass `--no-verify` to skip) |
| `ateam all` | Full pipeline: report → review → code → verify |
| `ateam verify` | Supervisor verifies recent code changes from `ateam code` |
| `ateam eval` | Compare two role prompt variants side-by-side with LLM scoring — see [EVAL.md](EVAL.md) |
| `ateam exec` | Run an agent with a custom prompt |
| `ateam parallel` | Run multiple agents in parallel, each with its own prompt |
| `ateam secret` | Manage API keys (keychain or file) |
| `ateam claude` | Run interactive claude in a container with shared config |
| `ateam agent-config` | [experimental] Audit agent auth, copy config between host and containers |
| `ateam container-cp` | Copy ateam binary into a running container |
| `ateam env` | Show environment and configuration status |
| `ateam serve` | Web UI for browsing reports and sessions |
| `ateam export` | Export reports as a self-contained HTML file |
| `ateam ps` | Recent run history |
| `ateam inspect` | Show details and logs for agent runs |
| `ateam resume` | Resume a previous claude agent run as an interactive session |
| `ateam cost` | Aggregated cost and token usage |
| `ateam prompt` | Debug prompt assembly |
| `ateam cat` | Pretty-print stream logs |
| `ateam tail` | Live-stream agent output |
| `ateam roles` | List available roles |
| `ateam projects` | List projects in the organization |
| `ateam project-rename` | Re-register a project after a directory move, or rename its state directory |
| `ateam update` | Update on-disk defaults to match binary |
| `ateam version` | Print version, build, and system information |

See [COMMANDS.md](COMMANDS.md) for all `ateam` commands and flags, and [CONFIG.md](CONFIG.md) for directory layout, prompt configuration, and runtime configuration.

## Isolation

ATeam runs unattended agents that must operate safely without constant permission approval. The field is evolving — ATeam supports multiple approaches and will adapt as best practices emerge.

**Why isolation matters:**
- **Filesystem**: prevent accidental or malicious writes outside the project, protect access to sensitive files, avoid time wasting configuration breakages
- **Network**: prevent data exfiltration (especially combined with filesystem access), prevent remote control

**The tradeoff**: stricter restrictions increase safety but can break tools that rely on directories outside the project, Unix sockets (Docker), pipes (tsx), nested sandboxes (Playwright on macOS), or shared `/tmp` directories.

### Execution modes

```
┌─ Host ──────────────────────────────┐   ┌─ Host ──────────────────────────────┐
│ ┌─ ateam ─────────────────────────┐ │   │ ┌─ ateam ─────────────────────────┐ │
│ │ ┌─ agent ─────────────────────┐ │ │   │ │ ┌─ container ─────────────────┐ │ │
│ │ │ ┌─ sandbox ───────────────┐ │ │ │   │ │ │ ┌─ agent ─────────────────┐ │ │ │
│ │ │ │    tools / commands     │ │ │ │   │ │ │ │    tools / commands     │ │ │ │
│ │ │ └─────────────────────────┘ │ │ │   │ │ │ └─────────────────────────┘ │ │ │
│ │ └─────────────────────────────┘ │ │   │ │ └─────────────────────────────┘ │ │
│ └─────────────────────────────────┘ │   │ └─────────────────────────────────┘ │
└─────────────────────────────────────┘   └─────────────────────────────────────┘
① Built-in sandbox — default profile      ② Docker one-shot — --profile docker

┌─ Host ──────────────────────────────┐   ┌─ Host ──────────────────────────────┐
│ ┌─ ateam ─────────────────────────┐ │   │ ┌─ container ─────────────────────┐ │
│ │ ┌─ running container ─────────┐ │ │   │ │ ┌─ ateam ─────────────────────┐ │ │
│ │ │ ┌─ agent ─────────────────┐ │ │ │   │ │ │ ┌─ agent ─────────────────┐ │ │ │
│ │ │ │    tools / commands     │ │ │ │   │ │ │ │    tools / commands     │ │ │ │
│ │ │ └─────────────────────────┘ │ │ │   │ │ │ └─────────────────────────┘ │ │ │
│ │ └─────────────────────────────┘ │ │   │ │ └─────────────────────────────┘ │ │
│ └─────────────────────────────────┘ │   │ └─────────────────────────────────┘ │
└─────────────────────────────────────┘   └─────────────────────────────────────┘
③ Docker exec — --profile docker-exec     ④ ATeam inside Docker — container-native
```

### Approaches

| Approach | How it works | Best for |
|----------|-------------|----------|
| **Built-in sandbox** (default) | OS-level syscall restrictions (Seatbelt/bubblewrap) per command | Most projects — fast, no setup |
| **Docker one-shot** | Fresh Linux container built and run per command | Strong isolation; need build/test tooling |
| **Docker exec** | Exec into an existing user-managed container (docker-compose, devcontainer, …) | You already run a long-lived dev container |
| **ATeam inside Docker** | Run ateam itself from inside a container; agents inherit container isolation | Docker-native projects |
| **None** | No isolation (agent runs directly on host) | Debugging only |

By default ATeam uses the agent's built-in sandbox. Use `--profile docker` for one-shot container isolation or `--profile docker-exec` to exec into an existing container. See `defaults/runtime.hcl` for all profiles.

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

### Separate configuration for coding agents

By default ateam uses your local agent configuration (for example `~/.claude` for Claude) that may include some settings that could be helpful (skills, plugins, mcp servers) or not helpful (custom logging of tools, custom notifications). Eventually it is recommended to use a different configuration directory (for example `~/.ateamorg/claude`) and change `runtime.hcl` to use it by default.

### Docker
#### One-shot (docker run)

Use `--profile docker` for one-shot container isolation, or run ateam inside your own Docker setup. Agents auto-detect containers and skip sandbox/permissions — no profile switching needed. A default Dockerfile is used so agent is available inside of the container.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.

#### docker exec

Use `--profile docker-exec` to run agents within existing docker containers. Ateam makes sure that agents skip sandbox/permissions — no profile switching needed.

A coding agent has be available within the container, by default an oauth token is passed so no need to authenticate inside the container. No need to install ateam itself unless you run the supervisor for coding this way. This mode is best used for code agent runs so they have access to a proper test environment.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.

#### Run ateam inside a container

No custom profile needed, ateam detects it runs within a container and runs agents without sandbox or permission approval. But this does require to install team inside the container and (optionally) mount the ateamorg directory to have access to defaults. Also the coding agent inside the container should be fully authenticated and optionally have an oauth token.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.


### Customizing Runtime

- **`config.toml`**: simple customization — sandbox paths, container extras, unsandboxed commands, profiles
- **`runtime.hcl`**: full control — agent definitions, container types, profiles, pricing

See [CONFIG.md](CONFIG.md) for complete configuration documentation.


## FAQ

See [FAQ.md](FAQ.md) for frequently asked questions.

## Future

- 0.9.0 Refactor roles and do some eval to use less tokens and improve accuracy
- 1.0.0 Small improvements over what is already there
- 2.0.0 Add an internal task system and move coding to algorithmic instead of relying on on agent prompt to consume less tokens and make the system more deterministic

In General other areas of interest:
- Reduce input token usage
    - Adaptive report commissioning based on recent code changes (can reduce token usage)
    - tune roles
    - use an internal findings/task system instead of read/write files
- Collection of roles for various phases of project life cycle: feedback on design, analyze for production (observability, etc ...), stack specific prompts, etc ...
- Stricter testing policy and automation (opt-in)
- More agent types (gemini, cursor, ...) and containers (MacOS native container, alternative sandboxes)
- more flexible and dynamic prompts: execute commands (sandboxed) during prompt assembly, include prompt fragments, CLI args expansion in prompt, generalized mechanism to inject pre/post prompt instructions
- more flexible workflow: execute commands before/after agents to verify/prepare work/env, jump steps, resumable steps leveraging the future internal persistent task system
- Improve reporting and better integrate ateam into various project workflow: teams where humans don't code, where they don't, solo project vs. much bigger teams, ...
- maybe: Built-in scheduling

## Development

See [DEV.md](DEV.md) for development setup, testing, and architecture details.

## More docs

- [APPROACH.md](APPROACH.md) — rationale and design principles
- [COMMANDS.md](COMMANDS.md) — full `ateam` command reference
- [CONFIG.md](CONFIG.md) — directory layout, `config.toml`, `runtime.hcl`
- [ISOLATION.md](ISOLATION.md) — sandbox and container guide (modes, secrets, auth)
- [ROLES.md](ROLES.md) — built-in roles
- [EVAL.md](EVAL.md) — evaluating prompt and role changes
- [FAQ.md](FAQ.md) — frequently asked questions
