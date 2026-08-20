# ADR-0160: Fix the connector from the incident

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0158](0158-a-connector-reference-that-explains-itself.md) made a parked connector
task say what was actually wrong with it, and gave the incident one way to act:
correct the instance's *variables* and retry. That covers the case where the data is
wrong. It does not cover the case the original report was about.

The operator's next question was the obvious one:

> Shouldn't you be able to adjust the configuration of a service task — the mail
> provider, say — and try again?

A mail task parked on `smtp: dial tcp: connection refused`, or on `no credential (set
credentialsRef to a JSON auth bundle in the vault)`, has nothing wrong with its
variables. What is wrong is the thing it talks to. Three separate layers were tangled
up in that question and they have different answers:

1. **The connector configuration** — endpoint, provider, credential reference, enabled.
   This is operator-managed runtime state (ADR-0041), *not* part of the deployed model,
   and every change already rebuilds the runtime registry inside the same run-loop
   closure that saved it. It is changeable now, and takes effect at once. It was simply
   unreachable from where the failure was reported.
2. **What the model says** — which connector name a task references, its subject
   template, its recipients. That is compiled into an immutable deployment
   (ADR-0019) and is deliberately not editable in place: `applyToState` must produce
   the same result on recovery as it did live (I4), and events are facts (I6). Changing
   it means a new version.
3. **Moving a running instance to that new version** — instance migration. Atlas has
   no such feature; it needs a durable migration event with an element mapping, and its
   own ADR. Out of scope here, and named in the follow-ups so it is not mistaken for an
   oversight.

Only (1) was in reach, and it was three navigations away: read the incident, remember
the connector name, leave for Organization › Connectors, find the row, guess which
record it is, edit it through **two `window.prompt` boxes** — the whole editing surface
a stored connector had — then come back and resolve. The incident did not even name the
connector: `mail: no connector registered as "Patrick Blumer"` says the name, but a
message like `smtp: 535 authentication failed` says nothing at all about *which*
integration produced it.

Two smaller problems fell out of the same look:

- **The edit form and the create form disagreed.** The create form knows that a Gmail
  connector needs a credential bundle and no endpoint, that the preview transport needs
  neither, that Remedy needs both. The two prompts knew none of it — they offered
  `endpoint` and `credentialsRef` to every kind, including the ones that do not use
  them.
- **`PATCH /connectors/{id}` could not change the provider.** The one field the
  operator's question was literally about. Re-creating the connector under the same name
  is not a workaround: the name *is* the binding every deployed model references
  (ADR-0036/0041), so deleting and re-adding it parks every task in between.

## Decision drivers

- **The fix belongs where the failure is reported.** An operator standing at an
  incident should not have to navigate away, match a name by eye, and navigate back.
- **One dialog, not two surfaces that drift.** The rules about which fields a kind and
  provider use are already subtle; a second, poorer editor guarantees they diverge.
- **Say which integration failed.** A message from a provider is unactionable until you
  know which connector produced it.
- **Validate at the moment of typing.** A connector edited into an unusable shape
  should be refused in the dialog, not discovered by the next token parking on it.
- **Nothing new in the durable record.** Connector configuration is runtime state; the
  deployed model and the event log are untouched.

## Considered options

1. **Link the incident to the connector row.** A deep link into Organization ›
   Connectors. Cheap; still leaves the operator editing through two prompts and
   navigating back to resolve.
2. **Name the connector on the incident, and edit it in place through one shared
   dialog.** The incident carries its connector; the dialog is the same one the Console
   uses; saving can retry the parked job in the same step.
3. **Make the service task's model configuration editable too.** What the operator's
   question could be read to ask. Rejected: it breaks I4/I6 — a deployment is immutable
   and `applyToState` must replay identically — and the honest form of it is a new
   version plus instance migration, which is a different, larger feature.

## Decision outcome

Chosen option: **2**, in four parts.

**The incident carries its connector.** `Server.incidentConnector` resolves the parked
element back through its compiled process to the connector reference and reserved job
type behind it, and matches that name and kind against the connector store. Both
incident surfaces — the list at `GET /incidents` and the runtime overlay the live view
and replay poll — gain `connector`, `connectorKind` and `connectorId`. All three are
derived on read from state that already exists; nothing is written back into the
record (I6). `connectorId` is empty when the model references a name nobody has
configured, which is a different situation and renders differently.

**One connector dialog, shared.** [`api/web/connectordialog.js`](../../api/web/connectordialog.js)
holds the dialog and, more importantly, `connectorShape(kind, provider)` — the single
description of which fields a kind and provider actually use, what to call them, and
what to say about them. The Console's create form now derives its field visibility from
the same function instead of its own copy of the rules, and the row's **Edit** opens
the dialog instead of two prompts. It carries the connector's `problem` (ADR-0158) at
the top, an **Enabled** toggle, and the existing `POST /connectors/test` check, so what
is typed can be verified before it is stored.

