# Prompt: Generate a project-specific Dockerfile + docker-compose for ateam

This is a prompt to give to Claude (or use as an ateam auto-setup enhancement). It generates a Dockerfile and run script tailored to a specific project.

Make the following changes:
* structure your work in the following parts:
    * Steps required to build the project in docker
        * tech stack needed to compile
        * tools helpful when doing development
            * basic CLI like: ag, fd, jq, etc ...
            * if the project is or contains a web app install playwright-cli
        * identify the workspace directory to mount (the git repo base need to be mounted in docker but the starting directory must be the directory with .ateam if it exists)
    * Common quality of life steps
        * simplified user id mapping to/from the image
    * Steps required to run a coding agent within docker
        * Mount the shared claude code config directory in <ateamorg> if it exists (see CONTAINER.md)
    * Steps required to run ateam within the container
        * install the ateam binary
        * configure ateam secrets with agent env variables and API keys as needed
* Produce:
    * Dockerfile
    * docker.sh [--help] build | start | stop | restart | status | shell | exec | claude | codex [--container NAME]

---

## The Prompt

You are setting up a Docker environment for a software project so that ateam agents and Claude Code can build, test, and work on the code inside an isolated container.

### What you need to produce

1. A `Dockerfile` (goes in `.ateam/Dockerfile`)
2. A `docker-run.sh` script (goes in `.ateam/docker-run.sh`) that builds the image and starts a container with the right mounts

### Requirements for the Dockerfile

**Base image**: Pick the right base for the project's primary language/runtime:
- Node/JS/TS projects → `node:20-bookworm-slim`
- Python projects → `python:3.12-slim-bookworm`
- Go projects → `golang:1.25-bookworm` (or slim variant if no CGo)
- Java/Kotlin → `eclipse-temurin:21-jdk-jammy`
- Ruby → `ruby:3.3-slim-bookworm`
- Multi-language → pick the primary, install others via apt or version managers
- If unsure → `debian:bookworm-slim` with manual installs

**Essential packages** (always install):
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree make \
    && rm -rf /var/lib/apt/lists/*
```

**Claude Code** (always install):
```dockerfile
RUN npm install -g @anthropic-ai/claude-code
```
If the base image doesn't have Node, install it first.

**Project-specific packages**: Analyze the project to determine what's needed:
- Look at `package.json`, `requirements.txt`, `pyproject.toml`, `go.mod`, `Gemfile`, `build.gradle`, `pom.xml`, `Makefile`, `Dockerfile` (existing), `.tool-versions`, `.nvmrc`, etc.
- Look at CI config (`.github/workflows/`, `.gitlab-ci.yml`, `Jenkinsfile`) for build/test dependencies
- Look for database services (postgres, mysql, redis, etc.) in docker-compose files or test configs
- Install compilers, build tools, runtime dependencies needed for `make build`, `npm test`, `go test`, `pytest`, etc.

**User setup** (match host UID for file ownership):
```dockerfile
ARG USER_UID=1000
RUN if getent passwd $USER_UID >/dev/null 2>&1; then \
      EXISTING=$(getent passwd $USER_UID | cut -d: -f1); \
      usermod -l agent -d /home/agent -m "$EXISTING" 2>/dev/null || true; \
    else \
      useradd -m -u $USER_UID agent; \
    fi
RUN echo "agent ALL=(root) NOPASSWD: /bin/chown, /bin/ln" >> /etc/sudoers.d/agent-chown
```

**Entrypoint** (fix volume ownership + symlink ateam binary):
```dockerfile
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
USER agent
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
```

Where `entrypoint.sh` is:
```sh
#!/bin/sh
if [ -d "$HOME/.claude" ] && [ "$(stat -c %u "$HOME/.claude" 2>/dev/null)" != "$(id -u)" ]; then
    sudo chown -R "$(id -u):$(id -g)" "$HOME/.claude" 2>/dev/null || true
fi
if [ -f /opt/ateam/ateam-linux-amd64 ] && [ ! -f /usr/local/bin/ateam ]; then
    sudo ln -sf /opt/ateam/ateam-linux-amd64 /usr/local/bin/ateam 2>/dev/null || true
fi
exec "$@"
```

**Environment files**: Look for `.env.example`, `.env.test`, `.env.sample`, `env.template`, or similar. If found, copy it into the image as `.env` or document which env vars need to be set.

### Requirements for docker-run.sh

The script should:

1. **Build the image** with `--build-arg USER_UID=$(id -u)` to match the host user
2. **Mount the workspace** at `/workspace`
3. **Mount the ateam linux binary** (from the ateam build dir or the org cache) read-only at `/opt/ateam/`
4. **Sync timezone** with the host:
   ```bash
   if [[ -f /etc/localtime ]]; then
       args+=(-v "/etc/localtime:/etc/localtime:ro")
   fi
   args+=(-e "TZ=${TZ:-$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || echo UTC)}")
   ```
5. **Mount shared Claude config** if it exists. Check `<ateamorg>/linux-shared-claude/` (where `<ateamorg>` is resolved from `ateam env` output or defaults to `~/.ateamorg`). If the directory contains `.claude/`, `.claude.json`, and/or `secrets.env`, mount them:
   ```bash
   SHARED_CLAUDE="$ATEAMORG_DIR/linux-shared-claude"
   if [[ -d "$SHARED_CLAUDE/.claude" ]]; then
       args+=(-v "$SHARED_CLAUDE/.claude:/home/agent/.claude")
   fi
   if [[ -f "$SHARED_CLAUDE/.claude.json" ]]; then
       args+=(-v "$SHARED_CLAUDE/.claude.json:/home/agent/.claude.json")
   fi
   if [[ -f "$SHARED_CLAUDE/secrets.env" ]]; then
       args+=(-v "$SHARED_CLAUDE/secrets.env:/home/agent/.ateamorg/secrets.env")
   fi
   ```
6. **Support interactive and detached modes** via `--interactive` / `--detach` flags
7. **Pass through extra args** after `--` to claude (e.g., `-- -p "hello"`)

### How to analyze the project

Run these commands and use the output to determine dependencies:

```bash
# Project structure
find . -maxdepth 2 -name "*.json" -o -name "*.toml" -o -name "*.yaml" -o -name "*.yml" \
  -o -name "Makefile" -o -name "Dockerfile" -o -name "*.gradle*" -o -name "pom.xml" \
  -o -name "Gemfile" -o -name "*.mod" -o -name "*.sum" | head -30

# Package managers
cat package.json 2>/dev/null | jq '.dependencies,.devDependencies' 2>/dev/null
cat requirements.txt 2>/dev/null
cat pyproject.toml 2>/dev/null
cat go.mod 2>/dev/null | head -5

# CI config (reveals build/test commands and dependencies)
cat .github/workflows/*.yml 2>/dev/null | head -100

# Existing Docker setup (reuse what's there)
cat Dockerfile 2>/dev/null
cat docker-compose*.yml 2>/dev/null

# Env files
ls -la .env* env.* 2>/dev/null

# Test commands
grep -r "test" Makefile 2>/dev/null | head -10

# Ateam org location
ateam env 2>/dev/null | head -5
```

### Output format

Produce:
1. `.ateam/Dockerfile` — the complete Dockerfile
2. `.ateam/entrypoint.sh` — the entrypoint script
3. `.ateam/docker-run.sh` — the build + run script (executable)
4. Brief explanation of what was detected and why specific packages were included
