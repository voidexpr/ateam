# Summary

Reviewed the 8 commits since the previous `code.recent` report (2026-05-13_20-31-33): `2f81a30`, `0f45d36` (already in prior scope — re-examined), `c308666`, `595a9b4`, `ccd5003`, `22c0cac`, `1516d06`, `386e62d`, `6dcf9a0`. Three commits change Go code (the `--roles` authority refactor + `runsEnabledGate` helper, the `roles --docs` `|` escape, the codex-args-after-`exec` fix), one adds a meaningful Python feature (`--max-sleep`), and four are prose-only. The Go diffs are tight and well-tested. Two previous Python findings remain unresolved, one was fixed (`--cache-ttl h`), and the codex/runtime.hcl rename introduced a stale comment. No CRITICAL/HIGH issues.

## Role performing the audit

- Role: `code.recent` (new dotted-prefix role; A/B base prompt `defaults/new_report_base_prompt.md`).
- Model: `claude-opus-4-7`, default thinking, no extended-thinking attributes specified by the harness for this run.
- Working tree: clean at `6dcf9a0`.
- Scope: commits since 2026-05-13_20-31-33 + immediate dependencies; out-of-scope architectural debt deferred to `code.structure`/`project.*` roles.

# Findings

## `cmd_sleep` still sleeps on the 5-hour reset even when only the 7-day window breached (unresolved from prior report)

- **Location**: `scripts/claude-usage.py:266-289`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Re-flagged from the 2026-05-13 report. The `--max-sleep` addition in `22c0cac` is orthogonal to this bug. `cmd_sleep` runs `threshold_exit_code(args, data)`, which returns the first breach code (5 for 5-hour, 7 for 7-day); the function then unconditionally reads `data.get("five_hour", {}).get("resets_at")` and sleeps until that timestamp regardless of which window actually breached. Trigger: a caller with `--alert-7d-max 90` (no `--alert-5h-max`) that is over the 7-day threshold but well under the 5-hour ceiling. The script sleeps until the 5-hour reset (≤ 5h) and returns 0, even though the 7-day rolling window will still be over threshold after the wake-up. With `--max-sleep` set this is somewhat mitigated (exit 10 if the 5-hour window is many hours out), but the window-selection logic is still wrong. The on-disk source (line 11) advertises "sleep until the 5-hour reset" so callers may already be aware, but the docstring should still match the breach window.
- **Recommendation**: Use `threshold_breaches(args, data)` (already returns labelled breaches), pick the latest `resets_at` among the breached windows, and sleep until that. If `seven_day.resets_at` is unavailable, `sys.exit` with a clear message rather than silently sleeping on the wrong window. Update the top-of-file docstring (`scripts/claude-usage.py:11`) once the behavior is corrected.

## `parse_duration` reports `invalid --cache-ttl` for malformed `--max-sleep` (new in 22c0cac)

- **Location**: `scripts/claude-usage.py:114-120` + caller at `278`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `parse_duration` `sys.exit`s with the literal string `error: invalid --cache-ttl '{s}' (expected ...)`. `22c0cac` added a second caller — `cmd_sleep` parses `args.max_sleep` via the same function (line 278). A user passing `--max-sleep 30x` now gets `error: invalid --cache-ttl '30x'`, naming the wrong flag. This compounds the prior finding about `parse_pct` (argparse-typed) vs `parse_duration` (custom `sys.exit`) being inconsistent — both can be addressed in the same change.
- **Recommendation**: Make `parse_duration` raise `ValueError` (no hard-coded flag name) and have each caller frame the error, or thread the flag name in as an argument. Easiest path: register `--cache-ttl` and `--max-sleep` with `type=parse_duration` so argparse emits its own consistent framing — eager parsing also means a malformed TTL fails immediately rather than only on the cache path.

## `cache_status` parameter is unused by `cmd_cat` while every other command threads it (unresolved from prior report)

