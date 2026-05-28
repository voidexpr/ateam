# Implementation steps: prompt & artifact filesystem refactor

Companion to `Feature_prompt_report_fs_refactor.md`. Covers the foundational refactor — the assembler, template engine, auto-migration, embedded-defaults restructure, and caller rewire. Tasks 2 (Stage), 3 (telemetry), 8 (`--pre/post-prompt` normalize) from the spec are out of scope here.

**Mid-flight revision (commit 992ea3a):** the variable rename (Task 7 in the spec) was decoupled from the structural refactor. The new engine accepts both `{{ALL_CAPS}}` (via a compat shim through `VarRenameMap`) and `{{ns.key}}` vocabularies. Defaults were eventually rewritten to dotted form in Step 2; the compat shim stays for backward-compat with user prompts.

## Pick-up notes for a new agent

The branch is `small-fixes`. Everything is committed; `make run-ci` is green on every commit. Conventions you should know:

- **Working dir**: `/Users/nicolas/SyncDatabox/nicmac/projects/ateam-small-fixes/`
- **Build**: `make build` (stamps ldflags) or `go build ./...` (no ldflags — `BuildTime` is "unknown")
- **Test**: `make run-ci` runs build, race-tests, fmt, tidy, vet, lint, vuln (vuln skipped offline)
- **Test conventions**: ateam tests create real files; use `t.TempDir()` for unit tests, `mktemp -d /tmp/...` for shell-level reproduction. The `internal/migrate/v1_layout_test.go::TestRsyncFixture` pattern is the canonical "real-repo migration test" — rsync from `~/projects/ateam/.ateam` into a tempdir, exercise commands, assert results.
- **Real fixture**: `~/projects/ateam/.ateam` is the user's complex live ateam project; rsync from there to test migrations. `~/projects/ateam/test_data/projects/listmanager/.ateam` has a `setup_overview.md` that exercises a specific migration row.
- **CLAUDE.md** (root + user-level) has key project conventions: don't commit without asking unless executing a skill, prefer dedicated tools, use scripts not complex commands, etc.

### Architecture quick map

- `internal/prompts/assembler/` — the v1 assembler. Self-contained. Reads anchored fs.FS trees, composes per filename pattern, runs template engine with compat shim. Mostly stable; touch with care.
  - `parser.go`: filename → (kind, role, fragment)
  - `assembler.go`: anchor list, FirstMatch (most-specific wins), AllMatches (override-keeps-slot semantics)
  - `assemble.go`: walks per spec slot order (root_pre → dir_pre → role_pre → role_main → role_post → dir_post → root_post), joins with `SectionSeparator` (`\n\n---\n\n`)
  - `template.go`: `{{ns.key}}` resolution + `{{include}}` / `{{include?}}` / `{{include_glob}}` + ALL_CAPS compat shim
  - `varmap.go`: closed `VarRenameMap` (ALL_CAPS → dotted) and `VarLiteralRewrites` (e.g. `SOURCE_DIR` → `.`); `RewriteContent` is the migration tool
  - `frontmatter.go`: strict allow-list (`description`/`deprecated`/`legacy` only)
  - `orphan.go`: scans for `<role>.pre.*.md` without matching `<role>.prompt.md`, with Levenshtein hint
  - `anchors.go`: `BuildAnchors(projectDir, orgDir, embedded)` factory
- `internal/migrate/v1_layout.go` — auto-migrator. Default-on; `ATEAM_NO_MIGRATE=1` to suppress. `staticMigrations` + `roleMigrations` tables + cleanup of well-known runtime junk (`last_run_*.md`, `code_output.md`).
- `internal/root/resolve.go` — `ResolvedEnv` carries env state. Key helpers:
  - `Assembler()` returns a v1 Assembler with the standard anchor chain
  - `BuildAssemblerVars(promptPath, roleLabel, action)` builds a `MapVars` populated for the current env. Pass `roleLabel=""` to suppress `{{project.info}}`.
  - `SharedDir()`, `SharedPromptDir(promptPath)` — v1 destination paths
  - `RoleReportPath` / `ReviewPath` / `VerifyPath` — dual-read v1 + legacy
  - `applyV1LayoutMigration` runs in `Resolve` and `LookupFrom`
