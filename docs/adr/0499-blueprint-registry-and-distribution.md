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

## Amendment 2026-11-02: optional release tags and the sample registry

**Decision (owner pblumer).** A v1 release may carry an optional `tags` list: at most 16 unique lower-case labels of 1–32 characters (`[a-z0-9]`, inner hyphens). Tags are signed with the rest of the index and only steer presentation — a Console filters the listing by them. They grant nothing, are not trusted as a description of the package's behaviour, and an installation draws no security or entitlement conclusion from them. The field stays in schema version 1 because no registry had been published yet: a server that predates the amendment refuses an index carrying `tags`, as it refuses every unknown property, so an installation must run a build that includes the amendment before it is pointed at a registry that uses them. After a registry is public, a further field needs the same explicit compatibility review rather than a silent schema extension.

**Sample registry.** `atlas hub keygen|lock|build` is the publisher side for the examples of this repository. Small examples are bundled by theme into a few applications and the handbook's flagship examples keep their own application (`examples/hub/bundles.json`); `examples/hub/releases.json` records every `(bundle, version, digest)` so that a version never names two contents. The private key is a CI secret; servers hold only the public key, pinned out of band. Only the latest version of each bundle is listed until BMP2 decides on archive retention.

## Amendment 2026-11-02 (b): two pinned keys for a rotation

