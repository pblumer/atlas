# ADR-0192: BMC Remedy runs on a worker by default

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0164](0164-no-in-process-service-tasks.md) settled the direction — a side-effecting
service task belongs on a worker — and [ADR-0168](0168-connector-work-on-a-worker.md) made
it the *default* for the kinds a supervised worker can actually serve: csv, script and
webscrape because they need no credential, mail because the engine writes its connector's
configuration into the child's environment at spawn. [ADR-0182](0182-ad-default-offload.md)
then added Active Directory, by rendering the bind-password references its deployed models
name.

BMC Remedy was excluded for one reason, and it was not a judgement about the kind: there
was no worker to move it to. [ADR-0106](0106-bmc-remedy-connector.md) shipped the connector
with an in-process handler only, and the 2026-08-25 implementation audit listed `remedy`
among the offloadable kinds with nothing behind them. Its amendment closed that: the kind
now has the `Resolve`/`Run` split every offloaded kind has, an `atlas worker --connector
remedy` configured from its own environment, and `remedyWorkerEnv`, which hands a
supervised child the AR System base URL and the service account out of the connector store
and the vault. That is exactly the list the audit set as the condition for promoting a kind
— payload resolution, credential provisioning, worker execution, fallback tests.

So the question is no longer whether a Remedy task can run on a worker. It is whether it
should run there **without an operator naming the kind** — because until it does, a login,
a create and a logout against somebody else's ITSM host keep happening on the engine's
single-writer loop, which is the arrangement ADR-0164 exists to end.

It is worth being concrete about what that costs today. The AR System has no session
Atlas keeps: every entry is a `POST /api/jwt/login`, the create, and a best-effort logout —
three round trips to a host that is frequently on the far side of a corporate boundary. The
bounded call budget ([ADR-0149](0149-bounded-connector-call-budget.md)) caps how long that
can hold the loop; it does not take it off the loop.

## Decision drivers

- **A ticket create must not sit on the loop.** It is a network round trip to somebody
  else's server, and the engine has one writer per partition (I3).
- **No running installation may break.** An upgrade that turns every Remedy task into an
  incident is not a default; it is an outage with a changelog entry.
- **A default must never produce a worker that cannot serve what it was given** — already a
  stated property of the default set, guarded by
  `TestEveryDefaultOffloadedKindCanBeServedByItsWorker`.
- **A credential belongs where it is used** (ADR-0168): an AR System service account,
  typically allowed to file against every form in the instance, is worth less to an
  attacker in a worker than in the engine.
- **Two kinds with one mechanism should not have two defaults.** Since ADR-0106's
  amendment, mail and Remedy are configured and provisioned identically — a managed record,
  a vault bundle, rendered into the child at spawn. A difference in default between them is
  one an operator has to memorize rather than derive.

## Considered options

1. **Leave it opt-in.** `--offload-connectors remedy` or `--supervise-connector remedy`
   stay the only ways.
2. **Default it.** `remedy` joins `DefaultOffloadedKinds()`, and Atlas supervises a Remedy
   worker as it does a mail one.
3. **Make it worker-only**, as Entra and the SQL kinds are: delete the in-process handler
   entirely, so the worker is the only way a Remedy task ever runs.
4. **Default it conditionally** — offload the kind only on a server that has a Remedy
   connector configured.

## Decision outcome

Chosen: **option 2 — default it**.

The single reason for the exclusion is gone, and what replaced it is not a new mechanism
but mail's, which has been the default since ADR-0168 and is held to the same guard. A
Remedy connector's endpoint and `{username,password}` bundle live in the connector store
and the vault; `remedyWorkerEnv` renders them into the supervised child at every spawn and
refresh, resolved through the same `resolveConnectorSecret` the engine's own registry is
built from. An installation with a working Remedy connector therefore keeps working across
the upgrade with nothing to do — the same three values, built into the same client, in a
different process.

An installation with **no** Remedy connector — which is most of them — gets a supervised
worker that holds no instance and parks the kind rather than leasing its jobs and failing
them. That is not a new state: it is what a default mail worker does on the many servers
that have never configured a mailbox.

**Why not option 3.** It is the more honest endgame of ADR-0164 and it is tempting: a kind
with no in-engine form cannot drift, and Entra and the SQL kinds prove the shape. It is
rejected here for two reasons. `--in-process-connectors` is the escape hatch every default
in this set keeps honouring, and option 3 takes it away for this kind alone. And the kinds
that are worker-only never had an in-engine form to remove — for SQL, a DSN *is* the
credential, so there was never a client the engine could hold. Removing a working handler
that installations run today is a larger decision than promoting a default, it applies to
mail and AD in exactly the same way, and it should be made once for all of them rather than
five times, kind by kind, in whichever order they happened to get workers.

