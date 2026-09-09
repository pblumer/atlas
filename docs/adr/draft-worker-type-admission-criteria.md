# ADR-DRAFT: What earns a Worker Type — admission criteria for a new kind

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers
- **Open question:** whether these gates should also govern a Worker Type shipped as an
  external package under [ADR-0207](0207-worker-type-packaging.md) /
  [ADR-0208](0208-worker-type-packages.md) rather than as a package in this tree. The
  packaging contract exists, but no third-party package has been through it, so what a
  gate costs an outside author is unmeasured. This record therefore binds only kinds
  proposed for the tree, and says so in *Scope*.
- **Question checked:** 2026-09

## Context and problem statement

Adding a Worker Type is cheap by design. [ADR-0203](0203-worker-execution-model.md) and
[ADR-0207](0207-worker-type-packaging.md) fixed the cost at one package under
`connector/`, one `managedConnectorKind` entry, and one reserved job type index — and
every record that adds a kind repeats that sentence approvingly. Cheap to *build* is not
the same as cheap to *own*, and nothing in this repository states what makes a capability
worth owning.

The consequence is visible in three places.

**The question is re-argued from scratch every time.** [ADR-0235](0235-google-sheets-worker.md)
argues that the generic REST Worker Type cannot reach Google's APIs because the credential
is structurally out of reach. [ADR-0258](0258-discord-worker.md) argues almost the opposite
starting point — REST *can* reach Discord — and lands on a Worker Type anyway, because
"what earns a Worker Type is a capability whose *operations* are worth naming".
[ADR-0201](0201-jira-connector.md) made that same argument first. Three records, three
framings, one unwritten rule.

**The refusals are unrecorded.** A record exists for every kind that was built. Nothing
records a candidate that was considered and declined, so the reasoning that declined it is
not available to the next person who proposes it. The immediate prompt for this record was
the question of an OpenTofu, Ansible or Puppet Worker Type, which had to be answered by
reading the codebase and would otherwise have left nothing behind.

**The debt grows quietly.** [`api/releasedkinds.go`](../../api/releasedkinds.go) carries
thirty reserved job types. Twenty-four are of `classConnector`, and sixteen of those ship
no Repository package, which [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md)
requires of a released kind. The registry pins that count so it cannot grow, which stops
the bleeding without saying what caused it: each of those sixteen was a kind admitted with
its publication left to a follow-up nobody scheduled.

Separately, [`docs/comparisons/n8n.md`](../comparisons/n8n.md) commits the project to
"selected high-value Worker Types only after the engine foundation is mature", without
saying what selects them. That sentence is the policy this record is the missing half of.

The question this record answers is therefore: **what must be true of a capability before
Atlas admits it as a Worker Type, and where is that shown?**

### Scope

This record governs a Worker Type proposed as a package in this repository. It does not
govern the generic REST Worker Type's reach, script tasks
([ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)), or a third-party package
installed under ADR-0208 — see the *Open question* above.

## Decision drivers

- **The rule already exists; it is just not written down.** Every criterion below is
  extracted from an accepted record's own argument. A rule that would have refused a kind
  already accepted would be a new policy in the disguise of a summary, so the retro-check
  in *Consequences* is part of the decision, not a courtesy.
- **A refusal must be actionable.** "No" teaches nothing. A candidate that fails must
  learn which gate it failed and what would change the answer — which, for the most
  interesting candidates, is work on the seam rather than on the integration.
- **Judgement is not mechanizable, but completeness is.** Whether an operation is a step a
  process takes cannot be tested. Whether a kind shipped its registry row, its setup entry
  and its handbook card already is ([ADR-0289](0289-worker-type-setup-in-the-panel.md),
  ADR-0167). The gates should not pretend to more enforcement than that.
- **The engine's shape is a real constraint, not a preference.** A job is leased, retried
  and completed once; a variable has a ceiling; there is no artifact store. A capability
  that does not fit is not a bad idea, it is an idea ahead of the seam.

## Considered options

