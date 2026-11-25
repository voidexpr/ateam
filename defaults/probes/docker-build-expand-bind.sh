#!/bin/sh
# id: docker-build-expand-bind
# group: docker
# target: docker bind escalation
# why: a container given one folder and the daemon's socket can mount any other folder of the host
# starts: two containers from docker:cli (pulled when missing), the second started from inside the first; both are removed
# risk: works
# needs: docker
#
# The first container gets the working folder and the Docker socket, nothing
# else. From inside it a second container is asked to mount a folder the
# first one was never given: the home, or the folder above the working
# folder when the working folder is not under the home.
#
# Proof is a regular file of that folder, known here, seen there. Counting
# entries is not enough: a daemon in a VM that does not share the folder
# (lima, colima, Docker Desktop with a short share list) creates an empty
# folder of that name inside the VM and mounts it. Only the file's name is
# used, never its content, and nothing is listed.

work="$ATEAM_PROBE_WORKDIR"
home="$ATEAM_PROBE_HOME"
case "$work" in
"$home"/*) outside="$home" ;;
*) outside=$(dirname "$work") ;;
esac

witness=""
for entry in "$outside"/.[!.]* "$outside"/*; do
	if [ -f "$entry" ] && [ ! -L "$entry" ]; then
		witness=$(basename "$entry")
		break
	fi
done
if [ -z "$witness" ]; then
	echo "no regular file in $outside to prove a mount with"
	exit 2
fi

socket="/var/run/docker.sock"
case "$DOCKER_HOST" in
unix://*) socket="${DOCKER_HOST#unix://}" ;;
esac

name="ateam-probe-$$"
trap 'docker rm -f "$name" "$name-inner" >/dev/null 2>&1' EXIT

if ! docker info >/dev/null 2>&1; then
	echo "the daemon does not answer"
	exit 1
fi

seen=$(docker run --rm --name "$name" \
	-v "$work":/work -v "$socket":/var/run/docker.sock \
	-e WITNESS="$witness" \
	docker:cli \
	docker run --rm --name "$name-inner" -v "$outside":/outside:ro -e WITNESS="$witness" docker:cli \
	sh -c 'if [ -f "/outside/$WITNESS" ]; then echo host; else echo started; fi' 2>/dev/null)

case "$seen" in
host)
	echo "a container given only the working folder mounted $outside of the host"
	exit 0
	;;
started)
	echo "a second container started, but the daemon does not share $outside: it mounted a folder of its own"
	exit 1
	;;
esac
echo "a container could not start another through the daemon"
exit 1
