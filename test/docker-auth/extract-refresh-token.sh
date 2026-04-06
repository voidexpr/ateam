#!/usr/bin/env bash
set -euo pipefail

# Extract the OAuth refresh token from a Docker container or volume.
#
# Usage:
#   extract-refresh-token.sh --volume VOLUME_NAME
#   extract-refresh-token.sh --container CONTAINER_NAME
#
# Outputs the raw refresh token to stdout. Pipe to ateam secret:
#   ./extract-refresh-token.sh --volume claude-login | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set

image="ateam-auth-test"
volume=""
container=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --volume)    volume="$2"; shift 2 ;;
        --container) container="$2"; shift 2 ;;
        --image)     image="$2"; shift 2 ;;
        *)           echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

extract_cmd='
import json, sys
try:
    data = json.load(open(sys.argv[1]))
    token = data.get("claudeAiOauth", {}).get("refreshToken", "")
    if not token:
        print("Error: no refreshToken in credentials file", file=sys.stderr)
        sys.exit(1)
    print(token, end="")
except (json.JSONDecodeError, FileNotFoundError) as e:
    print(f"Error: {e}", file=sys.stderr)
    sys.exit(1)
'

if [[ -n "$container" ]]; then
    docker exec "$container" python3 -c "$extract_cmd" /home/agent/.claude/.credentials.json
elif [[ -n "$volume" ]]; then
    docker run --rm -v "$volume:/home/agent/.claude" "$image" \
        python3 -c "$extract_cmd" /home/agent/.claude/.credentials.json
else
    echo "Error: specify --volume or --container" >&2
    exit 1
fi