1. **Leave it implicit.** Each proposing record argues admission from first principles, as
   ADR-0201, ADR-0235 and ADR-0258 each did.
2. **A prose paragraph in [`AGENTS.md`](../../AGENTS.md)** beside the "adding a Worker Type
   is one package" instruction.
3. **This record: five named gates**, answered in the proposing record, with the release
   registry as the durable evidence for the one gate that is mechanical.
4. **A test that enforces the gates.** A guard in `api` that refuses a new
   `managedConnectorKind` until it declares an answer to each gate.

## Decision outcome

Chosen: **option 3 — five gates, answered in the record that proposes the kind.**

A capability is admitted as a Worker Type when it passes all five. Failing one is not
always a refusal; gate 3 in particular produces a *deferral with a named missing piece*,
which is the distinction this record most wants to preserve.

### Gate 1 — Its operations are steps a process takes

The authored surface must be a short, closed list of named operations, each of which is a
thing a business process does. Not what the vendor's API offers: ADR-0258 declines Discord's
guilds, roles and voice; ADR-0235 declines the Sheets API's pivot tables and conditional
formats. The test is whether an operation would appear in a BPMN diagram as a task with a
verb on it.

A capability whose honest authored surface is "call this endpoint with this body" fails
here, because that surface already exists and is called the REST Worker Type
([ADR-0067](0067-service-task-connector-catalog.md)).

A capability with **no** operation at all fails here too, and this is the case worth naming
because it is easy to miss. A system that converges on a schedule is not invoked; its input
is a change to declared state, and its output arrives later, unprompted. In BPMN that is a
send task and a message catch event, not a service task — so the Worker Type it seems to
want is a Worker Type for something else.

### Gate 2 — The generic path is blocked, or lossy in a way every model would repeat

One of two things must be true, and the record must say which:

- **Blocked.** The REST Worker Type cannot reach it at all. ADR-0235 is the worked case:
  Google requires an OAuth2 token obtained by signing a JWT assertion with a service
  account's private key, and the REST Worker's auth surface is bearer, basic, or a
  server-held API key. No model author can bridge that without putting a private key in a
  process variable. The absence is structural, not unwritten work.
- **Lossy.** It can be reached, but only by every model re-authoring an integration that
  nothing validates: a URL with an id interpolated into its path, a hand-built body, a
  secret whose value must be a literal prefix plus a token. ADR-0258 is the worked case.
  The cost is not impossibility, it is that the mistake is found by a token parking on a
  404 instead of by a deploy check.

Where the vendor ships its own control plane with a plain bearer-token REST API, neither
limb holds: the REST Worker reaches it, deploy-time validation of *that* API's job model is
the vendor's job, and a Worker Type would buy convenience only. Convenience is not a gate.

### Gate 3 — The work fits a job

A job is leased once, may be executed more than once, and reports one result. Concretely,
today:

| Property | Value | Where |
|---|---|---|
| Default lease | 5 minutes | `defaultJobLease`, [`api/handlers.go`](../../api/handlers.go) |
| Longest lease a worker may ask for | 24 hours | `maxJobLease`, same file |
| Renewing a lease mid-run | no such call in the worker protocol | `/api/v1/jobs/{activate,complete,fail}` |
| One process variable | 1 MiB by default | `Limits.Variable`, [`limits/limits.go`](../../limits/limits.go) |
| Artifact or blob store | none | — |
| Streaming output from a worker | none | — |

Four questions follow, and a candidate must answer all four:

1. **Does it finish inside a lease it can ask for at activation?** The hold is chosen once,
   when the job is leased. Work whose duration is unknown at that moment has to pick a
   ceiling and hope.
2. **Is running it twice safe?** A lease that elapses hands the job to another Worker
   Instance. The `LeaseEpoch` fence ([ADR-0007](0007-job-worker-protocol.md)) rejects the
   *outcome* of the round whose lease expired; it does not stop that round's side effect,
   which has already happened. Execution is at-least-once and only the booking is
   at-most-once. A capability that is not idempotent, or that does not hold its own lock,
   fails here.
