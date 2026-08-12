# linear-independent

Conformance micro-fixture: two independent effects in a straight line.

Start -&gt; (set_x: = 1 -&gt; x) -&gt; (set_y: = 2 -&gt; y) -&gt; End.

Metamorphic partner of parallel-independent.bpmn. The two tasks do not depend on each other, so serializing them (here) must yield the same effect projection as running them concurrently (there).

![linear-independent diagram](../diagrams/linear-independent.png)

- **Model:** [`linear-independent.bpmn`](../../models/linear-independent.bpmn)
- **Features:** `sequence-flow` (Sequence flow between activities), `script-task` (Inline FEEL script task (in-engine, no worker))
- **Control-flow patterns:** WCP-1 (Sequence)
- **Metamorphic class:** `independent-effects`

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `set_x` → `set_y` → `end`
- **Variables:** `x = 1`, `y = 2`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
