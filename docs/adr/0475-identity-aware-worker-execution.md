# ADR-0475: Identity-aware Worker execution and policy-based authorization

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** whether an *external* (not Atlas-supervised) Worker Instance may
  receive a brokered, short-lived credential from the authorization endpoint. This record
  builds brokering for supervised Worker Instances only, because handing a credential to a
  process in another network is the option [ADR-0168](0168-connector-work-on-a-worker.md)
  rejected; widening it needs an explicit amendment of that record, proposed below and not
  taken here.
- **Question checked:** 2026-10

## Context and problem statement

Atlas executes external side effects through Workers ([ADR-0203](0203-worker-execution-model.md)):
a BPMN task becomes a durable job, a Worker Instance leases it, and the Worker Type's code
calls SharePoint, Graph, Jira or a database. *Who* that call is made as, and *whether it
should be made at all*, is today decided by nothing but the credential the Worker happens to
hold.

Measured against `main` at 9099000 (verified by reading the code, not the documentation):

| Question | Today |
|---|---|
| Who started a process instance? | Not recorded. `model.ProcessInstanceValue` has no principal field. |
| Who completed a user task? | A process variable `completedBy` (`api/taskcompleter.go`) — overwritable by any later step or an operator's variable edit, so not evidence. |
| External identities an Atlas user has linked | None. A user carries one `(Source, ExternalID)` pair; OIDC login binds by `sub` alone, without the issuer (`api/oidclogin.go`). |
| Delegated (per-person) OAuth for Workers | None. Every Worker grant is client credentials (secret or certificate), a Google service-account assertion, or a refresh token an operator pasted into a vault bundle. |
| Refresh-token rotation | Not persisted: `connector/oauth2` reads only `access_token`; a rotated `refresh_token` is discarded. |
| Where a Worker's credential lives | In the Worker Instance's environment. Supervised children receive the whole vault bundle at spawn (`api/superviseenv.go`); external Workers configure their own ([ADR-0168](0168-connector-work-on-a-worker.md)). |
| What a worker credential may lease | Any job type except user tasks and in-process types. The `worker` field is a self-asserted label; no credential is bound to a Worker or a job type. The worker scope also reaches the operator doors of `complete` (manual, with a reason) and an unfenced `fail`. |
| Authorization of an external action | None. Route roles ([ADR-0209](0209-roles-per-endpoint-group.md)) and design-time sharing scopes ([ADR-0071](0071-sharing-scopes.md), [ADR-0278](0278-object-authorization.md)) gate the *API*; nothing gates the *call a Worker makes*. |
| Policy, PDP, obligation, approval binding | No such abstraction exists. "Policy" in the code is `accessPolicy` — a route class and a role. |
| Durable authorization evidence | None. The only durable fact before execution is `JobActivated` (holder, expiry, epoch). The resolved job payload is in no event (ADR-0179). |
| Login and security audit | slog lines and a 1,000-line in-memory ring; not durable, not tamper-evident. |

So a SharePoint Worker configured with an app registration that holds `Sites.ReadWrite.All`
deletes whatever document a model's FEEL expression names, as the application, for whoever
started the instance, and the only record of it is the job's completion. Nothing answers
*who asked*, *who approved*, *which rule allowed it*, or *as whom it ran*.

The question this record answers: **how does every external Worker action get an
identifiable executing identity, an explicitly authorized execution context, an enforceable
policy decision and durable, explainable evidence — without breaking Atlas's invariants,
without a second queue or state machine, and without claiming an enforcement boundary that
the architecture cannot actually hold?**

### Business motivation

The processes Atlas is built for — joiner/mover/leaver, access requests, document approval,
licence assignment — are exactly the ones where an auditor asks who did it and on whose
authority. Credential-bound automation answers "the service account did it". The motivation
is to answer with the person, the approver, the rule and its version, the identity at the
provider, and the outcome — from durable facts, after a restart.

### What competing tools already offer (verified 2026-10, and only that)

