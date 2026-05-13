# Auto-Discovery of Project Test Commands

A pattern for letting an LLM discover a project's test commands once, cache the result, and only re-discover when project-specific signals say the commands probably changed. The cache is fed to coding tasks so they run the right tests (not just the basic ones the agent guesses).

## Problem

Coding agents currently default to running whatever they can infer from the project — usually the most obvious test target (`make test`, `go test ./...`, `npm test`). They miss:

- Slower or more thorough test suites (`make test-all`, `make test-docker`)
- Targets that have prerequisites (Docker, secrets, services)
- Project conventions documented elsewhere (CLAUDE.md, AGENTS.md, README)
- Multi-ecosystem repos where the right test command depends on what you changed

Parsing this out of arbitrary build systems is **not** mechanical:

- Test invocations live in Makefile, package.json scripts, pyproject.toml, Cargo.toml, tox.ini, noxfile.py, justfile, Taskfile.yml, build.gradle, pom.xml, mix.exs, composer.json, BUILD.bazel, `.github/workflows/*.yml`, plain shell scripts, custom Python launchers.
- Target names vary: `test`, `check`, `spec`, `ci`, `validate`, `verify`.
- A target named `test` might mean unit tests, integration tests, lint, type-check, or a Docker-required suite.
- No general tool exists for this — Renovate has ~100 per-ecosystem managers, Anthropic's own `/init` uses LLM heuristics, IDE test runners are per-language hardcoded.

The reliable approach is per-ecosystem heuristics plus asking the agent/user once and persisting it. This plan formalizes the "ask once and persist" path.

## Idea

1. An LLM runs a one-time discovery pass: reads the project, writes a `commands.md` describing how to run tests and when.
2. The LLM also writes a project-specific bash detector script: it captures **which file regions** the test commands came from and hashes only those regions.
3. On every subsequent `ateam` run, the detector script runs first (milliseconds, no LLM). If it exits 0, the cached `commands.md` is used as-is. If it exits 1, discovery re-runs.
4. The detector self-verifies at write time: the LLM tests it against synthetic edits to confirm it triggers on real changes and not on noise.
5. A periodic time-based forced refresh (every ~30 days) catches silent drift.

This trades a high one-time discovery cost for near-zero per-cycle cost, amortized over hundreds of `ateam` runs.

## Cache layout

```
.ateam/cache/test_discovery/
├── commands.md       # human-readable; fed to coding tasks
├── sources.json      # files + regions consulted
├── detector.sh       # generated per-project bash
└── baseline.txt      # hash from last discovery
```

### `commands.md` shape

The LLM's output: structured, fed verbatim into coding-task prompts.

- **Quick tests**: command(s), expected duration, no prerequisites.
- **Full tests**: command(s), expected duration, prerequisites (Docker, secrets, network).
- **When to run each**: heuristics tied to what changed (e.g., "if anything in `internal/runner` or `internal/container` changed, run `make test-docker`").
- **Coverage gaps**: tests not run by any command (e.g., live integration tests gated on credentials).

### `sources.json` shape

```json
{
  "discovered_at": "2026-05-11T14:33:00Z",
  "head_at_discovery": "1d44127",
  "sources": [
    { "file": "Makefile", "targets": ["test", "test-cli", "test-docker", "test-docker-live", "test-all", "check"] },
    { "file": "CLAUDE.md", "section": "## Testing" },
    { "file": ".github/workflows/ci.yml", "jobs": ["test", "lint"] }
  ],
  "watched_globs": ["tox.ini", "noxfile.py", "pytest.ini", "**/Taskfile.yml"],
  "ecosystem": ["go"],
  "notes": "Project uses make as the canonical test runner; CLAUDE.md declares the test convention."
}
```

## Detector script

Per-project bash that hashes the relevant regions, normalized so irrelevant edits don't trigger refresh. The LLM writes this; it is not a generic tool.

Example for ateam's setup:

