# ATeam POC: Coding Workspaces

## Goal

Extend the POC beyond reporting and review. Add the ability for the supervisor to dispatch coding tasks to isolated workspaces, collect results, and push a clean stream of commits to the main branch — all without human intervention unless something fails.

This document defines workspaces (git worktree + sandboxed runtime), the commands to manage them, the git branching strategy, and the SQLite schema that tracks state across projects, agents, and workspaces.

---

## Workspace Concept

A workspace is the unit of isolated work in ATeam. It combines a git worktree (for code isolation) with a sandboxed runtime environment (currently Docker, designed to be pluggable). Each workspace is independent: it has its own branch, its own container, and its own lifecycle. Workspaces belong to a specific ateam project.

### What a Workspace Is

- A git worktree branched from a known base commit (typically `main` or an integration branch)
- A Docker container (or future sandbox) bind-mounting that worktree
- A single-purpose environment: one task, one workspace

### What a Coding Agent Can Do Inside a Workspace

- Read and modify code in `/workspace`
- Install packages, run tests, start dev servers
- Create commits on the workspace's branch
- Write `result.md` to report outcome

### What a Coding Agent Cannot Do

- Push to any remote
- Merge branches
- Interact with other workspaces or branches
- Access files outside the bind-mounted worktree

The supervisor owns everything outside the workspace boundary: creating workspaces, reading results, merging branches, pushing to the remote.

### Workspace Lifecycle

```
create  →  idle  →  code  →  [inspect if failed]  →  cleanup
```

1. **Created** by the supervisor (or manually via CLI) with `ateam ws create`
2. **Used** for exactly one task via `ateam code`
3. **Inspected** if the task fails — the workspace is preserved for debugging, and the task can be resumed or finished manually instead of restarting from scratch
4. **Cleaned up** when no longer needed via `ateam ws cleanup`

A new task always gets a new workspace. Workspaces are not reused across tasks.

### Workspace Naming

Workspace names must be valid across three systems simultaneously: filesystem paths, Docker container names, and git branch names. The safe intersection is essentially DNS label rules.

**Rules:**

- Lowercase letters `a-z`, digits `0-9`, and hyphens `-`
- Must start with a letter
- Must end with a letter or digit
- No consecutive hyphens
- Maximum 63 characters

**Regex:** `^[a-z][a-z0-9]*(-[a-z0-9]+)*$`

**Naming scheme:** `{purpose}-{descriptor}-{seq}`

- **purpose**: `code`, `review`, `report`, `adhoc`
- **descriptor**: derived from the task — a slug of the task name, ticket ID, or short description
- **seq**: numeric suffix to handle duplicates

**Examples:**

- `code-fix-auth-timeout-1`
- `code-add-rate-limiter-1`
- `review-sprint-42-1`
- `adhoc-test-docker-config-1`
- `adhoc-fix-auth-timeout-1` (troubleshooting a failed coding workspace)

**Why not agent names?** One agent may have multiple workspaces. A workspace can outlive the agent's involvement (handed to a different agent for debugging, or inspected manually). The workspace identity describes *what it's for*, not *who's working on it*. Agent assignment is tracked as metadata in the database.

**Why not UUIDs?** They work as internal identifiers (and a rowid exists in SQLite for machine use) but are unusable for humans. The workspace name is used everywhere user-facing: CLI output, `ateam ws shell -w NAME`, git branch names, Docker container names. One name, one string to grep.

**Derived identifiers from the workspace name:**

| System | Identifier |
|---|---|
| Workspace directory | `.ateam/projects/{project}/workspaces/{workspace-name}/` |
| Git branch | `ateam/{workspace-name}` |
| Docker container | `ateam-{workspace-name}` |
| Worktree directory | `{Workspace directory}/worktree/{workspace-name}/` |
| Result file | `{Workspace directory}/result.md` |

The `ateam/` prefix on git branches and `ateam-` prefix on containers are added by the tooling, not part of the workspace name itself.

