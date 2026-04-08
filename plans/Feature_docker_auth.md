# Gap Analysis: Execution Modes × Auth Modes

## Context

Ateam supports multiple execution modes (how/where the agent runs) and multiple Claude Code auth modes (how the agent authenticates). The combinations create a matrix where some paths work well, some have rough edges, and some are broken or undocumented. This analysis maps the current state to identify gaps.

## Execution Modes

| # | Mode | Description | Container? | Sandbox? |
|---|------|-------------|------------|----------|
| 1 | **Sandbox** | Default host execution with OS-level sandbox (Seatbelt/bubblewrap) | No | Yes |
| 2 | **Sandbox Isolated** | Host execution with isolated `CLAUDE_CONFIG_DIR` (e.g., `.ateam/.claude/`) | No | Yes |
| 3 | **Docker** | Ateam-managed oneshot container (build + run + remove) | Yes (managed) | No (skipped) |
| 4 | **Docker-exec** | Exec into user-managed container (compose, devcontainer, manual) | Yes (external) | No (skipped) |
| 5 | **Inside Docker** | Ateam itself runs inside a container (no outer ateam on host) | Yes (ambient) | No (skipped) |

## Auth Modes

| # | Mode | Mechanism | Cost Model | Interactive? |
|---|------|-----------|------------|-------------|
| A | **OAuth** | `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) | Subscription | Headless only (`-p`) |
| B | **API** | `ANTHROPIC_API_KEY` | Pay-per-use | Both headless and interactive |
| C | **None** | Interactive login (`.credentials.json`) or no auth at all | Subscription | Full (Remote Control, etc.) |

## Matrix: Current Behavior

### Legend
- **OK**: Works correctly
- **WORKS BUT**: Functions but has rough edges
- **BROKEN**: Does not work
- **UNTESTED**: Not verified, behavior uncertain
- **N/A**: Not applicable

---

### 1. Sandbox (default host) 

| Auth | Status | How auth reaches agent | What happens |
|------|--------|----------------------|--------------|
| **OAuth** | **OK** | Env var (resolved by `ValidateSecrets` → `os.Setenv`) | Agent runs headless with sandbox restrictions. Token from env/secrets store. |
| **API** | **OK** | Env var (same chain) | Agent runs headless with sandbox restrictions. Stateless auth. |
| **None** | **WORKS BUT** | `.credentials.json` in `~/.claude/` from prior browser login | Agent uses stored credentials. **Gap: no upfront validation** — if credentials are expired/missing, agent fails at runtime with a confusing Claude API error, not an ateam error. |

**Gaps:**
- No upfront auth validation on host (only validated for container runs in `table.go:111-116`). Silent runtime failure if no auth exists.

---

### 2. Sandbox Isolated (`claude-isolated` agent, `config_dir = ".claude"`)

| Auth | Status | How auth reaches agent | What happens |
|------|--------|----------------------|--------------|
| **OAuth** | **OK** | Env var | Same as sandbox but `CLAUDE_CONFIG_DIR=.ateam/.claude/` isolates agent state. |
| **API** | **OK** | Env var | Same as above. |
| **None** | **BROKEN** | `.credentials.json` would need to be in `.ateam/.claude/` | Isolated config dir is empty on first use. No browser login has been done targeting this dir. No `ateam` command to bootstrap credentials into an isolated config dir. Agent fails with login prompt in headless mode. |

**Gaps:**
- **No bootstrap path for isolated config dir auth.** `ateam agent-config --setup-interactive` doesn't support `--config-dir`. You'd need to manually `CLAUDE_CONFIG_DIR=.ateam/.claude/ claude` and do browser login, which is undocumented.
- Isolated mode is documented in `runtime.hcl` but has no user-facing docs in REFERENCE.md or CONTAINER.md.
- No `ateam auto-setup` awareness of isolated config — it won't configure or mention it.

---

### 3. Docker (ateam-managed oneshot)

| Auth | Status | How auth reaches agent | What happens |
|------|--------|----------------------|--------------|
| **OAuth** | **OK** | Env var via `docker run -e` (`forward_env`) + optional `.credentials.json` mount (`mount_claude_config=true`) | Dual delivery: env var for headless auth, file mount for session context. `docker` profile mounts `.credentials.json:ro`. `docker-api` profile does not. |
| **API** | **OK** | Env var via `docker run -e` | Stateless, no file mount needed. Works with `docker` or `docker-api` profile. |
| **None** | **BROKEN** | No TTY forwarded → can't do interactive login. No `.credentials.json` inside container. | Container starts, agent can't authenticate, fails. `ValidateSecrets` catches this upfront with Docker-specific error message. |

**Gaps:**
- **OAuth + `mount_claude_config=true` on macOS**: macOS stores credentials in Keychain, NOT in `~/.claude/.credentials.json`. The mount may find an empty or nonexistent file. This is mentioned in the docker rework plan but not clearly documented for users. The `docker-api` profile works around this but users don't know when to use it.
- **No clear guidance** on `docker` vs `docker-oauth` vs `docker-api` profile selection. Three profiles for two auth methods is confusing — `docker` and `docker-oauth` are identical.
- `mount_claude_config` mounts only `.credentials.json` but the session-scoped token in it may have expired. No refresh mechanism inside a oneshot container.

---

### 4. Docker-exec (user-managed container)

| Auth | Status | How auth reaches agent | What happens |
|------|--------|----------------------|--------------|
| **OAuth** | **OK** | Env var via `docker exec -e` | Works. No file mount possible (container already running, can't add mounts). |
| **API** | **OK** | Env var via `docker exec -e` | Works. Stateless. |
| **None** | **WORKS BUT** | `.credentials.json` must already exist inside the container from a prior login | If user has previously done `claude` inside the container and logged in, credentials persist in the container's filesystem. But: container rebuild loses them. No ateam command helps here. |

**Gaps:**
- **No file-based auth forwarding.** `docker-exec` can only forward env vars, not mount files. If a user needs OAuth session context (`.credentials.json`), they must have it pre-installed in the container. This is a fundamental limitation of `docker exec` but it's not documented.
- **`copy_ateam` may fail** if container filesystem is read-only at `/usr/local/bin/`. No fallback path documented.
- **Precheck script receives `$1` as container name** but the docs use `{{.Names}}` Go template syntax in the grep which is Docker-specific — won't work with podman's different format string, despite `exec` template supporting podman.
- **Working directory resolution** assumes `/workspace` or similar. If the user's container has code at a different path, there's no override in `docker-exec` config (only implicit from git root detection).

---

### 5. Inside Docker (ateam running directly inside container)

| Auth | Status | How auth reaches agent | What happens |
|------|--------|----------------------|--------------|
| **OAuth** | **OK** | From `.ateam/secrets.env` (via `ateam secret --save-project-scope` on host) → `ValidateSecrets` → `os.Setenv` → child process | Core use case. Works well. Secret isolation means interactive `claude` doesn't see the token. |
| **API** | **OK** | Same as OAuth, or from container env (inherited from `docker run -e`) | Works. |
| **None** | **WORKS BUT** | `.credentials.json` must exist in container's `~/.claude/` from prior browser login | Requires one-time browser login inside container. `ateam agent-config --setup-interactive` can bootstrap from refresh token. But: **refresh token must be saved first** via `--save-refresh-token` after a successful login, and stored via `ateam secret`. Multi-step flow. |

**Gaps:**
- **No upfront validation** for inside-docker mode. `ValidateSecrets` is only called for explicit container launches (`table.go:111-116`), not when ateam detects it's already inside a container. Missing auth causes silent runtime failure.
- **`.ateam/secrets.env` persistence**: if user forgets `--save-project-scope` after changing a secret on the host, the container has stale credentials. `ateam env` warns about this but only when run on the host.
- **Refresh token expiry** is not handled. When the refresh token expires, `--setup-interactive` fails silently or with a confusing error. No detection or guidance.

---

## Cross-Cutting Gaps

### 1. Validation asymmetry
Secret validation (`ValidateSecrets`) only runs for container launches from the host. Missing for:
- Host sandbox runs (no validation at all)
- Inside-docker runs (ambient container, no explicit launch)

This means host and inside-docker modes fail at agent runtime with Claude API errors instead of clear ateam errors.

### 2. macOS Keychain vs `.credentials.json`
On macOS, Claude Code stores credentials in Keychain, not in `~/.claude/.credentials.json`. This breaks:
- `mount_claude_config=true` (mounts an empty/nonexistent file)
- `--save-refresh-token` (reads `.credentials.json` which doesn't exist on macOS)
- Any Docker workflow that assumes credentials are in a file

Documented in the docker rework plan but not in user-facing docs.

### 3. Profile naming confusion
- `docker` and `docker-oauth` profiles are identical (both mount `.credentials.json`)
- No clear decision tree for which profile to use
- `docker-api` exists but its name suggests it's for "API usage" not "API key auth"

### 4. Interactive login inside containers
The path is: browser login → `--save-refresh-token` → `ateam secret` → new container → `--setup-interactive`. This is 4+ steps, requires a browser-accessible container, and the distinction between OAuth token vs refresh token vs browser token is confusing.

### 5. Devcontainer support
`devcontainer` container type exists in `runtime.hcl` (REFERENCE.md documents it) but the gap analysis for auth is identical to `docker-exec` since devcontainer is essentially a `docker-exec` variant. The devcontainer CLI (`devcontainer up`) handles lifecycle but auth forwarding has the same env-only limitation.

### 6. `sandbox_inside_container` documentation
The field exists and defaults to `false`, but there's no guidance on when a user would want `true`. Use case: running ateam inside a shared container where you still want filesystem restrictions.

### 7. No auth mode detection/recommendation
`ateam env` shows which secrets are set but doesn't recommend an auth approach based on the execution mode. For example, it could say "You're using docker-exec — OAuth via env var is the recommended auth method" or "You're on macOS — use `docker-api` profile instead of `docker`".

---

## Summary: Status by Cell

| | OAuth | API | None (interactive) |
|---|---|---|---|
| **Sandbox** | OK | OK | Works but no upfront validation |
| **Sandbox Isolated** | OK | OK | **BROKEN** — no bootstrap path |
| **Docker** | OK | OK | **BROKEN** — no TTY, caught by validation |
| **Docker-exec** | OK | OK | Works but fragile (container rebuild loses creds) |
| **Inside Docker** | OK | OK | Works but complex multi-step setup |

**Key finding**: OAuth and API auth work across all modes. The gaps are concentrated in:
1. **"None" (interactive) auth** — broken or fragile in most container modes
2. **Validation coverage** — only container launches from host are validated
3. **macOS-specific** — credential file assumptions break for Docker workflows
4. **Documentation** — several working features lack user-facing docs

## Recommended Priority

1. **Extend `ValidateSecrets` to host runs** — quick win, prevents confusing runtime failures
2. **Document macOS credential behavior** — add to CONTAINER.md, recommend `docker-api` or `ateam secret` over `mount_claude_config`
3. **Remove `docker-oauth` profile** (or alias to `docker`) — reduce confusion
4. **Add `--config-dir` to `ateam agent-config`** — unblocks isolated config bootstrap
5. **Add auth recommendation to `ateam env`** — context-aware guidance
6. **Document `sandbox_inside_container` use cases** — when and why to enable
