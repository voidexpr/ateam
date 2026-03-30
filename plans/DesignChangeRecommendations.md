# ATeam Design — Change Recommendations

Based on [ResearchAgentControl.md](./ResearchAgentControl.md) and [ResearchSanboxing.md](./ResearchSanboxing.md), and the question of where Anthropic's Sandbox runtime fits.

---

## 1. Anthropic Sandbox for the Coordinator

**Recommendation: Yes, adopt this.**

The coordinator currently runs on the host with `--dangerously-skip-permissions` — fully trusted, no isolation at all. This is the riskiest component: it has direct access to the host filesystem, network, and the `ateam` CLI that manages everything.

Anthropic's Sandbox (bubblewrap on Linux, Seatbelt on macOS) is a natural fit here:

- **Filesystem restriction:** Allow read/write only to the `.ateam/` org directory and its project subdirectories. Block access to `~/.ssh`, `~/.aws`, `/etc`, and everything else the coordinator has no business touching.
- **Network restriction:** Allow only the LLM API endpoint (`api.anthropic.com`) via the unix domain socket proxy. The coordinator doesn't need general internet access — it calls `ateam` CLI commands locally and reads/writes files.
- **No Docker overhead:** The coordinator is a short-lived `claude -p` invocation. Spinning up a Docker container for it would be wasteful. The OS-level sandbox adds negligible overhead.
- **84% fewer permission prompts:** Anthropic's own stat. For the coordinator, this means it can run with the sandbox instead of `--dangerously-skip-permissions`, getting real isolation while still being fully autonomous.

**Concrete change to §8.1:**

Replace the coordinator invocation from:
```bash
claude -p "..." --dangerously-skip-permissions --max-budget-usd 5.00
```
To:
```bash
claude -p "..." --sandbox --max-budget-usd 5.00
```

The sandbox config would allowlist: the org root directory (rw), the `ateam` binary path (rx), `git` (rx), and the API endpoint. The coordinator gets full autonomy within its box without the nuclear option of `--dangerously-skip-permissions`.

**Impact on §7.7, §19 (Risk Mitigation):** The coordinator is no longer an uncontained process. This closes the gap where a misbehaving coordinator could, in theory, reach beyond the org directory.

---

## 2. Anthropic Sandbox for Sub-Agents

**Recommendation: Not as a replacement for Docker. Possibly as a lightweight option for simple projects.**

Your instinct is right — Docker provides stronger isolation than the OS-level sandbox:

| Concern | Docker | Anthropic Sandbox |
|---|---|---|
| Filesystem isolation | Full namespace (agent sees only mounted volumes) | Policy-based (agent sees the full filesystem, blocked by rules) |
| Network isolation | iptables + network namespace | Unix socket proxy (effective but less battle-tested) |
| Process isolation | Full PID namespace | Same PID namespace as host |
| Service dependencies | Compose adapter handles DB, Redis, etc. | No service orchestration |
| Subprocess containment | All subprocesses inherit container constraints | All subprocesses inherit sandbox constraints (this is actually good) |

Docker is the right call for agents that need databases, custom toolchains, or absolute containment. The design already handles this well.

**However**, there's a valid use case for the sandbox as a lighter adapter: projects that don't need Docker at all. Think of a pure TypeScript repo with no database, no services — just `npm test`. Building and maintaining a Docker image for that is friction that discourages adoption. A "sandbox adapter" could:

- Run Claude Code in the Anthropic Sandbox directly on the host
- Restrict filesystem to just the agent's worktree directory
- Restrict network to just the API endpoint + npm registry
- Skip Docker entirely — zero container overhead, instant startup

**Concrete change to §7.4 (Container Adapters):**

Add a `sandbox` adapter alongside `docker`, `compose`, and `script`:

```toml
[docker]
adapter = "sandbox"    # "docker" (default), "compose", "script", "sandbox"
```

The sandbox adapter would use Anthropic's open-sourced runtime to enforce filesystem and network policy without any container. It's the "zero-config" option for simple projects where Docker would be overkill.

**Important caveat in the doc:** The sandbox adapter should be clearly documented as providing weaker isolation than Docker. For projects with `.env` secrets, databases, or services, Docker remains the recommended adapter.

---

## 3. Replace `--dangerously-skip-permissions` with Claude Code Hooks Inside Containers

**Recommendation: Yes, this simplifies the permission story and adds a safety layer.**

Research §D.7 describes Claude Code's hooks system (`PreToolUse`, `PostToolUse`). Currently, the design uses `--dangerously-skip-permissions` as an all-or-nothing bypass because Docker provides the containment. But hooks offer a middle ground that's worth adopting:

