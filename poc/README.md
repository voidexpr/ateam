# ATeam — AI Agent Team for Code Analysis

A Go CLI that manages role-specific AI agents to analyze codebases and produce actionable reports. Agents run in parallel via `claude -p`, and a supervisor synthesizes their findings into prioritized decisions.

## Features

- **Organization/project split** — shared defaults in `.ateamorg/`, per-project config and results in `.ateam/`
- **Multi-project support** — multiple ateam projects per git repo (monorepo-friendly)
- **16 built-in agents** — security, testing, refactoring, dependencies, documentation, and more
- **3-level prompt fallback** — project overrides → org overrides → embedded defaults
- **Parallel execution** — configurable concurrency with per-agent timeouts
- **Report archiving** — timestamped history of all reports and reviews

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

## Usage

```bash
# Create an organization (shared defaults and agent prompts)
ateam install ~/projects

# Initialize a project (from within a git repo)
cd ~/projects/myapp
ateam init --agent security,testing_basic,refactor_small

# Or initialize with all agents enabled
ateam init

# Run agents
ateam report --agents all
ateam report --agents security,testing_basic
ateam report --agents all --extra-prompt "Focus on the API layer"
ateam report --agents all --dry-run     # show prompts without running
ateam report --agents all --print       # also print reports to stdout

# Supervisor review
ateam review
ateam review --extra-prompt "This is a production financial app"
ateam review --dry-run                  # show prompt without running

# Show current environment
ateam env

# List all projects under the organization
ateam projects

# Update default prompts to match current binary
ateam update
ateam update --quiet
```

### Global flags

All commands accept `-o`/`--org` and `-p`/`--project` to override automatic discovery.

## Directory Layout

### Organization: `.ateamorg/`

Created by `ateam install`. Holds shared defaults and org-level overrides.

```
.ateamorg/
  defaults/                          # embedded prompts written to disk
    agents/NAME/report_prompt.md
    agents/NAME/code_prompt.md       # (where available)
    supervisor/review_prompt.md
    supervisor/report_commissioning_prompt.md
    report_prompt.md                 # shared report format
    code_prompt.md                   # shared code format
  agents/                            # org-level overrides (empty by default)
    NAME/
    supervisor/
```

### Project: `.ateam/`

Created by `ateam init`. Holds project config and results.

```
.ateam/
  config.toml
  agents/NAME/
    report_prompt.md                 # project-level override (optional)
    full_report.md                   # latest report
    extra_report_prompt.md           # extra instructions (optional)
    history/                         # timestamped archive
  supervisor/
    review.md                        # latest review
    history/
```

### Prompt resolution

Prompts are resolved with a 3-level fallback:

1. `.ateam/agents/NAME/report_prompt.md` (project override)
2. `.ateamorg/agents/NAME/report_prompt.md` (org override)
3. `.ateamorg/defaults/agents/NAME/report_prompt.md` (embedded defaults)

## Agents

Agents are auto-discovered from `internal/prompts/defaults/agents/`. Each subdirectory containing a `report_prompt.md` becomes a valid agent. Use `all` as shorthand for every agent.

Available agents: `automation`, `basic_project_structure`, `critic_engineering`, `critic_project`, `database_config`, `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `refactor_architecture`, `refactor_small`, `security`, `shortcut_taker`, `testing_basic`, `testing_full`.

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
go test ./internal/root/ -run TestIntegration -v  # integration tests only
```

### Adding a new agent

1. Create `internal/prompts/defaults/agents/AGENT_NAME/report_prompt.md`
2. Optionally add `code_prompt.md` in the same directory
3. Rebuild with `make build` — the agent is auto-discovered from the embedded filesystem
