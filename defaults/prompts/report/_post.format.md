# Report Format

**IMPORTANT: when a `# Previous Report` section is present in this prompt, re-include the findings that haven't been addressed with a potentially updated priority. When absent, just produce the full standalone report. Always produce a complete report — don't refer to a previous version.**

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
- **Run analytical tools when your role's findings ARE the tool's output.** Some role lenses depend on data only tools can produce: dependency CVEs (`govulncheck`, `npm audit`, `pip-audit`), coverage gaps (`go test -coverprofile` + `go tool cover -func`, `pytest --cov`), dependency currency (`go list -m -u all`, `npm outdated`), license inventory (`go-licenses`), benchmark / profiler output. For those lenses, run the tool first and base the report on its output. For lenses unrelated to tool output (refactoring, design, documentation, code review), recommend tools in findings when appropriate but do NOT re-run linters, formatters, type-checkers, or test runners during the report — those are dev/CI's job, not the LLM's.
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

Write the complete report to disk using the `Write` tool. The destination is:

```
{{exec.output_file}}
```

The full report — starting with `# Summary` and containing every section listed under Output Validation Gate — must be the `content` argument of that single `Write` call.

After the `Write` call returns successfully, your FINAL assistant message must be a single short line confirming the write, e.g. `Report written to {{exec.output_file}}`. Do not include the report body in the final message; do not include any other commentary. The on-disk file is the source of truth — the harness reads it directly, so anything you stream as text is discarded.

If the `Write` call fails, retry it once. If it still fails, then (and only then) emit the report as your final message so the harness can recover it from the stream.

Do NOT spawn a subagent to write the report, and do NOT continue exploring after the Write succeeds — your next and final action after a successful Write is the one-line confirmation.
