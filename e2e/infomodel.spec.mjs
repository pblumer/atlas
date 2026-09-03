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
  await expect(page.locator(".im-class").first()).toBeVisible();
});

// Addressed by the name on the group, not by matching label text: "Order" is a
// prefix of "OrderStatus", and a locator that cannot tell them apart is a locator
// that will silently pass on the wrong box.
const box = (page, name) => page.locator(`.im-class[data-name="${name}"]`);

test("a class reads as UML: its kind, its members, and which of them identify it", async ({ page }) => {
  await expect(page.locator(".im-class")).toHaveCount(4);

  const order = box(page, "Order");
  await expect(order.locator(".im-stereo")).toHaveText("«businessObject»");
  // The business key is marked on the box, because it is the fact the whole model
  // turns on — what makes Order#ORD-1 the same order in two processes.
  await expect(order.locator(".im-attr.key .im-attr-name")).toHaveText("⚿ id");
  // A non-default multiplicity is shown; "exactly one" is the unstated default.
  await expect(order.locator(".im-attr-mult")).toHaveText(" [0..1]");

  // An enumeration carries literals where the others carry attributes.
  const status = box(page, "OrderStatus");
  await expect(status.locator(".im-stereo")).toHaveText("«enumeration»");
  await expect(status.locator(".im-literal")).toHaveCount(2);
  await expect(status.locator(".im-attr")).toHaveCount(0);
});

test("the canvas refuses what the server would refuse, in the server's words", async ({ page }) => {
  // A value type cannot be the whole that owns parts, so Address → Customer as a
  // composition must be refused while it is being drawn.
  await page.locator('.im-connect[data-kind="composition"]').click();
  await box(page, "Address").click();
  // Every class the matrix rules out fades, so the canvas offers only what is legal.
  await expect(box(page, "Customer")).toHaveClass(/unreachable/);
  await box(page, "Customer").click();

  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts.length).toBe(1);
  expect(toasts[0].kind).toBe("err");
  // The refusal teaches the notation rather than only saying no.
  expect(toasts[0].msg).toContain("no existence of its own");
  // And nothing was drawn.
  await expect(page.locator(".im-edge")).toHaveCount(1);
});

test("an enumeration cannot be related to at all", async ({ page }) => {
  await page.locator('.im-connect[data-kind="association"]').click();
  await box(page, "Order").click();
  await expect(box(page, "OrderStatus")).toHaveClass(/unreachable/);
  await box(page, "OrderStatus").click();
  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts[0].msg).toContain("closed set of values");
});

test("a legal relationship is drawn, and the panel states how to read it", async ({ page }) => {
  await page.locator('.im-connect[data-kind="composition"]').click();
  await box(page, "Order").click();
  await box(page, "Address").click();

  await expect(page.locator(".im-edge")).toHaveCount(2);
  // Selecting it lands on the relationship panel, which names both ends.
  await expect(page.locator(".im-reading")).toHaveText("Order → Address");
  // A composition is marked at the whole — the end the ownership belongs to.
  const drawn = page.locator(".im-line.composition");
  await expect(drawn).toHaveAttribute("marker-start", "url(#im-diamond-filled)");
  await expect(page.locator("#im-save")).toBeEnabled();
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
  await expect(box(page, "Order").locator(".im-attr.key")).toHaveCount(0);
});

// The order of a class's attributes is not a view setting: a class box reads top to
// bottom, so which attribute comes first is a statement about the class. A business
// key belongs where a reader looks for it. `attributes` is already an ordered array
// in the document, so moving a row is a model edit — it dirties the model, redraws
// the box, and is what gets saved.
test("an attribute can be moved, and the box and the document follow", async ({ page }) => {
  const order = box(page, "Order");
  const names = () => order.locator(".im-attr-name");
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
  await expect(order.locator(".im-attr.key .im-attr-name")).toHaveText("⚿ id");

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
  await expect(status.locator(".im-literal")).toHaveText(["draft", "approved"]);

  await status.click();
  await page.locator('tr[data-lit="1"] [data-f="literal"]').focus();
  await page.keyboard.press("Alt+ArrowUp");
  await expect(status.locator(".im-literal")).toHaveText(["approved", "draft"]);
});

