# parallel-independent

Conformance micro-fixture: parallel fork/join over two independent effects.

Start -&gt; parallel fork -&gt; (set_x: = 1 -&gt; x) &amp; (set_y: = 2 -&gt; y) -&gt; parallel join -&gt; End. Realizes WCP-2 (Parallel Split) and WCP-3 (Synchronization).

Metamorphic partner of linear-independent.bpmn: two independent effect tasks run concurrently here and sequentially there, so the two models must reach the same effect projection (final variables + terminal state) despite different control-flow shapes. See the "equivalence" oracle in the runner.

![parallel-independent diagram](../diagrams/parallel-independent.png)

- **Model:** [`parallel-independent.bpmn`](../../models/parallel-independent.bpmn)
- **Features:** `parallel-gateway` (Parallel fork and synchronizing join), `script-task` (Inline FEEL script task (in-engine, no worker))
- **Control-flow patterns:** WCP-2 (Parallel Split), WCP-3 (Synchronization)
- **Metamorphic class:** `independent-effects`

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `fork` → `set_x` → `set_y` → `join` → `join` → `end`
- **Variables:** `x = 1`, `y = 2`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
