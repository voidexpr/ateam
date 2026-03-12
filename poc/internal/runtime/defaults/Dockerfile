FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ARG USER_UID=1000
RUN useradd -m -u $USER_UID ateam
USER ateam
WORKDIR /workspace
