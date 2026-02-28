# Story Time

Based on my experience working on multiple projects with agents where I don't really touch the code:
* feature work with agents requires focused interactive sessions, it is hard to imagine orchestrating this work. If multiple features are developed in parallel it's just a matter of having multiple git checkouts of the project, each with its own agent and multi-task between them
* in order to maintain a healthy code base I frequently ask the coding agent to refactor recent code and then less frequently to refactor bigger parts of the code. It is essential to avoid hitting a wall where new features keep breaking the project. Tests are also important, internal docs, etc ...
* constant command approval is a drag (claude code mostly) and configuration tweaks aren't helping, giving permissions to run anything isn't feasible outside of containers
* code agents are usually very insightful auditing existing code bases for security, refactoring, testing, etc ... and they are of course great at coding
* if I'm not going to look at the code I want to outsource code reviews and maintenance of it as much as possible. I can always take control if I decide to
* features are best developed iteratively, modify how things should work as it shapes up
* coding agents are getting better at using sub-agents transparently for their work, no need to reinvent it. More generally coding agents are improving rapidly so we want to use them as much as possible

So the idea is to try to delegate well-defined tasks: software engineering maintenance. Just have a set of agents work after commits or over night to improve the quality of the project and leave feature work to interactive sessions. We use agents to check and improve the work of agents. We use containers so these agents can run with permissions to run any command and they mostly operate in non-interaction sessions.

So basically just run a cli and have a separate workspace focused on software engineer tasks without much supervision needed.

## How does it work ?
* doesn't require any configuration or to store its artifact in your project git repo, it is self contained and is essentially a separate contributor to your project
* for each project ateam works on:
  * role specific sub-agents focus on an aspect of engineering work: refactoring, testing, security, documentation, ... Each project can enable/disable, add/remove any pre-configured agent or add its own with just a prompt file to specify the mission
  * a coordinator agent to prioritize work between sub-agent
  * sub-agents only run one-shot tasks (claude -p) in a container on their own git work-tree and do one of:
    * generate a report of what they could be doing
    * implement a report (which could have been amended by the coordinator or a human)
  * the coordinator requests multiple reports and prioritize which ones to implement
  * when work is completed the sub-agent is instructed to maintain a summary of the project and what it's done about it so it maintains context
  * agents run within docker for isolation, the docker image needs to run only while the agent is active
* ateam is almost exclusively a CLI to orchestrate, setup, troubleshoot this system (and in the future starts a web interface)
  * markdown files are used for reports, sub-agent prompts. These files are managed in their own ateam specific per-proiect git repo. So git log is basically a teamline of the decision and work taken by ATeam.
  * a sqlite database is used to track the state of each sub-agent, which git commits they have seen so far
  * configuration (docker file)
* there is a notion of organization to reuse configuration between projects (defaults that each project can override)
  * sub-agents can create domain specific expertise between projects by create knowledge files (markdown) at the organization level

## Strategy
* simple architecture:
  * a cli
  * the same coding agent used for feature work is used for coordinator and sub-agent (claude, codex, ...)
  * the coordinator uses the main cli
  * maintain context as markdown that can be read and edited by both agents and humans.
  * use git to maintain this context to get versioning, history and timeline views
  * trivial agent communication and orchestration: one-shot execution, generate files. No IPC, no agent-to-agnt dialog
* agents are instructed to be conservative in the work they do and take into account the maturity and size of the code base they work on. A small code base doesn't need to overdo the software engineering but can certainly benefit from constant small and medium refactoring performed in the background
* try to be conservative in token usage and cost
* sub-agents are encouraged to add automation to the project for their respective areas: linter, automated smoke or integration tests, etc ... So less agent work is required over time.
* sub-agents are responsible for dealing with potential merge conflicts
* the coordinator agent can escalate decisions to humans to help with prioritizing, sub-agents either generate a report or perform it as a one-shot prompt
* at any point the ateam CLI can be used to create interactive sessions with
* flexible: can run ateam on any machine, can easily add new agents and instruct the coordinator to do things differently
* reduce cognitive load: using ateam is a single extra check-in of the code requiring little attention. Assuming proper dockerization of a project the only command required is either to push commits to the main repo or review changes auto-pushed by the coordinator depending on preference

Where many frameworks want you to design workflows and manage a lot of agents, the goal of ATeam is to require as little attention as possible. Just have the code be refactored in the background, test coverage improved, etc ... allowing developers to focus on the more interesting parts: feature work or directed software engineering tasks (refactoring, testing, docs, ...).

For feature development what I found works best is to focus on one feature at a time or have multiple checkout of a repository and multitask between them. In these scenarios it's not clear that additional automated orchestration would be beneficial, best to keep it simple.

## Architecture
The good way to understand Ateam is to look at its folder hierarchy and basic commands

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

The initial focus is on well defined engineering tasks that coding agents seem to understand well so needs less prompting and is therefore well suited for automation: bring the ateam to this project to take care of the quality.

But in the future some feature work could get in scope to help free more attention:
* tackle small issues from a bug tracker
* manage notifications from interactive sessions and organize them for remote access (be integrating with VibeTunnel or Claude remote capabilities), this would include interacting from a smartphone (and if agents containerized they can easily be spawned on-demand remotely)

## Conclusion

Maintaining code quality is essential in agentic coding, ATeam uses agents to help agents by performing quality engineering in the background. This way feature work can go further with less slow downs.
