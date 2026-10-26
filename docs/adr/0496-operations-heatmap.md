# ADR-0496: The Operations heatmap counts arrows and splits a task's time

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-26
- **Deciders:** Atlas maintainers

## Context and problem statement

The Operations view draws where tokens are and where they have been: every element
carries a live count, a "passed through" count and a cancelled count, read from the
maintained per-element counters (ADR-0022, ADR-0080, ADR-0249). It answers "did a
token get here", and it answers it for shapes only, in two states — gray for history,
green for live.

What an operator asks of a running process is different, and comes in two parts:

- **Which paths are used, which are never used?** A heat map in the ordinary sense —
  the busy parts of the diagram hot, the rare ones cool, the unused ones visibly cold.
  For that the arrows matter as much as the shapes: a gateway's branches are told apart
  by the flows that leave it, and the engine counted no flows. The Playground draws such
  a map (ADR-0215), but it folds its flow counts out of the lifecycle trail of a sandbox,
  which is no answer for a server holding years of history.
- **Where does the time go?** How long a task takes from activation to completion, and
  — for the work a person or a Worker picks up — how much of that is waiting for
  somebody to start and how much is the work. The lifecycle trail (ADR-0136) has the
  activation and the completion. Nothing has the moment somebody took the task: the job
  keeps only who holds it now (ADR-0042, ADR-0007), and is deleted when it completes.

The statusbericht of 2026-10-10 lists the first as item 803, "Heatmap liest nur
Besuche".

## Decision drivers

- **The hot path stays a merge and a set.** A new counter is a write-only merge, a new
  history fact a single set under a unique key (I1).
- **Derived from the event alone.** Both new facts are folded in `applyToState` from the
  header and the payload, so replay rebuilds them (I4, I6).
- **The diagram's meaning, not the engine's mechanics.** An event-based gateway arms
  every branch; the diagram means one arrow taken.
