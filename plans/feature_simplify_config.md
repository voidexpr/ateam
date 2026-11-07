# Feature: Simplify configuration

Companion to [feature_traceable_config.md](feature_traceable_config.md).
That plan makes the config *observable* (which layer set the model).
This plan makes the config *harder to get wrong* and reduces the number
of files, directories and rules. The two will be blended: provenance
capture belongs in whatever loader comes out of step 6 below, and the
`ateam env` rework in step 5 is where the traceable-config rendering
lands.

## Status

Items 1 to 5 of the order of work are implemented (per-field merge with
provenance, model warnings, `ateam login`, `ateam env` / `--dry-run`
chains, and the single `ateam.hcl` with `.ateam`-only layering, the
config/state split and name-based project identity). The single central
state db stays an open choice; per-project `state.sqlite` is kept.
Containers see the home and project layers only; ancestor layers are not
mounted (known limitation). Items 6 (`ateam setup`, the config-dir part of step 7), 7 (native config
reader) and 8 (suggested default model) are implemented. Every item of
the order of work is done; what remains is the local migration below.

Cleanup pending on the author's machine (the sandbox cannot write
`~/.ateam`); to do by hand, in this order:

1. Run `ATEAM=./ateam scripts/migrate-ateamorg.sh`. Until then every
   `ateam` command on this machine stops with the legacy-layout error.
   This repo's own `.ateam/` is already converted (old files kept under
   `.ateam/cache/migrated/`); the script converts the five other
   registered projects and registers all six.
2. Tidy `~/.ateam/ateam.hcl` into the per-field form: the org file pins
   `--model claude-opus-5-5` inside `args` on three agents, which
   `ateam env` flags; one `model` attribute on `claude-high` replaces
   them. `ateam setup --model-choice suggested` pins `claude-sonnet-5-5`
   for the base `claude` agent if wanted.
3. Bump `toolchain` in go.mod to go1.26.9 (or newer) on a machine with
   network access: the `vuln` step of `make run-ci` fails on
   standard-library advisories fixed there; the sandbox could not
   download the toolchain.

## Decisions

- **Breaking upgrade, no backward compatibility.** One release renames
  directories and file formats. No dual-read of legacy files, no
  `migrate-config` command. The release notes carry a short manual
  recipe (move `.ateamorg` → `~/.ateam`, convert `config.toml` by
  hand). ateam prints one clear error when it meets a legacy layout
  (`.ateamorg/`, `config.toml`, `defaults/`). The pre-v1 migrators
  (`internal/migrate`, `root/migrate_logs.go`, `root/shim.go`) go in
  the same release.
- **`.ateamorg` is renamed to `.ateam`.** One directory name. See
  step 6 for the discovery rules that make this unambiguous.
- **Project identity is the project name, not the path.** Today
  `ProjectID()` is the project path relative to the org root (parent
  of `.ateamorg`), encoded with `_`. With `~/.ateam` as the only org,
  a project outside `$HOME` has no such path (`../` is rejected by
  `ValidateProjectPath`). The name becomes the identity: required,
  validated (`[a-z0-9._-]`), unique across the registry. The db
  `project_id` column holds it. This is also what allows one central
  state db later (step 6, "single db" option).
- **Provenance covers every decision, from the start.** Not only
  which layer set `model` / `effort` on an agent block, but also why
  that agent block was chosen: the `config.toml` profile selection
  (`[profiles.roles]`, `supervisor.*_profile`, `project.default_profile`,
  built-in `"default"`) and the `--profile` flag. `ResolveProfile` /
  `ResolveSupervisorProfile` return the winning branch alongside the
  name. Decided before item 1 so the resolver signature is set once
  and the rename (item 5) only relabels sources.
- **Separate agent config dir is advised, not required.** Step 7.

## User-reported pain

- Never sure which project overloads `runtime.hcl`.
- Unclear what `~/.ateamorg` is for.
- Default model pinned in config goes stale; with no pin, the agent CLI
  picks its own default (often the most expensive model) and burns the
  weekly limit.
- Using ateam outside a code base still needs the `ateam init` ceremony.
- With a custom `CLAUDE_CONFIG_DIR`, re-login after auth expiry means
  remembering which claude instance to log into.
- Two files (`config.toml`, `runtime.hcl`) to audit and edit.
- `runtime.hcl` inheritance: not obvious what the result is.
- `ateam env` output is hard to read.
- `ateam claude` only runs on Linux inside a container.

