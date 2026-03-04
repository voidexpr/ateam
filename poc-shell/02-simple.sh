#!/usr/bin/env bash
# Run a trivial claude task in Docker to produce stream-json for testing step 05.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

clean_stream_output
mkdir -p "$WORK_DIR/output"

PROMPT="List the files in /workspace using the Bash tool, then write a one-paragraph summary of what this project does to /output/report.md"

header "Running simple task"

docker run --rm \
  --name "ateam-simple" \
  --cpus="$DOCKER_CPUS" --memory="$DOCKER_MEMORY" \
  -v "$HOME:$HOME:ro" \
  -v "$HOME/.claude:$HOME/.claude:rw" \
  -v "$HOME/.claude.json:$HOME/.claude.json:rw" \
  -v "$WORK_DIR/code:/workspace:ro" \
  -v "$WORK_DIR/output:/output:rw" \
  -e "HOME=$HOME" \
  -e "CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
  "$DOCKER_IMAGE" \
  bash -c "claude -p '$PROMPT' \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose \
    2>/output/stderr.log \
    > /output/stream.jsonl;
    echo \$? > /output/exit_code"

header "Results"

echo "Exit code: $(cat "$WORK_DIR/output/exit_code" 2>/dev/null || echo 'missing')"
echo "Stream lines: $(wc -l < "$WORK_DIR/output/stream.jsonl" 2>/dev/null || echo '0')"
echo ""

if [[ -f "$WORK_DIR/output/report.md" ]]; then
  echo "--- report.md ---"
  cat "$WORK_DIR/output/report.md"
fi

if [[ -s "$WORK_DIR/output/stderr.log" ]]; then
  echo ""
  echo "--- stderr (last 10 lines) ---"
  tail -10 "$WORK_DIR/output/stderr.log"
fi
