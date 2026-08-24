# ADR-DRAFT: Responsibility metadata — RACI on the element, with R derived from the assignment

- **Status:** Proposed
- **Date:** 2026-08-24
- **Deciders:** Atlas maintainers

> **Scope.** This record covers **Layer A only**: authoring A/C/I on a flow node, validating it,
> compiling it as metadata with **no execution semantics**, and surfacing it (properties panel,
> a matrix view, the documentation export, the task API). **Layer B** (the letters generate work —
> a `C` becomes a consultation task, an `I` a notification) and **Layer C** (the letters as
> authorization) are sketched here and deferred, each to its own record. The layering, and the
> refusal to let organizational metadata acquire execution semantics, follow
> [ADR-0121](0121-bpmn-lanes.md).

## Context and problem statement

"Does Atlas do RACI?" is a question the project cannot currently answer with a yes or a no, because
the four letters land in four different places — and three of them land nowhere.

**R is covered, and covered well.** A user task carries a `zeebe:assignmentDefinition` with
`assignee` and `candidateGroups`, compiled into `UserTaskDetail` ([ADR-0028](0028-forms-and-the-tasks-app.md));
the runtime assignee is a field on the job that claim/unclaim rewrites ([ADR-0042](0042-user-task-assignment-and-claim.md));
under `--auth` a claim is the signed-in principal and a named assignee must resolve to a real,
enabled account ([ADR-0045](0045-user-task-assignment-bound-to-identity.md)); and who forced a step
by hand is auditable ([ADR-0159](0159-manual-task-completion-audit.md)). A node's organizational
owner is expressible too: lanes are compiled and exposed as `lane`/`lanePath` on the task API
([ADR-0121](0121-bpmn-lanes.md)).

**A, C and I are not expressible at all.** There is nowhere in an Atlas model to say who *answers*
for a step, who must be *asked* before it is decided, and who is *told* afterwards. Today those
three facts live in a wiki page or a spreadsheet beside the model, which is precisely the drift
[ADR-0143](0143-process-documentation-export.md) was written to end for prose and diagrams: the
document and the model must be one artifact, or the authoritative one loses.

That leaves three concrete gaps behind the question:

1. **No place to author accountability.** Not on the element, not on the process.
2. **No matrix.** Even the responsibility Atlas *does* know — the assignment — is only visible one
   element at a time, in the properties panel. There is no per-process view, and the documentation
   PDF prints an element's `lane:` but never its assignment.
3. **No audit-shaped output.** An auditor asking "who signed off on step 4 in March" gets a
   screenshot.

The question this record answers: **where does a responsibility other than "who does the work" live
in an Atlas model, what exactly does it mean, and what must it deliberately not mean?**

## Decision drivers

- **One source of truth for who does the work.** The assignment (`assignee`/`candidateGroups`) is
  the established authority for R ([ADR-0042](0042-user-task-assignment-and-claim.md),
  [ADR-0045](0045-user-task-assignment-bound-to-identity.md)). A second, competing way to state R is
  exactly what [ADR-0121](0121-bpmn-lanes.md) refused to introduce when it declined to make a lane
  the assignment mechanism. Whatever we add must not re-open that.
- **Metadata stays metadata.** The engine, `applyToState` and token flow must not learn that RACI
  exists (I1, I2, I4, I6 stay trivially intact). Parsing happens at deploy, never on the hot path (I5).
- **The model is the artifact.** A matrix that does not travel with the BPMN file is a matrix that
  will drift — models move between servers as git-backed applications
  ([ADR-0134](0134-git-backed-applications.md)) and to remote deployment targets
  ([ADR-0129](0129-remote-deployment-targets.md)).
- **Group-ready, not group-blocked.** Groups are still deferred in the identity model
  ([ADR-0044](0044-user-management-and-authentication-boundary.md)). The shape must let a group be
  named today and enforced later without changing that shape — the move
  [ADR-0073](0073-principals-directory.md) already made with its `type` tag.
