# ADR-0408: The portal's order rows carry one link into a process, not three

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-22
- **Deciders:** Atlas maintainers

## Context and problem statement

The portal's orders table carried three ways into a process. On the order row, a
link to the fulfilment orchestration ([ADR-0384](0384-order-position-key.md)). On
every position row, two more: the position's own instance, and "Wo steht das?" —
the step that position is sitting on, answered by the portal route
[ADR-0390](0390-position-progress.md) built for exactly this.

Asked for by the people the page is for: delete the two on the position rows. They
are not useful there. What they read instead is the status beside the position's
name — "Wartet", "Provisioniert", "Zurückgezogen" — which comes out of the order's
own record and is on the screen already.

The third was reported as not working. It works while the orchestration is running,
and both ways it can fail put their answer in `state.error`, which the page paints
above the table: a reader who pressed a button in a row further down sees the page
do nothing at all. One of those two failures, an instance the exported event log
answered for, was not reported at all — it was followed, into a console replay view
with nothing to replay.

## Decision drivers

- What a page says in answer to a press belongs where the press was.
- A link is followed only where there is something to follow it to.
- A route is API surface, and the portal is one of its callers, not its purpose.

## Considered options

1. **Keep the position links and explain them better.** The ask was not that they
   are unclear; it was that the question they answer is already answered one
   column over.
2. **Delete the position links and the route behind the progress one.** The route
   is gated on owning the order and is reachable by any caller; deleting it
   because this page stopped drawing a button for it confuses a UI decision with
   an API one.
3. **Delete the position links, keep the routes, and repair the remaining link.**

## Decision outcome

Chosen option: **3**.

- The position rows keep what acts on a position — withdrawing it, correcting its
  details ([ADR-0359](0359-amending-an-order-line.md)) — and offer no way of
  looking at a process.
- `GET /api/v1/portal/orders/{id}/lines/{position}/progress` is unchanged and
  untouched. So is the instance search.
- The order's link answers beside its own row. The one outcome that is not drawn
  there is the one that navigates away.
- An archived hit is said, not followed. The instance search falls back to the
  exported event log when this server's own index has nothing
  ([ADR-0115](0115-history-retention-hard-delete.md)) and marks what it answers with; a row so
  marked describes an instance this server does not hold, and it gets words of its
  own rather than the three-cause message written for an absence nobody can
  explain.

## Consequences

- ADR-0390's consequence — "a reader with no role beyond `user` sees which step
  their own position is on" — no longer holds through the portal. The route that
  answers it does, and so does every caller of it that is not this page. That is
  the half of ADR-0390 this record retires, and the reason it exists rather than
  an edit: a record that still claims a button somebody will go looking for sends
  them looking for a defect.
- The orders table is shorter by two links per position, which is the visible part
  of the ask and the part worth naming: a table of four positions lost eight
  controls.
