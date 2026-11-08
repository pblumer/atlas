# BPMN 2.0.2 feature inventory — CON0 review seed

**Status:** Proposed and not normatively complete; prepared 2026-11-08 for the [CON programme](bpmn-conformance-program.md).  
**Authority:** [OMG BPMN 2.0.2](https://www.omg.org/spec/BPMN/2.0.2/PDF/), [OMG XSD/CMOF catalogue](https://www.omg.org/spec/BPMN/).  
**IMPORTANT:** This is an initial audit seed, NOT a compliance claim or finalized denominator. CON0 must review the specification clause by clause (including `2, `8–`13), verify positions/variant legality, and map stable IDs to test evidence.

### Classification and governance

**I** = implemented according to inspected code/roadmap (not necessarily conformance-tested); **P** = partial implementation / important limitation; **N** = explicit rejection; **?** = verification pending. All statuses remain provisional until CON0 review. An N row remains visible and scores zero positive-support credit; expected rejection proves a diagnostic only. Separate invalid-by-spec from valid-but-unsupported. Do not treat Zeebe or atlas extensions as normative BPMN support.

Each authoritative machine-readable row needs `id`, normative `specClause`, `conformanceClass`, applicability, position, trigger, interrupting/repeating behavior, implementation status, limitations, actual source/test/ADR links, and a reviewer decision.

## Core process and control flow

| Seed | Semantic candidate / variants | Status | Evidence or limitation |
| --- | --- | --- | --- |
| FLOW-01 | Process and explicit creation; none start/end | I | `conformance/scenario.go` |
| FLOW-02 | Sequence flow, conditions, defaults | I | `sequence`, `exclusive-gateway` |
| FLOW-03 | Exclusive choice and simple merge | I | `exclusive-gateway` |
| FLOW-04 | Parallel split and synchronizing join | I | `parallel-independent`; add edge cases |
| FLOW-05 | Inclusive choice/synchronizing merge | P | `inclusive-gateway`; ADR-0033 limits cycles |
| FLOW-06 | Cyclic or unstructured inclusive join | ? | ADR-0033 explicitly notes open gap |
| FLOW-07 | Exclusive event-based gateway | I | `event-gateway-message`, `event-gateway-timer` |
| FLOW-08 | Parallel event-based gateway | N | `compiler/validation.go` |
| FLOW-09 | Instantiating event-based gateway | N | `compiler/eventgateway_instantiate_test.go` |
| FLOW-10 | Complex Gateway | N | `compiler/scope_compile.go` |
| FLOW-11 | Multiple incoming/outgoing flows without gateways | ? | inspect normative token semantics |
| FLOW-12 | Unstructured cycles and process completion | ? | add divergent token/recovery tests |
| FLOW-13 | Gateway default-flow and no-match failure | ? | explicit variant oracles |
| FLOW-14 | Model validation: reachability/scope/refs | I | `compiler/validation.go`; diagnostic matrix incomplete |

## Task and activity families

| Seed | Semantic candidate / variants | Status | Evidence or limitation |
| --- | --- | --- | --- |
| ACT-01 | Generic task and manual task | I | `compiler/parse_test.go` |
| ACT-02 | User Task; start, park, complete | I | `user-task` |
| ACT-03 | Service Task; worker job completion | I | `service-task` |
| ACT-04 | Script Task; FEEL execution and script errors | P | `script-task`; dialect distinction required |
| ACT-05 | Business Rule Task; local/remote DMN | I | `compiler/process.go`, `engine/behavior.go` |
| ACT-06 | Send Task; message kind and worker kind | I | `compiler/scope_compile.go` |
| ACT-07 | Receive Task; correlation and boundaries | I | `receive-task` |
| ACT-08 | Embedded Subprocess and nested scope | I | `subprocess` |
| ACT-09 | Event Subprocess: interrupting / non-interrupting | P | concurrent non-interrupting scenario only |
| ACT-10 | Transaction: normal commit, cancel, compensation | P | `transaction-cancel` narrow variant |
| ACT-11 | Ad-hoc Subprocess, parallel ordering | I | `compiler/adhoc_test.go` |
| ACT-12 | Ad-hoc Subprocess, sequential ordering | N | `compiler/adhoc_test.go` |
| ACT-13 | Ad-hoc completionCondition/cancelRemainingInstances | ? | ADR-0138; add variant tests |
| ACT-14 | Call Activity, binding, child error/termination | P | `call-activity`; edge cases not in suite |
| ACT-15 | Standard loop: before/after/max/zero repeats | P | `standard-loop` covers one variant |
| ACT-16 | Parallel Multi-Instance; cardinality/empty input | P | `multi-instance` narrow variant |
| ACT-17 | Sequential Multi-Instance; cancellation/outputs | P | `multi-instance-sequential` narrow variant |
| ACT-18 | MI completionCondition and ordered outputCollection | ? | map existing engine tests |
| ACT-19 | Activity I/O mappings and variable scope | ? | ADR-0068; not centrally registered |
| ACT-20 | Compensation handler selection/reverse completion order | P | `compensation`; broader spec matrix |
| ACT-21 | Activity lifecycle, boundary interruption, nested termination | ? | spec `13; recovery checkpoints |

## Start and intermediate catch events

| Seed | Semantic candidate / variants | Status | Evidence or limitation |
| --- | --- | --- | --- |
| EVT-01 | None Start | I | `sequence` |
| EVT-02 | Message Start | I | `message-start` |
| EVT-03 | Timer Start: date, duration, cycle | P | `timer-start` tests limited variant |
| EVT-04 | Signal Start | I | `signal-start` |
| EVT-05 | Process-root Conditional Start | N | `compiler/validation.go` |
| EVT-06 | Conditional/Error/Escalation Start in event subprocess | ? | legal context must be verified |
| EVT-07 | Multiple Start / Parallel Multiple Start | ? | inspect spec and compiler |
| EVT-08 | Intermediate Message Catch | I | `message-catch` |
| EVT-09 | Intermediate Timer Catch | I | `timer-catch` |
| EVT-10 | Intermediate Signal Catch | I | `signal-throw-catch` |
| EVT-11 | Intermediate Conditional Catch | I | `compiler/conditional_test.go` |
| EVT-12 | Intermediate Link Catch | I | ADR-0132 |
| EVT-13 | Intermediate Multiple/Parallel Multiple Catch | ? | inspect spec and compiler |

## Intermediate throw, boundary and end events

| Seed | Semantic candidate / variants | Status | Evidence or limitation |
| --- | --- | --- | --- |
| EVT-14 | Intermediate None Throw | ? | model/legal position review |
| EVT-15 | Intermediate Message Throw | I | `ROADMAP.md` Milestone 2 |
| EVT-16 | Intermediate Signal Throw | I | `signal-throw-catch` |
| EVT-17 | Intermediate Escalation Throw | I | `compiler/escalation_test.go` |
| EVT-18 | Intermediate Compensation Throw | I | `compensation` |
| EVT-19 | Intermediate Link Throw | I | ADR-0132 |
| EVT-20 | Intermediate Multiple Throw | ? | spec/legal positions |
| EVT-21 | Boundary Timer interrupting | I | `boundary-timer-interrupting` |
| EVT-22 | Boundary Timer non-interrupting, recurring | ? | ADR-0054 |
| EVT-23 | Boundary Message interrupting/non | P | `boundary-message-noninterrupting` |
| EVT-24 | Boundary Signal interrupting/non | P | `signal-boundary` |
| EVT-25 | Boundary Error | I | `boundary-error` |
| EVT-26 | Boundary Escalation interrupting/non | P | `compiler/escalation_test.go` |
| EVT-27 | Boundary Conditional interrupting/non, repeatability | P | ADR-0137 repeat limitation |
| EVT-28 | Boundary Compensation | I | `compensation` |
| EVT-29 | Boundary Cancel on Transaction | I | `transaction-cancel` |
| EVT-30 | Boundary Multiple/Parallel Multiple | ? | inspect spec and compiler |
| EVT-31 | Event Subprocess message/timer/signal start, both modes | ? | existing engine tests, coverage unknown |
| EVT-32 | Event Subprocess Conditional non-interrupting repeat | P | once-per-arm limitation |
| EVT-33 | End None | I | `sequence` |
| EVT-34 | End Message | I | `ROADMAP.md` |
| EVT-35 | End Error | I | `ROADMAP.md` |
| EVT-36 | End Escalation | I | `compiler/escalation_test.go` |
| EVT-37 | End Signal | I | `ROADMAP.md` |
| EVT-38 | End Terminate | I | `compiler/parse_test.go` |
| EVT-39 | End Cancel in Transaction | I | `transaction-cancel` |
| EVT-40 | End Compensation | I | `ROADMAP.md` |
| EVT-41 | End Multiple | ? | spec/legal positions |
| EVT-42 | Error and escalation propagation across scopes/call activity | ? | needs independent semantics oracle |
| EVT-43 | Message delivery, correlation, deduplication and races | ? | distinguish BPMN and atlas extensions |
| EVT-44 | Timer events across restart / cancelled losing waits | ? | mid-flight recovery required |

## Data, collaboration, modeling and interchange

| Seed | Semantic candidate / variants | Status | Evidence or limitation |
| --- | --- | --- | --- |
| DATA-01 | DataObject/Reference/DataState | I | `data-object` |
| DATA-02 | Collection DataObject | I | `collection-data-object` |
| DATA-03 | Field-level DataObject writes | I | `data-object-fields` |
| DATA-04 | DataInput/DataOutput and Associations | ? | ADR-0058/0059 |
| DATA-05 | Assignment/transformation and item definition | ? | verify spec-supported dialect |
| DATA-06 | DataStore/DataStoreReference metadata | P | declarative layer in ROADMAP |
| DATA-07 | DataStore runtime reads/writes | P | runtime read pending, writes refused |
| DATA-08 | Property, variables and scope propagation | ? | BPMN vs Zeebe semantics |
| DATA-09 | Group, TextAnnotation, Association | ? | modelling class, not execution |
| COLL-01 | Collaboration with multiple pools/participants | I | `ROADMAP.md` Milestone 3 |
| COLL-02 | Black-box participants | I | `ROADMAP.md` |
| COLL-03 | Message flow and runtime message relationships | P | message-flow validation outstanding |
| COLL-04 | Lane/childLaneSet and flowNodeRef | I | ADR-0121 Layer A |
| COLL-05 | Collaboration import, atomic multi-process deployment | P | roadmap gap |
| COLL-06 | Resource/role and interface/operation metadata | ? | model metadata vs execution |
| MODEL-01 | BPMN XML/XSD import and validation | ? | class `2.3 import requirement |
| MODEL-02 | BPMNDI/layout/visual round trip | ? | modelling conformance, separate KPI |
| MODEL-03 | XML export/foreign namespaces and extensions | ? | interoperability evaluation |
| MODEL-04 | Documentation/artifacts | ? | non-executable subset |
| MODEL-05 | Choreography Task/Sub-Choreography | ? | separate OMG conformance class |
| MODEL-06 | Conversation Node/Link | ? | modelling/interchange classification |
| MODEL-07 | BPEL Process Execution mapping | ? | separate OMG class, evaluate out-of-scope |
| MODEL-08 | Activity/process lifecycle and token semantics (`13) | ? | cross-cutting semantic requirements |

## Workflow Patterns — independent second axis

The current suite tracks **WCP-1, WCP-2, WCP-3, WCP-4, WCP-5, WCP-6, WCP-7, WCP-16 and WCP-21**. During CON0, enumerate **WCP-1 through WCP-43** against [workflowpatterns.com](https://workflowpatterns.com/), recording authoritative pattern names, BPMN realizability, implementation/verification status, negative variants and tests. WCP coverage is **not** a BPMN normative conformance percentage.

## Review checklist (CON0 deliverable)

- [ ] Cross-check this seed against normative OMG ``2 and 8–13, XSD and CMOF; add every missing legal element/attribute/position and interaction.
- [ ] Classify **each** relevant element in at least Process Execution, Process Modeling, Choreography and BPEL lanes, with a reason for exclusions.
- [ ] Split rows for interrupting/non, repeatability, event combinations, nested scopes, concurrent tokens, loops, default handling and error propagation.
- [ ] Distinguish spec-illegal constructs from valid-but-unsupported constructs.
- [ ] Link to concrete compiler, behavior and test paths; independently review the provisional I/P/N/? classifications.
- [ ] Enumerate all 43 WCP patterns independently; do not equate them to OMG execution semantics.
- [ ] Publish a versioned authoritative register with stable IDs, reviewed clauses and explicitly approved denominator; then enable CI checks in CON1.
