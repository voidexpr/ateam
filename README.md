# ATeam — AI Role Team for Code Analysis

ATeam is designed for developers who want to focus on feature work and key architectural design aspects, while delegating more of the quality software engineering to agents. The goal is to produce a healthy codebase with minimal human effort.

ATeam is a CLI that runs role-specific coding agents against your codebase. Each agent audits code across selected dimensions like code refactoring, testing, security, dependencies, documentation, etc. Then a supervisor prioritizes the findings and runs coding agents to implement fixes. It works unattended, out of the box, for any tech stack. It is solely focused on project quality and doesn't make any feature change.

Think of it as a team of expert colleagues for software quality: they audit while you sleep, commit small focused fixes, and the next run builds on the last.

If you want to work on complex tasks then keep using interactive agents, if you want to run pre-built prompts to perform a task then `ateam` helps manage them.

At its core ateam is a CLI to run one-shot unattended agents with saved prompts. It layers a small workflow of parallel reports, supervisor review and supervised coding of selected tasks. But can also be used in shell commands for sequences of steps (single agent or parallel agents). The focus is on software engineering quality improvement tasks and not feature development. Feature development (or also some software engineering quality tasks) benefit from interactive agents. Ateam solves the problem of having background agent improve the code base quality behind the scene to reduce the need to explicitly do it.

Maybe:
Ateam focus is on:
* flexible agent execution
    * safe isolated execution
    * no request approval interrupting execution, pure unattended agent
    * prompt once and run with any agent, no lock-in
    * tracks cost, logs for easy troubleshooting
* pre-built pipeline for entire code enhancement
    * flexible full execution or step by step audit
    * pre-built prompts along many dimensions
    * web tool to review artifacts produced by ateam
* a simple command line that can be used in more complex workflow
    * want to perform adversarial reviews ? see scripts/ for examples
    * want something simple chaining sequential and parallel agents ? a simple bash script typical suffices
    * want complex prompt assembly and pre/post execution script ? just wrap the ateam CLI and build the framework you need. An example is available in python/ateam.py

The goal is to define asynchronous processes mixing agents and scripts to free up more time to focus where human attention is needed and not for tasks that can be prompted once and require little supervision/decision making once completed.

See [APPROACH.md](APPROACH.md) for the rationale and design principles behind ATeam.

## Key Features

* **use existing coding agents like claude code or codex**: leverages subscriptions instead of much more expensive APIs, benefit from the expertise of llm providers. Ateam focuses on automating them
    * an experimental `codex-tmux` agent additionally drives codex's interactive TUI through `tmux`, so TUI-only slash commands like `/review` can run in unattended pipelines (see [CONFIG.md](CONFIG.md#codex-tmux-experimental))
* **flexible isolation**: out of the box ateam uses your coding agents as-is for ease of configuration. But it also supports the following workflows:
    * run in a sandbox on your base host: protects your files
    * use a separate config for your coding agent (`CLAUDE_CONFIG_DIR`)
    * run inside docker (built-in secret management for oauth or just use an already authenticated agent in the container)
    * run outside of docker but docker exec only the agents in docker
* a set of **roles** covering all core aspects of a project: code quality, testing, documentation, dependencies, security, etc ... out of the box. Adding a new role is just a single Markdown prompt file to add
* **just a CLI**:
    * can run the built-in ateam workflow of parallel report, review, code, verify
    * ad-hoc unattended agent runs (`exec` for a single agent execution, `parallel` for multiple simultaneous agents)
* **convenient tooling**: `ps` to see current/past agent runs, `cat`, `tail`, `inspect` for logs and execution details, `serve` to browse reports and reviews
* **cost transparency**: all agent execution track token usage and estimated cost (less relevant for subscription). Tokens are the new software engineering currency and help gauge if an error is worthwhile

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
* **Complement interactive agents**: interactive agents remain best for difficult, iterative tasks and feature development but ateam can be used for its set of roles and improvement pipeline or for reusable scripts.
* **Get out of your way**: ATeam is not a generic workflow system, it is a focused report + review + code + verify automation layer designed to preserve your attention for high-value work

In any case there is no silver bullet, eventually documentation might need human direction to be better structure, a major code refactoring to better handle feature requirements is needed, etc ... But the goal is to reduce human involvement in day to day software engineering tasks.

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

