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

# Run live agent tests inside DinD with real Claude haiku (~$0.03).
# Auth: set ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN (either works).
test-docker-live:
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged \
		-e ANTHROPIC_API_KEY \
		-e CLAUDE_CODE_OAUTH_TOKEN \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

clean:
	rm -f $(BINARY)
