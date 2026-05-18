# Summary

Trace-mode follow of the documented install / quick-start / common-task paths against the current tree (commit `6dcf9a0`). All eight findings from the prior report (3 hours ago) remain unaddressed — none of the intervening commits touched `README.md`, `FAQ.md`, `EVAL.md`, or `install.sh`. Re-verified: `cmd/parallel.go` still has no `--prompt` flag, `cmd/version.go` still declares only the `version` subcommand (no `--version`), and `README.md:78` still reads `ataem serve`.

Role: `docs.followable`, model `claude-opus-4-7`, trace mode only (no execute-mode sandbox provisioned).

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
- **Description**: The example shows `ateam parallel --prompt "take care of problem 1" --prompt "take care of problem 2" --prompt "take care of problem 3"`. `ateam parallel` takes prompts as **positional arguments** (re-verified in `cmd/parallel.go:41-73` — declared flags are `--labels`, `--max-parallel`, `--common-prompt-first`, `--common-prompt-last`, `--dry-run`, etc.; no `--prompt` flag exists). `COMMANDS.md:404` and `cmd/parallel.go:51` both show the correct positional form: `ateam parallel "task A" "task B"`. An agent that copy-pastes the FAQ example will fail with an unknown-flag error.
- **Recommendation**: Rewrite the example to use positional prompts, e.g. `ateam exec @./decide.md && ateam parallel "take care of problem 1" "take care of problem 2" "take care of problem 3" && ateam exec "verify ..."`.

## 3. EVAL.md final example contradicts itself: `--judge-model sonnet --no-judge`

- **Location**: `EVAL.md:158-159`
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: The "Cheap iteration" example combines `--judge-model sonnet` with `--no-judge`. `--no-judge` is documented at line 112 of the same file as "Skip the LLM judge; print cost comparison only", which makes `--judge-model` meaningless in the same invocation. A literal reader / agent cannot tell which the example actually intends — judge enabled with sonnet, or no judge at all.
- **Recommendation**: Either drop `--no-judge`, or drop `--judge-model sonnet`, depending on intent. Likely the latter — the surrounding text frames this as a cost-comparison-only run, in which case the haiku-both-sides comment + `--no-judge` is the coherent variant.

## 4. install.sh prints `./ateam` instead of a real version after build

- **Location**: `install.sh:86`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `ok "Built: $(./ateam --version 2>/dev/null || echo './ateam')"`. The binary has no `--version` flag (`cmd/version.go:14-16` declares `Use: "version"` as a subcommand; Cobra does not auto-add a global `--version`). The fallback `echo './ateam'` always wins, so the installer's success line is uninformative ("Built: ./ateam"). Not a failure, but a literal reader/agent debugging an install will see misleading output.
- **Recommendation**: Replace `./ateam --version` with `./ateam version | head -1` (which prints the actual version line).

## 5. Quick Start does not say which test command runs the build verification

- **Location**: `README.md:52-95` (Quick Start) and the gap before `Prerequisites` at line 97
- **Severity**: MEDIUM
- **Effort**: SMALL
- **Description**: After `./install.sh`, the next thing many users / agents want is "did it actually work?". Three test targets exist (`make test`, `make test-docker`, `make test-docker-live`), and the docs don't tell a first-time user which to run as a smoke test. `make test-docker-live` consumes real Claude API credits (per `Makefile` and `DEV.md`) — an agent interpreting "run the tests" liberally could pick it. `DEV.md` distinguishes them, but it's two clicks away.
- **Recommendation**: Add one line in Quick Start (or right after `make build` in Manual Install) such as: `make test    # quick unit-only smoke test; do NOT run 'make test-docker-live' — it costs real API credits`.

## 6. README workflow example references `testing_full` without warning it is `off` by default

- **Location**: `README.md:152-155`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: The "Weekly (thorough)" example runs `ateam all --roles security,dependencies,testing_full`. `testing_full` is `off` in `defaults/config.toml`. Per recent commit `2f81a30` (`review: --roles is authoritative everywhere`), the explicit `--roles` flag now bypasses the enabled gate, so the command works. But a literal reader without that context might wonder why their `config.toml` has `testing_full = "off"` and not realize the flag overrides it.
- **Recommendation**: Add a brief note under Workflow Examples: "Roles listed in `--roles` run even when disabled in `config.toml`."

