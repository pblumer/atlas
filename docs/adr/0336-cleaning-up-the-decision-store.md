# ADR-0336: Cleaning up the decision store

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers

## Context and problem statement

Two stores in the decision stack only ever grew.

**Decision deployments** (ADR-0319) gain a version on every Deploy in the decision
editor (ADR-0322) and on every application publish. Iterating on a decision — which
is what the editor's Deploy button is *for* — mints a record per attempt, each
carrying the full DMN source, and nothing could take one back.

**Stored models** (`dmn-models/<handle>.dmn`) gain a file on every upload.
[ADR-0330](0330-a-model-with-no-reference-stays-findable.md) made the ones nothing
points at visible under Not assigned, and said in as many words that removing one
was left undecided.

[ADR-0329](0329-a-decision-deployment-is-not-deletable.md) had already written the
rule a delete would need, before any route existed, and deferred the route itself
for a stated reason: it had no caller, so its shape would be guessed. It named the
three questions the caller would have to settle:

> is it one version or a whole decision, does it need a dry run, does Operations
> need a "what is pinned to this" view first

There is now a caller. This record answers those three, adds a second guard the
first record did not foresee, and says plainly what remains wrong afterwards.

## Decision drivers

- **ADR-0329's rule is the specification.** A pinned definition refuses the delete;
  superseded versions are as pinned as current ones; running instances are not the
  test. Nothing here re-derives it.
- **A delete a restart undoes is not a delete.** The registry's "newest model
  providing this decision" pointers are last-write-wins with no history, so
  removing a key cannot simply clear them.
- **Version numbers are spent, not borrowed.** The per-decision counter is derived
  from the surviving records, and a release manifest (ADR-0128), an editor chip and
  an evaluation record all quote versions at readers.
- **Provenance is not a dependency.** A decision deployment records the `modelRef`
  it came from and carries its own XML; it never reads the file again.
- **Do not turn cleanup into a risk.** A destructive route that is easy to reach
  and hard to understand is worse than a store that grows.

## Considered options

1. **Delete by key, with two guards** — the pin guard ADR-0329 specified, plus a
   refusal on the current version of a decision that has older versions behind it.
2. **Delete by key, with the pin guard only** — ADR-0329's rule, literally and
   nothing more.
3. **Tombstone instead of delete** — keep the record, drop its XML, mark it
   deleted.
4. **Retention rather than deletion** — keep the newest N versions automatically.

## Decision outcome

Chosen option: **1 — `DELETE /api/v1/decision-deployments/{key}` with two guards**,
and `DELETE /api/v1/dmn-models/{ref}` for the other store.

### The three questions ADR-0329 asked

**One version or a whole decision?** *One deployment, addressed by key.* The key is
what was written, what a process pins to, and what the file on disk is named by. A
model providing two decisions holds one version of each lineage and they go
together, because they were written together. A "delete this decision entirely"
would be several deletes behind one confirmation, each with its own reason to be
refused — and the operator would learn about the refusals one at a time anyway.

**Does it need a dry run?** *No — the listing already is one.* Each row of
`GET /api/v1/decision-deployments` now carries `pinnedBy`: the deployed definitions
that resolved a latest-bound reference to that key. An operator choosing what to
remove sees what is holding each version before clicking, which is what a dry run
would have told them, in the place they are already looking.

**Does Operations need a "what is pinned to this" view first?** *Yes, and
`pinnedBy` is it.* The Console's decision page lists a decision's versions with what
holds each and offers Delete only where nothing does. A delete with nowhere to
stand would have been a route nobody could reach.

### The second guard, which ADR-0329 did not foresee

> A decision deployment may not be deleted while it is the **current** version of a
> decision that has an older version still deployed.

Two things go wrong at once there, and neither is a pin:

- **Latest binding silently rolls back.** The next process deploy would pin to the
  older version, with nothing recording that a version had been withdrawn.
- **The version counter goes backwards.** `decisionVersions` is rebuilt at startup
  as the maximum version across surviving records. Delete v3 while v1 and v2
  survive, restart, and the next deploy mints **v3 again** — a second, different v3
  that a release manifest and an editor chip both already spend that number on.

Deleting an *older* version is unaffected: the current version survives and holds
the high-water mark. Deleting the **last** version is allowed, because a decision
with no deployments left starts again at v1 and no survivor contradicts it. So the
practical rule is **oldest first**, and the refusal says so.

The cost is an ordering constraint on one workflow: "undo that deploy" is not
available, and the way to withdraw a version is to deploy a newer one. That is the
honest shape of an append-only lineage with readers.

### The registry is rebuilt, not patched

`Registry.UndeployDecision(key)` removes the key and then **recomputes** both
"newest provider" pointers from the survivors, in ascending key order. Keys come
from one monotonic counter, so ascending key order *is* registration order — live
and on recovery alike — and walking the survivors in it reproduces exactly what the
same records would produce after a reboot.

