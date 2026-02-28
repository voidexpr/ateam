# Overview

## Motivation

ATeam is an attempt at tackling the core issue of agentic coding: how to iterate fast on features while maintaining good software quality. We want to avoid that quick feature turnaround results into a code base that crumbles under its own complexity and any new feature breaks existing logic.

Agents are actually very good at finding issues in code or produce tests so why would humans have to spend attentions to a code base they may never contribute to ? Developers should direct all their attention to the big picture aspects of a project and how features should work, not how the engineering is performed.

## Story Team

Based on my experience working on multiple projects with agents:
* I touch code less and less, I used to do a lot of code reviews but why review the code if I'm not going to modify it myself ? I still need to do it to try to catch software architecture issues.
* feature work with agents requires focused interactive sessions, it is hard to imagine orchestrating this work (even though it seems to be the focus of agent orchestration projects in this area). If needed, it is relatively straightforward to develop multiple features in parallel by using distinct work areas with interactive agents and multi-task between them.
* in order to maintain a healthy code base I frequently ask the coding agent to refactor recent code and then less frequently to refactor bigger parts of the code. I also ask to update docs, tests, etc .. It is essential to avoid hitting a wall where new features keep breaking the project.
* constant command approval is a drag (Claude code especially) and configuration tweaks aren't helping, giving permissions to run anything isn't feasible outside of containers
* code agents are usually very insightful when auditing existing code bases for security, refactoring, testing, etc ... and they are of course great at coding too
* if I'm not going to look at the code I want to outsource code reviews and maintenance of it as much as possible. I can always take control if I decide to focus on an architectural aspect I think is important.
* features are best developed iteratively, modify how things should work as it shapes up. Software quality is less interactive
* coding agents are getting better at using sub-agents transparently for their tasks, no need to reinvent it. More generally coding agents are improving rapidly so we want to use them as much as possible

So the idea is to try to delegate well-defined tasks: software engineering maintenance. Just have a set of agents work after commits or over night to improve the quality of the project and leave feature work to interactive sessions. We use agents to check and improve the work of agents. We use containers so these agents can run with permissions to run any command and they mostly operate in non-interaction sessions.

## How does it work ?
* we want a system that works outside of our main repo so it can easily be used in seconds on any code base. It should manage its own work area, build expertise over time while managing its context
* for each project ateam works on:
  * run role specific sub-agents each focus on a specific aspect of engineering work: refactoring, testing, security, documentation, ...
    * each project can enable/disable, add/remove any pre-configured agent or add its own with just a prompt file to specify the mission
  * a coordinator agent prioritizes the work between sub-agent so we don't flood our project with agent busy work
  * sub-agents only run one-shot tasks (claude -p) in a container (with --dangerously-skip-permissions) on their own git work-tree and do one of:
    * generate a report of what they could be doing
    * implement a report (which could have been amended by the coordinator or a human)
  * the coordinator requests multiple reports and prioritize which ones to implement
  * when work is completed the sub-agent is instructed to maintain a summary of the project and what it's done about it so it maintains context
  * agents run inside docker for isolation, the docker image needs to run only while the agent is active
* ateam is almost exclusively a CLI to orchestrate, setup, troubleshoot these agents (and in the future starts a web interface)
  * markdown files are used for reports, sub-agent prompts, persistent knowledge between tasks. These files are managed in their own ateam specific per-proiect git repo. So that repo's `git log` is basically a teamline of the decision and work taken by ATeam.
  * a sqlite database is used to track the state of each sub-agent, which git commits they have seen so far
  * minimal configuration (docker file and some preferences about which coding agent to use, CLI args, etc ... with reasonable defaults)
* there is a notion of organization to reuse configuration between projects (defaults that each project can override)
  * sub-agents can create domain specific expertise between projects by create knowledge files (markdown) at the organization level

## Approach

In short: claude code + a cli + some markdown files + git

* existing coding agents (Claude code, codex, ...) are great, we don't want to reinvent our own, we want to reuse them as much as possible
* minimal architecture:
  * a cli `ateam`
  * the same coding agent used for feature work is used for coordinator and sub-agent (claude, codex, ...)
  * the coordinator uses the main ateam cli than humans
  * sub-agents get domain specific prompts (read-only) to give them their mission, they also maintain knowledge files about the project (read/write). All are markdown files.
    * use git to track these markdown files to get versioning, an audit trail and a timeline views
  * trivial agent communication and orchestration: one-shot execution, generate files. No IPC, no agent-to-agent dialog
* at any point the ateam CLI can be used to create interactive sessions with any agent to troubleshoot or do interactive work
* flexible: can run ateam on any machine, can easily add new agents and instruct the coordinator to do things differently

## Strategy

* agents are instructed to be conservative in the work they do and take into account the maturity and size of the code base they work on. A small code base doesn't need to overdo the software engineering but can certainly benefit from constant small and medium refactoring performed in the background
  * it's ok to be lazy, value can be created by constant incremental improvements
* try to be conservative in token usage and cost (actively report on it)
* sub-agents are encouraged to add automation to the project for their respective areas: linter, automated smoke or integration tests, etc ...
  * so less agent work is required over time.
