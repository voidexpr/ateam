BINARY = ateam

.PHONY: build build-binary companion clean tidy check-tidy check test test-all test-docker test-docker-live vuln docs lint fmt fmt-check install-hooks

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/ateam/cmd.BuildTime=$(BUILD_TIME) -X github.com/ateam/cmd.Version=$(VERSION) -X github.com/ateam/cmd.GitCommit=$(GIT_COMMIT)

build: build-binary

build-binary:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

docs: build-binary
	./$(BINARY) roles --docs > ROLES.md

companion:
	mkdir -p build
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-ldflags "$(LDFLAGS)" \
		-o build/ateam-linux-amd64 .

tidy:
	go mod tidy

check-tidy:
	go mod tidy -diff

check: test fmt-check check-tidy lint

test:
	go test -race ./...

test-all: test test-docker test-docker-live

# Run docker integration tests inside Docker-in-Docker (no host impact).
# Uses the default buildx builder (docker driver) to avoid BuildKit container
# crashes between sessions.
test-docker:
	docker buildx build --builder default --load -t ateam-test-dind -f test/Dockerfile.dind .
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
	docker buildx build --builder default --load -t ateam-test-dind -f test/Dockerfile.dind . && \
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
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files have formatting issues:"; \
		echo "$$unformatted"; \
		echo ""; \
		echo "Run 'make fmt' to fix them."; \
		exit 1; \
	fi

install-hooks:
	@printf '#!/bin/sh\nmake fmt-check && make check-tidy\n' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Installed pre-commit hook."

clean:
	rm -f $(BINARY) ateam-linux-*
	rm -rf build/
