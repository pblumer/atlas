# ADR-DRAFT: A decision has a draft, and Save stops writing the model

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-13
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0320](0320-the-decision-editor-is-a-page.md)
moved the decision editor onto a routed page and listed four things the overlay
had made invisible. Three of them it fixed. The third it named and left standing:

> **Saving is publishing.** The overlay's save writes the model file every
> reference resolves to. For BPMN the draft/deploy split exists precisely so that
> editing is not shipping; the decision editor has no such split.

This record closes that one.

A decision artifact has **three** layers, where a BPMN diagram has two:

| | BPMN | Decision |
|---|---|---|
| work in progress | `drafts/`, keyed by process id (ADR-0021) | — *nothing* |
| what everything else resolves | *(none — the deployment is it)* | `dmn-models/<handle>.dmn`, pointed at by a `dmnRef` (ADR-0014/0062) |
| what runs | `deployments/`, versioned (ADR-0019) | `decisions/`, versioned (ADR-0319) |

The middle layer is not an implementation detail of one application. A model
handle is a **shared, resolvable name**: the business-rule-task picker's decision
catalogue resolves it, a `dmnRef` in another application may point at the same
handle, `atlas_register_decision` and the DRG viewer read it, and an application's
publish resolves it to decide what to ship. Writing it is therefore an act with an
audience.

Because the decision editor had no layer above it, **every save was that act**.
Four failures follow, and they are not hypothetical:

1. **You cannot stop halfway.** A decision table with one FEEL expression still
   being typed is either saved — and therefore shown to everyone — or lost. There
   is nowhere to leave unfinished work.
2. **An unfinished decision blocks somebody else's publish.** `deployApplicationBundle`
   is "validate all, then deploy all": one reference that does not compile refuses
   the whole application (ADR-0034). So one author's half-written save stops an
   unrelated colleague from publishing that application at all, with a message
   about a decision they never touched.
3. **A half-written decision is offered for wiring.** The picker lists the
   decisions a model provides with their inputs and output. Saving a table whose
   output column is still called `result` puts `result` into the next business rule
   task that adopts it (ADR-0062), and the mistake is then in a diagram too.
4. **The first save of a new decision can silently fork a copy.** `writeUniqueModel`
   resolves a collision by suffixing: a second decision named *Eligibility* is
   written as `eligibility-2.dmn` and gets its own reference, with no word to the
   author. The Explorer then shows two rows called *Eligibility*, told apart only
   by the temis handle in the row's subtitle. This is exactly the "unwanted copy"
   [ADR-0222](0222-artifact-id-renames.md) set out to abolish for drafts and forms;
   the decision path never got the rule.

Two questions, then: **where does unfinished decision work live**, and **what
happens when a save lands on a model handle something else already holds?**

## Decision drivers

- **Saving must be safe.** Pressing Save must never be the thing that breaks a
  colleague's publish or puts a wrong variable into someone's diagram.
