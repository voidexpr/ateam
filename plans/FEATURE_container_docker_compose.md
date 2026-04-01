# Feature: Docker Compose container type

## Problem

Projects with multi-service stacks (app + postgres + redis + nginx) need service orchestration that goes beyond what a single Docker container can provide. Today, ateam's Docker container type runs a single container — users can add volumes and ports via `extra_args`, but cannot define companion services, shared networks, or compose-managed volumes.

Docker Compose is the standard tool for multi-service development environments. Many projects already have `docker-compose.yml` files. Integrating Compose as a first-class container type lets ateam leverage this existing ecosystem.

## Design

### New container type: "compose"

```hcl
container "compose" {
    type         = "compose"
    compose_file = "docker-compose.yml"   # relative to .ateam/
    service      = "agent"                # which service runs the agent
    forward_env  = ["ANTHROPIC_API_KEY"]
}

profile "compose" {
    agent     = "claude-docker"
    container = "compose"
}
```

### Lifecycle mapping

The Compose container implements the same `Container` interface as Docker, with different underlying commands:

| Interface method | Compose implementation |
|---|---|
| `EnsureImage()` | `docker compose -f <file> build` |
| `EnsureRunning()` | `docker compose -f <file> up -d` |
| `CmdFactory()` | Returns `docker compose -f <file> exec <service> <cmd>` |
| `IsRunning()` | `docker compose -f <file> ps --format json` — check service status |
| `Stop()` | `docker compose -f <file> down` |
| `TranslatePath()` | Parse volume mounts from compose file for the agent service |
| `DebugCommand()` | `docker compose -f <file> exec <service> <cmd>` as string |

### Why a new type, not extending Docker

- **Different lifecycle**: Compose manages multiple containers, networks, and volumes as a unit. `docker compose up -d` starts the entire stack, not just one container.
- **Named volumes are compose-managed**: No need for `${CONTAINER}-pgdata` prefixing — compose namespaces volumes per project automatically.
- **Service discovery**: Containers find each other by service name (e.g., `DB_HOST=postgres`). No `--network` flags needed.
- **Port forwarding is in the compose file**: Not in `extra_args`.
- **The compose file IS the configuration**: Users define everything in `docker-compose.yml`, not in HCL/TOML.

### Example compose file

```yaml
# .ateam/docker-compose.yml
services:
  agent:
    build:
      context: ..
      dockerfile: .ateam/Dockerfile
    volumes:
      - ..:/workspace:ro
      - ../.ateam:/workspace/.ateam:rw
      - node-modules:/workspace/node_modules
    working_dir: /workspace
    environment:
      DB_HOST: postgres
      DB_PORT: "5432"
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
    environment:
      POSTGRES_PASSWORD: devpass
      POSTGRES_DB: myapp
    healthcheck:
      test: pg_isready -U postgres
      interval: 5s
      timeout: 3s
      retries: 5

volumes:
  pgdata:
  node-modules:
```

### Interaction with existing features

- **Precheck hook**: Still useful for compose — runs inside the agent service via `docker compose exec`. Can verify DB connectivity, run migrations, etc.
- **ForwardEnv**: Applied to `docker compose exec` via `-e` flags (same as persistent Docker).
- **config.toml `[container-extra]`**: `extra_args` would apply to `docker compose exec` (the agent command). `env` and `forward_env` also apply.
- **HostCLIPath**: Can be added as a volume mount in the compose file for the agent service.
- **SourceWritable**: Controlled in the compose file via `:ro`/`:rw` on volumes.

### What compose handles that Docker+extra_args cannot

1. **Service health checks with dependencies**: `depends_on` with `condition: service_healthy` ensures postgres is ready before the agent starts.
2. **Shared networking**: Services communicate by name (`postgres:5432`) without host port mapping.
3. **Volume lifecycle**: `docker compose down -v` cleans up all named volumes. `docker compose down` preserves them.
4. **Multi-service logs**: `docker compose logs` shows all services.
5. **Reproducible environment**: The compose file captures the full stack definition.

## Implementation sketch

### New file: `internal/container/compose.go`

```go
type ComposeContainer struct {
    ComposeFile   string   // absolute path to docker-compose.yml
    Service       string   // agent service name
    ForwardEnv    []string // env vars to forward
    Env           map[string]string // literal env vars
    ProjectName   string   // compose project name (for isolation)
    PrecheckScript string
}

func (c *ComposeContainer) Type() string { return "compose" }
```

### Config additions

```go
// In ContainerConfig (internal/runtime/config.go)
ComposeFile string // compose: path to docker-compose.yml, relative to .ateam/
Service     string // compose: which service runs the agent

// In hclContainer
ComposeFile string `hcl:"compose_file,optional"`
Service     string `hcl:"service,optional"`
```

### buildContainer() case

```go
case "compose":
    composeFile := cc.ComposeFile
    if composeFile == "" {
        composeFile = "docker-compose.yml"
    }
    if !filepath.IsAbs(composeFile) {
        composeFile = filepath.Join(projectDir, composeFile)
    }
    return &container.ComposeContainer{
        ComposeFile: composeFile,
        Service:     cc.Service,
        ForwardEnv:  cc.ForwardEnv,
        ProjectName: buildContainerName(sourceDir, orgDir, roleID),
    }, nil
```

## When to use compose vs Docker+extra_args

| Scenario | Recommendation |
|---|---|
| Single container, no services | Docker (default) |
| Single container + persistent volumes + ports | Docker + `[container-extra]` |
| Multiple services (DB, cache, etc.) | Compose |
| Project already has docker-compose.yml | Compose |
| Need service health checks before agent | Compose (healthcheck + depends_on) |
| CI/CD pipeline | Docker (simpler, fewer dependencies) |

## Open questions

1. **Compose file location**: `.ateam/docker-compose.yml` (convention) or configurable? Probably both — convention with HCL override.
2. **Compose project name**: Use the same `buildContainerName()` for isolation? Or let compose auto-generate from directory name?
3. **docker compose vs docker-compose**: The modern `docker compose` (plugin) vs legacy `docker-compose` (standalone). Should we detect and support both?
4. **Volume path resolution**: Should ateam resolve relative paths in compose volumes, or leave that to compose itself?
5. **TranslatePath complexity**: Parsing compose volume mounts for path translation is non-trivial. Could skip this and require the compose file to use `/workspace` convention explicitly.
