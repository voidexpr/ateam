# Survey: Claude Code Skills & Prompt-Reuse Systems for Code Quality (Combined)

> Merge of `prompt_research_agent_a.md` (A) and `prompt_research_agent_b.md` (B).
> Scope: skills, subagents, slash commands, plugins, CLAUDE.md / rules systems,
> and adaptable prompt packs that improve code produced by agents —
> refactoring, bug-finding, security, unit/functional tests, E2E, Playwright,
> and general "make the agent less sloppy."

## Editorial notes — what was kept from each survey and why

- **Kept from A**: the cross-cutting frameworks table (Superpowers, Trail of
  Bits, anthropics/skills, wshobson/agents, VoltAgent, SuperClaude); the
  specific "why this is good" detail on individual skills (Iron Laws,
  bug-hunter Skeptic stage, fp-check gate, Playwright agent trio); the
  honest-caveats section (backdoor risk, awesome-* star inflation, built-in
  beats third-party); ATeam-specific borrow-this list.
- **Kept from B**: the native Claude Code stack framing (CLAUDE.md → rules →
  skills → subagents → hooks, with hooks as the only real enforcement layer);
  the executive-picks table; the Rep/Qual/Fit scoring shorthand; entries A
  missed (qdhenry Claude Command Suite, Iron-Ham claude-deep-review,
  PatrickJS awesome-cursorrules, wico Playwright Agent Skills, Kodus,
  ComposioHQ directory); the caveat that the 2,926-repo study found
  skills/subagents shallowly adopted — i.e. don't trust stars as proof of
  effectiveness.
- **Dropped or merged**: duplicate entries (both surveys hit Superpowers,
  Trail of Bits, anthropics security review, VoltAgent — merged); B's
  qdhenry-heavy picks where A's choices had stronger expert-vetted signal
  (kept qdhenry as a secondary pick, not primary); A's `testers.ai` /
  mcpmarket.com mentions (low signal — kept only as a "skip these" note).
- **Resolved disagreements**: where A and B picked different #1s I picked the
  one with the strongest *expert-vetted* or *official* signal, and listed the
  alternative as #2. Stars alone did not decide.

Scoring shorthand (carried from B): **Rep** = reputation/adoption/trust,
**Qual** = prompt quality (specificity, FP control, gates), **Fit** =
relevance to the category. 1–5, subjective.

---

## 0. Cross-cutting frameworks (read this before picking anything else)

