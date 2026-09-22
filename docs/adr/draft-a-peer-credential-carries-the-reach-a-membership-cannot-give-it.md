# ADR-DRAFT: A peer credential carries the reach a membership cannot give it

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-22
- **Deciders:** Atlas maintainers
- **Open question:** what the reach is expressed in. Projects are the unit sharing scopes
  already use, and a department plausibly *is* a project — but whether a real multi-domain
  installation's administrative units line up with its projects is a fact about that
  installation, not about Atlas, and nobody here has seen one. If they do not line up, the
  reach needs a unit of its own, and that is a larger change than this record.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0402](0402-one-estate-several-nodes.md) §1 wants a federated starmap read and finds an
escalation in it: a peer filters its answer for **the credential it was given**, not for the
human reading a screen on the asking side, and there is no mechanism to carry that human's
identity across an installation ([ADR-0373](0373-published-process-interface.md)'s open question records that there is none in
the tree beyond ADR-0129's deployment targets). §1 lists three ways out and gates the view on a local operator right, because
that was the only buildable one. Its own open question then records that the gate is likely
**insufficient** for the installation that most needs the view: in a multi-domain estate the
reader is plausibly a cross-departmental architect who is not the operator of each node, and
separation between administrative units is exactly what such an installation's
information-protection concept restricts.

Before that gate is built, the mechanism was measured. The finding is that the filtering is
already there and the *credential* is what cannot use it:

| | State in the tree |
|---|---|
| Filtering a landscape per caller | **exists, per item.** `panorama.Application.CanView` and `panorama.Process.CanView` are booleans on the collected landscape; `DeriveGraph` honours them and draws restricted placeholders for the rest. |
| Where those booleans come from | `api/panoramabindings.go` sets them from `Server.canViewArtifact`, which is `scopeRank(artifactRole(...)) >= Viewer`, and `artifactRole` defers to the project's `effectiveRole` for the request's principal. |
| A machine principal's role | **Viewer on everything.** `api/scopes.go` says why, in the code rather than in a wish: *"Sharing scopes cannot express it — there is no account to own or be a member of anything"*. |
| Why that is accepted today | The same comment states the trade-off and its bound: *"this is viewer on every application … The allowlist keeps that to exactly one read-only endpoint"*. |

So the escalation ADR-0402 §1 describes is not vague. It is `effectiveRole` returning Viewer
for a credential that cannot be a member of anything, and it is tolerated today precisely
because the route allowlist reduces it to one narrow read ([ADR-0129](0129-remote-deployment-targets.md)'s deploy agent). **A derived landscape is not that narrow**, so the reasoning that made the
trade-off acceptable does not carry over to the read ADR-0402 needs.

## Decision drivers

- **Least privilege is already the rule for a peer credential**, written into the scope list:
  `status` reaches the descriptor and nothing else, *"a credential handed to a peer should be
  the narrowest one that answers the question — here, one GET"* (`api/apitokenscope.go`).
- **The answering side owns its own disclosure.** Its protection concept is the one that
  says which unit may be seen from outside; nothing on the asking side can know that.
- **No second filter.** A landscape is already filtered per caller. A federated read that
  filtered somewhere else would be two mechanisms that must agree forever, and the day they
  disagree the picture discloses what the list hides.
- **No cross-installation identity.** Whatever is chosen must work without one, or it is
  ADR-0373 again.
- **Fail closed.** A credential whose reach is unstated must reach nothing on this route.
  Defaulting to everything is how the present trade-off became invisible.

## Considered options

1. **Make the machine principal a project member.** Sharing scopes get accounts for
   machines, and everything else follows unchanged.
2. **The credential carries the reach; the existing per-item filter applies it** (chosen).
3. **A scope name per unit** — `panorama.mesh.finance`, minted per department.
4. **Forward the reader's identity** — ADR-0373.
5. **Answer coarsely** — counts per domain and no more.

## Decision outcome

Chosen: **"the credential carries the reach; the existing per-item filter applies it"**.

A peer credential gains one field beside its scope: the set of subjects it may see, in the
vocabulary sharing scopes already use. `artifactRole` consults it where it today asks a
project for the principal's role, so the credential's reach lands in the **same**
`CanView` booleans a person's membership lands in — one filter, applied in one place, for
both kinds of caller. `DeriveGraph` changes not at all: it already draws exactly what
`CanView` allows.

Three consequences are part of the decision rather than side effects:

**A credential with no stated reach reaches nothing on this route.** Not everything, which
is today's behaviour and the thing being corrected. An unstated reach is a credential whose
operator has not said what it may see, and the honest answer to that is an empty landscape
with its restricted count stated — which the picture already knows how to draw.

**The reach is a maximum, not a grant.** It intersects with what the route's scope allows and
with what the answering installation would disclose anyway. A reach naming a project that no
longer exists narrows to nothing rather than widening to everything.

**It is not row-level security for the whole API.** Its reach is the derived landscape, which
is the one read ADR-0402 needs. Extending it to every route is a different record with a
different cost, and claiming it here would be claiming a guarantee nothing enforces.

### Why not the others

**Option 1 — machines as members.** It is the tidiest answer and the largest change: sharing
scopes would gain a principal kind that has no account, no owner and no way to be invited,
and every place that reasons about membership would have to mean both. `api/scopes.go`'s
comment is not an oversight to correct but a boundary that was drawn deliberately.

**Option 3 — a scope per unit.** The scope list is global, flat and validated
(`validAPIScope`), so a per-unit scope means data in a name and a list that grows with every
department. It also puts the answering installation's organisation chart into a vocabulary
the asking side can read, which is disclosure by itself.

**Option 4 — forward the identity.** There is no cross-installation identity in the tree
beyond ADR-0129's deployment targets, which is what ADR-0373's own open question says. This
record exists because of that.

**Option 5 — answer coarsely.** ADR-0402 §1 already refutes it: *"an estate view whose nodes
are 'domain B holds 12 applications' answers nothing the target list does not"*.

### Consequences

- **Positive:** ADR-0402's estate view becomes buildable for the installation that most needs
  it, and the federated read grants no reach the credential did not already have — which is
  the property §1 wanted from the gate and could not get.
- **Positive:** the trade-off `api/scopes.go` accepted in writing stops being invisible. A
  credential's reach becomes something an operator states rather than something a comment
  explains.
- **Negative / trade-offs accepted:** one more thing to configure per target, and an
  operator who states nothing gets an empty picture rather than a full one. That is the
  intended direction of the failure and it will be reported as a bug at least once.
- **Negative:** the reach is only as good as its vocabulary, which is the open question. If
  administrative units do not line up with projects, this buys a mechanism that cannot
  express the case it was built for.
- **Follow-ups / risks to watch:** whether the same field should bound the deploy agent's
  present Viewer-on-everything (it is the same defect, one route narrower); and
  ADR-0402 §1's gate, which stays as the fallback if this record is refused.

## Pros and cons of the options

### Option 1 — machines as members
- Good: no new concept; every existing filter works unchanged.
- Bad: a principal kind with no account inside a model built on accounts.

### Option 2 — the credential carries its reach (chosen)
- Good: one filter, one place, no cross-installation identity, fails closed.
- Bad: a second field on a credential, and a vocabulary question this record leaves open.

### Option 3 — a scope per unit
- Good: nothing new on the token.
- Bad: data in a name; a global list that grows per department; disclosure by scope name.

## Links

- relates to [ADR-0402](0402-one-estate-several-nodes.md) §1 — the record that commissioned
  this one, and whose gate is the fallback
- relates to [ADR-0373](0373-published-process-interface.md) — whose open question records
  that there is no cross-installation identity in the tree beyond ADR-0129's deployment
  targets, which is why the reach travels with the credential instead
- relates to [ADR-0129](0129-remote-deployment-targets.md) — the deploy agent whose
  Viewer-on-everything is the same defect one route narrower
- relates to [ADR-0071](0071-sharing-scopes.md) — the vocabulary the reach is expressed in
- relates to [ADR-0211](0211-panorama-derived-landscape-mesh.md) §3 — the per-caller
  filtering this reuses rather than duplicates
