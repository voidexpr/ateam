# External Docker Container Support

## Context

Ateam's Docker support is accumulating orchestration complexity (extra_args, persistent mode, precheck scripts, container-extra, planned Compose) — each layer patching the previous. The root cause: ateam is trying to be a Docker orchestrator, which isn't its job.

This plan introduces `docker_container` — a way to point ateam at a user-managed container. The user handles all Docker lifecycle/config; ateam just execs agents into it.

## The two Docker stories (after this change)

1. **Simple** (`--profile docker`): ateam builds a basic image, runs agent. Zero config. Good for simple projects.
2. **External** (`docker_container = "name"`): user manages the container entirely. Ateam just `docker exec`s. Good for complex projects.

This replaces: persistent mode, container-extra, precheck scripts, and the planned Compose feature for the "complex project" use case.

---

## What the user experience looks like

### Step 1: Project Dockerfile (user's responsibility)

```dockerfile
# docker/Dockerfile.dev (or wherever the project keeps it)
FROM node:20-bookworm-slim

# --- project toolchain ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep jq make \
    postgresql postgresql-client \
    && rm -rf /var/lib/apt/lists/*

# --- agent CLIs ---
RUN npm install -g @anthropic-ai/claude-code
# RUN npm install -g @openai/codex   # if using codex

# --- project dependencies ---
COPY package.json package-lock.json ./
RUN npm install

ARG USER_UID=1000
RUN useradd -m -u $USER_UID agent || true

USER agent
WORKDIR /workspace
```

### Step 2: Start the container (user's responsibility)

```bash
docker build -t myproject-dev -f docker/Dockerfile.dev .

docker run -d --name myproject-dev \
  -v $(pwd):/workspace:rw \
  -v ~/.ateamorg:/.ateamorg:ro \
  -v /path/to/ateam-linux-amd64:/usr/local/bin/ateam:ro \
  -p 3000:3000 \
  -p 5432:5432 \
  myproject-dev \
  sleep infinity
```

Notes:
- `~/.ateamorg:/.ateamorg:ro` — gives agents access to org config, prompts, defaults
- `ateam-linux-amd64:/usr/local/bin/ateam:ro` — built via `make companion` in the ateam repo. Enables supervisor-in-docker. Optional if only running single agents.
- Ports, volumes, env vars, services — all standard Docker, nothing ateam-specific
- For multi-service stacks, use Docker Compose and point at the agent service container name

### Step 3: Point ateam at it

```toml
# .ateam/config.toml
[container]
docker_container = "myproject-dev"
```

### Step 4: Run ateam

```bash
ateam secret ANTHROPIC_API_KEY    # stored in keychain, forwarded via -e
ateam report                      # execs into myproject-dev
ateam code                        # execs into myproject-dev, same container
```

What ateam actually runs:
```
docker exec -i -w /workspace \
  -e ANTHROPIC_API_KEY=sk-... \
  myproject-dev \
  claude -p --output-format stream-json --verbose --dangerously-skip-permissions \
  ...prompt...
```

That's it. No image building, no container lifecycle, no extra_args, no precheck scripts.

---

## New container type: `ExternalContainer`

### Go definition