n8n Enterprise documents *end-user credentials*: an OAuth credential that resolves, at run
time, to the account of the person who triggers the workflow, prompting them to connect if
they have not ([docs](https://docs.n8n.io/administer/manage-credentials/end-user-credentials)).
**A per-execution executing identity is therefore not unique to Atlas**, and this record does
not claim it is. n8n also documents credential sharing within projects and external secret
stores. This record found no n8n documentation of per-operation policy evaluation with
versioned policies, approval bound to an action digest, or durable decision evidence in the
execution history; *not found* is not *absent*, and `docs/comparisons/n8n.md` carries only
that qualified statement.

## Decision drivers

- **The six invariants hold.** No allocation added to the processor batch cycle for jobs
  that are not governed; durable before visible (decision evidence is fsynced before a
  credential is released); single writer (decisions enter the log through the processor);
  one deterministic `applyToState`; compile policies at publication, not per request; the
  decision is a fact frozen into an event and never re-evaluated on replay.
- **Fail closed.** An error, a missing policy, an unknown subject or a missing attribute is
  never a Permit.
- **Trust only server-side facts.** Nothing a BPMN model, a process variable or a Worker
  Instance asserts is an authorization attribute.
- **Do not overclaim.** Where Atlas cannot prevent a compromised process from using a
  credential it holds, the record says so and narrows the claim to what is enforced.
- **Reuse.** Users, groups, roles, the vault, the OIDC token verifier, FEEL (`expr`) and
  temis (`dmn`) — no second FEEL or DMN engine, no XACML runtime.
- **Existing deployments keep working.** A Worker without an identity policy behaves exactly
  as today and is reported as not yet governed; nothing is silently widened or dropped.

## Considered options

For **where the enforcement point sits** in the job lifecycle:

1. **Combine with leasing** — evaluate policy in `POST /jobs/activate` and hand out only
   permitted jobs.
2. **A separate authorization step after the lease and before the external call** — the
   Worker Instance presents its lease; the server decides, records, and only then releases
   what the action needs.
3. **Verify at completion only** — record and check after the fact.
4. **Server-side execution** — Atlas itself makes the external call after a Permit.
5. **A credential-injecting egress gateway** — Worker Instances call the provider through a
   proxy that attaches the token and checks the request against the authorized action.

For **where decision evidence is stored**:

- **a.** A new engine value type, appended through the processor on the job's partition.
- **b.** A separate append-only, fsynced audit file beside the WAL.
- **c.** The existing slog audit stream.

For **the policy language**:

- **A.** A full XACML (or OPA/Rego, or Cedar) runtime.
- **B.** DMN only — every policy is a decision table.
- **C.** A small, compiled JSON policy model with structured matchers, bounded FEEL
  conditions through `expr`, and optional DMN rules through temis, behind non-overridable
  built-in guards.

For **where a delegated credential lives and who refreshes it**:

- **α.** In the Worker Instance's environment, as every Worker credential is today.
- **β.** In Atlas's vault; Atlas alone refreshes it and releases only a short-lived access
  token per authorized job.

## Decision outcome

Chosen: **enforcement option 2, evidence option a, policy option C, credential option β** —
with brokering restricted to Atlas-supervised Worker Instances until ADR-0168 is amended.

### 1. Problem statement and business motivation

See *Context*. The capability is named **Identity-aware Worker execution**: every governed
Worker action carries an explicit executing identity, an explicit identity policy, an
enforced decision and durable evidence.

### 2. Existing Atlas architecture this builds on

- Task → durable Job → Worker Type / Worker / Worker Instance ([ADR-0203](0203-worker-execution-model.md)).
- The engine resolves a connector task into plain values at lease time; the Worker Instance
  holds endpoint and credential ([ADR-0168](0168-connector-work-on-a-worker.md)); side
  effects run after fsync, never in the engine process ([ADR-0164](0164-no-in-process-service-tasks.md)).
- Leases are durable `JobActivated` events with a fencing epoch; reports are fenced by
  `holdsLease` ([ADR-0007](0007-job-worker-protocol.md), [ADR-0274](0274-in-process-job-leases.md)).
- Principals, roles, groups, live session revocation
  ([ADR-0044](0044-user-management-and-authentication-boundary.md), [ADR-0180](0180-groups-as-members.md),
  [ADR-0185](0185-live-group-membership.md), [ADR-0209](0209-roles-per-endpoint-group.md),
  [ADR-0281](0281-session-role-revocation.md)); federated login with a hand-written
  RS256 ID-token verifier ([ADR-0210](0210-federated-authentication.md)).
- Append-only audit value types in the engine log keyed under their instance —
  `VTVariableAudit`, `VTOperatorAction`, `VTActionOutcome` — and the argument of
  ADR-0312 for keeping access facts in the log rather than a sidecar.
- The vault (AES-256-GCM, one atomically written file per secret,
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)).
- FEEL compiled once through `expr` and DMN compiled once by temis through `dmn`
  ([ADR-0015](0015-reuse-feel-engine.md), [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)).

### 3. Identity and Connection models

Four concepts, kept apart:

| Concept | What it is | Where it lives |
|---|---|---|
| **Atlas Principal** | An authenticated user (`usr_…`) or service principal (`system:…`), by stable id | existing user store |
| **External Identity** | One account at a provider, bound by `(provider, issuer, tenant, subject)` | new store `external-identities` |
| **Connection** | An authorized association of an Atlas principal, a provider, an external identity and usable credentials | new store `connections`; the credential in the vault |
| **Execution Context** | Who and what one external action involves | frozen into the decision event |

**External Identity.** Bound by the validated issuer, tenant and stable subject — never by
e-mail. For Microsoft that subject is `oid` within `tid` (the identity platform's `sub` is
pairwise per application, so it does not identify a person across apps); for Google it is
`sub` under `https://accounts.google.com`. An Atlas user may link several, including several
accounts of one provider. An identity already linked to another Atlas user is refused, not
re-bound. Linking does **not** touch `User.Source`, `User.ExternalID` or the login federation
of ADR-0210 — a second account is a second account, not a new login.

**Connection.** Stable id `con_…`; provider; owner (Atlas principal id); external identity
id; authentication method; tenant; granted scopes (as the provider reported them); allowed
Workers (the configured Worker names that may use it — empty allows none); status
(`active`, `reauth_required`, `revoked`); timestamps for creation, last interactive
authentication, last refresh and revocation; who revoked and why. Public metadata and the
credential are separate records: the refresh token is a vault secret under the reserved
prefix `atlas:connection-credential:`, which the generic secrets API refuses to read, write
or list, as it already refuses personal-data keys.

**Execution Context.** The conceptual fields of the request map onto these stored
attributes of each decision:

| Conceptual | Stored as | Source |
|---|---|---|
| `initiatedBy` | `InitiatedBy` | `ProcessInstanceValue.StartedBy`, frozen at creation from the authenticated principal |
| `requestedBy` | `InitiatedBy` (same in this slice) | — a dedicated request step is a follow-up |
| `approvedBy` | `Detail.approvals[]` | approval facts in the log (§9) |
| `executedFor` | `ExecutedFor` | the Worker's identity policy (§11) |
| `executedAs` | `ExecutedAs` | the external identity key, or `service:<worker>` |
| `workerId` | `Worker` | the configured Worker the compiled task names |
| `workerInstanceId` | `WorkerInstance` | the lease holder (self-asserted label; see the threat model) |
| `connectionId` | `ConnectionID` | the Worker's identity policy |
| `processInstanceId` / `jobKey` | `ProcessInstanceKey` / `JobKey` | the leased job |
| `policyDecisionId` | the event's key | the processor's key generator |

Authorization is not liability: these fields record who was authorized and how; they do not
assign legal responsibility.

### 4. Authentication and delegation

**Delegated linking (Microsoft, built first).** Authorization Code with PKCE (S256), a
`state` bound to the signed-in Atlas session and kept server-side and single-use (the
login-CSRF that links an attacker's account to a victim's Atlas user is refused because the
callback's session must be the one that started the flow), a `nonce`, and full ID-token
validation reusing ADR-0210's verifier: RS256 signature against the tenant's JWKS, issuer
equal to `https://login.microsoftonline.com/{tid}/v2.0` for an allowed tenant, audience,
expiry, issued-at, nonce, and non-empty `tid` and `oid`. Scopes requested: `openid profile
offline_access` plus the resource scopes an administrator configured (for SharePoint,
delegated `Sites.ReadWrite.All` or, to narrow it, delegated `Sites.Selected`, which Microsoft
has supported since 2024-02 and which still requires a per-site grant). The browser does
the provider's login, so organizational consent, Conditional Access and MFA stay the
provider's and are not bypassed. No password is collected and no browser token is read.

**Google** follows the same adapter shape (Authorization Code + PKCE, `https://accounts.google.com`,
`sub`, optional `hd`); its implementation is a later slice.

**Apple.** Sign in with Apple yields an identity, not delegated access to a person's iCloud
data: Apple's own forum answer is that Sign in with Apple and CloudKit user identifiers are
not linked, and CloudKit's server-to-server access reaches only a container's public
database. Atlas therefore does not present Apple as a Worker delegation provider. (Assessment
from Apple's developer forum and CloudKit Web Services reference; to be re-checked before any
Apple work.)

**Existing methods stay**: client credentials with a secret or a certificate, Google service
accounts, the manual refresh-token bundles of mail, SharePoint and Google Sheets, and every
Worker-specific mechanism. **Rejected outright**: the resource-owner password grant.
**Device authorization grant**: considered for headless setup; not built, because the
browser flow already needs no local software and the device flow is the one most exposed to
consent phishing.

**Managed identities and workload identity federation**: a Worker whose host has a platform
identity (Azure Managed Identity, Kubernetes workload identity) holds no secret Atlas can
broker, so it is declared as identity mode `service` with no credential reference; Atlas
records the decision and the identity label, and enforcement is cooperative (§11).

**Refresh ownership.** Atlas is the only party that ever holds or uses a delegated refresh
token. Per connection, one in-process mutex serializes refresh; a cached access token is
reused until 60 s before expiry. When the provider returns a new refresh token, it is written
to the vault (atomic replace, fsync) *before* the new access token is released, and the old
one is not kept, so no superseded token survives on disk. Microsoft documents that it does
not revoke the old refresh token on use and that clients should discard it, so a crash
between the provider's answer and the vault write leaves the previous, still valid token in
place; for a provider with strict reuse detection the same crash would invalidate the
connection, which is then marked `reauth_required` on the next failed refresh. Atlas is a
single node today (Milestone 5 not started); a multi-node deployment needs a distributed
lease on refresh and is out of scope.

The workflow WAL is not a credential store: no token, secret or credential reference reaches
an event, a variable, a job payload, BPMN, browser storage or a log line.

### 5. PDP and PEP

**PDP**: a pure package, `authz`. `Evaluate(set, request) Decision` reads only its arguments:
no I/O, no clock (the evaluation instant is in the request), no global state. Effects:
`Indeterminate` (the zero value, so an unset decision is never a Permit), `Permit`, `Deny`,
`NotApplicable`.

**PIP**: the server assembles the request from server-side facts only (§8). **PAP**: the
policy store and its admin routes (§9 below, *Policy administration*). Atlas keeps XACML's
four-way separation as a code boundary — `authz` cannot reach a store, and the API cannot
decide — without adopting a XACML runtime: Atlas has one enforcement point and a few dozen
policies, not a federation of them, and a second expression language beside FEEL would be a
second thing to secure.

**PEP**: `POST /api/v1/jobs/{key}/authorize`, in the worker scope, presenting the lease:

```text
Worker Instance leases job (durable JobActivated, epoch n)
  -> POST /jobs/{key}/authorize {worker, leaseToken: n}
     on the run loop:  holdsLease? resolve the task into its material parameters,
                       read the instance's StartedBy, the job, the element
     off the loop:     resolve the Worker's identity policy and connection,
                       read principal, roles, groups, approvals (PIP);
                       Evaluate (PDP)
     on the run loop:  holdsLease still? append AuthorizationDecided
                       (+ on Deny: fail the job, retries 0 -> incident;
                          on approval required: ApprovalRequested + BPMN error)
                       RunUntilIdle -> fsync
     off the loop:     Permit and delegated: refresh/obtain access token
  <- {effect, reasonCode, decisionKey, policy, job: <authorized parameters>,
      credential?: {accessToken, expiresAt}}
Worker Instance performs exactly the returned parameters with the returned credential
  -> complete/fail (fenced as today)
```

Separation of the two authorizations is explicit: leasing needs the worker scope and the job
type; the action needs a decision for *this* job, *this* lease epoch and *these* parameters.

The decision covers the parameters Atlas resolved, and the response returns them; an
Atlas-shipped Worker executes the returned parameters, not the ones it received at lease
time, so the time between check and use is the Worker's own call and nothing a variable
change can widen.

Why not the other options. **1** conflates the two authorizations and would evaluate policy
for jobs a Worker leases and never runs. **3** detects, never prevents. **4** puts
integrations back into the engine process, which ADR-0164 removed. **5** is the strongest
technically — a gateway that injects the token can also check that the outgoing request is
the authorized one, so a compromised Worker Instance never holds a bearer credential — and is
recorded as the *high-assurance* follow-up; it is a network component of its own with its
own availability and TLS story, and the slice that proves the model does not need it.

### 6. Policy language and temis/FEEL reuse

A policy is a JSON document an administrator writes and publishes:

```json
{
  "id": "sharepoint-documents",
  "priority": 10,
  "target": {"workerTypes": ["sharepoint"], "operations": ["delete-file", "delete-item"]},
  "ruleCombining": "first-applicable",
  "rules": [
    {"id": "members-may-delete-with-manager-approval", "effect": "Permit",
     "when": {"subjectGroupsAny": ["grp_documents"],
              "resourcePrefixes": ["sharepoint:site=contoso.sharepoint.com:/sites/hr"]},
     "obligations": [{"id": "manager", "type": "approval", "approverGroup": "grp_managers",
                      "approvals": 1, "validForSeconds": 86400, "excludeRequester": true}]},
    {"id": "otherwise", "effect": "Deny", "reasonCode": "DELETE_NOT_ALLOWED"}
  ]
}
```

A rule matches when every matcher it states matches (roles, group ids, principal ids,
identity modes, Workers, operations, process definitions, resource prefixes) **and** its
optional FEEL `condition` is true. FEEL conditions are compiled at publication through `expr`
with five declared inputs (`subject`, `actor`, `action`, `resource`, `context`); a name
outside them is a compile error, and a call of `now()` or `today()` is refused because FEEL
reads the wall clock and a policy must be a function of its input (the instant is
`context.time`). A rule may instead delegate to a **DMN decision** embedded in the policy,
compiled once by temis at publication; its outputs `effect` (`"Permit"`, `"Deny"`,
`"NotApplicable"`) and optional `reasonCode` are its answer, and anything else — an error, a
null, an unknown string, a missing output — is `Indeterminate`. DMN suits the business half
("which role may do which operation on which class of resource") and is what a business
analyst can read; it does not replace the built-in guards and can never override them.

Nothing is parsed or compiled on a request: publication compiles the whole set into an
immutable value swapped in atomically.

### 7. Policy combination and fail-closed behavior

1. **Built-in guards** run first and cannot be overridden by any policy: a delegated mode
   with no executing subject (`NO_EXECUTING_SUBJECT`), an unknown or disabled subject
   (`SUBJECT_UNKNOWN`, `SUBJECT_DISABLED`), no connection or one that is revoked, needs
   re-authentication, belongs to someone else, or does not allow this Worker
   (`NO_CONNECTION`, `CONNECTION_REVOKED`, `CONNECTION_REAUTH_REQUIRED`,
   `CONNECTION_NOT_OWNED`, `CONNECTION_NOT_ALLOWED_FOR_WORKER`), a missing operation
   (`MISSING_ATTRIBUTE` → Indeterminate), or a policy set that failed to load
   (`POLICY_SET_UNAVAILABLE` → Indeterminate).
2. **Applicable policies** are those whose target matches, ordered by `(priority, id)`.
   Within a policy, rules combine by the policy's declared algorithm, `first-applicable` or
   `deny-overrides`; an unknown algorithm is refused at publication, never defaulted.
3. **Across policies, deny-overrides, fixed**: any Deny → Deny; else any Indeterminate →
   Indeterminate; else any Permit → Permit; else NotApplicable.
4. **Obligations** of every Permit that contributed must all be satisfied (§9); an
   unsatisfied one turns the Permit into a Deny carrying the obligation's reason code.
5. **The PEP proceeds on Permit only.** NotApplicable is a Deny (`NO_APPLICABLE_POLICY`) —
   default deny — and Indeterminate is a Deny (`POLICY_INDETERMINATE`).

The deciding policy and version are the first Deny under deny-overrides, else the first
Permit; the trace lists every policy and rule that was asked and what each said.

### 8. Trusted attributes

| Attribute | Source of truth | Never from |
|---|---|---|
| Subject id | the Worker's identity policy + `StartedBy` frozen in the instance's creation event | variables, the worker |
| Subject roles, groups, enabled | user and group stores, read at decision time (current state is authoritative) | session snapshots, claims |
| Connection, external identity | connection and identity stores | the job payload |
| Worker, Worker Type, operation | the compiled process (the task's static Worker name) | variables, the worker |
| Resource, material parameters | Atlas's own resolution of the task over its scope chain | the worker's report |
| Approvals | approval facts in the log, approvers' current groups | variables, task forms |
| Time | the server clock, frozen into the request and the event | the worker |
| Worker Instance | the lease holder label | — it is self-asserted; recorded, never trusted for a decision |

Values a model computes (a site from a variable) are authoritative for *what will be done*
— that is precisely what is being authorized — and never for *who may do it*. A process
variable named `approved` changes nothing.

**Resource spellings.** SharePoint addresses one site by URL, `host:/path` or Graph id, and
Atlas cannot canonicalize them without a call to Graph. A *permit-list* over one spelling
only ever refuses the others (safe); a *deny-list* over one spelling can be walked around by
another. Publication therefore warns on a Deny rule that names resources, and the handbook
says to write resource policies as permit-lists.

### 9. Obligations and approvals

An obligation is a condition a Permit carries that the server verifies from its own facts:

| Type | Satisfied when | Unsatisfied reason |
|---|---|---|
| `approval` | `approvals` distinct principals in `approverGroup` (current membership) granted an approval of this exact action digest, none of them the executing subject or (with `excludeRequester`) the initiator, each within `validForSeconds` and not revoked | `APPROVAL_REQUIRED` (or `APPROVAL_REJECTED` when an eligible approver refused) |
| `fresh-authentication` | the connection's last interactive authentication is within `maxAgeSeconds` | `REAUTHENTICATION_REQUIRED` |

**The action digest** is SHA-256 over a canonical JSON of Worker Type, Worker, operation,
resource and every material parameter Atlas resolved (an upload's content is hashed into it,
not copied). An approval is of a digest, so approving "delete draft.docx" cannot be replayed
for "delete contract.docx": the second request has another digest and finds no approval.

**The bridge to BPMN.** The PDP never waits. On `APPROVAL_REQUIRED` the PEP, in the same
batch as the decision, appends an `ApprovalRequested` fact and throws the BPMN error
`ATLAS_APPROVAL_REQUIRED` on the job. A model that wants approval catches it with a boundary
error event, routes to a user task, and loops back to the service task; an uncaught error is
an incident, which is the correct answer for a model that never planned for approval. The
approver decides through `POST /api/v1/instances/{key}/action-approvals/{approval}/decide`
(the Console's *Approvals* page posts there), which checks group
membership and separation of duties on the server and appends `ApprovalDecided`; completing
the user task only moves the process. When the loop reaches the service task again, a new
job, a new lease and a new decision find the approval for the same digest. The policy never
injects a command, completes a task or writes a variable.

### 10. Separation of duties

Principals are compared by stable id, never by name or e-mail. An approval obligation
excludes the executing subject always and the initiator when `excludeRequester` is set; N
approvals must come from N distinct principals; a service principal can never be an
approver (approvals are decided only through a person's session). Group membership is
**current** at both moments — when the approval is decided and again when the action is
authorized — so an approver removed from the group in between no longer counts. Historical
membership is what the evidence records, not what decides.

### 11. Worker isolation and enforcement boundaries

A configured Worker gains an optional **identity policy**:

| Mode | Executes as | Executed for | Credential |
|---|---|---|---|
| *(absent)* — legacy | whatever the Worker Instance holds | unknown | Worker Instance environment |
| `service` | the Worker's service identity (`service:<worker>`) | the initiator, when known | Worker Instance environment (cooperative) |
| `delegated-fixed` | one connection's external identity | the connection's owner | brokered by Atlas |
| `delegated-per-job` | the executing subject's own connection | `actAs: "initiator"` — the principal who started the instance | brokered by Atlas |

and an **enforcement** setting: `shadow` (decide and record, label it, never block — not
offered for delegated modes, which are always enforced because a credential is only released
on a Permit) or `enforce`.

What each mode actually guarantees — the claim, stated narrowly:

- **Delegated modes, supervised Worker Instance: enforced at the credential.** Atlas holds
  the refresh token; the Worker Instance never sees it; an access token is released only
  after a durably recorded Permit for this job, this lease and these parameters. A
  compromised Worker Instance can still use a released access token, until it expires, for
  anything that token's scopes allow at the provider — Atlas cannot narrow a bearer token
  after issue. Narrow consented scopes (delegated `Sites.Selected`) bound that exposure;
  the egress gateway of §5 would remove it.
- **Service mode and legacy Workers: cooperative.** The Worker Instance holds its own
  credential, so a compromised or third-party Worker can act without asking. What Atlas
  enforces is the *process*: a Deny fails the job on the server, and a completion of a
  governed job without a Permit for its current lease is refused, so the process does not
  proceed on an unauthorized action and the attempt is visible. That is detection plus
  process integrity, not prevention, and the product says so.
- **External Worker Instances** (any process not supervised by Atlas) are refused brokered
  credentials (open question above). They may still use service mode.

The trust boundary in one sentence: **a Permit returned by an API cannot make untrusted
Worker code safe; only withholding the credential can, and Atlas withholds it only where it
holds it.**

Bypass paths and their answers:

| Path | Answer |
|---|---|
| External Worker on the HTTP job protocol | delegated credential never released to it; service-mode completion without a Permit refused |
| Worker retries | every lease needs its own decision; a Permit is bound to the lease epoch |
| A process variable selecting another connection or identity | identity and connection come from the Worker's identity policy, never from variables |
| MCP | the authorize route is omitted from MCP; agents cannot decide or broker |
| Process version change / migration | the Worker name comes from the compiled task the job sits on, read at decision time |
| Dynamic Worker names (Entra, Exchange `connector="=…"`) | out of scope for this slice; the identity policy is refused on those Worker Types until the resolved name is part of the decision input |
| Supervised Worker restarts | a restart loses only in-memory state; decisions and approvals are in the log |
| Calling an existing handler directly | in-process handlers are off by default (ADR-0233); a governed SharePoint Worker gets no in-engine client at all, so `--in-process-connectors` parks its tasks instead of running them without a decision (as built; the first draft said the in-process path would call the authorization, which it did not) |
| A Worker editor changing a governed Worker | refused: a governed Worker is changed or deleted only by an administrator, because its endpoint decides where a delegated token goes and deleting it would let a new Worker inherit the consent given its name |
| A delegated Worker's endpoint pointed at another host | refused where it is set, and again before a token is released: a delegated Worker's endpoint is Microsoft Graph or blank |
| Another supervised child presenting the shared internal token | refused: a delegated token is released only with the per-start broker key, which only the process serving the delegated Worker Type is handed |
| An application the person granted access to their account (OAuth) | it may not answer approvals or consent to Workers: both need the person's own session |
| The operator doors of `complete`/`fail` in the worker scope | pre-existing; recorded as a follow-up (narrow the worker scope to fenced reports) |
| A modeler deploying BPMN | policies are admin-owned; a model names a Worker, not an identity; parameters are what is authorized |
| Self-asserted worker label | recorded as `WorkerInstance`, never used to decide |

Network egress restriction, per-Worker-Instance credentials (ADR-0297's follow-up) and
attestation of Worker binaries are named follow-ups; none is claimed.

### 12. Durable decisions and audit

Two new value types, appended last so every earlier value keeps its number:

- **`VTAuthorization` / `AuthorizationDecided`**: process instance, element instance, job,
  lease epoch, effect, enforced or shadow, reason code, deciding policy id and version,
  Worker, Worker Instance label, Worker Type, operation, resource, action digest, initiated
  by, executed for, executed as, connection, identity mode, and a bounded JSON detail
  (obligations, approvals used, trace). Keyed under its instance, append-only.
- **`VTActionApproval`** with `ApprovalRequested`, `ApprovalDecided`, `ApprovalRevoked`:
  the digest, the obligation it answers, approver group and count, requester, expiry, and
  each decision with approver id, time, outcome and reason.

And one appended field: **`ProcessInstanceValue.StartedBy`**, the authenticated principal id
that created the instance through the API, frozen into the activation event.

Every value is decided on the command path and frozen into the event; `applyToState` only
stores it. Replay reproduces the same records without evaluating a policy, and a decision
already in the log is never recomputed. Evidence contains principal ids, not names or
e-mail addresses, and never a token, secret or credential reference; it is retained and
purged with its instance (the existing history retention, ADR-0115/0146), exported with the
rest of the log, and readable at `GET /api/v1/instances/{key}/authorizations`.

**Outcome.** The job's completion or failure is the execution outcome, linked by job key;
SharePoint already sends the job key as the request id, which is the external correlation
identifier. Atlas does not claim exactly-once execution: a crash after the provider acted
and before the completion was recorded leads to a new lease, a new decision and possibly a
second call. Idempotency remains a property of each Worker Type (ADR-0141 records that Graph
list-item creation is not idempotent).

### 13. Temporal authorization and revocation

| Moment | What is evaluated |
|---|---|
| A Worker's identity policy or a connection's allowed Workers is set | validated against the stores (the Worker exists, the connection is active and owned) |
| Before every external action | a full decision under the current published policy set and current attributes |
| A retry, a re-lease, a resumed approval | a new decision; nothing is inherited from an earlier Permit |
| Recovery | nothing is re-evaluated; the log's decisions are history |

The policy version used is the one published at the decision instant and is recorded.
Roles, groups, account state, connection state and approvals are read current at each
decision. A revoked connection fails the next decision and its cached access token is
dropped; an access token already released stays valid at the provider until it expires
(Graph tokens are typically valid for about an hour; Atlas does not shorten them), and an
operation already in flight at the provider cannot be recalled. An old Permit is never a
capability: it is bound to a lease epoch that the next lease moves past.

A release and a revocation that happen at the same moment are ordered by when the release
read the connection's state: a release that read *active* just before the revocation was
committed may still complete, exactly as an access token released a minute earlier stays
valid. Every release that starts after the revocation is refused. (A refresh that is in
flight when the revocation lands is refused too: its rotated refresh token is not written
back and nothing is released.)

### 14. Recovery and retries

| Failure | State after restart | What happens |
|---|---|---|
| Before the decision is fsynced | no evidence, no credential released | the Worker's call fails; the lease expires; a new lease decides again |
| After the decision, before the response | a recorded Permit, no execution | lease expiry; a new decision; the evidence shows a Permit with no completion for that epoch |
| After the external call, before completion | a Permit, a provider-side effect, no completion | a new lease and decision; the action may run twice (at-least-once) |
| A Deny | decision and incident in one batch | resolving the incident retries the job, which decides again |
| Policy changed between attempts | — | the next attempt is decided under the new version |

### 15. Threat model

Assets: delegated refresh tokens, access tokens in flight, the integrity of decisions and
approvals, the policy set. Actors: a modeler who can deploy BPMN; an operator who can edit
variables; a compromised Worker Instance; a compromised supervised child (e.g. a script
interpreter); a user trying to self-approve; an attacker able to start a linking flow.

| Threat | Mitigation | Residual |
|---|---|---|
| Model selects a dangerous target | the resolved target is the authorized input; permit-lists | deny-lists by name are bypassable (warned) |
| Variable forges approval or identity | neither is read from variables | — |
| Self-approval, collusion of one | stable-id SoD, N distinct approvers | collusion of N approvers |
| Approve-then-swap | approval bound to the action digest | — |
| Stolen worker token leases governed jobs | delegated credential only after a recorded Permit, only to supervised instances | released access token usable until expiry |
| Compromised Worker with its own credential | cooperative mode is labeled; completion without Permit refused | the side effect itself is not prevented |
| Linking CSRF / account mix-up | state bound to session, single-use, nonce, issuer+tenant+oid binding, refusal of re-binding | — |
| Token leakage through logs, events, payloads | never written there; reserved vault prefix | process memory of the server |
| Replay of an old Permit | lease-epoch binding | — |
| Policy evaluation error | Indeterminate → Deny | availability: a broken policy blocks governed work (by design) |
| Admin abuse | admins administer policy; their changes are recorded with who and when | an admin is trusted |

### 16. Performance trade-offs

- **Engine**: one extra event per authorized job, appended through the processor in the
  batch the PEP submits; jobs of ungoverned Workers pay nothing. The two value types ride
  on the processor's `inflightValue` behind pointers rather than inline: inline, a dozen
  strings each would widen every token movement's copy. The hot path pays sixteen bytes and
  no allocation; the allocation falls on the governed action, which has just made a
  network round trip (as built; the first draft of this section assumed inline values).
- **PDP**: off the run loop, on the request goroutine; an immutable compiled set; no network
  call; bounded inputs (at most 256 groups and roles, 64 approvals, 4 KiB detail). FEEL and
  temis evaluation has no step budget in Atlas ([expr](../../expr/expr.go) passes none), so
  bounded time rests on bounded inputs and on refusing recursion-free but unbounded inputs at
  publication; a DMN evaluation also gets the request's context deadline. Measured numbers are
  in *As built*.
- **Brokering**: one provider round trip per access-token lifetime per connection, not per
  job.

### 17. Compatibility and migration

- A Worker without an identity policy is **legacy**: unchanged behavior, no decision, and
  listed by `GET /api/v1/security/readiness` as not governed, with the credential method it
  uses.
- An operator moves a Worker to `service` + `shadow` to see what policies *would* decide
  (recorded and labeled shadow — never described as enforcement), then to `enforce`.
- Delegated modes are new; they start enforced.
- Persistent jobs and deployed models need no change: the identity policy is Worker
  configuration, and the decision is a step the Worker takes.
- Protocol: an Atlas worker built before this record never calls `authorize`. In `enforce`
  mode its completion is refused, which surfaces the version gap instead of hiding it: the
  409 and its audit entry name it. (As built, the readiness report does not know a Worker
  Instance's version, so it cannot name the gap in advance.) No deadline is imposed by this record; the roadmap carries the
  milestones (legacy reported → shadow → enforce for delegated Workers by default).
- Existing on-disk records decode unchanged: the new fields and value types are appended.

### 18. Rejected alternatives

- XACML, OPA/Rego or Cedar as the runtime: a second expression language and runtime beside
  FEEL and temis, for a policy count that does not need it.
- DMN only: a decision table cannot express the non-overridable guards or obligations, and
  "every rule is a table" makes the simple ones harder to read.
- Evidence in a sidecar file (b): a second durable log beside the WAL with its own crash
  semantics, outside replay and outside retention — the argument ADR-0312 already made for
  entitlements. Evidence in slog (c): not durable.
- Delegated refresh tokens in Worker environments (α): refresh ownership spread across
  replicas, rotation lost, per-job enforcement impossible.
- Server-side execution (4) and a lease-time decision (1): see §5.

### 19. Required changes to existing ADRs

- **ADR-0168** — proposed amendment: *for delegated connections, Atlas holds the refresh
  token and releases a short-lived access token to an Atlas-supervised Worker Instance
  through the authorization endpoint, after a recorded Permit.* This is narrower than the
  option ADR-0168 rejected (no long-lived secret crosses, and only to the controlled spawn
  path); extending it to external Worker Instances is the open question of this record.
- **ADR-0172** (and ADR-0462's dynamic `connector="=…"`) — an identity policy is refused on a
  Worker of a type whose name a model can compute, until the resolved name is part of the
  decision input.
- **ADR-0297** — follow-up made concrete: narrow the worker scope to fenced reports; the
  shared internal token remains the brokering principal until per-Worker-Instance
  credentials exist.
- **ADR-0210** — unchanged; noted that login federation binds `sub` without the issuer, which
  linked identities deliberately do not copy.
- **ADR-0041 / ADR-0069** — the vault gains a reserved prefix for connection credentials.

### 20. Roadmap placement and milestones

The capability belongs to **Milestone S — Single-binary server** (user management and the
authentication boundary is listed there as 🚧) with the Worker protocol work of
**Milestone 4 — Operability**. Slices:

| Phase | Content |
|---|---|
| 0 | this record |
| 1 | external identities, connections, ownership, revocation, Worker bindings |
| 2 | `authz`: versioned policies, deterministic PDP, trusted inputs, default deny |
| 3 | the PEP route, evidence events, Deny → incident, completion guard |
| 4 | Microsoft linking, brokered tokens, the SharePoint Worker using them |
| 5 | DMN/FEEL policy rules, approval obligation, separation of duties, the BPMN bridge |
| 6 | Console: identities and connections, policies, simulator, evidence in Operations |
| 7 | further Worker Types per the matrix below, after the SharePoint slice is complete |

**Worker Type compatibility matrix** (what each could support; nothing beyond SharePoint is
built by this record):

| Worker Type | External authentication today | Delegated per-person? | Notes |
|---|---|---|---|
| SharePoint | client credentials (secret, certificate), refresh-token bundle | yes — Graph delegated `Sites.*` | first slice; delegated `Sites.Selected` narrows reach |
| Microsoft Mail | client credentials, refresh token, Google service account | yes — `Mail.Send` delegated sends as the user | application `Mail.Send` sends as any mailbox; delegated is the accountable choice |
| Entra ID | client credentials only (deliberately, ADR-0172) | partly — many directory writes need admin roles the user must hold | dynamic Worker names must be resolved first (§19) |
| Teams Phone | certificate, PowerShell | no — `Connect-MicrosoftTeams` with app-only certificate | administrative roles; service mode only |
| Exchange Online | client credentials (secret, certificate); on-prem basic/Kerberos | unclear per cmdlet — to be investigated | service mode first |
| Google Sheets | service account, refresh token | yes — OAuth per user | adapter after Microsoft |
| Jira | API token, PAT | possible — Atlassian OAuth 2.0 (3LO) | needs an Atlassian adapter |
| Generic REST | basic, bearer, API key, client-credentials | provider-neutral OAuth possible | resource policies by URL prefix |
| SQL | database login in the DSN | no — database identity | service mode; database grants are the provider authorization |
| S3 | access key | temporary credentials (STS) possible | service mode |
| Script | none (sandbox) | no | arbitrary code: never receives a brokered credential |

Four layers stay distinct in every row: authenticating to the provider, Atlas allowing a
Worker's use, the provider allowing the resource, and the business policy allowing the
action.

### As built, first slice

Phases 1 to 6 of §20 landed together for the SharePoint Worker Type, with the departures and
gaps listed below. The status stays *Proposed*: the open question above, and the ADR-0168
amendment of §19, are not decided by having built the supervised half.

**What exists**

- **Identities and connections** (`api/identity`). An external identity is keyed by provider,
  tenant and the provider's immutable subject (Microsoft's `oid`), never by e-mail. A new
  connection allows no Worker; only its owner changes that, and only to configured Workers.
  Its owner or an administrator revokes it, which deletes the vault credential for good. A
  provider's `invalid_grant` marks it `reauth_required`. The refresh token lives in the vault
  under the reserved prefix `atlas:connection-credential:`. The secret routes, API-token
  sealing and Worker secret resolution refuse that prefix, and vault listings hide it. The
  broker serializes each connection's refreshes and persists a rotated refresh token before
  it releases the access token. A revocation that lands during a refresh wins.
- **The decision point** (`authz`). It is pure: no clock, no I/O. The built-in guards run
  before any policy (§7), then each policy's rule combining, then deny-overrides across
  policies. Indeterminate and NotApplicable refuse. Two obligations exist, approval and fresh
  authentication. Conditions are FEEL, and `now()`/`today()` are refused at compile. A rule
  may delegate to a DMN decision through `dmn.CompileStandalone`. The action digest is
  SHA-256 over canonical JSON. Published versions are immutable (`api/policy`), and a set
  that will not build leaves every governed action refused, not the server down.
- **Evidence** (`model`, `state`, `engine`). `VTAuthorization` (`AuthorizationDecided`) and
  `VTActionApproval` (`ApprovalRecorded`) are engine facts, fenced on the job's lease holder
  and epoch. `ProcessInstanceValue.StartedBy` records who started an instance. A refusal's
  consequence is appended in the decision's own batch: an incident
  (`IncidentAuthorizationDenied`), or the BPMN error `ATLAS_APPROVAL_REQUIRED` with an
  approval request bound to the digest. Recovery folds the facts and evaluates nothing.
  Purging an instance's history deletes both column families.
- **The enforcement point** (`api/authorize.go`). `POST /api/v1/jobs/{key}/authorize` decides
  for the lease the Worker holds. A governed Worker's completion without a recorded Permit
  for its lease is refused (409). A delegated Permit releases an access token, only to a
  Worker Instance Atlas supervises, with `Cache-Control: no-store`. The shipped SharePoint
  Worker asks before every action (`worker/authorize.go`). In delegated mode it receives no
  credential at start (`ATLAS_SHAREPOINT_<NAME>_BROKERED=1`) and calls Graph only with the
  brokered token.
- **Approvals** (`api/actionapprovals.go`). A person answers through
  `POST /api/v1/instances/{key}/action-approvals/{approval}/decide`. Only a person (`usr_`)
  may answer, never the one the action runs for. The approver must be in the approver group
  at the moment of answering, and again at the next decision, which reads current membership.
  An approval counts only for the same digest and only until it expires.
- **Linking** (`api/linkmicrosoft.go`). Authorization code with PKCE and `prompt=select_account`,
  bound to the Atlas session that started it. The ID token's issuer, audience, expiry, `tid`
  and `oid` are checked with the ADR-0210 verifier. No password is collected and there is no
  ROPC.
- **Console.** *Connected accounts* (account menu) and *Administration → Security*: readiness,
  identity policy per Worker, policy drafts and publication, the simulator, and an instance's
  decisions and approvals by key. The MCP tool `atlas_instance_authorizations` reads the same
  evidence. *Approvals* (account menu, and a dashboard tile when some wait) is where a person
  answers: `GET /api/v1/me/action-approvals` lists the open requests they may answer and those
  asked on their behalf, across instances, read through an index of open requests by expiry
  (column family `0x35`, written with the request fact and purged with the instance). It is a
  page of at most 200, soonest-expiring first, marked truncated when more are open.
- **Storage.** Evidence is under column family `0x34` and approval facts under `0x33`. Evidence
  was first given `0x32`, which the event log already used. The two did not meet only because,
  in partition 1, an instance key's leading bytes differ from the event log's sub-prefixes;
  in partition 0, or one of 256 and above, a range read of one would have reached the other.
  `state/columnfamily_test.go` now holds every family to a value of its own.
- **Backup.** The `identity` store is classified as a credential and `policies` as identity
  (`api/storeregistry.go`).

**Hardened after an independent review** (each change with a test that fails without
it): governed Workers are changed only by administrators; a delegated Worker's endpoint
is bound to Microsoft Graph; delegated tokens need a per-start broker key held only by
the supervised SharePoint process; governed Workers never run in-process; setting or
clearing an identity policy restarts the supervised Worker; approving and consenting need
the person's own session; the approval fold reads only answers for the action's digest,
keeps one place per approver and never truncates (an oversized set is refused); a Worker
may not reference a reserved vault name; resource values escape `;`, `=` and `%`, and a
resource prefix that ends inside a value is warned about at publication. The review also
named a window in which a cached access token can be released concurrently with a
revocation; it is the ordinary check-then-act window the refresh path has too, a later
release is always refused, and serializing the two would undo the stronger guarantee that
a revocation during a refresh wins, so it is accepted and stated in §13.

**Departures from the decision**

- The evidence view is on the Security page, looked up by instance key, not in the
  Operations instance view (§20, phase 6).
- Shadow is refused for the delegated modes. A delegated credential is released only on a
  Permit, so shadow would either release a person's credential on a refusal or not be
  shadow.

**Not built**

- Google (designed in §4, no adapter); Apple (investigated in §4 only).
- Brokering to external Worker Instances (the open question), the egress gateway,
  per-Worker-Instance credentials.
- Worker Types other than SharePoint, and Workers whose name a model computes (§19).
- A request step distinct from instance start: the per-job subject is the instance's
  starter.
- A durable login audit, per-user recent activity, and refresh ownership across nodes.

**Evidence that it holds**

- `authz`: `go test ./authz` (99 % statement coverage), including the fail-closed paths.
- Engine: `engine/authorization_test.go` covers lease fencing, consequences in the same batch,
  and replay without evaluation.
- Routes: `api/authorize_internal_test.go` runs the full approval loop and its refusals.
- End to end: `api/authorize_e2e_internal_test.go` runs the shipped Worker against a fake
  Graph. A refused action makes no Graph call.
- Linking: `api/linkmicrosoft_internal_test.go` runs against a fake tenant and covers every
  refusal of §4.
- Worked example: `api/identitysample_internal_test.go` holds the documentation's model and
  policies.
- Console: `e2e/console-identity.spec.mjs`, `e2e/console-approvals.spec.mjs`;
  `api/myapprovals_internal_test.go` for the worklist route.

**Measured** with `go test -bench . -benchmem ./authz` on a 4-core development container, one
decision per operation. These are single runs, taken while other tests were running, so
read them as orders of magnitude; repeated runs varied by up to about a quarter.

| Policy | Time | Allocations |
|---|---|---|
| matchers only | 0.41 µs | 2 |
| with an approval obligation | 0.79 µs | 4 |
| with a FEEL condition | 7.9 µs | 81 |
| with a DMN rule | 30 µs | 204 |

All of this runs on the HTTP goroutine of the authorize route, outside the run loop. The
engine only appends the decided event, so invariant I1 is untouched.

### Consequences

- **Positive:** the questions *who requested, who approved, which policy and version, as whom,
  which Worker Instance, which resource, what happened* have answers in durable facts, for
  governed Workers, after a restart.
- **Positive:** delegated execution exists without a refresh token ever leaving Atlas, and a
  revoked connection stops the next action.
- **Positive:** default deny, deny-overrides and Indeterminate-as-Deny make a mistake in a
  policy a refused action, not a silent Permit.
- **Negative:** a governed action costs a round trip and an fsync before it starts.
- **Negative:** a broken or missing policy stops governed work; that is the point of fail
  closed and an operational risk to plan for (the simulator exists to find it first).
- **Negative:** service mode is cooperative; only delegated modes on supervised instances are
  enforced at the credential. The product must not blur the two.
- **Follow-ups:** egress gateway; per-Worker-Instance credentials; narrowing the worker
  scope; Google and further providers; dynamic Worker names; a request step distinct from
  instance start; durable login audit; multi-node refresh ownership.

## Pros and cons of the options

### Enforcement 1 — at leasing
- Good: an unauthorized job is never handed out.
- Bad: conflates lease and action; batch pulls evaluate work never run; a lease outlives
  policy changes.

### Enforcement 2 — separate authorize step (chosen)
- Good: one decision per action and lease; durable before the credential; works for
  supervised and external Workers; no second queue.
- Bad: a round trip; cooperative for Workers holding their own credential.

### Enforcement 3 — at completion only
- Good: no protocol change.
- Bad: detects after the side effect.

### Enforcement 4 — server-side execution
- Good: the credential never leaves the server.
- Bad: reverses ADR-0164; integrations in the engine process again.

### Enforcement 5 — egress gateway
- Good: prevents misuse of a released credential.
- Bad: a new network component; provider-specific request matching.

### Evidence a — engine value type (chosen)
- Good: one log, replayed, fenced and ordered with the job; retention and export already
  exist.
- Bad: two value types widen the processor's in-flight record.

### Evidence b — sidecar audit file
- Good: no engine change.
- Bad: a second durable log with its own crash semantics; outside replay.

### Policy C — compiled JSON + FEEL + DMN behind built-in guards (chosen)
- Good: reuses `expr` and temis; deterministic; guards cannot be overridden.
- Bad: Atlas owns a small policy model and its validation.

### Policy A — XACML/OPA/Cedar runtime
- Good: mature combining semantics.
- Bad: a second language and runtime; more surface than the problem.

### Credential β — Atlas brokers (chosen)
- Good: one refresh owner; rotation persisted; enforcement at the credential.
- Bad: Atlas becomes a holder of delegated refresh tokens — a more valuable target.

### Credential α — Worker environment
- Good: no change.
- Bad: no enforcement possible; rotation lost.

## Links

- builds on [ADR-0203](0203-worker-execution-model.md), [ADR-0168](0168-connector-work-on-a-worker.md),
  [ADR-0007](0007-job-worker-protocol.md), [ADR-0274](0274-in-process-job-leases.md)
- proposes an amendment to [ADR-0168](0168-connector-work-on-a-worker.md); constrains
  [ADR-0172](0172-entra-id-connector.md) and [ADR-0462](0462-exchange-worker.md)
- reuses [ADR-0210](0210-federated-authentication.md)'s verifier, [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)'s vault,
  [ADR-0015](0015-reuse-feel-engine.md) and [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)
- relates to [ADR-0297](0297-confine-internal-worker-token.md), [ADR-0141](0141-sharepoint-connector.md),
  [ADR-0044](0044-user-management-and-authentication-boundary.md), [ADR-0209](0209-roles-per-endpoint-group.md),
  [ADR-0281](0281-session-role-revocation.md)
- honors invariants I1–I6 ([invariants](../architecture/invariants.md))

## Amendment (2026-10-26): linking is set up in the Console

§4 configured the app registration people link through with four start flags
(`--link-microsoft-*`). The people who meet linking are the ones in the account menu,
and the page they reached told them to run a command line they do not have. Linking is
now set up under **Administration → Security** as well, and the people who cannot set it
up are no longer shown how.

- **Where it lives.** The tenant, the client id and the scopes are kept in
  `identity/link-microsoft.json`, beside the identities and connections they are for: a
  restore that carries the connections carries the tenant they belong to, and a portable
  backup carries neither. The client secret is a vault secret under the reserved name
  `atlas:link-client-secret:microsoft`. Like a connection's credential, no Worker
  reference, API token or secrets route reaches it, and the vault listing hides it,
  because with it and a connection's refresh token anyone can act as that person.
- **The flags win.** With any of the four flags given, they decide and the Console only
  shows them (`PUT` and `DELETE` answer 409). An incomplete set leaves linking off, as
  before, rather than handing the decision to the Console: an installation its
  deployment configures is not reconfigured from a browser.
- **What is checked.** The tenant and client ids must be GUIDs, because every token is
  checked against one tenant by its id and a domain name or `organizations` would refuse
  every link for a reason only the audit log knows. At least one resource scope is
  required (`openid`, `profile` and `offline_access` are added anyway), and a client
  secret on the first setup, because a web app registration redeems its code with one.
  The flags are not held to these rules; they keep the meaning they had.
- **It applies at once.** The flow is swapped whole: a request reads it once, a link in
  flight under the previous setup cannot be finished, and the start and callback routes
  are mounted whether or not linking is set up, answering 404 until it is. The broker's
  cached access tokens are dropped with every change, so none obtained under the
  previous setup is handed out after it. Whatever the disk holds after a write — all of
  it, or after a failure the previous setup or part of it — is what runs.
- **Changing it strands connections, visibly.** A connection is bound to its tenant
  (§3), and a refresh token to the client that obtained it. Another tenant leaves the
  existing connections unable to act; another client id makes their owners sign in
  again; removing the setup leaves all of them unable to act. None is revoked or
  deleted: switching back restores them. The setup view counts the unrevoked
  connections and those that cannot act, and the Console asks before a change that
  strands any.
- **What the Console cannot do** is the half at Microsoft: the registration, its redirect
  URI and whatever consent the tenant requires. The card lists those steps and shows the
  redirect URI this server sends, built as the link flow builds it, to be copied.
- **Who sees what.** A person who cannot set linking up is told it is not set up and
  that changing that is an administrator's; an administrator is pointed to the Security
  page. The account menu offers *Connected accounts* when the server can link an account,
  or when the person already has one linked or a connection on record — then it is where
  they withdraw it, even after linking was removed.

Routes: `GET`, `PUT`, `DELETE /api/v1/security/linking/microsoft`, admin-only and not MCP
tools. Audit: `identity.linking_set_up`, `identity.linking_removed`, and
`identity.linking_setup_unreadable` for a stored setup the server could not read at
start, which leaves linking off. Tests: `api/linkmicrosoftsetup_internal_test.go`,
`e2e/console-identity.spec.mjs`.

**Not changed:** linking is still a confidential client of one Entra tenant. A shared,
multi-tenant registration operated by the Atlas project — which would let linking work
without any registration of one's own, and reach personal Microsoft accounts — needs a
public client, a solution for per-installation redirect URIs and publisher verification,
and is a decision of its own, not taken here.
