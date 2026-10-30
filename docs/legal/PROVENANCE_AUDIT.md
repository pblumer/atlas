# Camunda/Zeebe provenance audit — evidence register

**Baseline:** 2026-10-30, `pblumer/atlas` default branch (`main`).  
**Status:** **In progress — NOT a completed provenance or legal audit.**  
**Scope:** Current repository and declared upstream component inventory; not a comparison of every implementation against upstream sources.  
**Review standard:** [remediation plan](REMEDIATION_PLAN.md), section 3.

## What this audit does and does not establish

A vendor name, schema URI, compatible model format or architectural similarity is not proof of copying or infringement. Conversely, declaring a component MIT-licensed or calling code independent does **not** establish the provenance of every file. A future reviewer must compare upstream revisions, licence texts, committed changes and redistributed bundles and record evidence.

The original atlas source and third-party software have different licensing obligations. The current repository `LICENSE` and `README.md` specify **AGPL-3.0-only**, not Apache-2.0. Some earlier legal notes incorrectly describe the present project's licence as Apache-2.0 and need reconciliation.

## Initial subsystem inventory

| Surface | Repository evidence | Present use | Provenance status | Remaining evidence |
| --- | --- | --- | --- | --- |
| Go module dependencies | `go.mod` | No direct declared Camunda/Zeebe Go module; `pblumer/feel` and `pblumer/temis` are used | Direct-dependency fact checked; transitive/component licensing **not cleared** | Complete SBOM and transitive licence review |
| BPMN editor | `api/web/vendor/bpmn/README.md`; `docs/adr/0013-embed-bpmn-js-modeler.md` | Bundled `bpmn-js` 18.21.0; own modeler UI | **Needs independent verification** | Confirm upstream archive hash, licence text, watermark and distributable build contents |
| Zeebe BPMN descriptor | `api/web/vendor/bpmn/zeebe.json` and `README.md` | Declared source `zeebe-bpmn-moddle` 1.17.0, identified as MIT in vendor record; `zeebe:` namespace | **Needs independent verification** | Compare exact descriptor with published upstream revision, confirm licence/attribution, document any modification |
| DMN editor bundle | `api/web/vendor/dmn/ATLAS-VENDORED.txt`; `api/web/vendor/dmn/src/package.json` | `pblumer/dmn-js` and `pblumer/dmn-moddle` pinned forks, `dmn-js-properties-panel`, `camunda-dmn-moddle` 1.3.1 | **Needs independent verification** | Validate fork ancestry, upstream package licences, bundled/transitive modules, copyright and rebuild correspondence |
| DMN editing integration | `api/web/dmn-editor.js` and `api/web/vendor/dmn/src/index.js` | `CamundaPropertiesProviderModule`, `CamundaModdleDescriptor`, `camunda:versionTag` | **Needs independent verification** | Establish source/licence of panel implementation and whether atlas-owned replacement preserves version-tag authoring |
| BPMN compiler and graph | `compiler/parse.go`; `compiler/process.go`; `compiler/builder.go` | Compiles `zeebe:taskDefinition`, `zeebe:ioMapping`, `zeebe:calledElement` and related extensions into atlas structures | **Unresolved implementation provenance** | Review authorship/commit history and relevant upstream code; determine independently developed versus derived material |
| Runtime terminology and semantics | `engine/scope.go`; `engine/behavior.go`; `docs/adr/0068-task-io-variable-mappings.md` | Camunda/Zeebe compatibility concepts, incidents and variable propagation | **Unresolved implementation provenance** | Compare source provenance, not merely design terminology; examine tests and external references |
| Example and test models | `examples/`; `conformance/`; `e2e/`; `api/systemprocesses/`; compiler/engine tests | Numerous BPMN models declare `xmlns:zeebe` and `zeebe:` extensions | **Fixture provenance unverified** | Trace authored vs copied cases, distinguish standard BPMN TCK material, retain licences and test legacy import/recovery |
| Product/legal wording | `TRADEMARKS.md`; `THIRD_PARTY_NOTICES.md`; `docs/legal/`; `ROADMAP.md` | Names are used descriptively; non-affiliation notice exists | **Partially documented** | Complete legal review, align AGPL wording, check claims and release artefacts |

## Key verified implementation observations

- `compiler/parse.go` documents that `encoding/xml` matches certain XML elements by **local name** rather than by namespace. This currently helps parse alternative prefixes but is not a safe standalone validation strategy for vendor/atlas dialect separation. It must not be changed without legacy stored-definition/recovery tests.
- The BPMN editor's `zeebe.json` is a vendored moddle descriptor, not an engine dependency.
- The DMN editor actually installs the Camunda-specific properties provider and descriptor. Removing an unused word would not remove that integration.
- `docs/runtime-contract.md` and ADR-0176 explicitly classify `zeebe:` as a compatibility extension and do **not** assert drop-in Camunda/Zeebe runtime compatibility.
- The current project licence is AGPL-3.0-only per root `LICENSE` and `README.md`. Earlier Apache-2.0 statements were historical and must not be treated as the current licence.

## Review protocol for each unresolved item

1. Record repository path and SHA, author/commit history, upstream project and **exact revision**.
2. Obtain the applicable licence and any required copyright, NOTICE, source-offer and watermark obligations.
3. Compare material against upstream sources (not just names), record similarities/differences and whether code, fixtures or text are copied, adapted, independently authored or AI-assisted.
4. Classify **independent**, **standards/interoperability-driven**, **compatibly derived**, **uncertain** or **incompatible**, with an evidence link and reviewer.
5. Rewrite, replace, relicense or seek permission for any uncertain/incompatible material before sign-off.
6. Verify the shipped JS/CSS/fonts/schemas and generated bundles, not just `go.mod`; generate and retain an SPDX/CycloneDX SBOM.

**Completion gate:** no unresolved uncertain/incompatible material, independently checked bundled components and obligations, documented reviewer/date/decision and verified release artefacts. This document currently does **not** meet that gate.

## Related work

- [IP and licence review](IP_AND_LICENSE_REVIEW.md)
- [Remediation plan](REMEDIATION_PLAN.md)
- [Native dialect decision proposal](../adr/0513-native-model-dialect-and-vendor-compatibility.md)
- [Model independence programme](../planning/model-independence-and-provenance-program.md)
