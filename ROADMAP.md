# Atlas Roadmap

This roadmap describes the intended evolution of Atlas. It is a direction, not a contract — order and scope will shift as the project learns. Milestones are deliberately vertical: each one should produce something that *runs*, not just a layer that sits unused.

Status legend: 🔲 not started · 🚧 in progress · ✅ done

---

## Milestone 0 — Foundations ✅

The skeleton that proves the three pillars fit together end to end.

- ✅ Project layout, module, CI (build, test, lint, vet, race detector)
- ✅ `model`: record header, `ValueType`/`Intent`, hand-written binary codec + round-trip tests
- ✅ `wal`: segmented append-only log, group commit (one fsync per batch), forward iteration
- ✅ `state`: Pebble-backed store, transactions, column-family/index helpers
- ✅ `engine`: single-writer processor loop, batch cycle, `ProcessingContext`
- ✅ `applyToState` used identically live and on recovery; crash-recovery test
- ✅ Minimal `compiler`: BPMN-XML parse → resolve → linearize to `CompiledProcess` (programmatic builder + `Parse`); deeper validation (reachability, gateway coverage) still to come
- ✅ Behaviors: none/start event, end event, sequence flow, **service task**
- ✅ `job`: dedicated `job` package — in-process worker subscription that pulls activatable jobs and feeds completions back (ADR-0007); gRPC streaming transport + leases/retry are Milestone 4
- ✅ **Goal: execute `Start → ServiceTask → End` and recover it across a restart** (deployment is programmatic for now, pending the XML front end)

## Milestone 1 — Core BPMN 🔲

The control-flow basics most real models use.

- 🔲 Exclusive gateway (conditions via compiled FEEL subset)
- 🔲 Parallel gateway (fork + join with scope counters)
- 🔲 Inclusive gateway
- 🔲 Input/output variable mappings
- 🔲 Variable scopes (local vs. propagated) with copy-on-write
- 🔲 `expr`: FEEL subset → AST evaluation, with `inputs` analysis
- 🔲 Compiler validation: reachability, gateway coverage, scope consistency
- 🔲 Conformance tests against a curated BPMN model set

## Milestone 2 — Events and timers 🔲

Making processes wait, react, and time out.

- 🔲 Timer events (date, duration, cycle) + due-date index scanning
- 🔲 Message events + subscriptions + correlation (single-partition)
- 🔲 Signal events (broadcast)
- 🔲 Error events and error propagation
- 🔲 Boundary events: interrupting and non-interrupting
- 🔲 Receive tasks
- 🔲 Incident model: raise/resolve, operator resume

## Milestone 3 — Structure 🔲

Composition and reuse.

- 🔲 Embedded subprocesses (scope lifecycle via child counters)
- 🔲 Event subprocesses (interrupting and non-interrupting)
- 🔲 Call activities (single-partition)
- 🔲 Multi-instance activities (sequential and parallel)
- 🔲 Compensation and compensation handlers
- 🔲 BPMN transactions (with cancel/compensation)

## Milestone 4 — Operability 🔲

What it takes to run this for real.

- 🔲 Public API surface (deploy, create instance, publish message, complete job, queries)
- 🔲 gRPC job-worker protocol (streaming pull, leases, fencing) — ADR-0007
- 🔲 Worker SDK (Go first)
- 🔲 Metrics (throughput, batch size, fsync latency, queue depth), structured logs, OTel traces
- 🔲 Log compaction / snapshotting so recovery doesn't replay from genesis
- 🔲 Exported-log stream for downstream analytics
- 🔲 Operator tooling: list/inspect instances, incidents, jobs

## Milestone 5 — Scale-out 🔲

Beyond a single node.

- 🔲 Networked inter-partition message transport (ADR-0006)
- 🔲 Cross-partition message correlation and call activities
- 🔲 Multi-node deployment, partition placement
- 🔲 Replication of the WAL for high availability
- 🔲 Partition rebalancing / failover
- 🔲 Idempotency/dedupe for delivered cross-partition messages

## Milestone 6 — Ecosystem 🔲

Adoption and polish.

- 🔲 Worker SDKs in more languages
- 🔲 BPMN modeler interoperability (import from common tools)
- 🔲 Benchmark suite and published performance numbers
- 🔲 Documentation site, tutorials, examples
- 🔲 1.0 API stability commitment

---

## Explicit non-goals (for now)

- A graphical BPMN modeler — Atlas executes models, it doesn't draw them.
- A batteries-included application server — the engine core is a library first.
- DMN decision evaluation as a product surface (FEEL is used internally for expressions).

## Guiding constraints

Every milestone must respect the architecture's load-bearing decisions:

- No allocation on the hot path; immutable compiled graphs; value tokens.
- Durable before visible (fsync → commit → side effects).
- Single writer per partition; cross-partition only via async messaging.
- Same `applyToState` live and on recovery.