## 7. README "Quick Start" places Prerequisites after the procedure that needs them

- **Location**: `README.md:52-100` (Quick Start at lines 52-95, Prerequisites at lines 97-101)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A literal reader follows steps top-to-bottom: clones, installs, runs `ateam init`, then `ateam report` — and only then encounters "Prerequisites: Claude Code CLI or Codex CLI installed and authenticated". An agent without `claude` or `codex` authenticated will get past `ateam init` (it doesn't need an agent) and fail at `ateam report`. `install.sh` does warn at line 130-133 if `claude` is missing, but the README's reading order still puts the prerequisite after the procedure that needs it.
- **Recommendation**: Move Prerequisites above Quick Start, or add a one-line note at the top of Quick Start: "Requires Go 1.25+ and an authenticated `claude` or `codex` CLI — see Prerequisites below."

## 8. README "Manual Install" symlinks to a different location than `install.sh`

- **Location**: `README.md:105-110` vs `install.sh:7`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Manual Install symlinks into `/usr/local/bin` (with `sudo`); `install.sh` defaults to `$HOME/.local/bin`. A user who tries both ends up with two symlinks. An agent picking between them has no signal that they are equivalent.
- **Recommendation**: Either note that `install.sh` uses `~/.local/bin` so Manual Install is only needed when system-wide symlinking is preferred, or align both to the same location.

# Quick Wins

1. **`README.md:78` typo `ataem serve` → `ateam serve`** (Finding 1). SMALL, HIGH — the very first runnable block.
2. **`FAQ.md:21` rewrite the `ateam parallel` example to use positional prompts** (Finding 2). SMALL, HIGH — agents copy-pasting from FAQ will get an unknown-flag error.
3. **`EVAL.md:158-159` drop `--no-judge` or `--judge-model` from the contradictory example** (Finding 3). SMALL, MEDIUM.
4. **`README.md` add the "Roles in `--roles` run even if `off` in config.toml" note** (Finding 6) and reorder Prerequisites above Quick Start (Finding 7). SMALL, combined MEDIUM impact for agent-readability.

# Project Context

- **Audit scope (this role)**: experience of *following* documented procedures, not doc-content quality. Doc-quality findings belong to `docs.external` / `docs.internal`.
- **Top-level user docs**: `README.md` (~381 lines, primary entry point), `COMMANDS.md` (766 lines, authoritative command/flag reference), `CONFIG.md` (498 lines), `ISOLATION.md` (532 lines), `DEV.md` (364 lines, dev/test setup), `EVAL.md` (160 lines), `FAQ.md` (76 lines), `ROLES.md` (auto-generated by `ateam roles --docs`).
- **Install path**: `install.sh` checks for Go ≥ 1.25, asserts the repo by grepping `module github.com/ateam` in `go.mod` (matches), runs `make build`, symlinks to `~/.local/bin/ateam`. Quick Start clone URL `https://github.com/voidexpr/ateam.git` matches the real origin.
- **Build/test targets referenced by docs (all present in `Makefile`)**: `build`, `companion`, `test`, `test-docker`, `test-docker-live`, `test-all`, `lint`, `fmt`, `fmt-check`, `check`, `check-tidy`, `check-docs`, `vuln`, `install-hooks`, `clean`.
- **Default-on roles in `defaults/config.toml`** (cross-checked against the README "Enabled by default" table): `database_schema`, `dependencies`, `docs_external`, `docs_internal`, `project_characteristics`, `refactor_small`, `security`, `testing_basic` — match.
- **Re-verification this run**: `cmd/parallel.go` flags re-grepped (no `--prompt`); `cmd/version.go` re-read (no `--version` flag, only `version` subcommand); `README.md:78` re-read (`ataem serve` still present); `EVAL.md:158-159` re-read (`--judge-model sonnet --no-judge` still co-present); `install.sh:86` re-read (`./ateam --version` fallback still present). No commits since the prior report touched any of the affected files.
- **Operating mode this run**: trace mode only. No execute-mode sandbox provisioned. Re-running in execute mode (fresh Linux container, no host env vars) would add value before the next release, especially to catch hidden `~/.claude/` assumptions and Linux-vs-macOS branching in `install.sh`.
- **Files to revisit first next cycle**: `README.md` (Quick Start), `FAQ.md` (parallel/exec examples), `EVAL.md` (cheap-iteration example), `install.sh:86` (version-print line).
