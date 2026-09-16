# ADR-DRAFT: One decision covers a request; the engine still completes one task per line

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Open question:** Whether a collective decision should be offered across
  *orders* — one approver's whole list at once. It is refused here because the
  reason field is what makes a refusal reviewable a year later, and one sentence
  covering two people's requests is a sentence about neither. That argument is
  about the reason and not about the approval, so it would fall if a surface
  arrived that collected a reason per order while deciding several. Nothing here
  blocks that; it simply is not this measure.
- **Question checked:** 2026-09

## Context and problem statement

An approval in Atlas is one user task per order line. The approval process is
started multi-instance from the order's ready lines, so a workplace ordered as
twelve products is twelve process instances and twelve tasks.

That shape is right. A line is what gets provisioned, refused, escalated,
reassigned and returned, and every one of those needs its own instance to carry
its own state. Nothing below changes it.

What was wrong was the surface. The approver of a twelve-line workplace was asked
to press Genehmigen twelve times, read the same recipient twelve times, and — on a
refusal — type the same reason twelve times.

> As an approver I want to decide a request without deciding it twelve times.

## Decision drivers

- A person doing the same thing for the fourth time is no longer reading it. A
  surface that produces twelve identical clicks has not obtained twelve
  judgements; it has obtained one and a habit.
- The record must not claim more than happened, and must not claim less.
- Nothing may weaken who is allowed to decide.
- There is no transaction spanning twelve process instances, and there is no
  honest way to invent one.

## Considered options

1. **One task for the whole order.** Change the approval model so an order is
   approved once.
2. **A collective action over the existing tasks.** One call, one reason, N
   completions.
3. **Nothing.** The list already has search and sort; twelve clicks is twelve
   clicks.

## Decision outcome

Chosen option: **a collective action over the existing tasks** —
`POST /api/v1/approvals/decide`, naming the task keys, deciding all of one order's
open approvals the caller holds, each as its own task completion.

### Why not one task for the whole order

Because every other thing that happens to an approval happens per line. An
escalation moves one line's approval; a reassignment gives one line's approval to
somebody else; a stalled approval is one line's. A single order-level task would
have to grow all of that back, per line, inside one task's variables — which is
the per-line instance, rebuilt worse.

It also loses the case the whole measure exists beside: an approver who approves
nine lines and refuses three. With one task that is a decision the model has to
encode; with twelve it is two collective actions, or twelve single ones, and the
data model does not have to know.

### Is the record honest?

This is the objection worth stating in full, because it is the one that nearly
stopped the measure: **twelve task completions in the same second look like twelve
examinations, and only one happened.**

The objection does not hold, and it is worth being precise about why. Twelve tasks
completed in the same second, by the same person, on the same order, with the same
reason, are not *disguised* as twelve examinations — that pattern is the legible
signature of one collective decision, and a reader who finds it will read it
correctly. The alternative the objection implies is worse: twelve clicks a minute
apart, from a person who stopped reading after the third, which looks like twelve
examinations and is indistinguishable from them in the record.

A record is honest when what it shows is what happened. What happened is that one
person decided one request, and each of its lines was told.

### Why not atomicity

Because it cannot be had. The instances are independent, and a completion that has
gone through has already handed its answer to its process, which may have started
provisioning before the second completion is attempted. A rollback would have to
un-provision, which is a different act with its own approval.

So the call answers **per line**: what was decided, and what was not with the
reason for each. The page shows the unfinished ones by name. "Eleven of twelve" is
a number an approver cannot act on; "the laptop is still open because it was
decided in another tab" is.

### What it refuses

- **Keys from more than one order**, whole, with a 400. One reason cannot cover
  two requests, and deciding the first order's lines before noticing would leave
  the caller having decided something they did not mean to.
- **A refusal with no reason**, exactly as the single approval does — enforced on
  the server, because a rule only the browser knows is not a rule.
- **More than 100 keys.** Not a resource limit: the work is one store read and one
  completion per key. It is a statement about what one decision can plausibly be.
  Nobody examines two hundred lines at once.

### The gate

The listing's, not the task surface's: `holdsApproval`, which has no operator or
administrator bypass. This route serves one person's own approvals, and an
operator who has to step in does it on the task itself, where the record says an
operator did. A stranger naming keys from a screenshot decides nothing and is told
so per key — a blanket refusal would not say which key was the problem.

### The surface

Opt-in, on the decision already open, and never remembered across a selection:

- The card for the selected position gains a block naming **the rest of the
  request** — each position with its price, not a count, because the thing being
  ticked is "I have seen what is in this request" and a number is not something
  anybody can have seen.
- One checkbox. Absent when the request has one position: a checkbox offering to
  decide "all one of them" teaches people to tick boxes without reading.
- The count moves onto the **buttons**, because the button is the last thing
  somebody reads before the decision is irreversible.
- A request with one position still takes the single-task route, which is the path
  every other task surface uses. The collective route is an addition, not a
  replacement.

### Consequences

- **Positive:** the approver's story is answered without touching the process
  model; every per-line act keeps working; the partial case is visible instead of
  silent.
- **Negative / trade-offs accepted:** two client paths for one decision; no
  atomicity; a record that has to be read as one decision rather than counted as
  twelve.
- **Follow-ups / risks to watch:** the re-read before each completion closes the
  window between resolving the keys and using them, and no HTTP test can open that
  window — it is the one branch here proved by reading rather than by running.

## Pros and cons of the options

### Option 1 — one task for the whole order
- Good: one completion, one record, atomic by construction.
- Bad: rebuilds per-line escalation, reassignment and stalling inside one task's
  variables; cannot express nine yes and three no without the model knowing.

### Option 2 — a collective action
- Good: no change to the process model; every per-line act survives; partial
  results are stated.
- Bad: not atomic; two client paths; the record needs reading rather than counting.

### Option 3 — nothing
- Good: no decision to undo.
- Bad: the twelfth click is not a judgement, and a surface that produces it is
  manufacturing consent it then records as twelve approvals.

## Links

- relates to ADR-0311 — the approver's own surface, which this extends
- relates to ADR-0354 — finding one decision among forty, the other half of the
  same problem
- relates to ADR-0361 — the figure the collective block shows per
  position
