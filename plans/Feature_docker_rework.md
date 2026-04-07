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

### I. `ateam run --dry-run`

The `run` command is the only core execution command without `--dry-run` (`report`, `review`, `code`, `parallel` all have it). For Docker debugging, users need to see the exact commands, container config, and secret resolution before running.

**Output format:**

```
╔══ dry-run ══╗

Agent:     claude
Profile:   docker-persistent
Container: docker (persistent, ateam-projects_myapp-security)

Command:
  claude -p --output-format stream-json --verbose --dangerously-skip-permissions

Docker:
  docker exec -i -w /workspace -e CLAUDE_CODE_OAUTH_TOKEN=sk-a...zQAA ateam-projects_myapp-security claude ...

Secrets:
  CLAUDE_CODE_OAUTH_TOKEN  ✓ found (project, file)
  ANTHROPIC_API_KEY        ✗ not found

Settings (sandbox):
  { ... merged JSON ... }

Prompt:
  <the resolved prompt text>

╚══ dry-run ══╝
```

**Key behaviors:**
- Shows secret resolution without injecting into env (display-only, uses masking)
- Missing secrets show `✗ not found` instead of erroring — the point is diagnosis
- Skip `ValidateSecrets` in dry-run (pass `skipSecretValidation` flag to `newRunner`)
- Uses existing `DebugCommandArgs`, `DebugCommand`, `RenderSettings` methods

**Files:** `cmd/run.go` (flag + print block), `internal/secret/validate.go` (add `ResolveAllRequired` function)

## Implementation Status

All planned features have been implemented:

| # | Goal | Status | Commit |
|---|------|--------|--------|
| A | Environment-aware agent args | **Done** | `args_inside_container`, `args_outside_container`, `sandbox_inside_container` on `AgentConfig`. One `claude` agent works everywhere. `claude-docker` kept as backward-compat alias. |
| B | Secret management | **Done** | `ateam secret --print` and `--save-project-scope`. `ateam env` warns on project/global override. |
| C | `docker-exec` container type | **Done** | New container type with `docker_container`, `precheck`, and `exec` template (`{{CONTAINER}}`, `{{CMD}}`). |
| D | Interactive sessions (refresh token) | **Done** | `ateam agent-config --setup-interactive` and `--save-refresh-token`. |
| E | `copy_ateam` for docker-exec | **Done** | `copy_ateam = true` on `docker-exec` auto-copies via `docker cp`. `ateam container-cp` command for manual use. |
| F | Isolated agent config dirs | **Done** (pre-existing) | `claude-isolated` in runtime.hcl, `Runner.ConfigDir`, `CLAUDE_CONFIG_DIR`. |
| G | Docker error messages | **Done** | `ValidateSecrets` detects Docker and prints container-specific setup instructions. |
| H | Safety guard | **Done** | Runner warns when `--dangerously-skip-permissions` used outside containers. |
| I | `run --dry-run` | **Done** | Shows agent, profile, container, docker command, secret resolution, sandbox, prompt. |
| J | `agent-config` command | **Done** | Renamed from `agent-auth`. Has `--audit`, `--setup-interactive`, `--wipe-i-am-sure`. All marked experimental. |

**Key files changed:**
- `cmd/run.go` — `--dry-run` flag
- `cmd/secret.go` — `--print`, `--save-project-scope`
- `cmd/agent_config.go` — new command (replaced `agent_auth.go`)
- `cmd/container_cp.go` — new command
- `cmd/env.go` — project/global override warning
- `cmd/table.go` — `docker-exec` in `buildContainer`, `findLinuxBinary` searches `build/`
- `internal/runtime/config.go` — `ArgsInsideContainer`, `ArgsOutsideContainer`, `SandboxInsideContainer`, `DockerContainer`, `ExecTemplate`, `CopyAteam`
- `internal/runner/runner.go` — env-aware args, sandbox skip, safety guard, `DockerExecContainer` support
- `internal/container/docker_exec.go` — new container type
- `internal/secret/validate.go` — Docker error messages, `ResolveAllRequired`
- `internal/agent/claude_auth.go` — auth detection, cleanup, refresh token
- `defaults/runtime.hcl` — `claude` with `args_inside_container`, profiles use `claude` instead of `claude-docker`

