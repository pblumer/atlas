# ADR-0162: Process instance migration

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

A deployed process definition is immutable and versioned (ADR-0019). Deploying a
corrected model produces version *n+1* with a new definition key; every instance that
was already running stays bound to version *n* through
`ProcessInstanceValue.ProcessDefKey`, and every one of its element instances carries
that key plus an `ElementId` that is an **index into version n's compiled graph**, not
a name.

That binding is deliberate and it is what makes recovery correct. Replay must
reproduce the state the live processor produced, from the events alone (I4/I6); a
token that ran through version *n*'s graph has to keep running through version *n*'s
graph, or the fold and the behaviors disagree with the log. This is also why
[ADR-0160](0160-fix-the-connector-from-the-incident.md) could make a service task's
*connector* editable from an incident but not what the model says about it: connector
configuration is operator-managed runtime state, the model is a compiled artifact.

The consequence is that **a model fix cannot reach the instances that need it**. The
operator sequence today is:

1. an instance parks on an incident caused by the model — a wrong FEEL expression, a
   missing output mapping, a task pointed at the wrong connector name;
2. the modeler fixes it and deploys version *n+1*;
3. every *new* instance is fine;
4. the instances that motivated the fix are still on version *n*, still stuck, and the
   only ways out are to cancel and restart them — losing everything they have done and
   any side effects already committed — or to fix each one by hand through the operator
   overrides (ADR-0098 variables, ADR-0159 manual completion) for as long as it takes
   them to drain.

For a long-running process — an onboarding that waits weeks on a human task, a
contract that waits for a timer — "wait for them to drain" is not an answer, and
"cancel and restart" is not either: the earlier steps really happened, and their side
effects are not undone by throwing the instance away.

So: what does it take to move a running instance from one deployed version to the
next, without breaking the guarantee that replay reproduces state?

### What actually has to move

An instance's binding to its definition is not one field. Every durable record that
names an element does it by *compiled index within a definition*, so a migration
rewrites all of them or leaves the instance inconsistent. Grouped by what they are:

**Live state that carries a definition key or an element index — rewritten:**

| Family | What is bound |
|---|---|
| `cfProcessInstance` | `ProcessDefKey` |
| `cfElementInstance` | `ProcessDefKey`, `ElementId` |
| `cfIncident` | `ElementId` |
| `cfCompensable` | `ProcessDefKey`, `ElementId`, `HandlerNode` |
| `cfElementTokenCount` | keyed `elTok:<defKey>:<elementId>` — the live-token counter behind the Operations overlay (ADR-0080) |
| `cfDefInstanceCount` | keyed `defInst:<defKey>` — the active-instance counter |
| `cfActiveStartKey` | keyed `activeStartKey:<defKey>:<corrKey>`, for a message-start instance (ADR-0094) |

**Amended while implementing: execution follows the element instance, not the index.**
This record first listed timers and message/signal subscriptions among the families a
migration rewrites. Reading the engine before writing the fold showed that it does not
need to, and that rewriting them would be expensive for nothing. A due timer resolves
its element through `GetElementInstance(timer.ElementInstanceKey)` and never reads
`TargetElementId`; a correlated message completes `m.elKey`'s element instance and never
reads the subscription's `ElementId`. Both fields are carried for display — and a
recurring timer re-derives `TargetElementId` from the element instance the next time it
arms, so it heals itself, while a subscription's `ProcessDefKey` and `ElementId` are
copied *together* into the retained message-flow row, where the pair truthfully records
the element as it was in the version the catch was armed under.

Rewriting them anyway would have meant scanning every timer and every subscription per
migrated instance — no per-instance index exists for either — which makes a batch
migration quadratic, for values nothing reads to decide anything. So they are left
alone, and the fold's set is six families, all reachable from the instance and its
element instances without a scan. If a future change makes one of those fields
load-bearing, the fix is a per-instance index, not a scan.

**Live state keyed by instance or scope — untouched, and that is the point:**
`cfElByProc`, `cfVariable`, `cfDataObject`, `cfActiveChildren`, `cfJobByElement`,
`cfCanceling`. Variables, data objects, the parallel-join child counts, the boundary
event's link to its host, token ids — all of it keys off *element instance keys*, which
a migration does not change.

`cfJob` and `cfJobActivatable` join that list, but only recently and for a different
reason. A job carries its type as an integer, and until the engine-wide job-type table
landed (`jobtype.Registry`, ADR-0007 step 1 / ADR-0157) that integer was interned *per
compiled process*, so the same index meant different things in two definitions and a
migration would have had to remap it. Now every deploy resolves its service tasks
through one persistent table, so the same job-type **name** has the same index in both
versions and the job needs no rewriting. What is left is not a rewrite but a check: if
the mapped task's job-type *name* changed between versions, the job already on disk
still carries the old name's index, and no worker for the new name will pick it up.

