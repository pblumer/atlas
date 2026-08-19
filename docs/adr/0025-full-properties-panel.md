# ADR-0025: Extend the hand-written properties panel instead of vendoring bpmn-js-properties-panel

- **Status:** Proposed (amended 2026-08-18: documentation is carried, not discarded)
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

> **Amendment (2026-08-18) — the compiler carries documentation, reversing the "pure
> passthrough" clause below.** The original decision made `<bpmn:documentation>` invisible
> to everything but the model file: the panel writes it, the codec preserves it, the
> compiler ignores it. That is right for *execution* — documentation must never change
> what runs — but it also meant the prose could only ever be read by opening the model in
> the Modeler. The one place it is most needed is the opposite end: a **user task** is
> worked by a person who never opens the model, and what the modeler wrote about that step
> ("compare the figures with the attachments; if anything is missing, decline") is exactly
> the work instruction that person needs.
>
> So the compiler now **retains** each element's documentation as design-time metadata,
> interned beside the element's BPMN id, and `CompiledProcess.ElementDocumentation` /
> `Documentation` hand it back. The Tasks app shows a user task's documentation above the
> form; `GET /api/v1/tasks` and `/tasks/{key}` carry it as `documentation`.
>
> Two reading surfaces, two sources — deliberately. The **Tasks app** never loads the
> model, so it needs the compiled value over the API. The **Operations instance replay**
> already imports the diagram to draw it, so its Details panel reads the documentation
> straight off the rendered business object: no request, no endpoint, and it covers every
> element rather than only the ones the API happens to expose — including an element the
> instance never reached, whose prose is the answer to "what would this branch have done?".
> Prefer that pattern for any future surface that already holds the model.
>
> The execution guarantee is unchanged and now tested rather than assumed: a documented
> model compiles to *exactly* the graph its undocumented twin compiles to. "Passthrough"
> becomes the narrower, truer statement — the **processor** never reads documentation.
> Carrying it at compile time is what invariant I5 asks for: the model's XML is parsed once,
> at deploy, so no read path re-parses it to show a sentence. Retaining it for every element
> (not only user tasks) costs one `int32` per node and keeps the next surface that wants it —
> an Operations element inspector — a read away rather than a compiler change away.

## Context and problem statement

The Modeler ships a small, hand-written Details panel (ADR-0013) that authors the
handful of extensions the engine actually runs: a task type, a script task's FEEL
expression + result variable, a service task's job type. The reference modeler we
orient on (ADR-0012) exposes a much richer "Implement" surface on the same
selection: a **General** section (id, name, execution properties), an element
**Documentation** field, **input/output variable mappings**, **execution
listeners**, generic **extension properties** (arbitrary name/value pairs), and
per-element **example data**. Users author real executable models, so the panel
needs to cover these.

The roadmap already flagged the open question as "Full properties panel (would
vendor a pre-bundled `bpmn-js-properties-panel`)". This ADR decides **how** the
panel grows: vendor the upstream properties panel, or extend the hand-written one.

The tension is the one ADR-0013 already hit: `bpmn-js-properties-panel` is
ES-module-only and Preact-based. Vendoring it as usable pre-built assets would
reopen ADR-0012's buildless rule (`go build ./...` with no npm prerequisite).

## Decision drivers

- **Keep the build buildless (ADR-0012).** No bundler or npm step may become a
  prerequisite for building or testing Atlas.
- **Author only what the engine executes.** Every property the panel writes must
  map to an extension the compiler and processor understand; the panel must not
  invite models Atlas can't run (the same gating principle as ADR-0013).
- **Incremental coverage.** Each property group should land as its own vertical
  slice, not as a big-bang panel rewrite.
- **Passthrough for the rest.** BPMN carries data Atlas does not interpret
  (documentation, arbitrary extension elements). Storing and round-tripping it
  must not require the engine to understand it. (Amended: documentation is now
  carried into the compiled process for display — still never interpreted.)

## Considered options

1. **Vendor `bpmn-js-properties-panel`** (pre-built) and wire it to the zeebe
   moddle already vendored (ADR-0013).
2. **Bundle the properties panel from source** with a JS bundler in the build.
3. **Extend the hand-written Details panel** group by group, writing the same
   `zeebe:*` / BPMN extension elements through the `bpmn-js` modeling API.

## Decision outcome

Chosen option: **Option 3 — extend the hand-written panel.** Each property group
is added as a vertical slice that reads/writes through the modeling API:

- **General:** element id and name; documentation as a `<bpmn:documentation>`
  child (the compiler carries it as metadata but never acts on it — see the amendment
  above; originally: pure passthrough, the compiler ignores it).
- **Input/output mappings:** `zeebe:ioMapping` source/target rows, gated on the
  variable subsystem (Milestone 1) so the compiler can honor them.
- **Execution listeners** and **extension properties:** `zeebe:executionListeners`
  and `zeebe:properties` — listeners map to engine hooks as they land; extension
  properties are stored and round-tripped even when Atlas assigns them no meaning.
- **Example data:** editor-only mock data attached as an extension element, used
  by Play mode (ADR-0030) and never by the runtime.

Option 1/2 are rejected for the same reason as ADR-0013: they force a bundler and
break the buildless rule. Revisit this ADR if the front end ever adopts a build
step for another reason — at that point vendoring the upstream panel becomes the
cheaper path and this decision should be reconsidered wholesale.

### Consequences

- **Positive:** Buildless rule intact; the panel only ever offers properties the
  engine can act on; each group ships and is tested independently; unknown BPMN
  extensions round-trip untouched.
- **Negative / trade-offs accepted:** We reimplement, slowly, a slice of what the
  upstream panel already does; we will never reach full parity with it, only the
  subset Atlas executes. Hand-written form widgets are more code to maintain.
- **Follow-ups / risks to watch:** Keep a single "extension read/write" helper so
  each group is consistent; if the count of groups balloons, re-evaluate Option 1.

## Links

- builds on ADR-0013 (embedded bpmn-js modeler, hand-written Details panel)
- constrained by ADR-0012 (buildless, self-contained web UI)
- relates to ADR-0030 (Play mode uses example data), Milestone 1 (I/O mappings)
