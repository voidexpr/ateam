# ATeam Development Guide

## Build

```bash
make build companion    # builds the ateam binary + the linux binary for docker
make clean    # removes the binary
```

Requires Go 1.26+.

### Optional runtime dependencies

- `tmux` is required by the `codex-tmux` agent; if absent, `internal/tmuxctl` unit tests skip and codex-tmux runs fail at startup.

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

## Code Quality

### Before committing

```bash
make check         # runs test, fmt-check, check-tidy, and lint in one command
```

Or run individual checks:

```bash
make fmt-check     # verify gofmt formatting (no changes, exit 1 if issues)
make check-tidy    # verify go.mod is tidy (no changes, exit 1 if drift)
make build         # build the binary (catches compile errors)
make test          # unit tests
```

To fix formatting or tidy issues:

```bash
make fmt           # auto-format all .go files
make tidy          # run go mod tidy
```

### CI pipeline

GitHub Actions (`.github/workflows/ci.yml`) runs on every push to `main` and on pull requests:

```bash
make fmt-check     # verify gofmt formatting
make check-tidy    # verify go.mod is tidy
make lint          # golangci-lint
make test          # unit tests
make vuln          # govulncheck
```

Run the full CI suite locally with `make run-ci`.

A separate workflow (`.github/workflows/docker-tests.yml`) runs `make test-docker` on pushes to `main` that touch `test/`, `Dockerfile*`, or `internal/container/`.

### Git hooks

A pre-commit hook runs `fmt-check` and `check-tidy` automatically. Install it with:

```bash
make install-hooks
```

This creates `.git/hooks/pre-commit` that blocks commits with formatting or module drift issues.

### Additional checks

```bash
make lint          # golangci-lint (requires golangci-lint installed)
make vuln          # govulncheck for known vulnerabilities (installs itself if missing)
make test-docker   # Docker-in-Docker integration tests (see below)
```

## Docker binary resolution

Docker containers need a Linux/AMD64 ateam binary mounted at `/usr/local/bin/ateam`. The `findLinuxBinary()` function resolves it with this search chain:

1. **Host is linux/amd64** — uses the running binary directly
2. **Companion binary** — `ateam-linux-amd64` next to the host `ateam` binary (e.g. from a release archive)
3. **Build directory** — `build/ateam-linux-amd64` from `make companion`
4. **Org cache** — `.ateamorg/cache/ateam-linux-amd64` from a prior auto cross-compilation
5. **Cross-compile** — builds automatically if `go` and `go.mod` are available
6. **Warning** — prints a message and returns empty (Docker mount is skipped)

For developers building from source, cross-compilation happens automatically (step 5). To pre-build the companion binary:

```bash
make companion    # produces build/ateam-linux-amd64
```

Release archives should include both `ateam` (host) and `ateam-linux-amd64` so Docker mode works without a Go compiler.

## Adding a new role

1. Create `defaults/roles/<name>/report_prompt.md`
2. Optionally add `code_prompt.md` for code-action support
3. Run `make build` — the role is auto-discovered from the embedded filesystem
4. Enable it in a project: `ateam init --role <name>` or edit `.ateam/config.toml`

### Prompt assembly order

When a role runs, `internal/prompts/prompts.go::assembleRoleAction` concatenates these parts (separated by `---`):

