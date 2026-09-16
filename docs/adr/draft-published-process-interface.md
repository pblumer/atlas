# ADR-DRAFT: A process publishes an interface, not its model

- **Status:** Proposed
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
- the **terminal outcomes** an instance can reach.

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
(ADR-draft-peer-message-delivery-worker). Granting and revoking are auditable events
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

### Consequences

- **Positive:** a consumer binds to something small and stable; a publisher can
  refactor behind it; discovery and authorization stop being tribal knowledge. The
  descriptor is also exactly what the white box needs, so that feature becomes a
  rendering question rather than a disclosure question.
- **Negative / trade-offs accepted:** a new design-time store, a new service package
  and a grant model to administer. Derivation is only as good as the model: an entry
  point whose payload is assembled by FEEL from loosely typed variables will publish a
  shape nobody can rely on, and the descriptor will honestly say so rather than invent
  one. Publishing is also a new obligation on the publisher — an interface nobody
  maintains is worse than no interface, because it looks authoritative.
- **Follow-ups / risks to watch:** the **observe** grant and what it may reveal. A
  compatibility check between two contract versions ("is v3 a safe upgrade from v2?"),
  which is what would let a consumer bind to `latest` without holding its breath. And
  whether an interface belongs to a process or to a process application (ADR-0128) —
  the application is the better unit if a domain publishes several related processes,
  and this record does not foreclose it.

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
- authorizes ADR-draft-peer-message-delivery-worker; bound by
  ADR-draft-participant-binds-a-published-interface; rendered by ADR-draft-white-box-participant
- relates to ADR-0029 (public start links — a form for a human, not a contract)
- relates to ADR-0071 (sharing scopes), ADR-0278 (object authorization), ADR-0184 (grant audit)
- relates to ADR-0119 (deactivation) and ADR-0130 (deprecating a version)
- built as a service package per ADR-0147, on a sidecar store per ADR-0282
