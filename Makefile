# Atlas — developer command entry point.
# Agents and CI: prefer these targets so the canonical commands live in one place.

.PHONY: all build test race vet fmt fmt-check lint check cover tidy clean run server dist

all: check

# Build metadata stamped into the binary's `atlas version` output. Overridable,
# so CI can pass an exact release tag (make server VERSION=v0.1.0).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

build:
	go build ./...

# Build the single-binary server into bin/atlas, version-stamped.
server:
	go build -ldflags '$(LDFLAGS)' -o bin/atlas ./cmd/atlas

# Cross-compile release binaries into dist/ for the platforms the release
# workflow publishes. Mirrors .github/workflows/release.yml so a release can be
# reproduced locally: make dist VERSION=v0.1.0
dist:
	@rm -rf dist && mkdir -p dist
	@set -e; for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${pair%/*}; arch=$${pair#*/}; \
		ext=; [ "$$os" = windows ] && ext=.exe; \
		out=dist/atlas_$(VERSION)_$${os}_$${arch}$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -trimpath -ldflags '$(LDFLAGS)' -o $$out ./cmd/atlas; \
	done

# Run the single-binary server (override flags via ARGS, e.g. ARGS="--addr :9090").
run:
	go run ./cmd/atlas $(ARGS)

test:
	go test ./...

# Mandatory before considering any change done.
race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Fails (non-empty output) if anything is unformatted.
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

# Optional: requires golangci-lint to be installed.
lint:
	golangci-lint run

# Enforce the repository-wide statement-coverage floor (ADR-0018). Override the
# threshold via THRESHOLD, e.g. make cover THRESHOLD=90.
cover:
	./scripts/check-coverage.sh $(THRESHOLD)

# The full gate. A change is "done" when this passes.
check: build vet fmt-check race cover

tidy:
	go mod tidy

clean:
	go clean ./...
	rm -rf bin dist coverage
