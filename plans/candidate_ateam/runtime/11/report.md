# Summary

ATeam's docs describe a large CLI surface (~30 commands, dozens of documented flags) backed by ~680 Go tests, but several primary user workflows promised by `README.md` and `COMMANDS.md` have no test asserting the documented outcome. The most consequential gaps are: `ateam exec` stdin-auto-detection, `ateam parallel --common-prompt-first/last` assembly, the `ateam roles --docs` markdown-table contract, and the `ateam export` "three tabs" HTML contract. The only end-to-end CLI smoke harness (`test/cli/test-auth-combos.sh`) covers a single feature (secret resolution) — extending that harness is the single highest-leverage infrastructure investment for this role.

# Role

- Role: `test.blackbox`
- Model: claude-opus-4-7 (default reasoning, no extended-thinking flag)
- Methodology: read `README.md`, `COMMANDS.md`, `CONFIG.md`, `CLAUDE.md`, the per-command `Long`/`Short` cobra descriptions, and `Makefile` to enumerate documented behaviors. Listed test names (not bodies) under `cmd/` and `internal/` to find which behaviors lack an asserting test. Implementation files were consulted only to confirm the documented behavior exists in code and to name the test file that should host the new test — never to shape the recommendation itself.
- Working tree: clean at commit `ccd5003`.
- Prior `test.blackbox` reports: none (this is the first run of this role on this project).

# Findings

## 1. `ateam exec` stdin auto-detection is undocumented-by-test

- **Title**: `echo "..." | ateam exec` (no positional arg, no `-`) has no smoke test
- **Location**: documented in `COMMANDS.md:367-375` and `cmd/exec.go:44-45` (`Long` help: "if no argument is given AND stdin is piped/redirected, read stdin"). Implementation at `cmd/exec.go:355-365` (`stdinIsPiped()` gate). Existing tests in `cmd/exec_test.go` (`TestPrintExecDryRun`, `TestRunExecDryRunNoExec`) do not exercise stdin.
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: A primary user-facing entrypoint (`git diff | ateam exec --role critic_engineering`) is documented in the README's "Tips and Tricks" thinking and shown in `COMMANDS.md` examples. There is no test that pipes data to `runExec` (or a refactored equivalent that takes a `prompt-source` abstraction) and asserts the resolved prompt equals the stdin payload. A regression here is silent — `ateam exec` would print "no prompt provided" or hang, and only manual testing would catch it.
- **Recommendation**: Add `TestExecReadsStdinWhenNoArg` in `cmd/exec_test.go` that (a) replaces `os.Stdin` with a `*os.File` from `os.Pipe()` writing a fixed prompt, (b) runs `runExec` in dry-run mode with no positional argument, (c) asserts the resolved prompt body contains the piped text. If `stdinIsPiped` is hard to override under `--dry-run`, inject the stdin source via a package-level variable already used by the rest of the cmd package's tests. Pair with a negative case: no arg + no piped stdin returns the documented error `"no prompt provided: pass a prompt, @file, or pipe via stdin"`.

## 2. `ateam parallel` common-prompt assembly format is unverified

- **Title**: `--common-prompt-first` / `--common-prompt-last` produce the documented `first + "\n\n" + prompt + "\n\n" + last` format with no test
- **Location**: documented contract in `COMMANDS.md:437` ("The final prompt for each exec is: `common-first + "\n\n" + prompt + "\n\n" + common-last`"). Implementation at `cmd/parallel.go:89-106`. `cmd/parallel_test.go` has 12 tests — none assert the assembled prompt shape; `grep common-prompt cmd/parallel_test.go` returns nothing.
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: The documented prompt-assembly format is a public contract: users compose `--common-prompt-first @context.md` expecting the context to be prepended exactly per the documented separator. A future change that uses `\n` instead of `\n\n`, or that swaps the order, would silently shift behavior across every multi-prompt invocation. The bug shape is exactly what blackbox tests are designed to catch — a test that asserts the rendered prompt against the documented spec.
- **Recommendation**: Add `TestParallelCommonPromptAssembly` in `cmd/parallel_test.go` running `runParallel` with `parallelDryRun=true`, two prompts, `--common-prompt-first "CTX"`, `--common-prompt-last "POST"`, and capture stdout. Assert that stdout contains exactly `CTX\n\nprompt-1\n\nPOST` and `CTX\n\nprompt-2\n\nPOST`. Add three more cases: only-first set, only-last set, neither set (no separators added). Use the existing dry-run capture pattern from `TestParallelPoolWithMockAgent`.

