# ADR-0050: Central DMN decisions via a temis decision connector

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) decided how a business rule
task evaluates its decision: the DMN model is resolved and **snapshotted at deploy
time**, compiled by the **embedded temis library**, and evaluated **in-process**
by the DMN worker on the job path. [ADR-0039](0039-dmn-io-variable-mappings.md)
then wired real input/output variable mappings onto it. This is the *local* mode:
the decision travels with the process definition, versions with it, and runs
inside the engine with no runtime dependency on anything external.

That is the right default, but it is not the only shape a decision takes in
practice. Many organizations keep their decisions in a **central decision service**
— authored, governed, versioned, and audited in one place (a running temis
service / model repository), shared across many processes and systems, and
changeable **without redeploying every BPMN** that references them. For those, the
decision should be evaluated *centrally*, not baked into each Atlas deployment.

So there are two legitimate kinds of business rule decision, differing only in
**where the decision is evaluated**:

1. **Local** — embedded temis library, model snapshotted with the process
   (ADR-0014/0039). Self-contained, deterministic, no runtime dependency.
2. **Central** — a temis **service** evaluates the decision; Atlas sends inputs,
   gets outputs. Governance and a single source of truth are the drivers.

Only mode 1 exists today. This ADR decides how to add mode 2 without duplicating
the decision I/O semantics and without violating the engine's invariants (no DMN
or FEEL-input work on the single-writer path — I1; no side effect in
`applyToState` — I4; compile at deploy — I5; events are the only facts — I6).

Note: this is distinct from the model **source**. `dmn.ServiceResolver`
(ADR-0039 era) already lets the *model XML* be fetched from a temis git/service at
deploy time — but it is still compiled and evaluated **locally**. This ADR is
about the *evaluation locus*, an orthogonal axis.

## Decision drivers

- **Reuse the connector framework, don't invent one.** ADR-0036 already runs an
  outbound integration through the job path with a server-side name→endpoint
  registry, and [ADR-0041](0041-connector-management-and-secret-store.md) fixes the
  secret model (the model carries a connector *name*, never a URL or token). A
  central decision service is exactly that shape.
- **One decision-I/O implementation.** Input mappings and the result→variable
  write (ADR-0039) must not be duplicated per evaluation mode; local and central
  should differ only in *who evaluates*.
- **Author-visible, per-decision choice.** A model expresses "evaluate this
  decision centrally" with a small, standard-looking marker; everything else about
  a business rule task stays identical.
- **Respect every invariant.** Evaluation is a post-fsync side effect off the
  processor goroutine (I1/I2); the result is frozen into the completion event so
  replay never re-calls the service (I6); at-least-once is safe because DMN
  evaluation is pure (ADR-0014/0036).

## Considered options

**A — how a model selects central evaluation:**
1. A distinct BPMN element / connector task kind (like the clio connector service
   task), losing the business-rule-task identity and the `calledDecision` /
   `ioMapping` authoring.
2. **The same business rule task, marked with `<atlas:temisConnector connector="…"/>`
   (chosen).** Present → central via that connector; absent → local (ADR-0014).
   `calledDecision`, `ioMapping` inputs, and `resultVariable` are authored
   identically in both modes.

**B — where the shared decision-I/O logic lives:**
1. Duplicate input-building / output-variable helpers in the new connector worker.
2. **Extract an exported `dmn.DecisionHandler(bind)` core (chosen)** that owns the
   ADR-0039 I/O semantics and is parameterized by an `Evaluator`; the local
   handler binds the embedded registry, the connector handler binds a remote
   client. One implementation, two bindings.

## Decision outcome

**A2 + B2.** A business rule task bearing `<atlas:temisConnector connector="name"/>`
is a **central** decision: at compile time it carries the reserved job type
`io.atlas.temis.decision` (a fourth globally-pinned index, alongside DMN, user
task, and PowerShell) instead of the local DMN job type, and it records the
connector name. An in-process **temis decision connector worker** subscribes to
that job type; for each job it resolves the connector's client from a server-side
`temis.Registry` (name → endpoint + token, per ADR-0041), builds the decision's
input context from the same static-inputs + variable-mapping merge as the local
worker (ADR-0039), calls the remote temis service's evaluate endpoint, and returns
the result as the `resultVariable` process variable through the output-carrying
job completion. The processor never touches temis or the network (I1); the result
is written as a `VariableCreated` event and re-applied on replay, never
re-evaluated (I6).