**Decision (owner pblumer).** `ATLAS_BLUEPRINT_REGISTRY_KEY` pins one Ed25519 public key or two, separated by a comma, and an index signed by either is accepted. Both are explicitly configured by the operator, out of band, exactly as one key is; nothing is learnt from the registry, so the profile's "no trust-on-first-use, no key from downloaded content" rule stands. Two is the limit: the second key exists for the length of a planned rotation — pin old and new, switch the signer, unpin the old — and a server pinning two logs a WARN (`hub.registry_keys_pinned`) at every start until one is removed. More keys, a key twice or a malformed one fail startup closed. A compromised key is never pinned beside its successor; that procedure replaces the key outright. Considered and rejected: a key list or successor key inside the signed index (a compromised key could then name the attacker's successor), and an unbounded list (a standing set of trust roots v1 does not have). The operator runbook is [examples/hub/README.md](../../examples/hub/README.md#rotating-the-signing-key).

## Amendment 2026-11-02 (c): the fixed announcement channel for a new key

**Decision (owner pblumer).** A planned key rotation is announced in exactly one way: a statement (`atlas-hub-key-rotation/v1`: registry id, old fingerprint, new public key and fingerprint, announcement day, earliest switch day at least seven days later), signed with the **current** key over its exact bytes and published at a fixed place, `examples/hub/key-rotation.json` with `.sig` on the repository's `main` branch (copied beside the index on Pages). Operators verify it with `atlas hub verify-announcement` against the key they already pin and accept a new key from nowhere else. A forger then needs the current private key, not a convincing mail; the lead time gives the owner days to notice an announcement they did not make. A compromised or lost key cannot vouch for its successor, so an emergency rotation is confirmed in person: the owner reads the new fingerprint to each operator over a contact channel agreed before any incident, and a signed statement is never valid for it. Considered and rejected: a successor key inside the signed index (an index is fetched automatically, which would turn a stolen key into a silent takeover), announcements by mail or on the registry host alone (unauthenticated), and a separate long-lived "root" key (a second secret to guard, with no gain over the current key for the planned case). Runbook: [examples/hub/README.md](../../examples/hub/README.md#the-fixed-announcement-channel).

## Amendment 2026-11-03 (d): installing a release server-side, and over MCP

**Decision (owner pblumer).** `POST /api/v1/blueprints/{id}/releases/{version}/import` installs a release's application server-side: the server fetches it from the pinned registry, requires `?digest=` equal to the reviewed preview's, inspects it and hands the verified bytes to the existing source import (ADR-0467) — one importer, the same plan, decisions, editor rule and needs as `POST /api/v1/applications/source`. It exists so a client that cannot carry package bytes, an agent over MCP (ADR-0016), installs exactly what the Console does with nothing it could alter in between. The MCP tools `atlas_hub_list_releases`, `atlas_hub_preview_release` and `atlas_hub_import_release` proxy the list, the preview and this route; the import tool is a dry run unless asked, passes `overwrite`/`keep` only as the person decided, does not publish, and leaves catalogues — whose questions name this installation's groups and people — to a person in the Console. The raw package route stays out of MCP.

## Amendment 2026-11-06 (e): the way to the registry, station by station

**Context.** An installation behind a firewall answered every registry failure with one sentence, "trusted Blueprint registry is unavailable or invalid", and logged nothing. A blocked connection, a proxy-only network, a TLS inspection and a wrongly pinned key looked the same, from the Console and from the log.

**Decision (owner pblumer).** `GET /api/v1/blueprints/connection` (role modeler, like the listing) loads the signed index exactly as `GET /api/v1/blueprints` does — the same `PublicClient`, transport, vetting and refusals, not a probe beside it — and streams each station of that load as it is passed or where it stops, one JSON object per line: name resolved and vetted as public, TCP connection on 443, TLS handshake, index read, signature verified. A passed station carries its time and what it established (the answer's addresses, the address that answered, TLS version and certificate issuer, status and size, the fingerprint of the key that matched); a stopped one carries a cause from a fixed vocabulary (`timeout`, `refused`, `nonpublic_address`, `unknown_authority`, `no_pinned_key`, …). The done line carries the verified index and the releases this atlas is outside the compatibility range of, so the Console needs no second load. The Console draws the stations as a route from this server to the hub, with a running timer on the station being waited on and the usual meaning of the cause where the way ends.

- **What a caller is shown.** The cause and the facts above are shown to anybody who may list the hub: the registry URL is the operator's, not the caller's, so nothing here turns the server into a probe of a target the caller chooses. The error text a station stopped at can name this network's resolver or addresses, and is sent to administrators only. Every failure is also logged for the operator as `hub.registry_unavailable`.
- **Unchanged.** The client still ignores `HTTPS_PROXY`, follows no redirect, dials only vetted public addresses on 443 and trusts the operating system's roots. The connection route makes those rules visible; it does not relax any of them.

## Amendment 2026-11-09 (f): a refused request names the station too

**Context.** Amendment (e) gave the connection route its stations, but the routes a person and an agent use next — the listing, the preview, the package and the import — still answered a registry failure with one sentence. A person who opened a release after the registry had become unreachable read "trusted Blueprint registry is unavailable or invalid" in the Console and nothing more; an agent over MCP read the same.

**Decision.** Every load and download of the pinned client keeps the station it stopped at, traced or not, and returns it in its error (`blueprintregistry.StopError`, read with `Stopped`). The routes answer such a failure with 502 and the error envelope extended: `error` is the former sentence followed by `(stopped at <stage>: <cause>)`, `station` is the stopped station as the connection route sends it — stage, cause, the facts it established — and `registryHost` is the configured host. The Console's hub dialog explains it from the same vocabulary as the route and offers to check the whole way again. A release's download is a station of its own after the index, `package`, with one cause of its own: `package_mismatch`, bytes other than the size and SHA-256 the signed index states.

- **What a caller is shown.** Exactly what amendment (e) shows on the connection route, to the same role (modeler): the station, its cause and facts. The error text is sent to administrators only, and every failure is still logged as `hub.registry_unavailable`. An error that names no station keeps the plain sentence.
- **Unchanged.** The routes, their roles and their refusals; nothing about how the client connects or what it trusts.

## Amendment 2026-11-11 (g): the server records where an installed application came from

**Context.** The ADR asks for installation provenance — registry, signing key, release, digest, application — and nothing recorded it: an application installed from the hub could not be traced back to the package it came from. Draft PR #1424 proposed a pending-only journal the Console would write through a separate route before its browser-driven import, never marked complete, because a browser's report is no proof of what the server wrote. Since amendment (d) the server performs the application import itself, so it can record what it fetched and what it wrote in one place. #1424 was closed in favour of this.

**Decision (owner pblumer).** The server-side import (`POST /api/v1/blueprints/{id}/releases/{version}/import`) records, under the application's key, where the application came from: the registry ID the signed index names, the index URL the operator configured, the fingerprint of the pinned key whose signature verified that index (during a rotation the one that signed — `blueprintregistry.Origin`, from the same index snapshot as the bytes), the publisher, Blueprint, version and exact digest, the catalogues it left to the Console, who installed it and when. The record lives in `blueprint-installations`, design-time state of this installation (class instance): it never travels with an export and is never an engine event.

- **Order.** In the import's own run-loop turn, before the importer writes anything, the record is saved as `pending`; when the record cannot be saved, nothing is imported. When the import finishes it becomes `installed`. An import the importer refuses or stops for an undecided change writes nothing, so the record goes back to what it was (or away). An import that fails after it may have written keeps `pending`. A repeat of the same package into the same application keeps the first record. No other request sees the pending state; only a server that stopped in between leaves it, and the next finished import of a release replaces it.
- **The Console installs through it.** The atlas hub dialog sends its application step, dry run and real, to this route instead of uploading the downloaded package to the source import, so a person's install and an agent's are recorded alike. The Console still reads the downloaded package for its constants and catalogues and imports those itself.
- **Read.** `GET /api/v1/blueprints/installations` lists the records from this server's disk, with the registry unreachable or unconfigured; it is an administrator's, because it says who installed what from where. The MCP tool `atlas_hub_installations` proxies it.
- **Not decided here (#1422).** The record covers the application's source only: catalogues, constants and a published release are separate writes the Console makes afterwards and are not part of it. Nothing reconciles a pending record with what the application holds, a deleted application keeps its record, and updates and rollback stay BMP2.

## Links

- [ADR-0128](0128-process-applications.md)
- [ADR-0134](0134-git-backed-applications.md)
- [ADR-0467](0467-application-package-export.md)
- [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
- [ADR-0208](0208-worker-type-packages.md)
- [Architecture invariants](../architecture/invariants.md)
