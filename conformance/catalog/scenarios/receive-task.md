# receive-task

Conformance micro-fixture: a receive task (a message wait modeled as an activity).

Start -&gt; receiveTask "await_reply" -&gt; End. Semantically like an intermediate message catch, but an activity — it can carry boundary events and I/O mappings. The token parks on the subscription (name "reply", constant correlation key "K") until a matching message arrives. Driver: Publish("reply", "K").

![receive-task diagram](../diagrams/receive-task.png)

- **Model:** [`receive-task.bpmn`](../../models/receive-task.bpmn)
- **Features:** `receive-task` (Receive task (message wait as an activity))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `reply` (key `K`).

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `await_reply` → `end`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
