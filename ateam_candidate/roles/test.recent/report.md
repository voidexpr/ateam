# Summary

Four new commits landed since the previous report (10 hours ago); the working tree is clean. The behavior-changing commit is `386e62d` (codex arg reorder + `ateam update` fix), which updated `internal/agent/codex_test.go` correctly to cover the reordered argv and updated `internal/runtime/config_test.go` for the pricing rename. The remaining commits are prompt/markdown/Python script changes with no Go code impact and no test obligation. Two gaps survive: the previously-flagged `escapeTableCell` regression test (commit `ccd5003`) and a new gap — the freshly-added `runtime.DiffOrgDefaults` has no unit test for its three branches (missing / changed / error reading embedded).

# Role

- Role: `test.recent` (Recent Changes Test Coverage)
- Model: claude-opus-4-7 (default reasoning, no extended thinking flag set in invocation)
- Scope: commits since the prior report at `2026-05-14_06-19-38` — `22c0cac`, `1516d06`, `386e62d`, `6dcf9a0` — plus carry-forward of unresolved findings from the previous run (`ccd5003`). Working tree clean.
- Tests not re-executed (per base-prompt guidance for non-tool-output roles); commit message for `386e62d` records `make build` and `make test -race` green.

# Findings

## 1. `runtime.DiffOrgDefaults` — new exported function, no unit test

- **Title**: New `DiffOrgDefaults` in `internal/runtime/config.go` has no test for any of its three branches
- **Location**: `internal/runtime/config.go:521-548` (`RuntimeDiff` struct + `DiffOrgDefaults` function). Test should live in `internal/runtime/config_test.go`.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Commit `386e62d` adds `runtime.DiffOrgDefaults` as a sibling to `prompts.DiffOrgDefaults`, and `cmd/update.go:57` now relies on it to decide whether the update command's early-return fires. The function has three distinct branches: (a) file absent on disk → `"missing"`; (b) embedded read error → silently skipped; (c) `strings.TrimSpace`-normalized content differs → `"changed"`. None of these is exercised by a test. The bug the commit fixes (`ateam update` skipping the runtime write when only HCL changed) was precisely a "branch B was wrong" bug — there's no regression test that would have caught it, and no test that protects the corrected branch logic now. The function is small enough that a `t.TempDir()`-based table test mirroring the style of `TestPricingBlockParsed` in the same file is appropriate. Note that `prompts.DiffOrgDefaults` also lacks a unit test, so this gap is consistent with prior project hygiene; however, the recently-discovered bug raises the priority on at least the runtime variant.
- **Recommendation**: Add `TestRuntimeDiffOrgDefaults` to `internal/runtime/config_test.go` covering: (1) `defaults/runtime.hcl` and `defaults/Dockerfile` both absent from `orgDir` → both reported `missing`; (2) on-disk copy identical to embedded → empty diff; (3) on-disk copy whitespace-only different → empty diff (asserts the `strings.TrimSpace` normalization); (4) on-disk copy with a semantic edit (e.g. a trailing `// changed` line) → reported `changed`. Use `t.TempDir()` and write files with `os.WriteFile`; pull the embedded reference via `defaults.FS.ReadFile`. No need to mirror this on the `prompts` side in the same change — call it out as a follow-up only.

## 2. `escapeTableCell` bug fix still has no regression test (carry-over)

