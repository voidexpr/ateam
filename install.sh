#!/usr/bin/env bash
set -euo pipefail

# ATeam installer — checks dependencies, builds from source, adds to PATH.

REQUIRED_GO_VERSION="1.25"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33mWarning:\033[0m %s\n' "$*"; }
err()   { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; }
ok()    { printf '\033[1;32m✓\033[0m %s\n' "$*"; }

# --- Go ---

check_go() {
    if command -v go &>/dev/null; then
        local ver
        ver=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')
        local major minor req_major req_minor
        major=${ver%%.*}
        minor=${ver#*.}
        req_major=${REQUIRED_GO_VERSION%%.*}
        req_minor=${REQUIRED_GO_VERSION#*.}
        if [ "$major" -gt "$req_major" ] || { [ "$major" -eq "$req_major" ] && [ "$minor" -ge "$req_minor" ]; }; then
            ok "Go $ver found"
            return 0
        fi
        warn "Go $ver found but $REQUIRED_GO_VERSION+ required"
        return 1
    fi
    return 1
}

install_go() {
    info "Installing Go $REQUIRED_GO_VERSION..."
    case "$(uname -s)" in
        Darwin)
            if command -v brew &>/dev/null; then
                brew install go
            else
                err "Homebrew not found. Install Go manually: https://go.dev/dl/"
                exit 1
            fi
            ;;
        Linux)
            local arch
            arch=$(uname -m)
            case "$arch" in
                x86_64)  arch="amd64" ;;
                aarch64) arch="arm64" ;;
                *)       err "Unsupported architecture: $arch"; exit 1 ;;
            esac
            local tarball="go${REQUIRED_GO_VERSION}.0.linux-${arch}.tar.gz"
            local url="https://go.dev/dl/${tarball}"
            info "Downloading $url"
            curl -fsSL "$url" -o "/tmp/$tarball"
            sudo rm -rf /usr/local/go
            sudo tar -C /usr/local -xzf "/tmp/$tarball"
            rm "/tmp/$tarball"
            export PATH="/usr/local/go/bin:$PATH"
            ;;
        *)
            err "Unsupported OS. Install Go manually: https://go.dev/dl/"
            exit 1
            ;;
    esac
    ok "Go installed: $(go version)"
}

# --- Claude CLI ---

check_claude() {
    if command -v claude &>/dev/null; then
        ok "Claude Code CLI found"
        return 0
    fi
    return 1
}

# --- Build ---

build_ateam() {
    info "Building ateam..."
    make build
    ok "Built: $(./ateam --version 2>/dev/null || echo './ateam')"
}

# --- Install to PATH ---

install_to_path() {
    mkdir -p "$INSTALL_DIR"

    local src
    src="$(pwd)/ateam"
    local dest="$INSTALL_DIR/ateam"

    if [ -L "$dest" ] || [ -f "$dest" ]; then
        rm "$dest"
    fi
    ln -s "$src" "$dest"
    ok "Linked $dest -> $src"

    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        warn "$INSTALL_DIR is not in your PATH"
        echo "  Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
        echo "    export PATH=\"$INSTALL_DIR:\$PATH\""
    fi
}

# --- Main ---

main() {
    info "ATeam installer"
    echo

    # Check we're in the ateam repo
    if [ ! -f "go.mod" ] || ! grep -q "module github.com/ateam" go.mod; then
        err "Run this script from the ateam repository root"
        exit 1
    fi

    # Go
    if ! check_go; then
        install_go
    fi

    # Claude CLI
    if ! check_claude; then
        warn "Claude Code CLI not found (optional — needed to run agents)"
        echo "  Install: https://docs.anthropic.com/en/docs/claude-code"
        echo "  Then authenticate: claude login"
        echo
    fi

    # Build
    build_ateam
    echo

    # Install
    install_to_path
    echo

    info "Done! Quick start:"
    echo "  cd /path/to/your/project"
    echo "  ateam init"
    echo "  ateam auto-setup"
    echo "  ateam report"
    echo "  ateam review"
    echo "  ateam code"
}

main "$@"
