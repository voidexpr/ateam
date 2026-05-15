# ATeam Configuration

Configuration files and runtime config for ATeam. For command-line invocations, see [COMMANDS.md](COMMANDS.md). For sandbox and container setup, see [ISOLATION.md](ISOLATION.md).

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
    supervisor/code_verify_prompt.md           # supervisor verify prompt
    supervisor/report_commissioning_prompt.md  # report commissioning prompt
    supervisor/exec_debug_prompt.md            # agent_exec debug prompt (used by ateam inspect --auto-debug)
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

## `config.toml`

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
"project.security" = "on"
"test.gaps" = "on"
"code.structure" = "off"

# [supervisor]
# code_profile = "cheap"          # use a cheaper model for coding sub-runs

# [profiles]
# [profiles.roles]
# "project.security" = "docker"    # run security reports in Docker
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
| `code_profile` | Profile for code sub-runs (passed to `ateam exec --profile`) |
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
"project.security" = "docker"     # run security reports in Docker
critical_code_reviewer = "codex"  # use codex agent for this role
"test.gaps" = "docker"            # if test discovery needs Docker for the project's runner
```

Values can be either a **profile name** (defined in `runtime.hcl`) or an **agent name** (also defined in `runtime.hcl`). When the value matches a known agent but not a profile, it's treated as an agent shorthand — equivalent to `--agent NAME` on the CLI.

**Profile resolution order** (first match wins):
1. CLI flag (`--profile` or `--agent`) — overrides everything
2. `[profiles.roles]` — per-role override from config.toml
3. `[supervisor]` action-specific profile (for review/code actions)
4. `[supervisor]` default_profile
5. `[project]` default_profile
6. Built-in `"default"` profile

See [Profiles](#profiles) under Runtime Configuration for `runtime.hcl` profile definitions.

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

Roles are opt-in: only roles explicitly listed in `config.toml` with status `on` (or the legacy `enabled`) are included in `--roles all`. Unlisted roles — built-in or custom — and roles set to `off` are excluded from the `all` expansion. They can still be run by naming them directly: `ateam report --roles my_custom_role` works without any `config.toml` entry as long as the role's `report_prompt.md` exists.

## Prompt Configuration

All prompt files can be customized at the project level (`.ateam/`), organization level (`.ateamorg/`), or rely on built-in defaults.

To add instructions without replacing the default prompt, use `*_extra_prompt.md` files. To inspect what prompt will be used:

```bash
ateam prompt --role ROLE --action report
ateam prompt --supervisor --action review
```

### Prompt Resolution

Prompts are resolved with a 3-level fallback: **project** → **org** → **embedded defaults**. The first file found wins. Extra prompts are **additive** — all matching files are included.

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
  required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet"]
}
```

Agents support inheritance via `base`, sandbox settings, environment variables, isolated config dirs, and `required_env` for secret validation. When alternatives are declared (e.g., `required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]`), the first alternative in declaration order wins at each tier (store first, then env). Competing alternatives are stripped from the agent's process environment to avoid credential confusion. See [`ateam secret`](COMMANDS.md#ateam-secret) for the full resolution order.

`max_budget_usd = "<USD>"` can be set on an agent block as a default spend cap (claude only; codex ignores it). The CLI `--max-budget-usd` flag overrides the per-agent default for a single invocation.

### Effort levels

Each agent block accepts an optional `effort = "..."` field that controls the underlying CLI's reasoning depth. The string is passed through verbatim, so `ateam` does not need to be updated when agents add new levels. CLI flags (`--effort` on `exec`, `code`, `report`, `parallel`) override the per-agent default for a single invocation.

```hcl
agent "claude-sonnet" {
  base   = "claude"
  effort = "high"
}
```

Per-agent value sets at time of writing:

| Agent | Native flag | Accepted values |
|---|---|---|
| Claude Code | `--effort LEVEL` | `low`, `medium`, `high`, `xhigh`, `max`, `auto` |
| OpenAI Codex | `-c model_reasoning_effort=LEVEL` | `minimal`, `low`, `medium`, `high`, `xhigh` |

