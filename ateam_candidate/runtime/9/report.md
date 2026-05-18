# Summary

The last 5 commits on `candidate` are small and overwhelmingly well-tested. The two behavior-changing commits — `2f81a30` (review `--roles` authoritative) and `0f45d36` (chained `--review` honors `--roles`) — landed with a new test file (`internal/prompts/review_selector_test.go`) and an updated `cmd/report_test.go` that together cover the new semantics. One gap remains: commit `ccd5003` adds the `escapeTableCell` helper to fix a real table-corruption bug in `roles --docs`, but no regression test exercises an input containing `|` or a newline.

# Role

- Role: `test.recent` (Recent Changes Test Coverage)
- Model: claude-opus-4-7 (default reasoning, no extended thinking flag set in invocation)
- Scope: `HEAD~5..HEAD` (commits `0f45d36`, `2f81a30`, `c308666`, `595a9b4`, `ccd5003`); working tree clean
- Affected packages tested locally: `go test ./internal/prompts/... ./cmd/...` — all green

# Findings

## 1. `escapeTableCell` bug fix has no regression test

- **Title**: `roles --docs` `|`-escape fix lacks a regression test
- **Location**: `cmd/roles.go:129-133` (`escapeTableCell`); used at `cmd/roles.go:117` inside `printRolesDocs`. Test file would be `cmd/roles_test.go` (currently only tests `roleStatus`).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Commit `ccd5003` ("roles --docs: escape `|` in descriptions; preserves markdown table rows") is a bug fix: a role whose `description:` frontmatter contains a literal `|` (e.g. `code.structure` — "tagged by scope (local | module | architecture)") was breaking the generated markdown table by introducing extra columns. The fix adds `escapeTableCell` and the change also flattens newlines to spaces. The diff adds no test. The pre-fix bug had a concrete reproducer (`code.structure`'s description), so a regression test is cheap and direct. Without one, a future "simplification" of `printRolesDocs` could silently reintroduce the corruption — the existing `ROLES.md` only catches it if a human re-reads the table after `make docs`.
- **Recommendation**: Add a unit test in `cmd/roles_test.go` named `TestEscapeTableCell` covering: (a) input with `|` becomes `\|`; (b) input with `\n` becomes a single space; (c) input with both is handled; (d) input with no special chars is unchanged. Optionally extend with a `TestPrintRolesDocsTableRowShape` that asserts the rendered row for `code.structure` has exactly 4 `|`-separated cells. The new function is small enough that the table-test form is the right shape — two or three rows max.

## 2. `runsEnabledGate` and `IncludeDisabled` interaction — covered

- **Title**: `--all` + `--roles` redundant-but-harmless case is tested
- **Location**: `internal/prompts/review_selector_test.go:88-95` ("explicit roles + include disabled")
- **Severity**: LOW (informational — no gap)
- **Effort**: n/a
- **Description**: Commit `c308666` extracted `runsEnabledGate()` from inline logic in `Filter`. The new test file covers the four combinations of `IncludeDisabled` × `Roles non-empty`, including the case where both are set (the gate is skipped, `Roles` still narrows). This is the right minimum — no further coverage is needed here.
- **Recommendation**: None. Listed only to document that the refactor's new branch is intentionally exercised; no action.

## 3. `cmd/all.go` Phase 2 review — no test gap from these commits

- **Title**: `cmd/all.go` `IncludeDisabled` propagation — no remaining gap
- **Location**: `cmd/all.go:151`
- **Severity**: LOW (informational)
- **Effort**: n/a
- **Description**: Commit `0f45d36` originally changed `cmd/all.go` to set `IncludeDisabled: allAll || len(allRoles) > 0`, but commit `2f81a30` reverted that line in favor of moving the authority semantics into `prompts.ReviewSelector.Filter` (single source of truth). The behavior is now exercised through `Filter`'s tests, so no additional `cmd/all_test.go` assertion is needed for this code path. Verified `cmd/all.go:151` is back to `IncludeDisabled: allAll`.
- **Recommendation**: None.

## 4. `code.structure` prompt edit — no test impact

- **Title**: Markdown-only change in `code.structure/report_prompt.md` needs no test
- **Location**: `defaults/roles/code.structure/report_prompt.md:44`
- **Severity**: LOW (informational)
- **Effort**: n/a
- **Description**: Commit `595a9b4` adds one bullet to the structural role's instructions. It is text-only and changes only the model's behavior at runtime, not code paths. The role-runtime contract is already covered by the prompt-loading tests in `internal/prompts/`.
- **Recommendation**: None.

# Quick Wins

1. **Add `TestEscapeTableCell` table test** covering `|`, `\n`, both, and pass-through (Finding 1) — single SMALL-effort test that protects a just-shipped bug fix; uses the same test style as the existing `TestRoleStatusDefaultsToEnabled` in `cmd/roles_test.go`.

(Only one quick win surfaced; the remainder of the recent diff is adequately tested.)

# Project Context

- **Language / build**: Go module `github.com/ateam`; `go test ./internal/prompts/... ./cmd/...` runs in ~5s; `make build` / `make test` are the project-level commands per `CLAUDE.md`.
- **Key files touched by the recent window**:
  - `cmd/roles.go` — `printRolesDocs`, new `escapeTableCell` helper.
  - `cmd/report.go` — `reviewOptionsFromReport`; behavior consolidated upstream in `prompts.Filter` (no `IncludeDisabled` setting here anymore).
  - `cmd/all.go` Phase 2 review options at line 142.
  - `internal/prompts/prompts.go` — `ReviewSelector`, new `runsEnabledGate()`, `Filter`, `ReviewFunnel`.
- **Key test files in scope**:
  - `internal/prompts/review_selector_test.go` (new in this window) — funnel-shape assertions for every `IncludeDisabled` × `Roles` × `MaxAge` combination; uses `t.TempDir()` + `Chtimes` to control mtimes.
  - `cmd/report_test.go` — `TestReviewOptionsFromReportPropagation` covers report→review option passing (zero-value default behavior still asserted).
  - `cmd/roles_test.go` — only covers `roleStatus`; no coverage of `printRolesDocs` or `escapeTableCell` yet.
- **Convention**: tests use plain `testing.T` table tests with `slices.Equal` / `reflect.DeepEqual`; no testify. Match this style for any new test.
- **Prior reports**: no existing `.ateam/roles/test.recent/history/*` — this is the first `test.recent` report for the project.
- **Out of scope reminder**: project-wide coverage gaps belong to `test.gaps`; weak-assertion patterns belong to `test.quality`.