```go
// internal/container/external.go

// ExternalContainer runs agent commands inside a user-managed Docker container
// via `docker exec`. Ateam does not manage the container lifecycle — the user
// is responsible for creating, starting, and stopping it.
type ExternalContainer struct {
    ContainerName string            // docker container name or ID
    ForwardEnv    []string          // env var names to forward from host via -e
    Env           map[string]string // literal env vars to set via -e
    WorkDir       string            // working directory inside container (default /workspace)

    // For path translation (host paths → container paths)
    SourceDir string // project root on host
    MountDir  string // git root on host (if different from SourceDir)
    OrgDir    string // .ateamorg/ on host
}

const defaultExternalWorkDir = "/workspace"

func (e *ExternalContainer) Type() string { return "docker-exec" }

func (e *ExternalContainer) workDir() string {
    if e.WorkDir != "" {
        return e.WorkDir
    }
    return defaultExternalWorkDir
}

// EnsureRunning checks the container exists and is running.
// Returns a clear error message if not.
func (e *ExternalContainer) EnsureRunning(ctx context.Context) error {
    cmd := exec.CommandContext(ctx, "docker", "inspect",
        "--format", "{{.State.Running}}", e.ContainerName)
    var out bytes.Buffer
    cmd.Stdout = &out
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("container %q not found — start it before running ateam", e.ContainerName)
    }
    if strings.TrimSpace(out.String()) != "true" {
        return fmt.Errorf("container %q exists but is not running — start it with: docker start %s",
            e.ContainerName, e.ContainerName)
    }
    return nil
}

// ValidateAgent checks the agent CLI is available inside the container.
func (e *ExternalContainer) ValidateAgent(ctx context.Context, agentCmd string) error {
    cmd := exec.CommandContext(ctx, "docker", "exec", e.ContainerName, "which", agentCmd)
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("%q not found in container %q — install it in your Dockerfile",
            agentCmd, e.ContainerName)
    }
    return nil
}

// CmdFactory returns a function that wraps commands in docker exec.
func (e *ExternalContainer) CmdFactory() CmdFactory {
    return func(ctx context.Context, name string, args ...string) *exec.Cmd {
        dockerArgs := []string{"exec", "-i", "-w", e.workDir()}

        for _, key := range e.ForwardEnv {
            if val, ok := os.LookupEnv(key); ok {
                dockerArgs = append(dockerArgs, "-e", key+"="+val)
            }
        }
        dockerArgs = append(dockerArgs, e.envArgs()...)
        dockerArgs = append(dockerArgs, e.ContainerName, name)
        dockerArgs = append(dockerArgs, args...)

        cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
        cmd.Env = os.Environ()
        return cmd
    }
}

// TranslatePath maps host paths to container paths.
func (e *ExternalContainer) TranslatePath(hostPath string) string {
    // Same logic as DockerContainer.TranslatePath
    // OrgDir → /.ateamorg/...
    // MountDir/SourceDir → /workspace/...
}

// Run implements the Container interface.
func (e *ExternalContainer) Run(ctx context.Context, opts RunOpts) error {
    factory := e.CmdFactory()
    cmd := factory(ctx, opts.Command, opts.Args...)
    cmd.Stdin = opts.Stdin
    cmd.Stdout = opts.Stdout
    cmd.Stderr = opts.Stderr
    return cmd.Run()
}

func (e *ExternalContainer) DebugCommand(opts RunOpts) string {
    parts := []string{"docker", "exec", "-i", "-w", e.workDir()}
    for _, key := range e.ForwardEnv {
        parts = append(parts, "-e", key+"=...")
    }
    parts = append(parts, e.envArgs()...)
    parts = append(parts, e.ContainerName, opts.Command)
    parts = append(parts, opts.Args...)
    return strings.Join(parts, " ")
}
```

~100 lines. No image building, no lifecycle management, no Dockerfile resolution.

### Config (config.toml)

```go
// In internal/config/config.go, add to Config:
type ContainerConfig struct {
    DockerContainer string `toml:"docker_container"`
    Workspace       string `toml:"workspace"` // default /workspace
}
```

```toml
# .ateam/config.toml
[container]
docker_container = "myproject-dev"
workspace = "/workspace"          # optional, this is the default
```

### HCL definitions (defaults/runtime.hcl)

The container type in HCL defines the behavior (docker-exec) and env forwarding.
The actual container name is project-specific, so it comes from config.toml.

```hcl
# --- Container ---

container "docker-exec" {
  type        = "docker-exec"
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
  ]
}

# --- Agents ---

# claude-docker already exists:
agent "claude-docker" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose",
             "--dangerously-skip-permissions"]
  env = { CLAUDECODE = "" }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

# New: codex variant for containers
agent "codex-docker" {
  type    = "codex"
  command = "codex"
  args    = ["--sandbox", "workspace-write", "--ask-for-approval", "never"]
  required_env = ["OPENAI_API_KEY"]

  pricing {
    default_model = "gpt-5.3-codex"
    model "gpt-5.3-codex" {
      input_per_mtok  = 1.75
      output_per_mtok = 14.00
    }
  }
}

# --- Profiles (one per agent) ---

profile "docker-exec" {
  agent     = "claude-docker"
  container = "docker-exec"
}

profile "docker-exec-codex" {
  agent     = "codex-docker"
  container = "docker-exec"
}
```

Usage:
```bash
ateam code --profile docker-exec          # claude inside external container
ateam code --profile docker-exec-codex    # codex inside external container
```

Or set as default in config.toml:
```toml
[supervisor]
code_profile = "docker-exec"

[container]
docker_container = "myproject-dev"
```

### How container name flows through

1. HCL `container "docker-exec"` defines type + forward_env
2. config.toml `[container] docker_container = "name"` provides the name
3. `buildContainer()` creates ExternalContainer with both:
   - Container name from config.toml
   - ForwardEnv from HCL container definition

```go
// In buildContainer(), new case:
case "docker-exec":
    containerName := ""  // filled from config.toml below
    return &container.ExternalContainer{
        ContainerName: containerName,
        ForwardEnv:    cc.ForwardEnv,
        WorkDir:       "",  // filled from config.toml below
        SourceDir:     sourceDir,
        MountDir:      gitRepoDir,
        OrgDir:        orgDir,
    }, nil
```

Then in `newRunner()`, after buildContainer:
```go
// Fill ExternalContainer fields from config.toml
if ec, ok := ct.(*container.ExternalContainer); ok && env.Config != nil {
    cc := env.Config.Container
    if cc.DockerContainer == "" {
        return nil, fmt.Errorf("profile %q uses docker-exec container but [container] docker_container is not set in config.toml", profileName)
    }
    ec.ContainerName = cc.DockerContainer
    if cc.Workspace != "" {
        ec.WorkDir = cc.Workspace
    }
    // Merge env from container-extra (if any)
    ce := env.Config.ContainerExtra
    ec.ForwardEnv = append(ec.ForwardEnv, ce.ForwardEnv...)
    if len(ce.Env) > 0 {
        if ec.Env == nil {
            ec.Env = make(map[string]string, len(ce.Env))
        }
        for k, v := range ce.Env {
            ec.Env[k] = v
        }
    }
}
```