Future agents may accept different values. `ateam` does not validate; an invalid level fails at the underlying CLI, surfacing in the run summary.

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
| `{{BATCH}}` | Batch ID | `code-2026-03-31_06-09-39` |
| `{{TIMESTAMP}}` | Run start time | `2026-03-31_06-09-39` |
| `{{PROFILE}}` | Active profile name | `docker`, `default` |
| `{{EXEC_ID}}` | Call tracking ID (visible in `ateam ps`) | `42` |
| `{{AGENT}}` | Agent config name | `claude`, `claude-docker` |
| `{{MODEL}}` | Resolved model name | `sonnet`, `haiku` |
| `{{CONTAINER_TYPE}}` | Container type | `none`, `docker`, `docker-exec` |
| `{{CONTAINER_NAME}}` | Docker container name | `ateam-myapp-security` |
| `{{OUTPUT_DIR}}` | Absolute path to the agent's writable output directory for this run (`.ateam/runtime/<exec_id>/`) | `/proj/.ateam/runtime/42` |
| `{{OUTPUT_FILE}}` | Absolute path to the primary output file for the current action (e.g. the role's `report.md`) | `/proj/.ateam/runtime/42/report.md` |

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

Three container types are supported: `none`, `docker` (one-shot), and `docker-exec` (exec into a user-managed container).

```hcl
container "docker" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true     # mount ~/.claude/.credentials.json read-only (OAuth)
  forward_env         = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

`mount_claude_config = true` bind-mounts the host's `~/.claude/.credentials.json` into the container as read-only — required for OAuth-token auth that needs the credential store. Stateless API-key auth (`docker-api` profile) does not need it. See [ISOLATION.md](ISOLATION.md) for the full mount layout.

`docker-exec` runs agents inside a long-lived user-managed container (docker-compose service, devcontainer, manually started, etc.). The container name resolves from `--container-name` flag → `ateam secret CONTAINER_NAME --scope project` → `CONTAINER_NAME` env var → the `docker_container` field. A built-in `docker-exec` profile uses `{{CONTAINER_NAME}}` for secret-based resolution.

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

**Devcontainers** are supported as a use case of `docker-exec`: write a precheck that runs `devcontainer up --workspace-folder .` to ensure the container exists, then ateam `docker exec`s into it like any other long-lived container. See [ISOLATION.md](ISOLATION.md) for the worked example. Requires `@devcontainers/cli` on the host.

For authentication setup inside containers, see [ISOLATION.md](ISOLATION.md).

### Profiles

Profiles combine an agent and a container:

```hcl
profile "default" {
  agent     = "claude"
  container = "none"
}

profile "docker" {
  agent     = "claude"
  container = "docker"
}

profile "docker-exec" {
  agent     = "claude"
  container = "docker-exec"
}

profile "cheap" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "sonnet", "--max-budget-usd", "0.50"]
}
```

Use `--profile docker` to run in a fresh ateam-managed container, `--profile docker-exec` to exec into a user-managed long-lived container, or `--profile cheap` for cheaper runs. See `defaults/runtime.hcl` for the full list of built-in profiles.

For the resolution order when multiple profile selectors apply, see [Profile resolution order](#profiles-1) in the `[profiles]` config.toml section.

#### Running the supervisor inside Docker

Typically only coding agents need to run inside docker so they can build and run tests in an isolated environment. Basic docker config from [README.md](README.md) is enough. But if you want the supervisor itself to run in docker and launch ateam coding agents then a Linux build of ateam must be available inside of docker. Cross-compile the Linux companion binary:

```bash
make companion    # produces build/ateam-linux-amd64
```

The binary is automatically found by ateam from `build/`. For installations without a git checkout, place `ateam-linux-amd64` next to the host `ateam` binary.

For complete Docker setup including secrets, precheck scripts, and interactive sessions, see [ISOLATION.md](ISOLATION.md).
