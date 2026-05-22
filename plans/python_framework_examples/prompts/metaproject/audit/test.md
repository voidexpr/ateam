Audit the test commands for `{{arg.label}}` in 2 parts.

### Commands that exist today

Only document what can be run today:

* the test framework(s) in use
* the command for each category, and the directory it should be run from
* any setup required to run tests (services, fixtures, env vars)

If no tests exist, report that explicitly.

Write a structured toml file in `{{exec.shared_dir}}/{{arg.label}}/test/tests.toml`:

    [tests]
    fast=QUICK_VERIFICATION_COMMAND
    all=RUN_ABSOLUTELY_ALL_TESTS
    AREA_X=COMMAND_X
    AREA_Y=COMMAND_Y

Replace placeholders with actual commands. Areas are things like `backend`,
`frontend`, `cli`, or `benchmark`.

### How complete is the test story

Flag:

* test commands that do not match what the project supports
* missing categories (e.g. e2e tests with no documented runner)
* broken or skipped suites worth surfacing
