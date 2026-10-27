# ADR-DRAFT: Open Blueprint registries for process applications

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-26
- **Deciders:** atlas maintainers (review pending)

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

## Decision outcome (proposed)

Choose **option 2**. A Blueprint is an immutable, versioned distribution wrapper around the **existing application package**. The marketplace can also list element-template and Worker Type packages, but it does not replace their native package formats.

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
- A commercial service may separately provide purchase, entitlements and authorized download. Purchased applications continue to run offline and no process-instance execution checks the marketplace licence server.

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

## Links

- [ADR-0128](0128-process-applications.md)
- [ADR-0134](0134-git-backed-applications.md)
- [ADR-0467](0467-application-package-export.md)
- [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
- [ADR-0208](0208-worker-type-packages.md)
- [Architecture invariants](../architecture/invariants.md)