- **Statistics never hold the run loop.** Anything that grows with history runs on a
  snapshot (ADR-0266, ADR-0080's rule).
- **No statistics about people.** Times are aggregated per task, never per assignee.

## Considered options

1. **Fold everything on request** from the lifecycle trail, as the Playground does —
   flows, durations, and (with a new fact) the take-up moment.
2. **Maintain everything as counters**: per-flow traversals, and per-element sums and
   histogram buckets of duration, waiting and working.
3. **Counters for what is cumulative, a fold for what is windowed**: a per-flow traversal
   counter beside the per-element ones, a per-instance take-up history, and a statistics
   endpoint that folds a bounded window of history off the loop.

## Decision outcome

Chosen option: **3**.

### Flow counter

`cfFlowTakenAgg` keys a signed counter by `(processDefKey, flowIndex)`. `applyToState`
merges `+1` when an element instance activates over a flow (`SourceFlowId >= 0`) —
except a catch armed by an event-based gateway (`EventGatewayKey != 0`), whose flow is
counted when it *completes*: the race was decided in its favour. A losing branch is
terminated and never counted; an undecided race counts for no branch. The counter is
never decremented and survives a history purge, like the element aggregates (ADR-0486).

The compiled process now keeps each sequence flow's BPMN id
(`CompiledProcess.FlowBpmnId`), the way it always kept each element's; the goto a link
event compiles to (ADR-0133) has no arrow and no id.

The runtime overlay (`GET /api/v1/processes/{key}/runtime`) carries `flows`: for the
whole definition from the counter, for one isolated instance folded from that instance's
trail with the same rule.

A store upgraded into the counter reconstructs it once from the trail
(`backfillFlowTakenIfNeeded`), or every arrow of a long-running process would read
"never reached" between shapes with years of visits. The trail does not say which
activations were armed by a gateway; a branch still waiting is read off its live
element instance, and a cancelled branch is not counted when a sibling armed by the same
token completed. A parallel branch torn down while its sibling finished looks the same
and goes uncounted; that imprecision is accepted once, for history.

### Take-up history

`cfJobTake` keeps, per process instance and in log order, every time a task's job is
taken up — a claim with an assignee (ADR-0042), a lease by a Worker (ADR-0007) — or
handed back: an unclaim, a lease that ran out, a failure reported by the Worker. Each
fact names the element instance whose job it was. A task the model assigns when it
opens is not "taken": it is in somebody's inbox, not in their hands. The facts are
purged with the instance's history (ADR-0146).

### Statistics endpoint

`GET /api/v1/processes/{key}/statistics?from=&to=` folds, off the loop, the instances of
one definition that were running or finished within the window — newest first, at most
`maxStatisticsInstances`, with `truncated` saying when the bound cut it short — and
answers, for every element and flow of the model (zeroes included, which is the
"never reached" half of a heat map):

- **runs**: activations in the window; for a flow, traversals under the counter's rule;
- **duration**: activation → completion of the element instances completed in the
  window;
- **waiting** and **working**: for those whose last take-up before completion was not
  handed back again, activation → take-up and take-up → completion;
- for the instances: started, finished, running, and their duration.

Each time is reported as count, mean, median, 90th percentile, maximum and total — the
median because waiting times are skewed by nights and weekends, the total because mean ×
count is the load a capacity plan needs. An instance migrated to this definition
(ADR-0162) contributes only what happened after it arrived, because its earlier element
indices belong to another version's graph.

### The overlay

The Operations view gains an overlay strip over the diagram: **Live** (the view as it
was), **Runs**, **Waiting**, **Working** and **Duration**; a style, **Glow** — halos under
the shapes and arrows that add up where the traffic is — or **Outline**, the Playground's
five shaded strokes; and a period, *since deployment* (the counters, exact and kept past
a purge) or a window (the endpoint). The scale is relative to the largest value on
screen, on a square root, so a branch taken 33 times out of 1 248 still shows. Zero is
drawn dashed: "never reached" is a different statement from "reached least". A panel
beside the diagram lists the figures behind the colours.

### Consequences

- **Positive:** item 803 is closed; the arrows carry the same retained history as the
  shapes; the bottleneck question — waiting or working — is answered from facts the
  engine records rather than from a model somebody typed in, as the Playground has to.
- **Negative / trade-offs accepted:** one more merge per activation over a flow and one
  more set per take-up; the take-up history starts with the upgrade — there is no trail
  to rebuild it from, so waiting and working stay empty for older tasks; a task
  completed without being claimed (allowed — completing does not require a claim)
  contributes a duration but no split; a window over history beyond the bound or past
  the retention period (ADR-0146) sees less than happened, and says so through
  `truncated` and the scanned count, not silently.
- **Follow-ups / risks to watch:** a fixed scale (per cent of started instances) as an
  alternative to the relative one, so two periods can be compared; flow counts on the
  `diagrams`-token answer (ADR-0493), which deliberately carries no new field until
  somebody decides it should; working-time calendars.

### As built

- **Compiler:** `CompiledProcess.FlowBpmnId` and `FlowCount`; `Builder.SetFlowBpmnId`,
  called for every authored `<sequenceFlow>` (`compiler/scope_compile.go`).
- **State:** `cfFlowTakenAgg` (0x36) with `Tx.IncFlowTakenAgg` and `FlowTakenTotals`;
  `cfJobTake` (0x37) with `Tx.RecordJobTake`, `JobTakeHistory` and the kinds `JobTaken` /
  `JobReleased`, purged by `PurgeInstanceHistory`; the one-time reconstruction
  `backfillFlowTakenIfNeeded`, marked `flow_taken_v1`.
- **Engine:** the flow counter on element activation and on an armed catch's completion,
  and `jobTakeKind` for `JobActivated`, `JobAssigned`, `JobFailed` and `JobTimedOut`
  (`engine/apply.go`).
- **API:** `flows` on `GET /api/v1/processes/{key}/runtime`;
  `GET /api/v1/processes/{key}/statistics` (`api/processstats.go`), bounded by
  `maxStatisticsInstances` = 10 000; the MCP tool `atlas_process_statistics`.
- **Console:** `api/web/heatoverlay.js`, wired into the live view (`mountLive`); covered by
  `e2e/heatmap-overlay.spec.mjs`.

### As built, second slice: the figures are kept, not re-read

The first slice let every open tab re-read the statistics every fifteen seconds, and each
read folded up to ten thousand instances' histories again — about 70 µs per small
instance, so most of a second of CPU and some 20 MB of garbage at the bound, per tab.
Nothing the fold reads about a finished instance can change, so:

- **The Console reads the figures when asked:** when the heatmap opens and when the period
  changes, then every five minutes while the tab is visible, or on **Refresh**; the strip
  says when they were folded. A window's start is rounded to the minute, so tabs asking
  within one minute name the same window.
- **The endpoint shares a result** for a minute per (definition, window) — a window ending
  now is one window for that minute — and requests arriving while a fold runs wait for it
  (`statsResultCache`, at most 256 results; a failure or an unknown definition is never
  kept).
- **A finished instance's contribution is folded once and kept** (`instanceStatsCache`,
  least recently used out, at most twice the per-fold bound). It is the contribution to
  every window that holds the instance's whole trail; an instance still running, or one a
  window cuts, is folded for that window alone. Nothing invalidates an entry: a finished
  trail is fixed, keys are never reused, and a purged instance is never asked for again.
- **The runtime overlay folds an isolated instance's flows on a snapshot** after the loop
  turn rather than inside it, since a looping instance's trail grows without bound.
- **The glow layer is rebuilt only when its values change**, rather than on every poll.

The engine's own cost was measured unchanged: `BenchmarkInstanceLifecycle` ran at
2.29 ms per instance before the first slice and 2.30 ms after, two more allocations each.

## Pros and cons of the options

### Option 1 — fold everything on request
- Good: no new state for flows.
- Bad: "since deployment" becomes a walk over the whole history on every poll, and it
  forgets whatever a purge removed, so the picture shrinks as retention works.

### Option 2 — counters for everything
- Good: every view O(elements).
- Bad: a window ("the last 30 days") is impossible without time-bucketed counters;
  percentiles need histograms whose buckets are a guess; a duration counter needs the
  activation time on the completion event, which the element instance does not carry.

### Option 3 — counters for the cumulative, a fold for the windowed (chosen)
- Good: the always-on overlay stays O(flows); windows and percentiles are exact over what
  the fold reads; each new piece of state is one family with one writer.
- Bad: a window costs a bounded walk; the bound can truncate a busy definition's window.

## Links

- extends ADR-0022, ADR-0080 and ADR-0249 (the element counters) to sequence flows
- relates to ADR-0136 (the lifecycle trail both folds read), ADR-0042 and ADR-0007 (claims
  and leases), ADR-0215 (the Playground's heat map, whose shading the outline style reuses)
- relates to ADR-0146 and ADR-0486 (what a purge keeps), ADR-0162 (migrated instances)
