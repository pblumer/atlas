# ADR-DRAFT: Task folders are saved filters, stored as rules and generated into FEEL

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

The Tasks app's sidebar has four folders, fixed since [ADR-0028](0028-forms-and-the-tasks-app.md):
All tasks, Assigned to me, Unassigned, Group tasks. They are predicates in
`app.js`, evaluated in the browser over whatever page happens to be loaded.

That covers "what is mine" and nothing else. The question a person doing the work
actually arrives with is about a *kind* of work — "what is open on customer
enquiries?", "what has gone past its due date?" — and today the answer is retyped
into the free-text box every morning, or approximated by sorting.

The request that prompted this record asked for user-created folders, "nothing but
filters the user builds themselves, ideally with FEEL", and then named the
constraint that decides the whole design: *end users are at work here — listboxes
and input fields, not strings.*

Two things about Atlas make this less obvious than it looks:

- **The task list is capped.** `GET /api/v1/tasks` returns at most 500 rows
  (`maxTaskListDefault`), a cap added after a flood parked hundreds of thousands of
  instances on one user task. A filter applied in the browser sees only that page,
  so a folder would report "no tasks" while matching work existed — and its badge
  would be a confident wrong number.
- **The task scan is on the single writer.** `handleListTasks` walks the
  activatable-job index inside `s.do`. Evaluating an expression per task there, and
  several per task for the sidebar's badges, is exactly the walk
  [ADR-0239](0239-off-loop-queries.md) removed from the growing queries.

The question this record answers: **what does a folder store, where is its rule
evaluated, and what does the person building one actually touch?**

## Decision drivers

- A person who does not model processes must be able to build and re-open one.
  That rules out any design whose stored form cannot be rendered back into
  controls.
- A folder's count and contents must be true, not true-of-the-loaded-page.
- Compilation belongs at save time, not per request (I5, [ADR-0008](0008-feel-expression-strategy.md)).
- A query that grows with the instance population must not hold the single writer
  (I3, ADR-0239).
- Nothing here may become a second authorization mechanism. A folder is a question
  about work the asker may already see.

## Considered options

1. **Store a FEEL expression.** The folder holds the text; the editor is a code
   field, or a builder that parses the text back.
2. **Store a structured rule; generate FEEL from it.** The rule is the source of
   truth, the expression is derived and shown.
3. **Store a rule and evaluate it directly in Go**, without FEEL at all.

## Decision outcome

Chosen option: **"Store a structured rule; generate FEEL from it"**, evaluated on
the server, off the run loop.

**The rule is the stored form.** A folder holds `{match: all|any, conditions:
[{field, op, value|values|unit}]}`. The field/operator catalogue lives in one place
(`api/taskfolder/rule.go`) and is published to the client, so the editor's first two
listboxes are drawn from the same table the validator and the generator use. A test
walks the catalogue and proves every advertised pair validates and compiles — the
editor cannot offer a row nobody can save.

**FEEL is the output.** `Rule.FEEL()` renders the expression; the editor shows it
under the conditions, live. It is one direction on purpose: a generated expression
can always be rendered back into the listboxes that produced it, and a hand-written
one cannot. Reading the expression is also how somebody learns what their folder
does — the choices they made in listboxes appear as the thing the engine evaluates.

**Compiled once, at save.** `POST`/`PUT` validate and `expr.CompileAuto` the
generated text, so a stored folder is one the FEEL compiler has already accepted; a
listing only evaluates. The compiled matcher is cached per folder keyed by its own
`UpdatedAt`, so an edit invalidates its own entry and there is no separate
invalidation to forget.

**Evaluated off the loop.** `?folder=<id>` on the task listing, the sidebar counts
and the editor's live preview all go through `Server.readOffLoop`: a snapshot plus a
copy of the deployment metadata taken on the loop, and the scan run beside it. The
scan is bounded (`maxFolderScan`); a page that hits the bound reports it, so a
number is a floor rather than a lie.

**One scan for every badge.** `GET /api/v1/task-folders/counts` evaluates every
visible folder per task in a single pass. A request per folder would multiply the
work by however many folders somebody happens to have made.

**The value lists come from the server.** Processes from the deployments, task
names and candidate groups and lanes from the compiled models, users from the
directory. This is what makes the "no strings" constraint real: a process id cannot
be mistyped, because it is never typed.

### Consequences

- **Positive:** a folder is legible after the fact — the editor reopens it as the
  rows that built it, and the sidebar shows the generated expression above the list.
  The counts are true against the whole open-task population, not the page.
- **Positive:** the filter's vocabulary is one Go table. Adding a field is an entry
  plus a message id, not a change in four places.
- **Negative:** the expression cannot be hand-edited yet. A rule the catalogue
  cannot express has no escape hatch; the FEEL text field is a later slice, and it
  will be a one-way door out of the builder when it lands.
- **Negative:** filtering costs a scan. Bounded and off the loop, but it is real
  I/O per sidebar refresh, and the bound means a very large population answers with
  a floor.
- **Follow-ups:** process variables as a filter field, against the
  [ADR-0244](0244-searchable-variables.md) value index — a variable is not on a task
  and reading one per row is a scope read the scan should not make. Reordering
  folders by drag. The FEEL escape hatch.

## Pros and cons of the options

### Store a FEEL expression
- Good: maximally expressive, and the stored form is exactly what runs.
- Bad: the editor is then either a code field — which the request specifically
  rules out — or a parser, and a parser that round-trips only some expressions is
  worse than none: the same folder comes back editable or not depending on how it
  was written.

### Store a structured rule; generate FEEL
- Good: always re-openable in the builder; the catalogue is one table three
  consumers share; the generated text is a teaching surface.
- Bad: only what the catalogue describes can be expressed.

### Store a rule and evaluate it in Go
- Good: no expression layer at all; marginally faster per task.
- Bad: a second condition evaluator beside the one the engine already has, with its
  own null and type rules to get wrong — and nothing to show the person, so the
  folder stays a black box.

## Links

- builds on [ADR-0028](0028-forms-and-the-tasks-app.md) (the Tasks app), [ADR-0042](0042-user-task-assignment-and-claim.md) / [ADR-0045](0045-user-task-assignment-bound-to-identity.md) (assignee), [ADR-0121](0121-bpmn-lanes.md) (lanes)
- rests on [ADR-0008](0008-feel-expression-strategy.md) (compile once), [ADR-0239](0239-off-loop-queries.md) (off-loop queries), [ADR-0147](0147-splitting-the-api-server-object.md) (a new API area is a service), [ADR-0180](0180-groups-as-members.md) (identity groups)
- interface language: ADR-draft-console-speaks-german-first
