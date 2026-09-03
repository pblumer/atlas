# ADR-0230: The process information model — UML classes above BPMN's data objects

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Patrick Blumer

## Context and problem statement

Atlas took data objects further than the engines it competes with. ADR-0053 made a
`<dataObject>` a first-class, typed, event-sourced entity rather than canvas
decoration; ADR-0058/0059 gave it write and read associations; ADR-0060 let a write
target one member of a structured value. A modeled `order` is a real thing with a
value, a BPMN data state, and a replayable history.

It is real **inside one process instance**, and nowhere else. That is the whole of
what BPMN offers, and the limit is structural, not an Atlas gap:

- A `<dataObject>` is scoped to the process definition that declares it. Its name is
  a string local to that definition. Two processes that both handle an order have two
  unrelated `order`s, and nothing anywhere states that they mean the same thing.
- An `<itemDefinition structureRef="…">` is BPMN's type slot, and the specification
  deliberately leaves the target opaque — it points *into some other schema language*.
  Atlas parses `itemSubjectRef` into `CompiledDataObject.ItemType` (an interned
  string) and does nothing with it, because there is nothing to resolve it against.
- A `<dataStoreReference>` is BPMN's one gesture at data that outlives an instance,
  and the specification says nothing about its structure, its identity, or its
  contents. Atlas does not parse it at all today.

So the questions a person actually arrives with have no answer:

1. *What data does this instance carry, and where did each value come from?* The
   values are on disk (`cfDataObject`, `cfDataObjectSnapshot`) and readable over
   `GET /api/v1/instances/{key}/data-objects`, but nothing in the console shows them,
   the record does not say which element wrote a value, and the declared type is
   dropped on the floor.
2. *What is an Order, in this landscape?* Unanswerable. There is no place to say it.
3. *Which processes touch the same order?* Unanswerable, and it is the question that
   matters most: it is what "process data modeling" means once there is more than one
   process.

BPMN's own answer to (2) and (3) has always been "that belongs to another standard",
and the OMG's other standard is UML. This record settles: **does Atlas gain a
cross-process information model, in what notation, and how does it meet the BPMN
data objects that already exist?**

It is explicitly *not* about storage. Where a datum is persisted is ADR-0036's
question (a Worker behind a data store) and is settled per store; this record is
about what a datum *is*.

## Decision drivers

- **Cross-process identity is the point.** The deliverable is that two processes can
  state they mean the same `Order`, and that an operator can ask which instances
  touched it. A per-process type declaration would not deliver it.
- **Model concepts, not tables.** The model must not commit to persistence, because
  the same business object legitimately lives in clio, in SQL, in SharePoint, or only
  in an instance's memory (ADR-0036/0203).
- **The seam already exists.** `itemSubjectRef` is parsed and unused. Filling it is a
  resolution step at deploy time, not a new attachment point.
- **Compile, don't interpret (I5).** Type resolution and structural validation happen
  once, at deploy; the runtime keeps reading integer indices.
- **Say what happened, don't infer it (I4/I6).** A data object's provenance must be a
  recorded fact frozen into its event, on the ADR-0219 pattern — a snapshot diff
  cannot recover it on a fork.
- **Nothing here enters the WAL, `applyToState`, or a processor** beyond the one
  appended field named below. The model is design-time; the engine reads a compiled
  projection of it.
- **A declared subset, honestly stated** (the ADR-0189 §2 / ADR-0211 discipline): say
  which of UML this build authors, and keep "out of subset" distinct from "UML
  forbids it".
- **Buildless and CDN-free** (ADR-0012), like every other canvas in the app.

## Considered options

### What notation models the data

1. **An entity-relationship diagram (ERD).** Familiar, and the reflex answer.
2. **A UML class diagram.**
3. **JSON Schema, authored directly.**
4. **ArchiMate, reusing Panorama** (ADR-0189) — a Business Object per type.

**Option 2 is selected.** An ERD's entire vocabulary — entities, columns, foreign
keys, normal forms — *is* storage; drawing one would state something this record
explicitly does not mean, and would push every reader toward asking which table an
`Order` lives in. A UML class diagram says what an Order is (attributes, typed
associations, multiplicities, generalization) while saying nothing about where it
lives, which is exactly the altitude wanted.

Option 3 has the runtime contract right and is unreadable as a model; it is kept, but
as a *derived projection* rather than the authoring surface. Option 4 fails on the
one thing needed: an ArchiMate Business Object has a name and no structure — it
cannot carry attributes, so it cannot type a data object. Panorama and this model
answer different questions and are related by a binding, not a merge (§6).

