# Overview

## Motivation

ATeam tackles the core issue of agentic coding: how to iterate fast on features while maintaining good software quality. We want to avoid quick feature turnaround resulting in a codebase that crumbles under its own complexity where any new feature breaks existing logic.

Agents are very good at finding issues in code and producing tests, so why should humans spend attention on a codebase they may never contribute to? Developers should focus on the big picture: what the project does and how features should work, not how the engineering is performed.

## Story Team

Based on my experience working on multiple projects with agents:
* I touch code less and less. I used to do a lot of code reviews but why review code I'm not going to modify myself? But I still need to do it to catch architecture issues.
* feature work with agents requires focused interactive sessions that are hard to orchestrate (even though that seems to be the focus of agent orchestration projects). If needed, developing multiple features in parallel is straightforward: use distinct work areas with interactive agents and multi-task between them.
* to maintain a healthy codebase I frequently ask the coding agent to refactor recent code and less frequently to refactor bigger parts. I also ask to update docs, tests, etc. Avoiding the wall where new features keep breaking the project is essential.
* constant command approval is a drag (Claude Code especially) and configuration tweaks don't help much. Giving permissions to run anything isn't feasible outside of containers.
* coding agents are very insightful when auditing codebases for security, refactoring, testing, etc. and they are great at coding too.
* if I'm not going to look at the code I want to outsource code reviews and maintenance as much as possible. I can always take control to focus on architectural aspects I think are important.
* features are best developed iteratively, adjusting how things work as they shape up. Software quality work is less interactive.
* coding agents are getting better at using sub-agents transparently, no need to reinvent that. More generally they are improving rapidly so we want to leverage them as much as possible.

So the idea is to delegate well-defined tasks: software engineering maintenance. Have a set of agents work after commits, on-demand or overnight to improve the quality of the project and leave feature work to interactive sessions. We use agents to check and improve the work of agents. We use containers so these agents can run with full permissions and they mostly operate in non-interactive sessions.

## How does it work?
* we want a system that works outside of our main repo so it can be used on any codebase in seconds. It manages its own work area and builds expertise over time while managing its context.
* for each project ATeam works on:
  * run role-specific sub-agents each focused on a specific aspect of engineering: refactoring, testing, security, documentation, ...
    * each project can enable/disable, add/remove any pre-configured agent or add its own with just a prompt file to specify the role
  * a coordinator agent prioritizes the work between sub-agents so we don't flood the project with busy work
  * sub-agents only run one-shot tasks (claude -p) in a container (with --dangerously-skip-permissions) on their own git worktree and do one of:
    * generate a report of what they could be doing
    * implement a report (which could have been amended by the coordinator or a human)
  * the coordinator requests multiple reports and prioritizes which ones to implement
  * when work is completed the sub-agent maintains a summary of the project and what it's done so it preserves context across runs
  * agents run inside Docker for isolation, the container only needs to run while the agent is active
* ATeam is almost exclusively a CLI to orchestrate, setup, and troubleshoot these agents (a web interface can eventually be added)
  * markdown files are used for reports, sub-agent prompts, and persistent knowledge between tasks. These files are managed in their own ATeam-specific per-project git repo. That repo's `git log` is a timeline of the decisions and work taken by ATeam.
  * a SQLite database tracks the state of each sub-agent and which git commits they have seen so far
  * minimal configuration (Dockerfile and some preferences about which coding agent to use, CLI args, etc. with reasonable defaults)
* there is a notion of organization to reuse configuration between projects (defaults that each project can override)
  * sub-agents can build domain-specific expertise across projects by creating knowledge files (markdown) at the organization level

## Approach

In short: claude code + a cli + isolated docker container + some markdown files + git

* existing coding agents (Claude Code, Codex, ...) are great, we don't want to reinvent our own, we want to reuse them as much as possible
* minimal architecture:
  * a CLI `ateam`
  * the same coding agent used for feature work is used for coordinator and sub-agents (Claude, Codex, ...)
  * the coordinator uses the same ATeam CLI as humans
  * sub-agents get domain-specific prompts (read-only) for their mission and maintain separate knowledge files about the project (read/write). All are markdown files.
    * use git to track these markdown files for versioning, an audit trail, and a timeline view
  * trivial agent communication and orchestration: one-shot execution, generate files. No IPC, no agent-to-agent dialog
