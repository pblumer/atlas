# IP, trade-mark and licence remediation plan

This plan turns the findings in [IP_AND_LICENSE_REVIEW.md](IP_AND_LICENSE_REVIEW.md) into concrete repository and release work.

## Objectives

1. Remove avoidable product-name conflicts.
2. Preserve intentional standards and file-format interoperability without implying affiliation.
3. Prove the origin and licence compatibility of source code, documentation and vendored assets.
4. Make every source, binary and container distribution carry the required notices.
5. Prevent regressions through repeatable release checks.

## Workstreams

### WS-1 — Rename and trade-mark clearance

**Priority:** P0  
**Owner:** Maintainer  
**Release blocker:** Yes

#### Actions

- [ ] Create a shortlist of distinctive names that are not descriptive of workflow, process, rules, orchestration or standards.
- [ ] Search exact and similar names in:
  - Swissreg / IGE
  - WIPO Global Brand Database and Madrid Monitor
  - EUIPO / TMview
  - USPTO
  - GitHub, package registries, container registries, domain names and general web search
- [ ] Include at least Nice classes 9 and 42 in the professional clearance scope; add other classes based on the intended commercial offering.
- [ ] Obtain a written clearance result before public product launch.
- [ ] Record the selected name and clearance decision in an ADR.
- [ ] Rename all project-controlled identifiers:
  - repository and Go module path
  - command/binary name
  - UI product name and title
  - package documentation and examples
  - container image names
  - telemetry/service names
  - project-owned XML namespace and moddle prefix
  - screenshots and diagrams
- [ ] Add migration aliases or release notes where changing machine-facing identifiers would break users.
- [ ] Do not register or assert rights in `Atlas` for this product unless legal clearance explicitly supports it.

#### Acceptance criteria

- No release artefact displays `Atlas` as the product mark.
- Remaining occurrences are historical references, migration notes or third-party proper names.
- The legal record contains the search date, territories, classes and decision-maker.

### WS-2 — Trade-mark and affiliation notices

**Priority:** P0  
**Release blocker:** Yes

#### Actions

- [ ] Add a root `TRADEMARKS.md` containing factual no-affiliation language.
- [ ] Add a concise notice to the README and About/help screen.
- [ ] Use `BPMN` and `DMN` only to identify supported standards.
- [ ] Use `Camunda` and `Zeebe` only where needed to describe compatibility or source format.
- [ ] Never use third-party logos unless their owner’s published policy clearly permits the specific use.
- [ ] Review all marketing wording for endorsement, certification and replacement claims.

#### Acceptance criteria

- All public surfaces state that the project is independent.
- No unsupported certification or endorsement statement remains.

### WS-3 — Camunda/Zeebe provenance audit

**Priority:** P0  
**Release blocker:** Yes

#### Scope

Audit code, tests, comments, documentation and UI behaviour that resemble or reference Camunda/Zeebe, including:

- record/value-type/intent model;
- element-instance lifecycle;
- partition-key layout and source-position concepts;
- `zeebe:` XML extensions and moddle descriptors;
- job APIs, incidents and exporter-like concepts;
- user-task, form, I/O mapping and decision-binding behaviour;
- UI wording described as “Camunda-style” or “reference modeler”.

#### Actions

- [ ] Identify the author and external references for each affected subsystem.
- [ ] Compare the implementation with the relevant Camunda source revision.
- [ ] Use similarity tooling plus manual review; do not rely only on file names.
- [ ] Classify each item:
  - independently implemented idea;
  - standards or interoperability requirement;
  - derived from permissively licensed material with attribution;
  - uncertain provenance;
  - incompatible copy/derivative.
- [ ] For uncertain or incompatible material, either:
  - rewrite from standards and clean-room behavioural requirements;
  - obtain permission or a compatible licence;
  - remove the feature.
- [ ] Record reviewed source revisions, licences and conclusions in `docs/legal/PROVENANCE_AUDIT.md`.
- [ ] Replace aspirational language such as “mirroring the reference modeler” with precise behaviour descriptions where practical.
- [ ] Add a contributor declaration requiring disclosure of copied, adapted or translated third-party material.

#### Acceptance criteria

- Every flagged subsystem has a recorded provenance conclusion.
- No file remains classified as uncertain or incompatibly derived.
- Independent implementation is supported by tests written from public specifications or documented behavioural requirements.

### WS-4 — bpmn.io licence and watermark compliance

**Priority:** P0  
**Release blocker:** Yes

#### Actions

- [ ] Record exact package names, versions, source URLs and licences for:
  - `bpmn-js`
  - `bpmn-moddle`
  - `diagram-js`
  - properties-panel packages
  - `form-js` packages
  - all transitive packages included in generated vendor bundles
