# ADR-0327: A deployed decision satisfies a latest-bound business rule task

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

Deploying a BPMN diagram whose business rule task names a decision runs a
preflight: the decision has to be provided by a **stored model behind a DMN
reference**, or the deploy is refused with

> this diagram's business rule task(s) reference decision(s) [eligibility] that no
> DMN model provides — create the decision (or add its reference) in Atlas, then
> deploy

The refusal exists for a good reason, written into the code that performs it: *"a
model-less business-rule deploy is the trap that started this: its tasks create
DMN jobs that can never evaluate, and because every deploy drives all pending
jobs, one such job then fails every future deploy."*

[ADR-0319](0319-durable-versioned-decision-deployments.md) then made a decision a
runtime artifact of its own, and [ADR-0322](0322-deploying-one-decision.md) gave it
a Deploy button. A decision can now be **deployed without being in the model** —
the record carries its own XML — and ADR-0322 recorded the consequence as an
accepted trap: such a decision "cannot be named by a business rule task, because
the picker lists what references resolve".

**That sentence is wrong, and measured.** With one decision deployed on its own and
no reference anywhere:

| surface | what happens |
|---|---|
| the business-rule-task **picker** | offers `eligibility`, from the deployed catalogue, with an empty model handle |
| deploying a **`latest`-bound** process that names it | **409** — "no DMN model provides" |
| deploying a **`deployment`-bound** process that names it | 409 — "no DMN model provides" |

The picker has offered deployed decisions since ADR-0050: `GET /api/v1/decisions`
adds "the decisions of *deployed* models — those the engine can actually evaluate —
so an author can pick a decision that is deployed even when no DMN reference
artifact exists for it". So the Modeler invites the author to pick exactly the
thing the deploy then rejects.

And the third row is not like the second. The question this record answers:
**which of those two refusals is protecting anything?**

## Decision drivers

- **The refusal's own premise.** It exists to stop a job that *can never evaluate*.
  Where the job can evaluate, the premise is false and the refusal is a defect.
- **The bindings are not the same thing** (ADR-0063). `latest` resolves at deploy
  time to the newest decision deployment (ADR-0319); `deployment` evaluates the
  model *bundled with this process's own deployment*.
- **Do not weaken the guard.** A model-less deploy that really cannot evaluate must
  still be refused, and the message must still say what to do.
- **Deploy-time resolution stays deploy-time.** Nothing here may push a decision
  back to being chosen at task activation (I5/I6, ADR-0319).

## Considered options

1. **Make the preflight binding-aware**: a `latest`-bound task is satisfied by a
   reference *or* by an existing decision deployment; a `deployment`-bound task
   still needs a reference.
2. **Accept any deployed decision, for either binding.**
3. **Narrow the picker instead** — stop offering deployed decisions, so the two
   surfaces agree by offering less.
4. **Leave it** — ADR-0322's accepted trap.

## Decision outcome

Chosen option: **1 — the preflight becomes binding-aware.**

A local business rule task's decision must be provided by at least one of:

- a stored model behind a DMN reference — which is what gets **bundled** with the
  deployment; or
- **for a `latest`-bound task only**: a decision deployment already in the registry.

The second case needs nothing bundled, because `pinDecisions` resolves that
reference to the decision deployment's own key at deploy time and stores the
answer. The model that would have been bundled is never consulted.

### Why the two bindings differ, exactly

`pinDecisions` (ADR-0319) resolves a latest-bound reference in two steps:

1. the newest decision deployment providing that id, if there is one;
2. otherwise **this process deployment's own key** — the model bundled with it.

So a latest-bound task covered by case 1 never touches the bundle; one that falls
through to case 2 needs the bundle to exist, which is exactly what the reference
provides. A `deployment`-bound task has no step 1 at all: it evaluates the model
registered under the process's own key, so with nothing bundled it is the original
trap, unchanged. **It stays refused, and it should.**

That is why option 2 is wrong rather than merely generous: it would let a
`deployment`-bound task deploy with nothing under its own key, producing precisely
the jobs-that-can-never-evaluate this guard was written for.