UML brings a second thing no other candidate does, and it resolves the open question
of *list or diagram* rather than choosing between them: UML already distinguishes the
**class diagram** (types) from the **object diagram** (instances and their links).
That distinction maps one-to-one onto Atlas's design-time/run-time split, so both
surfaces exist and neither is a compromise (§4, §5).

### How UML is offered

1. **Full UML authoring**, metamodel and all.
2. **A declared subset of the class diagram**, with everything outside it refused *as
   out of subset*, and read-only projections (JSON Schema, XMI) that report loss.
3. **A renderer theme over the ArchiMate model.** Rejected on ADR-0189 §7's grounds,
   and for the structural reason above.

**Option 2 is selected**, on the precedent this repository already set for ArchiMate.

### Where a data object's type comes from

1. **`itemSubjectRef` resolves to a class in the information model at deploy time.**
2. **A new Atlas-namespaced extension attribute** on the data object.
3. **Structural inference** from the values an instance happens to produce.

**Option 1 is selected.** The BPMN attribute exists precisely for this, is already
parsed, and keeps a model that leaves Atlas still saying what it meant. Option 3
inverts the point — a model that is discovered from runtime data cannot validate it.

## Decision outcome

### 1. Atlas gains an **information model**: a UML class-diagram subset

A design-time document owned by a **process application** (ADR-0128), inheriting its
sharing scope (ADR-0071) exactly as a Panorama model does, and held in one
`sidecar.NewStore` (per `AGENTS.md`) rather than hand-rolled read/write mechanics.

A **class** is a business object type:

| Part | Meaning |
|------|---------|
| `name`, `documentation` | what it is, in the modeler's words |
| `stereotype` | `«businessObject»` (default), `«enumeration»`, `«valueType»` |
| `attributes[]` | `name`, `type`, `multiplicity`, `documentation` |
| `identity` | which attribute(s) form the **business key** |

An attribute's `type` is a primitive (`string`, `number`, `boolean`, `date`,
`dateTime`, `duration`) or another class in the same model. Multiplicity is
`0..1 | 1 | 0..* | 1..*`.

An **association** relates two classes: a name, a role and multiplicity at each end,
and a kind — `association`, `aggregation`, `composition`, or `generalization`.

**`identity` is the part BPMN has no equivalent for, and it is load-bearing.** It is
what makes `Order#ORD-1` the same order in three processes and in a data store; every
cross-process capability in §7 rests on it, and without it the model is decoration.

The authorable subset is **one table**, served to the browser rather than duplicated
there, so the canvas refuses while dragging exactly what the server refuses on write
— the reasoning `api/panorama/subset.go` already spells out.

### 2. Three projections, derived and read-only, loss reported

Following ADR-0211: a projection is never authored, never round-tripped, and states
what it dropped.

- **JSON Schema** — the honest *runtime* contract, and what a value is validated
  against. Derived from a class, never edited.
- **UML class diagram** — the canvas, and an SVG/PNG export of it.
- **XMI 2.5.1** — export for interchange with UML tools, with loss reported.

### 3. `itemSubjectRef` resolves at deploy time

A `<dataObject itemSubjectRef="Order">` resolves against the deploying application's
information model, in the compiler, once (I5). `CompiledDataObject` keeps its
interned `ItemType` and gains the resolved class, so the runtime still reads indices.

An unresolved reference is a **Problems-panel finding** (ADR-0026), never a deploy
failure and never a runtime error: a model that names a type the application has not
modeled yet must still deploy and run, exactly as it does today.

Reading the reference is itself three steps, because BPMN gives an `<itemDefinition>`
no name of its own — a root element carries an id and nothing else, so `structureRef`
is the only slot the specification offers for the name of the type being declared. A
tool that does not use it has to invent somewhere: MID Innovator writes a bare GUID id
with `<bpanda:property name="Name" value="Incident"/>` beside it. So the type is the
`structureRef`, else a vendor property called `Name`, else the id — and, when the
reference names no `<itemDefinition>` at all, the reference itself, which is the
shorthand every hand-written model uses. Anything less reports a GUID as the declared
type of every data object in an imported model, and then reports that GUID as a class
nothing models, against a name nobody could have modeled.

Resolution is what makes three things possible that are impossible now:

- **Static data-flow validation.** *"`Approve` writes `order.amount`, but `Order` has
  no attribute `amount`."* *"`Approve` reads `order`, and no upstream element
  produces it."* ADR-0053 named this as the payoff of an item definition.
- **Field writes get a schema.** ADR-0060's `<assignment><to>customer.name</to>` is
  checked against the class — its own named follow-up.
- **Identity.** Both processes' `order` are `Order`, and `Order`'s business key says
  *which* order.

### 4. Design time reads as a **class diagram**

The information model's own canvas: classes as UML class boxes, attributes and their
types listed, associations drawn with multiplicities and role names. Where a class is
bound to a Panorama element (§6) or to a data store (§7), the box says so.

