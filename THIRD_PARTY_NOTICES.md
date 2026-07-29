# Third-party notices

This file is the central register for third-party software and assets distributed with this project.

> [!WARNING]
> This register is not yet complete. Completion and verification are release blockers. See [`docs/legal/REMEDIATION_PLAN.md`](docs/legal/REMEDIATION_PLAN.md).

## bpmn.io components

The web interface intentionally embeds software from the bpmn.io ecosystem, including BPMN and form rendering/editing components.

Known or expected components include:

- `bpmn-js`
- `bpmn-moddle`
- `diagram-js`
- bpmn.io properties-panel packages
- `form-js` / `@bpmn-io/form-js-editor`

These components retain their own licence terms and copyright notices. Their use is not covered solely by this project's Apache-2.0 licence.

For bpmn.io diagram-rendering components, the applicable licence includes conditions concerning preservation and visibility of the bpmn.io watermark. The project must not remove, modify, hide or obstruct the watermark where the licence requires it.

Before release, this section must be expanded with:

- exact package and bundle versions;
- source repository and release URL;
- copyright holder;
- complete applicable licence text or bundled licence-file location;
- transitive packages included in generated JavaScript/CSS bundles;
- verification of required attribution and watermark behaviour.

Upstream references:

- <https://github.com/bpmn-io/bpmn-js>
- <https://github.com/bpmn-io/form-js>

## Go modules

Go module dependencies are declared in `go.mod` and `go.sum`. Their licences remain applicable to their respective components.

Before release, an automated licence report and an SPDX or CycloneDX SBOM must be generated and reviewed. Required upstream `NOTICE` files and attribution statements must be preserved in distributions.

## Camunda and Zeebe compatibility material

The project supports selected `zeebe:` XML extensions and behaviour for interoperability. Camunda and Zeebe names, schemas or public behavioural descriptions are not licensed as project-owned marks.

No Camunda source code is intentionally distributed as a third-party component under this notice. A separate provenance audit is required to verify that implementation code, tests and documentation were independently developed or otherwise used under compatible terms.

See:

- [`docs/legal/IP_AND_LICENSE_REVIEW.md`](docs/legal/IP_AND_LICENSE_REVIEW.md)
- [`docs/legal/REMEDIATION_PLAN.md`](docs/legal/REMEDIATION_PLAN.md)

## Adding a dependency or vendored asset

Every pull request adding or updating a distributed dependency, generated bundle, font, icon, image, schema or copied source file must record:

1. component name and exact version or revision;
2. canonical source location;
3. applicable licence;
4. required copyright, attribution and `NOTICE` text;
5. whether source code, object code, generated files or modified copies are distributed;
6. any UI attribution, watermark, source-offer or redistribution condition.