**History — deliberately not rewritten:** `cfProcessInstanceHistory`, `cfElementStep`,
`cfElementReplay`, `cfVariableSnapshot`, `cfDataObjectSnapshot`, `cfDecisionEvaluation`,
`cfVariableAudit`, `cfOperatorAction`, `cfMessageFlow`, `cfElementVisit`,
`cfElementVisitAgg`. See the decision.

## Decision drivers

- **Replay must stay identical.** Whatever migration does, `applyToState` has to
  reproduce it from the event alone, on a build of Atlas compiled a year later (I4/I6).
  This is the constraint that shapes everything else.
- **Refuse rather than guess.** A migration that strands a token, or lands one on an
  element of a different type, corrupts an instance in a way no later fix repairs. An
  unmappable migration must be rejected before anything is written.
- **Don't falsify the record.** What the instance did under version *n* happened under
  version *n*. The history is evidence, not a cache of the current shape.
- **The common case should be one click.** Element ids are stable across a normal
  edit — the modeler fixes an expression, the ids do not move — so the mapping the
  operator has to think about is usually empty.
- **Bounded blast radius.** One instance, one event, one fold. A batch is an API
  convenience, never a single durable transaction over thousands of instances.

## Considered options

1. **Cancel and restart.** What exists today. No new mechanism; loses the instance's
   work and re-runs its side effects.
2. **Rebind the instance in place, under a durable migration event that carries a
   frozen element mapping.** The instance keeps its keys, its variables and its
   history; only the definition binding and the element indices are rewritten, from
   data in the event.
3. **Recompute the mapping during the fold.** The event names only the two definition
   keys; `applyToState` derives element-to-element matching from the two compiled
   processes when it applies.
4. **Redeploy over the same definition key** ("edit the deployed version"). No
   migration at all — every instance follows because the artifact under its key
   changed.

## Decision outcome

Chosen option: **2 — rebind in place under a durable event carrying a frozen mapping**,
in five parts.

### 1. The mapping is data in the event, never a computation at apply time

The shape follows the one ADR-0159 established for an intervention: a command-only
`IntentMigrating` carries the operator's request, and its handler emits two events —
`IntentMigrated` on a new `VTProcessMigration` value, which is the durable fact
`applyToState` folds, and `IntentOperatorActed` beside it, which is the audit.

The `IntentMigrated` value carries the source and target definition keys and the
materialized element mapping: a length-prefixed list of `(fromElementIndex,
toElementIndex)` pairs covering every element index the instance's live state actually
references. `applyToState` reads it and rewrites; it computes nothing.

This is the whole reason option 3 is rejected. A fold that *derived* the matching would
depend on the matching algorithm's code, and an algorithm improved in a later release
would replay an old log into a different state — the exact divergence I4 exists to
prevent. It is the same reasoning that freezes `RaisedAt` into an incident event and
`PurgeDueDate` onto a terminal event rather than re-reading a clock or a definition
during replay.

The mapping is bounded by the instance's live element instances, subscriptions, timers,
incidents and compensable records — a handful for a typical instance, hundreds for a
wide multi-instance activity — not by the size of either graph.

### 2. Element instance keys are preserved

A migration rewrites *bindings*, it does not terminate and recreate. The element
instance keys, token ids, flow-scope keys and `AttachedToKey` links stay exactly as
they were, which is what lets variables, data objects, jobs, active-children counts and
the whole scope tree survive untouched — they are keyed by element instance key, not by
element index. It also means an in-flight job still resolves to its element instance
after the migration.

### 3. Validation is a gate in front of the event, and it refuses

The command is rejected — nothing written, no partial state — when any of these holds:

- **An active element instance has no mapping.** The token would be stranded on an
  index that means something else, or nothing, in the target graph.
- **A mapped element changes BPMN type.** `BpmnElementType` is stored on the element
  instance for dispatch; a token waiting on a receive task cannot become a token on a
  user task.
- **The scope chain does not map consistently.** An element's `FlowScopeKey` names its
  parent scope's element instance; the mapped element must sit under the mapped parent
  in the target graph. Moving a token into or out of a subprocess is not a rebinding,
  it is a different instance.
- **A multi-instance role changes.** A body must map to a body and an iteration to an
  iteration (ADR-0077); the same for an event-based gateway's race group, whose members
  must map together or the losers can no longer be cancelled (ADR-0110).
- **A waiting element's subscription identity changes.** A message subscription is
  keyed by `(name, correlationKey, elementInstanceKey)` and a signal subscription by
  `(name, elementInstanceKey)` — the name is in the *key*, not only in the value. If the
  target element waits on a different message or signal, the armed subscription is
  stale and a correct migration would have to re-arm it. Refused in this record; see the
  follow-ups.
