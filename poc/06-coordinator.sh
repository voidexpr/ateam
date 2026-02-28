#!/usr/bin/env bash
# Write the coordinator prompt (inlining report + actions) and run the coordinator agent.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

REPORT="$WORK_DIR/output/report.md"
ACTIONS="$WORK_DIR/output/actions.md"

if [[ ! -f "$REPORT" ]]; then
  echo "ERROR: $REPORT not found. Run 02-audit.sh first." >&2
  exit 1
fi

# actions.md is optional — the coordinator can still triage the audit report alone
ACTIONS_CONTENT=""
if [[ -f "$ACTIONS" ]]; then
  ACTIONS_CONTENT="$(cat "$ACTIONS")"
else
  ACTIONS_CONTENT="(No implementation actions have been taken yet.)"
fi

header "Writing coordinator prompt"

cat > "$WORK_DIR/prompt-coordinator.md" << PROMPT
You are the ATeam coordinator. Review the following agent output
and make decisions.

## Testing Agent — Audit Report

$(cat "$REPORT")

## Testing Agent — Implementation Summary

$ACTIONS_CONTENT

## Your Task

For each finding in the audit report, decide:
- **done**: already implemented in the actions summary
- **defer**: not worth doing now (explain briefly)
- **ask**: need human input (explain what's unclear)

Then assess overall: is the project in better shape? What should
the testing agent focus on next time?

Write your decisions to /output/decisions.md.
PROMPT

rm -f "$WORK_DIR/output/coordinator-stream.jsonl"

header "Running coordinator agent"

# Coordinator only needs /output mounted — no workspace access
docker run --rm \
  --name "ateam-coordinator" \
  --cpus="$DOCKER_CPUS" --memory="$DOCKER_MEMORY" \
  -v "$WORK_DIR/output:/output:rw" \
  -v "$WORK_DIR/prompt-coordinator.md:/agent-data/prompt.md:ro" \
  -v "$HOME:$HOME:ro" \
  -v "$HOME/.claude:$HOME/.claude:rw" \
  -v "$HOME/.claude.json:$HOME/.claude.json:rw" \
  -e "HOME=$HOME" \
  -e "CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
  "$DOCKER_IMAGE" \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --verbose \
      | tee /output/coordinator-stream.jsonl
  '

header "Results"

if [[ -f "$WORK_DIR/output/decisions.md" ]]; then
  echo "--- decisions.md ---"
  cat "$WORK_DIR/output/decisions.md"
else
  echo "WARNING: decisions.md was not created"
fi
