#!/usr/bin/env bash
set -euo pipefail

# Start a Docker container with persistent ~/.claude volume.
#
# Usage:
#   start.sh --name NAME [options] [-- CLAUDE_ARGS...]
#
# Options:
#   --name NAME          Container name (required, also used as default volume name)
#   --volume VOL         Named volume or absolute host path for ~/.claude (default: claude-NAME)
#   --workspace DIR      Host directory to mount at /workspace (default: cwd)
#   --image IMAGE        Docker image (default: ateam-auth-test)
#   --interactive        Start interactive shell (default)
#   --detach             Run in background
#   --rm                 Remove container on exit (default: true)
#   --keep               Don't remove container on exit
#   --shared-claude DIR  Host directory to mount at /home/agent/shared_claude
#
# A persistent ~/data directory is always mounted at ~/.ateam-containers/NAME/data
# on the host, surviving container restarts.
#
# Everything after -- is passed to claude (e.g., -- -p "hello")

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

name=""
volume=""
workspace="$(pwd)"
image="ateam-auth-test"
interactive=true
detach=false
auto_rm=true
shared_claude=""
claude_args=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)        name="$2"; shift 2 ;;
        --volume)      volume="$2"; shift 2 ;;
        --workspace)   workspace="$2"; shift 2 ;;
        --image)       image="$2"; shift 2 ;;
        --interactive) interactive=true; shift ;;
        --detach)      detach=true; interactive=false; shift ;;
        --rm)          auto_rm=true; shift ;;
        --keep)        auto_rm=false; shift ;;
        --shared-claude) shared_claude="$2"; shift 2 ;;
        -h|--help)
            sed -n '3,/^$/{s/^# *//;p;}' "$0"
            exit 0
            ;;
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

# Ensure volume exists (skip for absolute host paths)
if [[ "$volume" == /* ]]; then
    mkdir -p "$volume"
else
    docker volume inspect "$volume" &>/dev/null || {
        echo "Creating volume $volume..."
        docker volume create "$volume"
    }
fi

workspace="$(cd "$workspace" && pwd)"

# Check for ateam linux binary
ateam_build_dir="$REPO_ROOT/build"
if [[ ! -f "$ateam_build_dir/ateam-linux-amd64" ]]; then
    echo "Error: build/ateam-linux-amd64 not found" >&2
    echo "  Run: ./test/docker-auth/build.sh" >&2
    exit 1
fi

# Build docker run args
host_path_mode=false
[[ "$volume" == /* ]] && host_path_mode=true
args=(run)
$auto_rm && args+=(--rm)
args+=(--name "$name")
if $host_path_mode; then
    # Host path: mount .claude/, .claude.json, and secrets.env separately
    args+=(-v "$volume/.claude:/home/agent/.claude")
    if [[ -f "$volume/.claude.json" ]]; then
        args+=(-v "$volume/.claude.json:/home/agent/.claude.json")
    fi
    if [[ -f "$volume/secrets.env" ]]; then
        args+=(-v "$volume/secrets.env:/home/agent/.ateamorg/secrets.env")
    fi
else
    # Named volume: single mount for .claude/
    args+=(-v "$volume:/home/agent/.claude")
fi
# Persistent ~/data directory (per-container name, survives container restarts)
data_dir="$HOME/.ateam-containers/$name/data"
mkdir -p "$data_dir"
args+=(-v "$data_dir:/home/agent/data")

args+=(-v "$workspace:/workspace")
args+=(-v "$ateam_build_dir:/opt/ateam:ro")

ateamorg_dir="$(ateam env --print-org)"

args+=(-v "$ateamorg_dir:/.ateamorg:ro")

claude_shared_dir="claude_linux_shared"
ateamorg_claude_shared_dir="$ateamorg_dir/$claude_shared_dir"
if [[ -d "$ateamorg_claude_shared_dir" ]]; then
    args+=(-v "$ateamorg_claude_shared_dir:/.ateamorg/$claude_shared_dir:ro")
fi

if [[ -e "$ateamorg_claude_shared_dir/secrets.env" ]]; then
    args+=(-v "$ateamorg_claude_shared_dir/secrets.env:/home/agent/.config/ateam/secrets.env:ro")
fi

if [[ -n "$shared_claude" ]]; then
    shared_claude="$(cd "$shared_claude" && pwd)"
    args+=(-v "$shared_claude:/home/agent/shared_claude")
fi

# Sync timezone with host
if [[ -f /etc/localtime ]]; then
    args+=(-v "/etc/localtime:/etc/localtime:ro")
fi
args+=(-e "TZ=${TZ:-$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || echo UTC)}")

echo "Container:  $name"
echo "Volume:     $volume"
echo "Data:       $data_dir"
echo "Workspace:  $workspace"
[[ -n "$shared_claude" ]] && echo "Shared:     $shared_claude"
echo "Image:      $image"

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
    echo "Run 'claude' inside the container to start an interactive session."
    echo "Run 'claude auth login' if you need to authenticate."
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
