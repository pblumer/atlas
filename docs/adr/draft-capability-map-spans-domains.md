# ADR-DRAFT: The capability map spans domains; a node only ever sees its own

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-21
- **Deciders:** Atlas maintainers
- **Open question:** how the estate-wide map is *read* — whether a node federates by querying its peers, or whether the map is replicated and one node is its source of truth. Both need a cross-installation identity that does not exist in the tree beyond [ADR-0129](0129-remote-deployment-targets.md) deployment targets; [ADR-0373](0373-published-process-interface.md) carries the same question one level down and reaches the same conclusion
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0305](0305-business-capabilities-and-value-streams.md) gave Atlas a business
architecture: a flat list of capabilities, ordered value streams whose stages name
them, and a **gap report** that compares that map against what the installation
actually runs. It is design-time state in `api/capability`, per node, like every
other design-time store.

The estate it describes is not per node. Atlas is built to run **one instance per
domain** — the premise [ADR-0369](0369-cross-instance-message-addressing.md) states
outright and the one the whole cross-instance epic rests on. A value stream is the
customer's path through the business, and a customer's path does not stop at a
domain boundary: the stages before and after a hand-off are performed by
capabilities that live on different nodes.

The map has no way to say that, and the gap report is wrong about it in both
directions. `Gaps` (`api/capability/gaps.go`) compares the capability records
against a `Landscape` built from **this server's** deployments and Workers, so:

- if every node holds the whole estate map, each foreign realisation raises
  `realization.missing` — *"no process %q is deployed in application %q on this
  server"*, which is true and is not a defect;
- if each node holds only its own slice, every cross-domain `requires` raises
  `requires.unknown` — *"this capability requires %q, which is not in the map"*,
  where the map it means is this node's.

Neither is a modelling mistake. Both are the report describing the limit of one
node's eyesight as somebody's architecture defect — the one thing the file's own
header says it must never do.

`Restricted` does not cover it. It counts what the **caller's access** hid
([ADR-0071](0071-sharing-scopes.md)): a process present in the landscape as a
placeholder because the reader may not see it. A process on another node is not in
the landscape at all, and no grant would put it there.

Underneath both sits a plainer gap: **a domain is not modelled.** ADR-0305
deliberately keeps business areas as a tag and refuses a `parent` field, and that
refusal is right. But nothing today says which domain performs a capability, so
nothing can tell "not done" from "not done *here*".

The question: **how does a per-node capability map describe an estate of one node
per domain — without inventing the hierarchy the method refuses, and without
building a federation that nothing in the tree can yet support?**

## Decision drivers

- **The method's refusal of hierarchy stands.** ADR-0305 leaves out `parent` on
  purpose, because an end-to-end capability is regularly invoked from inside
  another one and any tree is wrong from some direction. A domain must not become
  that tree by another name. A domain says *who performs this*; it never says
  *what contains this*.
- **Report only what was checked.** The gap report already publishes `Checked` and
  `Restricted` rather than quietly comparing less than it claims. A third blind
  spot owes the same honesty and the same shape.
- **No migration, and silence costs nothing.** An installation that never mentions
  a domain must behave exactly as it does today, down to the finding counts. The
  feature has to be invisible until somebody opts in.
- **Reuse the identity that exists.** `/api/v1/node` already carries an
  operator-owned `Labels map[string]string` (`api/node.go`), bounded and editable
  through the API, and ADR-0369 already names that descriptor as the node's stable
  runtime identity. A node saying which domain it serves needs no new setting.
- **Declaring is not deriving.** ADR-0305's rule — the report compares, it never
  merges — holds here too. Atlas must not infer a capability's domain from where
  its realisation happens to be deployed; that would let the incomplete half
  overwrite the complete one the first time something is deployed to the wrong
  place.
- **Do not build a federation before there is one.** Reading one map across nodes
  needs cross-installation identity and a discovery model. ADR-0373 hit the same
  wall and named it as its open question rather than guessing. This record does
  the same, and ships the part that is buildable alone.

## Considered options

For **what carries the domain**:

1. **A reserved tag, `domain:<key>`, on the capability, and a node label saying
   which domain this node serves.**
2. **A new `domain` field** on the `Capability` and `ValueStream` records.
3. **A new `Domain` record** — name, owner, node address — as a third record kind
   in `api/capability`.
4. **Convention only**: an ordinary tag, documented, with nothing reading it.

For **what the gap report does with a capability performed elsewhere**:

- **a.** A third outcome, `Elsewhere`, counted like `Restricted` and raising no
  finding.
- **b.** Filter foreign records out of the comparison entirely.
- **c.** Leave the findings as they are; let readers learn which ones to ignore.

For **where the map lives**:

- **i.** Each node holds its own domain in full, plus a **reference record** for
  each foreign capability it depends on.
- **ii.** The whole estate map is replicated to every node.
- **iii.** One designated node holds the estate map; the others hold nothing.

## Decision outcome

