# ADR-0121: BPMN lanes

- **Status:** Accepted (Layer A)
- **Date:** 2026-08-12
- **Deciders:** Atlas engine team

> **Implementation status.** Layer A accepted and delivered. A `<laneSet>`/`<lane>` partitions a process's flow nodes
> into **organizational lanes** — a role, team, or system responsible for those nodes. Atlas adopts
> lanes as **metadata with no execution semantics** (spec-faithful): the compiler records which lane
> each node belongs to and exposes it to Operations and the Tasks app; the engine, `applyToState`,
> and token flow are untouched. A lane may **reference** an Atlas group (ADR-0044) so a user task
> without its own `candidateGroups` inherits the lane's group as a compile-time default — but a lane
> **is not** a group, and explicit task assignment always wins. Delivered in layers: **A** (metadata)
> now; **B** (lane→group assignment default) and **C** (instance-level access control) designed here,
> deferred.

## Context and problem statement

BPMN **lanes** (a `<laneSet>` of `<lane flowNodeRef>` inside a process/pool) partition a process's
flow nodes into swim-lanes — conventionally a role ("Approver"), a team ("Finance"), or a system.
Nested lanes are allowed (`<childLaneSet>`); a flow node belongs to **at most one** lane. Lanes are
the intra-pool partition, one level below the pools Atlas already models (ADR-0023).

Atlas ignores lanes entirely today. They are drawn by the vendored bpmn-js modeler but the compiler
never parses `<laneSet>`/`<lane>`, so lane structure is lost at deploy — Operations can't show which
lane a token is in, and the Tasks app can't group work by lane.

The question this ADR answers, sharpened by a design discussion: **what should a lane mean in Atlas —
and specifically, should lanes map directly onto Atlas users and groups?** The temptation is to make
the lane the *assignment* mechanism ("every task in the Finance lane goes to the Finance group").

Two facts frame the answer:

- **Lanes have no execution semantics in BPMN.** The spec is explicit: lanes are an organizational
  grouping and do **not** affect token flow. Camunda 8 / Zeebe deliberately **dropped** lane-based
  assignment in favor of explicit per-task assignment.
- **Assignment is already solved in Atlas — and not via lanes.** A user task carries a
  `zeebe:assignmentDefinition` with `assignee` + `candidateGroups` (`compiler/parse.go`,
  `UserTaskDetail.CandidateGroups`), bound to real identities (ADR-0042 runtime assignment/claim,
  ADR-0044 users and groups, ADR-0045 assignment bound to identity). This is the established,
  explicit source of truth for "who does this task".

So making lanes *also* drive assignment would create a second source of truth competing with
`candidateGroups`, and equating a lane with a group (by name) couples the diagram's visual structure
to the identity/security model in a brittle way.

## Decision drivers

- **Faithful BPMN.** Lanes are organizational metadata with no execution effect — keep them that way;
  the engine and `applyToState` must not learn about lanes (I4 stays trivially intact).
- **One source of truth for identity.** Users and groups (ADR-0044) are the authority; a lane may
  *reference* a group but must not *become* one. Explicit task `candidateGroups`/`assignee` always
  wins over a lane-derived default.
- **Reuse, don't reinvent.** Any assignment convenience (Layer B) must compile down to the existing
  `UserTaskDetail.CandidateGroups` — **no new runtime mechanism**, the same "reuse the machinery"
  move send tasks and terminate ends made.
- **Layered and low-risk first.** Metadata (A) is small and spec-faithful and unlocks Operations/
  Tasks value immediately; assignment defaults (B) and access control (C) are separable, larger, and
  each earns its own scope.
- **Decouple visual from security.** A lane→group binding is an **explicit reference** (by group id),
  robust to renaming a lane in the diagram, not implicit name-matching.

## Considered options

1. **Lanes as organizational metadata, with an *optional explicit* lane→group reference user tasks
   inherit as a `candidateGroups` default (chosen; layered A → B → C).** Parse `<laneSet>`/`<lane>`
   (incl. nested `<childLaneSet>`), record each flow node's lane in the compiled process, and expose
   it (Operations, Tasks app). No execution effect. Later, a lane may carry an `atlas:laneGroup`
   extension naming an Atlas group id; at deploy a user task **without** its own `candidateGroups`
   inherits the lane's group — compiling to the existing `CandidateGroups`, explicit task assignment
   winning. Access control by lane group is a further, separate layer.
2. **Lane == group, assignment by name-matching (implicit).** Rejected: a lane name is a role/team
   label, not an identity-system group; name-matching is brittle (renaming a lane silently breaks
   assignment), couples the diagram to security, and creates two competing sources of truth with
   `candidateGroups`. It also contradicts the Camunda 8 direction Atlas's assignment model already
   follows. And lanes contain **all** node types (gateways, service tasks) while only user tasks have
   assignees, so the equation only ever meaningfully applies to a subset.
3. **Keep ignoring lanes (status quo — purely visual, never stored).** Rejected: loses the
   organizational value (Operations "which lane", Tasks "group by lane") and the opt-in authoring
   convenience of Layer B, for no benefit beyond doing nothing.

## Decision outcome

Chosen: **option 1 — lanes are metadata; a lane references (never equals) a group; assignment stays
explicit.** Layer A is the delivered scope; B and C are designed and deferred.

### Layer A — metadata (this ADR's implementation scope)

