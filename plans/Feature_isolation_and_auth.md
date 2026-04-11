# Isolation and Auth: Gap Analysis + Planned Changes

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
4. **Standalone agent config + container audit via `agent-config`** — see next section
5. **Add auth recommendation to `ateam env`** — context-aware guidance
6. **Document `sandbox_inside_container` use cases** — when and why to enable

---

## Planned: Standalone Agent Config and Container Auth Management

Extend `ateam agent-config` rather than adding a new command. Three capabilities:

### 1. Standalone (isolated) agent config: `--config-dir PATH`

Create a self-contained Claude config directory at a given path, fully isolated from the default `~/.claude`. This works on the base host or inside a container.

```bash
# Create isolated config and do browser login
ateam agent-config --setup-interactive --config-dir .ateam/.claude --container-only=false

# Audit auth state of an isolated dir
ateam agent-config --audit --config-dir .ateam/.claude
```

The isolated dir stores its own `.credentials.json`, `settings.json`, and session state. It's used by the `claude-isolated` agent (`config_dir = ".claude"` in runtime.hcl) or any custom agent definition that sets `config_dir`.

**Behavior:**
- Creates the directory if it doesn't exist
- Runs `claude` with `CLAUDE_CONFIG_DIR=PATH` for interactive login
- Errors if PATH already has `.credentials.json` (use `--wipe-i-am-sure` first)
- `--setup-interactive` implies `--container-only=false` when `--config-dir` is set (isolated dirs are a host-side feature)

**Safety:** refuses if PATH resolves to `~/` or `~/.claude` — those are the default config paths, not isolated ones.

### 2. Container auth audit: `--container NAME`

Exec into a running container to audit auth state remotely.

```bash
ateam agent-config --audit --container my-app-dev
```

Runs `docker exec` to check:
- Whether `ateam` binary exists in the container
- Whether `.ateam/secrets.env` exists (for ateam-inside-docker mode)
- Whether `~/.claude/.credentials.json` exists (for interactive auth)
- If ateam is available: runs `ateam agent-config --audit` inside the container

Also checks on the host: whether relevant secrets exist in `ateam secret` and whether `--save-project-scope` needs to be (re-)run.

Future: `--container` could support `--setup-interactive` to run the interactive bootstrap inside the container, and `--wipe-i-am-sure` to clean container-side config.

### 3. Protection against dangerous operations on default config

**Principle:** never wipe or overwrite `~/.claude` through ateam tooling. Two safe paths instead:

- **On the base host:** always use a standalone (isolated) config dir at a custom path. The default `~/.claude` is the user's personal Claude config — ateam should not touch it. `--config-dir` enforces this separation.
- **Inside a container:** login interactively from within the container (browser login or refresh token bootstrap via `--setup-interactive`). This creates credentials in the container's own `~/.claude`, which is ephemeral by nature.

The existing `--wipe-i-am-sure` already has guards (container-only by default, Linux-only). These stay. The additional rule: `--config-dir` must not resolve to `~/` or `~/.claude`, and `--container` + `--wipe-i-am-sure` only affects the container's config, never a volume-mounted host `~/.claude`.

**Open question:** should we detect and refuse to wipe when `~/.claude` inside a container is a bind mount from the host? This could be checked via `docker exec NAME stat -f %d ~/.claude` vs the container root, or by inspecting mount points. Worth implementing if feasible, otherwise document the risk.

---

## Key Finding: `.claude.json` Location Depends on `CLAUDE_CONFIG_DIR`

Claude Code stores account state (userID, oauthAccount, migrations) in a file called `.claude.json`. Its location follows two different conventions:

**Default (no `CLAUDE_CONFIG_DIR`):**
- Config directory: `~/.claude/` (contains `.credentials.json`, `settings.json`, sessions, etc.)
- Account state: `~/.claude.json` (at the home root, **outside** `~/.claude/`)
- These are at the same level: `~/.claude/` is a directory, `~/.claude.json` is a sibling file

**With `CLAUDE_CONFIG_DIR=/some/path`:**
- Config directory: `/some/path/` (contains everything)
- Account state: `/some/path/.claude.json` (**inside** the custom config dir)
- Fully self-contained — the entire config dir is portable

### Implications for Docker volumes

When mounting `~/.claude` as a Docker volume (the default case), the volume only covers the directory. `.claude.json` at the home root is on the container filesystem and is **lost when the container is recreated**. Claude Code backs it up inside `~/.claude/backups/.claude.json.backup.*` (which does persist in the volume), and prints a restore command on startup when it detects the file is missing.

**Workarounds for the default case:**
- Restore from backup on container startup (entrypoint or manual `cp`)
- Mount `.claude.json` as a separate file (`-v path/.claude.json:/home/agent/.claude.json`)

**Using `CLAUDE_CONFIG_DIR` avoids this entirely** — `.claude.json` is inside the config dir, so a single volume mount covers everything. This is the recommended approach for portable/shared agent configs.

### Implications for copying configs between containers

- **Default case:** must copy both `~/.claude/` and `~/.claude.json` (two separate paths)
- **`CLAUDE_CONFIG_DIR` case:** copy the single directory — everything is inside it
- Moving `.claude.json` into `~/.claude/` does NOT work in the default case — Claude Code only looks at `~/.claude.json` (home root)

