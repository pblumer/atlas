# Model independence and provenance (MIP) delivery programme

**Programme ID:** MIP.  
**Baseline:** Public `pblumer/atlas` main, reviewed 2026-10-30.  
**Strategic tracks:** Roadmap Milestone 6 (interoperability), Milestone A (Modeler), Milestone 1 (compiler); legal/IP release-readiness gate.  
**Owner:** pblumer, repository maintainer (review/merge responsibility).  
**Architecture authority:** [ADR-0176](../adr/0176-standards-boundary-and-runtime-contract.md) (Accepted/Landed); [ADR-draft-native-model-dialect-and-vendor-compatibility](../adr/draft-native-model-dialect-and-vendor-compatibility.md) (Proposed/Not started).  
**Operational status:** [programme manifest](../programs/mip/program.json). The technical migration is not approved or implemented by this plan.

## Intended outcome

A newly authored BPMN process expresses atlas-owned executable extensions using the atlas namespace, survives Modeler save/export/reopen and executes durably. An existing Zeebe-dialect BPMN process still imports, runs and reloads after restart without silent data loss or altered semantics. The DMN editor does not require Camunda-specific authoring components once an independently reviewed replacement is available. Every redistributed third-party dependency and authored/derived code area has documented licence and provenance evidence.

This is a **compatibility and independence** programme, not a promise to remove the words Camunda/Zeebe from legitimate third-party notices or historical explanations.

## Existing implementations and gaps

| Area | Implemented on main | Gap |
| --- | --- | --- |
| Runtime | Go engine, immutable graph, single-writer partitions, deterministic WAL replay | No runtime rewrite is needed; tests must prove semantic equivalence |
| BPMN input | Compiler accepts supported Zeebe-style extensions; namespace-local-name parser in places | Atlas-owned default authoring contract and explicit namespace validation |
| BPMN authoring | `bpmn-js` with vendored `zeebe.json` | A compatible atlas moddle descriptor, native default property binding and lossless legacy round-trip |
| DMN authoring | `dmn-js` bundle with Camunda moddle/properties provider | Own or standard DMN properties for required version tags while keeping temis execution |
| Legal | Initial IP risk/remediation plan, trademark and third-party notices | Detailed provenance evidence, licence reconciliation, SBOM and verification of shipped bundles |
| Tests | Compiler, engine, UI, conformance and E2E suites with Zeebe fixtures | Dual-dialect regression corpus, namespace-negative tests and explicit legacy reload/replay |

## Slices and gates

### MIP0 — Provenance baseline and architecture gate

**Current PR: documentation/coordination only.** Register known components, what is verified and what is not; record the current root AGPL-3.0-only licence; propose a native authoring/compatibility ADR; publish milestones and independent acceptance gates.

**Exit gate:** Proposed ADR reviewed and explicitly accepted before any code change that alters canonical BPMN authoring. Complete the provenance audit with upstream revision comparisons; a first inventory is **not** a legal clearance. This PR does not complete MIP0.

### MIP1 — Native BPMN authoring, tested against old models

**Depends on:** MIP0 decision acceptance and approved atlas-extension catalogue.

- Test-first: new BPMN model uses a namespace-correct atlas extension and round-trips through Modeler/export; existing `zeebe:` models retain their exact supported semantics and attributes.
- Define the atlas moddle schema and update authoring/property tools only for supported executable metadata. Preserve all required bpmn.io assets and watermark.
- Keep original imported XML dialect on save by default, with no silent conversion or dropped unknown extensions. Explicit conversion is a separate operation with a loss report.

**Gate:** representative new and imported BPMN files pass compiler, Modeler E2E and round-trip tests; no unsupported engine features invented.

### MIP2 — Compiler compatibility and stored-definition recovery

**Depends on:** approved compatibility contract and MIP1's schema/round-trip evidence.

- Table-driven deployment validation for native, legacy, bad-namespace and conflicting extension fields.
- One compiled graph and one runtime; no dual job/variable/incident execution implementations.
- Legacy deployment reload must not become fatal due to new parser validation. Preserve original source and version history; do not rewrite historical deployments.
- Recompile after simulated restart, replay WAL and compare live/replayed state for each representative BPMN dialect, including jobs, mappings, user tasks, call activities and decisions.

**Gate:** no runtime-visible difference for the supported mapped semantics, no hot-path allocations or changes to post-fsync ordering.

### MIP3 — Remove Camunda-specific DMN authoring dependency

**Depends on:** MIP0 provenance review; can follow MIP2 separately to limit conflicts.

- Identify which DRD/decision properties are actually relied upon, especially `camunda:versionTag`.
- Replace only the Camunda-specific provider/descriptor with supported standard DMN or atlas-owned authoring fields, with backwards-compatible import of existing models.
- Rebuild pinned vendor bundle from a documented, reproducible process and verify its dependency licences and notices.

**Gate:** DMN open/save/reopen, version resolution, decision service/table execution and legacy models pass; `pblumer/temis` stays authoritative for FEEL/DMN.

### MIP4 — Release provenance and compliance gate

**Depends on:** MIP1–MIP3 and completed evidence.

- Complete `docs/legal/PROVENANCE_AUDIT.md` with reviewed upstream comparisons, authors and decisions.
- Produce SBOMs for source and distributed binary/browser assets; verify licence, notice, watermark and no-affiliation behaviour.
- Publish a versioned BPMN/DMN compatibility matrix and a documented conversion/rollback path.

**Gate:** release review can distinguish verified rights and independently supported semantics from open risks. Legal advice and trade-mark clearance remain independent gates.

## Architecture and safety requirements

1. No XML parsing, new allocations or namespace interpretation in the command hot path (I1, I5).
2. No external effects before WAL `fsync`; no single-writer violations (I2, I3).
3. `applyToState` remains the only deterministic live/replay materializer; generated keys/times remain facts in events (I4, I6).
4. Changing dialects affects model bytes and authoring UX, **not** event/WAL format or compiled-runtime semantics.
5. Follow `AGENTS.md` TDD, including failed tests first, then minimum implementation, recovery tests and the full Go build/test/race/vet/gofmt gates. Review E2E and vendor regeneration separately.
6. Keep third-party copyright and licence records; reducing grep hits is not a valid acceptance criterion.

## Immediate next action and explicit exclusions

1. Review the [initial evidence register](../legal/PROVENANCE_AUDIT.md) and complete its upstream comparisons; prioritise shipped JavaScript bundles, moddle descriptors, copied fixtures and compiler history.
2. Accept, revise or reject [ADR-draft-native-model-dialect-and-vendor-compatibility](../adr/draft-native-model-dialect-and-vendor-compatibility.md).
3. Only after these gates, start the first vertical TDD slice in MIP1.

**Not part of MIP0:** no compiler or Modeler behaviour changes; no renaming of the Go module, product, third-party software, public API or deployed BPMN; no legal clearance claim.
