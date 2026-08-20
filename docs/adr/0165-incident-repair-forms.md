# ADR-0165: A form on the incident — repairing an instance with named fields instead of raw JSON

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0158](0158-a-connector-reference-that-explains-itself.md) gave an operator standing
at an incident a way to act on it: correct the instance's variables and retry. It also
recorded, in its own trade-offs, what that way costs:

> The variable editor is raw JSON, which is honest about the value space but asks more
> of the operator than a form would.

That is what the incident offers today. **✎ Fix variables…** opens the instance's whole
variable set as a JSON document, and the operator edits it in a `<textarea>`. It was the
right first move — the endpoint behind it sets and overwrites exactly the keys it is
given, and a value can be an object or an array, which no flat field grid represents
honestly (ADR-0098). But it asks the person repairing a stuck process at an awkward hour
to know which of forty variables matters, what shape it should have, and to produce
valid JSON on the first try.

Meanwhile Atlas already has a form model. A **form** is a form-js JSON schema stored
under an id (ADR-0028), and it binds in exactly two places: to a **user task**
(`UserTaskDetail.FormId`, from `zeebe:formDefinition`) and to a **process start**
(`CompiledProcess.StartFormId`). Nothing binds a form to a service task, to an element
generally, or to an incident. So the surface where a human is *most* likely to need
guidance — a token parked because something went wrong — is the one surface with no way
to say what the human should look at.

The question: **can the modeler, who knows what a task needs, say how a parked instance
of it should be repaired — and can the operator then repair it through named, typed
fields instead of a JSON blob?**

## Decision drivers

- **The modeler knows; the operator is guessing.** Whoever authored the task knows which
  variables its retry depends on and what they must look like. That knowledge currently
  has nowhere to go.
- **Nothing new in the durable record.** A repair form must not introduce a new event, a
  new value type, or a new write path. The audited operator override (ADR-0098) is
  already the way variables are set on a running instance, and it should stay the only
  one.
- **Versioned with the model, not beside it.** What a repair needs is a property of the
  task as it was authored, so it belongs in the compiled process — which also means it
  costs nothing at runtime (I5) and moves with the instance under ADR-0162 migration.
- **Don't take the JSON editor away.** A form shows the fields somebody anticipated. An
  incident nobody anticipated still has to be repairable.
- **A form is data, and rendering it is a UI concern** — the position ADR-0028 already
  took, and there is no reason for this to take a different one.

## Considered options

1. **Bind a form to the element in the model** — `zeebe:formDefinition` on a service,
   send or business rule task, meaning "if this task parks, this is the form for
   repairing it". Compiled, versioned, per task.
2. **Bind one repair form per process**, beside `StartFormId`. Coarser and cheaper: one
   form for anything that goes wrong anywhere in the model.
3. **Bind a form per connector kind or job type**, as operator-managed runtime
   configuration — every mail incident gets the mail repair form.
4. **Leave it as raw JSON** and improve the editor instead (schema hints, a diff view,
   validation against the last known-good values).

## Decision outcome

Chosen option: **1**, with the JSON editor kept as the fallback.

**The binding is a form id on the element.** A task may carry `zeebe:formDefinition`
exactly as a user task does; the compiler interns it into the task's detail as
`FormId`, -1 when unset. The incident already resolves back through the compiled process
to its element — `CompiledProcess.NodeConnectorRef` does precisely that lookup for the
connector (ADR-0162/0160) — so reading a form id off the same node detail is the same
walk, and both incident surfaces can carry it beside the connector fields they already
carry.

**The form edits variables, and writes them the way they are written now.** Opening it
fetches the instance's current variables and prefills the fields whose keys it names;
submitting sends only those keys through `POST /instances/{key}/variables`, the audited
operator override — so every change is still recorded with its actor and still shows up
in the replay (ADR-0098). No new endpoint, no new event, no new durable state. The form
is a better *editor* over a write path that already exists, which is the entire scope of
this record.

**A form may show more than it edits.** The useful part of a repair form is often not
the input but the context: the provider's own message, the element that parked, the
input the task was given, the last three attempts. form-js has readonly fields, so a
repair form can present those beside the two values that actually need changing. This is
the difference between "here is a JSON document, good luck" and "the recipient address
was rejected; here it is, fix it".

