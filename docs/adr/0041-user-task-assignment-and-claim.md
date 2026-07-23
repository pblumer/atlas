# ADR-0041: User-task runtime assignment and claim/unclaim

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0028 made a user task a first-class element that parks a token and creates
an activatable job of the reserved `io.atlas.user-task` type, completed through
the same job path a service task uses. It also flagged, explicitly, that
**assignment — claim/unclaim and who a task belongs to — needs its own
decision.**

The Tasks app (slice 1) grew an Outlook-style inbox that filters by assignee
("Assigned to me", "Unassigned"). But the only assignee it can show is the
**design-time** default from the model's `zeebe:assignmentDefinition` — it is
compiled into `UserTaskDetail` and never changes. A real inbox needs a
**runtime** assignee: a person claims an open task (making it theirs) and can
release it (unclaim), and that assignment must survive a restart like every
other piece of instance state.

The question: **where does a user task's runtime assignee live, and how does
claim/unclaim change it, without a second execution model and without breaking
the engine invariants** (I1 no hot-path allocation, I2 durable before visible,
I4 one `applyToState`, I6 events are facts)?

## Decision drivers

- **Ride the job lifecycle we already committed to (ADR-0028).** A user task is
  a job; its assignee is naturally the job's assignee. Completion already
  retires the job — assignment should be retired by the same event, for free.
- **One `applyToState`, recovery-tested (I4).** Assignment is durable state, so
  it is an event folded into state by the single apply function and replayed on
  recovery, never a mutable side-channel.
- **Keep the hot path allocation-free (I1).** Service-task jobs — the common,
  token-movement case — must not start allocating because human tasks gained an
  assignee.
- **No new subsystem.** Avoid a parallel assignment store/index with its own
  lifecycle and its own recovery surface.

## Considered options

1. **Assignee on the job.** Add a runtime `Assignee` string to `JobValue` and a
   `JobAssigned` event that rewrites it. A user task's job is created carrying
   the model's default assignee; claim sets it to the claiming user, unclaim
   clears it. The Tasks list reads the assignee straight off the job.
2. **A dedicated user-task assignment record.** A new `VTUserTask` value type
   with its own column family, keyed by the element instance, holding the
   assignee, with `Assigned`/`Deleted` intents and its own store methods —
   separate from the job.
3. **Assignee as a process variable.** Store the assignee under a reserved
   variable name in the instance scope.

## Decision outcome

Chosen option: **Option 1 — the assignee is a field on the job.** A user task's
job is created with `Assignee` set to the model's default (the compiled
`zeebe:assignmentDefinition`); a new `IntentJobAssigned` event rewrites just that
field. **Claim** submits `JobAssigned` with the claiming user; **unclaim**
submits it with an empty assignee (explicitly available, overriding the model
default). The value is applied by the one `applyToState` (`PutJob`) and replayed
on recovery, so a claimed task is still claimed after a restart. Because a user
task *is* a job, its assignee dies with the job when the task completes — no
cleanup path, no orphaned record.

`JobValue` gains one trailing length-prefixed string. For a service-task job the
assignee is empty, which encodes as a 4-byte zero length and decodes to `""` —
**no allocation on the hot path**, honoring I1. A non-empty assignee only ever
appears on a human task and is written by a claim, which is off the
token-movement path.

Option 2 is rejected: it re-introduces the parallel lifecycle ADR-0028
deliberately avoided (a second thing to key, store, index, and recovery-test)
and needs an explicit deletion event to clean up on completion, purely to model
a datum that already belongs to the job. Option 3 is rejected: the assignee is
task metadata, not process data — burying it in the variable scope makes it
FEEL-visible and mixes it into input/output mappings, and it still needs a
reserved-name convention to be queryable as a task attribute.

Assignment is **not** yet authorization. There is no auth in the server, so who
"me" is remains a display-time identity the Tasks app carries (ADR-0028); claim
sets the assignee to whatever the caller passes. Candidate **groups** stay a
compile-time attribute in this decision — only the single assignee is mutable at
runtime. Authorization, candidate-group membership, and claim-conflict rules
(refusing to claim an already-claimed task) are follow-ups.

### Consequences

- **Positive:** Claim/unclaim reuse the job's event lifecycle and its proven
  recovery guarantees; the assignee is retired with the job automatically; the
  Tasks inbox filters on real runtime assignment; no new value type, column
  family, or store index; the hot path stays allocation-free for service jobs.
- **Negative / trade-offs accepted:** `JobValue` — shared with service tasks —
  carries a user-task-oriented field that is always empty for service jobs, and
  its on-disk layout changed (a pre-1.0 store/WAL from before this change cannot
  be replayed; acceptable at this milestone, no compatibility promise yet).
- **Follow-ups / risks to watch:** claim-conflict semantics (optimistic refuse
  vs. last-writer-wins — today it is last-writer-wins), authorization once the
  server has identity, mutable candidate groups, and a task-assigned timestamp
  for inbox sorting.

## Links

- decides the assignment question left open by
  [ADR-0028](0028-forms-and-the-tasks-app.md); builds on
  [ADR-0007](0007-job-worker-protocol.md) (job/task lifecycle)
- honors invariants I1, I2, I4, I6 ([invariants.md](../architecture/invariants.md))
