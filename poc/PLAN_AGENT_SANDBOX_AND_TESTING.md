# Plan: Pluggable Agents, Sandboxes, and Testing

## Context

The current system hardcodes `claude` as the execution engine and uses claude's native sandbox settings JSON for isolation. We need:

1. **Pluggable agents**: claude, codex, mock (for testing), and future additions
2. **Pluggable sandboxes**: none, claude-native, docker (and eventually MacOS native containers)
3. **A testing strategy** that leverages this pluggability for fast, cheap, deterministic validation

## Architecture

### Core insight

The current `ClaudeRunner.Run()` does three entangled things:
1. **Orchestrate**: logging, file I/O, archiving, history, progress reporting
2. **Configure sandbox**: resolve settings JSON, merge write paths
3. **Execute agent**: build CLI args, spawn process, parse stream

These need to be separated. The sandbox config feeds into the agent execution, not the other way around.

### Interfaces

```go
// Agent executes a prompt and produces a result.
type Agent interface {
    Name() string  // "claude", "codex", "mock"
    Run(ctx context.Context, req AgentRequest) AgentResult
}

type AgentRequest struct {
    Prompt     string
    WorkDir    string
    StreamFile string    // agent writes raw stream here
    StderrFile string    // agent writes stderr here
    Sandbox    SandboxRules
    ExtraArgs  []string
}

type SandboxRules struct {
    AllowWriteDirs []string
    DenyWriteDirs  []string
    AllowReadDirs  []string
    // Agent implementations translate this to their native format
    // Claude: settings JSON. Docker: mount flags. Mock: records for assertion.
}

type AgentResult struct {
    Output       string
    ExitCode     int
    Err          error
    Cost         float64
    InputTokens  int
    OutputTokens int
    Turns        int
    Duration     time.Duration
}
```

Key: `SandboxRules` is agent-agnostic. Each `Agent` implementation knows how to enforce them:

| Agent | How sandbox is enforced |
|-------|------------------------|
| `ClaudeAgent` | Translates `SandboxRules` → claude settings JSON, passes via `--settings` |
| `CodexAgent` | Translates `SandboxRules` → codex's equivalent mechanism |
| `DockerAgent` | Wraps another agent, translates `SandboxRules` → `docker run` volume mounts with `:ro`/`:rw` |
| `MockAgent` | Records the rules for test assertion, produces canned output |

`DockerAgent` is a decorator — it wraps any other agent and runs it inside a container. So you get combinations: `DockerAgent(ClaudeAgent)`, `DockerAgent(CodexAgent)`, or just `ClaudeAgent` (using claude's native sandbox).

### The Runner becomes the orchestrator

```go
// Runner orchestrates execution: logging, file I/O, archiving.
// It delegates the actual LLM call to an Agent.
type Runner struct {
    Agent      Agent
    LogFile    string
    ProjectDir string
    OrgDir     string
}
```

`Runner.Run()` keeps all the current orchestration logic (stream file creation, exec file, history archive, runner.log, error file, progress channel) but delegates the actual execution to `r.Agent.Run(req)`. The stream parsing moves into each agent implementation (claude and codex have different output formats) or stays shared if they use the same format.

## Testing Layers

### Layer 1: Mock agent, no sandbox — plumbing tests

**What**: `MockAgent` implements `Agent`. Returns canned JSONL. Tests ALL orchestration logic.

**How**: `MockAgent` is a Go struct (not a shell script — keeps everything in-process, easier to control):

```go
type MockAgent struct {
    Response string   // markdown to return
    Cost     float64
    Err      error    // simulate failures
    Requests []AgentRequest  // records what was called
}
```

It writes a valid JSONL stream to `req.StreamFile` and returns. Tests can inspect `Requests` to verify sandbox rules were computed correctly.

**Tests** (`runner/orchestration_test.go`):
- Single role report: prompt assembled → agent called → output file written → history archived → runner.log entry
- Multi-role parallel: pool runs N roles, all files created correctly
- Full pipeline: report → produces `full_report.md` → review reads it → produces `review.md` → code reads review → produces output
- Error paths: agent returns error → error file written, history still archived, runner.log has error entry
- Timeout: context cancelled → proper cleanup

**Speed**: <1s, $0, runs on every `go test`.

### Layer 2: Mock agent, sandbox rule verification — unit tests

**What**: Verify that `SandboxRules` are computed correctly from the project/org/role configuration.

The sandbox rule computation is pure logic: given a project dir, org dir, work dir, and role ID, produce the correct `SandboxRules`. This is testable without any agent.

**Tests** (`runner/sandbox_test.go`):
```go
func TestSandboxRules_RoleReport(t *testing.T) {
    rules := ComputeSandboxRules(projectDir, orgDir, workDir, "security", ActionReport)
    // Work dir is writable
    assert(rules.AllowWriteDirs, contains(workDir))
    // Project dir is writable (for report output)
    assert(rules.AllowWriteDirs, contains(projectDir))
    // Org state dir is writable (for logs)
    assert(rules.AllowWriteDirs, contains(stateDir))
    // Settings file itself is NOT writable
    assert(rules.DenyWriteDirs, contains(settingsPath))
}
```

**Also test with MockAgent**: Since `MockAgent.Requests` records the `SandboxRules` it received, you can run the full pipeline and assert what rules were passed for each phase:
- Report phase: role can write to its own report dir, not other roles' dirs
- Review phase: supervisor can read all reports, write to supervisor dir
- Code phase: supervisor can write to source dir

**Speed**: <1s, $0.

### Layer 3: Real agent smoke test — validates invocation works

**What**: Verify that `ClaudeAgent` (and eventually `CodexAgent`) can actually be called and produce output. Minimal prompt, cheapest model, single role.

**Gated by**: `ATEAM_SMOKE=1` env var or `//go:build smoke` build tag.

**How**:
- Tiny test project (1 Go file with an obvious issue)
- 1 role (`security`), cheapest model, 1 minute timeout
- Extra prompt: "Respond in under 50 words with exactly 2 bullet points."
- Assert: output file exists, is non-empty, cost > 0, no error
- Optionally: run review on the single report, assert review file exists

**Speed**: ~30s, ~$0.03. Run manually or nightly.

### Layer 4: Real sandbox enforcement — validates rules actually work

This is the most interesting one. The question is: "does the agent actually respect the sandbox rules?" This needs a real agent running inside real constraints.

**Approach: dedicated sandbox test role**

Create a built-in test role (`_sandbox_test`, prefixed with `_` so it never appears in `ateam roles`) with a prompt like:

```markdown
# Sandbox Verification Test

You are testing sandbox file access rules. Execute EXACTLY these steps:

1. Try to write "ALLOWED" to {{ALLOWED_PATH}}/sandbox_test.txt
2. Try to write "DENIED" to {{DENIED_PATH}}/sandbox_test.txt
3. Try to read {{READABLE_PATH}}/readable_test.txt
4. Report the result of each operation as: PATH: SUCCESS or PATH: DENIED

Output ONLY the results, one per line.
```

The test harness:
1. Creates temp dirs for allowed/denied/readable paths
2. Puts a file in readable path
3. Computes `SandboxRules` allowing write to `allowed`, denying write to `denied`
4. Runs the agent with these rules
5. Parses the output and asserts: allowed write succeeded, denied write failed, read succeeded

**For claude native sandbox**: Tests that `--settings` JSON enforcement works. This verifies ateam's settings generation is correct.

**For docker sandbox**: Tests that volume mounts with `:ro`/`:rw` work. The `DockerAgent` mounts `allowed` as `:rw` and `denied` as `:ro` (or not at all).

```go
func TestSandboxEnforcement(t *testing.T) {
    agents := []Agent{
        NewClaudeAgent(ClaudeOpts{Model: "haiku"}),
        // NewDockerAgent(NewClaudeAgent(...), DockerOpts{Image: "ateam-sandbox"}),
    }
    for _, agent := range agents {
        t.Run(agent.Name(), func(t *testing.T) {
            allowed := t.TempDir()
            denied := t.TempDir()
            // ... run sandbox test role, verify results
        })
    }
}
```

**Gated by**: `ATEAM_SANDBOX_TEST=1` (costs money, needs claude/docker).

**Speed**: ~1min per agent, ~$0.05. Run manually or weekly.

### Layer 5: Docker integration — full pipeline in container

**What**: Run the entire `report → review → code` pipeline inside Docker to validate:
- Container setup works
- Volume mounts are correct
- The agent can run `ateam` commands from inside (for the code phase supervisor)
- Source code is accessible, output dirs are writable

**How**: A `Dockerfile.test` that:
1. Installs Go, claude CLI (or mock), ateam binary
2. Copies a test project
3. Runs `ateam init && ateam report --roles security && ateam review`

For CI, use the mock agent inside Docker (tests container plumbing without API cost). For periodic validation, use real claude.

**Gated by**: `ATEAM_DOCKER_TEST=1`.

### Testing summary

| Layer | What | Agent | Sandbox | Speed | Cost | When |
|-------|------|-------|---------|-------|------|------|
| 1. Plumbing | Orchestration, file I/O, pipeline | Mock | None | <1s | $0 | Every `go test` |
| 2. Rules | SandboxRules computation | Mock (records) | Computed only | <1s | $0 | Every `go test` |
| 3. Smoke | Real agent invocation | Claude/Codex | Native | ~30s | ~$0.03 | Manual/nightly |
| 4. Enforcement | Sandbox actually blocks | Claude/Docker | Real | ~1m | ~$0.05 | Manual/weekly |
| 5. Docker E2E | Full pipeline in container | Mock or real | Docker | ~2m | $0-0.10 | CI/weekly |

## Implementation order

1. **Agent interface + MockAgent** — refactor `ClaudeRunner.Run()` to use `Agent` interface, extract `ClaudeAgent`
2. **Layer 1 + 2 tests** — immediate value, run on every build
3. **Layer 3 smoke test** — quick win, validates the refactor didn't break real usage
4. **DockerAgent** — wraps any agent in a container
5. **Layer 4 + 5** — sandbox enforcement tests

---

## Open Design Questions

### 1. Docker sandbox lifecycle: build, start, stop, exec

Docker containers (and MacOS native containers) have a lifecycle beyond a single command:

- **Build**: `docker build` to create the image (required for MacOS native containers, optional for pre-built images)
- **Start**: `docker run` or `docker start` to bring up the container
- **Exec**: `docker exec` to run commands inside a running container
- **Stop**: `docker stop` to shut down

Two execution models:

**a) Run-per-invocation**: `docker run --rm` for each agent call. Simple, stateless, but slow startup and no persistent processes.

