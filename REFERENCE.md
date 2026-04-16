# ATeam Reference

Full command reference, configuration, and troubleshooting. See [README.md](README.md) for an overview and quick start. See [CONTAINER.md](CONTAINER.md) for Docker setup and container guide.

## Global Flags

All commands accept:

| Flag | Short | Description |
|------|-------|-------------|
| `--org PATH` | `-o` | Organization path override (skips auto-discovery) |
| `--project NAME` | `-p` | Project name override (skips auto-discovery) |

## Commands

### `ateam install [PATH]`

Create a `.ateamorg/` directory with default prompts, runtime config, and Dockerfile.

```bash
ateam install              # creates .ateamorg/ in current directory
ateam install ~/projects   # creates .ateamorg/ at the given path
```

### `ateam init [PATH]`

Initialize a project by creating a `.ateam/` directory at PATH (defaults to `.`).

If no `.ateamorg/` is found, one is created in `$HOME` by default. Use `--org-create-prompt` for an interactive choice, or `--org-create PATH`.

```bash
ateam init
ateam init --name myproject --role testing_basic,security
ateam init --auto-setup                        # initialize and auto-configure
ateam init --org-home                          # auto-create .ateamorg/ in $HOME
```

| Flag | Description |
|------|-------------|
| `--name NAME` | Project name (defaults to relative path from org root) |
| `--role LIST` | Roles to enable (comma-separated; if omitted, defaults are used) |
| `--git-remote URL` | Git remote origin URL (auto-detected if omitted) |
| `--org-create PATH` | Create `.ateamorg/` at PATH if none exists |
| `--org-home` | Create `.ateamorg/` in `$HOME` if none exists |
| `--org-create-prompt` | Interactively choose where to create `.ateamorg/` |
| `--auto-setup` | Run `ateam auto-setup` after initialization |

### `ateam auto-setup`

Run the supervisor to analyze the project and auto-configure roles in `config.toml` and recommend settings. Creates `.ateam/setup_overview.md` as a setup reference (not included in agent prompts).

```bash
ateam auto-setup
ateam auto-setup --dry-run
ateam auto-setup --profile docker
```

| Flag | Description |
|------|-------------|
| `--profile NAME` | Runtime profile |
| `--agent NAME` | Agent name from runtime.hcl |
| `--timeout MINUTES` | Timeout in minutes |
| `--dry-run` | Print the prompt without running |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam report`

Run one or more roles in parallel to analyze the project and produce markdown reports.

```bash
ateam report --roles all
ateam report --roles security,testing_basic
ateam report --roles all --extra-prompt "Focus on the API layer"
ateam report --roles all --dry-run
ateam report --rerun-failed              # re-run only roles that failed last time
ateam report --rerun-failed --dry-run    # preview which roles would be rerun
```

| Flag | Description |
|------|-------------|
| `--roles LIST` | Comma-separated role list, or `all` (default: all enabled roles) |
| `--extra-prompt TEXT` | Additional instructions appended to every role's prompt (text or `@filepath`) |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout per role (overrides `config.toml`) |
| `--print` | Print reports to stdout after completion |
| `--rerun-failed` | Re-run only roles that failed in the last report round (mutually exclusive with `--roles`) |
| `--dry-run` | Print computed prompts without running roles |
| `--ignore-previous-report` | Do not include the role's previous report in the prompt |
| `--container-name NAME` | Override container name (for docker-exec or persistent containers) |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam review`

Have the supervisor read all role reports and produce a prioritized decisions document.

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
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout (overrides `config.toml`) |
| `--roles ROLE,...` | Limit coding tasks to these roles (reviews all reports but only assigns code tasks to listed roles) |
| `--print` | Print review to stdout after completion |
| `--dry-run` | Print computed prompt and list reports without running |
| `--container-name NAME` | Override container name (for docker-exec or persistent containers) |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam code`

Read the review document and execute prioritized tasks as code changes.

```bash
ateam code
ateam code --review @custom_review.md
ateam code --dry-run
```

| Flag | Description |
|------|-------------|
| `--review TEXT` | Review content (text or `@filepath`; defaults to `.ateam/supervisor/review.md`) |
| `--management TEXT` | Management prompt override (text or `@filepath`) |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--profile NAME` | Profile for sub-runs (passed to `ateam run --profile`) |
| `--supervisor-profile NAME` | Profile for the supervisor itself |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Timeout in minutes (overrides `config.toml`; default 120) |
| `--print` | Print output to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--container-name NAME` | Override container name (for docker-exec or persistent containers) |
| `--verbose` | Print agent and docker commands to stderr |
| `--tail` | Stream live output from supervisor and sub-runs |
| `--force` | Run even if the same action is already running |

### `ateam all`

Run the full pipeline sequentially: report → review → code.

```bash
ateam all
ateam all --extra-prompt "Focus on security"
ateam all --roles refactor_small,testing_basic
ateam all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions passed to all phases (text or `@filepath`) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--timeout MINUTES` | Per-phase timeout (overrides config) |
| `--roles ROLE,...` | Run only these roles in the report phase and limit coding tasks to them in review |
| `--profile NAME` | Profile for code sub-runs (passed to `ateam code --profile`) |
| `--report-profile NAME` | Override profile for the report phase |
| `--report-agent NAME` | Override agent for the report phase (uses 'none' container) |
| `--supervisor-profile NAME` | Override profile for the supervisor (review + code management) |
| `--supervisor-agent NAME` | Override agent for the supervisor (review + code management) |
| `--code-profile NAME` | Override profile for code sub-runs (overrides `--profile`) |
| `--code-agent NAME` | Override agent for code sub-runs (uses 'none' container) |
| `--quiet` | Suppress output printing |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam secret`

