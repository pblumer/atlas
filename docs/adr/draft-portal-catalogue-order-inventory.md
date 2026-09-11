# ADR-DRAFT: Catalogue, order, inventory — three models, not one

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether a column family holding millions of entitlements stays
  within a workable checkpoint. The inventory is engine state by this record, so it
  is written into every checkpoint ([ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)),
  and nobody has measured what that costs at the scale this record sizes for.
- **Question checked:** 2026-09

## Context and problem statement

Atlas executes provisioning today. A model can create an account in Active Directory
or Entra, assign a licence, add a group member, open a Jira issue, call a REST or SOAP
endpoint — durably, with incidents, retries and replay. What it has no notion of is
the thing a person actually arrives with: **a catalogue to order from, and a record of
what they already have.**

The request that prompted this record asks for a self-service portal with several
product catalogues, structured from an ArchiMate model or a SAP system, themed per
customer group, gated by approval, and fulfilled in dependency order. The part that
looks hardest — orchestrating provisioning across systems, in order, surviving a
crash — is the part that already exists. The parts that do not exist are the catalogue
and the inventory.

Both of those look like the same thing: a list of services attached to a person. That
resemblance is the trap this record exists to close. A first cut would store one list
and let the order be a flag on it, because at the moment somebody orders, the catalogue
entry, the order line and the entitlement all say "Alice, VPN access". They diverge
immediately afterwards, and they diverge along three axes that cannot be reconciled in
one structure:

- **Lifetime.** A catalogue entry is withdrawn and replaced. An order completes in
  days. An entitlement outlives both by years, and the instance that created it is
  eligible for retention deletion long before the entitlement ends
  ([ADR-0115](0115-history-retention-hard-delete.md), [ADR-0144](0144-per-definition-history-ttl.md)).
- **Truth.** A catalogue says what *may* be ordered. An order says what *was asked
  for*, and must not change while an approval is pending. An entitlement asserts what
  *is true in another system* — an assertion Atlas cannot guarantee, because target
  systems are changed outside Atlas.
- **Authority.** A catalogue is authored. An order is placed. An entitlement is
  observed or derived. Nobody edits an entitlement directly, and the day somebody can,
  the record of who was granted what stops being evidence.

The question this record answers: **what does the portal store, where does each part
live, and which of Atlas's existing mechanisms does each part reuse?**

Everything about who may see a catalogue, who approves, how personal data is retained
and how a catalogue is themed is deliberately *not* here. Those are companion records,
listed under Links.

## Decision drivers

- The engine must not learn about catalogues. Ordering is design-time data and
  runtime orchestration; neither is a new execution model.
- Work that can happen when a catalogue is published must not happen when somebody
  orders (I5, [ADR-0008](0008-feel-expression-strategy.md)).
- An order must be immune to catalogue edits made while it is being approved.
- A query that grows with the population must not hold the single writer
  (I3, [ADR-0239](0239-off-loop-queries.md), [ADR-0080](0080-runtime-aggregate-counters.md)).
- The inventory must survive the process instance that created it, and must be
  durable for as long as the evidence is needed.
- No second provisioning path. Everything that changes a target system goes through a
  modelled process, so it is visible in the diagram, the replay and the audit trail.
- Single binary ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)): no
  external database, whatever the volume.

## Considered options

1. **One model.** A single "service assignment" record carrying a state — offered,
   ordered, approved, provisioned, revoked.
2. **Two models.** Catalogue separate; order and inventory merged, the inventory being
   "the orders that succeeded".
3. **Three models,** with separate storage and separate lifetimes.

## Decision outcome

Chosen option: **three models** — a versioned design-time catalogue, an order that is
an ordinary process instance, and an inventory that is engine state in its own column
family.

Option 1 fails on lifetime alone: retention would delete the evidence that somebody
holds a privilege, or the privilege would keep a completed instance alive forever.
Option 2 is the near miss and deserves its refutation in full, because "the inventory
is just the successful orders" is true on the first day and false ever after. An
entitlement can exist that no order produced — it predates the portal, or an
administrator created it directly, and the reconciliation below adopts it. An order can
succeed and the entitlement later cease, because somebody removed it in the target
system. The moment either happens, "the orders that succeeded" is a wrong answer to
"what does this person have", and it is wrong silently.

