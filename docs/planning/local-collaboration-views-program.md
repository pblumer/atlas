# Local collaboration views: delivery programme (LCV)

**Programme:** Local collaboration views (ID: LCV)
**Milestone:** [Milestone 2 — Events and timers](../../ROADMAP.md#milestone-2--events-and-timers-).
**Architecture authority:** [ADR-0559](../adr/0559-local-collaboration-views.md) (**Accepted / Partial**).
**Programme owner:** pblumer (accepted 2026-11-11). **LCV1:** active (owner pblumer). LCV2 to LCV4
are planned and have no implementation owner yet.

## User outcome

A modeller or an operator opens a process and sees who it talks to on this installation:
the other processes, also when they were deployed from other files, and the external
parties that send to it, each exchange with what the definitions make possible and what
the event log shows happened. With every process collapsed, a replay shows only the
messages travelling between them, including the ones nobody received. A modeller may
save such a picture as a collaboration, and Atlas says where it has drifted from the
processes it shows.

## Constraints

- **No engine change.** Nothing new on the command path, in the WAL or in `applyToState`
  (invariants I1, I2, I4). Every slice reads what ADR-0446 and ADR-0472 already record.
- **Reads off the loop.** Anything that grows with the traffic runs on a `state.ReadView`
  with the loop free, and is bounded (`AGENTS.md`, ADR-0080).
- **One rule for who reads which row.** The event log's rule (ADR-0472 §5), through
  `eventLogReach`, for every view built on it.
- **Three layers, never merged** (ADR-0559 §1): possible, observed, intended.
- **Scale-out:** the record's assessment holds. The first slices are standalone-only by
  construction (one partition); serving more than one partition needs the seam the record
  names, in its own record.

## Non-goals

A contract checked on deploy, executable collaborations, signals and call activities as
edges, Worker ports, a replay of the whole installation (Panorama's), history older than
the event-log window.

## Slices

### LCV1 — Interaction graph around one process

**Delivers:** `GET /api/v1/process-interactions?processId=…&window=…` and the MCP tool
`atlas_process_interactions`. The answer holds the participants (the process, the processes
it exchanges names with, the black boxes), each exchange with `possible` and `observed`
(arrivals, instances started, last arrival), the sends that reached nobody, and the
process's message ports. ADR-0559 is accepted with this slice.

**Acceptance:**
- Two processes deployed from two files, linked by message names, appear as one exchange
  each way, possible and observed (`api/interactions_http_test.go`).
- A project member reads only what the event log shows them; the rest is a nameless
  `outside-view` participant (same test).
- The scan is bounded and says where it stopped; rows outside the window are not read;
  the answer is stable (`api/interaction/interaction_test.go`).

### LCV2 — Operations view, collapsed participants and the message-only replay

**Delivers:** ADR-0559 §7 and §8 in the Operations app, over the LCV1 route plus a
time-ordered read of the graph's rows for the replay.

**Acceptance:** a browser end-to-end test that opens the view collapsed, expands one
process, plays a replay with a delivery, a start and a send that reached nobody, and shows
"outside your view" for a reader without access; the history's beginning is stated.

### LCV3 — Saved collaboration and its findings

**Delivers:** ADR-0559 §5. Starts with the storage decision (draft kind or own store),
recorded in the ADR's *As built*.

**Acceptance:** each finding has a failing-then-passing test; export, adopt and delete of
an application include the document (ADR-0544, ADR-0467, ADR-0468).

### LCV4 — Composing a collaboration step by step

**Delivers:** ADR-0559 §6 in the Modeler.

**Acceptance:** an end-to-end test that builds a collaboration of two processes from one
process's ports, names a placeholder, and sees a drift finding after a redeploy.

## Handoff

Each slice's pull request updates [the manifest](../programs/lcv/program.json), this plan,
ADR-0559's *As built*, `ROADMAP.md` and `CHANGELOG.md`, and leaves the handoff the
[Programme Delivery Protocol](../development/program-delivery.md) requires.
