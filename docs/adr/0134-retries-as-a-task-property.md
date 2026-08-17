# ADR-0134: Retries as a property of every job-backed task

- **Status:** Accepted
- **Date:** 2026-08-17
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. **Retries** — how many attempts the engine grants a task's
> job before an exhausted failure raises an incident (ADR-0061) — is now an authorable property of
> **every task that creates a job**: the job-worker service and send task (`<zeebe:taskDefinition
> retries>`), each connector task (the connector extension's own `retries` attribute, overriding a
> task definition on the same element), the polyglot script task (`<atlas:jobScript retries>`) and
> the business rule task (`<zeebe:calledDecision retries>`). One compiler helper parses all of them,
> defaulting to **3** and **refusing a budget below 1 at deploy time**. The Modeler shows one
> *Failure handling → Retries* field on every one of those kinds, and the budget survives a switch
> of implementation kind. No runtime, event, or recovery change — the compiled details already
> carried a `Retries` field; the gap was authoring.

## Context and problem statement

Atlas's failure model is the **incident** (ADR-0061): a worker that reports a failure decrements the
job's retry budget; while retries remain the job returns to the activatable index for another
attempt, and when they run out the job parks off the index and an incident holds the token until an
operator resolves it. The budget is therefore the single knob that says *"how hard does the engine
try before a human is asked"* — a per-task decision (a flaky HTTP call wants three attempts, a
non-idempotent booking call wants exactly one).

Every compiled task detail already had the field — `ServiceTaskDetail.Retries`,
`ConnectorTaskDetail.Retries`, `ScriptJobTaskDetail.Retries`, `BusinessRuleTaskDetail.Retries`,
`UserTaskDetail.Retries` — but **an author could only set it in two of those places**:

- a job-worker service/send task read `<zeebe:taskDefinition retries>`;
- a business rule task read `<zeebe:calledDecision retries>`;
- a **polyglot script task** was compiled with a hard-coded `defaultRetries` — the
  `<atlas:jobScript>` extension had no `retries` attribute at all;
- a **connector task** (REST, mail, clio, SharePoint, Remedy, web scraping, user provisioning) took
  whatever the task definition said — and the Modeler *removes* the task definition when a connector
  kind is chosen (ADR-0067, mutually exclusive extensions), so a modeled connector task could never
  be anything but the default three. `<atlas:csvConnector retries>` was the single exception, parsed
  inline in the compiler; `<atlas:webscrapeConnector retries>` was parsed into a struct field and
  then **never read**.

And **the Modeler offered no Retries field anywhere** — the property existed only for someone
hand-editing XML.

Two smaller defects sat in the same place. Each site parsed the attribute with its own
`strconv.Atoi` and its own error string, so the diagnostics drifted; and none of them validated the
range. `retries="0"` deployed happily and produced a task that **hangs with no incident**: a job is
on the activatable index only while `Retries > 0` (`state.PutJob`), so a job created with none is
never offered to a worker, never fails, and never raises the incident an operator could resolve.

The question this ADR answers: **is retries a property of the task, or of the particular
implementation a task happens to use** — and where is it authored?

## Decision drivers

- **The property belongs to the task.** "How many attempts before a human is asked" is a business
  decision about the step, not a detail of whether that step is served by a worker, a REST
  connector, or a PowerShell script. Switching a task's implementation must not silently reset it.
- **One authoring surface.** ADR-0067's catalog renders every connector kind's panel from data;
  a retry field should be one entry in that data, not per-kind panel code.
- **Compile, don't interpret (invariant I5).** A budget that cannot produce a running job is a
  modelling error, and deploy is where modelling errors are caught.
- **Back-compatibility.** Models in the wild already carry `<zeebe:taskDefinition retries>` on
  connector tasks and `<atlas:csvConnector retries>`; both must keep working.

## Considered options

1. **One `retries` attribute per implementation extension, with a shared parser** (chosen) — the
   connector's own attribute wins, a task definition on the same element is the fallback.
2. **Keep `<zeebe:taskDefinition retries>` as the only place**, and have the Modeler keep a task
   definition alongside a connector extension purely to hold it.
3. **A single `<atlas:taskRetries>` extension element** on any activity, independent of kind.
4. **Leave authoring as it is** and document the default.

## Decision outcome

