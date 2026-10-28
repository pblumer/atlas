# ADR-0508: Scope-aware messaging, event-subprocess runs and compensation from an event subprocess

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-28
- **Deciders:** Atlas engine team
- **Open question:** BPMN 2.0.2 says a compensation event "within" an event subprocess and a cancel end event "within" a transaction without saying how deep "within" reaches. This record reads both as *directly in the event subprocess, whose scope is the one compensated or cancelled* — the reading its authors understand Camunda 8 to document for compensation, unverified against a running Camunda. Neither reading has been checked against a second engine; if one rejects a cancel end in an event subprocess or compensates only the event subprocess's own run, a model using it does not port.
- **Question checked:** 2026-10

## Context and problem statement

A repair order in a garage runs for weeks: the customer approves an estimate, parts are
ordered, the car is taken apart. While the work runs, the mechanic may report further
findings — each needs its own approval, while the main work goes on — and the customer
may cancel at any point, after which everything already done has to be undone before the
effort is billed. Modelled the way BPMN intends, that is: a non-interrupting message event
subprocess per finding, correlating the customer's answer on the finding's id; and a
cancellation handled by an event subprocess that rolls back what was done.

Atlas ran that model wrong in six places. Each looked locally reasonable:

1. **Correlation keys were evaluated on the instance root only**
   (`evalCorrelationKey(c, e, ei.ProcessInstanceKey)`, at every catch, receive task,
   boundary, event-subprocess trigger and throw). A key could not see a variable local to
   the waiting element's subprocess, its event-subprocess run or its own input mappings.
   Two runs of one event subprocess waiting at once subscribed with whatever the root last
   held, so the answer to the first finding reached nobody and the answer to the second
   reached both.
2. **A message's or signal's payload was always written to the root**, also when a scope
   between the receiver and the root declared the variable. The model's local
   `approved = null` shadowed the delivered `true`, and the gateway reading it declined.
3. **A run of a non-interrupting event subprocess read its message's payload through the
   root.** Two messages delivered before the first run activated gave both runs the
   second one's values.
4. **A non-interrupting message or signal trigger re-armed two batches after it fired**
   (a followup activation). A second message for it in that window found no subscription
   and, without a time to live, was lost — two findings reported back to back ran one
   handler.
5. **A compensation throw in an event subprocess compensated the event subprocess's own
   run**, which holds nothing compensable, instead of the scope whose work the
   cancellation has to undo. And a throw went on at once, though BPMN's
   `waitForCompletion` defaults to true (parsed but not honoured since ADR-0103).
6. **A cancel end event had to sit directly in its transaction.** The place BPMN gives a
   cancellation that can arrive at any point — an event subprocess inside the
   transaction — was refused at deploy.

## Decision drivers

- A model drawn the way BPMN and the Camunda modeler intend must run as it reads.
- Deployed models keep working: no change for a model that never hit one of the defects.
- The invariants hold: decisions on the command path, frozen into events, one
  `applyToState` (I4/I6); no per-command allocation added to the hot path (I1).

## Considered options

1. **Fix each place where it is wrong, with the semantics FEEL evaluation and Zeebe already
   use elsewhere** (scope chain for keys, nearest-declaring-scope propagation for payloads).
2. Make every message and signal payload local to the receiving element's scope, like a
   worker result (ADR-0074).
3. Leave the engine; document workarounds (call activities per finding, a parallel
   catch-and-terminate branch for cancellation, a time to live on every message).

## Decision outcome

Chosen option: **1**.

- **Correlation keys resolve up the scope chain** from the waiting or sending element:
  its own instance (so its input mappings are visible), a boundary's host, a trigger's
  scope — `bindInputsChain`, as every other model expression already does (ADR-0068).
