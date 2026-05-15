# Research: Codebase Discovery & Token Reduction

*Date: 2026-05-14*

> **Scope of this document.** This is **outside research only** — a survey of repository-map / structural-graph / RAG / prompt-caching / analyzer techniques drawn from the wider ecosystem. It does **not** account for ATeam's own mechanisms or measured behaviour. For anything ATeam-specific, use the two companion documents:
>
> - **[Research_InvestigateReportLogsForTokenUsage.md](./Research_InvestigateReportLogsForTokenUsage.md)** — empirical analysis of real ATeam stream.jsonl traces. Quantifies what actually costs tokens and which interventions help in practice.
> - **[Feature_TokenReduction.md](./Feature_TokenReduction.md)** — integrated findings and the proposed implementation plan. **This is the doc to act on.** It supersedes the priority order in §H below.
>
> Where this document and the empirical docs disagree, the empirical docs win. Several recommendations here (notably the structural-graph layer in §C.3, the Aider-style PageRank framing in §C.1, and the §F experiment design) are over-engineered for what the data actually shows is needed — see `Feature_TokenReduction.md` for the corrected priority order.

---

## A. Executive Summary

**Do not rely on each agent's free-form "overview for the next run" as the main token-saving mechanism.** Prose summaries are too lossy, too stale, and too hard to verify. For ATeam, the best path is a **shared, commit-scoped context plane** that every agent queries before reading code.

The strongest architecture is:

1. **Index the repository once per commit**: file manifest, symbols, imports, call graph, routes, tests, docs, schema/migrations, CI/config, dependency manifests.
2. **Give agents tools, not giant context**: search, symbol lookup, callers/callees, recent-diff impact, test lookup, schema lookup, prior findings.
3. **Store agent work as evidence-linked observations**, not prose summaries: what was read, what was concluded, which file spans support the claim, and when it becomes stale.
4. **Run deterministic analyzers before the LLM**: Semgrep, CodeQL, Trivy, Gitleaks, jscpd/CPD, SQLFluff, Atlas, SchemaSpy.
5. **Use prompt caching and scheduling** where a large shared prefix is unavoidable.

### Target stack

```
git commit SHA
  → deterministic repo facts
  → lexical search index           (ripgrep / Zoekt / OpenGrok)
  → structural code graph          (Codebase-Memory MCP / CodeRLM / Aider-style repo map)
  → optional semantic index        (Qdrant / Chroma / LlamaIndex CodeSplitter)
  → analyzer outputs               (SARIF / JSON)
  → agent work ledger              (SQLite, evidence-linked)
  → role-specific agents
```

**The fundamental shift**: agents should not *discover the repo* repeatedly; they should ask **scoped questions of a shared repo memory** and then inspect only the necessary source spans.

---

## B. Foundations: Why Shared Context Beats Prose Summaries

### B.1 The core problem

A single ATeam run spawns multiple role agents (security, refactor, docs, testing, database, …) against the same commit. Each one wants to "*understand the codebase*" before doing its job. Today, each does this independently:

- it reads top-level directories
- it opens a handful of likely files
- it forms a mental model
- it then begins its specialised work

This **discovery cost** is paid by every role on every run. For overlapping concerns (every role wants to know the route table; security and refactor both want the symbol graph; docs and refactor both want the public-API surface), the cost compounds linearly with role count.

### B.2 Why prose summaries fail

The natural fix — "*have the first agent write a summary, future agents read it*" — looks attractive but is too weak in practice:

- **Stale**: summaries don't track which files they describe. Any commit may silently invalidate them.
- **Ungrounded**: a summary says "*the auth code is in `src/auth` and looks fine*" but the next agent can't tell whether the claim was actually verified.
- **Too general**: summary-style text loses the specific spans an agent would need to act on the claim.
- **Mistake-contaminated**: if the first agent was wrong, every later agent inherits the error.
- **Hard to retrieve precisely**: full-text search over prose returns soft matches; no symbol-level addressability.
- **Hard to invalidate**: no clear rule for when a summary becomes stale.
- **Hard to dedupe**: two agents may write near-identical summaries with subtly different framings.

Summaries are useful only when **converted into evidence-linked, commit-scoped facts** (see §C.5).

### B.3 The shared-context-plane shift

The alternative is to treat the codebase as a **queryable knowledge graph**, built deterministically once per commit, and used by every role. Each role:

1. Queries the shared plane for the *small slice* relevant to its job.
2. Reads exact source spans only when the query result needs verification.
3. Writes its observations back as **evidence-linked, hashable, invalidatable** records.

This pattern shows up across the strongest agent frameworks reviewed in `ResearchAgentFramework.md` — Aider's repo-map, Codebase-Memory MCP, CodeRLM, and the Sourcegraph context engine all converge on it.

---

## C. Techniques (Survey)

### C.1 Repository Maps

A repository map is a compact representation of the codebase: files, important symbols, signatures, relationships, and selected snippets. It gives agents broad orientation without sending the whole repo.

#### Open-source landscape

| Tool | Best use |
|---|---|
| **Aider repo map** | Builds a concise map of the whole Git repo with important files, classes, functions, types, and signatures. Tree-sitter parse + NetworkX PageRank ranking. Default token budget ≈ 1,000, expands when no specific files are pinned to the chat. |
| **Repomix** | Packs a repo into an LLM-friendly artifact with token counting, include/exclude rules, `.gitignore` support, and optional Tree-sitter compression that preserves signatures, interfaces, types, and class structures while dropping implementation details. |
| **Code2Prompt** | Creates structured prompts from a repo with glob include/exclude, templates, Git diff/log extraction, JSON/Markdown/XML formats, MCP support. |
| **Gitingest** | Turns a Git repo into a text digest suitable for LLMs, include/exclude controls. |

