# Research: Claude Subagents

## F. Claude Code Built-in Subagent & Worktree Support

Claude Code has first-party support for spawning subagents with isolated worktrees. This section evaluates whether ATeam should build on these primitives or maintain its own orchestration.

### F.1 What Claude Code Provides

**The Task Tool (subagent spawning):**

Claude Code's [Task tool](https://code.claude.com/docs/en/sub-agents) allows a main agent to spawn subagents that run as separate Claude Code instances. Each subagent gets its own context window (preventing information overload), its own system prompt (via SKILL.md files or inline instructions), and a constrained tool set. The main agent creates subagents via the Task tool; subagents cannot spawn their own subagents (no recursion).

**Worktree isolation:**

The `isolation: worktree` parameter on subagent definitions causes Claude Code to automatically create a git worktree for the subagent, run it in that directory, and clean up the worktree when the subagent finishes (if no changes remain). This is the same mechanism as `claude --worktree NAME` from the CLI. The [/batch command](https://code.claude.com/docs/en/common-workflows) uses this pattern internally for parallel codebase migrations.

**Permission inheritance:**

When the main agent runs with `--dangerously-skip-permissions` (i.e., `bypassPermissions` mode), all subagents inherit the same unrestricted access. There is no way to selectively restrict a subagent's permissions — it's all or nothing. This is a design feature: `--dangerously-skip-permissions` [disables the entire safety stack](https://code.claude.com/docs/en/permissions) (command blocklist, write access restrictions, permission prompts, MCP trust verification).

**Community validation:**

Multiple community projects have validated this architecture. [ccswarm](https://github.com/nwiizo/ccswarm) (Rust) orchestrates specialized Claude Code agents with worktree isolation and supports multi-provider agent pools (Claude Code, Aider, OpenAI Codex). The [/batch pattern](https://claudefa.st/blog/guide/development/worktree-guide) is widely documented. [metaswarm](https://github.com/dsifry/metaswarm) extends this to Claude Code, Gemini CLI, and Codex CLI with 18 specialized agents.

### F.2 How It Maps to ATeam

The mapping is surprisingly direct:

| ATeam Concept | Claude Code Equivalent |
|---|---|
| Reporting agent | Task tool subagent with read-only tools |
| Review agent | Task tool subagent with read-only tools + Bash for tests |
| Implement agent | Task tool subagent with `isolation: worktree` + full tools |
| Worker pool | Multiple concurrent Task tool calls |
| Worktree lifecycle | Built-in: auto-create on spawn, auto-cleanup if no changes |
| `--dangerously-skip-permissions` in Docker | Same flag, but Docker provides the safety boundary |

### F.3 Why Not Just Use Claude Code Subagents

Despite the good fit, there are two fundamental issues and several practical concerns that prevent ATeam from building directly on Claude Code's subagent system:

**Issue 1: Multi-provider support is a design goal.**

ATeam's architecture (§A.4) explicitly plans for supporting Codex and Gemini alongside Claude Code. The agent runner should be provider-agnostic: the same `ateam implement` command should work whether the underlying agent is `claude -p`, `codex --full-auto`, or `gemini`. Claude Code's Task tool is Claude-only — it spawns Claude Code instances. There is no way to make it spawn a Codex or Gemini agent.

Codex CLI has its own sandbox model (Seatbelt on macOS, Windows Sandbox on Windows, container-based on Linux) and its own permission system. Gemini CLI has sandbox-exec profiles on macOS and Docker/Podman support cross-platform, with its own permission prompting. Building on Claude Code's Task tool would lock ATeam into a single provider.

**Issue 2: Permission-free execution requires a safety boundary.**

ATeam agents must run without interactive permission approval — the whole point is background, autonomous operation. With Claude Code, this means `--dangerously-skip-permissions`. The official documentation is explicit: this mode should only be used in containers or VMs. But Claude Code's built-in worktree isolation is NOT a security boundary — it's a convenience feature for preventing file conflicts between concurrent agents. A subagent with `isolation: worktree` still has full access to the host filesystem, network, and credentials.

ATeam's approach of running agents inside Docker containers provides the actual security boundary, making `--dangerously-skip-permissions` safe. If ATeam relied on Claude Code's worktree isolation alone, there would be no safety boundary for the permission bypass.

**Additional practical concerns:**

- **Orchestration control**: Claude Code's Task tool is fire-and-forget from the main agent's perspective. ATeam needs timeout enforcement, retry logic, outcome detection (success/failure reports), and worktree lifecycle management (push-on-success, preserve-on-failure). These are CLI/Go concerns, not agent concerns.
- **Observability**: ATeam needs to log agent output, track timing, and produce structured reports. The Task tool returns results to the parent agent's context, but ATeam needs them in files and logs.
- **Deterministic workflow**: ATeam's pipeline (report → review → implement) is a fixed sequence with well-defined inputs and outputs. Using Claude Code's Task tool would mean the main agent has to orchestrate this sequence via natural language prompting, which is less reliable than Go code.

### F.4 Where Claude Code Subagents DO Have a Place

Despite the above, there is one scenario where Claude Code's built-in subagent support fits naturally: **when Claude Code IS the agent being orchestrated by ATeam**.

Inside a Docker container, the Claude Code agent invoked by `ateam implement` could itself spawn subagents using the Task tool. For example, an implement agent might:

1. Spawn a subagent to analyze the test suite structure
2. Spawn another subagent to implement the fix
3. Spawn a third to run and verify tests

This is fine because:
- All subagents are inside the Docker container (the safety boundary)
- `--dangerously-skip-permissions` is already set (no permission prompts)
- The worktree isolation within the container prevents conflicts if the agent runs parallel subtasks
- It's entirely within Claude Code's own execution — ATeam doesn't need to know or care

In other words: ATeam orchestrates at the CLI/container level; Claude Code orchestrates within its own execution. These are complementary, not competing, layers.

### F.5 Comparison: ATeam Orchestration vs Claude Code Subagents

| Dimension | ATeam (Go CLI + Docker) | Claude Code Subagents |
|---|---|---|
| Provider support | Any (Claude, Codex, Gemini) | Claude only |
| Security boundary | Docker container | None (worktree is not a sandbox) |
| Permission bypass safety | Docker provides isolation | Requires external sandbox |
| Timeout & retry | Go code, deterministic | Up to the parent agent (LLM) |
| Worktree lifecycle | Explicit (push/cleanup/preserve) | Auto-cleanup only |
| Observability | Structured logs & files | Parent agent context window |
| Complexity | Higher (Go, Docker, devcontainer) | Lower (Task tool + SKILL.md) |
| Pipeline control | Fixed code path | Prompt-driven (less deterministic) |

### F.6 Recommendation

**Don't build on Claude Code's subagent system for orchestration.** The multi-provider goal and the need for a real security boundary are disqualifying. ATeam's Go CLI + Docker/devcontainer approach is the right architecture.

**Do allow agents to use subagents internally.** When Claude Code is the provider, the agent running inside Docker is free to use the Task tool, worktree isolation, and any other Claude Code features. ATeam shouldn't prevent this — it's an implementation detail of the agent's execution.

**Document the relationship clearly**: ATeam is the outer orchestrator (provider-agnostic, container-isolated, deterministic pipeline). Claude Code's subagent system is the inner orchestrator (Claude-specific, within-container, prompt-driven). They coexist at different layers of the stack.

**Revisit if Claude Code becomes provider-agnostic.** If Anthropic ever adds support for non-Claude providers in the Task tool (unlikely but possible), or if the community converges on a shared subagent protocol across providers, this analysis should be revisited. For now, the layered approach is the pragmatic choice.