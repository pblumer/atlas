# Deploying Atlas

Artifacts for running the single-binary Atlas server as a container.

| I want to… | Go to |
|------------|-------|
| Build the container image | [`../Dockerfile`](../Dockerfile) |
| Deploy on Kubernetes | [`helm/atlas`](helm/atlas) (Helm chart) |

## Container image

A published image is at `ghcr.io/pblumer/atlas` (built by
[`.github/workflows/docker.yml`](../.github/workflows/docker.yml) on pushes to
`main` and `v*` tags). To build it yourself:

```bash
make docker                      # local arch, tags ghcr.io/pblumer/atlas:dev
docker run --rm -p 8080:8080 -v atlas-data:/data ghcr.io/pblumer/atlas:dev
# open http://127.0.0.1:8080/
```

The image is a distroless static build (no shell) running as nonroot (uid 65532)
and storing durable state under `/data` — mount a volume there.

## Kubernetes (Helm)

```bash
helm install atlas ./deploy/helm/atlas
kubectl port-forward svc/atlas 8080:8080
```

Atlas is a durable, **single-writer** engine, so the chart deploys a StatefulSet
pinned to **one replica** with a ReadWriteOnce PersistentVolume — never scale it
up or swap it for a Deployment. Full instructions, values, and how to enable
authentication are in [`helm/atlas/README.md`](helm/atlas/README.md).
