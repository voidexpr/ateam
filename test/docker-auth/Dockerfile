FROM node:20-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl wget sudo ca-certificates locales \
    ripgrep fd-find jq tree make silversearcher-ag bubblewrap socat \
    python3 python3-pip \
    tmux procps file less util-linux vim-tiny htop bat \
    bash-completion lsof strace net-tools \
    && sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

ENV LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8

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
RUN ln -sf /opt/ateam/ateam-linux-amd64 /usr/local/bin/ateam \
    && ln -sf /usr/bin/batcat /usr/local/bin/bat

COPY test/docker-auth/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER agent
RUN echo "alias ll='ls -alFh'" >> ~/.bashrc \
    && echo "alias cd..='cd ..'" >> ~/.bashrc \
    && echo "alias bat='batcat'" >> ~/.bashrc
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
