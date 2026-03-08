# ATeam — AI Agent Team for Code Analysis

A Go CLI that manages role-specific AI agents to analyze codebases and produce actionable reports. Agents run in parallel via `claude -p`, and a supervisor synthesizes their findings into prioritized decisions.

## Features

- **Organization/project split** — shared defaults in `.ateamorg/`, per-project config and results in `.ateam/`
- **Multi-project support** — multiple ateam projects per git repo (monorepo-friendly)
- **15 built-in agents** — security, testing, refactoring, dependencies, documentation, and more
- **3-level prompt fallback** — project overrides → org overrides → embedded defaults
- **Parallel execution** — configurable concurrency with per-agent timeouts
- **Stream-json output** — real-time JSONL stream capture with cost/token tracking
- **Report archiving** — timestamped history of all prompts, reports, and reviews

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

Create a `.ateamorg/` directory with default prompts for all agents and the supervisor.

```bash
ateam install              # creates .ateamorg/ in current directory
ateam install ~/projects   # creates .ateamorg/ at the given path
```

### `ateam init [PATH]`

Initialize a project by creating a `.ateam/` directory. Requires a `.ateamorg/` discoverable from the current directory.

```bash
ateam init
ateam init --name myproject --agent testing_basic,security
```

| Flag | Description |
|------|-------------|
| `--name NAME` | Project name (defaults to relative path from org root) |
| `--agent LIST` | Agents to enable (comma-separated; if omitted, all are enabled) |
| `--git-remote URL` | Git remote origin URL (auto-detected if omitted) |

### `ateam report`

Run one or more agents in parallel to analyze the project and produce markdown reports.

```bash
ateam report --agents all
ateam report --agents security,testing_basic
ateam report --agents all --extra-prompt "Focus on the API layer"
ateam report --agents all --extra-prompt @notes.md
ateam report --agents all --dry-run
ateam report --agents all --print
```

| Flag | Description |
|------|-------------|
| `--agents LIST` | Comma-separated agent list, or `all` **(required)** |
| `--extra-prompt TEXT` | Additional instructions appended to every agent's prompt (text or `@filepath`) |
| `--timeout MINUTES` | Timeout per agent (overrides `config.toml`) |
| `--print` | Print reports to stdout after completion |
| `--dry-run` | Print computed prompts without running agents |

Output table columns: `AGENT`, `ENDED_AT`, `ELAPSED`, `COST`, `TURNS`, `STATUS`, `PATH`.

### `ateam review`

Have the supervisor read all agent reports and produce a prioritized decisions document.

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

### `ateam env`

Show the current ATeam environment: organization, project, agents, and latest report/review timestamps. Read-only — never creates or modifies anything.

```bash
ateam env
```

### `ateam projects`

List all projects discovered under the current organization.

```bash
ateam projects
```

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
  defaults/                                  # embedded prompts written to disk
    report_prompt.md                         # shared report format instructions
    code_prompt.md                           # shared code format instructions
    agents/<NAME>/report_prompt.md           # per-agent role prompt
    agents/<NAME>/code_prompt.md             # per-agent code prompt (where available)
    supervisor/review_prompt.md              # supervisor review role prompt
    supervisor/report_commissioning_prompt.md
  agents/                                    # org-level overrides (empty by default)
    <NAME>/report_prompt.md                  # override a specific agent
  supervisor/                                # org-level supervisor override
    review_prompt.md
  report_prompt.md                           # org-level report format override
```

### Project: `.ateam/`

Created by `ateam init`. Holds project config, results, and history.

```
.ateam/
  config.toml                                # project configuration
  report_prompt.md                           # project-level report format override (optional)
  agents/<NAME>/
    report_prompt.md                         # project-level agent prompt override (optional)
    extra_report_prompt.md                   # extra instructions for this agent (optional)
    full_report.md                           # latest successful report
    full_report_error.md                     # error details (on failure only)
    last_run_stream.jsonl                    # raw JSONL stream from last run
    last_run_stderr.log                      # stderr capture from last run
    history/                                 # timestamped archive
      2026-03-08_1504.full_prompt.md         # archived prompt
      2026-03-08_1504.full_report.md         # archived report
  supervisor/
    review_prompt.md                         # project-level supervisor override (optional)
    review.md                                # latest successful review
    review_error.md                          # error details (on failure only)
    last_run_stream.jsonl
    last_run_stderr.log
    history/
      2026-03-08_1504.review_prompt.md
      2026-03-08_1504.review.md
  logs/
    runner.log                               # append-only execution log
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
agent_report_timeout_minutes = 10

