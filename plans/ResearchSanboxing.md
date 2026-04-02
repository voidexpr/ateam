# Research: Sandboxing

## D. Permission Management

The landscape of open source tools for managing Claude Code permissions splits into roughly three categories: container isolation, smart hooks/filters, and programmatic permission servers.

### D.1 claude-code-sandbox (textcortex)

**Language:** TypeScript/Node.js
**GitHub:** `textcortex/claude-code-sandbox` (archived; continued as "Spritz")

Wraps Claude Code in a Docker container and runs it with `--dangerously-skip-permissions`. The isolation is at the container level — Claude can do anything inside, but the container is your actual permission boundary. Provides a browser-based terminal UI to monitor sessions, supports auto-push to git branches and auto-PR creation.

**Pros:**

- True isolation — Claude can run fully autonomously without a single prompt
- Great for unattended, async workflows
- Auto-PR creation is very useful for ATeam-style pipelines
- Podman support as Docker alternative

**Cons:**

- Archived; maintenance uncertain (though Spritz continues it)
- Docker overhead; slower startup
- All-or-nothing inside the container — no fine-grained tool policies
- No audit trail of specific tool calls

**Best for:** Long unattended sessions, batch jobs, CI/CD-adjacent autonomous work.

### D.2 viwo-cli (Hal Shin)

**Language:** Unknown (shell/script-based)
**GitHub:** via `awesome-claude-code`

Runs Claude Code in a Docker container with git worktrees as volume mounts to enable safer usage of `--dangerously-skip-permissions` for frictionless one-shotting prompts. Allows users to spin up multiple instances of Claude Code in the background easily with reduced permission fatigue.

**Pros:**

- Worktree per-agent isolation is elegant — each agent works its own branch
- Lightweight compared to full sandbox setups
- Good for parallel agent runs

**Cons:**

- Less tooling around monitoring/observability
- No fine-grained tool-level policy

**Best for:** Running multiple unattended Claude instances in parallel on separate worktrees.

### D.3 TSK — AI Agent Task Manager and Sandbox (dtormoen)

