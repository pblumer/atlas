# ADR-0014: DMN business rule tasks via the temis engine

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Core team

## Context and problem statement

BPMN models route on business rules. The standard way to express those rules is a **business rule task** that delegates to a DMN decision (a decision table or FEEL logic) rather than hard-coding the branching in the process. Atlas needs to execute business rule tasks, which means it needs a DMN engine.

Atlas already uses FEEL internally for expressions, and the roadmap lists "DMN decision evaluation as a product surface" as a non-goal — we are not building a DMN authoring/product experience. But *executing* a decision a model references is different from shipping a decision product, and it is squarely in scope for a workflow engine. The question is **how** a business rule task evaluates its decision without violating the engine's invariants:

- Evaluating a decision is real work (parsing FEEL, table matching, decimal arithmetic). It must not run on the single-writer processor path, which must stay allocation-free (I1) and must never block (ADR-0007).
- A decision evaluation is a side effect: it must not run inside `applyToState`, which is replayed on recovery and must stay deterministic and side-effect-free (I4). Re-evaluating on every replay would be wrong.
- Model parsing and rule compilation must happen at deploy time, not at runtime (I5).

We also do not yet have the process-variable subsystem (Milestone 1), so a business rule task cannot yet read real instance variables as inputs or write decision outputs back as variables.

## Decision drivers

- Reuse, don't reinvent: DMN + FEEL is a large spec; a conformant engine is a project in itself.
- Respect every load-bearing invariant (I1, I2, I4, I5) — no exceptions.
- Keep the DMN dependency off the engine hot path.
- Deliver a working vertical slice now, without waiting on the variable subsystem.

## Considered options

1. **Build a DMN evaluator inside Atlas**, reusing the internal FEEL work.
2. **Evaluate inline in the business rule task behavior**, calling an embedded engine synchronously on the processor goroutine.
3. **Embed the temis DMN engine and evaluate through the existing job path** — compile at deploy time, evaluate on an in-process worker off the processor goroutine.

## Decision outcome

Chosen option: **embed [temis](https://github.com/pblumer/temis) and evaluate through the job path.**

temis is an embeddable, DMN 1.5 / TCK-conformant decision engine with a two-phase API (compile once, evaluate many) that maps cleanly onto Atlas's compile-then-run architecture.

The integration deliberately mirrors the service-task/worker protocol (ADR-0007), so it inherits all of that protocol's durability and non-blocking properties for free:

1. **Deploy time:** the DMN model is compiled once by temis into immutable, thread-safe decisions, held in a `dmn.Registry` keyed by process-definition key (I5). No decision is a new element type on the hot path — a business rule task is just a compiled node with a `BusinessRuleTaskDetail` (decision id, static inputs, retries).
2. **Activation:** `businessRuleTaskBehavior` creates a **job** carrying a reserved DMN job type (`io.atlas.dmn`), exactly like a service task creates a job, and waits. The processor never touches temis, so it stays allocation-free and free of the dependency.
3. **Evaluation:** an in-process worker (`dmn.Handler`, registered with the existing `job.Runner`) pulls those jobs off the processor goroutine, resolves the decision and inputs from the compiled process, evaluates through temis, and submits `CompleteJob`. Evaluation is a post-fsync side effect (I2), never part of `applyToState` (I4).
4. **Completion:** the job completion drives the token onward through the normal completion path.

Because the variable subsystem did not exist yet, the original slice fed a business rule task's decision a **static input context** recorded at deploy time (JSON-encoded and interned), and surfaced its outputs through a caller-supplied sink rather than writing them back as variables. That was the explicit seam Milestone 1 would replace — now closed by [ADR-0038](0038-dmn-io-variable-mappings.md): a decision reads its inputs from process variables (io-mapping inputs evaluated over the instance) and writes its result back into the `resultVariable` process variable via an output-carrying job completion. Static inputs remain a constant base a mapping overrides.

### Consequences

- **Positive:** Zero changes to the processor, WAL, or `applyToState`; no new value type or record. The business rule task reuses the entire job lifecycle, including crash recovery — a business rule job survives a restart and is re-evaluated by the worker after replay (covered by a recovery test). The DMN dependency lives only in the new `dmn` package, never in `engine`. temis owns DMN/FEEL conformance.
- **Negative / trade-offs accepted:** Static inputs and sink-delivered outputs are a stand-in, not the final shape — a business rule task cannot yet participate in data flow. Like service tasks, DMN jobs inherit the current single-process job-type interning limitation (job-type indices are per-compiled-process; unifying the job-type space across deployments is future work, so the worker resolves each job's compiled process by its `ProcessDefKey`). A decision evaluation, being a job, is at-least-once (ADR-0007); DMN evaluation is pure, so re-evaluation is harmless.
- **Follow-ups / risks to watch:** wire real input/output variable mappings once Milestone 1 lands; deploy DMN models durably alongside processes (currently in-memory, like compiled processes); expose business rule tasks through the compiler's authoring gate and the web UI; track temis version pinning.

## Pros and cons of the options

### Build a DMN evaluator inside Atlas
- Good: no third-party dependency; full control.
- Bad: DMN + full FEEL + decimal semantics + TCK conformance is a large, ongoing effort that duplicates temis; distracts from the engine.

### Evaluate inline in the behavior
- Good: simplest to wire; no job round-trip.
- Bad: runs evaluation on the single writer (blocks the partition, allocates on the hot path — violates I1 and ADR-0007) and tempts running it inside state application (violates I4). Rejected.

### Embed temis via the job path
- Good: conformant engine reused; respects every invariant; reuses the proven worker protocol and its recovery semantics; dependency isolated from the engine.
- Bad: a job round-trip per decision (acceptable — same as service tasks); depends on an external module.

## Links

- mirrors ADR-0007 (job worker protocol); depends on ADR-0005 (evaluate only after fsync)
- respects ADR-0004 / I5 (compile at deploy time) and I1/I4
- relates to ADR-0008 (internal FEEL strategy)
- unblocks the roadmap's Milestone 1 input/output variable mappings
