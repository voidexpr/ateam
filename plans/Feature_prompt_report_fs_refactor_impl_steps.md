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
- `roleMigrations`: per-role moves templated with `{role}` substitution. Includes `report.md` → `shared/report/<role>.md` (the v1 flat layout — one file per role, no per-role subdir).
- `flattenSharedLayout`: **always** runs (even when `NeedsMigration=false`). Hoists pre-flat `shared/report/<R>/<R>.md` (or the older transitional `shared/report/<R>/report.md`) to `shared/report/<R>.md`, and `shared/{review,verify,auto_setup}/<X>.md` to `shared/<X>.md`. Removes the now-empty per-role/per-action dirs. Catches projects that ran on a pre-flat binary and would otherwise stay on the nested layout forever.
- `resolveExistingTarget`: when a per-file `move()` finds the target already exists, compares content. Identical → removes source (cleanup); different → renames source to `<src>.legacy` with a warning. `moveDir` is simpler — target-exists is always a warn, no automatic merge.
- `cleanup`: drops `history/`, well-known junk files (`last_run_*.md`, `code_output.md`), and empty `roles/<R>/` + `supervisor/` dirs.

### Environment helpers (`internal/root/resolve.go`)

`ResolvedEnv` carries env state. Key surfaces:

- `Assembler()` → standard 3-anchor chain (project → org → embedded).
- `BuildAssemblerVars(promptPath, roleLabel, action)` → `MapVars` with namespaces populated for the current env. Runner-deferred placeholders (`exec.id`, `exec.batch`, `exec.timestamp`, `exec.profile`, `exec.agent`, `exec.model`, `exec.output_dir`, `exec.output_file`, `container.type`, `container.name`) resolve to the runner's literal placeholder (e.g. `{{EXEC_ID}}`) so `internal/runner/template.go::Replacer` fills them at exec time. `roleLabel=""` suppresses the `{{project.info}}` block (matches `--no-project-info`).
- `SharedDir()`, `SharedPromptDir(promptPath)` → v1 artifact destination paths.
- `RoleReportPath(roleID)` → v1 flat (`shared/report/<role>.md`). `ReviewPath`/`VerifyPath`/`AutoSetupPath` are the singleton flat siblings (`shared/review.md`, etc.). Auto-migration covers the legacy and pre-flat nested paths before these are consulted.
- `ReviewPath()`, `VerifyPath()` → v1 only (`shared/{review,verify}/...`). Same rationale.
- `applyV1LayoutMigration(projectDir, orgDir)` runs in `Resolve()` and `LookupFrom()`.

### Embedded defaults (`internal/prompts/embed.go`, `defaults/`)

- `defaults/prompts/` is the only embedded prompt tree (legacy `roles/`/`supervisor/` was dropped in commit 97b55e0).
- `defaults/embed.go` `//go:embed` directive: `prompts/*.md prompts/report/*.md prompts/code/*.md` plus the sandbox JSON.
- `prompts.AllRoleIDs` is a package-level var initialized at boot via `discoverRoleIDs()`. **Panics** on empty result or read error — surfaces an embed regression at binary startup instead of silently degrading every command.
- `embeddedFiles()` also panics on walk error or empty result. Drives `WriteOrgDefaults` / `DiffOrgDefaults`.
- `WriteOrgDefaults` strips stale pre-v1 `defaults/{roles,supervisor,*_base_prompt.md}` from upgraded orgs via `cleanLegacyOrgDefaults`. Logs each removal to stderr.
- `IsValidRole` / `AllKnownRoleIDs` / `scanV1Roles` read only the v1 path (`prompts/report/<id>.prompt.md`); auto-migration covers the legacy `roles/<id>/` shape before they run.

### Stage abstraction (`internal/stage/`, `internal/stage/actions/`)