**Language:** Rust
**GitHub:** [dtormoen/tsk-tsk](https://github.com/dtormoen/tsk-tsk)

A Rust CLI tool that lets you delegate development tasks to AI agents running in sandboxed Docker environments. Multiple agents work in parallel, returning git branches for human review. Positions itself explicitly as a task delegation tool where human review happens at the branch/PR level rather than at individual tool calls.

What makes it relevant for ATeam is that the sandboxing story is more explicit than the original short description implied:

- **Repo copy, not bind mount.** `tsk` copies the repository into a per-task workspace and excludes gitignored files by default. This is a meaningful safety property: secrets and local detritus that are already ignored by git do not get handed to the agent accidentally.
- **Container image composition is layered.** Each sandbox image is assembled from a base Dockerfile plus stack-specific snippets (Go, Node, Python, etc.), an agent snippet (`claude`, `codex`), and an optional project layer. That makes the environment reproducible while still allowing repo-specific customization.
- **Network egress is proxied, not open.** Each task container routes traffic through a Squid forward-proxy sidecar. The proxy enforces a domain allowlist, and `tsk` fingerprints proxy config so tasks with different policies get different proxy containers.
- **Host-local service access.** `host_ports` configuration and `TSK_PROXY_HOST` allow agents inside the container to reach services running on the host (databases, dev servers), making tsk-tsk the most practical containerized option for integration tests that need localhost connectivity. Optional DinD support is also available.
- **Task artifacts are structured.** Each task directory contains `/repo`, `/instructions.md`, and `/output/agent.log`. The log is structured JSON-lines covering both infrastructure phases and processed agent output, which is useful for debugging and postmortems.
- **Docker and Podman are both supported.** That matters for environments where Docker Desktop is undesirable or unavailable.

This is still a container-first model: once the agent is inside its container, the control plane is coarse-grained. The security boundary is "the copied repo + the container + the proxy policy", not per-tool approval.

**Pros:**

- Rust gives it low overhead and reliability
- Designed from the ground up for unattended multi-agent work
- Human review at the output (branch) level is sane for async workflows
- Better-than-average network story for a Docker wrapper because the proxy is a first-class feature
- Repo-copy semantics reduce accidental secret exposure compared to naive bind-mount approaches

**Cons:**

- Rust means fewer people can contribute/customize
- Still Docker/Podman-dependent
- Domain allowlisting is proxy-based, not kernel-level network isolation
- Copying the repo is safer, but less convenient than bind mounts for very large repos or workflows that expect live host changes

**Best for:** Unattended batch agent work where "safe enough by container + allowlisted network + review the branch later" is the desired operating model.

### D.4 Dippy (Lily Dayton)

**Language:** Python (likely, based on AST parsing)
**GitHub:** via `awesome-claude-code`

Auto-approves safe bash commands using AST-based parsing, while prompting for destructive operations. Solves permission fatigue without disabling safety. Supports Claude Code, Gemini CLI, and Cursor. It parses the bash command AST before it executes, classifying it as safe (read, grep, ls, etc.) or potentially destructive (rm, curl with pipes, etc.) and only interrupts on the latter.

**Pros:**

- Genuinely smart — not just allowlist matching, but structural command analysis
- Keeps human-in-the-loop for dangerous ops without spamming prompts
- Multi-tool support (Gemini CLI, Cursor too)
- No container overhead

**Cons:**

- AST parsing can't catch all dangerous patterns (e.g., variables hiding `rm -rf`)
- Only covers bash, not MCP tools or file edits
- Primarily interactive — less suited to fully unattended runs

**Best for:** Interactive sessions where you want reduced prompt fatigue without losing safety.

### D.5 claude-code-permission-prompt-tool (community-documented)

**Language:** TypeScript/JavaScript (MCP server)
**GitHub:** `lobehub.com/mcp/user-claude-code-permission-prompt-tool`

Uses Claude Code's undocumented `--permission-prompt-tool` CLI flag that enables programmatic permission handling via MCP servers. This allows building custom approval workflows for tool usage without requiring interactive terminal input. When Claude wants to use a tool, instead of asking the user, it calls your MCP server, which can allow, deny, or log the request programmatically. You get the full tool name, arguments, and `tool_use_id` at decision time.

**Pros:**

- Extremely powerful — arbitrary logic for approval (database logging, remote approval, budget tracking)
- Allows async/remote human-in-the-loop (send Slack message, wait for approval)
- Enables full audit trails of every tool call with its exact arguments
- Can modify tool inputs before they execute (`updatedInput`)

**Cons:**

- Flag is undocumented and could break between Claude Code releases
- Requires building and running your own MCP server
- Adds latency to every tool call

**Best for:** Production automation, compliance/audit use cases, remote approval workflows, and building ATeam-style orchestrators with budget controls.

### D.6 Anthropic's Built-in Sandbox Runtime (open-sourced)

**Language:** C/Rust (OS primitives: Linux bubblewrap, macOS Seatbelt)
**GitHub:** Anthropic's experimental repo

Built on top of OS-level primitives such as Linux bubblewrap and macOS Seatbelt to enforce restrictions at the OS level. Covers not just Claude Code's direct interactions, but also any scripts, programs, or subprocesses spawned by commands. Enforces both filesystem isolation (allowing read/write to current working directory, blocking modification of files outside it) and network isolation (only allowing internet access through a unix domain socket connected to a proxy server). In Anthropic's internal usage, sandboxing safely reduces permission prompts by 84%.

**Pros:**

- The real deal — OS-level enforcement, not just policy
- Claude (and any subprocess it spawns) genuinely can't escape the sandbox
- Configurable at fine granularity: specific dirs, specific domains
- Now open-sourced and usable for your own agents

**Cons:**

- Requires platform-specific setup (bubblewrap on Linux, Seatbelt on macOS)
- Still relatively new/experimental API
- Less community tooling around it

**Best for:** High-security unattended sessions; the right foundation for any serious agentic system.

### D.7 Custom Bash Permission Hooks (community patterns)

**Language:** Bash/Python
**GitHub:** Various gists, e.g. `cruftyoldsysadmin` gist

Uses Claude Code's `hooks` system (`PreToolUse`, `PostToolUse` lifecycle events) to intercept tool calls with shell scripts. You write a script that receives the tool name and arguments via stdin, and exits 0 (allow), 1 (deny), or 2 (ask) based on your logic.

A useful concrete example from the `awesome-claude-code` list is [`pchalasani/claude-code-tools`](https://github.com/pchalasani/claude-code-tools). Its `safety-hooks` plugin packages a set of opinionated guardrails rather than making every team write hooks from scratch: blocking clearly destructive `rm` patterns, protecting `.env` files, and putting policy around risky git operations such as direct `git add` / `git commit`. That is not sandboxing, but it is a practical "policy layer" on top of a sandbox or container.

**Pros:**

- No external dependencies — pure shell
- Directly integrated into Claude Code's lifecycle
- Easily version-controlled in your repo (`.claude/hooks/`)
- Can block writes to production config files, enforce naming conventions, etc.

**Cons:**

- Shell scripting injection risks if you're not careful with quoting
- Limited expressiveness compared to a full MCP permission server
- No built-in remote/async approval capability
- Still policy-only. If the agent escapes the hook path or gains broader execution elsewhere, there is no containment

**Best for:** Project-specific rules in interactive sessions; simple allowlist/denylist enforcement.

### D.8 Comparison Table

| Project | Language | Interactive | Unattended | Isolation | Audit Trail | Complexity |
|---|---|---|---|---|---|---|
| claude-code-sandbox | TypeScript | ✓ | ✓✓ | Docker | None | Medium |
| viwo-cli | Shell | — | ✓✓ | Docker+worktree | None | Low |
| TSK | Rust | — | ✓✓ | Docker | Partial | Medium |
| Dippy | Python | ✓✓ | ✗ | None | None | Low |
| permission-prompt-tool | TypeScript/MCP | ✓ | ✓✓ | None | ✓✓ | High |
| Anthropic Sandbox runtime | C/Rust | ✓ | ✓✓ | OS-level | None | High |
| Claude Code hooks | Bash/Any | ✓ | ✓ | None | Optional | Low |

### D.9 Recommendations for ATeam Use Cases

Given unattended background agents with budget controls and audit needs, the most relevant combination would be:

**`permission-prompt-tool`** MCP server for fine-grained audit logging and budget enforcement at the tool call level, combined with either **Anthropic's sandbox runtime** or docker-based isolation (**viwo-cli/TSK**) for actual containment. The permission-prompt-tool's `updatedInput` capability is particularly interesting for implementing tool input sanitization before execution.

The **hooks system** is worth layering on top for project-specific blocking rules that you want committed to the repo itself. `claude-code-tools` is a good reference point for what a reusable hook pack can look like in practice.

---

## E. Agent Sandboxing

Running agents without approval prompts requires sandboxing — if you can't trust the agent to stay in its box, you need to watch every move. The goal is to make the box tight enough that `--dangerously-skip-permissions` (or the sandbox auto-allow mode) becomes genuinely safe, not just "YOLO." This section reviews the available approaches through three dimensions that matter for ATeam: filesystem isolation, network control, and remote session access.

### E.0 Claude Code sandbox

Limitations:
* still need to approve network requests
* reduces bash approval but not complete
* sandbox might block file access from tools and make them fail
    * docker might be a better way
* clear security risks:
    * data exfiltration

Known sandbox quirks worth noting for ATeam:
* The sandbox blocks .git access broadly — not just the repository directory, but .git marker files anywhere. This can affect tools whose caches contain .git marker files. Antisimplistic Blog
* Bash heredoc syntax (<< EOF) fails in the sandbox — the shell needs to create a temp file for the here document and the sandbox blocks it, even with TMPDIR pointed at an allowed path. Antisimplistic Blog
* The allowUnixSockets configuration can inadvertently grant access to powerful system services. For example, allowing access to /var/run/docker.sock would effectively grant access to the host system through the Docker socket. Claude

Can be used with /sandbox or pass a custom settings.json file (I think this involves merging with the local one to preserve local config).

```bash
cat sandbox_enabled.json
{
  "sandbox": {
    "enabled": true,
    "autoAllowBashIfSandboxed": true
  }
}
claude -p --settings sandbox_enabled.json "delete file foobar"
The file `foobar` exists (empty file). Shall I go ahead and delete it?
```

You can pass a settings file to Claude Code using the --settings flag.

Basic usage

claude -p --settings settings.local.json

With a path

claude -p --settings ./config/settings.local.json

Example unattended run

claude -p \
  --settings settings.local.json \
  --dangerously-skip-permissions \
  "review the repo and propose improvements"

Notes
    •   --settings replaces the default ~/.claude/settings.json for that run.
    •   The file can include things like:
    •   sandbox
    •   permissions
    •   hooks
    •   env
    •   This is commonly used in automation so CI/agents don’t depend on a user’s home directory config.

Useful pattern for agent runners

Many setups do something like:

WORKDIR=/workspace/task-123

claude -p \
  --settings "$WORKDIR/settings.local.json" \
  --dangerously-skip-permissions \
  --cwd "$WORKDIR" \
  "$PROMPT"

so each workspace/worktree has its own sandbox policy.

### E.1 The Three Dimensions

**Filesystem access** needs nuance. A code analysis agent needs read access to the project it's working on, read/write to its own working directory, and access to toolchain paths (`~/.npm`, `~/.cache`, `/usr/local`, etc.) and `~/.claude` for Claude Code to function. It should NOT have access to `~/.ssh`, `~/.aws`, `~/.gnupg`, other project directories, or anything outside its scope. The hard part is that "normal tool usage" (node, npm, go, pip, cargo) requires scattered filesystem access — you can't just mount one directory.

**Network access** has three useful tiers, not two:

| Tier | What's allowed | When to use |
|---|---|---|
| **Full network** | Everything | Agent needs to research, browse docs, or use external APIs. Filesystem isolation is the safety boundary. |
| **Developer network** | LLM API + package managers (npm, pypi, crates.io, proxy.golang.org, ...) + API docs + local ports | Agent needs to install packages, run tests against local services, read documentation. No arbitrary outbound. |
| **Minimal network** | LLM API only | Maximum containment. Agent can think but can't reach anything. |

A fourth dimension — **local port access** — cuts across these tiers. An agent testing a web app needs `localhost:3000` regardless of whether it can reach the internet. Local port access should be configurable per-project (read from `.env` or `config.toml`).

**Remote session access** is always important. Running agents unattended means checking on them from a phone, giving instructions, reviewing progress. This requires exposing the agent's session without exposing the host machine. Every sandboxing approach should be evaluated for how well it supports this — and critically, remote access means the sandbox is the only thing standing between the outside world and the development environment.

### E.2 Anthropic Sandbox Runtime (sandbox-runtime)

**GitHub:** [anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)
**npm:** `@anthropic-ai/sandbox-runtime`
**Language:** TypeScript/C (bubblewrap on Linux, Seatbelt on macOS)

The official Anthropic tool. Uses OS-level primitives — not containers — to enforce filesystem and network restrictions on arbitrary processes. Integrated into Claude Code as the "sandboxed bash tool" since late 2025.

**How it works:**

This is a **per-command sandbox** — Claude Code itself runs unsandboxed on the host and wraps each individual bash command with `sandbox-exec` before executing it:

```
Claude Code (unsandboxed, on host)
  └─ sandbox-exec -p <dynamic-profile> bash -c "npm install"   ← sandboxed
  └─ sandbox-exec -p <dynamic-profile> bash -c "git status"    ← sandboxed
  └─ playwright install                                         ← NOT sandboxed (if excluded)
```

Each command gets its own dynamically generated Seatbelt profile. This means no nesting issues — each invocation is independent. The tradeoff is that Claude Code's own Node.js process has full host access; only the bash commands it spawns are constrained.

- On **macOS**: generates a dynamic Seatbelt profile (`.sb` file) and executes via `sandbox-exec`. The profile is regenerated per invocation based on the configured policy.
- On **Linux**: uses [bubblewrap](https://github.com/containers/bubblewrap) to create a restricted namespace. Requires `bubblewrap` and `socat` packages.
- **Network proxy**: all network traffic is forced through a unix domain socket to a proxy running outside the sandbox. The Seatbelt profile only allows connections to the proxy's localhost ports; the proxy enforces domain allowlists. This is how network isolation works without iptables or network namespaces on macOS.

**Filesystem policy (Claude Code defaults):**

- **Write allowed**: current working directory and subdirectories, `/tmp`
- **Write denied**: everything else (home directory, system paths, other projects)
- **Read allowed**: entire filesystem by default (agent needs to read toolchains, system libraries)
- **Read denied**: configurable — can block `~/.ssh`, `~/.aws`, `~/.gnupg`, etc.
- **Configurable** via `settings.json`:
  ```json
  {
    "sandbox": {
      "enabled": true,
      "filesystem": {
        "allowWrite": ["~/.npm", "~/.cache", "//tmp"],
        "denyRead": ["~/.ssh", "~/.aws", "~/.gnupg"]
      },
      "allowedDomains": ["api.anthropic.com", "registry.npmjs.org", "github.com"]
    }
  }
  ```
- Path prefixes: `//` = absolute from root, `~/` = relative to home, `/` = relative to settings file, `./` = relative to cwd.
- Settings from multiple scopes (managed, user, project) **merge** — you can't accidentally override a broader policy with a narrower one.

**Network policy:**

- Domain-based allowlist via `allowedDomains` in settings.
- New domains trigger a permission prompt (in interactive mode) — user can allow once or permanently.
- Custom proxy support for organizations: `sandbox.network.httpProxyPort` and `socksProxyPort` allow routing through corporate inspection infrastructure.
- **Limitation**: domain filtering only — no deep packet inspection. Domain fronting can bypass it. Allowing broad domains like `github.com` creates exfiltration risk.

**Escape hatch**: when a command fails due to sandbox restrictions, Claude is prompted to retry with `dangerouslyDisableSandbox`, which falls back to the normal permissions flow (user approval required). Can be disabled entirely with `"allowUnsandboxedCommands": false`.

**Key stat**: reduces permission prompts by 84% internally at Anthropic.

**Compatibility notes:**

- `docker` doesn't work inside the sandbox (sandbox can't sandbox Docker's privileged operations). Must be listed in `excludedCommands`.
- `watchman` (used by Jest) is incompatible — use `jest --no-watchman`.
- Some CLI tools need network access to specific hosts on first use — prompts build up the allowlist over time.
- WSL1 not supported (bubblewrap needs kernel namespaces). WSL2 works.
- Linux: `enableWeakerNestedSandbox` mode exists for running inside Docker (e.g., CI) but "considerably weakens security."

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Good | Fine-grained read/write policies. Covers subprocesses. |
| Network control | Good | Domain allowlist via proxy. All three tiers achievable. |
| Tool compatibility | Good | Most tools work. Docker and watchman are exceptions. |
| `~/.claude` access | Built-in | Claude Code handles this automatically. |
| Remote access | N/A | Not a feature. Would need a separate mechanism. |
| Overhead | Minimal | OS primitives, no VM or container startup. |
| Unattended use | Excellent | Combined with auto-allow mode, eliminates prompts within policy. |

**Best fit**: the coordinator agent (runs on host, needs minimal isolation) and lightweight agent tasks that don't need Docker services.

### E.3 neko-kai/claude-code-sandbox

**GitHub:** [neko-kai/claude-code-sandbox](https://github.com/neko-kai/claude-code-sandbox)
**Language:** Bash (wrapper script)
**Platform:** macOS only (sandbox-exec / Seatbelt)

A community-built wrapper that predates Anthropic's official sandbox integration. Generates a Seatbelt profile that restricts Claude Code's filesystem READ access — the official sandbox focuses on write restrictions, but this project also limits what Claude can see.

**How it works:**

- `claude-sandbox` wrapper script generates a `.sb` Seatbelt profile dynamically based on the target directory.
- Runs `sandbox-exec -f profile.sb claude` (or any command).
- Based on the [Para sandboxing profile](https://github.com/nickthecook/para) and Anthropic's own dynamic profile, with Claude Code-specific additions.

**Filesystem policy:**

- **Read denied**: `~` (home directory) is blocked by default. Claude cannot read your dotfiles, SSH keys, AWS credentials, or other projects.
- **Read allowed**: the target project directory, system paths needed for toolchains, and paths needed for Claude Code to function.
- **Directory listing**: directories leading up to the target can be listed (`ls`) but files/metadata cannot be read. This is necessary because Claude glitches and sets `PATH` to `""` if it can't list parent directories.
- **Write allowed**: target directory and temp locations only.

**Key difference from Anthropic's sandbox**: this denies READ access to the home directory by default. The official sandbox allows reads everywhere and only restricts writes. For ATeam's remote access use case — where the sandbox is the only barrier between a remote session and the development machine — read denial is the stronger posture.

**Network**: no network restrictions. This is purely filesystem sandboxing.

**Installation**: Nix (`nix run github:neko-kai/claude-code-sandbox -- claude`) or manual copy to `~/.local/bin/`.

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | Denies reads to ~ by default — strongest posture of any non-container approach. |
| Network control | None | No network restrictions at all. |
| Tool compatibility | Good | macOS sandbox-exec is mature. Some tools may need path additions. |
| `~/.claude` access | Handled | Explicitly allowed in the profile. |
| Remote access | N/A | No built-in support. |
| Overhead | Zero | sandbox-exec is a kernel-level mechanism, no process overhead. |
| Platform | macOS only | Cannot be used on Linux. |

**Best fit**: macOS development where filesystem read-denial is the priority and network isn't a concern (Tier 1: full network + tight filesystem).

### E.4 kohkimakimoto/claude-sandbox

**GitHub:** [kohkimakimoto/claude-sandbox](https://github.com/kohkimakimoto/claude-sandbox)
**Language:** Go
**Platform:** macOS only (sandbox-exec / Seatbelt)

A newer macOS wrapper around `claude` that takes a narrower, more operationally pragmatic stance than Anthropic's built-in sandbox: constrain writes predictably, keep reads mostly open, and provide an explicit escape hatch for tools that cannot run in a nested macOS sandbox.

**How it works:**

This is a **whole-process sandbox** — the entire Claude Code process runs inside `sandbox-exec`, not just individual commands:

```
claude-sandbox (unsandboxed parent)
  ├─ unboxexec daemon goroutine (unsandboxed, listening on Unix socket)
  └─ sandbox-exec → claude (entire Claude Code process is sandboxed)
       └─ bash commands (inherit sandbox, also sandboxed)
       └─ claude-sandbox unboxexec → talks to daemon → runs outside sandbox
```

- `claude-sandbox` is a drop-in replacement for `claude`. You can run `claude-sandbox --dangerously-skip-permissions` and get Seatbelt-enforced write confinement around that session.
- Configuration is TOML-based with **three scopes**: user (`~/.claude/sandbox.toml`), project (`.claude/sandbox.toml`), and local overrides (`.claude/sandbox.local.toml`).
- The default profile is intentionally simple: **deny file writes globally**, then allow writes to the working directory, `~/.claude`, and `/tmp`.
- The tool exposes useful introspection commands: `claude-sandbox profile` shows the generated Seatbelt profile and `claude-sandbox config` shows the merged effective config.

**Sandbox-external execution (`unboxexec`):**

The daemon exists because of a fundamental macOS Seatbelt constraint: once a sandbox profile is applied to a process, it **cannot be removed or nested**. Every child process inherits the sandbox — you can't opt out per-command, and you can't call `sandbox-exec` again from within a sandboxed process. The only way to escape is to talk to a process that was never sandboxed — hence the daemon listening on a Unix socket outside the sandbox wall.

Anthropic's sandbox-runtime doesn't need this mechanism because Claude Code itself is never sandboxed — it's the caller, not the callee, of `sandbox-exec`.

- Claude invokes `claude-sandbox unboxexec -- <command> ...`
- The request goes over a Unix domain socket to the daemon
- The daemon runs the command **outside** the sandbox only if it matches an allowlisted regex in `[unboxexec].allowed_commands`

This is a deliberate escape hatch, not a bug. It makes the wrapper more usable for real development tasks (especially Playwright and other browser automation), but the safety posture depends heavily on how tight the `allowed_commands` patterns are.

**Security profile:**

- **Write isolation:** good and predictable on macOS
- **Read isolation:** weak by default compared to `neko-kai`; this tool is about write restriction, not hiding the home directory
- **Network isolation:** none
- **Escape hatch:** explicit and configurable via `unboxexec`, which is both the main feature and the main caveat

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Good | Strong write restriction; read access remains comparatively broad. |
| Network control | None | No network restrictions. |
| Tool compatibility | Very good | `unboxexec` exists specifically to make hard-to-sandbox tools workable. |
| `~/.claude` access | Built-in | Default profile explicitly allows it. |
| Remote access | N/A | No built-in remote story. |
| Overhead | Low | Seatbelt wrapper, no VM or container startup. |
| Platform | macOS only | Not useful for Linux-based agent fleets. |

**Best fit**: macOS users who want a predictable write-constrained wrapper with per-project config and occasionally explicitly-approved escapes for incompatible tools.

### E.4a Per-Command vs Whole-Process Sandboxing

The fundamental architectural difference between Anthropic's sandbox-runtime (E.2) and kohkimakimoto's claude-sandbox (E.4) is **what gets sandboxed**:

| Dimension | kohkimakimoto (whole-process) | Anthropic SRT (per-command) |
|---|---|---|
| **Scope** | Claude Code itself is constrained — can't write files or access network except as allowed | Only bash tool commands are sandboxed; Claude Code's own process has full access |
| **Defense depth** | Stronger — even if Claude Code has a bug, it can't write outside allowed paths | Weaker — relies on Claude Code correctly wrapping every command |
| **Bypass risk** | Needs an explicit escape hatch (the daemon), which is an attack surface | No escape hatch needed, but a bug in wrapping logic means no sandbox at all |
| **Nesting** | Can't nest sandbox-exec, so Playwright/etc. need the daemon | No nesting issue — each command is independently sandboxed |
| **Network** | Default profile doesn't restrict network at all | Full network filtering via proxy with domain allowlists |
| **Integration** | External wrapper, works with any Claude Code version | Library integrated into Claude Code, needs Anthropic to ship it |
| **Complexity** | Simple — one static profile + daemon | Complex — dynamic profiles, proxy servers, per-command wrapping, violation monitoring |

**The core tradeoff:** kohkimakimoto doesn't trust Claude Code itself — even its Node.js process can't escape the sandbox. Anthropic trusts Claude Code but doesn't trust the commands it runs. The daemon is the cost of not trusting the orchestrator: you need an out-of-band channel to run things that can't work inside the sandbox.

If Anthropic ships sandbox-runtime as a standard part of Claude Code (which is the trajectory), the per-command model covers most use cases. But the whole-process model still has value if you want to restrict what Claude Code itself can do — for example, preventing it from writing to `~/.ssh` through its own Node.js process, not just through bash commands.

**For ATeam:** the per-command model (Anthropic SRT) is the right default since it's officially supported and has network filtering. The whole-process model is worth considering for high-security scenarios where Claude Code's own process shouldn't have host access, but the daemon escape hatch partially undermines that benefit.

### E.5 cco (Claude Condom)

**GitHub:** [nikvdp/cco](https://github.com/nikvdp/cco)
**Language:** Bash
**Platform:** macOS (Seatbelt), Linux (bubblewrap), Docker (fallback)

A thin wrapper that auto-selects the best available sandbox backend. The value proposition is "one command, best available isolation."

**How it works:**

- Detects platform and available tools.
- macOS: uses `sandbox-exec` (Seatbelt) — near-zero overhead.
- Linux: uses bubblewrap.
- Fallback: Docker container.
- `--safe` mode: hides the entire `$HOME` directory from Claude, significantly reducing exposure of personal files, dotfiles, secrets, and caches.

**Assessment**: useful as a reference for how to detect and compose sandbox backends. For ATeam, the auto-detection pattern is worth adopting — the same agent code should work whether the host is macOS (Seatbelt) or Linux (bubblewrap) or CI (Docker).

### E.6 ClaudeCage

**GitHub:** [PACHAKUTlQ/ClaudeCage](https://github.com/PACHAKUTlQ/ClaudeCage)
**Language:** Bash (build script)
**Platform:** Linux only

A single portable executable (no dependencies) that packages Claude Code inside a bubblewrap sandbox. Uses the [RunImage](https://github.com/VHSgunzo/runimage) project to create lightweight, unprivileged containers.

**How it works:**

- Downloads and packages Claude Code CLI into a self-contained executable.
- The `claude` binary acts as a drop-in replacement — anything that calls `claude` (including Claude Code's own sub-agent spawning) automatically runs sandboxed.
- Linux namespaces enforce isolation: the process cannot access home directory, network info, or other processes.

**Key insight**: the drop-in replacement approach is elegant for ATeam. If the `claude` binary on `$PATH` inside the container IS the sandboxed version, then `claude -p` invocations (including any sub-agents spawned by the Task tool) are automatically sandboxed. No wrapper scripts, no special flags.

**Assessment**: Linux-only, but the drop-in pattern is worth considering for Docker-based agents.

### E.7 scode (Seatbelt for AI Coding)

**GitHub:** scode by Laurent Bindschaedler (MPI-SWS)
**Language:** Bash (single script)
**Platform:** macOS (Seatbelt), Linux (bubblewrap)

An opinionated wrapper that blocks 35+ credential and personal file paths out of the box, scrubs 28+ environment variable token patterns, and handles the Chromium double-sandbox problem automatically.

**How it works:**

- Single bash script: `scode claude`
- Blocks access to known credential paths (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gcloud`, etc.)
- Scrubs environment variables matching token patterns (AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN, etc.)
- Handles Chromium's nested sandbox incompatibility (relevant for Electron-based tools).

**Key insight for ATeam**: the curated blocklist of 35+ credential paths and 28+ env var patterns is valuable. Rather than building our own list from scratch, adopt scode's lists as the default deny policy.

**Assessment**: described as "a seatbelt, not an armored vehicle" — catches the common case of an agent wandering into personal files, not a determined attacker. Good for interactive use; for unattended agents with remote access, needs to be combined with stronger isolation.

### E.8 Docker-Based Approaches

Docker remains the strongest isolation option. The existing research in §D.1–D.3 covers textcortex/claude-code-sandbox, viwo-cli, and TSK. Key additions:

**run-claude-docker (icanhasjonas):**

[icanhasjonas/run-claude-docker](https://github.com/icanhasjonas/run-claude-docker) is a single-file Docker runner that bundles the Dockerfile, runtime wrapper, and MCP server setup into one script. From a safety perspective, the useful details are:

- It mounts the workspace plus `~/.claude`, and mounts `~/.ssh` / `~/.gitconfig` read-only.
- It supports a persistent container that is reused across runs, which is convenient but means state accumulates unless you deliberately recreate or remove it.
- It offers a `--safe` mode, but its default operating mode is intentionally YOLO: `--dangerously-skip-permissions`, auth forwarding, and a privileged container.

This makes it a good example of a **developer-convenience sandbox** rather than a high-assurance one. It is safer than running directly on the host, but weaker than a minimal-purpose container design because it forwards more of the developer environment into the sandbox and keeps state warm by default.

**Container Use (dagger):**

[dagger/container-use](https://github.com/dagger/container-use) is not primarily a permission system, but it is highly relevant as a **container management pattern** for safe execution. It gives each agent a fresh container and git branch, exposes complete command logs, and lets a human drop directly into the agent terminal when needed.

For the current ATeam question, the important part is not the MCP orchestration layer but the execution model:

- fresh container per agent
- branch-per-agent review flow
- direct observability into what commands actually ran
- interactive takeover when an agent gets stuck

That makes it a strong reference for **inspectable containerized sessions**, especially if ATeam later wants a human to be able to attach to a running sandbox rather than only waiting for the final result.

**Apple Container (macOS 26+):**

Apple's open-source container runtime, released at WWDC 2025 (v0.11.0 as of March 2026). Each container gets its own micro-VM via the Virtualization Framework — VM-level isolation rather than shared-kernel containers. Written in Swift, optimized for Apple Silicon. Docker-compatible CLI surface (`run`, `build`, `exec`, `push`, `pull`), OCI-compatible images. No daemon — uses launchd and XPC. See §E.13.8 for full technical details and ATeam assessment.

**Cloudflare Sandbox SDK:**

[cloudflare/sandbox-sdk](https://github.com/cloudflare/sandbox-sdk) — runs sandboxed Linux containers on Cloudflare's edge network. Each sandbox is a full Linux environment with its own filesystem, network, and process isolation. Claude Code can be run inside via their template.

Relevant for ATeam's remote access story: if agents run on Cloudflare's edge, remote access is built-in (HTTPS endpoint per sandbox), and there's zero exposure of the development machine. The tradeoff is that the project source must be synced to the remote sandbox.

**Dev Containers (containers.dev):**

The [Development Container Specification](https://containers.dev/) is an open standard for defining reproducible development environments inside containers. A `devcontainer.json` file describes the image, features, environment variables, port forwarding, and lifecycle hooks. The spec is backed by Microsoft, GitHub, and JetBrains, and supported in VS Code, IntelliJ, Codespaces, and CI systems.

The [devcontainer CLI](https://github.com/devcontainers/cli) (`npm install -g @devcontainers/cli`) is a reference implementation that can be used programmatically outside any editor:

- `devcontainer up --workspace-folder .` — builds and starts the container from `devcontainer.json`
- `devcontainer exec --workspace-folder . <cmd>` — runs a command inside the running container (wraps `docker exec` with user/workdir awareness)
- `devcontainer build --workspace-folder .` — pre-builds the image for caching in CI

Lifecycle hooks in devcontainer.json allow setup automation: `postCreateCommand` (runs after container creation — install deps, configure tools), `postStartCommand` (runs on every start), and `initializeCommand` (runs on host before container start). Features are composable add-ons (e.g., `ghcr.io/devcontainers/features/node:1`, `ghcr.io/devcontainers/features/go:1`) that layer tools into the container without custom Dockerfiles.

**Why devcontainers matter for ATeam:**

The community is already converging on devcontainers as the standard way to sandbox AI coding agents. Articles like ["How to Safely Run AI Agents Inside a DevContainer"](https://codewithandrea.com/articles/run-ai-agents-inside-devcontainer/) and ["Running AI Agents in Devcontainers"](https://markphelps.me/posts/running-ai-agents-in-devcontainers/) describe the exact pattern ATeam needs: a project checks in its `.devcontainer/devcontainer.json`, and the agent is invoked via `devcontainer exec`. This gives several advantages over raw Docker:

1. **Standardized config**: projects already ship devcontainer.json for human developers — ATeam agents can reuse the same environment, guaranteeing they see the same toolchain, dependencies, and settings the developer uses.
2. **Composable features**: instead of maintaining ATeam-specific Dockerfiles, the agent's needs (Node, Go, Python, Claude Code) are declared as features. Adding a tool is one line, not a Dockerfile rebuild.
3. **Lifecycle hooks**: `postCreateCommand` can run `npm install`, `go mod download`, etc. — the agent starts in a ready-to-work state without explicit setup prompts.
4. **CI parity**: the same devcontainer.json can drive GitHub Actions (via [devcontainers/ci](https://github.com/devcontainers/ci)), local development, and ATeam agents.
5. **Ecosystem momentum**: editor support (VS Code, IntelliJ), pre-built feature registry, and growing community adoption make this a low-risk bet.

**Tradeoffs vs raw Docker:**

- Adds a dependency on the devcontainer CLI (Node.js-based, ~50MB).
- Slightly less control than a hand-crafted Dockerfile (the feature system has opinions).
- The spec is still evolving (lifecycle hooks for features were only recently added).
- If a project doesn't already have a devcontainer.json, ATeam would need to generate one or fall back to raw Docker.

**Recommendation:** Adopt devcontainers as ATeam's primary Docker strategy. For projects that ship a devcontainer.json, use it as-is. For projects without one, ATeam generates a minimal devcontainer.json with the detected language features. Raw Docker remains available as a fallback for advanced use cases (custom networking, multi-container services). The abstraction should be: `devcontainer exec` is the default, `docker run` is the escape hatch.

### E.9 Comparison Across Dimensions

| Approach | Filesystem R | Filesystem W | Network | Tools Work | `~/.claude` | Remote Access | Overhead | Platform |
|---|---|---|---|---|---|---|---|---|
| **Anthropic SRT** | Allow all, deny list | CWD only, allow list | Domain proxy | Most | Auto | None | Minimal | macOS, Linux |
| **neko-kai** | Deny ~, allow project | Project + tmp | None | Most | Explicit | None | Zero | macOS only |
| **claude-sandbox (kohkimakimoto)** | Broad reads | Workdir + `~/.claude` + `/tmp` | None | Very good | Built-in | None | Low | macOS only |
| **cco** | Configurable | Configurable | Depends on backend | Most | Handled | None | Near-zero | macOS, Linux |
| **ClaudeCage** | Deny ~, allow project | Project | Linux ns | Most | Packaged | None | Minimal | Linux only |
| **scode** | Block 35+ cred paths | CWD | None | Most | Handled | None | Zero | macOS, Linux |
| **Docker** | Mount only | Mount only | iptables/ns | All (in container) | Mount | Via port/tunnel | Medium | Any |
| **Apple Container** | VM isolation | VM isolation | VM networking | All (in VM) | Mount | Via port/tunnel | Low | macOS 26+ |
| **Cloudflare Sandbox** | Full isolation | Full isolation | Configurable | All (in container) | API key | Built-in HTTPS | High (remote) | Cloud |
| **Dev Container** | Mount only | Mount only | Container ns | All (in container) | Mount | Via port/tunnel | Medium | Any |
| **Greywall** | Default-deny, allowlist | Default-deny, allowlist | Transparent proxy + DNS (Linux); proxy-only (macOS) | Most | Configurable | None | Low | Linux, macOS |
| **Fence** | Practical allow/deny; /usr /lib /bin /etc readable | Allow/deny with always-protected targets | Default-deny outbound; domain rules; localhost controls | Most | Configurable | None | Low | Linux, macOS |
| **clampdown** | Workdir + system dirs RX, rest blocked | Workdir only; masked .env/.npmrc | Agent allowlist; RFC1918/loopback/link-local blocked | All (in container) | Masked | None | Medium | Linux only |
| **jailoc** | Explicit mounts only | Explicit mounts only | Private networks blocked by default | All (in container) | Mount | None | Medium | Linux, macOS (Docker) |
| **Anthropic Devcontainer** | Bind mount workspace | Bind mount workspace | iptables ipset (IP-based, resolved at start) | All (in container) | Passed in | None | Low-Medium | Any Docker host |
| **Docker Sandboxes (Docker Inc.)** | VM + bidirectional sync | VM + bidirectional sync | Proxy with domain allow/deny; private nets blocked | All (in VM + private Docker) | Injected by proxy (on host) | None | Medium | macOS, Windows (exp.) |
| **VibeBox** | VM isolation (explicit mounts) | VM isolation (explicit mounts) | Full (no filtering) | All (in VM) | Not mounted | None | Low–Medium | macOS (Apple Silicon) |

### E.10 Network Tier Implementation

How to implement each network tier with available tools:

**Tier 1: Full network + tight filesystem** (research agents, documentation agents)

- Use Anthropic SRT or neko-kai for filesystem isolation.
- Don't restrict network — `allowedDomains: ["*"]` or equivalent.
- The filesystem sandbox IS the security boundary.
- Ideal for agents that need to browse docs, check APIs, research libraries.

**Tier 2: Developer network** (build/test agents)

- Use Anthropic SRT with `allowedDomains` covering:
  - `api.anthropic.com` (LLM)
  - `registry.npmjs.org`, `pypi.org`, `files.pythonhosted.org`, `proxy.golang.org`, `crates.io`, `rubygems.org` (package managers)
  - `github.com`, `*.github.com` (git operations)
  - `localhost`, `127.0.0.1` (local services)
- Or Docker with iptables allowlist (Anthropic devcontainer pattern from §7.1).
- Per-project port access: read `PORT`, `DATABASE_URL`, etc. from `.env` and add those ports to the localhost allowlist.

**Tier 3: Minimal network** (sensitive codebases)

- `allowedDomains: ["api.anthropic.com"]` only.
- All dependencies must be pre-installed (no runtime package fetching).
- Docker: default-deny iptables with only the API endpoint whitelisted.
- This is the only tier appropriate for codebases with trade secrets.

**Local port access** (cross-cutting):

- Read `.env` or `config.toml` for port mappings.
- In Docker: `--network host` (simple) or explicit port mapping.
- In Anthropic SRT: localhost is accessible by default (no domain restriction on loopback).
- Specific port restriction isn't natively supported by any tool — would need a custom proxy rule.

### E.11 Remote Access Patterns

Running agents unattended requires a way to check in, give instructions, and review progress without exposing the development machine.

**Pattern A: Tunnel to agent session** (simplest)

- Agent runs locally in a sandbox.
- Expose the session via a tunnel service (VibeTunnel, ngrok, Cloudflare Tunnel, bore).
- The tunnel only exposes the agent's terminal/API, not the host filesystem.
- Risk: if the sandbox is compromised, the tunnel gives the attacker a foothold. The sandbox must be the trust boundary.

**Pattern B: Agent runs remotely** (strongest isolation)

- Agent runs on a remote machine or cloud service (Cloudflare Sandbox, Daytona, GitHub Codespaces).
- Project source is synced to the remote environment.
- Remote access is built-in (HTTPS, SSH, web terminal).
- Development secrets are in the remote environment only — not on the local machine.
- Tradeoff: latency, sync complexity, cost.

**Pattern C: Docker + web terminal** (middle ground)

- Agent runs in Docker on the local machine.
- A web terminal (ttyd, wetty) or REST API inside the container provides remote access.
- Docker's network and filesystem isolation ensures the agent can't reach the host.
- sandbox-agent's SSE/REST pattern (§C.9) or OpenHands' web UI are examples of this.

For ATeam, Pattern C is the likely sweet spot: Docker provides strong isolation, a web terminal provides remote access, and the agent's filesystem is limited to bind-mounted volumes.

### E.12 Recommendation for ATeam

**Layer the approaches based on trust level:**

1. **Coordinator** (runs on host, trusted): Anthropic Sandbox Runtime with filesystem deny-list (adopt scode's 35+ credential path list) + developer-tier network. No Docker — the coordinator needs to call the `ateam` CLI and read/write the org directory.

2. **Sub-agents in Docker** (untrusted, default): Docker container with bind-mounted project worktree. iptables firewall for network tier. `--dangerously-skip-permissions` inside the container (Docker IS the sandbox). Web terminal or REST API for remote access.

3. **Sub-agents without Docker** (lightweight option): Anthropic Sandbox Runtime with filesystem restricted to the project directory + `~/.claude` + toolchain paths. Network tier via the proxy. For simple projects that don't need databases or services. On macOS, `kohkimakimoto/claude-sandbox` is also a credible lightweight option when the goal is "predictable write restriction with a pragmatic escape hatch," not full network control.

4. **Remote access** for all modes: web terminal (ttyd) exposed via an authenticated tunnel. The agent sees only its sandbox; the remote user sees only the agent's terminal.

**The auto-detection pattern from cco is worth adopting**: the same `ateam run` command should automatically select the best available sandbox — Anthropic SRT on host, Docker when configured, Apple Container on macOS 26+. The container adapter abstraction (§7.4) already supports this.

### E.13 Additional Native Sandbox Tools

#### E.13.1 Greywall

**Platform:** Native Linux + macOS (Linux has transparent proxy + DNS capture; macOS loses these)

The closest native tool to default-deny path allowlisting. Only the working directory is accessible unless you explicitly allow more, with `defaultDenyRead`, `allowRead`, and `allowWrite` config. On Linux, it integrates a transparent proxy (Greyproxy) with DNS capture for network control; on macOS it runs natively but without those network features.

Key features:
- **Default-deny filesystem** for both reads and writes — strongest native posture
- **Built-in agent/toolchain profiles** for common stacks
- **Learning mode** that auto-generates least-privilege profiles by tracing actual access patterns — useful for bootstrapping per-project policies
- **Localhost outbound and bind toggles** for Postgres/Redis/dev servers

Limitation: in Docker/CI environments, missing `CAP_NET_ADMIN` removes network-namespace isolation.

**Assessment:** Best all-around native generalist. If ATeam wants a single native sandbox foundation on the host, Greywall is the strongest current option, especially on Linux where the network story is complete.

#### E.13.2 Fence

**Platform:** Native Linux + macOS

Developer-friendly policy layer rather than hard containment. Practical path allow/deny rules, but not pure deny-everything: `/usr`, `/lib`, `/bin`, `/etc` stay readable, and always-protected targets reduce common persistence vectors.

Key features:
- **Best turnkey dev policy**: code template includes package registries and command restrictions out of the box
- **Monitor mode** for auditing what `npm install` or `pip install` actually tries to access before committing to a policy
- **Command rules** that can block `git push`, `npm publish`, and dangerous Docker invocations
- **Default-deny outbound network** with domain rules and localhost outbound/bind controls
- **Unix-socket controls** for Docker socket and similar

Positioned as defense-in-depth, not a hostile-code boundary. The package-manager safety and guardrails around npm/pip/git are the strongest of any tool reviewed.

**Assessment:** Best for layering dev-specific command/package policies on top of a filesystem sandbox. ATeam should borrow Fence's package-manager and command-policy patterns regardless of which filesystem sandbox is used.

#### E.13.3 clampdown

**Platform:** Linux only (macOS only via Linux VM: Colima/Podman machine; Docker Desktop explicitly unsupported)

The hardest container-based isolation option reviewed. Designed for environments where blast-radius reduction matters more than developer convenience.

Key features:
- **Workdir RWX, system dirs RX, everything else blocked**
- **Protected and masked sensitive paths** (`.env`, `.npmrc`, etc.) propagate to nested containers
- **Network**: agent allowlist; RFC1918/loopback/link-local/ULA blocked for both agent and tool containers
- **Optional image-digest enforcement** for supply-chain protection
- **Project-only access** — no access to host home or other projects

The RFC1918/loopback/link-local blocking makes it explicitly unsuitable for host-local integration tests (Postgres, Redis, dev servers). Use it when maximal containment matters more than localhost connectivity.

**Assessment:** Strongest Linux hardening choice. Reserve for high-security agent work where the agent should have zero access to host services or private networks.

#### E.13.4 jailoc

**Platform:** Linux, macOS (via Docker)

Container-based sandboxing focused on explicit workspace isolation:
- Only exposes explicitly mounted directories — nothing else from the host
- Blocks private networks by default
- Per-workspace DinD sidecar instead of sharing the host Docker socket
- Ships a pinned default image with Node.js, Python 3, npm, and common language servers

Relevant if OpenCode is the baseline agent. Good isolation model for ATeam if the explicit-mount-only pattern is desired.

#### E.13.5 Anthropic Reference Devcontainer

**Source:** [anthropics/claude-code/.devcontainer](https://github.com/anthropics/claude-code/tree/main/.devcontainer)
**Docs:** [code.claude.com/docs/en/devcontainer](https://code.claude.com/docs/en/devcontainer)
**Platform:** Any Docker host

Anthropic's official reference Docker setup for running Claude Code in a container. A standard `node:20` container (not a microVM) with an iptables-based network firewall. Three components: `devcontainer.json`, `Dockerfile`, and `init-firewall.sh`.

**Filesystem model:**
- Workspace bind-mounted from host (`source=${localWorkspaceFolder},target=/workspace`)
- Named volumes for bash history and Claude config persistence
- Host `~/.ssh`, `~/.aws`, etc. are NOT mounted — invisible to the agent

**Network isolation (`init-firewall.sh`):**

The firewall script implements a default-deny iptables policy:
- Allows DNS (UDP 53), SSH (TCP 22), localhost loopback, host network (auto-detected)
- Creates an `ipset` of allowed domains by DNS-resolving them at startup: GitHub (via `api.github.com/meta` IP ranges), `registry.npmjs.org`, `api.anthropic.com`, `sentry.io`, VS Code marketplace domains
- Sets `iptables -P OUTPUT DROP` — everything not in the allowlist is rejected
- Requires `--cap-add=NET_ADMIN` and `--cap-add=NET_RAW` for firewall setup

**Key limitation:** IP-based firewall (DNS resolved at startup). If IPs change after container start, access breaks until restart. No Docker-in-Docker support (no Docker socket mounted). Credentials passed into the container are exposed if a malicious project exfiltrates them.

**Intended usage:** Run `claude --dangerously-skip-permissions` inside. The container boundary + iptables firewall IS the safety layer.

**Assessment for ATeam:** Good reference implementation for Docker-based agent isolation. The iptables/ipset pattern is a practical alternative to proxy-based domain filtering for containers. The IP-at-startup resolution is a weakness for long-running agents, but fine for task-scoped runs.

#### E.13.6 Docker Desktop Sandboxes (Docker Inc.)

**Docs:** [docs.docker.com/ai/sandboxes](https://docs.docker.com/ai/sandboxes/)
**Platform:** macOS (Apple Virtualization.framework), Windows experimental (Hyper-V); Linux gets legacy container-based sandboxes (Docker Desktop 4.57+)
**Requires:** Docker Desktop 4.58+

This is **Docker Inc.'s product**, not Anthropic's. Each sandbox is a lightweight **microVM** (not a container), running its own Linux kernel and its own private Docker daemon.

**Container vs Sandbox — the security model difference:**

| Dimension | Regular Docker container | Docker Sandbox (microVM) |
|---|---|---|
| **Kernel** | Shares host kernel — kernel exploits can escalate to host | Own kernel inside VM — kernel exploit stays in sandbox |
| **Docker daemon** | Shared daemon (or `--privileged` DinD shares host kernel) — agent can see host containers/images | Private daemon per sandbox — agent sees only its own containers |
| **Visibility** | Shows in `docker ps` | Does not appear in `docker ps`; use `docker sandbox ls` |
| **Image/layer sharing** | Shared between containers on same daemon | No sharing — each sandbox has its own storage |
| **Isolation boundary** | Linux namespaces + cgroups (process-level) | Hardware hypervisor (Apple Virtualization.framework / Hyper-V) |
| **Escape surface** | Container escapes are a known, well-studied attack class | Requires VM escape — significantly harder |

The fundamental difference: containers isolate processes, sandboxes isolate kernels. A compromised container can potentially reach the host through kernel exploits or misconfigured capabilities. A compromised sandbox is still trapped in its own VM.

**CLI:**
```
docker sandbox run claude ~/my-project           # run Claude Code in a sandbox
docker sandbox run claude ~/project ~/docs:ro    # multiple workspaces, read-only option
docker sandbox ls                                 # list sandboxes
docker sandbox exec -it <name> bash               # shell into a running sandbox
docker sandbox rm <name>                           # remove a sandbox
```

**Filesystem model — bidirectional file sync (not bind mounts):**
- Files sync between host and VM at identical absolute paths
- Changes propagate in both directions automatically
- Path consistency ensures error messages match between environments
- When a sandbox is removed, the VM is deleted but workspace changes have already synced back

**Docker-in-Docker:**

Each sandbox has its own private Docker daemon. Agents can `docker build`, `docker run`, `docker compose` — all inside the sandbox VM. No access to the host Docker daemon, host containers, or host images. This is the only sandboxing solution reviewed that provides safe Docker-in-Docker.

**Network policy:**

An HTTP/HTTPS filtering proxy runs on the host at `host.docker.internal:3128`. All agent traffic passes through it.

Default policy (allow mode):
- All internet traffic allowed except private networks and metadata services
- Blocked by default: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `169.254.0.0/16`, IPv6 link-local/ULA
- `*.anthropic.com` and `platform.claude.com:443` pre-allowed

Deny mode (strict):
```
docker sandbox network proxy <name> --policy deny --allow-host api.anthropic.com --allow-host "*.npmjs.org"
```

Additional features:
- **Credential injection:** the proxy injects API keys (Anthropic, OpenAI, GitHub) so credentials stay on the host, never inside the VM
- **HTTPS bypass:** `--bypass-host` for certificate-pinned services (loses MITM inspection)
- **Monitoring:** `docker sandbox network log [--json]` shows access patterns
- **Config persistence:** per-sandbox at `~/.docker/sandboxes/vm/<name>/proxy-config.json`; defaults at `~/.sandboxd/proxy-config.json`

**Key limitations:**
- Domain fronting can bypass HTTPS filters
- No inter-sandbox networking
- Sandboxes are persistent until explicitly removed; multiple sandboxes do not share images or layers

**Assessment for ATeam:** Strongest isolation of any Docker-based approach (hypervisor-level). The only solution with safe DinD and credential injection. The right choice when agents need to build/test Docker images.

ATeam's current approach uses `--privileged` DinD containers, which gives each agent its own Docker daemon but still shares the host kernel. Docker Sandboxes would give stronger isolation boundaries per agent at the cost of higher resource overhead (a full microVM + daemon per sandbox). This overhead is significant if running many agents in parallel. The Linux limitation (no microVM, only legacy containers) is also relevant since ATeam targets Linux CI environments.

**Constraints:** Docker Desktop license requirement for large orgs. microVM only on macOS/Windows; Linux falls back to legacy containers which lose the kernel isolation advantage.

**References:**
- [Docker Sandboxes product page](https://www.docker.com/products/docker-sandboxes/)
- [Architecture docs](https://docs.docker.com/ai/sandboxes/architecture/)
- [Getting started](https://docs.docker.com/ai/sandboxes/get-started/)
- [Docker blog announcement (Jan 2026)](https://www.docker.com/blog/docker-sandboxes-run-claude-code-and-other-coding-agents-unsupervised-but-safely/)
- [Containers vs MicroVMs comparison (Ajeet Raina, Feb 2026)](https://www.ajeetraina.com/docker-sandboxes-containers-vs-microvms-when-to-use-what/)
- [Sandboxed container technologies overview (Palo Alto Unit 42)](https://unit42.paloaltonetworks.com/making-containers-more-isolated-an-overview-of-sandboxed-container-technologies/)
- [AI agent sandboxing strategies (Northflank, Feb 2026)](https://northflank.com/blog/how-to-sandbox-ai-agents)

#### E.13.7 VibeBox

**GitHub:** [robcholz/vibebox](https://github.com/robcholz/vibebox)
**Language:** Rust
**Platform:** macOS (Apple Silicon only — uses Apple Virtualization Framework)

A per-project micro-VM sandbox for running coding agents. Each project gets its own lightweight Linux VM with explicit mount allowlists, `.git` masking, and session reuse. Written in Rust, focused on fast iteration: warm re-entry is ~5 seconds on M3.

**How it works:**

- Running `vibebox` from any project directory starts or reattaches to a Debian-based micro-VM for that project.
- Uses Apple's Virtualization Framework — each VM has its own kernel, not a shared-kernel container.
- The project directory is mounted read-write at `~/<project-name>` inside the guest. All other host paths are invisible unless explicitly declared in `vibebox.toml`.
- `.git` directories are masked with tmpfs to prevent accidental modifications from the guest.
- Multi-terminal sessions: multiple shells can attach to the same running VM.

**Configuration (`vibebox.toml`):**

```toml
cpu_count = 2
ram_mb = 2048
disk_gb = 5
auto_shutdown_ms = 20000

[[mounts]]
host = "~/shared-libs"
guest = "~/shared-libs"
mode = "read-only"
```

Mounts use `host:guest[:mode]` format. Host paths support tilde expansion. Only explicitly listed paths are mounted — default-deny for anything outside the project.

**Guest environment:**

- Debian base image (downloaded and cached on first run at `~/.cache/vibebox`)
- SSH user: `vibecoder`
- Pre-installed: build tools, git, curl, ripgrep, openssh-server
- First login provisions `mise` with `uv`, `node`, `codex`, and `claude-code`

**State:**

- Per-project: `.vibebox/` (disk image, SSH keys, logs, manager socket)
- Global cache: `~/.cache/vibebox` (base image)
- Session index: `~/.vibebox/sessions`

**CLI:**

```
vibebox              # start/attach to project VM
vibebox list         # show known sessions
vibebox reset        # delete .vibebox/ and recreate on next run
vibebox purge-cache  # remove global cache
vibebox explain      # show resolved mounts and network config
```

**Security model:**

- VM-level isolation (own kernel) — stronger than container-based approaches
- Explicit mount allowlists — nothing from the host is visible unless declared
- `.git` masking via tmpfs — prevents accidental repo mutation from the guest
- No network filtering — the VM has full outbound access

**Comparison with similar tools:**

- vs **Apple Container** (E.8): both use Apple Virtualization Framework for micro-VMs. VibeBox is higher-level — project-aware sessions, mount management, agent provisioning. Apple Container is a lower-level runtime.
- vs **Docker Sandboxes** (E.13.6): Docker Sandboxes add network filtering, credential injection, and DinD. VibeBox is simpler — no Docker dependency, no network policy, but also no DinD.
- vs **neko-kai** / **kohkimakimoto** (E.3–E.4): those are Seatbelt wrappers (process-level sandboxing). VibeBox provides full VM isolation at the cost of higher overhead and macOS-only / Apple Silicon-only support.

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | VM boundary + explicit mount allowlists. Default-deny. |
| Network control | None | Full outbound access, no filtering. |
| Tool compatibility | Excellent | Full Linux VM — everything works. |
| `~/.claude` access | Not mounted | Would need explicit mount in `vibebox.toml`. |
| Remote access | None | No built-in remote story. |
| Overhead | Low–Medium | Micro-VM startup ~5s warm. Lighter than full Docker but heavier than Seatbelt. |
| Platform | macOS Apple Silicon only | Not usable on Linux CI or Intel Macs. |

**Best fit:** macOS Apple Silicon development where VM-level filesystem isolation is wanted without Docker overhead, and network filtering is not a concern. The per-project session model with `.git` masking is well-suited to agent workflows where you want the agent to work in an isolated copy without risking host repo state.

#### E.13.8 Apple Container

**GitHub:** [apple/container](https://github.com/apple/container)
**Language:** Swift (CLI + services), built on the [Containerization](https://github.com/apple/containerization) Swift package
**Platform:** macOS 26+ (Apple Silicon only)
**License:** Apache-2.0
**Status:** Active development, v0.11.0 (March 2026). Breaking changes possible until 1.0.

Apple's open-source container runtime where each container runs inside its own lightweight VM via the macOS Virtualization Framework. Not a Docker wrapper — a ground-up implementation with a Docker-compatible CLI surface and OCI image compatibility.

**Architecture:**

```
container CLI
  └─ container-apiserver (launchd agent, manages container/network resources)
       ├─ container-core-images (XPC helper: image management, local content store)
       ├─ container-network-vmnet (XPC helper: virtual networking via vmnet framework)
       └─ container-runtime-linux (per-container runtime management)
            └─ Lightweight VM (Virtualization Framework) ← one per container
```

Each container gets a dedicated VM with its own Linux kernel, not a shared kernel with namespace isolation. This gives "the isolation properties of a full VM, using a minimal set of core utilities and dynamic libraries."

**CLI (Docker-compatible surface):**

Core operations map directly to Docker equivalents:

```bash
container run [-d] [-it] [-p 8080:80] [-v host:guest] [-e KEY=VAL] [-m 4g] [-c 4] image [cmd]
container build [-f Dockerfile] [-t tag] [--platform linux/arm64,linux/amd64] .
container exec [-it] <name> <cmd>
container stop/kill/rm <name>
container ls [-a]
container logs [--follow] <name>
container stats <name>
container inspect <name>
```

Image management: `container image pull/push/ls/rm/save/load/tag/inspect/prune`
Volume management: `container volume create [-s size]/ls/rm/inspect/prune`
Network management: `container network create [--subnet]/ls/rm/inspect/prune`
System management: `container system start/stop/status/version/logs/df`

**Filesystem model:**

- **Bind mounts**: `--volume host-path:container-path` or `--mount source=path,target=path` — same syntax as Docker.
- **Named volumes**: `container volume create` with optional size. Persist across container runs.
- **SSH agent forwarding**: `--ssh` mounts the host SSH socket automatically (equivalent to `--volume "${SSH_AUTH_SOCK}:/run/host-services/ssh-auth.sock"`).

**Networking:**

- Uses macOS vmnet framework for virtual networking.
- **Port forwarding**: `-p [host-ip:]host-port:container-port[/protocol]` — standard Docker syntax.
- **Host access from containers**: custom DNS domains via `sudo container system dns create host.container.internal --localhost <ip>`.
- **Isolated networks**: `container network create` with custom subnets. Networks are isolated from one another.
- **Container-to-container**: supported on macOS 26+ via user-defined networks. Not available on macOS 15.
- **Default subnet**: 192.168.64.1/24, configurable via `container system property set network.subnet`.

**Resource management:**

- Default: 1 GB RAM, 4 CPUs per container.
- Override: `--memory 32g --cpus 8`.
- Builder (BuildKit) has separate limits: `container builder start --cpus 8 --memory 32g`.
- Memory ballooning incomplete — freed guest pages are not relinquished to the host. Restart containers for memory-intensive workloads.

**Build system:**

- Full Dockerfile/Containerfile support via BuildKit running in its own isolated VM.
- Multi-platform builds: `--arch arm64 --arch amd64` (multiple `--arch` flags).
- `--target` for multi-stage builds, `--build-arg`, `--no-cache`, `--pull`.

**Advanced features:**

- **Nested virtualization**: M3+ only, `--virtualization` flag with custom kernel.
- **Custom init images**: `--init-image` for custom boot-time logic, additional daemons.
- **`--init` flag**: lightweight PID 1 for signal forwarding and orphan reaping.

**Security model:**

- VM-level isolation per container — kernel exploits in the guest don't reach the host.
- Only explicitly mounted paths are visible inside the VM.
- Registry credentials stored in macOS Keychain.
- No `--privileged` mode — the VM boundary makes it unnecessary.
- OCI image compatibility means standard base images work without modification.

**Comparison with Docker:**

| Dimension | Apple Container | Docker (on macOS) |
|---|---|---|
| **Isolation** | VM per container (Virtualization Framework) | Shared Linux VM (all containers share one kernel) |
| **Daemon** | Launchd agents + XPC (native macOS services) | Docker Desktop daemon in a Linux VM |
| **Image format** | OCI-compatible | OCI-compatible |
| **CLI** | Near-identical to Docker CLI | Docker CLI |
| **Networking** | vmnet framework (native macOS) | Docker Desktop's virtualized networking |
| **Build** | BuildKit in isolated VM | BuildKit in shared VM |
| **Licensing** | Apache-2.0, no commercial restrictions | Docker Desktop: commercial license for large orgs |
| **DinD** | Nested virtualization (M3+) | `--privileged` DinD or Docker-in-Docker images |
| **Platform** | macOS 26+ / Apple Silicon only | macOS, Linux, Windows |

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | VM boundary. Only explicit mounts visible. |
| Network control | Good | Port forwarding, isolated networks, DNS. No domain-level filtering (would need a proxy layer). |
| Tool compatibility | Excellent | Full Linux VM with OCI images — everything works. |
| `~/.claude` access | Via mount | `--volume ~/.claude:/root/.claude` or equivalent. |
| Remote access | Via port/tunnel | No built-in remote UI. Expose via port forwarding + tunnel. |
| Overhead | Low | Lighter than Docker Desktop (no daemon VM). Heavier than Seatbelt wrappers. |
| Platform | macOS 26+ / Apple Silicon | Not usable on Linux CI or Intel Macs. |
| DinD | M3+ only | Nested virtualization requires M3 or later. |
| Maturity | Pre-1.0 | Active development, breaking changes expected. |

**Best fit:** macOS Apple Silicon development and local agent runs where Docker Desktop licensing is undesirable. The Docker-compatible CLI means existing Dockerfiles and workflows port over with minimal changes. Stronger per-container isolation than Docker (VM vs shared kernel). The main gaps are: no built-in domain-level network filtering (needs an external proxy), macOS 26+ requirement, and pre-1.0 stability.

**For ATeam:** Apple Container is the likely successor to Docker Desktop for macOS-based agent sandboxing. The VM-per-container model gives isolation parity with Docker Sandboxes (E.13.6) without the Docker Desktop license. The CLI compatibility means ATeam's container adapter can support it as a drop-in backend. The pre-1.0 status and macOS 26+ requirement mean it's not ready as the primary backend today, but worth tracking for the next macOS cycle.

### E.14 Network Companion Tools

**OpenSnitch** (Linux) and **Little Snitch** (macOS) provide per-app/domain outbound firewalling as a standalone layer. They don't provide filesystem sandboxing but can be paired with any of the above tools:

- **OpenSnitch**: interactive per-app/domain filtering with rule persistence
- **Little Snitch**: app/domain/server/port/protocol rules with a GUI

Useful as a defense-in-depth network layer when the primary sandbox doesn't have strong network controls (e.g., neko-kai, kohkimakimoto/claude-sandbox, scode).

### E.15 Updated Recommendation Summary

The March 2026 landscape adds Greywall, Fence, and clampdown as meaningful options beyond the original E.12 recommendation. The updated guidance:

- **Best native generalist (host sandbox):** Greywall — replaces or supplements Anthropic SRT as the primary host sandbox, especially on Linux where the network story is strongest. On macOS, Anthropic SRT or Greywall are comparable.
- **Package-manager and command safety:** Borrow Fence's policy patterns (command restrictions, monitor mode, registry allowlists) regardless of which sandbox is used.
- **Localhost integration tests in containers:** tsk-tsk is the most practical choice — `host_ports` + `TSK_PROXY_HOST` provide clean host-service access that clampdown and Docker Sandboxes explicitly block.
- **Docker-in-Docker isolation:** Docker Desktop Sandboxes (Docker Inc.) — the only solution with safe DinD via private daemon per sandbox, plus credential injection that keeps API keys off the VM. Requires Docker Desktop license; microVM only on macOS/Windows.
- **Reference Docker setup:** Anthropic's devcontainer is a good starting point for iptables-based container isolation without Docker Desktop. IP-based firewall is less flexible than proxy-based domain filtering but simpler to audit.
- **Maximum Linux hardening:** clampdown when the agent needs zero access to host services or private networks.
- **Network-only companion:** Pair Linux hosts with OpenSnitch, macOS with Little Snitch, for domain-level outbound filtering alongside any filesystem sandbox.
