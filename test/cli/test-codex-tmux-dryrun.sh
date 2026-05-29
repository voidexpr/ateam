#!/usr/bin/env bash
# CLI test: verify `ateam exec --agent codex-tmux --dry-run` produces the
# expected codex invocation inside a project, and errors with actionable
# guidance outside one.
#
# This is the end-to-end shape of the path the user actually exercises —
# distinct from the unit tests, which cover the helpers but not the CLI
# resolution + buildAgent + dry-run-printing chain.
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

# Isolated workspace.
TMPROOT=$(realpath "$(mktemp -d -p "${TMPDIR:-/tmp}")")
trap 'rm -rf "$TMPROOT"' EXIT

PROJ="$TMPROOT/work/proj"
export HOME="$TMPROOT/home"
mkdir -p "$PROJ" "$HOME"
(cd "$PROJ" && "$ATEAM" init --org-create "$TMPROOT/work" >/dev/null 2>&1)

pass=0
fail=0
failures=()

ok()    { pass=$((pass+1)); printf '  PASS  %s\n' "$1"; }
fail()  { fail=$((fail+1)); failures+=("$1"); printf '  FAIL  %s\n    %s\n' "$1" "$2"; }

# Case 1: inside a project, codex-tmux dry-run prints the expected codex
# command. This catches regressions where the agent type lookup, model/effort
# flags, or InteractiveArgs ordering would silently change.
run_dryrun_inside_project() {
    local label="01  inside project: dry-run prints codex-tmux command"
    local out
    out=$(cd "$PROJ" && "$ATEAM" exec --agent codex-tmux --dry-run "/review the pending changes" 2>&1)
    local rc=$?
    if [ "$rc" -ne 0 ]; then
        fail "$label" "exit=$rc, output:\n$out"
        return
    fi
    local missing=()
    for pattern in \
        "Agent:.*codex-tmux" \
        "codex --no-alt-screen" \
        "check_for_update_on_startup=false" \
        "/review the pending changes" \
        ; do
        if ! grep -qE "$pattern" <<<"$out"; then
            missing+=("$pattern")
        fi
    done
    if [ "${#missing[@]}" -ne 0 ]; then
        fail "$label" "missing patterns: ${missing[*]}\noutput:\n$out"
        return
    fi
    ok "$label"
}

# Case 2: org-only (no .ateam/) is the scratch-mode path. codex-tmux must
# resolve to an actionable dry-run that roots its tmux socket under the org
# dir's cache/. Previously this case was rejected up-front; the rejection now
# only triggers when neither .ateam/ nor .ateamorg/ is found.
run_codex_tmux_in_scratch_mode() {
    local label="02  org-only (no project): codex-tmux dry-run uses org cache dir"
    local stray="$TMPROOT/work/no-project"
    mkdir -p "$stray"
    local out rc
    out=$(cd "$stray" && "$ATEAM" exec --agent codex-tmux --dry-run "/help" 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        fail "$label" "expected exit 0 in scratch mode, got $rc; output:\n$out"
        return
    fi
    if ! grep -qE "Agent:.*codex-tmux" <<<"$out"; then
        fail "$label" "missing 'Agent: codex-tmux' in dry-run; output:\n$out"
        return
    fi
    ok "$label"
}

# Case 2b: with neither an org nor a project resolvable, rejection still
# fires and points at the missing state directory.
run_reject_without_any_state_dir() {
    local label="02b no .ateam/ or .ateamorg/: codex-tmux rejected with state-dir guidance"
    local stray="$TMPROOT/orphan"
    mkdir -p "$stray"
    local out rc
    out=$(cd "$stray" && HOME="$stray" "$ATEAM" exec --agent codex-tmux "/help" 2>&1)
    rc=$?
    if [ "$rc" -eq 0 ]; then
        fail "$label" "expected non-zero exit, got 0; output:\n$out"
        return
    fi
    if ! grep -qE "state directory|\.ateamorg|\.ateam" <<<"$out"; then
        fail "$label" "missing state-dir / .ateam(org) guidance; output:\n$out"
        return
    fi
    ok "$label"
}

# Case 3: the codex-tmux profile (`--profile codex-tmux` from runtime.hcl)
# resolves to the same agent. Catches accidental profile-table breakage.
run_profile_resolves() {
    local label="03  --profile codex-tmux resolves to codex-tmux agent"
    local out
    out=$(cd "$PROJ" && "$ATEAM" exec --profile codex-tmux --dry-run "ping" 2>&1)
    local rc=$?
    if [ "$rc" -ne 0 ]; then
        fail "$label" "exit=$rc, output:\n$out"
        return
    fi
    if ! grep -qE "Agent:.*codex-tmux" <<<"$out"; then
        fail "$label" "missing 'Agent: codex-tmux'; output:\n$out"
        return
    fi
    ok "$label"
}

echo "Running codex-tmux CLI dry-run tests..."
echo

run_dryrun_inside_project
run_codex_tmux_in_scratch_mode
run_reject_without_any_state_dir
run_profile_resolves

echo
echo "---"
echo "Result: $pass passed, $fail failed"
if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    for f in "${failures[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi
