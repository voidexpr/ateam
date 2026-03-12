#!/bin/sh
set -e

echo "==> Starting dockerd..."
dockerd-entrypoint.sh dockerd &
DOCKERD_PID=$!

# Wait for Docker daemon to be ready
echo "==> Waiting for Docker daemon..."
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        echo "==> Docker daemon ready"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "==> Docker daemon failed to start"
        exit 1
    fi
    sleep 1
done

echo "==> Running docker integration tests..."
cd /src
go test -tags docker_integration -v -count=1 -timeout 5m ./internal/container/
TEST_EXIT=$?

echo "==> Stopping dockerd..."
kill "$DOCKERD_PID" 2>/dev/null || true
wait "$DOCKERD_PID" 2>/dev/null || true

exit $TEST_EXIT
