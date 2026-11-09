# BPMN 2.x Conformance (CON) delivery programme

**Programme ID:** bpmn-conformance (CON delivery slices; Windows-safe manifest ID)  
**Baseline:** `pblumer/atlas` main, reviewed 2026-11-08.  
**Status:** CON0–CON4 implementation active (PR #1618 and PR #1622 merged), based on an explicitly provisional CON0 candidate interface; denominator review and every slice acceptance gate remain pending.  
**Accountable programme owner:** pblumer (initiating repository maintainer); a coordinating owner is assigned explicitly when each slice starts.  
**Strategic track:** [Milestone 1 — Core BPMN](../../ROADMAP.md#milestone-1--core-bpmn-), with dependencies on Milestone 2 (events/data), Milestone 3 (structure), and Milestone 4 (operability).  
**Operational status:** [programme manifest](../programs/bpmn-conformance/program.json).  
**Delivery protocol:** [Programme Delivery Protocol](../development/program-delivery.md).

## Purpose and user-visible outcome

Build a trustworthy, reproducible explanation of **which normative BPMN 2.0.2 features atlas supports, with which semantic variants, and how independently that claim has been tested**. An architect, modeller or operator can open one generated report and distinguish proven support, documented partial support, a deliberately unsupported element, an untested implementation, and a consciously excluded conformance class. No feature disappears just because the engine cannot execute it.

**End-to-end proof:** Take a model using a supported interrupting boundary event, a still-unsupported complex gateway, and a separately supported but not-yet-in-suite escalation variant. The registry shows all three; the report links the first to a specification-derived expected outcome and executable recovery test, the second to a diagnostic-code assertion, and the third to an explicit untested gap rather than a green check. Regenerating the report cannot turn an unsupported/unknown row into a pass merely by omitting it.

A successful programme does **not** itself establish OMG certification or permit a general “BPMN compliant” marketing claim. Any conformance claim requires an explicit, separately reviewed assessment of the relevant OMG §2 requirements, not a percentage.

## Normative authority and scope

- Primary authority: [OMG BPMN 2.0.2](https://www.omg.org/spec/BPMN/2.0.2/PDF/), including §2.2 Process Modeling, §2.3 Process Execution, §2.4 BPEL Process Execution and §2.5 Choreography Modeling conformance; §13 is the execution-semantics authority. Validate exact clause citations during CON0.
- Machine-readable references: [OMG BPMN schema and metamodel index](https://www.omg.org/spec/BPMN/) (BPMN20.xsd, Semantic.xsd, BPMNDI.xsd, BPMN20.cmof).
- Secondary taxonomy: [Workflow Patterns Initiative](https://workflowpatterns.com/) control-flow patterns (WCP) are a **separate descriptive axis**, not a substitute for BPMN normative clauses. Explicitly inventory the 43 classic WCP control-flow entries, mapping any not directly representable in BPMN to `not-applicable` with a reason; never divide BPMN compliance by 43.
- The normative target for this engine is **Process Execution Conformance** (§2.3), including import of process diagrams and their defining collaboration. Process Modeling, Choreography Modeling and BPEL mapping remain independently reported lanes; there is no implication that all four are required for §2.3.
- Extensions (e.g. `zeebe:`, `atlas:`, FEEL task scripts, worker protocol, Incidents, replay, deployment) have separate rows and evidence, never counted as normative BPMN capabilities.

### Coverage taxonomy

Every relevant construct has rows for **element**, **position**, **behavioural variant**, and **meaningful interaction**:

1. Process and control flow: start/end, sequence flow, conditions/defaults, XOR/AND/OR/complex/event gateways, multiple incoming/outgoing paths, cyclic/unstructured topology, token completion.
2. Activities: abstract, service, send, receive, user, manual, script, business rule; subprocess, event subprocess, transaction, ad-hoc, call activity, loop, multi-instance, error and compensation lifecycle.
3. Events: none, message, timer (date/duration/cycle), signal, error, escalation, conditional, link, cancel, compensate, terminate and multiple/parallel-multiple definitions, as applicable by start/intermediate/boundary/end position, interrupting/repeating and scoped propagation.
4. Collaboration: participants, pools, lanes, message flows, correlation, black-box participants, collaboration import, cross-participant exchange.
5. Data and artifacts: data objects/references/states/collections, data inputs/outputs, stores, associations, mappings, expression languages, documentation, groups and text annotations.
6. Non-execution BPMN conformance: BPMN-DI/layout, XML import/export, Choreography and Conversation elements, BPEL mapping where applicable.

The [initial inventory](bpmn-conformance-feature-inventory.md) is a **seed for normative line-by-line review**, not a completed BPMN compliance catalogue. No metric may claim 100% of normative features until CON0 reconciles it against the OMG spec, schemas and variant matrix.

## Evidence and status model

Store the eventually authoritative feature register in `conformance/registry/`, using stable IDs independent of Go type names. Suggested fields: `id`, `name`, `conformanceClass`, `specClause`, `category`, `variants`, `applicability`, `engineStatus`, `limitations`, `codeRefs`, `testRefs`, `scenarioIds`, `negativeIds`, `recoveryIds`, `referenceIds`, `adrRefs`, `reviewedOn`, `reviewer`. Choose JSON/YAML and validate schema/links in CON1; do not build runtime parsing into the engine.

Three orthogonal fields must not be collapsed:
- **Applicability:** `required-in-target` / `optional` / `out-of-scope` / `not-applicable`, with a normative reason.
- **Implementation:** `implemented` / `partial` / `unsupported` / `unknown` (with code evidence for positive claims).
- **Verification:** `spec-reviewed-and-tested` / `suite-tested` / `tested-elsewhere` / `untested` / `blocked` (with runnable evidence).
- Record feature restrictions and accepted deviations explicitly, and distinguish normative BPMN, atlas-specific extensions and Zeebe compatibility.

A failure to execute an unsupported feature is **not a pass** for positive execution conformance. A separately registered expected compile/validation rejection is evidence only for a truthful diagnostic, not evidence that the feature is supported. A model merely being recognized/rendered likewise does not prove executable semantics.

### Reporting measures (distinct denominators)

- **Inventory completeness:** reviewed normative candidates / all candidates enumerated in the chosen spec corpus; report unreviewed count and corpus revision.
- **Executable semantic coverage:** verified executable variants / applicable normative executable variants; separate implemented-but-untested, unsupported and unknown.
- **Implementation coverage:** implemented applicable variants / applicable normative variants; do not confuse with Go statement coverage.
- **Evidence strength:** test suite / spec-reviewed oracle / restart-in-flight recovery / independent reference-engine translation, displayed separately.
- **Pattern inventory:** WCP rows with realized / partial / unsupported / not-representable. No OMG compliance claim from this ratio.
- **Modeling/interchange and choreography** have separate denominators and cannot inflate execution coverage.

Targets: 100% enumerated-and-classified applicable BPMN 2.0.2 inventory; 100% transparent limitations; a meaningful conformance case for each supported executable variant; restart-in-flight tests for stateful variants; and eventually >=95% *supported applicable execution variants*, subject to a reviewed denominator and legitimate exclusions. The percentage is a **programme objective**, not a current result or certification threshold.

## Baseline and current evidence (as of 2026-11-08)

- `conformance/scenario.go` registers **34 Features** and **9 WCP patterns**. `conformance/COVERAGE.md` reports “Gaps: None” solely because **all registered** features/patterns have a scenario; it does not have a normative inventory denominator.
- `conformance/catalog/scenarios/README.md` shows **34 positive scenarios**; `scenario.go` registers **4 negative models**. The opt-in differential runner in `conformance/differential/differential_test.go` maps **4 control-flow scenarios** to `bpmn-engine`.
- `conformance/runner.go` checks golden traces, replay-derived **root-instance outcome** (`State`, `Path`, `Variables`, `DataObjects`), and the no-orphan-tokens invariant after a completed root. It does **not** compare every persisted state family nor guarantee a restart while the process is still waiting.
- Existing compiler/engine tests already cover multiple absent registry features; see the [baseline findings](bpmn-conformance-baseline.md). Reuse those tests or add linked cases, rather than reimplementing working BPMN behaviours.
- Documentation drift: `README.md` currently reports **31** features while the register holds **34**; `ROADMAP.md` and `conformance/tck/README.md` retain older scenario totals; `conformance/README.md` still describes terminate end as rejected though `compiler/parse_test.go` and the roadmap show it implemented. These statements must be reconciled based on source and tests.
- Known explicit engine restrictions include `complexGateway`, `eventBasedGateway` with `eventGatewayType="Parallel"` or `instantiate="true"`, process-level conditional start, sequential ad-hoc ordering, and repeatable non-interrupting conditional triggers. See source in the baseline inventory; more gaps may be discovered.

## Architecture invariants, ADR gates and non-goals

Follow [AGENTS.md](../../AGENTS.md) and [invariants](../architecture/invariants.md): I1 no per-command hot-path allocations; I2 durable before visible; I3 single writer; I4 one deterministic `applyToState`; I5 compile don't interpret; I6 event facts and frozen routing/timestamps. Report generation, schema review, WCP mapping, and differential model translation stay off the processor hot path.

- Existing ADRs: [ADR-0033](../adr/0033-inclusive-gateway-join.md) (cyclic inclusive joins limited), [ADR-0110](../adr/0110-event-based-gateways.md), [ADR-0116](../adr/0116-terminate-end-events.md), [ADR-0125](../adr/0125-escalation-events.md), [ADR-0137](../adr/0137-conditional-events.md), [ADR-0138](../adr/0138-adhoc-subprocesses.md), [ADR-0508](../adr/0508-scope-aware-messaging-and-compensation.md).
- CON0–CON5 are cataloguing/test-contract work. No new engine ADR is needed merely to document a gap. **CON6 cannot change behaviour, persistence formats, routing or architecture without its own reviewed ADR(s)**, each with constraints and recovery proof.
- Non-goals for the planning PR: implementing missing BPMN semantics; marking any partly supported feature as complete; adding a bespoke BPMN renderer; claiming official OMG certification; counting arbitrary custom Worker behaviour as normative execution.

## Delivery order and acceptance

### CON0 — Normative inventory and source-to-evidence audit (first executable slice)

**Depends on:** none. **Review gate:** a maintainer accepts this charter and the owner/coordinator.  
- Reconcile `bpmn-conformance-feature-inventory.md` against the OMG §2 and §§8–13 inventory and XSD/CMOF, enumerating all applicable positions, flags, interactions and normative exclusions with clause anchors.
- Audit compiler parse/validation, compiled types, engine behaviors, modeler restrictions, existing tests, ADRs and roadmap; use repeatable searches and record paths (ideally test names) per row.
- Count and label all 43 WCP patterns as independent taxonomy. No guessed support from a similar element.
- **Acceptance:** no normative class or element family silently omitted; every row classified or explicitly `unknown`; unsupported/unreviewed examples visible; reviewer sign-off on denominator and scoping; signed baseline numbers in report. Deliver a reviewer-ready complete inventory with traceability.

### CON1 — Machine-readable catalogue and truthful coverage generator

**Depends on:** CON0.  
- Select JSON/YAML schema and stable IDs, allow normative hierarchy + test-level variants, validate unique IDs, normative anchors, references, statuses and reason-required omissions.
- Generate `conformance/COVERAGE.md` (or split normative matrix from existing scenario report), exposing **unregistered, unsupported, partial and untested**. Preserve the current scenario coverage generator as backward-compatible input or explicitly migrate it in one reviewed PR.
- Write tests that fail if an applicable normative row disappears, if a claimed pass lacks runnable evidence, if a known unsupported row becomes green, or if generated artifacts drift.
- **Acceptance:** a known `complexGateway` row appears unsupported and an unregistered escalation variant appears untested; generator/test gate detect omissions; summary denominators never rely solely on `Scenarios`; docs and gallery counts are aligned.

### CON2 — Supported feature execution scenarios and negative diagnostics

**Depends on:** CON1.  
- Add deterministic minimal positive fixtures for *supported* but missing event, task, data, lane/collaboration and structure variants; reuse other package tests as secondary evidence, not as substitutes for incomplete semantic oracles.
- Expand negative catalogue: unsupported elements/modes, malformed scope, dangling references, ambiguous defaults, illegal event positions, invalid combinations. Assert **diagnostic codes and locations**, not only non-nil errors.
- Extend drivers for multiple roots/subscriptions, instance selection, repeated tokens and event races without wall-clock sleeps or random scheduling. Upgrade reviewed expected outcome fixtures; never blindly regenerate baselines.
- **Acceptance:** each supported applicable variant either has a passing normative outcome fixture or is labelled `untested` with an issue; unsupported modes have explicit negative fixtures and no “supported” KPI credit.

### CON3 — Recovery, failure-injection and invariant oracles

**Depends on:** CON1; coordinates with CON2.  
- Separate finished-run live/replay *projection* from **full persisted-state equivalence**; compare relevant state families (instances and child scopes, element tokens, jobs, timers, subscriptions, incidents, variables, data objects, compensation and message facts) using deterministic canonical projections.
- Restart **before completion** at meaningful checkpoints (half-arrived joins, armed event races, active boundary/event subprocess, in-flight MI, cancellation, compensation); then drive live/recovered runs identically.
- Include crash ordering across WAL append/fsync/commit/side-effects where existing test seams permit; do not inject network effects into `applyToState`.
- **Acceptance:** at least one deterministic mid-flight/restart case per applicable stateful family; no orphan waits or externally visible side effects before fsync; exact recovery equivalence documented.

### CON4 — Independent semantic validation and portable TCK

**Depends on:** CON1, CON2.  
- Extend the opt-in `bpmn-engine` reference corpus beyond the current four cases where faithful translations are possible; clearly distinguish reference subset and extension dialect (Zeebe/FEEL versus JS).
- Curate explicit, spec-reviewed expected outcomes for complex boundary/cancellation/event cases; mark cases the independent engine cannot execute. Do not let independently translated cases inherit an atlas-generated expected value unreviewed.
- Ensure a dedicated CI job runs the differential tests and **fails** if prerequisites are absent in that job; retain deterministic local skip in non-differential jobs.
- **Acceptance:** published reference coverage counts, per-case provenance and reproducible CI; no misleading statement of external certification.

### CON5 — Public conformance report and documentation reconciliation

**Depends on:** CON1, CON2, CON3, CON4.  
- Generate one audit-ready report with conformance class, normative anchors, support, semantic evidence, restrictions, test confidence, known deviations and concrete implementation roadmap.
- Update main README, ROADMAP, `conformance/README.md`, `tck/README.md` and optional UI gallery **from truthful numbers**, without hand-maintained conflicting statistics.
- **Acceptance:** a reader can distinguish proven, partially proven, unsupported, out-of-scope and unknown; generated docs agree with registry; report references code/CI evidence, limitations and current revision.

### CON6 — Close genuine engine semantic gaps in separate, approved slices

**Depends on:** CON0, CON1; each implementation requires explicit ADR gate and CON2/CON3 test protocol.  
- Triage candidate gaps first (Complex Gateway, parallel/instantiating event gateways, repeatable conditional triggers, cyclic inclusive joins, sequential ad-hoc, conditional process start); confirm real scope and spec semantics individually.
- Prioritize correctness and Recovery over raw percentage. Each approved gap becomes a separate roadmap item/issue/ADR and **vertical** PR with compiler, engine, tests, modeler validity and replay, not a batch of unrelated semantic changes.
- **Acceptance:** only after accepted ADR, deterministic tests, full race/build/vet checks, reviewed standard semantics, recovery and documentation; programme must not mark supported merely because a negative rejection was added.

## Delivery rules and next action

Keep at most two active integration slices; today all slices are **planned**. Start **CON0** first after owner/coordinator confirmation. A finished planning PR is not completion of CON0. At each merge: update `docs/programs/bpmn-conformance/program.json` with actual status, issue/PR evidence, blockers and next step; regenerate `docs/programs/README.md` with `python3 scripts/programs.py`. For code, use red/green tests and run `go build ./...`, `go test ./...`, `go test -race -timeout=45m ./...`, `go vet ./...` and `gofmt -l .`.

**CON0 working evidence:** [normative crosswalk and source audit](bpmn-conformance-con0-audit.md), [OMG event legal-position matrix](bpmn-conformance-con0-event-matrix.md), and [all 43 Workflow Control-Flow Patterns](bpmn-conformance-con0-workflow-patterns.md). The unresolved per-attribute normative review, explicit denominator and maintainer acceptance are tracked in [Issue #1615](https://github.com/pblumer/atlas/issues/1615). Only an approved CON0 can unlock a conformance percentage; these documents do not assert a completed implementation.

**CON1 implementation evidence:** [candidate register](../../conformance/registry/registry.json), [generated normative candidate report](../../conformance/NORMATIVE_COVERAGE.md), [validator](../../scripts/conformance_registry.py) and [regression tests](../../scripts/test_conformance_registry.py). The two-step implementation was authorized together; CON1 output must not be accepted or treated as normative compliance until the CON0 reviewer and CI gates pass.

**CON2–CON4 working evidence (not yet accepted):** [exact diagnostic and execution oracles](../../conformance/normative_oracles_test.go), [event, end-event and flow execution oracles](../../conformance/normative_events_oracles_test.go), [exact negative diagnostics](../../conformance/normative_diagnostics_test.go), [midflight recovery and continuation](../../conformance/recovery_midflight_test.go), [independent differential test](../../conformance/differential/differential_test.go) and [reference translation](../../conformance/differential/reference/models/linear-independent.bpmn). These extend the reviewed **subset** of behavior and are not a completed statement-by-statement OMG assessment; consult [CON2 issue #1619](https://github.com/pblumer/atlas/issues/1619), [CON3 issue #1620](https://github.com/pblumer/atlas/issues/1620) and [CON4 issue #1621](https://github.com/pblumer/atlas/issues/1621).

**Second CON2/CON3 increment (2026-11-09, not yet accepted):** inline execution oracles now cover terminate end at root and subprocess scope, link throw/catch, an error end interrupting a concurrent sibling, escalation end (interrupting) and intermediate throw (non-interrupting), signal end, message end and intermediate message throw, exclusive-gateway default flow and the no-route incident, and uncontrolled flow without gateways. The mid-flight restart table grows from six to thirteen checkpoints (in-flight sequential multi-instance, compensation and transaction cancellation awaiting a handler, concurrent and half-answered event-subprocess runs, cancellation and compensation from an event subprocess). The register links these tests to 27 rows; ten rows move from `unverified` to `package-tested`. Implementation status is unchanged wherever the evidence covers only part of a row (FLOW-11, FLOW-13, FLOW-15, EVT-44); promoting it is a reviewer decision, not a consequence of a green test.

**Third CON2 increment (2026-11-09, not yet accepted): negative diagnostics.** `TestNegativeModels` only asserted that a model is refused; the new tests pin *which* diagnostic is given. Eight graph-wide faults (cross-scope flow, gateway without outgoing or incoming flow, boundary with incoming flow, cancel end outside a transaction, cancel boundary on a non-transaction, event gateway targeting a task, unresolvable timer-start schedule) are refused with a stable rule slug and the element id on both the deploy and the modeler path. Ten element-local faults (duplicate id, unknown error/escalation/signal/message reference, link throw without catch, unparsable condition, dangling flow, boundary on an unknown host, loop without bound) are refused, but under the generic `compile` rule with **no element field**; the id appears only in the message text. Three shapes the specification forbids are **deployed without error**: a start event with an incoming flow, an end event with an outgoing flow (the flow is silently never taken) and a gateway default that names a non-outgoing flow (ignored; the gateway parks with an incident and the modeler shows only the generic missing-default warning). These are characterised, not fixed: refusing them changes deploy acceptance and belongs in an ADR-gated slice (CON6 triage). Registry row FLOW-14 moves from `implemented` to `partial`.
