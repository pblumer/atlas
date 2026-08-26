# ADR-0188: A database is a Console entry, not a start parameter — and a worker is never a thing you create

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0173](0173-generic-sql-connector.md) made the three SQL connectors worker-only and
put their connection strings in the worker's environment, so the engine would never
hold a database credential. Configuring one therefore means setting
`ATLAS_POSTGRES_CONNECTORS` and `ATLAS_POSTGRES_<NAME>_DSN` on the host and passing
`--supervise-connector postgres`, and the record accepted that cost in one line:
"Connector configuration for these kinds is not in the Console."

In use that line is the whole experience. An operator who has a database and a running
Atlas cannot get from one to the other without editing a unit file and restarting the
server — for a kind whose *only* configuration is one string. Every other integration
in Atlas is a form in **Organization › Connectors**.

Underneath that sits a second question the first one keeps running into: **should the
Console be able to create and configure workers at all?** It cannot today, and
`cmd/atlas/main.go` says so deliberately — "the API can restart one of these, and can
neither introduce a worker nor name a command."

And underneath *that* is a defect that makes the intended answer not work. [ADR-0172](0172-entra-id-connector.md)
already established the shape this should take: a kind Atlas supervises by default,
whose worker **parks** with nothing to serve until a Console entry appears, at which
point `refreshSupervisedWorkers` brings it up. The parking half is not implemented.
`exitNothingToServe` (status 78, which the supervisor parks on rather than restarting)
is only reached when a worker holds *no handler at all*, but `entraRegistryFromEnv` and
`sqlRegistryFromEnv` return a hard **error** when their `CONNECTORS` variable is empty,
and `BuiltinConnectors` propagates it. The worker dies before it can reach the parking
branch, the supervisor records `failed`, and it restarts on a doubling backoff capped
at 30s with no give-up condition. Observed on a server with no Entra tenant configured:

```
started a supervised worker id=entra pid=30288
started a supervised worker id=entra pid=30303   (+0.5s)
started a supervised worker id=entra pid=30309   (+1s)
started a supervised worker id=entra pid=30315   (+2s)
```

That is precisely what the comment on `exitNothingToServe` says must not happen: "a
backoff loop that never converges and a console full of red." Mail gets this right —
it reports the kind as `Unconfigured` and simply does not subscribe — and mail is the
only kind that does.

## Decision drivers

- **A database should be configurable where every other integration is**, without a
  deployment change.
- **The engine must not become able to run arbitrary commands** on an API call.
- **ADR-0173's promise must survive for every worker Atlas does not start itself.**
- **A kind supervised by default must be harmless when unconfigured**, because that is
  the state every fresh server is in.
- **No second configuration mechanism.** The supervised path must hand the worker the
  same variables an operator sets by hand, or the supervised path becomes the only one
  that is tested (ADR-0157).
- **The record holds references, never secrets** (ADR-0041, I6).

## Considered options

**For "configure a worker from the GUI":**

1. **A create-worker API**: the Console names a kind (and, in the general form, a job
   type and a command) and Atlas starts a worker for it.
2. **A connector entry, and the worker follows**: the kind is supervised by default and
   parks; creating a *connector* is what brings it up. The Entra shape.
3. **Leave it on the command line**, as ADR-0173 has it.

**For what the Console stores:**

- **A.** The **whole connection string** as the credential, sealed in the vault.
- **B.** **Host, port, database and user as record fields**, with only the password in
  the vault; the worker assembles the DSN.

## Decision outcome

Chosen: **option 2** — a connector entry, with the worker following it — storing
**option A**, the whole connection string. Explicitly **not** option 1.

### A worker is never a thing you create

"Create a worker" bundles three different requests, and they have three different
answers.

Naming an arbitrary **command** for Atlas to execute is remote code execution with
extra steps: anyone who can POST to the Console runs binaries as the Atlas user.
`--supervise id=type=command` stays on the command line, permanently.

