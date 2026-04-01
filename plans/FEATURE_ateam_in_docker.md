# Feature: Ateam in Docker

## Problem

Running ateam on the host requires managing sandbox permissions, dealing with platform-specific quirks (macOS Seatbelt, bubblewrap on Linux), and configuring network policies. The sandbox reduces permission prompts by ~84% but doesn't eliminate them. Docker provides a simpler security model: the container IS the boundary.

The goal is to make Docker the primary way to run ateam, where all commands — not just the agents — execute inside a container with all dependencies pre-installed.

## Approaches

### Approach A: `ateam shell` only

```
ateam init --docker-mode
ateam shell                  # enters Docker, user runs commands inside
> ateam report
> ateam code
```

The host ateam binary builds the image, starts the container, and hands over control. Once inside, the user runs ateam commands natively.

**Pros:**
- Simplest to implement — just `docker run -it` with the right mounts
- No re-exec logic, no recursion detection
- Clear mental model: "I'm inside Docker"

**Cons:**
- Interactive only. `ateam report && ateam review && ateam code` from a CI script or cron job doesn't work without wrapping everything in `docker exec`
- Two different workflows: shell for interactive, docker exec for automation
- State management is manual (is the container running? did it restart?)

### Approach B: Transparent re-exec

```
ateam init --docker-mode     # sets config, builds image
ateam report                 # detects docker-mode, re-execs inside container
ateam code                   # same — transparent to the user
ateam shell                  # convenience: interactive session in the container
```

Every ateam command checks `config.toml` for docker-mode. If set and not already inside Docker, re-exec via `docker exec` into a persistent container (started on demand).

The re-exec:
```
host$ ateam report
  → reads config.toml: execution_mode = "docker"
  → ensures container is running (start if needed)
  → docker exec ateam-<project> ateam report
    → inside container: detects ATEAM_DOCKER=1, runs normally (no re-exec)
```

**Pros:**
- Zero workflow change — every command works the same, locally or in Docker
- Works for automation (CI, cron, scripts) without special handling
- `ateam shell` is just `docker exec -it ... bash`, not a special mode
- One mental model regardless of how you invoke ateam

**Cons:**
- Re-exec adds ~100ms latency per command (exec into running container)
- Needs recursion guard (env var or file check)
- Container lifecycle: who starts it? Who stops it? What if it crashes?
- Path translation: host paths in flags (e.g. `--review @/host/path/file.md`) need mapping
- Stdout/stderr/exit code forwarding must be perfect

### Approach C: Devcontainer as primary environment

```
ateam init --docker-mode     # generates .devcontainer/devcontainer.json
code .                       # VS Code opens in devcontainer, ateam available inside
```

Or without an IDE:
```
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . ateam report
```

**Pros:**
- Standard tooling with IDE integration (VS Code, JetBrains)
- Reproducible environment via checked-in config
- Community ecosystem (features, prebuilt images)

**Cons:**
- Requires devcontainer CLI or VS Code — ateam shouldn't depend on external tools
- Heavier: devcontainer up is slower than a raw Docker container
- Less control over mounts, networking, lifecycle
- Doesn't help with automation unless you also use devcontainer CLI in CI

### Approach D: Docker Compose sidecar

```
ateam init --docker-mode     # generates docker-compose.yml
docker compose up -d         # starts ateam container + optional services
ateam shell                  # docker compose exec ateam bash
```

**Pros:**
- Can bundle services (Postgres, Redis) alongside the ateam container
- Standard tooling
- Good for projects that already use compose

**Cons:**
- Requires docker compose — adds a dependency
- Overkill for projects that don't need service sidecars
- Same "two workflows" problem as Approach A unless combined with re-exec

## Recommendation: Approach B (transparent re-exec)

Approach B is the strongest because it makes Docker invisible. The user types the same commands regardless of whether they're running locally or in Docker. This is critical for:
- Scripts and automation that call `ateam` commands
- The `ateam code` supervisor which invokes `ateam run` — it shouldn't need to know about Docker
- CI pipelines that run ateam commands

Approach A (`ateam shell`) stays as a convenience on top.

## Design: Transparent Re-Exec

### Configuration

In `config.toml`:

```toml
[project]
execution_mode = "docker"    # "local" (default) or "docker"
```

Set by `ateam init --docker-mode` or manually.

### Container lifecycle

A persistent container named `ateam-<project-id>` (derived from config.toml project name + path hash, same as keychain key).

**Start on demand:** The first ateam command that needs Docker starts the container:
```
docker run -d --name ateam-<id> \
  -v <source>:/ateam/src \
  -v <ateamorg>:/ateam/.ateamorg:ro \
  -e ATEAM_DOCKER=1 \
  <image> sleep infinity
```

**Persistent:** Container stays running between commands. This avoids startup cost per command (~100ms for exec vs ~2s for run).

**Stop/restart:**
```
ateam docker stop             # stops the container
ateam docker start            # starts it if not running
ateam docker restart           # rebuild image + restart
ateam docker status            # show container state
```

Or just `docker stop/rm ateam-<id>` directly.

### Re-exec logic

In the root command's `PersistentPreRun` (or a helper called early in each command):

```go
func maybeReExecInDocker(cmd *cobra.Command) error {
    if os.Getenv("ATEAM_DOCKER") == "1" {
        return nil // already inside, run normally
    }
    cfg := loadProjectConfig()
    if cfg.Project.ExecutionMode != "docker" {
        return nil // local mode
    }
    // excluded commands that must run on host
    if isHostOnlyCommand(cmd) {
        return nil
    }
    return execInDocker(cmd, os.Args[1:])
}
```

