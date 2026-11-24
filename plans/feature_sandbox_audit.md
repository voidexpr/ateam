# Sandbox audit command: `ateam probe`

Status, 2026-11-24: spec, not built. Written for the agent that builds it.

Context to read first: `ISOLATION.md` (mode ⑤), `SANDBOX_DIFFERENCES.md`, `plans/feature_sandboxing_and_attended_agents_impl_steps.md` (sections "Sandbox probes", "fence on Linux", "Network rules"), `test/sandbox/run-probes.sh` and `sandbox-probe.sh` (the shell probes this command grows out of).

## Goal

One command that says what the current environment can read, write, replace and reach. It answers three questions:

1. **What is open here?** Run it where you are: a plain terminal, a nono or fence sandbox started by hand, Claude's own sandbox, a Docker container. No ateam sandbox is started.
2. **Does an ateam profile do what it says?** Run it under a profile: ateam starts the sandbox, runs the same checks inside, and compares each result with what the profile's policy expects.
3. **How do two environments differ?** The same checks with a stable output, to compare macOS and Linux, nono and fence, or a standalone sandbox and ateam's.

Not a goal: enforcing anything, fixing anything, or replacing the backends' own diagnostics.

## Name

`ateam probe`. Reasons: the repository already calls these "probes" (`test/sandbox`), it is short, and `audit` is taken by ateam's code audits (roles, reports). Alternatives considered: `ateam permissions-check`, `ateam sandbox-audit`. The owner decides; the spec uses `probe`.

## Command

```bash
ateam probe [PATH]... [flags]

# where am I, what is open?
ateam probe
ateam probe ~/.ssh ~/projects/other ./secrets.env

# does this profile hold? (starts the sandbox, checks inside)
ateam probe --profile sandbox/fence,dev/go
ateam probe --profile myproject --write ~/tmp/scratch --domain example.com

# I am already inside some sandbox: does it give what dev/go needs?
ateam probe --here --profile dev/go

# machine-readable, to compare two environments
ateam probe --json > mac-nono.json
```

Flags:

| Flag | Meaning |
|---|---|
| `PATH`... | Extra paths to check: blocked, readable, writable. |
| `--profile`, `--read`, `--write`, `--exclude`, `--domain`, `--deny-domain`, `--listen` | The launch flags of `chat`, `run`, `shell` (`addLaunchFlags`). They define the policy the results are compared with, and, unless `--here`, the sandbox that is started. |
| `--here` | Never start a sandbox: check the current environment. Implied when no profile is given, and when ateam is already inside a sandbox or a container (`sandboxType` returns `inside`). |
| `--no-defaults` | Skip the built-in checks. Only the paths, programs, hosts and ports given on the command line, and the policy's own rules, are checked. |
| `--program NAME` | Extra program to check (repeatable). |
| `--connect HOST[:PORT]`, `--url URL` | Extra destination to reach (repeatable). |
| `--listen-check [ADDR:]PORT` | Extra port to try to listen on (repeatable). Separate from `--listen`, which is a policy rule. |
| `--json` | One JSON document on stdout instead of the table. |
| `--quiet` | Only the lines with a verdict other than `ok`. |
| `--dry-run` | Print the checks, what is expected of each, and the command line that would start the sandbox. Check nothing. |

Exit code: 0 when every verdict is `ok`; 1 when at least one is `RISK` or `DIFFERS`; 2 for a usage or launch error.

## Modes

**In place** (`--here`, or implied). The process runs the checks itself and prints the results. It must work with no readable ateam configuration: inside a foreign sandbox `~/.ateam` is often closed. So:

- Without `--profile`: load nothing. The built-in checks come from the binary (the embedded `agent/secrets` filter included).
- With `--profile`: load the configuration as usual. When a layer cannot be read, say which one on stderr and continue with the layers that could be read. Do not fail: an audit tool that refuses to run inside the sandbox it audits is useless.

**Under a profile.** The outer ateam:

1. Composes the profile and builds the policy with the existing code (`composeLaunch`, `buildSandboxPolicy`). Do not write a second policy path.
2. Builds the check list: built-in checks, command-line checks, and the checks derived from the policy (below), each with its expected outcome.
3. Records, from outside, what it can see of each file target: exists, is a folder, is empty, is a link and where it leads. These "before" facts travel with the check list.
4. Starts itself inside the sandbox through the same path as `ateam run` (`planLaunch`, `wrapped`, `runGuarded`), as a hidden mode: `ateam probe --run-checks JSON`. The inner process loads no configuration, runs the checks, compares and prints. Pass the list as an argument, not through a file (a file may be unreadable inside) nor stdin (the terminal is attached).
5. Returns the inner exit code.

