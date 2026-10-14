# ADR-0389: Forked instance migration

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0162](0162-process-instance-migration.md) moves a running instance from one
deployed version of its process to the next by **rebinding it in place**: the instance
keeps its key, its variables, its jobs and its whole history, and a frozen element
mapping rewrites the six durable families that name an element by compiled index. That
is the right answer whenever it holds, and for an ordinary model fix — a corrected FEEL
expression, a retyped output mapping, a task pointed at another Worker — it holds every
time, because BPMN element ids do not move when a modeler edits an expression.

It is also, by design, a validator that **refuses rather than guesses**. An active
element with no counterpart in the target version, a mapped element that changed BPMN
type, a waiting catch whose message name changed, a scope chain that no longer lines up,
a service task whose job type name changed while a job for it exists — each of those
refusals is correct, because the alternative is a token on an index that means something
else, which is an instance no later fix repairs.

What ADR-0162 left unanswered is what the operator does **after the refusal**. Today the
answer is the one that record set out to replace: cancel the instance and start a new
one by hand, re-entering its variables, and accept that everything it already did is
gone from the running system. That is precisely the case where the new version matters
most — the model was changed *because* the old shape was wrong, so the elements a token
is parked on are exactly the ones most likely to have been redrawn, retyped or removed.
The stronger the fix, the more certainly in-place migration refuses it.

So: what does it take to let an instance continue under a new version when its current
tokens **cannot** be carried across as they are?

### What "continuing" can honestly mean here

An in-place migration preserves the execution. A fork cannot, and pretending otherwise
is how this feature would produce corrupt instances instead of clear ones. Once a token
sits on an element the target version does not have — or has as a different kind of
element — there is no truthful way to say where that token "is" in the new graph. The
only honest construction is: **stop the old execution where it is, and start a new one
at points a human names**, carrying the instance's data forward.

That makes the two modes genuinely different operations rather than two settings of one:

| | In-place rebind (ADR-0162) | Fork (this record) |
|---|---|---|
| Instance key | kept | a new instance |
| Tokens | keep running where they are | old ones end; new ones start at named resume points |
| Jobs / user tasks in flight | survive | cancelled with the old instance; the new instance creates its own |
| Variables | untouched | root-scope variables and data objects copied over |
| History | one instance, read through two definitions | two instances, linked both ways |
| When it applies | the mapping holds | the mapping cannot hold, or the operator wants a clean cut |

## Decision drivers

- **A refusal must have a next step.** ADR-0162's validator is right to refuse; a
  refusal that leaves "cancel and re-enter everything by hand" as the only route makes
  the refusal itself expensive.
- **Never silently degrade.** A fork must never be what an in-place migration turns into
  when it fails. The operator chooses it, sees what it costs, and says why — the same
  gate ADR-0159 put on a manual completion.
- **The two records must find each other.** An instance that ends because its work moved
  elsewhere is not a cancelled instance, and one that starts mid-process is not a
  freshly started one. Each has to name the other, durably, or the audit trail is a pair
  of unexplained events.
- **Atomic.** The old instance's termination and the new instance's creation are one
  fact. A crash between them would leave either two live instances or none.
- **Replay identical.** Whatever this writes, `applyToState` has to reproduce from the
  events alone (I4/I6) — including the generated key of the new instance.
- **No new durable machinery unless the facts demand it.** ADR-0162 needed a new value
  type because the rebinding is a fact no existing event states. A fork is not: it is an
  instance being created and another being terminated, which are the two oldest events
  in this engine.

## Considered options

1. **Do nothing; keep cancel-and-restart as the answer to a refusal.**
2. **Widen the in-place validator** — let a token whose element vanished be moved to an
   operator-named element in the target graph, still inside the same instance.
3. **Fork: end the old instance, create a new one in the target version at
   operator-named resume points, carry the data, and link the two records both ways.**
4. **Fork as an API-level script** — cancel via the existing endpoint, start a new
   instance via the existing endpoint, and leave the linking to whoever wrote the script.

## Decision outcome

Chosen option: **3 — fork into a linked successor instance**, in five parts.

### 1. It is one command, and one batch

`IntentForking` is a command-only intent on the existing `VTProcessInstance` value — the
same shape ADR-0162 gave `IntentMigrating`, and for the same reason: the operator's
request is an intention, not a fact, and it is re-validated on the run loop because the
instance is free to move between the API's answer and the command's turn.

