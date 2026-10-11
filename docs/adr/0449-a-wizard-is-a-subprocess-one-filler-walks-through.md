# ADR-0449: A wizard is a subprocess one filler walks through

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-10
- **Deciders:** Atlas maintainers
- **Open question:** How often anonymous fillers abandon a public wizard, and how many
  attempts are bots, is unmeasured. Running every step on the server means every
  abandoned attempt is a live instance holding the data entered so far, until the
  wizard's timeout ends it. The choice of server-side steps over a flow run in the
  browser rests on that cost staying small next to the rate limit and the timeout.
- **Question checked:** 2026-10

## Context and problem statement

Business forms often run over several screens, and what the next screen asks depends
on what the last one answered. People call this a wizard. Atlas can already *execute*
the branching. A user task carries exactly one form (`xmlFormDefinition`,
`compiler/parse.go`). Completing it writes the submitted fields into the task's flow
scope before the element completes (`Processor.CompleteJob`). An exclusive or
inclusive gateway behind it routes on those fields (`selectExclusiveFlow`).
`examples/reisebuchung/` models "if long-haul, a visa form; if a minor, a consent
form" in exactly this way, and it is visible in the diagram.

What Atlas cannot do is let the person **stay in the flow**.

- **The Tasks app moves to a list slot, not to the next step.** `completeCurrent`
  (`api/web/app.js`) reloads the list and selects whatever task now sits in the slot
  that was just emptied. That is the next item of a queue, not the next step of this
  instance. The filler of a three-screen form gets screen one, then somebody else's
  approval, then has to find screen two in the inbox.
- **The public start link goes silent after the first screen.**
  `handlePublicFormStart` answers `{"started": true}` and nothing else
  ([ADR-0029](0029-public-process-start-links.md)). An anonymous filler has no way
  to reach a second screen at all. The silence is deliberate:
  [ADR-0335](0335-starting-an-instance-returns-its-key.md) declined to hand an
  instance key to an unauthenticated caller and left that decision open.
- **The only working wizard is a demo.** `api/web/reisebuchung-kunde.html` lists
  `GET /api/v1/tasks?processInstance=` after each completion and renders the first
  open task in place. It needs a signed-in user. It does not tell "the engine is
  still working" apart from "this step belongs to somebody else". Its own comment
  says a public multi-step flow needs "one rich start form, or correlated public
  steps". `api/web/reisebuchung-einschritt-kunde.html` does the opposite: one user
  task, with pages cut out of one form by a hard-coded list in the page. Its
  branching is not in the diagram at all.

The diagram has no element that says "these steps are one sitting of one person".
Without such an element, the runtime cannot know when to carry a filler on to the
next step, when the sitting is over, or what an abandoned sitting is.

The question this record answers: **how does a model declare a multi-step form whose
branching is visible in the diagram, and how does one filler, signed in or anonymous,
walk through it without leaving it?**

## Decision drivers

- **The flow is in the diagram.** Which screen follows which answer is process
  logic. It belongs where a reader of the model sees it, not in page code or in a
  form's hidden-field rules. Field-level reactivity *within* one screen stays in the
  form (form-js `conditional`, `valuesExpression`), as `examples/reisebuchung`
  already separates it.
- **One execution model.** [ADR-0028](0028-forms-and-the-tasks-app.md) rejected a
  second subsystem for human work. A step that the history, the token simulation
  and the instance view cannot see is a step the diagram claims and the engine does
  not perform.
- **A sitting has a boundary.** The runtime needs a defined start, a defined end and
  a defined place to attach "abandoned after N minutes".
- **Anonymous fillers must work**, without weakening the trust boundary that
  ADR-0029 drew around `/public/*`. A wizard opened from a public link may reach the
  steps of *its own* sitting and nothing else, not the instance, not other tasks,
  and not other instances.
