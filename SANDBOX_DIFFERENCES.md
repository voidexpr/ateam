# Sandbox differences

What each sandbox can express, per operating system. Used to decide which rules ateam can offer and where a profile must be refused. How to use the backends: [ISOLATION.md](ISOLATION.md). Where each one can start (Docker, CI, nested): [ISOLATION.md → Where a sandbox backend can start](ISOLATION.md#where-a-sandbox-backend-can-start).

Last checked: 2026-11-24. nono 0.82.0, fence 0.1.67 (upstream) and ateam's fork of it, Claude Code's documentation of 2026-11-23.

How sure each cell is:

| Mark | Meaning |
|---|---|
| **run** | Seen in a real run (`make test-sandbox` on macOS; on Linux a hand test for nono, and for fence the same probes on a desktop as a plain user and in a Debian container, `make test-sandbox-docker`) |
| src | Read in the tool's source |
| doc | From the tool's documentation, not run |
| ? | Not checked |

Cells for the Claude sandbox come from a summarised read of its documentation: check the page before relying on one.

## The two fences

- **Upstream**: `github.com/fencesandbox/fence`. Cells marked **run** for it were seen with v0.1.67, which ateam built with until 2026-11-24.
- **ateam's fork**: `github.com/voidexpr/fence`, branch `fork`, pinned in `go.mod` by a `replace` line (`7c71685`: upstream's main branch plus the carve-out changes). The changes: a literal `allowRead` path strictly inside a `denyRead` entry stays readable, and a literal `allowWrite` path strictly inside a `denyWrite` entry stays writable; the deepest path wins and a deny wins a tie. On macOS a denied path that is a link is denied as a link too.
- Network rules, the mechanism and where a sandbox can start are the same in both: the tables below have one fence column for them.

## Mechanism

