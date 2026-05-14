Report: reducing token usage for ateam codebase discovery

Date context: May 14, 2026

Executive recommendation

Do not rely on each agent’s free-form “overview for next run” as the main token-saving mechanism. It can help a little, but it is too lossy, stale, and hard to verify. For ateam, the best path is a shared, commit-scoped context plane that every agent queries before reading code.

The strongest architecture is:

1. Index the repository once per commit: file manifest, symbols, imports, call graph, routes, tests, docs, schema/migrations, CI/config, dependency manifests.
2. Give agents tools, not giant context: search, symbol lookup, callers/callees, recent diff impact, test lookup, schema lookup, prior findings.
3. Store agent work as evidence-linked observations, not prose summaries: what was read, what was concluded, which file spans support it, and when it becomes stale.
4. Use deterministic analyzers before LLMs: Semgrep, CodeQL, Trivy, Gitleaks, jscpd/CPD, SQLFluff, Atlas, SchemaSpy, etc.
5. Use prompt caching and scheduling where a large shared prefix is unavoidable.

For your use case, I would prioritize a stack like:

Git commit SHA
  → deterministic repo facts
  → lexical search index: ripgrep / Zoekt / OpenGrok
  → structural code graph: Codebase-Memory MCP or CodeRLM-style server
  → optional semantic index: Qdrant / Chroma / LlamaIndex CodeSplitter
  → analyzer outputs: SARIF / JSON
  → agent work ledger
  → role-specific agents

The most important shift is this: agents should not “discover the repo” repeatedly; they should ask scoped questions of a shared repo memory and then inspect only the necessary source spans.

⸻

1. Repository maps and compressed context

What it is

A repository map is a compact representation of the codebase: files, important symbols, signatures, relationships, and sometimes selected snippets. This gives agents broad orientation without sending the whole repo.

The strongest existing example is Aider’s repo map. Aider builds a concise map of the whole Git repo containing important files, classes, functions, types, and signatures, then sends that map with each change request. Its docs describe a graph-ranking algorithm that ranks definitions and references by importance and fits the result into a token budget; its default map budget is about 1,000 tokens, expanding when no specific files are already in chat.  ￼

Other useful open-source tools in this category:

Tool    Best use
Repomix Packing a repo into an LLM-friendly artifact, with token counting, include/exclude rules, .gitignore support, and optional Tree-sitter-based compression. Its compression mode preserves signatures, interfaces, types, class structures, and other structural elements while removing many implementation details.  ￼
Code2Prompt Creating structured prompts from a repo with include/exclude globs, templates, Git diff/log extraction, JSON/Markdown/XML formats, and MCP support.  ￼
Gitingest   Quickly turning a Git repo into a text digest suitable for LLMs, with include/exclude controls.  ￼

How it reduces token usage

Repository maps reduce repeated orientation cost. Instead of each agent reading directories, opening files, and reconstructing architecture, they get a compact overview and then request specific source only when needed.

This is especially useful for:

* global architecture review;
* documentation review;
* “where should I look?” reasoning;
* small refactoring suggestions;
* deciding which files to inspect next.

Where it fails

Compressed maps are not enough for:

* security analysis requiring exact control/data flow;
* bug finding requiring implementation details;
* subtle database migration behavior;
* concurrency bugs;
* framework magic;
* generated code or dynamic dispatch.

Repomix-style compression can remove precisely the implementation detail a bug-finding agent needs. Use this as orientation, not evidence.

Recommendation for ateam

Create a per-commit artifact:

.ateam/index/repo-map/{commit}.md

Include:

* file tree, but pruned;
* top-level modules/packages;
* public APIs and exported symbols;
* entrypoints;
* routes/controllers/jobs/commands;
* database migrations and schema entrypoints;
* test layout;
* docs layout;
* CI/automation files;
* dependency manifests;
* generated/vendor/ignored regions;
* “hot files” from recent diffs and prior findings.

Then give each role a filtered repo map. For example:

security-agent:
  include: auth, permissions, input parsing, secrets, external calls, dependency manifests, infra, migrations
docs-agent:
  include: README, docs, public APIs, CLI commands, config schema, examples
database-agent:
  include: migrations, schema files, ORM models, seed data, db tests

Use the map to route the agent, not to let it conclude.

⸻

2. Fast lexical and symbol search

What it is

