# Sandboxing and attended agents in ateam

Status, 2026-11-21: proposal. Nothing built.

## Summary

- Move sandbox configuration and launching from targy + `nono-sandbox` into ateam.
- Extend `profile` to carry the sandbox policy. A profile can `use` other profiles.
- Add an attended entry point (`ateam chat`) and arbitrary commands under a sandbox.
- Add sandbox backends as container types, nono first.
- Order: filesystem and commands, then network, then secrets.

## Scope statement

ateam is automation around coding agents. Unattended quality workflows are one use; attended sessions are another.

What ateam adds for attended sessions:

- Sandboxing beyond what `claude` and `codex` ship: one boundary set from outside the agent, the same for every agent and every command.
- Arbitrary commands in a sandbox: test a policy (`make test` under it), or open a secure shell to work in as an agent would.
- A saved, auditable policy per project, shared by attended and unattended runs.

## Background

Current state (targy `plans/feature_agent_nono.md`, built and tested on macOS, 2026-11-20):

- `~/dotfiles/agents/agents.targy`: the one source of rules. Read filter (`agent-read`), system filters copied from nono, groups (`go`, `node`, `dev`, `claude`, ...), `DOMAINS`, Claude tool and deny lists.
- targy steps `nono_profile`, `claude_permissions`, `json_merge`: about 700 lines of Python, plus `make test-nono` and `make test-claude-settings`.
- `~/dotfiles/bin/nono-sandbox`: parses flags, runs targy to write a nono profile and matching Claude settings, then execs `nono run`.
- Linux (meerdog, Landlock) is not run yet.

Why this pushes toward ateam:

- targy cannot run the command itself: it pipes output into its log, so the agent gets no terminal. ateam already execs agents.
- The saved-policy plan (targy `plans/feature_sandbox_saved_policy_file.md`) needs either new targy modes or a standalone launcher. Its open options already list "without targy" and "one Go binary".
- The nono plan lists "ateam starting its agents through nono-sandbox" as later work.
- ateam already has layered HCL config, profiles, `ateam secret`, dry-run, outer-sandbox detection, and a call database.
- autoversion's `filterrules` Go package parses the same filter syntax as targy.

Related research: `research/ResearchSandboxing.md`, sections F.1.2 (review-time hazards), F.3 (nono), G (network and secret injection).

## Design

### Profiles carry the policy

No new `sandbox` block. `profile` gains policy fields and composition.

```hcl
# a fragment: policy only, no agent or container
profile "go" {
  write = ["~/go/pkg", "~/Library/Caches/go-build", "~/.cache/go-build"]
  read  = ["~/Library/Application Support/go"]
}

profile "dev" {
  use = ["go", "node", "python"]
}

profile "nono" {
  agent     = "claude"
  container = "nono"
  use       = ["base-read", "net-default"]
}

# a project's saved policy
profile "myproject" {
  use     = ["nono", "go"]
  write   = ["~/SyncDatabox/dotfiles"]
  read    = ["~/.ssh/config"]          # opens a path base-read keeps unreadable; reported at launch
  exclude = ["~/SyncDatabox/nicmac/projects/other"]
  domains = ["example.com"]
}
```

New profile fields:

| Field | Meaning |
|---|---|
| `use` | Profiles merged into this one, in order |
| `read` | Paths readable, including ones an inherited `exclude` closes |
| `write` | Paths writable, besides the working folder |
| `exclude` | Paths unreadable |
| `domains` | Hosts reachable through the backend's proxy |
| `open_net` | No domain filtering |
| `filters` | Filter files or inline filters in the targy/autoversion syntax, for rules too long for lists |
| `command` | Default command, for saved policies |

Merge rules for `use`:

- Lists (`read`, `write`, `exclude`, `domains`, `filters`): union, in `use` order, then the profile's own.
- Scalars (`agent`, `container`, `model`, `effort`, `max_budget_usd`, `open_net`, `command`): last `use` wins, then the profile's own.
- A `read` in a nearer profile reopens a path an inherited `exclude` closes. The launch reports it.
- Cycles are an error. `ateam env` shows the resolved profile and where each entry came from.

Existing pieces to reconcile:

- `sandbox_extra` in project config (`allow_write`, `allow_read`, `allow_domains`, `unsandboxed_commands`) overlaps with the new fields. Keep it as an alias that feeds the same lists, then deprecate.
- The agent's `sandbox` template (Claude's own sandbox settings) becomes one rendering of the policy, used by the built-in sandbox backend.

### Filters

- `agents.targy` content moves to `~/.ateam/`: base profiles in HCL, long rule sets as filter files.
- ateam parses filters with `github.com/nicad/autoversion/filterrules`.
- Gap: `filterrules` resolves `use NAME` against built-in groups only. targy resolves named filters. Add a caller-supplied resolver to `filterrules`, or express named sets as profiles and keep filter files flat.
- To check: sugar and list parity between targy's Python filters and `filterrules` (shared conformance tests exist in autoversion).

### Backends as container types

| Container type | Policy rendered as | State |
|---|---|---|
| `none` + agent sandbox | Claude sandbox settings, Codex flags | exists |
| `nono` | nono profile JSON + matching Claude settings (its own sandbox off) | new, first |
| `fence` or an embedded Go sandbox | that tool's config | later |
| `docker`, `docker-exec` | mounts from `read`/`write`; domains need a gateway (research G) | later |

nono backend, ported from targy:

- macOS: one grant of the home, `exclude` paths denied inside it. Linux: the home listed around unreadable paths.
- nono's required deny groups lifted for paths the policy keeps readable.
- Profile written beside its final path, validated by `nono profile validate`, then renamed.
- `suppress_save_prompt` set, so nono keeps no second source of rules.
- A verify mode for after a nono upgrade.

