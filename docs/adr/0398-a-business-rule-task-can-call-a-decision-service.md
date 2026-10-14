# ADR-0398: A business rule task can call a decision service

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether a business rule task should eventually name a service *distinctly* from a decision — an explicit binding rather than one shared vocabulary — which becomes necessary only if models routinely want one name for both; not observed, and refused at the gate meanwhile
- **Question checked:** 2026-09

## Context and problem statement

A `<decisionService>` is DMN's published interface over part of a DRD (DMN §10.4).
It holds no logic of its own and names four sets of existing elements:

| element | meaning |
|---|---|
| `outputDecision` | what the service returns |
| `encapsulatedDecision` | what it evaluates internally, invisible to the caller |
| `inputDecision` | decisions it does **not** evaluate — the caller supplies their results |
| `inputData` | the input data it takes |

The fourth row is the one that matters. An input decision is a **boundary**: the
service cuts the graph where its author chose, computes everything above the cut
and is handed everything on it. Calling the output decision directly cannot
express that — the engine then demands every input the whole sub-graph reaches.

**temis supports one completely**, measured on the pinned version: it compiles
the service, honours the boundary, coerces the declared output type, and — the
question #998 left open — evaluates one whose output decision is a **boxed
context**, which is the shape of the reference tariff model.

**Atlas could not reach any of it.** `evalDecision` resolved through
`Definitions.Decision` alone, so a business rule task's `decisionId` named a
decision and nothing else. A model carrying a service deployed happily; the
service was simply never looked at. The workaround — call the service's output
decision — returns the same value and loses the interface: the caller must know
which decision inside is the right one and which inputs the graph happens to
need, so the inside cannot be rearranged without breaking every caller.

### What the engine does not offer

`Index()` lists decisions and input data, `Graph()` has no node for a service, and
`Functions()` lists business knowledge models only — measured. **There is no way
to ask a compiled model which services it has.** `Service(idOrName)` answers for a
name already known.

So the names have to be read from the document, which Atlas already parses for
its own purposes (`dmn/layout.go`). Each parsed name is then asked of the engine,
so the document supplies the enumeration and the engine remains the authority on
what exists.

## Decision drivers

- A task names one string. Whatever is chosen has to work with a `decisionId`
  that carries a name and nothing else.
- Every task already deployed names a decision. That must keep working
  unchanged (invariant I6).
- The gate and the runtime agree: a deploy must refuse exactly what the runtime
  would fail to resolve.
- Ambiguity is refused, not resolved. A rule nobody wrote down that silently
  prefers one of two meanings is worse than a refusal.
- Nothing is re-derived per evaluation that can be settled at deploy time
  (invariant I5).

## Considered options

1. One addressing vocabulary — a `decisionId` names a decision or a service — with a model that uses one name for both refused at the gate
2. An explicit binding on the business rule task saying which kind it means
3. Only a service may be called; a decision stops being addressable
4. Leave it, and keep the workaround of calling the output decision

### Option 3, argued rather than waved past

This is the purist reading, and it has the better of the argument on design: a
service *is* the published interface, and if a process may reach past it into an
individual decision, the encapsulation is advisory. Every integration point
Atlas has elsewhere — a worker, a connector — is a published thing, not an
internal one. Taken seriously, it would also push model authors to state their
interfaces instead of letting a process reach for whatever decision looks right.

It is still wrong here, for three measured reasons.

- **Every deployed task names a decision.** Making that unaddressable breaks
  running processes for a modelling ideal. Invariant I6 settles this alone.