test("a move past either end is refused rather than wrapping", async ({ page }) => {
  const order = box(page, "Order");
  await order.click();
  await page.locator('tr[data-attr="0"] [data-f="name"]').focus();
  await page.keyboard.press("Alt+ArrowUp"); // already first
  await expect(order.locator(".im-attr-name")).toHaveText(["⚿ id", "placedOn", "total"]);
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
  await expect(page.locator(".im-panel h3")).toHaveText("Class");
});

test("the JSON Schema projection is shown as derived, and says what it dropped", async ({ page }) => {
  await box(page, "Order").click();
  await page.locator('[data-act="schema"]').click();
  await expect(page.locator(".im-schema")).toContainText("json-schema.org/draft/2020-12");
  await expect(page.locator(".im-loss")).toContainText("JSON Schema has no keyword for identity");
  await expect(page.locator(".im-panel .im-hint-text")).toContainText("Derived, never edited");
  await page.locator('[data-act="close-schema"]').click();
  await expect(page.locator(".im-schema")).toHaveCount(0);
});

test("the panel states that this is a subset, and what it does not author", async ({ page }) => {
  await page.locator(".im-svg").click({ position: { x: 700, y: 470 } });
  await expect(page.locator(".im-note")).toContainText("This is a subset of UML");
  await expect(page.locator(".im-note li")).toHaveCount(3);
  await expect(page.locator(".im-note")).toContainText("Where a datum lives is the data store's question");
});

// Data stores on the class canvas (ADR-0230, slice 5b).
// A store is where instances of a class outlive the process that made them — the
// thing BPMN's <dataStoreReference> gestures at and then says nothing about. It is
// declared once per application here, and named by every process that reaches it.
test.describe("data stores", () => {
  const store = (page) => page.locator('.im-store[data-name="Orders"]');

  test("a store is drawn as a store, and says what it holds", async ({ page }) => {
    await expect(store(page)).toBeVisible();
    // Not a class box: the one mistake to prevent is reading it as one. A class says
    // what an Order is; a store says where Orders are kept.
    await expect(store(page).locator(".im-store-body")).toHaveCount(1);
    await expect(store(page).locator(".im-store-name")).toHaveText("Orders");
    await expect(store(page).locator(".im-store-sub")).toHaveText("«read» Order");
    // The line to the class it keeps is an annotation, not an association.
    await expect(page.locator(".im-store-line")).toHaveCount(1);
  });

  test("the panel offers only classes a store can keep", async ({ page }) => {
    await store(page).click();
    await expect(page.locator(".im-panel h3")).toHaveText("Data store");
    // Only a business object with a business key: a process reads from a store by
    // naming which thing it wants, and the key is the only thing that names one.
    // Customer and Order have keys; Address is a value type and OrderStatus an
    // enumeration, so neither is offered.
    const options = await page.locator("#im-s-class option").evaluateAll((els) => els.map((e) => e.value));
    expect(options).toEqual(["", "Customer", "Order"]);
    await expect(page.locator("#im-s-worker")).toHaveValue("clio-main");
    await expect(page.locator(".im-panel")).toContainText("business object with a business key");
  });

  test("adding a store puts it on the canvas and selects it", async ({ page }) => {
    await page.locator('[data-add="store"]').click();
    await expect(page.locator(".im-store")).toHaveCount(2);
    await expect(page.locator(".im-panel h3")).toHaveText("Data store");
    await page.locator("#im-s-name").fill("Invoices");
    await expect(page.locator('.im-store[data-name="Invoices"]')).toBeVisible();
    // A store with no class yet says so rather than claiming to hold something.
    await expect(page.locator('.im-store[data-name="Invoices"] .im-store-sub')).toHaveText("holds nothing yet");

    await page.locator("#im-save").click();
    const saved = await page.evaluate(() => window.__saved);
    const added = saved.stores.find((s) => s.name === "Invoices");
    expect(added).toBeTruthy();
    // Ids are the server's to issue, here as everywhere else.
    expect(added.id.startsWith("new-")).toBe(true);
    expect(page.__errors).toEqual([]);
  });

  test("the subset states that writing through a store is not authored", async ({ page }) => {
    await page.locator(".im-svg").click({ position: { x: 700, y: 500 } });
    await expect(page.locator(".im-note")).toContainText("Writing through a data store");
  });
});