- [ ] Preserve the complete upstream licence notice in source and distributions.
- [ ] Confirm the bpmn.io watermark code is not removed, modified, hidden or obscured.
- [ ] Add an end-to-end UI test or release checklist step verifying watermark visibility in viewer and modeler views at supported viewport sizes.
- [ ] Ensure custom overlays, drawers, banners and white-label CSS never cover the watermark.
- [ ] Document the intentional bpmn.io use in `THIRD_PARTY_NOTICES.md`.
- [ ] Recheck the upstream licence whenever package versions are changed.

#### Acceptance criteria

- A clean build includes the required notices.
- Screenshots from the release test show an unobstructed watermark where required.
- No minification or bundling step strips copyright notices that must be retained.

### WS-5 — Third-party inventory, notices and SBOM

**Priority:** P0  
**Release blocker:** Yes

#### Actions

- [ ] Add root `THIRD_PARTY_NOTICES.md`.
- [ ] Inventory Go modules with `go list -m -json all` and licence scanning.
- [ ] Inventory vendored JavaScript, CSS, fonts, icons, images, schemas and generated bundles.
- [ ] Preserve NOTICE files and attribution notices required by Apache-2.0 dependencies.
- [ ] Generate CycloneDX or SPDX SBOMs for source, binary and container release artefacts.
- [ ] Add a CI job that fails on unknown, missing or policy-disallowed licences.
- [ ] Keep generated licence reports as release artefacts.
- [ ] Document how users can obtain corresponding source for any component whose licence requires it.

#### Acceptance criteria

- Every distributed component maps to a known source, version and licence.
- No `UNKNOWN`, `NOASSERTION` or unreviewed custom licence remains at release time.

### WS-6 — Clarify project licensing

**Priority:** P1  
**Release blocker:** Yes

#### Actions

- [ ] Remove “Proposed default” from the README.
- [ ] State clearly that original project code is Apache-2.0 unless a file says otherwise.
- [ ] State clearly that third-party components retain their own licences.
- [ ] Decide whether the copyright holder should be a named individual, organisation or contributors collectively.
- [ ] Ensure source headers, licence file and release metadata use consistent wording.
- [ ] Retain DCO sign-off requirements and add provenance disclosure to contribution guidance.

#### Acceptance criteria

- A contributor or recipient can determine the applicable licence without interpretation.

### WS-7 — Compatibility claims and namespace governance

**Priority:** P1  
**Release blocker:** Yes for marketing claims

#### Actions

- [ ] Create a compatibility matrix listing the exact supported BPMN/DMN and `zeebe:` elements and semantics.
- [ ] Replace broad “Camunda-compatible” claims with versioned, testable statements.
- [ ] Maintain conformance tests based on public specifications and independently authored fixtures.
- [ ] Keep third-party XML namespaces unchanged when reading/writing compatibility formats.
- [ ] Use a project-controlled namespace only for project-owned extensions.
- [ ] After rename, publish stable namespace and extension governance rules.
- [ ] Do not describe custom extensions as part of the BPMN or DMN standards.

#### Acceptance criteria

- Every compatibility claim links to a testable matrix.
- Project-specific extensions are visibly distinguished from standards content.

### WS-8 — Patent and commercial release review

**Priority:** P2 before commercial scale  
**Release blocker:** Risk-based

#### Actions

- [ ] Document the intended territories, customer segments and deployment model.
- [ ] Identify high-risk technical areas for a targeted patent search, such as durable workflow execution, log-based process engines and distributed partitioning.
- [ ] Obtain professional freedom-to-operate advice before major commercial investment where appropriate.
- [ ] Keep invention and design records for independently developed mechanisms.
- [ ] Review contributor agreements and employer/IP obligations for core contributors.

## Delivery sequence

### Phase 0 — Immediate repository safeguards

- Add legal review, remediation plan and temporary-name warning.
- Add `TRADEMARKS.md` and a starter `THIRD_PARTY_NOTICES.md`.
- Correct README licence wording.
- Mark legal compliance as a release gate.

### Phase 1 — Evidence collection

- Complete dependency and vendored-asset inventory.
- Complete Camunda/Zeebe provenance audit.
- Record bpmn.io versions and watermark verification.
- Generate first SBOM.

### Phase 2 — Rename

- Select and clear the new name.
- Perform mechanical and semantic rename.
- Introduce project-owned extension namespace.
- Publish migration notes.

### Phase 3 — Automated enforcement

- Add licence-policy CI.
- Add SBOM generation.
- Add watermark UI regression coverage.
- Add checks for prohibited branding and unsupported claims.

### Phase 4 — Release sign-off

The maintainer signs a release checklist confirming:

- trade-mark clearance or completed rename;
- no unresolved provenance items;
- complete third-party notices and SBOM;
- bpmn.io watermark compliance;
- accurate compatibility claims;
- no known legal complaint or unresolved rights assertion.

## Suggested tracking issues

Create one issue per workstream with labels such as:

- `legal`
- `release-blocker`
- `licensing`
- `trademark`
- `provenance`
- `security-and-compliance`

The P0 issues should be grouped in a milestone named **Legal and IP release readiness**.
