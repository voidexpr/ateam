# Implementation steps: prompt & artifact filesystem refactor

Hand-off doc for `Feature_prompt_report_fs_refactor.md`. The v1 refactor is **code-complete** on the `small-fixes` branch; `make run-ci` is green on every commit. This document covers what got built, where it lives, how to debug it, and what dual-read / compat code can still be removed in future iterations.

If you are picking this up to fix bugs, start with [Bug-hunting guide](#bug-hunting-guide). If you are picking it up to continue the cleanup arc, start with [Future cleanup: removing transitional code](#future-cleanup-removing-transitional-code).

## Pick-up notes for a new agent

- **Working dir**: `/Users/nicolas/SyncDatabox/nicmac/projects/ateam-small-fixes/`
- **Build**: `make build` (stamps ldflags) or `go build ./...` (no ldflags — `BuildTime` is "unknown")
- **Test**: `make run-ci` runs build, race-tests, fmt, tidy, vet, lint, vuln (vuln skipped offline). `make test` is the test-only subset.
- **Real-fixture migration testing**: `internal/migrate/v1_layout_test.go::TestRsyncFixture` rsyncs `~/projects/ateam/.ateam` into a tempdir, runs the migrator, asserts shape. Skips when the source isn't present (CI / fresh checkout). `TestRsyncListmanagerFixture` is the same shape against `~/projects/ateam/test_data/projects/listmanager/.ateam`.
- **Pre-v1 project for manual smoke**: `~/projects/ateam/.ateam` is the user's live ateam project. `cp -r` (or `rsync`) it to a scratch dir, then run `ateam env` / `ateam roles` / `ateam serve` from there to exercise migration end-to-end.
- **Golden-prompt comparison**: `scripts/capture_golden_prompts.sh <out_dir>` captures `ateam prompt --inline-paths` for a representative role set. Run on `main`, then on `small-fixes`, then `diff -r before/ after/` to verify per-section content was preserved.
- **CLAUDE.md** (root + `~/.claude/CLAUDE.md`) has conventions: don't commit without asking, prefer dedicated tools, use scripts not complex commands, etc.

## Architecture quick map

### Assembler (`internal/prompts/assembler/`)

Self-contained. Reads anchored `fs.FS` trees, composes per filename pattern, runs template engine with the ALL_CAPS compat shim. Stable; touch with care.

- `anchors.go`: `BuildAnchors(projectDir, orgDir, embedded)` factory. Anchors are rooted at `prompts/` within each tree (so callers reference files as `report/security.prompt.md`, never `prompts/report/...`).
- `parser.go`: filename → `(kind, role, fragment)`. Filename patterns documented in the spec.
- `assembler.go`: anchor list, `FirstMatch` (most-specific wins), `AllMatches` (additive composition).
- `assemble.go`: walks per-spec slot order (root_pre → dir_pre → role_pre → role_main → role_post → dir_post → root_post), joins with `SectionSeparator` (`\n\n---\n\n`). Accepts `*AssembleOptions{ReplaceRoleMain, PrePrompt, PostPrompt}` for CLI overrides — `nil` for the default pass-through.
- `template.go`: `{{ns.key}}` resolution + `{{include}}` / `{{include?}}` / `{{include_glob}}` + ALL_CAPS compat shim.
- `varmap.go`: closed `VarRenameMap` (ALL_CAPS → dotted) and `VarLiteralRewrites` (e.g. `SOURCE_DIR` → `.`); `RewriteContent` is the migration-style content rewriter used by `scripts/rewrite_defaults_vars.go`.
- `frontmatter.go`: strict allow-list (`description` / `deprecated` / `legacy` only).
- `orphan.go`: scans for `<role>.pre.*.md` without matching `<role>.prompt.md`, with Levenshtein hint.

### Migrator (`internal/migrate/v1_layout.go`)

Default-on; `ATEAM_NO_MIGRATE=1` opts out. Runs in `Resolve()` and `LookupFrom()` against both `projectDir` and `orgDir`.

- `staticMigrations`: per-file moves (`supervisor/review_prompt.md` → `prompts/review.prompt.md`, etc.).
- `staticDirMigrations`: per-directory moves (currently just `supervisor/code` → `shared/code`). Uses `moveDir()` — same-FS `os.Rename` only; EXDEV errors with a "move it manually" message.
- `roleMigrations`: per-role moves templated with `{role}` substitution. Includes `report.md` → `shared/report/<role>/<role>.md` (the spec filename, not the older `report.md`).
- `renameLegacyReportFiles`: **always** runs (even when `NeedsMigration=false`). Walks `shared/report/<R>/` and renames any `report.md` to `<R>.md`. Catches projects that ran on a pre-Step-6 binary, were already on v1, and would otherwise stay on the transitional filename forever.
- `resolveExistingTarget`: when a per-file `move()` finds the target already exists, compares content. Identical → removes source (cleanup); different → renames source to `<src>.legacy` with a warning. `moveDir` is simpler — target-exists is always a warn, no automatic merge.
- `cleanup`: drops `history/`, well-known junk files (`last_run_*.md`, `code_output.md`), and empty `roles/<R>/` + `supervisor/` dirs.

### Environment helpers (`internal/root/resolve.go`)

`ResolvedEnv` carries env state. Key surfaces:

- `Assembler()` → standard 3-anchor chain (project → org → embedded).
- `BuildAssemblerVars(promptPath, roleLabel, action)` → `MapVars` with namespaces populated for the current env. Runner-deferred placeholders (`exec.id`, `exec.batch`, `exec.timestamp`, `exec.profile`, `exec.agent`, `exec.model`, `exec.output_dir`, `exec.output_file`, `container.type`, `container.name`) resolve to the runner's literal placeholder (e.g. `{{EXEC_ID}}`) so `internal/runner/template.go::Replacer` fills them at exec time. `roleLabel=""` suppresses the `{{project.info}}` block (matches `--no-project-info`).
- `SharedDir()`, `SharedPromptDir(promptPath)` → v1 artifact destination paths.
- `RoleReportPath(roleID)` → v1 only (`shared/report/<role>/<role>.md`). Auto-migration covers the legacy paths before this is consulted.
- `ReviewPath()`, `VerifyPath()` → v1 only (`shared/{review,verify}/...`). Same rationale.
- `applyV1LayoutMigration(projectDir, orgDir)` runs in `Resolve()` and `LookupFrom()`.

### Embedded defaults (`internal/prompts/embed.go`, `defaults/`)

- `defaults/prompts/` is the only embedded prompt tree (legacy `roles/`/`supervisor/` was dropped in commit 97b55e0).
- `defaults/embed.go` `//go:embed` directive: `prompts/*.md prompts/report/*.md prompts/code/*.md` plus the sandbox JSON.
- `prompts.AllRoleIDs` is a package-level var initialized at boot via `discoverRoleIDs()`. **Panics** on empty result or read error — surfaces an embed regression at binary startup instead of silently degrading every command.
- `embeddedFiles()` also panics on walk error or empty result. Drives `WriteOrgDefaults` / `DiffOrgDefaults`.
- `WriteOrgDefaults` strips stale pre-v1 `defaults/{roles,supervisor,*_base_prompt.md}` from upgraded orgs via `cleanLegacyOrgDefaults`. Logs each removal to stderr.
- `IsValidRole` / `AllKnownRoleIDs` / `scanV1Roles` read only the v1 path (`prompts/report/<id>.prompt.md`); auto-migration covers the legacy `roles/<id>/` shape before they run.

### cmd layer

- `cmd/{report,review,verify,code,auto_setup,inspect,prompt}.go` — all wired through v1 helpers. Default and `--prompt` (customPrompt) paths both go through the v1 assembler via `AssembleOptions.ReplaceRoleMain`.
- `cmd/{report_v1,review_v1,code_v1}.go` — v1 helpers: `assembleRoleReportV1`, `assembleRoleCodeV1`, `assembleReviewV1`, `assembleSupervisorV1`, `assembleCodeManagementV1`, `formatReportsBlock`, `previousReportBlock`, `previewSubRunFlags`, `SubRunFlags`.
- All 7 prompt-taking commands (`report`, `review`, `code`, `verify`, `auto-setup`, `prompt`, `all`) accept `--pre-prompt TEXT` / `--post-prompt TEXT` (text or `@filepath`). Wrap order: anchors → dir-level `_pre`/`_post` → role-level pre/post → CLI `--pre-prompt` (outermost head) → … → `--post-prompt` (outermost tail).
- `--extra-prompt` stays as a separate flag, appended after the assembled body but before any outer `--post-prompt` wrap. Different position from `--post-prompt` by design — `--extra-prompt` is inside the prompt, `--post-prompt` wraps it.
- `exec` and `inspect --auto-debug` now accept `--pre-prompt` / `--post-prompt` alongside `--extra-prompt`. `exec` is raw-prompt — pre wraps at the very front, post wraps at the very end, extra sits between body and post under `# Additional Instructions`. `inspect --auto-debug` goes through the assembler and mirrors `assembleSupervisorV1`: pre/post ride through `AssembleOptions`; when `--extra-prompt` is set, post is held back and appended after the `# Additional Debug Instructions` block. `parallel` still has neither — it doesn't take a single prompt to wrap.

### Web (`internal/web/`)

- `handlers.go::handlePrompts` uses the v1 assembler via `assemblerSourcesForRole` (constructs a minimal `*root.ResolvedEnv` from `ProjectEntry`, reuses `env.Assembler()` + `env.BuildAssemblerVars`).
- `codeSessionDirs`, `scanCodeSessions` read only `shared/code/<dirName>/`.
- The legacy `prompts.TraceRolePromptSources` / `TraceRoleCodePromptSources` / `TraceReviewPromptSources` / etc. are all deleted. `PromptSource` + `DisplayPath` + `EstimateTokens` survive in `internal/prompts/trace.go` because the web templates reference them.

## Critical invariants — don't "fix" these naively

### Runtime-deferred placeholders

`BuildAssemblerVars` sets several `exec.*` and `container.*` keys to the literal runner placeholders (`{{EXEC_ID}}`, `{{OUTPUT_DIR}}`, `{{BATCH}}`, `{{CONTAINER_TYPE}}`, …) **not** to empty strings. The runner's `strings.Replacer` (`internal/runner/template.go::TemplateVars.Replacer`) fills them at exec time with the actual per-run values.

If you make these resolve to `""` at assembly time, agents get blank exec IDs / output destinations and silently fail to write. The placeholder-preservation pattern is by design; see the long comment in `resolve.go::BuildAssemblerVars`.

Adding a new runner-resolved variable requires both ends:
1. Add to `runner.TemplateVars.Replacer` (the runtime substitution).
2. Add to `BuildAssemblerVars`'s placeholder seed (the assembly-time pass-through).
3. Add to `assembler.VarRenameMap` if the user-facing form is ALL_CAPS.

### `--no-project-info` mechanism

`{{project.info}}` is referenced by the embedded `defaults/prompts/_pre.context.md`. The template engine errors on a known-namespace + missing-key combination (`template.go::MapVars.Resolve`). So `BuildAssemblerVars` always populates `vars.Project["info"]` — empty string when `roleLabel == ""` (the `--no-project-info` path). The assembler's whitespace-only filter then drops the rendered-empty `_pre.context.md` section.

Don't change the `roleLabel == ""` branch to skip the key — that re-introduces the engine error.

### Assembler section ordering for `--prompt` + `--extra-prompt` + `--post-prompt`

The v1 helpers (`assembleReviewV1`, `assembleSupervisorV1`, `assembleCodeManagementV1`, `assembleRoleReportV1`, `assembleRoleCodeV1`) handle a subtle ordering rule:

- `PrePrompt` rides through the assembler (lands before `_pre.context.md` at the very front).
- `ReplaceRoleMain` swaps in customPrompt as the role body via the assembler (framing fragments still compose).
- `extraPrompt`, manually-appended blocks (reports manifest in review, Sub-Run Flags in code, previous-report in role-report), and `PostPrompt` are appended **after** the assembler returns, in that order. `PostPrompt` is the outermost tail.

If you find yourself wanting to push `extraPrompt` or `PostPrompt` into the assembler too, double-check the manually-appended blocks come BEFORE `PostPrompt` — that's the rule that keeps the supervisor's last context as the user-supplied tail wrap.

### `prompts.AllRoleIDs` init-order

`internal/prompts/embed.go` declares `var AllRoleIDs = discoverRoleIDs()`. It runs at package load. Implications:

- Test code that wants to mutate `defaults.FS` can't shift `AllRoleIDs` after init (and Go's `embed.FS` is read-only anyway).
- Cmd-level tests that reference a specific role (e.g. `TestPromptRoleDryRun` uses `testing_basic`) depend on that role existing in `defaults/prompts/report/`. If a role is deleted/renamed in defaults, the role-presence tests pick up the change but hardcoded role names in tests don't.
- The `discoverRoleIDs` function panics on empty result. A future "discover roles dynamically" feature must plumb through a function call, not a new package-level var.

