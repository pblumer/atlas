# ADR-0306: A lifecycle may take its states from an «enumeration»

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Patrick Blumer
- **Open question:** whether the enumeration should also be offerable as an attribute's
  type on the same class — `status : Lebenszustand` beside the lifecycle that reads the
  same literals — or whether that is one statement too many for one document to make.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0259](0259-data-object-lifecycle.md) gave a class a state machine, and it was
written against a real model that already had one — drawn as an «enumeration».

That model is the whole problem statement. Its author had written the five states of an
identity as an «enumeration» named `Lebenszustand`, with a paragraph of documentation on
each literal, *because that was the only place the states could be written down at all*.
ADR-0259 then added a second place, and the model now says the same five strings twice:
once as literals a person maintains, once as states a deploy resolves against. Nothing
connects them, nothing notices when they drift, and the documentation — the part worth
more than the strings — sits on the half the engine does not read.

Asked how to show the tie in the class diagram, the honest answer today is: you cannot.
`AllowAssociation` refuses every relationship that touches an enumeration, correctly, and
suggests an attribute typed as it instead. But `status : Lebenszustand` is a different
statement — it says an instance carries one of these values, and says nothing whatever
about which may follow which, which is most of what a lifecycle is for.

So: **can the enumeration a person already wrote become the states of the lifecycle,
rather than a second copy of them?**

## Decision drivers

- **One place, or the two drift.** Two lists of the same five strings in one document is
  a defect waiting for its first rename. Whatever is chosen has to leave exactly one
  place where a state's name is written.
- **The documentation is the valuable half.** A generator will never produce "an identity
  is *gesperrt* when compliance has objected but the record is not yet closed". Wherever
  the states come from, that prose has to survive and stay attached.
- **A lifecycle is not a type, and this must not blur that.** ADR-0259's reasoning
  stands: an enumeration is a closed set of values; a lifecycle is that set *plus an
  order*, and the order is what it exists to say. Nothing here may end with the order
  being expressible as an attribute.
- **The order cannot be drawn in a class box.** No UML class diagram carries "cancelled,
  but not after shipped". That belongs on the state machine and nowhere else, so the
  class diagram's job here is to show the *tie*, not the content.
- **The subset is the contract, and the server owns it** ([ADR-0237](0237-class-canvas-on-diagram-js.md) §2).
  Whatever the canvas draws, what is stored is what `Validate` accepts.
- **Silence when unused.** A lifecycle that names no enumeration must behave exactly as
  it does today, which is how ADR-0259 itself degrades.

## Considered options

1. **A lifecycle references an «enumeration»; its literals are the state names
   (chosen).** The order, the initial state, the final states and the layout stay on the
   lifecycle. The strings live once, in the enumeration.
2. **An attribute typed as the enumeration, and nothing else.** What the subset says
   today.
3. **Generate an «enumeration» from a lifecycle.** The same tie, read the other way: draw
   the machine, get a read-only enumeration for attributes to be typed as.
4. **Leave them unrelated and check for drift.** A finding when a class's states and
   some enumeration's literals are nearly the same.

## Decision outcome

Chosen option: **"A lifecycle references an «enumeration»"**.

`Lifecycle.StatesFrom` names an «enumeration» in the same model. When it is set, that
enumeration's literals *are* the lifecycle's states: the name of a state is written in
exactly one place, and the documentation a person wrote on a literal is the documentation
of the state. The lifecycle keeps everything an enumeration cannot hold — which state is
initial, which are final, what may follow what, and where each sits on the canvas.

The two statements stay apart, which is the point. The enumeration answers *which values
are there*; the lifecycle answers *in what order*. Neither is derivable from the other,
and this is the reference between them rather than a merge of them.

**In the class diagram the tie is one dashed line.** A `«lifecycle»` dependency from the
class to the enumeration, derived from the reference and never authored — exactly the
store link of [ADR-0230](0230-process-information-model.md) §7, for exactly its reason: a
class and its enumeration do not *relate*, one *takes its states from* the other. So
`AllowAssociation` is untouched and an enumeration is still not the end of a
relationship. It is not an association, so nothing that counts relationships counts it.

