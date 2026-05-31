# ATeam Development Guide

> **Commands and general dev info live in [CLAUDE.md](CLAUDE.md)** — build (`make build-all`), test tiers (`make test` / `test-cli` / `test-docker` / `test-docker-live` / `test-all`), quality (`make check`, `make run-ci`, `make fmt`, `make tidy`, `make install-hooks`), and git workflow. CLAUDE.md is loaded automatically in every agent session and is also the right starting point for humans. This file covers internals: what those commands actually do, on-disk layout, runner contract, prompt-assembly internals, legacy migration, compat shims.

Requires Go 1.26+. Optional runtime dependency: `tmux` (needed by the `codex-tmux` agent; `internal/tmuxctl` unit tests skip when tmux is absent).

## Test harness internals

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

## CI and pre-commit gates

GitHub Actions (`.github/workflows/ci.yml`) is `workflow_dispatch`-only — triggered manually from the Actions tab, not on push or pull request. Its single step runs `make run-ci`, which expands to `check + vuln + deadcode` where `check = test + fmt-check + check-tidy + check-docs + lint`.

Because nothing auto-gates push or PR, contributors and agents must run `make run-ci` locally before committing. The pre-commit hook described below covers a subset; `make run-ci` is the full gate.

A separate workflow (`.github/workflows/docker-tests.yml`) runs `make test-docker`. It is also `workflow_dispatch`-only.

The pre-commit hook installed by `make install-hooks` blocks commits that fail `fmt-check`, `check-tidy`, `check-docs`, or `lint`. It does not run `test`, `vuln`, or `deadcode` — run `make run-ci` for that.

## Docker binary resolution

