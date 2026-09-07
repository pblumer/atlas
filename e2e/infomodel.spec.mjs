// End-to-end coverage for the UML class canvas (api/web/infomodel-editor.js,
// ADR-0230 slice 2).
//
// The case behind it: BPMN scopes a data object to one process definition and
// leaves its type slot opaque, so the class diagram is where a type gets a meaning
// that two processes can share. The canvas is only worth having if it enforces the
// same rules the server does — and it does that by being *served* the matrix rather
// than carrying its own, which is what these tests pin.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/infomodel-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-class").first()).toBeVisible();
});

// Addressed by the name on the group, not by matching label text: "Order" is a
// prefix of "OrderStatus", and a locator that cannot tell them apart is a locator
// that will silently pass on the wrong box.
//
// It resolves to the diagram-js element rather than to the drawing inside it,
// because diagram-js lays a hit rectangle over every shape — which is what makes a
// click select for a real user, and what a click aimed at the drawing bounces off.
const box = (page, name) => page.locator(`.djs-element:has(.uml-class[data-name="${name}"])`);

test("a class reads as UML: its kind, its members, and which of them identify it", async ({ page }) => {
  await expect(page.locator(".uml-class")).toHaveCount(4);

  const order = box(page, "Order");
  await expect(order.locator(".uml-stereo")).toHaveText("«businessObject»");
  // The business key is marked on the box, because it is the fact the whole model
  // turns on — what makes Order#ORD-1 the same order in two processes.
  await expect(order.locator(".uml-attr.key .uml-attr-name")).toHaveText("⚿ id");
  // A non-default multiplicity is shown; "exactly one" is the unstated default.
  await expect(order.locator(".uml-attr-mult")).toHaveText(" [0..1]");

  // An enumeration carries literals where the others carry attributes.
  const status = box(page, "OrderStatus");
  await expect(status.locator(".uml-stereo")).toHaveText("«enumeration»");
  await expect(status.locator(".uml-literal")).toHaveCount(2);
  await expect(status.locator(".uml-attr")).toHaveCount(0);
});

test("the canvas refuses what the server would refuse, in the server's words", async ({ page }) => {
  // A value type cannot be the whole that owns parts, so Address → Customer as a
  // composition must be refused while it is being drawn.
  await page.locator('.im-connect[data-kind="composition"]').click();
  await box(page, "Address").click();
  // Every class the matrix rules out fades, so the canvas offers only what is legal.
  await expect(box(page, "Customer").locator(".uml-class")).toHaveClass(/unreachable/);
  await box(page, "Customer").click();

  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts.length).toBe(1);
  expect(toasts[0].kind).toBe("err");
  // The refusal teaches the notation rather than only saying no.
  expect(toasts[0].msg).toContain("no existence of its own");
  // And nothing was drawn.
  await expect(page.locator(".uml-edge")).toHaveCount(1);
});

test("an enumeration cannot be related to at all", async ({ page }) => {
  await page.locator('.im-connect[data-kind="association"]').click();
  await box(page, "Order").click();
  await expect(box(page, "OrderStatus").locator(".uml-class")).toHaveClass(/unreachable/);
  await box(page, "OrderStatus").click();
  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts[0].msg).toContain("closed set of values");
});

test("a legal relationship is drawn, and the panel states how to read it", async ({ page }) => {
  await page.locator('.im-connect[data-kind="composition"]').click();
  await box(page, "Order").click();
  await box(page, "Address").click();

  await expect(page.locator(".uml-edge")).toHaveCount(2);
  // Selecting it lands on the relationship panel, which names both ends.
  await expect(page.locator(".im-reading")).toHaveText("Order → Address");
  // A composition is marked at the whole — the end the ownership belongs to.
  const drawn = page.locator(".uml-edge.composition .uml-edge-line");
  await expect(drawn).toHaveAttribute("marker-start", "url(#uml-diamond-solid)");
  await expect(page.locator("#im-save")).toBeEnabled();
});

test("a relationship is picked off the drawing, and stays picked while it is edited", async ({ page }) => {
  // A line is the one thing diagram-js draws no outline for, so which relationship
  // you are editing has to be said on the drawing or it is only said in the panel.
  const edge = page.locator(".djs-element:has(.uml-edge)");
  await edge.click();
  await expect(page.locator(".im-reading")).toHaveText("Customer → Order");
  await expect(edge).toHaveClass(/selected/);
  // The ends carry the role and the multiplicity: "1 customer places 0..* orders" is
  // the sentence the diagram is drawn to say.
  await expect(page.locator(".uml-end-label")).toHaveText(["customer 1", "orders 0..*"]);

  // Typing in the panel re-renders, and reconciling rebuilds every line — so the
  // selection has to survive that, or the first keystroke deselects what is being
  // renamed and the panel closes under the cursor.
  await page.locator("#im-a-name").fill("bestellt");
  await expect(page.locator(".uml-edge-label")).toHaveText("bestellt");
  await expect(page.locator(".im-reading")).toHaveText("Customer → Order");
  await expect(page.locator(".djs-element:has(.uml-edge)")).toHaveClass(/selected/);
  expect(page.__errors).toEqual([]);
});

