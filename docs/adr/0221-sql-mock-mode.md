# ADR-0221: A database task runs against seeded answers, not against a SQL engine

- **Status:** Proposed (amended 2026-09-02: the switch is in the Console — an org-wide
  setting with the seed stored beside it, restarting the supervised SQL workers on save,
  which is the follow-up the original record named as not built.)
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0173](0173-generic-sql-connector.md) made the three SQL Worker Types worker-only:
the engine resolves a task into a statement and its bound parameters, and a worker that
holds the connection string executes it. That is the right seam, and it leaves the type
with a property no other Worker Type has — **it cannot be tried at all without the
production dependency**. Every other kind has some way to be exercised: mail has the
preview provider and its outbox, Active Directory has a mock forest ([ADR-0181](0181-ad-connector-mock-mode.md)),
REST and webscrape reach a test server somebody stands up in a minute.

A database does not work that way. The database an identity or order process reads is
the HR system or the ERP, and the whole reason it is interesting is that it is the one
nobody wants a model under development writing into. So the first time anyone runs a
model with a SQL task is against a real database, with a real statement, and the first
thing they learn is whether their placeholders were right.

The AD mock answered exactly this question for directories, and its argument transfers
without modification: *the switch is the worker's, not the model's, so a mockup run
proves the process and moving it to the real thing is an environment, not an edit.*

What does not transfer is **what the stand-in is**. A directory is a tree of entries
and an LDAP filter over it; a few hundred lines are a faithful-enough directory. A
relational database is a SQL engine, and Microsoft SQL Server is a specific one.

## Decision drivers

- **A model must run end to end without a database**, unchanged, so that a mockup run
  proves the process rather than a variant of it.
- **A mock must never teach a model something false.** It is used precisely where
  nobody can check the answer against reality, so a wrong answer is believed.
- **No new dependency, no CGO** ([ADR-0010](0010-go-and-no-cgo.md)). ADR-0173 already
  recorded the module graph growing by twenty for the drivers; a mock must not add to
  that, and a supply-chain review reads `go.sum`.
- **The seed is written by an operator in a text editor**, so its vocabulary and its
  errors are the file's, not a Go type's.

## Considered options

1. **An embedded pure-Go SQL engine** as the mock backend (`modernc.org/sqlite`).
2. **A hand-written micro-SQL engine** — a table store with a small parsed subset.
3. **A seeded answer table**: statements matched to the rows or affected count they
   produce, with no execution at all.

## Decision outcome

Chosen: **option 3, a seeded answer table** — `sqldb.MockDatabase`, reached by
`ATLAS_<PRODUCT>_MOCK=1` on a worker, seeded from the JSON file
`ATLAS_<PRODUCT>_MOCK_SEED` names.

It is a `driver.Connector` behind `sql.OpenDB`, so it sits exactly where a real driver
sits: the same `sqldb.Client`, the same `Run`, the same pool policy, the same
`database/sql` value conversion. Nothing in the connector knows which one it has, which
is what makes a mockup run evidence about the real path.

### Why not an engine

Options 1 and 2 are the tempting ones, because an engine makes `INSERT` then `SELECT`
behave, and the seeded table does not. Both were rejected on the same ground, from
opposite directions.

A **real engine that is not the real engine** (option 1) is a mock that disagrees with
the thing it stands in for exactly where a statement goes wrong: SQLite has no `TOP`,
different identifier quoting, different implicit conversion, no `@p1` positional
binding, a different collation model, and `MERGE` not at all. A model developed against
it would be developed against a dialect it will never run on — and the failures would
land in production, which is the outcome a mockup mode exists to prevent. It also costs
a dependency ADR-0173 explicitly promised not to add to.

A **hand-written subset** (option 2) is worse in the way that matters most: it is
guaranteed to be wrong somewhere, and nobody can say where. A mock that silently
mis-parses a `WHERE` clause and returns the wrong rows is not a degraded mock, it is a
confident liar, and the error surfaces as a business decision rather than a failure.

The seeded table cannot be wrong in that way, because **it does not interpret the
statement at all**. It matches it, and if it has no answer it says so.

### The refusal is the feature

The one rule that makes this trustworthy: **a statement nobody seeded fails, naming
itself and its bound parameters.** It never answers with no rows.

That asymmetry is deliberate. An empty result set is a *business answer* — a lookup
that found nobody, a leaver with no account, a query whose `WHERE` excluded everything
— and a process routinely branches on it. A mock that invents one hands the process a
fact, in the direction that is hardest to notice: the run looks like it worked. Failing
instead costs the operator one round trip to the seed file, and the error carries the
statement and the values so the answer can be pasted in.