Task 2 from the spec — **landed for 4 of 5 supervisor commands**; `report` stays hand-written for shape reasons. Captures the pre/agent-run/post envelope that the singletons share. See [Task 2: Stage abstraction](#task-2-stage-abstraction--landed-with-one-principled-exception) for the design rationale, migration status, and the Phase F decision.

- `internal/stage/stage.go`: `Stage` (static declaration), `Ctx` (per-invocation state), `Action` interface (single signature for both Pre and Post), `Executor` interface (the minimal surface needed from `*runner.AgentExecutor` — fakable in tests), `ErrSkip` sentinel.
- `internal/stage/run.go`: `Run(s Stage, c *Ctx) error` — drives Pre → BuildPrompt → BuildRunOpts → Execute (or `RunAgent` hook) → Post.
- `internal/stage/actions/actions.go`: reusable typed actions: `CheckConcurrentRuns`, `FailOnExecError`, `PrintDone`, `PrintArtifactPath`, `PrintArtifactBody`.

cmd-specific actions live in their cmd file (e.g. `printCodeSessionAction` in `cmd/code.go`) — the boundary is "shared across ≥2 stages → actions package."

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
| Nested `shared/report/<R>/<R>.md` (or `report.md`) survives instead of being hoisted to flat | `internal/migrate/v1_layout.go::flattenSharedLayout` — always runs, even when `NeedsMigration=false`. Check whether the flat `shared/report/<R>.md` already exists with different content (source kept as `.legacy`, warning surfaced) |
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

- `helpers_test.go`, `code_sessions_test.go`, `export_test.go`, `handlers_test.go` — hand-built fixtures. **All v1 flat paths** (`shared/report/<role>.md`, `shared/review.md`, `shared/code/<exec>/`).

### Verification scripts (`scripts/`)

- `capture_golden_prompts.sh` — captures `ateam prompt --inline-paths` for a representative role set into a snapshot dir. Run once before an upgrade and once after; `diff -r` to verify content preserved.
- `rewrite_defaults_vars.go` (`//go:build ignore`) — one-shot tool that walks a prompts tree and applies `assembler.RewriteContent` ALL_CAPS → dotted. Used once for the defaults; available as a template if a `ateam migrate-prompts` subcommand ever lands.

## Task 2: Stage abstraction — landed (with one principled exception)

Status: 4 of 5 supervisor commands run through the `internal/stage` abstraction. `cmd/report.go` was assessed in Phase F and left hand-written — see [Phase F: why report doesn't migrate](#phase-f-why-report-doesnt-migrate). One unplanned API addition (`Stage.RunAgent`) was the only design change after Phase A.

### Migration status

| Cmd | Phase | Commit | Notes |
|---|---|---|---|
| (skeleton) | A | `33366e8` | Stage + Ctx + Action + ErrSkip + Executor interface + Run |
| (actions) | B | `06ff449` | `CheckConcurrentRuns`, `FailOnExecError`, `PrintDone`, `PrintArtifactPath`, `PrintArtifactBody` |
| `verify` | C | `75c939f` | Simplest singleton; reference shape for the migration pattern |
| `auto_setup` + `review` | D | `b8f05f6` | Added `Ctx.Progress` for live-progress drain goroutine |
| `code` | E | `2168ac3` | Added `Stage.RunAgent` hook for the `--tail` concurrent tailer |
| `report` | F | (deferred) | Doesn't fit; stays hand-written. See below. |

`exec` and `parallel` stay out of Stage entirely, per the spec: they take a raw prompt with no fixed shape worth abstracting.

### Phase F: why report doesn't migrate

`cmd/report.go` was assessed for migration and the call was made to leave it hand-written. The mismatch is structural:

- **N parallel agent invocations via `runner.RunPool`**, not a single `Execute`. Stage's pipeline assumes one agent per invocation; `Ctx.Result` is one `*RunSummary`.
- **Per-role profile resolution** — each `runner.PoolExec` task can carry its own `AgentExecutor` override (different profile for different roles).
- **Early-exit paths before assembly** — `--auto-roles` planner returns "done" (no roles to run); `--rerun-failed` returns "no failed roles to retry".
- **Aggregate post-processing** — count succeeded, optional per-role `--print` loop, optional `--review` chain, final "Run 'ateam review'..." hint. None of this is a per-role action shape.

Three options were considered (recorded in the Phase E impl doc):
1. Add a `Stage.RunPool` variant or `Stages` plural type — significant API expansion for one consumer.
2. Use `RunAgent` for the whole pool, returning a synthetic single-`RunSummary` — wrong shape; loses per-role visibility.
3. Leave report hand-written — chose this.

The cost of forcing the migration would have been a substantial API expansion (multi-stage dispatch, per-task Pre/Post chains, aggregate post hooks) for one consumer. The cost of NOT migrating is that report's batch concerns (concurrent-runs check, budget precheck, "Running N role(s)..." line, per-role print loop) stay as inline cmd code — same shape it had before Task 2.

Net win from Task 2 stands as: the 4 supervisor singletons share a uniform envelope, the actions are tested in isolation, and Task 3 (progress telemetry) has a single hook point in `stage.Run` instead of needing to be wired across 4 cmd files.

### What this means for cmd helpers

`cmd/table.go::printArtifact` and `cmd/db.go::checkConcurrentRunsEnv` survive — they're still used by `cmd/report.go` and `cmd/parallel.go`. They are NOT redundant with `actions.PrintArtifactBody` / `actions.CheckConcurrentRuns`; the actions wrap equivalent logic but consume a `*stage.Ctx`, which is awkward to construct outside Stage. The two-implementation cost is ~30 lines and tolerable.

If `parallel` ever migrates to a hypothetical batch-Stage variant (which Phase F rejects), these cmd helpers can be retired. Not on any roadmap.

### Design decisions (the four the spec called out)

1. **Stage vs Runner relationship** — Stage *wraps* `AgentExecutor`, doesn't replace it. Runner (renamed to `AgentExecutor`, commit `8f7cba4` — see the rename's commit body for the rationale) handles the per-invocation mechanics; Stage handles the envelope around it. Clean separation.
2. **Ctx construction** — each cmd's Cobra wrapper builds the Ctx directly from its flag globals. No shared `buildCtx` helper; the variability between cmds (which subset of flags, which action label, which override fields) made the helper less useful than expected. Closures over cmd-layer locals carry the rest.
3. **Action dependency on Runner internals** — actions take a `*Ctx`, nothing else. The Ctx exposes `Executor` (interface), `Env`, `DB`, `Result`, `Prompt`, `Progress`. Actions that need `*runner.AgentExecutor` specifics (none so far) would type-assert at the call site. Minimal surface; no leaks.
4. **`exec` and `parallel` interact with Stage how?** — they don't. Their shape (raw prompt, no canonical destination, no Pre/Post hooks beyond what's inline) doesn't match. Kept out of Stage entirely.

