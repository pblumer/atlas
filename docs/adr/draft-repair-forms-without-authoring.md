# ADR-DRAFT: A repair form without authoring one — per connector kind, and derived from the task

- **Status:** Proposed
- **Date:** 2026-08-24
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0169](0169-incident-repair-forms.md) let a modeler bind a repair form to a task: when
that task parks behind an incident, the operator is shown the fields whoever wrote the
task said matter, instead of the instance's whole variable set as raw JSON. It also named,
in its own follow-ups, the two things that keep it from being useful in practice:

> **a form on the connector kind** (option 3) is the natural second step — a mail incident's
> repair form is the same everywhere, and binding it per task would mean copying it into
> every model. […] **A default repair form derived from the task's input mappings** would
> give an operator named fields with no authoring at all, and is worth exploring before
> asking anyone to write forms by hand.

Both are about the same gap. ADR-0169 shipped a mechanism that only pays off where somebody
took the trouble to author a form and bind it — per task, in every model that has one. The
tasks that park most often are connector tasks, and their failure is the *same failure*
everywhere: a mail connector rejected the recipient, a REST connector got a 4xx. Authoring
that form once per task, in every process that sends mail, is exactly the kind of work
nobody does — so in practice the incident still shows raw JSON.

The question: **can an operator get named fields on an incident without anyone having
authored a form for that task?**

## Decision drivers

- **The common case must need no authoring.** A feature that requires per-task work in
  every model is a feature most incidents will never have. If the default is still raw
  JSON, ADR-0169 has not landed where it matters.
- **Specific beats general, always.** A form somebody wrote for *this* task knows more than
  one written for a whole connector kind, which knows more than one a machine derived. The
  order must be unambiguous and must never let a general answer shadow a specific one.
- **Nothing new in the durable record.** ADR-0169's constraint stands: no new event, no new
  value type, no new write path. The audited operator override (ADR-0098) stays the only
  way variables are set on a running instance.
- **A derived form must not lie about what it knows.** It can name the variables a task
  reads; it cannot know their shape, their validity, or which of them is the problem.
  Presenting a guess with the confidence of an authored form would be worse than the JSON
  editor, which at least does not pretend.
- **One question, one answer.** The surface should ask "what form do I show for this
  incident?" once and get an answer, rather than implementing the precedence itself in
  every place an incident is rendered.

## Considered options

1. **Resolve on the server, behind one endpoint.** The incident says a repair form is
   available and where it came from; a single endpoint answers with the schema, having
   applied the precedence. The dialog renders whatever it is handed.
2. **Resolve in the browser.** The incident carries the task's form id, the connector kind,
   and the derived field list; the dialog decides. No new endpoint.
3. **Materialise a derived form as a stored form** at deploy time — write a real form record
   for every task that has none, so everything downstream sees one uniform binding.
4. **Only do the connector-kind half** and leave derivation out. Less machinery, and it
   covers the tasks that park most.

## Decision outcome

Chosen option: **1 — the server resolves, behind one endpoint.**

**Three sources, one order.** A repair form is resolved most-specific-first:

1. the task's own `zeebe:formDefinition` (ADR-0169) — the modeler wrote it for *this* task;
2. the form an operator bound to the task's **connector kind** — written once for "how a
   mail task fails", and applying to every mail task in every model;
3. a form **derived from the task's input mappings** — no authoring at all.

The order is the whole design. Each step down knows strictly less than the one above, so a
more general answer can never shadow a more specific one, and an operator who authors a form
for one troublesome task does not have to unbind anything to make it win.

**One endpoint answers the question.** `GET /api/v1/incidents/{elementInstanceKey}/repair-form`
returns `{source, formId, name, schema}` — or 404 when none of the three applies, which is
still a real answer and means "the raw editor is the way". Resolving in the browser (option
2) would have put the precedence in the surface, where the live view, the replay panel and
the incidents table would each have to implement it identically and would eventually not.
The incident response keeps a small `repairForm` marker so a row can render its action
without a call per row; the endpoint is what the dialog opens.

**The connector-kind binding is operator configuration, not model.** It is keyed by kind —
`mail`, `rest`, `sharepoint` — and lives in the settings sidecar beside the theme and the
registration process (ADR-0113/0126): org-wide, design-time, captured by the design-time
backup, and never in the durable log. That is the honest home for it, because it describes
*the integration's* failure rather than any one model's data, and because an operator must
be able to change it without redeploying anything.