---

## CLI Commands

### `ateam ws create`

Create a new workspace: git worktree + Docker container ready for use.

```bash
# Create a workspace for a coding task, --purpose NAME is optional, it is derived from the name specified
ateam ws create -w 'code-fix-auth-timeout' --purpose code

# Create an ad-hoc workspace for debugging or experimentation
ateam ws create -w 'adhoc-test-env' --purpose adhoc

# Auto-generate a name from a task description
ateam ws create --purpose code --description "Fix authentication timeout in login flow"
```

**What it does:**

1. Validate the workspace name (or generate one from `--description` by slugifying)
2. Determine the base commit — current `main` HEAD (or integration branch, depending on mode)
3. Create the git worktree:
   ```bash
   git worktree add workspaces/{name}/worktree -b ateam/{name}
   ```
4. Record the workspace in `state.sqlite` with status `Idle` and `git_commit_at_creation`
5. Optionally start the Docker container (or defer to `ateam code`)

### `ateam code`

Run a coding task inside an existing workspace.

```bash
# Run with an inline prompt
ateam code "Fix the authentication timeout bug in the login flow" -w code-fix-auth-timeout

# Run from a prompt file
ateam code @tasks/fix-auth.md -w code-fix-auth-timeout

# Combine: file prompt with extra instructions
ateam code @tasks/fix-auth.md --extra-prompt "Focus on the OAuth provider" -w code-fix-auth-timeout
```

**What it does:**

1. Set workspace status to `InUse` in `state.sqlite` (error if not Idle)
2. Rebase the workspace branch onto the current base (main or integration) to incorporate recent changes
3. Start the Docker container with the worktree bind-mounted as `/workspace`:
   ```bash
   docker run --rm \
     --name ateam-{name} \
     --network host \
     -v WORKTREE_PATH:/workspace \
     -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY \
     ateam-agent:latest \
     claude -p "PROMPT" --dangerously-skip-permissions
   ```
4. Wait for completion (with timeout from config)
5. Check for `result.md` in the workspace
6. Update `state.sqlite`: set `last_git_commit`, set status to `Done` or `Error`
7. If the coding agent made commits, update `last_git_commit` in the database

**The coding agent's contract:**

- Make code changes, run tests, commit
- Each logical change gets its own commit with prefix: `[ateam/{purpose}] description`
- Write `/workspace/result.md` with:
  - Status: `PASS` or `FAIL`
  - Summary of what was done (one line per commit)
  - If anything was skipped or deferred, why
  - If failed: the exact error, what was tried, assessment of root cause

### `ateam ws shell`

Drop into an interactive shell inside a workspace's sandbox.

```bash
# Open a shell in a workspace
ateam ws shell -w code-fix-auth-timeout

# Open a shell in an ad-hoc workspace
ateam ws shell -w adhoc-test-env
```

Useful for debugging failed workspaces, inspecting partial work, or manual experimentation. The container is started without `--rm` so it persists for inspection. Mounts `~/.bashrc` read-only so the user gets their familiar shell environment.

```bash
# Under the hood (docker sandbox):
docker run -it \
  --name ateam-{name} \
  -v WORKTREE_PATH:/workspace \
  -v ~/.bashrc:/home/node/.bashrc:ro \
  --network host \
  ateam-agent:latest \
  bash -l
```

### `ateam ws start`

Start a workspace's sandbox without running a coding task. Useful for manual work or preparing the environment.

```bash
ateam ws start -w code-fix-auth-timeout
```

**What it does:**

1. Verify workspace exists and is in `Idle` or `Error` status
2. Start the sandbox (Docker container) with the worktree bind-mounted
3. Update `container_id` in `state.sqlite`
4. The container stays running until explicitly stopped

### `ateam ws stop`

Stop a workspace's sandbox.

```bash
ateam ws stop -w code-fix-auth-timeout
```

**What it does:**

