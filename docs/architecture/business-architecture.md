# Business architecture: capabilities, value streams, and Atlas

Atlas answers *how* work runs. This document is about the layer above it — *what* an
organisation must be able to do, who owns it, and what it has promised — and about
how to work that way with Atlas as it stands today.

The method is the business architecture set out in **Bernd Ruecker and Leon Strauch,
*Enterprise Process Orchestration*** (Wiley, 2025). It is not an Atlas invention and
this document does not restate the book; it maps the method onto Atlas, says
truthfully which parts Atlas supports today, shows how to work the method with what
exists, and names the parts that are proposed but not built. The design record behind
the proposed parts is
[ADR-draft-business-capabilities-and-value-streams](../adr/draft-business-capabilities-and-value-streams.md).

**Related, but a different document.** [`enterprise-architecture.md`](enterprise-architecture.md)
models *Atlas itself* in ArchiMate — Atlas as a capability provider. This document is
about modelling *your* business capabilities, with Atlas as the tool.

## Contents

1. [The method in one page](#the-method-in-one-page)
2. [What a business capability is](#what-a-business-capability-is)
3. [Where Atlas stands today](#where-atlas-stands-today)
4. [Working this way with Atlas today](#working-this-way-with-atlas-today)
5. [Modelling for measurement](#modelling-for-measurement)
6. [Distributing a KPI as SLAs](#distributing-a-kpi-as-slas)
7. [What is proposed and not built](#what-is-proposed-and-not-built)

---

## The method in one page

Five levels, top down. Each is a different question, and mixing them is the failure
mode the method exists to prevent.

| Level | What it describes | Who owns it | Typical artefact |
|-------|-------------------|-------------|------------------|
| 1 | **Business areas** — the segments of the business model | executive | a list |
| 2 | **Customer journeys and value streams** — the customer's path, and the high-level activity that meets it | C-level / SVP | a value stream with ordered stages |
| 3 | **Strategic end-to-end processes** — a burst of activity inside a value stream | process owner | a strategic model on one page, plus a process profile with goals and KPIs |
| 4 | **Business capabilities** — what has to be done, stated independently of how | capability owner | a capability definition |
| 5 | **Integration capabilities** — the technical means that make a capability reachable | platform team | a Worker Type, an integration flow |

Two properties of the method matter more than the levels themselves, and both are
easy to lose:

**Levels 2 and 3 do not nest cleanly, and that is accepted.** An end-to-end process
spans several stages of a value stream *and* is itself a business capability. You
cannot zoom from a value stream down to an executable process through a strict
containment tree. The method's advice is to ignore the inconsistency rather than
invent a level to hide it, because the fix costs information in the value stream and
buys only tidiness in the picture.

**Capabilities are a flat, tagged list — never a hierarchy.** A capability that looks
top-level in one process is invoked from inside another. Customer onboarding is a
process a customer runs through *and* a step inside issuing a policy. Any tree you
draw is wrong from some direction, and the argument about which tree is right consumes
the time the architecture was supposed to save. Tags reproduce whatever view you need
without committing to one.

## What a business capability is

A capability names a job to be done — *underwrite a loan*, *verify an identity*,
*bill a subscription* — and says nothing about how it is done. Its definition has six
parts:

- **Scope** — what it is responsible for, and explicitly what it is *not*. Underwriting
  owns the credit decision; it does not own address verification. This field settles
  boundary arguments before they start.
- **Input** — the trigger, and the data required. A REST call, an event, a message, or
  a human handing over a form.
- **Output** — the result, and how it is handed back.
- **Owner** — a person on the business side, accountable for the capability's
  performance. Frequently not an IT role, and frequently not a user of your
  orchestration platform.
- **Resources** — the teams, systems and other capabilities it draws on.
- **Metrics and controls** — the KPIs it is steered by and the SLAs it has committed
  to.

Two consequences follow, and both are what makes the method useful rather than
decorative:

**The implementation is free to change.** The same capability can be a purchased
system, a clerk with a form, or an executable BPMN process, and it is expected to
move between them over its life. Swapping a home-grown billing service for a SaaS
product changes the realisation and leaves the end-to-end process untouched.

**A required capability is a black box.** When customer onboarding requires identity
verification, onboarding knows the interface and the SLA and nothing else — not that
identity verification is itself a process, not which systems it calls. In BPMN terms
this is normally a **service task**, not a call activity. A call activity would state
that the callee is a process in this engine, which is an implementation detail of the
other capability and precisely what the black box is hiding.

### A worked example

The book's running example is a consumer loan. Its value stream, in stages:

```
Marketing → Application submission → Credit evaluation and underwriting → Loan disbursement → Repayment monitoring → Closing
```

The capabilities derived from it are a flat list, not a tree. Two of them:

| | Customer Onboarding | Loan Underwriting |
|---|---|---|
| **Scope** | establishing that a new applicant is a customer we may deal with — identity, address, fraud. Not the credit decision. | the credit decision and the bank's risk assessment. Not identity or address checks. |
| **Input** | applicant details, from the web form or a branch clerk | applicant, verified; requested amount and term |
| **Output** | a customer record, or a refusal with a reason | approve / decline / refer, with the reason |
| **Owner** | Head of Customer Operations | Head of Credit Risk |
| **Requires** | Identity Verification | Customer Onboarding's output; Credit Scoring |
| **SLA** | 90% within 10 minutes (internal) | decision within 5 business days (internal) |
| **Realisation today** | executable process | a clerk with a scoring tool — *not automated yet* |

Three things this shows that a process model alone cannot:

- **Underwriting is not automated, and the record says so.** That is the single most
  useful line in the table, and it is the one a tool holding only deployed processes
  can never contain.
- **The end-to-end loan application process is itself a capability**, tagged as such,
  and it spans three stages of the value stream. It orchestrates the capabilities
  above; it does not sit above them in a tree.
- **Onboarding requires Identity Verification as a black box.** Identity Verification
  is very probably an executable process too, but Onboarding does not know that, and
  must not — otherwise replacing it with a purchased service becomes a change to
  Onboarding's model.

## Where Atlas stands today

The honest mapping. Nothing in the "have" column is speculative; nothing in the
"missing" column is a plan.

| Level | What Atlas has today | What is missing |
|-------|----------------------|-----------------|
| 1 Business areas | — | no record; a tag on an application is the nearest thing |
| 2 Value streams | — | no record of streams or their stages |
| 3 End-to-end processes | a deployed BPMN process, versioned, with documentation ([ADR-0143](../adr/0143-process-documentation-export.md)) | no process profile: no goal, no KPI, no business owner |
| 4 Business capabilities | — | no capability record at all |
| 5 Integration capabilities | Worker Types and Workers ([ADR-0203](../adr/0203-worker-execution-model.md)), the connector catalog | nothing ties a Worker Type to the business capability it serves |
| Realisation edge | — | no statement of which process realises which capability |
| Ownership | an application's sharing scope ([ADR-0071](../adr/0071-sharing-scopes.md)) | that is **access control**, not business ownership — see below |
| Metrics | per-element visit and termination counters ([ADR-0080](../adr/0080-runtime-aggregate-counters.md)), the instance timeline, searchable variables ([ADR-0244](../adr/0244-searchable-variables.md)), the OpenSearch export ([ADR-0114](../adr/0114-opensearch-event-exporter.md)) | no KPI or SLA declaration, and nothing that aggregates per capability |
| Architecture drawing | Panorama's ArchiMate documents and derived mesh ([ADR-0189](../adr/0189-panorama-architecture-modeling-and-live-overlays.md), [ADR-0211](../adr/0211-panorama-derived-landscape-mesh.md)) | a drawing has no owner field, no SLA and no list you can query |

**Panorama draws a capability; it does not register one.** Panorama's authorable
subset includes the ArchiMate `Capability` element, and its bindings can point an
element at an Atlas application, process, Worker or release. That is genuinely useful
for communicating an architecture. It is not a capability registry: a shape on a view
has a name and a position, not a scope, an owner, an input contract or an SLA, and
"which capabilities have no realisation" becomes a question about a picture, answered
by reading the picture.

**An application's owner is not a capability's owner.** Atlas's `ownerId`,
`visibility` and `members` on an application decide *who may open and change it*. The
business owner of a capability is accountable for how it performs, is usually a
different person, and often has no Atlas account at all. Do not read one as the other.

## Working this way with Atlas today

The registry described in the next section does not exist yet. The method still works
without it, with conventions and the artefacts Atlas already has. This is what to do
now.

### One capability, one application, one process

Make an Atlas **application** the unit of a business capability
([ADR-0128](../adr/0128-process-applications.md)). Its portable key
([ADR-0134](../adr/0134-git-backed-applications.md)) becomes the capability's stable
name across servers, so the same capability is the same thing in development and in
production. Inside it, one executable process realises the capability; supporting
processes and forms live beside it.

This gives you three things immediately: a boundary that matches the capability's
boundary, a deployable unit that matches the ownership unit, and a name that survives
a move between installations.

### Write the capability definition into the model

`AGENTS.md` already requires a `<bpmn:documentation>` on the process and on every
element whose purpose is not obvious. Use the process-level documentation for the
capability definition itself — the six parts above. Atlas shows it in the Modeler's
Documentation field, beside the selected element in the replay, and it travels with
every deploy, export and version. Written anywhere else, the next reader never finds
it.

Keep it to the definition, not the implementation. If the text explains *how* the
process works, it is element documentation and belongs on the elements.

### Keep a required capability a black box

Call another capability with a **service task** through a Worker, or by publishing a
message — not with a call activity, unless you deliberately mean "the callee is a
process in this engine and I depend on that". This is not pedantry: the choice decides
whether swapping the other capability's implementation for a purchased system is a
configuration change or a model change.

Atlas resolves the call-activity graph, so call activities remain visible and useful —
they are simply a statement about implementation coupling, which is what they should
be.

### Name the outcome, not the shape

Name end events for what happened in business terms — `verified`, `rejected`,
`timed-out` — not `end-1`. The measurement section below is entirely built on this,
and it costs nothing at modelling time.

### Tag, do not nest

Applications carry no capability tag today, so the classification lives in your naming
until the registry exists. Whatever convention you pick, resist a hierarchy: prefix
tags (`area:lending`, `type:end-to-end`, `bian:level1:sales`) express any view you
need without committing to one.

## Modelling for measurement

The method's most practical advice is that process design decides what can be
measured, and that this should be considered *before* the model is built rather than
bolted on after. Atlas's side of it: here is what each pattern actually produces, and
how it is read back.

| Modelling pattern | What Atlas records | How you read it |
|-------------------|--------------------|-----------------|
| **A distinct end event per outcome** (`verified`, `rejected`, `timed-out`) | a per-definition cumulative visit counter per element ([ADR-0080](../adr/0080-runtime-aggregate-counters.md)) | outcome distribution in O(elements) from maintained counters — no scan over instances |
| **The same end states reused** on every branch, including escalation to a human | the same counters | a success rate that does not silently exclude the escalation path |
| **Milestones on the token's path** | every element instance's activation and completion, with timestamps, on the instance timeline | when a milestone was passed, and where time was spent between two of them |
| **Phases as embedded subprocesses** | the subprocess's own element-instance lifecycle | cycle time per phase, not only per process |
| **An interrupting boundary timer** on the step that can stall | a per-element cumulative *termination* counter — tokens that arrived and did not go on ([ADR-0249](../adr/0249-overlay-cancelled-tokens.md)) | a timeout rate per step, separate from the outcome distribution |
| **`atlas:searchable` on the variables you will slice by** ([ADR-0244](../adr/0244-searchable-variables.md)) | an index over those variables | drill-down: cycle time by channel, by amount band, by segment |

Two limits, so nothing here is read as more than it is.

**The visit counters are per process definition, which means per version.** A metric
spanning a redeployment is the sum over that process's versions, not a single read.

**Atlas's Prometheus surface is operational, not business-level.** It exposes durability,
queue depth, open jobs, incidents, active instances — the health of the engine. It
carries no per-process cycle-time histogram and no per-definition labels. Business
metrics come from the state store's counters and the instance history, and at large
instance volumes from the OpenSearch export
([ADR-0114](../adr/0114-opensearch-event-exporter.md)). Do not plan a KPI dashboard
against `/metrics`.

### The milestone gap

The method's milestone marker is a **none intermediate throw event** — an event with
no execution semantics whose only job is to leave a trace in the engine's history, so
that "identity verification started" becomes a readable business state even where no
task sits at that point.

**Atlas does not compile it today.** An intermediate throw event with no event
definition is rejected by the compiler:

```
compiler: intermediate throw event "m": only message, signal, compensation,
escalation, and link events are supported yet
```

Until that changes, use what does compile and does leave a trace:

- **an embedded subprocess per phase** — the honest substitute, since a phase boundary
  is a milestone with a duration attached, which is usually the more useful reading;
- **the nearest named task**, whose visit and timestamps are recorded anyway. Every
  element leaves a trace in Atlas, so a milestone that coincides with a task needs no
  extra element at all.

The dedicated marker earns its place only where no task sits at the point you want to
measure. Supporting it is a small compiler change — the event compiles to a
pass-through node, which link throw events already do — and it is a slice on the
roadmap rather than a gap somebody has to rediscover.

## Distributing a KPI as SLAs

This is the part of the method that makes capabilities more than a filing system, and
it is worth stating precisely because the two words are used loosely everywhere else.

- A **KPI** is a direction with a goal: *disburse within three days*. Nobody is fined
  for missing it.
- An **SLA** is a commitment with a threshold, a window and a counterparty:
  *underwriting decides within five business days*. Missing it has consequences —
  internally a broken promise between two teams, externally a contract or a
  regulator.

A KPI at the top of an end-to-end process is met only if every capability it depends
on holds up. So the end-to-end KPI is **distributed downward as internal SLAs** on the
required capabilities, and the owner of the end-to-end process negotiates those SLAs
rather than reaching into the other team's implementation. When a three-day
disbursement goal is unreachable, the conversation is with the owner of underwriting
about their SLA — not an inspection of underwriting's process model.

This is exactly why a capability declares `requires` as black boxes and carries its
own SLA list: the dependency plus the promise is the whole interface, and it is what
lets an end-to-end target be reasoned about without opening every box beneath it.

Atlas holds none of these declarations today. What it does hold is the data to check
them once they exist, per the table above.

## What is proposed and not built

[ADR-draft-business-capabilities-and-value-streams](../adr/draft-business-capabilities-and-value-streams.md)
proposes two design-time records in a new area service:

- a **`Capability`** record — flat and tagged, with scope, inputs, outputs, business
  owner, resources, realisations, required capabilities, KPIs, SLAs and a lifecycle
  state, and deliberately **no parent field**;
- a **`ValueStream`** record — ordered stages, each naming the capabilities that
  perform it.

The realisation edge — *this capability is currently done by that* — lives on the
capability and points outward by portable key, because two of its four kinds
(a purchased system, manual work) have no BPMN model to carry it. Everything mutable
about it is resolved when it is read and stored nowhere, which is the discipline
[ADR-0189](../adr/0189-panorama-architecture-modeling-and-live-overlays.md) §4
established for Panorama's bindings.

The reverse direction is computed rather than stored — capabilities realised by
nothing, deployed processes no capability claims, dependencies declared but never
called, call activities that cross a capability boundary the caller never declared.
That gap report is the point of the whole thing: it turns the adoption journey into a
list that shrinks.

Read the record for the options weighed, what was refused and why, and the open
question the measurement slice has to answer before any KPI in this method can be
computed rather than merely declared.

## Further reading

- **Bernd Ruecker, Leon Strauch, *Enterprise Process Orchestration: A Hands-on Guide to
  Strategy, People, and Technology That Will Transform Your Business***, Wiley 2025,
  ISBN 978-1-394-30968-9 — the method. Chapter 1 for the business architecture,
  chapter 3 for implementing capabilities, chapter 5 for measurement.
- [`enterprise-architecture.md`](enterprise-architecture.md) — Atlas itself in ArchiMate.
- [`glossary.md`](glossary.md) — the terms used here.
- [`../adr/0189-panorama-architecture-modeling-and-live-overlays.md`](../adr/0189-panorama-architecture-modeling-and-live-overlays.md)
  — the architecture document, its bindings, and the rule that a binding stores no
  mutable fact.
- [`../../ROADMAP.md`](../../ROADMAP.md) — Milestone B, where this is going.