**Superseded plans** (deleted):
- `FEATURE_ateam_in_docker.md` — transparent re-exec rejected
- `FEATURE_docker_alt_designs.md` — safety guard done, lifecycle deferred
- `FEATURE_docker_refactoring.md` — `docker-exec` implemented here
- `FEATURE_container_docker_compose.md` — superseded by `docker-exec`

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

## Resolved Questions

1. **`forward_env` and `ANTHROPIC_API_KEY`**: keep `ANTHROPIC_API_KEY` in default `forward_env`. Both tokens should be available in containers — the secret management design (section B) handles the coexistence problem by using file-based resolution instead of env vars.

2. **devcontainer**: no separate type. Use `docker-exec` with a precheck script that runs the devcontainer CLI:
   ```hcl
   container "devcontainer" {
     type             = "docker-exec"
     docker_container = "my-project-devcontainer"
     precheck         = "devcontainer-precheck.sh"
     forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
   }
   ```
   Example precheck:
   ```bash
   #!/bin/bash
   # devcontainer-precheck.sh
   if ! docker ps --format '{{.Names}}' | grep -q my-project-devcontainer; then
       devcontainer up --workspace-folder .
   fi
   ```
   Document as an example in REFERENCE.md.

3. **Auto-rebuild detection**: removed. The `docker` oneshot type already rebuilds on every run (layer cache handles speed). The docker-sandbox hash detection is sandbox-specific. No need for a general mechanism.

4. **`exec` property templating**: evaluated below.

5. **`agent-auth` rename**: rename to `agent-config` with subcommands. See section J below.

### Exec property templating evaluation

The `exec` property on `docker-exec` containers would allow custom exec commands:
```hcl
container "podman-app" {
  type             = "docker-exec"
  docker_container = "my-app"
  exec             = "podman exec {{CONTAINER}} {{CMD}}"
}

container "remote-app" {
  type             = "docker-exec"
  docker_container = "my-app"
  exec             = "ssh devbox docker exec {{CONTAINER}} {{CMD}}"
}
```

**Level of effort**: low-medium. The `docker-exec` container's `CmdFactory` already builds the `docker exec` command. Templating means: read the `exec` string, replace `{{CONTAINER}}` and `{{CMD}}`, parse into command + args. ~30 lines in `docker.go`, plus HCL schema change (~5 lines in `config.go`).

**What it abstracts**: the container runtime. `docker exec`, `podman exec`, `ssh ... docker exec`, `kubectl exec` — all become config, not code. The `docker-exec` type becomes a generic "exec into a named thing" type.

**Recommendation**: implement when building `docker-exec`. The marginal cost over hardcoded `docker exec` is small and the abstraction is clean. Default `exec` template: `"docker exec {{CONTAINER}} {{CMD}}"`.

### J. Rename `agent-auth` to `agent-config`

Rename the current `agent-auth` command to `agent-config` with clear subcommands. All marked as **experimental** in help text and output.

```
ateam agent-config --audit                    # show auth state, tokens, config
ateam agent-config --setup-interactive        # browser login + refresh token flow
ateam agent-config --wipe-i-am-sure           # nuke ~/.claude state (current cleanup)
```

**`--audit`** output:
```
[experimental] Claude Code Agent Configuration Audit

Config dir:       /home/agent/.claude
Active auth:      oauth (CLAUDE_CODE_OAUTH_TOKEN from project/file)

Secrets:
  ANTHROPIC_API_KEY:            (not set)
  CLAUDE_CODE_OAUTH_TOKEN:      sk-ant-oat01-... (project, file)
  CLAUDE_CODE_OAUTH_REFRESH_TOKEN: sk-ant-ort01-... (global, keychain)

Credentials file: present (.credentials.json)
  Refresh token:   sk-ant-ort01-Eqq29ZR...8Dx0bwAA

To use this auth in another environment:
  export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...
  # or for interactive sessions:
  export CLAUDE_CODE_OAUTH_REFRESH_TOKEN=sk-ant-ort01-...
  export CLAUDE_CODE_OAUTH_SCOPES="user:profile user:inference"
```

