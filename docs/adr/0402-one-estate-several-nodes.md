# ADR-0402: One estate, several nodes — a starmap stitched from subgraphs

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Open question:** whether an estate view is ever read by somebody who may not see all
  of it. The operator gate in §1 is right only while the answer is "no". Looked at in
  2026-09, and the question now has an addressee rather than a hypothetical: multi-domain
  installations exist, and a federal administration is one of them. There the reader who
  wants an estate picture is plausibly a cross-departmental architect who is *not* the
  operator of each node, and separation between administrative units is precisely what such
  an installation's information-protection concept restricts. So the gate is likely
  **insufficient** for the customer who most needs this view, rather than merely unproven.
  That does not change the gate — it is still the only posture buildable today — but it
  moves the cross-installation identity
  [ADR-0373](0373-published-process-interface.md) could not settle from a follow-up to the
  thing this feature actually waits on.
- **Question checked:** 2026-09

## Context and problem statement

The starmap draws one server. A peer appears on it as a single `target` node with no
edges, and ADR-0211's own comment says why: a promotion is an act, not a stored
relationship, and this server does not record which of its applications is running over
there.

In an estate of one Atlas per domain ([#986](https://github.com/pblumer/atlas/issues/986))
that means there is no picture of the estate at all — at exactly the altitude Panorama
exists to serve. Every domain has a landscape; nobody has the landscape.

What already exists, and does not have to be built:

- **the peer read channel** (`api/panoramaremote.go`): off the run loop, an 8-second
  deadline, four-way bounded concurrency, a response-size limit, TLS verified by the same
  client the promotion path uses, per-target error isolation, and a cache that keeps
  *unreachable* and *stale* apart on purpose;
- **a descriptor that advertises what a node can be asked for**, derived from the routes
  it actually mounts — and `panorama.mesh` is already in that list (`api/node.go`,
  `nodeFeatures`);
- **a credential per target**, stored as a vault reference and never as a secret
  ([ADR-0129](0129-remote-deployment-targets.md),
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md));
- **the budget, the cache, the restricted placeholder, the provenance legend and the
  severity mapping** (ADR-0211 §§2–4, §7).

So the mechanism is almost entirely assembly. What needs deciding is not how to fetch —
that is solved — but what the stitched picture is allowed to claim, and who may look at
it.

## Decision drivers

- **Every line is a fact the server can point at** (ADR-0211 §1). A cross-node edge has
  to meet that test or it does not get drawn.
- **A filtered picture must say that it is filtered** (ADR-0211 §3). The restricted
  placeholder exists because an absence that means "you may not see this" reads as "this
  depends on nothing".
- **The budget is measured, not chosen.** 400 nodes is where browser layout stops
  painting in about a second.
- **There is no cross-installation identity.** ADR-0373 says so in its own open question,
  and ADR-0129's targets are the only trust relationship in the tree.
- **A peer is asked, never trusted to be present.** The four states are already the
  contract, and collapsing them is the failure `panoramaremote.go` was written to avoid.

## Considered options

1. **A central estate registry** — one node, or a new component, holds the estate graph
   and every node reports into it.
2. **Each node derives its own subgraph; a reader's node asks reachable peers for theirs
   and stitches.**
3. **Export and diff offline** — each node's ArchiMate export
   (`GET /api/v1/panorama/mesh/archimate`) is deterministic and committable, so an estate
   picture is a job somebody runs in CI over N exports.

## Decision outcome

Chosen: **option 2 — every node keeps deriving its own subgraph, and the reader's node
stitches.** No node becomes the estate's master, nothing new is stored, and the graph
inherits the derivation rules it already has. Identities are
ADR-0401'.

Four things the stitching has to decide.

### 1. Authorization is not transitive, and pretending otherwise is an escalation

A peer filters its answer for **the credential it was given** — the target's — not for the
human reading a screen here. ADR-0211 §3 computes the mesh per requesting principal; that
principal does not exist on the far side, and there is no mechanism to carry it there.

So a federated read has a real escalation in it: a viewer on node A would see node B's
estate at the reach of A's stored credential. Three ways out, and only one is buildable:

- **forward the reader's identity** — needs the cross-installation identity ADR-0373 could
  not settle. Not available;
- **make the peer's answer coarse enough that the credential's reach does not matter** —
  honest, and nearly worthless: an estate view whose nodes are "domain B holds 12
  applications" answers nothing the target list does not;
- **gate the federated view on the local right that already sees the whole estate.**

Chosen: **gate it.** The estate view is an operator's view, behind the same local right
that configures deployment targets — because whoever configured them already holds the
credential whose reach is in question, so the view grants no reach they did not have.

On the answering side, a **new least-privilege scope**. `status` reaches the descriptor
and nothing else, deliberately and for exactly this reason
(`api/apitokenscope.go`: "a credential handed to a peer should be the narrowest one that
answers the question — here, one GET"). A starmap read is a second, much wider question
and must not be folded into `status`. It gets its own scope, whose whole reach is the
derived landscape, and which can neither deploy, read an instance, nor list a person.

And the picture **states whose credential drew each subgraph**, beside the same legend
that already carries the restricted count. The same discipline: an incompleteness that is
stated is a fact; one that is not is a discovery.

### 2. The budget is per node and cannot be multiplied

Eight domains at 400 nodes each is a hairball by arithmetic, inside every individual
budget. So the estate view is **not L0 repeated — it is an altitude above L0**:

| Level | Content | Owner |
|---|---|---|
| **L-1 Estate** | one node per domain, joined where promotion joined them | Panorama (new) |
| L0 Landscape | one domain's derived mesh | Panorama, existing |
| L1 Application | one application | Panorama, existing |
| L2 / L3 | process and instance | Operations, existing |

A node at L-1 is a **domain**, expanded one at a time into the L0 view that already
exists. The estate view's own budget is the number of domains, which is operator
configuration. The collapse-to-applications fallback ADR-0211 §7 already ships has exactly
the shape the expansion needs, which is why this is an extension of the existing altitudes
(§5) rather than a new picture.

### 3. A peer that does not answer is a shape, not a gap

The four states carry over unchanged — *unreachable* is nothing is known, *stale* is
something is known and may be wrong, and the two are never rendered alike (ADR-0211 §4).
A federated starmap adds a fifth case that deserves its own word rather than being folded
into either:

**the peer answered, and does not serve this view.** An older build whose descriptor
advertises no `panorama.mesh` feature is neither unreachable nor stale — it is a version
boundary. Reading it as unreachable sends an operator to look at a network; reading it as
stale implies there was once an answer. The descriptor's derived feature list is what makes
this distinguishable at all, which is the property it was built for.

### 4. No edge is invented across the boundary

The only estate-wide edges that are facts today are the application joins a promotion
recorded. Two things are therefore explicitly refused:

- **a message flow somebody drew is not an edge here.** ADR-0023 made the arrow
  documentation and the parser still does not read `<messageFlow>`; drawing it on a derived
  picture would be a drawn assertion on the one canvas that admits none;
- **a peer's promotion history is not an edge either.** This server knows it promoted
  application X to target T. It does not know what else T runs, or where T promotes onward.
  An edge implying it does would be inference dressed as fact.

Real cross-node edges arrive with #986: a published interface ([ADR-0373](0373-published-process-interface.md))
is a declared contract, and a delivered envelope
([ADR-0372](0372-peer-message-delivery-worker.md)) is an observed traversal in the sense of
ADR-0400. Both are facts, and
both are drawable when they exist. Until then the estate view draws domains, their
contents and the promotion joins, and says that is what it draws.

### Consequences

- **Positive:** buildable **before any of #986 lands**, because it rides ADR-0129 targets
  and the existing peer channel rather than messaging. The value is real on day one: which
  domain runs which version of a shared definition, and where an application has drifted
  between Test and Production — the comparison that today means opening two browser tabs
  and reading carefully.
- **Positive:** no node becomes a master, so there is nothing to elect, nothing to
  reconcile, and an estate of two works the same way as an estate of twenty.
- **Negative / trade-offs accepted:** it is an operator's view, and will stay one until
  cross-installation identity exists. A domain owner cannot be given a scoped estate view,
  and this record does not pretend otherwise. Known as of 2026-09 to bind rather than to be
  hypothetical: see the open question. For a multi-domain public administration this is not
  a limitation at the edge of the feature — it is plausibly the difference between a view
  somebody may open and one nobody may.
- **Negative:** the picture's completeness is a function of reachability at the moment it
  was drawn. Four peers answering and one timing out is a different picture from the same
  estate a minute later, and the freshness of each subgraph has to be rendered *into* it —
  the same requirement ADR-0211 §10 already puts on every exported starmap, for the same
  reason: an undated all-green picture is believed long after it stopped being true.
- **Negative:** it is **not** an operational picture of the estate. Every peer's status is
  still that peer's, at the freshness of its last answer, and no aggregate severity across
  domains is computed. A cross-domain "is the estate healthy" reading would be a
  monitoring claim, which ADR-0189's non-goal refuses and #986 refuses again.
- **Follow-ups / risks to watch:** the fan-out cost when a domain count grows past the
  four-way concurrency bound; whether a peer should be asked for a *summary* subgraph at
  L-1 and the full one only on expansion (almost certainly yes, and it needs a route the
  peer serves rather than a client-side trim); and the reverse direction — a node that
  wants to know it is *in* somebody's estate view has no way to find out, which is a
  privacy question nobody has asked yet.

## Pros and cons of the options

### Option 1 — a central estate registry
- Good: one place to query, cheap reads, a natural home for history.
- Bad: a new component to operate and back up, against the single-binary posture; an
  election or a designation problem; and every node now has to push, which is a write path
  where today there is only a read. It also makes the estate picture only as fresh as the
  last report, replacing "I asked and it did not answer" with "it has not reported", which
  is strictly less informative.

### Option 2 — derive locally, stitch at the reader (chosen)
- Good: no new storage, no master, no new protocol — the channel, the credential, the
  feature advertisement and the four states all exist; each node stays the authority on
  itself; authorization stays where it already is on each side.
- Bad: cost is paid per read and grows with domain count; the picture is bounded by
  reachability; and it is an operator's view until identity crosses installations.

### Option 3 — export and diff offline
- Good: zero server work, deterministic and committable artifacts, and a real answer for
  "what changed between releases".
- Bad: not a view. It answers an auditor's question on a schedule and cannot answer an
  operator's question now. Worth keeping *as well* — the deterministic ArchiMate export
  already exists and this is the estate-wide use for it.

## Links

- extends ADR-0211 (§§2–5, §7, §10 — the derivation rules, the altitudes, the budget and
  the export honesty) and ADR-0189 §6 (the remote read constraints, unchanged)
- builds on ADR-0129 (targets and their credential), ADR-0069 (the vault reference), the
  node descriptor's derived feature list (ADR-0189 P4a), ADR-0071 (the local sharing scopes
  the gate sits beside)
- requires ADR-0401; bounded by
  ADR-0403
- blocked-on-nothing, but completed by [#986](https://github.com/pblumer/atlas/issues/986):
  ADR-0373's contract and ADR-0372's delivery are what finally make a cross-node edge a
  fact
