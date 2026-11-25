#!/usr/bin/env bash
# Runs the fence probes on Linux inside a Debian container: a way to check
# the Linux side from macOS, or from an agent's sandbox, which can reach
# Docker but cannot start a sandbox of its own.
#
# Run: make test-sandbox-docker
# or:  test/sandbox/run-probes-in-docker.sh LINUX_ATEAM_BINARY [BACKEND...]
#
# What differs from a real Linux desktop:
#   - the probes run as root: as a plain user in a container, bwrap cannot
#     create a network namespace, and ateam refuses to start (the first
#     lines of the output show that refusal);
#   - the container gets NET_ADMIN and SYS_ADMIN, seccomp and AppArmor off;
#   - /.dockerenv is removed, so ateam starts its own sandbox instead of
#     taking the container for one.
# Files are copied in with `docker cp`: host folders may not be shared
# with the Docker machine.
#
# SANDBOX_PROBE_NET=0 skips the host checks.

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
BINARY=${1:?usage: $0 LINUX_ATEAM_BINARY [BACKEND...]}
shift
BACKENDS=${*:-fence}
IMAGE=${SANDBOX_PROBE_IMAGE:-debian:bookworm-slim}

if [ ! -f "$BINARY" ]; then
    echo "$BINARY not found — run 'make companion' first" >&2
    exit 1
fi

cid=$(docker create --cap-add=NET_ADMIN --cap-add=SYS_ADMIN \
    --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
    -e SANDBOX_PROBE_NET="${SANDBOX_PROBE_NET:-1}" -e PROBE_BACKENDS="$BACKENDS" \
    "$IMAGE" sh /mnt/sandbox/in-docker.sh)
trap 'docker rm -f "$cid" >/dev/null 2>&1' EXIT
docker cp "$BINARY" "$cid:/mnt/ateam" >/dev/null
docker cp "$SCRIPT_DIR" "$cid:/mnt/sandbox" >/dev/null
docker start -a "$cid"
