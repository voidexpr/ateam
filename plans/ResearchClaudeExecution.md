# Claude Code Execution Research

Research notes on Claude Code (v2.1.90+) authentication, configuration, and execution modes. Findings are based on changelog analysis, source inspection of the bundled CLI, and empirical testing in Docker containers.

## Authentication Methods

Claude Code supports three authentication approaches, with a strict priority order:

| Priority | Method | Env Var / Mechanism | Scope | Works with `-p` | Works interactive |
|----------|--------|---------------------|-------|-----------------|-------------------|
| 1 (highest) | API Key | `ANTHROPIC_API_KEY` | Full API access | Yes | Yes |
| 2 | OAuth Token | `CLAUDE_CODE_OAUTH_TOKEN` | Inference-only | Yes | **No** |
| 3 (lowest) | Interactive Login | Browser OAuth → keychain / `.credentials.json` | Full (includes `user:inference` + `user:profile`) | Yes | Yes |

### API Key (`api`)

- Set `ANTHROPIC_API_KEY` with a key from console.anthropic.com
- Pay-per-use billing through Anthropic Console
- Works in all modes (interactive, `-p`, `--bare`)
- Highest priority: if set, all other auth methods are ignored

### OAuth Token (`oauth`)

- Set `CLAUDE_CODE_OAUTH_TOKEN` with a token from `claude setup-token`
- Uses your Claude subscription (Pro, Max, Team, Enterprise)
- **Inference-only**: the token is created with `inferenceOnly: true` and a 1-year expiry. It does NOT include the `user:profile` scope
- **Only works with `-p` (headless) mode**. Interactive mode checks for `user:inference` scope via a refresh token flow that this token type doesn't support, and falls back to the login prompt
- Remote Control explicitly rejects these tokens: *"Long-lived tokens are limited to inference-only for security reasons"*

### Interactive Login (`regular`)

- Triggered by `claude` without any auth env vars, or via `/login` command
- Browser-based OAuth flow that stores full-scope credentials (including `user:profile`)
- Credentials stored in OS keychain (macOS Keychain as "Claude Code-credentials") or `~/.claude/.credentials.json`
- Supports interactive sessions, Remote Control, and `-p` mode
- Tokens auto-refresh via `refreshToken`

### Token from `claude setup-token`

```bash
claude setup-token
# Outputs: export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-...
```

This command runs the OAuth flow with `inferenceOnly: true` and `expiresIn: 31536000` (1 year). The resulting token is designed for CI/automation with `-p` mode. It cannot be used for interactive sessions or Remote Control.

### Bootstrapping Interactive Sessions via Refresh Token

Claude Code supports bootstrapping full-scope interactive credentials from a refresh token, without a browser. This is the recommended way to set up interactive sessions in containers.

**Env vars:**
```bash
CLAUDE_CODE_OAUTH_REFRESH_TOKEN="<refreshToken>"
CLAUDE_CODE_OAUTH_SCOPES="user:profile user:inference"
```

When Claude Code starts with these set, it exchanges the refresh token for a fresh access token, stores full-scope credentials in `~/.claude/.credentials.json`, prints "Login successful.", and the session is immediately interactive — no browser needed. Remote Control also works.

**Credentials file format** (what gets written to `.credentials.json`):
```json
{
  "claudeAiOauth": {
    "accessToken": "...",
    "refreshToken": "...",
    "expiresAt": 1234567890,
    "scopes": "user:profile user:inference",
    "subscriptionType": "pro",
    "rateLimitTier": null
  }
}
```

**Practical flow:**

1. Do a browser-based interactive login once (in any container with a persistent volume):
   ```bash
   ./test/docker-auth/start.sh --name login-once --interactive
   # inside: claude → complete browser login → exit
   ```

2. Extract the refresh token:
   ```bash
   ./test/docker-auth/extract-refresh-token.sh --volume claude-login-once
   # or from a running container:
   ./test/docker-auth/extract-refresh-token.sh --container login-once
   ```

