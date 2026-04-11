#!/bin/sh
# Fix ownership of ~/.claude when volume-mounted (Docker creates it as root).
if [ -d "$HOME/.claude" ] && [ "$(stat -c %u "$HOME/.claude" 2>/dev/null)" != "$(id -u)" ]; then
    sudo chown -R "$(id -u):$(id -g)" "$HOME/.claude" 2>/dev/null || true
fi

exec "$@"