> **Side-dive — How Aider's repo map actually works.** Aider parses every file with Tree-sitter (130+ language grammars), extracts every definition and reference, builds a graph where files are nodes and edges are weighted by reference relationships, then runs **NetworkX PageRank** with a personalisation vector biased toward identifiers the user has mentioned. Edge-weight multipliers documented in the Aider write-up: **10× for mentioned identifiers, 10× for well-named identifiers, 50× for files already pinned to the chat**. The result is a budgeted sample of the highest-PageRank symbols, ordered by relevance. ATeam can use this same algorithm without depending on Aider — there's a standalone port at [pdavis68/RepoMapper](https://github.com/pdavis68/RepoMapper). The PageRank+personalisation trick is the most important single idea in this section.

#### How it reduces token usage

Repository maps reduce **repeated orientation cost**. Instead of each agent reading directories, opening files, and reconstructing architecture, they get a compact overview and request specific source only when needed.

Especially useful for:

- global architecture review
- documentation review
- "*where should I look?*" reasoning
- small refactoring suggestions
- deciding which files to inspect next

#### Where it fails

Compressed maps are **not enough** for:

- security analysis requiring exact control/data flow
- bug finding requiring implementation details
- subtle database-migration behaviour
- concurrency bugs
- framework magic
- generated code or dynamic dispatch

Repomix-style compression can strip out exactly the implementation detail a bug-finding agent needs. **Use maps as orientation, not as evidence.**

#### Recommendation for ATeam

Generate a per-commit artifact:

```
.ateam/index/repo-map/{commit}.md
```

Contents:

- pruned file tree
- top-level modules / packages
- public APIs and exported symbols
- entry points (`main`, CLI binaries, web server `init`)
- routes / controllers / jobs / commands
- database migrations and schema entry points
- test layout
- docs layout
- CI / automation files
- dependency manifests
- generated / vendor / ignored regions
- "hot files" from recent diffs and prior findings

Then give each role a **filtered** repo map:

```yaml
security-agent:
  include: auth, permissions, input parsing, secrets, external calls,
           dependency manifests, infra, migrations
docs-agent:
  include: README, docs, public APIs, CLI commands, config schema, examples
database-agent:
  include: migrations, schema files, ORM models, seed data, db tests
```

**Use the map to route the agent, not to let it conclude.**

### C.2 Lexical & Symbol Search

A lexical search index lets agents search code cheaply instead of reading whole files. The minimum viable version is `ripgrep`. For repeated unattended runs across larger repos, use a persistent code-search engine.

| Tool | Notes |
|---|---|
| **ripgrep** | Excellent local baseline. Fast, simple, no index required. |
| **Zoekt** | Indexed code search for Git repos. Substring + regexp, boolean queries, multi-repo/branch, code-aware ranking, API/server modes. README reports sub-50 ms queries on Android- and Chromium-scale corpora. |
| **OpenGrok** | Source search and cross-reference engine, understands source trees and SCM history. |
| **SCIP-compatible indexes** | When language servers or code-intelligence tooling already emit SCIP, you get symbol/reference data for free. SCIP is a language-agnostic protocol for source indexing, go-to-definition, and find-references. |
| **ast-grep** | Tree-sitter-powered **structural** search — patterns are written as code, matched against the AST. Sits between `ripgrep` (lexical) and a full graph database. There's an [`ast-grep-mcp`](https://github.com/ast-grep/ast-grep-mcp) server already built for agent use. |
| **Probe (probelabs/probe)** | "AI-friendly semantic code search" — `ripgrep` speed + Tree-sitter AST parsing. Aimed at coding assistants. |

> **Side-dive — `ast-grep` as the missing middle.** The jump from `ripgrep` (no AST) to a full structural graph (full AST + index) is large and expensive. `ast-grep` is the cheap intermediate step: you write a pattern like `function $NAME($$$_ARGS) { return $$$_ }` and it returns every function across the repo, with names captured. For agents wanting "*find every route handler that doesn't call `requireAuth`*", this is dramatically more precise than regex without the cost of building a full graph. Worth adding to ATeam's tool contract alongside `code_search`.

#### How it reduces token usage

Agents ask **targeted** questions:

```text
search("password|secret|token", paths=["src", "config"], ignore=["testdata"])
search("TODO|FIXME|HACK", paths=["src"])
search("CREATE TABLE users")
search("retry", paths=["docs", "src"])
search("parseJwt")
```

…and read only the matching spans plus nearby context, instead of `read src/**` + `read docs/**` + `read migrations/**`.

#### Best fit

- documentation-coverage checks
- public-API consistency
- finding config/env vars
- security keyword scans
- error-handling patterns
- route/controller discovery
- TODO/FIXME/HACK review
- migration history
- duplicate naming patterns
- dependency usage

#### Where it fails

Lexical search depends on good queries. It misses semantic equivalents: a security agent looking only for `authorize` may miss `policy.check`, `canAccess`, `permitted`, `guard`, `ACL`. Combine with structural and semantic search.

#### Recommendation

Expose a single tool contract to every agent:

```json
{
  "tool": "code_search",
  "args": {
    "query": "permission OR authorize OR policy",
    "paths": ["src"],
    "context_lines": 5,
    "max_results": 30,
    "commit": "<sha>"
  }
}
```

Record every search in the **agent work ledger** (§C.5). Failed searches are useful too: "*security-agent searched for hardcoded secrets in `config/**` and found none*" is a reusable observation when tied to commit SHA and query details.

### C.3 Structural Code Graph (the highest-leverage layer)

A structural code graph indexes the repository by symbols, definitions, references, calls, imports, tests, routes, and relationships. Instead of asking an agent to rediscover the codebase, give it graph queries:

```text
find_symbol("UserService.create")
callers("UserService.create")
callees("UserService.create")
tests_for("UserService.create")
routes_to("UserController.create")
recently_changed_symbols(base, head)
impact_radius(symbol, depth=2)
```

**This is probably the highest-leverage category for ATeam.**

#### Promising open-source projects (May 2026)

| Project | Why it matters |
|---|---|
| **Codebase-Memory MCP** | Tree-sitter parsing across many languages, structural graph in SQLite, MCP-exposed tools, incremental re-indexing with file fingerprints. The paper reports graph-based querying used about **10× fewer tokens** and **2.1× fewer tool calls** than file-exploration baselines, at slightly lower but still high answer quality. Treat as a benchmark to validate on your own repos, not a universal guarantee. |
| **CodeRLM** | Rust server indexing files and symbols with Tree-sitter; JSON APIs for structure, symbols, source, callers, tests, grep, annotations, exploration history. Explicitly supports per-agent annotations visible to other agents in a swarm-like workflow. |
| **Aider repo-map internals** | Not a full graph server, but the Tree-sitter + PageRank approach is directly applicable. |
| **AFT (cortexkit/aft)** | Tree-sitter-powered toolkit for AI coding agents — outline a file's structure in one call, zoom into a function, edit by name, follow callers across the workspace. Aimed at exactly the use case discussed here. |

MCP matters because it gives agents a standard way to access external tools, data, and workflows instead of receiving all context in the prompt.

#### How it reduces token usage

A structural graph lets agents expand context **by need**:

```text
recent-bug agent:
  start from changed symbols
  inspect callers/callees
  inspect tests
  inspect related config/schema
  stop

architecture agent:
  inspect module graph
  inspect high-fan-in/high-fan-out symbols
  inspect cycles
  inspect duplicated abstractions
  stop

security agent:
  inspect routes → handlers → auth checks → DB writes / external calls
  inspect missing-guard patterns
  stop
```

The agent no longer needs to read every file to learn what calls what.

#### Where it fails

Structural tools can be wrong or incomplete, especially with:

- dynamic languages
- metaprogramming
- dependency injection
- reflection
- framework conventions
- generated code
- SQL embedded in strings
- cross-service behaviour

**Every final finding should cite exact source spans, not just graph edges.**

#### Recommendation

Make this a core ATeam service:

```bash
ateam-indexer build --repo . --commit <sha>
ateam-context-server serve --commit <sha>
```

Minimum graph entities:

```
File · Symbol · Import · Call · Reference · Route ·
Command · Job · Migration · Table · Column · Test ·
ConfigKey · DocPage · Finding · Observation
```

Minimum graph queries:

```text
repo_summary()
changed_files(base, head)
changed_symbols(base, head)
symbol(name)
callers(symbol)
callees(symbol)
imports(file)
imported_by(file)
tests_for(symbol_or_file)
routes()
db_tables()
migrations_touching(table)
config_keys()
docs_for(symbol_or_command)
prior_observations(path_or_symbol, role)
```

For recent-change agents, default flow: `diff → changed symbols → impact radius`. For whole-codebase agents, default flow: `module graph → hotspots → representative source spans`.

### C.4 Semantic Retrieval & Hybrid RAG

Semantic retrieval indexes chunks with embeddings so agents can ask conceptual questions:

```text
"Where do we validate user permissions?"
"How is retry behaviour documented?"
"What code handles tenant isolation?"
"Where are background jobs scheduled?"
```

| Tool | Role |
|---|---|
| **Qdrant** | Open-source vector / semantic search engine. |
| **Chroma** | Open-source retrieval infrastructure for embeddings + metadata-filtered search. |
| **LlamaIndex CodeSplitter** | Code-oriented splitting using Tree-sitter and token-based splitting. |

#### How it reduces token usage

Instead of reading all documentation or all modules, agents retrieve the top-k relevant chunks and then verify with exact source. Useful for docs agents, architecture agents, onboarding-style questions, prior agent reports, issue history, design docs, comments and README content, and "*find concept X even when names differ*" queries.

#### Where it fails

Pure vector search is often weak for code because **exact identifiers matter**. It can retrieve conceptually similar but wrong code. For codebases, use **hybrid retrieval**:

```text
hybrid_score =
   α·BM25/lexical
 + β·vector similarity
 + γ·path relevance
 + δ·symbol graph proximity
 + ε·recency / change relevance
 + ζ·prior finding relevance
```

Do not let vector retrieval be the only path to evidence.

#### Recommendation

Use semantic retrieval mainly for:

- docs
- comments
- architecture notes
- previous agent observations
- issue / PR text
- tests with descriptive names
- natural-language descriptions around schema/business logic

Chunk by AST or document section, not arbitrary token windows. Attach metadata:

```json
{
  "commit": "abc123",
  "path": "src/billing/invoices.ts",
  "language": "typescript",
  "symbol": "InvoiceService.finalize",
  "span": [120, 188],
  "kind": "function",
  "blob_sha": "...",
  "tokens": 742
}
```

For code retrieval, require a **two-step**: semantic hit → exact source read → final conclusion.

### C.5 Evidence-Linked Agent Ledger

Later agents should see what earlier agents looked at — but the **format matters**.

A free-form overview is weak:

> "*The auth code generally lives in `src/auth` and seems okay.*"

A reusable observation is structured:

```json
{
  "commit": "abc123",
  "role": "security",
  "claim": "User deletion route checks admin permission before deleting records.",
  "evidence": [
    { "path": "src/routes/users.ts",   "span": [88, 104], "blob_sha": "..." },
    { "path": "src/auth/policies.ts",  "span": [31, 52],  "blob_sha": "..." }
  ],
  "confidence": "medium",
  "invalid_if": [
    "src/routes/users.ts blob changes",
    "src/auth/policies.ts blob changes"
  ],
  "queries_tried": ["delete user", "requireAdmin", "UserPolicy"],
  "created_by_run": "security-2026-05-14T..."
}
```

#### Recommended ledger schema

| Table / collection | Purpose |
|---|---|
| `agent_run` | Role, model, prompt hash, commit SHA, token usage, cache usage, duration, status. |
| `read_event` | Every file/span read by an agent, with reason and token count. |
| `search_event` | Queries tried, filters, result counts, selected results. |
| `observation` | Evidence-linked claim about code / docs / schema / security. |
| `finding` | User-facing issue with severity, category, evidence, repro, suggested fix, status. |
| `coverage` | Which files / symbols / categories were inspected by which role. |
| `negative_result` | "*Looked for X and did not find it*" — tied to queries and scope. |
| `invalidation_key` | Blob hashes, symbol hashes, tool versions, config versions determining staleness. |

#### How later agents should use it

Before reading files, each agent asks:

```text
prior_observations(role="security", path="src/routes/users.ts", commit="<sha>")
prior_findings(symbol="UserService.delete", status=["open", "fixed", "stale"])
coverage(role="database", table="users")
negative_results(query_family="hardcoded secrets", scope="config")
```

…and receives a compact result:

```text
Prior relevant work:
1. security-agent inspected src/routes/users.ts lines 80-130 at commit abc123.
   It found admin checks before deletion. Evidence: ...
   Stale? no.
2. docs-agent noted DELETE /users/:id is undocumented.
   Evidence: route exists; docs omit it.
   Stale? yes — docs changed since observation.
```

#### How it reduces token usage

Prevents **repeated exploration** — and more importantly, **repeated failed exploration**, which is where many unattended agents waste tokens:

```text
Agent A searches for schema docs and finds none.
Agent B later repeats the same search.
Agent C later repeats it again.
```

A ledger lets agents reuse "*already searched*" knowledge while still verifying when needed.

#### Recommendation

Replace "*write an overview for your next run*" with a required structured output:

```json
{
  "read_events": [],
  "observations": [],
  "findings": [],
  "negative_results": [],
  "recommended_next_queries": [],
  "staleness_keys": []
}
```

Keep the prose report for humans, but make the structured ledger the artifact other agents consume.

### C.6 Deterministic Analyzers Before LLM Review

Many dimensions of ATeam reports do not require an LLM to discover the first layer of evidence. Run deterministic tools first; have the LLM **triage, explain, correlate, and prioritise**.

| Dimension | Tools |
|---|---|
| Security SAST | Semgrep Community Edition, CodeQL |
| Dependency / container / config / security | Trivy |
| Secret scanning | Gitleaks, Trivy secret scan |
| Duplication / refactoring | jscpd, PMD CPD |
| SQL / database | SQLFluff, Atlas migrate lint, SchemaSpy |
| Docs / style | markdownlint, Vale, link checkers |
| CI / automation | actionlint, shellcheck, hadolint, language-specific linters |

#### How it reduces token usage

Instead of "*read the repo and find security issues*", do:

```text
run Semgrep / CodeQL / Trivy / Gitleaks
give the agent only:
  - high-confidence findings
  - affected spans
  - rule metadata
  - surrounding source
  - related call graph
ask it to: triage, deduplicate, judge reachability, judge exploitability
```

Turns broad discovery into compact evidence review.

#### Where it fails

Static tools produce false positives and miss business-logic bugs. **LLMs are useful after the tools run** to answer questions like: *Is this reachable? Is it already guarded? Is it exploitable in this app? Is it a duplicate? What's the minimal fix? Does the documentation need updating?*

#### Recommendation

Make analyzers first-class data sources:

```
.ateam/artifacts/{commit}/semgrep.sarif
.ateam/artifacts/{commit}/codeql.sarif
.ateam/artifacts/{commit}/trivy.json
.ateam/artifacts/{commit}/gitleaks.json
.ateam/artifacts/{commit}/jscpd.json
.ateam/artifacts/{commit}/sqlfluff.json
.ateam/artifacts/{commit}/schema.json
```

Then index those artifacts into the same ledger and graph.

### C.7 Prompt Caching & Run Scheduling

Prompt caching reduces cost and latency when multiple requests share a large identical prefix. This matters for ATeam because many agents may share the same system instructions, tool definitions, repo map, style guide, policy rules, and index summaries.

Common pattern:

```
[ system instructions       ]  ← stable across all agents
[ shared tool contract      ]
[ repo policy               ]  ← stable per project
[ repo map for commit abc123]  ← stable per commit
[ static analyzer summary   ]
[ role-specific instructions]  ← varies per agent
[ dynamic question          ]  ← varies per turn
```

Avoid:

```
[ role-specific intro       ]  ← invalidates cache
[ random run id             ]  ← invalidates cache
[ dynamic date text         ]  ← invalidates cache
[ agent-specific scratchpad ]
[ repo map                  ]
```

Small differences **before** the large shared block destroy cache reuse.

> **Side-dive — Anthropic's cache TTL change matters here.** Until ~March 2026 Claude's prompt cache had a 1-hour default TTL. Anthropic silently dropped that to **5 minutes** as the default; the 1-hour TTL is now an opt-in option at **2× the base input rate for cache writes** (vs 1.25× for the 5-minute write). Cache reads stay at **0.1× base input** for both. For ATeam this means: if all overlapping agents on a commit fire within a 5-minute window, you can use the cheap default; if they fan out over longer windows (say nightly + on-demand on the same commit), the 1-hour cache pays back as soon as you have two cache-read runs against it. Plan agent batching around this window.
>
> **Source**: [Anthropic prompt caching docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching); [community write-up of the change](https://dev.to/whoffagents/anthropics-prompt-caching-ttl-change).

#### Parallel vs sequential agents

Parallel agents are good for wall-clock time, but can waste tokens if each has a different prompt prefix. Better pattern:

1. Build shared index once.
2. Create shared cached prefix.
3. Fan out role agents with identical prefix + role-specific suffix.
4. Each agent writes findings / observations to ledger.
5. Optional synthesis agent reads compact outputs only.

#### Recommendation

Track per run:

```text
input_tokens
cached_input_tokens
output_tokens
tool_call_count
source_tokens_read
tokens_per_confirmed_finding
```

Then compare:

```
A. parallel no-cache
B. parallel shared-prefix
C. sequential shared-prefix
D. graph-tools + shared-prefix
```

Prompt caching is especially useful for stable context (repo maps, tool schemas, rubric definitions, project policies). It is less useful for unique source snippets that change per agent.

> **Side-dive — Use git tree-hash, not commit SHA, as the cache key.** Two different commits may have *identical tree contents* (e.g. a re-pushed branch, a rebase that didn't change files, an amended commit message). Git's content-addressable model exposes the tree hash via `git rev-parse HEAD^{tree}`. Keying `.ateam/index/<tree-hash>/...` instead of `.ateam/index/<commit-sha>/...` lets you reuse indexed artifacts across branches and rebases without recomputation. This is invisible to humans but a meaningful CI/build win.

### C.8 Role-Specific Retrieval Policies

Different agents should not discover context the same way. A recent-bugs agent, security agent, docs agent, and architecture agent need different retrieval contracts.

| Role | Start context | Expansion rule | Stop condition |
|---|---|---|---|
| **Recent-bug finder** | Git diff, changed symbols, changed tests, dependency impact | Callers/callees radius 1–2, related tests, recent issues, touched configs | Enough evidence for likely regression or no-risk conclusion |
| **Security** | Routes, auth boundaries, input parsers, external calls, secrets/config, analyzer findings | Trace source → validation → authorization → sink | Evidence-backed finding or scoped negative result |
| **Architecture** | Repo map, module graph, dependency cycles, high fan-in/fan-out files, duplicate abstractions | Sample representative files, inspect edges/cycles | Pattern-level finding with examples |
| **Refactoring** | Duplication reports, complexity metrics, hotspots, repeated patterns | Inspect representative duplicate groups | Concrete small refactor proposal |
| **Documentation** | Public APIs, CLI commands, config schema, routes, examples, docs index | Compare code surface to docs surface | Missing / stale / unclear docs finding |
| **Automation / CI** | CI files, scripts, package manifests, test commands, release config | Trace scripts to commands/tools | Broken / missing automation finding |
| **Database / schema** | Schema, migrations, ORM models, queries, db tests, Atlas/SQLFluff output | Table → model → migration → query → test | Migration / schema risk or doc gap |

#### How it reduces token usage

Agents are **prohibited from broad reads** unless their policy allows it:

```text
security-agent may not read all src/**
security-agent must first inspect route/auth/analyzer indexes
security-agent may expand only through graph edges or search hits
```

Turns unattended agent exploration into bounded investigation.

#### Recommendation

Give every role a **retrieval budget**:

```json
{
  "role": "security",
  "max_source_tokens": 30000,
  "max_files_read": 30,
  "required_first_tools": [
    "routes",
    "security_analyzer_findings",
    "prior_security_observations"
  ],
  "allowed_expansion": [
    "callers", "callees", "search", "tests_for", "config_keys"
  ]
}
```

Agents can request budget increases — but the request itself must be logged and justified.

---

## D. Commercial Options Worth Knowing About

Open source should cover most of ATeam's infrastructure, but a few commercial products are ahead in polished context systems. Treat these as **benchmarks**, not necessarily as build targets.

| Product | Why mention it | Small-team pricing signal |
|---|---|---|
| **Augment Code** | "Context engine" positioning, coding agent, MCP/native tools, small-team plans. Indie $20/mo, Standard $60/dev/mo. | Worth evaluating if you want a polished coding-agent context layer. |
| **Cursor** | Strong IDE agent workflow and shared team context. Individual $20/mo, Teams $40/user/mo. | Good for human-in-the-loop coding, less directly a backend for unattended ATeam. |
| **Qodo** | PR review, code review, context-aware review workflows. Free developer tier. | Useful if one ATeam dimension is PR / review quality. |
| **Sourcegraph** | Strong code search / code intelligence, MCP server / API / CLI, semantic / natural-language search, multi-repo scale. Enterprise starts $16K. | Best when codebase / multi-repo scale justifies it. |

**Recommendation**: do not start by buying a commercial tool. Build the open-source context plane first; use commercial products as benchmarks — "*can our open-source stack answer the same context questions with comparable token use?*"

---

## E. Concrete Architecture for ATeam

### E.1 Index build step

Run once per repo / commit:

```bash
ateam index build \
  --repo . \
  --commit  "$(git rev-parse HEAD)" \
  --tree    "$(git rev-parse HEAD^{tree})" \
  --base    "$(git merge-base main HEAD)"
```

Produces:

```
.ateam/index/<tree-hash>/
  manifest.json
  repo-map.md
  symbols.sqlite
  graph.sqlite
  search-index/
  embeddings/
  analyzer-results/
  docs-index/
  db-index/
  ledger.sqlite
```

### E.2 Agent run flow

1. Load **role policy**.
2. Query **prior observations and findings** from the ledger.
3. Query **repo map / graph / search / analyzers**.
4. Read **exact source spans** only when needed.
5. Produce **evidence-backed findings**.
6. Write **read events, observations, negative results, findings**.
7. Mark **stale prior observations** if touched files changed.

### E.3 Worked examples

**Recent-bug agent**:

```text
Input:  base commit, head commit
Steps:
  1. changed_files(base, head)
  2. changed_symbols(base, head)
  3. tests_for(changed_symbols)
  4. callers/callees radius 1
  5. recent prior findings touching same symbols
  6. inspect exact source spans
  7. produce findings or scoped negative results
```

**Documentation agent**:

```text
Input:  repo@commit
Steps:
  1. public API / CLI / route / config index
  2. docs index
  3. compare surfaces
  4. inspect mismatches
  5. produce missing/stale docs findings
```

**Security agent**:

```text
Input:  repo@commit
Steps:
  1. analyzer findings: Semgrep / CodeQL / Trivy / Gitleaks
  2. routes and external inputs
  3. auth/permission symbols
  4. sensitive sinks: DB writes, filesystem, subprocess, network, deserialization
  5. inspect only relevant traces
  6. produce triaged findings
```

---

## F. Proposed Experiment: Commit-Scoped Map + Overlap Measurement

The simplest experiment that can move the needle: build a deterministic per-commit map via a script, maintain it via a git hook, and measure token reduction when multiple overlapping role agents run on the same commit.

### F.1 Hypothesis

> Prepending every role's prompt with a stable, commit-scoped `repo-map.md` reduces total input tokens by **≥ 30%** across a fan-out of overlapping roles, **without degrading findings quality**, primarily by (a) cutting initial-discovery reads and (b) enabling prompt-cache hits across roles.

### F.2 Two scripts

**`scripts/ateam-map-build.sh`** — deterministic, ~150 LOC, no model calls:

```bash
#!/usr/bin/env bash
# Usage: ateam-map-build.sh <git-ref>
set -euo pipefail

REF="${1:-HEAD}"
TREE="$(git rev-parse "${REF}^{tree}")"
OUT=".ateam/index/${TREE}"

[[ -f "${OUT}/repo-map.md" ]] && { echo "cached: ${OUT}"; exit 0; }
mkdir -p "${OUT}"

# 1. File manifest (respects .gitignore)
git ls-tree -r --name-only "${REF}" > "${OUT}/files.txt"

# 2. Symbol table via universal-ctags (no language-specific deps needed)
ctags -R --output-format=json -f "${OUT}/symbols.jsonl" \
      --languages=Go,TypeScript,JavaScript,Python,Rust \
      --exclude='*.test.*' --exclude='node_modules' --exclude='vendor' \
      "$(git rev-parse --show-toplevel)"

# 3. Compact map (deterministic, no model)
python3 scripts/ateam-map-format.py \
        --files "${OUT}/files.txt" \
        --symbols "${OUT}/symbols.jsonl" \
        --out "${OUT}/repo-map.md"

# 4. Stable pointer
ln -sfn "${TREE}" .ateam/index/HEAD-tree
```

`ateam-map-format.py` (the only non-trivial piece) produces a budgeted map: pruned file tree, top exported symbols per file, routes/CLI commands/migrations detected via simple regex heuristics. Target output size: **≤ 5,000 tokens**.

**`scripts/ateam-map-route.sh`** — filters the map per role:

```bash
#!/usr/bin/env bash
# Usage: ateam-map-route.sh <role>
ROLE="$1"
TREE="$(cat .ateam/index/HEAD-tree)"
python3 scripts/ateam-map-filter.py \
        --map ".ateam/index/${TREE}/repo-map.md" \
        --role-config "configs/roles/${ROLE}.yaml" \
        --out "/tmp/ateam-map-${ROLE}.md"
```

### F.3 Git hook

`.git/hooks/post-commit`:

```bash
#!/usr/bin/env bash
set -e
./scripts/ateam-map-build.sh HEAD &  # build asynchronously, don't block commit
```

Optionally also `post-merge` and `post-checkout`. Builds are idempotent on tree hash, so they're cheap once warm.

### F.4 Role overlap design

Pick three roles that share most of the discovery surface but do different work:

| Role | Needs |
|---|---|
| **security** | Routes, auth, input parsers, config, deps, migrations |
| **refactor_architecture** | Module graph, fan-in/out, duplicates, deps |
| **docs_external** | Public APIs, CLI commands, config schema, routes |

All three need the route table. Two of three need the dependency manifest. Two of three need the public-API surface. **This overlap is the experiment's reason for existing** — if a shared map can't help here, it can't help anywhere.

### F.5 Arms

| Arm | Description |
|---|---|
| **A — Control** | Roles run as today: cold start, no map injected. Discovery via `ls` / `Read` / `grep` from scratch. |
| **B — Shared map** | Each role prepended with the **same** `repo-map.md` as the first stable block of the prompt (cache-friendly). |
| **C — Role-filtered maps** | Each role prepended with a **filtered** map (smaller per-role prefix, but no cache reuse across roles). |

A and B are the primary comparison; C is the secondary "*is filtering worth losing cache reuse?*" test.

### F.6 Metrics (captured per run via the existing call DB)

| Metric | What it measures |
|---|---|
| `input_tokens_uncached` | The dominant cost driver. |
| `input_tokens_cached` | Whether prompt caching actually fired. |
| `output_tokens` | Generation cost. |
| `tool_calls` | Whether retrieval is efficient. |
| `files_read` | Whether agents are over-reading. |
| `source_tokens_read` | How much code each agent consumed. |
| `wall_seconds` | Latency. |
| `findings_count` | Raw output volume. |
| `unique_findings` (cross-role dedupe) | Quality measure. |
| `tokens_per_confirmed_finding` | Cost-quality combined metric. |

### F.7 Procedure

1. Pick a stable commit on a target repo (ATeam itself, or a comparable mid-size Go project).
2. Build the map for that commit: `./scripts/ateam-map-build.sh HEAD`.
3. Run each role **5×** under each arm: 3 roles × 3 arms × 5 runs = **45 runs**. Same prompts, same seed where supported, same commit.
4. Compute mean ± stddev per metric per arm. Use paired comparisons (same commit) to reduce variance.
5. Compute per-role deltas and cross-role deltas separately.

### F.8 Cost-math sketch (sanity check before running)

Assume base input price $P$, cached-read $0.1 P$, 5-min cache-write $1.25 P$. Map size 5,000 tokens. Discovery reads in arm A average 20,000 input tokens per cold role; reads in arm B average 5,000 tokens per role (because the map already covers most of what they were grepping for).

Arm A: $3 \times 5 \times 20{,}000 = 300{,}000$ input tokens at full price → **$300{,}000 P$**.

Arm B, all runs in cache window: first run writes the map (5,000 × 1.25 P), remaining 14 runs read it (5,000 × 0.1 P each) → ~$7{,}250 P$ cache cost. Plus 15 × 5,000 P = $75{,}000 P$ for non-map input. **Total ≈ $82{,}250 P$ ≈ 73% reduction.**

Arm B, cache misses on every run: 15 × 5,000 × 1.25 P = $93{,}750 P$ cache cost + $75{,}000 P$ non-map = **$168{,}750 P$ ≈ 44% reduction**.

So the experiment also tests whether you're getting the cache hits you expected — the gap between 73% and 44% is the cache-hit-rate signal.

### F.9 Decision criteria

| Outcome | Action |
|---|---|
| B reduces total input tokens by ≥ 30% with no degradation in `unique_findings`. | Ship the map + git hook to all roles. |
| B reduces tokens but `unique_findings` drops by > 10%. | Investigate map content — likely missing the slice some role needs. |
| C beats B on quality but loses on cost. | Recommend C only for runs that won't share a cache window (i.e. genuine one-offs). |
| Neither B nor C beats A. | Either the map is too small (raise budget) or the roles weren't actually overlapping (rare). |
| `input_tokens_cached` ≈ 0 in B. | Cache invalidation issue — something dynamic is leaking into the prefix. Fix and re-run. |

### F.10 Stretch goals (post-experiment)

- Add a `symbols.json` query tool exposed to agents (the cheapest possible structural-graph layer).
- Add `ast-grep`-based structural search as a second tool.
- Extend the experiment to **7 roles × 7 runs** once the basic shape works.
- Compare 5-minute vs 1-hour Anthropic cache TTL across runs spread over a workday.

---

## G. General Evaluation Plan (Beyond This Experiment)

Once the basic experiment proves out, expand to a full A/B/C/D/E/F/G/H grid:

| Arm | Setup |
|---|---|
| A | Baseline (current behaviour) |
| B | Baseline + prose overview |
| C | Repo map only |
| D | Repo map + lexical search |
| E | Repo map + structural graph |
| F | Graph + analyzer outputs |
| G | Graph + analyzer outputs + agent ledger |
| H | G + prompt caching / scheduled fan-out |

Compare on: cost, latency, unique useful findings, duplicate findings, false positives, missed known issues, human review time.

**Expectation**: prose overviews alone (B) will show modest benefit; graph + search + analyzer + ledger (G/H) will show much larger benefit.

---

## H. Practical Priority Order

| Phase | What | Why |
|---|---|---|
| **1. Instrumentation** | `agent_run`, `read_event`, `search_event`, `token_usage`, `finding` logging. | Without this you cannot know what helps. |
| **2. Repo map + search** | `repo-map.md`, ripgrep or Zoekt, role-specific include/exclude. | Fastest win. The proposed experiment lives here. |
| **3. Structural graph** | Pilot Codebase-Memory MCP or CodeRLM-style server. Agents ask graph questions before reading source. | The highest-leverage long-term layer. |
| **4. Analyzer integration** | Semgrep, CodeQL where applicable, Trivy, Gitleaks, jscpd/CPD, SQLFluff, Atlas/SchemaSpy. | Turns broad discovery into compact evidence review. |
| **5. Agent work ledger** | Structured, evidence-linked, invalidatable observations. | Replaces prose summaries with reusable knowledge. |
| **6. Prompt caching & scheduling** | Stabilise shared prefixes; fan out agents with identical cached context. | Multiplier on top of everything above. |

---

## I. Open Questions / Future Research

The first-pass research surfaced several threads that need deeper investigation before locking in long-term architecture.

### I.1 Map content & ranking
- **PageRank vs simpler heuristics**: Aider's PageRank+personalisation is impressive but heavy. Can a cheaper "exports + entry points + recently-changed" heuristic achieve 80% of the value? Measure within the proposed experiment.
- **Role-filtered map vs shared map**: the cache-vs-precision tradeoff in §F is real. Are there roles where filtering pays off more (security?) vs roles where the shared map dominates (architecture?)?
- **Token budget per map**: Aider defaults to 1,000 tokens. The proposed experiment uses 5,000. What's the right ceiling for ATeam-scale repos?

### I.2 Invalidation & staleness
- **Cascade semantics**: when `src/auth/policies.ts` changes, which prior observations citing it become stale? Currently every citation invalidates; a smarter model would distinguish line-range overlaps from file-level edits.
- **Symbol-hash invalidation**: hashing each symbol's AST subtree (not just blob SHA) would let observations citing `UserService.create` survive unrelated edits to the same file. Worth prototyping.
- **Tool-version invalidation**: if ripgrep, Tree-sitter grammars, or Semgrep rules update, do prior negative results still hold? Probably not for analyzer outputs; arguably yes for lexical searches.

### I.3 Structural graph quality
- **Granularity**: file-level edges? Symbol-level? Statement-level? Codebase-Memory MCP and CodeRLM differ here.
- **Dynamic-dispatch gaps**: how do we surface "*this graph edge probably exists at runtime but the static analyzer can't see it*" without polluting confidence?
- **Cross-language edges**: a TypeScript front-end calling a Go API. The graph needs to know these are connected; today's tools don't.

### I.4 Hybrid retrieval scoring
- The doc proposes `α·BM25 + β·vector + γ·path + δ·graph + ε·recency + ζ·priors`. **Nobody has tuned these weights for code retrieval at ATeam's use case yet.** A small grid search on representative queries would be high-leverage.

### I.5 Generated code, migrations, and framework magic
- Where does generated code fit in the map? Include? Exclude? Mark explicitly as "*do not edit*"?
- Migrations are time-ordered, not space-ordered — does the graph model that or are they a separate timeline?
- Framework conventions (Rails, Django, Next.js): the implicit routes / controllers / handlers don't appear in source. Worth a per-framework "implicit edges" plugin?

### I.6 Cross-repository graph queries
- When ATeam manages multiple projects, can a finding in repo A reference a contract in repo B (shared API)? How is invalidation propagated across repos?

### I.7 Differential indexing performance
- How fast can we *update* the graph on each commit (not rebuild)? Tree-sitter is incremental but the graph layer often isn't.
- What's the wall-clock cost of `ateam index build` on a 100k-LOC repo? On a 1M-LOC repo? Make this a tracked metric.

### I.8 Token budget enforcement
- The doc proposes per-role budgets (`max_source_tokens`, `max_files_read`). What enforces them at runtime? Container-level memory ulimit equivalents? Agent self-policing? Hard cut-off in the tool-call layer? Probably the last, but the API doesn't exist yet.

### I.9 Ledger contention
- Multiple roles writing observations to a shared SQLite ledger concurrently. WAL mode is probably sufficient but the contention curve isn't measured. Worth a quick benchmark before rolling out.

### I.10 LLM claim verification
- A "graph-side fact-check" step: before accepting an LLM claim ("*this route checks auth*"), verify against the graph that the cited symbols actually exist and the cited edge is present. Could catch a meaningful fraction of hallucinated findings cheaply.

### I.11 Cache-window batching
- Given Anthropic's 5-min default TTL, what's the optimal batching of overlapping roles? Wall-clock parallel fan-out within a 5-min window vs sequential within 1-hour TTL (at 2× write cost)? Worth modelling, then validating.

### I.12 Interaction with role-driven discovery
- The proposed experiment uses three fixed roles. In production, ATeam's coordinator chooses which roles to run. Does the map need to know which roles are coming so it can pre-include their slices? Or does the cache invalidation cost of customisation exceed the gain?

---

## J. Final Recommendation

Yes, index agent work — but **not as ordinary summaries**. Index it as **commit-scoped, evidence-linked observations** plus read/search history.

The best overall strategy for ATeam:

- Do not make every agent reread the repo.
- Make every agent **query a shared context system**.
- Make every conclusion **cite exact evidence**.
- Make every observation **invalidatable by file/symbol hash**.
- Use **deterministic tools** to produce compact evidence.
- Use **prompt caching** for the unavoidable shared prefix.

A strong first implementation:

```
repo map
+ ripgrep/Zoekt search
+ Codebase-Memory or CodeRLM-style structural graph
+ SARIF/JSON analyzer outputs
+ SQLite ledger of agent reads, observations, findings, invalidation keys
```

That will reduce token usage much more reliably than asking agents to write better overviews, while also improving deduplication, auditability, and report quality.

**Start by running the experiment in §F.** It's the cheapest signal-producing thing on the roadmap and answers the load-bearing question — "*does a shared map even help our overlapping agents?*" — before committing to the deeper graph/ledger work.

---

## K. References & Side-Dive Sources

- [Aider: Building a better repository map with tree-sitter](https://aider.chat/2023/10/22/repomap.html)
- [Aider repo-map docs](https://aider.chat/docs/repomap.html)
- [pdavis68/RepoMapper](https://github.com/pdavis68/RepoMapper) — standalone port of Aider's repo-map
- [ast-grep](https://ast-grep.github.io/) — Tree-sitter structural search
- [ast-grep-mcp](https://github.com/ast-grep/ast-grep-mcp) — MCP server for ast-grep
- [cortexkit/aft](https://github.com/cortexkit/aft) — Tree-sitter toolkit for AI coding agents
- [probelabs/probe](https://github.com/probelabs/probe) — `ripgrep` + Tree-sitter semantic search
- [Anthropic prompt caching docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
- [Zoekt](https://github.com/sourcegraph/zoekt)
- [SCIP protocol](https://github.com/sourcegraph/scip)
- [Codebase-Memory MCP](https://github.com/cs-zhanglei/codebase-memory) — structural code graph as MCP server
- [Semgrep CE](https://github.com/semgrep/semgrep), [CodeQL](https://codeql.github.com/), [Trivy](https://github.com/aquasecurity/trivy), [Gitleaks](https://github.com/gitleaks/gitleaks), [jscpd](https://github.com/kucherenko/jscpd), [SQLFluff](https://sqlfluff.com/), [Atlas](https://atlasgo.io/), [SchemaSpy](https://schemaspy.org/)

# Appendix: Incremental Maintenance of Codebase Information - RAW DUMP

Research report: incremental LLM-maintained codebase knowledge for ateam

As of May 14, 2026

Bottom line

Yes, people are actively exploring this, but under several different names:

* repository-level documentation generation
* living code wiki
* persistent codebase memory
* code graph / repository knowledge graph
* incremental LLM dataflow
* test impact analysis
* agent instruction files / rules

The exact thing you described — Makefile-like dependency tracking for LLM-generated architecture, test, verification, and code-map documents — is not yet a mature, standard open-source product. But the pieces exist. The closest matches are:

1. RepoDoc: research prototype closest to your idea. It uses a repository knowledge graph, Git diffs, AST change detection, semantic impact propagation, and selective documentation regeneration.
2. RepoAgent: open-source LLM documentation maintainer with Git-integrated incremental updates, but currently narrower and Python-oriented.
3. CocoIndex: general incremental dataflow framework for LLM transformations; very relevant for “only recompute affected pieces.”
4. Repowise / llmdoc / LLM wiki prototypes: practical early tools for codebase wikis, file hashes, Git checkpoints, and stale detection.
5. Codebase-Memory, CodeRLM, GitNexus, CodeGraphContext: structural code graph systems that can serve as the dependency oracle for deciding which summaries are stale.
6. pytest-testmon, Nx affected, Bazel/bazel-diff, Jest changed-file modes: deterministic tools that already solve part of “what tests/verification commands should run when.”

My recommendation for ateam: do not make the LLM the only source of dependency truth. Treat LLM-written docs as derived build artifacts whose inputs are file hashes, symbol hashes, graph edges, analyzer outputs, test coverage data, and prompt/model versions.

⸻

1. The core pattern: docs as build targets

The best mental model is exactly the one you proposed: generated knowledge documents as build artifacts.

Traditional build systems already solve the analogous problem: determine which outputs need to be rebuilt when inputs change. GNU Make describes its job as automatically determining which pieces of a program need recompilation and issuing the relevant commands; Bazel goes further with declared inputs/outputs, action graphs, and cacheable actions.  ￼

For ateam, the equivalent is:

source files + graph indexes + tests + configs + prior docs
  → generated knowledge docs
  → coding agents / review agents / report agents

Each generated doc should have a dependency declaration, not just a Git hash.

Example:

target: .ateam/knowledge/architecture.md
kind: architecture_summary
commit: 8b7a6c...
generator:
  name: architecture-doc-v3
  prompt_sha: 91d0...
  model: gpt-5.5-pro
inputs:
  files:
    - path: package.json
      blob: 1ab4...
    - path: src/server/routes.ts
      blob: 99ef...
  symbols:
    - name: BillingService
      signature_hash: 42cd...
    - name: UserController.create
      signature_hash: c1a8...
  graph_edges:
    - src/server/routes.ts -> src/billing/BillingService.ts
  derived_indexes:
    - route_index_sha: 771a...
    - schema_index_sha: 3d21...
staleness:
  hard_refresh_if:
    - input_blob_changed
    - public_symbol_signature_changed
    - module_dependency_edge_changed
    - generator_prompt_changed
  review_if:
    - new_file_in_relevant_module
    - new_route_added
    - new_database_table_added

The important detail: the commit hash is necessary but not sufficient. A doc should say both:

Generated from commit abc123

and:

Generated from these exact source spans, symbols, graph edges, commands, analyzer versions, and prompts.

That lets you do Make/Bazel-like invalidation rather than “regenerate everything every commit.”

⸻

2. RepoAgent: Git-aware repository documentation maintenance

RepoAgent is one of the clearest open-source explorations of LLM-maintained code documentation. It aims to generate, maintain, and update repository documentation, and its paper describes three stages: global structure analysis, documentation generation, and documentation update integrated with Git. It builds a project tree, extracts AST-level class/function structure, uses Jedi for caller/callee references, and forms a dependency graph.  ￼

The most relevant part for your idea is its update mechanism. RepoAgent’s paper describes a Git pre-commit hook that checks code changes and updates documentation for affected objects. It updates documentation when an object’s source changes, when referrers no longer reference it, or when new references appear; it also intentionally avoids updating some documentation when only referenced objects change, because references are included primarily for background context rather than direct ownership.  ￼

Why it matters for ateam

RepoAgent is a strong signal that LLM-maintained docs should be tied to code objects and Git changes, not generated as one giant repo summary.

It is especially relevant for:

* per-file summaries;
* per-class/per-function summaries;
* maintaining local code documentation;
* storing dependency-aware documentation units.

Limitations

RepoAgent is narrower than your desired system. Its own paper notes Python-specific limitations due to Jedi, the need for human oversight, hallucination risk, and immature documentation quality evaluation.  ￼

For ateam, RepoAgent is useful as a pattern, but not sufficient as the full solution for:

* architecture documents;
* test-selection documents;
* verification-command documents;
* security maps;
* database maps;
* multi-language repositories.

⸻

3. RepoDoc: closest research match to “Makefile for summary docs”

RepoDoc is probably the closest direct research match to what you are describing.

It builds a repository knowledge graph and uses that graph as the semantic foundation for documentation. Its incremental update section describes taking the existing knowledge graph, existing generated documentation, and a commit diff, then producing updated documentation with minimal regeneration. The paper describes stages including change detection from Git diff plus AST analysis, semantic impact propagation through the RepoKG, selective regeneration, and validation of cross-references.  ￼

This is very close to your desired flow:

changed files
  → changed AST entities
  → affected graph nodes
  → affected documentation sections
  → selectively regenerate only those sections

RepoDoc reports large efficiency gains from incremental updates, including reduced update time and token usage compared with full regeneration. Treat those as research results to validate on your own repos, but the approach is directly relevant.  ￼

Why it matters for ateam

RepoDoc points to the right architecture:

Do not map docs directly to files only.
Map docs to semantic entities and graph relationships.

For example, architecture.md should not depend on every file in src/. It should depend on:

* module graph;
* public APIs;
* entrypoints;
* routes;
* database schema;
* important service boundaries;
* cross-module dependencies;
* selected representative files.

Then a file change only invalidates architecture.md if it changes one of those relevant entities or relationships.

Limitation

RepoDoc is a research system, not necessarily a production-ready drop-in tool. But conceptually, it is the strongest match for ateam’s “minimum LLM work” goal.

⸻

4. CodeWiki, DeepWiki, Google Code Wiki: holistic codebase wikis

Several projects focus on generating full codebase wikis rather than just local docstrings.

CodeWiki

CodeWiki is an open-source framework for generating repository-level documentation across multiple languages. It emphasizes hierarchical decomposition, recursive agentic processing, static-analysis dependency graphs, Tree-sitter extraction, cross-file/cross-module interactions, and system-level diagrams.  ￼

This is relevant for the kinds of documents you listed:

* architecture;
* code map;
* module interactions;
* data flows;
* high-level system understanding.

CodeWiki seems more focused on generating comprehensive docs than on minimal incremental refresh, but its decomposition and dependency-graph approach are useful patterns for ateam.

DeepWiki

Cognition’s DeepWiki is a commercial/non-open system that automatically indexes repositories and produces architecture diagrams, source-linked docs, summaries, and Q&A. It also supports .devin/wiki.json, which lets users steer which pages should exist and what they should cover.  ￼

The .devin/wiki.json idea is especially useful: ateam should have an explicit wiki/page manifest rather than asking the LLM to invent the doc structure every time.

Example:

{
  "pages": [
    {
      "path": ".ateam/knowledge/architecture.md",
      "purpose": "Explain system architecture, module boundaries, and key flows"
    },
    {
      "path": ".ateam/knowledge/test-selection.md",
      "purpose": "Explain what tests to run for different changes"
    },
    {
      "path": ".ateam/knowledge/verification-commands.md",
      "purpose": "List verified commands and when they apply"
    },
    {
      "path": ".ateam/knowledge/code-map.md",
      "purpose": "Map important code areas, entrypoints, schemas, and ownership"
    }
  ]
}

Google Code Wiki

Google has also previewed Code Wiki, described as a platform that scans a full codebase and regenerates documentation after changes, with structured wiki pages, source links, diagrams, and chat over the generated wiki.  ￼

This supports the general thesis that “living codebase wiki” is a real direction. However, from what is publicly described, it sounds more like continuous regeneration after changes than a fully transparent, open-source, minimal-delta Makefile-style system.

⸻

5. CocoIndex: incremental LLM dataflow

CocoIndex is one of the most relevant infrastructure ideas even though it is not only for code documentation. It frames indexing as:

target_state = transformation(source_state)

and tracks dependencies so only affected portions are recomputed. Its code-wiki example describes scanning directories, extracting structured information from Python files using LLMs, aggregating file summaries, and generating Markdown/Mermaid documentation.  ￼

The key feature for your use case is memoization/incrementality. CocoIndex describes using memo=True so unchanged inputs and unchanged transformation code skip recomputation, avoiding unnecessary remote LLM calls. It also discusses choosing granularity — directory, file, or smaller semantic units — and only reprocessing modified files or newly added projects.  ￼

Why it matters for ateam

CocoIndex is very close to the “LLM Makefile engine” layer.

You could model:

file_summary = LLM(file_content)
module_summary = LLM(file_summaries + module_graph)
architecture_doc = LLM(module_summaries + entrypoints + graph)
test_selection_doc = deterministic_test_map + LLM_explanation
verification_doc = command_registry + LLM_explanation

Then recompute only the affected derived nodes.

Caveat

CocoIndex gives you incremental dataflow. It does not automatically know which code changes semantically affect architecture, tests, or verification. For that, you still need a code graph, coverage graph, build graph, or LLM classifier.

⸻

6. Hash-based and Git-checkpoint code wiki tools

There are also smaller practical tools that match parts of your idea.

llmdoc

llmdoc scans a codebase, generates concise summaries, stores them either as comment headers or an index file, and uses SHA-256 hashes to detect changed files so only modified files require LLM calls. It also includes the previous summary during incremental updates.  ￼

This is the simplest useful version of your system:

file hash changed?
  yes → update file summary
  no  → reuse previous file summary

It is not enough for architecture-level staleness, but it is a good primitive.

Repowise

Repowise is another practical early project. It positions itself as codebase intelligence for AI-assisted engineering, with generated docs, dependency graphs, Git analytics, and MCP-style access. Its docs describe detecting when a wiki is stale by comparing the last indexed commit to HEAD, plus auto-sync methods such as post-commit hooks, file watchers, webhooks, and polling.  ￼

This is relevant to ateam because it treats stale knowledge as a first-class operational problem.

LLM wiki idea

Andrej Karpathy’s “LLM wiki” idea is not specific to code, but it describes the broader pattern: instead of rediscovering context on every query, an LLM incrementally builds and maintains a persistent wiki, integrates new sources, resolves contradictions, and keeps a navigable index/log.  ￼

A code-specific article adapts that idea to Git: ingest HEAD, record the last commit, later diff last_commit..HEAD, update affected pages, and advance the checkpoint.  ￼

For ateam, this suggests a useful principle:

Agents should not repeatedly rediscover the repo.
They should read a maintained, source-linked, stale-aware knowledge base.

⸻

7. Structural code graphs: the dependency oracle

To decide whether a summary is stale, file hashes alone are not enough. You need to know what each file means in the codebase.

That is where code graph systems matter.

Relevant projects

Codebase-Memory builds a persistent Tree-sitter-based knowledge graph exposed through MCP, with structural queries such as call graph traversal and impact analysis. Its paper reports large token reductions compared with file-exploration baselines.  ￼

CodeRLM is a Rust server that indexes project files and symbols using Tree-sitter and exposes APIs for structure, symbols, source, callers, tests, grep, and targeted context retrieval.  ￼

GitNexus and CodeGraphContext are newer projects in the same family: local code graph databases / MCP servers that index dependencies, calls, clusters, execution flows, and expose that context to AI agents.  ￼

Aider’s repo map is also relevant: it uses Tree-sitter and graph ranking to create compact code maps for coding agents. It is not primarily a persistent doc updater, but it is strong evidence that compact graph-derived maps are useful for agent context.  ￼

How this applies to stale summary docs

A code graph lets you move from this crude rule:

src/** changed → refresh architecture.md

to this better rule:

changed file contains only private helper body change
  → refresh file summary only
changed public symbol signature
  → refresh file summary, module summary, code map, docs map
changed route/auth/schema/entrypoint
  → refresh architecture, security map, verification commands
new file imported by core module
  → classify and maybe add to architecture/code map
new test file added
  → refresh test-selection doc

This is where the major token savings will come from.

⸻

8. Test and verification command selection: use deterministic tools first

For “what tests to run when” and “what verification commands to run when,” the strongest existing work is not LLM-first. It is test impact analysis and build graph analysis.

Useful tools and patterns

pytest-testmon selects tests affected by changed files or methods by collecting dependencies between tests and executed code using Coverage.py, then comparing code changes against that dependency database.  ￼

Nx affected computes affected projects/tasks from a base and head commit, allowing commands to run only on projects impacted by changes.  ￼

bazel-diff computes affected Bazel targets between two Git revisions, which can be used to select the exact build/test set.  ￼

Jest has changed-file modes such as running tests related to changes since a branch or commit.  ￼

Recommendation for ateam

Do not ask an LLM to invent test-selection logic from scratch. Instead:

coverage data
+ build graph
+ package/project graph
+ test file naming conventions
+ historical test runs
+ LLM explanation layer

The LLM can maintain a readable document like:
```
# What tests to run when
## Backend route changes
Run:
- npm test -- routes
- npm test -- auth
- npm run typecheck
Generated from:
- route index
- Jest dependency graph
- package.json scripts
- historical changed-file test mappings
```
But the actual mapping should come from deterministic sources whenever possible.

⸻

9. Agent instruction files and rules: useful destination, not enough by themselves

There is also a separate ecosystem around persistent agent instructions:

* AGENTS.md
* CLAUDE.md
* Cursor rules
* Windsurf rules/memories
* Cline memory/context systems
* llms.txt

OpenAI Codex documents AGENTS.md as project instructions that Codex reads before work, with root and nested files and a 32 KiB size cap.  ￼

Windsurf distinguishes memories, rules, and AGENTS.md, and supports activation modes such as always-on, model-decision, glob-based, and manual activation. That is relevant because it points toward progressive disclosure: do not always include every summary; include the right summary when the agent is working in the matching area.  ￼

Cline’s docs also highlight context window pressure, token usage, checkpoints, and ways to reduce baseline token use.  ￼

Recommendation for ateam

Use agent instruction files as entrypoints, not as the whole knowledge base.

Example root AGENTS.md:
```
# Agent instructions
Before broad exploration, read:
- .ateam/knowledge/index.md
Use area-specific docs only when relevant:
- .ateam/knowledge/architecture.md
- .ateam/knowledge/code-map.md
- .ateam/knowledge/test-selection.md
- .ateam/knowledge/verification-commands.md
- .ateam/knowledge/security-map.md
Each doc includes freshness metadata and source dependencies.
Do not trust docs marked stale or suspect.
```
Then let ateam decide which doc to expose based on role and touched files.

⸻

10. Recommended ateam design

I would build this as a knowledge build system.

10.1 Generated docs

Start with a small set:

.ateam/knowledge/index.md
.ateam/knowledge/architecture.md
.ateam/knowledge/code-map.md
.ateam/knowledge/test-selection.md
.ateam/knowledge/verification-commands.md
.ateam/knowledge/security-map.md
.ateam/knowledge/database-map.md
.ateam/knowledge/docs-map.md

Each doc should contain:

commit: <git_sha>
status: current | stale | suspect | partial
generated_at: <timestamp>
generator: <name>
prompt_sha: <sha>
model: <model>
input_summary:
  files: <count>
  symbols: <count>
  graph_edges: <count>
  analyzer_outputs: <count>
stale_if:
  - ...

10.2 Store dependency metadata outside Markdown too

Do not parse Markdown frontmatter as the source of truth. Store a machine-readable registry:

.ateam/state/doc_targets.sqlite

Tables:

doc_target
doc_input_file
doc_input_symbol
doc_input_graph_edge
doc_input_command
doc_input_analyzer
doc_generation_run
doc_staleness_event

10.3 Use hierarchical summaries

Avoid regenerating top-level docs directly from raw source.

Use a tree:

file summaries
  → module summaries
  → subsystem summaries
  → architecture summary

Then a changed file updates only:

file summary
possibly module summary
possibly subsystem summary
rarely architecture summary

This is the same general idea used by RepoAgent’s bottom-up documentation generation and RepoDoc’s graph/impact propagation, but adapted to your ateam docs.  ￼

10.4 Classify changes before calling the LLM

For every Git diff:

changed files
  → changed AST entities
  → changed public signatures?
  → changed imports/dependencies?
  → changed routes?
  → changed DB schema?
  → changed test files?
  → changed package/build config?
  → changed docs?

Only then decide which summaries need LLM work.

Example:

Private function body changed
  → update file summary only if summary references behavior
  → no architecture refresh
New route added
  → update code map
  → update architecture if new entrypoint/subsystem
  → update security map
  → update docs map
  → update verification/test-selection docs
package.json scripts changed
  → update verification-commands.md
  → maybe update test-selection.md
new migration added
  → update database-map.md
  → maybe update architecture/security/docs maps

10.5 New-file handling

Your idea that an LLM should judge whether new files need inclusion is right, but use a two-stage process.

First, deterministic classification:

path
extension/language
imports
exports
symbols
test naming
route patterns
schema/migration patterns
config file names
dependency graph location

Then call the LLM only if needed:

Given:
- new file path
- extracted symbols
- imports/exports
- nearest module summary
- existing doc page manifest
Decide:
1. Which knowledge docs, if any, need updates?
2. Which section should change?
3. Is this a local implementation detail or architecture-relevant?
4. What minimal patch should be made?

This avoids spending tokens on obvious cases like:

new snapshot file
new generated file
new test fixture
new private helper

10.6 Use patching, not regeneration

When a doc is affected, do not ask:

Rewrite architecture.md from scratch.

Ask:

Given old section X, changed facts Y, and source evidence Z,
produce a minimal patch to section X.

Then validate:

- all cited files exist
- all cited symbols exist
- commands still exist
- no stale commit hash remains
- generated doc references only current facts

This is closer to Make’s incremental rebuild principle and much cheaper than full regeneration.

⸻

11. Who is furthest ahead?

Closest to your exact research idea

RepoDoc is the closest conceptual match: repository knowledge graph, Git diff, AST change detection, semantic impact propagation, and selective regeneration.  ￼

Closest open-source practical starting point

RepoAgent is a useful starting point for Git-aware incremental code documentation, especially for Python and object-level docs.  ￼

CocoIndex is a strong candidate for the incremental computation layer if you want to build your own system.  ￼

Repowise and llmdoc are useful practical references for Git checkpointing, file hashes, stale wikis, and cheap summary refresh.  ￼

Best codebase understanding / wiki generation systems

CodeWiki, DeepWiki, and Google Code Wiki are the most relevant for holistic architecture/code-map documentation. CodeWiki is open source; DeepWiki and Google Code Wiki are more polished commercial/platform-style examples.  ￼

Best dependency oracle layer

Codebase-Memory, CodeRLM, GitNexus, and CodeGraphContext are the most relevant for building the dependency graph that tells you which docs are affected.  ￼

Best for “what tests to run when”

Use deterministic systems first: pytest-testmon, Nx affected, bazel-diff/Bazel, and Jest changed-file modes. The LLM should explain and maintain the human-readable policy, not be the only mechanism deciding test impact.  ￼

⸻

12. Practical first implementation for ateam

A good MVP would be:

1. Generate file-level summaries with content hashes.
2. Generate module-level summaries from file summaries.
3. Generate architecture/code-map docs from module summaries + graph facts.
4. Generate test-selection docs from coverage/build/test-impact data.
5. Generate verification-command docs from package scripts, CI config, and historical successful commands.
6. Store every doc target’s dependencies in SQLite.
7. On each commit, compute changed files/symbols/edges.
8. Rebuild only affected summaries.
9. Mark uncertain docs as suspect, not current.
10. Let agents consume only relevant current docs.

A minimal layout:

.ateam/
  knowledge/
    index.md
    architecture.md
    code-map.md
    test-selection.md
    verification-commands.md
    security-map.md
    database-map.md
  state/
    manifest.sqlite
    graph.sqlite
    summaries.sqlite
    doc-targets.sqlite
    test-impact.sqlite
  generated/
    file-summaries/
    module-summaries/
    analyzer-results/

A minimal rebuild algorithm:

changed_files = git_diff(base, head)
changed_entities = ast_index.diff(changed_files)
changed_edges = graph.diff(changed_entities)
affected_leaf_summaries = files_with_changed_hashes(changed_files)
affected_module_summaries = modules_containing(affected_leaf_summaries)
affected_docs = dependency_registry.query(
    files=changed_files,
    symbols=changed_entities,
    graph_edges=changed_edges,
)
for doc in affected_docs:
    if doc.can_patch:
        llm_patch_doc_section(doc, changed_facts)
    else:
        llm_regenerate_doc(doc)
mark_unaffected_docs_current_at(head)
mark_uncertain_docs_suspect(head)

The main design rule:

LLM work should happen at the smallest semantic unit that changed.

Not:

Run all agents and regenerate all summaries every commit.

⸻

Final assessment

Your instinct is right. These summary documents would likely be very beneficial for ateam and for coding agents generally, but only if they are treated as versioned, dependency-tracked, evidence-linked build artifacts.

The field is moving in that direction, but it is fragmented:

* RepoAgent and RepoDoc explore incremental repository documentation.
* CodeWiki, DeepWiki, and Google Code Wiki explore generated codebase wikis.
* CocoIndex explores incremental LLM dataflow.
* Codebase-Memory and CodeRLM explore persistent code graphs.
* pytest-testmon, Nx, Bazel, and Jest already solve parts of test impact analysis.
* AGENTS.md/rules systems provide a place for agents to consume the resulting knowledge.

For ateam, the novel and useful thing would be to combine these into one workflow:

code graph
+ build/test impact graph
+ generated doc targets
+ file/symbol/edge hashes
+ Git checkpoints
+ minimal LLM patching
+ stale/suspect/current status

That would give you the benefit of long-lived architecture/test/verification/code-map documents without paying the cost of re-reading and re-summarizing the whole repository on every agent run.

