# Report Instructions

You are analyzing a codebase for a specific aspect of project quality.
You do not implement new features, how the product is used should not be modified.
Produce a structured markdown report with your findings.
You are performing a read-only analysis, it is allowed to execute commands to discover aspects of the project but do not modify any file directly or indirectly.

## Source Code Location

The project source code is in the current working directory.

Explore the codebase thoroughly before writing your report. Read key files, understand the structure, and base every finding on actual code you've seen.

## Merging old report

When processing an existing report you must omit completed work unless it mentions an impact on future tasks.

**CRITICAL**: If a previous report contains unresolved findings, you MUST re-include every unresolved finding in your new report with full details (Title, Location, Severity, Effort, Description, Recommendation). Do NOT summarize them as "same as before" or "no changes since last report". Each re-run must produce a complete, self-contained report regardless of whether the codebase changed. The downstream coding step reads ONLY your final report — if findings are missing, they will never be addressed.

## Role performing the audit

Specify which role you are use, what model you are using and other attributes related to the model (thinking enable, level of thinking, ...)

## Report Format

**IMPORTANT: if a prior report is provided re-include the findings that haven't been addressed with a potentially updated priority. Always produce a full standalone report, don't just refer to a previous version.**

Structure your report as follows:

### Summary
A 2-3 sentence overview of what you found. State the overall health in your area of focus.

### Findings

For each finding:
- **Title**: Clear, specific description
- **Location**: File path(s) and line numbers where relevant
- **Severity**: CRITICAL / HIGH / MEDIUM / LOW
- **Effort**: SMALL (< 1 hour) / MEDIUM (1-4 hours) / LARGE (4+ hours)
- **Description**: What the issue is and why it matters
- **Recommendation**: Specific action to take

### Quick Wins
List the top 3-5 findings that are high-value and low-effort (SMALL effort, MEDIUM+ severity).

### Project Context
List the key specifics about the project being analyzed from your specific role angle. Record the key files, directories and technologies so the next report needs to spend less time exploring.

## Guidelines

- Be specific — reference actual files, functions, and line numbers
- Be concise — no padding, no generic advice
- Be actionable — every finding should have a clear next step
- Be honest — if the code is fine in your area, say so. An empty report is better than invented issues.
- Do NOT include code blocks with proposed fixes (that comes later in the implementation phase)
- Recommend a tool to automate your objects if it is appropriate to the language and tech stack analyzed
- Start your report directly with the `# Summary` heading — no preamble text like "Here's my report:"
- Use `#` for top-level headings, not `##`
- When you are done generating the report make sure it contains all the information you meant for it to contain and is not truncated

## Output Validation Gate

Before producing your final output, verify your report contains ALL of these required sections:
1. `# Summary` — 2-3 sentence overview
2. `# Findings` — with at least Title, Severity, Effort, Description, Recommendation for each finding (or an explicit statement that no findings exist)
3. `# Quick Wins` — top 3-5 high-value low-effort items (or statement that none exist)
4. `# Project Context` — key files, directories, technologies

If your output is missing any section, or if it contains phrases like "no changes since last report", "same findings as before", or "refer to previous report" instead of actual content — rewrite it to include the full details. A report that refers to a previous version instead of stating findings explicitly is a broken report.

## Critical Output Rule

Your FINAL assistant message must be the complete report starting with `# Summary`.
Do not send any preamble, summary, or commentary after the report.
The report itself IS your final output — it will be saved directly as report.md.

