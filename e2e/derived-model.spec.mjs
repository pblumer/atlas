// The "as built" reading: what an application's processes imply about its data, drawn
// without anyone having modelled anything
// (ADR-draft-derive-the-model-from-the-processes, §3).
//
// The half these tests care most about is not the drawing — it is what the drawing
// admits it cannot see. A derived picture mistaken for a complete one is worse than no
// picture, and the business key is the fact derivation can never produce.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/derived-model-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const mounted = async (page) => {
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-class").first()).toBeVisible();
};

test("the classes the processes carry are drawn, with no model behind them", async ({ page }) => {
  await mounted(page);
  await expect(page.locator(".uml-class")).toHaveCount(3);
  for (const name of ["Order", "identitaet", "Notiz"]) {
    await expect(page.locator(`.uml-class[data-name="${name}"]`)).toHaveCount(1);
  }
  expect(page.__errors).toEqual([]);
});

test("what the reading cannot see is said above the drawing, not under it", async ({ page }) => {
  await mounted(page);
  const gaps = page.locator(".dm-gaps");
  await expect(gaps).toBeVisible();
  // The one that matters: nothing in BPMN says which attribute identifies a thing, and
  // it is the fact every cross-process capability rests on.
  await expect(gaps).toContainText("business key");
  await expect(gaps).toContainText("first thing to add by hand");
  await expect(gaps).toContainText("untyped");

  // Above the drawing: a reader must meet the qualification before forming a view.
  const gapsBox = await gaps.boundingBox();
  const stageBox = await page.locator(".dm-stage").first().boundingBox();
  expect(gapsBox.y).toBeLessThan(stageBox.y);
});

test("no derived class carries a business key on the drawing", async ({ page }) => {
  await mounted(page);
  // The class canvas marks a key attribute with ⚿. A derived class has none, and that
  // absence has to be real rather than merely undrawn.
  await expect(page.locator(".uml-class .key")).toHaveCount(0);
});

test("a class named after its data object says so, and only that one does", async ({ page }) => {
  await mounted(page);
  await page.locator('.djs-element:has(.uml-class[data-name="identitaet"])').click();
  await expect(page.locator(".dm-card-head")).toContainText("identitaet");
  await expect(page.locator(".dm-gaps-inline")).toContainText("no itemSubjectRef declared a type");

  // Order declares its type, so it carries no such note — but it does carry the other,
  // about the member a dotted write path proved is structured.
  await page.locator('.djs-element:has(.uml-class[data-name="Order"])').click();
  await expect(page.locator(".dm-card-head")).toContainText("Order");
  await expect(page.locator(".dm-gaps-inline")).not.toContainText("itemSubjectRef");
  await expect(page.locator(".dm-gaps-inline")).toContainText("has members of its own");
  expect(page.__errors).toEqual([]);
});

test("the states a class moves through are drawn beside it", async ({ page }) => {
  await mounted(page);
  await page.locator('.djs-element:has(.uml-class[data-name="Order"])').click();
  await expect(page.locator(".dm-side .uml-state")).toHaveCount(3);
  await expect(page.locator('.dm-side .uml-state[data-name="received"]')).toHaveClass(/initial/);
  await expect(page.locator(".dm-side .uml-transition")).toHaveCount(2);

  // Switching class switches the machine rather than adding to it.
  await page.locator('.djs-element:has(.uml-class[data-name="identitaet"])').click();
  await expect(page.locator(".dm-side .uml-state")).toHaveCount(2);
  await expect(page.locator('.dm-side .uml-state[data-name="ERFASST"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a class no process gives a state says that, rather than drawing an empty machine", async ({ page }) => {
  await mounted(page);
  await page.locator('.djs-element:has(.uml-class[data-name="Notiz"])').click();
  await expect(page.locator(".dm-card-head")).toContainText("Notiz");
  await expect(page.locator(".dm-side .uml-state")).toHaveCount(0);
  await expect(page.locator(".dm-side")).toContainText("no life to draw");
  // And it names the field that would make one, because that is the useful half.
  await expect(page.locator(".dm-side")).toContainText("Data state");
});

test("nothing on the reading is editable", async ({ page }) => {
  await mounted(page);
  // It is evidence about the processes, not a document about the business — so it
  // carries no palette, which is what an editable canvas puts down its left edge.
  await expect(page.locator(".djs-palette")).toHaveCount(0);
});

test("an application whose processes carry no data says so plainly", async ({ page }) => {
  // The case the whole feature exists for, met from the other end: nothing to derive.
  // An empty drawing with the usual qualifications under it would be nonsense — the
  // sentences exist to warn about a picture, and there is no picture.
  await page.evaluate(() => window.__mountEmpty());
  await expect(page.locator("#dm-body")).toContainText("Nothing to read yet");
  await expect(page.locator(".dm-gaps")).toHaveCount(0);
  await expect(page.locator(".uml-class")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
