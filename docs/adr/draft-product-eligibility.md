# ADR-DRAFT: A product may narrow its catalogue's audience, because a person only ever sees one catalogue

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a restricted product should be marked in the catalogue
  listing. It is deliberately not hidden — the browser is the *orderer* and the
  restriction is about the *recipient*, so hiding by the viewer's own groups would
  hide exactly the products a manager orders for other people. But a person who
  browses their own shop and cannot tell what they may actually receive learns it
  from a refusal, which is a worse place to learn it. Marking needs a second
  question answered first: marked for whom, when the screen has one viewer and an
  order has two people.
- **Question checked:** 2026-09

## Context and problem statement

A catalogue carries an audience — the group ids whose members may order from it —
and `Catalog.ReachedBy` is fail-closed on purpose: a catalogue with no groups
reaches nobody, because the dangerous default is the one where a half-filled shop
is visible to everybody.

That gate is the **only** one. Whoever is in a catalogue's audience may order
anything in it, and the only thing standing between a person and domain
administration is an approval rule — which says *who decides*, not *who may ask*.

### Why "put it in a stricter catalogue" is not the answer

It is the obvious workaround and it does not work, for a reason written into the
design: **one catalogue per person** (ADR-0312 decision 11). `Resolve` returns the
highest-ranked catalogue a person's groups reach, and exactly one. A second,
stricter catalogue therefore does not restrict a product — it *hides* it behind the
shop that person already has, or replaces their shop wholesale if it outranks it.

So a product available to part of a catalogue's audience could not be expressed at
all. The only way to express it was to duplicate the entire catalogue per audience,
which multiplies releases, approval rules and process bindings — and splits an
estate the conflict check (ADR-draft-conflicting-rights) deliberately merges
across catalogues, because a person holds products rather than catalogues.

### Why an approval rule is not a substitute

Leaving it to approval means a thousand people may request domain administration
and one person must say no nine hundred and ninety-nine times. A line manager's
attention is the scarcest resource the portal spends — the recertification record
is built around that — and spending it to enforce a rule that could be stated once
is the trade backwards. It is the same argument the conflict check makes about
refusing at placement rather than detecting afterwards.

## Decision

`Item.Eligible` names the group ids whose members may **receive** the product. It
travels into the release like the ceiling and the approval rule beside it, so a
restriction relaxed next week cannot retroact on an order placed this week, and one
*added* next week cannot invalidate an order already approved.

### Empty means no narrowing, and that is not fail-open

The catalogue's audience is still the gate and it is still fail-closed. This only
ever narrows it, so an item naming no group inherits a restriction rather than
removing one. The comparison with a catalogue that names no groups — which reaches
nobody — is the wrong one: that is the *outer* gate, and an outer gate that
defaulted open would be a shop open to everybody.

The alternative would have made every catalogue in existence unorderable on the day
this landed, which is why the test that pins it is the first one in the file.

### The recipient, never the orderer

An order has two people. The question this answers is who may end up holding the
thing, so it is asked about the recipient. Checking the caller would refuse a
manager ordering a workplace for a new hire — the ordinary case — and would also
let an eligible manager order a restricted product *for* somebody who may not have
it, which is the same hole from the other side.

The recipient is not the caller and so carries no principal in the request. The
group ids are resolved with the same synthesis the reminder route needed
(ADR-draft-pending-work), and nothing else from it.

### A refusal names the whole when the part was never chosen

A `composition` part is integral and never deselectable. Telling somebody "you may
not receive a licence" about a licence they never chose and cannot remove leaves
them concluding the portal is broken. So the refusal names the product whose
composition carried it, and says that the whole is what is being refused.

### What publishing can and cannot check

It refuses a blank group id — that would match nobody, leaving a product orderable
by no one with nothing in the catalogue saying why.

It deliberately does **not** refuse an eligible list disjoint from the catalogue's
audience, which looks like the obvious second check. One person is in many groups
at once, and being *reached* by the catalogue through one group while being
*eligible* through another is the ordinary way this is used. A static check there
would reject correct catalogues, which is worse than accepting questionable ones.

## Consequences

**A restricted product is visible and refused rather than hidden.** See the open
question: the listing has one viewer, the order has two people, and hiding by the
viewer's groups would break ordering for others.

**403 and not 409.** A conflict is a state of the estate that giving something back
would resolve; this is a statement about who the recipient is. A caller needs to
know whether there is anything to do about it.

**It does not reach a right that already exists.** Like every other release-frozen
rule, it decides what may be *ordered*. Somebody who holds a product and later
leaves the eligible group keeps it — that is recertification's question, and
answering it here would take access away on a directory change nobody reviewed.
