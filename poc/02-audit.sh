#!/usr/bin/env bash
# Write the audit prompt and run the audit agent.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

header "Writing audit prompt"

cat > "$WORK_DIR/prompt-audit.md" << 'PROMPT'
# Role: Testing Agent — Audit Mode

You are a testing specialist. Your job is to analyze this codebase
and produce a report on testing gaps.

## What To Do

1. Explore the project structure. Understand what it does.
2. Look at existing tests. Run them if a test command exists.
3. Identify the most important untested code paths.
4. For each gap, describe what test would cover it and why it matters.

## What NOT To Do

- Do not write or modify any code in this mode.
- Do not install new dependencies.
- Do not over-report. Focus on the 3-5 most impactful gaps.

## Output

Write your report to /output/report.md with this structure:

```markdown
# Testing Audit Report

## Summary
(2-3 sentences: overall test health)

## Findings

### 1. (title)
- **File(s):** ...
- **Risk:** high/medium/low
- **What's missing:** ...
- **Suggested test:** ...

(repeat for each finding)
```

Be concise. Be specific. Reference actual file paths and function names.
PROMPT

clean_stream_output

header "Running audit agent"

run_agent "ateam-audit" "$WORK_DIR/prompt-audit.md"

header "Results"

echo "Exit code: $(cat "$WORK_DIR/output/exit_code" 2>/dev/null || echo 'missing')"
echo ""

if [[ -f "$WORK_DIR/output/report.md" ]]; then
  echo "--- report.md ---"
  cat "$WORK_DIR/output/report.md"
else
  echo "WARNING: report.md was not created"
fi

if [[ -s "$WORK_DIR/output/stderr.log" ]]; then
  echo ""
  echo "--- stderr (last 20 lines) ---"
  tail -20 "$WORK_DIR/output/stderr.log"
fi
