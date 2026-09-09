# ADR-DRAFT: Business capabilities and value streams as design-time records

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers
- **Open question:** whether the KPIs and SLAs this record lets somebody declare can
  actually be computed from Atlas's own state at the instance volumes it is aimed at,
  for an installation that does not run the [ADR-0114](0114-opensearch-event-exporter.md)
  exporter. Nothing has been measured. The declaration model below is only worth
  having if the measurement slice can rest on the state store, and the honest
  fallback — "export to OpenSearch and aggregate there" — is not available to every
  installation.
- **Question checked:** 2026-09

## Context and problem statement

Atlas records *how* work executes. A BPMN model is deployed, compiled, versioned and
run; instances leave a timeline; jobs go to Workers; incidents surface what stalled.
Every one of those is a statement about an implementation.

It holds no statement about *what the organisation must be able to do*. That gap is
not cosmetic. It is the reason a deployed process in Atlas can only be found by its
name and the application it was filed under, and why nobody can ask the server which
part of the business would stop if that process stopped.

Ruecker and Strauch's *Enterprise Process Orchestration* (Wiley, 2025) sets out a
business architecture in five levels that closes exactly this gap, and does so in a
shape a process orchestrator can hold:

| Level | What it is |
|-------|------------|
| 1 | Business areas — the segments of the business model |
| 2 | Customer journeys and **value streams** — the high-level activities that meet a customer's need |
| 3 | Strategic **end-to-end processes** — bursts of activity inside a value stream |
| 4 | **Business capabilities** — what has to be done, stated independently of how |
| 5 | **Integration capabilities** — the technical means that make a capability reachable |

Level 4 carries the method's weight. A business capability names a job to be done —
underwrite a loan, verify an identity, bill a subscription — with a scope, an input,
an output, a business owner, the resources it draws on, and the metrics and controls
it is held to. It says nothing about implementation: the same capability can be a
purchased system, a clerk with a form, or an executable BPMN process, and it is
expected to change between them over its life. The book is explicit that capabilities
are kept as a **flat, tagged list rather than a hierarchy**, that a capability
requires other capabilities as **black boxes**, and that a KPI at the top of an
end-to-end process is distributed downward as **internal SLAs** on the capabilities
that process depends on.

What Atlas holds against those five levels today:

| Level | What Atlas has | What is missing |
|-------|----------------|-----------------|
| 1 | — | no record of business areas |
| 2 | — | no record of value streams or their stages |
| 3 | a deployed BPMN process | no profile: no goal, no KPI, no owner in business terms |
| 4 | — | no capability record at all |
| 5 | Worker Types and Workers ([ADR-0203](0203-worker-execution-model.md)) | the technical half exists; nothing ties it to a business capability |

Panorama ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md)) can
already *draw* an ArchiMate `Capability`, and bind an ArchiMate element to an Atlas
resource. That is genuinely useful and it is not this. A drawing has a shape, a
position and a name; it has no owner field, no input/output contract, no SLA, and no
list a person can sort, filter and answer a question from. Asking Panorama "which
capabilities have no realisation" means parsing views to find boxes with no incoming
realisation edge — a question about a picture, answered by reading the picture.

Concretely, these are the questions Atlas cannot answer today, and each of them is
asked in a real organisation running the method:

1. Which business capability does this deployed process realise, and who owns it on
   the business side?
2. Which capabilities does this capability depend on, and what have those promised?
3. Which capabilities are realised by nothing — still done by hand, or by a system
   Atlas has never heard of?
4. Which deployed processes belong to no capability at all?
5. Which stage of which value stream stalls if this process stalls?
6. What is this capability's target, and what has it committed to?

## Decision drivers

- **The method's own shape decides the model.** Flat and tagged rather than
  hierarchical; implementation-independent; dependencies as black boxes. A record
  that quietly reintroduces a capability tree would be a different method wearing the
  same words.