Clearing the pointer instead would have been the obvious patch and is wrong: it
aims at nothing where the truth is the next-newest version, so a later deploy would
bundle a model it did not need to, and a restart would disagree with the running
server. The test that pins this compares a live delete against a freshly booted
registry holding only the survivors.

### Deleting a model file, and what does not block it

`DELETE /api/v1/dmn-models/{ref}` refuses while **any DMN reference points at the
handle**. An unresolved reference is a state Atlas tolerates and reports, but not
one a deletion should create behind an author's back; the remedy — delete the
reference, or point it elsewhere — is theirs to choose, and
[ADR-0331](0331-deleting-a-dmn-reference-says-what-it-breaks.md) already tells them
what that costs.

A **decision deployment's `modelRef` does not block it.** That field is provenance:
the record carries its own XML and is rebuilt from it at startup, so a deployed
decision keeps evaluating exactly what it was deployed with whether or not the file
still exists. Refusing here would tie a runtime artifact's lifetime to a
design-time file it never reads — and would make a decision impossible to clean up
precisely because it had once been deployed.

Both spellings of a handle (`.dmn` and `.xml`) are removed, because both are the
same handle to a reference; leaving one would make a deleted model come back.

### What is still wrong afterwards

Two things, stated rather than implied.

**A whole lineage's version numbers are reusable.** Delete every deployment of
`eligibility` and the next one is v1 again, while an old release manifest may still
name a different `eligibility v1`. The manifest also records the deployment **key**,
which is the unambiguous half, so a reader can tell them apart — but the version
alone no longer identifies anything across that boundary.

**Deleting the highest-keyed record lets its key be reused after a restart.**
`nextKey` is rebuilt as `max(key over surviving records) + 1`. This is **not new
here**: `DELETE /api/v1/processes/{key}` has had the same property since ADR-0019,
and completed instances reference process definition keys. It is a general question
about the key space, it wants one answer for both kinds, and inventing half of one
inside a cleanup slice would be worse than naming it.

### Consequences

- **Positive:** The two stores stop growing without bound. Iterating in the editor
  no longer costs permanent disk.
- **Positive:** ADR-0329's rule is now exercised rather than described; the guard
  test that asserted the route's absence is replaced by the refusal's own tests, as
  its failure message instructed.
- **Positive:** `pinnedBy` answers "what is using this version" for the first time,
  which is a question asked without a deletion in mind.
- **Negative / trade-offs accepted:** Removing a version history is oldest-first,
  and "undo the last deploy" is not a delete.
- **Negative:** The listing now walks the deployed definitions' pins on every call,
  including the editor's version chip. It is an in-memory walk over compiled state,
  no I/O, and the alternative — learning about a pin only from the refusal — is
  worse.
- **Negative:** Version and key reuse, above.
- **Follow-ups / risks to watch:** The key space deserves one answer across process
  and decision deployments; a monotonic high-water mark that survives deletion
  would settle both, and neither delete should be extended before it exists.

## Pros and cons of the options

### Option 1 — delete by key, two guards *(chosen)*
- Good: implements the recorded rule and closes the counter hole it did not see.
- Good: the unit matches what is written, pinned and named on disk.
- Bad: an ordering constraint, and no way to withdraw the current version.

### Option 2 — the pin guard only
- Good: literally ADR-0329, nothing invented.
- Bad: it leaves a version number that two records spend differently, discovered
  only after a restart — the class of defect this stack has been closing all along.

### Option 3 — tombstone
- Good: no ordering constraint, counters never move, and the audit trail of "there
  was a v3, withdrawn by X" survives.
- Bad: a durable format change and a recovery path for a state that does not exist
  yet, to avoid a constraint that is one sentence to explain.
- Bad: it does not delete, which is what was asked for; it compacts.

### Option 4 — automatic retention
- Good: no route, no decision per version.
- Bad: it would delete a version something is pinned to, or need the same guard and
  then silently skip what it cannot remove — a cleanup whose result nobody can
  predict.
- Bad: "keep the newest N" is the wrong axis: what matters is what is pinned, not
  what is recent.

## Links

- implements [ADR-0329](0329-a-decision-deployment-is-not-deletable.md) — the rule written before this route
- extends [ADR-0330](0330-a-model-with-no-reference-stays-findable.md) — which made an unreferenced model visible and left removing it open
- relates to [ADR-0331](0331-deleting-a-dmn-reference-says-what-it-breaks.md) — the reference deletion this one sends an author through first
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the record and the version lineage
- relates to [ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md) — the pin that makes the guard necessary
- relates to [ADR-0128](0128-process-applications.md) — the release manifest that quotes a version and a key
- relates to [ADR-0019](0019-durable-deployments.md) — the process delete this mirrors, and the key-reuse question it shares
