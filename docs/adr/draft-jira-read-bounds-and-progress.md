# ADR-DRAFT: A Jira read is bounded and moves forward

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

An Atlas instance became unresponsive under a handful of Jira watches. Every HTTP
request that has to reach the run loop — which is nearly all of the Console — queued
behind work the Jira path was generating, while the instance kept answering `/info` and
looked alive.

Two mechanisms in the Jira reading path can consume without limit, and both were
introduced as correctness measures rather than as oversights.

**Where each of them runs matters, and the two halves differ.** A Jira *connector task*
is offloaded by default and served by a supervised worker process
([ADR-0164](0164-no-in-process-service-tasks.md)/[ADR-0218](0218-jira-default-offload.md)):
`applyOffloadedKinds` removes its handler from the engine's job runner at startup, so
the engine never runs one. The *watch* is not a job at all — it has no job type, nothing
to lease, and no worker form. It is a ticker goroutine in the server that reaches the
run loop through `s.do`, and `applyOffloadedKinds` does not touch the client registry it
reads through, so the server keeps building a Jira client (and holding the site
credential) for the watch alone. ADR-0214 named moving the reader onto the worker as a
follow-up and it has not happened. So everything below about run-loop pressure is about
the watch, and the search ceiling has to hold in both processes.

**1. The watch cursor can stop moving.** A jira watch resumes from a `created >=` /
`updated >=` clause ([ADR-0214](0214-jira-inbound-issue-watch.md)). JQL compares those
to the minute, and Jira's search index lags its writes, so the cursor is deliberately
held a **lag** (2 minutes by default) behind the newest issue a poll saw; the per-issue
mark makes the resulting re-read free. That reasoning holds at the *tip*. Behind a
**full** page it inverts: a page that filled the bridge's batch limit stopped at the
limit and not at the end of the result set, and subtracting the lag puts the next
cursor *inside the page just read*. The watch then re-reads and re-publishes the same
page on every tick and never reaches the issue behind it — for ever. Nothing about it
looks broken: the reads succeed, the publishes are real, and the engine correctly
discards every one of them against the durable high-water mark.

A bulk import or a bulk transition — a few hundred issues sharing one minute of the
cursor field — is all it takes, and `updated` watches meet it routinely.

**2. An uncapped search has no ceiling.** `maxResults="0"` means "read every match", and
the compiler writes that through verbatim (I5). The client then pages until the site
runs out, holds every result in memory, and hands the lot to the engine as **one**
process variable to encode and fsync. "Every match" is a number Jira decides and a JQL
can be wrong about it by four orders of magnitude, so a mistyped query is an
out-of-memory in the server rather than a failed task. The account search
([ADR-0223](0223-jira-account-lookup.md)) has a second edge: it answers with a bare
array, so it carries no total and no page token, and a server that ignores `startAt` is
read for ever with nothing able to notice.

## Decision drivers

- **A stuck watch is worse than a slow one.** A watch that cannot move delivers nothing
  about any issue after the point it is stuck at, and says nothing about it.
- **One bad model must not take the instance down.** Connector work is deliberately off
  the hot path (ADR-0007/0067); a failed job retries and raises an incident (ADR-0061).
  That is the failure mode a Jira read should have.
- **A model must not be told it read everything when it did not.** Silent truncation is
  a defect nobody finds twice.
- **The lag and the "0 = unbounded" contract both exist for reasons that still hold**
  and should be narrowed, not removed.

## Considered options

1. **Bound both reads: make the cursor's forward progress a rule, and give an uncapped
   read a ceiling it fails at.**
2. Truncate an uncapped read at a ceiling and return what was read.
3. Leave both; document the batch limit and `maxResults` as operator responsibilities.

## Decision outcome

Chosen option: **1**.

**The cursor.** The safety lag applies only to a page that is *not* full — which is
exactly the tip it was written for. Behind a full page the cursor lands on the newest
issue's own minute: `>=` re-reads that minute, so nothing is skipped, and the read moves.
For the residual case a minute-granular cursor cannot express — a full page whose issues
*all* share one minute, resumed from a cursor that is already that minute — the watch
steps past the minute and logs `inbound_watch.minute_overflowed`. That skips the issues
in that one minute beyond the batch limit; standing still skips every issue after it,
for ever, and says nothing.

**The search.** An uncapped read stops at a ceiling of 5000 results with an error naming
the fix (`jira.SearchCeiling`). It fails the job — retry, then an incident an operator
can see — instead of truncating, because a model that believes it read everything is the
worse outcome. A task that states its own `maxResults` is untouched.

### Consequences

- **Positive:** a watch cannot silently stop; a wrong JQL costs one incident instead of
  the instance. The account search terminates against a server that ignores `startAt`,
  which nothing else in its answer allows it to detect.
- **Negative / trade-offs accepted:** the pathological same-minute page skips what it
  could not read — logged, not silent. `maxResults="0"` is no longer literally unbounded;
  a legitimate read of more than 5000 must now say so. Behind a full page an issue the
  index publishes late can fall outside the next window, which the lag would have caught
  — a catch-up window is historical, so the index has long since caught up there.
- **Follow-ups / risks to watch:** the batch limit and the ceiling are both server-wide
  constants; a watch whose minute genuinely exceeds the batch limit wants a larger read
  for that watch, not a global one. The remaining unbounded quantity in this path is the
  *payload*: ADR-0214 specifies a bounded `fields` list for the watch's search, and the
  shipped client asks for `*navigable`, so every custom field of every issue rides into
  a process variable on every poll. Narrowing it now would change what an existing
  watch's `issue` variable contains, so it wants its own record.

## Pros and cons of the options

### Option 1 — bound both, fail loudly
- Good: both failures become visible and recoverable; neither can consume the instance.
- Bad: two behaviours change for models that depend on today's ones.

### Option 2 — truncate an uncapped read
- Good: no task ever fails on the ceiling.
- Bad: the model is told it read every match when it read the first 5000. The defect
  surfaces later, somewhere else, as missing work.

### Option 3 — document it
- Good: nothing changes.
- Bad: the two failure modes stay invisible and stay fatal, and both are reachable by
  ordinary use — a bulk edit in Jira, a JQL that matches more than its author thought.

## Links

- relates to [ADR-0214](0214-jira-inbound-issue-watch.md) — the jira watch, its cursor
  and its lag
- relates to [ADR-0201](0201-jira-connector.md) and
  [ADR-0223](0223-jira-account-lookup.md) — the search and account-search operations
- relates to [ADR-0075](0075-clio-inbound-event-bridge.md) — the inbound bridge and its
  durable high-water mark
- relates to [ADR-0061](0061-incident-model.md) — what a failed job does
