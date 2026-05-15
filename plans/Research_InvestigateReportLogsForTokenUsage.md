# Token-consumption analysis: is overlapping discovery a real problem?

## What I measured

I parsed every stream.jsonl for the 16 completed report runs and the 5 supervisor reviews in this cycle. The total work is 796 tool calls, 50.4M cache_read tokens, 2.0M cache_write tokens, 283K output tokens, $42.57 spent. Average 3.15M
cache_read per role, average 50 turns per role.

The headline answer: yes, overlapping discovery is real and measurable — but it's the third-biggest token sink, not the first. The first is static-prompt re-reading per turn (~71% of all cache_read). The second is roles taking far more
turns than they need to (which compounds the first). Cross-role discovery overlap, the thing the prompt asks about, lives inside the remaining ~29%.

Let me unpack the numbers, then come back to what they imply.

## Where the tokens actually go

Three quantitatively-different sinks. I derived each by walking the stream.jsonl events and reconstructing the cache-amplification model.

### Sink 1 — Static baseline × turn count (~71% of cache_read, dominant)

Every role starts with a near-identical prefix: system prompt + tool registry + role prompt + project-info preamble. Measured directly from the first turn's cache_creation_input_tokens:

┌─────────────────────────────────────────────────┬─────────────────┐
│                   Role family                   │ Baseline tokens │
├─────────────────────────────────────────────────┼─────────────────┤
│ code.* / design.* / test.* / docs.* / project.* │ 34,000 – 36,000 │
├─────────────────────────────────────────────────┼─────────────────┤
│ supervisor review (cycle summary)               │ 50,000 – 58,000 │
└─────────────────────────────────────────────────┴─────────────────┘

The baseline is re-cached on every turn. With 50 turns avg per role: 35K × 50 = 1.75M cache_read just from re-reading the prompt. Summed across 16 roles, the static baseline alone accounts for ~71% of the 50.4M cache_read (35.9M amplified
 vs 50.4M actual; the residual ~29% is tool results and accumulated assistant text).

This is invariant of what the role does. The biggest lever is therefore not "share discovery" — it is "cut turn count." A role that finishes in 30 turns instead of 80 saves ~1.7M cache_read regardless of how good its discovery is.

### Sink 2 — Tool-result bytes (~29% of cache_read, of which 40% redundant)

Total tool-result bytes across all roles: 2.23 MB → ~558K raw tokens. After cache-amplification (each result is re-cached for the remaining turns of the run): ~24.7M tokens, matching the residual cache_read.

Of those tool-result bytes, 40.2% are emitted by an identical canonical call already made in another role. 148 calls are exact duplicates. Top offenders (file Reads, by total bytes wasted):

5 roles, 20 reads | internal/runner/runner.go        154,785 b
3 roles,  7 reads | COMMANDS.md                      125,461 b
3 roles, 12 reads | internal/web/handlers.go         110,648 b
4 roles,  5 reads | README.md                         83,156 b
3 roles, 11 reads | internal/calldb/calldb.go         51,767 b
3 roles, 3  reads | CONFIG.md                         49,630 b
5 roles,  5 reads | internal/runtime/config.go        42,617 b
3 roles,  3 reads | DEV.md                            39,462 b
4 roles,  4 reads | Makefile                          24,420 b

This is the "agents rediscovering the same code base" finding the prompt asks about. It is real, and 40% of the relevant slice is non-trivial, but the slice itself is only 29% of total cost.

### Sink 3 — Per-role "warmup orientation" (subset of Sink 2 but worth calling out separately)

Across the 16 report runs, 10 of the 16 spent ≥9 of their first 10 tool calls on layout-orientation (ls .ateam/runtime/<id>/, ls project root, ls .ateam/, `find .ateam -name "*.md"`). Top warmup pattern: ls .ateam/runtime/<id>/ — invoked 9
times across 7 different roles, even though the role itself is the one populating that directory.

Orientation density in first 10 calls:
  code.bugs           9/10        test.quality        10/10
  code.structure     10/10        test.gaps           10/10
  design.architecture 10/10       docs.external        9/10
  docs.internal      10/10        project.security    10/10

Two outliers that did not warm up this way: code.recent (2/10 — went straight to git log) and code.structure run #23 (2/10 — went straight to `find . -name "*.go" | xargs wc -l`).

## Cost calibration

Reported total: $42.57 for 16 roles. If the cache_read pricing for Opus 4.7 is in the $0.50–0.80/M tokens range, the static-baseline component is ~$18–28 and tool-result re-caching is ~$7–12. The redundant-call slice of tool-results is
~$3–5. These are rough — pricing isn't published per my context — but the ratios are what matter.

## Answering the four headline questions

### 1. Is it a real problem?

Yes, with the right framing. 40% of tool-result tokens are spent on identical calls already made by another role, and the warmup pattern (orientation discovery) eats 6-10 of the first 10 turns for most roles. That is genuine waste. But it
 sits inside a smaller cost slice (~29% of total) than the dominant cost (static-baseline amplification, ~71%). So:

- "Overlapping discovery between agents" = real, measurable, ~10-15% of total cycle cost ($4-6 of the $42).
- "Roles run too many turns" = bigger problem, ~30-40% of cycle cost is sensitive to turn count.

### 2. How many tokens are consumed in discovery? As a percentage?