### The catalogue — design-time, versioned, published

A `CatalogItem` is a product or a service; the distinction is its position, not its
type, so the structure nests to arbitrary depth. Items are linked by a `CatalogEdge`
of one of **three** kinds, and the split matters more than it looks — they belong to
two graphs answering two different questions:

- **Structure** — a **composition** (integral, always included, not deselectable) or
  an **aggregation** (optional, separately orderable). These are the two ArchiMate
  relationships the source model already distinguishes, and they say what belongs to
  what.
- **Precedence** — **requires**, saying one service cannot be provisioned before
  another is. This is the edge the fulfilment order is computed over, and the only one
  whose direction means "after".

Writing this record, the two were conflated: it named the structural edges and then
spoke of "the dependency graph" as though that were the same thing. It is not, and
reading them as one graph refuses ordinary catalogues — a workplace that *contains* an
account and cannot be provisioned *before* one exists would be a cycle. Both graphs
must be acyclic; they are checked separately.

An item may have several parents: the same service legitimately appears in several
products.

Each item carries its orderable window (`orderableFrom`, `orderableUntil`), a
withdrawal state, multilingual texts, its variant definition, an approval rule, a
`multipleAllowed` flag, and bindings to two processes: one to provision, one to
deprovision. **A service without a deprovisioning process cannot be published.** A
catalogue that can only grant is not a lifecycle, and the day somebody needs to revoke
at scale is the wrong day to discover the process was never written.

A `Catalog` is a named set of items plus a theme, a group assignment and a **rank**.
The rank resolves the one-catalogue-per-user rule against directory group membership,
which is many-to-many; ranks must be unique, and publishing refuses a tie rather than
falling back to an id nobody chose.

A `CatalogRelease` is a frozen, published version, following the shape applications
already have ([ADR-0128](0128-process-applications.md)). **Publishing is where the work
happens**, and this is I5 applied to a catalogue:

- both graphs are checked for cycles, and the precedence graph is resolved into
  **waves** — rounds in which nothing depends on anything else in the same round, and
  everything it does depend on has already run. Computed once, never derived per
  order, and deterministic: a schedule that reordered between two publishes of one
  input would make a diff of two releases unreadable and fulfil the same order
  differently twice;
- every binding is resolved — a process that no longer exists fails the publish;
- every item has both processes, and translations for every declared language;
- ranks are unique.

A catalogue that interprets a dependency graph at order time is an interpreter on the
runtime path, and a cycle in it is a modelling error that must surface when somebody
publishes, not as an incident for whoever orders at 23:00.

**Nothing is ever deleted from a catalogue** — items are withdrawn. An order placed
years ago and an entitlement still held both resolve through the item, and a store that
can delete one of those is a store that can orphan evidence.

### The order — an ordinary process instance

An `Order` names exactly one `CatalogRelease`. That is the whole of the snapshot rule:
the scope of what was ordered cannot shift because somebody edited the catalogue while
an approval was pending.

An `OrderLine` per position carries its own status, its own approval and its resolved
variant, because a single order can require several independent approvals.

**A line's status distinguishes three ways of not being provisioned**, and merging
them would break two things at once. *Failed* is a defect: something broke, an
operator repairs it, it retries. *Rejected* is a decision: nothing is broken, somebody
said no, and there is nothing to repair — filing it as a failure raises incidents
nobody can close and reports a decision as a malfunction. *Blocked* is a consequence:
the line was never attempted because something it requires failed or was rejected, and
it carries the **root** cause rather than the intermediate blocked line between, since
that one is blocked for the same reason and naming it makes a reader walk the chain.
A fourth, *skipped*, is a line the recipient already holds; it satisfies its dependents
exactly as a provisioned one does, or the inventory would block a line for the reason
that it was unnecessary.