### What it is faithful about

Where faithfulness is cheap, it is bought, on the AD mock's principle that a stand-in
accepting what the real thing refuses teaches a model to be wrong:

- **Parameters are bound, never interpolated.** The statement that reaches the mock is
  the one the author wrote, with the values beside it — so a seeded answer can be
  narrowed to one binding, and the journal records what was actually passed.
- **A seeded answer may fail.** `error` on an answer fails the statement, because the
  failures a database has are part of what a process must be tried against: a
  unique-key violation on a replayed create is not an edge case in an identity process,
  it is the delivery guarantee.
- **Values keep their types.** A seeded `1000000` arrives as an integer, not as the
  `1e+06` a float64 round trip produces and nothing matches — the same rule
  `jsonScalar` already enforces on a parameters variable.
- **There are no transactions.** `Begin` refuses, matching ADR-0173's connector exactly:
  one autocommit statement per task, nothing spanning two.
- **Statement identity ignores whitespace and case, and nothing else.** The Modeler's
  statement field wraps across lines and SQL Server folds case for keywords and, by its
  default collation, identifiers; a different column list is a different statement.

### What it is not

Stated plainly so it is not rediscovered as a surprise:

- **An `INSERT` does not change what a later `SELECT` returns.** Both are seeded. A
  process that writes and reads back sees what the seed says it sees. This is the cost
  of not having an engine, and it is why this is a mockup aid, not a test database.
- **Nothing is durable.** It is memory; a restart is an unseeded database.
- **A mock worker still serves only the names it was configured for.** The model names a
  Worker, so `ATLAS_<PRODUCT>_CONNECTORS` is still what a task resolves against — mock
  mode removes the DSN, not the name. A worker in mock mode with no names parks like
  any other unconfigured kind.
- **The Console can switch it** — see the amendment below. The original record shipped
  the environment variables only, and named a Console switch as the follow-up.

### The seed file

```json
{
  "answers": [
    {
      "statement": "SELECT id, mail FROM personen WHERE id = @p1",
      "params": [42],
      "columns": ["id", "mail"],
      "rows": [[42, "arno@example.com"]]
    },
    { "statement": "SELECT id, mail FROM personen WHERE id = @p1", "columns": ["id", "mail"], "rows": [] },
    { "statement": "UPDATE personen SET aktiv = 0 WHERE id = @id", "named": { "id": 42 }, "affected": 1 },
    { "statement": "INSERT INTO personen (mail) VALUES (@p1)", "error": "Violation of UNIQUE KEY constraint 'UQ_personen_mail'" }
  ]
}
```

An answer that names `params` or `named` answers only that binding; one that names
neither is the statement's fallback, whatever order the file lists them in. That is what
lets a seed say "person 42 exists and everyone else does not" without an engine.

A seed file that cannot be **read** starts an unseeded mock and warns, because the
supervisor restarts a child that exits and refusing over an optional path is the
restart loop [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) already paid
for once — and every statement then fails naming itself, which points at the missing
seed rather than hiding it. A file that is there and is **malformed** is refused, because
that is a typo in something the operator wrote and is fixed by being told.

### Consequences

- **Positive:** the SQL Worker Types stop being the only ones that cannot be tried. A
  model, its parameters variable, its placeholders and its result mapping are all
  exercised end to end before anyone has a connection string. The mock's journal answers
  "what would this process have done to the database", which is the question a review
  of a new SQL task actually asks. No dependency, no CGO, and the mock rides the same
  `database/sql` path as production rather than a second one.
- **Negative / trade-offs accepted:** a seed is written by hand and drifts from the
  statements when they change — the refusal makes that visible rather than silent, but
  it is still work. Write-then-read within one process is not modelled. The seed is
  matched, so a statement's *logic* is never checked; only its shape, its binding and
  its result mapping are.
- **Follow-ups / risks to watch:** a Console switch and a view of the journal, as the AD
  mock has (ADR-0213). Whether the whitespace-and-case-only matching rule stays right as
  people write longer statements.

## Amendment (2026-09-02): the switch is in the Console

The original record shipped `ATLAS_<PRODUCT>_MOCK` and nothing else, and said a Console
switch was a follow-up. This is that follow-up, and it is
[ADR-0193](0193-ad-mock-in-the-console.md) applied unchanged: the decision
that the mockup belongs to the *operator* rather than to the model stands — this only
moves where the operator reaches it, because a variable set once at start is the wrong
ceremony for a thing you flip while trying a process out.

