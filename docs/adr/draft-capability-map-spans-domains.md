# ADR-DRAFT: The capability map spans domains; a node only ever sees its own

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-21
- **Deciders:** Atlas maintainers
- **Open question:** what grant a node presents when it reads a peer's map. The *shape* of the estate-wide read is settled — a live read, each node asking its peers, no replicated map and no source-of-truth node — and so is what comes back, the reference-record shape below. What is not settled is the cross-installation identity behind it: nothing in the tree carries one beyond [ADR-0129](0129-remote-deployment-targets.md) deployment targets, and [ADR-0373](0373-published-process-interface.md) leaves the neighbouring grant (`observe`) undecided for the same reason
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

- **The grant behind the estate-wide read.** Nothing here lets one screen show the
  whole value stream across domains, and two of the three questions that would need
  are already answered, so state them rather than leave the whole thing open.

  *How it is read* is a **live read**: each node asks its peers when somebody opens
  the picture, so whichever node you have open is the centre of it. There is no
  replicated map and no node designated to hold one — the estate map has no writes
  to arbitrate, so it needs neither a source of truth nor anything elected, and a
  peer that does not answer degrades to the `unreachable` the landscape already
  models. The machinery is the one ADR-0189 §6 already uses for deployment targets:
  resolved off the run loop, bounded concurrency, a deadline, a response-size limit,
  verified TLS, per-target error isolation.

  *What comes back* is the **reference-record shape** — key, name, scope, inputs,
  outputs, owner, SLAs, domain — and never a peer's local map in full. A peer's
  `realizations` say which applications and processes exist behind its boundary, and
  a picture must not become the way to read another domain's internals without a
  grant. The read discloses what that domain already published, and nothing it did
  not.

  It carries one thing more: **the domain names that peer knows** — its own, and
  the ones its reference records name. Names only. Without them the width of a map
  would depend on how diligently somebody here typed foreign dependencies, rather
  than on what the estate contains, and a domain nobody here ever dealt with would
  be missing from every picture with nobody to miss it. With them, a viewer can say
  *billing exists and this node has no way to reach it*, which is a finding an
  operator can act on rather than a hole nobody sees.

  Two bounds make that safe, and both already have a precedent in the tree. **A
  name travels; an address never does.** Panorama's `Target` discloses a peer's
  name to every caller and withholds its base URL and credential reference, because
  those are "this operator's map of where their infrastructure lives"
  (`api/panorama/mesh.go`) — the same line holds here. And **learning a name is not
  learning a peer**: it must never auto-configure a target. Adding one stays an
  operator's act, because a target is a trust relationship
  ([ADR-0129](0129-remote-deployment-targets.md)), and a picture that quietly
  acquired peers would be deciding who this node talks to.

  Stated honestly, the name list does disclose a peer's dependency neighbourhood —
  that credit works with billing and with fraud. That is what a map of an estate is
  for, and it is the same order of disclosure the reference record already makes;
  it is written down here rather than discovered later by somebody who assumed the
  answer was scoped to the asker.

  *How far it reaches* is **one hop**. A peer answers for its own domain, out of
  its own store, and never forwards the question or aggregates an answer on
  somebody else's behalf. The read is therefore not recursive, and that is a
  property rather than a limit somebody might later relax: no request causes
  another request, so no cycle can form between two nodes that each list the other,
  no hop counter or visited-set is needed to notice one, and the cost of opening a
  view is exactly the number of peers this node has configured — never that number
  squared, and never a burst that grows as an outage is retried across an estate.
  Depth beyond one would also make every node a proxy for a read it has no
  authority to relay, which is the grant question below arriving through the back
  door.

  The honest cost is that **the picture is only as wide as the viewer's own peer
  list**: if this node knows the credit domain and the credit domain knows
  billing, billing is still not on this node's map. It must not look complete
  either — a foreign capability whose domain this node cannot reach is *named but
  not reached from here*, which is a third thing to say beside `unreachable` (a
  peer configured, asked, and silent) and `Elsewhere` (a peer reached, reporting
  its own). Saying which of the three is the same discipline `Restricted` and
  `Checked` already apply: publish the blind spot rather than render around it.

  *Under what authority* is the part left open, and it is a real gap rather than a
  formality: reading a peer's map is adjacent to the `observe` grant ADR-0373 names
  and deliberately does not decide. Answering it there and here at once is likely
  cheaper than answering either alone.
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
- **Put the map read on the freshness contract that already exists.** A live read
  is a read somebody can leave running: an architecture view on a wall refreshing
  every half minute, or twenty nodes asking each other, is a load this server
  inflicts on itself and on every peer. Atlas has already answered that for the
  peer *descriptor* read and the answer is in constants, not in prose
  (`api/panoramaremote.go`): an 8-second per-peer deadline, at most 4 peers asked
  at once, an answer served for 30 seconds before asking again, and a failed
  refresh reporting the last answer as **stale** for 15 minutes before it becomes
  **unreachable**. The map read rides that contract rather than inventing a second
  one, and three things follow that this record does not settle: whether a map
  answer shares the descriptor's window or earns a longer one of its own — a
  capability map changes far more slowly than reachability, but a second number is
  a second thing to keep honest; that **stale must mean the same thing for the map**
  as it does for the descriptor, so a domain served from a failed refresh is drawn
  as history rather than as healthy; and whether this path wants the breaker from
  [ADR-0340](0340-worker-circuit-breaker.md), since bounded concurrency limits one
  view's burst but nothing yet stops a persistently dead peer from being asked
  again on every open. Deciding the window with the feature is cheap; retrofitting
  it after the first domain complains about load is not.
- **Build the domain edge so it can be drawn.** A cross-domain dependency is the
  first relationship in Atlas that a landscape could honestly draw across a node
  boundary, and Panorama is one decision away from it: it already draws a deployment
  target as a node — the only kind whose state comes from outside the process, and
  so the only one that can be *unreachable* or *stale* — but derives **no** edge to
  it, because "a promotion is an act, not a stored relationship" and a line would
  assert something nobody stated (`api/panorama/mesh.go`, `Target`;
  [ADR-0211](0211-panorama-derived-landscape-mesh.md)). A declared dependency is
  exactly the stored relationship a target is not. So whatever holds it — the
  `domain:` tag and reference record here, the `interfaceRef` resolution in
  [ADR-0371](0371-participant-binds-a-published-interface.md) — should be persisted
  as a resolvable pair rather than as free text, so an edge is *derivable* from it
  later without re-authoring anything. This record does not decide the drawing, and
  it must not: a picture across a boundary has to distinguish what this node
  observed from what it was told, the way [ADR-0374](0374-white-box-participant.md)
  distinguishes a cached contract from a live look inside. The point is only to not
  foreclose it by storing the relationship in a shape no edge can come out of.

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
- relates to [ADR-0340](0340-worker-circuit-breaker.md) (an outage stops at the
  worker, not at every token — the posture a peer read that keeps failing should
  probably adopt)
- relates to [ADR-0211](0211-panorama-derived-landscape-mesh.md) and
  [ADR-0374](0374-white-box-participant.md) (the landscape that draws a peer without
  an edge today, and the rule that a picture across a boundary must not claim to see
  through it)
