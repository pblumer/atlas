# ADR-0077: Multi-instance activities (parallel and sequential)

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

> **Implementation status.** Phases 1–4 delivered; Phase 5 (Modeler) pending. Each
> phase lands test-first with a recovery test (ADR-0018). Multi-instance builds directly
> on the embedded-subprocess scope lifecycle (ADR-0074) and the call-activity child
> termination (ADR-0076); it introduces no new value type, record, counter, or recovery
> path.
>
> **Delivered (Phase 1, compiler):** a `MultiInstanceDetail` (input collection or
> cardinality, input element, output collection/element, completion condition,
> sequential flag) indexed by a new `CompiledNode.MultiInstance` field — the node keeps
> its real activity type, no new `BpmnType`. `<multiInstanceLoopCharacteristics>` (with
> Zeebe's nested `<zeebe:loopCharacteristics>`) is parsed on service, script, and user
> tasks, call activities, and subprocesses, and wired recursively over the scope tree
> (`wireScopeMI`, mirroring `wireScopeIO`). Every FEEL source compiles once at deploy
> (I5); a loop with neither an input collection nor a cardinality — or with both — is
> refused, as is an uncompilable source. Verified: the detail compiles with its fields
> (sequential/parallel default, optional output and completion condition, cardinality
> form), a multi-instance subprocess keeps its `TypeSubProcess` type and carries the
> loop, and the deploy-refusal branches.
>
> **Delivered (Phase 2, parallel runtime):** an append-compatible
> `ElementInstanceValue.MultiInstance uint8` role marker (0 none / 1 body / 2 inner).
> A flow into a multi-instance activity activates its **body** (`activateElement` sets
> the marker); `handleElementActivating` routes the body to `seedMultiInstance` instead
> of the node's real behavior. The body evaluates the input collection (or cardinality)
> once over its scope chain and seeds N **inner** element instances of the same node,
> each scoped under the body (`FlowScopeKey = body key`) with its `loopCounter`
> (1-based) and, when named, its input element written as variable events into the
> inner's own scope. Each inner runs the node's real behavior (a service task parks on
> its own job); `handleElementCompleting` routes an inner through
> `finishMultiInstanceIteration` — drop the iteration's locals, `Completed` (which
> decrements the body's `activeChildren`), then `completeScope(body)` — so an inner
> never takes an outgoing flow. When the last iteration drains, the body completes like
> any activity and takes its single outgoing flow. An empty collection / non-positive
> cardinality seeds nothing and completes the body at once. Recovery is inherited: the
> collection is evaluated only live, and the inner `Activated`/variable events plus the
> merge counter rebuild the iterations on replay. Verified: parallel fan-out over a
> three-element collection with per-iteration `item`/`loopCounter` bindings and a join;
> the cardinality form; the empty and degenerate-collection edges; and a crash+replay
> with every iteration parked on its job that still joins and finishes.
>
> **Delivered (Phase 3, sequential + output collection):** a **sequential** loop seeds
> only the first iteration; each completion seeds the next (in `loopCounter` order)
> until the set is exhausted, then the body completes — so exactly one iteration is
> live at a time. The **output collection** is order-preserving: `seedMultiInstance`
> initialises a slot-per-iteration list on the body scope, and each iteration writes its
> `outputElement` (a FEEL over the iteration's own variables) at its own index, so
> completion order does not matter; on body completion `promoteMultiInstanceOutput`
> promotes the assembled list to the enclosing scope and drops the body's locals. An
> iteration's own result is now **inner-scoped** (`ioResultScope` returns the inner key
> for a multi-instance iteration), so each iteration's `outputElement` reads *its* value
> rather than a value colliding at the shared body scope. Deviation from the plan below:
> the sequential "seed next" reads the iteration set by **re-deriving it from the
> committed scope chain** on each completion rather than freezing it into a body-scope
> variable — because seeding runs only live (never during replay), this is
> deterministic under I6 and equivalent to a one-time freeze for any model that does not
> mutate the collection source mid-loop. Verified: sequential runs one job at a time in
> index order; the output collection is assembled in input order for both parallel and
> sequential (`[1,2,3] → [10,20,30]`); and a sequential loop parked mid-sequence
> recovers and finishes the remainder.
>
> **Delivered (Phase 4, completion condition, interruption, nesting):** a
> **completion condition** is evaluated over each iteration's scope chain (so it reads
> `loopCounter`, the item, and the accumulating output collection) after the iteration
> completes; when it holds, the loop ends early — `terminateScope` cancels any
> still-running iterations (a no-op for a sequential loop) and the body completes.
> **Interruption** needed no new engine code: the body is a scope, so an interrupting
> boundary on a multi-instance activity runs `interruptHost` → `terminateScope`, which
> tears down every iteration; a fix hardened the sibling path so terminating a *process
> instance* (`handleProcessInstanceTerminating`) now also cancels each element's job,
> leaving no orphan in the activatable index — matching `terminateScope`. **Nesting**
> also composed for free: a multi-instance **subprocess** runs each iteration as a full
> subprocess instance (the inner dispatch runs `subProcessBehavior`), and a
> multi-instance **call activity** starts one child per iteration, an interrupt tearing
> each child down through the ADR-0076 `terminateChildInstance` that `terminateScope`
> already calls per victim. Verified: sequential and parallel completion conditions
> (early stop / cancel remaining); an interrupting boundary terminating all iterations
> and routing out its flow; a multi-instance subprocess assembling an output collection;
> and a multi-instance call activity fanning out children and, on interrupt, terminating
> every child.