*Blocked* is therefore **derived, never stored**, and recomputed on every pass. That is
what makes a repair effective: an operator who fixes the incident behind a failed
precondition releases the line that was waiting on it, with nobody rewriting a status
by hand. And it is what decides how long an order lives — **an order stays open while
any blockage can still be repaired.** A failure is an incident somebody can fix, after
which the line runs after all; a rejection will not change, so waiting on one is
waiting for nothing. One rejection among a line's causes settles it whatever happens to
the rest. Settling an order while an incident behind it is being worked would tell the
orderer their line is never coming, at the moment somebody is fixing the reason it has
not. A generic
fulfilment process works the release's waves: every line in a wave starts its
provisioning process as a call activity, and the next wave begins when the current one
settles. When a line fails, every line that does not depend on it continues; dependent
lines stop and raise an incident. Partial fulfilment is the intended behaviour, not a
degraded mode.

**The schedule is waves rather than a sequence precisely because of that failure
rule.** A flat topological list answers "what before what" and nothing else — it has
already discarded the reason each item sits where it does, so a failure in the middle
of it stops everything after, including branches that never depended on the failure.
The first implementation of this record built the list, and the gap surfaced when the
fulfilment process was designed against it. An item's wave is one past the *latest* of
its preconditions, so "everything this needs has already run" is true at every wave
boundary.

Waves alone are still not enough, and the same design pass found why. A wave is the
right unit to **run** in and the wrong unit to **start on**: at a wave boundary the
only fact available is "the previous wave is done", which cannot tell a line whose
precondition failed from one whose precondition succeeded beside it. A wave-wide
barrier therefore either starts a line whose precondition is missing or holds one
whose preconditions are all present — both wrong. So a release carries **both**: the
waves, which are what a person reads and what the fulfilment process parallelises
over, and each line's direct preconditions, which are what decides who a failure
takes with it. The release answers that question itself, so the orchestrator never
reconsults the catalogue.

Two resolutions happen in the basket, before the order exists, and both are shown to
the person rather than decided for them: a service the ordering user **already holds**
is marked as held and skipped, and the same service pulled in twice in **different
variants** is a conflict the orderer resolves. Where the service arrived by composition
it cannot be dropped, so only the variant is choosable; by aggregation, the optional
position can be removed.

### The inventory — engine state, its own column family

An `Entitlement` records that a principal holds a service, in a variant, since when,
from which order, against which target, in which state. It lives in a new column family
in the state store, written through the log and rebuilt by `applyToState` like every
other engine fact (I4, [ADR-0001](0001-event-sourcing-and-log-structured-state.md)).
Pebble carries millions of keys; the volume is not the reason to put it elsewhere, and
there is nowhere else to put it that does not break
[ADR-0011](0011-single-binary-distribution-and-web-ui.md).

Three things follow and none of them is optional:

- **Retention must exempt this column family.** The instance that produced an
  entitlement is eligible for deletion long before the entitlement ends. This is an
  explicit amendment to [ADR-0115](0115-history-retention-hard-delete.md) and
  [ADR-0144](0144-per-definition-history-ttl.md), not an oversight to be discovered.
- **Every inventory query runs off the loop.** They grow with the population, which is
  precisely what [ADR-0239](0239-off-loop-queries.md) removed from the single writer.
  The catalogue's "already held" marking reads only the asking user's entitlements —
  bounded per user, but it still takes the read view rather than the loop.
- **An entitlement stores a principal reference, never personal data.** Names,
  addresses and superiors resolve from the account. This is what keeps `applyToState`
  free of any key access, and it is what makes the personal-data record's erasure
  mechanism possible at all — an append-only log cannot forget what it was given in
  the clear.

Entitlements carry their **origin**: `ordered` when an order produced it, `adopted`
when reconciliation found it in a target system and somebody accepted it, `legacy` when
it came from the initial load at commissioning. The distinction is the difference
between evidence and an assumption. On the first day the inventory is empty and reality
is full; without `legacy`, the first reconciliation reports every existing privilege in
the estate as a discrepancy, and each of those carries an executable "remove in the
target system". A field that costs nothing now costs a migration of an append-only
column family later.

### Reconciliation — report with an action, never act alone

