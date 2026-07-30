# Intellectual-property and licence review

**Review date:** 29 July 2026  
**Status:** Initial maintainer assessment

> [!IMPORTANT]
> This is a technical risk assessment, not legal advice and not a finding that an infringement has occurred.

## Executive decision

The current product name **Atlas** is treated as a temporary development name and a release blocker. No public or commercial release should use `Atlas`, `Atlas Engine`, `Atlas Workflow` or `Atlas BPMN Engine` unless the name has been professionally cleared for the intended territories and goods/services. The preferred mitigation is a rename before release.

## Findings

| ID | Area | Risk | Finding | Required response |
|---|---|---:|---|---|
| IP-01 | Product name | **High** | `ATLAS` is used as a mark by the OMG, which also controls BPMN/DMN marks; Apache Atlas and other enterprise-software products use the same name. | Rename or obtain documented professional clearance. |
| IP-02 | BPMN/DMN terminology | Medium | Standards names are intentionally used descriptively, but wording must not imply certification, ownership, affiliation or endorsement. | Add trade-mark and no-affiliation notices; do not use third-party logos without permission. |
| IP-03 | bpmn.io components | **High if unmet** | Embedded `bpmn-js`/`form-js` components carry their own notice and watermark obligations. | Preserve notices and required watermark visibility; record exact versions. |
| IP-04 | Camunda/Zeebe proximity | Medium–high | The project deliberately supports `zeebe:` extensions and uses similar architectural terminology. Ideas and interoperability are not automatically infringement, but copied or closely translated code, tests or documentation may create licence/copyright risk. | Complete a documented provenance audit and replace uncertain material. |
| IP-05 | Vendored web assets | High if undocumented | Generated JS/CSS bundles, schemas, icons and fonts may contain third-party components not visible in `go.mod`. | Inventory, preserve notices and generate an SBOM. |
| IP-06 | Project licence wording | Low | Apache-2.0 exists, while repository wording may still describe it as provisional. | State the project licence unambiguously and distinguish third-party licences. |
| IP-07 | Patent freedom to operate | Unknown | Apache-2.0 is not a general freedom-to-operate opinion. | Consider targeted professional review before substantial commercial deployment. |
| IP-08 | AI-assisted work | Medium | AI assistance does not establish provenance or licence compatibility. | Require source disclosure and human review of generated contributions. |

## Deliberate compatibility choices

- BPMN and DMN names are used only to identify the OMG standards implemented or consumed.
- Selected `zeebe:` XML extensions are intentionally supported for file-format interoperability. This does not imply Camunda affiliation, certification or full feature equivalence.
- bpmn.io software is intentionally embedded to provide modelling and form functionality, subject to its licence and watermark conditions.
- New project-owned XML extensions should use a project-controlled namespace after the rename.

## Release blockers

A public or commercial release is blocked until:

- [ ] product rename or documented professional trade-mark clearance;
- [ ] repository/module/binary/UI/namespace aligned to the cleared name;
- [ ] complete third-party inventory and exact bpmn.io versions recorded;
- [ ] required licence texts, notices and watermark behaviour verified;
- [ ] Camunda/Zeebe provenance audit completed with no unresolved items;
- [ ] Apache-2.0 wording clarified;
- [ ] SBOM generated for release artefacts;
- [ ] no-affiliation and trade-mark notices added to public surfaces.

See [REMEDIATION_PLAN.md](REMEDIATION_PLAN.md).

## External wording

Until rename, use wording such as:

> This is an independent, early-stage workflow-engine project implementing BPMN and DMN interoperability. The current project name is temporary and under legal review. The project is not affiliated with or endorsed by the Object Management Group, the Apache Software Foundation, Camunda or bpmn.io.

Avoid unsupported claims such as `official BPMN engine`, `OMG certified`, `drop-in Zeebe replacement`, or broad `Camunda-compatible` statements without an exact compatibility matrix.

## Sources consulted

- OMG trade-mark list: <https://www.omg.org/legal/tm_list.htm>
- OMG ATLAS specification: <https://www.omg.org/spec/ATLAS/1.0>
- Apache trade-mark policy: <https://www.apache.org/foundation/marks/>
- bpmn-js licence: <https://github.com/bpmn-io/bpmn-js/blob/main/LICENSE>
- Camunda repository licensing statement: <https://github.com/camunda/camunda#license>
- Swiss IGE guidance on conflict searches: <https://www.ige.ch/en/protecting-your-ip/trade-marks/before-you-apply/requirements-for-protection/risk-of-conflict>