Naming a **built-in kind** would be safe — the API would pick from
`worker.KnownConnectorKinds()`, a closed list compiled into the binary, not a command.
It is rejected anyway, because it is unnecessary once the third case works, and
strictly worse than it: it invents a lifecycle (create, rename, delete a worker) that
the operator would then have to keep consistent with the connector list, and it makes
it possible to create a worker that serves nothing, or two that race for one kind.

What an operator actually wants is the third: **configure the database.** So the worker
is a property of the build, not of the configuration. `DefaultSupervisedWorkerOnlyKinds`
names the three SQL products alongside Entra, Atlas always supervises one worker per
kind, and it parks until a connector exists. The operator creates a *database*; the
process is an implementation detail and stays one.

This is stated as a decision rather than left implicit because "can I add a worker in
the UI" is a question that will be asked again, and the answer — *you never need to* —
is only convincing next to the reason.

### Parking must work first

Defaulting three more kinds to a supervised worker without fixing the parking defect
would add three more permanent restart loops to every `atlas serve`. So this record
carries that fix, and the fix draws a line the code did not have:

- **Nothing configured** (`CONNECTORS` empty) is *unconfigured*: the registry builder
  returns no registry and no error, `BuiltinConnectors` reports the kind as
  `Unconfigured`, and a worker holding nothing else exits 78 and parks.
- **Something configured but broken** (a named database with no DSN, an unparsable DSN,
  a tenant missing its client id) is *misconfigured*, and still fails at startup while
  the operator is watching. ADR-0173 was right about that and it is unchanged.

The distinction has a sharp edge that is easy to get wrong, and it is why
`sqlWorkerEnv` omits a connector's *name* — not just its DSN — when its secret is not
set. A name in `CONNECTORS` with nothing behind it is the misconfiguration case above.
Rendering it would mean that creating a second database in the Console stops the worker
serving the first one, for the window between saving the record and saving the secret.
That is not hypothetical: it is what the first version of this change did, and the test
that catches it is deliberately written with two connectors, because with one the
`fromStore` guard hides it.

### The credential is the whole connection string

Option B is the tidier-looking one and it is the one ADR-0173 already refused, on an
argument that has not weakened: splitting a DSN means re-assembling it per vendor —
MySQL's is not even a URL — which is a credential-handling path invented for the
convenience of putting an address in a record. It also silently drops the driver
parameters that make a real deployment work (`sslmode`, `default_query_exec_mode`, pool
limits) unless an "extra parameters" field brings a second grammar in through the back
door.

So the whole string is the secret. The operator pastes it into the create form, the
server seals it into the vault under a key derived from the record id (`sql/<id>/dsn`,
derived from the id rather than the name so a rename cannot orphan it), and the record
stores that reference and nothing else. Naming an existing vault key instead still
works, and is what an operator who already keeps DSNs in the vault does.

The cost of option A is that a connector would otherwise be an opaque name — "is this
the test database or production?" is exactly what you need to know before pointing a
process at it. So the server derives a **redacted label** (`user@host:port/database`)
at save time and stores it as the record's endpoint. Deriving this is allowed where
assembling is not, and the difference is the failure mode: an assembler that gets a
vendor's grammar wrong breaks the connection, while this only ever produces a label, so
it is written to give up rather than guess. Anything that is not a URL with a host
yields no label at all, the password is never read, and the query string is dropped
wholesale because some vendors carry a password in it.

### What this costs against ADR-0173

Plainly, because it is the point of the record: **the engine now holds a database
credential** — encrypted at rest in the vault (ADR-0069/0070), never in the WAL, an
event or a variable, and in the engine's process memory only while it renders a child's
environment.

That is the thing ADR-0173 refused, and this record does not pretend otherwise. What it
claims is that the refusal was broader than its own argument. ADR-0173's *stated*
objection was to splicing a secret into a model-supplied string; keeping the whole DSN
as one opaque secret does not splice anything. And the precedent is already set: mail's
password and Entra's client secret live in the same vault and are handed to the same
supervised children, on `superviseEnv`'s own reasoning that "a supervised worker is the
one case where the engine also happens to *be* the operator."

