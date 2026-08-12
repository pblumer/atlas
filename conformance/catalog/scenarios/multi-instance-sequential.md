# multi-instance-sequential

Conformance micro-fixture: a sequential multi-instance activity.

Start -&gt; script "seed" (items = [1, 2, 3]) -&gt; sequential multi-instance service task "step" -&gt; End. A sequential multi-instance runs its iterations one at a time: only one job is ever active, so the driver completes them one after another with Complete("step") x3 — the very fact that each Complete resolves a single job (never an ambiguous set) is the sequential property. Contrast the parallel multi-instance, which activates every iteration's work at once.

![multi-instance-sequential diagram](../diagrams/multi-instance-sequential.png)

- **Model:** [`multi-instance-sequential.bpmn`](../../models/multi-instance-sequential.bpmn)
- **Features:** `multi-instance-sequential` (Sequential multi-instance activity)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `step`.
2. Complete job `step`.
3. Complete job `step`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `seed` → `step` → `step` → `step` → `step` → `end`
- **Variables:** `items = [1,2,3]`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
