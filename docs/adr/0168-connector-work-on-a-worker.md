# ADR-0168: Moving a connector onto a worker — where the task detail travels, and where the credential lives

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0164](0164-no-in-process-service-tasks.md) decided that every side-effecting
service task belongs on a worker, and reached it by deprecation because of one
concrete gap: **a connector task cannot run on a worker yet.** This record is about
closing that gap, and it turns out to hold two questions rather than one.

An in-process connector handler does four things, and only the first two are the
"detail travels with the job" problem [ADR-0157](0157-worker-processes-supervision-and-console.md)
named:

1. **Find the task's configuration.** `reader.GetElementInstance(job.ElementInstanceKey)`
   → the compiled process via a `ProcessLookup` → `cp.ConnectorTaskOf(elementId)` →
   the model-authored detail: which connector, and the literal-or-FEEL values for
   recipients, subject, URL, and the rest.
2. **Read the variables** those FEEL values evaluate against.
3. **Resolve the connector's client** — `registry.Client(name)` — the endpoint and
   the credential an operator registered on the *server* (ADR-0041), backed by the
   engine's encrypted vault (ADR-0069/0070) or its environment.
4. **Make the call.**

An external worker already gets (2): the type-keyed pull returns the variables
visible at the task. (1) is model data — static, deploy-time, and portable; it can
ride on the job with no more than encoding work.

**(3) is the actual decision.** A credential in the server's vault is not something
a job payload may carry, and a worker in another network cannot reach the vault.
So "move connectors onto workers" is not a plumbing task: it is a question about
where integration credentials live.

That question is worth deciding on its own, because the answer shapes the product's
security posture and its operational story far more than the encoding of a task
detail does.

## Decision drivers

- **A credential should live where it is used.** Every hop it takes is a place it
  can leak, be logged, or be cached.
- **ADR-0041's promise.** A secret is registered once, by an operator, and never
  appears in a model. Whatever changes, that must survive.
- **The single-node install must stay simple.** ADR-0011: one binary, one command.
  An answer that makes every small installation manage a second secret store has
  bought nothing.
- **The worker is not necessarily trusted the same way.** The point of a worker in
  the customer's network is that it reaches systems the engine cannot; that is not
  the same as it being allowed everything the engine is.
- **Don't strand the existing catalog.** Twelve connector kinds work today. An
  answer they cannot all follow is not an answer.

## Considered options

1. **The worker fetches the connector's configuration from the server**, including
   the resolved credential, over the authenticated API — the engine remains the
   registry and the vault, and hands out what a leased job needs.
2. **The worker owns its own connector configuration.** The model names a connector
   (`orders-mail`); the *worker* configured for that name knows the endpoint and the
   credential. The engine stops holding integration credentials.
3. **Split by kind.** Kinds whose configuration carries no credential (CSV import,
   web scraping against a public URL) move first and prove the detail-on-the-job
   mechanism; credential-bearing kinds wait for a decision between 1 and 2.
4. **Leave connectors in-process indefinitely**, and let ADR-0164's rule apply only
   to model-authored job types.

## Decision outcome

Chosen: **option 2 — the worker owns its connector configuration**, reached through
option 3's first slice.

**The split.** The engine decides *what* to send; the worker knows *where* to send
it and with what credential.

That line falls in a place that was already half-drawn. A connector handler's first
half is engine work and has to stay engine work: finding the task's detail in the
compiled process and evaluating its FEEL values against the instance's variables.
FEEL is compiled at deploy (ADR-0008/0015) and a worker has neither the compiled
process nor the variables' scope chain. So the engine **resolves the detail into
plain values** and those travel with the leased job — not the expressions, which
would make every worker carry a FEEL engine and a copy of the compiled process.

The handler's second half — hold the endpoint and the credential, make the call — is
what moves. The worker is configured for a connector *name*; the model names that
name and nothing else, exactly as it does today (ADR-0041: a model never carries a
secret). What changes is which process holds the value behind the name.

