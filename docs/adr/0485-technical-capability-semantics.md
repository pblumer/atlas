# ADR-0485: Versioned semantic contracts for technical capabilities

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-24
- **Deciders:** atlas maintainers
- **Open question:** Which vetted JSON Schema validation and provider conformance runner should gate verified provider mappings? This initial record limits enforcement to the contract envelope; full JSON Schema validation and provider evidence are subsequent slices.
- **Question checked:** 2026-10

## Context and problem statement

ADR-0480 added optional `CapabilityClaim` metadata to each Worker Type operation: an ID, exact semantic version and optional profile. It explicitly does not prove interchangeability, route jobs by capability, or define the claimed contract. An Entra user, an LDAP entry and a Google contact remain different resource kinds, even when their operations use the same CRUD verb.

Milestone 6, T1 in `docs/planning/technical-capability-contract.md`, needs a canonical description against which declarations can eventually be reviewed and tested. The description must be versioned independently of Worker Type packages and must not become a new mutable integration-capability register (ADR-0305) or an execution registry (ADR-0203).

## Decision drivers

- Keep the normative contract ID, semantic version and profile consistent with ADR-0480.
- Distinguish resource semantics, identity scope, request and response shapes, errors, pagination, retry/idempotency and consistency.
- Prefer a portable, inspectable artifact, readable without installing any Worker.
- Reject malformed contract metadata without network access or side effects.
- Retain provider operation tables as the only source of available implementation operations.
- Preserve all six engine invariants: nothing in the processor, event model, WAL, worker routing or `applyToState` changes.
- Treat a contract declaration and a provider claim as **unverified** until a conformance runner, evidence and reviewer have approved their relationship.

## Considered options

1. **Free-text prose in the Worker Type manifest.** No enforceable shape, duplicate contracts in each package and no independent versioning.
2. **Store technical capability records in `api/capability`.** Creates a second design-time register, conflates business ownership with technical semantics and contradicts ADR-0305.
3. **A strict, independently versioned JSON semantic-contract envelope.** Separate definition artifacts, no persisted store or runtime selection. Validate the envelope and its JSON Schema root declarations now; add full conformance later.
4. **Immediate JSON Schema validation of provider inputs/outputs at runtime.** Changes job execution and performance without conformance evidence, routing or policy decisions.

## Decision outcome

Chosen for T1a: **option 3**. Introduce pure Go parsing/structural validation in the existing `workertype` package, with contract fixtures under its testdata directory.

### Identity and ownership

A technical contract is identified by `(metadata.id, metadata.version, metadata.profile)`; version is an exact SemVer, never a range or a Worker Type package version. The ID uses ADR-0480’s lower-case dotted identifier syntax. A profile differentiates constraints within the same capability; an empty profile is an explicit profile value, **not** evidence that every provider implements the same behavior. Once published, an identity is immutable; changing semantics requires a new version/profile, not editing a previous definition.

This first slice **does not publish a canonical production corpus**: fixtures in `workertype/testdata` are examples of the envelope, not approved operational contracts. Eventually a curated, reviewable corpus can be shipped with the binary as ordinary immutable source artifacts. It is not a new sidecar store, mutable system of record or provider registry. Publishing and trust rules remain a separate follow-up.

### Envelope (v1alpha1)

```json
{
  "apiVersion": "atlas.io/technical-capability/v1alpha1",
  "kind": "TechnicalCapabilityContract",
  "metadata": {
    "id": "io.atlas.contact.record.create",
    "version": "1.0.0",
    "profile": "address-book",
    "name": "Create an address-book contact"
  },
  "spec": {
    "resourceKind": "contact-record",
    "identityScope": "address-book",
    "verb": "create",
    "inputSchema": { "$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object" },
    "outputSchema": { "$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object" },
    "errors": [{ "code": "CONTACT_ALREADY_EXISTS", "description": "A matching contact already exists", "retryable": false }],
    "idempotency": "non-idempotent",
    "consistency": "provider-defined",
    "pagination": "none"
  }
}
```