- **Compiler.** Parse `<laneSet>`/`<lane>` at the process (and any subprocess) level, including nested
  `<childLaneSet>`. Each `<lane>` has an id, an optional name, and `<flowNodeRef>` children naming the
  flow nodes it contains. Build a compiled **lane table** (id → {name, parent lane, optional group
  ref reserved for B}) and record, per compiled node, the **leaf lane** it belongs to (a new interned
  index on `CompiledNode`, `-1` when the node is in no lane), resolving `flowNodeRef` like other id
  references. A flow node in two lanes of the same lane set is a deploy error (BPMN forbids it).
- **Runtime.** None. Lanes carry no tokens, arm nothing, and change no flow; `applyToState` and the
  behavior dispatch are untouched. Recovery is unaffected (nothing lane-specific is logged).
- **API / Operations / Tasks.** Expose each element's lane (and, for nested lanes, its lane path) so
  Operations shows "this token is in lane X" and the Tasks app can **group/filter** open tasks by
  lane. Read-only, derived from the compiled process — no new durable state.

### Layer B — lane→group as an assignment *default* (deferred; designed)

- A lane may carry an explicit `<atlas:laneGroup groupId="…">` extension naming an **Atlas group by
  id** (ADR-0044) — a reference, robust to renaming the lane. At **deploy** (compile), a user task
  that has **no** `candidateGroups` of its own inherits its lane's group into `UserTaskDetail.CandidateGroups`;
  a task with explicit `candidateGroups`/`assignee` is untouched (**explicit wins**). This compiles
  entirely to the existing assignment field — **no new runtime, value type, or recovery path**, and
  the Tasks app resolves candidates exactly as it does today. Non-user-task nodes in the lane are
  unaffected (they have no assignee).

### Layer C — instance/task access control by lane group (deferred)

- Gating **who may see and act on** an instance or task in Operations by the lane's group is a real
  feature that touches the authentication/authorization boundary (ADR-0044) at the *instance* level,
  not just task assignment. It is out of scope here and warrants its own ADR.

### Phased implementation plan (test-first, Layer A)

- **Phase 1 — Compile (done).** Parse `<laneSet>`/`<lane>`/`<flowNodeRef>`/`<childLaneSet>`; a lane
  table + a per-node leaf-lane index resolved from `flowNodeRef`; deploy errors for an unknown
  `flowNodeRef` and a node in two lanes. *Tests:* a two-lane process compiles with each node mapped
  to its lane; a nested lane maps a node to its leaf lane; an unknown/duplicate `flowNodeRef` is a
  deploy error; a process with no lanes is unaffected.
- **Phase 2 — API/Tasks (done).** Expose element→lane over the HTTP task API (`lane` = leaf-lane
  name, `lanePath` = outermost→leaf), and surface it in the Tasks app (a lane chip in the list row, a
  Lane detail row showing the full path). *Tests:* the task API returns the lane and lane path for a
  user task in a lane. (Operations lane display and Tasks grouping/filtering are follow-ups.)
- **Phase 3 — Docs (done).** Accept this ADR (Layer A), update the ROADMAP. (Layers B and C are
  separate future ADRs/PRs.)

### Consequences

- **Positive:** Atlas gains organizational structure it currently discards — Operations "which lane",
  Tasks "group by lane" — with **no execution change** and no new durable state. The design keeps
  identity (ADR-0044) as the single source of truth and leaves a clean, opt-in path (Layer B) that
  reuses `candidateGroups` for assignment convenience without a second mechanism. It follows the
  industry direction (explicit assignment, lanes organizational).
- **Negative / trade-offs accepted:** a compiled lane table and a per-node lane index (small, static
  metadata). Nested lanes and the "node in exactly one lane" rule need care at compile. The Layer B
  binding (`atlas:laneGroup`) is an Atlas extension, not standard BPMN — a portability cost accepted
  in exchange for an explicit, rename-safe reference (the same trade-off Atlas's connector extensions
  already make).
- **Follow-ups / risks to watch:** Layer B (assignment default) and Layer C (access control) as their
  own ADRs; boundary events and sequence flows are not `flowNodeRef` targets, so their lane is
  derived (a boundary's lane = its host's) — an edge to specify in Phase 1; a lane spanning an
  embedded subprocess boundary (a lane set inside a subprocess) is supported by parsing lane sets per
  scope.

## Pros and cons of the options

### Option 1 — lanes as metadata; explicit lane→group reference; assignment stays explicit (chosen)
- Good: spec-faithful (no execution effect); identity stays the single source of truth; Layer B
  reuses `candidateGroups` (no new runtime); decoupled and rename-safe; layered and low-risk first.
- Bad: adds a lane table + per-node index; the B binding is a non-standard Atlas extension; two ways
  to reach a task's candidate group (its own vs. inherited from the lane) — resolved by "explicit wins".

### Option 2 — lane == group, implicit name-based assignment (rejected)
- Good: zero authoring for the simple "one lane, one group" case.
- Bad: two competing sources of truth; brittle name-matching; couples visual structure to security;
  only applies to user tasks; contradicts the Camunda 8 assignment direction Atlas follows.

### Option 3 — keep ignoring lanes (rejected)
- Good: zero work.
- Bad: discards real organizational value (Operations/Tasks) and the opt-in assignment convenience.

## Links

- builds on ADR-0023 (collaborations and pools — lanes are the intra-pool partition) and ADR-0074
  (subprocess scopes — lanes are orthogonal metadata, parsed per scope, not a scope themselves)
- integrates with ADR-0042/0044/0045 (user-task assignment, users and groups, identity binding) —
  Layer B fills `candidateGroups` from a lane's explicit group reference; identity stays authoritative
- honors I4 (no engine/`applyToState` change — lanes are compile-time metadata) and ADR-0018
  (test-first)
- ROADMAP: organizational modeling; Layer B (assignment default) and Layer C (access control) are
  future, separately-scoped work
