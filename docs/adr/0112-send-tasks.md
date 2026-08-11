# ADR-0112: Send tasks

- **Status:** Accepted
- **Date:** 2026-08-11
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. A `<sendTask>` is Atlas's **single outbound element**: what
> it sends is chosen at author time (the *kind*), and the compiler dispatches on that kind, exactly
> as a service task already dispatches on its connector extension. Three kinds:
>
> - **Job / connector** (a `zeebe:taskDefinition` or an `atlas:*Connector` extension): a
>   job-creating **activity** identical in execution to a service task — it creates a job and waits.
>   Reuses `serviceTaskBehavior`, the job machinery, the incident model (ADR-0061/0111),
>   boundary/I/O/data/multi-instance wiring, and recovery **wholesale**, via one compiled type
>   (`TypeSendTask`) that dispatches to the existing service-task behavior — as `TypeConnectorTask`
>   already does. This is the send task's primary, most-requested use (e-mail, REST, …).
> - **Message** (a `messageRef`): a correlating fire-and-forget send — the message intermediate
>   throw's semantics in task form. It compiles straight to `TypeMessageThrowEvent` and reuses
>   `messageThrowEventBehavior` (correlate, then flow on) with **no new runtime code**.
>
> No new job path, value type, behavior, or recovery path in any case.

## Context and problem statement

A **send task** (`<bpmn:sendTask>`) models "the process sends something out" — an e-mail, an API
call, a message to another system. In BPMN it is an **activity**, so like any task it accepts
**boundary events** (time out the send after N minutes), **I/O variable mappings** (ADR-0068),
**data associations** (ADR-0058/0059), and **multi-instance** looping (ADR-0077).

Atlas cannot express one today. `<sendTask>` is parsed only to be **rejected**: `xmlDefinitions`
collects `SendTasks []xmlNode` (`compiler/parse.go:854`) with no attributes, and `registerScope`
fails any model containing one with an actionable "…which Atlas can't execute yet (supported: …)"
that steers the author to a **service task** (`compiler/scope_compile.go:743`). The Modeler mirrors
this: `bpmn:SendTask` is in `UNSUPPORTED_TYPES` ("Send tasks can't run yet",
`api/web/editor.js:581`), so it draws with an unrunnable badge. There is no compiled type and no
behavior. A send task is the last standard BPMN **task** type Atlas doesn't run — service, script,
business-rule, user, receive, and undefined/manual tasks all execute; only the send task is
refused.

The question this ADR answers: **what does a send task *mean* in Atlas, and how do we run it
without a second outbound mechanism** — given that the outbound work modelers want on a send task
(e-mail, REST, SharePoint, clio, BMC Remedy) is *already* done by **connectors on a service task**
(ADR-0036/0067/0079/0105/0106), and given that "create a job, wait for a worker, complete or
incident" is a solved, durable path.

What already exists, and is load-bearing:

- **The job path is solved and durable.** `serviceTaskBehavior` (`engine/behavior.go:1520`)
  creates a `JobValue` on activation (`JobType`, `Retries`) and stays Activated; a worker completes
  the job and `completeAndTakeFlows` runs I/O output mappings and data associations and takes the
  outgoing flow. A failed job with retries left backs off (ADR-0111) and, exhausted, raises an
  ADR-0061 incident. A send task's execution is *this*, unchanged.
- **A "service task by another type" is already precedented.** `TypeConnectorTask`
  (`compiler/process.go:37`) is documented as "a service task that delegates to a server-registered
  connector via the job path (ADR-0036); like a service task it creates a job and waits" — a
  **distinct compiled type that reuses `serviceTaskBehavior`**. The same registry-driven behaviors
  table (`p.behaviors[TypeServiceTask] = serviceTaskBehavior{}`, `engine/behavior.go:52`) can point
  `TypeSendTask` at the very same behavior value.
- **Activity machinery is generic.** Boundary events attach to any host the compiler admits via
  `isActivity` (`compiler/validation.go:599`); I/O mappings, data associations, and multi-instance
  are per-element wiring loops already applied to every task type (`compiler/parse.go`). Adding the
  send task to `isActivity` and those loops makes all of it work with no new mechanism.
