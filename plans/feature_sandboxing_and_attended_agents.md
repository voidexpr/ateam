# Sandboxing and attended agents in ateam

Status, 2026-11-23: phase 1 is built and in use on macOS, except Linux and the cleanup in targy. What is built, how it was checked and what is next: `feature_sandboxing_and_attended_agents_impl_steps.md`. User documentation: `ISOLATION.md` (mode ⑤), `CONFIG.md` (composable profiles, filters), `COMMANDS.md`.

This file is the design and its decisions. Where the code differs from a section below, the decisions table and the steps file say so.

## Summary

- Move sandbox configuration and launching from targy + `nono-sandbox` into ateam.
- A `profile` becomes a set of saved properties that compose. Agent and container are optional.
- New entities referenced from profiles: `filter` (file access) and `network` (network access). Later: ports, Playwright, build containers, remote computers.
- New verbs: `chat` (attended agent), `run` (arbitrary command), `shell`, `profile save/show/list`.
- Sandbox backends are container types, nono first.
- Rules are written in a tool-agnostic way and mapped to the backend. Anything that cannot be enforced is an error.
- Order: filesystem and commands, then network, then secrets.

## Scope statement

ateam is automation around coding agents. Unattended quality workflows are one use; attended sessions are another.

What matters in this work:

- Sandboxing inside ateam itself: one boundary set from outside the agent, the same for every agent and every command, stronger than and independent of each agent's own sandbox.
- Saving exactly how to run an agent or a command for a project, inside or outside the project.
- Arbitrary commands in a sandbox: test a policy (`make test` under it), or open a secure shell to work in as an agent would.

Shared between attended and unattended runs:

- Profiles, filters, network rules, secrets.
- The metrics database: attended sessions record usage and cost too.
- Not logs: an attended agent already keeps its own session history.

Not in scope for now:

- Generating the agents' own settings (Claude permissions, managed settings). That stays in targy. ateam invests in outside sandboxes. An HCL syntax to configure an agent could come later.
- Project-level profile trust. Project profiles already exist in `ateam.hcl` and are not the focus.

Note: `exec` and `prompt` are probably misnamed given this direction. To revisit later.

## Background

Current state (targy `plans/feature_agent_nono.md`, built and tested on macOS, 2026-11-20):

- `~/dotfiles/agents/agents.targy`: the one source of rules. Read filter (`agent-read`), system filters copied from nono, groups (`go`, `node`, `dev`, `claude`, ...), `DOMAINS`, Claude tool and deny lists.
- targy steps `nono_profile`, `claude_permissions`, `json_merge`: about 700 lines of Python, plus `make test-nono` and `make test-claude-settings`.
- `~/dotfiles/bin/nono-sandbox`: parses flags, runs targy to write a nono profile and matching Claude settings, then execs `nono run`.
- Linux (meerdog, Landlock) is not run yet.

Why this pushes toward ateam:

- targy cannot run the command itself: it pipes output into its log, so the agent gets no terminal. ateam already execs agents.
- The saved-policy plan (targy `plans/feature_sandbox_saved_policy_file.md`) needs either new targy modes or a standalone launcher. Its open options already list "without targy" and "one Go binary".
- The nono plan lists "ateam starting its agents through nono-sandbox" as later work.
- ateam already has layered HCL config, profiles, `ateam secret`, dry-run, outer-sandbox detection, and a call database.
- autoversion's `filterrules` Go package parses the same filter syntax as targy.

Related research: `research/ResearchSandboxing.md`, sections F.1.2 (review-time hazards), F.3 (nono), G (network and secret injection).

## Decisions (2026-11-21)