**b) Start-then-exec**: `docker start` once, then `docker exec` for each agent call. Faster, preserves state between calls, enables debugging (attach to running container). Required for the code phase where the supervisor calls `ateam run` multiple times.

Questions to resolve:
- Should the sandbox have explicit lifecycle commands (`ateam sandbox start/stop/status`)?
- Should the container auto-start on first use and auto-stop after idle timeout?
- How does `start+exec` interact with the code phase supervisor, which itself spawns sub-agents? Does the inner agent reuse the outer container or create a nested one?
- For debugging: `ateam sandbox shell` to get an interactive shell in the running container?

### 2. `ateam exec` command — agent/sandbox usage outside of roles

We want to use the pluggable agent+sandbox system independently of the ateam role workflow. Use cases:
- `ateam exec "fix this bug"` — one-shot prompt with configured agent+sandbox
- `ateam exec --interactive` — interactive session with agent in sandbox
- `ateam exec @prompt.md --agent codex --sandbox docker` — explicit overrides
- Debugging: run arbitrary commands inside the sandbox container

Questions to resolve:
- How does `exec` relate to `run`? Is `run` a role-aware `exec`, or is `exec` a lower-level primitive that `run` and the pipeline commands use internally?
- Does `exec` inherit project context (`.ateam/`, `.ateamorg/`) or can it work standalone?
- For interactive mode: does the agent handle interactivity, or does ateam proxy stdin/stdout to the agent process?

### 3. Configuration format: HCL for agents, sandboxes, profiles

Current config is TOML (`config.toml`) with flat sections. The agent+sandbox config needs more structure. HCL is a good fit for its block syntax.

Draft structure:

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  model   = "sonnet"
  // Agent-specific sandbox translation
  sandbox_settings_template = "claude_sandbox.json"
}

agent "codex" {
  command = "codex"
  args    = ["--full-auto"]
  model   = "codex-mini"
}

agent "mock" {
  type     = "mock"
  response = "Mock response for testing"
}

sandbox "none" {
  type = "none"
}

sandbox "claude-native" {
  type = "native"
  // Uses the agent's built-in sandbox
  settings_file = "ateam_claude_sandbox_extra_settings.json"
}

sandbox "docker" {
  type  = "docker"
  image = "ateam-sandbox:latest"
  build_context = "./docker"
  auto_build    = true
  auto_stop_after = "30m"
  // Mount rules are derived from SandboxRules at runtime
}

profile "default" {
  agent   = "claude"
  sandbox = "claude-native"
}

profile "cheap" {
  agent   = "claude"
  sandbox = "claude-native"
  agent_override {
    model = "haiku"
  }
}

profile "docker-codex" {
  agent   = "codex"
  sandbox = "docker"
}

profile "test" {
  agent   = "mock"
  sandbox = "none"
}
```

Questions to resolve:
- Does this HCL config live alongside `config.toml` (e.g. `runtime.hcl`) or replace it?
- Is HCL overkill? The block structure is nice but adds a dependency. Could we use TOML with nested tables instead?
- Should profiles be the primary selection mechanism (`ateam report --profile docker-codex`) or should agent+sandbox be selected independently (`ateam report --agent codex --sandbox docker`)?
- Where does this config file live? Project-level (`.ateam/runtime.hcl`), org-level (`.ateamorg/runtime.hcl`), or both with 2-level fallback?

### 4. Agent-specific sandbox capability differences

Different agents have fundamentally different sandboxing models:

| Capability | Claude | Codex |
|-----------|--------|-------|
| Filesystem write control | Per-directory allow/deny lists | Container-level, limited control |
| Filesystem read control | Not restrictable | Reads entire HOME by default |
| Network control | Domain allowlist | Container-level |
| Process isolation | None (runs in user space) | Container-based |
| Tool approval | Per-tool allow/deny | Full auto mode |

This means `SandboxRules` can't be a simple common denominator — some rules are unenforceable on some agents.

Questions to resolve:
- Should `SandboxRules` include a capability query (`CanRestrictReads() bool`) so the runner can warn when a rule can't be enforced?
- Should the profile config include agent-specific sandbox overrides?
- For codex's HOME-readable limitation: is this acceptable (document it), or does docker wrapping become mandatory for codex when read isolation matters?
- Should ateam validate that the chosen agent+sandbox combination can enforce the project's security requirements, or just warn?

```hcl
sandbox "strict" {
  type = "docker"
  requires_capabilities = ["restrict_reads", "restrict_writes", "restrict_network"]
  // If the agent can't provide these natively, docker wrapping is mandatory
}
```

---

## Recommendations

### 1. Docker sandbox lifecycle: start+exec with lazy lifecycle

**Recommendation: start+exec as default, with explicit lifecycle commands for control.**

The code phase is the forcing function: the supervisor calls `ateam run` and `ateam prompt` multiple times within a single `ateam code` invocation. With run-per-invocation, each inner `ateam run` would start a new container (~2-5s overhead each, plus losing any in-container state like installed packages or compiled binaries). Start+exec is the only viable model for this.

**Lifecycle model:**

```
ateam sandbox start [--profile P]    # explicit start, returns container ID
ateam sandbox stop                   # explicit stop
ateam sandbox status                 # show running containers
ateam sandbox shell                  # interactive shell for debugging
```

But most users shouldn't need these. The default flow is lazy:

1. **First agent call in a session** checks if a sandbox container is running for this project+profile
2. If not: auto-build (if needed) + auto-start
3. Agent calls use `docker exec` into the running container
4. Container auto-stops after idle timeout (configurable, default 30m)
5. `ateam sandbox stop` for immediate cleanup

The container is scoped to a **project** — identified by project ID. This means different projects get different containers (different mounts, different state).

**Inner agent calls (code phase):**

The supervisor runs on the host (or in a container). When it calls `ateam run`, the `ateam` CLI detects that a sandbox container is already running for this project and reuses it via `docker exec`. No nesting — the outer and inner calls share the same container.

Implementation:
- `Sandbox` interface has `Start(ctx) error`, `Exec(ctx, cmd []string) error`, `Stop() error`, `IsRunning() bool`
- `DockerSandbox` implements this with `docker run -d` for start, `docker exec` for exec, `docker stop` for stop
- A lock file or pid file in `.ateamorg/projects/<id>/sandbox.lock` tracks the running container ID
- `NoneSandbox` is a no-op implementation

**For MacOS native containers**: Same interface, different commands (`container run` vs `docker run`). The `Sandbox` interface abstracts this.

### 2. `ateam exec`: a lower-level primitive

**Recommendation: `exec` is the raw agent+sandbox execution primitive. `run` becomes a thin wrapper that adds role context.**

Hierarchy:

```
exec                  # raw: prompt + agent + sandbox → output
  └─ run              # role-aware: adds role prompt resolution, output file management
      └─ report       # workflow: parallel runs + archiving
      └─ review       # workflow: supervisor + all reports
      └─ code         # workflow: supervisor orchestration