### `SectionSeparator`

`\n\n---\n\n`, exported as `assembler.SectionSeparator`. Matches the legacy join byte-for-byte. If you ever change it, golden-prompt diffs against `main` will explode; verify the change is intentional and re-snapshot.

## Bug-hunting guide

Use this section to find the right file when something misbehaves.

| Symptom | Look at |
|---|---|
| `--no-project-info` errors with "unknown key in project namespace" | `internal/root/resolve.go::BuildAssemblerVars` — ensure `vars.Project["info"]` is always set |
| User prompt with `{{ROLE}}` / `{{BATCH}}` / etc. errors at assembly | `internal/prompts/assembler/varmap.go::VarRenameMap` (compat shim) + `internal/root/resolve.go::BuildAssemblerVars` (must seed the dotted-form target key) |
| Agent writes to blank `OUTPUT_DIR` / `EXEC_ID` | `BuildAssemblerVars` resolving a runner-deferred placeholder to `""` instead of the runner's literal — restore the placeholder pass-through |
| `ateam prompt --supervisor --action code` preview doesn't match what `ateam code` sends | `cmd/code.go::runCode` vs `cmd/prompt.go::runPromptSupervisor` → both should call `assembleCodeManagementV1`. The preview passes `previewSubRunFlags(env.SourceDir)`; real run passes a populated `SubRunFlags` |
| `ateam prompt --paths` / `--inline-paths` shows different sections than the live run | `cmd/prompt.go::assembleForInspection` — synthesizes "[live]" sections to mirror what the helper appends. Per-action branch must list everything the live helper does |
| `ateam roles --docs` emits broken markdown links | `cmd/roles.go::printRolesDocs` (the link path string) |
| Web `/p/<project>/prompts` page shows stale or missing content | `internal/web/handlers.go::assemblerSourcesForRole` — uses the v1 assembler. The legacy `prompts.Trace*` are gone |
| Web `/p/<project>/code/<session>/` shows wrong files | `internal/web/handlers.go::codeSessionDirs` / `scanCodeSessions` / `buildCodeSessionEntry` |
| Migrator skips files / re-migrates / fails on conflict | `internal/migrate/v1_layout.go::move` (per-file) + `moveDir` (recursive) + `resolveExistingTarget` (conflict handler). Idempotence assertions live in `TestV1LayoutIdempotent*` |
| `ateam serve` history view lost old code sessions after upgrade | `cmd/code.go` writes to `shared/code/`; old `supervisor/code/<exec>/` should have migrated. If not, look at `staticDirMigrations` + `moveDir`. `EXDEV` cross-FS surfaces an explicit error |
| `report.md` survives instead of being renamed to `<role>.md` | `internal/migrate/v1_layout.go::renameLegacyReportFiles` — always runs, even when `NeedsMigration=false`. Check whether the user's `shared/report/<R>/` has a non-renamable `<R>.md` already on disk |
| Embedded defaults didn't get picked up | `defaults/embed.go` (the `//go:embed` directives) + `discoverRoleIDs` / `embeddedFiles` (which panic on empty result — surfaces embed regressions at boot) |
| Test failures after touching `prompts/report/*.prompt.md` | `prompts.AllRoleIDs` is built at package init; CI's `make check-docs` regenerates ROLES.md and diffs against the committed copy. Run `make docs` to refresh |
| `make run-ci` fails on `check-docs` | Run `make docs` to regenerate `ROLES.md` (`./ateam roles --docs > ROLES.md`) |
| `make run-ci` fails on `fmt-check` | Run `make fmt` (gofmt over everything). Often happens when adding tests with `\t` mismatches |
| `make run-ci` lint failure | Usually staticcheck. The fmt/lint config is checked in; obey the suggestions |

