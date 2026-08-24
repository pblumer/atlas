# ADR-0165: SOAP / Web Services (WSDL) connector

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas is being built out as the process- and approval layer in front of identity
management (the connectors initiative, #431), where a joiner/mover/leaver process must
read and provision accounts across many systems. The modern systems speak SCIM 2.0
(ADR-0153) or expose a REST API (ADR-0067); a large share of the *legacy* estate —
in-house AR/HR systems, older middleware, government back-ends — exposes only **SOAP /
Web Services described by a WSDL**. Without a first-class connector, a modeler has to
express a SOAP call by hand with the generic REST connector: assembling the SOAP
envelope as a string, setting the `text/xml` media type and the `SOAPAction` header,
and reading a raw HTTP body instead of the SOAP `Fault`. That is error-prone and leaks
the envelope mechanics into every process.

The question: how should a BPMN process invoke a SOAP operation — reading identities
(import) or provisioning accounts (outbound) — against a web service, without weakening
the engine's durability and single-writer invariants?

## Decision drivers

- **Reuse the established connector seam.** A connector kind must ride the same
  `TypeConnectorTask` → reserved `*JobTypeIndex` → in-process worker path every other
  connector uses (ADR-0007/0067), so it inherits durability, at-least-once delivery,
  and the non-blocking run loop for free.
- **Credentials never in the model.** Like REST/SCIM, authentication must be a
  *reference* to a server-side secret resolved at call time (ADR-0041), never a value in
  a BPMN file.
- **Generic, not WSDL-bound.** Legacy SOAP services are diverse and their WSDLs large;
  the connector should invoke an operation with a model-authored payload, not attempt a
  build-time WSDL binding or code generation. This mirrors MIM's generic WS connector,
  which the referenced gap analysis names as the counterpart.
- **SOAP semantics, not raw HTTP.** The connector should encode what makes SOAP SOAP —
  the envelope, the version-specific media type and action, and the `Fault` object — so a
  modeler authors an operation and its body, not an HTTP request.
- **One kind covers both checked capabilities.** Import (reading identities) and
  provisioning (creating/updating accounts) are both "invoke a SOAP operation and read
  the response"; a single operation-invoking connector serves both, distinguished only by
  which operation the model names.

## Considered options

1. **No dedicated connector** — model SOAP with the generic REST connector.
2. **A WSDL-binding connector** — parse the WSDL at deploy time and generate typed
   request/response bindings.
3. **A managed connector** (a server-registered SOAP endpoint, like clio/mail), with the
   endpoint and credentials configured server-side.
4. **A model-authored generic SOAP connector** — a new connector kind whose endpoint,
   operation, SOAPAction, body, and version live in the model, authenticating via a
   secret reference, mirroring the REST/SCIM connectors' posture.

## Decision outcome

Chosen option: **"a model-authored generic SOAP connector"** (option 4), because it
matches the REST/SCIM connectors' proven design exactly where SOAP is HTTP (a
model-authored endpoint plus a secret reference) and adds only the SOAP-specific
behavior — envelope wrapping, the version-appropriate `Content-Type`/`SOAPAction`, and
`Fault` handling — on top. It avoids the deploy-time complexity and fragility of a WSDL
binding (option 2), which buys little for legacy services whose WSDLs are often
incomplete or non-conformant, and it avoids the extra configuration surface of a managed
connector (option 3), which buys nothing here because a SOAP endpoint is naturally model
data. It removes the hand-assembly and foot-guns of option 1.

Concretely:

- A new reserved job type `io.atlas.soap` at `SoapJobTypeIndex == 18`, reserved by
  `NewBuilder` after the eighteen existing types, so one in-process worker serves every
  deployed process.
- `<atlas:soapConnector endpoint operation soapAction body soapVersion resultVariable
  authType authUsername authApiKeyName authSecret retries>` on a service task compiles to
  a `ConnectorTaskDetail`. `endpoint`/`soapAction`/`body` are literal-or-FEEL values (the
  ADR-0067 fx toggle) evaluated over the instance's variables at call time; `operation`
  is the operation name (used in diagnostics and as the default `SOAPAction`);
  `soapVersion` is `1.1` (default) or `1.2`.
