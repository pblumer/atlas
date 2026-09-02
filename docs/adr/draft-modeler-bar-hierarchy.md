# ADR-DRAFT: The Modeler's editor bar carries two acts and a menu

- **Status:** Accepted
- **Date:** 2026-09-02
- **Deciders:** Modeler UI

## Context and problem statement

The Modeler's editor bar ended with seven buttons, added one at a time as the editor
grew: Token simulation, Variables, Auto-layout, Save, Export XML, Documentation, Deploy.
Every one of them was `class="btn neutral"` — the same white fill, the same border, the
same weight, the same height. They read as one group because they looked like one group,
but they are four unrelated kinds of act:

- **two view toggles** that turn something on and leave it on (Token simulation,
  Variables) — and only one of them even carried `aria-pressed`, so the other announced
  itself as a plain command;
- **one canvas command** that re-flows the diagram in place (Auto-layout);
- **three document acts** that write or emit a file (Save, Export XML, Documentation);
- **one act that leaves the browser entirely** and cannot be taken back (Deploy).

Giving all seven the same weight says they are the same size of decision. They are not:
Auto-layout nudges boxes, Deploy puts a definition on a server. A row of identical
rectangles also has no anchor for the eye — every border says "separate object", so the
bar read as seven competing objects and roughly sixty characters of label, competing in
the same 44-px line as the breadcrumbs and the tabs.

Two further things followed from the flat row rather than from any single button:

**It broke apart under width.** `.editor-bar` is `flex-wrap: wrap` and the buttons were
its direct children, so a narrower window did not shrink the group — it dropped
individual buttons into a second, ragged row, splitting the group at whatever point the
arithmetic landed on rather than at a meaningful edge. The e2e viewport is 900 px wide,
which is well inside that.

**The bar did not know which tab it was on.** `wireTabs` switches the properties panel
and nothing else, so all seven buttons showed on every tab. On the Playground tab that
put "▶ Token simulation" — a drawn walkthrough with no engine behind it (ADR-0078) —
directly above the Playground's own "▶ Run", which drives a real sandboxed engine
(ADR-0215): two different executions, the same triangle, one row apart.

The question is what the top of the editor is *for*.

## Decision drivers

- An act that cannot be undone must not look like an act that can.
- State and command are different things and should not share a shape.
- A control belongs where a reader would look for what it changes.
- The bar competes for one line with breadcrumbs and tabs; it has to hold at the widths
  people actually use, not only at a wide desktop.
- Whatever changes, every control keeps working: the behaviour is wired by id across
  several thousand lines and a dozen e2e specs.

## Considered options

1. **Group and rank in place.** Keep all seven visible; put the two toggles in a
   segmented control, reduce Auto-layout to an icon, collapse Export XML and
   Documentation under one "Export ▾", and make Deploy the only filled button.
2. **Move the canvas tools onto the canvas.** As above, but Token simulation, Variables
   and Auto-layout leave the bar for a floating palette over the diagram, shown according
   to the tab; the bar keeps only document-level acts.
3. **Two acts and a menu.** The bar carries Save and Deploy. Everything else moves into
   one overflow menu behind a "…" trigger.

## Decision outcome

Chosen option: **"Two acts and a menu"** (option 3), with Deploy as the only filled
button and Save beside it.

The bar now answers one question — *what do I do with this diagram* — and answers it with
the two things an author does over and over. Deploy is filled because it is the only act
here that leaves the browser; Save is next to it because it is the one pressed most, and
stays `neutral` so the rank between the two is legible. The remaining five sit in a menu
grouped **View** (Token simulation, Variables) and **Diagram** (Auto-layout, Export XML,
Documentation) — a division that states the thing the flat row hid, that the first two
change what you see and the last three act on the diagram itself.

A toggle in a menu cannot lean on looking pressed, so "on" is a check at the end of the
row plus `aria-pressed`; the accent fill both toggles used to take is how a *button*
reads as held down and would read on a menu row as the row under the pointer.

Every control kept its id. Nothing about what the buttons *do* moved — `wireActions`,
`wireTokenSim`, `wireEditorVars` and the F8 shortcut all reach the same elements, F8
included, because a keyboard shortcut clicks the button wherever it sits.

