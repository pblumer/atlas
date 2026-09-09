# ADR-0259: The data object lifecycle — what the BPMN data state resolves against

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0230](0230-process-information-model.md) found an opaque slot in BPMN and filled
it. A `<dataObject>`'s `itemSubjectRef` points at a type the specification
deliberately leaves undefined, so two processes that both handle an order shared a
five-letter string and nothing else; the information model gave that string something
to resolve against, and three deploy checks followed from the resolution.

BPMN has a second slot of exactly that shape, one field over, and it is still open.

A `<dataObjectReference>` carries a **data state** — the `[received]`, `[approved]`,
`[shipped]` in square brackets under the box. The specification says it is "an
optional reference to a `DataState`" and says nothing whatever about what states
exist, which of them an object may start in, or which may follow which. It is a free
string, and it is the field that carries the one thing a class diagram cannot say:
not what an order *is*, but where in its life it has got to.

Atlas already reads that field end to end, and that is what makes this worth doing
now rather than as a green-field feature:

- The compiler parses it (`compiler/parse.go`, `xmlDataState`), interns it, and puts
  it on the compiled model twice — `CompiledDataObject.InitialState` for where an
  object starts, and `DataOutputAssociation.TargetState` for where a write moves it.
- The engine advances it. `applyDataOutputAssociations` in `engine/behavior.go`
  emits a `DataObjectStateChanged` event carrying the new state, and — the part that
  matters here — the state rides *in* the event, so replay re-applies it without
  re-deriving it (I4/I6).
- The Operations replay shows it. Every data object's **state trail** is on disk:
  each durable write, the state it moved the object into, and which element made it
  ([ADR-0219](0219-variable-write-attribution.md)'s attribution, applied to data
  objects by ADR-0230 slice 1).
- The object diagram renders it: `ObjectNode.State` in `api/infomodel/objectgraph.go`
  is the `[approved]` chip on the box.

So Atlas parses the state, compiles it, persists every transition of it with
attribution, and draws it — **and nothing anywhere declares what the legal states
are.** `order [approved]` and `order [Approved]` are two unrelated states. A typo in
one output association invents a fourth state in a three-state process and no test,
no deploy and no panel says a word. A modeler who wants to know which states an order
can be in reads every output association in every process of the application and
takes notes.

That is the same gap ADR-0230 closed for the type slot, and it is open for the same
reason: there is no vocabulary, so there is nothing to check against.

The question this record answers: **does the information model gain a lifecycle, what
does it look like, and what is allowed to follow from it?**

## Decision drivers

- **A notation earns its place by pairing with execution semantics.** That is the
  test ADR-0230 passed and the reason its UML subset is a subset rather than UML: the
  class diagram is in Atlas because `itemSubjectRef` resolves against it, and the
  object diagram is in Atlas because it is that model's run-time twin. A lifecycle
  earns its place the same way or not at all.
- **The subset is the contract, and the server owns it.** Whatever a canvas draws,
  what gets stored is what `Validate` accepts, and the rules reach the browser by
  being served ([ADR-0237](0237-class-canvas-on-diagram-js.md) §2) rather than by
  being restated in it.
- **Deploy time, never runtime.** Whether a transition is declared is knowable from
  the model and the vocabulary, so it is decided once, at deploy, and the engine goes
  on reading integer indices. That is invariant I5
  ([ADR-0004](0004-compile-bpmn-to-indexed-graph.md)) as it applies here, and it is
  how `CheckDataFlow` already works.
- **No new events, and no new state.** The run-time half of this must fall out of
  what is already on disk. A lifecycle that required a new record type would be
  paying for a picture with a migration.
- **Warnings, not refusals.** ADR-0230 slice 3 settled this: a model is routinely
  drawn before the vocabulary it names exists, so a finding informs a deploy and
  never blocks it.
- **Buildless** ([ADR-0012](0012-web-ui-app-shell.md)): whatever the browser draws
  ships pre-built in `api/web/vendor/`, never compiled at runtime.

## Considered options

### What declares the states

1. **Nothing — leave the data state free-form.** The status quo. It costs nothing and
   it is what produced the problem statement. Worth naming because it is defensible
   for an installation that never writes a data state at all, which is why everything
   below degrades to silence when no lifecycle is declared.
2. **A flat set of legal states per class.** Cheap, and it catches the typo — the
   single most common defect. It cannot say that an order may not go from `draft`
   straight to `shipped`, which is most of what a person means when they say a thing
   has a lifecycle, and it produces a list rather than a diagram.
3. **A UML state machine per class, in the information model (chosen).** States and
   transitions, owned by the class the way its attributes are. Catches the typo, says
   what may follow what, and is drawable — which matters because the reason to put
   this in the information model rather than in a config file is that a lifecycle is
   something people argue about in front of a picture.
