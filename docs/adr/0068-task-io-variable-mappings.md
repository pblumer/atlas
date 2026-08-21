# ADR-0068: Task input/output variable mappings with activity-local scopes

- **Status:** Proposed
- **Date:** 2026-07-27
- **Deciders:** Atlas engine team

> **Implementation status.** Partially implemented. This ADR decides the shape; the
> work is phased (scope-chain variable resolution → compiler parse → engine
> apply → UI), each phase test-first with a recovery test. Delivered: scope-chain
> resolution (`engine.ProcessingContext.ResolveVariable`), compiler parsing of
> generic `zeebe:ioMapping` input/output (`compiler.IOMapping`, per-node
> `IOInputs`/`IOOutputs`), and the engine apply — input mappings write an
> activity-local scope on activation, output mappings promote to the parent scope on
> completion, and the local scope is dropped via `VariableDeleted` events; the inline
> script task and the polyglot script worker read their inputs up the scope chain.
> Also delivered: the modeler properties-panel I/O-mapping editor (input and output
> mapping lists on service, script, and user tasks), and the worker scope-chain read
> across every job worker — script, DMN, CSV, and now the whole of `connector/` plus
> the user-provisioning connector, which read a single flat scope and so ignored their
> own task's input mappings. Where a connector's payload *is* a variable scope (the
> clio event body, the REST request body, the SCIM body with no body variable named),
> a task's input mappings are that payload
> (ADR-draft-connector-payloads-are-the-input-mapping).

## Context and problem statement

An activity needs to shape the data that flows into and out of it. Today Atlas
offers only the coarsest version of this:

- **Input:** a task sees the *entire* process-instance variable scope. A script
  task's worker reads every variable; a gateway condition and a script expression
  bind names from the one root scope (`bindInputs(c, names, ei.ProcessInstanceKey)`
  everywhere in `engine/behavior.go`).
- **Output:** a job's result is written straight back into that same root scope —
  a script task writes its single result variable there; a service/connector job
  merges whatever variables it returns.

There is no way to (a) feed a task a *renamed* or *computed* input without first
materialising it as a process variable, (b) keep a task's scratch variables out
of the process scope, or (c) promote only *selected*, possibly reshaped, outputs
back. This is exactly what Camunda/Zeebe **I/O mappings** provide:
`<zeebe:ioMapping>` with `<input source target>` and `<output source target>`,
where `source` is a FEEL expression and `target` a variable name, evaluated
against **activity-local** variable scopes.

Atlas already has most of the substrate:

- Element instances carry a **`FlowScopeKey`** (the parent scope) — a scope
  hierarchy already exists in the model (`model.ElementInstanceValue`).
- Variables are keyed by a **`ScopeKey`** (`model.VariableValue`), today always
  the process-instance key, but the field is general.
- A **FEEL-input-mapping** precedent exists: a business rule task compiles
  `<zeebe:ioMapping><input>` into `DecisionInputMapping{Target, Source *expr.Compiled}`
  evaluated over instance variables (ADR-0039).

What is missing is the generalisation: **local variable scopes per activity**, and
**variable resolution that walks the scope chain**. Everything reads one scope
today.

## Decision drivers

- **Faithful Camunda/Zeebe semantics.** Authors expect `zeebe:ioMapping`
  input/output to behave as it does in Camunda 8: input mappings create local
  variables the activity sees; output mappings promote selected values to the
  parent scope; unmapped names resolve up the scope chain.
- **Invariants hold.** FEEL sources compile at deploy (I5); mapping results are
  evaluated during command processing and *frozen into variable events* so replay
  re-applies them rather than re-evaluating (I6); evaluation must stay
  allocation-light on the processor path (I1); `applyToState` stays a pure
  variable-event apply (I4). Single writer per partition is untouched.
- **Reuse the existing model.** Build on `FlowScopeKey` and `VariableValue.ScopeKey`
  and the `DecisionInputMapping` compile path rather than inventing a parallel
  variable system.
- **Generality.** The same mechanism should serve every job-backed activity
  (service, script, business rule, user task) and, later, subprocess scopes — not
  a one-off per task type.

## Considered options

1. **Worker-side select/rename only (no engine scope).** Extend each worker to
   accept a per-task list of input names to pass and output names to write. No
   local scope; mappings live in the task detail and are applied by the worker.
   Simple, but: script-task-only unless every worker reimplements it; no
   isolation (scratch vars still land in the root scope); no faithful
   "unmapped names resolve up a chain"; output *promotion* semantics can't be
   expressed. Rejected as the general answer (though it is roughly what script
   tasks do today).
2. **Activity-local variable scopes + scope-chain resolution (chosen).** Give an
   activity instance its own variable scope (keyed by its element-instance key),
   parented by `FlowScopeKey`. Variable resolution walks child→parent→…→root.
   Input mappings write locals; output mappings promote to the parent. Generic
   across activities, and the same machinery a subprocess scope needs later.
3. **Flat scope with name prefixing** (e.g. `local:<elementInstanceKey>:name`).
   Avoids a hierarchy but leaks encoding into names, collides with real variable
   names, and has no clean "promote to parent" or "drop the locals" step.
   Rejected.

