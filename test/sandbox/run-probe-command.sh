#!/bin/sh
# Real runs of `ateam probe`, each one's output kept in a file so it can be
# read afterwards (by the owner, or by an agent that could not run them).
#
#   run-probe-command.sh [RUN]...     default: every run for this system
#
# Runs: plain json compare nono fence active linux
#   plain    ateam probe, in place
#   json     the same as one JSON document
#   compare  --profile sandbox/nono,dev/go without a launch
#   nono     --start-sandbox under nono
#   fence    --start-sandbox under fence
#   active   the probe scripts: a browser, a docker build, two containers
#   linux    the Linux binary in a Debian container, as root (needs `make companion`)
#
# nono, fence and active need a plain terminal: a sandbox does not start
# inside another one. Exit code 1 of a run is normal (a RISK or an UNMET);
# 2 is a failure of the command and makes this script fail.
set -u

ATEAM=${ATEAM:-./ateam}
OUT=${PROBE_OUT:-test_data/probe-runs}
mkdir -p "$OUT"
failed=0

run() {
	name=$1
	shift
	echo "== $name: $*"
	"$@" >"$OUT/$name.txt" 2>&1
	code=$?
	echo "   exit $code, output in $OUT/$name.txt"
	tail -1 "$OUT/$name.txt" | sed 's/^/   /'
	if [ "$code" -ge 2 ]; then
		failed=1
	fi
}

linux_run() {
	arch=$(uname -m)
	case "$arch" in
	aarch64) arch=arm64 ;;
	x86_64) arch=amd64 ;;
	esac
	binary="build/ateam-linux-$arch"
	if [ ! -x "$binary" ]; then
		echo "== linux: skipped, $binary is missing (make companion)"
		return
	fi
	# The binary goes in through stdin: a Docker daemon in a VM may not share
	# this folder, and then a mount of it is empty.
	run linux sh -c "docker run --rm -i debian:stable-slim sh -c 'cat >/usr/local/bin/ateam && chmod +x /usr/local/bin/ateam && mkdir -p /root/work && cd /root/work && ateam probe --skip network' <$binary"
}

[ $# -gt 0 ] || set -- plain json compare nono fence active linux
for name in "$@"; do
	case "$name" in
	plain) run plain "$ATEAM" probe ;;
	json) run json "$ATEAM" probe --json ;;
	compare) run compare "$ATEAM" probe --profile sandbox/nono,dev/go ;;
	nono) run nono "$ATEAM" probe --start-sandbox --profile sandbox/nono,dev/go ;;
	fence) run fence "$ATEAM" probe --start-sandbox --profile sandbox/fence,dev/github ;;
	active) run active "$ATEAM" probe --only docker,playwright --with playwright-launch,docker-build,docker-build-expand-bind ;;
	linux) linux_run ;;
	*)
		echo "unknown run: $name" >&2
		exit 2
		;;
	esac
done
exit $failed