Three definitions, three answers:

- Raw discovery output (Glob/Grep/LS/find/wc results, before cache amplification): 108,419 tokens out of 557,817 tool-result tokens = 19.4% of tool-result tokens.
- Cache-amplified discovery (counting how many turns each discovery output is re-cached in): ~4.5M tokens out of ~24.7M tool-result tokens = 18.5% of tool-result tokens, ~9% of total cache_read.
- Including warmup orientation that happens via Read/Bash instead of Glob/Grep (the ls .ateam/runtime/<id>/ pattern): closer to 25-30% of tool-result tokens, ~7-9% of total cache_read.

So discovery proper consumes ~8-10% of total cycle tokens. The redundant slice of that (identical across roles) is ~3-5%.

### 3. Are some roles more overlapping than others?

Yes, in two distinct ways.

By absolute count of identical calls (within-batch, where roles run together):

docs-batch    (3 roles): 10 shared canonical calls — README.md, COMMANDS.md, ROLES.md, CONFIG.md, FAQ.md, install.sh, ISOLATION.md all read by 2+ roles
project-batch (4 roles): 11 shared calls — install.sh, Makefile, CLAUDE.md, cmd/serve.go, `internal/secret/*`, internal/web/markdown.go
code-batch    (3 roles):  6 shared calls — internal/runner/runner.go, internal/runtime/config.go, internal/agent/{claude,codex}.go, cmd/parallel.go
test-batch    (4 roles):  2 shared calls — only ls and cmd/verify.go

By cost-per-finding (which exposes inefficient roles):
- project.dependencies — $1.99 spent, 1 finding → $1.99 per finding (worst).
- project.security — $4.08, 3 findings → $1.36 per finding.
- design.architecture — $4.25, 5 findings → $0.85.
- code.structure run #23 — $1.09, ~11 findings → $0.10 per finding (best, by a wide margin).

The code.structure rerun is the most informative single data point in the whole dataset: same role, same project, same model, 4× cheaper than the first run. The difference is turn count (23 vs 77) and warmup density (2/10 vs 10/10 orientation calls). When the role skipped layout-orientation and went straight to `find . -name "*.go" | xargs wc -l` followed by targeted grep, the cost dropped 77%. This is the strongest empirical evidence in the data that the cost is mostly turn-count, and turn-count is mostly warmup.

The docs batch overlaps most heavily because all three docs roles legitimately need to read the same handful of top-level markdown files. That overlap is partly structural — it's not waste, it's parallel work on a shared input. The project batch overlap is similar (everyone reads install.sh, Makefile, CLAUDE.md).

### 4. What would solve it — anchored in the data

Listed in expected-impact order:

(a) Pre-compute and inject a "project map" into the role prompt — Highest leverage. Eliminate the warmup turns. Today every role spends 6-10 turns running ls/find to learn that the project has cmd/, internal/, top-level .md files, a .ateam/ directory, and certain agent files. If the harness pre-runs ls -R | head, wc -l cmd/*.go internal/*/*.go, git log --oneline -20, and find .ateam/roles -maxdepth 2 -type d and bakes the output into the role prompt, those 6-10 turns disappear. Estimated savings:
- 16 roles × 8 warmup turns × 35K baseline = ~4.5M cache_read tokens eliminated (~$3-4 of the $42).
- Output quality should not regress — the warmup turns are pure orientation, no model reasoning happens in them.
- Caveat: the map adds ~2-5K tokens to the baseline, but it's added once and amplified across turns just like the rest of the baseline. Net is heavily positive.

(b) Shared, on-disk "discovery cache" the harness writes once per batch — Second-highest leverage. The pricing-table conversion code, internal/runner/runner.go, and the top-level .md files are read by 3-5 different roles. A per-batch precomputed cache directory (.ateam/cache/<batch_id>/) containing pre-rendered file contents would let role prompts say "this file's content is at .ateam/cache/<batch>/internal/runner/runner.go" and the model could cat it via Bash (which is cheaper than a Read tool call because Bash output is a single contiguous bytes blob, no offset/limit accounting). Estimated savings:
- 148 redundant calls × avg 6KB = 224K raw tokens × ~30-turn avg amplification = ~6.7M cache_read tokens eliminated (~$4-6).
- This is a strictly orthogonal win to (a).

(c) Hard turn-count budget per role with a "show your plan" pre-flight — The code.structure rerun (23 turns, $1.09) vs first run (77 turns, $4.80) is the empirical case. Some roles wander. A turn-count budget (e.g. "you have 30 tool calls; spend them wisely") combined with a one-turn plan ("name the 5 files you intend to read before reading any") would force the role to skip warmup. Estimated savings:
- Cut median turn count from ~60 to ~35 = ~40% reduction in baseline amplification = ~14M cache_read tokens (~$8-10).
- Risk: some findings depend on broad exploration. Roles like code.bugs (151 turns, 8 findings) might lose findings. This needs an A/B experiment to validate.

(d) Surgical fix: ban ls .ateam/runtime/<id>/ warmup — The single most common warmup call is the role checking its own runtime dir for a previous report. That directory doesn't exist yet when the role starts running — the role is the one
populating it. Eliminating just this one pattern saves: 9 calls × 7 roles × ~30-turn amplification ≈ 200K-300K cache_read tokens. Small but unambiguous and trivially correctable in role prompts.

