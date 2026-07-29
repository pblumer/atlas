# ADR-0076: Call activities (single-partition)

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

> **Implementation status.** Phases 1–2 delivered — a call activity runs end to end:
> it starts the called process as a child instance, passes variables in and out, and
> resumes the caller on the child's completion. Phase 3 (termination propagation,
> Modeler editor) pending. Each phase was test-first with a recovery test (ADR-0018).
> Cross-partition call activities are **out of scope** (ADR-0006, Milestone 5); the
> child runs in the caller's partition.
>
> **Delivered (Phase 1, compiler):** `TypeCallActivity` + `CallActivityDetail`
> (called process id, binding, `propagateAll*` flags); parse of `<callActivity>` /
> `<zeebe:calledElement>` and its I/O mappings; `processId` required.
>
> **Delivered (Phase 2, runtime):** `callActivityBehavior` — on activation it
> resolves the called def key (a `processId → latest defKey` index maintained on the
> `Processor` in `Deploy`, rebuilt oldest-first on recovery), builds the child's
> start variables (all caller variables when `propagateAllParent`, plus the
> input-mapped locals, which win on name clash — or *only* the input-mapped locals
> when propagation is off, i.e. isolation), and spawns the child via
> `AppendCreateChildInstanceCommand`, then parks. `model.ProcessInstanceValue` gains
> an append-compatible `ParentElementInstanceKey`; the child records it. When the
> child completes, `completeScope`'s root branch calls `resumeCaller`, which promotes
> the child's variables to the caller (all when `propagateAllChild`, else the output
> mappings evaluated over the child's variables) and resumes the caller's call-activity
> element. The generic output-mapping promotion is skipped for a call activity (its
> outputs come from the child, not a local scope). Verified: isolation both ways,
> propagate-all both ways, and a child parked on a job that recovers across a crash
> and still resumes its caller.

## Context and problem statement

A **call activity** (`bpmn:callActivity`) invokes a *separate* process definition
as a **child instance**: a token reaching it starts a new instance of the called
process, waits for that instance to finish, then continues. It differs from an
embedded subprocess (ADR-0074), which nests a scope inside the *same* instance:

- **Reuse & versioning.** The called process is deployed independently and
  referenced by its bpmn process id with a **binding** (latest / this deployment),
  so one process can be called by many, each pinning or floating its version.
- **True variable isolation.** By default a call activity may propagate all
  variables both ways, but with propagation off it passes in *only* the
  input-mapped variables and returns *only* the output-mapped ones — the
  "the child sees only what I hand it" semantics an embedded subprocess does *not*
  give (an embedded subprocess always inherits the parent scope — see ADR-0074).

Atlas has no `callActivity` today — no compiled type, no behavior. This ADR
decides how to execute one, reusing the machinery already in place.

What already exists and is load-bearing:

- **Instance creation from inside the engine.** `AppendCreateInstanceCommand`
  (`engine/context.go:303`) appends a `VTProcessInstance/IntentActivating` followup
  that runs in the next batch on the same processor goroutine, seeded with
  `StartVars` — exactly how a message-start instantiates a process (ADR-0035).
- **The park-then-resume pattern.** A service task parks in `OnActivated` (creates
  a job, no `Completing`) and is resumed by `handleJobCompleted` with
  `AppendElementCommand(elementInstanceKey, IntentCompleting, …)` after writing the
  job's outputs into the instance. A call activity is the same shape, resumed by
  the child's completion instead of a job's.
- **The binding model (ADR-0063).** A business rule task resolves a decision by
  `latest`/`deployment` binding; the same `DecisionBinding` type and
  `bindingType` parse apply to a called process.
- **Single writer / same partition (ADR-0002/0006).** The child create is a
  followup on the caller's own goroutine, and `c.NewKey()` mints the child instance
  key in the caller's partition — no cross-partition routing.

What is missing: a `ParentElementInstanceKey` on the process instance (so a child
knows its caller), a `processId → latest defKey` index on the processor (for
`latest` binding), and the call-activity behavior plus the child-completion resume.

## Decision drivers

- **Reuse, don't reinvent.** Build on `AppendCreateInstanceCommand`, the
  job-style park/resume, the `DecisionBinding` model, and the generic
  `zeebe:ioMapping` evaluation (ADR-0068).
- **Invariants hold.** No per-command hot-path allocation (I1); durable before
  visible (I2); one `applyToState` (I4); structure resolved at deploy (I5);
  deterministic replay (I6) — the caller link is frozen into the child's
  `ProcessInstance` event, and `latest` binding resolves from a Deploy-maintained
  map rebuilt oldest-first on recovery (the ADR-0063 argument).
- **Single-partition first.** The child runs in the caller's partition; a
  cross-partition call activity is a separate, later concern (ADR-0006).
- **Faithful Zeebe semantics.** `zeebe:calledElement` with `processId`,
  `bindingType`, and `propagateAllParentVariables` / `propagateAllChildVariables`;
  input mappings shape what goes in, output mappings what comes back.

## Considered options

1. **Child process instance linked by a caller key (chosen).** The call activity
   spawns a real child instance via the existing create path; the child carries a
   `ParentElementInstanceKey`; on the child's completion the caller's element is
   resumed and outputs promoted. Reuses instance creation, park/resume, binding,
   and recovery wholesale.