Manage secrets (API keys). Secrets are stored in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager) or plain `.env` files.

You can obtain a long lived token for claude code with:
```bash
claude setup-token
```

```bash
ateam secret                                  # list all required secrets
ateam secret ANTHROPIC_API_KEY                # check/set a specific secret
ateam secret ANTHROPIC_API_KEY --set          # set (reads from stdin or --value)
ateam secret ANTHROPIC_API_KEY --get          # print raw value (for scripting)
ateam secret ANTHROPIC_API_KEY --delete
ateam secret ANTHROPIC_API_KEY --storage file # force .env file backend
ateam secret --print                          # print all as KEY=VALUE (raw, for piping)
ateam secret --save-project-scope             # write all to .ateam/secrets.env
```

| Flag | Description |
|------|-------------|
| `--scope SCOPE` | Secret scope: `global` (default), `org`, or `project` |
| `--storage BACKEND` | Storage backend: `keychain` (default if available) or `file` |
| `--set` | Set the secret (reads value from stdin) |
| `--get` | Print raw value to stdout (for scripting) |
| `--value VALUE` | Secret value (alternative to stdin) |
| `--delete` | Delete the secret from the selected backend |
| `--print` | Print all (or named) secrets as raw `KEY=VALUE` to stdout |
| `--save-project-scope` | Resolve from any source and write to `.ateam/secrets.env` |

Agents declare required secrets via `required_env` in `runtime.hcl`.

**Resolution order** (secret store is authoritative):
1. Project `.ateam/secrets.env` / keychain
2. Org `.ateamorg/secrets.env` / keychain
3. Global `~/.config/ateam/secrets.env` / keychain
4. Process environment (fallback only)

If `ateam secret` has a value configured, it always wins over inherited environment variables. When alternatives exist (e.g., `ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN`), store-backed credentials are preferred over env-only ones. Competing alternatives are stripped from the agent's process environment to prevent credential confusion (e.g., Claude Code's auth priority is `ANTHROPIC_API_KEY > CLAUDE_CODE_OAUTH_TOKEN` — without stripping, the wrong credential could be used).

**Validation** runs before agent spawn for container runs and inside containers. On host without containers, validation is skipped — agents handle their own auth (interactive login, macOS Keychain). Credential isolation (stripping competing env vars) always runs regardless of context.

Use `ateam run --dry-run` to see the full credential resolution: which credentials are active, which are stripped, and their sources.

**Docker usage**: secrets in OS keychains don't cross into containers. Use `--save-project-scope` to write resolved secrets to `.ateam/secrets.env`, which is mounted into containers. Inside the container, `ateam run` resolves them from the project scope automatically.

### `ateam agent-config`

[experimental] Audit and configure Claude Code agent authentication. Default action is audit.

```bash
ateam agent-config                                        # audit (default)
ateam agent-config --audit --container my-app             # remote audit inside a container
ateam agent-config --copy-out --container my-app          # copy config from container to host
ateam agent-config --copy-in --container my-app --force   # copy config into container
ateam agent-config --setup-interactive                    # bootstrap interactive session
```

| Flag | Description |
|------|-------------|
| `--audit` | Show auth state, `claude auth status`, and mismatch detection (default action) |
| `--copy-out` | Copy `.claude/` and `.claude.json` from a container to a local directory |
| `--copy-in` | Copy `.claude/`, `.claude.json`, and `secrets.env` into a container |
| `--container NAME` | Target container (for `--copy-out`, `--copy-in`, `--audit`) |
| `--path PATH` | Local directory for agent config (default: `<ateamorg>/claude_linux_shared`) |
| `--home PATH` | Override container home directory (auto-detected by default) |
| `--force` | Overwrite existing config in container (for `--copy-in`) |
| `--copy-ateam` | Also copy ateam linux binary into the container (for `--copy-in`) |
| `--dry-run` | Show what would be copied without executing |
| `--essentials-only` | Copy only essential files (credentials, settings, plugins, skills, hooks, backups) |
| `--setup-interactive` | Bootstrap an interactive Claude session from a saved refresh token |

