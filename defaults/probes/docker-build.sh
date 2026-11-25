#!/bin/sh
# id: docker-build
# group: docker
# target: docker build
# why: a command that can build an image can put anything in it and run it
# starts: builds one image from scratch with one empty file (nothing is pulled), then removes it
# risk: works
# needs: docker
#
# How to add a probe: DEV.md, "Adding a probe".

image="ateam-probe-$$"
trap 'docker image rm -f "$image" >/dev/null 2>&1' EXIT

# The builder keeps its own state under ~/.docker, which a sandbox may
# close: the scratch folder takes it, so the answer is the daemon's.
BUILDX_CONFIG="$PWD/buildx"
export BUILDX_CONFIG

if ! docker info >/dev/null 2>&1; then
	echo "the daemon does not answer"
	exit 1
fi
mkdir context
: >context/marker
printf 'FROM scratch\nCOPY marker /marker\n' >context/Dockerfile
if docker build -q -t "$image" context >/dev/null 2>&1; then
	echo "an image was built"
	exit 0
fi
echo "the build was refused"
exit 1
