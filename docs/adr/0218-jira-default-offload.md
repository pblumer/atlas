# ADR-0218: Jira runs on a worker by default

- **Status:** Proposed
- **Date:** 2026-09-01
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0201](0201-jira-connector.md) made Jira a Worker Type. Its follow-ups gave it an
actual worker: `worker/connectors.go` serves the kind from its own environment, and
`jiraWorkerEnv` hands a supervised worker the site URL and the vault credential at spawn,
so `--offload-connectors jira` and `--supervise-connector jira` both work end to end.

What was deliberately not done was defaulting it. `DefaultOffloadedKinds()` still omits
`jira`, so on an installation that configures nothing, a Jira task runs **in the engine**
— exactly as it did before the worker existed. The reasoning at the time: defaulting a
kind changes behaviour for every existing installation, and that is a decision worth its
own record rather than a line slipped into a feature.

This is that record. What forced it was not the argument, which had not moved, but
watching the consequence.

### What the deferral actually cost

An operator with a working Jira connector went looking for its worker and could not find
it. Three places were checked, and the kind is absent or invisible in all three:

| Where they looked | What is there |
|---|---|
| **Workers** — who has pulled | nothing; no worker exists to pull |
| **Supervised by this server** | nothing; the ten processes are exactly `DefaultOffloadedKinds()` + `DefaultSupervisedWorkerOnlyKinds()` |
| **Job types** | `io.atlas.jira`, served in-process, **folded away** |

The third is the one that matters. The Workers view hides an idle built-in on purpose —
"an engine knows eighteen built-in job types and a given installation uses two of them" —
and a healthy in-process kind is idle by that definition: nothing queued, nothing in
flight, no incidents, and no worker pulling it because none may. So a Jira connector that
is *working* is a row the view deliberately does not show, in a table whose entire subject
is who is doing the work.

It becomes visible when it fails. An incident makes the row busy and unfolds it, which is
how this operator had seen `io.atlas.jira` before — in the screenshots of a task that was
erroring. Working, it disappears again.

That is not a bug in the view: an idle built-in genuinely is noise, and folding it is what
keeps the two types someone deployed readable among fifty. It is a bug in the *default*.
The view is built around workers, and [ADR-0203](0203-worker-execution-model.md) says a
Worker Type is an external worker like every other. A kind that answers "who is doing this
work" with "the engine, quietly, and you will hear about it when it breaks" is the odd one
out — and it is odd only because of a default nobody chose deliberately for Jira; it was
inherited from the period when there was no worker to choose.

## Decision drivers

- **Consistency with what the product says a Worker Type is** (ADR-0203).
- **Observability.** Work being done should be visible where an operator looks for it,
  and a process with a pid, a log and a restart button is the visible form.
- **The loop.** A Jira operation is one to three round trips to somebody else's cloud;
  ADR-0164's argument for moving that off the engine applies unchanged.
- **The credential is worth less in a worker than in the engine** (ADR-0166's argument,
  which ADR-0182 used for AD and ADR-0192 for Remedy).
- **Reversibility.** A default that cannot be turned back is not a default.

## Considered options

1. **Add `jira` to `DefaultOffloadedKinds()`** — a supervised worker per installation,
   provisioned from the connector store and the vault.
2. **Leave the default and make the Workers view show in-process kinds.** Unfold a served
   built-in, or say somewhere that the engine is serving it.
3. **Leave it as it is** and document that Jira runs in-process unless asked otherwise.

## Decision outcome

Chosen option: **1 — Jira joins the default set.**

It is Remedy's case ([ADR-0192](0192-remedy-default-offload.md)) with an issue tracker in
place of an ITSM host, and it meets the same condition the set requires: every managed kind
in it must be one `superviseEnv` provisions, which `TestEveryDefaultOffloadedKindCanBeServedByItsWorker`
holds and `jiraWorkerEnv` satisfies. Nothing new has to be built. What changes is where a
Jira task runs when nobody said anything.

Option 2 is a real improvement and is **not** an alternative to this one — it is worth
doing on its own account for the kinds that stay in the engine (clio, temis, SharePoint,
DMN, REST, LDAP, SOAP, SCIM). But it answers "how do I find the thing" with a better
signpost, where the question underneath was "why is this kind not like the others". A
signpost to an inconsistency is not the fix for the inconsistency.

Option 3 keeps a default that exists only because of the order the work was done in.

### Consequences

- **Positive:** a Jira connector's work appears in the Workers view as a supervised
  process with a pid, a log and a restart button, next to mail and Remedy. Three Jira
  round trips leave the engine's loop. The credential sits in a worker rather than in the
  engine. And the kind stops being the exception to what ADR-0203 says a Worker Type is.
- **Negative / trade-offs accepted:** this is a **behaviour change for every existing
  installation**. On the next start, Jira tasks are leased by a supervised worker instead
  of served in the engine — one more process, and a Jira site that was reachable from the
  engine's network must be reachable from the worker's (the same process, on the same
  host, so this is a change of principle rather than of route today). A connector whose
  vault bundle does not resolve is left out of the handover and its tasks park rather than
  run, where before they would have failed with a message from Jira; the Console shows the
  connector as configured-not-working, and the parked state is the honest one.
- **Follow-ups / risks to watch:** `--in-process-connectors jira` is the way back, and it
  is the same escape every defaulted kind has. Option 2 as its own change, for the kinds
  the engine still serves — the argument for it survives this record and is not spent by
  it. And the set is now five managed kinds wide: the next one added should be asked
  whether it belongs here at the time it gets its worker, rather than a month later.

## Pros and cons of the options

### Option 1 — default the kind
- Good: consistent with ADR-0203; visible where operators look; off the loop; credential
  in the worker; nothing new to build; reversible with one flag.
- Bad: changes behaviour for every installation on upgrade; a misconfigured connector
  parks instead of erroring.

### Option 2 — make in-process work visible instead
- Good: helps every kind that stays in the engine, not just Jira; no behaviour change.
- Bad: signposts the inconsistency rather than removing it; leaves Jira as the Worker Type
  without a worker.

### Option 3 — leave it
- Good: no upgrade surprise.
- Bad: keeps a default that was inherited rather than chosen, and that an operator has
  already been caught by.

## Links

- follows [ADR-0192](0192-remedy-default-offload.md), the same decision for Remedy
- follows [ADR-0182](0182-ad-default-offload.md), the same decision for Active Directory
- extends [ADR-0201](0201-jira-connector.md), whose follow-ups built the worker
- governed by [ADR-0164](0164-no-in-process-service-tasks.md) and
  [ADR-0168](0168-connector-work-on-a-worker.md)
- required by [ADR-0203](0203-worker-execution-model.md)'s definition of a Worker Type
