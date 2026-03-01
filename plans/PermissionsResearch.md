# Open Source Projects for Claude Code Permission Management

The landscape of open source tools for managing Claude Code permissions splits into roughly three categories: container isolation, smart hooks/filters, and programmatic permission servers.

---

## 1. claude-code-sandbox (textcortex)

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

---

## 2. viwo-cli (Hal Shin)

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

---

## 3. TSK — AI Agent Task Manager and Sandbox (dtormoen)

**Language:** Rust
**GitHub:** via `awesome-claude-code`

A Rust CLI tool that lets you delegate development tasks to AI agents running in sandboxed Docker environments. Multiple agents work in parallel, returning git branches for human review. Positions itself explicitly as a task delegation tool where human review happens at the branch/PR level rather than at individual tool calls.

**Pros:**

- Rust gives it low overhead and reliability
- Designed from the ground up for unattended multi-agent work
- Human review at the output (branch) level is sane for async workflows

**Cons:**

- Rust means fewer people can contribute/customize
- Still Docker-dependent

**Best for:** Unattended batch agent work; fits naturally with ATeam-style orchestration.

---

## 4. Dippy (Lily Dayton)

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

---

## 5. claude-code-permission-prompt-tool (community-documented)

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

---

## 6. Anthropic's Built-in Sandbox Runtime (open-sourced)

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

---

## 7. Custom Bash Permission Hooks (community patterns)

**Language:** Bash/Python
**GitHub:** Various gists, e.g. `cruftyoldsysadmin` gist

Uses Claude Code's `hooks` system (`PreToolUse`, `PostToolUse` lifecycle events) to intercept tool calls with shell scripts. You write a script that receives the tool name and arguments via stdin, and exits 0 (allow), 1 (deny), or 2 (ask) based on your logic.

**Pros:**

- No external dependencies — pure shell
- Directly integrated into Claude Code's lifecycle
- Easily version-controlled in your repo (`.claude/hooks/`)
- Can block writes to production config files, enforce naming conventions, etc.

**Cons:**

- Shell scripting injection risks if you're not careful with quoting
- Limited expressiveness compared to a full MCP permission server
- No built-in remote/async approval capability

**Best for:** Project-specific rules in interactive sessions; simple allowlist/denylist enforcement.

---

## Summary Table

| Project | Language | Interactive | Unattended | Isolation | Audit Trail | Complexity |
|---|---|---|---|---|---|---|
| claude-code-sandbox | TypeScript | ✓ | ✓✓ | Docker | None | Medium |
| viwo-cli | Shell | — | ✓✓ | Docker+worktree | None | Low |
| TSK | Rust | — | ✓✓ | Docker | None | Medium |
| Dippy | Python | ✓✓ | ✗ | None | None | Low |
| permission-prompt-tool | TypeScript/MCP | ✓ | ✓✓ | None | ✓✓ | High |
| Anthropic Sandbox runtime | C/Rust | ✓ | ✓✓ | OS-level | None | High |
| Claude Code hooks | Bash/Any | ✓ | ✓ | None | Optional | Low |

---

## Recommendations for ATeam Use Cases

Given unattended background agents with budget controls and audit needs, the most relevant combination would be:

**`permission-prompt-tool`** MCP server for fine-grained audit logging and budget enforcement at the tool call level, combined with either **Anthropic's sandbox runtime** or docker-based isolation (**viwo-cli/TSK**) for actual containment. The permission-prompt-tool's `updatedInput` capability is particularly interesting for implementing tool input sanitization before execution.

The **hooks system** is worth layering on top for project-specific blocking rules that you want committed to the repo itself.