### Runner integration (runner.go)

Add a new block after the existing Docker/devcontainer/sandbox blocks:

```go
if ec, ok := r.Container.(*container.ExternalContainer); ok {
    if err := ec.EnsureRunning(ctx); err != nil {
        return failEarly(err)
    }
    agentCmd, _ := r.Agent.DebugCommandArgs(nil)
    if err := ec.ValidateAgent(ctx, agentCmd); err != nil {
        return failEarly(err)
    }
    req.CmdFactory = ec.CmdFactory()
    for i, a := range req.ExtraArgs {
        if a == "--settings" && i+1 < len(req.ExtraArgs) {
            req.ExtraArgs[i+1] = ec.TranslatePath(req.ExtraArgs[i+1])
        }
    }
    req.WorkDir = ec.TranslatePath(cwd)
}
```

### buildContainer (cmd/table.go)

In `newRunner()`, after resolving the profile, check config.toml:

```go
// If docker_container is set in config.toml, override container with ExternalContainer
if env.Config != nil && env.Config.Container.DockerContainer != "" {
    cc := env.Config.Container
    ct = &container.ExternalContainer{
        ContainerName: cc.DockerContainer,
        WorkDir:       cc.Workspace,
        ForwardEnv:    []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
        SourceDir:     env.SourceDir,
        MountDir:      env.GitRepoDir,
        OrgDir:        env.OrgDir,
    }
}
```

---

## About keeping .ateam/Dockerfile for basic mode

The basic `--profile docker` currently:
1. Uses embedded Dockerfile (node:20, basic tools, claude code)
2. Auto-generates .ateam/Dockerfile with toolchain detection when `--docker-auto-setup` flag is passed
3. But the flag is opt-in and buried — most users won't know about it

**Problem**: a Go/Rust/Java project tries `--profile docker` → build succeeds but agent can't compile → confusing failure.

**Recommendation**: add explicit `ateam docker setup` subcommand that:
1. Detects toolchains (existing `DetectToolchains()`)
2. Generates .ateam/Dockerfile (existing `AutoSetupDockerfile()`)
3. Prints what it detected and what to do next
4. Clear output: "Generated .ateam/Dockerfile with go 1.25. Run `ateam report --profile docker` to test."

This replaces the hidden `--docker-auto-setup` flag with an explicit, discoverable command. The auto-setup flag can be deprecated.

Keep the basic `--profile docker` with the embedded Dockerfile — it works for Node/Python projects out of the box. For others, `ateam docker setup` is the on-ramp.

For complex projects, the guidance becomes: skip `--profile docker` entirely, manage your own container, set `docker_container`.

---

## Migration path

| Current feature | Status | Replacement |
|-----------------|--------|-------------|
| `--profile docker` (oneshot) | **Keep** | Still the zero-config option |
| `--docker-auto-setup` flag | **Deprecate** | `ateam docker setup` command |
| `--profile docker-persistent` | **Deprecate** | `docker_container` |
| `[container-extra]` in config.toml | **Deprecate** | `docker_container` |
| Precheck scripts | **Deprecate** | User handles init in their container |
| Docker Compose plan | **Drop** | User runs `docker compose up`, sets `docker_container` |
| Docker Sandbox | **Keep** | Different purpose (microVM) |
| Devcontainer | **Keep** | Different purpose (VS Code ecosystem) |

"Deprecate" = still works, no longer documented as recommended path, may be removed later.

---

## Files to create/modify

| File | Change |
|------|--------|
| `internal/container/external.go` | **New** ~100 lines — ExternalContainer type |
| `internal/config/config.go` | Add `ContainerConfig` struct (docker_container, workspace) |
| `internal/runtime/config.go` | Add "docker-exec" to ContainerConfig type validation; add HCL schema fields if needed |
| `internal/runner/runner.go` | Add ExternalContainer block (~15 lines) |
| `cmd/table.go` | Add "docker-exec" case in buildContainer, fill from config.toml in newRunner |
| `cmd/docker.go` | **New** `ateam docker setup` subcommand |
| `defaults/runtime.hcl` | Add `codex-docker` agent, `docker-exec` container, `docker-exec` + `docker-exec-codex` profiles |
| `SANDBOX_DOCKER.md` | Rewrite to lead with external container approach |

## Verification

1. Build: `make build`
2. Unit tests: `make test`
3. Manual test: start a container manually, set `docker_container`, run `ateam run "echo hello" --profile docker`
4. Verify `ateam docker setup` generates correct Dockerfile for a Go project
5. Docker integration: `make test-docker`
