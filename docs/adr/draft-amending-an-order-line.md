# ADR-DRAFT: A position can be withdrawn on its own and its details corrected; what is held is never changed in place

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a correction made while a position is still waiting
  should reach a fulfilment process that has already read the order. Today it
  cannot have: a line is read when its turn comes, and a line that has not had its
  turn has not been read. That holds because fulfilment is driven by waves and the
  order is the single record it reads — and it stops holding the day a model reads
  the whole order up front and caches it. There is no such model today, and the
  failure would be silent: the process would act on figures the record no longer
  shows. It stays a stated assumption rather than a mechanism until a model needs
  one.
- **Question checked:** 2026-09

## Context and problem statement

The story asks to **modify or delete positions directly**. Neither was possible in
the way it means.

**Deleting existed only for a whole order.** `CancelOrder` takes back everything
that has not happened yet, and there was no per-position form of it — although the
pure transition for one line has been in the package since it was written, with
nothing calling it. Somebody who no longer wanted the second screen had to take the
laptop back with it.

**Modifying did not exist at all.** No route changed anything about a placed line.

## Decision drivers

- The access record has to answer what somebody had and when. Anything that makes
  that unanswerable is not available, whatever it costs in convenience.
- A rule enforced in one screen and not another is not a rule.
- The order's status machine already decides what may still change. A second set of
  rules beside it would disagree with it eventually.
- A correction that cannot be distinguished from the truth is worse than no
  correction.

## Considered options

1. **One "edit position" operation** that changes whatever the caller sends.
2. **Two operations**, separating what is held from what is recorded about it.
3. **Nothing**: cancel the order and place another.

## Decision outcome

Chosen option: **"two operations"**, because "modify" is two different acts and
treating them as one is how a record starts lying.

### Changing what is held is not offered

Another product, another variant — that is a different claim about the past. A line
that was provisioned and then quietly became a different product leaves the access
record unable to answer the one question it exists for. The path already exists and
is the honest one: give it back, order the other thing, and the record carries both,
with the dates that make it readable.

This is the same distinction `cancel.go` and `return.go` already draw between
themselves: *a cancellation is a line that never was; a return is a line that was
and is no longer, and a record that cannot tell the two apart cannot answer who had
access when.*

### Correcting what was recorded about it is offered, and the status machine says how

The configuration answers a product asked for
(ADR-0358) are not access. A cost centre is a booking,
and an access review does not ask about it. What a correction may do depends on
where the line stands, and that is asked of the existing statuses rather than
decided again:

| Where the line stands | What a correction does |
| --- | --- |
| `pending`, `blocked` (`Cancellable()`) | Replaces the answers. **No amendment is recorded** — nothing was attempted, so there is no delivery the old answers were ever true of, and recording one would tell a reader that something had been. |
| `done`, `returning`, `returnFailed` (`Held()`) | Replaces the answers **and records the correction**: what they said before, who corrected them, when, and optionally why. |
| `running` | Refused, and the refusal says to wait. |
| `rejected`, `cancelled`, `abandoned` | Refused: closed records of requests that produced nothing. |

**Why a held line records rather than overwrites.** The laptop is at the wrong site
and correcting the record does not move it. An overwrite would leave the order
saying something that was never true of the delivery, and a reader could not tell
the corrected record from an accurate one. Recording the correction *as* a
correction is more true than either version alone: it says what was ordered, what it
should have been, who said so and when. The amendments are a list and not a slot,
because details having been wrong twice is a different fact from their having been
wrong once.

**Why a running line is refused.** A provisioning process has the line, which is a
conversation with a system this server does not control. Changing the answers
underneath it would leave the record saying one thing and the target system having
been told another, with nothing anywhere saying which the delivery followed. That is
the same reason `Cancellable()` excludes `running`, asked of a different act.

**What is deliberately not re-decided here** is whether a field is required. That is
the form's own statement, checked by the form runtime, exactly as when the order was
placed.

### A position its whole always carries cannot be withdrawn on its own

The basket will not let anybody deselect an integral part — a workplace is not a
workplace without its account — and a rule enforced when ordering and not afterwards
is not a rule. One API call would otherwise undo what the portal refuses to offer.

The order could not tell: it carries the precedence graph and not the composition
one. So the line now carries `Integral` and `Includes`, frozen at placement like
every other statement about the release it was placed against. The refusal names
what carries the part, because the answer somebody needs is "take back the workplace
instead", not "no".

### Consequences

- **Positive:** the story is answered without any way to rewrite what somebody held;
  a correction after delivery is legible as a correction; the portal and the server
  agree about what may be withdrawn, held by a test that reads the statuses out of
  the Go constants.
- **Negative / trade-offs accepted:** correcting a cost centre on a delivered laptop
  does not move the laptop, and nothing here pretends it does — a second act, in the
  world, is still somebody's. Two new fields on the line exist only to make one
  refusal possible.
- **Follow-ups / risks to watch:** the open question above, and the approval page:
  an approver sees a position's details but not that they were corrected. That is a
  display gap rather than a record gap — the amendments are on the order — and it is
  the next thing to close if approvers start asking.

## Pros and cons of the options

### Option 1 — one "edit position" operation
- Good: one route, one mental model, the thing most people would ask for.
- Bad: it makes "what did this person have, and when" unanswerable the first time
  somebody edits a provisioned line. The convenience is real and the cost is the
  record's whole purpose.

### Option 2 — two operations
- Good: nothing can rewrite what was held; a correction after delivery is legible as
  one; the rules come from the status machine that already exists.
- Bad: "modify" answers differently depending on where the line stands, which has to
  be explained once.

### Option 3 — nothing
- Good: no new surface, no new risk.
- Bad: a mistyped cost centre would be fixed by deprovisioning and reprovisioning a
  laptop, which is absurd in the ordinary case and impossible in the common one.

## Links

- relates to ADR-0358 — the answers this corrects
- relates to ADR-0312 — the three models, and why the order is the record
- relates to ADR-0346 — the entitlement history, which is what survives an order's
  deletion and is deliberately untouched by a correction