The half of ADR-0173 that does not move: **an external worker is handed nothing from
here.** It reads its own environment, in its own network, and the engine has no way to
give it anything. That was always the substance of the promise, and it is intact.

Two smaller rules keep the paths from diverging. The variables rendered into the child
are exactly the ones an operator sets by hand — no private channel, or the supervised
path becomes the only tested one (ADR-0157). And a DSN the operator set on the host
wins over the vault, so a stale Console entry cannot silently beat an explicit choice.

### Consequences

- **Positive:** a database is configured where every other integration is, with no
  restart and no start parameter. `doAndRefresh` already routes every connector write
  through `refreshSupervisedWorkers`, so the worker picks it up on save.
- **Positive:** the parking defect is fixed for Entra too, which has been restarting a
  worker every 30 seconds on every server with no tenant configured since ADR-0172.
- **Positive:** "can the Console create workers" has an answer on the record, and the
  answer removes the need rather than deferring it.
- **Negative / trade-offs accepted:**
  - **The engine's vault holds a database credential**, as above.
  - **Three more supervised workers** on a default `atlas serve`. Each is one process
    that exits 78 within a second and parks, so the standing cost is a row in the
    Workers view, not a running process — but it is three more rows.
  - **A connection string cannot be read back**, by anyone, including the operator who
    typed it: the vault has no read route by design. Replacing one means overwriting
    its key under **Secrets**. That is the correct trade and it will surprise someone.
  - **The redacted label can be blank** for a keyword-form DSN, so MariaDB connectors
    will more often show a name and nothing else.
- **Follow-ups / risks to watch:** the create form seals a secret, which no other
  connector form does — the record still holds only a reference, but the *dialog* is
  now a secret-writing surface and should stay the only one. Editing a connector still
  cannot replace its connection string in one step; the vault key is shown, and
  overwriting it under Secrets is the path until that is worth its own field.

## Pros and cons of the options

### Option 1 — a create-worker API
- Good: the most literal reading of "configure workers in the GUI".
- Bad: in its general form it executes operator-supplied commands; in its safe form it
  invents a worker lifecycle for no gain, and permits workers that serve nothing.

### Option 2 — a connector entry, and the worker follows (chosen)
- Good: the operator manages databases, not processes; no way to create a worker that
  serves nothing or two that race; the Entra shape, already built.
- Bad: needs the parking defect fixed first, and adds a supervised worker per kind to
  every server whether or not it is used.

### Option 3 — leave it on the command line
- Good: no engine-held credential at all; ADR-0173 unchanged.
- Bad: the one kind whose entire configuration is a single string is the one kind that
  requires a deployment change to configure.

### A — the whole connection string as the credential (chosen)
- Good: no parsing, no per-vendor grammar, driver parameters come along for free.
- Bad: the record cannot describe the database without a derived label, and that label
  is best-effort.

### B — address fields plus a password
- Good: the Console can show and diff exactly what a connector points at.
- Bad: per-vendor assembly — the thing ADR-0173 refused — and it loses driver
  parameters unless a second grammar is added to carry them.

## Relationship to other records

- amends [ADR-0173](0173-generic-sql-connector.md): its "not in the Console" cost is
  paid off for the supervised case, and its promise kept for every other worker
- follows [ADR-0172](0172-entra-id-connector.md)'s worker-only, supervised-by-default
  shape, and repairs the parking half of it that was never implemented
- uses [ADR-0168](0168-connector-work-on-a-worker.md)'s engine-resolves /
  worker-executes split, and its worker-reported connector-name coverage
- keeps [ADR-0041](0041-connector-management-and-secret-store.md)'s rule that a record
  holds a reference and never a secret, and seals into
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)'s vault
- honors [ADR-0157](0157-worker-processes-supervision-and-console.md)'s rule that a supervised worker
  is configured through the same variables an external one is
