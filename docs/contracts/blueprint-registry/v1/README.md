# atlas hub — Blueprint Registry v1 contract review fixtures

**Naming:** atlas hub is the user-facing experience. This immutable v1
wire contract is still **Blueprint Registry v1**, with existing
`blueprintId`, `/api/v1/blueprints` and
`ATLAS_BLUEPRINT_REGISTRY_*` identifiers. It distributes application
packages (optionally including product catalogues); standalone catalogues
and other offer kinds require separately approved extensions.

**Status: accepted v1 contract; public Console integration landed in PR #1416 (BMP1 remains active).** See [ADR-0499](../../../adr/0499-blueprint-registry-and-distribution.md) (**Accepted / Partial**). Signed-index verification, bounded HTTPS fetching, archive inspection and authorized Console package import are implemented. A publicly hosted signed sample and persistent installation provenance are still missing.

## Publication layout

A publicly readable registry can be plain static HTTPS content:

```text
https://example.org/blueprints/index.json
https://example.org/blueprints/index.json.sig
https://example.org/blueprints/packages/purchase-approval-1.0.0.tar.gz
```

The `index.json` bytes must satisfy [index.schema.json](index.schema.json). The detached `index.json.sig` is the base64 encoding (RFC 4648 standard alphabet, no line wrapping) of the 64-byte **Ed25519 signature over the exact raw index.json bytes**. Do **not** reserialize JSON before verifying the signature. For v1, the registry operator explicitly installs a trusted public key out of band. A key found in the downloaded index is **not** self-authenticating and must not be trusted automatically.

The signed index records `sha256:<64 lowercase hex>` of **the exact compressed archive bytes**, not a normalized tar tree, and `sizeBytes` of that same archive. The package is an existing ADR-0467 application package; this contract does not invent another application serializer. Example checksums here are illustrative and do not correspond to a downloadable package. No signing keys, test secrets or real payment endpoints are included.

## Identity and compatibility

- `registryId` identifies the configured signed index, not a vendor authorization grant.
- `publisherId` and `blueprintId` are namespaced identifiers. Additional cross-field validation MUST require `blueprintId` to start with `publisherId + "."`; a signer endorses the listing but **does not prove** the publisher's domain control.
- Unique releases use the tuple (`blueprintId`, `version`) inside one index. Duplicated tuples and duplicate JSON object keys MUST be rejected, regardless of the schema's limits.
- Compatibility uses inclusive `atlas.minVersion` and optional **exclusive** `atlas.maxVersionExclusive`. Inverted or empty version ranges MUST be rejected. A rejected compatibility check MUST not silently fall back to a newer/older package.
- `needs` lists *references*, not credential values. The installer must verify the actual needs from imported models (ADR-0467): signed metadata can be incomplete or misleading.
- An atlas application has its own local key and deployment releases. A Blueprint release does not deploy itself or migrate existing instances.

## Source boundary and security

1. Only a privileged administrator may configure the registry HTTPS URL and pinned signing key; an arbitrary user-supplied URL cannot be fetched by the server.
2. Registry indexes are bounded to **1 MiB**, 512 releases, and a bounded JSON parser. Verify signature before trusting any listed content.
3. Artifact paths are relative to the configured index directory, never absolute URLs, include no traversal or percent escapes, and resolve only on the approved same-origin HTTPS endpoint in v1. Redirects outside that origin are refused. Treat DNS rebindings, private/link-local/metadata IPs, timeouts and download limits as explicit implementation security gates. An administrator may separately configure a private organizational registry; that exception is explicit, not inferred from a user-controlled path.
4. Bound compressed artifact download to **50 MiB** and extracted data to a separately enforced hard limit (proposed **200 MiB**) with archive entry count and path validation; use no implicit trust of tar headers or symlinks.
5. A successful index signature + artifact digest authenticates an artifact **relative to the operator-pinned registry key**. It does not certify BPMN, scripts, Worker Types or publisher reputation. Any executable content remains gated by existing explicit review and deployment rules.
6. Cache by (`registry key fingerprint`, `blueprintId`, `version`, `digest`). Network/signature failure must not interrupt an already deployed application. Registry removal is not runtime licence revocation.
7. Key rotation requires an explicit operator trust update in v1 (out of band). Automatic trust-on-first-use and signer keys embedded in remotely fetched metadata are forbidden.
8. Authenticated/private delivery and third-party payment settlement remain outside v1 (BMP3), not implied by the static public example.

## Operator-pinned Console integration (BMP1)

The first public Console source is deliberately **single-registry** and disabled
until the server operator configures both environment variables before startup:

```bash
ATLAS_BLUEPRINT_REGISTRY_URL=https://your-registry.example/blueprints/index.json
ATLAS_BLUEPRINT_REGISTRY_KEY=<base64-of-32-raw-Ed25519-public-key-bytes>
```

The signing key must be obtained **out of band**, never from the index URL or a
user request. A missing pair disables the current hub Blueprint source; an incomplete or invalid
configuration prevents server startup. Only operator-controlled environment
configuration is accepted; there is no user-facing add-URL form.

In the Console's application overview, use **Create new → Browse atlas hub…**. The browser calls the existing authenticated atlas API:
`GET /api/v1/blueprints`, then `GET /api/v1/blueprints/{id}/releases/{version}/preview`.
A preview downloads and rechecks the signed index, digest, atlas compatibility and
the bounded tar.gz archive **off the engine run loop**, returning an application
name, requirements and file names. Explicit approval requests the archive through
`GET /api/v1/blueprints/{id}/releases/{version}/package` with the reviewed digest
as an `If-Match` precondition. A changed digest returns HTTP 412.

The browser gives the verified package bytes to **the existing application package
import workflow** (ADR-0467), with its dry-run conflict decisions, user/team
authorization and optional publish where catalogues require it. An unsigned or
unconfigured registry cannot initiate any import. The registry HTTP routes
require the `modeler` role, and the source/publish routes retain their existing
authorization. This is design-time work, not a Worker task or an engine command.

**Still open for BMP1 acceptance:** persist registry identity, signer fingerprint,
Blueprint release/version/digest and application key in sidecar state with a
restart/reload test; verify a real static signed free Blueprint end to end; and
document failure/recovery outcomes. Browsing and importing alone are not
provenance or a complete BMP1 milestone.

## Contract fixtures

- [Valid index](examples/valid-index.json): structural positive case.
- [Rejected traversal](examples/reject-path-traversal.json): invalid artifact path.
- [Rejected digest](examples/reject-digest.json): invalid package digest.

**As built:** BMP0 implements the signed-index verifier and deterministic negative tests. BMP1 implements the pinned HTTPS client with redirect/DNS/IP and size checks (#1411), safe bounded tar.gz inspection (#1415), and the Console/API listing, preview and verified download linked to the existing authorized import workflow (#1416). This is not yet a complete or provenance-backed atlas hub installation: a public signed demo, restart/recovery checks and durable source/catalogue IDs remain open. The v1 schema contains no prices, entitlements, purchase tokens or runtime licences. ADR-0506's catalogue-only and auto-safe extensions are Proposed, not implemented.