**Why not option 4.** A default that depends on configuration is invisible. The same server
would place the same task differently before and after somebody adds a connector, and
"where does this kind run" would stop being a question the flags answer — including for the
Modeler's placement badge ([ADR-0183](0183-the-modeler-asks-where-a-kind-runs.md)), which
answers per kind, not per store contents.

**What follows from the mechanism**, each a real consequence rather than an implementation
detail:

- **Only the Remedy worker gets the service account.** Provisioning is per kind, so the
  script worker — which runs model-authored code and inherits its whole environment — is
  never handed an ITSM credential. This is the property ADR-0182 called the failure that
  would matter most, and it holds here for the same reason.
- **The diagnosis does not move.** The engine still builds its own Remedy registry from the
  store, so a connector that is disabled, endpoint-less or holding a malformed bundle is
  still reported as *configured and broken* rather than *never configured*
  ([ADR-0158](0158-a-connector-reference-that-explains-itself.md)). Offloading removes the
  handler, not the registry.
- **An unusable connector is left out of the handover**, not passed over half-filled: a
  *named* instance missing a field makes the worker refuse at startup, which would take
  down every other kind that worker serves.
- **A Console change restarts the worker only when what it holds changed.** Adding a Helix
  instance cycles the Remedy worker once; an unrelated connector edit costs nothing.
- **An external Remedy worker is unaffected.** It is handed nothing and its operator sets
  the same variables by hand, as before. Only a supervised child — this process's own child,
  same host, same user — is provisioned, which is the boundary ADR-0168 drew.
- **Delivery is unchanged.** At-least-once with the job key as `X-Request-ID` (ADR-0106);
  which process files the ticket does not change what a replay does.

### Consequences

- **Positive:** a ticket create leaves the engine's loop for every installation, not only
  for those that opted in; mail and Remedy stop differing for no reason an operator can
  derive; an AR System reachable only from inside a customer's network becomes a question
  of where the worker sits rather than how Atlas is configured; and a supervised Remedy
  worker needs no configuration at all, which keeps ADR-0011's one-command story.
- **Negative / trade-offs accepted:** the supervised Remedy worker holds the AR System
  service account decrypted in its process environment — readable by anyone who can read
  that user's `/proc`, and not encrypted at rest the way the vault is. That is the same
  widening ADR-0168 accepted for mail and ADR-0182 for AD, accepted for the same reason: it
  is the same host and the same user, and the alternative is a credential that crosses a
  network on every lease. Every server also gains one more supervised child process by
  default, including the majority that will never file a ticket. And an operator debugging
  a Remedy task now reads the Workers view and a worker's log rather than the server's own,
  which is a place to have to know about.
- **Follow-ups / risks to watch:** clio, SharePoint and temis are the managed kinds still
  without a worker at all (the 2026-08-25 audit's P0), and each is a promotion like this one
  once it has the same four things; whether the in-process handlers should be removed
  outright, for every kind that has a worker, is ADR-0164's remaining question and is not
  answered here.

## Pros and cons of the options

### Option 1 — leave it opt-in
- Good: nothing changes; no new supervised process on any server.
- Bad: the work keeps running on the loop unless an operator intervenes, on a kind whose
  every call is three round trips to somebody else's host.

### Option 2 — default it (chosen)
- Good: no operator action, no model change, no upgrade step; the mechanism and its guard
  are mail's, already the default; symmetric with the kind it is configured identically to.
- Bad: a decrypted service account in a child's environment; one more supervised process on
  servers that have no Remedy instance.

### Option 3 — make it worker-only
- Good: one path instead of two, so nothing can drift; the honest end state of ADR-0164.
- Bad: removes `--in-process-connectors` for this kind alone, and decides for mail and AD by
  precedent what should be decided for them explicitly.

### Option 4 — default it conditionally
- Good: no idle worker on a server with no Remedy instance.
- Bad: placement becomes invisible and store-dependent; the same flags describe two
  different servers.

## Links

- builds on [ADR-0106](0106-bmc-remedy-connector.md) (amended) — the worker this default
  starts for you
- follows [ADR-0182](0182-ad-default-offload.md) — the same decision, for the previous kind
- builds on [ADR-0168](0168-connector-work-on-a-worker.md) — the provisioning boundary and
  the default set this joins
- realizes [ADR-0164](0164-no-in-process-service-tasks.md) — for one more kind that stayed
  behind
- relates to [ADR-0157](0157-worker-processes-supervision-and-console.md) — the supervised
  child
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) /
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) — the store and vault a worker
  cannot read
- relates to [ADR-0149](0149-bounded-connector-call-budget.md) — what bounds the call while
  it is still on the loop