1. Stop the sandbox (Docker container)
2. Clear `container_id` in `state.sqlite`
3. The worktree and git branch are preserved — only the sandbox is stopped

### `ateam ws status`

Show detailed status for a specific workspace: database state, git info, and sandbox status.

```bash
ateam ws status -w code-fix-auth-timeout
```

```
Workspace:    code-fix-auth-timeout
Purpose:      code
Status:       Error
Agent:        security
Branch:       ateam/code-fix-auth-timeout
Base commit:  a1b2c3d (main at creation)
Last commit:  e4f5g6h "Fix OAuth token refresh"
Result:       FAIL

Sandbox:      docker
Container:    ateam-code-fix-auth-timeout
Running:      no

Worktree:     .ateam/projects/myapp/workspaces/code-fix-auth-timeout/worktree/
Result file:  .ateam/projects/myapp/workspaces/code-fix-auth-timeout/result.md
Created:      2026-03-05 14:23
```

### `ateam ws list`

Show all workspaces and their status.

```bash
ateam ws list
```

```
NAME                          PURPOSE   STATUS   BRANCH                              AGENT         CREATED
code-fix-auth-timeout-1       code      Error    ateam/code-fix-auth-timeout-1       security      2026-03-05 14:23
code-add-rate-limiter-1       code      Done     ateam/code-add-rate-limiter-1       performance   2026-03-05 14:25
adhoc-test-docker-config-1    adhoc     Idle     ateam/adhoc-test-docker-config-1    —             2026-03-05 15:01
```

### `ateam ws cleanup`

Safely remove a workspace.

```bash
# Clean up a specific workspace
ateam ws cleanup -w code-fix-auth-timeout-1

# Clean up all workspaces with status Done
ateam ws cleanup --done

# Force cleanup (skip safety checks)
ateam ws cleanup -w code-fix-auth-timeout-1 --force
```

**Safety checks before cleanup:**

- If status is `InUse`: refuse (workspace is actively being used)
- If status is `Error`: warn that the workspace has unresolved failures, require `--force`
- If the branch has unmerged commits not present in main or integration: warn, require `--force`

**What it does:**

1. Stop and remove the Docker container (if running)
2. Remove the git worktree: `git worktree remove workspaces/{name}`
3. Delete the branch: `git branch -D ateam/{name}`
4. Update `state.sqlite`: remove the workspace record (or mark as cleaned — TBD)

### `ateam push`

Push the supervisor's integrated work to the main branch.

```bash
# Push integrated commits to main
ateam push

# Dry run: show what would be pushed
ateam push --dry-run
```

**What it does (POC flow):**

1. Ensure all workspace branches that should be integrated have been merged into the integration branch
2. Rebase the integration branch onto the latest `main` (fetch first)
3. Run validation (tests, build) if configured
4. Fast-forward `main` to the integration branch tip
5. Push `main` to the remote
6. Clean up: delete the integration branch (it will be recreated for the next cycle)

If the rebase onto main has conflicts (main moved and conflicts with integrated work), the push fails and the supervisor must resolve or the user intervenes.

---

## Sandbox Abstraction

A workspace's runtime isolation is provided by a **sandbox** — currently Docker, but designed to be pluggable. The sandbox is responsible for filesystem isolation, process isolation, and network control. The workspace layer doesn't know or care how isolation is achieved; it just calls sandbox operations through a consistent interface.

### Sandbox Types

| Type | Description | Use Case |
|---|---|---|
| `docker` | Full Docker container with bind-mounted worktree | Production coding tasks (POC default) |
| `docker-no-network` | Docker with `--network none` | Security-sensitive tasks, offline-only work |
| `docker-full-network` | Docker with `--network host` | Tasks that need localhost services (databases, dev servers) |
| `none` | Direct execution, no isolation | Testing, development of the CLI itself |

For the POC, only `docker` (with `--network host`) and `none` are implemented. The sandbox type is configured per-project in `config.toml` and can be overridden per-workspace at creation time.

