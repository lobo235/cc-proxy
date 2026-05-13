APP := cc-proxy
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test lint cover run clean

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/$(APP)

test:
	go test ./...

lint:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

run:
	go run ./cmd/$(APP) serve

clean:
	rm -rf bin coverage.out
