# IP, trade-mark and licence remediation plan

This plan implements the controls identified in [IP_AND_LICENSE_REVIEW.md](IP_AND_LICENSE_REVIEW.md).

## P0 — Must be complete before public or commercial release

### 1. Rename and trade-mark clearance

- [ ] Build a shortlist of distinctive names.
- [ ] Search Swissreg/IGE, WIPO, EUIPO/TMview, USPTO, GitHub, package/container registries and domain names.
- [ ] Include at least Nice classes 9 and 42 in professional clearance.
- [ ] Record the selected name, territories, classes, search date and decision in an ADR.
- [ ] Rename repository, Go module, binary, UI, documentation, images, telemetry names and project-owned XML namespace.
- [ ] Add migration notes or aliases where machine-facing names change.

**Acceptance:** no release artefact uses `Atlas` as the product mark unless written clearance explicitly permits it.

### 2. Trade-mark and no-affiliation notices

- [ ] Add root `TRADEMARKS.md`.
- [ ] Add concise no-affiliation wording to README and product About/help surfaces.
- [ ] Use BPMN/DMN only as standards names and Camunda/Zeebe only for precise interoperability descriptions.
- [ ] Remove unsupported certification, endorsement or drop-in-replacement claims.
- [ ] Do not use third-party logos without verified permission.

### 3. Camunda/Zeebe provenance audit

Audit code, tests, comments, documentation and UI behaviour involving:

- record/value-type/intent concepts;
- element-instance lifecycle;
- partition/source-position concepts;
- `zeebe:` XML extensions and moddle descriptors;
- jobs, incidents, forms, I/O mappings and decision binding;
- wording such as “Camunda-style” or “reference modeler”.

For every affected subsystem:

- [ ] identify authors and external references;
- [ ] compare against the relevant upstream revision and licence;
- [ ] classify as independent, standards/interoperability-driven, compatibly derived, uncertain, or incompatible;
- [ ] rewrite, remove, relicense or obtain permission for uncertain/incompatible material;
- [ ] record conclusions in `docs/legal/PROVENANCE_AUDIT.md`;
- [ ] require contributors to disclose copied, adapted, translated or AI-generated third-party material.

**Acceptance:** no item remains classified as uncertain or incompatibly derived.

### 4. bpmn.io licence and watermark compliance

- [ ] Record exact versions, source URLs and licences for `bpmn-js`, `bpmn-moddle`, `diagram-js`, properties-panel and `form-js` packages.
- [ ] Record transitive packages included in generated browser bundles.
- [ ] Preserve upstream licence and copyright notices in source and distributions.
- [ ] Confirm required bpmn.io watermark code is not removed, changed, hidden or obstructed.
- [ ] Add an end-to-end test or release-check screenshot for viewer/modeler watermark visibility.
- [ ] Recheck licence terms on every package update.

### 5. Third-party inventory and SBOM

- [ ] Add root `THIRD_PARTY_NOTICES.md`.
- [ ] Inventory Go modules and vendored JS/CSS/fonts/icons/images/schemas/bundles.
- [ ] Preserve required upstream `NOTICE` and attribution files.
- [ ] Generate SPDX or CycloneDX SBOMs for source, binary and container artefacts.
- [ ] Add CI that fails on unknown, missing or policy-disallowed licences.
- [ ] Retain licence reports as release artefacts.

**Acceptance:** every distributed component has a known source, version and licence; no unresolved `UNKNOWN` or `NOASSERTION` entries remain.

## P1 — Repository policy and compatibility governance

### 6. Clarify project licensing

- [ ] Remove provisional licence wording from README.
- [ ] State that original project code is Apache-2.0 unless a file says otherwise.
- [ ] State that third-party components retain their own licences.
- [ ] Align copyright wording, source headers and release metadata.
- [ ] Keep DCO sign-off and add provenance disclosure to contribution guidance.

### 7. Compatibility claims and namespaces

- [ ] Publish a versioned compatibility matrix for supported BPMN/DMN and `zeebe:` elements and semantics.
- [ ] Replace broad compatibility claims with testable statements.
- [ ] Base conformance tests on public specifications and independently authored fixtures.
- [ ] Keep third-party namespaces unchanged only for compatibility formats.
- [ ] Use a project-controlled namespace for project-owned extensions after rename.
- [ ] Clearly distinguish custom extensions from OMG standards.

## P2 — Commercial risk review

- [ ] Document intended territories, customers and deployment model.
- [ ] Identify patent-search areas such as durable workflow execution, log-based engines and partitioning.
- [ ] Consider professional freedom-to-operate advice before major commercial investment.
- [ ] Keep invention/design records for independently developed mechanisms.
- [ ] Review contributor employer/IP obligations.

## Delivery sequence

### Phase 0 — Immediate safeguards

- Add this review and plan.
- Add `TRADEMARKS.md` and `THIRD_PARTY_NOTICES.md`.
- Add temporary-name/release-gate wording to README.
- Clarify Apache-2.0 wording and contribution provenance requirements.

### Phase 1 — Evidence

- Complete dependency and vendored-asset inventory.
- Complete Camunda/Zeebe provenance audit.
- Record bpmn.io versions and watermark verification.
- Generate first SBOM.

### Phase 2 — Rename

- Select and clear a new name.
- Rename all controlled identifiers and namespace.
- Publish migration notes.

### Phase 3 — Automation

- Add licence-policy CI, SBOM generation, watermark regression coverage and prohibited-branding checks.

### Phase 4 — Release sign-off

The maintainer confirms:

- trade-mark clearance or completed rename;
- no unresolved provenance items;
- complete notices and SBOM;
- bpmn.io watermark compliance;
- accurate compatibility claims;
- no unresolved rights complaint.

## Suggested issue structure

Create one issue per numbered workstream, label P0 items `legal`, `release-blocker`, `licensing`, `trademark` or `provenance`, and group them in a milestone named **Legal and IP release readiness**.
