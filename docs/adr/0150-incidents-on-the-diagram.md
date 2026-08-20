# ADR-0150: Incidents on the diagram — the live view, the replay, and the lists that lead there

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

An incident is the engine's durable "this token cannot move" fact (ADR-0061, extended
to timers by ADR-0064/0111). Until now it had exactly one operator surface: the
Operations **Incidents** table — a flat, server-wide list of rows carrying an element
instance key and a compiled element index.

That is the wrong place to meet an incident. An operator does not start from the
incident; they start from a process. They open the **live view** of a version to see
where its tokens are, or the **replay** of one instance to reconstruct what it did.
Both draw the diagram, both mark the element a token sits on — and neither said a
word about the token being *stuck*. A parked token renders exactly like a working
one: green, on its element, apparently fine. The operator's own question — "why has
this instance not moved since Tuesday?" — was answerable only by leaving the diagram,
opening a separate table, and matching keys by eye.

The same view already solves this for user tasks. A user-task element waiting for a
human carries an "Open task" badge that jumps straight to its form (ADR-0028), and
the side panel lists the instance's waiting tasks as links. The operator goes from
"the token is here" to "work it" in one click. An incident — the case where something
is actually *wrong* — deserves at least the affordance a routine wait already has.

Three things stood in the way, all in the read model rather than the engine:

1. The incident list returned the **compiled element index** (an integer internal to
   the compiled graph), not the BPMN diagram id. A diagram can only be marked by
   diagram id.
2. It returned no **process definition key**, so an incident could not be attributed
   to the version whose diagram is on screen — and the table's own "instance" link was
   wrong for exactly that reason: it fed the *instance* key to the live view's
   *definition* route.
3. It was **unscoped and page-capped** (5000 per call). A view that wanted only its
   own incidents had to pull everyone's and hope its own survived the cap.

## Decision drivers

- **Meet the operator where they already are.** The diagram is the operator's map;
  a fault belongs on the map, not only in an index of faults.
- **Same fact, same affordance, everywhere.** An incident should read and resolve
  identically in the table, the live view, and the replay — one interaction, not three
  that drift.
- **Resolve without leaving.** The point of surfacing it is acting on it: the existing
  `POST /incidents/{elementInstanceKey}/resolve` should be one click from the badge.
- **Nothing new in the durable record.** The engine's `IncidentValue` is correct as it
  stands; this is a *read-side* problem. Invariants I4/I6 mean nothing derived for a
  view may be written back into an event.
- **A whole version at once.** "Is anything in production stuck?" is asked of a
  version, not of an instance — the live view's "All instances" scope should answer it,
  and the overview should answer the same question for the server without being opened.

## Considered options

1. **Link the existing table better.** Keep incidents where they are, fix the broken
   instance link so a row can at least reach the live view.
2. **Enrich the incident read model and render it on both diagrams.** Resolve the
   definition and the BPMN element id when the list is read, scope the list by
   instance/definition, and let the live view and the replay badge, list, and resolve
   an incident in place — with the Instances overview flagging the way in.
3. **Store the diagram id on the incident.** Write the BPMN id into `IncidentValue` at
   raise time so no read-side resolution is needed.

## Decision outcome

Chosen option: **"Enrich the incident read model and render it on both diagrams"**,
because the missing information is derivable at read time from state the server
already holds, and the affordance it unlocks is the one the same views already give a
waiting user task.

**Read model.** `GET /api/v1/incidents` resolves each incident's process instance to
its definition and that definition's compiled process, and returns:

- `processDefKey` / `processId` — which version the incident belongs to,
- `elementId` — the **BPMN diagram id** of the stuck element (the name every other
  view in the API already uses for that value),
- `elementIndex` — the compiled-graph index, which is what the durable record
  actually stores (this is the field the old `elementId` held: a **breaking rename**,
  called out in the changelog),
- `type` — `"job"` or `"timer"`, the distinction all three views label.

The lookup is memoized per process instance for the length of one request, because a
flood of incidents is usually a flood on few instances. When an instance has outlived
its deployment the diagram fields are simply absent — the incident is still listed and
still resolvable, since the resolve key is its element instance.

Nothing is written back: `IncidentValue` keeps holding only the compiled index, so
`applyToState` and replay are untouched (I4/I6).

**Scoping.** The endpoint takes `?instance=` and `?process=`. The replay fetches one
instance's incidents, the live view one version's. Neither can be pushed out of the
capped page by unrelated failures elsewhere on the server, which is precisely the
failure mode that would make this feature lie in the situation it exists for.

