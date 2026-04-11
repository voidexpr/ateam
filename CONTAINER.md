# Running ATeam with Docker

This guide covers how to run ateam agents inside Docker containers — from simple one-shot sandboxing to integrating with existing Docker infrastructure and running interactive Claude sessions. See [README.md](README.md) for general setup and [REFERENCE.md](REFERENCE.md) for command details.

## Quick Start

```bash
# Simplest: run a report using Docker for isolation
ateam report --profile docker

# Run a single agent in Docker
ateam run "analyze the auth module" --profile docker
```

This builds a Docker image from `.ateam/Dockerfile` (or the default), runs the agent inside it, and cleans up. No additional configuration needed beyond having Docker installed and credentials set via `ateam secret`.

## Secrets

Ateam agents need API credentials. The `ateam secret` command manages them securely.

### Setting up secrets

```bash
# Store a Claude OAuth token (for subscription-based usage)
ateam secret CLAUDE_CODE_OAUTH_TOKEN --set

# Or store an API key (for pay-per-use)
ateam secret ANTHROPIC_API_KEY --set

# List all secrets and their status
ateam secret
```

Secrets are stored in the OS keychain (macOS Keychain, Linux Secret Service) or in `.env` files. They are resolved automatically when running agents — you don't need to set environment variables.

### How secret resolution works

Before spawning an agent, ateam resolves each secret declared in the agent's `required_env` using this search order: environment variable → project `.ateam/secrets.env` → org `.ateamorg/secrets.env` → global keychain/file.

If a secret is found from any source other than an existing environment variable, ateam calls `os.Setenv` to inject it into the current process. This means:

1. The agent child process inherits the secret automatically (standard Unix env inheritance)
2. For Docker containers, `forward_env` picks it up via `os.LookupEnv` and passes it as `docker run -e` / `docker exec -e`
3. The injection only affects the ateam process and its children — your shell environment is not modified

In practice: if you run `ateam secret CLAUDE_CODE_OAUTH_TOKEN --set` on the host, then `ateam run --profile docker "do something"`, the token flows from keychain → ateam process env → `docker run -e CLAUDE_CODE_OAUTH_TOKEN=...` → agent inside container. No manual `export` needed.

**Note on OAuth tokens:** `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) is a standalone inference-only token — it does not require `.credentials.json`. The default `docker` profile mounts `.credentials.json` read-only for interactive auth compatibility, but headless agents work with just the token. See [Docker Profiles](#docker-profiles) for details.

### Secrets and Docker

OS keychains don't cross into Docker containers. Ateam handles this automatically for each container mode:

| Mode | How secrets reach the container |
|------|-------------------------------|
| Docker one-shot (`--profile docker`) | Ateam resolves secrets on the host and passes them via `docker run -e` |
| Docker exec (`docker-exec` type) | Ateam resolves secrets on the host and passes them via `docker exec -e` |
| Ateam inside Docker | Mount `secrets.env` at `~/.config/ateam/secrets.env` (global scope) |

For the "ateam inside Docker" case, mount the shared config's `secrets.env` at the global secret scope path:

```bash
docker run \
  -v "<ateamorg>/claude_linux_shared/secrets.env:/home/agent/.config/ateam/secrets.env:ro" \
  ...
```

Inside the container, `ateam run` resolves `CLAUDE_CODE_OAUTH_TOKEN` from `~/.config/ateam/secrets.env` automatically — no `export` needed. See [Shared Linux Agent Config](#shared-linux-agent-config) for the full setup.

### Why not just use environment variables?

Setting `CLAUDE_CODE_OAUTH_TOKEN` as a shell environment variable works but causes problems:

- Claude Code's auth priority is: `ANTHROPIC_API_KEY` > `CLAUDE_CODE_OAUTH_TOKEN` > interactive login
- If `CLAUDE_CODE_OAUTH_TOKEN` is in your shell env, interactive Claude sessions can't use features like Remote Control
- With `ateam secret`, the token is resolved at runtime and only injected into the agent child process — your shell stays clean

### Printing secrets

```bash
# Print all secrets as KEY=VALUE (raw, for piping)
ateam secret --print

# Print specific secrets
ateam secret CLAUDE_CODE_OAUTH_TOKEN --print

