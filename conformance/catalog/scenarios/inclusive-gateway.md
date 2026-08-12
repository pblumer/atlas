# inclusive-gateway

Conformance micro-fixture: an inclusive (OR) gateway.

Start -&gt; script "seed" (order = {priority: true, region: "EU"}) -&gt; inclusive split -&gt; up to three branches -&gt; inclusive join -&gt; End. The split opens every branch whose condition holds (priority, EU region) plus the default only if none do; the join waits for exactly the branches that opened, then fires once. Seeded so priority and EU both hold and the default "standard" branch is suppressed — the join synchronizes those two. Self-completing (inline scripts). Realizes WCP-6 (Multi-Choice) and WCP-7 (Structured Synchronizing Merge).

![inclusive-gateway diagram](../diagrams/inclusive-gateway.png)

- **Model:** [`inclusive-gateway.bpmn`](../../models/inclusive-gateway.bpmn)
- **Features:** `inclusive-gateway` (Inclusive (OR) gateway split and synchronizing join)
- **Control-flow patterns:** WCP-6 (Multi-Choice), WCP-7 (Structured Synchronizing Merge)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `seed` → `split` → `priority` → `eu_check` → `join` → `join` → `end`
- **Variables:** `eu = done`, `order = {"priority":true,"region":"EU"}`, `prio = done`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
