# ADR-0251: Adjusting a deployed definition's diagram without redeploying it

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas maintainers

> **Implementation status.** Delivered. `PUT /api/v1/processes/{key}/diagram` takes a
> BPMN document, keeps only its diagram interchange, and writes it into the deployment
> record in place of the stored one. `layout.Transplant` does the surgery, the Modeler
> offers it as **Save layout to deployment** when a deployment is what is open, and
> `atlas_save_process_diagram` is its MCP form.

## Context and problem statement

A BPMN document has two halves. The semantic half — `<process>`, its flow nodes, its
sequence flows, the extension elements — is what Atlas compiles. The other half, the
`<bpmndi:BPMNDiagram>` block, says where each of those things is drawn. The compiler
does not read it: `grep -r bpmndi compiler/` finds nothing, and a `CompiledProcess`
holds no coordinate. Layout is presentation, in the standard and in this engine.

Deployments, on the other hand, are immutable here. `deployModel` writes the whole
document into the deployment record (ADR-0019) and the only field anything ever rewrites
afterwards is the deactivation flag (ADR-0119). The Operations views render that stored
snapshot, addressed by the definition key an instance carries.

Those two facts meet in a complaint that has nothing to do with modelling and everything
to do with running a process: **you find out that the picture is wrong once it is in
production.** A task's name disappears under a runtime badge, a label sits on an edge, a
row of shapes that read fine on the modelling canvas is unreadable with token counts and
incident markers on it. None of that is visible while authoring, because none of those
overlays are drawn until instances exist.

Today the only answer is to redeploy, and redeploying is the wrong instrument:

- It mints a version (`version = versions[pid] + 1`) that differs from its predecessor
  in nothing the engine can see. The Deployments view then lists v1…v12, of which nine
  are "moved a label", and the versions that are real changes are no longer findable
  among them.
- **Running instances keep the old picture.** They resolve their definition by key, and
  their key still points at the old record. So the diagram that was hard to read stays
  hard to read for exactly the instances an operator is looking at, until somebody
  migrates them (ADR-0162) — a migration that writes an audit record and a timeline
  entry for a change that moved a box 20 pixels.
- A deploy re-arms start timers, re-registers DMN models, re-resolves job types and
  supersedes the previous version's message and signal subscriptions. All of it correct,
  all of it unnecessary, and all of it risk taken on for a cosmetic edit.

So the question: **can the picture of a deployed definition be changed in place, and
what has to be true for that to be safe?**

## Decision drivers

- **Nothing but the picture may move.** A deployed definition's behaviour is the one
  thing this must not be able to touch, and "the endpoint promises not to" is not the
  standard — the mechanism should make a semantic change impossible rather than
  refused-if-noticed.
- **No recompile, no re-key, no re-version, no migration.** If the `CompiledProcess`
  survives untouched, none of the deploy machinery has to run and no instance is
  affected. That is both the safety argument and the performance one.
- **An editor round-trip reformats.** bpmn-js re-serialises the whole document: prefixes,
  attribute order, indentation and the exporter stamp all change. A check that cannot
  forgive that forgives nothing, because no editor writes a document back the way it read
  it.
- **The change is visible to everyone.** The diagram is not private to whoever adjusted
  it, so "why does this look different from last week" needs an answer.
- **Collaborations are one drawing.** A model with pools deploys as one definition per
  pool (ADR-0023), each holding its own copy of the same document. Pools of one picture
  must not disagree about where they are.

## Considered options

1. **Do nothing; redeploy and migrate.** The status quo.
2. **Take the whole submitted document after proving it compiles to the same thing.**
   Compare the two `CompiledProcess`es and store the new document if they match.
3. **Take only the diagram, keep the stored bytes for everything else** — a transplant,
   guarded by a check that the two documents describe the same model.
4. **Version the layout**: keep every diagram a definition has ever had, and let a view
   pick one.

## Decision outcome

Chosen option: **3 — transplant the diagram, keep the stored bytes** — because it is the
only one where "nothing but the picture moved" is a property of the mechanism rather than
a claim about it.

`layout.Transplant(stored, incoming)` returns the stored document with its
`<BPMNDiagram>` blocks replaced by the incoming document's. Every other byte of `stored`
comes back unchanged, so the model the compiler already turned into a `CompiledProcess`
is bit-for-bit the model it still holds — script bodies with load-bearing indentation
included. The handler then writes that document into the deployment record, swaps the
in-memory copy, and stops. No `proc.Deploy`, no `ArmStartTimers`, no key, no version.

Two guards sit on top of the mechanism:

- **The submitted model must be the deployed one.** A canonical digest of everything
  outside the diagram — namespaces resolved, attributes sorted, whitespace between
  elements ignored, the root element's own metadata attributes skipped because they are
  not transplanted either — must match. A layout is only meaningful against the shapes it
  was drawn for, and grafting one process's diagram onto another's model would render as
  a blank or half-drawn canvas. A mismatch is a 409 that says to deploy instead.
- **The diagram must be a diagram.** A body whose `<BPMNDiagram>` carries no shape or
  edge is refused: replacing a picture with an empty plane is not an adjustment.

