# ADR-DRAFT: Read the difference between what is built and what is planned

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Patrick Blumer
- **Open question:** whether a difference is worth handing to whatever tracks work — an
  issue, a backlog item — or whether the reading is the useful artifact and exporting it
  would only create a second place the same fact lives. This record settles the reading
  and deliberately not the export.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0301](0301-derive-the-model-from-the-processes.md) settled that Atlas holds two
statements about the same subject and must not merge them. What is derived from the
processes is what is **built**; what a person models by hand is what is **wanted**.

It also said what makes the pair worth having: *their difference is the work not yet
done*. And then it stopped, because it could not settle the shape — and because §4 named
a blocker:

> No reconciliation, no diff. Comparing the derived reading against an authored model is
> the obvious next record and is not this one: it needs a stable identity for a derived
> class across two derivations, which nothing yet provides.

That blocker is the thing to look at first, because if it holds, nothing below is
buildable.

**It does not hold — for a reading.** It is a real requirement of *reconciliation*: a
mechanism that offers individual changes to accept or reject has to remember which
derived element is the one you rejected last time, and across two derivations nothing
carries that. A stateless reading remembers nothing. It derives both sides fresh and
compares them, so there is no identity to keep across anything. The sentence was true of
option 3 as ADR-0301 first framed it, and ADR-0301 itself then said option 3 "survives in
altered form — not as a reconciliation that merges toward one model, but as a *reading of
the difference*". This record is that altered form, and the blocker belonged to the form
that was dropped.

## Decision drivers

- **A false backlog item is worse than a missing one.** The list will be read as work.
  Anything on it that is not work destroys its credibility, and the first person to find
  three inventions on it stops reading the other twelve.
- **Derivation cannot see most of a model** (ADR-0301 §2). Comparing what it cannot see
  would report derivation's own limits as though they were facts about the system.
- **Two documents, not one, and the reading must not blur them.** ADR-0301's negative
  consequence was "two drawings a reader must not confuse"; a difference laid *on* one of
  the drawings is exactly that confusion, drawn.
- **Nothing is written.** The posture of ADR-0301 §4 carries over unchanged: this is a
  read over a derivation and a vocabulary, and it costs a request and no storage.
- **The names are already the mechanism.** `itemSubjectRef` resolves a data object's type
  against a class *by name*; `NewVocabulary` indexes classes by name; a lifecycle state's
  name **is** its identity, because it is the string every process writes (ADR-0259). The
  comparison does not need a new key, and inventing one would be a second identity for
  things that already have one.

## Considered options

1. **A reading of the difference, grouped by class, in both directions (chosen).**
2. **The derived drawing, marked up** — the authored model's absences drawn onto the
   picture of what is built.
3. **A deploy warning.** Report the difference the way `data.unreachable-state` reports
   its one instance of it.
4. **Export to whatever tracks work.** Turn each difference into an issue.

## Decision outcome

Chosen: **a reading of the difference**, per application, with the correspondence by
name at every level and both directions kept apart.

### 1. What corresponds to what

By name, all the way down, because that is what the engine already does:

| Level | Key | Why it is already the key |
|---|---|---|
| Class | its name | what `itemSubjectRef` resolves against (ADR-0230) |
| Member | its name | what a write path names (ADR-0060) |
| State | its name | the string a process writes; ADR-0259 makes the name the identity outright |
| Transition | the ordered pair of state names | a transition's own id is documentation; its ends are what it says |

No new identity, and nothing remembered between two readings.

### 2. The two directions, which mean different things

**Planned, not built** — in the model, nothing in the processes. This is the backlog, and
it is the direction the pair exists for. A class declaring `cancelled` that no process
ever writes is not a defect; it is a decision taken and not yet implemented.
`data.unreachable-state` (ADR-0259) already reports exactly one case of this from the
other side, and this reading generalises it.

**Built, not described** — in the processes, nothing in the model. The model is behind
reality. Usually that means write it down; occasionally it means a process is doing
something nobody agreed to, which is the more interesting reading and the reason this
direction is not dropped as mere bookkeeping.

