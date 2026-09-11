// A lifecycle whose states are an «enumeration»'s literals
// (ADR-draft-a-lifecycle-may-take-its-states-from-an-enumeration).
//
// The model that prompted this had its states written twice: as literals somebody
// maintained, with documentation, and as states a deploy resolved against. What these
// tests hold is that there is now one place — that a rename in the enumeration is a
// rename of the state and of every transition naming it, that a literal removed takes
// its state and arrows with it, and that the class diagram says which enumeration is
// which class's life rather than leaving it to a convention in two people's heads.
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

// The fixture's OrderStatus declares draft and approved, and Order is the one class in
// it with an identity to have a life.
const sourceOrderFrom = async (page, enumName = "OrderStatus") => {
  await box(page, "Order").click();
  await page.locator("#im-c-lcfrom").selectOption(enumName);
};

const lifecycleOf = (page, className) => page.evaluate((n) =>
  window.__saved.classes.find((x) => x.name === n).lifecycle, className);

const save = async (page) => {
  await page.locator("#im-save").click();
  await expect.poll(() => page.evaluate(() => !!window.__saved)).toBe(true);
};

test("choosing an enumeration makes its literals the states", async ({ page }) => {
  await sourceOrderFrom(page);
  await save(page);
  const lc = await lifecycleOf(page, "Order");
  expect(lc.statesFrom).toBe("OrderStatus");
  expect(lc.states.map((s) => s.name)).toEqual(["draft", "approved"]);
  // A machine with no start is one the server refuses, so the first literal stands in
  // — and it is one click to move it.
  expect(lc.states.filter((s) => s.initial).map((s) => s.name)).toEqual(["draft"]);
  expect(page.__errors).toEqual([]);
});

test("the class diagram draws the tie, and it is not a relationship", async ({ page }) => {
  await expect(page.locator(".uml-lifecycle-link")).toHaveCount(0);
  await sourceOrderFrom(page);

  const link = page.locator(".uml-lifecycle-link");
  await expect(link).toHaveCount(1);
  // The keyword is what says which dependency this is: UML allows many and draws them
  // all as one dashed line.
  await expect(link.locator(".uml-edge-label")).toHaveText("«lifecycle»");

  // A class and its enumeration do not relate, one takes its states from the other —
  // so nothing that counts relationships counts this.
  await save(page);
  const saved = await page.evaluate(() => window.__saved);
  expect(saved.associations).toHaveLength(1);
  expect(page.__errors).toEqual([]);
});

test("renaming a literal renames the state and every transition that names it", async ({ page }) => {
  await sourceOrderFrom(page);
  // Draw the one transition the enumeration cannot hold: draft → approved.
  await page.locator('[data-act="open-lifecycle"]').click();
  await expect(page.locator(".uml-state")).toHaveCount(2);
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="draft"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="approved"])').click();
  await expect(page.locator(".uml-transition")).toHaveCount(1);

  await page.locator("#im-lc-back").click();
  await box(page, "OrderStatus").click();
  await page.locator('[data-lit="1"] [data-f="literal"]').fill("freigegeben");
  await page.locator('[data-lit="1"] [data-f="literal"]').blur();

  await save(page);
  const lc = await lifecycleOf(page, "Order");
  expect(lc.states.map((s) => s.name)).toEqual(["draft", "freigegeben"]);
  // The transition follows, because a state's name *is* what it is named by — an arrow
  // left pointing at the old string is what the server refuses.
  expect(lc.transitions).toHaveLength(1);
  expect(lc.transitions[0].to).toBe("freigegeben");
  expect(page.__errors).toEqual([]);
});

test("removing a literal takes its state and the arrows touching it", async ({ page }) => {
  await sourceOrderFrom(page);
  await page.locator('[data-act="open-lifecycle"]').click();
  await page.locator('.djs-palette [data-action="transition"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="draft"])').click();
  await page.locator('.djs-element:has(.uml-state[data-name="approved"])').click();
  await expect(page.locator(".uml-transition")).toHaveCount(1);

  await page.locator("#im-lc-back").click();
  await box(page, "OrderStatus").click();
  await page.locator('[data-lit="1"] [data-act="del-literal"]').click();

  await save(page);
  const lc = await lifecycleOf(page, "Order");
  expect(lc.states.map((s) => s.name)).toEqual(["draft"]);
  expect(lc.transitions).toHaveLength(0);
  expect(page.__errors).toEqual([]);
});

test("a state added on the sheet writes the literal, because that is where names live", async ({ page }) => {
  await sourceOrderFrom(page);
  await page.locator('[data-act="open-lifecycle"]').click();
  await expect(page.locator(".uml-state")).toHaveCount(2);

  await page.locator('.djs-palette [data-action="state"]').click();
  await expect(page.locator(".uml-state")).toHaveCount(3);

  await save(page);
  const saved = await page.evaluate(() => window.__saved);
  // Adding a state that the enumeration does not declare is what the server refuses,
  // so the gesture writes the literal instead of drawing something unsaveable.
  expect(saved.classes.find((c) => c.name === "OrderStatus").literals).toEqual(
    ["draft", "approved", "new"]);
  expect(saved.classes.find((c) => c.name === "Order").lifecycle.states.map((s) => s.name))
    .toEqual(["draft", "approved", "new"]);
  expect(page.__errors).toEqual([]);
});

test("on a sourced lifecycle the name is read where it is written, not here", async ({ page }) => {
  await sourceOrderFrom(page);
  await page.locator('[data-act="open-lifecycle"]').click();
  await page.locator('.djs-element:has(.uml-state[data-name="draft"])').click();

  // Read-only rather than hidden or silently ignored: a field that takes a rename and
  // drops it is the worse surprise, and the remedy is one sentence.
  await expect(page.locator("#im-st-name")).toHaveAttribute("readonly", "");
  await expect(page.locator(".im-hint-text").first()).toContainText("OrderStatus");
  // Nor may it be deleted from here: it would come straight back on the next sync.
  await expect(page.locator('[data-act="del-state"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("unhooking keeps the states that were there", async ({ page }) => {
  await sourceOrderFrom(page);
  await page.locator("#im-c-lcfrom").selectOption("");

  await expect(page.locator(".uml-lifecycle-link")).toHaveCount(0);
  await save(page);
  const lc = await lifecycleOf(page, "Order");
  // They were this class's states all along; what changed is that the enumeration no
  // longer supplies them. Emptying the machine would lose work nobody asked to lose.
  expect(lc.statesFrom).toBeUndefined();
  expect(lc.states.map((s) => s.name)).toEqual(["draft", "approved"]);
  expect(page.__errors).toEqual([]);
});

test("renaming the enumeration carries the reference with it", async ({ page }) => {
  await sourceOrderFrom(page);
  await box(page, "OrderStatus").click();
  await page.locator("#im-c-name").fill("Lebenszustand");
  await page.locator("#im-c-name").blur();

  // The line survives, because a reference by name has to follow the name.
  await expect(page.locator(".uml-lifecycle-link")).toHaveCount(1);
  await save(page);
  const lc = await lifecycleOf(page, "Order");
  expect(lc.statesFrom).toBe("Lebenszustand");
  expect(page.__errors).toEqual([]);
});
