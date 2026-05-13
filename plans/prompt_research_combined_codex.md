# Combined Survey: Claude Code Skills and Prompt-Reuse Systems for Code Quality

Date: May 11, 2026

This combines `prompt_research_agent_a.md` and `prompt_research_agent_b.md`.
The goal is to identify reusable prompts, skills, subagents, command libraries,
and agent workflows that make coding agents produce less sloppy code: better
refactoring, bug finding, security review, unit tests, E2E tests, Playwright
tests, and future feature work.

## Merge stance

Agent A is the stronger source for quality judgment. It gives more caveats,
separates popularity from expert-vetted quality, and names concrete mechanisms
such as false-positive gates, mutation testing, second-opinion reviews, and
structured handoffs.

Agent B is useful for adoption shape. It highlights the native Claude Code
reuse stack (`CLAUDE.md`, `.claude/rules`, skills, subagents, hooks), official
commands, and a pragmatic rollout sequence. Its tables are compact, but some
rankings lean too heavily on broad popularity or command-library coverage.

## Executive recommendations

| Need | Best starting point | Why |
|---|---|---|
| Overall discipline for coding agents | `obra/superpowers` plus native Claude Code skills/hooks | Strongest workflow discipline: plan, TDD, debugging, verification, and fresh-agent review. |
| Refactoring and duplicate-code cleanup | Superpowers discipline, `citypaul` refactoring skill, Claude `/simplify` where available | Keeps refactors tied to behavior and tests instead of generic cleanup churn. |
| Logic bug review | Claude Code Review where available, Trail of Bits review workflow, Superpowers `systematic-debugging` | Combines broad review with root-cause discipline and false-positive filtering. |
| Security audit | `trailofbits/skills`, Anthropic `claude-code-security-review` / `/security-review` | Trail of Bits has the highest expert density; Anthropic is the best official default. |
| Unit and functional tests | Superpowers TDD, `wshobson/agents` `test-automator`, Trail of Bits property/mutation testing | Covers workflow, generation, and test-quality measurement. |
| E2E tests | Anthropic `webapp-testing`, Playwright official agents, `testdino/playwright-skill` | Best mix of official workflow, structured planning/generation/healing, and detailed Playwright guidance. |
| Playwright automation | Playwright MCP/agents, Anthropic `webapp-testing`, PatrickJS and wico Playwright rules/skills | Use official automation when possible, then adapt strong rule packs into local skills. |
| Reducing future mistakes | Native Claude Code stack: `CLAUDE.md`, `.claude/rules`, skills, subagents, hooks | Put conventions in context, repeatable procedures in skills, specialist work in subagents, and hard gates in hooks. |

## Cross-cutting systems worth knowing

### `obra/superpowers`

Best overall methodology. The important pattern is not any one skill, but the
workflow: brainstorm, write a plan, execute with tests, debug systematically,
verify before completion, and use fresh subagents for review. Keep this as the
main model for "make agents less sloppy."

Tradeoff: opinionated and heavier than ad hoc prompting.

### `trailofbits/skills`

Highest expert-vetted quality density. Especially relevant skills include
static analysis, Semgrep rule creation, supply-chain review, insecure defaults,
false-positive checking, property-based testing, mutation testing, and
second-opinion review.

Tradeoff: security-research oriented; some workflows are heavier than routine
feature work.

### Anthropic official skills and commands

Use official defaults where they fit: `webapp-testing`, `skill-creator`,
`claude-code-security-review`, and built-in review/security/simplification
commands where available in the installed environment.

Tradeoff: official tools are conservative and may not be as deep as a custom
multi-pass workflow.

### `wshobson/agents` and VoltAgent catalogs

Good sources for role prompts such as code reviewer, debugger, refactoring
expert, security auditor, and test automator. Treat them as a marketplace to
curate from, not as a library to install wholesale.

Tradeoff: broad coverage, uneven quality.

### SuperClaude Framework

Useful command choreography for analyze, improve, cleanup, test, implement, and
document flows. Better as a pattern source than as the core quality authority.

Tradeoff: broad framework overhead and risk of generic cleanup.

## Category recommendations

### 1. Refactoring

Top picks:

1. `citypaul/.dotfiles` refactoring skill
   - Best principle: DRY is about duplicated knowledge, not merely duplicated
     text.
   - Strong gate for when not to refactor.
   - Best when paired with tests or mutation testing.

2. `obra/superpowers`
   - Strongest discipline for refactoring after behavior is protected.
   - Useful fresh-agent code-quality pass catches duplication and overcomplexity.

3. Claude `/simplify` and SuperClaude `/sc:improve` / `/sc:cleanup`
   - Useful default cleanup passes if available.
   - Should be constrained by project rules to avoid broad churn.

Also worth borrowing:

- `l-mb/python-refactoring-skills` for Python projects with real tooling:
  radon, vulture, pylint, lizard, mutmut, ruff, and basedpyright.
- `Effeilo/claude-code-frontend-skills` for frontend-only refactors.
- `wshobson/agents` `refactoring-expert` as a generic role prompt.

### 2. Logic bug finding

Top picks:

1. Claude Code Review / `/review` / `/ultrareview`, if available
   - Best official starting point for PR-level bug and regression review.
   - Should be backed by tests and CI, not trusted alone.

2. `obra/superpowers` `systematic-debugging`
   - Best root-cause workflow for known failures.
   - Forces reproduce, isolate, hypothesize, verify.

3. Trail of Bits review workflow and `fp-check`
   - Strong second-opinion and false-positive dismissal pattern.
   - Good model for multi-agent review where findings must be disproven before
     being accepted.

Also worth borrowing:

- `codexstar69/bug-hunter` for adversarial review flow with Skeptic and Referee
  stages.
- VoltAgent debugger and code-reviewer subagents for generic runtime debugging
  and correctness review.

### 3. Security audit

Top picks:

1. `trailofbits/skills`
   - Best expert-authored security skill collection.
   - Especially useful for static analysis, supply-chain risk, sharp-edge APIs,
     insecure defaults, and custom Semgrep rules.

2. Anthropic `claude-code-security-review` / `/security-review`
   - Best official default for diff-aware security review.
   - Prefer high-confidence, newly introduced, exploitable findings.

3. `Security-Phoenix-demo/security-skills-claude-code`
   - Useful for hook-based AppSec automation and blocking risky tool use.
   - More integration friction than a single skill.

Also worth borrowing:

- `AgriciDaniel/claude-cybersecurity` for parallel specialist security agents.
- `netresearch/security-audit-skill` for PHP/OWASP-heavy projects.
- `tdccccc/claude-security-audit` for auditing Claude Code configuration,
  hooks, and MCP servers.

### 4. Unit and functional tests

Top picks:

1. `obra/superpowers` `test-driven-development`
   - Strongest behavioral discipline.
   - Explicitly prevents "write tests after" and tests that accidentally pass on
     the first run.

2. `wshobson/agents` `test-automator`
   - Useful general-purpose test generator role.
   - Covers happy path, edge cases, error cases, boundary cases, mocks, and
     coverage expectations.

3. `trailofbits/skills` property-based testing and mutation testing
   - Best answer to "are these tests actually good?"
   - Use selectively because mutation campaigns can be slow.

Also worth borrowing:

- `qdhenry` `/test:write-tests` and `/test:generate-test-cases` for broad test
  design checklists.
- Alex Oprescu's TDD workflow pattern: separate test-writer and implementer
  subagents to reduce overfitting.

### 5. E2E tests

Top picks:

1. Anthropic `webapp-testing`
   - Best official skill for web integration testing.
   - The process-lifecycle helper pattern is especially useful in sandboxed
     agent environments.

2. Playwright official planner / generator / healer agents
   - Strong structured-artifact workflow: explore, plan, generate, heal.
   - Good model for ATeam-style handoffs between specialized agents.

3. `testdino/playwright-skill`
   - Detailed Playwright knowledge base with rules, CI, POM, migration, and
     debugging guidance.

Also worth borrowing:

- `qdhenry` `/test:e2e-setup` for broad setup coverage.
- SuperClaude `/sc:test --type e2e` where its command workflow is already in
  use.

### 6. Playwright and web automation

Top picks:

1. Playwright MCP and official Playwright agents
   - Best structured browser/DOM/network access when supported.

2. Anthropic `webapp-testing`
   - Best official black-box testing pattern.

