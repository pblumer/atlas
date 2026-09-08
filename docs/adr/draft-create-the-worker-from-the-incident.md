# ADR-DRAFT: Create the worker from the incident, and run the deploy preflight on every deploy path

- **Status:** Proposed
- **Date:** 2026-09-08
- **Deciders:** Atlas maintainers

## Context and problem statement

Two reports, one about the same message. An operator watching a deployed application
found two tokens parked on

```
clio: no worker registered as "clio"
```

and asked why the incident panel gave them nothing to do about it. It gave them a
link.

That is not an oversight, it is what [ADR-0160](0160-fix-the-connector-from-the-incident.md)
decided, in as many words:

> When the referenced name is not configured at all there is nothing to open, so the
> action becomes the way to the Console, where one is created.

The reasoning held for the surface ADR-0160 was building — a dialog that *edits* a
record needs a record. But the sentence it left in place is the pre-ADR-0160 detour
that ADR-0160 exists to remove: read the incident, carry the name and the Worker Type
in your head, find the Console's add form, fill it in, navigate back, find the incident
again, resolve. And it left it in place for precisely the message that started
[ADR-0158](0158-a-connector-reference-that-explains-itself.md). The one incident whose
cause is named in its own text — *this worker does not exist* — was the one incident
with no way out.

"There is nothing to open" was also the wrong reading of the situation. Creating a
worker from the Console is an act of *choosing*: which Worker Type, under which name.
Creating one from an incident chooses neither. The deployed model states both, and a
worker created under anything else leaves the task parked on the name it actually
references (ADR-0036/0041). So the two fields that make a create a create are, here,
already decided — which makes it the same dialog with those two fixed and a `POST`
instead of a `PATCH`.

The second report came out of investigating the first: how did an application reach
production naming a worker nobody had configured, when ADR-0158 added a deploy-time
preflight to say exactly that? Because the preflight ran on one deploy path. It lived
in `handleDeploy`, which serves `POST /api/v1/deployments` — the Modeler's own Deploy
button. Publishing an application (`POST /api/v1/applications/{id}/deploy`,
[ADR-0128](0128-process-applications.md)) and importing a release
([ADR-0129](0129-remote-deployment-targets.md)) reach `deployModel` directly, and neither
said a word. `projectDeployResp` had no `warnings` field at all, so the information
could not have reached the client even if it had been computed. The MCP authoring
tools post to the bundle endpoint, so an agent-driven deploy was blind too.

The same gap swallowed the other two deploy-time warnings —
[ADR-0230](0230-process-information-model.md)'s data-flow findings and the foreign
Atlas-namespace check — which is the tell that this is structural: three checks were
added to one handler in turn, and each one silently applied to a third of the deploys.

## Decision drivers

- **A warning that only fires on the path fewest applications take is not a warning.**
  Publish is the headline action of ADR-0128; it is how a multi-process application
  reaches a server.
- **The fix belongs where the failure is reported** — ADR-0160's own driver, applied to
  the case it excluded.
- **The name is the binding.** Anything that creates a worker from a model's reference
  must use that reference verbatim, not a re-typed approximation of it.
- **One dialog, not two surfaces that drift** — also ADR-0160's, and the reason the
  create body is built by the function the Console's add form already posts through.
- **A preflight is not a refusal.** Deploying before the workers exist is legitimate and
  stays legitimate (ADR-0158); this is about who gets told, not about who gets blocked.

## Considered options

1. **Leave the link, fix only the preflight.** The operator would at least have been
   warned at publish. But the incident would still be a dead end for the case whose
   cause it names.
2. **Deep-link the Console's add form, prefilled.** The route would have to learn query
   parameters, the form would have to learn to read them, and the operator would still
   navigate away and back to resolve — ADR-0160 rejected this shape once already.
3. **Open the shared dialog in a create mode, and run the preflight from a helper every
   deploy path calls.**
4. **Refuse a deploy whose worker references do not resolve.** Rejected outright: it
   breaks deploy-before-provision, which ADR-0158 examined and kept.

## Decision outcome

Chosen option: **3**, in two parts.