- **Location**: `scripts/claude-usage.py:245-247`, dispatch at `315`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Re-flagged. `cmd_check` and `cmd_sleep` both make use of `cache_status` (`cmd_check` via `cache_status_line`, `cmd_sleep` via the embedded `cmd_check` call). `cmd_cat` accepts the parameter but never references it. Either the parameter should be dropped at the signature + call site, or a cache status line should be emitted to stderr (safe because `cmd_cat` writes JSON only to stdout). Today it is dead signature noise.
- **Recommendation**: Emit `cache_status_line(args, cache_status)` to stderr in `cmd_cat`, or drop the parameter from the signature and from the dispatch table call so each handler only takes what it uses.

## Stale comment in `defaults/runtime.hcl` after the codex model rename

- **Location**: `defaults/runtime.hcl:287-289` (introduced/preserved by `386e62d`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The block-comment above the codex model list still says: `gpt-5.4 = the new name for what was gpt-5.4-codex (codex dropped the -codex suffix). Both names are kept so streams that report either resolve cleanly.` Commit `386e62d` renamed every `-codex` variant (gpt-5.4-codex → gpt-5.4, gpt-5.3-codex → gpt-5.3, etc.), so there is no `gpt-5.4-codex` model block anywhere in the file. "Both names are kept" no longer matches the code: only the new (de-suffixed) names exist. A reader will look for the parallel entry that the comment promises and find none.
- **Recommendation**: Update the comment to describe the current state: codex dropped the `-codex` suffix in its event stream, so the file only lists the suffix-free names. If keeping a back-compat alias is desired, restore one explicit `gpt-5.4-codex` entry; otherwise just trim the second sentence. Per the role's rules, do not delete the explanatory note — only refresh it.

## Heading-style mismatch in `defaults/new_report_base_prompt.md` (unresolved, acknowledged)

- **Location**: `defaults/new_report_base_prompt.md:36-53` vs. `64` and `70-73`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Re-flagged from the prior report. Commit `6dcf9a0` touched this file (dropped the maintenance-mode clause from `## Project maturity`) but did not address the heading-style inconsistency: the Report Format section gives `### Summary` / `### Findings` examples while the same file tells the role "Use `#` for top-level headings, not `##`" and the Output Validation Gate references `# Summary` / `# Findings`. The prior author explicitly deferred the fix to keep the A/B base narrow. Flagging only because the file was touched again in this window without picking it up.
- **Recommendation**: When the A/B verdict is in (the file has a `TODO: fix this before v1` marker at line 1 covering the merge with `report_base_prompt.md`), flip the four `### `-prefixed example headings to `# ` or weaken the validation gate to accept any heading level. Until then, leave the legacy/new base prompts identical apart from the two intentional deltas.

# Quick Wins

1. Fix `cmd_sleep` to use the breach window's reset (`scripts/claude-usage.py:266-289`) — MEDIUM, SMALL effort. Highest impact in this batch.
2. Update the stale "Both names are kept" comment in `defaults/runtime.hcl:287-289` — LOW, SMALL effort.
3. Decouple `parse_duration`'s error message from `--cache-ttl`, or route both flags through argparse `type=` (`scripts/claude-usage.py:114-120`, `278`, `308-311`) — LOW, SMALL effort, fixes the misleading message for `--max-sleep`.
4. Drop or wire the `cache_status` parameter in `cmd_cat` (`scripts/claude-usage.py:245`) — LOW, SMALL effort.

# Project Context

- **Recent diff window**: 2026-05-13_18:33 → 2026-05-14_13:54, ~8 commits since the prior report. Code-bearing changes: `2f81a30`+`c308666`/`0f45d36` (review --roles authority refactor + `runsEnabledGate` helper + new test file), `ccd5003` (`escapeTableCell` helper for `roles --docs`), `22c0cac` (`--max-sleep`, `fmt_duration`, `h` in `parse_duration`), `386e62d` (codex `exec`-subcommand arg ordering + runtime.hcl model renames + cmd-file backtick fences). The rest is prose.
- **Working tree**: clean at `6dcf9a0`. Previous `code.recent` report was at `.ateam/runtime/{prior}/report.md`.
- **Key files touched by recent code changes**:
  - `internal/prompts/prompts.go` — `ReviewSelector.runsEnabledGate()` helper at line ~273, used twice in `Filter`. The enabled-only gate now centrally derives from `!IncludeDisabled && len(Roles)==0` (a single source of truth).
  - `internal/prompts/review_selector_test.go` — new ~182-line test file added in `2f81a30`, covering enabled-only / `--all` / explicit-roles / max-age combinations and the empty-after-filter error path.
  - `cmd/all.go:140-156` and `cmd/report.go:364-385` — both call sites stopped synthesizing `IncludeDisabled` from explicit `--roles`; the responsibility lives in `prompts.ReviewSelector.Filter` only. `cmd/report_test.go:264-290` updated to match.
  - `cmd/roles.go:114-128` — `printRolesDocs` now passes role descriptions through `escapeTableCell` to keep `|` and newlines from corrupting the markdown table.
  - `internal/agent/codex.go` (and `agent.go:194-203`, `codex_test.go`) — codex invocation switched from `codex <args> exec --json <prompt>` to `codex exec --json <args> <prompt>` because flags like `--skip-git-repo-check` are exec-only. `args_outside_container` still holds the non-container flags.
  - `defaults/runtime.hcl:267-318` — codex agent default args now empty (the previously-listed `--ask-for-approval never` is a top-level-only flag and no-op for `exec`); codex models renamed to drop the `-codex` suffix; `default_model` bumped to `gpt-5.4`; a new `gpt-5.5` price entry; pricing for renamed models unchanged.
  - `internal/runtime/config.go:521-550` — new `RuntimeDiff` type + `DiffOrgDefaults` parallel to `prompts.DiffOrgDefaults`; consumed by `cmd/update.go:43-85`, which now reports prompt and runtime diffs together.
  - `internal/runner/runner.go:1068-1101` — `writeCmdFile` wraps env dumps in triple-backtick fences for the cmd file output (cosmetic; markdown rendering only).
  - `scripts/claude-usage.py` — adds `--max-sleep`, `fmt_duration`, `h` in `parse_duration`, and a `cmd_check` call inside `cmd_sleep` so the sleep path also surfaces window pcts and threshold warnings on stderr-printable output.
  - `defaults/new_report_base_prompt.md:18` — `## Project maturity` paragraph trimmed; maintenance-mode clause removed (now lives in `project.maintenance`'s own prompt).
  - `defaults/roles/project.maintenance/report_prompt.md` — full rewrite of the tool-rules section in `6dcf9a0`: hard rule "No new tools, with one exception" (vuln-scanner), the redundant `## Tool recommendation discipline` section deleted, anti-drift unchanged.
  - `defaults/supervisor/review_prompt.md:31-39` — "Inputs and where things live" rewritten so the "no disk reads" rule is scoped to role reports only; policy files (`CLAUDE.md`, `AGENTS.md`, README policy sections, recent `git log`) are explicitly readable when the policy principle applies.
  - `defaults/roles/code.structure/report_prompt.md:44` — new Module-scope bullet "N+ site duplication is the finding, not the per-site fix". Prose-only.
- **TODO discipline**: every piece of the A/B base-prompt split still carries `TODO: fix this before v1` markers. `defaults/runtime.hcl:273-274` has a new `TODO: fix this once it's possible to write …` describing the codex top-level/sub-command flag-split limitation. Worth tracking issues so these markers don't outlive the eval.
- **Resolved since prior report**: `--cache-ttl` now accepts `h` (commit `22c0cac` — line 116). Prior finding closed.
- **Out of scope for `recent`**: structural debt in untouched files; prior dotted-prefix role design choices; the legacy `report_base_prompt.md` heading-style inconsistency that predates this series.
