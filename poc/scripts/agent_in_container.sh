#!/usr/bin/env bash
# Consolidated agent runner: build image, run claude in Docker, monitor, summarize.
# No external dependencies beyond docker and jq.

set -euo pipefail

# --- A. Defaults and embedded Dockerfile ---

DEFAULT_IMAGE="ateam-agent"
DEFAULT_CPUS=2
DEFAULT_MEMORY="4g"
PROMPT=""
WORKSPACE=""
DOCKERFILE_PATH=""
OUTPUT_DIR=""
IMAGE_NAME=""
CONTAINER_NAME=""
CPUS=""
MEMORY=""
QUIET=false

EMBEDDED_DOCKERFILE='FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ARG USER_UID=1000
RUN useradd -m -u $USER_UID agent

RUN mkdir -p /data /artifacts /output /agent-data \
    && chown -R agent:agent /data /artifacts /output /agent-data

USER agent
WORKDIR /workspace
'

usage() {
  cat <<'EOF'
Usage: agent_in_container.sh [OPTIONS] -p <prompt> -w <workspace>

  -p, --prompt <string|file>   Prompt text or path to .md file (required)
  -w, --workspace <path>       Directory to mount as /workspace (required)
  -d, --dockerfile <path>      Custom Dockerfile (default: embedded)
  -o, --output <path>          Output directory (default: ./output)
  -i, --image <name>           Docker image name (default: ateam-agent)
  -n, --name <name>            Container name (default: ateam-<timestamp>)
      --cpus <n>               CPU limit (default: 2)
      --memory <size>          Memory limit (default: 4g)
  -q, --quiet                  Suppress live progress
  -h, --help                   Show help
EOF
  exit 0
}

# --- B. Argument parsing ---

while [[ $# -gt 0 ]]; do
  case "$1" in
    -p|--prompt)   PROMPT="$2"; shift 2 ;;
    -w|--workspace) WORKSPACE="$2"; shift 2 ;;
    -d|--dockerfile) DOCKERFILE_PATH="$2"; shift 2 ;;
    -o|--output)   OUTPUT_DIR="$2"; shift 2 ;;
    -i|--image)    IMAGE_NAME="$2"; shift 2 ;;
    -n|--name)     CONTAINER_NAME="$2"; shift 2 ;;
    --cpus)        CPUS="$2"; shift 2 ;;
    --memory)      MEMORY="$2"; shift 2 ;;
    -q|--quiet)    QUIET=true; shift ;;
    -h|--help)     usage ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# Apply defaults after parsing
IMAGE_NAME="${IMAGE_NAME:-$DEFAULT_IMAGE}"
CPUS="${CPUS:-$DEFAULT_CPUS}"
MEMORY="${MEMORY:-$DEFAULT_MEMORY}"
CONTAINER_NAME="${CONTAINER_NAME:-ateam-$(date +%s)}"

# Validate required args
if [[ -z "$PROMPT" ]]; then
  echo "ERROR: --prompt is required" >&2
  exit 1
fi
if [[ -z "$WORKSPACE" ]]; then
  echo "ERROR: --workspace is required" >&2
  exit 1
fi
WORKSPACE="$(cd "$WORKSPACE" && pwd)"
if [[ ! -d "$WORKSPACE" ]]; then
  echo "ERROR: workspace is not a directory: $WORKSPACE" >&2
  exit 1
fi
if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required but not installed" >&2
  exit 1
fi

# Resolve output dir (default: ./output relative to cwd at invocation)
OUTPUT_DIR="${OUTPUT_DIR:-./output}"
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

# --- Temp dir for build artifacts ---

TMPDIR_ROOT="$(mktemp -d)"

# Resolve prompt: .md file → use directly, otherwise write string to temp file
PROMPT_FILE=""
if [[ -f "$PROMPT" && "$PROMPT" == *.md ]]; then
  PROMPT_FILE="$(cd "$(dirname "$PROMPT")" && pwd)/$(basename "$PROMPT")"
else
  PROMPT_FILE="$TMPDIR_ROOT/prompt.md"
  printf '%s' "$PROMPT" > "$PROMPT_FILE"
fi

# --- C. Auth resolution ---

