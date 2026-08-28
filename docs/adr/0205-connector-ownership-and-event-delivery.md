# ADR-0205: Who owns a connector, and who may use the events it brings in

- **Status:** Accepted (2026-08-27: the configuration half is implemented; the
  message-name claim is not built yet)
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0071](0071-sharing-scopes.md) gave design-time work an owner and a share
list, and [ADR-0180](0180-groups-as-members.md) let a whole group be one
member of it. Both stop at the same line, which ADR-0071 draws in as many words:
sharing governs **design-time content**, and runtime visibility is "explicitly out
of scope".

A **connector** (ADR-0041) sits on the far side of that line. It is modelled as
operator configuration: `api/connectorstore.go` stores an id, a name, a kind, an
endpoint, a credential *reference*, and nothing that says whose it is. That was a
fair description of a connector while every connector was infrastructure — one
Jira site, one clio instance, one outbound SMTP relay, configured once for the
installation.

[ADR-0075](0075-clio-inbound-event-bridge.md) changed what a connector can be. An
**inbound subscription** watches an external subject and republishes what arrives
as an Atlas message, so an external event starts processes and wakes waiting ones.
The connector stopped being only a way to *reach out* and became a way for the
outside world to *reach in*.

The case that forces this record does not exist yet. Inbound is clio-only today;
the mail connector is outbound (ADR-0079/0093), and an *inbound* one is neither
built nor on the roadmap. It is what was asked for — "if I have an inbound mail
connector, nobody but me may use those events" — and a mailbox is personal in a way
a Jira site is not. Deciding the ownership model while the mailbox is still
hypothetical is the cheap moment; deciding it after people have one is the moment
where every answer costs a migration.

### What is actually true today

Measured against a server started with `--auth`, as an ordinary account holding
only the `user` role:

| It can | Because |
|---|---|
| Create a connector | `handleCreateConnector` has no role gate |
| List **every** connector, with endpoints and sender mailboxes | `handleListConnectors` has none either |
| Edit and **delete somebody else's** connector | same; `DELETE …?force=true` answered `204` |
| Read **every** inbound subscription — so every message name | `handleListInboundSubscriptions` has none |
| Add a subscription to somebody else's connector, publishing under a message name it chooses | `handleCreateInboundSubscription` has none |

Only `POST /api/v1/connectors/{id}/provision-clio-key` calls `requireAdmin`. The
rest of the surface is "any authenticated principal", which under ADR-0195 means
any account on the installation.

And past the configuration is the delivery. `Processor.PublishInbound` carries a
**message name and a correlation key** — nothing else. Every deployed definition
with a matching message-start event starts. So the message name *is* the
authorization, and the message name is readable by everyone from the listing
above.

The question: **how does a person come to own a connector, share it with whom
they choose, and have the events it brings in reach their processes and no
others — without a second sharing vocabulary and without reopening the six
invariants?**

## Decision drivers

- **The events, not only the configuration.** "Nobody but me may use these events"
  is the actual requirement. A measure that locks the connector record and leaves
  the message deliverable to any process that names it has answered a different
  question.
