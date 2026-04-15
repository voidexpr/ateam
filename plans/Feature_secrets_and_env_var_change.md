# Design: Credential Isolation for Agent Execution

This is the raw plan from Claude. We are doing A. So it's best to always use ateam secrets to shield against issues. But env vars can work still.
This breaks if:
* use ateam secret in base host
* docker exec into a container with an API KEY then it will get used instead of ateam secrets


## The Problem

When ateam spawns an agent, it inherits the full `os.Environ()`. This causes credential confusion:
- **Host:** forgotten `export ANTHROPIC_API_KEY=...` in `.bashrc` overrides ateam secret
- **Docker:** a container's `ANTHROPIC_API_KEY` (for its own app) gets used by ateam agents
- **CI (future):** pipeline has `ANTHROPIC_API_KEY` for app tests; ateam picks it up for its own agents
- Claude Code auth priority: `ANTHROPIC_API_KEY > CLAUDE_CODE_OAUTH_TOKEN > interactive` — wrong var wins

## CI System Patterns

| Model | Systems | How it works |
|-------|---------|-------------|
| **Constructed env** | GitHub Actions, GitLab CI, CircleCI, K8s | Clean slate. Secrets explicitly injected. Safe by default. |
| **Inherited env** | Jenkins, Buildkite | Agent's env leaks into builds. Known footgun. |

Ateam is currently "inherited env" — the dangerous model. No CI system solves tool-vs-app credential namespacing natively; they all rely on **naming conventions**.

## Key Constraint

Claude Code reads `ANTHROPIC_API_KEY` from its process env — no flag to override. If both `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` exist, `ANTHROPIC_API_KEY` always wins. We must remove competing credentials from the agent's process env.

## Two Viable Approaches

### Approach A: Reversed Priority + Agent-Level Stripping

**How:** Secret store checked first (not env). After resolution, strip "losing" credential alternatives from `ac.Env` using the existing `""` = exclude mechanism in `buildProcessEnv()`. Don't touch `os.Environ()` globally.

**Pros:**
- Simple: 4 files, no new naming conventions, no Docker forwarding changes
- Uses existing `ac.Env["KEY"] = ""` mechanism (proven with `CLAUDECODE`)
- Works for host, Docker with mounted secrets.env, most CI setups

**Cons:**
- CI: requires `ateam secret` setup step or mounted `secrets.env`
- Edge case: nested ateam in Docker without secrets.env, with conflicting env vars → wrong credential. Mitigated by warning.
- Doesn't fully solve "inherited env" — it filters at the agent level, but the ateam process itself still inherits everything

### Approach B: ATEAM_SECRET_ Prefix

**How:** New `ATEAM_SECRET_<NAME>` env var namespace. Strip original credential names everywhere. Resolver checks `ATEAM_SECRET_*` first. Docker forwarding uses prefix.

**Pros:**
- Handles 100% of cases (nested Docker, CI without setup step)
- CI-friendly: just set `ATEAM_SECRET_CLAUDE_CODE_OAUTH_TOKEN` in CI secrets — done
- Clean namespace: impossible to confuse ateam's credential with app's
- Follows the "naming convention" pattern that CI systems universally use

**Cons:**
- More files to change (6 vs 4)
- New convention to learn + document
- `os.Unsetenv` modifies global process state
- If a project's tests need `ANTHROPIC_API_KEY` for their own API calls, stripping from `os.Environ()` affects the agent's child processes too

### Comparison

| Scenario | Approach A | Approach B |
|----------|-----------|-----------|
| Host: shell has wrong key | works (store wins, alt stripped from agent) | works (original stripped, store wins) |
| Docker: container has app's API key | works (if secrets.env mounted) | works (prefix survives stripping) |
| Docker: nested ateam, no secrets.env | fails (falls back to env) | works (prefix from docker-exec survives) |
| CI: pipeline has app's API key | works (if ateam secret setup step) | works (just set ATEAM_SECRET_* in CI) |
| Project tests need ANTHROPIC_API_KEY | works (only agent env filtered, not os.Environ) | broken (os.Unsetenv strips it globally) |

**That last row is important.** Approach A filters at the agent level — the ateam process's `os.Environ()` keeps the original value, so if the agent spawns test runners that need `ANTHROPIC_API_KEY`, those tests... wait, no. The agent IS the child process. `buildProcessEnv` constructs `cmd.Env` for the agent, which strips `ANTHROPIC_API_KEY`. The agent's children inherit the agent's env. So **both approaches have the same impact on project tests**.