## Diagnosis

The number of concepts is not the main problem. ateam cannot tell the
user what will run, and lets a run start with nothing pinned. Four
causes account for most of the pain:

1. **Whole-block replace across layers.** `mergeHCL`
   (`internal/runtime/config.go`) assigns `cfg.Agents[name] = ...` for
   each block, so a later layer replaces a same-name `agent` /
   `container` / `profile` block entirely. Changing one model means
   copying the whole block. Two inheritance mechanisms coexist (layer
   replace + `base`), which is why the result is hard to predict.
   The author's own `~/.ateamorg/runtime.hcl` shows the effect: three
   agent blocks that each repeat the full `args` list and `config_dir`
   only to change the model.
2. **Model hidden in `args`.** `--model X` lives inside `args` in the
   embedded defaults and in user files. Nothing warns that it is old.
   Nothing warns when no model is set at all.
3. **Four layers plus an on-disk copy of the defaults.**
   `.ateamorg/defaults/runtime.hcl` exists only so users can read it.
   It is one more layer, one more file to audit, and drifts until
   `ateam update` runs.
4. **Two formats, two directories, config mixed with state.**
   `~/.ateamorg` holds runtime config, prompts, several Claude config
   dirs, backups, logs, caches, the project registry and scratch dirs.

## Plan

Small, independent steps first. Each removes a reported pain without a
migration. One larger breaking change at the end (step 6), then the
setup flow (step 7) that builds on the new layout.

### 1. Model warnings

Running with no model pinned stays allowed. The agent CLI's own default
is a valid choice. ateam only makes the situation visible.

- `ateam env` (and any config audit command) warns when the resolved
  profile has no model pinned, and names the file and block to edit.
- Warn at launch when the resolved model is expensive. Expensive is
  derived from the existing `pricing` tables (`output_per_mtok` above
  a threshold set in the embedded defaults, overridable), not from a
  separate model list. The embedded `claude` agent has no `pricing`
  block today; add one (it is also what `ateam cost` falls back on).
- Lint: `--model` / `-m` / `model_reasoning_effort` inside `agent.args`
  produces a warning pointing at the `model` / `effort` attributes.
  Move the embedded defaults (`claude-sonnet`, `claude-haiku`,
  `claude-high`, `claude-xhigh`, profiles `cheap` / `cheapest`) to the
  attributes.
- Warnings need provenance from step 2 to say which file to edit, so
  step 2 lands first.

### 2. Per-field merge across layers

- A later layer's block overrides only the fields it sets. Maps (`env`)
  merge per key. Lists (`args`) replace, since append semantics are
  surprising.
- Implementation constraint: `AgentConfig` uses zero values for
  "unset", and `resolveInheritance` already cannot tell `false` from
  unset for `sandbox_inside_container`. Per-field merge needs
  attribute presence. Decode each block's attributes with
  `hcl.Body.JustAttributes` (or pointer-typed fields in the HCL
  struct) and merge only the attributes present in the file.
- Keep `base` only for defining a *new* agent from an existing one.
  It resolves after all layers are merged, as today.
- Document one rule: nearer layer wins per field.
- Provenance (traceable-config step 1) is recorded here, per field,
  while merging.
- Behavior change: existing project files that redeclare a full block
  keep working, since every field they set still wins.

### 3. `~/.ateamorg` as fallback from any cwd

- `root.FindOrg` walks up from cwd; from a directory outside `$HOME` it
  fails. Add `$HOME/.ateamorg` as the last fallback.
- `exec` and `parallel` already run in scratch mode (state under the org
  dir). With the fallback, monitoring / ad-hoc prompts need no `init`.
- `report`, `review`, `code`, `verify` keep requiring a project.
- Folded into step 6 once the rename lands (`~/.ateam` is the
  fallback). Worth doing early only if step 6 slips.

### 4. `ateam login [--profile X | --agent X]`

- Resolves the agent's `config_dir` from the merged config and execs
  the agent's native interactive login on any OS. The env var depends
  on the agent type: `CLAUDE_CONFIG_DIR` for claude, `CODEX_HOME` for
  codex / codex-tmux. Today `config_dir` only sets `CLAUDE_CONFIG_DIR`
  (`runner.buildRequest`); the mapping moves next to the agent type.
