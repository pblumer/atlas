# ADR-DRAFT: The Console may dial a database, and the engine still links no driver

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

`POST /api/v1/connectors/test` checks a connector against what is *typed*, before it is
saved, because that is the moment a wrong host or a dead credential is cheapest to fix
([ADR-0150](0150-preview-mail-provider-and-visible-incidents.md)). It covered mail and nothing
else: every other kind answered "only a mail connector can be checked today."

For the SQL kinds that gap is worse than for the others, and for a specific reason: a
connection string is a SQL connector's **whole** configuration. Every other kind's form
has parts an operator can eyeball — a URL, a sender, a tenant. A SQL connector is one
opaque string sealed into the vault ([ADR-0188](0188-console-managed-sql-connectors.md)),
which the Console can never show back. So a typo in it is invisible from the moment it
is saved, and the first thing that reads it is a supervised worker at spawn time, whose
failure reaches the operator as a task that parks.

The obstacle is not the check. It is where the check can run.
[ADR-0173](0173-generic-sql-connector.md) put the database drivers in `worker` and
deliberately kept them out of the connector package and out of the engine — "the engine,
which never opens a database, does not link a driver by depending on the connector."
`api` therefore cannot call `sql.Open`: there is no driver registered in it to find.

## Decision drivers

- **A connector whose whole configuration is a secret must be checkable**, or it is
  configured blind.
- **`api` must keep linking no database driver.** It is a real property, not a
  stylistic one: it is what makes "the engine never opens a database" checkable by
  reading the import graph rather than by reading the code.
- **The check must not run on the run loop.** A database that does not answer would
  park the single writer behind it (I3).
- **No new promise about credentials.** Whatever this does must already be inside what
  ADR-0188 decided.

## Considered options

1. **Blank-import the drivers in `api`** and dial directly.
2. **Ask a worker to check**, over a new request path from server to worker.
3. **Inject the check**: `api` takes a probe function; whoever assembles the binary and
   already links the drivers supplies it.

## Decision outcome

Chosen: **option 3**. `api.WithSQLProbe(worker.ProbeSQL)`, wired in `cmd/atlas`.

`api` declares the seam (`SQLProbe`) and calls it. `worker.ProbeSQL` opens the product's
driver, pings, and closes. The single binary ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)) links
both and joins them in one line. An embedder who wires nothing gets a check that answers
**"this server cannot check a database connection: it was built without a database
driver"** — which is the truth, and is better than a connector reported broken because
a driver was absent.

So the import-graph property survives exactly: `go list -deps ./api` still names no
database driver.

### What is actually new, and why it is inside ADR-0188

The engine process now opens a TCP connection to a database. That is worth naming
plainly, because ADR-0173's prose says it does not.

What ADR-0173 was protecting is intact. Its argument was that **a model must never carry
a database credential**, and that an operator's own worker must be able to hold a DSN the
engine has never seen. Both still hold: nothing here reads a model, and an external
worker is still handed nothing.

What changed was decided already. ADR-0188 put the connection string of a *Console-
managed* connector into the engine's vault, so that a supervised worker could be handed
it. The engine holds these strings today. This record adds one thing to that: **on an
operator's click, it dials one.** The operator is present, it is their database and their
credential, and the alternative is that they cannot check it at all. It is not process
work: no token, no job, and nothing scheduled.

### What the check proves, and what it does not

The verdict says so, because "OK" is read as "the task will work":

> Connected to `sa@db.example.com:1433/hr` and authenticated. No statement was run, so
> this does not prove the login may read or write the tables a task names.

The address comes from `redactedSQLTarget`, which is the same derivation the connector
list already shows and never reads the password.

### The shape of the answers

Mail's rules, unchanged: a failed connection is **200 with `ok:false`** — the request was
served and the answer is "no". Only an unusable *request* is a 4xx, and there are two:
neither a `connectionString` nor a `credentialsRef` to dial (400), and a reference
belonging to a connector the caller may not edit (403, per
[ADR-0205](0205-connector-ownership-and-event-delivery.md)). A reference the vault does not
resolve is `ok:false` with that reason, rather than an empty string handed to a driver —
each of the three reports that differently, and none of them says "the secret is not set
yet", which is the state every connector passes through while it is being made.

The resolve happens on the run loop (the vault's owner) and the dial happens off it, in
that order — the same split mail's check makes, for the same reason.

### Consequences

- **Positive:** the kind whose configuration is least inspectable becomes the one the
  Console can answer for. The check works both before a connector is saved (against the
  typed string, in the create form) and afterwards (against the vault reference, in the
  edit dialog) — and the second is the case that matters, because an operator opens that
  dialog when something *stopped* working. `Client.Ping` also gives a worker something to
  check with later.
- **Negative / trade-offs accepted:** the server process can open a database connection.
  Bounded to an operator's click, to `connectorTestTimeout`, and to one connection that
  is closed immediately — but it is a socket the engine did not open before. An embedder
  that skips the wiring gets a check that cannot run, which is a worse experience than
  a check that does not exist would be if the message were not explicit.
- **Follow-ups / risks to watch:** the other kinds still cannot be checked; the refusal
  now names them so the message does not go stale as they gain one. If a worker ever
  gains a request path, moving the dial there is strictly better and this seam is where
  it would be replaced.

## Pros and cons of the options

### Option 1 — blank-import the drivers in `api`
- Good: one line, no seam.
- Bad: breaks the import-graph property ADR-0173 relies on, and links three drivers into
  every embedder that only ever resolves jobs.

### Option 2 — ask a worker to check
- Good: the dial happens where the credential is meant to live; works for external
  workers too.
- Bad: there is no server-to-worker request path, and inventing one for a check is a new
  protocol, a new trust boundary and a new failure mode — for a button.

### Option 3 — inject the check (chosen)
- Good: `api` links no driver; the dependency is explicit in the binary; an embedder gets
  an honest answer rather than a wrong one.
- Bad: one more option to wire, and one more state ("no probe") to word carefully.

## Links

- extends [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) to a second kind
- rides [ADR-0188](0188-console-managed-sql-connectors.md), which already put these
  connection strings in the engine's vault
- keeps [ADR-0173](0173-generic-sql-connector.md)'s driver placement intact
- honors [ADR-0205](0205-connector-ownership-and-event-delivery.md) on a credential reference
  the caller does not own
