# ADR-0292: A MIM import hands over its rows as data, and counts them as work

- **Status:** Accepted
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers
- **Open question:** MIMWAL's grid column semantics are not established from its own
  source. The positional cells, and the refusal to name a target column in
  machine-readable markup, rest on that gap; answering it makes `target`/`allowNull`
  an additive change.
- **Question checked:** 2026-09

## Context and problem statement

`mimimport` converts a MIM/FIM XOML workflow into Atlas-deployable BPMN. Its
governing rule is that nothing is silently dropped: a construct without a faithful
BPMN counterpart is still emitted, its markup preserved verbatim in
`<atlas:mimSource>`, and a `Report` names every lossy point.

That rule held for *structure* and broke down for *substance*. A MIMWAL activity
keeps its actual work in serialised .NET collections hung off the element as WF
property elements: an `UpdateResources` carries a `QueriesTable` of named queries
and an `UpdatesTable` of assignments, a `GenerateUniqueValue` carries
`ValueExpressions` and an `LdapQueriesTable`. One such activity out of a real
workflow:

```
UpdatesTable (5 rows)
  [0] IIF(ConvertToBoolean([//Target/AssignExchangeMailbox]), …) | [//Target/SetOutOfOffice]      | false
  [1] IIF(Or(ConvertToBoolean([//Target/AssignExchangeMailbox]), …),TRUE,Null()) | [//Target/HideFromAddressList] | false
  [2] IIF(IsPresent([//Target/PersonType/JobsEndDateReached]),710,800) | [//Target/ObjectLifeCycleStatus] | false
  [3] IIF(IsPresent([//Target/PersonType/JobsEndDateReached]),"Grace Period Job Execution","In Grace Period") | … | false
  [4] TRUE | [//Target/InGracePeriod] | false
QueriesTable (1 row)
  [0] OutOfOffice | /Feature[starts-with(DisplayName, '%Out Of Office Management')] | false
```

These rows were decoded and rendered as a readable table on the step's
`<documentation>`. Two properties that a migration needs were still missing.

**They were not addressable.** A person could read the table; a tool could not take
one row from it without re-parsing the escaped XOML in `atlas:mimSource` and
re-implementing the Hashtable decoding that the importer had already done.

**They were not counted.** The activity above reported as a single line —
`[preserved] … resource operation kept in atlas:mimSource` — whatever it carried
inside. Six reads and writes, each of which has to be re-expressed against a real
target system, entered the plan as one item. A worksheet that under-reports the
work by a factor of six is worse than no worksheet, because it is used.

Behind both sits a question that had to be answered first: **should the importer
put those rows into the BPMN graph** as one task per read and per write? That is
the shape the runtime wants — an Atlas job is one operation (ADR-0007/0067/0168) —
and it is the shape the migrator ends up building by hand.

## Decision drivers

- **Nothing is asserted that was not read out of the source.** The importer's value
  rests on a reader being able to trust what it produces; a structure it invented is
  indistinguishable, in the output, from one MIM wrote.
- **The number a migration is planned with must be the honest one.**
- **A tool should not have to re-implement the decoding** the importer already did.
- **The existing renderings must survive**: the verbatim source and the readable
  table are what makes a preserved node reviewable at all.
- **Re-import stability.** Node ids come from the activity's `x:Name` so a re-import
  of a workflow that gained a step does not renumber the steps it already had.

## Considered options

1. **Leave it as documentation text.** Decoded, readable, not machine-readable, and
   counted as one item per node.
2. **Split each row into its own flow node**, with a gateway where the row's value
   expression is conditional.
3. **Emit the decomposition as extension elements on the node, and one Report item
   per row.** The graph keeps one node per MIM activity.
4. **Option 3 plus an opt-in `-split-updates` flag** producing option 2 on request.

## Decision outcome

Chosen option: **3**.

Each decoded collection is emitted alongside the preserved source as

```xml
<atlas:mimCollection property="UpdatesTable" kind="table" count="2">
  <atlas:mimRow index="0">
    <atlas:mimCell column="0">[//Queries/AllGroups]</atlas:mimCell>
    <atlas:mimCell column="1">[//WorkflowData/AllGroups]</atlas:mimCell>
    <atlas:mimCell column="2">false</atlas:mimCell>
  </atlas:mimRow>
</atlas:mimCollection>
```