```

`exec` is useful beyond ateam workflows:
- `ateam exec "explain this function" --agent claude` — quick one-off question
- `ateam exec --interactive --sandbox docker` — interactive coding session in sandbox
- `ateam exec --agent mock "test prompt"` — test without API cost

**Interface:**

```
ateam exec PROMPT_OR_@FILE [--agent NAME] [--sandbox NAME] [--profile NAME]
                           [--work-dir PATH] [--timeout MINUTES]
                           [--interactive] [--stream]
```

- Defaults to the project's default profile (from config)
- Can work without a project: `--agent` and `--sandbox` flags override, no `.ateam/` needed
- `--interactive` passes stdin/stdout directly to the agent process (no prompt, no stream parsing). For claude this means running `claude` without `-p`. For docker this means `docker exec -it`.
- Without `--interactive`, works like `run` but without role awareness

`run` then becomes:

```
ateam run PROMPT_OR_@FILE --role ROLE [--agent/--sandbox/--profile overrides]
```

Internally: resolve role config, compute sandbox rules for the role, call `exec` logic.

### 3. Configuration: HCL, org-level with project overrides

**Recommendation: Use HCL. Place the config at org level with project-level overrides. Keep `config.toml` for project-specific settings (roles, timeouts).**

**Why HCL over TOML**: The agent/sandbox config is inherently block-structured — you define named instances of agents and sandboxes, then compose them into profiles. TOML can do this with nested tables (`[agent.claude]`, `[sandbox.docker]`) but it gets awkward for nested blocks and lists. HCL was designed for exactly this pattern. The `hashicorp/hcl` Go library is mature and well-maintained.

**File locations and resolution (3-level, same pattern as other ateam configs):**

```
.ateamorg/defaults/runtime.hcl  # embedded defaults (written by ateam install)
.ateamorg/runtime.hcl           # org-level overrides (shared across projects)
.ateam/runtime.hcl              # project-level overrides (optional)
```

Resolution: project overrides org, org overrides defaults. Same-named blocks (agents, containers, profiles) at a higher level replace the lower-level definition entirely. This is NOT per-role or per-supervisor — runtime config is global to the project.

**Separation of concerns:**

- `runtime.hcl` — defines what agents, containers, and profiles are available (the "what can run")
- `config.toml` — defines which profiles are used for which actions/roles, plus timeouts, parallelism (the "what to use when")

They don't overlap. `config.toml` stays simple TOML for operational config. `runtime.hcl` handles the structured agent/container/profile definitions.

**Profile as the primary selection mechanism:**

```bash
ateam report --roles all --profile cheap       # use cheap profile
ateam report --roles all --agent codex         # override just the agent
ateam exec "fix this" --profile docker-codex   # explicit profile for exec
```

Both `--profile` and `--agent`/`--sandbox` work. `--profile` selects a named combo. `--agent`/`--sandbox` override individual components. `--agent codex` without `--sandbox` uses the default sandbox for that agent (from config).

**Embedded defaults:**

```hcl
agent "claude" {
  command      = "claude"
  args         = ["-p", "--output-format", "stream-json", "--verbose"]
  default_model = "sonnet"
  stream_format = "claude-stream-json"
}

agent "codex" {
  command      = "codex"
  args         = ["--full-auto", "--quiet"]
  stream_format = "codex-json"  // different parsing
}

agent "mock" {
  type = "builtin"
  // No command — handled in-process
}

sandbox "none" {
  type = "none"
}

sandbox "native" {
  type = "agent-native"
  // Delegates to the agent's own sandbox mechanism
  // Claude: settings JSON. Codex: its container.
}

sandbox "docker" {
  type          = "docker"
  image         = "ateam-sandbox:latest"
  dockerfile    = "Dockerfile.sandbox"
  build_context = "."
  auto_build    = true
  idle_timeout  = "30m"
}

profile "default" {
  agent   = "claude"
  sandbox = "native"
}

profile "cheap" {
  agent   = "claude"
  sandbox = "native"
  override_model = "haiku"
}

profile "test" {
  agent   = "mock"
  sandbox = "none"
}
```

### 4. Sandbox capabilities: document, warn, don't block

**Recommendation: Model capabilities as properties of the sandbox, not the agent. Warn on mismatch, don't block execution.**

The key insight: sandboxing has two layers that compose independently.

**Layer 1: Agent-native sandbox** — what the agent itself can enforce.
- Claude: fine-grained file write control, tool approval, network domain allowlist. No read restriction, no process isolation.
- Codex: container-based process isolation, can restrict writes. Reads entire HOME. Network is container-scoped.

**Layer 2: Container sandbox** — what docker/container runtime enforces.
- Full filesystem isolation via mounts
- Full network isolation
- Process isolation

When you combine them: `DockerSandbox(ClaudeAgent)` gives you docker's filesystem isolation + claude's tool approval. The capabilities stack.

**Modeling this:**

```go
type SandboxCapabilities struct {
    RestrictWrites  bool  // can prevent writes to specific dirs
    RestrictReads   bool  // can prevent reads from specific dirs
    RestrictNetwork bool  // can control network access
    ProcessIsolation bool // agent runs in isolated process space
}

// Each sandbox reports its capabilities
func (s *DockerSandbox) Capabilities() SandboxCapabilities {
    return SandboxCapabilities{
        RestrictWrites: true, RestrictReads: true,
        RestrictNetwork: true, ProcessIsolation: true,
    }
}

