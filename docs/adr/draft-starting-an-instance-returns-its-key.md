# ADR-DRAFT: Starting an instance returns its key

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** How often two callers start the same definition inside one batch is
  unmeasured. The case below rests on that collision being real rather than theoretical:
  if it never happens, the recipe clients use is good enough as it stands and this record
  is work for nothing. Nothing in the engine counts it, and the demo apps that would hit it
  first have one user at a time.
- **Question checked:** 2026-09

## Context and problem statement

Atlas's direct way to start a process instance over HTTP is
`POST /api/v1/processes/{key}/instances`, and it answers with the definition key it was
handed and the engine's live counts (`createInstanceResp`, `api/handlers.go`). It does not
say which instance it made.

[ADR-0241](0241-finding-an-instance.md) settled how an instance is *found*: a bare
instance key is a point read through `lookupInstanceByKey`, and one definition's instances
come off that definition's own index, newest first. Neither answers the question a client
has the moment it presses Start. It holds the definition key it posted to, which names a
version rather than an execution, and on that version's index the instance it just caused
is indistinguishable from every other instance of the same version.

So every such client reconstructs the identity by elimination: list the definition's
instances, start, list again, take the difference, and if the difference is empty take
the newest row and hope. Four hosted apps under `api/web`, the shape
[ADR-0204](0204-hosted-apps-on-an-isolated-origin.md) describes, each carry a copy of that
recipe, and so does anything else written against this endpoint.

The recipe failed in production. The apps diffed the *unscoped* listing, which is capped
and whose active half is scanned in ascending instance-key order, so on a server holding
fifty thousand active instances their own instance was never on the page they were
searching. The start died on `Cannot read properties of undefined (reading 'key')` while
the instance sat correctly on its first user task. Scoping the listing with `?process=`
takes that failure away, because it reads the definition's own index rather than the
engine's oldest rows. That repair is a change to the clients and is handled separately
from this record.

What scoping cannot repair is the other defect in the same recipe, which is that it is
still a guess. Two callers starting the same definition inside one batch are each shown
two new instances and have no way to tell which is theirs. For the booking wizard that is
one customer handed another customer's form.

The endpoint's silence is structural rather than an oversight. `Processor.CreateInstance`
appends a command to the queue; the key is minted later, inside the batch, by
`handleProcessInstanceActivating` calling `c.NewKey()` on the single-writer goroutine
(I3), and is frozen into the `ProcessInstanceActivated` event so that recovery reads it
back rather than regenerating it (I6). Recovery replays events through `applyToState` and
never re-runs commands. Nothing in that path carries the key back out to whoever asked.

The question this record answers: **how does a caller learn the identity of the instance
it just started, without weakening the single-writer boundary or the durable-before-visible
ordering?**

## Decision drivers

- **The answer must be the instance's identity, not a guess that is usually right.** A
  start that returns the wrong instance under load is worse than one that returns
  nothing, because the client cannot tell.
- **I3, single writer.** The key is minted on the loop. Nothing may read processor state
  from another goroutine to get at it.
- **I2, durable before visible.** A key minted in phase 1 of a batch that then fails to
  append or sync names an instance that does not exist. Nothing may be reported before
  the batch's one fsync ([ADR-0005](0005-group-commit-and-fsync-strategy.md)).
- **I4/I6, replay must not change.** Anything added for the benefit of a live caller has
  to stay out of the event and out of `applyToState`, or recovery starts depending on
  who was listening.
- **I1, the hot path.** `Command` is the queue element for every intent, token movement
  included. What is added for a start is paid for by every command.
- **Most starts have nobody waiting.** A timer start, a correlating message, a broadcast
  signal and a call activity's child all create instances with no caller to tell.
- **The public start link stays silent on purpose.** `handlePublicFormStart` answers
  `{"started": true}` and reads nothing back ([ADR-0029](0029-public-process-start-links.md)).
  An instance key is the handle to the `/instances/{key}/...` routes; handing one to an
  unauthenticated caller is a different decision with a different risk, and this record
  does not take it.
- **Additive.** Clients reading `definitionKey` and `stats` must not break.

## Considered options

1. Return the key from the create endpoint, correlated per command.
2. Read it back server-side: the handler runs the same scoped diff the clients run.
3. A caller-supplied correlation token, persisted on the instance and searched for.
4. Leave it, and document the scoped-listing recipe. This is what is in place today.
5. An await or subscribe surface over the event stream.

## Decision outcome

Chosen option: **"Return the key from the create endpoint, correlated per command"**,
because it is the only option that answers with the instance's identity rather than with
a narrowed guess, and the machinery it needs already exists at every point it touches.

The shape, stated as constraints rather than as a struct, because the struct is the
implementer's to choose:

- **`CreateInstance` gains an optional result handle** that the caller owns: a pointer,
  nil for every start with nobody waiting. It has to be a pointer because `processOne`
  takes its `Command` by value and `ProcessingContext` holds a copy, so a plain field
  would be written to a copy nobody reads.
- **Correlation is per command, never per drive.** `driveMu` serializes rounds, but one
  `RunUntilIdle` drains the whole queue, so another handler's drive may be the one that
  folds this command. A cell on the command is filled by whoever processes it. An answer
  built from "the instances created during my drive" would be wrong for exactly the
  callers this record exists for.
- **Reporting happens on the post-fsync seam, not at mint time.** The processor already
  has one: phase 5 of `processBatch`, after the append, the single `Sync` and the commit.
  A batch that fails anywhere before that returns its error, `drive` propagates it, and
  the handler answers 500 exactly as it does today, having reported no key. This is what
  keeps I2 intact, and it is the reason the write cannot simply live next to `c.NewKey()`.
