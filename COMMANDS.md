# ATeam Commands

Full command reference for the `ateam` CLI. See [README.md](README.md) for an overview and quick start, [CONFIG.md](CONFIG.md) for configuration files and runtime config, and [ISOLATION.md](ISOLATION.md) for sandbox and container setup.

## Global Flags

All commands accept:

| Flag | Short | Description |
|------|-------|-------------|
| `--org PATH` | `-o` | Organization path override (skips auto-discovery) |
| `--project PATH` | `-p` | Project path override (skips auto-discovery). |
| `--work-dir PATH` |       | Agent working directory (overrides the project-aware default). |

`report`, `code`, `review`, `verify`, and `all` require their work-dir to be inside a git repo or worktree; `exec` and `parallel` work in any directory.

### `--project` and `--work-dir` together

The agent's working directory is decided in this order:

1. `--work-dir PATH` if set — always wins.
2. Otherwise, when cwd is inside the project tree (parent of `.ateam/`): agent runs at the **project root**, regardless of which subdir you invoked from. This is the git-style default — `cd subdir && ateam report` operates on the whole project, just like `git status` does.
3. Otherwise (cwd is outside the project tree, e.g. `--project ../other`): agent runs in **cwd**.

Common patterns:

```bash
# From any subdir of the project: agent runs at the project root.
cd ~/work/myproj/some/subdir
ateam report                                      # → agent cwd = ~/work/myproj

# Worktree: cwd is outside the project tree, so agent runs in the worktree.
cd ~/work/myproj-feat-foo
ateam --project ~/work/myproj report              # → agent cwd = ~/work/myproj-feat-foo

# Explicit override.
ateam report --work-dir ~/work/myproj/services/billing
```

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
ateam init --name myproject --role test.gaps,project.security
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
| `--debug` | Print step-by-step progress to stderr |

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
ateam report --roles project.security,test.gaps
ateam report --roles all --extra-prompt "Focus on the API layer"
ateam report --roles all --dry-run
ateam report --rerun-failed              # re-run only roles that failed last time
ateam report --rerun-failed --dry-run    # preview which roles would be rerun
ateam report --auto-roles                # let a planner agent pick the role list
ateam report --auto-roles --plan-only    # print the recommendation, don't run
```

| Flag | Description |
|------|-------------|
| `--roles LIST` | Comma-separated role list, or `all` (default: all enabled roles) |
| `--extra-prompt TEXT` | Additional instructions appended to every role's prompt (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`) |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout per role (overrides `config.toml`) |
| `--parallel N` | Max number of roles to run in parallel (overrides `config.toml`) |
| `--max-budget-usd USD` | Per-role USD spend cap (claude-only; warns on codex) |
| `--max-budget-usd-batch USD` | Stop dispatching new roles once batch cost crosses this USD |
| `--print` | Print reports to stdout after completion |
| `--rerun-failed` | Re-run only roles that failed in the last report round (mutually exclusive with `--roles`) |
| `--dry-run` | Print computed prompts without running roles |
| `--ignore-previous-report` | Do not include the role's previous report in the prompt |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--force` | Run even if the same action+role is already running |
| `--verbose` | Print agent and docker commands to stderr |
| `--review` | Run review automatically after reports complete |
| `--auto-roles` | Spawn a planner agent (`defaults/prompts/report_auto_roles.prompt.md`) that inspects git history since the last review, prior reports, and the latest code-cycle execution report, then picks a short role list. Mutually exclusive with `--roles` and `--rerun-failed`. |
| `--plan-only` | With `--auto-roles`: print the planner's rationale and recommended commands, then exit before running any reports. |

### `ateam review`

Have the supervisor read role reports and produce a prioritized decisions document.

By default review only feeds reports from currently-enabled roles into the supervisor prompt. Use `--all` to include disabled roles too, `--roles` to restrict to a specific subset, and `--max-age` to drop stale reports. When all filters together leave zero reports, review exits non-zero with a per-step funnel breakdown.

```bash
ateam review
ateam review --extra-prompt "This is a production financial app"
ateam review --prompt @custom_review.md
ateam review --roles project.security,project.dependencies        # only these reports
ateam review --all                         # include disabled roles' reports
ateam review --max-age 2h                  # drop reports older than 2h
ateam review --dry-run
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions appended to the supervisor prompt (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt (text or `@filepath`) |
| `--prompt TEXT` | Custom prompt replacing the default supervisor role entirely (text or `@filepath`) |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout (overrides `config.toml`) |
| `--roles ROLE,...` | Limit review to these roles' reports (default: all enabled roles) |
| `--all` | Include reports from roles disabled in `config.toml` |
| `--max-age DURATION` | Drop reports older than this. Accepts stdlib durations (`30m`, `2h30m`, `90s`) and plain `Nd` (e.g. `1d`, `7d`) |
| `--max-budget-usd USD` | USD spend cap for the supervisor (claude-only; errors on codex) |
| `--print` | Print review to stdout after completion |
| `--dry-run` | Print computed prompt and list reports without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--force` | Run even if the same action+role is already running |
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
| `--review TEXT` | Review content (text or `@filepath`; defaults to `.ateam/shared/review/review.md`) |
| `--management TEXT` | Management prompt override (text or `@filepath`) |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt (text or `@filepath`) |
| `--profile NAME` | Profile for sub-runs (passed to `ateam exec --profile`) |
| `--agent NAME` | Agent for sub-runs (passed to `ateam exec --agent`) |
| `--supervisor-profile NAME` | Profile for the supervisor itself |
| `--supervisor-agent NAME` | Agent for the supervisor itself |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override for the supervisor and every sub-run; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort for the supervisor and every sub-run, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout in minutes (overrides `config.toml`; default 120) |
| `--max-budget-usd USD` | USD spend cap for the supervisor and every sub-run (claude-only) |
| `--max-budget-usd-batch USD` | Stop spawning new sub-runs once the code batch crosses this USD |
| `--print` | Print output to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--verbose` | Print agent and docker commands to stderr |
| `--tail` | Stream live output from supervisor and sub-runs |
| `--force` | Run even if the same action is already running |
| `--no-verify` | Skip the verify phase that normally runs after code completes |

### `ateam verify`

Have the supervisor inspect commits made by the most recent `ateam code` run, look for logical bugs, broken or missing tests, and risky changes, then run the project's test suite and record findings in a verification report.

`ateam code` and `ateam all` chain verify automatically; run this command directly to re-verify, or pass `--no-verify` to skip the chained run.

```bash
ateam verify
ateam verify --extra-prompt "Pay extra attention to migrations"
ateam verify --dry-run
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt (text or `@filepath`) |
| `--timeout MINUTES` | Timeout in minutes (overrides `config.toml`) |
| `--print` | Print verification report to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--max-budget-usd USD` | USD spend cap for the supervisor (claude-only; errors on codex) |
| `--verbose` | Print agent and docker commands to stderr |
| `--force` | Run even if the same action is already running |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--container-name NAME` | Override container name (for docker-exec containers) |

### `ateam all`

Run the full pipeline sequentially: report → review → code → verify. Pass `--no-verify` to stop after the code phase.

`--roles` applies to both the report and review phases (and never to the code phase). `--all` and `--max-age` only affect review — report always runs only on enabled roles, since producing fresh reports for disabled roles defeats the purpose of disabling them.

```bash
ateam all
ateam all --extra-prompt "Focus on security"
ateam all --roles code.structure,test.gaps   # report+review only those roles
ateam all --all                                  # include disabled roles' stale reports in review
ateam all --max-age 2h                           # review drops reports older than 2h
ateam all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
ateam all --auto-roles                           # planner picks the role list before the pipeline runs
ateam all --auto-roles --plan-only               # print the recommendation, don't execute the pipeline
```