- `resourceKind` and `identityScope` are explicit lower-case kebab-case identifiers; a contact record is not an account, and an address book is not a tenant.
- `verb` is descriptive, never used to infer equivalence.
- `inputSchema` / `outputSchema` are self-contained JSON Schema Draft 2020-12 **descriptions**; v1alpha1 admits root `object` or `null`, not network-loaded schema documents.
- `errors` are distinct symbolic codes with descriptions and a declared retryable flag, not vendor HTTP or LDAP codes.
- `idempotency` is one of `read-only`, `idempotent`, `conditional`, `non-idempotent`, `unknown`.
- `consistency` is one of `strong`, `eventual`, `provider-defined`, `unknown`.
- `pagination` is `none`, `cursor`, `offset` or `provider-defined`; any paginated result must declare a positive bounded `maxResults`, and a non-paginated one must not.

### Exact validation boundary

`ParseTechnicalContractJSON` rejects unknown envelope fields, multiple JSON values, invalid IDs/versions/profiles, missing required semantics, invalid enum values, duplicate error codes and malformed/unpinned root schemas. `ValidateTechnicalContracts` rejects repeated `(id, version, profile)` identities in a set.

**This is not full JSON Schema validation.** Embedded schemas remain JSON Schema documents; T1a checks their root dialect and type only and does not assert that provider request or result objects conform to every keyword. No network retrieval, `$ref` execution, provider calls, or runtime input/output validation is introduced. Because schema documents are not evaluated, consumers must not advertise provider conformance based on a successful envelope parse.

### Conformance and trust (not yet implemented)

T1b will choose a maintained JSON Schema validator, restrict remote references to prevent network retrieval, validate positive/negative fixtures, and define a provider mock/conformance runner with independently reviewed expected outcomes. A self-declared capability claim cannot mark its own conformance state. Evidence must include the exact contract identity and content digest, operation mapping, runner/fixture revision, and review status. The contract format alone does not grant policy permissions (ADR-0475).

### Stability and compatibility

This slice does not change `workertype.Manifest`, existing operations, job type indices, BPMN XML, process compilation, credentials, jobs, WAL, events or the graph. Existing manifests continue to parse exactly as before. No recovery test is necessary for design-time-only metadata; runtime changes in later slices require deterministic live/replay tests.

### Consequences

- **Positive:** A strict, reviewable contract vocabulary that distinguishes accounts, directory entries and contacts before any provider equivalence is considered.
- **Positive:** Pure design-time parser; no new store, network path, third-party runtime code or processor allocation.
- **Negative:** JSON Schema keyword-level conformance and publisher trust remain outstanding, and fixture documents are not operational guarantees.
- **Follow-ups:** T1b conformance runner, T2 source-derived provider mappings, T3 graph relations under ADR-0420, and separately approved T5 policy-gated routing.

## Pros and cons of the options

### Option 1 — Prose in manifests
- Good: Almost no code.
- Bad: No consistent, versioned definition to test; repeats data for every provider.

### Option 2 — Persisted integration capability register
- Good: User-defined catalogue.
- Bad: Conflicts with ADR-0305 and introduces synchronized copies.

### Option 3 — Strict contract envelope (chosen)
- Good: Clear semantics, small Go package, versionable source artifacts.
- Bad: A descriptive contract still needs provider-level conformance evidence.

### Option 4 — Runtime schema enforcement now
- Good: Early enforcement.
- Bad: Moves interpretation into job execution and implies false confidence without proof; requires a separate runtime/security decision.

## Links

- [ADR-0480 — Technical capability claims](0480-technical-capability-contract.md)
- [ADR-0203 — Worker execution model](0203-worker-execution-model.md)
- [ADR-0208 — Worker Type packages](0208-worker-type-packages.md)
- [ADR-0305 — Business capabilities](0305-business-capabilities-and-value-streams.md)
- [ADR-0420 — One graph model](0420-one-graph-model-several-projections.md)
- [ADR-0475 — Identity-aware Worker execution](0475-identity-aware-worker-execution.md)
- [Implementation plan](../planning/technical-capability-contract.md)
