# ADR-DRAFT: What an agent may read is authored, the way its reach already is

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) settled what an agent may
*do*: the contained activities of its ad-hoc container are its tools, named by their
element ids and described by the modeller's own `<bpmn:documentation>`. That is the
record's central claim — **what an agent may reach is the diagram** — and it is what
makes an agent reviewable at all. A person can look at a picture and see the whole of
what this thing can set in motion.

What an agent may *know* was never decided, and the code says so plainly. `agent.Request`
carries a `Context map[string]string`. It is serialized into the job payload, rebuilt on
the worker, and rendered into the prompt under "What the process knows:". Nothing ever
fills it. `agent.Resolve` builds a round from the container's documentation, its toolbox
and the results of its own earlier calls — and not one process variable.

So an agent-driven container does not know which case it is working on.

This is not a subtle failure. Running one for the first time against a real model, on a
container documented "prüfe, ob die Finanzierung dieses Kunden tragbar ist", the agent
answered:

> Ich soll die Finanzierung eines Kunden prüfen. Dafür benötige ich folgende
> Informationen: Kaufpreis, Eigenmittel, Bruttojahreseinkommen, gewünschte Laufzeit.
> Bitte geben Sie mir diese Angaben.

It was right to ask. The process had a `dossier` variable with every one of those numbers
in it, and no way to hand it over.

There is a workaround, and it works: give the agent a tool that reads the case file. It is
a legitimate pattern — an agent asking for what it needs is fine — but as the *only* way
it costs a round, hides the process's own data behind a script, and turns "give this agent
the customer's name" into a modelling exercise.

## Decision drivers

- **ADR-0253's argument applies to both halves.** An agent is reviewable because what it
  may run is in the diagram. What it may read belongs in the same place, for the same
  reason: a reviewer who can see one and not the other has seen half the agent.
- **"Everything in scope" is a set nobody chose.** A process carries what it carries — a
  secret reference, a customer's identifier, an earlier model's output, a scratch variable
  an unrelated branch left behind. Sending all of it to a third-party endpoint is a
  decision, and making it the default is making it silently, over and over, and changing
  it whenever someone adds a variable somewhere else in the model.
- **A one-shot ai task needs none of this**
  ([ADR-0256](0256-the-model-is-authored-the-provider-is-configured.md)). Its prompt is a
  literal-or-FEEL value evaluated over the variables it sees, so it carries its own data
  by construction. A container's goal is static text — which is exactly why it needs a
  window, and why the window is the container's alone.

## Considered options

1. **Leave it.** The tool-that-reads-the-file stays the only way.
2. **Every variable the container sees.**
3. **An authored list of variable names on the element** (chosen).
4. **A FEEL expression producing the context.**

## Decision outcome

Chosen: **`<atlas:agentConnector context="dossier,kunde">`** — the names, comma-separated,
of the process variables this agent is given.

### What it means

- The names are resolved up the **container's scope chain**, nearest scope winning, the
  same walk every other authored value takes ([ADR-0068](0068-task-io-variable-mappings.md)).
- Values travel as their **string form** — which is what a model reads anyway, and what
  the prompt already renders. A JSON variable travels as its JSON text.
- A named variable that is **not there** travels as `(not set)` rather than being dropped.
  "The process meant to tell you this and had nothing" is a different fact from "you were
  never told", and only the first one lets an agent say so instead of inventing a value.
- **Naming none is allowed**, and means what it means today: the agent knows its goal, its
  tools, and what its own calls returned. That is a real design — an agent whose tools
  fetch everything it needs — and it stays available.
- On an **ai service task** the attribute is refused, the way the other host's attributes
  already are: a task's prompt is FEEL over the variables it sees, so a second, weaker way
  to pass it data would be a way to write a model that does not deploy.

### Why not a FEEL expression

Option 4 reads as the flexible one and is the wrong trade. The point of the list is that a
reviewer can look at the element and see what leaves the process. A name is readable at a
glance; an expression has to be evaluated in the reader's head, against variables they
would have to go and find. The same argument ADR-0253 makes for tools — the model *is* the
description — makes itself here.

### Why not everything

Option 2 is one line of code and no authoring, and that is its whole appeal. It fails the
driver above: the set is not chosen, it is not stable, and it is sent to somebody else's
endpoint. An agent that needs six variables should say six.

### Consequences

- **Positive:** an agent can be given its case without spending a round to fetch it; what
  it reads is as reviewable as what it may run; and a field that was already plumbed from
  the engine through the payload into the prompt finally has a source.
- **Negative / trade-offs accepted:** another attribute on an element that already carries
  several. And a name here must be kept in step with the variable it points at — a rename
  in one place and not the other leaves `(not set)`, which the agent reports rather than
  hides, but which is still a paper cut a whole-scope dump would not have.
- **Follow-ups:** whether a *tool's* result should be able to say more about itself than
  one FEEL expression is a separate question, and this record does not open it.

## Links

- [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) — agent tool calls drive ad-hoc activation
- [ADR-0254](0254-agent-rounds-on-a-worker.md) — an agent round on a worker
- [ADR-0256](0256-the-model-is-authored-the-provider-is-configured.md) — the model is authored, the provider is configured
- [ADR-0068](0068-task-io-variable-mappings.md) — task I/O variable mappings