func (s *NativeSandbox) Capabilities(agent Agent) SandboxCapabilities {
    // Depends on the agent — claude native sandbox has different caps than codex native
    return agent.NativeSandboxCapabilities()
}
```

**Behavior on mismatch:**

If `SandboxRules` requests read restriction but the sandbox can't enforce it:
1. Print a warning to stderr: `Warning: sandbox "native" with agent "codex" cannot restrict reads. Use --sandbox docker for read isolation.`
2. Proceed anyway — the user chose this configuration.
3. Do NOT silently ignore the rule or block execution.

**In HCL config, capabilities are informational, not prescriptive:**

```hcl
sandbox "strict" {
  type = "docker"
  // No requires_capabilities — that's over-engineering.
  // The sandbox knows what it can do. The runner warns on gaps.
}
```

The agent-specific differences (codex reads HOME) are documented in `ateam agents --verbose` or `ateam sandbox --capabilities`, not enforced through config complexity.

**For codex specifically**: Document that `sandbox = "native"` with codex means it can read your entire HOME. If that's not acceptable, use `sandbox = "docker"`. Don't try to paper over the limitation in code.

---

## What to work on next

Additional questions to consider in our design:

* db interface to track agent calls
* how to interact with containers
* how to detect incompatible layers:
  * sandbox with codex and no RO for home should error out, we will document it in the default config

Also is there value in interacting with containers through ateam or it just adds unnecessary layers? For example we want to run claude in a container so we have a clear security model. But then for interactive work we may want to run commands in that same container (sandbox in ateam speak) with the same config to troubleshoot permission issues or just do regular dev work.

How do you suggest to combine all these features? We need to nail the sandbox interface so that it will work well with containers. Or make it very simple for only one-shot agent calls (ateam exec, ateam run, ateam report/review/code) and provide enough debug output (maybe ateam cli --sandbox FOO to output raw container commands to use it).
Also we need a way to bootstrap container config using an agent that based on what it knows of the project can create a reasonable Dockerfile to get going because most users might have never used docker before.

I know it's a lot but we need to explore this design space.

I'm thinking of a CLI interface like:

```
ateam exec PROMPT --profile claude-sandbox   # no sandbox per-se (as a container) but built-in claude
ateam exec PROMPT --profile codex-docker-basic # because codex doesn't limit reads as well as claude we would offer it only with a docker config
ateam exec PROMPT --profile claude-docker-basic # even more isolation this way
```

We could also move away from 'sandbox' and use 'container'. Not sure.

First add this entire prompt at the end of PLAN_AGENT_SANDBOX_AND_TESTING.md as 'What to work on next'.
Then try to answer.

---

## Resolved Design

### Terminology: "container" not "sandbox"

Drop "sandbox" from CLI and user-facing concepts entirely. Three terms:

- **Agent**: a named configuration of an agent binary (claude, codex, mock). Multiple configs per binary.
- **Container**: an isolation runtime (docker, SRT, none). Concrete, not abstract.
- **Profile**: a named combo of agent + container. The primary user-facing concept.

Claude's native settings JSON is just how claude works — it's not a "sandbox", it's agent configuration. Docker and SRT are containers. No ambiguity.

### Agent configs: named instances of the same binary

A single binary can have multiple named configurations with different behaviors:

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  model   = "sonnet"
  # Bare claude — uses $HOME/.claude, no extra settings
}

agent "claude-sandbox" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  model   = "sonnet"
  # Uses $HOME/.claude but injects sandboxing settings (file write restrictions, etc.)
  settings_template = "claude_sandbox_settings.json"
}

agent "claude-isolated" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  model   = "sonnet"
  # Ignores $HOME/.claude entirely — for container use where host config shouldn't leak
  env_override = { HOME = "/home/ateam" }
}

agent "codex" {
  command = "codex"
  args    = ["--full-auto", "--quiet"]
  # Bare codex — reads HOME, limited write control
}

agent "codex-restricted" {
  command = "codex"
  args    = ["--full-auto", "--quiet", "--sandbox-permissions", "restricted"]
  # Codex with tighter CLI flags (as codex adds more permission options)
}

agent "mock" {
  type     = "builtin"
  response = "Mock response for testing"
}
```

The key: agent name ≠ binary name. `claude-sandbox` and `claude-isolated` both run the `claude` binary but with different settings and env.

### Container types

| Type | Runtime | Image required | Use case |
|------|---------|----------------|----------|
| `none` | — | No | Quick runs, trusted code, claude-sandbox with native settings |
| `docker` | Docker | Yes (`.ateam/Dockerfile`) | Full isolation, custom tooling, reproducible env |
| `srt` | Anthropic SRT | No (lightweight runtime) | Lightweight isolation without image management |

SRT is treated as a container type — same interface (start/exec/stop/shell), different backend. The `Container` interface in Go abstracts all three.

### Profiles

```hcl
profile "default" {
  agent     = "claude-sandbox"
  container = "none"
  # Claude's native settings handle isolation. Good enough for trusted projects.
}

profile "claude-docker" {
  agent     = "claude-isolated"
  container = "docker"
  # Full docker isolation. claude-isolated because $HOME inside container is clean.
}

profile "codex-docker" {
  agent     = "codex"
  container = "docker"
  # Required for codex — without docker, codex reads your entire HOME.
}

profile "codex-srt" {
  agent     = "codex"
  container = "srt"
  # Lightweight isolation for codex using Anthropic SRT.
}

profile "test" {
  agent     = "mock"
  container = "none"
}
```

### CLI interface: unified `ateam run`

`exec` and `run` are merged into a single `ateam run` command. Everything is optional except the prompt.

```
# Simplest form — no project, no role, just run an agent
ateam run "explain this error"

# With a role (requires project context)
ateam run "analyze auth module" --role security

# With explicit profile
ateam run "fix the bug" --profile claude-docker

# With role + profile override
ateam run @prompt.md --role security --profile codex-srt

# Workflow commands (unchanged)
ateam report --agents all --profile codex-srt
ateam code --profile claude-docker

# Interactive container shell
ateam shell --profile claude-docker

# Dockerfile generation
ateam docker-init
```

**Command hierarchy** (simplified from before):

```
run                  # universal: prompt + optional role/project/profile → output
  └─ report          # workflow: parallel runs for multiple roles
  └─ review          # workflow: supervisor synthesizes reports
  └─ code            # workflow: supervisor orchestrates code changes
shell                # interactive container session
docker-init          # generate Dockerfile
```

`report`, `review`, and `code` call `run` internally. `run` is the single execution primitive.

**`ateam run` flags**:

| Flag | Purpose | Notes |
|------|---------|-------|
| `--project NAME` | Select project | Inferred from cwd if in a project; optional |
| `--role NAME` | Run as a specific role | Optional; enables role-specific logging and sandbox rules |
| `--profile NAME` | Override profile | Wins over config.toml resolution |
| `--agent NAME` | Override agent | Picks up agent's default profile if no --profile |
| `--container` / `--no-container` | Toggle container wrapping | Shorthand |
| `--model NAME` | Override model | Agent-specific |
| `--agent-args "ARGS"` | Extra args for agent CLI | Appended after agent's configured args |
| `--container-args "ARGS"` | Extra args for docker run/exec | GPU, memory, ports, extra mounts, etc. |
| `--stream` | Show progress during execution | Default: quiet, print final output |
| `--summary` | Print run summary after completion | Cost, tokens, duration |

**Context resolution**:

```
Has --project or cwd is inside a project?
  YES → load config.toml, resolve profile from config, log to project logs dir
  NO  → need .ateamorg somewhere (for runtime.hcl); use --profile or runtime.hcl default
        log to .ateamorg/logs/
```

**Profile resolution** (updated for optional project/role):

```
1. CLI --profile flag                          (always wins)
2. [profiles.roles] for the role               (if --role given and project exists)
3. [report].default_profile / action default   (if project exists)
4. [project].default_profile                   (if project exists)
5. "default" profile from runtime.hcl           (always available)
```

**Logging** — file naming adapts to what context is available:

```
With project + role + action:
  .ateamorg/projects/<id>/logs/2026-03-11_15-04-05_security_report_stream.jsonl
  .ateamorg/projects/<id>/logs/2026-03-11_15-04-05_security_report_exec.md
  .ateamorg/projects/<id>/logs/2026-03-11_15-04-05_security_report_stderr.log

With project + role (no explicit action → "run"):
  .ateamorg/projects/<id>/logs/2026-03-11_15-04-05_security_run_stream.jsonl

With project, no role:
  .ateamorg/projects/<id>/logs/2026-03-11_15-04-05_run_stream.jsonl

No project at all:
  .ateamorg/logs/2026-03-11_15-04-05_run_stream.jsonl
```

Pattern: `TIMESTAMP[_ROLE][_ACTION]_{stream.jsonl,exec.md,stderr.log}`

**Behavior differences based on context**:

| Context | Logging | Sandbox rules | LastMessage file | History archive |
|---------|---------|---------------|------------------|-----------------|
| Project + role | Project logs dir | Role-specific from config | Yes (role dir) | Yes (role history) |
| Project, no role | Project logs dir | Project defaults | No | No |
| No project | Org logs dir | Profile defaults only | No | No |

Without a role, `ateam run` is a simple one-shot: output goes to stdout, stream is logged, no persistent state files. This is the old `exec` behavior, just under the `run` name.

### Container lifecycle (approach B: own what's needed, expose the rest)

