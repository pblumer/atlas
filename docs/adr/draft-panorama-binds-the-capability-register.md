# ADR-DRAFT: Panorama binds the capability register

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers

## Context and problem statement

Two things in Atlas now describe the same architecture and do not know about each
other.

Panorama holds an architect's drawing: ArchiMate elements, including a `Capability` in
the strategy layer, with Atlas bindings ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §4)
saying which Atlas resource each one refers to. The capability register
([ADR-0305](0305-business-capabilities-and-value-streams.md)) holds a record: what has
to be done, its scope, its business owner, what it requires of others, and the KPIs and
SLAs it is held to.

Draw *Underwrite a loan* and file a capability keyed `loan-underwriting`, and nothing
connects them but the fact that a person wrote a similar phrase twice. Rename either
and not even that survives. So the drawing and the register are two architectures, and
two architectures disagree — which is the failure ADR-0189 was written to prevent for
processes and applications, arriving again one layer up.

A second, smaller thing was wrong. ArchiMate's `ValueStream` was accepted by the
validator and absent from the authoring palette. An architect could open a model
containing value streams and edit around them, but never add one — the worst of the
three states an element can be in, because reading works and so nothing looks broken.

## Decision drivers

- ADR-0189 §4 already decides the mechanism: bindings are ordinary ArchiMate
  properties in the `atlas.` namespace, holding a stable identifier and nothing
  mutable, resolved by the server at read time.
- An allowlist of key against element type is what keeps a nonsense binding from
  travelling to another tool looking official.
- A binding must not become a way to read something a sharing scope withholds.
- The drawing and the register must stay the same architecture *seen twice*, never a
  copy of one inside the other.

## Considered options

1. **Two binding keys** — `atlas.capabilityKey` on a `Capability`,
   `atlas.valueStreamKey` on a `ValueStream` — resolved against the register, plus the
   missing palette entry.
2. **One generic key**, `atlas.registerKey`, disambiguated by the element type it sits
   on.
3. **Derive the link from names**: match an ArchiMate element to a record whose name
   is equal, case-folded.
4. **Store the capability's fields on the ArchiMate element** as properties — owner,
   scope, SLAs — so the drawing is self-contained.

## Decision outcome

Chosen option: **"two binding keys"**.

Option 3 is the status quo dressed up as a feature, and it fails the first time
somebody renames either side. Worse, it fails silently and in the direction of
appearing to work: a near-match is still a match to a person reading the screen.

Option 4 is the one ADR-0189 exists to refuse. A drawing that stored the owner and the
SLAs would be a second runtime database, stale from the moment the record changed, and
it would carry into an export a version of the truth nobody maintains.

Option 2 is the tempting one, because the two keys resolve against two halves of one
register and a single key would be one row rather than two. It is refused because the
element type would then be doing two jobs: saying what the architect drew, *and*
selecting which lookup the server performs. The allowlist would still have to name
which types are valid, so nothing is saved in rules — only in constants — and the cost
is that a key on the wrong element type stops being a refusal and becomes a different,
silent lookup.

### The key is the identity, not an opaque id

Every other binding in the contract holds an opaque id because the resource's own name
is mutable. A capability record's key is not: it is the filename on disk, it is not
renameable in place, and it is what an export carries. So the rule ADR-0189 §4 states —
carry the stable identifier, resolve the mutable rest at read time — points at the key
here, and the name still comes from the server on every read.

### Both keys, each on its own element, and not on the other's

`Capability` and `ValueStream` are both strategy-layer behaviour elements binding a key
from the same register. That makes them the pair a later edit is likeliest to treat as
interchangeable, and the pair where doing so would be least visible: both keys would
still resolve, against a register that holds both. The allowlist keeps them apart, and
a test asserts each is refused on the other's element rather than only on an obviously
wrong one.

### Every caller may see a capability, and that is the register's rule

Every other map in the resolution catalog is filtered per caller. These two are not,
and it is worth saying why rather than leaving it to read as an omission.

A capability record says what the organisation must be able to do. It says nothing
about what this server runs, and its read route is open to any signed-in identity
(ADR-0305). What *is* scoped are the processes a capability names as its realisations —
and those are resolved elsewhere, through their own sharing scope, where a caller who
may not see one reads it as restricted. Nothing about them reaches this catalog.

### The binding contract's version does not move; the authoring subset's does

These are two public version numbers and this change touches them differently, which is
the sort of asymmetry that looks like an oversight unless it is stated.

`BindingContractVersion` says which keys a client may rely on finding. Adding a key
takes nothing away from that: no key's meaning moved, none was removed, and a document
carrying the new keys reads correctly under the old contract, because the extractor
already ignores what it does not recognise. Bumping it would make the number mean
"something here changed", and a version that changes when nothing a client depends on
has changed is one clients stop reading.

`SubsetVersion` says what Atlas will create and which connections the canvas refuses
mid-drag. A browser holding version 1 against a server on version 2 disagrees about a
live rule, and would refuse an arrow the server permits. That is what the number is
for, so it moves to 2.

### What the palette entry costs: one line

The relationship matrix in `subset.go` is predicates over layer and aspect rather than
a table of type pairs. `ValueStream` as strategy-layer behaviour therefore inherits
exactly the matrix `Capability` already has, and no rule is touched. A test asserts
that equivalence across every drawable relationship and every authorable partner in
both directions, because those two fields are the whole of what decides it and a wrong
one would not fail loudly — it would quietly permit a different matrix.

## What this deliberately does not do

Three surfaces are left alone, and each absence is a decision rather than an omission.

- **The mesh overlay** (ADR-0211) draws the runtime landscape: applications,
  processes, workers. Its own comment says a key absent from it binds something the
  mesh does not draw, and that this "is not absence, it is a different altitude". A
  capability is exactly that. Adding it would report every drawn capability as drift
  against a landscape that was never meant to contain one.
- **Observations.** A capability has no runtime health. An absent map is the honest
  answer — "nothing on this server observes that kind" — where an empty one would claim
  the server looked.
- **The event-log context** answers "what happened to the thing this element binds".
  It cannot answer for a capability, and now says why in its own words rather than
  falling to the generic sentence: the log records process instances, so it can be
  asked about the processes that realise a capability and not about the capability
  itself. Asking it *through* the realisations is measurement, which is its own slice.

## Consequences

- **Good:** a drawing and the register are one architecture. A `Capability` on a
  diagram resolves to the record that carries its owner, its scope and its SLAs, and a
  binding whose record was deleted reads as missing rather than as a name that quietly
  stopped matching.
- **Good:** `ValueStream` is authorable, closing a gap that had nothing to do with this
  slice and would have made `atlas.valueStreamKey` a contract with no author.
- **Neutral:** the register is unchanged. Nothing in `api/capability` knows Panorama
  exists, and the binding travels in the ArchiMate document as a standard property.
- **Bad, and accepted:** there is no reverse index. "Which diagrams bind this
  capability" is not a question the register can answer, because the binding lives in
  documents the register does not read. Answering it would mean either scanning every
  model on every read or keeping a derived index — a second copy of a fact, which is
  the thing ADR-0189 §4 refuses. A reader who wants it opens the model.
