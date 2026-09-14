# ADR-DRAFT: The process document shows the decision a business rule task runs

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0143](0143-process-documentation-export.md) made the Modeler export a process
document: the diagram, then one section per element with the prose written about
it. It goes further than prose on purpose — a script task's source and a sequence
flow's FEEL condition are set verbatim, under the rule its own code states:

> The prose says what a step is for; the code says what it actually runs, which is
> exactly what an auditor or a new engineer opening the document needs to see.

A **business rule task** is the one element whose entire behaviour lives outside
the diagram. Today its section says its name, its type, its id and whatever
documentation somebody wrote. The decision table it runs — the rules that actually
decide the outcome — is not in the document at all.

That is not a gap in a nice-to-have. It is the rule above, unapplied to the single
element type it matters most for: a process whose whole branch structure hangs on
`eligibility` documents the gateway that reads the verdict and says nothing about
how the verdict is reached.

## Decision drivers

- **ADR-0143's own rule.** The code an element runs belongs in the document. A
  decision table is that code; there is no principled reason a PowerShell script
  qualifies and a rule table does not.
- **The document is a snapshot, by design.** It is versioned, dated and signed off
  (ADR-0143). It already embeds a script verbatim rather than linking to wherever
  that script lives now.
- **A decision has three homes** (ADR-0321/0319/0322): a draft, a model behind a
  reference, and a decision deployment. The document must name which one it read,
  or the reader cannot tell what they are looking at.
- **Binding decides which one runs** (ADR-0063). A document that shows a table
  without saying how the task binds is telling half the truth.
- **Do not require a second document.** [ADR-0324](0324-decision-documentation.md)
  gives a decision its own document; needing it to exist would make the process
  document incomplete whenever nobody exported one.

## Considered options

1. **Resolve the decision and render its rule table inside the process document**,
   reusing the collector and the table layout the decision document already has.
2. **Name the decision and cross-reference its own document** (ADR-0324).
3. **Show only what the diagram itself carries** — decision id, binding, result
   variable, input mappings — and no rules.
4. **Leave it.**

## Decision outcome

Chosen option: **1 — the rule table is rendered in the process document**, on top
of option 3, which is its base layer and is what a task documents when no model
can be resolved.

Each business rule task's section gains:

- **what the diagram says**: the decision id, the binding (`latest` / `deployment`
  and what each means), the result variable, and the input mappings that feed it —
  read straight off `zeebe:calledDecision`, no network, never missing;
- **what the decision says**: its rule table, laid out by the same renderer the
  decision document uses, preceded by a line naming **where the rules came from**.

A task backed by a temis **Worker** (`atlas:temisConnector`, ADR-0050) has no local
decision to resolve. It says so and names the worker: the rules live in that
service, and claiming otherwise would be worse than saying nothing.

### Where the rules are read from

In this order, and the document says which one it used:

1. the **model behind a DMN reference** providing that decision id
   (`GET /api/v1/dmn-models/{ref}/xml`) — the authoring source under change
   control, and the model a `deployment`-bound task would bundle;
2. otherwise the **decision deployment** providing it
   (`GET /api/v1/decision-deployments/{key}/xml`), naming the version — which is
   the only source a decision deployed from the editor and never saved to the
   model has, and which
   [ADR-draft-a-deployed-decision-satisfies-a-latest-bound-task](draft-a-deployed-decision-satisfies-a-latest-bound-task.md)
   has just made an ordinary state for a `latest`-bound task rather than a trap;
3. otherwise nothing: the section keeps its base layer and says the decision could
   not be resolved. A document that omits a table it could not read is honest; one
   that fails the whole export over it is not.

The reference is preferred over the deployment even for a `latest`-bound task,
where the deployment is what will actually run. That is deliberate: the document
describes the process **as authored**, the reference is the version an author can
change, and the deployment a `latest` task resolves to is chosen at the *next*
deploy and is not knowable from the editor. Saying "Model `eligibility.dmn`" and
"Binding: latest — the newest deployed version at deploy time" tells the reader
both halves without inventing a certainty that does not exist.

### The deployed source becomes readable by any signed-in identity

`GET /api/v1/decision-deployments/{key}/xml` was `operator`. ADR-0322 left it
there because nothing needed it; step 2 above needs it, and roles are flat
(ADR-0209), so a modeler exporting a process document is not an operator.

It is widened to `any` signed-in identity, which is exactly what its BPMN
counterpart `GET /api/v1/processes/{key}/xml` already is. That counterpart exposes
strictly more: a deployed process model carries its scripts, its FEEL conditions,
its worker types and its connector configuration. A decision table behind a task
in that model being harder to read than the model itself was an inconsistency with
no rationale behind it.

The trade is named rather than waved past: a `user` — an identity that only
completes tasks — can now read a deployed decision's rules. They could already
read every deployed process model, including the scripts in it, so this widens the
set of readable *artifacts* rather than the class of fact exposed.

### Consequences

- **Positive:** The document finally answers "what does this step decide, and
  how", which is the question a business rule task raises and the document was
  unable to answer.
- **Positive:** One renderer. The rule table looks the same in a process document
  and in a decision document, because it is the same function — a reader who has
  seen one reads the other without relearning it.
- **Positive:** The base layer is unconditional. Binding, result variable and
  input mappings come from the diagram, so even an unresolvable decision documents
  how the task calls it.
- **Negative / trade-offs accepted:** The rules are duplicated into the document,
  and go stale as the decision changes. That is what a dated snapshot is, and it
  is the same trade ADR-0143 already accepted for embedded scripts.
- **Negative:** The export now makes one request per distinct decision. They are
  fetched once per id and in parallel, and a failure degrades the section rather
  than the export.
- **Negative:** A widened role on the deployed DMN source, above.
- **Follow-ups / risks to watch:** A `latest`-bound task's document names the
  authoring model, not the deployment that will run. If that proves to mislead,
  the honest fix is to show both, not to switch.

## Pros and cons of the options

### Option 1 — render the table in the document *(chosen)*
- Good: applies ADR-0143's stated rule to the element it was missing from.
- Good: reuses the decision document's collector and table layout unchanged.
- Bad: duplication, and a document that can go stale against the decision.

### Option 2 — cross-reference the decision's own document
- Good: one copy of the rules, always current, with its own sign-off.
- Bad: the process document is then incomplete unless somebody remembered to
  export a decision document — and a reader holding one PDF cannot follow a
  reference to a document they were not given.
- Bad: it is not what ADR-0143 does for scripts, so business rule tasks would be
  documented by a rule that applies to nothing else.

### Option 3 — only what the diagram carries
- Good: no network, nothing to fail, no staleness.
- Bad: it does not answer the question. "Calls `eligibility`, binding latest" is
  what the reader already saw in the Modeler.

### Option 4 — leave it
- Good: nothing to change.
- Bad: the one element whose behaviour is entirely off-diagram is the one the
  document says least about.

## Links

- extends [ADR-0143](0143-process-documentation-export.md) — the document this completes
- extends [ADR-0324](0324-decision-documentation.md) — the collector and table renderer reused here
- relates to [ADR-0063](0063-dmn-decision-binding.md) — the binding the section reports
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the deployment read as a fallback source
- relates to [ADR-0322](0322-deploying-one-decision.md) — the role this record widens
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) — why a modeler is not an operator
- relates to [ADR-0050](0050-temis-decision-connector.md) — the worker-backed task that has no local decision
