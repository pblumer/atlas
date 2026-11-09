# CON implementation and acceptance runbook

**Status:** Proposed implementation instructions; no test or code work completed by this document.  
**Programme:** [BPMN Conformance](bpmn-conformance-program.md) · [source baseline](bpmn-conformance-baseline.md) · [feature inventory](bpmn-conformance-feature-inventory.md) · [operational manifest](../programs/bpmn-conformance/program.json).

## Working agreement

Begin each slice with an issue recording exact scope, coordinating owner, prerequisite PRs, accepted ADRs, acceptance test names and reviewed normative BPMN 2.0.2 clauses. Follow [AGENTS.md](../../AGENTS.md) and [programme delivery](../development/program-delivery.md). At most two integration slices at once; no architecture-changing engine work in CON0–CON5.

**Never convert absence of a failure into conformance.** A positive claim requires that the BPMN model compiles, executes to a **spec-reviewed** outcome, survives relevant recovery and that the test actually runs. A compiler rejection proves only rejection/diagnostics. A parser accepting or Modeler drawing a construct does not prove its execution.

## First PR: CON0 (normative inventory audit)

**Allowed surfaces:** `docs/planning/bpmn-conformance-*.md` and new `conformance/registry/` design/reference material; no compiler/engine mutations.

1. Pin current main SHA and OMG version. Enumerate normative Process Execution requirements and supported import-model elements; annotate section/table/paragraph anchors in the source PDF.
2. Reconcile full element/attribute/position matrix against the current seed. Review all 43 WCP patterns independently.
3. Perform code/test/ADR audit per row; capture any inconsistent claims as issues, not silent optimistic status updates.
4. Produce reviewed normative denominator and independent modeling/choreography/BPEL applicability decisions.
5. Record one explicit source/test/decision per supported or unsupported claim; unknown is allowed but must remain visible.

**Acceptance sample:** Register rows for (a) Terminate End: code present / central suite absent, (b) Complex Gateway: valid BPMN but rejected, (c) parallel event gateway: rejected, (d) Escalation: code and tests outside suite. All appear in generated preliminary gap summary; no normative feature family is silently unlisted.

## Second PR: CON1 (registry and generated reporting)

**Candidate surfaces:** `conformance/registry/*`, `conformance/coverage.go` and tests, generated `conformance/COVERAGE.md`, relevant docs, optional `scripts/conformance_*`. No execution change.

- Adopt a testable registry schema and stable IDs, with normative `specClause`, applicability, implementation, verification, limitation, source and tests.
- Normalize one row per semantically meaningful variant; link scenarios many-to-many rather than duplicating source of truth.
- Fail tests on duplicate IDs, missing normative anchor/review status, unknown status, dangling local refs, invalid WCP mapping, absent required feature, missing mandatory exclusion reason, and reporting drift.
- Prohibit a support percentage whose denominator is just the subset with scenarios.
- Render at least (1) OMG Process Execution (2) Modeling/Import (3) Choreography/BPEL scope (4) WCP (5) extension compatibility and (6) stateful recovery evidence separately.
- Preserve current `go test ./conformance` and `go test ./conformance -update` golden workflow or record a reviewed transition.

**Red test:** Remove/rename a known unsupported register row such as Complex Gateway: catalog completeness validation must fail, or an explicit removed-row review gate must require approval. A fake `implemented` value with no evidence must not turn green.

## Third PR: CON2 (executable semantics and diagnostics)

**Candidate surfaces:** `conformance/models`, `scenario.go`, `driver.go`, `golden`, `tck`, negative tests and `compiler` tests where needed.

- Positive spec test: enumerate inputs, observable activities/causal ordering, final state, variables and, when relevant, message/timer/subscription effects; review expectations **before** running atlas.
- Negative test: assert structured validation rule, element ID, location and reason; forbid false positives from unrelated syntax/encoding failures.
- Scenario-level evidence must state what it proves and **what it does not** prove.
- Driver must identify repeated MI jobs and concurrent scopes without ambiguous BPMN element IDs; advance a controlled clock, explicitly select a trigger/instance, and fail on unexpected extra waits.
- Capture both supported and unsupported variants in one report; no “gap disappears if it is unimplemented” loophole.