resolve_auth() {
  if [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
    return
  fi
  local search_dirs=("$PWD" "$WORKSPACE" "$HOME")
  for dir in "${search_dirs[@]}"; do
    if [[ -f "$dir/.claude_token" ]]; then
      CLAUDE_CODE_OAUTH_TOKEN="$(cat "$dir/.claude_token")"
      export CLAUDE_CODE_OAUTH_TOKEN
      return
    fi
  done
  echo "ERROR: No auth token found." >&2
  echo "Either set CLAUDE_CODE_OAUTH_TOKEN or create .claude_token in one of:" >&2
  echo "  $PWD  |  $WORKSPACE  |  $HOME" >&2
  echo "Generate a token with: claude setup-token" >&2
  exit 1
}
resolve_auth

# --- D. Image management ---

# Write Dockerfile content to a temp file if no custom one provided
if [[ -n "$DOCKERFILE_PATH" ]]; then
  DOCKERFILE_CONTENT="$(cat "$DOCKERFILE_PATH")"
else
  DOCKERFILE_CONTENT="$EMBEDDED_DOCKERFILE"
fi

DOCKERFILE_HASH="$(printf '%s' "$DOCKERFILE_CONTENT" | shasum -a 256 | cut -c1-12)"
IMAGE_TAG="${IMAGE_NAME}:${DOCKERFILE_HASH}"

echo "+ docker image inspect $IMAGE_TAG" >&2
if docker image inspect "$IMAGE_TAG" &>/dev/null; then
  echo "Image $IMAGE_TAG exists, skipping build." >&2
else
  BUILD_DIR="$TMPDIR_ROOT/build"
  mkdir -p "$BUILD_DIR"
  printf '%s' "$DOCKERFILE_CONTENT" > "$BUILD_DIR/Dockerfile"
  echo "+ docker build --build-arg USER_UID=$(id -u) -t $IMAGE_TAG $BUILD_DIR" >&2
  docker build --build-arg USER_UID="$(id -u)" -t "$IMAGE_TAG" "$BUILD_DIR"
fi

# --- E. Trap-based cleanup ---

MONITOR_PID=""
CONTAINER_STARTED=false

cleanup() {
  if [[ -n "$MONITOR_PID" ]] && kill -0 "$MONITOR_PID" 2>/dev/null; then
    kill "$MONITOR_PID" 2>/dev/null || true
    wait "$MONITOR_PID" 2>/dev/null || true
  fi
  if $CONTAINER_STARTED; then
    echo "+ docker kill $CONTAINER_NAME" >&2
    docker kill "$CONTAINER_NAME" 2>/dev/null || true
    echo "+ docker rm -f $CONTAINER_NAME" >&2
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR_ROOT"
}
trap cleanup EXIT INT TERM

# --- F. Live monitor (background) ---

live_monitor() {
  local stream="$1"
  local start_ts="$SECONDS"
  local tool_count=0
  local event_count=0

  # Wait for stream file to appear
  while [[ ! -f "$stream" ]]; do
    sleep 0.5
  done

  tail -f "$stream" 2>/dev/null | while IFS= read -r line; do
    event_count=$((event_count + 1))
    local elapsed=$(( SECONDS - start_ts ))
    local mm=$(( elapsed / 60 ))
    local ss=$(( elapsed % 60 ))
    local ts
    ts=$(printf '%02d:%02d' "$mm" "$ss")

    local etype
    etype=$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null) || continue

    case "$etype" in
      system)
        local model
        model=$(printf '%s' "$line" | jq -r '.subtype // empty' 2>/dev/null)
        if [[ "$model" == "init" ]]; then
          model=$(printf '%s' "$line" | jq -r '.session_id // "unknown"' 2>/dev/null)
          echo "[$ts] init session=$model" >&2
        fi
        ;;
      assistant)
        # Check for tool_use content blocks
        local tools
        tools=$(printf '%s' "$line" | jq -r '.message.content[]? | select(.type == "tool_use") | .name' 2>/dev/null) || true
        if [[ -n "$tools" ]]; then
          while IFS= read -r tname; do
            tool_count=$((tool_count + 1))
            echo "[$ts] tool #${tool_count}: $tname" >&2
          done <<< "$tools"
        else
          echo "[$ts] events=$event_count tools=$tool_count thinking..." >&2
        fi
        ;;
      result)
        local cost duration
        cost=$(printf '%s' "$line" | jq -r '.total_cost_usd // .cost_usd // "?"' 2>/dev/null)
        if [[ "$cost" != "?" ]]; then cost=$(printf '%.2f' "$cost"); fi
        duration=$(printf '%s' "$line" | jq -r '.duration_ms // "?"' 2>/dev/null)
        echo "[$ts] done cost=\$${cost} duration=${duration}ms" >&2
        break
        ;;
    esac
  done
}

# --- G. Docker run ---

# Clear previous run artifacts
rm -f "$OUTPUT_DIR/stream.jsonl" "$OUTPUT_DIR/exit_code" "$OUTPUT_DIR/stderr.log"

# Start monitor in background (unless quiet)
if ! $QUIET; then
  live_monitor "$OUTPUT_DIR/stream.jsonl" &
  MONITOR_PID=$!
fi

CONTAINER_STARTED=true

DOCKER_RUN_CMD=(docker run --rm \
  --name "$CONTAINER_NAME" \
  --cpus="$CPUS" --memory="$MEMORY" \
  -v "$HOME:$HOME:ro" \
  -v "$HOME/.claude:$HOME/.claude:rw" \
  -v "$HOME/.claude.json:$HOME/.claude.json:rw" \
  -v "$WORKSPACE:/workspace:rw" \
  -v "$OUTPUT_DIR:/output:rw" \
  -v "$PROMPT_FILE:/agent-data/prompt.md:ro" \
  -e "HOME=$HOME" \
  -e "CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
  "$IMAGE_TAG")