Its handler emits, in one batch and therefore under one fsync (I2):

1. `IntentActivated` for the **new instance**, carrying `PredecessorInstanceKey` — the
   ordinary instance-creation path, so the new instance gets its TTL timer, its declared
   data objects, its armed event subprocesses and its variable-index membership exactly
   as any other instance does;
2. the carried **variables** and **data object** values, as that creation's seed;
3. an element-activation command per **resume point**;
4. `IntentTerminated` for the **old instance**, carrying `SuccessorInstanceKey`, through
   the same teardown a cancel runs — every element instance terminated, every job
   cancelled, the expiry timer retired;
5. one `IntentOperatorActed` on **each** instance: `OperatorActionForkedTo` on the
   predecessor and `OperatorActionForkedFrom` on the successor, each with the actor and
   the required reason.

Because it is one batch, there is no window in which both instances are live or neither
is. And because the new instance's key is generated in the handler and written *into*
the events, replay rebuilds both sides from the log rather than re-generating anything
(I6).

### 2. The link is a field on the instance, not a new value type

`ProcessInstanceValue` gains two appended fields, `PredecessorInstanceKey` and
`SuccessorInstanceKey`, decoding to 0 on every record written before them, the way every
other appended field in this codebase does (ADR-0017). No new column family, no new
fold, no new decoder to fuzz.

This is the whole argument against giving the fork its own `VTProcessFork`: a rebinding
was a fact no existing event stated, so it needed one. A fork states nothing new — an
instance was created, another was terminated — except *which instance each of them is
connected to*, and that belongs on the instance record, where every reader already
looks. The terminal event carries the link into the history record with it, so a
finished predecessor still names its successor after it moves out of the active family.

The **state stays `PITerminated`**. A new lifecycle state would have to be taught to
every filter, counter, overlay and export in the product, and would answer a question no
operator asked; "terminated, and here is where the work went" is the truthful reading,
and the link is what makes it visible.

### 3. Resume points are named by a human, in the target version's vocabulary

The command carries resume points as BPMN element ids resolved to target indices
(`Command.StartElements`, the field a triggered start already uses), and the API derives
a **proposal**: for every live element instance of the old instance, the element of the
same BPMN id in the target version. That proposal is what the dialog opens with, and it
is right in the common case — the reason the instance could not rebind is usually one
element out of several, not all of them.

`ValidateFork` then refuses a resume point that cannot honestly seed an execution:

- **nothing to resume at.** An empty set creates an instance with no token — one that
  never ends, waiting for nothing.
- **not in the root scope.** Seeding inside a subprocess would need that subprocess's
  scope to exist first; the instance would carry a token whose `FlowScopeKey` names a
  scope nobody created. A token that belongs inside a subprocess resumes at the
  subprocess.
- **a boundary event, an event-subprocess start, or a compensation handler.** Each of
  those is armed *by* something else — a host activity, a scope, a completed compensable
  — and has no meaning started on its own.
- **a joining gateway.** A parallel or inclusive gateway with more than one incoming
  flow waits for the others; seeded with one token it is a deadlock, not a resume point.
- **a duplicate.** The same element twice is two tokens where the operator asked for
  one; if two are wanted, that is a model with two paths, not a repeated resume point.

And the fork as a whole is refused when the instance is not active, when the target is
not a deployed version of the *same* process id, when it is the version the instance is
already on, and — until somebody works out what it should mean — when the instance is a
**call activity's child**: its caller waits on the old instance's key through
`ParentElementInstanceKey`, and handing that caller a different child is a decision about
ADR-0076's contract, not a detail of this one.

### 4. What crosses, and what visibly does not

