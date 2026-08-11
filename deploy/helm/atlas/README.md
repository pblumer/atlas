# Atlas Helm chart

Deploys the single-binary [Atlas](https://github.com/pblumer/atlas) BPMN workflow
engine — the engine, HTTP API, web UI and MCP endpoint in one container.

> ⚠️ Atlas is in early development. APIs are unstable; this chart tracks the
> image built from `main` by default. Pin an image tag before relying on it.

## Why a StatefulSet with one replica

Atlas is a durable, event-sourced engine with a **single writer per partition**
(see the engine invariants). Exactly one process may own the write-ahead log and
state store at a time. The chart therefore renders a **StatefulSet pinned to one
replica** with a **ReadWriteOnce** persistent volume, so two servers can never
touch the same data. Do not convert it to a Deployment or scale it up — that
would corrupt durable state. Scale Atlas by adding partitions (a roadmap item),
not pods.

## Install

```bash
# From a checkout of the repo:
helm install atlas ./deploy/helm/atlas

# Pin an image tag and give it real storage:
helm install atlas ./deploy/helm/atlas \
  --set image.tag=v0.1.0 \
  --set persistence.size=20Gi \
  --set persistence.storageClass=fast-ssd
```

Then reach it (default `ClusterIP`):

```bash
kubectl port-forward svc/atlas 8080:8080
# open http://127.0.0.1:8080/  — API docs at /api/docs, MCP at /mcp
```

## Enable authentication

Auth is **off by default** — the API, UI and `/mcp` are open to anyone who can
reach the Service. Turn it on before exposing Atlas:

```bash
# Simplest (password stored in a chart-managed Secret):
helm install atlas ./deploy/helm/atlas \
  --set atlas.auth.enabled=true \
  --set atlas.auth.username=admin \
  --set atlas.auth.password='a-strong-password'

# Preferred: reference a Secret you manage, with keys `username` and `password`:
kubectl create secret generic atlas-admin \
  --from-literal=username=admin --from-literal=password='a-strong-password'
helm install atlas ./deploy/helm/atlas \
  --set atlas.auth.enabled=true \
  --set atlas.auth.existingSecret=atlas-admin
```

Recover a locked-out admin (the image has no shell, but the binary is directly
executable):

```bash
kubectl exec statefulset/atlas -- \
  /atlas reset-password --data-dir /data --create-admin admin
```

## Secret vault

The encrypted vault is on by default. Without an explicit key the server
generates one at `/data/vault.key` on the persistent volume. To pin the key
(`ATLAS_VAULT_KEY`), set `atlas.vault.key` or point `atlas.vault.existingSecret`
at a Secret with a `vault-key` entry.

## Script tasks

Script-task workers (`powershell`/`python`/`javascript`) shell out to interpreters
and run arbitrary code. The default image ships **none** of them, so they are
disabled by default. Enable one only with an image that bundles the interpreter,
and consider setting `securityContext.readOnlyRootFilesystem=false`.

## Ingress / TLS

```bash
helm install atlas ./deploy/helm/atlas \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=atlas.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

Put a TLS-terminating proxy (Ingress) in front before exposing Atlas publicly —
the `/mcp` endpoint is unauthenticated at the transport level by design.

## Values

See [`values.yaml`](values.yaml) for the full, inline-documented list. The most
common knobs:

| Key | Default | Description |
|-----|---------|-------------|
| `image.repository` | `ghcr.io/pblumer/atlas` | Image to run |
| `image.tag` | `""` (chart `appVersion`) | Image tag; pin in production |
| `persistence.enabled` | `true` | Use a PersistentVolume (never `false` for real use) |
| `persistence.size` | `8Gi` | PVC size for WAL + state store |
| `persistence.storageClass` | `""` (cluster default) | StorageClass for the PVC |
| `atlas.auth.enabled` | `false` | Require login for API/UI |
| `atlas.vault.enabled` | `true` | Encrypted secret vault |
| `atlas.docs.enabled` | `true` | Serve OpenAPI + API explorer |
| `service.type` / `service.port` | `ClusterIP` / `8080` | Service exposure |
| `ingress.enabled` | `false` | Create an Ingress |

## Uninstall

```bash
helm uninstall atlas
# The PVC is retained by design so data survives a reinstall. Remove it explicitly:
kubectl delete pvc data-atlas-0
```
