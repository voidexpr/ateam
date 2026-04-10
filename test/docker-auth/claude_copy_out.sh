#!/usr/bin/env bash
set -euo pipefail

# Copy ~/.claude/ and ~/.claude.json from a running container to a local directory.
# Does NOT copy secrets.env (manually maintained, contains OAuth token).
#
# The target directory will contain:
#   PATH/.claude/       (config directory)
#   PATH/.claude.json   (account state)
#
# Usage:
#   claude_copy_out.sh --container NAME --path TARGET_PATH
#
# Options:
#   --container NAME   Running container to copy from (required)
#   --path PATH        Local directory to copy into (required, created if needed)
#   -h, --help         Show this help

container=""
target=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --container) container="$2"; shift 2 ;;
        --path)      target="$2"; shift 2 ;;
        -h|--help)
            sed -n '3,/^$/{s/^# *//;p;}' "$0"
            exit 0
            ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$container" ]]; then
    echo "Error: --container is required" >&2
    exit 1
fi

if [[ -z "$target" ]]; then
    echo "Error: --path is required" >&2
    exit 1
fi

# Verify container is running
if ! docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null | grep -q true; then
    echo "Error: container '$container' is not running" >&2
    exit 1
fi

# Verify ~/.claude exists in container
if ! docker exec "$container" test -d /home/agent/.claude; then
    echo "Error: /home/agent/.claude does not exist in container '$container'" >&2
    exit 1
fi

mkdir -p "$target/.claude"

# Copy ~/.claude/ contents
docker cp "$container:/home/agent/.claude/." "$target/.claude/"
echo "Copied ~/.claude/ from '$container' to $target/.claude/"

# Copy ~/.claude.json if it exists
if docker exec "$container" test -f /home/agent/.claude.json; then
    docker cp "$container:/home/agent/.claude.json" "$target/.claude.json"
    echo "Copied ~/.claude.json from '$container' to $target/.claude.json"
else
    echo "Warning: ~/.claude.json not found in container '$container'"
fi

# secrets.env is NOT copied — it is manually maintained and contains
# the OAuth token from 'claude setup-token'. Overwriting it could lose
# a token that can't be automatically regenerated.
if [[ ! -f "$target/secrets.env" ]]; then
    echo ""
    echo "Note: no secrets.env in $target"
    echo "  If headless agents are needed, generate an OAuth token inside the container:"
    echo "    claude setup-token"
    echo "  Then save it:"
    echo "    ateam secret CLAUDE_CODE_OAUTH_TOKEN --scope org --set"
fi
