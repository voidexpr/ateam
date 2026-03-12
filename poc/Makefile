BINARY = ateam

.PHONY: build clean tidy test test-docker

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

clean:
	rm -f $(BINARY)
