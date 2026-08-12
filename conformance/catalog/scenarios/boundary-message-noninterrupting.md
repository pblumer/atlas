# boundary-message-noninterrupting

Conformance micro-fixture: a non-interrupting boundary message.

Start -&gt; userTask "review" -&gt; End "done", with a non-interrupting boundary message ("ping", correlation key "K") on "review" routing to End "notified". When the message arrives the boundary spawns a *parallel* token to "notified" while the host keeps running — the defining property of non-interrupting. The instance finishes only once both tokens end. Driver: Publish("ping", "K") then Complete("review").

![boundary-message-noninterrupting diagram](../diagrams/boundary-message-noninterrupting.png)

- **Model:** [`boundary-message-noninterrupting.bpmn`](../../models/boundary-message-noninterrupting.bpmn)
- **Features:** `boundary-message-noninterrupting` (Non-interrupting boundary message event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `ping` (key `K`).
2. Complete job `review`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `review` → `pinged` → `notified` → `done`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
