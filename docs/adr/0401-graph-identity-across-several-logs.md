# ADR-0401: What a node is called when there is more than one log

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Open question:** what the definition checksum is taken over. The question this record
  was written with — XML or the compiled form — was **measured in 2026-09 and answered:
  neither, as they stand.** See *What the measurement found* below. So the open question
  is now the narrower one it left: what a canonical projection of the executable model
  should contain, given that it has to be layout-immune like the compiled form and
  independent of the compiler's internals like the XML. Nothing needs it yet — the
  finding it enables requires two runtimes — so it is deliberately unbuilt rather than
  undecided.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0403 settles that an estate graph is a
projection and that merging logs is not what it needs. What it does need is that two
servers' derived subgraphs can be named in **one namespace**, and that is an identity
question nothing in the tree has answered.

"Merging several WALs" turns out to be three different problems wearing one phrase, and
only the first is free:

**(a) Several partitions of one node.** A key carries its partition in the high 16 bits
(`[16 bit partition][48 bit counter]`, see `docs/architecture/data-model.md`), so within
one installation every entity key is globally unique *by construction*, and a union of
partition logs needs no renaming at all. There is no global ordering across partitions —
`Position` is per-log — and a graph does not need one: causality inside a partition is
`SourcePos`, and across partitions it is explicit message passing and nothing else
([ADR-0006](0006-partition-routing-and-cross-partition.md), invariant I3). A node runs
one partition today (`api/node.go:193`, `Partitions: 1`), which is why this has never had
to be said.

**(b) Several nodes of one estate.** Here the keys collide outright: two installations
both mint partition 0, counter 1, and nothing in a key says which installation minted it.
This is the case Atlas is heading into — one instance per domain
([#986](https://github.com/pblumer/atlas/issues/986)).

**(c) The same node's log across time.** Archived segments, a restored backup, a
compacted log plus its checkpoints
([ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)).

Two identities that *do* carry across a node boundary already exist, and neither was
built for this:

- the **runtime id** in the node descriptor — minted once and persisted, served at
  `GET /api/v1/node` behind the narrow `status` scope
  ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) P4a), and already
  bindable from a model as `atlas.runtimeId`;
- a deployment target's **`Bindings` map** — local application id to the id the same
  application has on that target, *learned from the remote's reply on the first
  successful promotion* ([ADR-0129](0129-remote-deployment-targets.md), see
  `api/targetstore.go`).

The second is the more interesting find: the estate already records, as durable operator
configuration, that two applications on two servers are one application. Nobody built it
as a join key, and it is one.

## Decision drivers

- **An identity that has to be minted is an identity that has to be reconciled.** A new
  estate-wide id would need a registry, a protocol to hand it out, and an answer for the
  installation that has been running for two years without one.
- **A guess is worse than a duplicate.** A graph that silently merges two nodes because
  their names match is wrong in a way the reader cannot see. A graph showing two nodes is
  wrong in a way they can.
- **Infrastructure is not a business party.** #986's central correction — a BPMN
  participant must not be an Atlas instance, because moving a domain between servers
  would then change the model — has the same shape here.
- **Keys are engine-internal.** A process instance key is meaningful to the partition that
  minted it. Publishing it estate-wide as a bare number invites correlation that is only
  accidentally correct.

## Considered options

1. **A new estate-wide id**, minted per resource and reconciled between nodes.
2. **Qualify every existing identity with the runtime that minted it**, and use the
   identities the estate already records where they exist.
3. **Content addressing throughout** — every node identified by a checksum of what it is.
4. **Name matching** — join on process id, application name, worker name.

## Decision outcome

Chosen: **option 2, with content addressing confined to the one place it belongs.** Four
rules.

### 1. A runtime entity's estate-wide name is `(runtimeId, key)`

Not a new identifier: both halves exist today and both are already published. The key
keeps its meaning (it still carries its partition, so case (a) stays free), and the
runtime id says which log minted it. A bare key is never published estate-wide.

### 2. A definition and a deployment are two nodes, not one

This is the change that makes case (b) answerable at all. A starmap `process` node today
is *a deployed process on this server* — a definition and a deployment conflated, which
is harmless while there is one server and fatal with several.

Split them:

- a **definition** is estate-wide, identified by what it *is*: process id plus version,
  with the deployed model's checksum as the tie-breaker;
- a **deployment** is `(runtimeId, definition, deployedAt)`, and it is the thing that has
  instances, counters, incidents and a state.

Two drawings follow that the estate actually needs and cannot have today: *this process
runs in three domains*, and *these two claim the same version and are not the same
bytes*. The second is a finding; nothing today can produce it.

**A definition is derived always and drawn only where it says something.** On a single
runtime a definition has exactly one deployment, always — the collector reads the latest
deployment per process id — so a definition node there stands in a permanent 1:1 relation
to a process node. That is a field rather than a second thing, and drawing it would spend
the measured 400-node budget (ADR-0211 §7) to restate what the deployment already says:
200 processes would become 400 nodes and installations that paint today would collapse.
The identity is therefore always derived and the node appears exactly where a definition
has more than one deployment, which is the first case where it carries something a reader
cannot get from the deployment. The expense this record wanted paid early is paid in the
derivation, which is where the accretion it feared was happening.

### What the measurement found

The open question above — XML or compiled form — was tested over the seven system
processes, and both candidates fail on different axes:

