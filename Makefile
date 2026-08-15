VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOEXE := $(shell go env GOEXE)

# Mirrors .github/workflows/ci.yml so `make check` locally runs what the CI
# build-vet-test job runs (the golangci-lint job has its own target below).
RACE_PACKAGES := ./internal/agent/... ./internal/control/... ./internal/jobs/... ./internal/event/... ./internal/checkpoint/... ./internal/workspacelease/... ./internal/hook/... ./internal/plugin/...

.PHONY: build vet fmt fmt-check test race lint check clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/corvus$(GOEXE) ./cmd/corvus

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

# CI gate: report unformatted files without rewriting anything.
fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then echo "gofmt required on:"; echo "$$files"; exit 1; fi

test:
	go test ./... -timeout 20m

# The same core-package set CI races (see RACE_PACKAGES above).
race:
	go test -race -timeout 20m $(RACE_PACKAGES)

# CI pins golangci-lint v2.12 with .golangci.yml; install locally with
# `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.0`.
lint:
	golangci-lint run

check: vet fmt-check test race

clean:
	rm -rf bin dist
