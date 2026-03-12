FROM node:20-bookworm-slim

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
