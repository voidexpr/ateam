#!/usr/bin/env bash
# Shared variables and helpers for the ATeam PoC scripts.
#
# Auth: set CLAUDE_CODE_OAUTH_TOKEN in your environment.
# Generate one with: claude setup-token

set -euo pipefail

WORK_DIR="$PWD"
PROJECT_REPO="git@github.com-nicad:nicad/minimon.git"
DOCKER_IMAGE="ateam-poc"
BRANCH="main"

# Docker resource limits
DOCKER_CPUS=2
DOCKER_MEMORY=4g

if [[ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
  echo "ERROR: CLAUDE_CODE_OAUTH_TOKEN is not set" >&2
  echo "Generate one with: claude setup-token" >&2
  exit 1
fi

# --- helpers ---

# Run claude inside the ateam-poc container.
# Usage: run_agent <container-name> <prompt-file> [extra docker flags...]
run_agent() {
  local name="$1" prompt_file="$2"
  shift 2

  mkdir -p "$WORK_DIR/output"

  docker run --rm \
    --name "$name" \
    --cpus="$DOCKER_CPUS" --memory="$DOCKER_MEMORY" \
    -v "$HOME:$HOME:ro" \
    -v "$HOME/.claude:$HOME/.claude:rw" \
    -v "$HOME/.claude.json:$HOME/.claude.json:rw" \
    -v "$WORK_DIR/code:/workspace:rw" \
    -v "$WORK_DIR/data:/data:rw" \
    -v "$WORK_DIR/artifacts:/artifacts:rw" \
    -v "$WORK_DIR/output:/output:rw" \
    -v "$prompt_file:/agent-data/prompt.md:ro" \
    -e "HOME=$HOME" \
    -e "CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
    "$@" \
    "$DOCKER_IMAGE" \
    bash -c '
      claude -p "$(cat /agent-data/prompt.md)" \
        --dangerously-skip-permissions \
        --output-format stream-json \
        --verbose \
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
