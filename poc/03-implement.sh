#!/usr/bin/env bash
# Write the implement prompt (inlining the audit report) and run the implement agent.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

REPORT="$WORK_DIR/output/report.md"
if [[ ! -f "$REPORT" ]]; then
  echo "ERROR: $REPORT not found. Run 02-audit.sh first." >&2
  exit 1
fi

header "Writing implement prompt (inlining report.md)"

# Unquoted PROMPT so $(cat ...) expands
cat > "$WORK_DIR/prompt-implement.md" << PROMPT
# Role: Testing Agent — Implement Mode

You are a testing specialist. Implement the tests recommended in the
audit report below.

## Audit Report

$(cat "$REPORT")

## Instructions

1. Implement the suggested tests, starting with highest-risk.
2. Run the test suite after each change.
3. Stop after the top 3 findings.
4. Write a summary to /output/actions.md listing what you changed,
   test results, and anything you skipped.

Do not refactor production code. Only add or modify test files.
PROMPT

clean_stream_output

header "Running implement agent (budget: \$$BUDGET_IMPLEMENT)"

run_agent "ateam-implement" "$WORK_DIR/prompt-implement.md" "$BUDGET_IMPLEMENT"

header "Results"

echo "Exit code: $(cat "$WORK_DIR/output/exit_code" 2>/dev/null || echo 'missing')"
echo ""

if [[ -f "$WORK_DIR/output/actions.md" ]]; then
  echo "--- actions.md ---"
  cat "$WORK_DIR/output/actions.md"
else
  echo "WARNING: actions.md was not created"
fi

echo ""
echo "--- git diff --stat ---"
cd "$WORK_DIR/code" && git diff --stat