### Recommendation

Prefer `CLAUDE_CONFIG_DIR` for any managed/automated agent config. It provides full isolation and portability in a single directory. Reserve the default `~/.claude` layout for interactive human use where the split location is handled naturally by the OS.

---

## Recommended Approach: Shared Linux Agent Config Directory

A single host directory holds the complete Linux agent identity: credentials, account state, and OAuth token. It can be volume-mounted into containers or copied in/out.

### Host layout

```
~/.ateamorg/claude_linux_shared/
  .claude/          # mounted as $HOME/.claude
  .claude.json      # mounted as $HOME/.claude.json
  secrets.env       # mounted as $HOME/.ateamorg/secrets.env (org scope)
                    # contains CLAUDE_CODE_OAUTH_TOKEN for headless agents
```

### Volume-mount approach

Three mounts from the shared dir into any container:

```bash
docker run \
  -v ~/.ateamorg/claude_linux_shared/.claude:/home/agent/.claude \
  -v ~/.ateamorg/claude_linux_shared/.claude.json:/home/agent/.claude.json \
  -v ~/.ateamorg/claude_linux_shared/secrets.env:/home/agent/.ateamorg/secrets.env \
  ...
```

`start.sh --volume ~/.ateamorg/claude_linux_shared` handles this automatically.

Inside the container:
- Interactive `claude` works (`.credentials.json` + `.claude.json` present)
- `ateam run` resolves `CLAUDE_CODE_OAUTH_TOKEN` from org scope (`.ateamorg/secrets.env`) naturally
- No `CLAUDE_CONFIG_DIR`, no env vars to manage, no entrypoint hacks
- Host secret system is untouched (the file is only mounted inside containers)

### Copy in/out approach

For containers where you can't change the mount configuration.

#### CLI

```bash
# Copy agent config out of a running container
ateam agent-config --copy-out --container NAME [--path PATH] [--home CUSTOM_HOME]

# Copy agent config into a running container
ateam agent-config --copy-in --container NAME [--path PATH] [--force] [--copy-ateam] [--home CUSTOM_HOME]
```

`--path` defaults to `~/.ateamorg/claude_linux_shared/`.

`--home` overrides the container home directory. Auto-detected via `docker exec CONTAINER sh -c 'echo $HOME'` by default.

Container must be running (required for `$HOME` detection, file ownership fix, etc.).

#### --copy-out behavior

Copies from the container to the local path:
- `$HOME/.claude/` → `PATH/.claude/`
- `$HOME/.claude.json` → `PATH/.claude.json`
- Does NOT copy `$HOME/.ateamorg/secrets.env` — that file is manually maintained and contains the OAuth token from `claude setup-token`. Overwriting it would lose a token that can't be automatically regenerated.

Detects container user via `docker exec CONTAINER id -un` for accurate reporting.

#### --copy-in behavior

Copies from the local path into the container:
- `PATH/.claude/` → `$HOME/.claude/`
- `PATH/.claude.json` → `$HOME/.claude.json`
- `PATH/secrets.env` → `$HOME/.ateamorg/secrets.env` (if the file exists in PATH)
- Fixes file ownership after copy (`chown` to container user)
- `--force` overwrites existing `$HOME/.claude/` (clears contents, doesn't remove mount point)
- `--copy-ateam` also copies the ateam linux binary into the container

Writing `secrets.env` on copy-in is safe because the source file is the user's curated version from the shared dir.

#### `CLAUDE_CONFIG_DIR` detection

If `$CLAUDE_CONFIG_DIR` is set inside the container, both copy-out and copy-in adapt:
- `.claude.json` is inside the config dir (not at `$HOME/.claude.json`)
- Copy paths adjust automatically

`--home` has no effect when `CLAUDE_CONFIG_DIR` is detected (the config dir location is explicit).

### Setup workflow

One-time setup for a new Linux shared agent config:

```bash
# 1. Start a container with a fresh volume
start.sh --name setup --volume ~/.ateamorg/claude_linux_shared

# 2. Inside the container: interactive login
claude auth login
# Copy/paste URL, authorize, paste token

# 3. Inside the container: generate OAuth token for headless use
claude setup-token
# Copy/paste URL, authorize — prints the token

# 4. Inside the container: save token to org secrets
ateam secret CLAUDE_CODE_OAUTH_TOKEN --scope org --set
# Paste the token from step 3

# 5. Exit and stop the setup container
exit
```

The shared dir is now ready. Mount it into any container or use `--copy-in`.

### Flaw: secrets.env is manually maintained

The OAuth token in `secrets.env` is generated by `claude setup-token` and manually saved via `ateam secret`. There is no automated way to regenerate it — if the token expires, the user must redo the `setup-token` flow. `--copy-out` intentionally skips `secrets.env` to avoid overwriting a valid token with stale data from a container, but this means the canonical copy lives only in the shared dir on the host.

Mitigation: `ateam agent-config --copy-out` warns if `PATH/secrets.env` is missing or empty, suggesting the user run `claude setup-token` if headless agents are needed.
