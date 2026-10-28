# ADR-0499: atlas hub — open Blueprint registries for process applications

> **Product naming note (2026-10-28):** The distribution experience previously
> called "Blueprint Marketplace" is now named **atlas hub**. The hub is intended
> to cover applications, their product catalogues and future separately governed
> offer types. Signed Blueprint Registry v1 and all its existing identifiers,
> signing keys, URLs and import protections stay unchanged. This is a naming
> decision, **not** an approval of new schemas, catalogue-only offers,
> commercial entitlements or unattended updates. This ADR remains Accepted/Partial.

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-10-26
- **Deciders:** pblumer (maintainer; approval to implement on 2026-10-27)

## Context and problem statement

atlas already has versioned Process Applications (ADR-0128), portable application source (ADR-0134), and export/import of application packages with conflict plans and import baselines (ADR-0467). The existing Repository (ADR-0081) distributes reusable element templates and Worker-related packages, but its installed template store is not yet wired into the Modeler palette. There is no remote registry and no release provenance for an installed Blueprint.

We want a reusable, immutable Blueprint of a process application from an independent publisher, installable and safely updatable from public and private catalogs. A Blueprint release, an editable Process Application, its local release, a Git authoring binding and a template package are different concepts.

## Decision drivers

- Reuse the ADR-0467 application package, import preview, no-op updates and conflict resolution.
- Share discovery, distribution and trust primitives with the existing Repository without treating BPMN applications as element-template JSON.
- Keep an open, source-agnostic registry contract and generic install/update client within public atlas.
- Separate optional commercial sales/entitlements from the free engine and runtime.
- Preserve offline execution, application autonomy and the six engine invariants.
- Prevent secret material, executable code escalation and malicious package extraction.
- Avoid hardcoding GitHub as the only supported registry.

## Considered options

1. Implement full catalog, checkout, licence management and payment processing inside atlas.
2. Put public protocol, registry client and Blueprint lifecycle in atlas and use independently hosted public/private registries and optional separate commercial services.
3. Require all Blueprint consumers to clone GitHub repositories directly.
4. Distribute only a curated catalog embedded into the atlas binary.

## Decision outcome

Choose **option 2**. A Blueprint is an immutable, versioned distribution wrapper around the **existing application package**. The hub may eventually list element-template and Worker Type packages, but it does not replace their native package formats.

### Identities and boundaries

- Blueprint identity: publisher-controlled, globally namespaced Blueprint ID, semantic release version and content digest.
- Installed Process Application: separate portable application key, local edits, own releases and deployments.
- Installation provenance: registry origin, Blueprint ID/version, content digest, application identity and last import baseline. This is design-time sidecar state, never an engine event.
- MVP allows one installation of a Blueprint per server with its existing application key. Installing multiple independent copies needs another decision covering process, DMN, form and catalog ID remapping. Changing only a display name cannot solve this.
- A Blueprint update never implies that a running process instance is migrated.

### Registry and delivery

- A versioned registry index discovers immutable releases, declaring title, description, publisher, licence, compatibility, required Worker Types, other installation needs, digest and signing metadata.
- The initial public registry may be served as static GitHub-hosted files. Consumers need no GitHub account to browse free releases; publishers may use Git.
- Authenticated sources use the same registry discovery and package contract. Paid content must not be exposed through reusable public download URLs, shared tokens or direct customer access to a private source repository.
- Multiple registries have explicit origin/trust and namespace collision rules: never silently resolve colliding publisher identities.
- A commercial service may separately provide purchase, entitlements and authorized download. Purchased applications continue to run offline and no process-instance execution checks the hub licence server.

### Trust, installation and update

- Download off the single-writer loop with strict time, size, redirect and SSRF controls; validate manifest schema, format version, declared constraints, immutable digest and configured publisher-signature policy.
- A checksum alone does not authenticate a publisher; signature and trust policy require explicit decision.
- Validate archive paths, file types and decompressed limits. Reject secret-bearing, malformed and incompatible packages. Distributable packages contain no credentials or installation-specific secrets.
- Reuse ADR-0467 dry-run planning, per-artifact import decisions and preserved local edits. Never auto-publish an update; operator approval is required.
- An update failure leaves existing deployments unchanged. Rollback of source and republishing does not retroactively migrate existing process instances.
- Script-bearing content is review-gated; a downloaded Worker Type cannot load arbitrary code into the atlas server (ADR-0208).
- No new hot-path allocations, cross-partition access, processor network calls, or duplicated applyToState.

### Repository split

- Public pblumer/atlas: registry interface, Blueprint packaging/provenance rules, Console client and installation/update orchestration over established APIs.
- Separate public community content repository: public index, free Blueprint packages and CI verification.
- Separate private commercial code/services: checkout, entitlement/accounting and protected artifact delivery. Separate private source storage may hold proprietary Blueprint originals.
- These are **proposed ownership boundaries**, not repositories already created or products already implemented. Separate repositories are not an automatic exemption from AGPL obligations; obtain legal review before commercial distribution.

### Consequences

- **Positive:** one consistent open registry protocol supports community, private organizational and commercial sources without making the free engine dependent on a vendor service. Existing application packages remain authoritative.
- **Negative:** new versioned metadata, trust/bootstrap, source precedence, provenance persistence and update UX; commercial operations have separate infrastructure and support costs.
- **Follow-ups:** settle registry wire schema, signature/trust roots and key rotation, compatibility rules, provenance lifecycle, reliable rollback, licensing, and a separate ADR for multiple installations/remapping.

