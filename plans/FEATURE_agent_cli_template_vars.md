# Feature: Template Variables for Agent and Container CLI Args

## Problem

Agent args in `runtime.hcl` support `{{VAR}}` template resolution (e.g., `--name "{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}"`). Container args, env values, volumes, and other config fields do not. Extending templates to container-level fields enables naming Docker containers, setting hostnames, labeling runs, and customizing volumes per-role — all from HCL config without Go code changes.

## Current State

### Template resolution today

Only two things are resolved:
- `Agent.Args` (base args from `runtime.hcl`)
- `Runner.ExtraArgs` (profile's `agent_extra_args`)

Resolution happens in `Runner.Run()` (runner.go:197-200), after all 12 vars are known.

### When each variable value is known

```
┌─────────────────────┬───────────────────────────────────────────────────────────┐
│ Variable            │ When known                                                │
├─────────────────────┼───────────────────────────────────────────────────────────┤
│ PROJECT_NAME        │ root.Resolve() — before everything                        │
│ PROJECT_FULL_PATH   │ root.Resolve() — before everything                        │
│ PROJECT_DIR         │ root.Resolve() — before everything                        │
│ ROLE                │ Command handler — before resolveRunner()                   │
│ ACTION              │ Command handler — before resolveRunner()                   │
│ PROFILE             │ resolveRunner() — during runner construction               │
│ AGENT               │ newRunner() → buildAgent() — during runner construction    │
│ MODEL               │ newRunner() → buildAgent(); can be overridden later        │
│ CONTAINER           │ newRunner() — during runner construction                   │
│ TASK_GROUP          │ Command handler — before Runner.Run() but after runner     │
│                     │   construction. Known before resolveRunner() for ateam     │
│                     │   code (computed) and ateam run (--task-group flag).       │
│ TIMESTAMP           │ Runner.Run() — time.Now() at execution start              │
│ EXEC_ID             │ Runner.Run() — after CallDB.InsertCall()                  │
└─────────────────────┴───────────────────────────────────────────────────────────┘
```

### Code sequence

```
1. Command handler (cmd/report.go, cmd/run.go, cmd/code.go, ...)
   │
   ├── root.Resolve()
   │   → PROJECT_NAME, PROJECT_DIR, PROJECT_FULL_PATH
   │
   ├── roleID, action from CLI flags / hardcoded
   │   → ROLE, ACTION
   │
   ├── taskGroup computed or from CLI flag
   │   → TASK_GROUP  (known here but not threaded to buildContainer)
   │
   ├── resolveRunner(env, profileFlag, agentFlag, action, roleID)
   │   │
   │   └── newRunner(env, profileName, roleID)
   │       │   → PROFILE, AGENT, MODEL, CONTAINER
   │       │
   │       ├── runnerFromAgentConfig(env, ac)
   │       │   └── buildAgent(ac) → Agent{Args: ["--name", "{{PROJECT_DIR}}-{{ROLE}}"]}
   │       │       AGENT ARGS STORED — raw templates, NOT yet resolved
   │       │
   │       ├── r.ExtraArgs += prof.AgentExtraArgs
   │       │   AGENT EXTRA ARGS STORED — raw templates, NOT yet resolved
   │       │
   │       ├── buildContainer(cc, prof, sourceDir, projectDir, orgDir, ...)
   │       │   ├── extraArgs = prof.ContainerExtraArgs  ← CONTAINER ARGS SET
   │       │   ├── containerName = buildContainerName() ← CONTAINER NAME SET
   │       │   ├── volumes = resolveVolumePath(...)     ← VOLUMES SET
   │       │   └── DockerContainer{ExtraArgs, ContainerName, ExtraVolumes, ...}
   │       │       CONTAINER FIELDS SET — currently no templates, NOT resolved
   │       │
   │       └── merge config.toml [container-extra] into DockerContainer
   │           dc.ExtraArgs += ce.ExtraArgs
   │           dc.Env merged with ce.Env
   │
   ├── post-construction: r.CallDB = db, setSourceWritable, etc.
   │
   └── r.Run(ctx, prompt, RunOpts{RoleID, Action, TaskGroup, ...}, progress)

2. Runner.Run()
   │   → TIMESTAMP (time.Now())
   │
   ├── InsertCall → callID
   │   → EXEC_ID
   │
   ├── BuildTemplateVars() → all 12 vars
   ├── ResolveTemplateArgs(extraArgs)      ← AGENT EXTRA ARGS RESOLVED
   ├── resolveAgentTemplateArgs(r.Agent)   ← AGENT BASE ARGS RESOLVED
   │
   ├── Docker setup
   │   ├── dc.EnsureImage()
   │   ├── dc.EnsureRunning()              ← CONTAINER CLI BUILT from dc.ExtraArgs etc.
   │   │   docker run --name X -v ... -w ... <dc.ExtraArgs> <image> sleep infinity
   │   │   CONTAINER ARGS USED HERE — NOT template-resolved
   │   ├── dc.RunPrecheck()
   │   └── req.CmdFactory = dc.CmdFactory()
   │
   ├── agent.Run(ctx, req)
   │   └── exec.CommandContext(command, agent.Args + req.ExtraArgs)
   │       AGENT CLI BUILT: claude -p --name myproject-security-report ...
   │       AGENT ARGS RESOLVED ✓
   │
   └── return summary
```

## Proposal: Move TIMESTAMP and TASK_GROUP earlier

### TIMESTAMP

Currently `time.Now()` at the start of `Runner.Run()`. Nothing prevents computing it earlier. Add a `StartTimestamp` field to `RunOpts`:

```go
type RunOpts struct {
    // ...existing fields...
    StartTimestamp time.Time // if zero, defaults to time.Now() in Run()
}
```

Each command handler sets it before calling `Run()`:

```go
startedAt := time.Now()
opts := runner.RunOpts{
    StartTimestamp: startedAt,
    TaskGroup:      "code-" + startedAt.Format(runner.TimestampFormat),
    // ...
}
```

In `Runner.Run()`, use `opts.StartTimestamp` if set, else `time.Now()`:

```go
startedAt := opts.StartTimestamp
if startedAt.IsZero() {
    startedAt = time.Now()
}
```

This makes TIMESTAMP available at the command handler level — before `resolveRunner()`.

### TASK_GROUP

Already known at the command handler level:
- `ateam code`: `"code-" + timestamp` (computed in `runCode()`)
- `ateam run`: `--task-group` CLI flag
- `ateam report/review`: empty (no grouping)

Currently passed via `RunOpts.TaskGroup`, which is only available at `Runner.Run()` time. To make it available earlier, thread it through `resolveRunner()` → `newRunner()` → `buildContainer()`.

### After moving both earlier

**Available at runner/container construction time (10 of 12):**
PROJECT_NAME, PROJECT_FULL_PATH, PROJECT_DIR, ROLE, ACTION, PROFILE, AGENT, MODEL, CONTAINER, TIMESTAMP, TASK_GROUP

**Only available at `Runner.Run()` time (1 of 12):**
EXEC_ID

### Making EXEC_ID available to containers

EXEC_ID comes from `CallDB.InsertCall()` which returns the auto-increment row ID. Options:

**Option A: Pre-allocate the DB row**

Move `InsertCall` to before container setup in `Runner.Run()`. The call record is inserted with the same data as today (role, action, model, prompt hash, etc.), we get the ID, then resolve container templates, then proceed with Docker setup. If container setup fails later, mark the record as failed.

This already nearly works — InsertCall was moved early for agent template resolution. The remaining step is to resolve container fields between InsertCall and `dc.EnsureRunning()`.

```go
// In Runner.Run(), after InsertCall (already early):
tmplVars := BuildTemplateVars(...)

// Resolve agent args (already done)
extraArgs = ResolveTemplateArgs(extraArgs, tmplVars)
resolveAgentTemplateArgs(r.Agent, tmplVars)

// NEW: resolve container fields before Docker setup
if dc, ok := r.Container.(*container.DockerContainer); ok {
    dc.ExtraArgs = ResolveTemplateArgs(dc.ExtraArgs, tmplVars)
    dc.ContainerName = resolveTemplateString(dc.ContainerName, tmplVars)
    // ... other fields
}
```

**Tradeoff**: Clean — all 12 vars available. But Runner.Run() reaches into container internals (field mutation). Could be cleaned up with a `ResolveTemplates(vars)` method on the Container interface.

**Option B: Container interface method**

Add a `ResolveTemplates` method to the Container interface:

```go
type Container interface {
    Type() string
    ResolveTemplates(vars map[string]string)  // resolve {{VAR}} in config fields
}
```

Each container type implements it to resolve its own fields. Runner calls it before `EnsureImage`/`EnsureRunning`. No field mutation from outside.

**Option C: Accept the limitation**

EXEC_ID only available for agent args. Container args get the other 11 vars (resolved at construction time). EXEC_ID in a container name is a niche use case.

### Recommendation

Option A is simplest and gives all 12 vars everywhere. The field mutation concern is minimal — it's the same pattern as `resolveAgentTemplateArgs` which already mutates `Agent.Args`. If we later want cleaner boundaries, upgrade to Option B.

## Fields to template-resolve

### High value

| Field | Config source | Current value | Template use case |
|---|---|---|---|
| `DockerContainer.ExtraArgs` | `prof.ContainerExtraArgs` + `ce.ExtraArgs` | Static strings | `["--hostname", "{{PROJECT_DIR}}-{{ROLE}}"]` |
| `DockerContainer.ContainerName` | `buildContainerName()` hardcoded | `ateam-<projectID>-<roleID>` | `"ateam-{{PROJECT_DIR}}-{{ROLE}}"` (make configurable) |
| `DockerContainer.Env` values | `ce.Env` from config.toml | Static strings | `{"ATEAM_SESSION": "{{PROJECT_DIR}}-{{EXEC_ID}}"}` |
| `AgentConfig.Env` values | runtime.hcl `env = {...}` | Static strings | `{"ATEAM_ROLE": "{{ROLE}}"}` |
| `AgentConfig.ConfigDir` | runtime.hcl `config_dir` | Static string | `".claude-{{ROLE}}"` per-role config |

### Medium value

| Field | Template use case |
|---|---|
| `ContainerConfig.ExtraVolumes` | `["{{PROJECT_FULL_PATH}}/data:/data:ro"]` |
| `DockerContainer.Image` | `"ateam-{{PROJECT_DIR}}:latest"` (currently hardcoded in Go) |

### Low value (already handled by other mechanisms)

| Field | Why not worth it |
|---|---|
| `PrecheckScript` | Already has role-based resolution chain |
| `Dockerfile` | Already has role-based resolution chain |
| `Sandbox` (JSON) | Has its own merge mechanism |
| `RWPaths/ROPaths/DeniedPaths` | Dynamically merged at runtime |

## Implementation plan

### Phase 1: Move TIMESTAMP and TASK_GROUP earlier

1. Add `StartTimestamp time.Time` to `RunOpts`
2. Each command handler computes timestamp before `resolveRunner()`
3. Thread `taskGroup` and `timestamp` through `resolveRunner()` → `newRunner()` → `buildContainer()`
4. Build a "partial" TemplateVars (11 of 12) at container construction time
5. Resolve container fields with partial vars in `buildContainer()`

### Phase 2: Resolve container fields

1. Resolve `dc.ExtraArgs` with template vars at construction time (in `buildContainer`)
2. Resolve `dc.Env` values similarly
3. Make `ContainerName` configurable via HCL (new `name` field on container config) with template default
4. Resolve `ConfigDir` with template vars in `runnerFromAgentConfig`
5. Resolve agent `Env` values

### Phase 3: Late resolution for EXEC_ID (Option A)

1. In `Runner.Run()`, after InsertCall, resolve any remaining `{{EXEC_ID}}` in container fields
2. This is a second pass — most vars already resolved in Phase 2, only EXEC_ID is new

### Files to modify

- `internal/runner/runner.go` — RunOpts.StartTimestamp, late container resolution
- `internal/runner/template.go` — add `ResolveTemplateString` helper, partial vars builder
- `cmd/table.go` — thread timestamp/taskGroup to buildContainer, resolve container fields
- `cmd/code.go`, `cmd/run.go`, `cmd/report.go`, `cmd/review.go` — set StartTimestamp
- `internal/runtime/config.go` — optional: add `Name` field to ContainerConfig
- `internal/container/docker.go` — no changes needed (fields are mutated before use)

## Verification

- `make build && make test`
- Test with template args in runtime.hcl agent args (existing)
- Test with template args in profile container_extra_args (new)
- Verify container name includes resolved role via `docker ps`
- Verify `ateam cat` on a stream file from a templated run shows correct session name
