# Third-party notices

This is the central register for third-party software and assets distributed with the project.

> [!WARNING]
> The register is not yet complete. Completion and verification are release blockers. See [`docs/legal/REMEDIATION_PLAN.md`](docs/legal/REMEDIATION_PLAN.md).

## bpmn.io ecosystem

The web interface intentionally embeds components such as `bpmn-js`, `bpmn-moddle`, `diagram-js`, properties-panel packages and `form-js` / `@bpmn-io/form-js-editor`.

These components retain their own licence terms and copyright notices. Their use is not covered solely by this project's AGPL-3.0-only licence. Applicable bpmn.io terms may require preservation and visibility of the bpmn.io watermark; the project must not remove, change, hide or obstruct it where required.

Before release, record for every embedded package and transitive bundle component:

- exact name and version/revision;
- canonical source URL;
- copyright holder;
- complete applicable licence or bundled licence-file location;
- required notice, attribution, watermark, source-offer and redistribution conditions.

Upstream references:

- <https://github.com/bpmn-io/bpmn-js>
- <https://github.com/bpmn-io/form-js>

## Versioned moddle-extension notices

- `zeebe-bpmn-moddle` **1.17.0**: the bundled BPMN `zeebe.json` descriptor is byte-identical to the [tagged upstream resource](https://github.com/camunda/zeebe-bpmn-moddle/blob/v1.17.0/resources/zeebe.json). Its MIT notice and copyright are retained in [ZEEBE-BPMN-MODDLE-LICENSE](api/web/vendor/bpmn/ZEEBE-BPMN-MODDLE-LICENSE), separately from the bpmn.io licence.
- `camunda-dmn-moddle` **1.3.1**: integrated into the prebuilt DMN editor; its MIT copyright and permission notice is retained in [CAMUNDA-DMN-MODDLE-LICENSE](api/web/vendor/dmn/CAMUNDA-DMN-MODDLE-LICENSE). Source identification and bundle inclusion do **not** yet constitute an audited rebuild.

## DMN editor fork and npm dependency evidence

- `pblumer/dmn-js`, commit [`481cc5a`](https://github.com/pblumer/dmn-js/tree/481cc5a02ffa855689b59d81181478b84b25f9a5): its original bpmn.io licence with the required watermark condition is retained at [DMN LICENSE](api/web/vendor/dmn/LICENSE).
- `pblumer/dmn-moddle`, commit [`ac76928`](https://github.com/pblumer/dmn-moddle/tree/ac76928df99382c36833d9300fd98acc604dc673): its independent MIT copyright/permission notice is retained at [DMN-MODDLE-LICENSE](api/web/vendor/dmn/DMN-MODDLE-LICENSE).
- The DMN vendor build's npm lockfile records **68** package entries declaring 67 MIT and 1 ISC (`semver@7.8.5`). This is a **build dependency metadata inventory**, not a verified list of bundled/shipped modules or complete licence notices.

All other bundled dependencies and transitive notices remain subject to the incomplete inventory and release checks.

## Go modules and other assets

Dependencies in `go.mod` and `go.sum`, and vendored JavaScript, CSS, fonts, icons, images, schemas and generated bundles, retain their respective licences. Required upstream `NOTICE` and attribution files must be preserved.

An automated licence report and SPDX or CycloneDX SBOM must be generated and reviewed before release.

## Camunda and Zeebe interoperability

The project intentionally supports selected `zeebe:` XML extensions. Camunda and Zeebe names, schemas and public behavioural descriptions are not project-owned marks.

No Camunda **engine implementation** is declared as a Go dependency. The separately licensed Camunda-maintained moddle descriptors listed above **are** third-party source material distributed as a file or bundled resource. The [initial provenance register](docs/legal/PROVENANCE_AUDIT.md) identifies the present dependencies and unresolved evidence. A completed, independently verified audit must establish that implementation code, tests and documentation are independently developed or used under compatible terms.

## Adding third-party material

Every pull request adding or updating a dependency, generated bundle, font, icon, image, schema or copied source file must record:

1. component and exact version/revision;
2. canonical source;
3. applicable licence;
4. required copyright, attribution and notice text;
5. whether modified source, object code or generated files are distributed;
6. any UI attribution, watermark, source-offer or redistribution condition.
