# CON0 — Normative BPMN 2.0.2 scope and source-to-evidence audit

**Programme:** [BPMN 2.x Conformance](bpmn-conformance-program.md) · **Slice:** CON0 · [Issue #1615](https://github.com/pblumer/atlas/issues/1615)  
**Source baseline:** `pblumer/atlas` main commit `662c9e92e28b384a8c1ff4ddec7311ebf74ef87e`, 2026-11-08.  
**Authority:** OMG [BPMN 2.0.2](https://www.omg.org/spec/BPMN/2.0.2/PDF/) (formal/2013-12-09), [normative schemas](https://www.omg.org/spec/BPMN/) (BPMN20.xsd, Semantic.xsd, BPMNDI.xsd, BPMN20.cmof).  
**Status:** Source/specification desk audit, **not signed-off** normative completeness, passed Go tests or an OMG conformance assessment.

## 1. Conformance classes and real obligations

| Claim | Normative source | Obligation | Initial atlas assessment |
| --- | --- | --- | --- |
| Process Execution | **§2.3.1** and **§13.1–§13.5** | Execute all applicable operational semantics and activity lifecycle; interpret metamodel | **Not demonstrated** by closed 34-feature subset |
| Process/Collaboration import | **§2.3.2**, §8–10 and 15 | Import Process diagram types **including defining Collaboration** | **Partial/unverified**; collaboration/flow validation separate |
| Process Modeling | §2.2, incl. Descriptive (Table 2.1), Analytic (Table 2.2), Common Executable (Tables 2.3–2.4) and full class | Diagram authoring, attributes and visual interchange; some sub-classes have different mandatory data/service expression languages | **Separate claim**, not an execution percentage |
| Choreography Modeling | §2.5, §11 | Choreography diagrams/semantics | **Out of current execution target**; do not claim choreography conformance |
| BPEL Process Execution | §2.4, §14 | Execution plus BPMN-to-BPEL mapping | **Out of current target**; does not exempt §2.3 |

**Important:** “Common Executable” is a *Process Modeling* sub-class (§2.2), **not** a shortcut for Process Execution (§2.3). It prescribes XML Schema for data types, WSDL for service interface and XPath for data access. atlas FEEL and Worker Type semantics cannot be counted as WSDL/XPath support without evidence. Conversely a Process Execution tool is not required to implement §2.2 graphical notation or §2.5 choreography.

**Normative cross-reference inconsistency:** §2.3.1 cites §14.2.2 for operational semantics/activity lifecycle, but §14.2.2 is BPMN-to-BPEL **Activities mapping** and Clause 13 is headed *BPMN Execution Semantics*; §13.1 expressly states it is required for Process Execution. Audit against §13 and record this inconsistency instead of silently making BPEL obligatory. The OMG also lists a separate [misnumbered §2.1 conformance cross-reference issue](https://issues.omg.org/issues/spec/BPMN/2.0.2).

## 2. Exact §13.1 non-operational exceptions

The standard permits **ignoring operational behaviour** of these constructs for Process Execution; it does not automatically permit dropping required import metadata:

| Non-operational construct/attribute | CON0 treatment |
| --- | --- |
| Manual Task, Abstract Task | Keep in inventory; exclude from *mandatory executable* denominator, even if atlas provides pass-through |
| DataState, IORules | Keep as modelling/metamodel entries; execution optional |
| Ad-Hoc **Process** | Exact spec exception; **do not extend to Ad-Hoc Sub-Process** with §13.3.5 semantics |
| ItemDefinitions whose itemKind is Physical | Physical item kinds optional to execute; logical item definition validation still requires audit |
| DataInput.inputSetWithWhileExecuting | This attribute only; not DataInput generally |
| DataOutput.outputSetWithWhileExecuting | This attribute only; not DataOutput generally |
| Process.isClosed | This attribute only; other process lifecycle conditions remain normative |
| SequenceFlow.isImmediate | This attribute only; conditional/default/parallel propagation remain normative |

**Review note:** The original planning seed classifies Manual/Abstract Task as executed pass-through. Reinterpret them under the explicit §13.1 allowance rather than treating that implementation as a normative coverage obligation.

## 3. Clauses and source/test coverage crosswalk

This table identifies **review surfaces**, not assertion-by-assertion proof. Every relevant attribute, event position and combination needs eventual individual evidence.

| OMG clause(s) | Inventory scope | Current evidence | Gaps / next proof |
| --- | --- | --- | --- |
| §8.2–8.3, 15.3 | MODEL-01/03/04 | `compiler/parse.go`, `compiler/validation.go` | XSD namespaces/imports, extensions, validation diagnostics, metamodel fidelity |
| §8.4–8.5 | FLOW-02/14, DATA-04/08, COLL-06 | `compiler/process.go`, `compiler/scope_compile.go` | Correlation, expression languages, item types, resource roles, interface/operation semantics |
| §9.3 | COLL-01/02/04 | `compiler/parse.go`; ADR-0023/0121 | Pools/lanes, nested ownership, participant references |
| §9.4 | COLL-03/05 | `ROADMAP.md` Milestone 3, ADR-0023 | MessageFlow metadata and validation, defining Collaboration import |
| §9.5 | MODEL-06 | `compiler/parse.go` | Conversation/correlation imported versus ignored; modeling separately |
| §10.2–10.3.4 | FLOW-01, ACT-01–07 | `compiler/parse_test.go`, `engine/behavior.go` | Process type, task attributes, ReceiveTask.instantiate, WSDL/operation fidelity |
| §10.3.5–10.3.8 | ACT-08–21 | `compiler/adhoc_test.go`, `compiler/process.go` | Ad-hoc sequential, loop max/testBefore, MI cardinality, completion/cancel |
| §10.4 | DATA-01–09 | `conformance/models/data-object.bpmn`, ADR-0058/59/60 | I/O specifications, data-store operations, item types and mapping |
| §10.5.1–10.5.7 | EVT-01–44 | `compiler/conditional_test.go`, `engine/conditional_test.go` | Legal event-definition/position matrix, multiple and parallel-multiple, repeating noninterrupting |
| §10.6.2–10.6.4 | FLOW-03–06 | `conformance/scenario.go`, ADR-0033 | Structured versus cyclic/unstructured synchronizing merges |
| §10.6.5 | FLOW-10 | `compiler/parse_test.go:TestParseUnsupportedElementMessage` | **Complex Gateway rejected** |
| §10.6.6 | FLOW-07–09 | `compiler/eventgateway_instantiate_test.go` | **Parallel and instantiating variants rejected** |
| §10.7 | ACT-20, EVT-18/28/39/40 | `conformance/models/compensation.bpmn` | Ordering and scope errors/compensation interplay |
| §10.8 | COLL-04 | ADR-0121 | Nested lanes; role/assignment is distinct |
| §12 / Annex B | MODEL-02/03 | `api/web/editor.js`, `api/layout/layout.go` | Separate visual/interchange claims |
| §13.2 | FLOW-01/12, EVT-01–07 | `engine/behavior.go`, event-gateway tests | **Instantiating ReceiveTask/Gateway**, multiple/parallel multiple start, termination |
| §13.3.1–13.3.2 | FLOW-02/11–14, ACT-01–21 | `engine/behavior.go` | **Uncontrolled flow**: each independently arriving token; mixed conditional/unconditional outgoing |
| §13.3.3–13.3.5 | ACT-01–14 | `engine/behavior.go`, `compiler/adhoc_test.go` | Activity lifecycle and Ad-Hoc **Sub-Process** ordering |
| §13.3.6–13.3.7 | ACT-15–21 | `conformance/models/standard-loop.bpmn` and MI fixtures | Empty/parallel/sequential MI, early completion, collected outputs |
| §13.4.1–13.4.5 | FLOW-03–10 | 4 central gateway families; `compiler/validation.go` | Complex/parallel event gateways, cyclic OR joins |
| §13.5.1–13.5.6 | EVT-01–44 | Compiler/engine tests and narrow suite scenarios | Event start/catch/throw/boundary/event-sub/end; scope, interruption, repeated triggers |

### 2.2 modeling sub-classes: distinct inventory

- **Descriptive (Table 2.1):** pool/participant, lanes, sequence and message flow, XOR/AND gateways, none/user/service task, expanded/collapsed subprocess, call activity, data object/store reference, annotation/association, none start/end.
- **Analytic (Table 2.2):** adds send/receive tasks, conditional/default sequence flow, loops/MI, inclusive/event gateways, link/message/timer/signal/error/escalation/conditional events by position, documentation and group.
- **Common Executable (Tables 2.3–2.4):** distinct required modeling attributes for task I/O, operation, resource role, item definition, message and assignments; language requirements above.
- **Full Process Modeling:** includes Conversations (§9.5), symbol rules and interchange; do not confuse this with engine §2.3 support.

## 4. Concrete source and test findings

**Legend:** C=compiler-level test, E=engine semantic test, S=central conformance scenario, D=ADR/document only. The names below were found in the repository; **the test suite was not executed as part of this desk audit**.

| Construct / position | Status in checked code | Direct evidence | Missing central/standard proof |
| --- | --- | --- | --- |
| XOR/AND/structured OR gateways | Implemented subset; cyclic OR remains limited | S `exclusive-gateway`, `parallel-independent`, `inclusive-gateway`; D ADR-0033 | Cyclic/unstructured joins, repeated tokens |
| Complex Gateway | **Unsupported** | C `compiler/parse_test.go:TestParseUnsupportedElementMessage`; `compiler/scope_compile.go` | Explicit negative; valid BPMN not supported |
| Event gateway exclusive race | Implemented subset | S `event-gateway-message`, `event-gateway-timer` | Cross-trigger races and losing waits restart |
| Event gateway Parallel / instantiate=true | **Unsupported variants** | C `compiler/eventgateway_instantiate_test.go:TestParseRefusesAnEventGatewayAtlasDoesNotRun`, `TestReloadKeepsAnEventGatewayAtlasDoesNotRun` | Each variant tracked separately |
| Root conditional start | **Unsupported** | C `compiler/conditional_test.go:TestParseRefusesAProcessLevelConditionalStart` | Unlike event subprocess conditional start |
| Conditional intermediate catch | Implemented subset | C `TestParseConditionalCatch`; E `engine/conditional_test.go:TestConditionalCatchWaitsThenFires`, `TestConditionalCatchRecovers` | No `conformance.Features` entry |
| Conditional boundary / event subprocess | **Partial repeatability** | E `TestConditionalBoundaryNonInterruptingFiresOnce`, `TestConditionalEventSubNonInterrupting`; ADR-0137 | Noninterrupting repeated false→true absent |
| Escalation throw/end/boundary | Implemented subset | C `compiler/escalation_test.go:TestParseEscalationEndAndBoundary`; E `engine/escalation_test.go:TestEscalationBoundaryRecovers` | Not in central suite |
| Escalation through call activity | Implemented subset | E `engine/escalation_phase4_test.go:TestChildEscalationCaughtByCallerBoundary` | Not in central suite |
| Terminate End | Compiles; variants still need review | C `compiler/parse_test.go:TestParseTerminateEndCompiles`, D ADR-0116 | No central scenario |
| Manual/Abstract Task | Pass-through supported | C `compiler/parse_test.go:TestParsePlainTaskPassThrough` | **§13.1 non-operational**; not compulsory execution |
| Business Rule Task | Compiler supported | C `compiler/parse_test.go:TestParseBusinessRuleTask` | No central scenario |
| Ad-Hoc Sub-Process parallel | Implemented subset | C `compiler/adhoc_test.go:TestParseAdHocSubProcess`, `TestParseAdHocCompletionConditionAndCancel` | No central scenario |
| Ad-Hoc Sub-Process Sequential | **Unsupported** | C `compiler/adhoc_test.go:TestParseAdHocSequentialRejected`; ADR-0138 | Do not misapply Ad-Hoc **Process** exemption |
| Intermediate Link Throw/Catch | Compiler-supported subset | `compiler/process.go:TypeLinkThrowEvent`; ADR-0132 | No central scenario |
| Intermediate None Throw | Compiler-supported | `compiler/process.go:TypeNoneThrowEvent` | No central scenario |
| Intermediate timer catch with timeCycle | **Unsupported at this position** | `compiler/scope_compile.go` explicit rejection | Separate Start/Boundary timer cycles |
| ReceiveTask.instantiate | **Unknown** | §13.2 explicitly permits process instantiation; inspect `compiler/parse.go` | The ordinary `receive-task` case does not prove it |
| Multiple / Parallel Multiple event families | **Unknown** | §10.5 and §13.2; not registered | Require legal-position and code audit |
| Collaboration/pools/lanes | **Partial/unknown** | ADR-0023/0121; `compiler/parse.go`; `ROADMAP.md` | Normative defining Collaboration import (§2.3.2) |
| DataObject/collection/fields | Implemented subset | S `data-object`, `data-object-fields`, `collection-data-object` | DataStore/I/O/structure/XPath variants |
| Full-state restart/replay | **Not proven centrally** | `conformance/runner.go` compares root projection, not every persisted family | CON3 checkpoints and state families |
| Differential oracle | **Four opt-in translations** | `conformance/differential/differential_test.go` | Missing Node can skip; separate CI gate |
| Negative cases | **Four central fixtures** | `conformance/negative_test.go` and `scenario.go` | Assert exact diagnostic code/location |

## 5. Specific newly exposed semantic tests

**§13.3.1 uncontrolled flow.** A task with multiple incoming flows must independently enable for each arriving token (as opposed to a synchronizing AND join); multiple unconditional outgoing flows fork, and a mix of conditional and unconditional flows has special semantics. A gateway-only suite does not prove any of these. Need separate *same-task concurrent tokens*, duplicate activity enablement, and per-token recovery cases.

**§13.2 process creation/termination.** The standard permits process creation from an instantiating Receive Task or Event-Based Gateway and defines correlated parallel event-gateway completion. Multiple Parallel Start must wait for all triggers. Current event-based gateway compiler rejects parallel/instantiating variants; count each as an unsupported operational requirement, not as a missing visual icon. An instance completes only once no tokens **and no active activities** remain. Verify noninterrupting boundaries, MI, compensation and late events at exact checkpoints.

**§13.1 exemptions.** Ad-Hoc Process and Manual/Abstract Task are explicitly listed; the Ad-Hoc **Sub-Process** still has §13.3.5 semantics. This distinction changes how unsupported sequential ordering is counted.

**Observed documentation drift.** `conformance/COVERAGE.md` “Gaps: None” is only closed-registry coverage; `README.md` says 31 features, `conformance/tck/README.md` 30 positive, central registry has 34; `conformance/README.md` still treats terminate/escalation as unsupported. CON1/CON5 own generator and copied-number remediation.

## 6. Remaining CON0 acceptance gates

- [ ] Complete the legal **event-definition × event-position × scope × interrupting/repeating** matrix using §10.5 and XSD; expand all rows and mark spec-illegal combinations explicitly.
- [ ] Review §13.2–13.5 *per semantic rule*, not just headings; every SHALL/mandatory variant gets exact spec and test/issue.
- [ ] Review Collaboration import, message-flow and participant references (§2.3.2), ResourceRole/Operation, DataStore and all remaining unknown rows.
- [ ] Independently verify normalized row IDs and normative feature denominator; distinguish required, non-operational, optional, invalid and separate conformance classes.
- [ ] Map each applicable normative rule to a **run and reviewed outcome**, or explicitly missing evidence. The code-path audit alone does not prove correct behavior.
- [ ] Obtain maintainer sign-off on in-scope denominator/exclusions before CON1 computes percentages.
- [ ] Execute actual repository tests/CI and record results before declaring the slice complete.

**Result of this PR:** traceable normative review map, registered-only versus supported distinctions, explicit gaps and a 43/43 WCP title inventory. No feature-implementation, runtime or storage changes. No unsupported semantic is described as fully BPMN-conformant.
