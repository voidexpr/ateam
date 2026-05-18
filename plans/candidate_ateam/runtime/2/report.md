# Summary

Reviewed the last ~9 commits (since `974db24`, the start of the visible recent series). Three code-bearing changes: the prompts A/B-base extraction (`d543917`, `be7279b`), the new `scripts/claude-usage.py` (`974db24` + two follow-ups), and prose-only updates to `defaults/supervisor/review_prompt.md` and the embedded `new_report_base_prompt.md`. The Go change is small, well-scoped, covered by a new test, and correctly fixes the trace/assemble divergence flagged in its commit message. The Python script has one semantic gap worth documenting and a couple of minor inconsistencies. No CRITICAL/HIGH findings; the diff is clean.

# Findings

## `cmd_sleep` only waits on the 5-hour reset, even when the 7-day window is the only breach

- **Location**: `scripts/claude-usage.py:250-261`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `cmd_sleep` calls `threshold_exit_code(args, data)`, which returns the first breach found — `5` for 5-hour, `7` for 7-day. It then unconditionally reads `data["five_hour"]["resets_at"]` and sleeps until that timestamp, ignoring which window actually breached. Trigger: a user with `--alert-7d-max 90` (no `--alert-5h-max`) running over the 7-day threshold but well under the 5-hour ceiling. The script sleeps ~5h or less, returns 0, and the caller proceeds even though the 7-day window is still over threshold. The 7-day window is rolling, so a short sleep gives only marginal relief.
- **Recommendation**: Use the breaches list, not the first exit code. Sleep until the latest `resets_at` among breached windows (or until the 7-day reset when only the 7-day is breached). If `seven_day.resets_at` is unavailable, `sys.exit` with a clear message rather than silently sleeping on the wrong window.

## `cache_status` parameter is unused by `cmd_cat` while every other command threads it

- **Location**: `scripts/claude-usage.py:229-231`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `cmd_check` and `cmd_sleep` accept `cache_status` and either consume it (`cmd_check` calls `cache_status_line`) or formally ignore it (`cmd_sleep`); `cmd_cat` accepts it too but never prints anything about caching. Since `cat` writes JSON to stdout, a stderr "cache: hit/miss" line would be safe to add and consistent with `check`. Without it, the unused parameter is signature-noise that hints at an intent that was never finished.
- **Recommendation**: Either emit the cache status line to stderr in `cmd_cat`, or drop the parameter from `cmd_cat`'s signature and call site so the dispatcher passes only what's used.

## `--cache-ttl` accepts s/m/d but not h, in a tool whose domain unit is hours

- **Location**: `scripts/claude-usage.py:112-118`, doc at line 17
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The script's whole purpose is to read 5-hour and 7-day windows, yet the duration parser supports seconds, minutes, days. A user wanting "cache for 1 hour" must write `60m` or `3600s`. Documented as `<N>{s,m,d}` so this is consistent with itself, but the gap is jarring given the domain.
- **Recommendation**: Add `h` to the regex `([smhd])` and the unit map `{"s": 1, "m": 60, "h": 3600, "d": 86400}`. Update the docstring and `--cache-ttl` help text accordingly.

## Inconsistent error reporting between `parse_pct` and `parse_duration`

- **Location**: `scripts/claude-usage.py:104-118`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `parse_pct` is wired through `type=parse_pct`, so it raises `argparse.ArgumentTypeError` and gets argparse's "invalid value" framing. `parse_duration` is called from `load_data` and `sys.exit`s with a custom message. Both are user-input parsers but produce different exit codes and message formats. Minor style drift on a new file.
- **Recommendation**: Either move duration parsing into argparse via a `--cache-ttl` `type=` (parse eagerly), or have `parse_duration` raise `ValueError` and let the caller emit the message. Eager parsing also means a malformed TTL fails immediately rather than only when the cache path is taken.

