# ADR-0258: Discord as a Worker Type — a process speaks in the channel the team already reads

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

A running process regularly has something to say to people: an approval is waiting, an
import finished, an incident was raised, a case has been sitting untouched for three
days. Atlas can already put that in an inbox — outbound mail is
[ADR-0079](0079-outbound-mail-connector.md) — and a mail is the right shape for a
notice one person must act on and keep.

It is the wrong shape for the other half. A large number of teams run their working
day in a chat channel, and for them a mail is a thing that arrives where nobody is
looking. What they want from a process is a message in the channel, a thread under it
where the discussion lives, and the ability for the process to come back and edit
what it said when the facts change — a "3 pending" that becomes "0 pending" rather
than a third mail saying so.

Discord is a concrete instance of that and the one asked for here. The question this
record answers is **whether Discord earns a Worker Type of its own, and if so, what
its authored surface is** — not whether Atlas should eventually speak every chat
protocol.

### Why the generic REST Worker is not the answer here

The generic REST Worker Type ([ADR-0067](0067-service-task-connector-catalog.md)) is
the standing answer to "an API we have not wrapped yet", and unlike the Google Sheets
case ([ADR-0235](0235-google-sheets-worker.md)) it is not *structurally* excluded.
Discord authenticates a bot with one static header, `Authorization: Bot <token>`, and
the REST Worker's `apikey` scheme sends an arbitrary header name with the raw resolved
secret. A model author can reach Discord through it today, and this record does not
take that away.

What the REST Worker cannot do is make it a *step a process takes*. Through it, sending
a message is a model that authors a full URL with a channel id interpolated into its
path, a hand-built JSON body, and a secret whose value has to be the literal string
`Bot ` followed by a token — a detail that lives nowhere except in whoever set the
vault entry. Nothing checks at deploy that the body has a `content`, nothing knows that
editing a message needs a message id, and a channel id typed into the wrong half of a
URL is discovered by a token parking on a 404. The cost is not that the call is
impossible; it is that every model repeats an integration nobody validated.

That is the same argument the Jira Worker Type made
([ADR-0201](0201-jira-connector.md)), and it is the one this record makes. What earns a
Worker Type is a capability whose *operations* are worth naming.

### Why this record is outbound only

The complementary direction — a message in a channel starts a process — is deliberately
out of scope, and not for lack of interest. Atlas's inbound bridge
([ADR-0075](0075-clio-inbound-event-bridge.md),
[ADR-0214](0214-jira-inbound-issue-watch.md),
[ADR-0234](0234-google-inbound-watch.md)) is built around *polled* sources whose
deliveries carry a monotonically increasing `SourceSeq`, because the engine's
idempotency guard is one scalar high-water mark per source.

Discord's real-time API is the Gateway: a persistent WebSocket with a heartbeat, a
session resume and its own sequence numbering. It is not a poll, it does not fit the
bridge's shape, and forcing it in would mean either a second inbound mechanism or a
long-lived connection owned by something that is not the engine. That is a decision
worth its own record.

Notably, the *polled* form of the same question does fit: Discord message ids are
snowflakes, which are monotonically increasing by construction, and
`GET /channels/{id}/messages?after=<snowflake>` is exactly the shape `SourceSeq`
wants. This record therefore includes `list-messages` with an `after`, which is both
useful on its own and the operation a future inbound watch would be built on. It does
not build that watch.

## Decision drivers

- **The existing seam, not a new one.** [ADR-0203](0203-worker-execution-model.md) and
  [ADR-0207](0207-worker-type-packaging.md) fix what adding a capability costs: one
  package under `connector/`, one `managedConnectorKind` entry, one reserved job type
  index. Anything beyond that is a defect in the change.
- **No credential in a model (I6).** A bot token is the whole of a bot's authority.
  It belongs in the vault, resolved server-side by Worker name, and must never reach a
  BPMN file, an event, or a variable.
- **Off the hot path (I1/I2/I3/I4).** The call is network I/O: it happens in a job
  worker, after fsync, never on the processor goroutine and never inside
  `applyToState`.
- **Compile, don't interpret (I5).** Which values an operation takes is a deploy-time
  question. A model that authors a thread name on `delete-message` is refused at
  deploy, not silently ignored at call time.
- **The authored surface is what a process does, not what Discord offers.** Discord's
  API covers guilds, roles, scheduled events, stage instances, application commands and
  voice. None of those is a step a business process takes, and the generic REST Worker
  remains the way to reach them.

## Considered options

1. **Do nothing; document the REST Worker recipe.** Ship a handbook page showing the
   `apikey` scheme with an `Authorization` header and a `Bot <token>` secret.
2. **A general "chat" Worker Type** with Discord, Slack and Teams behind one authored
   surface.
3. **A Discord Worker Type**, outbound, six named operations, bot-token credential.
4. **A Discord Worker Type with a Gateway connection**, giving both directions now.

## Decision outcome

Chosen: **option 3 — a Discord Worker Type under `connector/discord`, outbound only,
with six operations and a bot token held in the vault.**

### The operations

What earns a row is a step a process takes. Six do:

| Operation | Discord call | Why it is a step |
|---|---|---|
| `send-message` | `POST /channels/{channel}/messages` | The reason the Worker Type exists. |
| `edit-message` | `PATCH /channels/{channel}/messages/{message}` | A status a process keeps current instead of repeating. |
| `delete-message` | `DELETE /channels/{channel}/messages/{message}` | Retracting a notice that is no longer true. |
| `get-message` | `GET /channels/{channel}/messages/{message}` | Reading back what is there — reactions included. |
| `list-messages` | `GET /channels/{channel}/messages` | What a channel has said since a point. |
| `create-thread` | `POST /channels/{channel}/messages/{message}/threads` or `POST /channels/{channel}/threads` | Giving one process instance its own discussion. |