**`--audit`** detects all auth sources (env vars, ateam secrets, credential files, keychain), runs `claude auth status` for ground truth, and warns on mismatches. With `--container`, runs audit remotely via `docker exec`.

**`--copy-out`** copies `.claude/` and `.claude.json` from the container. Useful for bootstrapping `<ateamorg>/claude_linux_shared` from a container where you've already logged in. Does not copy `secrets.env` (manually maintained).

**`--copy-in`** copies `.claude/`, `.claude.json`, and `secrets.env` (if present) into the container. **Not recommended for production use** — copying credentials breaks OAuth refresh token rotation. When one container refreshes its token, the other's copy is revoked. Use the shared mount approach instead (see [CONTAINER.md](CONTAINER.md)). Can be useful for one-time experimentation.

**`--setup-interactive`** bootstraps interactive Claude from a saved refresh token:
1. First time: do a browser login (`claude` → login → `/exit`), then save the refresh token with `ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set`
2. Any new container: `--setup-interactive` exchanges the refresh token for full credentials

### `ateam container-cp`

Copy the ateam linux binary into a running Docker container.

```bash
ateam container-cp --container-name my-app-dev
ateam container-cp --profile my-app
```

| Flag | Description |
|------|-------------|
| `--container-name NAME` | Target container name (supports partial matching) |
| `--profile NAME` | Read container name from profile's `docker_container` field |
| `--dry-run` | Show what would be copied without executing |

Requires a pre-built linux binary (`make companion` produces `build/ateam-linux-amd64`).

### `ateam claude`

Run interactive claude inside a Docker container with a shared config directory. Only works on Linux inside a container.

```bash
ateam claude                                       # uses <orgDir>/claude_linux_shared
ateam claude --config-dir ~/shared_claude           # explicit path
ateam claude --raw                                  # no default flags
ateam claude -- --name "my-session"                 # pass args to claude
```

| Flag | Description |
|------|-------------|
| `--config-dir PATH` | Shared config directory (default: `<orgDir>/claude_linux_shared`) |
| `--raw` | Run claude without `--dangerously-skip-permissions` and `--remote-control` |
| `--dry-run` | Show what would be executed without running |

Sets `CLAUDE_CONFIG_DIR` so all Claude state (`.credentials.json`, `.claude.json`, `settings.json`) lives in a single mounted directory. Unsets `CLAUDE_CODE_OAUTH_TOKEN` and `ANTHROPIC_API_KEY` to avoid auth conflicts.

**Recommended mount setup** (from the host):
```bash
docker run \
  -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" \
  ...
```

### `ateam run`

Run an agent with a prompt. Can run standalone (just needs `.ateamorg/`) or within a project.

```bash
ateam run "say hello"
ateam run "Analyze the auth module" --role security
ateam run "test" --profile docker
ateam run @prompt_file.md
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role to run (optional — requires project context) |
| `--profile NAME` | Runtime profile to use |
| `--agent NAME` | Agent name from runtime.hcl (mutually exclusive with --profile) |
| `--model MODEL` | Model override |
| `--work-dir PATH` | Working directory |
| `--agent-args "ARGS"` | Extra args passed to the agent CLI |
| `--task-group ID` | Group related calls |
| `--no-stream` | Disable progress updates on stderr |
| `--no-summary` | Disable cost/duration/tokens summary |
| `--quiet` | Disable both streaming and summary |
| `--dry-run` | Print resolved command, secrets, container config, and prompt without running |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam parallel`

Run multiple agents in parallel, each with its own prompt. All tasks share a single runner instance and task group for unified cost tracking.

```bash
ateam parallel "analyze auth module" "analyze payment module"
ateam parallel @task1.md @task2.md @task3.md --labels auth,payment,users
ateam parallel "task A" "task B" --max-parallel 1 --common-prompt-first @context.md
ateam parallel "task A" "task B" --dry-run
```

Each positional argument is a prompt (text or `@filepath`). Tasks run concurrently up to `--max-parallel`, with a live ANSI progress table showing status, tool calls, and elapsed time per task.

