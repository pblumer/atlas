# ADR-DRAFT: Derive the information model from the processes that use it

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-09
- **Deciders:** Patrick Blumer
- **Open question:** whether a derived model should ever be written into a document, and if so how a re-derivation reconciles with what a person has since edited. This record deliberately settles only the read.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0230](0230-process-information-model.md) gave BPMN's `itemSubjectRef` something to
resolve against, and [ADR-0259](0259-data-object-lifecycle.md) did the same one field
over for the data state. Both records run in one direction: **a person models the
vocabulary, and the processes are then checked against it.**

That direction has a cost nobody in either record priced. It is a blank page. Before a
single check can say anything useful, somebody has to have drawn classes, attributes,
states and transitions that the processes already imply — and until they have, every
surface stays silent. The Modeler's class picker is empty, the data-state picker is
free text, and the Problems panel reports nothing because there is nothing to report
against.

The observation that forces this record came from a working model, not from a test.
Its author had written the five states of an identity as an «enumeration» with careful
documentation, precisely because that was the only place the states could be written
down at all; and the class diagram they had drawn — `identitaet` with six attributes —
was, in their own words, *"abgeleitet aus den tatsächlich laufenden Instanzen"*. They
had done the derivation by hand, from the runtime, and then typed the result in.

Meanwhile the engine already knows most of it:

- Every data object declares a name, and — where a modeler set one — a type.
- Every data output association names the object it writes and, when it targets a
  member, the path it writes into (`customer.name`, ADR-0060).
- Every `<dataObject>` and `<dataObjectReference>` may carry a `<dataState>`.
- The compiled graph says which writes can precede which, which is exactly the
  reachability `checkLifecycles` already computes to decide whether a move is legal.

So the question this record answers is: **can the picture be read off the processes,
instead of being typed in beside them — and what happens to the model somebody has
already written by hand?**

## Decision drivers

- **The blank page is the real barrier**, not the modelling itself. A person who can
  see the classes their processes already carry has something to correct; a person
  facing an empty canvas has to invent it.
- **Nothing derived may overwrite anything authored.** The documentation on that
  enumeration is worth more than the five strings it documents, and no generator will
  ever produce it.
- **A derivation that hides its losses is worse than none.** ADR-0232 settled this for
  the XMI import and the discipline is the same here: a lossy read that does not
  report its losses is discovered later, by a deploy warning, at the worst moment.
- **The machinery mostly exists.** `CheckApplication` already collects the states an
  application writes per class; `checkLifecycles` already computes reachability over
  the compiled graph; the object graph already derives objects and their members from
  runtime values. Each is a comparison that throws away the thing this record wants
  to keep.
- **Invariant 6, events are facts.** A derived model is a *reading*, not a record. It
  must not become a second source of truth that can disagree with the document.

## Considered options

1. **A derived view, never a document.** Read the processes, draw the result, write
   nothing.
2. **A one-time seed.** A button that creates a real information model from the
   processes; hand-kept from then on.
3. **A reconciliation.** Derive continuously and offer the differences against the
   authored model as individual, accept-or-reject changes.
4. **Provenance inside one document.** Each class, attribute and state records whether
   it was derived or authored; re-derivation refreshes the first and never touches the
   second.

## Decision outcome

Chosen option: **"A derived view, never a document"** — for now, and deliberately only
for now.

It is the option that delivers what was actually asked for — *see which classes exist
and which states they can be in, without modelling first* — at close to no risk,
because it writes nothing. It is also the option that makes the other three
**evaluable**: you cannot sensibly decide whether to seed, reconcile or track
provenance until you can look at what derivation actually produces on a real
application. Options 2–4 all rest on an assumption this one tests.

### 1. What is read, and from where

Per application, over the newest active version of each of its processes — the same
set `CheckApplication` already assembles, and for the same reason:

| Derived | Read from |
|---|---|
| A class | a data object's `itemSubjectRef`; failing that, the data object's own name |
| An attribute | the target path of every data output association writing that object |
| A nested class | a path that walks through a member (`order.lines[…]`) |
| `isCollection` | the data object's own flag |
| A state | every `<dataState>` on the object or any reference to it |
| The initial state | the state the object is created in |
| A transition | an ordered pair of writes the compiled graph says can follow one another |

