# Docker Isolation Guide

This guide covers how to use Docker with ATeam. For sandbox configuration (the default, no-Docker approach), see the [Isolation section in README.md](README.md#isolation).

## Two Docker Workflows

### Workflow 1: ATeam Inside Docker (simple)

Add ATeam to your existing Docker image and run everything from within it. No special ateam config needed — the container is the isolation boundary.

```dockerfile
# Your project's Dockerfile
FROM node:20-bookworm-slim
# ... your project setup ...
RUN npm install -g @anthropic-ai/claude-code

# Install ateam
COPY --from=ateam-builder /ateam /usr/local/bin/ateam

WORKDIR /workspace
```

Since the container is the isolation boundary, define a no-sandbox profile in your project's `.ateam/runtime.hcl`:

```hcl
# .ateam/runtime.hcl — no sandbox, no Docker management (container IS the sandbox)
agent "claude-unsandboxed" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"]
  env     = { CLAUDECODE = "" }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

profile "no-sandbox" {
  agent     = "claude-unsandboxed"
  container = "none"
}
```

Then run with no sandbox and no permission prompts:

```bash
docker run -it -v $(pwd):/workspace \
  -e ANTHROPIC_API_KEY \
  my-project:latest \
  bash -c "cd /workspace && ateam init && ateam all --profile no-sandbox"
```

**Pros**: simple setup, no sandboxing needed, fully reproducible.
**Cons**: need to maintain a Docker image that includes both your project tooling and ATeam.

### Workflow 2: ATeam Orchestrates Docker (recommended)

ATeam runs on the host (or in CI) and launches agents inside Docker containers. This is what `--profile docker` does.

```bash
# Store API key for forwarding into containers
ateam secret ANTHROPIC_API_KEY
# or: ateam secret CLAUDE_CODE_OAUTH_TOKEN

# Run with Docker isolation
ateam report --profile docker
ateam review --profile docker
ateam code --profile docker
```

You can also make Docker the default for specific commands:

```toml
# .ateam/config.toml
[supervisor]
code_profile = "docker"           # only code runs in Docker
# or:
# default_profile = "docker"     # everything runs in Docker
```

**Pros**: mix profiles per command (sandbox for report, Docker for code), per-project Docker customization, parallel containers for report roles.
**Cons**: more configuration for complex projects.

## Getting Started with Docker Profiles

### Step 1: Store API Keys

Agents inside Docker need API keys forwarded as environment variables:

```bash
ateam secret CLAUDE_CODE_OAUTH_TOKEN    # recommended (uses your subscription)
# or:
ateam secret ANTHROPIC_API_KEY          # API key (pay as you go)
```

### Step 2: Verify Docker Works

Test that your project builds and runs tests inside Docker:

```bash
ateam run "run the build command and tests, report issues but don't fix them" --profile docker
```

This builds the default Docker image, mounts your code, and runs the agent inside the container.

### Step 3: Choose a Profile

| Profile | Container | Use case |
|---------|-----------|----------|
| `docker` | Fresh container per command | Default Docker isolation |
| `docker-persistent` | Long-lived container, reuses state | Slow-to-setup environments (large `npm install`, DB migrations) |
| `devcontainer` | Project's `.devcontainer/` | Already using devcontainers |

```bash
ateam code --profile docker-persistent
```

### Step 4: Customize for Your Project

If the default Dockerfile works, you're done. If your project needs extra Docker args (ports, volumes, env vars), see [Per-Project Customization](#per-project-customization) below.

## How Docker Containers Work

### Container Lifecycle

**Oneshot mode** (`docker` profile): each command runs `docker run --rm`, creating a fresh container and destroying it after. Clean but slower if setup is expensive.

**Persistent mode** (`docker-persistent` profile): a long-lived container is started with `docker run -d ... sleep infinity`, then commands are executed via `docker exec`. The container persists across commands within a single ATeam invocation — useful when setup (package install, DB init) is slow.

### What Gets Mounted

| Host path | Container path | Mode | Purpose |
|-----------|---------------|------|---------|
| Git root (or project source) | `/workspace` | `ro` (report/review) or `rw` (code/run) | Source code |
| `.ateam/` | `/workspace/.ateam/` | `rw` | Agent state, logs, artifacts |
| `.ateamorg/` | `/.ateamorg/` | `rw` | Organization config |
| `~/.claude/.credentials.json` | `/home/agent/.claude/.credentials.json` | `ro` | OAuth token session context (only with `mount_claude_config = true`) |
| `/etc/localtime` | `/etc/localtime` | `ro` | Host timezone (Linux/macOS) |

Source code is mounted read-only by default. The `code` and `run` commands mount it read-write so agents can modify files.

When the project is in a subdirectory of the git root (e.g. `repo/myapp/`), the entire git root is mounted at `/workspace` and the working directory is set to `/workspace/myapp`.

### The Default Dockerfile

ATeam ships with a default Dockerfile (`defaults/Dockerfile`) based on `node:20-bookworm-slim`:
- Installs: git, curl, python3, ripgrep, jq, make, Claude Code CLI
- Creates an `agent` user matching your host UID (so file ownership is correct on bind mounts)
- Working directory: `/workspace`

To customize, copy and modify:

```bash
cp defaults/Dockerfile .ateam/Dockerfile
# Edit .ateam/Dockerfile — it will be picked up automatically
```

Or place it in `.ateamorg/Dockerfile` to share across all projects.

### Dockerfile Resolution Order

ATeam looks for a Dockerfile in this order (first match wins):

1. `.ateam/Dockerfile` (project-specific)
2. `.ateamorg/Dockerfile` (organization-wide)
3. `.ateamorg/defaults/Dockerfile` (org defaults installed by `ateam update`)
4. Embedded default Dockerfile (built into the ateam binary)

This follows the same 4-level resolution as all ateam config: project > org override > org defaults > embedded.

## Per-Project Customization

For projects with multi-tier stacks (database, app server, caches), you can add Docker args, port forwards, volumes, and environment variables in `config.toml`:

```toml
# .ateam/config.toml

[container-extra]
# Raw docker run args — ports, named volumes, resource limits, env-file, etc.
extra_args = [
  "-p", "3000:3000",
  "-p", "5432:5432",
  "-v", "pgdata:/pgdata",
  "-v", "node-modules:/workspace/node_modules",
]

# Forward additional host env vars into the container
forward_env = ["PORT", "DATABASE_URL"]

# Set literal env vars (not dependent on host values)
[container-extra.env]
DB_HOST = "localhost"
DB_PORT = "5432"
BIND_HOST = "0.0.0.0"
```

All fields are **additive** — they extend whatever the HCL runtime config already defines.

### Field Reference

| Field | Type | Applies to | Purpose |
|-------|------|-----------|---------|
| `extra_args` | `string[]` | `docker run` only | Raw Docker flags: `-p`, `-v`, `--cpus`, `--memory`, `--network`, `--env-file`, etc. |
| `forward_env` | `string[]` | `docker run` and `docker exec` | Forward host env var values into the container (via `-e KEY=VALUE`) |
| `env` | `map[string]string` | `docker run` and `docker exec` | Set literal env vars inside the container |

**Important**: `extra_args` only applies to `docker run` (container creation). In persistent mode, subsequent commands use `docker exec` which only takes `-e` and `-w`. If you need env vars available on every exec, use `forward_env` or `env` instead of raw `-e` in `extra_args`.

### Example: Full-Stack Web App

```toml
# .ateam/config.toml
[project]
name = "my-webapp"

[container-extra]
extra_args = [
  "-p", "3000:3000",
  "-p", "5432:5432",
  "-v", "pgdata:/var/lib/postgresql/data",
  "--env-file", ".env",
]
forward_env = ["DATABASE_URL"]

[container-extra.env]
NODE_ENV = "test"
DB_HOST = "localhost"
```

```dockerfile
# .ateam/Dockerfile
FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep jq make \
    postgresql postgresql-client \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ARG USER_UID=1000
RUN useradd -m -u $USER_UID agent || true

# Start postgres on container boot (for persistent mode)
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]

USER agent
WORKDIR /workspace
```

### Why config.toml Instead of runtime.hcl?

HCL config merges at the block level — if a project redefines a `container` block to add `extra_volumes`, it **replaces the entire container definition**, losing `forward_env`, `dockerfile`, `mode`, etc. from defaults.

`config.toml`'s `[container-extra]` section is purely additive: it appends to whatever the HCL config defines. This follows the same pattern as `[sandbox-extra]`.

For per-profile Docker args without leaving HCL, use `container_extra_args` on a profile block:

```hcl
profile "docker-custom" {
  agent     = "claude"
  container = "docker"
  container_extra_args = ["-p", "3000:3000", "--cpus", "2"]
}
```

## Precheck Scripts (Persistent Mode)

For persistent containers that need setup before each agent run (e.g., starting a database, running migrations), you can define a precheck script. ATeam executes it inside the container before each command.

Place the script in your project:

```bash
# .ateam/precheck.sh
#!/bin/sh
pg_isready -q || pg_ctlcluster 15 main start
```

And reference it in your runtime config:

```hcl
# .ateam/runtime.hcl
container "docker-persistent" {
  type        = "docker"
  mode        = "persistent"
  dockerfile  = "Dockerfile"
  precheck    = "precheck.sh"   # relative to .ateam/
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

## Running the Supervisor in Docker

Typically only the `code` action benefits from Docker (it needs to build and run tests). Report and review are read-only analyses that work fine in the default sandbox.

If you want the supervisor itself to run in Docker and launch coding agents within it, a Linux build of ATeam must be available inside the container:

```bash
make companion
mkdir -p "$HOME/.ateamorg/cache"
ln -sf "$(pwd)/ateam-linux-amd64" "$HOME/.ateamorg/cache/"
```

ATeam auto-detects and mounts the Linux binary at `/usr/local/bin/ateam` inside the container.

## Troubleshooting

### Image Build Fails

Check that Docker is running and your Dockerfile is valid:

```bash
docker build -t test -f .ateam/Dockerfile .
```

### Agent Can't Access API

Verify your API key is stored:

```bash
ateam secret ANTHROPIC_API_KEY        # re-enter if needed
ateam env                              # check "secrets" section
```

### Container Can't Run Tests

The default image is minimal. If your tests need additional tools, create a custom Dockerfile in `.ateam/Dockerfile`.

### Debug the Docker Command

Use `ateam inspect` after a run to see the exact `docker run` or `docker exec` command that was used, including all mounts, env vars, and args.

### Persistent Container Issues

If a persistent container gets into a bad state:

```bash
docker rm -f ateam-$(basename $(pwd))-<profile>
```

The container will be recreated on the next run.

## Known Limitations

- **No macOS guest**: Docker containers run Linux — can't test macOS-specific code
- **Docker Sandbox** (`--profile docker-sandbox`): experimental, uses Docker Desktop 4.58+ microVMs. Limited to one synced workspace, can't build Docker images inside it, and inner containers have restricted networking
- **Parallel report roles**: in oneshot mode, each role gets its own container (works well). In persistent mode, all roles share one container
- **Entrypoint env vars not available in persistent mode**: Environment variables set by a custom `ENTRYPOINT` script (e.g., `export DB_URL=...`) are only visible to the container's PID 1 process. In persistent mode, `docker exec` starts a new process that does NOT inherit these. Use `[container-extra.env]` in config.toml instead — it passes `-e` flags to both `docker run` and `docker exec`
- **Named volumes persist across runs**: Volumes created via `extra_args` (e.g., `-v pgdata:/pgdata`) survive container removal — ateam uses `docker rm -f` without the `-v` flag. Clean up manually with `docker volume rm <name>` if needed