test("a generalization has no roles, because is-a is not counted", async ({ page }) => {
  await page.locator('.im-connect[data-kind="generalization"]').click();
  await box(page, "Order").click();
  await box(page, "Customer").click();
  await expect(page.locator(".im-reading")).toHaveText("Order → Customer");
  await expect(page.locator(".im-end")).toHaveCount(0);
  await expect(page.locator(".im-hint-text")).toContainText("not a counted relationship");
});

test("editing a class updates the drawing, and a rename retypes what referred to it", async ({ page }) => {
  await box(page, "Address").click();
  await page.locator("#im-c-name").fill("PostalAddress");
  await expect(box(page, "PostalAddress")).toBeVisible();

  // Switching a business object to a value type takes its business key with it: a
  // value has no identity of its own to declare.
  await box(page, "Order").click();
  await expect(page.locator(".im-attrs input[type=checkbox]")).toHaveCount(3);
  await page.locator("#im-c-stereo").selectOption("valueType");
  await expect(page.locator(".im-attrs input[type=checkbox]")).toHaveCount(0);
  await expect(box(page, "Order").locator(".uml-attr.key")).toHaveCount(0);
});

// The order of a class's attributes is not a view setting: a class box reads top to
// bottom, so which attribute comes first is a statement about the class. A business
// key belongs where a reader looks for it. `attributes` is already an ordered array
// in the document, so moving a row is a model edit — it dirties the model, redraws
// the box, and is what gets saved.
test("an attribute can be moved, and the box and the document follow", async ({ page }) => {
  const order = box(page, "Order");
  const names = () => order.locator(".uml-attr-name");
  await expect(names()).toHaveText(["⚿ id", "placedOn", "total"]);

  await order.click();
  // Alt+Down on the field being edited, so reordering needs no pointer and does not
  // take the author out of the row they are working in.
  await page.locator('tr[data-attr="0"] [data-f="name"]').focus();
  await page.keyboard.press("Alt+ArrowDown");

  await expect(names()).toHaveText(["placedOn", "⚿ id", "total"]);
  // The caret followed the row it was in, not the position it left.
  await expect(page.locator('tr[data-attr="1"] [data-f="name"]')).toBeFocused();
  // And the key is still the key: reordering moves an attribute, it does not
  // redeclare identity.
  await expect(order.locator(".uml-attr.key .uml-attr-name")).toHaveText("⚿ id");

  await page.locator("#im-save").click();
  await expect.poll(() => page.evaluate(() => window.__saved)).toBeTruthy();
  const saved = await page.evaluate(() => window.__saved);
  const cls = saved.classes.find((c) => c.name === "Order");
  expect(cls.attributes.map((a) => a.name)).toEqual(["placedOn", "id", "total"]);
  expect(cls.identity).toEqual(["id"]);
});

test("a row is dragged only by its grip", async ({ page }) => {
  const order = box(page, "Order");
  await order.click();
  // The grip is what carries the drag. Were the whole row draggable, selecting a
  // word inside a name field would drag the attribute instead of the text.
  const row = page.locator('tr[data-attr="0"]');
  await expect(row).toHaveJSProperty("draggable", false);
  await page.locator('tr[data-attr="0"] .im-grip').hover();
  await page.mouse.down();
  await expect(row).toHaveJSProperty("draggable", true);
  await page.mouse.up();

  await page.locator('tr[data-attr="0"] [data-f="name"]').hover();
  await page.mouse.down();
  await expect(row).toHaveJSProperty("draggable", false);
  await page.mouse.up();
});

test("an enumeration's literals reorder the same way", async ({ page }) => {
  const status = box(page, "OrderStatus");
  await expect(status.locator(".uml-literal")).toHaveText(["draft", "approved"]);

  await status.click();
  await page.locator('tr[data-lit="1"] [data-f="literal"]').focus();
  await page.keyboard.press("Alt+ArrowUp");
  await expect(status.locator(".uml-literal")).toHaveText(["approved", "draft"]);
});

test("a move past either end is refused rather than wrapping", async ({ page }) => {
  const order = box(page, "Order");
  await order.click();
  await page.locator('tr[data-attr="0"] [data-f="name"]').focus();
  await page.keyboard.press("Alt+ArrowUp"); // already first
  await expect(order.locator(".uml-attr-name")).toHaveText(["⚿ id", "placedOn", "total"]);
  // Nothing moved, so nothing was edited: the model is still clean.
  await expect(page.locator("#im-dirty")).toBeHidden();
});