When an interactive login is detected (`.credentials.json` has refresh token), it prints the raw tokens with instructions for how to use them in other environments.

**`--setup-interactive`**: replaces current `--method regular --exec`. Runs `claude auth login` with refresh token if available, otherwise opens browser login.

**`--wipe-i-am-sure`**: replaces current cleanup + `--wipe-config-clean`. Removes all auth state, keeps `settings.json`, `plugins/`, `skills/`.

Existing `--method`, `--exec`, `--dry-run`, `--container-only` flags stay but become secondary (used by `--setup-interactive` internally).

## Resolved

- **`exec` templating**: implement alongside `docker-exec`. Default: `"docker exec {{CONTAINER}} {{CMD}}"`.
- **`--container-only` on `agent-config`**: `--audit` works everywhere (read-only). `--wipe-i-am-sure` and `--setup-interactive` respect `--container-only` (default true — refuse on host without override).

---

## Implementation Steps

Ordered for incremental delivery. Each step is independently useful, builds on the previous, and can be stopped/resumed. Run `make build && make test` after each step.

### Step 1: `ateam run --dry-run` [I]

**Goal:** diagnostic tool to see exactly what would execute.

**Files:**
- `internal/secret/validate.go` — add `ResolveAllRequired(ac, resolver) []ResolveDetail` type+function (~30 lines). Returns resolution details per secret without injecting into env.
- `cmd/run.go` — add `--dry-run` flag. After runner resolution but before execution, print: agent/profile/container info, agent command (`DebugCommandArgs`), docker command (if container), secret resolution (`ResolveAllRequired`), sandbox settings, prompt. Skip `ValidateSecrets` in dry-run path (add `skipSecretValidation` param to `newRunner`).

**Test:** `ateam run --dry-run "hello" --profile docker` shows full output without running.

### Step 2: `ateam secret --print` and `--save-project-scope` [B]

**Goal:** secrets cross Docker boundaries via `.ateam/secrets.env`.

**Files:**
- `cmd/secret.go` — add `--print` flag: resolve named secrets (or all), print raw `KEY=VALUE` to stdout, no masking. Add `--save-project-scope` flag: resolve secrets, write to `.ateam/secrets.env` via `FileStore.Set`, report added/changed/same for each key.
- `cmd/env.go` — in the auth display section, compare project-scope and global-scope values for each secret. Print `⚠ project value overrides global value` when they differ.

**Test:** `ateam secret --print` outputs raw values. `ateam secret --save-project-scope` writes to `.ateam/secrets.env` with diff report. `ateam env` shows override warning.

### Step 3: Environment-aware agent args [A]

**Goal:** one `claude` agent definition works inside and outside Docker.

**Files:**
- `internal/runtime/config.go` — add `ArgsInsideContainer`, `ArgsOutsideContainer` (both `[]string`), `SandboxInsideContainer` (`bool`, default `false`) to `AgentConfig`. Parse from HCL: `args_inside_container`, `args_outside_container`, `sandbox_inside_container`. Update `resolveInheritance` to merge these fields.
- `internal/runtime/config_test.go` — test parsing and inheritance of new fields.
- `defaults/runtime.hcl` — update `claude` agent: move `--dangerously-skip-permissions` to `args_inside_container`. Remove `claude-docker` agent. Update profiles that reference `claude-docker` to use `claude`. Update `codex` agent: move `--sandbox workspace-write` to `args_outside_container`.
- `internal/runner/runner.go` — in `Run()`, after building agent args: if `isInContainer()`, append `args_inside_container` and skip sandbox file if `!sandbox_inside_container`. Else append `args_outside_container`.
- `cmd/table.go` — add `isInContainer()` utility (or reuse from `cmd/agent_auth.go`). May need to move to a shared location.