- **Title**: `roles --docs` `|`-escape fix lacks a regression test
- **Location**: `cmd/roles.go:126-133` (`escapeTableCell`); used at `cmd/roles.go:117` inside `printRolesDocs`. Test file would be `cmd/roles_test.go` (currently only tests `roleStatus`).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: Commit `ccd5003` ("roles --docs: escape `|` in descriptions; preserves markdown table rows") is a bug fix: a role whose `description:` frontmatter contains a literal `|` (e.g. `code.structure` — "tagged by scope (local | module | architecture)") was breaking the generated markdown table by introducing extra columns. The fix adds `escapeTableCell` and the change also flattens newlines to spaces. The diff adds no test, and none of the four commits since have addressed it. The pre-fix bug had a concrete reproducer (`code.structure`'s description), so a regression test is cheap and direct. Without one, a future "simplification" of `printRolesDocs` could silently reintroduce the corruption — the existing `ROLES.md` only catches it if a human re-reads the table after `make docs`.
- **Recommendation**: Add a unit test in `cmd/roles_test.go` named `TestEscapeTableCell` covering: (a) input with `|` becomes `\|`; (b) input with `\n` becomes a single space; (c) input with both is handled; (d) input with no special chars is unchanged. The function is small enough that the table-test form is the right shape — two or three rows max. Style: plain `testing.T` table test, no testify (consistent with the rest of `cmd/*_test.go`).

## 3. Codex arg-reorder is adequately tested

- **Title**: `codex exec --json <args> <prompt>` reorder — covered
- **Location**: `internal/agent/codex.go:66, 79-84` (run + DebugCommandArgs); `internal/agent/codex_test.go:21-39` (`TestCodexAgentDebugCommandArgs`)
- **Severity**: LOW (informational — no gap)
- **Effort**: n/a
- **Description**: Commit `386e62d` reorders codex argv so all configured args are passed after the `exec` subcommand. The four table-test cases in `TestCodexAgentDebugCommandArgs` were updated in the same commit to assert the new order across all four (model, effort) combinations. The base `Args` were also switched from `--ask-for-approval never` (a top-level flag that wouldn't have caught the reorder bug) to `--sandbox workspace-write` (an exec-scoped flag that does). The runtime-path (`run()` at codex.go:79) shares the same argv-construction expression with `DebugCommandArgs` (`append([]string{"exec", "--json"}, codexFlagArgs(...))`), so the existing test transitively covers it. No new test needed.
- **Recommendation**: None.

## 4. `cmd/update.go` combined-diff early-return — informational

- **Title**: `runUpdate` early-return now sums prompt + runtime diffs; CLI flow not unit-tested
- **Location**: `cmd/update.go:53-65`
- **Severity**: LOW (informational)
- **Effort**: n/a
- **Description**: Commit `386e62d` changes `runUpdate` to compute `total := len(promptDiffs) + len(runtimeDiffs)` and gate the early-return on the sum (the original bug). The function is a thin CLI controller that calls `prompts.WriteOrgDefaults` / `runtime.WriteOrgDefaults`; the project does not currently unit-test `cmd/update.go`, and bringing it under test would require carving out a writer-injection seam (LARGE effort). The behavior is sufficiently protected once Finding 1 lands (a `runtime.DiffOrgDefaults` unit test covers the underlying branch the controller depends on).
- **Recommendation**: None for this report. If a `cmd/update_test.go` is ever introduced for unrelated reasons, add one case where `runtime.hcl` differs but no prompts differ to lock in the early-return fix.

## 5. `writeCmdFile` markdown fences — extend the existing assertion

- **Title**: `cmd.md` Env-section code-fence wrapping has no assertion in `TestWriteCmdFileIncludesRunDetailsAndFilesCopy`
- **Location**: `internal/runner/runner.go:1071, 1082, 1101` (the three new ``` ` `` ` `` `` writes); `internal/runner/runner_test.go:405-510` (existing test)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Commit `386e62d` wraps the `## Inherited` and `## Specified` env sections in fenced code blocks for markdown viewers. The existing `TestWriteCmdFileIncludesRunDetailsAndFilesCopy` already asserts the env contents, but does not assert the fences themselves. A renderer regression that re-introduces the unfenced layout would not fail any test. This is a "one more row in an existing table" opportunity — fits the "extend an existing assertion" calibration explicitly listed as LOW in the role instructions.
- **Recommendation**: Add two strings to the existing `for _, want := range []string{ ... }` slice in the test: one asserting that `## Inherited\n```` appears, one asserting that `## Specified\n```` appears. No new test function.

## 6. Prompt-only commit — no test impact

- **Title**: `6dcf9a0` ("prompts: fix three internal contradictions") changes no Go code
- **Location**: `defaults/new_report_base_prompt.md`, `defaults/roles/project.maintenance/report_prompt.md`, `defaults/supervisor/review_prompt.md`
- **Severity**: LOW (informational)
- **Effort**: n/a
- **Description**: The diff is markdown text in default prompt files. The prompt-loading contract is already covered by tests under `internal/prompts/`. No Go behavior changed; no test obligation.
- **Recommendation**: None.

## 7. Python script and docs commits — out of scope

- **Title**: `22c0cac` (`scripts/claude-usage.py --max-sleep`) and `1516d06` (`plans/ResearchAgentFramework.md`) require no Go test changes
- **Location**: `scripts/claude-usage.py`, `plans/ResearchAgentFramework.md`
- **Severity**: LOW (informational)
- **Effort**: n/a
- **Description**: `claude-usage.py` is a standalone helper script with no Python test suite in the project. `ResearchAgentFramework.md` is a planning document. Neither affects the Go codebase's test contract.
- **Recommendation**: None. (If `scripts/` ever grows a `tests/` directory, the new `--max-sleep` branch would deserve coverage.)

# Quick Wins

1. **Add `TestRuntimeDiffOrgDefaults` to `internal/runtime/config_test.go`** (Finding 1) — locks in the just-fixed `ateam update` bug and covers the three branches of the new function in one SMALL table test.
2. **Add `TestEscapeTableCell` to `cmd/roles_test.go`** (Finding 2) — protects the `roles --docs` table-corruption fix from regression; carried forward from the previous report and still unresolved.
3. **Extend `TestWriteCmdFileIncludesRunDetailsAndFilesCopy` with two `want` strings asserting the new ```` ``` ```` fences** (Finding 5) — single-line additions to an existing assertion list; locks the markdown layout.

# Project Context

- **Language / build**: Go module `github.com/ateam`. Commit messages indicate `make build` and `make test -race` are the level the project relies on; per `CLAUDE.md`, run `make test` after code changes and `make test-docker` only when agent/container/runner code or deps change.
- **Files touched in this window**:
  - `internal/agent/codex.go`, `internal/agent/agent.go` — codex argv reorder (tested in `internal/agent/codex_test.go`).
  - `internal/runtime/config.go` — new `RuntimeDiff` + `DiffOrgDefaults` (no test yet, see Finding 1).
  - `internal/runtime/config_test.go` — `TestPricingBlockParsed` updated for the `gpt-5.4` rename.
  - `cmd/update.go` — combined diff early-return (CLI controller; no unit-test seam).
  - `internal/runner/runner.go` — `writeCmdFile` env code fences (existing test in `runner_test.go` should be extended, see Finding 5).
  - `defaults/runtime.hcl` — codex args + pricing model renames.
  - `defaults/*.md` and `scripts/claude-usage.py`, `plans/ResearchAgentFramework.md` — out of Go test scope.
- **Carry-over from prior `test.recent` report**:
  - `cmd/roles.go::escapeTableCell` (commit `ccd5003`) still lacks the regression test recommended in the previous report — re-included as Finding 2.
- **Key test files in the affected area**:
  - `internal/agent/codex_test.go` — `TestCodexAgentDebugCommandArgs`, refreshed in this window to assert the new argv order.
  - `internal/runtime/config_test.go` — `TestPricingBlockParsed` and surrounding HCL-parse tests; the natural home for a new `TestRuntimeDiffOrgDefaults`.
  - `internal/runner/runner_test.go` — `TestWriteCmdFileIncludesRunDetailsAndFilesCopy` is the existing assertion-list test for `cmd.md` output.
  - `cmd/roles_test.go` — currently only `TestRoleStatusDefaultsToEnabled`; gain a `TestEscapeTableCell` here.
- **Convention**: plain `testing.T` table tests with `slices.Equal` / `reflect.DeepEqual`; no testify. Match this style for any new test.
- **Out-of-scope reminder**: project-wide coverage gaps belong to `test.gaps`; weak-assertion patterns belong to `test.quality`. This report stays inside the recent diff.
