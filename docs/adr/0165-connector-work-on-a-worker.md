# ADR-0165: Moving a connector onto a worker — where the task detail travels, and where the credential lives

- **Status:** Proposed
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

**Open.** This record exists to put the question, not to settle it by default, and
the work it gates should not start until it is answered — the two answers produce
materially different systems.

The recommendation, for what it is worth: **option 3 first, then option 2.**

Option 3 is the honest first slice regardless of how the credential question lands.
It builds and proves exactly one mechanism — the task detail travelling on the job —
on kinds where nothing secret is involved, so the mechanism is settled and reviewed
before the security decision rides on it. CSV import is the clearest case: it is
pure computation over a variable, needs no network and no credential, and is
currently the only connector kind that can block the engine for reasons that have
nothing to do with anyone else's host.

Option 2 is where this should land, because a credential that lives where it is used
crosses no boundary at all, and because the worker's whole reason for existing is
that it sits where the integration is. It costs an operator story — connector
configuration would move from the Console to the worker's own configuration — and
that is the part worth arguing about.

Option 1 is the smaller change and the worse posture: the engine would hand
credentials to processes over the network, on a channel whose authentication is
currently a bearer token, and every worker becomes a place a credential can be read
from. It is defensible only if the workers are always the engine's own supervised
children on the same host — which is exactly the deployment that needs this least.

Option 4 is the null answer and would make ADR-0164's rule apply to a minority of
service tasks, leaving the majority permanently in the engine — the state that
record decided against.

### Consequences

- **Positive (of deciding at all):** ADR-0164 stops being aspirational for the
  kinds most operators actually use. Whichever way it lands, the mechanism from
  option 3 is needed and can be built now.
- **Negative / trade-offs accepted:** option 2 moves connector configuration out of
  the Console, which is a real loss of a good operator experience and needs its own
  answer (a worker that reports its configured connectors to the Workers view would
  recover most of it). Option 1 would need the worker channel hardened well beyond a
  bearer token before it could be considered.
- **Follow-ups / risks to watch:** whichever is chosen, the detail-on-the-job
  encoding is shared work; the FEEL values in a connector detail are compiled
  expressions today and would have to travel in a form a worker can evaluate, which
  is its own question and may argue for evaluating them engine-side and sending
  values rather than expressions.

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
