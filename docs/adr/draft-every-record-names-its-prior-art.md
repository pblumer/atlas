# ADR-DRAFT: Every record names what it looked at, and `none` says why

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Prior art:** Rust's RFC process requires a *Prior art* section, added by its own
  RFC 2333, and two of its conclusions are adopted here: that naming another system
  is worth far less than saying what its community experienced with it, and that "no
  prior art" is a perfectly good answer. MADR, the format this directory follows, has
  no such field — its supplemental parts are decision drivers, considered options and
  *More information* — so this is an Atlas addition rather than a format feature.

## Context and problem statement

[ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) measured the
catalogue against the standards that cover the same ground, and found that the
question had never been put. The evidence was checkable rather than
impressionistic: `grep -ci standard` on
[ADR-0312](0312-portal-catalogue-order-inventory.md) returns zero across its 767
lines, its considered options are "one model / two models / three models", and no
record in the complex cites
[ADR-0176](0176-standards-boundary-and-runtime-contract.md) — the record written to
place exactly that kind of question.

What made the omission invisible is that the answers were largely right. TM Forum
publishes the same catalogue/order/inventory split as TMF620/622/637, so ADR-0312
reached a correct conclusion and left the corroboration unclaimed. **A record that
names no prior art cannot be told apart from one that examined it and refused it,
and the refusal is the half a reviewer, a customer or the next maintainer can
check.** That is the defect, and it is a process defect rather than a modelling one:
nothing in `template.md`, in `README.md` or in the guard tests ever asked the
question, so for 387 records nobody was reminded to.

ADR-0387 named this line as a follow-up, in one sentence, in its *Follow-ups* list.
**Eight records were written after it — several of them catalogue records — and not
one named prior art.** A grep for `TMF`, `standard`, `prior art` or `open source`
across ADR-0390 through ADR-0397 finds a single incidental hit. That is the whole
argument for this record: a conclusion written into a document changes nothing about
what the next author does.

This repository has learned that lesson once already, and wrote it down in
`state_test.go`: 115 records read `Proposed` while their code had shipped, an audit
in August named 28 of them, and two weeks later not one had moved — "which is the
evidence that this is a missing mechanism rather than a missing afternoon"
([ADR-0298](0298-two-states-for-a-record.md)). The fix then was not more discipline.
It was two fields and a test between them.

## Decision drivers

- A record that does not name prior art is indistinguishable from one that examined
  it. The value is in the stated refusal, not in the search.
- A document is not a mechanism. ADR-0387 and the eight records after it are the
  local proof; ADR-0298's audit is the earlier one.
- The rule must not become ritual. Most records decide an internal detail, and no
  standard covers where a button sits — so the honest negative answer has to be
  cheap and legitimate, or authors will write something false to satisfy a guard.
- The 397 records written before the rule must not be retrofitted. Answering the
  question for each of them now would mean answering it from memory, which is the
  failure this line exists to prevent.
- A guard may only assert what it can see. A test that claimed to judge the quality
  of a search would buy the same false comfort as a coverage number with no
  assertions behind it — which `AGENTS.md` already refuses by name.

## Considered options

1. **An advisory line in the template**, with no guard.
2. **A required *Prior art* section in the body**, as Rust's RFC template has it.
3. **A required front-matter line, guarded from a cutoff**, where `none` is allowed
   and has to state why.
4. **Nothing further.** ADR-0387's follow-up sentence is the record of intent.

## Decision outcome

Chosen option: **3 — a required front-matter line, guarded from a cutoff.**

Option 1 is already refuted empirically, which is unusual for an option in a record
here and worth stating plainly: ADR-0387 *was* the advisory version. It stated the
problem, named the remedy and reached every author through the repository's own
documentation. Eight records followed, and the behaviour did not change. There is no
version of "we will remember" that this repository has not already tried.

Option 2 is the better-known shape — it is where the idea comes from — and it loses
on what a guard can check. A heading is trivially satisfied by an empty heading, so a
body section can only be enforced as "a line of prose exists under it", which is
weaker than it looks. Front matter is where this directory already keeps the states a
machine reads: `Status`, `Implementation`, and the `Open question` / `Question
checked` pair ([ADR-0293](0293-open-questions-in-records-expire.md)). A fourth field
sits beside them rather than inventing a second place to look. A record with more to
say says it in the body, as ADR-0387 does — the line is a claim, not a word limit.