| Flag | Description |
|------|-------------|
| `--labels LIST` | Comma-separated names for each task (must match prompt count; default: `agent-1`, `agent-2`, ...) |
| `--max-parallel N` | Maximum concurrent tasks (default: 3) |
| `--common-prompt-first TEXT` | Text or `@filepath` prepended to every prompt |
| `--common-prompt-last TEXT` | Text or `@filepath` appended to every prompt |
| `--task-group ID` | Custom task group name (default: `parallel-TIMESTAMP`) |
| `--profile NAME` | Runtime profile |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--model MODEL` | Model override |
| `--work-dir PATH` | Working directory (defaults to project source dir or cwd) |
| `--timeout MINUTES` | Timeout per task |
| `--no-progress` | Suppress ANSI progress table (use plain line output) |
| `--print` | Print task outputs to stdout after completion |
| `--dry-run` | Print assembled prompts without running |
| `--verbose` | Print agent and docker commands to stderr |
| `--force` | Run even if the same action is already running |
| `--docker-auto-setup` | Auto-setup Docker container if needed |

**Task group**: All tasks are grouped under a single task group (visible in `ateam cost`, `ateam ps`, and `ateam serve`). Use `--task-group` to set a custom name or let it auto-generate as `parallel-TIMESTAMP`.

**Common prompts**: Use `--common-prompt-first` and `--common-prompt-last` to inject shared context. The final prompt for each task is: `common-first + "\n\n" + task-prompt + "\n\n" + common-last`.

**Output**: Progress and status go to stderr. With `--print`, task outputs are printed to stdout in submission order, each preceded by a label header (omitted for single-task runs). This makes it composable with downstream tools.

**Logs**: Each task's logs are stored under `logs/parallel/{label}/` in the project or org directory.

### `ateam prompt`

Resolve and print the full prompt for a role or supervisor without running it.

```bash
ateam prompt --role security --action report
ateam prompt --supervisor --action review
ateam prompt --role security --action report --extra-prompt "Focus on auth"
```

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role name (mutually exclusive with `--supervisor`) |
| `--supervisor` | Generate supervisor prompt instead of role prompt |
| `--action ACTION` | Action type: `report` or `code` for roles; `review` or `code` for supervisor **(required)** |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--no-project-info` | Omit the ATeam Project Context section |
| `--ignore-previous-report` | Do not include the role's previous report |

### `ateam env`

Show the current environment: organization, runtime config, project, and role status.

| Flag | Description |
|------|-------------|
| `--claude-sandbox` | Print the generated Claude sandbox settings JSON for the default profile |

### `ateam inspect [ID...]`

Show the ps summary and log files for one or more agent runs. Select runs by ID, task group, or shorthand flags.

```bash
ateam inspect 42
ateam inspect 42 43
ateam inspect --last-run
ateam inspect --last-report
ateam inspect --last-report --auto-debug
ateam inspect --last-run --auto-debug-prompt
```

| Flag | Description |
|------|-------------|
| `--last-run` | Select the most recent run |
| `--last-report` | Select all tasks from the last report batch |
| `--last-review` | Select the last review run |
| `--last-code` | Select all tasks from the last code session |
| `--task-group NAME` | Select all runs in a task group |
| `--auto-debug` | Launch an agent in streaming mode to investigate the selected runs |
| `--auto-debug-prompt` | Print the auto-debug prompt without executing |
| `--profile NAME` | Profile for the auto-debug agent |
| `--agent NAME` | Agent for the auto-debug run |

The debug prompt uses the standard 3-level fallback (`supervisor/task_debug_prompt.md`). Debug reports are saved to `.ateam/logs/supervisor/`.

### `ateam version`

Print version, build, and system information.

```
ateam:  0.1.0
commit: e8348b8-dirty
built:  2026-03-30T22:58:33Z
system: Darwin ...
```

### `ateam serve`

Start a localhost web UI for browsing reports, reviews, sessions, and cost data.

| Flag | Description |
|------|-------------|
| `--port N` | Listen on a fixed port (default: random, configurable in `config.toml`) |
| `--no-open` | Do not open the browser automatically |
| `--public` | Bind to `0.0.0.0` instead of `127.0.0.1` (allow access from other machines) |
| `--bind IP` | Bind to a specific IP address (e.g. `192.168.1.50`) |

### `ateam cat`

Pretty-print stream logs by call ID or file path.

```bash
ateam cat 42
ateam cat 42 43 44 --verbose
ateam cat .ateam/logs/roles/security/2026-03-31_stream.jsonl
```

| Flag | Description |
|------|-------------|
| `--verbose` | Show full tool inputs and text content |
| `--no-color` | Disable color output |

### `ateam tail`

Live-stream agent output from running processes.

```bash
ateam tail                  # all running processes
ateam tail 42 43            # specific calls by ID
ateam tail --reports        # current report runs
ateam tail --coding         # current coding session
```

| Flag | Description |
|------|-------------|
| `--reports` | Tail all current report runs |
| `--coding` | Tail the latest coding session (supervisor + sub-runs) |
| `--verbose` | Show full tool inputs and text content |
| `--no-color` | Disable color output |

### `ateam cost`

Display aggregated cost and token usage. When run inside a project, results are filtered to that project.

