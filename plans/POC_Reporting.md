# ATeam POC: Reporting System

## Scope

A Go CLI that spawns `claude -p` processes to produce role-specific code analysis reports, then has a supervisor review them and produce a decisions document. No isolation, no persistent memory, no code changes — just reports.

**Not in scope (documented for later):** Docker/sandbox isolation, persistent agent memory (knowledge.md), code changes in isolated environments, full directory structure from the design doc, git worktrees, SQLite state, budget tracking.

---

## CLI Interface

```bash
# Initialize a working directory for a project
ateam init NAME --source PROJECT_DIR [--agents AGENT_LIST] [--work-dir DIR]

# Run agents to produce reports
ateam report --agents AGENT_LIST [--extra-prompt TEXT_OR_FILE]

# Supervisor reviews reports and produces decisions
ateam review [--extra-prompt TEXT_OR_FILE] [--prompt PROMPT]
```

---

## Agent Roles

| Agent ID | Description |
|---|---|
| `refactor_small` | Small refactoring opportunities in recent code: naming, duplication, error handling |
| `refactor_architecture` | Big-picture architecture: coupling, layering, abstractions, patterns |
| `docs_internal` | Internal documentation: architecture docs, code overview, dev guides |
| `docs_external` | External documentation: README, installation, usage, API docs |
| `basic_project_structure` | Project structure: file organization, build system, conventions |
| `automation` | CI/CD, linting, formatting, pre-commit hooks, build automation |
| `dependencies` | Dependency health: outdated, unused, vulnerable, alternatives |
| `testing_basic` | Test coverage gaps, missing edge cases, basic test quality |
| `testing_full` | Full test suite analysis: flaky tests, integration gaps, test architecture |
| `security` | Security: vulnerabilities, injection risks, secrets, auth patterns |

`all` is shorthand for every role above.

---

## Directory Structure

After `ateam init myproject --source /path/to/code --agents all`:

```
myproject/                          # working directory (NAME from init)
  config.toml                       # project config
  prompts/
    report_instructions.md          # generic report output instructions (shared)
    supervisor_role.md              # supervisor system prompt
    review_instructions.md          # review output instructions
    agents/
      refactor_small.md             # role prompt for each agent
      refactor_architecture.md
      docs_internal.md
      docs_external.md
      basic_project_structure.md
      automation.md
      dependencies.md
      testing_basic.md
      testing_full.md
      security.md
  reports/                          # current reports (overwritten each run)
    refactor_small.report.md
    testing_basic.report.md
    ...
  review.md                         # latest supervisor decisions
  archive/                          # historical reports
    2026-03-04_1430.refactor_small.report.md
    2026-03-04_1430.testing_basic.report.md
    2026-03-04_1445.review.md
    ...
```

### config.toml

```toml
[project]
name = "myproject"
source_dir = "/absolute/path/to/checked/out/code"

[agents]
enabled = ["refactor_small", "testing_basic", "security"]

[execution]
max_parallel = 3
agent_report_timeout_minutes = 10
```

Minimal. Future versions add budget, schedule, docker, etc.

---

## Prompt Architecture

Each agent invocation assembles a prompt from parts:

```
AGENT_PROMPT = prompts/agents/{agent_id}.md
             + prompts/report_instructions.md
             + [extra-prompt if provided]
```

The assembled prompt is piped to:
```bash
claude -p "ASSEMBLED_PROMPT" > reports/{agent_id}.report.md
```

No stream-json parsing, no output format flags. Plain text output piped straight to the report file. Maximum simplicity.

For the supervisor review:
```
REVIEW_PROMPT = prompts/supervisor_role.md
              + prompts/review_instructions.md
              + [contents of all reports/*.report.md]
              + [extra-prompt if provided]
```

Or if `--prompt` is passed, use that verbatim (with reports appended).

### Prompt Content (Hardcoded Defaults)

Each agent gets a role-specific prompt that:
1. Describes the agent's focus area and what to look for
2. Tells it the project source is at a specific path (injected from config.toml)
3. Asks it to produce a structured markdown report

The report instructions prompt tells all agents to:
- Start with a severity/priority summary
- List concrete findings with file paths and line references
- Provide actionable recommendations
- Estimate effort (small/medium/large) for each recommendation
- Be concise — no padding, no generic advice

The supervisor prompt tells it to:
- Read all agent reports
- Identify the highest-value improvements
- Flag conflicts between agent recommendations
- Produce a prioritized action plan
- Note which recommendations to act on now vs defer

These prompts are written to disk during `ateam init` so users can customize them before running reports.

---

## Implementation Plan

### Go Project Structure

