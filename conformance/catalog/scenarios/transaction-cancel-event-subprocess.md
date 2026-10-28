# transaction-cancel-event-subprocess

Conformance micro-fixture: a cancellation that can arrive at any point of a transaction, handled by an event subprocess inside it whose cancel end rolls the transaction back (ADR-draft-scope-aware-messaging-and-compensation, building on ADR-0108).

Start -&gt; transaction "order" { ts -&gt; serviceTask "reserve" (compensable, handler "unreserve") -&gt; userTask "wait" -&gt; End }, with an interrupting message event subprocess "cancellation" inside the transaction { message start -&gt; cancel end }. The transaction's cancel boundary routes to a script task "bill_effort"; a normal finish to "invoice".

Driver: complete reserve, publish the cancellation while "wait" is parked, complete the compensation handler. The trace shows wait torn down, unreserve run, and the instance leaving through the cancel boundary — not through "invoice".

![transaction-cancel-event-subprocess diagram](../diagrams/transaction-cancel-event-subprocess.png)

- **Model:** [`transaction-cancel-event-subprocess.bpmn`](../../models/transaction-cancel-event-subprocess.bpmn)
- **Features:** `transaction-cancel-event-subprocess` (Cancel end in an event subprocess inside a transaction (cancellation at any point))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `reserve`.
2. Publish message `cancellation` (key `O1`).
3. Complete job `unreserve`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `order` → `cancellation` → `ts` → `order_cancelled` → `reserve` → `wait` → `cancellation` → `cs` → `cancel_end` → `unreserve` → `bill_effort` → `cancelled`
- **Variables:** `outcome = cancelled`

## What is verified

The outcome above is not asserted once — it is cross-checked by several
independent oracles that must all agree, so a wrong result cannot slip through:

- **Golden trace** — the exact path, variables, and data objects above are
  compared byte-for-byte against a committed golden file; any drift fails the suite.
- **Replay equivalence (invariant I4)** — the scenario runs live, then replays
  from its event log; both must reach an identical state, proving recovery is
  deterministic and loses nothing.
- **Structural invariants** — the finished run must leave no orphan tokens and
  honor the engine's six state invariants (see `docs/architecture/invariants.md`).

## Run it yourself

- **Locally:** `go test ./conformance -run 'TestScenarios/transaction-cancel-event-subprocess'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/transaction-cancel-event-subprocess`](../../tck/cases/transaction-cancel-event-subprocess) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