| Project | What it is | Honest take |
|---|---|---|
| **Native Claude Code stack** ([docs](https://code.claude.com/docs/)) — CLAUDE.md, `.claude/rules`, Skills, Subagents, Hooks | The built-in reuse primitives. CLAUDE.md = conventions (context, not enforcement). Rules = topic-scoped guidance. Skills = on-demand procedures. Subagents = isolated specialist contexts. Hooks = the **only** hard-enforcement layer (can block tool calls at lifecycle events). | Best foundation. Most third-party frameworks below are layered on top of this. Don't skip it. |
| **obra/superpowers** ([GitHub](https://github.com/obra/superpowers)) | Opinionated skills framework: brainstorming → write-plan → execute-plan with TDD, systematic-debugging, subagent-driven-development, verification-before-completion. Multi-host. | Best in class for *enforcing discipline*. Each skill opens with an "Iron Law" + red-flags table of common AI rationalizations. Weakness: you adopt the methodology or fight it. The TDD and debugging skills are the strongest individual artifacts I found. |
| **trailofbits/skills** ([GitHub](https://github.com/trailofbits/skills)) | ~40 skills from the security firm: `static-analysis`, `semgrep-rule-creator`, `insecure-defaults`, `supply-chain-risk-auditor`, `mutation-testing`, `property-based-testing`, `second-opinion`, `fp-check`. CC BY-SA 4.0. | Highest *expert-vetted quality density* in the survey. Authored by actual auditors. The companion `trailofbits/skills-curated` lists *other* marketplaces they've code-reviewed — uniquely valuable in a backdoor-prone ecosystem. |
| **anthropics/skills** ([GitHub](https://github.com/anthropics/skills)) | Official: docx, pdf, pptx, xlsx, `webapp-testing`, `skill-creator`, `frontend-design`, etc. | Reference implementation. `webapp-testing` and `skill-creator` are directly relevant; rest is document-domain. Conservative, well-tested. |
| **wshobson/agents** ([GitHub](https://github.com/wshobson/agents)) | 185 subagents + 153 skills + 80 plugins + 100 slash commands; Sonnet/Haiku tier orchestration baked in. | Broadest "à la carte" library. Quality varies; orchestration patterns (`/full-stack-orchestration`, `/security-scanning`) are well thought out. Marketplace, not a methodology. |
| **VoltAgent/awesome-claude-code-subagents** ([GitHub](https://github.com/VoltAgent/awesome-claude-code-subagents)) | 100+ subagents organized by category. | Catalog. Useful for plucking specific role prompts (`code-reviewer`, `security-auditor`, `test-automator`, `refactoring-specialist`, `debugger`). Quality uneven — read before adopting. Maintainers explicitly disclaim correctness/security. |
| **SuperClaude_Framework** ([GitHub](https://github.com/SuperClaude-Org/SuperClaude_Framework)) | 30 slash commands + 9 personas + MCP routing. `/sc:improve`, `/sc:cleanup`, `/sc:analyze`, `/sc:test`. | More "context engineering" than skills. High adoption. Commands are broad and may need project-specific tightening to avoid generic clean-code churn. |
| **qdhenry Claude Command Suite** ([GitHub](https://github.com/qdhenry/Claude-Command-Suite)) | 216+ slash commands, 12 skills, 54 agents covering dev/testing/security/cleanup. | Broad coverage. Some commands are checklist prompts rather than deeply engineered agents — still useful as starting templates. |

> Caveat (from B): a recent study of 2,926 repos found CLAUDE.md-style context
> files are much more widely adopted than skills/subagents, and most
> skills/subagents are shallowly adopted. Stars ≠ effectiveness.

## Executive picks

| Use case | Best starting point |
|---|---|
| Refactor / dedupe | `obra/superpowers` (TDD posture) + `citypaul` refactoring skill; bundled `/simplify` for diffs |
| Logic bug review | Bundled `/review` / `/ultrareview` + Trail of Bits `review-pr.md` for second-opinion |
| Security audit | `anthropics/claude-code-security-review` (`/security-review`) for diffs; Trail of Bits skills for depth |
| Unit / functional tests | Superpowers `test-driven-development` + Trail of Bits `property-based-testing` + `mutation-testing` |
| E2E tests | `anthropics/skills/webapp-testing` + Playwright Agents (planner/generator/healer) |
| Playwright | Playwright official MCP + Agents; PatrickJS rules for breadth; wico for Skill-format scaffolds |
| Reduce future mistakes | Native CC stack first; Superpowers' two-stage review pattern on top |

---

## 1. Code refactoring (avoid duplication, future-proof)

### Top 3

1. **`citypaul/.dotfiles` → `refactoring` skill** ([link](https://github.com/citypaul/.dotfiles/blob/main/claude/.claude/skills/refactoring/SKILL.md)) — Rep 3 / Qual 4.7 / Fit 5
   - TDD-aware. Explicit "DRY = knowledge, not code." Refuses to extract for testability alone. Has a "when NOT to refactor" gate that prevents speculative restructuring. Pairs with mutation testing.
   - Weakness: tightly coupled to TDD; single-author dotfiles repo.

2. **`obra/superpowers` (refactoring posture inside framework)** ([link](https://github.com/obra/superpowers)) — Rep 5 / Qual 4.6 / Fit 4.5
   - Refactoring is a *post-mutation-testing* step. Forces YAGNI. `subagent-driven-development` runs a code-quality review pass on a second agent that catches DRY violations.
   - Weakness: not standalone — adopt the whole methodology.

3. **Bundled `/simplify` (Claude Code)** — Rep 5 / Qual 4 / Fit 5
   - Official. Spawns three review agents, aggregates findings, applies fixes. Best for recently-changed files.
   - Weakness: scoped to diffs; less suitable for architectural refactors. Pair with `/sc:improve` + `/sc:cleanup` for broader passes.

### Also reviewed

- **`l-mb/python-refactoring-skills`** — eight focused skills wiring real Python tooling (radon, vulture, pylint, lizard, mutmut, ruff, basedpyright). Python only.
- **VoltAgent `refactoring-specialist`** — smell detection, characterization tests, golden-master testing, incremental changes, rollback thinking. Community-contributed.
- **SuperClaude `/sc:improve` + `/sc:cleanup`** — explicit analyze → improve → cleanup → test workflow. Generic without project rules.
- **`WomenDefiningAI/.../code-refactoring`** — proactive file-size watcher before edits. Novel; dependency-heavy (Node daemon).
- **`Effeilo/claude-code-frontend-skills`** — `/front-refactor` with preview/apply modes. Frontend-only.
- **`alirezarezvani/claude-skills`** — `ln-623` DRY/KISS/YAGNI auditor, `ln-626` dead-code finder. 200+ skills of mixed quality.
- **qdhenry `/dev:refactor-code`** — tests-before-refactoring checklist. Long, lower adoption.
- **`wshobson/agents` → `refactoring-expert`** — solid generic agent, mostly prose checklist.

---

## 2. Find code logic bugs

### Top 3

1. **Claude Code `/review` / `/ultrareview` / official Code Review plugin** ([docs](https://code.claude.com/docs/en/code-review)) — Rep 5 / Qual 4.8 / Fit 5
   - Official multi-agent full-codebase review for logic errors, edge cases, subtle regressions. Plugin uses confidence filtering. Tunable via `CLAUDE.md` / `REVIEW.md`.
   - Weakness: managed review is research-preview / plan-gated; `/ultrareview` has extra cost.

2. **`obra/superpowers` → `systematic-debugging`** ([link](https://github.com/obra/superpowers)) — Rep 5 / Qual 4.8 / Fit 5
   - 4-phase root-cause process (reproduce → isolate → hypothesize → verify) that *forbids* fixing what you haven't understood. Includes `root-cause-tracing` and `defense-in-depth`. Architectural-review trigger after 3 failed fix attempts (good doom-loop escape valve).
   - Weakness: slower than "just try a fix" — intentionally.

3. **Trail of Bits `review-pr.md` + `fp-check`** ([link](https://github.com/trailofbits/skills)) — Rep 4.2 / Qual 4.7 / Fit 4.6
   - Parallel review agents + *external* Codex/Gemini second opinion, deduplication, severity ranking, FP dismissal, CI-as-source-of-truth. `fp-check` is a standalone false-positive gate you can bolt onto any bug-finder — addresses the #1 problem with LLM bug hunters.
   - Weakness: assumes `gh`, Codex/Gemini availability; "fix and push" behavior may be too aggressive.

### Also reviewed

- **`codexstar69/bug-hunter`** — Triage → Recon → Hunter → **Skeptic** → Referee → Fix Plan → Fixer → Verify. The Skeptic stage explicitly tries to disprove findings with counter-evidence. Young; adversarial loops are token-hungry.
- **`anthropics/claude-code-security-review`** — marketed as security but catches plenty of logic bugs. Not hardened against prompt injection — Anthropic says "only trusted PRs."
- **VoltAgent `debugger` + `code-reviewer`** — reproduce/hypothesize/isolate/validate; covers logic, races, error handling, data integrity. Generic.
- **Trail of Bits `differential-review`** — diff-aware review; less noisy on PRs.
- **Iron-Ham `claude-deep-review`** — multi-agent reviewers across code/errors/architecture/tests/concurrency/perf with ≥80% confidence threshold. Heavyweight; lower reputation signal than the above.
- **qdhenry `/dev:fix-issue`** — reproduce → root cause → minimal fix → regression test → PR. Assumes GitHub.
- **`shuvonsec/claude-bug-bounty`** — web-vuln-focused, not general logic.
- **Skip**: mcpmarket.com "Bug Detection Review" pages (pure SEO).

---

## 3. Security audit

### Top 3

1. **`trailofbits/skills`** ([link](https://github.com/trailofbits/skills)) — Rep 5 / Qual 5 / Fit 5
   - Professional auditors. `static-analysis` (CodeQL + Semgrep + SARIF), `insecure-defaults`, `semgrep-rule-creator`, `supply-chain-risk-auditor`, `entry-point-analyzer`, `sharp-edges`. Documented production bugs found (e.g. timing side-channel in ML-DSA). Companion `skills-curated` lists code-reviewed marketplaces — unique signal.
   - Weakness: heavy security-research lean; some smart-contract-specific.

2. **`anthropics/claude-code-security-review` / `/security-review`** ([link](https://github.com/anthropics/claude-code-security-review)) — Rep 5 / Qual 5 / Fit 5
   - Official. Diff-aware PR comments; context-aware semantic analysis; FP filtering; explicit "high-confidence, newly introduced, exploitable" filter. GitHub Action for PRs.
   - Weakness: explicitly not hardened against prompt injection — only trusted PRs.

3. **`Security-Phoenix-demo/security-skills-claude-code`** ([link](https://github.com/Security-Phoenix-demo/security-skills-claude-code)) — Rep 3 / Qual 4 / Fit 4.5
   - AppSec automation kit: 4 slash commands + 4 hooks (SessionStart, PreToolUse blocks malicious packages on Bash, PostToolUse pattern scan on writes, SessionEnd reminder). Multi-language.
   - Weakness: vendor-aligned; many hooks; integration friction.

### Also reviewed

- **SuperClaude `/sc:analyze --focus security --depth deep`** — useful in broader workflows; less security-specific than Anthropic's dedicated prompt.
- **VoltAgent `security-auditor`** — broad audit (vuln assessment, AC, compliance, incident response, remediation roadmap). Read-only tools. More compliance-oriented than PR-diff.
- **`AgriciDaniel/claude-cybersecurity`** — 8 parallel specialists (vulns, authz, secrets, supply chain, IaC, threat-intel, AI-code patterns, business logic). OWASP 2025 + CWE Top 25 + MITRE ATT&CK refs. One-author, less battle-tested.
- **`netresearch/security-audit-skill`** — 80+ PHP/OWASP checkpoints with reference docs. PHP only.
- **`wrsmith108/claude-skill-security-auditor`** — wraps `npm audit` with structured remediation. Thin.
- **`tdccccc/claude-security-audit`** — audits *Claude Code's own config* (hooks, MCP servers) for malicious entries. Directly relevant for ATeam's container hardening.
- **qdhenry `/security:security-audit`** — broad checklist; less FP discipline than Anthropic's prompt.
- **`Eyadkelleh/awesome-claude-skills-security`** — pentesting/CTF angle, not codebase audit.

---

## 4. Better unit / functional tests

### Top 3

1. **`obra/superpowers` → `test-driven-development`** ([link](https://github.com/obra/superpowers)) — Rep 5 / Qual 4.9 / Fit 5
   - Enforces strict red-green-refactor. Iron Laws treat "I'll write the test after" as grounds to delete the implementation. Red-flag list catches tests passing on first run, deleted-code-as-reference, "just this once." Pairs with `subagent-driven-development` so the implementer doesn't see the tests (anti-overfitting).
   - Weakness: workflow many devs find slow.

2. **`trailofbits/skills` → `property-based-testing` + `mutation-testing`** ([link](https://github.com/trailofbits/skills/tree/main/plugins)) — Rep 5 / Qual 4.9 / Fit 5
   - Serious test-quality territory. `property-based-testing` for Hypothesis/proptest invariants. `mutation-testing` *measures* whether tests actually catch bugs — the right answer to "are my tests any good."
   - Weakness: mutation campaigns can take hours.

3. **`qdhenry /test:write-tests` + `/test:generate-test-cases`** ([link](https://github.com/qdhenry/Claude-Command-Suite)) — Rep 3.6 / Qual 4.5 / Fit 5
   - Framework detection, existing-convention review, unit/integration/E2E strategy, edge/error/security cases, AAA, mocks, fixtures, async. Directly useful as a generator.
   - Weakness: checklist style; verbose unless scoped.

### Also reviewed

- **`wshobson/agents` → `test-automator` + `/unit-testing:test-generate`** — happy + edge + error + boundary, AAA, coverage thresholds, typed mocks. Generic; needs tuning.
- **SuperClaude `/sc:test`** — runner/coverage/failure-analysis with unit/integration/E2E/coverage/watch/fix modes. Better for executing than for generating design.
- **alexop.dev custom TDD workflow** ([blog](https://alexop.dev/posts/custom-tdd-workflow-claude-code-vue/)) — orchestrate a `tdd-test-writer` subagent and a separate `tdd-implementer` so the implementer can't see test rationale. Worth reading if you design your own.
- **dev.to "How we use Claude Agents to automate test coverage"** — published prompt for a TypeScript test-coverage agent.
- **`alirezarezvani/claude-code-skill-factory/tdd-guide`** — multi-framework TDD with coverage analysis. Decent reference.
- **VoltAgent `test-automator`** — strong test-automation *program* (CI/CD, flaky-test control, POM, performance, reporting). Heavier than a unit-test prompt.

---

## 5. Better end-to-end tests

### Top 3

1. **`anthropics/skills/webapp-testing`** ([link](https://github.com/anthropics/skills/tree/main/skills/webapp-testing)) — Rep 5 / Qual 4.5 / Fit 5
   - Official. Python + Playwright. Includes `with_server.py` to manage backend+frontend process lifecycle (important for sandboxed agents). Decision tree (static HTML → direct selectors; dynamic → recon-then-action). Scripts callable black-box without polluting context.
   - Weakness: Python Playwright only; minimal scaffolding.

2. **Playwright Agents (planner / generator / healer)** ([Shipyard guide](https://shipyard.build/blog/playwright-agents-claude-code/)) — Rep 5 / Qual 4.6 / Fit 5
   - Official Playwright project ships three Claude Code subagents: **planner** explores the app and writes a Markdown test plan, **generator** turns the plan into tests, **healer** fixes broken tests after feature changes. Structured-artifact handoff between agents.
   - Weakness: TS/JS Playwright only. Requires Playwright MCP.

3. **`qdhenry /test:e2e-setup`** — Rep 3.6 / Qual 4.6 / Fit 5
   - Most complete generic E2E setup prompt: framework selection, Playwright/Cypress/Selenium/Puppeteer/TestCafe, env config, POM, data management, core journeys, cross-browser, CI, reporting, a11y, security.
   - Weakness: setup-focused; weak on flaky-test triage and app-bug vs test-bug classification.

### Also reviewed

- **`testdino/playwright-skill`** — 70+ guides around "10 Golden Rules." TS and JS examples. CI, POM, migration from Cypress/Selenium.
- **PatrickJS Playwright E2E / Integration Cursor rules** — critical user flows, semantic/test-id selectors, deterministic `page.route` mocking. Cursor-rule format; needs conversion to Claude skill format.
- **SuperClaude `/sc:test --type e2e`** — activates Playwright MCP. Less detailed than dedicated Playwright prompts.
- **`alirezarezvani/claude-skills` → `playwright-pro`** — serviceable generic.
- **`lackeyjb/playwright-skill`** — Claude writes *custom* Playwright on the fly for one-off automation. Good for exploration; not a suite generator.

---

## 6. Better Playwright / web automation tests

Substantial overlap with §5; picks diverge:

### Top 3

1. **Playwright official MCP + Agents** ([docs](https://playwright.dev/), [plugin](https://claude.com/plugins/playwright)) — Rep 5 / Qual 4.7 / Fit 5
   - Microsoft-maintained MCP giving structured DOM/network/console access. Drop-in for Claude Code/Cursor/VS Code. Agents are first-party.
   - Weakness: heavyweight; not all sandbox setups support MCP.

2. **PatrickJS awesome-cursorrules: Playwright E2E / integration / API / a11y** — Rep 5 / Qual 4.2 / Fit 5
   - Highest-adoption Playwright prompt pack. TS auto-detection, critical user flows, semantic selectors, deterministic tests, response/schema validation, axe/WCAG.
   - Weakness: Cursor-centric. Heavy mocking in examples — pair with a policy requiring some real backend/staging flows.

3. **wico / `qualiow-playwright-skills`** — Rep 2 / Qual 4.6 / Fit 5
   - Most targeted Playwright Agent Skills package: scaffolds skills for Claude Code, Cursor, Copilot, or generic `.agent-skills`. Covers `waitForResponse`, `toPass`, `expect.poll`, network-first safeguards, POM, debugging, app-bug vs test-bug decision trees.
   - Weakness: new, low adoption.

### Also reviewed

- **`anthropics/skills/webapp-testing`** — see §5.
- **`lackeyjb/playwright-skill`** — model-invoked, Claude writes Playwright on demand. Visible browser by default.
- **Chrome Relay** (in ComposioHQ list) — drives the user's already-open Chrome (cookies, SSO, extensions). Useful for authenticated test flows Playwright can't easily replicate. Niche.
- **`testomat.io` Playwright MCP guide** — solid tutorial.
- **MindStudio Playwright MCP guide** — pattern reference, not a skill.

---

## 7. General "improve code from agents" / reduce future mistakes

### Top 3

1. **Native Claude Code stack** — CLAUDE.md + `.claude/rules` + Skills + Subagents + Hooks — Rep 5 / Qual 4.8 / Fit 5
   - Best foundation. Conventions in CLAUDE.md; topic-scoped guidance in rules; procedures in skills; specialist roles in subagents; mandatory gates in hooks. Hooks are the **only real enforcement layer** — CLAUDE.md is context, not policy.
   - Weakness: needs governance — third-party skills can grant tool permissions and run shell, so audit before mounting (Trail of Bits has found backdoors in published skills).

2. **`obra/superpowers`** ([link](https://github.com/obra/superpowers)) — Rep 5 / Qual 4.8 / Fit 5
   - Brainstorming → planning → subagent-driven implementation with a fresh-eyes review pass. Session-start hook injects a bootstrap doc telling the agent to invoke a relevant skill before doing anything else. Maps cleanly onto ATeam-style orchestration.

3. **`trailofbits/skills` → `second-opinion` + `skill-improver`** ([link](https://github.com/trailofbits/skills)) — Rep 5 / Qual 4.8 / Fit 5
   - `second-opinion` runs reviews via *external* LLM CLIs (Codex, Gemini) on your changes — defends against single-model blind spots. `skill-improver` iterates on your own skills.
   - Plus `anthropics/skill-creator` — the TDD-for-skills loop (pressure-test scenarios → write skill → measure compliance → refactor) is the right meta-process.

### Also reviewed

- **SuperClaude Framework** — analyze/troubleshoot/improve/cleanup/test/implement/build/document. High adoption; broad commands may need tightening.
- **VoltAgent awesome-claude-code-subagents** — independent contexts, domain-specific prompts, granular tool permissions. Curate to a project-specific subset.
- **Trail of Bits `claude-code-config`** — high-quality PR review/fix workflow with CI verification and multi-model second opinion. Operationally complex.
- **`affaan-m/everything-claude-code`** — 48 subagents + 182 skills + 68 commands + hooks. Includes context-management rules (e.g. "avoid last 20% of context window for large refactors"). Reference for an opinionated harness.
- **`FlorianBruniaux/claude-code-ultimate-guide` audit-prompt** — self-audit prompt grading your CC config across 8 weighted dimensions. Quarterly meta-tool.
- **Iron-Ham `claude-deep-review`** — heavyweight multi-agent deep review (code, architecture, tests, simplification, security, concurrency, perf, GHA, agent-instructions). Worth pruning.
- **ComposioHQ awesome-claude-skills** — large directory; good for discovering skill format patterns.
- **`alirezarezvani/claude-skills`** — 246 skills/plugins across 12 tools. Audit before adopting.
- **`heilcheng` Agent Skill Index** — curated cross-agent directory; varies by linked skill.
- **`Kodus` awesome-agent-skills** — clean lightweight template (scope, objective, steps, output format, examples). Low adoption.
- **`vijaythecoder/awesome-claude-agents/code-reviewer.md`** — well-structured review prompt with severity-tagged report template; delegates to specialists.
- **`wshobson/agents` → `code-reviewer` + `architect-reviewer`** — solid generic.

---

## Honest caveats / quality flags

- **Stars mislead.** "Awesome-" repos rack up stars from list inclusion. The 2,926-repo study found skills/subagents are mostly shallowly adopted. Read the actual SKILL.md before adopting.
- **Backdoor risk is real.** Trail of Bits has documented published skills with backdoors and malicious hooks. For ATeam (running skills in containers), audit any third-party skill before mounting it. `tdccccc/claude-security-audit` scans for this specifically.
- **Built-in often beats third-party.** Claude Code now bundles `/security-review`, `/review`, `/ultrareview`, `/simplify`, `/debug`, `/batch`, `/loop`, `/claude-api`. Don't reinvent these.
- **CLAUDE.md is not enforcement.** Anthropic's docs are explicit — CLAUDE.md shapes behavior but doesn't block anything. If you need a gate, write a hook.
- **`mcpmarket.com` pages are mostly SEO.** Skip as primary sources.

## For ATeam specifically

The patterns most worth borrowing:

- **Superpowers' two-stage review** (spec-compliance pass, then code-quality pass on a fresh subagent) — maps onto your role-specialized agent design.
- **Trail of Bits' `fp-check` gate** — bolt onto any bug-finding pipeline to suppress LLM false positives.
- **Playwright agent trio's structured-artifact handoff** (planner writes Markdown plan, generator consumes it, healer fixes drift) — clean model for inter-agent contracts.
- **`tdccccc/claude-security-audit`** — directly applicable to auditing skills/MCP configs before mounting in containers.
- **Hooks for enforcement, not prompts** — your existing logging infra is the right place to measure skill-trigger compliance (the loop `anthropics/skill-creator` recommends).

## Suggested adoption path

1. **Baseline first.** Stable conventions in CLAUDE.md; topic guidance in `.claude/rules`; procedures as skills; hard gates as hooks (tests after file change, block dangerous shell, lint before stop).
2. **Use official commands for default guardrails.** `/simplify` before PRs; `/security-review` for security-sensitive diffs; `/review` or `/ultrareview` for bug/regression review.
3. **Install a small specialist set.** code-reviewer, debugger, test-automator, security-auditor, refactoring-specialist — then rewrite to your stack and CI commands.
4. **For Playwright: anti-FP rules upfront.** Require role/test-id selectors, no arbitrary sleeps, no test-only app mutations, no runtime patching of app code, real assertions on user-visible outcomes, explicit app-bug vs test-bug classification.
5. **Deepen §2 (logic bugs) and §4 (unit tests) first** — highest leverage for the "agents make fewer mistakes" goal.