[agents]
security = "enabled"
testing_basic = "enabled"
refactor_small = "disabled"
```

## Prompt Resolution

Prompts are resolved with a 3-level fallback: **project** → **org** → **org defaults**. The first file found wins. This lets you customize prompts at any level without modifying the embedded defaults.

### `ateam report` — agent prompt assembly

Each agent's prompt is assembled from multiple parts, concatenated with `---` separators:

| Part | Source | Required |
|------|--------|----------|
| **Agent role prompt** | 3-level fallback (see below) | Yes |
| **Global report instructions** | 3-level fallback (see below) | No |
| **Git metadata** | Auto-detected from project | No |
| **Project-specific instructions** | `.ateam/agents/<NAME>/extra_report_prompt.md` | No |
| **Extra prompt** | `--extra-prompt` flag | No |

Agent role prompt fallback:

1. `.ateam/agents/<NAME>/report_prompt.md`
2. `.ateamorg/agents/<NAME>/report_prompt.md`
3. `.ateamorg/defaults/agents/<NAME>/report_prompt.md`

Global report instructions fallback:

1. `.ateam/report_prompt.md`
2. `.ateamorg/report_prompt.md`
3. `.ateamorg/defaults/report_prompt.md`

The placeholder `{{SOURCE_DIR}}` in prompts is replaced with the absolute path to the project source directory.

### `ateam review` — supervisor prompt assembly

When `--prompt` is provided, it replaces the supervisor role prompt entirely. Otherwise:

| Part | Source | Required |
|------|--------|----------|
| **Supervisor review prompt** | 3-level fallback (see below) or `--prompt` | Yes |
| **Report manifest** | Auto-discovered from agent reports | Yes |
| **Git metadata** | Auto-detected from project | No |
| **Agent reports** | All `full_report.md` files under `.ateam/agents/` | Yes |
| **Extra prompt** | `--extra-prompt` flag | No |

Supervisor review prompt fallback:

1. `.ateam/supervisor/review_prompt.md`
2. `.ateamorg/supervisor/review_prompt.md`
3. `.ateamorg/defaults/supervisor/review_prompt.md`

### Default prompt examples

The embedded default prompts are in the source tree under [`internal/prompts/defaults/`](internal/prompts/defaults/):

| Prompt | Source file |
|--------|------------|
| Global report instructions | [`defaults/report_prompt.md`](internal/prompts/defaults/report_prompt.md) |
| Global code instructions | [`defaults/code_prompt.md`](internal/prompts/defaults/code_prompt.md) |
| Supervisor review | [`defaults/supervisor/review_prompt.md`](internal/prompts/defaults/supervisor/review_prompt.md) |
| Agent: security | [`defaults/agents/security/report_prompt.md`](internal/prompts/defaults/agents/security/report_prompt.md) |
| Agent: testing_basic | [`defaults/agents/testing_basic/report_prompt.md`](internal/prompts/defaults/agents/testing_basic/report_prompt.md) |
| Agent: refactor_small | [`defaults/agents/refactor_small/report_prompt.md`](internal/prompts/defaults/agents/refactor_small/report_prompt.md) |

All agent prompts follow the same pattern: `defaults/agents/<NAME>/report_prompt.md`.

## Agents

Agents are auto-discovered from [`internal/prompts/defaults/agents/`](internal/prompts/defaults/agents/). Each subdirectory containing a `report_prompt.md` becomes a valid agent. Use `all` as shorthand for every agent.

Available agents: `automation`, `basic_project_structure`, `critic_engineering`, `critic_project`, `database_config`, `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `refactor_architecture`, `refactor_small`, `security`, `shortcut_taker`, `testing_basic`, `testing_full`.

## Troubleshooting

### Runner log

Every `ateam report` and `ateam review` invocation is logged to `.ateam/logs/runner.log`. Each line is tab-separated:

```
TIMESTAMP    AGENT_ID    STATUS    CLI_COMMAND    [EXTRA]
```

- **start** lines include the CLI invocation and the path to the archived prompt file
- **ok** lines confirm successful completion
- **error** lines include the error message

Example:

```
2026-03-08T15:04:00Z	security	start	claude -p --output-format stream-json --verbose	.ateam/agents/security/history/2026-03-08_1504.full_prompt.md
2026-03-08T15:06:23Z	security	ok	claude -p --output-format stream-json --verbose
2026-03-08T15:04:00Z	testing_basic	start	claude -p --output-format stream-json --verbose	.ateam/agents/testing_basic/history/2026-03-08_1504.full_prompt.md
2026-03-08T15:07:01Z	testing_basic	error	claude -p --output-format stream-json --verbose	timed out after 10 minutes
```

### Detailed output

Use `--dry-run` on `report` and `review` to inspect the fully assembled prompt without running anything:

```bash
ateam report --agents security --dry-run    # print the prompt that would be sent
ateam review --dry-run                      # print prompt and list discovered reports
```

When a run fails, inspect these files in the agent's directory:

| File | Content |
|------|---------|
| `full_report_error.md` | Error summary, exit code, duration, stderr, partial output, token usage |
| `last_run_stderr.log` | Raw stderr from the `claude` subprocess |
| `last_run_stream.jsonl` | Raw JSONL event stream (useful for debugging parsing issues) |

For the supervisor, the equivalent error file is `supervisor/review_error.md`.

### History

Every run archives its prompt and output to the `history/` directory with a timestamp prefix (`YYYY-MM-DD_HHMM`):

```bash
ls .ateam/agents/security/history/
# 2026-03-07_1430.full_prompt.md
# 2026-03-07_1430.full_report.md
# 2026-03-08_0900.full_prompt.md
# 2026-03-08_0900.full_report.md

ls .ateam/supervisor/history/
# 2026-03-07_1435.review_prompt.md
# 2026-03-07_1435.review.md
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

### Adding a new agent

1. Create `internal/prompts/defaults/agents/AGENT_NAME/report_prompt.md`
2. Optionally add `code_prompt.md` in the same directory
3. Rebuild with `make build` — the agent is auto-discovered from the embedded filesystem
