BINARY = ateam

.PHONY: build clean tidy test

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

build: tidy
	go build -ldflags "-X github.com/ateam-poc/cmd.BuildTime=$(BUILD_TIME)" -o $(BINARY) .

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -f $(BINARY)