### Sandbox Operations

Every sandbox type must implement these operations:

- **start(workspace)** — create/start the sandbox with the worktree mounted
- **stop(workspace)** — stop the sandbox, preserve the worktree
- **exec(workspace, command)** — run a command inside the sandbox (used by `ateam code`)
- **shell(workspace)** — open an interactive shell in the sandbox (used by `ateam ws shell`)
- **status(workspace)** — report whether the sandbox is running
- **cleanup(workspace)** — destroy the sandbox entirely

### Docker Sandbox Details

#### Base Image: `node:22-bookworm-slim`

- Claude Code installs via `npm install -g @anthropic-ai/claude-code` — Node is required
- Bookworm (Debian 12) has `apt` for installing anything else (git, python, build-essential, etc.)
- The `-slim` variant keeps the image small (~200MB base)
- Node 22 is current LTS

#### Dockerfile

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

This is the **generic ateam agent image**. Project-specific tools get installed at runtime by the agent or added in a project-specific Dockerfile that extends this one.

#### Authentication

The agent needs access to an API key. Two approaches:

**Option A: Environment variable (recommended for POC)**

```bash
-e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY
```

The CLI reads the key from the user's environment (or `.env`) and passes it to `docker run -e`. Simple, portable, no macOS Keychain issues.

**Option B: Mount `~/.claude` read-only**

```bash
-v ~/.claude:/home/node/.claude:ro
```

Gives the agent access to Claude Code config and cached auth. On macOS, the actual OAuth token is in the Keychain (not on disk), so this may not work without also setting `CLAUDE_CODE_OAUTH_TOKEN`.

#### Network: `--network host` (POC default)

Gives the container access to all localhost ports — any database, dev server, or service the developer has running. Also gives full internet access (the agent can install packages, read docs, etc.).

The filesystem bind-mount IS the security boundary: the container only sees the worktree directory and explicitly mounted paths.

For production, an iptables-based firewall with domain allowlists can be layered on later.

### `none` Sandbox (Testing)

Direct execution on the host. No isolation. The `exec` operation just runs the command in a subprocess with the worktree as the working directory. Useful for testing the CLI's workspace/git logic without Docker overhead.

**Warning:** The `none` sandbox provides no isolation. The coding agent can access anything the host user can. Only use for CLI development and testing.

---

## Git Branch Strategies

Three modes for how workspaces relate to the base branch. The supervisor chooses the mode based on the nature of the tasks.

### Mode 1: Parallel (Branch Off Main)

```
main ──────────────────────────────────────────────────►
  ├── ateam/code-fix-auth ──── commits ──┐
  ├── ateam/code-add-limiter ── commits ─┤ merge into integration
  └── ateam/code-update-deps ── commits ─┘
                                          integration ──► rebase onto main ──► push
```

All workspaces branch from the same `main` commit. Maximum parallelism. The supervisor merges them into an integration branch sequentially. Conflicts are possible if workspaces touch overlapping files.

**Best for:** Independent tasks touching different parts of the codebase.

**Tradeoff:** Merge conflicts surface late, at integration time.

### Mode 2: Sequential (Branch Off Integration)

```
main ──┐
       └── integration ──┬── ateam/code-task-1 ── commits ── merge back
                         ├── ateam/code-task-2 ── commits ── merge back
                         └── ateam/code-task-3 ── commits ── merge back
                                                             rebase onto main ──► push
```

Each workspace branches from the current tip of the integration branch, meaning it sees all previously merged work. Zero merge conflicts by construction, but work is serialized: task 2 starts only after task 1 is merged back into integration.

**Best for:** Interdependent tasks, tasks touching overlapping files, or when merge conflicts must be avoided.

**Tradeoff:** No parallelism. Tasks run one at a time.

### Mode 3: Hybrid (Future)

Start N workspaces in parallel off `main`. If conflicts are detected during integration, re-dispatch the conflicting workspace in sequential mode off the updated integration branch. Get parallelism when it's free, fall back to sequential only when needed.