## BMP0 registry v1 contract profile (accepted)

The maintainer accepted option 2 and this signed-registry v1 trust profile on 2026-10-27. **Implementation: Partial** reflects the merged offline schema/fixtures and signed-index verifier; it does not mean a registry endpoint, hub installer or authorization to execute untrusted downloaded code exists.

**Decision: signed, source-independent static metadata.** A registry may be hosted on GitHub Pages or any independent HTTPS server. Operators explicitly configure and pin an **Ed25519** registry public key alongside the registry URL. The registry signs the **exact index JSON bytes** with detached signature `index.json.sig`, base64 encoding of the 64-byte Ed25519 signature. This avoids ambiguous canonical-JSON semantics and self-declared publisher identity. An unsigned or wrongly signed remote index is rejected, not treated as merely unverified. Registry key rotation is a separate, explicit operator action; never bootstrap a trusted key from downloaded content.

The index declares a registry ID, a bounded list of immutable Blueprint releases, semver versions, publishers, descriptions, licence labels, atlas compatibility and required Worker Type/server-constant names. Every release names its **relative** application package path, compressed byte size and exact `sha256:` digest of the package bytes. A signed index does not make the package safe to execute; existing ADR-0467 source/import validation and manual publication remain mandatory.

**MVP policy:** a maximum 1 MiB signed index and 512 entries; 50 MiB compressed package cap and a proposed 200 MiB extracted cap; metadata cannot request arbitrary remote URLs. Package paths are confined to the registry base directory and same HTTPS origin, with cross-origin redirects refused. An admin-managed allowlist for private networks is an explicit configuration decision; user-supplied registry URLs and automatic trust-on-first-use are out of scope. Network/validation runs outside the run loop. Paid packages use an authenticated registry extension later, not reusable public signed URLs.

Release identity is `(registry signing trust, publisherId, blueprintId, version, digest)`. Within an index, `(blueprintId, version)` must be unique and a Blueprint ID must belong to its declared publisher namespace; unknown JSON properties, duplicate object keys and unsupported major schema versions fail closed. Multiple independent registries claiming the same Blueprint identifier are not automatically merged or given implicit priority. A known publisher is **attested by the registry operator**, not proven by mere DNS-shaped metadata.

Compatibility is inclusive `minVersion` and optional exclusive `maxVersionExclusive`; mismatches are surfaced before install. Declared `needs` are advisory until independently checked from the actual imported application models. Installation stores immutable provenance in design-time state, pins the exact package and never alters deployed instances or runs licence checks in `applyToState`.

The wire-shape proposal and deliberately invalid fixtures live at [Blueprint Registry v1 contract](../contracts/blueprint-registry/v1/README.md). **Implementation gate:** ADR accepted and BMP0 assigned to pblumer; deterministic tests for signature/digest/identity/format failure and independent security review of the production network/installation boundary are required before enabling registry downloads. This profile deliberately does not define external publisher onboarding, automatic key rotation, payment processing or multiple re-keyed installations.

### Accepted security trade-offs

- **Operator-pinned signing key vs. HTTPS-only:** HTTPS alone identifies the server connection but not an immutable community publishing authority across mirrors; pinning adds operational overhead and requires key rotation procedures.
- **Signed raw bytes vs. canonical JSON:** raw bytes are straightforward to verify across programming languages, at the cost of resigning when formatting changes.
- **Signed index vs. individually signed packages:** a signed index and pinned package digest prove the exact downloaded bytes originated from that trusted catalog listing. Per-publisher signatures could be added for third-party creators after publisher identity verification and revocation policy are designed.
- **Same-origin artifacts vs. arbitrary CDN URLs:** same-origin is restrictive for future paid content delivery but avoids a broad server-side SSRF/credential forwarding surface in the initial release.

## As built (2026-10-27)

**BMP0 complete:** PR #1402 accepted the signed registry v1 contract and PR #1405 implemented offline Ed25519 index and SHA-256 package verification, with negative and compatibility tests.

**BMP1 partial:** PR #1411 merged an operator-pinned HTTPS client with bounded responses, no redirects and DNS/IP/SSRF protections. PR #1415 merged bounded tar.gz package inspection (manifest, paths, entry types, sizes and GZIP integrity). PR #1416 merged operator-controlled registry configuration and authenticated Console browse/preview/digest-conditional download, handing the verified bytes to the existing ADR-0467 authorization, dry-run/conflict and optional publish import flow. The full GitHub CI, including Windows and browser tests, passed for #1416.

**Not yet delivered:** a publicly hosted, independently signed example registry with a real application and product catalogues, durable installed provenance (registry identity and signing key fingerprint, publisher, Blueprint version and package digest, application identity and catalogue IDs), and restart/repeated-import/partial-failure recovery tests. BMP1 remains active, not complete. PR #1420 verified the test-only application-plus-catalogue bundle (it does not replace a hosted registry). Draft PR #1423 extends the signed preview with catalogue/product identities, without changing the native installer. No engine run-loop, WAL or execution changes were required; auto-safe updates and standalone catalogue artifacts remain gated by Proposed ADR-0506.

## Links

- [ADR-0128](0128-process-applications.md)
- [ADR-0134](0134-git-backed-applications.md)
- [ADR-0467](0467-application-package-export.md)
- [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
- [ADR-0208](0208-worker-type-packages.md)
- [Architecture invariants](../architecture/invariants.md)
