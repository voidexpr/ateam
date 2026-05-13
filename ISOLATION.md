# Isolation

ATeam runs unattended coding agents — they must operate safely without per-tool permission prompts. This guide covers the four execution modes, from the simplest to the most isolated, plus secret management and credential handling.

See [README.md](README.md) for the project overview and the *why* of isolation, [COMMANDS.md](COMMANDS.md) for command reference, and [CONFIG.md](CONFIG.md) for `runtime.hcl` / `config.toml` details.

## Execution modes at a glance

See the [Execution modes diagram in README.md](README.md#execution-modes) for a visual map of the four modes.

| Mode | When to use | Setup cost |
|------|-------------|------------|
| ① Built-in sandbox | Most projects. Default. | None |
| ② Docker one-shot | Strong isolation; clean env every time; need build/test tooling | `.ateam/Dockerfile` + `ateam secret` |
| ③ Docker exec | You already have a long-lived dev container (docker-compose, devcontainer, …) | One config block |
| ④ ATeam inside Docker | The whole project is Docker-native; you already shell into a container to work | Mount the linux binary |

There is also a "no isolation" option (run the agent directly against the host with no sandbox) — covered briefly below; rarely the right choice.



### Sandbox

No config needed — works out of the box. ATeam's default sandbox restricts filesystem access to fewer directories than the agent's default and limits network to package registries and API endpoints.

To customize, edit `.ateam/config.toml`:

```toml
[sandbox-extra]
allow_write = ["/tmp/my-tool-output"]
allow_read = ["/opt/my-sdk"]
allow_domains = ["my-internal-registry.dev"]
unsandboxed_commands = ["playwright"]    # commands that can't run inside a sandbox
```

**Known limitations** (will change as agents evolve):
- Claude Code doesn't yet support Unix domain sockets or named pipes — Docker, playwright-cli and tsx must run unsandboxed
- Sandboxes can't be nested (e.g., Playwright CLI inside a sandbox)
- All files are readable by default; sensitive paths must be explicitly excluded

### Separate configuration for coding agents

By default ateam uses your local agent configuration (for example `~/.claude` for Claude) that may include some settings that could be helpful (skills, plugins, mcp servers) or not helpful (custom logging of tools, custom notifications). Eventually it is recommended to use a different configuration directory (for example `~/.ateamorg/claude`) and change `runtime.hcl` to use it by default.

### Docker
#### One-shot (docker run)

Use `--profile docker` for one-shot container isolation, or run ateam inside your own Docker setup. Agents auto-detect containers and skip sandbox/permissions — no profile switching needed. A default Dockerfile is used so agent is available inside of the container.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.

#### docker exec

Use `--profile docker-exec` to run agents within existing docker containers. Ateam makes sure that agents skip sandbox/permissions — no profile switching needed.

A coding agent has be available within the container, by default an oauth token is passed so no need to authenticate inside the container. No need to install ateam itself unless you run the supervisor for coding this way. This mode is best used for code agent runs so they have access to a proper test environment.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.

#### Run ateam inside a container

No custom profile needed, ateam detects it runs within a container and runs agents without sandbox or permission approval. But this does require to install team inside the container and (optionally) mount the ateamorg directory to have access to defaults. Also the coding agent inside the container should be fully authenticated and optionally have an oauth token.

See [ISOLATION.md](ISOLATION.md) for the full guide: container modes, secrets, precheck scripts, interactive Claude sessions, and agent auto-adaptation.


### Customizing Runtime

- **`config.toml`**: simple customization — sandbox paths, container extras, unsandboxed commands, profiles
- **`runtime.hcl`**: full control — agent definitions, container types, profiles, pricing

See [CONFIG.md](CONFIG.md) for complete configuration documentation.





## No isolation

If you run `ateam` with a profile whose container is `none` and use a custom agent that disables the sandbox (or use `--dangerously-skip-permissions` directly with `claude`), the agent operates on the host with full filesystem and network access. Useful only when ateam itself is already inside an isolated environment (e.g. CI runner, throwaway VM) — otherwise prefer the sandbox.

## ① Built-in sandbox (default)

No config needed. ATeam's default profile runs each agent through its built-in OS-level sandbox (Seatbelt on macOS, bubblewrap on Linux). Filesystem access is restricted to fewer directories than the agent's default, and network access is limited to package registries and API endpoints.

### Customizing the sandbox

Edit `.ateam/config.toml`:

```toml
[sandbox-extra]
allow_write = ["/tmp/my-tool-output"]
allow_read = ["/opt/my-sdk"]
allow_domains = ["my-internal-registry.dev"]
unsandboxed_commands = ["playwright"]    # commands that can't run inside a sandbox
```

Use `ateam env --claude-sandbox` to see the merged sandbox settings. See [CONFIG.md `[sandbox-extra]`](CONFIG.md#sandbox-extra) for the full field reference.

### Known limitations

These will change as agents evolve:

- Claude Code does not yet support Unix domain sockets or named pipes — Docker, playwright-cli and tsx must run unsandboxed.
- Sandboxes cannot be nested (e.g., Playwright CLI inside a sandbox).
- All files are readable by default; sensitive paths must be explicitly excluded.

## Separate config directory for the coding agent

This is a *form of isolation* that's independent of sandbox/Docker: instead of letting the unattended agent inherit your interactive Claude/Codex configuration (`~/.claude`, plugins, MCP servers, custom hooks, settings), point it at a different directory.

Why:
- Your interactive Claude likely has skills, plugins, custom logging, notifications. Some are helpful for unattended runs; many are noise or actively interfere.
- Keeps interactive state (sessions, history, settings) separate from unattended state.

How (Claude example):

```hcl
# .ateamorg/runtime.hcl
agent "claude" {
  base       = "claude"
  config_dir = "~/.ateamorg/claude"   # instead of ~/.claude
}
```

The agent reads/writes `.credentials.json`, `.claude.json`, `settings.json` from that directory. See `agent.config_dir` in [CONFIG.md → Agents](CONFIG.md#agents) and [Template Variables](CONFIG.md#template-variables) (you can also use `{{ROLE}}` for per-role isolation).

For *containerized* shared agent configuration across multiple containers, see [Shared Linux agent config](#shared-linux-agent-config) below.

## ② Docker — one-shot (`--profile docker`)

A fresh container is built and run for each command, then destroyed. The simplest Docker mode — strong isolation, clean environment every time.

```bash
ateam report --profile docker
ateam exec "analyze the auth module" --profile docker
```

ATeam builds the image from `.ateam/Dockerfile` (or `.ateamorg/Dockerfile`, or the embedded default), mounts your project, and runs the agent inside.

### Dockerfile resolution

First match wins:

1. `.ateam/Dockerfile` — project-specific
2. `.ateamorg/Dockerfile` — org-wide
3. `.ateamorg/defaults/Dockerfile` — org defaults (written by `ateam update`)
4. Embedded default — `node:20-bookworm-slim` based image with git, curl, python3, ripgrep, jq, make, and Claude Code CLI

The default image creates an `agent` user matching your host UID so bind-mount file ownership is correct.

### Mounts

| Host path | Container path | Mode | Purpose |
|-----------|----------------|------|---------|
| Git root (or project source) | `/workspace` | `ro` for report/review, `rw` for code/run | Source code |
| `.ateam/` | `/workspace/.ateam/` | `rw` | Agent state, logs, artifacts |
| `.ateamorg/` | `/.ateamorg/` | `rw` | Organization config |
| `~/.claude/.credentials.json` | `/home/agent/.claude/.credentials.json` | `ro` | OAuth credentials (only with `mount_claude_config = true`) |
| `/etc/localtime` | `/etc/localtime` | `ro` | Host timezone |

When the project is in a subdirectory of the git root (e.g. `repo/myapp/`), the entire git root is mounted at `/workspace` and the working directory is set to `/workspace/myapp`.

### Built-in profiles

| Profile | Auth method | Credentials mounted? | Cost model |
|---------|-------------|---------------------|------------|
| `docker` | OAuth token (default) | Yes (`.credentials.json`, read-only) | Subscription |
| `docker-oauth` | OAuth token | Yes (`.credentials.json`, read-only) | Subscription |
| `docker-api` | API key only | No | Pay-per-use |

The `docker` profile mounts only `~/.claude/.credentials.json` read-only — no other files from `~/.claude/` are exposed. Use `--profile docker-api` for stateless API-key auth with no host mounts.

If you have an existing long-lived container (e.g. a docker-compose service or devcontainer), prefer [③ Docker exec](#-docker--exec-into-existing-container---profile-docker-exec) — it avoids the rebuild-per-run cost.

### Per-project customization

Add raw `docker run` flags, env forwarding, or static env via `config.toml`:

```toml
[container-extra]
extra_args = [
  "-p", "3000:3000",
  "-p", "5432:5432",
  "-v", "pgdata:/pgdata",
  "--env-file", ".env",
]
forward_env = ["DATABASE_URL"]

[container-extra.env]
NODE_ENV = "test"
DB_HOST = "localhost"
```

These fields are **additive** — they extend the HCL container definition rather than replacing it (which is what redefining the whole `container` block would do, losing defaults like `forward_env` and `dockerfile`).

| Field | Applies to | Purpose |
|-------|-----------|---------|
| `extra_args` | `docker run` only | Raw flags: `-p`, `-v`, `--cpus`, `--memory`, `--network`, `--env-file`, … |
| `forward_env` | `docker run` and `docker exec` | Forward host env var values into the container |
| `env` | `docker run` and `docker exec` | Set literal env vars inside the container |

`extra_args` only applies on `docker run` (container creation). For env vars you need on every invocation, use `forward_env` or `env`.

## ③ Docker — exec into existing container (`--profile docker-exec`)

For projects that already manage a long-lived container (docker-compose, devcontainer, manually started), ATeam can exec into it without managing its lifecycle.

Zero-config quick start:

```bash
# Set the container name per-project (stored in keychain/secrets.env)
ateam secret CONTAINER_NAME=my-app-dev --scope project

ateam exec "do something" --profile docker-exec
# Or override on the command line
ateam exec "do something" --profile docker-exec --container-name my-app-dev
```

Container-name resolution order: `--container-name` flag > `ateam secret CONTAINER_NAME` > `CONTAINER_NAME` env var > `docker_container` in the container config. Use `--dry-run` to see which source is active.

Before exec, ateam runs `docker ps` to validate the container is running and to resolve partial names. If it isn't running, you get a clear error before the agent starts.

### Custom config

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"             # or "{{CONTAINER_NAME}}" for secret-based
  precheck         = ["sh", "docker-precheck.sh", "{{CONTAINER_NAME}}"]
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
  copy_ateam       = true                     # auto-copy ateam binary into container
  # exec           = "podman exec {{CONTAINER}} {{CMD}}"
}

profile "my-app" {
  agent     = "claude"
  container = "my-app"
}
```

- `copy_ateam = true` runs `docker cp` of the linux binary before each exec. Requires `make companion` (produces `build/ateam-linux-amd64`). Manual variant: `ateam container-cp --container-name my-app-dev`.
- `exec` supports `{{CONTAINER}}` and `{{CMD}}` placeholders for podman / ssh / kubectl wrappers.

### Devcontainer

Devcontainers are supported via `docker-exec` plus a precheck script that brings the container up. Define a custom container in your `runtime.hcl`:

```hcl
container "my-devcontainer" {
  type             = "docker-exec"
  docker_container = "my-project-devcontainer"
  precheck         = ["sh", "devcontainer-precheck.sh", "{{CONTAINER_NAME}}"]
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

```bash
#!/bin/bash
# .ateam/devcontainer-precheck.sh
CONTAINER="$1"
if ! docker ps --format '{{.Names}}' | grep -q "$CONTAINER"; then
    devcontainer up --workspace-folder .
fi
```

## ④ ATeam inside Docker

If your project is Docker-native — you already shell into a container to work — the simplest setup is to run `ateam` from within the container. No `--profile docker`, no cross-boundary auth. The container *is* the isolation boundary.

Setup: mount the ateam linux binary and the shared agent config:

```bash
docker run \
  -v ./build:/opt/ateam:ro \
  -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" \
  -v "$(ateam env --print-org)/claude_linux_shared/secrets.env:/home/agent/.config/ateam/secrets.env:ro" \
  ...
```

Build the linux binary with `make companion` (produces `build/ateam-linux-amd64`).

Running agents inside the container:

```bash
ateam exec "do something"
ateam report
```

The `claude` agent auto-detects `/.dockerenv` and:
- skips the sandbox (container provides isolation)
- adds `--dangerously-skip-permissions` (no interactive prompts)

No `--profile` switch needed — the default profile works inside containers.

Alternatively, build a single Dockerfile that already includes ateam:

```dockerfile
FROM node:20-bookworm-slim
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ateam-builder /ateam /usr/local/bin/ateam
WORKDIR /workspace
```

```bash
docker run -it -v $(pwd):/workspace \
  -e ANTHROPIC_API_KEY \
  my-project:latest \
  bash -c "cd /workspace && ateam init && ateam all"
```

## Agent behavior inside vs outside containers

Agents auto-adapt to their environment. The `claude` agent definition includes:

```hcl
agent "claude" {
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox

  args_inside_container    = ["--dangerously-skip-permissions"]
  sandbox_inside_container = false  # skip sandbox settings file
}
```

| Context | Sandbox | Permissions |
|---------|---------|-------------|
| Host (no container) | Applied | Required |
| Inside Docker (detected via `/.dockerenv`) | Skipped (container is the sandbox) | Skipped |

Override by writing a custom agent:

```hcl
agent "claude-with-sandbox" {
  base                     = "claude"
  sandbox_inside_container = true
  args_inside_container    = []  # don't skip permissions
}
```

The `codex` agent similarly adapts: `--sandbox workspace-write` is in `args_outside_container` and omitted inside containers.

## Secrets

Once you use Docker, secrets matter — agents inside containers need API credentials forwarded. The `ateam secret` command manages them across the host keychain and `.env` files.

### Setting secrets

```bash
ateam secret CLAUDE_CODE_OAUTH_TOKEN --set     # subscription-based usage
ateam secret ANTHROPIC_API_KEY --set           # pay-per-use API key
ateam secret                                    # list all required secrets and their status
```

You can obtain a long-lived Claude OAuth token with:

```bash
claude setup-token
```

Secrets are stored in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager) or `.env` files.

### Resolution order

Per-key (the secret store beats the environment for the same key):

1. Project `.ateam/secrets.env` / keychain
2. Org `.ateamorg/secrets.env` / keychain
3. Global `~/.config/ateam/secrets.env` / keychain
4. Process environment

**Alternatives (`A|B` in `required_env`)** — the winner is picked in two steps:

1. Walk alternatives at the **store tier** (project → org → global). The first alternative in declaration order that resolves wins.
2. Otherwise walk alternatives at the **env tier**. The first alternative in declaration order wins.

The default claude agents declare `CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY` (OAUTH first), so:

- OAUTH and API at the same level → **OAUTH wins**.
- API in store, OAUTH only in env → **API wins** (store tier beats env tier).
- OAUTH in store, API only in env → **OAUTH wins**.

**Credential isolation:** after resolution, ateam strips every non-winning alternative that also exists in the host environment, so the agent child process only sees the winner. This prevents Claude Code from picking up the wrong credential via its own internal auth priority (`ANTHROPIC_API_KEY > CLAUDE_CODE_OAUTH_TOKEN`). When a credential is stripped, ateam prints a notice:

```
Notice: use CLAUDE_CODE_OAUTH_TOKEN from ateam secret (project), ignore ANTHROPIC_API_KEY from the environment
```

Use `ateam env` to see every configured credential (including shadowed ones) and which one will be used. Use `ateam exec ... --dry-run` for the per-invocation equivalent.

### Host vs container behavior

| Context | Validation | Isolation |
|---------|-----------|-----------|
| Host (no container) | Skipped — agents handle their own auth | Always runs |
| Container (`--profile docker`) | Required — at least one credential must be resolvable | Always runs |
| Inside container (`/.dockerenv`) | Required | Always runs |

On the host, if no `ateam secret` is configured and no credential env vars are set, ateam does not error — the agent authenticates itself (e.g., interactive Claude Code login, macOS Keychain). Inside containers, where interactive login isn't available, at least one credential must be resolvable.

### Secrets and Docker

OS keychains don't cross into containers. ATeam handles this per mode:

| Mode | How secrets reach the container |
|------|---------------------------------|
| Docker one-shot (`--profile docker`) | Resolved on host, passed via `docker run -e` |
| Docker exec (`docker-exec`) | Resolved on host, passed via `docker exec -e` |
| ATeam inside Docker | Mount `secrets.env` at `~/.config/ateam/secrets.env` (global scope) — see [Shared Linux agent config](#shared-linux-agent-config) |

### Printing secrets

```bash
ateam secret --print                       # all secrets as KEY=VALUE (raw)
ateam secret CLAUDE_CODE_OAUTH_TOKEN --print
ateam secret --save-project-scope          # write resolved values to .ateam/secrets.env (mounted into containers)
```

### Why not just use environment variables?

Setting `CLAUDE_CODE_OAUTH_TOKEN` as a shell environment variable works but causes problems:

- Claude Code's auth priority is `ANTHROPIC_API_KEY` > `CLAUDE_CODE_OAUTH_TOKEN` > interactive login.
- If `CLAUDE_CODE_OAUTH_TOKEN` is in your shell env, interactive Claude sessions can't use features like Remote Control.
- With `ateam secret`, the token is resolved at runtime and only injected into the agent child process — your shell stays clean.

## Authentication methods (Claude Code)

This section is Claude-specific.

| Method | Token | `-p` (headless) | Interactive | Remote Control |
|--------|-------|-----------------|-------------|----------------|
| API Key | `ANTHROPIC_API_KEY` | Yes | Yes | N/A |
| OAuth Token | `CLAUDE_CODE_OAUTH_TOKEN` | Yes | **No** | **No** |
| Interactive Login | Browser → `.credentials.json` | Yes | Yes | Yes |

- **`CLAUDE_CODE_OAUTH_TOKEN`** (from `claude setup-token`) is inference-only. Works with `-p` (headless) but not for interactive sessions.
- **Interactive login** stores full-scope credentials in `~/.claude/.credentials.json` (on Linux/Docker; macOS uses the Keychain). Supports all features.
- **`ANTHROPIC_API_KEY`** works everywhere but is pay-per-use. Takes priority over everything else.

### Combining interactive + headless

`CLAUDE_CODE_OAUTH_TOKEN` is needed for headless ateam agents but blocks interactive Claude features. The [shared config](#shared-linux-agent-config) approach solves this:

- `ateam claude` uses `.credentials.json` (full-scope interactive login).
- `ateam exec` uses `CLAUDE_CODE_OAUTH_TOKEN` from the global secret scope.
- The token is injected only into the agent subprocess — your shell stays clean.

If `CLAUDE_CODE_OAUTH_TOKEN` is in the *shell* environment (e.g., from docker-compose), it will be used by Claude for `-p` mode but will prevent interactive features. Use `ateam agent-config` to see what auth is active.

### Auditing auth state

```bash
ateam agent-config                                    # local audit (default)
ateam agent-config --audit --container my-app-dev     # remote audit inside a container
```

Shows all detected auth sources, runs `claude auth status` for both the default config and the shared config, and warns on mismatches.

## Shared Linux agent config

For running the same Claude identity across multiple Linux containers (typical with mode ③ or ④), use a shared config directory. This provides both interactive Claude sessions (via `CLAUDE_CONFIG_DIR`) and headless `ateam exec` (via the global secret scope).

### Layout

```
~/.ateamorg/claude_linux_shared/
  .credentials.json  # OAuth tokens for interactive sessions (access + refresh)
  .claude.json       # Account state, onboarding flags
  settings.json      # Claude settings
  secrets.env        # CLAUDE_CODE_OAUTH_TOKEN for headless ateam exec
```

### One-time setup

```bash
# 1. Create the shared config directory
mkdir -p ~/.ateamorg/claude_linux_shared

# 2. Start a container with the shared dir mounted
docker run -it \
  -v ~/.ateamorg/claude_linux_shared:/home/agent/shared_claude \
  your-image bash

# 3. Inside the container: login interactively
ateam claude --config-dir ~/shared_claude
# Complete the login flow, then /exit

# 4. Generate a headless token for ateam exec
claude setup-token
echo "CLAUDE_CODE_OAUTH_TOKEN=<token>" >> ~/shared_claude/secrets.env
```

Or bootstrap from a container that already has credentials:

```bash
ateam agent-config --copy-out --container my-app --path ~/.ateamorg/claude_linux_shared
```

### Mounting into containers

Two mounts — the shared dir (for interactive claude) and `secrets.env` at the global secret scope path (for headless `ateam exec`):

```bash
docker run \
  -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" \
  -v "$(ateam env --print-org)/claude_linux_shared/secrets.env:/home/agent/.config/ateam/secrets.env:ro" \
  ...
```

### Why mount, not copy?

Copying credentials to multiple containers breaks OAuth refresh token rotation. When one container refreshes its token, the other's copy is revoked; using the revoked token triggers replay detection, invalidating all copies. A shared mount avoids this — all containers read/write the same credential file, like multiple sessions on a single Linux host.

`ateam agent-config --copy-in` exists for one-time experimentation but is not recommended in production.

## Precheck scripts

`docker-exec` containers can run a precheck on the host before each agent invocation — typically to ensure the target container exists, or to start dependencies like a database.

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"
  precheck         = ["sh", "docker-precheck.sh", "{{CONTAINER_NAME}}"]
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

`{{CONTAINER_NAME}}` is expanded to the resolved container name. Convention-discovered scripts (`.ateam/docker-agent-precheck.sh`) are auto-wrapped as `["sh", "<path>", "{{CONTAINER_NAME}}"]`.

Example — ensure the container is running:

```bash
#!/bin/bash
CONTAINER="$1"
if ! docker ps --format '{{.Names}}' | grep -q "$CONTAINER"; then
    docker compose up -d
    sleep 2
fi
```

## Running the supervisor in Docker

Typically only the `code` action benefits from Docker (it needs to build and run tests). Report and review are read-only analyses that work fine in the default sandbox.

If you want the supervisor itself to run in Docker and launch coding agents from inside it, a Linux build of ateam must be available inside the container:

```bash
make companion    # produces build/ateam-linux-amd64
```

ATeam auto-detects the binary and mounts it at `/usr/local/bin/ateam` inside the container.

## Troubleshooting

### Dry-run

```bash
ateam exec --dry-run "hello" --profile docker
```

Shows the resolved agent command, docker command, secret resolution, and sandbox settings without running anything.

### Inspect a past run

```bash
ateam env             # active profile, agent, container type, auth status
ateam inspect 42      # full execution context for a run: docker cmd, mounts, env
```

### Image build fails

```bash
docker build -t test -f .ateam/Dockerfile .
```

### Agent can't access the API

```bash
ateam secret ANTHROPIC_API_KEY        # re-enter if needed
ateam env                              # check "secrets" section
```

## Known limitations

- **No macOS guest** — Docker containers run Linux; you can't test macOS-specific code inside.
- **Named volumes persist across runs** — volumes created via `extra_args` (e.g., `-v pgdata:/pgdata`) survive container removal (ateam uses `docker rm -f` without `-v`). Clean up manually with `docker volume rm <name>`.
