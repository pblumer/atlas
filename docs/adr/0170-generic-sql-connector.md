# ADR-0170: Three SQL connectors, and the first kinds born on a worker

- **Status:** Proposed
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

[`docs/comparisons/mim.md`](../comparisons/mim.md) maps Microsoft Identity Manager's
connector surface onto Atlas and finds one hole larger than the rest: there is no
database connector of any kind in `connector/`. Four of MIM's supported management
agents are databases — Generic SQL, Microsoft SQL Server, Oracle Database, and IBM
DB2 — and identity work is full of them. The authoritative list of employees is a
view in an HR database far more often than it is a directory.

So: how should a BPMN process read from and write to a relational database?

Three things make this more than "another connector package".

- **A DSN is not an endpoint.** Every other connector splits cleanly into a
  model-authored endpoint plus a secret *reference* (ADR-0041). A connection string
  does not split: `sqlserver://user:pass@host:1433?database=hr` is one string that is
  half address and half credential, and the driver parses it as a unit.
- **A statement is not a URL.** A REST connector's URL is data. A SQL statement is
  *code*, executed by the database with the connector's privileges. Where it comes
  from is a security boundary, not a convenience.
- **[ADR-0164](0164-no-in-process-service-tasks.md) already answered where it runs.**
  It is Accepted, and it says plainly: "New connector kinds are built worker-first."
  Every connector before this one was built in-process and is now deprecated.

## Decision drivers

- **Close MIM's database rows** with as little duplicated machinery as possible.
- **No CGO** ([ADR-0010](0010-go-and-no-cgo.md)) — the binary is `CGO_ENABLED=0` and
  stays that way. This decides which databases can be served at all.
- **A model must not be able to compose a statement.** Whatever else is authored,
  the SQL text must not be assemblable from process data.
- **Don't ship a shape that is already deprecated.** ADR-0164 is Accepted; a new
  in-process connector would be born owing a migration.
- **Testable without a live database.** `database/sql` has a driver interface, so
  the whole connector must be exercisable against a registered fake.

## Considered options

**Where it runs:**

1. **A managed in-process connector**, like `remedy`/`mail`: a connector-store record
   holding the address, a credential bundle in the vault, an in-process worker.
2. **A model-authored connector**, like `ldap`/`soap`/`ad`: the DSN in the model with
   a secret reference for the password.
3. **A worker-only connector**: no in-process handler, no store record, no vault
   entry — the engine resolves the task, and the DSN lives in the worker's own
   environment.

**How many kinds:**

- **A.** One generic `sql` kind, with the product chosen by the worker's configuration.
- **B.** One kind per database product.

## Decision outcome

Chosen: **a worker-only connector** (option 3), as **one kind per product** (option B) —
Microsoft SQL Server, MariaDB, and PostgreSQL.

### Why worker-only

Option 2 is the one to reject first, and not on style: a DSN cannot carry a secret
*reference*. To keep the password out of the model the connector would have to
re-assemble the connection string from a partial DSN plus a resolved secret, which
means parsing and rewriting each vendor's connection-string grammar — MySQL's is not
even a URL. That is a credential-handling code path invented for the convenience of
putting an address in a model, and it would be the only place in Atlas where a secret
is spliced into a string the model supplied.

Option 1 is defensible and is what every existing managed kind does. It is rejected
because ADR-0164 is Accepted, not aspirational, and [ADR-0168](0168-connector-work-on-a-worker.md)
has already built the mechanism it names: `csvimport` proved a resolved detail can
travel with a leased job, and `mail` proved a credential can live only on the worker.
Building a *new* kind in-process now would ship it deprecated, and would put a
database credential — usually the most valuable one an organization has — into the
engine's address space for no reason other than precedent.

So these are the first kinds with no in-process half at all:

- **Reserved job types** `io.atlas.mssql` (20), `io.atlas.mariadb` (21) and
  `io.atlas.postgres` (22). No handler is registered in the engine, so the type-keyed
  pull always lets a worker lease them, and they need no entry in `offloadableKinds` —
  there is nothing to offload.
- **The engine resolves** (`sqldb.Resolve`): it finds the task detail in the compiled
  process, reads the parameters out of the instance's scope chain, and produces a
  `sqldb.Job` of plain values. This half must stay in the engine — FEEL is compiled at
  deploy (ADR-0008/0015) and only the engine has the scope chain.
