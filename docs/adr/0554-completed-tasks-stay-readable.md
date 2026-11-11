# ADR-0554: A completed task stays readable to the person who completed it, in the form they answered

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-11
- **Deciders:** pblumer (maintainer; asked on 2026-11-11 that a closed task can be looked
  at again)
- **Open question:** who besides the person who completed a task may read it again: the
  members of the groups it was offered to, its assignee if somebody else completed it, a
  deputy. This record lets only the completer read it (§4), because a completed task
  shows an answer the others never saw while it was open. Widening that is a decision
  about disclosure that nobody has asked for yet with a concrete case.
- **Question checked:** 2026-11

## Context and problem statement

A person completes a task and later wants to see it again. They want to know what they
answered, what the form showed them, and which figures a report contained
([ADR-0555](0555-report-tasks-with-charts.md)). Today that is
not possible:

- **The Tasks app lists open tasks only.** `GET /api/v1/tasks/{key}` answers a completed
  task with 404 (`api/handlers.go`, `handleGetTask`). A connected system is answered 410
  with how and when the task ended, and nothing more
  ([ADR-0512](0512-connected-systems.md), `api/taskreach.go`).
- **Nothing finds a person's completed tasks.** The job row is deleted when the job ends
  (`engine/apply.go`). The event feed keeps one row per turn in a task's life, but it is
  pruned and keyed by log position, not by person ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5).
  Who completed a task is written as the variable `completedBy` (`api/taskcompleter.go`).
  A variable is process data: the next completion in the same scope overwrites it, and
  nothing indexes it.
- **The form may have changed since.** Forms are referenced by id and stored in a
  mutable store (`api/forms.go`). Saving a form overwrites it; there are no revisions.
  Showing an old answer in today's form can show a field that did not exist or hide one
  that did.
- **The answer itself is kept.** The completion's variables are frozen into
  `VariableCreated` events attributed to the task's element instance
  ([ADR-0219](0219-variable-write-attribution.md)). The instance history keeps the
  variables as they stood before and after each element
  ([ADR-0048](0048-per-step-variable-snapshots.md),
  [ADR-0542](0542-variables-by-provenance.md)) for as long as the instance's history
  lives ([ADR-0085](0085-process-instance-ttl.md),
  [ADR-0146](0146-history-expiry-due-date-index.md),
  [ADR-0486](0486-operator-history-purge-and-compaction.md)). An operator can already
  see it there in the replay.

The question: **how does the person who completed a task find it again and see it as
they answered it, without making the history readable to people the open task was not
addressed to?**

## Decision drivers

- **Who completed a task is a fact, not a variable.** It must survive the next
  completion in the same scope, and be indexable by person.
- **One `applyToState` (I4), facts in events (I6).** An index must be folded from an
  event in the one function, live and on replay alike. It must not be written beside
  the log by a handler.
- **No allocation on the hot path (I1).** A user task's completion is human-paced, but
  it is folded in the batch cycle like every other record.
- **The form as it was.** A completed task is shown in the revision of the form the
  person answered.
- **Nothing is kept longer than the history.** A completed task is readable while its
  instance's history exists and goes with it. No second retention policy.
- **Showing is not answering.** A completed task is read-only. It cannot be reopened:
  its token has moved on.
- **Disclosure stays where it was.** Reading a completed task must not show anybody an
  answer they could not have seen before.
- **The existing contract holds.** `GET /api/v1/tasks/{key}` keeps answering 404 for a
  completed task. Clients rely on it.

## Considered options

For how a person's completed tasks are found:

1. **A new fact `TaskAnswered`, appended with every completion of a user task by a
   person, folded into an index by person.**
2. Fold the index from the `VariableCreated` event named `completedBy` whose producer is
   a user task.
3. Read the event feed and filter by completer.
4. A store beside the log, written by the completion handler after the completion is
   durable.

For the form:

1. **The form store keeps every saved schema as an immutable revision; the fact names
   the revision that was answered.**
2. Show today's form and mark that it may have changed.
3. Freeze the forms into the deployment.

## Decision outcome

Chosen: **a `TaskAnswered` fact (1); form revisions (1); the completer reads it (§4).**

