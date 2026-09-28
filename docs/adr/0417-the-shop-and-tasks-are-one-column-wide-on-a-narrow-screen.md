# ADR-0417: The shop and Tasks are one column wide on a narrow screen

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-24
- **Deciders:** Atlas maintainers
- **Open question:** measured in Chromium at 390px and 768px only; whether iOS Safari and a real touch device agree (the viewport height under a visible keyboard, the forms' own date and select controls) has not been checked on hardware
- **Question checked:** 2026-09

## Context and problem statement

The shop ([ADR-0312](0312-portal-catalogue-order-inventory.md)) was drawn from desktop
mockups. Its page declared a device viewport, and its header, navigation and action row
already wrapped, but its three working views did not fit a phone. Measured at 390px, the
width of a common phone:

- **The catalogue** is a four-column cascade, Kategorie › Produktgruppe › Produkt ›
  Services ([ADR-0383](0383-portal-level-names.md)). Its columns are at least 220px wide and
  scroll sideways inside their box, so about one and a half of them showed. The stylesheet
  said this was on purpose: "a narrow window should move it, not fold it".
- **The basket** is a grid with a line per offering, which keeps a service beside the
  offering it belongs to. Its services and options columns were cut off.
- **My orders** is a table. An order's positions, the open tasks under them
  ([ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md)) and the form to answer a task
  are all in the last column, which sat past the right edge of the screen. The task form
  was laid out about 780px wide.

The Console's Tasks app is where the same tasks are answered by those who are not the
orderer: an approver, an administrator setting a leaving date. Measured at 390px:

- **The inbox** is three panes side by side: folders (232px), the list (360px, resizable)
  and the open task. Together they are wider than the phone, so the task was off screen.
- **Start** is the same grid with two panes, the startable processes and the form of the
  one chosen.
- **Access review** is a six-column table whose answer buttons are in the last column.
- **The top bar** holds the view names between the app's name and its icons; on a phone
  it pushed the icons past the edge.

Measuring also found that Access review did not open at all, on any screen. The router
passed it, and the operators' Reconciliation, a closure over a navigation generation that
neither route had taken, so the view's first check after loading threw
`gen is not defined`.

The shop is where people order and where they answer the tasks of their orders; Tasks is
where everybody else answers theirs. Both are exactly what somebody does away from a desk:
approving an order, entering the address for a new account. The question is how every view
of both should read on a narrow screen, without changing how it reads on a wide one.

## Decision drivers

- Nothing on a narrow screen may sit past its right edge, the page's or a box's. A table
  that scrolls inside its own frame hides its last column as well as a page that overflows.
- A wide screen keeps the layout it has. The mockups, and the browser tests that measure the
  basket's alignment at 900px, describe it.
- The relations each view draws stay drawn: a service belongs to its offering, a task to its
  position, a column to the one chosen before it.
- One behaviour per screen width across all views, so a reader does not meet a phone layout
  in one view and a squeezed desktop in the next.
- The views are rendered by one script; a second, mobile markup would be a second copy to
  keep in step.

## Considered options

1. **Keep sideways scrolling** and shrink the minimum column width.
2. **One breakpoint, one column below it**: the same markup, restyled. The cascade shows the
   column the reader has reached with a way back; the basket stacks each offering over its
   services and options; the orders table becomes a list of cards.
3. **A separate mobile page** with its own markup.

## Decision outcome

Chosen option: **2**.

- **One breakpoint, 860px.** Below it every view is one column wide. The four-column cascade
  needs about 920px to show its columns at their minimum width, so a small tablet in
  portrait falls below it and reads the shop as a phone does. 860px and not 900px, because
  the browser tests' default window is 900px wide and measures the wide layout.
- **The catalogue steps through its columns.** The page holds which column a narrow screen
  shows (`state.step`). Choosing in a column moves to the next one, and a stepper above the
  column offers a back button and the path chosen so far, in the reader's language. A wide
  screen never reads the step and hides the stepper. Every column is on screen there, so a
  back button would lead somewhere already in view. A search hit opens at the services
  column, where the hit is.
- **Columns nothing is chosen in stack.** "My services" shows what somebody holds under the
  same four headings, with nothing to choose. Below the breakpoint its columns stack instead
  of stepping.
- **The basket stacks each offering over its own services and options.** Each block is
  labelled "Service" or "Optional", because the column names at the top no longer stand
  above them. An offering's block ends before the next one begins, which is the relation the
  wide grid draws by lines.
- **An order is a card.** The column heads are hidden, and each cell names itself from a
  `data-label`. The filters sit two to a row above the cards. A task's form opened in a card
  takes the card's width.
- **Touch targets grow.** The 24px squares (−, +, ×, i, ☆) were sized for a pointer. Below
  the breakpoint they are 40px, and rows grow with them.

The Console's Tasks app follows the same breakpoint and the same rule:

- **The inbox and Start show the list or the task, never both.** The grid carries
  `has-selection` while a task or a process is open. Below the breakpoint that class hides
  the folders and the list; without it the task pane is hidden. The task opens with a
  "Back to the list" button, which a wide screen hides, because the list is beside it.
- **The folders become a row of chips** above the list, scrolling sideways within
  themselves. A task's fields put their label above the value instead of in a 150px column
  beside it, and its actions wrap under its name.
- **Between 861px and 1180px the three panes stay**, at 180px, 300px and the rest. The list's
  resizer writes its widths inline, and a width dragged on a desktop is not a width for a
  tablet, so this range overrides it.
- **An access review row is a card**, each cell named by its label, with "Still needed" and
  "Withdraw…" under it at 40px. The table enhancer's filter row goes with the column heads;
  the campaign and "only rows I can answer" stay above the cards.
- **The top bar gives up the organisation's name**, and the view names scroll sideways
  within the bar, the open one brought into view. The icons stay on screen.

The router's two routes now take the generation before loading the view, as the three routes
above them already did.

The work also found a defect that is not specific to phones. The table's field rule,
`.table input { width:100% }`, also matched the inputs of a task form opened inside an order
row. It stretched the form's checkbox to the cell's width and pushed its label off the end,
on every screen. The rule now covers the filter row only.

### Consequences

- **Positive:** every view of the shop, and the inbox, Start and Access review, can be used at 390px, with nothing past the edge and
  no view that works only by scrolling sideways. The task form, the reason to use the shop
  away from a desk, is laid out at the phone's width.
- **Negative / trade-offs accepted:** on a narrow screen the catalogue shows one level at a
  time, so the four-level decomposition the wide screen shows at a glance is read as a path.
  A small tablet in portrait gets the phone layout, although four narrow columns might just
  fit.
- **Follow-ups / risks to watch:** real-device behaviour, which is the open question above.
  The Console's other surfaces (Operations, the modeller, Catalogue, Panorama) are not covered
  by this record and are still desktop surfaces; the top bar they share is.
  A task's form is drawn by the form viewer, whose own layout is not restyled; a form laid
  out in several columns has not been measured at 390px.

## Pros and cons of the options

### Option 1: keep sideways scrolling
- Good: no change to the markup.
- Bad: on a phone the reader still sees one column and a half and has to find the rest by
  swiping inside a box. The orders table's last column, with the task form in it, stays off
  screen. It moves the problem instead of solving it.

### Option 2: one breakpoint, one column
- Good: one script and one markup; the wide layout is untouched; each view keeps the relation
  it draws.
- Bad: the catalogue's cascade becomes a sequence on a phone, and the page carries a small
  piece of state (`step`) that only a narrow screen reads.

### Option 3: a separate mobile page
- Good: free to design for a phone from scratch.
- Bad: a second rendering of every view to keep in step with the first. The shop's rules,
  such as what may be withdrawn, what a return asks for and which forms a basket needs,
  would be maintained twice.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) (the shop), [ADR-0383](0383-portal-level-names.md) (the cascade's levels), [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md) (tasks under an order's positions)
- tests: `e2e/shop-responsive.spec.mjs`, `e2e/tasks-responsive.spec.mjs`, `e2e/route-superseded.spec.mjs`