## Context and problem statement

A **multi-instance activity** runs one activity — a task, an embedded subprocess, or
a call activity — **N times**, where N is data-driven: once per element of a
runtime collection (`for each order line …`) or a fixed cardinality. The N runs are
either **parallel** (all at once, join when every one finishes) or **sequential**
(one after another). Each run gets its own copy of a loop variable bound to its
element; the activity's node completes and its outgoing flow is taken only when the
runs have all finished (or a completion condition cuts them short). It is one of the
most common constructs in real models and the last big control-flow item of
ROADMAP Milestone 3 after subprocesses (ADR-0074) and call activities (ADR-0076).

Atlas cannot execute one today. `<multiInstanceLoopCharacteristics>` is not parsed —
it is absent from the activity `xml*` structs, so `encoding/xml` silently drops it and
the activity deploys and runs exactly **once**. There is no compiled loop marker and
no behavior.

The question this ADR answers: **how do we run an activity N times without inventing
a parallel token/scope/recovery subsystem** — reusing the scope machinery ADR-0074
already put in place (a scope is an element instance; a per-scope `activeChildren`
counter drains to completion; interruption tears the scope down).

What already exists, and is load-bearing for the answer:

- **A scope is an element instance; it completes when its child counter drains.**
  ADR-0074 made an embedded subprocess "an element instance that is its own scope":
  its element-instance key is the `FlowScopeKey` its children carry, and the
  `activeChildren:<scopeKey>` merge counter (`state/keys.go`, `state/tx.go`,
  recovery-safe via `state/merge.go`) is incremented on every child `Activated` and
  decremented on `Completed`/`Terminated` in `applyToState` — for *any* scope, not
  just the root (`engine/apply.go`). `completeScope(c, scope)` drives the scope's
  owner (a subprocess element, or the process-instance root) to completion when the
  counter hits zero, as a pure function of state.
- **Seeding children into a scope is a solved primitive.** `subProcessBehavior`
  seeds a subprocess's inner start event(s) scoped by the container
  (`FlowScopeKey = <subprocess element-instance key>`) with the same
  `activateElement` primitive process-instance start uses. A multi-instance body
  seeds **N copies of one activity** into its scope the same way — the only
  difference is *what* it seeds and *how many*.
- **Activity-local variable scopes and scope-chain resolution.** Each activity
  already gets a local scope keyed by its element-instance key; a value written
  there resolves for that activity and its descendants, nearest-scope-wins, and is
  dropped on completion (ADR-0068). A per-iteration `inputElement`/`loopCounter`
  binding is exactly a value written into the inner instance's scope.
