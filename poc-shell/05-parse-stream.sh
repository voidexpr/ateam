#!/usr/bin/env bash
# Parse stream-json output for event types, cost, and tool-use counts.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

STREAM="$WORK_DIR/output/stream.jsonl"
if [[ ! -f "$STREAM" ]]; then
  echo "ERROR: $STREAM not found. Run an agent step first." >&2
  exit 1
fi

header "Event type distribution"
jq -r '.type' "$STREAM" | sort | uniq -c | sort -rn

header "Result event (last)"
jq -s '[.[] | select(.type == "result")] | last' "$STREAM"

header "Cost / usage data"
jq -s '[.[] | select(.type == "result")] | last | {cost_usd, total_cost_usd, total_cost, usage}' "$STREAM"

header "Tool-use count"
TOOL_COUNT=$(jq -s '[.[] | select(.type == "tool_use")] | length' "$STREAM")
echo "$TOOL_COUNT tool-use events"

header "Tool names used"
jq -r 'select(.type == "tool_use") | .tool' "$STREAM" | sort | uniq -c | sort -rn

# Also check coordinator stream if it exists
COORD_STREAM="$WORK_DIR/output/coordinator-stream.jsonl"
if [[ -f "$COORD_STREAM" ]]; then
  header "Coordinator stream — event types"
  jq -r '.type' "$COORD_STREAM" | sort | uniq -c | sort -rn

  header "Coordinator cost"
  jq -s '[.[] | select(.type == "result")] | last | {cost_usd, total_cost_usd, total_cost, usage}' "$COORD_STREAM"
fi
