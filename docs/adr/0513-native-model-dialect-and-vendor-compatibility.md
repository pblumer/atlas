# ADR-0513: Native atlas authoring with versioned vendor-format compatibility

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-30
- **Deciders:** atlas maintainers
- **Open question:** The lossless mapping coverage of existing Zeebe BPMN and Camunda DMN extensions, including unknown extension elements, is not yet independently inventoried.
- **Question checked:** 2026-10

## Context and problem statement

atlas uses BPMN 2.x and DMN as interchange standards. The BPMN editor embeds `bpmn-js` and the `zeebe-bpmn-moddle` descriptor (`api/web/vendor/bpmn/zeebe.json`), while the DMN editor includes `camunda-dmn-moddle` and a Camunda properties provider. The compiler reads selected `zeebe:` elements and converts their values into atlas's immutable compiled process. Examples, conformance fixtures, tests and deployed definitions contain the Zeebe XML namespace.

The third-party names and interchange elements are not themselves proof of unlawful copying. Their provenance and licences must be evaluated separately from technical independence. `docs/legal/PROVENANCE_AUDIT.md` is an evidence register, not a legal sign-off.

ADR-0176 establishes a standards/compatibility/atlas-extension boundary, and ADR-0013/ADR-0062 establish vendored editors. Making atlas-owned BPMN extensions the default *changes authoring and interchange policy*; it must not silently rewrite existing models or repurpose third-party namespaces.

## Decision drivers

- Preserve BPMN/DMN interoperability where it is useful and lawful.
- Make new atlas-specific execution metadata independently owned and versionable.
- Keep deployed source, version history, model round-trips and restart recovery intact.
- Do not interpret vendor-specific formats on the runtime processor hot path.
- Avoid accidental interpretation of an unknown namespace as an atlas extension.
- Preserve required upstream licence notices and visible bpmn.io watermark.

## Considered options

1. Keep `zeebe:` as the default authoring format and improve only legal notices.
2. Globally replace `zeebe:`/`camunda:` tokens with `atlas:` and delete vendor descriptors.
3. Introduce native `atlas:` authoring as the default, retaining an explicit, tested, versioned import/round-trip compatibility path.

## Proposed decision

Prefer **option 3**, subject to acceptance after the provenance inventory and a compatibility matrix.

1. **Native authoring.** Newly authored BPMN models use atlas-owned extension definitions in the existing `http://atlas/schema/1.0` namespace, subject to a reviewed extension catalogue. Do not rename existing atlas elements or introduce collisions. Standard BPMN constructs take precedence wherever they can express the same meaning.
2. **Compatibility boundary.** Imported Zeebe BPMN stays legible and deployable. A deploy-time adapter maps *explicitly supported, namespace-qualified* vendor elements to the same compiled structures as native elements. Unsupported extensions are reported, not silently reinterpreted. The external namespace is not rebranded as project-owned.
3. **Model fidelity.** A source model retains its original XML dialect, unknown metadata and version history during open/save/export unless the author explicitly requests conversion. Conversion must offer a preview/report of unsupported or lossy constructs; never silently rewrite a deployed or stored source.
4. **Recovery.** Previously deployed definitions must still recompile at startup and preserve execution semantics. New deployment validation must not turn legacy valid models into a server startup failure (ADR-0177 and ADR-0393). The engine, WAL, event record and `applyToState` remain unchanged.
5. **DMN is separate.** Replacing the Camunda-specific DMN properties provider and descriptor is its own reviewed slice; preserve any supported version-tag semantics and DMN round-trip fidelity. DMN execution stays in `pblumer/temis`.
6. **Third-party obligations remain.** Keep the legitimate bpmn.io libraries, watermark, licence texts, source/version records and notices even if vendor-specific extensions are removed. Historical comparisons, trademarks and attribution remain accurate.
7. **Compatibility contract.** Publish a versioned matrix identifying implemented standard BPMN/DMN features, native atlas extensions, intentionally supported Zeebe/Camunda import fields and unsupported features. Do not claim engine/API drop-in compatibility.

### Invariants and implementation boundaries

- Parse and normalize XML at **deployment**, not per command (I1, I5).
- Do not add a second execution path or a separate live/replay implementation (I2–I4, I6).
- The compiled graph and existing job, task, timer, message and decision semantics remain the runtime contract.
- Where a previously accepted ADR explicitly requires the vendor-specific authoring format, supersede that *portion* with a subsequent accepted decision; historical ADRs are not retroactively edited.

### Required acceptance tests before a native-format rollout

- Table-driven parser cases for standard BPMN, native atlas, Zeebe-compatible and *unknown/incorrect namespace* extensions; check conflicts and precedence.
- Modeler import/save/reopen/export and BPMN-DI/extension round trips for existing fixtures.
- Freshly authored native BPMN executes the same representative job, script, I/O mapping, user task, call activity and DMN workflows as corresponding existing Zeebe examples.
- Deploy, run, persist, restart, replay and compare state for both dialects; include a legacy stored-definition restart.
- Conformance fixtures and release assets are only migrated with deliberate test review; no global string replacement.
- Visual bpmn.io watermark and third-party notices stay present in shipped artifacts.

### Consequences

- **Positive:** native model ownership without losing useful import capabilities; a single runtime.
- **Negative:** two authoring/import dialects need compatibility tests and maintenance; native descriptor and property-panel work.
- **Risks:** XML local-name matching in `compiler/parse.go` currently discards namespace ownership in places; that must be tightened without breaking legacy restart. The completeness of imported extension fidelity is not yet known.

## Pros and cons of the options

### Option 1 — keep Zeebe as canonical

- Good: smallest migration and testing burden.
- Bad: atlas remains tied to a vendor's model authoring vocabulary.

### Option 2 — global replacement

- Good: fewer vendor names in new source files.
- Bad: breaks existing stored models, loses provenance and may corrupt model semantics; unacceptable.

### Option 3 — native default and explicit compatibility

- Good: clear independent authoring contract and migration without runtime duplication.
- Bad: requires a versioned compatibility matrix, two descriptors during transition and regression coverage.

## Links

- [ADR-0176](0176-standards-boundary-and-runtime-contract.md) — existing standards/runtime boundary
- [ADR-0013](0013-embed-bpmn-js-modeler.md) and [ADR-0062](0062-embedded-dmn-editor.md) — editor decisions
- [ADR-0068](0068-task-io-variable-mappings.md) — adopted Zeebe I/O semantics
- [Programme plan](../planning/model-independence-and-provenance-program.md)
- [Initial evidence register](../legal/PROVENANCE_AUDIT.md)
