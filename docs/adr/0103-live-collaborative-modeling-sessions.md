# ADR-0103: Live collaborative modeling sessions — real-time co-editing of drafts by people and AI agents

- **Status:** Proposed
- **Date:** 2026-08-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas can already be edited from two directions, but never *together at the same
time*:

- A person edits a diagram in the Modeler and **saves a draft** (ADR-0021). The
  draft store is keyed by process id and **overwrites on every save** — it is
  last-write-wins with no notion of a second editor
  (`api/draftstore.go`).
- An AI agent reaches the same drafts over MCP (ADR-0016, ADR-0032):
  `atlas_get_draft_xml` reads the XML, `atlas_save_draft` writes it back. This
  is **turn-based** — the agent produces a whole document and drops it in as a
  reviewable draft. There is no channel for the agent to watch a human edit, or
  for a human to watch the agent edit, as it happens.

Sharing already exists as an **authoring authorization** boundary: a project
carries `owner`, `visibility` (`private | shared`), and a `members` list of
`{ref, role}` with `viewer`/`editor` roles (ADR-0071), keyed to the identity
model (ADR-0044, ADR-0073). So "invite a colleague to this body of work" is a
solved *permission* question.

What is missing is the **live, shared session**: several editors — some human,
at least one AI agent joined over MCP — working on the *same* draft
simultaneously, each seeing the others' changes and presence in real time. The
vision is "sit at one table and build the model together." The three things that
do not exist yet:

1. **A real-time transport.** Atlas has none. The Operations live overlay is
   *polled*; there is no WebSocket or SSE anywhere in `api/`.
2. **A concurrency model for drafts.** The draft store is overwrite-only, so two
   editors (human + human, or human + agent) silently clobber each other.
3. **The AI agent as a live participant**, not just a producer of finished
   drafts.

The question: **how do we let multiple principals — people and MCP agents —
co-edit a draft in real time, reusing the sharing/identity model we already have,
without touching the engine or its invariants?**

## Decision drivers

- **Design-time only; the six invariants stay untouched.** Collaborative editing
  of a *draft* is design-time work. Like projects (ADR-0034) and sharing
  (ADR-0071), it must live in a **sidecar / HTTP layer below the API** and never
  reach the WAL, the processor, or `applyToState`. No hot path, no engine event,
  no recovery impact. This is the non-negotiable framing.
- **Reuse sharing and identity, don't reinvent them.** Who may join a session and
  in what capacity is exactly the project's `visibility` + `members` + role from
  ADR-0071, keyed to `User.ID`/principal refs (ADR-0044, ADR-0073). An `editor`
  may change the model; a `viewer` may only watch. The AI agent joins as a
  **principal** like anyone else.
- **Reuse the MCP surface for the agent.** ADR-0032 already models "an agent
  manipulates the canvas" as authoring tools. A live agent participant is *more
  of the same* (join a session, apply an edit, read presence), not a new
  integration style. It must stay **provider-neutral** — Atlas ships the session
  surface, not a model.
- **Smallest honest first slice, without boxing in the trajectory.** Ship one
  coherent vertical — presence + live change propagation + a coarse but correct
  concurrency rule — while leaving room for the expensive-to-change shape (true
  character-level concurrent editing).
- **Correctness over fluidity, first.** It is acceptable that the first cut
  forbids two people typing into the *same element* at once. It is **not**
  acceptable that concurrent edits silently corrupt or lose a draft, which is
  what the overwrite store does today.
- **Buildless UI (ADR-0012) and no heavy deps (ADR-0010).** The transport and any
  merge logic must fit a single Go binary and a buildless front end.

## Considered options

For the **concurrency model** (the load-bearing choice):

1. **Element-level locking + broadcast.** A session holds soft locks per BPMN
   element. Acquiring an element blocks others from editing *that* element; all
   participants receive the change immediately. Coarse, but simple and always
   correct — no merge to get wrong.
2. **Operation log / OT.** Stream bpmn-js command-stack operations, order them on
   the server, transform concurrent ops. Fluid, but a genuine
   operational-transform problem with a long tail of edge cases.
3. **Full CRDT** over the document. Google-Docs-grade simultaneous editing.
   Most powerful, materially the most complex and the slowest to ship, and the
   hardest to keep inside a small binary.

For the **transport**:

