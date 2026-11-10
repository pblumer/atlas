# ADR-0549: A loop's collection is retained one element per round

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** Atlas maintainers

## Context and problem statement

A multi-instance activity collects one result per round into its output collection,
which lives in the body's scope until the loop promotes it (ADR-0077). ADR-0296 made
each round record only its element — `IntentVariableElementSet`, the index and the one
value — so the log stays linear in the round count. It also decided that the element
write **records no snapshot**: the half-filled collection was scratch until the
promotion, and snapshotting the whole list each round is what had made the variable
timeline grow with the square of the count.

The replay pays for that. Every round's snapshot of the body shows the collection as it
was seeded — `[null, null, …]` — and the filled list appears only once the loop has
finished. ADR-0542 made the replay say so on the collection and point to each round's
Output, but the list a reader steps through is still not the list the instance held: a
round that answered shows no answer in the collection, and the round after it starts
from nothing.

The question: **can the replay show the collection as each round left it without
bringing back the quadratic history ADR-0296 removed?**

## Decision drivers

- **The history stays linear.** What ADR-0296 removed — the whole list once per round —
  must not return, in the log or in the retained history.
- **The log does not change.** The element event already carries everything needed;
  a new event, or a fatter one, would be a format change for nothing.
- **Deterministic folds (I4, I6).** What is retained is written in `applyToState` from
  the event and the batch's own state, live and on replay alike.
- **History goes with its instance.** A purge (ADR-0115, ADR-0146) must take it.
- **No reader misreads it.** The existing variable snapshots are read by several
  surfaces as whole values; a whole-value write is stored with `Index` 0, so an element
  row in that family could not be told from a whole value.

## Considered options

1. **Retain the element, in a family of its own, keyed under the instance** (chosen).
2. **Retain the element in the variable snapshot family.**
3. **Snapshot the whole list each round again.**
4. **Leave it; keep the explanation (ADR-0542).**

## Decision outcome

Chosen option: **1**, because it is the only one that shows the list the instance held,
keeps both the log and the history linear, and touches no existing reader.

- **What is retained.** When the fold applies `IntentVariableElementSet`, it also writes
  the element — the `VariableValue` with its `Index`, the size of one result — to a new
  column family `varElemSnap:<piKey>:<scopeKey>:<ts>:<pos>` (`0x48`). Only an element the
  fold actually set is retained (an absent collection or an index outside it is a no-op
  there, and here), so the history never claims a write the state did not take.
- **Under the instance.** The scope is the body, an element instance live in the same
  batch; its process instance is read from the batch's state, so replay files the row
  under the same key. The instance prefix is in `PurgeInstanceHistory`'s list, so the
  rows go with the instance — unlike the scope-keyed variable snapshots, which a purge
  does not reach.
- **The replay folds them.** The timeline reads a multi-instance body's elements beside
  its whole-value snapshots and applies each one, by log position, to the list as it
  stands, serialised as the engine's fold serialises it. A round sees the collection as
  the rounds before it left it; its value at completion includes its own answer. A body
  whose elements are on record says so on its values (`elementsRecorded`).
- **Older instances.** An instance recorded before this has no element rows. Its
  collection reads as seeded until the loop is promoted, as before, and the Console's
  explanation from ADR-0542 is now shown only there: on a body without recorded
  elements, once one of its rounds has finished.

### Consequences

- **Positive:** the replay shows the collection the instance held, round by round. The
  log is unchanged; the retained history grows by one element-sized row per round.
- **Negative / trade-offs accepted:** one more state write, and one point read of the
  body's element instance, per round in `applyToState`. The fold already parses and
  re-serialises the whole list per round (ADR-0296's quadratic store churn), so this is
  a small constant beside it, with the same allocation profile as the snapshot every
  other variable write records. Reading a round's list costs the replay a parse and a
  serialisation per element, linear in the elements on top of the steps it already
  renders.
- **Negative:** instances recorded before this change cannot be backfilled. The rows
  could only be rebuilt by replaying the log; the explanation stays for them.
- **Follow-ups / risks to watch:** the Multi-Instance Explorer (ADR-0517) reads an
  iteration's own scope and the scopes opened inside it, not the body's, so it shows no
  collection and needs nothing from this family today; a view of the body there would
  read it the way the timeline does. Sub-scope variable snapshots are still not reached
  by a purge, as `PurgeInstanceHistory` documents; that gap predates this record.

## Pros and cons of the options

### Option 1 — own family, under the instance (chosen)
- Good: linear; log unchanged; purged with the instance; no existing reader affected.
- Bad: a new family; one extra point read per round in the fold.

### Option 2 — the variable snapshot family
- Good: no new family; the existing scans would carry it.
- Bad: a whole-value write is stored with `Index` 0, the same as element 0, so a row
  could not be told apart; every reader of the family — the timeline, the
  Multi-Instance Explorer's scope and step reads — would misread elements as values.
  And the family is keyed by scope, which a purge does not reach.

### Option 3 — snapshot the whole list again
- Good: every reader works unchanged.
- Bad: brings back exactly the quadratic history ADR-0296 removed.

### Option 4 — leave it
- Good: nothing changes.
- Bad: the replay keeps showing a list the instance never held.

## Links

- amends ADR-0296 (its "records no snapshot": the element is now retained, not the list)
- amends ADR-0542 (the collection's explanation is shown only where elements are not on record)
- relates to ADR-0048, ADR-0077, ADR-0115, ADR-0146, ADR-0517
