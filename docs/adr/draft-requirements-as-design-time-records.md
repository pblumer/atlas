# ADR-DRAFT: Requirements as design-time records, attached by a declared edge

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-21
- **Deciders:** Atlas maintainers
- **Open question:** whether a BPMN element id is stable enough in practice to carry a
  requirement's target. The Modeler generates ids, a redrawn element gets a new one,
  and no installation data says how often that happens — the element-level target and
  the gap finding that reports a dangling one both rest on that gap.
- **Question checked:** 2026-09

## Context and problem statement

Atlas records what an organisation must be able to *do*
([ADR-0305](0305-business-capabilities-and-value-streams.md)), what currently *runs*
([ADR-0019](0019-durable-deployments.md)), and prose *about* a model
([ADR-0143](0143-process-documentation-export.md)). It records nothing about what a
process is **obliged to satisfy**, and nothing about where that obligation came from.

That is the gap this record addresses. It is not a gap in documentation — the
documentation export already carries every element's prose. It is a gap in
*identity*: an obligation written into a `documentation` field has no name, no owner,
no state, no second reader and no existence apart from the element it was typed into.
It cannot be listed, cannot be counted, cannot be reviewed, and disappears with the
task.

These are the questions Atlas cannot answer today, and each is asked wherever a
process is audited, certified or handed to a customer:

1. Which obligation does this task exist for, and who said so?
2. Which processes does this regulation touch — all of them, not the one in front of me?
3. If this task is deleted, what obligation loses its only implementation?
4. Which obligations is nothing doing yet?
5. Which obligations has nobody re-read since the person who wrote them left?
6. What did the obligation say when the process was signed off in March?

Question 6 is the one that shows the shape of the answer. It is the question ADR-0143
already answers for a model, by snapshotting prose into an immutable numbered version.
It cannot be answered for an obligation that has no record of its own.

### A word that is already taken

`requirement` occurs throughout this tree and means something else: DMN's **decision
requirements graph** (`dmn/drggraph.go`, `atlas_dmnref_graph`, the read-only DRG view).
That is a graph of decisions and their inputs, and it has nothing to do with this
record. Anything written here says **requirement register** or names the record type
explicitly, and the DMN structure keeps its full name wherever the two could be read
together.

### What exists today, exactly

| What | What it holds | Why it is not this |
|---|---|---|
| BPMN `documentation` per element | free prose, snapshotted at publish into an immutable documentation version ([ADR-0143](0143-process-documentation-export.md), [ADR-0328](0328-the-process-document-shows-the-decision-a-task-runs.md)) | prose with no identity: not listable, not ownable, not shared between two processes, deleted with the element |
| `Capability.kpis` / `slas` | a capability's own targets and promises, with SLA attainment measured against what ran ([ADR-0309](0309-measuring-a-capability.md)) | what a capability *promises*, not what a process is *required* to do; attached to a capability and to nothing else |
| Panorama | the validator accepts ArchiMate `Requirement`, `Constraint`, `Goal`, `Driver` and `Principle` (`api/panorama/validation.go`), the document is stored verbatim and exported unchanged | the motivation aspect is deliberately outside the authorable subset (`api/panorama/subset.go`): motivation elements connect through a different part of the relationship matrix, and a palette without those rules is a palette that cannot be connected. So Atlas can *hold* an ArchiMate requirement and can do nothing with it — it is inert |
| [ADR-0380](0380-a-commitment-is-checked-against-what-the-process-does.md) | a stated commitment warned against the distribution actually observed | the same figure — a promise checked against what ran — on a different subject. Its driver ("no second measurement path") is borrowed below |
| [`api/storeregistry.go`](../../api/storeregistry.go) | the single inventory of everything on disk, with a test that refuses an unclassified directory | it has no requirements entry, which is what makes "Atlas has no requirement register" a fact rather than a search result |

## Decision drivers

