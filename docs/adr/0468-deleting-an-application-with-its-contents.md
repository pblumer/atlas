# ADR-0468: Deleting an application with everything it owns

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-21
- **Deciders:** Atlas maintainers

## Context and problem statement

Deleting an application removed the application record, its releases, its access history
and its constants, and nothing else. [ADR-0034](0034-projects-and-artifacts.md) chose
that on purpose: an artifact is a draft or a deployment carrying a soft `projectId` tag,
and "deleting a project does **not** delete its artifacts by default (they fall back to
Ungrouped) — deletion of runnable definitions stays the deliberate, separate action it is
today".

That default left no way to do the other thing. An operator who installed the
Microsoft 365 example and then deleted it found fourteen deployed processes, their
drafts, nineteen forms and five decision references under **Not assigned**, and had to
remove them one at a time. One of them could not be removed at all: the Modeler reported
*"Could not delete 1 version(s) — running instances?"* for a process that had never been
started. The server had refused for a different reason — the newest release of a
catalogue the example installs binds that process
([ADR-0427](0427-converting-a-product-to-a-lifecycle-process.md)) — and the Modeler
threw the server's message away and printed a guess in its place. A process that does
have running instances could only be removed after cancelling them on another screen.

The question: **how does somebody remove an application and everything it brought onto
this server, deliberately and in one act, without weakening the guards for everybody who
does not mean to?**

## Decision drivers

- **The default stays safe.** A plain delete must not start destroying runtime state
  that somebody did not ask to destroy.
- **One deliberate act, stated in full.** Whoever confirms a destructive cascade must see
  what it removes and what outside the application will stop working, before it runs.
- **A guard explains itself.** A refusal is shown as the server words it, not replaced by
  a guess.
- **Cancelling instances is the operator's right** (ADR-0209): a route that cancels them
  must not hand that right to a modeler who does not hold it.
- **Silent corruption is never an option**, even on request — loud failure of a
  dependent can be repaired, a reused version number cannot be noticed.
- **Single writer (I3) and durable before visible (I2).** Selecting instances grows with
  the instance population and runs off the run loop; every removal writes its record
  before the in-memory registry changes.

## Considered options

1. **Change the default**: deleting an application always deletes its contents.
2. **An explicit purge**: the default stays as ADR-0034 has it; a second, explicit mode
   deletes the contents too, after a preview that names everything it touches.
3. **Client-side cascade**: the Modeler calls the existing per-artifact deletes one by
   one.

## Decision outcome

Chosen option: **2 — an explicit purge with a preview**, together with a forced delete of
one deployed version.

### 1. Forced delete of a deployed version

`DELETE /api/v1/processes/{key}?force=true` removes a deployed version whatever holds it:

- it **cancels the version's running instances** first — selected off the run loop,
  cancelled on it in bounded batches, as `POST /processes/{key}/cancel-instances` does —
  and then deletes the definition in the same turn that confirms none is left;
- it **overrides the ADR-0427 guard**: neither an order line that could still start the
  process nor the current catalogue release binding it stops the delete;
- it never deletes a protected system process (ADR-0122);
- when there are instances to cancel, the caller must hold `operator` or `admin`, the
  roles that may cancel an instance; with none running, `modeler` suffices as before.

Without `force` nothing changes: the route refuses exactly as before.

### 2. Purging an application

`DELETE /api/v1/applications/{id}?purge=true` deletes, in this order:

1. every deployed process version tagged with the application, each as in §1;
2. every decision deployment published from it (ADR-0319), its whole history at once;
3. its BPMN drafts, decision drafts, decision references, forms, test scenarios,
   Panorama models and information models;
4. the local DMN model file behind each of its decision references, when no reference
   outside the application points at the same file and the models are kept on this
   server (a remote temis service keeps its own);
5. then what a plain delete removes: releases, access history, constants, the
   application record.

The application record goes **last**, for the reason the plain delete already deletes
releases before it: a purge that fails half-way leaves the application in place, and
repeating the request finishes the work, because every step is idempotent.

It still refuses — before deleting anything — in three cases:

- the application is protected (ADR-0122);
- the caller is not the application's owner (ADR-0071);
- a decision it published is the **current** version of a decision whose older
  versions survive outside the application. Removing it would lower the per-decision
  version counter, and the next deploy would mint a version number that a surviving
  record, a release manifest and the editor already use for different content
  (ADR-0336). That is the one guard a purge does not override, because its failure is
  silent.

### 3. The preview

