# ADR-DRAFT: The Modeler walks a process's versions and restores one as the draft

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-05
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0452](0452-modeler-shows-its-version.md) put a chip in the editor bar that names the
record on screen — the draft, or version N of M — and how it stands against the other. It
answers "which model am I looking at", and it links to the newest version and to the draft.
It does not answer the question an author brings next: *show me v6, and let me take it back.*

Reaching an older version meant leaving the editor for Operations or the Modeler home, and
there was no way to page through a process's versions from the diagram. Taking a version back
as the draft was possible, but only by accident of the Save button: open the version, press
Save, have the server refuse because the draft exists (ADR-0222), and confirm a browser
dialog that offers the overwrite by its error message. That dialog did not say what the
draft held that would be lost — and a draft keeps no history
([ADR-0021](0021-diagram-drafts.md); [ADR-0031](0031-diagram-version-history.md) is not
built), so whatever was saved and not deployed was gone.

The request, from an author: switch between the deployed models in the Modeler, take any
version back as the draft at any time, and deploy it as a new version.

## Decision drivers

- **Say it where the author already looks.** The chip is the place that names the record on
  screen; a second control saying the same thing beside it can disagree with it.
- **The bar has no room.** At 900 px it has a few dozen pixels to spare (ADR-0452); whatever
  is added must not push it into a second row.
- **A destructive step names what it destroys.** Replacing the draft loses what the draft
  holds beyond every deployed version, and only the server can say what that is.
- **No new server surface for a client question.** The draft comparison
  (`GET /api/v1/drafts/{id}/comparison`) and the process listing already carry everything.

## Considered options

1. **Arrows on the chip and a version list in its panel**, with *Restore as draft* on a
   version's panel and a dialog that warns about the draft.
2. **A separate stepper** between the tabs and the bar's buttons.
3. Option 1, but **refuse** the restore while the draft differs from the newest version.
4. Option 1, plus a **checkpoint** of the draft before it is replaced (part of ADR-0031).

## Decision outcome

Chosen option: **1**.

**Walking.** The chip carries an arrow on each side. The records of a process are ordered
oldest version first, newest last, and the draft after the newest — the draft is where the
next version comes from. ‹ opens the record before the one on screen, › the one after; at
either end the arrow is disabled rather than removed, so the chip does not move. The panel
lists every record, the draft on top and then the versions newest first, each with when and
by whom it was deployed (or saved), tagged *newest*, *= draft* (the version the server says
the draft is the same model as) and *shown*. Arrows and list appear only when there is
somewhere to go. The panel's actions come before the list, and the panel scrolls within the
window, so Restore is in reach however many versions there are.

Every link out of the panel — the arrows, the list, *Open v7*, *Open the draft* — goes
through the question every door out of the editor asks (`makeKeepEdits`): a draft with
unsaved edits is saved first, a version's edits are given up only on a yes. A click that asks
for a new tab is left to the browser.

**Restoring.** A version's panel offers *Restore vN as draft…*, unless the draft already is
that model. The dialog reads the draft against the **newest** version — the chip's own
comparison is against the version on screen, which says nothing about undeployed work — and
says:

- no draft exists: vN becomes the draft; nothing is lost; the button reads *Make vN the
  draft*;
- the draft is the newest version's model: it is replaced, nothing undeployed is lost;
- the draft differs from the newest version: a warning that what it holds beyond that version
  is lost, with the first difference, the counts and the full list of differences; the button
  is styled as destructive;
- the draft could not be read: a warning that what it holds is not known.

It warns and lets the author decide; it does not refuse. Confirming saves the canvas through
the Save path with the overwrite already agreed (no second browser dialog), then reopens the
editor on the draft, so the next Deploy runs it as a new version. Edits made on the version's
canvas go into the draft with it, and the dialog says so.

Saving a version over its existing draft now also moves the session onto the draft
(`fromDeployment` cleared, the URL pointed at the draft), as a first save of a version already
did. Before, that path left the session believing it addressed a version, so its Deploy
panel said the draft would be left alone when it had just been replaced.

### Why not the others

- **A separate stepper** (2) is easier to find and steps without opening anything. It repeats
  what the chip says in a second place, which can disagree with it for a moment after a save,
  takes room the bar does not have at 900 px, and walks one version at a time with no way to
  jump to v2 of 20.
- **Refusing** (3) protects undeployed work absolutely, but it makes the common case — "the
  draft is a dead end, take v6 back" — require deploying or discarding the draft first, which
  is a worse outcome than the one it prevents. The author asked for a warning.
- **A checkpoint** (4) would make the restore reversible, which the warning cannot. It needs a
  history store beside the draft store, which is ADR-0031's decision to make, not this one's.

### Consequences

- **Positive:** any deployed version is one click from the diagram, and one dialog from being
  the draft again, with what that costs stated before it is paid. The Save-path inconsistency
  above is gone.
- **Negative / trade-offs accepted:** the restore cannot be undone; the warning is the only
  safeguard. "Undeployed" is judged against the newest version only: a draft that equals an
  older version is reported as differing from the newest, which is true but reads as a larger
  loss than it is. A draft session now reads `GET /api/v1/processes` on each refresh when the
  draft has been deployed — the same listing a version session already read.
- **Follow-ups / risks to watch:** a co-editor in a live session on the draft
  ([ADR-0140](0140-live-collaborative-modeling-sessions.md)) is not told that the draft was
  replaced from outside the session; their next Save writes over the restored model. That was
  already so for Save from a version. A checkpoint before the overwrite remains ADR-0031's.

## Links

- extends [ADR-0452](0452-modeler-shows-its-version.md)
- relates to [ADR-0021](0021-diagram-drafts.md), [ADR-0031](0031-diagram-version-history.md),
  [ADR-0222](0222-artifact-id-renames.md), [ADR-0530](0530-compare-process-versions.md)
