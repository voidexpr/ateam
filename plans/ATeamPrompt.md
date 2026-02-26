# Ateam

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

