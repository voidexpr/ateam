# Implementation steps: prompt & artifact filesystem refactor (Task 1 + Task 7)

Companion to `Feature_prompt_report_fs_refactor.md`. Covers the foundational refactor — the assembler, template engine rename, auto-migration, embedded-defaults restructure, and caller rewire. Tasks 2 (Stage), 3 (telemetry), 4 (`prompt --preview`), 8 (`--pre/post-prompt` normalize) come after this lands.

Task 7 (variable rename to `scope.name`) is folded in at Phase B so embedded defaults never ship with ALL_CAPS variables in v1.

## Phase A — Foundation: assembler skeleton (no behavior change yet)

1. **New package `internal/prompts/assembler/`** (or rewrite-in-place if cleaner). Define the surface: `Assembler` constructed with `prompt_dir`, `shared_dir`, ordered list of anchors as `fs.FS`. No callers wired yet — just the type + tests against a synthetic FS.
2. **Filename-pattern parser** with the suffix-anchored rules (`.prompt.md`, `.pre[.NAME].md`, `.post[.NAME].md`, `_pre[.NAME].md`, `_post[.NAME].md`). Pure function, table-driven tests.
3. **Role-name validators**: rejects `_`-prefix, rejects `.pre`/`.post` suffix. Errors at load with the offending path.
4. **Anchor resolution primitives**: `firstMatch(name)` and `allMatches(glob)` across the anchor list, with the documented ordering (most-specific first for overload; most-general-first concatenation for composition).

## Phase B — Templating

5. **Template engine** with `{{include}}`, `{{include?}}`, `{{include_glob}}` (two-pass substitution: vars in path → resolve against anchors). Depth limit + cycle detection.
6. **Variable rename (Task 7) — do this before defaults move.** Restructure `internal/runner/template.go` into namespace dispatchers (`prompt.*`, `exec.*`, `project.*`, `git.*`, `container.*`, `env.*`, `ateam.*`, `role.*`). Unknown var in known namespace → error; unknown namespace → pass-through literal. Add `{{git.*}}` (cached per env) and `{{project.info}}` as a single computed block.
7. **Frontmatter parsing** with strict allow-list (empty in v1; unknown keys error). Lives on `<role>.prompt.md` and dir-level `_pre`/`_post`.
8. **Orphan-fragment detection** — walk all `<role>.pre*.md` / `.post*.md`, ensure a matching `<role>.prompt.md` exists somewhere; Levenshtein hint on miss.

## Phase C — Migration

9. **`internal/migrate/v1_layout.go`** with the migration map from the spec. Idempotent (re-run resumes). On partial failure: stop, leave moved files in place, print "migration paused at <step>; re-run ateam to continue."
10. **Content-rewrite pass** in the migrator: rewrite known ALL_CAPS vars in user-authored `.ateam/prompts/*.md` to dotted form. Closed mapping table (no aliases past migration).
11. **Wire migrator into `internal/root/resolve.go`** on first env materialization. One-line stderr notice on first migration.

## Phase D + E — Restructure defaults and rewire callers (combined)

Originally split, but Phase D as an isolated step provided no value: moving
defaults out from under the old readers is itself a "breaking" change, so
either we do it together with the caller rewire (Phase E) or we dual-ship
content in the embedded FS. Dual-ship wastes binary size and confuses
readers. Combined.

12. **Move `defaults/roles/<R>/report_prompt.md` → `defaults/prompts/report/<R>.prompt.md`**, same for `code/`. Move `defaults/supervisor/*` → `defaults/prompts/*.prompt.md` per the migration map. Easiest path: run the migrator (`migrate.V1Layout`) against `defaults/` and commit the result.
13. **Author `defaults/prompts/_pre.context.md`** containing `{{project.info}}` (replaces the per-cmd hardcoded `FormatProjectInfo` injection).
14. **Author `defaults/prompts/report/_pre.intro.md`** and `_post.format.md` (split the shared report framing out of `report_base_prompt.md`).
15. **Update `defaults/embed.go`** with `prompts/**/*.md` and `shared/**/*.md` globs; drop the old role/supervisor globs.
16. **`internal/root/resolve.go`** — replace direct path helpers (`RoleDir`, `SupervisorDir`, `RoleReportPath`, `ReviewPath`, `VerifyPath`) with prompt-name-based lookups via the assembler. Flip the `applyV1LayoutMigration` gate from `ATEAM_AUTO_MIGRATE=1` opt-in to "on by default, opt-out via `ATEAM_NO_MIGRATE=1`".
17. **`internal/runner/runner.go:1156`** — drop the `*_prompt.md` exclusion; update canonical destination to `SharedPromptDir(promptName)/<basename>.md`.
18. **`cmd/*.go`** — rewire `report`, `code`, `review`, `verify`, `auto_setup`, `inspect`, `prompt`, `roles` to use the assembler. Drop `RoleID: "supervisor"` hardcodes. Per-cmd `FormatProjectInfo` injection goes away (now in `_pre.context.md`).
19. **`internal/web/handlers.go`, `internal/web/export.go`** — update artifact paths to read from `shared/...` instead of `roles/<R>/...` and `supervisor/...`.