- **Atlas cannot author one.** The vendored dmn-js refuses a decision service on
  the DRD canvas in every configuration probed (#998). A rule that only services
  may be called, in a product whose own editor cannot draw one, means models must
  be authored elsewhere.
- **Most models have one decision.** Measured across the reference installation:
  ten of eleven stored models are a single decision table. Wrapping each in a
  service to satisfy a rule is ceremony, not encapsulation.

The honest form of option 3 is a *convention* — publish a service, point tasks at
it — which option 1 makes possible without making anything else impossible.

## Decision outcome

Chosen option: **"One addressing vocabulary, ambiguity refused at the gate"**.

- **Resolution.** `evalDecision` tries `Definitions.Decision` and then
  `Definitions.Service`. A service evaluates through `evalService`, which passes
  the caller's inputs (its input data *and* its input decisions) and restores the
  declared types of its outputs.
- **The vocabulary.** A registration works out, once, every name a `decisionId`
  may carry for that model — its decisions under both of their names (ADR-0385)
  and its services — and the registry indexes all of them. So the pinning
  selector, the latest pointer, the model a deployment-bound task resolves to,
  the try-a-decision membership check and the deploy gate's coverage report all
  answer for a service exactly as they do for a decision.
- **What is published.** A decision deployment records its services beside its
  decisions and versions them the same way: a service is a published interface in
  its own right, and what a task may name is what a deployment should record.
- **Ambiguity is refused.** A model that gives one name to both a decision and a
  service, or to two services, is refused at validation — the deploy gate and the
  try-a-decision panel both say so, naming the name. A reload does not re-apply
  the gate (ADR-0177), so a model already on disk comes back, publishes the name
  once and evaluates whatever the engine resolves.
- **The picker.** A service is described like a decision, marked as one, with the
  inputs a caller must supply — its input data and its input decisions, under the
  FEEL identifiers the evaluation binds, in the order temis registers them as the
  service's parameters. A service with one output decision reports that
  decision's variable and type; with several, the result is a context this shape
  cannot name, so the service's own name stands there untyped.

`Result.Outputs` for a service is keyed by **output-decision name**, so a
single-output service lands in a task's result variable exactly as the decision
inside it would — which is why calling the service is a drop-in replacement for
the workaround.

## Consequences

**Good**

- A model authored in the temis Modeler, in Camunda or by hand, whose interface
  is a decision service, is callable from a process — including the boundary,
  which nothing else in Atlas can express.
- The existing vertical slice (#915) now proves both paths on one model: the
  decision inside the service, and the service itself.
- Nothing about a task naming a decision changes.

**Bad, or merely true**

- ~~**A service call retains no trace.**~~ **Resolved** (2026-09-20). It was true:
  temis offered no trace option for a service evaluation, so the durable record
  (ADR-0066) held a service call's inputs and outputs and nothing about how it got
  there, while a decision call was unaffected. It was the clearest reason to want
  the accessor upstream, and the accessor landed there —
  `CompiledService.Evaluate` takes `opts ...EvalOption` (temis#226) — so
  `evalService` threads `WithTrace` through and a service call retains its rules
  like any other. The boundary holds in the trace as in the result: an input
  decision is supplied rather than computed, so its table never ran and never
  appears. Records written **before** this carry no trace and never will; the
  record is frozen history, not a thing to recompute, and the surfaces that read
  it say which silence they are looking at (ADR-0405).
- **The service names come from the document, not from the engine.** They are
  verified against it, so nothing unrunnable is offered, but a second reader of
  the XML is a second thing to keep right if DMN's spelling ever changes.
- A decision deployment's listing now mixes services and decisions in one list.
  A task calls either the same way, which is what that list means, but the
  Console does not yet say which is which.
- **The editor still cannot draw one** (#998 part two). An author who wants a
  service must write it into the XML or bring the model from elsewhere.

## References

- #998 — the measurement that started this, and its part two
- #915, ADR-0319 — the decision deployment this rides on
- ADR-0385 — a decision's two names, whose vocabulary this extends
- ADR-0386 — restoring a result's declared type, applied per output decision here
- ADR-0177 — why a reload does not re-apply the gate
- `dmn/services.go`, `dmn/services_test.go` — the reading of the document and its tests
