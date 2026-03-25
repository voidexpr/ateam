# ATeam Development Guide

## Build

```bash
make build    # builds the ateam binary
make clean    # removes the binary
```

Requires Go 1.24+.

## Tests

```bash
make test             # unit tests (no Docker needed)
make test-docker      # docker integration tests via Docker-in-Docker
make test-docker-live # live agent tests via DinD (requires API auth, ~$0.03)
```

### Docker integration tests (`make test-docker`)

Tests run inside Docker-in-Docker so nothing touches your host Docker daemon. They verify:

- Image building and caching (`EnsureImage`)
- Mount layout: source → `/workspace` (rw), org → `/.ateamorg` (ro)
- File permission matrix: rw read/write, ro read/write-denied, unmounted inaccessible
- Env var forwarding into containers
- `CmdFactory` produces correct `docker run` commands

Build tag: `docker_integration`. The DinD image is built from `test/Dockerfile.dind`.

### Live agent tests (`make test-docker-live`)

Runs real Claude (haiku) inside Docker containers to verify end-to-end agent behavior:

- Agent reads a mounted file
- Agent writes a file visible on host
- Agent reads org config from read-only mount
- Agent cannot access unmounted host paths

Build tag: `docker_live`. Requires one of these auth methods (`CLAUDE_CODE_OAUTH_TOKEN` takes precedence if both are set):

**Option A — OAuth token** (reuses your Claude Code login):

Authenticate Claude Code if you haven't already (`claude` will prompt on first run), then:

```bash
export CLAUDE_CODE_OAUTH_TOKEN="$(cat ~/.claude/.credentials.json | grep -o '"accessToken":"[^"]*"' | cut -d'"' -f4)"
```

**Option B — API key** (recommended for CI):

Create a key at https://console.anthropic.com/settings/keys, then:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

The Makefile checks for auth before starting and fails with setup instructions if neither is set. The tests themselves also fail (not skip) when auth is missing — this catches configuration issues in CI.

## Docker binary resolution

Docker containers need a Linux/AMD64 ateam binary mounted at `/usr/local/bin/ateam`. The `findLinuxBinary()` function resolves it with this search chain:

1. **Host is linux/amd64** — uses the running binary directly
2. **Companion binary** — `ateam-linux-amd64` next to the host `ateam` binary (e.g. from a release archive)
3. **Org cache** — `.ateamorg/cache/ateam-linux-amd64` from a prior cross-compilation
4. **Cross-compile** — builds automatically if `go` and `go.mod` are available
5. **Warning** — prints a message and returns empty (Docker mount is skipped)

For developers building from source, cross-compilation happens automatically (step 4). To pre-build the companion binary:

```bash
make companion    # produces ateam-linux-amd64 next to the ateam binary
```

Release archives should include both `ateam` (host) and `ateam-linux-amd64` so Docker mode works without a Go compiler.

## Adding a new role

1. Create `defaults/roles/<name>/report_prompt.md`
2. Optionally add `code_prompt.md` for code-action support
3. Run `make build` — the role is auto-discovered from the embedded filesystem
4. Enable it in a project: `ateam init --role <name>` or edit `.ateam/config.toml`

## Architecture: Runtime / Agents / Containers / Profiles

Configuration lives in `runtime.hcl` with 4-level resolution: embedded defaults → org defaults → org overrides → project overrides.

### Agents

Defined in `internal/agent/`. Each agent implements the `Agent` interface (Run, ParseStreamFile). Available agents:

| Agent | Description |
|-------|-------------|
| `claude` | Claude Code CLI with sandbox settings (for host execution) |
| `claude-docker` | Claude without sandbox, `--dangerously-skip-permissions` (for containers) |
| `claude-sonnet` | Claude with sonnet model + budget cap |
| `claude-haiku` | Claude with haiku model + budget cap |
| `claude-isolated` | Claude with project-local config dir |
| `codex` | OpenAI Codex CLI |
| `mock` | Built-in mock for testing |

Agents receive a `CmdFactory` from the container layer. When set, they use it to spawn subprocesses instead of `exec.CommandContext` directly. This is how Docker execution works transparently.

### Containers

Defined in `internal/container/`. Each container implements the `Container` interface.

| Container | Description |
|-----------|-------------|
| `none` | Direct host execution (default) |
| `docker` | One-shot `docker run --rm -i` per invocation |

#### Dockerfile resolution

The Dockerfile used to build the container image is resolved with a fallback chain (first match wins):

1. `.ateam/roles/<role>/Dockerfile` — role-specific (when a role is specified)
2. `.ateam/Dockerfile` — project-level
3. `.ateamorg/Dockerfile` — org-level
4. `.ateamorg/defaults/Dockerfile` — org defaults
5. Embedded default — built into the `ateam` binary

