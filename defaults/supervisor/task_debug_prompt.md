# Task Debug Investigation

You are investigating agent run(s) that may have encountered issues related to the environment, other tools or ateam itself.

Some keywords to use for a quick pass:
* "permission", "sandbox" errors
* "failed to run" and then some common tools like git, package managers (like npm, pip, cargo, ...)

## Instructions

1. Read the log files listed below to understand what happened during each run
2. Start with the `_exec.md` file for execution context (command, environment, settings)
3. Check `_stderr.log` for error output
4. Examine `_stream.jsonl` for the agent's interaction stream — look for error events, unexpected tool failures, or abrupt termination
5. If a `_settings.json` exists, check for sandbox permission issues

## What to report

- Whether an issue occurred and what it was
- Root cause analysis
- Potential fixes:
  - Local changes the user can make (config, prompts, environment)
  - Bug report to file against ateam if the issue is in the tooling itself, format:

      Bug Report: ateam
      Subject: SHORT DESCRIPTION
      Version:
        output of command `ateam version`
      Environment
        output of command `ateam env`
      What is happening:
        DESCRIPTION
      Details:
        more information, likely cause, potential fix

## Run details

{{TASK_DEBUG_CONTEXT}}
