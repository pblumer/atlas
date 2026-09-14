# ADR-0323: Two people can edit one decision together, and the lock is the decision

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0140](0140-live-collaborative-modeling-sessions.md) gave a BPMN draft a live
session: several editors — people, and an AI agent joined over MCP — on one draft
at once, each seeing the others' presence, selections and changes as they happen.
Its first-cut concurrency rule is a **soft per-element lock**: selecting an
element claims it, and an element somebody else holds is refused.

A decision now has a draft too ([ADR-0321](0321-decision-drafts.md)) and is edited
on a page of its own ([ADR-0320](0320-the-decision-editor-is-a-page.md)), and it
has none of this. Two people editing one decision table today are the
last-write-wins situation ADR-0140 was written to end.

The obvious move is "do the same for DMN", and it does not survive contact with
the editor. ADR-0140's rule is expressed in diagram-js elements, and **dmn-js is
not one canvas**:

- the **DRG view** is diagram-js — the same library, the same element registry.
  A decision, an input datum and a requirement are elements, and everything
  ADR-0140 does works there unchanged.
- a **decision-table view** is not. It is a grid, and the things two people would
  collide over — a rule, a cell, an input column — have no diagram-js element and
  no id the session could name.

So the question is not whether to give a decision a session. It is: **what is an
element, when half the editor has none?**

## Decision drivers

- **One session mechanism.** The registry, the SSE transport, the presence model
  and the lock semantics exist and are tested. A second, differently-shaped
  session would be a second thing to keep correct.
- **Correctness over fluidity, still.** ADR-0140 accepted that the first cut
  forbids two people typing into the same element at once. It did not accept
  silent loss, and neither does this.
- **Say what is true.** A lock that claims to protect a rule and does not is worse
  than no lock: it is a promise the editor cannot keep.
- **Design-time only.** Nothing here reaches the WAL, the processor, or recovery.

## Considered options

1. **The lock is the decision.** The DRG's elements are locked as ADR-0140 locks
   them; opening a decision's table takes the lock on *that decision*, so two
   people may edit two different decisions of one model at once but not one table
   together.
2. **Per-rule locks.** Invent ids for table rows and lock those.
3. **Presence only.** Show who else is here and what they are looking at; lock
   nothing.
4. **No session for decisions.** Leave ADR-0140 to BPMN.

## Decision outcome

Chosen option: **1 — the lock is the decision.**

It is not a weaker rule than ADR-0140's. It is *the same rule*, applied to the
element that actually exists: in a decision requirements graph, a decision **is**
one element, and its table is that element's contents. "Two people may not edit
one element at once" therefore reads, in a table view, as "two people may not edit
one decision's table at once" — which is what it already meant in the DRG.

Concretely, on a decision draft:

- the DRG view behaves exactly as a BPMN canvas does: selecting a decision or an
  input datum claims it, somebody else's is refused with a hint, presence is drawn
  on the elements;
- opening a decision's table view claims that decision, and releases it on the way
  out. A colleague who opens the same table is told who has it and gets the view
  read-only rather than a canvas that will silently lose their typing;
- a peer's saved change re-imports the draft into the live editor, viewport and
  open view preserved, exactly as ADR-0140 does for a diagram.

### What this buys and what it does not

Two people can genuinely work on one decision model at the same time — one on the
eligibility table, one on the fee calculation — which is the common case for a
model with several decisions. Two people cannot fill in one table together, and
the editor says so rather than letting them find out.

Option 2 is the one to argue with, and it is rejected for a specific reason rather
than for effort: **a DMN rule has no stable identity to lock.** `<rule id="...">`
exists in the XML, but the ids dmn-js mints are regenerated freely as rows are
added, moved and removed, and a lock on an id that the other editor's next insert
renumbers is a lock that protects the wrong row. Locking rows properly needs the
op-log ADR-0140 already names as its own future step — at which point it is worth
doing for both editors at once, not for this one alone.

Option 3 — presence with no lock — is the tempting middle, and it is the thing
ADR-0140 explicitly refused: "it is **not** acceptable that concurrent edits
silently corrupt or lose a draft, which is what the overwrite store does today."
A decision draft is the same overwrite store.

### The seam this opens in the server

The session handlers were written against *the* draft store. They are now
parameterised by a **subject**: what key the registry uses, and how a request is
authorized against the artifact behind it. The BPMN routes pass the draft store
and its project scope; the DMN routes pass the decision-draft store and its own
(ADR-0321 files a decision draft into an application exactly as a BPMN draft is).

That is extraction before addition, and it is affordable here precisely because a
session holds **no durable state**: there are no records to migrate and no stored
field to rename — the opposite of the decision-documentation case, where a sibling
package was the cheaper honest answer.

The registry key is namespaced (`dmn:` before the draft id) so a BPMN draft and a
decision draft that happen to share an id are two sessions rather than one, which
would otherwise be a silent cross-wiring nobody would look for.

### Consequences

- **Positive:** A decision is co-edited with the same mechanism, the same
  transport and the same semantics as a diagram, including by an agent over MCP.
- **Positive:** The lock means what it says. A reader of the code can state the
  rule in one sentence and it is true in both views.
- **Positive:** One session subsystem, now explicitly parameterised, so a third
  artifact with a draft costs a subject rather than a copy.
- **Negative / trade-offs accepted:** Two people cannot fill in one decision table
  at the same time. For a model with one decision that is the whole of the model,
  so co-editing it degenerates to taking turns — with the editor saying whose turn
  it is, which is the improvement over finding out afterwards.
- **Negative:** Presence in a table view is coarse: it says who is in which
  decision, not where in the table. Finer presence needs the same row identity a
  row lock would, so it waits for the same thing.
- **Follow-ups / risks to watch:** The op-log / CRDT upgrade ADR-0140 names would
  give rows identity and let both editors relax to true concurrent editing. When
  it happens it should happen for both, from one implementation.

## Pros and cons of the options

### Option 1 — the lock is the decision *(chosen)*
- Good: ADR-0140's rule, unchanged, applied to the element that exists.
- Good: the common case — a model with several decisions — is genuinely
  concurrent.
- Bad: a single-decision model serialises completely.

### Option 2 — per-rule locks
- Good: the finest granularity anybody would want.
- Bad: a DMN rule has no stable id across another editor's inserts, so the lock
  would protect the wrong row — worse than no lock, because it would be believed.
- Bad: it needs the op-log, which is a decision for both editors, not this one.

### Option 3 — presence only
- Good: cheap, and never wrong about what it promises.
- Bad: it leaves the overwrite loss ADR-0140 exists to stop.

### Option 4 — no session for decisions
- Good: nothing to build; the parity gap is only a gap.
- Bad: a decision is now a first-class artifact with a draft and an editor of its
  own, and this is the last thing on that list that a diagram has and it does not.

## Links

- extends [ADR-0140](0140-live-collaborative-modeling-sessions.md) — the session this reuses, and whose lock rule it applies
- extends [ADR-0321](0321-decision-drafts.md) — the draft a decision session is held on
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor the session runs in
- relates to [ADR-0071](0071-sharing-scopes.md) — who may join, and who may only watch
- relates to [ADR-0032](0032-modeler-ai-copilot.md) — the agent that joins as a participant
