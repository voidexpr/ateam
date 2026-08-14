# Code Verification

Review the recent commits performed by ateam agents. They are selected tasks to improve the project's quality without modifying its features.

Your task is to review these changes:
- look for logical bugs
- look for broken or missing tests
- look for changes that are too risky
    - security changes breaking existing features
    - changing database schema without an automatic migration
- run all test cases and investigate failures
    - don't just run the most minimal tests, run all of them
- make sure no coding tasks cheated by modifying the code where test cases actually found a real issue
- make sure all code changes have properly been checked in: no uncommitted changes

## Headless Execution Model — no notifications, ever

You run under a headless CLI (`claude -p`): the moment you emit an assistant
message with no tool call pending, your process exits and every background
task you started is SIGTERM'd. There are no future turns and no wake-ups.

* Completion notifications DO NOT EXIST here. No monitor, task notification,
  or harness event will ever wake you when a background task finishes. Never
  emit text like "waiting on the monitor" — that message would terminate the
  session and kill the still-running work.
* Run test commands in a FOREGROUND `Bash` call with an explicit large
  `timeout` so your turn stays open until they return. Only if a single
  command genuinely exceeds the 10-minute per-call cap, launch it with
  `run_in_background: true` and actively poll `BashOutput` (paced with
  foreground `Bash({command: "sleep 30"})` calls) until it reports
  `completed` — never end your turn while it is still `running`.

Record all your findings using the structure below.

```
# Code verification report

LIST OF ALL COMMITS REVIEW

## Executive summary

quickly tell which commits are fine and which aren't

## Issues

### Issue 1: title

#### Description

describe what is the problem found

#### Resolution

describe the step taken to solve it and the new git commit hash
```

## Critical Output Rule

Write the complete code verification report to disk using the `Write` tool. The destination is:

```
{{exec.output_file}}
```

The full report — every section listed above, with the per-commit review and any issues found — must be the `content` argument of that single `Write` call.

After the `Write` call returns successfully, your FINAL assistant message must be a single short line confirming the write, e.g. `Verification report written to {{exec.output_file}}`. Do not include the report body in the final message; do not include any other commentary. The on-disk file is the source of truth — the harness reads it directly, so anything you stream as text is discarded.

If the `Write` call fails, retry it once. If it still fails, then (and only then) emit the verification report as your final message so the harness can recover it from the stream.

The `Write` is mandatory: if your run ends without having written the file, the harness marks the whole run as failed (`missing_artifact`) — streamed text is NOT accepted as a substitute for the on-disk report.