(e) Eliminate cross-batch overlap by upstream-rolling the docs reads — All three docs.* roles read README.md, CONFIG.md, COMMANDS.md, etc. — that's structural, they all need this input. The harness could pre-render a "docs digest" once
per batch and inject it. Same logic for the project batch (Makefile, install.sh, CLAUDE.md). Estimated savings:
- docs-batch: ~50K tokens of shared file content × 40 turns avg amplification × 3 roles = ~6M cache_read amortized over the batch.
- project-batch: similar.
- But: this overlaps with (b); pick one approach.

(f) Skip the per-call cache pricing entirely by using subagents — Task calls subagent in their own context; they return only the final answer. Today, only 2 of 796 tool calls in this cycle used Task. Heavy discovery (find files, count
LOC, list packages) could happen in a subagent that returns a 200-token summary instead of a 10,000-token raw listing. Direct evidence: across all 16 roles, 0 used Glob, 0 used LS, 0 used Task for discovery — all discovery went through
Bash (which inflates context).

## Patterns to keep in mind — and patterns that aren't there

### Real patterns from the data:

1. Static baseline is the cost, not the work. ~71% of cache_read comes from re-reading the prompt across turns. Anything that cuts turn count beats anything that cuts tool-result size by 3-5×.
2. Warmup is identical and wasteful. 10+ roles do 6-10 layout-orientation calls in their first 10 turns, and the most expensive of them (looking for previous reports in the role's own runtime dir) is checking a directory that doesn't
exist yet.
3. Bash dominates tool use (415 of 796 calls, 52%). The dedicated Glob/Grep/LS tools combined: 92 calls (12%). The model strongly prefers Bash for discovery — likely because the responses come back unstructured and cheaper-to-reason-over.
4. Cross-role-identical reads cluster on a small set of files. 5 of 5 roles in two cases hit internal/runner/runner.go and internal/runtime/config.go. README/Makefile/CONFIG.md hit by 3-4 roles. These are predictable, pre-cacheable.
5. Fast-mode Haiku absorbs the discovery in code.bugs (2.3M of its 3.0M cache_read is on Haiku, costing $0.45 vs Opus's $2.65). This is the only run where fast-mode Haiku fired — visible in modelUsage. The pricing differential is ~10×, so
 this single run is structurally efficient in a way the others are not. Worth understanding why fast-mode activated here but not in the other 15 runs.

### Patterns I expected to see but didn't:

1. Subagent (Task) decomposition. Almost nonexistent — 2 of 796 calls. If the design experiment is whether agents over-explore, the absence of Task is telling: every role is doing its own discovery sequentially, in-context, never
delegating to a context-isolated subagent that returns just the answer.
2. Glob/LS adoption. Effectively zero. Roles default to Bash with ls/find shell pipelines even when Glob exists. The reason is probably that Bash output is opaque/free-form, so the model isn't constrained by the tool schema — but the cost is the entire shell output goes into context.
3. Read offset/limit usage. I checked a sample: most Read calls don't specify offset/limit, meaning the entire file is loaded. For files like internal/runner/runner.go (1281 LOC) read 20 times across roles, that's ~25K tokens × 20 reads ≈ 500K raw tokens just for that one file — most reads were full-file. The Read tool's offset/limit is under-used.
4. Intra-role redundancy. I expected to find roles re-reading the same file multiple times within a single run. It happens but isn't the main waste — the bigger waste is across roles, not within them.
5. Token cost growth correlated with finding count. It doesn't — project.dependencies cost $1.99 for 1 finding while docs.external cost $1.93 for 12 findings. Cost is dominated by turn count, which is dominated by warmup, which is dominated by role prompt clarity. The two cheapest runs per finding (code.structure #23 at $0.10/finding, docs.external at $0.16/finding) are the two where the role got to work quickly.

## Recommended tools

### Now (to keep analyzing existing logs)

1. A small ateam token-audit subcommand. Given a batch ID or list of exec IDs, parse stream.jsonl and emit: per-role total tokens, turn count, warmup density, redundant-call ratio, cost-per-finding. The Python scripts I wrote above (get_tool_calls, is_discovery, redundancy across roles) are ~100 LOC and would belong in cmd/token_audit.go or scripts/. This is the immediate enabler for any of the changes below — you can't tune what you don't measure cycle-over-cycle.
2. A canonicalized tool-call deduplicator script. Same input, but output a single file listing canonical (tool_name, normalized_input) tuples with the role-count they appear in. This is the data source for deciding what to pre-cache. ~50 LOC; the canonicalization rules I wrote above are the starting point (strip absolute path prefix, replace numeric exec IDs with <id>, normalize whitespace).
3. scripts/claude-usage.py-style histogram of stream.jsonl events. The existing script tracks 5h/7d usage windows; extend it to summarize per-role turn count, tool-call distribution, and warmup-pattern detection over a sliding window of recent runs. The infrastructure to ingest stream.jsonl already exists in internal/streamutil/parse.go.

### Future (as the project grows)

4. A prefetch phase before each batch. The harness runs ls -R, wc -l, git log -20, find .ateam/roles -maxdepth 2, and any role-specific pre-discovery once per batch and writes the output to .ateam/cache/<batch_id>/prefetch.txt. Each role's prompt includes a reference to this file plus a directive: "Do not run ls or find on the project root or .ateam/ — the prefetch above already has it." This is the implementation of recommendation (a) above. ~100 LOC in the harness.
5. A turn-budget enforcement hook. The runner already counts turns. Adding MaxTurns int to Runner config and emitting a stream event when 80% of budget is used ("only N turns left; finalize findings") would let the model self-throttle.
Empirically, code.structure #23 produced equivalent findings in 23 turns vs the first run's 77 turns, so the budget is well below what roles currently use.
6. A "show your plan" preamble in every role prompt. Single sentence: "Before reading any file, output the 5-7 files you plan to read and why. Do not run discovery commands." The two outlier-efficient runs (code.recent, code.structure #23) effectively did this; baking it in would generalize the pattern.
7. An offline replay tool. Given a stream.jsonl, replay it against a Mock provider that returns canned tool results — useful for testing prompt changes without re-spending real API tokens. The MockAgent already exists in internal/agent/mock.go; extending it to read recorded streams would close the loop.

## Suggested further experiments (with concrete designs)

1. A/B: pre-cached project map vs no pre-cache. Run the same 16 roles twice on the same commit, once with a .ateam/cache/prefetch.txt injected into the role prompt and once without. Measure: turn count, cost, finding count, finding overlap with control run. Hypothesis: 30-40% cost reduction, no finding loss. This is the cleanest single experiment.
2. A/B: hard turn budget at 30 vs unbounded. Same 16 roles, half with MaxTurns: 30, half unbounded. Measure same metrics. Hypothesis: 50% cost reduction; some roles (code.bugs, code.structure first run) may lose 1-2 findings; others unchanged. Pair this experiment with (1).
3. Why did fast-mode Haiku fire for code.bugs and nothing else? Examine fast_mode_state in the init events across the 16 runs. If activatable on demand, it's a 10× cost reduction with apparently no quality loss (code.bugs produced one of the higher-quality reports). This may be the single biggest lever in the system and it's currently a black box.
4. Replace Bash-based discovery with Glob/Grep/Task. Add a hook that intercepts Bash calls matching ^(ls|find|grep|wc) , refuses them, and asks the model to use the dedicated tool. Measure: do the dedicated tools produce smaller, more structured outputs? Hypothesis: yes, ~30% reduction in discovery-result bytes.
5. Measure the cost of the supervisor review's input. Supervisor reviews are very cheap (1-3 tool calls, $0.50-0.66), but their cache_creation is large (50-58K vs 34K for report runs) because the prior role reports are bundled into the prompt. A natural cost-reduction would be summarizing each role report to <2K tokens before feeding to supervisor — but this risks losing the line-level evidence that makes supervisor recalibration accurate. Worth testing.

## Bottom line

Cross-role discovery overlap is real (40% of tool-result tokens, ~$4-6 of the $42 cycle cost) and surgically fixable with a pre-cached project map and per-batch file digest. But it's the third biggest token sink behind static-baseline
amplification (71% of cache_read) and excessive turn count. The strongest empirical signal is the code.structure rerun: same role, same code, 77% cost reduction by skipping warmup and going straight to targeted reads. Reproducing that
pattern across roles via prompt changes and a pre-fetched project map would shift roughly 30-40% of total cycle cost — much more than the discovery-deduplication slice alone would yield.

# 2nd Run Analysis

Here we re-ran all roles to check some of the assumptions made above.

Analysis of the re-run + critique of Project Context

## Re-run confirms the warmup hypothesis — strongly

Same 14 roles, same code base, same model. With prior reports on disk to "re-verify":

┌──────────────────────────────────────────────┬─────────────────────┬─────────────────────────────────────────────────────────────┬──────────┐
│                    Metric                    │ Cold run (16 roles) │               Re-run (14 roles, same project)               │    Δ     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Total cost                                   │ $42.57              │ $13.05                                                      │ -69%     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Total cache_read                             │ 50.0M               │ 8.0M                                                        │ -84%     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Total tool calls                             │ 796                 │ 228                                                         │ -71%     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Median turns per role                        │ 66                  │ 18                                                          │ -73%     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Roles with 9-10/10 warmup orientation        │ 10/16               │ 0/14                                                        │ gone     │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Redundant-call ratio (across roles in batch) │ 40.3%               │ 11.7%                                                       │ dropped  │
├──────────────────────────────────────────────┼─────────────────────┼─────────────────────────────────────────────────────────────┼──────────┤
│ Naive baseline-share of cache_read           │ 72%                 │ 165% (saturated; baseline now bigger than tool-result load) │ inverted │
└──────────────────────────────────────────────┴─────────────────────┴─────────────────────────────────────────────────────────────┴──────────┘

No findings were lost. Comparing finding counts across the 13 role pairs that ran in both batches:

role                        first → rerun
code.bugs                       8 → 8
code.structure                 11 → 11
test.quality                    9 → 9
test.blackbox                  13 → 14   (+1)
docs.followable                 8 → 8
docs.internal                   8 → 8
docs.external                   9 → 9
project.security                3 → 4    (+1)
project.dependencies            1 → 1
project.automation              3 → 3
project.production_ready        4 → 4
test.gaps                       8 → 9    (+1)
test.recent                     4 → 7    (+3)

Three roles gained findings — test.recent jumped from 4 to 7 because the headroom freed up by skipping warmup let it look at more recent diff. So the cost savings are not a quality trade-off; in this dataset they're a strict win.

This is the cleanest A/B you could ask for on the "warmup is the dominant cost" hypothesis. It empirically validates the recommendation I made in the previous turn. The Project Context section is doing the job a precomputed project map
would do — plus things a script can't do.

## What Project Context actually contains

I read sections from runs 32, 33, 36, 38, 41 to characterize the content. The role-by-role variance is large, but the items break into five categories:

┌─────────────────────┬─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┬────────────────────────────────────────────────────────────────┐
│      Category       │                                                                     Example                                                                     │                        Source of value                         │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Mechanical facts    │ "171 .go files, ~44k LOC", "Biggest non-test files: handlers.go 1433, table.go 1390, runner.go 1282…", "HEAD commit 6dcf9a0", "Default-on       │ A script could produce these more accurately. Currently        │
│                     │ roles: database_schema, dependencies, …", "Build targets: build, companion, test, …"                                                            │ generated by the model.                                        │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Role-anchored       │ "Event loop at runner.go:513-553, classifyFailure at line 902, promoteRuntimeFiles at line 1160"                                                │ Role-specific judgment about which surfaces matter.            │
│ landmarks           │                                                                                                                                                 │ Mechanical-ish but the selection is role-specific.             │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Conventions         │ "All stream scanners use 64KB/1MB buffer pairs", "runtime/<exec_id>/ is agent-writable, logs/<exec_id>/ is forensic", "Helper that already      │ Pure model judgment. Encodes patterns the role learned. High   │
│ remembered          │ exists for boilerplate: cmd/runner_overrides.go::applyRunnerOverrides"                                                                          │ value.                                                         │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Re-verification     │ "README.md:78 re-read (ataem serve still present); EVAL.md:158-159 re-read (--judge-model sonnet --no-judge still co-present); install.sh:86    │ High value as audit trail. Unverified — model claims, no       │
│ claims              │ re-read…"                                                                                                                                       │ enforcement.                                                   │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Scope /             │ "Out of scope for this role: dependency CVEs (handled by project.dependencies); coverage gaps; feature/design recommendations.", "do not        │ Pure recalibration. Prevents the role from churning on items   │
│ don't-re-flag       │ propose adding auth in future cycles without owner confirmation"                                                                                │ another role owns. High value.                                 │
├─────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────┤
│ Forward pointers    │ "Files / dirs to revisit in future cycles: …"                                                                                                   │ Memory for next run. Medium value.                             │
└─────────────────────┴─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┴────────────────────────────────────────────────────────────────┘

The mix is wildly different per role — code.bugs (run 36) is 90% role-anchored landmarks + conventions. project.automation (run 38) is 60% mechanical facts. docs.followable (run 32) is 30% re-verification + 70% forward-looking notes.

## Criticism

1. Mechanical facts are the biggest waste. "171 .go files, ~44k LOC", file-size lists, recent-commit headers, default-on roles, Makefile target lists — every role generates some of these. They are:
- Generatable by wc, find, git log, grep — exactly the discovery the model is supposed to skip. Including them in Project Context implicitly forces the next run to either re-verify them (defeating the point) or trust the model's memory
(risky if code changes).
- Hallucination-prone. The previous-cycle base/docs reports both confidently asserted that voidexpr/ateam.git was a broken clone URL and that AGENTS.md/CLAUDE.md were duplicates with no shared mechanism. Both wrong. If wrong facts persist in Project Context across runs, the model treats its own prior hallucination as a load-bearing premise.
- Duplicated across roles. Every Project Context section repeats the Go version, the build commands, the file-size leaders. Across 14 roles × ~50 tokens of repeated facts = ~700 tokens of redundancy in the corpus.

2. Re-verification claims are unverifiable. When docs.followable (run 32) writes "README.md:78 re-read (ataem serve still present)", no part of the harness checks that it's true. The model might have written this without actually running a Read. There's no audit log of which tool calls produced which re-verification claim. This is a trust gap that grows over cycles.

3. The format is human-prose, not structured. Bullet points are fine for human readers but they prevent the harness from doing useful things:
- The supervisor cannot reliably parse "out of scope" items to avoid passing them as findings.
- A future role cannot consume just the "conventions remembered" section.
- Project Context can't be diffed across runs in a meaningful way (only line-by-line, which is too noisy).

4. Length is unbounded. Run 33 (code.structure) has a ~700-token Project Context with 9 detailed bullets. Run 32 (docs.followable) has ~500 tokens with verification details. Run 40 (project.production_ready) has ~200. There's no convention for how long it should be or what must be included. The amplification cost (it becomes input on the next run) scales with length × turn count of the next run.

5. There's no model of when context should be discarded. If the codebase substantially changes, the carried-over Project Context becomes stale. Currently the only signal is the model noticing "head commit unchanged since prior report" — fragile, and not enforced.

6. Conflation of two different concerns. Project Context simultaneously serves as:
- (a) Memory for the next run of the same role
- (b) Documentation for human readers of the report

These have conflicting requirements: (a) wants minimal structured data the model can consume efficiently; (b) wants prose with rationale. Trying to be both makes it good at neither.

## Recommendations

### Keep (these are uniquely role-judgment, no script can replace)

- Out-of-scope declarations. "Dependency CVEs handled by project.dependencies" is exactly the kind of cross-role coordination the supervisor needs to honor. Move it to a structured field.
- Conventions remembered. "Stream scanners use 64KB/1MB buffers", "runtime/ is agent-writable, logs/ is forensic". These are patterns the role discovered the hard way. No script produces them.
- Re-verification status (with audit). The mechanism is good; it needs enforcement (next section).
- Forward pointers. "Files to revisit next cycle" is memory the model can act on.

### Replace with a mechanical pre-injection

Add a Project Facts block auto-rendered by the harness into every role prompt:
```
## Project Facts (auto-generated <timestamp>, commit <sha>)

- module: github.com/ateam, go 1.26.3
- 171 .go files, ~44k LOC; 14 test files; 17 internal packages
- 10 largest non-test files:
    1433  internal/web/handlers.go
    1390  cmd/table.go
    1282  internal/runner/runner.go
    ... (top 10)
- recent commits (last 5):
    6dcf9a0  prompts: fix three internal contradictions ...
    2f81a30  review: --roles is authoritative everywhere
    ...
- default-on roles (defaults/config.toml): database_schema, dependencies, docs_external, docs_internal, project_characteristics, refactor_small, security, testing_basic
- make targets: build, companion, test, test-cli, test-docker, test-docker-live, check, run-ci, vuln, install-hooks
- ateam ps (last 10): <id, role, action, status, batch>
```
Implementation: ~80 LOC, identical to what stream-audit.py does but a different shape. Belongs at cmd/<something>.go so the runner injects it automatically.

This eliminates mechanical facts from every Project Context section. The model stops spending attention on counting files and listing build targets. Roles get more accurate facts than the model could produce from memory.

### Enhance — make Project Context structured and auditable

A schema like:

project_context:
  scope:
    in: "experience of following documented procedures"
    out:
      - "doc-content quality → docs.external"
      - "internal docs structure → docs.internal"
  conventions:
    - "stream scanners use 64KB/1MB buffer pairs"
    - "runtime/<exec_id>/ agent-writable, logs/<exec_id>/ forensic"
  reverified:
    - {ref: "README.md:78", claim: "ataem serve typo still present", verified_via: "tool_use_id=…"}
    - {ref: "EVAL.md:158-159", claim: "--judge-model + --no-judge contradiction still present", verified_via: "tool_use_id=…"}
  revisit:
    - "README.md Quick Start"
    - "FAQ.md parallel/exec examples"
    - "install.sh:86"
  stale_after_commits: ["a file:line changed", ...]   # auto-injected

Three immediate wins:
1. The harness can verify reverified claims by linking them to actual tool_use_ids in stream.jsonl. If a role claims "re-read README.md:78" but no Read of README.md happened that run, the supervisor can flag it.
2. The supervisor can consume scope.out to avoid bouncing items between roles.
3. The harness can invalidate carried-over context when the cited commit changes (stale_after_commits).

Render the YAML back as prose for the human-readable report (don't lose the readability), but keep the structured form for machine consumption.

Don't separate it into a new script — bake it into the runner

A standalone ateam project-facts command would be useful, but the real win is having the runner automatically inject the facts block + parsed prior Project Context into every role prompt. That makes the optimization invariant — roles
can't accidentally skip it. The current behavior is "model voluntarily reads prior report.md if it remembers to" — which 14/14 roles did in this batch (good), but isn't guaranteed.

Sketch:
```go
// internal/prompts/prompts.go or similar
func AssembleRolePrompt(role string, env *root.ResolvedEnv) (string, error) {
    parts := []string{
        rolePromptTemplate(role),
        ProjectFacts(env),                 // auto: wc, git log, defaults/config.toml
        PriorProjectContext(role, env),    // structured carry-over from prior report
        RecentChangesSince(role, env),     // git log since last report's HEAD
    }
    return strings.Join(parts, "\n\n"), nil
}
```
This is small (~150 LOC), test-shaped (golden-output tests on ProjectFacts), and would land in the existing prompt-assembly machinery without restructuring.

### Add measurement to the harness so the next round of tuning is data-driven

Three counters worth tracking per run, written next to stream.jsonl:

1. Warmup density (orientation calls in first 10 tool calls). Today it's 0/14 in this batch; in cold-cache runs it spikes. If a future change regresses this, you'd see it immediately. The data is already in stream-audit.py warmup.
2. Re-verification accuracy. For each reverified claim in Project Context, link to the tool_use_id that produced it. % verified vs % claimed = a quality signal.
3. Findings stability. % of findings retained from prior-run-of-same-role. Three roles gained findings here; none lost. A future regression that drops findings should show up as a stability drop.

These can all live in stream-audit.py or a sibling ateam role-stats command.

### Treat the cold-cache → warm-cache gap as the design point

The 84% cache_read reduction between cold and warm runs is the strongest evidence that the system is dramatically over-paying when a role runs for the first time. The Project Context mechanism solves it for subsequent runs but not for the
 first run. The mitigation is: the auto-injected Project Facts block above also serves as the first-run substitute for what previous runs' Project Context provides on later runs.

Combined: cold runs get Project Facts (mechanical), warm runs get Project Facts + Prior Project Context (mechanical + role memory). Both paths skip warmup. The gap closes.

## Bottom line

Don't replace Project Context with scripts. Replace the mechanical 60% of Project Context with scripts, structure the remaining 40% so it's verifiable, and inject both via the runner so it's invariant rather than role-discretion. The empirical case is already in the data: 14/14 roles in this batch ran 3-4× cheaper than their cold-cache versions while producing equal-or-better findings — that's the design target. The current implementation is a clever workaround that proves the design works; productionizing it means moving the mechanism from "model voluntarily writes good Project Context" to "harness enforces good Project Context."

# Appendix: Ideas about pre-computing some of the information agents need

**IMPORTANT**: this script was designed by another agent without reading the reports above. That agent (unlike the report above) understood each role very well and was asked to point out role specific needs/similarities/differences.

Spec: Project map pre-pass for ateam role reports

## Goal

Run once before reports to eliminate the discovery overhead each role independently re-does. Cuts wasted exploration turns and tokens. Generic across project types.

## Output layout

.ateam/cache/project_map/<short-sha>/
├── core.md            # always loaded into every role prompt
├── core.json          # same data structured (for delta computation)
├── roles/
│   ├── code.bugs/discovery.md
│   ├── code.recent/discovery.md
│   ├── test.gaps/discovery.md
│   └── …              # one per role that opts in
└── meta.json          # build SHA, build timestamp, tools used, cached roles

A separate current symlink points at the active SHA's directory. Old caches pruned after N SHAs or N days.

## Two layers

### Layer 1 — Generic core (always built, always injected)

Cheap, ~seconds, works for any project. Always re-run from current state; not cached.

- Identity: project name (from manifest), primary language(s), build system, last commit SHA, branch, age since first commit, age since last commit.
- Layout: top-3-levels file tree; top 30 files by LOC; package/module list; biggest non-test files (debt hotspots).
- Build / test / lint commands: parsed from Makefile / package.json scripts / Cargo.toml / pyproject.toml / equivalent, plus what CLAUDE.md / AGENTS.md says is canonical. Tagged
by tier where detectable (fast / slow / costly).
- Recent activity: last 20 commits (subject + author + date), distinct-author count over last 30 days, files-changed count.
- Documentation surface: list of *.md at root + docs/ first level; presence/absence of CLAUDE.md / AGENTS.md / README / CONTRIBUTING.
- Tool inventory: detected linters / formatters / vuln-scanners / test runners. Configured-yes/no, installed-yes/no. CI workflow files: paths only, not content.
- Manifest summary: direct dep count, language version requirement, license.

This is everything 90% of role prompts manually re-discover in their first 5–10 turns.

### Layer 2 — Role-specific discovery (built on demand, cached by SHA)

Each role declares a manifest of map sections it wants. Built when the role is enabled, cached for re-runs at the same SHA.

Role: code.recent / test.recent
What to pre-compute: git diff <base>..HEAD --stat, uncommitted files, files touched in last N commits ranked by churn
────────────────────────────────────────
Role: code.bugs
What to pre-compute: grep-able patterns: `_ =``  discards, empty catch/recover, scanner.Scan without scanner.Err(), time.Sleep in non-test code, panic call sites, concurrency
  primitive concentration per package
────────────────────────────────────────
Role: code.structure
What to pre-compute: function-per-file count, file-size distribution, catch-all file candidates (>800 LOC + >30 functions), import-graph cycles, dupl output if tool present
────────────────────────────────────────
Role: design.architecture
What to pre-compute: package import graph (DOT), exported-API surface (count + names per package), HTTP/RPC handler inventory if framework-detectable
────────────────────────────────────────
Role: test.gaps
What to pre-compute: coverage profile output (go test -coverprofile / pytest --cov / equivalent), 0%-coverage function list, files without sibling *_test.go
────────────────────────────────────────
Role: test.quality
What to pre-compute: time.Sleep in tests, t.Parallel count, t.Skip count, mock-vs-real-call ratio per test file
────────────────────────────────────────
Role: test.blackbox
What to pre-compute: CLI help-text dumps (<cmd> --help recursively for cobra-style trees), README example commands, public API surface from docs
────────────────────────────────────────
Role: perf.benchmarks
What to pre-compute: Benchmark* function inventory, baseline snapshot (if cached), benchmark coverage per package
────────────────────────────────────────
Role: perf.optimization
What to pre-compute: hot-path candidate paths (CLI commands, HTTP handlers, queue consumers), profiler artifacts (`*.pprof`) if present
────────────────────────────────────────
Role: project.security
What to pre-compute: `.env*` file inventory, install scripts content, Dockerfile base images + pin status, secret-shape grep results in source (api_key=, token=, etc.),
  shell-arg-injection candidate sites (user input → exec/run)
────────────────────────────────────────
Role: project.dependencies
What to pre-compute: dep tree with last-release-date per dep (from registry, opt-in), EOL/archived flags, license per dep, govulncheck/npm audit/pip-audit output
────────────────────────────────────────
Role: project.automation
What to pre-compute: Makefile target list with summarized recipes, CI workflow file content, pre-commit hook on-disk content vs install-hooks template (drift detector)
────────────────────────────────────────
Role: project.production_ready
What to pre-compute: env-separation signals (`.env.*` per env, config.*.yml), destructive commands in scripts (DROP/TRUNCATE/migrate down/rm -rf), prod-credential indicators
  (real-looking secrets in committed env files)
────────────────────────────────────────
Role: project.maintenance
What to pre-compute: KEV-affected packages, packages with no upstream activity in 18+ months, last-build-passing date
────────────────────────────────────────
Role: critic.engineering
What to pre-compute: tech stack inventory (language, framework, persistence, build, deploy)
────────────────────────────────────────
Role: critic.features
What to pre-compute: plans/ files, README "Future" section, TODO/FIXME inventory ranked by file
────────────────────────────────────────
Role: critic.project
What to pre-compute: README content extract, top-level positioning sections
────────────────────────────────────────
Role: docs.accuracy
What to pre-compute: cross-reference table: CLI flags in code vs flags in docs; CLI commands in code vs commands in docs
────────────────────────────────────────
Role: docs.external
What to pre-compute: README structure, install steps as a list, examples surface
────────────────────────────────────────
Role: docs.internal
What to pre-compute: architecture-doc inventory, agent-facing docs content
────────────────────────────────────────
Role: docs.followable
What to pre-compute: install / getting-started / upgrade procedures as step lists
────────────────────────────────────────
Role: database.schema
What to pre-compute: schema files (.sql, ORM models), migration file list with dates, query call sites

Roles that don't have a section just get the generic core. Graceful degradation — they fall back to direct exploration.

## Full vs delta

Generic core: always rebuilt from current state. It's cheap (seconds) and accuracy matters more than caching.

Role-specific: cached by SHA with delta refresh on dirty tree.

- If <short-sha>/ exists AND tree clean → full reuse, instant.
- If <short-sha>/ exists AND tree dirty → cheap re-derived sections (diff stat, uncommitted file list, recent activity) rebuilt; expensive sections (coverage, dep registry, import graph) reused.
- If <short-sha>/ doesn't exist → check for the most-recent cached SHA. If the diff between that SHA and HEAD is small (< 50 files), refresh only affected sections (re-run coverage on touched packages, regen function inventory for touched files). Otherwise full rebuild.
- --rebuild flag forces full rebuild for safety.

This keeps re-runs at the same commit free, and small-diff re-runs cheap.

## Tool tier / what NOT to compute

Use only what's already there. No new tools installed by the script:
- Language built-ins first (go list, cargo metadata, npm ls, git, wc, find).
- Standard ecosystem tools (govulncheck, npm audit, pip-audit, dupl, gocyclo, staticcheck, cloc) only if installed. Skip silently otherwise.
- No network calls without explicit opt-in (--allow-network). Registry queries for dep status / KEV lookup gated.
- No test runs without explicit opt-in. The script may run go build, go vet, go list. It does NOT run make test, make test-docker, anything that could touch external state.
- No writes to the project source tree.

Don't compute things the LLM does better:
- Severity calibration, finding prioritization
- Judgment about which patterns matter
- Qualitative architecture critique
- Anything requiring reading code semantics rather than counting/grepping it

The map is what's there, not what to do about it.

## Integration with ateam

- Hook point: ateam report / ateam all runs the map script before role-prompt assembly. Probably as a new package internal/projectmap invoked by the report pre-pass.
- Prompt assembler:
  - Generic core → injected as a "# Project Map" section into the universal preamble (in report_base_prompt.md assembly, just after Project Info).
  - Role-specific section → injected as # Project Map — <role> after the role prompt, before the base prompt.
- Script failure: log warning, omit the map section, proceed. Roles fall back to direct exploration.
- Config: [projectmap] block in config.toml with knobs for enabled = true, allow_network = false, tools = [...], prune_after = "30d", max_cached_shas = 10.

## Cost characteristics (expected)

- Generic core: 1–10 seconds per run; 1–5 KB of markdown.
- Cheap role-specific: 1–5 seconds; 1–3 KB each.
- Expensive role-specific (coverage, dep registry, full dupl scan): 10–60 seconds first time, free after; 5–20 KB each.
- Total cache footprint per SHA: well under 1 MB.

For a typical full cycle (4–6 roles enabled), expect ~30 seconds extra wall-clock the first time at a new SHA, near-zero on re-run. In return: each role's first 5–10 discovery turns become "read the map" instead of "grep + read + grep + read", and many role reports already include these facts in their "Project Context" section — that section can now be much shorter (or generated from the map directly).

## Open questions worth deciding before implementation

- Per-project vs per-org cache location. Currently proposed at .ateam/cache/project_map/. Per-project is simpler; per-org could share generic-core across worktrees of the same repo. Probably per-project to start.
- Markdown-only or JSON-too. Markdown is what the prompt eats. JSON is what delta-detection needs. The proposal keeps both; that's a duplication. Could generate JSON and convert to markdown on inject. Probably fine to keep both for clarity.
- Should the map script itself be a role? Tempting (consistent UX, leverages role discovery) but the script is deterministic shell-and-tool work, not LLM judgment. Better as a separate command (ateam map [--rebuild]) that runs implicitly before report/all and can be triggered explicitly for inspection.
- What's the right "base" for git diff base..HEAD in code.recent / test.recent? Default branch tip, last tag, configurable, or always HEAD~5? Probably configurable with a default of "merge-base with default branch, fallback to HEAD~5 if no default branch detected".
