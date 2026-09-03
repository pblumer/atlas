// Package sqldb runs one SQL statement against a relational database on behalf of a
// BPMN service task — for Microsoft SQL Server, MariaDB, or PostgreSQL (ADR-0173).
//
// # Worker-only
//
// This is the first Worker Type with no in-process half at all. [ADR-0164] decided
// that every side-effecting service task belongs on a worker and that new kinds are
// built worker-first; a database credential is the strongest case there is for taking
// that literally, because a DSN cannot be split into a public address and a secret
// reference the way every other worker's endpoint can. So the engine never holds
// one: nothing here is registered with the engine's job runner, and a SQL task waits
// until a worker that holds the DSN leases it.
//
// # Where the line falls
//
// [ADR-0168]'s split, applied here:
//
//   - [Resolve] is engine work. It reads the task's detail out of the compiled
//     process and the parameters out of the instance's scope chain, because FEEL is
//     compiled at deploy and only the engine has the scope. It produces a [Job] of
//     plain values.
//   - [Run] is worker work. It resolves the job's worker *name* against a registry
//     the worker built from its own environment, opens nothing the engine knows
//     about, and executes.
//
// A [Job] has nowhere to put a DSN, which makes "the engine holds no database
// credential" a property of the type rather than of the code filling it in.
//
// # The statement
//
// [Job.Statement] arrives literal. The compiler refuses a FEEL statement outright
// (see compileSqlConnectorTask), because a statement assembled from process data is
// an injection that needs no quoting bug to work — it would be the field doing
// exactly what it advertises. Data reaches the statement only through bound
// parameters, in the placeholder syntax of the product the task was authored for.
//
// [ADR-0164]: https://github.com/pblumer/atlas/blob/main/docs/adr/0164-no-in-process-service-tasks.md
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md
package sqldb
