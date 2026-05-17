# Feature: Token Reduction in ATeam Reports

*Last updated: 2026-05-14*

## Source documents

This feature integrates findings from three sources:

- **[Research_InvestigateReportLogsForTokenUsage.md](./Research_InvestigateReportLogsForTokenUsage.md)** — empirical analysis of real ATeam stream.jsonl traces (cold run + 2nd run + appendix from a role-expert agent). All quantitative claims below come from this dataset.
- **[ResearchCodebaseDiscoveryTokenReduction.md](./ResearchCodebaseDiscoveryTokenReduction.md)** — outside-research survey of repository maps, code graphs, RAG, prompt caching, and analyzer integration. Useful framing; does **not** account for ATeam-specific mechanisms.
- **`defaults/new_report_base_prompt.md`** — the live base prompt every report role inherits. Critically, it mandates a `Project Context` section in every report (line 52–54).

The two research files are deliberately complementary: the first is ATeam-specific and grounded in measurement; the second is general-purpose and surveys the literature. **For anything ATeam-specific, prefer the first.**

## Headline finding

> **ATeam already implements the recommended fix, accidentally.** The base prompt mandates a `Project Context` section. The harness inlines the prior report into the next prompt. Cold→warm runs produce **−69% cost / −84% cache_read with zero findings lost**. The mechanism works in production today; the work ahead is to *productionise* it (make it script-augmented, structured, and auditable) rather than to invent it.

## What the data shows (cold vs warm, same code, same model, dotted roles only)

| Metric | Cold (16 runs) | Warm (14 runs, prior report inlined) | Δ |
|---|---|---|---|
| Total cost | $41.5 | $13.0 | **−69%** |
| Cache_read tokens | ~50M | ~8M | **−84%** |
| Median tool calls per role | ~50 | ~10 | **−80%** |
| Warmup ≥7/10 in first 10 calls | 10 of 16 | 0 of 14 | gone |
| Mean warmup score | 7.6/10 | 1.6/10 | gone |
| Findings lost | — | **0** | strict win |
| Findings *gained* | — | +5 across 3 roles | strict win |

The clearest single canary is `code.structure` (three runs on the same commit):

```
id 3   65 tool calls,  $4.80 — 11 orientation calls before any read; cmd/table.go re-read 4×
id 23  14 tool calls,  $1.09 — 1 orientation, then targeted greps for named symbols
id 33   5 tool calls,  $0.71 — wc on specifically-named files; git log -5; 2 targeted greps; Write
```

**6.8× cost reduction with same quality, same code.** That ratio sets the design target.

## What works, and where

The model-authored `Project Context` section is doing the heavy lifting. Sampled from `runtime/33/report.md`, it carries:

- Language / build commands / lint config
- Package layout (16 internal packages, 61 cmd files, etc.)
- **Biggest non-test files with LOC** (the structural debt hotspots a role wants to know about)
- Concurrency contract
- Log layout invariants
- Entry points
- Helper conventions ("`cmd/runner_overrides.go::applyRunnerOverrides` is the boilerplate model")
- Known intentional duplication
- Recommended mechanical tools

This is exactly the role-aware map the outside research recommended building from scratch with PageRank + Tree-sitter. The model produces a *better* one because it has role context PageRank can't have.

## What doesn't, and why

Four failure modes, ranked by severity:

1. **Model-authored mechanical facts hallucinate.** Live evidence in the dataset: two confidently-wrong facts (about `voidexpr/ateam.git` as a "broken clone URL" and AGENTS.md/CLAUDE.md as "duplicates with no shared mechanism") survived into a subsequent run's prompt. A script with `git remote -v`, `ls AGENTS.md CLAUDE.md`, `wc -l` does not hallucinate.
2. **Re-verification claims have no audit.** When a Project Context says "`README.md:78 re-read (ataem serve typo still present)`", the harness can't check whether a `Read` actually fired this run. The trust gap compounds over cycles.
3. **Prose format is unparseable.** The supervisor can't reliably consume "out of scope" declarations to dedupe findings across roles. The harness can't diff Project Context across runs meaningfully.
4. **`code.bugs` is the wandering role that the map doesn't fix.** Warm `code.bugs` still ran 91 tool calls / 32 turns / $2.16 (4× any other warm role). It's reading breadth, not confirming depth — a separate problem with a separate fix.

## Earlier research vs reality

### Right but underweighted
- **Shared map injection is the right mechanism.** Confirmed at ~7× per-role cost factor.
- **Static-baseline × turn-count is the dominant cost.** Confirmed: even warm, residual cost is baseline × ~15 turns rather than × ~50.
- **Discovery overlap is real but small.** Cross-role overlap 40% → 12% in warm. That freed ~$3–4 of the $28 saved; the rest came from cutting turn count.

### Wrong in priority order
- **Structural code graph as "highest-leverage layer"** — third-place at best. The role-authored map outperforms a generic symbol graph because it carries role-specific judgment.
- **Aider-style PageRank** — probably overkill at ATeam scale. Roles pick better landmarks than import-graph centrality.
- **Hybrid RAG / semantic retrieval** — defer indefinitely. At $13/cycle warm, this isn't where the next $5 lives.

### Missed entirely
1. The base prompt already mandates a Project Context section. Design intent has been in place from day 1; the 84% gap is what happens when the mechanism actually runs (warm vs cold).
2. Model-authored map ≠ script-authored map. They have *opposite* failure modes — scripts are mechanically correct but role-blind; models are role-aware but hallucinate facts. The right answer is a **hybrid**: script-generated mechanical block + model-authored role-specific layer.
3. Re-verification audit gap — neither the original analysis nor the outside research raised this.
4. `code.bugs` as a structurally different problem from cross-role overlap.
5. Sub-agent (`Task`) delegation is essentially absent — 1 use across 228 warm tool calls.

## Two-agent convergence as a design signal

The data-only log analyst and the role-only domain expert independently propose the same architecture from opposite information bases:

| Log analyst (data, no role knowledge) | Role expert (roles, no log access) |
|---|---|
| "Replace mechanical 60% of Project Context with scripts" | "Layer 1 generic core: identity, layout, build commands, recent activity, doc surface, tool inventory, manifest summary" |
| "Structure the remaining 40% as machine-consumable YAML" | "Layer 2 role-specific discovery, built on demand, cached by SHA" |
| "Inject via the runner so it's invariant" | "Hook point: `ateam report` runs the map script before role-prompt assembly" |

Same conclusion from independent inputs → strong design signal.

## Stack-aware project-info extension (Phase 0.5)

The cheapest single change in this whole document. **Extends the existing `# ATeam Project Context` preamble** (already injected at the top of every role prompt) with a small block of orientation facts. No new infrastructure, no new commands, no new directories — just additional shell-outs in the existing prompt-assembly path.

The base prompt today emits ~7 lines of preamble (project name, role, project dir, timestamp, last commit, working-tree status). The trace data shows roles spending 6–10 of their first 10 tool calls re-deriving things this preamble could just *contain*. Filling that gap eliminates most of the cold-cache penalty for mechanical orientation.

### Why this comes before Phase 0

Phase 0 (doc injection) carries judgment-bearing content from `CLAUDE.md` / `CONCURRENCY.md` / `DEV.md`. Phase 0.5 carries mechanical orientation that any role would re-derive in its first turns. They're complementary, but **Phase 0.5 is cheaper to ship and likely captures the larger fraction of the cold-cache penalty** because the empirical warmup pattern is overwhelmingly orientation (`ls`, `find`, `wc -l`, `git log`), not concept-level (which is where Phase 0 helps).

### Two-layer architecture (language- and tool-agnostic)

The original sketch was Go-and-Makefile-shaped. The corrected architecture detects the stack and emits stack-appropriate facts, with a generic fallback. It works on any git repo regardless of language or build system.

**Layer A — Universal core (always emitted; depends only on `git`).** Works on any git repository, any language, any stack. No external tools required.

| Field | Source |
|---|---|
| Top-level entries | `git ls-files \| awk -F/ '{print $1}' \| sort -u \| head -30` |
| Total tracked files | `git ls-files \| wc -l` |
| Recent commits (last 10) | `git log --oneline -10` |
| Doc files at root | `git ls-files \| grep -E '^[^/]+\.(md\|rst\|adoc\|txt)$'` |
| Detected manifests | `git ls-files \| grep -E '^(go\.mod\|package\.json\|Cargo\.toml\|pyproject\.toml\|requirements\.txt\|Gemfile\|mix\.exs\|build\.gradle\|pom\.xml\|composer\.json\|setup\.py\|deno\.json\|build\.zig\|CMakeLists\.txt\|Makefile\|justfile\|Taskfile\.yml)$'` |
| Prior reports for this role | from `state.sqlite` + `.ateam/runtime/` |
| Working tree status, last commit | already in preamble |

**Layer B — Stack profiles (detected from manifests in Layer A).** Each profile is a small Go function that runs only if its manifest is present and produces a uniformly-shaped block. Skip silently when the relevant CLI isn't installed. Profile inventory for v1:

| Profile | Detected via | Identity | Commands extractor | Source LOC filter | Layout |
|---|---|---|---|---|---|
| **Go** | `go.mod` | `go list -m` + `go version` | `Makefile` targets, `make help` if available | `*.go`, exclude `*_test.go` | `go list ./...` |
| **Node.js / TypeScript** | `package.json` | `name`+`version` from JSON | `package.json scripts` | `*.{ts,tsx,js,jsx}`, exclude `*.test.*` / `*.spec.*` | top-level dirs under `src/` or `app/` |
| **Python** | `pyproject.toml` / `setup.py` / `requirements.txt` | `[project]` name+version | `pyproject.toml [tool.poetry.scripts]`, `[tool.pytest]`, `Makefile` | `*.py`, exclude `test_*.py` / `*_test.py` | `__init__.py` discovery, ≤3 deep |
| **Rust** | `Cargo.toml` | `[package]` name+version | `Cargo.toml [alias]` + `cargo --list` | `*.rs`, exclude `tests/` | `cargo metadata` workspace members |
| **Elixir** | `mix.exs` | parsed `mix.exs` | `mix help` | `*.{ex,exs}`, exclude `test/` | `lib/` top-level dirs |
| **Java/Kotlin (Gradle)** | `build.gradle{,.kts}` | parsed root project name | `gradle tasks --group=...` | `*.{java,kt}`, exclude `*Test.{java,kt}` | `settings.gradle` modules |
| **Generic fallback** | (no profile match) | — | `Makefile` / `justfile` / `Taskfile.yml` if present | top files by raw `wc -l` from `git ls-files`, excluding `.{lock,json,yaml,yml,toml,md,svg,csv}` | top-level dirs from `git ls-files` |

**Detection is purely manifest presence.** Polyglot projects (Go backend + Next.js frontend, etc.) emit **multiple profile sections in sequence**, ordered by tracked-file count (dominant stack first). Unknown stacks fall to Generic, which is degraded but non-empty.

### Example outputs

**Go project (ATeam itself):**

```
## Quick orientation (auto-generated; do not re-derive)

Universal:
* Top-level: cmd, internal, defaults, scripts, plans, README.md, AGENTS.md,
  CLAUDE.md, CONCURRENCY.md, DEV.md, ISOLATION.md, Makefile, go.mod, go.sum
* Tracked files: 412
* Recent commits (last 10):
    6dcf9a0  prompts: fix three internal contradictions surfaced in review
    2f81a30  review: --roles is authoritative everywhere
    [...]
* Manifests detected: go.mod, Makefile
* Docs at root: README.md, AGENTS.md, CLAUDE.md, CONCURRENCY.md, DEV.md, ISOLATION.md
* Prior reports for this role: .ateam/runtime/3/, .ateam/runtime/23/, .ateam/runtime/33/

Profile: Go (detected via go.mod)
* Module: github.com/ateam, go 1.26.3
* Internal packages: agent, calldb, container, display, eval, fsclone, gitutil,
  prompts, root, runner, runtime, secret, streamutil, web
* Top non-test .go files by LOC:
    1433  internal/web/handlers.go
    1390  cmd/table.go
    1282  internal/runner/runner.go
    [...]
* Make targets: build, companion, test, test-cli, test-docker, check, run-ci, vuln

Do not re-run ls / find / wc / git log on the project root —
the orientation above is current and authoritative.
```

**Polyglot (Go backend + Node frontend):**

```
Universal: [...]
Manifests detected: go.mod, package.json

Profile: Node.js / TypeScript (detected via package.json) — 1,247 tracked files
* Package: @org/web v0.18.2
* npm scripts: dev, build, start, lint, test, test:e2e, format, typecheck
* Top non-test .ts/.tsx files by LOC: [...]
* Source dirs: app/, lib/, components/

Profile: Go (detected via go.mod) — 312 tracked files
* Module: github.com/org/api, go 1.24
* Top non-test .go files by LOC: [...]
* Make targets: build, test, lint

Do not re-run ls / find / wc / git log on the project root —
the orientation above is current and authoritative.
```

**Unknown stack (Zig, Nim, custom build):**

```
Universal: [...]
Manifests detected: build.zig

Profile: Generic fallback (no recognised stack manifest)
* Top files by LOC (source-looking; excludes lock/yaml/json/md):
     402  src/main.zig
     [...]
* Build commands (Makefile / justfile / Taskfile): make build, make test
* Source dirs (heuristic): src/

Do not re-run ls / find / wc / git log on the project root —
the orientation above is current and authoritative.
```

### Implementation shape

```
internal/projectinfo/
  info.go              — top-level assembler
  universal.go         — git-only facts
  profile.go           — Profile interface + DetectAll(root)
  profile_go.go
  profile_node.go
  profile_python.go
  profile_rust.go
  profile_elixir.go
  profile_gradle.go
  profile_generic.go   — fallback
```

```go
type Profile interface {
    Name() string
    Detected(root string) bool
    Render(root string, cfg Config) (string, error)
    TrackedFileCount(root string) int
}

func Assemble(root string, role string, cfg Config) string {
    var b strings.Builder
    b.WriteString(Universal(root, role))
    profiles := DetectAll(root)
    sort.Slice(profiles, func(i,j int) bool {
        return profiles[i].TrackedFileCount(root) > profiles[j].TrackedFileCount(root)
    })
    if len(profiles) == 0 {
        b.WriteString(Generic{}.Render(root, cfg))
    } else {
        for _, p := range profiles { b.WriteString(p.Render(root, cfg)) }
    }
    b.WriteString(footer)
    return b.String()
}
```

Each profile is ~30–80 LOC + golden-output tests. Total v1 (universal + 6 profiles + generic + assembler): ~600–800 LOC. Half a day to two days.

External CLIs (go, npm, cargo, pip, mix, gradle) are **advisory** — if not installed, the profile emits manifest-only facts and skips tool-derived fields. No new hard dependencies.

### Token budget

