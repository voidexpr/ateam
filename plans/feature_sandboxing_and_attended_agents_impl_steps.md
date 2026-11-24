# Sandboxing and attended agents: state and next steps

Design and decisions: `feature_sandboxing_and_attended_agents.md`. What each backend can express, per operating system: `SANDBOX_DIFFERENCES.md`. User documentation: `ISOLATION.md` (mode ⑤), `CONFIG.md`, `COMMANDS.md`.

Status, 2026-11-23: phase 1 is built on macOS and committed (`d7ecad6`, `10f6d34`, `55d7abf`, `a842f8a`, `1d5bc94`). fence as an embedded sandbox backend is built, not committed and not yet run for real (see "fence, embedded").

## What is built

| Area | What | Where |
|---|---|---|
| Profiles | `use`, `read`, `write`, `exclude`, `domains`, `command`, `ateam_files`; optional agent and container; cycle and unknown-name errors; origin of every value | `internal/runtime/profile_compose.go`, `config.go` |
| Filters | `filter` blocks (`file` or `rules`), resolved with autoversion's `filterrules`; filter names in `read`, `write`, `exclude`; checked at load | `internal/sandboxpolicy/filters.go` |
| Policy | Backend-neutral rules from a profile; ateam's own files closed; real paths; read-only paths | `internal/sandboxpolicy/policy.go` |
| nono backend | Profile rendering (macOS), nono's blocks lifted where the profile does not exclude, Seatbelt rules for read-only paths, validation by nono before use | `internal/sandboxpolicy/nono.go` |
| Attended commands | `ateam chat`, `run`, `shell`; `--dry-run`; git comparison after the run | `cmd/run.go`, `cmd/sandbox_launch.go`, `internal/sandboxpolicy/gitguard.go` |
| Profile commands | `ateam profile list`, `show`, `save`, `export`; each layer loads `profiles/*.hcl`; `--profile FILE` | `cmd/profile.go` |
| Unattended runs | The runner uses a sandbox container (nono or fence) like a Docker one, with the same policy code | `internal/container/sandbox.go`, `cmd/runner_setup.go` |
| fence backend | A second backend, embedded as a library; see "fence, embedded" | `internal/sandboxpolicy/fence.go`, `cmd/sandbox_fence.go` |
| ateam inside the sandbox | `agent/ateam`, `ATEAM_IN_SANDBOX=1`, no second sandbox from inside | `cmd/sandbox_launch.go` (`sandboxType`) |
| Built-in profiles | `sandbox/nono`, `sandbox/fence`, `agent/secrets`, `agent/claude`, `agent/codex`, `agent/ateam`, `dev/*`, naming filters | `defaults/ateam.hcl` |
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

## fence, embedded