### 1. The fact

A new value type, `VTTaskAnswer`, with one intent, `TaskAnswered`. It is appended by
`handleJobCompleted` (`engine/behavior.go`) in the same batch as `JobCompleted` when:

- the job is a user-task job, and
- the command carries a completer.

The handler that completes a task from the Tasks app, a link or MCP puts the completer
on the command, as `CompleteJobManually` puts its actor. `withCompleter` reads the same
principal.

The value carries:

- `ProcessInstanceKey`, `ElementInstanceKey`, `JobKey`;
- `ProcessDefKey` and `ElementId`, read from the element instance as `putTaskFeedRow`
  reads them, because the element instance is gone once the element completes;
- `Completer`, the principal id;
- `FormId` and `FormRevision` (§2);
- `Assignee` and `CandidateGroups` as the job held them when it ended.

The time is the record header's timestamp (I6).

It is **not** appended:

- for a manual completion by an operator ([ADR-0159](0159-manual-task-completion-audit.md)),
  which already has its own audit fact and is not the operator's answer;
- with authentication off, when there is nobody to name;
- for a cancelled task.

`completedBy` stays as it is. Models read it, and this record does not change what a
model sees.

Option 2 is refused because it ties a fold to a variable's name and to what a producer
happens to be. A worker output or a script that writes `completedBy` would have to be
told apart from a completion. Option 3 is refused: the feed is pruned on its own
schedule, keyed by position, and reading it per person is a scan. Option 4 is a side
effect nobody replays: a crash between the commit and the write loses the row.
[ADR-0312](0312-portal-catalogue-order-inventory.md) already argues against keeping
evidence that way.

### 2. Form revisions

- The form store keeps every saved schema as an **immutable revision**, addressed by the
  SHA-256 digest of its bytes. Saving appends a revision and moves the form's current
  pointer. Saving an unchanged schema adds nothing.
- The Tasks app sends the revision it rendered with the completion
  (`formRevision`). The server accepts it only if it is a revision of the task's form,
  and otherwise records the current revision. A caller that sends none, such as an older
  client or a plain API call, gets the current revision recorded.
- Revisions are design-time data like the forms. They go into the design-time backup
  ([ADR-0107](0107-backup-and-restore.md)) and are deleted with their form.
- A completed task whose revision is gone (the form was deleted) is shown as a plain
  list of its answers under their field labels, with a note saying the form no longer
  exists. It is not shown in a different form.

Option 2 is refused because showing an answer in a form it was not given in misstates
it. Option 3, forms frozen into the deployment, is the stronger rule for open tasks too:
an open task of an old version would keep its old form. It changes what open tasks show
today and is a decision of its own. Revisions are its prerequisite, so this record does
not close it off.

### 3. The index and what is shown

- `applyToState` folds `TaskAnswered` into two rows. Both use the batch's scratch
  buffers, as the feed's rows do (I1):
  - `doneTask:<completer>:<endedAt>:<partition>:<jobKey>` holds the value. It is the
    person's list, newest first.
  - `doneTaskOf:<piKey>:<jobKey>` points back, so a purge of the instance's history
    finds and drops both. `PurgeInstanceHistory` gets the prefix in its list, as
    [ADR-0549](0549-collection-by-round.md) added its family there.
- **What the view shows:**
  - the form revision, rendered read-only (form-js `readonly`);
  - filled with the variables as they stood once the task completed, read from the
    instance history (ADR-0048), with the task's own writes (ADR-0219) marked as the
    answer;
  - display-only components (`mailview`, `chart`, disabled fields), which show what they
    showed at completion.

  If a variable changed while the task was open, the view shows its value at completion,
  which may differ from what was on screen when the task was opened. That is stated in
  the view's help text and not hidden.
- **Personal values** ([ADR-0314](0314-portal-personal-data.md)) are deciphered under the
  same rule as for an open task. A value whose key was destroyed shows as destroyed.
- **Retention:** a completed task is listed and readable for as long as its instance's
  history exists, and no longer. The list says so in one line.