## Future cleanup: removing transitional code

Auto-migration is default-on. Once every active project has been touched at least once by a current binary, the following transitional code can go. There's no hard deadline; coordinate with the user before removing.

### Migrator transitional code (`internal/migrate/v1_layout.go`)

- **`renameLegacyReportFiles` always-runs pass** — runs on every `V1Layout()` invocation. After all projects have migrated to `<role>.md` filenames, this can be gated behind a stat check (`if shared/report/exists && contains report.md`) or dropped entirely.
- **`resolveExistingTarget` `.legacy` rename branch** — handles the case where a user has half-migrated a project manually. If you decide that scenario is so rare it's acceptable to require manual cleanup, simplify back to the original "warn + leave both in place" behavior.
- **The migrator itself** — `internal/migrate/v1_layout.go` plus `applyV1LayoutMigration` in `resolve.go` are a transitional bridge. Long-term, once every project on the user's machine has been migrated, the whole package can go. Realistically this is months out.

### Embedded-defaults cleanup (`internal/prompts/embed.go::cleanLegacyOrgDefaults`)

Runs every `WriteOrgDefaults` call (triggered by `ateam init --org` / `ateam update`). Cheap when there's nothing to clean. Could be gated by a once-per-binary-version marker file in `.ateamorg/` (`.ateamorg/.migrated-<version>`) to skip the stat calls entirely on repeat runs. Optional; current cost is negligible.