Ateam manages containers for agent calls. For debugging, `ateam shell` provides a thin convenience wrapper.

**One-shot** (exec, run, report):

```bash
docker run --rm -i \
  --name ateam-<project-id>-<timestamp> \
  -v /path/to/source:/workspace:rw \
  -v /path/to/.ateam:/workspace/.ateam:rw \
  -v /path/to/.ateamorg:/home/ateam/.ateamorg:rw \
  -w /workspace \
  -e ANTHROPIC_API_KEY \
  -e CLAUDE_CODE_OAUTH_TOKEN \
  ateam-<project-id>:latest \
  claude -p --output-format stream-json --verbose --settings /workspace/.ateam/settings.json
```

Prompt piped to stdin. Mounts derived from SandboxRules — dirs not mounted are invisible.
Auth env vars forwarded from host (see "Container auth" section).

**Start+exec** (code phase, where supervisor calls `ateam run` multiple times):

```bash
# Start once per session
docker run -d \
  --name ateam-<project-id> \
  -v /path/to/source:/workspace:rw \
  -v /path/to/.ateam:/workspace/.ateam:rw \
  -v /path/to/.ateamorg:/home/ateam/.ateamorg:rw \
  -w /workspace \
  -e ANTHROPIC_API_KEY \
  -e CLAUDE_CODE_OAUTH_TOKEN \
  ateam-<project-id>:latest \
  sleep infinity

# Each agent call
docker exec -i ateam-<project-id> \
  claude -p --output-format stream-json --verbose --settings /workspace/.ateam/settings.json

# Teardown
docker stop ateam-<project-id> && docker rm ateam-<project-id>
```

**Interactive shell** (`ateam shell --profile claude-docker`):

```bash
# If container already running (from code phase):
docker exec -it ateam-<project-id> bash

# If no container running, start one:
docker run --rm -it \
  -v /path/to/source:/workspace:rw \
  ... (same mounts) ...
  ateam-<project-id>:latest \
  bash
```

**Debug output**: all commands print what they run:

```
[container] docker exec -i ateam-myproject claude -p --output-format stream-json ...
[container] To attach: docker exec -it ateam-myproject bash
```

**SRT equivalent**: same pattern, different binary (`srt run`, `srt exec`, etc.). The `Container` interface abstracts this.

### Dockerfile management

**Default**: `ateam init` offers to create `.ateam/Dockerfile` with a minimal image:

```dockerfile
FROM node:22-slim
RUN npm install -g @anthropic-ai/claude-code
RUN useradd -m ateam
USER ateam
WORKDIR /workspace
```

Enough to run claude. No project-specific tooling.

**`ateam docker-init`**: agent-assisted Dockerfile generation.

1. Reads the project (go.mod, package.json, requirements.txt, Makefile, docker-compose.yml, etc.)
2. Asks the user what they need:
   - "Your project uses Go 1.23 and PostgreSQL. Need a running DB in the container?"
   - "Need to expose ports for testing a web app?"
   - "Need system packages (ffmpeg, imagemagick, etc.)?"
3. Generates a tailored `.ateam/Dockerfile`

Implementation: a built-in prompt template run via `ateam run` with `--profile default`. The agent reads the project, asks questions (via tool_use), writes the Dockerfile. Not a separate binary — just a specialized prompt.

### Incompatible layers

Defined in default config with clear documentation:

```hcl
# In default runtime.hcl:

profile "codex" {
  agent     = "codex"
  container = "docker"  # REQUIRED: codex reads entire HOME without container
}

# If user tries --agent codex --no-container:
# Error: codex requires a container (reads entire HOME directory).
#        Use --profile codex-docker or --profile codex-srt.
```

Behavior:

| Combo | Behavior |
|-------|----------|
| `codex` + `no container` | **Error** (documented in default config) |
| `claude` + `no container` | OK (uses native settings for isolation) |
| `claude-isolated` + `no container` | **Warning** (isolation only works inside container) |
| Any agent + `docker` without Dockerfile | **Error** with hint: `run 'ateam docker-init' to create .ateam/Dockerfile` |

These rules live in the profile/agent config, not in code. The default `runtime.hcl` documents them. Users can override (at their own risk) with `--force`.

### Container auth: token forwarding

Agents running inside containers need API credentials. The host has these (env vars, config files), but the container doesn't unless we forward them.

**Known requirements**:

| Agent | Auth mechanism | Env var |
|-------|---------------|---------|
| Claude Code | OAuth token | `CLAUDE_CODE_OAUTH_TOKEN` |
| Claude Code | API key (alternative) | `ANTHROPIC_API_KEY` |
| Codex | TBD — needs investigation | TBD |

**Problem**: if we start a container without the right token, the agent fails with an auth error. This might be a clear error ("not authenticated") or an obscure one depending on the agent. We don't know yet.

**Design options** (to be decided after testing):

1. **Pre-flight check**: before starting a container, check that the required env vars for the agent exist on the host. If missing, error with a clear message: `Error: claude-isolated requires CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY to run in a container. Run 'claude auth' on the host first.`

2. **Automatic forwarding**: the agent config in `runtime.hcl` declares which env vars it needs. The container startup automatically forwards them via `-e`:
   ```hcl
   agent "claude-isolated" {
     command = "claude"
     requires_container = true
     container_env = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]  # forwarded to container
   }
   ```

3. **Token mounting**: for agents that use config files (e.g. `~/.claude/` or `~/.codex/`), mount the relevant host path read-only into the container. But this conflicts with `claude-isolated`'s goal of ignoring `$HOME/.claude`.

**Recommendation**: option 2 (declare in agent config) + option 1 (pre-flight check). The agent config lists required env vars. Before container start, ateam checks they exist and errors early if not. This avoids obscure container failures.

**TODO**: test what happens when claude/codex run in a container without auth tokens. Document the actual error messages. Decide if pre-flight check is sufficient or if we need a token-acquisition callback.

### Passthrough args: `--agent-args` and `--container-args`

`ateam run` supports passing arbitrary args to the agent CLI and to the container runtime. Both use the same `"ARGS"` string convention for consistency.

**Agent CLI args** — `--agent-args` passes additional flags to the agent binary:

```bash
# Passes --model haiku --max-turns 5 to claude
ateam run "fix the bug" --profile claude-docker --agent-args "--model haiku --max-turns 5"

# Passes --full-auto --model o3 to codex
ateam run "fix the bug" --profile codex-docker --agent-args "--full-auto --model o3"
```

These args are appended to the agent's configured `args` from `runtime.hcl`. They appear after the hardcoded args, so they can override them (last flag wins for most CLIs).

**Container extra args** — `--container-args` passes additional flags to `docker run`/`docker exec`:

```bash
# Add GPU access and extra memory
ateam run "train the model" --profile claude-docker \
  --container-args "--gpus all --memory 8g"

# Mount an extra volume
ateam run "analyze data" --profile claude-docker \
  --container-args "-v /data/datasets:/datasets:ro"

# Expose a port for testing
ateam run "start the server" --profile claude-docker \
  --container-args "-p 8080:8080"
```

**Implementation**: the `--agent-args` string is split and appended to `AgentRequest.ExtraArgs`. The `--container-args` string is split and passed to the `Container.Start()` / `Container.Exec()` methods as additional flags. Both are stored in the DB call record for reproducibility.

**In runtime.hcl**, agents can also declare default extra args:

```hcl
agent "claude-isolated" {
  command    = "claude"
  args       = ["-p", "--output-format", "stream-json", "--verbose"]
  # User's -- args are appended after these
}
```

### DB interface for tracking agent calls

Replace the append-only `runner.log` text file with SQLite. Every agent invocation inserts a row.
We must use wal and trict mode.

**Schema**:

```sql
CREATE TABLE agent_calls (
  id            INTEGER PRIMARY KEY,

  -- Core Dimensions
  project_id    TEXT NOT NULL
  profile       TEXT NOT NULL,     -- profile name used
  agent         TEXT NOT NULL,     -- agent config name
  container     TEXT NOT NULL,     -- "none", "docker", "srt"
  action        TEXT NOT NULL,     -- "report", "review", "code", "run", "exec"
  role          TEXT,              -- null for exec/review
  task_group    TEXT,              -- when executing related tasks set a common label to report on their aggregate or just find them

  -- Arguments
  model         TEXT,
  prompt_hash   TEXT,              -- SHA-256 of prompt (not the full prompt). TODO: need a way to find that prompt. Or maybe store the prefix (so exact file name if a file or first part of a real longer prompt)
  -- TODO: exact CLI option ?

  -- Runtime at start
  started_at    TEXT NOT NULL,     -- ISO-8601
  stream_file   TEXT,              -- path to JSONL stream
  -- TODO: container id ?

  -- Runtime at end
  ended_at      TEXT,
  duration_ms   INTEGER,
  exit_code     INTEGER,
  is_error      BOOLEAN DEFAULT 0,
  error_message TEXT,
  cost_usd      REAL,
  input_tokens  INTEGER,
  output_tokens INTEGER,
  cache_read_tokens INTEGER,
  turns         INTEGER,

  -- TODO: need a way to get a path to the formatted last message output
);

CREATE INDEX idx_calls_project ON agent_calls(project_id, started_at);
CREATE INDEX idx_calls_action ON agent_calls(action, started_at);
```

**Location**: `.ateamorg/projects/<id>/calls.db` (per-project).

**Usage**: after each `Runner.Run()`, insert a row with the `RunSummary` data + profile/agent/container metadata.

**Queries** (future `ateam stats` command):

```
ateam stats                  # total cost, call count, avg duration
ateam stats --last 7d        # last 7 days
ateam stats --agent codex    # filter by agent
ateam stats --action report  # filter by action
```

The existing `runner.log` text format is kept as a human-readable debug log alongside the DB.

### Profile resolution in config.toml

`runtime.hcl` defines what profiles exist. `config.toml` controls which profile is used when.

**Updated config.toml**:

```toml
[project]
name = "myproject"
default_profile = "claude-sandbox"   # project-wide fallback

[roles]
security = "enabled"
testing_basic = "enabled"
refactor_small = "enabled"

[report]
max_parallel = 3
report_timeout_minutes = 20
default_profile = "codex-docker"     # all reports use codex in docker

[supervisor]
default_profile = "claude-sandbox"   # supervisor default (review + code orchestration)
review_profile = "codex-docker"      # review specifically uses codex
code_profile = "claude-docker"       # code phase uses claude in docker

[review]
timeout_minutes = 20

[code]
timeout_minutes = 120

# Per-role profile overrides (takes precedence over action defaults)
[profiles.roles]
security = "claude-docker"           # security role always uses claude-docker
testing_basic = "codex-srt"          # testing uses codex with SRT
```

**Resolution order** (highest priority first):

```
For role actions (report, run):
  1. CLI --profile flag
  2. [profiles.roles] for the role
  3. [report] default_profile  (or [run] default_profile if we add one)
  4. [project] default_profile

For supervisor actions (review, code):
  1. CLI --profile flag
  2. [supervisor] review_profile / code_profile
  3. [supervisor] default_profile
  4. [project] default_profile

For exec (no action/role context):
  1. CLI --profile flag
  2. [project] default_profile
```

Examples:

```bash
# Uses codex-docker (from [report] default_profile)
ateam report --roles refactor_small

# Uses claude-docker (from [profiles.roles] security, overrides report default)
ateam report --roles security

# Uses codex-srt (explicit CLI flag overrides everything)
ateam report --roles security --profile codex-srt

# Uses claude-docker (from [supervisor] code_profile)
ateam code

# Uses codex-docker (from [supervisor] review_profile)
ateam review

# Uses claude-docker (from [profiles.roles] security)
ateam run "fix this" --role security

# Uses claude-sandbox (from [project] default_profile, no role override)
ateam run "fix this" --role refactor_small
```

**Go config struct changes**:

```go
type Config struct {
    Project    ProjectConfig     `toml:"project"`
    Git        GitConfig         `toml:"git"`
    Report     ReportConfig      `toml:"report"`
    Supervisor SupervisorConfig  `toml:"supervisor"`
    Review     ReviewConfig      `toml:"review"`
    Code       CodeConfig        `toml:"code"`
    Roles      map[string]string `toml:"roles"`
    Profiles   ProfilesConfig    `toml:"profiles"`
}

type ProjectConfig struct {
    Name           string `toml:"name"`
    DefaultProfile string `toml:"default_profile"`
}

type ReportConfig struct {
    MaxParallel          int    `toml:"max_parallel"`
    ReportTimeoutMinutes int    `toml:"report_timeout_minutes"`
    DefaultProfile       string `toml:"default_profile"`
}

type SupervisorConfig struct {
    DefaultProfile string `toml:"default_profile"`
    ReviewProfile  string `toml:"review_profile"`
    CodeProfile    string `toml:"code_profile"`
}

type ProfilesConfig struct {
    Roles map[string]string `toml:"roles"`  // role → profile name
}

// ResolveProfile returns the profile name for a given action and role.
// CLI override is handled by the caller (if --profile is set, skip this).
func (c *Config) ResolveProfile(action, roleID string) string {
    // 1. Per-role override (report/run actions)
    if roleID != "" {
        if profile, ok := c.Profiles.Roles[roleID]; ok {
            return profile
        }
    }

    // 2. Action-specific defaults
    switch action {
    case "report", "run":
        if c.Report.DefaultProfile != "" {
            return c.Report.DefaultProfile
        }
    case "review":
        if c.Supervisor.ReviewProfile != "" {
            return c.Supervisor.ReviewProfile
        }
        if c.Supervisor.DefaultProfile != "" {
            return c.Supervisor.DefaultProfile
        }
    case "code":
        if c.Supervisor.CodeProfile != "" {
            return c.Supervisor.CodeProfile
        }
        if c.Supervisor.DefaultProfile != "" {
            return c.Supervisor.DefaultProfile
        }
    }

    // 3. Project-wide fallback
    if c.Project.DefaultProfile != "" {
        return c.Project.DefaultProfile
    }
    return "default"
}
```

**Display**: `ateam report --roles all` should show which profile each role resolves to:

```
Role             Profile         Agent            Container
security         claude-docker   claude-isolated  docker       (from role override)
testing_basic    codex-srt       codex            srt          (from role override)
refactor_small   codex-docker    codex            docker       (from report default)
```

### Updated Go interfaces

```go
// Agent executes a prompt and produces a result.
type Agent interface {
    Name() string
    Run(ctx context.Context, req AgentRequest) AgentResult
}

// Container provides isolation for agent execution.
type Container interface {
    Type() string                  // "none", "docker", "srt"
    Start(ctx context.Context) error
    Exec(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error
    Shell(ctx context.Context) error  // interactive shell
    Stop(ctx context.Context) error
    IsRunning() bool
    DebugCommand(args []string) string  // returns raw command for debug output
}

// NoneContainer is a passthrough — runs commands on host.
type NoneContainer struct{}

// DockerContainer manages a docker container lifecycle.
type DockerContainer struct {
    ProjectID string
    Image     string
    Mounts    []Mount
    Env       map[string]string
    // container ID tracked in .ateamorg/projects/<id>/container.lock
}

// SRTContainer manages an Anthropic SRT container.
type SRTContainer struct {
    ProjectID string
    // SRT-specific config
}

// Profile combines agent + container.
type Profile struct {
    Name      string
    Agent     Agent
    Container Container
}

// Runner orchestrates execution using a Profile.
type Runner struct {
    Profile    Profile
    LogFile    string
    ProjectDir string
    OrgDir     string
    DB         *AgentCallDB  // SQLite tracking
}
```

### Updated runtime.hcl