| | XML checksum | compiled-form checksum |
|---|---|---|
| The layout transplant (ADR-0124/0251) | **splits a definition an operator sees as one** — nudging only `bpmndi:` changed the XML in 7 of 7 | **keeps it whole** — the compiled form was byte-identical in 7 of 7, because the compiler never reads `bpmndi:` |
| Deterministic within one build | yes, trivially | **no, unless the encoding sorts maps** — with `joinReach`, `searchableSet` and `decisionPins` left in iteration order, the same model hashed differently within one process |
| Survives an Atlas upgrade | yes | **no** — the values are intern-table indices, so the hash is a function of the compiler's private struct; removing any of six probed fields moved it |
| Needs a format frozen forever | no | yes — `CompiledProcess` has no serialization at all, so a checksum means inventing one |

The third row is the decisive one. A definition is recompiled from stored XML on every
start (`api/deploystore.go` keeps "the original BPMN XML, enough to recompile the
definition"), so the compiled form is whatever the running binary produces — the same
model would get a new identity after any release that touches the compiler, and
ADR-0400's own implementation touched it.

So the tie-breaker wants a third thing neither option named: a **designed, public,
layout-immune projection of the executable model**, independent of how the compiler
happens to represent it. That is its own slice of work. It is not in the identity today,
and the reason is recorded rather than the field being added with the wrong content: on
an XML checksum, every diagram nudge would publish the finding *"these two claim the same
version and are not the same bytes"* as a false positive, which is worse than not being
able to state it.

### 3. An application is joined only where a promotion joined it

The `Bindings` map is the join key, and it is the **only** one. Two applications that
merely share a name are two nodes, drawn as two nodes. This is deliberately conservative:
the join is then a fact the server can point at (an operator promoted this application to
that target and the target replied with its id), which is the same test every starmap edge
already has to pass (ADR-0211 §1). Name matching would let the picture invent estate
structure, and an invented edge on a derived picture is worse than a missing one because
the picture's whole claim is that it invented nothing.

The consequence is stated plainly rather than hidden: **an estate that has never promoted
anything has no cross-node structure to draw**, only a set of unjoined subgraphs. That is
the truth about such an estate.

### 4. Case (c) gets no identity scheme

A restored log is the same node's own history, and Atlas already refuses the one dangerous
case — a portable backup does not overwrite another installation's identity
([ADR-0357](0357-a-portable-backup-does-not-overwrite-another-installations-identity.md)).
Anything that has to outlive the retention window belongs to tier T4, where retention
already lives. Merging archives into a live graph would produce a graph of a population
that no longer exists, presented beside one that does.

### A runtime node is an operator's node

Following #986's correction: a runtime appears on the estate graph as infrastructure —
the thing that *holds* deployments — and must never become the identity of a modelled
thing. A capability, a value stream, a product and a published interface are estate-wide
by nature and are named without a runtime id. If one of them ever needs qualifying by the
server it happens to sit on, that is a sign it was modelled at the wrong altitude.

### Consequences

- **Positive:** nothing new is minted, nothing has to be reconciled, and every
  installation already has both halves of every name. Case (a) needs no work; case (b)
  needs the definition/deployment split and nothing else; case (c) is out of scope with a
  reason.
- **Positive:** the definition/deployment split is worth having even on a single server —
  it is what lets a version-drift comparison be stated as a comparison rather than as two
  lists.
- **Negative / trade-offs accepted:** the split is a change to a shipped node kind. The
  starmap's `process` node, its id scheme (`KindProcess + ":" + …`), its drilldown links
  and the bindings that resolve against it all assume the conflated form, and the
  ArchiMate and C4 projections map from it (ADR-0211 §8). This is the expensive part of
  the whole idea and it is expensive here rather than later.
- **Negative:** cross-node structure is limited to what promotion recorded. An estate that
  deploys to every domain by hand gets a set of islands, correctly.
- **Negative:** `(runtimeId, key)` is a pair, not a string, and everything that logs,
  exports or links a runtime entity has to carry both or be explicit that it is local.
  Half-qualified identifiers are the predictable defect.
- **Follow-ups / risks to watch:** whether the checksum is taken over the XML or the
  compiled form (the open question above); whether a `Bindings` entry should be reversible
  from the target's side, so that the joined pair is symmetric rather than known only to
  the publisher; and what happens to a `Bindings` entry when an application is deleted on
  one side.

## Pros and cons of the options

### Option 1 — a new estate-wide id
- Good: one identifier, unambiguous, no pairs to carry.
- Bad: needs a registry and a protocol, needs an answer for every existing installation,
  and creates a second source of truth for identity beside the two that already exist.

### Option 2 — qualify with the runtime, join on what promotion recorded (chosen)
- Good: built from published identities; nothing to reconcile; case (a) free by
  construction; every join is a fact the server can point at.
- Bad: identifiers become pairs; cross-node structure limited to promoted applications;
  requires splitting a shipped node kind.

### Option 3 — content addressing throughout
- Good: identity without coordination, and exactly right for a definition.
- Bad: wrong for anything with a lifecycle. Two instances doing identical work are not the
  same instance, and two workers configured identically against two hosts are not the same
  worker. It also makes the legitimate byte differences of the layout transplant into
  identity differences.

### Option 4 — name matching
- Good: free, and works on the demo.
- Bad: silently wrong, invisibly. Two domains both running `order-intake` for unrelated
  business would be drawn as one process, and no reader could tell.

## Links

- honors I3 (single writer, partition ownership); builds on the key layout in
  `docs/architecture/data-model.md`
- builds on ADR-0189 P4a (the runtime id), ADR-0129 (deployment targets and their
  `Bindings`), ADR-0006 (cross-partition is message passing only), ADR-0357 (a backup does
  not take over an identity)
- required by ADR-0402; bounded by
  ADR-0403
- relates to [#986](https://github.com/pblumer/atlas/issues/986), whose correction about
  participants is the same argument one altitude up