3. Store it in ateam secrets:
   ```bash
   ./test/docker-auth/extract-refresh-token.sh --volume claude-login-once \
     | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set
   ```

4. Any fresh container can now bootstrap interactive sessions:
   ```bash
   ./test/docker-auth/start.sh --name fresh-session --login --interactive
   # Claude auto-authenticates using the refresh token, no browser needed
   ```

This is better than volume mounting because the refresh token is a single string, portable across any container, and stored securely in ateam's secret system.

## `-p` (Print/Pipe) Mode vs Interactive Mode

### `-p` mode (headless)

- Non-interactive, reads prompt from args or stdin, outputs to stdout
- Skips the login screen entirely — uses env vars directly
- Auth resolution: `ANTHROPIC_API_KEY` → `CLAUDE_CODE_OAUTH_TOKEN` → stored credentials
- Combined with `--bare`: skips hooks, LSP, plugin sync, skill walks. Requires `ANTHROPIC_API_KEY` or `apiKeyHelper` — **OAuth and keychain auth disabled in `--bare`**
- `--bare -p` is ~14% faster to first API request (v2.1.81)

### Interactive mode (default)

- Full TUI with conversation history, tool approvals, slash commands
- Goes through `ConsoleOAuthFlow` component for auth
- Checks `needsLogin` state by calling `FI8()` which verifies:
  1. `kJ()` — is this a "first-party" session? (returns false for `--bare`/`CLAUDE_CODE_SIMPLE`)
  2. `n7()` — does the token have `user:inference` scope? (`CLAUDE_CODE_OAUTH_TOKEN` lacks this)
- If scopes are missing, shows the login method selection screen
- Supports Remote Control (`/remote-control`)

### Why `CLAUDE_CODE_OAUTH_TOKEN` fails in interactive mode

The interactive startup calls `n7()` which checks `VS(t7()?.scopes)` — whether the token's scopes include `user:inference`. Tokens from `claude setup-token` have `scopes: null` (inference-only), so `VS(null)` returns `false`. With `kJ()` returning `true` (first-party session), `n7()` returns `false`, but a separate code path in the interactive UI still triggers the login selection when it can't find full-scope credentials.

### Remote Control

Remote Control (`/remote-control` in interactive mode) allows controlling a Claude Code session from another device. It has specific auth requirements:

1. **Requires a valid interactive login** — a full-scope OAuth token with `refreshToken`, obtained via browser-based login. `CLAUDE_CODE_OAUTH_TOKEN` (inference-only) is not sufficient.

2. **`CLAUDE_CODE_OAUTH_TOKEN` must NOT be set** — even if you have a valid interactive login (`.credentials.json` with full-scope token), Remote Control will fail if `CLAUDE_CODE_OAUTH_TOKEN` is also present in the environment. The source code explicitly states: *"Remote Control requires a full-scope login token. Long-lived tokens (from `claude setup-token` or CLAUDE_CODE_OAUTH_TOKEN) are limited to inference-only for security reasons. Run `claude auth login` to use Remote Control."*

This creates a paradox for container setups: the inference-only token can't start an interactive session, but if you set it alongside a valid interactive login to have a fallback, it blocks Remote Control. The workaround is to use `regular` auth exclusively (no `CLAUDE_CODE_OAUTH_TOKEN` in the environment) when Remote Control is needed.

**Note (April 2026):** There is an inconsistency in how Claude Code handles `CLAUDE_CODE_OAUTH_TOKEN` — the token is insufficient to authenticate an interactive session (gets the login prompt), yet its mere presence blocks Remote Control even when a valid interactive login exists. This may be a regression or an intentional security boundary that was tightened recently. The behavior may have been different in earlier versions.

## Configuration Structure

### `~/.claude/` (config directory)

Controlled by `CLAUDE_CONFIG_DIR` env var, defaults to `$HOME/.claude`.