- **One sharing vocabulary.** ADR-0071's `ownerId` + `visibility` + `members[{ref,
  role}]` already exists, is already understood, and already carries groups
  (ADR-0180). A second model for connectors would be two things to learn and two
  things to get wrong.
- **The invariants stay shut.** ADR-0071's discipline is the precedent: access
  data lives in a sidecar store below the HTTP API and is enforced in the handlers
  off the resolved principal. Nothing here may reach the WAL, the processor, or
  `applyToState`.
- **Auth off stays open.** With `--auth=false` the server is open by declaration
  (ADR-0195); every rule here is a no-op there, as ADR-0071's are.
- **Say what a gate is not.** A check at the door is not isolation in the room.
  Whatever is chosen has to state plainly which of the two it is, or the next
  reader will assume the stronger one.

## Considered options

1. **Do nothing**, and document connectors as installation-wide operator config.
2. **Admin-gate the connector surface.** Add `requireAdmin` to the handlers that
   lack it.
3. **Owner + sharing scope on the connector**, reusing ADR-0071's three fields
   verbatim.
4. **Owner on the subscription only**; the connector stays shared infrastructure.
5. **Isolation in the engine**: the published message carries a scope, the
   subscription carries one, correlation compares them.
6. **Namespace message names by scope at compile time**, so a definition outside
   the scope cannot name the message.
7. **3 + 4 + a two-sided claim on the message name**, enforced at the two design-time
   doors where a definition and a subscription meet.

## Decision outcome

Chosen option: **7**, with **5** named as its successor and deliberately not built
yet.

### The configuration half

A `connector` gains the same three fields a project has (ADR-0071), so groups work
with no further thought:

```
connector {
  id, name, kind, endpoint, credentialsRef, enabled, createdAt,  // unchanged (ADR-0041)
  ownerId,                                  // User.ID (ADR-0044)
  visibility,                               // "private" | "shared"
  members [ { ref, role } ]                 // ref = { type: "user" | "group", id }
}
```

Enforced in the handlers off the principal, exactly where ADR-0071 enforces its
own: **viewer** to see a connector's configuration and its subscriptions,
**editor** to change them or to add a subscription, **owner** to delete it, to share
it, or to hand it on. An administrator sees and may act on all of them — that is
what admin means here, as it does in ADR-0071.

**Existence is not configuration**, and this record did not say so until the code
did. The modeler fills its connector picker from the same listing
(`api/web/editor.js`), so scoping the listing outright would leave every non-owner
authoring against an empty dropdown — a sharing rule that stops people doing their
work is not a sharing rule. So the listing has two shapes: at viewer or above, the
record; below it, a *catalog entry* — id, name, kind, enabled, and whether the
runtime could build its client. What ownership governs is the endpoint, the sender,
the credential reference, the member list and the inbound subscriptions. A separate
shape rather than the record with its fields blanked, because a blank endpoint is
indistinguishable from an unconfigured one and would tell an operator something
false about the connector instead of something true about their own access.

**The connector check must not borrow a credential.**
`POST /api/v1/connectors/test` resolves whatever credential reference its body
names and, given a recipient, sends real mail with it — so before this it was a
"send mail as anyone, with anyone's credential" endpoint for every account on the
installation. Locking the record while that stood would be theatre. The rule is
exact: a credential reference may be named only by somebody who may already edit a
connector that uses it; naming none resolves nothing and stays open. Found while
implementing this, not while writing it, and fixed here rather than filed, because
the rest of the measure depends on it.

An inbound subscription is governed by its connector's scope, the way an artifact
is governed by its project's (ADR-0071's inheritance rule, `api/artifactscope.go`).
It records its creator besides, because "who pointed this mailbox at that message"
is a question an audit will ask.

**A connector that predates this record becomes admin-only under `--auth`.** This
departs from ADR-0071, which let a legacy ownerless artifact stay open, and the
departure is the point: that record was adding a capability, this one is closing a
hole, and a security measure that exempts every installation that already has
connectors has closed nothing. The cost is real and belongs in the release note —
an installation where non-administrators managed connectors will find them
admin-managed until an administrator assigns each an owner.

### The delivery half

The configuration half alone does not answer the question. With it in place, Anna
still cannot subscribe to your mailbox — but she can still deploy a process whose
message-start event names `mail-received`, and your events will start it, because
the name is the whole key.

So: **an inbound subscription claims its message name.** While the claim stands,
that name is deliverable only to definitions inside the claiming subscription's
scope. The claim is enforced at the two design-time doors, because a check at one
moment is not a rule:

- **Claiming** (creating or enabling a subscription) is refused when a definition
  the claimant may not reach already catches that name.
- **Deploying** is refused when the definition catches a name claimed by a
  subscription the deployer may not reach.

Both are design-time checks about a runtime relationship, which is the shape
ADR-0163 already established for refusing to delete a referenced connector. Both
refusals name the message, never the other party — the point is to stop the
delivery, not to disclose whose mailbox exists.

### What this is and is not

It is a **gate at two doors**, not isolation. It stops the accident and the casual
grab, which is what the reported case is. It does not survive an administrator,
and it depends on the claim being checked at both doors — miss one and it is
decoration.

**Option 5 is the real isolation** and is the successor to write: the published
message carries the scope it was published into, a subscription carries the scope
it may be started from, and correlation compares them. Then nothing depends on a
name being claimed, and nothing depends on a check having run at the right moment.
It changes the message value and touches `applyToState`, so it is its own decision
with its own recovery story — and ADR-0071 already deferred runtime isolation once
for exactly this reason.

Option 1 is rejected because the measured behaviour is not what "operator config"
implies: an ordinary account can delete another's connector today. Option 2 is
rejected as an end state, though its effect is included: making everything
admin-only answers "keep others out" and refuses "let me have my own mailbox",
which is the request. Option 4 is rejected alone — a subscription owned inside a
connector anybody may delete is not owned. Option 6 is rejected because message
names are authored in the model and are how one process signals another; making
them scope-local would break process-to-process messaging to fix an inbound
problem.

### Consequences

- **Positive:** a person owns their mailbox, shares it in one action, and shares it
  with a group as easily as with one colleague — ADR-0180's shape, unchanged.
- **Positive:** the measured holes close with no engine change: no unauthorized
  delete, no reading of everybody's endpoints and message names, no subscribing to
  somebody else's connector.
- **Positive:** it is the right order. Whenever an inbound mail connector is built,
  it lands after this rather than before, so a personal mailbox is never — not even
  briefly — installation-wide.
- **Negative / trade-offs accepted:** message names become a namespace with claims.
  A deploy can now be refused for a reason that lies in configuration the deployer
  cannot see, and the refusal has to be legible without disclosing what it is
  protecting.
- **Negative:** legacy connectors change behaviour under `--auth` (above).
- **Negative:** it is a gate, and a gate is only as good as its two checks. The
  record says so out loud so that nobody reads it as isolation.
- **Follow-ups / risks to watch:** option 5 as the successor; and the claim check
  must run against *already deployed* definitions when a claim is made, not only
  against future deploys.

### As built, step one

The configuration half is implemented (`api/connectorscope.go`): the three fields,
the two-shape listing, the role checks on the connector and inbound-subscription
handlers, the credential-borrow rule, and `auth.connector_shared` /
`auth.connector_unshared`. Sharing is in the Console beside each connector — the
follow-up this section used to list, done in the same change, because ADR-0200
already taught what an API-only capability is worth. Transfer of ownership came
with it, unplanned: an ownerless connector is admin-only, so without it "Anna left"
would mean an administrator inherits every connector Anna ever made.

The delivery half — the claim on the message name — is **not** built. Until it is,
this measure protects a connector's configuration and not the events it brings in:
a process that names the right message still receives them. That is the gap the
record opened with, and it stays open until the second step lands.

## Pros and cons of the options

### 1 — do nothing
- Good: no new access model, no new refusals.
- Bad: an ordinary account can delete another's connector today, and read every
  endpoint and message name. That is not a documentation problem.

### 2 — admin-gate the connector surface
- Good: one line per handler; correct as far as it goes, and included in 7.
- Bad: answers "keep others out", refuses "let me own mine" — which is the request.

### 3 — owner + sharing scope on the connector
- Good: reuses the existing vocabulary; groups arrive free.
- Bad: protects the configuration and leaves the events deliverable by name.

### 4 — owner on the subscription only
- Good: smallest change; the subscription is where the event actually enters.
- Bad: an owned subscription inside a connector anybody may delete is not owned.

### 5 — isolation in the engine
- Good: the real answer; no claim to keep, no door to miss.
- Bad: changes the message value and touches `applyToState`, with a recovery story
  of its own. Its own record.

### 6 — namespace message names by scope
- Good: a definition outside the scope cannot name the message at all.
- Bad: message names are how one process signals another; scoping them breaks
  process-to-process messaging to fix an inbound problem.

### 7 — 3 + 4 + a two-sided claim (chosen)
- Good: answers the configuration and the delivery question with design-time
  checks only; one vocabulary; no engine change.
- Bad: a gate rather than isolation, and it introduces a claimed namespace.

## Links

- extends [ADR-0071](0071-sharing-scopes.md) past the line it drew, to a connector
  and the events it brings in
- reuses [ADR-0180](0180-groups-as-members.md): a group is one member, so
  "share it with my colleague" and "share it with the team" are one action
- the connector model it adds an owner to: [ADR-0041](0041-connector-management-and-secret-store.md)
- what made a connector an inbound path: [ADR-0075](0075-clio-inbound-event-bridge.md)
- the precedent for a design-time check about a runtime relationship:
  [ADR-0163](0163-deleting-a-referenced-connector.md)
- the enforcement point and the auth-off rule: [ADR-0044](0044-user-management-and-authentication-boundary.md),
  [ADR-0195](0195-auth-on-by-default.md)