## 3. `ateam roles --docs` markdown-table shape has no structural test

- **Title**: No test asserts the documented "Role | Default | Description | Prompt" 4-column row shape
- **Location**: documented in `cmd/roles.go:24-31` and via the existence of `ROLES.md` produced by `make docs`. Implementation: `cmd/roles.go:104-124`. `cmd/roles_test.go` only tests `roleStatus`, not `printRolesDocs` output.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ROLES.md` is a checked-in, generated artifact, gated only by `make check-docs` which runs `./ateam roles --docs | diff - ROLES.md`. That gate catches *any* change but does not assert the *structural invariant* a blackbox reader would expect from the docs ("each row has 4 cells, the description never breaks the column count"). The `escapeTableCell` bug (commit `ccd5003`) confirms the failure mode: a single role description containing `|` corrupted the table without breaking the check-docs golden file (the corruption was checked in until a human noticed). A blackbox-style test would fail for *any* role whose description contains a `|` or newline, regardless of whether `ROLES.md` is up to date. Note: a separate `test.recent` finding (run 9, finding 1) recommends a unit test for `escapeTableCell` covering the recent fix — that test is necessary but narrower. This finding is complementary and should be filed in addition.
- **Recommendation**: Add `TestPrintRolesDocsRowShape` in `cmd/roles_test.go` that calls `printRolesDocs()` with stdout captured (the existing `captureStdout` helper in `cmd/review_test.go` already lives in the package), scans every line after the header that begins with `| `\` `` `, and asserts the line contains exactly 5 `|` separators (i.e. 4 cells) — for every built-in role, not just the ones that happen to be in `ROLES.md` today. This catches future regressions where a new role's description introduces an unescaped `|` or newline.

## 4. `ateam export` HTML contract is under-tested

- **Title**: Documented "three tabs (Overview, Review, Code)" and "anchor-based navigation" are not asserted
- **Location**: documented in `README.md:300` ("export reports as a self-contained HTML file") and `COMMANDS.md:644` ("three tabs (Overview, Review, Code) and anchor-based navigation"). Existing tests at `internal/web/export_test.go` only assert the project name and one fragment of report content appear in the output.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam export` is the user-visible artifact for sharing ateam output outside the dev environment. The docs make three structural claims — self-contained (no external CSS/JS refs), three tabs, anchor-based nav — none asserted by tests. A change that drops the Code tab, breaks an anchor, or links to a non-bundled stylesheet ships silently.
- **Recommendation**: Extend `internal/web/export_test.go` with `TestExportHTMLStructure` that, on a fixture with at least one report, one review.md, and one code session under `.ateam/supervisor/code/`, asserts the rendered HTML contains: (a) three tab triggers labelled `Overview`, `Review`, `Code` (case-sensitive substring or `aria-controls=` attribute, whichever the implementation uses); (b) at least one `id="..."` anchor target referenced by an `href="#..."` link in the same document; (c) no `<link rel="stylesheet" href="http` or `<script src="http` (self-contained means no off-host fetches). Each assertion is a single `strings.Contains`/`strings.Count`.

## 5. `ateam version` output format is unverified

- **Title**: The 4-line `version` output (`ateam:`, `commit:`, `built:`, `system:`) has no smoke test
- **Location**: documented in `COMMANDS.md:534-543` (full output block). `cmd/version_test.go` only contains `TestFormatBuildTime` (one helper). The command itself, `runVersion` in `cmd/version.go`, is not exercised.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam version` is the canonical way users report bugs and confirm installs (called out in the install instructions). The documented format includes four labelled lines populated from build-time `-ldflags`. A change that drops a line, renames a label, or reorders fields breaks the documented support contract.
- **Recommendation**: Add `TestVersionCommandPrintsAllFourLines` in `cmd/version_test.go` that invokes `versionCmd.RunE` (or its inner function) with stdout captured and asserts the output contains exactly one line each prefixed `ateam:`, `commit:`, `built:`, `system:`. Use the documented format as the test fixture.

## 6. `ateam env` flag outputs (`--print-org`, `--claude-sandbox`) lack smoke tests