- **Say what it is not.** "RACI" is heard as access control. Whatever ships must be impossible to
  mistake for enforcement, in the UI and in this record.

## Considered options

1. **An `atlas:responsibility` extension carrying A, C and I; R derived from the assignment.**
   Declarative metadata on any flow node (and on the process), compiled into the process the way
   lanes are, surfaced in a matrix view, the documentation export and the task API.
2. **The same extension, but authoring all four letters including R.**
3. **A RACI sidecar store** — a per-process matrix record edited outside the diagram, persisted like
   forms, projects and drafts ([ADR-0019](0019-durable-deployments.md), [ADR-0034](0034-projects-and-artifacts.md)).
4. **One lane per RACI letter** — an "Approver" lane means A, a "Consulted" lane means C.
5. **Derive the whole matrix from the model as it already stands** — R from the assignment, A from
   the lane, C from parallel user tasks, I from mail/send tasks — with no authoring at all.
6. **Prose in `<bpmn:documentation>`** (status quo, formalized by convention only).

## Decision outcome

Chosen option: **option 1 — an `atlas:responsibility` extension that carries A, C and I only. R is
never authored; it is derived from the assignment the model already states.**

The rule is one sentence, and it is the whole design: **Atlas already knows who does it — you tell
it who answers for it, who is asked, and who is told.**

### What is authored

```xml
<bpmn:userTask id="ApproveInvoice" name="Approve invoice">
  <bpmn:extensionElements>
    <zeebe:assignmentDefinition candidateGroups="accounting" />
    <atlas:responsibilities>
      <atlas:responsibility role="A" type="user"  ref="k.stalder" />
      <atlas:responsibility role="C" type="group" ref="legal" />
      <atlas:responsibility role="I" type="group" ref="controlling" />
    </atlas:responsibilities>
  </bpmn:extensionElements>
</bpmn:userTask>
```

A container element holding many entries, mirroring the `atlas:startForm` / `atlas:startVariable`
pair the moddle descriptor (`api/web/atlas-moddle.json`) already defines. `Responsibilities` is
`allowedIn` `bpmn:FlowNode` **and** `bpmn:Process` — a process-level entry is the process owner, the
row every real matrix has above the steps.

A `ref` is a **name**, in the same string universe assignment already uses: a username or a group
name, never an opaque `usr_…` id. That is deliberate. The id is the stabler handle inside one server
([ADR-0071](0071-sharing-scopes.md)/[ADR-0073](0073-principals-directory.md)), but it is server-local,
and this reference lives in a file that is committed to git and deployed to other servers. Usernames
are create-only and therefore stable ([ADR-0045](0045-user-task-assignment-bound-to-identity.md));
group names carry the same fragility `candidateGroups` already carries, and no more. The Modeler
picker is sourced from `GET /api/v1/principals` ([ADR-0073](0073-principals-directory.md)) so an
author picks a real person and the file stores that person's name.

### What is derived

`R` is answered, not stored:

1. the node's `assignee`/`candidateGroups`, when it has an assignment; otherwise
2. the node's lane ([ADR-0121](0121-bpmn-lanes.md)); otherwise
3. nothing — the cell is empty, which is an honest statement about a step nobody owns.

So authoring `role="R"` is a **validation error**, and the message names the assignment field to use
instead. There is no precedence rule to invent, no drift to reconcile, and the matrix cannot
disagree with the inbox.

### What is compiled

The compiler records the entries the way it records lanes — a flat table plus a per-node range, the
offset/count idiom `CompiledNode` already uses for boundary events, I/O mappings and data associations:

```go
// CompiledNode
RespStart int32 // offset into responsibilities
RespCount int32 // number of entries (0 for a node that declares none)

type ResponsibilityDetail struct {
	Role RaciRole      // RoleAccountable | RoleConsulted | RoleInformed
	Kind PrincipalKind // PrincipalUser | PrincipalGroup
	Ref  int32         // interned principal name → index
}
```

