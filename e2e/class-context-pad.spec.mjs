// The context pad on the class canvas
// (ADR-draft-draw-a-relationship-from-the-class-it-starts-at).
//
// Drawing a relationship used to be a *mode*: arm a kind in the palette, then remember
// which class to click first and which second. The pad puts the kinds beside the class
// they would start at, so the gesture is the one the BPMN modeler already has — drag out of
// the thing, drop on the thing at the other end.
//
// What these tests are most careful about is that the pad never creates anything on the
// canvas. The document belongs to the editor and reconcile rebuilds every line from it,
// so a connection diagram-js added to its own model would live exactly until the next
// keystroke.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/infomodel-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-class").first()).toBeVisible();
});

const box = (page, name) => page.locator(`.djs-element:has(.uml-class[data-name="${name}"])`);
const pad = (page) => page.locator(".djs-context-pad.open");
const entries = (page) => page.locator(".djs-context-pad .entry");

const openPad = async (page, name) => {
  await box(page, name).click();
  await expect(pad(page)).toBeVisible();
};

// A real drag fires the HTML5 dragstart the pad's entries are marked for. Playwright's
// synthetic mouse does not, so the event is dispatched and the pointer then moved —
// which is the same sequence a browser produces, minus the native drag image.
const dragTo = async (page, kind, targetName) => {
  const entry = page.locator(`.djs-context-pad .entry.uml-pi-${kind}`);
  const eb = await entry.boundingBox();
  await page.mouse.move(eb.x + eb.width / 2, eb.y + eb.height / 2);
  await entry.dispatchEvent("dragstart", { clientX: eb.x + eb.width / 2, clientY: eb.y + eb.height / 2 });
  const tb = await box(page, targetName).boundingBox();
  await page.mouse.move(tb.x + tb.width / 2, tb.y + tb.height / 2, { steps: 10 });
  await page.waitForTimeout(80);
  await page.mouse.up();
};

test("the pad offers the kinds this class could actually reach something with", async ({ page }) => {
  // A business object relates every way the subset has.
  await openPad(page, "Order");
  await expect(entries(page)).toHaveClass([
    /uml-pi-association/, /uml-pi-aggregation/, /uml-pi-composition/, /uml-pi-generalization/,
    /uml-pi-trash/,
  ]);

  // A value type has no existence of its own, so it can be associated and specialized
  // and can own nothing. Offering the other two would be two buttons whose only
  // possible outcome is the refusal.
  await openPad(page, "Address");
  await expect(entries(page)).toHaveClass([
    /uml-pi-association/, /uml-pi-generalization/, /uml-pi-trash/,
  ]);

  // And an enumeration relates to nothing at all, so the pad is the bin alone.
  await openPad(page, "OrderStatus");
  await expect(entries(page)).toHaveClass([/uml-pi-trash/]);
  expect(page.__errors).toEqual([]);
});

test("a relationship is drawn by dragging out of the class it starts at", async ({ page }) => {
  await expect(page.locator(".uml-edge")).toHaveCount(1);
  await openPad(page, "Order");
  await dragTo(page, "composition", "Address");

  await expect(page.locator(".uml-edge")).toHaveCount(2);
  // Marked at the whole, the end the ownership belongs to — the same line the armed
  // palette draws, because both ways end in the same place.
  await expect(page.locator(".uml-edge.composition .uml-edge-line"))
    .toHaveAttribute("marker-start", "url(#uml-diamond-solid)");
  // And it lands on the relationship panel, so the roles can be filled in at once.
  await expect(page.locator(".im-reading")).toHaveText("Order → Address");
  await expect(page.locator("#im-save")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("the drawn relationship belongs to the document, not to the canvas", async ({ page }) => {
  // The failure this rules out: diagram-js adding the connection to its own model,
  // where it lives until reconcile rebuilds the lines from the editor's document and
  // silently drops it. Typing in the panel is what forces that rebuild.
  await openPad(page, "Order");
  await dragTo(page, "association", "Address");
  await page.locator("#im-a-name").fill("ships to");
  await expect(page.locator(".uml-edge-label")).toHaveText(["places", "ships to"]);

  await page.locator("#im-save").click();
  const saved = await page.evaluate(() => window.__saved);
  expect(saved.associations).toHaveLength(2);
  expect(saved.associations[1]).toMatchObject({ kind: "association", name: "ships to" });
  expect(page.__errors).toEqual([]);
});

test("a target the subset refuses never lights, and says why when it is dropped on", async ({ page }) => {
  await openPad(page, "Order");
  const entry = page.locator(".djs-context-pad .entry.uml-pi-association");
  const eb = await entry.boundingBox();
  await page.mouse.move(eb.x + eb.width / 2, eb.y + eb.height / 2);
  await entry.dispatchEvent("dragstart", { clientX: eb.x + eb.width / 2, clientY: eb.y + eb.height / 2 });
  const tb = await box(page, "OrderStatus").boundingBox();
  await page.mouse.move(tb.x + tb.width / 2, tb.y + tb.height / 2, { steps: 10 });

  // The pointer says no while it is still over the class — the rule is asked on hover,
  // not on the drop, which is what makes the gesture readable.
  await expect(page.locator(".connect-not-ok")).toHaveCount(1);

  await page.mouse.up();
  await expect(page.locator(".uml-edge")).toHaveCount(1);
  // A red cursor is not a reason. bpmn-js drops such a gesture in silence; this canvas
  // has taught the notation at exactly this moment since the palette did the drawing.
  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts[0].msg).toContain("closed set of values");
  expect(page.__errors).toEqual([]);
});

test("the bin deletes what is selected, and asks first", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  await openPad(page, "Customer");
  // Customer is one end of the fixture's only relationship, so deleting it must take
  // that line with it — the same rule the panel's ✕ keeps, because they are one path.
  await page.locator(".djs-context-pad .entry.uml-pi-trash").click();
  await expect(page.locator(".uml-class")).toHaveCount(3);
  await expect(page.locator(".uml-edge")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a kind the pad does not offer cannot be drawn from it either", async ({ page }) => {
  // The pad is a shorter list, not a different rule. A value type owning parts is
  // refused by the served matrix, and the matrix is what the pad reads.
  await openPad(page, "Address");
  await expect(page.locator(".djs-context-pad .entry.uml-pi-composition")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the kind being dragged is what the rule is asked about, not merely whether two may relate", async ({ page }) => {
  // Address may be *associated* with Customer and may not be a kind of one: the matrix
  // says `valueType>businessObject: [association]`. A rule that only asked whether the
  // two may relate at all would light Customer green for a generalization, because the
  // list is not empty — and the refusal would arrive on the drop instead of under the
  // pointer, which is the whole difference between a gesture that reads and one that
  // guesses.
  await openPad(page, "Address");
  const entry = page.locator(".djs-context-pad .entry.uml-pi-generalization");
  const eb = await entry.boundingBox();
  await page.mouse.move(eb.x + eb.width / 2, eb.y + eb.height / 2);
  await entry.dispatchEvent("dragstart", { clientX: eb.x + eb.width / 2, clientY: eb.y + eb.height / 2 });
  const tb = await box(page, "Customer").boundingBox();
  await page.mouse.move(tb.x + tb.width / 2, tb.y + tb.height / 2, { steps: 10 });

  await expect(page.locator(".connect-not-ok")).toHaveCount(1);
  await page.mouse.up();
  await expect(page.locator(".uml-edge")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a derived line has no pad, because there is nothing to do to one", async ({ page }) => {
  // The store's line to the class it holds exists because the store names that class.
  await page.locator(".djs-element:has(.uml-store-link)").click({ force: true });
  await expect(pad(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
