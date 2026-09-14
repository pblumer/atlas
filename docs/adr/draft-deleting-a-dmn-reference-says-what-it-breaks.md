# ADR-DRAFT: Deleting a DMN reference says what it would break

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

A DMN reference (ADR-0034) is what makes a stored model an artifact: it files the
model under an application, gives it a name, and puts its decisions in the
business-rule-task picker. Deleting one is a single menu action behind one
sentence:

> Delete this DMN reference? The temis model itself is not affected.

Both halves of that sentence are true and neither is the thing the reader needs.
What the reference decides is not the file on disk; it is whether anything can
still be **deployed** against the decisions that model provides:

- a **`deployment`**-bound business rule task needs a model to bundle with its own
  process (ADR-0063), and the reference is where that model comes from. Delete the
  last reference providing that decision and the process can never be deployed
  again until somebody puts one back;
- a **`latest`**-bound task is satisfied by a decision deployment where one exists
  ([ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md)), and
  otherwise falls back to the bundle — so it is blocked only when nothing is
  deployed for that decision.

Nothing running is affected either way: a decision deployment and a process
deployment both carry their own XML, so the registry is untouched by a reference
disappearing. The damage is entirely in the future tense, which is exactly why it
is not noticed: the deploy that fails is the one somebody attempts next week, and
the message it fails with ("no DMN model provides it") does not mention that
anybody deleted anything.

The question: **what should the confirm say, given that the server can compute the
answer exactly?**

## Decision drivers

- **The refusal already exists and is precise.** The deploy preflight knows exactly
  which decisions are uncoverable and why. The same computation, run before the
  deletion instead of long after it, is a warning rather than a post-mortem.
- **A generic confirm trains people to click through it.** "Are you sure?" carries
  no information, so it is answered without reading. A sentence naming two
  processes is read.
- **Do not over-warn.** A decision another reference also provides, or one a
  deployment already covers for a `latest`-bound task, is not at risk. Listing it
  would make the warning noise.
- **Sharing scopes are not a leak channel** (ADR-0071). The impact is computed over
  everything, but a draft in an application the caller cannot see must not be named
  to them.
- **Nothing is blocked.** This is a warning, not a new refusal: the reference is the
  author's to delete.

## Considered options

1. **Compute the impact and name it in the confirm** — which decisions lose their
   last model, and which deployed definitions and drafts could then not be
   deployed.
2. **Refuse the deletion while anything would be blocked**, the way a running
   instance refuses deleting a process.
3. **Warn generically** — "processes may reference this; deleting it can break
   future deploys" — with no computation.
4. **Leave it.**

## Decision outcome

Chosen option: **1 — the confirm names what it would break.**

`GET /api/v1/dmnrefs/{id}/impact` answers, for one reference:

- `decisions` — the decision ids its model provides;
- `exclusive` — those of them **no other reference provides**, which are the ones
  that lose their last model source;
- `blocked` — the deployed definitions and drafts with a business rule task naming
  an exclusive decision that could then not be deployed, each with its binding;
- `blockedHidden` — how many further artifacts are affected in applications the
  caller cannot see.

The Console composes the confirm from it. Where nothing is at risk the sentence
stays short; where something is, it reads like the refusal that would otherwise
have arrived later, before the act rather than after it.

### What counts as blocked, exactly

Per artifact and per task, against the decision ids in `exclusive`:

| binding | a decision deployment exists | blocked |
|---|---|---|
| `deployment` | — | **yes**, always: it evaluates the model bundled with its own process, and there would be none |
| `latest` | yes | no — the deploy pins that deployment and never reads the bundle (ADR-0327) |
| `latest` | no | **yes**: it would fall back to a bundle that no longer exists |

That is the deploy preflight's own rule, read forwards. It is deliberately the same
condition rather than a second approximation of it: a warning that disagreed with
the refusal it predicts would be worse than none.

### Why it warns rather than refuses

Option 2 is the shape used for `DELETE /api/v1/processes/{key}`, and the analogy
does not hold. A running instance is state that *would be destroyed*; a future
deploy is work that has not happened. Refusing would mean an author cannot tidy up
a reference because a draft they have never seen names its decision — and the
remedy (deploy the decision, or rebind the task) is not theirs to perform. The
author keeps the decision; the product stops making it blind.

### Drafts the caller cannot see are counted, not named

Deployed definitions are engine-wide runtime state and are named freely, the way
`GET /api/v1/processes` already lists them to any signed-in identity. A **draft**
belongs to an application's sharing scope, so one the caller cannot view is
counted in `blockedHidden` and nothing else about it is disclosed — not its id, its
name, or which application it is in.

Counting rather than omitting is the deliberate half. A warning that silently
understated the damage because of who was looking would be a warning that lies to
exactly the person about to act.

### Cost

Deployed definitions are already compiled, so reading their business rule tasks is
a walk over the registry on the run loop — the same walk
`handleDeployedDecisions` already does. Drafts are not compiled, and compiling
every draft in the system for one dialog would be wasteful, so a draft is compiled
only when its XML mentions one of the exclusive decision ids as a substring. The
prefilter can admit a draft that turns out not to name the decision; it can never
skip one that does, because a task naming a decision has that id in its XML.

### Consequences

- **Positive:** The one destructive action on a reference stops being blind. The
  message that would have arrived at the next deploy arrives before the deletion.
- **Positive:** One rule, two places. The warning is the preflight's condition,
  so the two cannot drift into contradicting each other.
- **Positive:** It is also, incidentally, the answer to "what uses this reference",
  which nothing answered before.
- **Negative / trade-offs accepted:** The confirm can take a moment now, because it
  waits for a request. It fails open: an impact that cannot be fetched falls back
  to the sentence that was there before, rather than blocking the deletion.
- **Negative:** The prefilter makes the draft scan approximate in the harmless
  direction only, and that asymmetry has to stay true if either side changes.
- **Follow-ups / risks to watch:** The same computation would serve a "what uses
  this decision" view in the Modeler, which is a question authors ask without a
  deletion in mind. Not built here, because a dialog is not a feature.

## Pros and cons of the options

### Option 1 — name the impact *(chosen)*
- Good: the warning is specific, and specific warnings are read.
- Good: reuses the preflight's condition rather than approximating it.
- Bad: a request behind a confirm, and a scope rule to get right.

### Option 2 — refuse while anything is blocked
- Good: nothing can be broken by a deletion.
- Bad: it makes one author's tidying hostage to a draft they cannot see and cannot
  fix.
- Bad: the analogy to a running instance is false — nothing is destroyed, only a
  future act made harder.

### Option 3 — warn generically
- Good: no server work.
- Bad: it is the sentence that is already there, lengthened. A warning that cannot
  say whether anything is actually at risk is answered by clicking through it.

### Option 4 — leave it
- Good: nothing to change.
- Bad: the failure surfaces at the next deploy, in a message that does not mention
  the deletion that caused it.

## Links

- extends [ADR-0034](0034-projects-and-artifacts.md) — the reference this deletes
- extends [ADR-0327](0327-a-deployed-decision-satisfies-a-latest-bound-task.md) — the binding rule the warning reads forwards
- relates to [ADR-0063](0063-dmn-decision-binding.md) — the two bindings it distinguishes
- relates to [ADR-0071](0071-sharing-scopes.md) — why a draft elsewhere is counted and not named
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the deployments that keep a latest-bound task safe