**Host-only commands** (must NOT re-exec):
- `ateam init` — creates the project, can't run inside the container it creates
- `ateam docker *` — manages the container itself
- `ateam shell` — handled specially (interactive exec)
- `ateam secret` — accesses host keychain

**execInDocker:**
1. Ensure container is running (start if needed)
2. `docker exec -i ateam-<id> ateam <args...>`
3. Forward stdout/stderr, return exit code

For interactive commands (`ateam shell`): use `-it` flags.

### Docker detection (inside the container)

The `ATEAM_DOCKER=1` env var is set by ateam when it starts the container. This is the primary check.

For the safety check (refuse to run `claude-inside-docker` profile outside Docker), layer multiple signals:
- `ATEAM_DOCKER=1` env var (set by ateam, fast check)
- `/.dockerenv` file exists (set by Docker runtime)
- `/proc/1/cgroup` contains docker/containerd paths (Linux-specific)

Require at least 2 of 3 to pass. This is harder to accidentally fool than any single check.

### Agent profile inside Docker

New profile in `runtime.hcl`:

```hcl
agent "claude-inside-docker" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose",
             "--dangerously-skip-permissions"]
  env = {
    CLAUDECODE = ""
  }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

profile "inside-docker" {
  agent     = "claude-inside-docker"
  container = "none"                   # already in Docker, no nesting
}
```

When `--docker-mode` is active, `config.toml` sets:

```toml
[project]
execution_mode = "docker"
default_profile = "inside-docker"
```

### Image build

`ateam init --docker-mode` or `ateam docker build`:

1. Use the existing Dockerfile resolution chain (project → org → defaults)
2. Build with `docker build --build-arg USER_UID=$(id -u) -t ateam-<id>`
3. The default Dockerfile already installs git, ripgrep, Python, Node, Claude Code
4. `auto-setup` can optionally run inside the container to detect and install project-specific deps

### `ateam shell`

```go
// cmd/shell.go
func runShell(cmd *cobra.Command, args []string) error {
    // ensure container running
    // docker exec -it ateam-<id> bash
    // forward exit code
}
```

### `ateam init --docker-mode`

Extends the existing init flow:

1. Normal init (create `.ateam/`, `config.toml`, etc.)
2. Set `execution_mode = "docker"` and `default_profile = "inside-docker"` in config
3. Build the Docker image
4. Start the container
5. Optionally run `auto-setup` inside the container

### Secrets handling

Secrets need to be accessible inside the container. Options:
- **Forward env vars**: `docker run -e ANTHROPIC_API_KEY` (current approach for docker profiles)
- **Mount secrets.env**: `-v .ateam/secrets.env:/ateam/src/.ateam/secrets.env:ro`
- **ateam secret --get from host**: the re-exec wrapper resolves secrets on the host and passes them as `-e`

The re-exec wrapper should resolve auth secrets on the host (where keychain is available) and inject them into `docker exec -e`:

```go
func execInDocker(cmd *cobra.Command, args []string) error {
    dockerArgs := []string{"exec", "-i"}
    // Inject secrets from host keychain
    for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
        if val := resolveSecret(name); val != "" {
            dockerArgs = append(dockerArgs, "-e", name+"="+val)
        }
    }
    dockerArgs = append(dockerArgs, containerName, "ateam")
    dockerArgs = append(dockerArgs, args...)
    return syscall.Exec(dockerBin, dockerArgs, os.Environ())
}
```

### Git worktree integration

The user mentioned worktrees. This pairs well with Docker mode:

```
git worktree add ../my-project-work feature-branch
cd ../my-project-work
ateam init --docker-mode
ateam report    # runs in its own container, isolated from main worktree
```

Each worktree gets its own `.ateam/`, its own container, its own state. No special handling needed — worktrees are just directories with their own `config.toml`.

## What this replaces

With Docker mode as the default:

| Current | Docker mode |
|---|---|
| macOS Seatbelt sandbox | Container boundary |
| Complex sandbox settings.json | Not needed (`--dangerously-skip-permissions`) |
| Platform-specific quirks | Linux container, same everywhere |
| Permission prompt fatigue | No prompts (container is the boundary) |
| Cross-compilation for linux binary | ateam runs natively in the Linux container |
| Network policy via proxy | iptables or Docker network policy |

## Open questions

1. **Should re-exec use `syscall.Exec` (replace process) or `exec.Command` (subprocess)?** Exec is cleaner (single process) but loses the ability to do post-processing on the host. Subprocess is safer but means two processes for every command.

2. **Container networking**: Should the container have full network access or restricted? For `ateam report` (read-only analysis) full network is fine. For `ateam code` (writes code), restricted might be safer.

3. **Rebuilding**: When should the image be rebuilt? On `ateam docker rebuild`, on Dockerfile change detection, on ateam binary version change? The docker-sandbox approach uses a config hash — worth reusing.

4. **Multiple projects**: If two projects both use docker-mode, they get separate containers. Is that right, or should there be a shared base image with project-specific layers?

## Files to create/modify

### New
- `cmd/shell.go` — `ateam shell` command
- `cmd/docker_mgmt.go` — `ateam docker start|stop|restart|status|rebuild` commands
- Container startup/lifecycle logic (possibly in `internal/container/workspace.go`)

### Modified
- `cmd/root.go` — add `PersistentPreRun` re-exec check
- `cmd/init.go` — add `--docker-mode` flag
- `internal/config/config.go` — add `ExecutionMode` field
- `defaults/runtime.hcl` — add `claude-inside-docker` agent and `inside-docker` profile
