# ADR-0151: Incidents beyond the live diagram — the replay, the lists, and one shared action

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) fixed the worst of it:
a token parked behind an incident (ADR-0061, extended to timers by ADR-0064/0111) is no
longer drawn as a healthy one on the **live** diagram. It is outlined red, badged with
the failure message, and resolvable from the panel beside the diagram.

But the live view of a version is one of several places an operator arrives at a stuck
instance, and everywhere else it was still invisible:

- The **step-by-step replay** is the view for the question *what did this instance
  actually do, and where did it stop?* It drew the stuck element like any other, its
  Instance History listed the parked step with a cheerful "still active" dot, and
  nothing in the panel said an incident existed. The one view dedicated to
  reconstructing a failure never mentioned the failure.
- The **Instances list** — the page Operations opens on — counts a parked instance as
  **running** like any other. "3 running" reads as healthy while one of the three has
  not moved in a week. This is the first screen, and it was the last to know.
- The **incidents table**'s own "instance" link fed a *process instance* key to the
  live view's *definition* route (`#/operations/p/{key}`), so the one link from the
  incident to its diagram never landed where it said it would.

Underneath all three sits the same gap: `GET /api/v1/incidents` returns the incident's
**compiled element index** — under the name `elementId`, which every other endpoint
uses for the *BPMN diagram id* — and no definition key at all. Nothing that reads that
list can say which version an incident belongs to or which shape to mark. ADR-0150
sidestepped it for the live view by putting incidents on the runtime overlay, where the
definition is known from the request path. Every other reader still can't.

## Decision drivers

- **Don't build a second mechanism.** ADR-0150's overlay is the right source wherever a
  *diagram* is on screen. Anything new must ride it, not compete with it.
- **One incident, one affordance.** The badge, the panel and the resolve should be the
  same on the second diagram as on the first — not a second dialect that drifts.
- **The list surfaces need the list.** A per-process count and a per-instance flag are
  questions about many definitions at once; no single runtime overlay answers them.
- **Nothing new in the durable record.** `IncidentValue` is right as it stands. This is
  a read-side and presentation problem; I4/I6 mean nothing derived for a view is
  written back into an event.
- **Don't put a scan on the summary.** `/api/v1/instances/summary` is O(1) per
  definition by deliberate design (ADR-0083, after a scan-based version blocked the
  single-writer loop on every load).

## Considered options

1. **Leave it at the live view.** ADR-0150 covers the case an operator watching a
   version hits; the incidents table covers the rest.
2. **Give the replay and the lists their own incident fetch**, each scoped how it needs
   — enriching the incident list with the diagram context that makes it usable.
3. **Everything reads the runtime overlay.** Extend it until the overview can ask it
   about every definition at once.

## Decision outcome

Chosen option: **a mix of 2 and 3, split by what each surface actually is** — the
replay reads the overlay (it has exactly one instance and one definition, which is what
the overlay is good at); the lists read the incident list (they span definitions, which
the overlay cannot); and all of them render and resolve through one module.

**The replay reads the runtime overlay.** It already fetches
`/processes/{defKey}/runtime?instance={key}` for its execution-count badges, and
ADR-0150 put the instance's incidents on that response — point-looked-up per token,
exact rather than capped. So the replay needs no new endpoint and no second request:
the same poll now carries the fault. What it does with it:

- The stuck element keeps its outline **at every position of the playhead**. An
  incident is a fact about *now*, not about the frame being replayed, so it does not
  come and go as the operator scrubs.
- Its row in the Instance History is flagged (⚠, red) with the message as its tooltip,
  so the failure is visible in the *sequence*, which is what the replay is for.
- The Details panel shows the selected element instance's incident — or, with nothing
  selected, every one the instance holds, so it is reachable from the panel the replay
  opens on — and resolves it.
- The header carries an incident count beside the instance state.

The overlay read moves out of the "only when the step set grows" branch and onto every
poll. An incident is raised on an element that has *already* been activated, so it adds
no step; waiting for one would mean a replay that never mentions the fault that stopped
it. Both the badge redraw and the incident re-render are guarded by a change check, so
the extra poll costs a request and nothing else.

