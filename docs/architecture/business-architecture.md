# Business architecture: capabilities, value streams, and Atlas

Atlas answers *how* work runs. This document is about the layer above it — *what* an
organisation must be able to do, who owns it, and what it has promised — and about
how to work that way with Atlas as it stands today.

The method is the business architecture set out in **Bernd Ruecker and Leon Strauch,
*Enterprise Process Orchestration*** (Wiley, 2025). It is not an Atlas invention and
this document does not restate the book; it maps the method onto Atlas, says
truthfully which parts Atlas supports today, shows how to work the method, and names
what is not built. The design record behind it is
[ADR-0305](../adr/0305-business-capabilities-and-value-streams.md).

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
7. [What is built, and what is not](#what-is-built-and-what-is-not)

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

The honest mapping. Nothing in the "have" column is speculative, and the "missing"
column names what is not built rather than what is planned.

| Level | What Atlas has today | What is missing |
|-------|----------------------|-----------------|
| 1 Business areas | a tag on a capability | no record of its own, deliberately |
| 2 Value streams | a **value stream** record: ordered stages, each naming the capabilities that perform it | nothing draws it |
| 3 End-to-end processes | a deployed BPMN process, versioned, with documentation ([ADR-0143](../adr/0143-process-documentation-export.md)); its profile is a capability carrying a tag | nothing |
| 4 Business capabilities | a **capability** record: scope, inputs, outputs, business owner, resources, realisations, dependencies, KPIs, SLAs, tags, state | nothing |
| 5 Integration capabilities | Worker Types and Workers ([ADR-0203](../adr/0203-worker-execution-model.md)), nameable as a capability's realisation | nothing |
| Realisation edge | on the capability, by portable key, resolved at read time and stored nowhere | a model-side declaration, so the claim travels with an exported process |
| Ownership | the capability's **business owner**, beside the application's sharing scope ([ADR-0071](../adr/0071-sharing-scopes.md)) | nothing: the two are separate fields, which is the point |
| Freshness | when somebody last said a record's prose is still true, who said it and who they asked, with a configurable horizon and a review backlog | nothing Atlas can check *itself* — it dates the claim, it cannot verify it |
| Metrics | per-element visit and termination counters ([ADR-0080](../adr/0080-runtime-aggregate-counters.md)), the instance timeline, searchable variables ([ADR-0244](../adr/0244-searchable-variables.md)), the OpenSearch export ([ADR-0114](../adr/0114-opensearch-event-exporter.md)) | **nothing computes a declared KPI or SLA.** They are declarations, and the API says so in every answer that carries one |
| Architecture drawing | Panorama's ArchiMate documents and derived mesh ([ADR-0189](../adr/0189-panorama-architecture-modeling-and-live-overlays.md), [ADR-0211](../adr/0211-panorama-derived-landscape-mesh.md)) | no binding between an ArchiMate `Capability` and the registry record |

**Panorama draws a capability; the registry holds one.** Panorama's authorable subset
includes the ArchiMate `Capability` element, and its bindings can point an element at an
Atlas application, process, Worker or release. That is for communicating an
architecture. It is not the registry: a shape on a view has a name and a position, not a
scope, an owner, an input contract or an SLA, and "which capabilities have no
realisation" would be a question about a picture, answered by reading the picture. The
two meet in a later slice, through binding keys on the drawing that name a registry
record.

## Working this way with Atlas today

### Write the map down

The registry is two records and one report.

```
POST   /api/v1/capabilities            file a capability
GET    /api/v1/capabilities?realized=false   the adoption backlog
GET    /api/v1/capabilities/{key}/coverage   what actually does this, and what depends on it
POST   /api/v1/value-streams           file a value stream with its ordered stages
GET    /api/v1/business-architecture/gaps    the map against what this server runs
GET    /api/v1/business-architecture/subset  the vocabularies this build accepts
```

The same surface is available as MCP tools — `atlas_list_capabilities`,
`atlas_capability_coverage`, `atlas_business_architecture_gaps` and the rest — so an
agent that deploys a process can say what part of the business it is for.

Reading is open to every signed-in identity, because a capability map exists to cross
the silos an application scope draws. Writing takes the modeler role. What *is* filtered
is the landscape a read is resolved against: a process outside your scope is reported as
restricted rather than as missing.

**Start with the capabilities you do not automate.** A capability with no realisation is
valid, expected, and the most useful row in the list — it is the work still done by hand
or by a system nobody wrote down, and `?realized=false` is the backlog. A map that only
contains what Atlas already runs would tell you nothing you could not read off the
deployment list.

**The key is the identity and cannot be renamed.** Choose it as you would a package
name. It is the URL, the filename on disk, and what every `requires` and every
value-stream stage says. Renaming means export, edit, import.

### Name a realisation by its portable key

A process realisation names the application's **portable key**
([ADR-0134](../adr/0134-git-backed-applications.md)) and the BPMN process id, never a
local application id — so the same map resolves on a second server:

```json
{"kind": "process", "applicationKey": "consumer-loans", "processId": "identity-verification"}
{"kind": "worker",  "workerRef": "idmasters-rest"}
{"kind": "system",  "note": "Acme KYC SaaS, REST"}
{"kind": "manual",  "note": "Branch clerk checks the passport"}
```

Nothing about the deployed version, the running instances or whether it still exists is
stored. All of it is resolved when you read, which is why the coverage answer is current
and the record never goes stale about the installation.

### Declare a dependency; do not let it be inferred

`requires` is declared, and Atlas never derives it. From the caller's side a required
capability is normally a **service task** or a message, not a call activity — a call
activity states that the callee is a process in *this* engine, which is an
implementation detail of the other capability and exactly what the black box hides.

Atlas does compare the two. A call activity from one capability's process into
another's, where the caller never declared the dependency, is a finding in the gap
report. It is a comparison and never a merge: a declared dependency with no call
activity is the ordinary case and raises nothing.

### One capability, one application, one process

Make an Atlas **application** the unit of a business capability
([ADR-0128](../adr/0128-process-applications.md)): its boundary matches the
capability's, its deployable unit matches the ownership unit, and its portable key is
what the realisation names. Inside it, one executable process realises the capability;
supporting processes and forms live beside it, and a call between them stays inside the
black box.

### Write the definition into the model as well

`AGENTS.md` already requires a `<bpmn:documentation>` on the process and on every
element whose purpose is not obvious. The capability's own definition now has a better
home, but the process-level documentation is still where a reader in the Modeler is —
so say there what the process does and which capability it realises, and keep the
scope, the owner and the SLA in the record where they can be queried.

### Keeping the map true

The gap report checks a realisation against the deployment registry. It cannot check the
owner, the scope or the SLAs, and those are the fields anybody acts on: the escalation
goes to whoever `owner` names, and an end-to-end target is distributed against the SLAs
beneath it. A map whose realisations are green and whose owners left two years ago is
worse than no map.

So every record carries **when somebody last said it is still true**, and who said it:

```
POST /api/v1/capabilities/{key}/confirmation   {"with": "Head of Credit Risk", "note": "SLA renegotiated to 3 days"}
GET  /api/v1/capabilities?stale=true           the review backlog
GET  /api/v1/settings/confirmation             the horizon this installation applies
```

Four rules make it worth something.

**Only confirming confirms.** No edit sets the date, not even one that rewrites the
owner or an SLA. If saving refreshed it, fixing a typo in the summary would assert that
every field had been re-read. Creating a record does confirm it — somebody just wrote it
down, which is an assertion.

**Say who you asked.** The confirmer is almost never the owner, because the owner
usually has no Atlas account. Leaving `with` empty is legitimate and says you spoke for
the record alone; filling it in says somebody else stood behind it. The read shows both,
alongside whether the confirmer is also the last person who edited it.

**The horizon is yours.** Twelve months by default, the same interval this repository
uses for the two other things it dates and cannot verify. An admin can set it to a
quarter, or to something nothing outlives. That last one silences the check, and it is
allowed — it is one visible number, and every report says which interval it applied.

**A stale record is still served.** It is flagged everywhere it is read and never
withheld. Hiding it would make the map least useful exactly when it needs attention.

Nothing prevents somebody confirming without reading, and the API says so. What the
mechanism buys is that confirming is a deliberate act with a name and a date on it,
rather than the absence of one.

### Ownership: two different things, two different fields

An application's `ownerId`, `visibility` and `members` decide **who may open and change
it** ([ADR-0071](../adr/0071-sharing-scopes.md)). A capability's `owner` is the person
accountable for **how it performs**. They are frequently different people, and the
business owner often has no Atlas account at all — which is why that field is free text
with an optional account link rather than a principal.

Free text is also why nothing checks it. The owner is the field that decays fastest and
the one Atlas can least verify — which is why the record now carries when somebody last
stood behind it, and who they asked. An owner on a record confirmed last month is worth
more than one on a record nobody has read in two years, and the read tells you which.

### Name the outcome, not the shape

Name end events for what happened in business terms — `verified`, `rejected`,
`timed-out` — not `end-1`. The measurement section below is entirely built on this, and
it costs nothing at modelling time.

### Tag; do not nest

Tags are the only classification there is, and the record has no parent field. Prefix
them to express whatever view you need without committing to one: `area:lending`,
`type:end-to-end`, `bian:level1:sales`.

### Moving the map

Both stores are design-time, so the existing design-time export and restore
([ADR-0107](../adr/0107-backup-and-restore.md)) carry the map between installations with
everything else an author moves. Because a record is filed under its own key, the
archive reads as `capabilities/loan-underwriting.json` and diffs like source. A
document-level export and import of the map alone is a named slice, not yet built.

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

### The milestone marker

The method's milestone marker is a **none intermediate throw event** — an event with
no execution semantics whose only job is to leave a trace in the engine's history, so
that "identity verification started" becomes a readable business state even where no
task sits at that point.

**Atlas compiles it** ([ADR-0307](../adr/0307-the-milestone-event-compiles.md)).
Draw an intermediate throw event, leave it without an event definition, name it after the
point it marks. It waits for nothing and needs no worker: the token flows straight
through. What it produces is the record — the visit counters count it, the instance's
step trail carries it in order, and the Operations overlay lights it up — which is the
whole difference between a milestone and a label on a sequence flow.

It is not always the right element, and two alternatives leave the same trace:

- **an embedded subprocess per phase**, where the point you want is a phase boundary. A
  phase has a duration and a milestone does not, and the duration is usually the more
  useful reading;
- **the nearest named task**, whose visit and timestamps are recorded anyway. Every
  element leaves a trace in Atlas, so a milestone that coincides with a task needs no
  extra element at all.

The dedicated marker earns its place where no task and no phase boundary sits at the
point you want to measure.

An intermediate throw event carrying a definition the engine does not implement — a
timer, say, which BPMN allows only on a catch — is refused by name at deploy rather than
compiled to a milestone, so a model that asks for a wait never silently runs straight
through it.

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

## What is built, and what is not

**Built.** The capability record and the value-stream record, their stores and their
routes; the realisation edge with every mutable fact resolved at read time; the
per-capability coverage read; and the gap report, which compares the map against what
this server actually runs and raises ten kinds of finding:

| Finding | What it means |
|---------|---------------|
| `capability.unrealized` | nothing realises it: done by hand, done by a system nobody recorded, or not done |
| `realization.missing` | it names a process or Worker that is not here |
| `process.unclaimed` | a deployed process no capability claims |
| `requires.unknown` | a dependency naming no capability |
| `stage.empty` | a value-stream stage no capability performs |
| `stage.unknown` | a stage naming no capability |
| `process.shared` | two capabilities claim the same deployed process |
| `call.undeclared` | one capability's process calls another's, undeclared |
| `capability.unconfirmed` | nobody has said this capability's prose is still true |
| `value-stream.unconfirmed` | the same, for a value stream |

The report also says how many references your own access hid from it, and what it
looked at — so a suspiciously clean report can be told from an empty installation.

Nine of the ten are facts Atlas checked. The tenth pair is not, and the report does not
pretend otherwise: the owner, the scope and the SLAs are prose about people and
promises, so the only honest thing it can say about them is that nobody has stood behind
them lately. See [Keeping the map true](#keeping-the-map-true).

Two things it deliberately never reports. A realisation by a purchased system or by a
person is not checked, because Atlas cannot see either and reporting them would be
reporting the limits of its eyesight as a defect in your architecture. And a reference
outside your sharing scope is reported as restricted, never as missing.

**The milestone event is built** — see [the milestone marker](#the-milestone-marker)
above. **So are Panorama's binding keys**: an ArchiMate `Capability` carries
`atlas.capabilityKey` and a `ValueStream` carries `atlas.valueStreamKey`, so a drawing
and this register are the same architecture seen twice. The key travels in the exchange
document as an ordinary ArchiMate property; the record's name is resolved on every read,
so the drawing cannot go stale about the register
([ADR-0308](../adr/0308-panorama-binds-the-capability-register.md)).

**Not built, each a named slice on [Milestone B](../../ROADMAP.md):**

- **Measurement.** Every KPI and SLA in the registry is a *declaration*. Nothing computes
  one, and the coverage answer says so in a field rather than leaving a client to render
  a goal as an achievement. The data is there — see
  [Modelling for measurement](#modelling-for-measurement) — but whether it can be
  aggregated at the instance volumes this is aimed at, without the OpenSearch exporter,
  is the open question the decision record carries.
- **Document-level exchange** of the map on its own, with a dry-run import.
- **A model-side declaration**, so a process can state the capability it realises where
  the delivery team already works, and carry it through an export.
- **A Console surface.** Today the registry is the HTTP API and the MCP tools over it
  (`atlas_list_capabilities`, `atlas_capability_coverage`, `atlas_business_architecture_gaps`
  and the rest), so a person authors it through a client and an agent through its tools.

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
