# ATeam

**Run coding agents unattended. Keep your codebase healthy in the background.**

ATeam is a CLI to run existing coding agents (Claude Code, Codex) unattended. It also provides a four-stage software engineering quality pipeline (**report → review → code → verify**) and a library of role prompts covering bugs, tests, security, dependencies, docs, architecture, and more.

It automates the parts you don't want to do to free up your attention for feature work, architecture and any task you choose.

## Why ATeam

#### The Engineer night shift

Software engineering teams don't just ship features. Engineers refactor, write missing tests, debug, bump dependencies before they rot, and (ideally) keep docs honest. ATeam is designed to automate these tasks before your progress suffers from technical debt. Coding agents are perfectly capable of performing a baseline level of engineering work tirelessly. Ateam makes this a one-liner.

Agents are a great opportunity to delegate the parts of this work project owners don't want to focus on.

#### Coding Agents do only some of the work

Coding agents prioritize feature completion over software quality which is a good short-term tradeoff that degrades over time. Tests fall behind, security issues accumulate, code becomes spaghetti, docs go stale, dependencies rot, ...

At the same time coding agents are also very good at auditing and fixing. They can be prompted to be pragmatic: small wins are ok, look for automation opportunities so cost can go lower over time.

So ateam provides pre-made prompts to continuously improve any code base.

#### Your attention is the bottleneck

Developing new features or architecting a project requires a lot of thinking and concentration, interactive agents are great enabler and helping brainstorming, design and code extremely quickly. But then the bottleneck becomes our attention.

Get rid of repetitive prompts like `/write-tests /update-docs /update-architecture /simplify /code-review high --fix recent changes`, just run an ateam pipeline after your work day to do the same and also catch that security issue that new dependency just added.

A growing share of code is written by coding agents. Humans should focus on high value tasks and delegate more of the code lifecycle that can actually be prompted once and ran repetitively.

Unattended agents are one solution to free up more attention to high level work: prompt once and re-run on a schedule, after a work day or perform deeper audits before releasing a project.

#### Software engineer quality work is the sweet-spot for unattended agents

Feature work requires attention, iteration, experimentation and delegating it to a swarm of agents would require to perfectly understand it from the beginning. Then agents want to please whoever is prompted them so drift propagates between agents. So feature work doesn't seem a good candidate for unattended agents but quality tasks can be prompted once, reuse state from their previous run and re-evaluate the code along the criteria they are given.

This is the bet of ateam.

#### `claude -p` works until it doesn't

Coding agents all provide flexible ways to run unattended but eventually when ran regularly a lot more tooling is required: make uniform between agents, convention for logs, execution profile, isolation parameters, tokens used, turns, costs, etc ...

ateam gives you the `ps` command for unattended agents: clearly see how long they take and how much they cost so you can improve your prompt over time and not repetitively run this $20 one liner without realizing it.

The more features are needed over time: run in parallel, dynamic prompt assembly, delegate some work done by prompts to scripts, ...

ateam is not a workflow engine, it implements just one complex pipeline but it is a great building block to build more complex processes scaling from simple bash scripts mixing commands and prompts to true persistent workflow management can all be built using ateam.

**In practice:** reuses your existing coding agents (no lock-in), every artifact is a markdown file, every change is a git commit you can read or revert, and the whole system fits in your head — no DAG, no graph framework, no colorful vocabulary. More on the rationale: [APPROACH.md](APPROACH_2.md).

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

**Agents helpers everywhere**
- `init --auto-step`: don't read the docs, ask an agent to select ateam roles based on your project
- `inspect --auto-debug`: have an agent investigate why past run(s) failed, recommend config changes and provide the bug to file against ateam if needed
- `report --auto-roles`: dynamically select which roles to run based on recent commits
- `scripts/ateam-runall-mangaged.sh`: run a full quality pipeline and in case of error have an agent try to fix and resume

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

### 3. Run report → review → code → verify and get git commits performed locally
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

> TODO: screenshot of `ateam serve` / `ateam ps` / `ateam cost`

```
 ateam ps
ID   STARTED         PROFILE  ACTION  ROLE                  MODEL                      DURATION  COST   TOKENS  TURNS  STATUS  BATCH                       REASON
94   06-01 12:50:37  default  exec                          claude-opus-4-7            2s        $0.24  12      1      ok
95   06-01 13:23:46  default  exec                          claude-haiku-4-5-20251001  8s        $0.06  40.0K   7      error                               [user_canceled] run canceled (Ctrl-C, SIGTERM, or parent context canceled)
96   06-01 13:24:00  default  exec                          claude-haiku-4-5-20251001  6s        $0.06  39.9K   4      ok
97   06-01 13:37:45  default  exec                          claude-haiku-4-5-20251001  7s        $0.06  39.9K   6      ok
98   06-01 16:25:46  default  report  code.structure        claude-opus-4-7            3m44s     $2.02  2.1M    35     ok      report-2026-06-01_16-25-46
99   06-01 16:25:46  default  report  code.recent           claude-opus-4-7            7m42s     $4.58  6.0M    64     ok      report-2026-06-01_16-25-46
100  06-01 16:25:46  default  report  code.bugs             claude-opus-4-7            6m37s     $3.67  4.0M    79     ok      report-2026-06-01_16-25-46
101  06-01 16:29:32  default  report  docs.external         claude-opus-4-7            2m14s     $1.17  856.3K  14     ok      report-2026-06-01_16-25-46
102  06-01 16:31:47  default  report  docs.followable       claude-opus-4-7            3m26s     $2.00  1.9M    22     ok      report-2026-06-01_16-25-46
103  06-01 16:32:24  default  report  docs.internal         claude-opus-4-7            3m50s     $2.07  2.4M    35     ok      report-2026-06-01_16-25-46
104  06-01 16:33:29  default  report  project.automation    claude-opus-4-7            1m19s     $0.74  505.6K  13     ok      report-2026-06-01_16-25-46
105  06-01 16:34:48  default  report  project.dependencies  claude-opus-4-7            36s       $0.48  148.4K  5      ok      report-2026-06-01_16-25-46
106  06-01 16:35:14  default  report  project.security      claude-opus-4-7            2m57s     $2.28  2.6M    30     ok      report-2026-06-01_16-25-46
107  06-01 16:35:25  default  report  test.blackbox         claude-opus-4-7            4m5s      $1.88  1.4M    20     ok      report-2026-06-01_16-25-46
108  06-01 16:36:15  default  report  test.gaps             claude-opus-4-7            2m42s     $1.10  751.9K  16     ok      report-2026-06-01_16-25-46
109  06-01 16:38:11  default  report  test.quality          claude-opus-4-7            3m34s     $2.03  2.2M    28     ok      report-2026-06-01_16-25-46
110  06-01 16:38:58  default  report  test.recent           claude-opus-4-7            5m42s     $2.91  3.1M    32     ok      report-2026-06-01_16-25-46
111  06-01 16:44:41  default  review  supervisor            claude-opus-4-7            2m14s     $1.15  129.4K  2      ok
```

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
| **Pipeline** | `all` or: `report`, `review`, `code`, `verify` |
| **Ad-hoc** | `exec`, `parallel` |
| **Process / cost** | `ps`, `tail`, `resume`, inspect`, `cat`, `cost` |
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
