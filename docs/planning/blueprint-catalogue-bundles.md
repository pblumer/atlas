# Blueprint Marketplace extension: applications and product catalogues

**Programme:** BMP (Blueprint Marketplace)
**Milestone:** [6 — Ecosystem](../../ROADMAP.md#milestone-6--ecosystem-)
**Status:** planning proposal, **not implementation**
**Programme owner:** pblumer
**Architecture gate:** [ADR-0506](../adr/0506-marketplace-catalogue-bundles-and-updates.md).
Existing [ADR-0499](../adr/0499-blueprint-registry-and-distribution.md)
remains binding until an extension is accepted.

## Outcome

An organization finds a business solution in the Marketplace, clicks
**Install**, reviews what it will add, and receives a Process Application
**with its product catalogue(s)** on its own atlas instance. It may also
install a reusable standalone catalogue. Each installed item can be
configured for manual updates, update notifications, or — only after a
separate recovery and publication-policy approval — safe automatic
updates. Items may be free, sold separately, or available only within an
organization, without any licence dependency in the workflow run loop.

**Product catalogue** here means the *catalog.Document* containing
actual products, approvals, lifecycle bindings and catalogue releases.
It is not the Marketplace's own offer listings.

## Recommended user experience

### Marketplace browsing

Two consumer-visible ways of presenting the **same offer registry**:

- **Solutions** (default): app+catalogue bundles. A tile shows the
  application, included catalogues, products, compatible atlas versions,
  Worker dependencies, publisher, licence terms and update status.
- **Catalogues:** standalone reusable catalogues and optional add-ons.
  The detail view shows required application/process bindings and the
  products provided. An already installed compatible dependency is
  recognized rather than re-imported without permission.

An offer identifies access/price separately from content kind:

| Source/access | No charge | Paid entitlement |
| --- | --- | --- |
| Public | Community/free solution or catalogue | Public discovery, authenticated private package download |
| Internal/private | Organization-only free bundle/catalogue | Contractual or departmental entitlement, private delivery |

An internal offering need not be paid, and a paid offering need not be
public. All releases still require operator-pinned registry trust.
Terms, price and checkout may be displayed by an external provider, but
are not part of the engine and do not authorize importing into local
catalogues the purchaser does not maintain.

### Installation interaction

The button **Install** initiates a verified combined **preview**:
- Included Process Application, catalogues, products and releases.
- Dependencies: processes/forms/DMN, available configured Workers,
  missing server constants, identity mappings, shared-home products.
- Expected effects: create/modify/publish, with exact target ownership.
- Signed origin, version and SHA-256 digest; publisher and licence.
- What will remain unchanged and where local edits require decisions.

A first installation may finish with one confirmation when the target
already has all required authorizations and dependencies. Otherwise
the existing ADR-0467/0436 conflict and principal-choice dialogs are
mandatory. Do **not** equate "one click" with permission bypass.
No undocumented auto-publish during a read/preview.

A combined `application-bundle` reuses the existing `tar.gz` with
`katalog.json`. This should be the first testable free Marketplace
example, **without waiting for a new package serializer or registry v2**.
A standalone `catalogue` item uses native `catalog.Document`, with
signed v2 kind dispatch only after the ADR gate.

### Installed content and updates

Per *installed offer*, not a global toggle:

| Policy | Behaviour | Implementation gate |
| --- | --- | --- |
| Manual (default) | Show installed and available versions, compare, explicit import/publish | BMP2 |
| Notify | Verify signed latest release on a schedule, report a dry-run diff, no writes | BMP2 |
| Auto-safe (opt-in) | Apply only changes proven non-conflicting and pre-approved, otherwise pause | New acceptance/recovery gate |
| Auto-force | **Unsupported**: overwrites locally edited products/processes | Never |

The owner may pause all checks, pin a version, or revoke automatic
consent without deleting installed content. Shared catalogues require
explicit per-catalogue ownership and update scope. A release that
removes/withdraws a product, changes eligibility, approval rules,
process bindings or grants new privileges is a human-review event,
not a silent update.

A Marketplace entitlement authorizes obtaining new bytes. It must
**not** be checked when a running process executes or a previously
published catalogue serves a product. A paid provider may control
upgrade rights without retracting already installed releases.

## Existing code and exact reuse

| Existing capability | Current status | Reuse |
| --- | --- | --- |
| Signed static v1 app registry + bounded HTTPS fetch | Landed (BMP0, BMP1) | Shared trust/source boundary |
| Application package + optional `katalog.json` | Landed, ADR-0467 | Primary combined solution delivery |
| Standalone catalogue export/import | Landed, ADR-0436/0451 | Catalogue-only offers after v2 |
| Per-artifact import conflict baselines | Landed, ADR-0467 | Safe update diff for apps/catalogues |
| Catalogue authorization and publish | Landed, ADR-0436 | Same imported records, no new write API |
| Console registry browsing/preview/merge | In review, PR #1416 | Finish before extension touches shared UI |
| Durable Marketplace provenance | Not yet delivered | Required before reporting install success |
| Fully recoverable multi-stage automatic updates | Not yet delivered | Blocks Auto-safe |
| Authenticated/private and paid delivery | Planned BMP3 | External entitlement provider and mock |

A catalogue may bind processes located outside its associated
application. These are **dependencies**, not automatically imported
application resources. Native catalogue exports can carry bound
processes but omit forms, decisions and configured Workers. An
installation preview must report these omissions accurately.

## Vertical implementation order and tests

### C1 — Free solution bundle, integrated end-to-end (BMP1)

After PR #1416 lands, publish an actual **signed public bundle** with
an application and at least one catalogue. Verify installation through
the current application-package importer and catalogue release flow,
including a first-install and repeated no-op import, matching product
process bindings, correct permissions and no unintended publication.
Record the catalogues alongside application origin/version/digest in
persistent sidecar provenance and assert restart/reload equality.

**Acceptance:** one interactive solution installation in the Console
with a real free package; no duplicate app, products or releases on
repeat; local edits preserved and all source/deployment effects shown.

### C2 — Native standalone catalogue in the Marketplace

Accept proposed ADR and introduce a signed **registry v2** contract
with `artifactKind`, media type, verified component identity and
version compatibility. Keep v1 accepted unchanged; a v1 client must
reject v2 rather than misinterpret it.

Dispatch `catalogue` through ADR-0436/0451 importer after a combined
preview and productmanager/owner checks. Cover catalog/product ID
collisions, shared product home, missing bound processes/forms/DMN,
same-source and cross-source integrity, and import failures.

**Acceptance:** install a real standalone signed free catalogue with
a publishable product without creating an artificial Process Application.

### C3 — Manual + notify-only managed updates (BMP2)

Persist installed version, publisher/source fingerprint and **local
update policy**. Check for compatible newer signed releases outside
the run loop; present a diff using ADR-0467 import baselines. No
automatic write and no automatic publish. Test offline registries,
downgrade attempts, partial failures, local/remote divergence, new
dependencies and restored provenance after restart.

**Acceptance:** operators can see and selectively install an update,
or subscribe to notifications without applying changes automatically.

### C4 — Private/premium sources and optional safe automation (BMP3 and gated BMP2)

First test authenticated private download and mock entitlements
without ever exposing the credentials to publishers/redirects or
to the engine hot path. Next, design and approve **recoverable** update
application across application/import/publish/provenance stages.

Only after this proven recovery boundary may the operator choose
`auto-safe` for an installed item and a narrowly approved catalogue
publication scope. Inject fake clock/scheduler and test crash points,
rollback/manual recovery, unauthorized changes, conflicting local
edits, interrupted entitlement and registry outage.

**Acceptance:** a compatible, non-conflicting, pre-authorized update
can apply and survive restart; all other updates pause for review.
Historic process instances and orders retain their original semantics.

## Programme coordination

Do **not** change `docs/programs/bmp/program.json` or the generated
`docs/programs/README.md` in this proposal PR: PR #1416 is already
changing those shared-contract files. Once #1416 is integrated,
the programme owner updates BMP1/BMP2/BMP3 and adds any dedicated
catalogue slice with an assigned owner, dependencies and proof
before beginning code work. No parallel registry wire contract
modifications while a Proposed ADR is unresolved.

## Explicit non-goals

A general-purpose marketplace payment processor in atlas, one-click
bypass of catalogue authorizations, multiple re-keyed installations of
the same Blueprint, implicit process-instance migration, and
automatic removal of products or application resources.

## References

- [ADR-0499](../adr/0499-blueprint-registry-and-distribution.md)
- [ADR-0467](../adr/0467-application-package-export.md)
- [ADR-0436](../adr/0436-a-catalogue-is-imported-as-one-document.md)
- [ADR-0451](../adr/0451-catalogue-export.md)
- [Blueprint Marketplace programme](blueprint-marketplace-program.md)
