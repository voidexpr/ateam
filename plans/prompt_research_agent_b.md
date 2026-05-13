Survey: Claude Code Skills / Reusable Prompt Systems for Code Quality

Date: May 11, 2026

Scope and scoring

This survey treats “prompts” broadly: Claude Code bundled skills and slash commands, Claude Code subagents, CLAUDE.md / .claude/rules instruction systems, reusable Cursor/Copilot-style rule files, and public prompt packs that can be adapted into Claude Code.

Scores are subjective, 1–5:

Score   Meaning
Rep Reputation / adoption / source trust: official source, stars, maintainer credibility, active usage signals.
Qual    Known prompt quality: specificity, validation loops, false-positive controls, tool permissions, CI/test integration.
Fit Relevance to the requested category.

Important caveat: very few prompt packs publish rigorous benchmarks. A recent exploratory study of 2,926 repositories found context files are currently much more widely adopted than skills/subagents, and that skills/subagents are often shallowly adopted, so stars and popularity should not be treated as proof of effectiveness.  ￼

⸻

Executive picks

Use case    Best starting point
Refactoring / duplicate-code cleanup    Claude Code /simplify, then SuperClaude /sc:improve + /sc:cleanup, then a project-specific refactoring subagent.
Logic bug review    Claude Code Review / /ultrareview, plus a debugger subagent for runtime failures.
Security review Anthropic’s claude-code-security-review action or Claude Code /security-review.
Unit / functional tests qdhenry /test:write-tests + /test:generate-test-cases, then VoltAgent test-automator.
E2E tests   qdhenry /test:e2e-setup and SuperClaude /sc:test --type e2e.
Playwright tests    PatrickJS Playwright Cursor rules for broad adoption; wico Playwright Agent Skills for Claude/Cursor/Copilot-specific Playwright workflows.
Reducing future agent mistakes  Native Claude Code reuse stack: CLAUDE.md, .claude/rules, skills, subagents, hooks. Skills load on demand, subagents isolate specialized work, and hooks can enforce gates that prompts alone cannot.  ￼

⸻

1. Code refactoring: avoid duplication, make future work less error-prone

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   Claude Code /simplify — official bundled skill.  ￼  5 / 4.5 / 5 Directly targets code reuse, quality, and efficiency; spawns three review agents, aggregates findings, and applies fixes. Very relevant to “make future work less error-prone.” Focused on recently changed files; less suitable for large architectural refactors unless paired with planning or review commands.
2   SuperClaude /sc:improve + /sc:cleanup + /sc:test.  ￼    4.8 / 4 / 4.7   Very popular Claude Code framework; explicit workflow for analyze → improve → cleanup → test; covers maintainability, technical debt, dead code, import cleanup, and validation.    Framework-heavy; broad commands may need project-specific rules to avoid generic “clean code” churn.
3   VoltAgent refactoring-specialist subagent.  ￼   4.6 / 4.3 / 5   Excellent refactoring prompt structure: smell detection, characterization tests, golden-master testing, metrics, small incremental changes, duplication reduction, rollback thinking.   Community-contributed; maintainers explicitly do not guarantee correctness/security of subagents.  ￼

⸻

2. Find code logic bugs

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   Claude Code Review / /review / /ultrareview / Code Review plugin.  ￼    5 / 4.8 / 5 Best overall PR bug-finder. Official docs say it uses multi-agent full-codebase review for logic errors, security vulnerabilities, broken edge cases, and subtle regressions; plugin uses multiple agents plus confidence filtering.    Managed Code Review is research-preview / plan-dependent; /ultrareview may require extra usage; not a replacement for tests.
2   Trail of Bits review-pr.md Claude Code command.  ￼  4.2 / 4.7 / 4.6 Strong “second-opinion” design: parallel review agents, external Codex/Gemini review, deduplication, severity ranking, false-positive dismissal, and CI-as-source-of-truth verification.    Operationally complex; assumes gh, Codex/Gemini/Exa availability; includes “fix and push” behavior that may be too aggressive for some teams.
3   VoltAgent debugger + code-reviewer subagents.  ￼    4.6 / 4.2 / 4.7 Good runtime-bug workflow: reproduce, hypothesize, gather evidence, isolate root cause, validate fix, check side effects, document prevention. Code-reviewer also explicitly covers logic correctness, race conditions, error handling, and data integrity. Generic across languages; quality depends on project-specific constraints and whether the agent is allowed to run the right tests/tools.

⸻

