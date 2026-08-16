VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOEXE := $(shell go env GOEXE)

# Mirrors .github/workflows/ci.yml so `make check` locally runs what the CI
# build-vet-test job runs (the golangci-lint job has its own target below).
RACE_PACKAGES := ./internal/agent/... ./internal/control/... ./internal/jobs/... ./internal/event/... ./internal/checkpoint/... ./internal/workspacelease/... ./internal/hook/... ./internal/plugin/...

.PHONY: build vet fmt fmt-check test race lint check clean tool-catalog event-map verify-catalog

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

# Regenerate the checked-in catalog docs (see docs/tool-catalog.md and
# docs/event-map.md). Committed files are verified fresh by verify-catalog.
tool-catalog:
	go run ./cmd/corvus-catalog -kind tools > docs/tool-catalog.md

event-map:
	go run ./cmd/corvus-catalog -kind events -event-source internal/event/event.go > docs/event-map.md

verify-catalog:
	@tmp=$$(mktemp); \
	go run ./cmd/corvus-catalog -kind tools > "$$tmp" && \
	diff -u docs/tool-catalog.md "$$tmp" || { rm -f "$$tmp"; echo "docs/tool-catalog.md is stale: run 'make tool-catalog'"; exit 1; }; \
	rm -f "$$tmp"; \
	tmp=$$(mktemp); \
	go run ./cmd/corvus-catalog -kind events -event-source internal/event/event.go > "$$tmp" && \
	diff -u docs/event-map.md "$$tmp" || { rm -f "$$tmp"; echo "docs/event-map.md is stale: run 'make event-map'"; exit 1; }; \
	rm -f "$$tmp"

test:
	go test ./... -timeout 20m

# The same core-package set CI races (see RACE_PACKAGES above). The race
# detector needs a C toolchain; machines without one (ADR-0001) skip it
# loudly instead of failing with a bare gcc error — CI always runs it.
race:
	@if command -v $(CC) >/dev/null 2>&1 || command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then \
		CGO_ENABLED=1 go test -race -timeout 20m $(RACE_PACKAGES); \
	else \
		echo "SKIP race: no C compiler found (set CC or install gcc/clang; CI runs race for every PR)"; \
	fi

# CI pins golangci-lint v2.12 with .golangci.yml; install locally with
# `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.0`.
lint:
	golangci-lint run

check: vet fmt-check verify-catalog test race

clean:
	rm -rf bin dist