### ALL_CAPS compat shim (`internal/prompts/assembler/varmap.go`)

`VarRenameMap` + `VarLiteralRewrites` rewrite `{{ROLE}}` → `{{prompt.name}}` etc. at render time for user prompts that still use the old vocabulary. Defaults have been mechanically rewritten to the dotted form (commit 51ee9a5). The shim stays alive forever unless one of:

1. A `ateam migrate-prompts` subcommand ships that wraps `scripts/rewrite_defaults_vars.go` and runs it across the user's `.ateam/prompts/`. After that lands, the shim can be deprecated.
2. We accept the breaking change for users who haven't rewritten their prompts.

`scripts/rewrite_defaults_vars.go` is the existing tooling — `//go:build ignore`, used once for the defaults rewrite. Promoting it to a real subcommand is the cleanest path.

### Legacy `prompts.ReportFile` constant

`internal/prompts/prompts.go` still exports `ReportFile = "report.md"`. Used today by the migrator's tests, `internal/prompts/trace.go`'s test fixtures, and a few path constructors. After the migrator stops looking at the transitional filename, this can be deleted in favor of the per-role `<role>.md` convention everywhere.

### Reviewing for other dual-reads

The original Step 7 wave (commit 5d72cc5) audited and dropped every runtime dual-read. To find anything that's been added since:

```bash
grep -rn "legacy\|transitional\|dual-read\|fallback" --include="*.go" .
```

Anything new under `internal/root/`, `internal/prompts/`, `internal/web/`, or `cmd/` that looks like a path stat-then-fallback is a candidate for removal once the corresponding migration is universal.

## Test suite structure

Tests track source roughly 1:1. Conventions:

- Use `t.TempDir()` for unit tests; `mktemp -d /tmp/ateam-XXXXXX` for shell-level reproduction.
- Real-fixture tests skip when the source isn't available (`t.Skipf`); never break CI on machines without `~/projects/ateam/`.
- `cmd/` tests share `setupPromptProject`, `captureStdout`, `savePromptGlobals`/`resetPromptFlags` across files; reuse rather than duplicate.

### Assembler (`internal/prompts/assembler/`)

- `parser_test.go`, `validate_test.go` — pure-function table tests
- `assembler_test.go` — `Anchor` + `FirstMatch` / `AllMatches` over `fstest.MapFS`
- `assemble_test.go` — composition order, frontmatter strip, overload semantics, plus `TestAssemble{PrePromptWrap,PostPromptWrap,ReplaceRoleMain,ReplaceRoleMainWithoutAnchorFile,AllOverridesTogether,EmptyOverridesNoOp}` for `AssembleOptions`
- `template_test.go` — engine resolve + include directives + cycle/depth
- `varmap_test.go` — ALL_CAPS → dotted rewriting; plus `TestVarRenameMapCoversCurrentRunnerVars` (enumerates current runner vars to catch new additions that need a mapping)
- `frontmatter_test.go`, `orphan_test.go`, `anchors_test.go`
- `defaults_smoke_test.go` — integration smoke against the shipped `defaults.FS`; catches embed-glob drift early

