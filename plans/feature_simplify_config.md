# Feature: Simplify configuration

Companion to [feature_traceable_config.md](feature_traceable_config.md).
That plan makes the config *observable* (which layer set the model).
This plan makes the config *harder to get wrong* and reduces the number
of files, directories and rules. The two will be blended: provenance
capture belongs in whatever loader comes out of step 6 below, and the
`ateam env` rework in step 5 is where the traceable-config rendering
lands.

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
migration. One larger migration at the end.

### 1. Fail closed on model

- Refuse to launch when no model is resolved from config or `--model`.
  Error names the file and block to edit.
- `allow_agent_default_model = true` (top-level scalar) opts out
  explicitly.
- Warn at launch when the resolved model is on an "expensive" list
  (initially a hard-coded list in `defaults/runtime.hcl`, overridable).
- Lint: `--model` / `-m` / `model_reasoning_effort` inside `agent.args`
  produces a warning pointing at the `model` / `effort` attributes.
  Move the embedded defaults to the attributes.
- Directly fixes the weekly-limit problem. Traceable-config's audit
  shows the mistake after the fact; this prevents it.

### 2. Per-field merge across layers

- A later layer's block overrides only the fields it sets. Maps (`env`)
  merge per key. Lists (`args`) replace, since append semantics are
  surprising.
- Keep `base` only for defining a *new* agent from an existing one.
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

### 4. `ateam login [--profile X | --agent X]`

- Resolves the agent's `config_dir` from the merged config, sets
  `CLAUDE_CONFIG_DIR`, strips `CLAUDE_CODE_OAUTH_TOKEN` /
  `ANTHROPIC_API_KEY`, and execs interactive `claude` (or the agent's
  native login) on any OS.
- Replaces the hard-coded `claude_linux_shared` default and the
  Linux-only guard in `cmd/claude.go`. `ateam claude` stays as the
  in-container variant (`--dangerously-skip-permissions`,
  `--remote-control`).
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

One migration, done once, after steps 1 and 2 (per-field merge and
provenance are what make a single file worth having).

- Fold `config.toml` into HCL. New blocks: `project {}` (name, git,
  timeouts, default_profile), `role "x" { status = "on", profile = "docker" }`,
  `supervisor {}`, `sandbox_extra {}`, `container_extra {}`. Existing
  `agent`, `container`, `profile` blocks unchanged. Any layer may hold
  any block.
- Rename `.ateamorg` to `.ateam`. Rules:
  - Nearest `.ateam/` whose `ateam.hcl` has a `project` block is the
    project.
  - Every ancestor `.ateam/` and `~/.ateam/` are config layers only.
  - `~/.ateam` is never a project.
- Drop the on-disk `defaults/` copy. `ateam cat` serves embedded
  defaults. Layers become: embedded → `~/.ateam` → ancestors → project.
- Config vs state: `~/.ateam/` holds `ateam.hcl`, `prompts/`,
  `Dockerfile`. Everything else moves under `~/.ateam/state/`: `logs/`,
  `state.sqlite`, `cache/`, `projects/`, Claude config dirs.
- Migrator: read legacy `config.toml` + `runtime.hcl` + `.ateamorg` for
  one release with a warning, write the converted `ateam.hcl` on
  request (`ateam migrate-config`).
- Per-role profiles stay as they are today (`[profiles.roles]` becomes
  `role "x" { profile = ... }`).

## Considered and not planned

- **Dropping inheritance.** Changes would have to be repeated per
  project. Worse than today. Keep layers, fix merge semantics.
- **Reports as `parallel` prompts with a simplified namespace.** Only
  worth it if it deletes code. Not a user-facing problem; not checked
  how much `report` and `parallel` share. Revisit after step 6.
- **One Claude install for interactive and unattended.** Keep a
  separate `config_dir` for unattended agents. Once ateam pins the
  model (step 1), the "default install has the latest model" worry is
  gone. Re-login is covered by step 4.

## Order of value

Steps 1 to 5 are each small and independent. Together they remove the
limit-burning, login and "what will run" issues. Step 6 is the only
large one.
