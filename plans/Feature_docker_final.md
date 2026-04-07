# Docker Support Redesign

## Problem Statement

Running ateam inside Docker requires too much configuration and tribal knowledge. The auth priority order is backwards, sandbox dependencies surprise users, and there's no clean path for interactive+headless coexistence.

## Current State

### What works well
- `ateam run` with `CLAUDE_CODE_OAUTH_TOKEN` in `-p` mode (headless) — reliable, the core use case
- `ateam secret` makes token management straightforward on the host
- Profile/container/agent resolution is flexible (per-role, per-action overrides)
- Docker image builds leverage layer caching

### What's broken or confusing

**1. Auth priority is backwards**

Claude Code resolution: `ANTHROPIC_API_KEY` > `CLAUDE_CODE_OAUTH_TOKEN` > interactive login.

This is the opposite of what users want because:
- Interactive login is the most capable (full-scope, Remote Control, cheapest for subscription users)
- `CLAUDE_CODE_OAUTH_TOKEN` is headless-only (inference-only, no Remote Control)
- `ANTHROPIC_API_KEY` is the most expensive and least featured

Having both set causes silent behavior changes or require to have to login unexpectedly. Setting `ANTHROPIC_API_KEY` for one tool can break Claude's interactive session. We can't change Claude's priority order — we can only manage which env vars reach the agent.

**2. Default agent uses sandbox, which fails in containers**

The `default` profile uses `claude` agent which requires sandbox (bubblewrap, socat). These aren't installed in typical Docker images. First run fails with a confusing error. Users must know to:
- Either set `default_profile = "docker"` in config.toml
- Or pass `--profile docker` on every command
- Or install bubblewrap/socat in their Dockerfile

**3. No documented path for interactive auth in containers**

- macOS keychain credentials can't be used inside Linux containers
- `CLAUDE_CODE_OAUTH_TOKEN` doesn't work for interactive sessions
- Browser login inside containers works but credentials are lost on rebuild
- Refresh token bootstrap (`CLAUDE_CODE_OAUTH_REFRESH_TOKEN`) works but is undocumented and requires a specific multi-step flow
- The token shown in the browser is NOT the refresh token (common mistake)

**4. Container lifecycle is ambiguous**

Multiple overlapping patterns, no clear recommendation:
- Ateam manages the container (oneshot/persistent profiles)
- Ateam is exec'd into an externally managed container
- Docker-compose / devcontainer setups
- Docker-in-docker for projects that need Docker in tests

**5. ateam binary in containers**

Three ways to get it there, each with tradeoffs:
- Cross-compile + mount (dev workflow, breaks on inode change)
- Bake into image (requires rebuild on every code change)
- Mount build directory (works but requires entrypoint symlink)

## Goals

1. **Out of the box**: `ateam report --profile docker` works with as little extra config as possible and as predicable as possible
2. **Safe defaults**: no permission prompts inside containers, sandbox outside
3. **Predictable auth**: clear separation of headless vs interactive credentials. Be resilient to future changes or at least easy to detect and debug
4. **Low config**: minimal setup for the common case
5. **Container-agnostic**: support managed, external, compose, devcontainer patterns

## Proposed Changes

### A. Environment-aware agent args and sandbox

Instead of separate `claude` and `claude-docker` agents, add three new agent-level fields:

- `args_inside_container` — extra CLI args when running inside Docker (detected via `/.dockerenv`)
- `args_outside_container` — extra CLI args when running on the host
- `sandbox_inside_container` — bool, default `false`. Controls whether the sandbox settings file is written inside Docker.

Final args: `args` + (`args_inside_container` OR `args_outside_container` depending on environment).

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox

  args_inside_container    = ["--dangerously-skip-permissions"]
  args_outside_container   = []
  sandbox_inside_container = false  # default: skip sandbox in Docker

  env = { CLAUDECODE = "" }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

