# event-gateway-message

Conformance micro-fixture: an event-based gateway (deferred choice).

Start -&gt; eventBasedGateway -&gt; races a message catch ("go", keyless) against a timer catch (PT30S). Whichever fires first wins; the other is canceled. The outcome is decided by the environment, not by data — that is the deferred choice. One model, two scenarios: the message-driven run reaches "end_msg", the timer-driven run reaches "end_timeout".

![event-gateway-message diagram](../diagrams/event-based-gateway.png)

- **Model:** [`event-based-gateway.bpmn`](../../models/event-based-gateway.bpmn)
- **Features:** `event-based-gateway` (Event-based gateway (deferred choice))
- **Control-flow patterns:** WCP-16 (Deferred Choice)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `go`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `gw` → `got_msg` → `timed_out` → `end_msg`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