**The incident opens it, and can retry in the same step.** Every incident row — live
view, replay, incidents table — gains a **⚙ Connector…** action beside ✎ Fix variables…
and ↻ Resolve & retry, with the connector named on the row as a chip. The dialog opens
prefilled, quoting which element is parked on it and why; **Save & retry** writes the
change and hands the job one more attempt against the new configuration. When the
referenced name is not configured at all there is nothing to open, so the action
becomes the way to the Console, where one is created. The connector is fetched fresh
when the dialog opens rather than carried on the incident: the incident is a fact
frozen when the token parked, the connector is live state that may already have moved.

**A connector update is validated like a create.** `PATCH /connectors/{id}` accepts
`provider`, and `normalizeConnectorUpdate` now re-runs the kind's full create
validation against the record the patch produced, instead of only re-normalizing an
SMTP endpoint. Switching a mail connector to Gmail without a credential bundle is
refused with the reason; switching to preview clears the endpoint and credential it no
longer dials, so no dead configuration is left reading as if it were in use. Other
kinds still pass through untouched — emptying a temis endpoint stays allowed and is
reported as a problem on the connector, which is the shape an operator uses to park an
integration they are in the middle of moving.

### Consequences

- **Positive:** the four ways out of an incident — fix the data (ADR-0158), fix the
  integration, retry as-is, or declare the work done by hand
  ([ADR-0159](0159-manual-task-completion-audit.md)) — are now all reachable from the
  incident, and the one an operator most often needs no longer requires leaving it. A stored connector finally has a real edit
  form, with the same per-kind knowledge as the create form and a test button. The
  provider can be changed without deleting the name every deployed model references. A
  bad connector edit is refused where it is typed.
- **Negative / trade-offs accepted:** the incident response grows three fields, and
  computing them costs a connector-store read per listing (bounded by the number of
  configured connectors, which is small). The name is deliberately *not* editable in
  the dialog, so the rename trap of ADR-0158 is unchanged — renaming still means
  editing the models that reference it. Managing connectors stays admin-only
  (ADR-0041), so a non-admin sees the action and is told they cannot use it. And the
  dialog can now disable a connector from an incident, which parks *more* tasks: it is
  labelled with what it does, and it is the same switch the Console has always had.
- **Follow-ups / risks to watch:** **instance migration** is the missing piece behind
  layer 3; it now has its record in
  [ADR-0161](0161-process-instance-migration.md) — a durable per-instance event
  carrying a frozen element mapping, validated so `applyToState` stays deterministic
  across it (I4). Until that is built, a model change reaches running instances only by
  cancelling and restarting them. `connectorShape` covers the five
  managed kinds; a sixth must be added there rather than in either form.

## Pros and cons of the options

### Link the incident to the connector row
- Good: a few lines, and it does remove the "which record is it?" step.
- Bad: leaves the two prompts as the editing surface, and leaves the operator to
  navigate back to resolve. The action is still somewhere else.

### Name the connector on the incident and edit it in place
- Good: the failure and its fix are in one place; the Console's editing surface is
  fixed on the way past, because it is now the same code; the shape rules stop being
  duplicated.
- Bad: more moving parts than a link — a new shared module, three new response fields,
  and a stricter PATCH that can now reject updates it used to accept (which is the
  point, but it is a behaviour change).

### Make the model configuration editable
- Good: it is what the question sounds like it is asking for.
- Bad: breaks the invariant the whole engine rests on. A deployment is immutable and
  replay must be deterministic (I4/I6); editing a compiled process under running
  instances makes recovery disagree with what happened. The legitimate form is a new
  version plus migration.

## Links

- sits beside [ADR-0159](0159-manual-task-completion-audit.md), which adds the fourth
  way out of the same row — finishing a parked task by hand when it cannot succeed here
  at all. That one records a durable operator action; this one changes runtime
  configuration and records nothing, which is the difference between forcing a step the
  engine would not have taken and letting it take the step it was already trying to.
- extends [ADR-0158](0158-a-connector-reference-that-explains-itself.md) — that record
  made the incident say what was wrong and let an operator fix the *data*; this one
  lets them fix the *integration*
- builds on [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) and
  [ADR-0151](0151-incidents-beyond-the-live-diagram.md) (the incident block the live
  view, the replay and the lists share)
- relates to ADR-0036/0041 (a model references a connector by name only; the endpoint,
  provider and credential reference are operator-managed runtime state)
- bounded by ADR-0019 and invariants I4/I6 (a deployment is immutable; `applyToState`
  runs identically live and on recovery), which is why the model side of the question
  is answered by [ADR-0161](0161-process-instance-migration.md) and not here