- The worker wraps the authored `body` (the operation's request element, typically a FEEL
  expression that interpolates the instance's variables into the XML) in a SOAP envelope
  and POSTs it: SOAP 1.1 sends `text/xml` with a quoted `SOAPAction` header; SOAP 1.2
  sends `application/soap+xml` carrying the action as a `Content-Type` parameter. It
  parses the response envelope's `Body` into a generic structure (elements → maps,
  repeated elements → arrays, leaves → their text), which is written into the task's
  result variable, and turns a SOAP `Fault` (1.1 `faultcode`/`faultstring`, or 1.2
  `Code`/`Reason`) into the job-failure message so a parked incident is legible.
- Authentication (basic/bearer/apiKey) resolves a secret *reference* through the same
  `resolveConnectorSecret` the REST/SCIM connectors use (ADR-0041). The literal-or-FEEL
  and auth parsing is shared with REST/SCIM via `connectorValue`/`connectorAuth` rather
  than duplicated.
- The HTTP client is bounded by the shared connector call budget (ADR-0149), like REST.

**Relationship to ADR-0164.** ADR-0164 deprecates in-process service tasks and states
that new connector kinds are "worker-first" — but records the honest gap that a connector
task *cannot* run on a worker yet, because every connector handler resolves its
configuration from the compiled process through a local `ProcessLookup` an external
process does not have. Until the connector detail travels with the job, the in-process
seam is the only one available, and this connector deliberately follows the SCIM/LDAP
shape (ADR-0153/0154) so that when that gap closes, all three relocate together as
configuration rather than a rewrite. The connector's outbound call runs off the run loop
and after fsync, so a slow web service stalls only its own job, not the engine (ADR-0156
step 6).

### Consequences

- **Positive:** legacy SOAP integration is authorable in the Modeler as a first-class
  operation; it inherits durability, recovery, and at-least-once delivery; no new server
  dependency or configuration; both import and provisioning are covered by one kind; the
  `Fault` detail surfaces to operators.
- **Negative / trade-offs accepted:** the request body is authored XML (usually a
  FEEL-built string) rather than a typed binding — the connector does not validate it
  against a WSDL/XSD, so a malformed request is caught by the service, not at deploy time.
  The response decoder maps child elements and text but **not attributes** (legacy SOAP
  data is overwhelmingly element-carried); attribute mapping is a follow-up. There is no
  standard SOAP idempotency key, so at-least-once retries can re-invoke a non-idempotent
  operation — a provisioning operation should be idempotent on the service side or
  correlate on a business key in the body, as any retried connector call must (ADR-0007).
  The outbound call is bounded by the shared connector budget (ADR-0149).
- **Follow-ups / risks to watch:** optional WSDL import to pre-fill the operation and body
  skeleton in the Modeler; attribute and namespace mapping in the response decoder;
  WS-Security / signed-header support for services that require it; and, with ADR-0164's
  gap closed, relocating the kind to a worker.

## Pros and cons of the options

### Option 1 — generic REST connector
- Good: no new code.
- Bad: every process re-implements the envelope, media type, `SOAPAction`, and `Fault`
  handling; no shared validation; SOAP mechanics leak into the model.

### Option 2 — WSDL-binding connector
- Good: typed requests/responses; deploy-time validation against the contract.
- Bad: heavy deploy-time machinery for a legacy estate whose WSDLs are frequently
  incomplete or non-conformant; a large new dependency surface; brittle where it is most
  needed.

### Option 3 — managed SOAP connector
- Good: endpoint and credentials centralized server-side.
- Bad: extra configuration surface and a registry for what is naturally model data;
  inconsistent with REST/SCIM, the closest siblings; more moving parts for no gain here.

### Option 4 — model-authored generic SOAP connector (chosen)
- Good: mirrors the REST/SCIM connectors; SOAP semantics encoded once; secret-reference
  auth; no new dependency; one kind covers import and provisioning.
- Bad: the request body is authored XML rather than a typed binding, and the response
  decoder is element-only.

## Links

- part of the connectors initiative (#431); sibling of the SCIM (ADR-0153) and LDAP
  (ADR-0154) identity connectors
- relates to ADR-0067 (service-task connector catalog / REST connector) — the seam and the
  shared `connectorValue`/`connectorAuth` helpers
- relates to ADR-0041 (connector management and secret store) — the secret-reference auth
- relates to ADR-0007 (job protocol durability) and ADR-0149 (bounded connector call
  budget) — the SOAP HTTP client reuses the budget
- relates to ADR-0164 (no in-process service tasks) — this kind follows the in-process
  seam that record deprecates, per the gap it records, and is built to relocate with SCIM
  and LDAP once the connector detail travels with the job
