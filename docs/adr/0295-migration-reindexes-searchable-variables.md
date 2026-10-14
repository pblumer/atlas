# ADR-0295: A migration re-indexes what its target declares

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-09
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0244](0244-searchable-variables.md) made a declared variable findable by a seek:
`atlas:searchable="identityId,item"` on a process, and a value index maintained from
the `Indexed` flag each variable event carries. It argued that no backfill was needed,
and for the case it looked at that is right — the attribute postdates every definition
that could lack it, so every instance of a declaring version is started after the
declaration and stamped from its first write.

It missed one way an instance changes version after it has already written values:
[ADR-0162](0162-process-instance-migration.md) migration. An instance started on a version that
declares nothing and migrated onto one that declares `identityId` holds a value that was
stamped `Indexed: false`, and the fold has no reason to revisit it. The version-scoped
search then answers `identityId=MT-1998` from the index alone — that is the whole point
of a declaration — and the migrated instance is not in it.

The result is not a slow answer. It is a **wrong** one: the operator gets an empty
result for an instance the engine is holding, with nothing on screen to say so.

The reverse direction is benign, and it is worth saying why, because it decides how much
machinery this needs. Migrating *off* a declaring version leaves entries behind, but a
search filters them by the instance's definition key, and the next write of that variable
removes them (`reindexVariable` deletes the old entry whenever the stored record says it
was indexed). Nothing answers wrongly; some dead keys sit in the index until then.

## Decision drivers

- **A wrong answer must be fixed where it is produced.** The search cannot compensate:
  reading the index *and* walking the instances would give up the seek exactly where an
  operator needs it, and would make one query mean two things — precisely what
  [ADR-0248](0248-search-terms-are-literal.md) refused.
- **The fold cannot ask a compiled process anything** (ADR-0244's own finding). Whatever
  fixes this has to be decided at command time and frozen into the event (I6).
- **An instance already in step must cost nothing.** A repair an operator can run over a
  version has to be safe to run twice, and cheap the second time.
- **Derived state stays folded** (I4): the index must remain reproducible by replaying
  the log into an empty store.

## Considered options

1. Re-stamp membership as part of the migration, and expose the same correction as an
   explicit per-version repair.
2. Rebuild the index outside the fold — at startup, or on demand — by scanning the
   variables and the declarations.
3. Union the index with a walk on the search side for definitions that have received
   migrated instances.
4. Document the hole and leave it.

## Decision outcome

Chosen option: **1**, in two pieces that share one mechanism.

**A new record: `VTVariableIndex` / `IntentVariableIndexed`.** It carries a process
instance key, a variable name and a membership flag, and nothing else — deliberately no
value, because the value did not change. `applyToState` folds it through
`Tx.SetVariableIndexed`, which re-puts the variable with the new flag and so moves the
entry through `reindexVariable`, the one function that maintains the index. A variable
that is gone, or already in step, is a no-op, so a replayed event whose variable a later
record deleted is harmless.

**The comparison happens at command time, against the compiled process**, in
`appendVariableIndexChanges`: it reads the instance's root-scope variables — the only
scope that is indexed, an activity-local scope being scratch (ADR-0068) — and emits one
event per variable whose membership differs from what the process declares. What arrives
at the fold is the answer, never the question, which is ADR-0244's own discipline applied
to the one case it did not cover.

**Two callers:**

- **A migration emits them for its target.** `handleProcessMigrating` already holds the
  target's `CompiledProcess` for validation; after the migration event it emits the
  membership corrections the new declaration implies. Both directions are covered by the
  same comparison: a name the target declares and the source did not is added, and one it
  no longer declares is dropped.
- **An operator can ask for the same thing, per version.**
  `POST /api/v1/processes/{key}/reindex-instances` (admin, `?limit=`, default 500, max
  5000, `remaining` for the next round) queues one `IntentVariableReindex` command per
  running instance of the definition. It exists for the instances that were migrated
  before this record, which nothing else will ever revisit — and for an operator who
  would rather be sure than reason about when a version was deployed. It reports what the
  definition declares, so the answer says what the index will now answer for.

**Running instances only.** A finished instance's membership cannot change through any
normal path, so the repair would reach into the history family for a strictly historical
case; the command handler stays on the active family instead. The cost is stated rather
than hidden: an instance migrated before this record and *since finished* keeps the
membership it had, and is found by the walk rather than the seek.

### Consequences

- **Positive:** the operator's search over a version means the same thing for every
  instance on it, however the instance got there.
- **Positive:** a process that declares nothing still pays nothing. A migration between
  two versions that declare the same names emits no events at all: the comparison is
  per variable, against the declaration, and equality writes nothing.
- **Positive:** the repair is idempotent by construction, so "run it again" is always a
  safe answer.
- **Negative / trade-offs accepted:** a migration now reads the instance's root-scope
  variables at command time. That is a bounded read on a path that already validates a
  whole element mapping, but it is not free, and a batch migration pays it per instance.
- **Negative:** one more value type on the log, for a fact that is about an index rather
  than about the process. The alternative was re-emitting variable events, which would
  have written "this variable was updated" into an instance's audit trail when nothing
  about the variable changed.
- **Follow-ups / risks to watch:** finished instances, as above. And the declaration
  itself is still unvalidated against the model — a name nothing ever writes indexes
  nothing while looking like it works, which the Modeler is better placed to say than
  the deploy.

## Pros and cons of the options

### 1. Re-stamp at command time (chosen)
- Good: one mechanism serves both the automatic case and the repair; the fold stays
  ignorant of definitions; replay reproduces the index exactly.
- Bad: a new value type and a new command; a read of the instance's variables per
  migration.

### 2. Rebuild outside the fold
- Good: no new record; would also cover finished instances and any drift from any cause.
- Bad: it makes the index something two writers maintain — the fold and a rebuilder —
  which is the shape correctness bugs live in. A full rebuild is O(every variable), the
  cost ADR-0244 set out to avoid, and a rebuild that runs after replay is state derived
  from state rather than from the log (I4).

### 3. Union index and walk on the search side
- Good: no writes at all; self-correcting.
- Bad: gives up the seek precisely where it was asked for, and makes one query mean two
  things — an exact, case-sensitive index match unioned with a case-insensitive substring
  walk. ADR-0248 refused exactly that ambiguity.

### 4. Document it
- Good: free.
- Bad: the failure is a wrong answer, not a slow one, and it is silent.

## Links

- corrects the "no backfill is needed" reasoning of [ADR-0244](0244-searchable-variables.md)
  for the one case it did not consider
- rides [ADR-0162](0162-process-instance-migration.md)'s migration command, whose handler already
  holds both compiled processes
- keeps the query semantics [ADR-0248](0248-search-terms-are-literal.md) settled
- scope rule from ADR-0068 (activity-local variable scopes are not indexed)
