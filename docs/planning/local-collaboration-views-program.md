# Local collaboration views: delivery programme (LCV)

**Programme:** Local collaboration views (ID: LCV)
**Milestone:** [Milestone 2 — Events and timers](../../ROADMAP.md#milestone-2--events-and-timers-).
**Architecture authority:** [ADR-0559](../adr/0559-local-collaboration-views.md) (**Accepted / Partial**).
**Programme owner:** pblumer (accepted 2026-11-11). **LCV1**, **LCV2** and **LCV3:** active (owner
pblumer). Everything built so far is merged (#1710, #1739, #1740) and awaits the owner's review;
no slice is complete. LCV4 is planned and has no implementation owner yet.

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
"outside your view" for a reader without access; the history's beginning is stated
(`e2e/operations-interactions.spec.mjs`). The replay route lists what the graph counts, under
the same rules, in both directions (`api/interaction/replay_test.go`,
`api/interactions_http_test.go`).

**As built:** the view is a generated BPMN collaboration rendered by bpmn-js, pools stacked
with the process in the middle, and an expanded white box draws its deployed diagram
(ADR-0559 *As built*); narrowing the replay to a correlation key is left for later.

### LCV3 — The kind of a pool, saved collaborations and their findings

**Reshaped by the amendment of ADR-0559 (2026-11-25).** No file is a collaboration by
declaration. Each pool is modelled here, a reference (`atlas:processId`) or an external party,
read from its content, and a saved collaboration is an ordinary BPMN draft. The storage
decision this slice was to start with is thereby made.

**Built (first part):** reading a model's pools and checking its message flows against the
deployments (`api/interaction`, `POST /api/v1/collaboration-check`,
`atlas_collaboration_check`); the Modeler's pool panel says the kind and assigns a deployed
process; drafts are listed with their participant count, and an application calls a draft with
two or more pools a collaboration.

**Built (second part):** a message flow's name is picked in its panel, from the names its
source sends and its target waits for; the Modeler runs the check while a diagram holds pools
and shows the findings on the elements they are about and in the collaboration's panel, worded
from the catalogue and kept out of the Problems bar.

**Built (third part):** a reference the check resolved shows its deployed process in the pool,
as a picture that is neither editable nor saved; the elements that send or wait for a message
are named from the check's `points`, and a flow at the pool's edge is led on to the element that
carries its message (#1743).

**Remaining:** `flow-not-observed`; the amendment's open questions (two files
modelling one process id, moving a pool into a file of its own, the identity of a draft).

**Acceptance:** each finding has a test that states it (`api/interaction/document_test.go`);
the kinds, the assignment and the export are driven through the real bpmn-js
(`e2e/pool-kind.spec.mjs`); a draft is listed by what it holds
(`api/interactions_http_test.go`).

### LCV4 — Composing a collaboration step by step

**Delivers:** ADR-0559 §6 in the Modeler.

**Acceptance:** an end-to-end test that builds a collaboration of two processes from one
process's ports, names a placeholder, and sees a drift finding after a redeploy.

## Handoff of 2026-11-25

- **Landed.**
  - #1710 (2026-11-13): LCV1 and LCV2 — the interaction graph, the Operations page
    *Interactions* as a BPMN collaboration, the message-only replay.
  - #1739 (2026-11-25): the amendment of ADR-0559 (the kind is the pool's, not the file's), the
    first part of LCV3 — reading a model's pools, the collaboration check, the Modeler's pool
    panel, the participant count of a draft — and the Operations link moved into the row's menu.
  - #1740 (2026-11-25): the second part of LCV3 — the message picker on a message flow and the
    findings shown in the Modeler.
  - No migration: nothing is stored that was not stored before, and no document carries the
    attribute name the amendment replaced.
- **Verified.** Each pull request was merged on a green CI run of its head: build, vet, format,
  race, the statement floor, both Windows jobs and the browser suite. Locally, `go test -race
  -timeout=45m ./...` was green for each change before it was pushed, and the browser specs
  `operations-interactions` and `pool-kind` pass on the real bpmn-js. Not verified: any of it
  against a real installation's data; the open question in ADR-0559's front matter, whether
  aggregating the event log on request is cheap enough, is still unmeasured.
- **Outstanding.**
  - No human review took place on any of the three pull requests. Until the owner has reviewed
    and accepted them, every slice stays `active`.
  - LCV3: `flow-not-observed`; the three questions the amendment leaves open.
  - LCV2: narrowing the replay to one correlation key (ADR-0559 §7).
  - Follow-ups of ADR-0559 without an owner: Worker ports, a replay of the whole installation,
    the view with many participants, aligning the "white box" wording with ADR-0374.
- **Next executable slice.** The rest of LCV3, owner pblumer, blocked on the three open
  questions being decided. Entry point: `api/interaction/document.go` for `flow-not-observed`,
  which needs the event log the check does not read yet; `showPool` and the collaboration
  check in `api/web/editor.js` for the Modeler. LCV4 should be re-scoped first: the
  `undrawn-exchange` finding already names both pools and the message, so "draw this flow" from
  a finding may replace most of composing from ports.
- **Architectural divergence.** None beyond the amendment of 2026-11-25, which is recorded in
  ADR-0559 itself.

## Handoff

Each slice's pull request updates [the manifest](../programs/lcv/program.json), this plan,
ADR-0559's *As built*, `ROADMAP.md` and `CHANGELOG.md`, and leaves the handoff the
[Programme Delivery Protocol](../development/program-delivery.md) requires.
