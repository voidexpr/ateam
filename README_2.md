# ATeam

**Run coding agents unattended. Keep your codebase healthy in the background.**

ATeam is a CLI to run existing coding agents (Claude Code, Codex) unattended. It also provides a four-stage software engineering quality pipeline (**report → review → code → verify**) and a library of role prompts covering bugs, tests, security, dependencies, docs, architecture, and more.

It automates the parts you don't want to do to free up your attention for feature work, architecture and any task you choose.

## Why ATeam

### The missing quality pipeline

Coding agents prioritize feature completion over software quality, which is a good short-term tradeoff that degrades over time. Tests fall behind, security issues accumulate, code becomes spaghetti, docs go stale, dependencies rot, ...

At the same time, coding agents are good at auditing and fixing quality issues. They can be prompted to be pragmatic: adapt to the project size, small wins are ok, avoid busy work, look for automation opportunities so cost goes down over time, ...

ATeam makes quality oriented work a one-liner you can run on demand, daily, schedule weekly to keep a code base healthy. It is useful whether the code is authored by agents, humans or both, to be perfectly fair it's not like humans don't need to refactor their code, forget to add tests, neglect security, ... So being able to get a consistent baseline of quality work automated is clear win.

### Attention is the new bottleneck

Developing new features or the architecture of a project requires a lot of thinking and concentration, interactive agents are a great enabler, helping brainstorm, design, and code extremely quickly. But then the bottleneck becomes your attention.

A growing share of code is written by coding agents. So instead of making humans full time reviewer, having to create an ever growing set of skills (for example: `/write-tests /update-docs /update-architecture /simplify /code-review high --fix recent changes`), be the first line quality enforcer we can free up time by automating at least a baseline of it.

Software engineering quality work is the true sweet spot for unattended agents because it can be prompted once unlike features that benefit more from an interactive model. `ateam resume` is provided to take an unattended session and make it interactive, so you can talk to your agent that did that refactor last Tuesday night to find out more.

### `claude -p` works until it doesn't

Coding agents all provide flexible ways to run unattended, but a lot more tooling is required: a uniform interface across agents, conventions for logs, execution profiles, isolation parameters, tracking cost (tokens, turns, context), dynamic prompt assembly, move prompt logic to scripts to reduce costs, ...

ATeam gives you the `ps` command for unattended agents: clearly see how long they take and how much they cost, so you can improve your prompts over time, decide what runs daily vs. weekly and not repeatedly run that $20 one-liner without realizing it.

It also gives the 'exec' and 'parallel' command to build your own scripts as a simple bash script or you can wrap it in something more complex.

Having this type of harness makes it possible to invest more heavily on unattended work without being too dependent of a specific coding agent, given flexibility to choose the best pricing or the ones providing the most interesting features.

see more at [APPROACH.md](APPROACH_2.md).

## Key Features

**Agents and isolation**
- Drives Claude Code (`claude -p` with `stream-json`) and Codex (`exec`); experimental `codex-tmux` lets TUI-only commands like `/review` run unattended
- Multiple isolation modes: built-in agent sandbox (default), one-shot Docker, exec into a long-lived container (Docker / devcontainer / compose), or run ateam itself inside Docker (removes all permission checks). This is required to balance permissions vs. safety.
- Config files to manage agent and container invocation, for example profiles select agent + container + custom arguments combos (`--profile docker`, `--profile codex-high`)
- Can use the default subscription, oauth, API keys using secret management in OS keychain, prioritizing the cheapest mode if multiple keys are available

**Quality pipeline**
- Four stages: `report` (parallel role audits) → `review` (supervisor prioritizes) → `code` (delegated fixes, small commits) → `verify` (commit inspection + tests). Run as `ateam run-all` or stage-by-stage.
- 11 built-in roles: code bugs, recent-change review, structural quality, system architecture, internal/external docs, project automation, dependencies, security, test gaps, recent-test coverage (see [ROLES.md](ROLES.md))
- Stateful markdown artifacts: each cycle reads the previous reports and review; quality compounds, no context is lost
- 3-level prompt fallback (project `.ateam/` → org `.ateamorg/` → embedded defaults) with composable pre/post extensions; new roles are a single markdown file

**Observability and troubleshooting**
- `ateam ps` — recent runs and their status; `ateam tail` — live agent output
- `ateam inspect EXEC_ID` — full execution details, prompts, and logs; `--auto-debug` runs an agent that reads the failure and proposes a fix
- `ateam resume EXEC_ID`: create an interactive session from an unattended one, ask questions to any past agent
- `ateam cost` — token usage and dollars per run, role, and agent
- `ateam serve` — web UI for browsing all reports, reviews, runs, and costs; `ateam export` for a self-contained HTML snapshot
- `ateam prompt --role NAME` shows the exact assembled prompt; `ateam env` summarizes config and environment

**Agent helpers everywhere**
- `ateam auto-setup`: don't read the docs — ask an agent to select ATeam roles based on your project
- `ateam inspect --auto-debug`: have an agent investigate why past runs failed, recommend config changes, and draft a bug to file against ATeam if needed
- `ateam report --auto-roles`: dynamically select which roles to run based on recent commits
- `scripts/ateam-runall-managed.sh`: run a full quality pipeline and, on error, have an agent try to fix it and resume

Note about maturity and cost: ateam was started in Feb 2026 and has been used mostly on vibe coded project (including itself). The approach is validated: it improves code bases and saves attention. It is also not free, especially once the mid June 2026 Claude unattended agent price increase kicks in. It still seems well worth it. It's not like code agent produces will magically engineer itself as it is written. A pipeline like ateam is needed by agentic project and can still benefit more classical project with developers written a lot of the code by narrowing the roles ateam uses to audit the code base.