**Crosses:** the root-scope variables (with index membership recomputed against the
target version's declaration, ADR-0244/0295) and the root-scope data objects whose name
the target version still declares.

**Does not cross, and the dialog says so before the operator confirms:** activity-local
variables, in-flight jobs and user tasks, open incidents, armed timers and
subscriptions, compensation records, running call-activity children (they are torn down
with the predecessor, exactly as a cancel tears them down), and the history. The old instance keeps all of it
as the record of what happened; the new instance starts from its resume points with the
data and nothing else.

This is not a limitation to be lifted later — it is what a fork *is*. Carrying a job
across would mean handing a worker a job whose element instance belongs to a terminated
instance; carrying an armed subscription across would re-arm a catch the new execution
has not reached. The instance that already did those things is still there, still
readable, still linked.

### 5. The surface offers it only where in-place has run out

The migration dialog plans both modes in one call: it shows the in-place plan first and,
when that plan cannot hold, the fork below it as the alternative — resume points listed
by element id and editable, what will not cross stated plainly, and the same required
reason. A fork is never the button an operator presses by accident on an instance that
could simply have been rebound, and never the fallback the server takes on its own.

`POST /api/v1/instances/{key}/migrate/fork` is the endpoint; the plan comes back from
the existing `POST /api/v1/instances/{key}/migrate/plan`, which now answers both
questions about the same target. The endpoint reads the successor's key back off the
predecessor's own record after the batch commits, rather than inventing one: the key is
minted on the run loop, and its *absence* is how the API learns the handler re-validated
and dropped a fork that no longer held — reported as a refusal, never as a success with
nothing behind it. There is deliberately **no batch fork**: resume points
are a judgement about one instance's position, and a hundred instances parked at
different elements do not share one answer.

### Consequences

- **Positive:** ADR-0162's refusal now has a next step that keeps the instance's data
  and its record. A model fix that genuinely restructures a process — the case where
  in-place migration must refuse — can still reach the instances that motivated it. The
  two instances name each other durably, so "why did this instance stop" and "where did
  this instance come from" are answerable from the record alone, months later.
- **Negative / trade-offs accepted:** an instance's story can now span two keys, and
  every reader that assumed one instance is one story has to follow a link to be
  complete (the timeline, the archive, an OpenSearch consumer). In-flight work is lost
  by construction — a user task parked for a week is cancelled and recreated, which
  re-notifies its assignee. And the honest answer to "where should this continue?" is a
  human's, so a fork is one dialog an operator has to read rather than one click.
- **Follow-ups / risks to watch:** **a call activity's child** cannot be forked (above);
  giving its caller the successor is the obvious next move and needs ADR-0076 consulted
  rather than assumed. **Chained forks** are supported by construction — the successor
  can be forked again — but no reader yet walks a chain of more than one link and
  renders it as one story. And a **fork is not undoable**: the predecessor is terminated,
  and nothing here can restart it.

## Pros and cons of the options

### Do nothing
- Good: no new surface on live process state; the validator's refusal stays the whole
  answer.
- Bad: leaves cancel-and-restart as the operator's only route in exactly the case
  ADR-0162 was written to remove it from, and silently pushes people toward redeploying
  over a version or editing state by hand.

### Widen the in-place validator
- Good: one instance, one key, one history — every reader stays as it is.
- Bad: it would have to move a token to an element that is not the mapped counterpart of
  where it was, inside an instance whose jobs, subscriptions and scope chain still refer
  to the old shape. That is not a rebinding with a bigger mapping; it is a fork wearing
  the in-place event's clothes, and it would put the inconsistency ADR-0162 refuses
  inside a single instance where nothing marks it.

### Fork into a linked successor
- Good: honest about what can and cannot be carried; reuses the two oldest events in the
  engine instead of inventing a durable fact; atomic in one batch; both records name the
  other.
- Bad: two instances where an operator may have wanted one; in-flight work is genuinely
  lost; resume points need a human.

### Fork as an API-level script
- Good: no engine change at all.
- Bad: not atomic (a crash between the two calls leaves the instance cancelled and
  nothing started), not linked (nothing durable connects the two), not validated (a new
  instance starts at its start event, not where the work actually was), and the reason
  is nowhere.

## Links

- completes [ADR-0162](0162-process-instance-migration.md) — this is the answer to the
  refusal that record's validator is designed to give
- follows [ADR-0159](0159-manual-task-completion-audit.md)'s operator-action record for
  the audit, adding two kinds to the same closed set
- carries [ADR-0244](0244-searchable-variables.md)/[ADR-0295](0295-migration-reindexes-searchable-variables.md)
  membership onto the copied variables, for the same reason a migration re-indexes them
- bounded by [ADR-0076](0076-call-activities.md): a child instance's caller waits on its
  key, so a child cannot be forked until that contract says what should happen
- keeps [ADR-0017](0017-process-instance-history.md)'s appended-field discipline for
  the two link fields