- **a. Server-Sent Events (SSE)** for server→client fan-out, plain HTTP POST for
  client→server ops. One-directional stream, trivially proxyable, no new
  protocol, no dependency.
- **b. WebSocket** full-duplex.

For **where a session lives**:

- **i. A design-time sidecar session registry** below the HTTP API, holding
  ephemeral session state (participants, locks, the current draft op-sequence),
  owned by a single run-loop goroutine — the same discipline as ADR-0034/0071.
- **ii. Model the session in the engine** as its own process/event stream.

## Decision outcome

Chosen option: **element-level locking + broadcast (1), over SSE + HTTP POST (a),
in a design-time sidecar session registry (i).** Sharing and identity are reused
verbatim; the AI agent joins over MCP as a principal.

A **collaboration session** is an **ephemeral, design-time** object attached to a
draft (by `processId`). It is **not** event-sourced, **not** durable across a
restart, and **never** touches the engine — it is server memory guarded by the
run-loop goroutine, exactly the placement ADR-0071 chose for authorization data.
The *draft* remains the durable artifact (ADR-0021); a session is just the live
coordination around editing it.

**Membership and roles come from the project's sharing scope (ADR-0071).** To
join a session for a draft, a principal must be able to *reach* that draft under
the existing rules: `editor`/owner may make changes, `viewer` may observe. Under
`--auth` off, everything stays open exactly as today (ADR-0044/0071 stance) — the
session fields exist but enforcement is a no-op. **Zero blast radius on existing
single-user deployments.**

**Transport.** A new SSE endpoint (`GET /api/v1/drafts/{id}/session`, subject to
the same principal/scope check) streams session events to every participant:
`presence` (who joined/left, their selection), `lock` (element locked/released),
and `change` (an element was edited, with the resulting element XML/patch).
Clients push their own actions with plain `POST` to sibling endpoints
(`.../session/lock`, `.../session/change`, `.../session/presence`). SSE keeps the
transport a single one-directional HTTP stream — no new protocol, nothing for a
reverse proxy to special-case, no dependency added to the binary.

**Concurrency.** The session holds **soft, auto-expiring locks per BPMN element**.
An editor acquires an element before mutating it; the lock broadcasts so others
see it as "held by Anja" (or "held by Claude"). Edits to *different* elements
proceed in parallel and stream to everyone; edits to the *same* element are
serialized by the lock. A lock lapses on release, on disconnect, or after a short
TTL so a dropped client never wedges an element. The **draft is persisted through
the normal ADR-0021 path** — the server folds accepted changes into the draft
XML and writes it with the existing atomic-write store, so a session that ends
(or a server that restarts) leaves a normal, valid draft behind and nothing else.

**The AI agent is a live participant over MCP.** ADR-0032's authoring group grows
a small session-aware set, so an agent can `join`/`leave` a draft session,
`acquire`/`release` an element lock, `apply` a change, and read `presence` — the
same operations the browser performs, exposed as MCP tools and keyed to the
agent's principal. The agent thus appears at the table like any other editor:
you see Claude take a lock on a gateway and add its branches live, and Claude
sees your edits arrive on the stream. This stays provider-neutral — Atlas ships
the session tools, not an LLM (ADR-0032).

**Why not the richer concurrency models now.** Option 2 (OT) and option 3 (CRDT)
both buy *simultaneous editing of the same element*, which element-locking
forbids. That is a real limitation but a tolerable first cut: BPMN authoring is
node-and-edge structural editing, not prose, so per-element granularity already
lets a room of people work productively in parallel. Locking is **always correct
with no merge algorithm to get wrong**, ships inside the binary, and — critically
— leaves the door open: the transport, the session registry, the presence model,
and the sharing/identity wiring are all identical whether the payload is a
whole-element replace (now) or a stream of transformed ops (later). We can
upgrade concurrency under a fixed session API. Modeling the session in the engine
(ii) is rejected outright: it would drag design-time, throwaway coordination
state onto the WAL and into recovery, violating the invariant boundary every
prior design-time ADR has held.

### Consequences

- **Positive:** Real-time co-editing of a draft by people *and* AI agents, built
  entirely in the design-time layer with the six invariants untouched; reuses
  ADR-0071 sharing, ADR-0044/0073 identity, ADR-0021 drafts, and ADR-0032's MCP
  authoring surface rather than inventing new concepts; SSE adds a real-time
  transport with no new dependency and no protocol a proxy must learn;
  concurrency is always correct because there is no merge to be wrong; the agent
  is a first-class participant, which is the heart of the request; enforcement is
  opt-in (`--auth`) with zero impact on existing open deployments.
