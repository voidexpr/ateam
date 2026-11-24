# Sandbox differences

What each sandbox can express, per operating system. Used to decide which rules ateam can offer and where a profile must be refused. How to use the backends: [ISOLATION.md](ISOLATION.md). Where each one can start (Docker, CI, nested): [ISOLATION.md → Where a sandbox backend can start](ISOLATION.md#where-a-sandbox-backend-can-start).

Last checked: 2026-11-23. nono 0.82.0, fence 0.1.67, Claude Code's documentation of that date.

How sure each cell is:

| Mark | Meaning |
|---|---|
| **run** | Seen in a real run (`make test-sandbox` on macOS, or a hand test on Linux) |
| src | Read in the tool's source |
| doc | From the tool's documentation, not run |
| ? | Not checked |

Cells for the Claude sandbox come from a summarised read of its documentation: check the page before relying on one.

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

| Rule | Example | nono macOS | fence macOS | nono Linux | fence Linux |
|---|---|---|---|---|---|
| Deny inside a grant | the home, except `~/.ssh` | Yes, **run** | Yes, **run** | No, **run**: refuses to start | Yes, src |
| Allow inside a deny (carve-out) | `~/.ssh/config` with `~/.ssh` closed | Yes, **run** | No, **run**: denies are written last and win | Only by granting entry by entry, doc | No, src: its mount planner forbids it |
| The home as one grant | `read ~` | Yes, **run** (with `allow_parent_of_protected`) | Yes, **run** | No, **run**: it holds nono's own state | Yes, src |
| Read-only inside a writable folder | `.git/hooks` in the working folder | Yes, **run**: raw Seatbelt rules added by ateam | Yes, **run**: `denyWrite` | No: Landlock has no deny | Yes, src: read-only mount |
| Rename of a folder above a protected path refused | `mv .git .git2` | Yes, **run**: ateam pins each folder | Yes, **run**: fence does it itself | ? | ? |
| An excluded path that is a link | `~/.ssh` kept in a synced folder | Link and target closed, **run** | Target closed; the link's name and target stay visible, **run** | ? | ? |
| Reads the tool blocks on its own | credentials, keychains, shell configs | Yes; ateam lifts each one the profile does not exclude | None | Yes, 5 required groups, **run** | None, src |
| Writes the tool refuses on its own | `.vscode/`, `.mcp.json`, shell startup files | None left | Yes, a fixed list no setting lifts | None left | Yes, src |
| The profile is checked before the run | | `nono profile validate`; does not catch the Linux refusals, **run** | fence is asked about every rule (`CheckReadPath`, `CheckWritePath`) | same | same |

Consequences for ateam:

- macOS: both backends enforce every rule of a profile. fence refuses a carve-out; a change in fence's rule order would lift that.
- Linux, nono: needs the home listed entry by entry around every exclude and around nono's state. The list is taken at launch: an entry created later is not readable. No read-only path inside the working folder.
- Linux, fence: expresses excludes and read-only paths. Not enabled in ateam yet, and nothing was run there.

## Network rules

All three filter hosts with a proxy outside the sandbox and refuse direct connections. A program that ignores the proxy variables reaches nothing.

| Rule | Example | nono macOS | nono Linux | fence macOS | fence Linux | Claude macOS | Claude Linux |
|---|---|---|---|---|---|---|---|
| Close everything | no host listed | Yes, **run** (`network.block`; open when left out) | Yes, doc | Yes, **run** | Yes, src | Yes, doc | Yes, doc |
| Allow a host | `github.com` | Yes, **run** | Yes, doc | Yes, **run** | Yes, src | Yes, doc | Yes, doc |
| Wildcard host | `*.github.com` | Yes, doc | Yes, doc | Yes, src | Yes, src | Yes, doc | Yes, doc |
| Deny one host inside a wildcard | `*.example.com` but not `internal.example.com` | ? | ? | Yes, src (`deniedDomains`) | Yes, src | ? | ? |
| Host with a port | `example.com:8443` | ? | ? | ? | ? | Yes, doc | Yes, doc |
| Method and path on a host | `GET api.github.com/repos/*` | Yes, doc (`--allow-endpoint`) | Yes, doc | No | No | No | No |
| All of the host's localhost | `localhost` | Yes, doc (`open_port` 0) | No, doc | Yes, src (`allowLocalOutbound`) | No, src | Yes, doc (`allowLocalBinding`) | No, doc |
| One port of the host's localhost | `localhost:5432` | Yes, doc (`open_port`) | Yes, doc | No: all or nothing | Yes, src (`allowLocalOutboundPorts`) | No: all or nothing, doc | No, doc: the sandbox's localhost is not the host's |
| A port the sandbox listens on, reached from the host | a dev server on `:3000` | Yes, doc | Yes, doc | Yes, src | Yes, src (a bridge per port) | Yes, doc (`allowLocalBinding`) | Not documented |
| Unix socket by path | `/var/run/docker.sock` | Yes, doc | Yes, doc (`af_unix_mediation`) | Yes, src | Yes, src | Yes, doc (`allowUnixSockets`) | No, doc: all or none |
| Network rules hold when a private network cannot be created | Docker without `NET_ADMIN`, CI | not applicable | ? | not applicable | No, doc: fence continues with the proxy variables only | not applicable | ? |

Notes:

- ateam writes two things today: the list of hosts (`domains`), and "all of localhost" whenever there is at least one host. Without a host the network is closed, localhost included.
- "All of localhost" exists on macOS only, in every tool. "One port" exists on Linux in nono and fence, and on macOS in nono only. No tool has both forms on both systems.
- Claude sandbox: listing `localhost` in `allowedDomains` only affects connections that go through the proxy; `localhost` is in `NO_PROXY`, so a plain `curl localhost` is a direct connection and is not covered by it.
- nono's method and path rules and its key injection are not used by ateam yet (phase 3).

## What this means for the `network` entity (phase 2)

- `localhost` (all ports), `localhost:PORT` (a server on the host) and a port the sandboxed command listens on are three separate forms.
- Each backend maps the forms it has and refuses the others by name: `localhost` on Linux; `localhost:PORT` under fence on macOS, where only "all of localhost" exists (wider than asked).
- fence on Linux without a private network must be refused, not accepted.