| # | Topic | Decision |
|---|---|---|
| 1 | Composable unit | `profile`. No separate `sandbox`, `policy` or `capability` entity. Agent and container are optional. Complex things (filters, network, ports, Playwright, ...) are their own entities, referenced from profiles. Revisit if profile stops fitting. |
| 2 | Composition | Profiles compose with `use`. The default is to add. Any inconsistency, mistake, or rule that cannot be enforced is an error: fail closed. |
| 3 | List changes | First phase: plain lists add only. Second phase: `_remove` and `_replace` suffixes. |
| 4 | Verbs | `chat`, `run`, `shell`, `profile save/show/list`. `--profile` selects; no `--policy`, no `--resume`. |
| 5 | Saved command | A profile can hold a `command`. `run` uses it; `--command` replaces it for one launch. |
| 6 | `.ateam` access | Readable by default: `.ateam/runtime/EXEC_ID`, `.ateam/shared/`, `.ateam/prompts`. Everything else in `.ateam` is unreadable by default. Customizable per profile. |
| 7 | Project-supplied profiles | Trust on first use, second phase. Widening must stay possible. |
| 8 | Network | A `network` entity, tool-agnostic, mapped to the backend or an error. Default: deny, with LLM provider hosts and localhost allowed. |
| 9 | Filters | A `filter` entity in the unified syntax (autoversion). Defined by file or inline. Profile lists accept filter names and literal paths together. |
| 10 | Agent settings generation | Stays in targy for now. |
| 11 | Git hooks (2026-11-22, revised 2026-11-23) | `.git/config` and `.git/hooks` of the working folder are read-only in every sandboxed launch (raw Seatbelt rules in the nono profile). Attended launches also compare them after the run. |
| 12 | Backend default blocks (2026-11-22) | The profile's excludes decide alone. What nono blocks on its own and the profile does not exclude is lifted, as targy did. |
| 13 | Unattended runs (2026-11-22) | Same container and same rules as attended ones. Recording attended sessions in the metrics database: a task for later, after the sandbox work. |

| 14 | Read-only inside a writable folder (2026-11-23) | A `read` that reopens an excluded path inside a writable folder stays read-only; `write` opens it fully. The folders above a read-only path are pinned against rename. |
| 15 | ateam inside the sandbox (2026-11-23) | `ateam_files = "open"` (profile `agent/ateam`) opens ateam's own files. Every sandboxed command gets `ATEAM_IN_SANDBOX=1`; an ateam that sees it starts no second sandbox, and its runs share the outer one with the outer rules. |
| 16 | Supervised coding (2026-11-22/23) | Two supported setups: supervisor without nono and sub-runs with a nono profile; or supervisor under nono with `agent/ateam`, all in one sandbox. |
| 17 | Names (2026-11-21) | Built-in composable profiles carry a folder prefix (`sandbox/nono`, `agent/claude`, `dev/go`): a profile named `claude` would shadow the agent shorthand of unattended runs. No `--go` shorthand: `--profile a,b`. |
| 18 | `domains` (2026-11-21) | A plain profile list for now, so a sandboxed build can reach its registries. The `network` entity (decision 8) replaces it. |
| 20 | Embedded sandbox (2026-11-23) | fence is added as a second backend, used as a Go library so ateam carries its own sandbox. It also tests that the policy model is not shaped by nono. Built 2026-11-22 on macOS, not yet run for real. Result of the test: one nono shape in the model, a grant inside an excluded folder, which fence refuses (the steps file lists where it is used). |
| 19 | Default container (2026-11-21) | A profile without agent or container takes the `default` profile's. No new top-level setting. No wrapper phase. |

## Design

### Profiles

A profile is a set of saved properties. Every field is optional.

```hcl
profile "go" {
  write = ["go-write"]            # a filter name
  read  = ["go-read"]
  network = ["go-registry"]
}

profile "dev" {
  use = ["go", "node", "python"]
}

profile "nono" {
  agent     = "claude"
  container = "nono"
  read      = ["agent-read"]
  network   = ["base", "llm"]
}

# how to run this project, saved
profile "myproject" {
  use     = ["nono", "go"]
  write   = ["~/SyncDatabox/dotfiles"]    # a literal path
  read    = ["~/.ssh/config"]             # opens a path agent-read keeps unreadable; reported at launch
  exclude = ["~/SyncDatabox/nicmac/projects/other"]
}

profile "build" {
  use     = ["myproject"]
  command = ["make", "test"]
}
```

Fields:

| Field | Kind | Meaning |
|---|---|---|
| `use` | list | Profiles merged into this one, in order |
| `agent`, `container` | scalar | As today; now optional |
| `model`, `effort`, `max_budget_usd` | scalar | Pins, as today |
| `command` | scalar | Saved command for `run` |
| `read` | list | Filter names or paths readable, including ones an inherited `exclude` closes |
| `write` | list | Filter names or paths writable, besides the working folder |
| `exclude` | list | Filter names or paths unreadable |
| `network` | list | `network` entity names |
| `exposes`, `resources` | list | Later: what the profile exposes (ports) and uses (Playwright, containers, remote computers). Working names. |

In `read`, `write` and `exclude`, an entry starting with `~` or `/` is a path; anything else is a filter name. An unknown name is an error.

Composition:

- Lists add up across `use`, in order, then the profile's own entries.
- Scalars: the profile's own value wins over its `use` list; a later `use` entry wins over an earlier one.
- Cycles are an error.
- `ateam profile show` prints the resolved profile and where each entry came from.

Resolution at launch:

- No agent: fine for `run` and `shell`. `chat` falls back to the default agent.
- No container: fall back to a configured default sandbox backend (for example a top-level `default_container = "nono"`).
- If the resolved container cannot enforce every rule of the profile, the launch is refused. A policy is never dropped silently.
- `--profile` can be given several times or as a comma-separated list; they compose in order. `--read`, `--write`, `--exclude`, `--domain` add entries for one launch. (The `--go` shorthand was dropped: decision 17.)

Second phase, `_remove` and `_replace`:

- `write_remove = [...]` takes inherited entries out. Removing something that is not there is an error.
- `domains_replace`-style fields discard what was inherited. Used with the plain name in the same profile: an error.
- Security note: both can widen access. `_remove` on `exclude` reopens a path; `_replace` can drop an inherited restriction. They need the same trust rules as any other widening, and the launch should report them.

Existing pieces to reconcile:

- `sandbox_extra` in project config (`allow_write`, `allow_read`, `allow_domains`, `unsandboxed_commands`) overlaps with the new fields. Keep it as an alias that feeds the same lists, then deprecate.
- The agent's `sandbox` template (Claude's own sandbox settings) stays as it is for the existing built-in mode.
- Role and supervisor blocks already select profiles. Composition must not change their resolution order.

### Filters

```hcl
filter "agent-read" {
  file = "filters/agent-read.filter"
}

filter "go-write" {
  rules = <<-EOF
    version 1
    root ~
    /go/pkg
    /{Library/Caches,.cache}/go-build
  EOF
}
```

- The unified filter syntax, parsed with `github.com/voidexpr/autoversion/filterrules`.
- A `use NAME` line inside a filter resolves against the other `filter` blocks, through `filterrules`' caller-supplied resolver (implemented in autoversion, 2026-11-21):
  - `Resolver` interface (`Lookup`, `Names`); `ParseFileWith`, `File.UseWith`, `NewWith`.
  - Caller names shadow built-in groups; nested `use`; a cycle is `use-cycle` naming the chain; prefixed names (`dev/go`) work.
  - A used filter with `root`, `types` or `max-file-size` is refused, never dropped.
  - Resolution is lazy: ateam calls `NewWith` when it loads its config, so errors surface at load, named by filter.
  - `Rules.List()` returns the flattened rules with kind, text and the filter each came from: what the nono renderer and `profile show` need.
  - autoversion is public at `github.com/voidexpr/autoversion` (moved 2026-11-23, so CI, Docker builds and other users can fetch it). No tags: ateam pins a commit hash. `filterrules` imports `golang.org/x/sys`, which ateam already has as an indirect dependency.
- While this is being developed, the shared filters live in ateam: filter files embedded in `defaults/`, under folder prefixes (`dev/go`, `dev/node`, `agent/secrets`, `system/read-macos`; labels are examples). A filter change is then an ateam change only, with no autoversion release.
- Machine-specific rules (`/SyncDatabox/nicmac/.ssh`) stay in the user's own config, as `filter` blocks or files under `~/.ateam/`.
- Resolution order for a name: the profile's HCL `filter` blocks, then the user layer, then ateam's embedded defaults, then autoversion's built-ins.
- Cost of keeping them in ateam: targy keeps its own copy of the rules it needs for Claude's settings (`agent-read`, in `agents.targy`), so that one filter exists twice and can drift. The `dev/*` and `system/*` filters are only used for sandboxing, so they have one copy.
- To limit the drift: `ateam filter show NAME` prints a resolved filter in the shared syntax, which targy can read or a test can compare.
- Later, once the set is stable: move the shared filters to autoversion's built-ins. targy already copies those (`make groups-sync`). That needs prefixed built-in names and built-in groups that include.
- To check: sugar and list parity between targy's Python filters and `filterrules` (autoversion has conformance tests).

