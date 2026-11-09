# BPMN 2.x conformance baseline — source audit

**Date:** 2026-11-08. **Repository:** `pblumer/atlas` default branch (code inspection only).  
**Status:** Initial evidence-based audit; NOT a verified OMG conformance assessment, test execution result or normative-complete inventory.  
**Programme:** [CON](bpmn-conformance-program.md) · [Inventory seed](bpmn-conformance-feature-inventory.md).

## Distinguish four claims

1. **Normative requirement** — derived from a clause of OMG BPMN 2.0.2, with applicable position/variant and conformance class.
2. **Engine implementation** — supported, partial, explicitly unsupported or unknown according to inspected compiler/engine source.
3. **Testing evidence** — actual reproducible scenario/test, *not* simply a code path or roadmap checkmark.
4. **Suite registration** — present in `conformance/scenario.go`, independently from all other tests.

“Covered” in `conformance/COVERAGE.md` means *a registered feature name is mapped to >=1 registered scenario*. Its “Gaps: None” means only *no absent coverage inside that small closed list*. It does not measure OMG conformance.

## Reproducible initial figures

| Measure | Inspected source | Value and qualification |
| --- | --- | --- |
| Registered feature IDs | `conformance/scenario.go` (`Features`) | 34 |
| Registered WCP IDs | `conformance/scenario.go` (`Patterns`) | 9 |
| Registered positive scenarios | `conformance/scenario.go` (`Scenarios`), generated `conformance/catalog/scenarios/README.md` | 34 |
| Registered negative models | `conformance/scenario.go` (`NegativeModels`) | 4 |
| Opt-in independent translations | `conformance/differential/differential_test.go` (`subset`) | 4: `sequence`, `exclusive-gateway`, `parallel-independent`, `inclusive-gateway` |
| BPMN execution conformance denominator | OMG `2.3 plus position/variant inventory | **Unknown until CON0** |
| BPMN support percentage | Normative denominator and semantic evidence pending | **Not calculated** |

**Existing oracles:** `conformance/conformance_test.go` runs goldens, a replay projection and a no-orphan check for completed/terminated instances, plus a metamorphic group. `conformance/runner.go` captures the **root** instance `State` / `Path` / `Variables` / `DataObjects`; it is not a canonical diff of all persisted store families. `conformance/negative_test.go` accepts any compile error, not an expected validation code/location. `conformance/differential/differential_test.go` runs only under `-tags differential` and skips when Node/dependencies are absent. TCK expected outcomes are generated from atlas result values (`conformance/tck_test.go`), so a golden alone is not an independent semantic oracle.

## Engine-supported but not centrally registered (documented/code-supported)

The following are **candidate registration gaps**, not claims of full variant-level correctness. Each row must gain a normative clause, concrete source/tests and a reviewed oracle under CON0–CON2.

| Candidate | Evidence and known limitations |
| --- | --- |
| Terminate End Event | `compiler/parse_test.go`: `TestParseTerminateEndCompiles`; `ROADMAP.md` Milestone 2. Conformance README still incorrectly says rejected. |
| Escalation throw/end/boundary/event-subprocess | `compiler/escalation_test.go`; `docs/adr/0125-escalation-events.md`; no entries in `Features`. |
| Conditional catch/boundary/event subprocess | `compiler/conditional_test.go`; `docs/adr/0137-conditional-events.md`; repeatable non-interrupting activation limitation. |
| Link intermediate throw/catch | `docs/adr/0132-link-events.md`; compiler resolves paired links at deploy. |
| Message throw/end and Error End | `ROADMAP.md` Milestone 2, `docs/adr/0052-message-end-events.md` and `docs/adr/0089-error-events.md`; not separately registered. |
| Send Task | `compiler/scope_compile.go`, `docs/adr/0112-send-tasks.md`. Job-backed vs message-kind must be separate variants. |
| Manual/undefined Task | `compiler/parse_test.go` `TestParsePlainTaskPassThrough`; task-as-pass-through is a semantic choice requiring spec review. |
| Business Rule Task | `compiler/process.go` / `engine/behavior.go`, `docs/adr/0014-dmn-business-rule-tasks-via-temis.md`; DMN itself tracked separately. |
| Ad-hoc Subprocess | `compiler/adhoc_test.go`, `docs/adr/0138-adhoc-subprocesses.md`; sequential ordering refused. |
| Compensation End | `ROADMAP.md` Milestone 3; current fixture covers another compensation path. |
| Timer date/cycle/cron and repeating non-interrupting boundaries | `ROADMAP.md` Milestone 2; current suite only has a narrow timer catch/start and interrupting boundary. Cron is an extension variant, not normative BPMN time grammar. |
| I/O mappings, variable scopes, collections and data lineage | `docs/adr/0068-task-io-variable-mappings.md`, `docs/adr/0058-data-output-associations.md`, `docs/adr/0059-data-input-associations.md`, `docs/adr/0060-field-level-data-object-writes.md`; current suite tests only three data-object scenarios. |
| Collaboration/pools and lanes | `ROADMAP.md` Milestone 2/3, `docs/adr/0023-collaborations-and-pools.md`, `docs/adr/0121-bpmn-lanes.md`; collaboration validation remains incomplete. |

## Confirmed explicit limitations in current source

| Construct / semantic variant | Source | Current behavior | Testing implication |
| --- | --- | --- | --- |
| `complexGateway` | `compiler/scope_compile.go` (~line 915), `compiler/parse_test.go` `TestParseUnsupportedElementMessage` | Error: can't execute yet | Negative semantic-compatibility case; no support credit |
| Event gateway with `eventGatewayType="Parallel"` | `compiler/validation.go` (`checkEventGatewayKinds`), `compiler/eventgateway_instantiate_test.go` | Validation error | Register distinct variant |
| Event gateway with `instantiate="true"` | Same | Validation error | Register distinct variant |
| Root `conditional` start | `compiler/validation.go` (`checkConditionalStarts`), `compiler/conditional_test.go` | Validation error; event subprocess conditional start is different and supported | Separate context rows |
| Sequential ordering of ad-hoc subprocess | `compiler/adhoc_test.go`, `ROADMAP.md` Milestone 2 | Rejected at deploy | Register variant, do not pass by modelling as parallel |
| Repeatable non-interrupting conditional boundary/event subprocess | `docs/adr/0137-conditional-events.md`, `ROADMAP.md` still open | Once per arm, no repeated false→true trigger | `partial` status with explicit regression scenario |
| Cyclic inclusive gateway synchronization | `docs/adr/0033-inclusive-gateway-join.md`, `ROADMAP.md` Milestone 1 | ADR scope limited to acyclic topology | Needs topology-dependent test; do not overstate support |
| BPMN data store runtime write and parts of read | `ROADMAP.md` Milestone 1 data-store track | Declarative read surface; write refused; runtime read follow-up | Separate metadata vs runtime semantics |
| Atomic multi-pool deployment/message flow validation | `ROADMAP.md` Milestone 3 | Follow-up outstanding | Distinguish valid collaboration import from runtime communication |

## Document drift to remove in CON1/CON5

- `README.md` “31 features” vs 34 in `scenario.go`.
- `ROADMAP.md` Milestone 1 conformance prose still cites 30 scenarios and an old terminate rejection.
- `conformance/tck/README.md` says “30 positive scenarios” though generated catalogue lists 34.
- `conformance/README.md` implies escalation “not supported yet” and terminate rejected; both claims contradict current compiler/roadmap.
- `docs/ARCHITECTURE.md` `BPMN coverage` is a phase-level description, not a normative report and should not be substituted for one.
- Separate stale statements in *old ADR context* from their explicit implementation-status headers; immutable historical rationale must not be silently rewritten.

## Outstanding audit items (no premature classification)

The source check did **not** establish a complete status for Multiple/Parallel-Multiple events, all Error/Compensation/Event position variants, Choreography, Conversation models, full XML schema/DI round trip, every Data Store operation, and all WCP semantic variants. These are explicit **unknown/needs-review** entries in the seed inventory, not inferred unsupported.

Check standard **abstract classes versus concrete event positions**: a definition may be legal only in some contexts. Unsupported input must be rejected with a diagnostic; a syntactically accepted model must not silently degrade to a different BPMN behaviour.

## Validation and evidence handoff

This audit used repository inspection. No Go build, test, race run or differential job has been executed during the planning change. First slice CON0 must freeze a source revision, run targeted compiler and engine tests, review normative text and record a reproducible script/test evidence set. For every future gap: link an issue, explicit owner, conformance class, original normative clause, implementation evidence, actual test case, expected outcome, restart checkpoint if applicable, and accepted limitation or ADR decision.