| | macOS | Linux |
|---|---|---|
| nono | Seatbelt | Landlock (a list of allowed folders; no deny), seccomp |
| fence | Seatbelt | `bwrap`: a private view of the filesystem built from mounts, and a private network; `socat` connects the proxy to it. Landlock and seccomp on top |
| Claude sandbox (the agent's own) | Seatbelt | `bwrap`, private network, proxies on the host |

What the mechanism decides:

- Seatbelt takes allow and deny rules on paths; the last matching rule wins. Anything can be expressed; what a tool offers depends on the order it writes its rules in.
- Landlock only allows. A deny inside an allowed folder cannot be expressed.
- Mounts can hide a folder (an empty one mounted over it) and make a path read-only (a read-only mount).

## File rules

The examples use the home as the readable area, as ateam profiles do.

Two fences are compared: **upstream** (v0.1.67, what the `fence` command of the project does) and **ateam's fork** (`github.com/voidexpr/fence`, the one compiled into ateam; see "The two fences" above). "same" means the fork does what upstream does.

| Rule | Example | nono macOS | fence upstream macOS | fence fork macOS | nono Linux | fence upstream Linux | fence fork Linux |
|---|---|---|---|---|---|---|---|
| Deny inside a grant | the home, except `~/.ssh` | Yes, **run** | Yes, **run** | same | No, **run**: refuses to start | Yes, **run**: the folder is seen empty | same |
| Read allowed inside a deny (read carve-out) | `~/.ssh/config` with `~/.ssh` closed | Yes, **run** | No, **run**: denies are written last and win | Yes, **run**: the allow is written after the deny | Only by granting entry by entry, doc | No, src: its mount planner forbids it | Yes, **run**: the file is mounted back over the hidden folder |
| What a listing of that folder shows | `ls ~/.ssh` | Refused | Refused | Refused, **run** | not applicable | An empty folder, **run** | The opened entries only (`config`), **run** |
| Write allowed inside a folder closed for reads and writes (write carve-out in an excluded folder) | `--write ~/.ssh/known_hosts` with `~/.ssh` closed | Yes, **run** | No | Yes, **run**: the allow and the unlink allow are written after the denies | not applicable | No | Yes, **run**: the path is mounted back read-write over the hidden folder |
| Write allowed inside a folder closed for writes only | `.ateam` read-only, `.ateam/shared` writable | Yes (raw Seatbelt rules), not used | No, **run** | Yes, src | not applicable | No, **run** | Yes, **run**: a writable mount after the read-only one |
| The home as one grant | `read ~` | Yes, **run** (with `allow_parent_of_protected`) | Yes, **run** | same | No, **run**: it holds nono's own state | Yes, **run** | same |
| Read-only inside a writable folder | `.git/hooks` in the working folder | Yes, **run**: raw Seatbelt rules added by ateam | Yes, **run**: `denyWrite` | same | No: Landlock has no deny | Yes, **run**: read-only mount | same |
| A read reopened inside an excluded folder of the working folder stays read-only | `--read private/readme.txt` | Yes, **run** | not applicable | Yes, **run** | not applicable | not applicable | Yes, **run** |
| Rename of a folder above a protected path refused | `mv .git .git2` | Yes, **run**: ateam pins each folder | Yes, **run**: fence does it itself | same | ? | Yes, **run**: only because ateam binds each such folder onto itself; not without | same |
| A protected path that does not exist yet cannot be created | `.ateam/profiles/` missing, in the working folder | Yes, **run** | Yes, **run** | same | ? | **No, run**: a mount needs the path to exist | Yes for the project's `.ateam`, **run**: ateam seals it (read-only, its open entries writable). Still no elsewhere (a missing `.git/hooks`) |
| A write into an excluded folder inside a writable one | `echo x > private/token` | Refused, **run** | Refused, **run** | same | not applicable | Succeeds and is thrown away; the real file is untouched, **run** | same |
| An excluded path that is a link | `~/.ssh` kept in a synced folder | Link and target closed, **run** | Target closed; the link's name and target stay visible, **run** | Link and target closed, src | ? | ? | ? |
| Reads the tool blocks on its own | credentials, keychains, shell configs | Yes; ateam lifts each one the profile does not exclude | None | same | Yes, 5 required groups, **run** | None, src | same |
| Writes the tool refuses on its own | `.vscode/`, `.mcp.json`, shell startup files | None left | Yes, a fixed list no setting lifts | same | None left | Yes, src | same |
| The profile is checked before the run | | `nono profile validate`; does not catch the Linux refusals, **run** | fence is asked about every rule (`CheckReadPath`, `CheckWritePath`) | same, and the check knows the carve-outs | same as macOS | same as macOS | same as macOS |

Consequences for ateam:

- macOS, nono: every rule of a profile.
- macOS and Linux, fence fork: every rule of a profile. `dev/go` works (it reads one folder inside an excluded one); an unattended run's own folders are opened inside the excluded `~/.ateam`.
- With upstream fence, a read inside an excluded folder stays closed: a `fence.json` exported by ateam and run with the upstream `fence` command is stricter than the same file under ateam, never looser.
- Linux, nono: needs the home listed entry by entry around every exclude and around nono's state. The list is taken at launch: an entry created later is not readable. No read-only path inside the working folder.
- Linux, fence fork: the project's `.ateam` is sealed (read-only, its open entries writable), so nothing can be planted in it. A protected path that does not exist yet elsewhere in a writable folder can still be created, and is said at every launch. With upstream fence the `.ateam` case is open too.

## Network rules

All three filter hosts with a proxy outside the sandbox and refuse direct connections. A program that ignores the proxy variables reaches nothing.

| Rule | Example | nono macOS | nono Linux | fence macOS | fence Linux | Claude macOS | Claude Linux |
|---|---|---|---|---|---|---|---|
| Close everything | no host listed | Yes, **run** (`network.block`; open when left out) | Yes, doc | Yes, **run** | Yes, **run** (another host is refused) | Yes, doc | Yes, doc |
| Allow a host | `github.com` | Yes, **run** | Yes, doc | Yes, **run** | Yes, **run** | Yes, doc | Yes, doc |
| Wildcard host | `*.github.com` | Yes, doc | Yes, doc | Yes, src | Yes, src | Yes, doc | Yes, doc |
| Deny one host inside a wildcard | `*.example.com` but not `internal.example.com` | ? | ? | Yes, src (`deniedDomains`) | Yes, src | ? | ? |
| Host with a port | `example.com:8443` | ? | ? | ? | ? | Yes, doc | Yes, doc |
| Method and path on a host | `GET api.github.com/repos/*` | Yes, doc (`--allow-endpoint`) | Yes, doc | No | No | No | No |
| All of the host's localhost | `localhost` | Yes, doc (`open_port` 0) | No, doc | Yes, src (`allowLocalOutbound`) | No, **run**: ateam refuses the rule there | Yes, doc (`allowLocalBinding`) | No, doc |
| One port of the host's localhost | `localhost:5432` | Yes, doc (`open_port`) | Yes, doc | No: all or nothing | Yes, **run** (`allowLocalOutboundPorts`): the listed port answers, another does not |
| The sandbox talks to what it starts, over localhost | a test that opens its own server | Only by opening all of localhost | ? | Only by opening all of localhost | Yes, **run**: a localhost of its own | No: all or nothing, doc | No, doc: the sandbox's localhost is not the host's |
| A port the sandbox listens on, reached from the host | a dev server on `:3000` | Yes, doc: with all of localhost | Yes, doc | Yes, src: with all of localhost | Yes, **run** (a bridge per port, `listen`) | Yes, doc (`allowLocalBinding`) | Not documented |
| The same, reached from other machines | `0.0.0.0:3000` | ateam refuses it | ? | No, src: only localhost can be bound | Yes, src (not run) | ? | ? |
| Unix socket by path | `/var/run/docker.sock` | Yes, doc | Yes, doc (`af_unix_mediation`) | Yes, src | Yes, src | Yes, doc (`allowUnixSockets`) | No, doc: all or none |
| Network rules hold when a private network cannot be created | Docker without `NET_ADMIN`, CI | not applicable | ? | not applicable | No, doc: fence continues with the proxy variables only. ateam makes fence's own probe first and refuses, **run** | not applicable | ? |

Notes:

- ateam writes: hosts, denied hosts (fence), `localhost` and `localhost:PORT` (the profile lists `domains` and `deny_domains`, see CONFIG.md). On macOS "all of localhost" is also opened whenever there is at least one host, so a command can talk to what it starts. Without any rule the network is closed, localhost included.
- "All of localhost" exists on macOS only, in every tool. "One port" exists on Linux in nono and fence, and on macOS in nono only. No tool has both forms on both systems.
- Claude sandbox: listing `localhost` in `allowedDomains` only affects connections that go through the proxy; `localhost` is in `NO_PROXY`, so a plain `curl localhost` is a direct connection and is not covered by it.
- nono's method and path rules and its key injection are not used by ateam yet (phase 3).

## Upstream issues

nono's tracker, all open on 2026-11-23. Only the issue texts were read, not the discussions.

| Issue | Subject | Row above |
|---|---|---|
| [nono #2296](https://github.com/nolabs-ai/nono/issues/2296) | Blocking a project's `.env` on Linux: the guide's supervised-mode advice does not work | Deny inside a grant, nono Linux |
| [nono #2086](https://github.com/nolabs-ai/nono/issues/2086) | A grant above nono's own state folder is refused | The home as one grant, nono Linux |
| [nono #2046](https://github.com/nolabs-ai/nono/issues/2046) | Read-only access to the root folder refused because of the protected state (title only, not read) | The home as one grant, nono Linux |
| [nono #2310](https://github.com/nolabs-ai/nono/issues/2310) | A write-deny for a subpath, with a nested re-allow | Read-only inside a writable folder; carve-out |
| [nono #2157](https://github.com/nolabs-ai/nono/issues/2157) | Protect `.git`, `.git/hooks` first, by default | Read-only inside a writable folder |
| [nono #1652](https://github.com/nolabs-ai/nono/issues/1652) | Localhost: port 0 works on macOS only; port ranges | All of the host's localhost |

fence: its tracker was not searched. The carve-out limit is documented behaviour there ("denyRead always wins").

## What ateam's network rules do with this (built 2026-11-23)

- `localhost` (all ports) and `localhost:PORT` (a server on the host) are separate forms. Each backend maps the forms it has and refuses the others by name: `localhost` under fence on Linux; `localhost:PORT` under fence on macOS unless all of localhost is open already.
- nono: localhost without any host, and a denied host, are refused: what nono does with them was not checked.
- `listen` (2026-11-24): a port the command serves on. Linux under fence bridges it; macOS backends need all of localhost open and refuse it otherwise.
- Not built: a port on a host; method and path rules; an open network with denies.
- fence on Linux without a private network is refused, not accepted.