```hcl
# .ateamorg/defaults/runtime.hcl — embedded defaults, written by ateam install
# Can be overridden in .ateamorg/runtime.hcl (org-level) or .ateam/runtime.hcl (project-level)

agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  model   = "sonnet"
}

agent "claude-sandbox" {
  command           = "claude"
  args              = ["-p", "--output-format", "stream-json", "--verbose"]
  model             = "sonnet"
  settings_template = "claude_sandbox_settings.json"
}

agent "claude-isolated" {
  command      = "claude"
  args         = ["-p", "--output-format", "stream-json", "--verbose"]
  model        = "sonnet"
  env_override = { HOME = "/home/ateam" }
  requires_container = true
}

agent "codex" {
  command = "codex"
  args    = ["--full-auto", "--quiet"]
  requires_container = true  # codex reads HOME
}

agent "mock" {
  type = "builtin"
}

container "none" {
  type = "none"
}

container "docker" {
  type       = "docker"
  dockerfile = ".ateam/Dockerfile"
  idle_timeout = "30m"
}

container "srt" {
  type = "srt"
}

profile "default" {
  agent     = "claude-sandbox"
  container = "none"
}

profile "claude-docker" {
  agent     = "claude-isolated"
  container = "docker"
}

profile "codex-docker" {
  agent     = "codex"
  container = "docker"
}

profile "codex-srt" {
  agent     = "codex"
  container = "srt"
}

profile "test" {
  agent     = "mock"
  container = "none"
}
```

### Implementation order (revised)

1. ~~**Agent interface + MockAgent** — extract from `ClaudeRunner`, add `ClaudeAgent`~~ ✅
2. ~~**Container interface + NoneContainer** — passthrough, no behavior change~~ ✅
3. ~~**Profile + HCL config** — load `runtime.hcl`, resolve profiles~~ ✅
4. ~~**Runner refactor** — uses Profile instead of ClaudeRunner directly~~ ✅
5. ~~**Unified `ateam run`** — merge exec into run, make --role/--project optional, adapt logging~~ ✅
6. ~~**Layer 1+2 tests** — mock agent plumbing + rule verification~~ ✅ (partial — runner_test.go + config_test.go)
7. **DockerContainer** — docker lifecycle, mount translation ← **NEXT**
8. **Layer 3 smoke test** — real agent in profile
9. **`ateam shell`** — interactive container shell
10. **AgentCallDB** — SQLite tracking, insert on each run
11. **`ateam docker-init`** — agent-assisted Dockerfile generation
12. **SRTContainer** — Anthropic SRT support
13. **Layer 4+5** — enforcement + docker E2E tests
14. **`ateam stats`** — query the call DB

---

## Implementation Status (as of 2026-03-11)

### What exists today

```
internal/
  agent/
    agent.go        # Agent interface, StreamEvent, Request, SandboxRules, buildProcessEnv
    claude.go       # ClaudeAgent — streaming JSONL parser, settings injection
    codex.go        # CodexAgent — codex exec --json, JSONL parser
    codex_test.go   # 14 parser tests
    mock.go         # MockAgent — canned events for testing
    mock_test.go
  container/
    container.go    # Container interface, NoneContainer (passthrough)
  runtime/
    config.go       # HCL parsing, 4-level resolution, agent inheritance (base)
    config_test.go  # Tests: defaults, inheritance, circular refs, sandbox, org/project override
    defaults/
      runtime.hcl   # Embedded defaults (agents, containers, profiles)
  runner/
    runner.go       # Runner orchestrator using Agent interface
    pool.go         # Parallel runner pool
    events.go       # Claude stream parser (legacy, used by FormatStream)
    format.go       # Stream formatting for ateam log/tail
    runner_test.go  # MockAgent integration tests
  config/
    config.go       # config.toml with ResolveProfile(), SupervisorConfig, ProfilesConfig
```

**Key design choices already locked in:**

- **Agent.Run() returns `<-chan StreamEvent`** — channel-based streaming, not blocking
- **Agents spawn their own process** — ClaudeAgent/CodexAgent each build their own `exec.Command`, manage env, parse output
- **sandbox attribute is inline JSON** in runtime.hcl (heredoc), not a filename reference. Runner parses it directly, merges runtime paths, writes to a temp file for `--settings`
- **Agent inheritance via `base`** — child agents inherit command, args, env, sandbox, model, type from parent. Zero-value = inherit. Circular detection.
- **4-level runtime.hcl resolution** — embedded → .ateamorg/defaults/ → .ateamorg/ → .ateam/
- **Profile resolution from config.toml** — role-specific → action-specific supervisor → supervisor default → project default → "default"
- **`--profile` and `--agent` flags** on run/report/review/code, mutually exclusive

**What the Runner does today (runner.go):**

1. Creates log files (stream.jsonl, stderr.log, exec.md, settings.json)
2. If `SandboxSettings != ""`: parses inline JSON, merges runtime write dirs, writes temp settings file, appends `--settings <path>` to extra args
3. Archives prompt to history dir
4. Builds `agent.Request` and calls `r.Agent.Run(ctx, req)`
5. Consumes `StreamEvent` channel, emits `RunProgress`, accumulates metrics
6. Writes output file (LastMessageFilePath) or error file
7. Appends to runner.log

**What the Runner does NOT do yet:**

- No container awareness — agents always run on host
- No `ParseStreamFile()` for log replay
- SandboxRules struct exists but is unused (settings JSON handles sandbox for claude, codex uses its own `--sandbox` flag)

---

## Phase 2: Docker Container Support

### Goal

Run agents inside Docker containers for filesystem isolation. The container wraps the agent process — same agent binary, different execution environment.

### Architecture

The container is **not** part of the agent. It's a wrapper around the agent's execution:

```
Without container (today):
  Runner → Agent.Run() → exec.Command("claude", args...) on host

With container:
  Runner → Container.Exec("claude", args...) → docker exec → claude inside container
```

The agent doesn't know or care whether it's in a container. The Runner decides based on the profile's container config.

### Container interface (expanded from current)

```go
package container

type Container interface {
    Type() string // "none", "docker", "srt"

    // Ensure the container is running (build image if needed, start if not running).
    // Idempotent — safe to call multiple times.
    EnsureRunning(ctx context.Context) error

    // Exec runs a command inside the container. Blocks until command completes.
    Exec(ctx context.Context, opts ExecOpts) error

    // Stop shuts down the container. No-op if not running.
    Stop(ctx context.Context) error

    // IsRunning checks if the container is currently running.
    IsRunning(ctx context.Context) bool

    // Shell opens an interactive shell in the container.
    Shell(ctx context.Context) error

    // DebugCommand returns the raw docker/srt command for debug output.
    DebugCommand(opts ExecOpts) string
}

type ExecOpts struct {
    Command   string
    Args      []string
    Stdin     io.Reader
    Stdout    io.Writer
    Stderr    io.Writer
    WorkDir   string
    Env       []string      // KEY=VALUE pairs
    ExtraArgs []string      // from --container-args
}
```

NoneContainer stays as-is (passthrough). The current `RunOpts` maps to the new `ExecOpts`.

### DockerContainer implementation

```go
type DockerContainer struct {
    // Config (from runtime.hcl container block)
    Name         string            // container name: "ateam-<project-id>"
    Image        string            // image name: "ateam-<project-id>:latest"
    Dockerfile   string            // path to Dockerfile (relative to source dir)
    IdleTimeout  time.Duration     // auto-stop after idle (0 = manual stop only)

    // Runtime context (set by Runner before use)
    SourceDir    string            // project source root — mounted as /workspace
    ProjectDir   string            // .ateam/ dir — mounted as /workspace/.ateam
    OrgDir       string            // .ateamorg/ dir — mounted read-only
    ExtraMounts  []Mount           // additional mounts from SandboxRules

    // Auth (forwarded from host)
    ForwardEnv   []string          // env var names to forward (e.g. ANTHROPIC_API_KEY)
}

type Mount struct {
    HostPath      string
    ContainerPath string
    ReadOnly      bool
}
```

### Docker lifecycle

**One-shot model** (for `ateam run`, `ateam report`):

```bash
docker run --rm -i \
  --name ateam-<project-id>-<timestamp> \
  -v <source>:/workspace:rw \
  -v <.ateam>:/workspace/.ateam:rw \
  -v <.ateamorg>:/home/ateam/.ateamorg:ro \
  -w /workspace \
  -e ANTHROPIC_API_KEY \
  -e CLAUDE_CODE_OAUTH_TOKEN \
  ateam-<project-id>:latest \
  claude -p --output-format stream-json --verbose --settings /workspace/.ateam/settings.json
```

