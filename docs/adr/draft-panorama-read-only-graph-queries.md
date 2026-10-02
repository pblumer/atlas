# ADR-DRAFT: Read-only graph queries in Panorama

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0211](0211-panorama-derived-landscape-mesh.md) established Panorama's Starmap as a derived, whole-instance graph over resources Atlas already owns. The graph is a projection rather than a second source of truth: process applications, deployed processes, calls, Workers, Worker Types, releases, deployment targets, and DMN decisions contribute nodes and factual relationships, while the requesting principal's sharing scopes determine what can be seen. Search, filters, dependency analysis, and impact analysis already operate over that landscape.

Those fixed interactions answer known questions well. They do not give a user a general way to ask a new graph-shaped question without Atlas first growing another dedicated filter, endpoint, or UI control. Examples include finding all paths from an application to deployment targets, all processes that depend on a class of Worker, or the part of a landscape reachable from a selected process within a bounded number of hops.

Graph query languages already have a familiar declarative vocabulary for that class of question. Cypher popularised the `MATCH` / `WHERE` / `RETURN` form, and ISO GQL now standardises a property-graph query language with substantial syntactic and semantic overlap. Atlas can benefit from that familiarity without adding Neo4j, duplicating the landscape in a graph database, or claiming conformance with a language profile it does not implement.

The decision to make is therefore:

**Should Panorama expose an ad-hoc graph query surface over the Starmap, and if so, what language profile, authorization boundary, execution limits, API contract, and persistence model keep that feature consistent with Atlas's existing architecture?**

## Decision drivers

- Reuse ADR-0211's derived landscape instead of introducing a second graph or synchronization path.
- Preserve the existing per-principal sharing-scope boundary and prevent graph queries from becoming an inference channel for hidden resources.
- Let users express path-oriented questions with a familiar declarative syntax rather than an Atlas-specific form language.
- Render query results with the existing Starmap semantics, including provenance, observation state, restricted placeholders, layout, and drill-down.
- Keep the query language read-only; Panorama queries must never mutate design-time models, engine state, Workers, deployments, or remote systems.
- Bound CPU, memory, traversal depth, and result size so an interactive query cannot become an unbounded graph walk.
- Keep query semantics on the server and expose the supported graph schema to the browser instead of duplicating it in UI code.
- Be explicit about compatibility: Atlas should be able to evolve toward GQL where useful without claiming Cypher, openCypher, or GQL conformance prematurely.
- Keep the engine invariants untouched: no WAL records, processor commands, `applyToState` changes, or runtime hot-path work are introduced by this feature.

## Considered options

1. Continue with fixed search, filters, dependency, and impact endpoints only.
2. Define an Atlas-specific JSON or form-based graph query DSL.
3. Add Neo4j or another graph database and expose its query language over a synchronized copy of the landscape.
4. Add a versioned, read-only Cypher/GQL-inspired query profile evaluated directly over ADR-0211's authorized landscape projection.

## Decision outcome

Chosen option: **4 — a versioned, read-only Cypher/GQL-inspired query profile over the authorized Panorama landscape projection.**

The first profile is named **Panorama Graph Query v1**. Its syntax deliberately follows the familiar property-graph shape of `MATCH`, `WHERE`, and `RETURN`, but Atlas does **not** claim that v1 is Cypher, openCypher, or ISO GQL conformant. GQL is the long-term standards direction when compatible syntax can be adopted without weakening the constraints in this record.

### 1. Authorization happens before query evaluation

The query evaluator never receives Atlas's unrestricted landscape and never performs authorization after matching.

The required order is:

```text
Atlas resources
  -> ADR-0211 per-principal landscape projection
  -> Panorama Graph Query evaluator
  -> result graph
  -> Starmap renderer
```

This order is security-significant. Evaluating against a complete graph and filtering the result afterwards could reveal hidden structure through path existence, result cardinality, predicates, or timing even when hidden node names are removed.

