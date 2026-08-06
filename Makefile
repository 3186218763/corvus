VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOEXE := $(shell go env GOEXE)

.PHONY: build vet fmt test clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/corvus$(GOEXE) ./cmd/corvus

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

clean:
	rm -rf bin dist
