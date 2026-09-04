# ADR-0233: Finish ADR-0164 — in-process connector work becomes a finite list, then nothing

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0164](0164-no-in-process-service-tasks.md) decided the rule — *every
side-effecting service task belongs on a worker; the engine runs the engine* — and
then chose **deprecation over a ban**, for one stated reason:

> **What is still owed**, and it is the honest gap: a connector task cannot run on a
> worker yet. […] Closing it means the connector's task detail travelling *with the
> job* […] Until that lands, this record states the direction and the guidance; only
> a model-authored job type can actually be relocated today.

That gap is closed. [ADR-0168](0168-connector-work-on-a-worker.md) made the resolved
task detail travel with the job, and the worker halves have landed kind by kind since
— mail, CSV, script, web scraping, Active Directory, Remedy, Jira, Entra, the three
SQL products. ADR-0164 names this record's occasion itself: *"It becomes available
once the detail travels with the job, and this ADR is where to revisit it."*

What the deprecation left behind is a state with no end and no mechanism:

- **A fresh install still runs integrations on the loop.** `rest` is the plainest
  case — an HTTP call to a host somebody else operates, the original argument for
  ADR-0164 — and it is in-engine by default on every Atlas started today.
- **Nothing distinguishes "not moved yet" from "will never move".** Both are
  "in-process": a kind whose worker half is a week of work reads exactly like the
  user-provisioning task, which has no out-of-process form at all and correctly never
  will. An operator reading the Workers view cannot tell which is which, and neither
  can the next contributor.
- **A deprecation with no list does not converge.** Six kinds are in-engine today for
  no reason anyone has stated recently; they stay there until somebody notices.

The observation that prompted this: on a running installation the in-process types are
`rest`, `clio.write/query/read`, `sharepoint.createitem`, `scim`, `ldap`, `soap`,
`ldif`, `temis.decision`, plus `dmn` and `user.provision`. Two of those ten need only
a default flipped. Six need a worker half that does not exist. Two belong in the
engine and always did. That mixture, presented as one undifferentiated "in-process",
is the actual problem.

## Decision drivers

- **The engine's worst case must not depend on a third party.** Unchanged from
  ADR-0164; this record is about finishing it, not re-deciding it.
- **No running deployment may break.** Every kind must keep working across the change,
  including for an operator who deliberately runs one in-engine today.
- **The gap must be finite and visible.** "Not yet moved" has to be an enumerated list
  that shrinks, not a permanent condition that looks the same as a deliberate
  exception.
- **Deterministic in-engine work stays.** FEEL, local DMN, timers, the mockup task and
  the user store are not integrations; ADR-0164's own carve-out is correct and stays.
- **The default is the decision.** What a fresh install does is what the architecture
  actually is; guidance nobody configures is not an architecture.

## Considered options

### Option 1 — leave ADR-0164 as it is, and move kinds when someone gets to them

No new record, no list. This is the status quo, and the status quo is what produced a
default that runs REST calls on the engine's loop two years into a decision that says
it must not. Rejected.

### Option 2 — refuse in-process execution now, for every kind

Delete the in-process handlers and `--in-process-connectors` outright. Honest and
short, and it breaks every installation using clio, SharePoint, SCIM, LDAP, SOAP or
temis, none of which *can* run on a worker today. Rejected — a ban that removes the
only available execution path is not a migration.

### Option 3 — close the gap in two halves: default what can move, enumerate what cannot (chosen)

Flip the default for every kind whose worker half exists (now: `rest`, `ldif`).
Record the kinds without one as a closed, named list — the migration's own to-do —
each with a slice that builds `Resolve → payload arm → worker runner`. Keep
`--in-process-connectors` as a deprecated escape hatch that says so at startup, and
refuse it once the list is empty.

## Decision outcome

Chosen: **option 3.**

### The rule, restated as a rule

A side-effecting service task runs on a worker. What runs in the engine is exactly
this list, and it is closed — adding to it needs a record of its own:

| Stays in the engine | Because |
|---|---|
| FEEL script task, gateway conditions, I/O mappings | Pure, deterministic, compiled at deploy; replayed from durable facts, never re-executed |
| Local DMN evaluation | CPU-bounded library code, no network (ADR-0164 names it) |
| Mockup task | A timer, not a call (ADR-0120) |
| Timers, user tasks, message correlation | Engine semantics; a user task waits for a person and creates no worker job |
| User provisioning | Mutates the run-loop-owned user store; there is no endpoint for a worker to reach and no credential for it to hold (ADR-0123, already recorded in `engineOnlyJobTypes`) |

Everything else that reaches another system belongs on a worker.

### What moves now

`rest`, `ldif` and `clio` join `DefaultOffloadedKinds`. LDIF needs nothing to make that work —
it reads and writes a file, and a supervised worker is a child process on the same
host. REST needed the same thing Active Directory needed (ADR-0182): its endpoint is
in the model and travels with the job, but its `authSecret` is a *vault reference*, and
a reference is resolved where it is used. `restWorkerEnv` renders exactly the
references the deployed models name, resolved through the vault, into the child's
environment under the `ATLAS_CONNECTOR_<REF>_TOKEN` names the worker already reads.
Only what is deployed is handed over — the running models' secrets, not the vault.

