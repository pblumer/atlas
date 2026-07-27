# ADR-0059: Data input associations — read a data object into an activity

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Patrick Blumer

## Context and problem statement

ADR-0058 executed the **output** half of BPMN data associations: a
`<dataOutputAssociation>` on a completing activity writes a value into a data
object and advances its data state. The **input** half is still missing. A
`<dataInputAssociation>` feeds a data object *into* an activity — it is how a task
or gateway reads the `order` a previous step produced. Without it a data object
can be written but never read, so data flows one way only.

Atlas activities read their inputs from **process variables** through FEEL (a
script task evaluates FEEL over the instance's variables, ADR-0008; a gateway
routes on a FEEL condition). Data objects live in their own store (ADR-0053),
which FEEL does not read. So "let an activity read a data object" means: bind the
data object's value somewhere the activity's FEEL can see it. This ADR decides
how.

## Decision drivers

- **Complete the read/write picture.** Data output associations (ADR-0058) write
  data objects; input associations must let a downstream activity read them, so
  data flows through the process both ways.
- **Reuse how activities already read data.** Atlas activities read process
  variables via FEEL; an input association should land the data object's value in
  a variable the activity reads, not invent a second read path.
- **Symmetry with ADR-0058.** Same compiled shape (a per-node association table
  with shared-array offsets), same FEEL-over-instance evaluation, same recovery
  story (the read value is frozen into the event, re-applied on replay).
- **Recovery (I4/I6) and compile-don't-interpret (I5).** Reference resolution and
  the transform expression compile at deploy time; the value written is frozen
  into a `VariableCreated` event and re-applied on replay, never recomputed.

## Considered options

1. **Bind at activation into a process variable.** At activity activation, read
   the source data object, optionally transform it with the association's
   `<assignment><from>` FEEL (with the object bound into the FEEL scope under its
   name), and write the result into the target process variable the activity
   reads. Evaluated before the activity's own behavior runs, so its FEEL sees it.
2. **Make FEEL read data objects directly.** Extend the FEEL scope so every
   expression can reference a data object by name without an explicit association.
   Implicit and convenient, but it erases the association's explicit read contract
   (the point of BPMN data associations) and couples FEEL to the data-object store.
3. **A distinct data-input value type the behavior reads.** Give each activity a
   typed input port. Atlas has no generic activity input ports; this is a larger
   model change for no gain over option 1.

## Decision outcome

Chosen option: **option 1 — bind at activation into a process variable.** It is
the mirror of ADR-0058, reuses the way activities already read data (process
variables through FEEL), and keeps the read explicit and compiled. The data object
is bound into the FEEL scope under its own name for the transform, so
`<from>=order.amount` or a bare `<from>=order` works; with no `<assignment>` the
target variable simply gets the object's value.

### Design

**Authoring (BPMN).** A `<dataInputAssociation>` on an activity names the data
object it reads (`sourceRef`, a `<dataObject>` or a `<dataObjectReference>` to
one) and the target it binds into (`targetRef`, the process-variable name the
activity reads), with an optional `<assignment><from>` FEEL transform:

```xml
<scriptTask id="total">
  <dataInputAssociation>
    <sourceRef>DataObject_order</sourceRef>
    <targetRef>orderAmount</targetRef>
    <assignment><from>=order.amount</from></assignment>
  </dataInputAssociation>
  <extensionElements><zeebe:script expression="=orderAmount * 2" resultVariable="doubled"/></extensionElements>
</scriptTask>
```

At `total`'s activation, Atlas reads data object `order`, evaluates `=order.amount`
with `order` bound into the FEEL scope, writes the result into the `orderAmount`
variable — and the script task then reads `orderAmount`.

**Compiler.** `<dataInputAssociation>` compiles to a
`DataInputAssociation{DataObject, Variable, Value}`: the interned source
data-object name (resolved from `sourceRef` through the same
reference-resolution as ADR-0058, the data state ignored on a read), the interned
target variable name (`targetRef`), and the optional `<assignment><from>` FEEL
compiled once (`nil` copies the object's value verbatim). Associations are grouped
per node into a shared array with `DataInStart/DataInCount` offsets on the node,
exactly like the output associations and outgoing flows.

**Engine.** `handleElementActivating` evaluates a node's input associations
**before** the activity's own `OnActivated` runs, so the activity's FEEL sees the
bound variables. For each: it reads the source data object; if the association has
a `<from>` expression it evaluates it over the instance's variables plus the source
object bound under its name (a failed eval yields null, as everywhere FEEL runs
today); otherwise it takes the object's value directly. The result is written into
the target variable as a `VariableCreated` event — so the value is frozen and
replay re-applies it without re-reading the object or re-evaluating (I4/I6).

### Consequences

- **Positive:** Completes data associations both ways — an activity can now read a
  data object a prior step wrote. Reuses the variable read path, the ADR-0058
  compiled shape and reference resolution, and the FEEL machinery; recovery-correct
  by construction. The read is explicit and compiled, not an implicit global scope.
- **Negative / trade-offs accepted:** The data object is surfaced to the activity
  *as a process variable*, not as a first-class input port — Atlas has no generic
  input ports, and variables are how activities read. The binding happens at
  activation and writes a variable, so the value is a snapshot at activation time
  (a later write to the object by another token is not re-read). A failed transform
  degrades to null (no incidents yet), as with output associations.
- **Follow-ups / risks to watch:** Input mappings that target an activity-local
  scope rather than the instance variables, once variable scoping grows beyond the
  instance root (the limitation ADR-0048 also notes). Incidents on a failed
  transform once incidents are modeled. The Modeler UX for drawing input
  associations.

## Pros and cons of the options

### Option 1 — bind at activation into a process variable (chosen)
- Good: mirror of ADR-0058; reuses the variable read path and FEEL; explicit,
  compiled read contract; recovery-correct.
- Bad: the object is read as a variable snapshot at activation, not a live port.

### Option 2 — make FEEL read data objects directly
- Good: no association needed; convenient.
- Bad: erases the explicit read contract that is the point of data associations;
  couples FEEL evaluation to the data-object store; no snapshot semantics.

### Option 3 — a distinct data-input value type per activity
- Good: a true typed input port.
- Bad: Atlas has no generic activity input ports; a large model change for no gain
  over binding into a variable.

## Links

- mirrors ADR-0058 (data output associations — same compiled per-node table,
  reference resolution, FEEL-over-instance evaluation, and recovery story, in the
  read direction)
- builds on ADR-0053 (first-class data objects — the object this reads) and reuses
  ADR-0008/0015 (FEEL) and the variable read path (ADR-0037)
