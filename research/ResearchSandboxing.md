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

### D.10 Policy / Approval Layer Tools

A new category that emerged in 2026: tools that sit *between* the agent and the OS, classifying or gating each action against a policy before it reaches a sandbox. They complement (not replace) sandboxes — the sandbox is the wall, the policy layer is the bouncer at the door.

#### D.10.1 Cupcake (eqtylab/cupcake)

**GitHub:** [eqtylab/cupcake](https://github.com/eqtylab/cupcake) — 258⭐, Rust, created July 2025.

OPA/Rego policy enforcement compiled to WebAssembly for fast evaluation. Native integrations for Claude Code, Cursor, Factory AI, OpenCode. The flow:

1. Agent prepares an action (tool call: `git push`, `fs_write`, etc.).
2. Cupcake gathers real-time environmental "Signals" (cwd, branch, recent commits, etc.).
3. Action + signals evaluated against compiled Wasm policies.
4. Decision: **Allow / Modify / Block / Warn / Require Review**.

Key property: policy logic lives outside the agent's context window — it doesn't consume tokens and the agent can't talk it out of a block. Useful for: locking down `git push --force`, `rm -rf /`, network egress to non-allowlisted hosts, and other categorical "never do this" rules.

#### D.10.2 nah (manuelschipper/nah)

**GitHub:** [manuelschipper/nah](https://github.com/manuelschipper/nah) — 417⭐, Python, created March 2026, pushed today.

Context-aware safety guard with deterministic structural classification. Distinguishes commands that look similar but mean different things:

- `rm -rf __pycache__` → **allowed** (cleanup of a build artifact).
- `rm ~/.bashrc` → **paused** (shell startup file, asks for confirmation).
- `base64 -d payload | bash` → **blocked** (hidden code execution pattern).
- `git status` vs `git push --force` → treated differently despite being "the same Git command."

Zero-config out of the box. Customisable via `~/.config/nah/config.yaml` (global) or `.nah.yaml` (per-project). Optional LLM layer can review ambiguous cases, but deterministic blocks cannot be relaxed by LLM. Integrates as a Claude Code hook or as a Claude Code plugin; guards Bash, file, search, notebook, and MCP tool calls.

#### D.10.3 Other entries (tracked, not yet evaluated)

From wincent's gist (E.18): predicate-secure (authz + post-run verification), claude-rule-enforcer (behavior rules), shannot (human-in-the-loop approvals), punkgo-jack (Merkle-logged audit receipts), deepclause-sdk (DML-style runtime authz). All worth a closer look if ATeam needs a richer policy story than hooks alone.

#### D.10.4 Where this layer fits in ATeam

ATeam already has a permission story (D.5, D.7) based on Claude Code hooks. The policy-layer tools above are a more structured upgrade path:

- **Cupcake** if the policy rules are complex enough to warrant a real policy language (Rego). Best when many rules need to compose, or when audit/compliance needs the policy to be a versioned, reviewable artifact.
- **nah** as a drop-in upgrade over hand-rolled hooks. Its deterministic command classifier is the kind of thing every project ends up writing badly; using a maintained library is a better default. Likely worth adopting as ATeam's recommended hook implementation.
- **Custom hooks** remain the right answer for project-specific ad-hoc rules ("don't touch `infra/prod/*`") that don't justify a policy engine.

The Cupcake/nah layer sits **above** the sandbox layer (E.2–E.13). The sandbox stops the agent from doing damage if a policy fails open; the policy layer stops most actions from ever reaching the sandbox boundary in the first place. They're complementary.

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
| **Eclipse Enclave** | Explicit mounts; home/dotfiles/app data refused | Project RW (or RO) + explicit mounts; Git config RO | Gateway sidecar: DNS + ipset + SNI proxy allowlist, JSONL audit | All (in container), no DinD | Host-side auth store; env tokens as placeholders | HomeShell UI (preview) | Medium | Linux, macOS, WSL2 |
| **Porta** | All but credential stores; strict mode: mounts only | `-v` mounts + `/tmp`; `.git/hooks`, agent settings protected | Open by default; TCP port rules or CONNECT proxy allowlist; placeholders | Most (clients must honor `HTTPS_PROXY`) | Mount via `porta init` | None | Near-zero | macOS, Linux |
| **NVIDIA OpenShell** | Landlock policy in container/VM | Landlock policy | Workload has no network; supervisor enforces per-binary + L7 rules; placeholders | All (in container/VM) | Image + providers | Gateway sessions | Medium–High | Linux, macOS (Apple Silicon), K8s |
| **exe.dev** | VM isolation | VM isolation | `all` or integrations-only; credentials at the edge | All (in VM) | LLM integration | Built-in HTTPS/SSH | High (remote) | Cloud |

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

#### E.13.9 Matchlock

**GitHub:** [jingkaihe/matchlock](https://github.com/jingkaihe/matchlock)
**Language:** Go
**Platform:** Linux (Firecracker) and macOS (Apple Virtualization.framework). Same CLI on both.
**License:** Open source. 565⭐, created Feb 2026, actively pushed.

A microVM sandbox for AI agents that fills two gaps in the existing macOS-native VM tools (Apple Container, VibeBox): **default-deny network with explicit allowlist**, and **in-flight secret injection** so real credentials never enter the VM. Cross-platform with identical UX — same CLI on a Linux server and on a MacBook.

**How it works:**

- Each `matchlock run` boots a fresh Firecracker (Linux) or Virtualization.framework (macOS) VM with its own kernel.
- Project directory mounted as an isolated overlay — writes are snapshotted and discarded when the VM exits unless promoted.
- Network is sealed by default. `--allow-host api.example.com` opens specific egress; everything else is dropped.
- **Secret injection at the network boundary:** declare `--secret OPENAI_API_KEY` and matchlock substitutes a placeholder inside the VM. When the agent makes an HTTPS call to an allowed host, the host-side proxy rewrites the placeholder with the real value before the request leaves the machine. The sandbox never sees the real key. Even if the agent is compromised, exfiltration paths are closed.

**Comparison with existing entries:**

- vs **VibeBox** (E.13.7): VibeBox has stronger session ergonomics (warm re-entry, multi-terminal attach) but no network filtering. Matchlock has weaker session reuse but adds network allowlist + secret injection. Complementary rather than competing.
- vs **Apple Container** (E.13.8): Apple Container is a Docker-CLI-compatible runtime; Matchlock is a purpose-built agent sandbox with network and secret semantics built in. Apple Container needs an external proxy to do what Matchlock does natively.
- vs **Anthropic SRT** (E.2): SRT is process-level (Seatbelt + bubblewrap) on the host; Matchlock is VM-level. Stronger isolation, higher overhead.
- vs **Docker Desktop Sandboxes** (E.13.6): both offer credential injection. Docker Sandboxes need Docker Desktop and have richer DinD support; Matchlock has no Docker dependency and works on Linux servers without Docker Desktop licensing.

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | VM boundary + overlay mounts (changes vanish unless promoted). |
| Network control | Excellent | Default-deny with explicit `--allow-host`. |
| Secret safety | Excellent | In-flight injection — real keys never reach the VM. |
| Tool compatibility | Excellent | Full Linux environment inside the VM. |
| Cross-platform | Excellent | Same CLI on Linux and macOS. Best in this section for portability. |
| Overhead | Medium | VM boot per run; offset by overlay reuse. |
| Maturity | Early | Created Feb 2026; pre-1.0. |

**Best fit:** Cross-platform agent runs where the same sandbox config must work on a developer's MacBook *and* a Linux CI host, and where credential leakage is a real concern (e.g., agents handling production API keys). The secret injection model is worth borrowing even if Matchlock itself isn't adopted — ATeam could implement an equivalent proxy layer for its container adapter.

#### E.13.10 Vibe (lynaghk/vibe)

**GitHub:** [lynaghk/vibe](https://github.com/lynaghk/vibe)
**Language:** Rust
**Platform:** macOS (Apple Silicon, Ventura+) only — Apple Virtualization.framework
**License:** Open source. 889⭐, created Jan 2026.

Distinct from VibeBox (E.13.7) despite the similar name. Vibe is the simplest possible "type a command, end up in a Linux VM in ~10s" workflow for sandboxing LLM agents. No project model, no `.toml` config, no session reuse — just a fresh Debian VM per invocation. Targets users who want zero ceremony.

**How it works:**

- `vibe` from any directory drops you into a Debian VM with the cwd mounted.
- ~10s cold start; VM is destroyed when you exit.
- No persistent project state, no explicit mount syntax — current dir is the only sharing surface.
- Pre-installed: standard build tools, git, common LLM agent CLIs.

**Comparison with VibeBox:**

| Dimension | Vibe | VibeBox |
|---|---|---|
| Session model | Ephemeral, one-shot | Persistent per-project, warm re-entry |
| Configuration | None | `vibebox.toml` with mount allowlists |
| Mount control | Cwd only | Explicit allowlist + `.git` masking |
| Multi-terminal | No | Yes |
| Best for | Throwaway exploration | Daily-driver project workflow |

Both use Apple Virtualization.framework, both are Apple Silicon only, both lack network filtering.

**Assessment for ATeam:** Marginal. Vibe is too unstructured for the ATeam use case (no project awareness, no mount control, no network policy). It's listed here for completeness — if a user wants the absolute lowest-friction macOS VM sandbox to play with, Vibe is the answer; for production agent workflows, VibeBox or Matchlock are better fits.

#### E.13.11 Agent Safehouse

**GitHub:** [eugene1g/agent-safehouse](https://github.com/eugene1g/agent-safehouse)
**Language:** Shell
**Platform:** macOS (Seatbelt / `sandbox-exec`)
**License:** Open source. 1.7K⭐, created Feb 2026, actively maintained.

The current best-in-class macOS-Seatbelt-only wrapper. Pure `sandbox-exec`, no Docker, no user account tricks, no VM. Deny-first composable profiles, agent-agnostic (Claude, Codex, Amp, etc.), Homebrew-installable.

**How it works:**

- Built-in profiles for each major coding agent CLI plus app-hosted workflows.
- Deny-first by default: HOME directory is metadata-only; the agent only sees what you explicitly allow.
- Composable via flags: `--add-dirs-ro <path>` (read-only allowlist), `--add-dirs <path>` (read-write allowlist), `--append-profile <file>` (drop in machine-specific or per-project rules).
- Per-project config layered on top of machine defaults via wrapper functions.

**Comparison with existing Seatbelt entries:**

- vs **Anthropic SRT** (E.2): SRT is Anthropic-official, agent-aware, JSON-configured. Safehouse is community, simpler, profile-file-configured, with Homebrew install and built-in per-agent profiles. Functionally similar; Safehouse leans more toward "ergonomic out-of-the-box" while SRT leans toward "programmable infrastructure."
- vs **neko-kai/claude-code-sandbox** (E.3): neko-kai is a single profile for one agent. Safehouse ships profiles for multiple agents and exposes a flag-driven composition layer.
- vs **kohkimakimoto/claude-sandbox** (E.4): kohkimakimoto is Go + TOML + Claude-only. Safehouse is shell + profile files + agent-agnostic.
- vs **scode** (E.7): both are deny-first wrappers; Safehouse has substantially higher momentum (1.7K vs 17 stars) and richer profile composition.

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | Deny-first; HOME metadata-only by default. |
| Network control | Limited | Documentation focuses on filesystem; no built-in domain-level filtering. Pair with Little Snitch for network. |
| Tool compatibility | Excellent | Same process model as the host — everything works including playwright-cli, dev Chrome, localhost Postgres. |
| Agent-agnostic | Yes | Profiles for Claude, Codex, Amp, etc. |
| Ease of use | Excellent | `brew install`, ready-made profiles, simple flag composition. |
| Per-project config | Yes | Wrapper functions + appended profiles. |
| Momentum | High | 1.7K⭐ in ~3 months, actively pushed. |

**Best fit:** macOS users who want pure Seatbelt sandboxing, no Docker, no VM, and don't want to hand-roll a `.sb` profile. The deny-first composable model is the closest match to the user's stated requirements (macOS Seatbelt + no Docker + ease of use + flexible config + momentum) of anything seen in this research. Likely the recommended starting point for any new ATeam-adjacent macOS sandbox work.

#### E.13.12 Eclipse Enclave

**GitHub:** [eclipse-enclave/enclave](https://github.com/eclipse-enclave/enclave)
**Language:** Go
**Platform:** Linux, macOS (via Docker Desktop), Windows (WSL2 launcher only)
**License:** MIT. Eclipse Foundation project. v1.0.0 released 2026-10-30, v1.0.1 on 2026-10-31; a `rolling` channel tracks `main`. ~94⭐ (2026-11-20 snapshot).
**Reviewed:** 2026-11-20, commit `689c6db`, from the repo docs (`docs/security/`, `networking.md`, `runtime/network-request-flow.md`, `auth.md`, `mounts.md`, `git.md`, `backends.md`, `integration.md`). Not run locally.

A Go CLI that runs one coding agent per container in YOLO mode, with a privileged gateway sidecar that enforces a domain allowlist and logs every network decision. Built-in tools: Claude Code, Codex, Copilot, OpenCode, Pi, and the Theia IDE; more ship in [enclave-extensions](https://github.com/eclipse-enclave/enclave-extensions). It is the most complete "container + egress allowlist + audit log + placeholder secrets" package in this survey. First noted in §F.3 (August 2026, pre-1.0); this entry is the v1.0 review.

Its own security doc is explicit about scope: "Enclave reduces an agent's access to the host and the network; it is not a hardened container-escape boundary."

**Architecture:**

```
enclave (host Go CLI)
  ├─ writes gateway bundle: dnsmasq.conf, domains.txt, secret mapping, CA
  ├─ creates a per-session bridge network
  ├─ gateway container (NET_ADMIN, NET_RAW)
  │    ├─ dnsmasq: answers only allowlisted names, adds their IPs to an ipset
  │    ├─ iptables: INPUT/OUTPUT DROP; redirect tcp/80,443 → proxy on loopback
  │    └─ enclave-gateway-proxy: SNI/Host re-check, optional TLS MITM,
  │         secret release, JSONL audit log
  └─ tool container (non-root, shares the gateway's network namespace)
       └─ claude / codex / … in YOLO mode (--no-yolo to disable)
```

**Filesystem model:**

- Project (or linked worktree) is a writable bind mount by default. Agent edits are real host edits. `--project-mount readonly` makes it read-only.
- Additional directories only via explicit `--add-dir` / `--add-readonly-dir` or config.
- **Sensitive-mount refusal**, read-only mounts included: home and its parents, any hidden home entry (`~/.ssh`, `~/.aws`, `~/.config`, …), XDG / `~/Library` app data, Enclave's own config/state/cache roots, and the directory holding `SSH_AUTH_SOCK`. Per-path exceptions exist, but the hard-protected set (home, Enclave roots, SSH socket) can only be unlocked by the CLI-only `--allow-sensitive-mounts`, never by a config file. Rationale for protecting its own roots: the agent could otherwise widen its next run's policy or add a host command.
- **Git config protection**: existing `.git/config`, `config.worktree`, included files, and global config are mounted read-only even when the project is writable. Parent directories are anchored so a rename can't bypass it. Side effects: `git remote add`, `git push -u` upstream tracking, and `git branch -m` can fail or partly succeed.
- **Does not cover `.git/hooks`**. The docs warn that an agent can install hooks or create `.git/commondir` to redirect Git to an unprotected config, which then runs on the host. This is the review-time hazard from §F.1.2. The only fix offered is `--project-mount readonly`, which also makes every Git write fail.
- Project config lives host-side at `~/.config/enclave/projects/<hash>/config.json`, outside the worktree, so neither the repo nor a session can rewrite its own isolation policy. The project's `.env` and `devcontainer.json` are still read and are documented as executable input.
- Refuses to run as root unless `--allow-root` (CLI or env only).

**Network model:**

- **Restricted by default.** Three independent checks, all fail-closed on startup or reload failure:
  1. dnsmasq returns NXDOMAIN for non-allowlisted names.
  2. iptables only reaches IPs that dnsmasq added to the `enclave_allowed` ipset at query time. This avoids the resolve-at-startup IP rot of the Anthropic devcontainer firewall (§E.13.5, §F.1.3).
  3. The proxy re-checks TLS SNI or HTTP `Host` against the allowlist.
- **Layered allowlist**: built-in per-tool `gateway-allowlist.conf` → global `allow_domains` → project `allow_domains` → per-run `--allow-domain` (with `+x` / `-x` list syntax). Features declare their own domains. Deny rules win over allows. User entries are bare DNS names only, no wildcards. A project policy can set `inherit_global_policy: false`.
- **Policy tooling**: `enclave network print|diff|status` previews the effective policy; `network add-domain` and `network apply` push domain changes into running gateways without a restart. Mode changes need a restart.
- **Audit log**: JSONL per project and tool, readable after the session exits (`enclave network log --json|--summary|--verdict deny|--follow`). Three levels:
  - `coarse` (default): one event per TLS connection, plus DNS denials.
  - `requests`: MITM every allowlisted HTTPS host, one event per request. Breaks cert-pinning clients.
  - `full`: also stores headers, bodies, and WebSocket messages. Captures replace released secrets with placeholders but store prompts and file contents as sent.
- **Inbound**: restricted gateways drop unsolicited inbound traffic. Each session gets its own bridge network, so sessions can't reach each other.
- **Host services**: `--bridge-port 9800` forwards a host port into the container (DNAT in the gateway). On Linux Docker Engine the host service must bind to the default bridge gateway IP and needs a per-session firewall rule, because the session subnet changes every run. Docker Desktop doesn't need this. `-p 0:3000` publishes container → host with an engine-assigned port, readable from `enclave ps --json`.
- **Documented limits**:
  - An allowed IP is reachable on any port. Only 80/443 go through the proxy, so SSH on 22 is never logged.
  - Destination policy, not content policy: paths, methods, and bodies are not constrained (same gap as §F.1.1).
  - The gateway is privileged and shares the tool's network namespace, so a gateway bug weakens policy. Byre's run-to-completion helper (§F.3) avoids a long-lived privileged container.
  - Unrestricted mode (`--allow-all-network`) still logs 80/443 but enforces nothing and disables secret release.

**Credentials:**

- **Placeholder release** for env credentials with a declared HTTP release rule. The tool container sees a per-session placeholder. The gateway terminates TLS only for the declared hosts, swaps the placeholder in the declared header only, and returns HTTP 403 for a placeholder in any other header or plaintext HTTP. If a placeholder can't be generated, startup fails instead of falling back to the real value. This is the header-only, host-bound design recommended in §F.1.1.
- Out-of-the-box coverage:

  | Tool | Variable | Release hosts |
  |---|---|---|
  | Claude | `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`) | `api.anthropic.com` |
  | Codex | `CODEX_API_KEY` / `OPENAI_API_KEY` | `api.openai.com`, `*.openai.com` |
  | Codex | `CODEX_ACCESS_TOKEN` (`at-` PATs only) | `auth.openai.com`, `chatgpt.com` |
  | Pi | `ANTHROPIC_OAUTH_TOKEN` | `api.anthropic.com` |

- **Not protected**: file-backed and refreshable logins, such as an interactive Claude OAuth login stored in `.credentials.json`. Those hold real values in the auth store and the container. Secrets without a release rule are real values too.
- **Auth stores** are host directories under `~/.local/state/enclave/`, bind-mounted, shared per tool by default. `--auth-name` selects one of several identities. `--ephemeral` uses no store. For Claude, the shared store points `CLAUDE_SECURESTORAGE_CONFIG_DIR` at one directory so parallel sessions coordinate OAuth refresh. That is the parallel-agent token-rotation problem ATeam also has.
- **Layered secrets files** (`global.env` → `global/<tool>.env` → `projects/<hash>/<tool>.env`) feed declared secrets. They are never copied into the container as files. Host env vars pass only when an extension declares them or `--pass-env` names them.
- **SSH push keys** are discouraged. `enclave ssh-init` mounts one read-only key into every session of every project. Commit signing is disabled in the container. The docs recommend committing in the container and pushing from the host, which matches §F.1.1 Tier C.

**Gateway implementation and interception tradeoffs:**

- The proxy is Enclave's own Go code (`enclave-gateway-proxy`, `internal/gateway/mitm/`), not Envoy, Squid, or mitmproxy. DNS filtering is dnsmasq; enforcement is iptables + ipset.
- **Interception is transparent.** The agent keeps calling the real hostname (`api.anthropic.com`), so clients need no base-URL changes. The container trusts an Enclave CA through `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `GIT_SSL_CAINFO`, and `NODE_EXTRA_CA_CERTS`. This is the main advantage over explicit integration endpoints (§E.13.15, §G.4).
- **TLS is decrypted only where needed**: hosts with a credential release rule, or every allowlisted host under `--network-log=requests|full`. Everything else is SNI-checked and tunneled unread.
- **Credential placement is header-only.** Each `serviceAuth` entry names one `headerName`. APIs that take the key in the query string (Google Maps `?key=`), in the body, or inside a request signature (AWS SigV4) are not covered without extending the gateway. Clients that verify a token's format locally can't use a placeholder; Enclave refuses Codex Agent Identity JWTs at startup for that reason.
- **UDP to allowlisted IPs is not proxied.** The ipset rule accepts any protocol to a resolved allowed IP. HTTP/3 (QUIC) and other non-80/443 traffic reach those hosts without passing the proxy or the log. A placeholder sent that way stays a placeholder, since release needs the proxy.
- Cert-pinning clients and clients with their own trust store fail on intercepted hosts.
- The gateway is a trusted component that sees plaintext for every host it intercepts. In `full` log mode it stores that plaintext on the host.

**Backends:**

| Backend | Status | Notes |
|---|---|---|
| Docker (rootful) | Default | userns-remap recommended in the hardening doc |
| Podman (rootless) | Supported | Gateway keeps `NET_ADMIN` inside its own user namespace |
| Docker (rootless) | Not supported | Gateway needs host netfilter; Enclave won't fall back to unrestricted |
| QEMU | Experimental | x86-64 Alpine microVM, foreground only, no gateway, unrestricted network, no secret release; TCG emulation (slow) on macOS and arm64 |

**Sessions and automation:**

- `enclave` starts a foreground session. `--background`, `attach`, `ps --json`, `stop`, `continue`, `resume`, and `--name` manage concurrent sessions. Concurrent starts for the same tool and project serialize until the container runs.
- Arguments after `--` go to the agent: `enclave --tool codex -- -p "…"` appears in the Windows doc. So one-shot headless runs look possible. **Not verified**: behavior without a TTY, and whether the agent's exit code propagates for a one-shot run. Signals are forwarded and "the session exits with its status".
- Versioned JSON contracts: `ps --json`, `config --json`, `network print --json`, `network log --json`. [HomeShell](https://github.com/eclipse-enclave/homeshell) (web UI and desktop app, preview) drives Enclave only through these. It is a working example of an orchestrator on top, and also the only remote/GUI session story in the Enclave ecosystem.
- Tool images build on first use. Agent CLIs auto-update in the image. `--no-rebuild` / `ENCLAVE_NO_REBUILD=1` pins them.
- Extensions (tools, features, commands) can ship `commands/host/*` scripts that run **on the host** with the user's privileges. That is a supply-chain surface; `add` lists them before install.
- `enclave devcontainer run` derives the base image from `devcontainer.json`.

**Comparison with existing entries:**

- vs **ATeam Docker one-shot** (ISOLATION.md mode ②): same shape — container per run, bind-mounted project, agent in YOLO. Enclave adds what ATeam's Docker modes lack today: egress allowlist, audit log, placeholder secrets, sensitive-mount refusal, and Git config protection. ATeam currently passes the real `CLAUDE_CODE_OAUTH_TOKEN` or API key into the container and has no egress control there.
- vs **Docker Sandboxes** (§E.13.6, §F.5): both have host-side credential release and a network log. Docker Sandboxes isolates with a microVM and a private Docker daemon (safe DinD). Enclave uses a shared-kernel container with no DinD story. Enclave needs no `sbx login`, is MIT, and works on Linux without KVM.
- vs **tsk** (§D.3): both are container + proxy allowlist. tsk copies the repo in; Enclave bind-mounts it. Enclave has placeholder secrets; tsk does not.
- vs **code-on-incus** (§F.3): coi makes `.git/hooks` immutable with `chattr +i`. Enclave protects Git config files but leaves hooks writable.
- vs **Matchlock** (§E.13.9): same secret-release idea. Matchlock is VM-level with an overlay workspace. Enclave is container-level with a bind mount, and has much richer policy and audit tooling.
- vs **Porta** (§E.13.13): Porta is a native process sandbox with no container. It protects `.git/hooks` inside writable mounts, and its placeholders also work in query strings. Its network is open by default and its adoption is near zero.
- vs **OpenShell** (§E.13.14): OpenShell keeps its trusted supervisor outside the workload, which has no network of its own. Enclave's privileged gateway shares the agent's network namespace. OpenShell adds per-binary and L7 rules, and more credential placements, at the cost of a persistent control plane.

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Good | Explicit mounts, sensitive-path refusal, Git config read-only. `.git/hooks` stays writable unless the project is read-only. |
| Network control | Excellent | Default-deny DNS + ipset + SNI proxy, fail closed, layered allowlists, hot apply. Non-80/443 ports to allowed IPs skip the proxy. |
| Secret safety | Good | Header-only, host-bound placeholder release for env tokens. Interactive OAuth file logins are not protected. |
| Audit | Excellent | JSONL decision log with per-request and full-capture modes. The most complete in this survey. |
| Tool compatibility | Good | Full Linux container. `requests`/`full` modes break cert-pinning clients. No DinD. |
| Host services | Fair | `--bridge-port` works. Linux Docker Engine needs a per-session bind address and firewall rule. |
| Platform | Linux, macOS, WSL2 | Rootful Docker or rootless Podman. macOS binaries are unsigned. |
| Unattended use | Promising, unverified | `-- <agent args>`, `--background`, JSON contracts. No documented exit-code contract for one-shot runs. |
| Maturity | Early 1.0 | Released Oct 2026, Eclipse governance, thorough docs. Low star count. |

**For ATeam:** two ways to use it.

1. **As a container backend.** ATeam could shell out to `enclave --tool claude -- -p "<prompt>"` as a new container mode. That brings egress allowlisting, the audit log, and placeholder secrets with no new ATeam code. Costs:
   - rootful Docker or rootless Podman, plus a per-tool image build;
   - Enclave's config tree and auth stores become a second source of truth next to `ateam secret`;
   - `.ateam/Dockerfile` customization would have to map onto `--base-image`, devcontainer mode, or features;
   - the bind-mount model keeps the `.git/hooks` hazard.
2. **As a design reference.** The gateway sidecar is an MIT-licensed Go implementation of what §F.6 items 2 and 4 recommend: DNS-filled ipset, SNI proxy, fail-closed iptables, header-only secret release. Files to read: `gateway-entrypoint.sh`, `internal/gateway/mitm/proxy.go`, `internal/runtime/network_manager.go`, `internal/runtime/auth_manager.go`.

Suggested next step: a small experiment running a one-shot Claude Code prompt through `enclave` with `CLAUDE_CODE_OAUTH_TOKEN` and no TTY. Check exit codes, the placeholder release, and the network log. If integration is awkward, borrow the gateway design for ATeam's Docker mode instead.

#### E.13.13 Porta

**GitHub:** [almide/porta](https://github.com/almide/porta)
**Language:** Almide (policy, CLI) + Rust (enforcement, proxy, Wasmtime)
**Platform:** macOS (Apple Silicon prebuilt; Intel builds from source), Linux x86-64 / arm64 (glibc 2.31+). Also a GitHub Action.
**License:** Apache-2.0. Latest release v0.6.17 (2026-10-03); `main` is at 0.7.0. **3⭐** (2026-11-20). Releases are Sigstore-signed with build provenance.
**Reviewed:** 2026-11-20, commit `3c79e54`, from the README, `docs/enforcement.md`, `docs/threat-model.md`, `docs/benchmarks/competitors.md`. Not run locally.

A native process sandbox in the same family as Anthropic SRT (E.2), nono (F.3), Fence (E.13.2), and Agent Safehouse (E.13.11). It wraps one command and everything it starts, using Seatbelt on macOS and Landlock + seccomp + namespaces on Linux. No container, no daemon. Unusually for this category, it also ships a per-run credential placeholder proxy.

**Usage:**

```bash
cd my-project
porta init claude                      # writes a commented porta.toml sized to the agent
porta up -- -p "Fix the failing test"  # runs the agent under that policy
porta run ./tool -v ./work --allow-net '*:443' --timeout 120 --max-procs 500
porta explain ./tool -v ./work         # prints the resolved policy; runs nothing
porta run ./tool -v ./work --why       # after a run: each denial and the flag it needed
```

- Policy comes from `porta.toml` or flags. The closed-path list is a data preset (`sandbox/native/presets/default.toml`), extendable with `--deny-read`, `--protect`, `--deny-unix`.
- **A rule the kernel can't express refuses the run** (exit 125) instead of silently weakening it.
- `--why` reads the kernel denial log on macOS and traces with `strace` on Linux.

**Filesystem:**

- Writes only to `-v` mounts, `/tmp`, and `/dev`. Nothing is mounted implicitly.
- Reads everything except a preset of credential stores: `~/.ssh`, `~/.aws`, `~/.config/gh`, `~/.config/gcloud`, `~/.docker`, `~/.kube`, `~/.netrc`, Keychains, browser profiles. `--read-policy strict` limits reads to the mounts plus system directories.
- **Protected names inside writable mounts**: the repository's `.git/hooks` and `.git/config`, shell rc files, `.gitconfig`, `.envrc`, `.mcp.json`, `.claude/commands`, `.vscode`, `porta.toml`. They stay unwritable and can't be renamed away. This closes the review-time hazard from §F.1.2 that Enclave leaves open. On Linux it needs unprivileged user namespaces; Ubuntu 23.10+ needs `sudo porta setup` or the `.deb` to load an AppArmor profile. Without namespaces porta still runs and reports what is left open.
- `--snapshot` copies the mounts first (an APFS clone on macOS) and `porta rollback` restores them. Snapshots live outside every mount.
- Credential sockets (SSH agent, gpg-agent, `docker.sock`) are refused unless `--allow-unix` names them.
- The environment starts empty. Only `PATH`, `HOME`, locale, terminal, and vars named with `-e` / `--env-pass` cross.
- Resource caps: `--timeout`, `--max-cpu`, `--max-procs`, `--max-file-size`, `--max-memory-mb` (cgroup v2 on Linux; polling on macOS).

**Network:**

- **Open by default.** Close it with `--no-net`, `--allow-net '<host>:<port>'` (TCP by port; UDP closed), or `--proxy-allow <hosts>` (by hostname).
- `--proxy-allow` starts a loopback CONNECT proxy and denies all direct TCP, UDP, and Unix-socket egress. On Linux, seccomp also refuses `io_uring`, which could open a socket without a syscall porta sees. Clients must honor `HTTPS_PROXY`; those that don't fail closed. Only HTTPS CONNECT on port 443 is supported.
- `HTTPS_PROXY` carries a per-run credential, so other local processes can't use the proxy as a relay.
- An allowed name that resolves to loopback, link-local, cloud metadata, or multicast is refused. Private ranges stay reachable.
- Every decision is synced to a JSONL audit file (`--proxy-audit`) before the connection proceeds.

**Credentials:**

```toml
[[credentials]]
name  = "ANTHROPIC_API_KEY"            # the variable the command sees
from  = { env = "ANTHROPIC_API_KEY" }  # where porta reads the real value
hosts = ["api.anthropic.com/v1"]       # host[:443][/path], *.wildcard allowed
```

- The command sees `porta-cred-ANTHROPIC_API_KEY-<random>`, minted per run. The real value stays in porta.
- For bound hosts only, porta terminates TLS with a per-run CA (in memory, removed at exit) and replaces the placeholder **anywhere in the request line, query, or headers**. A placeholder on an unbound path of a bound host gets HTTP 403. Other hosts are tunneled unread.
- The CA reaches clients via `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, and `NODE_EXTRA_CA_CERTS`. Go on macOS reads the Keychain instead and fails closed.
- Limits: intercepted connections speak HTTP/1.1, one request per connection. Bodies are passed through, not inspected. Values come only from porta's environment for now; file, Keychain, and command sources are planned.
- For Claude Code, `porta init claude` passes `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`) by name, since the macOS Keychain is closed inside the sandbox.

**Escape benchmark (project-maintained):** porta publishes an escape corpus (`scripts/escapes.py`) run under five tools, each with its policy translated to the other tool's nearest setting:

| | porta | srt | Fence | nono | landrun |
|---|---|---|---|---|---|
| macOS: tried / escaped | 22 / 0 | 17 / 5 | 14 / 5 | 13 / 5 | — |
| Linux: tried / escaped | 28 / 0 | 23 / 2 | 21 / 3 | 24 / 5 | 22 / 7 |

Run on 2026-09-24 with porta 0.6.11, srt 0.0.77, nono 0.78.0. The corpus, the runners, and the policy translations are written by porta's authors, and the project says there has been no third-party audit. Treat the numbers as a claim, not an independent result. The corpus itself is useful: it is a ready-made regression suite ATeam could run against its own Seatbelt / bubblewrap profile.

Out of scope for ATeam: a WASM runtime with capability grants, a WASM agent runtime, and a job API (`porta job-serve`).

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Excellent | Write-confined, credential stores closed, `.git/hooks` and agent settings protected inside mounts. |
| Network control | Good | Port rules, or a hostname CONNECT proxy with direct egress denied. Open by default. |
| Secret safety | Good | Per-run placeholders bound to host + path, in headers, query, and request line. HTTP/1.1 only. |
| Audit | Fair | Proxy decision log; kernel-denial explanations per run. |
| Tool compatibility | Good | Same process model as the host. Clients must honor `HTTPS_PROXY` and the CA env vars. |
| Platform | macOS, Linux | Same CLI on both, with documented differences. |
| Unattended use | Good | `porta up -- -p "…"`, documented exit codes, GitHub Action. |
| Maturity | Very early | 3⭐, self-tested, single project. |

**For ATeam:** Porta is the closest match to ATeam's default mode ① (OS sandbox on the host) that also does credential placeholders and `.git/hooks` protection. Its adoption is near zero, so use it as a design reference rather than a dependency. Worth borrowing: protected names inside writable mounts, refuse-if-unexpressible, `--why` denial explanations, and the escape corpus as a regression test.

#### E.13.14 NVIDIA OpenShell

**GitHub:** [ai-openshell/openshell](https://github.com/ai-openshell/openshell) (moved from `NVIDIA/OpenShell` when it joined the CNCF)
**Language:** Rust; SDKs for Python, TypeScript, Go, Rust
**Platform:** Linux amd64 / arm64, macOS Apple Silicon (Docker Desktop, or the MicroVM driver on Apple Hypervisor), WSL2 (experimental), Kubernetes via Helm
**License:** Apache-2.0. Latest stable v0.1.9 (2026-11-17). Weekly stable releases with a published compatibility policy (RFC 0014). ~21.8k⭐ (2026-11-20).
**Reviewed:** 2026-11-20, commit `a1c1e42`, from `docs/about/architecture.mdx`, `docs/about/support-matrix.mdx`, `docs/how-it-works/providers/`, `docs/how-it-works/policies/network-rules.mdx`, `docs/security/best-practices.mdx`. Not run locally.

This replaces the August note in §F.3, which described an alpha with env-var credentials. Both points are out of date.

A runtime for fleets of agents, with a control plane. The core idea: the agent's workload has **no network of its own**. Every TCP open and DNS lookup is intercepted inside the sandbox and handed to a trusted supervisor outside the boundary. The supervisor decides, adds credentials, and opens the real connection.

**Architecture:**

```
openshell CLI / SDK
  └─ gateway (control plane: identity, policy, providers, sandbox records)
       └─ compute driver (Docker | Podman | MicroVM | Kubernetes)
            ├─ supervisor (trusted: own container, host process, or pod)
            │    ├─ policy engine: per-binary destinations + L7 request rules
            │    ├─ credential resolution: placeholder → real value, or signing
            │    └─ DNS resolution, upstream connections, logs
            └─ workload (untrusted: networking off / no NIC / NetworkPolicy)
                 ├─ sandbox runtime: Landlock for files, seccomp user-notify on TCP + DNS
                 └─ agent (one non-root identity, no Linux capabilities)
```

- Supervisor ↔ sandbox is one HTTP/2 connection with TLS and a per-sandbox JWT, over a Unix socket (Docker/Podman), vsock (MicroVM), or TCP (Kubernetes).
- How each runtime blocks direct egress: the Docker/Podman workload container has networking turned off; the MicroVM guest has no network device; Kubernetes uses a NetworkPolicy that only allows the supervisor.
- Fail closed: the agent doesn't start until the supervisor confirms the boundary, and is frozen if the supervisor disconnects.
- The sandbox identifies the calling program from trusted `/proc` data, so network rules can name binaries.
- Unlike Enclave, nothing privileged shares the agent's network namespace.

**Network policy:**

- **Deny by default.** Each rule lists endpoints and the binaries allowed to reach them, by real path. Children of a listed binary inherit the rule. If the file at that path changes, later connections are denied.
- **L7 inspection** per endpoint via `protocol`:

  | `protocol` | Checked per request |
  |---|---|
  | `rest` | HTTP method, path, query parameters |
  | `websocket` | upgrade request and each client text message |
  | `graphql` | operation type, operation name, top-level fields |
  | `mcp` | MCP method and tool name |
  | `json-rpc` | method name |
  | `tcp` | nothing (databases and other non-HTTP protocols) |

  An `access: read-only` preset, explicit `rules`, and `deny_rules` are available. `enforcement: audit` logs violations without blocking.
- **TLS is terminated by default** with a per-sandbox ephemeral CA injected through `NODE_EXTRA_CA_CERTS`, `DENO_CERT`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`. `tls: skip` per endpoint covers mTLS and non-HTTP protocols.
- `openshell policy update` changes network rules on a running sandbox. Connections opened under the old rules keep them until the client reconnects.
- **Policy prover**: formal verification flags changes that add credentialed reach, new HTTP methods, or cloud-metadata access. Any finding blocks auto-approval. It also ships as a standalone `openshell-prover` command for CI. An **advisor** handles agent-proposed rules waiting for human review.

**Credentials (providers):**

- A provider profile declares credentials, where they go in a request, which endpoints may receive them, and which binaries may use them. The provider instance stores the values in the gateway.
- The sandbox gets opaque placeholders. The supervisor resolves one only if policy allows the binary and destination, host/port/path match the binding, and the placeholder sits in its declared location. Otherwise it returns HTTP 403 `credential_endpoint_mismatch`.
- **Supported placements**: headers (Bearer, Basic, custom), query parameters, URL path slots, opted-in request bodies, opted-in WebSocket text, and **proxy-side AWS SigV4 signing**. A JWT-shaped placeholder alias covers clients that parse token claims. This is the widest coverage in this survey.
- **Gateway-managed refresh** (OAuth2, Google service accounts, AWS STS AssumeRole) mints new credentials without putting refresh secrets in the sandbox.
- Attaching a provider is refused if a current rule lets uninspected traffic reach a credentialed endpoint, unless `allow_uninspected_credentials: true` is set.
- The bundled Anthropic profile uses `ANTHROPIC_API_KEY`; the docs say subscription users need a separate Console API key. Whether `CLAUDE_CODE_OAUTH_TOKEN` works through a custom profile was not verified.

**Running an agent:** `openshell sandbox create --from <image> -- claude`. The default image is minimal Ubuntu with no agent installed, so ATeam would ship its own image. Anonymous telemetry is on by default (`OPENSHELL_TELEMETRY_ENABLED=false` disables it).

**Assessment for ATeam:**

| Dimension | Rating | Notes |
|---|---|---|
| Filesystem isolation | Good | Landlock inside a container or VM, non-root, no capabilities. |
| Network control | Excellent | No NIC in the workload; per-binary + L7 rules; live updates; formal checks on changes. |
| Secret safety | Excellent | Placeholders in header/query/path/body, SigV4 signing, gateway-side refresh. |
| Audit | Good | Gateway-mediated logs, audit-mode rules, OpenTelemetry trace relay. |
| Tool compatibility | Good | Default TLS termination breaks pinning clients unless the endpoint sets `tls: skip`. |
| Operational weight | High | Persistent gateway, a supervisor per sandbox, a profile catalog to maintain. |
| Platform | Linux, macOS (Apple Silicon), K8s | Needs Docker/Podman or host virtualization. |
| Maturity | Pre-1.0, moving fast | 0.1.x stable line, weekly releases, CNCF, large community. |

**For ATeam:** OpenShell is the most complete answer to "which external requests can the agent make, and with which credentials". It is also a platform with its own control plane, heavier than ATeam's per-run model and overlapping with ATeam's orchestration. Two realistic uses:

1. **A backend for teams already running OpenShell.** ATeam creates one sandbox per run with `openshell sandbox create … -- claude -p …` and leaves network and credential policy to OpenShell.
2. **A design reference.** Worth borrowing: the supervisor outside the workload (no privileged sidecar in the agent's network namespace), per-binary rules, credential placement metadata, and refusing to attach a credential to an uninspected endpoint.

#### E.13.15 exe.dev (cloud VMs with integration endpoints)

**Site:** [exe.dev](https://exe.dev) — docs: [integrations](https://exe.dev/docs/integrations), [HTTP proxy integration](https://exe.dev/docs/integrations-http-proxy), [egress policy](https://exe.dev/docs/internet-access), [architecture](https://exe.dev/docs/architecture)
**Type:** Commercial cloud. Flat monthly plan for individuals; hourly compute pools for teams; single-tenant and BYOC for larger customers.
**Isolation:** One VM per workload on bare metal. The docs say "We happen to use Cloud Hypervisor, but that's a bit of an implementation detail (and may change!)."
**Reviewed:** 2026-11-20, public docs only.

Cloud VMs are not a local-first fit for ATeam (§E.16). exe.dev is here for its credential model, which is the clearest example of **explicit integration endpoints** instead of transparent TLS interception.

**Integrations:**

- An integration is a named server-side credential plus a target, attached to VMs directly, by tag, or with `auto:all`. Per the docs, the secret is "stored server-side and injected at the network edge when your VM calls the integration hostname."
- The VM calls a dedicated hostname, not the real API:

  ```
  exe.dev ▶ integrations add http-proxy --name myapi --target https://api.example.com --bearer sk-... --attach vm:my-vm
  # inside the VM:
  curl https://myapi.int.exe.xyz/users
  ```

  `--header name:value`, `--bearer`, basic auth, and `--strip-prefix` are supported. The target URL can't include a path.
- Built-in types include HTTP proxy, GitHub, LLM (managed Anthropic, OpenAI, Fireworks), S3 object storage (exe.dev signs each request with SigV4), Slack, Discord, remote MCP servers, VM-to-VM, and identity federation for AWS, GCP, Anthropic, OpenAI, and Tailscale. The catalog covers "well over a hundred services."
- `execonnect` is a process you run inside a private network. It dials out to exe.dev, so VMs can reach endpoints you publish there through an HTTP proxy integration. VMs reach only what you publish.
- Peer (VM-to-VM) integrations deliver a signed attestation of the source VM in `X-Exedev-Source-Vm`.

**Egress:** two modes per VM. `all` is the default. `integrations` blocks the public internet but keeps exe.dev metadata and attached integration endpoints reachable, so the agent can't bypass an integration by calling the real API directly. No per-domain allowlist is documented.

**Not found in the public docs (unverified):** query-parameter credential injection (e.g. Google Maps `?key=`), how a VM is authenticated for non-peer integrations, and internals named in some secondary summaries (`exepipe`, a userspace WireGuard transport, the implementation language).

**Tradeoff vs transparent interception (Enclave, Porta, OpenShell):** no CA inside the VM and no TLS interception, so certificate pinning and custom trust stores don't matter. In exchange, every client must be pointed at the integration hostname, which only works for tools with a configurable base URL. See §G.4.

**For ATeam:** not a backend candidate (cloud, VM per workload, source must be synced). The integration-endpoint pattern is the same as ATeam's planned `ANTHROPIC_BASE_URL` reverse proxy (§F.1.1 Tier A), generalized to any HTTP API.

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
- **Cross-platform agent VM with network allowlist + secret injection:** Matchlock (E.13.9) — same CLI on Linux and macOS, default-deny egress, real credentials never enter the sandbox. Worth tracking as it matures.
- **macOS Seatbelt with no Docker, no VM, no user-account tricks:** Agent Safehouse (E.13.11) — deny-first, agent-agnostic profiles, Homebrew install. Highest-momentum option in this exact slot (1.7K⭐).
- **Container mode with egress allowlist, audit log, and placeholder secrets already built:** Eclipse Enclave (E.13.12) — v1.0 since Oct 2026, MIT, Go. Candidate backend for ATeam's Docker mode, or a reference design for adding the same gateway to it.
- **Native process sandbox with placeholder credentials and `.git/hooks` protection:** Porta (E.13.13) — design reference for ATeam's mode ①, not a dependency (3⭐).
- **Full network and credential policy (per-binary, L7, many credential placements, gateway-side refresh):** NVIDIA OpenShell (E.13.14) — heavyweight platform; reference design or backend for teams already running it.
- **Explicit integration endpoints instead of TLS interception:** exe.dev (E.13.15) — cloud only; the pattern generalizes ATeam's planned `ANTHROPIC_BASE_URL` proxy. See §G for the networking tradeoffs.
- **Layer policy on top of sandbox:** prefer nah (D.10.2) over hand-rolled hooks for command classification; reach for Cupcake (D.10.1) when rules grow complex enough to need Rego.
- **Don't double-sandbox:** when running Codex CLI, lean on its built-in modes (E.17); for Claude Code, use SRT (E.2 / E.17) rather than wrapping Claude Code in yet another Seatbelt layer.

### E.16 External Tracking: awesome-agent-sandboxes

**Source:** [dloss/awesome-agent-sandboxes](https://github.com/dloss/awesome-agent-sandboxes) — a curated list of sandboxing solutions for AI agents, taxonomised by isolation level (microVM, container, process, filesystem, WASM, embedded interpreter).

**Why we track it:** the sandbox space is moving fast and new tools land monthly. This list is the most comprehensive index seen so far. **Check periodically** (quarterly is probably enough) for entries that have crossed the threshold of being worth a full E-section evaluation. Triage filter for ATeam relevance:

1. **macOS-native, no Docker required** (matches user constraint for local agent runs).
2. **Default-deny network with allowlist** OR **secret injection at the boundary** (gaps in most current entries).
3. **Agent-agnostic** (not Claude-specific).
4. **>500⭐ or >3 months of weekly commits** (momentum filter — too many one-off experiments otherwise).

Categories from the list as of May 2026 already covered above: Anthropic Sandbox Runtime (E.2), several Docker-based wrappers (E.8/E.13.6), Apple Container (E.13.8), VibeBox (E.13.7), Matchlock (E.13.9), Vibe (E.13.10).

Categories worth periodic re-check:

- **MicroVMs (cloud):** E2B, Sprites, Vercel Sandbox, Modal, Daytona, Runloop, exe.dev — all cloud, not a local-first fit for ATeam, but watch for self-hosted modes. exe.dev's integration-endpoint credential model is reviewed in E.13.15.
- **Local microVMs (non-macOS):** ERA (krunvm), Gondolin (QEMU), Arrakis, Netclode (Kata) — Linux-side equivalents to Apple Container; relevant if ATeam ever runs on Linux laptops.
- **Process / filesystem / WASM:** AgentFS (CoW filesystem isolation), Leash (Cedar policy enforcement), Wassette / Capsule / Eryx (WASM sandboxes) — orthogonal isolation models. Likely not primary backends but interesting building blocks.
- **Container wrappers with worktree support:** packnplay — closest to ATeam's worktree model, worth a look if/when ATeam revisits the worktree story.

### E.17 Built-In Agent CLI Sandboxes

A dimension that's easy to overlook: **the agent CLIs themselves now ship sandboxing**. When picking an external sandbox layer for ATeam, it matters what the agent already provides — sometimes the right answer is "just turn on the built-in" rather than wrapping the agent in a third-party tool. Snapshot as of May 2026:

| Agent CLI | Built-in mechanism | Default state | macOS | Linux | Windows |
|---|---|---|---|---|---|
| **OpenAI Codex CLI** | Seatbelt + Landlock + seccomp + restricted-token | **On by default**; tiered modes: `read-only`, `workspace-write`, `danger-full-access` | Seatbelt | Landlock + seccomp | Restricted tokens |
| **Anthropic Claude Code** | `@anthropic-ai/sandbox-runtime` (E.2) — Seatbelt or bubblewrap, plus a network proxy | Off by default in CLI; cloud sessions run in full microVMs | Seatbelt | bubblewrap | n/a |
| **Google Gemini CLI** | Seatbelt profile *or* Docker/Podman container per `.gemini/sandbox.Dockerfile` | Opt-in | Seatbelt | Docker/Podman | Docker/Podman |
| **GitHub Copilot Coding Agent** | Ephemeral cloud sandbox VM, per-task | On (cloud-only) | n/a | n/a | n/a |
| **Cursor / Devin / Cognition** | Hosted sandbox or dedicated cloud VM | On (cloud) | n/a | n/a | n/a |
| **Replit Agent** | Replit VM/container | On (cloud) | n/a | n/a | n/a |

**Implications for ATeam:**

- **Codex CLI is the only major agent that ships sandboxing on by default**, with a published mode hierarchy. If ATeam supports Codex as an alternate runtime, the host-side sandbox layer can be relaxed — Codex already does most of the work.
- **Claude Code's built-in sandbox is the same SRT covered in E.2**, just bundled. Running `srt` externally vs letting Claude Code invoke it internally is largely a packaging choice. The external invocation is more flexible (works for non-Claude commands inside the same session) but the internal one is one less moving part.
- **Gemini CLI's `.gemini/sandbox.Dockerfile`** is interesting: a checked-in, project-specific sandbox image contract. This is a pattern ATeam could borrow for the Docker container adapter — let projects ship a `.ateam/sandbox.Dockerfile` that overrides the default.
- **Cloud agents (Copilot, Devin, Cursor agent mode, Replit)** have sandboxing baked in but at a coarse grain — the whole agent run is in a VM. For ATeam's local-first model these are not direct competitors but they set a baseline expectation that "agents come pre-sandboxed."

**Recommendation:** ATeam's sandbox strategy should layer with, not replace, what the agent already does. For Claude Code: rely on SRT (turn on the built-in or invoke it externally). For Codex: trust the built-in modes and add ATeam-level controls (network policy, secret injection) on top. For Gemini: let projects ship their own `sandbox.Dockerfile` per the upstream convention.

### E.18 External Tracking: wincent's coding agent sandboxes guide

**Source:** [gist.github.com/wincent/2752d8d97727577050c043e4ff9e386e](https://gist.github.com/wincent/2752d8d97727577050c043e4ff9e386e) — Greg Hurrell's curated landscape of sandbox technologies for AI coding agents, organised by isolation method (OS primitives → application kernels → microVMs → containers → WASM → SaaS → self-hosted SDKs → CLI sandboxes → built-in agent sandboxes → policy/audit). Created May 3, 2026; treat as a point-in-time snapshot.

**Why we track it:** broader and better-categorised than the awesome-agent-sandboxes list (E.16). Particularly valuable for two dimensions our doc historically under-covered: **what the agents ship natively** (now folded into E.17 above) and **the policy/approval/audit layer** (now covered in D.10). Re-check on the same quarterly cadence as E.16, applying the same triage filter.

Entries from this gist already absorbed into our doc:

- **Section 1 (OS primitives):** Seatbelt and bubblewrap — covered via E.2/E.3/E.4/E.13.11.
- **Section 7 (self-hosted):** AIO Sandbox/agent-infra was previously sketched; microsandbox and Cleanroom are new — see "worth a deep dive" below.
- **Section 8a/b (CLI sandboxes):** Anthropic SRT (E.2), neko-kai (E.3), kohkimakimoto (E.4), cco (E.5), VibeBox (E.13.7), Matchlock (E.13.9), Vibe (E.13.10), Agent Safehouse (E.13.11). Conductor and Sculptor are commercial/UI-shaped and out of scope for this research.
- **Section 9 (built-in):** captured in E.17.
- **Section 10 (policy/audit):** captured in D.10.

Worth a deep dive on the next refresh:

- **microsandbox** (libkrun microVMs, sub-200 ms boot, MCP server) — closest competitor to Matchlock for local microVMs with agent-shaped UX.
- **Cleanroom** (Buildkite) — microVM with deny-by-default egress; the network model is the relevant comparable to Matchlock's.
- **container-use** (Dagger) — already noted briefly in E.8; if Dagger continues investing it may become the standard "branch + container per agent" reference and warrant a full entry.
- **shai** (colony-2/shai) and **treebeard** (divmain/treebeard) — Linux-side ergonomic sandboxes worth checking if ATeam ever ships on Linux laptops.

---

## F. August 2026 refresh — the "Docker Sandboxes" HN thread

Source: [HN 49239751](https://news.ycombinator.com/item?id=49239751) — 554 points, 322 comments, discussing Docker's [Docker Sandboxes](https://www.docker.com/products/docker-sandboxes/) product. The thread is mostly a *mechanism* debate ("is a container a security boundary") plus ~40 people posting their own sandbox tool. All projects below were verified to exist via GitHub/web on 2026-08-10; several names in circulation were garbled and are corrected inline. Metadata (stars, dates) is a 2026-08-10 snapshot and will drift.

**The single most load-bearing finding for ATeam:** Docker's own PM (`srini-docker`) lists *"running sandboxes in the background for long-horizon work"* as **roadmap, not shipped**, and the one user who asks for a long-running unattended agent is told the target workload is something else. Across 322 comments there is **no working unattended/overnight/CI-triggered agent story** — searches for `overnight`, `autonomous`, `unsupervised`, `queue`, `CI`, `cron` return nothing. This is precisely ATeam's category, and it is unclaimed.

### F.1 Cross-cutting: the two patterns worth adopting

Two design ideas dominate the thread and are more valuable to ATeam than any single tool.

#### F.1.1 Credential injection at the network boundary

The consensus pattern for "the agent needs an API key but must never hold one":

1. The real secret stays **host-side**, ideally in an OS keychain.
2. The sandbox gets a **high-entropy placeholder** in the env var the agent expects.
3. A **host-side proxy** intercepts egress and, *only* for requests whose destination hostname matches that secret's declared allowlist, substitutes the placeholder with the real value **in a specific HTTP header, never in the body**.

The header-only, host-bound scoping is the load-bearing detail. Docker's DevRel (`mikesir87`) was explicit: *"there is no 'search and replace' going on. It's only setting the header."* The placeholder exists only because most CLI tools treat "env var is set" as "auth is configured" — it is a compatibility shim, and it works because tools don't validate token shape.

**Who actually implements it** (verified): Docker Sandboxes (host proxy, sentinel `proxy-managed`/`sbx-cs-<rand>`, per-service domain lists); **nono** (`nolabs-ai/nono`, local reverse proxy, phantom `NONO_PROXY_TOKEN`, `--allow-endpoint` L7 method+path policy — works on macOS *and* Linux with no container); **Eclipse Enclave** (gateway holds the value, releases `ENCLAVE_SECRET_*` only on HTTPS to declared hosts, denies plaintext carrying placeholders); **runclaude** (stub `.credentials.json` inside + host MITM rewriting `x-api-key`, plus SigV4 re-signing for Bedrock); **yoloAI** (on-by-default broker for Claude/Gemini/Codex); **Gondolin/tuor** (destination-scoped placeholder splicing); **microsandbox/isolade** (placeholder swapped only for allowlisted hosts). Everything else — smolvm, Locki, agent-vm, amika, flar, code-on-incus, claude-contained, Byre, Litterbox, agent-images, agent-box — puts the **real key in the sandbox** (env var or bind-mounted credential file); protection there is scoping, not withholding.

**Weaknesses raised, and what they do/don't cover:**
- *Placeholder round-trip* (`llimllib`: "agent writes secret.txt with the placeholder, the proxy replaces it, then the agent reads secret.txt back"). Genuinely **refuted** for header-only designs, but valid against any proxy that does body-wide string replacement or substitutes on *any* allowlisted host rather than the host bound to that specific secret. This is the thing to get right if ATeam builds its own.
- *Exfiltration through an allowlisted host* (`kellpossible2`, `llimllib`, `messh`) — **unanswered by the pattern**. If `api.github.com` is allowlisted and carries a write-scoped token, the agent can create a public gist/branch/issue containing the repo, or reflect the secret back via a subsequent request. Credential injection prevents *key theft*; it does nothing about *data exfiltration through a legitimately-allowlisted, user-content endpoint*. Same is true of `api.anthropic.com` itself.
- *Maintenance burden* (`fg137`) — the allowlist fights build tooling and grows without end (see F.1.3).

**Mitigations worth encoding:** bind each secret to a host *and* validate the destination the proxy actually dials (resolve DNS in the proxy, reject CONNECT to bare IPs, verify the cert chains to the allowlisted name — closes domain fronting and DNS rebinding); header-only substitution, exact header, exact host; treat "allowlisted + accepts user content + carries a write credential" as forbidden (for ATeam, `github.com` should carry a **read-only** token or none); per-run high-entropy placeholders so you can grep the worktree/diff/logs for leakage before applying; block RFC1918 + `169.254.169.254` by default.

**Minimal self-hosted implementation for a Go CLI — and it's smaller than it looks:**

*Tier A — the agent's own LLM key needs no TLS interception at all.* Claude Code reads `ANTHROPIC_BASE_URL` at startup and routes every request (including sub-loops) there, authenticating with `ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_API_KEY`; Codex has the equivalent `model_provider` base-url override. So: run a plain `net/http/httputil.ReverseProxy` on the host, set `ANTHROPIC_BASE_URL=http://host.internal:PORT` and `ANTHROPIC_AUTH_TOKEN=<per-run nonce>` in the sandbox; the proxy validates the nonce, strips it, sets the real `x-api-key`, forwards to `api.anthropic.com` over real TLS. ~80 lines, no CA, no MITM, real key never enters the sandbox. Caveats: env var read once at startup (no hot-rotation); if the user auths Claude via subscription OAuth rather than an API key, `ANTHROPIC_BASE_URL` is the wrong lever (fall back to Tier B or passthrough); some agents change telemetry/model-availability behavior when pointed off-endpoint — worth an integration test.

*Tier B — everything else (git forge, registries, MCP): a CONNECT proxy.* Default to **no interception** — a CONNECT-only proxy that allowlists by SNI/hostname and tunnels, giving egress control + an audit log with zero TLS complexity (`HTTPS_PROXY`/`NO_PROXY` are honored by Go, npm, pip, cargo, curl, git). Only reach for MITM where you must inject a credential; then a per-install CA in `.ateamorg/` plus the long tail of runtime-specific trust env vars (`NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `GIT_SSL_CAINFO`, `REQUESTS_CA_BUNDLE`) is the real cost. Go libraries: `elazarl/goproxy` (simplest), `google/martian` (cleanest modifiers), `AdguardTeam/gomitmproxy` (production-tested). Do **not** shell out to Python mitmproxy — it breaks single-binary distribution. **The most actionable off-the-shelf artifact in the whole survey is runclaude's `runproxy`** — a standalone Apache-2.0 credential-injecting MITM with a documented Docker two-network recipe, usable from ATeam's *existing* container modes without adopting anyone's jail.

*Tier C — git push credentials: don't have any.* For ATeam, the cleanest control is that the sandbox never holds a push credential: the agent commits locally in the sandbox, the host fetches, and the *user* pushes. This eliminates the worst allowlist combination outright and costs nothing, because the user reviews commits on the host anyway.

#### F.1.2 Copy / bind-mount / sync / overlay for the workspace — and why bind-mount is wrong for ATeam

| Model | Who | Escape surface | Fits "produce reviewable commits"? |
|---|---|---|---|
| Bind mount / passthrough | Docker Sandboxes (virtio-fs, same absolute path), Gondolin VFS, smolvm `--volume`, most container harnesses | **Highest** — agent writes land on host disk | Yes, but see the `.git` hazard |
| Copy (CoW) + diff/apply | **yoloAI**, tsk | Lowest — nothing reaches host until explicitly applied | Yes, with a review gate |
| Bidirectional sync | Docker Sandboxes (its passthrough *is* instant bidirectional) | Same as mount | Same as mount |
| Overlay snapshot, discard on exit | Matchlock | Low | Only with an explicit export step |
| Lazy FUSE clone | Cloudflare artifact-fs | Low (clones from remote, not host worktree) | Yes — git works in the mount |

The bind-mount hazard is sharper for ATeam than it first appears, because ATeam's whole value proposition is *the user reviews the agent's commits on the host afterward*. With a live mount, an agent (confused, or prompt-injected by the codebase it's auditing) can write files the host will execute **during review**: `.git/hooks/*` (`post-checkout`, `pre-commit`), `.git/config` (`core.pager`, `alias.*`, `diff.*.textconv`, `filter.*.smudge` are all host command execution the next time the user runs `git log`/`git diff`/`git checkout`), `package.json` `scripts.preinstall`, `Makefile`, `.vscode/tasks.json`, `.envrc`. i.e. **the review step itself becomes the exploit trigger.** `kstenerud` (yoloAI) flagged exactly this in-thread as the deliberate reason yoloAI copies instead of mounting; it is a host-compromise path **no amount of kernel isolation closes**.

**What fits ATeam: copy in, git-fetch out.** (1) Cheap copy via APFS `clonefile` on macOS / reflink (`FICLONE`) on Linux, plain copy on ext4 — and **honor `.gitignore`, treating ignored files (`.env`, `*.pem`) as a secrets hazard not to copy in**, per yoloAI. (2) Git runs sandbox-side so hooks/filters behave and the agent makes real commits. (3) Extract by treating the sandbox worktree as a git remote: `git fetch <sandbox-path> <branch>:ateam/<run-id>` on the host. **`git fetch` does not execute the source repo's hooks or honor its `.git/config` aliases**, and hooks are untracked so they cannot travel in a commit — so extraction structurally cannot trigger the bombs. Tracked-file bombs (`package.json` scripts, `Makefile`) still travel but are visible in the diff the user reviews, i.e. the normal code-review threat model rather than a hidden one. This aligns with ATeam's recent `code: refuse dirty working tree before launching supervisor` change — a clean tree means "reproduce the current ref" is faithful.

**Cloudflare artifact-fs** ([`cloudflare/artifact-fs`](https://github.com/cloudflare/artifact-fs), Apache-2.0, Go, name/URL verified): `git clone --filter=blob:none` then a FUSE daemon serves the tree while blobs hydrate on read; read-write via a SQLite CoW overlay; git works in the mount. **Conditionally interesting, not near-term**: it clones from a *remote* (reproduces a committed ref, not the local working tree), needs macFUSE (a kernel extension with a user-approval prompt — hostile for a CLI install), and `git status` runs ~7s on a 5800-entry repo, which taxes an agent that polls status constantly. APFS clonefile / reflink solves "copies are expensive" for local repos with zero new dependencies; revisit artifact-fs only for multi-GB monorepos ATeam clones fresh.

#### F.1.3 Where the thread's consensus actually lands

- **Containers vs microVM vs VM.** The louder camp (`weinzierl`, `angry_octet`, `pojzon`, `sgc`) holds that shared-kernel containers are not a boundary against a capable adversary and that AI agents must be treated as one; the dissent (`cognitiveinline`, `Supermancho`, `devttyeu`) notes no participant cites a *specific* container-escape CVE, and `dhchun1203` deflates the incident record: *"Neither of the recent ones [OpenAI July, Kimi K3] was actually a container escape … Both went through stuff the sandbox was set up to allow."* Where real setups land is **not** microVM-per-agent but **VM-wrapping-containers by trust tier** (`TacticalCoder`: three VMs by trust level; `dist-epoch`: one Ubuntu VM hosting many Incus LXC containers) — driven by the only quantified argument in the thread: a VM costs ≥512 MB idle RAM vs ~50 MB per LXC, so VM-per-agent is a ~10× tax. VM memory reclaim (balloon drivers, virtio-mem) is left unresolved and is an un-refuted cost of the microVM-per-agent model.
- **Kernel isolation vs application/permission layers** is the most one-sided debate, and the decisive line is Docker's own: `mikesir87` — *"I can configure a harness not to access `secrets.txt`, then have it create a Python file that reads any file and reads `secrets.txt`. 'please' isn't security."* `AlotOfReading` adds that correct agent permissions aren't just unenforceable, they're **unspecifiable**. The one defensible policy-layer position (`Roark66`) enforces at the *infrastructure* level the agent can't reach (read-only / PR-only / admin pods, merge as the gate) — nobody rebuts it because the agent can't touch the enforcement point.
- **Deny-by-default egress vs usability** is the strongest *practical* argument, and it lands directly on ATeam's Seatbelt/bubblewrap default. `fg137`: *"As soon as your Go build fails because you haven't put the local cache dir in the 'allowed directories', you'll understand how painful this is … as soon as you reach for Go, Rust tooling nothing works. So you need to manually maintain this list which is nothing but painful trial and error."* Two separable defects: (a) **resolve-then-pin-IP allowlists rot mid-run** when a CDN returns a different A record — this is exactly how the widely-copied `anthropics/claude-code/.devcontainer/init-firewall.sh` works, and it is the failure mode that silently kills a long unattended run; (b) shipped default lists are short and JS/Python-shaped (nothing for Go/Rust/apt/brew/maven). Neither was answered. The most robust alternative offered is `sparsesignal`'s **"internet yes, private addresses no" plus a containment self-test that scans outward from inside the guest** so the boundary is verifiable — the self-test idea is worth stealing outright. Counter-evidence that OS-sandboxes *can* work day-to-day: `lemontheme` daily-drove nono.sh for eight months in YOLO mode without incident; `globular-toast` has a year-old bubblewrap `claude` script.
- **The enforcement gap** (`runtime_lens`, `dSebastien`): a sandbox limits what the agent can do but doesn't *enforce that the agent runs inside it* — you need a separate control layer, or you're scanning machines for binaries. Relevant to how ATeam guarantees its own isolation is applied. And `killerstorm`'s under-noticed point: the *harness* and the *environment it executes commands in* should ideally be in **different** sandboxes, because a rogue npm/pip package can corrode the harness config.

### F.2 New/verified tools — microVM and VM

**yoloAI** — [`kstenerud/yoloai`](https://github.com/kstenerud/yoloai) · Go / MIT · ~193★ · created 2026-02. The closest match to ATeam's own shape (Go single binary, headless `run --wait` with an exit-code taxonomy, copy-based worktrees, per-run isolation tiers) — which makes it the best source of design ideas and the most direct overlap. It's a *backend multiplexer*: selectable isolation from `container` (runc) through `vm-enhanced` (Kata+Firecracker) across Docker/Podman/containerd/Apple `container`/Tart/Seatbelt. Copies the repo (APFS clonefile/reflink), honors `.gitignore`, excludes poisoned build dirs, returns work via `diff`/`apply`. **Credential brokering on by default** (real key host-side, placeholder + `ANTHROPIC_BASE_URL` in the sandbox). Weaknesses: no clean host-Postgres story; egress rules are tamper-resistant only on the Docker backend (in-sandbox, thus defeatable, on others). Usable as a reference design today; young/single-maintainer for a dependency.

**Gondolin** — [`earendil-works/gondolin`](https://github.com/earendil-works/gondolin) · TypeScript / Apache-2.0 · ~1.9k★. QEMU/krun microVM with the network stack and VFS implemented **in JS on the host**, so policy is programmable code: `RealFSProvider`/`ReadonlyProvider`/`ShadowProvider` (tmpfs CoW), destination-scoped secret placeholders, `allowedHosts` egress. Hard limits: **HTTP/1.x + TLS-intercepted HTTPS only — no HTTP/2, HTTP/3, QUIC, or generic UDP** (breaks gRPC and some package managers), guest is **Alpine-only**. The isolation model is the most interesting here but the integration cost is wrong for a Go CLI (Node control plane). **tuor** ([`codethief/tuor`](https://github.com/codethief/tuor), 3★, JSON config not YAML) is a thin, self-described "very experimental" wrapper over it — not a candidate, but a worked example of the ergonomics people bolt on (`.tuorignore`, ephemeral CoW disks, overlay-with-persistent-upper).

**smolvm** — [`smol-machines/smolvm`](https://github.com/smol-machines/smolvm) · Rust / Apache-2.0 · ~4.7k★, pushed daily · macOS (incl. Intel) / Linux / Windows. The best-engineered general-purpose local microVM (libkrun, <200ms boot, runs OCI images, `pack` freezes a VM to a portable `.smolmachine`). **Network off by default** (good for audit runs), `--allow-host` allowlist — but per the repo's own plan doc the egress policy is **Linux/KVM-validated only; the macOS path is unproven**, which matters for ATeam's macOS base. No credential brokering (real key goes in the VM; `--ssh-agent` forwards a signing oracle instead). Slots in as a *runtime backend*, not an agent harness — ATeam would own worktrees/diffs.

**Locki** — [`JanPokorny/locki`](https://github.com/JanPokorny/locki) · Python / Apache-2.0 · ~57★. A hybrid: one Lima VM hosting many Incus containers (trust one kernel boundary, cheap per-sandbox containers inside — can run systemd/Docker/k8s). Its **git/worktree isolation is the most sophisticated in the survey and worth studying**: per-conversation worktrees visible on the host, the real repo and `.git` **hidden from the agent**, a filtered SSH command bridge forcing namespaced branches and redirecting hooks, shared package caches. But the isolation posture is the opposite of unattended needs: **unrestricted egress + real credentials in the sandbox**, explicitly acknowledged ("treat API keys as potentially exposed and disposable"). Local-network access makes host Postgres trivial.

**agent-vm** — [`sylvinus/agent-vm`](https://github.com/sylvinus/agent-vm) · Shell / MIT · ~92★. A few hundred lines of shell over Lima Debian VMs, one persistent VM per project. The simplest thing that works, and two features map onto ATeam almost one-to-one: an explicit **`--readonly` project mount aimed at audit tasks** (only agent-vm has this), and CI-detection that auto-skips the interactive wizard on a non-TTY. Limits: `--offline` is binary (no domain allowlist), real keys in the VM, persistent-per-folder rather than disposable. Good minimal reference for "Lima VM as an isolation backend."

**Tart** — [`openai/tart`](https://github.com/openai/tart) (Cirrus Labs was acqui-hired by OpenAI 2026-04; `cirruslabs/tart` redirects) · Swift · Apple Silicon only, runs macOS/Linux guests. **Not an agent tool** — a general Apple-Silicon VM runtime with OCI-registry image storage. Relevant only as a *backend* for macOS-native codebases (Xcode/iOS/Swift test targets), where a Linux microVM can't build the app and Tart is the only real option. No agent machinery (no egress allowlist, no secret broker, no worktrees). The maintainer transition is a genuine risk for a new dependency.

### F.3 New/verified tools — container and OS-level

**nono** — [`nolabs-ai/nono`](https://github.com/nolabs-ai/nono) · Rust / Apache-2.0 · ~3.6k★, by the Sigstore team. **The strongest credential story of any tool surveyed and the only one that makes "real keys never enter the sandbox" work on macOS *and* Linux with no container.** Kernel primitives on the process itself (Landlock on Linux, Seatbelt on macOS), deny-by-default filesystem (`--allow .`), local reverse proxy injecting the real key with a phantom `NONO_PROXY_TOKEN` and `--allow-endpoint` L7 method+path policy, plus a separate tool-sandbox layer where delegated `git`/`gh` run in their own child sandboxes. It is effectively **a strict superset of ATeam's current default mode** — same Seatbelt/Landlock primitives, but with policy ATeam controls plus key injection ATeam lacks. Its own docs are honest that it's not containment for hostile RCE ("run nono inside a container/microVM for that") — which fits ATeam's layered model. Watch-outs: pre-1.0 API churn; **network is allow-by-default** unless a profile or `--block-net`/`--allow-domain` is active (the marketing implies otherwise); and the unattended auto-deny-vs-prompt behavior of the supervisor's policy engine needs empirical verification before trusting it in a loop.

**Eclipse Enclave** — [`eclipse-enclave/enclave`](https://github.com/eclipse-enclave/enclave) · Go / MIT · Eclipse project. **Superseded by the v1.0 review in §E.13.12 (2026-11-20).** Essentially **"ATeam's Docker mode with the network and secret layers already built"**: rootful Docker (or rootless Podman) + a privileged gateway sidecar (dnsmasq + iptables/ipset + transparent HTTP/TLS proxy) enforcing a layered global/project/per-run domain allowlist; **gateway-side placeholder-release secrets** (real value never enters the tool container, plaintext-HTTP requests carrying placeholders denied); and **`--bridge-port` DNAT for host-service access**. YOLO on by default (`--no-yolo` to disable), `--background`, `--json` state.

*Corrections from the 2026-11-20 review:* (1) project config is **not** tighten-only. It is stored host-side, outside the worktree, so the repository can't rewrite its own policy, but it may set nearly every option, including adding allowed domains. (2) Standalone binaries now ship for Linux and macOS on amd64 and arm64; the macOS builds are unsigned. (3) Secret release covers env tokens with a declared release rule only (e.g. `CLAUDE_CODE_OAUTH_TOKEN`), not file-backed OAuth logins. (4) `--bridge-port` on Linux Docker Engine needs the host service bound to the bridge gateway IP plus a per-session firewall rule.

**runclaude** — [`hanwen/runclaude`](https://github.com/hanwen/runclaude) · Go / Apache-2.0 · Linux only (macOS is a design doc). As a whole sandbox it's redundant with ATeam's bubblewrap path and severs host loopback (fresh netns kills host-Postgres integration tests). **But its `runproxy` component is the single highest-value, lowest-disruption artifact in this survey** — a standalone Apache-2.0 credential-injecting MITM (stub `.credentials.json` inside, real token only in the host proxy which rewrites `Authorization`/`x-api-key`; SigV4 re-sign for Bedrock; git credential-helpers stripped), already documented for use from Docker via a two-network recipe. It would give ATeam's *existing* container modes the "real keys never enter the sandbox" property with no jail adoption. Unusually good test quality for a 3-month project.

**code-on-incus (`coi`)** — [`mensfeld/code-on-incus`](https://github.com/mensfeld/code-on-incus) · Go / MIT · ~638★, the most operationally mature community tool (1000+ commits, JSON Schema, macOS path via Colima/Lima/OrbStack). Incus system containers. Heavier than and largely redundant with ATeam's Docker modes as a *dependency* (adds Incus + an nftables sudoers rule + a ~3GB image), but **four ideas are directly portable**: (1) a **DNS-equality allowlist** — resolve names host-side, install those IPs in nftables, write the same IPs into the container's `/etc/hosts`, and **block DNS egress entirely** so the hosts file is the only name→address path (TTL-aware, wildcards *rejected* not mishandled, survives the launcher exiting, fails closed without sudo); (2) **`chattr +i` protection of `.git/hooks` and `.claude/settings*.json`** during a session (defeats an `unshare -m`+umount bypass — same threat as F.1.2); (3) a **`coi trust` gate** on repo-supplied config; (4) a **socket-forwarded credential broker**. Note its "active defense" monitoring is heuristic process-name/packet pattern-matching (off by default, trivially evadable — useful telemetry, not a boundary), and its HIGH-severity auto-*pause* is a **hang, not a failure, in an unattended pipeline**.

**flar** — [`swelljoe/flar`](https://github.com/swelljoe/flar) · Go / Apache-2.0 · ~48★ · Linux only. Conceptually the closest match to ATeam on Linux (bubblewrap, only the project dir RW, rest of `$HOME` hidden) and substantially redundant with what ATeam already drives. Three ideas worth lifting: isolated-netns + a **host-side validating proxy that rejects loopback/RFC1918/CGNAT/link-local and dials the exact validated IP with no re-resolution** (closes DNS-rebind/redirect-to-metadata); the **`-allow-port 5432` loopback forwarder** for integration tests; and the rule that **repo-local `.flar.json` can only tighten isolation**, ignored with a warning if it tries to widen.

**Byre** — [`pjlsergeant/byre`](https://github.com/pjlsergeant/byre) · Go / MIT · macOS + Linux first-class, already v1.6.0 (178 test files, 57 ADRs, macOS CI). Container-first, redundant with ATeam's one-shot Docker mode plus a config cascade and a TUI ATeam doesn't need, and its fixed per-agent command makes it awkward to shell out to for prompt-composing work. **But its firewall skill is the best-engineered egress design in the survey and is directly portable**: deny-by-default iptables applied **from *outside* the box by a run-to-completion helper container that joins the netns with `NET_ADMIN`** — so the agent's container gets no caps and structurally cannot alter the rules — with a **fail-closed launch gate**, a **deny-probe self-verification**, and an allowlist **derived from the enabled skills' declared egress** rather than hand-written. Strictly better than granting the agent's container `NET_ADMIN`. Its `AGENT-CREDENTIAL-MECHANICS.md` (vendor-by-vendor writeup of how each agent CLI stores creds) and its project-config-lives-host-side-so-the-agent-can't-rewrite-its-own-confinement rule (ADR 0003) are also worth reading. Documents its own holes (DNS/53 passes through; IP-snapshot allowlist staleness).

**claude-contained** — [`gregwebs/claude-contained`](https://github.com/gregwebs/claude-contained) · Go · macOS + Linux · **no LICENSE file (no grant of rights → cannot be vendored)**, 2★, three-week-old fork. Architecturally the closest sibling to ATeam and the most useful to study. Built on **Anthropic's own `@anthropic-ai/sandbox-runtime` (srt)** layered inside an Apple-Container/Docker boundary, with the srt policy **root-owned read-only at `/run`, merged from built-in + user + project + CLI, fail-closed if generation fails** (dual HTTP *and SOCKS5* proxies so non-proxy-aware programs are still caught). Two liftable pieces: that layered-policy design, and **`-H PORT` for host services** (with an explicit warning that Apple Containers can't reach localhost-only services). Better still: **ATeam could adopt `@anthropic-ai/sandbox-runtime` directly** inside its Docker images without taking this launcher.

**coder/boundary** ("Agent Firewall") — [`coder/boundary`](https://github.com/coder/boundary) · Go / MIT · Linux only. The standalone, Coder-independent piece. **Network-only, no filesystem/credential handling** — but the cleanest single-purpose egress allowlist here: `boundary --allow "domain=github.com path=/api/issues/*" --allow "method=GET,HEAD domain=github.com" -- <cmd>`, deny-by-default, L7 method/domain/path rules, config file mergeable with flags, structured audit log. A drop-in candidate for ATeam's **Linux** network-allowlist tier that wraps the agent process inside an existing container. Its `landjail` backend is the only Landlock-based *network* enforcement surveyed that needs no privileges. Hard limit: Linux only, so it can't serve ATeam's macOS path.

**NVIDIA OpenShell** — [`NVIDIA/OpenShell`](https://github.com/NVIDIA/OpenShell) (now [`ai-openshell/openshell`](https://github.com/ai-openshell/openshell)) · Rust / Apache-2.0. **Superseded by the full review in §E.13.14 (2026-11-20).** The August snapshot: ~8k★, self-declared alpha, L7 method/path egress policy that hot-reloads on a running sandbox, a swappable compute driver (Docker/Podman/MicroVM/K8s), and a persistent gateway control plane aimed at platform/k8s teams.

*Corrections from the 2026-11-20 review:* (1) It is no longer "do not use in production". It joined the CNCF, has a stable 0.1.x line with weekly releases and a compatibility policy, and ~21.8k★. (2) Credentials are **no longer plain env vars**. Providers hand the sandbox placeholders; the supervisor resolves them only for the bound binary, host, port, and path, in headers, query parameters, path slots, opted-in bodies, or via proxy-side AWS SigV4 signing.

**agent-images / agent-box** — [`nothingnesses/agent-images`](https://github.com/nothingnesses/agent-images) (Nix OCI image factory, **no isolation of its own** — the runtime confines) and the *separate* [`0xferrous/agent-box`](https://github.com/0xferrous/agent-box) (podman/docker wrapper; ships `-it` hardcoded so piped/CI use fails as-is). Two ideas worth stealing from agent-box regardless: **worktree mode** where the agent sees only committed/tracked files (untracked + gitignored content structurally invisible), and **Portal** — a host daemon brokering selected host operations (`gh`, git) over a Unix socket with per-method allow/ask/deny policy, so the credential never enters the container.

**Not applicable to ATeam** (verified but out of scope): **sandbubble** (Python-only payload — sandboxes generated snippets, can't exec `claude`); **Litterbox** (Linux+Wayland desktop tool, can't bind an arbitrary project path, no network filtering — but its purpose-generated per-sandbox SSH keys served through a host-side agent are a good pattern if ATeam ever needs agents to push); **vibebin** (dormant ~5mo self-hosted VPS platform, self-contradictory license, no headless CLI); **Agent Circus** (its advertised firewall is effectively dead code as shipped — the strict iptables branch is gated on an `AGENT_TYPE` the default compose never sets); **xbin.dev** (a self-hosted app-hosting platform, not an agent sandbox — but a decent reference for a default-deny capability-grant model over seccomp+Landlock).

### F.4 The sandbox directory worth tracking programmatically

**pleasedonotescape.com** (data at [`pjlsergeant/ai-sandboxes`](https://github.com/pjlsergeant/ai-sandboxes), single `ai-sandboxes.yaml`, served raw, changelogged, ~26 entries) grew directly out of this thread and is **the best of the three tracked directories** because it's machine-readable with an unusually good closed-vocabulary taxonomy: `product_role`, `execution_locus`, `isolation_boundary` (user/os_sandbox/container/microvm/vm/layered), `state_model` (incl. `copy_apply`), `network_policy`, **`credential_mediation`** (ambient/mounted/forwarded/host_side_injection/os_keyring/managed_integration), `threat_tier` (t1_accidents/t2_exfiltration/t3_hostile_escape), plus per-claim `basis`/source and `ev: editorial` provenance marks. **Worth polling the raw YAML monthly and diffing the changelog header** — it's the only one that answers structured questions ("which local, free_oss, microvm, host_side_injection + copy_apply tools exist?"), which is exactly the query ATeam keeps re-asking. Compares favorably to [`dloss/awesome-agent-sandboxes`](https://github.com/dloss/awesome-agent-sandboxes) (prose, ~30 entries, no credential/FS annotations, and notably missing Docker Sandboxes + yoloai) and [wincent's gist](https://gist.github.com/wincent/2752d8d97727577050c043e4ff9e386e) (broader but shallower; best for the OS-sandbox tier). ATeam maps cleanly onto the schema (`role: orchestrator`/`policy_wrapper`, `execution_locus: local`, `isolation_boundary: layered`, `state_model: configurable`) — **worth submitting as an entry.**

### F.5 Docker Sandboxes — updates to E.13.6

The E.13.6 snapshot is now partly stale:
- **Linux is no longer "legacy containers."** Supported on Ubuntu 24.04/25.10/26.04 and Rocky 8 (x86_64), arm64 since v0.33 using **KVM microVMs** — real parity, not containers. Distributed via [`docker/sbx-releases`](https://github.com/docker/sbx-releases), confirmed in-thread by `srini-docker`. **Unverified:** what `sbx` does on Linux without `/dev/kvm` (KVM needs bare metal, doesn't nest) — degrade-to-container or refuse is undocumented, which matters for ATeam's CI story on virtualized runners.
- **The VMM is custom, not Firecracker** (`srini-docker`: "to make it more effective across platforms" — no macOS/Windows Firecracker support). Whether it's libkrun-based and whether it'll be open-sourced were **asked repeatedly and never answered**.
- **Filesystem is bind-mount-equivalent** (virtio-fs passthrough at the identical absolute path, `sbx run claude ./ ../other:ro`), *not* a copy/sync model — so per F.1.2 it does **not** solve ATeam's `.git`-hooks-execute-during-review problem. Read-only mount enforcement (v0.37+) can't apply to a workspace the agent must edit.
- **Headless works:** `sbx run <agent> -- -p "prompt"` runs with no TUI (exactly ATeam's `claude -p` shape); `--detached` + experimental SSH-in (v0.37+) give the long-lived pattern. But **background/long-horizon is roadmap, not shipped** (F.0), and the **mandatory `sbx login`** is a hard blocker for headless/CI use and the loudest complaint in the thread (~14 users, never addressed).
- **Cadence is fast** (v0.33 → v0.38.0 in ~a quarter): Kit spec v2, `sbx mcp`, `--deny-network`, CVE-2026-17106 fix in `sbx cp`, JSONL audit logging of egress/mounts (forwardable to SIEM — but **audit-log forwarding + org network policy require a paid Docker AI Governance subscription**). The `sbx` CLI itself is free including commercial.
- **Strongest per-agent isolation of the Docker family and the only one with safe DinD + host-side credential injection**, but the microVM+daemon-per-sandbox overhead is real if running many agents in parallel — the thread's ~10× RAM tax vs LXC (F.1.3) applies.

### F.6 Consolidated ATeam takeaways

1. **The unattended/long-horizon niche is unclaimed** — confirmed by Docker's own PM and by the total absence of overnight/CI stories in 322 comments. ATeam is aimed at exactly the gap everyone else treats as roadmap.
2. **Adopt credential injection for the LLM key first — it's ~80 lines and needs no MITM** (Tier A, F.1.1): an `ANTHROPIC_BASE_URL` reverse proxy holding the real key. This closes the "real key inside the sandbox" hazard that ATeam's Docker modes currently have. For git/registries, prefer a CONNECT proxy (no interception) and reach for `runproxy` (runclaude) or `elazarl/goproxy` only where injection is genuinely needed.
3. **Prefer copy-in + git-fetch-out over bind mounts for the disposable/unattended profile** (F.1.2) — not for kernel-isolation reasons but because a live-mounted worktree lets an agent plant `.git/hooks`/`.git/config`/`Makefile` bombs that fire *on the host during review*. `git fetch` from the sandbox worktree can't trigger them and hooks can't travel in commits. Pairs with the existing clean-tree requirement.
4. **Egress allowlists are the #1 named breakage and fail silently and late** (F.1.3) — resolve-then-pin-IP lists rot mid-run (the copied `claude-code` devcontainer firewall has this), and allowed-directory lists break Go/Rust build caches. If ATeam tightens egress, prefer code-on-incus's DNS-equality model (resolve host-side → nftables + `/etc/hosts` + block DNS egress, wildcards rejected) or `sparsesignal`'s "private-addresses-no + outward containment self-test" over an IP-pinned domain list, and always **fail closed**.
5. **Two enforcement patterns to borrow into the Docker mode:** Byre's deny-by-default egress applied **from outside the box** by a netns-joining helper (agent container needs no `NET_ADMIN`, can't alter rules, fail-closed launch gate), and code-on-incus's `chattr +i` protection of `.git/hooks` + `.claude/settings*.json`. Both defend the same "agent rewrites its own next launch / poisons the host review" threat.
6. **On the default OS-sandbox path, nono is the one tool that's a strict superset of what ATeam already does** (Seatbelt/Landlock + policy ATeam controls + proxy-side key injection, macOS *and* Linux, no container) — worth a close look as either a backend or a design template, with the allow-by-default-network caveat noted.
7. **Track pleasedonotescape.com's YAML monthly; submit ATeam as an entry.**
8. **`config.toml` schema references worth mirroring:** amika's `.amika/config.toml` (per-repo lifecycle/services/sandbox/credentials tables) and the rule that a repository can't widen its own isolation. flar and Byre make repo-supplied config tighten-only. Enclave reaches the same goal differently: project config lives host-side, outside the worktree, where the agent can't edit it (see E.13.12).

---

## G. Networking: egress enforcement and secret injection

A running collection of network-layer approaches, their tradeoffs, and their known restrictions. Add to it as tools are reviewed. Related sections: §E.1 and §E.10 (network tiers), §F.1.1 (credential-injection consensus from the HN thread, and ATeam's Tier A/B/C plan), §F.1.3 (allowlist breakage). Facts below were checked against each project's own docs on 2026-11-20 unless marked otherwise.

### G.1 Three independently enforced layers

A useful way to split the problem. The layers don't need to come from the same project, and each one should be enforced outside the agent's writable filesystem.

1. **Execution sandbox** — what the agent can touch locally: files, processes, sockets, and which addresses it can reach at all. Examples: Anthropic SRT, Porta, nono, Fence, containers, VMs.
2. **Network gateway** — which external requests it can make, and with which credentials: destinations, HTTP methods and paths, credential release, audit, ideally quotas. Examples: Enclave's gateway, OpenShell's supervisor, Porta's proxy, exe.dev's edge, Docker Sandboxes' proxy.
3. **Privileged operation gateway** — actual operations, not possession of a credential: git push, deploy, production SSH, the Docker API. Examples: agent-box's Portal (§F.3), nono's delegated `git`/`gh` sandboxes, an SSH bastion, a restricted Docker API proxy. ATeam's "agent commits, user pushes" (§F.1.1 Tier C) is the simplest form.

| Tool | 1. Execution sandbox | 2. Network gateway | 3. Privileged operations |
|---|---|---|---|
| Anthropic SRT | Seatbelt / bubblewrap | Domain-allowlist proxy, no credential handling | — |
| Porta | Seatbelt / Landlock + seccomp | CONNECT proxy, placeholders for bound hosts | — |
| nono | Seatbelt / Landlock | Reverse proxy, L7 endpoint rules, key injection | Delegated `git`/`gh` child sandboxes |
| Eclipse Enclave | Container | Gateway sidecar, header placeholders, audit log | SSH key discouraged; push from host |
| NVIDIA OpenShell | Container / VM + Landlock | Supervisor: per-binary + L7 rules, providers | — |
| exe.dev | VM | Edge integrations, integrations-only egress | Integrations scope what each credential can reach |
| Docker Sandboxes | microVM | Host proxy, header injection | Private Docker daemon per sandbox |
| ATeam today | Seatbelt / bubblewrap (mode ①) or Docker | Domain allowlist in mode ① only; none in Docker modes; real tokens in env | User pushes from host |

### G.2 Four reference designs compared

| | Eclipse Enclave | exe.dev | Porta | NVIDIA OpenShell |
|---|---|---|---|---|
| Isolation | Docker/Podman container (QEMU experimental) | VM per workload (Cloud Hypervisor) | Native process sandbox | Container, MicroVM, or K8s pod, plus Landlock + seccomp |
| macOS | Docker Desktop or Podman | Remote VMs only | Native | Docker Desktop or MicroVM (Apple Hypervisor) |
| Where enforcement runs | Privileged sidecar sharing the agent's netns | Network edge, outside the VM | Kernel rules + loopback proxy in the host process tree | Supervisor outside the workload; workload has no network |
| Default egress | Restricted allowlist | Open (`all`) | Open | Deny |
| Domain allowlist | Yes | No; integrations-only mode instead | Yes (`--proxy-allow`) | Yes, per binary |
| L7 rules (method/path) | No | Per integration target only | Path binding for credentials only | Yes: REST, GraphQL, MCP, JSON-RPC, WebSocket |
| HTTPS interception | TLS MITM for credential hosts, or all hosts in `requests`/`full` log modes | None: explicit endpoints | TLS MITM for credential-bound hosts only | TLS termination by default; `tls: skip` per endpoint |
| Credential injection | Placeholder in one declared header | Edge adds header, bearer, or basic auth; signs S3 | Placeholder in request line, query, or headers | Placeholder in header, query, path, or body; SigV4 signing; gateway refresh |
| Agent sees the real secret | No, for env tokens with a release rule | No, for integrations | No, for `--credential` values | No, for provider credentials |
| Audit | JSONL decisions; per-request and full capture | Not documented | JSONL proxy decisions | Gateway logs, audit-mode rules, OTel relay |
| Implementation | Go | Closed service | Almide + Rust | Rust |
| License / maturity | MIT, v1.0 (Oct 2026), 94⭐ | Commercial | Apache-2.0, v0.6, 3⭐ | Apache-2.0, v0.1.9, ~21.8k⭐, CNCF |

**Corrections to a secondary summary we received (2026-11):**

- *"Porta has no comparable credential injection and no HTTPS interception."* Wrong on credentials: `--credential` / `[[credentials]]` mint per-run placeholders, and porta terminates TLS for bound hosts with a per-run CA. Right that interception isn't transparent: clients reach the proxy through `HTTPS_PROXY`, and direct egress is denied.
- *"OpenShell uses a credential-aware proxy rather than TLS interception."* It terminates TLS by default with a per-sandbox CA; credential resolution requires HTTP inspection.
- *"Enclave on macOS via Docker or Lima."* The docs name Docker Desktop and Podman; Lima isn't mentioned.
- *"exe.dev: exepipe, userspace WireGuard, written in Go."* Not found in exe.dev's public docs; unverified.

### G.3 Egress enforcement mechanisms

Where the "no" is actually enforced, and what a bypass would need.

| Mechanism | Examples | Bypass needs | Notes |
|---|---|---|---|
| Proxy env vars only (`HTTPS_PROXY`) | Many "set a proxy" wrappers | Any client that ignores the env var | Not a boundary on its own. |
| Proxy env vars + kernel denies direct egress | Anthropic SRT, Porta `--proxy-allow` | A sandbox or kernel bug | Non-compliant clients fail closed rather than leak. |
| Transparent redirect inside a netns | Enclave (iptables REDIRECT + dnsmasq/ipset), Docker Sandboxes | Traffic on ports or protocols that aren't redirected | Enclave proxies only TCP 80/443; other ports and UDP to allowed IPs pass unproxied and unlogged. |
| IP allowlist resolved at startup | Anthropic devcontainer `init-firewall.sh`, Byre | — | Rots mid-run when CDN IPs change (§F.1.3). |
| DNS-filled ipset | Enclave | — | IPs are added at query time, so no startup rot; stale IPs stay allowed. |
| Syscall mediation to an external supervisor | OpenShell (seccomp user-notify on connect and DNS) | Breaking the sandbox runtime | Enables per-binary identity. The workload has no network of its own. |
| No network device; explicit endpoints only | exe.dev `integrations` egress, OpenShell MicroVM (vsock) | — | Every API needs a rule or integration. |

### G.4 Secret injection approaches

1. **Real secret in the sandbox** (env var or mounted file). ATeam's Docker modes today and most container wrappers. Protection comes only from scoping and rotation.
2. **Explicit endpoint / base URL.** The agent calls a gateway hostname; the gateway adds the credential and forwards over real TLS. Examples: exe.dev integrations, ATeam's planned `ANTHROPIC_BASE_URL` proxy (§F.1.1 Tier A), nono's local reverse proxy. No CA and no interception, but each client needs a configurable base URL.
3. **Transparent TLS interception with placeholders.** The agent calls the real hostname with a placeholder; the gateway terminates TLS with its own CA and swaps the placeholder. Examples: Enclave, Porta, OpenShell, Docker Sandboxes, Matchlock, runclaude's `runproxy`. No URL changes, but every runtime must trust the CA, and pinning breaks.
4. **Gateway-side signing or minting.** For auth a placeholder can't satisfy: request signatures and short-lived tokens from refresh flows. Examples: OpenShell (SigV4 signing; OAuth2, Google, STS refresh), exe.dev (S3 SigV4), runclaude (Bedrock SigV4 re-sign).
5. **Operation broker.** The agent never holds or uses the credential. It asks a trusted service to perform a named operation (push a branch, open a PR, deploy). Examples: agent-box Portal, nono's delegated tools, an SSH bastion.

**Transparent interception vs explicit endpoint:**

| | Transparent interception (placeholder) | Explicit integration endpoint |
|---|---|---|
| Examples | Enclave, Porta, OpenShell, Docker Sandboxes | exe.dev, `ANTHROPIC_BASE_URL` proxy |
| API hostname in the client | Unchanged | Replaced by a gateway hostname |
| Client changes | Usually none, but the client must trust the CA | Base-URL config per client |
| Custom CA | Required, via per-runtime env vars | Not needed |
| Certificate pinning / own trust store | Fails (closed) | Unaffected |
| Credential placement | Gateway must know where each key goes | Gateway builds the outbound request itself |
| Gateway sees plaintext | Yes | Yes |
| Debugging | Less visible: the client thinks it talks to the real API | Explicit in config |
| Preventing bypass | Needs egress policy that denies direct access | Needs egress policy (exe.dev `integrations` mode) |

Neither approach stops misuse of an authorized credential. An agent can still make expensive or destructive calls through the gateway unless it also enforces methods, paths, or quotas (§G.6).

### G.5 Credential placement support

Verified from each project's docs on 2026-11-20. "—" means not supported or not documented.

| | Header | Query param | URL path | Body | SigV4 signing | Refresh in gateway | Binding scope |
|---|---|---|---|---|---|---|---|
| Eclipse Enclave | One declared header | — | — | — | — | — (file-backed logins unprotected) | Host |
| Porta | Any header | Yes | Yes (request line) | — (passed through) | — | — | Host + path prefix |
| NVIDIA OpenShell | Yes (Bearer, Basic, custom) | Yes | Yes (path slots) | Opt-in | Yes | OAuth2, Google SA, AWS STS | Binary + host + port + path |
| exe.dev | Yes (`--header`, `--bearer`, basic) | Not documented | Via target + `--strip-prefix` | — | S3 only | Identity federation | Per integration, per VM |
| Docker Sandboxes | Yes ("only setting the header", §F.1.1) | Not reviewed | Not reviewed | — | Not reviewed | Not reviewed | Host |
| ATeam Tier A plan (§F.1.1) | Yes (`x-api-key`) | — | — | — | — | — | LLM endpoint only |

### G.6 Known restrictions and failure modes

- **Query-string and body credentials.** Google Maps `?key=` and some webhook APIs put the key outside headers. Header-only gateways (Enclave, Docker Sandboxes) can't serve them; Porta and OpenShell can. Query strings also tend to leak into logs; Enclave and OpenShell strip them from logged paths.
- **Signed requests.** AWS SigV4 and similar schemes sign the request with the secret, so a placeholder yields a bad signature. They need gateway-side signing (OpenShell, exe.dev for S3, runclaude for Bedrock).
- **Client-side token checks.** Clients that parse or verify a token locally reject random placeholders. Workarounds: a custom placeholder shape (Enclave), a JWT-shaped alias (OpenShell), or leaving that credential unprotected (Enclave refuses Codex Agent Identity JWTs).
- **Refreshable and file-backed logins.** An interactive OAuth login writes access and refresh tokens to a file and rotates them. A placeholder saved to a file breaks the next session. Enclave leaves these logins unprotected (its issue #133); OpenShell moves refresh into the gateway. For Claude Code, the env-only `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token` works with Enclave and Porta placeholders. OpenShell's bundled Anthropic profile documents only `ANTHROPIC_API_KEY`.
- **TLS trust.** Each runtime reads a different variable: `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, `NODE_EXTRA_CA_CERTS`, `DENO_CERT`. Go on macOS reads the Keychain and ignores them. Pinned clients fail. Explicit endpoints avoid all of this.
- **Protocols.** Porta intercepts HTTP/1.1 only, one request per connection. Gondolin is HTTP/1.x only (§F.2). No surveyed gateway intercepts QUIC: Porta and OpenShell block UDP, while Enclave passes UDP to allowed IPs without the proxy. Non-HTTP protocols (SSH, databases) can be allowed by destination but carry no injected credentials.
- **Non-proxied ports.** Enclave proxies only 80/443. Other ports to allowed IPs pass unlogged, so an SSH push never appears in its network log.
- **Authorized misuse.** A brokered credential still allows anything that credential can do on an allowed endpoint: deleting a repo, running up costs, or exfiltrating through a user-content endpoint (§F.1.1). Mitigations: L7 rules (OpenShell `rest`/`mcp`/`graphql`, nono `--allow-endpoint`, coder/boundary, Porta path binding), least-privilege tokens (read-only GitHub), and quotas. No reviewed tool documents per-credential rate or cost limits.
- **The gateway is a trusted component.** It sees the plaintext of intercepted traffic and holds every secret. Enclave runs it privileged inside the agent's netns; OpenShell keeps the supervisor outside the workload; Byre uses a run-to-completion helper (§F.3). Full-capture logs (Enclave `full`) store prompts and file contents.
- **Address checks.** An allowed name can resolve to loopback, link-local, or a metadata address. Porta refuses those; OpenShell's prover flags metadata access; flar rejects RFC1918 too (§F.3). Porta still allows private ranges.
- **Policy location.** The agent must not be able to edit its own policy. Enclave keeps project config host-side; Porta protects `porta.toml` inside mounts; OpenShell keeps policy in the gateway.

### G.7 Design direction for the macOS / Claude Code workflow (notes, 2026-11)

- Keep interactive Claude Code native on macOS. Containers bring terminal, OAuth refresh, and session-lifecycle problems.
- Use an OS-level sandbox to close secret stores and unrestricted network access.
- Put API credentials in an independently trusted gateway rather than env vars inside the sandbox.
- Route production SSH and Docker operations through separately enforced services.
- Keep the sandbox policy outside the agent's writable filesystem.

### G.8 Implications for ATeam

1. **Mode ① (OS sandbox) covers layer 1 and part of layer 2** through SRT's domain allowlist. The gap is credentials: the agent's token sits in its environment. The planned Tier A base-URL proxy (§F.1.1) is the explicit-endpoint approach and needs no CA. Porta and nono show that native placeholders also work on macOS.
2. **Docker modes ②–④ have no layer 2.** Options: shell out to Enclave or OpenShell as the backend, or add an Enclave-style gateway. If ATeam needs placements beyond headers (query keys, SigV4), OpenShell's model is the reference.
3. **For APIs other than the LLM**, prefer explicit endpoints when the client has a configurable base URL, and fall back to interception with placeholders otherwise. Track the required placement per API using the table in §G.5.
4. **Keep layer 3 minimal**: no push credentials in any sandbox; the host pushes.
5. **Borrow Porta's protected names** (`.git/hooks`, `.git/config`, agent settings) inside writable mounts for every mode. It closes the review-time hazard from §F.1.2 without making the whole project read-only.