Prompt piped to stdin. Container removed after completion (`--rm`).

**Start+exec model** (for `ateam code`, where supervisor calls `ateam run` multiple times):

```bash
# Start once
docker run -d \
  --name ateam-<project-id> \
  -v <source>:/workspace:rw \
  -v <.ateam>:/workspace/.ateam:rw \
  -v <.ateamorg>:/home/ateam/.ateamorg:ro \
  -w /workspace \
  -e ANTHROPIC_API_KEY \
  -e CLAUDE_CODE_OAUTH_TOKEN \
  ateam-<project-id>:latest \
  sleep infinity

# Each agent call
docker exec -i ateam-<project-id> \
  claude -p --output-format stream-json --verbose --settings /workspace/.ateam/settings.json

# Teardown
docker stop ateam-<project-id> && docker rm ateam-<project-id>
```

**Decision: start with one-shot only.** The start+exec model is needed for the code phase but adds complexity (container lifecycle tracking, lock files, idle timeout). One-shot covers `ateam run`, `ateam report`, `ateam review`. The code phase can be added later.

### How the Runner uses containers

Today the Runner calls `r.Agent.Run(ctx, req)` which internally does `exec.CommandContext(...)`. With containers, the Runner needs to:

1. Check if the profile has a container != "none"
2. If yes: call `container.EnsureRunning()`, then `container.Exec()` instead of `agent.Run()`

But wait — the agent still needs to parse the JSONL output. The container just changes *where* the process runs, not *what* it does.

**Two approaches:**

**A) Container wraps the agent (decorator pattern)**:
```go
type ContainerAgent struct {
    Inner     Agent       // the real agent (claude, codex)
    Container Container   // docker, srt
}
// ContainerAgent.Run() calls Container.Exec() with the agent's command,
// then uses Inner's parser on the output.
```

**B) Agent receives a command executor**:
```go
type CommandExecutor func(ctx context.Context, cmd string, args []string, ...) (*exec.Cmd, error)
// Host executor: exec.CommandContext(ctx, cmd, args...)
// Docker executor: exec.CommandContext(ctx, "docker", "exec", containerID, cmd, args...)
```

**Recommendation: approach B (command executor).** It's simpler — the agent still owns its full lifecycle (build args, parse output, manage stream file), it just runs the command differently. The executor is a function, not a new type hierarchy.

```go
// In agent package:
type Request struct {
    Prompt     string
    WorkDir    string
    StreamFile string
    StderrFile string
    ExtraArgs  []string
    Env        map[string]string
    // NEW: if set, the agent uses this to create its subprocess
    // instead of exec.CommandContext directly.
    CmdFactory CmdFactory
}

// CmdFactory creates an *exec.Cmd. The agent calls this instead of
// exec.CommandContext when present. For docker, this returns a command
// like: docker exec -i <container> <original-cmd> <original-args>
type CmdFactory func(ctx context.Context, name string, args ...string) *exec.Cmd
```

When `CmdFactory` is nil, agents use `exec.CommandContext` as today (no behavior change). When set, the factory wraps the command in `docker exec`.

The Runner sets `req.CmdFactory` based on the profile's container:

```go
func (r *Runner) Run(ctx context.Context, prompt string, opts RunOpts, ...) RunSummary {
    // ... existing setup ...

    req := agent.Request{
        Prompt:     prompt,
        WorkDir:    cwd,
        StreamFile: streamFile,
        StderrFile: stderrFile,
        ExtraArgs:  extraArgs,
    }

    if r.Container != nil && r.Container.Type() != "none" {
        if err := r.Container.EnsureRunning(ctx); err != nil {
            return failEarly(err)
        }
        req.CmdFactory = r.Container.CmdFactory()
    }

    events := r.Agent.Run(ctx, req)
    // ... rest unchanged ...
}
```

### File paths inside the container

The container has different filesystem paths than the host. The agent runs inside the container, so file paths in `Request` need to be container-relative:

| Host path | Container path |
|-----------|---------------|
| `/Users/nic/projects/myapp` (source) | `/workspace` |
| `/Users/nic/projects/myapp/.ateam` | `/workspace/.ateam` |
| `/Users/nic/.ateamorg` | `/home/ateam/.ateamorg` |
| Stream/stderr/settings files (in .ateam/logs/) | `/workspace/.ateam/logs/...` |

The Runner needs to translate paths when building the Request. The container provides a `TranslatePath(hostPath) string` method.

### runtime.hcl additions

```hcl
container "docker" {
  type       = "docker"
  dockerfile = "Dockerfile"           # relative to .ateam/ dir
  idle_timeout = "30m"                # for start+exec model (future)
  forward_env = [                     # env vars forwarded from host
    "ANTHROPIC_API_KEY",
    "CLAUDE_CODE_OAUTH_TOKEN",
    "OPENAI_API_KEY",
  ]
}

# Agent config additions for container support:
agent "claude-isolated" {
  base    = "claude"
  sandbox = ""                        # no settings needed — container handles isolation
  env = {
    CLAUDECODE = ""
    HOME = "/home/ateam"              # clean HOME inside container
  }
}

# New profiles:
profile "claude-docker" {
  agent     = "claude-isolated"
  container = "docker"
}

profile "codex-docker" {
  agent     = "codex"
  container = "docker"
}
```

### ContainerConfig additions

```go
type ContainerConfig struct {
    Name        string
    Type        string   // "none", "docker", "srt"
    Dockerfile  string   // relative to .ateam/ dir
    IdleTimeout string   // duration string, e.g. "30m"
    ForwardEnv  []string // env var names to forward from host
}
```

### Default Dockerfile

Generated by `ateam init` or `ateam docker-init`:

```dockerfile
FROM node:22-slim
RUN npm install -g @anthropic-ai/claude-code
RUN useradd -m ateam
USER ateam
WORKDIR /workspace
```

Enough to run claude. The `docker-init` command (future) generates a project-specific Dockerfile using an agent.

### Implementation steps for Docker support

**Step 1: Expand ContainerConfig in runtime.hcl**
- Add `dockerfile`, `idle_timeout`, `forward_env` to hclContainer + ContainerConfig
- Add `container "docker"` to embedded defaults (with dockerfile and forward_env)
- No behavior change yet

**Step 2: Add CmdFactory to agent.Request**
- Add `CmdFactory func(ctx, name, args) *exec.Cmd` to Request
- Update ClaudeAgent.run() and CodexAgent.run() to use `req.CmdFactory` when set
- No behavior change when nil (all existing paths)

**Step 3: DockerContainer implementation**
- `internal/container/docker.go`
- `EnsureRunning()`: check if image exists → build if not → `docker run --rm -d`
- `CmdFactory()`: returns a function that wraps commands in `docker exec -i <id>`
- `Exec()`: wraps command in `docker exec`, connects stdin/stdout/stderr
- `Stop()`: `docker stop && docker rm`
- `IsRunning()`: `docker inspect`
- `Shell()`: `docker exec -it <id> bash`
- `DebugCommand()`: returns the full docker command string
- Path translation: `TranslatePath()` maps host paths to container paths

**Step 4: Wire Runner to Container**
- Add `Container container.Container` to Runner struct
- In `Runner.Run()`: if container != none, call `EnsureRunning()`, set `req.CmdFactory`
- Translate file paths (stream, stderr, settings) to container-relative paths
- `cmd/table.go`: `buildContainer()` from ContainerConfig, set on Runner

**Step 5: Add docker profiles to defaults**
- Add `claude-docker` and `codex-docker` profiles
- Add `claude-isolated` agent config
- Pre-flight check: verify required env vars exist before starting container

**Step 6: `ateam shell` command**
- `cmd/shell.go`: resolve profile, call `container.Shell()`
- Errors if profile uses container "none"

**Step 7: Tests**
- Unit: DockerContainer.DebugCommand() returns correct docker commands
- Unit: CmdFactory wrapping produces correct exec.Cmd
- Unit: path translation
- Integration (gated): `ATEAM_DOCKER_TEST=1` — build image, run mock agent inside, verify output