**Why:** Even inside a Docker container, there are things agents shouldn't do — `curl` to unknown URLs (if the firewall allows some domains), `rm -rf /workspace` (destroys the worktree), overwrite `/output/stream.jsonl` (corrupts the execution trace). Hooks can enforce project-specific rules cheaply:

```bash
# .claude/hooks/pre-tool-use.sh (committed to the project repo)
#!/bin/bash
TOOL=$(jq -r '.tool_name' < /dev/stdin)
INPUT=$(jq -r '.tool_input' < /dev/stdin)

# Block writes to output directory (reserved for framework)
if [[ "$TOOL" == "Write" && "$INPUT" == */output/* ]]; then
  echo '{"decision": "deny", "reason": "Cannot write to /output/ directly"}'
  exit 1
fi

# Allow everything else
exit 0
```

**Concrete change:** Keep `--dangerously-skip-permissions` (Docker is still the primary containment), but add a hooks layer inside the container via `.claude/hooks/` in the agent's worktree. The hooks are version-controlled in the ATeam repo, so they evolve with the project. This is defense-in-depth, not a replacement for Docker.

**Impact on §5.3 (How Sub-Agents Are Invoked):** Add a step between prompt assembly and container launch: "Copy `.ateam/hooks/` into the agent's worktree as `.claude/hooks/`." The hooks file is project-level policy that travels with the code.

---

## 4. Incorporate the Container Watchdog Bug from Research §C.9

**Recommendation: Strengthen §9.4 with specific failure modes.**

Research §C.9 documents two critical Claude Code bugs that affect ATeam directly:

- **Missing final `result` event** (#1920): Claude Code sometimes fails to emit the final result event after tool executions. The stream-json output just stops. The current watchdog (§9.4) detects this via "no events for N minutes," which works but is slow — 5 minutes of idle wait per stuck agent.
- **Silent mid-task hang** (#28482): Claude Code stops producing output entirely. No error, no exit. Marked as blocking for production automation.

**Concrete change to §9.4:** Add a secondary watchdog heuristic: if the stream-json shows the agent has completed its work (final assistant message with no pending tool calls) but the process hasn't exited within 30 seconds, force-kill. This catches the missing-result-event bug much faster than the 5-minute idle timeout.

```toml
[resources]
watchdog_timeout_minutes = 5           # no output at all → kill
watchdog_completion_timeout_seconds = 30  # output done but process alive → kill
```

---

## 5. Add `--permission-prompt-tool` as a Future Audit Trail Option

**Recommendation: Document as a future enhancement, not initial design.**

Research §D.5 describes the `--permission-prompt-tool` flag — a programmatic permission server via MCP. This is very powerful for audit logging: every tool call, with its exact arguments, can be logged to a database before execution.

For the initial design, this is overkill — stream-json already captures tool calls, and Docker provides the containment. But for organizations that need compliance-grade audit trails (every command the agent ran, every file it touched, with timestamps and approval status), the permission-prompt-tool is the right mechanism.

**Concrete change to §21 (Future Enhancements):** Add:
```
- **Audit trail via `--permission-prompt-tool`** — MCP-based programmatic permission
  handling for compliance-grade logging of every tool call. Enables remote approval
  workflows and per-tool-call budget enforcement. See Research §D.5.
```

**Why not now:** The flag is undocumented and could break between releases. The complexity of running an MCP server per agent is high. Stream-json gives 80% of the audit value at 10% of the complexity.

---

## 6. Simplify the Adapter Interface Using Research Insights

**Recommendation: Reduce the adapter interface to the minimum needed.**

Research §C.9 compares execution approaches. The key insight for ATeam: the one-shot Docker pattern (`claude -p`, wait for exit, read output files) is the simplest and most robust approach. The current adapter interface (§7.4) includes `Exec()` and streaming `Logs()`, which are only needed for interactive sessions (`ateam shell`) and future sophisticated agent loops (§20.8).

**Concrete change:** Split the adapter interface into two tiers:

```go
// Tier 1: Required for all adapters (one-shot execution)
type AgentRunner interface {
    Run(ctx context.Context, opts AgentRunOpts) (RunResult, error)  // blocking
    Kill(ctx context.Context, handle string) error
    Status(ctx context.Context, handle string) (AgentStatus, error)
}

// Tier 2: Optional, for interactive sessions
type InteractiveRunner interface {
    AgentRunner
    Start(ctx context.Context, opts AgentRunOpts) (Handle, error)
    Exec(ctx context.Context, handle string, cmd []string) (ExecResult, error)
    Logs(ctx context.Context, handle string) (io.ReadCloser, error)
}
```

This means the sandbox adapter and the Docker adapter both only need to implement `Run`, `Kill`, and `Status` to be fully functional. Interactive features (`ateam shell`) only work with adapters that implement the extended interface. This simplifies the initial implementation significantly — the sandbox adapter becomes trivial.

---

## 7. Drop Multi-Provider Support from Initial Design

**Recommendation: Remove §5.6 and simplify.**

The current design mentions swapping in Codex, Gemini CLI, or a custom API fallback. Research §C.9 confirms what §A.2 already argues: the tool maintenance burden of a custom agent loop is ongoing cost, and other CLI agents don't have the same quality of tool-use, iteration, and error recovery.

Multi-provider support adds complexity to the prompt builder, the adapter, the output parsing, and the watchdog — all for a feature that's speculative. The design should be opinionated: Claude Code is the runtime. If someone wants Codex, they can use the script adapter escape hatch.

**Concrete change:** Remove §5.6. Remove the `provider` field from config.toml. Keep the script adapter as the generic escape hatch for anything non-standard. This removes an entire dimension of variability from the initial implementation.

---

## 8. Adopt Event Sourcing Framing for Stream-JSON

**Recommendation: Reframe stream-json as the agent's event log, with recovery implications.**

Research §C.9 notes that OpenHands' event-sourced append-only log is "the gold standard for crash recovery" and that ATeam's stream-json already IS such a log. The missing piece is making this explicit in the design.

**Concrete change to §7.7:** Add a paragraph:

> The `stream.jsonl` file is the agent's authoritative event log. It captures every tool call, every file edit, every reasoning step, with timestamps. If an agent crashes mid-run, the stream-json contains the complete record of what was done before the crash. Combined with Claude Code's `--resume` flag, a crashed agent can be restarted from where it left off: `claude --resume <session-id> -p "Continue your task"`. The CLI should persist session IDs in the operations table to enable automatic retry after crashes.

**Impact on §9.4 (Container Watchdog):** When the watchdog kills a stuck container, it should record the session ID from stream-json. The `ateam retry` command can then use `--resume` to continue from the last checkpoint instead of starting over.

---

## 9. Strengthen the "Code Should Look Like It Was Written Better" Framing

**Recommendation: Add a design principle that makes the core goal explicit.**

The current executive summary says "ATeam manages non-interactive agents that work in the background to prevent this." But the goal you stated — "it should look like the code was written better in the first place" — is sharper and more actionable. This framing has design implications:

- **Agents should not leave fingerprints.** No `// Refactored by ATeam` comments. No separate "ateam-fixes" branches that pile up. Changes should be clean commits that look like a careful developer wrote them.
- **Small, frequent changes beat big rewrites.** A 3-line refactoring of a function that was just committed is invisible. A 500-line architectural overhaul is a disruption. The coordinator should strongly prefer the former.
- **Changes merge into the developer's flow.** The ideal is that ATeam's commits interleave with human commits on main, and you can't tell which is which without checking the author.

**Concrete change to §2 (Goals):** Add a design principle:

> **Invisible improvement.** ATeam's changes should look like the code was written better in the first place. No markers, no separate cleanup branches, no disruption to the developer's flow. Small, frequent commits that interleave naturally with human work. If someone reads the git log, they should see a codebase that's simply well-maintained — not one that has a cleanup bot running alongside it.

This principle should cascade into the coordinator's decision logic (§8.3, §14.1): prefer small changes, merge quickly, avoid accumulating large diffs.

---

## Summary of Recommended Changes

| # | Change | Section(s) Affected | Complexity | Priority |
|---|---|---|---|---|
| 1 | Anthropic Sandbox for coordinator | §8.1, §19 | Medium | High |
| 2 | Sandbox as lightweight adapter option | §7.4 | Medium | Medium |
| 3 | Claude Code hooks inside containers | §5.3, §7.7 | Low | Medium |
| 4 | Watchdog bug-specific timeouts | §9.4 | Low | High |
| 5 | Permission-prompt-tool as future item | §21 | Trivial | Low |
| 6 | Simplify adapter interface (two tiers) | §7.4 | Medium | High |
| 7 | Drop multi-provider from initial design | §5.6, config | Low | High |
| 8 | Event sourcing framing for stream-json | §7.7, §9.4 | Low | Medium |
| 9 | "Invisible improvement" design principle | §2, §8.3, §14.1 | Low | High |

Recommendations 1, 4, 7, and 9 are the highest-value changes: they either reduce risk (sandbox for coordinator, watchdog improvements), reduce complexity (drop multi-provider), or sharpen the core mission (invisible improvement principle). The rest are solid improvements that can be incorporated as the design is implemented.
