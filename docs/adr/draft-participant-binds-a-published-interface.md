# ADR-DRAFT: A participant names an interface; the server says which node

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0023 decided that a collaboration deploys one definition per executable pool and
that **message flows compile to nothing** — the arrow is documentation, correlation is
the runtime link. The compiler holds to that literally: `compiler/parse.go:1275` reads
a `<collaboration>` for its participants, `xmlParticipant`
(`compiler/parse.go:1278`) keeps `Id`, `Name`, `ProcessRef`, and `<messageFlow>` is
not parsed at all.

That is the right decision for pools **inside one model**, and this record does not
touch it. It leaves one thing unexpressible: a participant that stands for a *process
in another domain*, where the arrow has to say which entry point of that domain it
hits. There is nowhere in the model to put that, and nowhere for a deploy-time check
to look.

The obvious modelling — *a participant is another Atlas instance, and I pick the
process it runs* — has to be rejected, for a reason that has nothing to do with BPMN
purity:

- A participant is a **business party**. An Atlas node is **infrastructure**. Putting
  the node in the diagram means the business model changes when operations moves a
  domain from one server to another.
- Test and Production would need two diagrams, or one diagram that is wrong in one of
  them.
- It is the mistake ADR-0105 already fixed once for call activities, where resolution
  is per-server operator configuration and the model says only *what* it calls. And it
  is the principle ADR-0256 states outright: **the model is authored, the provider is
  configured.**

## Decision drivers

- **The model stays portable.** The same BPMN must deploy to Test and to Production
  and mean the same thing in both.
- **The operator keeps the controls ADR-0105 gave them** — pin a version, route to a
  stub, switch a target off during an incident — without anybody editing a model.
- **A drawn arrow that does nothing is a lie**, and today's message flow is one as
  soon as it crosses a domain. ADR-0023 itself lists validating message flows against
  their endpoints as a follow-up.
- **Fail at deploy, not at 3am.** A binding to an entry point that does not exist in
  the bound contract version is knowable before the model runs.
- **Extensions are visible as extensions.** An Atlas concept lives in the `atlas:`
  namespace with the disclosure ADR-0176 §1 and ADR-0269 require.

## Considered options

1. **The participant names the node** (`atlas:nodeId` or a target reference) and the
   modeller picks a process on it.
2. **The participant names a published interface** plus a version binding; the node is
   resolved per server from operator configuration.
3. **Neither — keep message flows decorative** and configure cross-domain routing
   entirely outside the model, in a mapping table.

## Decision outcome

Chosen option: **2 — a logical interface reference in the model, physical resolution
per server.**

### In the model

```xml
<bpmn:participant id="Participant_Kredit" name="Kreditprüfung"
                  atlas:interfaceRef="kreditpruefung"
                  atlas:binding="latest | version"
                  atlas:contractVersion="3" />
```

and on the arrow:

```xml
<bpmn:messageFlow id="Flow_1" sourceRef="Send_Antrag" targetRef="Participant_Kredit"
                  atlas:entryPoint="antragEingereicht" />
```

`atlas:entryPoint` names an entry point **in the published contract**
(ADR-draft-published-process-interface), never a foreign element id — so the
publisher may refactor behind it, which is the whole reason the contract exists.

A participant with no `atlas:interfaceRef` is an ordinary pool and behaves exactly as
ADR-0023 says: black box or local process, message flow decorative. **ADR-0023 is
narrowed, not superseded** — only a participant that carries an interface reference
compiles to anything.

### On the server

The `interfaceRef` resolves to a node through admin-owned, per-server configuration,
stored as its own design-time sidecar and reusing the ADR-0129 deployment target for
the address and the vault credential reference. This is the ADR-0105 pattern applied
one level out, and it inherits its three operator affordances: route this domain's
calls to a stub on the staging server, pin a contract version while a new one bakes,
and switch a peer off during an incident without editing a model.

An unresolved `interfaceRef` on a given server is reported the way a missing worker is
reported — a named, fixable gap in that server's configuration, not a broken model.

### What compiles

A send element (message throw, send task, message end) whose outgoing message flow
carries an `atlas:entryPoint` compiles to the peer-delivery job of
ADR-draft-peer-message-delivery-worker instead of the local `correlateMessage` path.
Everything else about the element is unchanged.

Incoming message flows from a remote participant stay **descriptive**: what makes this
pool reachable is its own published interface, not somebody else's arrow. Drawing the
inbound arrow is how the consumer documents the reply it expects; it grants nothing
and binds nothing.

### What is checked at deploy

- the referenced interface is known to this server (from the cached descriptor);
- the named entry point exists in the bound contract version;
- the payload the send element produces is compatible with what that entry point
  declares, to the extent the information model (ADR-0230) makes that checkable;
- the bound contract version is not deprecated (ADR-0130) — a warning, not a refusal.

These surface in the Problems panel with the versioned-validation machinery ADR-0026
already provides, so a modeller sees them while drawing rather than on deploy.

### Consequences

- **Positive:** the diagram is portable across environments and survives a server
  move; the message flow means something for the first time; the operator controls of
  ADR-0105 extend to cross-domain routing; a wrong entry point is a modelling error
  rather than a silent no-op.
- **Negative / trade-offs accepted:** `atlas:` extensions on a participant and a
  message flow make a cross-domain model less portable to other BPMN engines — the
  cost ADR-0176 §1 asks us to state rather than hide. Deploy-time validation now
  depends on a cached foreign descriptor, so a model can fail validation because a
  peer was unreachable when the cache went stale; the check must therefore say
  "unverified" rather than "invalid" in that case. And two servers can legitimately
  resolve the same model to different peers, which is the point and is also a new way
  to be confused about what production is doing.
- **Follow-ups / risks to watch:** whether `atlas:binding="latest"` is safe without
  the contract-compatibility check named as a follow-up in
  ADR-draft-published-process-interface — until that exists, pinning is the honest
  default and the modeler should say so. Whether the unit should be the process
  application (ADR-0128) rather than the process. And the layout question ADR-0023
  left open: generated pool layout still draws no message-flow edges.

## Pros and cons of the options

### Option 1 — the participant names the node
- Good: immediately concrete; the modeller sees exactly where it goes; no resolution
  layer to build or administer.
- Bad: infrastructure in a business model. Two diagrams for two environments, a model
  edit for every server move, and no operator control without a redeploy. It is the
  problem ADR-0105 and ADR-0256 were written to prevent.

### Option 2 — logical reference, per-server resolution (chosen)
- Good: portable model, operator keeps the controls, deploy-time checkability,
  publisher may refactor behind a stable entry-point name.
- Bad: an indirection to administer; `atlas:` extensions reduce portability to other
  engines; validation depends on a cached foreign descriptor.

### Option 3 — keep message flows decorative
- Good: nothing to decide, ADR-0023 stands untouched, and a REST worker against the
  peer's message endpoint already works today.
- Bad: two sources of truth — the drawing and the mapping table — and the drawing is
  the one people believe. It also forecloses the white box, which has nothing to
  attach to if the arrow carries no binding.

## Links

- narrows ADR-0023 (collaborations and pools) for participants that name an interface
- applies ADR-0256 (the model is authored, the provider is configured) and the
  per-server resolution pattern of ADR-0105
- binds ADR-draft-published-process-interface; compiles into
  ADR-draft-peer-message-delivery-worker; addressed per ADR-draft-cross-instance-message-addressing
- uses ADR-0129 deployment targets for the peer address and credential
- validated through ADR-0026 (problems panel and versioned validation)
- extension disclosure per ADR-0176 and ADR-0269; payload checking via ADR-0230