**Live view.** Each element holding an incident is outlined in red and carries a
`⚠ incident` badge; the toolbar counts them for the current scope. The side panel
lists them above the variables — each card naming the element, quoting the failure
message, and offering **Resolve…**. With "All instances" selected the scope is the
whole version: every stuck instance of it, each card linking to that instance's
replay, and each instance's row in the overview carrying a `⚠ n` chip. Clicking a
badge narrows the panel to that element; the diagram markers are drawn from the
incident list itself (not from the runtime element page) so an incident can never be
the thing that falls off the page.

**Replay.** The stuck element keeps its red outline at every position of the playhead
— an incident is a fact about *now*, not about the frame being replayed — carries the
same badge, and its row in the Instance History is flagged. The Details panel shows
the incident for the selected element instance, and the instance-level panel (nothing
selected) lists all of them; both resolve in place. The header gains an incident
count beside the state.

**Instances overview.** The list an operator opens *first* flags what is stuck too: a
per-process **Incidents** column, and a `⚠ n` on any variable-search hit that is parked
— because a stuck instance is counted as *running* like any other, so "3 running" reads
as healthy when one of the three has not moved in a week. The column links to the
version that actually holds the incidents rather than the latest one: landing on the
newest version's diagram would show nothing whenever the fault sits on an older one,
which is the case where the flag matters most. The counts come from the same incident
list, read once per refresh and bucketed in the browser, deliberately *not* from
`/api/v1/instances/summary` — that endpoint is O(1) per definition by design (ADR-0083,
after a scan-based version blocked the single-writer loop on every load) and must not
grow a scan. A capped incident page is called out, so the counts are never silently a
lower bound.

**One interaction.** The badge, the card, and the resolve dialog live in one module
(`api/web/incidents.js`) that every surface imports, so the Operations table's
own resolve action is now the same dialog — which also replaced its `window.prompt`
with something that can explain that a timer incident re-arms and ignores the retry
count. The table's instance link is corrected to the live view's definition/instance
route, and gains a replay link beside it.

### Consequences

- **Positive:** an operator sees a stuck token as stuck, on the diagram they were
  already looking at, and unblocks it without leaving. "Is anything in this version
  stuck?" is answered by opening the version — and "is anything stuck at all?" by the
  Instances overview, without opening anything. The incident, the task and the decision
  badge now form one consistent vocabulary of "something to look at here". The
  wrong-key link in the incidents table is fixed as a side effect of having the
  definition key.
- **Negative / trade-offs accepted:** `elementId` in the incident list changes meaning
  (integer index → diagram id string); pre-1.0, called out in the changelog, and the
  old value survives as `elementIndex`. Listing incidents now costs one process
  instance lookup per distinct instance rather than a pure scan — bounded by the same
  page cap, memoized per request, and off the hot path. The views poll incidents on
  the existing 1.5 s cadence, one more small request per tick; the Instances overview
  reads the list once per refresh, and under a flood its counts are a page-capped lower
  bound (said so on the page) rather than exact.
- **Follow-ups / risks to watch:** the resolve dialog grants a retry budget only; a
  "resolve and set variables first" flow (fix the cause, then retry) is the obvious
  next step and is deliberately not in this slice. An incident on an element of a
  *called* process is shown in that child's own views, not the caller's — the call
  activity's existing drill-down link is the path there.

## Pros and cons of the options

### Link the existing table better
- Good: a one-line fix; no read-model change.
- Bad: leaves the actual problem untouched — the diagram still shows a stuck token as
  a healthy one, and the operator still has to know to go looking in another view.

### Enrich the read model and render it on both diagrams
- Good: puts the fault where the operator already is; reuses the badge/link/resolve
  vocabulary the views have for tasks and decisions; no engine or durable-state
  change; the scoping removes a real "the page ate my incident" failure mode.
- Bad: a breaking field rename in the incident list; a per-instance lookup on read.

### Store the diagram id on the incident
- Good: the list stays a pure scan.
- Bad: writes a *derived, deploy-time* string into a durable event, duplicating what
  the compiled process already knows, and it would go stale the moment a definition is
  re-deployed. Against I6 in spirit: events carry facts, not renderings of them.

## Links

- extends ADR-0061 (incident model — raise / resolve / resume), whose stated follow-up
  was "an HTTP API + operator 'incidents' view"
- extends ADR-0064 and [ADR-0111](0111-incident-model-completion.md) (timer FEEL
  failures raise the same job-less incident)
- renders into the views of [ADR-0046](0046-single-process-step-replay.md) (the
  single-instance replay) and the ADR-0013/0022 live view
- follows the linking pattern of [ADR-0028](0028-forms-and-the-tasks-app.md) (a
  waiting user task badged on the diagram and linked to its form) and
  [ADR-0066](0066-decision-evaluation-records.md) (the decision badge)
