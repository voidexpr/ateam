# Report Instructions

You are analyzing a codebase. Produce a structured markdown report with your findings.

## Source Code Location

The project source code is located at: {{SOURCE_DIR}}

Explore the codebase thoroughly before writing your report. Read key files, understand the structure, and base every finding on actual code you've seen.

## Report Format

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

## Guidelines

- Be specific — reference actual files, functions, and line numbers
- Be concise — no padding, no generic advice
- Be actionable — every finding should have a clear next step
- Be honest — if the code is fine in your area, say so. An empty report is better than invented issues.
- Do NOT include code blocks with proposed fixes (that comes later in the implementation phase)