**A derived form names the variables the task reads, and says that is what it is.** The
derivation walks the task's `zeebe:ioMapping` inputs and collects the process variables
their FEEL source expressions reference — `expr.Compiled.Inputs()` already reports exactly
that. Those are the values the task was handed, so they are the values a retry depends on.
Every field is a plain text input, and the form leads with a line saying it was derived and
that the raw editor holds everything else. It does not guess types: a derived number field
that refuses "0042" would be a fabricated constraint, and the write path takes the value as
typed either way.

**A connector task usually has no input mappings, and that is why these two ship together.**
A connector's configuration lives in its own extension, not in `zeebe:ioMapping`, so
derivation finds nothing there — which is precisely the case the connector-kind form covers.
The two follow-ups are not independent features that happened to be listed together; each is
the other's fallback, and shipping either alone would leave the common case uncovered.

### What is deliberately not done

**Derivation does not read a connector's own expressions.** A mail connector's `to`,
`subject` and `body` are FEEL over the process variables, so in principle the same
derivation applies. It is left out because the connector-kind form is the better answer for
those tasks — authored once, by someone who understands the integration, with real labels —
and because reaching into each connector kind's detail would put a per-kind list in the
resolver, which is the shape this record exists to avoid.

**A derived form is never stored.** Option 3 would have made everything downstream uniform,
at the cost of writing form records nobody asked for, versioning them, and having to decide
what happens when the model changes underneath one. A derivation is cheap and always current;
materialising it trades that for bookkeeping.

### Consequences

- **Positive:** an incident on a task with input mappings, or on any connector kind an
  operator has configured, now offers named fields with no per-task authoring. The
  precedence lives in one function with one test, rather than in three surfaces.
- **Positive:** the connector-kind form is where an integration's knowledge belongs, and one
  person configuring it improves every model at once.
- **Negative / trade-offs accepted:** a derived form is shallow — plain text fields, no
  validation, no labels beyond the variable name — and it can name a variable that is not
  the problem. It says so. A connector-kind form is also anticipatory in the way ADR-0169's
  is, only more so: it cannot know *this* task's data, only the integration's failure shape.
  And there are now three places a repair form can come from, which is more to explain than
  one; the dialog names the source for exactly that reason.
- **Follow-ups / risks to watch:** the derived form's fields are ordered as the mappings are,
  which is the modeler's order and may not be the useful one. If connector-kind forms become
  common, the Console will want to show which kinds have one from the connector list rather
  than only in its own panel. And a per-*connector* binding (rather than per kind) is the
  obvious next ask the moment two mail connectors need different repairs.

## Pros and cons of the options

### Resolve on the server, behind one endpoint
- Good: the precedence exists once. Every surface asks the same question and cannot drift.
  A 404 is a complete answer. Adding a fourth source later changes one function.
- Bad: one more endpoint, and one more round trip when the dialog opens — though the dialog
  already fetched the form and the variables, so it replaces a call rather than adding one.

### Resolve in the browser
- Good: no new endpoint; the incident response already carries most of the inputs.
- Bad: the precedence would be implemented in the incidents table, the live view and the
  replay panel — three copies of a rule whose whole value is being the same everywhere.

### Materialise a derived form as a stored form at deploy time
- Good: everything downstream sees one uniform binding; no resolution at read time.
- Bad: writes form records nobody authored, which then need versioning, cleanup, and a rule
  for what happens when the model changes. A derivation is always current for free.

### Only the connector-kind half
- Good: less machinery, and it covers the tasks that park most often.
- Bad: leaves every non-connector task — a job worker whose retries ran out — back at raw
  JSON, which is the complaint ADR-0169 was written to answer.

## Links

- completes the two follow-ups [ADR-0169](0169-incident-repair-forms.md) named: a form per
  connector kind, and a default derived from the task's input mappings
- writes through ADR-0098's audited operator override, unchanged — as ADR-0169 does, this
  adds an editor and not a write path
- stores the connector-kind binding beside the theme (ADR-0113) and the registration process
  (ADR-0126) in the settings sidecar: org-wide operator configuration, design-time, backed up
- complements [ADR-0160](0160-fix-the-connector-from-the-incident.md): that record repairs
  the integration behind an incident, these forms repair its data
- reads the input mappings of [ADR-0068](0068-task-io-variable-mappings.md), whose source expressions
  already report the variables they reference
