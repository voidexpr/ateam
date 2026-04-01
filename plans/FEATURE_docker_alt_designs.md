# Feature: Docker Mode — Alternative Designs

Companion to `FEATURE_ateam_in_docker.md`. This document compares the transparent re-exec proposal against what already exists in ateam's profile system and proposes a simpler path.

## What already exists

### Profile-based Docker execution

Ateam already runs agents inside Docker via profiles. The `docker-persistent` profile:

```
ateam report --profile docker-persistent
```

This starts a persistent container, mounts the project, and runs Claude inside it with `--dangerously-skip-permissions`. The container stays running between commands. The ateam binary is mounted at `/usr/local/bin/ateam:ro` so the agent can call ateam sub-commands from inside.

With a single line in `config.toml`:

```toml
[project]
default_profile = "docker-persistent"
```

Every command automatically uses Docker:

```
ateam report    # agent runs in Docker, no flag needed
ateam code      # same — supervisor and sub-runs all in Docker
```

### What the agent sees inside the container

The Docker container already has:
- Project source mounted (read-only by default, writable for code changes)
- `.ateam/` mounted read-write (for state, logs, reports)
- `.ateamorg/` mounted read-only
- ateam binary at `/usr/local/bin/ateam`
- Claude Code installed (via Dockerfile)
- `--dangerously-skip-permissions` (no permission prompts)
- Auth env vars forwarded (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`)

The code management supervisor already calls `ateam prompt`, `ateam run`, `ateam roles` from inside this container. So ateam-in-Docker is not new — it's how the agent workflow already operates.

### Key runtime.hcl definitions involved

```hcl
agent "claude-docker" {
  # --dangerously-skip-permissions for unattended use inside Docker
  args = ["-p", "--output-format", "stream-json", "--verbose",
          "--dangerously-skip-permissions"]
}

container "docker-persistent" {
  type = "docker"
  mode = "persistent"          # keeps container running between commands
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}

profile "docker-persistent" {
  agent     = "claude-docker"
  container = "docker-persistent"
}
```

## Transparent re-exec vs. existing profiles

### Side-by-side comparison

| Dimension | Existing `docker-persistent` | Transparent re-exec |
|---|---|---|
| **Where ateam runs** | Host | In Docker |
| **Where agent runs** | In Docker | In Docker (same container) |
| **Prompt assembly** | Host filesystem | Container filesystem |
| **Git pre/post checks** | Host | Container |
| **Build/test verification** | Host (or via `--supervisor-profile`) | Container |
| **Cross-compile ateam** | Required (host→linux) | Not needed (already linux) |
| **Host dependencies** | git, ateam binary | Docker + ateam binary only |
| **Keychain/secrets** | Direct host access | Needs forwarding |
| **`@filepath` args** | Host paths work naturally | Need host→container path mapping |
| **Implementation** | Already working | New re-exec path in every command |

### What re-exec adds

1. **Eliminates cross-compilation** — ateam runs natively as linux/amd64 inside the container. Currently needs `findLinuxBinary()` which cross-compiles or finds a companion binary.

2. **True single-environment** — Build/test checks during `ateam code` run in the same environment as the agent. Currently, if the supervisor runs on the host, `go build` pre-checks use the host toolchain while the agent uses the container toolchain. (Already solvable today with `--supervisor-profile docker-persistent`.)

3. **"Docker is the only dependency"** — Cleaner story for onboarding. No Go/git needed on host.

### What re-exec costs

1. **Re-exec path in every command** — `PersistentPreRun` must intercept, check config, exec into Docker. Every edge case (signals, stdin/stdout, TTY detection, exit codes) must be handled correctly.

2. **Secrets forwarding** — Host keychain is inaccessible from inside the container. The re-exec wrapper must resolve secrets on the host and inject them via `-e`. This is a new code path that can fail silently.

3. **Path translation** — `ateam code --review @/Users/me/custom_review.md` points to a host path. The re-exec wrapper must detect `@filepath` arguments and either reject host paths or map them into the container. The current profile approach doesn't have this problem because ateam runs on the host and resolves paths natively.

4. **Two execution paths** — Every command has a "am I on the host or in Docker?" branch. Debugging issues requires knowing which path you're on. The existing profile system has one path: ateam always runs on host, container config determines where the agent runs.

5. **Container lifecycle coupling** — Re-exec needs the container to be running before any command works. If the container crashes or is removed, every ateam command fails with a Docker error instead of a clear ateam error. The existing profile approach degrades gracefully — ateam itself always works, only Docker-profiled runs need the container.

### Environment consistency: already solvable

The main technical argument for re-exec is environment consistency. But this is already solvable:

```toml
[supervisor]
code_profile = "docker-persistent"    # supervisor runs in Docker too
```

Or on the command line:

```
ateam code --supervisor-profile docker-persistent --profile docker-persistent
```

Both supervisor and sub-runs use the same container. Build/test checks run in the container environment. No re-exec needed.

## Recommended approach: improve the existing profile system

Instead of adding re-exec, fill the gaps in the current Docker workflow:

### Gap 1: Onboarding (`ateam init --docker-mode`)

```
ateam init --docker-mode
```

Does:
1. Normal init (create `.ateam/`, `config.toml`)
2. Set in `config.toml`:
   ```toml
   [project]
   default_profile = "docker-persistent"

   [supervisor]
   default_profile = "docker-persistent"
   ```
3. Build the Docker image (`docker build`)
4. Start the persistent container
5. Optionally run `auto-setup` inside the container

This is a one-time setup. After this, every `ateam` command uses Docker automatically.

### Gap 2: Interactive shell (`ateam shell`)

```
ateam shell
```

Does:
- Resolves the persistent container for the current project
- Ensures it's running (start if needed)
- `docker exec -it ateam-<project-id> bash`

Simple wrapper — no re-exec complexity. The user explicitly enters Docker when they want an interactive session.

### Gap 3: Container lifecycle (`ateam docker`)

```
ateam docker status    # is the container running? image age? config hash?
ateam docker rebuild   # rebuild image + restart container
ateam docker stop      # stop the persistent container
ateam docker start     # start it if stopped
```

These manage the persistent container directly. Currently users must use raw `docker` commands.

### Gap 4: Auto-rebuild on changes

Steal from the docker-sandbox implementation (`internal/container/docker_sandbox.go`): compute a config hash from the Dockerfile content + ateam binary version. Store in `.ateam/cache/docker-config-hash`. On each run, if the hash changed, rebuild the image and restart the container.

This ensures the container stays current without manual intervention.

### Gap 5: Safety check on `claude-docker` agent

The `claude-docker` agent uses `--dangerously-skip-permissions`. Currently nothing prevents running it on the host (outside Docker), which would give the agent unrestricted access.

Add a pre-run check: if the resolved agent is `claude-docker` (or any agent with `--dangerously-skip-permissions`) and the container type is `"none"`, refuse to run unless an env var (`ATEAM_DOCKER=1`) or file (`/.dockerenv`) confirms we're inside a container.

```go
// In runner.go or table.go, before agent execution
if agent.HasSkipPermissions() && container.Type == "none" && !isInsideContainer() {
    return fmt.Errorf("agent %q uses --dangerously-skip-permissions but is not " +
        "running inside a container; use a docker profile or set execution_mode")
}
```

The check layers multiple signals to be resilient:
- `ATEAM_DOCKER=1` env var (set by ateam when starting the container)
- `/.dockerenv` file (created by Docker runtime)
- `/proc/1/cgroup` contains docker/containerd paths (Linux-specific)

Require at least 2 of 3 for extra safety.

### Gap 6: Secrets injection into persistent containers

The persistent container needs auth tokens. Currently handled via `forward_env` in the container config — env vars from the host process are passed to `docker run`/`docker exec`.

Improve this by integrating with `ateam secret --get`:
- When starting the persistent container, resolve secrets from the host keychain/secrets.env
- Pass them via `docker run -e` at container start
- For `docker exec`, inject them if not already in the container's environment
- If secrets change (e.g., OAuth token refresh), `ateam docker restart` picks up the new values

## Summary

| Gap | Solution | Complexity |
|---|---|---|
| Onboarding | `ateam init --docker-mode` | Low — flag + config writes |
| Interactive | `ateam shell` | Low — wrapper around `docker exec -it` |
| Lifecycle | `ateam docker start/stop/rebuild/status` | Medium — new subcommand |
| Auto-rebuild | Config hash detection | Low — steal from docker-sandbox |
| Safety check | Refuse `--dangerously-skip-permissions` outside Docker | Low — pre-run guard |
| Secrets | Resolve on host, inject into container | Low — extend existing `forward_env` |

Total: ~6 focused changes to the existing architecture. No new execution model, no re-exec path, no path translation. The profile system already handles where agents run — these changes just make the Docker workflow smoother to set up and operate.

## When transparent re-exec would be worth it

Re-exec becomes worthwhile if:
- Ateam needs to run on machines with no Go/git/toolchain at all (pure Docker host)
- The host is Windows where the native sandbox doesn't work
- You want CI runners that only have Docker and the ateam binary

In those cases, the re-exec complexity is justified by the constraint. But for the common case (developer laptop with Docker Desktop), the profile-based approach is simpler and already works.