* sub-agents are responsible for dealing with potential merge conflicts
* the coordinator agent acts as a second level of review to prioritize with the project as a whole's best interest vs. sub-agent looking at a specific domain each (to maximize specific context)
* only the coordinator agent can escalate decisions to humans to help with prioritizing, sub-agents either generate a report or implement a report as a one-shot prompt (they can't notify a human)
* reduce cognitive load: using ateam is a single extra check-in of the code requiring little attention. Assuming proper dockerization of a project the only command required is either to push commits to the main repo or review changes auto-pushed by the coordinator depending on preference

Where many frameworks want developers to design workflows to manage a lot of agents, the goal of ATeam is to require as little attention as possible. Just have the code be refactored in the background, test coverage improved, etc ... allowing developers to focus on the more interesting parts: feature work or directed software engineering tasks (refactoring, testing, docs, ...).

For feature development what seems to work best is to focus on one feature at a time, ateam is designed to do everything else in the background as-if the main coding agent would not only implement the feature requests but be an entire software engineering team that can get as busy as required or do small scale changes for small projects.

## Architecture

The good way to understand Ateam's mental model is to look at its folder hierarchy and basic commands.

### File Structure
```
my_org/

  .ateam/
    ateam.sqlite     # maintain state of all agents of all projects in one spot (tables: agent_status, agent_history, reports_history)
    config.toml      # default config projects can inherit
    agents/          # define reusable role specific sub-agents
      agent_x/
        prompt.md    # what the agents is supposed to do
    expertise/       # where cross-project knowledge goes

  my_project_1
    .git/            # to version ateam's own artifacts (agent config, reports, etc ...)
    config.toml      # project specific config
    Dockerfile       # environment to run sub-agents in
    docker.sh

    bare.git/        # checkout of the actual code to work on (i.e. my_project_1)

    coordinator/
      prompt.md      # read-only: instructions for the coordinator
      project.md     # read-write: context the coordinator maintains about the project
      sessions/      # keep session logs for the coordinator
        YYYY-MM-DD_HHMMSS.jsonl

    agents/
      agent_x/
        extr_prompt.md   # read-only: can just add instructions to the org-level default per-agent prompt
        mission.md       # read-write: often update automatically between tasks
        code/            # checkout from the bare repo
          .git           # worktree
        reports/         # keep all reports as markdown and track if they are implementation or not
          YYYY-MM-DD-report_title.report.md
          YYYY-MM-DD-report_title.report.session.jsonl
          YYYY-MM-DD-report_title.impl.md
          YYYY-MM-DD-report_title.impl.session.jsonl
```

### Commands

```bash
# Create an org to host projects, create claude oauth token to use in unattended claude sessions
ateam init-org --agent cmd

# Create a project
#   --auto-dockerize runs an agent prompt to look at the project and try to come up with the proper isolated docker container to run sub-agents to perform dev tasks
ateam init my_project_1 --git URL --agent refactor,test,user-docs --auto-dockerize

cd my_project_1

# run the coordinattor
ateam run [--once | --at-commit | --every DUR | --schedule START_TIME:END_TIME]

# make sure everything is properly configured
ateam audit

# chat with the coordinator by spawning a container and running claude with proper prompt and context
ateam chat

# chat with a specific agent
cd agents/agent_x && ateam chat

# see what agents are running and how up to date they are
ateam status

# see status across all projects by running the same command in the organization folder
cd .. && ateam status

# review recent work
git log

# a report centric status: which reports were acted on or not and when + git commit
ateam reports

# push changes to the project repo
ateam push

# Web dashboard
ateam ui

# run an ad-hoc agent and review the work before committing and pushing
ateam run --prompt "Please check if we could improve setup scripts" \
  --new-agent --agent master_automator --agent-prompt "You can't stand running manual commands" \
  --implement --no-commit --allow WebSearch
# enter a docker image to experiment with the new scripts
ateam shell --agent master_automator
# commit the work and contribute to the main repo
cd agents/testing/code && git commit -m "improved setup scripts"
# contribute back to the bare git repo and main repo, update agent status
ateam push

# same but ask the supervisor to oversee
ateam run --prompt "Spawn an ad-hoc agent called master_automator who hates running manual commands, it should improve setup scripts if it makes sense. It can perform web searches and implement its recommendation directly.Verify its work in its workspace and if it looks good commit and push"
```

ATeam can run on separate build servers or on the same machines

## Future

The initial focus is on well defined engineering tasks that coding agents seem to understand well. They need less prompting than for features, it is therefore well suited for automation: bring the ateam to this project to take care of the quality.

But in the future some feature work could get in scope to help free more attention:
* tackle small issues from a bug tracker (pick the top few bug reports or small features and try to one-shot them, have the supervisor to a first pass to assess their quality and relevance)
* manage notifications from interactive sessions and organize them for remote access (be integrating with VibeTunnel or Claude remote capabilities), this would include interacting from a smartphone (and if agents containerized they can easily be spawned on-demand remotely). It is a different tool focus but could fit well remote sessions because it also requires container isolation.

## Conclusion

Maintaining code quality is essential in agentic coding, ATeam uses agents to help agents by performing quality engineering in the background. This way feature work can go further with less slow downs.
