# ADR-0538: Every operation declares its idempotency and effect, jobs carry an idempotency key, and atlas warns where a duplicate can do harm

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-11-06
- **Deciders:** pblumer (maintainer; approval to record this and to implement the declaration and the key on 2026-11-06)

## Context and problem statement

A job is delivered **at least once** ([ADR-0007](0007-job-worker-protocol.md)). A Worker
Instance that performs its side effect and then dies before it reports completion leaves
a job whose lease runs out; the job is offered again and the side effect can happen
twice. The protocol contract tells a Worker Type to be idempotent, but nothing makes it
so, and nothing tells a modeler or an operator which operations are.

With Worker Types coming from third parties ([ADR-0535](0535-worker-types-as-distributed-artifacts.md))
the question moves out of atlas's own code. Three facts bound what atlas can do about it:

1. **Exactly once is not available for an external side effect** without the target's
   cooperation, such as a duplicate filter keyed by a value the caller sends.
2. **Some operations cannot be made idempotent at all** by the caller: an SMTP mail, an
   SMS, a telephone call. After a crash the worker cannot know whether it went out.
3. **Idempotency cannot be verified in general.** A review can confirm it for the cases
   it looks at; a requirement nobody can check becomes a formality.

So "require idempotency" alone would promise what it cannot hold. What atlas can do is
make the property declared, give every worker the means to be idempotent, test what can
be tested, and warn loudly where a duplicate can do harm.

## Decision drivers

- An operation's behaviour under a second delivery is visible where somebody decides:
  publishing, installing, modelling, deploying, operating.
- A Worker Type that can be idempotent has what it needs, from atlas, to be so.
- Warnings are graded so they are read: the one that matters is not lost among many.
- Nothing here changes the engine's delivery semantics; that is a separate record.

## Considered options

1. Require idempotency in the contract and the hub's listing terms, and nothing else.
2. Declare per operation, give a key, grade and place warnings, test where possible
   (chosen).
3. Make every delivery at most once.

## Decision outcome

Chosen: **option 2**. Option 3 trades every duplicate for a lost action and a manual
check, which is wrong for the many operations that are idempotent; it survives only as
an opt-in mode for the operations that need it, in its own record
([ADR-0537](0537-at-most-once-delivery-for-irreversible-operations.md)).

### 1. Every operation declares two properties

In the Worker Type manifest, each entry of `spec.operations` carries:

| Field | Values | Meaning |
|---|---|---|
| `idempotency` | `natural` | A second delivery is harmless by nature (setting a record, assigning a group). |
| | `key` | The target filters duplicates by a key the worker sends: the job's `idempotencyKey`. |
| | `check` | The worker checks before acting whether the action already happened. |
| | `none` | Not idempotent: a second delivery can repeat the effect. |
| `effect` | `reversible` | A repeated effect can be undone or is tolerable (a second mail). |
| | `irreversible` | A repeated effect cannot be undone (a payment, a final deletion). |

Both fields are **required**. A manifest without them is refused at validation, so the
property is never assumed silently. Work that carries no manifest — a model-authored job
type served by a `--handle` command or by an external worker — is treated as
`none` / `irreversible` wherever this record warns: the worst case, because nothing says
otherwise.

### 2. Every job carries an idempotency key

The Worker protocol v1 gains `idempotencyKey` on every leased job, an additive field
within the major version (protocol contract, compatibility rule).

- Its value is `atlas:<node id>:<job key>`: the installation's stable node id
  ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §6, the node descriptor: sixteen random bytes minted once and never
  changed) and the job's key.
- It is **the same for every delivery of one task execution**: retries after a failure,
  redelivery after a lease ran out, and the retry an operator starts by resolving the
  job's incident all keep the job's key. Checked in the engine: resolving an incident
  re-creates the job under its existing key.
- It is **different for every execution**: a loop iteration or a multi-instance element
  creates new jobs and so new keys, and the node id keeps two installations' keys apart
  when they share a target system.
- A copied data directory or a restored backup carries its node id with it. Two
  installations started from one copy would share keys; that is stated, not prevented.

A worker passes the key to a target that accepts one (an idempotency header, a request
id), or uses it to check before acting. It is what makes `key` and `check` honest.

### 3. Warnings are graded

| Declaration | Grade |
|---|---|
| `natural`, `key`, `check` | no warning |
| `none` + `reversible` | notice |
| `none` + `irreversible`, or no declaration | warning |