agent "codex" {
  command = "codex"
  args    = ["--ask-for-approval", "never"]

  args_inside_container    = []
  args_outside_container   = ["--sandbox", "workspace-write"]
  sandbox_inside_container = false

  required_env = ["OPENAI_API_KEY"]
}
```

**Overrides via inheritance:**

```hcl
# Keep sandbox even inside Docker (bubblewrap must be installed)
agent "claude-with-sandbox" {
  base                     = "claude"
  sandbox_inside_container = true
  args_inside_container    = []     # don't skip permissions either
}
```

**Runner logic:**

```
final_args = agent.args
if isInContainer():
    final_args += agent.args_inside_container
    if !agent.sandbox_inside_container:
        skip sandbox settings file
else:
    final_args += agent.args_outside_container
    write sandbox settings file if agent.sandbox is set
```

**What this eliminates:**
- `claude-docker` agent (replaced by `claude` with `args_inside_container`)
- `docker` profile needing a separate agent — the `default` profile works everywhere
- The "doesn't work out of the box inside Docker" problem

**What stays the same:**
- `sandbox` field and its existing mechanism (JSON blob → temp file → `--settings PATH`)
- `args` field
- Agent inheritance (`base`)
- Profile/container resolution

**Deferred (related but orthogonal):**
- Template variable expansion for settings files (e.g., `{{SANDBOX_FILE}}`) — would allow moving sandbox from a magic runner behavior to explicit args like `["--settings", "{{SANDBOX_FILE}}"]`. Cleaner but bigger refactor of how settings files work.
- Renaming `sandbox` to `sandbox_file_content` or similar for clarity
- Generic "dump any agent field to file" mechanism — useful for agents that need config files generated from HCL
- Expanding the template variable system (`{{CONTAINER_NAME}}`, `{{PROJECT_DIR}}`, etc.) for complex environment management

These may need to happen as part of the broader docker refactor but don't block the container experience fix.

### B. Secret management across Docker boundaries

The core problem: secrets stored in OS keychains don't cross into containers, and env vars cause conflicts between headless ateam and interactive claude.

**New `ateam secret` flags:**

```bash
# Print raw KEY=VALUE to stdout (for piping/redirecting)
ateam secret --print
ateam secret CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY --print

# Resolve from any source and write to .ateam/secrets.env (overwrite)
ateam secret --save-project-scope
ateam secret CLAUDE_CODE_OAUTH_TOKEN --save-project-scope
```

`--print` outputs raw values (no masking) so it can be sourced or redirected:
```bash
ateam secret --print > /path/to/secrets.env
```

`--save-project-scope` resolves secrets from env/keychain/global and writes to `.ateam/secrets.env`. Reports what changed:
```
  [added]   CLAUDE_CODE_OAUTH_TOKEN
  [changed] ANTHROPIC_API_KEY
  [same]    OPENAI_API_KEY
Wrote 3 secret(s) to .ateam/secrets.env
```

When no names given: writes all resolved secrets. When names given: updates those keys only, preserves others.

**`ateam env` enhancement:**

When a secret exists at both project and global scope with different values:
```
  Auth: CLAUDE_CODE_OAUTH_TOKEN=sk-a...zQAA (project, file)
        ⚠ project value overrides global value
