# ADR-0051: First-class data objects — typed, event-sourced state, and lineage

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Patrick Blumer

## Context and problem statement

BPMN models carry **data objects** (`<dataObject>` / `<dataObjectReference>`),
**data stores** (`<dataStoreReference>`), and the **data associations**
(`<dataInputAssociation>` / `<dataOutputAssociation>`) that read and write them.
Data is the asset a process exists to move: an order, a claim, a contract. Yet
most engines — Zeebe and Camunda 8 included — treat data objects as *canvas
decoration*. At runtime everything collapses into one untyped, flat variable
bag: no declared structure, no per-datum lifecycle state, no record of which
step produced a value or from what. The Atlas Modeler can already draw a
`DataObjectReference` (it ships in bpmn-js), but the properties panel for it is
empty and the engine ignores the element entirely — deploying a model with a
data object runs as if it were not there.

Atlas is event-sourced (ADR-0001): every state transition is a durable fact, and
every record already carries a `SourcePos` causal back-pointer (data-model.md).
That is exactly the substrate needed to make data first-class — to answer *what
is this datum, what state is it in, who produced it, and from what* — which a
mutate-in-place variable bag structurally cannot. This ADR decides how Atlas
models data objects so that data, not just control flow, is a first-class,
inspectable, replayable citizen.

## Decision drivers

- **Data front and center.** A data object should be a typed, named, scoped
  entity with a declared structure and an explicit lifecycle **state** — not an
  anonymous entry in a flat map.
- **Event sourcing is the differentiator.** A data object's value *and* its data
  state should transition as durable events, so its full history and provenance
  fall out of the log the engine already keeps — no separate audit system.
- **Recovery (invariant I4).** Whatever a data object's state is, it must rebuild
  byte-identically by replaying the log; the value and state are frozen into the
  event, never recomputed on replay (invariant I6).
- **Compile, don't interpret (invariant I5).** A data object's structure/type and
  the FEEL of its data associations are validated and compiled once at deploy
  time; the runtime reads integer indices and pre-compiled expressions.
- **Hot path (invariant I1).** Data objects are runtime *data* (names, contents),
  seeded and written on the same non-hot-path intents as process variables
  (instance creation, task completion), so they do not touch token movement.
- **Reuse, don't reinvent.** The variable subsystem (ADR-0037 structured JSON,
  ADR-0048 per-step snapshots) and the io-mapping precedent (ADR-0039) already
  solve storage, JSON round-tripping, change history, and FEEL-mapped I/O. Data
  objects extend that machinery rather than duplicating it.

## Considered options

1. **Data objects as decoration only** — parse them for the diagram, give them no
   runtime meaning (the Zeebe/Camunda 8 status quo).
2. **Data objects as plain variables** — treat every data object as a process
   variable under the instance scope; ignore data state.
3. **Data objects as a distinct, typed, event-sourced entity** — a new value type
   `VTDataObject` carrying a structured value *and* a BPMN data-state, seeded from
   the compiled definition at scope creation, transitioned by events, with its
   change history retained exactly as ADR-0048 does for variables, and read/write
   contracts (data associations) compiled from FEEL as ADR-0039 does for DMN I/O.

## Decision outcome

Chosen option: **option 3 — data objects as a distinct, typed, event-sourced
entity.** It is the only option that delivers the driver "data front and center":
a declared structure, an explicit data-state, and — because every transition is a
`SourcePos`-linked event — provenance and a replayable per-datum timeline, all on
the infrastructure Atlas already has. Option 1 keeps the neglect this ADR exists
to end; option 2 discards the data-state and the typing that make a data object
more than a variable.

Because this is a large surface, it lands in slices. This ADR describes the
**target design** and pins down the **first slice** precisely; later slices are
listed as follow-ups and get their own ADR only where they change a decision here.

### Target design (the full vision)

A **data object** is a `(scope, name)` entity, like a variable, but with three
things a variable does not have:

1. **A declared type / structure (`itemDefinition`).** Compiled and validated at
   deploy time. Enables static data-flow validation in the Problems panel
   (ADR-0026): "task *Approve* reads `order` but no upstream element produces it",
   "`order` has no field `amount`". Backed at runtime by the structured-JSON value
   machinery (ADR-0037), so `order.customer.name` works in FEEL.
2. **A data-state (the BPMN `dataState`).** The same object appears on the canvas
   as `order [received]` → `order [validated]` → `order [approved]`. In Atlas each
   transition is a durable `DataObjectStateChanged` event, so the state history is
   an event-sourced, replayable fact — the audit trail is free.
3. **Explicit read/write associations.** `<dataInputAssociation>` /
   `<dataOutputAssociation>` on an activity declare exactly which data objects it
   reads and writes, each with an optional FEEL transformation, compiled once
   (the ADR-0039 mechanism, generalized beyond DMN). This yields a real
   data-flow graph, least-privilege data access, and the static checks above.

**Lineage** is the headline capability and needs no new storage: every
`DataObjectCreated` / `DataObjectStateChanged` event already carries `SourcePos`
(the record that caused it) and the activating element's key on its causal chain.
Folding that chain answers "which element produced this value, from which inputs"
— a query no flat-variable engine can serve. It surfaces as a data-lineage view
alongside the ADR-0046/0048 step-and-variable timeline.

**Data stores** (`<dataStoreReference>`) are data that outlives the instance. They
map onto the connector boundary (ADR-0036): a data store backed by the clio event
store is event-sourced data persisted to an event-sourced sink — a natural fit,
and the reason the design keeps the data-object value shape independent of where
it is persisted.

