# ADR-DRAFT: Delivering a message to another node is a worker's job

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-draft-cross-instance-message-addressing fixes what a cross-node message *is*.
This record decides what carries it, and the constraint is hard: **the engine may not
make the call itself.**

- I2 fixes the order — append, one fsync, commit, *then* side effects. A network call
  during command handling would act on an event that is not yet on disk.
- I3 gives one goroutine the partition. An outbound HTTP request inside it stalls
  every other command for the duration of the request or its timeout. ADR-0129 hit
  exactly this and answered it by resolving on the loop and calling off it.

Atlas already has one answer to "a modelled step must reach something outside this
process", and has had it since ADR-0007: the step compiles to a job, a worker leases
it after fsync, and failure becomes retries and then an incident. ADR-0203 named the
parts, and ADR-0164/0233 closed the door on doing such work in-process.

The question is therefore narrow: **is peer delivery a new engine capability, or is
it one more Worker Type?**

## Decision drivers

- **Do not grow the engine for something the job seam already does.** Retries
  (ADR-0135), the circuit breaker (ADR-0340), call budgets (ADR-0291), incidents
  (ADR-0061), incident repair (ADR-0160/0169/0287) and the offload choice
  (ADR-0203/0183) all hang off that seam already.
- **Credentials by reference.** A peer credential is a vault handle, never a value in
  a store or a log — ADR-0041 and ADR-0129 both settled this.
- **Honest failure.** A message that could not be delivered must end up visible, with
  a named cause, on somebody's screen. Silent loss is the failure this whole epic
  exists to remove.
- **Acceptance is not processing.** Whatever the receiver acknowledges has to be
  something it can actually promise, and it must be stated in the record rather than
  assumed by whoever reads the mockup.

## Considered options

1. **Engine-native outbound** — a durable outbox in the state store, drained by a
   goroutine the processor owns, with its own retry and backoff state.
2. **A reserved Worker Type** (`atlaspeer`), driven by a job the sending element
   compiles to, exactly like every other outbound capability.
3. **Push through an external broker** and let it own delivery.

## Decision outcome

Chosen option: **2 — a reserved Worker Type.**

### Sending

A message throw event, send task or message end event whose message flow binds to a
remote participant (ADR-draft-participant-binds-a-published-interface) compiles to a
`TypeConnectorTask` carrying a reserved `compiler.*JobTypeIndex`, the way every
Worker Type task does. The job payload is the envelope plus the resolved target; the
credential is a vault reference resolved by the worker, not by the engine.

The element is **not** complete when the job is created. It completes when the worker
reports the delivery accepted — so a peer that is down parks a token on a send step,
which is exactly what an operator needs to see, and exactly how every other outbound
step in Atlas already behaves.

### What "accepted" means, and what it does not

The receiver acknowledges **acceptance of the envelope into its durable buffer**
(ADR-draft-durable-message-buffer) — that is, the envelope is on disk, past fsync,
and will be correlated when a subscriber exists or will expire trying. It is
explicitly **not** an acknowledgement that a process ran, that a human acted, or that
the outcome was favourable.

This has to be written down because the modeller will assume otherwise. A business
acknowledgement is a **reply message in the other direction**, modelled as such, with
the sender waiting at a catch event and a boundary timer for the case where it never
comes. Nothing in the transport can substitute for that, and a record that let people
believe otherwise would be the most expensive sentence in this epic.

### Failure, and who owns it

At-least-once delivery, with the receiver deduplicating on `messageId`. The retry
ladder is the task's (ADR-0135); the circuit breaker is per peer, so one unreachable
domain costs one probe rather than N incidents (ADR-0340); a budget bounds the calls
one instance may make (ADR-0291). When the retries are exhausted the job fails and
raises an incident **on the sending element, in the sending domain** — the domain
that modelled the interaction owns the fact that it did not happen. The incident
names the peer, the interface, the contract version and the transport failure, so the
repair path (ADR-0160) has something to act on.

### Receiving

A new route `POST /api/v1/peer/messages` takes one envelope. It sits in its own
access class (ADR-0199), authenticates a scoped API token (ADR-0194) and authorizes
the caller against the addressed interface's **send** grant
(ADR-draft-published-process-interface). Validation and authorization happen off the
run loop (ADR-0239); only the resulting publish command goes onto it. The response is
returned after the batch's fsync, never before (I2) — the acceptance promise above is
worth nothing otherwise.

### Consequences

- **Positive:** no new engine machinery, no new recovery path, and the operational
  surface an operator already knows — a parked token, a retry count, an incident, a
  worker to fix. A peer that is down degrades the way a failing connector degrades
  instead of inventing a second vocabulary for the same situation.
- **Negative / trade-offs accepted:** a Worker Type is a heavier packaging unit than
  this needs (ADR-0207/0208/0299 all apply, including the setup documentation
  ADR-0289 requires), and delivery latency is now job-scheduling latency rather than
  an immediate call. A cross-node handshake also consumes job-worker capacity that
  operators sized for outbound integrations.
- **Follow-ups / risks to watch:** whether the sending side needs its own delivery
  *state* visible per message, or whether the job and its incident are enough — the
  honest answer probably only arrives with the cross-node conversation view. Ordering
  is not promised: two messages sent in sequence may be accepted out of order, and any
  model that depends on order must say so with its own correlation, not assume the
  transport. And a peer credential is a long-lived secret on the sending side, with
  the rotation and revocation duty ADR-0129 already accepted for deploy tokens.

## Pros and cons of the options

### Option 1 — engine-native outbound
- Good: delivery state lives in the log with everything else, and a cross-node send is
  visible without a worker being configured at all.
- Bad: rebuilds retries, backoff, breaker, budget and incident reporting inside the
  processor, where every one of them is a chance to break I1/I2/I3 — and every one of
  them exists already, twenty lines away, on the job seam. It is the ADR-0164 mistake
  with a different payload.

### Option 2 — a reserved Worker Type (chosen)
- Good: inherits retries, breaker, budgets, incidents, repair and the offload choice;
  keeps network I/O strictly post-fsync and off the loop; nothing new to recover.
- Bad: packaging overhead; scheduling latency; shares worker capacity with integrations.

### Option 3 — external broker
- Good: delivery, ordering and buffering become somebody else's solved problem.
- Bad: a second operational component between two Atlas nodes that can already reach
  each other over HTTP, and the same ADR-0001 objection as elsewhere in this epic.

## Links

- builds on ADR-0007 (job worker protocol) and ADR-0203 (worker execution model)
- carries the envelope of ADR-draft-cross-instance-message-addressing
- requires ADR-draft-durable-message-buffer (at-least-once needs receiver dedup)
- authorized by ADR-draft-published-process-interface (the send grant)
- follows ADR-0129 for peer credentials, and ADR-0041 for credentials by reference
- relates to ADR-0135 (retries), ADR-0340 (circuit breaker), ADR-0291 (budgets),
  ADR-0061 (incidents), ADR-0194/0199 (token scope and route access), ADR-0239 (off-loop)
- honors the invariants in docs/architecture/invariants.md (I1, I2, I3)
