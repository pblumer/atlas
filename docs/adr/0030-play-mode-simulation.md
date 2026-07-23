# ADR-0030: Play mode — ephemeral in-Modeler process simulation

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler has three top-level modes on a diagram: **Design**,
**Implement**, and **Play**. Play is a sandbox: you run the model right there in
the editor, stepping tokens through it, feeding inputs, and inspecting variables,
*without* deploying it to a real environment or leaving durable state behind. It
is how an author sanity-checks control flow before committing.

Atlas already has the deploy path (Deploy & run, ADR-0013) and a live runtime
overlay (Milestone S). But those produce **real, durable, versioned** instances
in the engine's log. There is no way to try a model cheaply and throw the result
away. The question: **how does Atlas offer a Play/simulation mode**, and does it
reuse the real engine or simulate control flow separately?

## Decision drivers

- **Same semantics as production.** A simulation that disagrees with the real
  engine is worse than none. Play must exercise the *actual* behaviors (gateways,
  sequence flow, tokens), or authors will trust a lie.
- **Ephemeral and side-effect-free.** Play must not mint versions, must not
  persist to the durable log, and must not fire real external side effects (no
  real service-task workers, no real messages leaving).
- **No second control-flow implementation.** Reimplementing token movement in JS
  for the browser would fork engine semantics — the interpret-don't-compile trap.
- **Cheap to start and discard**, per diagram, from the editor.

## Considered options

1. **A JS token-simulation engine** in the browser that walks the diagram.
2. **An ephemeral engine sandbox** — the server runs the real compiler + processor
   against an in-memory, non-durable partition seeded from the draft, with
   external effects mocked (service tasks auto-complete from example data,
   messages/timers steppable), exposed through a Play-scoped API and torn down on
   exit.
3. **No Play mode** — authors deploy to a throwaway instance to test.

## Decision outcome

Chosen option: **Option 2 — an ephemeral engine sandbox.** Play compiles the
current draft (reusing ADR-0026's dry-run compile) and runs it on a **real
processor over an in-memory, non-durable WAL/state**, isolated from deployed
definitions and never written to the durable stores (ADR-0019). Because it is the
real processor, control-flow semantics are identical to production by
construction. External effects are **mocked for the sandbox only**: service tasks
complete from example data (ADR-0025) or a stubbed result, timers and message
catches are advanced by the author stepping them, and nothing leaves the process.
The Modeler drives it through a Play-scoped API and overlays token positions and
variables using the same overlay Operations already uses (Milestone S). Exiting
Play discards the sandbox.

Option 1 is rejected outright: a browser token-walker is a second, divergent
implementation of the engine's core — exactly the fork the invariants forbid.
Option 3 works but pollutes the durable log with throwaway instances and versions,
which is what Play exists to avoid.

### Consequences

- **Positive:** Simulation is the real engine, so it can't drift from production;
  nothing durable is created; authors iterate fast; reuses the compiler, the
  processor, the runtime overlay, and example data rather than building anew.
- **Negative / trade-offs accepted:** Running a real-but-ephemeral processor per
  Play session has a cost and needs careful isolation so a sandbox can never touch
  durable partitions or deployed definitions. Mocking external effects is its own
  small design space (how a service task is stubbed, how a timer is fast-forwarded).
- **Follow-ups / risks to watch:** Guarantee sandbox partitions are unreachable
  from the durable engine; decide stepping vs. free-run controls; bound sandbox
  lifetime/resources so abandoned Play sessions are reclaimed.

## Links

- reuses ADR-0026 (dry-run compile), ADR-0025 (example data as mock inputs),
  Milestone S runtime overlay
- honors ADR-0001/0002/0005 by using the real processor rather than a second
  control-flow implementation; deliberately non-durable, unlike ADR-0019
