# ADR-0369: Addressing and the envelope for a message that leaves the node

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** whether a participant on the far side is always another Atlas, and
  whether peers ever cross an organisational boundary. If a foreign system can be a
  participant, this envelope has to map onto an open format (CloudEvents / AsyncAPI)
  rather than remain Atlas's own; if peers cross organisations, bearer credentials are
  the wrong trust primitive and mTLS is the right one.
- **Question checked:** 2026-09

## Context and problem statement

Atlas is meant to run one instance per domain, and processes in one domain need to
reach processes in another. Everything the engine has for that today stops at the
node boundary:

- `correlateMessage` (`engine/behavior.go:2522`) scans the **local** transaction. A
  message that matches nothing is a no-op and is lost (ADR-0020).
- A message start event matches **by name only** (ADR-0035), and `Processor.Deploy`
  folds every deployed definition into one `messageStarts[name]` table. A bare name
  is therefore a process-wide broadcast channel.
- A node runs one partition (`api/node.go:193`, `Partitions: 1`), so even the
  cross-partition routing ADR-0006 anticipated has never had to be built.
- The collaboration replay (ADR-0038) keys history by `receiverDefKey` and finds
  sibling pools by **byte-identical BPMN**. Neither survives two nodes.

Extending the local semantics over the wire would make the message *name* a global
namespace shared by every domain that can reach the node: two domains that both use
`OrderPaid` would cross-start each other's processes. That is an availability and an
authorization failure at once, and no amount of care in the modeler prevents it.

Before anything is delivered, therefore, one question has to be settled: **what is
the address of a message that leaves the node, and what travels with it?**

## Decision drivers

- **Invariants hold.** Whatever identifies a delivery must be frozen into the event
  and re-read on replay, never regenerated (I6). Nothing is allocated per command on
  the processor path (I1), and no network I/O happens before fsync (I2).
- **Decide the envelope before the features that depend on it.** A conversation
  identifier is cheap to introduce now and expensive to retrofit: every record
  written without one is a record that can never be correlated across nodes.
- **Reuse the identity that already exists.** `/api/v1/node` (`api/node.go:62`) is a
  stable runtime identity, distinct from the binary's build info, and ADR-0129
  targets already address a peer by URL plus a vault credential reference.
- **Stay inside the Atlas runtime contract.** Cross-instance delivery is the kind of
  behaviour ADR-0176 §3 reserves for the versioned Atlas contract, not something a
  standard settles for us.

## Considered options

1. **Address by bare message name**, extending today's semantics over the wire.
2. **Address by `(node, published interface, entry point)` with a versioned
   contract**, carried in an explicit envelope alongside the payload.
3. **Address by topic on an external event log** (clio, ADR-0036/0075), letting the
   log own identity, ordering and retention.

## Decision outcome

Chosen option: **2 — a qualified address plus an explicit envelope.**

### The address

A message crossing a node boundary is addressed by
`(target node, published interface id, contract version, entry point)` — never by a
bare message name. The entry point is a **name in the published contract**
(ADR-0373), not a foreign element id, so the publisher
may refactor its model freely as long as the contract name stands.

The receiving node maps the entry point to its local message name. **A peer message
never enters the local `messageStarts[name]` index unqualified**, which is what keeps
a reachable node from being startable by anyone who guesses a message name.

### The envelope

Every cross-node delivery carries:

| field | purpose |
|---|---|
| `messageId` | idempotency key; the receiver correlates it at most once |
| `conversationId` | minted by the initiator, copied by every reply; the cross-node join key |
| `causationId` | the `messageId` this one answers, for ordering within a conversation |
| `senderNodeId` | the sending node's `/api/v1/node` id, not its URL |
| `senderInstanceKey` | the sending process instance, for the sender's own trace |
| `interfaceRef` + `contractVersion` | what was addressed, and under which contract |
| `entryPoint` | the contract name being hit |
| `correlationKey` | the FEEL canonical string, exactly as ADR-0020 compares it |
| `issuedAt` / `expiresAt` | when it was minted and when buffering it stops being useful |

`messageId`, `conversationId` and `causationId` are generated **at command time and
frozen into the event** (I6), like every other generated key in the log. They are
never produced inside `applyToState` (I4).

### Why the conversation identifier is in this record and not a later one

It is the only field here that cannot be added cheaply afterwards. A delivery
recorded without one can never be joined to the exchange it belonged to, so a
cross-node conversation view built later would start with a blind spot exactly as
wide as the period before it was introduced. The view itself is deliberately *not*
decided here — this record only guarantees that the data it will need exists.

### Consequences

- **Positive:** a reachable node is no longer startable by message name alone. Every
  delivery carries what a retry needs (`messageId`), what a trace needs
  (`conversationId`, `causationId`) and what a version check needs
  (`contractVersion`). Local correlation semantics are untouched: ADR-0020 and
  ADR-0035 keep working exactly as they do for messages that never leave the node.
- **Negative / trade-offs accepted:** the envelope is an Atlas format, so a foreign
  system cannot participate without an adapter — the open question above is precisely
  this. It also adds a second addressing vocabulary next to "message name plus
  correlation key", and a reader now has to know which one applies where. And a
  conversation identifier that the initiator mints is only as good as the modeller's
  discipline in carrying it: a reply that drops it is untraceable, and nothing in the
  engine can force it back.
- **Follow-ups / risks to watch:** a cross-node conversation view over
  `conversationId`, which is the ADR-0038 replay one boundary further out and needs
  ADR-0370 and ADR-0372 first.
  The same qualified address would serve cross-*partition* correlation inside one
  node (ADR-0006, still `Partial`); designing it twice is the risk worth watching.

## Pros and cons of the options

### Option 1 — bare message name
- Good: nothing new to learn; the wire form is what the model already says.
- Bad: turns every message name into a global topic across every node that can reach
  this one, which is unfixable afterwards without breaking deployed models. No
  idempotency key, so any retry duplicates business effect. No trace identity.

### Option 2 — qualified address plus envelope (chosen)
- Good: the publisher controls its own namespace; retries are safe; a conversation is
  reconstructable; the contract version is checkable at deploy and at delivery.
- Bad: a second addressing vocabulary, and an Atlas-specific envelope that a
  non-Atlas participant cannot speak without an adapter.

### Option 3 — external event log
- Good: identity, ordering, retention and replay are the log's problem, and clio is
  already integrated (ADR-0036/0075).
- Bad: a message flow is point-to-point and conversational; a log is broadcast. It
  also moves the correlation state outside the engine log, which is the boundary
  ADR-0020 declined to cross for exactly the ADR-0001 reason. Deferred rather than
  dismissed: it remains the better answer if the dominant interaction turns out to be
  domain events rather than conversations.

## Links

- builds on ADR-0020 (message events and correlation) and ADR-0035 (message start events)
- constrains ADR-0370, ADR-0372,
  ADR-0373
- relates to ADR-0006 (partition routing — the same address one level down)
- relates to ADR-0038 (collaboration replay — what a cross-node view would need)
- relates to ADR-0129 (deployment targets — peer identity and credentials)
- relates to ADR-0176 (standards boundary — this is Atlas runtime contract)
- honors the invariants in docs/architecture/invariants.md (I1, I2, I4, I6)