| Layer | Tokens |
|---|---|
| Universal core | ~250 |
| One profile | ~150 |
| Polyglot (2 profiles) | ~400 |
| Worst case (4 profiles) | ~700 |

All well under the ~6–10K cache-friendly preamble cap.

### What this changes about Phase 1

The previously-listed Phase 1 ("Auto-generate Project Facts block") is **subsumed by Phase 0.5**. The Facts block was Go-and-Makefile-shaped; Phase 0.5 generalises it correctly. Once Phase 0.5 ships, the dedicated Facts block is no longer needed — the orientation it would carry is already in the preamble.

### Configuration

```toml
[projectinfo]
enabled = true
include_recent_commits = 10
top_files_per_profile = 10
max_total_tokens = 1500          # safety cap; truncate excess profiles
profiles = []                     # empty = auto-detect; can override
exclude_paths = ["vendor/", "node_modules/", ".git/"]
```

### A/B procedure (cheap)

1. Implement `internal/projectinfo/`. Wire into the existing preamble assembler (probably `internal/prompts/`).
2. Pick three roles with high cold-warmup density: `code.structure`, `test.gaps`, `docs.external`. Run cold against a fresh project (or re-run against the recorded baseline — ids 1–20 in `state.sqlite`).
3. Measure: warmup density (target ≤ 2/10 in first 10 calls), total cost, finding count.
4. If warmup density drops to 0–2/10 and finding count is preserved: ship to all roles. Phase 0 (doc injection) becomes the next experiment.
5. If warmup density doesn't drop: inspect which calls survive and add the corresponding bullet to the relevant profile.

## Shared Architecture Context (Phase 0)

A judgment-bearing layer that sits underneath the mechanical Facts block. The data argues for it as the *first* move, ahead of the Facts script.

**Why first.** The warm-run `Project Context` content I sampled is dominated by facts a script can't produce: the concurrency contract, the `runtime/` vs `logs/` invariant, "`applyRunnerOverrides` is the boilerplate model", "intentional A/B branching in `prompts.go`". Those are convention and rationale — distilled judgment. A competent role would re-derive `wc -l`/`git log`/make-targets in 2–3 turns; it cannot re-derive conventions cheaply. So the per-token value of a stable conventions layer is higher than that of the mechanical Facts block.

**Why this isn't a new authoring project.** ATeam already has the content. The redundancy analysis shows 3–5 cold roles independently reading `CLAUDE.md`, `CONCURRENCY.md`, `DEV.md`, `ISOLATION.md`, `AGENTS.md`. The fact that roles bother to read them under cost pressure says they carry load-bearing information. The Phase 0 work is **inject what exists** — not write what's missing.

**Steps.**

1. Add a `[shared_architecture]` block in `config.toml`: `enabled`, `docs = [...]` (default: `CLAUDE.md`, `CONCURRENCY.md`, `DEV.md` when present), `max_tokens` (default ~6K), `position = "first_stable"`.
2. Extend `internal/prompts/` to concatenate the listed docs as a single preamble, placed immediately after the system prompt (cache-friendly position). Skip silently if a listed doc doesn't exist.
3. Render a header before each injected doc (`# Shared Architecture Context: CONCURRENCY.md`) so the model can cite it.
4. Token-cap the block; if over budget, truncate later docs first and emit a single warning line.
5. **Only** author a consolidated `ARCHITECTURE.md` if the measurement below shows existing docs aren't sufficient. Don't write a new doc speculatively.

**Effort.** ~50 LOC + config knob + golden-output tests + one integration test asserting the block lands in the right prompt position. Smaller than the Facts block.

**4-baseline measurement (attributes savings between the layers).**

| Arm | What's in the prompt | Hypothesis |
|---|---|---|
| A | Current cold baseline | $42/cycle (known) |
| B | A + shared architecture docs injected | Closes a meaningful fraction of the cold-cache gap on its own |
| C | B + auto-Facts block | Closes most of the remaining gap |
| D | C + prior report (warm) | Approaches the $13/cycle warm baseline (known) |

Running A→D on the same commit attributes the savings layer-by-layer. If B alone gets close to D, existing docs are sufficient and no new authoring is needed. If B is flat and C is large, the mechanical facts are doing the work and doc-injection is secondary. If both contribute, both layers are justified.

**Risk and mitigations.** A wrong architecture doc is more dangerous than no doc — agents treat it as authoritative. Two safeguards:

- **Prefer source-of-truth over summaries.** Inject `CONCURRENCY.md` itself, not a meta-summary. Source of truth lives next to the code it describes and rots more visibly.
- **Staleness markers if/when authoring a new doc.** Any new consolidated doc carries a `Last verified: <commit>` line and a list of "anchor files" (e.g., `runner.go`, `prompts.go`). CI flags drift when an anchor file changes without the doc being touched.

## Revised priority order (replaces §H of ResearchCodebaseDiscoveryTokenReduction.md)