The inner process needs the ateam binary to be readable and executable inside the sandbox. When it is not (a binary under `/tmp` with fence on Linux, where `/tmp` is private), fail before the launch and say so.

## Checks

Three kinds, one function each, shared by every built-in and user check. No per-target code.

### Files

| Outcome | Meaning |
|---|---|
| `not_found` | Nothing there, and it cannot be created. |
| `can_create` | Nothing there, and the folder above accepts a new entry. |
| `blocked` | It exists (or the sandbox hides that it does) and cannot be read. |
| `can_read` | Readable, not writable. |
| `can_write` | Writable (readable or not; say so in the detail). |

`can_create` is not in the owner's first list. It is needed: under fence on Linux a protected path that does not exist yet can be created, which is how a command would plant `.ateam/profiles/x.hcl`. `not_found` alone would hide that.

How to test, without changing anything of the owner's:

- **Read, file:** open and read one byte, discard it. Never print or keep content.
- **Read, folder:** list it. With "before" facts: a folder that was not empty outside and lists empty inside is `blocked` (fence on Linux shows an excluded folder as an empty one). The same for a file that had a size and reads empty.
- **Write, existing file:** open for writing **without truncating and without writing a byte** (`O_WRONLY|O_APPEND`), then close. Never `O_TRUNC`, never `O_CREATE` on an existing path.
- **Write, folder, and create:** create one empty file named `.ateam-probe-<pid>-<random>` in it, then remove it. If the removal fails, report it on stderr with the path. This is the only thing the command ever creates.
- **Links:** check where the path really leads, and report the link and its target separately when they differ. Lesson from the shell probes: plain `ls` of a link whose target is closed succeeds and prints the link's name.

Known limit, to state in the output and in the docs: on Linux a write into an excluded folder that lies inside a writable one succeeds in a throwaway copy. From inside this looks like `can_write`. Phase 2 below closes it.

### Programs

| Outcome | Meaning |
|---|---|
| `not_found` | Not in `PATH`. |
| `ok` | Found, and neither the file nor its folder is writable. |
| `can_replace` | The file is writable, or its folder accepts a new entry (rename over it). |

A program check is two file checks (the resolved file, its folder) plus the `PATH` lookup. Also check each folder of `PATH` in order: a writable folder earlier in `PATH` than a program lets a command shadow it. Report that as `can_replace` with the folder in the detail.

### Network

| Outcome | Meaning |
|---|---|
| `blocked` | Refused, or no answer within the timeout. |
| `can_connect` | A connection was made. |
| `can_listen` | A listening socket could be opened. |

How to test:

- **Listen:** open a TCP listener on the address and close it. Port 0 for the built-in checks.
- **Connect to a port:** dial it.
- **Reach a URL or host:** two separate checks, because sandboxes filter with a proxy: once honouring the proxy variables (`HTTPS_PROXY`, `ALL_PROXY`), once dialling directly. A TLS handshake or an HTTP `HEAD` is enough; send no payload.
- **Own loopback:** listen on `127.0.0.1:0`, then connect to that port.
- Timeout 5 seconds per check, run in parallel.

Not checkable from inside: whether a port the command listens on is reachable from outside the sandbox. Phase 2.

## Built-in checks

On unless `--no-defaults`. Each has an id, a one-line reason, and the outcomes that are safe. Anything else is a `RISK`.

