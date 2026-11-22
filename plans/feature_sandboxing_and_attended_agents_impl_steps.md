# Sandboxing and attended agents: implementation steps (phase 1)

Design: `feature_sandboxing_and_attended_agents.md`. Status, 2026-11-21: steps 1 to 5 built for attended launches on macOS; see "State".

## State (2026-11-21)

Built, with unit tests, `make run-ci` passing:

- Step 1: profile composition (`internal/runtime/profile_compose.go`): `use`, `read`, `write`, `exclude`, `domains`, `command`; optional agent and container; cycle and unknown-name errors; origins.
- Step 2: `ateam profile list`, `ateam profile show`.
- Step 3: `ateam run`, `ateam shell`, `ateam chat` (`cmd/run.go`, `cmd/sandbox_launch.go`), on `none` and `nono` containers.
- Step 4: `ateam profile save`; each layer loads `profiles/*.hcl`; `--profile FILE`.
- Step 5: `internal/sandboxpolicy` (neutral policy, nono renderer, macOS), the `nono` container type, ateam's implicit rules, `--dry-run`, `ateam profile export`.
- Built-in profiles in `defaults/ateam.hcl`: `sandbox/nono`, `agent/secrets`, `agent/claude`, `agent/codex`, `dev/*`, with literal paths ported from `agents.targy`.

Checked how:

- Unit tests for composition, policy building, rendering, launch planning, save round trip.
- The rendered profiles are accepted by the real `nono profile validate` (a test, skipped without nono).
- Real launches on macOS (nono 0.82.0), with the profiles ateam generates, run through `nono` directly because the agent's own sandbox cannot nest another one:
  - `sandbox/nono`: 19 probes, all as designed. Refused: `~/.ssh/config`, `~/.aws`, `~/.zshrc`, writes outside the project, `~/.ateam`, `.ateam/ateam.hcl` and `state.sqlite` (read and write), creating `.ateam/profiles/x.hcl`, every network host. Allowed: the project, `.ateam/shared`, `~/.gitconfig`, `git status`.
  - `sandbox/nono,dev/go`: the same, plus `~/go/pkg` writable, github.com reachable, example.com refused. `make test` passes under it.
  - `ateam chat` profile (`agent/claude` added): `claude -p` answers, signed in through the keychain grant.
  - `.git/hooks` is writable, as the known gap says.
- Seen in those runs, not fixed: `~/.claude.json.lock` is not writable (it does not exist at launch and nono skips a grant of a missing file; Claude worked anyway); nono reports a blocked macOS preference read for Claude.
- Not tried: an interactive `ateam chat` or `ateam shell` in a terminal, and `ateam run` itself doing the exec (only the command line it prints was run).

Decided and built on 2026-11-22:

- **Git hooks**: compared after the run. Attended launches under a sandbox wait for the command, then compare `.git/config` and `.git/hooks` with a snapshot. A change is listed, the earlier versions are kept under `~/.ateam/state/sandbox/git.<pid>/`, exit code 3.
- **nono's default blocks**: targy's behavior. Every path nono's required groups block that the profile does not exclude is lifted (`.bashrc`, `.npmrc`, ...). The excludes decide alone. Checked with nono: a lifted file and a lifted link are readable; an excluded path stays closed; a grant inside an excluded folder works.
- **Unattended runs under nono**: built. `internal/container/nono.go` is a `Container` the runner uses like the Docker ones; the policy code is the one `chat` uses (`buildSandboxPolicy`, `writeNonoProfile`). `exec --profile sandbox/nono --dry-run` shows the wrapped command; the same command run through nono starts Claude, which reaches the API (the unattended config directory's login had expired, so no full agent turn was seen: `ateam login`, then try `ateam exec "..." --profile sandbox/nono`).

Different from the steps below:

- `domains` is a profile list already (phase 2 planned a `network` entity). Without it a sandboxed build could reach no registry. The `network` entity replaces it later.
- The built-in profiles are named with a folder prefix (`dev/go`, `agent/claude`). A profile named `claude` would shadow the agent shorthand `--profile claude` of unattended runs.
- No `--NAME` shorthand: `--profile a,b` instead. cobra rejects unknown flags.
- `chat`, `run` and `shell` refuse `docker` and `docker-exec` containers for now.
- nono keeps its own optional groups (`extends: default`, nothing excluded): the system paths are nono's, not a copy in ateam.

Consequences of nono for unattended runs (the container is the same; what runs in it is not):

1. **A role that calls `ateam` from inside the agent cannot run under nono.** The `code` supervisor pipes prompts into `ateam exec` for each sub-run. Under Claude's own sandbox this works because `ateam:*` is an excluded command that runs outside it. nono wraps the whole process and has no such exit: the inner `ateam` cannot read ateam's files (closed by the policy) and cannot start a second nono. Options: keep the supervisor on a profile without nono and give nono to the sub-runs it starts (works today, the supervisor only orchestrates); or open ateam's files to the supervisor and let inner runs share the outer sandbox through `sandbox_detection`; or a broker outside the sandbox that starts sub-runs on request.
2. **One profile per runner, written once.** Every run of a runner shares the working folder of the launch. A per-run folder (a worktree per role) would need a profile per run.
3. **No hooks comparison yet**: the runner has no step after the agent ends where `runGuarded`'s check fits. To add in `finalizeCall`.
4. **`container_extra` is ignored**, and `forward_env` has no meaning: the agent has the host environment, as with no container.
5. **Login**: the agent uses its host login (keychain or `config_dir`). Unlike Docker, no secret is required up front.

Not built:

- Attended sessions in the metrics database (task, later). `chat` now waits for the agent under a sandbox, so the row can be written there.
- The hooks comparison for unattended runs (consequence 3).
- `chat`, `run`, `shell` on Docker containers.
- Linux; filters (step 6); retiring `nono-sandbox` (step 7).

## Before coding

| # | Check | State |
|---|---|---|
| A | `.ateam` paths agents touch | Done: `runtime/<exec_id>/`, `shared/`, `cache/` (see the design, "Where profiles live") |
| B | Claude command deny rules under `--dangerously-skip-permissions` | Not run: Claude's login is not usable from the agent's sandbox. Command below, for the owner |
| C | `DEV.md` says `roles/` and `supervisor/`, `CONFIG.md` and the prompts say `shared/` | To settle: which is current |
| D | Agent attributes `rw_paths`, `ro_paths`, `denied_paths` exist in `hclAgent` | To read: what consumes them, and whether profile `read`/`write`/`exclude` feed the same path |
| E | Wrapper phase 0 | Proposed: skip |
| F | Default container | Proposed: the `default` profile's container, no new top-level setting |

Check B:

```bash
cd "$(mktemp -d)" && printf '{"permissions":{"deny":["Bash(echo deny-probe*)"]}}' > s.json
claude -p --dangerously-skip-permissions --settings ./s.json \
  "Run exactly this bash command and report its literal output or the exact error: echo deny-probe-123"
```

Denied: the rule holds. `deny-probe-123` printed: the rule does not hold, and the deny list has no effect under a sandbox backend.

## Step 1: profiles compose

`internal/runtime/config.go`, `CONFIG.md`, header of `defaults/ateam.hcl`.

- `hclProfile` / `ProfileConfig`: add `use`, `read`, `write`, `exclude`, `command`. `agent` and `container` become optional.
- New `(*Config).ResolveComposedProfile(names []string, overrides)`:
  - walks `use` depth-first, in order; a cycle is an error naming the chain; an unknown name is an error.
  - lists add up (deduped, order kept); scalars: nearest wins (own value, then later `use`, then earlier).
  - records where each entry came from, with the existing `Contribution` / `record` machinery.
- `ResolveProfile` keeps its signature for current callers and goes through the new resolver. A profile without `use` resolves exactly as today.
- `validateProfiles`: agent and container references are checked only when set.
- Missing agent or container after composition: filled from the `default` profile (check F).
- Tests: composition order, nearest-wins, cycle, unknown name, provenance, and every built-in profile resolving unchanged.

Not in this step: `_remove`, `_replace` (phase 2).

## Step 2: `ateam profile show` and `list`

`cmd/profile.go` (new), `COMMANDS.md`.

- `profile list`: names, with agent and container when set.
- `profile show NAME...`: the composed result, each entry with its source (layer and profile). Accepts the launch flags of step 3, so it shows exactly what a launch would use.
- Reuse the rendering `ateam env` already has for provenance.

## Step 3: `run`, `shell`, `chat` on existing containers

`cmd/run.go`, `cmd/shell.go`, `cmd/chat.go` (new), shared flag helper, `cmd/runner_setup.go`, `COMMANDS.md`, `README.md`.

- Shared flags: `--profile` (repeatable; a name or a file path), `--NAME` shorthand, `--read`, `--write`, `--exclude`, `--dry-run`.
  - `--NAME` shorthand: unknown flags are tried as profile names. Check how this sits with cobra; if it fights the flag parser, keep only `--profile NAME` in this step.
- `run`: the profile's `command` plus arguments after `--`, or the command after `--`; `--command` replaces the saved one; neither: an error.
- `shell`: `$SHELL`, ignoring `command` and `agent`.
- `chat`: the agent of the composed profile, with the terminal attached; arguments after `--` go to the agent. `cmd/claude.go` and `cmd/resume.go` already exec an agent interactively: share that code, do not copy it.
- Launch goes through the `Container` interface (`CmdFactory`), so `none`, `docker` and `docker-exec` work at once.
- Fail closed: a composed profile with `read`, `write` or `exclude` on a container that cannot enforce them is refused, with the container named. In this step only an empty policy launches.
- Metrics: `chat` records a row in the call database (start, end, agent, profile, and usage and cost when the agent's session data gives them). No log capture.
- Tests: argument rules of `run`, `--command`, refusal of an unenforceable policy, dry-run output.

## Step 4: `profile save`

`cmd/profile.go`.

- `ateam profile save NAME [launch flags]`: writes `~/.ateam/profiles/NAME.hcl` holding a `profile "NAME"` block: `use` for the referenced profiles, the paths given, `command` when `--command` is given. `--project` writes to the project's `.ateam/profiles/` instead.
- `Load` reads `profiles/*.hcl` in each layer, after `ateam.hcl`.
- Refuses to replace an existing file without `--force`.
- Launches nothing.
- Tests: round trip (save, then `profile show` equals the flags), refusal without `--force`.

## Step 5: nono container type, literal paths

`internal/container/nono.go` (new), `internal/sandboxpolicy/` (new, backend-neutral), `internal/runtime/config.go` (`validateContainerTypes`), `defaults/ateam.hcl`, `ISOLATION.md`, `CONFIG.md`.

- `internal/sandboxpolicy`: the composed profile as a neutral `Policy` (read, write, exclude as absolute paths; later network). One function per backend renders it or returns an error. No nono vocabulary outside the nono renderer.
- nono renderer, ported from targy `tools/nono.py` and `steps/nono.py`:
  - macOS: one grant of the home (`allow_parent_of_protected`), `exclude` paths denied inside it.
  - Linux: refuse with a clear message until step 6 brings the listing walk (nono refuses a deny inside a grant there).
  - nono's required groups read from nono, lifted for paths the policy keeps readable; optional groups excluded, their paths taken from the `system/*` paths.
  - `suppress_save_prompt`; network: nono's open default is not acceptable, so pass the LLM hosts and localhost as a fixed list until the `network` entity exists (phase 2).
  - written beside the final path, checked by `nono profile validate`, then renamed. State under `~/.ateam/state/`.
- `Container` implementation: `CmdFactory` wraps the command as `nono run --profile FILE --workdir DIR --allow-cwd -- CMD`. `Clone` per the pool rules in `CONCURRENCY.md`; one profile file per exec.
- "Inside a container" for the agent: a nono container sets the same context as Docker, so `inside_container` applies (sandbox settings dropped, permission prompts skipped).
- Implicit rules added to every policy: ateam's files (design, "Where profiles live"), `.git/hooks`, `.git/config`, agent settings inside writable folders.
- `--dry-run` and `ateam profile export NAME --backend nono --out DIR` share the renderer: `profile.json`, `run.sh`, `RULES.md`, and the command line printed.
- Tests: renderer unit tests against fixtures; a test like targy's `make test-nono` (a generated profile on a scratch home, accesses checked by the real nono), skipped when nono is absent.

## Step 6: filters

Needs `make update-autoversion` (private repo; pin the commit that has the resolver).

- `filter "NAME" { file | rules }` blocks in HCL; filter files embedded in `defaults/filters/` with folder prefixes (`dev/go`, `agent/secrets`, `system/read-macos`, ...), ported from `agents.targy`.
- A `filterrules.Resolver` over: HCL blocks, then user-layer files, then embedded defaults. `NewWith` is called at config load, so errors surface there.
- `read`, `write`, `exclude` entries: `~` or `/` first is a path, anything else a filter name; unknown: an error.
- `Rules.List()` feeds `sandboxpolicy`. The home listing walk for Linux (targy `filters/listing.py`) comes over here.
- Base profiles in `defaults/ateam.hcl`: `go`, `node`, `python`, `rust`, `dev`, `claude`, ...
- `ateam filter show NAME`: the resolved filter in the shared syntax.
- Tests: resolver order, shadowing, the rendered nono profile equal to targy's for the same filters (fixture taken from a targy run).

## Step 7: retire the launcher

- Linux run on meerdog with the ateam implementation.
- `ateam chat` and `ateam run` replace `nono-sandbox`; remove it from `~/dotfiles/bin`.
- targy: remove the `nono_profile` step and the `launch` target; keep `claude_permissions`, `json_merge` and the `claude-settings` target, plus one new target for "Claude under an outside sandbox" settings.
- Docs: `ISOLATION.md` gains the nono mode and the attended commands.

## Order and size

| Step | Depends on | Rough size |
|---|---|---|
| 1 | — | config code + tests |
| 2 | 1 | small |
| 3 | 1 | three commands, shared launch path |
| 4 | 1, 3 | small |
| 5 | 1, 3 | the largest: renderer, container type, export |
| 6 | 5, autoversion commit | filters, base profiles, Linux walk |
| 7 | 6, Linux run | cleanup across three repos |
