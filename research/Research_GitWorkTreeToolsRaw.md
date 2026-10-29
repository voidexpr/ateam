Here are recent (mostly 2024–2026) open-source GitHub projects specifically aimed at making parallel work with git worktree easier, especially for multi-task or multi-agent development. I grouped them by how close they are to your use case (parallel agents / automation).

⸻

1. Projects explicitly designed for parallel agents / automation

These are closest to the workflows you described (agents in worktrees, automation, orchestration).

1️⃣ Worktrunk
    •   GitHub: max-sixty/worktrunk
    •   Focus: parallel AI agents using git worktrees
    •   Language: Go
    •   Stars: small but purpose-built

Key ideas:
    •   Treat worktrees almost like branches
    •   CLI with few commands to spawn and manage worktrees
    •   Hooks for automation

“CLI for git worktree management, designed for running AI agents in parallel.”  ￼

Typical pattern:

worktrunk new feature-auth
worktrunk list
worktrunk remove feature-auth

This is one of the few tools explicitly designed for agent workflows.

⸻

2️⃣ git-worktree-runner
    •   GitHub: coderabbitai/git-worktree-runner
    •   Very recent (active as of 2026)
    •   Bash-based automation tool

Focus:
    •   create worktree per branch
    •   install dependencies automatically
    •   configure environment for agents

Features:
    •   automated worktree creation
    •   per-branch setup
    •   tool/editor integration

Automates “per-branch worktree creation, configuration copying, dependency installation.”  ￼

This is closer to a CI/agent orchestration helper than a pure worktree CLI.

⸻

3️⃣ ccswarm
    •   GitHub: nwiizo/ccswarm
    •   Purpose: multi-agent orchestration using worktrees

Key concept:
    •   each agent → its own worktree
    •   agents collaborate on the same repo

“Multi-agent orchestration system using Claude Code with Git worktree isolation.”  ￼

This one is interesting if you’re building something like your Ateam agent system.

⸻

4️⃣ agenttools/worktree
    •   GitHub: agenttools/worktree
    •   Focus: GitHub issues → worktree per task

Features:
    •   auto worktree for issues
    •   Claude Code integration
    •   agent workflow hooks

Creates isolated workspaces for each issue with automation.  ￼

⸻

2. Modern general worktree managers

These simplify worktree management but are not specifically AI-agent oriented.

⸻

5️⃣ Kosho
    •   GitHub: carlsverre/kosho
    •   Language: Go

Idea:
    •   create .kosho/ directory with managed worktrees
    •   quick switching
    •   lightweight

Manages git worktrees to enable concurrent development environments.  ￼

⸻

6️⃣ Branchlet
    •   GitHub: raghavpillai/branchlet
    •   Interactive CLI (Node)

Features:
    •   quick jump to worktrees
    •   interactive commands

Interactive CLI tool for creating and managing Git worktrees.  ￼

⸻

7️⃣ gwq
    •   GitHub: d-kuro/gwq

Focus:
    •   fuzzy-finder UI
    •   easy switching

Provides intuitive operations for creating and switching worktrees.  ￼

⸻

8️⃣ wtp (Worktree Plus)
    •   GitHub: satococoa/wtp

Adds:
    •   automated setup
    •   branch tracking
    •   hooks

Extends git worktree functionality with automated setup and hooks.  ￼

⸻

3. Tools focused on IDE / developer UX

Not agent-focused but useful.

⸻

VSCode Git Worktree Manager
    •   GitHub: jackiotyu/git-worktree-manager
    •   VSCode extension

Features:
    •   create / delete worktrees
    •   open them in editor
    •   manage multiple workspaces

Makes parallel development easier by working on multiple branches simultaneously.  ￼

⸻

4. Why these tools exist now

There’s a recent surge in these tools because of AI coding agents.

Git worktrees are becoming the standard isolation mechanism for agents so they don’t modify the same working directory simultaneously.  ￼

Typical structure used:

repo/
repo-feature-a/
repo-bugfix-b/
repo-refactor/

Each agent runs inside one worktree.

⸻

5. The most interesting projects for your use case

Given your agent orchestration + Docker + worktree model, these are the most relevant:

Project Why interesting
Worktrunk   purpose-built for AI agent parallelism
git-worktree-runner automates environment setup
ccswarm actual multi-agent orchestration
agenttools/worktree integrates worktrees with issues/tasks
Kosho   simple structured worktree layout


⸻

💡 My quick observation after looking at these repos:

Almost all tools are missing three things you care about:
    1.  locking / concurrency protection
    2.  agent lifecycle management
    3.  merge / review automation

Most only manage the filesystem layout.

⸻

✅ If you want, I can also show you 5 very experimental projects that try to orchestrate AI agents + worktrees + Docker together, which are closer to what you seem to be building with your Ateam system.