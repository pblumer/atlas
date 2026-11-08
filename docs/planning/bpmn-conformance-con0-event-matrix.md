# CON0 — Event legality and evidence matrix (BPMN 2.0.2)

**Source:** [OMG BPMN 2.0.2](https://www.omg.org/spec/BPMN/2.0.2/PDF/) **Table 10.93, pp. 259–260** (PDF pages 288–289), and Tables 10.84–10.92, §10.5.4–10.5.5 and 13.5.  
**Programme:** [CON0](bpmn-conformance-program.md) · [Issue #1615](https://github.com/pblumer/atlas/issues/1615) · [normative audit](bpmn-conformance-con0-audit.md).  
**Status:** Legality transcribed from the official marker table; implementation evidence below remains **provisional**. No aggregate execution conformance claim.

The matrix has **13** families (10 EventDefinition subtypes, None, Multiple, Parallel Multiple), **8** positions and **63** legal family/position cells. The other **41** cells are **invalid by specification**, *not* missing implementation. Each legal cell can still have many behavioural/attribute variants; 104 cells is **not** a final semantic denominator.

## 1. Normative event/position legality

**L** = legal in OMG Table 10.93. **—** = not legal in this context.

| Family | Top Start | Event Sub int. | Event Sub nonint. | Catch | Boundary int. | Boundary nonint. | Throw | End |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| None | L | — | — | — | — | — | L | L |
| Message | L | L | L | L | L | L | L | L |
| Timer | L | L | L | L | L | L | — | — |
| Error | — | L | — | — | L | — | — | L |
| Escalation | — | L | L | — | L | L | L | L |
| Cancel | — | — | — | — | L | — | — | L |
| Compensation | — | L | — | — | L | — | L | L |
| Conditional | L | L | L | L | L | L | — | — |
| Link | — | — | — | L | — | — | L | — |
| Signal | L | L | L | L | L | L | L | L |
| Terminate | — | — | — | — | — | — | — | L |
| Multiple | L | L | L | L | L | L | L | L |
| Parallel Multiple | L | L | L | L | L | L | — | — |

## 2. Preliminary atlas source or test evidence by cell

**S** = central scenario exercises a subset; **C** = supporting compiler/engine code or package tests; **P** = partial semantics; **U** = explicit unsupported valid case; **?** = source status not reviewed enough; **—** = illegal by spec. Neither S nor C is an independent specification-conformance pass.

| Family | Top Start | Event Sub int. | Event Sub nonint. | Catch | Boundary int. | Boundary nonint. | Throw | End |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| None | S | — | — | — | — | — | C | S |
| Message | S | C | C | C | S | S | C | C |
| Timer | S | C | C | S | S | C | — | — |
| Error | — | C | — | — | S | — | — | C |
| Escalation | — | C | C | — | C | C | C | C |
| Cancel | — | — | — | — | S | — | — | S |
| Compensation | — | ? | — | — | S | — | C | S |
| Conditional | U | C | C | C | C | P | — | — |
| Link | — | — | — | C | — | — | C | — |
| Signal | S | C | C | S | S | C | C | C |
| Terminate | — | — | — | — | — | — | — | C |
| Multiple | ? | ? | ? | ? | ? | ? | ? | ? |
| Parallel Multiple | ? | ? | ? | ? | ? | ? | — | — |

The seed status table must be checked against precise code, not inferred from the fact a BPMN icon is available in bpmn-js. In particular, Multiple and Parallel Multiple are currently **unknown**, not assumed unsupported.

## 3. Mandatory special constraints

- **Cancel** boundary catch is only legal attached to a Transaction Sub-Process, and Cancel End only inside a transaction (§10.5.5). No Cancel in normal flow.
- **Compensation** Start is only legal within an Event Subprocess; a compensation boundary catches completed handler execution and has different sequence-flow rules from normal boundary catches.
- **Error** cannot be a root start, non-interrupting event-subprocess start or non-interrupting boundary event. These combinations are *spec-illegal*.
- **Link** is a paired intermediate catch/throw within a single scope; no start/end/boundary variant, no simultaneous incoming and outgoing sequence flows on one link.
- **Multiple** combines more than one EventDefinition; **Parallel Multiple** uses the `parallelMultiple` boolean attribute rather than its own event-definition subtype. Restrictions of the included triggers still apply.
- **Timer** subtypes timeDate, timeDuration, timeCycle have position-specific semantics. The current compiler expressly rejects timeCycle on an ordinary intermediate catch (`compiler/scope_compile.go`).
- **None** at an intermediate position is a **Throw**, not a normal Catch.
- **§10.5.4**: ordinary intermediate events with multiple incoming flows exhibit uncontrolled-flow semantics; boundary events have no incoming flow.

## 4. Source anchors and remaining validation

| Family/variant | Direct source or test | Audit note |
| --- | --- | --- |
| Root conditional start | `compiler/conditional_test.go:TestParseRefusesAProcessLevelConditionalStart` | Explicitly unsupported, unlike event-sub conditional |
| Conditional catch/boundary/event sub | `engine/conditional_test.go:TestConditionalCatchRecovers` and `TestConditionalBoundaryNonInterruptingFiresOnce`; ADR-0137 | Repeatable noninterrupting activation limited |
| Escalation throw/end/boundary/event sub | `compiler/escalation_test.go:TestParseEscalationEndAndBoundary`, `engine/escalation_test.go:TestEscalationBoundaryRecovers` | Source tested but central registry missing |
| Cancel transaction end and boundary | `conformance/scenario.go:transaction-cancel` | Other transaction variants unverified |
| Terminate end | `compiler/parse_test.go:TestParseTerminateEndCompiles` | No central scenario |
| Link catch and throw | `compiler/process.go:TypeLinkThrowEvent` and ADR-0132 | No central scenario |
| Message/timer/signal | `conformance/scenario.go` and `engine/behavior.go` | Position, payload, repeating timing and parallel trigger variants missing |
| Multiple/Parallel Multiple | §10.5.4–10.5.5, §13.2 | No evidence sufficient for support classification |

**CON0 completion gates still outstanding:** expand the **63** legal cells into attribute/trigger/interruptibility/repetition/nesting variants; inspect exact code and tests per row; independently review expected semantics and reject illegal variants with correct diagnostics; obtain owner approval on the normative denominator before CON1 calculates anything. A successful compilation of one example is not a pass for its entire cell.