The menu deliberately does **not** use `app.js`'s delegated `.dropdown-toggle` handler,
which drives every other menu in the Console. `editor.js` is mounted on its own by the
e2e harnesses, with no `app.js` in the page; a bar that depended on that handler would be
a bar the harnesses cannot open. It borrows the `.dropdown-menu` look and carries its own
behaviour (`wireBarMenu`), stopping propagation on the trigger so `app.js`'s
close-everything-on-any-click does not close it the instant it opens.

### Consequences

- **Positive:** Deploy is unmistakable and no longer one of seven identical rectangles.
  The bar holds one row at any width anyone uses, so `flex-wrap` no longer splits a group
  mid-way. The two toggles finally announce their state. The Playground tab no longer
  shows two play triangles for two different engines in adjacent rows.
- **Negative / trade-offs accepted:** Five controls cost a click they did not cost
  before, and a reader who has not opened the menu cannot see that they exist —
  discoverability is the price of quiet, and it is a real price for someone opening the
  Modeler for the first time. Auto-layout in particular loses its label from the bar; F8
  is unchanged and is now shown nowhere, which is a gap this record does not close.
- **One control could not simply move.** Token simulation is not a command but a *mode*:
  while it is on, the diagram is played rather than edited and the modeling palette and
  context pad are hidden. Putting its only switch behind a menu would make it a mode that
  is easy to enter and awkward to leave, so the simulation's own control bar — which is
  on screen for exactly as long as the mode is — now carries **Exit simulation**. The
  menu row still toggles it; the bar is the way out you can see from inside.
- **The bar is still not tab-aware, and this record does not make it so.** The two play
  triangles no longer sit one above the other because one of them moved into a menu, not
  because the bar learned which tab it is on: Token simulation is still offered on the
  Playground tab, where it is at best pointless. The collision is gone; the underlying
  fact that `wireTabs` switches only the properties panel is untouched. Option 2 is what
  would fix it, and remains available on top of this.
- **Follow-ups / risks to watch:** `docs/screenshots/modeler.png`, the README's front-page
  image, shows the old seven-button bar and is now wrong about it. It was already stale
  in two other ways — the top nav reads "Marketplace" rather than "Repository", and there
  is no Playground tab — so it wants one retake for all three, not a patch for this. No
  keyboard shortcuts are advertised in the menu yet;
  the rows are where they would go. A command palette would make the menu a convenience
  rather than the only way in, and is the obvious next step if the click proves to cost
  more than the quiet is worth. `flex-wrap: wrap` on `.editor-bar` is left alone — with
  three controls it no longer bites — but note that `app.css` gives `.editor-bar >
  .crumbs` `overflow: hidden` and `text-overflow: ellipsis` while `nav.crumbs` sets
  `display: flex`, so the breadcrumbs never actually ellipsize; the shortening that was
  meant there does not happen.

## Pros and cons of the options

### Option 1 — group and rank in place
- Good: nothing is hidden; no control costs an extra click; purely classes and order.
- Good: fixes the rank problem and most of the width problem on its own.
- Bad: leaves five labels in the bar, so the bar still competes with breadcrumbs and tabs
  for the line, and still wraps on a narrow window, only later.
- Bad: does nothing about the two play triangles on the Playground tab, because
  Token simulation stays in the bar.

### Option 2 — canvas tools onto the canvas
- Good: puts each control where the thing it changes is, which is where a hand goes.
- Good: solves the tab problem structurally — the palette shows what the tab supports.
- Bad: a floating palette overlays the diagram and needs a corner that is reliably dead,
  and must not cover the bpmn.io watermark.
- Bad: the largest change of the three, and it makes the bar's contents depend on the tab,
  which is new behaviour to keep correct in both directions.

### Option 3 — two acts and a menu (chosen)
- Good: the bar states its purpose in two buttons and cannot wrap.
- Good: a menu is the ordinary home for "everything else", and the Console already has
  the component; groups and check marks carry meaning a flat row could not.
- Bad: hides five controls behind a click, which is worst for a first-time author.
- Bad: a menu row is a weaker home for a *toggle* than a button is; the check mark carries
  the whole of the state.

## Links

- relates to [ADR-0078](0078-design-view-token-simulation.md) — the Token simulation this bar toggles
- relates to [ADR-0215](0215-modeler-playground.md) — the Playground tab whose Run this no longer sits above
- relates to [ADR-0143](0143-process-documentation-export.md) — the Documentation act now in the menu
- relates to [ADR-0128](0128-process-applications.md) — Deploy here is the single-diagram deploy
