BINARY = ateam
GO_CACHE_DIR := $(CURDIR)/.cache/go-build
GO_TOOL_BIN := $(CURDIR)/.cache/bin
GO_CMD ?= go
GO := GOCACHE=$(GO_CACHE_DIR) $(GO_CMD)
GO_INSTALL := GOBIN=$(GO_TOOL_BIN) $(GO)

.PHONY: build build-binary build-binary-race companion companion-race build-all build-all-race clean tidy check-tidy check-docs check test test-all test-cli test-docker test-docker-live claude-in-docker vuln deadcode docs lint fmt fmt-check install-hooks run-ci

BUILD_TIME := $(shell python3 -c 'import time; print(f"{time.time():.6f}")' 2>/dev/null || date +%s)
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/ateam/cmd.BuildTime=$(BUILD_TIME) -X github.com/ateam/cmd.Version=$(VERSION) -X github.com/ateam/cmd.GitCommit=$(GIT_COMMIT)

build: build-binary

build-binary:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) .

docs: build-binary
	./$(BINARY) roles --docs > ROLES.md

# Build a linux companion binary for the host's CPU arch — Apple Silicon
# devs get arm64 to match their default Docker platform; Intel/Linux x86_64
# devs get amd64. Override with `make companion HOST_ARCH=amd64` to force.
HOST_ARCH ?= $(shell $(GO_CMD) env GOHOSTARCH)

companion:
	mkdir -p build
	GOOS=linux GOARCH=$(HOST_ARCH) CGO_ENABLED=0 $(GO) build \
		-ldflags "$(LDFLAGS)" \
		-o build/ateam-linux-$(HOST_ARCH) .

build-all: build companion

# -race-enabled host binary. Writes to $(BINARY)-race so it coexists with the
# normal build. `-race` requires CGO_ENABLED=1 which is the default locally.
build-binary-race:
	$(GO) build -race -ldflags "$(LDFLAGS)" -o $(BINARY)-race .

# -race-enabled linux companion. `-race` needs CGo + a linux C toolchain; the
# easiest portable way is to run `go build` inside a linux container so the
# architecture follows your docker default platform (linux/arm64 on Apple
# Silicon, linux/amd64 on Intel). Output: build/ateam-linux-race.
companion-race:
	mkdir -p build
	docker run --rm -v "$(CURDIR)":/src -w /src \
		-e CGO_ENABLED=1 \
		golang:1.26 \
		go build -race -ldflags "$(LDFLAGS)" -o build/ateam-linux-race .

build-all-race: build-binary-race companion-race

tidy:
	$(GO) mod tidy

check-tidy:
	$(GO) mod tidy -diff

check-docs: build-binary
	./$(BINARY) roles --docs > .roles-docs.gen
	diff .roles-docs.gen ROLES.md
	rm -f .roles-docs.gen

# Developer quick health check: tests, formatting, tidiness, linting.
check: test fmt-check check-tidy check-docs lint

# Full CI check: everything in 'check' plus vulnerability and dead-code scanning.
run-ci: check vuln deadcode

test:
	$(GO) test -race ./...

# CLI integration tests — exercise the `ateam` binary end-to-end with an
# isolated HOME and project dir. Requires the binary to be built.
test-cli: build-binary
	./test/cli/test-auth-combos.sh
	./test/cli/test-codex-tmux-dryrun.sh

test-all: test test-cli test-docker test-docker-live

# Run docker integration tests inside Docker-in-Docker (no host impact).
# Auto-detects a working buildx builder (falls back to plain docker build).
BUILDX_BUILDER := $(shell docker buildx ls --format '{{.Name}}:{{.Status}}' 2>/dev/null | grep ':running' | head -1 | cut -d: -f1)
test-docker:
	$(if $(BUILDX_BUILDER),docker buildx build --builder $(BUILDX_BUILDER) --load,docker build) -t ateam-test-dind -f test/Dockerfile.dind .
	docker run --rm --privileged ateam-test-dind

# Drop into an interactive Claude Code session running inside the dind image,
# with the project and ~/.codex / ~/.claude bind-mounted. Use this when you
# need Claude Code to drive codex-tmux end-to-end — the macOS host sandbox
# can't fork tmux's inner shell, but the linux container has no such limit.
#
# Usage: make claude-in-docker
# Then ask Claude to: `go test -count=1 -run TestRunTmuxFakeCodexTUI ./internal/codex/`
# Or: `./build/ateam-linux-$(HOST_ARCH) exec --agent codex-tmux "/help"`
claude-in-docker:
	$(if $(BUILDX_BUILDER),docker buildx build --builder $(BUILDX_BUILDER) --load,docker build) -t ateam-test-dind -f test/Dockerfile.dind .
	docker run -it --rm --privileged \
	  -v $(PWD):/src \
	  -v $$HOME/.codex:/root/.codex \
	  -v $$HOME/.claude:/root/.claude \
	  -w /src \
	  --entrypoint claude \
	  ateam-test-dind

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
	$(if $(BUILDX_BUILDER),docker buildx build --builder $(BUILDX_BUILDER) --load,docker build) -t ateam-test-dind -f test/Dockerfile.dind . && \
	docker run --rm --privileged \
		-e CLAUDE_CODE_OAUTH_TOKEN="$$OAUTH" \
		-e ANTHROPIC_API_KEY="$$APIKEY" \
		-e TEST_TAGS="docker_integration,docker_live" \
		ateam-test-dind