clio needed Remedy's answer rather than AD's: its endpoint is a *connector record* and
its token a vault reference behind that record, so `clioWorkerEnv` renders the stores
the engine has configured. One difference is deliberate — a store with no token is
still handed over, because clio can be reached without one and dropping it would leave
a working instance unserved, where Remedy without a password is simply not configured.
A clio write also carries something no other kind does: the event *body*, which is the
task's input mappings or the variables it sees (ADR-0174). That is engine state and
nothing else could reconstruct it, so it travels resolved, in the payload, beside the
idempotency key that de-duplicates the retry.

### What is still in the engine, and is now a list

*Empty.* Every kind that reaches another system now has a worker half; the slices
landed in the order of their difficulty: `rest`/`ldif`, `clio`, `ldap`, `soap`,
`sharepoint`, `scim`, and finally `temis`, which needed the engine↔worker completion
contract widened before it could move at all.

Six of the seven took the same shape: resolve the authored detail against the
instance's variables in the engine, put the resolved values (never the credential) on
the payload arm, unmarshal them into the kind's `Job` in `worker/connectors.go`, and
hand the credential over at spawn where one exists. The payload/struct parity test
(`TestEveryPayloadArmSendsTheWholeResolvedJob`) is what keeps each of them honest.

`temis` was the seventh and did not fit that shape, which is why it went last. A
central decision is a **business rule task**, not a connector task, and its completion
carries something no other job's does: a durable evaluation record — inputs, outputs
and the service's trace — retained so an operator can see how a decision was made
(ADR-0066). Nothing in the engine↔worker protocol could carry one: `CompleteJobWithDecision`
existed only on the in-process runner, and the HTTP completion a worker posts had no
field for it.

So this slice widened that contract rather than copying the others. The division it
draws is the part worth remembering: the worker is believed about the **evaluation** —
which decision, what it was asked, what came back, and the trace — and is believed
about nothing else. Which element instance the decision belongs to is stamped by the
engine from the job the worker held a lease on, so a report cannot attach itself to a
task it did not run. And the record's provenance is unchanged by the move, which looks
like it should have changed and does not: a central decision's trace has always been
the remote service's account of its own evaluation. Offloading changed which process
makes that call, not who authored the trace.

### `--in-process-connectors`

Stays, and logs a deprecation warning at startup naming this record and the kind it
was given. It is the escape hatch for an operator who has a reason — a worker that
cannot reach a host the engine can, a credential they have not moved yet.

**Amended when the table emptied.** This section originally said the flag "becomes an
error once the table above is empty, which is the moment option 2 stops breaking
anyone". The table is empty, and that sentence turns out to contradict this record's
own second decision driver — *no running deployment may break, including for an
operator who deliberately runs one in-engine today*. Both cannot hold: an installation
passing `--in-process-connectors` today would stop starting.

The premise was wrong rather than the conclusion. Option 2 stopped breaking anyone who
was blocked *by a missing worker half* — that was the gap the table tracked. It did
not stop breaking the operator the escape hatch was written for in the same paragraph,
whose reason is a network position, not an unbuilt worker. Those are different people
and only the first was counted.

So the flag stays what it is, and whether it is ever removed is a decision with its own
argument to make and its own migration to state — not a consequence that falls out of
this table reaching zero. What *has* changed is that using it is now always a choice
about deployment topology, never a workaround for something Atlas cannot do yet.

## Consequences

- **Positive:** a fresh install no longer makes an outbound HTTP call from the
  processor's own process. The remaining in-engine integrations are six named kinds
  with a defined slice each, instead of an unbounded "deprecated" state. The two kinds
  that legitimately stay are written down beside the ones that do not, so the Workers
  view and the Modeler's placement badge mean something.
- **Negative / trade-offs accepted:** an authenticated REST task now depends on the
  engine having rendered its secret reference into the worker's environment — one more
  thing to get right at spawn, and a reference that collides on its environment name is
  handed over once and warned about, exactly as AD's is. A REST call also gains a
  process hop, which is the trade ADR-0164 already accepted.
- **Follow-ups / risks to watch:** all seven slices have landed. What is left to watch
  is a check that no *new* kind ever ships with an in-engine handler and no worker half
  — the rule is only as good as the next kind added — and the open question this record
  no longer answers for itself: whether `--in-process-connectors` is ever removed, and
  what that migration would owe the operator who uses it deliberately.

## Links

- finishes [ADR-0164](0164-no-in-process-service-tasks.md) (its option 3)
- made possible by [ADR-0168](0168-connector-work-on-a-worker.md)
- default set and supervision: [ADR-0157](0157-worker-processes-supervision-and-console.md),
  [ADR-0182](0182-ad-default-offload.md)
- vocabulary and migration sequence: [ADR-0203](0203-worker-execution-model.md),
  [`worker-execution-migration.md`](../architecture/worker-execution-migration.md)