### What ended up in Stage vs cmd

This is the key design call worth flagging — it's leaner than the spec proposed. The Stage absorbs the **agent-invocation envelope**:

- **In Stage**: `BuildPrompt`, `BuildRunOpts`, the Pre/Post chain around the Execute call, the (optional) `RunAgent` override for non-default execution mechanics.
- **In cmd (inline)**: env resolve, git-repo gate, prompt resolution + assembly, dry-run early-return, starting line, `resolveRunner` + `applyRunnerOverrides` + `setSourceWritable`, `openStateDB` + wiring DB to executor, `defer db.Close()`, progress channel + drain goroutine + cleanup defers, `cmdContext` setup.

The spec proposed making most of that cmd-inline setup into `PreAction` types (`ResolveExecutor`, `ApplyOverrides`, `SetSourceWritable`, `OpenDB`). Tried it in design; rejected because:
- Each command resolves the executor with slightly different flags (profile/agent/role label/docker-auto-setup); abstracting it would force a giant action-config struct that mirrors the cmd-options struct anyway.
- The DB needs `defer db.Close()` in cmd-layer scope; Stage.Run can't own its lifetime cleanly.
- Setup is sequential, no branching, no reuse value beyond "I wrote fewer lines in each cmd."

Result: each migrated cmd has ~30 lines of inline setup before `stage.Run(...)`. Verbose but uniform; the envelope around it is declarative.