| Group | Targets | Safe |
|---|---|---|
| Shell startup files | From `$SHELL`: bash `~/.bashrc`, `~/.bash_profile`, `~/.profile`; zsh `~/.zshrc`, `~/.zprofile`, `~/.zshenv`; fish `~/.config/fish/config.fish` | Reading is reported, not judged. `can_write` and `can_create` are a risk: code that runs in every later shell. |
| Secrets | Every path of the embedded `agent/secrets` filter (`sandboxpolicy.FilterSet.Paths`). One list, no copy. | `blocked`, `not_found`. `can_read` is a risk (leak), `can_write` and `can_create` too (tampering). |
| Agent configuration | Claude: `$CLAUDE_CONFIG_DIR` when set, else `~/.claude` and `~/.claude.json`; in it `settings.json`. Codex: `$CODEX_HOME` when set, else `~/.codex` (`config.toml`, `auth.json`). In the working folder: `.claude/`, `.mcp.json`. | Reading is reported. Writing is a risk unless the policy grants it (an agent writes its own files: `agent/claude`). |
| ateam's own files | The user directory (`ATEAM_HOME`, else `~/.ateam`): `ateam.hcl`, `secrets.env`, `profiles/`. The project's `.ateam/`: `ateam.hcl`, `secrets.env`, `profiles/`, `state.sqlite`. | `blocked`, `not_found`. `can_create` on `profiles/` is the risk the Linux gap opens. |
| Git | `.git/config`, `.git/hooks/` of the working folder; `~/.gitconfig` | `can_read`. Writing is a risk: git runs them outside the sandbox later. |
| Where am I | The working folder, the home, `$TMPDIR`, `/tmp` | Reported, not judged, except the home: `can_write` on the home itself is a risk. |
| Programs | ateam itself (`os.Executable`), `$SHELL`, `sh`, `bash`, `zsh`, `git`, `ssh`, `sudo`, `curl`, `python3`, `node`, `go`, `make`, `claude`, `codex`, `nono`, `fence`; then every folder of `PATH` | `ok`, `not_found`. `can_replace` is a risk. On a developer's machine ateam itself is usually replaceable: expected, still flagged. |
| Network | Listen on `127.0.0.1:0` and on `0.0.0.0:0`; own loopback; `https://example.com` through the proxy and directly; a direct dial to `1.1.1.1:443`; a DNS lookup | Reported. Without a policy nothing is judged: an open network is normal outside a sandbox. |
| Environment | Not a check, a header: the system, `container.IsolationSource()`, `ATEAM_IN_SANDBOX`, `FENCE_SANDBOX`, which proxy variables are set, `TMPDIR` | |

## Checks from a policy

With a profile, every rule of the policy becomes a check with an expected outcome. This is what "dev/go-style rules" means: `ateam probe --here --profile dev/go` asks whether the current environment gives what `dev/go` needs.

| Policy | Check | Expected |
|---|---|---|
| The working folder, each `Write` rule | file | `can_write` (`can_create` when missing) |
| Each `Read` rule | file | `can_read`, or `can_write` when also writable |
| Each `Exclude` rule | file | `blocked`, `not_found` |
| Each `ReadOnly` rule | file | `can_read` |
| Each host of `Domains` | network, through the proxy | `can_connect` |
| One host that is not listed (`example.org`) | network, both ways | `blocked` |
| Each `DeniedDomains` host | network | `blocked` |
| `HostPorts`, `HostLocalhost` | connect | reported only: nothing may be listening there |
| Each `Listen` port | listen | `can_listen` |
| No network rule at all | every network check | `blocked` |

A built-in check whose target a policy rule covers takes the policy's expectation instead of the built-in "safe" set. Example: `~/.claude` is writable under `agent/claude`, and that is `ok`, not a `RISK`.

Skip wildcard hosts (`*.github.com`): there is nothing to dial.

## Verdicts and output

Each line has an outcome and a verdict:

- `ok`: as expected, or nothing is expected.
- `RISK`: a built-in check outside its safe set, with no policy rule that allows it.
- `DIFFERS`: a policy expectation that does not hold. Say what was expected.

Table, grouped as above, one line per check:

```
environment  darwin, sandbox: env:ATEAM_IN_SANDBOX, proxy: HTTPS_PROXY ALL_PROXY

secrets
  blocked      ~/.ssh                          ok
  can_read     ~/.npmrc                        RISK   a token may be in it
  not_found    ~/.aws                          ok
ateam
  can_create   ./.ateam/profiles               DIFFERS  the profile excludes it
programs
  can_replace  ateam  ~/projects/ateam/ateam   RISK   the folder is writable
network
  can_connect  https://example.com (proxy)     ok
  blocked      1.1.1.1:443 (direct)            ok

3 to look at: 2 RISK, 1 DIFFERS
```

`--json`: `{"environment": {...}, "checks": [{"id", "group", "kind", "target", "outcome", "expected", "verdict", "detail"}]}`. Ids are stable across systems (`secrets:~/.ssh`, with `~` kept), so two files can be compared with any diff tool. Paths are printed with `~` for the home (`display.TildeHome`).

## Safety rules

These are requirements, each with a test.

