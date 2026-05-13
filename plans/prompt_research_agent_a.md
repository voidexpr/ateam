# Survey: Claude Code Skills & Prompt-Reuse Systems for Code Quality

> Scope: skills, subagents, plugins, and slash-command libraries that improve
> code produced by agents — refactoring, bug-finding, security, tests, E2E,
> Playwright, and general "make the agent less sloppy."
>
> Ranking signals (subjective): **(1)** reputation/popularity, **(2)** known
> quality of usage, **(3)** how well it actually does the job. ⭐ count is a
> rough proxy — many "awesome-*" repos have inflated stars from list inclusion,
> so where I have firsthand or expert-vetted signal I weight that higher.

---

## 0. Cross-cutting frameworks (worth knowing before you pick anything else)

These dominate the ecosystem and most per-category picks below either come
from them or compete with them.

| Project | What it is | Honest take |
|---|---|---|
| **obra/superpowers** ([GitHub](https://github.com/obra/superpowers)) | Jesse Vincent's opinionated skills framework: brainstorming → write-plan → execute-plan with TDD, systematic-debugging, subagent-driven-development, verification-before-completion. Multi-host (Claude Code, Codex, Gemini CLI, Cursor, Copilot CLI). ~174k ⭐ in 7 months. | Best-in-class for *enforcing discipline*. Each skill opens with an "Iron Law" + red-flags table of common AI rationalizations. Weakness: opinionated; you adopt the whole methodology or fight it. The TDD and debugging skills are the strongest individual artifacts in the ecosystem. |
| **wshobson/agents** ([GitHub](https://github.com/wshobson/agents)) | 185 subagents + 153 skills + 80 plugins + 100 slash commands. Sonnet/Haiku tier orchestration baked in. | Broadest "à la carte" library. Quality varies per agent; the orchestration patterns (`/full-stack-orchestration`, `/security-scanning`) are well thought out. Good if you want a marketplace, not a methodology. |
| **trailofbits/skills** ([GitHub](https://github.com/trailofbits/skills)) | ~40 skills from the security firm: static-analysis, semgrep-rule-creator, insecure-defaults, supply-chain-risk-auditor, mutation-testing, property-based-testing, second-opinion, fp-check. CC BY-SA 4.0. | Highest *expert-vetted quality density* I found. Skills authored by actual auditors. Also publish `trailofbits/skills-curated` — third-party marketplaces they've code-reviewed (a useful signal in a backdoor-prone ecosystem). |
| **anthropics/skills** ([GitHub](https://github.com/anthropics/skills)) | Official skills: docx, pdf, pptx, xlsx, webapp-testing, skill-creator, frontend-design, etc. ~132k ⭐. | Reference implementation. `webapp-testing` and `skill-creator` are directly relevant; the rest are document-domain. Conservative, well-tested. |
| **VoltAgent/awesome-claude-code-subagents** ([GitHub](https://github.com/VoltAgent/awesome-claude-code-subagents)) | 100+ subagents organized by category. ~83k ⭐. | Catalog, not a methodology. Useful for plucking specific role prompts (code-reviewer, security-auditor, test-automator). Quality is uneven — read before adopting. |
| **SuperClaude_Framework** ([GitHub](https://github.com/SuperClaude-Org/SuperClaude_Framework)) | 30 slash commands + 9 personas (architect, security, performance, QA…) | More "context engineering" than skills. Useful patterns to study; less production-ready than Superpowers. |

---

## 1. Code refactoring (avoid duplicate code, future-proof)

### Top 3

1. **`citypaul/.dotfiles` → `refactoring` skill** — [link](https://github.com/citypaul/.dotfiles/blob/main/claude/.claude/skills/refactoring/SKILL.md)
   - **Strength**: TDD-aware. Explicit "DRY = knowledge, not code" principle. Refuses to extract for testability alone. Has a baked-in "When NOT to refactor" gate that prevents speculative restructuring. Pairs with mutation testing.
   - **Weakness**: Tightly coupled to a TDD workflow. Single-author dotfiles repo, not a marketplace plugin.
   - **Rank**: high quality, low fame.

2. **`obra/superpowers` (refactoring posture inside the framework)** — [link](https://github.com/obra/superpowers)
   - **Strength**: Refactoring is treated as a *post-mutation-testing* step. Forces YAGNI. The `subagent-driven-development` skill has a code-quality review pass that catches DRY violations on the second agent.
   - **Weakness**: Not a standalone refactoring skill — you adopt the whole methodology.

3. **`l-mb/python-refactoring-skills`** — [link](https://github.com/l-mb/python-refactoring-skills)
   - **Strength**: Eight focused skills (`py-refactor`, `py-complexity`, `py-code-health`, `py-modernize`, `py-quality-setup`, `py-git-hooks`, etc.). Wires up real Python tooling (radon, vulture, pylint, lizard, mutmut, ruff, basedpyright). Good for Bob-style Python projects.
   - **Weakness**: Python only. Some skills marked WIP.

### Also reviewed
- **`WomenDefiningAI/claude-code-skills/code-refactoring`** ([link](https://github.com/WomenDefiningAI/claude-code-skills/tree/main/skills/code-refactoring)) — proactive file-size watcher that triggers *before* edits. Clever but JS/Python only, and the "monitor before editing" mode requires a Node daemon. **+** novel proactive design **−** noisy in practice, dependency-heavy.
- **`Effeilo/claude-code-frontend-skills`** ([link](https://github.com/Effeilo/claude-code-frontend-skills)) — `/front-refactor` with preview/apply modes. **+** safe two-step UX, frontend-aware (JS/TS/React/Vue/Svelte/Astro/CSS) **−** frontend only, ignores backend/Go.
- **`alirezarezvani/claude-skills` engineering plugins** ([link](https://github.com/alirezarezvani/claude-skills)) — `ln-623` DRY/KISS/YAGNI auditor, `ln-626` dead-code finder, `ln-640` architectural pattern audit. **+** dedicated AI-tech-debt detectors **−** repo has 200+ skills of mixed quality, ranking is hard.
- **`wshobson/agents` → `refactoring-expert` agent** + `comprehensive-review` plugin — solid generic refactoring agent, mostly prose checklist. Useful as a starting point.

---

## 2. Find code logic bugs

### Top 3

1. **`obra/superpowers` → `systematic-debugging` skill** — [link](https://github.com/obra/superpowers)
   - **Strength**: 4-phase root-cause process (reproduce → isolate → hypothesize → verify) that *forbids* fixing what you haven't understood. Includes `root-cause-tracing` and `defense-in-depth` sub-skills. Architectural review trigger after 3 failed fix attempts (a great escape valve from doom loops).
   - **Weakness**: Slower than "just try a fix." That's the point, but agents often want to skip it.

2. **`anthropics/claude-code-security-review` → `/security-review`** — [link](https://github.com/anthropics/claude-code-security-review)
   - **Strength**: Official, ships as a built-in slash command in Claude Code. False-positive filtering pipeline is a separate stage (custom filter instructions supported). GitHub Action variant for PRs.
   - **Weakness**: Marketed as "security" but catches plenty of logic/correctness bugs. Not hardened against prompt injection — Anthropic explicitly says "only trusted PRs."

3. **`codexstar69/bug-hunter`** — [link](https://github.com/codexstar69/bug-hunter)
   - **Strength**: Adversarial multi-agent pipeline: Triage → Recon → Hunter → **Skeptic** → Referee → Fix Plan → Fixer → Verify. The Skeptic stage explicitly tries to disprove each finding with counter-evidence — exactly the "reduce false positives" pattern that's missing from most bug-finding prompts.
   - **Weakness**: Young project, modest stars; adversarial loops are token-hungry.

### Also reviewed
- **Trail of Bits `fp-check` skill** ([link](https://github.com/trailofbits/skills/tree/main/plugins)) — purely a false-positive verification gate to bolt onto *any* bug-finding skill. **+** addresses the #1 problem with LLM bug-finders **−** narrow.
- **Trail of Bits `differential-review`** — diff-aware review; less noisy on PRs.
- **Anthropic Code Review (research preview, Team/Enterprise)** ([docs](https://code.claude.com/docs/en/code-review)) — multi-agent PR review with `CLAUDE.md` and `REVIEW.md` tuning. Not OSS, but the docs are worth reading.
- **`shuvonsec/claude-bug-bounty`** ([link](https://github.com/shuvonsec/claude-bug-bounty)) — bug *bounty* focused (web vulns, not general logic bugs). Useful inspiration; not what you want for general code review.
- **mcpmarket.com "Bug Detection Review" / "Bug Detector"** — pure SEO-listing pages with little provenance. Skip.
- **`testers.ai` OpenTestAI** — 31 testing-agent profiles, ships as a single SKILL.md. Interesting but commercial-leaning.

---

## 3. Security audit

### Top 3

1. **`trailofbits/skills`** — [link](https://github.com/trailofbits/skills)
   - **Strength**: Authored by professional auditors. `static-analysis` (CodeQL + Semgrep + SARIF), `insecure-defaults`, `semgrep-rule-creator`, `supply-chain-risk-auditor`, `entry-point-analyzer`, `sharp-edges` (footgun APIs). Documented production bugs found (e.g. timing side-channel in ML-DSA). The companion `trailofbits/skills-curated` lists *other* marketplaces they've code-reviewed — uniquely valuable signal.
   - **Weakness**: Heavy security-research lean; some skills are smart-contract specific. Less suited to generic webapp audit.
   - **Verdict**: Highest signal-to-noise of anything in this survey.

2. **`anthropics/claude-code-security-review`** — [link](https://github.com/anthropics/claude-code-security-review)
   - **Strength**: Official. Already in Claude Code as `/security-review`. Production-tested at Anthropic. Custom prompt files for org-specific rules. GitHub Action for PRs.
   - **Weakness**: Single-pass; less depth than a Trail-of-Bits-style pipeline.

3. **`Security-Phoenix-demo/security-skills-claude-code`** — [link](https://github.com/Security-Phoenix-demo/security-skills-claude-code)
   - **Strength**: AppSec automation kit — 4 slash commands + 4 hooks (SessionStart, PreToolUse on Bash blocks malicious packages, PostToolUse pattern scan on writes, SessionEnd reminder). Multi-language (Python/JS/TS/Go/Java/Rust/Ruby/.NET).
   - **Weakness**: Vendor-aligned (Phoenix Security). Many hooks; integration friction.

### Also reviewed
- **`AgriciDaniel/claude-cybersecurity`** ([link](https://github.com/AgriciDaniel/claude-cybersecurity)) — 8 parallel specialist agents (vulns, authz, secrets, supply chain, IaC, threat-intel, AI-code patterns, business logic). OWASP 2025 + CWE Top 25 + MITRE ATT&CK references. **+** strong taxonomy, parallel execution **−** one-author project, less battle-tested.
- **`netresearch/security-audit-skill`** ([link](https://github.com/netresearch/security-audit-skill)) — 80+ automated PHP/OWASP checkpoints with reference docs (XXE, deserialization, JWT, file uploads). **+** deep on PHP **−** PHP-only.
- **`wrsmith108/claude-skill-security-auditor`** ([link](https://github.com/wrsmith108/claude-skill-security-auditor)) — wraps `npm audit` with structured remediation + risk-exception file. **+** simple, CI-friendly **−** thin (just `npm audit` parsing).
- **`Eyadkelleh/awesome-claude-skills-security`** ([link](https://github.com/Eyadkelleh/awesome-claude-skills-security)) — SecLists payloads + LLM testing. Pentesting/CTF angle, not codebase audit.
- **`tdccccc/claude-security-audit`** ([link](https://github.com/tdccccc/claude-security-audit)) — narrowly audits *Claude Code's own config* (hooks, MCP servers) for malicious entries. Relevant for ATeam's container hardening.

---

## 4. Better unit / functional tests

### Top 3

1. **`obra/superpowers` → `test-driven-development`** — [link](https://github.com/obra/superpowers)
   - **Strength**: Enforces strict red-green-refactor. The "Iron Laws" treat "I'll write the test after" as grounds to delete the implementation. Red-flag list explicitly catches tests that pass on first run, deleted-code-as-reference, and "just this once." Pairs with `subagent-driven-development` so the implementer doesn't see the tests being written (prevents overfitting).
   - **Weakness**: Mandates a workflow many devs find slow.

2. **`wshobson/agents` → `test-automator` + `/unit-testing:test-generate`** — [link](https://github.com/wshobson/agents)
   - **Strength**: Comprehensive prompts for happy-path + edge + error + boundary cases, AAA structure, coverage thresholds, typed mocks. Multi-framework.
   - **Weakness**: Generic; will produce serviceable but not great tests without project-specific tuning.

3. **`trailofbits/skills` → `property-based-testing` + `mutation-testing`** — [link](https://github.com/trailofbits/skills/tree/main/plugins)
   - **Strength**: This is where serious test-quality lives. `property-based-testing` for Hypothesis/proptest-style invariants; `mutation-testing` (mewt/muton) to *measure* whether your tests actually catch bugs. The combination is the right answer to "are my tests any good."
   - **Weakness**: Mutation testing campaigns can take hours; this skill helps configure them, but it's not magic.

### Also reviewed
- **`alirezarezvani/claude-code-skill-factory/tdd-guide`** ([link](https://github.com/alirezarezvani/claude-code-skill-factory/blob/dev/generated-skills/tdd-guide/SKILL.md)) — multi-framework TDD skill with coverage analysis and missing-scenario suggestions. Decent reference; quality typical of the broader factory.
- **alexop.dev custom TDD workflow** ([blog post](https://alexop.dev/posts/custom-tdd-workflow-claude-code-vue/)) — not a skill repo but a clean tutorial on using a Skill to orchestrate a `tdd-test-writer` subagent and a separate `tdd-implementer` subagent so the implementer can't see the test rationale (anti-overfitting). Worth reading if Bob designs his own.
- **`dev.to` "How we use Claude Agents to automate test coverage"** ([article](https://dev.to/melnikkk/how-we-use-claude-agents-to-automate-test-coverage-3bfa)) — published prompt for a test-coverage agent with TypeScript focus; useful raw text for cribbing.
- **VoltAgent `test-automator.md`** — standard catalog entry; nothing surprising.

---

## 5. Better end-to-end tests

### Top 3

1. **`anthropics/skills/webapp-testing`** — [link](https://github.com/anthropics/skills/tree/main/skills/webapp-testing)
   - **Strength**: Official. Python + Playwright. Includes a `with_server.py` helper that handles backend+frontend process lifecycle for integration tests (important for sandboxed agents). Clear decision tree (static HTML → direct selectors; dynamic → reconnaissance-then-action). Scripts designed to be called black-box without polluting context.
   - **Weakness**: Python Playwright only (no TS Playwright); minimal scaffolding.

2. **Playwright Agents (planner / generator / healer)** — [Shipyard guide](https://shipyard.build/blog/playwright-agents-claude-code/), [Playwright docs](https://playwright.dev/)
   - **Strength**: Official Playwright project ships three Claude Code subagents: **planner** explores the app and writes a Markdown test plan, **generator** turns the plan into tests, **healer** fixes broken tests after feature changes. Agents communicate via structured artifacts. Highly polished.
   - **Weakness**: TypeScript/JS Playwright only. Requires Playwright MCP.

3. **`testdino/playwright-skill`** — [link](https://testdino.com/blog/playwright-skill-claude-code) (open source)
   - **Strength**: 70+ guides organized around "10 Golden Rules." Both TS and JS examples. Sections for CI, POM, migration from Cypress/Selenium. Acts as a knowledge base the agent reads on demand.
   - **Weakness**: Vendor-aligned (TestDino reporting), but the Skill itself is OSS.

### Also reviewed
- **`alirezarezvani/claude-skills` → `playwright-pro`** — generic Playwright skill; serviceable.
- **`lackeyjb/playwright-skill`** ([link](https://github.com/lackeyjb/playwright-skill)) — Claude writes *custom* Playwright on the fly for one-off automation. **+** good for ad-hoc exploration, screenshots, smoke checks **−** not a test-suite generator.
- **MindStudio Playwright MCP guide** ([article](https://www.mindstudio.ai/blog/automate-browser-tasks-claude-code-playwright)) — pattern reference, not a skill.

---

## 6. Better Playwright / web automation tests

Substantial overlap with §5. The picks diverge slightly:

### Top 3

1. **Playwright official MCP + Agents** — [docs](https://playwright.dev/), [plugin page](https://claude.com/plugins/playwright)
   - **Strength**: Microsoft-maintained MCP server giving structured DOM/network/console access. Drop-in for Claude Code/Cursor/VS Code. Agents are first-party.
   - **Weakness**: Heavyweight; not all sandbox setups support MCP.

2. **`anthropics/skills/webapp-testing`** — see §5.

3. **`lackeyjb/playwright-skill`** — [link](https://github.com/lackeyjb/playwright-skill)
   - **Strength**: "Model-invoked" — Claude writes Playwright code on demand for whatever you describe. Visible browser by default. Useful when you want exploration, not a test suite.
   - **Weakness**: Doesn't produce maintainable test files by default.

### Also reviewed
- **Chrome Relay** (mentioned in ComposioHQ list) — drives the *user's* already-open Chrome session (cookies, SSO, extensions). Useful for authenticated test flows that Playwright can't easily replicate. Niche but interesting.
- **`testomat.io` Playwright MCP guide** — solid tutorial on Playwright MCP + Claude Code patterns.

---

## 7. General "improve code from agents" / less mistakes on future features

This is where the cross-cutting frameworks dominate, plus a few unique tools:

### Top 3

1. **`obra/superpowers`** ([link](https://github.com/obra/superpowers))
   - The most credible answer. Forces brainstorming → planning → subagent-driven implementation with a fresh-eyes review pass. The session-start hook injects a short bootstrap doc telling the agent to invoke a relevant skill *before doing anything else*. Directly applicable to ATeam-style orchestration.

2. **`trailofbits/skills` → `second-opinion`** ([link](https://github.com/trailofbits/skills))
   - Runs code reviews via *external* LLM CLIs (Codex, Gemini) on your changes. Effectively a "second auditor" pattern — defends against single-model blind spots. Relevant for ATeam's multi-pass design.
   - Plus `skill-improver` for iterating on your own skills.

3. **`anthropics/skill-creator`** ([link](https://github.com/anthropics/skills/blob/main/skills/skill-creator/SKILL.md))
   - The official "how to write skills that actually trigger" guide. The TDD-for-skills loop (write pressure-test scenarios with subagents → write skill → measure compliance → refactor) is the right meta-process. Bob has already built skills; this is the doc that calibrates against current Anthropic guidance.

### Also reviewed
- **`affaan-m/everything-claude-code`** ([link](https://github.com/affaan-m/everything-claude-code)) — 48 subagents + 182 skills + 68 commands + hooks. Heavy. Includes interesting context-management rules (e.g. "avoid last 20% of context window for large refactors"). Useful as a reference for what an opinionated harness looks like.
- **`SuperClaude_Framework`** ([link](https://github.com/SuperClaude-Org/SuperClaude_Framework)) — 30 slash commands, 9 personas, MCP routing. More about context engineering than skills. Worth skimming for the persona-flag pattern.
- **`FlorianBruniaux/claude-code-ultimate-guide` audit-prompt** ([link](https://github.com/FlorianBruniaux/claude-code-ultimate-guide/blob/main/tools/audit-prompt.md)) — self-audit prompt that grades your Claude Code config across 8 weighted dimensions. Meta but useful once a quarter.
- **`wshobson/agents` → `code-reviewer` + `architect-reviewer`** — solid generic review subagents.
- **`vijaythecoder/awesome-claude-agents/code-reviewer.md`** ([link](https://github.com/vijaythecoder/awesome-claude-agents/blob/main/agents/core/code-reviewer.md)) — well-structured review prompt with severity-tagged report template; delegates to specialist sub-agents.
- **`alirezarezvani/claude-skills`** (engineering pods) — claims 5,200 ⭐. Lots of skills. Quality is mixed; treat as an idea source, not a curated library.

---

## Honest caveats / quality flags

- **Star counts mislead.** "Awesome-" repos rack up stars from list inclusion. Cross-check by reading the actual SKILL.md before adopting.
- **Backdoor risk is real.** Trail of Bits explicitly notes: "Published skills have been found with backdoors and malicious hooks." For ATeam (running these in containers), audit any third-party skill before mounting it. The `tdccccc/claude-security-audit` skill specifically scans for this.
- **Built-in beats third-party for simple cases.** Claude Code now ships `/security-review`, `/debug`, `/simplify`, `/batch`, `/loop`, `/claude-api` as bundled prompt-based skills. Don't reinvent these.
- **mcpmarket.com pages are mostly SEO.** Skip them as primary sources.
- **For ATeam specifically:** the patterns most worth borrowing are (a) Superpowers' two-stage review (spec-compliance pass, then code-quality pass on a fresh subagent), (b) Trail of Bits' `fp-check` gate, and (c) the Playwright agent trio's structured-artifact handoff — all three map cleanly onto your role-specialized agent design.

---

## Suggested next steps for iteration

- Pick a target category to deepen first — I suspect **§2 logic bugs** and **§4 unit tests** have the highest leverage for your "agents make less mistakes" goal.
- For each picked skill, I can pull the actual SKILL.md, summarize its triggers and red-flag table, and propose how to adapt it into ATeam's role-specialized agent pattern.
- Worth a separate pass: how skills compose with hooks (your existing logging infra is a good fit for measuring skill-trigger compliance — same loop Anthropic's skill-creator recommends).