- **A requirement outlives the model, and usually predates it.** The state worth
  recording first is *nothing implements this yet*. A representation that cannot exist
  without a process cannot express it — the same argument ADR-0305 made for a
  capability whose `realizations` are empty.
- **The relation is many-to-many.** One regulation touches fourteen processes. Any
  representation that copies the requirement into each of them produces fourteen
  requirements at the first edit.
- **A requirement has its own life.** Owner, state, source, and a review that is not
  the model's review. None of those can hang off an element without the model's
  versioning owning them.
- **Design-time only.** No events, no replay, `applyToState` never sees one (I2, I4).
- **Portable.** Every reference is a portable key ([ADR-0134](0134-git-backed-applications.md)),
  never a local random id, because a register that cannot be moved is not a register.
- **Do not become a second runtime database.** [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §4:
  store the stable reference, resolve every mutable fact at read time.
- **Do not become a second rule engine.** Whatever "this requirement is met" means
  must rest on a fact Atlas already computes. ADR-0380's driver states it: the
  detector's measurement *is* the measurement, and a second one computed a second way
  would disagree with the thing it predicts.
- **Say what could not be known.** A check that passes silently on no data reads as
  confirmation ([ADR-0309](0309-measuring-a-capability.md), ADR-0380).
- **Exchange is part of the decision, not a follow-up.** An owned artefact that cannot
  leave is a hostage, and a register that cannot be reviewed in a pull request will not
  be reviewed at all. ADR-0305 made this argument; it transfers without change.
- **Established seams.** One area, one service ([ADR-0147](0147-splitting-the-api-server-object.md));
  one `sidecar.NewStore`; reads that grow with the instance population run off the loop
  or not at all ([ADR-0239](0239-off-loop-queries.md)).

## Considered options

1. **An extension element on the BPMN element** — `atlas:requirement` in the Atlas
   moddle namespace, carried by the model.
2. **Prose plus a convention** — keep using `documentation`, with a naming convention
   (`REQ-123: …`) and full-text search over it.
3. **ArchiMate motivation elements in Panorama** — open the authoring subset to
   `Requirement`, `Constraint`, `Goal` and `Driver`, and bind them to Atlas resources.
4. **A design-time record with a declared edge outward**, in a new area service.
5. **A read-only projection of an external tool** — Jira, Polarion, DOORS or Azure
   DevOps stays the source of truth; Atlas mirrors and links.

## Decision outcome

Chosen option: **4 — a `Requirement` record in a new `api/requirement` area service,
holding the edge to what it constrains and resolving every mutable fact at read time**,
with named extension points toward option 1 and option 5 rather than silent gaps.

### The `Requirement` record

| Field | Why it is there |
|---|---|
| `key` | **the** identity: the URL, the filename, what `derivedFrom` names, and what an export carries |
| `title` | the one line a list shows |
| `statement` | **one obligation, stated so that it could be checked.** The normative sentence and nothing else |
| `rationale` | why the obligation exists, separately — so the obligation can be checked and the reason can be argued about without touching it |
| `kind` | `business`, `regulatory`, `quality`, `constraint` — a closed vocabulary, because it decides which findings are noise (see the gap report) |
| `source` | where the obligation comes from: free text plus an optional URI. Free text first, because most sources — a clause, a contract section, a minute — are not addressable |
| `owner` | who is accountable, in business terms: name, role, contact, and optionally an Atlas principal. The shape ADR-0305 already argued for |
| `targets` | what this requirement constrains, by portable key. The edge; see below |
| `derivedFrom` | requirement keys this one refines. A set, not a tree; see below |
| `verification` | optional and declarative, from a closed vocabulary; see below |
| `state` | `proposed`, `agreed`, `retired` — a field somebody sets, not a lifecycle Atlas drives |
| `tags` | the only classification there is; a view of the register is a tag query |
| `confirmation` | `confirmedAt` / `confirmedBy` / `confirmedWith` / `confirmationNote`, exactly [ADR-0304](0304-a-capability-says-when-it-was-last-confirmed.md) |
| `revision` | optimistic concurrency; a write against a stale one is refused |

**The key is the identity and the filename**, as it is for a capability, and for the
same reasons: an export reads as `requirements/dsgvo-loeschfrist.json`, diffs like
source, and imports idempotently into a second installation with nothing to remap. The
cost is identical and is accepted rather than hidden — **the key cannot be renamed in
place**, a write that changes it is refused with that reason, and renaming means
export, edit, import.

**`statement` and `rationale` are separate fields, and that is load-bearing.** A
sentence that says both what must hold and why it must hold can be neither checked nor
contested: every challenge to the reason reads as a challenge to the obligation. The
split is also what makes `verification` below meaningful — there is exactly one
sentence for it to be about.

### The tree that is refused, and the edge that is not

ADR-0305 refused a capability hierarchy. **That argument is not reused here, because it
does not hold.** Requirement decomposition is real: a system requirement genuinely is
derived from a stakeholder requirement, every requirements-management tool models it,
and refusing it would be copying a conclusion without its premise.

What is refused is **containment**. `derivedFrom` is a set of requirement keys — an
edge in a directed acyclic graph, not a parent pointer:

- the same sub-requirement is regularly derived from two parents, which a tree cannot
  express and which is precisely why ReqIF has a `SpecRelation` beside its
  `SpecHierarchy`;
- a tree has **positional identity**, and positional identity is what makes an import
  a merge problem. A keyed set reconciles by key, which is what keeps the exchange
  slice below cheap;
- a cycle is refused at write time, because a requirement derived from itself has no
  reading.

The honest cost: a ReqIF `SpecHierarchy` imported into this shape **loses its
ordering**. Document order in a requirements specification carries meaning to its
authors, and this record does not keep it. That is stated here rather than discovered
later.

### The edge: `targets`

The edge lives on the requirement and points outward by portable key:

```
target: { kind: "process",          applicationKey: "consumer-loans", processId: "identity-verification" }
target: { kind: "element",          applicationKey: "consumer-loans", processId: "identity-verification", elementId: "Task_VerifyId" }
target: { kind: "capability",       capabilityKey: "identity-verification" }
target: { kind: "valueStreamStage", valueStreamKey: "onboarding", stageKey: "verify" }
target: { kind: "decision",         decisionId: "eligibility" }
target: { kind: "product",          catalogId: "it-services", productKey: "vpn-access" }
```

It lives there and not on the target for three reasons, each sufficient on its own:
two of the six target kinds are not BPMN and have no model to carry the edge; a
requirement with **no** target is the normal and most interesting starting state; and
one requirement regularly has many targets, which the reverse direction would store
many times. This is ADR-0305's realisation edge, in the other direction, for the same
reasons.

**Everything mutable about a target is resolved at read time and stored nowhere**
([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §4): whether the
application still exists, whether the process is deployed, at which version, whether
the element id is in the version currently deployed.

`kind: "element"` is the fragile one, by construction: an element id is generated by
the Modeler and a redrawn element gets a new one. It is kept because "this obligation
is about *that step*" is the most useful thing a register can say about a process, and
because the failure is **visible** rather than silent — a dangling element target is a
listed finding in the gap report, which is mitigation and not a repair. The front
matter carries this as the record's open question.

### `verification`: optional, declarative, from a closed vocabulary

This is the field that separates a register somebody reads from a register that does
something, and it is where a design of this kind usually goes wrong — by growing a
`satisfied: true/false` flag that a person sets by hand and that lies within a year.

So: `verification` is **optional**, and every kind rests on a fact Atlas already
computes. No new measurement path.

| Kind | The fact it rests on |
|---|---|
| `none` (default) | nothing. A statement somebody reads. Honest, and the majority for a long time |
| `processExists` | the target process is deployed at all |
| `elementReached` | the per-element visit history ([ADR-0022](0022-element-visit-history.md)): this step was actually taken in the window |
| `slaAttained` | [ADR-0309](0309-measuring-a-capability.md)'s attainment for a named SLA of a target capability |
| `confirmedWithin` | a person confirmed it inside a stated horizon — [ADR-0304](0304-a-capability-says-when-it-was-last-confirmed.md)'s date, used as a check |

**A free-form expression is refused.** A FEEL predicate over arbitrary state would make
the register a second rule engine, would put a number somebody acts on under a name
nobody authored (ADR-0309's own reasoning about KPIs it will not compute), and would
need its own evaluation, its own errors and its own versioning. Where an obligation
genuinely needs one, the thing to model is a process — Atlas is the engine for that.

**The result is three-valued, never two.** `satisfied`, `notSatisfied`, or
**`notDetermined`** with a reason: no window, no data in the window, the target is not
deployed, the named SLA carries no `thresholdSeconds`. A check that passes on no data
is the failure ADR-0309 and ADR-0380 both name, and two values cannot express it.

### Confirmation, and the absence of a workflow

`confirmation` is [ADR-0304](0304-a-capability-says-when-it-was-last-confirmed.md)
transplanted whole, including the parts that look like restrictions: set by an explicit
confirmation and by **no** edit, no bulk confirm, and a horizon that is one installation
setting. The argument carries over exactly — the half of a requirement Atlas cannot
check is the half anybody acts on, and a register whose targets resolve and whose
owners left two years ago is worse than no register.

**There is no approval workflow.** `state` is a field somebody sets. Atlas is the
engine an organisation would model an approval workflow *in*, and ADR-0305 declined
the same thing for the same reason.

### The gap report: computed, never stored

Following [ADR-0211](0211-panorama-derived-landscape-mesh.md)'s overlay discipline:

- requirements with no target — the backlog, made visible, except where `state` is
  `retired`, which is supposed to have nothing doing it;
- targets pointing at an application, process, element, capability, stage, decision or
  product that no longer exists;
- `derivedFrom` naming no requirement in the register;
- verifications that came back `notDetermined`, with their reason — the register's own
  blind spots, listed rather than implied;
- requirements nobody has confirmed within the horizon, as `?stale=true` beside
  `?targeted=false`: the review backlog beside the implementation one.

**One finding is deliberately absent: "this deployed process is claimed by no
requirement."** ADR-0305 reports its analogue because the method it implements says
every process realises a capability, so an unclaimed process is a real gap. No such
rule exists for requirements — most processes are legitimately subject to none — so
the same finding here would be noise on the first day and would train every reader to
ignore the report. If an installation wants it, it wants it for one `kind`
(`regulatory`, typically) over a tagged subset, which is a filter somebody states and
not a finding Atlas asserts.

### Exchange

Three layers, decided separately because they cost three different amounts.

1. **Installation level — one line.** Register the store in
   [`api/storeregistry.go`](../../api/storeregistry.go) as `classDesignTime`, and
   [ADR-0107](0107-backup-and-restore.md)'s portable export carries the register
   between installations with nothing further built.
   [ADR-0282](0282-store-registry.md) is what makes that a decision somebody takes
   rather than an omission nobody notices.
2. **Document level — the shape already specified.** One JSON document holding the
   whole register, reconciled **by key rather than by position**, with a `dryRun` that
   names what it would add, change and leave alone before doing any of it. This is the
   exchange slice ROADMAP B9 specifies for the capability map, applied to a second
   subject; it is cheap for the same reason — there are no local ids to remap and no
   positional identity to preserve, so the document is the records as they stand.
3. **Foreign formats — ranked, and not all decided here.**
   - **CSV/XLSX first.** The cheapest useful bridge, and the format real requirement
     lists actually arrive in. [ADR-0084](0084-csv-batch-validation.md)'s batch
     validation is the shape: report every defect in the file, not the first.
   - **ArchiMate Open Exchange, motivation layer, second.** Import maps `Requirement`
     and `Constraint` elements onto records and their `Realization` /
     `Influence` edges onto targets; export writes them back.
     **The authoring subset stays closed** — `api/panorama/subset.go`'s reason is
     unaffected by this record — so ArchiMate stays an exchange boundary and does not
     become a second editor for the register.
   - **ReqIF is named and explicitly not decided.** It is the right standard for
     exchange with DOORS, Polarion and codebeamer, and this record's author could not
     size a usable subset of `SpecObjectType`, `DatatypeDefinition`,
     `AttributeDefinition`, `SpecHierarchy` and the ReqIF-Z archive. Sizing it means
     measuring a real export from the tool an installation actually runs, not reading
     the specification. Deciding it here on an estimate would be exactly the
     manufactured number ADR-0380 exists to prevent.

### The two extension points, named rather than left open

- **Toward option 1.** An `atlas:requirement` extension element naming requirement keys
  on an element would let a delivery team state the link where they already work and
  let it travel with the model. It is not in this record because the compiler would
  have to carry one more piece of metadata and because the register must stay
  authoritative for the five non-BPMN target kinds. If it is built, it is a *secondary*
  source the register reads — never the store. ADR-0305 left `atlas:capability` open
  the same way and for the same reason.
- **Toward option 5.** `source.uri` plus the Jira Worker Type is enough to link a
  record to the ticket it came from today. A synchronising import — the register as a
  projection of an external tool — is a different decision with a different failure
  mode (two sources of truth), and it needs its own record.

### Consequences

- **Positive:** every question in the problem statement becomes a list query. An
  obligation exists before, and independently of, anything implementing it. One
  requirement reaches fourteen processes without being copied. The register moves
  between installations on the day it is registered, and reviews in a pull request.
  Requirement decay is a listed finding rather than a silent fact. Nothing touches the
  log, the processor or recovery.
- **Negative / trade-offs accepted:** a second artefact, maintained by hand, that is
  worthless if nobody maintains it — `confirmation` makes the neglect visible and
  cannot prevent it. The element-level target is fragile by construction and the gap
  report only makes the breakage loud. A ReqIF import arrives flattened, losing
  specification order. The key cannot be renamed in place. `verification: none` will be
  the great majority for a long time, and a register of unverifiable statements is
  exactly the documentation-only failure mode ADR-0305 warns about — the closed
  vocabulary is a floor under that, not a fix.
- **Follow-ups / risks to watch:** the Console surface, German first
  ([ADR-0267](0267-console-speaks-german-first.md)) — a register nobody can filter is a
  register nobody opens. MCP tools beside the HTTP surface, so an agent that deploys a
  process can state what it was required to do. A requirement in the process document
  ([ADR-0143](0143-process-documentation-export.md)), which is what makes question 6
  answerable and is the strongest single reason this record exists. And the naming
  risk: a `Requirement` record shipping next to `dmn/drggraph.go` will be confused with
  DMN's decision requirements graph unless every surface says which one it means.

## Pros and cons of the options

### Option 1 — an extension element on the BPMN element
- Good: the obligation sits where it applies; no second artefact; no reconciliation.
- Good: versioning and export come free, because the model has both.
- Good: the modeller sees it in the properties panel without changing tool.
- Bad: an obligation cannot exist before a process does, so the most valuable state —
  *nothing implements this yet* — is inexpressible.
- Bad: many-to-many becomes copy-and-paste, and the copies diverge at the first edit.
- Bad: owner, state, source and a review cycle of its own have nowhere to live that
  the model's own versioning does not already own.
- Bad: it cannot reach a capability, a value-stream stage, a decision or a catalogue
  product.

### Option 2 — prose plus a convention
- Good: available today, costs nothing, and is what people already do.
- Good: the documentation export already carries it into a signed-off version.
- Bad: a convention is not a schema. `REQ-123` has no owner, no state and no second
  occurrence anybody can find reliably.
- Bad: full-text search cannot answer "which obligations is nothing doing", because
  absence has no text to match.

### Option 3 — ArchiMate motivation elements in Panorama
- Good: the standard's own vocabulary, which architects already use, and the validator
  accepts the types today.
- Good: import and export exist; an ArchiMate document round-trips unchanged already.
- Bad: `api/panorama/subset.go` refuses the motivation aspect for a stated reason —
  those elements connect through a different part of the relationship matrix, so
  opening the palette means adopting that matrix, which is a larger decision than this
  one and is not about requirements.
- Bad: a view is not a list. Owner, source, state and verification would live in
  free-text ArchiMate properties that nothing validates and nothing can query — the
  objection ADR-0305 already raised against modelling capabilities this way.
- Bad: it makes the register the property of whoever owns the drawing.

### Option 4 — a design-time record with a declared edge *(chosen)*
- Good: every field has a place, and the state "nothing implements this" is
  expressible on day one.
- Good: many-to-many is one record with several targets.
- Good: it fits the seams — `sidecar.NewStore`, ADR-0147 for the service, ADR-0189 §4
  for the edge, ADR-0211 for the report, ADR-0304 for the review, ADR-0239 for the
  reads — so most of it is assembly rather than invention.
- Good: exchange is nearly free at the installation level and specified at the
  document level.
- Bad: a second artefact to keep current, by hand.
- Bad: the edge lives outside the model, so a model moved between installations
  arrives unconstrained until the register follows it.

### Option 5 — a read-only projection of an external tool
- Good: no second source of truth; the register is maintained where the requirements
  engineers already work.
- Good: for an installation that runs Polarion or DOORS, this is the honest answer, and
  the Jira Worker Type shows the integration shape exists.
- Bad: it makes the register unavailable to every installation that runs no such tool,
  which is most of them.
- Bad: the target edge — the half that is about *Atlas* — has nowhere to live in the
  foreign tool, so a local record is needed anyway and the projection is an additional
  source, not an alternative one.
- Bad: it puts a core read behind an external system's availability.

## Links

- relates to [ADR-0305](0305-business-capabilities-and-value-streams.md) — the record
  whose shape, key discipline, outward edge and refusals this one follows, and whose
  hierarchy argument it deliberately does **not** reuse
- relates to [ADR-0304](0304-a-capability-says-when-it-was-last-confirmed.md) — the
  confirmation pair, transplanted whole
- relates to [ADR-0309](0309-measuring-a-capability.md) — the SLA attainment
  `verification: slaAttained` rests on, and the rule that a figure Atlas cannot compute
  stays visible as a declaration
- relates to [ADR-0380](0380-a-commitment-is-checked-against-what-the-process-does.md) —
  no second measurement path, and never a check that passes silently on no data
- relates to [ADR-0022](0022-element-visit-history.md) — the visit history
  `verification: elementReached` reads
- relates to [ADR-0143](0143-process-documentation-export.md) — the document a
  requirement has to reach for question 6 to be answerable
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §4 —
  resolve every mutable fact at read time
- relates to [ADR-0211](0211-panorama-derived-landscape-mesh.md) — the computed
  both-directions overlay the gap report follows
- relates to [ADR-0107](0107-backup-and-restore.md) and
  [ADR-0282](0282-store-registry.md) — the design-time export that carries the register,
  and the registry that makes its class a decision
- relates to [ADR-0134](0134-git-backed-applications.md) — the portable application key
  every process and element target uses
- relates to [ADR-0147](0147-splitting-the-api-server-object.md) — a new API area is a
  service
- relates to [ADR-0239](0239-off-loop-queries.md) — the rule the verification and gap
  reads must be measured against
- relates to [ADR-0084](0084-csv-batch-validation.md) — the batch-validation shape the
  CSV import follows
- relates to [ADR-0267](0267-console-speaks-german-first.md) — the Console surface this
  record's follow-up owes
