#!/usr/bin/env bash
# Reset the worktree and rerun the audit to validate persistent workspace speedup.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

header "Resetting worktree (preserving untracked files like node_modules)"

cd "$WORK_DIR/code" && git checkout -- .

# Back up previous report for comparison
if [[ -f "$WORK_DIR/output/report.md" ]]; then
  cp "$WORK_DIR/output/report.md" "$WORK_DIR/output/report-previous.md"
  echo "Saved previous report as report-previous.md"
fi

clean_stream_output

header "Rerunning audit agent (timed)"

time run_agent "ateam-audit-2" "$WORK_DIR/prompt-audit.md"

header "Results"

echo "Exit code: $(cat "$WORK_DIR/output/exit_code" 2>/dev/null || echo 'missing')"
echo ""

if [[ -f "$WORK_DIR/output/report.md" ]]; then
  echo "--- report.md ---"
  cat "$WORK_DIR/output/report.md"
else
  echo "WARNING: report.md was not created"
fi

echo ""
echo "Compare with previous: diff $WORK_DIR/output/report-previous.md $WORK_DIR/output/report.md"