- Strips `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` /
  `OPENAI_API_KEY` from the child env so the login is stored in the
  config dir, not shadowed by a token.
- Replaces the hard-coded `claude_linux_shared` default and the
  Linux-only guard in `cmd/claude.go`. `ateam claude` stays as the
  in-container variant (`--dangerously-skip-permissions`,
  `--remote-control`). `agent-config --setup-interactive` is removed;
  `agent-config --audit` and `--copy-in` / `--copy-out` stay.
- Fixes "which claude do I log into again".

### 5. `ateam env` rework

- First lines answer, per relevant profile (default, supervisor
  review/code/code_supervisor, role overrides): agent, model, effort,
  config dir, auth status, and the file that set each value.
- Then the layer list (files that exist, files that are missing).
- Role table moves behind `ateam env --roles`.
- This is where the `ModelReport` rendering from traceable-config
  (steps 3 to 5 there) lands.

### 6. Single `ateam.hcl`, one directory name, config/state split

One breaking change, done once, after steps 2 and 1 (per-field merge
and provenance are what make a single file worth having).

**File format**

- Fold `config.toml` into HCL. New blocks: `project {}` (name,
  default_profile, timeouts), `role "x" { status = "on", profile = "docker" }`,
  `supervisor {}`, `sandbox_extra {}`, `container_extra {}`. Existing
  `agent`, `container`, `profile` blocks unchanged. Any layer may hold
  any block; `project {}` is only meaningful in the project layer.
- `git.repo` / `git.remote_origin_url` are dropped (informational only,
  never read at runtime; `git` is queried from WorkDir).
- `keychain_key` moves to the registry entry (it is state, not config).
- Per-role profiles stay as they are today (`[profiles.roles]` becomes
  `role "x" { profile = ... }`).

**Directory rename and discovery**

- Rename `.ateamorg` to `.ateam`. Mechanical part: `OrgDirName` plus
  about 40 string literals in Go, 27 docs, 2 scripts. Rules:
  - Walk up from cwd collecting every `.ateam/` found, then
    `~/.ateam/`.
  - The nearest `.ateam/` whose `ateam.hcl` has a `project` block is
    the project. Every other one is a config layer only.
  - `~/.ateam` is never a project, even with a `project` block.
  - No project found: scratch mode, state under `~/.ateam/state/`.
- Layers become: embedded → `~/.ateam` → ancestors (outermost first)
  → project. `ateam init` only ever creates the project layer;
  `ateam install` goes away (`~/.ateam` is created on first use).
- Drop the on-disk `defaults/` copy. `ateam cat` serves embedded
  defaults. `ateam update` goes away (the prompt assembler already
  reads project → org → embedded FS and never touched
  `defaults/prompts/`).

**Config vs state**

- `~/.ateam/` holds `ateam.hcl`, `prompts/`, `Dockerfile`,
  `secrets.env` (org scope).
- Everything else moves under `~/.ateam/state/`: `logs/`,
  `state.sqlite` (scratch mode), `cache/`, `projects.hcl` (the
  registry), and agent config dirs under `state/agents/<name>/`
  (replaces `claude_shared`, `claude_linux_shared`, …).
- Project `.ateam/` keeps the same split: `ateam.hcl`, `prompts/`,
  `secrets.env` versioned; `state.sqlite`, `logs/`, `runtime/`,
  `cache/` gitignored, as today.

**Registry and project identity**

- `~/.ateam/state/projects.hcl` maps project name → absolute path
  (plus keychain key). Replaces the `projects/<encoded path>/`
  directories and `ProjectIDToPath`. `ateam projects`, `ateam serve`
  and `project-rename` read it. `ateam init` registers; a stale entry
  is reported by `ateam env` and fixed by `project-rename`.
- `project_id` in the db becomes the project name.
- Open choice, not required by the rename: one central
  `~/.ateam/state/state.sqlite` for every project, keyed by name, as
  originally designed. Pro: `ateam ps` / `cost` / `serve` across
  projects from one query. Con: state no longer moves with the
  project directory (worktrees, clones on another machine). Default
  to keeping the per-project db and revisit once the name-based id is
  in.

### 7. Initial setup: separate agent config dir, suggested model

Runs from `ateam init` when `~/.ateam` does not exist yet, and on
demand as `ateam setup`. Non-interactive form: `ateam init
--agent-config home|project|none`.