Note what the digest is *not* doing. It is not proving the two documents compile the
same — the transplant provides that by construction, since the incoming semantic bytes
are discarded. It is answering the narrower question of whether this picture belongs to
this model, which is why it can afford to forgive reformatting.

### The blast radius, stated plainly

A deployment's layout is stored once per definition key, and every view of that
definition renders from it. So an adjustment necessarily changes how **every** instance
of that version is drawn — running, finished, and archived alike. This is the one
consequence that deserves to be argued for rather than buried:

**The diagram is not a historical fact.** What happened is in the event log: the
transitions, their order, their timestamps, the tokens, the incidents. The diagram is a
rendering of the elements those facts refer to, and moving an element's box changes no
fact about it. The markers, counts and badges an Operations view draws are computed from
the log every time and land on the shapes wherever the shapes are. A replay of an
instance from March, opened after an adjustment, shows the same steps in the same order
with the same values — laid out better.

The alternative reading — that the picture is part of the record — would argue for
option 4, a layout per version or per instance. We rejected it: an operator wants *one*
readable picture of v3, not one per instance, and a store of every layout a definition
has ever had is a large amount of machinery guarding an artefact nobody diffs. What that
reading is really asking for is knowing that the picture changed, which is what the audit
line and the stamp below provide.

### What it leaves behind

- `deployment.diagram_updated` on the audit trail (ADR-0197), with the actor, the client
  address, the definition key and how many definitions the adjustment touched.
- `diagramUpdatedAt` / `diagramUpdatedBy` on the deployment record and in the process
  listing: the stamp that says this drawing is no longer the one that was deployed, and
  who to ask about it.

Both are deliberately a stamp rather than a history. The question they answer is "is this
the deployed picture, and who changed it" — the audit stream carries the rest, and it is
the stream an operator already ships and alerts on.

### Consequences

- **Positive:** the fix for a picture problem costs a picture change. No version
  inflation, no migration audit for a cosmetic edit, no re-arming of anything. Running
  instances see the improvement immediately, which is the case redeploying could not
  reach at all. Layout-less deployments — the ones the UI renders through
  `layout.Ensure`'s generated diagram — can have that generated layout adjusted and
  kept, which they previously could not.
- **Negative / trade-offs accepted:** a definition's stored document is no longer
  byte-identical to what was deployed, so a reader comparing it against an archived copy
  of the deployment payload will find the diagram differs; the stamp is what tells them
  why. Finished instances render with a layout that did not exist when they ran. A
  hand-written model that the Modeler's `repairModel` fixes on import (a missing
  `itemDefinition`, an undeclared `dataObject`) will be refused, because what comes back
  from the editor is then genuinely a different document — the refusal names the cause,
  and deploying it once repairs it permanently.
- **Follow-ups / risks to watch:** the sibling rule is "identical body, same
  `DeployedAt`", which is precisely "deployed by the same call" except for two separate
  deploys of byte-identical XML within one second — where both would be adjusted, and
  both would end up with the same picture they already shared. If deployments ever gain a
  first-class deploy id, that is the field to key this on.

## Pros and cons of the options

### Option 1 — redeploy and migrate
- Good: no new surface; every mechanism involved already exists.
- Bad: answers a presentation problem with a behaviour-preserving-but-behaviour-touching
  operation; buries real versions among cosmetic ones; leaves running instances with the
  problem you set out to fix unless you also migrate them, and a migration for a moved
  label is a false entry in an instance's history.

### Option 2 — take the whole document, compare compiled output
- Good: conceptually direct — "same program, new picture".
- Bad: `CompiledProcess` holds compiled FEEL programs, which are not comparable by value,
  so the comparison would have to be re-derived and kept in step with the compiler
  forever. And it lets the submitted bytes into the store, which means every formatting
  difference an editor introduces is now the deployed model — including inside a script
  body, where indentation is executable.

### Option 3 — transplant the diagram (chosen)
- Good: the safety property is structural. The check that remains is small, testable and
  free of the compiler. Reformatting is forgiven exactly where forgiving it is harmless.
- Bad: a fragment moved between documents has to carry its namespace bindings, which is
  fiddly (and is why `Transplant` re-declares the prefixes it uses on the `<BPMNDiagram>`
  element itself).

### Option 4 — version the layout
- Good: the most honest model of "the picture changed" — nothing is ever lost.
- Bad: a store of diagrams, a picker in every view, and a question ("which layout was
  this instance rendered with?") that nobody has asked. The stamp plus the audit line
  covers the need behind it at a fraction of the cost.

## Links

- relates to [ADR-0019](0019-durable-deployments.md) — the deployment record this writes into
- relates to [ADR-0023](0023-collaborations-and-pools.md) — why one document is several definitions
- relates to [ADR-0124](0124-server-side-diagram-auto-layout.md) and [ADR-0127](0127-layered-layout-pipeline-and-invariants.md) — the layout package this extends
- relates to [ADR-0162](0162-process-instance-migration.md) — the instrument this exists to avoid using for cosmetics
- relates to [ADR-0197](0197-login-throttle-and-audit-log.md) — the audit trail the change is recorded on
- relates to [ADR-0229](0229-modeler-bar-hierarchy.md) — why the control sits in the Modeler's menu
- relates to [ADR-0252](0252-runtime-badges-clear-of-labels.md) — the other half of the same complaint
