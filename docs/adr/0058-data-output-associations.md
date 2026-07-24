# ADR-0058: Data output associations — write a value and transition a data object's state

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Patrick Blumer

## Context and problem statement

ADR-0053 made data objects first-class: a modeled `<dataObject>` is seeded under
each instance scope as a typed, event-sourced entity carrying its declared initial
**data state** (e.g. `order [received]`). But nothing writes to it yet. A data
object's value stays unset and its state never advances, so the feature is inert:
it records *that* a process carries an `order`, never *what* the order is or that
an activity moved it from `received` to `approved`.

BPMN models this with **data associations**: a `<dataOutputAssociation>` on an
activity writes the activity's output into a data object, and the target
`<dataObjectReference>` carries the data state the object is in after the write.
This is the mechanism that makes data *flow* through a process — the whole point
of ADR-0053. This ADR decides how Atlas executes the **output** direction (the
write); the input direction (reading a data object into an activity) is a stated
follow-up.

## Decision drivers

- **Make data objects live.** An activity should be able to set a data object's
  value and advance its data state, so the state history ADR-0053 records is a
  real trail (`received → validated → approved`), not a single seeded entry.
- **Reuse the FEEL and event machinery.** Value computation should be the same
  compile-once/eval-many FEEL over instance variables that script tasks (ADR-0008)
  and DMN I/O mappings (ADR-0039) already use; the write should be the same
  `DataObjectStateChanged` event ADR-0053 already defined.
- **Recovery (I4/I6).** The written value and state must be frozen into the event
  and re-applied on replay, never recomputed — exactly as a script task's result
  variable is.
- **Compile, don't interpret (I5).** Reference resolution (which data object, which
  target state) and the value expression are resolved and compiled at deploy time;
  the runtime reads integer indices and a pre-compiled expression.
- **One evaluation point.** Associations are orthogonal to element type — any
  activity can carry them — so they should be evaluated at a single shared point in
  the lifecycle, not bolted onto each behavior.

## Considered options

1. **Evaluate output associations centrally on element completion**, from a
   `<dataOutputAssociation>` whose target reference names the data object and its
   post-write data state, with an `<assignment><from>` FEEL expression as the
   value — evaluated over the instance's variables at completion.
2. **Per-behavior writes** — give each activity behavior its own data-object write
   logic. Duplicated across every element type; easy to forget one.
3. **Zeebe-style `<zeebe:output>` io-mappings targeting data objects** — reuse the
   ADR-0039 output-mapping shape instead of native associations. But io-mappings
   write process *variables*, not data objects, and carry no data-state concept,
   which is the differentiator here.

## Decision outcome

Chosen option: **option 1 — evaluate native `<dataOutputAssociation>`s centrally on
element completion.** It is BPMN-native (the Modeler draws the association as an
arrow from the activity to the data object, and the reference's `<dataState>` is
where the target state naturally lives), reuses the FEEL and event machinery, and
has a single evaluation point that covers every activity type.

### Design

**Authoring (BPMN).** A data object is declared once; a `<dataObjectReference>`
points at it and may carry a `<dataState>`; an activity's
`<dataOutputAssociation>` targets a reference (or the object directly) and carries
the value as an `<assignment><from>` FEEL expression:

```xml
<dataObject id="DataObject_order" name="order"><dataState name="received"/></dataObject>
<dataObjectReference id="Ref_approved" dataObjectRef="DataObject_order">
  <dataState name="approved"/>
</dataObjectReference>
<serviceTask id="approve">
  <dataOutputAssociation>
    <targetRef>Ref_approved</targetRef>
    <assignment><from>=decision</from></assignment>
  </dataOutputAssociation>
</serviceTask>
```

When `approve` completes, Atlas evaluates `=decision` over the instance's
variables, writes the result as `order`'s value, and sets `order`'s data state to
`approved`.

**Compiler.** `<dataObjectReference>` elements are indexed at compile time
(`refId → (dataObjectName, dataState)`). Each `<dataOutputAssociation>` compiles to
a `DataOutputAssociation{DataObject, Value, TargetState}` — the interned target
data-object name, the FEEL value expression compiled once (`nil` if the association
carries no `<from>`, a state-only transition), and the interned target data state
(`-1` when the target is the bare object with no state, meaning "keep the current
state"). The associations are grouped per node into a shared array with
`DataOutStart/DataOutCount` offsets on the node, exactly like outgoing flows and
boundary events, so lookup is allocation-free.

**Engine.** `handleElementCompleting` — the one place every activity passes through
on completion — evaluates the node's output associations after the behavior's
`OnCompleting`. For each: the value expression is evaluated over the instance's
variables (the `bindInputs` path script tasks use; a failed/absent expression
yields FEEL null, since incidents are not modeled yet), classified to a
kind/bool/text, and emitted as a `DataObjectStateChanged` event (ADR-0053) under
the instance scope with the target state (or the object's current state when the
association names no state). The value and state ride *in* the event, so
`applyToState` re-applies them on replay without re-evaluating (I4/I6); the write
lands in the same batch, before any outgoing flow activates the next element, so a
downstream reader sees it.

### Consequences

- **Positive:** Data objects become live — an activity sets their value and
  advances their data state, so ADR-0053's state history and lineage become a real
  `received → … → approved` trail. Reuses FEEL, the completion hook, and the
  existing `DataObjectStateChanged` event; recovery-correct by construction.
- **Negative / trade-offs accepted:** Only the **output** direction lands here;
  input associations (binding a data object into an activity's inputs) are a
  follow-up. The value comes from an `<assignment><from>` FEEL expression over
  instance variables rather than from a named activity data-output port — Atlas
  activities have no generic output ports, and FEEL is the engine's lingua franca.
  A failed value expression degrades to null (no incident yet), as everywhere FEEL
  is evaluated today.
- **Follow-ups / risks to watch:** Data **input** associations (read a data object
  into a FEEL scope for the activity). Multiple references to the same object in
  different states on one diagram (already supported at the reference level; the
  Modeler UX for it). Raising an incident instead of null on a failed value
  expression once incidents are modeled. The Modeler properties panel for drawing
  and configuring associations.

## Pros and cons of the options

### Option 1 — central evaluation of native associations (chosen)
- Good: BPMN-native; single evaluation point for all activity types; reuses FEEL
  and the ADR-0053 event; the target state lives where BPMN puts it (the reference).
- Bad: only the output direction; value source is a FEEL expression, not a native
  output port.

### Option 2 — per-behavior writes
- Good: local to each activity.
- Bad: duplicated logic across every element type; easy to miss one; no single
  place to reason about data writes.

### Option 3 — Zeebe `<zeebe:output>` io-mappings targeting data objects
- Good: reuses the ADR-0039 mapping shape.
- Bad: io-mappings write variables and have no data-state concept — they cannot
  express the `received → approved` transition that is the point of ADR-0053.

## Links

- extends ADR-0053 (first-class data objects — this writes the value and advances
  the data state the earlier ADR seeds, reusing its `DataObjectStateChanged` event
  and snapshot history)
- reuses ADR-0008/0015 (FEEL compile-once/eval-many) and mirrors ADR-0039 (DMN I/O
  mappings — FEEL over instance variables resolved off the hot path)
- follows the shared-array-per-node topology of ADR-0040 (boundary events) and the
  outgoing-flow grouping
