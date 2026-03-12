BINARY = ateam

.PHONY: build clean tidy test test-docker test-docker-live

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

build: tidy
	go build -ldflags "-X github.com/ateam-poc/cmd.BuildTime=$(BUILD_TIME)" -o $(BINARY) .

tidy:
	go mod tidy

test: build
	go test ./...

# Run docker integration tests inside Docker-in-Docker (no host impact).
test-docker:
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged ateam-test-dind

# Run live agent tests inside DinD (requires ANTHROPIC_API_KEY, costs ~$0.03).
test-docker-live:
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged \
		-e ANTHROPIC_API_KEY \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

clean:
	rm -f $(BINARY)
