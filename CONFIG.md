# ATeam Configuration

Configuration files and runtime config for ATeam. For command-line invocations, see [COMMANDS.md](COMMANDS.md). For sandbox and container setup, see [ISOLATION.md](ISOLATION.md).

## Directory Layout

Prompts compose via a 3-anchor chain: **project (`.ateam/`) → org (`.ateamorg/`) → embedded** (built into the ateam binary). The same filename at a more-specific anchor overrides; different filenames at any anchor compose additively (see [Prompt Composition](#prompt-composition) below).

### Organization: `.ateamorg/`

Created by `ateam install`. Holds shared defaults and org-level overrides.

```
.ateamorg/
  defaults/                                    # embedded defaults written to disk (kept in sync via `ateam update`)
    runtime.hcl                                # runtime config (agents, containers, profiles)
    Dockerfile                                 # default Dockerfile for container builds
    prompts/                                   # the v1 prompt tree (mirrors what the binary ships)
      _pre.context.md                          # root-level pre — applied to every prompt
      review.prompt.md                         # supervisor review body
      code_management.prompt.md                # supervisor code-management body
      code_verify.prompt.md                    # supervisor verify body
      auto_setup.prompt.md                     # auto-setup body
      exec_debug.prompt.md                     # inspect --auto-debug body
      report_auto_roles.prompt.md              # --auto-roles planner body
      code.prompt.md                           # implementer body piped into per-task `ateam exec`
      report/
        _pre.intro.md                          # dir-level pre — applied to every role/<R>.prompt.md
        _post.format.md                        # dir-level post — output format / validation
        <NAME>.prompt.md                       # per-role report bodies
  runtime.hcl                                  # org-level runtime config override (optional)
  Dockerfile                                   # org-level Dockerfile override (optional)
  prompts/                                     # org-level prompt overrides (any filename from defaults/prompts/)
    _pre.<NAME>.md                             # add an org-wide pre fragment to every prompt
    report/<NAME>.prompt.md                    # override a role's body
    report/<NAME>.post.<NAME>.md               # add a composable post fragment to a role
    code.post.extra.md                         # add a composable extra to the implementer body
    review.post.extra.md                       # add a composable extra to the supervisor review
    # … any other file matching the filename patterns defaults/prompts/ ships with
```

### Project: `.ateam/`

Created by `ateam init`. Self-contained: config, prompts, generated artifacts, and runtime state.

Versioned files are at the top level. Runtime artifacts (`logs/`, `runtime/`, `state.sqlite`, `secrets.env`) are gitignored.

```
.ateam/
  .gitignore                                   # excludes state.sqlite*, logs/, runtime/, secrets.env
  config.toml                                  # project configuration
  state.sqlite                                 # call database [gitignored]
  secrets.env                                  # project-scoped secrets [gitignored]
  runtime.hcl                                  # project-level runtime override (optional)

  prompts/                                     # project-level prompt overrides
    _pre.<NAME>.md                             # project-wide pre fragment
    code.post.<NAME>.md                        # project-wide implementer post fragment
    review.post.<NAME>.md                      # project-wide review post fragment
    report/<NAME>.prompt.md                    # role body override
    report/<NAME>.post.<NAME>.md               # composable role post fragment
    # … same filename patterns as .ateamorg/prompts/

  shared/                                      # cross-agent artifacts (gitignored or versioned per project policy)
    report/<NAME>/<NAME>.md                    # latest successful report per role
    review/review.md                           # latest successful supervisor review
    verify/verify.md                           # latest successful verification report
    auto_setup/auto_setup.md                   # auto-setup output
    code/<exec_id>/                            # per-exec code-session artifacts

  runtime/<exec_id>/                           # per-execution scratch [gitignored]
  logs/<exec_id>/                              # forensic logs (stream, stderr, cmd.md) [gitignored]
```

The auto-migrator upgrades pre-v1 layouts (`roles/`, `supervisor/`, `*_base_prompt.md`) to this shape on first contact. `ATEAM_NO_MIGRATE=1` opts out — not recommended unless you know why.

## `config.toml`

```toml
[project]
name = "myproject"

[git]
repo = "."
remote_origin_url = "git@github.com:org/repo.git"

[report]
max_parallel = 3
report_timeout_minutes = 20

[review]
timeout_minutes = 20

[code]
timeout_minutes = 120

[verify]
timeout_minutes = 30

[exec]
timeout_minutes = 60

# [serve]
# port = 8080  # fixed port for 'ateam serve' (default: random)

[roles]
"project.security" = "on"
"test.gaps" = "on"
"code.structure" = "off"

# [supervisor]
# code_profile = "cheap"          # use a cheaper model for coding sub-runs

# [profiles]
# [profiles.roles]
# "project.security" = "docker"    # run security reports in Docker
# critical_code_reviewer = "codex" # use codex agent for this role

# [sandbox-extra]
# allow_write = ["/path/to/extra/writable/dir"]
# allow_read = ["/path/to/extra/readable/dir"]
# allow_domains = ["api.example.com"]
# unsandboxed_commands = ["make:*"]
```

### `[sandbox-extra]`

Grant additional sandbox permissions to agents running in this project. These paths and domains are merged into the Claude sandbox settings alongside the defaults from `runtime.hcl`.

| Key | Description |
|-----|-------------|
| `allow_write` | Additional filesystem paths the agent can write to. Added to `sandbox.filesystem.allowWrite`. |
| `allow_read` | Additional filesystem paths the agent can read. Added to `sandbox.filesystem.allowRead`. |
| `allow_domains` | Additional network domains the agent can reach. Added to `sandbox.network.allowedDomains`. |
| `unsandboxed_commands` | Commands that bypass the sandbox entirely. Added to `sandbox.excludedCommands`. Use `name:*` for all subcommands (e.g. `make:*`, `docker:*`). |

All paths are absolute. Use `ateam env --claude-sandbox` to inspect the final merged sandbox settings.

```toml
[sandbox-extra]
allow_write = ["/data/output", "/tmp/scratch"]
allow_read = ["/opt/shared-tools"]
allow_domains = ["api.internal.example.com", "registry.npmjs.org"]
unsandboxed_commands = ["make:*", "docker:build"]
```

### `[supervisor]`

Configure which profiles the supervisor and code phases use.

| Key | Description |
|-----|-------------|
| `default_profile` | Default profile for supervisor actions (review, code management) |
| `review_profile` | Profile override for the review phase specifically |
| `code_profile` | Profile for code sub-runs (passed to `ateam exec --profile`) |
| `code_supervisor_profile` | Profile for the code management supervisor itself |

```toml
[supervisor]
code_profile = "cheap"              # use sonnet for coding sub-runs
code_supervisor_profile = "default" # use default for the supervisor
```

### `[profiles]`

Map individual roles to specific profiles or agents. This lets you run different roles with different agents or runtime configurations.

```toml
[profiles]
[profiles.roles]
"project.security" = "docker"     # run security reports in Docker
critical_code_reviewer = "codex"  # use codex agent for this role
"test.gaps" = "docker"            # if test discovery needs Docker for the project's runner
```

Values can be either a **profile name** (defined in `runtime.hcl`) or an **agent name** (also defined in `runtime.hcl`). When the value matches a known agent but not a profile, it's treated as an agent shorthand — equivalent to `--agent NAME` on the CLI.

**Profile resolution order** (first match wins):
1. CLI flag (`--profile` or `--agent`) — overrides everything
2. `[profiles.roles]` — per-role override from config.toml
3. `[supervisor]` action-specific profile (for review/code actions)
4. `[supervisor]` default_profile
5. `[project]` default_profile
6. Built-in `"default"` profile

See [Profiles](#profiles) under Runtime Configuration for `runtime.hcl` profile definitions.

### `[container-extra]`

Add extra Docker arguments, environment forwarding, or environment variables to container runs. Merged with the container definition from `runtime.hcl`.

| Key | Description |
|-----|-------------|
| `extra_args` | Additional `docker run` flags (e.g., `["--cpus=2", "--memory=4g"]`) |
| `forward_env` | Additional env var names to forward into containers |
| `env` | Static env vars to set inside containers (key-value map) |

```toml
[container-extra]
extra_args = ["--cpus=2"]
forward_env = ["MY_CUSTOM_TOKEN"]
```

### Custom Roles

Create a custom role by adding a `<NAME>.prompt.md` file under `prompts/report/` — no config.toml registration needed:

```bash
mkdir -p .ateam/prompts/report
# write your prompt
vim .ateam/prompts/report/my_custom_role.prompt.md
# run it
ateam report --roles my_custom_role
```

Roles are discovered from the union of:
- Built-in defaults (embedded in the binary, under `defaults/prompts/report/`)
- `config.toml` `[roles]` entries
- `.ateamorg/prompts/report/<NAME>.prompt.md` (org-level, shared across projects)
- `.ateam/prompts/report/<NAME>.prompt.md` (project-level)

Roles are opt-in: only roles explicitly listed in `config.toml` with status `on` (or the legacy `enabled`) are included in `--roles all`. Unlisted roles — built-in or custom — and roles set to `off` are excluded from the `all` expansion. They can still be run by naming them directly: `ateam report --roles my_custom_role` works without any `config.toml` entry as long as the role's `.prompt.md` file exists.

## Prompt Composition

Prompts are assembled by walking the **project → org → embedded** anchor chain and composing files by filename pattern. There is no template file to author — the assembly order is encoded in the names.

### Filename Patterns

| Pattern | Means |
|---|---|
| `<role>.prompt.md` | Role main body. First-match wins across anchors (most-specific overrides). |
| `<role>.pre.<NAME>.md` | Role pre fragment named `<NAME>` (additive — composes with other `<role>.pre.*.md`). |
| `<role>.post.<NAME>.md` | Role post fragment (additive). |
| `_pre.<NAME>.md` | Dir-level pre — applied to every role in this directory (additive). |
| `_post.<NAME>.md` | Dir-level post (additive). |

### Assembly Order

For `prompts/report/security`:

```
[--pre-prompt]                          (CLI, outermost head)
  _pre.<NAME>.md                        (root-level pre — every prompt)
    report/_pre.<NAME>.md               (dir-level pre — every report role)
      report/security.pre.<NAME>.md     (role-level pre fragments)
      report/security.prompt.md         (role main — first match wins)
      report/security.post.<NAME>.md    (role-level post fragments)
    report/_post.<NAME>.md              (dir-level post — every report role)
  _post.<NAME>.md                       (root-level post)
[--post-prompt]                         (CLI, outermost tail)
```

### Inspecting Assembly

```bash
ateam prompt --role ROLE --action report                  # print the assembled prompt
ateam prompt --role ROLE --action report --paths          # tabular per-section breakdown
ateam prompt --role ROLE --action report --inline-paths   # full prompt with per-section headers
ateam prompt --action review --paths                      # supervisor singletons take just --action
```

### `@PATH` dispatch (exec / parallel / prompt)

`ateam exec @PATH`, `ateam parallel @PATH`, and `ateam prompt @PATH` route the input through one of three Prompt implementations based on the file's suffix and `--raw`:

| Input | Implementation | What it does |
|---|---|---|
| `--raw` + anything | `RawTextPrompt` | Bytes-through: no template engine, no anchor walk. The agent sees the file verbatim. |
| `@foo.prompt.md` (path separator or leading `.`) | `PromptFile` (filesystem mode) | The file's parent dir is injected as a temporary anchor at the front of the standard chain. Sibling `foo.pre.*.md` / `foo.post.*.md` / dir-level `_pre.*.md` / `_post.*.md` compose around the body. Engine expands `{{namespace.key}}` directives. |
| `@foo.md` (any non-`.prompt.md` file) | `PromptText` | The file body runs through the template engine (vars + dynamics) but does NOT compose with any anchor framing. |
| `@-` (stdin) | `PromptText` | Same as `@foo.md`: engine-expand only, no framing. |
| Bare literal text (`ateam exec "hello"`) | `PromptText` | Same: engine-expand only. |

For role-scoped prompts (`ateam prompt --role X --action Y`) and supervisor singletons (`ateam prompt --action review`) the dispatch is implicit — those always go through `PromptFile` against the standard anchor chain (no temp injection).

### Common Override Patterns

**Add an instruction to every prompt without touching the embedded defaults:**

```bash
echo "Pay extra attention to memory safety." > .ateam/prompts/_pre.memory.md
```

**Add a role-specific note that survives upgrades:**

```bash
echo "For this project, treat any C extensions as untrusted." \
  > .ateam/prompts/report/project.security.post.notes.md
```

**Override a role wholesale (drift risk on upgrade):**

```bash
# Edit `.ateam/prompts/report/project.security.prompt.md` directly.
# Better: fork it under a new name (project_security_strict.prompt.md)
# so embedded improvements still land for the original.
```

### Template Variables

Prompts reference `{{namespace.key}}` directives. The resolver is **closed-set**: an unknown namespace passes through verbatim (so `{{foo.bar}}` in a code block survives unchanged), but a known namespace with an unknown key is a hard error (catches typos before the prompt reaches the agent). Only the dotted form is recognized in prompt bodies — legacy ALL_CAPS tokens (`{{OUTPUT_DIR}}`, `{{ROLE}}`, etc.) are no longer auto-translated and will reach the agent verbatim if left in a prompt file; rewrite project / org prompt overrides to the dotted form below. The ALL_CAPS aliases still work in `runtime.hcl` (agent args, container fields) — see the [runtime template variables](#available-variables) section.

| Namespace | Resolved by | Keys |
|---|---|---|
| `prompt.*` | assembler (per-prompt) | `name`, `path`, `action` |
| `project.*` | assembler | `name`, `root`, `full_path`, `dir` |
| `git.*` | assembler | `repo`, `branch`, `commit`, `head_short`, `dirty` |
| `container.*` | assembler | `type`, `name` |
| `ateam.*` | assembler | `own_readme`, `own_commands`, `own_config`, `own_isolation`, `own_roles`, `auto_roles_marker` |
| `role.*` | assembler | `reports` |
| `args.*` | factory | factory-curated CLI-flag values, e.g. `args.ignore_previous_report` for `ateam report --ignore-previous-report` |
| `roles.*` | env | `enabled` — comma-separated list of enabled role IDs from project config |
| `exec.*` | runtime (`flow.Runtime`) | `id`, `batch`, `output_dir`, `output_file`, `prompt_file`, `timestamp`, `profile`, `agent`, `model`, `effort`, `max_budget_usd`, `max_budget_usd_batch`, `subrun_args`, `debug_context`, `auto_roles_commands_output` |
| `env.NAME` | OS environment | Reads from the agent process's environment at assembly time. Unset variables error loudly. |
| `dynamic.NAME [args...]` | factory dynamics | Resolved by a registered function on the bundle. Shipped: `project_info`, `review_reports`, `code_mgmt_review`, `previous_report`. |

`{{git.*}}` returns the empty string (or `"false"` for `git.dirty`) when the work-dir isn't inside a git repo, so prompts using them keep rendering cleanly in scratch contexts.

`{{exec.*}}` is the **runtime namespace** — values are allocated by the runner's `Prepare` step (exec ID, output paths) or sourced from the verb factory (batch, profile, model, …). In live runs they render as their real values; in preview / verify / `ateam prompt --action X`, they render as the literal sentinel `{{AT RUNTIME:exec.<key>}}` so operators can see the structure without an exec actually allocated.

`{{args.*}}` and `{{roles.*}}` are the **factory-curated namespaces** — the verb's CLI surface exposes specific values into the prompt without leaking the whole option struct. Today only `args.ignore_previous_report` and `roles.enabled` are populated; verb factories add more on demand. See `internal/prompts/assembler/template.go::MapVars` for the resolver and `internal/root/resolve.go::BuildAssemblerVars` for where the env-shaped namespaces are seeded.

`{{dynamic.NAME}}` is the **late-binding namespace** — a registered function on the bundle returns a string when the directive expands. Dynamics that depend on artifacts generated by a previous step (e.g. `review_reports` reads the role reports written by `ateam report`) return a preview sentinel in `ModePreview` rather than reading disk, so `ateam prompt --action review` is deterministic before any reports exist.

### Dynamics

The shipped dynamics. Each is a stability commitment to prompt authors — args contract, output shape, side effects, and mode behavior are pinned. Wire is `{{dynamic.NAME [args...]}}`.

| Dynamic | Args | Output | Side effects | Mode behavior |
|---|---|---|---|---|
| `project_info` | none | The `# ATeam Project Context` block (project name, role, working dir, repo/branch facts, optional `Quick Orientation` Markdown). Empty string when `roleLabel` is `""` (i.e. `--no-project-info`). | Reads project config + git from `env.WorkDir`. Idempotent within a run (cached on the resolved env). | Mode-agnostic — real data in both `ModeReal` and `ModePreview`. |
| `review_reports` | none | A reports manifest table + bundled role-report bodies (`# Reports Under Review` table + `# Role Reports` sections). Matches `formatReportsBlock` exactly. | Reads `.ateam/shared/report/*.md` from disk. | `ModeReal`: real block. `ModePreview`: sentinel `{{AT RUNTIME: review reports manifest}}` (no disk read). |
| `code_mgmt_review` | none | The review block the code-management supervisor consumes: `# Review\n\n<body>`. Body source is the operator-supplied `--review @file` if set, otherwise `.ateam/shared/review.md`. | Reads disk in `ModeReal`. | `ModeReal`: real block. `ModePreview`: sentinel `{{AT RUNTIME: code-management review block}}`. |
| `previous_report` | none | The `# Previous Report` block for the role's prior cycle, or the `# Prior Report Status` "no prior" sentinel when the file is missing or empty. Returns `""` when the factory captured `SkipPreviousReport` (i.e. `--ignore-previous-report`). | Reads `env.RoleReportPath(roleID)`. | Mode-agnostic — the prior report is a stable input from a previous cycle, like `project_info`. |

Operator-authored dynamics: not supported today. Prompts only expand the shipped names above; `{{dynamic.unknown}}` errors at render with a no-dispatcher message. Adding new dynamics requires a code change in `cmd/<action>_factory.go` (factory registers the closure) — see [DEV.md → Adding a new action](DEV.md#adding-a-new-action).

Positional argument dispatch (e.g. `{{dynamic.review_reports security code.bugs}}`) is a documented spec extension; today's shipped dynamics ignore their args (the filtering / lookup state lives in the closure the factory captures). See `plans/feature_prompt_cmd_bundle_aware.md` "Future" section.

## Runtime Configuration

Runtime behavior is configured via `runtime.hcl` files using HCL syntax, with a 4-level resolution:

1. **Built-in defaults** — compiled into the binary
2. **Org defaults** — `.ateamorg/defaults/runtime.hcl`
3. **Org override** — `.ateamorg/runtime.hcl`
4. **Project override** — `.ateam/runtime.hcl`

Use `ateam env` to see the active resolution chain.

### Top-level scalars

| Field | Type | Default | Description |
|---|---|---|---|
| `sandbox_detection` | bool | `false` | Auto-detect when ateam is inside an outer non-container sandbox and treat it as a container so the agent's inner sandbox is skipped. Signal-driven: macOS Seatbelt probe, Linux `/proc` heuristics (user-namespace divergence, Seccomp, NoNewPrivs), cooperative env vars (`FENCE_SANDBOX`, `FIREJAIL_NAME`, `container=…`). Default off because the signals can have false positives that would silently drop defense in depth — opt in when knowingly running under any outer sandbox. See [ISOLATION.md](ISOLATION.md#detection-of-an-outer-sandbox-or-container). |
| `docker_detection` | bool | `true`  | Auto-detect `/.dockerenv` and `/run/.containerenv`. Default true (markers are reliable). Set false to exercise the host-execution code path from inside a container. |

Both can be overridden per-invocation with `--sandbox-detection true|false` / `--docker-detection true|false`.

### Agents

```hcl
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
  required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet"]
}
```

Agents support inheritance via `base`, sandbox settings, environment variables, isolated config dirs, and `required_env` for secret validation. When alternatives are declared (e.g., `required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]`), the first alternative in declaration order wins at each tier (store first, then env). Competing alternatives are stripped from the agent's process environment to avoid credential confusion. See [`ateam secret`](COMMANDS.md#ateam-secret) for the full resolution order.

`max_budget_usd = "<USD>"` can be set on an agent block as a default spend cap (claude only; codex ignores it). The CLI `--max-budget-usd` flag overrides the per-agent default for a single invocation.

#### `codex-tmux` (experimental)

`codex-tmux` drives the interactive Codex CLI through a detached `tmux` session and captures its output as a normal ateam run. It's an **experiment** — primarily to get experience with tmux-based agent wrappers, and to make Codex's TUI-only slash commands (`/review` in particular) usable from unattended pipelines that today rely on `claude -p` / `codex exec` for headless work.

```hcl
agent "codex-tmux" {
  base    = "codex"
  type    = "codex-tmux"
  command = "codex"
  model   = "gpt-5.5"
  effort  = "xhigh"

  start_timeout     = "15s"
  busy_timeout      = "20m"
  quiescence_window = "2s"
  tmux_width        = 300
  tmux_height       = 100
}

profile "codex-tmux" {
  agent     = "codex-tmux"
  container = "none"
}
```

**Example — running `/review` unattended**:

```sh
# Works: codex's /review accepts inline scope arguments, runs the review
# directly and produces output back to ateam.
ateam exec "/review the pending changes" --agent codex-tmux
```

**Interactive submenus — two ways to handle them**:

Some slash commands (notably bare `/review`) open an interactive preset picker before they run. There are two ways to get past that:

```sh
# Option A — pass the scope inline so codex bypasses the picker entirely:
ateam exec "/review the pending changes" --agent codex-tmux
ateam exec "/review the staged changes"  --agent codex-tmux
ateam exec "/review HEAD~3..HEAD"        --agent codex-tmux

# Option B — multi-line prompt where lines 2+ are tmux keystrokes that
# navigate the picker. First line is the slash command; each subsequent
# non-empty line is one `tmux send-keys` call with whitespace-split keys
# (literals like `2` or `y`, named keys like `Enter`/`Down`/`Tab`/`Esc`).
# Each step is run after the previous state settles. This works for any
# slash command whose flow is a fixed keystroke sequence.

# Example: select option 2 ("Review uncommitted changes") from /review's picker:
ateam exec "$(printf '/review\n2 Enter\n')" --agent codex-tmux

# Example: arrow-key navigation (when the menu lacks numeric shortcuts):
ateam exec "$(printf '/review\nDown Down Enter\n')" --agent codex-tmux
```

Empty lines in the multi-line prompt are ignored. Option A is preferred when the slash command accepts inline arguments (no fragile keystroke choreography); Option B is the escape hatch for commands that genuinely require menu interaction.

**Constraints**:
- **Host-only** — pairing with `container != "none"` is rejected at runner construction. `codex-tmux` needs a project (`.ateam/` directory) for its tmux socket; running outside a project errors with actionable guidance.
- **Auth** — reuses your existing `~/.codex/auth.json` natively. ateam does not stage a custom `CODEX_HOME`. The first run in a new workdir auto-accepts codex's trust dialog and persists one `[projects."<workdir>"]` entry in your `~/.codex/config.toml` — same outcome as a hand-typed `codex` session.
- **Token tracking** — costs and token usage are mined from `~/.codex/sessions/<date>/rollout-*.jsonl` after the run.
- **Concurrency** — multiple `codex-tmux` runs in different projects are isolated by EXEC_ID-based sockets and session names; concurrent runs in the same workdir use an EXEC_ID-tagged marker in the prompt body to keep their token stats from swapping. For slash-command prompts (where no marker can be injected), concurrent runs in the same workdir may misattribute token stats — open the issue if you hit it.

See `plans/feature_codex_tmux_agent.md` for the design rationale.

### Effort levels

Each agent block accepts an optional `effort = "..."` field that controls the underlying CLI's reasoning depth. The string is passed through verbatim, so `ateam` does not need to be updated when agents add new levels. CLI flags (`--effort` on `exec`, `code`, `report`, `parallel`) override the per-agent default for a single invocation.

```hcl
agent "claude-sonnet" {
  base   = "claude"
  effort = "high"
}
```

Per-agent value sets at time of writing:

| Agent | Native flag | Accepted values |
|---|---|---|
| Claude Code | `--effort LEVEL` | `low`, `medium`, `high`, `xhigh`, `max`, `auto` |
| OpenAI Codex | `-c model_reasoning_effort=LEVEL` | `minimal`, `low`, `medium`, `high`, `xhigh` |

Future agents may accept different values. `ateam` does not validate; an invalid level fails at the underlying CLI, surfacing in the run summary.

### Template Variables

Agent args, profile extra args, container config fields, and agent env values support `{{VAR}}` placeholder substitution. Variables are resolved at execution time by the runner.

#### Available Variables

| Variable | Description | Example value |
|---|---|---|
| `{{PROJECT_NAME}}` | Project name from `config.toml` | `myproject` |
| `{{PROJECT_FULL_PATH}}` | Absolute path to project root | `/home/user/projects/myproject` |
| `{{PROJECT_DIR}}` | Last component of the project path | `myproject` |
| `{{ROLE}}` | Role ID | `security`, `supervisor` |
| `{{ACTION}}` | Action type | `report`, `run`, `code`, `review` |
| `{{BATCH}}` | Batch ID | `code-2026-03-31_06-09-39` |
| `{{TIMESTAMP}}` | Run start time | `2026-03-31_06-09-39` |
| `{{PROFILE}}` | Active profile name | `docker`, `default` |
| `{{EXEC_ID}}` | Call tracking ID (visible in `ateam ps`) | `42` |
| `{{AGENT}}` | Agent config name | `claude`, `claude-docker` |
| `{{MODEL}}` | Resolved model name | `sonnet`, `haiku` |
| `{{CONTAINER_TYPE}}` | Container type | `none`, `docker`, `docker-exec` |
| `{{CONTAINER_NAME}}` | Docker container name | `ateam-myapp-security` |
| `{{OUTPUT_DIR}}` | Absolute path to the agent's writable output directory for this run (`.ateam/runtime/<exec_id>/`) | `/proj/.ateam/runtime/42` |
| `{{OUTPUT_FILE}}` | Absolute path to the primary output file for the current action (e.g. the role's `report.md`) | `/proj/.ateam/runtime/42/report.md` |

Unknown variables are left as-is (e.g. `{{UNKNOWN}}` passes through unchanged). When `EXEC_ID` is 0 (no DB tracking), it resolves to an empty string.

#### Where Templates Are Resolved

| Config field | Location | Templates supported |
|---|---|---|
| `agent.args` | `runtime.hcl` | Yes |
| `agent.env` values | `runtime.hcl` | Yes |
| `agent.config_dir` | `runtime.hcl` | Yes |
| `profile.agent_extra_args` | `runtime.hcl` | Yes |
| `profile.container_extra_args` | `runtime.hcl` | Yes (via Docker ExtraArgs) |
| Docker `ContainerName` | Computed at build time | Yes |
| Docker `ExtraVolumes` | `runtime.hcl` container config | Yes |
| Docker `Env` values | `config.toml` `[container-extra]` | Yes |
| `agent.args_inside_container` | `runtime.hcl` | Yes |
| `agent.args_outside_container` | `runtime.hcl` | Yes |
| docker-exec `docker_container` | `runtime.hcl` | Yes |
| docker-exec `WorkDir` | Computed from project paths | Yes |

Templates are **not** resolved in:
- Prompt files — these use the prompt engine's dotted `{{namespace.key}}` form documented in [Template Variables](#template-variables); the runtime.hcl ALL_CAPS aliases described in this section do not apply
- Sandbox settings JSON (has its own merge mechanism)
- Dockerfile paths (use the role-based resolution chain instead)
- Precheck command args — `{{CONTAINER_NAME}}` is expanded at execution time by `RunPrecheck`, not by the general template system
- `forward_env` key names (these are env var names, not values)
- Map keys in `env` blocks (only values are resolved)
- docker-exec `exec` template — uses its own `{{CONTAINER}}` and `{{CMD}}` placeholders which are expanded at execution time by the CmdFactory (separate from general template vars)

#### Examples

Session naming for agent runs:

```hcl
agent "claude" {
  args = ["-p", "--output-format", "stream-json", "--verbose",
          "--name", "{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}"]
}
```

Per-role Claude config directory:

```hcl
agent "claude-isolated" {
  base       = "claude"
  config_dir = ".claude-{{ROLE}}"
}
```

Custom Docker hostname per role:

```hcl
profile "docker" {
  agent              = "claude-docker"
  container          = "docker"
  container_extra_args = ["--hostname", "ateam-{{PROJECT_DIR}}-{{ROLE}}"]
}
```

Passing role context as environment variables:

```hcl
agent "claude" {
  env = {
    ATEAM_ROLE   = "{{ROLE}}"
    ATEAM_ACTION = "{{ACTION}}"
  }
}
```

### Cost Estimation & Pricing

- **Claude** reports actual cost directly in its stream output. No configuration needed.
- **Codex** (and other agents) rely on a `pricing` block in `runtime.hcl` to estimate cost from token counts.

```hcl
agent "codex" {
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
```

Pricing can go stale. Override at any level by redefining the agent's `pricing` block in the appropriate `runtime.hcl`.

### Containers

Three container types are supported: `none`, `docker` (one-shot), and `docker-exec` (exec into a user-managed container).

```hcl
container "docker" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true     # mount ~/.claude/.credentials.json read-only (OAuth)
  forward_env         = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
}
```

`mount_claude_config = true` bind-mounts the host's `~/.claude/.credentials.json` into the container as read-only — required for OAuth-token auth that needs the credential store. Stateless API-key auth (`docker-api` profile) does not need it. See [ISOLATION.md](ISOLATION.md) for the full mount layout.

`docker-exec` runs agents inside a long-lived user-managed container (docker-compose service, devcontainer, manually started, etc.). The container name resolves from `--container-name` flag → `ateam secret CONTAINER_NAME --scope project` → `CONTAINER_NAME` env var → the `docker_container` field. A built-in `docker-exec` profile uses `{{CONTAINER_NAME}}` for secret-based resolution.

```hcl
container "my-app" {
  type             = "docker-exec"
  docker_container = "my-app-dev"             # or "{{CONTAINER_NAME}}" for secret-based
  precheck         = ["sh", "precheck.sh", "{{CONTAINER_NAME}}"]  # command array
  forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
  copy_ateam       = true                     # copy ateam binary into container
  # exec           = "podman exec {{CONTAINER}} {{CMD}}"
}
```

The `precheck` field takes a command array. `{{CONTAINER_NAME}}` in args is replaced with the resolved container name at execution time. Convention-discovered scripts (`.ateam/docker-agent-precheck.sh`) are auto-wrapped as `["sh", "<path>", "{{CONTAINER_NAME}}"]`.

**Devcontainers** are supported as a use case of `docker-exec`: write a precheck that runs `devcontainer up --workspace-folder .` to ensure the container exists, then ateam `docker exec`s into it like any other long-lived container. See [ISOLATION.md](ISOLATION.md) for the worked example. Requires `@devcontainers/cli` on the host.

For authentication setup inside containers, see [ISOLATION.md](ISOLATION.md).

### Profiles

Profiles combine an agent and a container:

```hcl
profile "default" {
  agent     = "claude"
  container = "none"
}

profile "docker" {
  agent     = "claude"
  container = "docker"
}

profile "docker-exec" {
  agent     = "claude"
  container = "docker-exec"
}

profile "cheap" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "sonnet", "--max-budget-usd", "0.50"]
}
```

Use `--profile docker` to run in a fresh ateam-managed container, `--profile docker-exec` to exec into a user-managed long-lived container, or `--profile cheap` for cheaper runs. See `defaults/runtime.hcl` for the full list of built-in profiles.

For the resolution order when multiple profile selectors apply, see [Profile resolution order](#profiles-1) in the `[profiles]` config.toml section.

#### Running the supervisor inside Docker

Typically only coding agents need to run inside docker so they can build and run tests in an isolated environment. Basic docker config from [README.md](README.md) is enough. But if you want the supervisor itself to run in docker and launch ateam coding agents then a Linux build of ateam must be available inside of docker. Cross-compile the Linux companion binary:

```bash
make companion    # produces build/ateam-linux-<arch>
```

The binary is automatically found by ateam from `build/`. For installations without a git checkout, place `ateam-linux-<arch>` next to the host `ateam` binary.

For complete Docker setup including secrets, precheck scripts, and interactive sessions, see [ISOLATION.md](ISOLATION.md).
