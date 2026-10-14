# ADR-0282: One inventory of what is on disk, and what a backup owes it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas keeps about thirty things in its data directory: the log, the state store,
the checkpoints, and some two dozen sidecar stores created one per file across the
`api` package. What a backup carries was a list — `backupDirs` in `backup.go`,
extended by three more names in `snapshot.go`.

The list and the stores had nothing connecting them. Adding a store failed no
test, so twelve of them had drifted out of the whole-instance snapshot: `vault`,
`jobtypes`, `groups`, `process-docs`, `information-models`,
`playground-scenarios`, `api-tokens`, `oauth-clients`, `oauth-grants`, `targets`,
`call-overrides`, `grant-audit`. The archive reported success the whole time.

Two of those are worse than the rest. The vault **key** was backed up while the
`vault` directory holding the encrypted secrets was not — a restore produces the
key to a lock and nothing to unlock. And `jobtypes` holds the interned numbers
that already-stored jobs mean by their type; re-interning after a restore assigns
different ones, so a job stored as type 3 comes back meaning something else. That
is not a missing store, it is silently altered data.

A test over the list could not have found any of this. The list was internally
consistent; it just described less than the server writes.

## Decision drivers

- The next store added must not be able to slip out the same way. The mechanism
  matters more than the twelve, which are its symptom.
- A "full" archive that is not full is worse than an obvious failure: the first is
  discovered during a restore, which is the worst moment to discover anything.
- The portable design-time export and the whole-instance snapshot answer different
  questions and must be able to differ deliberately.
- Fixing the reported finding must not quietly widen the portable export.

## Considered options

1. Add the twelve missing names to the existing lists.
2. One inventory that classifies every entry, from which backup and restore are
   derived, plus a test that boots a server and refuses anything unclassified.
3. Back up the whole data directory and exclude by pattern.

## Decision outcome

Chosen option: **option 2.**

`persistentStores` is the inventory: one line per entry, each with a class saying
what a backup owes it, and a note saying what is lost with it. `backupDirs`,
`fullBackupDirs` and `fullBackupFiles` are computed from it, so they cannot
disagree with it. The classes are `design-time` (portable authoring),
`instance` (authoring and configuration belonging to this installation),
`runtime` (the engine's own durable state), `identity` (accounts and groups),
`credential` (standing machine permissions), `secret` (the vault and its key), and
`ephemeral` (rebuildable, so an archive would only carry a stale copy).

The part that does the work is the test. It boots a real server, reads the data
directory it wrote, and fails on any entry the inventory does not classify — with
a message saying what to do. A store added tomorrow cannot be merged without
someone deciding what happens to it when the disk is gone. The test over the
list is replaced by a test over the filesystem, because the list was never the
thing that was wrong.

The inventory carries two further facts the derivation needs, and both were found
by the tests rather than by design:

- **`onDemand`** marks entries a booted server does not create until the feature
  behind them is used — `checkpoints`, `dmn-models`, `exporter`. The
  registry-describes-what-exists test found these by failing.
- **`ownMechanism`** marks a store the archive carries through its own path rather
  than by walking the directory. The checkpoint root is the case: a snapshot takes
  the newest checkpoint that *verifies* and only that one. Adding it to the generic
  walk broke two existing tests, correctly.

Option 1 was rejected because it fixes the twelve and leaves the mechanism that
produced them. Option 3 was rejected because an exclude list fails the other way —
new files are carried by default, including ones that should never leave the
machine, and the failure is a leak rather than a gap.

### Consequences

- **Positive:** the whole-instance snapshot now carries all thirty entries. The
  vault travels with its key; the job-type table travels with the jobs that
  reference it.
- **Positive:** every entry now states what is lost with it, in the file where the
  decision is made. That note is for the person deciding whether their restore is
  complete, which is a question nobody could previously answer from the code.
- **Negative / trade-offs accepted:** the portable design-time export is
  **unchanged** — still the same thirteen directories. `process-docs`,
  `information-models` and `playground-scenarios` are arguably part of what an
  author would expect to move between installations, and they are classified
  `instance` instead. Widening a portable export as a side effect of fixing a
  snapshot bug is a change nobody asked for; the registry now makes it a visible
  question rather than an invisible omission, and it should be answered on its own
  terms.
- **Negative:** the completeness test boots a server, so it is slower than a table
  comparison and it fails on a machine where the server cannot start. That is the
  cost of asking the filesystem instead of the list.
- **Follow-ups / risks to watch:** an end-to-end restore into an empty data
  directory — decrypt a secret, compare group permissions, map an old job to the
  same worker type, resume a running instance — is what would prove the archive
  is usable rather than merely complete. This record makes it possible; it does
  not perform it. Whether a restore should carry credentials or deliberately
  revoke them is also still undecided: they are now carried, which at least makes
  the decision visible instead of settling it by omission.

## Pros and cons of the options

### Option 1 — add the twelve names
- Good: smallest possible diff.
- Bad: the thirteenth store drifts out exactly as the twelve did.

### Option 2 — one classified inventory, derivation, and a filesystem test
- Good: the omission becomes impossible to repeat silently; the two archives can
  differ on purpose.
- Bad: a boot in the test path; one more file to keep honest, though the test is
  what keeps it.

### Option 3 — back up everything, exclude by pattern
- Good: nothing can be forgotten.
- Bad: fails toward carrying what should never leave the machine, and a leak is
  harder to notice than a gap.

## Links

- extends [ADR-0107](0107-backup-and-restore.md) (the portable export) and
  [ADR-0109](0109-full-instance-snapshot.md) (the whole-instance snapshot)
- relates to [ADR-0070](0070-vault-on-by-default-with-generated-key.md) (the vault and its key, which had
  come apart)
- reported as F05 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