### 4. Where atlas warns

1. **atlas hub and installation.** A release shows how many of its operations are
   `none`, and names the irreversible ones. Installing such a release asks the
   administrator to confirm them.
2. **Modeler.** A task bound to such an operation carries a mark, and the problems panel
   ([ADR-0026](0026-problems-panel-and-versioned-validation.md)) explains it, with the
   ways out: an operation that takes a key, a check modelled before it, or accepting it.
3. **Deploy.** The warning does not block. An acknowledgement per model element is kept
   with the version, so the same warning is not repeated on every deploy — the guard
   against warnings nobody reads any more.
4. **Run time — the warning that matters most.** When a lease runs out on a job of such
   an operation, the worker may already have acted. atlas then either
   - **(a) warns and redelivers:** the job is marked «possible duplicate — check the
     target system» in the Workers view and the job history, and an event is emitted
     that an alert can act on; or
   - **(b) holds:** the job is not redelivered; it waits on an incident «outcome
     unknown» for a person to decide. This changes the engine's delivery semantics and
     is decided in the separate record.

   Defaults: (b) for `irreversible`, (a) for `reversible`; an operator may change both.
   Until the separate record is accepted, only (a) is available.
5. **Policy per installation.** An operator may refuse to deploy `none` + `irreversible`
   operations in an environment without an explicit approval — the only place a warning
   blocks.

A warning at run time is only as good as the person who reads it. The event of 4(a) is
part of the event catalogue ([ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md)),
so it reaches whoever an installation routes such events to, not only the Workers view.

### 5. Testing in atlas hub

A release in the `verified` tier ([ADR-0536](0536-worker-sdk-licence-and-hub-curation.md))
passes a duplicate-delivery test for every operation declared `natural`, `key` or
`check`: against a mock of its target, the same job is delivered twice, and once more
after a simulated crash between the side effect and the report, and the effect must
happen once. An operation declared `none` is exempt and listed as such. The test shows
the declaration holds for the cases tested; it is not a proof.

### Consequences

- **Positive:** a duplicate stops being a silent property of a worker's code; it is a
  declared, visible, graded fact at every point a decision is taken. Workers that can be
  idempotent get a stable key from atlas instead of inventing one.
- **Negative / trade-offs accepted:** every manifest must state two more fields; a
  warning system has to be built across hub, Console, Modeler and run time; the key is
  only as unique as the node id, so cloned installations share it.
- **Follow-ups:** the separate record for variant (b); the event of 4(a); the Modeler
  and problems-panel warnings; the deploy acknowledgement; the policy; the duplicate
  delivery test in the hub's checks; declarations for the built-in Worker Types when
  they move onto the manifest (ADR-0535 step 4); the question of who is liable when a
  declaration is false, for the hub's listing terms (ADR-0536's legal review).

## As built

**2026-11-06 — the declaration and the key.** `workertype.Operation` carries
`idempotency` (`natural`, `key`, `check`, `none`) and `effect` (`reversible`,
`irreversible`); `Manifest.Validate` refuses an operation missing either or naming another
value, and `Operation.DuplicateRisk` grades it as in §3. Every leased job carries
`idempotencyKey` = `atlas:<node id>:<job key>` (`api/handlers.go`, `workersdk.Job`); a test
leases a job, fails it with a retry left, fails it into an incident, resolves the
incident, and finds the same key on all three deliveries. The protocol contract documents
both. Not yet: every warning place of §4, the event of 4(a), the deploy acknowledgement,
the policy, and the hub's duplicate-delivery test (§5, recorded as a `verified` condition
in ADR-0536).

**2026-11-09 — built-in Worker Types.** A new built-in Worker Type ships a manifest with
the declaration, and the kinds released before are listed and owe one
([ADR-0546](0546-new-built-in-worker-types-ship-a-manifest.md)).
SharePoint Server is the first (`connector/sharepointserver/manifest.json`).

## Links

- [ADR-0007](0007-job-worker-protocol.md) — at-least-once delivery and leases
- [ADR-0535](0535-worker-types-as-distributed-artifacts.md) — Worker Types as artifacts
- [ADR-0536](0536-worker-sdk-licence-and-hub-curation.md) — the `verified` tier
- [ADR-0026](0026-problems-panel-and-versioned-validation.md) — the problems panel
- [ADR-0061](0061-incident-model.md) — incidents
- [ADR-0537](0537-at-most-once-delivery-for-irreversible-operations.md)