and contributes one `manual-review` item per row to the `Report`, which therefore
counts *work* rather than BPMN elements. An `ArrayList` is emitted as `kind="list"`
with one single-cell row per entry, so a consumer walks both shapes the same way.
Cell text is verbatim with only outer whitespace trimmed: a MIM expression can hold
a string literal whose spacing is part of its value, so the one-line collapsing that
keeps the documentation table readable stays a rendering and never the value.

**Option 2 is rejected on evidence, not taste.** Which target system a row writes to
is not in the XOML at all — all five rows above write to `[//Target/…]`, the same
MIM resource, and the routing to AD, Exchange and the identity store lives in MIM's
sync rules and attribute flows, which the workflow export does not contain. MIM
applies the whole table as one request. Splitting the rows into flow nodes would
therefore put a structure into the diagram that the source does not have, and would
carry the untranslated per-activity guard (`condAlways`, see `convert.go`) and the
placeholder `Iteration` onto every one of them: five gateways that always take the
execute branch. The result looks precise and is exactly as unexecutable as before —
the failure mode this package avoids everywhere else.

**Option 4 is rejected as unpaid-for surface.** The manual work after the split is
about the same, because the cut a migrator actually needs is by target system and
that cut is not derivable here; a second output shape would double the test surface
of a tool that runs once per workflow.

The same restraint governs the inside of the structured form: a cell states which
column it sat in, never what that column means. MIMWAL's editor labels the updates
grid Target | Value | Allow Null, and the one real workflow checked against it does
not bear that out (column 1 holds a literal in nine rows, column 0 a query result in
six). With no reference that settles it, `target="…"` in machine-readable markup
would make an unverified reading look settled — and a consumer would act on it.

### Consequences

- **Positive:** the rows are addressable by a tool, checkable against the preserved
  source, and countable. The status counts in the report, the model's own
  `<documentation>`, the CLI help and the Console's import modal all describe items
  of work. A `Count` that disagrees with the decoded rows — the case that means a
  row went missing — is an item too, not only a line of prose.
- **Positive:** node ids and the graph are untouched, so re-import stability and the
  existing diagram are unaffected by this change.
- **Negative / trade-offs accepted:** the report is longer, and a single activity can
  contribute a dozen items. That is the honest length. The BPMN also grows by roughly
  the size of the decoded cells, which is small next to the escaped source it sits
  beside.
- **Negative:** positional cells are less useful than named ones. A consumer that
  wants "the target attribute" must still decide which column that is.
- **Follow-ups / risks to watch:** if MIMWAL's grid semantics are established from
  its own source, `atlas:mimCell` can gain `target`/`allowNull` additively — the
  structure is already there. A deploy-time warning for a model that still carries
  unworked `atlas:mimCollection` rows is a plausible next step and is deliberately
  not part of this record.

## Pros and cons of the options

### Option 1 — documentation only
- Good: nothing to design, nothing to maintain.
- Bad: the decoding is done and then thrown away at the boundary; the plan is made
  with a count that is wrong by the size of the tables.

### Option 2 — one flow node per row
- Good: matches the Atlas execution model, where one job is one operation; each write
  would get its own retry, incident and timeline entry.
- Bad: asserts a structure the source does not contain; multiplies the untranslated
  guard and iteration placeholders; the migrator re-cuts it by target system anyway.

### Option 3 — structured elements plus per-row items (chosen)
- Good: hands over everything that was decoded, asserts nothing that was not, leaves
  the modelling decision with the person who has the sync rules.
- Bad: the output is a worksheet, not a runnable model — which is what this importer
  is, and now says.

## Links

- relates to [ADR-0007](0007-job-worker-protocol.md), [ADR-0067](0067-service-task-connector-catalog.md),
  [ADR-0203](0203-worker-execution-model.md) — one job is one operation, which is why
  the per-row split is tempting
- relates to [ADR-0018](0018-test-driven-development.md) — the shapes here were pinned
  test-first
- context: [`docs/comparisons/mim.md`](../comparisons/mim.md) — why an Atlas process
  must model what MIM does declaratively