ADR-0211's restricted placeholders remain the only representation of a path cut by sharing scope. A restricted placeholder is opaque to the query language: it may appear as the endpoint already exposed by the authorized mesh, but it is not traversable as a bridge to discover topology behind it and exposes no properties beyond the metadata ADR-0211 already permits.

A query-backed saved view, if added later, is re-evaluated against the current principal's current authorized landscape every time it is opened. Persisted query text must never carry or cache authorization results.

### 2. Panorama Graph Query v1 is intentionally small and read-only

The initial profile supports the minimum useful graph-selection vocabulary:

- `MATCH` over node and relationship patterns;
- `WHERE` with property access, typed parameters, boolean operators, and basic comparisons;
- `RETURN` of matched node, relationship, and path variables that can form a result graph;
- `ORDER BY` and `LIMIT` where they have deterministic graph-result semantics;
- node-kind and relationship-kind constraints from the Panorama graph schema;
- bounded variable-length paths, for example a conceptual `[*1..4]` traversal;
- typed request parameters supplied separately from query text.

Unbounded variable-length paths are rejected. v1 does not include scalar aggregation as a primary result form: `COUNT`, grouping, tabular projections, and similar analytics are a later language-profile decision because the first product surface is a graph renderer and aggregate queries introduce additional inference and resource-governance questions.

The following classes of operation are outside the profile and are rejected during validation, including equivalent spellings introduced by any future parser dependency:

- graph mutations such as `CREATE`, `INSERT`, `MERGE`, `DELETE`, `SET`, and `REMOVE`;
- schema, transaction, session, or administration commands;
- procedures or arbitrary function execution such as unrestricted `CALL`;
- external I/O such as `LOAD CSV`, URL access, filesystem access, or network access;
- query constructs that can execute code or cause side effects.

This is a language-profile decision, not merely a UI restriction. The server rejects unsupported constructs even if a client bypasses Panorama.

### 3. The server owns the queryable graph schema

The query language addresses a versioned Panorama graph schema rather than Go types, Pebble keys, HTTP implementation details, or browser-specific names.

The server publishes the schema needed by clients, including:

- profile version;
- queryable node kinds;
- relationship kinds and their valid endpoints;
- queryable properties and their value types;
- supported clauses, operators, and functions;
- enforced limits relevant to authoring.

A route such as `GET /api/v1/panorama/query/schema` may expose that contract. The exact route is an implementation detail, but there is one canonical server-owned description. Panorama uses it for autocomplete, validation hints, and documentation rather than maintaining a second relationship/property matrix in JavaScript.

The schema may evolve compatibly within a profile; a semantic breaking change requires a new profile version.

### 4. Queries return a graph projection, not a database result set

A route such as `POST /api/v1/panorama/query` evaluates a query and typed parameters against the caller's authorized landscape. The response is a graph projection compatible with the Starmap renderer where practical, plus query metadata.

Conceptually:

```json
{
  "query": "MATCH p = (a)-[*1..4]->(x) WHERE a.name = $name RETURN p",
  "parameters": {
    "name": "Identity Management"
  }
}
```

The response carries at least:

- the graph nodes and relationships selected for rendering;
- the query profile version;
- the landscape observation/read time used for the query;
- diagnostics and source ranges for parse or validation findings;
- the effective traversal/result limits;
- explicit completeness information when a bound affects the answer.

Atlas must never silently truncate a graph and present it as complete. A query that exceeds a hard bound either fails with a clear diagnostic or returns an explicitly partial result whose metadata states which bound was reached. The UI makes that state visible next to the result.

### 5. Evaluation is bounded by construction

The service enforces server-side limits independent of client input. The initial implementation defines measured defaults for at least:

- query text size;
- parsed AST complexity;
- maximum variable-path depth;
- evaluation deadline;
- visited nodes and relationships;
- matches considered;
- returned nodes and relationships.

Parameters are transported and typed separately; user values are not interpolated into query source by the browser.