**Replying in a thread is not a seventh operation.** In Discord a thread *is* a
channel, and its id is on the object `create-thread` returns. So a reply is
`send-message` with that id as its channel, and adding a `reply-in-thread` row would
be a second name for a call the model can already make — the kind of duplication that
later disagrees with itself about which one sets `allowed_mentions`.

`create-thread` covers both of Discord's thread endpoints through one authored
difference: a task that names a message starts the thread under that message; a task
that names none starts a standalone thread in the channel. That is one operation
because it is one intent, and the two endpoints differ only in what the thread hangs
from.

### The escape hatch

A `<atlas:discordField>` list sets any further property of the request body by name,
each value keeping the JSON shape its FEEL expression had — a list stays a list, an
object stays an object. It is how a model reaches `embeds`, `allowed_mentions`,
`components` or `tts` without this record having to name every field Discord will ever
add, and it mirrors the Jira Worker's `jiraField` exactly.

Model-authored fields are merged **last**, so a model can override anything the worker
composed rather than be blocked by it.

### The credential

A Discord Worker's vault bundle is `{"botToken": "…"}`, sent as
`Authorization: Bot <token>`. There is one shape, so — unlike Jira's Cloud/Data Center
pair — there is nothing for a bundle to be ambiguous about.

The `Bot ` prefix is composed by the client, not stored. A stored value that already
carried it would make two vault entries that differ only in a prefix behave identically
in some places and not others, and an operator pasting a token out of Discord's
developer portal has no prefix in their hand.

The endpoint field stays an optional override, defaulting to
`https://discord.com/api/v10`. Discord's API base is the same for everyone; the
override exists for an operator behind a proxy, as SharePoint's and Google Sheets' do.

### At-least-once, and what `nonce` is for

Delivery is at-least-once, exactly as it is for every other Worker Type: a crash between
"Discord accepted the message" and "job completed" replays the send. Discord offers no
idempotency key, so this record does not claim one.

What it does do is send the job key as the message's `nonce`. Discord echoes a `nonce`
back on the message object, so a duplicate produced by a replay is *recognizable* as
one — by an operator reading the channel's message objects, or by a later reconciliation.
It does not suppress the duplicate, and the handbook must not say it does.

### Rate limits

Discord answers a throttled request with 429 and a `retry_after`. The worker returns
that as an error carrying the number, which leaves the job pending for the ordinary
retry-then-incident path ([ADR-0061](0061-incident-model.md)). It does not
sleep in the handler: the worker may run on the run-loop goroutine, and a handler that
waits out a rate limit there would stall the engine for every other request — which is
the whole reason for the shared call budget in `connector/nettimeout` (ADR-0149).

## Consequences

- **Positive:** a process can speak where its team is reading, with operations checked
  at deploy and a token that never leaves the vault. `list-messages` lands the read
  half a future inbound watch needs.
- **Negative / trade-offs accepted:** one more built-in Worker Type to carry, and a
  reserved job type index that can never be reused. The authored surface is
  deliberately narrower than Discord's API, so some questions still route through the
  generic REST Worker.
- **Follow-ups / risks to watch:** inbound (a polled channel watch over the existing
  bridge, using snowflakes as `SourceSeq`) is the obvious next record. Discord versions
  its API in the URL; `v10` is pinned in one constant so a bump is one edit and a test.

## Pros and cons of the options

### Option 1 — document the REST recipe
- Good: no code, no new job type index, available today.
- Bad: every model re-implements the integration; nothing is checked at deploy; the
  `Bot ` prefix lives in a vault entry where no error message can explain it.

### Option 2 — one "chat" Worker Type
- Good: one authored surface for three products.
- Bad: the products do not agree on what a message, a thread or a channel id *is*, so
  the common surface is either the intersection (too thin to be worth it) or a union
  with per-product fields — which is three Worker Types wearing one name. Atlas already
  chose the other way for mail, where SMTP, Gmail and Graph sit behind one *kind* only
  because they genuinely send the same object.

### Option 3 — Discord, outbound (chosen)
- Good: fits the seam exactly; one package, one kind entry, one job type.
- Bad: outbound only, so "a message starts a process" still has no answer.

### Option 4 — Discord with a Gateway connection
- Good: real-time inbound, which is what a chat integration eventually wants.
- Bad: a persistent WebSocket does not fit the polled inbound bridge or its scalar
  high-water guard, and would need a second inbound mechanism. That is a decision to
  take deliberately, not as a side effect of shipping outbound.

## Links

- [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker Instance
- [ADR-0207](0207-worker-type-packaging.md) — what adding a Worker Type costs
- [ADR-0201](0201-jira-connector.md) — the Worker Type this one is shaped after
- [ADR-0067](0067-service-task-connector-catalog.md) — the generic REST Worker
- [ADR-0079](0079-outbound-mail-connector.md) — the other way a process tells someone
- [ADR-0168](0168-connector-work-on-a-worker.md) — resolved values travel, credentials do not
- [ADR-0214](0214-jira-inbound-issue-watch.md) / [ADR-0234](0234-google-inbound-watch.md) — the polled inbound bridge this record does not join
- [ADR-0061](0061-incident-model.md) — retries and incidents
