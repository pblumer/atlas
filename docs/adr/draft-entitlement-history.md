# ADR-DRAFT: The remedy must not destroy the evidence, so a hold that ends leaves a row

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether this family needs an erasure path. Every other store in
  the portal holds principal references precisely so a person can be forgotten by
  forgetting the account; this one is built to outlive retention, which is the
  opposite pressure. The two are reconcilable — a row names an id and nothing else,
  so deleting the account already removes everything about the person — but that is
  an argument, not a mechanism, and nobody has asked the question against a real
  legal position yet.
- **Question checked:** 2026-09

## Context and problem statement

The inventory is present tense by construction. A grant writes a row, a revocation
deletes it, and "what does this person hold today" is answerable in one scan.

Two facts the codebase already states, put together, produce a third that nothing
had a place for:

- **An order is deleted by retention long before the right it granted ends.** This
  is why the inventory is engine state at all ([ADR-0312](0312-portal-catalogue-order-inventory.md));
  `order.Grant` says it in as many words.
- **A revocation deletes the inventory row.** That is what makes the inventory a
  statement about now.

So when a right ends, *everything* about it goes. The row is deleted, the order was
very likely deleted years earlier, and the estate can no longer answer whether the
person ever held the thing, under whose approval, or for how long.

That is the question an access review exists to ask, and it was the one question the
portal could not answer.

### Why it got worse rather than better

The last three slices made the portal good at finding problems. Every one of those
findings is about a *held* right, and every remedy ends the hold:

- Reconciliation ([ADR-0334](0334-reconciliation.md)) finds a right the target
  system does not have, and accepting the finding removes the record.
- An expiry (ADR-0344) reports a right overdue by forty
  days; returning it deletes the row, and with it the forty days.
- A conflict (ADR-0342) reports that somebody held
  `create-supplier` and `approve-payment` together for six months. **The moment
  anybody acts on it, the finding ceases to exist.**

An estate that remembers only the mistakes nobody fixed has the record exactly
backwards. The better the detection got, the more evidence the remedy destroyed.

## Decision

A hold that ends writes a row into a new engine-state column family
(`cfEntitlementHistory`, `0x2B`) in the same transaction that deletes the live
entitlement.

The row **copies** the hold rather than referring to it, because there is nothing
left to refer to — that is the whole point.

### The row says why it ended, and the reason changes what the row means

`HoldEnd` has two values and they are not two labels on a uniform row:

| Reason | What the row is evidence of |
|---|---|
| `returned` | the person **had** the access, until that moment |
| `corrected` | Atlas **claimed** they did, and reconciliation found the target system disagreed |

`handleRevokeDiscrepancy` already says this about itself: it "does not decide that
the disappearance was correct… what it decides is that Atlas will stop claiming
otherwise." Writing a correction as though it were a return would put, in a record
kept for years, an assertion that somebody had access nobody can show they had —
which is the direction of wrongness ADR-0334 calls the one that corrupts the
evidence. So `HoldEnd.Held()` exists, every row carries it, and the read route
renders both the word and the flag so that no reader has to rediscover the
distinction.

### The fold reads state, and that is not a violation of I4/I6

`RevokeEntitlement` carries the moment, the reason and the decider — everything
that could not be derived — frozen into the event at command time, as the grant
path already does. It deliberately does **not** carry the hold.

`Tx.EndEntitlement` reads the hold through the transaction instead. The invariants
require the fold to be deterministic and free of side effects, not free of reads: it
reads state this same pipeline built, through the same indexed batch, in the same
order, live and on replay alike.

Reading rather than freezing a copy is what makes two cases correct that a copy gets
wrong:

- **Revoking twice** writes one row, not two. The second read finds nothing, and a
  history of holds that never existed is not a smaller error than no history.
- **A grant and a revocation folded in the same batch** close the hold that was
  actually granted, because an indexed batch observes its own pending writes.

### `?at=` is the deliverable, not an extra

A list of ended holds is a pile of rows. The question an access review asks is *what
did this person hold on 3 March*, and answering it needs both halves: the ended
holds that cover the moment, and what is **still** held and began before it. A route
that offered only the first would answer "what had ended by then" while appearing to
answer the other.

The interval is half-open — a hold that ended at T did not cover T — so a right
returned and re-granted in the same nanosecond is not counted twice.

### A third actor field on the order line

`Line.ReturnedBy` joins `DecidedBy` and `AbandonedBy`, recorded when the return is
**asked for** rather than when it completes. What completes a return is a
deprovisioning process, and attributing the decision to it would name a robot where
a person decided.

## Consequences

**The family only grows.** That is correct and is the point — it is the one
structure in the portal whose purpose is to outlive every retention rule around it —
but it means there is deliberately no whole-family scan. "Every hold everybody has
ever ended" is a question with no bounded answer, and a route offering it would be a
slow way to export the estate's access history. Reads are per principal, bounded by
`HistoryReport` (2000, larger than its neighbours: every other ceiling in that group
bounds a list of *problems*, and this one grows with a person's tenure).

**The history starts today.** A revocation written before this record leaves no row,
and the fold's old arm cannot be deleted — the log is append-only and replayed
whole, so an installation upgrading into this replays years of them. Those events
drop the hold without a row, which is what they meant and all they can mean; the
fields a row needs were never written. A history that invented them would be worse
than one that starts on the day the family did.

**It is not an MCP tool.** The inventory routes are withheld because they are other
people's access. This one is withheld because of what it is *for*: an assistant that
could read it would assemble a person's whole access biography in one call, and
`?at=` reconstructs a past day, which is precisely the evidence somebody would want
before disputing it.

**What this still cannot say.** The row records what Atlas recorded. Atlas never
observed a target system except through reconciliation, so `?at=` answers what the
record said and not what was true. Where the two differ, the `corrected` flag is the
only thing standing between a reader and a confident wrong answer — and a reader who
ignores it gets the larger, more cautious set rather than the smaller, wrong one.