3. Security audit

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   Anthropic claude-code-security-review GitHub Action / Claude Code /security-review.  ￼  5 / 5 / 5   Best dedicated security prompt. Official repo; diff-aware PR comments; context-aware semantic analysis; false-positive filtering; prompt explicitly requires high-confidence, newly introduced, exploitable issues only.    Official repo warns the action is not hardened against prompt injection and should be used only on trusted PRs or PRs approved by maintainers.  ￼
2   SuperClaude /sc:analyze --focus security --depth deep.  ￼   4.8 / 4 / 4.3   Popular framework with a security-focused analysis mode; useful as part of broader review workflows that also run quality and integration checks.   Less security-specific than Anthropic’s dedicated security-review prompt; false-positive policy is less explicit.
3   VoltAgent security-auditor subagent.  ￼ 4.6 / 4.2 / 4.6 Broad, structured audit prompt: vulnerability assessment, access control, compliance frameworks, incident response, evidence collection, remediation roadmap. Read-only tools are appropriate for audit.    More compliance/audit oriented than PR-diff security review; may be too broad for quick code-change review.

⸻

4. Better unit / functional tests

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   qdhenry /test:write-tests + /test:generate-test-cases.  ￼   3.6 / 4.5 / 5   Very directly useful: framework detection, existing test-convention review, unit/integration/E2E strategy, edge/error/security cases, AAA pattern, mocks, fixtures, data-driven tests, async testing.   Lower adoption than SuperClaude/VoltAgent; checklist style can become verbose unless scoped tightly.
2   VoltAgent test-automator subagent.  ￼   4.6 / 4.3 / 4.7 Strong test-automation system prompt: framework architecture, CI/CD, flaky-test control, POM, API/UI/mobile/performance coverage, test data, reporting, maintenance.    More “test automation program” than small unit-test prompt; needs project-specific coverage targets and test runner commands.
3   SuperClaude /sc:test.  ￼    4.8 / 4 / 4.1   Good runner/coverage/failure-analysis command; detects test runner and supports unit, integration, E2E, all, coverage, watch, and fix modes.    Better for executing and improving tests than for generating a comprehensive test design from scratch.

⸻

5. Better end-to-end tests

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   qdhenry /test:e2e-setup.  ￼ 3.6 / 4.6 / 5   Most complete generic E2E setup prompt found: framework selection, Playwright/Cypress/Selenium/Puppeteer/TestCafe, test environments, POM, data management, core journeys, cross-browser, CI, reporting, a11y, security.    Setup-focused; less opinionated about ongoing triage of flaky tests or app-bug-vs-test-bug classification.
2   PatrickJS Playwright E2E / Integration Cursor rules.  ￼ 5 / 4.1 / 4.6   Huge adoption signal; E2E prompt emphasizes critical user flows, navigation/state/error validation, semantic/test-id selectors, descriptive grouping, and deterministic page.route mocking. Cursor-rule format, not Claude Skill format; API mocking can over-isolate tests if not paired with true staging tests.
3   SuperClaude /sc:test --type e2e.  ￼ 4.8 / 3.9 / 4.4 Popular Claude Code workflow; explicitly activates Playwright MCP for E2E, with coverage and failure analysis support.  Less detailed Playwright guidance than dedicated Playwright prompts.

⸻

6. Better Playwright / web automation tests

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   PatrickJS awesome-cursorrules: Playwright E2E, integration, API, accessibility rules.  ￼    5 / 4.2 / 5 Best popular Playwright prompt pack: E2E, integration, API, and accessibility prompts; emphasizes TypeScript auto-detection, critical user flows, semantic selectors, deterministic tests, response/schema validation, axe/WCAG testing.    Cursor-centric; needs conversion to Claude Code skill/rules. Some examples use mocks heavily, so add a policy requiring at least some real backend/staging flows.
2   wico / qualiow-playwright-skills.  ￼    2 / 4.6 / 5 Most targeted Playwright Agent Skills package: scaffolds skills for Claude Code, Cursor, Copilot, or generic .agent-skills; covers waitForResponse, toPass, expect.poll, network-first safeguards, POM conventions, debugging, and app-bug-vs-test-bug decision trees.  New / low adoption signal; only a few forks at time of review.
3   qdhenry /test:e2e-setup.  ￼ 3.6 / 4.4 / 4.5 Good Playwright setup guidance: install, browser projects, POM, data management, cross-browser/device config, CI, screenshots/videos, retries, a11y/security hooks. More setup than test-quality review; examples use some brittle selectors that should be adapted to role/test-id selectors.

⸻

7. General systems to improve agent-generated code and reduce future mistakes

