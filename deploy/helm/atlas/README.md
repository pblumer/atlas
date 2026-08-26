# Atlas Helm chart

Deploys the single-binary [Atlas](https://github.com/pblumer/atlas) BPMN workflow
engine — the engine, HTTP API, web UI and MCP endpoint in one container.

> ⚠️ Atlas is in early development (`0.x`) — APIs and on-disk formats are
> unstable. By default this chart deploys the pinned `0.4.0` release image; set
> `image.tag` to move to another release (or to the rolling `main` tag).

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
# From the OCI registry (no checkout needed):
helm install atlas oci://ghcr.io/pblumer/charts/atlas --version 0.4.0

# ...or from a checkout of the repo:
helm install atlas ./deploy/helm/atlas

# Pin an image tag and give it real storage:
helm install atlas oci://ghcr.io/pblumer/charts/atlas --version 0.4.0 \
  --set image.tag=0.4.0 \
  --set persistence.size=20Gi \
  --set persistence.storageClass=fast-ssd
```

The chart is published to `ghcr.io/pblumer/charts/atlas` on every chart-version
bump. List the available versions with
`helm show chart oci://ghcr.io/pblumer/charts/atlas`.

Then reach it (default `ClusterIP`):

```bash
kubectl port-forward svc/atlas 8080:8080
# open http://127.0.0.1:8080/  — API docs at /api/docs, MCP at /mcp
```

## Enable authentication

Auth is **off by default** — the API, UI and `/mcp` are open to anyone who can
reach the Service. Turn it on before exposing Atlas (it gates `/mcp` too):

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

Script-task workers shell out to interpreters and run arbitrary code (ADR-0047).
The default image bundles **python3** and **node**, so `atlas.script.python` and
`atlas.script.javascript` are **on by default**. The interpreters get a writable
`emptyDir` at `/tmp` (the container's `HOME`), so the root filesystem stays
read-only.

**PowerShell is opt-in.** The default image does not ship `pwsh`; build the image
with PowerShell first, then enable it:

```bash
docker build --build-arg INCLUDE_POWERSHELL=true -t ghcr.io/pblumer/atlas:pwsh .
helm install atlas ./deploy/helm/atlas \
  --set image.tag=pwsh \
  --set atlas.script.powershell=true
```

All three run inside the Linux container: `pwsh` is PowerShell Core (not Windows
PowerShell), `python3` (not Python 2), and Node.js — scripts must be
Linux-compatible. To turn a language off, set its `atlas.script.<lang>=false`.

## Ingress / TLS

```bash
helm install atlas ./deploy/helm/atlas \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=atlas.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

Put a TLS-terminating proxy (Ingress) in front before exposing Atlas publicly.
Atlas speaks plain HTTP, so the proxy is what terminates TLS; `/mcp` itself is
gated by `--auth` like every other route and no longer depends on a proxy rule.

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
