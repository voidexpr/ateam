#!/usr/bin/env bash
set -euo pipefail

# Automated test suite for Claude Code auth in Docker containers.
#
# Prerequisites:
#   - Docker running
#   - Image built: ./test/docker-auth/build.sh
#   - For oauth tests: CLAUDE_CODE_OAUTH_TOKEN in ateam secrets
#   - For interactive tests: a volume with valid credentials (run start.sh --interactive first)
#
# Usage:
#   test-auth.sh [--volume VOL] [--skip-interactive]
#
# Tests:
#   1. OAuth -p mode works with fresh volume
#   2. Credential persistence across containers (requires pre-authenticated volume)
#   3. OAuth -p coexists with interactive credentials on same volume
#   4. Remote Control prerequisites check
#   5. Refresh token bootstrap on fresh volume
#   6. Claude Code version

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

image="ateam-auth-test"
volume=""
skip_interactive=false
passed=0
failed=0
skipped=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --volume)           volume="$2"; shift 2 ;;
        --image)            image="$2"; shift 2 ;;
        --skip-interactive) skip_interactive=true; shift ;;
        *)                  echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

pass() { echo -e "  ${GREEN}PASS${NC} $1"; ((passed++)); }
fail() { echo -e "  ${RED}FAIL${NC} $1: $2"; ((failed++)); }
skip() { echo -e "  ${YELLOW}SKIP${NC} $1: $2"; ((skipped++)); }

# Resolve ateam binary
ateam_bin="$REPO_ROOT/ateam"
if [[ ! -x "$ateam_bin" ]]; then
    ateam_bin="$(command -v ateam 2>/dev/null || true)"
fi

# Try to get oauth token from ateam secrets
oauth_token=""
if [[ -n "$ateam_bin" ]]; then
    oauth_token=$("$ateam_bin" secret CLAUDE_CODE_OAUTH_TOKEN --get 2>/dev/null) || true
fi

echo -e "${CYAN}Claude Code Docker Auth Test Suite${NC}"
echo "Image: $image"
if [[ -n "$oauth_token" ]]; then
    echo "OAuth: from ateam secret"
else
    echo "OAuth: not available (set with: ateam secret CLAUDE_CODE_OAUTH_TOKEN --set)"
fi
echo ""

# ── Test 1: OAuth -p with fresh volume ────────────────────────────────────────

echo -e "${CYAN}Test 1: OAuth -p with fresh volume${NC}"

if [[ -z "$oauth_token" ]]; then
    skip "oauth-p-fresh" "CLAUDE_CODE_OAUTH_TOKEN not in ateam secrets"
else
    test_vol="claude-test-oauth-$$"
    docker volume create "$test_vol" >/dev/null

    output=$(docker run --rm \
        -v "$test_vol:/home/agent/.claude" \
        -e "CLAUDE_CODE_OAUTH_TOKEN=$oauth_token" \
        "$image" \
        claude -p "respond with exactly: AUTH_OK" 2>&1) || true

    docker volume rm "$test_vol" >/dev/null 2>&1 || true

    if echo "$output" | grep -q "AUTH_OK"; then
        pass "oauth-p-fresh"
    else
        fail "oauth-p-fresh" "expected AUTH_OK in output"
        echo "    output: $(echo "$output" | head -5)"
    fi
fi

# ── Test 2: Credential persistence across containers ─────────────────────────

echo -e "${CYAN}Test 2: Credential persistence across containers${NC}"

if $skip_interactive; then
    skip "credential-persistence" "--skip-interactive"
elif [[ -z "$volume" ]]; then
    skip "credential-persistence" "no --volume with pre-authenticated credentials"
else
    has_creds=$(docker run --rm -v "$volume:/home/agent/.claude" "$image" \
        sh -c 'test -f ~/.claude/.credentials.json && echo yes || echo no' 2>&1)

    if [[ "$has_creds" != "yes" ]]; then
        skip "credential-persistence" "volume $volume has no .credentials.json (run start.sh --interactive first)"
    else
        output=$(docker run --rm \
            --name "auth-test-persist-$$" \
            -v "$volume:/home/agent/.claude" \
            "$image" \
            claude -p "respond with exactly: PERSIST_OK" 2>&1) || true

        if echo "$output" | grep -q "PERSIST_OK"; then
            pass "credential-persistence"
        else
            fail "credential-persistence" "stored credentials did not work in new container"
            echo "    output: $(echo "$output" | head -5)"
        fi
    fi