### 2. What cannot be read, and is said so

This is the half that decides whether the feature is honest, and it is stated per
class rather than as a footnote:

- **The business key.** Nothing in BPMN says which attribute identifies the thing.
  This is the single fact ADR-0230 exists for — every cross-process capability rests
  on it — and derivation can never produce it. A derived class is therefore always
  keyless, and says so.
- **Attribute types.** A FEEL expression's result type is not a static fact of the
  model. Derived attributes are untyped.
- **Multiplicity** beyond the collection flag.
- **Which states are final.** "Nothing leaves it" is a statement about intent; the
  graph only shows what no process happens to do next.
- **Associations** other than the containment a nested write path implies.
- **Documentation**, which is the whole reason a person writes a model at all.

A derived picture that is mistaken for a complete one is worse than no picture. The
view names its own gaps in the same place it draws.

### 3. Where it is shown

Under **Data**, beside the authored models rather than inside them: a third reading
next to the class canvas and the object diagram, on the same `AtlasCanvas.uml`
bundle. Two drawings — the derived class diagram and, per class, the derived state
machine — using `ClassCanvas` and `StateCanvas` read-only, exactly as the run-time
overlay of ADR-0259 §4 does.

Nothing on it is editable, and that is the point rather than a limitation: it is
evidence about the processes, not a document about the business.

### 4. What is deliberately not done

- **Nothing is written.** No model is created, seeded, or updated. The one action the
  view offers is to *open* the authored model beside it.
- **No reconciliation, no diff.** Comparing the derived reading against an authored
  model is the obvious next record and is not this one: it needs a stable identity for
  a derived class across two derivations, which nothing yet provides.
- **No new persistence.** This is a read over compiled processes, so it costs a
  request and no storage — the same posture ADR-0259 §4 took, for the same reason.

### Consequences

- **Positive:** the blank page goes. An application that has never been modelled can
  be looked at. The states an identity moves through are visible without anyone
  drawing a machine — which is the specific thing that could not be seen at all.
- **Positive:** it makes the authored model checkable *by eye* against reality, which
  is a different and cheaper test than the deploy checks.
- **Negative:** two pictures of the same subject, one derived and one authored, with
  no mechanism keeping them in step. Mitigated only by the derived one being obviously
  read-only and obviously incomplete.
- **Negative:** a derived class named after a data object rather than a type will
  usually be named wrongly (`identitaet` the object versus `Identitaet` the class). The
  view must not pretend otherwise.
- **Follow-up:** whether to seed, reconcile or track provenance. That decision should
  be taken *after* this view has been pointed at real applications, and the evidence it
  produces is the input to it.

## Pros and cons of the options

### Option 1 — a derived view, never a document
- Good: writes nothing, so nothing can be lost; ships on machinery that exists; makes
  the other options evaluable instead of hypothetical.
- Bad: two pictures; the derived one cannot carry documentation, layout, or a key.

### Option 2 — a one-time seed
- Good: one document; a real head start on the blank page.
- Bad: the second run has nothing good to do. Re-deriving after the processes change is
  the merge problem again, with a person's edits in the middle of it.

### Option 3 — a reconciliation
- Good: one source of truth, kept current, and never silently overwritten.
- Bad: by far the most machinery, and it needs an identity for derived elements that
  survives a re-derivation — which is a design problem in its own right.

### Option 4 — provenance inside one document
- Good: precise about what may be refreshed.
- Bad: a rule inside the document that every reader and every writer must learn, to
  solve a problem we have not yet shown that we have.

## Links

- builds on [ADR-0230](0230-process-information-model.md) — the model this reads against
- builds on [ADR-0259](0259-data-object-lifecycle.md) — the lifecycle, and §4's read-only
  overlay, which is the shape this view copies
- reuses [ADR-0237](0237-class-canvas-on-diagram-js.md) — the canvases it draws with
- follows the loss discipline of [ADR-0232](0232-uml-model-import.md)
- rests on ADR-0060 for the write paths that become attributes