echo "+ ${DOCKER_RUN_CMD[*]} bash -c 'claude -p ... --dangerously-skip-permissions --output-format stream-json --verbose'" >&2

"${DOCKER_RUN_CMD[@]}" \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --verbose \
      2>/output/stderr.log \
      > /output/stream.jsonl
    echo $? > /output/exit_code
  ' &
DOCKER_PID=$!

# Wait for container to appear then show docker ps
for _ in $(seq 1 20); do
  if docker inspect "$CONTAINER_NAME" &>/dev/null; then
    echo "+ docker ps --filter name=$CONTAINER_NAME" >&2
    docker ps --filter "name=$CONTAINER_NAME" >&2
    break
  fi
  sleep 0.25
done

wait "$DOCKER_PID" || true
CONTAINER_STARTED=false

# Give monitor a moment to finish parsing
sleep 1

# --- H. Post-run summary ---

STREAM="$OUTPUT_DIR/stream.jsonl"
EXIT_CODE="$(cat "$OUTPUT_DIR/exit_code" 2>/dev/null || echo '?')"

if [[ ! -f "$STREAM" ]]; then
  echo "No stream output found." >&2
  exit "${EXIT_CODE:-1}"
fi

# Duration and cost from result event
RESULT_JSON="$(jq -s '[.[] | select(.type == "result")] | last' "$STREAM" 2>/dev/null || echo '{}')"
COST_RAW="$(echo "$RESULT_JSON" | jq -r '.total_cost_usd // .cost_usd // "?"')"
if [[ "$COST_RAW" != "?" ]]; then
  COST="$(printf '%.2f' "$COST_RAW")"
else
  COST="?"
fi
DURATION_MS="$(echo "$RESULT_JSON" | jq -r '.duration_ms // "?"')"
NUM_TURNS="$(echo "$RESULT_JSON" | jq -r '.num_turns // "?"')"
IS_ERROR="$(echo "$RESULT_JSON" | jq -r '.is_error // false')"

# Human-readable duration
if [[ "$DURATION_MS" != "?" ]]; then
  DURATION_S=$(( DURATION_MS / 1000 ))
  DURATION_HUMAN="$(( DURATION_S / 60 ))m $(( DURATION_S % 60 ))s"
else
  DURATION_HUMAN="?"
fi

# Token breakdown
INPUT_TOKENS="$(echo "$RESULT_JSON" | jq -r '.usage.input_tokens // "?"')"
OUTPUT_TOKENS="$(echo "$RESULT_JSON" | jq -r '.usage.output_tokens // "?"')"
CACHE_READ="$(echo "$RESULT_JSON" | jq -r '.usage.cache_read_input_tokens // .usage.cache_creation_input_tokens // "?"')"

# Event type distribution
EVENT_DIST="$(jq -r '.type' "$STREAM" | sort | uniq -c | sort -rn)"

# Tool usage breakdown (from assistant messages with tool_use content blocks)
TOOL_DIST="$(jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "tool_use") | .name' "$STREAM" 2>/dev/null | sort | uniq -c | sort -rn)"

# Extract the agent's final text response as report.md
# Take the last assistant message's text content blocks
AGENT_RESPONSE="$(jq -s '
  [.[] | select(.type == "assistant") | .message.content[]?
   | select(.type == "text") | .text] | last // empty
' "$STREAM" 2>/dev/null)"

# jq returns a JSON string (with quotes and escapes), decode it
if [[ -n "$AGENT_RESPONSE" && "$AGENT_RESPONSE" != "null" ]]; then
  echo "$AGENT_RESPONSE" | jq -r '.' > "$OUTPUT_DIR/report.md"
fi

REPORT_LINE=""
if [[ -f "$OUTPUT_DIR/report.md" && -s "$OUTPUT_DIR/report.md" ]]; then
  REPORT_LINES="$(wc -l < "$OUTPUT_DIR/report.md" | tr -d ' ')"
  REPORT_LINE="$OUTPUT_DIR/report.md ($REPORT_LINES lines)"
else
  REPORT_LINE="(not created)"
fi

cat <<EOF
=== Run Summary ===

Exit code:  $EXIT_CODE
Duration:   $DURATION_HUMAN
Cost:       \$$COST
Turns:      $NUM_TURNS
Error:      $IS_ERROR
Tokens:     input=$INPUT_TOKENS output=$OUTPUT_TOKENS cache_read=$CACHE_READ

Events:
$EVENT_DIST

Tools used:
$TOOL_DIST

Report:     $REPORT_LINE
EOF

if [[ -f "$OUTPUT_DIR/report.md" && -s "$OUTPUT_DIR/report.md" ]]; then
  echo ""
  echo "=== Agent Response ==="
  echo ""
  cat "$OUTPUT_DIR/report.md"
fi

exit "${EXIT_CODE:-0}"
