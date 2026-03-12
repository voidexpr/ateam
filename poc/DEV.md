# ATeam Development Guide

## Build

```bash
make build    # builds the ateam binary
make clean    # removes the binary
```

Requires Go 1.23+.

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

Build tag: `docker_live`. Requires one of these auth methods:

**Option A — API key** (recommended for CI):

Create a key at https://console.anthropic.com/settings/keys, then:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

**Option B — OAuth token** (reuses your Claude Code login):

Authenticate Claude Code if you haven't already (`claude` will prompt on first run), then:

```bash
export CLAUDE_CODE_OAUTH_TOKEN="$(cat ~/.claude/.credentials.json | grep -o '"accessToken":"[^"]*"' | cut -d'"' -f4)"
```

The Makefile checks for auth before starting and fails with setup instructions if neither is set. The tests themselves also fail (not skip) when auth is missing — this catches configuration issues in CI.

## Adding a new role

1. Create `internal/prompts/defaults/roles/<name>/report_prompt.md`
2. Optionally add `code_prompt.md` for code-action support
3. Run `make build` — the role is auto-discovered from the embedded filesystem
4. Enable it in a project: `ateam init --role <name>` or edit `.ateam/config.toml`

## Architecture: Runtime / Agents / Containers / Profiles

Configuration lives in `runtime.hcl` with 3-level resolution: org defaults → org overrides → project overrides.

### Agents

Defined in `internal/agent/`. Each agent implements the `Agent` interface (Run, ParseStreamFile). Available agents:

| Agent | Description |
|-------|-------------|
| `claude` | Claude Code CLI with sandbox settings |
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

The Docker container provides:
- Source dir mounted at `/workspace` (read-write)
- Org dir mounted at `/.ateamorg` (read-only)
- Env var forwarding (API keys)
- Path translation (host paths → container paths)

### Profiles

Profiles combine an agent + container. Defined in `runtime.hcl`:

```hcl
profile "docker" {
  agent     = "claude"
  container = "docker"
}
```

Select via `--profile` flag or `config.toml` per action/role.

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
