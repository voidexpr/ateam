#!/usr/bin/env bash
# Quick summary of all PoC outputs.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

header "Output files"
if [[ -d "$WORK_DIR/output" ]]; then
  ls -lh "$WORK_DIR/output/"
else
  echo "No output directory found at $WORK_DIR/output"
  exit 1
fi

header "Report summary"
if [[ -f "$WORK_DIR/output/report.md" ]]; then
  head -20 "$WORK_DIR/output/report.md"
  echo "..."
else
  echo "(not found)"
fi

header "Actions summary"
if [[ -f "$WORK_DIR/output/actions.md" ]]; then
  head -20 "$WORK_DIR/output/actions.md"
  echo "..."
else
  echo "(not found)"
fi

header "Coordinator decisions"
if [[ -f "$WORK_DIR/output/decisions.md" ]]; then
  head -20 "$WORK_DIR/output/decisions.md"
  echo "..."
else
  echo "(not found)"
fi

header "Git changes in worktree"
if [[ -d "$WORK_DIR/code" ]]; then
  cd "$WORK_DIR/code" && git diff --stat 2>/dev/null || echo "(clean or not a git repo)"
else
  echo "(no worktree)"
fi

header "Stream parsing (last run)"
if [[ -f "$WORK_DIR/output/stream.jsonl" ]]; then
  EVENTS=$(wc -l < "$WORK_DIR/output/stream.jsonl")
  TOOLS=$(jq -s '[.[] | select(.type == "tool_use")] | length' "$WORK_DIR/output/stream.jsonl")
  echo "Stream events: $EVENTS"
  echo "Tool-use events: $TOOLS"
  echo "Result:"
  jq -s '[.[] | select(.type == "result")] | last | {cost_usd, total_cost_usd, total_cost}' \
    "$WORK_DIR/output/stream.jsonl" 2>/dev/null || echo "(no result event)"
else
  echo "(no stream.jsonl)"
fi

header "Exit code"
if [[ -f "$WORK_DIR/output/exit_code" ]]; then
  cat "$WORK_DIR/output/exit_code"
else
  echo "(not found)"
fi
