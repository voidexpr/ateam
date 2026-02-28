# Ateam

## Story time

Based on my experience working on multiple projects with agents where I don't really touch the code:
* feature work with agents requires focused interaction sessions, it's hard to image orchestrating this work. If multiple features are developped in parallel it's just a matter of having multiple git checkouts of the project, each with its own agent and multi-task between them
* in order to maintain a healthy code base I frequently ask the coding agent to refactor recent code and then less frequently to refactor bigger part of the code. It is essential to avoid hitting a wall: new features keep breaking the project. Tests are also important, internal docs, etc ...
* constant command approval is a drag (claude code mostly) and configuration tweaks aren't helping, giving permissions to run anything isn't feasible outside of containers
* code agents are usually very insightful auditing existing code bases for security, refactoring, testing, etc ...

So the idea is to try to delegate well-defined tasks: software engineering maintenance. Just have a set of agents work after commits or over night to improve the quality of the project and leave feature work to interactive sessions. We use agents to check and improve the work of agents. We use containers so these agents can run with permissions to run any command and they mostly operate in non-interaction sessions.

So basically just run a cli and have a separate workspace focused on software engineer tasks without much supervision needed.

This CLI is called ateam

How does it work ?
* for each project ateam works on:
  * role specific sub-agents focus on an aspect of engineering work: refactoring, testing, security, documentation, ... Each project can enable/disable, add/remove any pre-configured agent or add its own with just a prompt file to specify the mission
  * a coordinator agent to prioritize work between sub-agent
  * sub-agents only run one-shot tasks (claude -p) in a container on their own git work-tree and do one of:
    * generate a report of what they could be doing
    * implement a report (which could have been amended by the coordinator or a human)
  * supervisor requests multiple reports and prioritize which ones to implement
  * when work is completed the sub-agent is instructed to maintain a summary of the project and what it's done about it so it maintains context
  * agents run within docker for isolation, the docker image needs to run only while the agent is active
* ateam is almost exclusively a CLI to orchestrate, setup, troubleshoot this system (and in the future starts a web interface)
  * markdown files are used for reports, sub-agent prompts. These files are managed in their own ateam specific per-proiect git repo. So git log is basically a teamline of the decision and work taken by ATeam.
  * a sqlite database is used to track the state of each sub-agent, which git commits they have seen so far
  * configuration (docker file)
* there is a notion of organization to reuse configuration between projects (defaults that each project can override)
  * sub-agents can create domain specific expertise between projects by create knowledge files (markdown) at the organization level

Strategy
* simple architecture: a cli, claude for coordinator and sub-agent, the coordinator uses the same cli. Maintain context as markdown that can be read and edited by both agents and humas. Use git to maintain this context to get versioning, history and timeline views
  * trivial agent communication and orchestration
* agents are instructed to be conservative in the work they do and take into account the maturiy and size of the code base they work on. A small code base doesn't need to overdo the software engineering but can certainly benefit from constant small and medium refactoring performed in the background
* try to be conservative in token usage and cost
* sub-agents are encouraged to add automation to the project for their respective areas: linter, automated smoke or integration tests, etc ... So less agent work is required over time.
* sub-agents are responsible for dealing with potential merge conflicts
* the coordinator agent can escalate decisions to humans to help with prioritizing, sub-agents either generate a report or perform it as a one-shot prompt
* at any point the ateam CLI can be used to create interactive sessions with
* reduce cognitive load: using ateam is a single extra check-in of the code requiring little attention. Assuming proper dockerization of a project the only command required is either to push commits to the main repo or review changes auto-pushed by the coordinator depending on preference

Where many frameworks want you to design workflows and manage a lot of agents, the goal of ATeam is to require as little attention as possible. Just have the code be refactored in the background, test coverage improved, etc ... allowing developers to focus on the more interesting parts: feature work or directed software engineering tasks (refactoring, testing, docs, ...).

For feature development what I found works best is to focus on one feature at a time or have multiple checkout of a repository and multitask between them. In these scenarios it's not clear that additional automated orchestration would be beneficial, best to keep it simple.

