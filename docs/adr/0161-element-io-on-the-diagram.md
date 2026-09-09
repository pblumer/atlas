# ADR-0161: What an element was handed, on the diagram

- **Status:** Accepted
- **Implementation:** Landed
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

## Amendment: the loop's own facts

The first cut of this decision listed the multi-instance loop counter among the things
an activity's local scope holds that are *not* inputs, and filtered it out. That was
wrong, and an operator reading a replay found it immediately: a loop runs the same node
again and again, so its history is a column of identical rows, and the one value that
tells them apart — which round this is — was the value being discarded.

`loopCounter` (1-based) and, for a collection loop, the round's item are bound into the
iteration's own scope by the engine (ADR-0077/0133). They are as much "what this element
instance was handed" as an input mapping is. They join `inputs`, and the counter is also
surfaced as `iteration` on the step — its identity rather than its data, so the history
can label a row and the Details tab can name the round without the frontend fishing a
known variable name out of a list.

**A loop body is a scope, and is now folded like one.** A standard loop holds what its
iterations write at the *body* scope so each round can read the previous round's result,
promoting it only when the loop ends (ADR-0133); a multi-instance loop assembles its
output collection there the same way (ADR-0077). Neither was folded, so a finished round
truthfully reported that nothing in the process scope had changed — which the diagram
card stated as *wrote nothing* about a round that had plainly done work. Loop bodies now
fold exactly like subprocess scopes (ADR-0074), labeled with the element's id, so a
round shows what it inherited from the previous one and what it left for the next.

The body is identified from the log rather than guessed: every iteration is activated
carrying its body's token as `ParentTokenID`, so the tokens named that way are the
bodies. A loop that never ran an iteration has no such scope and nothing to show in it.

## Amendment: the card chooses where to hang

The first cut hung the card under its element, always, and listed *the card can cover
what the modeler drew below a task* among the things accepted for it. Read on a real
model, that turned out to be an understatement. An event is 36px tall and a card with
eight input values is nearly 210px, so on the identity lifecycle the card of
"Service-Ereignis" came down over the entire mutation branch — two shapes and both their
captions — while the band above the event stood empty. Accepting that the card covers
the model is one thing; covering a branch while free air sits next to it is another.

So the card no longer has one place. It has eight — the four sides of its element, each
flush with one of that side's two edges — and it takes the one that hides the least, the
first of them in a fixed order when several tie. A card with room under it therefore does
not move at all, which keeps the ordinary case exactly where a reader has learned to look
for it.

**"Least" is measured as how much of each element disappears, not as area covered.**
Summed area answers the same question with the sign flipped: it prefers swallowing two
small elements to clipping the corner of a big one, which is how the first attempt at
this managed to cover the mutation branch a second time. Hiding an 8px strip of a 100×80
task costs 0.1; hiding a 36px event whole costs 1. A caption is scored as an element of
its own, because an element whose name is hidden is not much better off than one whose
shape is.

Two things are deliberately not weighed. **Sequence flows**, because a line whose two
ends both stay visible reads across a card, and no spot crosses none of them. And
**badges**, because they are 20px pills scattered over every executed element; dodging
them would push the card around for nothing, and what happens when the card lands on one
is already settled — the card is opaque over it (the stacking rule below).

**What is on screen counts too**, at twice the weight of hiding an element. A diagram is
fitted to its canvas, so an element at the edge of the model has free air on that side
and none of it in view; a card hung out there hides nothing by virtue of being nowhere.

The spot is chosen when the card is drawn — that is, when its content changes — and not
re-chosen on pan or zoom. A card that chased the viewport would be the more distracting
of the two ways to be wrong.

## Amendment: the card is one surface

Nothing pokes through the card. diagram-js wraps every overlay in a bare positioned div
with no `z-index`, so overlays paint in the order their *elements* first received one,
which is the order the runtime response lists them in. That order works against this card
systematically: it hangs off its element, so what it covers are shapes drawn after it,
whose execution and incident badges therefore landed on top of it — a covered neighbour's
"11" standing among the card's own value rows, reading as one of its values.

The card is the thing being read at that moment, so it takes the layer above the badges:
the overlay is typed `atlas-io` and the stylesheet gives that type a positive `z-index`.
What it covers is covered, and the transport bar's toggle is how a reader gets the model
underneath back.

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
- **The card can still cover what the modeler drew.** It now takes the least bad of
  eight spots rather than always the same one, but on a dense diagram every spot covers
  something and one of them is still chosen. Hence the toggle, and hence the six-row cap.
- **Placement costs one layout per redraw.** The card's height depends on its content and
  the CSS that lays it out, so it is measured — the real markup, off-screen, at its real
  width — rather than predicted from the row count. It is paid only when the card's
  content changes, not on every frame of a scrub.
- **Where the card hangs depends on the diagram around it**, so the same element can put
  its card on a different side after the model is edited, and two elements side by side
  need not agree. The alternative is a fixed side that is wrong wherever the model is
  dense, which is where a replay is read most closely.
