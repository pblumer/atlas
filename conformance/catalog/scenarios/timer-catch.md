# timer-catch

Conformance micro-fixture: an intermediate timer catch event.

Start -&gt; intermediateCatchEvent "wait" (duration PT30S) -&gt; End. The token parks until the timer is due. The driver advances the clock past the due date with Wait(31s) and fires due timers. Exercises the timer driver path.

![timer-catch diagram](../diagrams/timer-catch.png)

- **Model:** [`timer-catch.bpmn`](../../models/timer-catch.bpmn)
- **Features:** `timer-catch` (Intermediate timer catch event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Advance the clock by `31s`, firing any timer that comes due.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `wait` → `end`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