```

**How secrets flow in each scenario:**

**1. Host (with or without sandbox)**

Standard resolution: env → project → org → global. Set once globally, done:
```bash
ateam secret CLAUDE_CODE_OAUTH_TOKEN --set   # global scope, one-time
```

**2. Docker one-shot (ateam on host → container)**

Ateam on the host resolves secrets, passes via `docker run -e KEY=VALUE`. Standard `forward_env` mechanism, unchanged. Secrets live only for the container's lifetime.

**3. Docker exec (ateam on host → exec into user-managed container)**

Same as one-shot: `docker exec -e KEY=VALUE`. Resolved per-exec, not persisted.

**4. Inside Docker (ateam runs independently in the container)**

The critical case. Setup on the host, before entering or after any secret change:
```bash
ateam secret --save-project-scope
```

This writes resolved secrets to `.ateam/secrets.env`. Since `.ateam/` is already mounted in containers, secrets are immediately available.

Inside the container:
- `ateam run "do something"` → `ValidateSecrets` resolves `CLAUDE_CODE_OAUTH_TOKEN` from `.ateam/secrets.env` (project scope) → injects into process env via `os.Setenv` → passes to claude child process. Works.
- `claude` (interactive, run directly by user) → checks `os.Getenv` (nothing — the user's shell has no `CLAUDE_CODE_OAUTH_TOKEN`) → falls back to stored credentials (`.credentials.json` from browser login) → interactive session works. **No conflict.**

The key insight: `os.Setenv` in `ValidateSecrets` only affects the ateam process and its children. The user's shell env is unaffected. So ateam's headless agents get the token, interactive claude doesn't see it.

**What `forward_env` does in each mode:**

| Mode | Secret transport | Persists? |
|------|-----------------|-----------|
| Host | resolver (env/project/global) | N/A |
| Docker one-shot | `docker run -e` from `forward_env` | No (container removed) |
| Docker exec | `docker exec -e` from `forward_env` | No (per-exec) |
| Inside Docker | `.ateam/secrets.env` via project scope | Yes (in mounted .ateam/) |

**Credential cleanup:** remove `ANTHROPIC_API_KEY` from default `forward_env` in container definitions. Users add it explicitly if needed. This prevents accidental priority conflicts.

### C. Two container types: `docker` and `docker-exec`

Drop `docker-persistent`/`docker-managed`. Two types cover all cases:

**`docker` — ateam-managed, oneshot build+run**

Ateam builds the image from a Dockerfile and runs a fresh container per command. The existing oneshot mode. For CI, simple projects, and isolation-focused workflows.

```hcl
container "docker" {
  type        = "docker"
  dockerfile  = "Dockerfile"
  copy_ateam  = true                      # default: copy ateam binary into container
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN"]
}
```

**`docker-exec` — user-managed, exec into existing container**

Ateam does NOT build, start, or stop. It execs into a container managed by the user (docker-compose, devcontainer, manual `docker run`, etc.). The `precheck` script is the user's hook for any lifecycle management they want — from a health check to a full `docker compose up -d`.

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"          # required: name of running container
  precheck         = "docker-precheck.sh"  # optional: runs before every exec
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN"]
}

profile "my-app" {
  agent     = "claude"
  container = "my-app"
}
```