**Console → Workers → Databases**: a checkbox and the prepared answers, stored as one
org-wide setting. Saving restarts the supervised SQL workers holding it; Atlas keeps
running.

Three things follow from the AD switch and are taken over wholesale.

- **Absence and a stored "off" are different states.** No record means nobody has decided
  in the Console, so whatever the server was started with keeps deciding; a stored record
  decides either way. That is what keeps an existing installation working exactly as it
  did until somebody touches the switch — and it is why a switch reading "off" while the
  worker still simulates is a state this cannot reach.
- **The seed is content, not a path.** The Console is org-wide and a path typed there
  belongs to whichever host happens to run the worker, which is the mistake
  [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) already corrected once. Atlas stores
  the JSON and writes the file the worker reads, named by a digest of its own content —
  which is not caching: the supervisor restarts a child only when its rendered
  environment differs, so a fixed filename would hand an unchanged `MOCK_SEED` to a
  worker that then kept answering from yesterday's seed.
- **The seed is parsed where somebody is looking.** `sqldb.ParseMockSeed` runs on save,
  so a typo is refused at the form with the seed's own complaint. The worker parses it
  again and degrades to an unseeded mock if it cannot, which stays right *there* — a
  restart loop over an optional file is the outage ADR-0202 paid for — and is the wrong
  answer *here*, where the person who can fix it is waiting.

### One switch, three products

As the AD switch covers every directory at once. Mocking SQL Server while really writing
to PostgreSQL is a half-state whose whole risk is that it looks like a full mockup run,
and one seed serves all three because a statement written with `@p1` and one written
with `$1` are different statements and cannot collide.

### What had to give way for it to work at all

Two rules were correct for a real database and wrong for a mocked one, and a switch that
did not move them would have been a checkbox with nothing behind it.

- **A worker record with no secret was left out of `CONNECTORS`.** That is right
  normally — a name the worker is told to serve with no DSN behind it is exactly the
  misconfiguration it refuses to start on — and in mockup mode it means the mockup
  serves no name a task can address. In mockup mode the name is rendered and the DSN is
  not.
- **Creating a database worker demanded a connection string.** Also right normally: it
  is the whole configuration, so a record without one is almost always somebody who lost
  the paste. In mockup mode it is a credential for a database nobody will dial — and it
  is precisely the state an operator is in when they turn the mockup on *because* they
  have no database. The demand now depends on the switch, which is why it moved out of
  the static validator table and into a method that can read it. A connection string
  given anyway is still sealed and kept: the mockup is a thing you turn off again.

### Consequences of the amendment

- **Positive:** trying a database process needs no deployment change and no restart —
  the path from "I have a model" to "I have watched it run" no longer passes through a
  unit file. The seed is validated where it is written and stored where the worker will
  find it.
- **Negative / trade-offs accepted:** the engine now stores a document that describes a
  production schema (statements and column names, and whatever rows an operator pasted).
  It is admin-only on read for that reason, like the AD seed. And there is one more place
  the answer to "is this thing mocked?" can come from — the switch is the authority when
  set, and the host environment when it is not, which is one indirection an operator has
  to hold.
- **Follow-ups / risks to watch:** the mock's journal — what a process actually asked —
  is still only in the worker's log. The AD mockup grew a Console view for exactly that
  ([ADR-0213](0213-ad-mock-directory-in-the-console.md)) and this will want one.

## Pros and cons of the options

### Option 1 — an embedded pure-Go SQL engine
- Good: `INSERT` then `SELECT` behaves; no seed to write or maintain.
- Bad: a different dialect, so a model is developed against SQL nobody will run; a new
  dependency ADR-0173 promised not to add; the disagreements surface in production.

### Option 2 — a hand-written micro-SQL engine
- Good: no dependency; some statements behave.
- Bad: certainly wrong somewhere and nobody can say where; a mis-parsed `WHERE` returns
  the wrong rows confidently, which is worse than no mock at all.

### Option 3 — a seeded answer table (chosen)
- Good: cannot mis-execute what it never executes; the refusal makes every gap visible;
  no dependency; failures are seedable, which an engine makes hard.
- Bad: the seed is written and maintained by hand; no write-then-read.

## Links

- extends [ADR-0173](0173-generic-sql-connector.md), whose worker-only seam this rides
- follows [ADR-0181](0181-ad-connector-mock-mode.md)'s rule that the mockup switch is the
  worker's and never the model's
- follows [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) on an unreadable
  optional file
- honors [ADR-0010](0010-go-and-no-cgo.md) by adding no dependency