# Redirect to a file
ateam secret --print > /path/to/secrets.env
```

## Docker Profiles

Three built-in Docker profiles handle the two authentication methods:

| Profile | Auth method | Credentials mounted? | Cost model |
|---------|-------------|---------------------|------------|
| `docker` | OAuth token (default) | Yes (`.credentials.json`, read-only) | Subscription |
| `docker-oauth` | OAuth token | Yes (`.credentials.json`, read-only) | Subscription |
| `docker-api` | API key only | No | Pay-per-use |

**Why the distinction?** The `docker` profile mounts `.credentials.json` for interactive auth compatibility (needed if you want to run `claude` interactively inside the one-shot container). `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` are both stateless for headless use — no host mounts needed.

`--profile docker` (the default) mounts only `~/.claude/.credentials.json` read-only into the container — no other files from `~/.claude/` are exposed (sessions, backups, settings, etc.). Use `--profile docker-api` if you prefer stateless API key auth or don't want to expose any credentials to containers.

```bash
ateam report --profile docker       # OAuth token (mounts .credentials.json)
ateam report --profile docker-api   # API key only (no host mounts)
```

The mount is controlled by `mount_claude_config` in the container definition:

```hcl
container "docker" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true    # mount ~/.claude/.credentials.json read-only
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

## Container Modes

### Docker One-Shot (sandbox alternative)

Ateam builds and runs a fresh container for each command. The container is removed after the agent completes. This is the simplest Docker mode — a more isolated alternative to Claude's built-in sandbox.

```hcl
# In runtime.hcl (defaults are already configured)
container "docker" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}

profile "docker" {
  agent     = "claude"
  container = "docker"
}
```

Usage:
```bash
ateam report --profile docker
ateam run "do something" --profile docker
```

**Customize the Dockerfile**: create `.ateam/Dockerfile` to install project-specific tools. Or run `ateam auto-setup --profile docker` to auto-detect and generate one.

### Docker Exec (reuse existing infrastructure)

For projects that already use Docker (docker-compose, devcontainer, manually managed containers), ateam can exec into your running container without managing its lifecycle.

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"
  precheck         = "docker-precheck.sh"
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
  copy_ateam       = true   # auto-copy ateam binary into container
  # exec           = "podman exec {{CONTAINER}} {{CMD}}"  # custom exec template
}

profile "my-app" {
  agent     = "claude"
  container = "my-app"
}
```

Usage:
```bash
ateam run "do something" --profile my-app
```

**Precheck scripts** run on the host before each exec. The container name (`{{CONTAINER_NAME}}`) is passed as `$1`:

```bash
#!/bin/bash
# .ateam/docker-precheck.sh
CONTAINER="$1"

# Simple: just verify the container is running
if ! docker ps --format '{{.Names}}' | grep -q "$CONTAINER"; then
    echo "Container $CONTAINER not running. Start with: docker compose up -d"
    exit 1
fi
```

```bash
#!/bin/bash
# .ateam/docker-precheck.sh — full lifecycle
CONTAINER="$1"

if ! docker ps --format '{{.Names}}' | grep -q "$CONTAINER"; then
    docker compose up -d
    sleep 2