- **Interruption tears a scope down, including child instances.** `terminateScope`
  (ADR-0074) recursively terminates every element instance inside a scope, and
  `terminateChildInstance` (ADR-0076) also terminates a call activity's spawned
  child. A multi-instance body *is* a scope, so an interrupting boundary on it —
  and cancelling its instance — reuses both wholesale.
- **An append-compatible element-instance field has precedent.** ADR-0076 added
  `ParentElementInstanceKey` to `ProcessInstanceValue` with append-compatible
  encode/decode (ADR-0017). The one new field this ADR needs (a multi-instance
  role marker on `ElementInstanceValue`) follows the same pattern.

So the scope model, the counter, `applyToState`, key generation, seeding, local
scopes, interruption, and recovery already accommodate a multi-instance body. What is
missing is the compiled loop characteristics, a body behavior that seeds N inner
instances (and, for sequential, drives them one at a time), the per-iteration
variable bindings and output-collection aggregation, and the dispatch that makes an
**inner** run its real behavior while the **body** owns the outgoing flow.

## Decision drivers

- **Reuse, don't reinvent.** Build on the ADR-0074 scope lifecycle (element-as-scope,
  `activeChildren`, `completeScope`, `terminateScope`) and the ADR-0068 local-scope
  variable machinery. One scope model, one counter, one `applyToState`, one recovery
  path.
- **Invariants hold.** No per-command hot-path allocation (I1); durable before
  visible (I2); a single `applyToState` live and on recovery (I4); structure
  resolved at compile time (I5); deterministic replay with frozen keys, frozen
  variable bindings, and composing counters (I6).
- **Recovery correctness is the hard part.** The collection is evaluated **once**;
  the N inner instances and their loop bindings must be reconstructible from the log,
  not re-evaluated. The body's seeding and the sequential "spawn next" decision run on
  the behavior/command path (not persisted), so they must be **pure functions of
  replayed state**.
- **Faithful BPMN / Zeebe subset.** `<multiInstanceLoopCharacteristics isSequential>`
  with `<zeebe:loopCharacteristics inputCollection inputElement outputCollection
  outputElement/>` and an optional `<completionCondition>`; the standard
  `loopCounter` (1-based) local variable; `outputCollection` assembled from each
  iteration's `outputElement`.
- **Ship the runnable core first.** Parallel multi-instance over an input collection,
  binding `inputElement` + `loopCounter`, joining on completion, is the first
  runnable phase. Sequential, output collection, completion condition, and the
  Modeler layer on.

## Considered options

1. **Multi-instance body as a scope, inner runs as a scoped child (chosen).** The
   activity's compiled node becomes a **multi-instance body** — a scope, like a
   subprocess. On activation it evaluates the collection and seeds N copies of the
   *same node* as **inner** element instances scoped under itself (parallel: all at
   once; sequential: one, then the next on each completion), each seeded with its
   `inputElement` + `loopCounter`. An inner instance runs the node's real behavior
   (service task → job, subprocess → inner flow, call activity → child instance) via
   normal dispatch, but its completion does **not** take the model's outgoing flow —
   it decrements the body's counter and (sequential) triggers the next. When the
   counter drains, the body completes like a subprocess and takes the outgoing flow.
   The two roles are distinguished by one append-compatible marker on
   `ElementInstanceValue`. No new value type, record, counter, or recovery path.
2. **A synthetic multi-instance body node in the compiled graph.** Split every
   multi-instance activity into two compiled nodes — a synthetic body owning the
   outer flows and the real activity as a separate inner node — rewiring the parent
   flow to the body, renaming the inner, and re-registering a multi-instance
   subprocess's scope-start events under the new inner id. Rejected: heavy compiler
   surgery (flow rewiring, id renaming, scope-start re-registration) and two graph
   nodes per activity (affecting validation, overlays, and every id lookup) for no
   runtime-correctness gain over option 1's role marker.
3. **Compile-time unrolling.** Bake N static copies of the activity into the graph at
   deploy. Rejected: impossible — N is the length of a runtime collection, unknown at
   compile time (I5 resolves *structure*, not *data*); only a constant
   `loopCardinality` could unroll, and even that explodes the graph. A dead end.

## Decision outcome