* at any point the ateam CLI can be used to create interactive sessions with any agent to troubleshoot or do interactive work
* flexible: can run ateam on any machine, can easily add new agents and instruct the coordinator to do things differently
* docker is used to run coding agents with `--dangerously-skip-permissions`

## Strategy

* agents are instructed to be conservative and take into account the maturity and size of the codebase. A small codebase doesn't need heavy software engineering but can benefit from constant small and medium refactoring in the background.
  * it's ok to be lazy, value comes from constant incremental improvements
* be conservative in token usage and cost (actively report on it)
* sub-agents are encouraged to add automation to the project for their areas: linters, automated smoke or integration tests, etc.
  * so less agent work is required over time
* sub-agents are responsible for dealing with potential merge conflicts
* the coordinator agent acts as a second level of review, prioritizing with the project's best interest in mind vs. sub-agents each focused on a specific domain (to maximize domain context)
* only the coordinator can escalate decisions to humans if they need help with prioritizing. Sub-agents either generate a report or implement one as a one-shot prompt (they can't notify a human).
* reduce cognitive load: using ATeam is a single extra check-in requiring little attention. Assuming proper dockerization (which is an agent prompt away for mpst projects), the only command needed is to push commits to the main repo or review changes auto-pushed by the coordinator depending on preference.

Where many frameworks want developers to design workflows to manage agents, ATeam's goal is to require as little attention as possible. Have code refactored in the background, test coverage improved, etc. allowing developers to focus on the interesting parts: feature work or directed engineering tasks.

For feature development what works best is to focus on one feature at a time. ATeam does everything else in the background as if the main coding agent were not just implementing features but an entire software engineering team that can scale up as needed or do small changes for small projects.

## Architecture

A good way to understand ATeam's mental model is to look at its folder hierarchy and basic commands.

Core terminology:
* **organization**: collection of projects, accumulated knowledge between projects, global state of all agents for all projects (shared by coordinators)
* **project**: a bare git checkout of the project to work on plus coordinator and sub-agent prompts, knowledge and reports. These ateam artifacts are managed in their own per-project git repo.
* **coordinator**: each project has a coordinator who doesn't change code but manages domain-specific sub-agents (instance of Claude Code)
* **sub-agent**: role/domain-specific agent (instance of Claude Code that runs in a container one-shot), each project can have 1 or more

### File Structure
```
my_org/

  .ateam/            # Base config created by the ateam CLI for the entire org

    ateam.sqlite     # maintain state of all agents of all projects in one spot
                     # (tables: agent_status, agent_history, reports_history)
    config.toml      # default config projects can inherit
    agents/          # define reusable role specific sub-agents
      agent_x/
        prompt.md    # what the agent is supposed to do
    knowledge/       # where cross-project knowledge goes, automatically
                     # aggregated by ateam CLI actions

  my_project_1       # Created by `ateam init my_project_1 --git URL`
    .git/            # to version ateam's own artifacts (agent config, reports, etc ...)
    config.toml      # project specific config
    Dockerfile       # environment to run sub-agents in
    docker.sh

    bare.git/        # checkout of the actual code to work on (i.e. my_project_1)

    coordinator/
      prompt.md      # read-only: instructions for the coordinator
      project_overview.md  # read-write: context the coordinator maintains
                           # about the project: how it's structured
      project_goals.md     # read-write: context the coordinator maintains
                           # about the project: maintain goals based on the
                           # project size, maturity, ...
      decisions/
        YYYY-MM.md   # where the coordinator documents its rationale when
                     # deciding on priorities based on sub-agents reports
      sessions/      # keep session logs for the coordinator
        YYYY-MM-DD_HHMMSS.jsonl

    agents/
      agent_x/
        extra_prompt.md  # read-only: can just add instructions to the org-level
                         # default per-agent prompt
        knowledge.md     # read-write: often update automatically between tasks
        code/            # checkout from the bare repo
          .git           # worktree
        reports/         # keep all reports as markdown and track if they are implementation or not
          YYYY-MM-DD-report_title.report.md
          YYYY-MM-DD-report_title.report.session.jsonl
          YYYY-MM-DD-report_title.impl.md
          YYYY-MM-DD-report_title.impl.session.jsonl
```

### Commands

Just like git figures out repo context based on working directory, ATeam commands figure out their org, project, and agent based on which directory they are in (or explicitly via --org, --project, --agent).

Most of the time it's really just:

```bash
# Setup a new project
  # go to the ateam organization folder
  cd ~/ateam_projects

  # create a working folder for the new project
  ateam init project_foobar --git URL
  cd project_foobar

# Day-to-day commands
ateam run    # maybe runs for a few hours at night by default,
             # this just schedules it or this could trigger a one-time run on-demand
ateam review # see what ateam sub-agents have been up to (mix of coordinator
             # decisions and git commit against the project git repo)
ateam push   # contribute their work to the main git repo
```

Here are more commands:

```bash
mkdir my_org && cd my_org

# Create an org to host projects, create claude oauth token to use in unattended claude sessions
ateam init-org --agent cmd

# Create a project
#   --auto-dockerize runs an agent prompt to look at the project and try to
#                    come up with the proper isolated docker container to run
#                    sub-agents to perform dev tasks
ateam init my_project_1 --git URL --agent refactor,test,user-docs --auto-dockerize

cd my_project_1

# run the coordinator
ateam run [--once | --at-commit | --every DUR | --schedule START_TIME:END_TIME]

# make sure everything is properly configured, create coding agent credentials if needed
ateam audit

# see what agents have been contributing to the project
ateam review

# chat with the coordinator agent (just an instance of claude code with some
# extra context like the ateam CLI to control other agents and some associated skills)
ateam chat

# chat with a specific agent
cd agents/agent_x && ateam chat
# or:
ateam chat --agent agent_x

# see what agents are running and how up to date they are
ateam status

# see status across all projects by running the same command in the organization folder
cd .. && ateam status

# review recent work
git log

# a report centric status: which reports were acted on or not, why and the eventual git commit
ateam reports

# review ateam changes for the main repo
ateam git log
# same as
cd bare.git && git log

# push all ateam changes to the project repo
ateam push
# same as
cd bare.git && git fetch --all && git rebase && git push

# Web dashboard (future release)
ateam ui

# run an ad-hoc agent and review the work before committing and pushing
ateam run --prompt "Please check if we could improve setup scripts" \
  --new-agent --agent master_automator \
  --agent-prompt "You can't stand running manual commands" \
  --implement --no-commit --allow WebSearch
# enter a docker image to experiment with the new scripts
ateam shell --agent master_automator
# commit the work and contribute to the main repo
cd agents/testing/code && git commit -m "improved setup scripts"
# contribute back to the bare git repo and main repo, update agent status
ateam push

# same but ask the supervisor to oversee
ateam run --prompt "Spawn an ad-hoc agent called master_automator who hates
  running manual commands, it should improve setup scripts if it makes sense.
  It can perform web searches and implement its recommendation directly.
  Verify its work in its workspace and if it looks good commit and push"
```

ATeam can run on separate build servers or on the same machines.

## Future

The initial focus is on well-defined engineering tasks that coding agents understand well. They need less prompting than features, making them well suited for automation: bring ATeam to a project and let it take care of quality.

In the future some feature work could come in scope to free more attention:
* tackle small issues from a bug tracker (pick the top bug reports or small features and try to one-shot them, have the coordinator do a first pass to assess quality and relevance)
* manage notifications from interactive sessions and organize them for remote access (by integrating with VibeTunnel or Claude remote capabilities), including interacting from a smartphone (containerized agents can easily be spawned on-demand remotely). This is a different focus but fits well with remote sessions since it also requires container isolation.

## Conclusion

Maintaining code quality is essential in agentic coding. ATeam uses agents to help agents by performing quality engineering in the background. This way feature work can go further with fewer slowdowns.