### `ateam ps`

Display recent agent runs.

| Flag | Description |
|------|-------------|
| `--role ROLE` | Filter by role |
| `--action ACTION` | Filter by action (report, review, code, run) |
| `--limit N` | Max rows (default 30) |

### `ateam roles`

List roles configured for the current project.

| Flag | Description |
|------|-------------|
| `--enabled` | List enabled roles only |
| `--available` | List all roles with status (default) |

### `ateam projects`

List all projects discovered under the current organization.

### `ateam update`

Update on-disk default prompts and runtime config to match the current binary.

| Flag | Description |
|------|-------------|
| `--diff` | Show diffs between on-disk and embedded prompts |
| `--quiet`, `-q` | Suppress diff output |

## Directory Layout

### Organization: `.ateamorg/`

Created by `ateam install`. Holds shared defaults and org-level overrides.

```
.ateamorg/
  defaults/                                    # embedded defaults written to disk
    runtime.hcl                                # runtime config (agents, containers, profiles)
    Dockerfile                                 # default Dockerfile for container builds
    report_base_prompt.md                      # shared report base instructions
    code_base_prompt.md                        # shared code base instructions
    roles/<NAME>/report_prompt.md              # per-role report prompt
    roles/<NAME>/code_prompt.md                # per-role code prompt (where available)
    supervisor/review_prompt.md                # supervisor review prompt
    supervisor/code_management_prompt.md       # supervisor code management prompt
    supervisor/auto_setup_prompt.md            # auto-setup prompt
  runtime.hcl                                  # org-level runtime config override (optional)
  Dockerfile                                   # org-level Dockerfile override (optional)
  report_base_prompt.md                        # org-level report base override (optional)
  code_base_prompt.md                          # org-level code base override (optional)
  report_extra_prompt.md                       # org-wide extra instructions for reports (optional)
  code_extra_prompt.md                         # org-wide extra instructions for code (optional)
  roles/                                       # org-level role overrides
    <NAME>/report_prompt.md
    <NAME>/report_extra_prompt.md
    <NAME>/code_prompt.md
    <NAME>/code_extra_prompt.md
  supervisor/                                  # org-level supervisor overrides
    review_prompt.md
    review_extra_prompt.md
    code_management_prompt.md
    code_management_extra_prompt.md
```

### Project: `.ateam/`

Created by `ateam init`. Self-contained: config, prompts, reports, and runtime state.

Versioned files are at the top level. Runtime artifacts (`logs/`, `state.sqlite`, `secrets.env`) are gitignored.

```
.ateam/
  .gitignore                                 # excludes state.sqlite*, logs/, secrets.env
  config.toml                                # project configuration
  setup_overview.md                           # project overview from auto-setup (not included in prompts)
  state.sqlite                               # call database [gitignored]
  secrets.env                                # project-scoped secrets [gitignored]
  runtime.hcl                                # project-level runtime override (optional)
  roles/<NAME>/
    report_prompt.md                         # role report prompt override (optional)
    report_extra_prompt.md                   # extra instructions (optional)
    code_prompt.md                           # role code prompt override (optional)
    code_extra_prompt.md                     # extra instructions (optional)
    report.md                                # latest successful report
    history/                                 # timestamped archive
  supervisor/
    review_prompt.md                         # supervisor override (optional)
    review_extra_prompt.md                   # extra instructions (optional)
    review.md                                # latest successful review
    history/
  logs/                                      # runtime logs [gitignored]
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
report_timeout_minutes = 20

[review]
timeout_minutes = 20

[code]
timeout_minutes = 120

# [serve]
# port = 8080  # fixed port for 'ateam serve' (default: random)

[roles]
security = "on"
testing_basic = "on"
refactor_small = "off"

# [supervisor]
# code_profile = "cheap"          # use a cheaper model for coding sub-runs

# [profiles]
# [profiles.roles]
# security = "docker"             # run security reports in Docker
# critical_code_reviewer = "codex" # use codex agent for this role

# [sandbox-extra]
# allow_write = ["/path/to/extra/writable/dir"]
# allow_read = ["/path/to/extra/readable/dir"]
# allow_domains = ["api.example.com"]
# unsandboxed_commands = ["make:*"]
```

### `[sandbox-extra]`

Grant additional sandbox permissions to agents running in this project. These paths and domains are merged into the Claude sandbox settings alongside the defaults from `runtime.hcl`.

| Key | Description |
|-----|-------------|
| `allow_write` | Additional filesystem paths the agent can write to. Added to `sandbox.filesystem.allowWrite`. |
| `allow_read` | Additional filesystem paths the agent can read. Added to `sandbox.filesystem.allowRead`. |
| `allow_domains` | Additional network domains the agent can reach. Added to `sandbox.network.allowedDomains`. |
| `unsandboxed_commands` | Commands that bypass the sandbox entirely. Added to `sandbox.excludedCommands`. Use `name:*` for all subcommands (e.g. `make:*`, `docker:*`). |