Chosen: **option 1 — a multi-instance activity is a *body scope* that seeds N inner
copies of itself, reusing `FlowScopeKey`, the `activeChildren` counter,
`completeScope`, `terminateScope`, and the existing recovery path.** The genuinely
new logic is (a) a `multiInstanceBodyBehavior` that seeds the inner instances and
binds their loop variables, (b) two branches in the generic element lifecycle so an
**inner** completion aggregates output and decrements/advances instead of taking the
outgoing flow, and (c) one append-compatible role marker on `ElementInstanceValue`.

### The two roles, one node

A multi-instance activity compiles to the **same single node** it is today, keeping
its real activity type (`TypeServiceTask`, `TypeSubProcess`, `TypeCallActivity`, …).
The only compile-time addition is a `CompiledNode.MultiInstance int32` detail index
(into a new `multiInstances []MultiInstanceDetail` table, `-1` if none) — the exact
idiom `CallActivityDetail` follows (`compiler/process.go:100-120`, accessor mirroring
`CallActivity(detail)` at `compiler/process.go:635`). No new `BpmnType` is needed: the
node stays a valid boundary-event host through its real type (already in `isActivity`,
`compiler/validation.go:309-317`).

At runtime the one node takes two roles, distinguished by a new append-compatible
`ElementInstanceValue.MultiInstance uint8` field (`0` = not multi-instance, `1` =
body, `2` = inner):

- **Body** (`role = 1`). The element instance the parent flow activates. It is the
  scope. It owns the node's incoming/outgoing flows and any attached boundary events.
  It does **not** run the activity's real behavior.
- **Inner** (`role = 2`). Seeded by the body, `FlowScopeKey = <body key>`, one per
  collection element. It runs the node's **real** behavior via normal dispatch
  (`p.behavior(node.Type)`, `engine/behavior.go:671`). It has no outgoing flow of its
  own; its completion feeds the body.

Dispatch is a single indirection in the generic lifecycle: for a node with
`MultiInstance != -1`, `handleElementActivating` (`engine/behavior.go:154`) routes a
body (`ei.MultiInstance != 2`) to `multiInstanceBodyBehavior` and an inner
(`== 2`) to the node's real behavior; `handleElementCompleting`
(`engine/behavior.go:216`) routes an inner through the multi-instance completion path
(aggregate + advance/decrement, no outgoing flow) and a body through the normal
activity completion (`completeAndTakeFlows`, `engine/behavior.go:679`, which takes the
outgoing flow). A non-multi-instance node (`MultiInstance == -1`, `ei.MultiInstance
== 0`) is unaffected — the whole feature is inert unless the marker is set.

### The scope lifecycle

1. **Enter.** The parent flow activates the body; `Activating → Activated` like any
   element, incrementing the **parent** scope's `activeChildren` counter (existing
   `engine/apply.go:46`, keyed by the body's own `FlowScopeKey`).
2. **Evaluate once, freeze.** `multiInstanceBodyBehavior.OnActivated` evaluates the
   compiled `inputCollection` FEEL over the instance's variables (or reads the
   constant/FEEL `loopCardinality`) to a list of N items. It **freezes the collection
   and N into a body-scope variable** (a durable variable event) so both parallel
   seeding and — crucially — sequential resume read the iteration set from replayed
   state, never re-evaluate it (I6). An empty collection (N = 0) completes the body
   immediately (no inner ever seeded).
3. **Seed inner instances.** For **parallel**, seed all N inner instances in this
   batch. For **sequential**, seed only iteration 1. Each inner is seeded with
   `FlowScopeKey = <body key>` (the seeding `subProcessBehavior.OnActivated` uses for
   its inner start, `engine/behavior.go:1799`), its `loopCounter` (1-based) and
   `inputElement → items[i]` written as **variable events into the inner instance's
   scope** (`ScopeKey = <inner key>`, frozen and re-applied on replay, ADR-0068). Each
   inner `Activated` increments the body's counter (`engine/apply.go:46`).
4. **Run.** Each inner runs the node's real behavior scoped under the body: a service
   task parks on a job, a script evaluates, a subprocess seeds its own inner start, a
   call activity spawns a child instance — no behavior change, they are ordinary nodes
   that happen to be scoped under the body and reachable only through it.