**Test:** `make test`. `ateam run --dry-run "hello"` inside Docker shows `--dangerously-skip-permissions` in args, no sandbox. Outside Docker shows sandbox settings, no `--dangerously-skip-permissions`.

### Step 4: Docker error messages [G] and safety guard [H]

**Goal:** clear errors inside Docker, prevent `--dangerously-skip-permissions` on host.

**Files:**
- `internal/secret/validate.go` — in `ValidateSecrets`, if error and `isInContainer()`, append Docker-specific setup instructions to the error message.
- `internal/runner/runner.go` — before agent execution, if args contain `--dangerously-skip-permissions` (or codex equivalent) and NOT `isInContainer()`, print warning. If `--force` not set, refuse to run.

**Test:** missing secret inside Docker gives container-specific error. `--dangerously-skip-permissions` outside Docker warns/refuses.

### Step 5: Rename `agent-auth` to `agent-config` [J]

**Goal:** broader command with `--audit`, `--setup-interactive`, `--wipe-i-am-sure`.

**Files:**
- `cmd/agent_config.go` — rename from `cmd/agent_auth.go`. Change command name to `agent-config`. Add `--audit` (replaces default behavior — show auth state, print raw tokens when interactive login detected, show export instructions). Add `--setup-interactive` (replaces `--method regular --exec`). Add `--wipe-i-am-sure` (replaces cleanup + `--wipe-config-clean`). Mark all as `[experimental]` in output. `--audit` ignores `--container-only`. `--setup-interactive` and `--wipe-i-am-sure` respect it.
- `cmd/root.go` — replace `agentAuthCmd` with `agentConfigCmd`.
- `internal/agent/claude_auth.go` — no changes (logic stays the same).

**Test:** `ateam agent-config --audit` works everywhere. `ateam agent-config --wipe-i-am-sure` refuses on host without `--container-only=false`.

### Step 6: `docker-exec` container type with exec templating [C]

**Goal:** exec into user-managed containers.

**Files:**
- `internal/runtime/config.go` — add `DockerContainer` and `Exec` fields to `ContainerConfig`. Parse `docker_container` and `exec` from HCL.
- `internal/container/docker_exec.go` — new file. `DockerExecContainer` struct implementing `Container` interface. `CmdFactory` parses `exec` template (default `"docker exec {{CONTAINER}} {{CMD}}"`), replaces `{{CONTAINER}}` with container name, `{{CMD}}` with the agent command. `Precheck` runs the precheck script via `docker exec` or host shell. No `EnsureImage` or `EnsureRunning`.
- `cmd/table.go` — in `buildContainer`, handle `type = "docker-exec"`: create `DockerExecContainer`.
- `defaults/runtime.hcl` — add example `docker-exec` container definition (commented out).

**Test:** `make test`. Manual: configure a `docker-exec` container pointing to a running container, run `ateam run --dry-run --profile my-exec-profile "hello"`.

### Step 7: `copy_ateam` on docker containers [E]

**Goal:** ateam binary auto-included in oneshot containers.

**Files:**
- `internal/runtime/config.go` — add `CopyAteam` bool to `ContainerConfig` (default `true` for `docker` type).
- `internal/container/docker.go` — in image build or container start, if `CopyAteam`, use `findLinuxBinary` and `docker cp` (or Dockerfile COPY via build context) to include the binary.
- `defaults/runtime.hcl` — add `copy_ateam = true` to docker container definition.

**Test:** build a docker container without pre-installed ateam. `ateam run --profile docker "hello"` should find ateam inside.

### Step 8: Documentation [D]

**Goal:** user-facing docs for all the above.

**Files:**
- `REFERENCE.md` — update secret management section (add `--print`, `--save-project-scope`). Update container section (add `docker-exec` type, precheck examples, devcontainer example). Update agent section (add `args_inside_container` etc). Document `agent-config` command.
- `README.md` — update Docker quick start section.
- `DEV.md` — update Docker binary resolution section.
- `plans/ResearchClaudeExecution.md` — update container session persistence to reference `--save-project-scope` as the recommended approach.

