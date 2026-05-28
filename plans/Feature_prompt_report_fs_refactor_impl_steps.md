# Implementation steps: prompt & artifact filesystem refactor

Companion to `Feature_prompt_report_fs_refactor.md`. Covers the foundational refactor — the assembler, template engine, auto-migration, embedded-defaults restructure, and caller rewire. Tasks 2 (Stage), 3 (telemetry), 8 (`--pre/post-prompt` normalize) are out of scope here.

**Mid-flight revision (commit 992ea3a):** the variable rename (Task 7 in the spec) was decoupled from the structural refactor. The new engine accepts both `{{ALL_CAPS}}` (via a compat shim through `VarRenameMap`) and `{{ns.key}}` vocabularies. Defaults stay in `{{ALL_CAPS}}` through the structural rewires; a separate mechanical pass migrates them.

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
- ✅ Runtime-deferred output placeholders preserved (`output_dir`/`output_file` default to `{{OUTPUT_DIR}}`/`{{OUTPUT_FILE}}` so the runner's template engine can substitute at exec time)

cmd/* rewires (all main paths):
- ✅ Promotion writes to `shared/` for review/verify/report (bad0c88)
- ✅ `ateam prompt --preview` via new pipeline (a3edaa3) + anchor-rooted output paths (ac6690d)
- ✅ `ateam review` (000c067)
- ✅ `ateam verify` + `ateam auto_setup` via shared `assembleSupervisorV1` helper (7f5ec56)
- ✅ `ateam report --roles X` role-templated + previous-report inline (f4e0a64)
- ✅ `ateam code` (code_management) + `ateam report --auto-roles` + `ateam ps --auto-debug` with runtime context vars (f085b7c)

### Remaining

Loose ends, ordered by impact + risk:

1. **Drop dual-shipped legacy defaults.** All callers now read from `prompts/`; the legacy `defaults/roles/`, `defaults/supervisor/`, `defaults/report_base_prompt.md`, `defaults/code_base_prompt.md` plus their embed globs can go. ~250KB binary shrink. Two-commit safety: first commit removes the embed globs and re-runs CI (catches anything that still tries `defaults.FS.ReadFile("roles/...")`); second commit deletes the files.
2. **Mechanical `{{ALL_CAPS}}` → `{{dotted.form}}` rewrite over `defaults/prompts/`.** Apply `assembler.RewriteContent` to every `.md` file under `defaults/prompts/`. Result: defaults read modern, no compat shim needed for the embedded tree. Engine compat shim stays for backward compat with user prompts that haven't been touched.
3. **`cmd/prompt.go` non-preview branches.** Still call legacy `AssembleRolePrompt` / `AssembleReviewPrompt` / etc. Convert to use the v1 helpers (`assembleRoleReportV1`, `assembleReviewV1`, etc.) so the non-preview path matches what the actual commands produce.
4. **`code.go` per-exec destination design.** Today: `CanonicalDestDir: supervisorDir/"code"/{{EXEC_ID}}`. Not in any v1 shape. Either embrace it as a special case (the only multi-artifact per-exec action) and rewire to `shared/code_runs/<exec_id>/`, or rework code as N parallel `exec` invocations and drop the special case.
5. **Drop legacy `prompts.AssembleXxx` functions.** Only the `--prompt` (customPrompt) branches in review and code still reach them, plus `cmd/prompt.go` non-preview (#3). Once those land, the entire `internal/prompts/prompts.go` body shrinks dramatically and `Assemble*Prompt` exports go away.
6. **Primary-output filename rename `report.md` → `<role>.md`.** Per spec, `shared/report/security/security.md` not `shared/report/security/report.md`. Requires threading `{{exec.output_file}}` through `PrimaryOutputName` (currently returns hardcoded `report.md`/`review.md` per `OutputKind`).
7. **Drop legacy migration leftovers from env helpers.** `RoleReportPath` / `ReviewPath` / `VerifyPath` still do `os.Stat` dual-read for pre-migration projects. Once auto-migration has shipped and projects have all been touched at least once, simplify to just the v1 path. Defer until post-release.
8. **Phase F verification.** Golden prompt diff, idempotence under real load, migration test against several real projects.
9. **Docs (Task 5).** README, CONFIG.md, ROLES.md, ISOLATION.md.

## Detailed next coding steps

Each slice is sized for one commit. Order is recommended but most are independent.

### Step 1 — Drop dual-shipped legacy defaults

**Why:** the embedded binary ships every prompt twice (legacy paths + `prompts/` paths). After all cmd/* are on the v1 pipeline, nothing reads the legacy paths from `defaults.FS`. Cleanup safe.

**Files:**
- `defaults/embed.go` — drop these globs:
  ```
  //go:embed roles/*/report_prompt.md roles/*/code_prompt.md
  //go:embed report_base_prompt.md code_base_prompt.md
  //go:embed supervisor/review_prompt.md supervisor/code_management_prompt.md ...
  ```
- After build+test confirms nothing depends on them, delete the actual files:
  - `defaults/roles/` (entire tree)
  - `defaults/supervisor/` (entire tree)
  - `defaults/report_base_prompt.md`
  - `defaults/code_base_prompt.md`

**Risk:** moderate. `internal/prompts/embed.go` calls `defaults.FS.ReadFile("roles/<R>/report_prompt.md")` in helpers used by the legacy `prompts.AssembleXxx` functions. Those functions are still callable from:
- `cmd/review.go` (--prompt branch)
- `cmd/code.go` (--prompt branch)
- `cmd/prompt.go` non-preview (#3)
- `cmd/auto_setup.go` (already converted ✓ but verify)

Need to confirm with `grep -rn "AssembleRolePrompt\|AssembleReviewPrompt\|AssembleAutoRolesPrompt\|AssembleCodeManagementPrompt\|AssembleAutoSetupPrompt\|AssembleExecDebugPrompt\|AssembleCodeVerifyPrompt" cmd/ internal/` before dropping. Either land #3 first or remove only the embed globs and leave the legacy functions wired up against `os.Open` of the legacy paths (which will now just always miss the embedded fallback — that's fine for the project/org level reads).

**Test approach:**
- `make run-ci` after each step
- rsync of `~/projects/ateam/.ateam` → tempdir → migrate → exercise all commands
- Specific check: `go test ./...` should pass, plus `ateam prompt --supervisor --action review --preview` should produce the same output as before the cleanup

**Acceptance:** binary shrinks ~250KB; nothing in `defaults/{roles,supervisor}` and no `report_base_prompt.md`/`code_base_prompt.md` at the root.

### Step 2 — Mechanical `{{ALL_CAPS}}` → `{{dotted.form}}` rewrite over `defaults/prompts/`

**Why:** the engine's compat shim resolves `{{ROLE}}` → `{{prompt.name}}` etc. at every Render call. For shipped defaults we own, the cleaner form is to just have `{{prompt.name}}` in the source. The compat shim then becomes a backward-compat-only path for user prompts.

**Approach:** existing `assembler.RewriteContent` does the work. Apply it to every `.md` file under `defaults/prompts/`.

**Files:**
- New script or one-off Go test that walks `defaults/prompts/`, calls `assembler.RewriteContent`, writes back.
- Run once, commit the rewritten files.
- All changes should be diffable as `{{ROLE}}` → `{{prompt.name}}` / `{{OUTPUT_FILE}}` → `{{exec.output_file}}` etc.

**Risk:** low. The engine handles both vocabularies identically after the shim; the only difference is what the source looks like.

**Test approach:**
- Diff before/after of a sample assembled prompt — bytes should be identical.
- `make run-ci` clean.
- Verify against rsync'd fixture: `ateam prompt --role security --action report --preview --content` should produce identical output.

**Acceptance:** zero `{{[A-Z]` matches in `defaults/prompts/` (the rewriter leaves user-emitted `{{` text alone, so no semantic changes).

### Step 3 — Convert `cmd/prompt.go` non-preview branches

**Why:** `ateam prompt --role X --action report` (without `--preview`) currently uses the legacy `AssembleRolePrompt` and prints sources via `TraceRolePromptSources`. Inconsistent with what the actual `ateam report` command produces (which now uses the v1 assembler).

**Files:**
- `cmd/prompt.go` — `runPromptRole` and `runPromptSupervisor`
- The `--files-only` flag should also work (currently uses legacy `PromptSource` shape; needs `AssembleResult.Sections` instead)

**Approach:**
```go
func runPromptRole() error {
    ... validation ...
    var prompt string
    switch promptAction {
    case runner.ActionReport:
        prompt, err = assembleRoleReportV1(env, promptRole, extraPrompt, promptIgnorePreviousReport)
    case runner.ActionCode:
        // No assembleRoleCodeV1 yet — see step 4. For now still legacy or
        // add a v1 helper.
    }
    fmt.Println(prompt)
    // --files-only: walk env.Assembler().Assemble(...).Sections and print.
}
```

For `--files-only`, repurpose the `printPromptSources` helper to take `AssembleResult.Sections` (already has anchor + path + slot) plus the rendered content for token estimation.

**Risk:** low. `cmd/prompt` is for inspection, not for running agents. Output shape changes are acceptable.

**Test approach:**
- `cmd/prompt_preview_test.go` already covers the new path; add equivalent tests for the non-preview branches.
- Spot check that `ateam prompt --role X --action report` (without preview) prints the same prompt body that `ateam report --roles X --dry-run` shows.

**Acceptance:** non-preview paths use `env.Assembler()`; `prompts.TraceXxx` functions become unused and can be deleted.

### Step 4 — code.go destination design + role-templated code v1 path

**Why:** today `cmd/code.go`'s code_management Supervisor writes to `shared/code_management/` (v1 ✓ from step 2 above), but the per-exec sub-runs it spawns write to `supervisor/code/{{EXEC_ID}}/`. The latter is the only ateam path still creating files under `supervisor/`. Two design choices:

- **A. Embrace per-exec destination.** Promote `supervisor/code/<exec>/` to `shared/code_runs/<exec>/`. Code remains the special-case multi-artifact action. Migrator gains a mapping for old `supervisor/code/<exec>/` → `shared/code_runs/<exec>/`. Update web's `latestCodeSession` and `scanCodeSessions` to read from the new location (with dual-read fallback).
- **B. Reshape code as N parallel exec invocations.** Each sub-run becomes a regular `ateam exec` with its own `--work-dir` and own `runtime/<exec_id>/`. The "execution_report.md" becomes the canonical primary output. Bigger refactor, but more consistent with the spec.

**Recommendation:** A for now (smaller, ships sooner). B is a separate design pass.

**Files for A:**
- `cmd/code.go` — change `CanonicalDestDir` to use `shared/code_runs/<exec_id>/`-style path (probably via `env.SharedPromptDir("code_runs")` + per-exec subdir)
- `internal/migrate/v1_layout.go` — add migration: `supervisor/code/` → `shared/code_runs/` (whole dir tree move)
- `internal/web/handlers.go` and `internal/web/v1_paths.go` — dual-read for code session paths
- `internal/web/code_sessions_test.go` — update assertions

**Risk:** moderate. Code sessions are user-visible runtime artifacts; mismigrating loses run history.

**Test approach:**
- New TestRsyncFixture assertion: post-migration `shared/code_runs/<exec>/` should exist and contain the old supervisor/code/<exec>/ contents.
- Manual: `ateam serve` on migrated fixture, click into Code → session detail → verify files load.

### Step 5 — Drop legacy `prompts.Assemble*` functions

**Why:** after steps 1+3+4, the only callers of `prompts.AssembleRolePrompt` / `AssembleReviewPrompt` / `AssembleCodeManagementPrompt` etc. are the `--prompt` (customPrompt) branches in review and code. Those branches use the legacy path to support wholesale prompt-body replacement — a feature the v1 assembler doesn't yet have.

**Options:**
- **A. Add `--prompt` support to the v1 assembler.** Either a "replace role main" option, or a way to inject content as the role_main slot. Then drop the legacy assembler functions entirely.
- **B. Keep legacy code only for --prompt.** Leave `prompts.AssembleReviewPrompt` and `AssembleCodeManagementPrompt` callable; delete the others (`AssembleRolePrompt`, `AssembleRoleCodePrompt`, `AssembleCodeVerifyPrompt`, `AssembleAutoSetupPrompt`, `AssembleAutoRolesPrompt`, `AssembleExecDebugPrompt`) plus the helpers they used (`readWith3LevelFallback`, `assembleSupervisorPrompt`, etc.).

**Recommendation:** B first (delete the unreferenced ones), then A (cleanest, but adds engine surface).

**Files for B:**
- `internal/prompts/prompts.go` — delete unused functions and their helpers
- `internal/prompts/trace.go` — TraceXxx functions might also be unused after step 3
- `internal/prompts/embed.go` — ParsePromptFrontmatter et al. probably still used by `ateam roles --docs`; verify before deleting

**Risk:** low if I grep carefully. `internal/prompts` is reused for `RoleMetadata`, `DiscoverReports`, `ResolveValue` / `ResolveOptional` — those stay.

**Test approach:** `go build ./...` catches any missed callers.

### Step 6 — Primary-output filename rename

**Why:** per spec, `shared/report/security/security.md` (not `report.md`). Makes the filename role-distinct and matches the spec's `<prompt-basename>.md` convention.

**Files:**
- `internal/runner/template.go` — `PrimaryOutputName(kind string)` becomes `PrimaryOutputName(kind, promptName string)` returning `<promptName>.md`. Or simpler: pass it through `RunOpts` and have `BuildTemplateVars` set `vars.OutputFile = <runtime>/<promptName>.md`.
- `internal/prompts/prompts.go` — `DiscoverReports` already dual-reads, but the v1 path expects `<role>.md` not `report.md` after this change. Update accordingly.
- `internal/migrate/v1_layout.go` — `roleMigrations[report.md]` needs to write `shared/report/<role>/<role>.md` (matching spec) instead of the current `shared/report/<role>/report.md` (matching legacy filename). Add tests for the rename.
- `cmd/review_v1.go` and `cmd/report_v1.go` — verify `env.RoleReportPath` returns the right thing post-rename.

**Risk:** moderate. Existing migrated projects have `report.md` already; need a second-pass migration to rename. Possibly do as a separate idempotent step in the migrator: walk `shared/report/<R>/report.md` and rename to `<R>.md`.

**Test approach:** Full end-to-end run of `ateam report --roles X` against a fresh project — assert the artifact lands at `shared/report/X/X.md`.

### Step 7 — Drop legacy dual-read in env helpers

**Why:** `RoleReportPath`, `ReviewPath`, `VerifyPath` still `os.Stat` the v1 path then fall back to legacy `roles/<R>/report.md` / `supervisor/review.md`. Once auto-migration has been default-on for a release and migrations have run, the legacy fallback can go.

**Defer until post-release.** Not blocking v1; cleanup for next iteration.

### Step 8 — Phase F verification

**Why:** spec called for end-to-end verification before declaring done.

**Tasks:**
- Golden prompt test: capture `ateam prompt --role <r> --action report` outputs before the refactor (i.e. on `main` branch) for a representative set of roles; after `small-fixes` lands, compare. Modulo intentional ordering changes, should be substantively the same.
- Idempotence under load: run migrator against a project, run a real `ateam report --roles X`, run the migrator again — second pass should be a no-op.
- Real-project migration parade: rsync 3+ real projects to tempdirs, migrate each, run report+review+verify against each, confirm web renders right.

### Step 9 — Documentation pass (Task 5 in spec)

Update `README.md`, `CONFIG.md`, `ROLES.md`, `ISOLATION.md` for the new layout. Reference `plans/python_framework_examples/` for "I need more than prompt content customization."
