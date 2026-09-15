# ADR-DRAFT: Where a business object is used

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Patrick Blumer
- **Open question:** whether a Modeler draft should be read alongside the deployed
  processes. It would make the answer complete at the moment somebody is editing, which is
  exactly when they ask it — and it would make the answer depend on whether an unfinished
  document compiles. This record reads only what is deployed and says so on the page.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0230](0230-process-information-model.md) gave BPMN's opaque `itemSubjectRef` a type
to resolve against, and every reading built since runs from the process outwards: the
data-flow checks read one process against the vocabulary, the derived model
([ADR-0301](0301-derive-the-model-from-the-processes.md)) reads one application's
processes, the difference ([ADR-0310](0310-read-the-difference-between-what-is-built-and-what-is-planned.md))
reads those against what was authored, the object graph reads one instance.

The vocabulary itself has had no reading at all. Two consequences, and both of them are
ordinary working situations rather than edge cases:

1. **Nothing says what a change would break.** Somebody renaming `Order.total`, retiring
   an enumeration literal, or dropping a state can see what an Order *is* — the canvas
   draws it — and nothing anywhere says which processes write that member, which elements
   move the object into that state, or which class is typed with that enumeration. The
   facts exist: a data object's declared type, an output association's target path and
   target state, an input association's variable, a store's class. They have simply never
   been read from the class's side.

2. **The estate's classes are never seen together.** Both existing views are *per model*:
   the canvas draws one document, the library lists the documents. So two applications
   that both model an `Order` — the precise failure ADR-0230 exists to prevent, one level
   up — look exactly like two applications each of which models an Order once.

The question this record answers is what the reading is, what counts as a use, and what
the reading is allowed to see.

## Decision drivers

- **A change needs to know its blast radius.** The reading is not a report; it is what
  somebody consults before editing a class, so it has to be precise enough to act on:
  which process, which element, which member, which state.
- **An enumeration must not read as unused.** Most enumerations are declared by no data
  object at all. A reading that counted only processes would report the vocabulary's most
  shared elements as dead, and somebody would eventually act on that.
- **Say what was not looked at.** "Used by nothing" is the sentence people act on
  destructively. If the reading only sees deployed processes, the page has to say so where
  it makes the claim — the same discipline the derived model applies to its gaps.
- **No new state.** Atlas already holds two statements about the vocabulary and has a
  record about not merging them. A stored usage index would be a third, and a stale one.
- **One list, one table.** The console has a shared sort/filter table
  ([ADR-0286](0286-a-list-carries-its-own-search.md)); a catalogue that invents its own
  is a second thing to learn for no gain.

## Considered options

1. **Nothing — read the derived model instead.** It already says which members and states
   the processes reach.
2. **A stored usage index**, maintained as processes deploy and models are written.
3. **A computed reading, per class**, over the stored models and the compiled processes —
   a catalogue across applications, and a where-used page per class.

## Decision outcome

Chosen option: **3, a computed reading**, served as two routes:

- `GET /api/v1/infomodel/classes` — every class of every model the caller may view, with
  the counts of where each is used. One list across applications by construction, since
  that is the half a per-model view cannot show.
- `GET /api/v1/infomodel/models/{id}/usage?class=Order` — one class, and every place it is
  used. Addressed within its model exactly as the JSON Schema projection is, because a
  class name is unique within a model and is the string every process writes.

Both are MCP tools as well (`atlas_class_catalog`, `atlas_class_usage`), and not as an
afterthought: an agent proposing a rename is exactly the caller that cannot see what it
would break, and the repository's own drift guard treats a route with no tool and no
recorded reason as a defect.

Four decisions inside that:

**A use is declared, not inferred.** A process uses a class when a data object's
`itemSubjectRef` names it — and then when an activity reads that object, writes it or a
member of it, moves its state, or when the process names a store the model says holds that
class. An *untyped* data object whose name happens to match a class name is not a use. The
derived model reads a name that way deliberately and states the guess as a gap
(ADR-0301 §2); doing it silently in a list somebody is about to act on would put an
inference where a fact is expected.