- **Title**: Two documented `env` flags emit specific outputs (path / JSON) but neither is asserted
- **Location**: documented in `COMMANDS.md:465-472`. `cmd/db_lifecycle_test.go:TestEnvShowsNotFoundForMissingPaths` covers the default `ateam env` path-discovery output only.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam env --print-org` is documented as printing the absolute org-directory path (used in shell scripts: `-v "$(ateam env --print-org)/claude_linux_shared:..."` per `COMMANDS.md:355`). `ateam env --claude-sandbox` prints the merged sandbox JSON — referenced by `CONFIG.md:127` ("Use `ateam env --claude-sandbox` to inspect the final merged sandbox settings"). Both are scriptable outputs whose contract (single absolute path on stdout; valid JSON on stdout) is undefended. A regression breaks downstream user scripts.
- **Recommendation**: Add `TestEnvPrintOrg` asserting that running `ateam env --print-org` against a fixture project writes exactly one trailing-newline-stripped absolute path to stdout that equals the fixture's org-dir. Add `TestEnvClaudeSandboxIsValidJSON` asserting the output `json.Unmarshal`s into `map[string]any` without error and contains the documented top-level keys (`sandbox`, etc. — match whatever the embedded default produces). Both belong in `cmd/db_lifecycle_test.go` next to the existing env test, or in a new `cmd/env_test.go`.

## 7. `ateam init` flag matrix is mostly untested

- **Title**: `--name`, `--role`, `--auto-setup`, `--org-create`, `--org-home` documented but uncovered
- **Location**: documented in `COMMANDS.md:38-47`. `cmd/init_test.go` has 2 tests (`TestRunInitFindsOrgFromTargetPath`, `TestRunInitPrefersProjectLocalOrg`) covering org discovery only.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `ateam init` is the documented entry point in the Quick Start. The flags drive observable outcomes: `--name myproject` should produce a `config.toml` with `name = "myproject"`; `--role testing_basic,security` should leave those two roles enabled in `config.toml`; `--auto-setup` should chain `ateam auto-setup` (visible by `setup_overview.md` being written, per `COMMANDS.md:51`). None of these are tested.
- **Recommendation**: Add to `cmd/init_test.go`: (a) `TestRunInitNameFlag` — assert `config.toml`'s `[project] name = "X"` matches the flag; (b) `TestRunInitRoleFlag` — assert the comma-separated role list is exactly the set marked `"on"` in `[roles]`; (c) `TestRunInitOrgCreateCreatesOrgDir` — assert the directory passed to `--org-create` ends with `.ateamorg/` and contains `defaults/`. `--auto-setup` would require stubbing the supervisor run and is fine as MEDIUM-effort follow-up.

## 8. `ateam install [PATH]` has no test

- **Title**: Documented to "create a `.ateamorg/` directory with default prompts, runtime config, and Dockerfile" — no test
- **Location**: documented in `COMMANDS.md:16-23`. No `cmd/install_test.go` exists.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam install` is the bootstrap for organization-level configuration. The docs make three claims about the produced directory: default prompts, runtime config (`runtime.hcl`), and `Dockerfile`. A regression that drops one of these (e.g., the `Dockerfile` because it was moved out of embedded defaults) silently breaks all downstream `ateam init` runs whose users haven't yet bootstrapped an org.
- **Recommendation**: Add `cmd/install_test.go::TestInstallCreatesOrgDirContents` that runs `runInstall` against a temp dir and asserts the produced `.ateamorg/` contains `defaults/runtime.hcl`, `defaults/Dockerfile`, and at least one `defaults/roles/<NAME>/report_prompt.md`. Use the existing `t.TempDir()` pattern from `cmd/init_test.go`.

## 9. `ateam ps` output column contract is unverified

