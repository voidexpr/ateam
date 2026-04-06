#!/usr/bin/env bash
set -euo pipefail

# Start a Docker container with persistent ~/.claude volume.
#
# Usage:
#   start.sh --name NAME [options] [-- CLAUDE_ARGS...]
#
# Options:
#   --name NAME          Container name (required, also used as default volume name)
#   --volume VOL         Named volume for ~/.claude (default: claude-NAME)
#   --workspace DIR      Host directory to mount at /workspace (default: cwd)
#   --image IMAGE        Docker image (default: ateam-auth-test)
#   --interactive        Start interactive shell (default)
#   --oauth              Forward CLAUDE_CODE_OAUTH_TOKEN (from ateam secret)
#   --login              Bootstrap interactive credentials via refresh token (from ateam secret)
#   --detach             Run in background
#   --rm                 Remove container on exit (default: true)
#   --keep               Don't remove container on exit
#
# Everything after -- is passed to claude (e.g., -- -p "hello")

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

name=""
volume=""
workspace="$(pwd)"
image="ateam-auth-test"
interactive=true
use_oauth=false
use_login=false
detach=false
auto_rm=true
claude_args=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)        name="$2"; shift 2 ;;
        --volume)      volume="$2"; shift 2 ;;
        --workspace)   workspace="$2"; shift 2 ;;
        --image)       image="$2"; shift 2 ;;
        --interactive) interactive=true; shift ;;
        --oauth)       use_oauth=true; shift ;;
        --login)       use_login=true; shift ;;
        --detach)      detach=true; interactive=false; shift ;;
        --rm)          auto_rm=true; shift ;;
        --keep)        auto_rm=false; shift ;;
        --)            shift; claude_args=("$@"); break ;;
        *)             echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$name" ]]; then
    echo "Error: --name is required" >&2
    exit 1
fi

if [[ -z "$volume" ]]; then
    volume="claude-$name"
fi

# Resolve ateam binary
resolve_ateam() {
    local bin="$REPO_ROOT/ateam"
    if [[ ! -x "$bin" ]]; then
        bin="$(command -v ateam 2>/dev/null || true)"
    fi
    if [[ -z "$bin" ]]; then
        echo "Error: ateam binary not found (build with 'make build' or ensure it's in PATH)" >&2
        exit 1
    fi
    echo "$bin"
}

# Resolve oauth token if requested
oauth_token=""
if $use_oauth; then
    ateam_bin="$(resolve_ateam)"
    oauth_token=$("$ateam_bin" secret CLAUDE_CODE_OAUTH_TOKEN --get 2>/dev/null) || true
    if [[ -z "$oauth_token" ]]; then
        echo "Error: CLAUDE_CODE_OAUTH_TOKEN not found in ateam secrets" >&2
        echo "  Set it with: ateam secret CLAUDE_CODE_OAUTH_TOKEN --set" >&2
        exit 1
    fi
fi

# Resolve refresh token if --login requested
refresh_token=""
if $use_login; then
    ateam_bin="$(resolve_ateam)"
    refresh_token=$("$ateam_bin" secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --get 2>/dev/null) || true
    if [[ -z "$refresh_token" ]]; then
        echo "Error: CLAUDE_CODE_OAUTH_REFRESH_TOKEN not found in ateam secrets" >&2
        echo "  First do an interactive login, then extract and store the refresh token:" >&2
        echo "    ./test/docker-auth/start.sh --name login --interactive" >&2
        echo "    ./test/docker-auth/extract-refresh-token.sh --volume claude-login | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set" >&2
        exit 1
    fi
fi

# Ensure volume exists
docker volume inspect "$volume" &>/dev/null || {
    echo "Creating volume $volume..."
    docker volume create "$volume"
}

workspace="$(cd "$workspace" && pwd)"

# Check for ateam linux binary
ateam_build_dir="$REPO_ROOT/build"
if [[ ! -f "$ateam_build_dir/ateam-linux-amd64" ]]; then
    echo "Error: build/ateam-linux-amd64 not found" >&2
    echo "  Run: ./test/docker-auth/build.sh" >&2
    exit 1
fi

# Build docker run args
args=(run)
$auto_rm && args+=(--rm)
args+=(--name "$name")
args+=(-v "$volume:/home/agent/.claude")
args+=(-v "$workspace:/workspace")
args+=(-v "$ateam_build_dir:/opt/ateam:ro")

# Sync timezone with host
if [[ -f /etc/localtime ]]; then
    args+=(-v "/etc/localtime:/etc/localtime:ro")
fi
args+=(-e "TZ=${TZ:-$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || echo UTC)}")

if [[ -n "$oauth_token" ]]; then
    args+=(-e "CLAUDE_CODE_OAUTH_TOKEN=$oauth_token")
fi

if [[ -n "$refresh_token" ]]; then
    args+=(-e "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=$refresh_token")
    args+=(-e "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference")
fi

echo "Container:  $name"
echo "Volume:     $volume"
echo "Workspace:  $workspace"
echo "Image:      $image"
if [[ -n "$oauth_token" ]]; then
    echo "OAuth:      yes (from ateam secret)"
fi
if [[ -n "$refresh_token" ]]; then
    echo "Login:      yes (refresh token from ateam secret)"
fi

if [[ ${#claude_args[@]} -gt 0 ]]; then
    echo "Claude args: ${claude_args[*]}"
    echo "---"

    if $detach; then
        args+=(-d)
    fi

    docker "${args[@]}" "$image" claude "${claude_args[@]}"
elif $interactive; then
    echo "Mode:       interactive"
    echo "---"
    if [[ -n "$refresh_token" ]]; then
        echo "Claude will auto-authenticate using the refresh token."
    else
        echo "Run 'claude' inside the container to start an interactive session."
        echo "Run 'claude auth login' if you need to authenticate."
    fi
    echo ""

    args+=(-it)
    docker "${args[@]}" "$image" bash
else
    echo "Mode:       detached"
    echo "---"

    args+=(-d)
    docker "${args[@]}" "$image" sleep infinity
    echo "Container $name started. Use: docker exec -it $name bash"
fi
