# Summary

Trace-mode follow of the documented install / quick-start / common-task paths. Most procedures execute cleanly: `git clone https://github.com/voidexpr/ateam.git` matches the actual remote, the `install.sh` module check matches `go.mod` (`module github.com/ateam`), and every Makefile target referenced from the docs is declared. Two literal-reader failures will bite agents that copy-paste: a typo command in the Quick Start (`ataem serve`) and an `ateam parallel` example in `FAQ.md` that uses a `--prompt` flag the binary does not accept. Several smaller ambiguities and one self-contradictory eval example round out the findings.

Role: `docs.followable`, model `claude-opus-4-7`, trace mode only (no execute mode this run — no sandbox provisioned, and the report skill is read-only).

# Findings

## 1. Quick Start typo: `ataem serve` — command will fail when copy-pasted

- **Location**: `README.md:78`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: The Quick Start block ends with `ataem serve              # local web server to browse documents, processes, cost`. An LLM agent following the doc literally would execute `ataem serve` and get `command not found`. This is the last command in the very first runnable code block a new user encounters.
- **Recommendation**: Replace `ataem serve` with `ateam serve` on that line.

## 2. `ateam parallel` example in FAQ uses a non-existent `--prompt` flag

- **Location**: `FAQ.md:21`
- **Severity**: HIGH
- **Effort**: SMALL
- **Description**: The example shows `ateam parallel --prompt "take care of problem 1" --prompt "take care of problem 2" --prompt "take care of problem 3"`. `ateam parallel` takes prompts as **positional arguments** (verified in `cmd/parallel.go` — only `--labels`, `--batch`, `--common-prompt-first`, `--common-prompt-last`, `--model`, etc. are declared; no `--prompt` flag exists). `COMMANDS.md:404` shows the correct form: `ateam parallel "analyze auth module" "analyze payment module"`. An agent that copy-pastes the FAQ example will fail with an unknown-flag error.
- **Recommendation**: Rewrite the example to use positional prompts, e.g. `ateam exec @./decide.md && ateam parallel "take care of problem 1" "take care of problem 2" "take care of problem 3" && ateam exec "verify ..."`.

## 3. EVAL.md final example contradicts itself: `--judge-model sonnet --no-judge`

- **Location**: `EVAL.md:158-159`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The "Cheap iteration" example combines `--judge-model sonnet` with `--no-judge`. `--no-judge` is documented (same file, line 112) as "Skip the LLM judge; print cost comparison only", which makes `--judge-model` meaningless in the same invocation. A literal reader / agent cannot tell which the example actually intends — judge enabled with sonnet, or no judge at all.
- **Recommendation**: Either drop `--no-judge`, or drop `--judge-model sonnet`, depending on the intent. Likely the latter — the surrounding text talks about cost-comparison-only runs.

## 4. install.sh prints `./ateam` instead of a real version after build

- **Location**: `install.sh:86`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `ok "Built: $(./ateam --version 2>/dev/null || echo './ateam')"`. The binary has no `--version` flag (`cmd/version.go` declares `Use: "version"` as a subcommand and Cobra does not add a global `--version`). The fallback `echo './ateam'` always wins, so the installer's success line is uninformative ("Built: ./ateam"). Not a failure, but a literal reader/agent debugging an install will see misleading output.
- **Recommendation**: Replace `./ateam --version` with `./ateam version | head -1` (which prints `ateam:  <version>`).

## 5. Quick Start does not say which test command runs the build verification

- **Location**: `README.md:52-95` (Quick Start) and the gap before `Prerequisites` at line 97
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: After `./install.sh`, the next thing many users / agents want is "did it actually work?". Three test targets exist (`make test`, `make test-docker`, `make test-docker-live`), and the docs don't tell a first-time user which to run as a smoke test. `make test-docker-live` is destructive in the budget sense (~$0.03 of Claude API per the Makefile and DEV.md) — an agent that interprets "run the tests" liberally could choose it. `DEV.md` distinguishes them but is two clicks away.
- **Recommendation**: Add one line in Quick Start (or right after `make build` in Manual Install) such as: `make test    # quick unit-only smoke test; do NOT run 'make test-docker-live' — it costs real API credits`.

## 6. README workflow example references `testing_full` without warning it is `off` by default

