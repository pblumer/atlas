// Drawing a class's lifecycle in the information model editor (ADR-0259).
//
// The lifecycle is a *mode* of this editor rather than a route of its own: one
// document, one Save, one dirty flag, with the sheet swapped underneath. That is what
// these tests hold — that the way in is the class that owns it, that what is drawn
// lands in the document the Save sends, and that the notation's one hard rule (a
// final state is one nothing leaves) is enforced while drawing rather than at write.
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

const box = (page, name) => page.locator(`.djs-element:has(.uml-class[data-name="${name}"])`);
const openLifecycle = async (page, className) => {
  await box(page, className).click();
  await page.locator('[data-act="open-lifecycle"]').click();
  await expect(page.locator("#im-lc-canvas")).toBeVisible();
};

test("only a business object is offered a lifecycle", async ({ page }) => {
  // A business object has an identity that persists through states.
  await box(page, "Order").click();
  await expect(page.locator('[data-act="open-lifecycle"]')).toHaveCount(1);

  // A value type is equal to any other with the same contents — there is no "this
  // one, later" — and an enumeration is a set of values rather than a thing.
  await box(page, "Address").click();
  await expect(page.locator('[data-act="open-lifecycle"]')).toHaveCount(0);
  await box(page, "OrderStatus").click();
  await expect(page.locator('[data-act="open-lifecycle"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a state drawn on the sheet lands in the document, and the first one is the start", async ({ page }) => {
  await openLifecycle(page, "Order");
  await expect(page.locator("#im-lc-empty")).toBeVisible();

  await page.locator('.djs-palette [data-action="state"]').click();
  await expect(page.locator(".uml-state")).toHaveCount(1);
  // The first state is where instances are created. Marked here rather than left to
  // the author, because the server refuses a lifecycle with no initial state — a
  // canvas that let you add one and then refused to save it would be a trap.
  await expect(page.locator(".uml-state-start")).toHaveCount(1);

  await page.locator('.djs-palette [data-action="state"]').click();
  await expect(page.locator(".uml-state")).toHaveCount(2);
  await expect(page.locator(".uml-state-start")).toHaveCount(1);

  // Through Save, because that is what actually reaches the server: the document the
  // request carries is the only evidence the drawing landed anywhere.
  await page.locator("#im-save").click();
  const saved = await page.evaluate(() =>
    window.__saved.classes.find((x) => x.name === "Order").lifecycle);
  expect(saved.states.map((s) => s.name)).toEqual(["new", "new2"]);
  expect(saved.states[0].initial).toBe(true);
  expect(saved.states[1].initial).toBeFalsy();
  expect(page.__errors).toEqual([]);
});

test("renaming a state carries its transitions with it", async ({ page }) => {
  await openLifecycle(page, "Order");
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="state"]').click();

  // Two clicks make a transition, the way two clicks make a relationship.
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();
  await expect(page.locator(".uml-transition-line")).toHaveCount(1);

  // A transition names its ends by state name, because the name is the identity — it
  // is the string a process writes. So a rename has to move them with it, or the
  // document refers to a state that is no longer there.
  await page.locator('.djs-element:has(.uml-state[data-name="new"])').click();
  await page.locator("#im-st-name").fill("erfasst");
  await expect(page.locator('.uml-state[data-name="erfasst"]')).toHaveCount(1);

  await page.locator("#im-save").click();
  const saved = await page.evaluate(() =>
    window.__saved.classes.find((x) => x.name === "Order").lifecycle);
  expect(saved.transitions[0].from).toBe("erfasst");
  expect(saved.states.map((s) => s.name)).toContain("erfasst");
  await expect(page.locator(".uml-transition-line")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("marking a state final drops what left it, and says so on the drawing", async ({ page }) => {
  await openLifecycle(page, "Order");
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new"])').click();
  await expect(page.locator(".uml-transition-line")).toHaveCount(1);

  // "Final" and "leaves" are the two saying opposite things. Rather than saving a
  // document the server would refuse, the transition out of it goes — and the drawing
  // shows exactly what went.
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();
  await page.locator("#im-st-final").check();
  await expect(page.locator(".uml-state-final")).toHaveCount(1);
  await expect(page.locator(".uml-transition-line")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the way back leaves the class diagram as it was", async ({ page }) => {
  await openLifecycle(page, "Order");
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator("#im-lc-back").click();
  await expect(page.locator("#im-canvas")).toBeVisible();
  await expect(page.locator("#im-lc-canvas")).toBeHidden();
  await expect(page.locator(".uml-class")).toHaveCount(4);
  // And the panel offers the way back in, now naming what is there.
  await box(page, "Order").click();
  await expect(page.locator('[data-act="open-lifecycle"]')).toHaveText("Open lifecycle");
  expect(page.__errors).toEqual([]);
});

// Drawing is how a transition is made; it was also the only way to change one. Getting
// an end wrong meant deleting the line and drawing it again, which loses its name.
test("a transition can be re-aimed from the panel, without being redrawn", async ({ page }) => {
  await openLifecycle(page, "Order");
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();
  // force, because a horizontal line has a zero-height bounding box and Playwright
  // measures that rather than the 15px hit stroke a person actually clicks.
  await page.locator(".djs-element:has(.uml-transition-line) .djs-hit").click({ force: true });
  await page.locator("#im-tr-name").fill("approve");

  // The ends are the states the class declares, marked the way the drawing marks them.
  const labels = await page.locator("#im-tr-to option").evaluateAll((els) => els.map((e) => e.textContent.trim()));
  expect(labels).toEqual(["new · start", "new2", "new3"]);

  await page.locator("#im-tr-to").selectOption("new3");
  await expect(page.locator(".uml-transition-line")).toHaveCount(1);
  await page.locator("#im-save").click();
  const saved = await page.evaluate(() =>
    window.__saved.classes.find((x) => x.name === "Order").lifecycle);
  expect(saved.transitions).toHaveLength(1);
  expect(saved.transitions[0]).toMatchObject({ name: "approve", from: "new", to: "new3" });
  expect(page.__errors).toEqual([]);
});

test("a state nothing leaves is not offered as a From, the way the drawing refuses it", async ({ page }) => {
  await openLifecycle(page, "Order");
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="state"]').click();
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();

  // Mark the target final: nothing leaves it, so it cannot become the start of this
  // transition either. It is disabled rather than hidden — the reason it cannot be
  // picked is the point, and hiding it would say it does not exist.
  await page.locator('.djs-element:has(.uml-state[data-name="new2"])').click();
  await page.locator("#im-st-final").check();
  // force, because a horizontal line has a zero-height bounding box and Playwright
  // measures that rather than the 15px hit stroke a person actually clicks.
  await page.locator(".djs-element:has(.uml-transition-line) .djs-hit").click({ force: true });
  await expect(page.locator("#im-tr-from option[disabled]")).toHaveText("new2 · final");
  await expect(page.locator("#im-tr-to option[disabled]")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