Fixed-size, integer-indexed, interned, built once at deploy. No new `ValueType`, no intent, no event,
no behavior, no recovery path: the engine never reads this table, so I1/I2/I4/I6 hold without an
argument, and I5 is satisfied by construction — the parse happens at deploy or not at all.

### What validates, and where

**In the compiler** (`compiler/validation.go`, the deploy-time gate), because these are properties of
the document:

- `role` is one of `A`, `C`, `I`; `R` is rejected with the message above.
- `type` is `user` or `group`; `ref` is non-empty.
- **At most one `A` per element.** Two accountable parties is the failure mode the letter exists to
  prevent, so the model that states it does not deploy.
- The same `(role, type, ref)` twice on one element is an error, not a silent dedupe.

**In the Modeler's Problems panel** ([ADR-0026](0026-problems-panel-and-versioned-validation.md)),
because it needs the directory the compiler cannot see: a `ref` naming no known principal is a
**warning**, never a deploy error. A model must stay deployable on a server that does not hold those
accounts — a fresh dev instance, a remote target ([ADR-0129](0129-remote-deployment-targets.md)), a
repository checked out anywhere ([ADR-0134](0134-git-backed-applications.md)).

### Where it shows up

- **Properties panel** — a Responsibilities section on any flow node: A/C/I rows with a principal picker.
- **A RACI matrix view in the Modeler** — rows are the documentable elements (the same set
  `process-doc.js` already derives), columns are every principal named in the model, cells are
  letters, with R filled from the derivation above. Read-only, derived from the draft, no store.
- **The documentation export** ([ADR-0143](0143-process-documentation-export.md)) — a
  *Responsibilities* chapter carrying that matrix as a table, and a per-element line beside the
  existing `lane:` line. This is the audit deliverable, and it inherits ADR-0143's immutable,
  per-process version numbering for free.
- **The task API** — `taskResp` grows a `responsibilities` array beside `lane`/`lanePath`, so the
  Tasks app can show *accountable: K. Stalder* on the open task. Metadata, exactly like
  `documentation` and `lane` already are.

### What this deliberately is not

- **Not authorization.** Atlas will not stop a non-A from completing the task, and the UI labels the
  section as documentation. Enforcement needs a real group model
  ([ADR-0044](0044-user-management-and-authentication-boundary.md) deferred it) and is Layer C.
- **Not execution.** An `I` does not send anything. A process that must notify someone models a send
  task ([ADR-0112](0112-send-tasks.md)/[ADR-0079](0079-outbound-mail-connector.md)); a process that
  must collect an opinion models a task ([ADR-0077](0077-multi-instance-activities.md),
  [ADR-0138](0138-adhoc-subprocesses.md)). Wiring the letters to those is Layer B, and it is the step
  that would give organizational metadata execution semantics — so it earns its own record, not a
  paragraph in this one.
- **Not a new identity vocabulary.** The same names assignment uses.

### Consequences

- **Positive:**
  - The management question gets a straight answer: three letters authored, one derived, none
    enforced — and a matrix in a versioned PDF for the auditor who actually asked.
  - Zero engine surface. No value type, event, behavior or recovery path; the invariants are not
    merely respected but untouched.
  - One artifact. The matrix travels with the model into git, into an application release, onto a
    remote target — it cannot drift from the diagram, because it is in the diagram.
  - Group-ready without a shape change: `type="group"` is authorable the day this lands and becomes
    enforceable the day groups do.
  - No competing source of truth for R, which is the trap this design exists to avoid.
- **Negative / trade-offs accepted:**
  - **Names, not ids.** Renaming a group silently orphans references. Accepted for portability and
    for consistency with `candidateGroups`; usernames being create-only bounds the damage, and the
    Problems panel surfaces an unresolvable `ref` as a warning.
  - **Declarative only.** Someone will read RACI as access control and be disappointed. Mitigated by
    labelling, not by mechanism.
  - **More surface to carry:** a moddle type, a properties section, a matrix view, a PDF chapter, a
    compiled table and an API field — plus one more extension for element templates and the
    marketplace to know about. Every model file grows a little.
  - **The matrix is per process.** "What is Karin accountable for, across all processes?" needs a
    cross-process report that this record does not provide.