```
~/.claude/
  .claude.json          # Main state file (firstStartTime, userID, migrations, feature flags)
  .credentials.json     # OAuth credentials from browser login (if not using keychain)
  settings.json         # Permissions, sandbox config, forceLoginMethod
  plugins/              # Installed plugins/marketplace extensions
  skills/               # Custom skills
  plans/                # Saved plans
  projects/             # Per-project config and state
  sessions/             # Session history
  session-env/          # Session environment snapshots
  backups/              # .claude.json backup copies
  statsig/              # Feature flag cache (Statsig)
  cache/                # Changelog cache, etc.
  history.jsonl         # Command/conversation history
  telemetry/            # Telemetry data
  shell-snapshots/      # Shell state snapshots
  mcp-needs-auth-cache.json  # MCP OAuth state
  policy-limits.json    # Policy restrictions
```

### State file: `.claude.json`

Located at `$CLAUDE_CONFIG_DIR/.claude${suffix}.json` where suffix is empty for production.

Minimal content for a "not fresh install" state:
```json
{"firstStartTime": "2026-04-05T15:59:28.955Z"}
```

Full content after first session includes `userID`, `migrationVersion`, `cachedGrowthBookFeatures`, etc. Without this file, Claude treats the installation as fresh and may trigger onboarding flows.

### Resetting configuration

To reset Claude Code to a clean state for env-var-based auth in containers:

1. Remove all files in `~/.claude/` **except** `settings.json` (permissions config)
2. Optionally preserve `plugins/` and `skills/` if custom plugins/skills are installed
3. Recreate `.claude.json` with `{"firstStartTime": "<ISO timestamp>"}`
4. macOS only: delete keychain entry `security delete-generic-password -s "Claude Code-credentials"`

The `ateam agent-auth` command automates this.

### `settings.json`

Key fields relevant to auth and execution:

```json
{
  "skipDangerousModePermissionPrompt": true,
  "forceLoginMethod": "claudeai",
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": ["Read", "Edit", "Write", "Bash(*)", "..."],
    "deny": []
  }
}
```

- `forceLoginMethod`: `"claudeai"` (subscription) or `"console"` (API billing) — skips the login method selection screen but still requires credentials. Added in v1.0.32.
- `forceLoginOrgUUID`: string or array — restricts to specific organization(s)

## Environment Variables

### Authentication

| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | API key for direct API billing (highest auth priority) |
| `CLAUDE_CODE_OAUTH_TOKEN` | Long-lived inference-only OAuth token (from `claude setup-token`) |
| `CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR` | Read OAuth token from a file descriptor |
| `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` | OAuth refresh token — bootstraps full-scope interactive credentials on startup without browser login. Requires `CLAUDE_CODE_OAUTH_SCOPES`. |
| `CLAUDE_CODE_OAUTH_SCOPES` | Space-separated scopes for refresh token bootstrap (e.g., `"user:profile user:inference"`) |
| `CLAUDE_CODE_OAUTH_CLIENT_ID` | Custom OAuth client ID |
| `CLAUDE_CODE_CUSTOM_OAUTH_URL` | Custom OAuth server URL |
| `ANTHROPIC_AUTH_TOKEN` | Alternative auth token (lower priority than API key) |

### Configuration

| Variable | Purpose |
|----------|---------|
| `CLAUDE_CONFIG_DIR` | Override config directory (default: `~/.claude`) |
| `CLAUDE_CODE_SIMPLE` | Set to `1` for simplified mode — skips login check, plugins, skills. **Warning**: breaks `CLAUDE_CODE_OAUTH_TOKEN` recognition |
| `CLAUDE_CODE_ENTRYPOINT` | Set to `claude-desktop` for desktop app mode |

### Execution Control

