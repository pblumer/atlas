# Atlas — developer command entry point.
# Agents and CI: prefer these targets so the canonical commands live in one place.

.PHONY: all build test race vet fmt fmt-check lint check tidy clean

all: check

build:
	go build ./...

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

# The full gate. A change is "done" when this passes.
check: build vet fmt-check race

tidy:
	go mod tidy

clean:
	go clean ./...
	rm -rf bin dist coverage
