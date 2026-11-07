# Feature: Traceable Config — model/effort lineage reporting (audit first)

## Goal

Make it easy to answer, before spending money: **which model and effort will
each agent actually run with, and which config layer decided that?**
Surface the answer in `--dry-run` output and `ateam env`. Audit/reporting is
step one; warnings ("you're about to run a full pipeline on an expensive
model") and a guided config command build on it later.

## Background: how model/effort get decided today

The effective model for a run is the result of up to five layers. Nothing
records which layer won; the truth only shows up at runtime in the stream's
`init` event (`resolveExecModel`, `cmd/table.go`).

1. **runtime.hcl, 4-level layering** (`internal/runtime/config.go:168`,
   `Load`): embedded defaults → `orgDir/defaults/runtime.hcl` →
   `orgDir/runtime.hcl` → `projectDir/runtime.hcl`. Later files overload
   same-name `agent`/`profile` blocks; `agent.base` inheritance copies unset
   `model`/`effort` from the base agent (`config.go:263`). Two gotchas:
   - `mergeHCL` collapses layers, so **provenance is lost at parse time**.
   - some agent blocks embed `--model` in `args` directly (e.g.
     `claude-sonnet` in `defaults/runtime.hcl:283`) instead of the `model`
     attribute. `claudeArgs`/`codexFlagArgs` (`internal/agent/agent.go`)
     append the `model`-attr flag *after* base args, so the attr wins at the
     agent CLI (last flag wins), but a reader of the HCL can't tell.

2. **config.toml (project TOML, `internal/config`)** decides *which profile*
   (and therefore which agent block) is used per context:
   `project.default_profile`, `supervisor.{default,review,code,code_supervisor}_profile`,
   and the `[profiles.roles]` role→profile map. It never sets model/effort
   itself, but it's the layer that explains "why is the security report on
   haiku while verify is on opus".

3. **CLI flags**: `--model` / `--cheaper-model` / `--effort`
   (`applyModelOverrides` + `applyEffort`, `cmd/table.go:999`) call
   `Agent.SetModel`/`SetEffort`, which occupy the same argv slot as the HCL
   `model`/`effort` attrs — CLI wins over HCL.

4. **Agent-native config** — invisible to ateam today:
   - **claude**: `settings.json` (`model` key) under the *effective* config
     dir. That dir is itself resolved: agent `config_dir` in runtime.hcl
     (relative → under `.ateam/`, see `buildRequest`,
     `internal/runner/runner.go`) → else `$CLAUDE_CONFIG_DIR` → else
     `~/.claude` (`agent.ClaudeConfigDir`, `internal/agent/claude_auth.go:66`).
     Env (`ANTHROPIC_MODEL`) can also intervene.
   - **codex**: `$CODEX_HOME/config.toml` (`model`,
     `model_reasoning_effort`); codex-tmux reads `~/.codex` natively.
   - Container profiles read whatever config was mounted/copied into the
     container (`agent-config --copy-in`), which may differ from the host's.

5. **Left defaulted**: with none of the above, the agent CLI picks its own
   default (claude: account/CLI default). ateam's `pricing.default_model` is
   only a *pricing assumption*, not what runs — a known trap when they drift.

Believed precedence per agent (to be verified against the agent CLIs during
implementation, and encoded in one place):

    claude: CLI --model/--effort  >  hcl agent attr  >  --model inside hcl args
            >  settings.json / env  >  CLI account default
    codex:  CLI flags  >  hcl attr  >  hcl args  >  $CODEX_HOME/config.toml  >  codex default

Existing related surfaces: `ateam env` already prints the runtime.hcl
resolution chain (`printRuntimeSection`, `cmd/env.go`); `ateam agent-config
--audit` (`cmd/agent_config.go`) audits *auth* state only;
`Agent.DebugCommandArgs` returns the final argv for verbose mode.

## Design

### 1. Native-config parsing behind a per-agent interface

New optional interface in `internal/agent`, implemented per agent type
(pattern mirrors `ModelProvider`/`EnvProvider`):

```go
// NativeConfig is what the agent's own configuration would contribute
// if ateam passed no model/effort flags.
type NativeConfig struct {
    Model, Effort string
    Source        string // file that set it, e.g. "~/.claude/settings.json"
}

type NativeConfigReader interface {
    // env carries the per-run overrides (CLAUDE_CONFIG_DIR, CODEX_HOME)
    // so the reader inspects the same config the run would use.
    ReadNativeConfig(env map[string]string) (NativeConfig, error)
}
```

- `ClaudeAgent`: resolve config dir via the same chain as `buildRequest` +
  `ClaudeConfigDir()`, parse `settings.json`, note `ANTHROPIC_MODEL` env.
- `CodexAgent`/`CodexTmuxAgent`: parse `$CODEX_HOME/config.toml`.
- `MockAgent`: zero value.
- New agent types implement it alongside `Agent`; absence just means the
  "agent-native" row shows "(not inspectable)".

Read-only, best-effort: parse failures degrade to a note, never an error.

### 2. Provenance in runtime.hcl loading

`runtime.Load` gains an optional provenance record: for each agent block,
the last layer file that set `model`, `effort`, and whether `args` embeds a
`--model`/`-m`/`model_reasoning_effort` token (simple scan of the merged
args). Cheapest implementation: `mergeHCL` already knows the source label
per file; record `map[agentName]FieldSource` as it merges. No behavior
change to resolution itself.

### 3. A single resolver producing a layered trace, not just the winner

The API's core value is the **full chain**: every layer in precedence order
with what it contributes (including "nothing"), so callers can render the
whole story and any UI choice is a pure formatting concern:

```go
// One layer's contribution to the model/effort decision. Empty fields mean
// "this layer is silent on that slot".
type Contribution struct {
    Layer  string // "agent default" | "org defaults runtime.hcl" | "org runtime.hcl" | "project runtime.hcl" | "hcl args" | "settings.json" | "env" | "cli"
    Source string // concrete file path or flag, e.g. "~/org/.ateamorg/runtime.hcl" or "--effort"
    Model  string
    Effort string
    Note   string // e.g. "overloaded by project layer", "args embed --model sonnet (loses to model attr)"
}

type Attribution struct{ Value, Source string }

type ModelReport struct {
    Profile, Agent string
    ProfileSource  string         // "config.toml supervisor.code_profile" | "--profile" | ...
    Chain          []Contribution // ordered lowest → highest precedence, silent layers included
    Model, Effort  Attribution    // derived: fold over Chain, last non-empty wins per slot
    Warnings       []string
}
```

`Model`/`Effort` are computed by folding `Chain` — the precedence table
lives only in the fold and in the ordering of `Chain`. Renderers indent the
chain to show the layering, e.g.:

    agent: claude-high
      agent default: (nothing specified)
        org runtime.hcl: (overloaded by project)
          project runtime.hcl: model: opus-4.7, effort: high
            cli: effort: xhigh
    final: model opus-4.7 (project runtime.hcl), effort xhigh (cli)

Inputs: the resolved profile/agent (from `buildRunner`'s spec), CLI
overrides, provenance from (2), native config from (1). This is pure
computation over already-loaded data — one function, unit-testable without
running agents.

### 4. Surfaces (audit deliverables)

Two renderings of the same `ModelReport`:
- compact: `model: opus-4.7 (project runtime.hcl) · effort: xhigh (cli)`
- full chain: the indented per-layer view shown above.

- **`--dry-run`** (report/review/code/verify/exec): after the prompt, the
  full chain per runner — dry-run exists to answer exactly this question.
  Report dry-run prints one chain per role (roles can map to different
  profiles); collapse identical chains.
- **`ateam env`**: new subsection under Runtime listing the profiles that
  matter for this project (default, supervisor.{review,code,code_supervisor},
  role overrides) — compact line each, `env --models` (or similar) for the
  full chains.
- Optionally fold native-config rows into `agent-config --audit` since it
  already shows per-config-dir state.

## Plan (small steps)

1. Provenance capture in `internal/runtime` merge (no behavior change) + tests.
2. `NativeConfigReader` + claude/codex implementations + tests
   (fixture config dirs under `test_data/`).
3. `ModelReport` resolver + precedence tests (including the args-embedded
   `--model` vs attr case, base-agent inheritance, cheaper-model).
4. Wire into `--dry-run` outputs; `make test-cli` coverage.
5. Wire into `ateam env`; update COMMANDS.md.

## Later (out of scope for the audit step)

- **Cost warnings**: join `ModelReport` with the pricing tables
  (`AgentPricing`) and warn at launch when a full run (`ateam all`, report
  fan-out) resolves to a model above a configurable $/MTok threshold —
  precedence data makes the warning actionable ("set X in project
  runtime.hcl to override").
- **Guided config**: `ateam configure-model` writing an org- or
  project-level runtime.hcl override per action/role, using the same
  resolver to show before/after.

## Non-goals

- Changing any resolution behavior — audit only.
- Guaranteeing the agent CLI's internal default (we report "agent default"
  when nothing else claims the slot; the stream `init` event stays the
  ground truth after the fact).