### 5. Run time reads as a **list first, an object diagram beside it**

Per instance, a **Data** tab beside Variables and Decisions, in both the live view and
the replay:

- **The list is primary**, because it is what an operator scans: one row per data
  object — name, class, data state (the `[received] → [approved]` chip), value, and
  **which element wrote it, when**.
- **The state trail** per object, folded onto the replay's log positions exactly as
  the variable changes already are. The history family (`cfDataObjectSnapshot`) has
  been recorded since ADR-0053; nothing has ever read it.
- **The object diagram** is a toggle beside the list: this instance's objects as UML
  object nodes (`order:Order`), linked by the class model's associations resolved
  through the objects' own values. Derived, read-only — the posture ADR-0211 gives
  the landscape mesh. It is meaningful only once §3 has landed, and it degrades to
  the list when an instance's objects carry no resolved class.

Lineage needs **one durable fact that is not recorded today**: `DataObjectValue` has
no `ProducerKey`. ADR-0219 added exactly this to `VariableValue` because a diff of
two snapshots attributes both branches' writes to both branches on a fork, and log
positions cannot fix it. Data objects have the identical problem and take the
identical fix — an appended `uint64` (ADR-0009's shape; a record written before it
decodes to 0), stamped in the one place `AppendDataObjectEvent` already funnels every
write through, from the `producer` the processing context already carries.

### 6. Panorama is bound, not merged

A class may be bound to an ArchiMate Business Object or Data Object in a Panorama
model, through ADR-0189's namespaced-property binding mechanism. The two models
answer different questions — Panorama says *where data lives and which components
touch it*, the information model says *what it is* — and an ArchiMate Business Object
cannot hold attributes, so a merge would lose the structure that is the whole point.
The binding is what lets a Panorama element open its type, and a class show its
landscape.

### 7. A data store is a class plus a Worker — and it is where the model pays back

`<dataStoreReference>` is parsed for the first time and declares two things: the
**class** it holds, and the **Worker** that backs it (ADR-0036/0203 — clio, a SQL
database, SharePoint). The model itself stays storage-agnostic; only the store's
binding names a Worker, which is the seam ADR-0053 already anticipated.

The store is declared **in the application's information model, beside the classes** —
not inside the class it holds, and not in the diagram that names it. Beside, because an
Order is an Order wherever it is kept: putting the binding on the class would make
storage part of the type, which is exactly the collapse §1 rejected ERD for. Not in the
diagram, because a store is the one thing in this record that is deliberately
*process-crossing* — several processes name the same `Orders` store, and a per-diagram
declaration would give each of them a private one. Per application, then, on the same
scope as the classes (ADR-0128), declared once and named by every process that reaches
it. The canvas draws it as a cylinder with a dashed **annotation** line to its class,
not an association: an association is a statement about how two things in the model
relate, and a store and its class do not relate — one *is kept in* the other.

A store may only hold a **business object with a business key**. A process reads from a
store by naming which thing it wants, and the key is the only thing in the subset that
names one; a store over a class with no identity could be filled and never read again,
so it is refused at authoring time rather than discovered at runtime. A value type has
no existence of its own to keep, and an enumeration is a closed list, not a population.

The mode is **read**. Writing through a store is refused as *out of subset*, and says
so in those words, because a write is a transaction against something outside the
engine: the engine's durability guarantee (I2) stops at its own log, and what it means
for a store write to be part of a process's atomic step is a decision of its own, not a
detail of this one.

That is the cross-process channel BPMN never specified. Process A writes
`Order#ORD-1` into the `Orders` store; process B reads it back by its business key.
And once identity is a modeled fact, the question that motivated this record has an
answer: **which instances, across which processes, touched this order** — a
data-centric index over the instances, the mirror image of the process-centric views
Atlas has today.

What a deploy can settle, it settles: a store a process names that the application does
not declare, and a declared store no Worker backs, are both **warnings** — never a
refusal — for the reason an unresolved connector reference is one (ADR-0158). A diagram
is routinely drawn before the store it names is modeled, and a store is modeled before
somebody configures the Worker behind it; those are different days' work, and neither
is a reason to reject a model.

**Deferred to its own record: the runtime read.** Declaring the store is one half; an
activity that actually reads from one is the other, and it is not a detail of this
record. Such an activity has to park on a job the way a service task does, which is
either a two-phase activation the engine does not have today, or a connector kind
(ADR-0158) that delegates to the store's Worker — a choice about the engine's
activation model, not about the vocabulary.

### 8. Delivery slices

Ordered so each is useful on its own, and so the first needs none of the modeling
surface.

