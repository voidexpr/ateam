# Approach

The [README](README.md) covers what ATeam is and shows how to use it. This document covers the *why*: what ATeam bets on, where it fits among other agent tools, and the reasoning behind its shape.

## Beyond feature work

Software engineering teams don't just ship features. Engineers refactor, fill test gaps, bump dependencies before they rot, and (ideally) keep docs honest. This work is well-defined and repetitive — it rarely needs a conversation. ATeam is designed to automate it so technical debt doesn't slow you down.

Coding agents are perfectly capable of doing this kind of work tirelessly. What they need is the surrounding plumbing: isolation, secrets, process tracking, prompts that travel across agents, cost visibility. ATeam provides that.

Interactive agents remain the better fit for work that benefits from conversation — shaping features, exploring approaches, answering "why." ATeam complements them rather than replaces them.

## Your attention is the bottleneck

Routine engineering work still needs someone to do it. Without automation it either piles up or competes with feature work for your focus. ATeam's bet is that handing this class of work to unattended agents — with enough safety, visibility, and cost control to trust them — frees attention for the decisions only you can make.

This shifts the human role: less code review on routine changes, more architectural direction. Spot-check intent on commits; review content only when something looks off.

## A direction, not a prediction

A growing share of code is written by coding agents. It seems reasonable that a growing share of the work *around* the code — review, tests, refactors, dependency hygiene, docs — will be done by agents too. ATeam is built around that split, on the assumption it will become more useful over time as coding agents improve and as token cost becomes a more central concern.

## Why a CLI, not a workflow framework

Coding agents already know how to plan, parallelize, and track their own work. That's where most agent frameworks compete and where coding-agent vendors are getting steadily better. ATeam doesn't try to duplicate that. It focuses on the parts that don't live inside the agent:

- running coding agents unattended without permission prompts, safely
- mixing agents (e.g. Codex reviews, Claude implements) and isolation modes (sandbox, Docker)
- observable, troubleshootable execution: logs, cost, process state
- prompt management across projects and orgs
- a stable artifact layout (markdown files) that travels across runs

This makes ATeam a building block. The four-stage quality pipeline is one workflow built on that block; you can build others with `ateam exec` and `ateam parallel`.

## Why focus on quality work

Two reasons:

1. **Quality work is a good unattended fit.** It can be prompted once (find bugs, write tests, audit dependencies, review structure) and repeated across projects. LLMs are well-trained for it. Failure modes are bounded: at worst, a finding is wrong or a fix is rejected on review.
2. **Feature development benefits more from conversation.** Specs are ambiguous, tradeoffs need discussion, iteration is fast. Interactive agents do this better than any orchestration layer can.

ATeam may add more workflow primitives over time. Feature development will likely stay in interactive agents.

## Tokens as the metric

Token usage and dollar cost are first-class metrics in ATeam — exposed per run, per role, per agent. Two reasons:

1. **Subscription subsidies are eroding.** Claude Code's pricing model changes mid-2026; others will follow. Running quality automation at API cost is fine if you can measure and tune it.
2. **Tokens are a new resource to optimize** — like CPU, memory, and latency before them, but paid during development rather than production. A pipeline that produces the same results in half the tokens is straightforwardly better.

Future work skews toward token efficiency: prompt evals, narrower task granularity instead of whole-file reports, caching code discovery between runs, surfacing tools (linters, test runners, vulnerability scanners) so agents don't redo deterministic work.

## On vocabulary

ATeam avoids colorful imagery — no swarms, fleets, crews, employees. The vocabulary is the concrete one: *agent*, *prompt*, *role*, *report*, *review*, *code*. Less fun but simpler and easier to remember.

## How ATeam compares to other agent frameworks

Most current agent frameworks focus on orchestrating feature development or generic multi-agent workflows. ATeam differs on three axes:

- **Unattended-first.** Execution is designed for runs you don't sit through — sandboxing, secret handling, cost tracking, troubleshooting. Many frameworks assume interactive supervision.
- **Quality-focused.** One built-in workflow (report → review → code → verify) along software-quality dimensions, rather than a generic graph of agents.
- **Reuses existing coding agents.** Claude Code, Codex, etc. — and their subscription pricing. Not a new agent runtime.

If you need a generic workflow orchestrator, ATeam isn't it. If you want unattended quality work driven by the agents you already use, it's a closer fit.

Most agent frameworks try to recreate human team structure with evocative terminology and focus on feature work. ATeam sees feature work as a task that benefits from interactive agents and fundamentally requires attention; the sweet spot for unattended agents is software engineering quality. So ATeam focuses on getting the fundamentals right: reliably and safely running unattended agents, re-executing and auditing saved prompts.

The basic primitive of running a prompt against any agent/container environment while capturing costs and logs is surprisingly versatile. The `scripts/` directory has examples of common code review and testing workflows expressed as simple bash scripts on top of ateam.

This is a rapidly evolving field with new frameworks being born every day; time will tell which approaches work best. ATeam explores one of them.

## What ATeam is not

- **Not a feature-development tool.** Use interactive agents.
- **Not a generic agent workflow framework.** `exec` and `parallel` are primitives, not a full DAG.
- **Not an agent runtime.** It drives Claude Code, Codex, and similar — it doesn't reimplement them.
- **Not a CI replacement.** It commits changes; you still need tests, review gates, and deployment automation around it.

## Open questions

Honest about what's uncertain:

- **Will repeated ATeam runs really substitute for parts of a quality engineering team?** Plausible, not proven. An ongoing experiment is to run ATeam on its own codebase with minimal human code review.
- **Files or tasks as the unit of stateful work?** Files (current) are simple and work for many flows. A finer-grained task system is on the roadmap; the right cut isn't obvious yet.
- **Which workflows beyond report/review/code/verify are worth building in?** Adversarial review and blackbox testing look promising. We're interested in hearing other patterns from users.

## Roadmap

Near-term:
- **0.9.0** — role refactoring and prompt evals to reduce token use and improve accuracy
- **1.0.0** — cleanup pass on CLI options, file layout, and database structure

Longer-term directions: an internal task system replacing file-as-unit, prompt templating with variable expansion and sandboxed shell, resumable workflows with dependency management, more agents (Gemini, Pi), more containers (macOS containers, Fence to sandbox over the agent in a uniform way), automatic failover across LLM providers, a persistent memory system for per-project knowledge.