- **Recovery is inherited.** A send task's only durable state is its job, an existing recoverable
  record; a parked send task rebuilds from the log exactly as a parked service task does.

So the job creation, completion, incident, backoff, boundary arming, I/O, data, multi-instance, and
recovery all already accommodate a send task. What is missing is the compiled type, the parse
wiring, dropping the two rejections, and the Modeler surfacing.

## Decision drivers

- **Reuse, don't reinvent.** A send task *is* a service task with a send-flavored label — build it
  on the same job path and the same behavior, not a parallel one. The outbound *doing* is a
  worker/connector's job, which already exists.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before visible (I2); a
  single `applyToState` live and on recovery (I4); the job type resolved at compile (I5); the job
  key and timestamps frozen into events so replay never regenerates them (I6) — all inherited
  unchanged from the service task.
- **Faithful BPMN.** A send task is an activity: it accepts boundary events, I/O and data mappings,
  and multi-instance, and it round-trips as a `<sendTask>` (its own palette entry, marker, and
  identity), not as a service task in disguise.
- **The kind determines durability.** For the **job/connector** kinds the token **waits** for the
  worker to complete the job, so a send that fails backs off and ultimately raises an incident
  rather than being silently lost — the whole point of a durable engine. The **message** kind is a
  correlating throw: it publishes and flows straight on (a durably-logged event, but no waiting),
  the message intermediate throw's semantics. Both are legitimate "sends"; the author picks.
- **One outbound element, kind chosen at author time.** Rather than making the author choose between
  a send task and a message throw event, the send task carries the choice — dispatched by the
  compiler on the `messageRef` / connector / `taskDefinition` it finds, exactly as a service task
  already dispatches on its connector extension.
- **Keep it compile-gated.** A send task with no kind at all (no `messageRef`, no connector, no task
  definition) cannot execute, so it stays a deploy error (like a service task with no
  `taskDefinition` type) rather than a silent no-op.

## Considered options

1. **A distinct `TypeSendTask` element that dispatches to `serviceTaskBehavior` (chosen).** Parse
   `<sendTask>` into an `xmlSendTask` with the service task's activity shape (`TaskDefinition`,
   connector extensions, `IOMapping`, `MultiInstance`, `DataOut`, `DataIn`); compile it to a new
   `TypeSendTask` node carrying a `ServiceTaskDetail` (reusing the existing detail table and its
   `AddServiceTask`-shaped builder); register `p.behaviors[TypeSendTask] = serviceTaskBehavior{}`.
   Add it to `isActivity` and the data/I/O/multi-instance wiring loops. Drop it from the compiler's
   unsupported list and the Modeler's `UNSUPPORTED_TYPES`. This is exactly what `TypeConnectorTask`
   does — a separate type, the same behavior — so it preserves the element's BPMN identity at zero
   runtime cost.
2. **Compile `<sendTask>` down to `TypeServiceTask` (no new type).** Rejected: simplest, but the
   element loses its identity — a deployed send task would report and (on any type-driven rendering)
   round-trip as a service task, and there would be nowhere to hang send-specific semantics
   (message correlation) if they are wanted later. This mirrors ADR-0102's rejected option 2: the
   element-type distinction is real and should survive, and `TypeConnectorTask` already sets the
   "distinct type, shared behavior" precedent, which costs nothing.
3. **Send task as a message throw (standard-BPMN `messageRef`, correlating fire-and-forget send) —
   adopted as an *additional kind*, not the primary one.** A `messageRef` send task is the message
   intermediate throw's semantics in task form: on activation it correlates the referenced message
   (waking any waiting catch/receive) and flows straight on. It compiles directly to
   `TypeMessageThrowEvent` and reuses `messageThrowEventBehavior` with **no new runtime code** —
   the same "distinct authoring, existing behavior" move the job/connector kinds make. It is *not*
   the primary semantics (the job/connector send is the gap modelers hit, and is how Camunda 8 /
   Zeebe treat a send task), but offering it as a kind makes the send task Atlas's single outbound
   element: the author picks message vs. e-mail vs. REST vs. job worker in one place, rather than
   choosing between a send task and a message throw event. Because a throw is instantaneous
   (correlate, then flow on), the message kind does not host boundary events or carry I/O/data/MI —
   those apply to the job and connector kinds, which are activities.