- **Before the upgrade:** completions before the upgrade have no `TaskAnswered` and are
  not listed. There is no backfill: the completer is not recoverable as a fact from older
  records.

### 4. Who may read it

- **The completer.** The list holds only the tasks the caller completed. Opening one by
  key answers only its completer, within the caller's project reach (`inProjectsOnLoop`,
  as for an open task). Anybody else is answered 404, as if the task did not exist.
- **Operators** keep reading the instance in the replay, as today. That path and its
  rights do not change.
- Group members, an assignee who did not complete the task, and deputies are the open
  question.

### 5. API, Tasks app, MCP

- **Routes:**
  - `GET /api/v1/tasks/completed` lists the caller's completed tasks, newest first,
    paged by a cursor, with `?process=`, `?from=` and `?to=`.
  - `GET /api/v1/tasks/completed/{key}` answers the task's metadata, the form revision's
    schema and the values of §3.

  Both are read off the run loop. `GET /api/v1/tasks/{key}` is unchanged.
- **The Tasks app** gets a view *Erledigt* beside the folders
  ([ADR-0268](0268-task-folders-are-saved-filters.md) folders are filters over open tasks,
  so this is a view of its own and not a folder):
  - newest first, with filters for process and period;
  - opening an entry shows the read-only form under the line *Erledigt am … durch Sie*;
  - print and *Save as PDF* as in ADR-0555 §6;
  - no action that changes anything. A correction is something the model offers, never
    a reopened task.
- **MCP** ([ADR-0016](0016-mcp-server-over-http-api.md)) gets `atlas_list_completed_tasks`
  and `atlas_get_completed_task`, acting as the connected person, so an agent working for
  a person can look up what that person answered.

### As built

