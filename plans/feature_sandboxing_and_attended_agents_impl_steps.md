# Sandboxing and attended agents: state and next steps

Design and decisions: `feature_sandboxing_and_attended_agents.md`. User documentation: `ISOLATION.md` (mode ⑤), `CONFIG.md`, `COMMANDS.md`.

Status, 2026-11-23: phase 1 is built on macOS and committed (`d7ecad6`, `10f6d34`, `55d7abf`, `a842f8a`, `1d5bc94`). Next: fence as an embedded sandbox backend (see "Next: fence, embedded").

## What is built

| Area | What | Where |
|---|---|---|
| Profiles | `use`, `read`, `write`, `exclude`, `domains`, `command`, `ateam_files`; optional agent and container; cycle and unknown-name errors; origin of every value | `internal/runtime/profile_compose.go`, `config.go` |
| Filters | `filter` blocks (`file` or `rules`), resolved with autoversion's `filterrules`; filter names in `read`, `write`, `exclude`; checked at load | `internal/sandboxpolicy/filters.go` |
| Policy | Backend-neutral rules from a profile; ateam's own files closed; real paths; read-only paths | `internal/sandboxpolicy/policy.go` |
| nono backend | Profile rendering (macOS), nono's blocks lifted where the profile does not exclude, Seatbelt rules for read-only paths, validation by nono before use | `internal/sandboxpolicy/nono.go` |
| Attended commands | `ateam chat`, `run`, `shell`; `--dry-run`; git comparison after the run | `cmd/run.go`, `cmd/sandbox_launch.go`, `internal/sandboxpolicy/gitguard.go` |
| Profile commands | `ateam profile list`, `show`, `save`, `export`; each layer loads `profiles/*.hcl`; `--profile FILE` | `cmd/profile.go` |
| Unattended runs | The runner uses a nono container like a Docker one, with the same policy code | `internal/container/nono.go`, `cmd/runner_setup.go` |
| ateam inside the sandbox | `agent/ateam`, `ATEAM_IN_SANDBOX=1`, no second sandbox from inside | `cmd/sandbox_launch.go` (`sandboxType`) |
| Built-in profiles | `sandbox/nono`, `agent/secrets`, `agent/claude`, `agent/codex`, `agent/ateam`, `dev/*`, naming filters | `defaults/ateam.hcl` |
| Built-in filters | The path lists ported from `agents.targy`, as embedded filter files; a layer's `filters/*.filter` and `filter` blocks override them; `ateam filter list`, `show` | `defaults/filters/`, `cmd/filter.go` |

## How it was checked

- Unit tests for composition, filters, policy, rendering, launch planning, save round trip, the git guard, the nono container. `make run-ci` passes; `make test-docker test-docker-live` was clean on 2026-11-22 (owner), not rerun since `55d7abf`.
- A test has the real `nono profile validate` accept rendered profiles (skipped without nono).
- Real launches on macOS, nono 0.82.0. The agent's own sandbox cannot nest another one, so they were run as the command line ateam prints, through `nono` directly:
  - `sandbox/nono`: secrets, ateam's files, writes outside the project and every host refused; the project, `.ateam/shared`, `git status` allowed.
  - `sandbox/nono,dev/go`: `~/go/pkg` writable, github.com reachable, example.com refused; `make test` passes.
  - Lifted blocks: `~/.bashrc`, `~/.npmrc`, `~/.zprofile` readable; `~/.aws`, `~/.ssh/config`, keychains, shell history refused.
  - Read-only: `.ateam/ateam.hcl` (with `--read`), `.git/config`, `.git/hooks` readable and not writable; `.git` and `.ateam` cannot be renamed away; `git status`, `git add` work.
  - A filter-based profile: the filter's exclude refused, its write grant works.
  - Claude: `claude -p` under the chat profile, and as an unattended agent with its config directory, completes a turn.
  - `ateam exec` inside a `sandbox/nono,agent/ateam,agent/claude` launch completes a turn.
- By the owner, in a terminal: `ateam exec "reply with: ok" --profile sandbox/nono` (works), `ateam shell` sessions (found three bugs, fixed).

Not checked:

- An interactive `ateam chat` session.
- `ateam code` with sandboxed sub-runs, in either supported setup.
- A real `git commit` inside the sandbox.
- Anything on Linux.

## Known gaps

- macOS only. On Linux the launch is refused.
- Unattended runs: one working folder and one nono profile per command; `container_extra` ignored; no git comparison after the run (the read-only rule applies).
- `chat`, `run`, `shell` refuse `docker` and `docker-exec` containers.
- Network rules name hosts only; no ports, methods or paths. Secrets are not brokered.
- Seen and left: `~/.claude.json.lock` is not writable when it does not exist at launch (nono skips a grant of a missing file; Claude works); nono reports a blocked macOS preference read for Claude.
- nono keeps its own optional groups (`extends: default`): the system paths are nono's, not ateam's.

## Next: fence, embedded

