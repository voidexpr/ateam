# ATeam POC: Sandboxed Implementation via Docker

## Goal

Add an `ateam implement` command that takes `review.md` (the supervisor's prioritized decisions), runs a Claude Code agent inside a Docker container with `--dangerously-skip-permissions`, and lets it make code changes on an isolated git worktree. No approval prompts. The developer reviews the resulting branch.

---

## Why Docker (Not Anthropic Sandbox Runtime)

The Anthropic Sandbox Runtime (bubblewrap/Seatbelt) is good for interactive use but insufficient for unattended agents that need to install packages, run databases, and spawn web servers. Docker wins here for three reasons:

1. **Full Linux environment inside the container.** The agent can `npm install`, start PostgreSQL, run dev servers, execute test suites — the same workflow a developer would. The Anthropic sandbox restricts what subprocesses can do; Docker gives a full environment where nothing is restricted inside, but nothing leaks outside.

2. **Filesystem isolation is absolute.** Docker only sees what you bind-mount. No policy lists to maintain, no deny-lists to forget. The agent cannot see `~/.ssh`, `~/.aws`, or other projects — they simply don't exist in the container's filesystem. With Anthropic's sandbox, the default is allow-read-everything and you have to deny specific paths.

3. **Network control is familiar.** Docker networking with `--network host` gives access to all localhost ports (databases, dev servers, APIs bound to 127.0.0.1). Or use bridge mode with explicit port mappings. No custom proxies needed.

The tradeoff is startup overhead (a few seconds for `docker run`), which is irrelevant for agents that run for minutes.

---

## Recommended Approach

### Docker Image: `node:22-bookworm-slim`

Use `node:22-bookworm-slim` as the base:

- Claude Code installs via `npm install -g @anthropic-ai/claude-code` — Node is required.
- Bookworm (Debian 12) has `apt` for installing anything else (git, python, go, build-essential, postgres-client, etc.).
- The `-slim` variant keeps the image small (~200MB base) while having everything needed.
- Node 22 is current LTS, matches what most JavaScript projects need.

The Dockerfile should be minimal:

```dockerfile
FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y \
    git curl sudo ca-certificates \
    ripgrep jq tree \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

ARG USERNAME=node
RUN echo "$USERNAME ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

USER $USERNAME
WORKDIR /workspace
```

This is the **generic ateam agent image**. It doesn't include project-specific tools — those either get installed at runtime by the agent (if network allows it) or get added in a project-specific Dockerfile that extends this one. For the POC, the generic image is enough.

### Git Worktree: CLI Creates It Automatically

When `ateam implement` runs:

1. The CLI creates a git worktree from the source repo's current branch:
   ```bash
   cd SOURCE_DIR
   git worktree add /path/to/ateam-workdir/worktrees/implement-TIMESTAMP ateam/implement-TIMESTAMP -b ateam/implement-TIMESTAMP
   ```
2. The worktree path is bind-mounted into the Docker container as `/workspace`.
3. The agent makes changes inside the container (which writes to the worktree on the host).
4. After the agent finishes, the CLI reports which branch has the changes.
5. The developer can `cd SOURCE_DIR && git diff main..ateam/implement-TIMESTAMP` to review.

The worktree is created from whatever branch `HEAD` points to in the source repo (typically `main`). The branch name includes a timestamp to avoid collisions.

**Cleanup:** worktrees accumulate. The CLI should have a `ateam cleanup` command (or a flag) that removes old worktrees. For the POC, just tell the user to run `git worktree remove` manually.

### Network: `--network host`

For the POC, use `--network host`. This gives the container access to all localhost ports — any database, dev server, or service the developer has running. It also gives full internet access (the agent can install packages, read docs, etc.).

This is the simplest option and matches the "tight filesystem, open network" tier from Research §E.9. The filesystem bind-mount IS the security boundary: the container only sees the worktree directory and explicitly mounted paths.

For production, the design doc (§7.9) already describes the iptables-based firewall with domain allowlists. That can be layered on later.

### `~/.claude` and Authentication

Claude Code needs access to its configuration and auth token. Two options:

**Option A: Mount `~/.claude` read-only** (simplest for POC)

```
-v ~/.claude:/home/node/.claude:ro
```

This gives the agent access to the user's Claude Code config and cached auth. Read-only means the agent can't modify the config. On macOS, the actual OAuth token is in the Keychain (not on disk), so this alone may not work — but `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) or `ANTHROPIC_API_KEY` as env vars will.

**Option B: Pass auth via environment variable** (recommended)

```
-e ANTHROPIC_API_KEY=sk-ant-...
```

Or for subscription mode:

```
-e CLAUDE_CODE_OAUTH_TOKEN=$(claude setup-token)
```

The env var takes precedence over any config file. No need to mount `~/.claude` at all — the agent doesn't need the user's personal settings, just auth.

**Recommendation for POC:** use `ANTHROPIC_API_KEY` from the environment. The CLI reads it from the user's environment (or `.env`) and passes it to `docker run -e`. Simple, portable, no macOS Keychain issues.

### Shell Access and ~/.bashrc

For debugging, `ateam shell` (or a `--shell` flag on implement) should drop into the container interactively:

```bash
docker run -it \
  -v WORKTREE:/workspace \
  -v ~/.bashrc:/home/node/.bashrc:ro \
  --network host \
  ateam-agent:latest \
  bash -l
```

Mount `~/.bashrc` read-only so the user gets their familiar shell environment. The `-l` flag on bash sources login profiles. The `--rm` flag should NOT be used here so the container persists for inspection.

For `ateam implement` (non-interactive), the container runs with `--rm` and the entry command is `claude -p`.

### Git Worktree Lifecycle

Git worktrees share the object store with the source repo. Commits made inside the worktree are immediately visible from the source repo — no `git push` between local paths is needed. The branch created by `git worktree add -b BRANCH` already exists in the source repo's branch list.

**On success:**

1. The agent commits all changes inside the container (writes to the worktree on the host via bind-mount).
2. The CLI detects success (checks for `ateam_implement_report.md` with `Overall: PASS`).
3. If a remote is configured, the CLI pushes the branch: `git push origin ateam/implement-TIMESTAMP`.
4. The CLI removes the worktree: `git worktree remove WORKTREE_PATH`. The branch and commits persist in the repo — only the checkout directory is deleted.
5. The developer reviews and merges at their convenience.

**On failure:**

1. The agent may have partial commits and/or uncommitted changes.
2. The CLI detects failure (checks for `ateam_failure_report.md`, or timeout, or no report at all).
3. The worktree is **not** removed. The developer can `cd` into it to inspect the state, read the failure report, run tests, and debug.
4. The branch is **not** pushed to the remote (partial/broken work shouldn't be shared).
5. Cleanup is manual: `git worktree remove PATH && git branch -D BRANCH`.

**Why this matters:** worktrees are cheap (they share the git object store) but leaving dozens of failed worktrees wastes disk space. The `ateam cleanup` command (future) should list stale worktrees and offer to remove them. For the POC, manual cleanup is fine.

---

## CLI: `ateam implement`

```bash
# Run the agent to implement changes from review.md
ateam implement [--extra-prompt TEXT_OR_FILE] [--agent-report-timeout MINUTES]

# Override which review/report to implement from
ateam implement --prompt @custom_instructions.md

# Get a shell into the agent's environment for debugging
ateam implement --shell
```

### What It Does

1. **Read config.toml** — get `source_dir`, timeout, etc.
2. **Read `review.md`** — the supervisor's decisions (or use `--prompt` override).
3. **Create git worktree** from `source_dir` on a new branch `ateam/implement-YYYYMMDD-HHMM`.
4. **Build/pull Docker image** — `docker build` from the Dockerfile in the ateam project dir, or pull a pre-built image.
5. **Run container:**
   ```bash
   docker run --rm \
     --name ateam-implement-TIMESTAMP \
     --network host \
     -v WORKTREE_PATH:/workspace \
     -v REVIEW_MD:/agent-data/review.md:ro \
     -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY \
     ateam-agent:latest \
     claude -p "IMPLEMENT_PROMPT" \
       --dangerously-skip-permissions
   ```
6. **Wait for completion** (with timeout).
7. **Post-process based on outcome:**

   **If the agent succeeded** (ateam_implement_report.md exists with `Overall: PASS`):
   ```bash
   # Push the worktree branch to the source repo (commits are already there via the shared git object store)
   cd SOURCE_DIR
   git push origin ateam/implement-TIMESTAMP

   # Clean up the worktree — the branch persists in the repo
   git worktree remove WORKTREE_PATH
   ```
   Report to the user:
   ```
   Implementation complete (PASS).
   Branch: ateam/implement-20260305-1423
   Review changes:
     cd /path/to/source && git diff main..ateam/implement-20260305-1423
     git log main..ateam/implement-20260305-1423
   Merge when ready:
     git merge ateam/implement-20260305-1423
   ```

   **If the agent failed** (ateam_failure_report.md exists, or timeout):
   - Do NOT remove the worktree. The developer may need to inspect the partial state, debug, or manually continue the work.
   - Print the failure report contents to the console.
   ```
   Implementation failed (FAIL).
   Worktree preserved at: /path/to/myproject/worktrees/implement-20260305-1423
   Failure report: /path/to/myproject/worktrees/implement-20260305-1423/ateam_failure_report.md

   To inspect:
     cd /path/to/myproject/worktrees/implement-20260305-1423
     cat ateam_failure_report.md
   To clean up manually:
     git worktree remove /path/to/myproject/worktrees/implement-20260305-1423
   ```

### The Implement Prompt

The prompt sent to `claude -p` should contain:

```
[Contents of review.md — the supervisor's prioritized action plan]

---

# Implementation Instructions

You are working on the project at /workspace. This is a git worktree — you can make any changes you need.

Implement the Priority Actions from the review above, in order. For each action:
1. Make the code changes
2. Run any relevant tests to verify your changes work
3. Run the full test suite (if one exists) to verify nothing is broken
4. Commit your changes with a message in this format:
   [ateam/AGENT_ROLE] description of what was done
   Examples:
     [ateam/security] Fix SQL injection in user search endpoint
     [ateam/refactor_small] Extract duplicated auth check into middleware
     [ateam/testing_basic] Add missing edge case tests for payment validation

Work through P0 items first, then P1 if time permits. Skip P2 items.
Each finding should be its own commit — do not squash unrelated changes.

## On Success

If all changes are implemented and tests pass:
- Ensure every change is committed (no uncommitted files)
- Write /workspace/ateam_implement_report.md with a terse summary:
  - One line per commit: what was done, which review finding it addresses
  - If anything was skipped or deferred, note why
  - Overall: PASS

## On Failure

If you encounter a build error, test failure, or cannot implement a requested fix:
- Commit whatever partial work you've done (if any) with [ateam/ROLE] prefix
- Do NOT try to force broken code to pass — leave it honest
- Write /workspace/ateam_failure_report.md explaining:
  - Which finding you were implementing when it failed
  - The exact error (build error, test failure, etc.)
  - What you tried to fix it
  - Your assessment: is this a flawed recommendation, a missing dependency, or a genuine bug?
  - Overall: FAIL
```

The `--extra-prompt` flag appends additional instructions (e.g., "Only work on the security findings" or "Skip the refactoring items").

---

## Config Additions

Add to `config.toml`:

```toml
[docker]
image = "ateam-agent:latest"           # or a custom image name
dockerfile = ""                         # path to Dockerfile, if building locally
network = "host"                        # "host" or "bridge"

[implement]
auto_commit = true                      # agent should commit changes
timeout_minutes = 30                    # longer than report timeout
```

---

## Directory Structure Changes

After `ateam implement` runs:

```
myproject/                          # ateam working directory
  config.toml
  prompts/
  reports/
  archive/
  review.md
  Dockerfile                        # ateam agent Dockerfile (created by ateam init)
  worktrees/                        # git worktrees created by implement
    implement-20260305-1423/        # ← agent worked here
      ... (full project checkout with agent's changes)
```

The `worktrees/` directory is where `git worktree add` creates the checkouts. These are real git worktrees backed by the source repo — `git log`, `git diff`, `git branch` all work normally from within them.

---

## Putting It All Together: End-to-End Flow

```bash
# 1. Initialize (already exists from POC)
ateam init myproject --source ~/code/myapp --agents all

# 2. Generate reports (already exists from POC)
cd myproject
ateam report --agents testing_basic,security,refactor_small

# 3. Supervisor review (already exists from POC)
ateam review

# 4. NEW: Implement the supervisor's recommendations in a sandbox
ateam implement

# 5a. If implement succeeded — worktree is auto-cleaned, branch is pushed
cd ~/code/myapp
git diff main..ateam/implement-20260305-1423
git log main..ateam/implement-20260305-1423

# 6a. If happy, merge
git merge ateam/implement-20260305-1423

# 5b. If implement failed — worktree is preserved for inspection
cat myproject/worktrees/implement-20260305-1423/ateam_failure_report.md
cd myproject/worktrees/implement-20260305-1423
# ... debug, inspect partial changes ...

# 6b. Clean up failed worktree manually when done
git worktree remove myproject/worktrees/implement-20260305-1423
git branch -D ateam/implement-20260305-1423
```

---

## Supervisor Awareness of Implementation Outcomes

> **TODO:** Specify in detail. The high-level intent is:

After `ateam implement` completes (success or failure), the supervisor should be made aware of the outcome. The eventual flow is:

1. `ateam implement` produces either `ateam_implement_report.md` (PASS) or `ateam_failure_report.md` (FAIL).
2. A subsequent `ateam review` (or a new command) consumes the original `review.md` **plus** the implementation outcome report.
3. The supervisor produces a **full report**: the original review findings + what was implemented + what failed and why.

This enables the developer to get a single document that tells the complete story: what the agents found, what was prioritized, what was done, and what still needs attention.

For the POC, the CLI simply prints the outcome to the console. The supervisor integration is deferred.

---

## What's NOT in This POC

- **Network restrictions** (iptables firewall, domain allowlists) — use `--network host` for now.
- **Project-specific Dockerfiles** — the generic image works. Users can extend later.
- **Multiple implement rounds** — one agent, one pass. The coordinator loop comes later.
- **Persistent agent state** — no knowledge.md, no memory between runs.
- **Budget control** — no `--max-budget-usd`. The agent runs until done or timeout.
- **Automatic image building** — user runs `docker build` manually if needed. The CLI can add this later.

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Agent installs malicious packages | `--network host` means it can reach the internet. For POC this is acceptable — the agent can only write to the worktree. Production adds firewall. |
| Agent takes too long | Timeout kills the container. Default 30 minutes, configurable. |
| Worktrees accumulate | Document cleanup command. Future: auto-cleanup old worktrees. |
| Docker not installed | `ateam implement` checks for Docker and gives a clear error. |
| Auth token not available | CLI checks `ANTHROPIC_API_KEY` before launching container. Clear error if missing. |
| Agent makes bad changes | Changes are on a branch. Developer reviews before merging. This is the core safety model. |

---

## Future: Graduating to the Full Design

This POC sandboxing approach maps directly to the full ATeam design:

| POC | Full Design |
|---|---|
| Generic Dockerfile | Layered container architecture (§7.2) — base + project runtime |
| `--network host` | iptables firewall with domain allowlist (§7.9) |
| Single `ateam implement` | Coordinator runs `ateam run -a AGENT --mode implement` per agent |
| Manual worktree cleanup | CLI manages worktree lifecycle (§6.2) |
| No budget | `--max-budget-usd` per run (§9.1) |
| Review → implement (2 steps) | Audit → review → implement → verify cycle (§8.3) |
| One worktree per implement | Persistent per-agent worktrees (§7.3) |
