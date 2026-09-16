# ADR-0357: A portable backup does not overwrite another installation's identity

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas has two archives, and they are for different things
([ADR-0107](0107-backup-and-restore.md), [ADR-0109](0109-full-instance-snapshot.md)):

| | design-time backup | whole-instance snapshot |
|---|---|---|
| carries | what an author moves between installations | everything that cannot be rebuilt |
| includes `wal/`, `keyspace/`, `jobtypes/` | **no** | yes |
| applied | overlay onto a running instance, live | staged, replaces the data directory on restart |
| documented for | "restore it onto another instance" | "reconstitutes a complete engine on another box" |

The design-time archive carries `deployments/` and `decisions/`, which are filed by
**definition key** — and it does not carry `keyspace/`, the counter that issues those
keys, because that counter is runtime. So the archive carries records whose identity
was minted by a sequence it leaves behind, and the restore resolves the resulting
ambiguity by overwriting whatever is at that key.

[ADR-0339](0339-the-definition-key-space-never-goes-backwards.md) and
[ADR-0345](0345-the-job-type-index-space-never-goes-backwards.md) made a key
permanent *within* an installation. Neither says anything about a key arriving from
outside one, and the durable floor cannot: the archive does not carry it.

### Measured, not argued

**One: a restored definition inherits the local history at its key.** Installation A
deploys `alpha`, which takes key 1. Installation B deploys `beta`, which also takes
key 1, and runs one instance to completion. A's portable backup is restored onto B —
the documented use — and B is restarted:

```
B: key 1 runtime before the restore = {instances:0 finished:1 elements:[{s 1} {e 1}]}
B: restore said 200 {"restored":3,"restartRequired":true}
B after restart: [{"key":1,"processId":"alpha", …}]     ← beta is gone from the listing
B: key 1 runtime after  the restore = {instances:0 finished:1 elements:[{s 1} {wait 1}]}
```

`alpha` has never run on B. It reports one finished instance, and `beta`'s visit has
been **re-labelled onto `alpha`'s own element `wait`** — the aggregates are keyed by
definition key and element index, so a foreign definition does not merely inherit the
numbers, it redistributes them across whatever its elements happen to be at those
indices. `beta` is not reported as replaced; it is simply not there any more.

**Two: the archive carries the installation's identity, so the provenance is gone
too.** `settings/node.json` holds the node id (ADR-0189 §6), and `settings/` is
classified design-time, so it rides along:

```
A node id = 00ee538c4f478fbc064fc5e628679135
B node id = 4d16b665a356cf07a969d7357f5a0243
B node id after restoring A's archive = 00ee538c4f478fbc064fc5e628679135
```

B is now A. The store's own doc comment says why that is wrong — "a runtime that comes
back with a different identity looks to every correlator like a node that vanished and
a new one that appeared" — and this is worse than that, because both are running.

**Three, for completeness: the whole-instance snapshot is not affected.** It carries
`keyspace/`, `jobtypes/` and the WAL together and drops the materialized state, so it
is a consistent point in time. Measured, restoring A (keys 1–3) onto B (key 1, one
finished instance):

```
key 1 = a1, runtime {instances:0 finished:0 elements:[]}
next deploy took key 4
```

Nothing inherited, nothing reused. An earlier note in this work claimed a restore
brings back a lower mark and that nothing detects it; for the snapshot that is simply
wrong, and this record corrects it. For the portable archive the mark is not brought
back *low* — it is not brought back at all, which is a different and larger problem.

### What ADR-0107 did and did not decide

ADR-0107 chose "restore overlays files on disk … an overlay (merge), not a wipe" and
named one related risk:

> restoring a design-time backup onto an instance whose WAL references now-absent
> deployments can fail at the next boot

That is the case where the key is *missing*. The case where the key is **present and
now means something else** was not considered, and is the one that does not fail: it
succeeds, reports `200`, and changes what the installation's own history is about.

## Decision drivers

- **A key is local.** It is issued by one installation's counter and is meaningful
  only there. Two installations' key 1 are not the same thing and never were.
- **The corruption is silent, and that is the worst property.** A refusal an operator
  can read is strictly better than a success that re-points history.
- **Moving models between installations must keep working.** It is what the archive
  is for, and drafts, models, forms and projects carry no local identity at all.
- **Identity must not be a side effect of a file copy.** An installation that restores
  a colleague's models has not become that colleague's installation.
- **Do not break restoring your own backup onto yourself**, including an older one.
  ADR-0107 accepted that trade-off; this record is not the place to revisit it.

## Considered options

1. **Skip a keyed record that would overwrite a different definition, and report it** —
   plus stop carrying the node identity in the portable archive.
2. **Refuse the whole archive** when any keyed record collides.
3. **Re-key restored deployments** to fresh local keys, as a real import would.
4. **Carry `keyspace/` in the portable archive too**, so the counter travels with the
   records.
5. **Drop `deployments/` and `decisions/` from the portable archive**, making it purely
   authoring.
6. **Warn only**: restore exactly as today, and report the collisions afterwards.

## Decision outcome

Chosen option: **1 — skip and report, and leave the installation's identity at home.**

Two changes, both on the portable archive only:

- **`settings/node.json` is excluded** from the design-time backup *and* from its
  restore. Excluded on both sides, so an archive already carrying one — every archive
  taken before this — cannot overwrite an identity either. The whole-instance snapshot
  still carries it, which is right: that archive's documented job is to reconstitute
  *this* engine somewhere else.
- **A restored `deployments/` or `decisions/` record is written only when the key is
  free, or when the record already there is the same deployment.** "The same
  deployment" is the identity every screen shows: for a process, the same
  `processId` at the same `version`; for a decision deployment, the same decisions at
  the same versions. Anything else is skipped, and the response names it.

