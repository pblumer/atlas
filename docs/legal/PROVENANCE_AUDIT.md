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
| Zeebe BPMN descriptor | `api/web/vendor/bpmn/zeebe.json`, `README.md`, `ZEEBE-BPMN-MODDLE-LICENSE` | `zeebe-bpmn-moddle` v1.17.0, MIT, original `zeebe:` namespace | **Exact upstream descriptor content and licence verified** (see below) | Review distribution coverage and ensure required notice reaches every released bundle |
| DMN editor bundle | `api/web/vendor/dmn/ATLAS-VENDORED.txt`; `api/web/vendor/dmn/src/package-lock.json`; `LICENSE`, `DMN-MODDLE-LICENSE`, `CAMUNDA-DMN-MODDLE-LICENSE` | Exact pinned `pblumer/dmn-js`/`pblumer/dmn-moddle` commits, 68 npm lock entries, bundled `camunda-dmn-moddle` 1.3.1 | **Pinned fork licences verified**; 68 lockfile licence declarations inventoried; assembled bundle **unverified** | Validate fork ancestry, licence completeness of **shipped bundle**, rebuild reproducibility and watermark/notice display |
| DMN editing integration | `api/web/dmn-editor.js` and `api/web/vendor/dmn/src/index.js` | `CamundaPropertiesProviderModule`, `CamundaModdleDescriptor`, `camunda:versionTag` | **Needs independent verification** | Establish source/licence of panel implementation and whether atlas-owned replacement preserves version-tag authoring |
| BPMN compiler and graph | `compiler/parse.go`; `compiler/process.go`; `compiler/builder.go` | Compiles `zeebe:taskDefinition`, `zeebe:ioMapping`, `zeebe:calledElement` and related extensions into atlas structures | **Unresolved implementation provenance** | Review authorship/commit history and relevant upstream code; determine independently developed versus derived material |
| Runtime terminology and semantics | `engine/scope.go`; `engine/behavior.go`; `docs/adr/0068-task-io-variable-mappings.md` | Camunda/Zeebe compatibility concepts, incidents and variable propagation | **Unresolved implementation provenance** | Compare source provenance, not merely design terminology; examine tests and external references |
| Example and test models | `examples/`; `conformance/`; `e2e/`; `api/systemprocesses/`; compiler/engine tests | Numerous BPMN models declare `xmlns:zeebe` and `zeebe:` extensions | **Fixture provenance unverified** | Trace authored vs copied cases, distinguish standard BPMN TCK material, retain licences and test legacy import/recovery |
| Product/legal wording | `TRADEMARKS.md`; `THIRD_PARTY_NOTICES.md`; `docs/legal/`; `ROADMAP.md` | Names are used descriptively; non-affiliation notice exists | **Partially documented** | Complete legal review, align AGPL wording, check claims and release artefacts |

## Key verified implementation observations

- `compiler/parse.go` documents that `encoding/xml` matches certain XML elements by **local name** rather than by namespace. This currently helps parse alternative prefixes but is not a safe standalone validation strategy for vendor/atlas dialect separation. It must not be changed without legacy stored-definition/recovery tests.
- The BPMN editor's `zeebe.json` is a vendored moddle descriptor, not an engine dependency. The checked v1.17.0 file is byte-identical to the tagged MIT-licensed upstream source; its own licence is preserved separately.
- The DMN editor actually installs the Camunda-specific properties provider and descriptor. Removing an unused word would not remove that integration.
- `docs/runtime-contract.md` and ADR-0176 explicitly classify `zeebe:` as a compatibility extension and do **not** assert drop-in Camunda/Zeebe runtime compatibility.
- The current project licence is AGPL-3.0-only per root `LICENSE` and `README.md`. Earlier Apache-2.0 statements were historical and must not be treated as the current licence.

## Independently checked tagged upstream evidence (2026-10-30)

The following are **content-level source checks**, not legal clearance. Git blob IDs use Git's `sha1("blob " + byte_length + "\\0" + file_bytes)` scheme, and are compared against exact GitHub upstream tags.