- **One Modeler, one grammar.** In the BPMN editor, `Save` means *save my work as
  a draft* and a second, named action ships it. The decision editor should read the
  same way round, because that is the parity [#919](https://github.com/pblumer/atlas/issues/919)
  is about.
- **Do not break what resolves a model.** `modelRef` is a name other artifacts,
  other applications and other servers depend on. A draft must not change what a
  handle resolves to, and must not exist in a form that a resolver could pick up.
- **A remote model store must stay possible.** When `ATLAS_DMN_RESOLVER_URL` names
  a temis service, Atlas cannot write models at all. Whatever holds a draft has to
  work there too, which rules out "a draft is a second file next to the model".
- **No silent overwrite, no silent copy** (ADR-0222). Both of an author's worst
  outcomes stay refused-and-named rather than quietly chosen for them.
- **One writer, one turn.** A check-then-write on a *store* is atomic only inside
  the run loop's turn (ADR-0002). The model folder is not store state — it is a
  directory a resolver reads — and the existing upload deliberately stays off the
  loop for it.
- **No engine surface.** This is design-time. It must not touch `applyToState`,
  the event log, or the hot path (I1–I6).

## Considered options

For **where a draft lives**:

1. **Its own `dmn-drafts/` sidecar store**, parallel to `drafts/`.
2. **The existing `drafts/` store**, with a kind discriminator on the record.
3. **A second file in the model folder** — `eligibility.dmn.draft` beside
   `eligibility.dmn`.
4. **No store: keep it in the browser** (`localStorage`/`sessionStorage`).

For **what an application publish ships**:

A. **The model.** A draft never saved to the model does not travel.
B. **The draft**, making a decision draft behave exactly like a BPMN draft.

## Decision outcome

### 1. A decision draft lives in its own `dmn-drafts/` store — option 1

A new `sidecar.Store[dmnDraft]` under `dmn-drafts/`, classified `design-time` in
`storeregistry.go` (ADR-0282) and listed in `serverStoreDirs`, so a backup carries
it and a server refuses to start if it cannot open it.

A draft record is the DMN XML plus what the editor needs to come back to it:

```go
type dmnDraft struct {
    ID        string // the draft's key: the dmnRef's id, or a minted one for a decision not yet in the model
    Name      string // the decision name, read out of the XML
    RefID     string // the reference this draft edits; empty for a decision not yet in the model
    ModelRef  string // the model handle behind that reference, for the editor's chip
    ProjectID string // the application it is filed into (ADR-0034)
    OwnerID   string // the creator, which governs an Ungrouped draft (ADR-0071)
    SavedAt   int64
    XML       string
}
```

**The key is the reference's id** when the decision already exists in the model,
which makes "does this decision have unsaved work?" a single `Get`. A decision that
has never been written to the model has no reference, so its draft is minted a key
of its own, prefixed `dd-` so the two can never be confused.

**A draft exists only while it differs from the model.** Writing the model clears
it. That is what keeps the marker in the Explorer meaningful: a decision showing
*Draft* has work in it that nothing else can see yet.

**A draft belongs where its decision belongs**, and the server decides that rather
than believing the client: a save naming a reference takes that reference's
application and model handle, and needs editor on it (ADR-0071). Otherwise a draft
could be filed under somebody else's decision, or dragged out of its application by
a save that simply said so.

### 2. Save saves the draft; a second, named action writes the model

The bar becomes the BPMN editor's, word for word in the same positions:

| | BPMN editor | Decision editor |
|---|---|---|
| neutral, left | **Save** — save this diagram as a draft | **Save** — save this decision as a draft |
| primary, right | **Deploy** — this single diagram | **Save to model** — write the model every reference resolves |

`Save to model` is not called *Deploy*, because it does not deploy: it writes the
middle layer, and the runtime layer is still reached through the application's
**Publish** (ADR-0128), or — later — through Phase 3's Deploy button. Naming it
*Deploy* now would be the same conflation this record exists to undo.

A decision reached from a business rule task (`/for/{processId}/{elementId}`) is
adopted by that task on **Save to model** only, and a draft-only save says so in
the status line rather than leaving the task pointing at a decision the next
publish cannot resolve. Adoption means "this task calls that decision", and a
decision that is not in the model is not callable.

### 3. The model write refuses a handle something else holds — ADR-0222's rule

`POST /api/v1/dmn-models` gains ADR-0222's opt-in seam, spelled the same way:

- **`?from=` present** (empty for a decision with no model yet, or the handle this
  editing session opened) makes the upload identity-aware. The derived handle must
  be **free**, or the upload is **409**, nothing is written, and the message names
  the handle. `?from=<handle>` equal to the derived handle is a plain update.
- **`?from=` absent** keeps today's behaviour — derive, suffix to a free handle,
  write — which is what an import, a source-tree apply (ADR-0134) and the MCP
  authoring tools want.
- **`?handle=`** is unchanged: the explicit, named overwrite, which is what the
  editor sends to update the model it opened.
- **`?overwrite=true`** alongside `?from=` is the author answering *Replace it* to
  that refusal. It replaces the model the derived handle names rather than forking
  one — deliberately the server's own derivation again, so the Console never
  re-implements `sanitizeHandle` in order to name the file it is replacing.

So the silent fork of failure 4 becomes a question with the handle in it, and the
only overwrite left is one the author chose.

### 4. Publishing an application ships the model, not the draft — option A

`deployApplicationBundle` resolves each `dmnRef`'s `modelRef` and ships that. It
does not read `dmn-drafts/`, and this record forbids teaching it to.

This is deliberately **not** symmetric with BPMN, where a publish does ship drafts.
The asymmetry follows from the extra layer: a BPMN draft is the only copy of that
diagram, while a decision model is a name other artifacts resolve. If a publish
shipped the draft, then publishing application A would ship a model that
application B's reference — pointing at the same handle — does not resolve to, and
two publishes of what is visibly one decision would disagree about what it says.
With a remote temis resolver there would be no model to reconcile them against at
all.

A draft-only decision therefore does not travel, and the Explorer says so on the
row rather than letting the author discover it in a release that is missing a
decision.

### Consequences

- **Positive:** Pressing Save is safe. Nothing an author has not deliberately
  published to the model can refuse a colleague's publish, appear in the picker, or
  reach a release.
- **Positive:** The Modeler reads the same way for all three artifacts: Save keeps
  your work; a second, named button ships it.
- **Positive:** A decision can be started and left. A decision that was never
  finished is a row marked *Draft* instead of either a broken model or nothing.
- **Positive:** The first save of a decision whose name is taken asks instead of
  forking a second *Eligibility* nobody meant to create.
- **Negative / trade-offs accepted:** A decision now has three layers and two save
  verbs, which is one more than a form has. The middle layer is not something this
  record invented — it is ADR-0014's model handle — but it is now visible in the
  bar, and that is a thing to learn.
- **Negative:** `Save` in the decision editor writes something *different* from
  `Save` in the form editor (a draft vs. the form itself), because a form has no
  middle layer to draft against. The word is the same because the author's intent
  is the same ("keep my work"); what it keeps differs by artifact.
- **Negative:** Two authors can hold divergent drafts of one model and each write
  it, last one wins. That is the pre-existing behaviour of the model layer, not a
  regression, and the answer for diagrams (ADR-0140 live sessions) is not being
  extended to decisions here.
- **Negative:** The handle check is a check-then-write against a directory, so two
  first-saves of the same decision name that race land on the old behaviour — the
  loser is suffixed rather than refused. That is the outcome this record is
  replacing, but it is the *previous* outcome rather than a new failure, and the
  window is one upload wide.
- **Negative:** A draft holds a copy of the DMN XML, so a decision under edit is
  stored twice. Decision models are small; the alternative is a draft that cannot
  exist without a model, which is precisely the case that needs one most.
- **Follow-ups / risks to watch:** Two models declaring the same **decision id**
  still both deploy, and the later one takes the `latest` pointer (ADR-0319). That
  collision is a deploy-time question about decision ids, not a draft question about
  model handles, and it is left where it is rather than half-answered here.
  Deleting a reference does not delete a draft keyed by its id; the draft is
  orphaned and shows as a draft-only decision, which is recoverable but untidy.
  The application source export (ADR-0134) does not carry decision drafts. It
  already carries a reference as manifest data rather than copying the `.dmn`
  behind it, so unfinished work that is not in a model has nothing to hang on;
  the same rule as a publish, for the same reason, but worth saying out loud.
  Leaving the editor with changes that have not been saved does not ask. The
  BPMN editor's leave rule is a diagram-level guard tied to its command stack;
  giving the decision editor an equivalent is a separate piece of work, and a
  draft at least means the answer to "where do I put this" now exists.

## Pros and cons of the options

### Where a draft lives

#### Option 1 — its own `dmn-drafts/` store *(chosen)*
- Good: a draft is a different kind of thing from the model, with a different
  lifetime and a different audience (one author, until they say otherwise). A store
  of its own says that, and the registry makes the backup question explicit.
- Good: works unchanged when models come from a remote temis service, because the
  draft never touches the model folder.
- Good: one `Get` answers "has this decision unsaved work?".
- Bad: one more store, one more inventory line, one more thing a restore must carry.

#### Option 2 — put it in the existing `drafts/` store
- Good: no new store, no registry line.
- Bad: `drafts/` is keyed by **process id** and read by the BPMN publish path,
  the collaborative-session machinery (ADR-0140), the source export and the MIM
  import. Every one of those would need to learn to skip records of another kind,
  and a missed skip publishes DMN XML as a process. A discriminator that six
  readers must honour is a rule the store does not hold.

#### Option 3 — a second file beside the model
- Good: the draft sits next to what it drafts; no store at all.
- Bad: the model folder is a **resolver's** directory. Anything in it is a
  candidate for resolution, and the guard against picking up `.dmn.draft` would be
  a naming convention enforced nowhere.
- Bad: impossible with a remote resolver, which is exactly the deployment where an
  author most needs somewhere local to keep unfinished work.
- Bad: a draft would be unable to exist before its model does, leaving the
  brand-new decision — the commonest case — with no draft.

#### Option 4 — keep it in the browser
- Good: no server change whatsoever.
- Bad: work in progress that a different browser, a different machine or a cleared
  profile loses is not saved; calling it Save would be a lie.
- Bad: nothing else can see it, so the Explorer cannot mark the decision, and the
  question "what is unfinished in this application?" stays unanswerable.

### What a publish ships

#### Option A — the model *(chosen)*
- Good: one answer to "what does this handle say", for every reference in every
  application, on every server.
- Good: a publish ships what was reviewed, not what someone happened to have open.
- Bad: the author must take a second, explicit step before a decision can ship,
  and a draft-only decision silently missing from a release would be a trap — which
  is why the row is marked instead.

#### Option B — the draft
- Good: perfect symmetry with BPMN; one place to look; the model becomes a cache.
- Bad: two references pointing at one handle would resolve to different content
  depending on which application published last. The handle would stop being a
  name and become a per-application variable.
- Bad: a remote temis model store has no draft to ship, so the rule could not hold
  in the deployment where models are managed centrally — the feature would mean
  two different things depending on configuration.

## Links

- extends [ADR-0320](0320-the-decision-editor-is-a-page.md) — the fourth consequence it named and left open
- relates to [ADR-0021](0021-diagram-drafts.md) — the BPMN draft this one is modelled on
- relates to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) — the model handle that is the middle layer
- relates to [ADR-0062](0062-embedded-dmn-editor.md) — the adoption flow a model save completes
- relates to [ADR-0222](0222-artifact-id-renames.md) — the no-silent-overwrite rule this applies to model handles
- relates to [ADR-0282](0282-store-registry.md) — the inventory the new store is classified in
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the runtime layer a publish ships to
- relates to [ADR-0034](0034-projects-and-artifacts.md) — the application a draft is filed into
- relates to [ADR-0071](0071-sharing-scopes.md) — the scope a draft inherits