- **Location**: `README.md:152-155`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The "Weekly (thorough)" example runs `ateam all --roles security,dependencies,testing_full`. `testing_full` is `off` in `defaults/config.toml:60`. Per recent commit (`2f81a30 review: --roles is authoritative everywhere`) the explicit `--roles` flag overrides the enabled gate, so the command works. But a literal reader without that context might wonder why their `config.toml` has `testing_full = "off"` and not realize the flag bypasses it.
- **Recommendation**: Add a brief note under Workflow Examples: "Roles listed in `--roles` run even if disabled in `config.toml`."

## 7. README "Quick Start" implies `ateam init` is the first step, but Prerequisites come after

- **Location**: `README.md:52-100` (Quick Start at lines 52-95, Prerequisites at lines 97-101)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A literal reader follows steps top-to-bottom: clones, installs, runs `ateam init`, then `ateam report` — and only then encounters "Prerequisites: Claude Code CLI or Codex CLI installed and authenticated". An agent without `claude` or `codex` authenticated will get past `ateam init` (it doesn't need an agent) and fail at `ateam report`. `install.sh` does warn at line 130-133 if `claude` is missing, but the README's reading order still puts the prerequisite after the procedure that needs it.
- **Recommendation**: Move the Prerequisites section above the Quick Start block, or add a one-line note at the top of Quick Start: "Requires Go 1.25+ and an authenticated `claude` or `codex` CLI — see Prerequisites below."

## 8. README "Manual Install" `sudo ln -s` step has no Linux/macOS branching, may collide with install.sh's default location

- **Location**: `README.md:105-110`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Manual Install symlinks into `/usr/local/bin`, while `install.sh` defaults to `$HOME/.local/bin` (line 7 of install.sh). A user who runs both will end up with two symlinks. An agent picking between the two will not know they're equivalent. Minor.
- **Recommendation**: Either note that `install.sh` uses `~/.local/bin` so Manual Install is only needed when symlinking system-wide is preferred, or align both to the same location.

# Quick Wins

1. **`README.md:78` typo `ataem serve` → `ateam serve`** (Finding 1). SMALL effort, HIGH severity — the very first runnable block.
2. **`FAQ.md:21` rewrite the `ateam parallel` example to use positional prompts** (Finding 2). SMALL effort, HIGH severity — agents copy-pasting from FAQ will get an unknown-flag error.
3. **`EVAL.md:158-159` drop `--no-judge` or `--judge-model` from the contradictory example** (Finding 3). SMALL, MEDIUM.
4. **`README.md` add a one-line "Roles in `--roles` run even if `off` in config.toml" note** (Finding 6) and reorder Prerequisites above Quick Start (Finding 7). SMALL, MEDIUM combined.

# Project Context

- **Audit scope (this role)**: experience of following documented procedures, not doc-content quality. Doc-quality findings belong to `docs.external` / `docs.internal`.
- **Top-level user docs**: `README.md` (381 lines, primary entry point), `COMMANDS.md` (766 lines, command/flag reference, authoritative for syntax), `CONFIG.md` (498 lines), `ISOLATION.md` (532 lines), `DEV.md` (364 lines, dev/test setup), `EVAL.md` (160 lines), `FAQ.md` (76 lines), `ROLES.md` (auto-generated by `ateam roles --docs`).
- **Install path**: `install.sh` checks for Go ≥ 1.25, ensures the script is run from the ateam repo by grepping `module github.com/ateam` in `go.mod` (matches), runs `make build`, symlinks to `~/.local/bin/ateam`. Quick Start clone URL `https://github.com/voidexpr/ateam.git` matches the real origin remote.
- **Build/test targets used by docs (all present in `Makefile`)**: `build`, `companion`, `test`, `test-docker`, `test-docker-live`, `test-all`, `lint`, `fmt`, `fmt-check`, `check`, `check-tidy`, `check-docs`, `vuln`, `install-hooks`, `clean`.
- **Default-on roles in `defaults/config.toml`** (cross-checked against the README "Enabled by default" table): `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `project_characteristics`, `refactor_small`, `security`, `testing_basic` — match.
- **Operating mode this run**: trace mode only. No execute-mode sandbox provisioned. Re-run in execute mode (fresh Linux container, no host env vars) would add value before the next release, especially to catch hidden `~/.claude/` assumptions and Linux-vs-macOS branching in `install.sh`.
- **Files to revisit first next cycle**: `README.md` (Quick Start), `FAQ.md` (parallel/exec examples), `EVAL.md` (cheap-iteration example), `install.sh:86` (version-print line).
