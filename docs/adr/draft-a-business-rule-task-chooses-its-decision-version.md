# ADR-DRAFT: A business rule task follows the newest decision when it runs, or the version it names

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-28
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0319](0319-durable-versioned-decision-deployments.md) made a decision a
deployment artifact of its own and moved `latest` from task activation to process
deployment: a process deployed while `Decision 1` stood at v1 evaluates v1 for as
long as it exists, whatever is deployed afterwards.

That is what happened on the running instance on 2026-09-28, and it is not what the
author expected. `Entscheidungs Demployment Test` v2 was deployed while
`Decision 1` v1 was the only version; v2 and v3 were deployed from the decision
editor afterwards; the process was not redeployed. An instance started after v3
evaluated v1 — hit policy FIRST, no rule for 3 — and answered `andere zahl` where v3
answers `drei`. Nothing on any surface said which version had run.

Four things came together there, and each is a defect on its own:

1. **The Modeler says the opposite of what the engine does.** The binding field
   reads *Latest — newest deployed version* and its help *Latest evaluates the newest
   deployed version*. ADR-0319 listed "the Modeler's binding help has to say so" as a
   consequence; it never did.
2. **No surface names the version that answered.** The evaluation record
   (`DecisionEvaluationValue`) carries the decision id, inputs, outputs and trace,
   and no deployment key or version. Operations shows the table that ran and cannot
   say which version it was.
3. **A fixed version cannot be chosen.** ADR-0319 deferred `versionTag`. `deployment`
   binds to the copy of the model bundled with the process, which is not a version
   the author picked.
4. **`bindingType="versionTag"` is read as `latest`, silently** (`decisionBinding`
   in `compiler/parse.go`), and the Modeler rewrites it to `latest` on save. A model
   written for Camunda that pins a tag runs whatever is newest.

The owner of the product has decided what `latest` means (2026-09-28): the newest
deployed version *when the task runs*, as in Camunda; and that a fixed version is
chosen by its number from the versions that are deployed. This record says how.

## Decision drivers

- **The engine does what the Modeler says.** One meaning of `latest`, in the
  engine and in the panel that sets it.
- **Nothing is re-decided on replay** (I6). Whatever version answered must be a fact
  in the log, not something recomputed.
- **Nothing on disk changes meaning silently.** A definition deployed under
  ADR-0319 was deployed with a frozen version; it keeps it.
- **A version that answered is visible** on the evaluation it answered.

## Re-reading ADR-0319's second driver

ADR-0319 gave two reasons for resolving at deploy time. The first — a deployed
process should not change behaviour because something else was deployed — is a
product choice, and the product owner has now made the other one; the fixed-version
binding below keeps it available to anyone who wants it.

The second was a replay hazard: "a decision version selected on the worker at
activation time is a routing decision made outside the log". On reading the worker
it does not hold. A business rule task is a job; `dmn.Handler` evaluates it off the
run loop and returns a `job.Completion` carrying the outputs and the evaluation
record, which `Submit` turns into a command and the log records. Replay applies that
completion; it does not evaluate the decision again. A version chosen when the job is
worked is therefore the same kind of fact as a service task's HTTP answer — decided
once, outside the log, and recorded in it. What was missing is only that the record
did not say *which* version answered; this record adds that.

## Considered options

1. **Keep ADR-0319; fix the help text; add a version chip.** Rejected by the product
   owner: it keeps the behaviour that surprised the author and only describes it.
2. **`latest` resolves when the task's job is worked; the evaluation records the
   version; a fixed version is an explicit binding (chosen).**
3. **Re-pin every definition at each decision deploy.** Rewrites deployment records
   as a side effect of an unrelated deploy, and is option 2 with extra steps and a
   larger blast radius.

## Decision outcome

Chosen option: **option 2**.

### `latest` is resolved when the job is worked

A deployment written from here on carries `bindingPolicy: "runtime"`. Its
latest-bound tasks evaluate the **newest decision deployment** of the decision id at
the moment the job is worked — `latestDecision`, the index only decision deployments
write — and, when no decision deployment provides the id, the model bundled with the
process, as ADR-0319's rule 2 already does. The legacy "newest registered model of
either kind" pointer is not used: a bundle must not become a version of a decision
by being deployed with a process.

Records already on disk keep what they were deployed with:

| `bindingPolicy` | `latest` resolves to | why |
|---|---|---|
| `"runtime"` (new) | newest decision deployment when the job is worked, else the bundle | this record |
| `"pinned"` | the key frozen at deploy | ADR-0319; redeploying moves it to `runtime` |
| absent | newest registered model at activation | ADR-0063; unchanged |

### The evaluation records the version that answered

`DecisionEvaluationValue` gains one appended field, `DecisionKey`: the deployment
whose model answered — a decision deployment, or the process's own key for a bundled
model. A record written before it ends after `TraceJSON` and reads it as zero, "not
recorded" — the appended-field pattern the value file uses throughout. The version is
not stored a second time: the decision deployment record already says which version
of which decision it holds, so the API reads the key back as `decisionKey` and
`decisionVersion`, and Operations shows `v3` where it is known and nothing where it is
not. The requirements graph of an evaluation is drawn from the model that answered,
not from whatever the process key resolves to today.

### A fixed version is an explicit binding

A task that names a version carries it on `zeebe:calledDecision` as
`atlas:version="3"`, beside a `bindingType` of `latest` (or none). It is resolved **at
deploy time**, before anything is written, to the decision deployment holding version
3 of that id — a version that is not deployed refuses the whole model, naming the
decision and the versions that exist — and stored in the deployment record as a
binding with a version. The worker evaluates exactly that key and never another: a
binding it cannot resolve fails the job. The deletion guard that protects a pinned
decision deployment protects this one too.

The attribute is Atlas's own. A Camunda engine ignores it and runs `latest`; the
Modeler says so next to the field.

### `versionTag` is refused rather than guessed

Until a tag can be set on a decision version, a model binding `versionTag` is refused
at deploy with a message naming the task. The Modeler keeps an unknown
`bindingType` as it found it instead of rewriting it to `latest`. A definition
already deployed with one keeps loading (ADR-0177) and keeps its current behaviour.

### The Modeler says what happens

The binding field offers *Latest — newest version when the task runs*, *Version —
a deployed version you choose*, with a list of the deployed versions of the chosen
decision, and *Deployment — the model deployed with this process*. The help text says
what each does, and that Camunda ignores a chosen version.

### Deleting the last version

Under the runtime policy a delete changes what latest resolves to only when it removes
a decision's last deployment — deleting the current version while older ones survive
is already refused ([ADR-0336](0336-cleaning-up-the-decision-store.md)). Such a delete is refused as well when a definition
evaluates the decision as latest and carries no copy of it, because its task would
have nothing to evaluate.

### Prerequisite: the registry is read while it is written

`dmn.Registry` documented itself as safe once populated. It is populated at runtime —
every decision deploy writes its maps on the run loop — while job handlers evaluate
off it (`Runner.Work` runs outside `s.do`). A race-detector probe of
`EvaluateTraced` against `DeployDecision` reported a data race on
`Registry.definitions`, before any of this record; a Go map read during a write can
also end the process. Resolving `latest` when the job is worked adds reads of
`latestDecision` on the same path. The registry's indexes are now behind a read–write
lock; a compiled model is immutable, so the lock covers the lookup and not the
evaluation.

### Consequences

- **Positive:** the engine does what the Modeler says; a decision owner changes a
  rule and the next task runs it; the version that answered is on the record; a
  fixed version is a choice an author can make and see.
- **Negative / trade-offs accepted:** Atlas's `latest` is Camunda's again, so a
  process's behaviour now changes when a decision is deployed — that is the point,
  and the fixed-version binding is the way out of it. Three binding policies exist
  on disk where two did. `atlas:version` is not portable.
- **Follow-ups / risks to watch:** a definition pinned under ADR-0319 needs a
  redeploy to follow new versions, and nothing tells its owner; the process document
  still shows the newest deployed version for every business rule task, which is
  wrong for one bound to a version; setting a tag on a decision version would make
  `versionTag` supportable.

## Links

- supersedes the `latest` semantics of
  [ADR-0319](0319-durable-versioned-decision-deployments.md); its decision
  deployments, registry split and release manifest stand
- relates to [ADR-0063](0063-dmn-decision-binding.md) — the binding surface
- relates to [ADR-0066](0066-decision-evaluation-records.md) — the evaluation record
- relates to [ADR-0177](0177-reload-skips-the-deploy-gate.md) — reload does not re-gate
- relates to [ADR-0336](0336-cleaning-up-the-decision-store.md) — the guards on deleting a decision deployment
