# Research: Network Sandboxing — per-sandbox secrets, SSH and browser capabilities

Date: 2026-11-21. Companion to `ResearchSandboxing.md` (§F.1.1 credential injection, §G networking) and `plans/feature_sandboxing_and_attended_agents.md` (phases 2 to 4).

How to read this document:

- Versions, stars and dates were read from project pages, registries and the GitHub API on 2026-11-21. They will drift.
- **UNVERIFIED** marks a claim not confirmed from a primary source in this pass.
- **Inference** marks reasoning from verified facts, not a documented behavior.
- Nothing here was run locally. Tools already reviewed in `ResearchSandboxing.md` are only revisited for specific gaps.

## 1. What we want

A managed solution: ateam spawns the sandbox and, beside it, the services that give that sandbox its network capabilities.

| # | Requirement |
|---|---|
| R1 | An egress proxy per sandbox that allowlists destinations and injects secrets. The agent never holds real values. |
| R2 | Secrets encrypted at rest, decrypted at startup, held in memory. |
| R3 | A secret is usable only by the sandbox it was loaded for. Each agent has its own set. |
| R4 | SSH per sandbox: specific hosts, restricted commands, real keys outside the sandbox. |
| R5 | Browser per sandbox (Playwright or similar): specific addresses only. |
| R6 | Bonus: the browser is logged in to some sites with credentials the agent never sees. |
| R7 | Clients that honor a proxy environment variable are acceptable. |

Starting point in ateam: `ateam secret` stores values in the OS keychain or a plain `.env` file, and passes real tokens to the agent's environment. So R2 holds today for the keychain backend only, and R1 and R3 do not hold.

## 2. Summary of findings

- **No single tool covers R1 to R6.** The pieces exist separately.
- **Secret proxy (R1 to R3):** several Go projects do injection, but none combines encryption at rest, per-client binding and an importable package. The proxy core is small. Building it in-process is the best fit for a single Go binary.
- **Per-sandbox binding (R3):** the simple, portable answer is one proxy listener per sandbox that holds only that sandbox's secrets, a random token in the proxy URL, and a sandbox rule that allows only that port.
- **SSH (R4):** an SSH agent filter can restrict key, host and user, never the command. Command control needs a host-side SSH broker that terminates the session, or enforcement on the server. No surveyed proxy documents per-command filtering for ordinary OpenSSH servers. No surveyed agent sandbox brokers SSH with command rules.
- **Browser (R5):** no browser tool offers an address restriction that is a security boundary. Playwright MCP and agent-browser say so explicitly. The boundary must be OS-level egress confinement of the browser and its driver, with the managed proxy as the only exit.
- **Browser placement:** Chromium does not start under a deny-default Seatbelt profile today. The browser should run outside the agent sandbox, in its own confinement, and be exposed through a filtered MCP tool surface, never raw CDP.
- **Browser credentials (R6):** proxy-side header injection reuses the R1 mechanism and keeps the secret out of the browser. Cookie sessions prepared by a human cover the rest, with a reduced tool set.
- **Shared limit:** none of this stops misuse of an authorized capability. An agent can still act through an allowed host, an allowed command, or a logged-in session.

## 3. Common architecture: one capability broker per sandbox

The three capabilities share one shape. ateam stays outside the sandbox and runs, per sandbox:

```
ateam (host, unsandboxed)
 ├─ secret store: decrypt once at startup, keep only this sandbox's values
 ├─ egress proxy listener  127.0.0.1:PORT_A  (token T_A)   ← HTTP(S), allowlist, injection
 ├─ ssh broker             unix socket or CONNECT :22       ← host/user/command policy
 ├─ browser service        MCP stdio shim → confined browser → egress proxy
 └─ sandbox A (nono / Seatbelt / Landlock / Docker)
      env: HTTPS_PROXY=http://ateam:T_A@127.0.0.1:PORT_A, CA bundle vars, placeholders
      rule: may connect only to PORT_A (and the ssh socket)
```

The layers match `ResearchSandboxing.md` §G.1: execution sandbox, network gateway, privileged operation gateway. The proxy is layer 2. The SSH broker and the browser service are layer 3.

### 3.1 Binding a capability to one sandbox

| Mechanism | Used by | macOS | Linux | Notes |
|---|---|---|---|---|
| Per-run token in the proxy URL, sent as `Proxy-Authorization` | nono, agent-vault, OneCLI, tokenizer, srt | Yes | Yes | Stops processes outside the sandbox. Anything inside can use it. Keep the hop on loopback: the token travels in cleartext. |
| Unguessable id as proxy username, selecting the policy | srt | Yes | Yes | Same mechanism. srt: "the id is therefore the only thing binding a connection to an allow list. It must be unguessable". |
| Sandbox rule allowing one localhost port | srt, nono (Seatbelt `remote tcp "localhost:PORT"`) | Yes | n/a | Limits what the sandbox reaches. Other host processes can still reach the port, so pair it with a token. |
| Landlock TCP connect rule + seccomp user-notify on `connect()` | nono | No | Yes (Landlock v4, kernel 6.7+) | Landlock matches ports, not addresses. seccomp checks the destination. |
| Network namespace with no route; proxy on a unix socket bind-mounted in | srt, runclaude | No | Yes | Strongest on Linux. |
| Unix socket as the proxy endpoint | suggested by runproxy | Seatbelt can allow one socket path | Yes | Most clients cannot use a unix-socket proxy directly. Needs a TCP-to-unix shim inside the sandbox. |
| Peer credential check on a unix socket | SPIRE, agent-box Portal, tailscaled (§3.2); no egress proxy surveyed | `LOCAL_PEERCRED`, `LOCAL_PEERPID`, `LOCAL_PEERTOKEN` | `SO_PEERCRED`, `SO_PEERPIDFD` | Unix sockets only. Plain PID checks race with pid reuse. Same-uid agents are not distinguished by uid. |
| One proxy instance per sandbox | agent-creds, iron-proxy, runproxy | Yes | Yes | If instance A holds only A's secrets, cross-agent use needs no identity logic. |
| Docker `--internal` network with a dual-homed proxy | runproxy, yagop/sandbox | Yes | Yes | For Docker modes. |
| mTLS client certificate | smokescreen | Yes | Yes | Client support for proxy mTLS is poor. |
| WireGuard or NetworkExtension tunnel | clawpatrol | NetworkExtension | netns | Heavy. A signed system extension on macOS (UNVERIFIED). |

Fit for ateam:

- One in-process listener per sandbox on a random loopback port.
- A 256-bit token in the `HTTPS_PROXY` userinfo, compared in constant time.
- The sandbox profile allows only that port.
- Secrets for agent A are loaded only into listener A.
- On Linux with bubblewrap, prefer a network namespace and a bind-mounted unix socket when available.

### 3.2 The secret boundary: a resource only one sandbox's processes can reach

Added 2026-11-22. The question behind R3, R4 and R5: how does a parent give the processes of one sandbox access to something (a secret broker, a proxy, an ssh-agent, a browser service) that no other agent and no other sandbox can use.

Starting point: the note "How to give a unique secret to a CLI agent process" (2026-09-19, `~/projects/research/Isolation/research_process_specific_secrets.md`). Its essentials, then what this pass verified or corrected.

**The note's framing**

- One limit applies to everything: a same-UID, unsandboxed peer can tamper with another agent's config, `PATH`, worktree hooks and shell rc files. Every same-UID scheme stops accidental or opportunistic cross-access, including from a prompt-injected agent. It does not stop a determined adversary. Only one UID per agent is a hard boundary.
- What does not work between same-UID peers:
  - A secret in an env var. `/proc/PID/environ` is readable on Linux; `KERN_PROCARGS2` / `ps -E` exposes it on macOS.
  - An inherited memfd or file descriptor on Linux: a peer can reopen it through `/proc/PID/fd/N`. On macOS an inherited fd is private.

**The note's three options**