## Decision outcome

Chosen: **generic `zeebe:ioMapping` input/output on job-backed activities, backed
by activity-local variable scopes and scope-chain FEEL resolution (option 2).**

### Variable scopes and resolution

- A **scope** is identified by a key: the process-instance key (root) or an
  element-instance key (an activity-local scope). `VariableValue.ScopeKey` already
  carries this; today it is always the root.
- **Resolution** for a FEEL binding starting at scope `S`: for each needed name,
  read it from `S`; if absent, follow `S`'s owning element instance's
  `FlowScopeKey` to the parent scope and repeat, up to the root. The nearest
  binding wins (a local shadows an inherited value) — Camunda's rule. This is the
  one genuinely new engine capability; it replaces the single-scope
  `bindInputs(c, names, scope)` with a chain-walking `bindInputsChain(c, names, scope)`.
- An activity gets a **local scope only if it needs one** — i.e. it has input
  mappings, output mappings, or a worker that writes locals. Otherwise it keeps
  reading/writing the root scope exactly as today (no behaviour change, no extra
  bookkeeping for the common case).

### Input mappings (on activation)

For each `<input source="=…" target="name"/>`, compiled to `{Target, Source *expr.Compiled}`
at deploy time (I5), the behavior on activation evaluates `Source` over the
resolved chain from the activity's flow scope and writes `Target` as a variable at
the **activity-local scope**. The evaluated value is frozen into a
`VariableCreated` event (I6); `applyToState` just applies it. The task then reads
its inputs resolving from its local scope up the chain, so it sees the mapped
locals plus anything inherited.

### Output mappings (on completion)

On job completion the job's result variables are written to the **activity-local
scope** first (not the root). Then:

- **With output mappings:** for each `<output source="=…" target="name"/>`,
  evaluate `Source` over the local scope and write `Target` to the **parent
  (flow) scope**. Only these promoted values escape the activity; the raw result
  and the input locals are dropped when the local scope is discarded.
- **Without output mappings:** the job's result variables merge into the parent
  scope — today's behaviour, preserved for compatibility.

The activity-local scope is **discarded** (its variables deleted) when the
activity completes, after output mapping, via a deterministic
`VariableDeleted`/scope-drop event so replay reproduces it exactly (I6). This
keeps scratch data from accumulating.

### Consequences

- **Positive:** authors get Camunda-faithful data flow — feed a task a renamed or
  computed input, keep scratch variables local, and promote only selected,
  reshaped outputs. It is generic across job-backed activities and reuses the
  existing `FlowScopeKey`/`ScopeKey` model and the `DecisionInputMapping` compile
  path. The same local-scope machinery is what embedded subprocesses will need,
  so this is a step toward them, not a detour.
- **Negative / trade-offs accepted:** variable resolution now walks a chain rather
  than reading one scope — a per-read cost, bounded by scope depth (1 for
  flat processes today). The engine gains local-scope bookkeeping (create on
  demand, drop on completion) and a scope-drop event type. The
  "a script task sees the whole scope" behaviour changes **when input mappings are
  present** (then it sees the mapped locals + inherited), which is the intended
  Camunda semantics but is a behaviour change to document. Output mapping shifts
  where a job's raw result lands (local, not root) whenever output mappings exist.
- **Follow-ups / risks to watch:** reuse the local-scope mechanism for embedded
  subprocess scopes; a properties-panel I/O-mapping editor (reuse the DMN
  io-mapping input UI); measure chain-walk cost and consider caching the resolved
  binding per evaluation; define the interaction with first-class data objects
  (ADR-0053/0058/0059), which are a separate, typed state channel — I/O mappings
  are plain variables and should not silently write data objects; decide whether
  gateways/events (not just activities) get input mappings later.

## Pros and cons of the options

### Option 1 — worker-side select/rename (rejected as the general answer)
- Good: no engine change; simple for script tasks.
- Bad: per-worker duplication; no local isolation; can't express output promotion
  or "unmapped names resolve up a chain"; not generic.

### Option 2 — activity-local scopes + scope-chain resolution (chosen)
- Good: faithful Camunda semantics; generic across activities; reuses the existing
  scope/variable model; the substrate embedded subprocesses need anyway.
- Bad: introduces chain-walking resolution and local-scope lifecycle; a behaviour
  change where mappings are present; a new scope-drop event.

### Option 3 — flat scope with name prefixing (rejected)
- Good: no hierarchy.
- Bad: encoding leaks into names; collisions; no clean promote/drop.

## Links

- builds on the `FlowScopeKey` scope hierarchy in `model.ElementInstanceValue` and
  the `ScopeKey` variable model in `model.VariableValue`
- generalises the `DecisionInputMapping` FEEL-input-mapping compile path (ADR-0039)
- honors I1, I4, I5, I6 and ADR-0008 (FEEL compiled at deploy, evaluated without
  reparsing); frozen-result rule mirrors the script/DMN result handling (ADR-0047,
  ADR-0014)
- relates to ADR-0053/0058/0059 (first-class data objects) as a distinct,
  typed data channel that I/O mappings must not conflate with plain variables
- unblocks a future embedded-subprocess ADR, which reuses the activity-local scope
