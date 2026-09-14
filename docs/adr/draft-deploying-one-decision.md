# ADR-DRAFT: One decision can be deployed on its own, from the editor

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0319](0319-durable-versioned-decision-deployments.md) made a decision a
durable, versioned runtime artifact, and
[ADR-0321](0321-decision-drafts.md) gave it the two authoring
rungs below that: a draft nobody else resolves, and the model everything resolves.

The third rung has only one door. A decision reaches the runtime through the
application's **Publish** (ADR-0128), which deploys *everything* the application
holds — every BPMN draft and every other decision in it. There is no way to deploy
one decision, although there has always been a way to deploy one diagram
(`POST /api/v1/deployments`, the BPMN editor's **Deploy**).

That asymmetry is the whole of the problem, and it is the one the single-diagram
deploy was built to solve. An author who has just finished a decision and wants to
see it evaluate has to publish the application around it: other people's drafts
ship, other decisions get a new version, and the release manifest records a release
nobody asked for. For a diagram, pressing Deploy in the editor avoids exactly that.
The decision editor's bar reads `Save · Save to model` and stops, one rung short of
what its BPMN sibling offers.

The question: **what does Deploy mean for a decision, next to the two save verbs it
already has?**

## Decision drivers

- **The counterpart, not a new mechanism.** #919 asks for "the counterpart of the
  single-diagram Deploy … going through the same durable path ADR-0319
  established (no UI-to-engine shortcut)". A second way to make a decision runnable
  would be a second thing to keep correct across restarts.
- **One Modeler, one grammar.** The BPMN editor's Deploy takes what is on screen
  and ships that single diagram; its tooltip points at Publish for the whole
  application. A decision's Deploy should read the same way round.
- **Durable before visible (I2).** A decision must not be evaluable before its
  record is on disk, or a restart forgets something that already answered a call.
- **Single writer (I3).** Key allocation, the per-decision version counter, the
  record write and the registry registration are all run-loop state.
- **Do not reopen ADR-0319.** The storage model, the record and the binding
  semantics are out of scope for #919. This adds a caller to that path, not a
  variant of it.

## Considered options

1. **A Deploy that takes what is on screen**, like the BPMN editor's — the model is
   not written, and Save to model stays the separate act it is.
2. **A Deploy that first writes the model and then deploys it** — one button
   instead of two, at the cost of conflating the middle rung with the top one.
3. **A Deploy that refuses until the decision is in the model** — deploy only what
   a reference resolves, so every deployment has a model behind it.
4. **No single deploy: make Publish cheaper instead** — for example an application
   publish that skips unchanged artifacts.

## Decision outcome

Chosen option: **1 — Deploy takes what is on screen, through `deployDecisions`.**

`POST /api/v1/decision-deployments` accepts the DMN XML in its body, the way
`POST /api/v1/deployments` accepts BPMN, and:

- compiles and validates it **off** the run loop, as the bundle's preflight does;
- then, in **one run-loop turn**, calls the same `deployDecisions` the application
  publish calls: a key from the global definition key space, a version per decision
  id, the durable record written **before** the registry is touched.

It is literally the same function, with a one-element slice. There is no second
path to keep correct, and a decision deployed from the editor is indistinguishable
from one deployed by a publish — same record, same listing, same recovery.

Because `deployDecisions` registers the model, the deployment also becomes what a
**`latest`** reference pins to for its decision ids the next time a process is
deployed (ADR-0319). That is the point of deploying it, and it is the same effect a
publish would have had.

### Deploy does not write the model

The BPMN editor's Deploy does not write the draft, and this one does not write the
model. The two verbs answer different questions, and collapsing them would undo
what [ADR-0321](0321-decision-drafts.md) has just separated:

| | what it changes | who sees it |
|---|---|---|
| **Save** | the draft | you |
| **Save to model** | `dmn-models/<handle>.dmn` | every reference, every picker, the next Publish |
| **Deploy** | a decision deployment | the engine, now |

Option 2 was tempting — two buttons instead of three — and is rejected because the
middle rung is not a step on the way to the top one. A decision can legitimately be
in the model without being deployed (an application that is not published yet), and
legitimately deployed without the model moving (a fix tried on the engine before it
is offered to every reference). One button cannot express both.

### A decision that is not in the model can still be deployed

ADR-0319's record carries its own XML — that is what makes it survive the model
file changing underneath it — so a deployment does not *need* a model behind it.
Option 3 would add a rule the record does not require, and would break the
correspondence with the BPMN editor, where a diagram that was never saved as a
draft deploys perfectly well.

So the record's `modelRef` is simply empty in that case, rather than claiming a
model that does not exist, and its resource name is derived from the decision's own
id. `decisionResourceName` is widened by exactly that fallback; nothing else about
the record changes.

The honest cost is a trap, and the editor names it rather than leaving it to be
discovered: a decision that is deployed but not in the model **cannot be named by a
business rule task**, because the picker lists what references resolve. The toast
says so on that deploy. It is the same shape as a BPMN definition deployed from a
diagram that has no draft — visible in Operations, absent from the Modeler's list —
which Atlas has always allowed.

### The editor says what is deployed

The bar carries a chip with the current deployed version of the decision being
edited, read from `GET /api/v1/decision-deployments?decisionId=…`, the way a
deployed diagram shows its key. It is the answer to "is what I am looking at what
is running", which until now required leaving for Operations. It is keyed by the
**decision id**, not by the reference or the handle, because that is what the
runtime versions: renaming a decision genuinely changes which deployment the
question is about, and the chip follows it.

That listing was `operator`-only. Roles are flat (ADR-0209) — a modeler is not an
operator — so the chip would have been invisible to exactly the people the editor
is for. It is widened to `any` signed-in identity, which is what its BPMN
counterpart `GET /api/v1/processes` already is, and it exposes the same class of
fact: a key, a version, when and by whom something was deployed. The deployed DMN
source (`…/{key}/xml`) stays `operator`.

The bar's three buttons take the BPMN editor's grammar exactly: neutral for what
you keep (`Save`, `Save to model`), one primary button for the act that leaves the
browser (`Deploy`). `Save to model` therefore stops being the primary button it was
while it was the last verb on the bar.

### Consequences

- **Positive:** The decision editor's bar is complete: keep it, share it, ship it.
  Trying a decision on the engine no longer means publishing an application around
  it.
- **Positive:** One durable path. The single deploy cannot drift from the publish,
  because it *is* the publish's own function.
- **Positive:** A decision deployed alone still takes the `latest` pointer, so the
  next process deploy binds to it exactly as it would after a publish.
- **Negative / trade-offs accepted:** Three verbs on one bar. The ADR above already
  accepted two; this is the third, and it is the one the BPMN editor also has.
- **Negative:** A decision can now be deployed that no business rule task can name.
  The toast says so, and Save to model is one button away, but the state exists.
- **Negative:** A deploy from the editor mints a version without a release. The
  application's release manifest (ADR-0128) therefore no longer lists every
  deployed decision version — it lists every *published* one, which it always did,
  but the gap is now reachable from the Modeler rather than only from the API.
- **Follow-ups / risks to watch:** No "Deploy & run" counterpart, because a
  decision is not started — it is evaluated. The panel that evaluates a decision
  against sample inputs is Phase 4 of #919 and is where that belongs.

## Pros and cons of the options

### Option 1 — Deploy what is on screen *(chosen)*
- Good: the same verb, in the same place, meaning the same thing as in the BPMN
  editor.
- Good: reuses `deployDecisions` exactly, so I2 and I3 hold by construction.
- Bad: a third button, and a deployment whose model may differ from what the
  picker offers.

### Option 2 — Deploy writes the model, then deploys
- Good: two buttons instead of three; nothing can be deployed that references
  cannot resolve.
- Bad: it re-conflates what the draft record just separated — the author loses the
  ability to try something on the engine without changing what every reference
  resolves.
- Bad: it diverges from the BPMN editor, where Deploy leaves the draft alone; the
  parity #919 asks for would be broken by the very button meant to complete it.

### Option 3 — refuse until the decision is in the model
- Good: every deployment traces back to a model; `modelRef` is never empty.
- Bad: the record carries its own XML, so the rule buys provenance and nothing
  else — and costs the "try it now" case that motivates a single deploy.
- Bad: a diagram never saved as a draft deploys; a decision never saved to the
  model would not. Two artifacts, two rules, for no reason a reader could state.

### Option 4 — make Publish cheaper instead
- Good: no new endpoint; one way to ship.
- Bad: it does not answer the question. Publishing is an application-level act
  with a release and a manifest; making it skip unchanged artifacts would not make
  it the right act for "I have just written this decision, does it work".
- Bad: a release minted for one decision is a release nobody asked for, whatever
  it skips.

## Links

- extends [ADR-0319](0319-durable-versioned-decision-deployments.md) — the durable path this deploy goes through
- extends [ADR-0321](0321-decision-drafts.md) — the two rungs below this one
- relates to [ADR-0320](0320-the-decision-editor-is-a-page.md) — the editor this button is on
- relates to [ADR-0128](0128-process-applications.md) — the application Publish this is not
- relates to [ADR-0002](0002-single-writer-partition-model.md) — the turn the write happens in
- relates to [ADR-0071](0071-sharing-scopes.md) — the application scope a deploy is authorized against
