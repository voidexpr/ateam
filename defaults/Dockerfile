FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree make \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

# node:20 already has user "node" at UID 1000. Create "agent" at a different UID,
# or reuse node's UID if the host UID happens to match.
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

USER agent
WORKDIR /workspace
