# ADR-0296: A loop records the element it produced, not the collection so far

- **Status:** Draft
- **Date:** 2026-09-09
- **Deciders:** Atlas engine team

## Context and problem statement

A multi-instance activity collects one result per iteration into one list variable
(ADR-0077). It did that by writing the whole list back each round: read the
collection, set one element, serialise it again, emit a `VariableCreated` event
carrying the result.

So each round recorded the collection *as it then stood*. Round one recorded one
element, round fifty recorded fifty, round N recorded N. The bytes written grew with
the **square** of the iteration count, and every intermediate version of the list was
durable — in the log, and in the instance's variable timeline (ADR-0048), which keeps
a snapshot of every variable write.

ADR-0294 named this while deciding the collection budget, and named it as the reason
that budget cannot be the fix: to keep the bytes under ten gigabytes at the iteration
ceiling of a hundred thousand (ADR-0276), a collection would have to stay under about
200 KB — smaller than one variable is allowed to be. A budget that made the
amplification safe would be too small to be useful. This is that defect, measured and
fixed.

Measured on a loop whose every round produces about two hundred bytes:

| iterations | log before | log after | log ÷ answer, before | after |
|---|---|---|---|---|
| 10 | 31 983 | 22 879 | 15.7 | 11.3 |
| 20 | 82 263 | 43 239 | 20.3 | 10.6 |
| 40 | 245 223 | 83 959 | 30.2 | 10.3 |
| 80 | 820 743 | 165 399 | 50.5 | **10.2** |

Before, doubling the iterations multiplied the log by 3.3 and climbing, and the cost
per byte of answer doubled with the loop. After, doubling doubles, and the cost per
byte of answer is flat.

## Decision drivers

- The defect was invisible to every functional assertion: the loop always produced the
  right answer. What was wrong was the price of producing it, so the guard has to be a
  measurement.
- The log is the part that must not grow superlinearly: it is retained, replayed on
  recovery, and spanned by checkpoints.
- `applyToState` runs live and on replay and must stay a deterministic function of the
  batch's state and the event (I4).

## Considered options

1. Record the element: a new intent naming the index and the one value. (chosen)
2. Store the collection as one key per element.
3. Keep writing the whole list, but skip the per-round timeline snapshot.
4. Leave it; the collection budget bounds the peak.

## Decision outcome

Chosen option: **option 1.**

`IntentVariableElementSet` carries a `VariableValue` whose `Text` is *the element's*
value and whose new `Index` field says which element it is. The fold reads the
collection, sets that element and writes it back. The record is the size of one
result, whatever the collection has reached.

`Index` is appended to the encoding and stored one higher, so a record written before
the field existed decodes to **-1** — "this write is the whole value", which every
such record is. Zero would have been the wrong default: zero is a real index, and an
old record would have claimed to set element 0 of a list.

**It records no snapshot.** A loop's half-filled collection is scratch at the body
scope until the loop promotes it, and the promotion writes the whole value with a
snapshot of its own. This is the same reasoning that already keeps the dropping of an
activity-local scope out of the timeline: the local was scratch state, so its removal
is not part of the instance's variable history. Snapshotting each round is what put
the growing list into the timeline N times.

**What moved, and what did not.** The read-modify-write of the list did not get
cheaper; it moved from the behaviour into the fold. Live, the total work is what it
always was. On replay it is now paid too, where before replay only put a value that
had already been serialised — replay does the work the live path does, which is what
`applyToState` is for.

### Consequences

- **Positive:** the log is linear in the iteration count. At eighty iterations that is
  five times less, and the gap widens with every round.
- **Positive:** the instance's variable timeline shows the promoted collection once
  rather than every half-filled version of it.
- **Negative, and the honest limit of this change:** the **state store is still
  quadratic**. The fold still puts the whole collection per element, so Pebble still
  absorbs O(N²) bytes of churn — measured at 45 % less than before (the snapshots are
  gone) and still doubling as the loop doubles. Fixing that is option 2, one key per
  element, and it reaches into every reader of a variable: FEEL, the API, the value
  index, history, snapshots, migration. It is a change about how a list is stored, and
  it deserves its own decision rather than a rider on this one.
- **Negative:** the collection's own ceiling (ADR-0294) is no longer checked on the
  path that fills it, because that path no longer carries the collection. Each element
  is checked against the variable budget, and the iteration count is bounded, so the
  product of the two is the ceiling a collection actually has now. That is looser than
  a direct check, and it is the one guarantee this change gives up.
- **Follow-ups / risks to watch:** option 2, if a loop's store churn ever matters as
  much as its log did.

## Pros and cons of the options

### Option 1 — record the element (chosen)
- Good: linear log, smaller timeline, contained change, append-compatible encoding.
- Bad: the store's churn is untouched; the collection budget is now enforced indirectly.

### Option 2 — one key per element
- Good: linear everywhere, log and store alike.
- Bad: changes what a list variable *is* for every reader of one. A much larger change,
  and not one to make while measuring something else.

### Option 3 — keep the whole-list write, drop the snapshot
- Good: smaller; fixes the retained timeline.
- Bad: the log stays quadratic, which is the half that must not.

### Option 4 — leave it
- Good: nothing changes.
- Bad: a hundred thousand results of a kilobyte each cost a hundred gigabytes of log to
  record a hundred megabytes of answer, and ADR-0294 already established that no budget
  can make that safe.

## Links

- fixes the amplification named in [ADR-0294](0294-a-variable-is-a-record.md), which
  could not fix it with a budget and said so
- changes how [ADR-0077](0077-multi-instance-activities.md)'s output collection is
  recorded, not what it contains
- narrows what [ADR-0048](0048-per-step-variable-snapshots.md) keeps: the promoted
  collection, not every half-filled version of it
- bounded in size by [ADR-0276](0276-iteration-budget.md), which is now half of what
  bounds a collection