| Phase | What | Evidence | Est. impact |
|---|---|---|---|
| **0.5** | **Stack-aware project-info extension** — extend the existing `# ATeam Project Context` preamble with universal-core orientation + stack-detected profile blocks (Go / Node / Python / Rust / Elixir / Gradle / Generic). Language- and tool-agnostic. See [Stack-aware project-info extension](#stack-aware-project-info-extension-phase-05). | Cold-vs-warm gap is 84%; 10/16 cold roles spent ≥7 of first 10 calls on `ls` / `find` / `wc` / `git log` — exactly what this block contains. Half-day to ship. | Cheapest single change; likely captures the largest mechanical-orientation slice. |
| **0** | **Inject existing architecture docs** (`CLAUDE.md` / `CONCURRENCY.md` / `DEV.md` / `ISOLATION.md` as available) as a stable prompt preamble — see [Shared Architecture Context](#shared-architecture-context-phase-0) | 3–5 cold roles independently read these docs today; warm-run Project Context content is dominated by conventions a script can't produce | The judgment-layer complement to Phase 0.5; closes the conventions-and-rationale gap that scripts can't fill |
| ~~**1**~~ | ~~Auto-generate Project Facts block~~ | **Subsumed by Phase 0.5.** The Facts block was Go-and-Makefile-shaped; Phase 0.5 generalises it correctly. | — |
| **2+** | **Artifact system with provenance** — code map, architecture topics (with TOC), command catalogues, dependency summary, each with declared inputs and make-like incremental update via cheap-validate / patch / re-author. Subsumes the previously-listed Phase 2 (structured Project Context) and Phase 3 (per-role discovery). See [Artifact System with Provenance](#artifact-system-with-provenance-phase-2). | Forward-looking endpoint of the plan: eliminates ad-hoc Project Context, makes preprocessing-vs-LLM division explicit, supports on-demand topic injection. | Long-term: closes the residual variance between roles after Phases 0–1 land. |
| **4** | **Turn-cap + "name your suspects first" preamble for wandering roles** | `code.bugs` is the only warm role still >20 turns. Surgical fix, not architectural. | ~$1–2/cycle on `code.bugs`-class roles |
| **5** | **Audit hook linking `reverified` claims to `tool_use_id`s in stream.jsonl** | Closes the trust gap that compounds over cycles | Quality only |
| ~~6+~~ | ~~Structural graph, Aider PageRank, hybrid RAG~~ | Discovery is now 12% of cycle cost; further attack on it is third-order | Defer indefinitely |

## Next experiment

**Smallest cost / largest impact next move**: ship Phase 0.5, measure, then layer Phase 0 on top.

Sequencing:

1. **Phase 0.5 first** (half-day to two days). Implement `internal/projectinfo/` with universal core + Go profile + Generic fallback. Wire into the existing preamble assembler. Other profiles (Node, Python, Rust, Elixir, Gradle) can land incrementally as you encounter projects that need them.
2. Run all 14 dotted roles cold against the same commit used for the existing cold baseline (ids 1–20 in `state.sqlite`). Measure warmup density, total cost, finding count.
3. **Phase 0 second** (additional ~half-day). Add doc injection for `CLAUDE.md` / `CONCURRENCY.md` / `DEV.md` / `ISOLATION.md` on top of Phase 0.5. Re-run.
4. Compare against arm A (existing cold baseline) and arm D (warm baseline, already in the data).

The full A→D matrix is now:

| Arm | What's in the prompt | Hypothesis |
|---|---|---|
| A | Current cold baseline | $42/cycle (known) |
| A+ | + Phase 0.5 (stack-aware project-info) | Closes the mechanical-orientation gap |
| B | A+ + Phase 0 (architecture docs injected) | Closes the conventions/judgment gap |
| D | B + prior report (warm) | $13/cycle warm baseline (known) |

Running A→A+→B→D on the same commit attributes savings layer-by-layer.

**Decision criteria** (whole-cycle cost):

- ≤ $18 after A+ alone → ship Phase 0.5; Phase 0 is a refinement, not urgent.
- $18–$28 after A+ → ship Phase 0.5; proceed with Phase 0; re-measure.
- ≥ $28 after both A+ and B → the role-authored Project Context is carrying more weight than mechanical + judgment layers; either author a consolidated `ARCHITECTURE.md` or accelerate Phase 2+ (artifact system).

## `code.bugs` track (separate from main fix)

Once Phase 1 lands, `code.bugs` will be the conspicuous outlier. Three suggested tactics, ordered by cheapness:

1. **Turn cap (e.g. 40) with budget warning at 80%.** The runner already counts turns. ~20 LOC.
2. **Preamble: "Name up to 7 files you suspect, with one-line rationale each, before any Read."** Prompt-only change.
3. **Per-role pattern bank**: pre-grep for `_ =` discards, empty catch/recover, `time.Sleep` in non-test code, panic call sites — feed results into the role prompt. This is the appendix's role-specific layer applied to `code.bugs`.

Run as an A/B against current `code.bugs` after Phase 1 ships.

## Artifact System with Provenance (Phase 2+)

This is the forward-looking endpoint Phases 0 and 1 are building toward: replace the ad-hoc `Project Context` section with a small set of **provenance-tagged artifacts**, maintained by a make-like driver that reruns generators only when their declared inputs change. Most artifacts are deterministic scripts; a few (architecture topics) are LLM-authored. The whole system runs **outside** the report lifecycle — it's preprocessing, not a report phase.

### Goal

Lean heavily on cheap deterministic scripts for everything that can be derived from the codebase. Reserve LLM authorship for the judgment-heavy slice (conventions, architectural rationale). Make staleness deterministic and incremental — most commits don't trigger any LLM work. Make the always-injected slice small (a TOC + code map + commands) and let roles pull architectural detail on demand.

### The artifact set

A small fixed catalogue, contract-stable across project types:

| Artifact | Author | Always injected? | Purpose |
|---|---|---|---|
| `code_map.md` | script | yes (small) | File tree, package layout, top-LOC files, entry points. 90% mechanical (`wc`, `find`, `git log`, build-file parsers). |
| `architecture/INDEX.md` | LLM, small | yes | TOC: one line per topic + the source files that justify it. Always seen by every role. |
| `architecture/<topic>.md` | LLM, judgment-bearing | **no — on-demand** | One per topic (concurrency, isolation, prompt-assembly, error-handling, log-layout, …). Fetched by role config declaration or runtime tool call. |
| `commands/test.md` | script + light LLM tagging | role-conditional | Test commands tagged with triggers (`when src/auth/** changes`, `before release`, etc.). |
| `commands/build.md` | script + light LLM tagging | role-conditional | Build / lint / typecheck / format commands. |
| `commands/ops.md` | script + light LLM tagging | role-conditional | make targets, deploy scripts, operational tasks. |
| `dependencies.md` | script | yes (small) | Direct deps with one-line purpose; runtime requirements. |
| `hotspots.md` | script | yes (small) | Most-edited files in recent N commits, biggest non-test files, files with high churn × size product. Pure `git log` + `wc` analysis. |

LLM authorship is concentrated where judgment matters: architecture topics (substantive) and trigger-tagging on commands (small). Everything else is deterministic.

### Provenance schema

Every artifact carries a `.meta.yaml` sibling. This is the load-bearing piece — it's what lets the driver decide when to rerun what:

```yaml
artifact: architecture/concurrency.md
artifact_id: architecture.concurrency                 # stable ID, role configs reference this
generated_at: 2026-05-15T12:00:00Z
generated_by_model: claude-opus-4-7
generation_commit: 6dcf9a09f51a
generator: ateam-artifact-generator-v0.1
inputs:
  - path: internal/runner/runner.go
    blob_sha: <git blob sha at generation time>
    symbols: [Runner.Run, RunPool, CloneWithResolvedTemplates]  # optional, sub-file granularity
  - path: internal/runner/pool.go
    blob_sha: <sha>
  - path: CONCURRENCY.md
    blob_sha: <sha>
last_validated_at: 2026-05-15T12:00:00Z
last_validated_commit: 6dcf9a09f51a
validation_history:                                   # rolling, cap N entries
  - { at: 2026-05-15T12:00:00Z, commit: 6dcf9a09f51a, outcome: generated, cost_usd: 1.42 }
toc_entry: "Concurrency contract — runner fields read-only after RunPool dispatch, per-exec cloning model."
```

Two granularities for `inputs`:

- **File-level (default)**: `blob_sha` only. Cheap, coarse. Any change to the file invalidates.
- **Symbol-level (optional)**: `symbols` list of named functions/types with AST-subtree hashes. Survives comment / whitespace / unrelated-method changes. Tree-sitter or universal-ctags can produce these. Add only when measurement shows file-level granularity is invalidating unnecessarily.

`artifact_id` is the stable handle role configs reference. Renaming the file changes the path but not the ID.

### Self-declared provenance: the LLM authors its own manifest

The schema above is silent on *who writes it*. The answer that makes the system work: **the LLM authors both the artifact and its own deps manifest in a single call**. The LLM is the only entity that actually knows which files determined the artifact's content; asking a human or a heuristic to curate the dep list is asking them to mentally re-do the LLM's analysis.

Operational implication: every generator prompt instructs the LLM to emit *two* outputs.

```
LLM call: "Read these candidate driver files. Produce:
  (1) tests_overview.md — the summary
  (2) tests_overview.deps.yaml — the exact files whose contents
      determined (1), with blob SHAs at HEAD, plus the file-name
      patterns that, if they appear later, should trigger a review."
```

One token spend, two products, atomic.

#### Extended manifest with pattern-based `review_if`

The basic `inputs:` shape in the previous subsection covers files known at generation time. It does not cover the load-bearing failure mode: *a new file appears later that should have been an input*. To address that, the manifest splits into two halves:

```yaml
artifact: tests_overview
artifact_id: tests.overview
generator:
  prompt_sha: 91d0...
  model: claude-opus-4-7
  generated_at: 2026-05-15T12:00:00Z
  generated_commit: 6dcf9a09f51a

hard_refresh_if:
  files:
    - { path: Makefile,                       blob_sha: 1ab4... }
    - { path: package.json,                   blob_sha: 99ef... }
    - { path: .github/workflows/test.yml,     blob_sha: 42cd... }
  symbols: []                                  # optional, sub-file granularity

review_if:
  patterns:
    - "*.config.{js,ts,toml,yaml}"
    - "Makefile.*"
    - "scripts/test-*"
    - "tox.ini"
    - "pytest.ini"

last_validated_commit: 6dcf9a09f51a
validation_history:
  - { at: 2026-05-15T12:00:00Z, commit: 6dcf9a09f51a, outcome: generated, cost_usd: 1.42 }
```

- **`hard_refresh_if.files`** — the LLM declares specific files whose content was directly summarised. Blob SHA mismatch on any of these triggers a full validate-or-patch cycle.
- **`review_if.patterns`** — the LLM declares glob patterns that, if a *new* file matching one appears in the tree (or an existing matched file changes), trigger a cheap validation pass. This is the answer to "a new `cargo.toml` lands and no existing dep references it" — the pattern catches it.

The patterns are LLM-authored too. The model is good at predicting "what kinds of files would be relevant to a test-commands summary that I haven't seen yet" — it's just one more thing to ask for in the same call.

#### Sample one-call output

When the LLM authors `tests_overview` the first time, the generator prompt expects a structured response with two fenced blocks:

````
=== ARTIFACT: tests_overview.md ===

# Test Commands

The project uses three test runners across its Go and JavaScript code:

## Default suite
`make test` — runs `go test ./...` plus `go vet`.

## Docker-bound integration
`make test-docker` — required for any changes to agent/container/runner code.

[…]

=== MANIFEST: tests_overview.deps.yaml ===

artifact: tests_overview
artifact_id: tests.overview
generator: { prompt_sha: 91d0..., model: claude-opus-4-7, generated_at: ..., generated_commit: ... }
hard_refresh_if:
  files:
    - { path: Makefile,                       blob_sha: 1ab4... }
    - { path: .github/workflows/test.yml,     blob_sha: 99ef... }
review_if:
  patterns:
    - "Makefile.*"
    - "*.config.{js,ts}"
    - "pyproject.toml"
````

`ateam index` parses both blocks from the same response, writes them to disk side-by-side. The driver-side recipe is uniform across artifacts — only the deps manifest varies.

#### Why not just emit a per-artifact Makefile?

The natural first instinct (and what was first proposed in chat) is for the LLM to emit `tests_overview.makefile` directly. That works but has friction:

- Make syntax is awkward for the LLM to author robustly (escaping, line continuation, special characters in paths).
- Per-artifact Makefiles duplicate the recipe (the regen command) N times.
- Make can't express `review_if.patterns` cleanly — Make only triggers on declared deps, not on new files matching globs.
- Tying the data format to one orchestration engine forecloses on alternatives (DVC, a Go-native driver, CocoIndex).

The cleaner split: LLM authors the *declarative manifest* (`.deps.yaml`); a deterministic ATeam-side converter generates Make rules (or feeds the manifest to a Go driver) from it. The LLM never has to know Make syntax.

If you do want Make orchestration, the converter emits one tiny shim per artifact:

```make
# .ateam/cache/tests_overview.mk — auto-generated from tests_overview.deps.yaml
.ateam/cache/tests_overview.md: Makefile .github/workflows/test.yml
```

…and a single hand-written top-level Makefile picks them up:

```make
# .ateam/cache/Makefile — generic, never regenerated
include $(wildcard .ateam/cache/*.mk)

.ateam/cache/%.md: .ateam/cache/%.deps.yaml
	ateam index --ids $*
```

`review_if.patterns` are still enforced separately by `ateam index` (since Make can't express them).

#### Stale dep-list risk

The load-bearing concern with LLM-authored deps is dep-list drift: the manifest declares the dep set at time T, but at T+1 the project gains a new file the LLM couldn't predict. Mitigations, increasing in cost:

1. **`review_if.patterns`** (already shown). The LLM writes the patterns at generation time. A new file matching a pattern at any later commit triggers a cheap validation. Patterns + a one-line LLM call ("does this new file affect `tests_overview`?") is enough to catch most cases.
2. **Periodic full re-author**. `ateam index --from-scratch` on a monthly cron. Backstop for accumulated drift that patterns missed.
3. **New-file classifier** (optional, deferred to v2). When *any* new file lands at any path, run a small deterministic classifier ("does this look like a driver for any cached artifact?") and trigger a review when it might be. Cheap, no LLM call until something matches.

`review_if.patterns` + monthly full re-author probably gets 95% of the safety; the new-file classifier is the v2 tightening.

#### What this changes about the v1 plan

Two concrete adjustments to the v1 scope (in §*What lands in v1 of `ateam index`*):

- Every LLM-backed generator prompt explicitly demands the two-output format. Add a parser to `ateam index` that splits the response.
- `hard_refresh_if.files` and `review_if.patterns` are first-class fields in the SQLite registry, not optional add-ons. The change classifier consults both on every commit.

The recipe to author either becomes one prompt template parameterised by `artifact_id`, since the structure is uniform across artifacts.

### Storage layout

```
.ateam/artifacts/
  code_map.md
  code_map.meta.yaml
  dependencies.md
  dependencies.meta.yaml
  hotspots.md
  hotspots.meta.yaml
  architecture/
    INDEX.md
    INDEX.meta.yaml
    concurrency.md
    concurrency.meta.yaml
    isolation.md
    isolation.meta.yaml
    prompt_assembly.md
    prompt_assembly.meta.yaml
    ...
  commands/
    test.md
    test.meta.yaml
    build.md
    build.meta.yaml
    ops.md
    ops.meta.yaml
  .lock                                # held during validate/rebuild
  .log/                                # per-artifact run logs
    architecture.concurrency-2026-05-15T120000Z.jsonl
    ...
```

Living under `.ateam/artifacts/` keeps it next to `runtime/` and `logs/` without conflating semantics. Artifacts are checked in by default (they're authored content); the `.log/` directory is gitignored.

### The make-like driver

A single command with subcommands, callable from a git hook, CI, or the report runner:

```
ateam-artifacts status                     # show what's fresh / drift-candidate / stale
ateam-artifacts validate [--ids ...]       # cheap: SHA compare + LLM "is this still right?" for drift candidates
ateam-artifacts rebuild [--ids ...]        # expensive: re-author
ateam-artifacts validate --as-of HEAD~1    # for git hook integration
ateam-artifacts init                       # bootstrap a fresh project (one-time, expensive)
```

Per-artifact state machine:

```
all input blob_shas match current tree    → FRESH (do nothing, free)
one or more input shas differ             → DRIFT_CANDIDATE
                                          → cheap-validate (one LLM call with diff)
                                            → STILL_CORRECT → bump last_validated_commit (~$0.10)
                                            → NEEDS_PATCH   → patch the affected sections (~$0.50)
                                            → STALE         → full re-author (~$1–3)
```

The cheap validation prompt is small and structured:

```
You authored this artifact at commit <C0>:

  ## Artifact: architecture/concurrency.md
  <artifact body>

Since then, the following inputs have changed (showing diffs):

  ## internal/runner/runner.go (diff against C0)
  <unified diff, capped at N lines>

  ## CONCURRENCY.md (diff against C0)
  <unified diff>

Question: Is this artifact still accurate as of HEAD?
Respond with JSON:
  {"status": "still_correct", "notes": "<one line>"}                                              OR
  {"status": "needs_patch", "sections": ["<heading>", ...], "reason": "<why>", "guidance": ...}   OR
  {"status": "stale", "reason": "<why>"}

Do not include any text outside the JSON.
```

The cheap path is one model call, small input, tiny output. The patch / re-author paths fall back to a full generation prompt with the same input set.

### On-demand injection contract

This is the lever that keeps the always-injected slice small.

**Always injected** (stable prefix, cache-friendly):
- `code_map.md`
- `architecture/INDEX.md` (the TOC)
- `dependencies.md`
- `hotspots.md`
- The role-relevant subset of `commands/*.md`

Total budget: ~6–10K tokens.

**On-demand**, two paths:

1. **Role config declaration.** A role lists the architecture topics it cares about up-front:

   ```yaml
   # defaults/roles/code.structure.yaml (sketch)
   role: code.structure
   architecture_topics:                  # pre-injected after the always-injected block
     - architecture.prompt_assembly
     - architecture.log_layout
   commands:
     - test
     - build
     - ops
   on_demand_topics: true                # may also fetch any other topic via tool
   token_budget:
     architecture_topics: 5000           # cap before truncation
   ```

   The prompt assembler reads this, looks up the artifact files by `artifact_id`, and injects them as a stable second-tier block.

2. **Runtime tool fetch.** The role exposes a tool:

   ```json
   {
     "tool": "fetch_architecture_topic",
     "args": { "topic_id": "architecture.concurrency" }
   }
   ```

   Returns the topic file content as a tool result. Lives outside the cached prefix (it's a tool result, not part of the system prompt), so it doesn't pollute cache reuse across roles.

For a CLI-arg change, the role sees the TOC entry "*Concurrency contract — runner fields read-only after RunPool dispatch*", recognises its change doesn't touch Runner, and never fetches the topic. For a `runner.go` change, the role's config (or a runtime fetch) pulls the concurrency topic in.

### Worked example: a developer adds a `--quiet` flag

Walking the lifecycle on a concrete change:

1. **Commit lands.** `cmd/report.go` and `cmd/root.go` change. No source files cited by any architecture topic are touched.
2. **Git hook fires `ateam-artifacts validate`.**
3. **Driver compares blob_shas:**
   - `code_map.md` inputs (`go.mod`, top-LOC file list, `git log`): one input touched (`git log` output changes). Driver could rerun the deterministic script — sub-second, free.
   - `architecture/concurrency.md` inputs (`internal/runner/runner.go`, `CONCURRENCY.md`): no touched inputs. **FRESH.** No LLM call.
   - `architecture/prompt_assembly.md` inputs: no touched inputs. **FRESH.**
   - `commands/test.md` inputs (`Makefile`, CI files): unchanged. **FRESH.**
   - `commands/build.md` inputs: unchanged. **FRESH.**
4. **Net cost of the commit's artifact update:** zero LLM calls, ~100 ms of script work.

Compare to a commit that refactors `runner.go`:

1. `internal/runner/runner.go` blob_sha changes.
2. `architecture/concurrency.md` is a drift candidate.
3. Cheap-validate runs — one model call with the diff. Output: `{"status": "still_correct"}` → just bumps `last_validated_commit`. ~$0.15.
4. Or output: `{"status": "needs_patch", "sections": ["Cloning Model"]}` → driver runs a patch generation for that section. ~$0.50.
5. Or output: `{"status": "stale"}` → driver re-authors the whole topic. ~$1.50.

The expensive paths only fire on actual drift. The dominant path is FRESH.

### Project-type adaptation

Contracts are generic. Generators are project-aware. A small detector picks the generator stack:

| Marker file | Project profile | Generator stack |
|---|---|---|
| `go.mod` | Go | `go list`, `wc`, `find`, govulncheck-aware deps |
| `package.json` + `next.config.*` | Next.js webapp | `npm ls`, package.json scripts, Vite/Next config awareness |
| `package.json` + `react-native.config.*` | React Native | npm + Metro |
| `Cargo.toml` | Rust | `cargo metadata`, Cargo aliases |
| `pyproject.toml` | Python | `pip-tools`, `pyproject` script section |
| `mix.exs` | Elixir | `mix deps.tree`, mix tasks |
| (none of the above) | Generic | `find`, `git log`, `wc` only |

The differences:

- **What counts as "top-LOC files"**: each profile has its own ignore list (vendored, generated, minified, lock files).
- **Where commands live**: Makefile vs npm scripts vs Cargo aliases vs mix tasks vs `pyproject.toml [tool.poetry.scripts]`.
- **Which dependency manifest to parse.**
- **What "test command" / "build command" / "lint command" looks like.**

But every profile emits artifacts conforming to the same schema. A `commands/test.md` from a Go project and from a Next.js project look structurally identical: a list of `{trigger, command, when_to_run}` entries. Roles consume the schema without knowing the project type.

The generic fallback exists so unknown project types still get a degraded-but-functional artifact set.

### Configuration

```toml
[artifacts]
enabled = true
allow_network = false                        # blocks LLM calls in CI without explicit opt-in
generator_concurrency = 2                    # cap simultaneous LLM rebuilds
prune_log_after = "30d"

[artifacts.budget]
max_total_tokens_injected = 10000            # always-injected slice cap
max_topic_tokens = 3000                      # per architecture topic
max_cheap_validate_diff_lines = 800          # truncation for the validation prompt

[artifacts.architecture]
topics_seed = []                             # optional: maintainer can pre-declare topic names
allow_new_topics = true                      # whether the generator may propose new topics
```

Roles declare their topic interests in their own config (see role-config sketch above).

### Cost characteristics

For a stable project, average cost per commit, by path:

| Path | Frequency | Cost per artifact |
|---|---|---|
| No input file touched | most commits | $0 |
| Input touched, validation says still correct | common | ~$0.10–0.30 |
| Input touched, validation says needs patch | uncommon | ~$0.50–1.50 |
| Major refactor → multiple artifacts stale | rare | ~$3–10 across all artifacts |
| Cold start (`ateam-artifacts init`) | once per project | ~$5–15 |

For a project in active refactoring, you might see $1–5 per commit in artifact updates — paid **once**, not once per report. A subsequent report cycle that consumes the artifacts is dramatically cheaper than today's ~$13 warm baseline because the report no longer pays for the `Project Context` authorship.

### Bootstrap protocol

The first run is expensive and produces artifacts the maintainer should review.

```bash
ateam-artifacts init                # generates the full set at HEAD
ateam-artifacts status              # human-readable summary
$EDITOR .ateam/artifacts/architecture/INDEX.md   # maintainer reviews TOC
$EDITOR .ateam/artifacts/architecture/*.md       # maintainer spot-checks topics
git add .ateam/artifacts/ && git commit -m "bootstrap artifacts"
```

Strongly recommend maintainer review on bootstrap and after any full re-author. Periodic full-rebuild (e.g. monthly via CI) acts as a backstop against silent validation false-OKs.

### Audit trail

Every artifact run produces a stream log alongside the existing report logs:

```
.ateam/artifacts/.log/<artifact_id>-<timestamp>.jsonl
```

The `agent_execs` table in `state.sqlite` gains rows with `action ∈ {artifact_init, artifact_validate, artifact_rebuild, artifact_patch}`. Cost, model, turns, status all tracked the same way as report runs.

This gives operators a complete history: "*who validated `architecture.concurrency` and what did they say?*" → query the table.

### Failure modes and recovery

| Failure | Detection | Recovery |
|---|---|---|
| LLM authors a wrong artifact at bootstrap | Maintainer review on init; periodic full-rebuild surfaces drift between artifact and source | Re-run with `--rebuild --ids <bad-artifact>` after maintainer notes the issue |
| Validation gives false OK (drift went unnoticed) | Periodic full-rebuild backstop; report runs that read the artifact may surface contradictions | Schedule `ateam-artifacts rebuild --all` monthly; investigate any report that flags artifact-vs-code disagreement |
| Corrupt or missing artifact | `ateam-artifacts status` reports MISSING/INVALID; report runner falls back to no-artifact behaviour | `ateam-artifacts rebuild --ids <id>` |
| Two processes try to rebuild simultaneously | `.lock` file in `.ateam/artifacts/` | Second process waits; surfaces error if lock held > N seconds |
| Project-type detector picks the wrong profile | Generated `commands/*.md` look wrong | `[artifacts].project_profile = "go"` override in config |
| Network calls blocked in CI | `allow_network = false` makes registry-dependent generators degrade | Skip dep-status-from-registry sections silently; flag in `status` output |

### Migration from Phase 0/1

Phases 0 and 1 are pre-artifact. The migration path:

1. **Phase 0 ships first** — inject existing project docs (`CLAUDE.md`, `CONCURRENCY.md`, etc.) into the prompt as a stable preamble. This is the cheapest cold-cache fix.
2. **Phase 1 ships second** — auto-Facts block. Mechanical orientation. Together with Phase 0, this closes most of the cold-cache penalty for first-time runs.
3. **Phase 2+ supersedes Phases 0 and 1 mechanism, not function.** Once the artifact system exists:
   - `code_map.md` (artifact) replaces the Phase 1 Facts block — same content, better cache locality, provenance-tracked.
   - `architecture/INDEX.md` + selected topics replace the Phase 0 doc-injection — same content (drawn from the same source docs), but now with on-demand fetch.
   - The model-authored `Project Context` section in reports shrinks to **only** the role-specific role-state-after-this-run memory: findings to revisit, conventions newly discovered, scope declarations. The mechanical and architectural slices go to artifacts.
4. **Roles get a new prompt-assembly contract**: always-injected block (small, stable, cacheable) + role-declared topic block (medium, cache-eligible per role) + runtime tool fetches (uncached).

The migration is incremental: Phases 0 and 1 don't need to be torn out when Phase 2+ ships. The artifact system can co-exist with doc-injection for a transition period.

### Open design questions specific to the artifact system

1. **Topic granularity.** Too few topics = nothing benefits from on-demand fetch. Too many = TOC bloat. Initial heuristic: a topic is a candidate when (a) it has a coherent single-paragraph answer, (b) less than 50% of roles need it, (c) it's non-trivial to re-derive from source. Worth measuring topic-hit-rate per role to tune.
2. **Should symbol-level provenance be opt-in per-artifact or per-input?** Probably per-input — `runner.go` warrants symbol-level (changes often, most changes don't affect the concurrency doc); `CONCURRENCY.md` doesn't need it (small, infrequently touched).
3. **Cross-project artifact reuse?** When ATeam manages multiple projects, the architecture topics are project-specific but the *template* for a topic could be reusable. Probably out of scope for v1.
4. **Should the artifact builder propose its own topic list?** On `init`, the LLM scans the source docs and proposes a TOC. Maintainer can edit. Or accept a pre-declared `topics_seed` list. Probably support both.
5. **Hot-file → topic association.** When `cmd/runner.go` is the most-edited file and *no* architecture topic cites it, that's a flag worth surfacing — it likely means the project has a structural area that lacks documented architectural rationale. Surface as an `ateam-artifacts status` warning?
6. **Validation prompt's diff size cap.** The default 800 lines is arbitrary. Should it be per-input or total? Should oversized diffs auto-promote to STALE without trying to cheap-validate?

## Build vs Buy: External Options and `ateam index`

The Phase 2+ artifact system isn't a greenfield design — the ecosystem has been moving in this direction. `ResearchCodebaseDiscoveryTokenReduction.md` §L catalogues the relevant projects in depth. This section answers the practical follow-up: **which existing tool, if any, should we adopt; what should we build ourselves; and what does the build-ourselves entry point look like?**

### Easy-to-try external evaluations (low setup, fast signal)

For each, the goal is *validating a single hypothesis* — not adopting the tool wholesale.

| Tool | Hypothesis it tests | Evaluation effort | Worth doing? |
|---|---|---|---|
| **llmdoc** | "File-summary + content-hash + previous-summary injection is the minimum useful primitive." | ~1 hour: clone, point at ATeam repo, inspect output, measure token cost on a re-run after a single-file edit | Yes — cheapest possible sanity check that the basic pattern works. |
| **[pdavis68/RepoMapper](https://github.com/pdavis68/RepoMapper)** | "Aider-style PageRank repo-map gives useful 'where to start' orientation independently of the rest of ATeam's stack." | ~1 hour: install, point at ATeam repo, eyeball the map vs the model-authored Project Context in `runtime/33/report.md` | Yes — direct visual comparison of mechanical PageRank vs role-authored map. Tests whether scripts can match the model on the orientation layer. |
| **Repowise** | "Stale-detection (commit-comparison + hook-based auto-sync) catches the cases the Project Context staleness model misses." | 2–4 hours: install, run on ATeam repo, simulate code changes, observe what it flags as stale | Maybe — useful pattern reference. Adopt the *concept*, probably not the tool. |
| **Aider's repo-map (in-process via Aider itself)** | "PageRank-ranked map fits in 1,000 tokens and is good enough as a TOC." | ~2 hours: install Aider, run `aider --show-repo-map`, inspect | Yes alongside RepoMapper — Aider has had this in production longer and the ranking heuristics are well-tuned. |

**Recommended order of evaluation**: llmdoc → RepoMapper → Aider repo-map. Each is a single afternoon; together they give a strong empirical sense of where mechanical scripts can replace LLM authorship.

### Heavier-but-promising external evaluations

These take more effort to evaluate but answer architectural questions the lighter tools can't.

| Tool | What it would replace in our design | Evaluation effort | Worth doing? |
|---|---|---|---|
| **CocoIndex** | The whole make-like driver layer (dependency tracking + memoisation). Could be the substrate underneath ATeam's generators. | Half-day: install, define a small flow (`file_content → file_summary`), test memoisation behaviour after a single-file edit | **Yes — this is the highest-value heavier eval.** If CocoIndex memoisation works as advertised, we'd be writing fewer custom dependency-tracking primitives. |
| **RepoAgent** | The patch-not-regenerate, hook-driven update mechanism for *Python* projects | Half-day: clone, run against a small Python repo, observe Git hook behaviour | Yes if we're targeting Python projects; otherwise pattern-reference only. Validates the design before we re-implement for Go. |
| **Codebase-Memory MCP** | The dependency-oracle layer (graph queries: callers, callees, impact radius) | Half-day: install MCP server, run it against ATeam, query for `Runner.Run` callers etc. | Yes — directly tests whether a graph oracle moves the needle for our roles, separate from doc-update logic. |
| **DeepWiki** | The page-manifest concept (`.devin/wiki.json`-style explicit doc inventory) | None — observe-only via [deepwiki.com](https://deepwiki.com/). It's commercial; we're stealing the concept, not adopting the tool. | Yes — already informed our `manifest.json` design in §L.2.3. |

### Why ATeam should still build the core itself

After the above evaluations land, we'll know whether to integrate or build. The case for building (which I'd weight at ~70% likely as the conclusion):

1. **Role-aware artifacts are unique to ATeam.** None of the external tools generate per-role views: test-selection for `test.gaps` vs for `code.bugs`; security-map vs database-map; what each role *should care about* vs what's in the repo. RepoAgent, CodeWiki, etc. generate one neutral wiki for the whole codebase. ATeam's value is in role-aware filtering, which is a layer above the wiki.
2. **Multi-language scope.** RepoAgent is Python; bazel-diff is Bazel; pytest-testmon is Python. ATeam targets any project. The build-ourselves design specifies project-profile generators (Go / Next.js / Rust / Python / Elixir / …) with shared output contracts.
3. **Existing infrastructure to reuse.** ATeam already has SQLite state, stream.jsonl logging, the runner pool, role prompts, and config plumbing. The artifact system maps onto these naturally — `agent_execs` rows for `action ∈ {artifact_init, artifact_validate, artifact_rebuild, artifact_patch}`; stream files in `.ateam/artifacts/.log/`; existing runner abstractions for invoking the LLM.
4. **The provenance schema (Phase 2+) is more specific than any existing tool.** Our schema declares inputs at file + symbol granularity, separates `hard_refresh_if` from `review_if`, and tracks generator prompt SHAs. No off-the-shelf tool exposes this contract.
5. **Patch-not-regenerate is a design rule no existing tool fully delivers.** RepoAgent gets closest but is Python-only; RepoDoc is research-only. The cheap-validate / patch / re-author tri-state is ATeam-specific.
6. **Effort estimate is bounded.** v1 of the artifact system is ~1–2 person-weeks: file/module summarisers + SQLite dep registry + change classifier + cheap-validate LLM call wrapper + patcher + the `ateam index` command. CocoIndex could shave 2–3 days off the dependency-tracking layer if we choose to depend on it.

**Build-ourselves carries one significant adoption risk** worth naming: if the evaluations of CocoIndex + Codebase-Memory + RepoMapper suggest one of them *almost* solves the problem with a thin wrapper, we should adopt — re-implementing memoisation primitives or symbol-graph queries from scratch is wasteful. Run the four-tool eval gauntlet *before* committing to build.

### `ateam index` — the entry point command

The CLI surface for the artifact system. Sized to match other `ateam` subcommands.

```
ateam index [flags]

Build or refresh the codebase knowledge cache used by report and code agents.

Flags:
  --from-scratch          Rebuild all knowledge docs from raw source (expensive; for bootstrap or recovery)
  --output PATH           Output directory (default: .ateam/cache/)
  --ids ID[,ID...]        Only operate on specified artifact IDs (e.g., architecture.concurrency)
  --validate-only         Cheap LLM validation pass; no full regeneration
  --status                Show fresh / drift-candidate / stale / suspect counts; exit 0 if all current, 2 otherwise
  --commit SHA            Operate against a specific commit (default: HEAD)
  --base SHA              Treat as base for delta-aware paths (default: merge-base with default branch)
  --model NAME            Model for LLM-backed generators (default: from config)
  --max-cost-usd N        Halt before exceeding this LLM budget; exit 3
  --dry-run               Print what would happen; no LLM calls
  --jobs N                Parallel generator runs (default: from config, typically 2)
```

Conventions:

- **Default output**: `.ateam/cache/` per the user spec. Below it the layout matches the §L.5 minimum-viable scheme — `knowledge/`, `state/`, `generated/`.
- **`--from-scratch`** bypasses dependency-based reuse and re-authors every artifact. Use on bootstrap, after major schema changes, or when staleness has been flagged as unrecoverable.
- **`--validate-only`** is the cheap default for CI / pre-commit: walks the artifact set, runs the cheap-validate prompt only on drift candidates, never re-authors. Exits non-zero if any artifact ends up marked `stale`.
- **`--status`** is observe-only; pairs well with `watch ateam index --status` during heavy refactoring.
- **`--max-cost-usd`** is the safety rail. Critical for `--from-scratch` runs and for unattended CI.

Suggested git-hook integration:

```bash
# .git/hooks/post-commit
ateam index --validate-only --max-cost-usd 2.0 || true
```

Suggested CI integration:

```yaml
# CI step
- name: refresh artifacts
  run: ateam index --validate-only --max-cost-usd 5.0
- name: full rebuild (monthly)
  if: github.event_name == 'schedule'
  run: ateam index --from-scratch --max-cost-usd 20.0
```

### What lands in v1 of `ateam index`

Minimum to be useful:

1. **`internal/index/`** Go package with the artifact registry, dependency tracking, generator dispatch.
2. **Deterministic generators** for `code_map.md`, `dependencies.md`, `hotspots.md`, `commands/*.md` (the script-authored artifacts).
3. **LLM-backed generators** for `architecture/INDEX.md` and a small starting set of architecture topics (`concurrency.md`, `isolation.md`, `prompt_assembly.md`, `log_layout.md` — the four most-cited in the existing `runtime/33/report.md` Project Context).
4. **SQLite dependency registry** at `.ateam/cache/state/doc-targets.sqlite`.
5. **Change classifier** — Tree-sitter-based for Go (matches ATeam's own language) + a generic `git diff --stat` fallback for unknown project types.
6. **Cheap-validate prompt** per §L.3.6.
7. **`ateam index` command** with the flags above.
8. **`agent_execs` integration** so artifact runs appear in `state.sqlite` and `ateam ps` alongside reports.

What can wait for v2:

- Multi-language Tree-sitter coverage (start Go-only; add Python/JS/Rust as ATeam targets them).
- Symbol-level dependency granularity (start file-level; add symbol-level for hot files).
- Coverage-based test-selection (start with conventions; add coverage when there's pain).
- MCP server exposing the cache to non-Claude-Code agents.
- Cross-project artifact sharing.

### Open implementation questions

Folded back into the existing "Open questions" section below — see specifically: cold-cache penalty per project type, staleness model granularity, supervisor input cost.

## Open questions (not blocking, worth keeping on the radar)

1. **Cold-cache penalty per project type.** ATeam-on-ATeam shows 84% reduction. Does this hold on a 10× larger codebase? On a Python/JS project? Worth measuring once.
2. **Project Context staleness model.** Currently the model checks "head commit unchanged since prior report" by hand. Should `stale_after_commits` be a real field with harness-side invalidation?
3. **Supervisor input cost.** Supervisor reviews have 50–58K baseline (vs 34–36K for reports) because prior role reports are bundled. Could a compressed-summary format reduce this without losing line-level evidence?
4. **Cross-batch overlap.** Within a batch, roles run in parallel against the same prior context. Across batches, the carry-over is one-step. Should there be a longer-range carry-over for stable findings?
5. **Fast-mode Haiku.** Fired once across all runs in the dataset (cold `code.bugs`, ~10× cheaper on its discovery slice). The activation conditions are not documented in our context. If activatable on demand, this is potentially the single biggest lever in the system.

## What to commit first

When ready to implement Phase 1:

1. `internal/projectmap/` package — fact-extraction logic, golden-output tests
2. `internal/prompts/` integration — inject the Facts block as a stable preamble (cache-friendly position)
3. `cmd/projectmap.go` — standalone subcommand for inspection (`ateam projectmap --commit <sha>`)
4. `.ateam/cache/project_map/<short-sha>/` layout per the appendix spec
5. Configuration: `[projectmap]` block in `config.toml` (`enabled`, `allow_network`, `prune_after`, `max_cached_shas`)

Test plan:
- Golden-output tests on the Facts block
- Integration test: run a single role with Facts injected, assert no orientation calls in first 10 tool calls
- Cost comparison test: same role, with and without Facts, on a fixed commit