- `cmd/{report,review,verify,code,auto_setup,auto_roles,inspect,prompt}.go` — all wired to v1 helpers. The `--prompt` (customPrompt) branches in `review.go`/`code.go` still call legacy `prompts.AssembleReviewPrompt` / `AssembleCodeManagementPrompt` since the v1 assembler has no "replace role main" surface yet.
- `cmd/{report,review,report_v1,review_v1}.go` — v1 helpers `assembleRoleReportV1`, `assembleRoleCodeV1`, `assembleReviewV1`, `assembleSupervisorV1`, `formatReportsBlock`, `previousReportBlock`.

### Critical detail: runtime-deferred placeholders

`BuildAssemblerVars` sets `exec.output_dir`/`exec.output_file` to the literal strings `"{{OUTPUT_DIR}}"`/`"{{OUTPUT_FILE}}"` (NOT empty) so the runner's template engine (`internal/runner/template.go`) can substitute them at exec time with the actual `runtime/<exec_id>/...` paths. If you make these resolve to `""` at assembly time, agents get blank destinations and silently fail to write. This is by design; do not "fix" it without thinking through the exec-time substitution chain. See commit `71fa089` for the rationale comment in resolve.go.

### `--prompt` (customPrompt) branches

`cmd/review.go` and `cmd/code.go` have an `else` branch that calls the legacy `prompts.AssembleReviewPrompt` / `AssembleCodeManagementPrompt` when the user passes `--prompt <text-or-@file>`. The legacy functions short-circuit the supervisor body read when customPrompt is non-empty, so they don't need the legacy embed paths. These branches stay alive until the v1 assembler grows a "replace role main" surface — Step 5 below.

### What's `scripts/rewrite_defaults_vars.go`?

A `//go:build ignore` one-shot tool that walks `defaults/prompts/` and applies `assembler.RewriteContent` to every `.md`. Was used once for Step 2; kept in tree as documentation of how the rewrite was done. Re-run with `go run scripts/rewrite_defaults_vars.go` if a new default file ever ships with ALL_CAPS vars.

## Progress

### Done (committed on `small-fixes`)

Foundation:
- ✅ Phase A — parser + validator + Anchor/Assembler (commit 56b835b)
- ✅ Phase B — template engine + includes + frontmatter + orphan detection + varmap (56b835b)
- ✅ Phase B.5b — engine ALL_CAPS compat shim + migrator structural-only (992ea3a)
- ✅ Phase C — v1 layout migrator with real-fixture tests (a1d2b8d)
- ✅ External review fixes — singleton fragments, frontmatter stripping, preview-fails-on-orphans (c8afc00)

Assembler + defaults:
- ✅ BuildAnchors factory (65afadf)
- ✅ Dual-ship defaults at v1 paths + 3 framing files + embed.go (ac99c4b)
- ✅ Assemble walks anchors per spec order + real-defaults smoke test (527d61c)
- ✅ Section separator `\n\n---\n\n` matches legacy join (000c067)
- ✅ Split `report_base_prompt.md` into `_pre.intro.md` + `_post.format.md` per spec model (2cdfac5)

Env helpers + migration runtime:
- ✅ `ResolvedEnv.SharedDir` / `SharedPromptDir` / `Assembler` / `BuildAssemblerVars` (cc5da8c)
- ✅ Migrator default-on; web dual-read; `RoleReportPath` / `ReviewPath` / `VerifyPath` dual-read (5f5eb68)
- ✅ Migrator drops well-known runtime junk (`last_run_*.md`, `code_output.md`) (c89a779)
- ✅ Runtime-deferred output placeholders preserved — `output_dir`/`output_file` default to `{{OUTPUT_DIR}}`/`{{OUTPUT_FILE}}` so the runner's template engine can substitute at exec time (71fa089)

