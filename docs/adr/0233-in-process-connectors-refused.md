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

| Kind | Owed |
|---|---|
| `sharepoint` | Worker half |
| `scim` | Worker half |
| `soap` | Worker half |
| `temis` | Worker half |

Each is one slice of the same shape the landed kinds took: resolve the authored detail
against the instance's variables in the engine, put the resolved values (never the
credential) on the payload arm, unmarshal them into the kind's `Job` in
`worker/connectors.go`, and hand the credential over at spawn where one exists. The
payload/struct parity test (`TestEveryPayloadArmSendsTheWholeResolvedJob`) is what
keeps each of them honest.

Until a kind's slice lands it keeps working in-engine, and the Workers view keeps
marking it — that marking is now a countdown against this table rather than an
open-ended notice.

### `--in-process-connectors`

Stays, and logs a deprecation warning at startup naming this record and the kind it
was given. It is the escape hatch for an operator who has a reason — a worker that
cannot reach a host the engine can, a credential they have not moved yet — and it
becomes an error once the table above is empty, which is the moment option 2 stops
breaking anyone.

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
- **Follow-ups / risks to watch:** six slices; the `--in-process-connectors` warning
  becoming an error when the last lands; and a check that no *new* connector kind ever
  ships with an in-engine handler and no worker half — the rule is only as good as the
  next kind added.

## Links

- finishes [ADR-0164](0164-no-in-process-service-tasks.md) (its option 3)
- made possible by [ADR-0168](0168-connector-work-on-a-worker.md)
- default set and supervision: [ADR-0157](0157-worker-processes-supervision-and-console.md),
  [ADR-0182](0182-ad-default-offload.md)
- vocabulary and migration sequence: [ADR-0203](0203-worker-execution-model.md),
  [`worker-execution-migration.md`](../architecture/worker-execution-migration.md)
