#!/usr/bin/env bash
# Shared variables and helpers for the ATeam PoC scripts.

set -euo pipefail

WORK_DIR="$HOME/ateam-poc"
PROJECT_REPO="git@github.com-nicad:nicad/minimon.git"
DOCKER_IMAGE="ateam-poc"
BRANCH="main"

# Budget caps (USD)
BUDGET_AUDIT=2.00
BUDGET_IMPLEMENT=3.00
BUDGET_COORDINATOR=0.50

# Docker resource limits
DOCKER_CPUS=2
DOCKER_MEMORY=4g

# Require ANTHROPIC_API_KEY
if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "ERROR: ANTHROPIC_API_KEY is not set" >&2
  exit 1
fi

# --- helpers ---

# Run claude inside the ateam-poc container.
# Usage: run_agent <container-name> <prompt-file> <budget> [extra docker flags...]
run_agent() {
  local name="$1" prompt_file="$2" budget="$3"
  shift 3

  docker run --rm \
    --name "$name" \
    --cpus="$DOCKER_CPUS" --memory="$DOCKER_MEMORY" \
    -v "$WORK_DIR/code:/workspace:rw" \
    -v "$WORK_DIR/data:/data:rw" \
    -v "$WORK_DIR/artifacts:/artifacts:rw" \
    -v "$WORK_DIR/output:/output:rw" \
    -v "$prompt_file:/agent-data/prompt.md:ro" \
    -e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY" \
    "$@" \
    "$DOCKER_IMAGE" \
    bash -c '
      claude -p "$(cat /agent-data/prompt.md)" \
        --dangerously-skip-permissions \
        --output-format stream-json \
        --max-budget-usd '"$budget"' \
        2>/output/stderr.log \
        | tee /output/stream.jsonl
      echo $? > /output/exit_code
    '
}

# Remove transient output files between runs, keep reports.
clean_stream_output() {
  rm -f "$WORK_DIR/output/stream.jsonl" \
       "$WORK_DIR/output/exit_code" \
       "$WORK_DIR/output/stderr.log"
}

# Print a section header.
header() {
  echo ""
  echo "=== $* ==="
  echo ""
}
