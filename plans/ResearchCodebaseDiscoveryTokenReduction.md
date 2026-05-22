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
- [RepoAgent (OpenBMB)](https://github.com/OpenBMB/RepoAgent) — Git-aware LLM repository documentation maintainer
- [CocoIndex](https://github.com/cocoindex-io/cocoindex) — incremental LLM dataflow framework
- [pytest-testmon](https://github.com/tarpas/pytest-testmon) — test impact analysis for Python via Coverage.py
- [Nx affected](https://nx.dev/ci/features/affected) — affected projects/tasks from a base/head commit
- [bazel-diff](https://github.com/Tinder/bazel-diff) — affected Bazel targets between two Git revisions
- [DeepWiki (Cognition)](https://deepwiki.com/) — `.devin/wiki.json` page-manifest concept
- Karpathy's "LLM wiki" concept post (broader pattern; not code-specific)
- [Anthropic — How Claude Code works in large codebases](https://claude.com/blog/how-claude-code-works-in-large-codebases-best-practices-and-where-to-start) — Anthropic's own guidance on scoping CLAUDE.md, hooks, sub-agents, LSP, and MCP for large repos (see §M below)

## L. Incremental Maintenance of Codebase Information

*This section follows up on the §H/§I priority work with a deep-dive on a single design question: how to keep LLM-authored knowledge documents fresh as the source code evolves. The pattern the user proposed in `Feature_TokenReduction.md` — generated knowledge documents as build artifacts with declared inputs and make-like incremental update — is not yet a mature, standard open-source product, but the constituent pieces exist across several open-source and research projects. This section catalogues them and extracts the design principles ATeam should adopt.*

### L.1 The core pattern: docs as build targets

The strongest mental model is **generated knowledge documents are build artifacts**.

GNU Make automatically determines which pieces of a program need recompilation; Bazel extends this with declared inputs/outputs, action graphs, and cacheable actions. The analogous transformation for ATeam is:

```
source files + graph indexes + tests + configs + prior docs
  → generated knowledge docs
  → coding agents / review agents / report agents
```

Each generated doc should carry a **dependency declaration**, not just a Git hash. Example target manifest:

```yaml
target: .ateam/knowledge/architecture.md
kind: architecture_summary
commit: 8b7a6c...
generator:
  name: architecture-doc-v3
  prompt_sha: 91d0...
  model: claude-opus-4-7
inputs:
  files:
    - { path: package.json,         blob: 1ab4... }
    - { path: src/server/routes.ts, blob: 99ef... }
  symbols:
    - { name: BillingService,        signature_hash: 42cd... }
    - { name: UserController.create, signature_hash: c1a8... }
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
```

**Key principle**: the commit hash is necessary but not sufficient. A doc should record both "*generated from commit `abc123`*" *and* "*generated from these exact source spans, symbols, graph edges, commands, analyzer versions, and prompts*". That's what enables Make/Bazel-like invalidation instead of regenerating everything every commit.

### L.2 Existing projects, ranked by relevance to the pattern

#### L.2.1 RepoAgent — Git-aware repository documentation maintenance

[OpenBMB/RepoAgent](https://github.com/OpenBMB/RepoAgent). Generates, maintains, and updates repository documentation in three stages: global structure analysis, documentation generation, and Git-integrated documentation update. Builds a project tree, extracts AST-level class/function structure, uses Jedi for caller/callee references, and forms a dependency graph.

The update mechanism is the most relevant piece for ATeam. RepoAgent's paper describes a Git **pre-commit hook** that checks code changes and updates documentation for affected objects. It updates documentation when an object's source changes, when referrers no longer reference it, or when new references appear; it intentionally avoids updating some documentation when *only referenced objects* change, since references are background context, not direct ownership.

**Strengths**: strongest signal that LLM-maintained docs should be tied to **code objects** and **Git changes**, not generated as one giant repo summary. Especially relevant for per-file / per-class / per-function summaries.

**Limitations**: Python-only (Jedi). Doesn't cover architecture documents, test-selection documents, verification-command documents, security maps, database maps, or multi-language repositories.

**ATeam fit**: pattern reference for the file-summary tier of a hierarchical scheme. Not a drop-in.

#### L.2.2 RepoDoc — closest research match to "Makefile for summary docs"

(research paper, not a productionised tool). Builds a repository knowledge graph (RepoKG) and uses it as the semantic foundation for documentation. The incremental update pipeline takes the existing knowledge graph, the existing generated documentation, and a commit diff, and produces updated documentation with **minimal regeneration**:

```
changed files
  → changed AST entities
  → affected graph nodes
  → affected documentation sections
  → selectively regenerate only those sections
```

**Strengths**: the strongest direct architectural match for ATeam's design. Demonstrates the right rule — `architecture.md` should *not* depend on every file in `src/`; it should depend on the module graph, public APIs, entry points, routes, database schema, important service boundaries, cross-module dependencies, and selected representative files. A file change only invalidates `architecture.md` if it changes one of those relevant entities or relationships. Reports large efficiency gains from incremental updates vs full regeneration.

**Limitations**: research system; not necessarily production-ready.

**ATeam fit**: strongest conceptual reference. Informs the dependency-declaration design more than it supplies code.

#### L.2.3 CodeWiki / DeepWiki / Google Code Wiki — holistic codebase wikis

Three projects in the "*living codebase wiki*" space:

- **CodeWiki** (open source). Hierarchical decomposition, recursive agentic processing, static-analysis dependency graphs, Tree-sitter extraction, cross-file / cross-module interactions, system-level diagrams. Focused on comprehensive doc generation; less on minimal incremental refresh.
- **[DeepWiki](https://deepwiki.com/)** (Cognition, commercial). Automatically indexes repositories; produces architecture diagrams, source-linked docs, summaries, Q&A. Supports `.devin/wiki.json` — lets users steer which pages should exist and what they should cover. **The page-manifest idea is directly applicable to ATeam.**
- **Google Code Wiki** (preview). Scans a full codebase, regenerates documentation after changes, structured wiki pages with source links, diagrams, chat over the generated wiki. Continuous regeneration rather than minimal-delta.

The DeepWiki page-manifest idea translates directly:

```json
{
  "pages": [
    { "path": ".ateam/knowledge/architecture.md",
      "purpose": "System architecture, module boundaries, key flows" },
    { "path": ".ateam/knowledge/test-selection.md",
      "purpose": "What tests to run for different changes" },
    { "path": ".ateam/knowledge/verification-commands.md",
      "purpose": "Verified commands and when they apply" },
    { "path": ".ateam/knowledge/code-map.md",
      "purpose": "Important code areas, entry points, schemas, ownership" }
  ]
}
```

**ATeam fit**: borrow the explicit-manifest concept. ATeam declares which knowledge docs exist; the LLM doesn't reinvent the structure each time.

#### L.2.4 CocoIndex — incremental LLM dataflow

[cocoindex-io/cocoindex](https://github.com/cocoindex-io/cocoindex). General incremental dataflow framework for LLM transformations. Frames indexing as `target_state = transformation(source_state)` and tracks dependencies so only affected portions are recomputed. The code-wiki example: scan directories → extract structured info via LLM → aggregate file summaries → generate Markdown/Mermaid documentation. Memoization (`memo=True`) skips recomputation when inputs and transformation code are unchanged. Lets you choose granularity — directory, file, smaller semantic units — and reprocess only modified files or newly added projects.

**Strengths**: very close to the "LLM Makefile engine" layer. Lets you model:

```
file_summary       = LLM(file_content)
module_summary     = LLM(file_summaries + module_graph)
architecture_doc   = LLM(module_summaries + entry_points + graph)
test_selection_doc = deterministic_test_map + LLM_explanation
verification_doc   = command_registry + LLM_explanation
```

…and recompute only the affected derived nodes.

**Limitations**: CocoIndex gives you incremental dataflow. It does *not* know which code changes semantically affect architecture, tests, or verification — that requires a code graph, coverage graph, build graph, or LLM classifier *on top of* CocoIndex.

**ATeam fit**: strong infrastructure candidate if we choose to assemble vs. build from scratch. CocoIndex provides the dependency-tracking + memoisation layer; we provide everything above it.

#### L.2.5 Hash-based and Git-checkpoint small primitives

- **llmdoc**. Scans codebase → generates concise summaries → stores as comment headers or an index file. **SHA-256 hashes** detect changed files so only modified files require LLM calls. Includes the previous summary during incremental updates. The simplest useful version: *file hash changed → update file summary; unchanged → reuse*. Not enough for architecture-level staleness, but a good primitive.
- **Repowise**. Codebase intelligence for AI-assisted engineering with generated docs, dependency graphs, Git analytics, MCP-style access. Detects stale wikis by comparing last indexed commit to HEAD; auto-sync via post-commit hooks, file watchers, webhooks, polling. **Treats stale knowledge as a first-class operational problem.**
- **Karpathy's "LLM wiki" concept**. Not code-specific but describes the broader pattern: instead of rediscovering context on every query, an LLM incrementally builds and maintains a persistent wiki, integrates new sources, resolves contradictions, keeps a navigable index/log. The code-specific adaptation: ingest HEAD, record the last commit, later diff `last_commit..HEAD`, update affected pages, advance the checkpoint.

**ATeam fit**: llmdoc as a baseline to validate the basic pattern at zero cost; Repowise as a reference for the stale-detection operational model.

#### L.2.6 Structural code graphs — the dependency oracle

To decide whether a summary is stale, file hashes alone are not enough. You need to know what each file *means* in the codebase.

- **[Codebase-Memory MCP](https://github.com/cs-zhanglei/codebase-memory)** — persistent Tree-sitter knowledge graph exposed through MCP. Structural queries (call graph, impact analysis). Paper reports large token reductions vs file-exploration baselines.
- **CodeRLM** — Rust server indexing files and symbols with Tree-sitter; APIs for structure, symbols, source, callers, tests, grep, targeted context retrieval.
- **GitNexus**, **CodeGraphContext** — newer projects in the same family. Local code graph databases / MCP servers indexing dependencies, calls, clusters, execution flows; expose context to AI agents.
- **Aider's repo map** — Tree-sitter + graph ranking for compact code maps. Not a persistent doc updater but strong evidence that compact graph-derived maps work for agent context.

A code graph moves you from the crude rule *"`src/**` changed → refresh `architecture.md`"* to the precise rule:

| Change | Refresh |
|---|---|
| Private helper body changes | File summary only |
| Public symbol signature changes | File summary, module summary, code map, docs map |
| Route / auth / schema / entry-point change | Architecture, security map, verification commands |
| New file imported by core module | Classify; maybe add to architecture / code map |
| New test file added | Test-selection doc |

**ATeam fit**: if we build our own knowledge layer, we need at least a thin version of this graph. Tree-sitter is the substrate.

#### L.2.7 Test / verification command selection — deterministic tools first

For "*what tests to run when*" and "*what verification commands to run when*", the strongest existing work is **not LLM-first**. It's test-impact analysis and build-graph analysis.

| Tool | What it does |
|---|---|
| **[pytest-testmon](https://github.com/tarpas/pytest-testmon)** | Selects tests affected by changed files/methods using Coverage.py dependency tracking. |
| **[Nx affected](https://nx.dev/ci/features/affected)** | Computes affected projects/tasks from a base and head commit. |
| **[bazel-diff](https://github.com/Tinder/bazel-diff)** | Computes affected Bazel targets between two Git revisions. |
| **Jest changed-file modes** | Runs tests related to changes since a branch or commit. |

For ATeam the mapping should come from these deterministic sources whenever possible. The LLM's role is to *maintain a readable human-facing document*, not to invent the test-selection logic from scratch:

```
coverage data
+ build graph
+ package/project graph
+ test file naming conventions
+ historical test runs
+ LLM explanation layer
```

#### L.2.8 Agent instruction files — useful destination, not enough by themselves

`AGENTS.md` / `CLAUDE.md` / Cursor rules / Windsurf rules / Cline memory / `llms.txt`. OpenAI Codex documents `AGENTS.md` as project instructions with root and nested files plus a 32 KiB cap. Windsurf supports activation modes (always-on, model-decision, glob-based, manual) — useful pointer for **progressive disclosure**: don't always include every summary; include the right summary when the agent is working in the matching area.

**ATeam fit**: use agent instruction files as *entry points*, not as the whole knowledge base. A root `AGENTS.md` can direct agents at `.ateam/knowledge/index.md`, with area-specific docs only loaded when relevant:

```text
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

ATeam then decides which doc to expose based on role and touched files.

### L.3 Design principles for ATeam

#### L.3.1 Generated doc inventory (starter set)

```
.ateam/knowledge/index.md
.ateam/knowledge/architecture.md
.ateam/knowledge/code-map.md
.ateam/knowledge/test-selection.md
.ateam/knowledge/verification-commands.md
.ateam/knowledge/security-map.md
.ateam/knowledge/database-map.md
.ateam/knowledge/docs-map.md
```

Each doc carries provenance:

```yaml
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
```

#### L.3.2 Store dependency metadata outside Markdown

Don't parse Markdown frontmatter as the source of truth. Keep a machine-readable registry:

```
.ateam/state/doc_targets.sqlite
```

Tables:

```
doc_target
doc_input_file
doc_input_symbol
doc_input_graph_edge
doc_input_command
doc_input_analyzer
doc_generation_run
doc_staleness_event
```

#### L.3.3 Hierarchical summaries

Avoid regenerating top-level docs directly from raw source. Build a tree:

```
file summaries
  → module summaries
  → subsystem summaries
  → architecture summary
```

A changed file then updates only the file summary, possibly the module summary, occasionally the subsystem summary, and rarely the architecture summary. Same idea as RepoAgent's bottom-up generation and RepoDoc's graph impact propagation.

#### L.3.4 Classify changes before calling the LLM

For every Git diff, deterministically classify *what kind* of change before invoking the LLM:

```
changed files
  → changed AST entities
  → changed public signatures?
  → changed imports/dependencies?
  → changed routes?
  → changed DB schema?
  → changed test files?
  → changed package/build config?
  → changed docs?
```

Decision rules:

| Change pattern | Update |
|---|---|
| Private function body | File summary only if it references behavior; no architecture refresh |
| New route | Code map, architecture, security map, docs map, verification/test docs |
| `package.json` scripts | Verification-commands doc; maybe test-selection doc |
| New migration | Database map; maybe architecture / security / docs |

#### L.3.5 New-file handling — two-stage

First, deterministic classification:

```
path · extension/language · imports · exports · symbols
· test naming · route patterns · schema/migration patterns
· config file names · dependency graph location
```

Then call the LLM only if needed:

```
Given:
  - new file path
  - extracted symbols
  - imports/exports
  - nearest module summary
  - existing doc page manifest
Decide:
  1. Which knowledge docs, if any, need updates?
  2. Which section should change?
  3. Local implementation detail or architecture-relevant?
  4. What minimal patch should be made?
```

Skip the LLM for obvious cases: new snapshot file, new generated file, new test fixture, new private helper.

#### L.3.6 Patch, don't regenerate

When a doc is affected, prefer:

> Given old section X, changed facts Y, and source evidence Z, produce a minimal patch to section X.

…over:

> Rewrite `architecture.md` from scratch.

Then validate: all cited files exist, all cited symbols exist, commands still exist, no stale commit hash remains, generated doc references only current facts. This is Make's incremental rebuild principle, applied to LLM output.

### L.4 Who is furthest ahead, by axis

| Need | Best match |
|---|---|
| Conceptually closest to "Makefile for summary docs" | **RepoDoc** (research) |
| Practical Git-aware starting point (open source) | **RepoAgent** (Python-oriented but the closest open impl) |
| Incremental dataflow infrastructure | **CocoIndex** |
| Holistic codebase wikis (high-quality docs) | **CodeWiki** (open), **DeepWiki** / **Google Code Wiki** (commercial / preview) |
| Dependency-oracle layer (graph) | **Codebase-Memory**, **CodeRLM**, **GitNexus**, **CodeGraphContext** |
| Test impact analysis | **pytest-testmon**, **Nx affected**, **bazel-diff**, **Jest changed-file** modes |
| Light primitives | **llmdoc**, **Repowise** |

### L.5 Minimum viable implementation

A useful MVP:

1. Generate file-level summaries with content hashes.
2. Generate module-level summaries from file summaries.
3. Generate architecture / code-map docs from module summaries + graph facts.
4. Generate test-selection docs from coverage / build / test-impact data.
5. Generate verification-command docs from package scripts, CI config, and historical successful commands.
6. Store every doc target's dependencies in SQLite.
7. On each commit, compute changed files / symbols / edges.
8. Rebuild only affected summaries.
9. Mark uncertain docs as `suspect`, not `current`.
10. Let agents consume only relevant `current` docs.

Minimal layout:

```
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
```

Rebuild algorithm:

```python
changed_files  = git_diff(base, head)
changed_entities = ast_index.diff(changed_files)
changed_edges    = graph.diff(changed_entities)

affected_leaf_summaries   = files_with_changed_hashes(changed_files)
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
```

Design rule:

> **LLM work should happen at the smallest semantic unit that changed**, not "*run all agents and regenerate all summaries every commit*".

### L.6 Final assessment

The design instinct is correct: these summary documents would likely be very beneficial for ATeam and for coding agents generally — **but only if they are treated as versioned, dependency-tracked, evidence-linked build artifacts**.

The field is moving in this direction but is fragmented:

- **RepoAgent**, **RepoDoc** — incremental repository documentation
- **CodeWiki**, **DeepWiki**, **Google Code Wiki** — generated codebase wikis
- **CocoIndex** — incremental LLM dataflow
- **Codebase-Memory**, **CodeRLM** — persistent code graphs
- **pytest-testmon**, **Nx**, **Bazel**, **Jest** — test impact analysis
- **AGENTS.md** / rules systems — agent consumption surface

The novel and useful combination for ATeam is:

```
code graph
+ build/test impact graph
+ generated doc targets
+ file/symbol/edge hashes
+ Git checkpoints
+ minimal LLM patching
+ stale/suspect/current status
```

That gives long-lived architecture / test / verification / code-map documents without paying the cost of re-reading and re-summarising the whole repository on every agent run.

---

## M. Anthropic's Official Guidance — How Claude Code Works in Large Codebases

*Source: [How Claude Code works in large codebases — best practices and where to start](https://claude.com/blog/how-claude-code-works-in-large-codebases-best-practices-and-where-to-start) (Anthropic, May 2026).*

This section captures Anthropic's own guidance on running Claude Code in large repos and reconciles it with the rest of this document. Most of it **confirms** the architecture proposed in §A and §J; a handful of points add **new mechanisms** (path-scoped Skills, Stop-hook CLAUDE.md updates, MCP-exposed structured search) that ATeam should adopt.

### M.1 The headline claim — and what it means for ATeam's RAG decision

> "Claude Code navigates a codebase the way a software engineer would: it traverses the file system, reads files, uses grep to find exactly what it needs, and follows references across the codebase."
>
> "There's no embedding pipeline or centralized index to maintain as thousands of engineers commit new code."

This is the most load-bearing line in the post for ATeam. It **directly validates** §C.4's verdict that semantic retrieval is over-engineered for ATeam's use case, and validates the whole §A "deterministic facts + targeted tools" stack. Anthropic's own product team does *not* run a vector index against the codebase — they grep, read files, and follow references. ATeam should not build a vector store either; the empirical doc (Feature_TokenReduction.md) reached the same conclusion via measurement, and this is independent confirmation from the vendor.

**The flip side:** the same line explicitly bounds the approach — *"Claude's ability to help in a large codebase is bounded by its ability to find the right context."* Too much loaded context degrades performance; too little leaves it navigating blind. The whole point of §C.1–§C.3 (repo maps, lexical/symbol search, structural graph) is to give the agent **targeted findability**, not pre-loaded context.

### M.2 Hierarchical CLAUDE.md — root for pointers, subdirs for local rules

Anthropic prescribes a layered CLAUDE.md structure:

- **Root CLAUDE.md** — "pointers and critical gotchas only; everything else drifts into noise." High-level overview, one or two paragraphs of survival rules.
- **Subdirectory CLAUDE.md files** — local conventions, test commands scoped to that directory, build commands for that service.
- **Loading model** — "Claude automatically walks up the directory tree and loads every CLAUDE.md file it finds along the way, so root-level context is never lost." Initialize Claude in the subdirectory you're working in, not the repo root.

The cost of getting this wrong is measurable: *"Running the full suite when Claude changed one service causes timeouts and wastes context on irrelevant output."* The recommended fix is per-subdir test commands in the local CLAUDE.md.

**For ATeam.** This maps cleanly onto §L's "docs as build targets" and §C.8's role-specific retrieval. Practical implications:

- The generated knowledge documents from §L should be authored as a **hierarchy of CLAUDE.md files**, not a single monolithic one. ATeam's audit role should produce per-subdir CLAUDE.md updates when it discovers new local conventions or test commands.
- ATeam runs should be **invoked with `cwd` set to the relevant subdirectory** when the task is scoped, not always at repo root. The runner currently defaults to the workdir's repo root — this should become a per-task decision driven by the role and the work-unit scope.
- Per-subdir test commands in CLAUDE.md is exactly the mechanism §L.2.7 (Test/verification command selection) needs. The recommendation lives in `<subdir>/CLAUDE.md`, not in a side-channel database.

### M.3 `.claude/settings.json` for version-controlled exclusions

Anthropic recommends using `.claude/settings.json` with `permissions.deny` rules to version-control exclusions, "ensuring every developer on the team gets the same noise reduction without configuring it themselves."

**For ATeam.** This is the right place to encode "don't read these files" rules (build artifacts, vendored deps, generated code) so every agent invocation honours them. ATeam should:

- Ship a default `.claude/settings.json` template with `permissions.deny` for the obvious noise paths (`node_modules/`, `build/`, `dist/`, `vendor/`, `*.min.js`, large lockfiles).
- Let projects override via their own checked-in `.claude/settings.json`.
- Stop relying on prose instructions in CLAUDE.md to tell agents "don't read X" — make it a permission denial that fails fast.

### M.4 Skills with progressive disclosure and path-scoped activation

> "In a large codebase with dozens of task types, not all expertise needs to be present in every session. Skills solve this through progressive disclosure, offloading specialized workflows and domain knowledge that would otherwise compete for context space."

Skills can also be **scoped to specific paths so they only activate in the relevant part of the codebase**.

**For ATeam — this is the missing primitive in §C.8.** §C.8 prescribes role-specific retrieval policies but treats them as prompt-engineering. Skills are a mechanism: the skill loads only when the path glob matches, the description is short enough to fit in every session's discovery loop, and the body is consulted on demand. The implications:

- ATeam's roles (audit, implement, review, security, deps, docs) should be reified as **path-scoped Skills**, not as monolithic role prompts loaded into every run. The audit Skill activates when the work touches code; the deps Skill activates when the work touches `package.json` / `go.mod` / `requirements.txt`.
- Per-project specializations (e.g. "Django ORM conventions for this repo") become Skills shipped in the repo's `.claude/skills/`, scoped to the relevant paths.
- This is *cheaper* than the role-prompt approach because it doesn't pay the prompt cost for skills that don't apply.

### M.5 Hooks — three concrete patterns

Three hook patterns Anthropic explicitly endorses:

1. **Stop hook → propose CLAUDE.md updates while the context is fresh.** This is the feedback loop §L describes but does not specify a trigger for. The Stop hook is the trigger.
2. **Start hook → load team-specific context dynamically.** Each developer gets the right setup for their module without manual configuration.
3. **Hooks for deterministic checks (linting, formatting).** "Hooks enforce the rules deterministically and produce more consistent results than relying on Claude to remember an instruction." This is §C.6 (Deterministic Analyzers Before LLM Review) but at the hook layer, not the runner layer.

**For ATeam.**

- The **Stop hook → patch the appropriate CLAUDE.md** pattern is the missing piece of §L. ATeam should ship a default Stop hook that runs after every agent session and proposes targeted CLAUDE.md updates based on what the session discovered (new gotchas, surprising file locations, command-not-found-and-then-found pairs). The hook can write to a queue file; a separate ATeam role reviews the queue and merges accepted updates. This closes §L's loop without requiring the agent itself to remember to update the doc.
- The **Start hook → context priming** is a place to inject §C.8's role-specific retrieval. The Start hook can read the work-unit description, decide which role applies, and load the matching context block before the agent's first turn.
- For deterministic checks, the runner-level invocation of analyzers (§C.6) and the hook-level enforcement should both exist — hooks catch within-session violations, runner-level analyzers gate the run.

### M.6 Sub-agents — read-only mapping → file → main agent edits

> "A subagent is an isolated Claude instance with its own context window that takes a task, does the work, and returns only the final result to the parent."
>
> "Spin up a read-only subagent to map a subsystem and write findings to a file, then have the main agent edit with the full picture."

**For ATeam — this is exactly the architecture §C.5 (Evidence-Linked Agent Ledger) prescribes,** with the addition that Anthropic explicitly recommends *file-mediated* handoff between sub-agent and main agent. Two implications:

- ATeam's audit→implement separation should be implemented as **sub-agent → file → main agent**, not as two top-level runs sharing CallDB. The audit sub-agent has its own context window, returns only the findings file, and the implement agent reads the file. Total context the implement agent sees: its own role prompt + the findings file + the code it needs to edit. No bleed-through from the audit agent's exploration noise.
- The findings file is the **evidence-linked observation** of §C.5. Same artifact, just spawned by a sub-agent rather than a peer agent.

This shifts ATeam's architecture in a useful direction: instead of "ateam runs audit then implement as separate top-level invocations," it becomes "ateam runs implement with an audit sub-agent that produces the findings file as its first step." Cheaper, simpler, and matches Anthropic's intended Claude Code shape.

### M.7 LSP integration — symbol-level precision

> "LSP returns only the references that point to the same symbol, so the filtering happens before Claude reads anything."

This addresses the failure mode in §C.2 where grepping a common function name returns "thousands of matches." Anthropic notes one enterprise customer deployed LSP integrations org-wide *before* their Claude Code rollout specifically for C/C++ navigation at scale.

**For ATeam.** §C.2 already recommends LSP-grade lookups; this is independent confirmation that it matters at scale. Concrete plan:

- Install language servers (`gopls`, `pyright`, `typescript-language-server`, etc.) inside the ATeam container image as part of the runtime.
- Expose LSP's `references` / `definition` / `implementations` as **MCP tools** (see §M.8) the agent can call, rather than relying on the agent to know how to drive the LSP protocol directly.
- For Go specifically (ATeam's own codebase), `gopls` is already standard tooling; the missing piece is the MCP wrapper.

### M.8 MCP servers — structured search as a tool

> "MCP servers are how Claude connects to internal tools, data sources, and APIs that it can't otherwise reach."

Sophisticated teams "built MCP servers exposing structured search as a tool Claude can call directly."

**For ATeam.** This is the right delivery mechanism for everything in §C.1–§C.3 and §C.6. Rather than pre-loading repo maps, structural graph results, and analyzer outputs into the prompt, ATeam should:

- Run a **per-run MCP server** inside the container that exposes: repo-map query, symbol lookup, callers/callees, recent-diff impact, test lookup, schema lookup, prior-findings query, analyzer-output query.
- The agent calls these on demand. The context cost is the MCP tool catalog (small, cacheable) plus only the responses to actual queries.
- This is **strictly better than the alternative** of pre-loading the same information, because it shifts cost from "every run pays for the full graph" to "every run pays only for the parts of the graph it actually consults."

### M.9 Maintenance cadence — review every 3-6 months

> "Teams should expect to do a meaningful configuration review every three to six months" as model capabilities evolve.

Rules that once helped may become constraining with newer models. CLAUDE.md content that compensated for an old model's weakness is dead weight against a newer one.

**For ATeam.** Two operational implications:

- Schedule a **quarterly review job** that audits CLAUDE.md files for staleness — rules that haven't been triggered in N runs, advice that contradicts current model behaviour, gotchas that no longer apply.
- Track CallDB metrics that surface "rules whose violation rate has dropped to zero" — these are candidates for removal because the underlying problem is gone (model improved, code changed, convention solidified).

### M.10 What Anthropic does *not* recommend (notable omissions)

- **No vector indexing of the codebase.** Explicit. Reinforces ATeam's decision in §C.4.
- **No "summarize the repo into a giant CLAUDE.md" pattern.** The opposite — root file is pointers and gotchas only, everything else is layered.
- **No "load all CLAUDE.md files at startup" pattern.** It's hierarchical, walked from cwd upward, on demand.
- **No prescribed schema for skills, hooks, or sub-agent communication beyond files.** The file system is the integration substrate; this is exactly the §L principle and the §C.5 ledger principle, restated.

### M.11 Reconciliation — what changes in §A's architecture

Nothing in §A is contradicted. The blog adds these mechanisms and refinements:

| §A element | Update from this blog post |
|---|---|
| Index the repo per commit | Confirmed — but the *delivery* of the index to the agent should be via MCP tools, not pre-loaded context. |
| Give agents tools, not giant context | Confirmed — Anthropic explicitly says no embedding pipeline, no centralized index, agent uses grep + file reads + LSP. |
| Store agent work as evidence-linked observations | Confirmed and **strengthened** — the sub-agent → file → main-agent pattern is exactly this, recommended by Anthropic. |
| Run deterministic analyzers before the LLM | Confirmed — and additionally enforced at the hook layer, not just the runner. |
| Prompt caching + scheduling | Not directly addressed in this post; §C.7 still applies. |

**New elements to add to §A:**

1. **Path-scoped Skills as the unit of role specialization** (replacing monolithic role prompts).
2. **Hierarchical CLAUDE.md** as the canonical knowledge format, with per-subdir files for local commands and conventions.
3. **Stop hook → CLAUDE.md update queue** as the §L feedback loop trigger.
4. **MCP server inside the container** exposing repo-map / symbol / analyzer / ledger queries as agent-callable tools.
5. **Quarterly CLAUDE.md staleness review** as a scheduled ATeam job.

### M.12 ATeam priority recommendations from this post

In rough order of cost-vs-value:

1. **Ship a default `.claude/settings.json` template with `permissions.deny` for obvious noise paths.** Cheap. High value. Stops every agent from reading `node_modules/` etc.
2. **Set ATeam's runner `cwd` to the work-unit's scoped subdirectory when possible**, not always repo root. Walks the CLAUDE.md hierarchy correctly and reduces test-command blast radius.
3. **Implement the sub-agent → file → main-agent pattern for audit → implement.** Replaces the current two-top-level-run pattern with one run that spawns the audit as a sub-agent. Smaller context for the implement agent, same evidence-linked artifact.
4. **Stop hook → queue CLAUDE.md update proposals.** Closes §L's feedback loop without making the agent remember to do it.
5. **Per-subdir CLAUDE.md with scoped test/build commands.** Concrete fix for the "ran the full suite, timed out" failure mode.
6. **Path-scoped Skills for ATeam roles.** Larger refactor — fold the existing role prompts into Skills with path globs.
7. **MCP server for repo-map / symbol / analyzer / ledger queries.** Larger still — the right shape once the §C.1–§C.6 pieces are individually working.
8. **Quarterly CLAUDE.md staleness review** as a scheduled ATeam job. Operational.

Items 1–5 are small enough to implement individually as PRs. Items 6–8 are architectural and belong on the same roadmap as the §A index/ledger/analyzer stack — not separate work, just the right delivery layer for that work.
