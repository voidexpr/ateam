FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ARG USERNAME=node
RUN mkdir -p /data /artifacts /output /agent-data \
    && chown -R $USERNAME:$USERNAME /data /artifacts /output /agent-data

USER $USERNAME
WORKDIR /workspace