Rank    Prompt / system Rep / Qual / Fit    Strengths   Weaknesses
1   Native Claude Code reuse stack: CLAUDE.md, .claude/rules, Skills, Subagents, Hooks.  ￼  5 / 4.8 / 5 Best foundation. Put facts/conventions in CLAUDE.md; procedures in skills; specialist review/debug/test roles in subagents; mandatory gates in hooks. Official docs note hooks can block tool calls and run at lifecycle events, while CLAUDE.md is context rather than hard enforcement.  ￼    Requires governance. Skills can grant tool permissions and dynamic shell context, so project skills should be reviewed before trusting a repo; shell execution can be disabled by policy.  ￼
2   SuperClaude Framework.  ￼   4.8 / 4 / 4.7   Strong general-purpose workflow library: analyze, troubleshoot, improve, cleanup, test, implement, build, document; high adoption; useful command choreography for feature, bug-fix, refactor, and review workflows.    Broad framework may add cognitive overhead; individual commands may need tightening for your repo.
3   VoltAgent awesome-claude-code-subagents.  ￼ 4.6 / 3.9 / 4.6 Very strong reusable subagent library; independent contexts, domain-specific prompts, shared project/global installation, granular tool permissions, and many QA/security/refactoring agents.   Community-contributed and explicitly “as is”; should be curated into a smaller project-specific set.

⸻

Other prompts / systems reviewed

Prompt / system Core strength   Core weakness   Link
qdhenry /dev:refactor-code  Tests-before-refactoring, incremental steps, DRY, static analysis, integration tests, rollback/deploy thinking. Long generic checklist; lower adoption than larger frameworks.  ￼
qdhenry /dev:fix-issue  Good issue workflow: reproduce, root-cause analysis, minimal fix, regression tests, quality checks, PR. Assumes GitHub issue flow and gh; less useful for ad hoc bugs.  ￼
qdhenry /security:security-audit    Broad security checklist across dependencies, auth, input validation, secrets, logging, infra, headers/CORS, reporting. Less false-positive discipline than Anthropic security-review; broad rather than diff-focused.  ￼
qdhenry Claude Command Suite overall    216+ slash commands, 12 skills, 54 agents; strong coverage for dev, testing, security, cleanup, and automation. Command sprawl; some commands are checklist prompts rather than deeply engineered agents.   ￼
Iron-Ham claude-deep-review Multi-agent deep review with code, errors, architecture, tests, simplification, security, concurrency, perf, GitHub Actions, and agent-instruction reviewers; reports confidence ≥80% for code reviewer.    Heavyweight; lower reputation signal than official/SuperClaude/VoltAgent; may need pruning. ￼
Trail of Bits claude-code-config    Very high-quality PR review/fix workflow; strong CI verification and multi-model second opinion.    More an operational workflow than a simple prompt; assumes several external tools.  ￼
ComposioHQ awesome-claude-skills    Very large, high-adoption skills directory; useful for discovering skill format patterns and integrations.  Not code-quality-specific; directory size does not imply individual prompt quality. ￼
alirezarezvani claude-skills    246 production-ready skills/plugins across 12 AI coding tools; includes security auditor, Playwright toolkit, self-improving-agent. Very broad; individual engineering prompts need audit before team use.  ￼
heilcheng Agent Skill Index Curated cross-agent skill directory focused on real-world skills used by engineering teams. Directory, not a single reusable code-quality prompt; quality varies by linked skill.   ￼
Kodus awesome-agent-skills  Good lightweight template: clear scope, objective description, step-by-step instructions, explicit output format, real examples.    Low adoption signal; mostly a curated list/template.    ￼
idavidov13 Playwright Scaffold AI-Assisted Development  Maintained around Claude Code/Cursor/Copilot and Playwright releases; likely useful for Playwright-heavy teams. Public repo page is limited; deeper patterns appear gated/commercial.   ￼

⸻

Practical recommendation for adoption

1. Create a small native Claude Code baseline first. Put stable conventions in CLAUDE.md; split large topic-specific guidance into .claude/rules; convert repeatable procedures into skills; add hooks for hard gates such as “run tests after file change,” “block dangerous shell commands,” or “require lint before stop.” Claude’s docs explicitly note that CLAUDE.md shapes behavior but is not hard enforcement; hooks are the enforcement layer.  ￼
2. Use official commands for default guardrails. Run /simplify before PRs, /security-review for security-sensitive diffs, and /review or /ultrareview for bug/regression review.  ￼
3. Install only a few specialized subagents. Start with code-reviewer, debugger, test-automator, security-auditor, and refactoring-specialist; then rewrite them to your stack and CI commands.
4. For Playwright, add explicit anti-false-positive rules. Require role/test-id selectors, no arbitrary sleeps, no test-only app mutations, no runtime patching of app code, real assertions on user-visible outcomes, and clear classification of “app bug” vs “test bug.” Use PatrickJS for breadth and wico for Claude/Cursor/Copilot skill scaffolding.