- **Negative / trade-offs accepted:** No simultaneous editing of the *same*
  element in the first cut (per-element locks); a session is **ephemeral** —
  presence, locks, and unsaved in-flight changes do not survive a server restart
  (only the persisted draft does), which must be stated clearly in the UI; SSE is
  one-directional, so client actions ride a separate POST path (a small
  asymmetry); the coarse model will feel less fluid than Docs-style editing until
  a later op/CRDT upgrade; live sessions add fan-out and per-element lock
  bookkeeping to the run-loop goroutine that must stay non-blocking.
- **Follow-ups / risks to watch:** upgrade concurrency to an operation log / OT
  or CRDT under the fixed session API when per-element locking chafes; **runtime**
  collaboration is explicitly out of scope — this governs editing *drafts*, not
  running instances (same boundary ADR-0071 drew); reconnection/replay semantics
  for a client that drops mid-session (resume token on the SSE stream);
  presence/awareness richness (live cursors, selection highlights) beyond
  join/leave; back-pressure and fan-out limits for large rooms; making the AI
  agent's edits legible in the change stream (attribution, so a human can review
  what Claude changed); the `/mcp` unauthenticated caveat (ADR-0016) applies to
  the new session tools exactly as to the existing authoring tools.

## Pros and cons of the options

### Concurrency model

**Option 1 — element-level locking + broadcast (chosen).**
- Good: always correct (no merge); simplest thing that solves the actual
  corruption problem; fits the binary; leaves transport/session/sharing wiring
  reusable when concurrency is later upgraded; matches BPMN's structural,
  node-and-edge editing granularity.
- Bad: no two people in the same element at once; a coarse "held by X" experience
  rather than character-level co-editing.

**Option 2 — operation log / OT.**
- Good: fluid concurrent editing; fine-grained.
- Bad: genuine OT complexity with a long edge-case tail; more to get wrong;
  heavier first slice for a benefit (same-element co-editing) BPMN rarely needs.

**Option 3 — full CRDT.**
- Good: best-in-class simultaneous editing; offline-friendly convergence.
- Bad: materially the most complex and slowest to ship; largest footprint in a
  single small binary; overkill for structural diagram editing as a first cut.

### Transport

**(a) SSE + HTTP POST (chosen)** — Good: one-directional stream, no new protocol,
trivially proxyable, zero dependency, fits buildless UI. Bad: client→server rides
a separate POST channel (asymmetry); long-lived connections to manage.
**(b) WebSocket** — Good: full-duplex, symmetric. Bad: heavier to operate and
proxy; more than one-way fan-out + occasional POST requires.

### Where a session lives

**(i) design-time sidecar registry (chosen)** — Good: keeps throwaway
coordination state entirely out of the engine and its invariants; same discipline
as ADR-0034/0071; a dead session leaves only a normal draft behind. Bad: session
state is not durable (accepted — it is ephemeral by nature).
**(ii) session as an engine process/event stream** — Good: durable, replayable.
Bad: drags design-time, ephemeral state onto the WAL and into recovery; violates
the invariant boundary; a category error for coordination metadata.

## Links

- extends [ADR-0032](0032-modeler-ai-copilot.md) (MCP authoring surface) with a
  live, session-aware participant model
- reuses [ADR-0021](0021-diagram-drafts.md) (the draft is the durable artifact a
  session edits)
- reuses [ADR-0071](0071-sharing-scopes.md) (project visibility + members + roles
  decide who may join and in what capacity) and
  [ADR-0044](0044-user-management-and-authentication-boundary.md) /
  [ADR-0073](0073-principals-directory.md) (identity; the agent joins as a
  principal)
- builds on [ADR-0016](0016-mcp-server-over-http-api.md) (MCP over the HTTP API)
- constrained by [ADR-0010](0010-go-and-no-cgo.md) / [ADR-0012](0012-web-ui-app-shell.md)
  (single binary, buildless UI) and the six invariants
  ([`docs/architecture/invariants.md`](../architecture/invariants.md)) — session
  state is design-time only and never reaches the engine
