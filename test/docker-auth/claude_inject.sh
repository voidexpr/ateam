#!/usr/bin/env bash
set -euo pipefail

# Copy PATH/.claude/, PATH/.claude.json, and PATH/secrets.env into a running container.
#
# The input directory should contain:
#   PATH/.claude/       (config directory, required)
#   PATH/.claude.json   (account state, optional)
#   PATH/secrets.env    (OAuth token for headless agents, optional)
#
# Usage:
#   claude_inject.sh --container NAME --path INPUT_PATH
#
# Options:
#   --container NAME   Running container to copy into (required)
#   --path PATH        Local directory containing .claude/ and .claude.json (required)
#   --force            Overwrite ~/.claude in container if it already exists
#   -h, --help         Show this help

container=""
input=""
force=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --container) container="$2"; shift 2 ;;
        --path)      input="$2"; shift 2 ;;
        --force)     force=true; shift ;;
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

if [[ -z "$input" ]]; then
    echo "Error: --path is required" >&2
    exit 1
fi

if [[ ! -d "$input/.claude" ]]; then
    echo "Error: $input/.claude does not exist or is not a directory" >&2
    exit 1
fi

# Verify container is running
if ! docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null | grep -q true; then
    echo "Error: container '$container' is not running" >&2
    exit 1
fi

# Detect container user
user=$(docker exec "$container" id -un 2>/dev/null || echo "root")

# Check if ~/.claude already exists in container
if docker exec "$container" test -d /home/agent/.claude; then
    if ! $force; then
        echo "Error: /home/agent/.claude already exists in container '$container' (use --force to overwrite)" >&2
        exit 1
    fi
    # Clear contents instead of removing the dir (it may be a volume mount)
    docker exec "$container" sh -c 'rm -rf /home/agent/.claude/* /home/agent/.claude/.[!.]* 2>/dev/null || true'
fi

# Copy .claude/ directory contents
docker exec "$container" mkdir -p /home/agent/.claude
docker cp "$input/.claude/." "$container:/home/agent/.claude/"
echo "Copied $input/.claude/ into ~/.claude/ in container '$container'"

# Copy .claude.json if it exists
if [[ -f "$input/.claude.json" ]]; then
    docker cp "$input/.claude.json" "$container:/home/agent/.claude.json"
    echo "Copied $input/.claude.json into ~/.claude.json in container '$container'"
fi

# Copy secrets.env if it exists
if [[ -f "$input/secrets.env" ]]; then
    docker exec "$container" mkdir -p /home/agent/.ateamorg
    docker cp "$input/secrets.env" "$container:/home/agent/.ateamorg/secrets.env"
    echo "Copied $input/secrets.env into ~/.ateamorg/secrets.env in container '$container'"
fi

# Fix ownership
docker exec "$container" chown -R "$user:$user" /home/agent/.claude 2>/dev/null || true
docker exec "$container" chown "$user:$user" /home/agent/.claude.json 2>/dev/null || true
docker exec "$container" chown -R "$user:$user" /home/agent/.ateamorg 2>/dev/null || true
