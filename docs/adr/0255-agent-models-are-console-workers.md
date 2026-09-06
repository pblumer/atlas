# ADR-0255: An agent model is a Console Worker — the one field that is not a secret

- **Status:** Proposed
- **Date:** 2026-09-06
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0254](0254-agent-rounds-on-a-worker.md) shipped the agent Worker Type and configured it
the way an operator configures any external worker: from `ATLAS_AGENT_CONNECTORS` and
`ATLAS_AGENT_<NAME>_*` in that worker's own environment. That works, and it is what a
hand-started `atlas worker --handle agent` will always read.

It leaves two things that every other credentialed kind stopped having to live with.

**Adding a model is a deployment change.** Every other kind that needs a credential —
mail, Remedy, Jira, SharePoint, temis, clio, Entra, AD, the three SQL products — is a
**Console entry** (ADR-0041/0203): an operator adds a record, names a vault key, and the
supervised worker picks it up on the next refresh, with no restart and no start
parameter. An agent model today is an environment variable on the Atlas process, so
adding one means editing a deployment and restarting the engine to change nothing about
the engine.

**Atlas cannot supervise an agent worker at all.** `DefaultSupervisedWorkerOnlyKinds()`
is guarded by `TestEveryDefaultSupervisedWorkerOnlyKindCanBeServed`: a kind Atlas always
supervises must be a managed kind, marked worker-only, **and provisioned by the engine** —
otherwise the default would run a worker with nothing to serve, forever. The agent kind
is none of those, so it cannot be defaulted, and every installation that wants an agent
has to know to pass `--supervise-connectors agent`.

The question this record answers is not *whether* an agent model becomes a Console
Worker — the argument above is the same one every kind before it made. It is **what an
agent Worker's record holds**, because an agent is the first kind whose configuration
has a part that is neither an endpoint, nor a credential, nor derivable from either: the
**model name**.

## Decision drivers

- **One vocabulary for configuring a Worker.** ADR-0203 named the three things — Worker
  Type, Worker, Worker Instance — and every kind since has been an instance of that
  shape. An agent that needed a different one would be the exception a reader has to
  learn separately.
- **A record holds a *reference* to secret material, never the value (I6).** The API key
  is a vault key like every other credential; nothing about an agent changes that.
- **What is not secret must stay visible.** An operator's most common change to an agent
  Worker is which model it runs — a cost decision and a capability decision at once. It
  has to be readable and editable in the Console, not hidden behind a vault key.
- **The supervised worker and a hand-started one read the same variables.** There is no
  private channel between Atlas and its own child (ADR-0157): rendering an environment
  is the operator's own setup done by the program, and a variable only the supervised
  path used would be the one nobody tests.
- **A misconfiguration is refused where the operator is looking.** The worker already
  refuses an unusable model at startup (ADR-0254). A record the Console accepts and the
  worker then rejects would move that discovery to a log nobody is reading.

## Considered options

1. **Leave it in the environment.** No record, no Console entry; `--supervise-connectors
   agent` and `ATLAS_AGENT_*` stay the only way.
2. **A managed kind with the model in the vault bundle**, as Entra puts three values
   behind one `credentialsRef`.
3. **A managed kind with the model as a record field**, the credential a plain vault
   secret as temis's token is.

## Decision outcome

Chosen option: **"a managed kind with the model as a record field"**.

The agent becomes the fifth **worker-only** managed kind, on Entra's and AD's shape
exactly: no registry, no rebuild, no in-process handler — ADR-0164 forbids a round in
the engine process and nothing here softens that — and a store record whose only purpose
is that an operator can add a model in the Console and the supervised worker be
provisioned from it.

Its record reads:

| record field | holds | why |
| --- | --- | --- |
| `Endpoint` | the model endpoint, optional | defaults per protocol; an operator overrides it for a gateway, a proxy or a self-hosted deployment |
| `CredentialsRef` | a vault key holding the **API key** | one value, so a plain secret like temis's token rather than a bundle |
| `Provider` | the wire format: `messages` or `chat-completions` | the field mail already uses to pick a transport, asked of the one other kind that has dialects |
| `Model` | the model name | **new** |

**`Provider` carries the wire format** rather than a second field meaning the same
thing. Mail's `Provider` selects between SMTP, Gmail and Microsoft Graph — three ways of
saying "send this" that differ in their protocol and nothing else. An agent's choice
between the Messages API and Chat Completions is the same question about the same kind
of answer, and giving it its own field would leave two record fields that a reader has
to be told are not related.

**The model name is a record field, not a bundle entry.** It is not a secret, and the
vault's contract is that what goes in does not come back out for display — which is
right for a key and wrong for the single most-changed piece of an agent's configuration.
An operator comparing cost against capability changes the model, and they must be able
to see what it is set to without opening a secret store. `Model` is `omitempty`, so a
record written before this carries none and reads as "the protocol's default" — which is
exactly what an unconfigured Messages worker already means.

**Validation refuses at the Console what the worker would refuse at startup.** A key is
required unless an endpoint is named (a self-hosted endpoint may legitimately need
none); `chat-completions` requires a model, because that adapter deliberately has no
default — which account may use which model is not something Atlas can know
(ADR-0254). The rules are the worker's own, stated once more where the operator is.

With that, `agent` joins `DefaultSupervisedWorkerOnlyKinds()`: Atlas starts an agent
worker of its own accord, it parks with nothing to serve, and the moment an operator
saves a model in the Console it comes up — the tenant a Console entry rather than a
deployment change, which is ADR-0172's sentence applied to a fifth kind.

### Consequences

- **Positive:** an agent model is configured like everything else in the product, by
  someone who is already in the Console adding workers; the credential is in the vault
  where it belongs; a model change needs no restart; and an installation that wants an
  agent needs no start parameter. The environment path is untouched — a hand-started
  worker reads exactly the variables it read before, because the supervised one is
  handed those same variables.
- **Negative / trade-offs accepted:** the durable record grows a field used by one kind,
  which `Provider` and `Sender` already established but which is not free: the record is
  shared, and a reader now has three kind-specific fields to place. The Console dialog
  grows an arm and its first free-text field that is neither endpoint nor credential.
- **Follow-ups / risks to watch:** the finer knobs ADR-0254 exposes to the environment —
  `_AUTH`, `_THINKING`, `_MAX_TOKENS`, `_ANSWER_VARIABLE` — stay environment-only for
  now. They are for a gateway that does not behave quite like the provider it imitates,
  which is a deployment's problem rather than an authoring one; if that turns out to be
  wrong the record grows, and this record is where to say so.

## Links

- [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) — agent tool calls drive ad-hoc activation
- [ADR-0254](0254-agent-rounds-on-a-worker.md) — an agent round on a worker
- [ADR-0203](0203-worker-execution-model.md) — Worker Type / Worker / Worker Instance
- [ADR-0172](0172-entra-id-connector.md) — the worker-only kind and its supervised provisioning
- [ADR-0164](0164-no-in-process-service-tasks.md) — no in-process service tasks
- [ADR-0041](0041-connector-management-and-secret-store.md) / [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) — the credential lives in the vault
