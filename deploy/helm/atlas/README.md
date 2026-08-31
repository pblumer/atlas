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
# open http://127.0.0.1:8080/ and sign in — API docs at /api/docs, MCP at /mcp
```

A login is required by default. With no credentials configured, the first start
seeds an administrator and logs a generated password **once**:

```bash
kubectl logs sts/atlas | grep auth.admin_seeded
```

## Authentication

Auth is **on by default** — the API, the UI and `/mcp` all require a login. Set
`atlas.auth.enabled=false` only for a throwaway development install; the server
then logs a warning (`auth.disabled`) and is open to anyone who can reach the
Service.

For anything real, give it the bootstrap credentials rather than letting one be
generated into a pod log:

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

### Single sign-on with an identity provider

The chart has no dedicated values for it, because the server takes it as a handful
of environment variables and `extraEnv` carries them without a new schema. Point
Atlas at an OpenID Connect provider by putting the issuer and client id there and
the secret in a Secret you manage:

```yaml
# values.yaml — four settings, one of them from a Secret you manage.
atlas:
  extraEnv:
    - name: ATLAS_EXTERNAL_URL
      value: https://atlas.example.com
    - name: ATLAS_OIDC_ISSUER
      value: https://login.example.com/realms/atlas
    - name: ATLAS_OIDC_CLIENT_ID
      value: atlas
    - name: ATLAS_OIDC_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: atlas-oidc
          key: client-secret
```

```bash
kubectl create secret generic atlas-oidc \
  --from-literal=client-secret='the-secret-the-provider-issued'
helm upgrade --install atlas ./deploy/helm/atlas -f values.yaml
```

`ATLAS_EXTERNAL_URL` matters here: the redirect URI is built from it, and
`<external-url>/auth/oidc/callback` is the exact string to register at the
provider. Set it to the origin your Ingress serves, not the Service name.

Keep the bootstrap administrator above — it is the way back in when the
provider is unreachable. Which Atlas roles the provider's groups grant is
configured in the running instance, under Console → Organization → Single
sign-on, and is off until somebody turns it on. See
[`docs/install.md`](../../../docs/install.md#single-sign-on-with-an-identity-provider).

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

Terminate TLS before exposing Atlas publicly. In a cluster the Ingress usually
does it and Atlas serves plain HTTP behind it, which is what the chart defaults
to. Where that last hop must be encrypted as well — an Ingress on another node, or
another Atlas publishing an application to this one, which requires `https` of its
target — set `atlas.tls.enabled` with a `kubernetes.io/tls` Secret and the pod
terminates TLS itself; the probes then switch to the HTTPS scheme automatically.
Either way `/mcp` is gated by `--auth` like every other route and no longer depends
on a proxy rule, while `/metrics`, `/healthz` and `/readyz` stay unauthenticated by
design — encryption is not authorization.

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
| `atlas.tls.enabled` | `false` | Terminate TLS in the pod. Needs `atlas.tls.existingSecret`; the probes switch to HTTPS with it |
| `atlas.tls.existingSecret` | `""` | `kubernetes.io/tls` Secret with the certificate and key (what cert-manager writes) |
| `atlas.tls.caKey` | `""` | Key in that Secret holding a CA bundle to trust when publishing to another Atlas (`--tls-ca`) |
| `service.type` / `service.port` | `ClusterIP` / `8080` | Service exposure |
| `ingress.enabled` | `false` | Create an Ingress |

## Uninstall

```bash
helm uninstall atlas
# The PVC is retained by design so data survives a reinstall. Remove it explicitly:
kubectl delete pvc data-atlas-0
```