- **Payload propagation (Zeebe's rule).** A receiver with zeebe:ioMapping outputs gets the
  payload in its own local scope, and its output mappings decide what escapes — as a
  service task's result does (`ioResultScope`). Otherwise each variable updates the
  nearest scope, from the receiver's flow scope up, that already declares it; a variable
  no scope declares lands on the root, as every payload did before. Messages and signals
  alike (`writePayload`).
- **A trigger keeps a copy of its payload**, which the run it starts takes over
  (`bindTriggerPayload`) before its input mappings run. The root copy is unchanged, so a
  main flow reading what a non-interrupting event subprocess received keeps working.
- **A fired non-interrupting message or signal trigger is re-armed in the step that fires
  it** (`rearmFiredTrigger`) — its Activated event and subscription are in state before
  the next command. A delivery out of the message buffer runs inside a trigger's own
  subscribing and keeps the old re-arm on completion, guarded so only one trigger is
  armed (`triggerArmed`).
- **Compensation scope.** A compensation throw or end event directly in an event subprocess
  compensates the scope that event subprocess handles (`compensationScope`).
- **waitForCompletion is honoured, default true.** A throw or end event that started
  handlers stays Activated; when a compensation handler finishes and none is left running
  in that scope, every live, incident-free compensation event compensating it is driven
  on (`releaseCompensationWaiters`). The wait is derived from state — no new record — and
  replays as the handlers' and the throw's events do. `waitForCompletion="false"` keeps
  the old behaviour. `CompiledNode.CompensationHandler` (set at compile, I5) makes the
  check O(1) per completion.
- **Cancel end in an event subprocess directly inside a transaction** is admitted by the
  compiler (`cancelEndTransaction`). At run time the cancel end terminates the
  transaction's other work, sparing itself and the handler run it sits in, compensates the
  transaction, and completes; `applyToState` hands the cancelling mark from the completing
  handler run to its flow scope — the only subprocess that can complete while marked —
  so the drained transaction routes out its cancel boundary.

### Consequences

- **Positive:** the repair order runs as drawn: findings correlate per run, answers reach
  the variable the gateway reads, back-to-back findings each start a run, and a
  cancellation rolls back exactly what was done before the effort is billed.
  Conformance scenarios `event-subprocess-concurrent-runs`,
  `transaction-cancel-event-subprocess` and `compensation-event-subprocess` pin it.
- **Negative / trade-offs accepted:**
  - *Behaviour change, waitForCompletion:* a compensation throw in a deployed model now
    waits for its handlers before going on. Where a model relied on the next step running
    beside the rollback, it says `waitForCompletion="false"`.
  - *Behaviour change, output mappings on a receive task or catch event:* the raw payload
    no longer reaches the root beside the mapped values.
  - *Behaviour change, shadowing:* a key or payload variable that a local scope declares
    is now read or written there, not on the root. A model only meets this where it hit
    defect 1 or 2.
  - A trigger's payload is written twice (root and run), so it counts twice against the
    variable budget (ADR-0294).
  - `applyToState` reads the cancelling mark once per completing subprocess.
- **Follow-ups / risks to watch:** compensation data (a handler reading the compensated
  activity's inputs) and compensation across a call activity stay open from ADR-0103.
  A reference example of the repair order under `examples/`, with its handbook card,
  belongs to a follow-up.

## Pros and cons of the options

### Option 1 — fix in place, Zeebe semantics
- Good: no change for models that never hit a defect; the same rule FEEL already uses;
  a model authored for Zeebe behaves the same in Atlas.
- Bad: three narrow behaviour changes (above) for models that did.

### Option 2 — payload always local to the receiver
- Good: one rule for jobs and messages.
- Bad: breaks every model whose main flow reads what a message inside a subprocess or a
  non-interrupting event subprocess delivered — a common pattern (an address update
  arriving while an order runs).

### Option 3 — workarounds only
- Good: no engine change.
- Bad: every modeller rediscovers six traps; a message without a time to live is still
  silently lost; the model no longer reads as the process.

## Links

- relates to ADR-0068 (I/O mappings and scope resolution), ADR-0074 (subprocess scopes),
  ADR-0082 (event subprocesses), ADR-0088 (signals), ADR-0103 (compensation),
  ADR-0108 (transactions), ADR-0294 (variable budget), ADR-0370 (message buffer)
