# Approach

_This document is a stub._ It will collect the rationale, principles, and key concepts behind ATeam. See [README.md](README.md) for the current overview, "Why ATeam" rationale, and key concepts.


The vision is to solve some of the immediate issues with agentic development:
* constant permission approvals vs. safety
* humans becoming the verifier of agent work at a low level of abstraction: code review, testing, request refactoring, etc ...

Ateam shows how an agent driven quality improvement pipeline can work.

As agentic coding engineering currently stands humans can only manage a handful of features, maybe 2 to 4 at a time. So the vision for ateam is for humans to have 2-4 active workspaces at a point in time. And then for these workspaces and the ones not currently active ateam runs keep improving the code: focus on recent code for projects under heavy development (small refactor, add tests, keep doc in sync), shape up a project for new ones (automation, establish security baseline, code architecture, database schema) or keep improving projects with a core set of features (dependencies management, security, test coverage, external documentation, ...).

From a human perspective once a feature is working then a few rounds of ateam runs would shape it up for the future and while this is happening focus can switch to another feature without having to prompt for better engineering.

As coding agents seem to be the killer app of AI, unattended coding agents seems to be the killer app for agent orchestration layers. There are a lot of frameworks designed to manage arbitrary workflows of agents to develop features but feature development seems better suited for interactive agent: explore the spec with an agent, refine it, then as parts are coded try it, evolve the design. This is a wonderful workflow. As opposed to write a huge spec upfront, devide it into sub tasks and then trying to keep up with all the work and figuring out where to unwind it when it goes astray.

Instead have a few interactive agents and each workspace has an ateam "team" doing all the engineering unattended, with appropriate isolation so they can run tests without impacting the rest of the system. Maintain project level context so decisions build on top of each others. Keep things simple until better patterns emerge for more efficient codebase discovery and context management. 2 areas still rapidly evolving. Claude code does quality work using grep/find but it's probably not the end state. Ateam is designed to take advantages of new trends in this area.


## Isolation

## Agent: reuse vs. custom

## Cost tracking

From day 1 cost tracking was built into ateam even though it was designed to be used with subscription. Both because tokens are the metric to optimize in the world of AI so tracking it is important to build a good mental model and measure what to optimize. The other reasons was that eventually subscriptions might not remain because of the arbitrage that orchestration layers can offer: 24/7 agent runs. Earlier than thought (announced in May 2026 for June 15 2026: Anthropic announced stronger limits with unattended agents). This is a natural evolution. it is a reality that this was not a sustainable model for LLM providers. The other reality is that the tokens used to build a feature are not enough to properly "engineer" a feature. And we should adjust our expectation. We still get to use agents and benefit from the extra velocity and productivity but we need to accept a higher cost or slower pace to engineer solutions.

It will be interesting to see how it evolves. My bet is that LLM gets better at performing tasks but not cheaper. Probably even more expensive. While ateam would strive more in a world of cheaper tokens (can just do more runs) it is still very important to make the advanced features newer LLMs are able to build features that can be used and improved for the long term by adding the missing engineering parts or looking at it from an angle not considered during development (testing, security, dependencies, ...)

## Evaluation Criterias

### Accuracy
Ateam is used in multiple personal project, based on early results:
* finds real issues, fix bugs
* some issues seemed like busy work: few line changes without a very clear benefit making through report and reviewer
    * could be based on the instruction to the reviewer that few small wins are a positive
* overzealous security checks
    * broke features by force mounting source code read-only in docker for ateam itself or disable javascript requests outside of the source server for a small web app breaking major features
* obsession for Github CI/CD: TODO

These issues resulted into the following changes:
* added a 'verify' step looking at commits and keeping them honest: make sure tests run clean, look for bugs
* tuned prompts to try to group nitpic changes into a single item and lower the priority
* security roles have had the most changes but it is also easy for a given project to add an extra prompt instruction to avoid repeating the same issue

Overall prompt tuning is an expected ongoing area of ateam. Adding strong test gates and verification seems to be good and more algorithmtically steps will be added

### Cost (aka token usage)

* a concsious design decision was to implement the coding phase as a pure prompt. This was done to see how good LLMs are at tracking multiple tasks to perform (Claude Code as a task/todo tool) and to more easily experiment and evolve the git part of the workflow. It has been great where the LLM is able to recover from errors that an algorithmic approach would have not been able to do. But of course it's a very token heavy step.
