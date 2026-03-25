# ATeam — AI Role Team for Code Analysis

ATeam is a CLI that points a crew of role-specific coding agents at your codebase. Each agent audits code across selected dimensions like code refactoring, testing, security, dependencies, documentation, etc. Then a supervisor prioritizes the findings and runs coding agents to implement fixes. It works unattended, out of the box, for any tech stack. It is solely focused on project quality and doesn't make any feature change.

Think of it as a team of expert colleagues for software quality: they audit while you sleep, commit small focused fixes, and the next run builds on the last.

Coding agents (rightfully) prioritize getting features to work at the cost of longer term software engineering tasks. As a result, new features eventually keep breaking existing ones or security issues appear. But it turns out that coding agents are also very good at auditing code and artifacts. Instead of typing “/simplify” after each change or asking agents to perform maintenance tasks repeatedly, with ATeam you simply schedule it to run after a workday. Over time, project quality is maintained so that only feature work requires attention and there is no slowdown because best practices are enforced along the way.

The architecture is very simple: a few prompt files, some automation to run one-shot coding agents like Claude or Codex (by default in a sandbox but it is configurable, including running within a Docker container), and it produces markdown files at each step to make auditing easy. The prompts instructs ateam agents to make pragmatic changes and not over do, adapting to the project size.

## Quick Start

```bash
git clone https://github.com/voidexpr/ateam.git
cd ateam && ./install.sh
```

The install script checks for Go (installs it if missing), builds the binary, and symlinks it into `~/.local/bin/`.

```bash
# 0. Use your own workspace, a git worktree or a separate workspace for ateam
cd /path/to/your/project

# 1. Initialize
ateam init

# 2. Auto-configure roles for your project (optional)
ateam auto-setup

# 3. Run
ateam report                         # run all enabled role analyses
ateam review                         # supervisor prioritizes findings
ateam code                           # execute top-priority fixes
```

Or run the full pipeline: `ateam all`

You can see all artifacts under `.ateam/` or via an experimental web UI `ateam serve`.

### Prerequisites

- **Go 1.24+** — installed automatically by `install.sh`
- **[Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code)** — install and authenticate before running agents
- **Docker** (optional) — enables isolated execution via `--profile docker`

### Manual Install

```bash
go version          # ensure Go 1.24+
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
- Performance regression detector

## Key Concepts

- **3-level prompt fallback**: project → org → embedded defaults. Customize at any level.
- **Multi-project support**: share org-wide defaults across projects via `.ateamorg/` (by default created in `$HOME`)
- **Runtime profiles**: switch agent/container combos with `--profile docker` or `--profile cheap`
- **Cost tracking**: `ateam cost` for aggregated reports, `ateam ps` for run history
- **Secret management**: `ateam secret` stores API keys in OS keychain or `.env` files

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

## Why ATeam

Coding agents prioritize feature completion over software quality — a good short-term tradeoff that degrades over time. Tests fall behind, security issues accumulate, docs go stale, dependencies rot.

ATeam addresses this by running quality-focused agents on a schedule. No interactive prompting needed. No feature changes. Just steady, incremental quality improvement that looks like the code was written well in the first place.

Core principles:
- **No feature work** — focus on quality, don't change behavior
- **Unattended** — works without approval or interaction
- **Pragmatic** — adapts to project size and maturity
- **Simple** — reuses existing coding agents, minimal orchestration
- **Safe** — sandboxing and container isolation
- **Auditable** — every artifact is a readable markdown file

## Future

- Better context and memory
  - Reduce prompt size by moving more instructions into ateam itself
  - Keep `overview.md` up to date based on commits
- More agents and profiles
- Scheduling integration

## Development

See [DEV.md](DEV.md) for development setup, testing, and architecture details.