- **Do not become a second runtime database.** ADR-0189 §4 states the rule for
  bindings: store the stable opaque id, resolve every mutable fact at read time.
  A capability record that stored "currently deployed version 7, healthy" would be a
  stale copy of the deployment registry within a week.
- **Design-time only.** Nothing here reaches the event log, the processor, or
  recovery. It emits no events, it participates in no replay, and `applyToState`
  never sees it (I2, I4).
- **Portable.** A capability map is worth having only if it survives being moved
  between installations, so every reference it holds must be a portable one — the
  application key of [ADR-0134](0134-git-backed-applications.md), not a local
  random id.
- **No unbounded scan on the run loop.** Coverage and gap reads walk deployments and
  can walk instances. [ADR-0239](0239-off-loop-queries.md) is the constraint: a scan
  that grows with the instance population runs off the loop or not at all.
- **One area, one service.** [ADR-0147](0147-splitting-the-api-server-object.md): a
  new API area is its own package holding a `*runloop.Loop`, not more methods on
  `Server`.

## Considered options

1. **Nothing new — express it in Panorama.** Model capabilities as ArchiMate
   `Capability` elements and value streams as ArchiMate `ValueStream` elements,
   binding each to its Atlas resources.
2. **Declare it in the BPMN model.** An `atlas:capability` extension attribute on the
   process, carried by the compiler as metadata and never acted on, with the registry
   derived from what is deployed.
3. **Two design-time records in a new area service**, holding the realisation edge and
   resolving every mutable fact at read time.
4. **Free-form.** Tags on applications plus process documentation
   ([ADR-0143](0143-process-documentation-export.md)) — no new record kind.

## Decision outcome

Chosen option: **3 — two design-time records in a new `api/capability` area service**,
with a named extension point toward option 2 rather than a silent gap.

### The `Capability` record

One flat record per capability. The fields are the method's own definition, not an
invention:

| Field | Why it is there |
|-------|-----------------|
| `id` | local, random — the identity a URL uses |
| `key` | the portable slug, stable across installations, the identity every reference uses |
| `name`, `summary` | what a reader sees in a list |
| `scope` | what this capability is and is **not** responsible for — the method's first field, and the one that settles boundary arguments |
| `inputs`, `outputs` | the interface: trigger and result, each with a kind (`api`, `event`, `message`, `manual`) and a description |
| `owner` | the **business** owner: name, role, contact, and optionally an Atlas principal |
| `resources` | teams, systems and other capabilities this one draws on |
| `realizations` | how it is currently done: `process`, `worker`, `system` or `manual` |
| `requires` | the capability keys this one depends on, as black boxes |
| `kpis` | metric, goal, direction — where it is trying to get to |
| `slas` | metric, threshold, window, internal or external, counterparty — what it has promised |
| `tags` | the only classification there is |
| `state` | `proposed`, `active`, `deprecated` |
| `revision` | optimistic concurrency, as Panorama's model records already use |

**The field that is deliberately absent is `parent`.** The method's advice is to
resist a capability hierarchy, because the hierarchy discussion consumes the time the
architecture was meant to save, and because an "end-to-end capability" is regularly
invoked from inside another one — so the tree is wrong as often as it is right. An
end-to-end process is therefore a capability carrying a tag, not a capability at a
higher level. Refusing the field is what makes that a property of the system rather
than a convention somebody will break under pressure in the second month.

**The owner is free text with an optional principal, not a principal.** The business
owner of a capability is frequently a person who has never signed in to Atlas — an
SVP, a department head, a role rather than an account. Requiring a principal would
either exclude them or produce a directory of ghost accounts. The optional principal
link is there so that an owner who *is* an Atlas user can be resolved.

### The `ValueStream` record

| Field | Why it is there |
|-------|-----------------|
| `id`, `key`, `name`, `description` | as above |
| `owner` | at this level, typically an executive; same shape as a capability's |
| `stages` | **ordered**; each stage has a key, a name, a description and the capability keys that perform it |
| `kpis` | the stream's own targets, which the capabilities' SLAs distribute |
| `tags`, `revision` | as above |