| File | Tagged upstream | Vendored/verified source blob | Licence |
| --- | --- | --- | --- |
| `api/web/vendor/bpmn/zeebe.json` | [camunda/zeebe-bpmn-moddle v1.17.0 resources/zeebe.json](https://github.com/camunda/zeebe-bpmn-moddle/blob/v1.17.0/resources/zeebe.json) | **Identical file**; `1a73ad8f64c6a3c6a2321f8765062ba429f3a38d` | [Upstream MIT](https://github.com/camunda/zeebe-bpmn-moddle/blob/v1.17.0/LICENSE), verbatim in `api/web/vendor/bpmn/ZEEBE-BPMN-MODDLE-LICENSE`, blob `f6d5667d451a6ecc20e941e8a26f5781933f9c7f` |
| `api/web/vendor/bpmn/LICENSE` | [bpmn-io/bpmn-js v18.21.0 LICENSE](https://github.com/bpmn-io/bpmn-js/blob/v18.21.0/LICENSE) | **Identical file**; `b20e936ddbc8af301806c0d1cd526b50802c8195` | bpmn.io terms **including watermark**; separate from Zeebe descriptor licence |
| `camunda-dmn-moddle` bundled descriptor | [camunda/camunda-dmn-moddle v1.3.1](https://github.com/camunda/camunda-dmn-moddle/tree/v1.3.1) | `resources/camunda.json` upstream blob `36eb57aefabf9aa7c0123731395337dee130a99b`; **not independently extract-compared against built JS** | [Upstream MIT](https://github.com/camunda/camunda-dmn-moddle/blob/v1.3.1/LICENSE), verbatim in `api/web/vendor/dmn/CAMUNDA-DMN-MODDLE-LICENSE`, blob `49c025e38d8340f3e30d24111e7ad3f071d71b5e` |

### DMN fork and lockfile evidence (2026-10-30)

- **[pblumer/dmn-js](https://github.com/pblumer/dmn-js/tree/481cc5a02ffa855689b59d81181478b84b25f9a5)** at commit `481cc5a02ffa855689b59d81181478b84b25f9a5`: the repository's exact `LICENSE` content has Git blob `c6786fe7f8afb91b456f3009ae5d69bb9e387402`, byte-identical with `api/web/vendor/dmn/LICENSE`. The terms include the bpmn.io watermark condition. The referenced `packages/dmn-js-shared/package.json` pins `dmn-moddle` to the commit below.
- **[pblumer/dmn-moddle](https://github.com/pblumer/dmn-moddle/tree/ac76928df99382c36833d9300fd98acc604dc673)** at commit `ac76928df99382c36833d9300fd98acc604dc673`: its original `LICENSE` is MIT, Git blob `e0a77fed0e410060b20cae9e1f2ec9eccc5618dc`. A byte-identical copy is retained at `api/web/vendor/dmn/DMN-MODDLE-LICENSE`; this is distinct from the modified dmn-js watermark licence.
- The committed **`api/web/vendor/dmn/src/package-lock.json`** has **68 `node_modules/` entries**: 67 declare MIT, and `semver@7.8.5` declares ISC. Each records a version, resolved package URL and integrity field. The Go regression test pins this inventory and refuses missing or unexpected licence metadata until deliberately reviewed.

**Evidence boundary:** The two exact fork commits and the lockfile metadata are verified as *declared build inputs*, not audited equality of every fork source file with its bpmn.io ancestor, not proof that all lockfile entries actually ship in `dmn-modeler.js`, and not a complete distribution SBOM. In particular `--legal-comments=none` in the documented esbuild command makes retained external licence/NOTICE availability and a release review important. The compiled bundle and CSS/icon assets still require third-party material analysis, reproducible rebuilding, copyright/notice checks and real watermark verification.

The descriptor and exact MIT licence bytes for v1.17.0 have been compared using GitHub's committed file hashes and content; no modifications were found. The DMN manifest and `src/package.json` pin v1.3.1, but bundling means the upstream descriptor identity alone does not prove the resulting bundle is identical to any upstream artifact. The vendored `bpmn-js` `LICENSE` is also byte-identical with v18.21.0; the bundled JavaScript and its watermark display still need runtime verification.

The tests in `api/vendor_provenance_internal_test.go` pin the upstream moddle licence texts, the pinned DMN fork licences, the DMN npm lockfile licence metadata and the untouched Zeebe descriptor to these upstream blob identities; the existing `TestVendoredBundlesMatchTheirRecordedChecksums` checks recorded JS bundle checksums, not their legal completeness.

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