**Red test:** A boundary event written with wrong interrupting flag must fail its expected path assertions. A `complexGateway` fixture must be rejected with the **intended** unsupported-element diagnostic, not any arbitrary XML error.

## Fourth PR: CON3 (mid-flight recovery and full-state oracles)

**Candidate surfaces:** `conformance/runner.go`, recovery-specific helpers/tests and existing state inspection APIs. Any state schema change requires an ADR and is not a harmless test utility.

Minimum deterministic checkpoint matrix:

| Waiting/in-progress state | Recovery continuation must show |
| --- | --- |
| Half-arrived parallel/inclusive join | exactly one correctly synchronized continuation, no duplicate token |
| Event-based gateway with two armed catches | first legal winner alone proceeds; losing subscription/timer inert |
| Active boundary timer/message/conditional | correct interrupting/repeating behavior, no orphan wait |
| Nested event subprocess | independent local correlation, correct scope and parent outcome |
| Sequential/parallel MI partially completed | correct remaining iterations, ordered collected outputs |
| Call activity awaiting child | parent/child linkage and propagation retained |
| Transaction cancellation/compensation in flight | correct handler order and no repeated side effects |
| Job failed with incident/retry backoff | incident, job and timer recovered exactly |
| Message published but not yet consumed | applicable buffered/delivered facts and dedup are preserved |

Canonicalize **all relevant persisted families** (root and child instances, tokens, jobs, subscriptions, timers, incidents, variables, data objects, compensation). Compare actual recorded state before and after replay without re-evaluating routing, FEEL or time. Verify live resumed execution and replay-only state. If an existing API cannot inspect a family, explicitly report the missing oracle rather than claiming full-state equality.

## Fifth PR: CON4 (independent reference oracle)

**Candidate surfaces:** `conformance/differential/reference`, `differential_test.go` and a dedicated CI workflow.

Keep model translations readable, IDs traceable, driver steps deterministic and expected outcomes reviewed from the normative specification. Declare unsupported reference translations as `unavailable`, not `passed`. A designated differential CI job must install Node dependencies reproducibly and treat missing prerequisites as a **failure**, while regular Go runs need not depend on Node. Publish cross-engine scenario count and open disagreements.

## Sixth PR: CON5 (reports and claims)

**Candidate surfaces:** `conformance/COVERAGE.md`/README/TCK docs, top-level README, ROADMAP, optional Modeler gallery.

Review every number and explicitly say what the denominator includes. Differentiate: supported+proven, implemented+untested, partial, unsupported, unknown, invalid-by-spec, out-of-scope. Link normative anchors and current revision. An independent external audit should reproduce the report from pinned sources and test results.

## Seventh workstream: CON6 (genuine missing BPMN execution)

Treat **each semantic feature as its own ADR-gated vertical slice** after owner prioritization (not one huge PR). Start with an independent BPMN semantic test that fails for the intended reason; inspect architecture impacts before choosing representation. Changes requiring additional durable markers must preserve event-before-visible, single writer and one apply path. Update Compiler, Engine, Modeler validation, API, tests and Docs together; add mid-flight restart and performance/regression checks before closing the slice.

## Verification and delivery evidence

Run from repository root after code changes:

```sh
go build ./...
go test ./...
go test -race -timeout=45m ./...
go vet ./...
gofmt -l .
python3 scripts/programs.py --check
python3 -m unittest discover -s scripts -p 'test_programs.py'
```

Format `gofmt -l .` must print nothing. Test a dedicated reference-engine job separately when present (`go test -tags differential ./conformance/differential/` with installed dependencies). Do not claim checks succeeded if they were not executed. Planning-only PRs should validate local links, JSON manifest schema and generated programme index.

**After each PR:** record merged commit and actual test evidence in `docs/programs/bpmn-conformance/program.json`, update `ROADMAP.md` and accepted ADR statuses as appropriate, regenerate `docs/programs/README.md`, and leave a next-slice handoff. A merged PR is not automatically an accepted slice.
