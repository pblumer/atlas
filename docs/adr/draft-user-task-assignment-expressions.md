# ADR-DRAFT: A user task's assignment may be an expression

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether `candidateGroups` should accept a FEEL *list* as well as a
  comma-separated string. The vocabulary allows one and the engine stores the groups as
  a single interned string, so a list would be decoded and rejoined at every activation.
  No model needs it yet, and `="a,b"` says the same thing; it is refused rather than
  coerced so that adding it later is a widening and not a change of meaning.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0042](0042-user-task-assignment-and-claim.md) gave a user task an assignee and
candidate groups from `zeebe:assignmentDefinition`, and the runtime a claim and an
unclaim over them. Both attributes were treated as literal text: the compiler interned
the string, and the engine copied it into the job when the task activated.

For a task addressed to a fixed team that is correct. For anything decided per instance
it is not, and the model vocabulary has always said so — a value beginning with `=` is a
FEEL expression, which is how `zeebe:input` sources, a REST worker's fields and a
timer's schedule are all written. Assignment was the one place that read the `=` as part
of the name.

The portal made that unignorable. Its three shipped approval processes address their
task with `assignee="=approvalRef"` — the person or group the *product* named, carried
into the instance as a variable — and one of them with the line manager a directory
lookup had just written into `vorgesetzter`. Every one of them assigned the literal
eight characters `=approvalRef`. No person matched, so no person held the task, so no
approval Atlas shipped could ever be decided. Nothing reported it: the XML parses, the
process compiles, and an assignee is just a string.

## Decision drivers

- The rule already exists in this vocabulary. A modeller should not have to learn that
  one attribute is the exception.
- Whatever is decided has to survive replay. An assignment re-evaluated on recovery
  against variables that have since moved would hand the task to somebody else.
- An assignment that cannot be resolved must not become *no* assignment. Under
  [ADR-draft-task-commands-are-an-object-question](draft-task-commands-are-an-object-question.md)
  a task the model addressed to nobody is open work anybody may complete — so a failed
  expression would silently turn one person's approval into everybody's.
- Invariant I1: nothing new allocated on the hot path that is not needed.

## Considered options

1. **Evaluate at activation and freeze the result into the job-created event**, as the
   due date already is.
2. **Evaluate at read time**, in the Tasks API, whenever somebody asks who holds a task.
3. **Leave the engine alone and rewrite the models** to park an unaddressed task,
   carrying the responsible party as a variable the portal's own endpoint checks.

## Decision outcome

Chosen option: **evaluate at activation and freeze it**.

Option 2 is where it would have been cheapest to add and is wrong for the same reason
the due date is not computed at read time: the answer would change under the reader. Two
people looking at one task after a variable changed would see two different holders, and
the claim written against one of them would be fenced by neither.

Option 3 keeps the engine still and breaks the rule the fix exists to hold. An
unaddressed task is work anybody may take; making the portal's endpoint the only thing
that knows better would put the authority for "who may decide this approval" in one
page's query rather than in the task, and every other route to that task — the Console,
the API, an MCP tool — would go around it.

### What is evaluated, and what happens when it cannot be

`compiler.Assign` reads the attribute the way the vocabulary means it: a leading `=` is
an expression, anything else a literal, an empty value is no assignment at all. The two
possibilities live in one type, `compiler.Assignment`, rather than in two parallel
strings — so a caller cannot build a user task while forgetting that one of them was an
expression, which is exactly the defect this replaces. An expression that does not
compile fails the deployment, where the modeller is still looking at it.

At activation the engine evaluates it over the task's own scope chain and writes the
result into the job-created event beside the due date. Recovery replays the event; it
never re-evaluates (invariants I4/I6).

A result that is not a string is **refused rather than coerced**. "Who is this for" has
one shape, and a number stringified into an assignee is a task addressed to somebody who
does not exist — decided silently, at the one gate that says who may act on it. An empty
or null result is refused for the stronger reason above: it would open the task.

Both refusals park the element with a job-less incident, the same shape a timer whose
schedule could not be resolved raises ([ADR-0064](0064-timer-feel-failure-incidents.md)). The token
stays visible, an operator fixes the data, and resolving the incident re-runs the
activation — a genuine retry, which either addresses somebody or parks again. It never
creates a job addressed to nobody.

### The resolved groups move onto the job

The assignee was already a runtime field on the job, because claim and unclaim rewrite
it. Candidate groups were read from the compiled model at query time, which cannot work
once a model can compute them: what the expression evaluated to is a fact about *this*
instance, not about the definition.

`JobValue` therefore carries `CandidateGroups`, appended so a record written before this
decodes to `""`, and a reader falls back to the model's own value for those. Both halves
of the assignment now come from the same place, which is what keeps the task list, the
variable-read gate and the task-command gate answering the same question the same way.

### Consequences

- **Positive:** The three shipped approval processes reach their approver. Any model can
  route a task to a person or group decided at runtime, which is the ordinary case for
  anything that involves an organisation chart. A modeller writes `=` where they write
  it everywhere else.
- **Negative / trade-offs accepted:** A failed expression is now a parked token where
  before it was a task assigned to nonsense — more visible, and more to attend to. A
  `JobValue` grows one string. Candidate groups exist in two places during the migration
  window (the job for new records, the model for old ones), which the fallback resolves
  and time removes.
- **Follow-ups / risks to watch:** The open question above. The Tasks app's folder
  editor builds its list of candidate groups from what the *models* name
  ([ADR-0268](0268-task-folders-are-saved-filters.md)), so a group that is computed is not among the
  values it suggests — the rule still matches it, but nobody is offered it. Filling that
  list from the open tasks instead would fix it and costs a scan, which is why it is
  noted rather than done. And [ADR-0178](0178-responsibility-metadata-raci.md)'s
  responsibility metadata is still literal — an expression there would be the same
  change again, and nobody has asked.

## Implementation

`compiler.Assignment` and `compiler.Assign` in `compiler/`, with
`UserTaskDetail.AssigneeExpr` / `CandidateGroupsExpr` carrying the compiled halves;
`resolveAssignment` and `raiseAssignmentIncident` in `engine/behavior.go`, called from
`userTaskBehavior.OnActivated`; a `TypeUserTask` case in `resumeParkedElement` so
resolving the incident re-runs the activation. `model.JobValue.CandidateGroups` carries
the resolved groups, read in preference to the model's by `enrichTaskWith` and
`taskFieldsFor`.

Five engine tests hold it — an expression names the person it evaluates to, a literal is
untouched, an unresolvable assignee parks and then resolves to the right person, a
non-name is refused, and an empty or malformed expression fails at compile time — and
two API tests start the shipped approval models the way the fulfilment orchestrator
starts them and check that the person and the group the product named actually hold the
task.

## Links

- extends [ADR-0042](0042-user-task-assignment-and-claim.md) — the assignment it made literal
- follows [ADR-0064](0064-timer-feel-failure-incidents.md) — a job-less incident parks the element and resolving it retries
- required by [ADR-draft-portal-approval-page](draft-portal-approval-page.md) — an approval that reaches nobody has no approver to show a page to
- guarded by [ADR-draft-task-commands-are-an-object-question](draft-task-commands-are-an-object-question.md) — which is why an unresolved assignment must not become no assignment
