# ADR-0022: Collaborations and pools as multi-process deployments

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

Atlas runs one process per instance (ADR-0002: a process instance is the root
scope its element instances live under). Real BPMN models, though, are often
*collaborations*: a `<collaboration>` holds several `<participant>` pools, each
referencing its own `<process>`, and the pools talk to each other through message
flows. We already execute the runtime side of that conversation — message events
and correlation (ADR-0020) — but the compiler only ever compiled
`defs.Processes[0]`, and the deploy path assigned exactly one definition per
file. So a two-pool collaboration could not be deployed, and the editor's
"rename diagram" panel went blank on a collaboration root.

We need to deploy and run collaborations without inventing a new runtime concept.

## Decision drivers

- **Don't grow the runtime model.** The engine's unit stays "one process per
  instance." A collaboration must decompose into things the engine already runs.
- **Reuse message correlation for the cross-pool link.** Message flows are the
  *diagram's* depiction of an interaction; the *executable* link is the message
  catch/throw + correlation we already built (ADR-0020). We must not compile
  message flows into a second, parallel mechanism.
- **One deploy, many definitions, cleanly keyed.** Deploying a collaboration
  should register each pool's process as its own runnable definition, each with
  its own id/version/key, reloadable after a restart (ADR-0019).
- **Keep the viewer honest.** A collaboration deployed as semantic-only XML
  should still render (as pools), the same promise the single-process
  auto-layout already makes.

## Considered options

1. **Multi-process deployment.** Compile every executable `<process>` in the
   file; deploying a collaboration registers one definition per pool. Message
   flows stay purely visual; correlation is the runtime link.
2. **A "collaboration" as a single compiled unit** that internally holds several
   processes and routes message flows directly.
3. **Reject collaborations at deploy**, requiring the user to split pools into
   separate files.

## Decision outcome

Chosen option: **"Multi-process deployment"**.

- The compiler gains `ParseAll(baseKey, version, r) → []Deployable`, which
  compiles every `<process>` that has a start event (executable), keying the
  i-th one `baseKey+i`. A `<participant>` whose process is a black box (no start
  event, or none) is skipped for execution but still valid — a message-flow
  counterpart pool is frequently left unmodeled. `Deployable` also carries the
  participant (pool) name, which labels the definition in the UI (a process in a
  collaboration is usually unnamed; the pool is what's labelled). `Parse` (single
  process) and a new `ParseNamed(…, processId)` (reload one specific process out
  of a collaboration's XML) share the same per-process compiler.
- The deploy handler uses `ParseAll` and registers/persists each pool's process
  as its own definition, returning all of them (`deployments: […]`) while still
  echoing the first flat for single-process clients. Reload recompiles each
  stored record by its process id (`ParseNamed`).
- Message flows compile to nothing; the runtime interaction between pools is the
  message correlation of ADR-0020. Two pools rendezvous exactly when one throws
  (or an API publishes) a message the other is subscribed to.
- The viewer's auto-layout (`ensureDiagramLayout`) grows a collaboration branch:
  a model with a `<collaboration>` and no diagram interchange is laid out as
  vertically stacked pools, each participant's process laid out left-to-right in
  its band, with the plane bound to the collaboration. The editor (bpmn-js)
  already authors pools and message flows natively.

### Consequences

- **Positive:** Collaborations deploy and run with no new runtime concept — each
  pool is an ordinary process instance, linked by the correlation we already
  have. Per-pool definitions get independent versions and lifecycles. Semantic
  collaboration XML still renders.
- **Negative / trade-offs accepted:** A collaboration deploy is not atomic — a
  persist failure partway leaves earlier pools deployed (no rollback until
  deployment is a first-class WAL event). Message flows are not validated against
  the message events that implement them, so a drawn message flow with no matching
  catch/throw simply does nothing. The generated pool layout is coarse (stacked
  bands, no message-flow edges).
- **Follow-ups / risks to watch:** Atomic multi-process deploy; validating that a
  message flow has executable catch/throw endpoints; message-flow edges in the
  generated layout; pool-scoped start (a message start event that creates an
  instance of a specific pool).

## Pros and cons of the options

### Option 1 — multi-process deployment (chosen)
- Good: no new runtime unit; reuses correlation; independent per-pool lifecycles;
  small, local compiler/deploy change.
- Bad: non-atomic multi-deploy; message flows are unvalidated documentation.

### Option 2 — collaboration as one compiled unit
- Good: a single deployable artifact; message flows could be compiled links.
- Bad: invents a second cross-process routing mechanism next to message
  correlation, and a multi-root scope the single-writer/instance model doesn't
  have. Large blast radius for little gain.

### Option 3 — reject collaborations
- Good: zero engine change.
- Bad: fails the user's actual models; pushes a manual split onto them; the
  editor still can't save what it can draw.

## Links

- relates to ADR-0002 (single-writer partition / one process per instance)
- builds on ADR-0020 (message events and correlation — the runtime pool link)
- relates to ADR-0019 (durable deployments — each pool persists independently)
- relates to ADR-0004 (compile BPMN to an indexed graph)
