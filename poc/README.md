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
ateam report --agents all --dry-run  # show computed prompts without running
ateam review
ateam review --extra-prompt "This is a production financial app"
ateam review --print                 # also display review to stdout
ateam review --dry-run               # show reports found and computed prompt

# Update default prompts to match current binary
ateam update-prompts
# Or symlink defaults to your own prompt directory
ateam update-prompts --symlink ~/my-prompts
```

`ateam report` and `ateam init` auto-create the `.ateam/` structure if it doesn't exist yet, so `ateam install` is optional.

## Prerequisites

- Go 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated (`claude` command available in PATH)

## Directory Layout

ATeam stores all artifacts in a `.ateam/` directory (by default `~/.ateam/`):

```
~/.ateam/
  defaults/                       # mirrors internal/prompts/defaults/ — can be symlinked
    agents/
      refactor_small/
        report_prompt.md          # agent role prompt
      security/
        report_prompt.md
      ...
    supervisor/
      review_prompt.md            # supervisor prompt
    report_instructions.md        # shared report format instructions
  expertise/                      # (reserved for future use)
  projects/
    code/myapp/                   # mirrors git root relative path from $HOME
      config.toml                 # project config (source dir, agents, timeouts)
      agents/
        refactor_small/
          report_prompt.md        # project-level role override (optional)
          full_report.md          # latest report
          extra_report_prompt.md  # project-specific extra instructions (optional)
          history/                # timestamped report archive
        ...
      supervisor/
        review_prompt.md          # project-level override (optional)
        review.md                 # latest supervisor decisions
        history/                  # timestamped review archive
```

Prompt lookup: project-level override → `defaults/`. Agent prompts are assembled by combining the role prompt with `report_instructions.md` at run time, so project overrides only need the role-specific part.

## Agents

Agents are auto-discovered from `internal/prompts/defaults/agents/`. Each subdirectory containing a `report_prompt.md` becomes a valid agent. To add a new agent, create the directory with its prompt and rebuild.

Use `all` as shorthand for every agent. Run `ateam report --agents invalid` to see the current list.
