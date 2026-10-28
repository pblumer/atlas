# ADR-0506: Marketplace catalogue bundles and opt-in updates

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-27
- **Deciders:** atlas maintainers (approval pending)

## Context and problem statement

Most Marketplace product catalogues belong to a Process Application.
The offer should install the application **with its catalogues** as one
reviewable solution. Some catalogues should also be installable independently.
Availability may be public/free, organizational/internal or commercially
licensed, and customers may optionally request automatic updates.

Existing foundations:
- [ADR-0467](0467-application-package-export.md) already exports an
  application **and** selected catalogues as one package with `atlas.json`,
  `katalog.json`, `fragen.json` and `antworten.json`. Imports use one
  established conflict-planning flow.
- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md) handles
  standalone catalogue/product documents under catalogue authorization.
- [ADR-0451](0451-catalogue-export.md) exports catalogue documents, optionally
  including bound processes. It does not carry all dependent forms, DMN
  models or Worker configuration.
- [ADR-0499](0499-blueprint-registry-and-distribution.md) already defines
  signed, immutable Marketplace releases and explicitly **forbids
  automatic publish**. This proposed ADR must be approved before changing
  that policy, even for previously authorized updates.

The signed v1 index strictly accepts only application `.tar.gz` releases.
Adding a different kind must not silently weaken its validator.

## Drivers

- Bundle first: application and its related catalogues are normally a single
  Marketplace offer, sharing version, provenance and an explicit import plan.
- Catalogues may alternatively stand alone and retain their native format.
- Reuse existing catalogue and application import, permissions, ownership,
  process binding checks and release semantics; introduce no second importer.
- Separate the *shop catalogues* distributed as content from the Marketplace
  index itself.
- Never overwrite a locally modified catalogue/product/process without consent.
- Keep purchased/installed applications and catalogue releases usable offline.
- Prevent unattended updates from bypassing authorization, publication
  approvals or durable-before-visible ordering.

## Options considered

1. **Bundle-first plus optional native catalogue-only artifact** with one
   discovery/trust/verification infrastructure and dispatch to existing APIs.
2. Distribute every catalogue and application separately; consumers manually
   resolve dependency and publication order.
3. Introduce a universal Marketplace installer with new write paths.
4. Wrap every catalogue in an artificial, empty Process Application.

## Proposed decision

Choose **option 1**.

### Releases and dependencies

- `application-bundle`: a single immutable ADR-0467 `.tar.gz` containing
  the application and zero or more related catalogues. Preferred offer kind.
- `catalogue`: a standalone ADR-0436/0451 `catalog.Document` and optional
  exported bound processes. Not a fictitious application.
- A signed **registry v2** distinguishes artifact kinds, explicit versioned
  media types and declared dependencies. v1 remains valid for existing
  application packages, and v1 clients must reject unknown v2 formats.
- Signed component declarations are advisory. Independently inspect actual
  application source, catalogue identities, product home/offer links,
  bindings and prerequisites before allowing import.
- Provenance records pinned registry identity, signing-key fingerprint,
  publisher, offer ID, version, digest, installed application key and
  catalogue IDs in restart-safe design-time sidecar state.
- A product offered by two catalogues retains its native home catalogue;
  another bundle cannot silently assume ownership or overwrite it.

### Installation and publication

- The normal installation unit is the **bundle**. Show one combined preview
  of applications, catalogues, products, process bindings, missing resources,
  local modifications, principal mappings, and publication effects.
- An **Install** action may appear to be one click *only when* identity,
  authority, dependencies, conflict decisions and publication permission
  are already resolved. Otherwise pause for explicit choices or refuse.
- Reuse ADR-0467 import planning and ADR-0436/0451 import/publish; the
  actual user must have each native role, including productmanager/owner
  and modeler when processes are deployed. A Marketplace download
  entitlement does not confer target installation roles.
- An imported catalogue publish changes future customer offerings.
  Running instances and past orders must not be retroactively changed.
- Current catalogue sidecar writes may fail partway through; importing
  bound processes can leave deployments in place even when later steps
  fail. Do not pretend that existing multi-step imports are atomic.

### Updates

Default to `manual`, with local per-installation policy:
- `manual`: present new releases and require a person to approve changes.
- `notify`: check compatible signed releases periodically, show an import
  diff and notify; never apply automatically.
- `auto-safe` (**proposed, blocked pending separate recovery gate**):
  only after explicit preauthorization of a specific installation,
  catalogue publication scope and accepted security policy; apply only
  when there are no local edits/conflicts, no unresolved identities,
  no missing dependencies, no new privileges/Worker Types, no destructive
  removal and no unreviewed script content. Otherwise stop and request
  human intervention.

Never support automatic forced overwrites. Do not implement `auto-safe`
by simply scheduling calls to the current multi-step import chain:
first prove recoverable idempotent updates, persistent provenance and
consistent failure handling across application import, catalogue writes,
publish and acknowledgement. All fetching and update planning are
off the processor run loop with deterministic tests and an injectable clock.

### Distribution and commerce

Content type, source access and business offer are independent:
public/free, private/internal/free, public/premium-discovery with
protected download, and private/premium sources. One paid offer may grant
both application and catalogues; standalone add-ons may have separate
entitlements. A protected package must not be published at an unauthenticated
public URL. External commercial services handle purchase and download
authorization; atlas does not handle checkout or runtime licence checks.
An unavailable entitlement service prevents **new downloads**, never
existing instance execution or previously published catalogue releases.

## Risks and consequences

The model enables reusable complete business solutions and standalone
catalogue extensions without duplicating established import formats.
The trade-offs are a v2 index, explicit source/dependency identities,
catalogue release provenance and controlled update scheduling.

**Architecture conflict:** accepted ADR-0499 prohibits automatic publish.
Only a new accepted decision may authorize narrowly scoped pre-approved
catalogue publication. Until then all updates remain manual or notify-only.
**Recovery conflict:** existing catalogue import cannot guarantee a full
multi-store rollback after a write failure; unattended updates are blocked.

## Required tests and approval gates

1. Signed v1/v2 negotiation; app+multiple-catalogue bundle; catalogue-only
   document; conflicting product home, missing processes/DMN/Workers, and
   source identity collisions.
2. First install and unchanged re-import; local-ahead, source-ahead and
   diverged updates with explicit overwrite/keep decisions and role checks.
3. Single combined preview with real catalogue release effects and no
   write on a cancelled or incomplete plan.
4. Restore/restart between each import and publish stage; compare durable
   installation/provenance and ensure a failed update does not claim success.
5. Free public bundle demonstration, standalone catalogue demonstration,
   private and premium mocks, and operation while the source is unavailable.
6. No scheduling or execution inside `applyToState`; no hot-path
   allocation, cross-partition access or licence check.

## Delivery placement

**BMP / ROADMAP Milestone 6 — Ecosystem.** Complete existing BMP1
Console/authorized package import first, then demonstrate a real
application+catalogue bundle and durable provenance. Add standalone
catalogue kind only after v2 design approval; manual/notify updates belong
to BMP2, protected distribution to BMP3. Any `auto-safe` slice is gated
by persistence/recovery architecture approval and must have a named owner.
The programme manifest will be reconciled after the pending PR #1416
finishes its shared-contract changes.

## Links

- [Blueprint Marketplace delivery plan](../planning/blueprint-marketplace-program.md)
- [Catalogue bundle extension plan](../planning/blueprint-catalogue-bundles.md)
- [ADR-0499](0499-blueprint-registry-and-distribution.md)
- [ADR-0467](0467-application-package-export.md)
- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md)
- [ADR-0451](0451-catalogue-export.md)
