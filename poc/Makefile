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
	@if [ -z "$$ANTHROPIC_API_KEY" ] && [ -z "$$CLAUDE_CODE_OAUTH_TOKEN" ]; then \
		echo ""; \
		echo "ERROR: No API authentication configured."; \
		echo ""; \
		echo "Set one of these environment variables:"; \
		echo ""; \
		echo "  Option A - API key (from https://console.anthropic.com/settings/keys):"; \
		echo "    export ANTHROPIC_API_KEY=sk-ant-..."; \
		echo ""; \
		echo "  Option B - OAuth token (reuses your Claude Code login):"; \
		echo "    export CLAUDE_CODE_OAUTH_TOKEN=\"\$$(cat ~/.claude/.credentials.json | grep -o '\"accessToken\":\"[^\"]*\"' | cut -d'\"' -f4)\""; \
		echo ""; \
		echo "See DEV.md for details. Then re-run: make test-docker-live"; \
		exit 1; \
	fi
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged \
		-e ANTHROPIC_API_KEY \
		-e CLAUDE_CODE_OAUTH_TOKEN \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

clean:
	rm -f $(BINARY)
