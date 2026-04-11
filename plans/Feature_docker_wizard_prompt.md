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
    * docker.sh [--help] build | precheck | start | stop | restart | status | shell | exec | claude | codex [--container NAME]

Options / hints:
* --agent yes/no
* --ateam yes/no
* --workspace PATH (force a specific path)
* --syspackages yes/no (iotop, htop, etc ...)
* --codex yes/no
* --remote-control

Actions
precheck is designed to work with ateam precheck container script and can used as:

    precheck = docker/docker.sh precheck --container

It will:
* build if necessary
* start if necessary

Q: firewall stuff ?
Q: devcontainer ?

docker/docker.sh claude --remote-control

alias: dcrc

.env.docker convention ?

Idea:
* also support a generic super-set docker container to get started or quickly jump onto a project with a known env
    * see: https://github.com/CoderLuii/HolyClaude


Additional research for ideas:

    Let me search for this — there are some emerging projects in this space.Good finds. There are several projects in this space, ranging from "generate a Dockerfile for my project" to "batteries-included AI coding workstation." Here's the landscape:

    ---

    **1. [ClaudeBox](https://github.com/RchGrav/claudebox)** (~849 stars) — the closest match to what you described. It provides per-project Docker isolation with development profiles you can compose (e.g., `claudebox profile python ml` or `claudebox profile rust go`). It includes network isolation with project-specific firewall allowlists, GitHub CLI, delta, fzf, zsh with oh-my-zsh powerline, and automatic per-project venv creation with uv. It also handles tmux socket mounting for multi-pane workflows. Claude Code is pre-installed inside the container. The profile system is the key differentiator — it builds project-appropriate Docker images with common dev packages baked in. It's a bash script, not something that uses AI to *generate* the Dockerfile, but it's the "quickly configure docker for a given dev project" tool you're describing.

    **2. [HolyClaude](https://github.com/CoderLuii/HolyClaude)** — a different philosophy: one mega-container with everything pre-installed. It bundles Claude Code, Gemini CLI, OpenAI Codex, Cursor, TaskMaster AI, Junie, and OpenCode, plus a CloudCLI web UI on port 3001, headless Chromium with Xvfb and Playwright, and 50+ dev tools including Node.js 22, Python 3, database clients, and deployment CLIs. It uses s6-overlay as PID 1 for process supervision, with bind mounts for credentials and workspace. It handles the TZ passthrough, the Chromium shm_size gotcha, UID remapping, etc. This is more "AI coding workstation" than "per-project Dockerfile generator," but it solves the "common dev packages, timezone, practical choices" part very well. The same author also has [HolyCode](https://github.com/CoderLuii/HolyCode) which wraps OpenCode instead of CloudCLI.

    **3. [cco (Claude Code condom)](https://github.com/nikvdp/cco)** — thinner wrapper, different goal. One command (`cco "help me code"`) with zero configuration, no IDE dependency, instant startup, automatic macOS Keychain integration for credentials, and host UID mapping. It supports both a native sandbox mode and a Docker fallback. It also sandboxes OpenAI Codex, OpenCode, and other agents. Less opinionated about dev packages — it's more about the isolation wrapper than the environment setup.

    **4. [claude-docker](https://github.com/VishalJ99/claude-docker)** — mounts your host's conda environments directly into the container so they work without Dockerfile modifications, auto-loads git config from the host, and has a modular plugin system for MCP servers. More focused on ML/conda workflows.

    **5. Anthropic's official devcontainer** — a reference `.devcontainer` setup with Dockerfile, `devcontainer.json`, and `init-firewall.sh` that implements network security with domain whitelisting, works with VS Code Dev Containers extension, and comes with Claude Code preinstalled. It's the blessed starting point but fairly minimal — you'd customize it per project. Available at [code.claude.com/docs/en/devcontainer](https://code.claude.com/docs/en/devcontainer).

    **6. Docker Sandboxes** — Docker's own approach. Run `sbx run claude` from inside your project and it launches Claude Code in an isolated sandbox with `--dangerously-skip-permissions` by default, using the `docker/sandbox-templates:claude-code` base image. Lightweight and ephemeral, but you can build custom images on top.

    ---

    **On the "AI generates the Dockerfile" angle specifically:** I didn't find a mature standalone tool that uses Claude (or any LLM) to *analyze your project and generate a custom Dockerfile*. The closest pattern is just asking Claude Code itself — `claude "generate a Dockerfile for this project with dev tools, timezone sync, and a non-root user"` — which works well in practice since it can read your `package.json`/`go.mod`/`pyproject.toml` and make reasonable choices. Docker's [compose-for-agents](https://github.com/docker/compose-for-agents) repo has some templates but it's more about agent runtimes than project-specific Dockerfile generation.

    **For your ATeam use case specifically**, ClaudeBox's profile system is probably the most relevant starting point — it already handles per-project isolation, dev tooling, and credential management. The gap is that none of these projects explicitly wire up `/remote-control` support (they all focus on `--dangerously-skip-permissions` instead), so you'd still need your `claude-auth.sh` approach for the OAuth token with the `user:sessions:claude_code` scope.

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
5. **Mount shared Claude config** if it exists. Check `<ateamorg>/claude_linux_shared/` (where `<ateamorg>` is resolved from `ateam env` output or defaults to `~/.ateamorg`). If the directory contains `.claude/`, `.claude.json`, and/or `secrets.env`, mount them:
   ```bash
   SHARED_CLAUDE="$ATEAMORG_DIR/claude_linux_shared"
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