The best way to understand Ateam and have the proper mental model is to look at its folder hierarchy and basic commands

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

    # chat with the cordinator by spawning a container and running claude with proper prompt and context
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
```

### v0

This is my story leading to ATeam: I find that working with agents require a lot of attention in burst and interaction is absolutely require to help shape a specific feature. But the constant approval of minor commands is a drag. Over time code tends to decay so I often ask the coding agent to refactor recent changes before I might even review them. Then less frequently I ask the agent to find and perform bigger refactoring, add tests, review security audit, do some performance optimizations, update documents (internal and external(, etc ... It's time consuming to ask for these tasks, then manage them to completion. And they could be happened asynchronously on every commit or at time or when development is not occurring. Lastly expertise and overview of the code from a particular perspective (architecture, security, etc ...) gets lost as features need to their own context. Not the speak about cross project knwoledge that is not passed along.

So the idea is to have one or more agents working on the side and doing all these well defined tasks on their own with full command approval so no interaction is needed. Then these changes are pulled in to the main repos (after these agents go into the trouble of merging and solving potential conflict from feature work). For this to work we must use containers and add a level of orchestration. Also deciding what to do next from these well defined tasks can be delegated to an agent itself. Thus ATeam:
* coordinator agent
* sub-agent with specific roles (refactoring, testing, security audit, documentation, ...)
* docker for isolation
* claude code or codex as the coding agent
* A CLI to tie all these pieces together, the coordinator uses this CLI by itself
* the CLI maintains a sqlite database to track what each agent is up and where they have caught up with the current code (by tracking git commit seen)
* then there is a layer of markdown files to provide agent role background and also have the agents maintain their own knowledge about the project. Then as they do work they merge their new findings with the old and summarize it
* git is used to track agent work, it can provide a simple timeline of decisions
* sub-agents work in one-shot sessions (claude -p) in 2 modes:
  * produce a report of what that agent could be doing
  * implement a report
* the coordinator agent schedules reports, looks at a few and prioritize which one to do

This is called ATeam, for a given project just have an ATeam instance on its own checkout of your project and have it automatically push its changes or review the local commits and push yourself. If you need to debug anything use the same CLI as the coordinator to start/stop/chat with any agent or the coordinator itself.

Where many frameworks want you to design workflows and manage a lot of agents, the goal of ATeam is to require as little attention as possible. Just have the code be refactored in the background, test coverage improved, etc ...

For feature development what I found works best is to focus on one feature at a time or have multiple checkout of a repository and multitask between them. In these scenarios it's not clear that additional automated orchestration would be beneficial.

The name: bring the a-team (Agent Team) to your project to help do the unglamorous.

## Mental model

ATeam does a checkout of your project to work on using role specific agents (refactoring, testing, ...) managed by a coordinator agent. It wraps the checkout it its own directory with the state required to coordinate these agents (role, project specific memories, state of agents). This internal directory uses its own local git repo to provide a full history of changes.

To your project ATeam appears like a single extra coloborator so the management overhead is minimal. Internally ATean uses per agent role git work trees to isolate and potentially parallelize the work but the coordinator aggregates all the work done and can leave it up to you to push the changes manually, automatically or just discard them.

So the mental model is to have an extra collaborator on your repo focus on the engineering itself. Because it is a well defined task the attention required should be minimal. This way you can focus on feature work where attention is required.

The basic idea is to use agents to fix the code of other agents so the quality remains good and adding new features don't start breaking the project left and right.

## First prompt

We want to design an agent coordination framework to use in software projects to perform the boring but necessary engineering tasks in the background: code quality, architecture integrity, testing, performance, security, internal and external documentation. To allow human developers to focus on feature work using their own agents. Then either at night or during low work periods this framework would relentlessly improve the software quality with minimal cognitive overhead to humans.

We want to build project specific knowledge (and potentially cross project cultural knowledge too), have agents configure tools to reduce the amount of future audits they need to perform. Most of the work should require no human intervention but some approval can be needed for bigger tasks or to resolve priority conflicts.

The working name is ATeam (like the old 1980s sitcom and because Agent-Team). Let's bring the ATeam to our project to improve the quality

## Goals

The goals are to automate the boring but important tasks of:
* Code refactoring, make sure abstraction layers are followed, good software architecture, code review all changes and improve them without touching other parts of the systems. Once in a while look at the big picture and recommend and perform bigger refactoring to reduce coupling and sources of bugs
* Testing
* Performance audits and improvements
* Security audits and improvements
* Dependency manager: make sure dependencies stay up to date for security vulnerabilities, try to remove unused dependencies, try to refactor lightly used dependencies into a project library to reduce the overhead of having dependencies, recommend other dependencies if the project is outgrowing a dependency. So both simplify and improve dependencies
* Internal documentation (architecture, code structure, development info)
* External documentation (overview, feature details, usage, installation, how to do local development)

So the main developers of this project can focus their work on feature work.

Additional goals:
* avoid the humans to be overwhelmed by agent activity
* avoid wasteful usage of tokens to just do busy work

## How would it work

For that we want a coordinator agent to interact with dedicated sub-agents.

Sub-agents:
* have a system prompt to give them their specialty and mission
* have some carried over knowledge about their past missions and project overall understanding
* work via one-shot prompts (claude -p to start with)
* perform either:
  1. produce a report of what should be done
  2. given a report (that could have been amended by the coordinator or a human) implement all the changes within docker with permissions to run anything
  3. based on last implement report and a current mission overview document maintain it by summarizing important knowledge to carry forward to other missions
  4. select and configure a relevant linter style tool for the project that will be integrated in the build system so future code changes require less audits/work
* sub-agents can also rebase their changes and fix git conflicts and then push

Coordinator agent
* as an interactive session with a human
* also acts on a schedule:
  * if there are new code commits always run the testing agent before doing anything else. As long as the build is not clean nothing else matters
  * after enough changes have been made, major features or enough time do:
    * run some of the sub-agents and generate a report
    * look at multiple reports and prioritize which ones to implement or wait
    * is allowed to amend to reports or answer open questions in the reports
    * if there are enough ambiguity, conflicting priorities them prompt the human with a summary
    * executes sub-agents within docker to perform a report and have the agent configure tools and maintain their mission file
  * track all the reports in a file hierarchy
  * track all the decisions in a markdown formatted log

So for each sub-agent with have the following files:

  my_projects/             # directory where to put all projects
    agents/
      coordinator_role.md   # default coordinator prompt
      SUB_AGENT_X/
        role.md              # default role file
        culture.md           # in the future add a feature to maintain cross project knowledge (preferred approaches, preferred tools for each programming language, ...)
    PROJECT_NAME/
      config.cfg           # git repo to use as a git tree, list of sub-agents that are enabled (it's possible to change it dynamically), token limits or other resource limits, how often should the coordinator take action
      Dockerfile           # docker image to use for sub agents in this project
      docker_run.sh        # script to run docker to execute a sub agent
      coordinator_role.md  # description of the coordinator role
      project_goals.md  # project specific instructions for the coordinator
      changelog.md  # where the coordinator records its decisions and requests to other agents
      SUB_AGENT_X/ # one director per sub agent
        role.md  # description of the goals of the agent
        knowledge.md  # project specific knwoledge to carry over tasks
        work/
          YYYY-MM-DD_HHMM_report.md  # specific report
          YYYY-MM-DD_HHMM_report_completion.md  # summary of what was done for that specific report

## Next Steps

Please create a markdown spec called ATeamDesign.md that is complete enough to implement this project:
* select an appropriate programming language amount: shell (ok to start with if simple enough), Golang, python, typescript
* note that sub-agents should be able to perform complex coding + testing + debugging tasks unattended so it must be done within a container, we have chosen Docker for that.
* I think it makes sense to have a git clone repo accessible by the coordinator agent and sub-agent get a work tree out of that repo
  * the main cloned repo remains under human control who decides when to rebase or push. Either by issuing git commands or by asking the coordinator to do it
  * when new changes are rebased the coordinator can ask specific sub-agent to resolve merge conflicts if any occur (only agents can run project specific commands, the coordinator has access to the source code but doesn't modify it besides some git commands)
* recommend an architecture
  * simple to start with
  * ideally can support parallel execution of sub-agents
  * how to implement schedule based execution of the coordinator agent (is it even possible using claude code ? or should it require a custom agent using ANTHROPIC API Key ?)
  * does claude -p work for sub-agents and allow the coordinator to get all the feedback they need ? Can sub-agent perform complex coding and debugging tasks like this ? if not how to do it ?
  * how to implement token awareness so we track how much tokens are consumed by these background tasks ? add a simple system to avoid using too many by slowing down this work (ideally we would monitor the account's progress toward Anthropic's limits)
  * ideally we'd like to use either claude code or OpenAI Codex (or Gemini in the future) and mix and match them based
  * we want to build a reliable system that can detect claude instances that consume too many resources and in general keeps an eye on CPU and throttles down the work to do if CPU usage is high.
  * maybe include a schedule config: be more aggressive running these background tasks during the night (say 11pm-5am) and less during the day
    * but have a user level instruction to ask the coordinator to focus on specific aspects (for example: "I completed a big new feature, focus on regression testing and then add additional test coverage. Then this feature might have security implication so that would be the next focus")

Let me know if you have any questions.

## Prompts

### Coordinator

You are a software project manager, you are organized and pragmatic. You will coordinate the work of sub-agents, decide what to do next and coordinate with the human you chat with. They are very busy working on features for this project so focus on engineering quality tasks: code refactoring, security, performance, documentation, regression testing ... For each area you have access to specialist sub agents, check config.toml to see who is available.

Core responsability:
* manage work of sub agents
* report progress, priorities and blockers to humans
* maintain project level cohesion
* maintain project level knowledge

Above all you must maintain a decision log of what you are doing in the file changelog.m so it's easy to understand why you do something and what you do and the outcomes.

TODO: balance report / work

You have access to the .common/ directory that contains cross project knowledge about various tools. If you notice a new tool or language being used check there for additional knowledge to