5. **Inner completes.** On an inner `Completing`, the multi-instance path (a) appends
   the iteration's `outputElement` (a compiled FEEL over the inner's variables) to the
   body-scope `outputCollection` list — a frozen variable event; (b) evaluates the
   optional `completionCondition`; then advances:
   - **parallel:** the inner's `Completed` event decrements the body counter; when the
     counter drains, `completeScope(body)` completes the body. A satisfied
     completion condition cancels the still-running inner siblings (via
     `terminateScope`) and completes the body early.
   - **sequential:** if items remain and the completion condition is unmet, seed the
     next inner (counter returns to 1); otherwise let the counter drain and the body
     complete.
6. **Body completes.** When the body's counter hits zero, `completeScope` drives the
   body element to `Completing`; the body promotes `outputCollection` to the parent
   scope (a variable event), drops its local scope, and takes its outgoing flow —
   exactly a subprocess's completion, plus the output-collection promotion.

Steps 4–6 are the ADR-0074 subprocess lifecycle unchanged; the multi-instance-specific
logic is confined to seeding (step 3), per-iteration bindings, and the inner-completion
aggregate/advance (step 5).

### Determinism and recovery (the load-bearing argument)

Every fact recovery needs is written as an event and re-applied by `applyToState`;
nothing is re-evaluated:

- **The iteration set** is frozen into a body-scope variable at activation, so N and
  each item survive replay. The body behavior re-runs only **live**, never on replay
  (recovery replays *events*, it does not re-run behaviors) — and when it does run
  live, the inner `Activated` events plus the `loopCounter`/`inputElement`/collection
  variable events it emits fully capture the iteration state.
- **Each inner instance** is an ordinary element instance with a persisted `Activated`
  event (`FlowScopeKey = body`), so replay rebuilds all N (parallel) or the current
  one (sequential) with their counters. This is exactly how subprocess inner starts
  recover.
- **The sequential "spawn next" decision** is a pure function of replayed state: the
  just-completed inner's `loopCounter` gives the index, the frozen collection gives N
  and `items[loopCounter]`. Like `completeScope`, it reads only
  `GetElementInstance`/`ActiveChildren`/variables, so recovery reconstructs the
  identical decision.
- **The output collection** accrues via frozen variable events and is promoted by a
  variable event; replay re-applies, never re-runs the `outputElement` FEEL.

### Compiler

- Add a `MultiInstance int32` field to `CompiledNode` (`compiler/process.go:100-120`),
  a `multiInstances []MultiInstanceDetail` slice on `CompiledProcess`
  (`:436-471`), and a `MultiInstance(detail)` accessor mirroring `CallActivity(detail)`
  (`:635`). A node keeps its real activity type; `MultiInstance == -1` means "not a
  loop". No new `BpmnType` and no `numBpmnTypes` bump.
- Parse `<multiInstanceLoopCharacteristics isSequential="…">` on a task, subprocess,
  or call activity (add it to the relevant `xml*` structs / the shared
  `xmlFlowContent`, `compiler/parse.go:612-640`), with its
  `<extensionElements><zeebe:loopCharacteristics inputCollection="=…"
  inputElement="…" outputCollection="…" outputElement="=…"/>` and optional
  `<completionCondition>…</completionCondition>` / `<loopCardinality>`. Compile a
  `MultiInstanceDetail{ InputCollection *expr.Compiled, InputElement int32
  (interned), OutputCollection int32, OutputElement *expr.Compiled, Sequential bool,
  CompletionCondition *expr.Compiled (optional), Cardinality *expr.Compiled (optional)
  }` — the same `expr.CompileAuto` → `*expr.Compiled` path every other FEEL site uses
  (`compiler/parse.go:464-474`), stored on the detail (I5). Exactly one of
  `inputCollection` or `loopCardinality` is required (both empty is a deploy error).
  A new builder `AddMultiInstance…`/`pendingMI` grouping in `Build()` follows the
  `AddCallActivity` (`compiler/builder.go:230-239`) and io-mapping-grouping precedent.
