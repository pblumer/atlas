# ADR-0374: The white box is a cached contract, not a live look inside

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** whether anyone actually needs the publisher's *diagram*, or
  whether the contract rendered as docking points is the whole feature. The diagram is
  the expensive half and the disclosing half; nobody has yet stated a modelling task
  that the contract alone cannot serve. Until someone does, the second layer below
  stays unbuilt.
- **Question checked:** 2026-09

## Context and problem statement

Once a participant binds a published interface
(ADR-0371), the modeller is docking an arrow
onto a name they have to know in advance. The feature that makes this pleasant — and
the one this epic was pitched on — is expanding the participant with the `+`
affordance and seeing what may be docked onto: entry points, the errors that can come
back, the messages and signals in play.

The temptation is to implement that by drawing the publisher's process. That raises
three problems at once, and all three have precedent in this tree:

1. **Availability.** A modeler that renders a foreign model by fetching it makes
   authoring depend on a peer being up. ADR-0189 and ADR-0211 already paid for that
   lesson; `api/panoramaremote.go` is the answer they arrived at — a cache with
   *fresh*, *stale* and *unreachable* kept apart, because "nothing changed" and
   "nobody could look" must not render alike.
2. **Disclosure.** A process diagram carries lane names, worker names, decision logic
   and step wording. The publisher agreed to an interface, not to that.
3. **Stability.** What is drawn must be what the model binds to. Rendering the
   publisher's *current* diagram while the consumer is bound to contract version 3
   shows one thing and deploys another.

## Decision drivers

- **Show what can be docked onto, which is the contract** — entry points, payloads,
  returns, outcomes — not the mechanism behind it.
- **Never couple modelling to peer availability.** The modeler must open, and the
  picture must be honest about its own age.
- **Draw the version that is bound**, not the version that is current.
- **The collapsed state has to say something.** A participant that is closed again
  should still show where the arrow goes, or the expansion was a detour rather than a
  view.

## Considered options

1. **Fetch and render the publisher's live BPMN** on expand.
2. **Render the cached contract** as docking points, with the foreign diagram as an
   optional, separately granted second layer.
3. **No white box** — a properties-panel picker listing entry points, and nothing on
   the canvas.

## Decision outcome

Chosen option: **2 — render the cached contract; the diagram is a separate, optional
layer.**

### What `+` opens

Expanding a bound participant renders, inside the pool band, the entry points of the
bound contract version as docking targets, each showing:

- whether it **starts** an instance or **continues** one;
- its correlation key, so the modeller can see what must match;
- its payload shape, by reference to the information model (ADR-0230) where there is
  one;
- the **returns** it may produce — errors (ADR-0089) and escalations (ADR-0125).

Dragging a message flow onto a target sets `atlas:entryPoint` and nothing else.

**The returns are the part worth building.** Docking onto an entry point that
declares an error should offer to attach the matching boundary event (ADR-0040) to the
sending activity right there. That is the point at which this stops being a nicer
picker and starts being something no other modeler does: the failure path gets
modelled while the happy path is being drawn, instead of after the first incident.

### Freshness is rendered, never assumed

The descriptor is cached per interface and version, and every expansion is labelled
with the three states `api/panoramaremote.go` already distinguishes:

- **fresh** — read within the freshness window;
- **stale** — an answer exists, a refresh failed, and what is shown is history;
- **unreachable** — nothing is known about this interface on this server.

A stale or unreachable participant still expands, from the cache, clearly marked.
Modelling continues; only the confidence label changes. A contract version, once
cached, is immutable (ADR-0373), so a stale reading is
wrong only about whether a *newer* version exists — never about the version in hand.

### The foreign diagram is a second layer, and it is gated twice

Only when the publisher grants it **and** the reader holds the **view** grant may a
read-only rendering of the publisher's process appear behind the docking points. It is
a picture: not selectable, not editable, not a source of bindings. Bindings come from
the contract even when the diagram is on screen, so a modeller cannot accidentally
attach to something the contract does not promise.

Per the open question above, this layer is **not** built until somebody states a
modelling task that needs it.

### Collapsed

A collapsed bound participant annotates each message flow with the entry point it
targets and the contract version it is bound to, plus the freshness state when it is
not fresh. Closing the participant loses the detail, not the fact.

### Consequences

- **Positive:** docking becomes a direct manipulation instead of typing a remembered
  name; the modeller sees the failure modes at the moment they can act on them; the
  modeler stays usable with every peer down; what is drawn is what deploys, because
  both come from the bound contract version.
- **Negative / trade-offs accepted:** a cache to invalidate, and a user-visible
  freshness vocabulary that has to be taught. Rendering foreign content inside a
  bpmn-js pool band is real front-end work (ADR-0013), even without the diagram layer.
  And a bound contract version can be deprecated upstream while the cached copy reads
  perfectly fine — the deprecation warning has to travel with the descriptor, or the
  picture is honest about its age and silent about its obsolescence.
- **Follow-ups / risks to watch:** whether the collapsed annotation survives
  auto-layout (ADR-0124/0127) without colliding with labels, which ADR-0252 had to
  solve once already for runtime badges. Whether the expansion should offer the
  publisher's *documentation* (ADR-0143/0250) alongside the contract. And the
  observe-grant question from ADR-0373 returns here in
  its most tempting form: showing live instance counts on a foreign participant would
  be the natural next click, and it is the one that leaks another domain's operations.

## Pros and cons of the options

### Option 1 — render the live foreign BPMN
- Good: complete, immediate, exactly what the pitch imagined.
- Bad: couples authoring to peer availability; discloses internal organisation by
  default; and shows the current model while the consumer binds to a pinned version,
  so the picture and the deployment can disagree without anybody noticing.

### Option 2 — cached contract, diagram optional (chosen)
- Good: works offline, honest about age, binds to what it draws, discloses only what
  was published; the error-to-boundary-event affordance falls out of it.
- Bad: a cache and a freshness vocabulary; less visually satisfying than a full
  foreign diagram; still non-trivial canvas work.

### Option 3 — properties-panel picker only
- Good: cheapest by a wide margin, and it delivers most of the practical value —
  the modeller stops guessing names.
- Bad: the interaction stays invisible on the canvas, which is the one place the
  business reader looks. Worth keeping as the fallback if the canvas work is deferred:
  it is a subset of this decision, not a competing one.

## Links

- renders ADR-0373 at the version bound by
  ADR-0371
- takes the fresh / stale / unreachable vocabulary from ADR-0189 and ADR-0211
- relates to ADR-0013 (bpmn-js), ADR-0025 (properties panel), ADR-0237 (canvas bundle)
- offers boundary events per ADR-0040 for returns declared under ADR-0089 / ADR-0125
- payload shapes reference ADR-0230; layout concerns relate to ADR-0124/0127 and ADR-0252
- relates to ADR-0245 (call-activity drilldown — the nearest existing "look inside")