- **A compensation handler does not map.** A compensable record names both the
  activity and its handler node (ADR-0103); both must land somewhere.
- **A mapped service task's job type name changes** while a job for it already
  exists. The job carries the engine-wide index of the *old* name, so after the
  migration it sits on the activatable index under a type no worker for the new
  version subscribes to — a token that looks live and is unreachable. Re-keying it is
  a follow-up; refusing it is this record.
- **The instance is not in a migratable state.** Terminated, completed, or currently
  being cancelled (`cfCanceling`, ADR-0108).
- **The target is not a version of the same process id**, or is not deployed.

A job currently **leased to an external worker** (ADR-0007) is refused by default and
allowed with an explicit override: the worker is going to complete work it started
against version *n*, and the completion will apply version *n+1*'s output mappings. That
is sometimes exactly what the operator wants and sometimes a surprise, so it is a
decision, not a default.

Because the migration runs as a command on the partition's single writer (I3), it is
serialized against everything else happening to the instance; there is no "quiesce the
instance first" step to get wrong.

### 4. History is not rewritten; the replay resolves across the boundary

An instance's steps under version *n* refer to version *n*'s element indices. Rewriting
them to version *n+1*'s indices would be falsifying the record — and would be impossible
anyway for an element the new version deleted. So `cfElementStep`, `cfElementReplay`,
the variable and data-object snapshots, the decision evaluations and the audit trails
all stay exactly as written.

That makes the *reader* the thing that has to change. The instance timeline today
resolves every step through one compiled process — the instance's current one — so
after a migration the pre-migration steps would silently render as the wrong element
ids. Instead: the migration writes a per-instance history record, and the timeline
resolves each step through the definition that was in force **at that step's log
position**, switching at the migration's position.

The record is an operator action (ADR-0159): that mechanism already stores
`{instance, elementInstance, job, kind, actor, reason}` append-only under its process
instance, keyed by `(timestamp, position)`, is already folded into the instance
timeline, and was explicitly left with "a seam for cancel/resolve to join later".
Migration joins it as a new `OperatorActionKind`, plus one append-compatible field — the
definition key the instance is migrating *from*, which an older record decodes to 0, the
way every other field appended to a value in this codebase does. That field is what the
reader actually needs: the instance's own record names the definition it is on *now*,
and each migration record names the one before it, so a chain of migrations reads back
as a chain of position ranges. No new column family, and the migration becomes
*visible* — "migrated v3 → v4, by whom, why" as a block on the timeline — which is the
difference between an instance whose behavior changed mid-flight for a reason and one
that appears to have changed by itself. A reason is required, as it is for manual
completion.

Two things the reader had to settle that this section did not anticipate, both found
building it:

**A source version can be deleted.** The API refuses to delete a definition with running
instances — but once an instance migrates away, the version it left has no instances on
it and may be deleted outright. So a historical definition is genuinely, routinely
absent, not merely absent in theory. The reader answers such a step with *no* element id
rather than falling back to the current graph: a step labelled with the wrong element is
worse than one labelled with none, because only the second is visibly missing. The one
place that must not degrade to "unknown" is the frame fold's leaf test, which decides
whether a token is dropped or held waiting for a successor — an unresolvable element
answers "leaf" there, so a token whose successor can never be identified is dropped
rather than stranded on the replay forever (the ghost [ADR-0136](0136-terminated-tokens-in-the-replay.md)
removed).

**Two indices from different versions cannot be compared.** The frame fold links a
deferred completion to the activation that succeeds it by comparing the completed node
against the activation's incoming-flow source — an index comparison, exact within one
definition and meaningless across two. Across a boundary it falls back to the BPMN
element id, the same identity the mapping pairs elements by. Deliberately only across a
boundary: a process built through the builder API rather than parsed from XML has no
BPMN ids at all, so making the id comparison the default would make every element equal
to every other.

The per-definition analytics stay split on purpose: `elVisit`/`elVisAgg` are keyed by
definition key, so version *n* keeps the visits that happened under it and version
*n+1* accrues its own. That is what actually happened.

### 5. One instance per event; the batch lives in the API

`POST /api/v1/instances/{key}/migrate` takes the target definition key and optional
explicit element overrides. A plan endpoint answers the same question without writing:
which instances would migrate, what mapping was derived, and what would be refused and
why. A batch form (`?process=<defKey>`, migrate every active instance of a version)
fans out to one command per instance, so a refusal on instance 400 does not roll back
the 399 that were fine — and each fold stays a bounded, independent unit.

The default mapping is **by BPMN element id** — the string the modeler wrote, which is
stable across an ordinary edit and is the only element identity a human controls.
Explicit overrides name elements by BPMN id on both sides, never by compiled index; the
server resolves them to indices when it builds the command, and only the resolved
indices reach the log (I5: no model strings in the log).

### Consequences

