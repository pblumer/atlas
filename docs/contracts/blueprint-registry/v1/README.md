# Blueprint Registry v1: contract review fixtures

**Status: accepted contract, implementation in progress.** See [ADR-0499](../../../adr/0499-blueprint-registry-and-distribution.md) (**Accepted / Not started**). Maintainer approval dated 2026-10-27. The material in this folder is a wire-contract draft, **not** an active atlas endpoint or installable package.

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

## Contract fixtures

- [Valid index](examples/valid-index.json): structural positive case.
- [Rejected traversal](examples/reject-path-traversal.json): invalid artifact path.
- [Rejected digest](examples/reject-digest.json): invalid package digest.

**Before code integration:** add a Go validator and deterministic tests for JSON schema + all cross-field requirements, Ed25519 verification (valid/invalid/wrong key), byte-exact digest, duplicate rejection, bound checks, and compatibility comparisons. A schema alone is not an install-time security boundary. The first network/client slice adds independent SSRF, redirect, archive and authentication tests. The schema does not contain prices, entitlements, purchase tokens or runtime licences.