4. **An executable state machine — a second engine.** Model the object's life and let
   Atlas *run* it. Rejected outright, and not on effort: Atlas executes BPMN, the
   token is the unit of execution, and a second thing that advances on its own would
   need its own compiler, its own processor and its own place in the batch cycle. A
   state machine here constrains data that BPMN moves; it does not move anything
   itself.

### Where the lifecycle lives

**On the class**, not in a document of its own and not on the data object.

Not on the data object, because that is the scope BPMN already has and the scope that
does not work: a `<dataObject>` belongs to one process definition, and an order's life
crosses three of them. Putting the lifecycle there would reproduce the exact problem
ADR-0230 exists to solve.

Not in a separate document, because a lifecycle is not a separate subject. It is a
fact about a business object in the same way its business key is — and the business
key is the precedent, since it too is a fact BPMN has no field for and it lives on the
class.

Only a **«businessObject»** may carry one. A «valueType» has no identity of its own,
so it has no life to have a stage in; an «enumeration» is not an object at all. That
falls out of the stereotype rules rather than being a new rule.

### How the browser draws it

The question that prompted this record was whether Atlas should build a general
`uml-js` library, on the model of bpmn-js, dmn-js and form-js. It should not, and the
reasons are worth writing down once because they will come up again for the next
notation.

`AtlasCanvas` already is the thing such a library would be — `api/web/vendor/canvas/`,
one diagram-js, one renderer per notation, namespaced. What separates it from bpmn-js
is not maturity but contract: bpmn-js round-trips a standard interchange document and
owns its own rules, while this canvas draws Atlas JSON and asks
`GET /api/v1/infomodel/subset` what is allowed. Extracting it would mean either
shipping a copy of the matrix the server owns — the duplication ADR-0237 §2 refused —
or shipping something that only works against an Atlas server, which is a client and
not a toolkit. There is also no second consumer, the repository is AGPL-3.0 (which
makes a published browser library largely unadoptable), the buildless rule means the
bundle would be committed here either way, and `TRADEMARKS.md` disclaims exactly the
affiliation that the `*-js` naming would imply.

So the state machine is **a third notation inside `AtlasCanvas.uml`**, beside the
class diagram, sharing the library and nothing else — the arrangement ADR-0237
arrived at for the class canvas and ADR-0189's before it.

## Decision outcome

**The information model gains a lifecycle: a UML state machine owned by a
«businessObject» class, against which a BPMN data state resolves at deploy the way
`itemSubjectRef` resolves against a class.**

### 1. The document

A class may carry a lifecycle: a set of named states, one of them initial, any number
of them final, and a set of transitions between them. A transition may be named; the
name is documentation, because what *causes* the transition is the BPMN element that
writes it and that fact lives in the process, not here.

The document stays what it is — stored in its native shape and projected to its
notations, never authored as XMI. Validation runs whole-model on every write as it
does today, so a model on disk is one the subset accepts: a lifecycle with no initial
state, a transition naming a state the lifecycle does not declare, or a duplicate
state name is refused at the point of writing, not discovered at deploy.

A class with **no** lifecycle is the normal case and stays silent everywhere below.
This is opt-in per class, and an application that declares none behaves exactly as it
does today.

### 2. The subset grows, and the canvas reads it

The states, whether a state may be initial or final, and what may connect to what
join the table `GET /api/v1/infomodel/subset` already serves, beside the stereotypes
and the association kinds. The canvas refuses mid-drag what the server refuses on
write, in the server's own words, and distinguishes *out of subset* from *the notation
says no* — the distinction ADR-0230 introduced, which is what lets a refusal say
"UML allows this, this build does not" rather than pretending the standard forbids it.

### 3. Three checks, at the seam that already exists

`CheckDataFlow` in `api/infomodel/dataflow.go` is the whole integration surface. It
already walks a `CompiledProcess` against a `Vocabulary` and returns
`[]compiler.Problem`, at deploy and on the Problems panel's dry run and nowhere else.
Three checks join the four that are there:

- **`data.unknown-state`** — an output association moves an object into a state its
  class's lifecycle does not declare. This is the typo check, and it is the one that
  will find something on the first application it runs against.
- **`data.illegal-transition`** — a write moves an object into a state that no
  declared transition reaches from any state the object can be in at that point.
- **`data.unreachable-state`** — a declared state that no process in the application
  writes. The lifecycle says an order can be `cancelled` and nothing ever cancels one:
  either a process is missing or the model is aspirational, and both are worth saying.

All three are **warnings**. None refuses a deploy.