vuln:
	@BIN=$$(command -v govulncheck 2>/dev/null || true); \
	if [ -z "$$BIN" ] && [ -x "$(GO_TOOL_BIN)/govulncheck" ]; then \
		BIN="$(GO_TOOL_BIN)/govulncheck"; \
	fi; \
	if [ -z "$$BIN" ] && [ -x "$$($(GO) env GOPATH)/bin/govulncheck" ]; then \
		BIN=$$($(GO) env GOPATH)/bin/govulncheck; \
	fi; \
	if [ -z "$$BIN" ]; then \
		mkdir -p "$(GO_TOOL_BIN)"; \
		if $(GO_INSTALL) install golang.org/x/vuln/cmd/govulncheck@latest >/dev/null 2>&1; then \
			BIN="$(GO_TOOL_BIN)/govulncheck"; \
		else \
			echo "vuln: skipping — cannot install govulncheck (no network or sandboxed)"; \
			exit 0; \
		fi; \
	fi; \
	out=$$(GOCACHE="$(GO_CACHE_DIR)" "$$BIN" ./... 2>&1); rc=$$?; \
	if [ $$rc -ne 0 ] && echo "$$out" | grep -q "fetching vulnerabilities"; then \
		echo "vuln: skipping — govulncheck cannot reach the vuln database (no network or sandboxed)"; \
		exit 0; \
	fi; \
	printf '%s\n' "$$out"; \
	if [ $$rc -ne 0 ]; then \
		echo "vuln: govulncheck reported issues (exit $$rc)" >&2; \
		exit $$rc; \
	fi

# Whole-program dead-code gate: fails on any unreachable function in the
# module. `-test` counts functions exercised only by the test suite (e.g.
# runner.RunPool) as live, so test-only seams are not flagged. Install-or-skip
# handling mirrors `vuln` so sandboxed/offline runs degrade gracefully.
deadcode:
	@BIN=$$(command -v deadcode 2>/dev/null || true); \
	if [ -z "$$BIN" ] && [ -x "$(GO_TOOL_BIN)/deadcode" ]; then \
		BIN="$(GO_TOOL_BIN)/deadcode"; \
	fi; \
	if [ -z "$$BIN" ] && [ -x "$$($(GO) env GOPATH)/bin/deadcode" ]; then \
		BIN=$$($(GO) env GOPATH)/bin/deadcode; \
	fi; \
	if [ -z "$$BIN" ]; then \
		mkdir -p "$(GO_TOOL_BIN)"; \
		if $(GO_INSTALL) install golang.org/x/tools/cmd/deadcode@latest >/dev/null 2>&1; then \
			BIN="$(GO_TOOL_BIN)/deadcode"; \
		else \
			echo "deadcode: skipping — cannot install deadcode (no network or sandboxed)"; \
			exit 0; \
		fi; \
	fi; \
	out=$$(GOCACHE="$(GO_CACHE_DIR)" "$$BIN" -test ./... 2>&1); rc=$$?; \
	if [ $$rc -ne 0 ]; then \
		printf '%s\n' "$$out"; \
		echo "deadcode: analysis failed (exit $$rc)" >&2; \
		exit $$rc; \
	fi; \
	if [ -n "$$out" ]; then \
		printf '%s\n' "$$out"; \
		echo "deadcode: unreachable functions found (see above)" >&2; \
		exit 1; \
	fi

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		mkdir -p "$(GO_TOOL_BIN)"; \
		$(GO_INSTALL) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest; \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		BIN=$$(command -v golangci-lint); \
	elif [ -x "$(GO_TOOL_BIN)/golangci-lint" ]; then \
		BIN="$(GO_TOOL_BIN)/golangci-lint"; \
	else \
		BIN=$$($(GO) env GOPATH)/bin/golangci-lint; \
	fi; \
	GOCACHE="$(GO_CACHE_DIR)" GOLANGCI_LINT_CACHE="$(CURDIR)/.cache/golangci-lint" "$$BIN" run ./...

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
	@printf '#!/bin/sh\nmake fmt-check && make check-tidy && make check-docs && make lint\n' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Installed pre-commit hook."

clean:
	rm -f $(BINARY) $(BINARY)-race ateam-linux-*
	rm -rf build/
