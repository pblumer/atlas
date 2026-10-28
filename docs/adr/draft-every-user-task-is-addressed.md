# ADR-DRAFT: Every user task is addressed, and whoever holds it sees where its instance stands

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-28
- **Deciders:** Atlas maintainers (requested 2026-10-28 by the maintainer: every user task is to
  be assigned to people or groups, a lane may stand for a group, the Tasks app shows each person
  what they are to work on, and a model that addresses nobody is refused rather than warned about)
- **Open question:** how many deployed models on running installations hold user tasks addressed
  to nobody. The repository's own BPMN files hold 27 such tasks outside wizards in 18 files
  (examples, templates, playground and script test data), and its Go tests inline about 190 user
  tasks in 104 files; the estates of installations were not counted. The hard refusal rests on the
  count being small enough to fix the models rather than to keep a switch.
- **Question checked:** 2026-10

## Context and problem statement

A user task names who it is for with `zeebe:assignmentDefinition`: an `assignee`, `candidateGroups`,
or both, as literals or as expressions evaluated at activation
([ADR-0042](0042-user-task-assignment-and-claim.md), [ADR-0045](0045-user-task-assignment-bound-to-identity.md),
[ADR-0318](0318-user-task-assignment-expressions.md)). A user task that names nobody is, by the line
[ADR-0317](0317-task-commands-are-an-object-question.md) draws, open work: `taskVisibleTo` lists it to
every signed-in account and `mayWorkTask` lets every signed-in account claim and complete it
(`api/taskauthority.go`). Every signed-in account holds the role `user`, portal accounts included.

The read side draws a different line. [ADR-0275](0275-instance-visibility.md) lets somebody who holds a
task read the fields of that task's form, and nothing else of the instance. `holdsTask`
(`api/instancescope.go`) answers *false* for a task with neither an assignee nor candidate groups, and a
task with no form contributes no field. The Tasks app's *Process* tab reads
`GET /api/v1/instances/{key}/timeline` (`api/web/editor.js`), which needs the role `operator`.

So the account that may complete an unaddressed task may read nothing of the instance it completes.
On a running installation, on 2026-10-28, a person with the role `user` was offered the task *Account
anlegen* of `proc_service_account_strang`: no assignee, no candidate groups, no form, a definition
filed under no application. Their *Variables* section answered "no instance with that key" and the
*Process* tab "the operator role is required". They could have completed it.

Lanes already partition a process by who is responsible ([ADR-0121](0121-bpmn-lanes.md)). Its layer A
is built: each node's lane is recorded and shown in the Tasks app. Its layer B, a lane naming an Atlas
group by id that a user task without groups of its own inherits, is designed there and not built
(`atlas:laneGroup` appears nowhere in the code). Its layer C, access control by lane, was left for a
record of its own.

Two facts bound the question:

- An assignment *expression* that evaluates to nothing is already refused at activation: the element
  parks with a job-less incident and never creates a job addressed to nobody (ADR-0318). Only a task
  whose model names nobody at all reaches the inbox as open work.
- A step of a wizard ([ADR-0449](0449-a-wizard-is-a-subprocess-one-filler-walks-through.md)) is the
  filler's. In a `public` wizard an assignment is refused (`wizard.public-assignment`); in an
  `internal` wizard the continuation claims an unaddressed step for the filler. The starter of an
  instance is recorded (`ProcessInstanceValue.StartedBy`) but is not a variable a model can address.

The question is how a person sees the work that is theirs and only that, and what they see of an
instance while they hold one of its tasks.

## Decision drivers

- A task belongs to somebody the model names. "Anybody signed in" is not an answer an installation with
  portal accounts can live with.
- Whoever may complete a task may see what they are deciding: the form's fields and where the instance
  stands. Nothing more of the instance than that.
- The diagram stays the place a modeller says who is responsible; a lane says it once for many tasks.
- Compile, don't interpret (I5): what can be refused at deploy is refused at deploy.
- Running instances are not changed under anybody: a decision about models applies to what is deployed
  next.

## Considered options

1. **Refuse a user task that names nobody at deploy**, with lanes naming groups (ADR-0121 layer B) as a
   way to name them, and a reduced view of the instance for whoever holds a task.
2. **Warn at deploy, keep unaddressed tasks open to every account.** The status quo with a hint.
3. **Keep the models as they are and treat an unaddressed task as operators' work** at runtime: no
   account but operators and administrators sees or works it.
4. **Lane equals group by name**, without an explicit reference.

## Decision outcome

Chosen option: **1**, because it puts "who is this for" where the modeller is looking, fails where it
can still be fixed, and leaves no task at runtime whose holder is everybody.

### A user task must name somebody

- A new validation rule `usertask.unaddressed` (`compiler/validation.go`), severity **error**: a user
  task that names no assignee and no candidate groups, as a literal or an expression, and inherits no
  lane group, refuses the deployment. The message names the task and says how to address it: an
  assignee, candidate groups, or a lane that names a group.
- **Exempt:** a step of a wizard. A `public` wizard's step must stay unaddressed (its holder is the
  pass holder); an `internal` wizard's step is the filler's through the continuation of ADR-0449.
- The Modeler's Problems panel ([ADR-0026](0026-problems-panel-and-versioned-validation.md)) shows the
  rule while the model is edited, as it shows every other error.
- What is deployed stays as it is. A version deployed before this rule keeps its unaddressed tasks as
  open work under ADR-0317 until it is redeployed or its instances finish; a redeploy is checked.
- The rule runs wherever a model is compiled for deployment. Generated models (`api/formgen`,
  `mimimport`) emit an addressed task or a lane with a group, or name the gap in their own output.

