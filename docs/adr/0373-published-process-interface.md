# ADR-0373: A process publishes an interface, not its model

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** whether the publisher grants access per consumer, or whether a
  central catalogue across the estate owns discovery and the grant. There is no
  cross-installation identity in the tree beyond ADR-0129 deployment targets, so the
  per-publisher grant chosen below is the only one buildable today — not necessarily
  the one an estate of twenty domains will want.
- **Question checked:** 2026-09

## Context and problem statement

For one domain to model an interaction with another, it must be able to read what the
other accepts. Today nothing in Atlas describes that. A deployed definition's message
entry points exist only inside its compiled form: message start events (ADR-0035),
message intermediate catches (ADR-0020), receive tasks (ADR-0102), event subprocesses
(ADR-0082). ADR-0226 established that start events are triggers, but that vocabulary
never left the modeler.

The closest existing thing is ADR-0029's public start link, which publishes a *form*
for a human, not a contract for a machine, and grants by possession of a URL.

Two failures follow from having nothing:

1. **The consumer guesses.** It hardcodes a message name learned from a screenshot or
   a conversation, and finds out it was wrong in production.
2. **The publisher cannot change anything.** With no stated contract, every element
   id, message name and payload field is potentially load-bearing for somebody, so
   nothing can be refactored safely.
3. **Nothing says when.** ADR-0372 settles that *accepted* means the envelope is
   durably buffered, and ADR-0370 lets it expire there in silence. Between those two
   truthful statements sits a message that was accepted, never processed, and never
   reported — the silent delivery this epic exists to end, moved one layer down.

There is also a real temptation to answer this by publishing the *process model*.
That is what the white-box idea suggests, and it is the wrong unit: a model is large,
volatile, and full of internal organisation (lane names, worker names, decision
logic) that the publisher has not agreed to disclose.

## Decision drivers

- **The published thing must be small and stable.** It is what another domain's
  deployed model binds to; if it churns, their models break.
- **Publication is an act, not a side effect of deploying.** Nothing becomes reachable
  from another domain because somebody deployed a process.
- **Seeing and sending are different rights.** Conflating them means anyone allowed
  to read a catalogue entry can also drive work into the publishing domain.
- **Derive what can be derived.** ADR-0301 already establishes that a model is read
  out of the processes rather than maintained beside them; a hand-maintained
  interface list would drift from the deployment within a week.
- **Reuse the design-time store shape.** `sidecar.NewStore` plus a service package
  (ADR-0147), not more `Server` methods.
- **A promise needs an owner and a bound.** A time commitment is worth nothing if
  nobody owns its breach, and it is dangerous if a peer can trigger an unbounded
  number of them.

## Considered options

1. **Publish the deployed BPMN**, and let the consumer read the entry points out of it.
2. **Publish a derived interface descriptor** — entry points, payloads, errors,
   outcomes — as an explicit, versioned act, with grants separate from it.
3. **Publish nothing; document out of band** (a wiki page, an OpenAPI file by hand).

## Decision outcome

Chosen option: **2 — a derived, explicitly published, versioned interface descriptor.**

### What a published interface is

A design-time record on the publishing node holding:

- a **stable id** (a slug the publisher chooses, e.g. `kreditpruefung`) and a
  **contract version**, which is the thing a consumer binds to;
- the **entry points**: each a contract-level name, the kind of element behind it
  (message start, intermediate catch, receive task, event subprocess), whether it
  *starts* an instance or *continues* one, and its correlation key expression;
- the **payload** each entry point expects, by reference to the information model
  (ADR-0230) where the publisher has one, and as a named shape where it does not;
- the **returns**: errors (ADR-0089), escalations (ADR-0125) and reply messages the
  publisher promises may come back, which is what a consumer needs in order to model
  a boundary event rather than discover the failure mode in production;
- the **terminal outcomes** an instance can reach;
- optionally, a **commitment** per entry point — by when an accepted message will be
  correlated, and to whom that is promised (below).

The entry points and returns are **derived** from the deployed definition, the way
ADR-0301 derives its model. The publisher's act is to *publish* a derived contract
under an id and a version — not to type it out.

### The entry point name is the contract; the element id is not

