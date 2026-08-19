# Third-party notices

This is the central register for third-party software and assets distributed with the project.

> [!WARNING]
> The register is not yet complete. Completion and verification are release blockers. See [`docs/legal/REMEDIATION_PLAN.md`](docs/legal/REMEDIATION_PLAN.md).

## bpmn.io ecosystem

The web interface intentionally embeds components such as `bpmn-js`, `bpmn-moddle`, `diagram-js`, properties-panel packages and `form-js` / `@bpmn-io/form-js-editor`.

These components retain their own licence terms and copyright notices. Their use is not covered solely by this project's Apache-2.0 licence. Applicable bpmn.io terms may require preservation and visibility of the bpmn.io watermark; the project must not remove, change, hide or obstruct it where required.

Before release, record for every embedded package and transitive bundle component:

- exact name and version/revision;
- canonical source URL;
- copyright holder;
- complete applicable licence or bundled licence-file location;
- required notice, attribution, watermark, source-offer and redistribution conditions.

Upstream references:

- <https://github.com/bpmn-io/bpmn-js>
- <https://github.com/bpmn-io/form-js>

## Go modules and other assets

Dependencies in `go.mod` and `go.sum`, and vendored JavaScript, CSS, fonts, icons, images, schemas and generated bundles, retain their respective licences. Required upstream `NOTICE` and attribution files must be preserved.

An automated licence report and SPDX or CycloneDX SBOM must be generated and reviewed before release.

## Camunda and Zeebe interoperability

The project intentionally supports selected `zeebe:` XML extensions. Camunda and Zeebe names, schemas and public behavioural descriptions are not project-owned marks.

No Camunda source code is intentionally declared here as a distributed dependency. A separate provenance audit must verify that implementation code, tests and documentation are independently developed or used under compatible terms.

## Adding third-party material

Every pull request adding or updating a dependency, generated bundle, font, icon, image, schema or copied source file must record:

1. component and exact version/revision;
2. canonical source;
3. applicable licence;
4. required copyright, attribution and notice text;
5. whether modified source, object code or generated files are distributed;
6. any UI attribution, watermark, source-offer or redistribution condition.
