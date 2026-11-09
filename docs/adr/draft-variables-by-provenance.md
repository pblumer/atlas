# ADR-DRAFT: A selected element's variables are shown by how it came by them

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** Atlas maintainers
- **Open question:** Should the engine record which variables an element actually read, so that Input can mean "used" rather than "handed"? Until it does, the Environment is everything an element could have read, never a claim that it did.
- **Question checked:** 2026-11

## Context and problem statement

The replay's Variables tab showed a selected element's variables as one list: the
element's own locals (its input mappings, and for one round of a loop the round's
`loopCounter` and item, ADR-0068/0077/0133) beside every variable of the process and of
every live subprocess and loop body. Three things made that list hard to read:

- **Two scopes, one chip.** A local was labelled with its element's id, and so was a
  variable of a loop body — a multi-instance's output collection, a standard loop's
  running result. In a round of a multi-instance task both read `BEWERTEN`, though one
  belongs to the round and the other to every round. The timeline carried only the
  scope's label, so the Console could not tell them apart either.
- **Two meanings of Input and Output.** ADR-0159 gave the tab an Input/Output switch
  that meant *as the variables stood before / after* the element. The diagram's in/out
  card beside it (ADR-0161, ADR-0219) uses the same two words for *mapped into the
  element* and *written by the element*. A reader asking "what did this task get and
  what did it produce" had to know which of the two surfaces meant what.
- **Everything else in the way.** With a task selected, the question is about the
  task. The process's variables answered a different question and outnumbered the
  task's own.

The question: **how does the tab show a selected element's variables so that what the
element was handed, what it produced and what it could merely reach are distinct — and
which scope holds each value is unambiguous?**

## Decision drivers

- **One vocabulary across surfaces.** Input and Output must mean on the tab what they
  mean on the diagram's card.
- **State only what the record knows.** The log says what an element was handed
  (`inputs`) and what it wrote (`writes`, ADR-0219). It does not say what an element
  read. Nothing on the tab may suggest otherwise.
- **Nothing that was reachable disappears.** A worker or a form can read any variable
  in its scope chain; when a task decided wrongly, what it could see on entry is the
  evidence. Removing it from the view of a selected task removes it where it is looked
  for.
- **Read-side only.** No event, no state, no hot path changes (I1–I6).

## Considered options

1. **Keep the list, improve the chips.** Name the scope's kind beside its id.
2. **Hide the process's variables once an element is selected.**
3. **Three sections — Input, Output, Environment — with the environment grouped by
   scope and folded by default** (chosen).

## Decision outcome

Chosen option: **3**, because it gives the two words the card's meaning, keeps every
reachable value one click away instead of removing it, and says which scope holds a
value with a heading instead of a chip.

### What the tab shows for a selected element

- **Input** — the element instance's own scope: its evaluated input mappings and, for
  a round of a loop, the round's counter and item. Exactly `step.inputs`.
- **Output** — what this element instance wrote (`step.writes`). A value that existed
  before says what it was; a value that did not is marked new. While the element runs
  there is no output yet, and the section says so rather than "wrote nothing". On an
  instance recorded before ADR-0219 the section keeps the old inference and says why in
  its tooltip.
- **Environment** — everything else the element could reach on entry, grouped by the
  scope that holds it, innermost first: its own loop body, each enclosing subprocess,
  the process, then the process's constants (ADR-0454/0456). A value that changed while
  the element ran shows what it had become at completion; one that appeared only then
  — another path's write, an operator's correction — is marked so. A subprocess running
  beside the element is folded into the same snapshot but is not in its reach, and is
  left out. The section is folded by default and the reader's choice is remembered in
  the browser; a name the filter finds in it is shown while folded.

With nothing selected the tab stays one list of the instance's variables as of the
replay frame. Its scope chip names the kind of scope beside the id.

The before/after switch is gone: Output carries what changed, and the Environment
carries what it had become.

### What the timeline adds

- `scopeKind` on a variable below the process: `subprocess`, `multiInstanceBody` or
  `loopBody`. It is also part of the key the timeline folds variables by, so a
  multi-instance subprocess's body and one of its iterations — two scopes under one
  element id — no longer overwrite each other's same-named variables.
- `scopeKind` `local` on a value an element wrote into its own scope (see below).
- `constant` on a process-scope variable the model's constants occupy: `process` for a
  process constant, `application` or `server` for the namespace the installation fills.

Both are additive and omitted when empty.

### Consequences

- **Positive:** the tab and the diagram's card tell the same story in the same words; a
  round's locals and its body's collection can no longer be mistaken for one another;
  the noise is folded rather than lost.
- **Negative / trade-offs accepted:** the Environment is reachability, not use. A reader
  can still not tell from the tab which of the reachable values a task actually read.
  The scope chain is read from the diagram on screen; for an element that is not on it
  (a migrated instance's removed element) every live scope is shown rather than guessed.
  A multi-instance body's collection reads unfilled until its loop finishes (see below).
- **Follow-ups / risks to watch:** recording reads — statically from FEEL expressions
  and form fields, or from a worker that declares the variables it fetches — is the
  open question above.

### What a round of a multi-instance wrote

A round keeps its result — a script's result variable, a person's answer on the form —
in its own scope until the body collects the output element from it (ADR-0077). The
timeline read that scope only for the round's inputs, so every round's Output said
"wrote nothing", including the round whose answer then appeared in the collection.

- **Writes include the element's own scope.** The scan of an element instance's own
  scope, which already ran for its inputs, keeps every other value the element itself
  wrote there (its producer key, ADR-0219) and adds it to the step's `writes` with
  `scopeKind` `local`. An operator's correction of a local is not the element's output
  and is not included. A loop body and a subprocess are left out: their scopes are
  folded and attributed already. No extra scan, and nothing for an element without
  inputs or a loop.
- **The body's collection stays as recorded.** Its per-round element writes are not
  part of the history, by ADR-0296's decision that a snapshot per round makes the
  timeline grow with the square of the count. It reads `[null, …]` in every round's
  snapshot until the loop finishes. The Environment says so on the collection, and
  points to each round's Output, instead of letting an empty list read as rounds that
  produced nothing. Recording the element writes would answer it at its source; that is
  ADR-0296's decision to revisit, not this record's.

## Pros and cons of the options

### Option 1 — keep the list, improve the chips
- Good: smallest change.
- Bad: leaves Input/Output meaning two things, and the task's own values still drown.

### Option 2 — hide the process's variables
- Good: the task's own values stand alone.
- Bad: removes the only evidence of what the task could have read, where an operator
  debugging it looks first.

### Option 3 — sections, environment folded
- Good: one vocabulary; nothing lost; scope stated by heading.
- Bad: one click to see the environment; a new field on the timeline.

## Links

- amends ADR-0159 (its Input/Output switch on the Variables tab)
- relates to ADR-0068, ADR-0074, ADR-0077, ADR-0133, ADR-0161, ADR-0219, ADR-0296, ADR-0454, ADR-0456
