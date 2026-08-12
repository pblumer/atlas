# message-catch

Conformance micro-fixture: an intermediate message catch event.

Start -&gt; intermediateCatchEvent "await_payment" -&gt; End. The token parks on a message subscription (name "payment-received", constant correlation key "K") until a matching message arrives. The driver delivers it with Publish("payment-received", "K", ...). Exercises the message driver path.

![message-catch diagram](../diagrams/message-catch.png)

- **Model:** [`message-catch.bpmn`](../../models/message-catch.bpmn)
- **Features:** `message-catch` (Intermediate message catch event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `payment-received` (key `K`).

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `await_payment` → `end`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
