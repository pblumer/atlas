# ADR-0503: A customer's reply finds the process that wrote to them

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-27
- **Deciders:** Atlas maintainers
- **Open question:** whether each provider keeps the `Message-ID` Atlas writes. SMTP
  submission servers normally keep a client's `Message-ID`; whether Gmail's
  `messages.send` keeps it on a raw message in every case, whether Microsoft Graph's
  `sendMail` keeps it when the message is submitted as MIME, and whether Graph answers
  `internetMessageHeaders` (and so `In-Reply-To`) on a *list* of messages is written from
  the providers' documentation and from reports, not from a send at a live tenant. The
  record does not rest on it alone — the plus address and the subject tag carry the same
  token through a provider that rewrites the `Message-ID` — but the claim that the
  invisible carrier works on every provider is unverified until somebody sends through
  each one and reads the reply.
- **Question checked:** 2026-10

## Context and problem statement

A process writes to a customer — a request for a missing document, a confirmation to
approve, a question about an order — and the customer answers by mail. The answer has to
reach the instance that asked. Today that is the modeler's problem, and Atlas gives them
almost nothing to solve it with:

- **What leaves.** A mail task frames its message with `Message-ID: <{jobKey}@atlas>`
  (`connector/mail/client.go`) on SMTP and Gmail. Microsoft Graph sends a JSON message
  to `sendMail`, which assigns its own `internetMessageId` and answers 202 with no body,
  so Atlas never learns it. A send answers no result, so the instance does not know what
  it sent either.
- **What arrives.** A mail watch ([ADR-0438](0438-mailbox-worker.md)) publishes an
  envelope that carries `internetMessageId` but neither `In-Reply-To` nor `References`;
  the latter is read for the `reply` operation and kept out of the envelope on purpose.
  A watch's correlation key therefore has nothing to correlate a reply on except the
  sender's address or something the modeler put in the subject and parses back with FEEL.
- **What is lost.** A watch publishes without a time to live, so a reply that arrives
  while the instance is not at its catch event — it is still running towards it, a timer
  already took the other branch, the customer answered twice — reaches nobody
  ([ADR-0020](0020-message-correlation.md)). The mail stays in the mailbox, because a
  watch never changes it, but no instance and no person learns of it. The event log
  ([ADR-0472](0472-event-log.md)) shows a `message.sent` that reached nobody, readable by
  operators only.

Two smaller facts belong here because the decision fixes them on the way. The right-hand
side `@atlas` is not a domain, so two installations — or one installation after a reset
— produce the same `Message-ID` for different mails, and a receiving mailbox that
deduplicates on it may drop the second. And a job key is a counter: a `Message-ID` built
from it alone could be guessed and written into a forged `In-Reply-To`.

The question: **how does a reply carry, through the customer's mail client, enough to
say which instance (and which mail) it answers — in a form a stranger cannot forge — and
what happens to a reply that is correctly addressed but arrives when nothing waits for
it?**

## Decision drivers

- **Only what the customer's client copies survives.** A reply is a new message the
  customer's client writes. It copies the original's `Message-ID` into `In-Reply-To` and
  `References` (RFC 5322 §3.6.4), the subject with a prefix, and the address it answers
  to (`Reply-To`, else `From`). Nothing else of the original comes back.
- **Every provider equally.** SMTP/IMAP, Gmail and Graph are first-class; a mechanism
  that works on two of them is not the mechanism.
- **Visible carriers are acceptable** where the invisible one is not enough.
- **A token is a capability.** Whoever holds it can put a message into a process. It
  must be unguessable and verifiable without a lookup, and a reply without a valid one
  is a stranger's mail, however it is dressed.
- **Least data in what waits** ([ADR-0370](0370-durable-message-buffer.md), amended
  2026-10-13): a message that may be kept carries no personal data.
- **Invariants.** Framing, verification and provider calls are worker and bridge work,
  off the processor goroutine (I3), never in `applyToState` (I4). The token is a pure
  function of values already on the log, so a retried send writes the same one.

## Considered options

For the carrier:

1. **A custom header** (`X-Atlas-Instance`) on the outgoing mail.
2. **Leave it to the model:** a subject tag the modeler writes and parses back with FEEL.
3. **The provider's conversation:** Graph's `conversationId`, Gmail's `threadId`.
4. **A signed token in the standard carriers**, written by the mail Worker and verified
   by the mail watch: the `Message-ID` always, a plus address in `Reply-To` and a subject
   tag where configured.

For a reply nothing waits for:

- **A.** Keep today's behaviour: it reaches nobody.
- **B.** Buffer it with the whole envelope, as ADR-0370 buffers a published message.
- **C.** Buffer a reference to it, for a time the watch states, and let the event log
  show what expired.

For a reply that never finds its instance:

- **a.** A log line.
- **b.** An incident.
- **c.** The event log, readable by the mailbox's people, and a modelled fallback as a
  follow-up.

## Decision outcome

Chosen: **4**, **C** and **c**.

### The token

```
token = base32lower( processInstanceKey[8] || ref[8] || mac[10] )      42 characters
mac   = HMAC-SHA256( K_reply, processInstanceKey || ref )[:10]
```

- `processInstanceKey` is the sending instance. `ref` names the conversation: by default
  the sending task's job key, so every mail is its own conversation; a task may continue
  an earlier one (below).
- `K_reply` is derived from a key the vault holds ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md)), for this purpose alone.
  Verification tries the current key and the retired ones still in the ring, so a
  rotation does not orphan every open conversation at once. A token signed under a key
  that has left the ring reads as no token.
- Lower-case base32 because a mail system may change the case of a local part, and
  because the token then fits every carrier unchanged.
- 80 bits of MAC: a stranger guessing one has to send mail, which the watch's budget
  ([ADR-0225](0225-inbound-watch-budget.md)) meters, so an online guess is out of reach.

The token is computed in `Resolve` — on the engine's side of the split ADR-0168 drew,
where the vault is — and travels to a Worker Instance as a finished string like the rest
of the resolved task. It is a pure function of the key ring, the instance key and the job
key, so the at-least-once retry ADR-0079 accepts writes the same headers.

### The carriers

| Carrier | Written by | Survives |
|---|---|---|
| `Message-ID: <atlas.{jobKey32}.{token}@{sender's domain}>` | every send, reply and forward | an ordinary reply in every common client |
| `Reply-To: {local}+{token}@{domain}` (RFC 5233 subaddress) | a Worker configured with a reply address | a client that drops `In-Reply-To`; a reply sent from a different mailbox |
| `[#{token}]` in the subject | a task that asks for it | a customer who writes a new mail and copies the subject |

- The `Message-ID` carries the job key beside the token, so two mails of one
  conversation still have distinct ids, and the sender's domain, so two installations
  never write the same one.
- The plus address is configured on the Worker, because whether it works is a property
  of the receiving server: Gmail and Google Workspace accept it, Exchange Online by its
  documentation does by default, an on-premises Exchange Server to the maintainers'
  knowledge does not, and other servers need a recipient delimiter configured. The
  delimiter is the Worker's setting, `+` by default, because not every server uses `+`.
  The Worker refuses a reply address whose local part leaves no room for the token in
  the 64 octets RFC 5321 §4.5.3.1.1 allows. The address may name another mailbox than the
  sender's: a shared `noreply@` sender (ADR-0438's recommendation) can route replies to a
  reply mailbox that a separate Worker watches, because every Worker of the installation
  verifies under the same ring.
- The subject tag is opt-in per task, because it is the one carrier the customer sees
  and may edit.
- The body is not a routing carrier: quoting, HTML rewriting and mobile clients make it
  the least reliable and the most visible. The reference line a task writes into the body
  when it asks for a signed answer binds the signature to the request
  ([ADR-0504](0504-what-a-mail-reply-proves.md)); the watch
  does not route on it.

**Graph** sends through `sendMail` with the message as MIME, framed by the same
`buildRFC822` SMTP and Gmail use, so the three providers write one message and Graph
gains the `Message-ID` idempotency the others have. If a tenant shows that Exchange
rewrites the id on that path, the alternative is a draft created with `internetMessageId`
and then sent — which needs `Mail.ReadWrite` instead of `Mail.Send`, and is therefore not
the default. Graph's `reply` operation moves to the same MIME path, so a reply Atlas
writes carries a token too.

### What a task gains

- A send, reply or forward with a `resultVariable` answers
  `{messageId, replyRef}`: the `Message-ID` it wrote and the conversation reference as a
  decimal string.
- `replyRef` (FEEL, optional) continues a conversation: a reminder written with
  `replyRef="= anfrage.replyRef"` carries the first mail's reference, so a reply to
  either mail correlates the same way. A value that is not a reference a mail task
  answered fails the job with that sentence.
- `replyTag="subject"` adds the subject carrier.

### What a watch gains

The mail Worker reads `In-Reply-To` on all three providers. The watch looks for a token
in order — `In-Reply-To`, then `References` newest first, then the `To` and `Cc`
addresses, then the subject — and takes the **first that verifies**. A token that does
not verify is ignored, never trusted, and the search goes on.

The envelope gains one object:

```
mail.reply = { ref, processInstanceKey, via }    via: in-reply-to | references | address | subject
```

absent when no carrier held a valid token. `ref` and `processInstanceKey` are decimal
strings, the form the `processInstanceKey` built-in already takes so that a 64-bit key
survives FEEL (`engine/behavior.go`). A watch states which mail it publishes:

| `replies` | publishes |
|---|---|
| `all` (default) | every admitted message, as today |
| `only` | messages with a valid token; the rest are consumed, not published, as a refused sender is |
| `exclude` | messages without one — the watch that starts a process for a new request |

So one mailbox carries two watches: one routing replies to the instances waiting for
them, one starting a process for everything else. A forged or stale token lands in the
second, which is what any stranger's mail does.

Two correlation patterns follow, and the handbook shows both:

- **Per instance:** watch key `= mail.reply.processInstanceKey`, catch event
  `= processInstanceKey` (a built-in name). The simplest wiring, right while an instance
  waits for one reply at a time.
- **Per conversation:** watch key `= mail.reply.ref`, catch event
  `= anfrage.replyRef`. Required where an instance waits for several replies at once — a
  multi-instance activity asking five approvers — because ADR-0020 delivers a message to
  every matching subscription, and an instance-wide key would complete all five with the
  first answer.

Sender rules (`allowedSenders`, `requireDmarcPass`) still apply first. A token proves
that a reply answers an Atlas mail, not who wrote it: a customer who forwards the mail
hands the token on. What a reply proves about its sender, and what a decision may
therefore rest on, is [ADR-0504](0504-what-a-mail-reply-proves.md).

### A reply that arrives when nothing waits: it waits, as a reference

A reply watch may state `replyWaitTime`, bounded by `--message-ttl-max` (ADR-0370's
seven days by default). With it, the bridge publishes with that time to live, and a reply
nothing waits for is buffered and handed to the subscription that opens for its key —
the "too early" case, and a second reply that the instance's next wait can take.

What waits is a **reference, not the mail**:

```
mail = { messageId, internetMessageId, folder, receivedAt, reply }
```

ADR-0370 refuses a buffered payload that carries personal data, because nothing on the
message path can seal it while it waits. A mail's sender, recipients, subject and body
are personal data whether or not a model declares `mail` personal, so a watch with a wait
time publishes this reduced shape — **every time, delivered at once or after waiting**,
so a model sees one shape and does not depend on timing. The process reads the message
with the mailbox `get` operation when it needs it, which also answers from the mailbox as
it is now rather than as it was when the reply arrived. A watch without a wait time
publishes the full envelope, as today.

`PublishInbound` gains the time to live; the buffer, its expiry and its rows in the event
log are ADR-0370's and ADR-0472's, unchanged.

### A reply that never finds its instance: the event log, not an incident

A reply whose wait time ends untaken expires as `message.expired`; one published without
a wait time reached nobody as `message.sent` with zero reached. Both are rows in the event
log already, keyed by the reference, which names the instance and the conversation. Two
changes make that the place to look:

- The dispatch records which watch published it (an appended field, zero on older
  records), and a row from a watch is readable by the **viewers of the watch's Worker** as
  well as by operators — the mailbox's people, who are the ones who can answer the
  customer ([ADR-0205](0205-connector-ownership-and-event-delivery.md)).
- The watch's view in the Console lists its replies that expired or reached nobody, with
  the reference and the instance.

A token that did not verify is counted per watch and logged as
`mail.reply_token_rejected` with the Worker, the watch and the provider's message id —
never the sender or the subject.

**An incident is not raised.** Atlas's incident is a token that cannot move, attached to
the element it stands on, and resolving it runs that element again
([ADR-0061](0061-incident-model.md)). An unmatched reply has no token standing anywhere:
the instance may be past its wait or finished, and "run the element again" would at best
do nothing and at worst send the original mail a second time. Incidents are also read by
every operator, so a customer's reply would leave the mailbox's ownership on its way into
one, and a mechanism that lets a stranger's mail open incidents is the operations-view
flood ADR-0337 and ADR-0370 (amended 2026-09-17) name.

What an incident would buy — a person who must act — belongs in the model. The follow-up
is a **fallback message**: a watch names a second message name and key (typically
`= mail.reply.processInstanceKey`), and a reply that expires untaken is published under
it, so an event subprocess in the instance or a central triage process opens a user task
for the people of that case. That is an engine change of its own — the expiry has to
publish, deterministically, from the processor — and it gets its own record.

### What this does not protect

- **The customer's side.** A token is visible to whoever reads the mail, including
  anyone the customer forwards it to.
- **A reply without any carrier.** A customer who writes a new mail with a new subject to
  the main address is a new request. The `exclude` watch catches it; matching it to a
  conversation is a person's job.
- **A provider that rewrites every carrier.** The open question above.

### Consequences

- **Positive:**
  - A modeler correlates a reply with one key on the watch and one on the catch event,
    on every provider, without parsing headers in FEEL.
  - A reply that comes early or twice waits instead of vanishing, and one that never
    finds its instance is visible to the people who own the mailbox.
  - The `Message-ID` becomes globally unique and unguessable, and Graph gains the
    idempotency SMTP and Gmail have.