## Decision outcome

Chosen: **option 1 as the primary kind, plus option 3 as an additional kind** — the send task is
Atlas's single outbound element, dispatched by the compiler on the kind it finds. A job/connector
send task is a distinct `TypeSendTask` activity whose behavior is `serviceTaskBehavior`, reusing the
job path, incident model, and activity machinery wholesale; a `messageRef` send task compiles to
`TypeMessageThrowEvent`, reusing `messageThrowEventBehavior`. The genuinely new logic is (a) an
`xmlSendTask` parse shape (the service task's activity shape plus a `messageRef`) and a compiled
`TypeSendTask` reusing `ServiceTaskDetail`, (b) adding the job/connector send task to `isActivity`
and the data/I/O/multi-instance wiring loops, (c) one line pointing `TypeSendTask` at the existing
`serviceTaskBehavior` and a `registerScope` branch routing `messageRef` to `AddMessageThrowEvent`,
and (d) dropping the "unsupported" rejections and surfacing the kind picker in the Modeler.

### Compiler

- Parse `<sendTask id>` into an `xmlSendTask` mirroring `xmlServiceTask`'s activity shape
  (`TaskDefinition xmlTaskDefinition`; the `Clio`/`Rest`/`Mail`/`SharePoint`/`Remedy` connector
  extensions; `IOMapping`, `MultiInstance`, `DataOut`, `DataIn`), replacing the `SendTasks
  []xmlNode` placeholder (`compiler/parse.go:854`).
- Add `TypeSendTask` to the `BpmnType` enum + `String()` and grow `numBpmnTypes`
  (`compiler/process.go`). Reuse `ServiceTaskDetail` and an `AddSendTask(jobType, retries)` builder
  that appends to the existing service-task detail table (or a parallel `sendTasks` table if the
  detail must record the send label; the detail itself is identical), plus a `SendTask(detail)`
  accessor.
- In `registerScope`, dispatch each `<sendTask>` on its kind: a **`messageRef`** resolves through
  `resolveMessage` (the intermediate throw's path) and registers `AddMessageThrowEvent(name, key)`
  → `TypeMessageThrowEvent`; otherwise it is a job/connector send — a connector extension takes the
  connector path (`AddConnectorTask`, `TypeConnectorTask`), else a `taskDefinition` type is required
  (empty is a deploy error, mirroring the service task's `"has no task definition type"`) and the
  node is `AddSendTask(type, retries)` → `TypeSendTask`. Remove `sendTask` from the "unsupported
  element" list. Add `TypeSendTask` to `isActivity` and call `wireDataOut`/`wireDataIn`/`wireIO`/
  `wireMI` over the job/connector send tasks so boundary attachment, data associations, I/O
  mappings, and multi-instance apply; the message kind (an instantaneous throw) is skipped from
  those loops, exactly as a message throw event is.

### Runtime

- `p.behaviors[compiler.TypeSendTask] = serviceTaskBehavior{}` (`engine/behavior.go`). No new
  behavior type: on activation it creates a job on the resolved `(JobType, Retries)` and waits; on
  completion it `completeAndTakeFlows`. Retry backoff (ADR-0111), incident-on-exhaustion (ADR-0061),
  boundary interruption, and recovery are inherited unchanged because they key off the job and the
  element instance, not the element type.

### Modeler

- Drop `"bpmn:SendTask"` from `UNSUPPORTED_TYPES` (`api/web/editor.js`) so the badge and Problems-bar
  warning clear. The Implement panel offers the send task a **kind picker** — the service task's
  `serviceTaskKindHTML` connector/job-worker catalog, plus a **"Message"** entry that writes a
  `messageRef` (the shared-message picker `messageFieldsHTML`, as on a message throw/receive) and
  strips any `taskDefinition`/connector extension. Gated on `bo.$type === "bpmn:SendTask"` alongside
  `"bpmn:ServiceTask"`. bpmn-js already draws the send task, offers it in the palette and replace
  menu, and round-trips it through the moddle, so no vendored-editor change is needed.

### Phased implementation plan (test-first)

- **Phase 1 — Compile.** `xmlSendTask` parse shape; `TypeSendTask` enum + `String()` +
  `numBpmnTypes`; `AddSendTask`/`SendTask` (over `ServiceTaskDetail`); `registerScope` compiling a
  send task like a service task (task-definition **and** connector paths); `isActivity` + the
  data/I/O/multi-instance wiring; drop the parse/`registerScope` rejection. *Tests:* a `<sendTask>`
  with a `taskDefinition` compiles to `TypeSendTask` with the right job type/retries; a connector
  send task compiles to `TypeConnectorTask`; a bare send task with no task definition is a deploy
  error; **replace** the existing `TestParseUnsupportedElementMessage` send-task case (it currently
  asserts a send task is rejected — flip it to assert a send task now compiles, and move the
  "unsupported" assertion to a still-unsupported element so the actionable-message test still runs).
- **Phase 2 — Runtime.** Wire `TypeSendTask` to `serviceTaskBehavior`. *Tests:* a send task creates
  an activatable job a worker pulls and completes, taking the outgoing flow; a worker failure with a
  backoff parks and re-activates (ADR-0111); exhausted retries raise an incident (ADR-0061); a
  **boundary timer** on a send task times the send out; a **recovery test** — a parked send-task job
  rebuilds from the log and completes after replay. Confirm no new per-command allocation (I1).
- **Phase 2.5 — Message kind (compile).** Add `messageRef` to the send-task parse shape; in
  `registerScope`, a `messageRef` send task registers `AddMessageThrowEvent` (→ `TypeMessageThrowEvent`,
  reusing `messageThrowEventBehavior`); gate the data/I/O/MI wiring loops so a message-kind send task
  is skipped (an instantaneous throw, like a message throw event). *Tests:* a `<sendTask messageRef>`
  compiles to `TypeMessageThrowEvent` with the referenced message's name/key; an unknown `messageRef`
  is a deploy error; a send task with neither a kind nor a `messageRef` is a deploy error; **runtime:**
  a message-kind send task wakes a waiting receive task and flows on (the throw path, reached through
  a send task).
- **Phase 3 — Modeler + docs.** Drop `bpmn:SendTask` from `UNSUPPORTED_TYPES`; offer the kind picker
  (connector/job-worker catalog **plus a Message entry** writing `messageRef`) on a send task; a
  round-trip test per kind (author → deploy → the send task survives as `<sendTask>` with the right
  extension/attribute). Update this ADR to **Accepted/Delivered**, `docs/adr/README.md`, and the
  ROADMAP task-type note. Full sequence green: `go test -race ./...`, `go vet ./...`, `gofmt -l .`,
  and `./scripts/check-coverage.sh 95`.

### Consequences

- **Positive:** Atlas runs the last standard BPMN task type — with connectors, boundary timeouts,
  I/O and data mappings, multi-instance, retry backoff, and incidents — on the job machinery it
  already has. One compiled type dispatching to an existing behavior (the `TypeConnectorTask`
  pattern); no new job path, value type, behavior, or recovery path. The send task becomes the
  semantically-correct home for outbound-send connectors, so a model reads as "send an e-mail" on a
  send task rather than a service task.
- **Negative / trade-offs accepted:** a second compiled type (`TypeSendTask`) whose behavior is
  identical to the service task's — the (near-zero) cost of preserving the send-vs-service identity.
  The send task's runtime shape now **depends on its kind**: job/connector kinds are activities that
  wait (boundaries, I/O, MI, incidents); the message kind is an instantaneous throw that does not
  host boundaries or carry I/O/MI. This asymmetry is inherent to what each kind *is* (a throw never
  waits, so a boundary on it could never fire) and is documented, but it means "send task" is not one
  uniform runtime contract — the Implement picker, not the element, tells you which.
- **Follow-ups / risks to watch:** a send/receive **task pair** (message-kind send task ↔ receive
  task, ADR-0102) is now a pure modeling convention, not new engine work. A boundary event drawn on
  a message-kind send task is caught at deploy with a **targeted** error (it names the send task and
  points at the fix — switch to a job/connector kind, or model the wait with a receive task and a
  boundary timer), rather than the generic "attaches to a non-activity".

### Operation references (`operationRef`) — an alternate spelling of the message kind

A `<sendTask operationRef="Op">` names a `<bpmn:operation id="Op"><inMessageRef>Msg</inMessageRef></bpmn:operation>`
inside a `<bpmn:interface>` — the WSDL-style service-interface model some BPMN tools emit. Atlas
does not run WSDL interfaces; instead it reads the operation only to resolve `operationRef` to the
operation's `inMessageRef`, so an operationRef send is compiled as **exactly** the message kind: a
`resolveSendTaskOperations` pre-pass (beside `foldTransactions`, before `registerScope`) rewrites
the send task's `operationRef` to that message id, and everything downstream — the throw
compilation, the boundary rejection, the wiring skip — applies unchanged. A `<bpmn:interface>` /
`<bpmn:operation>` is parsed at the definitions level; an unknown operation, an operation with no
`inMessageRef`, or a send task setting **both** `messageRef` and `operationRef` is a deploy error.
The operation's **`outMessageRef` (a response) is not supported** — an operationRef send is
fire-and-forget, like a messageRef send; a request/response (wait for the reply) would need response
correlation and is out of scope. `operationRef` is a **deploy-time compatibility** path for imported
models; the Modeler authors the message kind via `messageRef`, not `operationRef`.

## Pros and cons of the options

### Option 1 — distinct `TypeSendTask` dispatching to `serviceTaskBehavior` (chosen)
- Good: reuses the job path, incident model, backoff, boundary/I/O/data/MI machinery, and recovery;
  preserves the send-task identity; follows the established `TypeConnectorTask` "distinct type,
  shared behavior" precedent; one behavior-table line of new runtime.
- Bad: a compiled type whose runtime behavior duplicates none but *aliases* the service task's — a
  type that exists mostly for identity.

### Option 2 — compile `<sendTask>` to `TypeServiceTask` (rejected)
- Good: zero new element type.
- Bad: the send task loses its BPMN identity (reports/renders as a service task) and leaves nowhere
  to hang send-specific semantics later; contradicts the `TypeConnectorTask` precedent.

### Option 3 — send task as a message throw (adopted as an additional kind)
- Good: matches strict-BPMN `messageRef` send semantics; reuses `TypeMessageThrowEvent` and
  `messageThrowEventBehavior` with no new runtime code; makes the send task the single outbound
  element (message vs. connector vs. job worker chosen in one Implement picker).
- Bad: not the *primary* send (the job/connector send is the real gap), so it is offered alongside,
  not instead; and it makes "send task" a non-uniform runtime contract — the message kind is an
  instantaneous throw (no boundaries, no I/O/MI), unlike the activity-shaped job/connector kinds.

## Links

- reuses ADR-0036 (connector tasks — the `TypeConnectorTask` "distinct type, shared behavior"
  precedent this follows) and the service-task job path; inherits ADR-0061 (incident model) and
  ADR-0111 (retry backoff), ADR-0040 (boundary arm/fire), ADR-0068 (I/O mappings), ADR-0058/0059
  (data associations), and ADR-0077 (multi-instance) — all generic over activities
- relates to ADR-0102 (receive tasks — the wait-shaped sibling; the same "distinct activity type,
  reuse the machinery" design), and ADR-0020/0052 (message throw/end events — where a
  fire-and-forget correlating send lives instead)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery test up front)
