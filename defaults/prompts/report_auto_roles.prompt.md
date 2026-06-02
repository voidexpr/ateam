---
description: Recommend which roles to run report for at this point in time
---
# Recommend ateam roles

Ateam runs role-specific agents that audit this codebase, then a supervisor reviews their findings and a coder implements selected fixes. Given the current state, recent code changes, and prior runs, recommend which roles to run this round and at what depth.

## Current state

The harness has already collected every input you need for this decision. Read the sections below; do **not** re-run these commands.

{{exec.auto_roles_commands_output}}

If a role name above is unfamiliar (typically a custom role added by this project), read its prompt with `ateam prompt --action report --role NAME` — that's the only tool call you should need.

## Decide

Apply in order, stop at the first match:

1. **No code changes since the last review** → recommend no roles (emit `{{ateam.auto_roles_marker}}` with nothing after the colon). The existing review is still current; the user can run `ateam review` manually if needed. Regenerating reports against unchanged code wastes tokens.
2. **Recent changes touched a role's territory** → run that role. Examples:
 - SQL / migration / schema → `database.schema`
 - Build / CI / lint / hooks → `project.automation`
 - New code, new branches, missing tests → `test.gaps`, `test.recent`
 - Dependency bumps → `project.dependencies`
 - Auth / secrets / input handling → `project.security`
3. **Role's territory was untouched** → skip it, even if `on` by default. The prior report is still accurate.
4. **Prior review rejected or deferred every finding from a role AND that role's territory hasn't changed since** → skip.
5. **A disabled role's territory was heavily touched** → recommend it anyway. `--roles NAME` runs the role regardless of `config.toml` status.

Bias toward short role lists. Two well-chosen roles produce a better review than ten that mostly repeat last cycle.

## Pick depth

- `ateam report --roles X,Y` — fresh reports only; lets you inspect before reviewing.
- `ateam report --roles X,Y --review` — reports + review in one go.
- `ateam run-all --roles X,Y` — report → review → code → verify, end-to-end.

## Output

Write your recommendation to:

```
{{exec.output_file}}
```

The file must contain, in this order:

1. **Rationale** (2–4 lines): which roles you picked or skipped and why, noting each role's current status (`on` / `off` / not listed).
2. **Three copy-paste command variants** for the same role list, in a single fenced code block:
   ```
   ateam report --roles X,Y
   ateam report --roles X,Y --review
   ateam run-all --roles X,Y
   ```
   Replace `X,Y` with your recommended comma-separated role list (no spaces around commas).
3. **A final marker line on its own line, no markdown formatting:**

   ```
   {{ateam.auto_roles_marker}} X,Y
   ```

   Same role list as above, comma-separated, no spaces. If you recommend running no roles (everything's fresh and the existing review is current), emit exactly `{{ateam.auto_roles_marker}}` with nothing after the colon — ateam reads this as "no work to do".

After the `Write` call returns successfully, your FINAL assistant message must be a single short confirmation line, e.g. `Auto-roles written to {{exec.output_file}}`. The on-disk file is the source of truth — ateam parses it directly. Do not execute any of the recommended commands.