| Variable | Purpose |
|----------|---------|
| `CLAUDE_CODE_DISABLE_AUTO_MEMORY` | Disable auto-memory |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` | Reduce network calls |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | Limit output tokens |
| `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` | Strip credentials from subprocess environments (v2.1.83) |
| `CLAUDE_CODE_BRIEF` | Brief output mode |
| `CLAUDE_CODE_REMOTE` | Enable remote mode |

### Provider Selection

| Variable | Purpose |
|----------|---------|
| `CLAUDE_CODE_USE_BEDROCK` | Use Amazon Bedrock |
| `CLAUDE_CODE_USE_VERTEX` | Use Google Vertex AI |
| `CLAUDE_CODE_USE_FOUNDRY` | Use Anthropic Foundry |
| `CLAUDE_CODE_USE_ANTHROPIC_AWS` | Use Anthropic AWS |
| `CLAUDE_CODE_API_BASE_URL` | Custom API base URL |

### Shell Detection

| Variable | Purpose |
|----------|---------|
| `CLAUDECODE` | Set to `1` by Claude Code in shells it spawns (Bash tool, tmux sessions). **Not** set in hooks or status line commands. Use to detect when a script is running inside a Claude Code shell. |

**How ateam uses `CLAUDECODE`:** When ateam itself runs inside a Claude Code session (common during development), the `CLAUDECODE=1` variable is inherited by child processes. If ateam then launches its own Claude Code agents, the nested Claude would see `CLAUDECODE=1` and think it's inside another Claude session, which can cause issues (e.g., nested session detection, different behavior).

To prevent this, all ateam agent definitions in `defaults/runtime.hcl` set `CLAUDECODE = ""` (empty string) in their `env` block:

```hcl
agent "claude" {
  env = {
    CLAUDECODE = ""   # unset in child process
  }
}
```

The empty-string convention in ateam's env handling means "exclude this key from the child process environment" — see `buildProcessEnv()` in `internal/agent/agent.go`. This ensures the spawned Claude Code agent doesn't inherit the parent's `CLAUDECODE=1`, preventing nested session detection artifacts.

### Container / Ateam Relevant

| Variable | Purpose |
|----------|---------|
| `CLAUDE_CODE_AGENT_NAME` | Agent name for identification |
| `CLAUDE_CODE_ORGANIZATION_UUID` | Force organization UUID |
| `CLAUDE_CODE_CONTAINER_ID` | Container identifier |

## Key Changelog References

- **v2.1.81**: Added `--bare` flag — OAuth and keychain auth disabled in bare mode
- **v2.1.81**: Fixed multiple concurrent sessions requiring repeated re-authentication
- **v2.1.83**: Added `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` to strip credentials from subprocesses
- **v1.0.32**: Added `forceLoginMethod` setting to bypass login selection screen
- **v1.0.31**: Fixed `~/.claude.json` getting reset when file contained invalid JSON

## Implications for Ateam Container Execution

1. **Headless agent runs** (ateam's primary use case): Use `CLAUDE_CODE_OAUTH_TOKEN` with `-p` flag. This is the designed and supported path.
2. **Interactive container sessions**: Require browser-based login (`regular` method). `CLAUDE_CODE_OAUTH_TOKEN` will NOT work for interactive sessions.
3. **Container auth reset**: Use `ateam agent-auth --method oauth` to clean stale auth state before launching agents.
4. **`--dangerously-skip-permissions`**: Used in containers to skip tool approval prompts for unattended operation.
5. **`--bare -p`**: Fastest headless mode, but requires `ANTHROPIC_API_KEY` (OAuth disabled).

## Docker Container Session Persistence

### Problem

Interactive Claude Code sessions require browser-based OAuth login which produces full-scope credentials. Docker containers are ephemeral — `~/.claude/` lives inside the container filesystem and is lost on rebuild. Re-authenticating via browser for every container rebuild is impractical.

Ateam does NOT mount `~/.claude/` into containers by default. The only persisted mounts are `.ateam/` (project state) and `.ateamorg/` (org config).

### Where Interactive Credentials Live

In Linux Docker containers (no OS keychain), credentials go to files:

| File | Purpose | Must persist |
|------|---------|-------------|
| `.credentials.json` | OAuth access/refresh tokens | **Yes** — this IS the auth |
| `.claude.json` | Install state (userID, migrations) | **Yes** — without it, Claude runs onboarding |
| `settings.json` | Permissions config | Recommended |

### Option A: Refresh Token Bootstrap (recommended)

Store the refresh token from a one-time browser login in ateam secrets. Any new container can bootstrap full-scope credentials instantly:

```bash
# One-time: extract from an authenticated container and store
./test/docker-auth/extract-refresh-token.sh --volume claude-login | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set

# Any new container: auto-authenticates on startup
docker run --rm -it \
    -e "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=$(ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --get)" \
    -e "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference" \
    ateam-auth-test claude
```

With ateam `runtime.hcl`:
```hcl
container "docker" {
  forward_env = ["CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

**Pros:** No volume needed, portable, stored securely in ateam secrets, works for interactive AND `-p` mode.
**Cons:** Refresh token eventually expires (rare). Need to re-login and re-extract when it does.

### Option B: Named Docker Volume

```bash
docker volume create claude-config
docker run -v claude-config:/home/agent/.claude ...
claude  # → browser OAuth → credentials stored in volume
```

Subsequent containers with the same volume skip login. With ateam `runtime.hcl`:

```hcl
container "docker" {
  type          = "docker"
  extra_volumes = ["claude-config:/home/agent/.claude"]
  forward_env   = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

**Pros:** Survives rebuilds, isolated per volume name, easy per-project volumes.
**Cons:** Opaque (need `docker run ... ls` to inspect).

### Why Mounting Host `~/.claude` Doesn't Work (macOS)

On macOS, credentials are stored in the OS Keychain (entry "Claude Code-credentials"), NOT in `~/.claude/.credentials.json`. The `~/.claude/` directory contains state, settings, plugins — but not the auth tokens. Mounting it into a container gives everything except the credentials.

### Container-to-Container Credential Portability

Authenticating in container A and reusing credentials in container B **does work** because Linux containers have no OS keychain — credentials go to `~/.claude/.credentials.json` as a plain file:

1. Start container A with a named volume at `/home/agent/.claude`
2. Run `claude` interactively → browser OAuth → tokens written to `.credentials.json` in the volume
3. Stop container A
4. Start container B with the same volume → Claude finds `.credentials.json` → authenticated

The tokens auto-refresh via `refreshToken`. This works until the refresh token expires (rare). See `test/docker-auth/` for scripts that automate this flow.

### Option C: Bind Mount from Host

```bash
mkdir -p ~/.ateamorg/claude-docker-config
docker run -v ~/.ateamorg/claude-docker-config:/home/agent/.claude ...
```

With ateam:
```hcl
container "docker" {
  extra_volumes = ["~/.ateamorg/claude-docker-config:/home/agent/.claude"]
}
```

**Pros:** Visible on host, easy to back up.
**Cons:** UID alignment needed (ateam passes `--build-arg USER_UID=$(id -u)`).

### Option D: Persistent Container Mode

Ateam supports persistent containers (`mode = "persistent"`):

```hcl
container "docker-persistent" {
  type = "docker"
  mode = "persistent"
}
```

Container stays running between agent invocations. Login once per container lifetime. Lost on `docker rm` or host reboot — combine with a named volume for true persistence.

### Minimal Persistent Set

For credentials only: `.credentials.json`, `.claude.json`, `settings.json`.
With plugins/skills: also `plugins/`, `skills/`.
Do NOT persist: `sessions/`, `session-env/`, `backups/`, `statsig/` — stale session state can cause auth issues.

### Recommended Setup

**Headless runs (primary ateam use case):** No persistence needed. Use `CLAUDE_CODE_OAUTH_TOKEN` via `forward_env` with `-p` mode.

**Interactive container sessions:**
```hcl
container "docker" {
  type          = "docker"
  mode          = "persistent"
  extra_volumes = ["claude-config-PROJECTNAME:/home/agent/.claude"]
  forward_env   = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

First use: exec into container, run `claude`, complete browser login. Credentials persist in volume.