- **The worker executes** (`sqldb.Run`): it resolves the job's connector *name*
  against a registry built from its own environment — `ATLAS_<KIND>_CONNECTORS` names
  them, each contributing `ATLAS_<KIND>_<NAME>_DSN`. As with mail, the DSN comes from
  the environment and not from a flag because argv is readable by anyone who can list
  processes, and `sqldb.Job` has nowhere to put one.

### Why one kind per product

A generic kind is the tidier-looking option and it is the wrong one, because the
thing that differs between these databases is not configuration — it is the
*statement*, which is model data.

Placeholder syntax is per-product and cannot be abstracted: `@p1` for SQL Server, `?`
for MariaDB, `$1` for PostgreSQL. Atlas does not rewrite it (see below), so a
statement is already tied to one product the moment it is written. Given that, a
generic kind would let an operator point a `$1`-authored statement at a SQL Server
connector and discover it as a runtime error; a kind per product makes that
unrepresentable. The same asymmetry applies to named binding, which SQL Server
supports and the other two do not.

It costs almost nothing to do this way. The three share one package, one engine-side
resolve, one worker-side run, one task shape and one set of compiler rules; a
`Product` record holds the four fields that genuinely differ. What the modeler shows
is three recognizable products rather than one box with a dropdown — and an author
looks for "PostgreSQL", not for "SQL". MIM makes the same call, shipping *Microsoft
SQL Server* and *Oracle Database* agents alongside its generic one.

### The statement is literal, never FEEL

Every other literal-or-FEEL value on a connector task carries the `fx` toggle
(ADR-0067). The `statement` attribute deliberately does not, and it is held as an
interned string rather than a `RestExpr` so the type cannot express one.

If a statement could be a FEEL expression, a process variable could become part of
the SQL text, and an injection would need no quoting bug to succeed — it would be the
documented behaviour of the field. Making the statement literal moves that from "the
author must remember to be careful" to "the compiler will not accept it". The
compiler therefore rejects a `statement` beginning with `=`, naming
`parametersVariable` as the way to get data into a query.

Data reaches the statement only through **bound parameters**. `parametersVariable`
names one process variable:

- a **JSON array** binds positionally, in order;
- a **JSON object** binds as `sql.Named`, by key — accepted for SQL Server, and
  refused for MariaDB and PostgreSQL rather than flattened, because Go's map
  iteration has no order and "flatten it" would mean binding the author's values in
  an order nobody wrote;
- **any other kind** is the single-placeholder case and binds as one value.

Numbers bind as integers where they are integral, so a primary key does not
round-trip through `1e+06` and match nothing.

### Operations and the row cap

`query` returns rows as a JSON array; `query-one` returns a single row as an object
(no rows → `null`, more than one → an error, because "look up this person" that
matches twice is a bad assumption and not a result); `execute` returns the affected
row count.

A `SELECT` with no `WHERE` against a person table is the obvious way to exhaust the
worker's memory, so a query carries a **`maxRows` cap** (default 1000). Exceeding it
**fails the job** rather than truncating: a silently short result set is a wrong
business answer, and a process that branches on `count(rows)` would branch wrongly.
This is the same instinct as ADR-0149's call budget, applied to result size instead of
time. The call itself is bounded by the shared `nettimeout.Default`.

### The drivers, and what is not covered

`CGO_ENABLED=0` (ADR-0010) admits only pure-Go drivers:

| Kind | Driver | Module | Placeholders | Named binding |
|---|---|---|---|---|
| `mssql` | `sqlserver` | `github.com/microsoft/go-mssqldb` | `@p1` | yes |
| `mariadb` | `mysql` | `github.com/go-sql-driver/mysql` | `?` | no |
| `postgres` | `pgx` | `github.com/jackc/pgx/v5/stdlib` | `$1` | no |

The drivers are imported by `worker`, not by `connector/sqldb`. That keeps the
connector package free of vendor code and testable against a fake `database/sql`
driver, and it keeps the engine — which never opens a database — from linking a
driver by depending on the connector.

