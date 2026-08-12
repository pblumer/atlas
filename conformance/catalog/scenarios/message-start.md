# message-start

Conformance micro-fixture: a message start event.

A message named "order-placed" starts a new instance: messageStart "on_order" -&gt; script "record" -&gt; End. There is no CreateInstance call — the instance is born from the message. The suite's Start hook publishes it with MessageStart("order-placed", ""); the inline script then self-completes the run.

![message-start diagram](../diagrams/message-start.png)

- **Model:** [`message-start.bpmn`](../../models/message-start.bpmn)
- **Features:** `message-start` (Message start event)

## How it is driven

**Start:** message start — publishing `order-placed` births the instance.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `on_order` → `record` → `end`
- **Variables:** `recorded = 1`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
