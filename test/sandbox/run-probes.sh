#!/usr/bin/env bash
# Real-run checks of the sandbox backends: starts sandbox-probe.sh under
# `ateam run --profile sandbox/BACKEND` in a scratch project and reports
# what the sandbox allowed and refused. Works on macOS and Linux.
#
# Run: make test-sandbox            (nono and fence)
#      make test-sandbox-nono
#      make test-sandbox-fence
# or:  test/sandbox/run-probes.sh BACKEND...
#
# Run it from a plain terminal: a sandbox cannot start inside another one
# (an agent's shell, another sandboxed session).
#
# Everything is created under SANDBOX_PROBE_DIR (default
# ~/.cache/ateam-sandbox-probe), which must be below the home: the checks
# rely on the home being readable and not writable. The user's ateam
# directory is a scratch one too (ATEAM_HOME). Nothing of yours is written.
#
# Options, as environment variables:
#   SANDBOX_PROBE_DIR=DIR      where the scratch project goes
#   SANDBOX_PROBE_NET=0        skip the checks that need the network
#   SANDBOX_PROBE_DRY_RUN=1    print each launch (ateam --dry-run), run nothing

set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
ATEAM=${ATEAM:-"$REPO_ROOT/ateam"}
MARKER=.ateam-sandbox-probe

if [ $# -eq 0 ]; then
    echo "usage: $0 BACKEND...   (nono, fence)" >&2
    exit 2
fi
if [ ! -x "$ATEAM" ]; then
    echo "ateam binary not found at $ATEAM — run 'make build' first" >&2
    exit 1
fi
if [ -n "${ATEAM_IN_SANDBOX:-}" ] || [ -n "${ATEAM_IN_CONTAINER:-}" ]; then
    echo "already inside a sandbox or a container: run this from a plain terminal" >&2
    exit 1
fi

HOME_REAL=$(cd "$HOME" && pwd -P)
BASE=${SANDBOX_PROBE_DIR:-"$HOME/.cache/ateam-sandbox-probe"}
mkdir -p "$BASE" || exit 1
BASE=$(cd "$BASE" && pwd -P)
case "$BASE/" in
"$HOME_REAL"/*) ;;
*)
    echo "SANDBOX_PROBE_DIR ($BASE) must be below the home ($HOME_REAL)" >&2
    exit 1
    ;;
esac
# Only a folder this script made is ever removed.
if [ -n "$(ls -A "$BASE")" ] && [ ! -f "$BASE/$MARKER" ]; then
    echo "$BASE is not empty and was not made by this script: choose another SANDBOX_PROBE_DIR" >&2
    exit 1
fi
touch "$BASE/$MARKER"

dry=()
if [ "${SANDBOX_PROBE_DRY_RUN:-0}" = 1 ]; then
    dry=(--dry-run)
fi
ssh=0
if [ -e "$HOME/.ssh" ]; then
    ssh=1
fi

# setup BACKEND: a fresh scratch project, initialised as users do.
setup() {
    ROOT="$BASE/$1"
    rm -rf "$ROOT"
    mkdir -p "$ROOT/proj/private" "$ROOT/outside" "$ROOT/extra" "$ROOT/secret/rw" "$ROOT/ahome"
    echo readable >"$ROOT/outside/readable.txt"
    echo token >"$ROOT/secret/token"
    echo open >"$ROOT/secret/open.txt"
    echo token >"$ROOT/proj/private/token"
    echo readme >"$ROOT/proj/private/readme.txt"
    cp "$SCRIPT_DIR/sandbox-probe.sh" "$ROOT/proj/sandbox-probe.sh"
    export ATEAM_HOME="$ROOT/ahome"
    (
        cd "$ROOT/proj" &&
            git init -q . &&
            mkdir -p .git/hooks &&
            "$ATEAM" init >/dev/null 2>&1
    ) || {
        echo "cannot set up the scratch project in $ROOT/proj" >&2
        return 1
    }
    touch "$ROOT/proj/.ateam/ateam.hcl"
    echo "PROBE=secret" >"$ROOT/ahome/secrets.env"
}

# launch BACKEND SUITE [ateam run flags]: runs one suite under the sandbox.
# Returns 0 when every check passed, 1 when one failed, 2 when the sandbox
# did not start (the suite printed nothing).
launch() {
    local backend=$1 suite=$2 out status
    shift 2
    out="$ROOT/$suite.out"
    echo
    echo "== $backend: $suite"
    (
        cd "$ROOT/proj" &&
            "$ATEAM" run --profile "sandbox/$backend" "$@" ${dry[@]+"${dry[@]}"} -- \
                /usr/bin/env PROBE_ROOT="$ROOT" PROBE_NET="${SANDBOX_PROBE_NET:-1}" PROBE_SSH="$ssh" \
                /bin/sh ./sandbox-probe.sh "$suite"
    ) 2>&1 | tee "$out"
    status=${PIPESTATUS[0]}
    if [ ${#dry[@]} -gt 0 ]; then
        return "$((status != 0))"
    fi
    if ! grep -q "^$suite: [0-9]* passed" "$out"; then
        echo "-- the sandbox did not start, or the checks did not finish (exit $status)"
        return 2
    fi
    if [ "$status" -ne 0 ]; then
        # 1: a check failed. 3: ateam found .git/config or .git/hooks changed.
        echo "-- exit $status"
        return 1
    fi
    return 0
}

failed=0
summary=()

for backend in "$@"; do
    case "$backend" in
    nono | fence) ;;
    *)
        echo "unknown backend: $backend (want nono or fence)" >&2
        exit 2
        ;;
    esac
    setup "$backend" || {
        failed=1
        summary+=("$backend: setup failed")
        continue
    }

    launch "$backend" base \
        --write "$ROOT/extra" \
        --exclude "$ROOT/secret" --exclude "$ROOT/proj/private" \
        --domain example.com
    case $? in
    0) summary+=("$backend base: ok") ;;
    2) failed=1 && summary+=("$backend base: NOT STARTED") ;;
    *) failed=1 && summary+=("$backend base: FAILED") ;;
    esac

    launch "$backend" carve \
        --exclude "$ROOT/secret" --read "$ROOT/secret/open.txt" --write "$ROOT/secret/rw" \
        --exclude "$ROOT/proj/private" --read "$ROOT/proj/private/readme.txt"
    status=$?
    if [ "$backend" = fence ] && [ "$status" -ne 0 ] && grep -q "fence cannot open" "$ROOT/carve.out"; then
        # Known: fence has no rule that opens a path inside a denied folder
        # (plans/feature_sandboxing_and_attended_agents_impl_steps.md).
        summary+=("$backend carve: refused at launch (known: fence cannot open a path inside an excluded folder)")
        continue
    fi
    case $status in
    0) summary+=("$backend carve: ok") ;;
    2) failed=1 && summary+=("$backend carve: NOT STARTED") ;;
    *) failed=1 && summary+=("$backend carve: FAILED") ;;
    esac
done

echo
echo "== summary ($(uname -s))"
for line in "${summary[@]}"; do
    echo "  $line"
done
echo "scratch files: $BASE"
exit "$failed"