| Flag | Description |
|------|-------------|
| `--extra-prompt TEXT` | Additional instructions passed to all phases (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of every phase's assembled prompt (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of every phase's assembled prompt (text or `@filepath`) |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--model MODEL` | Model override applied to every phase; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort applied to every phase, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--max-budget-usd USD` | Per-agent USD spend cap applied to every phase (claude-only; warns on codex) |
| `--timeout MINUTES` | Per-phase timeout (overrides config) |
| `--parallel N` | Max parallel report roles (overrides config `max_parallel`) |
| `--roles ROLE,...` | Limit report and review to these roles (default: all enabled roles). Does not affect the code phase. |
| `--all` | Include reports from roles disabled in `config.toml` (review phase only). |
| `--max-age DURATION` | Drop reports older than this in the review phase (e.g. `2h`, `30m`, `1d`). |
| `--profile NAME` | Profile for code sub-runs (passed to `ateam code --profile`) |
| `--report-profile NAME` | Override profile for the report phase |
| `--report-agent NAME` | Override agent for the report phase (uses 'none' container) |
| `--supervisor-profile NAME` | Override profile for the supervisor (review + code management) |
| `--supervisor-agent NAME` | Override agent for the supervisor (review + code management) |
| `--code-profile NAME` | Override profile for code sub-runs (overrides `--profile`) |
| `--code-agent NAME` | Override agent for code sub-runs (uses 'none' container) |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--quiet`, `-q` | Suppress output printing |
| `--verbose` | Print agent and docker commands to stderr |
| `--no-verify` | Skip the verify phase that normally runs after code |
| `--auto-roles` | Spawn a planner agent before phase 1 that picks the role list based on git history, prior reports, and the last code-cycle execution report. Mutually exclusive with `--roles`. |
| `--plan-only` | With `--auto-roles`: print the planner's rationale and recommended commands, then exit before running any phase. |

### `ateam secret`

Manage secrets (API keys). Secrets are stored in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager) or plain `.env` files.

You can obtain a long-lived token for claude code with:
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

Agents declare required secrets via `required_env` in `runtime.hcl`. Scopes (`global`, `org`, `project`) match the three storage tiers; the store always beats the process environment for the same key.

Use `ateam env` to see every configured credential (including shadowed ones) and which the default agent will use. Use `ateam exec --dry-run` for the per-invocation view.

For the full priority chain, alternatives handling (`A|B` syntax), credential isolation, validation rules, and how secrets reach containers, see [ISOLATION.md → Secrets](ISOLATION.md#secrets).

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

**`--copy-in`** copies `.claude/`, `.claude.json`, and `secrets.env` (if present) into the container. **Not recommended for production use** — copying credentials breaks OAuth refresh token rotation. When one container refreshes its token, the other's copy is revoked. Use the shared mount approach instead (see [ISOLATION.md](ISOLATION.md)). Can be useful for one-time experimentation.

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

### `ateam exec`

Run an agent with a prompt. Can run standalone (just needs `.ateamorg/`) or within a project.

Prompt sources, in order of precedence:
- the positional argument: literal prompt text, `@PATH` to read a file, or `-` / `@-` to read stdin until EOF
- if no argument is given **and** stdin is piped/redirected, stdin is read automatically

```bash
ateam exec "say hello"
ateam exec "Analyze the auth module" --role project.security
ateam exec "test" --profile docker
ateam exec @prompt_file.md
echo "explain this code" | ateam exec            # auto-detected
git diff | ateam exec --role critic.engineering  # auto-detected
echo "still works" | ateam exec -                # explicit "-"
```

When used with `--agent codex-tmux` the prompt has an extra shape: the first line may be a `/slash` command and subsequent lines are tmux keystroke sequences (e.g. `2 Enter`, `Down Down Enter`) sent after the slash command's submenu appears. See [`codex-tmux` in CONFIG.md](CONFIG.md#codex-tmux-experimental) for the working example and the picker workaround.

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role to run (optional — requires project context) |
| `--action NAME` | Action label recorded for this run (default `exec`). Free-form; affects `ps`/template vars/labels, not output promotion |
| `--profile NAME` | Runtime profile to use |
| `--agent NAME` | Agent name from runtime.hcl (mutually exclusive with --profile) |
| `--model MODEL` | Model override |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--agent-args "ARGS"` | Extra args passed to the agent CLI |
| `--extra-prompt TEXT` | Additional instructions appended after the main prompt (text or `@filepath`) |
| `--batch ID` | Group related agent_execs |
| `--max-budget-usd USD` | Per-agent USD spend cap (claude-only; errors on codex) |
| `--max-budget-usd-batch USD` | Abort if `--batch` already exceeds this USD before starting |
| `--no-stream` | Disable progress updates on stderr |
| `--no-summary` | Disable cost/duration/tokens summary |
| `--quiet` | Disable both streaming and summary |
| `--dry-run` | Print resolved command, secrets, container config, and prompt without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--force` | Run even if the same action+role is already running |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam parallel`

Run multiple agents in parallel, each with its own prompt. All execs share a single runner instance and batch for unified cost tracking.

```bash
ateam parallel "analyze auth module" "analyze payment module"
ateam parallel @task1.md @task2.md @task3.md --labels auth,payment,users
ateam parallel "task A" "task B" --max-parallel 1 --common-prompt-first @context.md
ateam parallel "task A" "task B" --dry-run
```

Each positional argument is a prompt (text or `@filepath`). Agent execs run concurrently up to `--max-parallel`, with a live ANSI progress table showing status, tool calls, and elapsed time per exec.

| Flag | Description |
|------|-------------|
| `--labels LIST` | Comma-separated names for each prompt (must match prompt count; default: `agent-1`, `agent-2`, ...) |
| `--max-parallel N` | Maximum concurrent agent execs (default: 3) |
| `--common-prompt-first TEXT` | Text or `@filepath` prepended to every prompt |
| `--common-prompt-last TEXT` | Text or `@filepath` appended to every prompt |
| `--batch ID` | Custom batch name (default: `parallel-TIMESTAMP`) |
| `--profile NAME` | Runtime profile |
| `--agent NAME` | Agent name from runtime.hcl (shortcut, uses 'none' container) |
| `--model MODEL` | Model override |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout per agent exec |
| `--max-budget-usd USD` | Per-agent USD spend cap (claude-only; warns on codex) |
| `--max-budget-usd-batch USD` | Stop dispatching new agents once batch cost crosses this USD |
| `--no-progress` | Suppress ANSI progress table (use plain line output) |
| `--print` | Print exec outputs to stdout after completion |
| `--dry-run` | Print assembled prompts without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--verbose` | Print agent and docker commands to stderr |
| `--force` | Run even if the same action is already running |

**Batch**: All execs are grouped under a single batch (visible in `ateam cost`, `ateam ps`, and `ateam serve`). Use `--batch` to set a custom name or let it auto-generate as `parallel-TIMESTAMP`.

**Common prompts**: Use `--common-prompt-first` and `--common-prompt-last` to inject shared context. The final prompt for each exec is: `common-first + "\n\n" + prompt + "\n\n" + common-last`.

**Output**: Progress and status go to stderr. With `--print`, exec outputs are printed to stdout in submission order, each preceded by a label header (omitted for single-exec runs). This makes it composable with downstream tools.

**Logs**: Each exec's logs are stored under `logs/parallel/{label}/` in the project or org directory.

**Progress table columns**: `ID, LABEL, STATUS, EstTOKENS, TURNS, DETAILS`. `EstTOKENS` is the running input+output token count for each task. While a task is live it is an *estimate* built from the per-turn usage reported in the stream (the final total only arrives on the agent's terminal result event); once the task finishes it reflects the authoritative total from that event. The column exists so a crash or timeout before the terminal event still gives visibility into how much the task consumed. The same table is also rendered by `ateam report`.

### `ateam prompt`

Resolve and print the full prompt for a role or supervisor without running it. Default mode prints the assembled prompt body. Two inspection modes (`--paths`, `--inline-paths`) trace where each part of the prompt came from — useful when debugging an override or wondering whether a project-level fragment is taking effect.

```bash
ateam prompt --role project.security --action report                   # print the assembled prompt
ateam prompt --supervisor --action review
ateam prompt --role project.security --action report --extra-prompt "Focus on auth"

ateam prompt --role project.security --action report --paths           # tabular per-section breakdown
ateam prompt --role project.security --action report --inline-paths    # full prompt with per-section anchor/path headers
```

`--paths` example output:

```
Assembly for "report/project.security" (5 sections)

SLOT             ANCHOR      PATH                                                LAST MODIFIED             EST. TOKENS
root_pre         [embedded]  defaults/prompts/_pre.context.md                    05/28 (just now) (build)  660
dir_pre:report   [embedded]  defaults/prompts/report/_pre.intro.md               05/28 (just now) (build)  501
role_main        [embedded]  defaults/prompts/report/project.security.prompt.md  05/28 (just now) (build)  568
role_post        [project]   .ateam/prompts/report/project.security.post.extra.md  03/18 (70d ago)         39
dir_post:report  [embedded]  defaults/prompts/report/_post.format.md             05/28 (just now) (build)  1.0K
TOTAL                                                                                                      2.8K
```

`--inline-paths` prints the same sections in assembly order, each preceded by a metadata header so you can visually trace any paragraph of the prompt back to its source file. Headers are not markdown; the output is for a human, not for feeding to an agent.

| Flag | Description |
|------|-------------|
| `--role ROLE` | Role name (mutually exclusive with `--supervisor`) |
| `--supervisor` | Generate supervisor prompt instead of role prompt |
| `--action ACTION` | Action type: `report` or `code` for roles; `review`, `code`, or `verify` for supervisor **(required)** |
| `--extra-prompt TEXT` | Additional instructions (text or `@filepath`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt (text or `@filepath`) |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt (text or `@filepath`) |
| `--no-project-info` | Omit the ATeam Project Context section |
| `--ignore-previous-report` | Do not include the role's previous report |
| `--paths` | Show the per-section breakdown table (slot / anchor / path / last modified / est. tokens). No prompt body printed. |
| `--inline-paths` | Print the full prompt with each section preceded by an anchor/path/mod-time/tokens header. Troubleshooting view, not for agent consumption. |

### `ateam env`

Show the current environment: organization, runtime config, project, and role status.

| Flag | Description |
|------|-------------|
| `--claude-sandbox` | Print the generated Claude sandbox settings JSON for the default profile |
| `--print-org` | Print the absolute path to the org directory |

### `ateam project-info [PATH]`

Emit a small set of generic, language- and build-system-agnostic facts about a git repository: top-level entries, tracked-file count, recent commits, docs at the root, detected manifest files (`go.mod` / `package.json` / `Cargo.toml` / `pyproject.toml` / `Makefile` / …), and HEAD plus working-tree status.

The data is read-only and depends only on `git`. The output is intended as a stable, scriptable summary; downstream callers can consume the JSON form via `--format json`.

```bash
ateam project-info                          # markdown summary for cwd's git repo
ateam project-info /path/to/repo            # against another directory
ateam project-info --format json            # machine-readable
ateam project-info --format json | jq .head_hash
```

| Flag | Description |
|------|-------------|
| `--format` | Output format: `markdown` (default) or `json` |

### `ateam inspect [ID...]`

Show the ps summary and log files for one or more agent runs. Select runs by ID, batch, or shorthand flags.

```bash
ateam inspect 42
ateam inspect 42 43
ateam inspect --last
ateam inspect --last-report
ateam inspect --last-report --auto-debug
ateam inspect --last --auto-debug --extra-prompt "focus on the timeout"
```

| Flag | Description |
|------|-------------|
| `--last` | Select the most recent run (alias for `--last-run`) |
| `--last-run` | Select the most recent run |
| `--last-report` | Select all execs from the last report batch |
| `--last-review` | Select the last review run |
| `--last-code` | Select all execs from the last code session |
| `--batch NAME` | Select all runs in a batch |
| `--auto-debug` | Launch an agent in streaming mode to investigate the selected runs |
| `--extra-prompt TEXT` | Additional instructions appended to the auto-debug prompt (text or `@filepath`) |
| `--profile NAME` | Profile for the auto-debug agent |
| `--agent NAME` | Agent for the auto-debug run |

The debug prompt is composed via the v1 assembler (`prompts/exec_debug.prompt.md`). Debug reports are saved to `.ateam/logs/supervisor/`.

For each row, `inspect` prints the same metadata + `To resume run:` block that `ateam resume` produces (when the agent is resumable — `claude`, `codex`, `codex-tmux` — and a session id is recoverable from the stream file), followed by a `Files:` listing of every log + runtime artifact for that run. The resume block honors `ATEAM_RESUME_*_CMD` overrides (see [`ateam resume`](#ateam-resume-exec_id) below) so the printed command matches what would actually run.

### `ateam resume [EXEC_ID]`

Resume a previous agent run as an interactive session. The session id is read on demand from the run's `*_stream.jsonl` (no schema changes). The resumed session runs **outside** ateam — no `agent_execs` row, no sandbox, no tracking — and picks up where the original left off.

Supported agents: `claude`, `codex`, `codex-tmux`. Codex and codex-tmux share the same on-disk session id (codex rollout JSONL) and the same `codex` CLI, so resume is identical for both.

```bash
ateam resume 191             # print session id and the resume command
ateam resume --last          # most recent claude / codex / codex-tmux run
ateam resume 191 --launch    # exec into "claude --resume <id>" (or "codex resume ...")
```

| Flag | Description |
|------|-------------|
| `--last` | Resume the most recent `claude`, `codex`, or `codex-tmux` run instead of taking an `EXEC_ID` |
| `--launch` | Replace the current process with the resume command (uses `syscall.Exec`) |

Resume command per agent:

| Agent | Command (under `--launch`) |
|-------|----------------------------|
| `claude` | `claude --resume <session-id>` |
| `codex` / `codex-tmux` | `codex resume --include-non-interactive <session-id>` (the flag exposes sessions started by `codex exec --json`, which the interactive picker hides by default) |

Environment variable overrides for the resume binary:

| Env var | Default | Applies to |
|---------|---------|------------|
| `ATEAM_RESUME_CLAUDE_CMD` | `claude` | `claude` runs |
| `ATEAM_RESUME_CODEX_CMD`  | `codex`  | `codex` and `codex-tmux` runs |

Each may include extra leading arguments — the value is parsed with `strings.Fields`, the first token becomes the binary, and the remaining tokens are prepended to the resume args. Example: `ATEAM_RESUME_CLAUDE_CMD="my-claude --foo"` runs `my-claude --foo --resume <id>`.

`CLAUDE_CONFIG_DIR` resolution order:
1. The value recorded under `## Specified` in the run's `*_exec.md` (canonical — what was actually used).
2. Re-resolved from the agent's `config_dir` in the current `runtime.hcl` (best-effort fallback; the definition may have drifted since the run).

Container support:

| Container | Behavior |
|-----------|----------|
| `none` | Prints + supports `--launch` (host `claude --resume` / `codex resume`) |
| `docker-exec` | Prints session id and a `docker exec -it <name> ...` recipe; refuses `--launch` (session lives inside the long-lived container). Codex / codex-tmux runs warn that codex auth must already be set inside the container. |
| `docker` / `docker-oauth` / `docker-api` | Prints session id with a "oneshot container is gone" caveat; refuses `--launch` |

### `ateam version`

Print version, build, and system information.

```
ateam:  <version>
commit: <git-sha>
built:  <build-timestamp>
system: <os>
```

### `ateam serve`

Start a localhost web UI for browsing reports, reviews, sessions, and cost data.

| Flag | Description |
|------|-------------|
| `--port N` | Listen on a fixed port (default: remembered in `.ateam/cache/serve.port`, regenerated if the cached port is busy; also configurable in `config.toml`) |
| `--no-open` | Do not open the browser automatically |
| `--public` | Bind to `0.0.0.0` instead of `127.0.0.1` (allow access from other machines) |
| `--bind IP` | Bind to a specific IP address (e.g. `192.168.1.50`) |

### `ateam cat`

Read and format stream logs for one or more completed runs. Arguments can be numeric call IDs or file paths to JSONL stream files. Pass `--last` instead of an ID to format the most recent run.

```bash
ateam cat 42
ateam cat 42 43 44 --verbose
ateam cat --last
ateam cat .ateam/logs/42/stream.jsonl
```

| Flag | Description |
|------|-------------|
| `--last` | Format the most recent run (when no ID is given) |
| `--verbose` | Show full tool inputs and text content |
| `--no-color` | Disable color output |

### `ateam tail`

Live-stream agent output from running processes.

```bash
ateam tail                                 # all running processes
ateam tail 42 43                           # specific calls by ID
ateam tail --last                          # the most recent run
ateam tail --reports                       # current report runs
ateam tail --coding                        # current coding session
ateam tail 42 43 --final-message | jq .    # wait for finish, JSONL per run
```

| Flag | Description |
|------|-------------|
| `--last` | Tail the most recent run |
| `--reports` | Tail all current report runs |
| `--coding` | Tail the latest coding session (supervisor + sub-runs) |
| `--verbose` | Show full tool inputs and text content |
| `--no-color` | Disable color output |
| `--final-message` | Suppress the formatted stream and instead emit one JSONL line per run on stdout as each one finishes. Each line carries the call metadata (`exec_id`, `agent`, `model`, `role`, `action`, `batch`, `started_at`, `ended_at`, `duration_ms`, `is_error`, `exit_code`, `error_message`, `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `turns`) plus `final_message` (the last assistant message text). Exits non-zero if any run reported `is_error=true`. Works with any selector (IDs, `--last`, `--reports`, `--coding`, or none for all running). Designed for pipelining parallel runs: spawn N agents, then `ateam tail --final-message \| jq -r .final_message` to act on each as it completes. |

### `ateam cost`

Display aggregated cost and token usage. When run inside a project, results are filtered to that project.

### `ateam ps`

Display recent agent runs.

| Flag | Description |
|------|-------------|
| `--role ROLE` | Filter by role |
| `--action ACTION` | Filter by action (report, review, code, exec) |
| `--batch NAME` | Filter by batch |
| `--work-dir PATH` | Filter by the agent's working directory (absolute path; resolved against cwd) |
| `--pwd` | Shortcut for `--work-dir $(pwd)` |
| `--limit N` | Max rows (default 30) |
| `--git-hash` | Append GIT_START and GIT_END columns (first 6 chars of each hash) |
| `--git-branch` | Append GIT_START_BRANCH and GIT_END_BRANCH columns |

Output columns (12): `ID, STARTED, PROFILE, ACTION, ROLE, MODEL, DURATION, COST, TOKENS, STATUS, BATCH, REASON`.

### `ateam roles`

List roles configured for the current project.

| Flag | Description |
|------|-------------|
| `--enabled` | List enabled roles only |
| `--available` | List all roles with status (default) |
| `--docs` | Generate markdown documentation for built-in roles |

### `ateam projects`

List all projects discovered under the current organization.

### `ateam project-rename`

Re-register a project with its org after a directory move, or rename its state directory.

```bash
ateam project-rename                                    # re-register current project
ateam project-rename --old services/api --new backends/api  # rename after move
```

Without flags, re-registers the current project at its current location and cleans up stale registrations. With `--old` and `--new`, renames the legacy state directory under `.ateamorg/projects/`.

| Flag | Description |
|------|-------------|
| `--old PATH` | Old project path (relative to org root) |
| `--new PATH` | New project path (relative to org root) |
| `--dry-run` | Show what would be done without executing |

### `ateam export`

Export project reports as a self-contained HTML file with three tabs (Overview, Review, Code) and anchor-based navigation.

```bash
ateam export                              # writes to .ateam/ateam.html
ateam export --output report.html         # custom output path
ateam export --project "My Project"       # override display name
ateam export --ateam-project /path/to/.ateam  # export a specific project
```

| Flag | Description |
|------|-------------|
| `--output PATH` | Output file path (default: `.ateam/ateam.html`) |
| `--project NAME` | Display name override (instead of config.toml name) |
| `--ateam-project PATH` | Path to a `.ateam/` directory to export |

### `ateam update`

Update on-disk default prompts and runtime config to match the current binary.

| Flag | Description |
|------|-------------|
| `--diff` | Show diffs between on-disk and embedded prompts |
| `--quiet`, `-q` | Suppress diff output |

## Troubleshooting

### Debugging Prompts

```bash
ateam report --roles project.security --dry-run      # print prompt without running
ateam review --dry-run                       # print prompt and list reports
ateam prompt --role project.security --action report  # resolve and print a role prompt
```

### Stream Logs

```bash
ateam cat 42                    # pretty-print a completed run
ateam tail                      # live-stream all running processes
ateam tail --coding             # live-stream current coding session
```

### Where Output Goes

Each run writes to `.ateam/runtime/<exec_id>/` (the agent's scratch directory). On success, files other than `*_prompt.md` are cloned to a per-action canonical destination; the runtime copy is left in place for forensics. On failure, nothing is cloned — files stay in `runtime/<exec_id>/` and are listed in `.ateam/logs/<exec_id>/cmd.md` under `# Files Copy`.

| Action       | Canonical destination                          |
|--------------|------------------------------------------------|
| `report`     | `.ateam/shared/report/<role>/<role>.md`        |
| `review`     | `.ateam/shared/review/review.md`               |
| `verify`     | `.ateam/shared/verify/verify.md`               |
| `code`       | `.ateam/shared/code/<exec_id>/`                |
| `auto-setup` | `.ateam/shared/auto_setup/auto_setup.md`       |
| `exec`       | _none_ — output stays in `.ateam/runtime/<exec_id>/` |
| `parallel`   | _none_                                         |

For actions with no canonical destination, view the output with `ateam cat <exec_id>`. See [DEV.md](DEV.md) "Project on-disk layout" for the full per-run layout.

### Run Artifacts

| Path                                  | Content                                                      |
|---------------------------------------|--------------------------------------------------------------|
| `.ateam/logs/<exec_id>/cmd.md`        | Run details, command, env, settings, `# Files Copy` log      |
| `.ateam/logs/<exec_id>/stderr.out`    | Captured stderr                                              |
| `.ateam/logs/<exec_id>/stream.jsonl`  | Raw agent stream events                                      |
| `.ateam/logs/<exec_id>/prompt.md`     | Rendered prompt (used by `ateam resume`)                     |
| `.ateam/logs/<exec_id>/settings.json` | Rendered sandbox settings                                    |
| `.ateam/runtime/<exec_id>/`           | Agent-written output (preserved on both success and failure) |

On failure, error context lives in `cmd.md` / `stderr.out` / `stream.jsonl`; there are no per-action `*_error.md` files.

### History

The canonical copy (e.g. `.ateam/roles/<role>/report.md`, `.ateam/supervisor/review.md`) is overwritten on each successful run. Every run also archives its prompt and output to a sibling `history/` directory with a timestamp prefix, so prior versions are kept:

```bash
ls .ateam/roles/security/history/
# 2026-03-08_15-04-00.report_prompt.md
# 2026-03-08_15-04-00.report.md
```