1. ATeam Project Context (header line, role/action, working dir, git/orientation block — built by `FormatProjectInfo`)
2. Role-specific prompt (`report_prompt.md` or `code_prompt.md`)
3. Base prompt (`report_base_prompt.md` or `code_base_prompt.md`) — shared format/output rules
4. Extra prompts (`*_extra_prompt.md`) — additive across all levels, in order: org broad → org role → project broad → project role
5. Previous report (the role's existing `report.md`, with age) — skipped for code action; replaced by a "fresh cycle" notice when no prior report exists
6. CLI `--extra-prompt` text under "# Additional Instructions"

### Resolution precedence

Each role/base prompt file is resolved by `readFileOr3Level` (whose package comment in `prompts.go` documents the chain as 4-level):

1. Project — `.ateam/<file>` or `.ateam/roles/<name>/<file>`
2. Org — `.ateamorg/<file>` or `.ateamorg/roles/<name>/<file>`
3. Org defaults — `.ateamorg/defaults/<file>` or `.ateamorg/defaults/roles/<name>/<file>`
4. Embedded — bundled into the binary via `defaults.FS` (`defaults/roles/<name>/...`)

First non-empty match wins for role/base prompts. `*_extra_prompt.md` is additive (every level that contributes a non-empty file is appended). Use `ateam prompt --role <name> [--code]` to inspect the assembled prompt and which sources contributed.

### Template variables

Inside any prompt file, `{{VAR}}` placeholders are substituted by `internal/runner/template.go`. The canonical list lives in the `TemplateVars` struct and its `Replacer()` method — examples: `{{PROJECT_NAME}}`, `{{ROLE}}`, `{{ACTION}}`, `{{EXEC_ID}}`, `{{OUTPUT_DIR}}`, `{{OUTPUT_FILE}}`, `{{AGENT}}`, `{{MODEL}}`, `{{ATEAM_OWN_README}}` (embedded self-docs), `{{AUTO_ROLES_MARKER}}`. Add new placeholders there only; unknown `{{VAR}}` tokens are left as-is.

### `code_prompt.md` and the supervisor code phase

The per-role `code_prompt.md` is independent from the supervisor's code-management phase:

- **Role level** — when `defaults/roles/<name>/code_prompt.md` exists, `ateam code --role <name>` (and `--code-prompt`) assembles the role's code prompt via `AssembleRoleCodePrompt`, using `code_base_prompt.md` instead of `report_base_prompt.md` and never including the previous report.
- **Supervisor level** — `ateam code` (no role) drives the supervisor via `defaults/supervisor/code_management_prompt.md` (assembled by `AssembleCodeManagementPrompt`). The supervisor splits the review into individual tasks and writes per-task `<SEQ>_<SLUG>_code_prompt.md` files into `{{EXECUTION_DIR}}`, then invokes `ateam exec @... --role <ROLE>` for each. Adding a role's own `code_prompt.md` is what lets those per-task `exec` invocations target it.

## Project on-disk layout

Per-run artefacts are keyed by `agent_execs.id` (`<exec_id>`):

```
.ateam/
  state.sqlite                                # per-project agent_execs DB
  config.toml, runtime.hcl, …                 # config

  logs/<exec_id>/                             # forensic, runner-owned
    stream.jsonl                              # raw agent stream events
    stderr.out                                # captured stderr
    settings.json                             # rendered sandbox settings
    prompt.md                                 # rendered prompt
    cmd.md                                    # # Runtime / # Run details / # Command / # Env / # Settings / # Files Copy
  logs/.layout-v2                             # migration sentinel

  runtime/<exec_id>/                          # agent-writable output area
    report.md | review.md | verify.md | …     # whatever the agent wrote via {{OUTPUT_FILE}} / {{OUTPUT_DIR}}

  roles/<role>/                               # canonical, post-run promote target
    report.md                                 # latest report
    history/<TS>.report.md                    # archived outputs (kept across runs)
  supervisor/
    review.md, verify.md                      # canonical
    history/<TS>.review.md, …                 # archived outputs
    code/<exec_id>/<file>                     # `code` action canonical (per-exec_id)
```

Per-action canonical destinations (where `runtime/<exec_id>/` files are promoted to on success):

| Action       | `CanonicalDestDir`                          | Source                  |
|--------------|---------------------------------------------|-------------------------|
| `report`     | `roles/<role>/`                             | `cmd/report.go`         |
| `review`     | `supervisor/`                               | `cmd/review.go`         |
| `verify`     | `supervisor/`                               | `cmd/verify.go`         |
| `code`       | `supervisor/code/<exec_id>/` (per-exec_id)  | `cmd/code.go`           |
| `exec`       | _none_ (no promotion)                       | `cmd/exec.go`           |
| `parallel`   | _none_                                      | `cmd/parallel.go`       |
| `auto-setup` | _none_                                      | `cmd/auto_setup.go`     |

Key invariants:

- `logs/<exec_id>/` is forensics. The runner writes it; agents must not.
- `runtime/<exec_id>/` is the agent's writable scratch. The directory is created only when the action has an `OutputKind` — `report`, `review`, `verify`, `execution_report`, `setup_overview` (`internal/runner/runner.go` mkdir gate; `internal/runner/template.go::OutputKind*` / `PrimaryOutputName`). `exec`, `parallel`, `auto-setup`, `auto-debug` get no runtime dir.
- On success the runner clones every non-`*_prompt.md` file from `runtime/<exec_id>/` to the action's canonical destination (`roles/<role>/`, `supervisor/`, `supervisor/code/<exec_id>/`, …). Source remains in `runtime/<exec_id>/`. See `promoteRuntimeFiles` in `internal/runner/runner.go`. There is no filename-level filtering beyond the `*_prompt.md` exclusion — per-action behaviour is set entirely by `RunOpts.CanonicalDestDir` in `cmd/*.go`.
- When the action has no canonical destination (`exec`, `parallel`, `auto-setup`), nothing is promoted: files persist in `runtime/<exec_id>/` and are viewable via `ateam cat <exec_id>`. Each file gets the note `SKIPPED (action has no canonical destination)` in `cmd.md`.
- On failure, no clone happens; `cmd.md` lists what landed in `runtime/<exec_id>/` with the note `SKIPPED (run failed; not promoted)` via `listRuntimeForReport` (`internal/runner/runner.go`).
- Clones use `cp -pc` on Darwin (APFS clonefile) and `cp -p --reflink=auto` on Linux (btrfs/xfs/zfs reflink) to avoid double disk usage; falls back to a regular byte copy.
- `cmd.md` is written twice: once before the run (Run details "(pending)"), once at finalize with the actual exit code, status, and `# Files Copy` log.
- Per-action `*_error.md` files no longer exist — failure context lives in `logs/<exec_id>/{cmd.md, stderr.out, stream.jsonl}`.

### Migration of legacy projects (`internal/root/migrate_logs.go`)

Pre-`<exec_id>` projects used a flat layout: `logs/{roles/<id>,parallel,run,supervisor}/<TS>_<ACTION>_{stream.jsonl,stderr.log,settings.json,exec.md}` plus history-dir prompts and per-action `*_error.md` files. `MigrateLogsLayout` runs lazily on the first DB open (both `openProjectDB` and `requireProjectDB`), is sentinel-guarded by `logs/.layout-v2`, and is idempotent.

For each `agent_execs` row whose `stream_file` ends in `_stream.jsonl`:

1. `os.Rename` `<TS>_<ACTION>_stream.jsonl|_stderr.log|_settings.json|_exec.md` into `logs/<id>/{stream.jsonl,stderr.out,settings.json,cmd.md}` (cmd.md content untouched).
2. Locate the matching `<TS>.<action>_prompt.md` in `roles/<role>/history/` or `supervisor/history/` via `findClosestHistoryFile` (within `legacyPromptMatchWindow = 60s` of `started_at`); rename to `logs/<id>/prompt.md`. Started_at is parsed as RFC3339 and used directly — **no `.Local()`** — so DST/TZ moves don't break matching.
3. Set `agent_execs.stream_file = "logs/<id>/stream.jsonl"` and (when a matching `<TS>.<kind>.md` archive exists in history) `agent_execs.output_file = "<role>/history/<TS>.<kind>.md"` so the web run-page output link still resolves.

Then: delete canonical `report_error.md` / `review_error.md` / `verify_error.md` / `code_error.md` / `auto_setup_error.md`, remove `runner.log`, prune empty legacy log subdirs. Unmatched orphan files (no DB row, or skew >60s) are left in place — never destroyed.

### Compatibility shims (remove after legacy data ages out)

These exist so users with pre-migration databases keep working. Each has a clear marker for future removal:

| Shim | Where | When to remove |
|---|---|---|
| `root.IsLegacyStreamFile` (`stream_file` ends in `_stream.jsonl`) | `internal/root/resolve.go` | When no `agent_execs` row in any deployed project still has a `_stream.jsonl` stream_file. |
| Legacy branch in `cmd/inspect.go::logFilesForRun` (prefix-strip + `_exec.md`/`_settings.json`/`_stderr.log` suffixes) | `cmd/inspect.go` | Same condition as above. |
| Legacy branch in `cmd/resume.go::cmdMDPath` (returns `_exec.md` instead of `cmd.md`) | `cmd/resume.go` | Same. |
| Legacy branch in `cmd/pool_status.go::streamFilePrefix` (returns `<prefix>*` glob) | `cmd/pool_status.go` | Same. |
| Legacy branch in `internal/web/handlers.go::resolveRunFiles` and `handleRunFile` | `internal/web/handlers.go` | Same. |
| `internal/web/handlers.go::resolveHistoryFile` + `resolvePromptFile` + `resolveOutputFile` (±5s fuzzy match against `<role>/history/<TS>.<kind>.md` for runs with empty `output_file`) | `internal/web/handlers.go` | When all runs have a populated `agent_execs.output_file`. |
| `internal/web/history.go::discoverHistory` + `mergeHistory` (filename-scan fallback merged into the DB-driven view) | `internal/web/history.go` | When `<role>/history/<TS>.<kind>.md` is no longer present in any deployed project. |
| `internal/root/migrate_logs.go` whole file + the migration call in `cmd/table.go` | — | When legacy projects are no longer expected. Drop the sentinel constant too. |
| `legacyOutputSuffix` / `legacyPromptSuffix` / `legacyHistoryDir` (action → kind/path mapping) | `internal/root/migrate_logs.go` | With the migration. |
| `internal/runner/template.go::EXECUTION_DIR` template alias (legacy alias for `OUTPUT_DIR` used by older `code_management_prompt.md`) | `internal/runner/template.go` | When all in-tree and user-overloaded prompts use `{{OUTPUT_DIR}}`. |
| Streamed-text fallback that seeds `runtime/<id>/<primary>.md` when the agent didn't `Write` | `internal/runner/runner.go` (after the event loop) | When all default prompts reliably use the `Write` tool and we sandbox-deny stray writes. |

Search for `legacy` in those files to find the relevant blocks.

## Architecture: Runtime / Agents / Containers / Profiles

Configuration lives in `runtime.hcl` with 4-level resolution: embedded defaults → org defaults → org overrides → project overrides.

For the concurrency contract that governs parallel pool execution — what's shared, what gets cloned, what flows through channels — see [CONCURRENCY.md](CONCURRENCY.md).

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
| `codex-tmux` | OpenAI Codex CLI driven through tmux for TUI-only slash commands (experimental, host-only) |
| `mock` | Built-in mock for testing |

Agents receive a `CmdFactory` from the container layer. When set, they use it to spawn subprocesses instead of `exec.CommandContext` directly. This is how Docker execution works transparently.

#### Codex parity caveats

The codex agent matches claude on cost accounting, cache-token tracking, context utilization, verbose tool detail, and `ateam resume`. A few items are intentionally claude-only:

- **OAuth login** (`claude setup-token`). OpenAI ships no equivalent; auth uses `OPENAI_API_KEY` or `~/.codex/auth.json` directly.
- **Sandbox `--settings` JSON.** Codex CLI ships its own sandbox model (`workspace-write` / `read-only` plus approval policies). Codex sandbox flags belong in `agent "codex" { args = [...] }` in runtime.hcl, not in a settings JSON.
- **Multi-turn turn count.** ateam invokes codex via `exec --json`, which is one-shot, so `Turns: 1` is hardcoded. A future contributor shouldn't try to "fix" this by reading a `turns` field that doesn't exist.

#### codex-tmux design notes

`codex-tmux` drives the interactive Codex TUI through a tmux session so that TUI-only slash commands (`/review`, etc.) can be invoked unattended. Key constraints:

- **Host-only in v1.** Rejected with an actionable error at `cmd/table.go:140–142` when a profile binds it to a non-`none` container, and at `cmd/table.go:398–407` when invoked without project context. Container support would require tmux+codex inside the image plus host↔container path translation that isn't wired up.
- **Per-`EXEC_ID` socket and session naming.** The tmux socket lives under `<ProjectDir>/cache/tmux/` and the session name embeds the `EXEC_ID`, so concurrent runs in the same workdir don't collide.
- **Token/cost data is sourced from `$CODEX_HOME/sessions/...`** (the rollout JSONL Codex writes itself), not from a streamed JSON channel — the TUI doesn't emit one. The agent live-tails that rollout into `stream.jsonl` and archives it to `codex-session.jsonl.gz` on completion.
- The original design rationale lives in [`plans/feature_codex_tmux_agent.md`](plans/feature_codex_tmux_agent.md) — historical, not normative.

### Containers

Defined in `internal/container/`. Each container implements the `Container` interface.

| Container | Description |
|-----------|-------------|
| `none` | Direct host execution (default) |
| `docker` | One-shot `docker run --rm -i` per invocation |
| `docker-exec` | Exec into a user-managed container |

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
| Project source dir | `/workspace` | read-only by default; read-write for `code`, `run`, `parallel`, `inspect`, `auto-setup` |
| `.ateamorg/` dir | `/.ateamorg` | read-write |
| `~/.claude/.credentials.json` | `/home/agent/.claude/.credentials.json` | read-only (only when `mount_claude_config = true`) |

The agent sees only these mount points. See `ISOLATION.md` for detailed per-mode setup. Host paths in agent arguments (stream files, stderr files, settings) are automatically translated via `TranslatePath()`. For example, `/Users/me/myproject/output.jsonl` becomes `/workspace/output.jsonl` inside the container.

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


