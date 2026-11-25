#!/bin/sh
# Runs INSIDE the container run-probes-in-docker.sh starts: installs what
# fence needs on Linux, then runs the probes. See that script.
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq bubblewrap socat git curl ca-certificates >/dev/null 2>&1
mkdir -p /opt/ateam/test/sandbox
cp /mnt/ateam /opt/ateam/ateam
cp /mnt/sandbox/*.sh /opt/ateam/test/sandbox/
chmod -R a+rx /opt/ateam
# ateam takes a Docker container as a sandbox already in place.
rm -f /.dockerenv
bwrap --version

echo
echo "== as a plain user: ateam must refuse (no network namespace in a container)"
useradd -m probe
su probe -c 'mkdir -p ~/w && cd ~/w && /opt/ateam/ateam run --profile sandbox/fence -- true' 2>&1 | tail -1 || true

echo
echo "== as root"
mkdir -p ~/.ssh
# Stand-ins for what a desktop keeps under /run, so the probes check them.
mkdir -p /run/user/0 && socat UNIX-LISTEN:/run/user/0/bus,fork SYSTEM:"echo bus" &
socat UNIX-LISTEN:/run/docker.sock,fork SYSTEM:"echo docker" &
sleep 1
cd /opt/ateam
# shellcheck disable=SC2086  # a list of backends
exec ./test/sandbox/run-probes.sh ${PROBE_BACKENDS:-fence}
