# ADR-0380: A commitment is checked against what the process actually does

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0373 lets a published entry point state a **commitment**: a duration within which an
accepted message will be correlated. Its *Negative* section says what happens when the
duration is wrong — *"a publisher who states a duration it cannot hold manufactures
incidents for its own domain, and the honest first move is to state none"* — and that is
where the matter currently rests.

That is advice, not a mechanism, and advice of this shape has a known lifetime: it holds
until a consumer asks "how fast?" and somebody types a number into a form. The number is
then authoritative, nobody has compared it to anything, and the first evidence that it
was wrong is a stream of breaches in the publisher's own incident list — the failure the
commitment was introduced to prevent, now caused by the commitment.

The data to prevent it already exists, and this is what makes the gap worth closing
rather than merely worth noting. Detecting a breach at all requires measuring the
acceptance-to-correlation span (ADR-0373); Atlas additionally keeps per-definition
runtime aggregates (ADR-0080) and a per-element visit history (ADR-0022). Nothing new
has to be measured. The question is only whether anybody looks before the promise is
made.

## Decision drivers

- **A promise is not a measurement.** A publisher may legitimately commit to something
  it intends to improve toward. Whatever is built must not make that impossible.
- **No second measurement path.** The breach detector's span is the span. A separate
  statistic computed a second way would disagree with the thing it is supposed to
  predict.
- **Say what could not be known.** An entry point that has never run has no history, and
  a check that passes silently on no data is worse than no check, because it reads as
  confirmation (ADR-0189).
- **Design-time, off the loop.** Publishing is not on the hot path and must not take the
  single writer with it (ADR-0239, I3).

## Considered options

1. **Keep the advice.** Document the risk and rely on the publisher.
2. **Warn at publish time** when the stated duration sits below the observed
   distribution for that entry point, naming the observed value.
3. **Refuse to publish** a commitment the history does not support.

## Decision outcome

Chosen option: **2 — warn at publish time, and never refuse.**

### What is compared

At publish, the stated duration is compared against the observed acceptance-to-
correlation distribution for the element behind that entry point, over a bounded recent
window. A stated duration below the configured percentile of that distribution produces
a **warning** that names both numbers — what was promised, and what was measured.

The percentile and the window are **operator configuration, not constants**. A domain
running ten instances a day and one running ten thousand do not have the same idea of
what a tail is, and a constant would be wrong for one of them by construction.

### Never a refusal

A commitment is a target that a domain undertakes to meet, not a description of what it
already does. A publisher may state one it does not yet hold and then work toward it,
and refusing that would make the mechanism useless for exactly the case that matters.

The weaker practical argument points the same way: a refusal would make an interface
unpublishable on a bad week, which turns a quality tool into an outage.

### No history is not a pass

An entry point that has never run, or whose window holds too few samples to say anything,
produces **"unverified"** — stated as its own outcome, never as success. The rule is
ADR-0189's, and it is the part that makes this check honest rather than decorative: a
green result that means "nobody looked" is indistinguishable from one that means "this is
fine", and the difference is the whole point of running it.

### Where it runs

The check is a design-time step on the publish path, resolving state on the run loop and
computing off it (ADR-0239). It changes no runtime behaviour and adds nothing to the hot
path (I1, I5). It is a property of the publish request's *response*, so a client that
ignores warnings still publishes — the warning is information, not a gate.

### Consequences

- **Positive:** closes the follow-up ADR-0373 left open, using data that has to exist
  anyway. A publisher learns what it is promising at the moment it promises it, rather
  than from its own incident list a week later. "Unverified" makes the absence of
  evidence visible instead of silently favourable.
- **Negative / trade-offs accepted:** a warning nobody reads changes nothing, and this
  record deliberately builds a warning rather than a gate — so the failure mode it
  prevents is the *uninformed* promise, not the *reckless* one. The percentile becomes
  another operator knob with no obviously right default. And the observed distribution is
  history: an entry point whose process was just rewritten is measured against the
  behaviour of the old one, which the warning cannot know and must not pretend to.
- **Follow-ups / risks to watch:** whether the distribution should be read from this node
  only or from every node running the definition — a publisher can already read its own
  deployment targets (ADR-0129), so the question is whether it *should*, not whether it
  can. Whether a commitment that has been breached repeatedly should be re-surfaced to
  its publisher as a suggestion to restate it, which is the same comparison run in the
  other direction and probably belongs with the problem record. And whether the warning
  should also fire when a *deployment* changes under an unchanged commitment, which is
  the more common way a promise silently stops being true.

## Pros and cons of the options

### Option 1 — keep the advice
- Good: nothing to build; the record already states the risk plainly.
- Bad: it is the state of affairs that produces the failure. The advice is read once, by
  the person who writes the record, and never by the person who types the number.

### Option 2 — warn at publish time (chosen)
- Good: uses measurement that must exist anyway; informs without constraining; the
  "unverified" outcome keeps a missing answer from reading as a good one.
- Bad: a warning can be ignored; the percentile is a knob; history describes the old
  deployment.

### Option 3 — refuse to publish
- Good: the promise and the measurement can never disagree.
- Bad: forbids committing to an improvement, which is the normal reason to state a target
  at all, and turns a slow week into an inability to publish. It also puts an operational
  statistic in the position of vetoing a business decision.

## Links

- closes a follow-up of ADR-0373 (a process publishes an interface, and what it promises)
- reads the span ADR-0373's breach detection already measures; related aggregates in
  ADR-0080 and ADR-0022
- takes its "unverified is not success" rule from ADR-0189
- runs off the loop per ADR-0239; honors I1, I3, I5
