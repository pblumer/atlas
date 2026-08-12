# timer-start

Conformance micro-fixture: a timer start event.

A duration timer (PT30S) starts a new instance: timerStart "on_schedule" -&gt; script "tick" -&gt; End. There is no CreateInstance call — the suite's Start hook arms the start timer, advances the clock past its due date with TimerStart(31s), and the timer births the instance, which the inline script self-completes.

![timer-start diagram](../diagrams/timer-start.png)

- **Model:** [`timer-start.bpmn`](../../models/timer-start.bpmn)
- **Features:** `timer-start` (Timer start event)

## How it is driven

**Start:** timer start — an armed timer firing after `31s` births the instance.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `on_schedule` → `tick` → `end`
- **Variables:** `fired = 1`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