### API surface

`Stage`:

| Field | Required | Purpose |
|---|---|---|
| `Name` | yes | User-facing label ("report", "verify", ...) |
| `Action` | yes | `agent_execs.action` value (use `runner.Action*` constants) |
| `BuildPrompt` | yes | Closure returning the prompt string from Ctx |
| `BuildRunOpts` | yes | Closure returning `runner.RunOpts` |
| `Pre` | no | Actions run before BuildPrompt/Execute |
| `Post` | no | Actions run after Execute, with `Ctx.Result` populated |
| `RunAgent` | no | Override the default `Executor.Execute` call (used by `code --tail`) |

`Ctx`:

| Field | Set by | Purpose |
|---|---|---|
| `Context` | cmd | Cancellation context for the agent run |
| `Env` | cmd | Resolved ateam env |
| `Executor` | cmd | Configured `*runner.AgentExecutor` (satisfies the `Executor` interface) |
| `DB` | cmd | Open call DB (cmd owns Close via defer) |
| `Progress` | cmd | Optional `chan<- runner.RunProgress`; cmd owns the chan's lifetime |
| `Prompt` | `Stage.Run` | Populated by `BuildPrompt`; visible to Post actions |
| `Result` | `Stage.Run` | Populated after `Execute` returns; read by Post actions |
| `Extra` | (escape hatch) | `map[string]any` for cross-action state; currently unused — candidate for deletion if Phase F doesn't claim it |

`Action`: single interface `Run(*Ctx) error`. Phase is determined by which slice on Stage holds the action. `ErrSkip` sentinel: a Pre action returns it to end the stage successfully without running the agent or any Post action.

### Effects on cmd code observed during migration

- **`cmd/table.go::printDone` deleted** (Phase E) — all 4 migrated stages route through `actions.PrintDone`. The cmd/internal duplication that existed during Phases C–E disappeared in one move when the lint pass caught it dead.
- **`printArtifact` and `checkConcurrentRunsEnv` survive** — used by the un-migrated `report.go` and `parallel.go`. Equivalent action wrappers exist (`actions.PrintArtifactBody`, `actions.CheckConcurrentRuns`) but they consume `*stage.Ctx`, awkward to construct outside Stage. Two-implementation cost is ~30 lines; tolerable.
- **First unplanned API change**: `Stage.RunAgent` (Phase E) — `code --tail` runs Execute concurrently with a DB tailer, which broke the sequential model the initial design assumed. Made the hook nullable so verify/review/auto_setup keep using the default path. The hook is the only API surface change after Phase A; encouraging signal that the abstraction is roughly the right shape.
- **Closures-over-cmd-scope works well** — `BuildPrompt`, `BuildRunOpts`, and `RunAgent` all capture cmd-layer locals (env, opts, prompt, timeout, db) without plumbing through Ctx. Originally feared the closures would be ugly; in practice they're 5–10 lines each and make the per-cmd specifics obvious next to the shared Pre/Post chain.
- **The cmd-layer setup section is converging** — every migrated cmd has the same 7-line ritual (resolve env → git gate → resolve prompts → assemble → compute timeout/paths → dry-run early-return → starting line). Tempting to factor into a helper, but the variation across cmds (which subset of prompt fields, which timeout config field, what `--dry-run` actually prints) is enough that a helper would have a 5-field options struct. Not pursuing — keep the duplication, it reads well.
- **Per-cmd custom actions** — `printCodeSessionAction` in `cmd/code.go` is the first demonstration that narrow actions can live next to the cmd that uses them. Right granularity: shared actions in the package, narrow ones inline.

### Open questions (now resolved)