An entry point carries a name the publisher assigns, and the descriptor maps it to
the element behind it. Renaming or moving the element changes the mapping and not the
contract. This is the single property that makes a consumer's binding survive the
publisher's refactoring, and it is why the descriptor exists at all rather than the
consumer reading element ids out of shared XML.

### Two grants, deliberately separate

- **view** — may read this descriptor. Enables modelling, and the white box.
- **send** — may deliver to this interface's entry points. Enables runtime.

A third, **observe** (may see what became of the message I sent), is named here and
deliberately **not** decided: it is the one that leaks the publisher's runtime state
across a domain boundary, and it deserves its own record rather than arriving as a
convenience on this one.

Grants are checked on `GET /api/v1/published-interfaces` (which lists only what the
caller may view) and on `POST /api/v1/peer/messages`
(ADR-0372). Granting and revoking are auditable events
in the sense ADR-0184 already established for rights.

`public` remains available and means "viewable without a grant". It never implies
**send**.

### Versioning, deprecation and the consumer's binding

A contract version is immutable once published. Changing an entry point's name,
payload or correlation key means a new contract version. The publisher may mark a
version **deprecated**, reusing the vocabulary ADR-0130 already has for process
versions, and a consumer bound to a deprecated version is told at deploy rather than
at 3am. Deactivating the underlying definition (ADR-0119) makes the interface
unavailable without deleting the contract, so the consumer's binding stays legible.

### The commitment: what an entry point promises, and by when

An entry point may state a **commitment**: a duration within which an accepted message
will be correlated, and a scope — `internal` for an operational level agreement between
domains of one organisation, `external` for a service level agreement toward a party
outside it.

**One mechanism, two words.** The engine cannot tell internal from external: that
depends on who owns the consuming domain, which is an organisational fact no runtime
holds. The scope is therefore a label carried for reporting and escalation policy, and
never a second code path. Building an OLA mechanism beside an SLA mechanism is the
mistake this paragraph exists to prevent.

**Which clock — the load-bearing choice.** Three spans are measurable, and they are
three different promises:

| span | who can measure it | who can keep it |
|---|---|---|
| `issuedAt` → acceptance | the sender | the network and the peer's availability |
| acceptance → **correlation** | the receiver | the receiving engine, fully |
| acceptance → business completion | the receiver | nobody — it waits on human work |

A commitment covers **acceptance → correlation**, and nothing else. It is the only span
the promising engine controls end to end, and it is exactly the gap named in the context
above: ADR-0372's *accepted* means durably buffered, and without a commitment nothing
ever says the buffer should have been drained by now.

The third span is what a business reader usually means by an SLA, and no interface can
honestly promise it — it belongs to the process and is its own commitment, not decided
here. The first is the sender's to observe and needs no promise from the publisher.

**A breach opens an incident in the promising domain.** The receiver made the promise,
so the receiver owns the incident (ADR-0061). A consumer never receives an incident in
its own domain from another domain's breach; it learns that something went wrong the way
it must anyway — the reply it waits for does not arrive and its own boundary timer fires.
That keeps every incident list a list about its owner's own promises.

**And it is bounded, because it is remotely triggerable.** Without a bound, a peer that
sends messages which cannot be correlated opens incidents in this domain at will — a
denial of service against the operations view, reachable by anyone holding a **send**
grant. Breaches are therefore rate-limited and aggregated per interface and per peer
under ADR-0337, and the **send** grant is what a publisher revokes when one peer is the
cause. This bound is part of the decision, not a hardening step afterwards.

**Who measures, and what happens when the measurer is down.** The receiver measures — so
when the receiver is down nobody does, which is precisely when a breach is most likely.
Two rules follow, both cheap now and expensive to retrofit:

- the buffered envelope already carries `expiresAt` durably (ADR-0370), so a breach that
  occurred during an outage is reconstructable on recovery and is opened then rather
  than lost;
- a commitment whose window passed while the node could not observe it is recorded as
  **unmeasured**, never as met. A register of promises that counts what it could not see
  as kept is worse than no register, because "nothing was breached" and "nobody was
  watching" then read alike — the rule ADR-0189 already states for peer observations.