The second one needs care and this record would rather name the limit than discover
it. Knowing which state an object *can* be in at an element means reachability over
the compiled graph, which is exactly what `checkReadOrder` already does for
"reads `order`, and nothing upstream produces it" — and it is deliberately
conservative there for good reasons that apply unchanged here. A loop whose writer
precedes its reader is not flagged; a fork where two branches write different states
and join is not flagged. **Conservative means silent, not wrong**: the check reports a
transition only when every path into the element agrees the object is in a state the
transition cannot start from. A cross-process transition — an order left `approved` by
one process and picked up by another — is out of reach of a per-process compiled
graph entirely, and is stated as such rather than guessed at.

### 4. The run-time twin costs nothing, which is the point

The state trail is already on disk, with attribution, for every data object of every
instance. So the Operations view gains the lifecycle **as an overlay**: the declared
state machine, with the states this instance has actually been through marked, the
transition it is sitting on highlighted, and each edge carrying the element that made
it. No new event, no new record type, no migration, and nothing added to
`applyToState` — which is the one part of the engine this record must not touch.

This is the same relationship the object diagram has to the class diagram, and it is
the reason UML was the right notation in the first place: the standard already draws
the type and the instance as two diagrams, which is Atlas's design-time/run-time line.

### 5. What is deliberately not done

- **Nothing executes.** No element waits on a state, no transition fires by itself,
  and the engine is not aware that a lifecycle exists. A deploy resolves states the
  way it resolves types; after that the compiled model is what it was.
- **A deploy is never refused.** Consistent with ADR-0230 slice 3, and for its reason.
- **No lifecycle import.** UML state machines are expressible in XMI 2.5.1 and
  [ADR-0232](0232-uml-model-import.md)'s reader could grow to read them. It should not
  do so in the same change: the import's guarantee is that it states every loss
  element by element, and that discipline is worth more than the feature is. Named as
  a follow-up.
- **No guard expressions on transitions.** A FEEL guard would be a second place where
  a condition decides what happens, and the first place is the BPMN sequence flow.
  If it ever earns its place it needs its own record.

## Consequences

- **Positive:** the second opaque slot in BPMN's data model resolves against something
  real; the typo class of defect becomes visible at deploy; an application gains a
  drawn, arguable answer to "what states can an order be in", which today exists only
  as a habit distributed across output associations; and the run-time view of it is
  free.
- **Positive:** the integration is three functions at an existing seam plus a renderer
  in an existing bundle. No engine change, no new event, no hot-path change, no
  `applyToState` change — the invariant surface of this record is empty.
- **Negative / trade-offs accepted:** the information model document grows a second
  dimension, and the class canvas grows a second thing a class can be opened to. The
  illegal-transition check is conservative and will stay quiet in cases a person would
  catch by eye — that is the same trade `checkReadOrder` makes, and the alternative is
  a check that cries wolf on every loop. Cross-process transitions are outside what a
  per-process compiled graph can see at all.
- **Negative:** a third notation is a third thing to keep looking like the other two.
  ADR-0237 is the evidence that this is a real cost and that the substrate is what
  pays it.
- **Follow-ups / risks to watch:** the **object diagram is still hand-rolled SVG** —
  `renderObjectDiagram` in `api/web/editor.js` builds SVG strings with its own layout
  and has no zoom, pan or selection. That is precisely the complaint ADR-0237 made
  about the class canvas, one altitude down and still open, and it should move onto
  `AtlasCanvas.uml` before a third notation is added rather than after. Reading UML
  state machines from XMI is the second follow-up.
- **Risk to watch:** the temptation to let a lifecycle *do* something — to make an
  element wait for a state, or a transition fire a message. Every one of those is a
  second execution model wearing a diagram, and each needs its own record and a hard
  look at whether BPMN already says it.

## Links

- [ADR-0230](0230-process-information-model.md) — the information model this extends,
  and the record whose shape this one copies
- [ADR-0053](0053-first-class-data-objects.md) — data objects, their state history and
  their lineage
- [ADR-0058](0058-data-output-associations.md) — the write that advances the data
  state, and the event it rides in
- [ADR-0237](0237-class-canvas-on-diagram-js.md) — the canvas this notation joins, and
  why the rules are served rather than restated
- [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the vendoring
  pattern, and the first Atlas-owned notation on diagram-js
- [ADR-0232](0232-uml-model-import.md) — the reader that could one day bring a
  lifecycle in, and the loss-reporting discipline it would have to keep
- [ADR-0219](0219-variable-write-attribution.md) — the attribution that makes the
  run-time overlay able to say which element made a transition
- [ADR-0012](0012-web-ui-app-shell.md) — buildless web assets