- No new compile-time scope registration. Unlike a subprocess, whose inner *nodes*
  carry a static `FlowScope` (`compiler/builder.go:848-861`), a multi-instance body's
  inner is the **same node re-seeded** at runtime — nesting is expressed purely by the
  runtime role + `FlowScopeKey`, so the flat node/flow arrays and linearization are
  untouched. A multi-instance **subprocess** keeps its ordinary `TypeSubProcess`
  scope-start registration; each iteration is a normal subprocess instance seeded
  under the body.
- Validation: `isActivity` already includes the wrapped types
  (`compiler/validation.go:309-317`), so a multi-instance body is a valid
  boundary-event host with no change. A multi-instance activity with neither
  `inputCollection` nor `loopCardinality` fails deploy.

### Runtime

- New append-compatible `ElementInstanceValue.MultiInstance uint8` (tail-extended
  encode/decode, ADR-0017 — the same pattern ADR-0076 used for
  `ParentElementInstanceKey`; absent → `0`, non-multi-instance).
- `multiInstanceBodyBehavior` registered in `registerBehaviors` (`engine/behavior.go:47`)
  is *not* keyed by a `BpmnType` (the node keeps its real type); instead the
  activating/completing dispatch consults `node.MultiInstance` and the role marker
  and calls the body behavior directly. `OnActivated` freezes the collection and seeds
  the inner instances with `FlowScopeKey = <body key>` — exactly
  `subProcessBehavior.OnActivated`'s seeding (`engine/behavior.go:1783-1799`), but N
  copies of the node instead of one inner start. The inner-completion
  aggregate/advance lives in the generic completing path so it is type-agnostic across
  task / subprocess / call-activity inners.
- Two dispatch branches in `handleElementActivating` (`:154`) / `handleElementCompleting`
  (`:216`) keyed on `node.MultiInstance != -1` and the role marker.
- Interruption reuses `terminateScope` (`engine/behavior.go:816`; the body is a scope)
  and, for a call-activity inner, `terminateChildInstance` (`:794`, ADR-0076) —
  cancelling a multi-instance activity or firing an interrupting boundary on it via
  `interruptHost` (`:844`) terminates every iteration and every spawned child.

### Phased implementation plan (test-first)

- **Phase 1 — Compile the loop.** Parse `multiInstanceLoopCharacteristics` +
  `zeebe:loopCharacteristics` into `MultiInstanceDetail`; add the
  `CompiledNode.MultiInstance` index + `multiInstances` table + accessor;
  deploy-validate the collection/cardinality requirement. *Tests:* a parse test
  asserting the compiled detail (sources, `isSequential`, interned element names); a
  deploy-rejection test for a multi-instance activity with neither collection nor
  cardinality. Deploy accepts a valid model; no runtime yet.
- **Phase 2 — Parallel happy path.** `multiInstanceBodyBehavior` freezes the
  collection and seeds N inner instances binding `inputElement` + `loopCounter`; each
  inner runs its real behavior; the body completes when the counter drains and takes
  its outgoing flow. Adds the role marker. *Tests:* `start → [serviceTask ×N] → end`
  over a 3-element collection runs to completion (each inner visited once, each job
  completed, zero parked tokens); a **recovery test** — tokens parked on the N inner
  jobs, replay, assert body scope + child counter + per-iteration variables match live.
- **Phase 3 — Sequential + output collection.** Sequential progression (seed next on
  completion); `outputCollection`/`outputElement` aggregation; the empty-collection
  edge (N = 0 completes in one batch). *Tests:* sequential runs in index order (only
  one inner live at a time); the output list is assembled in order and promoted to the
  parent; an empty collection completes immediately; a **recovery test** mid-sequence
  (parked on iteration k of N) resumes and finishes the remainder.
- **Phase 4 — Completion condition, interruption, nesting.** `completionCondition`
  early-exit (parallel: cancel remaining via `terminateScope`; sequential: stop
  seeding); interrupting/non-interrupting boundary on a multi-instance activity;
  multi-instance over a **subprocess** and a **call activity** (the latter reuses
  ADR-0076 child termination on interrupt). *Tests:* a completion condition cancels the
  remaining iterations and completes the body; an interrupting timer boundary tears
  down all inner instances (and, for a call-activity inner, their child instances);
  multi-instance-over-subprocess runs to completion; a recovery test across an interrupt.
