# ADR-0105: Per-server call-activity target overrides

- **Status:** Accepted
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. The engine resolves an override ahead of the
> default `latestProcess` lookup (`ProcessingContext.resolveCallTarget`, driven by
> `Processor.SetCallTargetOverride`/`ClearCallTargetOverride`), the `callOverrideStore`
> sidecar persists the operator's intent, `loadCallOverrides` translates each record into
> a directive at startup once deployments are loaded, `PUT`/`DELETE
> /api/v1/call-activities/overrides/{processId}` are admin-gated, the
> `GET /api/v1/call-activities` inventory reports the override and the effective
> resolution, the modeler's management view edits it inline, and the MCP surface stays
> read-only as decided. The follow-ups recorded under *Consequences* — an incident for a
> disabled/parked call, per-caller-element granularity — remain open.

## Context and problem statement

A call activity resolves the process it starts as a child by its bpmn process id
plus a binding (ADR-0076). Resolution is global to the engine: at activation the
behavior looks up `latestProcess[calledProcessId]` and freezes the resulting
definition key into the child's create event. There is no way for an **operator**
to influence that resolution on a **particular server** without editing and
redeploying the model.

Real deployments need exactly that per-server control:

- **Environment routing.** On a staging server, calls to a heavyweight or
  side-effecting process should hit a lightweight stub instead — without touching
  the model that also runs in production.
- **Version pinning.** A `latest`-bound call floats to the newest deployment. An
  operator may need to pin one server to a known-good version while a new one
  bakes, then release the pin.
- **Kill switch.** Temporarily stop a call target on one server (incident
  response) without undeploying the callee, which other callers may still need.

The management view (ADR-0076 follow-up, the `GET /api/v1/call-activities`
inventory) already shows, per server, every call activity and its caller. What is
missing is the ability to **edit the target resolution right there** — the active
counterpart to that read-only view. This ADR decides how.

## Decision drivers

- **Invariants hold — especially I6 (deterministic replay).** Resolution already
  happens *live* and the chosen def key is frozen into the child create event, so
  an override may change *future* resolutions but must never change how an
  already-created child replays. No hot-path allocation (I1); single writer owns
  the override map (I3); `applyToState` stays untouched (I4) — an override is not
  a record.
- **Operator config, not process semantics.** Overrides are per-server operational
  configuration, the same category as connectors (ADR-0041) and deployments
  (ADR-0019): durable on a sidecar, owned by the run-loop goroutine, loaded at
  startup — deliberately *not* event-sourced, because the event log is the
  process's history, not the server's admin config.
- **Reuse the existing seams.** Build on the call-activity inventory view, the
  sidecar-store pattern, the `requireAdmin` gate, and the existing
  `latestProcess` resolution — add one branch in front of it, nothing more.
- **Simple, operationally meaningful unit.** Override by *called process id* — the
  unit every real use case (route/pin/disable) is expressed in — not per caller
  element, which no use case needs and which multiplies the surface.

## Considered options

1. **Per-server override map consulted before `latestProcess`, stored on a
   sidecar (chosen).** The processor holds a `calledProcessId → directive` map
   next to `latestProcess`; the behavior consults it first, else falls back to the
   default. The API persists operator records to a sidecar and pushes engine
   directives on change and at startup. Read-only inventory + admin PUT/DELETE.
2. **Event-source the overrides.** Model an override as a durable record applied
   through `applyToState`. Rejected: it conflates per-server admin config with the
   instance history in the log, complicates recovery, and buys nothing — resolution
   determinism is already guaranteed by the frozen child-create event, so the
   override need not be in the log to keep replay correct.
3. **Per-caller-element granularity.** Key an override by (caller process id,
   element id). Rejected as the primary model: no real use case targets a single
   call site, and it multiplies records and UI. The inventory still *shows* every
   call site (from-where), so the per-target rule's blast radius is visible; a
   finer key can be added later if a use case appears.

## Decision outcome

Chosen: **option 1 — a per-server, sidecar-backed override map, keyed by called
process id, consulted at activation in front of the default `latestProcess`
resolution.**

### The override directive (engine)

A directive is one of three shapes, resolved live at call-activity activation:

- **redirect** → resolve the *latest* deployment of a different process id.
- **pin** → resolve one exact definition key (the API picks it from a version the
  operator names; the engine is version-agnostic and just uses the key).
- **disable** → resolve nothing; the call parks exactly as an undeployed target
  does today (ADR-0076), i.e. the token waits.

