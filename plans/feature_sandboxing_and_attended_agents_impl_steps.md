# Sandboxing and attended agents: state and next steps

Design and decisions: `feature_sandboxing_and_attended_agents.md`. User documentation: `ISOLATION.md` (mode ⑤), `CONFIG.md`, `COMMANDS.md`.

Status, 2026-11-23: phase 1 is built on macOS and committed (`d7ecad6`, `10f6d34`, `55d7abf`).

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
| Built-in profiles | `sandbox/nono`, `agent/secrets`, `agent/claude`, `agent/codex`, `agent/ateam`, `dev/*`: literal paths ported from `agents.targy` | `defaults/ateam.hcl` |

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

## Next steps

In order:

1. **Owner: use it.** `ateam chat` and `ateam shell` for real work; `ateam code` with a nono code profile; `make test-docker` after `55d7abf`. Each refusal that is not obvious is a missing path or host in a built-in profile.
2. **Built-in profiles as filter files.** Move the path lists of `defaults/ateam.hcl` into embedded filter files (`agent/secrets`, `dev/go-write`, ...), keep the profiles as thin references. Add `ateam filter show NAME`. Needs embedded filters in the resolver order: HCL blocks, user layer, embedded defaults, autoversion built-ins.
3. **Linux.** Port the home listing walk from targy (`filters/listing.py`): the home listed around the excluded paths, since nono refuses a deny inside a grant there. Decide how read-only paths work without Seatbelt (open question 3). Then the run on meerdog.
4. **Retire `nono-sandbox`.** Remove it from `~/dotfiles/bin`; in targy remove the `nono_profile` step and the `launch` target, keep `claude_permissions`, `json_merge`, `claude-settings`. After a period of daily use of the ateam commands.
5. **Phase 2: the `network` entity**, replacing `domains`: default deny, named sets, `host:port`, mapped to nono's proxy or an error. Then `_remove` / `_replace` and trust on first use for project-supplied profiles.
6. **Phase 3: secrets.** Placeholders injected outside the sandbox, the LLM key first (research F.1.1 Tier A).

Tasks for later, not ordered:

- Attended sessions in the metrics database (usage and cost). `chat` already waits for the agent under a sandbox, so the row can be written there.
- A profile per run for unattended commands (a worktree per role).
- `chat`, `run`, `shell` on Docker containers.
- The git comparison after an unattended run (`finalizeCall`), as a second check.
- Fold `ateam claude` into `ateam chat`.

## Notes for whoever continues

- Read `CONCURRENCY.md` before touching `internal/container/` or `internal/runner/`: `NonoContainer` is immutable after construction on purpose.
- A nono launch cannot be started from inside another Seatbelt sandbox (an agent's shell). Test by printing the command with `--dry-run` and running `nono` outside, or run ateam from a plain terminal.
- nono's inspection commands take three seconds each on a fresh home unless `NONO_NO_UPDATE_CHECK=1` is set; ateam sets it.
- `unsafe_macos_seatbelt_rules` is a top-level profile key, accepted by `nono profile validate`, absent from `nono profile guide`.
- autoversion is public at `github.com/voidexpr/autoversion` and has no tags: `make update-autoversion` pins the latest commit of `main`; `go.mod` records it. It was private at first, which broke the Docker test build: a dependency must be fetchable by CI and by other users.