This follows the same pattern as prompt resolution. A security-focused role can use a locked-down container while other roles use the project default.

The filename searched for comes from the container config's `dockerfile` field (defaults to `"Dockerfile"`).

#### Docker path mapping

The Docker container maps host paths to fixed container paths:

| Host path | Container path | Mode |
|-----------|----------------|------|
| Project source dir | `/workspace` | read-write |
| `.ateamorg/` dir | `/.ateamorg` | read-only |

The agent sees only these mount points. Host paths in agent arguments (stream files, stderr files, settings) are automatically translated via `TranslatePath()`. For example, `/Users/me/myproject/output.jsonl` becomes `/workspace/output.jsonl` inside the container.

The container image is built with a non-root user matching the host UID (`--build-arg USER_UID=$(id -u)`), so files written by the agent inside `/workspace` have correct ownership on the host.

Env vars listed in `forward_env` are passed to `docker run -e`, forwarding their values from the host process.

#### Custom mounts and docker args

To give the agent access to directories outside the standard mounts, use `extra_volumes` on a container definition and/or `container_extra_args` on a profile. Paths can be relative to the project source dir for portability.

Example `.ateam/runtime.hcl`:

```hcl
// Extend the default docker container with custom mounts
container "docker-with-data" {
  type        = "docker"
  dockerfile  = "Dockerfile"
  forward_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
  extra_volumes = [
    "../shared-data:/data:ro",       // relative to project source dir
    "/opt/tools:/tools:ro",          // absolute paths work too
  ]
}

profile "docker-data" {
  agent     = "claude-docker"
  container = "docker-with-data"
  // Extra docker run args (e.g. resource limits, network, capabilities)
  container_extra_args = ["--cpus=2", "--memory=4g"]
}
```

The agent can then read files from `/data` and `/tools` inside the container. Relative host paths in `extra_volumes` are resolved from the project source directory, making the config portable across machines.

`container_extra_args` passes raw flags to `docker run`, useful for resource limits, network modes, or capabilities that don't have dedicated config fields.

### Profiles

Profiles combine an agent + container. Defined in `runtime.hcl`:

```hcl
profile "docker" {
  agent     = "claude-docker"
  container = "docker"
}
```

| Field | Description |
|-------|-------------|
| `agent` | Agent name from runtime.hcl |
| `container` | Container name from runtime.hcl |
| `agent_extra_args` | Appended to agent CLI args |
| `container_extra_args` | Passed as extra `docker run` flags |

Select via `--profile` flag or `config.toml` per action/role.

## Maintenance Commands

### `ateam project-rename`

Update state after moving a project directory within the org. Since `state.sqlite` is per-project (inside `.ateam/`), no DB updates are needed. This command only renames the legacy state directory under `.ateamorg/projects/` if one exists.

```bash
ateam project-rename --old services/api --new backends/api
```

| Flag | Description |
|------|-------------|
| `--old PATH` | Old project path (relative to org root) **(required)** |
| `--new PATH` | New project path (relative to org root) **(required)** |

### `ateam migrate-logs`

Migrate existing projects from the legacy org-level layout (logs and exec history in `.ateamorg/projects/<id>/`, shared `state.sqlite`) to the new per-project layout (everything inside `.ateam/`).

For each project discovered under `.ateamorg/projects/`:

1. Copies log files from `.ateamorg/projects/<id>/roles/<role>/logs/` to `.ateam/logs/roles/<role>/`
2. Copies supervisor logs from `.ateamorg/projects/<id>/supervisor/logs/` to `.ateam/logs/supervisor/`
3. Appends `runner.log` from `.ateamorg/projects/<id>/runner.log` to `.ateam/logs/runner.log`
4. Copies `agent_execs` rows from `.ateamorg/state.sqlite` to `.ateam/state.sqlite`, rewriting `project_id` to `""` and `stream_file` paths to the new layout
5. Creates `.ateam/.gitignore` if missing

The migration is idempotent: files that already exist are skipped, DB rows are only copied if the project DB is empty.

```bash
ateam migrate-logs              # run from anywhere under the org root
ateam migrate-logs --dry-run    # preview changes without applying
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview changes without applying them |

## Devcontainer

Claude Code provides a [devcontainer](https://code.claude.com/docs/en/devcontainer) for sandboxed agent execution. To use it:

1. Install the VS Code Dev Containers extension
2. Clone the repo and open in VS Code
3. Reopen in Container when prompted (or Command Palette → "Dev Containers: Reopen in Container")

The devcontainer provides:
- Network isolation via iptables firewall (only Anthropic API allowed)
- `--dangerously-skip-permissions` for unattended operation
- `NET_ADMIN`/`NET_RAW` capabilities for firewall rules

This is separate from ateam's own Docker container support. The devcontainer runs the entire development environment in a container, while ateam's Docker profiles run individual agent invocations in containers.
