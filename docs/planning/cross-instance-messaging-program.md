# Cross-instance Messaging: delivery programme

**Programme:** CIM (Cross-instance Messaging)  
**Baseline checked:** 2026-11-05, `main` (programme registration only; no runtime implementation claimed).  
**Roadmap owner:** [Milestone CIM](../../ROADMAP.md#milestone-cim--cross-instance-messaging-).  
**Architecture authority:** [ADR-0373](../adr/0373-published-process-interface.md), [ADR-0369](../adr/0369-cross-instance-message-addressing.md), [ADR-0372](../adr/0372-peer-message-delivery-worker.md) (all **Accepted / Not started**) and [ADR-0371](../adr/0371-participant-binds-a-published-interface.md) (**Proposed / Not started**).  
**Programme owner:** pblumer.  
**Related programme:** [Device Automation](device-automation-program.md) — its IOT7, an Atlas on a device coupled to a central one, is a case of this work.

## Outcome and boundaries

Several Atlas instances are planned. A process on one of them sends a message to a
process on another — to start it through a published entry point, or to continue an
instance that waits — and receives replies, errors and escalations back. Every
delivery is durable on both sides, addressed by a contract rather than a guessable
message name, authorized per interface, and visible to an operator while it waits.

**The demonstration that closes the core (CIM1–CIM3):** two instances; a process on A
starts a process on B through B's published interface and waits for its reply; B is
stopped while A sends, A's token parks visibly on the send step, B comes back and the
conversation completes exactly once; both logs replay to the same state.

**Non-goals.** No transaction across two logs: a consumer models the deadline of a
commitment as a boundary timer and decides what happens when it passes. No broker
beside the job store (ADR-0203, ADR-0372 option 3). No replication of one partition
across instances — that is ADR-0175's question, not this programme's.

**Invariants.** Delivery is a Worker job after fsync (I2); the receiver acknowledges
only once the envelope is past its own fsync; nothing new on the processor path (I1,
I5); keys and timestamps travel in the envelope and are never regenerated (I6).

## Baseline

| Area | Verified reference | Status / limitation |
|---|---|---|
| Publishing an application to another server | ADR-0129 | Landed: deploy-scoped token, bundle import, per-target binding |
| One estate across several nodes | ADR-0402, ADR-0410 | Landed: read-only, stitched from each node's subgraph, peer credentials carry their reach |
| Durable message buffer, idempotent by `messageId` | ADR-0370 | Landed as the local slice; delivery from a peer is not |
| Process-to-process messages between instances | ADR-0369, ADR-0372, ADR-0373 | **Not started.** No compiler element, no reserved job type, no interface record in the tree (checked 2026-11-05) |
| Today's workaround | REST task to `POST /api/v1/messages` of another instance | Works, but addresses by bare message name, has no contract, and needs a broad token: no scope reaches only message delivery |

## Slices

| Slice | Status | Depends on | Closure gate |
|---|---|---|---|
| **CIM0 — Topology, trust and open questions** | 🚧 | — | The planned instances described; ADR-0369's and ADR-0373's open questions answered; threat model for peer credentials; ADR-0371 decided |
| **CIM1 — First cut: deliver to a configured peer** | 🔲 | CIM0 | ADR-0372's first cut: peer Worker Type, full envelope, completion on the receiver's fsync, delivery to subscriptions and the buffer only, scoped receive token, recovery tests on both sides |
| **CIM2 — Published process interface** | 🔲 | CIM0 | ADR-0373's descriptor, derived and versioned, read and send grants apart |
| **CIM3 — Qualified entry points** | 🔲 | CIM1, CIM2 | Start by message only through a published entry point; never unqualified into `messageStarts` |
| **CIM4 — A participant names an interface** | 🔲 | CIM3 | ADR-0371 accepted and built: model vocabulary, per-server resolution, deploy checks |
| **CIM5 — Replies, errors and commitments** | 🔲 | CIM3 | Returns travel back on the `conversationId`; commitments become modelled deadlines |
| **CIM6 — Operations across instances** | 🔲 | CIM1 | In-flight messages in Console, MCP and `/metrics`; flows in the estate view; incidents naming the peer |
| **CIM7 — Intermittent peers** | 🔲 | CIM1 | With IOT7: hours-long outages, parked sends that resume, lifetimes that outlast them |

### Dependency path

```text
CIM0 topology + trust --+--> CIM1 configured peer --+--> CIM6 operations
                        |                           +--> CIM7 intermittent peers (with IOT7)
                        +--> CIM2 interface --------+
                                                    v
                                          CIM3 qualified entry points
                                               |              |
                                     CIM4 participant    CIM5 replies and commitments
```

## Open questions

- How many instances, in which roles, whether one is central and whether any crosses
  an organisation (CIM0; the owner's description is pending).
- Whether a peer is always an Atlas, or a foreign system may be one — then the envelope
  maps onto an open format (ADR-0369).
- Whether grants live with each publisher or in a central catalogue (ADR-0373).
- The size of CIM1–CIM3, not estimated yet.
