BINARY = ateam

.PHONY: build clean tidy

build: tidy
	go build -o $(BINARY) .

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