A lexical search index lets agents search code cheaply instead of reading whole files. The minimum viable version is ripgrep. For repeated unattended runs across larger repos, use a persistent code-search engine.

Good open-source options:

Tool    Notes
ripgrep Excellent local baseline. Fast, simple, no index required.
Zoekt   Indexed code search for Git repositories. It supports substring and regexp search, boolean queries, multiple repositories/branches, ranking signals for code, and API/server modes. Zoekt’s README describes sub-50ms results on large corpuses such as Android and Chromium.  ￼
OpenGrok    Source search and cross-reference engine that understands source trees and SCM history.  ￼
SCIP-compatible indexes Useful where language servers or code intelligence tooling already produce symbol/reference data. SCIP is a language-agnostic protocol for source indexing, go-to-definition, and find-references.  ￼

How it reduces token usage

Agents ask targeted questions:

search("password|secret|token", paths=["src", "config"], ignore=["testdata"])
search("TODO|FIXME|HACK", paths=["src"])
search("CREATE TABLE users")
search("retry", paths=["docs", "src"])
search("parseJwt")

Then they read only the matching spans plus nearby context.

This is a big improvement over:

read src/**
read docs/**
read migrations/**

Best fit for ateam

Lexical search is particularly good for:

* documentation coverage checks;
* public API consistency;
* finding config/env vars;
* security keyword scans;
* error handling patterns;
* route/controller discovery;
* TODO/FIXME/HACK review;
* migration history;
* duplicate naming patterns;
* dependency usage.

Where it fails

Lexical search depends on good queries. It misses semantic equivalents:

authorize, canAccess, permitted, policy.check, guard, ACL

A security agent looking only for auth may miss policy, capability, or framework-specific guards. That is why lexical search should be combined with structural graph tools and semantic search.

Recommendation for ateam

Expose a common tool contract to every agent:

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

Record every search in the agent work ledger. Failed searches are useful too: “security-agent searched for hardcoded secrets in config paths and found none” is a reusable observation when tied to commit SHA and query details.

⸻

3. Persistent structural code graph / code memory

What it is

A structural code graph indexes the repository by symbols, definitions, references, calls, imports, tests, routes, and relationships. Instead of asking an agent to rediscover the codebase, you give it graph queries:

find_symbol("UserService.create")
callers("UserService.create")
callees("UserService.create")
tests_for("UserService.create")
routes_to("UserController.create")
recently_changed_symbols(base, head)
impact_radius(symbol, depth=2)

This is probably the highest-leverage category for ateam.

Promising open-source projects as of May 2026:

Project Why it matters
Codebase-Memory MCP An open-source system that parses many languages with Tree-sitter, stores a structural graph in SQLite, exposes MCP tools, and supports incremental re-indexing with file fingerprints. Its paper reports that graph-based querying used about 10× fewer tokens and 2.1× fewer tool calls than file-exploration baselines in an evaluation, with lower but still high answer quality. Treat that as a benchmark result to validate on your own repos, not a universal guarantee.  ￼
CodeRLM A Rust server that indexes files and symbols with Tree-sitter and exposes JSON APIs for targeted context such as structure, symbols, source, callers, tests, grep, annotations, and exploration history. It explicitly supports annotations visible to other agents in a swarm-like workflow.  ￼
Aider repo-map internals    Not a full persistent graph server, but its Tree-sitter and graph-ranking approach is very relevant for compact codebase discovery.  ￼

MCP is relevant because it gives agents a standard way to access external tools, data, and workflows instead of receiving all context in the prompt.  ￼

How it reduces token usage

A structural graph lets agents expand context by need:

recent bug agent:
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
  inspect routes → handlers → auth checks → DB writes/external calls
  inspect missing guard patterns
  stop

The agent no longer needs to read every file to learn what calls what.

Where it fails

Structural tools can be wrong or incomplete, especially with:

* dynamic languages;
* metaprogramming;
* dependency injection;
* reflection;
* framework conventions;
* generated code;
* SQL embedded in strings;
* cross-service behavior.

So every final finding should cite exact source spans, not just graph edges.

Recommendation for ateam

Make this a core service:

ateam-indexer build --repo . --commit <sha>
ateam-context-server serve --commit <sha>

Minimum graph entities:

File
Symbol
Import
Call
Reference
Route
Command
Job
Migration
Table
Column
Test
ConfigKey
DocPage
Finding
Observation

Minimum graph queries:

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

For recent-change agents, default to diff → changed symbols → impact radius. For whole-codebase agents, default to module graph → hotspots → representative source spans.

⸻

4. Semantic retrieval and hybrid RAG

What it is

Semantic retrieval indexes chunks using embeddings so agents can ask conceptual questions:

"Where do we validate user permissions?"
"How is retry behavior documented?"
"What code handles tenant isolation?"
"Where are background jobs scheduled?"

Open-source building blocks:

Tool    Role
Qdrant  Open-source vector search / semantic search engine.  ￼
Chroma  Open-source retrieval infrastructure for embeddings and metadata-filtered search.  ￼
LlamaIndex CodeSplitter Code-oriented splitting using Tree-sitter and token-based splitting.  ￼

How it reduces token usage

Instead of reading all documentation or all modules, agents retrieve the top relevant chunks and then verify with exact source.

This is useful for:

* docs agents;
* architecture agents;
* onboarding-style questions;
* prior agent reports;
* issue history;
* design docs;
* comments and README content;
* “find concept X even when names differ.”

Where it fails

Pure vector search is often weak for code because exact identifiers matter. It can retrieve conceptually similar but wrong code. For codebases, use hybrid retrieval:

hybrid_score =
  lexical/BM25 score
  + vector similarity
  + path relevance
  + symbol graph proximity
  + recency/change relevance
  + prior finding relevance

Do not let vector retrieval be the only path to evidence.

Recommendation for ateam

Use semantic retrieval mainly for:

* docs;
* comments;
* architecture notes;
* previous agent observations;
* issue/PR text;
* tests with descriptive names;
* natural-language descriptions around schema/business logic.

Chunk by AST or document section, not arbitrary token windows. Attach metadata:

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

For code retrieval, require a second step:

semantic hit → exact source read → final conclusion

⸻

5. Indexing agent work: yes, but not as plain summaries

What it is

You asked whether later agents should see what earlier agents looked at. Yes — but the format matters.

A free-form overview like this is weak:

"The auth code generally lives in src/auth and seems okay."

A reusable observation looks like this:

{
  "commit": "abc123",
  "role": "security",
  "claim": "User deletion route checks admin permission before deleting records.",
  "evidence": [
    {
      "path": "src/routes/users.ts",
      "span": [88, 104],
      "blob_sha": "..."
    },
    {
      "path": "src/auth/policies.ts",
      "span": [31, 52],
      "blob_sha": "..."
    }
  ],
  "confidence": "medium",
  "invalid_if": [
    "src/routes/users.ts blob changes",
    "src/auth/policies.ts blob changes"
  ],
  "queries_tried": [
    "delete user",
    "requireAdmin",
    "UserPolicy"
  ],
  "created_by_run": "security-2026-05-14T..."
}

Why summaries alone do not help enough

Plain summaries tend to be:

* stale after code changes;
* ungrounded;
* too general;
* contaminated by agent mistakes;
* hard to retrieve precisely;
* hard to invalidate;
* hard to deduplicate.

They are useful only when converted into evidence-linked, commit-scoped facts.

Recommended ateam ledger schema

Table / collection  Purpose
agent_run   Role, model, prompt hash, commit SHA, token usage, cache usage, duration, status.
read_event  Every file/span read by an agent, with reason and token count.
search_event    Queries tried, filters, result counts, selected results.
observation Evidence-linked claim about code/docs/schema/security.
finding User-facing issue with severity, category, evidence, repro, suggested fix, status.
coverage    Which files/symbols/categories were inspected by which role.
negative_result “Looked for X and did not find it,” tied to queries and scope.
invalidation_key    Blob hashes, symbol hashes, tool versions, config versions that determine staleness.

How later agents should use it

Before reading files, each agent should ask:

prior_observations(role="security", path="src/routes/users.ts", commit="<sha>")
prior_findings(symbol="UserService.delete", status=["open", "fixed", "stale"])
coverage(role="database", table="users")
negative_results(query_family="hardcoded secrets", scope="config")

The agent should receive a compact result:

Prior relevant work:
1. security-agent inspected src/routes/users.ts lines 80-130 at commit abc123.
   It found admin checks before deletion. Evidence: ...
   Stale? no.
2. docs-agent noted DELETE /users/:id is undocumented.
   Evidence: route exists; docs omit it.
   Stale? yes, docs changed since observation.

How it reduces token usage

This prevents repeated exploration. More importantly, it prevents repeated failed exploration, which is where many unattended agents waste tokens:

Agent A searches for schema docs and finds none.
Agent B later repeats the same search.
Agent C later repeats it again.

A ledger lets agents reuse “already searched” knowledge while still verifying when needed.

Recommendation for ateam

Replace “write an overview for your next run” with a required structured output:

{
  "read_events": [],
  "observations": [],
  "findings": [],
  "negative_results": [],
  "recommended_next_queries": [],
  "staleness_keys": []
}

Keep the prose report for humans, but make the structured ledger the thing other agents consume.

⸻

6. Specialized analyzers before LLM review

What it is

Many dimensions of your reports do not require an LLM to discover the first layer of evidence. Run deterministic tools first, then ask the LLM to triage, explain, correlate, and prioritize.

Useful open-source tools:

Dimension   Tools
Security static analysis    Semgrep Community Edition, CodeQL
Dependency/container/config/security    Trivy
Secret scanning Gitleaks, Trivy secret scan
Duplication/refactoring jscpd, PMD CPD
SQL/database    SQLFluff, Atlas migrate lint, SchemaSpy
Docs/style  markdownlint, Vale, link checkers
CI/automation   actionlint, shellcheck, hadolint, language-specific linters

Semgrep CE is an open-source SAST engine for finding insecure coding patterns and vulnerabilities; CodeQL supports semantic code analysis and is free for open-source and research use; Trivy scans filesystems and other targets for vulnerabilities, misconfigurations, secrets, and licenses; Gitleaks detects secrets in Git repos, files, and stdin.  ￼

For duplication and refactoring, jscpd supports many languages and has an AI-oriented reporter, while PMD CPD is a long-standing copy/paste detector for duplicate code.  ￼

For database-oriented agents, SchemaSpy generates HTML database documentation and ER diagrams, Atlas can analyze schema migrations for dangerous or breaking changes, and SQLFluff is an open-source SQL linter with multiple dialects and autofix support.  ￼

How it reduces token usage

Instead of asking an LLM:

Read the repo and find security issues.

Do:

Run Semgrep/CodeQL/Trivy/Gitleaks.
Give the agent only:
  - high-confidence findings
  - affected spans
  - rule metadata
  - surrounding source
  - related call graph
Ask it to triage and deduplicate.

This turns broad discovery into a compact evidence review.

Where it fails

Static tools produce false positives and miss business-logic bugs. LLMs are useful after the tools run:

* Is this reachable?
* Is this already guarded?
* Is this exploitable in this app?
* Is this duplicate of another finding?
* What is the minimal fix?
* Does the documentation need updating?

Recommendation for ateam

Make analyzers first-class data sources:

.ateam/artifacts/{commit}/semgrep.sarif
.ateam/artifacts/{commit}/codeql.sarif
.ateam/artifacts/{commit}/trivy.json
.ateam/artifacts/{commit}/gitleaks.json
.ateam/artifacts/{commit}/jscpd.json
.ateam/artifacts/{commit}/sqlfluff.json
.ateam/artifacts/{commit}/schema.json

Then index those artifacts into the same ledger and graph.

⸻

7. Prompt caching and run scheduling

What it is

Prompt caching reduces cost and latency when multiple requests share a large identical prefix. This matters for ateam because many agents may share the same system instructions, tool definitions, repo map, style guide, policy rules, and index summaries.

OpenAI’s prompt caching documentation says cache hits reduce latency and cost, with caching available for repeated prompt prefixes and best practices such as placing static repeated content at the beginning. It also documents minimum prefix sizes and cache retention behavior.  ￼

Anthropic’s docs similarly describe caching prompt prefixes, tools, system messages, and messages, with static content placed earlier in the prompt. Gemini’s docs describe implicit caching for Gemini 2.5 models and explicit caching options, with the common-prefix pattern also recommended.  ￼

OpenAI’s current pricing page shows cached input pricing materially lower than normal input pricing for current GPT-5-family models; for example, GPT-5.5 lists input at $5.00 per 1M tokens and cached input at $0.50 per 1M tokens.  ￼

How it reduces cost

Put stable content first:

[system instructions]
[shared ateam tool contract]
[repo policy]
[repo map for commit abc123]
[static analyzer summary]
[role-specific instructions]
[dynamic question]

Avoid:

[role-specific intro]
[random run id]
[dynamic date text]
[agent-specific scratchpad]
[repo map]

Small differences before the large shared block can destroy cache reuse.

Parallel vs sequential agents

Parallel agents are good for wall-clock time, but they can waste tokens if each has a different prompt prefix. A better pattern is:

Stage 1: build shared index once
Stage 2: create shared cached prefix
Stage 3: fan out role agents with identical prefix and role-specific suffix
Stage 4: write findings/observations to ledger
Stage 5: optional synthesis agent reads compact outputs only

Where providers support cache retention windows, schedule agents for the same repo/commit close together. But do not rely on caching as the only solution: it reduces cost/latency, not necessarily the model’s need for good context.

Recommendation for ateam

Track:

input_tokens
cached_input_tokens
output_tokens
tool_call_count
source_tokens_read
tokens_per_confirmed_finding

Then compare:

parallel no-cache
parallel shared-prefix
sequential shared-prefix
graph-tools + shared-prefix

Prompt caching is especially useful for stable context such as repo maps, tool schemas, rubric definitions, and project policies. It is less useful for unique source snippets that change per agent.

⸻

8. Role-specific retrieval policies

What it is

Different agents should not discover context the same way. A recent-bugs agent, security agent, docs agent, and architecture agent need different retrieval contracts.

Recommended policies

Agent role  Start context   Expansion rule  Stop condition
Recent bug finder   Git diff, changed symbols, changed tests, dependency impact Callers/callees radius 1–2, related tests, recent issues, touched configs   Enough evidence for likely regression or no-risk conclusion
Security    Routes, auth boundaries, input parsers, external calls, secrets/config, analyzer findings   Trace source → validation → authorization → sink    Evidence-backed finding or scoped negative result
Architecture    Repo map, module graph, dependency cycles, high fan-in/out files, duplicate abstractions    Sample representative files, inspect edges/cycles   Pattern-level finding with examples
Refactoring Duplication reports, complexity metrics, hotspots, repeated patterns    Inspect representative duplicate groups Concrete small refactor proposal
Documentation   Public APIs, CLI commands, config schema, routes, examples, docs index  Compare code surface to docs surface    Missing/stale/unclear docs finding
Automation/CI   CI files, scripts, package manifests, test commands, release config Trace scripts to commands/tools Broken/missing automation finding
Database/schema Schema, migrations, ORM models, queries, db tests, Atlas/SQLFluff output    Table → model → migration → query → test    Migration/schema risk or doc gap

How it reduces token usage

Agents should be prohibited from broad reads unless their policy allows it. For example:

security-agent may not read all src/**
security-agent must first inspect route/auth/analyzer indexes
security-agent may expand only through graph edges or search hits

This turns “unattended agent exploration” into bounded investigation.

Recommendation for ateam

Give every role a retrieval budget:

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
    "callers",
    "callees",
    "search",
    "tests_for",
    "config_keys"
  ]
}

Agents can request budget increases, but the request itself should be logged and justified.

⸻

9. Commercial options worth knowing about

Open source should cover most of ateam’s infrastructure, but a few commercial products are ahead in polished context systems.

Product Why mention it  Small-team pricing signal
Augment Code    Strong commercial “context engine” positioning, coding agent, MCP/native tools, and small-team plans. Its pricing page lists an Indie plan at $20/month and a Standard plan at $60/developer/month.  ￼  Worth evaluating for solo/small teams if you want a polished coding-agent context layer.
Cursor  Strong IDE agent workflow and shared team context features. Its pricing page lists Individual at $20/month and Teams at $40/user/month.  ￼  Good for human-in-the-loop coding, less directly a backend for unattended ateam.
Qodo    Focused on code review, PR review, and context-aware review workflows, with a free developer tier.  ￼   Useful if one ateam dimension is PR/review quality.
Sourcegraph Very strong code search/code intelligence, MCP server/API/CLI, semantic/natural-language search, and multi-repo scale. Its current pricing page lists Enterprise starting at $16K, so it may be too expensive for many solo developers.  ￼  Best when the codebase/multi-repo scale justifies it.

For ateam, I would not start by buying a commercial tool. I would first build the open-source context plane. Then use commercial products as benchmarks: “Can our open-source stack answer the same context questions with comparable token use?”

⸻

10. Concrete architecture for ateam

Index build step

Run once per repo/commit:

ateam index build \
  --repo . \
  --commit "$(git rev-parse HEAD)" \
  --base "$(git merge-base main HEAD)"

Produce:

.ateam/index/
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

Agent run flow

1. Load role policy.
2. Query prior observations and findings.
3. Query repo map / graph / search / analyzers.
4. Read exact source spans only when needed.
5. Produce evidence-backed findings.
6. Write read events, observations, negative results, and findings.
7. Mark stale prior observations if touched files changed.

Example: recent-bug agent

Input:
  base commit, head commit
Steps:
  1. changed_files(base, head)
  2. changed_symbols(base, head)
  3. tests_for(changed_symbols)
  4. callers/callees radius 1
  5. recent prior findings touching same symbols
  6. inspect exact source spans
  7. produce findings or scoped negative results

Example: documentation agent

Input:
  repo@commit
Steps:
  1. public API/CLI/route/config index
  2. docs index
  3. compare surfaces
  4. inspect mismatches
  5. produce missing/stale docs findings

Example: security agent

Input:
  repo@commit
Steps:
  1. analyzer findings: Semgrep/CodeQL/Trivy/Gitleaks
  2. routes and external inputs
  3. auth/permission symbols
  4. sensitive sinks: DB writes, filesystem, subprocess, network, deserialization
  5. inspect only relevant traces
  6. produce triaged findings

⸻

11. Evaluation plan: prove what actually saves tokens

You are right to be unsure whether agent-written overviews help. Measure it.

Metrics to track

Metric  Why
input_tokens_total  Overall cost driver.
cached_input_tokens Whether prompt caching is working.
source_tokens_read  How much code the agent actually consumed.
files_read_count    Whether agents are over-reading.
tool_calls_count    Whether retrieval is efficient.
tokens_per_confirmed_finding    Best cost-quality metric.
duplicate_findings_rate Whether agents are repeating each other.
stale_observation_rate  Whether memory invalidation works.
false_positive_rate Whether compressed context is harming quality.
missed_regression_rate  Whether retrieval is too narrow.

A/B tests

Run the same set of agents on the same commits:

A. baseline: current agent behavior
B. baseline + prose overview
C. repo map only
D. repo map + lexical search
E. repo map + structural graph
F. graph + analyzer outputs
G. graph + analyzer outputs + agent ledger
H. G + prompt caching/scheduled fan-out

Compare:

cost
latency
unique useful findings
duplicate findings
false positives
missed known issues
human review time

My expectation: prose overviews alone will show modest benefit; graph/search/analyzer/ledger will show much larger benefit.

⸻

12. Practical priority order

Phase 1: instrumentation and repo facts

Add logging first:

agent_run
read_event
search_event
token_usage
finding

Without this, you cannot know what helps.

Phase 2: repo map + search

Add:

repo-map.md
ripgrep or Zoekt
role-specific include/exclude rules

This is the fastest win.

Phase 3: structural graph

Pilot either Codebase-Memory MCP or a CodeRLM-like server. Make agents ask graph questions before reading source.

Phase 4: analyzer integration

Run Semgrep, CodeQL where applicable, Trivy, Gitleaks, jscpd/CPD, SQLFluff, Atlas/SchemaSpy depending on repo type.

Phase 5: agent work ledger

Convert summaries into structured, evidence-linked, invalidatable observations.

Phase 6: prompt caching and scheduling

Stabilize shared prefixes and fan out agents with identical cached context.

⸻

Final recommendation

Yes, you should index agent work — but not as ordinary summaries. Index it as commit-scoped, evidence-linked observations plus read/search history.

The best overall strategy for ateam is:

Do not make every agent reread the repo.
Make every agent query a shared context system.
Make every conclusion cite exact evidence.
Make every observation invalidatable by file/symbol hash.
Use deterministic tools to produce compact evidence.
Use prompt caching for the unavoidable shared prefix.

A strong first implementation would be:

repo map
+ ripgrep/Zoekt search
+ Codebase-Memory or CodeRLM-style structural graph
+ SARIF/JSON analyzer outputs
+ SQLite ledger of agent reads, observations, findings, and invalidation keys

That should reduce token usage much more reliably than asking agents to write better overviews, while also improving deduplication, auditability, and report quality.