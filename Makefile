BINARY = ateam

.PHONY: build build-binary companion clean tidy check-tidy test test-docker test-docker-live vuln docs lint fmt fmt-check

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/ateam/cmd.BuildTime=$(BUILD_TIME) -X github.com/ateam/cmd.Version=$(VERSION) -X github.com/ateam/cmd.GitCommit=$(GIT_COMMIT)

build: build-binary docs

build-binary:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

docs: build-binary
	./$(BINARY) roles --docs > ROLES.md

companion:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-ldflags "$(LDFLAGS)" \
		-o ateam-linux-amd64 .

tidy:
	go mod tidy

check-tidy:
	go mod tidy -diff

test:
	go build ./...
	go test ./...

# Run docker integration tests inside Docker-in-Docker (no host impact).
test-docker:
	docker build -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged ateam-test-dind

# Run live agent tests inside DinD with real Claude haiku (~$0.03).
# Auth: uses ateam secret resolution (keychain, secrets.env, or env vars).
test-docker-live: build-binary
	@OAUTH=$$(./ateam secret CLAUDE_CODE_OAUTH_TOKEN --get 2>/dev/null || true); \
	APIKEY=$$(./ateam secret ANTHROPIC_API_KEY --get 2>/dev/null || true); \
	if [ -z "$$OAUTH" ] && [ -z "$$APIKEY" ]; then \
		echo ""; \
		echo "ERROR: No API authentication configured."; \
		echo ""; \
		echo "Configure with ateam secret:"; \
		echo "  ateam secret CLAUDE_CODE_OAUTH_TOKEN --set"; \
		echo "  ateam secret ANTHROPIC_API_KEY --set"; \
		echo ""; \
		echo "Or set environment variables directly:"; \
		echo "  export ANTHROPIC_API_KEY=sk-ant-..."; \
		echo ""; \
		exit 1; \
	fi; \
	docker build -t ateam-test-dind -f test/Dockerfile.dind . && \
	docker run --rm --privileged \
		-e CLAUDE_CODE_OAUTH_TOKEN="$$OAUTH" \
		-e ANTHROPIC_API_KEY="$$APIKEY" \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

vuln:
	@which govulncheck > /dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

clean:
	rm -f $(BINARY)