- **Title**: 12 documented columns and the `--git-hash`-adds-2 contract are not asserted
- **Location**: documented in `COMMANDS.md:609` ("Output columns (12): `ID, STARTED, PROFILE, ACTION, ROLE, MODEL, DURATION, COST, TOKENS, STATUS, BATCH, REASON`"). Implementation in `cmd/runs.go` (no `runs_test.go`).
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam ps` is one of the most-used commands per the README. The column layout is a documented user contract and a typical place where a developer "adds one helpful column" and silently changes script-parseable output. `--git-hash` is documented to append exactly `GIT_START` and `GIT_END`.
- **Recommendation**: Add `cmd/runs_test.go::TestPsHeaderHasTwelveColumns` that seeds a single row via the existing DB helpers (`cmd/db_lifecycle_test.go` shows the pattern), runs `ateam ps`, captures stdout, parses the header row by whitespace columns, and asserts exactly the documented 12-name sequence. Add `TestPsGitHashAppendsTwoColumns` asserting the header gains exactly `GIT_START` and `GIT_END` at the right edge when `--git-hash` is set.

## 10. `ateam project-rename` has no test

- **Title**: Documented dual mode (re-register vs `--old/--new` rename) has no test
- **Location**: documented in `COMMANDS.md:625-640`. No `cmd/project_rename_test.go`.
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: This command mutates `.ateamorg/projects/` state — exactly the kind of file-system operation where a silent bug (rename to the wrong location, fail to delete the old registration) is most painful. The docs name two distinct behaviors (no-flag re-register vs `--old/--new` legacy rename) and a `--dry-run` mode.
- **Recommendation**: Add `cmd/project_rename_test.go` with: (a) `TestProjectRenameReRegistersCurrent` — set up a project under `.ateamorg/projects/old/`, move the directory, run `runProjectRename` with no flags, assert the old registration is gone and a new one matching the new path exists. (b) `TestProjectRenameOldNew` — assert that with `--old A --new B`, the directory `.ateamorg/projects/A/` is renamed to `.ateamorg/projects/B/`. (c) `TestProjectRenameDryRun` — assert no file-system changes when `--dry-run` is set but the planned actions are printed.

## 11. `ateam tail` selection flags have no test

- **Title**: `--reports`, `--coding`, `--last` selection logic on `tail` is uncovered
- **Location**: documented in `COMMANDS.md:574-591`. No `cmd/tail_test.go`.
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: `ateam tail` is documented for active debugging of a running run. The selection logic (`--reports` picks "all current report runs", `--coding` picks the latest coding session, `--last` picks the most recent) is purely a selection-set computation and is easily unit-testable with a stubbed DB and process list — same pattern as the existing `TestInspectRunSelection`.
- **Recommendation**: Mirror `cmd/inspect_test.go::TestInspectRunSelection` in a new `cmd/tail_test.go`. Seed the DB with a mix of running and finished runs, call the function that resolves `tail` flags into a set of exec_ids, and assert the resolved set matches the documented selection rule. Three small subtests cover `--reports`, `--coding`, `--last`.

## 12. `ateam update --diff` has no test

- **Title**: Documented diff-vs-embedded behavior has no test
- **Location**: documented in `COMMANDS.md:702-709`. No `cmd/update_test.go`.
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `ateam update` is the documented way to bring on-disk defaults forward when the binary advances. `--diff` is the documented preview mode. Coverage gap is real but blast radius is low (users see the diff before applying).
- **Recommendation**: Add a single `TestUpdateDiffShowsChangedPrompts` that seeds an `.ateamorg/defaults/runtime.hcl` whose content differs from the embedded copy, runs `ateam update --diff`, captures stdout, and asserts the diff output references `runtime.hcl` and contains a `-`/`+` line for the seeded difference.

## 13. Extend the CLI smoke harness (`test/cli/`) as the right home for many of the above

- **Title**: Infrastructure finding — one CLI smoke test exists; extending it gives blackbox coverage at the binary level
- **Location**: `test/cli/test-auth-combos.sh` is the only file under `test/cli/`. Driven by `make test-cli`.
- **Severity**: MEDIUM (infrastructure)
- **Effort**: MEDIUM (per script; small ongoing)
- **Description**: Several findings above (1, 2, 5, 8, 9) are testable as Go unit tests but the *true* blackbox shape — running the built `ateam` binary, with a real isolated `HOME`, and asserting documented stdout — is what the existing `test-auth-combos.sh` already does for `ateam exec` auth. A pattern is in place: isolated `TMPROOT`, `ateam init`, run a command, grep stdout. Reusing it for `ateam version` (1 line of grep), `ateam env --print-org` (path check), `ateam install` (file-existence check), and `ateam roles --docs | head -2` (header check) takes one short script per command. This is itself a finding because the alternative — table tests inside `go test` — bypasses the actual CLI plumbing (cobra wiring, exit codes, stderr separation) that real users hit.
- **Recommendation**: Add `test/cli/test-smoke-basics.sh` (mirroring `test-auth-combos.sh`'s layout) that bootstraps an isolated project and runs: `ateam version | grep -q '^ateam:'`, `ateam env --print-org | grep -q '^/'`, `ateam install /some/tmp && test -f /some/tmp/.ateamorg/defaults/runtime.hcl`, `ateam roles --docs | head -1 | grep -q '^# ATeam Built-in Roles'`. Wire it into `make test-cli` after `test-auth-combos.sh`. Subsequent commands (`ps`, `parallel --dry-run`, `exec - <<< "x"`) can be appended to the same script over time. This is a prerequisite for any further `test.blackbox` recommendation that should be exercised at the binary level rather than the package level.

# Quick Wins

1. **Finding 2** — `TestParallelCommonPromptAssembly` (SMALL). Single new test, captures `--dry-run` stdout, asserts the documented `\n\n` separator format. Protects a public prompt-assembly contract.
2. **Finding 1** — `TestExecReadsStdinWhenNoArg` (SMALL). The piping behavior is described in two doc files and shown in examples, but has zero coverage. A single os.Pipe-based test in `cmd/exec_test.go` covers it.
3. **Finding 3** — `TestPrintRolesDocsRowShape` (SMALL). Asserts the 4-cell-per-row invariant against `printRolesDocs()` output. Complements (does not duplicate) the `escapeTableCell` unit test recommended by `test.recent` in run 9.
4. **Finding 5** — `TestVersionCommandPrintsAllFourLines` (SMALL). Two minutes of test code defends the documented bug-reporting format.
5. **Finding 8** — `TestInstallCreatesOrgDirContents` (SMALL). One `t.TempDir()`, three `os.Stat` assertions, covers the bootstrap contract.

# Project Context

- **Stack**: Go module `github.com/ateam`, Go 1.25+, single binary `ateam`. Cobra-based CLI. SQLite for run state. Embedded defaults under `defaults/` (prompts, `runtime.hcl`, `Dockerfile`).
- **Documentation sources used (top-to-bottom blackbox sweep)**:
  - `README.md` — pipeline overview, isolation modes, role catalog, Quick Start.
  - `COMMANDS.md` — full per-command flag reference; the authoritative source for documented behaviors that this role works backward from.
  - `CONFIG.md` — directory layout, `config.toml` schema, runtime profiles, template variables.
  - `CLAUDE.md` / `AGENTS.md` — developer guidelines; `make test`, `make build`, `make test-docker` are the documented checks.
  - `ROLES.md` — generated; gated by `make check-docs`.
  - Per-command cobra `Long` strings (`cmd/*.go`).
- **Test layout**:
  - `cmd/*_test.go` — 24 test files for 37 cmd files. Untested cmd files: `auto_setup`, `container_cp`, `env`, `eval`, `install`, `pool_render_mpb`, `project_rename`, `projects`, `prompt`, `root`, `runs`, `serve`, `tail`, `update`, `verify`. (Note: many of these are covered indirectly via integration tests in `all_test.go` or the prompts package — but several documented user-facing behaviors above are not.)
  - `internal/**/*_test.go` — broad coverage of prompts, runner, web, streamutil, gitutil, config.
  - `test/cli/test-auth-combos.sh` — only end-to-end CLI script today; driven by `make test-cli`.
  - `test/Dockerfile.dind` + `make test-docker` — DinD harness for container-related changes.
- **Helpers worth reusing in new tests**: `captureStdout` (defined in `cmd/review_test.go`), `setupReviewFixture` (same file), `withChdir` (same), the DB-seeding helpers in `cmd/db_lifecycle_test.go`, and the `t.TempDir()` + `root.InitProject` pattern.
- **Test-style convention**: plain `testing.T` table tests with `reflect.DeepEqual`/`slices.Equal`. No testify. Match this style.
- **Tooling already configured (do not introduce parallel stacks)**: golangci-lint, govulncheck (`make vuln`), gofmt. No Cypress / Playwright / hypothesis-style tools exist; do not propose them.
- **Out-of-scope reminders for this role**: untested *functions* (no docs trail) → `test.gaps`. New diff branches without tests → `test.recent`. Existing test quality (flaky, over-mocked) → `test.quality`. Doc-vs-reality drift → `docs.followable`.
- **First report**: no prior `test.blackbox` history to merge.
