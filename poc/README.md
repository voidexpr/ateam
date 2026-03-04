# ATeam POC — Reporting System

A Go CLI that spawns `claude -p` processes to produce role-specific code analysis reports, then has a supervisor review and prioritize findings.

## Build

```bash
make build
```

Or manually:

```bash
go mod tidy
go build -o ateam .
```

## Usage

```bash
# Initialize a project (creates working directory with prompts and config)
./ateam init myproject --source /path/to/your/code --agents all

# Run agents to produce reports (from the project directory)
cd myproject
../ateam report --agents all
../ateam report --agents testing_basic,security --extra-prompt "Focus on the API layer"

# Have the supervisor review all reports
../ateam review
../ateam review --extra-prompt "This is a production financial app"
```

## Prerequisites

- Go 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`claude` command available in PATH)

## Project Structure

After `ateam init`, the working directory looks like:

```
myproject/
  config.toml           # project config (source dir, agents, timeouts)
  prompts/
    report_instructions.md    # shared report format instructions
    supervisor_role.md        # supervisor system prompt
    review_instructions.md    # review output format
    agents/
      refactor_small.md       # per-agent role prompts (customizable)
      security.md
      ...
  reports/              # latest reports (overwritten each run)
  archive/              # timestamped copies of all reports
  review.md             # latest supervisor decisions
```

All prompts are written to disk during `init` — edit them before running reports to customize behavior.

## Agents

| Agent | Focus |
|---|---|
| `refactor_small` | Small refactoring: naming, duplication, error handling |
| `refactor_architecture` | Architecture: coupling, layering, abstractions |
| `docs_internal` | Internal docs: architecture, code overview, dev guides |
| `docs_external` | External docs: README, installation, usage |
| `basic_project_structure` | Project structure: file layout, build system, conventions |
| `automation` | CI/CD, linting, formatting, pre-commit hooks |
| `dependencies` | Dependency health: outdated, unused, vulnerable |
| `testing_basic` | Test coverage gaps, missing edge cases |
| `testing_full` | Test suite architecture, flaky tests, integration gaps |
| `security` | Vulnerabilities, injection risks, secrets, auth patterns |

Use `all` as shorthand for every agent.