- Never truncate, never write a byte to an existing file, never rename or remove anything but the command's own marker files.
- Marker files are empty, named `.ateam-probe-<pid>-<random>`, and removed at once. A removal that fails is reported with the path.
- Never print, log or keep file content. One byte is read and discarded.
- No request beyond a handshake or a `HEAD`. No credentials are sent: build the HTTP client without the user's cookies or netrc.
- Listeners are closed at once.
- Never follow a check with a "fix".

An earlier probe appended a line to `.ateam/ateam.hcl` by accident. The tests must prove the write check leaves size and modification time unchanged.

## Code layout

- `internal/sandboxprobe/`: no cobra, no configuration loading.
  - `Check{ID, Group, Kind, Target, Why, Safe []Outcome, Expect []Outcome, Before *Facts}` and `Result{Check, Outcome, Verdict, Detail}`.
  - `CheckFile`, `CheckProgram`, `CheckNet`: the three functions. `CheckProgram` calls `CheckFile`.
  - `Defaults(env Env) []Check`: the built-in list, from a small struct (home, working folder, `$SHELL`, `PATH`, the config variables), so tests can build it for a scratch home.
  - `FromPolicy(*sandboxpolicy.Policy) []Check`.
  - `Run(checks) []Result`, `Table(w, results)`, JSON encoding.
- `cmd/probe.go`: flags, mode choice, the launch, the hidden `--run-checks`.
- Reuse, do not copy: `addLaunchFlags`, `composeLaunch`, `buildSandboxPolicy`, `planLaunch`, `sandboxType`, `runGuarded`, `shellJoin`, `sandboxpolicy.RealPath`, `sandboxpolicy.Within`, `container.IsolationSource`, `display.TildeHome`.
- `planLaunch` refuses a profile with rules on a container that enforces none. `--here --profile dev/go` must not hit that: it needs the policy, not a launch. Build the policy without going through that refusal.

Rules of the codebase that apply: no duplicated code, comments only where not obvious, the style of `internal/sandboxpolicy`. Ask the owner before adding anything this spec does not list.

## Tests

- Unit tests in `internal/sandboxprobe`, in a scratch home (`t.TempDir()`), using file permissions to produce each outcome: all five file outcomes, a link to a closed target, a hidden folder through "before" facts, the three program outcomes including a writable folder earlier in `PATH`, listen and connect on local ports. No test may need the network.
- A test that the write check changes neither size nor modification time, and that no marker file is left.
- `cmd` tests in the style of `cmd/sandbox_launch_test.go`: the check list of a profile, the expectations, `--dry-run`, the verdict when a policy rule covers a built-in check.
- `make test`, `make run-ci`. `make test-docker` is not needed: nothing under `internal/container/` or `internal/runner/` changes.
- Real runs, by the owner from a plain terminal (a sandbox does not start inside an agent's sandbox):
  - `ateam probe` in a plain terminal: mostly `RISK`, which is right.
  - `ateam probe --profile sandbox/nono` and `sandbox/fence` on macOS: no `DIFFERS`.
  - `ateam probe --profile sandbox/fence` on Linux: one `DIFFERS`, `can_create` on `.ateam/profiles`, the known gap.
  - Inside a hand-started nono or fence sandbox: `ateam probe`.
  - The Linux binary in the Debian container used for the fence probes (see the steps file), which the agent can run itself through Docker.

## Docs to update

`COMMANDS.md` (the command), `ISOLATION.md` ("Seeing what a profile does": add the command), `README.md` (one line), `CLAUDE.md` ("How to test"), `SANDBOX_DIFFERENCES.md` (how its cells can now be reproduced), the steps file.

## Phase 2, not in the first version

- **Checks from outside after the run.** The outer ateam verifies what only it can see: that a marker created inside exists outside (else the write was thrown away: outcome `discarded`), and that a port the command listens on answers from outside. This replaces `verify` in `test/sandbox/run-probes.sh`.
- **`make test-sandbox` on top of this command**, retiring `sandbox-probe.sh`.
- **`ateam probe diff A.json B.json`**, a readable comparison.
- **More kinds:** process visibility, Unix sockets (`/var/run/docker.sock`), environment variables that carry tokens.

## Open questions for the owner

1. The name: `probe`, or another.
2. Whether `RISK` on ateam's own binary being replaceable should be muted by a flag while developing ateam.
3. The built-in program list: which programs matter to you beyond the ones listed.
4. Whether the secrets list should be `agent/secrets` only, or also what the profile in use excludes (the spec says: both, the second through the policy).
