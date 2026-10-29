# Research: Design

## A. Design Exploration

### A.1 Why Go, No MCP

The framework is a CLI, not an MCP server. The coordinator is Claude Code with a system prompt that describes how to use the `ateam` CLI. When the coordinator needs to run an agent, it calls `ateam run -a testing -p myapp` via its native Bash tool. When it needs status, it calls `ateam status --json`. No MCP layer, no JSON-RPC, no tool registration — just a binary that Claude Code shells out to.

**Why this works better than MCP:**

- Claude Code can already run shell commands. Adding an MCP server between "Claude Code wants to run an agent" and "the CLI runs the agent" is pure indirection.
- The CLI composes naturally: the coordinator can pipe, grep, chain commands with `&&`, use `--json` for structured output — things that are awkward with discrete MCP tool calls.
- Developers and the coordinator use the exact same interface. No divergence between "what the MCP tool does" and "what the CLI does."
- One less process to manage. No `ateam serve` that must be running when the coordinator runs.

**Why Go:**

- **Single binary.** `go build` → one file. No Python runtime, no virtualenv, no `pip install`. Copy and run.
- **Embedded assets.** Default role prompts, knowledge templates, and the SQLite schema are embedded in the binary via `embed.FS`. `ateam install` extracts them to disk.
- **Fast compilation and type checking.** The framework is infrastructure code (Docker, git, SQLite, file I/O) — exactly what Go is built for.
- **Native concurrency.** Goroutines for monitoring multiple agent containers, streaming logs, watching for timeouts.
- **Docker and git ecosystem.** Official Docker client SDK, `go-git` for pure-Go git operations, `os/exec` for worktree management.

**MCP escape hatch:** If MCP is needed later (e.g., to use ATeam from Claude.ai chat or other MCP clients), add an `ateam serve-mcp` command that wraps CLI functions as MCP tools. This is a backwards-compatible addition.

### A.2 Claude Code vs Custom API Agent (Comparison)

| Concern | Custom API loop | Claude Code in Docker |
|---|---|---|
| Coding quality | Must implement file editing, shell, error recovery, iterative debugging from scratch | Already built-in and battle-tested |
| Maintenance burden | We maintain the agent loop; it rots as APIs change | Anthropic maintains it; `claude update` stays current |
| Authentication | API key management | Mount `~/.claude` — uses existing auth, billing, and higher plan limits |
| Tool permissions | Must define and implement each tool | Already has shell, file edit, search, etc. with proper sandboxing |
| Cost | Raw API tokens (potentially more expensive) | Claude Code subscription limits are often more economical |
| Complexity | Hundreds of lines of tool-use loop code | Zero agent code — just prompt construction and file I/O |

**Tradeoffs accepted:** No per-invocation token counts (use `--max-budget-usd` instead), harder to swap LLM providers (addressed via container adapter), less programmatic control (but stream-json gives visibility).

### A.3 Is `claude -p` Sufficient? (Analysis)

`claude -p` (print/pipe mode) runs Claude Code in non-interactive single-prompt mode. It executes the full agent loop — tool use, file editing, shell commands, iterative debugging — until the task completes or a limit is hit.

**What it does NOT do:** No interactive follow-up mid-session. No mid-stream human approval. Less sophisticated context management for very long sessions.

**Assessment: Sufficient for sub-agents.** Tasks are well-scoped (audit → approve → implement). The prompt includes all context. If a task is too complex, the agent writes `blocked.md` and the coordinator re-scopes. For rare cases needing multi-turn interaction, `--resume` flag or PTY automation are available as optimizations.

### A.4 Multi-Provider Support (Reference)

| Provider | Approach |
|---|---|
| **Claude (primary)** | Claude Code via `claude -p` |
| **OpenAI Codex** | Codex CLI via `codex -p` (same file-based I/O pattern) |
| **Gemini** | Gemini CLI agent if available, or custom API fallback |
| **Custom API** | Fallback provider — custom tool-use loop for providers without CLI agents |

Provider is configured per-project in `config.toml`:
```toml
[providers]
default = "claude-code"
# testing = "codex"
```

### A.5 Coordinator Architecture Options (Historical)

Four options were evaluated for the coordinator:

- **Option A: Claude Code + MCP (interactive)** — Human tells coordinator what to do. No scheduling.
- **Option B: Deterministic daemon + LLM escalation** — Rule-based Python. Less capable for nuanced decisions.
- **Option C: Claude Agent SDK** — Python-only, new API. Substantial framework code.
- **Option D: Claude Code + MCP server** — Framework as MCP server. Claude Code calls tools.

**Final decision: Go CLI + Claude Code (no MCP).** Simpler than all options above. The CLI is the tool; Claude Code calls it via Bash. No MCP indirection, no separate server process, no Python dependency.

### A.6 Git-Versioned Configuration

Project and org directories are git repos. This provides:

- **Timeline view**: `git log` shows all agent activity.
- **Rollback**: Revert corrupted knowledge files or bad decisions.
- **Auditability**: The git log is the authoritative record beyond changelog.md.

**Coordinator commit patterns:**

| Event | Commit Message Pattern |
|---|---|
| Report generated | `[testing] audit report 2026-02-26_2300` |
| Implementation complete | `[testing] implementation 2026-02-26_2300` |
| Knowledge updated | `[testing] knowledge update` |
| Coordinator decision | `[coordinator] auto-approved testing report` |
| Human decision | `[coordinator] human approved refactor report` |

**.gitignore for project repos:**
```gitignore
workspace/
repos/
.env
.env.*
**/current_prompt.md
**/stream.jsonl
*.tar
*.log
.DS_Store
```

---
