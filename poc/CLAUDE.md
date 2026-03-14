# CLAUDE for ateam POC

## Testing
* ateam testing requires to create and delete files, use ./test_data/

## Development guidelines
* run make build after all code changes
* run tests:
    * `make test`: always
    * `make test-docker`: any time you change agent, container, runner related code or dependencies
* when not executing a skill don't commit without asking first
* avoid complex commands that require approvals. Consider using reusable scripts for common testing