cmd/* rewires (all main paths):
- ✅ Promotion writes to `shared/` for review/verify/report (bad0c88)
- ✅ `ateam prompt --paths` (table) and `--inline-paths` (full prompt with per-section anchor/path/mod-time/tokens headers); `--files-only`/`--preview`/`--show-files`/`--content` all consolidated into these two modes (a3edaa3, ac6690d, 1444eef, 997d02e, 9760755, 44a6437, eda98bd)
- ✅ `ateam review` (000c067)
- ✅ `ateam verify` + `ateam auto_setup` via shared `assembleSupervisorV1` helper (7f5ec56)
- ✅ `ateam report --roles X` role-templated + previous-report inline (f4e0a64)
- ✅ `ateam code` (code_management) + `ateam report --auto-roles` + `ateam ps --auto-debug` with runtime context vars (f085b7c)
- ✅ `ateam prompt` non-preview branches use v1 helpers; `printPromptSources` deleted; new `assembleRoleCodeV1` helper for role-code preview (eda98bd)

Cleanup wave:
- ✅ **Step 2** — mechanical `{{ALL_CAPS}}` → `{{dotted.form}}` rewrite over `defaults/prompts/` (51ee9a5). 8 files touched. No semantic change.
- ✅ **Step 1** — drop dual-shipped legacy defaults (97b55e0). Removed `defaults/{roles,supervisor,*_base_prompt.md}` and their embed globs. `RoleMeta` / `discoverRoleIDs` / `IsValidRole` / `embeddedFiles` now read from `defaults/prompts/`. Pre-migration projects still validate via legacy-path fallback in `IsValidRole` / `scanLegacyRoles`. Binary ~250KB lighter.

### Remaining

Loose ends, ordered by recommended sequence:

4. **`code.go` per-exec destination design** — biggest remaining structural decision. See Step 4 detail below.
5. **Drop dead legacy `prompts.AssembleXxx` functions** — most are now unreferenced after Step 3 + the customPrompt branches being the only remaining callers. See Step 5 detail.
6. **Primary-output filename rename** — `shared/report/<R>/report.md` → `shared/report/<R>/<R>.md` per spec. See Step 6.
7. **Drop legacy dual-read in env helpers** — post-release cleanup, deferred.
8. **Phase F verification** — golden prompt diff, idempotence under load, real-project migration tests.
9. **Docs (Task 5)** — README, CONFIG.md, ROLES.md, ISOLATION.md.

## Detailed remaining coding steps

Each slice is sized for one commit. Order is recommended but most are independent.

### Step 4 — `code.go` destination design + role-templated code v1 path

**Why:** today `cmd/code.go`'s code_management Supervisor writes to `shared/code_management/` (v1 ✓), but the per-exec sub-runs it spawns write to `supervisor/code/{{EXEC_ID}}/`. The latter is the only ateam path still creating files under `supervisor/`. Two design choices:

- **A. Embrace per-exec destination.** Promote `supervisor/code/<exec>/` to `shared/code_runs/<exec>/`. Code remains the special-case multi-artifact action. Migrator gains a mapping for old `supervisor/code/<exec>/` → `shared/code_runs/<exec>/`. Update web's `latestCodeSession` and `scanCodeSessions` to read from the new location (with dual-read fallback).
- **B. Reshape code as N parallel exec invocations.** Each sub-run becomes a regular `ateam exec` with its own `--work-dir` and own `runtime/<exec_id>/`. The "execution_report.md" becomes the canonical primary output. Bigger refactor, but more consistent with the spec.

**Recommendation:** A for now (smaller, ships sooner). B is a separate design pass.

**Files for A:**
- `cmd/code.go` — change `CanonicalDestDir` to `env.SharedPromptDir("code_runs/<exec_id>")`-style path. Look at how `{{EXEC_ID}}` resolution happens in `internal/runner/runner.go::ResolveTemplateString` since `CanonicalDestDir` goes through that substitution.
- `internal/migrate/v1_layout.go` — add a "directory move" pattern (current migrator only handles single-file moves). Move `supervisor/code/` → `shared/code_runs/` as a recursive operation. Add `TestRsyncFixture` assertion that post-migration `shared/code_runs/<exec>/` contains the old contents.
- `internal/web/handlers.go` `scanCodeSessions` + `latestCodeSession` — dual-read from `shared/code_runs/` then `supervisor/code/`.
- `internal/web/v1_paths.go` — likely a new `codeSessionsDir(projectDir)` helper.
- `internal/web/code_sessions_test.go` — update fixture path assertions.

**Risk:** moderate. Code sessions are user-visible runtime artifacts; mismigrating loses run history.

**Test approach:**
- Synthetic-tree test in `internal/migrate/v1_layout_test.go` for the dir move.
- `TestRsyncFixture` extension: assert `supervisor/code/597/` (from the real ~/projects/ateam fixture) ends up at `shared/code_runs/597/` with all files intact.
- Manual `ateam serve` against migrated fixture; click Code → session detail → verify files load.

### Step 5 — Drop dead legacy `prompts.AssembleXxx` functions

**Why:** after Steps 1+3, most legacy assembler entry points are dead code. Live callers per `grep -rn "prompts\.AssembleXxx" --include="*.go" .`:

- `prompts.AssembleReviewPrompt` — `cmd/review.go` `--prompt` branch (customPrompt overrides supervisor body)
- `prompts.AssembleCodeManagementPrompt` — `cmd/code.go` `--prompt` branch (customManagement)
- *All other* `Assemble*` (`AssembleRolePrompt`, `AssembleRoleCodePrompt`, `AssembleCodeVerifyPrompt`, `AssembleAutoSetupPrompt`, `AssembleAutoRolesPrompt`, `AssembleExecDebugPrompt`) — **no live callers**, only comment references

**Options:**
- **A. Add `--prompt` support to the v1 assembler.** Either a "replace role main" option on `Assemble`, or have the helpers (`assembleReviewV1`, `assembleSupervisorV1`) accept an override string. Then drop the customPrompt branches AND all legacy `Assemble*` functions entirely.
- **B. Keep legacy code only for `--prompt`.** Delete the unused functions plus their helpers (`readWith3LevelFallback`, `assembleRoleAction`, `assembleSupervisorPrompt`, `readFileOr3Level`). Keep `AssembleReviewPrompt` + `AssembleCodeManagementPrompt`.

**Recommendation:** B first (zero-risk cleanup), then A.

**Files for B:**
- `internal/prompts/prompts.go` — delete `AssembleRolePrompt`, `AssembleRoleCodePrompt`, `AssembleCodeVerifyPrompt`, `AssembleAutoSetupPrompt`, `AssembleAutoRolesPrompt`, `AssembleExecDebugPrompt`, `assembleRoleAction`, `collectRoleExtras`, `assembleSupervisorPrompt`, `readWith3LevelFallback`, `readFileOr3Level`, `readFileWithModTime`, `formatAge`, `traceFileOr3Level` (if only used by the deleted functions)
- `internal/prompts/trace.go` — `TraceCodeVerifyPromptSources`, `TraceCodeManagementPromptSources`, `TraceReviewPromptSources` if not used by web anymore. But web/handlers.go still calls `TraceRolePromptSources` + `TraceRoleCodePromptSources`, so keep those. Verify with grep.
- `internal/prompts/prompts_test.go` — delete tests for deleted functions
- `internal/root/integration_test.go::TestIntegration_3LevelPromptFallback` — currently exercises legacy `AssembleRolePrompt`; if `AssembleRolePrompt` goes, delete or rewrite this test to exercise v1

**Risk:** low if grep is thorough. The `prompts` package keeps surface needed elsewhere: `RoleMetadata`, `DiscoverReports`, `ResolveValue` / `ResolveOptional`, `ReviewSelector`, `ReviewEmptyError`, `RoleReport`, `IsValidRole`, `AllKnownRoleIDs`, `AllRoleIDs`, `RoleMeta`, `EstimateTokens`, `FormatProjectInfo`, `ProjectInfoParams`, `ParsePromptFrontmatter`, `AutoRolesMarker`, the file constants (`ReportFile` etc.).

**Test approach:** `go build ./...` catches any missed callers; `make run-ci` validates lint+fmt.

### Step 6 — Primary-output filename rename `report.md` → `<role>.md`

**Why:** per spec, `shared/report/security/security.md` (not `report.md`). Matches the spec's `<prompt-basename>.md` convention and makes the file role-distinct.

**Files:**
- `internal/runner/template.go` — `PrimaryOutputName(kind string)` returns hardcoded `report.md`/`review.md` per `OutputKind`. Change signature to `PrimaryOutputName(kind, promptName string)` returning `<promptName>.md` for kinds where the prompt name is meaningful. OR (simpler): plumb the prompt path through `RunOpts` and have `BuildTemplateVars` set `vars.OutputFile = <runtime>/<promptName>.md` directly.
- `internal/runner/runner.go` — see `BuildTemplateVars` and the `promoteRuntimeFiles` call site at line ~777. The promotion copies all files; need to verify the agent actually wrote `<role>.md` (not `report.md`).
- `defaults/prompts/_post.format.md` and similar — the prompt text says "Write the complete report to `{{exec.output_file}}`". That variable should resolve to `<runtime>/<role>.md` so the agent writes the right filename.
- `internal/prompts/prompts.go` `DiscoverReports` — already dual-reads, but the v1 branch hardcodes `ReportFile` (which is `"report.md"`). Update to scan `<role>.md` instead.
- `internal/migrate/v1_layout.go` `roleMigrations[report.md]` — currently writes `shared/report/<role>/report.md` (to match runner). Update to `shared/report/<role>/<role>.md`. Add an idempotent rename pass for projects already migrated under the old filename: walk `shared/report/<R>/report.md`, rename to `<R>.md`.
- `cmd/review_v1.go` / `cmd/report_v1.go` — `previousReportBlock`'s `env.RoleReportPath(roleID)` needs to return the new filename. The dual-read in `env.RoleReportPath` needs to know about the new filename too.

**Risk:** moderate. Existing migrated projects have `report.md` files on disk; the second-pass migration must be reliable. Test against rsync fixtures.

**Test approach:**
- Unit test: fresh `ateam report --roles X` against a new project, assert `shared/report/X/X.md` exists.
- Migration test: pre-migration fixture with `roles/security/report.md`; migrate; assert `shared/report/security/security.md` (NOT `report.md`).
- Second-pass migration test: project that was migrated to `shared/report/security/report.md` (old filename), re-run migrator, assert renamed to `security.md`.

### Step 7 — Drop legacy dual-read in env helpers

**Why:** `RoleReportPath`, `ReviewPath`, `VerifyPath`, `IsValidRole`, `scanLegacyRoles` still do `os.Stat` dual-read for pre-migration projects. Once auto-migration has been default-on for a release and projects have all been touched at least once, the legacy fallback can go.

**Defer until post-release.** Not blocking v1; cleanup for next iteration.

### Step 8 — Phase F verification

**Why:** spec called for end-to-end verification before declaring done.

**Tasks:**
- **Golden prompt test:** capture `ateam prompt --role <r> --action report --inline-paths` outputs before the refactor (i.e. checkout `main` branch) for a representative set of roles; after `small-fixes` lands, compare. Modulo intentional section-ordering changes (extras position shifted post-Step 4-like fixes), the rendered content per section should be substantively the same.
- **Idempotence under load:** run migrator against a project, run a real `ateam report --roles X` + `ateam review`, run the migrator again — second pass should be a no-op.
- **Real-project migration parade:** rsync 3+ real projects to tempdirs (the live one in `~/projects/ateam/.ateam` plus 2 user projects of varying complexity), migrate each, run report+review+verify against each, confirm `ateam serve` web UI renders correctly.

### Step 9 — Documentation pass (Task 5 in spec)

Update `README.md`, `CONFIG.md`, `ROLES.md`, `ISOLATION.md` for the new layout:

- `README.md`: prompt customization story → edit files under `.ateam/prompts/`; reference the new anchor model (project → org → embedded).
- `CONFIG.md`: replace the old `roles/<id>/` / `supervisor/` examples with v1 paths.
- `ROLES.md`: auto-generated from `ateam roles --docs`. Re-run after defaults are stable.
- `ISOLATION.md`: verify the prompt-fs paths it mentions are still accurate.
- Reference `plans/python_framework_examples/` as the answer for "I need more than prompt content customization."

Also update `COMMANDS.md` — already updated for `ateam prompt --paths`/`--inline-paths` (commit 44a6437); verify no other commands changed surface in the refactor.

## Things to know that aren't obvious

- **The migrator runs in `Resolve()` and `LookupFrom()`** — both. So `ateam ps`, `ateam env`, anything that touches a project, triggers migration on first contact. `ATEAM_NO_MIGRATE=1` disables.
- **`internal/web` still uses legacy `TraceRolePromptSources` / `TraceRoleCodePromptSources`** for the role-detail page. These produce a list of `PromptSource` for display. Step 5 could replace them with the new assembler's `Sections` output, but it's a separate UI change.
- **`prompts.AllRoleIDs` is a package-level var** initialized at package load via `discoverRoleIDs()`. It walks `defaults.FS`'s `prompts/report/` and strips `.prompt.md`. Used by `ateam roles --docs` and various validation helpers. Don't move/rename without auditing callers.
- **`SectionSeparator = "\n\n---\n\n"`** is exported in `internal/prompts/assembler/assemble.go`. Matches the legacy join. If you ever change it, the legacy-vs-v1 byte diff comparisons in Phase F will need updating.
- **The migrator's `staticMigrations` and `roleMigrations` tables in `internal/migrate/v1_layout.go`** are the source of truth for the legacy→v1 path mapping. The spec's mapping table reflects the original design; the code's tables reflect what actually ships (with the `report.md` filename preserved per Step 6 note).
- **`internal/prompts/prompts.go` is still a big file** (~750 lines) full of legacy `Assemble*` functions. Step 5 shrinks it dramatically.
- **`internal/runner/template.go::Replacer`** has hardcoded `{{OUTPUT_DIR}}` / `{{OUTPUT_FILE}}` / `{{EXECUTION_DIR}}` substitutions that run at exec time. The new assembler emits these as literal placeholder strings (see "Critical detail" above), so the runner template does the final substitution. Don't remove the runner template's substitution without also having the assembler resolve them with real values.

## Commit history (recent first, on `small-fixes` branch)

```
97b55e0 defaults: drop dual-shipped legacy embed paths
51ee9a5 defaults/prompts: mechanical {{ALL_CAPS}} → {{dotted.form}} rewrite
eda98bd cmd/prompt: non-preview branches rewired to the v1 assembler
44a6437 cmd/prompt: rename --preview to --paths, --show-files to --inline-paths
9760755 cmd/prompt --preview: show build time for embedded files
997d02e cmd/prompt: --preview gains mod-time + token columns; drop --files-only
1444eef cmd/prompt --show-files: interleave anchor/path markers
ac6690d cmd/prompt --preview: anchor-rooted paths in output
71fa089 prompts+root: preserve runner output placeholders
f085b7c cmd: rewire code_management, auto_roles, inspect to v1 assembler
f4e0a64 cmd/report: rewire role report assembly to v1 assembler
2cdfac5 defaults+migrate: split report base prompt into _pre.intro + _post.format
7f5ec56 cmd: rewire verify + auto_setup to v1 assembler
000c067 cmd/review + assembler: ateam review uses the v1 assembler
c89a779 migrate: drop well-known runtime junk
ac6690d cmd/prompt --preview: anchor-rooted paths
5f5eb68 web+migrate+root: flip migrator default-on; dual-read v1 paths in web
bad0c88 cmd+resolve+prompts: promote artifacts to .ateam/shared/
c8afc00 assembler: include singleton fragments, strip frontmatter, fail on orphans
[earlier: A, B, C foundation + dual-ship]
```
