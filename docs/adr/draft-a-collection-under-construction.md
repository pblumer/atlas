# ADR-DRAFT: A collection under construction is stored one key per element

- **Status:** Draft
- **Date:** 2026-09-09
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0296](0296-a-loop-records-its-element.md) stopped a multi-instance activity
recording the whole collection once per round. The event now names the element, so the
*log* grew linearly. That ADR then said, in as many words, what it had not fixed:

> the **state store is still quadratic**. The fold still puts the whole collection per
> element, so Pebble still absorbs O(N²) bytes of churn — measured at 45 % less than
> before (the snapshots are gone) and still doubling as the loop doubles.

This is that half. Measured over the same loop, whose every round produces about two
hundred bytes:

| iterations | store before | store after | store ÷ answer, before | after |
|---|---|---|---|---|
| 10 | 34 394 | 25 228 | 16.9 | 12.4 |
| 20 | 84 697 | 45 491 | 20.9 | 11.2 |
| 40 | 247 743 | 86 002 | 30.5 | 10.6 |
| 80 | 823 503 | 167 037 | 50.7 | **10.3** |

Doubling the iterations multiplied the store by 2.5, then 2.9, then 3.3 — climbing,
which is what a quadratic looks like when you only have four points. It now multiplies
it by 1.8, 1.9, 1.9. The log is unchanged by this: it was already linear, and the two
now cost about the same, which is the shape a loop should have.

ADR-0296 also named the price it paid for the linear log: the collection's own budget
([ADR-0294](0294-a-variable-is-a-record.md)) stopped being checked on the path that
filled it, because that path no longer carried the collection. What it did not notice
is that the one remaining check — at the promotion — had its result thrown away. That
is fixed here too, because it is the same question: where does a collection exist, and
who measures it there.

## Decision drivers

- The store is the half a user pays for continuously. The log is retained and spanned
  by checkpoints; the store is what the machine has to hold.
- Whatever changes, no reader of a variable may have to change with it. A list is read
  by FEEL, the API, connectors, the playground, the conformance runner — around
  twenty-five call sites.
- `applyToState` runs live and on replay and must stay a deterministic function of the
  batch's state and the event (I4).
- The form must not cost anything to a variable that is not a collection.

## Considered options

1. Hold a collection's elements one key per element **while a loop is filling it**, and
   collapse it into an ordinary record when the loop promotes it. (chosen)
2. Store **every** list variable one key per element, permanently.
3. Leave it: the log is linear now, and the store compacts.

## Decision outcome

Chosen option: **option 1.**

A variable record gains `Parts`. When it is positive the record is a *stub*: it says
how many elements the list has and nothing about their values, and the elements live in
their own column family under `varEl:<scope>:<name>:0x00:<index>`. When it is zero —
every variable that is not a collection under construction, and every record written
before the field existed — the value is in `Text` exactly as before.

**The form never leaves the storage layer.** Three read paths assemble it: `GetVariable`
and `VariablesOfScope` on a transaction, and the committed-store decode that a
`ReadView` and the live `Store` share. Between them they carry every one of those
twenty-five call sites, so a caller gets the list and no caller learns it was stored in
pieces. This is what let the change stay inside `state` and the fold.

**Assembly is exact, not approximate.** The element's stored bytes are its own canonical
JSON, and `encoding/json` renders a list as its elements separated by commas inside
brackets, escaping an element identically whether it encodes it alone or in place. So
joining the fragments reproduces, byte for byte, the text a whole-list write would have
stored. That is what makes the two forms the same value rather than two values that
usually agree, and there is a test that holds them together.

**A slot no iteration has written yet reads as null** — the same null the collection was
seeded with, so a half-filled collection reads mid-loop exactly as it did before. A
loop's completion condition can read the accumulating collection, and it still sees
what it saw.

**The fold reads nothing.** `setVariableElement` asks only how long the collection is —
a point read of the stub — and writes one key. Reading the collection back to add to it
is precisely the cost this removes; doing it to find the length would have put the same
cost back as CPU. The one exception is the first element of a run, which moves the
seeded list into the element family once.

### The collection's budget, restored

ADR-0296 left a collection measured only at its seed, where it is a list of nulls.
The promotion — the one moment the assembled collection exists as a value — did raise
an incident when it did not fit, and then completed the body anyway, which deleted the
incident along with the element carrying it and dropped the collection with the scope
holding it. A loop that ran every iteration finished looking successful with nothing to
show. The refusal is now honoured: the body stays activated holding its token, and
resolving promotes the same collection again.

So a collection is measured at its seed and at its promotion, and between those two the
elements are measured one at a time against the variable budget. That is the honest
account of what bounds a collection, and it is stricter than what ADR-0296 left.

### Consequences

- **Positive:** the store is linear in the iteration count, and a round costs one
  element in both the log and the store.
- **Positive:** the collection budget is enforced again at the moment the collection
  exists, and a refusal there parks the loop instead of vanishing.
- **Positive:** nothing outside `state` and the fold changed. No reader, no snapshot, no
  migration, no API shape.
- **Negative:** reading a half-filled collection is a scan of its elements, so a
  completion condition that reads it every round is O(N²) *work*. It always was — the
  old form decoded an N-element JSON list on the same reads — so this is a cost carried
  over, not one introduced. It is the next thing to measure if a loop's condition ever
  dominates.
- **Negative:** a variable now has two storage forms. The stub is unreachable by
  construction from outside `state`, but it is a second shape a future reader could
  meet by scanning the family directly, and that is a thing to remember rather than a
  thing that is checked.
- **Negative:** a delete of any variable now opens an iterator over the element prefix,
  which finds nothing for the overwhelming majority. It is paid on scope teardown, not
  on the write path, and it is what makes a leaked element impossible rather than
  unlikely.
- **Follow-ups / risks to watch:** option 2 remains available if a list written whole
  ever gets large enough to matter; nothing here forecloses it.

## Pros and cons of the options

### Option 1 — element-wise while a loop fills it (chosen)
- Good: linear store, contained to two packages, no reader changes, no change to what a
  list is anywhere a list is read.
- Bad: two storage forms for one value type; the assembly cost on a mid-loop read.

### Option 2 — every list variable, permanently
- Good: linear for any repeated element write, whatever writes it.
- Bad: it changes what a list *is* for every reader — FEEL, the API, the value index,
  history, snapshots, migration, purge — for a benefit only a loop can collect. The
  quadratic churn comes from writing elements repeatedly, and only a loop does that.

### Option 3 — leave it
- Good: nothing changes.
- Bad: a hundred thousand results of a kilobyte each churn the store with the square of
  the count to hold a hundred megabytes of answer. ADR-0294 established that no budget
  makes that safe, and ADR-0296 fixed only the log.

## Links

- finishes [ADR-0296](0296-a-loop-records-its-element.md), which made the log linear and
  named this as the half it had not done
- restores the collection ceiling of [ADR-0294](0294-a-variable-is-a-record.md) at the
  promotion, where the assembled collection exists
- changes how [ADR-0077](0077-multi-instance-activities.md)'s output collection is
  stored, not what it contains
- leaves [ADR-0048](0048-per-step-variable-snapshots.md) as ADR-0296 left it: the
  promoted collection is snapshotted, the half-filled one is not
- bounded in element count by [ADR-0276](0276-iteration-budget.md)