Option 2 is what the subset already says, and the record exists because it is not enough.
Option 3 inverts the dependency the wrong way: it would make the lifecycle the place the
strings live and hand the modeler a generated enumeration, which is exactly the artifact
whose documentation nobody writes. Option 4 detects the drift this option makes
impossible, at more cost.

### 1. What the reference is

- `Lifecycle.StatesFrom` — the **name** of a class in the same model, empty for the
  normal case. A name rather than an id, for the reason `Class.Identity` names attributes
  and `DataStore.Class` names a class: a lifecycle state's identity is already its name,
  and this is the same kind of reference.
- It is legal only on a class that may carry a lifecycle at all, which the stereotype
  rules already settle.
- The target must exist and must be an «enumeration». Anything else is the document
  saying two contradictory things and is refused on write.
- Every state must be one of that enumeration's literals. A state that is not is the
  same contradiction, and is refused.

A literal with **no** state is not refused. A lifecycle half-drawn is the normal
condition ADR-0259's validator is careful about, and "you have declared a value you have
not placed on the machine yet" is incompleteness, not incoherence.

### 2. Where the strings live

Renaming a literal renames the state, and the transitions that name it are rewritten —
which is precisely what renaming a state already does, because a state's name *is* what
every process writes. Adding a literal offers a state; removing one removes the state and
the transitions that touch it. The editor does this at the point of the edit, so what
reaches the server is already coherent and the refusals above stay refusals of a
malformed document rather than a workflow.

### 3. What is deliberately not done

- **No generated enumeration.** A lifecycle that declares its states itself stays exactly
  as it is; nothing produces an enumeration from it.
- **No attribute.** This record does not add `status : Lebenszustand` to the class, and
  whether it should is the open question above. A BPMN data state is not held in an
  attribute — it is held in the `<dataState>` — so an attribute would be a third place
  the same fact could be written.
- **Nothing in the engine.** The reference is resolved where a class name is: at deploy,
  into the same vocabulary `CheckDataFlow` already reads. The run-time half of ADR-0259 is
  untouched.
- **Nothing in the derivation.** A derived model (ADR-0301) has no enumerations to point
  at — it reads states off the processes, and that is a different statement again.

### Consequences

- **Positive:** the five strings are written once, and the documentation a person wrote
  is the documentation the state carries.
- **Positive:** the class diagram finally shows what the modeler asked for — which
  enumeration is which class's life — as one line rather than as a convention in two
  people's heads.
- **Positive:** it makes the enumeration worth writing. Before this it was a place to
  park strings the engine ignored.
- **Negative:** a class's states can now be edited from two screens — the enumeration's
  literals and the lifecycle sheet — and only one of them may rename. The lifecycle sheet
  therefore shows the names as read-only and says where they come from, which is a
  smaller surprise than a rename that silently does nothing.
- **Negative:** a second kind of derived line on the class canvas. Mitigated by its being
  the same construction as the store link rather than a new one.

## Pros and cons of the options

### Option 1 — the lifecycle references an enumeration
- Good: one place per string; the documentation survives; the order stays where only a
  state machine can hold it; the tie is drawable.
- Bad: two screens edit one list; a new derived edge.

### Option 2 — an attribute typed as the enumeration
- Good: already in the subset, and correct UML for what it says.
- Bad: it says the wrong thing. A value an instance carries is not a set of stages with
  an order, and nothing connects it to what a deploy resolves.

### Option 3 — generate an enumeration from a lifecycle
- Good: the same single source, read the other way, and it makes the states usable as an
  attribute type.
- Bad: it puts the strings in the half nobody documents, and it produces a class the
  modeler did not draw and may not delete.

### Option 4 — leave them unrelated and report drift
- Good: no new field.
- Bad: it reports a problem this record prevents, and it has to guess which enumeration
  was meant.

## Links

- extends [ADR-0259](0259-data-object-lifecycle.md) — the lifecycle this gives a source to
- reuses [ADR-0230](0230-process-information-model.md) §7 — the store link, which is the
  same derived-edge construction
- constrained by [ADR-0237](0237-class-canvas-on-diagram-js.md) — the served subset
- unrelated to [ADR-0301](0301-derive-the-model-from-the-processes.md), deliberately: a
  derived model has nothing authored to point at