Chosen: **1** for the domain, **a** for the report, **i** for where the map lives —
with the estate-wide *read* named as an extension point rather than decided.

### A domain is a tag, and a node says which one it is

A capability carries a reserved tag `domain:<key>`. The prefix is the only reserved
one, validation refuses a record carrying two of them — a capability is performed
by exactly one domain, and two tags is an unanswerable map rather than a rich one —
and everything else about tags is unchanged.

The node says which domain it serves through its existing identity:

```json
{"labels": {"domain": "kredit"}}
```

Nothing else changes about the node descriptor, and a node with no `domain` label
is a node that has not opted in: every comparison below falls through to exactly
today's behaviour, including the counts. That is the whole of the migration story.

A tag rather than a field, because a domain *is* a business area and ADR-0305
already decided business areas are tags with no record of their own. A tag rather
than a `Domain` record, because a record would want an owner, an address and a
lifecycle, and every one of those already has a home: the owner is the capability's
owner, the address is an ADR-0129 deployment target, and the lifecycle is the
node's. Option 3 is the concept this record most wants to add and most has to
refuse.

### `Elsewhere` is the third thing the report can say

`GapReport` gains `Elsewhere int`, counted once per capability performed by a
domain other than this node's, beside `Restricted` and published the same way. For
such a capability the comparison **suppresses exactly the findings that are claims
about this server's deployments**:

- `realization.missing` — the realisation is not here because it is not supposed
  to be here;
- `capability.unrealized` — a foreign capability has no realisation *in this map*
  by construction, and saying so would put every hand-off on the adoption backlog.

Everything else still fires. `requires.unknown` remains a real finding, because a
dependency naming a key that is in **no** record is a broken map whichever domain
performs it. `stage.unknown`, `process.unclaimed`, `process.shared` and
`call.undeclared` are all statements about this node's own content and are
untouched.

One finding is added, and it is the reason the tag is worth reading at all:

- **`domain.misplaced`** — a capability tagged with another domain that
  nevertheless has a realisation deployed here. Either the tag is wrong or the
  deployment is in the wrong place, and both are worth a sentence. Without this
  check the tag convention decays silently, which is the failure mode every
  convention has.

The shape is deliberately the one `Restricted` already has: a placeholder that
resolves, counted once, published in the answer. A reader who sees
`elsewhere: 14` knows the report compared less than the map, and why.

### The map on a node is its domain, plus the contracts it consumes

A node holds the capabilities its domain performs, in full. For each foreign
capability it depends on it holds a **reference record**: the same `Capability`
shape, tagged with the foreign domain, carrying what a consumer legitimately knows
— key, name, scope, `inputs`, `outputs`, `owner`, `slas` — and **no**
`realizations`. How the other domain does it is not this node's business, which is
the method's black box stated as data.

That is the same object as a published interface ([ADR-0373](0373-published-process-interface.md))
one level up. The interface descriptor is the machine-readable half — entry points,
payloads, returns, and optionally a commitment — and the reference record is the
half a person reads: what it is for, who owns it, what it has promised. They
describe one boundary and should not be authored twice; **deriving one from the
other is the extension point named below**, not something this record decides while
the epic is unbuilt.

Replication (option ii) was rejected because it is the library problem from
[ADR-draft-shared-artifact-library](draft-shared-artifact-library.md) one level up,
with no publish step to hang it on: twenty copies of one map, drifting, and no
answer to which is right. A designated node (option iii) was rejected because it
inherits every foreign realisation as `elsewhere` and so knows the least about the
thing it is supposed to own.

### What is deliberately not decided

- **The estate-wide read.** Nothing here lets one screen show the whole value
  stream across domains. That needs either a federation — each node querying its
  peers, with an identity and a grant per peer — or a source-of-truth map with a
  distribution mechanism. It is the open question in the front matter, and it is
  the same one ADR-0373 asks about interface discovery. Answering both at once,
  later, is likely cheaper than answering either now.
- **SLA and commitment as one statement.** A capability's `slas` (metric,
  threshold, window, counterparty) and an entry point's **commitment** in ADR-0373
  ("by when an accepted message will be correlated, and to whom that is promised")
  are the same promise written for two audiences, and
  [ADR-0370](0370-durable-message-buffer.md) already turns a lapsed commitment into
  a breach rather than a silent outcome. Binding them — the commitment derived from
  the SLA, the breach counted against it — is the one place this map would stop
  being a declaration and start being measured. It is named here so the seam is
  deliberate, and left undecided because the epic that would carry it is
  `Implementation: Not started` across all of ADR-0369 to ADR-0373.

### Slices

1. **The tag and the label.** Reserved `domain:` prefix, the two-tag refusal, the
   node label, and the `domain:` filter on the capability listing. No report change
   — useful alone, because it makes the map readable by domain.
2. **`Elsewhere` and `domain.misplaced`.** The suppression, the count, the new
   finding kind (additive: `FindingKinds()` is already served in the authoring
   subset, so a client renders it without a second change).