Running `ateam run --profile my-app "do something"`:
1. Runs `precheck.sh` (user's hook — can start compose, verify health, install deps)
2. Execs `docker exec my-app-dev ateam run ...` with forwarded env vars

**Example precheck scripts:**

```bash
# Simple: just verify the container is running
#!/bin/bash
docker ps --format '{{.Names}}' | grep -q my-app-dev || {
    echo "Container my-app-dev not running. Start with: docker compose up -d"
    exit 1
}
```

```bash
# Full lifecycle: start if not running
#!/bin/bash
if ! docker ps --format '{{.Names}}' | grep -q my-app-dev; then
    docker compose up -d
    sleep 2
fi
```

**No `ateam docker build/start/stop` commands.** Ateam doesn't manage container lifecycle. Users manage their own containers and call precheck scripts directly if needed.

| | `docker` | `docker-exec` |
|---|---|---|
| Who manages container | ateam | user |
| Dockerfile needed | yes | no |
| Image building | automatic | none |
| Container lifecycle | ateam (one-shot) | external (precheck hook) |
| ateam binary | copied or mounted | must be pre-installed or mounted |
| Use case | simple projects, CI | compose, devcontainer, complex apps |

**Future**: the `exec` command could be templated (`exec = "docker exec {{CONTAINER}} {{CMD}}"`) to support podman, remote hosts, or VMs. Not needed now — default `docker exec` covers current use cases.

**Dynamic container names**: for multi-work-area setups (same project, multiple checkouts, each with its own container), the simplest approach is to run ateam inside each container rather than exec'ing from the host — this eliminates container name resolution entirely. For the rare host→container case, the future template system would support `docker_container = "myapp-{{WORK_AREA}}"` with env var expansion. Undefined vars produce a clear error. No registry or required/optional semantics needed.

### D. Interactive sessions in containers

For interactive claude inside Docker (Remote Control, development):

**One-time setup:**
```bash
# Start container, do browser login
claude                                    # complete browser login → /exit
ateam agent-auth --save-refresh-token     # extract and save refresh token
```

**Any new container (no browser needed):**
```bash
ateam agent-auth --method regular --exec
# Claude exchanges refresh token → stores .credentials.json → interactive session
```

The refresh token is stored via `ateam secret` (global scope) and survives container rebuilds. See `plans/ResearchClaudeExecution.md` for details on the refresh token flow.

**Coexistence (headless + interactive in same container):**

This works naturally with the `.ateam/secrets.env` approach (section B):
- `ateam run` → resolves `CLAUDE_CODE_OAUTH_TOKEN` from `.ateam/secrets.env` via `ValidateSecrets` → `os.Setenv` → child process only
- `claude` (interactive) → user's shell has no `CLAUDE_CODE_OAUTH_TOKEN` → uses `.credentials.json` → no conflict

### E. ateam binary in containers

**`copy_ateam`** (default: `true` for `docker` type)

For `docker` oneshot containers, ateam cross-compiles and copies the linux binary into the container during image build. Users don't need to pre-install ateam or manage mounts. Set `copy_ateam = false` for dev workflows with directory-mount live-reload.

For `docker-exec` containers, ateam must be pre-installed in the container image, mounted by the user, or copied via `docker cp` in the precheck script.

**Dev workflow**: mount `build/` directory for live-reload:
```
make companion   → build/ateam-linux-amd64
docker run -v ./build:/opt/ateam:ro ...
```
Directory mount tracks by filename, so `make companion` updates are visible to running containers without restart.

### F. Isolated agent config directories

Use `CLAUDE_CONFIG_DIR` to create fully segregated agent environments in arbitrary directories. Each gets its own credentials, settings, plugins, and session history.

Use cases:
- Multiple agents with different auth (one interactive, one headless) in the same container
- Project-specific agent config that doesn't pollute `~/.claude`
- Testing different Claude Code configurations side by side

Two modes:
- **Interactive isolated**: has its own browser login stored in `<dir>/.credentials.json`. Setup driven by `ateam agent-auth --config-dir <dir>`.
- **Background isolated**: uses `CLAUDE_CODE_OAUTH_TOKEN` only. No stored credentials needed.

Already partially implemented: `claude-isolated` agent in runtime.hcl sets `config_dir = ".claude"` relative to `.ateam/`. Needs: better onboarding, `ateam agent-auth` support for arbitrary config dirs, docs.

### G. Better error messages inside Docker

When inside Docker (`/.dockerenv` exists) and credentials are missing, ateam should detect this and print container-specific guidance:

```
Error: CLAUDE_CODE_OAUTH_TOKEN is required inside Docker containers.

Setup:
  On the host:  ateam secret CLAUDE_CODE_OAUTH_TOKEN --set
  In runtime.hcl, ensure forward_env includes CLAUDE_CODE_OAUTH_TOKEN
  Or pass directly: docker run -e CLAUDE_CODE_OAUTH_TOKEN=... 
```

Outside Docker, `CLAUDE_CODE_OAUTH_TOKEN` is optional (interactive login works). Inside Docker, it's required for headless agents. The error message should reflect this.

### H. Safety guard on `--dangerously-skip-permissions`

Add a pre-run check: refuse to run agents with `--dangerously-skip-permissions` unless inside a container (detected via `/.dockerenv`). This prevents accidentally running unsandboxed agents on the host. Can be overridden with `--force`.

## Additional Issues from Prior Research

### Auth credential persistence

- **Linux containers**: credentials go to `~/.claude/.credentials.json` (no OS keychain)
- **macOS host**: credentials in macOS Keychain, NOT in `~/.claude/` — can't be mounted into containers
- **Minimal persistent set**: only `.credentials.json`, `.claude.json`, `settings.json` need to survive container rebuilds. Don't persist `sessions/`, `session-env/`, `backups/`, `statsig/` — stale state causes auth issues
- **Refresh token expiry**: `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` is long-lived but not forever. Need to re-login and re-extract when it expires

### Docker-compose and multi-service

- Compose services discover each other by name (e.g., `DB_HOST=postgres`)
- Named volumes have lifecycle tied to `docker compose down -v`
- Health checks via `depends_on: condition: service_healthy`
- The compose file becomes the source of truth, potentially replacing parts of HCL config

### External containers

Some users manage their own containers (docker-compose, devcontainer, manually). Ateam should support pointing to a user-managed container by name:
```toml
[container]
docker_container = "my-dev-container"
```
Then ateam does `docker exec my-dev-container ateam ...` without managing the lifecycle.

### UID alignment

Bind-mounting host dirs requires matching UIDs. Current approach: `--build-arg USER_UID=$(id -u)`. This works for single-user setups. Multi-user or CI environments may need different approaches.

### Docker-in-docker

For projects whose tests require Docker (building images, running containers), options:
- `--privileged` DinD: full Docker daemon inside container, kernel-sharing security risk
- Sidecar pattern: separate Docker daemon container, shared socket
- Docker-sandbox (microVM): isolated Docker daemon, requires Docker Desktop 4.58+

### Network isolation

Docker doesn't isolate network by default. Options for restricting agent network access:
- iptables default-deny with allowlist (Anthropic's reference devcontainer pattern)
- Proxy-based domain filtering (Greywall, Claude's built-in SRT)
- Docker `--network none` + explicit port forwarding
- Docker-sandbox `network_policy = "deny"` (microVM level)

### Host-local service access

Integration tests often need to reach host services (database, dev server). Docker containers use `host.docker.internal` but this needs explicit config. Not all Docker setups support it.

## Open Questions

1. **Should `forward_env` include `ANTHROPIC_API_KEY` by default?** Current: yes. Proposed: no (remove from container definitions, users add explicitly if needed).
2. **devcontainer**: is it a flavor of `docker-exec` (just set `docker_container` to the devcontainer name) or does it need its own type?
3. **Auto-rebuild detection**: compute hash from Dockerfile + binary version, rebuild only when changed? (docker-sandbox already does this)
4. **`exec` property templating**: future-proof `docker-exec` with `exec = "docker exec {{CONTAINER}} {{CMD}}"` to support podman/remote? Or wait for real demand?
5. **`agent-auth` vs `agent-config`**: rename to broader `agent-config` with `--auth` subcommand? Or fold auth audit into `ateam env`?
6. **`--export-auth`**: experimental flag to print token + instructions for replicating auth in another environment. Useful but scope creep?

## Priority Order

1. **Secret management** (B) — `--save-project-scope` and `--print` on `ateam secret`, project/global override warning in `ateam env`. Unblocks "ateam inside Docker" without env var conflicts.
2. **Environment-aware agent args** (A) — `args_inside_container` + `sandbox_inside_container` eliminates `claude-docker`, one agent definition works everywhere
3. **`docker-exec` container type** (C) — supports compose/devcontainer/external containers with precheck lifecycle hooks
4. **`copy_ateam` default** (E) — ateam binary auto-included in `docker` containers
5. **Docker error messages** (G) — container-specific guidance when credentials are missing
6. **Safety guard** (H) — runtime warning if `--dangerously-skip-permissions` used outside Docker
7. **Document refresh token flow** (D) — already works, needs user-facing docs
8. **Isolated agent configs** (F) — enables interactive+headless coexistence, future work