The parser produces an AST, semantic validation resolves kinds, relationships, properties, and supported operations, and only a validated bounded plan can traverse the immutable request-local landscape projection. There is no arbitrary expression evaluator and no escape from the supplied graph abstraction into Atlas internals.

The implementation may reuse an open-source parser or grammar only after its licence, maintenance state, grammar version, and ability to enforce this restricted profile have been reviewed. This ADR deliberately does not select a parser library or a graph database.

### 6. Panorama reuses Starmap instead of building another graph renderer

Panorama gains a query authoring surface associated with the Starmap. The first vertical slice provides:

- a query editor;
- server-backed syntax/schema assistance;
- `Run`;
- line/column parse and semantic diagnostics;
- visible limit/completeness information;
- rendering of the returned subgraph on the existing Starmap canvas;
- the existing node details, provenance, status, restricted-placeholder rendering, layout, and drill-down behaviour where those semantics apply to the selected nodes.

A useful product shape is `Explore | Query | Saved views`, but that navigation is not normative. Existing saved Starmap views remain distinct from query definitions until a later slice explicitly integrates them.

### 7. Saving a query is a follow-up, not materializing its result

A later query-backed saved-view slice may persist query text, typed parameter defaults, and presentation state such as camera or manual layout. It does **not** persist the materialized result graph as architectural truth.

Opening such a view re-runs the query against the current authorized landscape. This preserves ADR-0211's projection semantics, current observations, and authorization revocation behaviour.

If saved queries need storage, they are design-time sidecar state and use the existing `api/sidecar` discipline. They do not enter the engine WAL or `applyToState`.

### 8. This feature stays outside the engine execution path

Panorama graph queries are an API/read-model concern. They do not create commands or events, do not alter process execution, and do not participate in recovery.

The query service follows [ADR-0147](0147-splitting-the-api-server-object.md): it is a dedicated API service rather than more unrelated methods on the central server object, and it reaches shared Atlas state only through the established service/run-loop boundaries used by the landscape derivation.

The engine's "compile, don't interpret" invariant remains intact. Interactive query text cannot be compiled at BPMN deployment because it does not exist then; instead, each query request is parsed and validated once into an execution representation before traversal. No query parsing or evaluation enters the processor batch cycle.

### 9. Testing must prove authorization and bounds, not only syntax

The implementation requires tests for at least:

- parser/AST behaviour for every supported construct;
- rejection of every mutation, administration, external-I/O, and unsupported construct class;
- graph-schema semantic validation and typed parameters;
- bounded path traversal over cycles;
- deterministic results for the same authorized graph, query, and parameters;
- query-size, AST, deadline, traversal, match, and result limits;
- explicit partial/failure behaviour when a bound is reached;
- authorization regressions proving hidden nodes, properties, and topology cannot be inferred through a query beyond ADR-0211's existing restricted-placeholder disclosure;
- restricted placeholders being opaque/non-traversable;
- API contract and error source ranges;
- Panorama rendering through the existing Starmap path rather than a second renderer.

This feature by itself does not mutate engine persistence, processor behaviour, events, jobs, timers, messages, or variables, so it does not require an engine recovery test. If an implementation slice later changes those areas, the normal Atlas recovery-test requirement applies to that change.

### 10. Roadmap placement

Milestone P is already complete; this decision does not retroactively reopen P1-P5. It is a post-Milestone-P Panorama extension, naturally following P2.5 because the derived landscape is its execution substrate.

A vertical delivery should be tracked as a new Panorama follow-up slice rather than as isolated parser, API, and UI layers. The first slice is complete only when one supported query can be authored in Panorama, evaluated under the caller's sharing scope and resource limits, and rendered as a Starmap result end to end.

Query-backed saved views, broader language constructs, scalar/aggregate results, and any claim of GQL compatibility beyond explicitly documented syntax are separate follow-ups.

### Consequences

