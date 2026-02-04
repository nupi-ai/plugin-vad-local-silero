BINARY_NAME := vad-local-silero

.PHONY: build clean test

build:
	go build -o $(BINARY_NAME) ./cmd/adapter/

clean:
	rm -f $(BINARY_NAME)

test:
	go test -race ./...
