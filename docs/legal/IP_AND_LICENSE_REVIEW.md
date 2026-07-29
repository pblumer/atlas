# Intellectual-property and licence review

**Review date:** 28 July 2026  
**Status:** Initial project-maintainer assessment  
**Applies to:** source code, documentation, product naming, embedded web assets and public distribution of this repository

> [!IMPORTANT]
> This document is a technical risk assessment, not legal advice and not a legal finding that any party's rights have been infringed. It records potential conflicts, confirmed licence obligations and the controls required before a public or commercial release.

## Executive decision

The current project name **Atlas** is treated as a **temporary development name** and a **release blocker**.

No release, commercial offering, hosted service or public marketing campaign should use **Atlas**, **Atlas Engine**, **Atlas Workflow** or **Atlas BPMN Engine** as the product mark until either:

1. a qualified trade-mark professional has cleared the name for the intended countries and goods/services; or
2. the project has been renamed to a name that has passed an appropriate clearance search.

The project may continue to use the repository during early development while the rename is prepared, provided that the temporary status is stated prominently and no claim of ownership in the Atlas name is made.

## Findings

| ID | Area | Risk | Finding | Required response |
|---|---|---:|---|---|
| IP-01 | Product name `Atlas` | **High** | `ATLAS™` is listed by the Object Management Group (OMG), which also owns or claims marks including `BPMN™`, `DMN™` and their expanded names. The Apache Software Foundation also operates Apache Atlas and treats its project names as trade marks. The identical name is already heavily used in enterprise software. | Rename before release, or obtain professional clearance and document the result. |
| IP-02 | BPMN and DMN terminology | Medium | BPMN and DMN are required standards terminology, but they are also OMG trade marks. Descriptive, nominative use is intentional; wording must not imply certification, ownership, affiliation or endorsement. | Add a trade-mark notice and use the names only to describe standards support. Do not use OMG logos without permission. |
| IP-03 | `bpmn-js` and `form-js` | **High if unmet** | The embedded bpmn.io components are intentionally used. Their licence requires preservation of the copyright/permission notice. For rendered diagrams, the source responsible for the bpmn.io watermark must not be removed or changed, and the watermark must remain fully visible and unobstructed. | Preserve the watermark, ship the licence text, record exact component versions and add a regression check before release. |
| IP-04 | Camunda/Zeebe concepts and compatibility | Medium to high | The design and history use Zeebe-compatible XML extensions and terminology such as records, value types, intents and element-instance lifecycle states. Similarity of ideas or interoperability is not by itself infringement, but copied or closely translated source code, tests or documentation could create a licence or copyright problem. Current Camunda core files are generally offered under the Camunda License 1.0, with specific exceptions. | Perform and record a source-provenance audit. Replace any unverified copied/derived implementation. Keep interoperability statements factual and include a no-affiliation notice. |
| IP-05 | Vendored web assets | High if undocumented | Bundled JavaScript, CSS, icons, fonts, moddle descriptors and generated artefacts may carry licences and notices that are not visible in `go.mod`. | Inventory every vendored asset, preserve required notices and generate an SBOM. |
| IP-06 | Repository licence wording | Low | The repository contains an Apache-2.0 licence, while the README still describes it as a proposed default. This is ambiguous for contributors and recipients. | Declare Apache-2.0 as the project licence, subject to third-party components retaining their own licences. |
| IP-07 | Patent freedom to operate | Unknown | Apache-2.0 grants patent rights only from contributors for claims necessarily infringed by their contributions. It is not a general freedom-to-operate opinion. | Consider a targeted patent review before substantial commercial deployment. |
| IP-08 | AI-assisted contributions | Medium | Commit history records AI-assisted work. AI assistance does not establish source provenance or licence compatibility. | Require contributors to confirm that submitted material is original or licence-compatible and to identify external source material used. |

## Deliberate and permitted compatibility choices

The following choices are intentional, subject to the controls in this repository:

### BPMN and DMN support

The project implements and describes support for OMG standards. `BPMN`, `Business Process Model and Notation`, `DMN` and `Decision Model and Notation` are used only as names of those standards. The project does not claim OMG certification or endorsement.

### Zeebe-compatible XML extensions

Support for selected `zeebe:` XML extensions is intended as an interoperability feature for user-authored BPMN files. Compatibility does not imply that the project is Camunda, Zeebe, sponsored by Camunda, or a drop-in replacement for every Camunda feature.

The namespace and compatible input format may be retained where technically useful. New project-owned extensions should use a project-owned namespace after the project rename.

### bpmn.io components

The project intentionally embeds bpmn.io software to avoid reimplementing BPMN and form rendering/editing. This choice is acceptable only while all applicable licence terms are observed, including the visible watermark obligation for rendered diagrams.

## Release blockers

A public or commercial release is blocked until all of the following are complete:

- [ ] Product rename or documented professional trade-mark clearance
- [ ] Repository, Go module, executable, UI, documentation and project-owned XML namespace aligned to the cleared name
- [ ] Complete third-party component inventory, including vendored browser assets
- [ ] Exact versions and source locations recorded for `bpmn-js`, `form-js` and related packages
- [ ] Required third-party licence texts and notices included in source and binary distributions
- [ ] Automated or documented verification that the bpmn.io watermark is visible and unobstructed
- [ ] Camunda/Zeebe source-provenance audit completed and signed off
- [ ] Any copied or uncertain code/documentation replaced, relicensed with permission, or otherwise brought into compliance
- [ ] Apache-2.0 project licensing wording made unambiguous
- [ ] SBOM generated for the release artefact
- [ ] Trade-mark and no-affiliation notice visible in the repository and relevant product surfaces

The detailed implementation plan is in [REMEDIATION_PLAN.md](REMEDIATION_PLAN.md).

## Required wording for external descriptions

Until the rename is complete, external descriptions should use wording similar to:

> This is an independent, early-stage workflow-engine project implementing BPMN and DMN interoperability. The current project name is temporary and under legal review. The project is not affiliated with or endorsed by the Object Management Group, the Apache Software Foundation or Camunda.

Avoid statements such as:

- "official BPMN engine"
- "OMG certified" unless certification has actually been obtained
- "Camunda-compatible" without immediately specifying the exact compatibility surface
- "drop-in Zeebe replacement"
- any statement suggesting ownership of the names BPMN, DMN, Zeebe, Camunda or Apache Atlas

## Sources consulted

The review used the following primary or authoritative sources as of 28 July 2026:

- OMG trade-mark list: <https://www.omg.org/legal/tm_list.htm>
- OMG ATLAS specification: <https://www.omg.org/spec/ATLAS/1.0>
- Apache Software Foundation trade-mark policy: <https://www.apache.org/foundation/marks/>
- Apache trade-mark listing: <https://www.apache.org/foundation/marks/list/>
- bpmn-js licence: <https://github.com/bpmn-io/bpmn-js/blob/main/LICENSE>
- Camunda repository licensing statement: <https://github.com/camunda/camunda#license>
- Swiss Federal Institute of Intellectual Property guidance on conflict searches: <https://www.ige.ch/en/protecting-your-ip/trade-marks/before-you-apply/requirements-for-protection/risk-of-conflict>

## Maintenance

This review must be updated when any of the following occurs:

- the project or repository is renamed;
- a new modelling, execution or UI dependency is embedded;
- a new compatibility claim is published;
- distribution changes from source-only to binaries, containers, hosted service or commercial support;
- a legal notice, cease-and-desist request or trade-mark objection is received;
- the applicable licence of an upstream dependency changes.