# 2. Authenticate your coding agent (pick what you use — without this `ateam report` will fail)
claude                                            # interactive Claude Code login, then exit
# or store credentials with ateam:
claude setup-token                                # produces a long-lived OAuth token to paste below
ateam secret CLAUDE_CODE_OAUTH_TOKEN --set        # paste the token from setup-token (reads from stdin)
ateam secret ANTHROPIC_API_KEY --set              # or an API key

# 3. Auto-configure roles for your project (optional)
ateam auto-setup

# 4. Run
ateam report             # run all enabled role analyses
ateam review             # supervisor prioritizes findings
ateam code               # implement top-priority fixes
ateam verify             # audits at the commits from previous phase

# 5. Look (at any step)
ateam serve              # local web server to browse documents, processes, cost
```

Once familiar with ateam just run the full pipeline: `ateam all` or `ateam all && ateam serve`.

You can see all artifacts using web UI `ateam serve` or under `.ateam/`

Other very useful commands:
```bash
# See current and past agent runs
ateam ps

# See logs of running agents
ateam tail

# Auto-debug issues using an agent
ateam inspect EXEC_ID --auto-debug
```

### Prerequisites

- **Go 1.26+** — installed automatically by `install.sh`
- **[Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code)** or **[Codex](https://developers.openai.com/codex/cli)** — install and authenticate before running agents (one or the other or both)
- **Docker** (optional) — enables isolated execution via `--profile docker`

### Manual Install

```bash
go version          # ensure Go 1.26+
git clone https://github.com/voidexpr/ateam.git
cd ateam && make build
sudo ln -s "$(pwd)/ateam" /usr/local/bin/ateam
```

### Upgrade

```bash
git pull --rebase && make build-all
ateam update --diff   # preview which embedded prompts changed
ateam update          # sync on-disk prompts to the new embedded defaults
```

Run `ateam update` after rebuilding: on-disk org/project prompts shadow the binary's embedded defaults, so without it prompt changes shipped in the new version silently don't take effect. Use `ateam update --diff` first to preview the changes.

## How It Works

By default coding agents will be ran in a sandbox providing a good balance of file system protection and ease of use out of the box. Read the [Isolation](#Isolation) section for more options on how to run your coding agents.

### The Pipeline

When running `ateam all` the following steps are executed (they are also available as individual commands):

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

**Verify**: The supervisor inspects the commits made during the code phase, looks for logical bugs, broken or missing tests, and risky changes, then runs the project's test suite and records findings. `ateam code` stops after the code phase — run `ateam verify` explicitly, or use `ateam all` which always runs verify as the final phase.

Each run archives its artifacts. The next cycle's reports incorporate previous findings, so quality improves incrementally with a memory of what has been done so far.

### Workflow Examples

Daily (quick pass, focused on recent changes):
```bash
ateam all --roles code.recent,test.recent
```

Weekly (thorough):
```bash
ateam all --roles code.bugs,test.gaps,project.security,project.dependencies,design.architecture
```

Step by step (with review):
```bash
ateam report && ateam review --print    # inspect findings
# optionally edit .ateam/shared/review.md
ateam code && ateam verify && ateam serve # fix, verify and serve all artifacts produced
```

### Git

The current version of ateam doesn't perform any git actions except `git commit` during the `code` or `verify` phases.

So the git workflow is up to you:
- **Simplest**: run ateam in your or a dedicated work area, review its commits, push.
- **Worktree**: run ateam in a separate git worktree, review, merge/cherry-pick
- **Branch**: same as worktree but with a dedicated work area

### Steering Ateam

#### 1. Providing directions
* for ad-hoc steering, every prompt-taking command accepts three text-or-`@file` flags:
    * `--extra-prompt TEXT` — appended after the assembled body, inside the prompt
    * `--pre-prompt TEXT` — wrapped at the very front, outermost
    * `--post-prompt TEXT` — wrapped at the very end, outermost
    * example: `ateam all --extra-prompt "focus on the changes related to the authentication model"`
* for persistent steering (like reject a type of findings ateam proposes) write them as composable fragments at the appropriate level:
    * project-level role override: `.ateam/prompts/report/NAME.post.extra.md` (composes with the embedded role)
    * project-level supervisor review override: `.ateam/prompts/review.post.extra.md`
    * org-level (shared across projects): same paths under `.ateamorg/prompts/`

see more options in [CONFIG.md](CONFIG.md)

#### 2. Specify selected roles

It doesn't make sense to run the same roles all the time, for example:
* during lunch run code review and test roles only
* after a day of work do the same but add security update, documentation roles
* once or twice a week also review dependencies and more advanced testing

## Isolation

ATeam runs unattended agents that must operate safely without constant permission approval requests. The field is evolving, ATeam supports multiple approaches and will adapt as best practices emerge.

**Why isolation matters:**
- **Filesystem**: prevent accidental or malicious writes outside the project, protect access to sensitive files, avoid time-wasting configuration breakages
- **Network**: prevent data exfiltration (especially combined with filesystem access), prevent remote control

**The tradeoff**: stricter restrictions increase safety but can break tools that rely on directories outside the project, Unix sockets (Docker), pipes (tsx), nested sandboxes (Playwright on macOS), or shared `/tmp` directories. Also more isolation environments like Docker require more configuration, there are also extra steps to configure coding agents within containers.

The exact isolation is configuration driven so highly customizable.

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

| Approach | How it works | Best for |
|----------|-------------|----------|
| **Built-in sandbox** (default) | OS-level syscall restrictions (Seatbelt/bubblewrap) per command | Most projects — fast, no setup |
| **Built-in sandbox** and **separate agent configuration** | Same as above but don't share the same configuration as interactive agents (different hooks, ...) | Useful when the default agent configuration is highly customized with notifications |
| **Docker one-shot** | Fresh Linux container built and run per command | Strong isolation; need build/test tooling |
| **Docker exec** | Exec into an existing user-managed container (docker-compose, devcontainer, …) | You already run a long-lived dev container |
| **ATeam inside Docker** | Run ateam itself from inside a container; agents inherit container isolation and runs without any restriction | Docker-native projects |
| **None** | No isolation (agent runs directly on host) | Debugging only |

By default ATeam uses the agent's built-in sandbox. Use `--profile docker` for one-shot container isolation or `--profile docker-exec` to exec into an existing container. See `defaults/runtime.hcl` for all profiles.

## Key Configuration Concepts

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

## Roles

Many built-in roles covering security, testing, documentation, dependencies, refactoring, and more. See [ROLES.md](ROLES.md) for full descriptions.

Roles are organized into collections using a `collection.role` naming convention (e.g. `code.bugs`, `test.recent`). Older single-name roles like `security` or `refactor_small` are kept on disk for backward compatibility but hidden from listings — see [ROLES.md](ROLES.md) for the canonical list.

**Enabled by default** (11 roles):

| Role | Description |
|------|-------------|
| `code.bugs` | Hunts for logic bugs across the codebase — wrong conditions, broken contracts, silent failures, lifecycle and concurrency issues — with strict false-positive filtering. |
| `code.recent` | Reviews recent changes (uncommitted + last few commits) for bugs, regressions, duplication, and structural slips before they harden into debt. |
| `code.structure` | Project-wide structural quality — duplication, anti-patterns, naming, layering, missing/unnecessary abstractions — tagged by scope (local \| module \| architecture). |
| `design.architecture` | System-design review — where logic should live across layers, API/contract design quality, service boundary decisions, and cross-component operational contracts. |
| `docs.external` | Maintains user-facing docs — README, install, usage examples, public API reference. Drives both presence and accuracy with README-size discipline. |
| `docs.internal` | Maintains engineer-facing technical depth — architecture, internal protocols/formats, build/test setup rationale, per-area design principles, and agent-facing instructions. |
| `project.automation` | Project foundations and automation — strict priority order: build/verification first, multi-tier test commands, then lint/format, then hooks, with CI/CD as the lowest priority. |
| `project.dependencies` | Conservative dependency health review — flags abandoned/EOL packages, deprecated APIs in use, exploitable CVEs, and major-version-gap blockers. |
| `project.security` | First-line security review — focuses on confirmed exploitable bugs and data exposure with realistic triggers. |
| `test.gaps` | Project-wide missing-test discovery — untested CLI commands, untested user-visible workflows, 0%-coverage functions on reachable paths, and integration boundaries with no end-to-end test. |
| `test.recent` | Checks that recent changes have appropriate test coverage — new branches exercised, modified contracts re-tested, bug fixes locked in by regression tests. |

Role enablement is opt-in: only roles explicitly listed as `on` in `config.toml` are included when running `ateam report --roles all`. Edit `.ateam/config.toml` to enable additional roles, or let `ateam auto-setup` configure them based on your project. Any role can still be run by name (`ateam report --roles code.bugs,test.quality`) regardless of its default status.

### Creating Custom Roles

Create `prompts/report/YOUR_NEW_ROLE_NAME.prompt.md` (in `.ateam/` for project-scoped or `.ateamorg/` for org-shared). You can then run a report with `ateam report --roles YOUR_NEW_ROLE_NAME`. To have it run by default, enable it in `config.toml`.

Ideas:
- GDPR/PII privacy
- Cloud deployment safety
- Observability
- Framework, language, company infra specific best practices
- UI Accessibility

There is a very long list of potentially very useful roles to add.

## Commands

* Main pipeline: [`all`](COMMANDS.md#ateam-all), [`report`](COMMANDS.md#ateam-report), [`review`](COMMANDS.md#ateam-review), [`code`](COMMANDS.md#ateam-code), [`verify`](COMMANDS.md#ateam-verify)
* Review documents: [`serve`](COMMANDS.md#ateam-serve), [`export`](COMMANDS.md#ateam-export)
* Process management: [`ps`](COMMANDS.md#ateam-ps), [`inspect`](COMMANDS.md#ateam-inspect-id), [`tail`](COMMANDS.md#ateam-tail), [`cat`](COMMANDS.md#ateam-cat), [`resume`](COMMANDS.md#ateam-resume-exec_id)
* Troubleshooting: [`env`](COMMANDS.md#ateam-env), [`prompt`](COMMANDS.md#ateam-prompt), [`roles`](COMMANDS.md#ateam-roles), [`version`](COMMANDS.md#ateam-version)
* Ad-hoc agents: [`exec`](COMMANDS.md#ateam-exec), [`parallel`](COMMANDS.md#ateam-parallel)
* Installation and update: [`init`](COMMANDS.md#ateam-init-path), [`auto-setup`](COMMANDS.md#ateam-auto-setup), [`install`](COMMANDS.md#ateam-install-path), [`update`](COMMANDS.md#ateam-update)

| Command | Description |
|---------|-------------|
| [`ateam init`](COMMANDS.md#ateam-init-path) | Initialize a project (`.ateam/` directory) |
| [`ateam auto-setup`](COMMANDS.md#ateam-auto-setup) | Auto-configure roles for the current project |
| [`ateam report`](COMMANDS.md#ateam-report) | Run role analyses |
| [`ateam review`](COMMANDS.md#ateam-review) | Supervisor reviews and prioritizes findings |
| [`ateam code`](COMMANDS.md#ateam-code) | Execute prioritized coding tasks (run [`ateam verify`](COMMANDS.md#ateam-verify) after, or use `ateam all` for the full pipeline) |
| [`ateam all`](COMMANDS.md#ateam-all) | Full pipeline: report → review → code → verify (verify always runs) |
| [`ateam verify`](COMMANDS.md#ateam-verify) | Supervisor verifies recent code changes from [`ateam code`](COMMANDS.md#ateam-code) |
| [`ateam exec`](COMMANDS.md#ateam-exec) | Run an agent with a custom prompt |
| [`ateam parallel`](COMMANDS.md#ateam-parallel) | Run multiple agents in parallel, each with its own prompt |
| [`ateam env`](COMMANDS.md#ateam-env) | Show environment and configuration status |
| [`ateam project-info`](COMMANDS.md#ateam-project-info-path) | Emit generic, language-agnostic orientation about a git repository |
| [`ateam serve`](COMMANDS.md#ateam-serve) | Web UI for browsing reports and sessions |
| [`ateam export`](COMMANDS.md#ateam-export) | Export reports as a self-contained HTML file |
| [`ateam ps`](COMMANDS.md#ateam-ps) | Recent run history |
| [`ateam inspect`](COMMANDS.md#ateam-inspect-id) | Show details and logs for agent runs |
| [`ateam resume`](COMMANDS.md#ateam-resume-exec_id) | Resume a previous claude, codex, or codex-tmux run as an interactive session |
| [`ateam prompt`](COMMANDS.md#ateam-prompt) | Debug prompt assembly |
| [`ateam cat`](COMMANDS.md#ateam-cat) | Pretty-print stream logs |
| [`ateam tail`](COMMANDS.md#ateam-tail) | Live-stream agent output |
| [`ateam roles`](COMMANDS.md#ateam-roles) | List available roles |
| [`ateam cost`](COMMANDS.md#ateam-cost) | Aggregated token-usage and cost reports across runs |
| [`ateam secret`](COMMANDS.md#ateam-secret) | View, set, or delete agent secrets (keychain or `.env`) |
| [`ateam version`](COMMANDS.md#ateam-version) | Print version, build, and system information |

See [COMMANDS.md](COMMANDS.md) for all `ateam` commands and flags, and [CONFIG.md](CONFIG.md) for directory layout, prompt configuration, and runtime configuration.

## How to best use ateam

See [GUIDE.md](GUIDE.md)

## FAQ

See [FAQ.md](FAQ.md) for frequently asked questions.

## Future

Ateam was born from the frustration of dealing with constant permission approval notices and having to constantly prompt coding agents to refactor code, add tests, audit security, review code when agents themselves are very good at finding issues. This is mostly achieved via the flexible isolation options and running one-shot unattended agents in multiple stages. Coding agents have been surprisingly brittle so stability has been an issue but it has improved to be able to get more stable runs. Cost is definitely going up and maxing the use of subscriptions price subsidy is already gone for Claude Code (June 15th 2026 pricing model change). But it is just a reality that coding a long term project is a lot more than getting a feature to work and cost expectations need to be adjusted.

The vision moving forward is to improve ateam along the following paths:
* reduce token usage
    * tokens are the currency of AI and the metric to optimize for: cheaper, runs faster, want to use the right amount of tokens to get the best possible results to avoid having to redo work or deal with issues that could have been preventing by being a bit more thorough
    * this means introducing a task system to have a granularity of work that is finer grain than an entire file and track review/code status on it, do evals to improve prompts
* improve the quality engineering component
    * allow humans to focus on features and do barely any code review, testing and other similar tasks because they know ateam will keep improving this area automatically
    * this means improving prompts, adding more workflows (see the scripts doing adversarial reviews/testing, lunch time runs vs. daily vs. weekly), maybe ways to hint ateam to steer it toward the current needs (or have ateam surface these priorities as questions)
* autonomy and safety
    * add more agent support, add more isolation options (built-in consistent sandbox independent of the agent, MacOS native containers, ...)
    * gracefully handle LLM provider outages (failover from one another to the other, pause/resume), usage restrictions (short term windows, long term windows / budget) so that ateam pipelines complete no matter what
* improve ateam as an orchestration layer (in addition to currently available exec/parallel)
    * orchestration of multiple stages into resumable, observable workflows
    * manage prompts by providing easy ways to add pre/post instructions and code so that the coding agents waste less tokens and time discovering information that can be algorithmically discovered and to verify the work produced deterministically. It is how ateam itself is evolving: start with prompts doing most of the heavy lifting and as the process matures move more to code before/after LLMs
    * better context reuse: try to avoid running from scratch every time by reusing discovery of a codebase

- 0.9.0 Refactor roles and do some eval to use less tokens and improve accuracy
- 1.0.0 Cleanup in CLI options, file layout and database structure for future work

Then the focus becomes
- add an internal task system instead of relying on files to make the unit of report/review/code a finding and not have to deal with entire files. It will reduce token consumption and make the underlying plumbing strong for find/review/edit flows. Files are still great for many workflows
- Focus on reducing token consumption
    - Use an internal task system and move coding to algorithmic instead of relying on on agent prompt to consume less tokens and make the system more deterministic
    - More formal prompt evals to tune prompt
    - Research code indexing tools to reduce code discovery cost of each report
    - Auto-discovery of testing tools and run them outside of agents as gates
- More flexible and generic core
    - full prompt templating: variable expansion, inclusion, run sandboxed shell commands (integrate with Fence to have a native sandbox inside of ateam)
    - custom resumable workflow using the task system for state tracking
    - ad-hoc execution based on tasks with automatic life cycle management and dependencies management
- other
    - more agents: Pi, Gemini
    - more containers: MacOS Containers, Fence sandbox as a lightweight consistent sandbox around any agent
    - memory system: persist preferences and knowledge acquired per project and per org on tech stack
- more flexible workflow
    - make report/review/code/verify an instance of a resumable workflow system leveraging the future task system
- maybe: Built-in scheduling

## Development

See [DEV.md](DEV.md) for development setup, testing, and architecture details.

## More docs

- [APPROACH.md](APPROACH.md) — rationale and design principles
- [COMMANDS.md](COMMANDS.md) — full `ateam` command reference
- [CONFIG.md](CONFIG.md) — directory layout, `config.toml`, `runtime.hcl`
- [ISOLATION.md](ISOLATION.md) — sandbox and container guide (modes, secrets, auth)
- [ROLES.md](ROLES.md) — built-in roles
- [GUIDE.md](GUIDE.md) - how to, best practices, tips and tricks
- [FAQ.md](FAQ.md) — frequently asked questions
