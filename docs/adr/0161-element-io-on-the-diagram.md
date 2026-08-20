# ADR-0161: What an element was handed, on the diagram

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

Reading a replay, an operator's question about a particular task is almost always the
same one: *what went into it, and what came out?* Both halves existed, but neither was
where the question is asked.

**The output half** arrived with ADR-0159: `variablesAfter` is the variable fold at an
element's completion, and the Variables tab offers Input / Output for a finished
element. That answers the question — in a tab, about the whole instance, for whichever
element happens to be selected.

**The input half did not exist at all.** An activity's `zeebe:ioMapping` inputs are
evaluated into its *activity-local* scope, keyed by the element instance (ADR-0068).
The instance timeline folds the process scope and each subprocess scope (ADR-0074) and
nothing else, so the values a task was actually handed — the result of evaluating
`=order.amount * 2` at the moment the token arrived — sat on the log and appeared
nowhere in the API. The Properties panel could show the mapping *as written*; no
surface showed what it *evaluated to* on this instance.

So the operator's question was answered by reading a source expression in one view and
guessing which of the instance's variables it had read in another.

## Decision drivers

- **Answer the question where it is asked.** While reading a diagram, the answer
  belongs on the diagram, not two clicks away in a tab.
- **What happened, not what was declared.** A mapping source is the model's intent;
  the evaluated local is the fact. Only the fact is worth putting on a canvas.
- **Glanceable, or not there at all.** A card that covers the model to restate the
  Variables tab makes the diagram worse. It must be small, bounded, and skippable.
- **No new persistence.** These values are already on the log. This is a read-side
  change; nothing about how anything is written may move.

## Decision

**API.** A timeline step gains `inputs`: the element's own input-mapping locals, as
evaluated, sorted by name. It is built by scanning the activity-local scope — but only
for elements whose compiled definition declares input mappings, so an instance with
none costs no extra reads, and keeping only the declared targets, since an activity's
local scope also holds values its behavior parked there (a script result awaiting its
output mapping, a multi-instance loop counter) which are not inputs.

`inputs` is deliberately **not** folded into `variables` / `variablesAfter`. A local
belongs to one element instance; merging it into the shared running set would leak it
onto every concurrent step's snapshot.

**UI.** Selecting an element in the replay hangs a small card under it on the canvas:

- **in** — the step's `inputs`, or *no input mapping*.
- **out** — what the element itself wrote: the difference between the variables it saw
  on entry and the ones it left behind. *still running* while it has no completion yet,
  which is a different statement from *wrote nothing*.

The card is capped at six rows a side (the Variables tab has them all), takes no
pointer events so a click always reaches the element underneath, and is toggleable from
the transport bar — the preference persists, because it is about how a person reads a
diagram rather than about one instance.

The "out" set is computed by the same function that marks the `+` chips in the
Variables tab, so the diagram and the tab cannot come to tell different stories.

## Consequences

**Positive.** The question a replay is opened to answer is answered in place, from
runtime fact rather than model intent. Input-mapping values become inspectable at all —
previously they were durable but unreachable. No new value type, event, column family,
or recovery path: the timeline reads a scope it already stores.

**Negative / accepted.**

- **One store scan per io-mapped element instance** on the timeline read. Bounded by
  the elements that declare mappings, and the same shape as the per-subprocess scan
  already there, but an instance with very many mapped activities pays for it. If that
  becomes real, the scans belong behind a query parameter, not in the default response.
- **"in" covers input mappings only.** A task without them receives its variables by
  the ordinary scope chain, and the card says so rather than inventing a list.
- **The card can cover what the modeler drew** below a task. Hence the toggle, and
  hence the six-row cap.
