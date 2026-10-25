# ADR-0483: Technical capability contract publication and conformance governance

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-10-25
- **Deciders:** atlas maintainers
- **Deferred decision:** External publisher trust roots and signed attestations remain out of scope until ADR-0208 package provenance and signature verification are implemented. The accepted initial trust model recognizes only maintainer-reviewed, source-controlled definitions and evidence.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0480](0480-technical-capability-contract.md) introduced optional `CapabilityClaim` descriptors on Worker Type operations. The draft semantic-envelope design in [PR #1346](https://github.com/pblumer/atlas/pull/1346) describes a contract independently of a provider. Neither a syntactically valid manifest claim nor a well-formed JSON Schema establishes that the operation really behaves as the contract promises.

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

**Accepted decision: option 3**, initially restricted to maintainer-reviewed definitions and tests committed alongside atlas. This record establishes the governance model; its implementation is **Not started**, and acceptance does **not** certify existing providers, promote manifest claims, or authorize runtime selection.

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

The proposed slices are CDA2 and CDA3 in [the CDA programme](../planning/capability-driven-automation-program.md):

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
