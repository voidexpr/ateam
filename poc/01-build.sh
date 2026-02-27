#!/usr/bin/env bash
# Build the Docker image and verify Claude Code is installed.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

header "Building Docker image: $DOCKER_IMAGE"

docker build -t "$DOCKER_IMAGE" "$SCRIPT_DIR"

header "Verifying claude is available"
docker run --rm "$DOCKER_IMAGE" claude --version

echo ""
echo "Image ready."
