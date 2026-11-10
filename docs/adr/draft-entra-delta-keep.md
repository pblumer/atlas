# ADR-DRAFT: A delta query's keep is applied by the Entra Worker, before it answers

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-10
- **Deciders:** Atlas maintainers
- **Open question:** which properties Graph carries on an *updated* object of a `/users/delta` or `/groups/delta` page after the first. Microsoft documents "at least the updated properties"; the directory mirroring already reads it that way (`api/directorysync.go`, `directoryUser`). If a page carries only what changed, an update to a kept object whose keep field did not change arrives without it and is withheld as `undecided`. No run against a tenant has said how often that happens.
- **Question checked:** 2026-11

## Context and problem statement

The directory mirroring ([ADR-0332](0332-entra-directory-provisioning.md)) reads Microsoft
Graph's `/users/delta` and `/groups/delta` through the Entra Worker (`delta-users`,
`delta-groups`, [ADR-0172](0172-entra-id-connector.md)) and reports the change sets to
`POST /api/v1/directory-sync`. The first run has no cursor, so it enumerates the whole
tenant.

The M365 test environment (`deploy/opentofu/m365-devenv`) runs in a tenant it **shares
with real people** (`shared_tenant = true`). Its test users and groups carry the
environment's id — the users as their `department`, the groups at the start of their
`displayName` — and everything else in the tenant belongs to somebody real.

Run as it ships, the mirroring would put every person of that tenant into the test
server's event log. The order of events makes that unavoidable from inside the model: a
job's completion is written as `VariableCreated` events *before* any output mapping runs
(`engine/behavior.go`, `handleJobCompleted`; invariant I6 — events are facts). A model that
reads the delta and then drops the strangers has already recorded them. And the
directory-sync route cannot help either: it sees the batch after the log does.

So the question is where a filter can stand such that only the objects a process is
about ever reach Atlas.

## Decision drivers

- Nothing about a person who is not the subject of the process may reach the event log,
  the state store or a report — names, addresses, departments, ids.
- The filter has to apply to every run of one cursor in the same way, or a change could be
  read under one filter and lost under another.
- An author has to be told at deploy, not by an empty change set, when a filter cannot
  work.
- No new language for authors to learn beyond what Graph's own `$filter` already taught
  them, and nothing that evaluates arbitrary expressions inside the Worker.

## Considered options

1. **Filter in the model** — an output mapping or a script task after the delta.
2. **Send the filter to Graph** — `$filter` on the delta request.
3. **Ask for a list of ids** — Graph's delta does accept a filter on `id`.
4. **The Worker filters every page before it answers** (`keep` on the task).

## Decision outcome

Chosen option: **4, the Worker filters**, because it is the only place that sees the page
before the engine does.

The task names a predicate:

```xml
<atlas:entraConnector connector="…" operation="delta-users"
                      select="id,userPrincipalName,displayName,mail,accountEnabled,manager,department"
                      keep="department eq 'atlas-test-58o4' or startswith(userPrincipalName,'atlas-test-58o4-')"
                      deltaLink="=stand.usersDeltaLink" resultVariable="konten" />
```

### The language

It is deliberately tiny, and lives in `connector/entra/keep`, which imports nothing of
Atlas so the compiler and the Worker share it:

- `<field> eq '<literal>'` and `startswith(<field>,'<literal>')`;
- joined with `and` / `or`, `and` binding tighter, as in OData; parentheses group;
- keywords are case-insensitive (`startsWith` is `startswith`); a literal is
  single-quoted, a quote inside it doubled (`'O''Brien'`);
- a field is one top-level property: no paths, no lambdas, no negation, no other operator
  or function. `ne`, `not`, `endswith`, `in` and the rest are refused by name.
- bounded: 1024 bytes, 16 comparisons, nesting depth 8; an empty literal is refused,
  because `eq ''` matches nothing Graph sends and `startswith(…,'')` keeps everything.

**Comparison is case-insensitive.** Graph compares directory strings that way in a
`$filter`, and a keep that disagreed with the `$filter` an author knows would be a second
rule to remember. Only a string value matches: `null`, a number, a boolean, a list or an
object compares false.

**The literal is a literal, never FEEL.** Every other Entra attribute takes a leading `=`,
evaluated at job hand-over (the `filter` of a listing among them). `keep` does not:

- its fields must be checked against the `select` at deploy (below), which needs them
  known at deploy;
- one cursor is a single change stream across runs, and a predicate that could change
  between runs would filter one stream two ways — a group dropped by Monday's value is
  never sent again on Tuesday's;
- the environment it was built for writes the literal when it installs its copy of the
  model, so FEEL buys nothing there.

### What the compiler refuses

At deploy, a `keep`

- on any operation other than `delta-users` and `delta-groups` — a listing has Graph's own
  `filter`, which is strictly better because Graph does the reading;
- that begins with `=`;
- that does not parse, or names an operator or function the language lacks;
- on a task with no `select`, or reading a field the `select` does not name (`id` aside,
  which Graph returns on every object): Graph returns only selected properties, so such a
  field is absent from every page and every object would be withheld.

`keep` is a new attribute, so no model deployed before it can carry one; the refusals
cannot stop a server from loading what it already holds (ADR-0177, ADR-0393). A model that
carried a stray `keep=` attribute before this record — ignored then, and offered by no
Modeler — would now be refused at reload; that is accepted as practically nil.

### What the Worker does

`connector/entra/offload.go`, `deltaCollection`:

- every page is filtered as it arrives, before the next is fetched and before anything
  is answered; only kept objects are added to the result;