All paths are absolute. Use `ateam env --claude-sandbox` to inspect the final merged sandbox settings.

```toml
[sandbox-extra]
allow_write = ["/data/output", "/tmp/scratch"]
allow_read = ["/opt/shared-tools"]
allow_domains = ["api.internal.example.com", "registry.npmjs.org"]
unsandboxed_commands = ["make:*", "docker:build"]
```

### `[supervisor]`

Configure which profiles the supervisor and code phases use.

| Key | Description |
|-----|-------------|
| `default_profile` | Default profile for supervisor actions (review, code management) |
| `review_profile` | Profile override for the review phase specifically |
| `code_profile` | Profile for code sub-runs (passed to `ateam run --profile`) |
| `code_supervisor_profile` | Profile for the code management supervisor itself |

```toml
[supervisor]
code_profile = "cheap"              # use sonnet for coding sub-runs
code_supervisor_profile = "default" # use default for the supervisor
```

### `[profiles]`

Map individual roles to specific profiles or agents. This lets you run different roles with different agents or runtime configurations.

```toml
[profiles]
[profiles.roles]
security = "docker"               # run security reports in Docker
critical_code_reviewer = "codex"  # use codex agent for this role
testing_full = "docker"           # testing needs Docker for build tools
```

Values can be either a **profile name** (defined in `runtime.hcl`) or an **agent name** (also defined in `runtime.hcl`). When the value matches a known agent but not a profile, it's treated as an agent shorthand — equivalent to `--agent NAME` on the CLI.

**Profile resolution order** (first match wins):
1. CLI flag (`--profile` or `--agent`) — overrides everything
2. `[profiles.roles]` — per-role override from config.toml
3. `[supervisor]` action-specific profile (for review/code actions)
4. `[supervisor]` default_profile
5. `[project]` default_profile
6. Built-in `"default"` profile

### `[container-extra]`

Add extra Docker arguments, environment forwarding, or environment variables to container runs. Merged with the container definition from `runtime.hcl`.

| Key | Description |
|-----|-------------|
| `extra_args` | Additional `docker run` flags (e.g., `["--cpus=2", "--memory=4g"]`) |
| `forward_env` | Additional env var names to forward into containers |
| `env` | Static env vars to set inside containers (key-value map) |

```toml
[container-extra]
extra_args = ["--cpus=2"]
forward_env = ["MY_CUSTOM_TOKEN"]
```

### Custom Roles

Create a custom role by adding a directory with a `report_prompt.md` — no config.toml registration needed:

```bash
mkdir -p .ateam/roles/my_custom_role
# write your prompt
vim .ateam/roles/my_custom_role/report_prompt.md
# run it
ateam report --roles my_custom_role
```

Roles are discovered from the union of:
- Built-in defaults (embedded in the binary)
- `config.toml` `[roles]` entries
- `.ateamorg/roles/<NAME>/report_prompt.md` (org-level, shared across projects)
- `.ateam/roles/<NAME>/report_prompt.md` (project-level)

Custom roles not listed in `config.toml` default to enabled and are included in `--roles all`. Add the role to `config.toml` with `off` to exclude it.

## Prompt Configuration

All prompt files can be customized at the project level (`.ateam/`), organization level (`.ateamorg/`), or rely on built-in defaults.

To add instructions without replacing the default prompt, use `*_extra_prompt.md` files. To inspect what prompt will be used:

```bash
ateam prompt --role ROLE --action report
ateam prompt --supervisor --action review
```

### Prompt Resolution

Prompts are resolved with a 3-level fallback: **project** → **org** → **org defaults**. The first file found wins. Extra prompts are **additive** — all matching files are included.

The placeholder `{{SOURCE_DIR}}` in prompts is replaced with the project source directory path.

### Role Prompt Assembly (report and code)

```
ATeam Project Context → Base prompt → Role prompt → Extra prompts → CLI --extra-prompt
```

### Supervisor Prompt Assembly (review and code)

```
ATeam Project Context → Action prompt → Extra prompts → Reports/Review → CLI --extra-prompt
```

### Extra Prompt Locations

For roles (e.g. report):
1. `.ateamorg/report_extra_prompt.md` — org-wide
2. `.ateamorg/roles/<NAME>/report_extra_prompt.md` — org role-specific
3. `.ateam/report_extra_prompt.md` — project-wide
4. `.ateam/roles/<NAME>/report_extra_prompt.md` — project role-specific

For supervisors (e.g. review):
1. `.ateamorg/supervisor/review_extra_prompt.md` — org-level
2. `.ateam/supervisor/review_extra_prompt.md` — project-level

