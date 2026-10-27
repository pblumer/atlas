# Blueprint Marketplace (BMP): delivery programme

**Programme:** BMP (Blueprint Marketplace)  
**Baseline:** Public atlas repository checked on 2026-10-26.  
**Milestone:** [Milestone 6 — Ecosystem](../../ROADMAP.md#milestone-6--ecosystem-).  
**Architecture authority:** [ADR-0499](../adr/0499-blueprint-registry-and-distribution.md) (**Accepted / Partial**).  
**Programme owner:** pblumer (accepted 2026-10-27). **BMP0:** complete (owner pblumer, PR #1402 and #1405). **BMP1:** active (owner pblumer). BMP2/BMP3 remain planned with no implementation owner.

## User outcome

An authorized atlas operator finds a free Blueprint from an independent static registry, previews the complete Process Application and its requirements, fetches a pinned immutable version, verifies it, reviews an import plan, configures missing dependencies and explicitly publishes. A later release shows a safe update preview that keeps local edits. Registry downtime or expired commercial entitlement never interrupts already deployed instances.

## Existing foundations and gaps

| Concern | Current repository baseline | Gap |
| --- | --- | --- |
| Process Applications and versioned local releases | ADR-0128, implemented | No Blueprint origin/version attached to an application |
| Portable packages and import preview | ADR-0467, implemented | No registry download and immutable release verification |
| Existing Repository | ADR-0081, partial | Curated templates only, no remote registry; installed templates not yet applied in the Modeler |
| Git-backed authoring | ADR-0134, partial | Git binding absent; not a prerequisite for static registry downloads |
| Worker Type metadata and security | ADR-0208, partial | Third-party packages must not load code in-process |
| Information/Panorama models and catalog media | ADR-0467, excluded from package | Declare missing content; do not claim it travels |

## Scope and non-goals

- Shared *catalog, discovery, transport, trust*, not a new Process Application package format.
- Distinguish Blueprint ID/version/digest, application key, deployed application release and authoring Git source.
- Public integration permits self-hosted registries; customers do not require a central account to install free content.
- The first static registry may use GitHub, but the API is provider-neutral.
- Premium checkout, payments, publisher settlement, ratings, universal licensing, process-instance migration, and multiple differently keyed installations of one Blueprint are **not** MVP scope.
- No secrets, generated credentials or foreign server principal IDs in downloaded packages.
- No private pricing, internal commercial strategy or customer-specific positioning in public documents.

## Vertical slices and acceptance

### BMP0 — Contract and trust foundation (architecture gate)

**Complete:** [PR #1402](https://github.com/pblumer/atlas/pull/1402) accepted ADR-0499 and introduced the signed v1 contract with fixtures; [PR #1405](https://github.com/pblumer/atlas/pull/1405) implemented tested offline Ed25519 verification and SHA-256 checks. This is not yet a working external registry or a package installer.


Define and review the versioned registry index, globally namespaced publisher/Blueprint identity, immutable release digest, compatibility, installation needs, signature/trust bootstrap, source collisions and threat model. Propose installation provenance representation and required authorization gates.

**Done only when:** ADR is accepted or replaced; human owner assigned; validated examples and invalid contract fixtures exist. No premature registry implementation.

### BMP1 — Public registry to executable Process Application

Expose a free Blueprint from a static GitHub-backed index; browse from the Console; download and verify immutable package off-loop; invoke existing import-plan APIs; require explicit publish; record source/version/digest and resume after restart.

**Test obligations:** mock registry, schema/digest mismatch, invalid signature policy, decompression bomb/path traversal, SSRF/redirect, incompatible atlas version, missing Worker, unauthorized import, failed network, unchanged re-import no-op, provenance reload, e2e install and offline engine restart.

### BMP2 — Managed update and recovery of source

Pin installed versions. Show compatible later releases and compare source with existing local changes using ADR-0467 import baselines; preserve the current version on failure and require explicit approval to import/publish.

**Test obligations:** unchanged, remote-ahead, local-ahead and diverged cases; partial failures; persisted provenance after restart; no automatic instance migration; clear rollback procedure for source and deployed versions.

### BMP3 — Federated/private sources (separate commercial implementation)

Support multiple trusted public/private sources and external authenticated downloads with a provider-neutral contract. Demonstrate against a mock entitlement provider; **never** put purchase/accounting services or runtime licence checks into the public atlas execution engine.

**Test obligations:** origin collision, expired download token, redaction, credentials not sent to redirects, registry isolation, offline use after installation, and safe source withdrawal.

## Engineering gates

- ADR-0499 Accepted and BMP0 complete; BMP1 is owned by pblumer. The public HTTPS transport must pass redirect, IP/DNS, size and signature failure tests before any API/Console integration. BPM2/BMP3 still require owners and dependencies.
- Follow AGENTS.md, TDD, API route table/OpenAPI, MCP drift classification, Console security, and changelog discipline.
- Sidecar state on the run-loop owner and new network operations off the run loop. Do not touch engine, WAL or applyToState.
- Reuse one portable application package and the existing import-plan machinery.
- For code work run: go build ./..., go test ./..., go test -race -timeout=45m ./..., go vet ./..., gofmt -l ., and relevant e2e.
- Report exact limitations in release notes: package transport does not yet include information models, Panorama models, server constants, Workers or all catalogue media.

## First next action

BMP1: PR #1411 merged the bounded public HTTPS registry client with pinned Ed25519 identity, no redirects, DNS/IP checks and verified archive digests. Draft PR #1415 inspects the existing tar.gz application package with bounded expansion, path/link/duplicate/manifest/CRC guards, returning metadata without installing. Draft PR #1416 adds an operator-pinned public Registry browser with signed release previews and a digest-conditional package GET, then reuses the existing authorized Console import workflow. Durable provenance, restart/recovery tests and a signed free demo registry remain open gates. **No automatic installation, runtime licence checks or direct Worker code execution.**

## References

[ADR-0128](../adr/0128-process-applications.md), [ADR-0134](../adr/0134-git-backed-applications.md), [ADR-0467](../adr/0467-application-package-export.md), [ADR-0081](../adr/0081-community-marketplace-for-connectors-and-tasks.md), [ADR-0208](../adr/0208-worker-type-packages.md), [Programme Delivery Protocol](../development/program-delivery.md).