## Install

```bash
git clone https://github.com/voidexpr/ateam.git
cd ateam && ./install.sh
```

Authenticate Claude Code or Codex if you haven't already (`claude` / `codex`). For unattended use in cron or containers, see [CONFIG.md](CONFIG.md) for credential storage.

Requires Go 1.26+ (installed automatically by `install.sh`) and one coding agent CLI. Docker optional.

## Quick Start

### 1. Configure ateam for a git workspace
```bash
cd /path/to/your/project
ateam init                # create .ateam/
```

### 2. Select which roles to enable for your project

* A: Edit `.ateam/config.toml` to select which [role](ROLES.md) to enable in your project
* B: Or let an agent decide: `ateam auto-setup` (detect stack, enable a reasonable role set)

### 3. Run report → review → code → verify; ATeam commits the changes locally
```bash
ateam run-all                 # report → review → code → verify

# or run one by one:
ateam report
ateam review
ateam code
ateam verify
```

### 4. Browse all artifacts in a web browser
```bash
ateam serve               # browse artifacts in your browser
```

That's the whole flow. You can also run `ateam exec` and `ateam parallel` from there for your own scripts.

Once familiar with ateam read [ISOLATION.md](ISOLATION.md) to choose the best balance for your project.

## How it works

`ateam init` creates a `.ateam/` directory in your repo. It holds a small SQLite database tracking agent executions and cost, all logs, and the markdown artifacts produced by roles. You can run `ateam init` anywhere — the CLI walks up parent directories to find it, like `git`.

A second directory, `.ateamorg/` (default: `$HOME/.ateamorg`), holds prompts and configuration you want shared across projects.

Prompts resolve in order: **project → organization → embedded defaults.** You can fully override a prompt at any level, or — more commonly — extend it with a post-prompt fragment. Example: drop `*.post.extra.md` into `.ateam/prompts/report/project.security/` with *"do not flag GitHub Actions secrets, we use a separate vault"* and that instruction is appended every time the role runs.

Full details: [CONFIG.md](CONFIG.md).

`ateam serve`:
![ateam serve — browse reports, reviews, and cost in your browser](docs/ateam_serve_overview.jpg)

`ateam ps`:
![ateam ps — recent runs with duration, cost, and tokens](docs/ateam_ps.jpg)

## Two ways to use it

#### As a quality pipeline

`ateam run-all` runs the four-stage loop across the roles enabled in `.ateam/config.toml`:

- **report** — role agents audit the codebase in parallel
- **review** — supervisor prioritizes findings into coding tasks
- **code** — coding agents implement the top tasks, commit small changes
- **verify** — supervisor inspects the commits and runs tests

Roles are single markdown prompt files. Built-in ones cover code bugs, recent-change review, structure, architecture, internal/external docs, automation, dependencies, security, test gaps, and recent-test coverage. Add your own by dropping a file in `.ateam/prompts/report/`.

#### As a primitive

`ateam exec` and `ateam parallel` run unattended coding agents with your own prompts. Drop them into shell scripts to build any workflow.

```bash
ateam exec "audit recent changes for bugs" --agent codex
ateam parallel "@prompts/security.md" "@prompts/tests.md"
ateam exec "review findings in $REPORT and apply the fixes" --agent claude
```

## Examples

A few flavors of how people use it:

#### Daily pass on recent changes
```bash
ateam run-all --roles code.recent,test.recent
```
Quick, focused, cheap. Good before a PR or as a recurring run.

#### Adversarial review — Codex critiques, Claude implements
```bash
ateam exec "critical review of recent changes into review.md" --agent codex-high
ateam exec "review.md → apply fixes, commit each separately"  --agent claude-high
```
Two agents, two viewpoints. The CLI primitive lets you compose any pattern in shell.

#### Background quality on a fast-moving project
```bash
ateam run-all                # end-of-day, in cron, or before commits
```
Roles from `.ateam/config.toml` run unattended. You wake up to small commits to review or merge.

More recipes (lunch-pass / weekly audit / step-by-step / mixed-agent scripts): [GUIDE.md](GUIDE_2.md).

## Isolation

By default ATeam uses the agent's built-in sandbox — fast, no setup, OS-level syscall restrictions. You can also use a one-shot Docker container per command, exec into an existing container (devcontainer / compose), or run ateam itself from inside a container. Switch with `--profile docker` or `--profile docker-exec`. See [ISOLATION.md](ISOLATION.md).

## Commands

| | |
|---|---|
| **Per project setup** | `init`, `auto-setup` |
| **Pipeline** | `run-all` or: `report`, `review`, `code`, `verify` |
| **Ad-hoc** | `exec`, `parallel` |
| **Process / cost** | `ps`, `tail`, `resume`, `inspect`, `cat`, `cost` |
| **Web Interface for Artifacts** | `serve`, `export` |
| **Config / debug** | `env`, `prompt`, `roles`, `secret` |

Full reference: [COMMANDS.md](COMMANDS.md).

## Docs

- [GUIDE.md](GUIDE_2.md) — recipes, role tuning, when (not) to use ateam
- [APPROACH.md](APPROACH_2.md) — rationale, positioning, how ateam compares to other frameworks
- [CONFIG.md](CONFIG.md) — directory layout, prompt overrides, runtime profiles
- [ISOLATION.md](ISOLATION.md) — sandbox and container modes
- [ROLES.md](ROLES.md) — built-in role catalog
- [FAQ.md](FAQ.md)
- [DEV.md](DEV.md) — development setup, testing, internals