```bash
#!/bin/bash
# Generated 2026-05-11 for ateam project.
# Returns 0 if test commands appear unchanged, 1 if rediscovery recommended.
set -e

# Extract just the targets whose recipes were captured.
# Normalize whitespace so reformatting doesn't trigger refresh.
relevant_makefile() {
  awk '
    /^(test|test-cli|test-docker|test-docker-live|test-all|check):/ { in_target=1; print; next }
    in_target && /^[ \t]/ { print; next }
    in_target { in_target=0 }
  ' Makefile | tr -s ' \t' ' ' | grep -v '^$'
}

# Extract the Testing section from CLAUDE.md until the next heading.
relevant_claude() {
  awk '/^## Testing/{flag=1; next} /^## /{flag=0} flag' CLAUDE.md | tr -s ' \t' ' '
}

# Watched globs: if these appear/disappear, that's a refresh signal.
watched_files() {
  ls -1 tox.ini noxfile.py pytest.ini 2>/dev/null | sort
}

hash=$( { relevant_makefile; relevant_claude; watched_files; } | sha256sum | cut -d' ' -f1 )
baseline=$(cat .ateam/cache/test_discovery/baseline.txt 2>/dev/null || echo "")

if [ "$hash" = "$baseline" ]; then
  exit 0
else
  exit 1
fi
```

For a Python+poetry project the detector watches `pyproject.toml` `[tool.pytest.ini_options]`, `tox.ini` envs, `noxfile.py` sessions. For a Node monorepo, `package.json` scripts plus per-workspace `scripts.test`. Each project gets its own bespoke detector.

## Self-verification at write time

The discovery prompt requires the LLM to verify the detector before saving it. Without this, detector quality is uncertain.

1. Save the detector.
2. Run it once. Save the result as the baseline.
3. **No-op edit** test: add a blank line to a source file, then remove it. Run detector → must exit 0.
4. **Relevant edit** test: rename a captured target (or change its recipe), run detector → must exit 1. Revert.
5. **Irrelevant edit** test: add a comment outside captured regions. Run detector → must exit 0.

If any invariant fails, the LLM rewrites the detector. If it can't be made to hold after a small number of attempts, the LLM falls back to "no detector; refresh every N cycles regardless."