- **Nothing enters the event or `applyToState`.** The key is already in the
  `ProcessInstanceActivated` event, where replay reads it. The handle is a live-only
  observation of a fact that is durable by other means, and the field needs a comment
  saying so, or the next reader will look for it on the replay path and not find it.
- **The read is safe without new synchronisation.** `Loop.Do` blocks until its closure
  finishes so that "a caller can read results out of fn by assignment", and `drive`'s
  `RunUntilIdle` runs inside one such closure. The handler reads its cell after `drive`
  returns.
- **A closing loop reports nothing.** `Loop.Do` documents that its closure does not run
  when the loop is shutting down and that callers must read the zero value as "not
  produced". The handler omits the field rather than answering with key 0.
- **Scope:** the authenticated create endpoint, and the CSV start
  ([ADR-0084](0084-csv-batch-validation.md)), which also creates exactly one instance.
  `createInstanceResp` and `csvInstanceResp` each gain `instanceKey`. The public link and
  the triggered starts are untouched.

### Consequences

- **Positive:** a client stops guessing. The four recipes under `api/web` collapse into
  reading one field, and the concurrent-start collision disappears rather than being made
  rarer. Anything written against the endpoint in future gets the answer without having to
  know that an instance listing is capped, or how it is ordered — which is knowledge no
  client should need and which nothing but a test currently supplies.
- **Negative / trade-offs accepted:** `Command` grows by one pointer for every queued
  command, hot path included. That is eight bytes on a struct that already carries
  three slices, two strings and a pointer, and one small allocation per API start.
  Creation is explicitly not the hot path, which is the same argument that admits
  `StartVars` and `Decision` on the same struct, but it is an argument and not a
  measurement. A live-only field is also a new category in a struct whose other
  non-hot-path fields are all frozen into events, and it will read as an omission to
  anyone who does not find the comment.
- **Follow-ups / risks to watch:**
  - The at-most-once gap stays. An instance created in a batch that synced, in a process
    that dies before the response is written, exists with its key lost. Every synchronous
    start has that gap today and this does not close it, which is why the scoped-listing
    recovery path stays documented rather than deleted.
  - `POST /api/v1/messages` and the timer and signal starts are left alone. A published
    message may start zero instances or many, so one key is the wrong shape for that
    answer and it needs its own record rather than a field bolted onto this one.
  - The hot-path cost is asserted from the struct's existing shape. A processor benchmark
    with `-benchmem` should confirm there is no new per-command allocation before this
    lands (the I1 checklist item in
    [invariants.md](../architecture/invariants.md)).

## Pros and cons of the options

### Option 1 — return the key, correlated per command
- Good: answers with the identity, so no client can be told about somebody else's
  instance.
- Good: every seam it needs exists. The loop already returns values by assignment, the
  processor already has a post-fsync phase, and creation is already a non-hot-path intent
  carrying per-command payload.
- Good: additive on the wire, so no client breaks.
- Bad: touches the engine's command struct and the processor's batch cycle, which is the
  code with the least tolerance for a careless change in the repository.
- Bad: introduces the first field on `Command` that exists only for a live caller.

### Option 2 — read it back server-side
- Good: no engine change at all, and it removes the duplicated guesswork from four
  clients by concentrating it in one handler.
- Good: works uniformly for every start path, including the triggered ones.
- Bad: it is still the guess. The server can take its before-snapshot inside the same
  `s.do` that enqueues the command, and another handler can still enqueue a create that
  the same batch folds, leaving two new instances and no way to attribute them. Moving a
  race into the server does not resolve it, it only makes it harder to see.

### Option 3 — a caller-supplied correlation token
- Good: no engine change, and the client chooses an identifier it already has.
- Good: survives a lost response, because the token is still the client's on a retry.
- Bad: needs the token persisted on the instance to be searchable, which is a new field
  on `ProcessInstanceValue` and therefore an event-format change. That is a heavier
  commitment than the live-only cell of option 1, not a lighter one.
- Bad: `CorrelationKey` is spoken for by message correlation
  ([ADR-0020](0020-message-correlation.md)). Overloading it would make two unrelated
  things share one field, which is the mistake ADR-0203 spent a migration undoing for a
  word.
- Bad: costs a second round trip to turn the token back into a key.

### Option 4 — leave it, and document the recipe
- Good: free, and correct for a single user, which is every demo and most first
  integrations.
- Good: the scoped listing and the point read of ADR-0241 make the recipe cheap on any
  population, so the failure that prompted this record is already fixed.
- Bad: leaves a race in the one operation clients start with, and leaves every future
  client to rediscover a recipe whose correctness depends on knowing that an instance
  listing is ordered oldest-key-first and capped.

### Option 5 — an await or subscribe surface
- Good: general. It would answer this question and several others.
- Bad: the HTTP API has no such surface, and inventing one to return a single scalar is
  an order of magnitude more machinery than the question needs.

## Links

- builds on [ADR-0241](0241-finding-an-instance.md) — a key is a point read, a definition
  has an index; this record is about how a caller comes to hold a key at all
- rests on [ADR-0002](0002-single-writer-partition-model.md) and
  [ADR-0005](0005-group-commit-and-fsync-strategy.md) — where the key is minted, and when
  it may be spoken about
- constrained by [ADR-0001](0001-event-sourcing-and-log-structured-state.md) — the key is
  a fact in an event, and replay reads it rather than producing it
- affects the clients of [ADR-0204](0204-hosted-apps-on-an-isolated-origin.md)
- deliberately does not change [ADR-0029](0029-public-process-start-links.md)
- tracked as [issue #933](https://github.com/pblumer/atlas/issues/933)