**Why this way.** A credential that lives where it is used crosses no boundary at
all. A worker exists precisely because it sits where the integration is — in the
customer's network, next to the mail relay or the ERP — and that is also where the
people who own that integration are. Moving the value there removes the engine from
the set of things worth attacking for someone else's credentials, and it removes the
network hop that option 1 would have added to every lease.

**What it costs, and what recovers it.** Connector configuration leaves the Console
for the kinds that move. That is a real loss and it needs an answer rather than a
shrug: a worker should report the connector names it is configured for when it
announces itself, so the Workers view can show which names are served, by whom, and
which are configured nowhere — the same "who is doing this work" question that view
already answers for job types. Configuring a *supervised* worker stays a matter of
the server's own command line, so the single-node install keeps its one-command
story.

**Sequenced through option 3.** The first slice is a kind with no credential at all,
so the mechanism — a resolved detail travelling on the job — is built and reviewed
before any security decision rides on it. Then one credential-bearing kind end to
end, then the rest. A kind that has not moved keeps its in-process handler and its
deprecation notice.

**A prerequisite this exposes.** The type-keyed pull refuses a job type an
in-process worker is registered for, which is what keeps work from being done twice.
So a kind cannot move until its in-process handler can be turned off — ADR-0157's
per-kind switch, listed there as a follow-up and never built. It comes first.

### Consequences

- **Positive:** ADR-0164 stops being aspirational for the kinds most operators use.
  Integration credentials leave the engine, so a compromised engine no longer yields
  them. The engine keeps FEEL and the compiled process — the things only it can have
  — and the worker gets a payload it can act on with no engine concepts in it.
- **Negative / trade-offs accepted:** connector configuration leaves the Console for
  every kind that moves, and an operator now configures two things unless the worker
  is supervised. A resolved payload is larger on the wire than a reference would be.
  And the resolution happens at lease time rather than at call time, so a variable
  changed in between is not seen — the same instant the in-process handler already
  read, moved earlier by the length of one lease.
- **Follow-ups / risks to watch:** the per-kind in-process switch, which everything
  else waits on; a worker reporting its configured connector names so the Workers
  view can show which are served and which are configured nowhere; and the order the
  kinds move in, cheapest and least secret first.

## Pros and cons of the options

### Option 1 — the worker fetches the credential from the server
- Good: the Console stays the one place connectors are configured; no operator
  story changes; every kind can follow at once.
- Bad: credentials cross the network to every worker that leases a job; the engine
  becomes a credential distribution point; the blast radius of a compromised worker
  grows from "one integration" to "whatever it could fetch".

### Option 2 — the worker owns its connector configuration
- Good: the credential lives where it is used and crosses nothing; a worker in the
  customer's network is configured by the people who own that network; the engine
  stops being a place worth attacking for integration secrets.
- Bad: connector configuration leaves the Console; two places to configure things
  in a single-node install unless the supervised case is given a shortcut.

### Option 3 — credential-free kinds first
- Good: proves the mechanism with no security decision attached; CSV import is
  worth moving on its own merits.
- Bad: partial; the kinds most operators use are the credential-bearing ones.

### Option 4 — leave connectors in-process
- Good: nothing to build.
- Bad: makes ADR-0164 a rule about a minority of service tasks.

## Links

- closes the gap [ADR-0164](0164-no-in-process-service-tasks.md) recorded, and which [ADR-0157](0157-worker-processes-supervision-and-console.md) named as "the connector detail travelling with the job"
- constrained by [ADR-0041](0041-connector-management-and-secret-store.md) (a secret is registered by an operator and never appears in a model) and [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)/[ADR-0070](0070-vault-on-by-default-with-generated-key.md) (where it is kept)
- the worker protocol that would carry any of this is [ADR-0007](0007-job-worker-protocol.md)
- the single-binary posture that limits how much operator machinery any answer may add is [ADR-0011](0011-single-binary-distribution-and-web-ui.md)
