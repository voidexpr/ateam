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
# One-time: create ~/.ateam/ with default prompts
ateam install

# From any git project directory — auto-discovers git root and .ateam/
ateam init --agents all
ateam report --agents all
ateam report --agents testing_basic,security --extra-prompt "Focus on the API layer"
ateam report --agents all --print    # also display reports to stdout
ateam review
ateam review --extra-prompt "This is a production financial app"
ateam review --print                 # also display review to stdout
```

`ateam report` and `ateam init` auto-create the `.ateam/` structure if it doesn't exist yet, so `ateam install` is optional.

## Prerequisites

- Go 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`claude` command available in PATH)

## Directory Layout

ATeam stores all artifacts in a `.ateam/` directory (by default `~/.ateam/`):

```
~/.ateam/
  agents/
    refactor_small/
      report_prompt.md          # default agent prompt (customizable)
    security/
      report_prompt.md
    ...
  supervisor/
    review_prompt.md            # default supervisor prompt
  expertise/                    # (reserved for future use)
  projects/
    code/myapp/                 # mirrors git root relative path from $HOME
      config.toml               # project config (source dir, agents, timeouts)
      agents/
        refactor_small/
          full_report.md        # latest report
          extra_report_prompt.md  # project-specific extra instructions (optional)
          history/              # timestamped report archive
        ...
      supervisor/
        review.md               # latest supervisor decisions
        history/                # timestamped review archive
```

Prompt lookup order: project-level override → root-level default.

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
