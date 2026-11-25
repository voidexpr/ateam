# Environment audit command: `ateam probe`

Status, 2026-11-24: built on the branch `worktree-ateam-probe`, all seven steps. Unit tests pass. Real runs done by the agent: in place inside Claude's sandbox on macOS, the three probe scripts there, and the Linux binary as root in a Debian container. Not run yet, by the owner from a plain terminal (`make test-probe`): `--start-sandbox` under nono and fence, a plain terminal, `playwright-launch` where a browser can start, Linux outside a container.

## Where the build differs from the spec below

- **`create` is tested at the path itself** (decided by the owner, 2026-11-24, after the first real runs). An empty file is created at the missing path with `O_CREATE|O_EXCL` and removed at once. A marker in the folder above does not see a sandbox that refuses by name: it reported `.ateam/profiles` as creatable under nono and fence on macOS, where it is not. This replaces "the only thing the command ever creates is its marker file" below. Exceptions: a path whose folder is missing too is tested through the nearest folder that exists; a file a running program would trust (`-wal`, `-shm`, `-journal`, `.lock`, `.pid`) is not tried and shows as `absent`, with a detail that says so.
- **Network**: a blocked connection says why. An answer whose certificate is not trusted is asked again without checking it, and counts as a connection: a proxy that intercepts TLS accepts the tunnel of a host it refuses.
- **Two more groups**: `profile` (the checks made from a profile's rules) and `paths` (the paths given on the command line).
- **The readable home is not a profile rule.** Only rules a profile states replace a built-in judgment. Otherwise `--profile dev/go`, which does not exclude `~/.ssh`, made a readable `~/.ssh` expected.
- **Folded rows.** The ok rows of quiet checks (programs present, their files, the folders of `PATH`) are one line per group. More than three `RISK create` rows in a group (paths with nothing there, under a writable folder) are one line. `--all` lists each. An ok row with `create` is left out like one with `absent`.
- **A variable that is not set** gives an `absent` row, left out by default, instead of no row: the list is built outside a sandbox and checked inside.
- **Variable names**: `AWS_*` is not a pattern. `AWS_SECRET_ACCESS_KEY` and the like match `SECRET`, `KEY`, `TOKEN`; `AWS_REGION` is no secret. Added: `PASSWD`, `CREDENTIAL`, `DATABASE_URL`, `KUBECONFIG`, `NETRC`.
- **Secret file names** in a working folder skip source and text files (`token.go`, `.env.example`) and public keys.
- **`docker-build`** builds `FROM scratch` with one empty file: nothing is pulled, so the answer does not depend on a registry. It keeps the builder's state in its scratch folder, since `~/.docker` may be closed.
- **`docker-build-expand-bind`** proves the mount with a regular file known on the host. A daemon in a VM that does not share the folder mounts an empty folder of its own, which a count of entries took for the host's.
- **Scripts** get `ATEAM_PROBE_WORKDIR` and `ATEAM_PROBE_HOME`; their own working folder is a scratch folder.
- **Mounts** are listed only in place: inside a sandbox ateam starts they are the sandbox's own.
- **Listening, the own loopback and DNS are not judged under a profile**: backends differ on them without a rule saying so.
- **`--start-sandbox` inside a sandbox**: refused when ateam's detection sees one (`ATEAM_IN_SANDBOX`, a container marker). Detection of other sandboxes is off by default in ateam, so there the launch is tried, after a warning.
- **The header's isolation** uses the sandbox detection even when it is off for launches. On macOS that runs `sandbox-exec ... /usr/bin/true` once.
- **The encoded check list** is gzip and base64: one argument is limited to 128 KiB on Linux.
- **Not built**: the `discarded` outcome and the checks from outside (phase 2, as planned).

Context to read first: `ISOLATION.md` (mode ⑤), `SANDBOX_DIFFERENCES.md`, `plans/feature_sandboxing_and_attended_agents_impl_steps.md` (sections "Sandbox probes", "fence on Linux", "Network rules"), `test/sandbox/run-probes.sh` and `sandbox-probe.sh` (the shell probes this command grows out of).

## Goal

One command that says what the environment it runs in can read, write, replace, reach and do. It is generic: the environment may be a plain terminal, a sandbox ateam started, a nono or fence sandbox started by hand, an agent's own sandbox, a Docker container, a remote VM. It answers:

1. **What is open here, and what is lying around?** Run it where you are. Nothing is started. This is the main use.
2. **Does this environment give what a profile asks for?** The same run, compared with a profile's policy.
3. **Does an ateam profile do what it says?** ateam starts the sandbox, runs the same checks inside, compares.
4. **How do two environments differ?** The same checks with a stable output.

A remote host needs nothing special: copy the binary there (`build/ateam-linux-<arch>`) and run it.

Not a goal: enforcing anything, fixing anything, or replacing the backends' own diagnostics.

## Decisions taken in the review

| Subject | Decision |
|---|---|
| Name | `ateam probe`. One check is "a probe". |
| What starts something | Nothing by default. A sandbox only with `--start-sandbox`. An active check only with `--with ID`. The first output line always says what was started. |
| `--here` | Removed. `--profile` alone only compares. |
| No isolation detected | Said in the header, then the full report. `RISK` outside a sandbox is not noise. |
| Scope | Fixed lists plus bounded discovery by name. Never by content. |
| Types | `file`, `network`, `capability`, `env`. |
| Verdicts | `ok`, `RISK`, `UNMET`. `UNMET` replaces `DIFFERS` and covers one direction only (below). |
| Selecting checks | `--only`, `--skip`, `--with`, each taking group names and check ids. `--no-defaults` is removed. |
| Where it is built | Go in ateam for files, network, discovery, report, launch. Active capability checks are small scripts embedded in the binary. |
| Docker | No container or image is started by the built-in passive check. When it cannot tell whether the daemon is limited, it says so and the row is a `RISK`. Active docker probes are scripts, off by default. |
| playwright-cli | Presence by default. A launch when asked (`--with playwright-launch`). |
| ateam's own binary replaceable | A `RISK` like any other. No dedicated flag: `--skip programs:ateam`. |
| sudo | Presence and replaceability only. No test of password-less sudo. |
| Secrets list | Three sources: the embedded `agent/secrets` filter, discovery, the profile's excludes. |
| macOS privacy dialogs | Not handled. Documented (below). |

## Command

```bash
ateam probe [PATH]... [flags]

# where am I, what is open? (starts nothing)
ateam probe
ateam probe ~/.ssh ~/projects/other ./secrets.env
ateam probe --only secrets,git
ateam probe --skip network

# does this environment give what dev/go needs? (starts nothing)
ateam probe --profile dev/go

# does this profile hold? (starts the sandbox, checks inside)
ateam probe --start-sandbox --profile sandbox/fence,dev/go
ateam probe --start-sandbox --profile myproject --write ~/tmp/scratch --domain example.com

# active checks, each named
ateam probe --with playwright-launch,docker-build

# what exists, what is on, what each one starts
ateam probe --list

# machine-readable, to compare two environments
ateam probe --json > mac-nono.json
```

Flags:

| Flag | Meaning |
|---|---|
| `PATH`... | Extra paths to check. |
| `--profile`, `--read`, `--write`, `--exclude`, `--domain`, `--deny-domain`, `--listen` | The launch flags of `chat`, `run`, `shell` (`addLaunchFlags`). They define the policy the results are compared with. They start nothing on their own. |
| `--start-sandbox` | Start the sandbox the launch flags describe and run the checks inside. Needs a profile or a launch flag. Refused when ateam is already inside a sandbox or a container (`sandboxType` returns `inside`): a sandbox does not nest. |
| `--only LIST` | Run only these groups or check ids (comma-separated, repeatable). |
| `--skip LIST` | Leave out these groups or check ids. Counted on the last line as `skipped`. |
| `--with LIST` | Enable active checks, which are off by default. |
| `--list` | Print every group and check: id, on or off, and for an active check what it starts. Check nothing. |
| `--program NAME` | Extra program to check (repeatable). |
| `--connect HOST[:PORT]`, `--url URL` | Extra destination to reach (repeatable). |
| `--listen-check [ADDR:]PORT` | Extra port to try to listen on (repeatable). Separate from `--listen`, which is a policy rule. |
| `--all` | Also show the `ok` rows whose permission is `absent`, hidden by default. |
| `--quiet` | Only the rows with a verdict other than `ok`. |
| `--json` | One JSON document on stdout instead of the table. Always complete: `--all` and `--quiet` do not apply. |
| `--dry-run` | Print the checks, what is expected of each, and with `--start-sandbox` the command line that would start the sandbox. Check nothing. |

An unknown group or id in `--only`, `--skip` or `--with` is a usage error that names the valid ones.

Exit code: 0 when every verdict is `ok`; 1 when at least one is `RISK` or `UNMET`; 2 for a usage or launch error.

## Modes

**In place** (default). The process runs the checks itself and prints the results. It must work with no readable ateam configuration: inside a foreign sandbox `~/.ateam` is often closed. So:

- Without `--profile`: load nothing. The built-in checks, the embedded `agent/secrets` filter and the probe scripts all come from the binary.
- With `--profile`: load the configuration as usual. When a layer cannot be read, say which one on stderr and continue with the layers that could be read. Do not fail: an audit tool that refuses to run inside the sandbox it audits is useless.

**Inside a sandbox it starts** (`--start-sandbox`). The outer ateam:

1. Composes the profile and builds the policy with the existing code (`composeLaunch`, `buildSandboxPolicy`). Do not write a second policy path.
2. Builds the check list: built-in checks, discovered checks, command-line checks, and the checks derived from the policy (below), each with its expected outcome.
3. Records, from outside, what it can see of each file target: exists, is a folder, is empty, is a link and where it leads. These "before" facts travel with the check list.
4. Starts itself inside the sandbox through the same path as `ateam run` (`planLaunch`, `wrapped`, `runGuarded`), as a hidden mode: `ateam probe --run-checks JSON`. The inner process loads no configuration, runs the checks, compares and prints. Pass the list as an argument, not through a file (a file may be unreadable inside) nor stdin (the terminal is attached).
5. Returns the inner exit code.

Discovery runs outside, in step 2, so that what the sandbox hides still gets a row. The inner process does not discover again.

The inner process needs the ateam binary to be readable and executable inside the sandbox. When it is not (a binary under `/tmp` with fence on Linux, where `/tmp` is private), fail before the launch and say so.

## Checks

Four types. `file`, `network` and `env` have one Go function each, shared by every built-in, discovered and user check: no per-target code. `capability` is per-target by nature: passive ones in Go, active ones as scripts.

### Files

| Permission | Meaning |
|---|---|
| `absent` | Nothing there, and it cannot be created. |
| `create` | Nothing there, and the folder above accepts a new entry. |
| `blocked` | It exists (or the sandbox hides that it does) and cannot be read. |
| `read` | Readable, not writable. |
| `write` | Writable (readable or not; say so in the detail). |
| `connect` | A Unix socket that accepts a connection. |

`create` is needed: under fence on Linux a protected path that does not exist yet can be created, which is how a command would plant `.ateam/profiles/x.hcl`. `absent` alone would hide that.

How to test, without changing anything of the owner's:

- **Read, file:** open and read one byte, discard it. Never print or keep content.
- **Read, folder:** list it. With "before" facts: a folder that was not empty outside and lists empty inside is `blocked` (fence on Linux shows an excluded folder as an empty one). The same for a file that had a size and reads empty.
- **Write, existing file:** open for writing **without truncating and without writing a byte** (`O_WRONLY|O_APPEND`), then close. Never `O_TRUNC`, never `O_CREATE` on an existing path.
- **Write, folder, and create:** create one empty file named `.ateam-probe-<pid>-<random>` in it, then remove it. If the removal fails, report it on stderr with the path. This is the only thing the Go checks ever create.
- **Links:** check where the path really leads, and report the link and its target separately when they differ. Lesson from the shell probes: plain `ls` of a link whose target is closed succeeds and prints the link's name.
- **Socket:** connect and close. Send nothing.

Known limit, to state in the output and in the docs: on Linux a write into an excluded folder that lies inside a writable one succeeds in a throwaway copy. From inside this looks like `write`. Phase 2 below closes it.

### Programs

A program is not a type of its own. It produces:

- a `capability` row: `present` or `absent` (the `PATH` lookup);
- a `file` row when the resolved file is writable, or its folder accepts a new entry (rename over it): `RISK`, the detail says which.

Every folder of `PATH` is also checked once, in order. A writable folder is a `file` row with `RISK`: a command can shadow every program found later in `PATH`. The detail names the first few it shadows.

### Network

| Permission | Meaning |
|---|---|
| `blocked` | Refused, or no answer within the timeout. |
| `connect` | A connection was made. |
| `listen` | A listening socket could be opened. |

How to test:

- **Listen:** open a TCP listener on the address and close it. Port 0 for the built-in checks.
- **Connect to a port:** dial it.
- **Reach a URL or host:** two separate checks, because sandboxes filter with a proxy: once honouring the proxy variables (`HTTPS_PROXY`, `ALL_PROXY`), once dialling directly. A TLS handshake or an HTTP `HEAD` is enough; send no payload.
- **Own loopback:** listen on `127.0.0.1:0`, then connect to that port.
- Timeout 5 seconds per check, run in parallel.

These checks leave the machine. `--skip network` turns them all off; the docs say so next to the command.

Not checkable from inside: whether a port the command listens on is reachable from outside the sandbox. Phase 2.

### Environment variables

| Permission | Meaning |
|---|---|
| `set` | The variable exists and is not empty. |

Names only. The value is never read into a result, printed or logged. A variable that is not set produces no row.

### Capabilities

| Permission | Meaning |
|---|---|
| `absent` | Not there. |
| `present` | There; not exercised. |
| `works` | An active check exercised it and it worked. |
| `fails` | An active check exercised it and it did not work. |

Passive capability checks are Go and on by default. Active ones are scripts, off by default (see "Probe scripts").

## Groups and built-in checks

A group is the unit of `--only` and `--skip`. Each check has an id `group:name`, a one-line reason, and the permissions that are safe. Anything else is a `RISK`.

| Group | Targets | Safe |
|---|---|---|
| `shell` | Startup files, from `$SHELL`: bash `~/.bashrc`, `~/.bash_profile`, `~/.profile`; zsh `~/.zshrc`, `~/.zprofile`, `~/.zshenv`; fish `~/.config/fish/config.fish` | Reading is reported, not judged. `write` and `create` are a risk: code that runs in every later shell. |
| `secrets` | Every path of the embedded `agent/secrets` filter (`sandboxpolicy.FilterSet.Paths`): one list, no copy. Plus discovery (below). Plus, with a profile, its excludes. | `blocked`, `absent`. `read` is a risk (leak), `write` and `create` too (tampering). An `env` row is a risk when `set`. |
| `agent` | Claude: `$CLAUDE_CONFIG_DIR` when set, else `~/.claude` and `~/.claude.json`; in it `settings.json`. Codex: `$CODEX_HOME` when set, else `~/.codex` (`config.toml`, `auth.json`). In the working folder: `.claude/`, `.mcp.json`. | Reading is reported. Writing is a risk unless the policy grants it (an agent writes its own files: `agent/claude`). |
| `ateam` | The user directory (`ATEAM_HOME`, else `~/.ateam`): `ateam.hcl`, `secrets.env`, `profiles/`. The project's `.ateam/`: `ateam.hcl`, `secrets.env`, `profiles/`, `state.sqlite`. | `blocked`, `absent`. `create` on `profiles/` is the risk the Linux gap opens. |
| `git` | `.git/config` of the working folder; `~/.gitconfig`; the hooks folder (`.git/hooks/`, or where `core.hooksPath` points, read from the repository's and the user's config); each hook in it that is not a `.sample` | `read`. A hooks folder that accepts a new file (`create`) and a hook that is writable are risks: git runs them outside the sandbox later. A hook that exists is listed by name even when it is safe. |
| `where` | The working folder, the home, `$TMPDIR`, `/tmp`; projects next to the working folder (discovery) | Reported, not judged, except: `write` on the home itself, and `write` on a sibling project, are risks. |
| `programs` | ateam itself (`os.Executable`), `$SHELL`, `sh`, `bash`, `zsh`, `git`, `ssh`, `sudo`, `curl`, `python3`, `node`, `go`, `make`, `claude`, `codex`, `nono`, `fence`, `docker`, `podman`, `limactl`, `colima`, `playwright-cli`, `npx`, `bwrap`, `socat`, `tmux`, `gh`, `aws`, `gcloud`, `kubectl`, `npm`, `uv`, `bun`, `pip3`; then every folder of `PATH` | `present`, `absent`. A replaceable file or a writable `PATH` folder is a risk. On a developer's machine ateam itself is usually replaceable: expected, still flagged. |
| `network` | Listen on `127.0.0.1:0` and on `0.0.0.0:0`; own loopback; `https://example.com` through the proxy and directly; a direct dial to `1.1.1.1:443`; a DNS lookup; the cloud metadata address `169.254.169.254:80` | Reported. `connect` on the metadata address is a risk: it hands out the machine's credentials. The rest is not judged without a policy: an open network is normal outside a sandbox. |
| `sockets` | `SSH_AUTH_SOCK`; `DOCKER_HOST` when it is a `unix://` path; `/var/run/docker.sock`; the rootless Docker and podman sockets under `$XDG_RUNTIME_DIR`; `/run/containerd/containerd.sock`; the gpg-agent socket | `absent`, `blocked`. `connect` is a risk. |
| `docker` | Passive, through the socket's `/info` and `/version` (plain HTTP over the socket, no `docker` CLI needed): rootless or rootful, the runtimes (`sysbox-runc`), the security options (user namespaces), what backs it (Docker Desktop, lima, colima, a plain daemon). | No reachable daemon is `ok`. A reachable rootless daemon, or one whose default runtime is sysbox, is reported with that in the detail. A reachable rootful daemon is a `RISK`; when `/info` does not tell whether the daemon is constrained, the detail says so ("cannot tell if limited: `--with docker-build`"). |
| `playwright` | `playwright-cli` in `PATH`; the browser cache (`~/Library/Caches/ms-playwright`, `~/.cache/ms-playwright`, `$PLAYWRIGHT_BROWSERS_PATH`) | Reported, not judged. The detail says it was not launched and how to launch it. |
| `identity` | The user id (root or not); the groups (`docker`, `sudo`, `wheel`, `admin`); on Linux the effective capabilities (`CapEff` in `/proc/self/status`) | Root, the `docker` group, and any capability beyond the default set of an unprivileged process are risks. |
| `mounts` | Linux only: `/proc/self/mountinfo`. Bind mounts that come from outside the container's own filesystem, each with read-only or read-write. | Reported. A writable mount of a home, of `/`, or of a Docker socket is a risk. |

The header is not a check: the system, `container.IsolationSource()`, `ATEAM_IN_SANDBOX`, `FENCE_SANDBOX`, the user, which proxy variables are set, `TMPDIR`. When none of these shows a sandbox or a container, it says `isolation: none detected`, and the report follows as usual.

## Discovery

Bounded, by name and by place. No file content is read to decide anything. Each rule produces ordinary checks, in the group shown.

| Rule | Looks at | Finds | Group |
|---|---|---|---|
| Names in the home | depth 1, and `~/.config` to depth 2 | `id_*`, `*.pem`, `*.key`, `.env*`, `*credentials*`, `*token*`, `.netrc` | `secrets` |
| Names in the working folder | depth 3, skipping `.git`, `node_modules`, `vendor` | the same patterns | `secrets` |
| Environment variables | names only | `*TOKEN*`, `*KEY*`, `*SECRET*`, `*PASSWORD*`, and known ones (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GH_TOKEN`, `GITHUB_TOKEN`, `AWS_*`) | `secrets` |
| Sibling projects | entries of the working folder's parent that hold a `.git` | readable, writable | `where` |
| Mounts | `/proc/self/mountinfo` | see the `mounts` group | `mounts` |

Rules:

- Caps: at most 2000 entries listed and 200 rows produced per rule. When a cap is hit, say so on stderr with the rule's name.
- A path found by discovery that a fixed list already has appears once, with the fixed list's id (compare with `sandboxpolicy.RealPath`).
- A discovered row's id is `group:path` with `~` kept. It is stable on one machine, not comparable across machines the way a fixed id is.
- Known limit, for the docs: discovery works by name. A secret in a file called `notes.txt` is not found.

Variable names that match a pattern without holding a secret (`SSH_AUTH_SOCK` does not match; `KEYBOARD_LAYOUT` would) are noise. Keep a short ignore list next to the patterns, seeded with the common ones (`KEYBOARD_*`, `*_KEYMAP`, `SSH_ASKPASS`, `GPG_TTY`, `*KEYRING*` names that hold a path, `COLORTERM`-style terminal variables that happen to match), and extend it as cases show up.

## Probe scripts (active capability checks)

An active check is one shell script, embedded in the binary, off by default, enabled by id with `--with`. Adding one is adding a file: no Go change.

```sh
#!/bin/sh
# id: docker-build
# group: docker
# target: docker build
# why: a command that can build an image runs arbitrary code in the daemon
# starts: builds one image from a two-line Dockerfile, then removes it
# risk: works
# needs: docker
```

Contract:

- Location: `defaults/probes/<id>.sh`, added to the `embed.FS` of `defaults/embed.go`.
- Header: `id`, `group`, `target` (the name column), `why`, `starts` (one line, printed by `--list` and on stderr before the script runs), `risk` (`works`, `fails` or `none`: which result is a `RISK`), `needs` (programs; when one is missing the result is `absent` and the script does not run).
- Run with `sh -s`, the script on stdin: no file is written. The working folder is a scratch folder under `$TMPDIR` that the runner creates and removes.
- Exit 0: `works`. Exit 1: `fails`. Any other code, or the timeout (60 seconds): `fails` with `unknown: <code>` in the detail and no `RISK` judgment.
- The first line of stdout is the detail column. Nothing else is kept.
- A script cleans up what it starts (a `trap`), and names what it creates `ateam-probe-<pid>` so a leftover can be found.
- A script only exercises the capability. It never reads the owner's files, never sends anything beyond what the capability needs.

First scripts:

| Id | Does | `risk` |
|---|---|---|
| `playwright-launch` | Starts a headless browser with `playwright-cli`, loads `about:blank`, closes it. | `none` |
| `docker-build` | Builds one image from a two-line Dockerfile, removes it. | `works` |
| `docker-build-expand-bind` | Privilege escalation through the daemon. Starts a container that mounts only the working folder and the Docker socket. From inside it, starts a second container that mounts the home (the parent of the working folder when the working folder is not under the home) and lists it. `works` when the second container sees entries of a folder the first one was never given: the daemon lets a confined container widen its own mounts. Both containers are removed. | `works` |

`docker-build-expand-bind` needs an image that holds a `docker` CLI. It uses `docker:cli`, pulled when missing; the `starts:` line says so.

Documentation for agents that add a probe: a section "Adding a probe" in `DEV.md` (the header fields, the exit codes, the cleanup rule, how to test one with `ateam probe --only ID --with ID`), and a pointer to it at the top of `defaults/probes/`. A unit test loads every embedded script and fails on a missing or unknown header field, so a new probe is checked by `make test`.

Not in the first version: `--with-script PATH` for a script outside the binary.

## Checks from a policy

With a profile, every rule of the policy becomes a check with an expected permission. `ateam probe --profile dev/go` asks whether the current environment gives what `dev/go` needs; with `--start-sandbox` it asks whether the backend applies the profile.

| Policy | Check | Expected |
|---|---|---|
| The working folder, each `Write` rule | file | `write` (`create` when missing) |
| Each `Read` rule | file | `read`, or `write` when also writable |
| Each `Exclude` rule | file | `blocked`, `absent` |
| Each `ReadOnly` rule | file | `read` |
| Each host of `Domains` | network, through the proxy | `connect` |
| One host that is not listed (`example.org`) | network, both ways | `blocked` |
| Each `DeniedDomains` host | network | `blocked` |
| `HostPorts`, `HostLocalhost` | connect | reported only: nothing may be listening there |
| Each `Listen` port | listen | `listen` |
| No network rule at all | every network check | `blocked` |

A built-in check whose target a policy rule covers takes the policy's expectation instead of the built-in safe set. Example: `~/.claude` is writable under `agent/claude`, and that is `ok`, not a `RISK`.

Skip wildcard hosts (`*.github.com`): there is nothing to dial.

## Verdicts

- `ok`: as expected, or nothing is expected.
- `RISK`: more open than it should be. A built-in check outside its safe set with no policy rule that allows it, or a policy rule that closes something which is open (an exclude that is readable, an unlisted host that answers).
- `UNMET`: more closed than the profile asks. A `Write` folder that is blocked, an allowed host that does not answer, a `Listen` port that cannot be opened. Only possible with a profile. The detail says what the profile asked for.

Without a profile the report is `ok` or `RISK` only.

## Output

```
nothing started: checking this environment
linux, container: docker, user: root, proxy: none, isolation: none detected

secrets
  RISK   read     file        ~/.ssh/id_ed25519
  RISK   set      env         GH_TOKEN
  ok     blocked  file        ~/.aws
git
  RISK   write    file        .git/hooks/pre-commit      runs outside the sandbox later
  RISK   create   file        .git/hooks/
docker
  RISK   connect  file        /var/run/docker.sock       rootful daemon, cannot tell if limited: --with docker-build
network
  RISK   connect  network     169.254.169.254:80         cloud metadata
  ok     connect  network     https://example.com (direct)
playwright
  ok     present  capability  playwright-cli             not launched: --with playwright-launch

5 RISK, 0 UNMET, 23 ok (18 hidden: --all), 0 skipped
```

- First line: what was started. `nothing started: checking this environment`, or `started: fence sandbox, profile sandbox/fence,dev/go`, followed by one line per active check that ran (`started: docker-build, builds one image ...`).
- Second line: the environment header.
- Columns: verdict, permission, type, name, detail. Aligned across the whole report, not per group. The detail is filled only when it adds something.
- Groups in the fixed order of the table above. Inside a group: `RISK`, then `UNMET`, then `ok`. A group with no visible row is not printed.
- Hidden by default: `ok` rows whose permission is `absent`. `--all` shows them. The last line counts them.
- Colours: `RISK` red, `UNMET` yellow, `ok` dim, group names bold. Off when stdout is not a terminal or `NO_COLOR` is set. The escape codes already live in `internal/runner/format_stream.go`; move the ones needed to `internal/display` and use them from both, do not copy them.
- Paths are printed with `~` for the home (`display.TildeHome`).

`--json`: `{"started": [...], "environment": {...}, "checks": [{"id", "group", "type", "target", "permission", "expected", "verdict", "detail"}]}`. Fixed ids are stable across systems (`secrets:~/.ssh`), so two files can be compared with any diff tool.

## Safety rules

These are requirements, each with a test.

- Never truncate, never write a byte to an existing file, never rename or remove anything but the command's own marker files and scratch folders.
- Marker files are empty, named `.ateam-probe-<pid>-<random>`, and removed at once. A removal that fails is reported with the path.
- Never print, log or keep file content or the value of an environment variable. One byte is read and discarded.
- No request beyond a handshake or a `HEAD`. No credentials are sent: build the HTTP client without the user's cookies or netrc.
- Listeners are closed at once.
- Nothing is started unless `--start-sandbox` or `--with` asks for it, and the output says what was.
- Never follow a check with a "fix".

An earlier probe appended a line to `.ateam/ateam.hcl` by accident. The tests must prove the write check leaves size and modification time unchanged.

## Notes for the docs

- **macOS privacy dialogs.** Reading `~/Documents`, `~/Desktop`, `~/Downloads` or the keychains from a plain terminal can make macOS ask the owner for permission. Not handled, not tested: the dialog is the system's answer, and a refusal shows as `blocked`.
- **Network checks leave the machine**: `example.com`, `1.1.1.1`, a DNS lookup. `--skip network`.
- **Discovery is by name**, and capped.
- **Docker**: the passive check cannot always tell what a reachable daemon allows. The active probes can.

## Code layout

- `internal/sandboxprobe/`: no cobra, no configuration loading.
  - `Check{ID, Group, Type, Target, Why, Safe []Permission, Expect []Permission, Before *Facts}` and `Result{Check, Permission, Verdict, Detail}`.
  - `CheckFile`, `CheckNet`, `CheckEnv`: one function per type. Programs are built from `CheckFile` and a `PATH` lookup.
  - `Defaults(env Env) []Check`: the built-in list, from a small struct (home, working folder, `$SHELL`, `PATH`, the config variables, the environment's names), so tests can build it for a scratch home.
  - `Discover(env Env) []Check`.
  - `FromPolicy(*sandboxpolicy.Policy) []Check`.
  - The passive capability checks (docker `/info`, identity, mounts, playwright presence).
  - The script runner: parses the headers from the embedded files, runs one.
  - `Select(checks, only, skip, with)`, `Run(checks) []Result`, `Table(w, results)`, JSON encoding.
- `defaults/probes/*.sh`: the probe scripts.
- `cmd/probe.go`: flags, mode choice, the launch, the hidden `--run-checks`.
- Reuse, do not copy: `addLaunchFlags`, `composeLaunch`, `buildSandboxPolicy`, `planLaunch`, `sandboxType`, `runGuarded`, `shellJoin`, `sandboxpolicy.RealPath`, `sandboxpolicy.Within`, `container.IsolationSource`, `display.TildeHome`.
- `planLaunch` refuses a profile with rules on a container that enforces none. `--profile dev/go` without `--start-sandbox` must not hit that: it needs the policy, not a launch. Build the policy without going through that refusal.

Rules of the codebase that apply: no duplicated code, comments only where not obvious, the style of `internal/sandboxpolicy`. Ask the owner before adding anything this spec does not list.

## Build order

Each step ends with `make test` green and something the owner can run.

1. `internal/sandboxprobe`: `CheckFile`, `CheckNet`, `CheckEnv`, the fixed groups, the table, `--json`, `--only`, `--skip`, `--all`, `--quiet`, `--list`. `ateam probe` in place.
2. Discovery, `sockets`, `identity`, `mounts`.
3. The passive `docker` and `playwright` checks.
4. The script runner, `--with`, `playwright-launch`, `docker-build`, the "Adding a probe" documentation.
5. `--profile` in place: `FromPolicy`, `UNMET`.
6. `--start-sandbox`: the launch, the "before" facts, `--run-checks`.
7. The docs.

## Tests

- Unit tests in `internal/sandboxprobe`, in a scratch home (`t.TempDir()`), using file permissions to produce each permission: all file permissions, a link to a closed target, a hidden folder through "before" facts, a replaceable program, a writable folder earlier in `PATH`, a Unix socket, listen and connect on local ports. No test may need the network or Docker.
- A test that the write check changes neither size nor modification time, and that no marker file is left.
- Discovery in a scratch home: each pattern, the caps, the merge with a fixed id. Environment names from a given list, and a test that no value reaches a result.
- The docker check against a fake `/info` served on a Unix socket in the scratch folder: rootless, rootful, sysbox, Docker Desktop.
- `mountinfo` and `CapEff` parsing from sample text.
- The script runner with test scripts: exit 0, 1, another code, a missing `needs` program, the timeout. Every embedded script has a valid header.
- `Select`: groups, ids, an unknown name.
- `cmd` tests in the style of `cmd/sandbox_launch_test.go`: the check list of a profile, the expectations, `--dry-run`, the verdict when a policy rule covers a built-in check, `--start-sandbox` refused inside a sandbox, `--profile` alone starting nothing.
- `make test`, `make run-ci`. `make test-docker` is not needed: nothing under `internal/container/` changes, and the move of the colour helpers out of `internal/runner/` changes no behaviour (say so to the owner when doing it; run `make test-docker` if more than that moves).
- Real runs, by the owner from a plain terminal (a sandbox does not start inside an agent's sandbox):
  - `ateam probe` in a plain terminal: many `RISK`, which is right.
  - `ateam probe --start-sandbox --profile sandbox/nono` and `sandbox/fence` on macOS: no `UNMET`, no `RISK` on a path the profile excludes.
  - `ateam probe --start-sandbox --profile sandbox/fence` on Linux: one `RISK`, `create` on `.ateam/profiles`, the known gap.
  - Inside a hand-started nono or fence sandbox: `ateam probe`.
  - `ateam probe --with playwright-launch,docker-build`.
  - The Linux binary in the Debian container used for the fence probes (see the steps file), which the agent can run itself through Docker.

## Docs to update

`COMMANDS.md` (the command), `ISOLATION.md` ("Seeing what a profile does": add the command), `README.md` (one line), `CLAUDE.md` ("How to test"), `DEV.md` ("Adding a probe"), `SANDBOX_DIFFERENCES.md` (how its cells can now be reproduced), the steps file.

## Phase 2, not in the first version

- **Checks from outside after the run.** The outer ateam verifies what only it can see: that a marker created inside exists outside (else the write was thrown away: permission `discarded`), and that a port the command listens on answers from outside. This replaces `verify` in `test/sandbox/run-probes.sh`.
- **`make test-sandbox` on top of this command**, retiring `sandbox-probe.sh`.
- **`ateam probe diff A.json B.json`**, a readable comparison.
- **`--with-script PATH`**: a probe script outside the binary.
- **More kinds:** process visibility.

## Still open

Nothing. Settled on 2026-11-24: `docker-build-expand-bind` is the nested-container escalation described above; the ignore list for environment variable names is seeded.
