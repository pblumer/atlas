# ADR-DRAFT: A problem is an aggregation over incidents, not a BPMN element

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether a problem may span nodes — whether one cause showing up as
  breaches in two domains can be recognised as a single problem. Doing so means reading
  another domain's incidents, which is the deferred **observe** grant of ADR-0373 in a
  new guise. The per-node problem decided below is right while that grant does not
  exist, and would be re-argued the day it does.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0061 gave Atlas an incident: a named, repairable failure attached to an element
instance. ADR-0337 bounded them, because volume is itself a failure mode — a hundred
instances failing on one cause produce a hundred incidents and an unreadable list.
ADR-0373 then added a source that can produce them from outside: a breach of a
commitment, rate-limited and aggregated per interface and per peer.

Aggregating for **suppression** and aggregating for **diagnosis** are not the same
thing, and Atlas only does the first. After ADR-0337 has collapsed a flood, what
survives is a shorter list — not the fact that those hundred failures were one cause
with one owner and one fix. The grouping is computed, used to keep a screen readable,
and discarded.

ITIL names the missing half. An **incident** is "restore the service"; a **problem** is
"find and remove the cause", with a lifecycle of its own that outlives any single
incident. Atlas has the first and not the second, and the gap shows exactly where a
flood is: the operator can see that something is wrong a hundred times, and has nowhere
to record that it is wrong *once*.

There is a tempting wrong turn here, and it is worth naming because it is the first
thing asked: that BPMN has no notation for a problem. Neither does it have one for an
incident. An incident is a record type in the operations model, not a symbol on a
canvas, and a problem belongs in the same place.

## Decision drivers

- **One grouping, not two.** ADR-0337 already computes what belongs together. A second
  grouping rule would drift from it, and the two would disagree on the screen.
- **A problem must be actionable.** Without an owner and a lifecycle it is a label on a
  list, which is the thing operators already have.
- **Do not put it in the log.** An incident is a fact about one instance's execution. A
  problem is a judgement spanning instances and definitions, and is not any instance's
  history (I6).
- **Atlas runs processes.** Whatever handling a problem needs, the engine is already the
  thing that executes handling. Building a second, hard-coded workflow inside the server
  would be the one mistake this project is least excused for.

## Considered options

1. **No problem record** — keep incidents plus ADR-0337 grouping and let operators
   carry the causal link in their heads or in a ticket system.
2. **A problem record in the operations model**, opened from a recurring incident group,
   with its own lifecycle, and with its handling optionally driven by a deployed BPMN
   process.
3. **A BPMN element or notation for problems**, so a modeller can draw one.

## Decision outcome

Chosen option: **2 — a problem record in the operations model.**

### What a problem is

A design-time record — not an event in the log — holding:

- the **grouping key** ADR-0337 already computes, which is what makes it *this* problem
  and not another;
- the **member incidents**, by reference, with first seen, last seen and a count;
- a **state**: `open` → `known error` (the cause is identified and a workaround is
  stated) → `resolved`. ITIL's words, because operators already have them and inventing
  synonyms would buy nothing;
- an **owner**, which is a person and never a default.

It is opened automatically when a group crosses a threshold, and **closed only by a
person**. That asymmetry is deliberate: a machine can see that something recurs, and
cannot see that the cause is gone.

### It lives beside the engine, not inside the log

A problem is stored on a sidecar (`sidecar.NewStore`, ADR-0282) and served by its own
service package (ADR-0147), the same category as connectors (ADR-0041) and call-target
overrides (ADR-0105). It is never written to the WAL and never rebuilt by `applyToState`
(I4/I6).

The reason is not convenience. An incident is a fact about one process instance and
belongs to that instance's history. A problem is an assertion *about* a set of
incidents — a judgement that they share a cause — and a judgement that a later reading
may revise is not a fact the log should be forced to carry forever.

### Its handling is a process, because Atlas runs processes

A problem may name a deployed process, started when the problem is opened and seeded
with the problem's key and its member incidents. Escalation, notification, approval of a
workaround and the review that closes it are then **modelled**, in the same Modeler, by
the same people, and visible in the same Operations view as everything else.

This is the part no other engine gets cheaply, and it is the reason option 3 is not
merely unnecessary but backwards: the answer to "there is no BPMN element for a problem"
is that the *handling* of a problem is an entire BPMN process, which is a great deal
more than an element would have given.

Naming such a process is optional. A problem with none is still a record an operator
works by hand.

### Consequences

- **Positive:** a flood stops being only noise to suppress and becomes a thing with an
  owner and a state. ADR-0337's grouping gains a second, better use without changing.
  No new runtime concept, no new recovery path, and the handling is authored rather than
  coded.
- **Negative / trade-offs accepted:** a second lifecycle for operators to learn beside
  the incident's. A problem opened automatically and closed only by a person will
  accumulate if nobody works the list — a new kind of noise, one level up, and the
  honest answer is that it needs its own retention rather than that it will not happen.
  The sidecar also means a problem does not survive a restore of the design-time backup
  onto another installation the way a definition does (ADR-0357), which is correct — a
  judgement about one installation's incidents does not travel — but it will surprise
  somebody.
- **Follow-ups / risks to watch:** the threshold at which a group becomes a problem, and
  whether it is one number or per interface. Retention for resolved problems, shared with
  the concerns of ADR-0017 and ADR-0022. And whether a problem should be able to cite a
  *deployment* or a *release* (ADR-0128) as its cause, which is the shape most real
  problem records take and which this record deliberately does not decide.

## Pros and cons of the options

### Option 1 — no problem record
- Good: nothing to build; ADR-0337 already keeps the list readable.
- Bad: the causal link lives nowhere. The flood is suppressed and the knowledge that
  produced the suppression is thrown away, so the next occurrence is diagnosed from
  scratch. It also leaves ADR-0373's breaches with no destination beyond a rate limit.

### Option 2 — a problem record in the operations model (chosen)
- Good: reuses ADR-0337's grouping, the sidecar shape and the incident model; adds no
  runtime concept; the handling is a process rather than server code.
- Bad: a second lifecycle; auto-opened problems accumulate without retention; the record
  is per installation and does not travel.

### Option 3 — a BPMN element for problems
- Good: it is what a modeller asks for out loud.
- Bad: a category error. An incident is not a BPMN element either. It would put an
  operations record type into the execution notation, where it has no token semantics,
  nothing for the compiler to do with it, and no reason to be in a process model at all.

## Links

- builds on ADR-0061 (incident model) and ADR-0337 (incident floods — the grouping)
- receives the breaches of ADR-0373 (a commitment on a published entry point)
- stored per ADR-0282 (store registry) and served per ADR-0147 (a service, not more
  `Server` methods); sidecar category of ADR-0041 / ADR-0105
- the travel caveat relates to ADR-0357 (a portable backup and installation identity)
- honors the invariants in docs/architecture/invariants.md (I4, I6)
