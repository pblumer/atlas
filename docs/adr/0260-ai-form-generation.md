# ADR-0260: A form is generated at design time, by the Worker an operator already configured

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas has an AI Worker. [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) put a
model inside a running instance as an agent-driven container, [ADR-0254](0254-agent-rounds-on-a-worker.md)
put its rounds on a worker, [ADR-0255](0255-agent-models-are-console-workers.md) made the
model a Console record with an endpoint, a wire format, a model name and a vault
reference to an API key, and [ADR-0256](0256-the-model-is-authored-the-provider-is-configured.md)
added the one-shot ai task. Two provider wire formats ship, spoken over plain HTTP, with
no provider SDK in the binary (ADR-0117).

All of that serves the *runtime*. The authoring side of the same question —
[ADR-0032](0032-modeler-ai-copilot.md), an in-Modeler copilot — is still open, and its
answer was "a panel over an agent endpoint the user configures", written when there was
no such thing as an agent Worker in Atlas. There is one now.

Meanwhile the most repetitive authoring job in the product is not the diagram. It is the
**form**. A start form or a task form is fourteen components, each with a key that has to
match what the process calls that datum, a label, a validation rule and a description; and
the person writing it has just finished writing the same facts down in two other places —
in the process's `<bpmn:documentation>`, and in the input/output mappings that name its
variables. The blank form-js canvas asks them to say it a third time, by hand.

The question this record answers: **where does a generated form come from** — which
model writes it, reached how, on whose credential, and what happens to what it produces.

## Decision drivers

- **One place to configure a model.** ADR-0255's whole argument was that adding a model
  should be a Console entry rather than a deployment change. A second mechanism for
  authoring — its own endpoint field, its own key, its own screen — would undo that on
  the day it shipped, and would be the one nobody remembers to rotate.
- **No credential in the browser.** An author's browser must never hold the API key, and
  a form generator that called a provider from JavaScript would put it there or would
  need a proxy that is this decision under another name.
- **The engine's core loop is not negotiable (I3, ADR-0164).** A model call is seconds to
  minutes and can hang. Wherever it happens, it must not be on the single writer.
- **A generated artifact is a draft.** ADR-0032 settled this for diagrams: what a model
  produces is a proposal that passes the same gate a hand-drawn one does, and the author
  reviews it. A form that generated itself into the store would be a different product.
- **What the model reads should be what a reviewer can read.** ADR-0253's central claim
  about agents — what an agent may reach is the diagram — applies here too: the material
  a generation sees is the process model, which is versioned, reviewable and already
  written.

## Considered options

1. **Generate in the browser**, against an endpoint the author configures in the Modeler
   — ADR-0032's original shape, applied to forms.
2. **Generate through the job path**: a system process with an ai task in it, started per
   generation, its answer read back from the instance.
3. **Generate in the engine process, from the agent Worker's record** — a design-time
   HTTP handler that resolves the Console record, calls the same adapters the runtime
   agent uses, and returns an unsaved schema (chosen).

## Decision outcome

Chosen option: **"generate in the engine process, from the agent Worker's record"**.

`POST /api/v1/forms/generate` takes a brief and, optionally, the process the form belongs
to and the step it is for. It resolves an agent Worker (ADR-0255) — endpoint, wire format,
model name, and the API key from the vault — builds a prompt, calls `agent.Model` through
the adapter for that wire format, checks what came back, and returns a form-js schema.
`GET /api/v1/forms/generate/workers` says whether there is anything to ask, so the editor
can leave the button out rather than offering a failure.

**Nothing is stored.** The schema arrives in the form editor unsaved, under the id the
editor was already holding, and the author reads it and presses Save themselves through
the ordinary save path — with the ordinary id check (ADR-0222), the ordinary scope check
(ADR-0071), and their own name on it. This is ADR-0032's sentence about diagrams, applied
to forms for the same reason.