Atlas holds the intended state, reconciles against the target systems, and records
**transitions** rather than samples — the shape `api/panorama/drift.go` already uses,
for the reason given there: nobody reads a graph of "healthy, healthy, healthy". Unlike
Panorama's journal this one is durable, because it is evidence rather than a reading
surface.

A discrepancy is never acted on automatically. It carries two offered actions:
**remove in the target system**, which runs the service's deprovisioning process — not a
direct worker call, or there would be a second provisioning path outside the model and
outside the audit trail — and **adopt into the inventory**, which writes an entitlement
with origin `adopted`.

Atlas does not enforce. A system that silently removes privileges it did not grant locks
people out on its first bad reconciliation, and the value here is in naming the
discrepancy, which no other system in the estate can do.

### Consequences

- **Positive:** The catalogue is data and the fulfilment is a model, so a new service is
  authored, not coded. Dependency order, cycle freedom and binding integrity are proven
  at publish time. An order cannot change under an approver. Evidence outlives the
  instance. Nothing reaches a target system except through a process.
- **Negative / trade-offs accepted:** Three stores instead of one, and a rule per store
  about what may write it. Retention gains an exemption, which is a thing an operator
  must understand rather than infer. The basket carries two resolution steps that a
  simpler design would decide silently. Publishing can fail for reasons an author must
  then fix — which is the point, but it is friction at authoring time.
- **Follow-ups / risks to watch:** The checkpoint cost in the open question above, to be
  measured before the inventory is built rather than after. The initial load at
  commissioning is a deliverable, not a script somebody writes on the day. Whether a
  service can be held more than once is per-service data here; if that turns out to be
  per-variant, it is a migration.

## Pros and cons of the options

### One model
- Good: one store, one set of handlers, no question about which record is authoritative.
- Bad: one lifetime for three lifetimes. Either retention deletes evidence of a held
  privilege, or a completed instance is kept alive to preserve it. Editing the catalogue
  edits pending orders.

### Two models (order and inventory merged)
- Good: the inventory needs no separate write path — it is the successful orders.
- Bad: cannot represent an entitlement no order produced (pre-existing, or created
  directly in the target system), nor one that ceased outside Atlas. Both are the normal
  case, not the exception, and both fail silently.

### Three models
- Good: each store has one lifetime, one notion of truth and one writer. Reuses
  applications for versioning, the engine for orders, the state store for the inventory.
- Bad: the most machinery. Three stores must agree about what a service *is*, which is
  what the catalogue item id is for and what a review must check.

## Implementation

`api/catalog` carries the catalogue model and `Publish` — the validation, the wave
schedule and the preconditions described above, with `Release.Blocked` answering which
lines a failure stops. `api/order` carries the order model and the propagation:
`Propagate` marks what a settled outcome stopped, and `Derive` reads an order's own
standing off its lines rather than storing it. The order, the basket and the inventory are not built yet,
which is why this record reads `Partial`.

## Links

- builds on [ADR-0128](0128-process-applications.md) — versioned, publishable bundles
- builds on [ADR-0028](0028-forms-and-the-tasks-app.md) and [ADR-0029](0029-public-process-start-links.md) — forms, the Tasks app and public start
- builds on [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the ArchiMate binding mechanism the import reuses
- amends [ADR-0115](0115-history-retention-hard-delete.md) and [ADR-0144](0144-per-definition-history-ttl.md) — the inventory is exempt from retention
- constrained by [ADR-0239](0239-off-loop-queries.md) and [ADR-0080](0080-runtime-aggregate-counters.md) — inventory queries run off the loop
- constrained by [ADR-0011](0011-single-binary-distribution-and-web-ui.md) — no external database
- companion records, to be written: personal data and erasure in the portal; portal
  roles and responsibilities, amending [ADR-0209](0209-roles-per-endpoint-group.md),
  [ADR-0180](0180-groups-as-members.md) and [ADR-0278](0278-object-authorization.md); a
  theme per catalogue, amending [ADR-0113](0113-org-wide-ui-theme.md) and
  [ADR-0263](0263-form-runtime-brand-theming.md); portal language following the browser,
  an exception to [ADR-0267](0267-console-speaks-german-first.md)