Restoring your own backup onto yourself is unchanged, including an older one whose
records have since been edited: the identity still matches, so it still overwrites,
exactly as ADR-0107 decided. What changes is only the case that was corrupting.

### Why skip rather than refuse (option 2)

A refusal is the tidier story and the wrong trade. The archive is one file holding
drafts, models, forms, projects *and* deployments; the first four are what an author
is usually moving, and they carry no local identity. Refusing the whole archive over
one deployment key would block a legitimate model migration entirely, and the operator's
remedy — hand-editing a tar — is worse than the problem.

There is also an honest mechanical reason. The restore streams the archive and writes
as it goes, so a true all-or-nothing refusal needs a staging pass, which is the
snapshot's design and not this endpoint's. Claiming atomicity that the code does not
have would be worse than not claiming it.

### Why not re-key (option 3)

This is what an *import* would do, and it is the option most worth wanting. It is
deferred, not dismissed. A definition key is quoted by more than its own record: a
release manifest's members ([ADR-0128](0128-process-applications.md)), a decision
deployment's pin ([ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md)),
a per-server call-activity override
([ADR-0105](0105-per-server-call-activity-target-overrides.md)), the process
documentation filed per key. Re-keying means rewriting that graph consistently, across
records that may or may not all be in the same archive — and it changes an artifact's
identity silently, which is the family of behaviour that produced this defect. It is a
feature ("import an application"), not a repair.

### Why not carry the key space (option 4)

It looks like the symmetric fix and it is meaningless. Two installations' counters are
two sequences; a restore cannot merge them into one, and taking the source's mark would
either strand the target's own keys or leave the collision untouched. The problem is
not that the mark is missing — it is that the key is not portable.

### Why not drop deployments from the archive (option 5)

Conceptually the cleanest: a deployment has a key and a history, so it is arguably not
design-time at all, and [ADR-0282](0282-store-registry.md) classifying it as such is
where this begins. But removing it takes away "restore my deployed processes onto a
fresh box", which works correctly today and is a large part of why operators take the
backup. That is a product decision, and it is not forced by this defect.

### Why not warn only (option 6)

The damage is done by the time the warning is written, and it cannot be undone from the
warning: the overwritten record is gone and the history it described is now attached to
something else.

### Consequences

- **Positive:** The measured corruption does not reproduce. A cross-installation
  restore now leaves the target's deployed definitions and their history alone, and
  says which records it did not take.
- **Positive:** An installation keeps its own identity through a restore, so a
  correlator sees one node per node.
- **Positive:** The operator gets the fact they need — "these keys are taken here" —
  which is the input to deciding whether they wanted a fresh instance.
- **Negative / trade-offs accepted:** A restore onto a used installation is now
  partial, and a partial restore is a state ADR-0107 did not have. The response reports
  it; nothing else does.
- **Negative:** A skipped record means the archive's application or release manifest
  may reference a definition that was not taken. That is visible as an unresolved
  member, not as silent corruption, and it is the honest consequence of the key not
  being portable.
- **Negative:** Two installations that both deployed the same `processId` at the same
  `version` on the same key will still overwrite. The records describe the same model,
  so the history stays about the right process — but the XML may differ, and this
  record does not detect that. Comparing the XML too would also block a legitimate
  same-installation rollback, which ADR-0107 allows on purpose.
- **Follow-ups / risks to watch:** A real "import an application onto this
  installation" — option 3 — is the thing an operator moving work between installations
  actually wants, and nothing here provides it. Separately, `settings/` mixes portable
  configuration with one file that is installation identity; this excludes that one
  file by name rather than splitting the store, so a future identity file added beside
  it would travel again unless it is excluded too.

## Pros and cons of the options

### Option 1 — skip the colliding record, report it, leave identity at home *(chosen)*
- Good: the corruption becomes a fact in a response instead of a rewrite of history.
- Good: model migration, the archive's main use, is untouched.
- Good: no new durable state, no new endpoint.
- Bad: a restore can now be partial, which is a new state for the operator to read.

### Option 2 — refuse the whole archive on any collision
- Good: one outcome, no partial state.
- Bad: blocks a legitimate model migration over an unrelated deployment key.
- Bad: the endpoint streams and writes as it goes, so the atomicity would be claimed
  rather than held.

### Option 3 — re-key restored deployments
- Good: what an import should do, and the only option that takes everything.
- Bad: a key is quoted by manifests, pins, overrides and documentation; rewriting that
  graph is a feature, not a fix.
- Bad: silently changing an artifact's identity is how this defect happened.

### Option 4 — carry the key space in the portable archive
- Good: symmetric with the records it would accompany.
- Bad: two counters cannot be merged; it would strand keys or change nothing.

### Option 5 — drop deployments and decisions from the portable archive
- Good: removes the class mismatch at its root.
- Bad: takes away a capability that works, to fix one that does not.

### Option 6 — restore as today and warn
- Good: no behaviour change to reason about.
- Bad: the warning describes damage that has already happened and cannot be undone.

## Links

- extends [ADR-0107](0107-backup-and-restore.md) — the portable archive and the overlay
  this constrains
- relates to [ADR-0109](0109-full-instance-snapshot.md) — the whole-instance archive,
  measured here and unaffected
- relates to [ADR-0282](0282-store-registry.md) — the classification that puts keyed
  records and installation identity in the portable export
- relates to [ADR-0339](0339-the-definition-key-space-never-goes-backwards.md) — the
  durable floor, which cannot reach a key arriving from another installation
- relates to [ADR-0345](0345-the-job-type-index-space-never-goes-backwards.md) — the
  same argument for job-type indices
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the
  node identity that must not travel