**The lists read the incident list, which is enriched to make that possible.**
`GET /api/v1/incidents` now resolves, at read time and memoized per process instance:
`processDefKey` / `processId`, the BPMN **`elementId`** (the compiled index it used to
return under that name survives as `elementIndex` — a **breaking rename**, called out
in the changelog), and `type` (`"job"` or `"timer"`). It also takes `?instance=` and
`?process=`, which is what makes it usable from the API and the `atlas_list_incidents`
MCP tool without pulling — and page-capping — every incident on the server. Nothing is
written back; `applyToState` and replay are untouched.

On that:

- The **Instances overview** gains an **Incidents** column, and a flag on any
  variable-search hit that is parked (those rows are individual instances, and "active"
  reads identically for a stuck one and a healthy one). The column links to the version
  that *holds* the incidents rather than the latest, because landing on the newest
  version's diagram shows nothing whenever the fault sits on an older one — the case
  where the flag matters most. Counts come from the incident list, not from
  `/instances/summary`, which stays O(1) per definition; under a flood the list's page
  cap makes them a lower bound, and the page says so.
- The **incidents table**'s instance link is fixed by the same enrichment — it now opens
  the instance on its own version's live diagram, with a replay link beside it.

**One shared action.** The row markup, the panel wrapper and both resolve actions move
into `api/web/incidents.js`, which the live view, the replay and the table all import.
The live view's rendering and semantics are unchanged by the move — deliberately, down
to `retries: 1`, whose rationale ADR-0150 states — but they are now *one* implementation
rather than two that look alike today. Two resolve actions exist, and the split is the
point: beside a diagram the operator has just read the message and wants the job to try
again, so it is one click with a single attempt; in the incidents table they are triaging
a list and may want a bigger budget, so it asks — in a dialog that has room to say a
timer incident re-arms and ignores the count, which `window.prompt` did not.

### Consequences

- **Positive:** every route to a stuck instance now says it is stuck: the version's
  diagram (ADR-0150), the instance's replay, the overview, the search, the table. The
  replay costs no new endpoint. The incident list is finally addressable — a client can
  ask "what is stuck in this instance" and get diagram ids back — which the MCP tool
  gets for free. The second diagram cannot drift from the first, because there is one
  renderer.
- **Negative / trade-offs accepted:** `elementId` in the incident list changes meaning
  (a breaking rename, pre-1.0 and called out). Listing incidents costs one process
  instance lookup per distinct instance rather than a pure scan — bounded by the same
  page cap and off the hot path. The replay polls the runtime overlay every 1.5 s
  instead of only when its step set grows. The overview's counts are a page-capped lower
  bound under a flood.
- **Follow-ups / risks to watch:** an incident count in the Operations nav (ADR-0150's
  own follow-up) is now one `incByDef` away and still unbuilt; "resolve *and* fix the
  variables first" is the obvious next step past a retry budget; an incident on an
  element of a *called* process shows in that child's views, not the caller's — the call
  activity's existing drill-down is the path there.

## Pros and cons of the options

### Leave it at the live view
- Good: nothing to build; ADR-0150 already covers the operator watching a version.
- Bad: leaves the replay — the view whose entire purpose is reconstructing what an
  instance did — silent about why it stopped, and leaves the first screen counting
  stuck instances as healthy ones.

### Each surface fetches what it needs (replay: overlay, lists: incident list)
- Good: each reader uses the source that answers its question exactly — the overlay is
  precise for one instance, the list spans definitions; no new endpoint for the replay;
  the enrichment pays for the table's broken link too.
- Bad: two data paths for one concept, which is only safe because one renderer and one
  action sit above them.

### Everything reads the runtime overlay
- Good: a single data path.
- Bad: the overview asks about *every* definition at once — served from a per-definition
  overlay that would mean N requests, or a new all-definitions variant that is the
  incident list with extra steps. The overlay is keyed by the thing the overview does
  not have.

## Links

- extends [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) (incidents on
  the live diagram, and the runtime overlay that carries them)
- extends ADR-0061 (the incident model: raise / resolve / resume) and ADR-0064 /
  [ADR-0111](0111-incident-model-completion.md) (timer FEEL failures)
- renders into the view of [ADR-0046](0046-single-process-step-replay.md) (the
  single-instance replay)
- honours [ADR-0083](0083-o1-instance-summary.md)'s O(1) summary by not reading
  incidents through it