- **Phase F (report) shape** — Resolved in Phase F: report stays hand-written. The three options (RunPool variant, RunAgent with synthetic result, leave hand-written) were considered; (3) was the right call. See [Phase F: why report doesn't migrate](#phase-f-why-report-doesnt-migrate).

- **`Ctx.Extra` map** — Zero users through Phase F. Strong candidate for deletion in a follow-up commit. Discourages reaching for `map[string]any` when typed action fields work.

- **`Executor` interface vs concrete `*runner.AgentExecutor`** — Held up through Phase F. No action needed `*AgentExecutor` specifics; the interface keeps stage tests fakable without spinning up a real executor. Right call.

### Test footprint

- `internal/stage/stage_test.go` — 11 unit tests against synthetic Stage + fake Executor: ordering, ErrSkip, Pre/Post error propagation, BuildPrompt error, missing executor, missing hooks, nil Ctx, progress forwarding (nil + populated), `RunAgent` override.
- `internal/stage/actions/actions_test.go` — per-action tests with a real call DB for `CheckConcurrentRuns`; stdout-capture for the print actions.
- `cmd/{verify,review,auto_setup,code}_test.go` — per-cmd happy-path tests against the mock agent, verifying the migrated path emits the right starting line + Done summary + artifact pointer.
- All existing cmd tests (TestAll*, TestVerifyWarnsWhenCheaperAndModelBothSet, TestReviewDryRun, TestCodeDryRun*) continue to pass through the migrations — the public `runXxx` contract is unchanged.

### Final tally

7 commits land the Task 2 work, on top of the prerequisite `runner.Runner` → `runner.AgentExecutor` rename (`8f7cba4`, separate concern but blocking — see its commit body for why):

```
8f7cba4 runner: rename Runner → AgentExecutor, Run → Execute
33366e8 stage: Phase A — skeleton (Stage, Ctx, Action, ErrSkip, Executor, Run)
06ff449 stage/actions: Phase B — CheckConcurrentRuns, FailOnExecError, PrintDone, PrintArtifactPath, PrintArtifactBody
75c939f cmd/verify: Phase C — first real consumer
b8f05f6 cmd/{auto_setup,review}: Phase D — added Ctx.Progress
2168ac3 cmd/code: Phase E — added Stage.RunAgent hook for --tail
a83514f plans: Phase F decision — report stays hand-written
```

Cumulative diff: ~+1000 lines new (`internal/stage/` + tests + per-cmd happy-path tests), ~-270 lines removed (`printDone` + the inline Execute/Post boilerplate across verify/review/auto_setup/code).

What's available for downstream work:
- **Task 3 hook point**: `stage.Run` in `internal/stage/run.go` is the single dispatch site for the 4 supervisor singletons. Wiring progress telemetry there once covers report-via-RunPool's mirror in `runner/pool.go` (where the per-task `Execute` calls live), so the two-file touch covers all 5 commands.
- **Stage API surface for new commands**: a new built-in workflow (e.g. a future `ateam audit`) defines `Stage{...}` + `*stage.Ctx{...}`, picks Pre/Post actions, calls `stage.Run`. No need to hand-write the envelope.
- **Test fixtures**: `internal/stage/stage_test.go::fakeExecutor` is reusable for unit-testing any new action without spinning up a real `AgentExecutor`.

What did NOT happen, deliberately:
- No public `stage` package — internal/ on purpose, per the spec's "no Go-API contract" boundary.
- No user-pluggable stages — the `Stage` declaration is Go code, not config. Spec is firm on this.
- No `runner.AgentExecutor` interface — the type stays concrete; only `stage.Executor` (the one method `stage.Run` needs) is an interface, scoped to the stage package.

## Out of scope (spec follow-ups)

These are open per `Feature_prompt_report_fs_refactor.md` but were explicitly out-of-scope for the v1 refactor:

- **Task 3 (Progress telemetry)** — `ateam exec` should emit structured JSON progress events that get persisted to `state.sqlite`; `ateam parallel` should share the same stream. Not started. Today `parallel` renders directly to the terminal in memory and `exec` doesn't emit anything structured. **Note**: Task 2's Stage abstraction is the natural single hook point for this — Phase F will land it in `stage.Run`, replacing the need to wire 5 cmd files individually.
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