**Two MIM database agents stay unserved.** *IBM DB2* cannot be served without
reversing ADR-0010: IBM's driver is a CGO wrapper over the CLI client library and
there is no pure-Go alternative. *Oracle Database* can be — `github.com/sijms/go-ora`
is pure Go — and is deliberately left for a follow-up rather than bundled in here, so
the seam is reviewed on three products before a fourth rides on it. Adding it is a
row in the product table, a job type, and a blank import. Both are stated gaps in the
MIM comparison, not oversights.

### Consequences

- **Positive:** three of MIM's four database rows close (a fourth needs only the
  Oracle driver). The engine never holds a database credential — the highest-value
  secret in most organizations stays in the network that owns it. A statement cannot
  be composed from process data, and that is a property of the compiled type rather
  than of a validation someone could relax. And ADR-0164 gets its first kinds that owe
  no migration, which makes the rule concrete rather than a note on the deprecated
  ones.
- **Negative / trade-offs accepted:**
  - **Dependency weight.** The module graph grows from 155 to 175 and the binary
    links about 47 more packages. It is far less than the drivers' `go.mod` files
    suggest — the Azure SDK, Kerberos, gorm and xorm all appear in `go.sum` through
    sibling packages Atlas does not import, and none of them are linked — but a
    supply-chain review reads `go.sum`, not the link map. Recorded here so it is not
    rediscovered as a surprise.
  - **A SQL task cannot run without a worker.** `atlas serve --supervise` keeps that
    to one flag, but unlike every other kind there is no in-process fallback. That is
    the point, and it is the first time a modeler can author a task the default
    single-process install will not execute.
  - Connector configuration for these kinds is not in the Console (ADR-0168 accepted
    this cost for every kind that moves); the Workers view showing which names are
    served is what recovers it.
  - **Startup validation is best-effort.** A worker refuses a configured name with no
    DSN, and refuses a malformed DSN where the driver parses it eagerly — MySQL and
    SQL Server do, pgx defers to the first connection. A database that is merely down
    must *not* block startup, since a worker has to survive a database restart.
  - A model is not portable between the three products, because the placeholders are
    not. Making the product part of the kind is how that becomes visible at authoring
    time instead of at runtime.
  - No transaction spans two tasks. Each task is one autocommit statement; a
    cross-task transaction would need a connection pinned across an fsync boundary and
    a lease, which the connector seam does not offer. A process that needs atomicity
    puts it in one statement or in a stored procedure.

## Pros and cons of the options

### Option 1 — managed in-process connector
- Good: consistent with every existing managed kind; configurable in the Console.
- Bad: ships a shape ADR-0164 deprecated; puts the database credential in the engine.

### Option 2 — model-authored DSN
- Good: consistent with `ldap`/`soap`/`ad`; no worker needed.
- Bad: a DSN cannot hold a secret *reference*, so it would need per-vendor
  connection-string splicing — a credential path invented to keep an address in a model.

### Option 3 — worker-only (chosen)
- Good: the credential lives where it is used; no deprecated half to migrate later;
  the engine keeps only the work that needs the compiled process.
- Bad: no in-process fallback, so these kinds always need a worker.

### A — one generic kind
- Good: one entry in the modeler; a model could in principle be repointed.
- Bad: "in principle" is false, because placeholders are per-product; the mismatch
  becomes a runtime error instead of something the model cannot express.

### B — one kind per product (chosen)
- Good: the product is visible where the statement is written; an author finds the
  database by name; per-product hints and rules are stated exactly.
- Bad: three catalog entries, three job types, and three reserved indices for what is
  one package.

## Relationship to other records

- follows [ADR-0164](0164-no-in-process-service-tasks.md)'s rule that new connector
  kinds are built worker-first, and is the first kind with no in-process half
- uses [ADR-0168](0168-connector-work-on-a-worker.md)'s resolved-detail-on-the-job
  mechanism and its environment-held worker credentials
- honors [ADR-0010](0010-go-and-no-cgo.md) (no CGO), which decides the driver set and
  excludes DB2
- honors [ADR-0041](0041-connector-management-and-secret-store.md)'s promise that a
  model never carries a secret — here by keeping the DSN out of the engine entirely
- bounds its calls with [ADR-0149](0149-bounded-connector-call-budget.md)'s budget
- rides the connector seam of [ADR-0007](0007-job-worker-protocol.md)/[ADR-0067](0067-service-task-connector-catalog.md)
- answers the largest gap named in [`docs/comparisons/mim.md`](../comparisons/mim.md)