- **Positive:** a model fix can reach the instances that motivated it. A long-running
  instance is no longer a reason to keep a broken version alive, which also makes
  ADR-0119's deactivation useful for its intended purpose — stop new instances on the
  old version, migrate the rest, retire it. The instance keeps its variables, its
  history and its keys, so nothing downstream that references an element instance key
  breaks. The migration is on the record, with an actor and a reason.
- **Negative / trade-offs accepted:** it is a genuinely large surface — six durable
  families rewritten in one fold, and every one of them a place to be wrong; the
  validation rules are the feature, and they will refuse migrations an operator
  believes should work — and the surface moves as the engine does: the engine-wide
  job-type table removed one family from it while this record was being written, and
  the next structural change will move it again. The event is variable-length in a way most engine events are
  not (the mapping), so the record encoding needs a length-prefixed list and an
  explicit bound, and the value decoder needs a fuzz/round-trip test against a
  truncated or oversized list the way the other variable-length values have.
  History that spans two versions makes the replay reader more complicated than "one
  instance, one diagram" — the timeline has to hold two compiled processes and switch
  between them. And migration cannot fix what has already happened: an instance that
  took the wrong branch under version *n* is still on that branch afterwards.
- **Follow-ups / risks to watch:** **re-arming a changed subscription** is the first
  thing operators will ask for after this ships — it needs the migration event to carry
  the new subscription records rather than only the mapping, so the fold stays a pure
  read of the event. **Migrating a call-activity parent and its children together** is
  out of scope here: the child instance is bound to its own definition and keeps its
  `ParentElementInstanceKey`, so the pair is consistent, but a caller whose
  `calledElement` binding changed needs its own answer. **A migration dry-run in the
  Modeler**, beside the deploy, would catch an id rename at the moment it is made
  rather than at the moment somebody tries to migrate past it. And the **rename trap**
  of ADR-0158 has a cousin here: renaming a BPMN element id between versions defeats
  the default mapping and turns a one-click migration into a manual one.

## Pros and cons of the options

### Cancel and restart
- Good: exists today; no new durable surface, no new failure mode.
- Bad: it is not a migration. The instance's completed work is discarded and its
  committed side effects are not — a restart re-sends the mail that was already sent.
  For a process that waits weeks on a human, it is the most expensive possible answer.

### Rebind in place under a frozen mapping
- Good: the only option that both preserves the instance and keeps replay
  deterministic. Preserving element instance keys means most of the instance — every
  variable, every scope count, every job — needs no rewriting at all. Refusing early
  keeps the failure mode "your migration was rejected" rather than "your instance is
  now inconsistent".
- Bad: the biggest single addition to the durable record since incidents; a
  variable-length event; and a validation surface that has to be right the first time,
  because a bad fold is discovered on recovery, months later.

### Recompute the mapping during the fold
- Good: a much smaller event — two definition keys and nothing else.
- Bad: breaks I4 outright. Replay would depend on the matching algorithm rather than on
  the log, so improving that algorithm silently rewrites history, and two Atlas builds
  replaying the same log could disagree about where a token is. Non-negotiable.

### Redeploy over the same definition key
- Good: no migration mechanism at all; every instance follows automatically.
- Bad: destroys the immutability ADR-0019 exists for. Element indices are positions in
  a compiled graph, so replacing the artifact under a key re-points every element
  instance, timer and subscription already referring to it — the log would replay
  through a graph that is not the one the events were produced by. It does not avoid
  the migration problem, it performs an unvalidated migration on every instance at
  once, silently.

## Links

- constrained by [ADR-0019](0019-durable-deployments.md) (immutable
  versioned deployments) and invariants I4/I6 — the reason this needs a record at all
- named as the missing piece by
  [ADR-0160](0160-fix-the-connector-from-the-incident.md), which fixed the *runtime
  configuration* layer of "adjust this service task and try again" and could not touch
  the model layer
- narrowed by the engine-wide job-type table (`jobtype.Registry`, the ADR-0007 /
  [ADR-0157](0157-worker-processes-supervision-and-console.md) prerequisite): with job
  type indices resolved once per engine instead of interned per process, a job needs no
  rewriting on migration — only a check that its type name did not change
- reuses [ADR-0159](0159-manual-task-completion-audit.md)'s operator-action record —
  the seam it left for other interventions — for the migration's audit and for the
  position the replay switches definitions at
- makes [ADR-0119](0119-deactivate-deployed-process.md) (deactivate a deployed process)
  usable as intended: stop new instances on the old version, migrate the rest
- touches the per-definition counters of [ADR-0080](0080-runtime-aggregate-counters.md),
  which are keyed by definition key and so must move with the instance
- alternatives to cancelling: ADR-0098 (operator variable override) and ADR-0159
  (manual completion) are the per-instance repairs this replaces for the case where the
  *model* is what is wrong