**The dialog creates as well as edits.** `askWorker` gains a `create` flag and
`workerdialog.js` gains `createWorkerFlow`. In create mode the name and the Worker Type
are shown on the form, disabled, so what is about to be committed can be read back; the
connection-string field appears for the SQL kinds, whose whole configuration is that
one secret and which therefore cannot be created without it (ADR-0188); the Enabled
toggle goes away, because a worker being created is enabled by the server's own default.
The body is built by `workerCreateBody` — the same function the Console's add form posts
through — from a form synthesized out of the fields this kind actually shows, so a
field the shape hides cannot reach the server and the two surfaces cannot come to
disagree about what a create carries.

**The incident offers it.** Where `connector` is set and `connectorId` is empty, every
incident surface now renders **⚙ Create worker…** instead of the link, over
`addWorkerFlow` — `fixWorkerFlow`'s sibling: same intro quoting the parked element and
its message, same second button (**Add & retry**) that writes and then hands the job one
more attempt. The link survives for one case only: an incident that names a worker but
not its Worker Type. There is nothing to create it *as*, and guessing the type would
create the wrong worker under the right name.

**The preflight is a function, not four lines in a handler.**
`Server.deployWarningsOnLoop(deployed, applicationID)` holds what `handleDeploy` used to
inline, and `handleDeploy`, `deployApplicationBundle` and the bundle import all call it
inside the run-loop closure that registered the deploy (I3). `projectDeployResp` and
`importBundleResp` gain the same `warnings` field `deployResp` has, so a client reads
one field whichever route it deployed through, and the Console reports them after a
publish. The bundle paths add the namespace check per artifact off the loop, and
collapse exact repeats — a namespace prefix is bound once per document, so several
drafts sharing one mistake would otherwise say the same sentence several times.

### Consequences

- **Positive:** the incident whose message is "this worker does not exist" now has the
  act that fixes it, in one step, under the name the model states. Publish and import
  report what they deployed anyway, so an application naming a worker nobody configured
  is caught by the operator publishing it rather than by the first token to park. A
  fourth deploy-time check will be written once and run three times.
- **Negative / trade-offs accepted:** the dialog now has two modes, and the fields that
  differ between them (name, connection string, Enabled) are shown or hidden by a flag
  rather than by the shape — one more thing `sync` has to get right. Creating a worker
  is admin-only when auth is on (ADR-0041), so a non-admin sees the action and is told
  they cannot use it, exactly as with the edit action. A publish that warns costs a
  dialog the operator has to dismiss.
- **Follow-ups / risks to watch:** the incident still cannot tell the operator *what*
  the worker should point at — only that one is missing. Nothing here changes
  ADR-0160's second layer: which worker name a task references is compiled into the
  deployment and changing it still means a new version, reaching running instances only
  through migration (ADR-0162). And the deprecated `POST /api/v1/projects/{id}/deploy`
  shares `handleDeployProject`, so it gains the warnings too — deliberate, since it is
  the same deploy.

## Pros and cons of the options

### Leave the link, fix only the preflight
- Good: the smaller half, and it addresses how the situation arises.
- Bad: leaves the operator standing at an incident that names its own cause with
  nothing to do about it. Warning earlier does not help the deploys already out there.

### Deep-link the Console's add form, prefilled
- Good: no second create surface.
- Bad: the route learns query parameters it has no other use for, and the operator still
  leaves the incident and comes back to resolve. ADR-0160 weighed and rejected this
  shape for the edit case; nothing about the create case makes it better.

### Create mode in the shared dialog, preflight in a shared helper
- Good: the act happens where the failure is reported; the create body stays one
  decision; every deploy path gains every present and future deploy-time check.
- Bad: a mode flag in a dialog that had none, and two more response types carrying a
  `warnings` field.

### Refuse a deploy whose worker references do not resolve
- Good: the failure could not reach production at all.
- Bad: breaks deploying a model before its workers are provisioned, and deploying to an
  environment that provisions them later — both normal, both examined and kept by
  ADR-0158.

## Links

- extends ADR-0160 (fix the connector from the incident) — supersedes its decision that
  an unconfigured name has nothing to open
- extends ADR-0158 (a connector reference that explains itself) — its deploy-time
  preflight now runs on every deploy path
- relates to ADR-0128 (application releases), ADR-0129 (cross-server publishing),
  ADR-0230 (process information model), ADR-0041, ADR-0036, ADR-0188