- **Phase 5 — Modeler.** A properties-panel "Multi-instance" section on tasks,
  subprocesses, and call activities: mode (none / parallel / sequential), input
  collection (FEEL), input element, output collection, output element, completion
  condition. Writes the `multiInstanceLoopCharacteristics` + `zeebe:loopCharacteristics`
  the compiler reads; bpmn-js draws the ∥/≡ marker from them automatically.

### Consequences

- **Positive:** the engine gains data-driven fan-out (parallel and sequential) on top
  of the subprocess scope lifecycle — no new value type, record, counter, or recovery
  path; the body reuses `completeScope`, `terminateScope`, and (for call-activity
  inners) `terminateChildInstance`; inner behaviors are untouched. Completes the core
  of Milestone 3.
- **Negative / trade-offs accepted:** one append-compatible field on the hot
  `ElementInstanceValue`; two role-dispatch branches in the generic element lifecycle
  (a hot, well-tested path) that must stay pure functions of state; a body-scope
  variable that freezes the (possibly large) input collection for sequential resume.
  Parallel multi-instance can seed many element instances in one batch — bounded by
  the collection size, the same surface a wide parallel gateway already produces.
- **Follow-ups / risks to watch:** the seeding and sequential-advance decisions are
  not persisted, so every fact they read must be rebuilt by replay first — keep them
  pure functions of `GetElementInstance`/`ActiveChildren`/variables and the frozen
  collection. Guard the N = 0 and "inner completes within the seeding batch" cases so a
  body is never declared complete before it has seeded. Very large collections
  (memory/batch size) and `outputCollection` growth are a compaction/limits concern.
  `versionTag` binding and nested multi-instance-in-multi-instance depth reuse the
  ADR-0074 `maxScopeDepth` bound. Cross-partition fan-out stays out of scope
  (Milestone 5).

## Pros and cons of the options

### Option 1 — body-as-scope, inner runs scoped, role marker (chosen)
- Good: one scope model, one counter, one `applyToState`, one recovery path; reuses
  `completeScope`/`terminateScope`/`terminateChildInstance`; inner behaviors unchanged;
  the node keeps its real type (no new `BpmnType`), so the compiler delta is one node
  field + one detail table; the append-compatible element-instance field has direct
  precedent (ADR-0076).
- Bad: adds two role branches to the generic element lifecycle; freezes the collection
  into a body-scope variable for sequential resume.

### Option 2 — synthetic body node in the compiled graph (rejected)
- Good: the inner is a fully ordinary node, so the lifecycle needs no role branch.
- Bad: heavy compiler surgery (flow rewiring, id renaming, scope-start
  re-registration for a multi-instance subprocess); two graph nodes per activity ripple
  through validation, overlays, and every id lookup; no runtime-correctness gain.

### Option 3 — compile-time unrolling (rejected)
- Good: no runtime lifecycle for the constant-cardinality case.
- Bad: impossible for the collection case (N is runtime data, not compile-time
  structure — I5); explodes the graph even for constants; a dead end.

## Links

- builds directly on ADR-0074 (embedded subprocesses — the element-as-scope lifecycle,
  `activeChildren`, `completeScope`, `terminateScope`, `interruptHost`) and ADR-0068
  (task I/O variable mappings — activity-local scopes and scope-chain resolution)
- reuses ADR-0076 (call activities — `terminateChildInstance` for a call-activity
  inner, and the append-compatible `ElementInstanceValue`/`ProcessInstanceValue` field
  pattern) and ADR-0017 (append-compatible value encoding)
- honors I1, I2, I4, I5, I6 and ADR-0018 (test-first, recovery tests up front)
- ROADMAP Milestone 3 "Multi-instance activities (sequential and parallel)"; the last
  large control-flow item after subprocesses and call activities. Cross-partition
  fan-out is deferred to Milestone 5 (ADR-0006).
