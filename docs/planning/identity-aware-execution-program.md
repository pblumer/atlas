# Identity-aware execution and policy-based authorization: delivery programme

**Programme:** IAE (Identity-aware Execution)  
**Baseline checked:** 2026-10-25, `main` (programme registration only; no new runtime implementation claimed).  
**Roadmap owner:** [Milestone IAE](../../ROADMAP.md#milestone-iae--identity-aware-execution-).  
**Architecture authority:** [ADR-0475](../adr/0475-identity-aware-worker-execution.md) (**Proposed / Partial**).  
**Related programme:** [Capability-driven Automation (CDA)](capability-driven-automation-program.md). CDA governs semantic Worker operation contracts, discovery and opt-in capability routing; IAE governs *who can act, as whom, under which policy, with what durable evidence*.  
**Communication artifact:** [Identity-Aware Workflow Execution whitepaper](../whitepaper/identity-aware-workflow-execution.md) (strategic stakeholder narrative; not an accepted ADR or proof of completed implementation).

## Outcome and boundaries

An external action is not authorized merely because a Worker has credentials, a Worker Type claims a capability, or a BPMN task can reach a job. The intended end-to-end chain is:

```text
Atlas Principal + External Identity + Connection
     -> Worker identity policy and trusted job context
     -> PDP evaluates published policy (with trusted attributes)
     -> obligations (e.g. independent approval) satisfied
     -> durable AuthorizationDecided event; WAL + fsync
     -> PEP permits precisely bound, fenced action/credential
     -> Worker Instance executes externally
     -> durable outcome and attributable audit trail
```

A Worker Instance's Atlas protocol identity, the Worker configuration, the initiating/approving Atlas principals, and the external executing identity are different entities. Policy `Permit` does not imply remote provider permission. An authenticated user is not automatically authorized to bind or use somebody else's Connection.

The architecture uses existing Worker Type / Worker / Worker Instance concepts (ADR-0203), OIDC user federation (ADR-0210), sidecar and vault infrastructure, `pblumer/feel` and `pblumer/temis`. No new policy expression interpreter, no provider-side password collection, and no secret or access token in BPMN, jobs, variables or WAL. The IAE programme does not make all third-party Workers secure through documentation alone: enforceable boundaries and supported execution modes must be evidenced.

## Status and actual baseline

| Area | Verified reference | Status / limitation |
|---|---|---|
| Stable users, groups, roles, principals, federated login | ADR-0044, ADR-0210, ADR-0281; `api/userstore.go` | Implemented foundations; provider account linking must remain separate from login identity |
| Worker vocabulary and job lease/fencing | ADR-0203, ADR-0297 | Implemented foundations; ordinary Worker credential does not prove action authorization |
| Shared OAuth/token grants and SharePoint integration | `connector/oauth2`, `connector/sharepoint`, ADR-0141 | Existing app-only, cert and manually configured refresh-token methods; do not conflate with managed per-user Connections |
| Identity-aware Worker policy and delegated SharePoint example | [ADR-0475](../adr/0475-identity-aware-worker-execution.md), `authz`, `api/authorize.go` | **Proposed / Partial**: limited supervised, provider-specific path. Inspect code/tests again before claiming precise behaviors or broad enforcement |
| Google/Jira/Microsoft Worker family support | Worker packages and provider docs | Candidate extensions; not an all-provider guarantee |
| Independent technical capability conformance and routing | [CDA programme](capability-driven-automation-program.md), ADR-0480/0482/0483 | Separate programme; claims are not authorizations |
| Universal enforcement for arbitrary remote/third-party Workers | ADR-0475 and ADR-0168 | **Unresolved trust boundary**; require security decision before broadening |
| Production validation in a restricted BIT tenant | Tenant consent, Conditional Access and provider access policy | **Not demonstrated**; tenant admin configuration and controlled real integration tests are external gates |

The existence of an accepted or proposed ADR does not imply implementation is complete. Do not relabel the earlier delivered supervised SharePoint slice as universal policy enforcement.

## Deliverable slices and acceptance gates

| Slice | Status | Owner / milestone | Depends on | Demonstrable deliverable and closure gate |
|---|---|---|---|---|
| **IAE0 — Baseline, ADR and threat model** | 🚧 ADR proposed / partial baseline | Security architecture; IAE | ADR-0203, 0168, 0210, 0475 | Approve/adjust ADR-0475; executable trust boundaries, actor matrix, revocation, policy combining, retention, migrations and legacy modes explicitly decided |
| **IAE1 — External identities and Connections** | 🚧 Partial; verify coverage | API / identity / vault; IAE | IAE0 | Link multiple issuer+tenant+subject identities without changing Atlas login source; scoped connection ownership, binding, lifecycle, revocation, redacted APIs and restart tests |
| **IAE2 — Provider authentication and token lifecycle** | 🚧 Partial Microsoft path | OAuth / supervised Worker; IAE | IAE1 | Microsoft Authorization Code + PKCE with server-side validation, persisted refresh rotation, reauth errors, app-only/cert compatibility and isolated broker; mock tests and restart consistency |
| **IAE3 — Policy administration and deterministic decisions** | 🚧 Partial policy seam | `authz` / API / Temis; IAE | IAE0 | Publish immutable policy revisions, default deny/indeterminate handling, trusted PIP attributes, non-overridable guards, bounded FEEL/DMN rules and negative tests; no second FEEL/DMN engine |
| **IAE4 — Enforced authorization before external effects** | 🚧 Narrow supervised SharePoint example | Job protocol / Worker supervision / audit; IAE | IAE1–IAE3 | Server-authoritative fenced request, exact action/parameter binding, durable decision before credential release, fail-closed deny/revocation/retry, no replay side effects; explicitly list supported Worker execution modes |
| **IAE5 — Obligations and separation of duties** | 🔲 Not started / verify against ADR | BPMN User Tasks / authorization; IAE | IAE3–IAE4 | Independent approver bound to exact action digest, expiring approval, no self-approval, durable obligation satisfaction and recovery; never trust BPMN variable `approved=true` |
| **IAE6 — Console, Operations and observability** | 🔲 Not started / verify existing UX | Console / API / audit; IAE | IAE1–IAE5 | User-linked accounts, managed connections, policy editor and simulator, Worker binding, visible connection health, decision/approval/action audit, access-controlled views, accurate support labels |
| **IAE7 — Wider Worker Type adoption and hardening** | 🔲 Not started | Worker Type owners / ecosystem; IAE | IAE4–IAE6 and provider matrix | Per-operation provider compatibility table; expand Microsoft mail, selected Entra actions, Google Sheets/Drive and other compatible types; do not claim generic coverage without real PEP enforcement |
| **IAE8 — Real tenant PoC and production readiness assessment** | 🔲 Not started | Operators / security / tenant administration | IAE4–IAE6 plus consent | Controlled delegated SharePoint document scenario with admin-approved Entra app, effective site permissions, denial and audit demonstration; classify observed vs simulated results, risks and operational readiness |

**Statuses are conservative planning markers, not fresh full-suite code verification.** Closure requires specific linked PRs, tests and evidence; a merged PR is not itself conformance or production readiness.

### Dependency path

```text
IAE0 architecture + threat model
  |--> IAE1 identities/connections --> IAE2 OAuth/refresh
  |                                  |
  |--> IAE3 policies/DMN ------------+
                                     v
                              IAE4 enforcement
                                  |        |
                         IAE5 obligations  IAE6 UX/audit
                                  \        /
                                   IAE8 PoC
                                      |
                           IAE7 controlled expansion

CDA6 verified deployment binding ------> CDA7 governed execution
                       IAE4 provider-specific enforcement --^
```

Do not block core security and recovery work on the CDA graph or provider contract corpus. Conversely, CDA7 must not claim governed execution until **the specific pinned Worker path** is verified by IAE4, not merely because a semantic contract exists.

## Decision register and unresolved architectural questions

| Decision | Current state | Gate |
|---|---|---|
| [ADR-0475: Identity-aware Worker execution](../adr/0475-identity-aware-worker-execution.md) | Proposed / Partial | IAE0 review; all later security claims |
| [ADR-0168: Worker-side credential boundary](../adr/0168-connector-work-on-a-worker.md) | Accepted; external broker expansion unresolved | IAE4 remote Worker design |
| [ADR-0203: Worker vocabulary](../adr/0203-worker-execution-model.md) | Accepted / Landed | No replacement registry or invented Worker identity class |
| [ADR-0210: Federated authentication](../adr/0210-federated-authentication.md) | Accepted / Landed | Separate user login vs linked external accounts |
| [ADR-0281: Role revocation](../adr/0281-session-role-revocation.md) | Accepted / Landed | Clarify permission changes for in-flight jobs |
| [ADR-0482: Capability binding and routing](../adr/0482-capability-aware-binding-and-routing.md) | Proposed / Not started | CDA7 cannot treat metadata as permission |

1. **The PEP trust boundary:** Supervised trusted Worker, untrusted external Worker and script execution cannot all share an unqualified guarantee. Assess broker, restricted egress, sandbox and provider policy enforcement.
2. **Authorization durability:** Freeze policy ID/version, decision, actor and action digest before releasing a token; re-evaluate *new attempts* against applicable current revocation; replay historical facts without reauthorization.
3. **Policy precedence:** Define the conflict combining rule and fail-closed defaults before an editor ships.
4. **Identity binding:** Distinguish request initiator, approver, owner of external identity and runtime protocol principal; prohibit arbitrary FEEL-based impersonation.
5. **Obligations:** Connect to real immutable approval facts with job/action hash, not incidental process variables.
6. **Rotation and revocation:** Single authoritative encrypted refresh-token store, no stale overwrite, immediate visible connection state and safe incident transitions.
7. **Data protection and audit:** Restrict decision metadata visibility, data retention and export, no bearer material or private document bodies in the WAL.
8. **Policy timing:** Specify how version changes, disabled accounts and resumed timers affect authorization without retroactively altering recorded facts.

## First vertical showcase

**Delegated SharePoint document approval**: A signed-in Atlas user links a Microsoft account; an approved Connection is bound to one configured SharePoint Worker. A BPMN request identifies an exact document operation. Policy denies unauthorized deletion or demands independent approval, and rejects self-approval. After sufficient trusted approval, a fenced Worker gets permission to perform the exact operation using the authorized external identity, **only after a durable decision**. The result and policy provenance appear in Operations.

**Negative acceptance:** unknown identity, revoked user/connection, missing Graph consent or SharePoint access, stale lease, forged approval variable, policy Indeterminate, changed action after approval, crashed refresh, duplicate external effect and restart/replay. Microsoft Graph may deny despite Atlas Permit; no bypass or fallback to app-wide credentials.

**Provider test rule:** A fake token endpoint and mock Graph prove integration logic only. An actual BIT tenant needs its administrators' authorization and consent; do not state the BIT integration is proven until tested with approved accounts/sites.

## Whitepaper and positioning

Stakeholder narrative: *Identity-aware Workflow Execution — policy-enforced, attributable actions with durable evidence*. The previously produced externally shared whitepaper is an explanatory deliverable, **not** an accepted ADR or implementation proof. Publication of a vetted source document/PDF into the repository remains an explicit programme task. No unverified artifact path is linked here.

Comparison with n8n: [docs/comparisons/n8n.md](../comparisons/n8n.md). n8n offers end-user credentials in some editions; do **not** claim per-user OAuth is exclusive to atlas. Proposed differentiation is **the combination** of per-action policy, trusted approval obligations, immutable policy revision, pre-effect durability, identity attribution and replayable evidence, subject to measured product validation.

## Programme management and completion rules

- The programme file is the **status, dependency and evidence authority**. Each slice has one reviewable PR or clearly bounded PR sequence, a linked ADR decision, tests, owner subsystem, risk/blocked state and closure proof.
- Update this file and the Roadmap IAE subsection when a PR merges, a decision changes or a gate opens. Record remaining work; do not equate `Merged`, `Proposed`, `Partial`, `Landed` and `Production validated`.
- New runtime/processor/jobs/events/authz behavior requires negative security tests and a recovery comparison: process -> persist -> restart -> replay -> live/replay equality. No network I/O in `applyToState`; no pre-fsync side effects or allocations on command hot path.
- Follow `AGENTS.md`: `go build ./...`, `go test ./...`, `go test -race -timeout=45m ./...`, `go vet ./...`, `gofmt -l .` empty, coverage expectations and CI. Do not label docs-only changes as code verified.
- Do not edit the in-progress [PR #1346](https://github.com/pblumer/atlas/pull/1346): its technical capability semantic envelope belongs to CDA1, not this programme.
