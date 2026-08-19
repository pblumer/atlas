# Atlas single-binary server image.
#
# Stage 1 compiles the self-contained binary (the web UI is embedded via
# go:embed). Stage 2 is a small Debian runtime that also carries the script-task
# interpreters Atlas shells out to (ADR-0047): python3 and node are always
# present; PowerShell (pwsh) is opt-in because it is large and Microsoft-sourced.
#
# Build (default: python + node):
#   docker build -t ghcr.io/pblumer/atlas:dev .
# Build including PowerShell:
#   docker build --build-arg INCLUDE_POWERSHELL=true -t ghcr.io/pblumer/atlas:dev-pwsh .
# Multi-arch (requires buildx):
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

# ---------------------------------------------------------------------------

FROM debian:bookworm-slim AS runtime

# Whether to bundle PowerShell (pwsh). Off by default — it adds a few hundred MB
# and pulls a Microsoft-sourced runtime. Turn it on with
# `--build-arg INCLUDE_POWERSHELL=true` if you run PowerShell script tasks.
ARG INCLUDE_POWERSHELL=false
# PowerShell version fetched when INCLUDE_POWERSHELL=true (LTS 7.4.x).
ARG PWSH_VERSION=7.4.6
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive \
    DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    POWERSHELL_TELEMETRY_OPTOUT=1 \
    POWERSHELL_UPDATECHECK=Off \
    HOME=/tmp

# python3 and node are always installed; the `node` command is symlinked from
# Debian's `nodejs` binary. pwsh is unpacked from the official tarball (uniform
# across amd64/arm64) only when requested, along with its runtime libraries.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates tzdata python3 nodejs libicu72; \
    command -v node >/dev/null || ln -s /usr/bin/nodejs /usr/bin/node; \
    if [ "$INCLUDE_POWERSHELL" = "true" ]; then \
      apt-get install -y --no-install-recommends curl libssl3 libgssapi-krb5-2; \
      case "${TARGETARCH:-amd64}" in \
        amd64) pwsh_arch=x64 ;; \
        arm64) pwsh_arch=arm64 ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
      esac; \
      curl -fsSL -o /tmp/pwsh.tar.gz \
        "https://github.com/PowerShell/PowerShell/releases/download/v${PWSH_VERSION}/powershell-${PWSH_VERSION}-linux-${pwsh_arch}.tar.gz"; \
      mkdir -p /opt/microsoft/powershell/7; \
      tar zxf /tmp/pwsh.tar.gz -C /opt/microsoft/powershell/7; \
      chmod +x /opt/microsoft/powershell/7/pwsh; \
      ln -sf /opt/microsoft/powershell/7/pwsh /usr/bin/pwsh; \
      rm -f /tmp/pwsh.tar.gz; \
      apt-get purge -y --auto-remove curl; \
    fi; \
    rm -rf /var/lib/apt/lists/*

# Nonroot user, uid/gid 65532 to match the Helm chart's securityContext. The
# data directory is pre-created and owned so a volume mount at /data is writable.
RUN groupadd -g 65532 atlas \
    && useradd -u 65532 -g 65532 -M -s /usr/sbin/nologin atlas \
    && mkdir -p /data && chown 65532:65532 /data

COPY --from=build /out/atlas /atlas

EXPOSE 8080
VOLUME ["/data"]
USER 65532:65532

ENTRYPOINT ["/atlas"]
CMD ["serve", "--addr", ":8080", "--data-dir", "/data"]