- **Follow-ups / risks to watch:**
  - Groups in the identity model, then Layer C (the letters as authorization) and the separation of
    duties question that follows immediately after ("whoever was R may not be A").
  - [ADR-0121](0121-bpmn-lanes.md) Layer B (a lane naming a group) sharpens the derived R for nodes
    that carry no assignment; the two records should land in that order.
  - The ArchiMate export ([ADR-0099](0099-archimate-enterprise-architecture-view.md)) could emit the
    letters as business-role assignments.
  - **Vocabulary creep.** RASCI ("Support"), RACI-VS ("Verify", "Sign-off") and DACI will all be
    asked for. `role` is a string, so growth is possible — but each new letter needs a meaning
    Atlas can state, not just a column someone wants.

## Pros and cons of the options

### Option 1 — A/C/I authored, R derived (chosen)
- Good: one source of truth for who works; the matrix cannot contradict the inbox; no engine change;
  travels with the model; group-ready; layered so the expensive parts stay optional.
- Bad: R is not editable where the matrix is read, which is mildly surprising until the rule is
  learned; a node with neither assignment nor lane has an empty R.

### Option 2 — author all four letters
- Good: the matrix is authored in one place, and an automated step can state a responsible party
  without an assignment.
- Bad: two ways to say who does the work, and therefore a precedence rule, drift between the matrix
  and the Tasks app, and a deploy-time conflict to adjudicate. This is the mistake
  [ADR-0121](0121-bpmn-lanes.md) declined to make for lanes; it is not more attractive here.

### Option 3 — a RACI sidecar store
- Good: no BPMN extension; editable without opening the Modeler; could span processes.
- Bad: the matrix stops travelling with the model (git applications, remote targets), needs its own
  lifecycle, versioning, backup and access rules, and drifts the moment an element is renamed or
  deleted — the drift ADR-0143 exists to prevent.

### Option 4 — one lane per RACI letter
- Good: no new concept; lanes already exist and are already drawn.
- Bad: a flow node belongs to **at most one** lane, so a node can never be both R and A — the matrix
  is unrepresentable. It also re-litigates ADR-0121's decision that a lane is not an identity
  mechanism, and it makes the diagram's layout carry organizational meaning.

### Option 5 — derive everything, author nothing
- Good: zero authoring; works on every existing model instantly.
- Bad: accountability is nowhere in the model, so it cannot be derived at all; and "a mail task means
  those recipients are Informed" is a guess that will be wrong often enough to discredit the matrix.
  The half of it that *is* sound — deriving R from the assignment — is kept.

### Option 6 — prose in `<bpmn:documentation>`
- Good: works today, costs nothing.
- Bad: not structured, so no matrix, no validation, no "exactly one A", no table in the PDF, no field
  on the task. It is the status quo the question was asked about.

## Links

- relates to [ADR-0121](0121-bpmn-lanes.md) — lanes as organizational metadata; the layering and the
  refusal to let a lane become the assignment mechanism are the direct precedent for this record
- relates to [ADR-0042](0042-user-task-assignment-and-claim.md) and
  [ADR-0045](0045-user-task-assignment-bound-to-identity.md) — the assignment R is derived from
- relates to [ADR-0044](0044-user-management-and-authentication-boundary.md) — groups, still deferred;
  Layer C depends on them
- relates to [ADR-0073](0073-principals-directory.md) — the picker's directory, and the `type` tag
  this record's `type` attribute mirrors
- relates to [ADR-0143](0143-process-documentation-export.md) — the versioned document the matrix
  chapter joins
- relates to [ADR-0026](0026-problems-panel-and-versioned-validation.md) — where an unresolvable
  principal is reported