3. **Reference records.** The consumer-side foreign capability, its validation
   (a foreign record carries no realisations), and the value-stream read that shows
   a stage performed elsewhere as such rather than as a hole.

### Consequences

- **Positive:** the gap report stops reporting a domain boundary as a defect, in
  both directions, and says how much it did not compare. A value stream becomes
  readable across domains, and the stages where it changes domain are exactly the
  places the cross-instance epic needs a published interface — so the business map
  becomes the plan for the technical one instead of a parallel document. Nothing
  changes for an installation that does not opt in. No hierarchy is introduced, and
  the method's flat list survives.
- **Negative / trade-offs accepted:** the domain is a convention with one check
  behind it, not a modelled entity, so a capability with no `domain:` tag on a node
  that has one is ambiguous and is treated as local — a deliberate choice that
  favours today's installations over strictness. A reference record is content
  maintained on the consuming node, so it can go stale against the domain that owns
  it; the confirmation mechanism dates it but cannot verify it, exactly as ADR-0305
  says of every other field. And the estate still has no single screen.
- **Follow-ups / risks to watch:** whether a reference record should be *imported*
  from the owning node rather than typed (which is the federation question wearing
  a smaller hat); whether `domain.misplaced` needs an exemption for a deliberate
  second deployment during a migration between domains; and whether the
  `domain:` tag should later become a field after all, if a second reserved prefix
  ever appears and the two start to need different handling.

## Pros and cons of the options

### Option 1 — a reserved tag plus a node label (chosen)
- Good: no new record kind and no new field; consistent with ADR-0305's decision
  that a business area is a tag; reuses a node identity that already exists and is
  already operator-owned; entirely inert until an operator sets the label.
- Bad: a convention rather than a constraint — nothing stops a capability going
  untagged, and only `domain.misplaced` notices one tagged wrongly.

### Option 2 — a `domain` field on the record
- Good: unambiguous, validatable, and impossible to misspell past validation.
- Bad: a second classification axis beside tags in a design whose whole claim is
  that tags are the only one; and a required field would make every existing record
  incomplete on the day it lands.

### Option 3 — a `Domain` record
- Good: a home for the owner, the node address and the lifecycle of a domain, and
  the thing an estate of twenty domains probably does want.
- Bad: every field it would carry already has a home (the capability's owner, an
  ADR-0129 target, the node itself); it is a containment layer in all but name, and
  the moment it exists somebody will nest capabilities under it.

### Option 4 — convention only
- Good: nothing to build.
- Bad: leaves the false findings in place, which is the entire problem.

### Option a — `Elsewhere` (chosen)
- Good: matches `Restricted`'s established shape; the report keeps saying what it
  checked; the suppression is narrow and enumerable.
- Bad: a third counter in an answer that already has two, and one more thing a
  reader has to understand before trusting a zero.

### Option b — filter foreign records out
- Good: simplest to implement.
- Bad: the report would silently compare less than the map contains — the precise
  behaviour `Restricted` exists to prevent.

### Option c — leave the findings
- Good: no code.
- Bad: a report whose readers are taught to ignore rows is a report nobody reads,
  and the rows to ignore grow with every domain added.

## Links

- extends [ADR-0305](0305-business-capabilities-and-value-streams.md) (the
  capability and value-stream records, the realisation edge, the gap report and its
  comparison-never-merge rule — all inherited unchanged; the domain is the business
  area that record already keeps as a tag)
- rests on [ADR-0369](0369-cross-instance-message-addressing.md) (one instance per
  domain, the premise that makes a per-node map insufficient)
- relates to [ADR-0371](0371-participant-binds-a-published-interface.md),
  [ADR-0372](0372-peer-message-delivery-worker.md) and
  [ADR-0373](0373-published-process-interface.md) (the hand-off between domains as
  the runtime sees it; a value stream that changes domain at a stage boundary is
  where one of these interfaces belongs, and the reference record is the same
  boundary described for a person)
- relates to [ADR-0370](0370-durable-message-buffer.md) (a lapsed commitment is a
  breach — the mechanism that would let a declared SLA finally be measured)
- relates to [ADR-0071](0071-sharing-scopes.md) (`Restricted`, whose shape
  `Elsewhere` copies, and which it must not be confused with)
- relates to [ADR-0128](0128-process-applications.md) and
  [ADR-0134](0134-git-backed-applications.md) (the application as the unit of a
  capability, and the portable key a realisation names)
- relates to [ADR-0129](0129-remote-deployment-targets.md) (the deployment target,
  which is where a domain's address already lives)
- relates to [ADR-draft-shared-artifact-library](draft-shared-artifact-library.md)
  (the same estate model one layer down: a library is a domain's library, and a
  reference record is to the map what a declared dependency is to an export)
- relates to [ADR-0147](0147-splitting-the-api-server-object.md) (`api/capability`
  is an area service, and everything above is a change inside it)
