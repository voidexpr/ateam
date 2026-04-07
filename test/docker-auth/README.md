# Docker Auth Test Scripts

Test Claude Code authentication inside Docker containers. Status: **experimental checkpoint** — refresh token bootstrap works for interactive sessions.

## What Works

- Browser login in container with persistent volume
- Extracting refresh token from authenticated session
- Bootstrapping interactive sessions in fresh containers via refresh token (no browser)
- Headless `-p` mode with `CLAUDE_CODE_OAUTH_TOKEN`

## TODO

- Try to revive an old session as-is (resume with same volume)
- Try to run both interactive sessions at the same time (shared credentials)

## Prerequisites

```bash
# Set the oauth token in ateam secrets (for headless -p mode)
ateam secret CLAUDE_CODE_OAUTH_TOKEN --set
```

## Full Setup Guide

### 1. Build image + linux binary

```bash
./test/docker-auth/build.sh
```

The ateam binary is NOT baked into the image — it's mounted from `build/` at runtime. After code changes, just run `make companion` (no image rebuild needed).

### 2. First-time browser login

```bash
./test/docker-auth/start.sh --name setup --interactive
```

Inside the container:
```bash
claude                                  # complete browser login → /exit
ateam agent-config --save-refresh-token   # extracts token, prints to stdout
exit
```

### 3. Save refresh token on host

```bash
./test/docker-auth/extract-refresh-token.sh --volume claude-setup \
    | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set
```

### 4. Test: fresh container with refresh token (no browser)

```bash
./test/docker-auth/start.sh --name fresh --login --interactive
```

Inside the container:
```bash
ateam agent-config --setup-interactive
# Prints "Login successful." → starts interactive claude session
```

### 5. Test: shared volume persistence

```bash
./test/docker-auth/start.sh --name reuse --volume claude-setup --interactive
```

Inside the container:
```bash
claude
# Should start without login prompt
```

### 6. Test: headless -p with oauth token

```bash
./test/docker-auth/start.sh --name headless --oauth -- -p "respond with: OK"
```

## What Actually Worked (Manual Testing April 2026)

The refresh token bootstrap flow:

1. Started a container, did browser login, extracted `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` from `.credentials.json`
2. Stopped that container
3. Started a fresh container (no volume sharing)
4. Set the refresh token: `export CLAUDE_CODE_OAUTH_REFRESH_TOKEN=<token>`
5. Ran `ateam agent-config --setup-interactive`
6. Got "Login successful." → interactive Claude session worked

Key finding: the token shown in the browser during OAuth is NOT the refresh token. The refresh token must be extracted from `~/.claude/.credentials.json` after a successful login.

## Scripts

| Script | Purpose |
|--------|---------|
| `build.sh` | Build Docker image + cross-compile ateam for linux |
| `start.sh` | Start container with persistent `.claude` volume |
| `extract-refresh-token.sh` | Extract refresh token from volume or container |
| `test-auth.sh` | Automated test suite |

### start.sh flags

| Flag | Description |
|------|-------------|
| `--name NAME` | Container name (required). Default volume: `claude-NAME` |
| `--volume VOL` | Reuse an existing volume |
| `--workspace DIR` | Mount as /workspace (default: cwd) |
| `--oauth` | Forward `CLAUDE_CODE_OAUTH_TOKEN` from ateam secrets |
| `--login` | Forward refresh token from ateam secrets for bootstrap |
| `--interactive` | Start bash shell (default) |
| `--detach` | Run in background |
| `-- ARGS` | Pass args to `claude` directly |