**Not implemented in this POC.**

### POC Default: Sequential with Rebase

For the POC, we use a sequential approach with a twist:

1. Supervisor creates an integration branch from `main`
2. For each coding task (in sequence):
   a. `ateam ws create` creates a workspace branching from `main`
   b. `ateam code` runs the task; the coding agent rebases onto latest `main` before committing
   c. Supervisor merges the workspace branch into integration (should be a fast-forward or clean merge since the agent just rebased)
3. When all tasks are done, `ateam push` rebases integration onto latest `main` and pushes

This gives us the simplicity of sequential execution while keeping each workspace's branch based on `main` (so the coding agent works against a known-good state). The supervisor's integration branch accumulates the results.

If the supervisor determines the work split was bad (too many conflicts, interdependent changes that should have been one task), it can give up and report the issue rather than producing a broken merge.

---

## SQLite State Management

State is stored in `.ateam/state.sqlite` at the organization root. The database is created on install (`ateam install`). It tracks projects, agents, and workspaces — enough for the CLI to operate without scanning the filesystem.

TODO: use strict typing for sqlite

### Schema

```sql
-- Projects: map internal ID to filesystem path
-- path is relative to the parent of .ateam/
CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL UNIQUE,
    git_remote  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Agents: what is configured per project
-- Can enable/disable without losing prompts and reports
CREATE TABLE agents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    name        TEXT NOT NULL,               -- e.g. 'security', 'refactor_small', 'testing_basic'
    enabled     INTEGER NOT NULL DEFAULT 1,  -- 1 = active, 0 = disabled (keeps config)
    prompt_path TEXT,                        -- relative path to prompt.md (NULL = uses org default)
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, name)
);

-- Workspaces: track lifecycle of each isolated work unit
CREATE TABLE workspaces (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id           INTEGER NOT NULL REFERENCES projects(id),
    agent_id             INTEGER REFERENCES agents(id),  -- NULL for adhoc workspaces
    name                 TEXT NOT NULL UNIQUE,            -- human-readable, DNS-label-safe
    purpose              TEXT NOT NULL CHECK(purpose IN ('report', 'review', 'code', 'feature', 'adhoc')),
    sandbox_type         TEXT NOT NULL DEFAULT 'docker'  -- 'docker', 'none' (from config or override)
                         CHECK(sandbox_type IN ('docker', 'none')),
    status               TEXT NOT NULL DEFAULT 'Idle'
                         CHECK(status IN ('Idle', 'InUse', 'Error', 'Done')),
    branch               TEXT NOT NULL,                  -- e.g. 'ateam/code-fix-auth-timeout-1'
    worktree_path        TEXT NOT NULL,                  -- relative path to worktree directory
    container_id         TEXT,                           -- Docker container ID (if running)
    git_commit_at_creation TEXT NOT NULL,                -- SHA of base commit when worktree was created
    last_git_commit      TEXT,                           -- SHA of latest commit in the workspace
    result_status        TEXT CHECK(result_status IN ('PASS', 'FAIL')),  -- from result.md
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
```

### Usage Examples

```sql
-- List active workspaces for a project
SELECT w.name, w.purpose, w.status, a.name as agent
FROM workspaces w
LEFT JOIN agents a ON w.agent_id = a.id
WHERE w.project_id = 1
ORDER BY w.created_at;

-- Find all failed workspaces that haven't been cleaned up
SELECT name, branch, worktree_path
FROM workspaces
WHERE status = 'Error';

-- Disable an agent without losing its configuration
UPDATE agents SET enabled = 0, updated_at = datetime('now')
WHERE project_id = 1 AND name = 'refactor_small';

-- Get workspace by name (what the CLI does for -w flag)
SELECT * FROM workspaces WHERE name = 'code-fix-auth-timeout-1';
```

### Why SQLite

