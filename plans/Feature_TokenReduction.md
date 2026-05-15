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

## Revised priority order (replaces §H of ResearchCodebaseDiscoveryTokenReduction.md)

| Phase | What | Evidence | Est. impact |
|---|---|---|---|
| **1** | **Auto-generate Project Facts block** (module/lang/build/top-files/recent-commits/make-targets/default-roles) injected into every role prompt | Cold-vs-warm gap is 84%; 60% of the closing content is mechanical facts that scripts produce more reliably | Closes the cold-run penalty (~$20/cycle on first run at a new SHA) without depending on a prior report existing |
| **2** | **Convert `Project Context` to structured YAML** with `scope / conventions / reverified / revisit / stale_after` fields | Eliminates hallucination risk in re-verification; lets supervisor consume `scope.out` to dedupe across roles | Quality win + supervisor reliability |
| **3** | **Per-role discovery layer** (govulncheck for `project.dependencies`, coverage for `test.gaps`, function-per-file counts for `code.structure`, etc. — full list in appendix of `Research_InvestigateReportLogsForTokenUsage.md`) | Specific roles still over-read at warm: `code.bugs` 51 file reads, `test.gaps` 18 calls. Pre-computed per-role data trims this. | ~$2–4/cycle |
| **4** | **Turn-cap + "name your suspects first" preamble for wandering roles** | `code.bugs` is the only warm role still >20 turns. Surgical fix, not architectural. | ~$1–2/cycle on `code.bugs`-class roles |
| **5** | **Audit hook linking `reverified` claims to `tool_use_id`s in stream.jsonl** | Closes the trust gap that compounds over cycles | Quality only |
| ~~6+~~ | ~~Structural graph, Aider PageRank, hybrid RAG~~ | Discovery is now 12% of cycle cost; further attack on it is third-order | Defer indefinitely |

## Next experiment

**Smallest cost / largest impact next move**: Phase 1, isolated.

1. Build `internal/projectmap` package (~150 LOC Go). Generates a Project Facts block from `git`, `wc`, `find`, `go list`, `defaults/config.toml`. Format per the rerun-doc recommendation (module/LOC/top-files/recent-commits/make-targets/default-roles).
2. Wire into `internal/prompts/` so the runner injects it for every role automatically.
3. **Run the 14 dotted roles cold with the Facts block injected but no prior report.** Compare against the existing cold baseline (in `state.sqlite`, ids 1–20).

**Hypothesis:** cold-with-Facts approaches warm-with-prior-report cost (~$13/cycle), proving the cold-cache penalty is closable without a prior report.

**Decision criteria:**
- If cold-with-Facts ≤ $20/cycle and ≥ same finding count: ship it as Phase 1, proceed to Phase 2.
- If cold-with-Facts is between $20–$30: investigate which roles didn't benefit and why; iterate on Facts content.
- If cold-with-Facts ≥ $30: the role-authored Project Context is carrying more weight than the mechanical facts, and Phase 2 (structured PC) becomes urgent ahead of Phase 1.

## `code.bugs` track (separate from main fix)

Once Phase 1 lands, `code.bugs` will be the conspicuous outlier. Three suggested tactics, ordered by cheapness:

1. **Turn cap (e.g. 40) with budget warning at 80%.** The runner already counts turns. ~20 LOC.
2. **Preamble: "Name up to 7 files you suspect, with one-line rationale each, before any Read."** Prompt-only change.
3. **Per-role pattern bank**: pre-grep for `_ =` discards, empty catch/recover, `time.Sleep` in non-test code, panic call sites — feed results into the role prompt. This is the appendix's role-specific layer applied to `code.bugs`.

Run as an A/B against current `code.bugs` after Phase 1 ships.

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