**Separate config dir (advised, not required)**

- Explain in two lines why: the interactive Claude / Codex install
  carries hooks, plugins, MCP servers and notifications that are
  noise or interfere in an unattended run; a separate dir also keeps
  unattended sessions and history out of the interactive one. (The
  longer version already exists in ISOLATION.md, "Separate config
  directory for the coding agent".)
- Offer three choices, default is the first:
  1. `~/.ateam/state/agents/claude/` — one unattended identity for
     every project.
  2. `<project>/.ateam/state/agents/claude/` — per project.
  3. none — inherit the interactive install.
- On 1 or 2, write `config_dir` into the `agent "claude"` block of
  the chosen layer's `ateam.hcl` and print `ateam login` as the next
  step. Same for codex when `codex` is on PATH (`CODEX_HOME`).
- Nothing is copied from `~/.claude`. Login creates the credentials;
  `agent-config --copy-in` remains for containers.

**Suggested default model (phase 2)**

- After step 1 the unpinned-model warning is visible. Phase 2 makes
  `ateam setup` propose a pin: fetch the current model list and
  prices (Anthropic / OpenAI pricing endpoints, or an ateam-hosted
  JSON refreshed at release time), and suggest the model one tier
  below the newest for each agent type, with its price. Written to
  the chosen layer's `ateam.hcl` on confirmation. Also refreshes the
  `pricing` blocks so `ateam cost` and the expensive-model warning
  use current numbers.
- Decided and implemented: the embedded pricing tables are the price
  list, with a `pricing_as_of` date and a `suggested_model` per agent
  type, refreshed at release time. `ateam setup --refresh-models` fetches
  the repository's current `defaults/ateam.hcl` (no API key, no new
  layer) into a managed section of `~/.ateam/ateam.hcl`; `ateam env`
  warns past 90 days. `ateam setup --model` proposes the suggested model
  with prices and writes the pin to the home or project layer.

## Considered and not planned

- **Dropping inheritance.** Changes would have to be repeated per
  project. Worse than today. Keep layers, fix merge semantics.
- **Reports as `parallel` prompts with a simplified namespace.** Only
  worth it if it deletes code. Not a user-facing problem; not checked
  how much `report` and `parallel` share. Revisit after step 6.
- **One Claude install for interactive and unattended.** Keep a
  separate `config_dir` for unattended agents as the advised default
  (step 7). Once `ateam env` warns about an unpinned model (step 1),
  the "default install has the latest model" worry is visible before
  a run. Re-login is covered by step 4.
- **Refusing to run without a pinned model.** Rejected. The agent CLI's
  default is a legitimate choice; ateam warns, it does not block.
- **Keeping `.ateamorg` and `config.toml` readable for one release.**
  Rejected (see Decisions). The migration code would outlive its
  usefulness and double the test matrix.

## Order of work, in lockstep with traceable-config

Simplification leads. Tracing is built on the new layering, not on
today's, so nothing is written twice.

1. **Per-field merge with provenance built in** (step 2 above +
   traceable-config step 1). The merge rewrite is where "which file set
   this field" is recorded. Profile selection provenance
   (`ProfileSource`) is part of the same change. One change, both
   specs served.
2. **Model warnings** (step 1 above). Uses the provenance from the
   previous item to say which file to edit.
3. **`ateam login`** (step 4 above). Independent of tracing. The home
   fallback (step 3) is folded into item 5 unless item 5 slips.
4. **`ateam env` and `--dry-run` rendering** (step 5 above +
   traceable-config steps 3 to 5). First visible tracing deliverable.
   Uses the current layer names.
5. **Single `ateam.hcl`, `.ateam` layering, state split, name-based
   project identity** (step 6 above). Renames the layer vocabulary in
   one place. The chain rendering from the previous item keeps
   working. Breaking release.
6. **Initial setup** (step 7 above, config dir part). Depends on the
   new layout and on `ateam login`.
7. **Native config reader** (traceable-config step 2). Optional. It
   explains what the agent CLI defaults to when nothing is pinned.
8. **Suggested default model** (step 7, phase 2). Optional.

Only one real ordering constraint: doing item 4 before item 5 means the
layer names change once. Accepted, to get the first visible payoff
early.

Items 1 to 4 are each small. Together they remove the limit-burning,
login and "what will run" issues. Item 5 is the only large one.
