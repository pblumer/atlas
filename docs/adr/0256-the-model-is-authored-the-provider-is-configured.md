# ADR-0256: The model is authored, the provider is configured — and one call is a task

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

Two things are wrong with the agent as it stands, and they have one cause.

**A process cannot ask a model a single question.** [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)
made an agent-driven ad-hoc subprocess the way an agent is modelled, and the compiler
refuses one with no contained activity — pointing at
[ADR-0117](0117-ai-agent-task.md)'s service-task form, which has never been built. So
the most ordinary use of a language model in a business process — *summarise this,
classify that, extract these fields, draft this letter* — has no element. It needs no
tools and no rounds; it is one call, one answer, one variable. Modelling it as an
ad-hoc container with a toolbox would be dressing a straight line as a loop.

**And which model a step uses cannot be chosen by the person choosing it.**
[ADR-0255](0255-agent-models-are-console-workers.md) put the model name on the Worker
record, arguing it must be visible to an operator rather than buried in a vault bundle.
That argument was about the wrong axis. The question is not *Console or vault*; it is
**deployment configuration or model authoring** — and the model is authoring. A
classification step wants a small, cheap model and the advisory step beside it wants a
strong one, in the same process, against the same account and the same key. Under
ADR-0255 that needs `anthropic_haiku`, `anthropic_sonnet` and `anthropic_opus`: three
Workers with an identical endpoint and an identical credential, differing in one string.
That is paperwork the architecture invents, not paperwork the problem has.

## Decision drivers

- **[ADR-0168](0168-connector-work-on-a-worker.md)'s division decides this, and it
  already has.** The engine resolves what only it has; the worker holds the reach and
  the credential. A model *name* is neither — it is an authored value like a decision id
  or a REST task's URL, and it travels in the resolved payload. Putting it on the Worker
  was the less consistent choice, not the more.
- **A credential must never be in a model** ([ADR-0041](0041-connector-management-and-secret-store.md)/[ADR-0069](0069-engine-internal-encrypted-secret-vault.md)),
  and an endpoint is a deployment's business. Neither moves.
- **An operator still has to see what runs.** Whatever a task leaves unsaid has to be
  answerable from the Console without opening a model file.
- **One element per shape.** A step that calls a model once and a container whose model
  chooses among activities are different shapes, and a reader should be able to tell
  them apart on the canvas.
- **No provider in the binary** ([ADR-0117](0117-ai-agent-task.md)). Whatever is added
  runs through the `Model` interface and the adapters that exist.

## Considered options

1. **Leave both.** Ad-hoc only; a model per Worker.
2. **Model on the element, no new element.** Fixes the multiplication, leaves the
   one-shot case unmodellable.
3. **Model on the element, and a service-task form for one call** (chosen).
4. **A model *selector* on the element** — a task names "cheap" or "strong" and the
   Worker maps those to models.

## Decision outcome

Chosen: **"the model is authored on the element, the provider is configured on the
Worker, and a single call is a service task"**.

### The rule

> **A model name is authored. A provider — endpoint, credential, wire format — is configured.**

`<atlas:agentConnector>` grows a **`model`** attribute, on both hosts: the agent-driven
ad-hoc container and the new service task. The Worker's `Model` stays and becomes what
it should have been from the start — the **default** a task inherits when it names none.
That keeps what ADR-0255 got right (an operator reads the Console and knows what an
unspecified step will ask) and drops what it got wrong (that it was the only source).

Option 4 was refused. "Cheap" and "strong" are a vocabulary Atlas would have to define,
maintain against every provider's catalogue, and explain — and the first time somebody
needed a specific model for a specific reason they would be arguing with an abstraction
instead of naming a model. The provider's own model id is the thing everyone already
has.

### One call is a service task

A service task carrying `<atlas:agentConnector>` with a **prompt** and a **result
variable** is ADR-0117's form, minus the part ADR-0253 answered differently: there is no
tool allow-list, because a step with tools *is* the ad-hoc container. The compiler
already refuses an agent-driven ad-hoc with no entry activity and points here; this is
the other side of that sentence.

It takes its own reserved job type — `io.atlas.ai.task` beside `io.atlas.ai.agent` —
rather than a discriminator inside one payload. Two shapes that never have to be told
apart by sniffing is worth an index, and one kind serving several job types is clio's
shape already.

What travels is the resolved prompt, the model and the answer's variable; what does not
is the endpoint and the key. The completion is variables, so the ordinary
`job.Handler` shape serves it — none of ADR-0254's tool-call machinery is involved.

### What does not change

The round loop, the toolbox, the scope-drain boundary, the incident on an unknown tool,
`connector/agent`'s decision logic and both provider adapters. This record adds an
element and moves one string; it does not touch how a round runs.

### Consequences

- **Positive:** the ordinary language-model step becomes modellable at all; a process
  can use three models in three steps against one Worker; and the division of ADR-0168
  reads the same for the agent as for every other kind.
- **Negative / trade-offs accepted:** `<atlas:agentConnector>` now means two things
  depending on where it sits, which a reader has to learn once. A model id in a model
  file is a string that can go stale when a provider retires it — the failure is a 404
  on the first call, which is the same failure a wrong decision id has, and the deploy
  cannot check it without asking the provider.
- **Follow-ups:** ADR-0255 is amended by this, not superseded: its Console record, its
  worker-only placement and its provisioning all stand. And the ad-hoc container is
  still not marked as agent-driven *on the canvas* — a reader must open the panel — which
  is a Modeler question this record does not answer.

## Links

- [ADR-0117](0117-ai-agent-task.md) — an AI agent task (this builds its service-task form)
- [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) — agent tool calls drive ad-hoc activation
- [ADR-0254](0254-agent-rounds-on-a-worker.md) — an agent round on a worker
- [ADR-0255](0255-agent-models-are-console-workers.md) — an agent model is a Console Worker (amended here)
- [ADR-0168](0168-connector-work-on-a-worker.md) — the engine resolves, the worker reaches