1. **The per-instance Data view.** `ProducerKey` on `DataObjectValue`; the
   data-objects endpoint carries the declared item type, collection flag, producer
   and timestamp; a new history endpoint over `cfDataObjectSnapshot`; the Data tab
   with the list and the state trail. No new model, no new store.
2. **The information model store and the class canvas** — the subset table, the
   authoring surface, the JSON Schema projection.
3. **Binding.** `itemSubjectRef` resolution at deploy, the Problems-panel data-flow
   findings, field-write checking.
4. **The object diagram** on the instance.
5. **Identity, across processes** — split in two once the shape became clear.
   *5a*: the cross-instance data-centric index, grouped by class and then by business
   key. *5b*: `<dataStoreReference>` parsed, and the store declared in the application's
   information model with its class and its Worker — the declarative half. The runtime
   read follows in a record of its own (§7).

### Consequences

- **Positive.** Data becomes a modeled subject rather than a per-process side effect:
  a shared vocabulary above BPMN's per-definition scope, a real type behind
  `itemSubjectRef`, the static data-flow validation ADR-0053 promised, and — once
  identity and stores land — the cross-process question BPMN cannot express. The
  runtime half (slice 1) is nearly all read-side: the values and their history have
  been on disk since ADR-0053 and have simply never been shown. Notation is honest at
  both altitudes: a class diagram for types, an object diagram for instances, and no
  ERD implying a storage decision nobody made.
- **Negative / trade-offs accepted.**
  - **A second modeling surface**, with its own canvas, subset table and store,
    alongside BPMN, DMN, forms and Panorama. Justified by the same argument that
    justified Panorama: the alternative is that the knowledge lives nowhere.
  - **A subset, and it will be asked to grow.** No interfaces, no operations, no
    n-ary associations, no association classes, no profiles. Refusals must say *out
    of subset*, not *invalid UML*.
  - **`DataObjectValue` grows by eight bytes**, on the ADR-0219 trade-off exactly:
    a producer is present on virtually every write, so a sidecar attribution event
    would double the event count on the writing path.
  - **A model authored after the fact does not retype history.** An instance recorded
    before its class existed keeps an unresolved type, and its Data tab shows the list
    without the object diagram. The log cannot be back-filled with a fact it never
    carried.
  - **XMI is an export, not an interchange.** Round-tripping a foreign UML tool's XMI
    back into the model is out of scope and stays out until it is tested.
- **Follow-ups / risks to watch.** Instance-scoped data objects are still rooted at
  the process instance (the scoping limitation ADR-0048 and ADR-0053 both note), so
  a subprocess-scoped object waits on variable scoping. Validating a *value* against
  its class at runtime (as opposed to validating the model at deploy) is deliberately
  not decided here — it is an incident-raising decision and needs its own record.
  Retention of `cfDataObjectSnapshot` joins the shared compaction policy the other
  history families are waiting on.

## Pros and cons of the options

### Option A — an ERD
- Good: instantly familiar; multiplicity notation people already read.
- Bad: its vocabulary is storage, which is the one thing this is not about; no
  instance-level counterpart; nothing to say about behaviour or stereotypes.

### Option B — a UML class diagram (chosen)
- Good: models concepts without committing to storage; attributes, typed
  associations, multiplicity, generalization; the object diagram gives the run-time
  altitude for free; the OMG standard next door to BPMN, so the two read as one
  family.
- Bad: a large standard, so the subset must be declared and defended; XMI
  interchange is famously lossy.

### Option C — JSON Schema authored directly
- Good: it *is* the runtime contract; validation is immediate.
- Bad: unreadable as a model; no associations between types, so the cross-process
  relationships this record exists for cannot be drawn. Kept as a projection.

### Option D — ArchiMate Business Objects in Panorama
- Good: one modeling surface; the landscape already exists.
- Bad: a Business Object carries no attributes, so it cannot type a data object —
  it fails the requirement outright. Related by a binding instead (§6).

## Links

- completes ADR-0053 (first-class data objects — its named `itemDefinition` /
  schema-validation and data-store follow-ups) and ADR-0060 (field-level writes —
  the schema a member target is checked against)
- reuses ADR-0219 (variable write attribution — the same appended `ProducerKey`,
  stamped at the one funnel, for data objects)
- follows ADR-0189 §2 / ADR-0211 (a declared authorable subset served as one table;
  read-only projections that report loss) and binds to ADR-0189's model
- scopes by ADR-0128 (process applications) and ADR-0071 (sharing scopes); stores by
  the `sidecar.NewStore` discipline in `AGENTS.md`
- relates to ADR-0026 (the Problems panel — where a data-flow finding lands),
  ADR-0036/0203 (the Worker behind a data store), ADR-0012 (buildless canvas)