Decided by the owner, 2026-11-23: the next piece of work is a second sandbox backend, [fence](https://github.com/fencesandbox/fence), used **as a Go library** so that ateam carries its own sandbox and needs no external binary. Two purposes: an embedded sandbox, and a stress test of the policy model, which only nono consumes today.

What is known (from the `fence` binary on the owner's machine, `fence --help`; the library API has not been looked at):

- Linux and macOS, no container. Network blocked unless domains are listed.
- Config (`fence.jsonc`): `network.allowedDomains`, `network.deniedDomains`, `filesystem.denyRead`, `filesystem.allowWrite`, `filesystem.denyWrite`, `command.deny`. Flags: `-p PORT` to expose a port, `--settings FILE`, templates.
- Research notes: `research/ResearchSandboxing.md` E.13.2.

Expected mapping from `sandboxpolicy.Policy`:

| Policy | fence |
|---|---|
| `Exclude` | `filesystem.denyRead` |
| `WorkDir` + `Write` | `filesystem.allowWrite` |
| `ReadOnly` (git hooks and config, reopened reads) | `filesystem.denyWrite` |
| `Domains` | `network.allowedDomains` |
| reading open below the home | fence's default |

To find out first:

1. Whether fence is usable as a library: the module path, which packages are importable (not `internal/`), its license, its dependencies, how a caller starts a sandboxed command with a config and a TTY, and whether the library needs a helper binary or re-executes itself.
2. A read that reopens a path inside an excluded folder (`read ~/.ssh/config` with `~/.ssh` excluded): fence shows `denyRead` and no matching allow. If it cannot be expressed, the launch is refused, per the fail-closed rule.
3. fence's own always-protected paths, and whether they can be lifted as ateam does for nono's required groups (decision 12: the profile's excludes decide alone).
4. Localhost: the policy means "localhost open"; fence exposes ports one by one.
5. Whether a rename of a folder above a `denyWrite` path is refused (nono needed explicit pins).
6. Behavior on Linux, where nono is refused today: this may give Linux support without porting targy's home listing.

Shape of the work:

- A renderer beside `internal/sandboxpolicy/nono.go` (`fence.go`): `Policy` in, fence config out, an error for anything fence cannot express. If the neutral `Policy` needs a change to fit fence, change it and keep nono passing: that is the point of the exercise.
- A container type `fence` (`runtime.ContainerTypeNono` is the model: `internal/runtime/config.go` `validateContainerTypes`, `cmd/sandbox_launch.go` `sandboxType` / `planLaunch` / `wrapped`, `cmd/runner_setup.go` `buildNonoContainer`, `internal/container/nono.go`, `cmd/exec_bundle.go` `runInsideContainer`, `cmd/docker.go` `buildContainer`). Embedded means no `exec.LookPath`: if the library has to run in the child process, ateam re-executes itself with a hidden subcommand.
- A built-in profile `sandbox/fence`, next to `sandbox/nono`, reusing `agent/secrets` and the rest unchanged.
- `ATEAM_IN_SANDBOX=1` for the command, as with nono; ateam already detects fence as an outer sandbox (`FENCE_SANDBOX`, `internal/container/isolation.go`).
- `--dry-run` and `ateam profile export --backend`: today export assumes nono.
- The same probes as for nono (the lists under "How it was checked"): secrets and ateam's files refused, the project writable, `.git/hooks` and `.git/config` read-only with git working, hosts allowed and refused, `make test` under `dev/go`, an agent turn attended and unattended, `ateam exec` inside the sandbox with `agent/ateam`.
- Docs: `ISOLATION.md` (mode ⑤ becomes "OS sandbox backends" or fence gets its own part), `CONFIG.md` container types, `README.md`.

## Next steps

In order:

1. **fence, embedded** (above).
2. **Owner: use it.** `ateam chat` and `ateam shell` for real work; `ateam code` with a nono code profile. Each refusal that is not obvious is a missing path or host in a built-in filter or profile.
3. **Linux.** With fence if it works there. For nono: port the home listing walk from targy (`filters/listing.py`), and decide how read-only paths work without Seatbelt (open question 3). Then the run on meerdog.
4. **Retire `nono-sandbox`.** Remove it from `~/dotfiles/bin`; in targy remove the `nono_profile` step and the `launch` target, keep `claude_permissions`, `json_merge`, `claude-settings`. After a period of daily use of the ateam commands.
5. **Phase 2: the `network` entity**, replacing `domains`: default deny, named sets, `host:port`, mapped to each backend or an error. Then `_remove` / `_replace` and trust on first use for project-supplied profiles.
6. **Phase 3: secrets.** Placeholders injected outside the sandbox, the LLM key first (research F.1.1 Tier A; see also `research/ResearchNetworkSandboxing.md`).

Done since the first version of this list: built-in profiles as filter files, `ateam filter list` and `show` (2026-11-23, `1d5bc94`): every filter resolves to the same paths as the literal list it replaced.

Tasks for later, not ordered:

- Attended sessions in the metrics database (usage and cost). `chat` already waits for the agent under a sandbox, so the row can be written there.
- A profile per run for unattended commands (a worktree per role).
- `chat`, `run`, `shell` on Docker containers.
- The git comparison after an unattended run (`finalizeCall`), as a second check.
- Fold `ateam claude` into `ateam chat`.
- Move the shared filters to autoversion's built-ins once the set is stable.

## Notes for whoever continues

- Read `CONCURRENCY.md` before touching `internal/container/` or `internal/runner/`: `NonoContainer` is immutable after construction on purpose.
- A nono launch cannot be started from inside another Seatbelt sandbox (an agent's shell). Test by printing the command with `--dry-run` and running `nono` outside, or run ateam from a plain terminal.
- nono's inspection commands take three seconds each on a fresh home unless `NONO_NO_UPDATE_CHECK=1` is set; ateam sets it.
- `unsafe_macos_seatbelt_rules` is a top-level profile key, accepted by `nono profile validate`, absent from `nono profile guide`.
- Commit only when the owner asks; before a commit run `make run-ci`. `make test-docker` cannot run from an agent's sandbox: ask the owner after changes to `internal/container/`, `internal/runner/`, `internal/agent/`, `cmd/runner_setup.go`.
- A dependency must be fetchable by CI and by other users: check that before adding one (fence included).
- autoversion is public at `github.com/voidexpr/autoversion` and has no tags: `make update-autoversion` pins the latest commit of `main`; `go.mod` records it. It was private at first, which broke the Docker test build: a dependency must be fetchable by CI and by other users.
