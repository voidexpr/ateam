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
GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$REPO_ROOT/build/ateam-linux-amd64" "$REPO_ROOT"

# Build base image (no ateam binary — mounted at runtime by start.sh)
echo "Building image $IMAGE_NAME..."
docker build \
    --build-arg "USER_UID=$(id -u)" \
    -t "$IMAGE_NAME" \
    -f - "$REPO_ROOT" <<'DOCKERFILE'
FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree make silversearcher-ag bubblewrap socat \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ARG USER_UID=1000
RUN if getent passwd $USER_UID >/dev/null 2>&1; then \
      EXISTING=$(getent passwd $USER_UID | cut -d: -f1); \
      usermod -l agent -d /home/agent -m "$EXISTING" 2>/dev/null || true; \
    else \
      useradd -m -u $USER_UID agent; \
    fi

RUN mkdir -p /data /artifacts /output /agent-data \
    && chown -R agent:agent /data /artifacts /output /agent-data 2>/dev/null \
    || chown -R $USER_UID /data /artifacts /output /agent-data

# Allow agent to fix volume mount ownership and create symlinks
RUN echo "agent ALL=(root) NOPASSWD: /bin/chown, /bin/ln" >> /etc/sudoers.d/agent-chown

COPY test/docker-auth/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER agent
RUN echo "alias ll='ls -alFh'" >> ~/.bashrc \
    && echo "alias cd..='cd ..'" >> ~/.bashrc
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
DOCKERFILE

echo ""
echo "Image:   $IMAGE_NAME"
echo "Claude:  $(docker run --rm "$IMAGE_NAME" claude --version 2>/dev/null || echo 'unknown')"
echo "Ateam:   build/ateam-linux-amd64 (mounted at runtime)"
