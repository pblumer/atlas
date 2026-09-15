# ADR-0326: A decision can be tried against sample inputs before anything is deployed

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

A decision table is a program. Like every program, the question its author asks
first is *does this do what I meant* — and until now Atlas could only answer it
the long way round:

1. save the decision to the model,
2. deploy it ([ADR-0322](0322-deploying-one-decision.md) made that one button, which is
   progress — before it, publish the whole application),
3. deploy a BPMN process with a business rule task that calls it,
4. start an instance,
5. read the result off the instance, or find the evaluation in
   **Operations → Decisions**.

Five steps, three of which are about processes, to answer a question about one
table. #915 listed a "dedicated DMN test UI" as explicitly out of scope, and #919
says this is where it would belong and that **it should be its own decision**.
This is that decision.

Everything needed is already in the building. temis compiles and evaluates
(ADR-0014); it produces a **trace** saying which tables ran, which rules matched
and why (ADR-0066); and the Console already draws that trace as a rule matrix in
Operations → Decisions, where a rule that never matches shows its condition in
red. The only thing missing is a way to ask the question about a model that is not
deployed — the one in front of the author.

The question: **what does an author evaluate when they press Test, and what does
the server have to hold to answer?**

## Decision drivers

- **Answer the question actually being asked.** "Does what I just typed work?" is
  about the unsaved model on screen, not about what production is running. The
  latter is a different question, and Operations already answers it.
- **Nothing is created by trying.** No key from the definition key space, no
  durable record, no registry entry, no release. Trying must not be a deploy with
  a different name — that is the "no UI-to-engine shortcut" line #919 draws.
- **One trace renderer.** The rule matrix exists. A second one that drifts from it
  would be a second DMN renderer in all but name.
- **A failure is a result, not an error.** A model that does not compile and inputs
  that do not satisfy it are the normal output of authoring. The panel must be able
  to show them.
- **Design-time only.** No engine state, no invariant, no run loop.

## Considered options

1. **Evaluate the XML on screen, in a throwaway compile** — the model is compiled
   for this one call and discarded.
2. **Evaluate the deployed version** through the DMN registry by key.
3. **A playground session**, as BPMN has ([ADR-0215](0215-modeler-playground.md)): open a sandbox, run cases
   against it, close it.
4. **Reuse `POST /api/v1/feel/evaluate`** — the author tests the expressions.

## Decision outcome

Chosen option: **1 — evaluate the XML on screen, compiled for this call and
thrown away.**

`POST /api/v1/decisions/evaluate` takes `{xml, decisionId, inputs}` and answers
with the model's decisions, the outputs, and the temis trace. It compiles with a
temis engine of its own — the validator's, which exists precisely to compile
without deploying (`dmn.Validator`, ADR-0034 Phase 2) — so:

- no key is allocated, so the definition key space is untouched;
- no record is written, so nothing survives the request;
- the DMN registry is not consulted and not modified, so nothing a process is
  bound to can move;
- it never touches the run loop, so I2 and I3 are not in play at all.

That is the whole point of the option: **trying a decision is a pure function of
the bytes in the request.** Two people trying two different edits of the same
decision at the same moment cannot see each other, because there is nothing shared
to see.

### One call answers both halves

The panel needs two things: what this model can run and what it wants, and then
what it returns. Those come from the same compile, so they come from the same
call:

- with **no `decisionId`**, the response describes the model — its decisions, each
  with the input data it consumes (transitively, ADR-0039) and its output. The
  panel builds its form from that, so the fields are temis's own view of the model
  rather than something the browser re-derived from the XML.
- with a **`decisionId`**, the same response additionally carries `outputs` and
  `trace`.

It is one resource — *what this model does with these inputs* — answered as
completely as the request allows, not two modes.

### A failure comes back 200

The response carries `ok`, and `ok: false` with a `message` is how a model that
does not compile, a decision id that is not in it, and an evaluation that errors
all arrive. This is `POST /api/v1/feel/evaluate`'s grammar, for its reason: while
somebody is authoring, a thing that does not work yet is the expected state, and a
panel that has to distinguish "the server refused my request" from "my table is
not finished" in order to render is a panel that will get it wrong.

A malformed *request* — a body that is not JSON, an empty `xml` — is still a 400.
The distinction is whether the caller made a mistake or the model did.

### The trace is the one Operations draws

The rule matrix — a row per rule, input columns tinted by whether each condition
held, the matched rule highlighted — moves out of `app.js` into a module both use
(`api/web/dmn-trace.js`). The panel does not get its own. A decision traced in the
Modeler and the same decision traced in Operations therefore look the same, which
is the point: the author learns one picture.

### Consequences

- **Positive:** The loop closes in the editor. Type a rule, press Test, see which
  rule fired and why — without a process, a deploy, or an instance.
- **Positive:** It works on a decision that has never been saved anywhere, which is
  exactly when the question is asked most.
- **Positive:** Nothing to clean up. There is no session to close, no sandbox to
  reap, no record to prune.
- **Negative / trade-offs accepted:** The model is recompiled on every press. A
  decision table is small and this is a human pressing a button, so the cost is a
  few milliseconds; a cache would be state, which is exactly what this decision
  spends its value avoiding.
- **Negative:** Testing the *deployed* version is not offered here. It is a
  different question with a different answer (Operations → Decisions), and
  conflating them would put a "which one am I looking at?" control on a panel whose
  whole appeal is that there is nothing to choose.
- **Negative:** An expensive model can occupy a request. It is bounded by a
  timeout and by the model-upload budget, and it is a modeler-role route; that is
  the same exposure `POST /api/v1/feel/evaluate` and the deploy already carry,
  since both compile and run author-supplied logic.
- **Follow-ups / risks to watch:** Saved test cases — "these inputs should give
  that output", kept with the decision and re-run — are the obvious next thing and
  deliberately not here: a stored expectation is durable state with a lifecycle,
  and it should be recorded on its own.

## Pros and cons of the options

### Option 1 — evaluate the XML on screen *(chosen)*
- Good: answers the question an author has, at the moment they have it.
- Good: pure. No key, no record, no registry, no cleanup, no concurrency.
- Bad: recompiles per press, and says nothing about what production runs.

### Option 2 — evaluate the deployed version
- Good: it is exactly what a process will see.
- Bad: it cannot answer the question before a deploy, so the author still has to
  ship something to try it — which is the problem.
- Bad: it needs the registry and a key, which drags a read-only "try it" onto the
  same path a deployment uses.

### Option 3 — a playground session
- Good: consistent with the BPMN playground.
- Bad: a playground exists because a *process* has state — tokens, timers, jobs,
  a clock to advance. A decision has none: it is inputs in, outputs out. A session
  would be a lifecycle wrapped around a pure function, with an open/close to get
  wrong and sandboxes to reap.

### Option 4 — reuse the FEEL endpoint
- Good: nothing new to build.
- Bad: it evaluates one expression. A decision table is a hit policy over a set of
  rules, and the thing an author gets wrong is which rule matched — precisely what
  a per-expression evaluation cannot show.

## Links

- extends [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) — temis compiles and evaluates; this adds no engine
- extends [ADR-0066](0066-decision-evaluation-records.md) — the trace this shows
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor this panel is in
- relates to [ADR-0039](0039-dmn-io-variable-mappings.md) — how a decision's inputs are derived
- relates to [ADR-0215](0215-modeler-playground.md) — the process counterpart, and why a decision needs no session
