#!/usr/bin/env bash
set -euo pipefail

# Build the ateam Docker image for auth testing and cross-compile ateam.
# The ateam binary is NOT baked into the image — it's mounted at runtime
# via start.sh so recompiles are picked up without rebuilding the image.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IMAGE_NAME="${1:-ateam-auth-test}"

# Detect target arch (match Docker's platform)
DOCKER_ARCH=$(docker info --format '{{.Architecture}}' 2>/dev/null || echo "aarch64")
case "$DOCKER_ARCH" in
    x86_64)  GOARCH=amd64 ;;
    aarch64) GOARCH=arm64 ;;
    *)       GOARCH=amd64 ;;
esac

# Cross-compile ateam for linux
mkdir -p "$REPO_ROOT/build"
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION=$(cat "$REPO_ROOT/VERSION" 2>/dev/null || echo dev)
GIT_COMMIT=$(cd "$REPO_ROOT" && git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS="-X github.com/ateam/cmd.BuildTime=$BUILD_TIME -X github.com/ateam/cmd.Version=$VERSION -X github.com/ateam/cmd.GitCommit=$GIT_COMMIT"

echo "Cross-compiling ateam for linux/$GOARCH..."
make -C ../.. companion
# GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$REPO_ROOT/build/ateam-linux-amd64" "$REPO_ROOT"

# Build base image (no ateam binary — mounted at runtime by start.sh)
echo "Building image $IMAGE_NAME..."
docker build \
    --build-arg "USER_UID=$(id -u)" \
    -t "$IMAGE_NAME" \
    -f "$SCRIPT_DIR/Dockerfile" "$REPO_ROOT"

echo ""
echo "Image:   $IMAGE_NAME"
echo "Claude:  $(docker run --rm "$IMAGE_NAME" claude --version 2>/dev/null || echo 'unknown')"
echo "Ateam:   build/ateam-linux-amd64 (mounted at runtime)"