2. **Inline expansion (copy the called process's nodes into the caller).**
   Rejected: breaks independent versioning/binding, explodes the compiled graph,
   and gives no isolation — it is an embedded subprocess by another name.
3. **Cross-partition child from day one.** Rejected for now: correct eventual
   design (ADR-0006) but adds message-passing, routing, and distributed completion
   before the single-partition case even works. Deferred to Milestone 5.

## Decision outcome

Chosen: **option 1 — a call activity spawns a child process instance in the
caller's partition, linked by a caller key, and resumes the caller on the child's
completion.**

### Compiler (Phase 1)

- Add `TypeCallActivity` to the `BpmnType` enum (an activity, so it is a valid
  boundary-event host — `isActivity`).
- Parse `<callActivity>` with `<zeebe:calledElement processId="…"
  bindingType="latest|deployment" propagateAllParentVariables="…"
  propagateAllChildVariables="…"/>` and its `zeebe:ioMapping`. Compile a
  `CallActivityDetail{ CalledProcessId int32 (interned), Binding DecisionBinding,
  PropagateAllParent bool (default true), PropagateAllChild bool (default true) }`.
  `processId` is required (empty is a deploy error). The I/O mappings wire through
  the same generic path subprocesses use.
- Validation: a call activity with no `processId` fails deploy. Whether the called
  process exists is a *deploy-time* check (the caller may deploy before the
  callee), surfaced by the server, not the pure compiler.

### Runtime (Phase 2)

- `callActivityBehavior.OnActivated`: resolve the called `defKey` — for
  `deployment` binding, the defKey frozen at deploy; for `latest`, from a new
  `processId → defKey` map maintained on the `Processor` in `Deploy`
  (rebuilt oldest-first on recovery, I6). Evaluate the variables to pass in —
  `propagateAllParent` ? the caller's instance variables, else only the input
  mappings — and `AppendCreateInstanceCommand(childDefKey, startVars, parentKey)`
  (a new variant that carries the caller's element-instance key). Then **park**
  (no `Completing`).
- `model.ProcessInstanceValue` gains `ParentElementInstanceKey uint64`
  (append-compatible encode/decode, ADR-0017); `handleProcessInstanceActivating`
  sets it from the create command.
- Child completion: in `completeScope`'s root branch, if the completing instance
  has a non-zero `ParentElementInstanceKey`, write the child's outputs into the
  caller's instance (`propagateAllChild` ? all child variables, else the output
  mappings) and `AppendElementCommand(parentKey, IntentCompleting, …)` to resume
  the caller — structurally identical to `handleJobCompleted`. The call activity's
  `OnCompleting` takes its outgoing flow.

### Termination & Modeler (Phase 3)

- Terminating/interrupting a call activity terminates its child instance
  (`handleProcessInstanceTerminating` on the child, found via the caller link).
- Modeler: an Implement-panel editor for the called process id, binding, propagation
  toggles, and I/O mappings (reusing the ADR-0068 io-mapping editor).

### Consequences

- **Positive:** real process reuse and opt-in variable isolation, built almost
  entirely on existing seams (instance creation, park/resume, binding, io mapping);
  no new value type — the child is an ordinary `ProcessInstance` with one extra
  field; recovery is inherited.
- **Negative / trade-offs accepted:** a `VTProcessInstance/IntentCompleted` follow-on
  now exists (resume the caller) where completion was previously a pure state-apply —
  it must stay a pure function of the child's persisted `ParentElementInstanceKey`.
  The processor gains a `processId → latest defKey` index (small, Deploy-maintained).
  Single-partition only; a called process in another partition is unsupported until
  Milestone 5.
- **Follow-ups / risks to watch:** define behavior when a `latest`-bound callee is
  redeployed mid-flight (the running child keeps its version; new calls float);
  guard against a call cycle (A calls B calls A) — bound by instance depth or a
  deploy-time cycle check; decide `versionTag` binding (ADR-0074's version tag) later.

## Pros and cons of the options

### Option 1 — child instance linked by caller key (chosen)
- Good: reuses creation/park/resume/binding/recovery; no new value type; faithful
  Zeebe semantics; true isolation via propagation flags.
- Bad: adds a completion follow-on and a processId→defKey index; single-partition
  only for now.

### Option 2 — inline expansion (rejected)
- Good: no new runtime lifecycle.
- Bad: no independent versioning/binding, no isolation, graph blow-up — it is an
  embedded subprocess, not a call activity.

### Option 3 — cross-partition from day one (rejected for now)
- Good: the eventual scalable design.
- Bad: distributed creation/completion and routing before the basic case works;
  deferred to Milestone 5 (ADR-0006).

## Links

- builds on ADR-0035 (message-start `AppendCreateInstanceCommand`), ADR-0063
  (decision binding — latest/deployment), ADR-0068 (I/O variable mappings),
  ADR-0074 (embedded subprocesses — the park/resume and scope model), ADR-0017
  (instance history on completion, append-compatible instance encoding)
- honors I1, I2, I4, I5, I6 and ADR-0002/0006 (single writer; the child runs in the
  caller's partition, cross-partition deferred)
- ROADMAP Milestone 3 "Call activities (single-partition)"; Milestone 5
  "Cross-partition … call activities"