Option 4 is Option 1 with the pretence removed.

### What the line says

```
- **Prior art:** TMF620/622/637 carry the same catalogue/order/inventory split;
  mapped in docs/comparisons/catalogue-standards.md. Syncope and midPoint refused
  as runtimes on ADR-0011.
```

or, for the many records that decide something no outside work touches:

```
- **Prior art:** none — this decides how one index is keyed inside the state store.
```

`none` is a first-class answer. Rust's own meta-RFC says so ("if there is no prior
art, that is fine"), and it is true of most records here. What is not allowed is a
bare `none`, because a bare `none` reads exactly like a blank — the state this record
exists to end.

### What the guard checks, and what it does not

`priorart_test.go` asserts two things: that the line is present, and that a `none`
carries a reason after it. It asserts nothing about the answer's quality, and the
package documentation says so rather than implying otherwise. No test can see that a
search was cursory, and Rust's meta-RFC makes the same point from the other side —
name-dropping a system is worth much less than saying what happened to the people who
used it. That half of the work belongs to review, which is where it was always going
to live.

This is deliberately a low ceiling. The guard cannot make anybody think; it can only
make the absence of thought visible, which is a smaller claim and an achievable one.

### The cutoff

The rule binds every draft — a draft lands above any cutoff — and every numbered
record above **ADR-0397**, the highest number written before it. The constant lives
in `number.go` as `lastRecordWithoutPriorArt` with that reasoning attached.

A number rather than a date, because the date in a record's front matter is
self-declared while its number is assigned on `main` by `make adr-number`
([ADR-0170](0170-adr-numbers-assigned-at-merge.md)) and cannot be backdated to slip
under a threshold.

### Consequences

- **Positive:** the question is put once per record, by the process rather than by
  whoever happens to remember. A reviewer can see the answer without asking for it.
- **Positive:** the negative answer becomes evidence. "None applies, because this is
  an internal storage detail" is a fact a later reader can weigh; a blank is not.
- **Negative / trade-offs accepted:** one more required line on every record,
  including the majority for which the honest answer is `none`. Some of those will be
  written as reflex, and the guard cannot tell a considered `none` from a lazy one.
  That is the accepted cost of a rule a machine can hold.
- **Negative:** the 397 existing records stay silent on the question. The directory
  is therefore inconsistent by design, and the cutoff constant is where that is
  admitted.
- **Follow-ups / risks to watch:** after twenty or so records under the rule, read
  the lines and see whether they carry substance or have become ritual — the share of
  bare-ish `none` answers against ones naming something specific is the measure, and
  the answer decides whether the rule earns its keep or wants sharpening. Tracked in
  #1030 with the rest of ADR-0387's follow-ups.

## Pros and cons of the options

### An advisory line in the template
- Good: costs nothing, annoys nobody, leaves judgement entirely with the author.
- Bad: measured, and it does not work. ADR-0387 was this option; eight records
  followed it and none named prior art.

### A required section in the body
- Good: the established shape, with room for a real discussion rather than a claim.
- Bad: a heading is satisfied by an empty heading, so the guard is weak where it
  matters; and it puts a machine-read state somewhere this directory does not keep
  machine-read states.

### A required front-matter line, guarded from a cutoff
- Good: sits beside the states already guarded there; a cheap honest negative;
  enforceable without retrofitting 397 records.
- Bad: one line of ceremony on every record forever, and it can only check form.

### Nothing further
- Good: no work, no ceremony.
- Bad: leaves the next complex to repeat the catalogue's path, with the record that
  diagnosed it already on `main` and already ignored eight times.

## Links

- follows up [ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) — which
  diagnosed the gap and named this line as a follow-up
- applies [ADR-0176](0176-standards-boundary-and-runtime-contract.md) — the boundary a
  prior-art line makes somebody look at
- follows the shape of [ADR-0293](0293-open-questions-in-records-expire.md) and
  [ADR-0298](0298-two-states-for-a-record.md) — front-matter state a guard test holds,
  and the evidence that a mechanism beats an intention
- constrained by [ADR-0170](0170-adr-numbers-assigned-at-merge.md) — why the cutoff is
  a number and not a date
- built test-first per [ADR-0018](0018-test-driven-development.md)
- the diagnosis it enforces: [`docs/comparisons/catalogue-standards.md`](../comparisons/catalogue-standards.md)