Docker containers need a linux ateam binary (matching the host's CPU arch — `ateam-linux-<arch>`, e.g. `arm64` on Apple Silicon) mounted at `/usr/local/bin/ateam`. The `findLinuxBinary()` function resolves it with this search chain:

1. **Host is linux** — uses the running binary directly (it is already linux/`<host arch>`)
2. **Build directory** — `build/ateam-linux-<arch>` from `make companion`
3. **Companion binary** — `ateam-linux-<arch>` next to the host `ateam` binary (e.g. from a release archive)
4. **Org cache** — `.ateamorg/cache/ateam-linux-<arch>` from a prior auto cross-compilation
5. **Cross-compile** — builds automatically if `go` and `go.mod` are available
6. **Warning** — prints a message and returns empty (Docker mount is skipped)

For developers building from source, cross-compilation happens automatically (step 5). Release archives should include both `ateam` (host) and `ateam-linux-<arch>` so Docker mode works without a Go compiler.

## Adding a new role

1. Create `defaults/prompts/report/<name>.prompt.md` (the role's main report body)
2. Optionally add `defaults/prompts/code/<name>.prompt.md` for code-action support
3. Run `make build` — the prompt tree is embedded via `defaults.FS`, so the role is discoverable from the embedded anchor
4. Enable it in a project: `ateam init --role <name>` or edit `.ateam/config.toml`

The user-facing assembly model (anchor chain, filename patterns, `FirstMatch` vs `AllMatches`, template variables) is documented in **[CONFIG.md → Prompt Composition](CONFIG.md#prompt-composition)** and **[CONFIG.md → Template Variables](CONFIG.md#template-variables)**. The sections below cover the dev-only deltas.

### Assembly internals — code pointers

- `(*Assembler).Assemble` (`internal/prompts/assembler/assemble.go`) walks the anchor chain and joins the slots described in CONFIG.md.
- `assembler.BuildAnchors(projectDir, orgDir, embedded)` (`internal/prompts/assembler/anchors.go`) builds the project → org → embedded chain, wired up by `(*ResolvedEnv).Assembler()` (`internal/root/resolve.go`).
- Prompt-file `{{namespace.key}}` directives are resolved by `internal/prompts/assembler/template.go` and `MapVars`. The canonical variable names plus the legacy ALL_CAPS → dotted compatibility mapping live in `internal/prompts/assembler/varmap.go` (`VarRenameMap`, `VarLiteralRewrites`) — add new prompt variables there.
- `runtime.hcl` ALL_CAPS `{{VAR}}` placeholders (agent CLI args, container fields) are a *separate* substitution pass handled by `TemplateVars.Replacer()` in `internal/runner/template.go`. Add new placeholders there. Do not conflate the two systems.
- The `ATeam Project Context` header, the previous-report block, and the `# Additional Instructions` block are appended around the assembled body by the cmd layer (see `assembleRoleReportV1` in `cmd/report_v1.go`), not by the assembler itself.

### CLI override surface (`AssembleOptions`)

`Assemble` takes an `*AssembleOptions` (`internal/prompts/assembler/assemble.go`) carrying caller-supplied overrides; `nil` means no overrides. Each field is rendered through the same template engine as anchor content; whitespace-only values are dropped.

- `ReplaceRoleMain` — swaps in caller text as the role's main body; all surrounding framing (pre/post fragments, CLI wrappers) still composes. Used by `ateam review --prompt` and `ateam code --prompt`.
- `PrePrompt` — wrapped at the very front, before any anchor content (`--pre-prompt`).
- `PostPrompt` — wrapped at the very end, after every anchor section (`--post-prompt`).

### Role code prompts vs. supervisor code phase

A per-role `code/<name>.prompt.md` is independent from the supervisor's code-management phase:

- **Role level** — when `code/<name>.prompt.md` exists (project, org, or embedded), `ateam code --role <name>` and `ateam prompt --role <name> --action code` assemble the `code/<name>` path with no previous-report block (the source of truth for "what changed" is the patch's git history). See `assembleRoleCodeV1` in `cmd/report_v1.go`.
- **Supervisor level** — `ateam code` (no role) drives the supervisor via the `code_management.prompt.md` body, assembled by `assembleCodeManagementV1` (`cmd/code_v1.go`). The supervisor splits the review into individual tasks and writes per-task code prompts into `{{OUTPUT_DIR}}` (the prompt still ships the legacy `{{EXECUTION_DIR}}` alias for the same directory), then invokes `ateam exec @... --role <name>` for each. A role's own `code/<name>.prompt.md` is what lets those per-task `exec` invocations target it.

## Project on-disk layout (runner contract)

The user-facing directory tree is in **[CONFIG.md → Directory Layout](CONFIG.md#directory-layout)**. This section is the runner-internal contract — who writes what, when, and the per-action promotion rules.

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

The configuration surface (HCL syntax, the 4-level resolution chain, agent/container/profile fields, `[container-extra]`, built-in profile catalogue) is documented in **[CONFIG.md → Runtime Configuration](CONFIG.md#runtime-configuration)**. The Docker mount layout, Dockerfile fallback chain, secret resolution, and the four execution modes are in **[ISOLATION.md](ISOLATION.md)**. For the concurrency contract that governs parallel pool execution see **[CONCURRENCY.md](CONCURRENCY.md)**.

The remainder of this section covers contributor-only details — the Go-side surface and the agents whose internals don't fit cleanly in CONFIG.md.

### `internal/flow` (composition framework)

The spine of every agent-running cmd (`exec`, `parallel`, `verify`, `review`, `auto_setup`, `code`, `report`). A `Step` is one of three types: `PromptBundle` (leaf — one agent invocation, with `PreExec` / `PostExec` `Action` hooks), `Pipeline` (sequence; stops on first errored step, skip results don't stop the chain), `Parallel` (fan-out with bounded workers and per-step panic recovery; does not short-circuit on errors). Cmds enter through `flow.Run`, which always returns `PipelineResult`; single-bundle callers use `flow.RunBundle`.

- `RunCtx` carries the per-execution session: `context.Context`, `*calldb.CallDB`, `*root.ResolvedEnv`, and the `Reporter`. Threaded unchanged through every Step.
- `RuntimeEnv` is the "where & how to invoke the agent" config (executor, workdir, role, action, dry-run, batch, prompt-dir) and is freely rebound at composition boundaries via `Pipeline.Env` / `Parallel.Env` / `PromptBundle.Env`.
- `Reporter` is the single observability seam — bundle/stage lifecycle, agent progress events, skipped-step notifications. Cmd-layer TUIs and JSON reporters implement it; `NoopReporter` is the default.

Design rationale and the Phase F→G migration: [`plans/Feature_prompt_report_fs_refactor_phaseG.md`](plans/Feature_prompt_report_fs_refactor_phaseG.md). The pre-Phase-G `internal/stage` package has been removed; do not reference it.

### Agent and container Go surface

- Each agent under `internal/agent/` implements the `Agent` interface (`Run`, `ParseStreamFile`). Built-ins: `claude`, `claude-docker`, `claude-sonnet`, `claude-haiku`, `claude-isolated`, `codex`, `codex-tmux`, `mock`.
- Each container under `internal/container/` implements the `Container` interface. Built-ins: `none`, `docker`, `docker-exec`.
- Agents receive a `CmdFactory` from the container layer. When set, they use it to spawn subprocesses instead of `exec.CommandContext` directly. This is how Docker execution works transparently — the agent code is unaware.

### Codex parity caveats

The codex agent matches claude on cost accounting, cache-token tracking, context utilization, verbose tool detail, and `ateam resume`. A few items are intentionally claude-only — a future contributor shouldn't try to "fix" them:

- **OAuth login** (`claude setup-token`). OpenAI ships no equivalent; auth uses `OPENAI_API_KEY` or `~/.codex/auth.json` directly.
- **Sandbox `--settings` JSON.** Codex CLI ships its own sandbox model (`workspace-write` / `read-only` plus approval policies). Codex sandbox flags belong in `agent "codex" { args = [...] }` in runtime.hcl, not in a settings JSON.
- **Multi-turn turn count.** ateam invokes codex via `exec --json`, which is one-shot, so `Turns: 1` is hardcoded. There is no `turns` field to read.

### codex-tmux design notes

User-facing usage is in [CONFIG.md → `codex-tmux`](CONFIG.md#codex-tmux-experimental). Internals worth knowing if you touch the agent:

- **Host-only in v1.** Rejected with an actionable error at `cmd/table.go:140–142` when a profile binds it to a non-`none` container, and at `cmd/table.go:398–407` when invoked without project context. Container support would require tmux+codex inside the image plus host↔container path translation that isn't wired up.
- **Per-`EXEC_ID` socket and session naming.** The tmux socket lives under `<ProjectDir>/cache/tmux/` and the session name embeds the `EXEC_ID`, so concurrent runs in the same workdir don't collide.
- **Token/cost data is sourced from `$CODEX_HOME/sessions/...`** (the rollout JSONL Codex writes itself), not from a streamed JSON channel — the TUI doesn't emit one. The agent live-tails that rollout into `stream.jsonl` and archives it to `codex-session.jsonl.gz` on completion.
- **`ateam resume` works** because the live-tailed rollout translates `session_meta` into a `thread.started` line in `stream.jsonl`, carrying the same session id the `codex` CLI uses. Resume runs `codex resume --include-non-interactive <id>`, identical to the regular `codex` agent.
- Original design rationale: [`plans/feature_codex_tmux_agent.md`](plans/feature_codex_tmux_agent.md) — historical, not normative.

### Container `extra_volumes` (HCL example)

CONFIG.md lists `ExtraVolumes` in the template-vars table but doesn't show the HCL example. To give the agent access to directories outside the standard mounts, use `extra_volumes` on a container definition (paths can be relative to the project source dir for portability) and/or `container_extra_args` on a profile.

```hcl
// .ateam/runtime.hcl — extend the default docker container with custom mounts
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
  container_extra_args = ["--cpus=2", "--memory=4g"]
}
```

Relative host paths in `extra_volumes` are resolved from the project source directory, making the config portable across machines. `container_extra_args` passes raw flags to `docker run` for resource limits, network modes, or capabilities that don't have dedicated config fields.

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
