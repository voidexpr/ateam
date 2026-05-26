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

### 2. Write the setup overview

Create a concise project overview at `{{PROJECT_FULL_PATH}}/.ateam/setup_overview.md`. This file is used as a reference during setup and is not included in agent prompts (agents read the codebase directly, and project context is maintained in CLAUDE.md or similar files).

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

Prefer the dotted `collection.role` set (legacy single-name roles like `security` or `refactor_small` are kept on disk but deprecated — don't enable them).

- **Always on**: `code.structure` (project-wide structural quality), `code.bugs` (bug hunt), `code.recent` (review the last few commits)
- **On if tests exist or should**: `test.gaps` (coverage holes), `test.recent` (tests for recent changes)
- **On if tests exist and are non-trivial**: `test.quality` (flakiness, weak assertions, over-mocking)
- **On if external docs exist**: `docs.external` (README, install, public API)
- **On if internal docs matter**: `docs.internal` (architecture, internal protocols, agent-facing instructions)
- **On for production apps**: `project.security`, `project.production_ready`
- **On if dependencies exist**: `project.dependencies` (package.json, go.mod, Cargo.toml, etc.)
- **On for cross-cutting design review**: `design.architecture`
- **On for build/test/lint foundations**: `project.automation`
- **Database role**: `database.schema` only if a database with a managed schema is clearly used
- **Off for small/early or maintenance-only projects**: `test.quality`, `project.production_ready`, `design.architecture`; consider `project.maintenance` instead for dormant projects
- **Off by default unless explicitly opted in**: `critic.*`, `perf.*`, `docs.followable`, `test.blackbox`, `project.discover_cmd`

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
