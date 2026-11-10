# Planning

This directory contains **product and engineering plans for atlas**: implementation slices, programme delivery, and follow-up work identified by audits. Plans may be active or retained as historical delivery records. A plan's status is defined in the document, its ADR, and (for registered programmes) the programme manifest — its location here does not mean the work is still open.

## Delivery plans

| Document | Scope |
| --- | --- |
| [Process applications (ADR-0128)](0128-process-applications-plan.md) | Application delivery model and engineering slices |
| [Product actions (ADR-0429)](0429-product-actions-plan.md) | Product action contracts and implementation slices |
| [Audit follow-up (2026-09-07)](audit-2026-09-07-umsetzungsplan.md) | Findings and remediation plan from the September audit |
| [BPMN 2.x Conformance](bpmn-conformance-program.md) | Normative OMG coverage programme (CON), with [baseline](bpmn-conformance-baseline.md), [feature inventory](bpmn-conformance-feature-inventory.md), [CON0 normative audit](bpmn-conformance-con0-audit.md), [event matrix](bpmn-conformance-con0-event-matrix.md), [43 workflow patterns](bpmn-conformance-con0-workflow-patterns.md) and [implementation runbook](bpmn-conformance-implementation-runbook.md) |
| [Capability-driven Automation](capability-driven-automation-program.md) | Multi-slice capability-driven automation programme |
| [Identity-aware Execution](identity-aware-execution-program.md) | Identity and policy-aware worker execution programme |
| [Technical Capability Contract](technical-capability-contract.md) | Capability contract semantics, conformance and integration plan |
| [Documents instead of variables (ADR draft)](documents-instead-of-variables.md) | Storing uploaded files once in a document store, with a reference in the variable |
| [AD search as a document](ad-search-as-document.md) | Reading a whole directory into a CSV document, so a business rule task over a file can check every user (ADR-0548) |

The [programme register](../programs/README.md) is generated from manifests and tracks ownership and delivery gates. The [roadmap](../../ROADMAP.md) covers strategic milestones.

**Not product planning:** course materials are in [training](../training/README.md); reusable BPMN models are in [templates](../templates/README.md). Examples that users can install and run are in [examples](../../examples/).
