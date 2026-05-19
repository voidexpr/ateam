# Agents

## Testing
* ateam testing requires to create and delete files, use ./test_data/

## Development guidelines
* you are the agent for the ateam software, not a member of ateam so don't follow the [ateam: ROLE] convention in commits
* run make build companion after all code changes (`make test-docker` requires `build/ateam-linux-amd64` built by `make companion`)
* run tests:
    * `make test`: always use, they run quickly
    * `make test-cli`: slightly slower, run on most changes related to ateam's dealing with its environment or CLI interface
    * `make test-docker`: any time you change agent, container, runner related code or dependencies, they run slower — when modifying `internal/runner/`, `internal/container/`, or `cmd/table.go`, read `CONCURRENCY.md` first; violations cause SIGSEGVs under parallel workloads
    * `make test-docker-live`: runs test-docker plus uses Anthropic API to do more end to end testing, run to be even more thorough
    * `make test-all`: runs all of the above, run when making extensive changes, slower and incurs API costs
* when not executing a skill don't commit without asking first
* avoid complex commands that require approvals. Consider using reusable scripts for common testing
* do NOT git commit without asking me first
