# ADR-0426: An untriggered create never seeds several start events

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-29
- **Deciders:** Atlas maintainers
- **Open question:** how many deployed models rely on the behaviour this record refuses —
  a process with no none start and several start events, created through the API or a
  call activity, running every branch. Nothing in the engine counts such creates, so the
  size of the behaviour change is unmeasured. The decision rests on the reading that no
  such model is correct today; a deployed model that depends on it would be the evidence
  against that reading.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0226 decided that a start event is a trigger: when a message, a signal or a start
timer fires, only that start event is seeded. For a create that nobody triggered — the
API, a call activity — it kept a fallback in `startElementsFor`:

- seed the process's **none** start events, which is what "start this by hand" means;
- where the process has **no** none start, seed **every** start event, keeping the
  permissiveness ADR-0035 recorded so that a message-only process can still be started
  by hand and tested.

The fallback was argued for a process with **one** start event. With one, seeding "every"
start event seeds that one, and the argument holds. With several and no none start, it
seeds all of them — which is the defect ADR-0226 was written to remove, reached through a
different door.

This was checked against the engine rather than inferred. A process with three message
start events (`provision`, `change`, `deprovision`), each leading to its own branch:

| Created by                            | Branches that ran                |
|---------------------------------------|----------------------------------|
| publishing `…deprovision`             | `deprovision`                    |
| `CreateInstance` (the API path)       | `provision`, `change`, `deprovision` |
| a call activity                       | `provision`, `change`, `deprovision` |

The call activity reaches it because `callActivityBehavior.OnActivated` creates the child
with `AppendCreateChildInstanceCommand`, which carries no `StartElements`, so the child
goes through the same fallback.

The shape is not hypothetical. Modelling a product's lifecycle as one process with one
message start per operation (ADR-0425)
is exactly this shape, and anybody who starts such a process by hand from the Console, the
MCP create tool or a call activity would provision and deprovision in the same instant.

## Decision drivers

- **ADR-0226's own reasoning.** Running branches nobody triggered is not a lenient
  reading of BPMN; it is a different process.
- **Keep what the permissiveness was for.** A process whose only entry is one message
  start must stay startable by hand.
- **Loud, not silent.** The current failure produces well-formed instances and no error;
  the only symptom is effects in target systems.
- **No change to what is written or replayed.** The creation path is load-bearing for
  every instance (ADR-0226) and for recovery (I4).
- **No allocation on the create path** (I1).

## Considered options

1. **Leave it** and document that such a process must not be created by hand.
2. **Refuse the create** — at the API boundary with an error, and at a call activity
   with an incident.
3. **Seed nothing** and create an instance without a token.
4. **Let the caller choose** a start event by element id on every untriggered create.

## Decision outcome

Chosen option: **2 — refuse the create**, because it is the only option that turns the
silent case into a visible one without taking anything away from the case the
permissiveness was kept for.

The rule: an untriggered create of a process that has **no none start event and more than
one start event** does not create an instance.

**At the API boundary** — `POST /api/v1/processes/{key}/instances`,
`POST /api/v1/instances`, the MCP create tool — the request is answered with **409**,
naming the process's start events and saying that such a process is started by one of its
triggers. The check reads the compiled process before any command is enqueued. It writes
no event and changes nothing that is replayed.

**At a call activity** the child is not created. An incident is raised on the call
activity (ADR-0061), naming the target and its start events, and the token stays where it
is — resolving the incident after the target is fixed retries the create. The check reads
the target's compiled start events, which the create reads anyway, so it adds no
allocation to the path (I1). It runs when the call activity activates, because a target is
resolved per server and per version (ADR-0076, ADR-0105) and is not known at the caller's
deploy. The Problems panel (ADR-0026) additionally warns at design time where the target
can be resolved then; the runtime incident remains the authority.

**Unchanged:**

- a process with a none start: its none starts are seeded, as ADR-0226 decided;
- a process whose only start event is a message or timer start: it is seeded, as
  ADR-0035 and ADR-0226 decided — this is the case the permissiveness exists for;
- every triggered create: exactly the start event that fired, as ADR-0226 decided;
- a process with **two none** starts: both are seeded on a create by hand. ADR-0226
  names that ambiguity and leaves it deliberately, and this record does not reopen it.

### As built

Every door that creates an instance by hand asks one question of the compiled
process, `CompiledProcess.UntriggeredStartAmbiguous`, and refuses on it
(`api/untriggeredstart.go`). There are more doors than the three named above: besides
the two start routes (and the MCP tool, which calls them), a CSV upload, a public start
link, an order line's return and a reconciliation's deprovisioning create by hand too,
and each refuses the same way — the public link with the same answer it gives for a
non-executable process, because the person filling in its form cannot act on the
model's shape. The call activity raises a job-less incident, and resolving it re-runs
the activation through `resumeParkedElement`. The Problems panel's finding is rule
`call.untriggered-start`, reported by `POST /api/v1/validate` and as a deploy warning.

### Consequences

- **Positive:** a process with several triggers can no longer be started into all of them
  at once, by hand or by a caller model.
- **Positive:** the refusal names the start events, so the person who pressed Start
  learns what the process expects instead of discovering it in a target system.
- **Negative / trade-offs accepted:** a create accepted today is refused. A client or a
  caller model that relied on it was running every branch; the change is called out in
  the changelog, not hidden.
- **Negative:** a call activity that runs today stops with an incident. The model was
  already wrong, but it now stops visibly where it used to run silently.
- **Follow-ups / risks to watch:**
  - Playing such a process in the Modeler's token simulation (ADR-0078) should follow the
    same rule, so that the simulation does not show what the engine refuses.
  - A caller that genuinely needs to enter such a process by hand uses one of its
    triggers — a message, or the directed trigger of
    ADR-0425. Option 4 is not
    needed for that.

## Pros and cons of the options

### Option 1 — leave it and document
- Good: no behaviour change.
- Bad: the rule to remember is "a create by hand runs every branch", and forgetting it
  provisions and deprovisions at once, silently.

### Option 2 — refuse (chosen)
- Good: turns a silent failure into an answer; keeps every case the permissiveness was
  kept for; no event, no replay change.
- Bad: a behaviour change for any model that relied on the old reading.

### Option 3 — seed nothing
- Good: nothing unintended runs.
- Bad: creates an instance with no token — one that never ends and waits for nothing,
  which ADR-0226 already judged worse than the permissive answer.

### Option 4 — choose a start event by element id
- Good: gives the caller full control.
- Bad: makes element ids a contract every caller depends on, which ADR-0373 declines to
  do; and a caller that does not choose still needs a rule, which is this record.

## Links

- amends ADR-0226 (the fallback for an untriggered create) and ADR-0035 (the
  permissiveness it kept)
- relates to ADR-0076 and ADR-0105 (call activities and their per-server resolution)
- relates to ADR-0061 (incidents) and ADR-0026 (the Problems panel)
- relates to ADR-0078 (token simulation)
- motivated by ADR-0425
