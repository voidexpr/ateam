# ATeam Commands

Full command reference for the `ateam` CLI. See [README.md](README.md) for an overview and quick start, [CONFIG.md](CONFIG.md) for configuration files and runtime config, and [ISOLATION.md](ISOLATION.md) for sandbox and container setup.

## Global Flags

All commands accept:

| Flag | Short | Description |
|------|-------|-------------|
| `--home DIR` |      | The user-wide ateam directory (default `$ATEAM_HOME`, else `~/.ateam`): lowest config layer and user state |
| `--project PATH` | `-p` | Project path override: the project root, its `.ateam/`, or any path inside it (walks up from there instead of cwd) |
| `--work-dir PATH` |       | Agent working directory (overrides the project-aware default). |
| `--sandbox-detection true\|false` |       | Toggle auto-detection of an outer non-container sandbox. Signal-driven: macOS Seatbelt (sandbox-exec probe), Linux user namespace / Seccomp / NoNewPrivs (/proc), cooperative env vars (`FENCE_SANDBOX`, `FIREJAIL_NAME`, `container=…`). Built-in default `false` — opt in when knowingly running ateam under any outer sandbox. Overrides `ateam.hcl` `sandbox_detection`. See [ISOLATION.md](ISOLATION.md#detection-of-an-outer-sandbox-or-container). |
| `--docker-detection true\|false` |       | Toggle auto-detection of `/.dockerenv` / `/run/.containerenv`. Built-in default `true`. Overrides `ateam.hcl` `docker_detection`. |
| `--skip-model-name-check` |       | Run even when the model id is neither in the agent's `pricing` table nor matches its `model_pattern`; the refusal becomes a warning. See [CONFIG.md → Agents](CONFIG.md#agents). |

`report`, `code`, `review`, `verify`, and `run-all` require their work-dir to be inside a git repo or worktree; `exec` and `parallel` work in any directory.

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

### Ad-hoc prompt wrap (`--pre-prompt` / `--post-prompt`)

Every prompt-taking command (`exec`, `parallel`, `report`, `review`, `verify`, `code`, `auto-setup`, `run-all`, `inspect`, `prompt`) accepts the same wrap flags with identical semantics:

- `--pre-prompt TEXT` (or `@filepath`) — wrapped at the very front of the assembled prompt.
- `--post-prompt TEXT` — wrapped at the very end.

Wrap order, outermost first: `--pre-prompt` → anchors → dir-level `_pre`/`_post` fragments → role-level `pre`/`post` → main body → `--post-prompt`.

To tack on "Additional Instructions" the way the older `--extra-prompt` flag did, write `--post-prompt "# Additional Instructions\n\nfocus on..."` — the heading is just a markdown convention, no magic.

[^wrap]: See [Ad-hoc prompt wrap](#ad-hoc-prompt-wrap---pre-prompt----post-prompt) for the shared wrap-order contract.

## Commands

### `ateam init [PATH]`

Create a `.ateam/` project directory at PATH (default `.`) with an `ateam.hcl` naming the project and listing every role with its status, then register the project under `~/.ateam/state/projects.hcl`. The user directory (`~/.ateam`, or `$ATEAM_HOME` / `--home`) is created on first use with a starter `ateam.hcl`.

```bash
ateam init                                             # name defaults to the directory name
ateam init --name myproject --role test.gaps,project.security
ateam init --auto-setup                                # then let an agent pick the roles
```

| Flag | Description |
|------|-------------|
| `--name NAME` | Project name: unique across your projects, `[a-z0-9._-]`. Defaults to the directory name |
| `--role LIST` | Roles to enable (comma-separated); others are set to `off` |
| `--auto-setup` | Run `ateam auto-setup` right after |
| `--agent-config CHOICE` | Where unattended agents keep their config: `home`, `project` or `none`; asked on first use of `~/.ateam` when run from a terminal (see `ateam setup`) |
| `--debug` | Print step-by-step progress |

### `ateam auto-setup`

Run the supervisor to analyze the project and auto-configure roles in `ateam.hcl` and recommend settings. Creates `.ateam/setup_overview.md` as a setup reference (not included in agent prompts).

```bash
ateam auto-setup
ateam auto-setup --dry-run
ateam auto-setup --profile docker
```

| Flag | Description |
|------|-------------|
| `--profile NAME` | Runtime profile |
| `--agent NAME` | Agent name from ateam.hcl |
| `--timeout MINUTES` | Timeout in minutes |
| `--dry-run` | Print the prompt without running |
| `--verbose` | Print agent and docker commands to stderr |

### `ateam report`

Run one or more roles in parallel to analyze the project and produce markdown reports.

```bash
ateam report --roles all
ateam report --roles project.security,test.gaps
ateam report --roles all --post-prompt "Focus on the API layer"
ateam report --roles all --dry-run
ateam report --rerun-failed              # re-run only roles that failed last time
ateam report --rerun-failed --dry-run    # preview which roles would be rerun
ateam report --auto-roles                # let a planner agent pick the role list
ateam report --auto-roles --plan-only    # print the recommendation, don't run
```

| Flag | Description |
|------|-------------|
| `--roles LIST` | Comma-separated role list, or `all` (default: all enabled roles) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from ateam.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout per role (overrides `ateam.hcl`) |
| `--parallel N` | Max number of roles to run in parallel (overrides `ateam.hcl`) |
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
ateam review --post-prompt "This is a production financial app"
ateam review --roles project.security,project.dependencies        # only these reports
ateam review --all                         # include disabled roles' reports
ateam review --max-age 2h                  # drop reports older than 2h
ateam review --dry-run
```

Override the supervisor body by dropping a `review.prompt.md` under
`.ateam/prompts/` (project anchor) or `~/.ateam/prompts/` (home anchor) —
the standard framing still composes around it.

| Flag | Description |
|------|-------------|
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from ateam.hcl (shortcut, uses 'none' container) |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout (overrides `ateam.hcl`) |
| `--roles ROLE,...` | Limit review to these roles' reports (default: all enabled roles) |
| `--all` | Include reports from roles disabled in `ateam.hcl` |
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
| `--review TEXT` | Review content (text or `@filepath`; defaults to `.ateam/shared/review.md`) |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--profile NAME` | Profile for sub-runs (passed to `ateam exec --profile`) |
| `--agent NAME` | Agent for sub-runs (passed to `ateam exec --agent`) |
| `--supervisor-profile NAME` | Profile for the supervisor itself |
| `--supervisor-agent NAME` | Agent for the supervisor itself |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override for the supervisor and every sub-run; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort for the supervisor and every sub-run, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--timeout MINUTES` | Timeout in minutes (overrides `ateam.hcl`; default 120) |
| `--max-budget-usd USD` | USD spend cap for the supervisor and every sub-run (claude-only) |
| `--max-budget-usd-batch USD` | Stop spawning new sub-runs once the code batch crosses this USD |
| `--print` | Print output to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--verbose` | Print agent and docker commands to stderr |
| `--force` | Run even if the same action is already running |

`ateam code` stops after the code phase. Run `ateam verify` afterward (or `ateam run-all` for the full pipeline) to inspect the commits and run the test suite.

`ateam code` refuses to launch when the working tree has uncommitted tracked changes (untracked files are OK): the coding phase makes commits and its recovery paths inspect git state, so starting dirty is unsafe. Commit or stash your changes first. In `ateam run-all` this short-circuits the pipeline before the verify phase runs.

`ateam code` also exits non-zero when the supervisor itself finished cleanly but its delegated work did not: any sub-run in the code batch that is still running or ended in error (e.g. `parent_terminated`, timeout), or an execution report whose Summary counts a `Failed`, `Incomplete after retry` or `Not attempted` task. A report without those counters only prints a warning. In `ateam run-all` this too short-circuits before verify.

For live output from a running `code` session, run `ateam tail --coding` in a separate terminal; the previous `--tail` flag has been removed.

### `ateam verify`

Have the supervisor inspect commits made by the most recent `ateam code` run, look for logical bugs, broken or missing tests, and risky changes, then run the project's test suite and record findings in a verification report.

Run it explicitly after `ateam code`, or rely on `ateam run-all` which always runs verify as the final pipeline phase.

```bash
ateam verify
ateam verify --post-prompt "Pay extra attention to migrations"
ateam verify --dry-run
```

| Flag | Description |
|------|-------------|
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--timeout MINUTES` | Timeout in minutes (overrides `ateam.hcl`) |
| `--print` | Print verification report to stdout after completion |
| `--dry-run` | Print the computed prompt without running |
| `--cheaper-model` | Use a cheaper model (sonnet); ignored if `--model` is also set (`--model` wins) |
| `--model MODEL` | Model override; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--profile NAME` | Runtime profile (overrides config resolution) |
| `--agent NAME` | Agent name from ateam.hcl (shortcut, uses 'none' container) |
| `--max-budget-usd USD` | USD spend cap for the supervisor (claude-only; errors on codex) |
| `--verbose` | Print agent and docker commands to stderr |
| `--force` | Run even if the same action is already running |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--container-name NAME` | Override container name (for docker-exec containers) |

### `ateam run-all`

Run the full pipeline sequentially: report → review → code → verify. Verify always runs as the final phase — to stop earlier, run the individual commands instead (e.g. `ateam report && ateam review && ateam code`).

`--roles` applies to both the report and review phases (and never to the code phase). `--all` and `--max-age` only affect review — report always runs only on enabled roles, since producing fresh reports for disabled roles defeats the purpose of disabling them.

```bash
ateam run-all
ateam run-all --post-prompt "Focus on security"
ateam run-all --roles code.structure,test.gaps   # report+review only those roles
ateam run-all --all                                  # include disabled roles' stale reports in review
ateam run-all --max-age 2h                           # review drops reports older than 2h
ateam run-all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
ateam run-all --auto-roles                           # planner picks the role list before the pipeline runs
ateam run-all --auto-roles --plan-only               # print the recommendation, don't execute the pipeline
```

| Flag | Description |
|------|-------------|
| `--pre-prompt TEXT` | Text wrapped at the very front of every phase's assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of every phase's assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--cheaper-model` | Use a cheaper model (sonnet) |
| `--model MODEL` | Model override applied to every phase; takes precedence over `--cheaper-model` |
| `--effort VALUE` | Reasoning effort applied to every phase, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--max-budget-usd USD` | Per-agent USD spend cap applied to every phase (claude-only; warns on codex) |
| `--timeout MINUTES` | Per-phase timeout (overrides config) |
| `--parallel N` | Max parallel report roles (overrides config `max_parallel`) |
| `--roles ROLE,...` | Limit report and review to these roles (default: all enabled roles). Does not affect the code phase. |
| `--all` | Include reports from roles disabled in `ateam.hcl` (review phase only). |
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
| `--scope SCOPE` | Secret scope: `global` (default, `~/.ateam/secrets.env`) or `project` (`.ateam/secrets.env`) |
| `--storage BACKEND` | Storage backend: `keychain` (default if available) or `file` |
| `--set` | Set the secret (reads value from stdin) |
| `--get` | Print raw value to stdout (for scripting) |
| `--value VALUE` | Secret value (alternative to stdin) |
| `--delete` | Delete the secret from the selected backend |
| `--print` | Print all (or named) secrets as raw `KEY=VALUE` to stdout |
| `--save-project-scope` | Resolve from any source and write to `.ateam/secrets.env` |

Agents declare required secrets via `required_env` in `ateam.hcl`. Scopes (`global`, `project`) match the two storage tiers; the store always beats the process environment for the same key.

Use `ateam env` to see every configured credential (including shadowed ones) and which the default agent will use. Use `ateam exec --dry-run` for the per-invocation view.

For the full priority chain, alternatives handling (`A|B` syntax), credential isolation, validation rules, and how secrets reach containers, see [ISOLATION.md → Secrets](ISOLATION.md#secrets).

### `ateam agent-config`

[experimental] Audit and configure Claude Code agent authentication. Default action is audit.

```bash
ateam agent-config                                        # audit (default)
ateam agent-config --audit --container my-app             # remote audit inside a container
ateam agent-config --copy-out --container my-app          # copy config from container to host
ateam agent-config --copy-in --container my-app --force   # copy config into container
```

| Flag | Description |
|------|-------------|
| `--audit` | Show auth state, `claude auth status`, and mismatch detection (default action) |
| `--copy-out` | Copy `.claude/` and `.claude.json` from a container to a local directory |
| `--copy-in` | Copy `.claude/`, `.claude.json`, and `secrets.env` into a container |
| `--container NAME` | Target container (for `--copy-out`, `--copy-in`, `--audit`) |
| `--path PATH` | Local directory for agent config (default: `~/.ateam/state/agents/claude-linux`) |
| `--home PATH` | Override container home directory (auto-detected by default) |
| `--force` | Overwrite existing config in container (for `--copy-in`) |
| `--copy-ateam` | Also copy ateam linux binary into the container (for `--copy-in`) |
| `--dry-run` | Show what would be copied without executing |
| `--essentials-only` | Copy only essential files (credentials, settings, plugins, skills, hooks, backups) |

**`--audit`** detects all auth sources (env vars, ateam secrets, credential files, keychain), runs `claude auth status` for ground truth, and warns on mismatches. With `--container`, runs audit remotely via `docker exec`.

**`--copy-out`** copies `.claude/` and `.claude.json` from the container. Useful for bootstrapping `~/.ateam/state/agents/claude-linux` from a container where you've already logged in. Does not copy `secrets.env` (manually maintained).

**`--copy-in`** copies `.claude/`, `.claude.json`, and `secrets.env` (if present) into the container. **Not recommended for production use** — copying credentials breaks OAuth refresh token rotation. When one container refreshes its token, the other's copy is revoked. Use the shared mount approach instead (see [ISOLATION.md](ISOLATION.md)). Can be useful for one-time experimentation.

To log into the config directory an unattended agent uses, run [`ateam login`](#ateam-login).

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

Requires a pre-built linux binary (`make companion` produces `build/ateam-linux-<arch>`).

### `ateam setup`

Two optional steps for unattended agents, per agent CLI found on PATH. With no flags both run; a flag selects one.

```bash
ateam setup                                   # asks: config dir, then model pin
ateam setup --agent-config home               # ~/.ateam/state/agents/<agent>, every project
ateam setup --agent-config project --agent codex   # <project>/.ateam/agents/codex, this project only
ateam setup --agent-config none               # keep inheriting ~/.claude / ~/.codex
ateam setup --model                           # propose a model pin (asks)
ateam setup --model-choice suggested          # pin the suggested model without asking
ateam setup --model-choice claude-haiku-5-5 --layer project
ateam setup --refresh-models                  # fetch current prices and suggested models
```

| Flag | Description |
|------|-------------|
| `--agent-config CHOICE` | Config dir: `home`, `project` or `none`; without it and with a terminal, `ateam setup` asks |
| `--agent LIST` | Agent CLIs to configure (`claude`, `codex`); default: every one on PATH |
| `--model` | Propose a model pin for unattended runs (asks) |
| `--model-choice CHOICE` | Pin without asking: `suggested`, `none`, or a model id |
| `--layer LAYER` | Where `--model` writes the pin: `home` (default, every project) or `project` |
| `--refresh-models` | Fetch the current pricing tables and suggested models into `~/.ateam/ateam.hcl` |

**Model pin.** With nothing pinned the agent CLI picks its own default, often the top model, and burns the weekly limit. The pricing table's `suggested_model` is the tier below the top in the current generation (`claude-sonnet-5-5` for claude); `ateam setup --model` shows it with its price next to the alternatives and what runs today, and writes `model = "..."` on the agent block of the chosen layer. An agent already pinned by an `ateam.hcl` layer is left alone unless `--model-choice` says otherwise.

**Price tables.** Prices and suggested models are embedded in the binary with a `pricing_as_of` date, refreshed at release time. `--refresh-models` fetches the current `defaults/ateam.hcl` from the ateam repository (`ATEAM_DEFAULTS_URL` overrides the source) and writes its pricing blocks into a marked section of `~/.ateam/ateam.hcl`, replaced in place on the next refresh. `ateam env` warns when the tables are older than 90 days.

Why: the interactive Claude or Codex install carries hooks, plugins, MCP servers and notifications that are noise or interfere in an unattended run, and a separate directory keeps unattended sessions and history out of the interactive one. The choice is written as `config_dir` on the agent block of the chosen layer's `ateam.hcl` (derived agents inherit it through `base`); nothing is copied, `ateam login` creates the credentials. `ateam init` asks the same question the first time it creates `~/.ateam`, and takes `--agent-config` for scripts. An agent that already has a `config_dir` is reported and left alone.

### `ateam login`

Log into the agent CLI that unattended runs use, in the config directory they use. Works on any OS.

```bash
ateam login                        # the default profile's agent
ateam login --profile codex
ateam login --agent claude-high
ateam login --dry-run              # show the command and env, run nothing
ateam login -- --help              # pass args to the CLI's login command
```

| Flag | Description |
|------|-------------|
| `--profile NAME` | Pick the agent through a profile (default: the project's default profile) |
| `--agent NAME` | Pick an agent block directly |
| `--dry-run` | Show what would be executed without running |

The agent is chosen the way a run chooses it (`--agent`, else `--profile`, else `ateam.hcl`). Its `config_dir` is resolved like the runner does (`~` expanded, relative paths under `.ateam/` or `~/.ateam/`) and exported as `CLAUDE_CONFIG_DIR` (claude) or `CODEX_HOME` (codex); with no `config_dir`, the CLI's own default directory is used. The agent's `env` map is applied and the credential env vars that would shadow a stored login (`CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) are removed, then `claude auth login` or `codex login` runs, followed by the CLI's auth status. The first line of output names the agent and what selected it, so re-login after an expiry needs no guessing.

### `ateam claude`

Run interactive claude inside a Docker container with the unattended agent's config directory. Only works on Linux inside a container; on the host use `ateam login`.

```bash
ateam claude                                       # default profile's agent config_dir
ateam claude --config-dir ~/shared_claude           # explicit path (e.g. a mounted dir)
ateam claude --profile codex                        # error: not a claude agent
ateam claude --raw                                  # no default flags
ateam claude -- --name "my-session"                 # pass args to claude
```

| Flag | Description |
|------|-------------|
| `--config-dir PATH` | Config directory (default: the resolved agent's `config_dir`; none → claude's own default) |
| `--profile NAME` / `--agent NAME` | Pick the agent, as for `ateam login` |
| `--raw` | Run claude without `--dangerously-skip-permissions` and `--remote-control` |
| `--dry-run` | Show what would be executed without running |

Sets `CLAUDE_CONFIG_DIR` so all Claude state (`.credentials.json`, `.claude.json`, `settings.json`) lives in one directory. Unsets `CLAUDE_CODE_OAUTH_TOKEN` and `ANTHROPIC_API_KEY` to avoid auth conflicts.

**Recommended mount setup** (from the host):
```bash
docker run \
  -v "$(ateam env --print-home)/state/agents/claude-linux:/home/agent/shared_claude" \
  ...
```

### `ateam exec`

Run an agent with a prompt. Can run standalone (just needs `~/.ateam/`) or within a project.

Prompt sources, in order of precedence:
- the positional argument: literal prompt text, `@PATH` to read a file, or `-` / `@-` to read stdin until EOF
- if no argument is given **and** stdin is piped/redirected, stdin is read automatically

The positional argument routes through one of three Prompt impls based on its shape and `--raw` (see [CONFIG.md → `@PATH` dispatch](CONFIG.md#path-dispatch-exec--parallel--prompt) for the full table):

- `--raw` + anything → byte-through, no template engine.
- `@foo.prompt.md` (path separator or leading `.`) → anchored composition: the file's parent dir is injected as a temporary anchor, so sibling `foo.pre.*.md` / `foo.post.*.md` / dir-level `_pre.*.md` fragments compose around the body.
- Anything else (`@foo.md`, `@-` stdin, literal text) → engine-expanded inline text. `{{namespace.key}}` directives resolve; no anchor walk.

```bash
ateam exec "say hello"
ateam exec "Analyze the auth module" --role project.security
ateam exec "test" --profile docker
ateam exec @./team_prompt.prompt.md              # anchored: sibling .pre.*.md compose
ateam exec @notes.md                             # engine expansion only, no framing
ateam exec --raw @baked.md                       # verbatim, no engine
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
| `--agent NAME` | Agent name from ateam.hcl (mutually exclusive with --profile) |
| `--model MODEL` | Model override |
| `--effort VALUE` | Reasoning effort override, passed verbatim to the agent CLI (see [Effort levels](CONFIG.md#effort-levels)) |
| `--agent-args "ARGS"` | Extra args passed to the agent CLI |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--raw` | Feed the prompt to the agent byte-for-byte: skip template-engine expansion (no `{{var}}` substitution, no dynamics, no anchor walk). Default expands `{{exec.*}}`, `{{prompt.*}}`, and other vars. |
| `--batch ID` | Group related agent_execs |
| `--max-budget-usd USD` | Per-agent USD spend cap (claude-only; errors on codex) |
| `--max-budget-usd-batch USD` | Abort if `--batch` already exceeds this USD before starting |
| `--no-stream` | Disable progress updates on stderr |
| `--no-summary` | Disable cost/duration/tokens summary |
| `--quiet` | Disable both streaming and summary |
| `--timeout DUR` | Override the run's wall-clock timeout (Go duration, e.g. `30s`, `8m`, `1h30m`; overrides `Exec.TimeoutMinutes` from `ateam.hcl`) |
| `--dry-run` | Print resolved command, secrets, container config, and prompt without running |
| `--container-name NAME` | Override container name (for docker-exec containers) |
| `--docker-auto-setup` | Auto-generate `.ateam/Dockerfile` when using a docker profile (default true) |
| `--force` | Run even if the same action+role is already running |
| `--verbose` | Print agent and docker commands to stderr |
| `--format jsonl` | Emit a structured JSONL event stream on stdout. Implies `--no-stream --no-summary` and suppresses the agent's text output on stdout (preserved in the event stream's `assistant` events). See [Structured event stream](#structured-event-stream-jsonl) below |
| `--progress-fd N` | Redirect `--format` output to file descriptor `N` instead of stdout. Use when the orchestrator wants stdout for something else; pair with Popen's `pass_fds` |

#### Structured event stream (jsonl)

`ateam exec --format jsonl` emits one JSON object per line on stdout, interleaving bundle-lifecycle and per-agent events with a `source` discriminator (`"bundle"` or `"agent"`). Use for external orchestrators (Python framework, shell scripts, CI) that drive `ateam exec` subprocesses and need structured progress per subprocess.

```bash
ateam exec --format jsonl "say hi" | jq .
```

```python
proc = subprocess.Popen(
    ["ateam", "exec", "--format", "jsonl", "say hi"],
    stdout=subprocess.PIPE)
for line in proc.stdout:
    event = json.loads(line)
    update_progress(event)
```

Pass `--progress-fd N` to redirect events to a different fd (e.g. when the caller wants stdout for the agent's text output):

```python
proc = subprocess.Popen(
    ["ateam", "exec", "--format", "jsonl", "--progress-fd", "3", "say hi"],
    pass_fds=(3,))
```

The runner also prints `exec_id=<id>` on stderr right after the row is allocated — orchestrators can `grep ^exec_id=` to correlate without parsing the JSONL stream.

Lifecycle event vocabulary lives in `plans/Feature_prompt_report_fs_refactor_phaseH.md`. The same `bundle.jsonl` content is also written to `<state-dir>/logs/<exec_id>/bundle.jsonl` for post-mortem inspection.

### `ateam parallel`

Run multiple agents in parallel, each with its own prompt. All execs share a single runner instance and batch for unified cost tracking.

```bash
ateam parallel "analyze auth module" "analyze payment module"
ateam parallel @task1.md @task2.md @task3.md --labels auth,payment,users
ateam parallel "task A" "task B" --max-parallel 1 --pre-prompt @context.md
ateam parallel "task A" "task B" --dry-run
```

Each positional argument is a prompt (text or `@filepath`). Each arg dispatches independently using the same rules as `ateam exec` — see [CONFIG.md → `@PATH` dispatch](CONFIG.md#path-dispatch-exec--parallel--prompt). `@foo.prompt.md` args pick up sibling framing fragments; other args run through the engine as inline text; `--raw` skips the engine entirely for every arg. Agent execs run concurrently up to `--max-parallel`, with a live ANSI progress table showing status, tool calls, and elapsed time per exec.

| Flag | Description |
|------|-------------|
| `--labels LIST` | Comma-separated names for each prompt (must match prompt count; default: `agent-1`, `agent-2`, ...) |
| `--max-parallel N` | Maximum concurrent agent execs (default: 3) |
| `--pre-prompt TEXT` | Text wrapped at the very front of each task's prompt (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of each task's prompt (text or `@filepath`). [^wrap] |
| `--raw` | Feed each prompt to the agent byte-for-byte: skip template-engine expansion. Applies to every arg. |
| `--common-prompt-first TEXT` | Deprecated alias for `--pre-prompt`. |
| `--common-prompt-last TEXT` | Deprecated alias for `--post-prompt`. |
| `--batch ID` | Custom batch name (default: `parallel-TIMESTAMP`) |
| `--profile NAME` | Runtime profile |
| `--agent NAME` | Agent name from ateam.hcl (shortcut, uses 'none' container) |
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

**Shared wrap**: Use `--pre-prompt` and `--post-prompt` to inject shared context. For inline-text args the wrap is concatenated (`pre + "\n\n---\n\n" + prompt + "\n\n---\n\n" + post`); for `@foo.prompt.md` args the wrap flows through the assembler's `AssembleOptions` and frames the composed result. (`--common-prompt-first` / `--common-prompt-last` are deprecated aliases for the same vars.)

**Output**: Progress and status go to stderr. With `--print`, exec outputs are printed to stdout in submission order, each preceded by a label header (omitted for single-exec runs). This makes it composable with downstream tools.

**Logs**: Each exec's logs are stored under `logs/parallel/{label}/` in the project `.ateam/` or, outside a project, under `~/.ateam/state/`.

**Progress table columns**: `ID, LABEL, STATUS, EstTOKENS, TURNS, DETAILS`. `EstTOKENS` is the running input+output token count for each task. While a task is live it is an *estimate* built from the per-turn usage reported in the stream (the final total only arrives on the agent's terminal result event); once the task finishes it reflects the authoritative total from that event. The column exists so a crash or timeout before the terminal event still gives visibility into how much the task consumed. The same table is also rendered by `ateam report`.

### `ateam prompt`

Resolve and print the full prompt for a role or top-level action without running it. Default mode prints the assembled prompt body. Two inspection modes (`--paths`, `--inline-paths`) trace where each part of the prompt came from — useful when debugging an override or wondering whether a project-level fragment is taking effect.

```bash
ateam prompt --role project.security --action report                   # role-bound
ateam prompt --action code --post-prompt @task.md                      # top-level singleton: implementer body + task
ateam prompt --action code_management                                   # top-level singleton: supervisor body
ateam prompt --action review
ateam prompt --action verify
ateam prompt --role project.security --action report --post-prompt "Focus on auth"

ateam prompt --role project.security --action report --paths           # tabular per-section breakdown
ateam prompt --role project.security --action report --inline-paths    # full prompt with per-section anchor/path headers

ateam prompt @./mine.prompt.md                                          # @PATH: anchored, sibling .pre.*.md compose
ateam prompt @notes.md                                                  # @PATH non-.prompt.md: engine expansion only
ateam prompt @./mine.prompt.md --raw                                    # verbatim, no engine
```

When `--role` is omitted, `--action <NAME>` resolves the top-level singleton at `<NAME>.prompt.md`. This is how the code-management supervisor produces per-task implementer prompts: `ateam prompt --action code --post-prompt @task.md | ateam exec --action code ...`.

Positional `@PATH` follows the same three-way dispatch as `ateam exec` (see [CONFIG.md → `@PATH` dispatch](CONFIG.md#path-dispatch-exec--parallel--prompt)).

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
| `--role ROLE` | Role name (omit for top-level singletons) |
| `--action ACTION` | Action name. Required when no positional `@PATH` is given. With `--role`: `report` or `code`. Without `--role`: any top-level `<ACTION>.prompt.md` (e.g. `code`, `code_management`, `review`, `code_verify`). |
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled prompt, after every other section (text or `@filepath`). [^wrap] |
| `--no-project-info` | Omit the ATeam Project Context section |
| `--ignore-previous-report` | Do not include the role's previous report |
| `--raw` | Literal-file mode only: print `@PATH` verbatim (no template engine, no framing). Has no effect on `--role` / `--action` resolution. |
| `--paths` | Show the per-section breakdown table (slot / anchor / path / last modified / est. tokens). No prompt body printed. |
| `--inline-paths` | Print the full prompt with each section preceded by an anchor/path/mod-time/tokens header. Troubleshooting view, not for agent consumption. |

### `ateam env`

Show what a run in this project will use, then the environment around it.

```bash
ateam env                      # profiles in use, layers, container mode, credentials, project
ateam env --models             # plus the per-layer model/effort chain for each profile
ateam env --roles              # plus the role table with the last report time per role
ateam env --trace              # every merged attribute with the file:line that set it
ateam env --trace agent.claude # one block (or one attribute: agent.claude.model)
ateam env --trace --abs        # absolute file paths instead of relative / ~
```

The Runtime section opens with one block per profile a flag-less run can pick (the default, the supervisor's review/code choices, every role override from `ateam.hcl`), each naming what chose it:

```
Profile default (ateam.hcl project.default_profile)
  agent:      claude-high (type claude, container none)
  model:      opus (built-in)
  effort:     high (built-in)
  config dir: ~/~/.ateam/claude_shared (~/~/.ateam/ateam.hcl)
  auth:       CLAUDE_CODE_OAUTH_TOKEN (ateam secret, global scope)
```

Model and effort show the file that set them, or that nothing pinned them and the agent CLI will pick its own default. Warnings from the model audit (see [CONFIG.md → Agents](CONFIG.md#agents)) follow each profile. `--models` adds the resolution chain, lowest precedence first: the agent CLI's own configuration (claude's `settings.json` and `ANTHROPIC_MODEL`, codex's `config.toml`, read from the config dir the run would use; not inspected for container profiles), flags hidden in `args`, each `ateam.hcl` layer (a value inherited through `base` shows the base agent), then CLI flags, then a profile's `agent_extra_args`. The same chain is printed by every `--dry-run` (`exec`, `report` per role, `review`, `code`, `verify`), there including `--model` / `--cheaper-model` / `--effort`.

Then `Layers:` lists the `ateam.hcl` files consulted, marking the ones that do not exist. The Runtime section also includes an "Agent in container mode" line plus a 2×2 detection matrix (docker layer + sandbox layer × toggle on/off × source detected), e.g.:

```
Agent in container mode: false
  Docker  detected: no — docker_detection=true
  Sandbox detected: yes (fence) — sandbox_detection=false, NOT applied
```

See [ISOLATION.md](ISOLATION.md#detection-of-an-outer-sandbox-or-container) for what each row means.

The model warnings under each profile cover: no model pinned (the agent CLI then picks its own default), a pinned model priced above `expensive_output_per_mtok`, or a `--model` / `--effort` hidden inside `args`. Each names the file and block to edit. The same warnings print to stderr when a run starts, including `--dry-run`.

`--trace` replaces the report with the merged configuration itself: every attribute of every `agent`, `container`, `profile`, `project`, `role`, `supervisor`, `sandbox_extra` and `container_extra` block plus the top-level scalars, sorted by key, each with the file and line that set it. Lower layers the value overrides follow, indented (`plus` instead of `overrides` for a `_prepend` / `_append` list, whose layers accumulate; naming a list such as `agent.claude.args` also shows its additions). A value inherited through `base` names the base agent and where the base set it; `built-in:N` is a line of the embedded defaults (`ateam cat --defaults`). The optional `KEY` keeps the keys equal to it or under it: a block (`agent.claude`, `role`), one attribute (`agent.claude.model`), or an `env` key (`agent.claude.env.FOO`). Files under the current directory print relative to it and files under the home directory with `~`; `--abs` prints every path in full, for editors and scripts.

```
agent.claude.env.CLAUDECODE = ""  (built-in:278)
agent.claude.model = opus  (.ateam/ateam.hcl:5)
    overrides ~/.ateam/ateam.hcl:2 = sonnet
agent.claude.sandbox = { ... (180 lines)  (built-in:277)
agent.kid.model = opus  (agent "claude" (base) at .ateam/ateam.hcl:5)
role.security.status = on  (.ateam/ateam.hcl:8)
```

| Flag | Description |
|------|-------------|
| `--models` | Print the full per-layer model/effort chain for each profile in use |
| `--roles` | Print the role table with the last report time per role |
| `--trace [KEY]` | Print every merged attribute with the file and line that set it, and the layers it overrides; `KEY` limits it to a block or attribute |
| `--abs` | With `--trace`: absolute file paths instead of relative to the current directory / `~` |
| `--claude-sandbox` | Print the generated Claude sandbox settings JSON for the default profile |
| `--print-home` | Print the absolute path to the user ateam directory |

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
ateam inspect --last --auto-debug --post-prompt "focus on the timeout"
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
| `--pre-prompt TEXT` | Text wrapped at the very front of the assembled auto-debug prompt, before anchor-discovered content (text or `@filepath`). [^wrap] |
| `--post-prompt TEXT` | Text wrapped at the very end of the assembled auto-debug prompt, after every other section (text or `@filepath`). [^wrap] |
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
2. Re-resolved from the agent's `config_dir` in the current `ateam.hcl` (best-effort fallback; the definition may have drifted since the run).

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
| `--port N` | Listen on a fixed port (default: remembered in `.ateam/cache/serve.port`, regenerated if the cached port is busy; also configurable in `ateam.hcl`) |
| `--no-open` | Do not open the browser automatically |
| `--public` | Bind to `0.0.0.0` instead of `127.0.0.1` (allow access from other machines) |
| `--bind IP` | Bind to a specific IP address (e.g. `192.168.1.50`) |

### `ateam cat`

Read and format stream logs for one or more completed runs. Arguments can be numeric call IDs or file paths to JSONL stream files. Pass `--last` instead of an ID to format the most recent run.

```bash
ateam cat 42
ateam cat 42 43 44 --verbose
ateam cat --last
ateam cat .ateam/logs/42/agent.jsonl
ateam cat --defaults                                   # the built-in ateam.hcl
ateam cat --defaults prompts/report/project.security.prompt.md
```

| Flag | Description |
|------|-------------|
| `--last` | Format the most recent run (when no ID is given) |
| `--defaults [PATH...]` | Print the built-in defaults instead: `ateam.hcl` (no path), `Dockerfile`, or an embedded prompt path |
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

### `ateam top`

Live, top-like status table of running agents. Takes no flags.

Runs are discovered from the call database (any exec without an end time) and progress — turns, tokens, context size, current tool — is read from each run's stream log on disk, so `top` can attach to runs started from other terminals at any point in their lifetime. New runs appear as they start; finished rows keep their final status (`done` / `ERROR` / `canceled`, classified like `ateam ps`). Press Ctrl-C to quit.

Renders the same live table as `ateam parallel` / `ateam report` (`ID, LABEL, STATUS, EstTOKENS, TURNS, DETAILS`) and requires a terminal; use `ateam ps` for a one-shot snapshot.

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

List the projects registered under the user's ateam directory (`~/.ateam/state/projects.hcl`) with their path and whether their `.ateam/` is still there.

### `ateam project-rename`

Re-register the current project after a directory move, or rename it.

```bash
ateam project-rename                  # re-register at the current path
ateam project-rename --name backend   # rename: ateam.hcl project block, state db rows, registry
```

| Flag | Description |
|------|-------------|
| `--name NAME` | New project name |
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
| `--project NAME` | Display name override (instead of ateam.hcl name) |
| `--ateam-project PATH` | Path to a `.ateam/` directory to export |

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

Each run writes to `.ateam/runtime/<exec_id>/` (the agent's scratch directory). On success, the primary output is promoted to its canonical destination. Single-file actions (`report`/`review`/`verify`/`auto-setup`) promote only the primary file — any sidecars the agent wrote stay in `runtime/<exec_id>/` (visible via `ateam inspect <id>`). `code` promotes every file (besides `*_prompt.md`) because its session naturally produces a tree. On failure, nothing is cloned — files stay in `runtime/<exec_id>/` and are listed in `.ateam/logs/<exec_id>/cmd.md` under `# Files Copy`.

| Action       | Canonical destination                                |
|--------------|------------------------------------------------------|
| `report`     | `.ateam/shared/report/<role>.md`                     |
| `review`     | `.ateam/shared/review.md`                            |
| `verify`     | `.ateam/shared/verify.md`                            |
| `code`       | `.ateam/shared/code/<exec_id>/` (whole session dir)  |
| `auto-setup` | `.ateam/shared/auto_setup.md` (written by the agent directly) |
| `exec`       | _none_ — output stays in `.ateam/runtime/<exec_id>/` |
| `parallel`   | _none_                                               |

For actions with no canonical destination, view the output with `ateam cat <exec_id>`. See [DEV.md](DEV.md) "Project on-disk layout" for the full per-run layout.

### Run Artifacts

| Path                                  | Content                                                      |
|---------------------------------------|--------------------------------------------------------------|
| `.ateam/logs/<exec_id>/cmd.md`        | Run details, command, env, settings, `# Files Copy` log      |
| `.ateam/logs/<exec_id>/stderr.out`    | Captured stderr                                              |
| `.ateam/logs/<exec_id>/agent.jsonl`  | Raw agent stream events                                      |
| `.ateam/logs/<exec_id>/prompt.md`     | Rendered prompt (used by `ateam resume`)                     |
| `.ateam/logs/<exec_id>/settings.json` | Rendered sandbox settings                                    |
| `.ateam/runtime/<exec_id>/`           | Agent-written output (preserved on both success and failure) |

On failure, error context lives in `cmd.md` / `stderr.out` / `agent.jsonl`; there are no per-action `*_error.md` files.

### History

The canonical copy of each artifact lives in the flattened `.ateam/shared/` tree (e.g. `.ateam/shared/report/<role>.md`, `.ateam/shared/review.md`) and is overwritten on each successful run. Prior versions are not archived under the canonical path; instead, every run keeps its own per-run directories keyed by `<exec_id>`:

```bash
.ateam/runtime/<exec_id>/   # the agent-written output for that run (preserved on success and failure)
.ateam/logs/<exec_id>/      # rendered prompt, stream, stderr, and settings for that run
```

Use `ateam ps` to list past runs and `ateam inspect <exec_id>` to browse a specific run's prompt and output.