This catches the two failure shapes:
- Over-eager detector (every edit triggers — wastes LLM tokens)
- Over-narrow detector (real changes don't trigger — silent staleness)

## Failure modes and mitigations

| Failure mode | Mitigation |
|---|---|
| New test infrastructure appears (e.g., new `tox.ini` not in watched globs) | LLM's watched globs include broader test-name patterns (`*test*`, `*tox*`, `*nox*`, `noxfile*`, `pytest.ini`, `package.json` if absent). New matching file → trigger. |
| Detector has a subtle bug, always returns 1 | Log detector exit codes per cycle. If it triggers >3 cycles in a row with no real change, surface a warning. Time-based ceiling — force refresh every 30 days regardless. |
| Detector always returns 0 even when commands changed | Time-based floor — force refresh every N days catches the "we never noticed" case. |
| Multi-language repo | LLM writes a multi-segment detector. Same pattern, more segments. |
| Test commands declared only in CI YAML | Capture `.github/workflows/*.yml` job blocks by name. `yq` works when available; range extraction by marker comments works otherwise. |
| Submodules or generated build files | LLM explicitly notes them in `sources.json`, either including them in the detector or documenting why excluded. |
| Whitespace reformatting triggers false positive | Aggressive normalization in the extractors: `tr -s ' \t'`, strip blanks, sort order-independent lines, optional lowercase. LLM picks per file format. |
| LLM-written detector references files that don't exist | Discovery prompt requires the LLM to `ls` each source before writing the script. Script uses `2>/dev/null` for optional files. |

## Run flow

```
ateam report
  │
  ├─ test-discovery preflight
  │    ├─ exists .ateam/cache/test_discovery/detector.sh ?
  │    │    yes → run it
  │    │         ├─ exit 0 + age < 30d → use cached commands.md
  │    │         └─ exit 1 OR age ≥ 30d → trigger discover_tests
  │    │    no  → trigger discover_tests
  │    └─ discover_tests
  │         ├─ LLM writes commands.md, sources.json, detector.sh, baseline.txt
  │         ├─ self-verify detector against synthetic edits
  │         └─ on verification failure: fall back to "no detector, refresh every N cycles"
  │
  └─ roles run with commands.md injected into their preamble
```

The preflight is fast enough to run on every `ateam` invocation. The LLM discovery is gated behind detector + age, so it rarely fires.

## Cost shape

- **One-time per project (or per real change to test infrastructure)**: 5–15K tokens for discovery + detector writing + self-verification.
- **Per cycle**: milliseconds for the detector. Zero LLM tokens when the detector exits 0.
- **Periodic forced refresh** (every ~30 days): same one-time cost, amortized.
- **Worst case** (detector always triggers): same cost as discovering on every cycle. Bounded by what you'd pay anyway without this feature.

Net win if discovery would otherwise run every cycle (current state is worse — agents guess and miss the right command entirely).

## Implementation sketch

1. **Skill / role `discover_tests`** — produces `commands.md`, `sources.json`, `detector.sh`, `baseline.txt`. Self-verifies the detector. Timeout 5 minutes. Off by default; opt in per project or run on demand.

2. **Preflight hook in `ateam report` / `ateam code`** — if `detector.sh` is missing, fails to run, exits 1, or its baseline is older than 30 days, trigger `discover_tests` first. If the detector exits 0 and the cache is fresh, skip discovery.

3. **Inject `commands.md` into role preambles** — at minimum the `code` phase and `testing_*` roles. The `code` phase is the highest-value target because the supervisor decides what tests to run before declaring a task complete.

4. **Telemetry** — log detector hit rate (exit 0 vs 1) per project per cycle. If a detector triggers more than once a month on a quiet repo, surface a warning so the user can re-run discovery with a tightening hint.

5. **Manual override** — `.ateam/config.toml` can specify a static `test_commands` block that bypasses discovery entirely. For projects where the user just wants to declare commands once and never have an LLM touch them.

6. **Do not extend this pattern to non-test inventory** — file tree, recent commits, line counts, language detection are deterministic shell, no LLM, no detector. The discover+detect+cache pattern is overkill except where mechanical parsing genuinely doesn't work, and "test commands" is that case.

## Honest caveats

- The first project where the LLM writes a too-eager detector and nobody notices for a week, the savings are erased. Telemetry on detector hit rate is non-optional.
- The detector is bash. On Windows agents this needs WSL or PowerShell equivalents. For now: assume Unix; document the constraint.
- Self-verification requires the LLM to make synthetic file edits and revert them. This must happen in a sandboxed copy of the project, or the verification step itself becomes a risk. Easiest: do it in a temp worktree.
- The 30-day forced refresh is arbitrary. Better signal would be "refresh after the next non-trivial Makefile/build-config change regardless of detector", but that's a chicken-and-egg problem the detector is supposed to solve.
- For multi-language repos with very different ecosystems, a single `commands.md` may be unwieldy. Allow `commands_<lang>.md` if needed.

## Open questions

- Should `commands.md` distinguish "tests the code phase should run before declaring done" from "tests that are nice-to-run but not required"? Probably yes — the supervisor needs to know which gate is mandatory.
- Should the detector and cache live in the project (`.ateam/cache/`) or in the org (`.ateamorg/cache/<project>/`)? Project-local is simpler; org-level enables sharing across worktrees of the same repo.
- What happens when a project switches its build system entirely (Make → Bazel)? The detector should trigger because all captured Makefile regions vanished. But verify this is the case — a detector that conditionally hashes `Makefile` may silently drop the segment if the file is gone.
- Does this pattern generalize to "discover the lint command", "discover the build command", "discover the format command"? Probably, with the same caveats. Worth proving with tests first before extending.
- Should `discover_tests` use a cheap model? Probably not — the self-verification step needs to write working bash, and a cheap model is more likely to produce a fragile detector. The discovery cost is paid rarely; pay for quality.
