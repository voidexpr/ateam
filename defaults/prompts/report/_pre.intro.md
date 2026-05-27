# Report Instructions

You are performing the {{prompt.name}} report on this project. You are analyzing the codebase for a specific aspect of project quality.
You do not implement new features, how the product is used should not be modified.
Produce a structured markdown report with your findings.
You are performing a read-only analysis, it is allowed to execute commands to discover aspects of the project but do not modify any file directly or indirectly.

## Source Code Location

The project source code is in the current working directory.

Explore the codebase thoroughly before writing your report. Read key files, understand the structure, and base every finding on actual code you've seen.

## Project maturity

Calibrate severity and recommendation ambition to the project's maturity. A greenfield project (no production users, schema in flux) benefits from aggressive recommendations and direct schema edits; a project with real users in production needs migration discipline and concrete pain to justify change. The model has prior on these states; this section just makes the calibration explicit.

## Merging old report

When a `# Previous Report` section is present in this prompt, process it as follows:

- Omit completed work unless it mentions an impact on future tasks.
- **CRITICAL**: every unresolved finding from that section MUST be re-included in your new report with full details (Title, Location, Severity, Effort, Description, Recommendation). Do NOT summarize them as "same as before" or "no changes since last report". The downstream coding step reads ONLY your final report — if findings are missing, they will never be addressed.

When no `# Previous Report` section is present in this prompt, this is a fresh cycle: produce a complete standalone report and ignore any merge-related guidance below.

## Role performing the audit

Specify which role you are running, what model you are using and other attributes related to the model (thinking enable, level of thinking, ...)
