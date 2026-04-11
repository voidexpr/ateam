FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree make silversearcher-ag bubblewrap socat \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

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

# Allow agent to fix volume mount ownership
RUN echo "agent ALL=(root) NOPASSWD: /bin/chown" >> /etc/sudoers.d/agent-chown

# /opt/ateam is mounted at runtime; symlink so ateam is on PATH
RUN ln -sf /opt/ateam/ateam-linux-amd64 /usr/local/bin/ateam

COPY test/docker-auth/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Install Claude CLI directly to /usr/local/bin (skip claude install which
# puts files in ~/.claude/bin — conflicts with volume mount).
ARG CLAUDE_VERSION=2.1.101
RUN ARCH=$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/') \
    && BUCKET="https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases" \
    && VERSION=$(curl -fsSL "$BUCKET/latest") \
    && curl -fsSL -o /usr/local/bin/claude "$BUCKET/$VERSION/linux-$ARCH/claude" \
    && chmod +x /usr/local/bin/claude

USER agent
RUN echo "alias ll='ls -alFh'" >> ~/.bashrc \
    && echo "alias cd..='cd ..'" >> ~/.bashrc
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
