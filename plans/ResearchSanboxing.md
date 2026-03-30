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

- On **macOS**: generates a dynamic Seatbelt profile (`.sb` file) and executes via `sandbox-exec`. The profile is regenerated per invocation based on the configured policy.
- On **Linux**: uses [bubblewrap](https://github.com/containers/bubblewrap) to create a restricted namespace. Requires `bubblewrap` and `socat` packages.
- **Network proxy**: all network traffic is forced through a unix domain socket to a proxy running outside the sandbox. The proxy enforces domain allowlists. This is how network isolation works without iptables or network namespaces on macOS.

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

- `claude-sandbox` is a drop-in replacement for `claude`. You can run `claude-sandbox --dangerously-skip-permissions` and get Seatbelt-enforced write confinement around that session.
- Configuration is TOML-based with **three scopes**: user (`~/.claude/sandbox.toml`), project (`.claude/sandbox.toml`), and local overrides (`.claude/sandbox.local.toml`).
- The default profile is intentionally simple: **deny file writes globally**, then allow writes to the working directory, `~/.claude`, and `/tmp`.
- The tool exposes useful introspection commands: `claude-sandbox profile` shows the generated Seatbelt profile and `claude-sandbox config` shows the merged effective config.

**Sandbox-external execution (`unboxexec`):**

This is the most distinctive feature. Some tools, especially browser automation stacks like Playwright, do not work cleanly inside nested macOS sandboxes. `claude-sandbox` solves that by starting an internal daemon outside the sandbox and exposing `claude-sandbox unboxexec` inside the sandbox:

- Claude invokes `claude-sandbox unboxexec -- <command> ...`
- The request goes over a Unix domain socket to the daemon
- The daemon runs the command **outside** the sandbox only if it matches an allowlisted regex in `[unboxexec].allowed_commands`

This is a deliberate escape hatch, not a bug. It makes the wrapper more usable for real development tasks, but it also means the safety posture depends heavily on how tight the `allowed_commands` patterns are.

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

**Best fit**: macOS users who want a predictable write-constrained wrapper with per-project config and occasional explicitly-approved escapes for incompatible tools.

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

**Apple Container (macOS Tahoe / macOS 26+):**

Apple's open-source container runtime, released at WWDC 2025 (v0.9.0 as of February 2026). Each container gets its own micro-VM — better isolation than Docker's shared-kernel model, sub-second startup, no daemon. Written in Swift, optimized for Apple Silicon.

For ATeam on macOS, this is the Docker replacement: lighter, more secure (VM-level isolation per container), no Docker Desktop licensing. Still pre-1.0, but the runtime is solid.

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