### A lane names a group (ADR-0121 layer B, built as designed)

- `<atlas:laneGroup groupId="…"/>` in a lane's `extensionElements` names an Atlas group by id. At
  compile, a user task in that lane (its leaf lane, or the nearest enclosing lane that names a group)
  with no candidate groups of its own inherits the group into `UserTaskDetail.CandidateGroups`. An
  explicit `assignee` or `candidateGroups` on the task wins.
- The deploy handler refuses a `groupId` that names no group (the compiler is pure and cannot know; the
  handler can). A group deleted later leaves its tasks to operators, as a deleted assignee does today.
- Literal candidate groups stay free text matched by id or name, as `holdsTask` reads them. Whether a
  literal that matches no group should warn is left to a later record.
- The Modeler offers the group of a lane as a picker over `GET /api/v1/principals`, which lists every
  group to any signed-in account for the member and assignee pickers, beside the lane's name.

### The holder of a task sees where the instance stands

- `holdsTask` and `taskVisibleTo` draw one line: whoever the Tasks app lists a task to, and who may
  work it, is who may read its form's fields. For an unaddressed task of an old version, that is
  everybody it is listed to.
- A new route `GET /api/v1/instances/{key}/progress` (role `user`, object check in the handler) answers
  the instance's state and, per element of its definition, whether it is completed, active, or neither,
  with the element's name and the holder's own task marked. It carries no actor, no timestamp and no
  variable. It is answered to operators and administrators, to members (viewer or better) of the
  instance's application, to whoever holds a task on it, and to whoever the record on starting
  ([ADR-draft-starters-start-processes-and-follow-their-instances](draft-starters-start-processes-and-follow-their-instances.md))
  lets follow it; to anybody else it answers 404, as ADR-0275 does.
- The Tasks app's *Process* tab reads `progress` for a caller without the operator role and draws the
  model (`GET /api/v1/processes/{key}/xml`, already open to every account) with the completed and
  active elements marked and the holder's task highlighted. An operator keeps the timeline.
- `GET /api/v1/processes/{key}/runtime?instance=…` answers the same set of callers as `progress`. Read
  at commit 98c1a19, the route is open to every signed-in account and `serveProcessRuntime` asks no
  object question, so today any account can read where any instance's tokens are by its key. The
  definition-wide reading without `?instance=` and `GET …/statistics` stay as they are: they count,
  they name no instance (ADR-0496: no statistics about people).

### Consequences

- **Positive:** every task deployed from now on is somebody's; the Tasks app lists to each person what
  they are to work on and nothing addressed to others; a person who may complete a task sees its
  fields and where its instance stands; a lane says once who is responsible for its tasks.
- **Positive:** the read and write halves of "who holds this task" are one rule again, and the
  per-instance runtime overlay stops answering accounts that may not read the instance.
- **Negative / trade-offs accepted:** models with unaddressed tasks are refused on their next deploy:
  in this repository the 18 files named above, and the inline test models that contain a user task, need
  an assignment or a lane group; installations will find their own on redeploy. A refusal is louder
  than a warning on purpose.
- **Negative:** `atlas:laneGroup` is an Atlas extension, not standard BPMN (as ADR-0121 accepted).
- **Negative:** the progress view shows a holder which branches the instance took, which can say
  something about earlier decisions (an approval path taken). It shows no values and no people.
- **Follow-ups / risks to watch:** addressing an internal wizard's steps to `StartedBy` explicitly,
  rather than through the continuation; a warning for literal candidate groups that match no group;
  ADR-0121 layer C (access to an instance by lane) stays open.

## Pros and cons of the options

### Option 2: warn, keep unaddressed tasks open

Strongest form: nothing breaks; a shared inbox where anybody picks up the next job is a legitimate way
to work, and BPMN does not require an assignment.

- Good: no model, test or installation has to change.
- Bad: the warning does not change who can complete the task: every signed-in account, portal
  customers included. ADR-0317 already found that an approval anybody can complete is the absence of an
  authorization model; a warning leaves that hole to each modeller's attention. A team that wants a
  shared inbox names its group.

### Option 3: unaddressed tasks become operators' work

Strongest form: no model needs to change, and the hole closes at once on every installation.

- Good: immediate, uniform.
- Bad: it changes what running installations do without anybody asking (the reason ADR-0317 kept the
  open line), and a task only operators see is one the people it was meant for never find. It hides the
  missing assignment instead of making the modeller supply it.

### Option 4: lane equals group by name

Strongest form: no authoring at all for the common "one lane, one team" case.

- Good: zero effort.
- Bad: rejected in ADR-0121 for reasons that still hold: renaming a lane would silently change who may
  act, a lane name is a label and not an identity, and lanes also hold gateways and service tasks.

## Links

- builds layer B of [ADR-0121](0121-bpmn-lanes.md)
- amends [ADR-0317](0317-task-commands-are-an-object-question.md) (open work exists only in versions deployed before this record) and [ADR-0275](0275-instance-visibility.md) (a holder reads the progress of the instance)
- relates to [ADR-0042](0042-user-task-assignment-and-claim.md), [ADR-0045](0045-user-task-assignment-bound-to-identity.md), [ADR-0318](0318-user-task-assignment-expressions.md), [ADR-0449](0449-a-wizard-is-a-subprocess-one-filler-walks-through.md), [ADR-0496](0496-operations-heatmap.md)
- relates to [ADR-draft-starters-start-processes-and-follow-their-instances](draft-starters-start-processes-and-follow-their-instances.md)
