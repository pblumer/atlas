# Atlas — developer command entry point.
# Agents and CI: prefer these targets so the canonical commands live in one place.

.PHONY: all build test race vet fmt fmt-check lint check cover tidy clean run server \
        whats-new docker docker-powershell docker-buildx helm-lint helm-template helm-package

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

# Regenerate the Console "What's New" feed (api/web/whats-new.json) from CHANGELOG.md
# and scripts/whats-new/overrides.json. Commit the regenerated JSON. See
# scripts/whats-new/README.md.
whats-new:
	node scripts/whats-new/gen.mjs

tidy:
	go mod tidy

clean:
	go clean ./...
	rm -rf bin dist coverage

# --- Container image & Helm chart (see deploy/) -----------------------------

IMAGE ?= ghcr.io/pblumer/atlas:dev
CHART := deploy/helm/atlas

# Build the server image for the local architecture (python3 + node).
docker:
	docker build -t $(IMAGE) .

# Build the image with PowerShell (pwsh) bundled as well.
docker-powershell:
	docker build --build-arg INCLUDE_POWERSHELL=true -t $(IMAGE) .

# Build (and optionally push with PUSH=--push) a multi-arch image via buildx.
docker-buildx:
	docker buildx build --platform linux/amd64,linux/arm64 -t $(IMAGE) $(PUSH) .

# Lint the Helm chart.
helm-lint:
	helm lint $(CHART)

# Render the chart to stdout (override values via ARGS, e.g. ARGS="--set atlas.auth.enabled=true").
helm-template:
	helm template atlas $(CHART) $(ARGS)

# Package the chart into dist/ (CI pushes it to ghcr.io/pblumer/charts/atlas).
helm-package:
	helm package $(CHART) --destination dist
