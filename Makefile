# Atlas — developer command entry point.
# Agents and CI: prefer these targets so the canonical commands live in one place.

.PHONY: all build test race vet fmt fmt-check lint check cover tidy clean run server

all: check

build:
	go build ./...

# Build the single-binary server into bin/atlas.
server:
	go build -o bin/atlas ./cmd/atlas

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