3. **Does its result fit a variable?** A result that routinely exceeds the variable budget
   has nowhere to live. Putting it in a variable anyway makes the event log carry it
   forever.
4. **Is the result the whole answer?** If a human has to read a progress stream to decide
   what happens next, the capability wants something the seam does not have.

**Failing gate 3 is a deferral, not a refusal.** The record must name the missing piece —
lease renewal, an artifact store, worker log streaming — because that names the work that
would admit the capability. A "no" here that does not name a piece is an unexamined "no".

### Gate 4 — The credential has one shape and a server-side home

The Worker's secret resolves by Worker name from the vault
([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
[ADR-0070](0070-vault-on-by-default-with-generated-key.md)) and travels no further: a job
carries resolved work values, never credential material
([ADR-0168](0168-connector-work-on-a-worker.md)). It must never appear in a BPMN file, an
event, or a variable.

The shape must be settled at admission. One bundle shape is the easy case (ADR-0258's
`botToken`); two, distinguished by something the Worker declares, is the tolerable case
(ADR-0201's Cloud and Data Center pair). A credential that is per-model, per-end-user, or
negotiated at call time is a different problem with its own record, not a detail of this
kind.

### Gate 5 — It arrives complete

A kind is admitted with everything a released kind owes, in the same change:

- the `managedConnectorKind` entry in [`api/connectorkinds.go`](../../api/connectorkinds.go);
- the release registry row in [`api/releasedkinds.go`](../../api/releasedkinds.go), naming
  the record that decided it and its class;
- the Repository package ADR-0167 requires of a `classConnector` kind;
- the setup entry and handbook card of ADR-0289, with the month somebody last walked the
  steps at the provider.

The sixteen registry rows with no package are the argument for this gate. Each was a change
that was complete by the standard of its day, with publication left to a follow-up. The
count is pinned so it cannot grow; this gate is why it should not need to be.

### Not criteria

Four things that look like reasons and are not: that the vendor is popular; that a
competing engine ships one; that n8n has a node for it; that a Worker Type would be more
convenient than the REST Worker for a model somebody is writing this week. The first three
are about the market and the fourth is gate 2's second limb without its second half.

## Worked examples

The three candidates that prompted this record, and two retro-checks. A retro-check that
failed would falsify the gates rather than the kind.

| Candidate | G1 operations | G2 generic path | G3 job shape | G4 credential | G5 complete | Outcome |
|---|---|---|---|---|---|---|
| Puppet | **fails** — convergence is not an invocation | — | — | — | — | Refused at gate 1 |
| Ansible via its automation platform | passes | **fails** — a bearer-token REST API the REST Worker already reaches | — | — | — | Refused at gate 2 |
| OpenTofu | passes — plan, apply, output, destroy | passes on the lossy limb | **fails** — see below | unsettled — cloud access is federated, not a stored bundle | — | Deferred, pieces named |
| Discord (ADR-0258) | passes | passes, lossy limb | passes | passes | passes | Admitted |
| Google Sheets (ADR-0235) | passes | passes, blocked limb | passes | passes | passes | Admitted |

**OpenTofu is the instructive one.** It is the only candidate of the three with a real
claim, and the claim is not the integration. What is genuinely differentiating is the
process *around* an infrastructure change: a request, a risk decision in DMN, a plan, a
human approval on a user task, an apply, a verification, and a change record closed in Jira
or Remedy. Every element of that exists today, and the apply itself is reachable through the
REST Worker against whichever control plane runs it.

What fails is gate 3, on three counts: an apply's duration is not known when its lease is
taken and cannot be extended; a machine-readable plan routinely exceeds the variable
budget and has nowhere else to go; and an approver deciding on a plan wants the output, not
an exit code. So the pieces are **lease renewal, an artifact store, and worker log
streaming** — three records that do not exist. Until they do, the honest form of this
capability is an example process under [`examples/`](../../examples/), not a kind.

## Consequences

- **Positive:** "should X be a Worker Type" is answerable from a written rule instead of an
  afternoon's analysis, and a refusal leaves a reason behind. Gate 3 converts the most
  interesting refusals into a list of seam work, which is where the value actually is.
- **Positive:** the retro-check above states a property the gates must keep — they describe
  the kinds Atlas already has. A future gate that would evict one of them is wrong.
- **Negative / trade-offs accepted:** a change that used to be one package now owes five
  answers, and four of them are judgement. Gate 3 will refuse capabilities people want, and
  the refusal will be correct and unpopular in the same breath.
- **Negative:** gates 1, 2 and 4 are unenforceable by test. Their whole weight rests on
  review, which is the same weight the unwritten rule already carried, with the difference
  that a reviewer can now point at something.
- **Follow-ups / risks to watch:** the three pieces gate 3 names each want a record. The
  sixteen packages ADR-0167 is still owed are unaffected by this record and remain owed.
  Whether these gates bind an external package is the open question above.
- **Noted while writing this, out of scope:** ADR-0235 and ADR-0258 both cite "(I6)" for
  "no credential in a model". I6 in [`docs/architecture/invariants.md`](../architecture/invariants.md)
  is "events are facts, commands are intentions"; the credential rule is ADR-0069/0070 and
  ADR-0168, not an invariant. This record cites those instead and does not amend the two
  records.

## Pros and cons of the options

### Option 1 — Leave it implicit
- Good: no process, and three good records reached the right answer without one.
- Good: each argument is made against the concrete case rather than a checklist.
- Bad: the reasoning is spread across records that do not cross-reference on this point, so
  it is found only by someone who already knows it exists.
- Bad: it records no refusals, which is exactly the half a proposer needs.

### Option 2 — A paragraph in AGENTS.md
- Good: read by every agent, at the moment the instruction to add a package is read.
- Bad: `AGENTS.md` is an operating guide, and it says so — it points at records for *why*.
  A criteria list there would be the only load-bearing decision in the file with no record
  behind it, and it would drift from the records it summarizes.

### Option 3 — Five gates in a record (chosen)
- Good: the gates are extracted from the accepted records, and the retro-check states that
  as a property rather than a claim.
- Good: gate 3 is falsifiable against the numbers in the table, and the numbers move as the
  seam grows — a deferral is dated by construction.
- Bad: five gates is more ceremony than three of the last four kinds needed.
- Bad: a checklist invites being answered rather than thought about.

### Option 4 — Enforce the gates with a test
- Good: it would work for gate 5.
- Bad: gate 5 is already enforced, by `go test ./api` and the ADR-0289 setup guard. The
  test would be the existing tests with a new name.
- Bad: the other four gates ask whether an operation is a step a process takes and whether a
  side effect is safe twice. A test that claimed to answer those would answer a proxy for
  them, and the proxy would be gamed the first time it was inconvenient.

## Links

- decided by: [ADR-0203](0203-worker-execution-model.md) (what a Worker Type *is*),
  [ADR-0067](0067-service-task-connector-catalog.md) (the catalog and the generic REST kind)
- gate 2's worked cases: [ADR-0235](0235-google-sheets-worker.md) (blocked),
  [ADR-0258](0258-discord-worker.md) and [ADR-0201](0201-jira-connector.md) (lossy)
- gate 3's mechanics: [ADR-0007](0007-job-worker-protocol.md) (leases and the fence),
  [ADR-0149](0149-bounded-connector-call-budget.md) (the outbound call budget),
  [ADR-0270](0270-bounded-job-polling.md) (what a worker's poll costs)
- gate 4: [ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)
- gate 5: [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md),
  [ADR-0289](0289-worker-type-setup-in-the-panel.md),
  [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
- scope: [ADR-0207](0207-worker-type-packaging.md), [ADR-0208](0208-worker-type-packages.md)
- the policy this is the missing half of: [`docs/comparisons/n8n.md`](../comparisons/n8n.md)