fi
```

**`copy_ateam = true`** automatically copies the ateam linux binary into the container via `docker cp` before each exec. Requires a pre-built linux binary (`make companion`).

**Manual binary copy**: use `ateam container-cp --container-name my-app-dev` or `ateam container-cp --profile my-app`.

**Exec template**: the `exec` field supports `{{CONTAINER}}` and `{{CMD}}` placeholders for using podman, ssh, or kubectl instead of docker:

```hcl
exec = "podman exec {{CONTAINER}} {{CMD}}"
exec = "ssh devbox docker exec {{CONTAINER}} {{CMD}}"
```

**Devcontainer example**: use `docker-exec` with a precheck that starts the devcontainer:

```hcl
container "devcontainer" {
  type             = "docker-exec"
  docker_container = "my-project-devcontainer"
  precheck         = "devcontainer-precheck.sh"
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

### ATeam Inside Docker (recommended for Docker-native projects)

For projects that already use Docker for their development environment, the simplest approach is to run ateam directly inside the container. This eliminates all cross-boundary complexity.

**Setup:** mount the ateam binary and the shared config (see [Shared Linux Agent Config](#shared-linux-agent-config)):

```bash
docker run \
  -v ./build:/opt/ateam:ro \
  -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" \
  -v "$(ateam env --print-org)/claude_linux_shared/secrets.env:/home/agent/.config/ateam/secrets.env:ro" \
  ...
```

Build the linux binary with `make companion` (produces `build/ateam-linux-amd64`).

**Running agents:**

```bash
# Inside the container — just works (resolves token from global secret scope)
ateam run "do something"
ateam report
```

The `claude` agent auto-detects it's inside Docker and:
- Skips sandbox (container provides isolation)
- Adds `--dangerously-skip-permissions` (no interactive prompts)

No need for `--profile docker` — the default profile works everywhere.

**Interactive Claude + headless ateam in the same container:**

```bash
# Interactive (uses .credentials.json from shared config mount)
ateam claude --config-dir ~/shared_claude

# Headless (uses CLAUDE_CODE_OAUTH_TOKEN from ~/.config/ateam/secrets.env)
ateam run "analyze the codebase"
```

No conflict — interactive uses `.credentials.json`, headless uses the OAuth token. The token is only injected into the agent subprocess, so your shell stays clean.

## Agent Behavior Inside vs Outside Containers

Agents auto-adapt to their environment. The `claude` agent definition includes:

```hcl
agent "claude" {
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox

  # Inside containers: skip permissions (container provides isolation)
  args_inside_container    = ["--dangerously-skip-permissions"]
  sandbox_inside_container = false  # skip sandbox settings file
}
```

**Outside Docker** (host):
- Sandbox settings applied (bubblewrap/socat required)
- Permission approvals required
- Full args: `claude -p --output-format stream-json --verbose --settings /path/to/settings.json`

**Inside Docker** (detected via `/.dockerenv`):
- Sandbox skipped (container IS the sandbox)
- Permissions skipped
- Full args: `claude -p --output-format stream-json --verbose --dangerously-skip-permissions`

**Override**: create a custom agent that keeps sandbox inside containers:

```hcl
agent "claude-with-sandbox" {
  base                     = "claude"
  sandbox_inside_container = true
  args_inside_container    = []  # don't skip permissions
}
```

The `codex` agent similarly adapts: `--sandbox workspace-write` is in `args_outside_container` and omitted inside containers.

## Running Interactive Claude in Containers

This section is specific to Claude Code (the `claude` CLI) and how its authentication works inside Docker.

### Authentication methods

| Method | Token | `-p` (headless) | Interactive | Remote Control |
|--------|-------|-----------------|-------------|----------------|
| API Key | `ANTHROPIC_API_KEY` | Yes | Yes | N/A |
| OAuth Token | `CLAUDE_CODE_OAUTH_TOKEN` | Yes | **No** | **No** |
| Interactive Login | Browser → `.credentials.json` | Yes | Yes | Yes |

**`CLAUDE_CODE_OAUTH_TOKEN`** (from `claude setup-token`) is inference-only. It works with `-p` mode (headless) but does NOT work for interactive sessions. Interactive Claude will show a login prompt even if this token is set.

**Interactive login** stores full-scope credentials in `~/.claude/.credentials.json` (on Linux/Docker — macOS uses the Keychain instead). These support all features including Remote Control.

**`ANTHROPIC_API_KEY`** works everywhere but uses pay-per-use billing. It takes priority over all other auth methods.

### Setting up interactive sessions in containers

The recommended approach is the [Shared Linux Agent Config](#shared-linux-agent-config). One-time login, then mount into any container.

For quick experimentation without the shared config, you can log in directly:

```bash
# Inside a container with persistent ~/.claude volume:
claude
# Complete the login → /exit
```

But this approach has limitations — `.claude.json` is lost on container recreation, and copying credentials to other containers breaks refresh token rotation.

### Combining interactive Claude with ateam

`CLAUDE_CODE_OAUTH_TOKEN` is needed for headless ateam agents but blocks interactive Claude features (like Remote Control). The shared config approach solves this naturally:

- `ateam claude` uses `.credentials.json` (full-scope interactive login)
- `ateam run` uses `CLAUDE_CODE_OAUTH_TOKEN` from the global secret scope
- The token is injected only into the agent subprocess — your shell stays clean
- No conflict between interactive and headless

**If you have `CLAUDE_CODE_OAUTH_TOKEN` in the environment** (e.g., from docker-compose), it will be used by Claude for `-p` mode but will prevent interactive features. Use `ateam agent-config` to see what auth is active.

### Auditing auth state

```bash
ateam agent-config                                    # local audit (default)
ateam agent-config --audit --container my-app-dev     # remote audit inside a container
```

Shows all detected auth sources, runs `claude auth status` for both the default config and the shared config, and warns on mismatches.

## Shared Linux Agent Config

For running the same Claude identity across multiple containers, use a shared config directory. This provides both interactive Claude sessions (via `CLAUDE_CONFIG_DIR`) and headless `ateam run` (via the global secret scope).

### Host layout

```
~/.ateamorg/claude_linux_shared/
  .credentials.json  # OAuth tokens for interactive sessions (access + refresh)
  .claude.json       # Account state, onboarding flags
  settings.json      # Claude settings
  secrets.env        # CLAUDE_CODE_OAUTH_TOKEN for headless ateam run
  ...
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

# 4. Generate a headless token for ateam run
claude setup-token
# Copy the token, then save it:
echo "CLAUDE_CODE_OAUTH_TOKEN=<token>" >> ~/shared_claude/secrets.env
```

Or bootstrap from an existing container that already has credentials:

```bash
ateam agent-config --copy-out --container my-app --path ~/.ateamorg/claude_linux_shared
```

### Mounting into containers

Mount two things — the shared dir (for interactive claude) and `secrets.env` at the global secret scope path (for headless `ateam run`):

```bash
docker run \
  -v "$(ateam env --print-org)/claude_linux_shared:/home/agent/shared_claude" \
  -v "$(ateam env --print-org)/claude_linux_shared/secrets.env:/home/agent/.config/ateam/secrets.env:ro" \
  ...
```

### Usage inside the container

```bash
# Interactive claude (uses .credentials.json from shared dir):
ateam claude --config-dir ~/shared_claude

# Or manually:
CLAUDE_CONFIG_DIR=~/shared_claude claude --dangerously-skip-permissions --remote-control

# Headless ateam run (uses CLAUDE_CODE_OAUTH_TOKEN from global secret scope):
ateam run "do something"
ateam report
```

No `export` needed — `ateam run` inside a container automatically resolves secrets from the global scope (`~/.config/ateam/secrets.env`).

### Injecting ateam into a container

```bash
# Mount at runtime (recommended — picks up recompiles):
docker run -v ./build:/opt/ateam:ro ...

# Or copy into a running container:
ateam container-cp --container-name my-app
```

### Why mount, not copy?

Copying credentials to multiple containers breaks OAuth refresh token rotation. When one container refreshes its token, the other's copy is revoked. Using the revoked token triggers replay detection, invalidating all copies. A shared mount avoids this — all containers read/write the same credential file, just like multiple sessions on a single Linux host.

### How it works

- **Interactive**: `CLAUDE_CONFIG_DIR` tells Claude to store everything in one directory (instead of splitting between `~/.claude/` and `~/.claude.json`). `ateam claude` sets this, unsets API keys to avoid conflicts, and execs claude.
- **Headless**: `secrets.env` mounted at `~/.config/ateam/secrets.env` is the global secret scope. Inside a container, `ateam run` resolves `CLAUDE_CODE_OAUTH_TOKEN` from this file and injects it into the agent subprocess. Your shell environment stays clean.
- **No conflict**: interactive Claude uses `.credentials.json` (full-scope login). Headless agents use `CLAUDE_CODE_OAUTH_TOKEN` (inference-only). These are separate auth paths — no interference.

## Debugging

### Dry-run

```bash
ateam run --dry-run "hello" --profile docker
```

Shows the exact agent command, docker command, secret resolution, and sandbox settings without running anything. Useful for debugging container and auth issues.

### Inspect container config

```bash
ateam env
```

Shows the active profile, agent, container type, and auth status. Warns when project-scope secrets override global values.