### Network

```hcl
network "base" {
  default = "deny"                        # or "allow"
  allow   = ["localhost"]
}

network "llm" {
  allow = ["api.anthropic.com", "claude.ai"]
}

network "webapp-dev" {
  use   = ["base", "llm"]
  allow = ["registry.npmjs.org", "localhost:3000"]
  deny  = ["telemetry.example.com"]
}
```

- Tool-agnostic. Each backend maps the rules to its own semantics, or returns an error. No confusion between fail open and fail closed.
- Default when a profile names no network: deny, with LLM provider hosts and localhost allowed.
- The LLM hosts are their own named set. A profile that routes through a proxy leaves it out, so direct connections are closed.
- Ports: `host:port`. A dynamically allocated port is edited into the profile by hand in the first version. A proper mechanism (`exposes`) comes later.
- Localhost needs three forms (found with fence, 2026-11-23): all of localhost, `localhost:PORT` for a server on the host, and a port the sandboxed command listens on. nono and fence on macOS only open localhost as a whole; fence on Linux only opens listed ports. Each backend maps what it can and refuses the rest. Details: steps file, "Next steps", 5.
- `default = "allow"` with `deny` rules is the open mode; a backend that cannot deny inside an open network returns an error.
- The single `DOMAINS` list of `agents.targy` is split per group (`go-registry`, `npm`, ...).
- Note: nono leaves the network open unless a domain list or a block flag is given. The nono backend must always pass one.

### Backends as container types

| Container type | Rules rendered as | State |
|---|---|---|
| `none` + agent sandbox | Claude sandbox settings, Codex flags | exists |
| `nono` | nono profile JSON; for Claude, settings with its own sandbox off | new, first |
| `fence`, embedded as a Go library | a fence configuration, run by `ateam fence-exec` | built on macOS, not yet run for real |
| `docker`, `docker-exec` | mounts from `read`/`write`; network needs a gateway (research G) | later |

nono backend, ported from targy:

- macOS: one grant of the home, `exclude` paths denied inside it. Linux: the home listed around unreadable paths.
- nono's required deny groups lifted for paths the profile keeps readable.
- Profile written beside its final path, validated by `nono profile validate`, then renamed.
- `suppress_save_prompt` set, so nono keeps no second source of rules.
- A verify mode for after a nono upgrade.

### Claude settings under a sandbox backend

ateam generates no Claude settings for this. What exists is enough:

- The agent's `inside_container` block already drops the sandbox settings file and adds `--dangerously-skip-permissions`. A nono container counts as "inside a container", so unattended runs need nothing new.
- For `chat`, the user wants their own interactive setup (hooks, plugins). targy generates one settings file per machine for "Claude under an outside sandbox": the base parts, Claude's sandbox off, the command deny list. ateam passes it through the agent's arguments (`--setting-sources project,local --settings FILE`), which HCL can already express.

What is dropped compared to targy's per-launch file:

- `Read` denies mirroring the unreadable paths: nono refuses those reads anyway. Only the error message differs.
- `Edit` allows for the writable paths: not needed when permissions are skipped or in auto mode.

To verify: that Claude's command deny rules (`Bash(sudo *)`, ...) still apply with `--dangerously-skip-permissions`; that `--settings` plus `--setting-sources project,local` behaves the same in `-p` mode.

### Standalone sandbox config

A profile can be exported as the backend's own config plus a command line, usable without ateam.

```bash
ateam profile export myproject --backend nono --out ~/sandboxes/myproject/
# writes: profile.json, run.sh (the nono command line), RULES.md (what each rule is for)
# prints: the command line that uses the exported profile, ready to paste
```

Uses:

- Troubleshooting, or working around an ateam bug.
- A backend feature ateam does not expose yet.
- Portability: run the project on a machine without ateam.
- Not trusting ateam: edit the exported profile, then run ateam itself inside it (`nono run --profile profile.json -- ateam chat ...`). ateam's outer-sandbox detection already makes it skip its own sandbox there.

Requirements:

- Self-contained: every rule resolved, no reference back to ateam's config.
- Portable paths where the backend allows (`$HOME`, `$WORKDIR` in a nono profile).
- ateam's implicit rules (protection of `.ateam`, `state.sqlite`, its config) are listed separately in `RULES.md`, so they can be relaxed or tightened by hand.
- The same code path as a real launch: `chat`/`run --dry-run` prints what `export` writes. No second renderer.
- The reverse: a profile can name a hand-edited backend config to use as is (`container_config = "path"`), and `container_extra_args` passes raw backend flags.

### Commands

```bash
# attended agent
ateam chat --profile myproject
ateam chat --profile myproject,dev/go --write ~/tmp/scratch -- --continue

# a command under a profile
ateam run --profile myproject -- make test      # no saved command: command after --
ateam run --profile build                        # saved command
ateam run --profile build -- -run TestFoo        # saved command, extra arguments appended
ateam run --profile build --command "make lint"  # replace the saved command for this launch

# a shell under a profile
ateam shell --profile myproject

# profiles
ateam profile save myproject --profile sandbox/nono,dev/go --write ~/SyncDatabox/dotfiles
ateam profile show myproject
ateam profile list
ateam profile export myproject --out DIR
```

- `chat`: launches the agent, from the profile or else the default agent. Ignores `command`. Arguments after `--` go to the agent. Adds the agent-specific wiring: its settings, its inner sandbox off, usage and cost recorded in the metrics database.
- `run`: runs the profile's `command` with arguments after `--` appended, or the command given after `--` when the profile has none. Neither: an error.
- `shell`: the user's shell under the profile. Ignores `command` and `agent`.
- `profile save NAME`: writes a profile from the flags and the profiles they reference. Launches nothing, so a quick test launch never replaces a reviewed profile.
- `profile show NAME`: the resolved profile, where each entry came from, and the backend command.
- `--profile` takes a name or a file path.
- `ateam claude` (interactive Claude inside a container) becomes a special case of `chat`; fold it in later.
- Unattended runs (`exec`, `report`, `code`) take the same profiles through `--profile`, as today.

### Where profiles live, and protection of ateam's files

- `ateam profile save` writes to the user layer by default (`~/.ateam/profiles/NAME.hcl`). A flag writes to the project's `.ateam/` instead.
- A hand-written file anywhere works with `--profile PATH`.
- Every launch protects ateam's own files. This is implicit:
  - No write to the user ateam directory, the project's `.ateam/` config, or a profile file given by path.
  - In the project's `.ateam/`, only `runtime/EXEC_ID`, `shared/` and `prompts` are readable; `runtime/EXEC_ID` and `shared/` are writable. Agents use `shared/` to read each other's work.
  - A profile can open more (a debug agent gets full read).
- Checked against the default prompts (2026-11-21). Agents themselves touch only these under `.ateam/`:
  - `runtime/<exec_id>/`: write (their output).
  - `shared/`: read (`shared/report/<ROLE>.md`, `shared/review.md`) and write (the code-management supervisor updates `shared/review.md` and the role reports; `auto_setup` writes `shared/auto_setup.md`).
  - `cache/`: the `project.discover_cmd` role writes `cache/test_quick.sh` and `cache/tests_all.sh`, which later runs execute. So `cache/` is readable, and writable for that role.
  - Prompts are assembled by ateam on the host and passed to the agent; agents do not read `.ateam/prompts` themselves. Keeping it readable is harmless.
  - `state.sqlite`, `logs/`, `ateam.hcl`, `secrets.env`: never needed by an agent. Unreadable by default.
- `DEV.md` (runner contract) still describes `roles/<role>/` and `supervisor/` as promotion targets, while `CONFIG.md` and the prompts use `shared/`. One of the two is stale; to settle before the protection rules are coded.
- Every backend also protects `.git/hooks`, `.git/config` and agent settings inside writable folders, as Porta does (research E.13.13, F.1.2).
- Second phase: trust on first use for project-supplied profiles. A profile shipped in a repository runs only after the user approves it, and again when its content changes; the approved hash is kept in the user layer.