- **an object marked `@removed` is always kept, reduced to its `id` and `@removed`.** A
  deletion carries nothing to decide on, and the receiver needs the id. In a shared
  tenant that means the ids of strangers' deletions reach the result — ids only. The
  directory-sync route skips an id no account mirrors: `decideDeparture` records a
  `skipped` note ("reports this object as removed and no account here mirrors it") and
  writes nothing, and a removed group it does not know is skipped the same way
  (`api/directorysyncgroups.go`);
- **an object lacking a property the keep reads is `Undecided` and withheld.** Absent is
  not null: `null` is a value and compares false, absent is unknown, and `and` / `or`
  follow three-valued logic, so `department eq 'x' or startswith(displayName,'x')` still
  keeps an object that carries only `displayName`. Undecided is never kept — keeping it
  would let a stranger's change out — and it is counted;
- the **deltaLink is Graph's, unchanged**, so the next run reads the same change stream
  and the same filter applies to it;
- the result is `{value, deltaLink, withheld, undecided}`: `withheld` the objects not
  passed on, `undecided` those of them withheld only because the page did not carry a
  field the keep reads. Without a keep the result stays `{value, deltaLink}`, every
  object as Graph sent it — the default behaviour is untouched;
- **two bounds count two things.** The page bound (`maxListPages`, 1000 requests) counts
  requests to Graph, filtered or not, so a run whose every page is withheld still ends.
  `maxUsers` counts what is *kept* — what lands in the result variable, which is what it
  protects — so a keep over a tenant of fifty thousand does not fail on objects it never
  passes on. A result can therefore be smaller than one page, or empty with a fresh
  cursor; a keep that keeps more than `maxUsers` still fails the job rather than
  truncating;
- a job whose keep does not parse (built by hand, or by an older engine) fails; it never
  answers unfiltered. `keep` on a non-delta operation fails too.

### Consequences

- **Positive:** a test environment can mirror its own users and groups out of a tenant
  shared with real people, and nothing about those people reaches Atlas but the ids of
  their deletions. The filter is the Worker's, so it holds whatever the model does next.
  The compiler tells an author at deploy when a keep cannot work.
- **Negative / trade-offs accepted:**
  - **The Worker still reads the whole tenant.** Graph sends every object; the Worker
    holds one page at a time plus what it keeps, and only matches leave it. The Graph
    permission is still `User.Read.All` / `Group.Read.All`. A keep is a data-minimisation
    measure, not an access boundary.
  - **A change can be lost to `undecided`.** If Graph carries only changed properties on a
    later page (the open question above), an update to a kept user whose department did
    not change arrives without it and is withheld; a membership change on a kept group
    arrives without its `displayName`. `undecided` in the result makes this visible. The
    repair is ADR-0332's own: delete the `directory-sync` state record, and the next run
    enumerates afresh, with every selected property, and re-decides every object.
  - **Leaving the scope is not reported.** A user whose department changes away from the
    keep is, from then on, withheld — the server is never told, and the account stays
    mirrored. The Worker has no memory of what it kept before, by design (a stateless
    Worker Instance, scaled by adding more).
  - **A keep and its select travel with the cursor.** Graph threads `$select` into the
    deltaLink, so adding a field to the select for a new keep takes effect only on a fresh
    cursor; until then the field is absent from every page and everything is undecided.
    Change the select, then reset the cursor.
- **Scale-out:** nothing new to own. The predicate is compiled model data that travels
  on the job like `select`; the Worker Instance that applies it holds no state between
  jobs, so any instance may take any page of any cursor, and the cursor stays where
  ADR-0332 keeps it, in the server's `directory-sync` record. No engine, partition or
  run-loop path changes.
- **Follow-ups / risks to watch:**
  - Answer the open question against a tenant. If updates arrive partial, consider having
    the Worker re-read an undecided object by id (`GET /users/{id}?$select=<keep fields>`,
    or `getByIds` in batches) and decide it then — more Graph calls, the same privacy.
  - A keep for other change-tracking reads, should the Worker grow them.

## Pros and cons of the options

### Option 1 — filter in the model
- Good: no Worker change; FEEL can express any filter.
- Bad: too late. The job's result is written as events before an output mapping runs
  (I6), so every stranger is in the log the moment the job completes. A model cannot undo
  a fact.

### Option 2 — send the filter to Graph
- Good: Graph would read less, and the Worker nothing extra.
- Bad: Graph's delta query does not support `$filter` beyond `id`; `department eq` and
  `startswith(displayName,…)` are refused there. The compiler already refuses `filter` on
  a delta operation for that reason.

### Option 3 — a list of ids
- Good: Graph supports it, so Graph would do the filtering.
- Bad: it filters on who is known, and the mirroring exists to learn who is new. A test
  user created after the list was built would never be read; the list would also have to
  live somewhere and be kept current, which is the problem the mirroring solves.

### Option 4 — the Worker filters (chosen)
- Good: the only point that sees the page before the engine; holds whatever the model
  does next; checkable at deploy; the same filter for every page of one cursor.
- Bad: the Worker still reads everything; a partial page can leave an object undecided; a
  move out of the scope goes unreported.

## Links

- amends [ADR-0332](0332-entra-directory-provisioning.md) — the directory mirroring, and
  where a shared tenant's filter belongs
- extends [ADR-0172](0172-entra-id-connector.md) — the Entra Worker, `delta-users`,
  `delta-groups`, `select` and `maxUsers`
- rests on invariant I6 ([`docs/architecture/invariants.md`](../architecture/invariants.md)) —
  a job's result is a fact before the model sees it
- relates to ADR-0177 / ADR-0393 — a new compiler rule must not stop a reload
- relates to [ADR-0411](0411-system-processes-call-atlas-directly.md) — the REST shape the
  test environment's copy of the model uses to call Atlas
