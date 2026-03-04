# Ateam

### POC CLI - v1

Let's implement a proof of concept for ateam, we want to focus on reports and summary of reports and leave a few features to later:
* isolation of execution
* manage persistent memory of agents
* perform changes in isolated environment
* overall directory structure

Concepts:
* an existing checkout of a project
* sub-agents to report to produce reports for a specific role: small refactoring, big refactoring/rearchitecting, database integrity, internal docs, external docs, testing, etc ...
* supervisor reviews and makes decisions on one or more reports and later requests the report by itself
* report: an assessment of what to do for a particular role
* decisions: the report produced by the supervisor

Here is the basic features we want:
* designate a ateam working directory to store reports and designate which directory the project is already checked out in. ateam commands will be run within this working directory
* request one or more reports from sub-agents
* have the supervisor process these reports and produce a decisions report

Here is the CLI:

```bash
# create $(pwd)/NAME/ + a sub dir for each agent
# if the directories already exist then no-op except if new agents are specified then add them
# create config.toml with PROJECT_DIR
ateam init NAME --source PROJECT_DIR [--agents AGENT_LIST] [--work-dir DIR (default: current dir)]

# AGENT_LIST is one or or more of: all, refactor_small, refactor_architecture, docs_internal, docs_external, basic_project_structure, automation, dependencies, testing_basic, testing_full, security
# all means all these roles

# must be ran within a directory with config.toml
# runs all the agents in parallel (max of 3 at a time) as claude -p DEFAULT_AGENT_PROMPT + DEFAULT_REPORT_PROMPT + CUSTOM_PROMPT > AGENT_NAME.report.md
# also archive the report within the agent hown directory as: YYYY-MM-DD_HHMM.AGENT_NAME.report.md
ateam report --agents AGENT_LIST [--extra-prompt TEXT_OR_FILE]

# Look at all *.report.md files and produce review.md
# this executes claude -p SUPERVISOR_PROMPT + REVIEW_PROMPT + CUSTOM_PROMPT or just uses PROMPT with --prompt is passed in
ateam review [--extra-prompt TEXT_OR_FILE] [--prompt PROMPT]
```

We want to implement this in Go. Parallel agent execution is done via a worker pool with max number of parallelism (ok to use a dependency for that if it's very small, ideally use a built-in worker pool if it exists). This program mostly spawns "claude -p" processes. It will hardcoded a mapping between AGENT_NAME and their DEFAULT_AGENT_PROMPT. Also hardcode a generic DEFAULT_REPORT_PROMPT, SUPERVISOR_PROMPT, REVIEW_PROMPT.
When doing init these prompts are written to appropriately named markdown files in the directory structure created and each execution will read from them. This way it's possible to customize these prompts easily.

Next features will be:
* create a basic project overview for each sub-agent that can be used when producing a report
* maintain a context between reports so there is some memory
* have the supervisor create a summary of the project and based on its assessment (project size, complexity, velocity, ... ) will decide itself which agent reports to commission.
* more isolated environment (docker, anthropic experimental sandbox)
* stateful agents: last commit seen, decision/feedback on each report to incorporate in future reports

But don't implement any of these features yet. Just given context.

Please propose a plan and ask clarifying questions. Write this plan in plans/POC_Reporting.md

### POC CLI - v0.5

* run within an existing directory
* pre-create simple prompts aimed to be called with "claude -p" just to produce reports. Give this CLI to a supervisor with its own prompt.
* sub-agent prompt includes to write AGENT.report.md into a directory (by default /tmp/ateam/PROJECT_PATH)
* the supervisor then reads these reports and make a summary
* play with different conservative/ambitious prompts

```bash
ateam report --agent AGENT [--output-dir DIR]
ateam review [--output-dir DIR]
ateam all
```

TODO: cd to ateam project dir and specify the project to work on

Alt:
* cd to ateam state dir
* run: ateam init --project-dir DIR
  * save where the project is in project.config
* commands:

```bash
ateam report --agent AGENT [--project-dir DIR (default from local project.config or error)]
ateam review [--agents AGENT1,AGENT2,...]   (default review all reports) [--project-dir DIR (default from local project.config or error)]
ateam all (have the supervisor request reports and summarize them) [--project-dir DIR (default from local project.config or error)]
  # this commands produces a review.md that could then be given to an agent to implement (for now any claude but eventually )
```

Initial agents:
* small refactor
* big architecture review
* small testing
* full testing
* documentation-internal
* documentation-external
* dependency analysis:
  * deprecation
  * security
  * behind major versions + how it could impact the project
* review scripts/tooling ?


Concepts:
* report
  * stateful ?
* environment:
  * sandbox, docker, nothing, custom CLI args
* memory mode:
  * none
  * agg reports incrementally
  * build overview of project and reuse only that for each new report
  * look at X commits
  * look at specific changes
  * look at "recent"
  * since commit of last report
    * need to track commits


### POC CLI - v0

Let's move to building a CLI to start proving this idea:
* focus on specialized sub-agents
* implement the core workflow of: produce report, implement report
* initially I'll act as the supervisor to decide what report to implement and I'll manually run each agent
* little emphasis on directory structure

#### Phase 1: Focus on container execution and report generation

```bash
# Run a specific agent
ateam agent [report|implement] --agent AGENT_NAME --role PROMPT \
  [--report FILENAME (default: AGENT_NAME.YYYY-MM-DD_HHMM.report.md)]
  [--extra-prompt PROMPT] \
  [--commit yes (default for implement)|no (default for report)]
  [--docker-file FILE (default: Dockerfile)]
  [--docker-run FILE (default)]

# Run the coordinator agent and have it analyze a set of reports
ateam decide AGENT_NAME1:REPORT_FILE_1 AGENT_NAME2:REPORT_FILE_2 ...\
  [--decision FILEPATH (default: )]


# troubleshoot / interact
ateam chat --agent AGENT_NAME \
  [--docker-file FILE (default: Dockerfile)]
  [--docker-run FILE (default)]
```

#### Phase 2: Add automatic supervision

Implement the core coordinator

```bash
# Spawn sub-agents, wait for their completion and then run the coordinator to  and run the coordinator to wait
ateam run AGENT_NAME1,AGENT_NAME2,... [--mode report|implement (default: report)]
```


#### Phase 3: Implement directory structure and orchestration

many sub phases


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