## Phases

| Phase | Content | State (2026-11-23) |
|---|---|---|
| 0 | A wrapper container type around `nono-sandbox` | Dropped |
| 1 | Composable profiles; `filter` entity; nono backend; `chat`, `run`, `shell`, `profile save/show/list/export`; protection of ateam's files; unattended runs under nono; ateam inside the sandbox | Built on macOS. Left: Linux, built-in profiles as filter files, retiring `nono-sandbox` |
| 2 | `network` entity mapped to nono's proxy, with ports. `_remove` and `_replace`. Trust on first use for project-supplied profiles | Not started. `domains` stands in for the network entity |
| 3 | Secrets: `ateam secret` values as placeholders, injected outside the sandbox (research G.4, F.1.1 Tier A first) | Not started |
| 4 | `exposes` and `resources`: ports, Playwright, build containers, remote computers. Other backends for comparison: Enclave, OpenShell, Strands Box | Not started |
| — | fence as an embedded backend (Go library, no external binary on macOS): a second consumer of the policy model | Built on macOS, not yet run for real. Not a path to Linux as it stands: there fence needs `bwrap` and `socat`, and opens localhost one port at a time. See the steps file |

Later, outside the phases: attended sessions in the metrics database; `chat`, `run`, `shell` on Docker containers.

## What stays in targy and dotfiles

- targy keeps its targets, backups and filters.
- targy keeps generating the agents' own settings (plain `claude`, managed settings). It can configure agent-level policies.
- Its `nono_profile` step is removed after phase 1. `claude_permissions` and `json_merge` stay.
- Both tools read the same filter syntax. The shared filters live in ateam for now; targy keeps its own `agent-read` until they move to autoversion.

## Pros and cons

Moving to ateam:

| Pros | Cons |
|---|---|
| One Go binary; no Python or `uv` at launch; a project resumes on any machine | Port of the nono rendering and the home listing walk, plus its test suite |
| The terminal problem goes away: ateam execs the agent | Linux behavior is not settled yet |
| Attended and unattended runs share profiles, secrets and metrics | `filterrules` needs a named-filter resolver; parity to check |
| Layered HCL, profiles, secrets, dry-run, call database already exist | Work before v1 that is not release polish |
| Network and secret phases have a natural home | Agent settings stay in targy: two tools read the same filters |

Staying with targy + `nono-sandbox`:

| Pros | Cons |
|---|---|
| Built, committed, tested on macOS | Three moving parts per launch: launcher, targy, nono |
| Filter semantics have one owner | Saved policy needs targy changes anyway (working folder, launcher mode) |
| ateam stays on its release work | ateam's agents stay outside the nono boundary |

## Open questions

1. Claude under `--dangerously-skip-permissions`: do command deny rules still apply (see "Claude settings under a sandbox backend"). The owner has the command to run; not run yet.
2. When to move the shared filters to autoversion, once the built-in profiles are filter files.
3. Linux: the home listing around excluded paths (ported from targy), and a mechanism for read-only paths inside a writable folder, which on macOS relies on Seatbelt.
4. `unsafe_macos_seatbelt_rules` is not in nono's profile guide. Ask nono's authors whether it is stable, or whether a read-only rule is planned.
5. A profile per run for unattended commands (a worktree per role): today every run of a command shares one working folder and one nono profile.
6. Names for `exposes` and `resources`, and the shape of port and Playwright blocks. A dynamic port: how a profile learns the port that was chosen.
7. Whether `run` and `chat` should also accept a `--save NAME` shortcut. Current answer: no, `profile save` then launch.
8. Renaming `exec` and `prompt` to fit the wider scope.
10. fence: a grant inside an excluded folder cannot be expressed (`dev/go`, unattended runs without a project). Keep it refused, split the exclude around the grant at launch, or ask for it upstream. See the steps file.
9. `DEV.md` describes `roles/` and `supervisor/` as promotion targets while `CONFIG.md` and the prompts use `shared/`: which is current.