**Option 1.** Each implementation extension carries its own `retries` attribute; one helper,
`parseRetries(label, id, attr)`, interprets every one of them:

- blank → `defaultRetries` (3);
- a non-integer → a deploy error naming the element (`service task "t" has invalid retries "lots"`);
- **below 1 → a deploy error**, because a job with no attempt is never handed out — the token would
  park forever with no incident to resolve. `retries="1"` is the way to say "one attempt, no retry".

For a connector task the precedence is **the connector's own `retries`, then the task definition's,
then the default** — so hand-authored models that put the budget on `<zeebe:taskDefinition>` keep
working, while the Modeler (which drops the task definition when a connector kind is chosen) writes
it where the connector can hold it. The connector registry in `connector_compile.go` gains one
accessor per flavor, so the precedence rule lives in one place rather than in each connector's
compile function.

The Modeler gets **one field description**, `RETRIES_FIELD`, appended to every catalog kind by
`withRetries` — so a new connector kind inherits the property with no panel work — plus the same
field on the polyglot script task and the business rule task. Switching implementation kind carries
the budget across (`applyServiceTaskKind` seeds the new extension with the previous kind's value).
The `retries` attribute is declared in `atlas-moddle.json` for the Atlas extensions; for
`<zeebe:calledDecision>`, whose upstream descriptor does not know the attribute Atlas has always
parsed there, `patchZeebeModdle` declares it at load time so the vendored `zeebe.json` stays a
pristine copy (vendor/bpmn/README.md).

**Not included:**

- **The mockup task** (ADR-0120) — the engine simulates it and creates no job, so there is nothing
  to hand out again; its simulated failure raises the incident (or throws the BPMN error) directly.
- **The user task.** Its "worker" is a person (ADR-0028); the detail keeps the default budget, and
  offering an operator-facing retry count would describe nothing the Tasks app does.
- **A FEEL expression for the budget** (Camunda's `fx` on this field). Retries is compiled into an
  `int32` in the node detail at deploy time (invariant I5); making it per-instance would move the
  read onto the activation path. A literal count is what the engine can honour today.

### Consequences

- Every job-backed task's retry budget is authorable, in the Modeler and in XML, in one consistent
  place per kind — including the polyglot script task and every connector task, which could not
  express it at all before.
- `retries="0"` and negative counts are now **deploy errors**. This is a behaviour change for a
  model that carried one: it used to deploy and hang. No model in the repository does.
- Diagnostics for a malformed budget are identical across task kinds.
- `<atlas:webscrapeConnector retries>` is honoured rather than silently ignored.
- The engine, the event log, the state store and recovery are untouched — the compiled details
  already carried the field, so this is a compiler + authoring change only.

## Pros and cons of the options

### Option 1 — a `retries` attribute on each implementation extension, one shared parser (chosen)

- **+** The attribute sits where the rest of that implementation's configuration sits, so it reads
  naturally in the XML and round-trips through the Modeler.
- **+** Existing `<zeebe:taskDefinition retries>` models keep working via the fallback.
- **−** The same attribute name is declared in several places (one moddle property per connector).

### Option 2 — keep the task definition as the only holder

- **+** Exactly one place in the XML.
- **−** ADR-0067 makes the extensions mutually exclusive: a connector task carrying a
  `<zeebe:taskDefinition>` "just for retries" would need the compiler to ignore its `type`, and the
  Modeler's kind picker to keep an extension it otherwise deletes. The one-of invariant would become
  "one-of, except this".

### Option 3 — a kind-independent `<atlas:taskRetries>` element

- **+** Says "property of the task" most directly, and would extend to activities that are not tasks.
- **−** A new element for a single integer, foreign to the Zeebe-compatible spelling authors and
  existing models already use (`<zeebe:taskDefinition retries>` would have to keep working anyway,
  reintroducing the precedence rule this option was meant to avoid).

### Option 4 — leave it

- **−** Leaves a connector task and a script task with no way to express the property at all, and
  keeps `retries="0"` deploying a process that hangs.

## Links

- [ADR-0061](0061-incident-model.md) — the incident model this budget feeds
- [ADR-0067](0067-service-task-connector-catalog.md) — the catalog the Retries field is appended to
- [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) — the polyglot script task that gained the attribute
- [ADR-0111](0111-incident-model-completion.md) — the worker-supplied retry backoff between attempts