## Stale comment on `## Role performing the audit` in `new_report_base_prompt.md`

- **Location**: `defaults/new_report_base_prompt.md:26-28`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The author silently fixed a typo in this section ("what model you are use" → "what model you are using") when forking the legacy file, which is good. But the heading-style mismatch carried over: the file tells the role "Use `#` for top-level headings, not `##`" (line 64) and the validation gate lists `# Summary` / `# Findings` (lines 70-73), yet the Report Format examples in the same file render as `### Summary` / `### Findings` (lines 36-52). The commit message explicitly defers the heading-style fix to keep the A/B base narrow, so this is acknowledged. Flagging only because the new file is the place where any forward-only fix would land.
- **Recommendation**: When the A/B verdict comes in and the bases are merged, flip the four `### `-prefixed example headings to `# ` (or weaken the validation gate to accept any heading level). Don't touch it in the meantime — the comparison baseline depends on the prompts being identical apart from the two intentional deltas.

# Quick Wins

1. Fix `cmd_sleep` to use the breach window's reset time, not always `five_hour.resets_at` (`scripts/claude-usage.py:250`) — MEDIUM severity, SMALL effort.
2. Add `h` (hours) to `parse_duration` (`scripts/claude-usage.py:112`) — LOW severity, SMALL effort, but high user-fit value.
3. Drop or use the `cache_status` parameter in `cmd_cat` (`scripts/claude-usage.py:229`) — LOW severity, SMALL effort.

# Project Context

- **Recent diff scope**: ~9 commits since `974db24` (script intro). Of these, three changed code: `d543917` (A/B base prompt + helper + test), `be7279b` (extract `resolveBaseFileForRole`), and the `claude-usage.py` triplet. The rest are docs, prompt prose, and plan files.
- **Working tree**: clean. No prior `recent` report on disk under `.ateam/runtime/{1,2,3}/`.
- **Key files touched by the recent code changes**:
  - `internal/prompts/prompts.go` — new `resolveBaseFileForRole` helper at lines 107-119; `assembleRoleAction` routes through it at line 145. Constants for the A/B split at lines 40-50.
  - `internal/prompts/trace.go` — `traceRoleAction` routes through the same helper at line 69. Acknowledged caveat: `traceFileOr3Level` is on-disk only (no embedded fallback), so the `ateam prompt` trace shows the new base for dotted roles only after `ateam update` deploys the file — preexisting and documented.
  - `internal/prompts/embed.go` — `DefaultNewReportBasePrompt` and its `embeddedFiles()` entry at lines 188-198 and 254-261.
  - `defaults/embed.go` — additional `//go:embed new_report_base_prompt.md` directive at line 9.
  - `defaults/new_report_base_prompt.md` — new file; identical to `report_base_prompt.md` modulo the `## Project maturity` section and the "Run analytical tools..." Guidelines bullet.
  - `internal/prompts/prompts_test.go:228-264` — `TestDottedRoleSelectsNewReportBase` covers both the dotted-selects-new and non-dotted-keeps-legacy branches via embedded fallback.
  - `scripts/claude-usage.py` — new Python CLI (290 lines) hitting `api.anthropic.com/api/oauth/usage`, three commands (`cat`, `check`, `sleep`), with optional `--cache-file`/`--cache-ttl` and `--alert-{5h,7d}-max` thresholds.
  - `defaults/supervisor/review_prompt.md` — four new principles + two tightenings; prose-only.
- **TODO discipline**: every piece of the A/B split carries a "TODO: fix this before v1" marker (constants, embed, helper, test, file, embed directive). The resolution paths (merge/delete/keep) are spelled out in `d543917`'s commit message. Worth keeping a tracking issue so the markers don't outlive the eval.
- **Out of scope for `recent`**: structural debt in untouched files; the legacy `report_base_prompt.md` heading-style inconsistency that predates this series; any architectural opinions on the dotted-prefix role taxonomy itself.