- Single file, no daemon, zero configuration
- The CLI reads/writes it directly — no server process needed
- Survives crashes (WAL mode, ACID transactions)
- Human-inspectable: anyone can `sqlite3 .ateam/state.sqlite` to poke around
- Lightweight enough for the POC, powerful enough for production

---

## End-to-End Flow (POC)

A typical supervisor-driven coding cycle:

```bash
# 1. Supervisor decides on tasks from reports/reviews
#    (this is the existing report + review flow)

# 2. Supervisor creates workspaces and dispatches tasks sequentially
ateam ws create -w code-fix-auth-timeout --purpose code
ateam code "Fix the authentication timeout bug per review finding R-12" \
  -w code-fix-auth-timeout

ateam ws create -w code-add-rate-limiter --purpose code
ateam code @tasks/rate-limiter.md -w code-add-rate-limiter

# 3. Supervisor checks results
ateam ws list

# 4. If a workspace failed, inspect it
ateam ws shell -w code-fix-auth-timeout
# ... or have the supervisor read result.md and decide to retry

# 5. Supervisor merges workspace branches into integration
#    (this happens internally as part of the supervisor's workflow)

# 6. Push to main
ateam push

# 7. Clean up
ateam ws cleanup --done
```

The supervisor agent orchestrates steps 2-6 programmatically. The human's involvement is limited to reviewing the final commits on `main` (or stepping in when the supervisor reports a failure it can't resolve).

---

## Config Additions

Add to `config.toml` per project:

```toml
[sandbox]
type = "docker"                         # "docker" or "none"
image = "ateam-agent:latest"            # Docker image name
dockerfile = ""                         # path to Dockerfile, if building locally
network = "host"                        # "host", "none", or "bridge"

[coding]
timeout_minutes = 30                    # max time for a coding task
auto_rebase = true                      # rebase workspace onto base before coding
```

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Agent installs malicious packages | `--network host` means full internet. For POC this is acceptable — agent can only write to the worktree. Production adds firewall with domain allowlists. |
| Agent takes too long | Timeout kills the container. Default 30 minutes, configurable. |
| Workspaces accumulate | `ateam ws cleanup --done` cleans up finished workspaces. `ateam ws list` makes stale ones visible. |
| Docker not installed | `ateam ws create` checks for Docker and gives a clear error. Falls back to `none` sandbox if configured. |
| Auth token not available | CLI checks `ANTHROPIC_API_KEY` before launching sandbox. Clear error if missing. |
| Agent makes bad changes | Changes are on a branch. Supervisor reviews before merging into integration. Developer reviews before pushing to main. Two layers of defense. |
| Rebase conflicts during coding | If `auto_rebase` fails, workspace status set to `Error`. Supervisor can retry or re-dispatch. |
| SQLite concurrent access | WAL mode handles concurrent reads. The CLI is the only writer, and workspaces run sequentially in the POC, so write contention is not an issue. |
| Workspace name collisions | The `-{seq}` suffix and UNIQUE constraint in SQLite prevent duplicates. |

---

## What's NOT in This POC

- **Parallel workspace execution** — tasks are dispatched sequentially (Mode 2). Parallel mode (Mode 1) and hybrid (Mode 3) are future work.
- **Automatic conflict resolution** — if merging a workspace branch produces conflicts, the supervisor reports failure rather than attempting resolution.
- **Additional sandbox backends** — only `docker` and `none` are implemented. The sandbox abstraction supports future backends (Podman, Firecracker, etc.) but they are not built yet.
- **Persistent workspace reuse** — each task gets a fresh workspace. Resuming a failed task reuses the existing workspace, but no workspace persists across unrelated tasks.
- **Budget control** — no per-workspace token or cost limits.
- **Workspace-to-workspace communication** — workspaces are fully isolated. No shared state, no message passing.
- **The `merge` command** — integration is handled by the supervisor internally. An explicit `ateam merge` command may be needed in the future for manual integration workflows.
