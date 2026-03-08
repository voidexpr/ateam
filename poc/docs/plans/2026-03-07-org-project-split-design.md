# Design: Org/Project Directory Split

## Problem

The single `.ateam` directory serves as both organization-level config (defaults, shared state) and project-level state (config, reports, reviews). This prevents:
- Multiple ateam projects per git repo (monorepo teams)
- Hosting project state inside or outside a git repo flexibly
- Clear separation of shared defaults from project-specific data

## Solution

Split into `.ateamorg/` (organization) and `.ateam/` (project).

## Directory Structure

### Organization: `.ateamorg/`

Created by `ateam install [PATH]`.

```
.ateamorg/
  defaults/                                    # binary-embedded prompts
    agents/NAME/report_prompt.md
    agents/NAME/code_prompt.md                 # new (replaces apply_instructions.md)
    supervisor/review_prompt.md
    supervisor/report_commissioning_prompt.md   # new
    report_prompt.md                           # shared report format (was report_instructions.md)
    code_prompt.md                             # shared code format (new)
  agents/                                      # org-level overrides (empty by default)
    NAME/
    supervisor/
```

### Project: `.ateam/`

Created by `ateam init [PATH]`.

```
.ateam/
  config.toml
  agents/NAME/
    full_report.md
    history/
    extra_report_prompt.md     # optional project-specific extra prompt
  supervisor/
    review.md
    history/
```

## Config Format

```toml
[project]
name = "level1/myproj"
source = "level1/myproj"

[git]
repo = "."
remote_origin_url = "https://foobar/myproj.git"

[report]
max_parallel = 3
agent_report_timeout_minutes = 10

[review]

[code]

[agents]
testing_basic = "enabled"
security = "enabled"
refactor_small = "enabled"
automation = "disabled"
```

## Prompt Resolution

3-level fallback for all prompt files:

1. `.ateam/agents/NAME/report_prompt.md` (project override)
2. `.ateamorg/agents/NAME/report_prompt.md` (org override)
3. `.ateamorg/defaults/agents/NAME/report_prompt.md` (binary defaults)

Same cascade for `code_prompt.md` and supervisor prompts.

## Path Resolution

### Organization discovery

Walk up from cwd:
- Inside a directory named `.ateamorg` -> that's the org
- A parent directory has a `.ateamorg` child -> that's the org
- `-o|--org` flag overrides

### Project discovery

Walk up from cwd:
- Inside a directory named `.ateam` -> that's the project
- A parent directory has a `.ateam` child -> that's the project
- `-p|--project` flag overrides (searches for matching `project.name` under org tree)

No auto-creation. `install` and `init` are the explicit creation commands.

## Commands

### `ateam install [PATH]`

- PATH defaults to `.`
- Creates `.ateamorg/` with defaults and empty agent dirs
- Errors if exists

### `ateam init [PATH] [--source PATH] [--git-remote URL] [--name NAME] [--agent AGENT_LIST]`

- PATH defaults to `.`
- Creates `.ateam/` with config.toml
- Requires valid org discoverable
- Name defaults to relative path from org parent to cwd
- Source defaults to PATH
- Git repo auto-discovered; remote from `git config`
- Agents: listed in `--agent` are enabled, rest disabled
- Errors if `.ateam/` exists or duplicate name

### `ateam update [-q|--quiet] [--diff]`

- Replaces `update-prompts`
- Updates `.ateamorg/defaults/` from binary
- Always prints changed files
- `--diff` is default unless `--quiet`

### `ateam projects`

- Requires valid org
- Searches from org parent for all `.ateam/config.toml`
- Prints table: Project, Path, Source Dir, Git Repo Dir, Git Remote

### `ateam env`

- Shows org path, project path/name, source, git info, enabled agents, report/review ages

### `ateam report` / `ateam review`

- Behavior unchanged
- Use new resolution: org dir for prompts, project dir for config/results

### Global flags

All commands (except `install`): `-o|--org` and `-p|--project`

## Internal Architecture Changes

| Package | Change |
|---------|--------|
| `internal/config` | New Config struct: Project, Git, Report, Review, Code, Agents sections |
| `internal/root` | Rewrite resolve.go: `FindOrg()`, `FindProject()`, `ResolvedEnv` struct |
| `internal/root` | Split init.go: `InstallOrg(path)`, `InitProject(path, opts)` |
| `internal/prompts` | embed.go: add code_prompt.md, report_commissioning_prompt.md, rename files |
| `internal/prompts` | prompts.go: 3-level fallback, new `AssembleCodePrompt()` |
| `cmd/` | root.go: persistent flags, new commands, remove update-prompts |
| `cmd/` | install.go: creates .ateamorg |
| `cmd/` | init.go: new flags, new config format |
| `cmd/` | New: update.go, projects.go |
| `cmd/` | Delete: update_prompts.go |

### Embedded defaults renames

- `report_instructions.md` -> `report_prompt.md`
- `apply_instructions.md` -> `code_prompt.md`

## Testing

- Go tests using `./test_data/` for fixtures
- Unit tests for `FindOrg`, `FindProject`, 3-level fallback
- Integration tests covering spec examples (monorepo subdir, external project, remote-only)
- No migration from old `.ateam` format (POC)