The real difference: Approach A re-injects the resolved value under `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (whichever was resolved). If the project needs `ANTHROPIC_API_KEY` specifically and ateam resolved `CLAUDE_CODE_OAUTH_TOKEN` instead, the project's tests lose `ANTHROPIC_API_KEY` in both approaches.

**Net assessment:** Approach B's extra coverage (nested Docker, CI without setup) is real. The downsides (new convention, more files) are manageable. But Approach A's simplicity is attractive for a first version.

## Recommendation: Approach A first, with clear path to B

Start with Approach A (reversed priority + agent-level stripping). It solves the immediate problem with minimal changes. Add `ATEAM_SECRET_*` prefix support as a follow-up when CI integration becomes a priority.

The key insight: Approach A doesn't close any doors. We can add `ATEAM_SECRET_*` prefix to the resolver later without breaking anything. The reversed priority and agent-level stripping stay useful either way.

## Implementation (Approach A)

### 1. `internal/secret/resolve.go` — Reverse priority

Scopes first, env last. If `ateam secret` is configured, it always wins.

```go
func (r *Resolver) Resolve(name string) ResolveResult {
    // Walk scopes first (project → org → global). Secret store is authoritative.
    for _, scope := range r.Scopes {
        if val, src, ok := r.resolveScope(scope, name); ok {
            return ResolveResult{Value: val, Source: scope.Name, Backend: src, Found: ok}
        }
    }
    // Fall back to process environment.
    if val, ok := os.LookupEnv(name); ok {
        return ResolveResult{Value: val, Source: "env", Backend: "env", Found: true}
    }
    return ResolveResult{}
}
```

### 2. `internal/secret/validate.go` — IsolateCredentials + always inject

New function called after `ValidateSecrets`:

```go
// IsolateCredentials modifies ac.Env so that:
// - The resolved credential is overridden in the agent env
// - Competing alternatives are stripped (set to "") from the agent env
// Returns names of stripped vars for logging.
func IsolateCredentials(ac *runtime.AgentConfig, resolver *Resolver) []string {
    if resolver == nil { return nil }
    if ac.Env == nil { ac.Env = make(map[string]string) }
    var stripped []string
    for _, req := range ac.RequiredEnv {
        alternatives := strings.Split(req, "|")
        var resolvedKey, resolvedVal string
        for _, alt := range alternatives {
            alt = strings.TrimSpace(alt)
            if alt == "" { continue }
            result := resolver.Resolve(alt)
            if result.Found {
                resolvedKey, resolvedVal = alt, result.Value
                break
            }
        }
        if resolvedKey == "" { continue }
        ac.Env[resolvedKey] = resolvedVal
        for _, alt := range alternatives {
            alt = strings.TrimSpace(alt)
            if alt == "" || alt == resolvedKey { continue }
            if _, exists := os.LookupEnv(alt); exists {
                ac.Env[alt] = ""
                stripped = append(stripped, alt)
            }
        }
    }
    return stripped
}
```

In `resolveRequirement`, always inject (remove the `source != "env"` guard):

```go
if result.Found {
    _ = os.Setenv(alt, result.Value)  // always inject
    return true
}
```

### 3. `cmd/table.go` — Always validate + isolate

Remove container-only gate in `newRunner()`. Add to both `newRunner()` and `newRunnerFromAgent()`:

```go
resolver := secretResolver(env, secret.DefaultBackend())
if err := secret.ValidateSecrets(ac, resolver); err != nil {
    return nil, err
}
stripped := secret.IsolateCredentials(ac, resolver)
for _, key := range stripped {
    fmt.Fprintf(os.Stderr, "Notice: stripped %s from agent environment — using ateam secret\n", key)
}
```

### What Does NOT Change

- `defaults/runtime.hcl` — no config changes
- `internal/container/docker.go` — forward_env as-is
- `internal/container/docker_exec.go` — as-is
- `internal/agent/agent.go` — `buildProcessEnv` already handles `""` stripping

## Files to Modify

| File | Change |
|------|--------|
| `internal/secret/resolve.go:75-89` | Reverse priority: scopes first, env last |
| `internal/secret/validate.go` | Add `IsolateCredentials()`, always-inject in `resolveRequirement` |
| `cmd/table.go:111-119` | Remove container gate, add `IsolateCredentials` |
| `cmd/table.go:167-184` | Same for `newRunnerFromAgent` |
| `internal/secret/secret_test.go` | Tests for reversed priority, IsolateCredentials |

## Verification

1. `go build ./...` && `go test ./...`
2. Host: `ANTHROPIC_API_KEY=wrong` in shell + correct key via `ateam secret` → agent gets secret-store value
3. Host: no secret configured → falls back to env with notice
4. Docker: container has app's `ANTHROPIC_API_KEY`, mounted secrets.env has `CLAUDE_CODE_OAUTH_TOKEN` → agent uses ateam's token
5. `ateam run --dry-run` → shows credential resolution source