### Migrator (`internal/migrate/v1_layout_test.go`)

- Synthetic-tree tests: `TestV1LayoutStaticMoves`, `TestV1LayoutRoleMoves`, `TestV1LayoutIdempotent`, `TestV1LayoutPreservesUnknownFiles`, `TestV1LayoutCleansJunkArtifacts`, `TestV1LayoutMovesCodeSessions`, `TestV1LayoutCodeDirTargetExists`, `TestV1LayoutSecondPassRenamesReportMd`, `TestV1LayoutTargetExistsDifferentContent`, `TestV1LayoutTargetExistsIdenticalContent`.
- **`TestV1LayoutIdempotentAfterArtifacts`** — Phase F "idempotence under load": migrate pre-v1 → write the artifacts a real `ateam report`/`review`/`code` would produce → re-migrate. Second pass must be a no-op.
- **`TestRsyncFixture`** — canonical real-repo test. rsyncs `~/projects/ateam/.ateam` (skipped if absent), runs migrator, asserts shape. Re-runs to confirm idempotence.
- **`TestRsyncListmanagerFixture`** — same shape against the listmanager test_data fixture.

### Env helpers (`internal/root/`)

- `assembler_test.go` — `SharedDir` / `SharedPromptDir` / `Assembler()` / `BuildAssemblerVars`, including `TestBuildAssemblerVarsDefersOutputPaths` for the runner-placeholder invariant.
- `migrate_v1_test.go` — `applyV1LayoutMigration` wired into `Resolve()`.
- `integration_test.go` — `TestIntegration_RelPathHelper` and friends. Note: the old `TestIntegration_3LevelPromptFallback` was deleted alongside the legacy `AssembleRolePrompt` — no v1 equivalent (3-level fallback is a legacy artifact).

### cmd (`cmd/`)

- `prompt_test.go` — `runPrompt` end-to-end, `--paths` smoke, **`TestPromptPrePostWrap`** (verifies `--pre-prompt` / `--extra-prompt` / `--post-prompt` ordering through the assembler).
- `prompt_preview_test.go` — `--paths` / `--inline-paths` / orphan detection / `promptPathForCurrentFlags` mapping.
- `review_test.go`, `report_test.go`, `all_test.go`, `code_test.go` — command-level flow with mock agent.

### Web (`internal/web/`)

- `helpers_test.go`, `code_sessions_test.go`, `export_test.go`, `handlers_test.go` — hand-built fixtures. **All v1 paths** (`shared/report/<role>/<role>.md`, `shared/review/review.md`, `shared/code/<exec>/`).