`GET /api/v1/applications/{id}/deletion-preview` answers, for the owner, what a purge
would remove — every deployed version with its running instances, every decision
deployment, every design-time artifact — and what outside the application depends on
it: catalogue products whose current release binds one of its processes, order lines
that could still start one, and deployed processes of other applications that evaluate
one of its decisions. It also names a refusal the purge would meet and whether the
caller lacks the operator role the cancellations need. The Modeler shows it in the
confirmation dialog; nothing is deleted until the person confirms that list.

### 4. The Modeler says what the server said

The process delete reports the server's refusal verbatim and, on a 409, offers the
forced delete with an explicit statement that it cancels instances and cannot be
undone. The application delete offers both modes: keep the contents (the ADR-0034
default) or delete everything, the latter with the preview.

### Amendments

- **ADR-0427 §1** guarded the last deployed version of a process against deletion while
  an order line or the current release needs it. It still does for every delete that
  does not say `force=true`. A forced delete — and a purge — overrides it after the
  preview has named the products and lines that will no longer be able to start the
  process. A return of such a line then fails with *no deployed process*: loudly, and
  repaired by deploying the process again.
- **ADR-0336** refused to delete a decision deployment that a deployed process is
  pinned to, or whose removal leaves a latest-bound task with nothing to evaluate. A
  purge overrides those two refusals for decisions published from the purged
  application, after the preview has named the dependent processes; it keeps the
  version-counter refusal, as above.

### As built

- `api/forcedelete.go`: the forced delete and `removeDeployments`, the
  cancel-then-delete loop both paths share — instances selected off the run loop,
  cancelled on it in batches of at most 50,000, a version removed only in the turn
  that reads its live count as zero, at most 64 rounds.
- `api/apppurge.go`: the inventory of what an application owns, the preview, and the
  purge. `handleDeleteProcess` and `handleDeleteProject` take `force` and `purge`;
  without them their behaviour is unchanged, except that a plain refusal now names
  the forced delete.
- Two audit events: `deployment.force_deleted` and `application.purged`.
- `api/web/deletedialog.js`: the application delete dialog with its two modes; the
  process delete in `app.js` shows the server's refusal and offers the forced delete.
- MCP: `atlas_delete_process` takes `force`, `atlas_delete_application` takes `purge`,
  and `atlas_application_deletion_preview` reads the preview.

### Consequences

- **Positive:** an application and everything it brought can be removed in one act,
  including its running instances, after one screen that says what will happen.
- **Positive:** a refused process delete says why, and offers the way through.
- **Positive:** the default delete, and every guard on it, is unchanged for everybody
  who does not ask for more.
- **Negative / trade-offs accepted:** a forced delete or a purge can leave a catalogue
  product offering a process that no longer exists, and leaves held rights whose
  revocation process is gone. That is the cost of the request; the preview names both,
  and redeploying the process repairs them.
- **Negative:** a purge is not atomic. Each step is durable and idempotent, so a failure
  leaves a smaller application that a second request finishes.
- **Follow-ups / risks to watch:**
  - Process documentation (ADR-0143) binds to a process id, not to an application, and
    may be shared by link; a purge leaves it. Whether a purge should offer to remove it
    is open.
  - Catalogue products are not owned by an application and are not removed; the
    preview names them so their maintainer can retire them.
  - Instances that keep starting while a purge runs (a timer start event firing between
    the cancellation and the delete) are cancelled in further rounds; after a bounded
    number of rounds the delete is refused with the count still running.
  - The history of the cancelled and finished instances stays, as after any delete,
    until history retention removes it (ADR-0115). A purge that also erases the
    history would be a decision about the audit trail, and is not taken here.

## Pros and cons of the options

### Option 1 — change the default
- Good: one behaviour, nothing to choose.
- Bad: reverses ADR-0034 for every caller, including scripts that rely on contents
  surviving; a mistaken click destroys running work.

### Option 2 — explicit purge with preview (chosen)
- Good: the default keeps its guarantees; the destructive mode is a stated choice with
  its consequences in front of the person making it.
- Bad: two modes to explain; a new route for the preview.

### Option 3 — client-side cascade
- Good: no new server code.
- Bad: a dozen requests with no shared ordering or retry semantics; every guard refuses
  separately with no way through; nothing for an agent or a script to call.

## Links

- relates to [ADR-0034](0034-projects-and-artifacts.md), [ADR-0071](0071-sharing-scopes.md),
  [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md),
  [ADR-0128](0128-process-applications.md), [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md)
- amends [ADR-0427](0427-converting-a-product-to-a-lifecycle-process.md) and
  [ADR-0336](0336-cleaning-up-the-decision-store.md)