3. PatrickJS Playwright Cursor rules and wico / qualiow Playwright skills
   - PatrickJS has broad adoption and good Playwright rule coverage.
   - wico is lower adoption but more directly targeted at agent skills and
     Playwright failure-mode guidance.

Project-specific Playwright rules to adopt:

- Prefer role, label, and stable test-id selectors.
- Avoid arbitrary sleeps.
- Do not patch app code at test runtime.
- Assert user-visible outcomes, not implementation details.
- Separate app bugs from test bugs before healing.
- Keep at least some real backend or staging flows; do not over-isolate with
  mocks.

### 7. General reduction of future agent mistakes

Use the native Claude Code stack as the foundation:

- `CLAUDE.md`: stable facts, repo conventions, commands, architecture notes.
- `.claude/rules`: topic-specific guidance too large or conditional for the
  main context file.
- Skills: repeatable procedures that should be invoked on demand.
- Subagents: isolated specialist work such as reviewer, debugger, test writer,
  security auditor, and refactoring reviewer.
- Hooks: hard enforcement for things prompts cannot guarantee, such as blocking
  dangerous shell commands or requiring test/lint gates.

Recommended small initial agent set:

- `code-reviewer`
- `debugger`
- `test-automator`
- `security-auditor`
- `refactoring-specialist`
- `playwright-test-reviewer`

Do not install large catalogs wholesale. Curate, rewrite to the project stack,
and add verification gates.

## What to avoid or down-rank

- Star count as the primary ranking signal. Awesome lists and broad catalogs
  inflate popularity without proving prompt quality.
- SEO-only marketplaces or listings with little source provenance.
- Skills with hooks or shell execution that have not been audited.
- Generic cleanup commands without a behavior-preserving test plan.
- Playwright prompts that rely on sleeps, brittle selectors, or excessive API
  mocking.
- Commercial/gated resources unless their public prompt text is enough to audit.
- Language-specific skills outside their domain, such as PHP-only security
  skills or Python-only refactoring skills, unless the project matches.

## Practical adoption sequence

1. Build the native Claude Code baseline.
   - Update `CLAUDE.md` with stable project conventions and commands.
   - Split large guidance into `.claude/rules`.
   - Add a small number of skills for debugging, review, testing, security, and
     refactoring.

2. Add enforcement where prompts are unreliable.
   - Use hooks for dangerous shell blocking, required tests, lint checks, and
     session-end verification reminders.
   - Audit any third-party skill before enabling hooks or extra tool access.

3. Add specialist subagents slowly.
   - Start with reviewer, debugger, test automator, security auditor, and
     refactoring specialist.
   - Rewrite prompts to include project-specific test commands, framework
     conventions, deployment risks, and known failure modes.

4. Add false-positive gates.
   - Borrow Trail of Bits `fp-check` and Bug Hunter's Skeptic/Referee pattern.
   - Require evidence, exploitability or reproducibility, and a dismissal pass
     before presenting findings as real.

5. For Playwright, use structured artifacts.
   - Planner explores and writes a test plan.
   - Generator writes tests from the plan.
   - Reviewer/healer classifies failures as app bug or test bug before changing
     anything.

## Source merge notes

Kept from Agent A:

- The stronger caveats about star counts, backdoor risk, and low-provenance
  marketplaces.
- The emphasis on `obra/superpowers`, `trailofbits/skills`, Anthropic official
  skills, and structured Playwright agent handoffs.
- The expert-quality picks such as Trail of Bits `fp-check`, property-based
  testing, mutation testing, `citypaul` refactoring, and Bug Hunter's
  adversarial review pipeline.

Kept from Agent B:

- The native Claude Code reuse stack as the adoption foundation.
- The compact rollout model: conventions first, then skills/subagents, then
  hooks and verification gates.
- The useful alternate candidates: Claude `/simplify` and review commands,
  SuperClaude command choreography, `qdhenry` test commands, PatrickJS
  Playwright rules, and wico Playwright skills.

Down-ranked or omitted:

- Exact numeric scores from Agent B, because they imply more precision than the
  evidence supports.
- Broad catalogs as top recommendations unless they contribute a specific role
  prompt or workflow.
- Niche, commercial, SEO-like, or language-specific items that are only useful
  in narrow contexts.