**"Save & retry" stays the shape of the action.** The dialog behaves like the JSON one
it sits beside: save only, or save and resolve the incident in the same step. An
operator learns one interaction for repairing an incident, not two.

**The raw editor remains, always.** A form covers the failure its author anticipated.
When the incident is something else — and ADR-0160 exists because it often is — the
operator still needs the whole variable set. So **✎ Fix variables…** does not go away;
a task with a repair form gets a second action beside it, not a replacement.

### Consequences

- **Positive:** the knowledge of what a task needs moves from the person who has it to
  the person who needs it, at the moment they need it. Repairing a stuck instance stops
  requiring familiarity with the model's whole variable set. The binding is versioned
  with the model, so it migrates with an instance under ADR-0162 and cannot drift from
  the task it describes. And it is genuinely small: a compiler field, two response
  fields, a dialog that reuses the existing form renderer and the existing write path.
- **Negative / trade-offs accepted:** a fourth action on the incident row — which the
  table can now absorb behind its ⋯ menu (ADR-0163), but the diagram-side panel shows
  inline and will get busier. A repair form is *anticipatory*: it is written before the
  failure and can be wrong about it, which is why the raw editor stays and why a form
  that names a variable the instance does not have must render an empty field rather
  than refuse to open. A form that writes a variable the model no longer reads is
  silently useless — the same failure mode as any stale form. And this deliberately does
  **not** solve the incident whose cause is configuration rather than data; that is the
  connector dialog's job (ADR-0160), and a form must not pretend otherwise.
- **Follow-ups / risks to watch:** **a form on the connector kind** (option 3) is the
  natural second step — a mail incident's repair form is the same everywhere, and
  binding it per task would mean copying it into every model. **The Modeler has to offer
  the binding**, or the feature exists only for people who hand-edit XML. **A default
  repair form derived from the task's input mappings** would give an operator named
  fields with no authoring at all, and is worth exploring before asking anyone to write
  forms by hand. And if repair forms become common, the incident dialog should say
  *which* form it is showing and let the operator switch to the raw editor in one click,
  rather than making that a separate action from the row.

## Pros and cons of the options

### Bind a form to the element in the model
- Good: the binding lives where the knowledge is, versions with the model, costs nothing
  at runtime, and reuses ADR-0028's form model, ADR-0098's write path and the lookup
  ADR-0160 already built. Per task, so a repair form can be specific enough to be worth
  opening.
- Bad: it is authored per task, so a repair that is really about a *connector* gets
  copied into every model that uses it. Requires Modeler support to be usable by anyone
  who does not edit XML by hand.

### One repair form per process
- Good: one binding, one form, trivially authored.
- Bad: an incident is about one task, and a process-wide form has to be vague enough to
  cover all of them — which lands back at "here are all the variables", the problem this
  record exists to fix.

### A form per connector kind or job type
- Good: authored once, applies everywhere; and the mail case really is the same in every
  model.
- Bad: it is operator-managed runtime state, so it cannot describe *this* task's
  variables — only the shape of the integration's failure. Complementary to option 1
  rather than a substitute, which is why it is a follow-up.

### Improve the JSON editor instead
- Good: no model change, no new binding, helps every incident including the unanticipated
  ones.
- Bad: it makes the JSON document easier to edit without making it easier to *know what
  to edit*, which is the actual difficulty. Worth doing anyway, and orthogonal.

## Links

- extends [ADR-0028](0028-forms-and-the-tasks-app.md): forms bind to a user task and to
  a process start; this adds the third place a form is worth having
- fixes the trade-off [ADR-0158](0158-a-connector-reference-that-explains-itself.md)
  named when it shipped the raw-JSON variable editor on the incident
- writes through ADR-0098's audited operator override, unchanged — the form is an
  editor, not a new path
- complements [ADR-0160](0160-fix-the-connector-from-the-incident.md): that record
  repairs the *integration* behind an incident, this one repairs its *data*, and neither
  substitutes for the other
- reuses the element lookup of [ADR-0162](0162-process-instance-migration.md) /
  ADR-0160 (`NodeConnectorRef` walks a parked element back to its compiled detail; a
  form id is read the same way), and rides an instance's migration because it is
  compiled into the model
- sits behind the ⋯ menu [ADR-0163](0163-deleting-a-referenced-connector.md) gave the
  incidents table, which is what makes a fourth action affordable at all