- **CT1 (2026-11-11):**
  - The fact is `model.TaskAnswerValue` (`model/taskanswer.go`), value type
    `VTTaskAnswer`, intent `IntentTaskAnswered`. It carries the element by its compiled
    index and the form by its revision only: the form's id is the compiled element's,
    and element ids are never written to the log as text (I5).
  - `Processor.CompleteUserTask` puts the completer and the form revision on the command
    (`Command.Answer`, allocated by the API off the run loop). `handleJobCompleted` fills
    in the keys, the definition and the element, and appends the fact after the
    completion's variables and before the element completes. It is not appended for a
    forced completion, without a completer, or for a job that is not a user task's.
  - The fold is `state.Tx.RecordTaskAnswer` (`state/taskanswer.go`). The fact is stored
    under the instance with the record's timestamp **and log position**
    (`cfTaskAnswer` 0x49). The position is what the view reads the history up to. The two
    indexes are `cfTaskAnswerByCompleter` 0x4A and `cfTaskAnswerByJob` 0x4B. The
    instance purge drops all three. The fold allocates its keys as the other audit
    families do (`RecordOperatorAction`, `RecordAuthorization`). The processor itself
    allocates nothing for the answer.
  - Form revisions: `formStore` wraps the sidecar store. `Save` records the schema as a
    revision in the sibling directory `form-revisions` (design-time, in the backup), and
    `Delete` removes the form's revisions. A rename keeps the old id's revisions. A
    completion records the current schema as a revision if it is not one yet, so a form
    saved before revisions existed has one once it is answered.
    `GET /api/v1/forms/{id}` answers `revision`; the Tasks app sends it back as
    `formRevision`.
  - Completions recorded as answers: the Tasks app's completion
    (`POST /api/v1/tasks/{key}/complete`, which MCP's `atlas_complete_task` also uses)
    and the approvals decision. A task answered through a mail link or a public wizard
    has no signed-in person and is not listed.
  - The view reads the process scope and the task's own scope from the variable history
    up to the answer's position, the inner value winning. It shows what the form
    revision binds (`collectFormFieldKeys`, the open task's rule), and without a kept
    revision only the variables the task's element wrote. **Not covered:** a task inside
    an embedded subprocess whose form binds a variable of the subprocess's own scope.
    That variable is not shown, because the scope chain between the task and the process
    is not walked.
  - Tasks app: a view *Erledigt* / *Completed* beside the built-in folders
    (`api/web/completedtasks.js`), shown only with authentication on.
  - MCP: `atlas_list_completed_tasks`, `atlas_get_completed_task`.
  - **Not built yet:** print and *Save as PDF* for a completed task. They come with
    ADR-0555 §6, which draws both for every task form.

### Consequences

- **Positive:**
  - A person can look up what they answered, as they answered it, for as long as the
    history exists.
  - Who completed a task becomes a fact an audit can rely on, independent of the
    variables.
  - Forms get revisions, which is also the prerequisite for freezing forms per
    deployment.
  - A report task can be read and printed again after it was acknowledged.
- **Negative / trade-offs accepted:**
  - **The engine changes.** There is a new value type, a new case in `applyToState` and
    a new column family. That is the price of an index that recovery rebuilds. The
    alternatives either do not survive a crash or couple a fold to a variable name.
  - The log grows by one small record per completed user task.
  - The form store grows with every saved change.
  - Completions before the upgrade are not listed.
- **Follow-ups / risks to watch:**
  - Who besides the completer may read (the open question).
  - Cancelled tasks in the view.
  - Freezing forms per deployment, now possible on revisions.
  - Pruning revisions that no listed task and no current form references, if form
    storage ever matters.

## Pros and cons of the options

### `TaskAnswered` fact
- Good: an indexable, replayed fact; independent of variables; carries the form
  revision.
- Bad: an engine change (value type, fold, column family, coverage tests).

### Fold from `completedBy`
- Good: no new event.
- Bad: a fold coupled to a variable name and to a producer kind; any writer of that
  name is suspect.

### Event feed
- Good: already holds task completions.
- Bad: pruned on its own schedule; keyed by position; a scan per person; no completer.

### Store beside the log
- Good: no engine change.
- Bad: not replayed; a crash loses rows; the argument ADR-0312 made against it.

### Form revisions
- Good: the answer is shown in the form it was given in; prerequisite for per-deployment
  forms.
- Bad: the form store grows; a deleted form leaves only a plain list.

### Today's form
- Good: nothing to build.
- Bad: misstates old answers.

### Forms frozen into the deployment
- Good: open and completed tasks both show the form of their version.
- Bad: changes what open tasks show today; a decision of its own.

### Scale-out assessment

- **State owner:** `TaskAnswered` and both index rows belong to the partition that owns
  the instance. Nothing global is added.
- **Routing and boundary:** a person's list spans partitions. The read merges each
  partition's rows by `(endedAt, partition, jobKey)`, and the cursor carries a position
  per partition. Standalone mode has one partition and reads one. No route assumes
  `instanceKey % partitionCount` or reads another partition's state from the loop.
- **Failure and retry:** the fact is appended in the batch that completes the job, so
  both are durable together or neither is (I2). A second completion command finds no
  job and appends nothing.
- **Consistency and visibility:** the list reads committed state off the loop. A
  completion is listed once it is durable, never before. Across partitions the list is
  eventually complete, and a page states nothing more.
- **Standalone and migration:** the routes' contract does not depend on placement. A
  multi-node Atlas fans the read out over the partitions' owners.
- **Verification:**
  - a recovery test that replays a completion and finds the same rows;
  - a purge test that drops both rows with the history;
  - an allocation test for the fold;
  - route tests for completer, non-completer (404) and project reach;
  - a form-revision test for an answer given before the form changed;
  - the multi-partition merge is deferred to Milestone 5.

## Links

- extends the Tasks app of [ADR-0028](0028-forms-and-the-tasks-app.md); keeps the open
  task's contract of [ADR-0512](0512-connected-systems.md)
- reads the history of [ADR-0048](0048-per-step-variable-snapshots.md),
  [ADR-0219](0219-variable-write-attribution.md) and
  [ADR-0542](0542-variables-by-provenance.md); lives and goes with it
  ([ADR-0085](0085-process-instance-ttl.md), [ADR-0486](0486-operator-history-purge-and-compaction.md))
- beside the manual-completion audit of [ADR-0159](0159-manual-task-completion-audit.md)
- lets a report be read again: [ADR-0555](0555-report-tasks-with-charts.md)