### Verification scripts (`scripts/`)

- `capture_golden_prompts.sh` — captures `ateam prompt --inline-paths` for a representative role set into a snapshot dir. Run once before an upgrade and once after; `diff -r` to verify content preserved.
- `rewrite_defaults_vars.go` (`//go:build ignore`) — one-shot tool that walks a prompts tree and applies `assembler.RewriteContent` ALL_CAPS → dotted. Used once for the defaults; available as a template if a `ateam migrate-prompts` subcommand ever lands.

## Out of scope (spec follow-ups)

These are open per `Feature_prompt_report_fs_refactor.md` but were explicitly out-of-scope for the v1 refactor:

- **Task 2 (Stage abstraction)** — internal Go cleanup that would collapse `report` / `review` / `verify` / `auto_setup` etc. onto a `Stage` type with `PreAction` / `PostAction`. None of this exists yet. Each cmd is still hand-written; the v1 helpers (`assembleRoleReportV1` etc.) sit alongside but don't reach into runner internals.
- **Task 3 (Progress telemetry)** — `ateam exec` should emit structured JSON progress events that get persisted to `state.sqlite`; `ateam parallel` should share the same stream. Not started. Today `parallel` renders directly to the terminal in memory and `exec` doesn't emit anything structured.
- **`--pre-prompt` / `--post-prompt` on `parallel`** — `parallel` takes an N-prompt batch with no single body to wrap; pre/post weren't added. `exec` and `inspect --auto-debug` now accept them (see Architecture quick map → cmd layer).

## Commit history (recent first, on `small-fixes` branch)

```
40f5f63 verification+docs: Phase F coverage + Step 9 docs
5d72cc5 cmd+web+prompts: --pre-prompt / --post-prompt + drop dual-reads (Task 8 + Step 7)
fe431ac assembler+cmd: AssembleOptions {ReplaceRoleMain, PrePrompt, PostPrompt}; drop legacy Assemble* (Step 5a)
60e98b3 runner+migrate+web: supervisor/code → shared/code (Step 4)
96425a2 runner+migrate: primary report filename → <role>.md (Step 6)
1fc6e2c prompts: delete dead legacy assemblers + their trace twins (Step 5)
95ac781 plans: record code-review/simplify wave done; revise step 5 + step 7
458d915 simplify: dedup placeholders, hoist env, collapse legacy cleanup
6503a77 code-review: fix prompt-vars, preview parity, validation, migrator, web
0b1d9c2 plans: add test-structure, out-of-scope, init-order sections
99df47d plans: update impl-steps for hand-off
97b55e0 defaults: drop dual-shipped legacy embed paths
51ee9a5 defaults/prompts: mechanical {{ALL_CAPS}} → {{dotted.form}} rewrite
eda98bd cmd/prompt: non-preview branches rewired to the v1 assembler
44a6437 cmd/prompt: rename --preview to --paths, --show-files to --inline-paths
9760755 cmd/prompt --preview: show build time for embedded files
997d02e cmd/prompt: --preview gains mod-time + token columns
1444eef cmd/prompt --show-files: interleave anchor/path markers
71fa089 prompts+root: preserve runner output placeholders
f085b7c cmd: rewire code_management, auto_roles, inspect to v1 assembler
f4e0a64 cmd/report: rewire role report assembly to v1 assembler
2cdfac5 defaults+migrate: split report base prompt into _pre.intro + _post.format
7f5ec56 cmd: rewire verify + auto_setup to v1 assembler
000c067 cmd/review + assembler: ateam review uses the v1 assembler
c89a779 migrate: drop well-known runtime junk
5f5eb68 web+migrate+root: flip migrator default-on; dual-read v1 paths in web
bad0c88 cmd+resolve+prompts: promote artifacts to .ateam/shared/
c8afc00 assembler: include singleton fragments, strip frontmatter, fail on orphans
[earlier: A, B, C foundation + dual-ship of defaults]
```