- **The invariants.** Keys and timestamps are frozen into events (I6). Nothing is
  reported before the batch's one fsync (I2). State is read on the run loop only
  (I3). Nothing is added to the per-command hot path (I1). What can be decided at
  deploy time is decided at deploy time (I5).
- **Additive.** Clients that read `taskKey` from a completion, or `started` from a
  public start, must not break.

## Considered options

1. A BPMN **group** drawn around the steps.
2. A per-task flag, "continue with the next task of this instance", and no boundary.
3. An **embedded subprocess marked as a wizard**. The engine runs every step, and the
   API carries the filler from one step to the next.
4. A subprocess that is only drawn. The browser runs it as a page flow and submits
   once at the end.
5. Leave it: one user task with a paged form (the "Fall A" demo), or page code
   per wizard (the "Fall B" demo).

For the anonymous half, where the proof of "this is my sitting" is kept:

- a. As a hash on the process instance's creation event.
- b. In a sidecar store of passes, the way public links are kept.
- c. As a reserved process variable.
- d. Not kept at all: a pass signed with an HMAC under a server key.

## Decision outcome

Chosen option: **3, an embedded subprocess marked as a wizard**, with the anonymous
proof kept as **a, a hash on the creation event**. Option 3 is the only one that
gives the sitting a boundary the engine already understands. A subprocess has one
entry, one end and a scope of its own, and boundary events attach to it
([ADR-0074](0074-embedded-subprocesses.md), [ADR-0040](0040-boundary-events.md)).
Every step stays an ordinary user task.

### The model

- **`atlas:wizard` on `bpmn:subProcess`**, with two values:
  - `internal`: signed-in fillers.
  - `public`: the anonymous filler of a public start link.

  The modeler draws a marker on a wizard subprocess and offers the attribute in the
  details panel. The compiler reads it into the compiled node, and computes for every
  user task the nearest enclosing wizard scope (`CompiledNode.FlowScope` already
  gives the chain). That keeps the question off the runtime path (I5).

  A collapsed subprocess compiles like an expanded one, because the compiler never
  reads DI. That lets the overview show one step, "Capture application", while a
  drill-down shows the screens. This needs a test fixture, since none exists yet.
- **Validation** ([ADR-0026](0026-problems-panel-and-versioned-validation.md)):
  - **Error:** a wizard inside a wizard. Nesting has no meaning a filler could see.
  - **Error:** an event subprocess marked as a wizard. Nobody enters it, so there is
    nobody to carry.
  - **Error:** a user task in a `public` wizard that names an assignee or candidate
    groups. Its holder is the pass holder, and two answers to "who holds this" are
    one too many.
  - **Warning:** an element inside a wizard that waits on something other than the
    filler: a receive task, an intermediate message, signal or timer catch, an
    event-based gateway, or a call activity. A call activity's steps live in another
    instance, which the continuation does not follow (the same limit
    [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md) records for the shop).
  - **Warning:** a wizard with no interrupting timer boundary event, because an
    abandoned sitting stays open forever.

  For a `public` wizard, that last warning is **enforced at publication**. A start
  link is refused for a process whose public wizard has no interrupting timer
  boundary. The compiler cannot know which start events will be published, but the
  publication handler can.
- **Data.** A wizard's answers land in its own scope, because completion writes to
  the task's flow scope (`ioResultScope`). They leave the wizard only through the
  subprocess's output mappings ([ADR-0068](0068-task-io-variable-mappings.md)), and
  the rest is dropped with the scope. This is the right default for a sitting: a
  branch the filler went down and came back from leaves nothing behind outside the
  wizard, unless an output mapping names one of its fields. It needs documenting,
  because a modeler who expects the answers on the process scope will find them
  missing.

### The continuation, for a signed-in filler

The completion route answers with where the sitting stands. Before completing, the
handler reads the job's element instance and walks `FlowScopeKey` up to the nearest
element instance whose node is a wizard. It has to do this *before* completing,
because a completed task's element instance is deleted. After `drive()` returns, it
reads, on the loop:

- **`step`:** the wizard scope instance is live and has an open user task in it,
  the earliest activated first. Parallel branches are therefore presented one after
  the other, as the demo does. If the filler may claim the task
  ([ADR-0317](0317-task-commands-are-an-object-question.md): it is unaddressed, or
  offered to a group the filler is in), the handler **claims it for the filler**
  through the existing assign command ([ADR-0042](0042-user-task-assignment-and-claim.md))
  before answering. Without the claim, an unaddressed step sits in every inbox
  ([ADR-0421](0421-a-task-is-listed-to-whoever-it-was-addressed-to.md)) for the
  moment between two screens.
- **`handedOver`:** the next open task is addressed to somebody the filler is not.
  The response does not say to whom.
- **`pending`:** the scope is live but has no open user task. An external worker
  or a timer is between the screens. `drive()` completes everything an in-process
  handler serves before it returns, so this state means real waiting, not a race.
- **`done`:** the wizard scope instance has ended, normally or by its boundary.

The answer is additive: `{"taskKey": …, "wizard": {"state": …, "task": …}}`, where
`task` is the existing `taskResp`. It is computed after the batch's fsync (I2) and
read on the loop (I3). It writes no event other than the claim, which is an ordinary
`AssignJob`. The authenticated start answers with the same `wizard` object when the
new instance's first wait is a wizard step the starter may claim. The Tasks app and
the Console's Start view render the step in place, show the steps taken so far
(not "n of m", which branching makes unknowable), and poll a `pending` state with
back-off.

### The pass, for an anonymous filler

A public start of a process that contains a `public` wizard mints a **pass**:

1. **Minting.** The handler draws 32 random bytes (`token.New`). It hands their
   SHA-256 to the creation command, which carries it on a creation-only pointer the
   way `Command.Created` already does, so other commands do not grow (I1).
   `ProcessInstanceValue` gains `PublicPassHash`, frozen into the activation event
   and read back on replay, never recomputed (I4, I6). The hash is a fact about how
   the instance was created, like `CorrelationKey` and `PredecessorInstanceKey`
   beside it.
2. **The answer.** The handler starts through `CreateInstanceReporting`
   ([ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md)). After `drive()` it
   answers `{"started": true, "wizard": {"pass": "<instanceKey>.<hex>", "state": …,
   "step": …}}`. A start of any other process still answers `{"started": true}` and
   nothing more. That keeps the silence ADR-0029 and ADR-0335 chose for every link
   that does not need to speak.
3. **What the pass opens.** It opens only the open user tasks of *that* instance
   that lie inside a `public` wizard scope:
   - `GET /public/wizard/step` answers the current step: the form schema, and a
     prefill limited to that form's own field keys. That is the rule
     [ADR-0275](0275-instance-visibility.md) applies to the holder of a task, with
     the pass holder treated as the holder.
   - `POST /public/wizard/steps/{taskKey}/complete` completes it and answers with
     the same continuation states as the signed-in route. The handler refuses any
     key that is not one of the form's fields, and enciphers personal values at
     the in-edge as every completion does
     ([ADR-0314](0314-portal-personal-data.md)).

   Nothing else answers to a pass: no `/api/v1/*` route, no other task, no other
   instance.
4. **Carrying it.** The pass travels in an `Authorization: Bearer` header, never in
   a path or query, so it stays out of access logs and `Referer`. The handler looks
   up the instance by the key in front of the dot, compares hashes in constant
   time, and answers 404 for every failure alike. The instance key in the pass is
   an identifier, not a capability: every `/instances/{key}` route authorizes the
   caller ([ADR-0275](0275-instance-visibility.md)), and an anonymous caller is
   refused there. The `/public/wizard/*` routes join the per-IP rate limit and the
   CORS allow-list of [ADR-0186](0186-embed-public-forms-cross-origin.md), with
   `Authorization` added to the allowed headers. Credentials stay disallowed.
