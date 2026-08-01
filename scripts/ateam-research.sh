#!/usr/bin/env bash
set -euo pipefail

file_prefix="ateam-research-report"

function print_reports {
  ls -alF "${file_prefix}*.md" || true
}

echo "Reports before:"
print_reports

# TODO: START DATE and END DATE
ateam exec --agent codex --model gpt-5.6-sol <<EOF
You are producing a periodic **technical revue de presse** for the maintainer of this project: ateam.

Do not spend report space summarizing ateam itself.

## Project Context
* here are the first 100 lines of README.md for context:

$(head -100 README.md)

## Local Context
* git log gives you an idea of recent changes and direction to help you steer your research

### Researched projects
* track big changes for [fencesandbox/fence](https://github.com/fencesandbox/fence)
* here is an index of some projects looked at:
$(grep '###' docs/Research*.md)

## Reporting period

Report files are named: ${file_prefix}-YYYY-MM-DD.md
* Add the hour, minute second if already exists
* Read the most recent one if it exists
* Cover developments from **[START DATE] through [END DATE]**.
  * If these dates are not provided start from the timestamp of the most recent report file.
  * Otherwise, default to the previous 30 days.

Use the date when the change actually occurred, not merely the date of an article discussing it. Include an older development only when it has newly become relevant.

## What to research

Look for significant developments involving coding-agent products, agent harnesses, source-code agents, and their supporting infrastructure.

Examples include Claude Code, OpenAI Codex, GitHub Copilot coding agents, Gemini CLI, Cursor background agents, Aider, OpenHands, SWE-agent, Goose, OpenCode, Cline/Roo, and relevant sandbox or execution platforms. This is not a checklist: discover new projects as well.

Focus on:

### 1. Major product or architectural changes

- Important new capabilities or changes of direction
- New approaches to unattended or long-running agents
- Changes that materially affect how agents edit, test, review, or understand code
- Major integrations, acquisitions, deprecations, or changes in project focus

Do not report routine release notes, minor feature additions, or changelog noise.

### 2. Agent efficiency

Include credible techniques or products that claim to reduce:

- Token usage
- Context size
- Model turns
- Tool calls
- Execution time
- Cost
- Redundant repository exploration
- Repeated build, test, or debugging work

I am especially interested in:

- Host-side preparation of context so the model does not have to discover or summarize it
- Code maps, symbol indexes, AST-based context, retrieval, dependency analysis, and change-impact analysis
- Moving deterministic work out of the model loop
- Context caching, compaction, reuse, and handoff between agents
- Structured prompts or execution pipelines combining:
  - pre-model processing
  - model instructions
  - tool definitions
  - post-tool processing
  - validation or review stages

Do not repeat efficiency claims uncritically. Prefer measurements, implementation details, benchmarks, or credible user reports.

### 3. Isolation and execution environments

Look for new approaches involving:

- Containers, sandboxes, microVMs, namespaces, seccomp, or capability-based isolation
- Filesystem, process, network, and credential restrictions
- Fast-starting ephemeral environments
- Running agents safely against untrusted repositories
- Nested-container or container-in-container operation
- Isolation specifically designed for coding agents
- Alternatives or complements to Fence

Highlight anything that could plausibly become an ateam component.

### 4. Claude Code and Codex operational changes

Pay particular attention to changes affecting unattended agents.

For both Claude Code and OpenAI Codex, check for changes to:

- Whether consumer subscriptions may be used for unattended or automated execution
- Pricing, subscription eligibility, quotas, rate limits, or fair-use rules
- Distinctions between subscription usage and API-billed usage
- Terms or enforcement related to automation, sharing credentials, or continuous operation
- Login from containers, remote hosts, CI systems, and headless environments
- Device login, OAuth, API keys, token persistence, and credential expiration
- Permission and approval models
- Non-interactive or unattended modes
- Restrictions on bypassing approvals
- Filesystem, network, shell, or privileged-operation controls
- Machine-readable usage and telemetry
- Token and cost reporting
- Remaining daily, rolling, weekly, or monthly quota
- Quota reset times
- APIs or commands that allow an orchestrator to pace work based on remaining capacity

Clearly distinguish:

- Officially supported behavior
- Behavior that is technically possible but undocumented
- Community reports
- Confirmed restrictions
- Speculation

For pricing, subscriptions, authentication, limits, and terms, require an official primary source before presenting a conclusion. Community reports may be included only when clearly labeled.

## Source priorities

Prefer, in order:

1. Official documentation, announcements, terms, pricing pages, or engineering blogs
2. Repository code, releases, issues, pull requests, and design documents
3. Research papers, technical talks, and reproducible benchmarks
4. Credible independent analysis
5. Community reports, clearly labeled as anecdotal

Link to the most specific primary source, not a search result or generic project homepage.

Combine multiple articles about the same development into one item.

## Interestingness ranking

Group findings into these tiers:

### Tier 1 — Must read

Developments that are directly actionable for ateam, materially change unattended-agent operation, introduce a major architectural idea, or create an immediate policy, pricing, authentication, or compatibility risk.

A major feature from a major provider generally belongs here.

### Tier 2 — Worth studying

Credible adjacent work with useful architectural ideas, measurable efficiency improvements, or components that ateam might eventually adopt.

### Tier 3 — Weak signals and experiments

Early projects, prototypes, papers, or small tools that are not yet proven but contain a genuinely novel idea.

A basic agent wrapper or new pet project belongs here only if it introduces something technically distinctive.

Omit:

- Thin wrappers around existing agents
- Generic “AI developer” launches without technical substance
- Routine model-version support
- Minor UI improvements
- Unsubstantiated marketing claims
- Projects whose only novelty is combining existing tools

Rank based on:

- Relevance to ateam
- Magnitude of the change
- Technical novelty
- Evidence quality
- Project maturity or provider importance
- Potential actionability

## Output format

# Coding-agent revue de presse — YYYY-MM-DD

**Coverage:** START DATE–END DATE

## Operational alerts: Claude Code and Codex

Include only material changes. If none were found, say:

> No material subscription, authentication, permission, unattended-mode, quota, or telemetry changes found.

For each alert:

- **[Title](primary-source URL)** — Project · date  
  One short sentence describing the change. **Ateam relevance:** one short phrase.

## Tier 1 — Must read

- **[Title](primary-source URL)** — Project · date  
  Maximum 40-word summary. **Ateam relevance:** short phrase.

## Tier 2 — Worth studying

Use the same compact format.

## Tier 3 — Weak signals and experiments

Use the same compact format.

## Overall signal

Finish with no more than three bullets:

- The most important trend during this period
- The most actionable idea for ateam
- Any operational risk that should be monitored

## Length and quality constraints

- Prefer 6 strong findings over 20 weak ones.
- Maximum 15 findings unless the period was exceptionally active.
- Keep each item to two short lines.
- Do not add a general introduction.
- Do not summarize changelogs.
- Do not invent implications unsupported by the source.
- State uncertainty explicitly.
EOF

echo "Reports After:"
print_reports
