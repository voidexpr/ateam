#!/usr/bin/env bash
# CLI test: verify `--sandbox-detection` / `--docker-detection` accept
# only 'true' or 'false' (the contract change from the old bool-flag
# shape), and that `ateam env` renders the matrix correctly when each
# flag is set explicitly.
#
# Run: make test-cli

set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
ATEAM=${ATEAM:-"$REPO_ROOT/ateam"}

if [ ! -x "$ATEAM" ]; then
    echo "ateam binary not found at $ATEAM — run 'make build' first" >&2
    exit 1
fi

# Isolated org+project — `ateam env` needs a discoverable project root.
TMPROOT=$(realpath "$(mktemp -d -p "${TMPDIR:-/tmp}")")
trap 'rm -rf "$TMPROOT"' EXIT

PROJ="$TMPROOT/work/proj"
mkdir -p "$PROJ"

# Initialize a real project + org (the same way users do); reuses the
# embedded defaults so `ateam env` has everything it needs to render.
(cd "$PROJ" && "$ATEAM" init --org-create "$TMPROOT/work" >/dev/null 2>&1)

ATEAM_ENV=(--project "$PROJ")

pass=0
fail=0

check() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    if [ "$actual" = "$expected" ]; then
        printf '  ✓ %s\n' "$desc"
        pass=$((pass + 1))
    else
        printf '  ✗ %s\n     expected: %s\n     got:      %s\n' "$desc" "$expected" "$actual"
        fail=$((fail + 1))
    fi
}

check_contains() {
    local desc="$1"
    local needle="$2"
    local haystack="$3"
    if echo "$haystack" | grep -qF -- "$needle"; then
        printf '  ✓ %s\n' "$desc"
        pass=$((pass + 1))
    else
        printf '  ✗ %s\n     expected substring: %s\n     in output:\n%s\n' "$desc" "$needle" "$haystack"
        fail=$((fail + 1))
    fi
}

echo "== flag rejects bogus value =="
out=$("$ATEAM" "${ATEAM_ENV[@]}" --sandbox-detection=maybe env 2>&1)
rc=$?
check "exit code is 1 for --sandbox-detection=maybe" "1" "$rc"
check_contains "error mentions the flag" \
    "--sandbox-detection requires 'true' or 'false'" "$out"

echo
echo "== flag accepts true =="
out=$("$ATEAM" "${ATEAM_ENV[@]}" --sandbox-detection=true env 2>&1)
rc=$?
check "exit code is 0" "0" "$rc"
check_contains "env shows sandbox_detection=true" \
    "sandbox_detection=true" "$out"

echo
echo "== flag accepts false =="
out=$("$ATEAM" "${ATEAM_ENV[@]}" --sandbox-detection=false env 2>&1)
rc=$?
check "exit code is 0" "0" "$rc"
check_contains "env shows sandbox_detection=false" \
    "sandbox_detection=false" "$out"

echo
echo "== docker-detection symmetric =="
out=$("$ATEAM" "${ATEAM_ENV[@]}" --docker-detection=bogus env 2>&1)
rc=$?
check "exit code is 1 for --docker-detection=bogus" "1" "$rc"
check_contains "error mentions the docker flag" \
    "--docker-detection requires 'true' or 'false'" "$out"

echo
echo "== env output: agent in container mode line is present =="
out=$("$ATEAM" "${ATEAM_ENV[@]}" env 2>&1)
check_contains "Agent in container mode headline" \
    "Agent in container mode:" "$out"
check_contains "Docker matrix row" \
    "Docker  detected:" "$out"
check_contains "Sandbox matrix row" \
    "Sandbox detected:" "$out"

echo
echo "== env output: FENCE_SANDBOX detected but not applied (default) =="
out=$(FENCE_SANDBOX=1 "$ATEAM" "${ATEAM_ENV[@]}" env 2>&1)
check_contains "fence shown as detected" \
    "Sandbox detected: yes (fence)" "$out"
check_contains "NOT applied annotation" \
    "sandbox_detection=false, NOT applied" "$out"

echo
echo "== env output: FENCE_SANDBOX detected and applied with --sandbox-detection=true =="
out=$(FENCE_SANDBOX=1 "$ATEAM" "${ATEAM_ENV[@]}" --sandbox-detection=true env 2>&1)
check_contains "via sandbox headline" \
    "Agent in container mode: true (via sandbox)" "$out"
check_contains "applied annotation" \
    "sandbox_detection=true, applied" "$out"

echo
printf 'detection-flags: %d passed, %d failed\n' "$pass" "$fail"
if [ "$fail" -gt 0 ]; then
    exit 1
fi
