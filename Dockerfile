# Atlas single-binary server image.
#
# Two stages: a full Go toolchain compiles the self-contained binary (the web UI
# is embedded via go:embed, so nothing else needs to be copied), then a
# distroless static base carries just the binary. Pebble is pure Go, so the
# binary links statically with CGO_ENABLED=0 and needs no libc at runtime.
#
# Build (single arch):
#   docker build -t ghcr.io/pblumer/atlas:dev .
# Build (multi-arch, requires buildx):
#   docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/pblumer/atlas:dev .

FROM golang:1.26-bookworm AS build
WORKDIR /src

# Download modules first so the layer caches until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# TARGETOS/TARGETARCH are set by buildx; they default to the build host when
# building with plain `docker build`. -trimpath and -s -w keep the binary small
# and reproducible.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/atlas ./cmd/atlas

# Seed an empty data directory owned by the nonroot user so a bind- or
# named-volume mount at /data is writable without an explicit chown.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# distroless static: no shell, no package manager, just CA certs, tzdata and a
# nonroot (uid 65532) user. `atlas reset-password` is still reachable without a
# shell via `docker exec <ctr> /atlas reset-password ...` / `kubectl exec`.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/atlas /atlas
COPY --from=build --chown=65532:65532 /out/data /data

EXPOSE 8080
VOLUME ["/data"]
USER 65532:65532

ENTRYPOINT ["/atlas"]
CMD ["serve", "--addr", ":8080", "--data-dir", "/data"]