A value stream is ordered because its whole purpose is to show the sequence of
activity that meets a customer need; a capability list is unordered because its whole
purpose is to be composable.

**The inconsistency the method names is accepted rather than engineered away.** An
end-to-end process spans several stages of a value stream *and* is itself a
capability, so a value stream cannot be zoomed into consistently down to executable
processes. The book's advice is to ignore this. A fourth record kind invented to make
the containment total would buy consistency in the diagram and lose information in
the value stream. So a stage names capabilities, an end-to-end capability is named by
every stage it spans, and the read that renders it says so.

### The edge, and its reverse

The realisation edge — *this capability is currently done by that* — lives on the
capability record and points outward by portable key:

```
realization: { kind: "process", applicationKey: "consumer-loans", processId: "identity-verification" }
realization: { kind: "worker",  workerRef: "crm-rest" }
realization: { kind: "system",  note: "Acme KYC SaaS, REST" }
realization: { kind: "manual",  note: "Ops team, four-eyes check, email trigger" }
```

It lives there and not in the BPMN model because two of those four kinds have no BPMN
model to carry it. A capability realised by a purchased system or by a clerk is not an
edge case in this method — it is the normal state of most capabilities before the work
starts, and the record has to be able to say so on day one, which is exactly when it
is most useful.

Everything mutable about a realisation is resolved at read time and stored nowhere:
whether the application still exists, whether the process is deployed, at which
version, whether instances are running. That is ADR-0189 §4's discipline, applied for
the same reason.

The reverse direction is **computed, not stored**, mirroring Panorama's
modelled-but-absent / present-but-unmodelled overlay
([ADR-0211](0211-panorama-derived-landscape-mesh.md)):

- capabilities with no realisation — the manual work, made visible;
- realisations pointing at an application or process that no longer exists;
- deployed processes no capability claims;
- value-stream stages with no capability;
- `requires` entries naming no capability;
- and the one worth the most: a **call activity** from a realising process into a
  process that realises another capability, where the caller's `requires` does not
  name it. Atlas already resolves the call-activity graph, so this is a comparison of
  two things it holds, not new bookkeeping. It is a comparison and never a merge — a
  declared dependency with no call is perfectly normal (the method's black box is
  usually a REST call, not a call activity), so the derived graph can add a finding
  but must never rewrite `requires`.

### What this record does not do

Stated so the gaps are decisions rather than omissions:

- **It measures nothing.** `kpis` and `slas` are declarations. No number in this
  record is computed, and no read of it returns a live figure. Measurement is a
  separate slice, and it is the one carrying the open question above.
- **It does not model Level 1 or Level 5.** Business areas become a tag; integration
  capabilities are Worker Types, which ADR-0203 already defines.
- **It imports no industry reference model.** BIAN, ACORD and eTOM are hierarchical,
  and mapping their levels onto tags (`bian:level1:…`) is a straightforward later
  slice — but doing it here would drag the hierarchy question into the record that
  exists to settle it.
- **It has no approval workflow.** `state` is a field somebody sets, not a lifecycle
  Atlas drives. Atlas is the engine an organisation would model such a workflow *in*.

### Consequences

- **Positive.** The six questions above become one read each. A capability map can be
  authored before any process exists, which is when the method says to author it, and
  the coverage report then fills in as delivery lands. The gap report gives the
  adoption journey a measurable front: capabilities with no realisation is the backlog,
  and it shrinks. Everything stays design-time, so no invariant is touched and no
  event type is added.
- **Negative / trade-offs accepted.** A capability map is a second thing to maintain,
  and a stale one is worse than none — the gap report is the mitigation, not a cure.
  Rolling a set of capabilities up is a tag query, not a tree walk, which is less
  convenient for a reference-model import and is the price of refusing the hierarchy.
  The realisation edge lives outside the model, so exporting an application to git
  carries the process and not the capability that claims it — until the extension
  point below is built.