### Step 9: Cleanup

**Goal:** remove dead code and old patterns.

**Files:**
- `defaults/runtime.hcl` — remove `claude-docker` agent (if not removed in step 3), remove `docker-persistent` container (superseded by `docker-exec`), update profiles.
- `cmd/agent_auth.go` — delete (replaced by `agent_config.go` in step 5).
- `test/docker-auth/` — update scripts to use `agent-config` instead of `agent-auth`.

## Priority Order

1. **`run --dry-run`** (I) — diagnostic tool showing exact commands, secret resolution, container config. Helps users debug Docker issues while bigger changes are in progress.
2. **Secret management** (B) — `--save-project-scope` and `--print` on `ateam secret`, project/global override warning in `ateam env`. Unblocks "ateam inside Docker" without env var conflicts.
3. **Environment-aware agent args** (A) — `args_inside_container` + `sandbox_inside_container` eliminates `claude-docker`, one agent definition works everywhere
4. **Docker error messages** (G) — container-specific guidance when credentials are missing. Quick win.
5. **Safety guard** (H) — runtime warning if `--dangerously-skip-permissions` used outside Docker. Quick win.
6. **`docker-exec` container type** (C) — supports compose/devcontainer/external containers with precheck lifecycle hooks
7. **`copy_ateam` default** (E) — ateam binary auto-included in `docker` containers
8. **Document refresh token flow** (D) — already works, needs user-facing docs
9. **Isolated agent configs** (F) — enables interactive+headless coexistence, future work

## Future Work

Features considered but intentionally deferred:

### Isolated agent config as default for ateam

Currently ateam outside containers uses `~/.claude` (the shared global Claude config — settings.json, hooks, permissions). Inside containers, `~/.claude` is empty or container-specific. This means ateam runs can behave differently inside vs outside containers because the settings differ.

A potential improvement: ateam could default to using an isolated config dir (e.g., `.ateam/.claude/`) for all agent runs, making behavior consistent regardless of environment. The existing `claude-isolated` agent and `config_dir` mechanism support this — it just needs to become the default.

Tradeoffs:
- Pro: predictable behavior, no interference from user's personal Claude settings
- Pro: eliminates "works on my machine" differences between host and container
- Con: loses access to user's custom skills, plugins, and hooks during ateam runs
- Con: requires initializing the isolated config on first use

### Template variable expansion for settings files

Allow `{{SANDBOX_FILE}}` in agent args to make sandbox settings explicit: `["--settings", "{{SANDBOX_FILE}}"]` instead of the current magic runner behavior. Part of a broader template system (`{{CONTAINER_NAME}}`, `{{PROJECT_DIR}}`, etc.).

### Generic "field to file" mechanism

Allow any HCL agent field to be dumped to a file and referenced in args. Useful for agents that need config files generated from HCL.

### Network isolation for containers

Docker doesn't isolate network by default. Options: iptables default-deny, proxy-based domain filtering, `--network none` + explicit forwarding. Not implemented — the current container isolation focuses on filesystem, not network.

### `container_name_script` or template variable for dynamic container names

For multi-work-area setups where container names can't be hardcoded. Deferred in favor of the advice: run ateam inside each container rather than exec'ing from the host. The future template system (`docker_container = "myapp-{{WORK_AREA}}"`) covers the rare host→container case.

### Secrets injection to `/run/ateam/` (tmpfs)

Write secrets to a tmpfs path inside the container (wiped on restart) instead of `.ateam/secrets.env` (persists on disk). More secure but more complex. Deferred in favor of the simpler `--save-project-scope` approach.

### `ateam agent-create` command

Create isolated agent directories with controlled auth and config import. Deferred — the existing `config_dir` field and `CLAUDE_CONFIG_DIR` mechanism handle the underlying need.

### Rename `sandbox` field to `sandbox_file_content`

Clarify that the `sandbox` field in agent definitions is JSON content that gets written to a file, not a sandbox configuration in the traditional sense.