Decided by the owner, 2026-11-23: a second sandbox backend, [fence](https://github.com/fencesandbox/fence), used **as a Go library** so that ateam carries its own sandbox and needs no external binary. Two purposes: an embedded sandbox, and a stress test of the policy model, which only nono consumed.

State, 2026-11-23: built on macOS, committed (`2e01bc7`). The file, git and host probes pass in a real run by the owner (see "Sandbox probes"). Not yet run: an interactive shell, an agent turn, a toolchain build.

### What was found

The library:

- Module `github.com/fencesandbox/fence`, package `pkg/fence` (the rest is `internal/`). Apache-2.0. v0.1.67 (2026-09-01), served by proxy.golang.org and sum.golang.org.
- New modules in `go.mod`: fence, `bmatcuk/doublestar/v4`, `things-go/go-socks5`, `tidwall/jsonc`, `golang.org/x/term`; `golang.org/x/sys` goes from v0.46.0 to v0.47.0. The Linux companion still builds with `CGO_ENABLED=0`.
- API: `NewManager(cfg)`, `Initialize()` starts an HTTP and a SOCKS proxy in the calling process, `WrapCommandInDir(command, dir)` returns a shell string (`env ... sandbox-exec -p PROFILE bash -c COMMAND`), which the caller runs with its own terminal. `CheckReadPath` / `CheckWritePath` answer whether a configuration allows a path.
- macOS: no helper binary; it runs the system's `sandbox-exec`. The proxies must live as long as the command, so the process that calls the library must stay.
- Linux: needs `bwrap` and `socat` installed, and the caller must call `fence.DispatchInternalHelper(os.Args)` first in `main` and name itself as helper (`SetLinuxHelperPath`). So on Linux it is not "no external binary".
- The `go` command cannot check TLS certificates from the agent's sandbox. The module was fetched with `curl` into a local file proxy and every new `go.sum` line was compared with sum.golang.org: all 20 match. The owner should run `go mod verify` once outside the sandbox.

The six questions:

| # | Question | Answer |
|---|---|---|
| 1 | Usable as a library | Yes, see above. ateam re-executes itself (`ateam fence-exec`, hidden) so that one process per run holds the proxies. |
| 2 | A read that reopens a path inside an excluded folder | Cannot be expressed. In fence a deny wins over an allow, in both read modes (the deny rules are written after the allow rules in the Seatbelt profile). The launch is refused and the error names both rules. |
| 3 | fence's own always-protected paths | None for reads: decision 12 holds without lifting anything. For writes there is a fixed list that no setting lifts (see ISOLATION.md): stricter than the policy inside the working folder. A `write` grant of one of them is an error. |
| 4 | Localhost | macOS: `allowLocalBinding` and `allowLocalOutbound` open localhost as a whole. Linux: outbound to the host's localhost is one listed port at a time (`allowLocalOutboundPorts`). |
| 5 | Rename of a folder above a `denyWrite` path | macOS: refused. fence adds `deny file-write-unlink` on every folder above a denied path, up to `/`. Seen in the generated profile, not run. Linux: not known (the path is a read-only bind mount). |
| 6 | Linux | Refused by the renderer for now: localhost cannot be opened as a whole (question 4), and nothing was run there. |

What the stress test showed about the policy model:

- `Exclude` means "neither readable nor writable", and nono's `deny` does both. In fence `denyRead` leaves a path writable when a write grant surrounds it (`.ateam/ateam.hcl` in the working folder). The renderer puts every exclude in `denyRead` and `denyWrite`. The mapping table of the first version of this section missed that.
- A write grant does not make a path readable in fence's Seatbelt profile: writable paths are also listed in `allowRead`.
- "A grant inside an exclude" is a nono shape (deny plus bypass). Three places use it, and all three are refused under fence:
  - `dev/go`: `dev/go-read` opens `~/Library/Application Support/go` inside the excluded `~/Library/Application Support`.
  - An unattended run: the agent's config folder and, without a project, its output lie inside the excluded `~/.ateam`. Fixed in the policy, see open point 2.
  - Any `--read` or `--write` below an excluded folder.
- The `Policy` type did not change. `Policy.Gaps` is still unused.

### What is built

| What | Where |
|---|---|
| Renderer: `Policy` to a fence configuration; refuses Linux and a grant inside an exclude | `internal/sandboxpolicy/fence.go` |
| `ateam fence-exec` (hidden): loads the configuration, starts the proxies, runs the command under the sandbox with `ATEAM_IN_SANDBOX=1`, drops `LD_*` and `DYLD_*` variables; refuses to start inside a sandbox | `cmd/sandbox_fence.go` |
| A check before each launch: fence is asked about every rule of the policy on the file written (`checkFenceConfig`) | `cmd/sandbox_fence.go` |
| Container type `fence`, `runtime.IsSandboxBackend` | `internal/runtime/config.go` |
| `chat`, `run`, `shell`, `--dry-run` | `cmd/sandbox_launch.go` (`sandboxPrefix`) |
| Unattended runs: `NonoContainer` became `SandboxContainer` with a `Kind`; `buildNonoContainer` became `buildSandboxContainer` | `internal/container/sandbox.go`, `cmd/runner_setup.go` |
| `ateam profile export`: `fence.json`, and `--backend nono|fence` | `cmd/profile.go` |
| Container `fence`, profile `sandbox/fence` | `defaults/ateam.hcl` |

Choices made while building, for the owner to confirm:

- Reading is closed by default (`defaultDenyRead`) and opened for the home and the grants, as the policy says. fence adds the system folders, `/tmp` and `/opt`.
- fence's own list of refused commands (`shutdown`, `reboot`, ...) is turned off: the policy has no command rule, and both backends then run the same commands.
- `allowPty` is on, so a shell or an agent can open a pseudo-terminal.
- Without a domain the network is closed, localhost included, as under nono.
- `profile export` for fence writes a `run.sh` that uses the `fence` command, so the export works without ateam. The installed `fence` may be older than the embedded one; not checked.
- `fence.DispatchInternalHelper` is not called in `main`: only Linux needs it.

### How it was checked

- Unit tests: the renderer (`internal/sandboxpolicy/fence_test.go`); launch planning, the unattended container, the refusals, the nesting guard (`cmd/sandbox_launch_test.go`); the Seatbelt profile the library generates from a rendered configuration (`cmd/sandbox_fence_test.go`).
- `make build-all`, `make test-cli`, `make fmt-check`, `check-tidy`, `check-docs`, `lint`, `deadcode`: pass. `make vuln` skipped itself (no network).
- `make test`: one failure, `TestValidateLocalPath/dot-claude_rejected`. It fails the same way on the unmodified tree from the agent's sandbox.
- `ateam run --dry-run --profile sandbox/fence,X` for every built-in profile X, with a scratch home: all render except `dev/go`.
- A real launch from the agent's shell reaches `sandbox-exec` and stops there (`sandbox_apply: Operation not permitted`, exit 71).

Not checked:

- Any real launch. `make test-sandbox-fence` runs the file, git and host probes (see "Sandbox probes" below). The rest by hand, from a plain terminal, in a scratch project:
  - `ateam run --profile sandbox/fence -- sh -c 'cat ~/.ssh/config; cat .ateam/ateam.hcl; touch ~/outside; touch inside; git status'`: the first three refused, the last two work.
  - Read-only: `ateam run --profile sandbox/fence -- sh -c 'echo x >> .git/config; touch .git/hooks/x; mv .git .git2; git add -A'`: only `git add` works.
  - Network: `ateam run --profile sandbox/fence,dev/github -- sh -c 'curl -sI https://github.com | head -1; curl -sI https://example.com | head -1'`: the first answers, the second is refused. Without `dev/github`: both refused.
  - `ateam run --profile sandbox/fence,dev/rust -- cargo build` (or `dev/node`), since `dev/go` is refused.
  - `ateam shell --profile sandbox/fence`: prompt, job control (Ctrl-Z, `fg`), Ctrl-C, exit code.
  - `ateam chat --profile sandbox/fence` and `ateam exec "reply with: ok" --profile sandbox/fence` in a project: one agent turn each.
  - `ateam shell --profile sandbox/fence,agent/ateam,agent/claude`, then `ateam exec "ping" --model haiku` inside.
- Programs that ignore `HTTP_PROXY`: under fence they reach no host. Claude Code and Go honor it; others to find by use.
- `make test-docker`: the owner, since `internal/container/` and `cmd/runner_setup.go` changed.
- Linux.

### Open points for the owner

0. A user's `--read` or `--write` inside an excluded folder (`~/.ssh/config`): accepted as refused under fence (owner, 2026-11-23). Agents are not given `~/.ssh` that way in practice; see the dotfiles profile under "Tasks for later".
1. `dev/go` under fence: decided 2026-11-23, kept as it is. `dev/go` works under nono and stays refused under fence; to revisit. What is known for then:
   - The read is `~/Library/Application Support/go`: the file `env` (settings saved with `go env -w`) and Go's telemetry counters.
   - Without it Go works: with the folder unreadable (file permissions in a scratch home, not a sandbox refusal), `go env`, `go build` and `go vet` pass and the settings of `env` are ignored. So dropping the read from `dev/go` would make it work under both backends, at the cost of `go env -w` settings inside the sandbox.
   - Opening the parent folder instead is ruled out: it holds every app's data, and excluding known secrets is the point of the sandbox.
   - The other ways: an allow that wins inside a deny in fence (a fork or upstream).
2. Unattended runs under fence: done 2026-11-23. They were refused with and without a project: the agent's config folder (`~/.ateam/state/agents/...`) and, without a project, the run's output lie inside `~/.ateam`, excluded as a whole. `sandboxpolicy.Build` now closes an exclude of ateam's own entry by entry around a grant inside it (`splitAround`), with the known names closed before they exist. Both backends get the same rules; without such a grant (chat, run, shell) nothing changes. Checked by tests and by `ateam exec --dry-run` with both backends; not by a real unattended run.
3. Linux: decided 2026-11-23. `bwrap` and `socat` are acceptable requirements, documented in ISOLATION.md ("Where a sandbox backend can start"), with an early failure when one is missing. Not coded yet: the renderer still refuses Linux one step earlier. See "Next steps", 3.
4. Carve-outs (points 1 and 2) by changing fence itself, in a fork. Read of the source, v0.1.67:
   - macOS: small. The Seatbelt profile lists allows, then denies, and the last matching rule wins. A carve-out is an allow written after the denies for each allowed path inside a denied one (`generateReadRules`, `generateWriteRules` in `internal/sandbox/macos.go`), plus the rename guard (`deny file-write-unlink` on the denied subtree must be reopened for a writable carve-out), plus the same precedence in `CheckReadPath` / `CheckWritePath`. nono does the same on Seatbelt, and `dev/go` works under it.
   - Linux: larger. A denied folder is hidden by mounting an empty one over it, and the mount planner is written so that a later mount cannot open a hidden folder. A carve-out is a bind mount of the subfolder after the mask, ordered by depth, with masks inside it applied again.
   - A fork is a `replace` line in `go.mod` to a public repository, pinned by commit as autoversion is. Cost: following upstream, and owning a change in security code.

## Sandbox probes

`test/sandbox/run-probes.sh` starts `test/sandbox/sandbox-probe.sh` under `ateam run --profile sandbox/BACKEND` in a scratch project (`~/.cache/ateam-sandbox-probe`, with its own `ATEAM_HOME`) and prints PASS or FAIL per expectation. Same script on macOS and Linux, for both backends.

- `make test-sandbox`, `make test-sandbox-nono`, `make test-sandbox-fence`. From a plain terminal: a sandbox cannot start inside another one. Not part of `make test-all`.
- Suite `base`: the working folder and a `--write` path writable; the rest of the home readable and not writable; an excluded folder closed for reads and writes, outside and inside the working folder, and not renamable; ateam's own files closed, `.ateam/shared` open; `~/.ssh` closed; git status, add and commit working with `.git/config` and `.git/hooks` read-only and `.git` not renamable; a `--domain` host reachable and another refused.
- Suite `carve`: a `--read` and a `--write` inside an excluded folder; read-only inside the working folder. Under fence the launch is refused, which the script reports as known.
- Checked from the agent's sandbox: `SANDBOX_PROBE_DRY_RUN=1 make test-sandbox` plans every launch; the probe run with no sandbox reports each missing refusal as FAIL. Not checked: a real run. On Linux both backends are refused before the launch for now, so the targets report "NOT STARTED" there.
- The dry run found one bug, fixed: the check before a fence launch compared an excluded link (`~/.claude` kept in a synced folder) by its own spelling and refused the launch.
- Real run on macOS, owner, 2026-11-23 (nono 0.82.0, `2e01bc7`): nono `base` 27 of 27 and `carve` 8 of 8; fence `base` 26 of 27, `carve` refused at launch as expected. The one fence failure was the probe: `~/.ssh` is a link there, and `ls` of a link whose target is closed prints the link's name and succeeds. The check now lists the content (`ls ~/.ssh/`); rerun the same day: fence `base` 27 of 27.
- What that failure does show: fence resolves a denied link to its target and leaves the link itself visible (its name and where it points); nono closes both. A fork can deny both spellings.
- Linux, owner on meerdog, 2026-11-23 (nono 0.82.0, kernel 7.1):
  - nono still refuses to start with a deny inside a grant ("Landlock deny-overlap is not enforceable on Linux"). `nono profile validate` accepts the same profile: validation does not catch it.
  - nono's guide points to "supervised mode" (`"security": {"capability_elevation": true}`), which checks file opens against the deny list through seccomp-notify. To test before porting the home listing from targy: whether it starts with a deny inside a grant, and whether it asks a question at run time, which an unattended run cannot answer.
  - `allow_parent_of_protected` is ignored on Linux: a grant of a folder that holds nono's own state (`~/.nono`, `~/.local/state/nono`) is always rejected. So the home cannot be one grant there, whatever the deny rules: either supervised mode lifts this too, or the home is listed entry by entry.
  - Required deny groups on Linux: `deny_browser_data_linux`, `deny_credentials`, `deny_keychains_linux`, `deny_shell_configs`, `deny_shell_history`.
  - Tested the same day with `capability_elevation` on: both refusals stay. A deny inside a grant: "Landlock deny-overlap is not enforceable on Linux". The home as one grant: "overlaps protected nono state root ~/.local/state/nono". The guide's advice does not hold for 0.82.0 as run here (`nono run --silent --profile`); no other flag was tried.
  - Conclusion: nono on Linux can only take grants that contain nothing denied. That means the home listed entry by entry around every exclude and around nono's own state (the targy port), and no read-only path inside a writable folder (`.git/hooks` in the working folder), since Landlock has no deny. fence on Linux hides a denied folder with a mount and makes a path read-only with a mount, so it can express both: fence is the better first backend for Linux.
  - `open_port` 0 (all of localhost) is macOS only in nono; Linux takes explicit ports. So localhost by port (step 5) is needed for nono on Linux too, not only for fence.
- Not covered by the script: an interactive shell, an agent turn, `ateam exec` inside the sandbox, a `dev/` toolchain build.

## Next steps

In order:

1. **fence: the owner's real runs** (the probes under "fence, embedded"), then the open points there.
2. **Owner: use it.** `ateam chat` and `ateam shell` for real work; `ateam code` with a nono code profile. Each refusal that is not obvious is a missing path or host in a built-in filter or profile.
3. **Linux.** With fence first; `bwrap` and `socat` as requirements are accepted (owner, 2026-11-23). Before the renderer accepts Linux:
   - Fail early, before anything is written or started: `bwrap` and `socat` in `PATH`, and a probe that `bwrap` can create its namespaces (user and network). The error names what is missing and how to install it (`bubblewrap`, `socat`).
   - fence falls back to running **without a network namespace** when it cannot create one (Docker without `NET_ADMIN`, CI runners, Ubuntu 24.04 AppArmor): only the proxy variables then carry the domain rules. ateam must refuse that case, not accept it: a weakened rule.
   - Localhost by port, from the `network` entity (step 5).
   - `fence.DispatchInternalHelper(os.Args)` first in `main`, and `SetLinuxHelperPath(os.Executable())` in `fence-exec`.
   - Check on meerdog: the rename of a folder above a read-only path; the probes of the macOS list.
   - For nono on Linux, later: port the home listing walk from targy (`filters/listing.py`), and decide how read-only paths work without Seatbelt (open question 3).
4. **Retire `nono-sandbox`.** Remove it from `~/dotfiles/bin`; in targy remove the `nono_profile` step and the `launch` target, keep `claude_permissions`, `json_merge`, `claude-settings`. After a period of daily use of the ateam commands.
5. **Phase 2: the `network` entity**, replacing `domains`: default deny, named sets, `host:port`, mapped to each backend or an error. Then `_remove` / `_replace` and trust on first use for project-supplied profiles.
   - Localhost with ports (needed for fence on Linux). Today a profile with a domain means "localhost open, any port". The entity must be able to say: `localhost` (all ports), `localhost:PORT` (connect to a server on the host), and a port the sandboxed command listens on that the host must reach.
   - Mapping: nono and fence on macOS open localhost as a whole (a `localhost:PORT` rule is then wider than asked: an error or a stated gap, to decide). fence on Linux: `network.allowLocalOutboundPorts` for outbound, `Manager.SetService` exposures for a listening port; plain `localhost` cannot be expressed there and is an error.
   - A dynamically chosen port stays the open question 6 of the design file.
6. **Phase 3: secrets.** Placeholders injected outside the sandbox, the LLM key first (research F.1.1 Tier A; see also `research/ResearchNetworkSandboxing.md`).

Done since the first version of this list: built-in profiles as filter files, `ateam filter list` and `show` (2026-11-23, `1d5bc94`): every filter resolves to the same paths as the literal list it replaced.

Tasks for later, not ordered:

- A profile for sessions that work on dotfiles and `~/.ssh/config` (owner, 2026-11-23). Such a session changes its rules through a dedicated profile instead of opening one file inside an excluded folder. The result must be the same under nono and fence, so it cannot rely on a path opened inside a folder that stays excluded.

- Attended sessions in the metrics database (usage and cost). `chat` already waits for the agent under a sandbox, so the row can be written there.
- A profile per run for unattended commands (a worktree per role).
- `chat`, `run`, `shell` on Docker containers.
- The git comparison after an unattended run (`finalizeCall`), as a second check.
- Fold `ateam claude` into `ateam chat`.
- Move the shared filters to autoversion's built-ins once the set is stable.

## Notes for whoever continues

- Read `CONCURRENCY.md` before touching `internal/container/` or `internal/runner/`: `SandboxContainer` (nono and fence) is immutable after construction on purpose. A fence run starts its own `ateam fence-exec` process, which holds that run's proxies: nothing is shared between runs.
- The `go` command cannot check TLS from an agent's sandbox, so it cannot fetch a module. To add or update one: let the owner run `go get`, or fetch the module files with `curl` into a `file://` proxy and compare the new `go.sum` lines with sum.golang.org.
- A nono launch cannot be started from inside another Seatbelt sandbox (an agent's shell). Test by printing the command with `--dry-run` and running `nono` outside, or run ateam from a plain terminal.
- nono's inspection commands take three seconds each on a fresh home unless `NONO_NO_UPDATE_CHECK=1` is set; ateam sets it.
- `unsafe_macos_seatbelt_rules` is a top-level profile key, accepted by `nono profile validate`, absent from `nono profile guide`.
- Commit only when the owner asks; before a commit run `make run-ci`. `make test-docker` cannot run from an agent's sandbox: ask the owner after changes to `internal/container/`, `internal/runner/`, `internal/agent/`, `cmd/runner_setup.go`.
- A dependency must be fetchable by CI and by other users: check that before adding one (fence included).
- autoversion is public at `github.com/voidexpr/autoversion` and has no tags: `make update-autoversion` pins the latest commit of `main`; `go.mod` records it. It was private at first, which broke the Docker test build: a dependency must be fetchable by CI and by other users.
