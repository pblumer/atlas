# ADR-0028: User tasks, forms, and the Tasks app

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler's "Implement" panel binds a **Form** to an element and has a
**Test** tab for exercising it. Forms are how a human participates in a process:
a user task pauses an instance, a person opens a rendered form, fills it in, and
submitting completes the task with the form's data as variables. The app shell
(ADR-0012) already reserves a **Tasks** app name for exactly this surface, but it
is an empty placeholder — Atlas has no user tasks, no form model, and no task
inbox.

Atlas executes service tasks (external workers, ADR-0007) and script tasks
(in-engine FEEL). A **user task** is structurally close to a service task — it
parks a token and waits for an external completion — but the "worker" is a person
using a form, not a job worker. The question: **how does Atlas model user tasks
and their forms, and how is a form rendered and completed**, without shipping a
heavyweight form-builder product or a second execution model?

## Decision drivers

- **Reuse the job/task lifecycle we already have.** A user task waiting for human
  input should ride the same "park a token, wait for an external completion"
  machinery as a service task (ADR-0007), not a new engine subsystem.
- **Forms are data.** A form definition is a JSON schema of fields; rendering and
  submitting it is a UI concern, not an engine concern. The engine only stores the
  form reference and applies the submitted variables on completion.
- **Buildless rendering (ADR-0012).** A form renderer must run in the hand-written
  UI without a bundler, or be vendored pre-built like bpmn-js (ADR-0013).
- **Durable before visible (I2).** Completing a task writes variables and advances
  the token through the normal fsync→commit→side-effect ordering; no special path.

## Considered options

1. **User task = a built-in job type** the Tasks app claims. The task parks like a
   service task; the Tasks app is a specialized worker that lists tasks, renders
   the linked form, and completes the task through the existing completion path.
2. **A dedicated user-task engine subsystem** with its own store, assignment, and
   lifecycle separate from jobs.
3. **No user tasks** — humans interact only via external systems calling the API.

## Decision outcome

Chosen option: **Option 1 — user task as a first-class element completed through
the job/task path.** A `<bpmn:userTask>` parks a token and creates an activatable
human task (assignee/candidate metadata + a form reference in `zeebe:*`
extensions). The **Tasks** app is the human-facing "worker": it lists open tasks,
renders the referenced form (a JSON form schema — adopt the bpmn.io **form-js**
schema so the vendored ecosystem and the Modeler's Form editor agree), and on
submit completes the task, whose variables are applied on completion exactly like
a service-task result. The form definition is stored server-side alongside
deployments/drafts (ADR-0019/0021) and referenced by id from the model.

The **Test** tab in the properties panel renders the linked form against
example data (ADR-0025) so the author can preview it without deploying.

Option 2 is rejected: it duplicates the parked-token/external-completion lifecycle
the job path already provides, for no behavioral gain, and adds a second thing to
recovery-test. Option 3 leaves the reserved Tasks app permanently empty and cedes
the human-in-the-loop story that a workflow engine exists to tell.

### Consequences

- **Positive:** Human tasks reuse the proven job lifecycle and its recovery
  guarantees; forms are portable JSON (form-js) shared between the Modeler's form
  editor and the Tasks renderer; the reserved Tasks app gets a real purpose; the
  engine stays ignorant of form *rendering*, only storing a reference and applying
  submitted variables.
- **Negative / trade-offs accepted:** New surface area — a form model, a form
  store, a task-list query, and a form renderer (likely a vendored form-js dist,
  per ADR-0013's vendoring pattern). Assignment/candidate-group semantics and
  task permissions are a real design space we are only opening here.
- **Follow-ups / risks to watch:** Depends on the variable subsystem (Milestone 1)
  for input/output data; task assignment, claim/unclaim, and authorization need
  their own decision; decide vendored form-js vs. a minimal hand-written renderer.

## Links

- fills in the Tasks app reserved by ADR-0012; relates to ADR-0007 (task/job
  completion lifecycle), ADR-0025 (Form binding + example data in the panel)
- prerequisite for ADR-0029 (public start via a published form)
- depends on Milestone 1 variables (input/output mappings)
