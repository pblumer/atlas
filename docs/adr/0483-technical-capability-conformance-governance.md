# ADR-0483: Technical capability contract publication and conformance governance

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-10-25
- **Deciders:** atlas maintainers
- **Open question:** Which externally issued signature trust roots, third-party attestations and installation trust controls can be admitted once ADR-0208 signature verification is implemented? This accepted first-party decision explicitly excludes them; until a new accepted decision, external claims remain unverified candidates.
- **Question checked:** 2026-10
- **Decision recorded:** 2026-10-25; option 3 approved for the CDA programme, with approval authenticated by repository provenance and the maintainer as sole reviewer for now (see *Approval authority in the initial phase*). This acceptance is an architecture decision, not proof that a provider conforms or that code is integrated.

## Context and problem statement

[ADR-0480](0480-technical-capability-contract.md) introduced optional `CapabilityClaim` descriptors on Worker Type operations. The versioned semantic-envelope implementation in merged [PR #1346](https://github.com/pblumer/atlas/pull/1346) describes a contract independently of a provider ([ADR-0485](0485-technical-capability-semantics.md), Proposed/Partial). Neither a syntactically valid manifest claim nor a well-formed JSON Schema establishes that the operation really behaves as the contract promises.

Entra users are tenant-scoped authentication accounts, LDAP entries obey a configured directory schema, and Google Contacts are address-book records. Common verbs do not establish substitutability. If claims were trusted automatically, the graph would report false coverage and a future router could execute an authorized *different* action from the one a process requested.

ADR-0208 specifies the Worker Type package trust model but its package-provenance features are only partially implemented. ADR-0475 has partially implemented identity-aware execution, not universal policy enforcement. The business capability register (ADR-0305) must not be turned into a technical contract store.

**Question:** How are versioned contracts owned and published, and how may a specific Worker Type operation earn, retain and lose an independently verified claim without creating a second mutable operation catalogue or making metadata an authorization grant?

## Decision drivers

- Exactly one normative meaning for an immutable `id + version + profile` contract identity.
- Explicit provenance for the contract document, mapped operation and evidence.
- Reuse existing built-in operation catalogues; do not manually clone operation inventories.
- Fail closed on missing, incompatible or expired evidence; never infer semantics from names.
- Keep contract validation and verification outside the engine hot path.
- Preserve stable Worker Type, configured Worker and Worker Instance boundaries (ADR-0203).
- No source credentials, personal documents or external payloads in the technical contracts or WAL.
- No third-party declaration becomes trusted merely because it passes JSON parsing.

## Considered options

1. **Self-attestation:** any manifest `capabilities[]` claim is immediately trusted.
2. **Central mutable contract database:** users register contracts and operation mappings in a new installation-wide store.
3. **Reviewable, immutable contract artifacts with evidence tied to exact package/operation digests**, plus explicit publication and revocation gates.
4. **Infer contracts from provider method names and OpenAPI schemas** on startup.

## Decision outcome

**Decision: option 3 — accepted.** Initially only maintainer-reviewed definitions and independently reviewed evidence committed alongside atlas can be trusted. This decision establishes the publication and conformance governance model; it does **not** certify existing providers or make a runtime selection decision. Do not derive `verified` from schema-only fixture success, a self-declared reviewer string or a submitted report.

**Implementation status:** The publication, approval and revocation workflow is not implemented. Schema-only checks in [PR #1357](https://github.com/pblumer/atlas/pull/1357) and content digests in [PR #1360](https://github.com/pblumer/atlas/pull/1360) are under review; they are not independently verified evidence and have not been integrated into `main`.

### Initial publication and trust boundary (accepted)

- A repository-reviewed publication comprises the exact canonical contract,
  its content digest, the exact Worker Type package/code digest and operation/job
  type, reproducible conformance results (including meaningful negative cases),
  and an **independent approval in the repository review history**. A string
  such as `reviewer: alice` inside a JSON file is **not** proof of approval.
- Recalculate digests from immutable bytes when verifying publication or
  evidence; never rely on a claim-supplied hash alone. A changed contract,
  package/operation, fixture or material test assumption invalidates the old
  approval rather than silently updating it.
- Maintain publication/withdrawal history as reviewable, append-only source
  artifacts. A withdrawal or reapproval references the previous immutable
  publication and includes a reason. It does not overwrite the original
  contract or past evidence.
- Statuses exposed outside build/review tooling are **candidate** until an
  implementation securely binds the evidence and authenticates the approval
  from trusted repository/installation provenance. This ADR authorizes no
  direct `verified` property on an arbitrary manifest or API payload.
- Package signing and external publisher onboarding are **not approved** by
  this decision and cannot be added as an implementation shortcut; revisit
  their trust anchors separately under ADR-0208.

### Approval authority in the initial phase (accepted)

Three ways to authenticate the independent approval were weighed for CDA2:
repository provenance, reviewer signatures checked against a key list in the
repository, and a CI job that reads pull-request reviews through the GitHub
API. The maintainer chose **repository provenance**:

- Evidence, publication and withdrawal records live in a source path owned
  through `CODEOWNERS`. A record counts as approved only once it reached the
  protected `main` branch through a pull request that the code owner approved
  or personally merged. Build tooling verifies offline everything a file can
  prove (recomputed digests, the binding to the contract identity and to the
  exact Worker Type operation, staleness, append-only withdrawal) and takes
  the protected-branch history as the approval itself.
- The code cannot authenticate a person on its own. Branch protection and the
  code-owner rule are therefore part of the trust boundary: they must be in
  force before the first record is published, because other collaborators
  with write access could otherwise merge evidence without the code owner.
- **Single reviewer for now.** The maintainer (pblumer) is the only code owner
  and reviewer. Changes prepared by coding agents are pushed under the
  maintainer's account, so GitHub cannot record a separate approving review
  for them; for those the approval is the maintainer's own merge. A merge
  performed by a coding agent, even with the maintainer's credentials, is
  **not** an approval.
- Independence in this phase means separation between whoever authored the
  evidence (a contributor or a coding agent) and the maintainer's acceptance,
  not two independent people. Evidence approved this way records that a
  single maintainer approved it, so a later, stricter rule can tell such
  approvals apart instead of silently inheriting them.
- Reviewer signatures and API-verified reviews are not adopted. Adding a
  second reviewer or a stricter rule amends this record.

### Normative identity and artifact ownership

- A canonical technical capability is identified by `id`, exact SemVer `version`, and an explicit `profile` (which may be empty). The meaning, accepted inputs, outputs, errors, side effects and consistency requirements are immutable under that identity.
- A canonical document is source-controlled, canonicalized for content hashing and distributed as a read-only artifact with atlas. A new meaning requires a new identity/version/profile; a corrected *publication record* may withdraw a bad artifact, but must not silently redefine it.
- The canonical document is not a new `api/capability` record or a second runtime store. A future external distribution channel can reuse ADR-0208's package envelope after its trust model is operational.
- A `CapabilityClaim` in a Worker Type manifest remains **advisory** until evidence binds that *particular* implementation to the canonical contract. The package's own version is not the contract version.
- The initial ADR-0480 manifest admission rule rejects multiple claims with the same `id@version` within one operation even when profiles differ. This is an intentional current limitation to be reviewed during T1b: the semantic contract catalogue may define multiple profiles, but a single operation cannot declare two simultaneously without a separately tested manifest change.

### Evidence and conformance

A verification record must capture, at minimum:

| Fact | Purpose |
|---|---|
| Contract ID, exact version, profile and immutable content digest | Identify the specification actually tested |
| Worker Type ID, package version or code/artifact digest, operation ID and job type | Bind evidence to one executable operation |
| Schema-validator and conformance-runner versions | Make the result reproducible |
| Fixture/expectation revision and test-environment assumptions | Prevent passing only a generic CRUD smoke test |
| Observed results including negative/error cases | Show what passed, failed or was not exercised |
| Reviewer, review date, publication state and revocation reason | Separate verification from the provider's self-assertion |

Conformance checks include schema keywords (not only the JSON Schema root), absent versus null fields, resource identity/scope, error mapping, authorization failures, pagination/truncation, retry and idempotency behavior, partial failure and eventual consistency. Test cases must distinguish account, LDAP entry and contact semantics. An implementation may support a **provider-specific profile** without claiming generic cross-provider equivalence.

Use mock providers for deterministic, credential-free CI, supplemented by explicit opt-in live integration tests where provider behavior cannot be proved in a mock. A mock test alone must not be labelled evidence of a live provider's guarantees. Disable remote schema resolution, unbounded references and network I/O in validator execution. Conformance runs happen in build/review tooling, not in `applyToState`, job leasing or the processor.

### Trust and claim states

For read-only discovery and impact analysis:

- **Candidate:** self-declared or otherwise mapped but not independently proven. Display with provenance; never eligible for generic routing.
- **Verified:** exact operation and contract identity have current, reviewer-approved evidence for the claimed scope/profile. Verification is **not** authorization or a guarantee that the current configured Worker is reachable.
- **Withdrawn:** the previous evidence was revoked, superseded by an incompatible implementation, or failed revalidation. Do not silently relabel it as verified.

Absence of evidence means *unknown*, not `unsupported`. Publish an `unsupported` conclusion only after an explicit negative test of a declared scope, and never assume a provider has no ability merely because atlas does not map one of its operations.

Changes to contract bytes, operation semantics, Worker Type package/runtime digest or material conformance fixtures invalidate the existing approval until reverified. Changes to credentials, installed Worker configuration, authorization policy and Worker Instance health affect **runtime eligibility** and must be checked separately; they are not part of static conformance.

### Admission, lookup and compatibility

Do not add a new mutable design-time store. Project verified technical capabilities from the authoritative Worker Type catalogues and trusted contract/evidence artifacts at read time. Only immutable source identifiers are joined, never display names or approximate verbs. A publication gate rejects duplicate contract identities with different content, evidence that names an absent operation, out-of-date package digests and semantic contract/profile mismatches.

No backwards-compatibility or substitutability inference is made merely because two contracts share a major SemVer version. A process seeking an exact version/profile must have explicit validated matching behavior; an alternate version requires a documented, tested compatibility rule, which is out of scope for the first slice.

### Implementation boundaries and gates

The delivery slices are CDA2 and CDA3 in [the CDA programme](../planning/capability-driven-automation-program.md):

1. Specify the immutable artifact encoding and digest; choose a maintained JSON Schema Draft 2020-12 validator with remote resolution disabled.
2. Create positive **and** adversarial conformance fixtures with meaningful assertions; test provider error mappings and idempotency.
3. Establish review/revocation records and reproducible report generation.
4. Project a first **honest** provider mapping from an existing built-in catalogue, preferably Entra **or** Google Contacts, and prove that the other resource kind cannot pass that contract by accident.
5. Add more mappings only after conformance evidence exists. Keep third-party self-assertions as candidate until ADR-0208 trust dependencies are resolved.

No HTTP/MCP endpoint, new sidecar store, credential broker, routing action or WAL record is introduced by this ADR.

### Consequences

- **Positive:** Descriptive claims can be distinguished from independently checked behavior; provider drift and withdrawal become reviewable.
- **Positive:** Contracts remain portable, immutable and separate from business architecture and runtime authorization.
- **Negative:** Provider onboarding costs meaningful testing and review; a claim may remain candidate for a long time.
- **Follow-ups / risks:** Secure third-party attestations depend on ADR-0208; a false conformance fixture can create false trust; semantic version and profile compatibility must be tested, not assumed. Do not mark a Worker eligible for capability routing until the security gates of the routing ADR also pass.

## Pros and cons of the options

### Option 1 — Self-attestation
- Good: Lowest cost to package authors.
- Bad: A package can make unverified interchangeability and compliance claims.

### Option 2 — Mutable contract database
- Good: Easy installation-specific edits.
- Bad: Duplicated sources of truth, drift, and no immutable definition for deployed processes.

### Option 3 — Immutable artifacts plus evidence (accepted)
- Good: Reviewable, auditable, reproducible and consistent with ADR-0208.
- Bad: Requires a publication pipeline and withdrawal discipline.

### Option 4 — Infer from provider schemas
- Good: Automated discovery.
- Bad: Similar field types do not establish business semantics, security or side-effect guarantees.

## Links

- [CDA programme and delivery gates](../planning/capability-driven-automation-program.md)
- [ADR-0480 — Technical capability claims](0480-technical-capability-contract.md)
- [ADR-0203 — Worker execution model](0203-worker-execution-model.md)
- [ADR-0208 — Worker Type package trust](0208-worker-type-packages.md)
- [ADR-0305 — Business capability records](0305-business-capabilities-and-value-streams.md)
- [ADR-0420 — Unified graph proposal](0420-one-graph-model-several-projections.md)
- [ADR-0475 — Identity-aware execution](0475-identity-aware-worker-execution.md)
- [PR #1346 — Semantic contract envelope](https://github.com/pblumer/atlas/pull/1346)
