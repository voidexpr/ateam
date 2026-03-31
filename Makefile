BINARY = ateam

.PHONY: build build-binary companion clean tidy test test-docker test-docker-live vuln docs

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/ateam/cmd.BuildTime=$(BUILD_TIME) -X github.com/ateam/cmd.Version=$(VERSION) -X github.com/ateam/cmd.GitCommit=$(GIT_COMMIT)

build: build-binary docs

build-binary: tidy
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

docs: build-binary
	./$(BINARY) roles --docs > ROLES.md

companion:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-ldflags "$(LDFLAGS)" \
		-o ateam-linux-amd64 .

tidy:
	go mod tidy

test: build
	go test ./...

# Run docker integration tests inside Docker-in-Docker (no host impact).
test-docker:
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged ateam-test-dind

# Run live agent tests inside DinD with real Claude haiku (~$0.03).
# Auth: set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY (either works).
test-docker-live:
	@if [ -z "$$CLAUDE_CODE_OAUTH_TOKEN" ] && [ -z "$$ANTHROPIC_API_KEY" ]; then \
		echo ""; \
		echo "ERROR: No API authentication configured."; \
		echo ""; \
		echo "Set one of these environment variables:"; \
		echo ""; \
		echo "  Option A - OAuth token (reuses your Claude Code login):"; \
		echo "    run: claude setup-token"; \
		echo "    export CLAUDE_CODE_OAUTH_TOKEN=\"PASTE TOKEN FROM ABOVE COMMAND HERE\""; \
		echo ""; \
		echo "  Option B - API key (from https://console.anthropic.com/settings/keys):"; \
		echo "    export ANTHROPIC_API_KEY=sk-ant-..."; \
		echo ""; \
		echo "See DEV.md for details. Then re-run: make test-docker-live"; \
		exit 1; \
	fi
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged \
		-e CLAUDE_CODE_OAUTH_TOKEN \
		-e ANTHROPIC_API_KEY \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

vuln:
	@which govulncheck > /dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

clean:
	rm -f $(BINARY)