## Runtime Configuration

Runtime behavior is configured via `runtime.hcl` files using HCL syntax, with a 4-level resolution:

1. **Built-in defaults** — compiled into the binary
2. **Org defaults** — `.ateamorg/defaults/runtime.hcl`
3. **Org override** — `.ateamorg/runtime.hcl`
4. **Project override** — `.ateam/runtime.hcl`

Use `ateam env` to see the active resolution chain.

### Agents

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet"]
}
```

Agents support inheritance via `base`, sandbox settings, environment variables, isolated config dirs, and `required_env` for secret validation. When alternatives are declared (e.g., `required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]`), the secret store takes priority over environment variables, and competing alternatives are stripped from the agent's process environment. See [`ateam secret`](#ateam-secret) for the full resolution order.

### Template Variables

Agent args, profile extra args, container config fields, and agent env values support `{{VAR}}` placeholder substitution. Variables are resolved at execution time by the runner.

#### Available Variables

| Variable | Description | Example value |
|---|---|---|
| `{{PROJECT_NAME}}` | Project name from `config.toml` | `myproject` |
| `{{PROJECT_FULL_PATH}}` | Absolute path to project root | `/home/user/projects/myproject` |
| `{{PROJECT_DIR}}` | Last component of the project path | `myproject` |
| `{{ROLE}}` | Role ID | `security`, `supervisor` |
| `{{ACTION}}` | Action type | `report`, `run`, `code`, `review` |
| `{{TASK_GROUP}}` | Task group ID | `code-2026-03-31_06-09-39` |
| `{{TIMESTAMP}}` | Run start time | `2026-03-31_06-09-39` |
| `{{PROFILE}}` | Active profile name | `docker`, `default` |
| `{{EXEC_ID}}` | Call tracking ID (visible in `ateam ps`) | `42` |
| `{{AGENT}}` | Agent config name | `claude`, `claude-docker` |
| `{{MODEL}}` | Resolved model name | `sonnet`, `haiku` |
| `{{CONTAINER_TYPE}}` | Container type | `none`, `docker`, `docker-exec` |
| `{{CONTAINER_NAME}}` | Docker container name | `ateam-myapp-security` |

Unknown variables are left as-is (e.g. `{{UNKNOWN}}` passes through unchanged). When `EXEC_ID` is 0 (no DB tracking), it resolves to an empty string.

#### Where Templates Are Resolved

| Config field | Location | Templates supported |
|---|---|---|
| `agent.args` | `runtime.hcl` | Yes |
| `agent.env` values | `runtime.hcl` | Yes |
| `agent.config_dir` | `runtime.hcl` | Yes |
| `profile.agent_extra_args` | `runtime.hcl` | Yes |
| `profile.container_extra_args` | `runtime.hcl` | Yes (via Docker ExtraArgs) |
| Docker `ContainerName` | Computed at build time | Yes |
| Docker `ExtraVolumes` | `runtime.hcl` container config | Yes |
| Docker `Env` values | `config.toml` `[container-extra]` | Yes |
| `agent.args_inside_container` | `runtime.hcl` | Yes |
| `agent.args_outside_container` | `runtime.hcl` | Yes |
| docker-exec `docker_container` | `runtime.hcl` | Yes |
| docker-exec `WorkDir` | Computed from project paths | Yes |

Templates are **not** resolved in:
- Prompt files (use `{{SOURCE_DIR}}` which has its own separate substitution)
- Sandbox settings JSON (has its own merge mechanism)
- Dockerfile paths (use the role-based resolution chain instead)
- Precheck command args — `{{CONTAINER_NAME}}` is expanded at execution time by `RunPrecheck`, not by the general template system
- `forward_env` key names (these are env var names, not values)
- Map keys in `env` blocks (only values are resolved)
- docker-exec `exec` template — uses its own `{{CONTAINER}}` and `{{CMD}}` placeholders which are expanded at execution time by the CmdFactory (separate from general template vars)

#### Examples

Session naming for agent runs:

```hcl
agent "claude" {
  args = ["-p", "--output-format", "stream-json", "--verbose",
          "--name", "{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}"]
}
```

Per-role Claude config directory:

```hcl
agent "claude-isolated" {
  base       = "claude"
  config_dir = ".claude-{{ROLE}}"
}
```

Custom Docker hostname per role:

```hcl
profile "docker" {
  agent              = "claude-docker"
  container          = "docker"
  container_extra_args = ["--hostname", "ateam-{{PROJECT_DIR}}-{{ROLE}}"]
}
```

Passing role context as environment variables:

```hcl
agent "claude" {
  env = {
    ATEAM_ROLE   = "{{ROLE}}"
    ATEAM_ACTION = "{{ACTION}}"
  }
}
```

### Cost Estimation & Pricing

- **Claude** reports actual cost directly in its stream output. No configuration needed.
- **Codex** (and other agents) rely on a `pricing` block in `runtime.hcl` to estimate cost from token counts.

```hcl
agent "codex" {
  type    = "codex"
  command = "codex"
  args    = ["--sandbox", "workspace-write", "--ask-for-approval", "never"]
  required_env = ["OPENAI_API_KEY"]

  pricing {
    default_model = "gpt-5.3-codex"
    model "gpt-5.3-codex" {
      input_per_mtok  = 1.75
      output_per_mtok = 14.00
    }
  }
}
```

Pricing can go stale. Override at any level by redefining the agent's `pricing` block in the appropriate `runtime.hcl`.

### Containers

```hcl
container "docker" {
  type        = "docker"
  mode        = "oneshot"        # or "persistent"
  dockerfile  = "Dockerfile"
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

Docker-exec runs agents inside a user-managed container (docker-compose, devcontainer, etc.). The container name can be set via config, `--container-name` flag, or `ateam secret CONTAINER_NAME --scope project`. A built-in `docker-exec` profile uses `{{CONTAINER_NAME}}` for automatic resolution.

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"             # or "{{CONTAINER_NAME}}" for secret-based
  precheck         = ["sh", "precheck.sh", "{{CONTAINER_NAME}}"]  # command array
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
  copy_ateam       = true                     # copy ateam binary into container
  # exec           = "podman exec {{CONTAINER}} {{CMD}}"
}
```

The `precheck` field takes a command array. `{{CONTAINER_NAME}}` in args is replaced with the resolved container name at execution time. Convention-discovered scripts (`.ateam/docker-agent-precheck.sh`) are auto-wrapped as `["sh", "<path>", "{{CONTAINER_NAME}}"]`.

Devcontainers use the project's `.devcontainer/devcontainer.json` to run agents in a pre-configured environment. The agent CLI (e.g. `claude`) must be installed inside the devcontainer. Requires `@devcontainers/cli` (`npm install -g @devcontainers/cli`).

```hcl
container "devcontainer" {
  type        = "devcontainer"
  # config_path = ".devcontainer/backend/devcontainer.json"  # optional override
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

For authentication setup inside containers, see [CONTAINER.md](CONTAINER.md#running-interactive-claude-in-containers).

### Profiles

Profiles combine an agent and a container:

```hcl
profile "default" {
  agent     = "claude"
  container = "none"
}

profile "docker" {
  agent     = "claude-docker"
  container = "docker"
}

profile "cheap" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "sonnet", "--max-budget-usd", "0.50"]
}

profile "devcontainer" {
  agent     = "claude-docker"
  container = "devcontainer"
}
```

Use `--profile docker` to run in ateam's Docker, `--profile devcontainer` to run in your project's devcontainer, or `--profile cheap` for cheaper runs.

#### If run supervisor in docker

Typically only coding agents need to run inside docker so they can build and run tests in an isolated environment. Basic docker config from [README.md](README.md) is enough. But if you want the supervisor itself to run in docker and launch ateam coding agents then a Linux build of ateam must be available inside of docker. This is supported, cross-compile the Linux companion binary:

```bash
make companion    # produces build/ateam-linux-amd64
```

The binary is automatically found by ateam from `build/`. For installations without a git checkout, place `ateam-linux-amd64` next to the host `ateam` binary.

For complete Docker setup including secrets, precheck scripts, and interactive sessions, see [CONTAINER.md](CONTAINER.md).

## Troubleshooting

### Debugging Prompts

```bash
ateam report --roles security --dry-run      # print prompt without running
ateam review --dry-run                       # print prompt and list reports
ateam prompt --role security --action report  # resolve and print a role prompt
```

### Stream Logs

```bash
ateam cat 42                    # pretty-print a completed run
ateam tail                      # live-stream all running processes
ateam tail --coding             # live-stream current coding session
```

### Error Files

| File | Location | Content |
|------|----------|---------|
| `report_error.md` | `.ateam/roles/<NAME>/` | Error summary, exit code, stderr, partial output |
| `*_stderr.log` | `.ateam/logs/roles/<NAME>/` | Raw stderr |
| `*_stream.jsonl` | `.ateam/logs/roles/<NAME>/` | Raw JSONL event stream |
| `*_exec.md` | `.ateam/logs/roles/<NAME>/` | Full execution context |

Supervisor errors: `.ateam/supervisor/review_error.md` and `.ateam/supervisor/code_error.md`.

### History

Every run archives its prompt and output to `history/` with a timestamp prefix:

```bash
ls .ateam/roles/security/history/
# 2026-03-08_15-04-00.report_prompt.md
# 2026-03-08_15-04-00.report.md
```
