.PHONY: build test lint vet fmt fmt-check tidy clean install run-self release-dry

BINARY  ?= bin/test-cli
PKG     := github.com/jhl-labs/test-cli
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/test-cli

test:
	go test ./...

# Race-enabled tests with a coverage profile, for CI.
test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

# Aggregate quality gate run locally and in CI.
lint: fmt-check vet

# Build the binary then run it against this repo as a self-check.
run-self: build
	$(BINARY) run . --profile ci --output-dir reports/test

# Dry-run the cross-platform release build.
release-dry:
	VERSION=$(VERSION) OUT_DIR=dist/release bash scripts/build-release-artifacts.sh

install: build
	install -m 0755 $(BINARY) $(or $(PREFIX),/usr/local)/bin/test-cli

clean:
	rm -rf bin dist reports coverage.out