- **Positive:** users can ask new dependency and path questions without waiting for a dedicated Atlas feature for each question.
- **Positive:** Atlas reuses its existing factual landscape, authorization semantics, provenance, status, and renderer; no graph database or synchronization pipeline is introduced.
- **Positive:** familiar graph-query syntax lowers the conceptual cost while a versioned Atlas profile keeps the contract small and testable.
- **Positive:** the feature remains outside the workflow processor and therefore does not compromise durable-before-visible, single-writer, `applyToState`, or processor hot-path invariants.
- **Negative / trade-off:** a query parser, semantic validator, bounded evaluator, diagnostics, and schema contract are substantial product surface even for a subset.
- **Negative / trade-off:** Cypher/GQL-like syntax creates a risk that users assume full compatibility. The UI and documentation must call the language `Panorama Graph Query v1` and enumerate supported constructs.
- **Negative / trade-off:** ad-hoc traversal creates denial-of-service and information-inference risks that fixed filters do not expose as broadly; strict pre-authorization, opaque restricted placeholders, and resource limits are mandatory rather than optimizations.
- **Follow-up:** decide whether a suitable parser/grammar can be reused after licence and maintenance review, or whether the deliberately small v1 grammar is better implemented locally.
- **Follow-up:** add query-backed saved-view integration only after the query execution contract is stable.
- **Follow-up:** evaluate additional GQL-compatible syntax profile by profile; never silently expand the accepted language through a parser-library upgrade.

## Pros and cons of the options

### Option 1: Fixed search, filters, dependency, and impact endpoints only

- Good: smallest attack surface and simplest product model.
- Good: every supported question can receive a purpose-built UX.
- Bad: each new graph question requires Atlas development even when the underlying mesh already contains the answer.
- Bad: combinatorial filters eventually approximate a query language without the clarity of one.

### Option 2: Atlas-specific JSON or form DSL

- Good: easy to constrain and transport through the HTTP API.
- Good: could map directly onto internal traversal operations.
- Bad: users must learn Atlas-only syntax for a standard graph-query problem.
- Bad: a form or JSON tree becomes awkward for multi-hop patterns and boolean predicates.
- Bad: inventing syntax does not remove the need for semantic validation, authorization, or resource bounds.

### Option 3: Neo4j or another graph database with its native query language

- Good: mature graph execution and a broad query language are available immediately.
- Good: existing graph tooling could connect to the database.
- Bad: creates a second persisted representation of resources ADR-0211 deliberately derives from Atlas's own stores.
- Bad: synchronization, staleness, backup, authorization, schema migration, and operational ownership become new correctness problems.
- Bad: querying the database safely would still require Atlas-specific authorization and result semantics.
- Bad: the added platform dependency is disproportionate to an interactive projection that Atlas can already construct.

### Option 4: Read-only Cypher/GQL-inspired profile over the authorized landscape

- Good: familiar graph-pattern vocabulary without a second database.
- Good: preserves ADR-0211's source-of-truth and authorization model.
- Good: a small versioned profile can be bounded, tested, and evolved deliberately toward GQL syntax.
- Good: results naturally reuse the Starmap graph renderer.
- Bad: Atlas owns the evaluator semantics and must document exactly where they differ from Cypher/GQL.
- Bad: parser/evaluator correctness and security become part of Atlas's API surface.

## Links

- [ADR-0071: Sharing scopes](0071-sharing-scopes.md)
- [ADR-0147: Splitting the API server object](0147-splitting-the-api-server-object.md)
- [ADR-0189: Panorama architecture modeling and live operational overlays](0189-panorama-architecture-modeling-and-live-overlays.md)
- [ADR-0211: Panorama's derived landscape mesh and notation projections](0211-panorama-derived-landscape-mesh.md)
- [Atlas architecture invariants](../architecture/invariants.md)
- [openCypher specification resources](https://opencypher.org/resources/)
- [Neo4j: GQL conformance of Cypher](https://neo4j.com/docs/cypher-manual/current/appendix/gql-conformance/)