| Option | How | Pros | Cons |
|---|---|---|---|
| 1. Launcher as broker, authorizing by ancestry (the note's recommendation) | The launcher holds the decrypted key in memory and listens on a unix socket. It exports only the socket path, which is not a secret. It answers only if the peer is its own descendant. Linux: `SO_PEERPIDFD`, then walk the parent chain, or run the agent in a `systemd-run --user --scope` and compare cgroups. macOS: `LOCAL_PEERTOKEN`, then walk parents. | Works with unmodified agents. The key never enters the agent's process tree. Lifetime equals the launcher's. Per-secret audit, rate limiting, revocation. Fails closed. | Daemonized children reparent and lose access. Children need a client command, not a raw key. Assumes Yama `ptrace_scope` ≥ 1 on Linux. |
| 2. Ambient kernel-held key, no daemon | Linux: an anonymous session keyring with possessor-only permissions, inherited across fork and exec. macOS: an fd from `shm_open` + `shm_unlink`. | Kernel-enforced "me and my children" on Linux. No moving parts. | Two implementations. The macOS half depends on the agent not closing inherited fds. Docker's default seccomp blocks `keyctl`. No audit or revocation. |
| 3. One UID per agent | Key in a `0400` file owned by the agent's UID; spawn with `sudo -u`. | The only real boundary without a sandbox. | Needs root or a sudoers rule. Worktree ownership, per-UID agent login. Clunky on macOS. |

The note suggests option 1 by default and option 3 when the launcher has the privileges, behind the same client interface.

**What changes when the client is already inside a sandbox**

The note assumes unsandboxed same-UID peers. In ateam the agent is sandboxed, which adds a stronger and simpler tool: **reachability**. A unix socket path that exists only in that sandbox's view cannot be reached by another sandbox, whatever its ancestry. This is what most agent sandboxes actually use (see below). Ancestry then covers the remaining case: unsandboxed processes of the same user.

**Primitives, verified**

Linux:

| Item | Finding |
|---|---|
| `SO_PEERPIDFD` | Kernel 6.5. Returns a pidfd for the peer, so no PID-reuse race. Available in `golang.org/x/sys/unix`. ([socket.h](https://raw.githubusercontent.com/torvalds/linux/v6.5/include/uapi/asm-generic/socket.h)) |
| `SO_PEERCRED` | Credentials at `connect` time, as a plain PID. Any later `/proc/PID` lookup is racy. ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html)) |
| Peer cgroup | No `SO_PEERCGROUPID` exists. `PIDFD_GET_INFO` (kernel 6.13) returns `cgroupid` and `ppid` from a pidfd, without `/proc`. ([pidfd.h](https://raw.githubusercontent.com/torvalds/linux/master/include/uapi/linux/pidfd.h)) |
| `/proc/PID/environ` | **The note holds.** Gated by ptrace read mode; Yama only acts on attach. Same-UID reads work under `ptrace_scope` 1. ([proc_pid_environ(5)](https://man7.org/linux/man-pages/man5/proc_pid_environ.5.html), [yama_lsm.c](https://raw.githubusercontent.com/torvalds/linux/master/security/yama/yama_lsm.c)) |
| What does block it | A non-dumpable target; a Landlock domain on the reader; a PID namespace with a fresh `/proc`. |
| `/proc/PID/fd/N` | Also read mode, so Yama does not block it. Inference: this applies to files, memfds and pipes. A socket cannot be reopened with `open()`; stealing one needs `pidfd_getfd`, which Yama scope 1 blocks for non-ancestors. |
| Landlock and unix sockets | Abstract sockets and signals: scoped from ABI 6 (kernel 6.12). Pathname sockets: `LANDLOCK_ACCESS_FS_RESOLVE_UNIX`, ABI 9 (kernel 7.1). Before that Landlock cannot restrict connecting to a pathname socket. ([landlock.h](https://raw.githubusercontent.com/torvalds/linux/master/include/uapi/linux/landlock.h)) |
| cgroup as identity | Survives double-fork. Inference: in a `systemd-run --user --scope` subtree the user owns `cgroup.procs`, so an unsandboxed same-UID process can move itself in. A sandbox with no writable cgroupfs cannot. |
| Session keyring | Inherited across fork, preserved across exec. Use an anonymous keyring: a named one can be joined by name. ([session-keyring(7)](https://man7.org/linux/man-pages/man7/session-keyring.7.html)) |

macOS:

| Item | Finding |
|---|---|
| `LOCAL_PEERTOKEN` | Public in `sys/un.h`. Apple DTS: supported on macOS 13+, and "Protecting against PID reuse is one of the primary reasons that audit tokens exist." ([forum thread](https://developer.apple.com/forums/thread/850777)) Usable from Go without cgo through a raw `getsockopt` (inference, not tested). |
| **Correction: not fully race-free** | XNU does not store the token at connect. It looks up the last process that used the peer socket at `getsockopt` time. Inference: a narrow reuse window remains if that process exited. Call it right after `accept`, then re-check liveness. ([uipc_usrreq.c](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/kern/uipc_usrreq.c)) |
| Passed descriptors | Apple DTS: the identity is not trustworthy once the descriptor is passed to another process. |
| Parent chain | `unix.SysctlKinfoProc` gives the parent PID without cgo. The "responsible process" call is not in public headers (UNVERIFIED as usable). |
| `KERN_PROCARGS2` | A same-UID caller gets the environment unless the target is `CS_RESTRICT` with SIP on. **Correction:** the gate is `CS_RESTRICT`, not hardened runtime. Whether hardened runtime implies it is UNVERIFIED. Inference: Node or Bun agents and a Go launcher are readable by unsandboxed same-UID processes. ([kern_sysctl.c](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/kern/kern_sysctl.c)) |
| Seatbelt peer inspection | sandbox-runtime and Codex allow `process-info*` and `signal` only for `(target same-sandbox)`, and do not allow `kern.procargs2`. Inference: a sandboxed agent cannot read other processes' environment. Exact semantics UNVERIFIED. ([Codex policy](https://raw.githubusercontent.com/openai/codex/main/codex-rs/sandboxing/src/seatbelt_base_policy.sbpl)) |
| Seatbelt socket reachability | sandbox-runtime emits `(allow network-outbound (remote unix-socket (subpath ...)))` per allowed path under a deny default. |
| `es_new_descendants_client` | Endpoint Security client for a process's descendants, in the macOS 27 beta per Apple DTS. Entitlement terms not confirmed. A possible replacement for parent walking. ([thread](https://developer.apple.com/forums/thread/842442)) |

Inherited descriptors, tested by the research agent on macOS (Node 24, Bun 1.4.2) and Linux (Node 22, Bun 1.4.5):

- The agent process keeps an inherited fd 3. **The tools it spawns do not get it.** Node's default for fd 3 and above is `'ignore'`, and libuv sets close-on-exec by default on macOS.
- bubblewrap passes extra fds to the command. sandbox-exec: UNVERIFIED.
- So a pre-connected socketpair reaches the agent but not `git`, `curl` or `npm` run by it. It needs a shim inside the sandbox.

**Projects that authorize by kernel-attested identity**

| Project | Mechanism | Attests | Platforms | Weaknesses it documents |
|---|---|---|---|---|
| [SPIRE Workload API](https://raw.githubusercontent.com/spiffe/spire/main/doc/plugin_agent_workloadattestor_unix.md) | `SO_PEERCRED` / `LOCAL_PEERPID`, then process lookup. PID reuse handled by holding a `/proc/PID` handle and comparing start time (Linux), kqueue exit notification (macOS). | uid, gid, optional binary path and sha256 | Linux, macOS, BSD | Its own comments call the macOS check "a blind attempt". Does not use `SO_PEERPIDFD` or `LOCAL_PEERTOKEN`. |
| SPIRE docker / k8s / systemd attestors | Peer PID → cgroup path → container, pod or unit | cgroup membership | Linux | "cgroup membership is an authoritative identity signal only against unprivileged workloads." |
| [agent-box Portal](https://raw.githubusercontent.com/0xferrous/agent-box/main/docs/src/explanation/rationale/adr/0001-agent-portal.md) | `SO_PEERCRED`, then PID and namespace/cgroup → Podman container. Per-container policy. | Container identity | Linux | "Identity resolution from PID/ns/cgroup to Podman ID must be robust." **The closest match to the note's option 1, for an agent sandbox.** |
| [NVIDIA OpenShell](https://docs.nvidia.com/openshell/latest/security/best-practices.md) | The proxy binds each connection to the executable or an ancestor executable; hash trusted on first use | Binary path and hash, ancestry | Linux | Broad globs "allow any leaf or ancestor executable to authorize". |
| [1Password CLI](https://www.1password.dev/cli/app-integration-security/) | macOS: XPC with a code-signature check. Linux: socket plus group check. Session keyed by terminal and start time. | Signed binary, terminal session | macOS, Linux, Windows | Root can bypass; accessibility-enabled apps may get around the prompt. |
| [1Password SSH agent](https://www.1password.dev/ssh/agent/security/) | Prompt names the requesting process; approval per application or terminal tab | Requesting application | all | How the process is identified is not documented. |
| [Secretive](https://github.com/maxgoedjen/secretive) | Walks parents up to a GUI app, then checks its code signature | Ancestry, signature | macOS | PID based. Used to display provenance. |
| [tailscaled LocalAPI](https://raw.githubusercontent.com/tailscale/tailscale/main/ipn/ipnserver/actor.go) | `SO_PEERCRED`; `LOCAL_PEERCRED` + `LOCAL_PEERPID` on macOS | uid, pid | all | A source TODO notes PID reuse is not handled. |
| OpenSSH ssh-agent | `getpeereid` | uid only | Unix | No per-process scoping. |
| [systemd credentials](https://systemd.io/CREDENTIALS/) | A per-unit read-only directory in non-swappable memory | Unit membership | Linux | Invisible to other services only "if the service is using file system namespacing". Inference: a `--user` unit without that is readable by same-UID processes. |

- Code-signature attestation tells *which binary*, not *which instance*. Two sandboxes running the same agent binary are indistinguishable (inference).
- **No project was found that uses `SO_PEERPIDFD` or `LOCAL_PEERTOKEN` for ancestry-based authorization.** The note's option 1 is sound but has no direct precedent with the modern primitives.

**What agent sandboxes do instead: reachability plus a bearer token**

- **Anthropic sandbox-runtime.** Linux: no network namespace route; proxies on unix sockets "bind-mounted into the sandbox". macOS: Seatbelt allows only `localhost:<proxyPort>`, plus a per-session token. On per-command ids: "this is attribution, not a boundary between concurrent commands of one session."
- **nono.** A 256-bit session token in the environment, proxy on loopback. Its docs note that "`/proc/PID/environ` is readable by same-user processes".
- **Docker Sandboxes.** The microVM's network is the binding.
- **aws-vault `--ecs-server`.** Random port plus a token "only exposed to the subprocess via environment variables". Its `--ec2-server` mode has no scoping at all.
- **Nomad Task API.** A socket in the task's secrets directory that "always requires authentication".
- **AWS IMDSv2.** A hop limit of 1: network distance as the credential.
- `op run`, `doppler run`, envchain, teller: env injection into a child (UNVERIFIED this pass). That is the weakest row below.

**Comparison**

| Approach | Kernel-enforced | Stops an unsandboxed same-UID peer | Stops another sandbox | Survives daemonization | macOS | Linux | Unmodified agents | Audit / revocation |
|---|---|---|---|---|---|---|---|---|
| Bearer token in env | No | No | Yes, if the sandbox blocks process inspection | Yes | Yes | Yes | Yes | Per token |
| Socket path reachability | Yes | No | Yes | Yes | Yes (Seatbelt) | Yes (bwrap; Landlock from 7.1) | Yes | Close the listener |
| Ancestry check | Identity yes; the walk is userspace | Yes | Yes | No | Yes | Yes (6.5+) | Yes | Per connection |
| cgroup / scope check | Yes | No, if it can write `cgroup.procs` | Yes | Yes | No | Yes | Yes | Per scope |
| Session keyring | Yes | Yes (inference) | Yes | Yes | No | Yes | No | Revoke the key |
| Inherited fd | Yes | Yes (Yama scope 1) | Yes | Only if kept | Yes | Yes | No (dropped for spawned tools) | Close the fd |
| Network namespace | Yes | Yes | Yes | Yes | No | Yes | Yes | Per namespace |
| Per-UID | Yes | Yes | Yes | Yes | Needs admin | Needs root | Yes | Per UID |
| Code signature / binary hash | Yes | Only against other binaries | No | Yes | Yes | Partial | Yes | Per binary |

No single check stops both other sandboxes and unsandboxed same-UID peers and survives daemonization, short of per-UID. The checks compose:

- **Reachability** stops other sandboxes.
- **The peer check** (ancestry or cgroup) stops stray unsandboxed same-UID processes and gives a per-connection audit record.
- **A token** covers a misconfiguration of either and costs nothing.

**Recommendation**

Linux:

1. Reachability as the base: a per-sandbox socket directory bind-mounted by bubblewrap, with a PID namespace and a fresh `/proc`. In Landlock-only mode, use `LANDLOCK_ACCESS_FS_RESOLVE_UNIX` when the ABI is 9 or more, and always set both scope flags.
2. Peer check on accept: `SO_PEERPIDFD`, then the cgroup id (`PIDFD_GET_INFO` on 6.13+, `/proc/PID/cgroup` validated against the pidfd otherwise). Prefer cgroup to a parent walk: it survives double-fork.
3. A per-session token.
4. Below 6.5: `SO_PEERCRED` plus start time, as SPIRE does.

macOS:

1. Reachability through the Seatbelt profile: allow outbound only to that sandbox's socket path or port. Keep process inspection at `same-sandbox`.
2. `LOCAL_PEERTOKEN` right after accept, then a parent walk up to the PID the launcher spawned. For reparented daemons: fail closed, or accept on reachability plus token only.
3. A per-session token.

Consequences for the capabilities in this document:

- A unix socket is the better endpoint than a loopback port wherever the client can use one: the ssh-agent socket and the SSH `ProxyCommand` helper (§5), the browser MCP shim (§6). All three then get the peer check.
- HTTP clients need a TCP port in `HTTPS_PROXY`. There the peer check is not available, so the proxy relies on the sandbox rule plus the token (§3.1), or on a small TCP-to-unix shim inside the sandbox.
- The limit to state in any design: an unsandboxed same-UID attacker can still modify the launcher's config, `PATH` or binary. Only per-UID addresses that.

### 3.3 The socket scheme, and a weaker file variant

Added 2026-11-22. §3.2 lists the checks; this section states the resulting scheme on its own. Not tested locally.

**Principle: pass no secret.** Any bearer value the sandboxed processes can read, from an env var or a file, is also readable by an unsandboxed process of the same user. So the sandbox gets a unix socket *path*, which is not a secret, and the launcher decides on `accept` who is connecting. The socket is then the handle to a per-sandbox store that lives only in the launcher's memory.

**The scheme**

1. The launcher creates a unix socket in a per-sandbox directory and exports its path.
2. The sandbox profile lets only that sandbox reach the path. This stops other sandboxes.
3. On each `accept` the launcher asks the kernel who the peer is, and checks that it descends from the sandbox process it spawned. This stops unsandboxed same-user processes.
   - Linux: `SO_PEERPIDFD` (kernel 6.5+), then walk parents.
   - macOS: `LOCAL_PEERTOKEN` right after `accept`, then walk parents.
4. Anything else is refused. Every decision is logged.

Properties:

- Works with unmodified agents and with the tools they spawn.
- Nothing leaks through `environ`, `ps` or a file.
- Secrets for sandbox A exist only in the listener for sandbox A. Lifetime equals the launcher's.
- One socket can serve several things: secret lookups, the ssh-agent protocol, the SSH `ProxyCommand` helper, the browser MCP shim.

Limits:

| Limit | Detail | Options |
|---|---|---|
| Daemonized children | A double-forked process is reparented and fails the ancestry check. Fails closed. | On Linux a cgroup check survives it, but a same-user process can move itself into a user-owned cgroup, so it is weaker. |
| HTTP proxies | `HTTPS_PROXY` needs a TCP port. A loopback port has no peer identity. | (a) Port rule plus token, accepting that an unsandboxed same-user process can read the token from the environment. (b) A network namespace on Linux (bubblewrap, Docker): the port exists only inside the sandbox, no token needed. Seatbelt and Landlock-only modes have no equivalent. (c) Find which descendant owns the connecting source port. Feasible in principle, untested, more code. |
| Debugger and swap | Out of scope here by decision. | — |

So the SSH and browser capabilities get the full guarantee. The HTTP proxy gets it only with namespaces.

**Weaker variant: a file only that sandbox can read**

The launcher creates the sandbox, then writes a secret to a file that the profiles of all other sandboxes exclude.

| | Socket scheme | Sandbox-scoped file |
|---|---|---|
| Stops another sandbox | Yes | Yes |
| Stops an unsandboxed same-user process | Yes | **No** |
| Needs a client command or protocol | Yes | No: any tool can read a file |
| Secret at rest | Never | On disk for the sandbox's lifetime, unless on a memory filesystem |
| Audit, revocation | Per connection | None; delete the file |
| Depends on | One profile and one peer check | Every other sandbox's profile excluding the path |

- It keeps a secret between sandboxes, not from the same user.
- Its use: tools that can only take a credential from a file or a variable, and the token of the HTTP proxy in option (a) above. That token is the same class of secret.
- It fails open: a sandbox profile that forgets the exclusion can read it. The implicit protection of ateam's files in the plan is the natural place for that rule, so it applies to every profile without being listed.

**Trust in the configuration**

The scheme assumes the launcher and its policy were not altered by a same-user process. Signing the configuration detects tampering under these conditions:

- **Signing key:** secrecy. Hardware bound and gated by Touch ID, so out of reach of any non-interactive process.
- **Verification key:** public, but it needs integrity. If it can be replaced, an attacker signs their own configuration. It belongs in the binary or in a root-owned file, not under `~/.ateam/`.
- **Binary:** root owned.
- **Rollback:** an older signed configuration stays valid. A version counter kept outside user-writable space, or an expiry, closes that.
- **Coverage:** the signature must span everything that shapes the policy: every config layer, filter files, profiles, the project's `ateam.hcl`. One unsigned include is enough to widen access.
- **What the launcher executes:** `nono`, the agent binary, anything found through `PATH`. These need absolute root-owned paths or a pinned hash, or a fake binary earlier in `PATH` takes over the sandbox's job.

Verification needs no Touch ID, so unattended runs are unaffected. Only configuration changes prompt.

### 3.4 Identifying a sandboxed peer, and bootstrap tokens

Added 2026-11-22. **Nothing in this section was verified.** The macOS part is from memory; a local test of `sandbox_check` was not run. Treat every claim as UNVERIFIED until tested.

The underlying problem is building a set of processes that trust each other, so secrets can be delegated to them. The goal is to fail closed: a process that is not properly sandboxed gets nothing.

**Can the launcher tell whether a peer is sandboxed?**

| Platform | Answer | How |
|---|---|---|
| macOS, Seatbelt | Yes, and it can probe the policy | `sandbox_check(pid, operation, ...)` asks the kernel whether that process would be allowed an operation. With no operation it reports whether the process is sandboxed at all. A variant takes an audit token, which avoids PID reuse. Private interface: undocumented, may change, needs a dynamic library call from Go. |
| Linux, bubblewrap | Yes, indirectly | No "sandboxed" flag. `/proc/PID/ns/*` gives the namespaces, `/proc/PID/status` gives `NoNewPrivs` and `Seccomp`, `/proc/PID/mountinfo` gives the filesystem view. Open these relative to a validated pidfd. |
| Linux, Landlock only | No, as far as known | Another process's Landlock domain is not exposed. |

**Is there an OS-level identity for a process and all its children?**

Several exist. The unprivileged ones are weaker.

| Identity | Inherited | Can a same-user outsider join? | Needs root to set up | Readable for a socket peer |
|---|---|---|---|---|
| The sandbox itself (Seatbelt, Landlock, seccomp) | Yes; cannot be removed | No | No | macOS: by probing. Landlock: no. |
| Linux PID / mount namespace | Yes | Hard, but possible for the owning user | No | Yes, through `/proc/PID/ns` |
| Linux cgroup | Yes | Yes, if the user owns it | Only for a root-owned one | Yes |
| SELinux / AppArmor label | Yes | No | Yes | Yes: `SO_PEERSEC` returns the peer's label |
| Separate UID | Yes | No | Yes | Yes |

- The label row is the long-standing answer on Linux, and what Android relies on. It needs root to install policy.
- macOS has no public equivalent. Code signatures identify a binary, not an instance.

**Is the sandbox's configuration needed to trust it?**

No, for two reasons:

- **The launcher applied it.** Sandboxes are inherited and cannot be loosened. "Descends from the process I sandboxed" proves membership and configuration together. A check that passes stays true for the life of the process.
- **Behavior can be probed instead of reading configuration.** On macOS: ask whether the peer may read a canary path such as `~/.ssh`. An unsandboxed process may, so it fails. This tests the effective policy, which is better than parsing a profile.

**Instance probe: which sandbox is this peer in?**

This closes the daemonized-children gap of §3.3, where a reparented process fails the ancestry check although it is still sandboxed.

| Platform | Probe |
|---|---|
| macOS | Give each sandbox one unique allowed path (a marker). A peer is in sandbox A if it may read A's marker **and** may not read the canary. An unsandboxed process fails the canary. Another sandbox fails the marker. Our own construction on a private interface. |
| Linux, bubblewrap | Compare the peer's namespace IDs with the sandbox's. |
| Linux, Landlock only | None known. Ancestry or cgroup only. |

**The gate on `accept`**

1. Kernel peer identity: pidfd on Linux, audit token on macOS.
2. Membership: the peer descends from the sandbox's root process, **or** it passes the instance probe.
3. Otherwise refuse.

**One-time bootstrap token**

An alternative considered: pass a single-use token by env var or by a file unlinked on first read. A daemon inside the sandbox exchanges it for a long-lived token and talks outward on the sandbox's behalf.

- What it gives: a tamper-evident bootstrap. A stolen token is used twice, the second exchange fails, and the launcher aborts and discards the secrets. The long-lived token lives only in the daemon's memory. Vault's response wrapping is the same idea.
- Why it does not solve the problem: the tools in the sandbox still reach that daemon over a socket or a port, and an unsandboxed same-user process can connect to it too. The daemon needs reachability or the peer check anyway. The identity question has moved one hop inward.
- Theft is detected late: the thief holds a valid token until the legitimate daemon tries the used one.
- Not needed for a daemon ateam starts itself: an inherited socketpair is private to that process and has no race window. The inheritance problem of §3.2 concerns tools spawned by Node or Bun, not ateam's own first process in the sandbox.
- Still useful where no descriptor can be passed: `docker exec` into an existing container, or a remote machine.

**To test before relying on any of this**

1. `sandbox_check` on another process of the same user: sandboxed flag, and a per-path read probe.
2. The audit-token variant, called from Go without cgo.
3. The marker-plus-canary probe across two Seatbelt sandboxes and one unsandboxed process.
4. Whether nono's Seatbelt profile can carry a per-sandbox marker path.
5. Namespace comparison through a pidfd under bubblewrap.

## 4. Secret-injecting egress proxy

### 4.1 Projects not covered in `ResearchSandboxing.md`

Closest matches:

| Project | Language, license | Stars, latest | Secrets at rest | Destination binding | Injection points | Client identity | TLS interception | Go import | macOS |
|---|---|---|---|---|---|---|---|---|---|
| [superfly/tokenizer](https://github.com/superfly/tokenizer) | Go, Apache-2.0 | 509; no releases; pushed 2026-11-18 | None on the proxy. The client holds secrets sealed (NaCl sealed box) to the proxy's public key. Private key from an env var. | `allowed_hosts` inside the sealed blob | Header, query, HMAC of body, SigV4, JWT bearer, OAuth2 client credentials, GitHub App token | `Proxy-Authorization` checked against a digest inside the sealed blob; macaroons | No. Rejects CONNECT. The client sends plain HTTP to the proxy, which upgrades to HTTPS. | Yes (`NewTokenizer`) | UNVERIFIED |
| [Infisical/agent-vault](https://github.com/Infisical/agent-vault) | Go, MIT plus `ee/` | 2521; v0.40.2 (2026-10-22) | AES-256-GCM. Random data key wrapped by a key derived with Argon2id from a master password. Passwordless mode stores the data key in plaintext. SQLite or Postgres. | Service rules per host; default-deny option | Header (placeholder swap or replace) | Per-agent token in `Proxy-Authorization`, stored hashed | Yes. CA key encrypted with the data key. | Not documented | Yes |
| [paradigmxyz/iron-proxy](https://github.com/paradigmxyz/iron-proxy) | Go, Apache-2.0 | 876; v0.59.0 (2026-11-19) | None. Sources: env, file, AWS SM/SSM, Vault KV, 1Password. | Per-secret rules: host, methods, paths | Header, query, path, body. `require: true` returns 403 without the token. | None. One proxy per sandbox by design. | Yes. You supply the CA. | No (`internal/`) | Binaries exist; nftables/TPROXY parts are Linux only |
| [onecli/onecli](https://github.com/onecli/onecli) | Rust + TypeScript, Apache-2.0 plus `ee/` | 3651; v2.20.0 (2026-11-09) | "AES-256-GCM at rest". Key source not stated. | Host and path pattern | Header, query | Per-agent token in `Proxy-Authorization` | Yes | No. Multi-service, needs Postgres. | UNVERIFIED |
| [dtkav/agent-creds](https://github.com/dtkav/agent-creds) | Go + JS, MIT | 32; pushed 2026-08-29 | SOPS file; age identity in the system keychain | Macaroon caveats: host, method, path, validity | Bearer, header, basic, OAuth2, SigV4 | Macaroon per sandbox, plus one Envoy and netns per sandbox | Yes (Envoy) | No | Docker based; UNVERIFIED |
| [yagop/sandbox](https://github.com/yagop/sandbox) | Go stdlib only, no license | 4; pushed 2026-09-18 | None. Values from host env or a command. | Host, with wildcard | Bearer, basic, custom header | None | Yes, only for hosts with a rule. ALPN pinned to `http/1.1`. | Not documented | Docker |

Notes:

- agent-vault's README says the repository is "no longer the recommended way to run Agent Vault" and points to the Infisical platform. Treat it as reference code, not a dependency.
- agent-vault keeps secrets as `[]byte`, never Go strings, and calls wiping best-effort.
- No project above has all three of: encryption at rest, client identity, importable Go package.

Candidates that do not fit:

| Project | Finding |
|---|---|
| [stripe/smokescreen](https://github.com/stripe/smokescreen) | Go, MIT, v0.1.0 (2026-09-21). CONNECT proxy with per-role ACL and private-IP blocking. Embeddable (`pkg/smokescreen`). A `mitm: add_headers` option is mentioned; how it works is UNVERIFIED. Good allowlist and SSRF reference, not a secret store. |
| [cyberark/secretless-broker](https://github.com/cyberark/secretless-broker) | Go, Apache-2.0, v2.0.0. HTTP connectors for Basic, Conjur, AWS. Grants a session to "any client that can open a TCP connection to its listen port". HTTPS interception UNVERIFIED. |
| [pipelab-org/pipelock](https://github.com/pipelab-org/pipelock) | Go. Scans, blocks and redacts. No injection: the agent holds the secrets. |
| [agentgateway](https://github.com/agentgateway/agentgateway) | Rust. Route-based gateway with backend auth. Forward-proxy support UNVERIFIED. |
| [Envoy credential injector](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/credential_injector_filter) | HTTP filter; Envoy must terminate TLS. Not embeddable in Go. |
| [mitmproxy](https://github.com/mitmproxy/mitmproxy) | Python. Addons can inject. Needs a Python runtime, which breaks single-binary distribution. |
| [Tailscale Aperture](https://tailscale.com/docs/features/aperture) | Hosted LLM gateway. Not a local component. |
| [HashiCorp Vault Proxy](https://developer.hashicorp.com/vault/docs/agent-and-proxy/proxy) | A proxy for the Vault API. Does not inject into third-party APIs. |
| [Pomerium](https://github.com/pomerium/pomerium) | Identity-aware reverse proxy. Not a forward proxy. |
| Cloudflare Sandbox outbound Workers | Injection for Cloudflare-hosted sandboxes. Platform bound. |

Others listed by [awesome-agent-runtime-security](https://github.com/bureado/awesome-agent-runtime-security), metadata only: [matchlock](https://github.com/jingkaihe/matchlock) (Go, `--secret NAME@host`, microVM), [clawpatrol](https://github.com/denoland/clawpatrol) (Go, HCL rules, CEL conditions), [warden](https://github.com/stephnangue/warden) (Go, SPIFFE identity), [wardgate](https://github.com/wardgate/wardgate) (Go, AGPL), [leash](https://github.com/strongdm/leash) (Go, Cedar policy), [authproxy](https://github.com/rmorlok/authproxy) (Go, OAuth refresh).

### 4.2 Gaps closed on tools already reviewed

**nono** (v0.82.0, 2026-11-14, Apache-2.0):

- The proxy runs standalone: `nono proxy` prints a URL with the credential as userinfo (`http://nono:<token>@127.0.0.1:8080`). ([networking](https://nono.sh/docs/cli/features/networking))
- It is a separate crate, [`nono-proxy`](https://docs.rs/crate/nono-proxy/latest). Rust only; C and Python bindings exist, no Go binding.
- No store of its own. Sources: macOS Keychain, Linux Secret Service, `op://`, `bw://`, `env://`, `file://`, `cmd://`. Loaded once at proxy start and held in zeroizing memory. ([credentials](https://nono.sh/docs/cli/features/credential-injection))
- Client authentication: a 256-bit random token per session. `Proxy-Authorization` for CONNECT; the phantom token in the normal credential header for reverse-proxy routes.
- The docs disagree on the token's name (auth header, `X-Nono-Token`, `NONO_PROXY_TOKEN`).
- TLS interception only for routes that need L7 visibility, with an ephemeral CA per session. `--trust-proxy-ca` adds the CA to the macOS user trust store.

**runclaude `runproxy`** (no tags, Apache-2.0, pushed 2026-11-11):

- Runs standalone. The README link is broken; the real path is [`cmd/runclaude-proxy/README.md`](https://github.com/hanwen/runclaude/blob/main/cmd/runclaude-proxy/README.md).
- No store of its own. Reads `~/.claude/.credentials.json` or `ANTHROPIC_API_KEY`; Bedrock uses the AWS SDK chain. Refreshed tokens are written back.
- **No client authentication.** README: "anything that can connect to its listen address can exfiltrate them through it". Protection is the bind address, netns or a Docker internal network.
- Separable Go packages `proxy` and `creds`. README: "embedding the MITM into another tool is just a matter of constructing a `proxy.Setup` and calling `proxy.Serve`".
- Only Anthropic and Bedrock injection is built in. No generic secret configuration.
- `ResearchSandboxing.md` calls it the most actionable artifact. That still holds as reference code. As a dependency it has no tags and 3 stars.

**Docker Sandboxes (`sbx`)**, docs read 2026-11-22. Updates `ResearchSandboxing.md` §E.13.6 and §F.5, which describe the older `docker sandbox network proxy` commands.

- Default is now deny: "All outbound TCP traffic, including HTTP, HTTPS, and SSH, is blocked unless an explicit rule allows the destination." ([defaults](https://docs.docker.com/ai/sandboxes/security/defaults/))
- Presets: `allow-all`, `balanced` (deny plus a baseline allowlist), `deny-all`. Unmatched requests prompt for approval. ([policy](https://docs.docker.com/ai/sandboxes/security/policy/))
- Rules: host with wildcards, IP and CIDR, port (`myhost:22`), and HTTP `--method`, `--path`, `--query`. Deny rules can narrow an allowed host. Global by default; `--sandbox <name>` scopes a rule. ([network rules](https://docs.docker.com/ai/governance/local/network-rules/))
- Non-HTTP: all TCP, including SSH, goes through the host proxy and is checked. UDP is off by default (experimental flag). DNS uses an internal resolver that enforces policy. ICMP is blocked.
- **Both proxy modes:** "Agents use a forward proxy for HTTP and HTTPS; other TCP traffic is forwarded transparently." ([architecture](https://docs.docker.com/ai/sandboxes/architecture/)) Because the microVM has no other route, a client that ignores the proxy variables is still filtered. Only the forward proxy injects credentials.
- **TLS interception is not documented** in the current pages: no CA, trust store, bypass list or pinning guidance. The pages refer to "connections the proxy doesn't decrypt" and to the proxy overwriting the auth header, which implies decryption for some traffic. The older docs (§E.13.6) had `--bypass-host` for pinned services.
- Secret injection: built-in services (anthropic, openai, github, google, groq, mistral, openrouter, xai and others). Custom secrets are experimental: `sbx secret set-custom --host <host> --env <VAR>`, with wildcards, and values from stdin, a 1Password or AWS reference, or a command. ([credentials](https://docs.docker.com/ai/sandboxes/security/credentials/))
- The sandbox sees `proxy-managed` or `sbx-cs-<rand>`. "Replacement applies only to HTTP request headers, not request bodies."
- Storage: macOS Keychain, Windows Credential Manager, Linux Secret Service, or a 0700 file without a keyring. Global by default, `--sandbox` to scope; sandbox-scoped values win.
- SSH: host ssh-agent forwarding is on by default.
- Known limit: "a denied host that shares an IP address with an allowed host can still be reached".
- Not standalone: no documented way to use the proxy or the secret layer without the microVM. License not stated. `sbx login` is required. The CLI and local compute are free; organization policy and audit logs are paid.
- Platforms: macOS 14+ on Apple silicon, Windows 11, Ubuntu 24.04+ with KVM.

Against the requirements of §1: R1 yes, including L7 rules. R2 through the OS keychain. R3 through `--sandbox` scoping, with the VM as the binding. R4 only as agent forwarding plus a port rule, with no command control. R5 and R6 not covered. It is a backend ateam could drive, not a component it can embed.

### 4.3 Encryption at rest for a Go CLI

| Option | cgo | Unattended use | What protects the key |
|---|---|---|---|
| [`filippo.io/age`](https://pkg.go.dev/filippo.io/age), passphrase | No | Needs the passphrase from a TTY, env or file | Passphrase strength |
| age, X25519 identity file | No | Works | File permissions. Moves the problem to where the identity lives. |
| age + [age-plugin-se](https://github.com/remko/age-plugin-se) | No cgo; needs the plugin binary | `--access-control none` avoids prompts; the default prompts Touch ID | Secure Enclave key, bound to the machine. macOS 14+. |
| SOPS | Go library (UNVERIFIED) | Depends on backend | Backend key. Partial-file encryption is not needed for one blob. |
| macOS Keychain via `/usr/bin/security` ([zalando/go-keyring](https://github.com/zalando/go-keyring)) | No | Works when the login keychain is unlocked. Over SSH or launchd it may be locked (UNVERIFIED). | Login keychain. The ACL is tied to `/usr/bin/security`, so any same-user process can read the item the same way. |
| macOS Keychain via Security.framework ([keybase/go-keychain](https://github.com/keybase/go-keychain)) | Yes | Same | A per-application ACL becomes possible if the binary is code-signed. |
| macOS Keychain via purego bindings | No | Same | Same as the cgo path. Small projects; maturity UNVERIFIED. |
| [99designs/keyring](https://github.com/99designs/keyring) | Keychain backend needs cgo | File backend prompts | Depends on backend |
| Linux Secret Service (D-Bus) | No | Fails without a session bus and an unlocked collection | User login password |
| Linux kernel keyring (keyctl) | No | Works | Kernel memory. Not persistent across reboot. A session cache, not storage. |
| [systemd-creds](https://www.freedesktop.org/software/systemd/man/latest/systemd-creds.html) | No (shell out) | Works | TPM2 and/or a root-only host secret. Linux only. |

Practical shape, matching agent-vault's split between a data key and a key-encryption key:

- One age-encrypted file per secret set.
- The file key is unlocked at ateam start from the OS keychain, then a passphrase (prompt, env or file) for headless runs.
- No cgo.

Note on the keychain: on macOS without a signed binary and the cgo path, the keychain protects against other users and offline disk access, not against other processes of the same user. A sandbox that closes the keychain to the agent is what protects it from the agent.

In-memory protection:

- [awnumar/memguard](https://github.com/awnumar/memguard): mlock, guard pages, encryption of idle buffers. Its README calls the API experimental.
- The secret must become a plain header value on every request, so it passes through `net/http` buffers that memguard does not control.
- Assessment (ours): against a same-user attacker who can ptrace or read the keychain, memguard adds little. The cheap measures that close real leaks:
  - keep secrets as `[]byte`, never log them;
  - disable core dumps (`RLIMIT_CORE=0`; `PR_SET_DUMPABLE=0` on Linux);
  - never pass secrets to the child through env or argv;
  - keep the proxy outside the sandbox.

### 4.4 Client compatibility: proxy variable and custom CA

| Client | Proxy | Custom CA | Source |
|---|---|---|---|
| Claude Code | `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`. Basic auth in the URL is supported. No SOCKS. | `NODE_EXTRA_CA_CERTS`. Also trusts the OS store by default. | [network-config](https://code.claude.com/docs/en/network-config) |
| Codex CLI | Standard variables per third-party guides (UNVERIFIED) | `CODEX_CA_CERTIFICATE`, then `SSL_CERT_FILE`. Applies to HTTPS and WebSocket clients. | [env vars](https://learn.chatgpt.com/docs/config-file/environment-variables) |
| Node `fetch`, `http`, `https` | Needs `NODE_USE_ENV_PROXY=1` (v22.21.0 / v24.5.0+) | `NODE_EXTRA_CA_CERTS` | [Node docs](https://nodejs.org/en/learn/http/enterprise-network-configuration) |
| Bun | Reads the standard variables on each fetch | Whether it honors `NODE_EXTRA_CA_CERTS` is UNVERIFIED | [Bun fetch](https://bun.com/docs/runtime/networking/fetch) |
| Python requests / httpx | Standard variables (UNVERIFIED this pass) | `REQUESTS_CA_BUNDLE`; `SSL_CERT_FILE` | — |
| pip | Standard variables | `PIP_CERT`, `REQUESTS_CA_BUNDLE` | [pip docs](https://pip.pypa.io/en/stable/topics/https-certificates/) |
| uv | Standard variables | `SSL_CERT_FILE` replaces the default roots | [uv docs](https://docs.astral.sh/uv/concepts/authentication/certificates/) |
| Go programs on macOS | `HTTPS_PROXY` | Go 1.27+ honors `SSL_CERT_FILE` on macOS. Binaries built with Go 1.26 or older ignore it and use the system trust store. | [Go 1.27 notes](https://go.dev/doc/go1.27) |
| git | `http.proxy`, else `https_proxy` | `GIT_SSL_CAINFO` (UNVERIFIED this pass) | — |
| curl | Standard variables | `CURL_CA_BUNDLE`, `SSL_CERT_FILE`. On macOS `--cacert` skips the system trust unless `--ca-native` is added. | [curl docs](https://curl.se/docs/sslcerts.html) |
| cargo | `HTTPS_PROXY`, `CARGO_HTTP_PROXY` | `CARGO_HTTP_CAINFO` | [cargo config](https://doc.rust-lang.org/cargo/reference/config.html) |

Consequences:

- Environment for the child: `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`, `NODE_USE_ENV_PROXY=1`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, `CODEX_CA_CERTIFICATE`, `CARGO_HTTP_CAINFO`, `PIP_CERT`, `DENO_CERT`.
- `SSL_CERT_FILE` replaces the roots for some clients, so the exported bundle must be the system roots plus the per-run CA. runproxy's `--export-bundle` does this.
- Go tools built before 1.27 on macOS (many `gh` and `terraform` builds) need the CA in the user trust store (nono `--trust-proxy-ca`) or must be left uninjected.
- **Intercept only hosts that have an injection rule. Tunnel the rest unread.** nono, yagop/sandbox and runproxy all do this. It limits CA-trust breakage and pinning failures.

HTTP/2, streaming and WebSocket under interception:

- Pinning ALPN to `http/1.1` on intercepted connections is the simple route and works for SSE (yagop/sandbox).
- SSE needs unbuffered response copying. Body injection or body scanning forces buffering; avoid both on LLM routes.
- Claude Code has stream watchdogs; a buffering proxy will trip them.
- Codex uses WebSockets, so `Upgrade` must pass on intercepted connections.
- gRPC clients need HTTP/2. Tunnel those hosts without interception.

### 4.5 Recommendation for the proxy

1. **Build it in-process in Go.**
   - The core is small: CONNECT handler, host allowlist, per-run CA with a leaf cache, `httputil.ReverseProxy` with a header rewrite. runclaude and yagop/sandbox do it with the standard library.
   - In-process gives one listener per sandbox, secrets that never cross a process boundary, and no second binary.
   - First version: header-only placeholder replacement bound to a host (optionally method and path), default-deny allowlist, per-run token, unread tunnel for other hosts, HTTP/1.1 on intercepted connections with WebSocket upgrade pass-through, a decision log.
   - Keep the explicit base-URL path (`ANTHROPIC_BASE_URL`, `ResearchSandboxing.md` §F.1.1 Tier A) for the LLM key: no CA needed.
   - Later: query placement, SigV4, OAuth refresh.
2. **Embed libraries for narrow parts only:** `filippo.io/age` for the file, a keychain call for the unlock key.
3. **Shelling out is not the default.** iron-proxy (Go) and `nono proxy` (Rust) could be optional backends. Neither encrypts at rest; iron-proxy has no client authentication.

Reference code, in reading order:

1. `hanwen/runclaude` `proxy/` and `creds/`: minimal Go interception, OAuth refresh, SigV4 re-signing.
2. `paradigmxyz/iron-proxy`: secret rule model (host, method, path; `require`), streaming handling.
3. `Infisical/agent-vault`: key wrapping, token hashing, the CA variable list, threat model.
4. `nono-proxy` docs: session token, selective interception, Seatbelt and Landlock binding.
5. `superfly/tokenizer`: processors (HMAC, SigV4, JWT), sealing the destination with the secret.
6. `stripe/smokescreen`: allowlist semantics, private-address blocking.
7. `anthropic-experimental/sandbox-runtime`: proxy username as binding id, unix socket bridge.

## 5. SSH capability per sandbox

### 5.1 The four models

| Model | Restricts host/user | Restricts command | Server change | Key location |
|---|---|---|---|---|
| 1. Filtering SSH agent | Yes | **No** | None | Host-side agent |
| 2. Host-side SSH broker (terminates the session) | Yes | Yes | None | Host side |
| 3. Server-side restriction (forced command, certificates) | By key or certificate | Yes | Yes | Per-sandbox key or short-lived certificate |
| 4. Operation broker (named operations, MCP) | Yes | Yes, by design | None | Host side |

### 5.2 Model 1: filtering SSH agent

OpenSSH destination constraints (`ssh-add -h`, OpenSSH 8.9):

- A constraint binds a key to `[user@]host`, or to a hop. Hosts are identified by their host keys. ([ssh-add(1)](https://man.openbsd.org/ssh-add), [agent-restrict](https://www.openssh.org/agent-restrict.html))
- The client sends `session-bind@openssh.com` to the agent with the host key, the session identifier and the server's signature. ([PROTOCOL.agent](https://raw.githubusercontent.com/openssh/openssh-portable/master/PROTOCOL.agent))
- Stated limits: restrictions apply only to user authentication; confirmation prompts carry no destination.

What an agent filter can know:

- The key, the session, the user name, the server host key.
- **Never the command.** It travels later as a channel request inside the encrypted transport.
- After one allowed signature the client holds a full session: shell, port forwarding, sftp.
- Inference: port forwarding through an allowed host bypasses the sandbox's egress proxy.

Go: `golang.org/x/crypto/ssh/agent` exposes `ExtendedAgent.Extension`, so a custom agent can parse `session-bind` itself. The built-in keyring does not support it.

| Tool | Filters on | Command aware | Status |
|---|---|---|---|
| [ssh-agent-filter](https://sources.debian.org/src/ssh-agent-filter/0.5.3-2/README.md) (C++) | Key allowlist, per-use confirm | No | Packaged in Debian |
| [guardian-agent](https://github.com/StanfordSNR/guardian-agent) (Go) | Server and command; the agent does the handshake | Yes, on OpenSSH servers | Prototype; last push 2023; needs its own client |
| [sshield](https://github.com/gotlougit/sshield) (Rust) | Prompt on key use | No | Experimental |
| [1Password agent](https://www.1password.dev/ssh/agent/security/) | Per key and application | No | Commercial |
| [Secretive](https://github.com/maxgoedjen/secretive) | Secure Enclave keys, Touch ID | No | macOS only |
| [moat `sshagent`](https://pkg.go.dev/github.com/majorcontext/moat/internal/sshagent) (Go) | Key per granted host; the host is named by a wrapper inside the sandbox | No | Internal package |

Verdict: usable only as "full trust in host X as user Y". It needs direct egress to port 22 and gives no command control.

### 5.3 Model 2: host-side SSH broker

Design:

- The sandbox gets a throwaway key and can reach only a local SSH endpoint.
- The endpoint (a Go `x/crypto/ssh` server inside ateam) authenticates that key, checks the requested host, user and `exec` command against policy, then opens the upstream connection with the real key and pipes the session.

Prior art. None documents per-command filtering for arbitrary OpenSSH servers (UNVERIFIED that they cannot; their docs describe none):

| Project | Language | Target addressing | Upstream key | Notes |
|---|---|---|---|---|
| [sshpiper](https://github.com/tg123/sshpiper) | Go | `target+username` or plugins | A separate mapping key re-signs upstream | v1.6.2 (2026-11-20). Closest Go prior art for "two connections". A daemon with plugins, not a library. |
| [Warpgate](https://github.com/warp-tech/warpgate) | Rust | `user:target@gateway` | Its own keys in the target's `authorized_keys` | Recording and audit. |
| [sshportal](https://github.com/moul/sshportal) | Go | User name = host alias | In its database | Built on gliderlabs/ssh. |
| [ContainerSSH sshproxy](https://containerssh.io/v0.6/reference/sshproxy/) | Go | Config or webhook | Key in config; pinned host key fingerprints | Audit log. |
| [Teleport agentless](https://goteleport.com/docs/enroll-resources/server-access/openssh/openssh-agentless/) | Go | Teleport client | Teleport CA; the server must trust it | Server change needed. |
| [Boundary](https://developer.hashicorp.com/boundary/docs/concepts/credential-management) | Go | `boundary connect` | Injected; the user never sees it | Whether injection needs the paid edition is UNVERIFIED. |
| [Tailscale SSH](https://tailscale.com/kb/1193/tailscale-ssh) | Go | Tailnet address | No client keys | Needs tailscaled on the target. ACL has no command rules. |
| [AWS SSM Session Manager](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-restrict-command-access.html) | — | AWS CLI | IAM | Command restriction through documents with `allowedPattern`. Needs the SSM agent. |

Go building blocks:

- `golang.org/x/crypto/ssh`: `NewServerConn` exposes each channel and each request, so the broker can accept or reject per channel. Also `FixedHostKey`, `Certificate`, `SignCert`.
- [gliderlabs/ssh](https://pkg.go.dev/github.com/gliderlabs/ssh): `Session.RawCommand()`, PTY callback, port-forwarding callbacks that deny when nil, subsystem handlers.

Addressing the real host from the sandbox:

- User-name encoding (sshpiper, Warpgate, sshportal). Inference: it breaks `git@github.com:org/repo` remotes, because the URL fixes the user.
- Better: a generated `ssh_config` with `ProxyCommand <helper> %h %p`. The original host name is preserved. The helper dials a per-sandbox unix socket, or sends CONNECT to the egress proxy.
- The generated config also sets a throwaway `IdentityFile`, `IdentitiesOnly yes`, `IdentityAgent none` and a `UserKnownHostsFile` holding the broker's host key.

Host key verification:

- The sandbox can only verify the broker's key.
- The broker must verify the real host against the user's `known_hosts` and resolve names host-side. A broker that skips this silently removes host verification.

git over SSH:

- The server-side commands are `git-upload-pack` (fetch), `git-receive-pack` (push) and `git-upload-archive`. ([git-shell](https://git-scm.com/docs/git-shell))
- Inference: a rule "allow upload-pack on repository X, deny receive-pack" gives fetch-only, and works against hosted forges because only the exec string is inspected.

### 5.4 Model 3: server-side restriction

When the server can be configured:

- `authorized_keys` options ([sshd(8)](https://man.openbsd.org/sshd.8)): `command=` forces a command, with the original in `SSH_ORIGINAL_COMMAND`. `restrict` disables forwarding, PTY and rc files. Also `from=`, `permitopen=`, `expiry-time=`.
- Certificates ([ssh-keygen(1)](https://man.openbsd.org/ssh-keygen)): `force-command`, `source-address`, validity, principals. Go can issue them (`ssh.Certificate`, `SignCert`).
- A local CA per installation needs one server change: trust the CA. Each sandbox then gets a short-lived certificate with its own forced command.
- [step-ca](https://smallstep.com/docs/step-ca/templates/) and [Vault's SSH engine](https://developer.hashicorp.com/vault/api-docs/secret/ssh) issue such certificates too.

Forced-command wrappers:

| Wrapper | Model | Notes |
|---|---|---|
| [rrsync](https://raw.githubusercontent.com/RsyncProject/rsync/master/support/rrsync.1.md) | rsync confined to one directory; read-only or write-only | Warns that the login shell may run rc files before the forced command. |
| [git-shell](https://git-scm.com/docs/git-shell) | Only the three git commands | No read-only mode documented. |
| [sshdo](https://github.com/raforg/sshdo) | Allowlist per user and key, with a training mode | Matching syntax UNVERIFIED. |
| [authprogs](https://github.com/daethnir/authprogs) | Decides on command, key, client address | Rule syntax UNVERIFIED. |
| [lshell](https://github.com/ghantoos/lshell) | Allowed commands and forbidden characters | CVE-2016-6902 and -6903: escapes through parsing and through allowed commands that run others. |

Known bypass classes for command allowlists by string:

- Shell metacharacters when only the first token is checked.
- Allowed commands that run others: `find`, `vim`, `xargs`.
- Pagers: CVE-2017-8386, `git-receive-pack --help` opened `less` under git-shell.
- Option injection: CVE-2017-1000117, a host name starting with a dash became a `ProxyCommand` option.
- scp: CVE-2020-15778, backticks in the destination run on the remote.
- Login-shell rc files run before the forced command.

### 5.5 Model 4: operation broker

| Project | Model | Weakness |
|---|---|---|
| [ShellGuard](https://github.com/fawdyinc/shellguard) (Go, Apache-2.0) | MCP server. Parses the command to an AST with `mvdan.cc/sh`, rejects `;`, redirects and substitution, allowlists commands and flags, re-quotes arguments. | Read-only diagnostics only. Reads `~/.ssh` and the agent itself. |
| [n0madic/ssh-mcp](https://pkg.go.dev/github.com/n0madic/ssh-mcp) (Go) | Host and command allow/deny by regex | README: "PTY sessions bypass the command filter". Includes upload and file-edit tools. |

Shared weaknesses:

- The agent must use a tool instead of `ssh`, so `git`, `rsync` and scripts that call ssh do not work.
- These servers hold or read the real keys themselves.

ShellGuard's parse, allowlist and re-quote pipeline is reusable Go prior art for a "diagnostics" rule type.

### 5.6 Models against real workloads

| Workload | Agent filter | Broker with exec rules | Server forced command | Operation broker |
|---|---|---|---|---|
| git fetch / push | Host only; push not separable | Works; fetch and push separable | Works with git-shell | Not applicable |
| rsync deploy | Host only | Fragile: rsync sends a generated `--server` argument string | Works with rrsync | Works as a named operation |
| One-shot diagnostics (`journalctl -u x`, `docker ps`) | Host only | Works with exact or templated arguments | Works | Works |
| `tail -f`, interactive shell, pagers | Allowed entirely | Deny PTY, or trust the host fully | `no-pty` or full shell | Blocked |
| scp / sftp | Allowed entirely | Per subsystem on or off; sftp gives file write | `restrict` plus forced command | Download-style tools |

- Interactive debugging does not fit a command allowlist. It is either full trust in that host and user, or a curated read-only command set.
- Inference: sftp or rsync write access to a home directory equals command execution, through rc files or `authorized_keys`.

### 5.7 How agent sandboxes handle SSH today

| Product | Verified fact |
|---|---|
| nono | "`git` can call `ssh` under its own policy, while the agent cannot call `ssh` directly." No SSH key brokering or command rules described. ([README](https://raw.githubusercontent.com/nolabs-ai/nono/main/README.md)) |
| Docker Sandboxes | Host ssh-agent forwarding on by default: a signing oracle. Injection is HTTP only. ([docs](https://docs.docker.com/ai/sandboxes/security/credentials/)) |
| Claude Code cloud sessions | No SSH. A git proxy swaps a scoped credential for the real token and rejects branch deletions. ([docs](https://code.claude.com/docs/en/claude-code-on-the-web)) |
| Codex cloud | Secrets available during setup only. SSH handling UNVERIFIED. |
| Dev Containers | Local ssh-agent forwarded automatically. |
| Daytona, E2B | Docs cover SSH into the sandbox only. |

None of them brokers outbound SSH with command rules.

### 5.8 Recommendation for SSH

Ranking:

1. **Host-side broker inside ateam** (model 2). The only option with host, user and command control and no server change.
2. **Per-sandbox certificates with `force-command`** (model 3) where the server can be configured. Defense in depth, not a replacement.
3. **Filtering agent** (model 1) only as a "full trust in host X" mode.
4. **Named operations** (model 4) as a complement for deploy-style tasks.

Minimal version:

- One broker per sandbox, on a per-sandbox unix socket or behind the egress proxy's CONNECT handling for port 22.
- A generated `ssh_config` as in §5.3.
- Accept only `session` channels. On each, accept only an `exec` that matches a rule. Reject `shell`, `pty-req`, `subsystem`, `env`, agent and X11 forwarding, `direct-tcpip` and `tcpip-forward` unless a rule enables them.
- Upstream: dial host-side, verify against the user's `known_hosts`, authenticate through the host's ssh-agent so ateam never reads key files.
- A built-in git rule type: fetch, push, repository list.
- Log every decision.

Policy language:

- Safe: exact match on the full exec string.
- Safe with care: argument templates. Tokenize with a strict lexer that rejects every shell metacharacter. Match each argument against an anchored character class. Forbid a leading `-` in placeholders. Re-quote before sending upstream.
- Unsafe: prefix match (`ls; rm` passes, because the remote shell parses the string).
- Unsafe: regex over the raw string.
- No deny lists.

Pitfalls:

- The exec payload is one string run by the remote user's shell. Policy must reason about what that shell will do.
- Standard input is unrestricted. An allowed `sh`, `python` or `tee` rule is full access.
- Enforce per channel, not per connection: clients multiplex.
- Upstream prompts (passphrase, keyboard-interactive, 2FA) do not work unattended. Keys must already be loaded in the host agent.
- EOF, exit status and signal forwarding must be correct, or git and rsync may hang (our caution, not sourced).

### 5.9 Variant: special ssh-agent plus a key-agnostic jump host

Added 2026-11-22. The idea: a per-sandbox ssh-agent and a generated `ssh_config` give the ability to connect out; a bastion sits in between, knows nothing about keys, and is shared between agents.

What it gives:

| Property | Holds? | Why |
|---|---|---|
| Real keys stay outside | Yes | The agent signs; the sandbox only has a socket. |
| Per-agent hosts | Yes | Enforced twice: the bastion's destination allowlist per client identity, and the agent's key-to-host binding (`session-bind`, §5.2). |
| Bastion agnostic to keys and destinations | Yes | With `ProxyJump` the bastion only opens a `direct-tcpip` channel to `host:port`. The SSH session inside is end to end between client and destination. |
| Shared between agents | Yes | Each sandbox authenticates to the bastion with its own throwaway key or certificate. The bastion maps that identity to a destination list. |
| Simple to operate | Yes | Stock `sshd` can do it: `restrict,port-forwarding,permitopen="host:22"` per key in `authorized_keys`. |
| Command restriction | **No** | The bastion forwards ciphertext. It never sees the `exec` request. |
| Session audit | Connections only | Who connected where and when. Not what was run. |

Consequences:

- A key-agnostic bastion is an allowlisting TCP tunnel. The egress proxy of §4 already is one: `CONNECT host:22` with a per-sandbox allowlist does the same job. A separate bastion adds only the SSH-native client side (`ProxyJump` instead of a `ProxyCommand` helper).
- **Key-agnostic and command-aware exclude each other.** To see commands, the intermediary must terminate SSH (§5.3), and then it must authenticate upstream itself.
- The two can still be combined so the bastion stores no keys: the terminating broker asks the per-sandbox ssh-agent to sign the upstream authentication. The agent socket stays on the host side; the sandbox never reaches it. The broker is then shared, stateless about keys, and selects the agent by the sandbox identity that connected.
- In the key-agnostic variant the agent socket is inside the sandbox. It is a signing oracle, limited by the host binding: a signature is only valid for a session with an allowed host key.
- Inference: once connected to an allowed host, the client can ask that host for port forwarding or a further jump. The allowed host becomes a pivot unless its own `sshd` forbids it. Only the terminating broker can refuse `direct-tcpip`.

Two tiers follow:

| Tier | Pieces | Restricts | Cost |
|---|---|---|---|
| A. Host trust | Per-sandbox filtering ssh-agent + `CONNECT host:22` allowlist in the egress proxy (or a shared stock-`sshd` bastion) | Key, host, user | Small. A custom agent on `x/crypto/ssh/agent` and a generated `ssh_config`. |
| B. Command rules | Terminating broker (§5.3) that authenticates upstream through the same per-sandbox agent | Key, host, user, command, forwarding, subsystems | Larger. Session piping, policy language, the pitfalls of §5.8. |

Tier A is a subset of tier B's parts, so A can ship first and B can reuse the agent and the configuration.

## 6. Browser capability per sandbox

### 6.1 Playwright MCP and Playwright CLI

Versions seen: `@playwright/mcp` 0.0.86 and `@playwright/cli` 0.1.25 (both 2026-11-18, Apache-2.0), `playwright` 1.65.1.

The project's own position ([README](https://github.com/microsoft/playwright-mcp/blob/main/README.md)):

- "Playwright MCP is **not** a security boundary."
- `--allowed-origins` and `--blocked-origins`: "*does not* serve as a security boundary and *does not* affect redirects."
- `secrets`: "It is a convenience and not a security feature."

| Option | What it does | Limits |
|---|---|---|
| `--allowed-origins`, `--blocked-origins` | Implemented with `context.route`: abort everything, continue per allowed origin (checked in the 1.65.1 bundle). Covers page requests and subresources that routing sees. | Not redirects. Service worker script updates cannot be routed. WebSocket coverage UNVERIFIED. |
| `--allowed-hosts` | Not a browser restriction. It is the Host-header check of the MCP HTTP server. | — |
| `--block-service-workers` | Blocks registration. | Closes the service worker gap. |
| `--proxy-server`, `--proxy-bypass` | Passed to the browser. | Chromium bypasses the proxy for loopback by default (§6.3). |
| `--isolated` | In-memory profile. | The default is a persistent profile per workspace. |
| `--storage-state <path>` | Loads cookies and localStorage. | The file holds session secrets. |
| `--secrets <dotenv>` | The model passes a key name; the tool types the real value. Responses are redacted by exact string match. | No host binding. No redaction of encoded forms. Screenshots are not redacted. |
| `--caps` | Extra tool groups, including `storage` (cookie and state read/write) and `devtools`. | — |
| `--cdp-endpoint` | Attach to an existing browser. | — |
| `--extension` | Attach to the user's Chrome. Clients "can read browser-wide data such as cookies". | Not per sandbox. |
| `--config <json>` | Same settings as a file. | — |

- The default tool set includes `browser_evaluate`, `browser_run_code_unsafe`, `browser_file_upload` and network request tools. No per-tool disable flag was found (UNVERIFIED that none exists).
- Playwright CLI: shell commands instead of MCP tools, with `eval`, `run-code`, `cookie-*`, `state-save`, `attach --cdp`. Same statement about origin lists.
- Inference: the CLI runs in the agent's shell, so the agent controls its flags. It is harder to constrain than an MCP server that ateam launches.

### 6.2 Alternatives

| Tool | Version, license | Destination restriction | Credentials away from the model | Notes |
|---|---|---|---|---|
| [Chrome DevTools MCP](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/docs/configuration.md) | 1.13.0, Apache-2.0 | `--allowedUrlPattern` (Chrome 149+): "blocks runtime requests (including navigations and subresources)". Not described as a boundary. | None | `--javascriptEvaluation=false` exists. Usage statistics on by default. |
| [Vercel agent-browser](https://agent-browser.dev/security) | 0.43.0, Apache-2.0, Rust | `--allowed-domains`: navigations and subresources; WebSocket by constructor patching. "The domain allowlist is not an operating-system firewall." | Auth vault, AES-256-GCM: "The LLM never sees passwords." | An action policy can deny `eval`, `download`, `upload`. |
| [browser-use](https://docs.browser-use.com/customize/sensitive-data) | 1.0.8, MIT | `allowed_domains`, navigation level | `sensitive_data` scoped to a site pattern; redaction covers base64, hex and URL-encoded forms; tool-side TOTP | "Screenshots are not redacted." "This is not a sandbox." |
| [Stagehand](https://docs.stagehand.dev/v3/basics/act) / Browserbase | MIT | None documented | `variables` "not shared with LLM providers" | Hosted browser. |
| [1Password agentic autofill](https://docs.browserbase.com/integrations/1password/agentic-autofill.md) | Service | — | The user approves each request; values are not returned to the application | Browserbase only; HTTPS pages only. |
| [Claude Code `--chrome`](https://code.claude.com/docs/en/chrome) | — | Per-site approval prompts | No: it shares the user's browser login state | Not per sandbox. Needs a claude.ai login. |
| [chromedp](https://github.com/chromedp/chromedp) | v0.24.0, MIT, Go | None built in; the caller sets flags | None built in | Uses `--remote-debugging-pipe`, so Chrome opens no debugging port. |
| [go-rod](https://github.com/go-rod/rod) | v0.116.2 (2024), MIT, Go | None built in | None built in | Last release over two years old. |
| [Lightpanda](https://github.com/lightpanda-io/browser) | 1.0.5, AGPL-3.0 | None | None | Not Chromium. Telemetry on by default. |
| [Steel](https://steel.dev/docs/logins/credentials) | v0.5.5-beta, Apache-2.0 | `origins` limits where a session may fill | Credentials API: fills user, password, TOTP; "Steel never returns it." | Docs warn that page code "can still read a filled field". |

A Go binary can host the browser itself with chromedp, over a pipe. The cost: ateam would implement its own tool surface (snapshots, element references, waiting), which Playwright MCP already has.

### 6.3 Enforcing the address list below the tool

| Mechanism | Covers | Does not cover | A boundary? |
|---|---|---|---|
| Playwright `context.route` (what `--allowed-origins` uses) | Requests that routing sees | Redirects; service worker updates; requests made by the driver process | No |
| Chrome policy `URLBlocklist: ["*"]` + `URLAllowlist` | Navigation | Subresources and API calls of an allowed page (secondary source) | No. A second layer. |
| `--proxy-server` + `--proxy-bypass-list="<-loopback>"` | All HTTP, HTTPS and WebSocket traffic. With an HTTP proxy, "name resolution is always deferred to the proxy." | UDP; anything launched with other flags | Only if the process cannot open other sockets |
| PAC file | Same as proxy | The implicit loopback bypass cannot be disabled with PAC | No |
| OS egress confinement of browser and driver, proxy as the only exit | Everything: WebRTC, QUIC, DNS-over-HTTPS, custom flags | Exfiltration through allowed hosts | **Yes** |

Details:

- Chromium bypasses the proxy by default for `localhost`, `127.0.0.1/8`, `[::1]` and link-local ranges. `<-loopback>` removes that rule. ([proxy.md](https://chromium.googlesource.com/chromium/src/+/HEAD/net/docs/proxy.md)) This matters: the usual target is `localhost:3000`.
- Linux managed policy files live under `/etc/opt/chrome/policies/managed/`. The macOS location and whether Chrome for Testing honors policies are UNVERIFIED.
- Leak blockers such as `--disable-quic` and the WebRTC IP handling policy are UNVERIFIED this pass. With OS confinement they are defense in depth only.
- Inference: `browser_run_code_unsafe` runs Playwright code in the driver process, and `page.request` sends from that process. So the driver must be confined with the browser.
- Inference: because the proxy resolves names, DNS rebinding is decided at the proxy. It should match host and port, then pin the resolved address.

### 6.4 Where the browser runs

Inside the agent sandbox:

- **macOS Seatbelt:** Chromium fails at launch under a deny-default profile with `bootstrap_check_in org.chromium.Chromium.MachPortRendezvousServer.<pid>: Permission denied (1100)`. It is a `mach-register` operation. A proposed fix is `(allow mach-register (global-name-prefix "org.chromium."))`, unconfirmed. ([issue](https://github.com/anthropics/claude-code/issues/82660), open since 2026-07-30)
- Chrome DevTools MCP documents the same class of problem and suggests starting Chrome outside and connecting to it. ([troubleshooting](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/docs/troubleshooting.md))
- bubblewrap sets `no_new_privs`, which blocks Chromium's setuid sandbox fallback (secondary sources). Landlock interaction UNVERIFIED.
- Docker ([Playwright docs](https://playwright.dev/docs/docker)): root disables the Chromium sandbox; a custom seccomp profile "is needed to run Chromium with sandbox"; "It is not recommended to use this Docker image to visit untrusted websites."
- `--no-sandbox` removes renderer isolation. A compromised renderer then has the privileges of the surrounding sandbox.

Outside the agent sandbox, as a managed service:

- Raw CDP is full control. `Storage.getCookies` returns all cookies including `httpOnly` ones; `IO.read`, `Browser.setDownloadBehavior` and `WebAuthn.addCredential` (which takes a private key) exist. ([protocol](https://github.com/ChromeDevTools/devtools-protocol))
- Chrome 136 stopped honoring `--remote-debugging-port` on the default profile because attackers used it to extract cookies. ([Chrome blog](https://developer.chrome.com/blog/remote-debugging-port))
- So the safe interface is a filtered MCP tool surface. Do not expose CDP, a Playwright server endpoint, or code-execution tools.
- Hosted browsers (Browserbase, Steel) are this split by design.

### 6.5 Credentials the agent never sees

| Method | Works for | Leak paths | Must be turned off |
|---|---|---|---|
| Proxy-side header injection | Basic auth, bearer tokens, API keys, static session cookies. The secret never enters the browser. | The site reflecting the header; `Set-Cookie` responses giving the browser a session | Nothing in the browser. HTTPS needs interception and a CA the browser trusts. |
| Pre-authenticated `storageState` or profile | Any site, including ones where a human completed 2FA | Non-HttpOnly cookies through `document.cookie`; all cookies through storage tools or CDP; headers in network tools and traces | `browser_evaluate`, `browser_run_code_unsafe`, the `storage` capability, network detail tools, trace saving, CDP. The state file must live outside the agent sandbox. |
| Form fill with placeholders | Login forms | Page script reading the input back; screenshots; snapshots; `evaluate`. Playwright MCP `--secrets` has no host binding. | `evaluate`, screenshots during fill. Needs host binding (browser-use and Steel have it). |
| `httpCredentials` | Basic auth, restricted by `origin` | The value sits in driver configuration | Driver configuration out of the agent's reach |
| Client certificates | mTLS sites | Key file readable by the driver | Proxy-side mTLS is the alternative |
| WebAuthn virtual authenticator | Passkey sites in test setups | The private key is supplied over CDP | — |
| TOTP | Sites with authenticator codes | The seed is as sensitive as the password | Tool-side generation (browser-use, Steel, 1Password) |

Proxy injection for browsers:

- Header injection is independent of page content, so CSRF tokens work as usual (general HTTP behavior, not verified per tool).
- OAuth redirects work only if every host in the chain is allowed.
- Rotating session cookies would need a cookie jar in the proxy.
- CA trust: Playwright `ignoreHTTPSErrors` exists. Chromium's SPKI list flag, the NSS database on Linux and the Keychain on macOS are UNVERIFIED this pass.
- `http://localhost` needs no interception.

No method stops the agent from *using* the session. Only the egress allowlist limits where a stolen token can be sent.

### 6.6 Threats specific to agent browsing

- **Prompt injection from pages.** Page content is untrusted input. agent-browser wraps page output in nonce-marked boundaries and calls it defense in depth.
- **Exfiltration through allowed sites.** Any allowed host that accepts input is a channel. Allowlist by host and port.
- **Local network access.** Chrome's Local Network Access prompt (Chrome 142; WebSockets in 147) is a user protection, not enforced on main-frame navigation. Behavior in headless automation is UNVERIFIED. Inference: with `<-loopback>` the proxy decides, and it must reject private and loopback targets not on the list.
- **Downloads and uploads.** `acceptDownloads` defaults to true; `browser_file_upload` reads the driver's filesystem. With the browser outside the sandbox, files cross the boundary through the driver. Use a dedicated exchange directory.
- **Screenshots, snapshots, traces.** They capture whatever the page shows, including filled fields.

### 6.7 Recommendation for the browser

1. **Run the browser outside the agent sandbox, one managed instance per sandbox, in its own egress confinement.**
   - First choice: a Docker container with headless Chromium, non-root, seccomp profile, Chromium sandbox on, on a network that reaches only the ateam proxy. Works on macOS and Linux.
   - Second choice: a host process under a dedicated Seatbelt or Landlock profile. On macOS this needs a test of the `mach-register` allowance.
   - Not recommended: Chromium inside the agent's sandbox with `--no-sandbox`.
   - Confine the driver (Playwright MCP's Node process) with the browser.
2. **Expose MCP only, through an ateam-owned stdio shim.**
   - Start with Playwright MCP behind the shim.
   - Filter out `browser_run_code_unsafe`. No `storage` or `devtools` capability, no CDP endpoint, no extension mode.
   - For a credentialed browser, also drop `browser_evaluate` and the network detail tools.
   - Never expose CDP or the Playwright CLI for a credentialed browser.
   - Later option: a chromedp tool surface over a pipe, which removes the Node dependency. Larger scope.
3. **Enforce the address list at the ateam proxy, by host and port.**
   - Launch with `--proxy-server=<proxy>` and `--proxy-bypass-list=<-loopback>`, so `localhost:3000` is also decided by the proxy.
   - Inference: in a container, the proxy can map `localhost:3000` to the host's dev server while the page origin stays `localhost`.
   - `--allowed-origins`, `--block-service-workers` and URL policies are early-failure hints only.
4. **Credentials: proxy-side header injection first.**
   - It reuses the placeholder mechanism of §4.
   - Second: a human-prepared `storageState`, loaded with `--isolated --storage-state`, stored outside the agent sandbox, with the reduced tool set.
   - Third: host-bound form-fill placeholders in the shim, with tool-side TOTP.
   - WebAuthn and client certificates on demand.

## 7. Limits that apply to all three

- **Authorized misuse.** A brokered credential, an allowed SSH command and a logged-in browser all let the agent do whatever that capability permits. Mitigations: least-privilege tokens, method and path rules, exact-match commands, read-only accounts. See `ResearchSandboxing.md` §G.6.
- **Exfiltration through allowed endpoints.** An allowed host that accepts content is a channel.
- **The broker is trusted.** It holds every secret of its sandbox and sees the plaintext of intercepted traffic. Keeping one broker per sandbox limits the loss to that sandbox's set.
- **Policy location.** The agent must not be able to edit its own capability configuration. This matches the plan's protection of ateam's files.
- **Address checks.** An allowed name can resolve to loopback, link-local or a metadata address. Resolve in the broker, check the address, dial that address.
- **Unattended runs.** Anything that prompts (keychain unlock, Touch ID, SSH passphrase, 2FA) blocks an unattended run. Each capability needs a defined behavior when it cannot unlock: fail closed at launch.

## 8. Implications for ateam

These follow the plan's phases 2 to 4. They are observations, not decisions.

- **One mechanism, three uses.** The per-sandbox proxy (§4) carries the `network` entity's allowlist, secret injection, the browser's traffic and, through CONNECT on port 22, the SSH broker. The plan's phase 2 maps `network` to nono's proxy; a built-in proxy would make `network` independent of the backend, and each backend would only need "reach this one port".
- **Capabilities fit the plan's `resources` list.** Secrets, SSH and browser are entities referenced from a profile, like `filter` and `network`. Composition by `use` then gives per-agent sets with no new concept.
- **Fail closed.** A capability the backend cannot bind to one sandbox, or a secret that cannot be unlocked, refuses the launch. Same rule as the plan's decision 2.
- **`ateam secret` needs a third backend** for R2 on headless Linux: an encrypted file whose key comes from the keychain or a passphrase.
- **Order by cost.** The base-URL LLM key proxy needs no CA. Allowlist-only CONNECT needs no CA. Placeholder injection needs the CA. The SSH broker and the browser service are separate, larger pieces.

Illustrative shape only, to connect with the plan's syntax:

```hcl
secret_rule "github-read" {
  secret = "GITHUB_TOKEN_RO"             # an `ateam secret` name
  hosts  = ["api.github.com"]
  header = "Authorization"
}

ssh "staging-logs" {
  host    = "staging.example.com"
  user    = "deploy"
  allow   = ["journalctl -u app --since {duration}", "docker ps"]
}

browser "webapp" {
  allow       = ["localhost:3000", "staging.example.com:443"]
  credentials = ["staging-basic-auth"]
}

profile "webapp-dev" {
  use       = ["nono", "node"]
  network   = ["base", "llm"]
  resources = ["github-read", "staging-logs", "webapp"]
}
```

## 9. Open questions

1. Build the proxy in-process, or keep nono's proxy as the first backend and build later.
2. Whether TLS interception is acceptable at all, or only explicit base URLs and allowlist tunnels at first.
3. Where the unlock key lives for unattended Linux runs.
4. SSH: is host-and-user trust enough for a first version, or are command rules needed from the start.
5. Browser: is a Docker dependency acceptable for agents that otherwise run under Seatbelt or nono.
6. Whether a Seatbelt profile with the `mach-register` allowance starts Chromium. Needs a local test.
7. Whether Playwright MCP tools can be filtered by configuration, or only by a shim.
8. How a profile learns a dynamic dev-server port (also open in the plan).

## 10. Not verified in this pass

- Command filtering in sshpiper, Warpgate, Teleport and Boundary (docs are silent).
- sshdo and authprogs rule syntax.
- Codex CLI proxy variables; Bun's CA variables; git and Python variables were not re-read.
- macOS managed-policy location for Chrome; Chromium leak-blocker flags; SPKI list flag.
- Local Network Access behavior in headless Chromium.
- Playwright MCP WebSocket coverage of `--allowed-origins`; absence of a per-tool disable flag.
- Whether the `proxy` package of runclaude builds on macOS.
- The escape and injection behavior of every tool: nothing was run.