fi

# ── Test 3: OAuth -p coexists with interactive on same volume ────────────────

echo -e "${CYAN}Test 3: OAuth -p + interactive credentials coexistence${NC}"

if $skip_interactive || [[ -z "$volume" ]] || [[ -z "$oauth_token" ]]; then
    skip "coexistence" "needs --volume with credentials AND oauth token in ateam secrets"
else
    has_creds=$(docker run --rm -v "$volume:/home/agent/.claude" "$image" \
        sh -c 'test -f ~/.claude/.credentials.json && echo yes || echo no' 2>&1)

    if [[ "$has_creds" != "yes" ]]; then
        skip "coexistence" "volume $volume has no .credentials.json"
    else
        output=$(docker run --rm \
            --name "auth-test-coexist-$$" \
            -v "$volume:/home/agent/.claude" \
            -e "CLAUDE_CODE_OAUTH_TOKEN=$oauth_token" \
            "$image" \
            claude -p "respond with exactly: COEXIST_OK" 2>&1) || true

        if echo "$output" | grep -q "COEXIST_OK"; then
            pass "coexistence"
        else
            fail "coexistence" "oauth -p failed on volume with interactive credentials"
            echo "    output: $(echo "$output" | head -5)"
        fi
    fi
fi

# ── Test 4: Remote Control prerequisites ─────────────────────────────────────

echo -e "${CYAN}Test 4: Remote Control prerequisites${NC}"

if $skip_interactive || [[ -z "$volume" ]]; then
    skip "remote-control-prereqs" "needs --volume with credentials"
else
    has_creds=$(docker run --rm -v "$volume:/home/agent/.claude" "$image" \
        sh -c 'test -f ~/.claude/.credentials.json && echo yes || echo no' 2>&1)

    if [[ "$has_creds" != "yes" ]]; then
        skip "remote-control-prereqs" "no credentials"
    else
        pass "remote-control-prereqs (credentials present in volume)"
        echo -e "  ${YELLOW}NOTE${NC} Remote Control requires CLAUDE_CODE_OAUTH_TOKEN to NOT be set"
    fi
fi

# ── Test 5: Refresh token bootstrap on fresh volume ──────────────────────────

echo -e "${CYAN}Test 5: Refresh token bootstrap on fresh volume${NC}"

refresh_token=""
if [[ -n "$ateam_bin" ]]; then
    refresh_token=$("$ateam_bin" secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --get 2>/dev/null) || true
fi

if [[ -z "$refresh_token" ]]; then
    skip "refresh-bootstrap" "CLAUDE_CODE_OAUTH_REFRESH_TOKEN not in ateam secrets"
else
    test_vol="claude-test-refresh-$$"
    docker volume create "$test_vol" >/dev/null

    output=$(docker run --rm \
        -v "$test_vol:/home/agent/.claude" \
        -e "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=$refresh_token" \
        -e "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference" \
        "$image" \
        claude -p "respond with exactly: REFRESH_OK" 2>&1) || true

    docker volume rm "$test_vol" >/dev/null 2>&1 || true

    if echo "$output" | grep -q "REFRESH_OK"; then
        pass "refresh-bootstrap"
    else
        fail "refresh-bootstrap" "refresh token bootstrap failed"
        echo "    output: $(echo "$output" | head -5)"
    fi
fi

# ── Test 6: Claude version check ─────────────────────────────────────────────

echo -e "${CYAN}Test 6: Claude Code version${NC}"

version=$(docker run --rm "$image" claude --version 2>&1) || version="unknown"
echo -e "  ${GREEN}INFO${NC} $version"
pass "version-check"

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo -e "${CYAN}Results: ${GREEN}$passed passed${NC}, ${RED}$failed failed${NC}, ${YELLOW}$skipped skipped${NC}"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