### What the refusal says now

The message names only the decisions that are genuinely uncoverable, and — when a
decision *is* deployed but the task binds `deployment` — says that, because
"no DMN model provides it" would be false and unhelpful:

> business rule task(s) bound to `deployment` reference decision(s) [eligibility]
> that no DMN model provides. A deployed decision cannot satisfy `deployment`
> binding, which evaluates the model bundled with this process — add the
> decision's reference, or bind the task to `latest`.

### This supersedes ADR-0322's accepted trap

ADR-0322 recorded "a decision deployed but not in the model cannot be named by a
business rule task" as a cost it accepted. Two things were wrong with it: the
picker already offered such a decision, and nothing about `latest` binding needed
a model. The editor's toast on deploying a model-less decision is narrowed to what
is still true — such a decision cannot be used by a `deployment`-bound task, and
is not offered inside a project's own scope.

### Consequences

- **Positive:** The Modeler stops offering what the deploy refuses. A decision
  deployed from its editor is usable by a process, which is what deploying it was
  for.
- **Positive:** The guard keeps doing its job where it has one. The measured
  `deployment`-bound refusal is unchanged, and now explains itself.
- **Positive:** An application made of decisions can be shipped, and a process in
  *another* application can bind to those decisions with `latest` — which is the
  cross-application case ADR-0319's registry was built for and the preflight was
  silently blocking.
- **Negative / trade-offs accepted:** A latest-bound task can now deploy with
  nothing bundled for it, so the decision deployment it pins has to outlive the
  process. It does: **nothing deletes a decision deployment.** There is no route in
  the HTTP surface, none in the MCP adapter, no control in the Console, and
  deleting the application explicitly leaves deployed definitions alone. What this
  record changes is that the store being append-only stops being merely convenient
  and becomes load-bearing.
- **Negative:** The preflight now reads run-loop-owned registry state, so the set
  of deployed decision ids is read on the loop and passed in, next to the reference
  records. One more parameter on a function that already takes one.
- **Follow-ups / risks to watch:** The day a decision deployment becomes
  deletable, a definition pinned to it has to be a reason to refuse — the way a
  running instance already refuses `DELETE /api/v1/processes/{key}`. It is worth
  writing that rule down *before* the delete route exists rather than after,
  because a deployed process carries no fallback for a latest-bound task that
  resolved away from its own bundle. Deleting the **reference**, which is
  reachable and confirmed with a generic prompt, moves the other way: a
  latest-bound task no longer needed it before this deploy and does not need it
  after, so the reference is less load-bearing than it was, not more. It still
  decides whether a `deployment`-bound task can be re-deployed, and the prompt
  does not say so — a warning that named the affected deployed processes would be
  an improvement, and belongs to whoever owns that dialog.

## Pros and cons of the options

### Option 1 — binding-aware preflight *(chosen)*
- Good: refuses exactly what cannot run, and nothing else.
- Good: the two bindings already mean different things; the guard now says so.
- Bad: the preflight needs one more input, read on the run loop.

### Option 2 — accept any deployed decision
- Good: one rule, no binding to reason about.
- Bad: it re-opens the original trap for `deployment`-bound tasks, which evaluate
  the bundled model and would have none.

### Option 3 — narrow the picker
- Good: the surfaces agree.
- Bad: they agree on the wrong answer. A deployed decision *is* evaluable; hiding
  it makes ADR-0322's Deploy button pointless for anything but the author's own
  eyes.

### Option 4 — leave it
- Good: nothing to change.
- Bad: the product offers a choice it then refuses, with a message telling the
  author to do something they have already done.

## Links

- supersedes a consequence of [ADR-0322](0322-deploying-one-decision.md) — the trap it accepted is not one
- extends [ADR-0319](0319-durable-versioned-decision-deployments.md) — the deploy-time pinning this reads
- relates to [ADR-0063](0063-dmn-decision-binding.md) — the two bindings this distinguishes
- relates to [ADR-0050](0050-temis-decision-connector.md) — the picker that already offers deployed decisions
- relates to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) — the model a deployment bundles