**The model's own uses are read beside the processes'.** An attribute typed with the
class, an association with it at one end, a lifecycle taking its states from it
([ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)), a store holding
it. They are listed apart from the process uses rather than added to them, because a
reader acts on the two differently — one is "a process will break", the other is "the
vocabulary will not resolve" — and because for an «enumeration» the model's uses are
normally the only ones there are.

**Only deployed processes are read** — the deployed, active, latest version of each,
which is the same set the derived model and the data-flow checks read. A draft is not
compiled, and compiling every draft to answer a list would make the answer depend on
whether somebody's work in progress parses. The cost is real and it is stated on the page
rather than left to be assumed: a class used by nothing *here* may still be used by work
in progress.

**Nothing is stored.** Both sides are read fresh on every call and matched by name, for
the reason ADR-0310 gives: the names are already the mechanism — `itemSubjectRef` resolves
a class by name, a write path names a member, a state's name *is* its identity — so there
is no second identity to keep and nothing to keep it in.

The computation lives in `api/infomodel` (pure, testable, walking each process once and
bucketing every use by the class it resolves to); the handlers live in `api` beside the
derived model's, because the answer joins the models the service owns to the compiled
processes only the server can reach.

### Consequences

- **Positive:** the question a change actually asks has an answer, located precisely
  enough to act on. An enumeration's worth is visible for the first time. Two applications
  modelling the same thing twice is visible for the first time. No new state, so nothing
  can go stale or need repairing after a restore.
- **Negative / trade-offs accepted:** the reading is recomputed per call, walking the
  application's compiled processes — cheap at design-time scale and not free for ever; if
  it starts to hurt, the honest fix is a cache with an invalidation, not a stored index.
  A class used only by a draft reads as used by nothing, qualified by a sentence somebody
  may not read.
- **Follow-ups / risks to watch:** a link from the class canvas's properties panel into
  the where-used page is the obvious next step and is deliberately not taken here — the
  editor holds unsaved state, so navigating out of it is a decision of its own. The open
  question above (reading Modeler drafts) is the other half of "used by nothing" and is
  worth revisiting once somebody is bitten by it.

## Pros and cons of the options

### 1. Nothing — read the derived model
- Good: exists; already says which members and states the processes reach.
- Bad: it is per application and per class-in-aggregate. It says `Order.total` is written
  somewhere in this application; it does not say by which element of which process, which
  is the whole content of the question. It also cannot see the model's own uses, so an
  enumeration is invisible to it by construction.

### 2. A stored usage index
- Good: constant-time to read; could be queried across applications without walking
  anything.
- Bad: a third statement about the vocabulary, and the only one that can be wrong. It has
  to be maintained on deploy, on deactivation, on model write, on delete, and on restore —
  and every one of those is a place it can drift from the two documents that are actually
  authoritative. The reading is cheap enough that this buys nothing but risk.

### 3. A computed reading
- Good: cannot be stale; adds no state to back up, migrate or repair; the same walk
  answers both the list and the detail.
- Bad: costs a walk per call; bounded by deployments rather than by instances, which is
  the bound that matters.

## Links

- builds on [ADR-0230](0230-process-information-model.md) — the information model itself
- reads the same process set as [ADR-0301](0301-derive-the-model-from-the-processes.md)
  and [ADR-0310](0310-read-the-difference-between-what-is-built-and-what-is-planned.md)
- reads lifecycles per [ADR-0259](0259-data-object-lifecycle.md) and the
  enumeration source of [ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)
- the list uses the shared table of [ADR-0286](0286-a-list-carries-its-own-search.md)
- served as a read on the server side beside the other two, per [ADR-0147](0147-splitting-the-api-server-object.md)