**The entry point is the diagram, not the forms list.** "Create a new form" on a user
task, and on a start event, carries the process and the element into the route
(`#/modeler/form/new/for/{processId}[/{elementId}]`), and the editor opens with the
generator already on that step. Pressing that link *is* the author saying what the form
is for; asking them to say it again in a dialog is asking twice for one answer. A start
event names the process and no element, because a start form is for starting the process
rather than for a step inside it. A **repair form**'s link (ADR-0169) stays the plain one:
it is neither of the two things this generator writes — it is the subset of a parked
instance's variables an operator corrects — and opening the generator on that task would
frame it as the task's work form, the confusion that panel's wording exists to prevent.
The button in the form editor's own bar remains, for a form begun from the forms list.

### Why this is not a service task, and does not contradict ADR-0164

ADR-0164 says no side-effecting service task runs in the engine process, because a slow
endpoint must never be able to wedge the core loop. That is a rule about the **job path**,
and a generation is not on it: there is no instance, no token, no job, no event, no
retry, no incident. It is an HTTP request handler, and the call runs on that request's own
goroutine under that request's own context — the loop is touched only to read the Worker
record and its credential, in one turn, before the call. A model endpoint that hangs costs
the author their spinner and nothing else. The precedent is already in the tree: the
engine makes outbound design-time calls in `api/promote.go` and `api/panoramaremote.go`.

The runtime rule is untouched. An agent round and an ai task still run **only** on a
worker, exactly as ADR-0254 and ADR-0164 require. What is shared between the two is the
adapter and the configuration, not the execution path.

Option 1 is rejected on the credential and on the configuration: it either puts an API key
in a browser or invents a second place to configure a model, and it cannot read the draft
the author is working on without shipping the whole BPMN outline through the browser first.
Option 2 is rejected as machinery: a durable instance, a job, a lease and an incident per
"write me a form" — the engine would be doing bookkeeping for something with nothing to
recover. Recovering a generation nobody was waiting for is not a feature.

### What the model is told, and what it may read

Two prompts. The **system prompt** is Atlas's and fixed: answer with one form-js document
and nothing else, out of a stated vocabulary. The **goal** is the request: the author's
brief in their own language, the process's own account of itself, and — when this is a
refinement — the form as it stands.

The process's account is read tolerantly from its BPMN, draft first and deployed version
second: the process id, name and documentation, each step's kind, name and
`<bpmn:documentation>`, sequence-flow conditions, and the variable names its mappings,
data objects and result variables use. It is a tolerant walk and not a compile, because a
draft mid-edit is routinely not deployable and that is exactly when this helps most. It is
bounded, because an outline of a 400-element landscape would crowd out the brief.

Nothing about a running instance is read. There are no instance variables here and no
case data: a generation sees the model, which is the reviewable artifact, and nothing a
customer ever typed.

### What comes back is checked before it is shown

A model's answer is unwrapped from whatever prose came with it, and then gated:

- The root is forced to form-js's one root type, and the form's **id is the editor's**,
  never the model's — a generated rename would unbind the user task that binds it
  (ADR-0222).
- The vocabulary is a **subset** of form-js: the components that render in a task form
  with no further wiring. The iframe, the html block, the file picker, the document
  preview and the expression field each need something a generation cannot supply — an
  origin to embed, sanitized markup, a document store, a FEEL expression over variables it
  has not seen — so a component outside the subset is refused **by name** rather than
  dropped. A form quietly missing the field the author asked for is worse than one that
  says it could not be written. The subset the prompt offers and the subset the gate
  accepts are held together by a test, not by discipline.
- Every input gets a usable, unique **key** — the model's own where it wrote one, because
  naming a field the way the process already names that datum is what the outline was
  handed over for, and a transliterated one derived from the label otherwise.
- The document is bounded, in bytes and in component count.

An answer that is not a form is a **422** with the reason. It is neither a server failure
nor the author's mistake, and reporting it as either would send somebody looking in the
wrong place.

### Consequences