```
ateam-poc/
  go.mod
  go.sum
  main.go                    # cobra root command setup
  cmd/
    init.go                  # ateam init
    report.go                # ateam report
    review.go                # ateam review
  internal/
    config/
      config.go              # TOML config read/write
    prompts/
      prompts.go             # prompt assembly logic
      defaults.go            # hardcoded default prompt strings (embed)
    runner/
      runner.go              # claude -p execution (direct pipe to file)
      pool.go                # worker pool for parallel execution
    agents/
      agents.go              # agent registry (ID → default prompt mapping)
```

### Dependencies

- `github.com/spf13/cobra` — CLI framework (standard for Go CLIs)
- `github.com/BurntSushi/toml` — TOML parsing
- Go stdlib only for everything else: `os/exec` for process spawning, `sync` for worker pool (semaphore via buffered channel), `embed` for default prompts

No SQLite, no Docker SDK — those come later.

### Worker Pool

Simple channel-based semaphore pattern using Go stdlib:

```go
sem := make(chan struct{}, maxParallel)
var wg sync.WaitGroup
for _, agent := range agents {
    wg.Add(1)
    sem <- struct{}{} // acquire
    go func(a Agent) {
        defer wg.Done()
        defer func() { <-sem }() // release
        runAgent(a)
    }(agent)
}
wg.Wait()
```

No external dependency needed.

### Implementation Steps

#### Step 1: Project scaffolding
- `go mod init`, cobra setup, main.go with root command
- Three subcommands: init, report, review
- Default prompt strings as embedded Go constants

#### Step 2: `ateam init`
- Parse flags: NAME, --source, --agents, --work-dir
- Validate source directory exists
- Create directory structure (config.toml, prompts/, reports/, archive/)
- Write default prompts to prompts/ files
- Write config.toml with project name, source dir, enabled agents
- If directory exists: only add new agents (don't overwrite existing prompts)

#### Step 3: `ateam report`
- Read config.toml to get source dir and enabled agents
- Parse --agents flag (resolve `all`, validate against known agents)
- For each agent: assemble prompt (read from prompts/ files + extra-prompt)
- Run agents in parallel via worker pool (max 3)
- For each: spawn `claude -p "PROMPT" > reports/{agent_id}.report.md` with timeout
- Archive to archive/YYYY-MM-DD_HHMM.{agent_id}.report.md
- Print summary to stdout (which agents ran, success/failure, time taken)

#### Step 4: `ateam review`
- Read all reports/*.report.md files
- Assemble supervisor prompt: role + instructions + all report contents
- If --prompt provided, use that instead (still append reports)
- Spawn `claude -p "PROMPT" > review.md`
- Archive to archive/YYYY-MM-DD_HHMM.review.md
- Print summary

#### Step 5: Polish
- Error handling: missing config.toml, no reports to review, claude not found
- User feedback: progress output showing which agents are running/complete
- Validate agent names in --agents flag

---

## Decisions Made

| Decision | Rationale |
|---|---|
| No `--dangerously-skip-permissions` | POC is read-only analysis. Agents can still read all files and reason about them. Future versions add this for agents that need to run tests. |
| No model flag | Just call `claude -p` — inherits whatever model the user's Claude Code is configured for. Keeps the POC simple. |
| Go project in ~/ateam-poc/ | Separate from the plans directory. Clean standalone project. |
| Direct pipe to file | `claude -p "PROMPT" > report.md` — no stream-json parsing, no output format flags. Maximum simplicity. |
| `@filename` convention | All prompt-related CLI args accept either inline text or `@path/to/file` to read from disk (like curl). |
| Prompts written to disk on init | Users can customize before running. Future versions inherit/override at org level. |
| No SQLite | Reports are files. State is the filesystem. Database comes with stateful agents. |
| Archive is append-only | Never delete old reports. Simple `ls archive/` shows history. |
| Per-agent timeout | Default 10 minutes, configurable via config.toml `agent_report_timeout_minutes` or `--agent-report-timeout`. |

---

## Resolved Decisions

- **`@filename` convention** for all prompt-related CLI args — `@path/to/file` reads from disk, anything else is inline text (like curl).
- **Pure markdown** for reports. Structured frontmatter documented for later.
- **Failure handling**: if one agent fails, others continue. Failed agents get a report file with an error message.
- **Timeout**: 10 minutes default, configurable via `agent_report_timeout_minutes` in config.toml or `--agent-report-timeout` CLI flag.

---

## Future (Context Only)

These features are documented in ATeamDesign.md and DesignChangeRecommendations.md but explicitly out of scope for this POC:

- **Project overview generation** — each agent gets a baseline understanding of the project
- **Persistent memory** — knowledge.md files that accumulate across runs
- **Supervisor autonomy** — supervisor decides which agents to commission
- **Docker/sandbox isolation** — container or OS-level sandboxing for agents
- **Stateful agents** — last commit seen, feedback incorporation, decision history
- **Git worktrees** — isolated branches per agent for code changes
- **Budget tracking** — cost monitoring and limits
- **SQLite state** — structured state management