- **Negative / trade-offs accepted:**
  - A watch with a wait time hands the process a reference, so the model reads the mail
    with a `get` task.
  - A key leaving the ring orphans the replies still signed under it.
  - The plus address and the subject tag are visible to the customer.
  - Graph sends as MIME, which changes how a Graph-sent mail is framed: one body choice
    becomes `multipart/alternative`, as SMTP and Gmail already send it.
  - A send in flight across the upgrade can be retried under the new `Message-ID`, so a
    receiving de-duplicator would not recognise it as the same mail.
- **Follow-ups / risks to watch:**
  - The fallback message on expiry (own record).
  - An atlas hub Blueprint ([ADR-0499](0499-blueprint-registry-and-distribution.md))
    carrying the pattern pre-wired — reply watch, catch event, timer, reminder — so an
    installation without modelling experience starts from it.
  - Bounces: a VERP return path carrying the token would route a non-delivery report to
    the instance whose mail bounced. Not decided here; SMTP can set it, Graph and Gmail
    cannot.
  - Verify the open question at a live tenant for each provider before the handbook
    promises the invisible carrier.

### Delivery in slices

1. **Inbound:** `In-Reply-To` on all three mailboxes, token verification, `mail.reply`,
   the `replies` filter.
2. **Outbound:** the token `Message-ID` on SMTP and Gmail sends, replies and forwards;
   `resultVariable` on send; `replyRef`.
3. **Graph:** MIME `sendMail` and the MIME reply, verified at a tenant.
4. **Visible carriers:** the Worker's reply address, the task's subject tag.
5. **Waiting:** `PublishInbound` with a time to live, the reference shape,
   `replyWaitTime`.
6. **Visibility:** the watch on the dispatch, the event-log read rule, the watch view.

Each slice ships on HTTP, MCP and in the handbook, as AGENTS.md asks; slices 1 and 2
together are the first that is useful.

## Pros and cons of the options

### Option 1 — a custom header
- Good: invisible, unambiguous, trivial to write.
- Bad: no client copies it into a reply, so it never comes back. It solves the problem
  only for mail Atlas itself sends in reply.

### Option 2 — leave it to the model
- Good: works today for SMTP and Gmail; no change to the Worker.
- Bad: every modeler invents a tag and its parser; nothing verifies it, so anyone who
  can guess an order number can answer for the customer; the `Message-ID`, the most
  reliable carrier, stays unused.

### Option 3 — the provider's conversation
- Good: no token at all; the provider groups the thread.
- Bad: IMAP has none; it works only when the sending and the watched mailbox are the
  same; Graph and Gmail group by rules of their own (Gmail also by subject) that Atlas
  cannot state or test.

### Option 4 — a signed token in the standard carriers (chosen)
- Good: every common client returns at least one carrier; the token verifies without a
  lookup and cannot be guessed; one mechanism on three providers.
- Bad: a key to keep and rotate; visible carriers where they are switched on; Graph has
  to move to MIME.

### A — keep today's behaviour
- Good: no change.
- Bad: a reply that comes a second early is lost to the process, silently.

### B — buffer the whole envelope
- Good: the process receives the mail as it does today, with no `get` task.
- Bad: a customer's mail waits in the buffer, readable and beyond erasure, which is
  exactly what ADR-0370 refuses; a model that declares `mail` personal would make every
  buffered publish fail.

### C — buffer a reference (chosen)
- Good: nothing personal waits; one shape whatever the timing; the mailbox stays the
  store of the mail.
- Bad: one more task in the model.

### a — a log line
- Good: cheap.
- Bad: read by whoever reads the server log, not by the mailbox's people; nobody acts on
  it.

### b — an incident
- Good: where operators look, with a resolve workflow.
- Bad: no token stands anywhere to resolve; resolving would re-run an element; every
  operator would read the reply; a stranger's mail could flood the view.

### c — the event log, readable by the mailbox's people, and a modelled fallback (chosen)
- Good: uses records that exist; the people who can answer the customer see it; the
  follow-up puts a responsible person in the model.
- Bad: until the fallback lands, seeing an unmatched reply needs someone to look.

## Links

- extends [ADR-0438](0438-mailbox-worker.md) (the mail watch and the `reply` operation)
  and [ADR-0079](0079-outbound-mail-connector.md) / [ADR-0093](0093-native-mail-providers.md)
  (the sending half)
- waits in [ADR-0370](0370-durable-message-buffer.md)'s buffer, under its rule on
  personal data
- shows in [ADR-0472](0472-event-log.md)'s event log, with a wider read rule for watch rows
- correlates as [ADR-0020](0020-message-correlation.md) decided, including its fan-out
- the key lives in the vault: [ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md)
- why not an incident: [ADR-0061](0061-incident-model.md), [ADR-0337](0337-incident-floods.md)