- **Follow-ups / risks to watch.** The model-side declaration (option 2) as a
  *secondary* source the registry reads: an `atlas:capability` attribute would let a
  delivery team state the claim where they already work, and let it travel with the
  model. It is deliberately not in this record because it needs the compiler to carry
  one more piece of metadata and because the registry has to stay authoritative for
  the non-BPMN realisations. Panorama bindings gain `atlas.capabilityKey` on
  `Capability` and `atlas.valueStreamKey` on `ValueStream`, which is the point where
  the drawing and the registry meet — ArchiMate's `ValueStream` type is already
  accepted by Panorama's validator and only missing from its authorable palette.

## Pros and cons of the options

### Option 1 — express it in Panorama
- Good: nothing new to build; the ArchiMate types already exist; a capability map is
  genuinely a thing architects want to *see*.
- Good: the binding mechanism for pointing at Atlas resources is built and proven.
- Bad: a view is not a list. Owner, scope, input/output, SLA and KPI have nowhere to
  live except free-text ArchiMate properties, which nothing validates and nothing can
  query.
- Bad: the questions this record exists for are list questions. Answering them by
  parsing views makes the answer depend on whether somebody drew the box.
- Bad: it makes the capability map the property of whoever owns the drawing, when the
  method's whole point is that it is shared across silos.

### Option 2 — declare it in the BPMN model
- Good: the claim lives where the delivery team works, and travels with the model
  through export, import and git.
- Good: deploying a process fills the coverage report with no second step.
- Bad: it can only express the realisation kind Atlas can see. A capability done by a
  purchased system or by hand — the majority, at the start — cannot be declared at
  all, which is precisely when the map earns its keep.
- Bad: the capability's own definition (scope, owner, SLA) would have nowhere to live,
  so it would need this record anyway; the model attribute is an additional source,
  not an alternative one.
- Bad: it puts business-architecture edits behind a redeploy.

### Option 3 — two design-time records in an area service *(chosen)*
- Good: every field of the method's definition has a place, and the flat/tagged shape
  is structural rather than advisory.
- Good: all four realisation kinds are expressible from the first day, including the
  ones that say "this is not automated yet".
- Good: it fits the established seams — `sidecar.NewStore` for the store, ADR-0147 for
  the service, ADR-0189's discipline for the edge, ADR-0239 for the reads.
- Bad: a second artefact to keep current, maintained by hand.
- Bad: the edge is outside the model, so a model moved between installations arrives
  unclaimed until the registry follows it.

### Option 4 — tags and documentation
- Good: no new concept, no new store, available today.
- Bad: a tag has no owner, no scope, no SLA and no dependency. Every question above
  stays unanswerable, and the map would exist only in the head of whoever invented the
  tag convention.

## Links

- follows the business architecture of *Enterprise Process Orchestration*, Bernd
  Ruecker and Leon Strauch, Wiley 2025 (ISBN 978-1-394-30968-9), chapters 1, 3 and 5
- described for readers in [`docs/architecture/business-architecture.md`](../architecture/business-architecture.md)
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the
  architecture document, its bindings, and the rule that a binding stores no mutable fact
- relates to [ADR-0211](0211-panorama-derived-landscape-mesh.md) — the derived mesh and
  its both-directions overlay, which the gap report follows
- relates to [ADR-0203](0203-worker-execution-model.md) — Worker Types are the method's
  Level 5 integration capabilities
- relates to [ADR-0134](0134-git-backed-applications.md) — the portable application key
  every realisation reference uses
- relates to [ADR-0147](0147-splitting-the-api-server-object.md) — a new API area is a
  service
- relates to [ADR-0239](0239-off-loop-queries.md) — why the coverage and gap reads run
  off the run loop
- relates to [ADR-0099](0099-archimate-enterprise-architecture-view.md) — Atlas's own
  ArchiMate view, which models Atlas as a capability provider rather than modelling a
  customer's capabilities