## Phase F — Verify

20. **Golden prompt test** (spec verification #3): before/after diff of `ateam prompt` output for a representative set of roles + the supervisor commands. Should be byte-identical modulo intentional ordering.
21. **Migration test**: rsync-into-tempdir approach (see below). Re-run for idempotence.
22. **`make build-all` + `make test` + `make test-cli`** then **`make test-docker`** at the end.

## Sequencing rules

- Phases A→B→C can be built and unit-tested in isolation against synthetic FSes — no other code needs to change.
- Phase D **must** happen after the variable rename (step 6) so embedded defaults never ship ALL_CAPS in v1.
- Phase E is the only "breaking" phase — the moment cmd/ switches over. Land it as one commit so bisecting works.
- Tasks 2, 3, 4, 8 come **after** Phase E lands and stabilizes.

## Testing methodology: rsync-into-tempdir against real fixtures

Synthetic fixtures cover happy paths; **real `.ateam/` trees expose the edge cases that bit us in production-ish use**. The pattern:

1. `mktemp -d` for an ephemeral workdir.
2. `rsync -a <source>/.ateam/ $TMPDIR/.ateam/` (plus the project source files the runner expects, if the test invokes real commands).
3. Run the migration / command under test against the tempdir.
4. Assert on the resulting tree; on failure leave `$TMPDIR` for inspection.
5. Tempdir lives under `./test_data/` per the CLAUDE.md rule about file-creating tests.

This way we get exposure to real complexity (org overrides, sibling files, history dirs, weirdly-named overrides) without ever touching the source `.ateam/` directories — even a buggy migrator can't corrupt them.

### Available fixtures

- **`~/projects/ateam/.ateam/`** — the most complex real-life setup. Has `roles/` (many roles, several with `report_prompt.md` + `code_prompt.md` + history), `supervisor/` (review + code_management + code_verify + auto_setup + exec_debug + review_extra_prompt.md), plus root-level cousins (`old_overview.md`, `overview.*.md`, `cranky_dev_fixer.md`, `ateam.html`). Use this as the primary migration fixture.
- **`~/projects/ateam/test_data/projects/listmanager/.ateam/`** — has `setup_overview.md` at root, exercises the migration row for that file (Pending question 1 in the spec).
- **`~/projects/ateam/test_data/projects/minimon/`**, **`ateam_wt/`** — additional sample projects.

### Wrap it in a reusable test helper

Build the rsync-into-tempdir setup as a Go test helper from day one — `internal/migrate/testutil_test.go` or similar — taking a source path and returning a tempdir. Migration tests, golden prompt tests, and end-to-end tests all call it. Avoids the "every test invents its own setup" sprawl.

### Concrete test cases to cover

- **Idempotent migration**: run twice against `~/projects/ateam/.ateam/`, second run is a no-op (no stderr notice, no file changes).
- **Partial-failure resume**: simulate a permission error mid-migration (chmod a target dir read-only), re-run after restoring perms, migration completes.
- **Content rewrite of ALL_CAPS vars**: a user prompt with `{{OUTPUT_DIR}}`, `{{EXEC_ID}}`, `{{ROLE_REPORTS}}` migrates to dotted form; literal ALL_CAPS text that isn't a known var is left untouched.
- **Anchor override after migration**: an org-level override (synthesized into the tempdir's `.ateamorg/`) still wins over embedded post-migration.
- **Golden prompt diff**: capture `ateam prompt --role <r> --action report` and `ateam prompt --supervisor --action review` against an un-migrated copy with the pre-refactor binary, then after migration assert byte-identical (modulo intentional ordering) with the new binary.
- **Roles listing parity**: `ateam roles` returns the same set before/after migration.
- **`setup_overview.md` handling**: listmanager fixture — confirm it ends up at `.ateam/shared/auto_setup/auto_setup.md`.
- **Non-migrating fixture**: a fresh `ateam init` project starts in the new layout, never triggers migration, no stderr notice.

## What this plan deliberately does NOT cover

- Stage / PreAction / PostAction refactor (Task 2) — follow-on.
- Progress telemetry + DB writes (Task 3) — independent, can start in parallel by a different person.
- `ateam prompt --preview` UI (Task 4) — needs the assembler shape from Phase A but no other dependency.
- `--pre-prompt` / `--post-prompt` normalize (Task 8) — last.
- Documentation pass (Task 5) — last, after Phases E + F are stable.
