# Agents

## Mission
A Go CLI to manage unattended agent + software engineering quality oriented prompts and workflows

## Current objectives
* get to v1 feature set
* test, refactor, code quality
* gear up to be in a good release state

## Tool Use
* avoid complex commands that require approvals. Consider using reusable scripts for common testing

## How to use Git
* you are the agent for ateam own software development, not a member of ateam so don't follow the [ateam: ROLE] convention in commits
* when not executing a skill don't commit without asking first
* do NOT git commit without asking me first
* if you are asked to commit make sure to run: `make run-ci` and fix any issues found

## How to learn
* README.md: mission, core features, core commands
* when changing agent related code or code dealing with concurrency: CONCURRENCY.md
* how to run ateam: COMMANDS.md
* how to configure ateam: CONFIG.md
* how isolation of agents work in ateam: ISOLATION.md

## How to build
* `make build-all`: after all code changes
    * because `make test-docker` requires `build/ateam-linux-amd64` built by `make companion` and `make build-all`

## How to test
* ateam testing requires to create and delete files, use ./test_data/

### Always
* `make test`: always use, they run quickly

### CLI Related / end-to-end testing
* `make test-cli`: slightly slower, run on most changes related to ateam's dealing with its environment or CLI interface

### Dock Related
* `make test-docker`: any time you change agent, container, runner related code or dependencies, they run slower — when modifying `internal/runner/`, `internal/container/`, or `cmd/table.go`, read `CONCURRENCY.md` first; violations cause SIGSEGVs under parallel workloads
* `make test-docker-live`: runs test-docker plus uses Anthropic API to do more end to end testing, run to be even more thorough

### Full regression run
* `make test-all`: runs all of the above, run when making extensive changes, slower and incurs API costs