### First slice (this change)

The foundational spine that makes a modeled data object a live, typed,
event-sourced, scoped entity that survives recovery — proving the differentiator
(event-sourced data-state) end to end, model → compiler → engine → state →
replay. It deliberately excludes the read/write associations, the lineage/UI
query, and the Modeler properties panel, which are named follow-ups below.

- **model:** a new `VTDataObject` value type and two intents,
  `IntentDataObjectCreated` and `IntentDataObjectStateChanged`, appended after the
  existing values/intents so every prior numeric tag is unchanged on the log. Its
  payload `DataObjectValue{ScopeKey, Name, State, Kind, Bool, Text}` is a
  variable-shaped value (reusing `VarKind`, including `VarJSON` from ADR-0037) plus
  a `State` string — the BPMN data-state. The durable format is the same
  length-prefixed shape as `VariableValue` with one extra string, so ADR-0009 is
  unchanged.
- **compiler:** a `CompiledDataObject{Name, ItemType, IsCollection, InitialState}`
  table (interned indices) on `CompiledProcess`, a `Builder.AddDataObject`, and
  parsing of `<dataObject>` (its `name`, `isCollection`, and an optional
  `<dataState name="…"/>` child — spec-legal on an `ItemAwareElement`). A data
  object is **not** a `BpmnType`/flow node: no token flows through it, so it adds
  no behavior and does not grow the dispatch table.
- **engine:** at instance creation, right after the start variables are seeded,
  `handleProcessInstanceActivating` emits one `DataObjectCreated` event per
  compiled data object, bound to the instance scope and carrying its declared
  initial data-state (value unset for now — associations write values in a later
  slice). `applyToState`, on `DataObjectCreated`/`DataObjectStateChanged`, puts the
  object in the new `cfDataObject` family and records a `cfDataObjectSnapshot`
  history entry — the same two-write pattern ADR-0048 uses for variables, so the
  state timeline and lineage rebuild identically on replay (I4).
- **state:** two new column families, `cfDataObject` (`do:<scope>:<name>`, current
  value) and `cfDataObjectSnapshot` (`doSnap:<scope>:<ts>:<pos>`, append-only
  history), keyed with the same shapes as their variable counterparts so the
  data-object, variable, and element-step timelines fold together by log position.

### Consequences

- **Positive:** A deployed model's data objects become real: typed, scoped,
  event-sourced, with a durable data-state history and recovery. The design reuses
  the variable storage, JSON round-tripping (ADR-0037), and per-step snapshot
  mechanics (ADR-0048), and the io-mapping precedent (ADR-0039) for the coming
  associations. Lineage needs no new persistence — it reads the `SourcePos` chain
  the log already records.
- **Negative / trade-offs accepted:** Two more column families with the same
  unbounded retention as the existing history families (ADR-0017/0022/0038/0046/
  0048), to be addressed by the shared compaction policy. The first slice seeds a
  data object with no value (only its state), so its usefulness is limited until
  the associations slice lands — accepted because the value type is deliberately
  future-proofed (it already carries a `VarJSON`-capable value). Scope is the
  process-instance root, as everywhere in the engine today; subprocess-scoped data
  objects follow when scoping grows (the same limitation ADR-0048 notes).
- **Follow-ups / risks to watch:**
  - **Data associations** (`<dataInput/OutputAssociation>`): a compiled
    read/write contract with FEEL transformations, generalizing ADR-0039 beyond
    DMN — a task reads named data objects into FEEL and writes results back,
    transitioning data-state on write.
  - **Lineage view + data-objects API**: an endpoint alongside the instance
    variables/timeline endpoints, and an Operations panel that folds the
    `SourcePos` chain into "where did this value come from".
  - **Modeler properties panel** for `DataObjectReference` (fills the empty panel
    in the current UI): name, itemDefinition/structure, collection flag, data
    state — the design-time authoring surface (ADR-0025).
  - **Item definitions / schema validation** wired into the Problems panel
    (ADR-0026), and **data stores** backed by connectors (ADR-0036).
  - Shared retention/compaction for all history families.

## Pros and cons of the options

### Option 1 — decoration only
- Good: zero engine work; the diagram still renders.
- Bad: entrenches the neglect this ADR exists to end; data has no runtime meaning,
  no state, no provenance.

### Option 2 — data objects as plain variables
- Good: reuses the variable subsystem wholesale; trivial.
- Bad: throws away the data-state and the declared type — the two things that make
  a data object more than a variable; no distinct entity to hang lineage or
  associations off later without a migration.

### Option 3 — distinct, typed, event-sourced entity (chosen)
- Good: delivers typed, stateful, provenance-bearing data on the existing
  event-sourced substrate; recovery-correct; extends (not duplicates) the variable
  machinery; leaves clean seams for associations, lineage, and data stores.
- Bad: a new value type, two intents, and two column families to carry; lands over
  several slices before the full authoring/mapping experience exists.

## Links

- builds on ADR-0037 (structured JSON variables — the data object's value shape
  and JSON round-trip)
- mirrors ADR-0048 (per-step variable snapshots — the two-write current+history
  pattern, reused for data-object state history) and ADR-0046/0022/0038 (retained
  per-instance history from `applyToState`)
- generalizes ADR-0039 (DMN io-variable mappings — the compiled-FEEL read/write
  contract the data-associations follow-up adopts)
- relates to ADR-0001 (event sourcing / `SourcePos` causal chain — the basis for
  lineage), ADR-0036 (connectors — the data-store backing), ADR-0025/0026 (the
  Modeler properties and Problems panels — the authoring and validation surfaces)
