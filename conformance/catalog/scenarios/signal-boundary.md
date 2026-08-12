# signal-boundary

Conformance micro-fixture: an interrupting boundary signal event.

Start -&gt; parallel fork -&gt; two branches: A: userTask "review" with an interrupting boundary signal "abort" -&gt; "aborted" B: script "prep" -&gt; intermediate signal throw "abort" -&gt; "thrown" When branch B throws "abort", branch A's boundary signal fires; being interrupting it cancels the still-parked "review" and routes to "aborted". The script keeps the throw one hop behind the boundary's arming, so the boundary is always subscribed first — self-completing, no driver. The user task is never completed; being interrupted is the point.

![signal-boundary diagram](../diagrams/signal-boundary.png)

- **Model:** [`signal-boundary.bpmn`](../../models/signal-boundary.bpmn)
- **Features:** `signal-boundary` (Interrupting boundary signal event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `fork` → `review` → `prep` → `on_abort` → `raise_abort` → `aborted` → `thrown`
- **Variables:** `ready = 1`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
