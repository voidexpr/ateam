# Auto-Setup: Configure ateam for this project

You are configuring ateam for a software project. Ateam runs specialized background agents that audit and improve code quality across dimensions like testing, security, refactoring, dependencies, and documentation — without changing features.

## Steps

### 1. Understand the project

Read README.md and scan the top-level directory structure. Identify:
- Language(s) and framework(s)
- Build system (Makefile, package.json, Cargo.toml, go.mod, etc.)
- Test runner and how to run tests (quick vs full)
- Main source directories
- Whether a database is involved
- Whether other services or middleware are involed
- Project maturity (prototype vs production)

### 2. Write .ateam/setup_overview.md

Create a concise project overview in `.ateam/setup_overview.md`. This file is used as a reference during setup and is not included in agent prompts (agents read the codebase directly, and project context is maintained in CLAUDE.md or similar files).

```markdown
# Project Overview

## Tech Stack
- Language: ...
- Framework: ...
- Build: ...

## Testing
- Quick: `<command for fast tests>`
- Full: `<command for all tests>`

## Structure
- `src/` — ...
- `tests/` — ...

## Notes
- <anything relevant for code quality agents>
```

### 3. Configure roles in .ateam/config.toml

Read the current `.ateam/config.toml` and update the `[roles]` section. Turn roles on/off based on what makes sense:

- **Always on**: `refactor_small` (every project benefits)
- **On if tests exist**: `testing_basic`, `testing_full` (only if the project has tests or should have them)
- **On if external docs exist**: `docs_external` (README, API docs)
- **On if internal docs matter**: `docs_internal` (code comments, architecture)
- **On for production apps**: `security`, `production_assessment`
- **On if dependencies exist**: `dependencies` (package.json, go.mod, Cargo.toml, etc.)
- **Off for small/early projects**: `testing_full`, `production_assessment`
- **Database roles**: only if a database is clearly used

Example:
```bash
# Read current config
cat .ateam/config.toml
# Edit it directly to enable/disable roles
```

### 4. Check Docker availability

Run `docker --version` to check if Docker is installed. If Docker is available, note it in the setup overview. Docker enables isolated execution via the `docker` profile but is not required.

### 5. Print summary

After making changes, print a short summary of what was configured:
- Which roles were enabled/disabled and why
- Whether Docker is available
    - if so print commands to configure agents available to run isolated in docker (claude, codex, ...)
- Suggested next command: `ateam report` or `ateam all`