Resolution precedence at activation:

```
resolveCallTarget(calledProcessId):
    if override exists for calledProcessId:
        disabled     → (0, false)              # park
        pinned defKey → (defKey, defKey is still deployed)
        redirect pid  → latestProcess[pid]      # one hop, no chaining → no cycles
    else:
        latestProcess[calledProcessId]          # unchanged default (ADR-0076)
```

A redirect resolves the *target's* latest directly (not recursively through the
target's own override), so overrides cannot form a cycle. A pinned or redirected
target that is not currently deployed parks, identical to the existing
"callee not deployed yet" behavior — no new failure mode.

### Why this is I6-safe (the load-bearing argument)

`callActivityBehavior.OnActivated` already resolves the def key and writes it into
the child's `CreateChildInstance` command, which becomes the durable create event
(ADR-0076). Replay does **not** re-resolve; it replays the event with its frozen
key. An override therefore only affects call activities that resolve *after* it is
set — precisely the semantics `latest` binding already has when a new version is
deployed. Children in flight are untouched; recovery is unaffected; the override
map need not, and does not, live in the log.

### Storage & lifecycle (API)

- A `callOverrideStore` sidecar (one JSON file per called process id, hex-named),
  mirroring `connectorStore` (ADR-0041): owned by the run-loop goroutine, atomic
  writes, oldest-first `loadAll`. It stores the operator's intent
  (`{calledProcessId, action, targetProcessId, targetVersion}`), never a raw def
  key, so it survives redeploys sensibly (a pin re-resolves its version → key at
  load time).
- At startup, after deployments are loaded (so a pin's version resolves to a key),
  the server translates each record into an engine directive and calls
  `proc.SetCallTargetOverride` on the run-loop goroutine.
- `PUT /api/v1/call-activities/overrides/{processId}` and `DELETE …/{processId}`
  set and clear an override; both are `requireAdmin`-gated, like connector config.
  The existing `GET /api/v1/call-activities` inventory gains the override and the
  *effective* resolution per row, so the management view shows intent versus
  outcome and the UI edits it inline.
- The MCP surface stays read-only: overrides are admin config, so the two write
  routes are classified as intentionally omitted (mirroring connectors), while the
  existing `atlas_call_activities` read tool simply reports the override too.

### Consequences

- **Positive:** real per-server control (route / pin / disable) with no model edit
  or redeploy; built entirely on existing seams; invariants preserved; the
  read-only inventory becomes an editable management surface without a new value
  type or a new event.
- **Negative / trade-offs accepted:** resolution grows one branch (a map lookup,
  no allocation); a second server-local config store to back up/restore alongside
  connectors and deployments; an override is a per-server fact, so the same model
  can resolve differently on two servers — intended, but operators must know to
  look here when a call "goes somewhere unexpected" (the inventory makes it
  visible).
- **Follow-ups / risks to watch:** a disabled/parked call has no incident yet —
  raising one (or an audit event) is a follow-up; per-caller-element granularity
  if a use case appears; whether a redirect should be allowed to point at a pinned
  version in one record (today: redirect = latest, pin = a version of the same id).

## Pros and cons of the options

### Option 1 — sidecar override map, consulted before default (chosen)
- Good: I6-safe by construction (frozen create event); reuses sidecar + admin +
  resolution seams; operationally meaningful unit; no new event or value type.
- Bad: a second admin-config store; a per-server divergence operators must know
  about.

### Option 2 — event-sourced overrides
- Good: one uniform durability story (everything is events).
- Bad: conflates admin config with instance history; heavier recovery; no
  determinism benefit, since the child-create event already freezes the key.

### Option 3 — per-caller-element granularity
- Good: maximally precise.
- Bad: no use case needs it; multiplies records and UI; the inventory already
  shows every call site, so the coarser key's effect is already visible.

## Links

- builds on ADR-0076 (call activities — the resolution path and the frozen
  child-create event this rests on), ADR-0041 (operator-managed connector config —
  the sidecar-store pattern and the secret/reference discipline), ADR-0019
  (deployment sidecar + startup reload ordering)
- honors I1 (map lookup, no per-command allocation), I3 (run-loop owns the map),
  I4 (`applyToState` untouched — an override is not a record), I6 (frozen
  child-create key ⇒ replay unaffected)
- extends the ADR-0076 follow-up call-activity management view
  (`GET /api/v1/call-activities`) from read-only inventory to editable per-server
  control