The shared core is `dmn.DecisionHandler`: it reads the business rule task detail,
builds inputs, evaluates via a supplied `Evaluator`, and writes the output
variable. `dmn.Handler` (local) binds `registry.Evaluate`; `temis.Handler`
(central) binds a `temis.Client.Evaluate`. The `temis` package ships the
connector trio — `Registry` / `Client` / `Handler` — mirroring `clio`.

Because a central decision's model lives in temis, a connector-mode business rule
task is **excluded from the deploy-time local-model resolution gate**
(`CompiledProcess.BusinessRuleDecisions` skips it), so a project made only of
central decisions needs no local DMN reference.

### Scope of this slice

Following the clio/REST precedent (ADR-0036/0041), this slice ships the compiler
wiring, the `temis` connector trio, the shared `DecisionHandler` core, and
job-path + engine tests proving a central decision drives a downstream gateway.
Wiring the connector worker into the **server run loop** and its **managed
configuration / secret resolution** are the same open follow-ups ADR-0041 tracks
for clio and REST — the temis connector joins that shared wiring rather than
inventing its own.

### Consequences

- **Positive:** a per-decision choice between local and central with *no* change
  to how a business rule task is otherwise authored or how its I/O maps
  (ADR-0039). Central decisions are governed once and change without redeploying
  consumers. Zero new engine surface: same job path, same completion-carries-
  variables mechanism, same recovery. The decision-I/O logic has one
  implementation. Secrets follow ADR-0041 (a connector name, env-resolved).
- **Negative / trade-offs accepted:** a central decision adds a runtime dependency
  on the temis service (its token parks if the service is down — the same failure
  mode as any connector/service task) and a network hop's latency. The HTTP wire
  format to the temis service is provisional until the contract is fixed (isolated
  in `temis.HTTPClient`, like clio's). Server run-loop wiring and managed connector
  config are not in this slice (shared ADR-0041 follow-up).
- **Follow-ups / risks to watch:** wire the connector worker into the server run
  loop and its registry from managed config (shared with clio/REST, ADR-0041);
  fix the temis service evaluate wire contract; a Modeler affordance to mark a
  business rule task central and pick the connector; per-connector retry/incident
  policy for a persistently unreachable service (extends ADR-0036).

## Pros and cons of the options

### A2 — same business rule task with a connector marker (chosen)
- Good: keeps the decision identity and reuses all of ADR-0039's authoring and I/O;
  the mode is a single, discoverable marker.
- Bad: two runtime behaviors behind one BPMN element type (mitigated: they differ
  only by reserved job type, resolved at compile time).

### A1 — a distinct connector task kind
- Good: visually explicit that it calls out.
- Bad: loses `calledDecision`/`ioMapping`/`resultVariable` reuse; a decision stops
  looking like a decision. Rejected.

### B2 — shared `DecisionHandler` core (chosen)
- Good: one implementation of the ADR-0039 I/O semantics; local and central can't
  drift.
- Bad: the `temis` connector package depends on `dmn` for the core (a benign,
  acyclic dependency — the shared concept genuinely lives there).

### B1 — duplicate the helpers
- Good: fully decoupled packages.
- Bad: two copies of subtle FEEL-input / output-canonicalization logic to keep in
  sync and separately test. Rejected.

## Links

- adds the *central* counterpart to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
  (local, embedded) and reuses [ADR-0039](0039-dmn-io-variable-mappings.md)'s
  input/output variable mappings verbatim
- an instance of the connector framework: [ADR-0036](0036-clio-connector.md)
  (connector-via-job + name→endpoint registry) and
  [ADR-0041](0041-connector-management-and-secret-store.md) (secret model, managed
  config) — the temis connector is a new connector *kind* under both
- runs on the [ADR-0007](0007-job-worker-protocol.md) job path; honors I1/I2/I4/I6
  ([`docs/architecture/invariants.md`](../architecture/invariants.md))