5. **Lifetime.** A pass is useful while its instance has a live `public` wizard
   scope. The mandatory timer boundary bounds the sitting, and the instance's
   history TTL ([ADR-0085](0085-process-instance-ttl.md),
   [ADR-0146](0146-history-expiry-due-date-index.md)) bounds the data. Revoking the
   link stops new starts. It does not end sittings in flight; an operator cancels
   those.
6. **Inboxes.** A task inside a `public` wizard belongs to the pass holder.
   `taskVisibleTo` and `mayWorkTask` leave it out of every inbox and refuse it to
   everyone except operators and administrators. Without this, ADR-0421's rule
   would put every customer's half-filled screens in every signed-in user's inbox,
   because those tasks name nobody.

`public-form.html` and the embedded widget keep the pass in `sessionStorage`, so a
reload resumes the sitting. They render each step in place, and show a closing
message when the state is `done`. They show nothing of the instance's outcome.

### Not decided here: going back

A completed step is a fact (I6), and Atlas has no command that moves a token back
inside a live instance. Migration refuses scope changes
([ADR-0162](0162-process-instance-migration.md)), and a fork ends the instance
([ADR-0389](0389-forked-instance-migration.md)). Under this record, a "Back" button
is a sequence flow the modeler draws back to the earlier step. The prefill brings
the earlier answers back, since they still sit in the wizard scope. Whether "back"
becomes a platform operation (end the current step, re-activate the previous one,
both written as new events) is a decision of its own.

### Consequences

- **Positive:**
  - The branching of a multi-step form is in the diagram, and one marker turns it
    into a sitting.
  - Every screen the filler saw is in the instance's element history
    ([ADR-0022](0022-element-visit-history.md)), so an operator can see at which
    step people give up. The Design view's token simulation walks an expanded
    wizard like any subprocess ([ADR-0078](0078-design-view-token-simulation.md),
    [ADR-0104](0104-token-simulation-embedded-subprocesses.md)). A collapsed one
    it passes over.
  - Steps may run decisions and workers between screens, because the engine runs
    them.
  - The demo's hand-rolled `advance()` becomes a product behaviour that the Tasks
    app, the Console, the public page and the widget share.
  - Anonymous fillers get exactly one sitting. The silence of every other public
    link is unchanged.
- **Negative / trade-offs accepted:**
  - Every screen is one durable round trip, and a claimed step is a second one. At
    human pace this costs nothing measurable. It is still two fsyncs where a page
    flow in the browser has none.
  - Every abandoned anonymous attempt is a live instance until its timeout, holding
    what the filler typed. That is the open question above. The mandatory timeout,
    the history TTL and the rate limit are the controls.
  - `ProcessInstanceValue` grows by a field that almost every instance leaves
    empty. It is a schema change ([ADR-0009](0009-record-serialization-format.md)),
    which the instance record has absorbed before.
  - Revoking a link does not revoke the passes it issued.
  - A modeler must map a wizard's answers out explicitly.
- **Follow-ups / risks to watch:**
  - Implement in slices: (1) attribute, compiler and validation, with a collapsed
    subprocess fixture; (2) the signed-in continuation and the Tasks app; (3) the
    inbox and authority rule for `public` wizard tasks; (4) the pass, the public
    routes and the public page and widget; (5) rebuild `examples/reisebuchung` on a
    wizard subprocess and replace the demo's `advance()`.
  - The public start does not check submitted keys against the start form, although
    ADR-0029 says it does. The wizard routes will check, and the start route should
    get the same check.
  - Issuing a pass from the model, for example a mail carrying a link to continue a
    later `public` wizard, would let a customer come back. It is the natural next
    record.
  - "Back" as a platform operation, as above.

## Pros and cons of the options

### 1. A group around the steps
- Good: it has no execution semantics, so it touches nothing in the engine. A
  presentation hint is arguably what a non-executing artifact is for.