test("saving sends local handles for new shapes and lets the server name them", async ({ page }) => {
  await page.locator('.im-add[data-stereotype="businessObject"]').click();
  await page.locator("#im-c-name").fill("Invoice");
  await page.locator('[data-act="add-attr"]').click();
  await page.locator("#im-save").click();

  const saved = await page.evaluate(() => window.__saved);
  const invoice = saved.classes.find((c) => c.name === "Invoice");
  expect(invoice).toBeTruthy();
  // The canvas names a box it has just drawn with a local handle; ids are the
  // server's to issue, and it rewrites the ends that pointed at the handle.
  expect(invoice.id.startsWith("new-")).toBe(true);
  expect(saved.revision).toBe(3);
  await expect(page.locator("#im-rev")).toHaveText("r4");
  await expect(page.locator("#im-dirty")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a refused save shows the server's findings rather than one sentence", async ({ page }) => {
  await page.evaluate(() => { window.__saveFails = true; });
  await box(page, "Order").click();
  await page.locator("#im-c-name").fill("Ordr");
  await page.locator("#im-save").click();

  await expect(page.locator(".im-problem-head")).toHaveText("1 problem");
  await expect(page.locator("button.im-problem")).toContainText("no class of that name");
  // The tag is the distinction the whole validation rests on: a modeling mistake is
  // not the same answer as "this build does not author that".
  await expect(page.locator("button.im-problem .im-problem-tag")).toHaveText("invalid");
  // Clicking a problem selects what it is about, so it can be fixed where it is.
  await page.locator("button.im-problem").click();
  await expect(page.locator(".phead .kv")).toHaveText("Business object");
  // The header names the element, so it carries the rename that caused the finding.
  await expect(page.locator(".phead b")).toHaveText("Ordr");
});

test("the JSON Schema projection is shown as derived, and says what it dropped", async ({ page }) => {
  await box(page, "Order").click();
  await page.locator('[data-act="schema"]').click();
  await expect(page.locator(".im-schema")).toContainText("json-schema.org/draft/2020-12");
  await expect(page.locator(".im-loss")).toContainText("JSON Schema has no keyword for identity");
  await expect(page.locator(".psec .im-hint-text")).toContainText("Derived, never edited");
  await page.locator('[data-act="close-schema"]').click();
  await expect(page.locator(".im-schema")).toHaveCount(0);
});

test("the panel states that this is a subset, and what it does not author", async ({ page }) => {
  await page.locator(".djs-container svg").click({ position: { x: 4, y: 4 } });
  await expect(page.locator('.pgroup[data-group="This is a subset of UML"]')).toHaveCount(1);
  await expect(page.locator(".im-note li")).toHaveCount(3);
  await expect(page.locator(".im-note")).toContainText("Where a datum lives is the data store's question");
});

// The properties panel wears the Modeler's chrome (api/web/pgroup.js): an element
// header naming what is selected, and collapsible property groups. Shared code rather
// than a lookalike — a person moves between the two surfaces in one session.
test("the panel is grouped like the Modeler's, and remembers what was opened", async ({ page }) => {
  await box(page, "Order").click();
  // Every group starts open here. A class has three sections and one of them is its
  // attributes, so collapsing is worth offering and not worth doing by default — the
  // opposite of the Modeler's dozen groups, where only General starts open.
  await expect(page.locator('.pgroup[data-group="General"]')).not.toHaveClass(/collapsed/);
  await expect(page.locator('.pgroup[data-group="Attributes"]')).not.toHaveClass(/collapsed/);
  // The dot says a section carries content, so a collapsed one still says so.
  await expect(page.locator('.pgroup[data-group="Attributes"] .pgroup-dot')).toHaveCount(1);

  await page.locator('.pgroup[data-group="Attributes"] .pgroup-head').click();
  await expect(page.locator('.pgroup[data-group="Attributes"]')).toHaveClass(/collapsed/);

  // Selecting another class re-renders the panel from scratch. The choice is held by
  // group title, so it survives that — otherwise every selection would re-open the
  // section the author just put away.
  await box(page, "Customer").click();
  await expect(page.locator('.pgroup[data-group="Attributes"]')).toHaveClass(/collapsed/);

  // Collapse all closes General too, so the whole outline of an element fits at once.
  await page.locator('.pgroup-all[data-all="collapse"]').click();
  await expect(page.locator(".pgroup.collapsed")).toHaveCount(await page.locator(".pgroup").count());
  await page.locator('.pgroup-all[data-all="expand"]').click();
  await expect(page.locator(".pgroup.collapsed")).toHaveCount(0);

  expect(page.__errors).toEqual([]);
});

test("a relationship's ends are their own group, and a generalization says why it has none", async ({ page }) => {
  await page.locator(".djs-element:has(.uml-edge)").click();
  await expect(page.locator(".phead .kv")).toHaveText("Association");
  await expect(page.locator('.pgroup[data-group="Ends"]')).toHaveCount(1);
  // Roles and multiplicities are content, so the group says so before it is opened.
  await expect(page.locator('.pgroup[data-group="Ends"] .pgroup-dot')).toHaveCount(1);

  await page.locator('.im-connect[data-kind="generalization"]').click();
  await box(page, "Order").click();
  await box(page, "Customer").click();
  await expect(page.locator(".phead .kv")).toHaveText("Generalization");
  // The group is still there — it is where the sentence explaining the absence goes.
  await expect(page.locator('.pgroup[data-group="Ends"] .im-hint-text'))
    .toContainText("not a counted relationship");
  await expect(page.locator(".im-end")).toHaveCount(0);
});

// Data stores on the class canvas (ADR-0230, slice 5b).
// A store is where instances of a class outlive the process that made them — the
// thing BPMN's <dataStoreReference> gestures at and then says nothing about. It is
// declared once per application here, and named by every process that reaches it.
test.describe("data stores", () => {
  const store = (page) => page.locator('.djs-element:has(.uml-store[data-name="Orders"])');

  test("a store is drawn as a store, and says what it holds", async ({ page }) => {
    await expect(store(page)).toBeVisible();
    // Not a class box: the one mistake to prevent is reading it as one. A class says
    // what an Order is; a store says where Orders are kept.
    await expect(store(page).locator(".uml-store-body")).toHaveCount(1);
    await expect(store(page).locator(".uml-store-name")).toHaveText("Orders");
    await expect(store(page).locator(".uml-store-sub")).toHaveText("«read» Order");
    // The line to the class it keeps is an annotation, not an association.
    await expect(page.locator(".uml-store-link")).toHaveCount(1);
  });

  test("the panel offers only classes a store can keep", async ({ page }) => {
    await store(page).click();
    await expect(page.locator(".phead .kv")).toHaveText("Data store");
    // Only a business object with a business key: a process reads from a store by
    // naming which thing it wants, and the key is the only thing that names one.
    // Customer and Order have keys; Address is a value type and OrderStatus an
    // enumeration, so neither is offered.
    const options = await page.locator("#im-s-class option").evaluateAll((els) => els.map((e) => e.value));
    expect(options).toEqual(["", "Customer", "Order"]);
    await expect(page.locator("#im-s-worker")).toHaveValue("clio-main");
    await expect(page.locator(".psec")).toContainText("business object with a business key");
  });

  test("adding a store puts it on the canvas and selects it", async ({ page }) => {
    await page.locator('[data-add="store"]').click();
    await expect(page.locator(".uml-store")).toHaveCount(2);
    await expect(page.locator(".phead .kv")).toHaveText("Data store");
    await page.locator("#im-s-name").fill("Invoices");
    await expect(page.locator('.djs-element:has(.uml-store[data-name="Invoices"])')).toBeVisible();
    // A store with no class yet says so rather than claiming to hold something.
    await expect(page.locator('.djs-element:has(.uml-store[data-name="Invoices"]) .uml-store-sub')).toHaveText("holds nothing yet");

    await page.locator("#im-save").click();
    const saved = await page.evaluate(() => window.__saved);
    const added = saved.stores.find((s) => s.name === "Invoices");
    expect(added).toBeTruthy();
    // Ids are the server's to issue, here as everywhere else.
    expect(added.id.startsWith("new-")).toBe(true);
    expect(page.__errors).toEqual([]);
  });

  test("the subset states that writing through a store is not authored", async ({ page }) => {
    await page.locator(".djs-container svg").click({ position: { x: 4, y: 4 } });
    await expect(page.locator(".im-note")).toContainText("Writing through a data store");
  });
});

// A class box takes its colour from CSS alone — the renderer sets no `fill` (see
// api/web/vendor/canvas/src/uml.js). That makes the canvas one bad paint away from
// unreadable, because `fill` is inherited and its initial value is black: a tint
// that fails to compute is not an off-colour box, it is a solid black one with its
// label painted black on top. The tints are registered with @property so a mix a
// browser will not compute falls to a pale literal instead, and this pins that —
// asserting the fallback, not the mix, because the mix is what breaks.
test("a class box never falls through to SVG's black default", async ({ page }) => {
  const fills = () => page.evaluate(() =>
    [".uml-class.businessObject .uml-box", ".uml-class.enumeration .uml-box",
      ".uml-class.valueType .uml-box", ".uml-store .uml-store-body"]
      .map((sel) => getComputedStyle(document.querySelector(sel)).fill));

  // Every source the tints mix from, made unresolvable at the root — which is what
  // a malformed theme cache does, since index.html applies it without validating.
  await page.evaluate(() => {
    for (const v of ["--accent-soft", "--bg", "--muted", "--accent"]) {
      document.documentElement.style.setProperty(v, "not-a-colour");
    }
  });

  for (const fill of await fills()) {
    expect(fill).not.toBe("rgb(0, 0, 0)");
    // Still the intended pale tint: the colour is lost, the drawing is not.
    const [r, g, b] = fill.match(/[\d.]+/g).map(Number);
    expect(Math.min(r, g, b)).toBeGreaterThan(200);
  }
  expect(page.__errors).toEqual([]);
});

// Zoom, pan and fit have been the canvas's own since it moved onto diagram-js
// (ADR-0237). What a person found was a diagram they could not make fit: the wheel
// scrolls, ctrl and the wheel zoom, and nothing on the screen says so. These are the
// controls the record's "looks like the two canvases beside it" was about.
//
// The assertion is the viewport's own scale rather than a class on a button: the
// question is whether the drawing actually zoomed.
const scale = (page) => page.evaluate(() => {
  const viewport = document.querySelector("#im-canvas .djs-container .viewport");
  return new DOMMatrix(getComputedStyle(viewport).transform).a;
});

test("the canvas can be zoomed and fitted from the controls, not only by a gesture", async ({ page }) => {
  const tools = page.locator("#im-canvas .im-tools");
  await expect(tools.locator('[data-tool="zoom-in"]')).toBeVisible();
  await expect(tools.locator('[data-tool="zoom-out"]')).toBeVisible();
  await expect(tools.locator('[data-tool="fit"]')).toBeVisible();

  const fitted = await scale(page);
  expect(fitted).toBeGreaterThan(0);

  await tools.locator('[data-tool="zoom-in"]').click();
  const zoomedIn = await scale(page);
  expect(zoomedIn).toBeGreaterThan(fitted);

  await tools.locator('[data-tool="zoom-out"]').click();
  expect(await scale(page)).toBeLessThan(zoomedIn);

  // Fit is the way back: after zooming somewhere, one click returns the whole
  // diagram to the window.
  await tools.locator('[data-tool="zoom-in"]').click();
  await tools.locator('[data-tool="zoom-in"]').click();
  expect(await scale(page)).toBeGreaterThan(fitted);
  await tools.locator('[data-tool="fit"]').click();
  expect(await scale(page)).toBeCloseTo(fitted, 5);

  expect(page.__errors).toEqual([]);
});

// The zoom has to stop somewhere at both ends: a canvas that keeps zooming out loses
// the diagram in a dot, and one that keeps zooming in loses it off every edge.
test("zooming stops at a bound rather than running away", async ({ page }) => {
  const tools = page.locator("#im-canvas .im-tools");
  for (let i = 0; i < 15; i++) await tools.locator('[data-tool="zoom-out"]').click();
  const out = await scale(page);
  expect(out).toBeGreaterThanOrEqual(0.2);

  for (let i = 0; i < 25; i++) await tools.locator('[data-tool="zoom-in"]').click();
  expect(await scale(page)).toBeLessThanOrEqual(4);
  expect(page.__errors).toEqual([]);
});

// The controls sit over the sheet, so the sheet has to stay reachable around them:
// the group itself ignores the pointer and only its buttons take it.
test("the control group does not take the pointer away from the sheet", async ({ page }) => {
  const events = await page.evaluate(() => ({
    group: getComputedStyle(document.querySelector("#im-canvas .im-tools")).pointerEvents,
    button: getComputedStyle(document.querySelector('#im-canvas .im-tools [data-tool="fit"]')).pointerEvents,
  }));
  expect(events.group).toBe("none");
  expect(events.button).toBe("auto");
});

// Finding something in a model that outgrew its window. Two surfaces, one need: a
// class among many on the sheet, and a member among many in the panel.
const search = (page) => page.locator("#im-search");
const hits = (page) => page.locator("#im-search-results .im-search-hit");

test("the search finds a class and brings it into view", async ({ page }) => {
  await search(page).fill("addr");
  await expect(hits(page)).toHaveCount(1);
  await expect(hits(page).first()).toContainText("Address");

  await hits(page).first().click();
  // Found means selected: the panel is on it, and the canvas says so too.
  await expect(page.locator(".psec input#im-c-name")).toHaveValue("Address");
  await expect(page.locator(".djs-element.selected .uml-class[data-name='Address']")).toHaveCount(1);
  // The field empties itself, so the next search starts from nothing.
  await expect(search(page)).toHaveValue("");
  expect(page.__errors).toEqual([]);
});

// The half that matters on a class with forty attributes: a member is findable
// without knowing which class holds it, and picking it narrows the panel to it.
test("the search finds a member, and the panel opens narrowed to it", async ({ page }) => {
  await search(page).fill("placedOn");
  await expect(hits(page).first()).toContainText("Order · placedOn");

  await hits(page).first().click();
  await expect(page.locator(".psec input#im-c-name")).toHaveValue("Order");
  await expect(page.locator("#im-member-filter")).toHaveValue("placedOn");
  const rows = page.locator(".im-attrs tbody tr[data-attr]");
  await expect(rows).toHaveCount(3);          // every row is still there…
  await expect(rows.locator("visible=true")).toHaveCount(1); // …one is on screen
  await expect(page.locator("#im-member-count")).toHaveText("1 of 3");
  expect(page.__errors).toEqual([]);
});

test("a search that matches nothing says so and changes no selection", async ({ page }) => {
  await search(page).fill("zzz");
  await expect(page.locator(".im-search-none")).toContainText("Nothing in this model matches");
  await expect(hits(page)).toHaveCount(0);
  // Nothing was selected: no class panel, nothing marked on the sheet.
  await expect(page.locator(".psec input#im-c-name")).toHaveCount(0);
  await expect(page.locator(".djs-element.selected")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test.describe("the member filter", () => {
  test.beforeEach(async ({ page }) => {
    await box(page, "Order").click();
    await expect(page.locator("#im-member-filter")).toBeVisible();
  });

  test("narrows the table without removing a row, and counts what it hid", async ({ page }) => {
    const rows = page.locator(".im-attrs tbody tr[data-attr]");
    await expect(rows.locator("visible=true")).toHaveCount(3);

    await page.locator("#im-member-filter").fill("total");
    await expect(rows).toHaveCount(3);
    await expect(rows.locator("visible=true")).toHaveCount(1);
    await expect(page.locator("#im-member-count")).toHaveText("1 of 3");

    // Clearing puts them all back.
    await page.locator("#im-member-filter").fill("");
    await expect(rows.locator("visible=true")).toHaveCount(3);
    await expect(page.locator("#im-member-count")).toHaveText("");
    expect(page.__errors).toEqual([]);
  });

  test("matches a member's type as well as its name", async ({ page }) => {
    await page.locator("#im-member-filter").fill("date");
    const shown = page.locator(".im-attrs tbody tr[data-attr]:visible input[data-f='name']");
    await expect(shown).toHaveCount(1);
    await expect(shown).toHaveValue("placedOn");
  });

  test("keeps the caret in the field, and still edits the right attribute", async ({ page }) => {
    // The panel re-renders on every keystroke of an edit; the filter must not, or
    // typing into it would take the caret out of it after the first letter.
    await page.locator("#im-member-filter").pressSequentially("tot");
    await expect(page.locator("#im-member-filter")).toBeFocused();
    await expect(page.locator("#im-member-filter")).toHaveValue("tot");

    // A row hidden by the filter keeps its index, so the visible row still edits the
    // attribute it names rather than the one that happens to be third on screen.
    const name = page.locator(".im-attrs tbody tr[data-attr]:visible input[data-f='name']");
    await name.fill("totalAmount");
    await expect(box(page, "Order").locator(".uml-attr-name")).toContainText(["id", "placedOn", "totalAmount"]);
    expect(page.__errors).toEqual([]);
  });

  test("says when nothing matches, and refuses to reorder while narrowed", async ({ page }) => {
    await page.locator("#im-member-filter").fill("nothinghere");
    await expect(page.locator(".im-filter-empty")).toContainText("Nothing here matches");
    await expect(page.locator(".im-attrs tbody tr[data-attr]:visible")).toHaveCount(0);

    await page.locator("#im-member-filter").fill("id");
    const grip = page.locator(".im-attrs tbody tr[data-attr]:visible .im-grip").first();
    await expect(grip).toHaveClass(/disabled/);
    await expect(grip).toHaveAttribute("title", /Clear the filter to reorder/);
  });

  test("belongs to the class it was typed for", async ({ page }) => {
    await page.locator("#im-member-filter").fill("total");
    await box(page, "Customer").click();
    await expect(page.locator("#im-member-filter")).toHaveValue("");
    await expect(page.locator(".im-attrs tbody tr[data-attr]:visible")).toHaveCount(2);
  });
});

// The canvas binds the keyboard — arrow keys nudge the selection — and the search
// field sits above it. Typing a class's name into the field must not also drive the
// drawing underneath.
test("typing in the search does not reach the canvas's keyboard", async ({ page }) => {
  const order = box(page, "Order");
  await order.click();
  const before = await order.boundingBox();

  await search(page).click();
  await search(page).pressSequentially("Order");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowDown");

  const after = await order.boundingBox();
  expect(Math.abs(after.x - before.x)).toBeLessThan(0.5);
  expect(Math.abs(after.y - before.y)).toBeLessThan(0.5);
  expect(page.__errors).toEqual([]);
});

// Undo and redo were the other half of ADR-0237's promise, reachable from nowhere:
// the canvas has kept a command stack since the port, and nothing asked it for
// anything. What they undo is what the canvas does — moving something — which is why
// the control says "the last move" and not "the last change".
test.describe("undo and redo on the canvas", () => {
  const undo = (page) => page.locator('#im-canvas [data-tool="undo"]');
  const redo = (page) => page.locator('#im-canvas [data-tool="redo"]');

  // A drag on the sheet, through diagram-js's own hit layer.
  async function dragOrder(page, dx, dy) {
    const target = box(page, "Order");
    const at = await target.boundingBox();
    await page.mouse.move(at.x + at.width / 2, at.y + 10);
    await page.mouse.down();
    await page.mouse.move(at.x + at.width / 2 + dx, at.y + 10 + dy, { steps: 8 });
    await page.mouse.up();
  }

  test("a mis-drag is undone, and the undo is redone", async ({ page }) => {
    // Nothing has happened yet, so there is nothing to undo.
    await expect(undo(page)).toBeDisabled();
    await expect(redo(page)).toBeDisabled();

    const before = await box(page, "Order").boundingBox();
    await dragOrder(page, 90, 60);
    const moved = await box(page, "Order").boundingBox();
    expect(moved.x).toBeGreaterThan(before.x + 40);
    await expect(undo(page)).toBeEnabled();

    await undo(page).click();
    const back = await box(page, "Order").boundingBox();
    expect(Math.abs(back.x - before.x)).toBeLessThan(2);
    await expect(redo(page)).toBeEnabled();

    await redo(page).click();
    expect((await box(page, "Order").boundingBox()).x).toBeGreaterThan(before.x + 40);
    expect(page.__errors).toEqual([]);
  });

  // The undone position has to reach the document, or Save would write the geometry
  // the reader just took back.
  test("what was undone is what gets saved", async ({ page }) => {
    const before = await box(page, "Order").boundingBox();
    await dragOrder(page, 100, 0);
    await undo(page).click();
    await page.locator("#im-save").click();
    await expect.poll(() => page.evaluate(() => window.__saved !== null)).toBe(true);

    const saved = await page.evaluate(() => window.__saved.classes.find((c) => c.name === "Order"));
    expect(saved.x).toBe(340); // the model's own position, not the dragged one
    expect(before).toBeTruthy();
    expect(page.__errors).toEqual([]);
  });

  test("Ctrl+Z undoes on the sheet, and leaves a field being typed in alone", async ({ page }) => {
    await dragOrder(page, 90, 0);
    const moved = (await box(page, "Order").boundingBox()).x;
    await page.keyboard.press("Control+z");
    expect((await box(page, "Order").boundingBox()).x).toBeLessThan(moved - 40);

    // With the caret in a field, Ctrl+Z belongs to that field: the box stays put.
    // Clicked on its header rather than its middle, where a relationship's hit line
    // crosses the box and would take the click.
    await box(page, "Order").click({ position: { x: 30, y: 12 } });
    await dragOrder(page, 90, 0);
    const again = (await box(page, "Order").boundingBox()).x;
    await page.locator("#im-c-name").click();
    await page.keyboard.press("Control+z");
    expect((await box(page, "Order").boundingBox()).x).toBeCloseTo(again, 0);
    expect(page.__errors).toEqual([]);
  });
});

// A class with a hundred attributes needs room the fixed panel does not have, and the
// name is the column that loses when there is none: it is the one a member is found
// by, and the two selects beside it carry class names, so left to size themselves they
// take the width and leave the name a stub.
test.describe("room for a long member list", () => {
  const resizer = (page) => page.locator("#im-resizer");
  const panelWidth = (page) => page.locator("#im-side").evaluate((el) => el.getBoundingClientRect().width);
  const nameWidth = (page) => page.locator(".im-attrs tbody tr[data-attr] input[data-f='name']").first()
    .evaluate((el) => el.getBoundingClientRect().width);

  const widen = async (page, by) => {
    const at = await resizer(page).boundingBox();
    await page.mouse.move(at.x + at.width / 2, at.y + at.height / 2);
    await page.mouse.down();
    await page.mouse.move(at.x - by, at.y + at.height / 2, { steps: 10 });
    await page.mouse.up();
  };

  test.beforeEach(async ({ page }) => {
    await page.evaluate(() => localStorage.removeItem("atlas.imPanelWidth"));
    await box(page, "Order").click({ position: { x: 30, y: 12 } });
    await expect(page.locator(".im-attrs")).toBeVisible();
  });

  test("the divider widens the panel, and the room goes to the name", async ({ page }) => {
    const before = { panel: await panelWidth(page), name: await nameWidth(page) };
    await widen(page, 200);
    expect(await panelWidth(page)).toBeGreaterThan(before.panel + 150);
    expect(await nameWidth(page)).toBeGreaterThan(before.name + 140);
    expect(page.__errors).toEqual([]);
  });

  test("the width is remembered, and a double-click puts it back", async ({ page }) => {
    await widen(page, 160);
    const widened = await panelWidth(page);
    expect(await page.evaluate(() => Number(localStorage.getItem("atlas.imPanelWidth")))).toBeGreaterThan(400);

    await page.evaluate(() => window.__mount());
    await expect(page.locator(".uml-class").first()).toBeVisible();
    expect(await panelWidth(page)).toBeCloseTo(widened, 0);

    await resizer(page).dblclick();
    expect(await panelWidth(page)).toBeCloseTo(340, 0);
  });

  test("a name too long for its column is readable on hover, while it is typed", async ({ page }) => {
    const first = page.locator(".im-attrs tbody tr[data-attr] input[data-f='name']").first();
    await expect(first).toHaveAttribute("title", "id");
    // The row is not repainted while it is typed in — that is what keeps the caret in
    // the field — so the tooltip has to be kept current by hand.
    await first.fill("allowedAttributesForThisParticularCase");
    await expect(first).toHaveAttribute("title", "allowedAttributesForThisParticularCase");
    // And the filter matches what the row says now, not what it said when it was drawn.
    await page.locator("#im-member-filter").fill("ParticularCase");
    await expect(page.locator(".im-attrs tbody tr[data-attr]:visible")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });

  test("the sheet keeps working after the panel takes its room", async ({ page }) => {
    await widen(page, 220);
    // The classes are still there to be clicked, and clicking one still selects it.
    await box(page, "Customer").click({ position: { x: 30, y: 12 } });
    await expect(page.locator(".psec input#im-c-name")).toHaveValue("Customer");
    expect(page.__errors).toEqual([]);
  });
});

// Taking hold of several classes at once. The gesture is diagram-js's lasso, and what
// it needed was a way in: a plain drag on empty sheet pans — it has to, or a diagram
// larger than its window could not be moved — so drawing a box is a mode, armed from
// the control group or by holding Shift, as it is in the process modeler beside it.
//
// The part these tests exist for is what happens *after* the box: the canvas reports
// the whole selection and the panel keeps it. Told only about the first of it, the
// editor would put that one back as the selection and the other three would be gone
// before anything could be done with them — a marquee that looks like it worked.
test.describe("selecting several at once", () => {
  const marquee = (page) => page.locator('#im-canvas [data-tool="marquee"]');

  // A box drawn round the lower row — OrderStatus and Address — because those two
  // have empty sheet on every side of them: the box has to *enclose* what it takes,
  // and the upper row sits against the top edge of the window with nowhere to start.
  // Neither the relationship (its waypoints are in the upper row) nor the data store
  // below is inside it.
  async function boxLowerRow(page, { shift = false } = {}) {
    const left = await box(page, "OrderStatus").boundingBox();
    const right = await box(page, "Address").boundingBox();
    const x1 = Math.min(left.x, right.x) - 14;
    const y1 = Math.min(left.y, right.y) - 14;
    const x2 = Math.max(left.x + left.width, right.x + right.width) + 14;
    const y2 = Math.max(left.y + left.height, right.y + right.height) + 14;
    await page.mouse.move(x1, y1);
    if (shift) await page.keyboard.down("Shift");
    await page.mouse.down();
    await page.mouse.move(x2, y2, { steps: 10 });
    await page.mouse.up();
    if (shift) await page.keyboard.up("Shift");
  }

  test("a box takes hold of what is inside it, and the panel says what it holds", async ({ page }) => {
    await expect(marquee(page)).toHaveAttribute("aria-pressed", "false");
    await marquee(page).click();
    await expect(marquee(page)).toHaveAttribute("aria-pressed", "true");

    await boxLowerRow(page);

    // Both, and only both. Without the whole selection reaching the panel this is 1:
    // the editor puts the first element back as the selection and drops the rest.
    await expect(page.locator(".djs-element.selected")).toHaveCount(2);
    await expect(box(page, "OrderStatus")).toHaveClass(/selected/);
    await expect(box(page, "Address")).toHaveClass(/selected/);
    await expect(page.locator(".phead b")).toHaveText("2 elements");
    await expect(page.locator(".im-many-row")).toHaveCount(2);

    // The mode is spent with the box. A button still lit would promise a gesture that
    // is back to panning.
    await expect(marquee(page)).toHaveAttribute("aria-pressed", "false");
    expect(page.__errors).toEqual([]);
  });

  test("Shift and drag draws the box without arming anything", async ({ page }) => {
    await boxLowerRow(page, { shift: true });
    await expect(page.locator(".djs-element.selected")).toHaveCount(2);
    await expect(page.locator(".phead b")).toHaveText("2 elements");
    expect(page.__errors).toEqual([]);
  });

  test("dragging one of them moves them all, and the document keeps both", async ({ page }) => {
    await marquee(page).click();
    await boxLowerRow(page);

    const before = {
      status: await box(page, "OrderStatus").boundingBox(),
      address: await box(page, "Address").boundingBox(),
      order: await box(page, "Order").boundingBox(),
    };
    // Dragged by its header: a class's middle is where a relationship's hit line
    // crosses it, and that line would take the press.
    await page.mouse.move(before.status.x + 30, before.status.y + 12);
    await page.mouse.down();
    await page.mouse.move(before.status.x + 90, before.status.y + 52, { steps: 8 });
    await page.mouse.up();

    expect((await box(page, "OrderStatus").boundingBox()).x).toBeGreaterThan(before.status.x + 30);
    expect((await box(page, "Address").boundingBox()).x).toBeGreaterThan(before.address.x + 30);
    // What was not in the box stays where it was.
    expect(Math.abs((await box(page, "Order").boundingBox()).x - before.order.x)).toBeLessThan(2);

    await page.locator("#im-save").click();
    await expect.poll(() => page.evaluate(() => window.__saved !== null)).toBe(true);
    const saved = await page.evaluate(() => window.__saved.classes);
    expect(saved.find((c) => c.name === "OrderStatus").x).toBeGreaterThan(40);
    expect(saved.find((c) => c.name === "Address").x).toBeGreaterThan(340);
    expect(saved.find((c) => c.name === "Order").x).toBe(340);
    expect(page.__errors).toEqual([]);
  });

  // Undoing a move of several is where reading only what *differs* from the opened
  // document goes wrong: the undo puts every box back, so nothing differs — and the
  // document quietly keeps the positions that were just taken away.
  test("moving several and undoing it leaves the document where it started", async ({ page }) => {
    await marquee(page).click();
    await boxLowerRow(page);
    const at = await box(page, "OrderStatus").boundingBox();
    await page.mouse.move(at.x + 30, at.y + 12);
    await page.mouse.down();
    await page.mouse.move(at.x + 90, at.y + 52, { steps: 8 });
    await page.mouse.up();

    await page.locator('#im-canvas [data-tool="undo"]').click();
    await page.locator("#im-save").click();
    await expect.poll(() => page.evaluate(() => window.__saved !== null)).toBe(true);
    const saved = await page.evaluate(() => window.__saved.classes);
    expect(saved.find((c) => c.name === "OrderStatus").x).toBe(40);
    expect(saved.find((c) => c.name === "Address").x).toBe(340);
    expect(page.__errors).toEqual([]);
  });

  test("a line in the list goes back to editing that one on its own", async ({ page }) => {
    await marquee(page).click();
    await boxLowerRow(page);
    await page.locator(".im-many-row", { hasText: "Address" }).click();

    await expect(page.locator("#im-c-name")).toHaveValue("Address");
    await expect(page.locator(".djs-element.selected")).toHaveCount(1);
    await expect(box(page, "Address")).toHaveClass(/selected/);
    expect(page.__errors).toEqual([]);
  });

  test("Escape gives the drag back to panning", async ({ page }) => {
    await marquee(page).click();
    await expect(marquee(page)).toHaveAttribute("aria-pressed", "true");
    await page.keyboard.press("Escape");
    await expect(marquee(page)).toHaveAttribute("aria-pressed", "false");

    // The same gesture now moves the sheet, and takes hold of nothing.
    const before = await box(page, "OrderStatus").boundingBox();
    await boxLowerRow(page);
    await expect(page.locator(".djs-element.selected")).toHaveCount(0);
    expect((await box(page, "OrderStatus").boundingBox()).x).toBeGreaterThan(before.x + 20);
    expect(page.__errors).toEqual([]);
  });
});
