# Deploying Atlas

Artifacts for running the single-binary Atlas server as a container.

| I want to… | Go to |
|------------|-------|
| Build the container image | [`../Dockerfile`](../Dockerfile) |
| Deploy on Kubernetes | [`helm/atlas`](helm/atlas) (Helm chart) |
| Run the binary directly instead | [`../docs/install.md`](../docs/install.md) |

## Container image

A published image is at `ghcr.io/pblumer/atlas` (built by
[`.github/workflows/docker.yml`](../.github/workflows/docker.yml) on pushes to
`main` and `v*` tags). To build it yourself:

```bash
make docker                      # local arch, tags ghcr.io/pblumer/atlas:dev
docker run --rm -p 8080:8080 -v atlas-data:/data ghcr.io/pblumer/atlas:dev
# open http://127.0.0.1:8080/ and sign in
```

A login is required by default. The first start seeds an administrator and logs a
generated password **once** — read it out of the container output, or set your own
with `-e ATLAS_ADMIN_PASSWORD=…` before that first start. For a throwaway container
you just want to click around in, append `--auth=false`; it runs open and says so at
startup.

The image is a Debian-slim build running as nonroot (uid 65532), storing durable
state under `/data` — mount a volume there. It bundles the **python3** and
**node** interpreters for script tasks; build with
`--build-arg INCLUDE_POWERSHELL=true` to also include **PowerShell** (`pwsh`).
Everything runs as a Linux container (amd64/arm64); Windows containers are not
supported and script tasks must be Linux-compatible (PowerShell Core, `python3`,
Node.js).

## Kubernetes (Helm)

```bash
# From the OCI registry (published to ghcr.io/pblumer/charts/atlas on each
# chart-version bump), or from a local checkout:
helm install atlas oci://ghcr.io/pblumer/charts/atlas --version 0.1.1
helm install atlas ./deploy/helm/atlas
kubectl port-forward svc/atlas 8080:8080
```

Atlas is a durable, **single-writer** engine, so the chart deploys a StatefulSet
pinned to **one replica** with a ReadWriteOnce PersistentVolume — never scale it
up or swap it for a Deployment. Full instructions, values, and how to enable
authentication are in [`helm/atlas/README.md`](helm/atlas/README.md).