- **Positive:** an installation that already has an AI Worker gets form generation with no
  further configuration; there is one model configuration, one credential and one place to
  change either; the browser never sees a key; the material the model reads is the diagram,
  which is versioned and reviewable; and what it produces passes the same gate a
  hand-drawn form does, because it *is* a hand-drawn form until somebody saves it.
- **Negative / trade-offs accepted:**
  - The engine process now makes an outbound model call. Bounded and off the loop, but it
    is one more thing the engine's process does, and the first that is neither
    engine-to-engine nor an identity provider.
  - Only Console records can be asked. A model configured purely in a worker's
    environment (`ATLAS_AGENT_*`) is that worker's own and is invisible here; ADR-0255
    made the record the way to configure an agent, and this is the first thing that
    depends on it.
  - The generation is not audited. It writes nothing and reads only design-time state, so
    there is no event to write it to — but it does spend an operator's budget, and the
    number of times somebody pressed the button is not currently anywhere.
  - The generator's vocabulary will lag form-js. A new component type is two edits and a
    test, and until they are made an author asking for it gets a clear refusal.
- **Follow-ups / risks to watch:**
  - **The same seam serves the diagram.** ADR-0032's copilot is the same shape as this —
    a design-time authoring call to the Worker an operator configured — and this record
    is the argument it was missing. Whoever writes it should reuse this seam rather than
    invent a second one.
  - **Cost visibility.** One button, one model call, no meter. If generation becomes
    common, a per-Worker count belongs in the Console beside the model name.
  - **The process information model** (ADR-0230) is the obvious next thing for the prompt
    to read: a data object's `itemSubjectRef` resolves to a typed class, and a form
    generated against those types would produce keys that match by construction rather
    than by the model's judgement.

## Pros and cons of the options

### Option 1 — generate in the browser
- Good: no engine involvement at all; the Modeler talks to whatever the author has.
- Bad: the credential is in the browser, or behind a proxy that is Option 3 wearing a hat;
  a second place to configure a model, which ADR-0255 spent a record removing; and the
  draft's own words have to be shipped out of Atlas and back before the model sees them.

### Option 2 — generate through the job path
- Good: reuses the runtime seam exactly; the call is on a worker, where ADR-0164 wants it;
  every generation is durable and inspectable.
- Bad: an instance, a job, a lease, a retry policy and an incident for an authoring
  action with nothing to recover; the author waits on a poll rather than on a response;
  and a failed generation becomes an operational alert somebody has to resolve.

### Option 3 — generate in the engine, from the Worker's record
- Good: one configuration and one credential for runtime and authoring alike; the request
  is a request, with a status and a message the author reads; the draft is read from
  design-time state directly, so the process the author is looking at is the process the
  model sees.
- Bad: an outbound call from the engine process, which needs the argument above rather
  than being obviously fine; and a model configured only in a worker's environment cannot
  be reached.

## Links

- gives [ADR-0032](0032-modeler-ai-copilot.md) (in-Modeler AI copilot) its seam: the same
  design-time authoring call, for the diagram instead of the form
- builds on [ADR-0255](0255-agent-models-are-console-workers.md) (an agent model is a
  Console Worker) and [ADR-0256](0256-the-model-is-authored-the-provider-is-configured.md)
  (the model is authored, the provider is configured)
- reuses the adapters of [ADR-0254](0254-agent-rounds-on-a-worker.md) and the
  no-provider-SDK stance of [ADR-0117](0117-ai-agent-task.md)
- bounded by [ADR-0164](0164-no-in-process-service-tasks.md) (no in-process service tasks),
  which is about the job path and is not relaxed here
- produces a form under [ADR-0028](0028-forms-and-the-tasks-app.md), keeps its identity
  per [ADR-0222](0222-artifact-id-renames.md), and is an area service under
  [ADR-0147](0147-splitting-the-api-server-object.md)
- reads the process, never an instance — the authoring counterpart of
  [ADR-0257](0257-what-an-agent-may-read.md)