**Stating no commitment is the default, and it changes nothing.** An entry point without
one makes no promise, and ADR-0370's silent expiry stands exactly as that record decided.
A commitment is opt-in and per entry point; stating one changes ADR-0370's reading for
that entry point alone, where expiry becomes a breach rather than an ordinary outcome.
ADR-0370 is `Accepted`, so that reading needs an amendment **on** that record when this
one is accepted. It is named here rather than made here.

**Problem management is named and deferred.** Breaches aggregate, and a flood of them is
a symptom of one cause rather than many — which is what ITIL calls a Problem. That it has
no BPMN notation is not a gap: an Incident is already a record type in the operations
model rather than a notation, and a Problem would be an aggregation over incidents with
its own lifecycle. Worth stating even out of scope: the Atlas-shaped version is that the
*handling* of a Problem is itself a process, because Atlas runs processes. It gets its
own record.

### Consequences

- **Positive:** a consumer binds to something small and stable; a publisher can
  refactor behind it; discovery and authorization stop being tribal knowledge. The
  descriptor is also exactly what the white box needs, so that feature becomes a
  rendering question rather than a disclosure question. A commitment closes the last
  silent path in the transport half: a message that is accepted and then never
  processed now has a deadline, an owner and a bound.
- **Negative / trade-offs accepted:** a new design-time store, a new service package
  and a grant model to administer. Derivation is only as good as the model: an entry
  point whose payload is assembled by FEEL from loosely typed variables will publish a
  shape nobody can rely on, and the descriptor will honestly say so rather than invent
  one. Publishing is also a new obligation on the publisher — an interface nobody
  maintains is worse than no interface, because it looks authoritative. A commitment sharpens that
  obligation into a measurable one: a publisher who states a duration it cannot hold
  manufactures incidents for its own domain, and the honest first move is to state
  none. The **unmeasured** outcome is also a third state operators have to learn,
  beside met and breached.
- **Follow-ups / risks to watch:** the **observe** grant and what it may reveal. A
  compatibility check between two contract versions ("is v3 a safe upgrade from v2?"),
  which is what would let a consumer bind to `latest` without holding its breath. And
  whether an interface belongs to a process or to a process application (ADR-0128) —
  the application is the better unit if a domain publishes several related processes,
  and this record does not foreclose it. The **Problem** record named above, and the amendment
  ADR-0370 needs once this record is accepted. Whether a consumer may see the
  publisher's breach record at all is the deferred **observe** grant in another guise,
  and is decided with it rather than here.

## Pros and cons of the options

### Option 1 — publish the deployed BPMN
- Good: nothing to build; it is already the complete truth.
- Bad: discloses internal organisation the publisher never agreed to; makes every
  element id load-bearing; gives the consumer no version to bind to other than the
  whole model; and is far too large to be the thing another model references.

### Option 2 — derived, published, versioned descriptor (chosen)
- Good: small, stable, versionable, grantable; derived so it cannot drift from the
  deployment; renames behind it are free.
- Bad: a new store, a new service and a grant model; derivation cannot invent a
  payload shape the model does not have.

### Option 3 — document out of band
- Good: zero engine work, and it is what most estates do.
- Bad: it is not checkable at deploy, not grantable, and wrong within a month. It also
  cannot feed a white box, so it forecloses the feature this epic is for.

## Links

- derived the way ADR-0301 derives a model from the processes
- describes entry points from ADR-0020, ADR-0035, ADR-0102, ADR-0082, ADR-0226
- describes returns from ADR-0089 (errors) and ADR-0125 (escalations)
- payload shapes reference ADR-0230 (process information model)
- authorizes ADR-0372; bound by
  ADR-0371; rendered by ADR-0374
- a commitment's breach is an incident per ADR-0061, bounded per ADR-0337, and changes
  the reading of ADR-0370's silent expiry for the entry points that state one
- takes its "say what could not be observed" rule from ADR-0189
- relates to ADR-0029 (public start links — a form for a human, not a contract)
- relates to ADR-0071 (sharing scopes), ADR-0278 (object authorization), ADR-0184 (grant audit)
- relates to ADR-0119 (deactivation) and ADR-0130 (deprecating a version)
- built as a service package per ADR-0147, on a sidecar store per ADR-0282