A backend that cannot express a rule refuses the launch. It never weakens the rule silently.

### Commands

```bash
# attended agent, policy from flags, saved for next time
ateam chat --go --write ~/something --read ~/other/path --save myproject.hcl

# the same launch again
ateam chat --policy ./myproject.hcl

# extra arguments go to the agent
ateam chat --policy ./myproject.hcl -- --continue

# any command or a shell under the same policy
ateam sandbox --policy ./myproject.hcl -- make test
ateam sandbox --go -- zsh

# audit: resolved policy and the backend command, run nothing
ateam chat --policy ./myproject.hcl --dry-run
```

- `--GROUP` is shorthand for `use GROUP`, as in `nono-sandbox --go`.
- `--save FILE` writes a profile from the flags. Hand-written files work the same.
- `--policy FILE`, not `--resume`: `ateam resume EXEC_ID` and `claude --resume` already mean something else.
- Unattended runs (`ateam exec`, `report`, `code`) take the same profiles through `--profile`.
- Names `chat` and `sandbox` are placeholders.

### Where saved policies live

- A session must not be able to widen its own next launch.
- The project's `.ateam/` is inside the working folder, so it is writable by the agent.
- Proposed: saved policies go in the user layer (`~/.ateam/projects/<id>/`) or beside the project (`../myproject.hcl`), both outside the write grant.
- Policy fields in a project-layer `ateam.hcl` can only tighten (add `exclude`, remove nothing). Widening fields there are ignored with a warning. Same rule as flar and Byre (research F.3).
- Every backend also protects `.git/hooks`, `.git/config`, agent settings, and ateam's own config inside writable folders, as Porta does (research E.13.13).

## Sandbox merged into profile: assessment

I agree with merging. Reasons:

- One concept fewer. A saved policy is a profile; `--profile` and the profile resolution order already exist.
- Composition (`use`) is needed anyway for groups such as `go` and `dev`.
- Model, effort and budget pins compose with policy for free (`use = ["cheap", "go"]`).

Points to settle, none blocking:

1. **Fragments.** A profile with policy only (`go`) has no agent or container. Selecting it directly must fall back to the default agent and container, or be an error.
2. **Two kinds of content in one block.** "What runs" (agent, container, model) and "what it may touch" (paths, domains). The merge rules differ per field kind and must be documented in one table.
3. **Trust per layer.** The tighten-only rule applies to policy fields, not to model or budget pins. The config loader needs to know which fields are policy.
4. **Supervisor profile selection.** Role and supervisor blocks already pick profiles. Check that composition does not change their resolution order.

## Phases

| Phase | Content |
|---|---|
| 0 | Wrapper container type: ateam prefixes the agent command with `nono-sandbox ... --`. No port. Unattended runs get nono now. |
| 1 | Profile `use` and policy fields; `filterrules` resolver; nono backend port with targy's test fixtures; `ateam chat`, `ateam sandbox`, `--save`, `--policy`. Retire `nono-sandbox`. |
| 2 | Network: `domains` per group; audit log of decisions; Docker modes behind a gateway (research G.3). |
| 3 | Secrets: `ateam secret` values as placeholders, injected outside the sandbox (research G.4, F.1.1 Tier A first). |
| 4 | Other backends: fence or an embedded Go sandbox; Enclave or OpenShell for container modes. |

Phase 1 should wait for the Linux run of the current targy implementation (K4 on meerdog), so the port copies settled behavior.

## What stays in targy and dotfiles

- targy keeps its targets, backups and filters. Its `nono_profile`, `claude_permissions` and `json_merge` steps are removed after phase 1.
- Settings for plain `claude` (no launcher): today generated from `agents.targy`. Either ateam generates them from the same profiles (`ateam env --claude-settings`), or targy keeps that one target and reads the filter files ateam uses. One source of rules either way.

## Pros and cons

Moving to ateam:

| Pros | Cons |
|---|---|
| One Go binary; no Python or `uv` at launch; a project resumes on any machine | Port of about 700 lines of tested Python plus two test suites |
| The terminal problem goes away: ateam execs the agent | Linux behavior is not settled yet |
| Attended and unattended runs share one policy | `filterrules` needs a named-filter resolver; parity to check |
| Layered HCL, profiles, secrets, dry-run, call database already exist | Work before v1 that is not release polish |
| Network and secret phases have a natural home | Plain-`claude` settings need a new owner or a shared source |

Staying with targy + `nono-sandbox`:

| Pros | Cons |
|---|---|
| Built, committed, tested on macOS | Three moving parts per launch: launcher, targy, nono |
| Filter semantics have one owner | Saved policy needs targy changes anyway (working folder, launcher mode) |
| ateam stays on its release work | ateam's agents stay outside the nono boundary |

## Open questions

1. Command names: `chat` and `sandbox`, or `agent` and `run`.
2. Named filters: a `filterrules` resolver, or profiles only with flat filter files.
3. Saved policy location: user layer keyed by project, or `../myproject.hcl`.
4. Save only with `--save`, or at every launch.
5. Selecting a fragment profile directly: default agent and container, or an error.
6. Plain-`claude` settings: generated by ateam, or kept in targy.
7. nono's network is allow-by-default without a domain list (research F.3). Should a profile with no `domains` mean closed or open?
8. Go version: ateam is on 1.26.5, autoversion on 1.25.0. Fine for importing; confirm `make run-ci` (vuln, deadcode) with the new dependency.