They are never mixed into one list of "differences". A reader acts on them differently,
so they are counted, grouped and labelled separately.

### 3. What is deliberately not compared

This is the half that decides whether the list can be trusted, and it follows directly
from ADR-0301 §2 — derivation cannot see these, so a difference in them is a fact about
derivation and not about the system:

- **The business key.** Every derived class is keyless by construction. Comparing it
  would put "missing business key" on every class, for ever.
- **Attribute types and multiplicity.** A FEEL expression's result type is not a static
  fact, so a derived member is untyped and unbounded.
- **Which states are final.** "Nothing leaves it" is intent; the graph shows only what no
  process happens to do next.
- **Associations** beyond the containment a dotted path implies, and **documentation**,
  which derivation can never produce.

The reading says this where it lists, not in a footnote: a reader who does not know what
was excluded cannot tell a short list from a clean bill.

An application that models **nothing** produces no findings at all, rather than every
derived class reported as undescribed. Nothing has been planned, so nothing is missing
from the plan — and a first-time user would otherwise meet a wall of findings that are
only the absence of a document they have not started.

### 4. Where it is shown

Beside the derived reading, on its own route, as a **list grouped by class**. Not marked
onto either drawing: ADR-0301's own negative consequence is that the two pictures must
not be confused, and painting one with the other's absences is that confusion made
visual. A list can say *which document* each row is about, which is the thing a drawing
cannot.

### 5. What is deliberately not done

- **Nothing is written**, to either model. Same as ADR-0301 §4.
- **No acceptance, no rejection, no dismissal.** The moment a difference can be ticked
  off, the reading needs to remember which — and that is the identity problem, back, and
  earned this time. If dismissal is ever wanted, it is its own record.
- **No export.** Whether a difference should become an issue is the open question above.
- **No deploy warning.** Option 3 is refused on ADR-0301's ground rather than on cost: a
  deploy refuses or warns about *this deploy*, and "the plan is ahead of the build" is
  not a statement about a deploy. `data.unreachable-state` is the one case that genuinely
  is one, because an unreachable state makes that model unsatisfiable by these processes.

### Consequences

- **Positive:** the pair finally answers the question it was built to answer. Until now a
  person had to hold both pictures in their head and spot the gap.
- **Positive:** it makes modelling ahead of implementation *safe to do*, which is what
  ADR-0259 and ADR-0301 both say is normal practice. The plan stops looking like drift.
- **Negative:** a third reading of the same subject, and the burden of saying plainly
  which of the three any screen is. Mitigated by it being a list rather than a fourth
  drawing.
- **Negative:** the exclusions in §3 mean the reading is quiet about real gaps — a class
  whose business key is wrong is a real problem this will never report. It says so.

## Pros and cons of the options

### Option 1 — a reading of the difference
- Good: stateless, so no identity to keep; both directions stay distinct; writes nothing.
- Bad: a third surface; silent on everything §3 excludes.

### Option 2 — the derived drawing, marked up
- Good: one picture instead of a picture and a list.
- Bad: it draws the authored model's absences onto the picture of what is built, which is
  the exact confusion ADR-0301 names as the cost of having two pictures.

### Option 3 — a deploy warning
- Good: it reaches somebody at a moment they are already paying attention.
- Bad: a deploy is about that deploy. Reporting the whole backlog there makes the one
  finding that *is* about it (`data.unreachable-state`) harder to see, not easier.

### Option 4 — export to whatever tracks work
- Good: the difference becomes work somebody schedules.
- Bad: a second place the same fact lives, and it needs the dismissal state §5 refuses.
  It is the open question, deliberately left open.

## Links

- completes [ADR-0301](0301-derive-the-model-from-the-processes.md) — which named this as
  the follow-up and left the shape open
- generalises [ADR-0259](0259-data-object-lifecycle.md)'s `data.unreachable-state`, the
  one instance of "planned, not built" that already existed
- keys on [ADR-0230](0230-process-information-model.md)'s name resolution, which is why
  no new identity is needed