- Good: it can be drawn round existing tasks, across lanes, without restructuring
  the model, and the palette already offers it. The compiler ignores it today.
- Bad: membership is geometry. BPMN does define `categoryValueRef` on flow
  elements, but the modeler does not write it when a shape is dropped inside a
  group. Moving a shape, or an auto-layout pass, would change behaviour.
- Bad: no entry, no end and no scope. A token may enter or leave anywhere, groups
  may overlap, and there is nothing for a timeout or a pass to attach to.
- Bad: every reader of BPMN expects a group to change nothing.

### 2. A per-task "continue" flag
- Good: the finest control, and no restructuring.
- Bad: there is no sitting, so "done" and "abandoned" have no definition. A pass
  would have to be bound to individual tasks of an instance whose later tasks the
  model has not declared yet.

### 3. A marked embedded subprocess (chosen)
- Good: a real boundary, scope, end and boundary events, all existing semantics.
  Collapsible in the diagram.
- Good: one execution model, and full history.
- Bad: a round trip per screen, and a live instance per abandoned attempt.

### 4. A subprocess drawn but run in the browser
- Good: this is the strongest alternative. "Back" is free, the submission is atomic,
  there is no round trip per screen, and no instance exists until the filler
  finishes. That is data minimisation for anonymous fillers by construction, the
  property option 3 has to buy with a timeout.
- Bad: it is a second execution model. FEEL in the browser (form-js) and FEEL in the
  engine would decide the same conditions, and nothing guarantees they agree.
- Bad: the diagram would claim steps that neither the history nor the simulation
  shows, and no decision or worker could run between screens. A public endpoint that
  evaluates decisions for anonymous callers would be the price of getting that back.

### 5. Leave it
- Good: nothing to build. The paged-form demo works for a fixed sequence.
- Bad: the branching stays in page code, which is what this record exists to move
  into the diagram, and anonymous fillers stay limited to one screen.

### a. A hash on the creation event (chosen)
- Good: durable in the same fsync as the instance it unlocks. There is no window in
  which an instance exists without its pass, and no store to keep in step.
- Good: immutable after creation, it dies with the instance, and it is not
  business data.
- Bad: a field on a core record.

### b. A sidecar pass store
- Good: the proven shape of public links: a hex token, one file each, and the
  `token.IsHex` guard.
- Bad: two stores. A crash between the start and the pass write leaves an instance
  nobody can continue, and passes of ended instances need collecting.

### c. A reserved process variable
- Good: no schema change.
- Bad: Atlas has no reserved-variable concept. Every variable in-edge (completion,
  mapping, script, worker, operator edit) would have to guard the name, and a
  credential-derived value would sit among the business data in every variable view
  and export.

### d. An HMAC-signed pass
- Good: stateless.
- Bad: Atlas has no signing key. The only key material is the vault's, which is
  optional and deliberately not exposed. A new long-lived secret is new surface for
  backup and rotation, and could revoke only by rotating every pass at once.

## Links

- builds on [ADR-0028](0028-forms-and-the-tasks-app.md) (user tasks and forms),
  [ADR-0074](0074-embedded-subprocesses.md) (embedded subprocesses) and
  [ADR-0040](0040-boundary-events.md) (boundary events)
- takes the decision [ADR-0335](0335-starting-an-instance-returns-its-key.md) left
  open, for public wizards only, and narrows
  [ADR-0029](0029-public-process-start-links.md)'s "the route exposes nothing else"
  to "nothing beyond the sitting it started"
- refines [ADR-0421](0421-a-task-is-listed-to-whoever-it-was-addressed-to.md) for
  tasks inside a `public` wizard
- relates to [ADR-0186](0186-embed-public-forms-cross-origin.md) (widget mode) and
  [ADR-0204](0204-hosted-apps-on-an-isolated-origin.md), whose hosted apps get a
  multi-step public surface without authenticated app credentials
