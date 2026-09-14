# ADR-DRAFT: A model with no reference stays findable

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-14
- **Deciders:** Atlas maintainers

## Context and problem statement

A decision reaches an author through three rungs (ADR-0321/0034/0319): a draft, a
**model** in `dmn-models/<handle>.dmn`, and a decision deployment. The middle rung
is the one everything resolves — but nothing *lists* it. Every surface that shows
decisions reads DMN **references**:

- the decision catalog (`GET /api/v1/decisions`) folds references into decisions;
- the business-rule-task picker reads the catalog;
- the Modeler's application view lists references;
- a publish deploys the models its references resolve.

The model file itself is reachable only by handle
(`GET /api/v1/dmn-models/{ref}/xml`), and a handle is something you have to already
know.

So deleting a DMN reference — a single menu action — takes the model out of the
product while leaving it on disk. Its confirm even says so, in a sentence that
reads as reassurance:

> Delete this DMN reference? The temis model itself is not affected.

That is true and it is the problem. The file is fine; it is simply gone from every
list, every picker and every publish, and the only way back is to remember the
handle and hand it to the "reference an existing temis model by name" prompt. In
practice the model is lost and the author re-uploads it — which files a *second*
copy under a suffixed handle (ADR-0222), and now there are two.

## Decision drivers

- **Nothing should be unreachable.** A deletion may remove an artifact; it should
  not make content invisible with no way back to it.
- **The store is a real rung, not an implementation detail.** ADR-0319's records
  carry their own XML precisely so the file can change underneath them — which
  means the file has an independent life and deserves to be inspectable.
- **Do not make the deletion destructive instead.** Deleting the model with the
  reference would trade an invisible file for a lost one.
- **The deletion stays cheap.** It is the right action when an application no
  longer uses a decision; nothing here should make it feel dangerous.
- **A remote model source owns its own catalog.** Where `ATLAS_DMN_RESOLVER_URL`
  serves models, Atlas has no folder to read and must say so rather than guess.

## Considered options

1. **List the store, and surface the models nothing points at**, so an orphan can
   be found and re-referenced.
2. **Delete the model file with the last reference to it.**
3. **Offer it as a choice in the delete dialog** — a "also delete the model file"
   checkbox.
4. **Fix the sentence only** — say the file stays and can only be reached by
   handle.

## Decision outcome

Chosen option: **1 — the store is listed, and a model nothing points at is shown
where artifacts with no home already are.**

`GET /api/v1/dmn-models` returns one row per stored handle: what the model
declares, whether it compiles, and whether any DMN reference points at it. It is
the only route that reads the model folder *as a folder*; every other one resolves
a single handle.

The Console shows the unreferenced ones in **Not assigned**, as rows in the table
that already holds artifacts belonging to no application, with **Add reference** on
each. That is the same argument ADR-0321 made for a decision that exists only as a
draft — it gets a row of its own "because nothing else in this table represents it"
— applied one rung up. An orphaned model is not a new kind of thing to explain; it
is the thing that has always been there, finally drawn.

The delete confirm stops reassuring and starts saying where the file went.

### Referenced is a fact, the references are a view

Whether a model is pointed at is computed over **every** reference, including ones
in applications the caller cannot see (ADR-0071). The named `references` are
filtered to what the caller may view.

The asymmetry is deliberate and is the safe direction: reporting a model as
unreferenced because of who is looking would invite a second reference to a model
that already has one — exactly the duplicate this record exists to prevent. The
caller learns that *something* points at it, and nothing about what.

### Why the model is not deleted with its reference

Option 2 is the tidy answer and it is wrong in the direction that cannot be undone.
A reference is a pointer; a model is content, and it is content Atlas did not
author — it came from temis, or from an import, or from an editor session whose
author has moved on. More concretely: a decision **deployment** may be running
against a model whose reference somebody tidied away, and while the deployment
carries its own XML and does not need the file, an author restoring that decision
does.

Option 3 (a checkbox) is option 2 with a click in front of it. Destructive defaults
are what dialogs are for, but a dialog offering to delete content as a side effect
of deleting a pointer is a dialog that will be clicked through — and the recovery
it removes is the whole subject of this record.

Option 4 alone is honest and insufficient: knowing that the file is reachable only
by a handle does not help somebody who does not have the handle.

### Deleting a model is left undecided

This record deliberately adds no way to delete a stored model. The store now has a
list, which is what an author needs to recover one; whether Atlas should also be
able to remove one — and what would have to be true first, given that a decision
deployment's provenance points at a handle — is the same question
[ADR-draft-a-decision-deployment-is-not-deletable](draft-a-decision-deployment-is-not-deletable.md)
asks one rung above, and it deserves its own answer rather than a checkbox here.

### Consequences

- **Positive:** A model can no longer become unreachable. The one action that used
  to hide it now has a place that shows it.
- **Positive:** The re-upload-and-duplicate path stops being the only recovery, so
  the store stops accumulating `eligibility-2.dmn`.
- **Positive:** A model that no longer compiles is listed too, which is the only
  way an author finds it in order to fix it.
- **Negative / trade-offs accepted:** The listing resolves and compiles every model
  in the store, so it costs what the decision catalog costs, on a view that is
  opened rarely.
- **Negative:** With a remote temis resolver there is no folder and the route
  refuses with 409. That is the same limit `POST /api/v1/dmn-models` already has,
  and the Console simply shows no section.
- **Follow-ups / risks to watch:** A model nothing points at and nothing has
  deployed is genuinely garbage, and there is still no way to remove it. Left to
  the record above.

## Pros and cons of the options

### Option 1 — list the store *(chosen)*
- Good: nothing becomes unreachable, and the recovery is one click.
- Good: reuses the table that already means "belongs to no application".
- Bad: a route that reads a folder, and a per-model compile on a rarely opened
  view.

### Option 2 — delete the model with the reference
- Good: no orphans, nothing to list.
- Bad: it turns a pointer deletion into content deletion, with no undo — and the
  content is what an author would need to restore the decision.

### Option 3 — a checkbox in the dialog
- Good: the author decides.
- Bad: it is option 2 behind a click, in a dialog people click through, and what it
  removes is the recovery.

### Option 4 — fix the sentence only
- Good: one string, no surface.
- Bad: telling somebody the file is reachable by a handle they do not have is not
  telling them anything.

## Links

- extends [ADR-0034](0034-projects-and-artifacts.md) — the reference this is the rung below
- relates to [ADR-0321](0321-decision-drafts.md) — the rung below that, and the row-of-its-own argument reused here
- relates to [ADR-0222](0222-artifact-id-renames.md) — the suffixed duplicate a lost model produces
- relates to [ADR-0071](0071-sharing-scopes.md) — why "referenced" is a fact and the references are a view
- relates to [ADR-0319](0319-durable-versioned-decision-deployments.md) — the deployment that keeps running without the file
- relates to [ADR-draft-a-decision-deployment-is-not-deletable](draft-a-decision-deployment-is-not-deletable.md) — where deleting content is decided
